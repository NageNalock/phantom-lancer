package stockv2

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAssetMaintenanceRetryClaimIsDurableAndExclusive(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().Truncate(time.Millisecond)
	job := testAssetMaintenanceJob("job-retry", AssetMaintenanceScopeFullUniverse, time.Time{})
	job.Status = "completed"
	job.CoverageStatus = AssetMaintenanceCoverageCovered
	job.FreshnessStatus = AssetMaintenanceFreshnessRetrying
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	item, err := store.UpsertAssetMaintenanceItem(ctx, AssetMaintenanceItem{
		ID: "item-retry", JobID: job.ID, Symbol: "600000",
		Status: AssetMaintenanceItemStatusRetryWait, AttemptCount: 1,
		NextRetryAt: now.Add(-time.Minute), StartedAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueAssetMaintenanceRetry(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != item.ID || claimed.Status != AssetMaintenanceItemStatusRunning || claimed.AttemptCount != 1 {
		t.Fatalf("claimed item = %+v", claimed)
	}
	if _, err := store.ClaimDueAssetMaintenanceRetry(ctx, now); !errors.Is(err, ErrAssetMaintenanceRetryQueueEmpty) {
		t.Fatalf("second claim error = %v", err)
	}
	if err := store.RecoverClaimedAssetMaintenanceRetries(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil || len(recovered) != 1 || recovered[0].Status != AssetMaintenanceItemStatusRetryWait {
		t.Fatalf("recovered items = %+v err=%v", recovered, err)
	}
}

func TestPauseAssetMaintenanceJobQueuesUncheckedTailWithoutAdvancingCoverage(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().Truncate(time.Millisecond)
	job := testAssetMaintenanceJob("job-paused-tail", AssetMaintenanceScopeExplicit, time.Time{})
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
	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	items[0].Status = AssetMaintenanceItemStatusCompleted
	items[0].CheckedAt = now.Add(-time.Minute)
	items[0].FinishedAt = items[0].CheckedAt
	if _, err := store.UpsertAssetMaintenanceItem(ctx, items[0]); err != nil {
		t.Fatal(err)
	}

	if err := store.PauseAssetMaintenanceJob(ctx, job.ID, "resource gate paused: memory_low", now); err != nil {
		t.Fatal(err)
	}
	storedJob, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedJob.Status != "paused" || storedJob.CheckedCount != 1 || storedJob.RetryCount != 1 ||
		storedJob.CoverageStatus != AssetMaintenanceCoverageIncomplete {
		t.Fatalf("paused job = %+v", storedJob)
	}
	items, err = store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var pendingTail AssetMaintenanceItem
	for _, item := range items {
		if item.Status == AssetMaintenanceItemStatusRetryWait {
			pendingTail = item
		}
	}
	if pendingTail.ID == "" || !pendingTail.CheckedAt.IsZero() || pendingTail.AttemptCount != 0 ||
		pendingTail.NextRetryAt.Before(now) {
		t.Fatalf("paused tail = %+v", pendingTail)
	}
	claimed, err := store.ClaimDueAssetMaintenanceRetry(ctx, now)
	if err != nil {
		t.Fatalf("claim paused parent tail: %v", err)
	}
	if claimed.ID != pendingTail.ID || claimed.Status != AssetMaintenanceItemStatusRunning {
		t.Fatalf("claimed paused tail = %+v", claimed)
	}
}

func TestAssetMaintenanceRetryUsesOneItemWhenResourceGateIsThrottled(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().Truncate(time.Millisecond)
	job := testAssetMaintenanceJob("job-throttled-retry", AssetMaintenanceScopeExplicit, time.Time{})
	job.Status = "paused"
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"000001", "600000"} {
		if _, err := store.UpsertAssetMaintenanceItem(ctx, AssetMaintenanceItem{
			ID: assetMaintenanceItemID(job.ID, symbol), JobID: job.ID, Symbol: symbol,
			Status: AssetMaintenanceItemStatusRetryWait, NextRetryAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(store, nil, nil)
	svc.resourceGateReader = func() ResourceGateStatus {
		return ResourceGateStatus{State: ResourceGateThrottled, Reasons: []string{"load_high"}}
	}
	if err := svc.processAssetMaintenanceRetries(ctx, now); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	attempted := 0
	for _, item := range items {
		if item.AttemptCount == 1 {
			attempted++
		}
	}
	if attempted != 1 {
		t.Fatalf("throttled retry attempted %d items, want 1: %+v", attempted, items)
	}
}

func TestAssetMaintenanceRetryDelayIsBounded(t *testing.T) {
	if got := assetMaintenanceRetryDelay(1); got != 15*time.Minute {
		t.Fatalf("first delay = %v", got)
	}
	if got := assetMaintenanceRetryDelay(99); got != 4*time.Hour {
		t.Fatalf("bounded delay = %v, want 4h", got)
	}
}

func TestAnnouncementRetryReusesMarketCursorThatCoversJobCutoff(t *testing.T) {
	cutoff := time.Date(2026, 7, 12, 23, 0, 0, 0, chinaMarketTZ)
	now := cutoff.Add(time.Hour)
	state := readyAnnouncementSyncState(now)
	state.LastSuccessAt = cutoff.Add(time.Minute)
	state.CoveredThrough = cutoff.Add(time.Minute)
	job := StockV2UpdateJob{MessageCutoffAt: cutoff}
	if !announcementSyncCoversMaintenanceJob(state, true, job, now) {
		t.Fatal("covered market cursor would trigger a duplicate per-symbol sync")
	}
	state.CoveredThrough = cutoff.Add(-time.Second)
	if announcementSyncCoversMaintenanceJob(state, true, job, now) {
		t.Fatal("cursor behind the job cutoff was treated as covered")
	}
}

func TestServiceStartupRecoversFrozenMaintenanceTailWithoutReopeningCompletedItems(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	symbols := []string{"000001", "300750", "600000"}
	snapshot, err := store.EnsureAssetUniverseSnapshot(ctx, symbols, "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testAssetMaintenanceJob("job-crashed-frozen", AssetMaintenanceScopeFullUniverse, time.Now())
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, job, snapshot, symbols); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil || len(items) != 3 {
		t.Fatalf("prepared items=%d err=%v", len(items), err)
	}
	now := time.Now()
	items[0].Status = AssetMaintenanceItemStatusCompleted
	items[0].DailyBarStatus = AssetDailyBarStatusSkipped
	items[0].DailyFlowStatus = AssetDailyFlowStatusReady
	items[0].BaseProfileStatus = AssetBaseProfileStatusUnchanged
	items[0].AnnouncementStatus = AssetAnnouncementStatusChecked
	items[0].CheckedAt = now
	items[0].FinishedAt = now
	if _, err := store.UpsertAssetMaintenanceItem(ctx, items[0]); err != nil {
		t.Fatal(err)
	}
	items[1].Status = AssetMaintenanceItemStatusRunning
	if _, err := store.UpsertAssetMaintenanceItem(ctx, items[1]); err != nil {
		t.Fatal(err)
	}

	svc := NewService(store, nil, nil)
	defer svc.StopBackground()
	recovered, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	bySymbol := make(map[string]AssetMaintenanceItem, len(recovered))
	for _, item := range recovered {
		bySymbol[item.Symbol] = item
	}
	if bySymbol[items[0].Symbol].Status != AssetMaintenanceItemStatusCompleted {
		t.Fatalf("completed item reopened: %+v", bySymbol[items[0].Symbol])
	}
	for _, original := range items[1:] {
		item := bySymbol[original.Symbol]
		if item.Status != AssetMaintenanceItemStatusRetryWait || !item.CheckedAt.IsZero() || item.NextRetryAt.IsZero() {
			t.Fatalf("frozen tail item not retryable: %+v", item)
		}
	}
	finalJob, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalJob.Status != "failed" || finalJob.CoverageStatus != AssetMaintenanceCoverageIncomplete || finalJob.CheckedCount != 1 {
		t.Fatalf("recovered parent counts are not actual: %+v", finalJob)
	}
	claimed, err := store.ClaimDueAssetMaintenanceRetry(ctx, time.Now().Add(time.Second))
	if err != nil || claimed.JobID != job.ID {
		t.Fatalf("claim from incomplete parent = %+v err=%v", claimed, err)
	}
}
