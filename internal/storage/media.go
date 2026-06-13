package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"phantom-lancer/internal/ids"
)

type MediaProviderSettings struct {
	Provider           string         `json:"provider"`
	Enabled            bool           `json:"enabled"`
	APIKey             string         `json:"-"`
	HasAPIKey          bool           `json:"hasApiKey"`
	MaskedAPIKey       string         `json:"maskedApiKey"`
	DefaultImageModel  string         `json:"defaultImageModel"`
	DefaultVideoModel  string         `json:"defaultVideoModel"`
	DefaultImageParams map[string]any `json:"defaultImageParams"`
	DefaultVideoParams map[string]any `json:"defaultVideoParams"`
	LastTestedAt       string         `json:"lastTestedAt,omitempty"`
	LastError          string         `json:"lastError,omitempty"`
	CreatedAt          string         `json:"createdAt"`
	UpdatedAt          string         `json:"updatedAt"`
}

type MediaGenerationSource struct {
	ID          string `json:"id"`
	JobID       string `json:"jobId"`
	AssetID     string `json:"assetId,omitempty"`
	Slot        int    `json:"slot"`
	SourceType  string `json:"sourceType"`
	SourceLabel string `json:"sourceLabel,omitempty"`
	SourceRole  string `json:"sourceRole,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	URLRedacted string `json:"urlRedacted,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type MediaGenerationOutput struct {
	ID                string         `json:"id"`
	JobID             string         `json:"jobId"`
	AssetID           string         `json:"assetId,omitempty"`
	Slot              int            `json:"slot"`
	MediaType         string         `json:"mediaType"`
	RemoteURLRedacted string         `json:"remoteUrlRedacted,omitempty"`
	LocalName         string         `json:"localName,omitempty"`
	MimeType          string         `json:"mimeType,omitempty"`
	RevisedPrompt     string         `json:"revisedPrompt,omitempty"`
	Storage           string         `json:"storage,omitempty"`
	SizeBytes         int64          `json:"sizeBytes,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         string         `json:"createdAt"`
}

type MediaGenerationJob struct {
	ID              string                  `json:"id"`
	MediaType       string                  `json:"mediaType"`
	Provider        string                  `json:"provider"`
	Status          string                  `json:"status"`
	Mode            string                  `json:"mode"`
	ModeLabel       string                  `json:"modeLabel"`
	Model           string                  `json:"model"`
	Endpoint        string                  `json:"endpoint,omitempty"`
	Prompt          string                  `json:"prompt"`
	Parameters      map[string]any          `json:"parameters"`
	SourceCount     int                     `json:"sourceCount"`
	OutputCount     int                     `json:"outputCount"`
	ProviderTaskID  string                  `json:"providerTaskId,omitempty"`
	ProviderVideoID string                  `json:"providerVideoId,omitempty"`
	ProviderStatus  string                  `json:"providerStatus,omitempty"`
	Progress        int                     `json:"progress,omitempty"`
	Usage           map[string]any          `json:"usage"`
	ErrorMessage    string                  `json:"errorMessage,omitempty"`
	CreatedAt       string                  `json:"createdAt"`
	StartedAt       string                  `json:"startedAt,omitempty"`
	CompletedAt     string                  `json:"completedAt,omitempty"`
	Sources         []MediaGenerationSource `json:"sources,omitempty"`
	Outputs         []MediaGenerationOutput `json:"outputs,omitempty"`
}

type MediaAsset struct {
	ID                     string  `json:"id"`
	MediaType              string  `json:"mediaType"`
	AssetType              string  `json:"assetType"`
	Status                 string  `json:"status"`
	Private                bool    `json:"private"`
	Provider               string  `json:"provider,omitempty"`
	Model                  string  `json:"model,omitempty"`
	JobID                  string  `json:"jobId,omitempty"`
	SourceRole             string  `json:"sourceRole,omitempty"`
	Slot                   int     `json:"slot,omitempty"`
	PromptPreview          string  `json:"promptPreview,omitempty"`
	RevisedPromptPreview   string  `json:"revisedPromptPreview,omitempty"`
	OriginalFilename       string  `json:"originalFilename,omitempty"`
	OriginalSourceRedacted string  `json:"originalSourceRedacted,omitempty"`
	MimeType               string  `json:"mimeType,omitempty"`
	Extension              string  `json:"extension,omitempty"`
	SizeBytes              int64   `json:"sizeBytes,omitempty"`
	Width                  int     `json:"width,omitempty"`
	Height                 int     `json:"height,omitempty"`
	DurationSeconds        float64 `json:"durationSeconds,omitempty"`
	FrameRate              int     `json:"frameRate,omitempty"`
	FrameCount             int     `json:"frameCount,omitempty"`
	ChecksumSHA256         string  `json:"checksumSha256,omitempty"`
	LocalName              string  `json:"localName,omitempty"`
	URL                    string  `json:"url,omitempty"`
	DownloadURL            string  `json:"downloadUrl,omitempty"`
	StorageBackend         string  `json:"storageBackend"`
	ObjectStorageProfileID string  `json:"objectStorageProfileId,omitempty"`
	S3Bucket               string  `json:"s3Bucket,omitempty"`
	S3Region               string  `json:"s3Region,omitempty"`
	S3EndpointLabel        string  `json:"s3EndpointLabel,omitempty"`
	S3Key                  string  `json:"s3Key,omitempty"`
	S3ETag                 string  `json:"s3Etag,omitempty"`
	PrivateAt              string  `json:"privateAt,omitempty"`
	ArchivedAt             string  `json:"archivedAt,omitempty"`
	DeletedAt              string  `json:"deletedAt,omitempty"`
	DeletedReason          string  `json:"deletedReason,omitempty"`
	LastError              string  `json:"lastError,omitempty"`
	CreatedAt              string  `json:"createdAt"`
	UpdatedAt              string  `json:"updatedAt"`
}

func NormalizeMediaProviderSettings(s MediaProviderSettings) MediaProviderSettings {
	s.Provider = strings.TrimSpace(strings.ToLower(s.Provider))
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.HasAPIKey = s.APIKey != ""
	s.MaskedAPIKey = maskStoredSecret(s.APIKey)
	s.DefaultImageModel = strings.TrimSpace(s.DefaultImageModel)
	s.DefaultVideoModel = strings.TrimSpace(s.DefaultVideoModel)
	if s.DefaultImageParams == nil {
		s.DefaultImageParams = map[string]any{}
	}
	if s.DefaultVideoParams == nil {
		s.DefaultVideoParams = map[string]any{}
	}
	s.LastError = strings.TrimSpace(s.LastError)
	return s
}

func (s *Store) EnsureMediaProviderSettings(ctx context.Context, provider string) error {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		return errors.New("provider is required")
	}
	now := now()
	imageParams, _ := json.Marshal(map[string]any{})
	videoParams, _ := json.Marshal(map[string]any{})
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO media_provider_settings (
  provider, enabled, api_key, api_key_masked, default_image_model, default_video_model,
  default_image_params_json, default_video_params_json, last_tested_at, last_error,
  created_at, updated_at
) VALUES (?, 1, '', '', '', '', ?, ?, '', '', ?, ?)`,
		provider, string(imageParams), string(videoParams), now, now)
	return err
}

