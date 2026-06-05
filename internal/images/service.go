package images

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"phantom-lancer/internal/events"
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
}

func NewService(store *storage.Store, hub *events.Hub, dataDir string, logger *slog.Logger) *Service {
	return &Service{
		Store:  store,
		Hub:    hub,
		Assets: NewAssetStore(filepath.Join(dataDir, "images", "generated"), nil),
		XAI:    NewXAIClient("https://api.x.ai/v1", nil),
		Log:    logger,
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
	count, _ := s.Store.CountImageGenerationJobs(ctx)
	jobs, _ := s.Store.ListImageGenerationJobs(ctx, 1, "", "")
	status := Status{
		Available:    true,
		Provider:     settings.Provider,
		HasAPIKey:    settings.HasAPIKey,
		MaskedAPIKey: settings.MaskedAPIKey,
		DefaultModel: settings.DefaultModel,
		HistoryCount: count,
	}
	if len(jobs) > 0 {
		status.LastJobID = jobs[0].ID
		status.LastJobStatus = jobs[0].Status
		status.LastError = jobs[0].ErrorMessage
		status.LastCompletedAt = jobs[0].CompletedAt
	}
	return status
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

	callCtx, cancel := context.WithTimeout(ctx, 145*time.Second)
	defer cancel()
	result, err := s.XAI.Imagine(callCtx, settings.XAIAPIKey, request)
	if err != nil {
		_, _ = s.failJob(ctx, job.ID, job.Endpoint, err.Error())
		return
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
	return s.Store.UpdateImageStorageSettings(ctx, settings, updateSecret, clearSecret)
}

func (s *Service) TestStorage(ctx context.Context, settings storage.ImageStorageSettings) error {
	client, err := NewS3ObjectStore(settings)
	if err != nil {
		return err
	}
	return client.Test(ctx)
}

func (s *Service) ReadAsset(ctx context.Context, asset storage.ImageAsset) (string, []byte, error) {
	switch asset.StorageBackend {
	case "s3":
		settings, err := s.Store.GetImageStorageSettings(ctx)
		if err != nil {
			return "", nil, err
		}
		client, err := NewS3ObjectStore(settings)
		if err != nil {
			return "", nil, err
		}
		return client.Get(ctx, asset.S3Key)
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
		client, err := NewS3ObjectStore(settings)
		if err != nil {
			return storage.ImageAsset{}, err
		}
		if asset.S3Key != "" {
			if err := client.Delete(ctx, asset.S3Key); err != nil {
				asset.LastError = err.Error()
				_, _ = s.Store.UpdateImageAsset(ctx, asset)
				return storage.ImageAsset{}, err
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
	client, err := NewS3ObjectStore(settings)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	mimeType, data, err := s.Assets.ReadLocal(asset.LocalName)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	key := objectKey(settings, asset, asset.Extension)
	etag, err := client.Put(ctx, key, data, mimeType)
	if err != nil {
		asset.LastError = err.Error()
		_, _ = s.Store.UpdateImageAsset(ctx, asset)
		return storage.ImageAsset{}, err
	}
	localName := asset.LocalName
	asset.StorageBackend = "s3"
	asset.LocalName = ""
	asset.S3Bucket = settings.S3Bucket
	asset.S3Region = settings.S3Region
	asset.S3EndpointLabel = settings.S3Endpoint
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
	return updated, nil
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
			outputs = append(outputs, output)
			continue
		}
		asset := storage.ImageAsset{
			AssetType:              "generated",
			Status:                 "available",
			Provider:               job.Provider,
			Model:                  job.Model,
			JobID:                  job.ID,
			SourceRole:             "output",
			Slot:                   index + 1,
			PromptPreview:          request.Prompt,
			RevisedPromptPreview:   image.RevisedPrompt,
			OriginalSourceRedacted: image.URL,
			MimeType:               mimeType,
		}
		created, err := s.Store.CreateImageAsset(ctx, asset)
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
	if settings.Backend == "s3" {
		if client, err := NewS3ObjectStore(settings); err == nil {
			key := objectKey(settings, asset, asset.Extension)
			if etag, err := client.Put(ctx, key, data, info.MimeType); err == nil {
				asset.StorageBackend = "s3"
				asset.S3Bucket = settings.S3Bucket
				asset.S3Region = settings.S3Region
				asset.S3EndpointLabel = settings.S3Endpoint
				asset.S3Key = key
				asset.S3ETag = etag
				asset.LastError = ""
				return s.Store.UpdateImageAsset(ctx, asset)
			} else if !settings.FallbackToLocal {
				return storage.ImageAsset{}, err
			}
		} else if !settings.FallbackToLocal {
			return storage.ImageAsset{}, err
		}
	}
	local, err := s.Assets.StoreBytes(asset.ID, data, info.MimeType)
	if err != nil {
		return storage.ImageAsset{}, err
	}
	asset.LocalName = local.LocalName
	asset.MimeType = local.MimeType
	asset.Extension = imageExt(local.MimeType)
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
	failed, err := s.Store.FailImageGenerationJob(ctx, jobID, endpoint, message)
	if err != nil {
		return storage.ImageGenerationJob{}, err
	}
	s.append(ctx, jobID, "images.job.failed", map[string]any{"message": message, "mode": failed.Mode, "model": failed.Model})
	_, _ = s.Store.AddAudit(ctx, storage.AuditEvent{
		EventType: "images.job.failed",
		RiskLevel: "medium",
		Summary:   "Images 生成调用失败",
		Payload:   map[string]any{"jobId": failed.ID, "mode": failed.Mode, "model": failed.Model, "error": message},
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
