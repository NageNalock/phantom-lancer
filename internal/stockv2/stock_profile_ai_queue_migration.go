package stockv2

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ensureStockProfileAIQueueSchema performs the one-time payload-to-reference
// migration. Context is rebuilt from DuckDB at claim time, so old serialized
// prompts are intentionally not copied into the new queue.
func (s *Store) ensureStockProfileAIQueueSchema(ctx context.Context) error {
	hasPayload, err := s.sqliteColumnExists(ctx, "stockv2_stock_profile_ai_queue", "payload_json")
	if err != nil {
		return err
	}
	if hasPayload {
		now := time.Now()
		if err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `
			UPDATE stockv2_agent_runs
			SET status = 'superseded', error_message = 'migrated to reference-only AI queue',
			    finished_at = ?, updated_at = ?
			WHERE task_type = 'stock_profile_summary' AND status IN ('ready','running')
		`, now, now); err != nil {
				return fmt.Errorf("supersede old stock profile AI runs: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
			DROP TABLE IF EXISTS stockv2_stock_profile_ai_queue_new;
			CREATE TABLE stockv2_stock_profile_ai_queue_new (
				symbol TEXT PRIMARY KEY,
				market TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				priority INTEGER NOT NULL DEFAULT 0,
				trigger_reason TEXT NOT NULL,
				requested_by TEXT,
				desired_input_version TEXT NOT NULL,
				claimed_input_version TEXT,
				current_agent_run_id TEXT UNIQUE,
				attempt_count INTEGER NOT NULL DEFAULT 0,
				available_at DATETIME NOT NULL,
				lease_owner TEXT,
				lease_token TEXT,
				lease_expires_at DATETIME,
				completed_input_version TEXT,
				completed_at DATETIME,
				result_json TEXT,
				result_hash TEXT,
				result_model TEXT,
				result_confidence REAL NOT NULL DEFAULT 0,
				last_error TEXT,
				created_at DATETIME NOT NULL,
				updated_at DATETIME NOT NULL
			);
			INSERT INTO stockv2_stock_profile_ai_queue_new (
				symbol, market, status, priority, trigger_reason, requested_by,
				desired_input_version, attempt_count, available_at,
				completed_input_version, completed_at, created_at, updated_at
			)
			SELECT symbol, market,
			       CASE WHEN status = 'completed' AND completed_input_version = desired_input_version
			            THEN 'completed' ELSE 'ready' END,
			       priority, trigger_reason, requested_by, desired_input_version, 0, ?,
			       completed_input_version, completed_at, created_at, ?
			FROM stockv2_stock_profile_ai_queue;
			DROP TABLE stockv2_stock_profile_ai_queue;
			ALTER TABLE stockv2_stock_profile_ai_queue_new RENAME TO stockv2_stock_profile_ai_queue;
			CREATE INDEX idx_stockv2_profile_ai_queue_claim
			    ON stockv2_stock_profile_ai_queue(status, available_at, priority DESC, updated_at);
			CREATE INDEX idx_stockv2_profile_ai_queue_lease
			    ON stockv2_stock_profile_ai_queue(lease_expires_at)
			    WHERE status IN ('running','applying');
		`, now, now); err != nil {
				return fmt.Errorf("rebuild stock profile AI queue: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
			UPDATE stockv2_stock_profile_update_tasks
			SET status = 'queued', agent_run_id = NULL, ai_profile_status = 'queued',
			    finished_at = NULL, updated_at = ?
			WHERE status IN ('queued','running');
			UPDATE stockv2_asset_maintenance_items
			SET agent_run_id = NULL,
			    ai_profile_status = CASE WHEN COALESCE(ai_decision,'') LIKE 'called_%' THEN 'queued' ELSE ai_profile_status END,
			    ai_queue_status = CASE WHEN COALESCE(ai_decision,'') LIKE 'called_%' THEN 'ready' ELSE ai_queue_status END,
			    updated_at = ?
			WHERE COALESCE(ai_queue_status,'') IN ('ready','running','retry_wait');
		`, now, now); err != nil {
				return fmt.Errorf("rebind migrated stock profile AI progress: %w", err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET ai_desired_input_version = (
			SELECT desired_input_version FROM stockv2_stock_profile_ai_queue
			WHERE symbol = stockv2_asset_maintenance_items.symbol
		), updated_at = ?
		WHERE COALESCE(ai_desired_input_version, '') = ''
		  AND ai_queue_status IN ('ready', 'running', 'retry_wait', 'apply_pending', 'applying')
		  AND EXISTS (
			SELECT 1 FROM stockv2_stock_profile_ai_queue
			WHERE symbol = stockv2_asset_maintenance_items.symbol
		  )
	`, time.Now())
	return wrapError(err, "backfill stock profile AI maintenance bindings")
}
