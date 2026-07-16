package stockv2

import (
	"context"
	"strings"
	"time"
)

func (s *Store) ensureEmbeddingSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_embedding_config (
			id TEXT PRIMARY KEY,
			embedding_model_id TEXT,
			enabled INTEGER NOT NULL DEFAULT 0,
			auto_maintain_enabled INTEGER NOT NULL DEFAULT 0,
			maintain_interval_seconds INTEGER NOT NULL DEFAULT 600,
			maintain_batch_size INTEGER NOT NULL DEFAULT 50,
			maintain_rate_limit_ms INTEGER NOT NULL DEFAULT 500,
			last_probe_at DATETIME,
			last_probe_status TEXT,
			last_error TEXT,
			last_maintain_at DATETIME,
			next_maintain_at DATETIME,
			last_maintain_result TEXT,
			updated_at DATETIME NOT NULL
		);
		INSERT OR IGNORE INTO stockv2_embedding_config
			(id, embedding_model_id, enabled, last_probe_status, updated_at)
		VALUES
			('stockv2-embedding-config', '', 0, 'embedding_model_not_configured', datetime('now'));
		CREATE TABLE IF NOT EXISTS stockv2_embedding_assets (
			id TEXT PRIMARY KEY,
			object_type TEXT NOT NULL,
			object_id TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			text_summary TEXT,
			model_id TEXT NOT NULL,
			provider_id TEXT,
			embedding_protocol TEXT,
			embedding_dimensions INTEGER NOT NULL DEFAULT 0,
			vector_ref TEXT,
			status TEXT NOT NULL,
			error_message TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(object_type, object_id, model_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_object ON stockv2_embedding_assets(object_type, object_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_model ON stockv2_embedding_assets(model_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_status ON stockv2_embedding_assets(status);
		CREATE TABLE IF NOT EXISTS stockv2_news_context_mcp_verifications (
			thread_id TEXT PRIMARY KEY,
			version_id TEXT NOT NULL,
			status TEXT NOT NULL,
			checked_at DATETIME NOT NULL,
			verified_at DATETIME,
			error_message TEXT,
			updated_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_mcp_verifications_status
			ON stockv2_news_context_mcp_verifications(status, checked_at);
	`)
	if err != nil {
		return wrapError(err, "ensure embedding schema")
	}
	columns := []struct {
		name    string
		colType string
	}{
		{"auto_maintain_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"maintain_interval_seconds", "INTEGER NOT NULL DEFAULT 600"},
		{"maintain_batch_size", "INTEGER NOT NULL DEFAULT 50"},
		{"maintain_rate_limit_ms", "INTEGER NOT NULL DEFAULT 500"},
		{"last_maintain_at", "DATETIME"},
		{"next_maintain_at", "DATETIME"},
		{"last_maintain_result", "TEXT"},
	}
	for _, column := range columns {
		if err := s.ensureColumn(ctx, "stockv2_embedding_config", column.name, column.colType); err != nil {
			return wrapError(err, "ensure embedding config column "+column.name)
		}
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_config
		SET auto_maintain_enabled = 1,
			maintain_interval_seconds = CASE WHEN maintain_interval_seconds <= 0 THEN 600 ELSE maintain_interval_seconds END,
			maintain_batch_size = CASE WHEN maintain_batch_size <= 0 THEN 50 ELSE maintain_batch_size END,
			maintain_rate_limit_ms = CASE WHEN maintain_rate_limit_ms <= 0 THEN 500 ELSE maintain_rate_limit_ms END,
			next_maintain_at = COALESCE(next_maintain_at, datetime('now')),
			updated_at = datetime('now')
		WHERE enabled = 1
		  AND COALESCE(embedding_model_id, '') <> ''
		  AND auto_maintain_enabled = 0
		  AND last_maintain_at IS NULL
		  AND next_maintain_at IS NULL
		  AND COALESCE(last_maintain_result, '') = ''
	`)
	return wrapError(err, "migrate embedding maintenance defaults")
}

func (s *Store) MarkEmbeddingAssetsStaleForModelChange(ctx context.Context, modelID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE status = ? AND model_id <> ?
	`, EmbeddingAssetStatusStale, time.Now(), EmbeddingAssetStatusReady, strings.TrimSpace(modelID))
	if err != nil {
		return wrapError(err, "mark embedding assets stale for model change")
	}
	if _, err := s.assetDB().ExecContext(ctx, `UPDATE stockv2_news_threads SET index_status=?, index_error=NULL, updated_at=?`, NewsContextIndexStale, time.Now()); err != nil {
		return wrapError(err, "mark news thread indexes stale for model change")
	}
	if _, err := s.assetDB().ExecContext(ctx, `UPDATE stockv2_news_thread_versions SET index_status=?, index_error=NULL`, NewsContextIndexStale); err != nil {
		return wrapError(err, "mark news thread version indexes stale for model change")
	}
	return nil
}

func (s *Store) MarkEmbeddingAssetsStaleForObject(ctx context.Context, objectType, objectID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE object_type = ? AND object_id = ? AND status = ?
	`, EmbeddingAssetStatusStale, time.Now(), strings.TrimSpace(objectType), strings.TrimSpace(objectID), EmbeddingAssetStatusReady)
	if err != nil {
		return wrapError(err, "mark embedding assets stale for object")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check stale embedding asset rows")
	}
	if rows == 0 {
		return nil
	}
	switch strings.TrimSpace(objectType) {
	case EmbeddingObjectNewsThread:
		return s.UpdateNewsThreadIndexStatus(ctx, objectID, NewsContextIndexStale, "")
	case EmbeddingObjectNewsThreadVersion:
		return s.UpdateNewsThreadVersionIndexStatus(ctx, objectID, NewsContextIndexStale, "")
	default:
		return nil
	}
}

func (s *Store) MarkEmbeddingAssetsStaleForObjectTextHash(ctx context.Context, objectType, objectID, currentTextHash string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE object_type = ? AND object_id = ? AND status = ? AND text_hash <> ?
	`, EmbeddingAssetStatusStale, time.Now(), strings.TrimSpace(objectType), strings.TrimSpace(objectID),
		EmbeddingAssetStatusReady, strings.TrimSpace(currentTextHash))
	return wrapError(err, "mark embedding assets stale for changed object text")
}

func (s *Store) CountMissingEmbeddingSourcesByType(ctx context.Context, objectTypes []string, modelID string) (map[string]int, error) {
	out := map[string]int{}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return out, nil
	}
	for _, objectType := range normalizeEmbeddingObjectTypes(objectTypes) {
		var count int
		var err error
		switch objectType {
		case EmbeddingObjectStockProfile:
			count, err = s.countMissingEmbeddingSourcesFromAssetDB(ctx, objectType, modelID, `
				SELECT symbol
				FROM stockv2_stock_profiles
				WHERE TRIM(COALESCE(symbol, '')) <> ''
			`)
		case EmbeddingObjectNewsEvent:
			count, err = s.countMissingEmbeddingSourcesFromAssetDB(ctx, objectType, modelID, `
				SELECT id
				FROM stockv2_news_events
				WHERE COALESCE(context_status, 'pending') <> 'compacted'
				  AND TRIM(COALESCE(source, '') || COALESCE(title, '') || COALESCE(summary, '') || COALESCE(content, '') || COALESCE(quality_status, '')) <> ''
			`)
		case EmbeddingObjectNewsThread:
			var ids []string
			ids, err = s.listNewsThreadEmbeddingSourceIDs(ctx)
			if err == nil {
				count, err = s.countMissingEmbeddingSourcesForIDs(ctx, objectType, modelID, ids)
			}
		case EmbeddingObjectNewsThreadVersion:
			var ids []string
			ids, err = s.listNewsThreadVersionEmbeddingSourceIDs(ctx)
			if err == nil {
				count, err = s.countMissingEmbeddingSourcesForIDs(ctx, objectType, modelID, ids)
			}
		case EmbeddingObjectOpportunity:
			err = s.db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM stockv2_opportunities o
				LEFT JOIN stockv2_embedding_assets a
				  ON a.object_type = ? AND a.object_id = o.id AND a.model_id = ?
				WHERE a.id IS NULL
				  AND TRIM(COALESCE(o.title, '') || COALESCE(o.user_thesis, '')) <> ''
			`, objectType, modelID).Scan(&count)
		}
		if err != nil {
			return nil, wrapError(err, "count missing embedding sources "+objectType)
		}
		out[objectType] = count
	}
	return out, nil
}

