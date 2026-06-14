package images

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

const (
	videoPollIntervalMin = 4 * time.Second
	videoPollIntervalMax = 12 * time.Second
	videoPollStaleAfter  = 2 * time.Hour
)

type videoPollSupervisor struct {
	mu       sync.Mutex
	svc      *Service
	active   map[string]bool
	cancelFn map[string]context.CancelFunc
	started  bool
}

func newVideoPollSupervisor(svc *Service) *videoPollSupervisor {
	return &videoPollSupervisor{
		svc:      svc,
		active:   make(map[string]bool),
		cancelFn: make(map[string]context.CancelFunc),
	}
}

func (p *videoPollSupervisor) Start(ctx context.Context) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()

	jobs, err := p.svc.Store.ListActiveMediaVideoJobs(ctx)
	if err != nil && p.svc.Log != nil {
		p.svc.Log.Warn("list active video jobs on startup failed", "error", safelog.Error(err, 200))
	}
	for _, job := range jobs {
		p.track(job.ID)
		go p.pollLoop(context.Background(), job)
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newJobs, err := p.svc.Store.ListActiveMediaVideoJobs(ctx)
			if err != nil {
				if p.svc.Log != nil {
					p.svc.Log.Warn("video poller periodic scan failed", "error", safelog.Error(err, 200))
				}
				continue
			}
			for _, job := range newJobs {
				if p.isTracked(job.ID) {
					continue
				}
				p.track(job.ID)
				go p.pollLoop(context.Background(), job)
			}
		}
	}
}

func (p *videoPollSupervisor) isTracked(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active[id]
}

func (p *videoPollSupervisor) track(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active[id] = true
}

func (p *videoPollSupervisor) untrack(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.active, id)
	if c, ok := p.cancelFn[id]; ok {
		c()
		delete(p.cancelFn, id)
	}
}

func (p *videoPollSupervisor) Cancel(id string) {
	p.mu.Lock()
	c, ok := p.cancelFn[id]
	p.mu.Unlock()
	if ok && c != nil {
		c()
	}
}