func (s *Store) GetMediaProviderSettings(ctx context.Context, provider string) (MediaProviderSettings, error) {
	if err := s.EnsureMediaProviderSettings(ctx, provider); err != nil {
		return MediaProviderSettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `
SELECT provider, enabled, api_key, api_key_masked, default_image_model, default_video_model,
       default_image_params_json, default_video_params_json, last_tested_at, last_error,
       created_at, updated_at
FROM media_provider_settings WHERE provider = ?`, provider)
	settings, err := scanMediaProviderSettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NormalizeMediaProviderSettings(MediaProviderSettings{Provider: provider}), nil
	}
	if err != nil {
		return MediaProviderSettings{}, err
	}
	return NormalizeMediaProviderSettings(settings), nil
}

func (s *Store) ListMediaProviderSettings(ctx context.Context) ([]MediaProviderSettings, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT provider, enabled, api_key, api_key_masked, default_image_model, default_video_model,
       default_image_params_json, default_video_params_json, last_tested_at, last_error,
       created_at, updated_at
FROM media_provider_settings ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaProviderSettings{}
	for rows.Next() {
		s, err := scanMediaProviderSettings(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, NormalizeMediaProviderSettings(s))
	}
	return out, rows.Err()
}

func (s *Store) UpdateMediaProviderSettings(ctx context.Context, settings MediaProviderSettings, updateAPIKey bool, clearAPIKey bool) (MediaProviderSettings, error) {
	existing, err := s.GetMediaProviderSettings(ctx, settings.Provider)
	if err != nil {
		return MediaProviderSettings{}, err
	}
	settings = NormalizeMediaProviderSettings(settings)
	provider := settings.Provider
	if provider == "" {
		return MediaProviderSettings{}, errors.New("provider is required")
	}
	if clearAPIKey {
		settings.APIKey = ""
	} else if !updateAPIKey {
		settings.APIKey = existing.APIKey
	}
	settings.MaskedAPIKey = maskStoredSecret(settings.APIKey)
	settings.HasAPIKey = settings.APIKey != ""

	imageParamsJSON := "{}"
	if len(settings.DefaultImageParams) > 0 {
		data, _ := json.Marshal(settings.DefaultImageParams)
		imageParamsJSON = string(data)
	}
	videoParamsJSON := "{}"
	if len(settings.DefaultVideoParams) > 0 {
		data, _ := json.Marshal(settings.DefaultVideoParams)
		videoParamsJSON = string(data)
	}

	n := now()
	if existing.CreatedAt != "" {
		settings.CreatedAt = existing.CreatedAt
	}
	if settings.CreatedAt == "" {
		settings.CreatedAt = n
	}
	settings.UpdatedAt = n
	_, err = s.db.ExecContext(ctx, `
INSERT INTO media_provider_settings (
  provider, enabled, api_key, api_key_masked, default_image_model, default_video_model,
  default_image_params_json, default_video_params_json, last_tested_at, last_error,
  created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider) DO UPDATE SET
  enabled = excluded.enabled,
  api_key = excluded.api_key,
  api_key_masked = excluded.api_key_masked,
  default_image_model = excluded.default_image_model,
  default_video_model = excluded.default_video_model,
  default_image_params_json = excluded.default_image_params_json,
  default_video_params_json = excluded.default_video_params_json,
  last_tested_at = excluded.last_tested_at,
  last_error = excluded.last_error,
  updated_at = excluded.updated_at`,
		provider, boolInt(settings.Enabled), settings.APIKey, settings.MaskedAPIKey,
		settings.DefaultImageModel, settings.DefaultVideoModel,
		imageParamsJSON, videoParamsJSON, settings.LastTestedAt, settings.LastError,
		settings.CreatedAt, settings.UpdatedAt)
	if err != nil {
		return MediaProviderSettings{}, err
	}
	return s.GetMediaProviderSettings(ctx, provider)
}

