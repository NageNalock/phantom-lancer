package images

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/logsampler"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

const (
	eventScope = "image_job"
)

type Service struct {
	Store  *storage.Store
	Hub    *events.Hub
	Assets *AssetStore
	XAI    *XAIClient
	Agnes  *AgnesClient
	Log    *slog.Logger

	videoPoller *videoPollSupervisor

	LogSampler *logsampler.Sampler
}

func NewService(store *storage.Store, hub *events.Hub, dataDir string, logger *slog.Logger) *Service {
	svc := &Service{
		Store:      store,
		Hub:        hub,
		Assets:     NewAssetStore(filepath.Join(dataDir, "images", "generated"), nil),
		XAI:        NewXAIClient("https://api.x.ai/v1", nil),
		Agnes:      NewAgnesClient(agnesBaseURL, nil),
		Log:        logger,
		LogSampler: logsampler.New(5 * time.Second),
	}
	svc.videoPoller = newVideoPollSupervisor(svc)
	return svc
}

func (s *Service) Ensure(ctx context.Context) error {
	if err := s.Store.EnsureImageProviderSettings(ctx); err != nil {
		return err
	}
	if err := s.Store.EnsureImageStorageSettings(ctx); err != nil {
		return err
	}
	if err := s.Store.BackfillImageAssets(ctx); err != nil {
		return err
	}
	for _, provider := range []string{string(ProviderXAI), string(ProviderAgnes)} {
		if err := s.Store.EnsureMediaProviderSettings(ctx, provider); err != nil {
			return err
		}
	}
	if err := s.backfillXAItoMediaSettings(ctx); err != nil {
		if s.Log != nil {
			s.Log.Warn("backfill xai settings to media_provider_settings failed", "error", safelog.Error(err, 200))
		}
	}
	ids, err := s.Store.InterruptStaleImageGenerationJobs(ctx, "服务重启后中断未完成的 Images job")
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.append(ctx, id, "images.job.interrupted", map[string]any{"reason": "service restarted"})
	}
	if len(ids) > 0 {
		_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
			EventType: "images.jobs.interrupted",
			RiskLevel: "medium",
			Summary:   "服务启动时中断未完成 Images job",
			Payload:   map[string]any{"count": len(ids)},
		})
	}
	mediaIDs, err := s.Store.InterruptStaleMediaJobs(ctx, "服务重启后中断未完成的 Media job")
	if err != nil && s.Log != nil {
		s.Log.Warn("interrupt stale media jobs failed", "error", safelog.Error(err, 200))
	}
	for _, id := range mediaIDs {
		s.appendMedia(ctx, id, "media.job.interrupted", map[string]any{"reason": "service restarted"})
	}
	if s.videoPoller != nil {
		go s.videoPoller.Start(context.Background())
	}
	return nil
}

func (s *Service) Status(ctx context.Context) Status {
	settings, err := s.Store.GetImageProviderSettings(ctx)
	if err != nil {
		return Status{Available: true, Provider: "xai", LastError: err.Error()}
	}
	count, countErr := s.Store.CountImageGenerationJobs(ctx)
	jobs, jobsErr := s.Store.ListImageGenerationJobs(ctx, 1, "", "")
	status := Status{
		Available:    true,
		Provider:     settings.Provider,
		HasAPIKey:    settings.HasAPIKey,
		MaskedAPIKey: settings.MaskedAPIKey,
		DefaultModel: settings.DefaultModel,
		HistoryCount: count,
	}
	if countErr != nil {
		status.LastError = statusError(countErr)
	}
	if jobsErr != nil {
		status.LastError = statusError(jobsErr)
		return status
	}
	if len(jobs) > 0 {
		status.LastJobID = jobs[0].ID
		status.LastJobStatus = jobs[0].Status
		if status.LastError == "" {
			status.LastError = jobs[0].ErrorMessage
		}
		status.LastCompletedAt = jobs[0].CompletedAt
	}
	return status
}

func statusError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	runes := []rune(message)
	if len(runes) > 240 {
		return string(runes[:240]) + "..."
	}
	return message
}

func (s *Service) UpdateSettings(ctx context.Context, settings storage.ImageProviderSettings, updateAPIKey bool, clearAPIKey bool) (storage.ImageProviderSettings, error) {
	settings.DefaultModel = strings.TrimSpace(settings.DefaultModel)
	if settings.DefaultModel == "" {
		settings.DefaultModel = "grok-imagine-image-quality"
	}
	if !modelNamePattern.MatchString(settings.DefaultModel) {
		return storage.ImageProviderSettings{}, errors.New("model name is invalid")
	}
	if err := SettingSupported("response_format", strings.TrimSpace(settings.DefaultResponseFormat)); err != nil {
		return storage.ImageProviderSettings{}, err
	}
	if err := SettingSupported("resolution", strings.TrimSpace(settings.DefaultResolution)); err != nil {
		return storage.ImageProviderSettings{}, err
	}
	if err := SettingSupported("aspect_ratio", strings.TrimSpace(settings.DefaultAspectRatio)); err != nil {
		return storage.ImageProviderSettings{}, err
	}
	updated, err := s.Store.UpdateImageProviderSettings(ctx, settings, updateAPIKey, clearAPIKey)
	if err != nil {
		return storage.ImageProviderSettings{}, err
	}
	if syncErr := s.syncLegacyXAISettingsToMedia(ctx, updated, updateAPIKey, clearAPIKey); syncErr != nil && s.Log != nil {
		s.Log.Warn("sync legacy xai settings to media provider failed", "error", safelog.Error(syncErr, 200))
	}
	return updated, nil
}

func (s *Service) ListPrompts(ctx context.Context, limit int, q, mode, status string) ([]storage.ImagePrompt, error) {
	return s.Store.ListImagePrompts(ctx, limit, q, mode, status)
}

func (s *Service) CreatePrompt(ctx context.Context, prompt storage.ImagePrompt) (storage.ImagePrompt, error) {
	prompt = storage.NormalizeImagePrompt(prompt)
	if err := ValidatePrompt(prompt); err != nil {
		return storage.ImagePrompt{}, err
	}
	return s.Store.CreateImagePrompt(ctx, prompt)
}

func (s *Service) UpdatePrompt(ctx context.Context, id string, prompt storage.ImagePrompt) (storage.ImagePrompt, error) {
	prompt = storage.NormalizeImagePrompt(prompt)
	if err := ValidatePrompt(prompt); err != nil {
		return storage.ImagePrompt{}, err
	}
	return s.Store.UpdateImagePrompt(ctx, id, prompt)
}

func (s *Service) DeletePrompt(ctx context.Context, id string) (storage.ImagePrompt, error) {
	return s.Store.DeleteImagePrompt(ctx, id)
}

func (s *Service) UsePrompt(ctx context.Context, id string) (storage.ImagePrompt, error) {
	return s.Store.UseImagePrompt(ctx, id)
}

func (s *Service) CreateJob(ctx context.Context, request ImagineRequest) (storage.ImageGenerationJob, error) {
	if request.Provider == "" {
		request.Provider = ProviderXAI
	}
	if request.Provider == ProviderAgnes {
		return storage.ImageGenerationJob{}, errors.New("Agnes provider requires CreateMediaJob for image generation")
	}
	request = NormalizeRequest(request)
	storageSettings, err := s.Store.GetImageStorageSettings(ctx)
	if err != nil {
		return storage.ImageGenerationJob{}, err
	}
	request.ResponseFormat = responseFormatForStorage(request.ResponseFormat, storageSettings)
	if err := ValidateRequest(request); err != nil {
		return storage.ImageGenerationJob{}, err
	}
	endpoint, _, _ := RequestPayload(request)
	job, err := s.Store.CreateImageGenerationJob(ctx, storage.ImageGenerationJob{
		Provider:       "xai",
		Status:         "queued",
		Mode:           request.Mode,
		ModeLabel:      ModeLabel(request.Mode),
		Model:          request.Model,
		Endpoint:       endpoint,
		Prompt:         request.Prompt,
		AspectRatio:    request.AspectRatio,
		Resolution:     request.Resolution,
		ResponseFormat: request.ResponseFormat,
		ImageCount:     request.N,
		Usage:          map[string]any{},
	}, storageSources(request.Images))
	if err != nil {
		return storage.ImageGenerationJob{}, err
	}
	s.storeSourceAssets(ctx, &job, request)
	s.linkLibrarySourceAssets(ctx, &job, request)
	s.append(ctx, job.ID, "images.job.created", map[string]any{"mode": job.Mode, "model": job.Model, "sourceCount": job.SourceCount, "imageCount": job.ImageCount})
	s.append(ctx, job.ID, "images.job.queued", map[string]any{"mode": job.Mode, "model": job.Model})

	go s.runJob(context.Background(), job, request)
	return job, nil
}

func (s *Service) backfillXAItoMediaSettings(ctx context.Context) error {
	legacy, err := s.Store.GetImageProviderSettings(ctx)
	if err != nil {
		return err
	}
	existing, err := s.Store.GetMediaProviderSettings(ctx, string(ProviderXAI))
	if err != nil {
		return err
	}
	if existing.HasAPIKey && existing.DefaultImageModel != "" {
		return nil
	}
	updated := existing
	updated.Enabled = true
	if legacy.HasAPIKey {
		updated.APIKey = legacy.XAIAPIKey
	}
	if legacy.DefaultModel != "" {
		updated.DefaultImageModel = legacy.DefaultModel
	}
	if len(legacy.DefaultResponseFormat) > 0 || len(legacy.DefaultResolution) > 0 || len(legacy.DefaultAspectRatio) > 0 {
		params := map[string]any{}
		if legacy.DefaultResponseFormat != "" {
			params["responseFormat"] = legacy.DefaultResponseFormat
		}
		if legacy.DefaultResolution != "" {
			params["resolution"] = legacy.DefaultResolution
		}
		if legacy.DefaultAspectRatio != "" {
			params["aspectRatio"] = legacy.DefaultAspectRatio
		}
		updated.DefaultImageParams = params
	}
	_, err = s.Store.UpdateMediaProviderSettings(ctx, updated, updated.APIKey != existing.APIKey && legacy.HasAPIKey, false)
	return err
}

