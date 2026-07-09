package stockv2

import (
	"context"
	"strings"
	"time"
)

// migrateEmbeddingAssetsToMarketDB 将 stockv2_embedding_assets 从 ops SQLite 迁入 market DuckDB。
// 与 stockv2_embedding_vectors_v2 同库后，向量检索可在 SQL 层 JOIN 做状态/维度过滤。
// 只在 market DB 表为空且 ops DB 表有数据时执行。
func (s *Store) migrateEmbeddingAssetsToMarketDB(ctx context.Context) error {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return nil
	}
	// 检查 market DB 是否已填充
	var marketCount int
	if err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_assets`).Scan(&marketCount); err != nil {
		return err
	}
	if marketCount > 0 {
		return nil // 已有数据，不重复迁移
	}
	// 检查 ops DB 是否有旧数据
	var opsCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_assets`).Scan(&opsCount); err != nil {
		// ops DB 可能还没有这张表（全新部署），不算错误
		return nil
	}
	if opsCount == 0 {
		return nil
	}

	// 从 ops DB 读取全部行
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, object_type, object_id, text_hash, text_summary,
			model_id, provider_id, embedding_protocol, embedding_dimensions,
			vector_ref, status, error_message, created_at, updated_at
		FROM stockv2_embedding_assets
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO stockv2_embedding_assets
			(id, object_type, object_id, text_hash, text_summary,
			 model_id, provider_id, embedding_protocol, embedding_dimensions,
			 vector_ref, status, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	count := 0
	for rows.Next() {
		var id, objectType, objectID, textHash, modelID, status string
		var textSummary, providerID, embeddingProtocol, vectorRef, errorMessage *string
		var embeddingDimensions int
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &objectType, &objectID, &textHash, &textSummary,
			&modelID, &providerID, &embeddingProtocol, &embeddingDimensions,
			&vectorRef, &status, &errorMessage, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, id, objectType, objectID, textHash,
			textSummary, modelID, providerID, embeddingProtocol, embeddingDimensions,
			vectorRef, status, errorMessage, createdAt, updatedAt); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureEmbeddingSchema(ctx context.Context) error {
	// stockv2_embedding_config 保留在 ops DB（系统配置）
	// stockv2_embedding_assets 已迁移到 market DB（与 embedding_vectors_v2 同库），
	// 由 MarketDataStore.init() 创建。此处不再创建。
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
	_, err := s.marketDB.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE status = ? AND model_id <> ?
	`, EmbeddingAssetStatusStale, time.Now(), EmbeddingAssetStatusReady, strings.TrimSpace(modelID))
	return wrapError(err, "mark embedding assets stale for model change")
}

func (s *Store) MarkEmbeddingAssetsStaleForObject(ctx context.Context, objectType, objectID string) error {
	_, err := s.marketDB.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_assets
		SET status = ?, updated_at = ?
		WHERE object_type = ? AND object_id = ? AND status = ?
	`, EmbeddingAssetStatusStale, time.Now(), strings.TrimSpace(objectType), strings.TrimSpace(objectID), EmbeddingAssetStatusReady)
	return wrapError(err, "mark embedding assets stale for object")
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
				SELECT symbol AS id
				FROM stockv2_stock_profiles
				WHERE TRIM(COALESCE(symbol, '')) <> ''
			`)
		case EmbeddingObjectNewsEvent:
			count, err = s.countMissingEmbeddingSourcesFromAssetDB(ctx, objectType, modelID, `
				SELECT id
				FROM stockv2_news_events
				WHERE TRIM(COALESCE(source, '') || COALESCE(title, '') || COALESCE(summary, '') || COALESCE(content, '') || COALESCE(quality_status, '')) <> ''
			`)
		case EmbeddingObjectOpportunity:
			// opportunities 在 ops DB，embedding_assets 已迁到 market DB，分两步计算
			rows, qErr := s.db.QueryContext(ctx, `
				SELECT id FROM stockv2_opportunities
				WHERE TRIM(COALESCE(title, '') || COALESCE(user_thesis, '')) <> ''
			`)
			if qErr != nil {
				err = qErr
				break
			}
			oppIDs := make([]string, 0, 64)
			for rows.Next() {
				var id string
				if sErr := rows.Scan(&id); sErr != nil {
					err = sErr
					break
				}
				if id = strings.TrimSpace(id); id != "" {
					oppIDs = append(oppIDs, id)
				}
			}
			rows.Close()
			if err != nil {
				break
			}
			if eErr := rows.Err(); eErr != nil {
				err = eErr
				break
			}
			existing, cErr := s.countEmbeddingAssetsForObjectIDs(ctx, objectType, modelID, oppIDs)
			if cErr != nil {
				err = cErr
				break
			}
			count = len(oppIDs) - existing
		}
		if err != nil {
			return nil, wrapError(err, "count missing embedding sources "+objectType)
		}
		out[objectType] = count
	}
	return out, nil
}

func (s *Store) countMissingEmbeddingSourcesFromAssetDB(ctx context.Context, objectType, modelID, sourceIDSQL string) (int, error) {
	// 迁移后：source 表和 embedding_assets 表都在 market DB，可单条 SQL 完成
	// ponytail: DuckDB 单连接，子查询 + LEFT JOIN 比 Go 层分批更高效且原子
	var count int
	err := s.marketDB.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (`+sourceIDSQL+`) AS src
		LEFT JOIN stockv2_embedding_assets a
		  ON a.object_type = ? AND a.object_id = src.id AND a.model_id = ?
		WHERE a.id IS NULL
	`, objectType, modelID).Scan(&count)
	if err != nil {
		return 0, wrapError(err, "count missing embedding sources from asset db")
	}
	return count, nil
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
		err := s.marketDB.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM stockv2_embedding_assets
			WHERE object_type = ? AND model_id = ? AND object_id IN (`+strings.Join(placeholders, ",")+`)
		`, args...).Scan(&count)
		if err != nil {
			return 0, wrapError(err, "count embedding assets for object ids")
		}
		total += count
	}
	return total, nil
}