func (s *Store) TestMediaProviderSettings(ctx context.Context, provider string, success bool, errMsg string) error {
	if err := s.EnsureMediaProviderSettings(ctx, provider); err != nil {
		return err
	}
	n := now()
	safeErr := previewText(strings.TrimSpace(errMsg), 240)
	_, err := s.db.ExecContext(ctx, `
UPDATE media_provider_settings SET last_tested_at = ?, last_error = ?, updated_at = ? WHERE provider = ?`,
		n, safeErr, n, provider)
	return err
}

func scanMediaProviderSettings(row workspaceScanner) (MediaProviderSettings, error) {
	var s MediaProviderSettings
	var enabledInt int
	var apiKey, apiKeyMasked, imageParamsJSON, videoParamsJSON, lastTested, lastErr string
	err := row.Scan(&s.Provider, &enabledInt, &apiKey, &apiKeyMasked,
		&s.DefaultImageModel, &s.DefaultVideoModel,
		&imageParamsJSON, &videoParamsJSON, &lastTested, &lastErr,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return MediaProviderSettings{}, err
	}
	s.Enabled = enabledInt != 0
	s.APIKey = apiKey
	s.MaskedAPIKey = apiKeyMasked
	s.HasAPIKey = strings.TrimSpace(apiKey) != ""
	s.LastTestedAt = lastTested
	s.LastError = lastErr
	if imageParamsJSON != "" {
		_ = json.Unmarshal([]byte(imageParamsJSON), &s.DefaultImageParams)
	}
	if s.DefaultImageParams == nil {
		s.DefaultImageParams = map[string]any{}
	}
	if videoParamsJSON != "" {
		_ = json.Unmarshal([]byte(videoParamsJSON), &s.DefaultVideoParams)
	}
	if s.DefaultVideoParams == nil {
		s.DefaultVideoParams = map[string]any{}
	}
	return s, nil
}

