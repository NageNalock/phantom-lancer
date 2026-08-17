package stockv2

import (
	"context"
	"strings"
	"time"
)

type embeddingWorkItem struct {
	ObjectType string
	ObjectID   string
	Revision   int64
}

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
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_search
			ON stockv2_embedding_assets(object_type, model_id, status, embedding_dimensions, object_id);
		CREATE TABLE IF NOT EXISTS stockv2_embedding_work_items (
			object_type TEXT NOT NULL,
			object_id TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			enqueued_at DATETIME NOT NULL,
			PRIMARY KEY(object_type, object_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_work_items_queue
			ON stockv2_embedding_work_items(enqueued_at, object_type, object_id);
		INSERT INTO stockv2_embedding_work_items (object_type, object_id, revision, enqueued_at)
			SELECT object_type, object_id, 1, updated_at
			FROM stockv2_embedding_assets
			WHERE status <> 'ready'
			ON CONFLICT(object_type, object_id) DO NOTHING;
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
	return wrapError(err, "ensure embedding schema")
}

func (s *Store) MarkEmbeddingAssetsStaleForModelChange(ctx context.Context, modelID string) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_work_items (object_type, object_id, revision, enqueued_at)
		SELECT object_type, object_id, 1, ?
		FROM stockv2_embedding_assets
		WHERE model_id <> ?
		ON CONFLICT(object_type, object_id) DO UPDATE SET
			revision = stockv2_embedding_work_items.revision + 1,
			enqueued_at = excluded.enqueued_at
	`, time.Now(), strings.TrimSpace(modelID)); err != nil {
		return wrapError(err, "queue embedding assets for model change")
	}
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

func (s *Store) MarkEmbeddingAssetsStaleForObjectTextHash(ctx context.Context, objectType, objectID, currentTextHash string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE object_type = ? AND object_id = ? AND status = ? AND text_hash <> ?
	`, EmbeddingAssetStatusStale, time.Now(), strings.TrimSpace(objectType), strings.TrimSpace(objectID),
		EmbeddingAssetStatusReady, strings.TrimSpace(currentTextHash))
	return wrapError(err, "mark embedding assets stale for changed object text")
}

func (s *Store) QueueEmbeddingWork(ctx context.Context, objectType, objectID string) error {
	objectType = strings.TrimSpace(objectType)
	objectID = strings.TrimSpace(objectID)
	if objectType == "" || objectID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_work_items (object_type, object_id, revision, enqueued_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(object_type, object_id) DO UPDATE SET
			revision = stockv2_embedding_work_items.revision + 1,
			enqueued_at = excluded.enqueued_at
	`, objectType, objectID, time.Now())
	return wrapError(err, "queue embedding work")
}

func (s *Store) EnsureEmbeddingWork(ctx context.Context, objectType, objectID string) error {
	objectType = strings.TrimSpace(objectType)
	objectID = strings.TrimSpace(objectID)
	if objectType == "" || objectID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_work_items (object_type, object_id, revision, enqueued_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(object_type, object_id) DO NOTHING
	`, objectType, objectID, time.Now())
	return wrapError(err, "ensure embedding work")
}

func (s *Store) QueueEmbeddingWorkItems(ctx context.Context, items []embeddingWorkItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin queue embedding work")
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO stockv2_embedding_work_items (object_type, object_id, revision, enqueued_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(object_type, object_id) DO UPDATE SET
			revision = stockv2_embedding_work_items.revision + 1,
			enqueued_at = excluded.enqueued_at
	`)
	if err != nil {
		return wrapError(err, "prepare queue embedding work")
	}
	defer stmt.Close()
	now := time.Now()
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.ObjectType = strings.TrimSpace(item.ObjectType)
		item.ObjectID = strings.TrimSpace(item.ObjectID)
		if item.ObjectType == "" || item.ObjectID == "" {
			continue
		}
		key := item.ObjectType + "\x00" + item.ObjectID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := stmt.ExecContext(ctx, item.ObjectType, item.ObjectID, now); err != nil {
			return wrapError(err, "queue embedding work item")
		}
	}
	return wrapError(tx.Commit(), "commit queued embedding work")
}

func (s *Store) ListEmbeddingWorkItems(ctx context.Context, objectTypes []string, limit int) ([]embeddingWorkItem, error) {
	objectTypes = normalizeEmbeddingObjectTypes(objectTypes)
	if limit <= 0 || len(objectTypes) == 0 {
		return []embeddingWorkItem{}, nil
	}
	placeholders := make([]string, len(objectTypes))
	args := make([]any, 0, len(objectTypes)+1)
	for i, objectType := range objectTypes {
		placeholders[i] = "?"
		args = append(args, objectType)
	}
	args = append(args, normalizedPageLimit(limit, maxEmbeddingMaintainBatchSize))
	rows, err := s.db.QueryContext(ctx, `
		SELECT object_type, object_id, revision
		FROM stockv2_embedding_work_items
		WHERE object_type IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY enqueued_at ASC, object_type ASC, object_id ASC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list embedding work items")
	}
	return scanRows(rows, func(row rowScanner) (embeddingWorkItem, error) {
		var item embeddingWorkItem
		err := row.Scan(&item.ObjectType, &item.ObjectID, &item.Revision)
		return item, err
	}, "scan embedding work item", "iterate embedding work items")
}

