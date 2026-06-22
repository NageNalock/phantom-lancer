package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	imagegen "phantom-lancer/internal/images"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

const (
	maxGenerationsBodyBytes = 4 << 20
	maxProvidersBodyBytes   = 64 << 10
)

func (s *Server) handleImagesProviders(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	_ = ctx
	status := s.images.ProvidersStatus(r.Context())
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleImagesProviderSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/images/providers/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider 不存在")
		return
	}
	providerStr := parts[0]
	provider := imagegen.NormalizeProvider(providerStr)
	if err := imagegen.ValidateProvider(provider); err != nil {
		writeError(w, http.StatusBadRequest, "provider_invalid", err.Error())
		return
	}
	sub := ""
	if len(parts) >= 2 {
		sub = parts[1]
	}
	switch r.Method {
	case http.MethodGet:
		settings, err := s.images.GetMediaProviderSettings(r.Context(), provider)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "provider_settings_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": settings})
	case http.MethodPut:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxProvidersBodyBytes)
		var req struct {
			Enabled            *bool          `json:"enabled"`
			APIKey             string         `json:"apiKey"`
			ClearAPIKey        bool           `json:"clearApiKey"`
			UpdateAPIKey       bool           `json:"updateApiKey"`
			DefaultImageModel  string         `json:"defaultImageModel"`
			DefaultVideoModel  string         `json:"defaultVideoModel"`
			DefaultImageParams map[string]any `json:"defaultImageParams"`
			DefaultVideoParams map[string]any `json:"defaultVideoParams"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		existing, err := s.images.GetMediaProviderSettings(r.Context(), provider)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "provider_settings_read_failed", err.Error())
			return
		}
		settings := existing
		if req.Enabled != nil {
			settings.Enabled = *req.Enabled
		}
		if req.DefaultImageModel != "" {
			settings.DefaultImageModel = req.DefaultImageModel
		}
		if req.DefaultVideoModel != "" {
			settings.DefaultVideoModel = req.DefaultVideoModel
		}
		if req.DefaultImageParams != nil {
			settings.DefaultImageParams = req.DefaultImageParams
		}
		if req.DefaultVideoParams != nil {
			settings.DefaultVideoParams = req.DefaultVideoParams
		}
		if req.APIKey != "" {
			settings.APIKey = req.APIKey
			req.UpdateAPIKey = true
		}
		updated, err := s.images.UpdateMediaProviderSettings(r.Context(), settings, req.UpdateAPIKey, req.ClearAPIKey)
		if err != nil {
			writeError(w, http.StatusBadRequest, "provider_settings_update_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "images.provider.settings.updated",
			RiskLevel: "medium",
			Summary:   "Images Provider 设置已更新",
			Payload: map[string]any{
				"provider":          string(provider),
				"enabled":           updated.Enabled,
				"hasApiKey":         updated.HasAPIKey,
				"defaultImageModel": updated.DefaultImageModel,
				"defaultVideoModel": updated.DefaultVideoModel,
				"clearedApiKey":     req.ClearAPIKey,
				"updatedApiKey":     req.UpdateAPIKey,
			},
		})
		writeJSON(w, http.StatusOK, map[string]any{"provider": updated})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		if sub == "test" {
			if err := s.images.TestMediaProvider(r.Context(), provider); err != nil {
				writeError(w, http.StatusBadRequest, "provider_test_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "provider": string(provider)})
			return
		}
		writeError(w, http.StatusNotFound, "provider_action_not_found", "操作不存在")
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	}
}

type mediaGenerationRequest struct {
	MediaType   string                  `json:"mediaType"`
	Provider    string                  `json:"provider"`
	Mode        string                  `json:"mode"`
	Model       string                  `json:"model"`
	Prompt      string                  `json:"prompt"`
	Parameters  map[string]any          `json:"parameters"`
	N           int                     `json:"n"`
	Size        string                  `json:"size"`
	AspectRatio string                  `json:"aspectRatio"`
	Width       int                     `json:"width"`
	Height      int                     `json:"height"`
	NumFrames   int                     `json:"numFrames"`
	FrameRate   int                     `json:"frameRate"`
	Seed        int                     `json:"seed"`
	Sources     []mediaGenerationSource `json:"sources"`
}

type mediaGenerationSource struct {
	Type     string `json:"type"`
	URL      string `json:"url"`
	AssetID  string `json:"assetId"`
	MimeType string `json:"mimeType"`
	Label    string `json:"label"`
}

func (s *Server) handleCreateMediaGeneration(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		s.handleCreateMediaGenerationMultipart(w, r, ctx)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxGenerationsBodyBytes)
	var req mediaGenerationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.createMediaGeneration(w, r, ctx, req)
}

func (s *Server) handleCreateMediaGenerationMultipart(w http.ResponseWriter, r *http.Request, ctx sessionContext) {
	if err := r.ParseMultipartForm(int64(imagegen.MaxFormBytes)); err != nil {
		writeError(w, http.StatusBadRequest, "multipart_parse_failed", err.Error())
		return
	}
	form := r.Form
	req := mediaGenerationRequest{
		MediaType:   formValue(form, "media_type", "image"),
		Provider:    formValue(form, "provider", "agnes"),
		Mode:        formValue(form, "mode", formValue(form, "video_mode", imagegen.ModeTextToImage)),
		Model:       formValue(form, "model", ""),
		Prompt:      formValue(form, "prompt", ""),
		N:           formInt(form, "n", 1),
		Size:        formValue(form, "size", ""),
		AspectRatio: formValue(form, "aspect_ratio", ""),
		Width:       formInt(form, "width", 0),
		Height:      formInt(form, "height", 0),
		NumFrames:   formInt(form, "num_frames", 0),
		FrameRate:   formInt(form, "frame_rate", 0),
		Seed:        formInt(form, "seed", 0),
	}
	if req.FrameRate <= 0 {
		req.FrameRate = formInt(form, "fps", 0)
	}
	if req.NumFrames <= 0 {
		req.NumFrames = numFramesFromDuration(formValue(form, "duration", ""))
	}
	provider := imagegen.NormalizeProvider(req.Provider)
	mediaType := imagegen.NormalizeMediaType(req.MediaType)
	inputs, err := imagegen.ParseMediaMultipartRequest(r, req.MediaType, req.Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart_images_parse_failed", err.Error())
		return
	}
	req.Sources = make([]mediaGenerationSource, 0, len(inputs.Images))
	for _, img := range inputs.Images {
		req.Sources = append(req.Sources, mediaGenerationSource{
			Type:     img.SourceType,
			URL:      img.URL,
			MimeType: img.MimeType,
			Label:    img.SourceLabel,
		})
	}
	if provider == imagegen.ProviderXAI && mediaType == imagegen.MediaTypeImage {
		if err := s.requireUnlockedForPrivateMediaSources(r, ctx, req.Sources); err != nil {
			if strings.Contains(err.Error(), "锁定") {
				writeError(w, http.StatusForbidden, "private_asset_locked", err.Error())
			} else {
				writeError(w, http.StatusBadRequest, "media_source_invalid", err.Error())
			}
			return
		}
		job, err := s.images.CreateJob(r.Context(), imagegen.ImagineRequest{
			Provider:       provider,
			Mode:           req.Mode,
			Prompt:         req.Prompt,
			Model:          req.Model,
			AspectRatio:    formValue(form, "aspect_ratio", ""),
			Resolution:     formValue(form, "resolution", ""),
			ResponseFormat: formValue(form, "response_format", "url"),
			N:              req.N,
			Images:         inputs.Images,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "media_generation_create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "jobType": "legacy_image"})
		return
	}
	s.createMediaGeneration(w, r, ctx, req)
}

func formValue(form map[string][]string, key, def string) string {
	if vals, ok := form[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return def
}

func formInt(form map[string][]string, key string, def int) int {
	raw := formValue(form, key, "")
	if raw == "" {
		return def
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return def
}

func numFramesFromDuration(raw string) int {
	switch strings.TrimSpace(raw) {
	case "3s":
		return 81
	case "5s":
		return 121
	case "10s":
		return 241
	case "18s":
		return 441
	default:
		return 0
	}
}

func videoSizeFromAspectRatio(raw string) (int, int) {
	switch strings.TrimSpace(raw) {
	case "16:9":
		return 1024, 576
	case "9:16":
		return 576, 1024
	case "2:3", "3:4":
		return 768, 1152
	case "3:2", "4:3":
		return 1152, 768
	default:
		return 1152, 768
	}
}

func (s *Server) createMediaGeneration(w http.ResponseWriter, r *http.Request, ctx sessionContext, req mediaGenerationRequest) {
	provider := imagegen.NormalizeProvider(req.Provider)
	mediaType := imagegen.NormalizeMediaType(req.MediaType)
	if err := imagegen.ValidateProvider(provider); err != nil {
		writeError(w, http.StatusBadRequest, "provider_invalid", err.Error())
		return
	}
	if err := imagegen.ValidateMediaType(mediaType); err != nil {
		writeError(w, http.StatusBadRequest, "media_type_invalid", err.Error())
		return
	}
	images := make([]imagegen.ImageInput, 0, len(req.Sources))
	for _, src := range req.Sources {
		img := imagegen.ImageInput{
			SourceType:  src.Type,
			SourceLabel: src.Label,
			MimeType:    src.MimeType,
			URLRedacted: src.Label,
		}
		switch src.Type {
		case "library_asset", "asset":
			if src.AssetID != "" {
				img.URL = "asset:" + src.AssetID
				img.SourceType = "library_asset"
				img.URLRedacted = src.AssetID
			} else if strings.HasPrefix(src.URL, "asset:") {
				img.URL = src.URL
				img.SourceType = "library_asset"
				img.URLRedacted = strings.TrimPrefix(src.URL, "asset:")
			} else if strings.TrimSpace(src.URL) != "" {
				img.URL = "asset:" + strings.TrimSpace(src.URL)
				img.SourceType = "library_asset"
				img.URLRedacted = strings.TrimSpace(src.URL)
			}
		case "url":
			img.URL = src.URL
			if img.URLRedacted == "" {
				img.URLRedacted = redactURLLabel(src.URL)
			}
		case "upload", "data":
			img.URL = src.URL
		default:
			img.URL = src.URL
			if img.URLRedacted == "" {
				img.URLRedacted = redactURLLabel(src.URL)
			}
		}
		images = append(images, img)
	}
	if err := s.requireUnlockedForPrivateMediaSources(r, ctx, req.Sources); err != nil {
		if strings.Contains(err.Error(), "锁定") {
			writeError(w, http.StatusForbidden, "private_asset_locked", err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "media_source_invalid", err.Error())
		}
		return
	}
	switch mediaType {
	case imagegen.MediaTypeImage:
		imgReq := imagegen.ImagineRequest{
			Provider:       provider,
			Mode:           req.Mode,
			Prompt:         req.Prompt,
			Model:          req.Model,
			Size:           req.Size,
			Width:          req.Width,
			Height:         req.Height,
			AspectRatio:    req.AspectRatio,
			ResponseFormat: "url",
			N:              req.N,
			Images:         images,
		}
		if req.Parameters != nil {
			if sz, ok := req.Parameters["size"].(string); ok && sz != "" {
				imgReq.Size = sz
			}
			if rf, ok := req.Parameters["responseFormat"].(string); ok && rf != "" {
				imgReq.ResponseFormat = rf
			}
			if ar, ok := req.Parameters["aspectRatio"].(string); ok {
				imgReq.AspectRatio = ar
			}
			if res, ok := req.Parameters["resolution"].(string); ok {
				imgReq.Resolution = res
			}
		}
		if provider == imagegen.ProviderXAI {
			job, err := s.images.CreateJob(r.Context(), imgReq)
			if err != nil {
				writeError(w, http.StatusBadRequest, "image_generation_create_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "jobType": "legacy_image"})
			return
		}
		job, err := s.images.CreateMediaJob(r.Context(), imagegen.MediaTypeImage, imgReq)
		if err != nil {
			writeError(w, http.StatusBadRequest, "image_generation_create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "jobType": "media_image"})

	case imagegen.MediaTypeVideo:
		if provider != imagegen.ProviderAgnes {
			writeError(w, http.StatusBadRequest, "video_provider_unsupported", "当前仅 Agnes 支持视频生成")
			return
		}
		width := req.Width
		height := req.Height
		if width <= 0 || height <= 0 {
			ratio := req.AspectRatio
			if req.Parameters != nil {
				if ar, ok := req.Parameters["aspectRatio"].(string); ok && ar != "" {
					ratio = ar
				}
			}
			width, height = videoSizeFromAspectRatio(ratio)
		}
		vidReq := imagegen.VideoRequest{
			Provider: provider,
			Mode:     req.Mode,
			Prompt:   req.Prompt,
			Model:    req.Model,
			Parameters: imagegen.VideoParameters{
				Width:     width,
				Height:    height,
				NumFrames: req.NumFrames,
				FrameRate: req.FrameRate,
				Seed:      req.Seed,
			},
			Images: images,
		}
		if req.Parameters != nil {
			if wv, ok := req.Parameters["width"].(float64); ok {
				vidReq.Parameters.Width = int(wv)
			}
			if hv, ok := req.Parameters["height"].(float64); ok {
				vidReq.Parameters.Height = int(hv)
			}
			if nv, ok := req.Parameters["numFrames"].(float64); ok {
				vidReq.Parameters.NumFrames = int(nv)
			}
			if fv, ok := req.Parameters["frameRate"].(float64); ok {
				vidReq.Parameters.FrameRate = int(fv)
			}
			if sv, ok := req.Parameters["seed"].(float64); ok {
				vidReq.Parameters.Seed = int(sv)
			}
		}
		job, err := s.images.CreateMediaJob(r.Context(), imagegen.MediaTypeVideo, vidReq)
		if err != nil {
			writeError(w, http.StatusBadRequest, "video_generation_create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "jobType": "media_video"})

	default:
		writeError(w, http.StatusBadRequest, "media_type_unsupported", "不支持的媒体类型")
	}
}

func (s *Server) handleListMediaGenerations(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	_ = ctx
	q := r.URL.Query()
	limit, page, offset := paginationParams(q, 48, 200)
	mediaType := q.Get("mediaType")
	provider := q.Get("provider")
	status := q.Get("status")
	mode := q.Get("mode")
	model := q.Get("model")
	includeLegacy := q.Get("includeLegacy") != "false"

	var jobs []storage.MediaGenerationJob
	var legacyJobs []storage.ImageGenerationJob
	var mediaTotal int
	var legacyTotal int
	var err error
	jobs, mediaTotal, err = s.images.ListMediaJobsPage(r.Context(), limit, offset, mediaType, provider, status, mode, model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "media_generations_list_failed", err.Error())
		return
	}
	if includeLegacy && (mediaType == "" || mediaType == string(imagegen.MediaTypeImage)) {
		legacyJobs, legacyTotal, err = s.store.ListImageGenerationJobsPage(r.Context(), limit, offset, status, mode)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "legacy_image_jobs_list_failed", err.Error())
			return
		}
	}
	count := len(jobs) + len(legacyJobs)
	total := mediaTotal + legacyTotal
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       jobs,
		"legacyItems": legacyJobs,
		"count":       count,
		"total":       total,
		"mediaTotal":  mediaTotal,
		"legacyTotal": legacyTotal,
		"page":        page,
		"pageSize":    limit,
		"offset":      offset,
		"hasNext":     offset+len(jobs) < mediaTotal || offset+len(legacyJobs) < legacyTotal,
	})
}

func (s *Server) handleMediaGenerationSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/images/generations/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "generation_job_not_found", "Job 不存在")
		return
	}
	id := parts[0]
	action := ""
	if len(parts) >= 2 {
		action = parts[1]
	}
	switch r.Method {
	case http.MethodGet:
		job, err := s.images.GetMediaJob(r.Context(), id)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"job": job, "jobType": "media"})
			return
		}
		if !errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "generation_job_read_failed", err.Error())
			return
		}
		legacy, legErr := s.store.GetImageGenerationJob(r.Context(), id)
		if legErr != nil {
			writeError(w, http.StatusNotFound, "generation_job_not_found", "Job 不存在")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": legacy, "jobType": "legacy_image"})

	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		switch action {
		case "cancel":
			if err := s.images.CancelMediaJob(r.Context(), id); err != nil {
				writeError(w, http.StatusBadRequest, "generation_cancel_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true, "jobId": id})
		case "retry":
			job, err := s.images.GetMediaJob(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusBadRequest, "generation_retry_failed", "无法找到原 job")
				return
			}
			req := mediaGenerationRequest{
				MediaType: job.MediaType,
				Provider:  job.Provider,
				Mode:      job.Mode,
				Model:     job.Model,
				Prompt:    job.Prompt,
				Sources:   mediaGenerationSourcesForRetry(job.Sources),
			}
			if job.SourceCount > 0 && len(req.Sources) != job.SourceCount {
				writeError(w, http.StatusBadRequest, "generation_retry_sources_unavailable", "原 job 的参考图没有完整保存在图库中，请重新选择参考图后再生成")
				return
			}
			if p := job.Parameters; p != nil {
				req.Parameters = p
				if sz, ok := p["size"].(string); ok {
					req.Size = sz
				}
				if wv, ok := p["width"].(float64); ok {
					req.Width = int(wv)
				}
				if hv, ok := p["height"].(float64); ok {
					req.Height = int(hv)
				}
				if nv, ok := p["numFrames"].(float64); ok {
					req.NumFrames = int(nv)
				}
				if fv, ok := p["frameRate"].(float64); ok {
					req.FrameRate = int(fv)
				}
				if nv, ok := p["n"].(float64); ok {
					req.N = int(nv)
				}
			}
			s.createMediaGeneration(w, r, ctx, req)
		default:
			writeError(w, http.StatusNotFound, "generation_action_not_found", "操作不存在")
		}
	case http.MethodDelete:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		job, err := s.images.GetMediaJob(r.Context(), id)
		if err == nil {
			if mediaGenerationStatusActive(job.Status) {
				writeError(w, http.StatusConflict, "generation_delete_active", "运行中的任务请先取消后再删除")
				return
			}
			if err := s.store.DeleteMediaGenerationJob(r.Context(), id); err != nil {
				writeError(w, http.StatusInternalServerError, "generation_delete_failed", err.Error())
				return
			}
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "media.job.deleted",
				RiskLevel: "medium",
				Summary:   "Media generation 历史记录已删除",
				Payload: map[string]any{
					"jobId":       job.ID,
					"mediaType":   job.MediaType,
					"provider":    job.Provider,
					"status":      job.Status,
					"sourceCount": job.SourceCount,
					"outputCount": len(job.Outputs),
				},
			})
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "jobId": id, "jobType": "media"})
			return
		}
		if !errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "generation_job_read_failed", err.Error())
			return
		}
		legacy, err := s.store.GetImageGenerationJob(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "generation_job_not_found", "Job 不存在")
			return
		}
		if imageGenerationStatusActive(legacy.Status) {
			writeError(w, http.StatusConflict, "generation_delete_active", "运行中的任务请先取消后再删除")
			return
		}
		if err := s.store.DeleteImageGenerationJob(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "generation_delete_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "images.job.deleted",
			RiskLevel: "medium",
			Summary:   "Images generation 历史记录已删除",
			Payload: map[string]any{
				"jobId":       legacy.ID,
				"provider":    legacy.Provider,
				"status":      legacy.Status,
				"sourceCount": legacy.SourceCount,
				"outputCount": len(legacy.Outputs),
			},
		})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "jobId": id, "jobType": "legacy_image"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	}
}

func mediaGenerationStatusActive(status string) bool {
	switch strings.TrimSpace(status) {
	case "queued", "running", "provider_queued":
		return true
	default:
		return false
	}
}

func imageGenerationStatusActive(status string) bool {
	switch strings.TrimSpace(status) {
	case "queued", "running":
		return true
	default:
		return false
	}
}

func mediaGenerationSourcesForRetry(sources []storage.MediaGenerationSource) []mediaGenerationSource {
	out := make([]mediaGenerationSource, 0, len(sources))
	for _, src := range sources {
		if strings.TrimSpace(src.AssetID) == "" {
			continue
		}
		out = append(out, mediaGenerationSource{
			Type:     "library_asset",
			URL:      "asset:" + strings.TrimSpace(src.AssetID),
			AssetID:  strings.TrimSpace(src.AssetID),
			MimeType: src.MimeType,
			Label:    src.SourceLabel,
		})
	}
	return out
}

func (s *Server) handleListMediaAssets(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, page, offset := paginationParams(q, 48, 200)
	mediaType := q.Get("mediaType")
	provider := q.Get("provider")
	assetType := q.Get("assetType")
	status := q.Get("status")
	scope := q.Get("scope")
	privacy := "public"
	if scope == "private" || scope == "all" {
		unlocked, _ := s.privateImages.IsUnlocked(ctx.Session.ID, time.Now())
		if !unlocked {
			writeError(w, http.StatusForbidden, "private_asset_locked", "私密资产已锁定，请先解锁")
			return
		}
		privacy = scope
	}
	assets, total, err := s.images.ListMediaAssetsPage(r.Context(), limit, offset, mediaType, provider, assetType, status, privacy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "media_assets_list_failed", err.Error())
		return
	}
	count := len(assets)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    assets,
		"count":    count,
		"total":    total,
		"page":     page,
		"pageSize": limit,
		"offset":   offset,
		"hasNext":  offset+count < total,
	})
}

func (s *Server) handleMediaAssetSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/images/media-assets/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "media_asset_not_found", "Asset 不存在")
		return
	}
	id := parts[0]
	sub := ""
	if len(parts) >= 2 {
		sub = parts[1]
	}
	asset, err := s.images.GetMediaAsset(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "media_asset_not_found", "Asset 不存在")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return
		}
		switch sub {
		case "", "details":
			writeJSON(w, http.StatusOK, map[string]any{"asset": asset})
		case "content":
			mimeType, data, err := s.images.ReadMediaAsset(r.Context(), asset)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "media_asset_read_failed", err.Error())
				return
			}
			serveMediaAssetContent(w, r, asset, mimeType, data, false)
		case "download":
			mimeType, data, err := s.images.ReadMediaAsset(r.Context(), asset)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "media_asset_read_failed", err.Error())
				return
			}
			serveMediaAssetContent(w, r, asset, mimeType, data, true)
		default:
			writeError(w, http.StatusNotFound, "media_asset_action_not_found", "操作不存在")
		}
	case http.MethodPost:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		switch sub {
		case "private":
			if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
				return
			}
			var req struct {
				Private bool `json:"private"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
				return
			}
			updated, err := s.store.SetMediaAssetPrivate(r.Context(), id, req.Private)
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					writeError(w, http.StatusNotFound, "media_asset_not_found", "Asset 不存在")
				} else {
					writeError(w, http.StatusInternalServerError, "media_asset_private_update_failed", err.Error())
				}
				return
			}
			eventType := "media.asset.private.added"
			summary := "已加入媒体资源私密收藏夹"
			if !req.Private {
				eventType = "media.asset.private.removed"
				summary = "已移出媒体资源私密收藏夹"
			}
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: eventType,
				RiskLevel: "medium",
				Summary:   summary,
				Payload: map[string]any{
					"assetId": id,
					"jobId":   asset.JobID,
				},
			})
			writeJSON(w, http.StatusOK, map[string]any{"asset": updated})
		case "archive-s3":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
				return
			}
			if !s.requireCSRF(w, r, ctx.Session) {
				return
			}
			if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
				return
			}
			archived, err := s.images.ArchiveMediaAssetToS3(r.Context(), id)
			if err != nil {
				_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
					EventType: "media.asset.archive_failed",
					RiskLevel: "medium",
					Summary:   "媒体资产归档失败",
					Payload: map[string]any{
						"assetId": id,
						"jobId":   asset.JobID,
						"error":   safelog.Error(err, 200),
					},
				})
				writeError(w, http.StatusBadRequest, "media_asset_archive_failed", err.Error())
				return
			}
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "media.asset.archived.s3",
				RiskLevel: "medium",
				Summary:   "媒体资产已归档到对象存储",
				Payload: map[string]any{
					"assetId": archived.ID,
					"jobId":   archived.JobID,
					"bucket":  archived.S3Bucket,
				},
			})
			writeJSON(w, http.StatusOK, map[string]any{"asset": archived})
		default:
			writeError(w, http.StatusNotFound, "media_asset_action_not_found", "操作不存在")
		}
	case http.MethodDelete:
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		deleted, err := s.images.DeleteMediaAsset(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "media_asset_delete_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"asset": deleted})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	}
}