func (s *Store) CreateMediaGenerationJob(ctx context.Context, job MediaGenerationJob, sources []MediaGenerationSource) (MediaGenerationJob, error) {
	id, err := ids.New("medjob")
	if err != nil {
		return MediaGenerationJob{}, err
	}
	n := now()
	job.ID = id
	if job.MediaType == "" {
		job.MediaType = "image"
	}
	if job.Provider == "" {
		job.Provider = "xai"
	}
	if job.Status == "" {
		job.Status = "queued"
	}
	job.SourceCount = len(sources)
	job.CreatedAt = n
	if job.Status == "running" && job.StartedAt == "" {
		job.StartedAt = n
	}
	paramsJSON := "{}"
	if len(job.Parameters) > 0 {
		data, _ := json.Marshal(job.Parameters)
		paramsJSON = string(data)
	}
	usageJSON := "{}"
	if len(job.Usage) > 0 {
		data, _ := json.Marshal(job.Usage)
		usageJSON = string(data)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaGenerationJob{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO media_generation_jobs (
  id, media_type, provider, status, mode, mode_label, model, endpoint, prompt,
  parameters_json, source_count, output_count, provider_task_id, provider_video_id,
  provider_status, progress, usage_json, error_message, created_at, started_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.MediaType, job.Provider, job.Status, job.Mode, job.ModeLabel,
		job.Model, job.Endpoint, job.Prompt, paramsJSON, job.SourceCount, job.OutputCount,
		job.ProviderTaskID, job.ProviderVideoID, job.ProviderStatus, job.Progress,
		usageJSON, job.ErrorMessage, job.CreatedAt, job.StartedAt, job.CompletedAt)
	if err != nil {
		return MediaGenerationJob{}, err
	}
	job.Sources = make([]MediaGenerationSource, 0, len(sources))
	for _, src := range sources {
		srcID, err := ids.New("medsrc")
		if err != nil {
			return MediaGenerationJob{}, err
		}
		src.ID = srcID
		src.JobID = job.ID
		src.CreatedAt = n
		_, err = tx.ExecContext(ctx, `
INSERT INTO media_generation_sources (
  id, job_id, asset_id, slot, source_type, source_label, source_role, mime_type, size_bytes, url_redacted, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			src.ID, src.JobID, src.AssetID, src.Slot, src.SourceType, src.SourceLabel,
			src.SourceRole, src.MimeType, src.SizeBytes, src.URLRedacted, src.CreatedAt)
		if err != nil {
			return MediaGenerationJob{}, err
		}
		job.Sources = append(job.Sources, src)
	}
	if err := tx.Commit(); err != nil {
		return MediaGenerationJob{}, err
	}
	return job, nil
}

func (s *Store) StartMediaGenerationJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE media_generation_jobs SET status = 'running', started_at = ?, error_message = ''
WHERE id = ? AND status IN ('queued', 'running')`, now(), id)
	return err
}

func (s *Store) UpdateMediaJobProgress(ctx context.Context, id, providerStatus string, progress int) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE media_generation_jobs SET provider_status = ?, progress = ? WHERE id = ?`,
		providerStatus, progress, id)
	return err
}

func (s *Store) SetMediaJobProviderIDs(ctx context.Context, id, taskID, videoID, providerStatus string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE media_generation_jobs SET provider_task_id = ?, provider_video_id = ?, provider_status = ? WHERE id = ?`,
		taskID, videoID, providerStatus, id)
	return err
}

func (s *Store) CompleteMediaGenerationJob(ctx context.Context, id, endpoint string, usage map[string]any, outputs []MediaGenerationOutput) (MediaGenerationJob, error) {
	n := now()
	usageJSON, _ := json.Marshal(usage)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaGenerationJob{}, err
	}
	defer tx.Rollback()
	outputCount := len(outputs)
	_, err = tx.ExecContext(ctx, `
UPDATE media_generation_jobs SET status = 'success', endpoint = ?, output_count = ?,
  usage_json = ?, completed_at = ?, error_message = ''
WHERE id = ?`, endpoint, outputCount, string(usageJSON), n, id)
	if err != nil {
		return MediaGenerationJob{}, err
	}
	for _, output := range outputs {
		outputID, err := ids.New("medout")
		if err != nil {
			return MediaGenerationJob{}, err
		}
		output.ID = outputID
		output.JobID = id
		output.CreatedAt = n
		metadataJSON := "{}"
		if len(output.Metadata) > 0 {
			data, _ := json.Marshal(output.Metadata)
			metadataJSON = string(data)
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO media_generation_outputs (
  id, job_id, asset_id, slot, media_type, remote_url_redacted, local_name, mime_type,
  revised_prompt, storage, size_bytes, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			output.ID, output.JobID, output.AssetID, output.Slot, output.MediaType,
			output.RemoteURLRedacted, output.LocalName, output.MimeType,
			output.RevisedPrompt, output.Storage, output.SizeBytes, metadataJSON, output.CreatedAt)
		if err != nil {
			return MediaGenerationJob{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MediaGenerationJob{}, err
	}
	return s.GetMediaGenerationJob(ctx, id)
}

func (s *Store) FailMediaGenerationJob(ctx context.Context, id, endpoint, message string) (MediaGenerationJob, error) {
	safeMsg := previewText(message, 300)
	_, err := s.db.ExecContext(ctx, `
UPDATE media_generation_jobs SET status = 'failed', endpoint = ?, error_message = ?, completed_at = ? WHERE id = ?`,
		endpoint, safeMsg, now(), id)
	if err != nil {
		return MediaGenerationJob{}, err
	}
	return s.GetMediaGenerationJob(ctx, id)
}

func (s *Store) CancelMediaGenerationJob(ctx context.Context, id, message string) error {
	if message == "" {
		message = "cancelled by user"
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE media_generation_jobs SET status = 'interrupted', error_message = ?, completed_at = ? WHERE id = ? AND status IN ('queued', 'running', 'provider_queued')`,
		previewText(message, 300), now(), id)
	return err
}

func (s *Store) InterruptStaleMediaJobs(ctx context.Context, message string) ([]string, error) {
	staleWhere := `status IN ('queued', 'running', 'provider_queued')
AND NOT (
  media_type = 'video'
  AND (provider_task_id != '' OR provider_video_id != '')
)`
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM media_generation_jobs WHERE `+staleWhere)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idsList := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		idsList = append(idsList, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(idsList) == 0 {
		return nil, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE media_generation_jobs SET status = 'interrupted', error_message = ?, completed_at = ? WHERE `+staleWhere,
		previewText(message, 300), now())
	return idsList, err
}

func (s *Store) ListActiveMediaVideoJobs(ctx context.Context) ([]MediaGenerationJob, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, media_type, provider, status, mode, mode_label, model, endpoint, prompt,
       parameters_json, source_count, output_count, provider_task_id, provider_video_id,
       provider_status, progress, usage_json, error_message, created_at, started_at, completed_at
FROM media_generation_jobs
WHERE media_type = 'video' AND status IN ('queued', 'running', 'provider_queued')
ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaGenerationJob{}
	for rows.Next() {
		job, err := scanMediaGenerationJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) GetMediaGenerationJob(ctx context.Context, id string) (MediaGenerationJob, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, media_type, provider, status, mode, mode_label, model, endpoint, prompt,
       parameters_json, source_count, output_count, provider_task_id, provider_video_id,
       provider_status, progress, usage_json, error_message, created_at, started_at, completed_at
FROM media_generation_jobs WHERE id = ?`, id)
	job, err := scanMediaGenerationJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaGenerationJob{}, ErrNotFound
	}
	if err != nil {
		return MediaGenerationJob{}, err
	}
	if err := s.attachMediaJobRelations(ctx, &job); err != nil {
		return MediaGenerationJob{}, err
	}
	return job, nil
}

func (s *Store) ListMediaGenerationJobs(ctx context.Context, limit int, mediaType, provider, status, mode, model string) ([]MediaGenerationJob, error) {
	if limit <= 0 || limit > 400 {
		limit = 120
	}
	query := `
SELECT id, media_type, provider, status, mode, mode_label, model, endpoint, prompt,
       parameters_json, source_count, output_count, provider_task_id, provider_video_id,
       provider_status, progress, usage_json, error_message, created_at, started_at, completed_at
FROM media_generation_jobs`
	args := []any{}
	clauses := []string{}
	if mediaType = strings.TrimSpace(mediaType); mediaType != "" {
		clauses = append(clauses, "media_type = ?")
		args = append(args, mediaType)
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		clauses = append(clauses, "provider = ?")
		args = append(args, provider)
	}
	if status = strings.TrimSpace(status); status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if mode = strings.TrimSpace(mode); mode != "" {
		clauses = append(clauses, "mode = ?")
		args = append(args, mode)
	}
	if model = strings.TrimSpace(model); model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, model)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []MediaGenerationJob{}
	for rows.Next() {
		job, err := scanMediaGenerationJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	return out, nil
}

func (s *Store) CountMediaGenerationJobs(ctx context.Context, mediaType, provider string) (int, error) {
	query := `SELECT COUNT(*) FROM media_generation_jobs WHERE status != 'deleted'`
	args := []any{}
	if mediaType = strings.TrimSpace(mediaType); mediaType != "" {
		query += " AND media_type = ?"
		args = append(args, mediaType)
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		query += " AND provider = ?"
		args = append(args, provider)
	}
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *Store) PruneMediaGenerationJobs(ctx context.Context, retention int) error {
	if retention <= 0 {
		retention = 500
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id FROM media_generation_jobs ORDER BY created_at DESC LIMIT -1 OFFSET ?`, retention)
	if err != nil {
		return err
	}
	defer rows.Close()
	idsList := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		idsList = append(idsList, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(idsList) == 0 {
		return nil
	}
	placeholders := make([]string, len(idsList))
	args := make([]any, len(idsList))
	for i, id := range idsList {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := strings.Join(placeholders, ",")
	if _, err := s.db.ExecContext(ctx, `DELETE FROM media_generation_sources WHERE job_id IN (`+inClause+`)`, args...); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM media_generation_outputs WHERE job_id IN (`+inClause+`)`, args...); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM media_generation_jobs WHERE id IN (`+inClause+`)`, args...)
	return err
}

func scanMediaGenerationJob(row workspaceScanner) (MediaGenerationJob, error) {
	var job MediaGenerationJob
	var paramsJSON, usageJSON string
	err := row.Scan(&job.ID, &job.MediaType, &job.Provider, &job.Status, &job.Mode, &job.ModeLabel,
		&job.Model, &job.Endpoint, &job.Prompt, &paramsJSON, &job.SourceCount, &job.OutputCount,
		&job.ProviderTaskID, &job.ProviderVideoID, &job.ProviderStatus, &job.Progress,
		&usageJSON, &job.ErrorMessage, &job.CreatedAt, &job.StartedAt, &job.CompletedAt)
	if err != nil {
		return MediaGenerationJob{}, err
	}
	if paramsJSON != "" {
		_ = json.Unmarshal([]byte(paramsJSON), &job.Parameters)
	}
	if job.Parameters == nil {
		job.Parameters = map[string]any{}
	}
	if usageJSON != "" {
		_ = json.Unmarshal([]byte(usageJSON), &job.Usage)
	}
	if job.Usage == nil {
		job.Usage = map[string]any{}
	}
	return job, nil
}

func (s *Store) attachMediaJobRelations(ctx context.Context, job *MediaGenerationJob) error {
	sources, err := s.listMediaGenerationSources(ctx, job.ID)
	if err != nil {
		return err
	}
	job.Sources = sources
	outputs, err := s.listMediaGenerationOutputs(ctx, job.ID)
	if err != nil {
		return err
	}
	job.Outputs = outputs
	return nil
}

func (s *Store) listMediaGenerationSources(ctx context.Context, jobID string) ([]MediaGenerationSource, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, job_id, asset_id, slot, source_type, source_label, source_role, mime_type, size_bytes, url_redacted, created_at
FROM media_generation_sources WHERE job_id = ? ORDER BY slot`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaGenerationSource{}
	for rows.Next() {
		var src MediaGenerationSource
		if err := rows.Scan(&src.ID, &src.JobID, &src.AssetID, &src.Slot,
			&src.SourceType, &src.SourceLabel, &src.SourceRole, &src.MimeType, &src.SizeBytes,
			&src.URLRedacted, &src.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *Store) listMediaGenerationOutputs(ctx context.Context, jobID string) ([]MediaGenerationOutput, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, job_id, asset_id, slot, media_type, remote_url_redacted, local_name, mime_type,
       revised_prompt, storage, size_bytes, metadata_json, created_at
FROM media_generation_outputs WHERE job_id = ? ORDER BY slot`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaGenerationOutput{}
	for rows.Next() {
		var o MediaGenerationOutput
		var metadataJSON string
		if err := rows.Scan(&o.ID, &o.JobID, &o.AssetID, &o.Slot, &o.MediaType,
			&o.RemoteURLRedacted, &o.LocalName, &o.MimeType, &o.RevisedPrompt,
			&o.Storage, &o.SizeBytes, &metadataJSON, &o.CreatedAt); err != nil {
			return nil, err
		}
		if metadataJSON != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &o.Metadata)
		}
		if o.Metadata == nil {
			o.Metadata = map[string]any{}
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) CreateMediaAsset(ctx context.Context, asset MediaAsset) (MediaAsset, error) {
	id, err := ids.New("medasset")
	if err != nil {
		return MediaAsset{}, err
	}
	n := now()
	asset.ID = id
	if asset.Status == "" {
		asset.Status = "available"
	}
	if asset.StorageBackend == "" {
		asset.StorageBackend = "local"
	}
	asset.CreatedAt = n
	asset.UpdatedAt = n
	_, err = s.db.ExecContext(ctx, `
INSERT INTO media_assets (
  id, media_type, asset_type, status, private, provider, model, job_id, source_role, slot,
  prompt_preview, revised_prompt_preview, original_filename, original_source_redacted,
  mime_type, extension, size_bytes, width, height, duration_seconds, frame_rate, frame_count,
  checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region,
  s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason,
  last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, asset.MediaType, asset.AssetType, asset.Status, boolInt(asset.Private),
		asset.Provider, asset.Model, asset.JobID, asset.SourceRole, asset.Slot,
		asset.PromptPreview, asset.RevisedPromptPreview, asset.OriginalFilename,
		asset.OriginalSourceRedacted, asset.MimeType, asset.Extension, asset.SizeBytes,
		asset.Width, asset.Height, asset.DurationSeconds, asset.FrameRate, asset.FrameCount,
		asset.ChecksumSHA256, asset.LocalName, asset.StorageBackend, asset.ObjectStorageProfileID,
		asset.S3Bucket, asset.S3Region, asset.S3EndpointLabel, asset.S3Key, asset.S3ETag,
		asset.PrivateAt, asset.ArchivedAt, asset.DeletedAt, asset.DeletedReason,
		asset.LastError, asset.CreatedAt, asset.UpdatedAt)
	if err != nil {
		return MediaAsset{}, err
	}
	return asset, nil
}

func (s *Store) GetMediaAsset(ctx context.Context, id string) (*MediaAsset, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, media_type, asset_type, status, private, provider, model, job_id, source_role, slot,
  prompt_preview, revised_prompt_preview, original_filename, original_source_redacted,
  mime_type, extension, size_bytes, width, height, duration_seconds, frame_rate, frame_count,
  checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region,
  s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason,
  last_error, created_at, updated_at
FROM media_assets WHERE id = $1 AND deleted_at IS NULL`, id)
	asset, err := scanMediaAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("media_asset_not_found")
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (s *Store) SetMediaAssetPrivate(ctx context.Context, id string, private bool) (*MediaAsset, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE media_assets SET private = $1,
  private_at = (CASE WHEN $1 THEN NOW() ELSE NULL END),
  updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL
RETURNING id, media_type, asset_type, status, private, provider, model, job_id, source_role, slot,
  prompt_preview, revised_prompt_preview, original_filename, original_source_redacted,
  mime_type, extension, size_bytes, width, height, duration_seconds, frame_rate, frame_count,
  checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region,
  s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason,
  last_error, created_at, updated_at`, boolInt(private), id)
	asset, err := scanMediaAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (s *Store) UpdateMediaAssetStorage(ctx context.Context, id, backend, profileID, bucket, region, endpointLabel, key, etag string) (*MediaAsset, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE media_assets SET storage_backend = $1, object_storage_profile_id = $2,
  s3_bucket = $3, s3_region = $4, s3_endpoint_label = $5, s3_key = $6, s3_etag = $7,
  archived_at = NOW(), updated_at = NOW()
WHERE id = $8 AND deleted_at IS NULL
RETURNING id, media_type, asset_type, status, private, provider, model, job_id, source_role, slot,
  prompt_preview, revised_prompt_preview, original_filename, original_source_redacted,
  mime_type, extension, size_bytes, width, height, duration_seconds, frame_rate, frame_count,
  checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region,
  s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason,
  last_error, created_at, updated_at`,
		backend, nullableText(profileID), bucket, region, endpointLabel, key, etag, id)
	asset, err := scanMediaAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) UpdateMediaAsset(ctx context.Context, asset MediaAsset) (MediaAsset, error) {
	n := now()
	asset.UpdatedAt = n
	_, err := s.db.ExecContext(ctx, `
UPDATE media_assets SET
  media_type = ?, asset_type = ?, status = ?, private = ?, provider = ?, model = ?,
  job_id = ?, source_role = ?, slot = ?, prompt_preview = ?, revised_prompt_preview = ?,
  original_filename = ?, original_source_redacted = ?, mime_type = ?, extension = ?,
  size_bytes = ?, width = ?, height = ?, duration_seconds = ?, frame_rate = ?, frame_count = ?,
  checksum_sha256 = ?, local_name = ?, storage_backend = ?, object_storage_profile_id = ?,
  s3_bucket = ?, s3_region = ?, s3_endpoint_label = ?, s3_key = ?, s3_etag = ?,
  private_at = ?, archived_at = ?, last_error = ?, updated_at = ?
WHERE id = ?`,
		asset.MediaType, asset.AssetType, asset.Status, boolInt(asset.Private),
		asset.Provider, asset.Model, asset.JobID, asset.SourceRole, asset.Slot,
		asset.PromptPreview, asset.RevisedPromptPreview, asset.OriginalFilename,
		asset.OriginalSourceRedacted, asset.MimeType, asset.Extension, asset.SizeBytes,
		asset.Width, asset.Height, asset.DurationSeconds, asset.FrameRate, asset.FrameCount,
		asset.ChecksumSHA256, asset.LocalName, asset.StorageBackend, asset.ObjectStorageProfileID,
		asset.S3Bucket, asset.S3Region, asset.S3EndpointLabel, asset.S3Key, asset.S3ETag,
		asset.PrivateAt, asset.ArchivedAt, asset.LastError, asset.UpdatedAt, asset.ID)
	if err != nil {
		return MediaAsset{}, err
	}
	updated, err := s.GetMediaAsset(ctx, asset.ID)
	if err != nil {
		return MediaAsset{}, err
	}
	return *updated, nil
}

func (s *Store) DeleteMediaAsset(ctx context.Context, id, reason string) (MediaAsset, error) {
	asset, err := s.GetMediaAsset(ctx, id)
	if err != nil {
		return MediaAsset{}, err
	}
	n := now()
	safeReason := previewText(reason, 200)
	_, err = s.db.ExecContext(ctx, `
UPDATE media_assets SET status = 'deleted', deleted_at = ?, deleted_reason = ?, updated_at = ? WHERE id = ?`,
		n, safeReason, n, id)
	if err != nil {
		return MediaAsset{}, err
	}
	asset.Status = "deleted"
	asset.DeletedAt = n
	asset.DeletedReason = safeReason
	asset.UpdatedAt = n
	return *asset, nil
}

func (s *Store) GetPublicMediaAssetByChecksum(ctx context.Context, checksum string) (MediaAsset, bool) {
	if checksum == "" {
		return MediaAsset{}, false
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, media_type, asset_type, status, private, provider, model, job_id, source_role, slot,
  prompt_preview, revised_prompt_preview, original_filename, original_source_redacted,
  mime_type, extension, size_bytes, width, height, duration_seconds, frame_rate, frame_count,
  checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region,
  s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason,
  last_error, created_at, updated_at
FROM media_assets
WHERE checksum_sha256 = ? AND private = 0 AND status = 'available'
LIMIT 1`, checksum)
	asset, err := scanMediaAsset(row)
	return asset, err == nil
}

func (s *Store) LinkMediaSourceAsset(ctx context.Context, sourceID, assetID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE media_generation_sources SET asset_id = ? WHERE id = ?`, assetID, sourceID)
	return err
}

func (s *Store) ListMediaAssets(ctx context.Context, limit int, mediaType, provider, assetType, status string, includePrivate bool) ([]MediaAsset, error) {
	if limit <= 0 || limit > 400 {
		limit = 120
	}
	query := `
SELECT id, media_type, asset_type, status, private, provider, model, job_id, source_role, slot,
  prompt_preview, revised_prompt_preview, original_filename, original_source_redacted,
  mime_type, extension, size_bytes, width, height, duration_seconds, frame_rate, frame_count,
  checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region,
  s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason,
  last_error, created_at, updated_at
FROM media_assets`
	args := []any{}
	clauses := []string{`status != 'deleted'`}
	if !includePrivate {
		clauses = append(clauses, `private = 0`)
	}
	if mediaType = strings.TrimSpace(mediaType); mediaType != "" {
		clauses = append(clauses, "media_type = ?")
		args = append(args, mediaType)
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		clauses = append(clauses, "provider = ?")
		args = append(args, provider)
	}
	if assetType = strings.TrimSpace(assetType); assetType != "" {
		clauses = append(clauses, "asset_type = ?")
		args = append(args, assetType)
	}
	if status = strings.TrimSpace(status); status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	query += " WHERE " + strings.Join(clauses, " AND ")
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaAsset{}
	for rows.Next() {
		a, err := scanMediaAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanMediaAsset(row workspaceScanner) (MediaAsset, error) {
	var a MediaAsset
	var privateInt int
	err := row.Scan(&a.ID, &a.MediaType, &a.AssetType, &a.Status, &privateInt,
		&a.Provider, &a.Model, &a.JobID, &a.SourceRole, &a.Slot,
		&a.PromptPreview, &a.RevisedPromptPreview, &a.OriginalFilename,
		&a.OriginalSourceRedacted, &a.MimeType, &a.Extension, &a.SizeBytes,
		&a.Width, &a.Height, &a.DurationSeconds, &a.FrameRate, &a.FrameCount,
		&a.ChecksumSHA256, &a.LocalName, &a.StorageBackend, &a.ObjectStorageProfileID,
		&a.S3Bucket, &a.S3Region, &a.S3EndpointLabel, &a.S3Key, &a.S3ETag,
		&a.PrivateAt, &a.ArchivedAt, &a.DeletedAt, &a.DeletedReason,
		&a.LastError, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return MediaAsset{}, err
	}
	a.Private = privateInt != 0
	if a.Status == "" {
		a.Status = "available"
	}
	if a.StorageBackend == "" {
		a.StorageBackend = "local"
	}
	a.URL = "/api/images/media-assets/" + a.ID + "/content"
	a.DownloadURL = "/api/images/media-assets/" + a.ID + "/download"
	return a, nil
}