func (p *videoPollSupervisor) pollLoop(ctx context.Context, job storage.MediaGenerationJob) {
	defer p.untrack(job.ID)

	jobCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancelFn[job.ID] = cancel
	p.mu.Unlock()

	prov := NormalizeProvider(job.Provider)
	provSettings, err := p.svc.Store.GetMediaProviderSettings(jobCtx, string(prov))
	if err != nil {
		_, _ = p.svc.failMediaJob(jobCtx, job.ID, "", err.Error())
		return
	}
	if !provSettings.HasAPIKey {
		_, _ = p.svc.failMediaJob(jobCtx, job.ID, "", "Agnes API key is not configured")
		return
	}
	created, err := time.Parse(time.RFC3339Nano, job.CreatedAt)
	if err != nil {
		created = time.Now()
	}
	lastProgress := -1
	backoff := videoPollIntervalMin
	attempt := 0
	for {
		if jobCtx.Err() != nil {
			return
		}
		if time.Since(created) > videoPollStaleAfter {
			_ = p.svc.Store.CancelMediaGenerationJob(jobCtx, job.ID, "video job exceeded max poll duration")
			p.svc.appendMedia(jobCtx, job.ID, "media.job.timeout", map[string]any{"reason": "exceeded max poll duration"})
			return
		}
		attempt++
		current, err := p.svc.Store.GetMediaGenerationJob(jobCtx, job.ID)
		if err != nil {
			if p.svc.Log != nil {
				p.svc.Log.Warn("video job reload failed during poll", "job_id", job.ID, "error", safelog.Error(err, 200))
			}
			time.Sleep(backoff)
			continue
		}
		if current.Status != "queued" && current.Status != "running" && current.Status != "provider_queued" {
			return
		}
		taskID := current.ProviderTaskID
		videoID := current.ProviderVideoID
		if taskID == "" && videoID == "" {
			if attempt > 15 {
				_, _ = p.svc.failMediaJob(jobCtx, job.ID, "", "video task_id missing after repeated checks")
				return
			}
			time.Sleep(backoff)
			continue
		}
		pollResult, pollErr := p.svc.Agnes.GetVideo(jobCtx, provSettings.APIKey, taskID, videoID)
		if pollErr != nil {
			if p.svc.Log != nil && p.svc.LogSampler.Allow("video:poll-err:"+job.ID) {
				p.svc.Log.Warn("agnes video poll failed", "job_id", job.ID, "task_id", taskID, "video_id", safelog.Text(videoID, 24), "attempt", attempt, "error", safelog.Error(pollErr, 240))
			}
			backoff = minDuration(backoff*2, videoPollIntervalMax)
			time.Sleep(backoff)
			continue
		}
		backoff = videoPollIntervalMin

		if pollResult.Progress != lastProgress {
			lastProgress = pollResult.Progress
			_ = p.svc.Store.UpdateMediaJobProgress(jobCtx, job.ID, pollResult.RawStatus, pollResult.Progress)
			p.svc.appendMedia(jobCtx, job.ID, "media.video.progress", map[string]any{
				"progress":       pollResult.Progress,
				"providerStatus": pollResult.RawStatus,
			})
		}

		switch pollResult.Status {
		case "queued", "running", "unknown":
			_ = p.svc.Store.SetMediaJobProviderIDs(jobCtx, job.ID, firstNonEmpty(taskID, pollResult.ProviderTaskID), firstNonEmpty(videoID, pollResult.ProviderVideoID), pollResult.RawStatus)
			if pollResult.Status == "running" {
				if current.Status == "queued" || current.Status == "provider_queued" {
					_ = p.svc.Store.StartMediaGenerationJob(jobCtx, job.ID)
				}
			}
			time.Sleep(backoff)
			continue
		case "failed":
			msg := pollResult.ErrorMessage
			if msg == "" {
				msg = "provider reported generation failed"
			}
			_, _ = p.svc.failMediaJob(jobCtx, job.ID, "", msg)
			return
		case "completed":
			p.svc.finalizeVideoJob(jobCtx, current, pollResult, provSettings.APIKey)
			return
		default:
			time.Sleep(backoff)
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (s *Service) createMediaVideoJob(ctx context.Context, request VideoRequest, storageSettings storage.ImageStorageSettings) (storage.MediaGenerationJob, error) {
	if request.Provider == "" {
		request.Provider = ProviderAgnes
	}
	prov := NormalizeProvider(string(request.Provider))
	request.Provider = prov
	if err := ValidateProvider(prov); err != nil {
		return storage.MediaGenerationJob{}, err
	}
	if strings.TrimSpace(request.Model) == "" {
		request.Model = DefaultModel(prov, MediaTypeVideo)
	}
	modelCap, ok := GetModelCapability(prov, request.Model)
	if !ok {
		return storage.MediaGenerationJob{}, ErrModelNotFound
	}
	if modelCap.MediaType != MediaTypeVideo {
		return storage.MediaGenerationJob{}, ErrMediaTypeMismatch
	}
	if modelCap.Deprecated {
		return storage.MediaGenerationJob{}, ErrModelDeprecated
	}
	request.Parameters = videoParametersWithDefaults(request.Parameters, modelCap.Parameters)
	if err := s.validateAgnesVideoRequest(request); err != nil {
		return storage.MediaGenerationJob{}, err
	}
	provSettings, err := s.Store.GetMediaProviderSettings(ctx, string(prov))
	if err != nil {
		return storage.MediaGenerationJob{}, err
	}
	if !provSettings.Enabled {
		return storage.MediaGenerationJob{}, ErrProviderUnavailable
	}
	if !provSettings.HasAPIKey {
		return storage.MediaGenerationJob{}, ErrAgnesAPIKeyMissing
	}
	params := map[string]any{
		"width":     request.Parameters.Width,
		"height":    request.Parameters.Height,
		"numFrames": request.Parameters.NumFrames,
		"frameRate": request.Parameters.FrameRate,
		"seed":      request.Parameters.Seed,
	}
	if request.Parameters.NumFrames > 0 && request.Parameters.FrameRate > 0 {
		params["seconds"] = float64(request.Parameters.NumFrames) / float64(request.Parameters.FrameRate)
	}
	sources := mediaSourcesFromImages(request.Images)
	job, err := s.Store.CreateMediaGenerationJob(ctx, storage.MediaGenerationJob{
		MediaType:   string(MediaTypeVideo),
		Provider:    string(prov),
		Status:      "queued",
		Mode:        request.Mode,
		ModeLabel:   ModeLabel(request.Mode),
		Model:       request.Model,
		Endpoint:    agnesVideosEndpoint,
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
		"mediaType":   string(MediaTypeVideo),
		"provider":    string(prov),
		"mode":        job.Mode,
		"model":       job.Model,
		"sourceCount": job.SourceCount,
		"numFrames":   request.Parameters.NumFrames,
		"frameRate":   request.Parameters.FrameRate,
	})
	s.appendMedia(ctx, job.ID, "media.job.queued", map[string]any{
		"mediaType": string(MediaTypeVideo),
		"provider":  string(prov),
	})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.video.job.created",
		RiskLevel: "low",
		Summary:   "Video generation job 创建",
		Payload: map[string]any{
			"jobId":       job.ID,
			"provider":    string(prov),
			"mode":        job.Mode,
			"model":       job.Model,
			"sourceCount": job.SourceCount,
			"numFrames":   request.Parameters.NumFrames,
			"frameRate":   request.Parameters.FrameRate,
		},
	})
	go s.runMediaVideoJob(context.Background(), job, request)
	return job, nil
}

func (s *Service) validateAgnesVideoRequest(request VideoRequest) error {
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
	refCount := len(request.Images)
	if refCount < cap.MinReferences || refCount > cap.MaxReferences {
		return ErrReferenceCount
	}
	if err := validateVideoModeReferences(request.Mode, refCount); err != nil {
		return err
	}
	width := request.Parameters.Width
	height := request.Parameters.Height
	if width <= 0 {
		width = cap.Parameters.DefaultWidth
	}
	if height <= 0 {
		height = cap.Parameters.DefaultHeight
	}
	if width <= 0 || height <= 0 {
		return errors.New("video width and height are required")
	}
	numFrames := request.Parameters.NumFrames
	if numFrames <= 0 {
		numFrames = cap.Parameters.DefaultNumFrames
	}
	if err := ValidateNumFrames(numFrames, cap.Parameters.MaxNumFrames); err != nil {
		return err
	}
	frameRate := request.Parameters.FrameRate
	if frameRate <= 0 {
		frameRate = cap.Parameters.DefaultFrameRate
	}
	if err := ValidateFrameRate(frameRate, cap.Parameters.MinFrameRate, cap.Parameters.MaxFrameRate); err != nil {
		return err
	}
	request.Parameters.Width = width
	request.Parameters.Height = height
	request.Parameters.NumFrames = numFrames
	request.Parameters.FrameRate = frameRate
	for _, img := range request.Images {
		if strings.HasPrefix(img.URL, "http") || strings.HasPrefix(img.URL, "data:image/") {
			if err := ValidateImageURL(img.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

func videoParametersWithDefaults(params VideoParameters, schema ModelParameterSchema) VideoParameters {
	if params.Width <= 0 {
		params.Width = schema.DefaultWidth
	}
	if params.Height <= 0 {
		params.Height = schema.DefaultHeight
	}
	if params.NumFrames <= 0 {
		params.NumFrames = schema.DefaultNumFrames
	}
	if params.FrameRate <= 0 {
		params.FrameRate = schema.DefaultFrameRate
	}
	return params
}

func validateVideoModeReferences(mode string, refCount int) error {
	switch mode {
	case VideoModeTextToVideo:
		if refCount != 0 {
			return ErrReferenceCount
		}
	case VideoModeImageToVideo:
		if refCount != 1 {
			return ErrReferenceCount
		}
	case VideoModeMultiImageVideo, VideoModeKeyframes:
		if refCount < 2 || refCount > 3 {
			return ErrReferenceCount
		}
	}
	return nil
}

func (s *Service) runMediaVideoJob(ctx context.Context, job storage.MediaGenerationJob, request VideoRequest) {
	prov := NormalizeProvider(job.Provider)
	provSettings, err := s.Store.GetMediaProviderSettings(ctx, string(prov))
	if err != nil {
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, err.Error())
		return
	}
	if !provSettings.HasAPIKey {
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, ErrAgnesAPIKeyMissing.Error())
		return
	}
	resolved, err := s.resolveMediaLibraryVideoInputs(ctx, request)
	if err != nil {
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, err.Error())
		return
	}
	request = resolved

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	callStarted := time.Now()
	if s.Log != nil {
		s.Log.Debug("agnes video create request started", "job_id", job.ID, "model", request.Model, "mode", request.Mode, "num_frames", request.Parameters.NumFrames)
	}
	createResult, err := s.Agnes.CreateVideo(callCtx, provSettings.APIKey, request)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("agnes video create failed", "job_id", job.ID, "model", request.Model, "mode", request.Mode, "latency_ms", time.Since(callStarted).Milliseconds(), "error", safelog.Error(err, 240))
		}
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, err.Error())
		return
	}
	if s.Log != nil {
		s.Log.Debug("agnes video create completed", "job_id", job.ID, "task_id", safelog.Text(createResult.ProviderTaskID, 32), "status", createResult.Status, "latency_ms", time.Since(callStarted).Milliseconds())
	}
	_ = s.Store.SetMediaJobProviderIDs(ctx, job.ID, createResult.ProviderTaskID, createResult.ProviderVideoID, createResult.Status)
	if createResult.Status == "queued" || createResult.Status == "" {
		_ = s.Store.UpdateMediaJobProgress(ctx, job.ID, createResult.Status, 0)
	}
	s.appendMedia(ctx, job.ID, "media.video.task_created", map[string]any{
		"status":         createResult.Status,
		"providerTaskId": safelog.Text(createResult.ProviderTaskID, 24),
	})
	if s.videoPoller != nil {
		s.videoPoller.track(job.ID)
		go s.videoPoller.pollLoop(context.Background(), job)
	}
}

