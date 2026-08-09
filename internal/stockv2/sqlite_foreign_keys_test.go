package stockv2

import (
	"context"
	"database/sql"
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