func (s *Service) syncLegacyXAISettingsToMedia(ctx context.Context, legacy storage.ImageProviderSettings, updateAPIKey, clearAPIKey bool) error {
	media, err := s.Store.GetMediaProviderSettings(ctx, string(ProviderXAI))
	if err != nil {
		return err
	}
	media.Provider = string(ProviderXAI)
	media.Enabled = true
	media.DefaultImageModel = legacy.DefaultModel
	params := map[string]any{}
	if legacy.DefaultResponseFormat != "" {
		params["responseFormat"] = legacy.DefaultResponseFormat
	}
	if legacy.DefaultResolution != "" {
		params["resolution"] = legacy.DefaultResolution
	}
	if legacy.DefaultAspectRatio != "" {
		params["aspectRatio"] = legacy.DefaultAspectRatio
	}
	media.DefaultImageParams = params
	if updateAPIKey {
		media.APIKey = legacy.XAIAPIKey
	}
	if clearAPIKey {
		media.APIKey = ""
	}
	_, err = s.Store.UpdateMediaProviderSettings(ctx, media, updateAPIKey || clearAPIKey, clearAPIKey)
	return err
}

func (s *Service) ProvidersStatus(ctx context.Context) ProvidersStatus {
	settings, err := s.Store.ListMediaProviderSettings(ctx)
	if err != nil {
		return ProvidersStatus{
			Providers:  []ProviderStatus{},
			Models:     ListModelCapabilities(false),
			DefaultXAI: string(ProviderXAI),
		}
	}
	statuses := make([]ProviderStatus, 0, len(settings))
	for _, ps := range settings {
		prov := NormalizeProvider(ps.Provider)
		if err := ValidateProvider(prov); err != nil {
			continue
		}
		imageCount, _ := s.Store.CountMediaGenerationJobs(ctx, string(MediaTypeImage), ps.Provider)
		videoCount, _ := s.Store.CountMediaGenerationJobs(ctx, string(MediaTypeVideo), ps.Provider)
		legacyCount := 0
		if prov == ProviderXAI {
			legacyCount, _ = s.Store.CountImageGenerationJobs(ctx)
		}
		statuses = append(statuses, ProviderStatus{
			Provider:          prov,
			Enabled:           ps.Enabled,
			HasAPIKey:         ps.HasAPIKey,
			MaskedAPIKey:      ps.MaskedAPIKey,
			DefaultImageModel: ps.DefaultImageModel,
			DefaultVideoModel: ps.DefaultVideoModel,
			LastTestedAt:      ps.LastTestedAt,
			LastError:         ps.LastError,
			ImageJobCount:     imageCount + legacyCount,
			VideoJobCount:     videoCount,
		})
	}
	return ProvidersStatus{
		Providers:  statuses,
		Models:     ListModelCapabilities(false),
		DefaultXAI: string(ProviderXAI),
	}
}

func (s *Service) GetMediaProviderSettings(ctx context.Context, provider ProviderID) (storage.MediaProviderSettings, error) {
	return s.Store.GetMediaProviderSettings(ctx, string(provider))
}

func (s *Service) UpdateMediaProviderSettings(ctx context.Context, settings storage.MediaProviderSettings, updateAPIKey, clearAPIKey bool) (storage.MediaProviderSettings, error) {
	prov := NormalizeProvider(settings.Provider)
	if err := ValidateProvider(prov); err != nil {
		return storage.MediaProviderSettings{}, err
	}
	settings.Provider = string(prov)
	return s.Store.UpdateMediaProviderSettings(ctx, settings, updateAPIKey, clearAPIKey)
}

func (s *Service) TestMediaProvider(ctx context.Context, provider ProviderID) error {
	provStr := string(provider)
	settings, err := s.Store.GetMediaProviderSettings(ctx, provStr)
	if err != nil {
		_ = s.Store.TestMediaProviderSettings(ctx, provStr, false, err.Error())
		return err
	}
	if !settings.HasAPIKey {
		msg := "API key is not configured"
		_ = s.Store.TestMediaProviderSettings(ctx, provStr, false, msg)
		return errors.New(msg)
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var testErr error
	switch provider {
	case ProviderXAI:
		_, testErr = s.XAI.Imagine(callCtx, settings.APIKey, ImagineRequest{
			Provider:       ProviderXAI,
			Mode:           ModeTextToImage,
			Prompt:         "connectivity test",
			Model:          DefaultModel(ProviderXAI, MediaTypeImage),
			ResponseFormat: "url",
			N:              1,
		})
	case ProviderAgnes:
		_, testErr = s.Agnes.GenerateImage(callCtx, settings.APIKey, ImagineRequest{
			Provider:       ProviderAgnes,
			Mode:           ModeTextToImage,
			Prompt:         "connectivity test",
			Model:          DefaultModel(ProviderAgnes, MediaTypeImage),
			Size:           "1024x768",
			ResponseFormat: "url",
			N:              1,
		})
	default:
		testErr = errors.New("unsupported provider for connectivity test")
	}
	errMsg := ""
	success := testErr == nil
	if !success {
		errMsg = safelog.Error(testErr, 240)
	}
	if storeErr := s.Store.TestMediaProviderSettings(ctx, provStr, success, errMsg); storeErr != nil && s.Log != nil {
		s.Log.Warn("persist media provider test result failed", "provider", provStr, "error", safelog.Error(storeErr, 200))
	}
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "images.provider.test",
		RiskLevel: "low",
		Summary:   "Images provider 连通性测试",
		Payload:   map[string]any{"provider": provStr, "success": success, "error": previewError(errMsg)},
	})
	return testErr
}

func previewError(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 160 {
		return msg[:157] + "..."
	}
	return msg
}

func (s *Service) CreateMediaJob(ctx context.Context, mediaType MediaType, request any) (storage.MediaGenerationJob, error) {
	if err := ValidateMediaType(mediaType); err != nil {
		return storage.MediaGenerationJob{}, err
	}
	storageSettings, err := s.Store.GetImageStorageSettings(ctx)
	if err != nil {
		return storage.MediaGenerationJob{}, err
	}
	switch mediaType {
	case MediaTypeImage:
		return s.createMediaImageJob(ctx, request.(ImagineRequest), storageSettings)
	case MediaTypeVideo:
		return s.createMediaVideoJob(ctx, request.(VideoRequest), storageSettings)
	default:
		return storage.MediaGenerationJob{}, ErrMediaTypeMismatch
	}
}

func (s *Service) createMediaImageJob(ctx context.Context, request ImagineRequest, storageSettings storage.ImageStorageSettings) (storage.MediaGenerationJob, error) {
	if request.Provider == "" {
		request.Provider = ProviderAgnes
	}
	prov := NormalizeProvider(string(request.Provider))
	request.Provider = prov
	if err := ValidateProvider(prov); err != nil {
		return storage.MediaGenerationJob{}, err
	}
	if strings.TrimSpace(request.Model) == "" {
		request.Model = DefaultModel(prov, MediaTypeImage)
	}
	modelCap, ok := GetModelCapability(prov, request.Model)
	if !ok {
		return storage.MediaGenerationJob{}, ErrModelNotFound
	}
	if modelCap.MediaType != MediaTypeImage {
		return storage.MediaGenerationJob{}, ErrMediaTypeMismatch
	}
	if modelCap.Deprecated {
		return storage.MediaGenerationJob{}, ErrModelDeprecated
	}
	request = NormalizeRequest(request)
	if request.ResponseFormat == "" {
		request.ResponseFormat = modelCap.Parameters.DefaultFormat
	}
	if prov == ProviderAgnes {
		request.ResponseFormat = agnesResponseFormatForStorage(request.ResponseFormat, storageSettings)
	}
	if err := s.validateAgnesImageRequest(request); err != nil {
		return storage.MediaGenerationJob{}, err
	}
	providerSettings, err := s.Store.GetMediaProviderSettings(ctx, string(prov))
	if err != nil {
		return storage.MediaGenerationJob{}, err
	}
	if !providerSettings.Enabled {
		return storage.MediaGenerationJob{}, ErrProviderUnavailable
	}
	if !providerSettings.HasAPIKey {
		return storage.MediaGenerationJob{}, ErrAgnesAPIKeyMissing
	}
	endpoint := agnesImagesEndpoint
	if prov == ProviderXAI {
		endpoint, _, _ = RequestPayload(request)
	}
	params := map[string]any{
		"size":           request.Size,
		"width":          request.Width,
		"height":         request.Height,
		"aspectRatio":    request.AspectRatio,
		"resolution":     request.Resolution,
		"responseFormat": request.ResponseFormat,
		"n":              request.N,
	}
	sources := mediaSourcesFromImages(request.Images)
	job, err := s.Store.CreateMediaGenerationJob(ctx, storage.MediaGenerationJob{
		MediaType:   string(MediaTypeImage),
		Provider:    string(prov),
		Status:      "queued",
		Mode:        request.Mode,
		ModeLabel:   ModeLabel(request.Mode),
		Model:       request.Model,
		Endpoint:    endpoint,
		Prompt:      request.Prompt,
		Parameters:  params,
		SourceCount: len(sources),
		Usage:       map[string]any{},
	}, sources)
	if err != nil {
		return storage.MediaGenerationJob{}, err
	}
	s.storeMediaSourceAssets(ctx, &job, request.Images)
	s.linkMediaLibrarySourceAssets(ctx, &job, request.Images)
	s.appendMedia(ctx, job.ID, "media.job.created", map[string]any{
		"mediaType":   string(MediaTypeImage),
		"provider":    string(prov),
		"mode":        job.Mode,
		"model":       job.Model,
		"sourceCount": job.SourceCount,
	})
	s.appendMedia(ctx, job.ID, "media.job.queued", map[string]any{
		"mediaType": string(MediaTypeImage),
		"provider":  string(prov),
	})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.job.created",
		RiskLevel: "low",
		Summary:   "Media generation job 创建",
		Payload: map[string]any{
			"jobId":       job.ID,
			"mediaType":   string(MediaTypeImage),
			"provider":    string(prov),
			"mode":        job.Mode,
			"model":       job.Model,
			"sourceCount": job.SourceCount,
		},
	})
	go s.runMediaImageJob(context.Background(), job, request)
	return job, nil
}

