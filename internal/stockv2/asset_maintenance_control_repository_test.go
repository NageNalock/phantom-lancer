package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreInitPreservesDataWhenLegacySchemaUsesTextTime(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stockv2.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE stockv2_portfolios (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			cash REAL NOT NULL DEFAULT 0,
			risk_level TEXT,
			max_single_position_pct REAL,
			max_drawdown_pct REAL,
			allow_buy INTEGER,
			allow_add INTEGER,
			allow_reduce INTEGER,
			allow_sell INTEGER,
			notes TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO stockv2_portfolios (
			id, name, cash, created_at, updated_at
		) VALUES ('legacy-portfolio', 'keep me', 1234, '2026-01-01', '2026-01-01');
		CREATE TABLE stockv2_agent_authorizations (
			id TEXT PRIMARY KEY,
			note TEXT NOT NULL
		);
		INSERT INTO stockv2_agent_authorizations (id, note)
		VALUES ('legacy-authorization', 'keep retired data');
	`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("initialize legacy store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var portfolioName, authorizationNote string
	if err := store.db.QueryRow(`SELECT name FROM stockv2_portfolios WHERE id = 'legacy-portfolio'`).Scan(&portfolioName); err != nil {
		t.Fatalf("legacy portfolio was not preserved: %v", err)
	}
	if err := store.db.QueryRow(`SELECT note FROM stockv2_agent_authorizations WHERE id = 'legacy-authorization'`).Scan(&authorizationNote); err != nil {
		t.Fatalf("retired authorization data was not preserved: %v", err)
	}
	if portfolioName != "keep me" || authorizationNote != "keep retired data" {
		t.Fatalf("preserved values = %q, %q", portfolioName, authorizationNote)
	}
}

func TestAssetUniverseSnapshotAndMaintenanceSlot(t *testing.T) {
	store := newAssetMaintenanceControlTestStore(t)
	ctx := context.Background()

	snapshot, err := store.EnsureAssetUniverseSnapshot(ctx, []string{" 600000 ", "000001", "600000"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TargetCount != 2 || snapshot.UniverseHash != assetUniverseHash([]string{"000001", "600000"}) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	same, err := store.EnsureAssetUniverseSnapshot(ctx, []string{"600000", "000001"}, "ignored-on-reuse")
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != snapshot.ID {
		t.Fatalf("same universe created snapshot %q, want reuse %q", same.ID, snapshot.ID)
	}
	var activeID string
	if err := store.db.QueryRow(`SELECT active_snapshot_id FROM stockv2_universe_state WHERE id = 'active'`).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	if activeID != snapshot.ID {
		t.Fatalf("active snapshot = %q, want %q", activeID, snapshot.ID)
	}
	rows, err := store.db.Query(`
		SELECT symbol FROM stockv2_universe_snapshot_members
		WHERE snapshot_id = ? ORDER BY position
	`, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var members []string
	for rows.Next() {
		var symbol string
		if err := rows.Scan(&symbol); err != nil {
			t.Fatal(err)
		}
		members = append(members, symbol)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(members) != "[000001 600000]" {
		t.Fatalf("snapshot members = %v", members)
	}

	badJob := testAssetMaintenanceJob("job-bad-snapshot", AssetMaintenanceScopeFullUniverse, time.Now())
	if err := store.CreateUpdateJob(ctx, badJob); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, badJob, snapshot, []string{"000001"}); err == nil {
		t.Fatal("full-universe job accepted targets that did not match its snapshot")
	}

	slotStart := time.Date(2026, 7, 11, 23, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	job := testAssetMaintenanceJob("job-full-slot", AssetMaintenanceScopeFullUniverse, slotStart)
	job.ExpectedLatestDate = "2026-07-11"
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, job, snapshot, []string{"600000", "000001"}); err != nil {
		t.Fatal(err)
	}
	slot, ok, err := store.GetAssetMaintenanceSlot(ctx, slotStart)
	if err != nil || !ok {
		t.Fatalf("get slot: ok=%v err=%v", ok, err)
	}
	if slot.Status != AssetMaintenanceCoveragePending || slot.JobID != job.ID || slot.TargetCount != 2 || !slot.SlotEnd.Equal(slotStart.Add(7*time.Hour)) {
		t.Fatalf("prepared slot = %+v", slot)
	}

	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil || len(items) != 2 {
		t.Fatalf("prepared items = %d, err=%v", len(items), err)
	}
	for i := range items {
		items[i].Status = AssetMaintenanceItemStatusCompleted
		items[i].DailyBarStatus = AssetDailyBarStatusSkipped
		items[i].DailyFlowStatus = AssetDailyFlowStatusReady
		items[i].BaseProfileStatus = AssetBaseProfileStatusUnchanged
		items[i].AnnouncementStatus = AssetAnnouncementStatusChecked
		items[i].CheckedAt = time.Now()
		items[i].FinishedAt = items[i].CheckedAt
	}
	// A completed legacy/inconsistent row with a failed market facet is checked,
	// but must not be reported as fresh.
	items[0].DailyBarStatus = AssetDailyBarStatusFailed
	for _, item := range items {
		if _, err := store.UpsertAssetMaintenanceItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := store.finalizeAssetMaintenanceJob(ctx, job.ID, AssetMaintenanceStats{DailyBarSkipped: 1}, nil, 4096, 512)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Target != 2 || counts.Checked != 2 || counts.Fresh != 1 {
		t.Fatalf("final counts = %+v", counts)
	}
	finalJob, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalJob.Status != "completed" || finalJob.CoverageStatus != AssetMaintenanceCoverageCovered || finalJob.FreshnessStatus != AssetMaintenanceFreshnessStale || finalJob.FreshCount != 1 || finalJob.StaleCount != 1 {
		t.Fatalf("final job = %+v", finalJob)
	}
	progress, err := store.GetAssetMaintenanceAssetsProgressByJobIDs(ctx, []string{job.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := progress[job.ID]; got.MarketFresh != 1 || got.MessageFresh != 2 || got.Fresh != 1 {
		t.Fatalf("asset progress = %+v", got)
	}
	slot, ok, err = store.GetAssetMaintenanceSlot(ctx, slotStart)
	if err != nil || !ok || slot.Status != AssetMaintenanceCoverageCovered || slot.CoveredAt.IsZero() {
		t.Fatalf("final slot = %+v, ok=%v err=%v", slot, ok, err)
	}
	if _, err := store.finalizeAssetMaintenanceJob(ctx, "missing-job", AssetMaintenanceStats{}, nil, 0, 0); !errors.Is(err, ErrUpdateJobNotFound) {
		t.Fatalf("finalize missing job error = %v", err)
	}
}

func TestAssetMaintenanceRetryStateSurvivesJobFinalization(t *testing.T) {
	store := newAssetMaintenanceControlTestStore(t)
	ctx := context.Background()
	job := testAssetMaintenanceJob("job-retry", AssetMaintenanceScopeExplicit, time.Time{})
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	snapshot := AssetUniverseSnapshot{
		UniverseHash: assetUniverseHash([]string{"000001", "600000"}),
		TargetCount:  2,
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, job, snapshot, []string{"000001", "600000"}); err != nil {
		t.Fatal(err)
	}
	markedAt := time.Now()
	if err := store.MarkAssetMaintenanceItemsRetryWait(ctx, job.ID, []string{" 000001 ", "000001", "not-in-job"}, "temporary provider failure"); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	bySymbol := make(map[string]AssetMaintenanceItem, len(items))
	for _, item := range items {
		bySymbol[item.Symbol] = item
	}
	retry := bySymbol["000001"]
	if retry.Status != AssetMaintenanceItemStatusRetryWait || retry.AttemptCount != 1 || retry.CheckedAt.IsZero() || retry.FinishedAt.IsZero() || retry.NextRetryAt.Before(markedAt.Add(14*time.Minute)) {
		t.Fatalf("retry item = %+v", retry)
	}
	if pending := bySymbol["600000"]; pending.Status != AssetMaintenanceItemStatusPending || pending.AttemptCount != 0 {
		t.Fatalf("unmarked item = %+v", pending)
	}

	counts, err := store.finalizeAssetMaintenanceJob(ctx, job.ID, AssetMaintenanceStats{}, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Target != 2 || counts.Checked != 1 || counts.Retry != 1 {
		t.Fatalf("retry counts = %+v", counts)
	}
	finalJob, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalJob.Status != "failed" || finalJob.CoverageStatus != AssetMaintenanceCoverageIncomplete || finalJob.FreshnessStatus != AssetMaintenanceFreshnessRetrying || finalJob.RetryCount != 1 {
		t.Fatalf("retry job = %+v", finalJob)
	}
}

func TestFinalizeAssetMaintenanceKeepsCalendarFailureRetrying(t *testing.T) {
	store := newAssetMaintenanceControlTestStore(t)
	ctx := context.Background()
	job := testAssetMaintenanceJob("job-calendar-degraded", AssetMaintenanceScopeExplicit, time.Time{})
	job.ErrorMessage = "trading_calendar_unavailable: provider timeout"
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	snapshot := AssetUniverseSnapshot{UniverseHash: assetUniverseHash([]string{"000001"}), TargetCount: 1}
	if err := store.PrepareAssetMaintenanceJob(ctx, job, snapshot, []string{"000001"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	item := items[0]
	item.Status = AssetMaintenanceItemStatusCompleted
	item.DailyBarStatus = AssetDailyBarStatusSkipped
	item.DailyFlowStatus = AssetDailyFlowStatusReady
	item.BaseProfileStatus = AssetBaseProfileStatusUnchanged
	item.AnnouncementStatus = AssetAnnouncementStatusChecked
	item.CheckedAt = time.Now()
	item.FinishedAt = item.CheckedAt
	if _, err := store.UpsertAssetMaintenanceItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err := store.finalizeAssetMaintenanceJob(ctx, job.ID, AssetMaintenanceStats{}, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" || got.CoverageStatus != AssetMaintenanceCoverageCovered ||
		got.FreshnessStatus != AssetMaintenanceFreshnessRetrying {
		t.Fatalf("calendar-degraded final job=%+v", got)
	}
}

func TestUnverifiedCachedUniverseCannotReplaceActiveSnapshotOrFinalizeCovered(t *testing.T) {
	store := newAssetMaintenanceControlTestStore(t)
	ctx := context.Background()
	active, err := store.EnsureAssetUniverseSnapshot(ctx, []string{"000001"}, "live")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := store.EnsureUnverifiedAssetUniverseSnapshot(ctx, []string{"000001", "600000"}, "cached")
	if err != nil {
		t.Fatal(err)
	}
	var activeID string
	if err := store.db.QueryRowContext(ctx, `SELECT active_snapshot_id FROM stockv2_universe_state WHERE id = 'active'`).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	if activeID != active.ID || cached.Status != assetUniverseSnapshotStatusCachedUnverified {
		t.Fatalf("active=%s cached=%+v", activeID, cached)
	}
	job := testAssetMaintenanceJob("job-unverified-cache", AssetMaintenanceScopeFullUniverse, time.Now())
	job.UniverseVerified = false
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, job, cached, []string{"000001", "600000"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		item.Status = AssetMaintenanceItemStatusCompleted
		item.DailyBarStatus = AssetDailyBarStatusSkipped
		item.DailyFlowStatus = AssetDailyFlowStatusReady
		item.BaseProfileStatus = AssetBaseProfileStatusUnchanged
		item.AnnouncementStatus = AssetAnnouncementStatusChecked
		item.CheckedAt = time.Now()
		item.FinishedAt = item.CheckedAt
		if _, err := store.UpsertAssetMaintenanceItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.finalizeAssetMaintenanceJob(ctx, job.ID, AssetMaintenanceStats{}, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	final, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.UniverseVerified || final.CoverageStatus != AssetMaintenanceCoverageIncomplete || final.Status != "failed" {
		t.Fatalf("unverified cached job was certified: %+v", final)
	}
}

func TestPruneAssetMaintenanceHistoryRetainsRunningAndReferencedState(t *testing.T) {
	store := newAssetMaintenanceControlTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	snapshots := make([]AssetUniverseSnapshot, 0, 6)
	for i := 0; i < 6; i++ {
		snapshot, err := store.EnsureAssetUniverseSnapshot(ctx, []string{fmt.Sprintf("%06d", i+1)}, "test")
		if err != nil {
			t.Fatal(err)
		}
		createdAt := now.Add(time.Duration(i-10) * time.Hour)
		if _, err := store.db.ExecContext(ctx, `UPDATE stockv2_universe_snapshots SET created_at = ? WHERE id = ?`, createdAt, snapshot.ID); err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, snapshot)
	}

	referenced := testAssetMaintenanceJob("job-referenced-snapshot", AssetMaintenanceScopeFullUniverse, now.Add(-24*time.Hour))
	if err := store.CreateUpdateJob(ctx, referenced); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, referenced, snapshots[0], []string{"000001"}); err != nil {
		t.Fatal(err)
	}

	oldCompleted := testAssetMaintenanceJob("job-old-completed", AssetMaintenanceScopeExplicit, time.Time{})
	oldCompleted.Status = "completed"
	oldRunning := testAssetMaintenanceJob("job-old-running", AssetMaintenanceScopeExplicit, time.Time{})
	for _, job := range []StockV2UpdateJob{oldCompleted, oldRunning} {
		if err := store.CreateUpdateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE stockv2_update_jobs SET created_at = ? WHERE id = ?`, now.AddDate(0, 0, -200), job.ID); err != nil {
			t.Fatal(err)
		}
		item := AssetMaintenanceItem{
			ID:        "item-" + job.ID,
			JobID:     job.ID,
			Symbol:    "600000",
			Status:    AssetMaintenanceItemStatusPending,
			StartedAt: now.AddDate(0, 0, -200),
			CreatedAt: now.AddDate(0, 0, -200),
		}
		if _, err := store.UpsertAssetMaintenanceItem(ctx, item); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE stockv2_asset_maintenance_items SET created_at = ? WHERE id = ?`, item.CreatedAt, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	orphan := AssetMaintenanceItem{
		ID:        "item-old-orphan",
		Symbol:    "000001",
		Status:    AssetMaintenanceItemStatusCompleted,
		StartedAt: now.AddDate(0, 0, -200),
		CreatedAt: now.AddDate(0, 0, -200),
	}
	if _, err := store.UpsertAssetMaintenanceItem(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items SET job_id = NULL, created_at = ? WHERE id = ?
	`, orphan.CreatedAt, orphan.ID); err != nil {
		t.Fatal(err)
	}

	if err := store.PruneAssetMaintenanceHistory(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_update_jobs WHERE id = ?`, oldCompleted.ID); got != 0 {
		t.Fatalf("old completed jobs retained = %d", got)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_update_jobs WHERE id = ?`, oldRunning.ID); got != 1 {
		t.Fatalf("old running jobs retained = %d, want 1", got)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_asset_maintenance_items WHERE job_id = ?`, oldRunning.ID); got != 1 {
		t.Fatalf("running job items retained = %d, want 1", got)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_asset_maintenance_items WHERE id = ?`, orphan.ID); got != 0 {
		t.Fatalf("old orphan items retained = %d", got)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_universe_snapshots WHERE id = ?`, snapshots[0].ID); got != 1 {
		t.Fatalf("referenced snapshot retained = %d, want 1", got)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_universe_snapshots WHERE id = ?`, snapshots[1].ID); got != 0 {
		t.Fatalf("old unreferenced snapshot retained = %d", got)
	}
	if got := testAssetControlRowCount(t, store.db, `SELECT COUNT(*) FROM stockv2_universe_snapshots`); got != 5 {
		t.Fatalf("snapshot count after prune = %d, want referenced plus newest four", got)
	}
}

func newAssetMaintenanceControlTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testAssetMaintenanceJob(id, scope string, slotStart time.Time) StockV2UpdateJob {
	return StockV2UpdateJob{
		ID:               id,
		TriggerType:      "manual",
		TriggerSource:    "test",
		Status:           "running",
		Scope:            scope,
		UniverseVerified: scope == AssetMaintenanceScopeFullUniverse,
		SlotStart:        slotStart,
		CoverageStatus:   AssetMaintenanceCoveragePending,
		FreshnessStatus:  AssetMaintenanceFreshnessPending,
		StartAt:          time.Now(),
	}
}

func testAssetControlRowCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
