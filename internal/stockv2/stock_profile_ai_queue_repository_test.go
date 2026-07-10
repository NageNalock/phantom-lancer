package stockv2

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStockProfileAIQueueCoalescesAndRequeuesNewVersion(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	first, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "000001", Market: "SZ", Priority: 100,
		DesiredInputVersion: "v1", PayloadJSON: `{"version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StockProfileAIQueueStatusReady {
		t.Fatalf("first status = %q", first.Status)
	}
	duplicate, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "000001", Market: "SZ", Priority: 300,
		DesiredInputVersion: "v1", PayloadJSON: `{"version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Priority != 300 {
		t.Fatalf("coalesced priority = %d, want 300", duplicate.Priority)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_stock_profile_ai_queue WHERE symbol = ?`, "000001").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("queue rows = %d, want 1", count)
	}

	now := time.Now()
	lease, err := store.ClaimStockProfileAI(ctx, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease.ClaimedInputVersion != "v1" {
		t.Fatalf("claimed version = %q", lease.ClaimedInputVersion)
	}
	running, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "000001", Market: "SZ", Priority: 400,
		DesiredInputVersion: "v2", PayloadJSON: `{"version":2}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StockProfileAIQueueStatusRunning || running.DesiredInputVersion != "v2" || running.ClaimedInputVersion != "v1" {
		t.Fatalf("running merge = %#v", running)
	}
	requeued, err := store.CompleteStockProfileAI(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if !requeued {
		t.Fatal("new desired version was not requeued")
	}
	next, err := store.ClaimStockProfileAI(ctx, "worker-2", now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if next.ClaimedInputVersion != "v2" || next.PayloadJSON != `{"version":2}` || next.AttemptCount != 1 {
		t.Fatalf("next lease = %#v", next)
	}
}

func TestEnqueueStockProfileAIIfAbsentPreservesNewerQueueInput(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "000001", Market: "SZ", DesiredInputVersion: "new-version", PayloadJSON: `{"new":true}`,
	}); err != nil {
		t.Fatal(err)
	}
	item, err := store.EnqueueStockProfileAIIfAbsent(ctx, StockProfileAIQueueItem{
		Symbol: "000001", Market: "SZ", DesiredInputVersion: "legacy-version", PayloadJSON: `{"legacy":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.DesiredInputVersion != "new-version" || item.PayloadJSON != `{"new":true}` {
		t.Fatalf("legacy recovery replaced newer queue input: %+v", item)
	}
}

func TestStockProfileAIQueueTransitionsBoundRecordsAtomically(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "300750", Market: "SZ", DesiredInputVersion: "v1", PayloadJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateStockProfileUpdateTask(ctx, StockProfileUpdateTask{
		Symbol: "300750", Status: StockProfileUpdateStatusQueued, AIProfileStatus: StockProfileAIStatusQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	asset, err := store.UpsertAssetMaintenanceItem(ctx, AssetMaintenanceItem{
		Symbol: "300750", Status: AssetMaintenanceItemStatusCompleted,
		AIDecision: AssetAIDecisionMissing, AIProfileStatus: StockProfileAIStatusQueued,
		AIQueueStatus: StockProfileAIQueueStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}

	lease, err := store.ClaimStockProfileAI(ctx, "worker-1", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindStockProfileAIRun(ctx, lease, "run-v1"); err != nil {
		t.Fatal(err)
	}
	lease.CurrentAgentRunID = "run-v1"
	if _, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "300750", Market: "SZ", DesiredInputVersion: "v2", PayloadJSON: `{"version":2}`,
	}); err != nil {
		t.Fatal(err)
	}
	requeued, err := store.CompleteStockProfileAI(ctx, lease)
	if err != nil || !requeued {
		t.Fatalf("complete requeued=%v err=%v", requeued, err)
	}
	tasks, err := store.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: task.Symbol, Limit: 10})
	if err != nil || len(tasks) != 1 || tasks[0].Status != StockProfileUpdateStatusQueued || tasks[0].AgentRunID != "" {
		t.Fatalf("task after requeue = %+v err=%v", tasks, err)
	}
	assets, err := store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{Symbol: asset.Symbol, Limit: 10})
	if err != nil || len(assets) != 1 || assets[0].AIQueueStatus != StockProfileAIQueueStatusReady || assets[0].AgentRunID != "" {
		t.Fatalf("asset after requeue = %+v err=%v", assets, err)
	}

	lease, err = store.ClaimStockProfileAI(ctx, "worker-2", time.Now().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindStockProfileAIRun(ctx, lease, "run-v2"); err != nil {
		t.Fatal(err)
	}
	lease.CurrentAgentRunID = "run-v2"
	if err := store.RetryStockProfileAI(ctx, lease, time.Now(), "temporary", false); err != nil {
		t.Fatal(err)
	}
	assets, err = store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{Symbol: asset.Symbol, Limit: 10})
	if err != nil || len(assets) != 1 || assets[0].AIQueueStatus != StockProfileAIQueueStatusRetryWait || assets[0].AgentRunID != "" {
		t.Fatalf("asset after retry = %+v err=%v", assets, err)
	}
	lease, err = store.ClaimStockProfileAI(ctx, "worker-3", time.Now().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindStockProfileAIRun(ctx, lease, "run-v2-retry"); err != nil {
		t.Fatal(err)
	}
	assets, err = store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{Symbol: asset.Symbol, Limit: 10})
	if err != nil || len(assets) != 1 || assets[0].AIQueueStatus != StockProfileAIQueueStatusRunning || assets[0].AgentRunID != "run-v2-retry" {
		t.Fatalf("asset after retry bind = %+v err=%v", assets, err)
	}
}

func TestStockProfileAIQueueClaimIsExclusive(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "600000", Market: "SH", DesiredInputVersion: "v1", PayloadJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, claimErr := store.ClaimStockProfileAI(ctx, "worker", time.Now(), time.Minute)
			results <- claimErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	claimed := 0
	for claimErr := range results {
		switch {
		case claimErr == nil:
			claimed++
		case errors.Is(claimErr, ErrStockProfileAIQueueEmpty):
		default:
			t.Fatalf("claim error = %v", claimErr)
		}
	}
	if claimed != 1 {
		t.Fatalf("successful claims = %d, want 1", claimed)
	}
}

func TestRecoverExpiredStockProfileAIQueueLeaseKeepsRunID(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "300750", Market: "SZ", DesiredInputVersion: "v1", PayloadJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	lease, err := store.ClaimStockProfileAI(ctx, "worker", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindStockProfileAIRun(ctx, lease, "run-expired"); err != nil {
		t.Fatal(err)
	}
	runIDs, err := store.RecoverExpiredStockProfileAILeases(ctx, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 1 || runIDs[0] != "run-expired" {
		t.Fatalf("recovered run ids = %#v", runIDs)
	}
	item, err := store.GetStockProfileAIQueueItem(ctx, "300750")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StockProfileAIQueueStatusReady || item.LeaseToken != "" || item.CurrentAgentRunID != "" {
		t.Fatalf("recovered item = %#v", item)
	}
}

func TestRecoverRunningStockProfileAIQueueLeaseOnRestart(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: "600519", Market: "SH", DesiredInputVersion: "v1", PayloadJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimStockProfileAI(ctx, "old-process", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindStockProfileAIRun(ctx, lease, "run-before-restart"); err != nil {
		t.Fatal(err)
	}
	runIDs, err := store.RecoverRunningStockProfileAILeases(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 1 || runIDs[0] != "run-before-restart" {
		t.Fatalf("restart recovery ids = %#v", runIDs)
	}
	item, err := store.GetStockProfileAIQueueItem(ctx, "600519")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StockProfileAIQueueStatusReady {
		t.Fatalf("restart recovery item = %+v", item)
	}
}
