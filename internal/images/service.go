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
	Log    *slog.Logger

	// LogSampler gates hot-path warnings (S3 put/fetch failures, output
	// store errors inside batch loops) so a transient backend outage does
	// not produce one Warn per asset per job. Interval is deliberately
	// longer than for DB-only failures because S3 blips may span a
	// minute or two and we want at most a handful of samples during a
	// short incident.
	LogSampler *logsampler.Sampler
}

func NewService(store *storage.Store, hub *events.Hub, dataDir string, logger *slog.Logger) *Service {
	return &Service{
		Store:      store,
		Hub:        hub,
		Assets:     NewAssetStore(filepath.Join(dataDir, "images", "generated"), nil),
		XAI:        NewXAIClient("https://api.x.ai/v1", nil),
		Log:        logger,
		LogSampler: logsampler.New(5 * time.Second),
	}
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
	return s.Store.UpdateImageProviderSettings(ctx, settings, updateAPIKey, clearAPIKey)
}

func (s *Service) CreateJob(ctx context.Context, request ImagineRequest) (storage.ImageGenerationJob, error) {
	request = NormalizeRequest(request)
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
	if asset.StorageBackend != "local" || asset.LocalName == "" {
		return storage.ImageAsset{}, errors.New("asset is not stored locally")
	}
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
	mimeType, data, err := s.Assets.ReadLocal(asset.LocalName)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	key := objectKey(settings, asset, asset.Extension)
	started := time.Now()
	if s.Log != nil {
		s.Log.Debug("image asset s3 archive started", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "bytes", len(data))
	}
	etag, err := client.Put(ctx, key, data, mimeType)
	if err != nil {
		asset.LastError = err.Error()
		_, _ = s.Store.UpdateImageAsset(ctx, asset)
		if s.Log != nil {
			s.Log.Warn("image asset s3 archive failed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		return storage.ImageAsset{}, err
	}
	localName := asset.LocalName
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
	updated, err := s.Store.UpdateImageAsset(ctx, asset)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	s.Assets.Remove([]string{localName})
	s.append(ctx, asset.JobID, "images.asset.archived.s3", map[string]any{"assetId": asset.ID, "key": key})
	if s.Log != nil {
		s.Log.Debug("image asset s3 archive completed", "asset_id", asset.ID, "job_id", asset.JobID, "bucket", bucket, "key", key, "latency_ms", time.Since(started).Milliseconds())
	}
	return updated, nil
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
		assetID := strings.TrimPrefix(image.URL, "asset:")
		asset, err := s.Store.GetImageAsset(ctx, assetID)
		if err != nil {
			return ImagineRequest{}, err
		}
		mimeType, data, err := s.ReadAsset(ctx, asset)
		if err != nil {
			return ImagineRequest{}, err
		}
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		if !AllowedImageMime(mimeType) {
			return ImagineRequest{}, errors.New("library image mime type is unsupported")
		}
		request.Images[index].URL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
		request.Images[index].MimeType = mimeType
		request.Images[index].SizeBytes = int64(len(data))
		if request.Images[index].SourceLabel == "" {
			request.Images[index].SourceLabel = asset.OriginalFilename
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