func (s *Service) resolveMediaLibraryVideoInputs(ctx context.Context, request VideoRequest) (VideoRequest, error) {
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
			return VideoRequest{}, err
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

func (s *Service) finalizeVideoJob(ctx context.Context, job storage.MediaGenerationJob, poll *VideoPollResult, apiKey string) {
	if poll.VideoURL == "" {
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, "provider completed but video URL is missing")
		return
	}
	s.appendMedia(ctx, job.ID, "media.video.download_started", map[string]any{
		"width":     poll.Width,
		"height":    poll.Height,
		"numFrames": poll.NumFrames,
		"frameRate": poll.FrameRate,
		"seconds":   poll.Seconds,
	})
	downloadStarted := time.Now()
	data, mimeType, err := s.downloadVideo(ctx, poll.VideoURL)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("video download failed", "job_id", job.ID, "source_host", safelog.HostLabel(poll.VideoURL), "error", safelog.Error(err, 240))
		}
		s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"mediaType": string(MediaTypeVideo), "message": safelog.Error(err, 200)})
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, "provider completed but generated video could not be stored; check media storage settings")
		return
	}
	if s.Log != nil {
		s.Log.Debug("video download completed", "job_id", job.ID, "bytes", len(data), "mime", mimeType, "latency_ms", time.Since(downloadStarted).Milliseconds())
	}
	if poll.SizeBytes <= 0 {
		poll.SizeBytes = int64(len(data))
	}
	if poll.Width <= 0 || poll.Height <= 0 {
		poll.Width, poll.Height = s.estimateVideoBounds(poll.NumFrames, poll.FrameRate)
	}
	asset := storage.MediaAsset{
		MediaType:       string(MediaTypeVideo),
		AssetType:       "generated",
		Status:          "available",
		Provider:        job.Provider,
		Model:           job.Model,
		JobID:           job.ID,
		SourceRole:      "output",
		Slot:            1,
		PromptPreview:   job.Prompt,
		MimeType:        mimeType,
		SizeBytes:       poll.SizeBytes,
		Width:           poll.Width,
		Height:          poll.Height,
		DurationSeconds: poll.Seconds,
		FrameRate:       poll.FrameRate,
		FrameCount:      poll.NumFrames,
		StorageBackend:  "local",
	}
	storageSettings, _ := s.Store.GetImageStorageSettings(ctx)
	createdAsset, createErr := s.Store.CreateMediaAsset(ctx, asset)
	if createErr != nil {
		if s.Log != nil {
			s.Log.Warn("video asset create failed", "job_id", job.ID, "error", safelog.Error(createErr, 200))
		}
		s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"mediaType": string(MediaTypeVideo), "message": safelog.Error(createErr, 200)})
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, "provider completed but generated video asset record could not be created")
		return
	}
	stored, storeErr := s.storeMediaAssetBytes(ctx, createdAsset, data, mimeType, storageSettings)
	if storeErr != nil {
		createdAsset.Status = "failed"
		createdAsset.LastError = storeErr.Error()
		_, _ = s.Store.UpdateMediaAsset(ctx, createdAsset)
		s.appendMedia(ctx, job.ID, "media.asset.store_failed", map[string]any{"assetId": createdAsset.ID, "mediaType": string(MediaTypeVideo), "message": safelog.Error(storeErr, 200)})
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, "provider completed but generated video could not be stored; check media storage settings")
		return
	}
	createdAsset = stored
	output := storage.MediaGenerationOutput{
		Slot:              1,
		MediaType:         string(MediaTypeVideo),
		AssetID:           createdAsset.ID,
		LocalName:         createdAsset.LocalName,
		MimeType:          createdAsset.MimeType,
		Storage:           createdAsset.StorageBackend,
		SizeBytes:         createdAsset.SizeBytes,
		RemoteURLRedacted: redactedURL(poll.VideoURL),
		Metadata: map[string]any{
			"width":     createdAsset.Width,
			"height":    createdAsset.Height,
			"seconds":   createdAsset.DurationSeconds,
			"frameRate": createdAsset.FrameRate,
			"numFrames": createdAsset.FrameCount,
		},
	}
	usage := map[string]any{
		"width":     poll.Width,
		"height":    poll.Height,
		"numFrames": poll.NumFrames,
		"frameRate": poll.FrameRate,
		"seconds":   poll.Seconds,
		"bytes":     int64(len(data)),
	}
	completed, err := s.Store.CompleteMediaGenerationJob(ctx, job.ID, agnesVideosEndpoint, usage, []storage.MediaGenerationOutput{output})
	if err != nil {
		_, _ = s.failMediaJob(ctx, job.ID, agnesVideosEndpoint, fmt.Sprintf("complete job failed: %v", err))
		return
	}
	s.appendMedia(ctx, job.ID, "media.video.download_completed", map[string]any{
		"bytes":   len(data),
		"storage": createdAsset.StorageBackend,
		"assetId": createdAsset.ID,
	})
	s.appendMedia(ctx, job.ID, "media.job.completed", map[string]any{
		"mediaType":   string(MediaTypeVideo),
		"provider":    completed.Provider,
		"mode":        completed.Mode,
		"model":       completed.Model,
		"outputCount": len(completed.Outputs),
		"seconds":     poll.Seconds,
	})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "media.video.job.completed",
		RiskLevel: "low",
		Summary:   "Video generation job 完成",
		Payload: map[string]any{
			"jobId":     completed.ID,
			"provider":  completed.Provider,
			"model":     completed.Model,
			"mode":      completed.Mode,
			"seconds":   poll.Seconds,
			"bytes":     int64(len(data)),
			"numFrames": poll.NumFrames,
		},
	})
}

func (s *Service) downloadVideo(ctx context.Context, rawURL string) ([]byte, string, error) {
	if rawURL == "" {
		return nil, "", errors.New("video url is empty")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Accept", "video/mp4,video/*;q=0.9,*/*;q=0.8")
	httpReq.Header.Set("User-Agent", remoteImageBrowserUserAgent)
	httpReq.Header.Set("Referer", "https://agnes-ai.com/")
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return nil, "", fmt.Errorf("video download failed: %s", safelog.Text(msg, 200))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxVideoDownloadBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > MaxVideoDownloadBytes {
		return nil, "", fmt.Errorf("video exceeds maximum download size of %d MB", MaxVideoDownloadBytes>>20)
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}

func (s *Service) estimateVideoBounds(numFrames, frameRate int) (int, int) {
	return 0, 0
}
