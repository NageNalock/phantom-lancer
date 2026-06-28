package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ensureEmbeddingSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_embedding_config (
			id TEXT PRIMARY KEY,
			embedding_model_id TEXT,
			enabled INTEGER NOT NULL DEFAULT 0,
			last_probe_at DATETIME,
			last_probe_status TEXT,
			last_error TEXT,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS stockv2_embedding_assets (
			id TEXT PRIMARY KEY,
			object_type TEXT NOT NULL,
			object_id TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			text_summary TEXT,
			model_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			embedding_protocol TEXT NOT NULL,
			embedding_dimensions INTEGER NOT NULL,
			vector_ref TEXT NOT NULL,
			status TEXT NOT NULL,
			error_message TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(object_type, object_id, model_id, embedding_dimensions)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_object
			ON stockv2_embedding_assets(object_type, object_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_model_status
			ON stockv2_embedding_assets(model_id, embedding_dimensions, status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_status
			ON stockv2_embedding_assets(status);
		INSERT OR IGNORE INTO stockv2_embedding_config
			(id, enabled, updated_at)
		VALUES ('default', 0, datetime('now'));
	`)
	return wrapError(err, "ensure embedding schema")
}

func (s *Store) GetEmbeddingConfig(ctx context.Context) (EmbeddingConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(embedding_model_id,''), enabled, last_probe_at,
		       COALESCE(last_probe_status,''), COALESCE(last_error,''), updated_at
		FROM stockv2_embedding_config
		WHERE id = ?
	`, EmbeddingConfigDefaultID)
	config, err := scanEmbeddingConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EmbeddingConfig{
				ID:        EmbeddingConfigDefaultID,
				Enabled:   false,
				UpdatedAt: time.Now(),
			}, nil
		}
		return EmbeddingConfig{}, wrapError(err, "get embedding config")
	}
	return config, nil
}

func (s *Store) UpdateEmbeddingConfig(ctx context.Context, config EmbeddingConfig) (EmbeddingConfig, error) {
	if strings.TrimSpace(config.ID) == "" {
		config.ID = EmbeddingConfigDefaultID
	}
	config.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_config (
			id, embedding_model_id, enabled, last_probe_at, last_probe_status, last_error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			embedding_model_id = excluded.embedding_model_id,
			enabled = excluded.enabled,
			last_probe_at = excluded.last_probe_at,
			last_probe_status = excluded.last_probe_status,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, config.ID, nullableAgentString(config.EmbeddingModelID), boolToInt(config.Enabled),
		nullableAgentTime(config.LastProbeAt), nullableAgentString(config.LastProbeStatus),
		nullableAgentString(config.LastError), config.UpdatedAt)
	return config, wrapError(err, "update embedding config")
}

func scanEmbeddingConfig(row rowScanner) (EmbeddingConfig, error) {
	var config EmbeddingConfig
	var enabled int
	var lastProbeAt sql.NullTime
	if err := row.Scan(
		&config.ID,
		&config.EmbeddingModelID,
		&enabled,
		&lastProbeAt,
		&config.LastProbeStatus,
		&config.LastError,
		&config.UpdatedAt,
	); err != nil {
		return config, err
	}
	config.Enabled = enabled != 0
	if lastProbeAt.Valid {
		config.LastProbeAt = lastProbeAt.Time
	}
	return config, nil
}

func (s *Store) UpsertEmbeddingAsset(ctx context.Context, asset EmbeddingAsset) (EmbeddingAsset, error) {
	now := time.Now()
	if asset.ID == "" {
		asset.ID = generateID()
	}
	if asset.VectorRef == "" {
		asset.VectorRef = generateID()
	}
	if asset.Status == "" {
		asset.Status = EmbeddingAssetStatusReady
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = now
	}
	asset.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_assets (
			id, object_type, object_id, text_hash, text_summary, model_id, provider_id,
			embedding_protocol, embedding_dimensions, vector_ref, status, error_message,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_type, object_id, model_id, embedding_dimensions) DO UPDATE SET
			text_hash = excluded.text_hash,
			text_summary = excluded.text_summary,
			provider_id = excluded.provider_id,
			embedding_protocol = excluded.embedding_protocol,
			vector_ref = excluded.vector_ref,
			status = excluded.status,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`, asset.ID, asset.ObjectType, asset.ObjectID, asset.TextHash, nullableAgentString(asset.TextSummary),
		asset.ModelID, asset.ProviderID, asset.EmbeddingProtocol, asset.EmbeddingDimensions,
		asset.VectorRef, asset.Status, nullableAgentString(asset.ErrorMessage), asset.CreatedAt, asset.UpdatedAt)
	if err != nil {
		return EmbeddingAsset{}, wrapError(err, "upsert embedding asset")
	}
	row := s.db.QueryRowContext(ctx, embeddingAssetSelectSQL()+`
		WHERE object_type = ? AND object_id = ? AND model_id = ? AND embedding_dimensions = ?
	`, asset.ObjectType, asset.ObjectID, asset.ModelID, asset.EmbeddingDimensions)
	stored, err := scanEmbeddingAsset(row)
	if err != nil {
		return EmbeddingAsset{}, wrapError(err, "get upserted embedding asset")
	}
	return stored, nil
}

func (s *Store) MarkEmbeddingAssetsStaleForModelChange(ctx context.Context, modelID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE status = ? AND model_id <> ?
	`, EmbeddingAssetStatusStale, time.Now(), EmbeddingAssetStatusReady, strings.TrimSpace(modelID))
	return wrapError(err, "mark embedding assets stale for model change")
}