func serveMediaAssetContent(w http.ResponseWriter, r *http.Request, asset storage.MediaAsset, mimeType string, data []byte, download bool) {
	if mimeType == "" {
		mimeType = asset.MimeType
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if download {
		filename := asset.ID
		if asset.Extension != "" {
			filename += asset.Extension
		} else if asset.MediaType == string(imagegen.MediaTypeVideo) {
			filename += ".mp4"
		} else if asset.OriginalFilename != "" {
			filename = asset.OriginalFilename
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) requireUnlockedForPrivateMediaSources(r *http.Request, ctx sessionContext, sources []mediaGenerationSource) error {
	unlocked := false
	unlockedChecked := false
	requireUnlocked := func() error {
		if !unlockedChecked {
			unlocked, _ = s.privateImages.IsUnlocked(ctx.Session.ID, time.Now())
			unlockedChecked = true
		}
		if !unlocked {
			return errors.New("私密资产已锁定，请先解锁")
		}
		return nil
	}
	checkMedia := func(assetID string) (bool, error) {
		mediaAsset, err := s.images.GetMediaAsset(r.Context(), assetID)
		if err != nil {
			if isMediaAssetNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if mediaAsset.MediaType != string(imagegen.MediaTypeImage) {
			return true, errors.New("只能使用图片资产作为参考图")
		}
		if mediaAsset.Private {
			if err := requireUnlocked(); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	checkLegacy := func(assetID string) (bool, error) {
		imageAsset, err := s.store.GetImageAsset(r.Context(), assetID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if imageAsset.Status == "deleted" {
			return true, errors.New("图片资产已删除")
		}
		if imageAsset.Private {
			if err := requireUnlocked(); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	for _, src := range sources {
		kind, assetID := mediaSourceAssetRef(src)
		if assetID == "" {
			continue
		}
		found := false
		var err error
		switch kind {
		case "media":
			found, err = checkMedia(assetID)
			if err != nil {
				return err
			}
			if !found {
				found, err = checkLegacy(assetID)
			}
		case "legacy":
			found, err = checkLegacy(assetID)
			if err != nil {
				return err
			}
			if !found {
				found, err = checkMedia(assetID)
			}
		default:
			found, err = checkMedia(assetID)
			if err != nil {
				return err
			}
			if !found {
				found, err = checkLegacy(assetID)
			}
		}
		if err != nil {
			return err
		}
		if !found {
			return errors.New("未找到图片资产")
		}
	}
	return nil
}

func mediaSourceAssetRef(src mediaGenerationSource) (string, string) {
	raw := strings.TrimSpace(src.AssetID)
	if raw == "" {
		urlValue := strings.TrimSpace(src.URL)
		if strings.HasPrefix(urlValue, "asset:") {
			raw = strings.TrimSpace(strings.TrimPrefix(urlValue, "asset:"))
		} else if src.Type == "asset" || src.Type == "library_asset" {
			raw = urlValue
		}
	}
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "asset:") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "asset:"))
	}
	if idx := strings.Index(raw, ":"); idx > 0 {
		prefix := raw[:idx]
		if prefix == "legacy" || prefix == "media" {
			return prefix, strings.TrimSpace(raw[idx+1:])
		}
	}
	return "", raw
}

func isMediaAssetNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, storage.ErrNotFound) || strings.Contains(err.Error(), "media_asset_not_found")
}

func intParam(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func paginationParams(q url.Values, def, max int) (int, int, int) {
	limit := intParam(q.Get("pageSize"), 0)
	if limit <= 0 {
		limit = intParam(q.Get("page_size"), 0)
	}
	if limit <= 0 {
		limit = intParam(q.Get("limit"), def)
	}
	if limit <= 0 {
		limit = def
	}
	if max > 0 && limit > max {
		limit = max
	}
	page := intParam(q.Get("page"), 1)
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		offset = intParam(raw, 0)
		if offset < 0 {
			offset = 0
		}
		if q.Get("page") == "" && limit > 0 {
			page = offset/limit + 1
		}
	}
	return limit, page, offset
}

func redactURLLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "data:image/") {
		return "data:image/[redacted]"
	}
	if strings.HasPrefix(raw, "data:video/") {
		return "data:video/[redacted]"
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		raw = parsed.String()
	} else {
		if idx := strings.IndexAny(raw, "?#"); idx >= 0 {
			raw = raw[:idx]
		}
	}
	if len(raw) <= 140 {
		return raw
	}
	return raw[:100] + "..." + raw[len(raw)-24:]
}