func agnesResponseFormatForStorage(format string, settings storage.ImageStorageSettings) string {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend == "s3" || settings.Backend == "object_storage" {
		return "b64_json"
	}
	format = strings.TrimSpace(format)
	if format == "" {
		return "url"
	}
	return format
}

func (s *Service) validateAgnesImageRequest(request ImagineRequest) error {
	request = NormalizeRequest(request)
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("prompt is required")
	}
	if len(request.Prompt) > 8000 {
		return errors.New("prompt is too long")
	}
	cap, ok := GetModelCapability(request.Provider, request.Model)
	if !ok {
		return ErrModelNotFound
	}
	refCount := len(request.Images)
	if refCount < cap.MinReferences || refCount > cap.MaxReferences {
		return ErrReferenceCount
	}
	modeSupported := false
	for _, m := range cap.SupportedModes {
		if m == request.Mode {
			modeSupported = true
			break
		}
	}
	if !modeSupported {
		return ErrModeNotSupported
	}
	if err := validateImageModeReferences(request.Mode, refCount); err != nil {
		return err
	}
	if request.N <= 0 {
		request.N = 1
	}
	maxN := cap.Parameters.MaxN
	if maxN <= 0 {
		maxN = 1
	}
	if request.N > maxN {
		return errors.New("image count exceeds model maximum")
	}
	format := strings.TrimSpace(request.ResponseFormat)
	if format != "" && format != "url" && format != "b64_json" {
		return errors.New("response format is not supported")
	}
	if request.Size != "" {
		if _, _, err := ParseSize(request.Size); err != nil {
			return err
		}
	}
	for _, img := range request.Images {
		if strings.HasPrefix(img.URL, "http") || strings.HasPrefix(img.URL, "data:image/") {
			if err := ValidateImageURL(img.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateImageModeReferences(mode string, refCount int) error {
	switch mode {
	case ModeTextToImage:
		if refCount != 0 {
			return ErrReferenceCount
		}
	case ModeImageToImage:
		if refCount != 1 {
			return ErrReferenceCount
		}
	case ModeMultiImageEdit:
		if refCount < 2 || refCount > 3 {
			return ErrReferenceCount
		}
	}
	return nil
}

func mediaSourcesFromImages(images []ImageInput) []storage.MediaGenerationSource {
	out := make([]storage.MediaGenerationSource, 0, len(images))
	for i, img := range images {
		role := "input_reference"
		srcType := img.SourceType
		if srcType == "" {
			srcType = "url"
		}
		out = append(out, storage.MediaGenerationSource{
			Slot:        i + 1,
			SourceType:  srcType,
			SourceRole:  role,
			SourceLabel: img.SourceLabel,
			MimeType:    img.MimeType,
			SizeBytes:   img.SizeBytes,
			URLRedacted: img.URLRedacted,
		})
	}
	return out
}

func (s *Service) runMediaImageJob(ctx context.Context, job storage.MediaGenerationJob, request ImagineRequest) {
	if err := s.Store.StartMediaGenerationJob(ctx, job.ID); err != nil {
		if s.Log != nil {
			s.Log.Error("start media image job failed", "job", job.ID, "error", err)
		}
		return
	}
	s.appendMedia(ctx, job.ID, "media.job.started", map[string]any{
		"mediaType":   string(MediaTypeImage),
		"provider":    job.Provider,
		"mode":        job.Mode,
		"model":       job.Model,
		"sourceCount": job.SourceCount,
	})
	prov := NormalizeProvider(job.Provider)
	provSettings, err := s.Store.GetMediaProviderSettings(ctx, string(prov))
	if err != nil {
		_, _ = s.failMediaJob(ctx, job.ID, job.Endpoint, err.Error())
		return
	}
	if !provSettings.HasAPIKey {
		_, _ = s.failMediaJob(ctx, job.ID, job.Endpoint, ErrAgnesAPIKeyMissing.Error())
		return
	}
	request, err = s.resolveMediaLibraryImageInputs(ctx, request)
	if err != nil {
		_, _ = s.failMediaJob(ctx, job.ID, job.Endpoint, err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 175*time.Second)
	defer cancel()
	callStarted := time.Now()
	var result *ImagineResult
	switch prov {
	case ProviderAgnes:
		if s.Log != nil {
			s.Log.Debug("agnes image provider request started", "job_id", job.ID, "model", request.Model, "mode", request.Mode)
		}
		result, err = s.Agnes.GenerateImage(callCtx, provSettings.APIKey, request)
	case ProviderXAI:
		if s.Log != nil {
			s.Log.Debug("xai image provider request started", "job_id", job.ID, "model", request.Model, "mode", request.Mode)
		}
		result, err = s.XAI.Imagine(callCtx, provSettings.APIKey, request)
	default:
		err = ErrProviderUnavailable
	}
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("media image provider request failed", "job_id", job.ID, "provider", job.Provider, "model", request.Model, "mode", request.Mode, "latency_ms", time.Since(callStarted).Milliseconds(), "error", safelog.Error(err, 240))
		}
		_, _ = s.failMediaJob(ctx, job.ID, job.Endpoint, err.Error())
		return
	}
	if s.Log != nil {
		s.Log.Debug("media image provider request completed", "job_id", job.ID, "provider", job.Provider, "model", request.Model, "mode", request.Mode, "output_count", len(result.Images), "latency_ms", time.Since(callStarted).Milliseconds())
	}
	outputs, storeFailures := s.storeMediaGeneratedImages(ctx, job, request, result)
	if len(result.Images) > 0 && !hasStoredMediaOutputs(outputs) {
		message := "provider completed but generated image could not be stored; check image storage settings"
		_, _ = s.failMediaJob(ctx, job.ID, result.Endpoint, message)
		if storeFailures > 0 {
			s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"count": storeFailures})
		}
		return
	}
	completed, err := s.Store.CompleteMediaGenerationJob(ctx, job.ID, result.Endpoint, result.Usage, outputs)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("complete media image job failed", "job", job.ID, "error", err)
		}
		_, _ = s.failMediaJob(ctx, job.ID, result.Endpoint, err.Error())
		return
	}
	s.appendMedia(ctx, job.ID, "media.job.completed", map[string]any{
		"mediaType":     string(MediaTypeImage),
		"provider":      job.Provider,
		"mode":          completed.Mode,
		"model":         completed.Model,
		"outputCount":   len(completed.Outputs),
		"storeFailures": storeFailures,
	})
	if storeFailures > 0 {
		s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"count": storeFailures})
	}
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.job.completed",
		RiskLevel: "low",
		Summary:   "Media image generation 完成",
		Payload: map[string]any{
			"jobId":       completed.ID,
			"mediaType":   string(MediaTypeImage),
			"provider":    completed.Provider,
			"mode":        completed.Mode,
			"model":       completed.Model,
			"sourceCount": completed.SourceCount,
			"outputCount": len(completed.Outputs),
		},
	})
	settings, _ := s.Store.GetImageProviderSettings(ctx)
	s.prune(ctx, settings.HistoryRetention)
	retention := settings.HistoryRetention
	if retention <= 0 {
		retention = 500
	}
	if pruneErr := s.Store.PruneMediaGenerationJobs(ctx, retention); pruneErr != nil && s.Log != nil {
		s.Log.Warn("prune media generation history failed", "error", safelog.Error(pruneErr, 200))
	}
}

func (s *Service) resolveMediaLibraryImageInputs(ctx context.Context, request ImagineRequest) (ImagineRequest, error) {
	for i, img := range request.Images {
		if img.SourceType != "library_asset" {
			continue
		}
		kind, bareID := imageInputAssetID(img)
		if bareID == "" {
			continue
		}
		mimeType, data, label, err := s.readReferenceKindedAsset(ctx, kind, bareID)
		if err != nil {
			return ImagineRequest{}, err
		}
		request.Images[i].URL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
		request.Images[i].MimeType = mimeType
		request.Images[i].SizeBytes = int64(len(data))
		if request.Images[i].SourceLabel == "" {
			request.Images[i].SourceLabel = label
		}
	}
	return request, nil
}

func imageInputAssetID(img ImageInput) (kind, assetID string) {
	raw := img.URL
	if strings.HasPrefix(raw, "asset:") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "asset:"))
	} else if img.SourceType == "library_asset" {
		raw = strings.TrimSpace(img.URL)
	} else {
		return "", ""
	}
	return splitKindedAssetID(raw)
}

func (s *Service) readReferenceImageAsset(ctx context.Context, assetID string) (string, []byte, string, error) {
	return s.readReferenceKindedAsset(ctx, "", assetID)
}

func (s *Service) readReferenceKindedAsset(ctx context.Context, kindHint, assetID string) (string, []byte, string, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return "", nil, "", errors.New("library image asset is required")
	}
	kind, bareID := splitKindedAssetID(assetID)
	if kind == "" {
		kind = kindHint
	}
	readLegacy := func(id string) (string, []byte, string, error) {
		asset, err := s.Store.GetImageAsset(ctx, id)
		if err != nil {
			return "", nil, "", err
		}
		mimeType, data, err := s.ReadAsset(ctx, asset)
		if err != nil {
			return "", nil, "", err
		}
		return normalizeReferenceImageBytes(mimeType, data, asset.OriginalFilename)
	}
	readMedia := func(id string) (string, []byte, string, error) {
		mediaAsset, err := s.Store.GetMediaAsset(ctx, id)
		if err != nil {
			return "", nil, "", err
		}
		if mediaAsset.MediaType != string(MediaTypeImage) {
			return "", nil, "", errors.New("library asset must be an image")
		}
		mimeType, data, err := s.ReadMediaAsset(ctx, *mediaAsset)
		if err != nil {
			return "", nil, "", err
		}
		return normalizeReferenceImageBytes(mimeType, data, mediaAsset.OriginalFilename)
	}
	switch kind {
	case "legacy":
		if mimeType, data, label, err := readLegacy(bareID); err == nil {
			return mimeType, data, label, nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return "", nil, "", err
		}
		if mimeType, data, label, err := readMedia(bareID); err == nil {
			return mimeType, data, label, nil
		}
	case "media":
		if mimeType, data, label, err := readMedia(bareID); err == nil {
			return mimeType, data, label, nil
		} else if !errors.Is(err, storage.ErrNotFound) && err.Error() != "media_asset_not_found" {
			return "", nil, "", err
		}
		if mimeType, data, label, err := readLegacy(bareID); err == nil {
			return mimeType, data, label, nil
		}
	default:
		if mimeType, data, label, err := readLegacy(assetID); err == nil {
			return mimeType, data, label, nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return "", nil, "", err
		}
		if mimeType, data, label, err := readMedia(assetID); err == nil {
			return mimeType, data, label, nil
		}
	}
	return "", nil, "", errors.New("library image asset not found")
}

