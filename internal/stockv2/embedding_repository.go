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
			last_probe_at DATETIME,
			last_probe_status TEXT,
			last_error TEXT,
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
	`)
	return wrapError(err, "ensure embedding schema")
}

func (s *Store) MarkEmbeddingAssetsStaleForModelChange(ctx context.Context, modelID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE status = ? AND model_id <> ?
	`, EmbeddingAssetStatusStale, time.Now(), EmbeddingAssetStatusReady, strings.TrimSpace(modelID))
	return wrapError(err, "mark embedding assets stale for model change")
}