func (s *Store) CompleteEmbeddingWork(ctx context.Context, item embeddingWorkItem) error {
	if strings.TrimSpace(item.ObjectType) == "" || strings.TrimSpace(item.ObjectID) == "" {
		return nil
	}
	query := `DELETE FROM stockv2_embedding_work_items WHERE object_type = ? AND object_id = ?`
	args := []any{strings.TrimSpace(item.ObjectType), strings.TrimSpace(item.ObjectID)}
	if item.Revision > 0 {
		query += ` AND revision = ?`
		args = append(args, item.Revision)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return wrapError(err, "complete embedding work")
}

func (s *Store) ListPendingNewsContextEmbeddingWork(ctx context.Context, limit int) ([]embeddingWorkItem, error) {
	limit = normalizedPageLimit(limit, maxEmbeddingMaintainBatchSize)
	out := make([]embeddingWorkItem, 0, limit)
	threadRows, err := s.marketDB.db.QueryContext(ctx, `
		SELECT id
		FROM stockv2_news_threads
		WHERE index_status <> ? AND status NOT IN (?, ?)
		ORDER BY updated_at ASC, id ASC
		LIMIT ?
	`, NewsContextIndexReady, NewsThreadStatusMerged, NewsThreadStatusArchived, limit)
	if err != nil {
		return nil, wrapError(err, "list pending news thread embedding work")
	}
	for threadRows.Next() {
		var id string
		if err := threadRows.Scan(&id); err != nil {
			threadRows.Close()
			return nil, wrapError(err, "scan pending news thread embedding work")
		}
		out = append(out, embeddingWorkItem{ObjectType: EmbeddingObjectNewsThread, ObjectID: id})
	}
	if err := threadRows.Err(); err != nil {
		threadRows.Close()
		return nil, wrapError(err, "iterate pending news thread embedding work")
	}
	threadRows.Close()
	remaining := limit - len(out)
	if remaining <= 0 {
		return out, nil
	}
	versionRows, err := s.marketDB.db.QueryContext(ctx, `
		SELECT id
		FROM stockv2_news_thread_versions
		WHERE index_status <> ? AND (window_type = ? OR material_change = 1)
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, NewsContextIndexReady, NewsContextWindowDaily, remaining)
	if err != nil {
		return nil, wrapError(err, "list pending news thread version embedding work")
	}
	for versionRows.Next() {
		var id string
		if err := versionRows.Scan(&id); err != nil {
			versionRows.Close()
			return nil, wrapError(err, "scan pending news thread version embedding work")
		}
		out = append(out, embeddingWorkItem{ObjectType: EmbeddingObjectNewsThreadVersion, ObjectID: id})
	}
	if err := versionRows.Err(); err != nil {
		versionRows.Close()
		return nil, wrapError(err, "iterate pending news thread version embedding work")
	}
	versionRows.Close()
	return out, nil
}

func (s *Store) CountMissingEmbeddingSourcesByType(ctx context.Context, objectTypes []string, modelID string) (map[string]int, error) {
	out := map[string]int{}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return out, nil
	}
	for _, objectType := range normalizeEmbeddingObjectTypes(objectTypes) {
		var count int
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM stockv2_embedding_work_items w
			LEFT JOIN stockv2_embedding_assets a
			  ON a.object_type = w.object_type AND a.object_id = w.object_id AND a.model_id = ?
			WHERE w.object_type = ? AND a.id IS NULL
		`, modelID, objectType).Scan(&count)
		if err != nil {
			return nil, wrapError(err, "count missing embedding sources "+objectType)
		}
		out[objectType] = count
	}
	return out, nil
}