func normalizeReferenceImageBytes(mimeType string, data []byte, label string) (string, []byte, string, error) {
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if !AllowedImageMime(mimeType) {
		return "", nil, "", errors.New("library image mime type is unsupported")
	}
	return mimeType, data, label, nil
}

func (s *Service) storeMediaSourceAssets(ctx context.Context, job *storage.MediaGenerationJob, images []ImageInput) {
	settings, _ := s.Store.GetImageStorageSettings(ctx)
	for i, img := range images {
		if img.SourceType != "upload" || !strings.HasPrefix(img.URL, "data:image/") || i >= len(job.Sources) {
			continue
		}
		data, mimeType, err := s.Assets.DecodeDataURL(img.URL)
		if err != nil {
			s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"source": i + 1, "message": err.Error()})
			continue
		}
		if duplicate, ok := s.publicDuplicateMediaAsset(ctx, data, mimeType); ok {
			_ = s.Store.LinkMediaSourceAsset(ctx, job.Sources[i].ID, duplicate.ID)
			job.Sources[i].AssetID = duplicate.ID
			s.appendMedia(ctx, job.ID, "media.asset.deduplicated", map[string]any{"assetId": duplicate.ID, "slot": i + 1, "source": true})
			continue
		}
		asset := storage.MediaAsset{
			MediaType:        string(MediaTypeImage),
			AssetType:        "source_upload",
			Status:           "available",
			Provider:         job.Provider,
			Model:            job.Model,
			JobID:            job.ID,
			SourceRole:       "input_reference",
			Slot:             i + 1,
			PromptPreview:    job.Prompt,
			OriginalFilename: img.SourceLabel,
			MimeType:         mimeType,
		}
		created, err := s.Store.CreateMediaAsset(ctx, asset)
		if err != nil {
			s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"source": i + 1, "message": err.Error()})
			continue
		}
		stored, err := s.storeMediaAssetBytes(ctx, created, data, mimeType, settings)
		if err != nil {
			created.Status = "failed"
			created.LastError = err.Error()
			_, _ = s.Store.UpdateMediaAsset(ctx, created)
			s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"assetId": created.ID, "message": err.Error()})
			continue
		}
		_ = s.Store.LinkMediaSourceAsset(ctx, job.Sources[i].ID, stored.ID)
		job.Sources[i].AssetID = stored.ID
		s.appendMedia(ctx, job.ID, "media.asset.source_uploaded", map[string]any{
			"assetId": stored.ID,
			"slot":    i + 1,
			"storage": stored.StorageBackend,
		})
	}
}

func (s *Service) linkMediaLibrarySourceAssets(ctx context.Context, job *storage.MediaGenerationJob, images []ImageInput) {
	for i, img := range images {
		if img.SourceType != "library_asset" || i >= len(job.Sources) {
			continue
		}
		assetID := strings.TrimPrefix(img.URL, "asset:")
		if assetID == "" {
			continue
		}
		_ = s.Store.LinkMediaSourceAsset(ctx, job.Sources[i].ID, assetID)
		job.Sources[i].AssetID = assetID
	}
}

func (s *Service) publicDuplicateMediaAsset(ctx context.Context, data []byte, mimeType string) (storage.MediaAsset, bool) {
	info := ImageInfo(data, mimeType)
	asset, ok := s.Store.GetPublicMediaAssetByChecksum(ctx, info.Checksum)
	return asset, ok
}

func (s *Service) storeMediaAssetBytes(ctx context.Context, asset storage.MediaAsset, data []byte, mimeType string, settings storage.ImageStorageSettings) (storage.MediaAsset, error) {
	if mimeType == "" {
		mimeType = asset.MimeType
	}
	info := ImageInfo(data, mimeType)
	asset.MimeType = info.MimeType
	asset.Extension = imageExt(info.MimeType)
	asset.SizeBytes = info.SizeBytes
	asset.Width = info.Width
	asset.Height = info.Height
	asset.ChecksumSHA256 = info.Checksum
	if settings.Backend == "s3" || settings.Backend == "object_storage" {
		bucket := objectStorageBucket(ctx, s.Store, settings)
		region := objectStorageRegion(ctx, s.Store, settings)
		endpointLabel := objectStorageEndpointLabel(ctx, s.Store, settings)
		if client, err := newObjectClient(ctx, s.Store, settings); err == nil {
			key := mediaObjectKey(settings, asset, asset.Extension)
			started := time.Now()
			if etag, putErr := client.Put(ctx, key, data, info.MimeType); putErr == nil {
				asset.StorageBackend = "s3"
				asset.ObjectStorageProfileID = settings.ObjectStorageProfileID
				asset.S3Bucket = bucket
				asset.S3Region = region
				asset.S3EndpointLabel = endpointLabel
				asset.S3Key = key
				asset.S3ETag = etag
				asset.LastError = ""
				if s.Log != nil {
					s.Log.Debug("media asset s3 put completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data), "latency_ms", time.Since(started).Milliseconds())
				}
				return s.Store.UpdateMediaAsset(ctx, asset)
			} else if !settings.FallbackToLocal {
				if s.Log != nil && s.LogSampler.Allow("media:s3-put:"+asset.JobID+":"+asset.ID) {
					s.Log.Warn("media asset s3 put failed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(putErr, 200))
				}
				return storage.MediaAsset{}, putErr
			} else if s.Log != nil && s.LogSampler.Allow("media:s3-put:"+asset.JobID+":"+asset.ID) {
				s.Log.Warn("media asset s3 put failed; falling back to local", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(putErr, 200))
			}
		} else if !settings.FallbackToLocal {
			if s.Log != nil && s.LogSampler.Allow("media:s3-client:"+asset.JobID) {
				s.Log.Warn("media s3 client setup failed", "asset_id", asset.ID, "job_id", asset.JobID, "backend", settings.Backend, "error", safelog.Error(err, 200))
			}
			return storage.MediaAsset{}, err
		} else if s.Log != nil && s.LogSampler.Allow("media:s3-client:"+asset.JobID) {
			s.Log.Warn("media s3 client setup failed; falling back to local", "asset_id", asset.ID, "job_id", asset.JobID, "backend", settings.Backend, "error", safelog.Error(err, 200))
		}
	}
	local, err := s.Assets.StoreBytes(asset.ID, data, info.MimeType)
	if err != nil {
		return storage.MediaAsset{}, err
	}
	asset.LocalName = local.LocalName
	asset.MimeType = local.MimeType
	if asset.Extension == "" {
		asset.Extension = imageExt(local.MimeType)
	}
	asset.SizeBytes = local.SizeBytes
	asset.Width = local.Width
	asset.Height = local.Height
	asset.ChecksumSHA256 = local.Checksum
	asset.StorageBackend = "local"
	asset.LastError = ""
	return s.Store.UpdateMediaAsset(ctx, asset)
}

func mediaObjectKey(settings storage.ImageStorageSettings, asset storage.MediaAsset, ext string) string {
	prefix := strings.Trim(settings.S3Prefix, "/")
	if ext == "" {
		ext = imageExt(asset.MimeType)
	}
	created := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339Nano, asset.CreatedAt); err == nil {
		created = parsed.UTC()
	}
	assetType := strings.ReplaceAll(asset.AssetType, "_", "-")
	return fmt.Sprintf("%s/%s/%s/%04d/%02d/%s/%s-%02d%s", prefix, asset.MediaType, assetType, created.Year(), int(created.Month()), asset.JobID, asset.ID, asset.Slot, ext)
}

func (s *Service) storeMediaGeneratedImages(ctx context.Context, job storage.MediaGenerationJob, request ImagineRequest, result *ImagineResult) ([]storage.MediaGenerationOutput, int) {
	settings, _ := s.Store.GetImageStorageSettings(ctx)
	outputs := make([]storage.MediaGenerationOutput, 0, len(result.Images))
	failures := 0
	for i, image := range result.Images {
		output := storage.MediaGenerationOutput{
			Slot:              i + 1,
			MediaType:         string(MediaTypeImage),
			RemoteURLRedacted: redactedURL(image.URL),
			MimeType:          image.MimeType,
			RevisedPrompt:     image.RevisedPrompt,
			Storage:           "remote",
		}
		data, mimeType, err := s.Assets.ImageBytes(ctx, image)
		if err != nil {
			failures++
			if s.Log != nil && s.LogSampler.Allow("media:output-fetch:"+job.ID) {
				s.Log.Warn("media image output fetch failed", "job_id", job.ID, "slot", i+1, "source_host", safelog.HostLabel(image.URL), "error", safelog.Error(err, 200))
			}
			continue
		}
		if duplicate, ok := s.publicDuplicateMediaAsset(ctx, data, mimeType); ok {
			outputs = append(outputs, mediaOutputForAsset(output, duplicate))
			s.appendMedia(ctx, job.ID, "media.asset.deduplicated", map[string]any{"assetId": duplicate.ID, "slot": i + 1})
			continue
		}
		created, err := s.createMediaGeneratedAsset(ctx, job, request, image, i+1, mimeType, "local", string(MediaTypeImage))
		if err != nil {
			failures++
			continue
		}
		stored, err := s.storeMediaAssetBytes(ctx, created, data, mimeType, settings)
		if err != nil {
			failures++
			created.Status = "failed"
			created.LastError = err.Error()
			_, _ = s.Store.UpdateMediaAsset(ctx, created)
			continue
		}
		output.AssetID = stored.ID
		output.LocalName = stored.LocalName
		output.MimeType = stored.MimeType
		output.Storage = stored.StorageBackend
		output.SizeBytes = stored.SizeBytes
		metadata := map[string]any{
			"width":  stored.Width,
			"height": stored.Height,
		}
		if len(metadata) > 0 {
			output.Metadata = metadata
		}
		outputs = append(outputs, output)
		s.appendMedia(ctx, job.ID, "media.asset.stored."+stored.StorageBackend, map[string]any{"assetId": stored.ID, "slot": i + 1})
	}
	return outputs, failures
}

func hasStoredMediaOutputs(outputs []storage.MediaGenerationOutput) bool {
	for _, output := range outputs {
		if output.AssetID != "" && output.Storage != "remote" {
			return true
		}
	}
	return false
}

func mediaOutputForAsset(output storage.MediaGenerationOutput, asset storage.MediaAsset) storage.MediaGenerationOutput {
	output.AssetID = asset.ID
	output.LocalName = asset.LocalName
	output.MimeType = asset.MimeType
	output.Storage = asset.StorageBackend
	output.SizeBytes = asset.SizeBytes
	if asset.StorageBackend == "remote" && asset.OriginalSourceRedacted != "" {
		output.RemoteURLRedacted = asset.OriginalSourceRedacted
	}
	return output
}

func (s *Service) createMediaGeneratedAsset(ctx context.Context, job storage.MediaGenerationJob, request ImagineRequest, image ResultImage, slot int, mimeType, storageBackend, mediaType string) (storage.MediaAsset, error) {
	return s.Store.CreateMediaAsset(ctx, storage.MediaAsset{
		MediaType:              mediaType,
		AssetType:              "generated",
		Status:                 "available",
		Provider:               job.Provider,
		Model:                  job.Model,
		JobID:                  job.ID,
		SourceRole:             "output",
		Slot:                   slot,
		PromptPreview:          request.Prompt,
		RevisedPromptPreview:   image.RevisedPrompt,
		OriginalSourceRedacted: redactedURL(image.URL),
		MimeType:               mimeType,
		StorageBackend:         storageBackend,
	})
}

func (s *Service) failMediaJob(ctx context.Context, jobID, endpoint, message string) (storage.MediaGenerationJob, error) {
	safeMessage := safelog.Text(message, 300)
	failed, err := s.Store.FailMediaGenerationJob(ctx, jobID, endpoint, safeMessage)
	if err != nil {
		return storage.MediaGenerationJob{}, err
	}
	s.appendMedia(ctx, jobID, "media.job.failed", map[string]any{
		"message":   safeMessage,
		"mediaType": failed.MediaType,
		"provider":  failed.Provider,
		"mode":      failed.Mode,
		"model":     failed.Model,
	})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.job.failed",
		RiskLevel: "medium",
		Summary:   "Media generation job 失败",
		Payload: map[string]any{
			"jobId":     failed.ID,
			"mediaType": failed.MediaType,
			"provider":  failed.Provider,
			"mode":      failed.Mode,
			"model":     failed.Model,
			"error":     safeMessage,
		},
	})
	settings, _ := s.Store.GetImageProviderSettings(ctx)
	retention := settings.HistoryRetention
	if retention <= 0 {
		retention = 500
	}
	if pruneErr := s.Store.PruneMediaGenerationJobs(ctx, retention); pruneErr != nil && s.Log != nil {
		s.Log.Warn("prune media generation history failed", "error", safelog.Error(pruneErr, 200))
	}
	return failed, errors.New(message)
}