func (s *Store) MarkEmbeddingObjectAssetsStaleExcept(ctx context.Context, objectType, objectID, modelID string, dimensions int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE object_type = ? AND object_id = ? AND status = ?
		  AND (model_id <> ? OR embedding_dimensions <> ?)
	`, EmbeddingAssetStatusStale, time.Now(), strings.TrimSpace(objectType), strings.TrimSpace(objectID),
		EmbeddingAssetStatusReady, strings.TrimSpace(modelID), dimensions)
	return wrapError(err, "mark embedding object assets stale")
}

func (s *Store) ListEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) ([]EmbeddingAsset, error) {
	where, args := embeddingAssetFilterSQL(filter)
	args = append(args, normalizedEmbeddingLimit(filter.Limit), normalizedAgentOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY updated_at DESC LIMIT ? OFFSET ?`, embeddingAssetSelectSQL(), where), args...)
	if err != nil {
		return nil, wrapError(err, "list embedding assets")
	}
	defer rows.Close()
	items := make([]EmbeddingAsset, 0)
	for rows.Next() {
		item, err := scanEmbeddingAsset(rows)
		if err != nil {
			return nil, wrapError(err, "scan embedding asset")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate embedding assets")
}

func (s *Store) CountEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) (int, error) {
	where, args := embeddingAssetFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_embedding_assets WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count embedding assets")
	}
	return count, nil
}

func (s *Store) CountEmbeddingAssetsByStatus(ctx context.Context, status string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_assets WHERE status = ?`, strings.TrimSpace(status)).Scan(&count)
	return count, wrapError(err, "count embedding assets by status")
}

func (s *Store) ListReadyEmbeddingAssetsForSearch(ctx context.Context, objectType, modelID string, dimensions, limit int) ([]EmbeddingAsset, error) {
	return s.ListEmbeddingAssets(ctx, EmbeddingAssetListFilter{
		ObjectType: objectType,
		ModelID:    modelID,
		Dimensions: dimensions,
		Status:     EmbeddingAssetStatusReady,
		Limit:      limit,
	})
}

func embeddingAssetSelectSQL() string {
	return `
		SELECT id, object_type, object_id, text_hash, COALESCE(text_summary,''), model_id,
		       provider_id, embedding_protocol, embedding_dimensions, vector_ref, status,
		       COALESCE(error_message,''), created_at, updated_at
		FROM stockv2_embedding_assets`
}

func embeddingAssetFilterSQL(filter EmbeddingAssetListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0, 5)
	agentFilterAdd(&where, &args, "object_type", filter.ObjectType)
	agentFilterAdd(&where, &args, "object_id", filter.ObjectID)
	agentFilterAdd(&where, &args, "model_id", filter.ModelID)
	agentFilterAdd(&where, &args, "status", filter.Status)
	if filter.Dimensions > 0 {
		where = append(where, "embedding_dimensions = ?")
		args = append(args, filter.Dimensions)
	}
	return strings.Join(where, " AND "), args
}

func scanEmbeddingAsset(row rowScanner) (EmbeddingAsset, error) {
	var asset EmbeddingAsset
	err := row.Scan(
		&asset.ID,
		&asset.ObjectType,
		&asset.ObjectID,
		&asset.TextHash,
		&asset.TextSummary,
		&asset.ModelID,
		&asset.ProviderID,
		&asset.EmbeddingProtocol,
		&asset.EmbeddingDimensions,
		&asset.VectorRef,
		&asset.Status,
		&asset.ErrorMessage,
		&asset.CreatedAt,
		&asset.UpdatedAt,
	)
	return asset, err
}

func normalizedEmbeddingLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func (s *MarketDataStore) UpsertEmbeddingVector(ctx context.Context, vectorRef, modelID string, dimensions int, vector []float64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("market data store is not initialized")
	}
	now := time.Now()
	raw, _ := json.Marshal(vector)
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO stockv2_embedding_vectors
			(vector_ref, model_id, dimensions, vector_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, vectorRef, modelID, dimensions, string(raw), now, now)
	return wrapError(err, "upsert embedding vector")
}

func (s *MarketDataStore) GetEmbeddingVectors(ctx context.Context, refs []string) (map[string][]float64, error) {
	out := make(map[string][]float64, len(refs))
	if len(refs) == 0 {
		return out, nil
	}
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(refs)), ",")
	args := make([]any, 0, len(refs))
	for _, ref := range refs {
		args = append(args, ref)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT vector_ref, vector_json
		FROM stockv2_embedding_vectors
		WHERE vector_ref IN (%s)
	`, placeholders), args...)
	if err != nil {
		return nil, wrapError(err, "get embedding vectors")
	}
	defer rows.Close()
	for rows.Next() {
		var ref, raw string
		if err := rows.Scan(&ref, &raw); err != nil {
			return nil, wrapError(err, "scan embedding vector")
		}
		var vector []float64
		if err := json.Unmarshal([]byte(raw), &vector); err == nil && len(vector) > 0 {
			out[ref] = vector
		}
	}
	return out, wrapError(rows.Err(), "iterate embedding vectors")
}

func (s *Store) UpsertEmbeddingVector(ctx context.Context, vectorRef, modelID string, dimensions int, vector []float64) error {
	return s.marketDB.UpsertEmbeddingVector(ctx, vectorRef, modelID, dimensions, vector)
}

func (s *Store) GetEmbeddingVectors(ctx context.Context, refs []string) (map[string][]float64, error) {
	return s.marketDB.GetEmbeddingVectors(ctx, refs)
}
