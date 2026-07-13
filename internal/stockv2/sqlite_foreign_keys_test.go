package stockv2

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSQLiteForeignKeyDeleteNullsAlertWatch(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	watch, err := store.CreateWatch(ctx, StockV2Watch{
		ID: "watch-fk", Name: "外键检查", Status: WatchStatusActive, Source: WatchSourceManual,
		TriggerPolicy: WatchTriggerPolicyAny, ScheduleKind: WatchScheduleManual,
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	alert, err := store.CreateAlert(ctx, StockV2Alert{
		ID: "alert-fk", WatchID: watch.ID, Status: AlertStatusOpen, Level: AlertLevelInfo, Title: "外键检查",
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM stockv2_watches WHERE id=?`, watch.ID); err != nil {
		t.Fatalf("delete watch: %v", err)
	}
	var watchID sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT watch_id FROM stockv2_alerts WHERE id=?`, alert.ID).Scan(&watchID); err != nil {
		t.Fatalf("read alert watch: %v", err)
	}
	if watchID.Valid {
		t.Fatalf("alert watch_id = %q, want NULL", watchID.String)
	}
}

func TestStoreInitRepairsDanglingAlertWatchReference(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stockv2.db")
	marketPath := filepath.Join(dir, "stock_market.duckdb")
	store, err := NewStoreWithMarketDB(dbPath, marketPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	raw, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=off")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO stockv2_alerts
			(id, watch_id, status, level, title, triggered_at, created_at, updated_at)
		VALUES ('alert-orphan', 'missing-watch', 'open', 'info', '历史告警', datetime('now'), datetime('now'), datetime('now'))
	`); err != nil {
		raw.Close()
		t.Fatalf("insert dangling alert: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	store, err = NewStoreWithMarketDB(dbPath, marketPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store.Close()
	var watchID sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT watch_id FROM stockv2_alerts WHERE id='alert-orphan'`).Scan(&watchID); err != nil {
		t.Fatalf("read repaired alert: %v", err)
	}
	if watchID.Valid {
		t.Fatalf("repaired watch_id = %q, want NULL", watchID.String)
	}
}