func (s *Service) appendMedia(ctx context.Context, jobID, eventType string, payload map[string]any) {
	event, err := s.Store.AppendEvent(ctx, "media_job", jobID, eventType, payload)
	if err == nil {
		s.Hub.Publish(event)
	}
}

func (s *Service) GetMediaJob(ctx context.Context, id string) (storage.MediaGenerationJob, error) {
	return s.Store.GetMediaGenerationJob(ctx, id)
}

func (s *Service) ListMediaJobs(ctx context.Context, limit int, mediaType, provider, status, mode, model string) ([]storage.MediaGenerationJob, error) {
	return s.Store.ListMediaGenerationJobs(ctx, limit, mediaType, provider, status, mode, model)
}

func (s *Service) ListMediaJobsPage(ctx context.Context, limit, offset int, mediaType, provider, status, mode, model string) ([]storage.MediaGenerationJob, int, error) {
	return s.Store.ListMediaGenerationJobsPage(ctx, limit, offset, mediaType, provider, status, mode, model)
}

func (s *Service) CancelMediaJob(ctx context.Context, id string) error {
	if s.videoPoller != nil {
		s.videoPoller.Cancel(id)
	}
	if err := s.Store.CancelMediaGenerationJob(ctx, id, "cancelled by user"); err != nil {
		return err
	}
	s.appendMedia(ctx, id, "media.job.cancelled", map[string]any{"reason": "user requested"})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.job.cancelled",
		RiskLevel: "low",
		Summary:   "Media generation job 取消",
		Payload:   map[string]any{"jobId": id},
	})
	return nil
}

func (s *Service) ListMediaAssets(ctx context.Context, limit int, mediaType, provider, assetType, status string, includePrivate bool) ([]storage.MediaAsset, error) {
	return s.Store.ListMediaAssets(ctx, limit, mediaType, provider, assetType, status, includePrivate)
}

func (s *Service) ListMediaAssetsPage(ctx context.Context, limit, offset int, mediaType, provider, assetType, status, privacy string) ([]storage.MediaAsset, int, error) {
	return s.Store.ListMediaAssetsPage(ctx, limit, offset, mediaType, provider, assetType, status, privacy)
}

func (s *Service) GetMediaAsset(ctx context.Context, id string) (storage.MediaAsset, error) {
	ptr, err := s.Store.GetMediaAsset(ctx, id)
	if err != nil {
		return storage.MediaAsset{}, err
	}
	return *ptr, nil
}

func (s *Service) ReadMediaAsset(ctx context.Context, asset storage.MediaAsset) (string, []byte, error) {
	switch asset.StorageBackend {
	case "s3":
		settings, err := s.Store.GetImageStorageSettings(ctx)
		if err != nil {
			return "", nil, err
		}
		client, err := newObjectClientForAsset(ctx, s.Store, toImageAssetRef(asset), settings)
		if err != nil {
			return "", nil, err
		}
		maxBytes := int64(maxStoredImageBytes)
		if asset.MediaType == string(MediaTypeVideo) {
			maxBytes = int64(MaxVideoDownloadBytes)
		}
		return client.Get(ctx, asset.S3Key, maxBytes)
	case "remote":
		return "", nil, errors.New("remote media asset URL is not persisted")
	default:
		if asset.MediaType == string(MediaTypeVideo) {
			mimeType, data, err := s.Assets.ReadLocal(asset.LocalName)
			if err != nil {
				return "", nil, err
			}
			if asset.SizeBytes > int64(MaxVideoDownloadBytes) || int64(len(data)) > int64(MaxVideoDownloadBytes) {
				return "", nil, errors.New("video exceeds maximum download size")
			}
			return mimeType, data, nil
		}
		return s.Assets.ReadLocal(asset.LocalName)
	}
}

func toImageAssetRef(a storage.MediaAsset) storage.ImageAsset {
	return storage.ImageAsset{
		ID:                     a.ID,
		StorageBackend:         a.StorageBackend,
		S3Key:                  a.S3Key,
		S3Bucket:               a.S3Bucket,
		S3Region:               a.S3Region,
		ObjectStorageProfileID: a.ObjectStorageProfileID,
		JobID:                  a.JobID,
		Slot:                   a.Slot,
	}
}

func (s *Service) DeleteMediaAsset(ctx context.Context, id string) (storage.MediaAsset, error) {
	assetPtr, err := s.Store.GetMediaAsset(ctx, id)
	if err != nil {
		return storage.MediaAsset{}, err
	}
	asset := *assetPtr
	switch asset.StorageBackend {
	case "s3":
		settings, err := s.Store.GetImageStorageSettings(ctx)
		if err != nil {
			return storage.MediaAsset{}, err
		}
		client, err := newObjectClientForAsset(ctx, s.Store, toImageAssetRef(asset), settings)
		if err != nil {
			return storage.MediaAsset{}, err
		}
		if asset.S3Key != "" {
			started := time.Now()
			if delErr := client.Delete(ctx, asset.S3Key); delErr != nil {
				asset.LastError = delErr.Error()
				_, _ = s.Store.UpdateMediaAsset(ctx, asset)
				if s.Log != nil {
					s.Log.Warn("media asset s3 delete failed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", asset.S3Bucket, "key", asset.S3Key, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(delErr, 200))
				}
				return storage.MediaAsset{}, delErr
			}
			if s.Log != nil {
				s.Log.Debug("media asset s3 delete completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", asset.S3Bucket, "key", asset.S3Key, "latency_ms", time.Since(started).Milliseconds())
			}
		}
	default:
		if asset.LocalName != "" {
			s.Assets.Remove([]string{asset.LocalName})
		}
	}
	deleted, err := s.Store.DeleteMediaAsset(ctx, id, "user requested")
	if err != nil {
		return storage.MediaAsset{}, err
	}
	s.appendMedia(ctx, asset.JobID, "media.asset.deleted", map[string]any{
		"assetId":   asset.ID,
		"storage":   asset.StorageBackend,
		"mediaType": asset.MediaType,
	})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.asset.deleted",
		RiskLevel: "low",
		Summary:   "Media asset 删除",
		Payload: map[string]any{
			"assetId":   asset.ID,
			"mediaType": asset.MediaType,
			"storage":   asset.StorageBackend,
		},
	})
	return deleted, nil
}

func storageSources(images []ImageInput) []storage.ImageGenerationSource {
	out := make([]storage.ImageGenerationSource, 0, len(images))
	for index, image := range images {
		out = append(out, storage.ImageGenerationSource{
			Slot:        index + 1,
			SourceType:  image.SourceType,
			SourceLabel: image.SourceLabel,
			MimeType:    image.MimeType,
			SizeBytes:   image.SizeBytes,
			URLRedacted: image.URLRedacted,
		})
	}
	return out
}

func responseFormatForStorage(format string, settings storage.ImageStorageSettings) string {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend == "s3" || settings.Backend == "object_storage" {
		return "b64_json"
	}
	return strings.TrimSpace(format)
}