func (s *Store) listNewsThreadEmbeddingSourceIDs(ctx context.Context) ([]string, error) {
	const pageSize = 200
	ids := make([]string, 0, pageSize)
	for offset := 0; ; offset += pageSize {
		items, err := s.ListNewsThreads(ctx, NewsThreadListFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if id := strings.TrimSpace(item.ID); id != "" && newsThreadEmbeddingIndexable(item) {
				ids = append(ids, id)
			}
		}
		if len(items) < pageSize {
			return ids, nil
		}
	}
}

func (s *Store) listNewsThreadVersionEmbeddingSourceIDs(ctx context.Context) ([]string, error) {
	const pageSize = 200
	ids := make([]string, 0, pageSize)
	for offset := 0; ; offset += pageSize {
		items, err := s.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if id := strings.TrimSpace(item.ID); id != "" && newsThreadVersionEmbeddingIndexable(item) {
				ids = append(ids, id)
			}
		}
		if len(items) < pageSize {
			return ids, nil
		}
	}
}

func (s *Store) countMissingEmbeddingSourcesForIDs(ctx context.Context, objectType, modelID string, ids []string) (int, error) {
	existing, err := s.countEmbeddingAssetsForObjectIDs(ctx, objectType, modelID, ids)
	if err != nil {
		return 0, err
	}
	return len(ids) - existing, nil
}

func (s *Store) countMissingEmbeddingSourcesFromAssetDB(ctx context.Context, objectType, modelID, sourceIDSQL string) (int, error) {
	// ponytail: source rows live in DuckDB and embedding assets live in SQLite; batch IDs here
	// instead of introducing cross-db attach or a mirrored queue table.
	rows, err := s.assetDB().QueryContext(ctx, sourceIDSQL)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	ids := make([]string, 0, 256)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	existing, err := s.countEmbeddingAssetsForObjectIDs(ctx, objectType, modelID, ids)
	if err != nil {
		return 0, err
	}
	return len(ids) - existing, nil
}

func (s *Store) countEmbeddingAssetsForObjectIDs(ctx context.Context, objectType, modelID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	const chunkSize = 500
	total := 0
	for start := 0; start < len(ids); start += chunkSize {
		end := start + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, 2+len(chunk))
		args = append(args, objectType, modelID)
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		var count int
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM stockv2_embedding_assets
			WHERE object_type = ? AND model_id = ? AND object_id IN (`+strings.Join(placeholders, ",")+`)
		`, args...).Scan(&count)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}
