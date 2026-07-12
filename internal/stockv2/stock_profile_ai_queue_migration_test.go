package stockv2

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStockProfileAIQueueMigrationDropsSerializedPayloadAndRecoversWork(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stockv2.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Millisecond)
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE stockv2_stock_profile_ai_queue (
			symbol TEXT PRIMARY KEY, market TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0, trigger_reason TEXT NOT NULL, requested_by TEXT,
			desired_input_version TEXT NOT NULL, claimed_input_version TEXT,
			payload_json TEXT NOT NULL, current_agent_run_id TEXT UNIQUE,
			attempt_count INTEGER NOT NULL DEFAULT 0, available_at DATETIME NOT NULL,
			lease_owner TEXT, lease_token TEXT, lease_expires_at DATETIME,
			completed_input_version TEXT, completed_at DATETIME, last_error TEXT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		);
		INSERT INTO stockv2_stock_profile_ai_queue (
			symbol, market, status, priority, trigger_reason, desired_input_version,
			claimed_input_version, payload_json, attempt_count, available_at,
			lease_owner, lease_token, lease_expires_at, created_at, updated_at
		) VALUES ('600000', 'SH', 'running', 400, 'called_missing', 'legacy-v1',
			'legacy-v1', '{"secret":"serialized prompt must disappear"}', 2, ?,
			'old-worker', 'old-token', ?, ?, ?)
	`, now, now.Add(time.Minute), now, now); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hasPayload, err := store.sqliteColumnExists(ctx, "stockv2_stock_profile_ai_queue", "payload_json")
	if err != nil {
		t.Fatal(err)
	}
	if hasPayload {
		t.Fatal("serialized queue payload column survived migration")
	}
	item, err := store.GetStockProfileAIQueueItem(ctx, "600000")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StockProfileAIQueueStatusReady || item.AttemptCount != 0 || item.LeaseToken != "" {
		t.Fatalf("migrated queue item = %+v", item)
	}
}