func (s *Service) runJob(ctx context.Context, job storage.ImageGenerationJob, request ImagineRequest) {
	if err := s.Store.StartImageGenerationJob(ctx, job.ID); err != nil {
		if s.Log != nil {
			s.Log.Error("start image generation job failed", "job", job.ID, "error", err)
		}
		return
	}
	s.append(ctx, job.ID, "images.job.started", map[string]any{"mode": job.Mode, "model": job.Model, "sourceCount": job.SourceCount, "imageCount": job.ImageCount})

	settings, err := s.Store.GetImageProviderSettings(ctx)
	if err != nil {
		_, _ = s.failJob(ctx, job.ID, job.Endpoint, err.Error())
		return
	}
	if strings.TrimSpace(settings.XAIAPIKey) == "" {
		_, _ = s.failJob(ctx, job.ID, job.Endpoint, ErrAPIKeyMissing.Error())
		return
	}

	request, err = s.resolveLibraryImageInputs(ctx, request)
	if err != nil {
		_, _ = s.failJob(ctx, job.ID, job.Endpoint, err.Error())
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, 145*time.Second)
	defer cancel()
	callStarted := time.Now()
	if s.Log != nil {
		s.Log.Debug("image provider request started", "job_id", job.ID, "provider", settings.Provider, "model", request.Model, "mode", request.Mode, "endpoint", job.Endpoint)
	}
	result, err := s.XAI.Imagine(callCtx, settings.XAIAPIKey, request)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("image provider request failed", "job_id", job.ID, "provider", settings.Provider, "model", request.Model, "mode", request.Mode, "endpoint", job.Endpoint, "latency_ms", time.Since(callStarted).Milliseconds(), "error", safelog.Error(err, 240))
		}
		_, _ = s.failJob(ctx, job.ID, job.Endpoint, err.Error())
		return
	}
	if s.Log != nil {
		s.Log.Debug("image provider request completed", "job_id", job.ID, "provider", settings.Provider, "model", request.Model, "mode", request.Mode, "endpoint", result.Endpoint, "image_count", len(result.Images), "latency_ms", time.Since(callStarted).Milliseconds())
	}

	stored, storeFailures := s.storeGeneratedAssets(ctx, job, request, result)
	completed, err := s.Store.CompleteImageGenerationJob(ctx, job.ID, result.Endpoint, result.Usage, stored)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("complete image generation job failed", "job", job.ID, "error", err)
		}
		_, _ = s.failJob(ctx, job.ID, result.Endpoint, err.Error())
		return
	}
	s.append(ctx, job.ID, "images.job.completed", map[string]any{
		"mode":          completed.Mode,
		"model":         completed.Model,
		"outputCount":   len(completed.Outputs),
		"storeFailures": storeFailures,
	})
	if storeFailures > 0 {
		s.append(ctx, job.ID, "images.asset.store_failed", map[string]any{"count": storeFailures})
	}
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "images.job.completed",
		RiskLevel: "low",
		Summary:   "Images 生成调用完成",
		Payload: map[string]any{
			"jobId":       completed.ID,
			"mode":        completed.Mode,
			"model":       completed.Model,
			"sourceCount": completed.SourceCount,
			"imageCount":  completed.ImageCount,
			"outputCount": len(completed.Outputs),
		},
	})
	s.prune(ctx, settings.HistoryRetention)
}

func (s *Service) StorageSettings(ctx context.Context) (storage.ImageStorageSettings, error) {
	return s.Store.GetImageStorageSettings(ctx)
}

func (s *Service) UpdateStorageSettings(ctx context.Context, settings storage.ImageStorageSettings, updateSecret bool, clearSecret bool) (storage.ImageStorageSettings, error) {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend == "s3" {
		if settings.S3Endpoint == "" {
			return storage.ImageStorageSettings{}, errors.New("S3 compatible endpoint is required")
		}
		if settings.S3Bucket == "" {
			return storage.ImageStorageSettings{}, errors.New("S3 bucket is required")
		}
	}
	if settings.Backend == "object_storage" {
		if settings.ObjectStorageProfileID == "" {
			return storage.ImageStorageSettings{}, errors.New("object storage profile is required")
		}
		if _, err := s.Store.GetObjectStorageProfile(ctx, settings.ObjectStorageProfileID); err != nil {
			return storage.ImageStorageSettings{}, errors.New("selected object storage profile does not exist")
		}
	}
	return s.Store.UpdateImageStorageSettings(ctx, settings, updateSecret, clearSecret)
}

func (s *Service) TestStorage(ctx context.Context, settings storage.ImageStorageSettings) error {
	endpointLabel := objectStorageEndpointLabel(ctx, s.Store, settings)
	bucket := objectStorageBucket(ctx, s.Store, settings)
	client, err := newObjectClient(ctx, s.Store, settings)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("image storage test setup failed", "backend", settings.Backend, "endpoint", endpointLabel, "bucket", bucket, "error", safelog.Error(err, 200))
		}
		return err
	}
	started := time.Now()
	if s.Log != nil {
		s.Log.Debug("image storage test started", "backend", settings.Backend, "endpoint", endpointLabel, "bucket", bucket)
	}
	err = client.Test(ctx, settings.S3Prefix)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("image storage test failed", "backend", settings.Backend, "endpoint", endpointLabel, "bucket", bucket, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		return err
	}
	if s.Log != nil {
		s.Log.Debug("image storage test completed", "backend", settings.Backend, "endpoint", endpointLabel, "bucket", bucket, "latency_ms", time.Since(started).Milliseconds())
	}
	return nil
}

func (s *Service) ReadAsset(ctx context.Context, asset storage.ImageAsset) (string, []byte, error) {
	switch asset.StorageBackend {
	case "s3":
		settings, err := s.Store.GetImageStorageSettings(ctx)
		if err != nil {
			return "", nil, err
		}
		client, err := newObjectClientForAsset(ctx, s.Store, asset, settings)
		if err != nil {
			return "", nil, err
		}
		return client.Get(ctx, asset.S3Key, maxStoredImageBytes)
	case "remote":
		job, err := s.Store.GetImageGenerationJob(ctx, asset.JobID)
		if err != nil {
			return "", nil, err
		}
		for _, output := range job.Outputs {
			if output.AssetID == asset.ID || (output.AssetID == "" && output.Slot == asset.Slot) {
				if output.RemoteURL == "" {
					break
				}
				data, mimeType, err := s.Assets.ImageBytes(ctx, ResultImage{URL: output.RemoteURL, MimeType: asset.MimeType})
				return mimeType, data, err
			}
		}
		return "", nil, errors.New("remote image url is missing")
	default:
		return s.Assets.ReadLocal(asset.LocalName)
	}
}

func (s *Service) DeleteAsset(ctx context.Context, id string) (storage.ImageAsset, error) {
	asset, err := s.Store.GetImageAsset(ctx, id)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	switch asset.StorageBackend {
	case "s3":
		settings, err := s.Store.GetImageStorageSettings(ctx)
		if err != nil {
			return storage.ImageAsset{}, err
		}
		client, err := newObjectClientForAsset(ctx, s.Store, asset, settings)
		if err != nil {
			return storage.ImageAsset{}, err
		}
		if asset.S3Key != "" {
			started := time.Now()
			if err := client.Delete(ctx, asset.S3Key); err != nil {
				asset.LastError = err.Error()
				_, _ = s.Store.UpdateImageAsset(ctx, asset)
				if s.Log != nil {
					s.Log.Warn("image asset s3 delete failed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", asset.S3Bucket, "key", asset.S3Key, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
				}
				return storage.ImageAsset{}, err
			}
			if s.Log != nil {
				s.Log.Debug("image asset s3 delete completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", asset.S3Bucket, "key", asset.S3Key, "latency_ms", time.Since(started).Milliseconds())
			}
		}
	default:
		if asset.LocalName != "" {
			s.Assets.Remove([]string{asset.LocalName})
		}
	}
	deleted, err := s.Store.DeleteImageAsset(ctx, id, "user requested")
	if err != nil {
		return storage.ImageAsset{}, err
	}
	s.append(ctx, asset.JobID, "images.asset.deleted", map[string]any{"assetId": asset.ID, "storage": asset.StorageBackend})
	return deleted, nil
}

func (s *Service) ArchiveAssetToS3(ctx context.Context, id string) (storage.ImageAsset, error) {
	asset, err := s.Store.GetImageAsset(ctx, id)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	if asset.StorageBackend == "s3" {
		return storage.ImageAsset{}, errors.New("asset is already stored in object storage")
	}
	if asset.StorageBackend != "local" && asset.StorageBackend != "remote" {
		return storage.ImageAsset{}, errors.New("asset cannot be archived to object storage")
	}
	sourceStorage := asset.StorageBackend
	settings, err := s.Store.GetImageStorageSettings(ctx)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	client, err := newObjectClient(ctx, s.Store, settings)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	bucket := objectStorageBucket(ctx, s.Store, settings)
	region := objectStorageRegion(ctx, s.Store, settings)
	endpointLabel := objectStorageEndpointLabel(ctx, s.Store, settings)
	mimeType, data, localName, err := s.archiveAssetBytes(ctx, asset)
	if err != nil {
		asset.LastError = safelog.Error(err, 240)
		_, _ = s.Store.UpdateImageAsset(ctx, asset)
		return storage.ImageAsset{}, err
	}
	info := ImageInfo(data, mimeType)
	asset.MimeType = info.MimeType
	asset.Extension = imageExt(info.MimeType)
	asset.SizeBytes = info.SizeBytes
	asset.Width = info.Width
	asset.Height = info.Height
	asset.ChecksumSHA256 = info.Checksum
	key := objectKey(settings, asset, asset.Extension)
	started := time.Now()
	if s.Log != nil {
		s.Log.Debug("image asset s3 archive started", "asset_id", asset.ID, "job_id", asset.JobID, "source_storage", asset.StorageBackend, "bucket", bucket, "key", key, "bytes", len(data))
	}
	etag, err := client.Put(ctx, key, data, mimeType)
	if err != nil {
		asset.LastError = safelog.Error(err, 240)
		_, _ = s.Store.UpdateImageAsset(ctx, asset)
		if s.Log != nil {
			s.Log.Warn("image asset s3 archive failed", "asset_id", asset.ID, "job_id", asset.JobID, "source_storage", asset.StorageBackend, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		return storage.ImageAsset{}, err
	}
	asset.StorageBackend = "s3"
	asset.ObjectStorageProfileID = settings.ObjectStorageProfileID
	asset.LocalName = ""
	asset.S3Bucket = bucket
	asset.S3Region = region
	asset.S3EndpointLabel = endpointLabel
	asset.S3Key = key
	asset.S3ETag = etag
	asset.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	asset.LastError = ""
	updated, err := s.Store.ArchiveImageAssetToS3(ctx, asset)
	if err != nil {
		_ = client.Delete(ctx, key)
		return storage.ImageAsset{}, err
	}
	if localName != "" {
		s.Assets.Remove([]string{localName})
	}
	s.append(ctx, asset.JobID, "images.asset.archived.s3", map[string]any{"assetId": asset.ID, "key": key, "sourceStorage": sourceStorage})
	if s.Log != nil {
		s.Log.Debug("image asset s3 archive completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds())
	}
	return updated, nil
}

func (s *Service) ArchiveMediaAssetToS3(ctx context.Context, id string) (*storage.MediaAsset, error) {
	asset, err := s.Store.GetMediaAsset(ctx, id)
	if err != nil {
		return nil, err
	}
	if asset.StorageBackend != "local" {
		return nil, errors.New("can only archive local media assets to object storage")
	}
	if asset.LocalName == "" {
		return nil, errors.New("local media asset file reference is missing")
	}
	settings, err := s.Store.GetImageStorageSettings(ctx)
	if err != nil {
		return nil, err
	}
	client, err := newObjectClient(ctx, s.Store, settings)
	if err != nil {
		return nil, err
	}
	bucket := objectStorageBucket(ctx, s.Store, settings)
	region := objectStorageRegion(ctx, s.Store, settings)
	endpointLabel := objectStorageEndpointLabel(ctx, s.Store, settings)
	mimeType, data, err := s.Assets.ReadLocal(asset.LocalName)
	if err != nil {
		asset.LastError = safelog.Error(err, 240)
		_, _ = s.Store.UpdateMediaAsset(ctx, *asset)
		return nil, err
	}
	info := ImageInfo(data, mimeType)
	asset.MimeType = info.MimeType
	asset.Extension = imageExt(info.MimeType)
	asset.SizeBytes = info.SizeBytes
	asset.Width = info.Width
	asset.Height = info.Height
	asset.ChecksumSHA256 = info.Checksum
	profileID := settings.ObjectStorageProfileID
	ext := asset.Extension
	if ext == "" {
		ext = imageExt(asset.MimeType)
	}
	prefix := strings.Trim(settings.S3Prefix, "/")
	created := time.Now().UTC()
	if parsed, parseErr := time.Parse(time.RFC3339Nano, asset.CreatedAt); parseErr == nil {
		created = parsed.UTC()
	}
	assetType := strings.ReplaceAll(asset.AssetType, "_", "-")
	jobID := asset.JobID
	if jobID == "" {
		jobID = "adhoc"
	}
	key := fmt.Sprintf("%s/images/media/%s/%04d/%02d/%s/%s-%02d%s",
		prefix, assetType, created.Year(), int(created.Month()), jobID, asset.ID, asset.Slot, ext)
	started := time.Now()
	if s.Log != nil {
		s.Log.Debug("media asset s3 archive started", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data))
	}
	etag, err := client.Put(ctx, key, data, mimeType)
	if err != nil {
		asset.LastError = safelog.Error(err, 240)
		_, _ = s.Store.UpdateMediaAsset(ctx, *asset)
		if s.Log != nil {
			s.Log.Warn("media asset s3 archive failed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		return nil, err
	}
	updated, err := s.Store.UpdateMediaAssetStorage(ctx, asset.ID, "s3", profileID, bucket, region, endpointLabel, key, etag)
	if err != nil {
		_ = client.Delete(ctx, key)
		return nil, err
	}
	if asset.LocalName != "" {
		localPath := asset.LocalName
		s.Assets.Remove([]string{localPath})
		if s.Log != nil {
			s.Log.Debug("media asset local file removed after archive", "asset_id", asset.ID, "local_name", safelog.Text(localPath, 200))
		}
	}
	s.appendMedia(ctx, asset.JobID, "media.asset.archived.s3", map[string]any{"assetId": asset.ID, "key": key, "bucket": bucket})
	if s.Log != nil {
		s.Log.Debug("media asset s3 archive completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds())
	}
	return updated, nil
}

func (s *Service) archiveMediaAssetBytes(ctx context.Context, asset storage.MediaAsset) (string, []byte, string, error) {
	switch asset.StorageBackend {
	case "local":
		if asset.LocalName == "" {
			return "", nil, "", errors.New("local media asset file is missing")
		}
		mimeType, data, err := s.Assets.ReadLocal(asset.LocalName)
		return mimeType, data, asset.LocalName, err
	case "remote":
		mimeType, data, err := s.ReadMediaAsset(ctx, asset)
		return mimeType, data, "", err
	default:
		return "", nil, "", errors.New("media asset cannot be archived to object storage")
	}
}

func (s *Service) archiveAssetBytes(ctx context.Context, asset storage.ImageAsset) (string, []byte, string, error) {
	switch asset.StorageBackend {
	case "local":
		if asset.LocalName == "" {
			return "", nil, "", errors.New("local asset file is missing")
		}
		mimeType, data, err := s.Assets.ReadLocal(asset.LocalName)
		return mimeType, data, asset.LocalName, err
	case "remote":
		mimeType, data, err := s.ReadAsset(ctx, asset)
		return mimeType, data, "", err
	default:
		return "", nil, "", errors.New("asset cannot be archived to object storage")
	}
}

func (s *Service) UploadLibraryAsset(ctx context.Context, filename string, data []byte, mimeType string) (LibraryUploadResult, error) {
	if len(data) == 0 {
		return LibraryUploadResult{}, errors.New("image file is empty")
	}
	if len(data) > MaxImageBytes {
		return LibraryUploadResult{}, fmt.Errorf("image file is larger than %d MB", MaxImageBytes>>20)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if !AllowedImageMime(mimeType) {
		return LibraryUploadResult{}, errors.New("image file must be jpeg, png, gif, or webp")
	}
	if existing, ok := s.publicDuplicateAsset(ctx, data, mimeType); ok {
		return LibraryUploadResult{Asset: existing, Duplicate: true}, nil
	}
	created, err := s.Store.CreateImageAsset(ctx, storage.ImageAsset{
		AssetType:        "manual_upload",
		Status:           "available",
		SourceRole:       "library_upload",
		OriginalFilename: filename,
		MimeType:         mimeType,
	})
	if err != nil {
		return LibraryUploadResult{}, err
	}
	settings, _ := s.Store.GetImageStorageSettings(ctx)
	stored, err := s.storeBytes(ctx, created, data, mimeType, settings)
	if err != nil {
		created.LastError = err.Error()
		_, _ = s.Store.UpdateImageAsset(ctx, created)
		return LibraryUploadResult{}, err
	}
	return LibraryUploadResult{Asset: stored}, nil
}

func (s *Service) storeSourceAssets(ctx context.Context, job *storage.ImageGenerationJob, request ImagineRequest) {
	settings, _ := s.Store.GetImageStorageSettings(ctx)
	for index, image := range request.Images {
		if image.SourceType != "upload" || !strings.HasPrefix(image.URL, "data:image/") || index >= len(job.Sources) {
			continue
		}
		data, mimeType, err := s.Assets.DecodeDataURL(image.URL)
		if err != nil {
			s.append(ctx, job.ID, "images.asset.store_failed", map[string]any{"source": index + 1, "message": err.Error()})
			continue
		}
		if duplicate, ok := s.publicDuplicateAsset(ctx, data, mimeType); ok {
			_ = s.Store.LinkImageSourceAsset(ctx, job.Sources[index].ID, duplicate.ID)
			job.Sources[index].AssetID = duplicate.ID
			s.append(ctx, job.ID, "images.asset.deduplicated", map[string]any{"assetId": duplicate.ID, "slot": index + 1, "source": true})
			continue
		}
		asset := storage.ImageAsset{
			AssetType:        "source_upload",
			Status:           "available",
			Provider:         job.Provider,
			Model:            job.Model,
			JobID:            job.ID,
			SourceRole:       "input_reference",
			Slot:             index + 1,
			PromptPreview:    job.Prompt,
			OriginalFilename: image.SourceLabel,
			MimeType:         mimeType,
		}
		created, err := s.Store.CreateImageAsset(ctx, asset)
		if err != nil {
			s.append(ctx, job.ID, "images.asset.store_failed", map[string]any{"source": index + 1, "message": err.Error()})
			continue
		}
		stored, err := s.storeBytes(ctx, created, data, mimeType, settings)
		if err != nil {
			created.LastError = err.Error()
			_, _ = s.Store.UpdateImageAsset(ctx, created)
			s.append(ctx, job.ID, "images.asset.store_failed", map[string]any{"assetId": created.ID, "message": err.Error()})
			continue
		}
		_ = s.Store.LinkImageSourceAsset(ctx, job.Sources[index].ID, stored.ID)
		job.Sources[index].AssetID = stored.ID
		s.append(ctx, job.ID, "images.asset.source_uploaded", map[string]any{"assetId": stored.ID, "slot": index + 1, "storage": stored.StorageBackend})
	}
}

func (s *Service) linkLibrarySourceAssets(ctx context.Context, job *storage.ImageGenerationJob, request ImagineRequest) {
	for index, image := range request.Images {
		if image.SourceType != "library_asset" || index >= len(job.Sources) {
			continue
		}
		assetID := strings.TrimPrefix(image.URL, "asset:")
		if assetID == "" {
			continue
		}
		_ = s.Store.LinkImageSourceAsset(ctx, job.Sources[index].ID, assetID)
		job.Sources[index].AssetID = assetID
	}
}

func (s *Service) resolveLibraryImageInputs(ctx context.Context, request ImagineRequest) (ImagineRequest, error) {
	for index, image := range request.Images {
		if image.SourceType != "library_asset" {
			continue
		}
		kind, assetID := imageInputAssetID(image)
		mimeType, data, label, err := s.readReferenceKindedAsset(ctx, kind, assetID)
		if err != nil {
			return ImagineRequest{}, err
		}
		request.Images[index].URL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
		request.Images[index].MimeType = mimeType
		request.Images[index].SizeBytes = int64(len(data))
		if request.Images[index].SourceLabel == "" {
			request.Images[index].SourceLabel = label
		}
	}
	return request, nil
}

func (s *Service) storeGeneratedAssets(ctx context.Context, job storage.ImageGenerationJob, request ImagineRequest, result *ImagineResult) ([]storage.ImageGenerationOutput, int) {
	settings, _ := s.Store.GetImageStorageSettings(ctx)
	outputs := make([]storage.ImageGenerationOutput, 0, len(result.Images))
	failures := 0
	for index, image := range result.Images {
		output := storage.ImageGenerationOutput{
			Slot:          index + 1,
			RemoteURL:     image.URL,
			MimeType:      image.MimeType,
			RevisedPrompt: image.RevisedPrompt,
			Storage:       "remote",
		}
		data, mimeType, err := s.Assets.ImageBytes(ctx, image)
		if err != nil {
			failures++
			if s.Log != nil && s.LogSampler.Allow("images:output-fetch:"+job.ID) {
				s.Log.Warn("image output fetch failed", "job_id", job.ID, "slot", index+1, "source_host", safelog.HostLabel(image.URL), "error", safelog.Error(err, 200))
			}
			asset, createErr := s.createGeneratedAsset(ctx, job, request, image, index+1, image.MimeType, "remote")
			if createErr == nil {
				output.AssetID = asset.ID
				output.URL = "/api/images/library/assets/" + asset.ID + "/content"
			} else if s.Log != nil && s.LogSampler.Allow("images:remote-create:"+job.ID) {
				s.Log.Warn("image remote asset create failed", "job_id", job.ID, "slot", index+1, "error", safelog.Error(createErr, 200))
			}
			outputs = append(outputs, output)
			continue
		}
		if duplicate, ok := s.publicDuplicateAsset(ctx, data, mimeType); ok {
			outputs = append(outputs, imageOutputForAsset(output, duplicate))
			s.append(ctx, job.ID, "images.asset.deduplicated", map[string]any{"assetId": duplicate.ID, "slot": index + 1})
			continue
		}
		created, err := s.createGeneratedAsset(ctx, job, request, image, index+1, mimeType, "local")
		if err != nil {
			failures++
			outputs = append(outputs, output)
			continue
		}
		stored, err := s.storeBytes(ctx, created, data, mimeType, settings)
		if err != nil {
			failures++
			created.LastError = err.Error()
			_, _ = s.Store.UpdateImageAsset(ctx, created)
			outputs = append(outputs, output)
			continue
		}
		output.AssetID = stored.ID
		output.LocalName = stored.LocalName
		output.MimeType = stored.MimeType
		output.Storage = stored.StorageBackend
		output.SizeBytes = stored.SizeBytes
		output.URL = "/api/images/library/assets/" + stored.ID + "/content"
		outputs = append(outputs, output)
		s.append(ctx, job.ID, "images.asset.stored."+stored.StorageBackend, map[string]any{"assetId": stored.ID, "slot": index + 1})
	}
	return outputs, failures
}

func (s *Service) publicDuplicateAsset(ctx context.Context, data []byte, mimeType string) (storage.ImageAsset, bool) {
	info := ImageInfo(data, mimeType)
	asset, err := s.Store.GetPublicImageAssetByChecksum(ctx, info.Checksum)
	return asset, err == nil
}

func imageOutputForAsset(output storage.ImageGenerationOutput, asset storage.ImageAsset) storage.ImageGenerationOutput {
	output.AssetID = asset.ID
	output.LocalName = asset.LocalName
	output.MimeType = asset.MimeType
	output.Storage = asset.StorageBackend
	output.SizeBytes = asset.SizeBytes
	if asset.StorageBackend == "remote" && asset.URL != "" {
		output.URL = asset.URL
	} else {
		output.URL = "/api/images/library/assets/" + asset.ID + "/content"
	}
	return output
}

func (s *Service) createGeneratedAsset(ctx context.Context, job storage.ImageGenerationJob, request ImagineRequest, image ResultImage, slot int, mimeType string, storageBackend string) (storage.ImageAsset, error) {
	return s.Store.CreateImageAsset(ctx, storage.ImageAsset{
		AssetType:              "generated",
		Status:                 "available",
		Provider:               job.Provider,
		Model:                  job.Model,
		JobID:                  job.ID,
		SourceRole:             "output",
		Slot:                   slot,
		PromptPreview:          request.Prompt,
		RevisedPromptPreview:   image.RevisedPrompt,
		OriginalSourceRedacted: redactedURL(image.URL),
		MimeType:               mimeType,
		StorageBackend:         storageBackend,
	})
}

func (s *Service) storeBytes(ctx context.Context, asset storage.ImageAsset, data []byte, mimeType string, settings storage.ImageStorageSettings) (storage.ImageAsset, error) {
	if mimeType == "" {
		mimeType = asset.MimeType
	}
	info := ImageInfo(data, mimeType)
	asset.MimeType = info.MimeType
	asset.Extension = imageExt(info.MimeType)
	asset.SizeBytes = info.SizeBytes
	asset.Width = info.Width
	asset.Height = info.Height
	asset.ChecksumSHA256 = info.Checksum
	if settings.Backend == "s3" || settings.Backend == "object_storage" {
		bucket := objectStorageBucket(ctx, s.Store, settings)
		region := objectStorageRegion(ctx, s.Store, settings)
		endpointLabel := objectStorageEndpointLabel(ctx, s.Store, settings)
		if client, err := newObjectClient(ctx, s.Store, settings); err == nil {
			key := objectKey(settings, asset, asset.Extension)
			started := time.Now()
			if etag, err := client.Put(ctx, key, data, info.MimeType); err == nil {
				asset.StorageBackend = "s3"
				asset.ObjectStorageProfileID = settings.ObjectStorageProfileID
				asset.S3Bucket = bucket
				asset.S3Region = region
				asset.S3EndpointLabel = endpointLabel
				asset.S3Key = key
				asset.S3ETag = etag
				asset.LastError = ""
				if s.Log != nil {
					s.Log.Debug("image asset s3 put completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data), "latency_ms", time.Since(started).Milliseconds())
				}
				return s.Store.UpdateImageAsset(ctx, asset)
			} else if !settings.FallbackToLocal {
				if s.Log != nil && s.LogSampler.Allow("images:s3-put:"+asset.JobID+":"+asset.ID) {
					s.Log.Warn("image asset s3 put failed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
				}
				return storage.ImageAsset{}, err
			} else if s.Log != nil && s.LogSampler.Allow("images:s3-put:"+asset.JobID+":"+asset.ID) {
				s.Log.Warn("image asset s3 put failed; falling back to local storage", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
			}
		} else if !settings.FallbackToLocal {
			if s.Log != nil && s.LogSampler.Allow("images:s3-client:"+asset.JobID) {
				s.Log.Warn("image s3 client setup failed", "asset_id", asset.ID, "job_id", asset.JobID, "backend", settings.Backend, "endpoint", endpointLabel, "bucket", bucket, "error", safelog.Error(err, 200))
			}
			return storage.ImageAsset{}, err
		} else if s.Log != nil && s.LogSampler.Allow("images:s3-client:"+asset.JobID) {
			s.Log.Warn("image s3 client setup failed; falling back to local storage", "asset_id", asset.ID, "job_id", asset.JobID, "backend", settings.Backend, "endpoint", endpointLabel, "bucket", bucket, "error", safelog.Error(err, 200))
		}
	}
	local, err := s.Assets.StoreBytes(asset.ID, data, info.MimeType)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	asset.LocalName = local.LocalName
	asset.MimeType = local.MimeType
	if asset.Extension == "" {
		asset.Extension = imageExt(local.MimeType)
	}
	asset.SizeBytes = local.SizeBytes
	asset.Width = local.Width
	asset.Height = local.Height
	asset.ChecksumSHA256 = local.Checksum
	asset.StorageBackend = "local"
	asset.LastError = ""
	return s.Store.UpdateImageAsset(ctx, asset)
}

func objectKey(settings storage.ImageStorageSettings, asset storage.ImageAsset, ext string) string {
	prefix := strings.Trim(settings.S3Prefix, "/")
	if ext == "" {
		ext = imageExt(asset.MimeType)
	}
	created := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339Nano, asset.CreatedAt); err == nil {
		created = parsed.UTC()
	}
	assetType := strings.ReplaceAll(asset.AssetType, "_", "-")
	return fmt.Sprintf("%s/%s/%04d/%02d/%s/%s-%02d%s", prefix, assetType, created.Year(), int(created.Month()), asset.JobID, asset.ID, asset.Slot, ext)
}

func (s *Service) failJob(ctx context.Context, jobID, endpoint, message string) (storage.ImageGenerationJob, error) {
	safeMessage := safelog.Text(message, 300)
	failed, err := s.Store.FailImageGenerationJob(ctx, jobID, endpoint, safeMessage)
	if err != nil {
		return storage.ImageGenerationJob{}, err
	}
	s.append(ctx, jobID, "images.job.failed", map[string]any{"message": safeMessage, "mode": failed.Mode, "model": failed.Model})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "images.job.failed",
		RiskLevel: "medium",
		Summary:   "Images 生成调用失败",
		Payload:   map[string]any{"jobId": failed.ID, "mode": failed.Mode, "model": failed.Model, "error": safeMessage},
	})
	settings, _ := s.Store.GetImageProviderSettings(ctx)
	s.prune(ctx, settings.HistoryRetention)
	return failed, errors.New(message)
}

func (s *Service) prune(ctx context.Context, retention int) {
	if err := s.Store.PruneImageGenerationJobs(ctx, retention); err != nil {
		if s.Log != nil {
			s.Log.Warn("prune image generation history failed", "error", err)
		}
		return
	}
}

func (s *Service) append(ctx context.Context, jobID, eventType string, payload map[string]any) {
	event, err := s.Store.AppendEvent(ctx, eventScope, jobID, eventType, payload)
	if err == nil {
		s.Hub.Publish(event)
	}
}
