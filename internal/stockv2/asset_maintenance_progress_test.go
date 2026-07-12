package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPopulateAssetMaintenanceProgressSeparatesCoverageAssetsAndAI(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	job := StockV2UpdateJob{
		ID:              "maintenance-progress-job",
		TriggerType:     "manual",
		TriggerSource:   "test",
		Status:          "completed",
		CoverageStatus:  AssetMaintenanceCoverageCovered,
		FreshnessStatus: AssetMaintenanceFreshnessRetrying,
		TotalCount:      9,
		CheckedCount:    9,
		ProcessedCount:  9,
		SuccessCount:    8,
		FailedCount:     1,
		StartAt:         now,
		EndAt:           now.Add(time.Minute),
	}
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatalf("create update job: %v", err)
	}

	items := []AssetMaintenanceItem{
		{Symbol: "000001", AIDecision: AssetAIDecisionMissing, AIQueueStatus: StockProfileAIQueueStatusReady, AIProfileStatus: StockProfileAIStatusQueued},
		{Symbol: "000002", AIDecision: AssetAIDecisionBaseChanged, AIQueueStatus: StockProfileAIQueueStatusRunning, AIProfileStatus: StockProfileAIStatusRunning},
		{Symbol: "000003", AIDecision: AssetAIDecisionRetry, AIQueueStatus: StockProfileAIQueueStatusRetryWait, AIProfileStatus: StockProfileAIStatusQueued},
		{Symbol: "000004", AIDecision: AssetAIDecisionAnnouncement, AIQueueStatus: StockProfileAIQueueStatusCompleted, AIProfileStatus: StockProfileAIStatusReady},
		{Symbol: "000005", AIDecision: AssetAIDecisionManualForce, AIQueueStatus: StockProfileAIQueueStatusFailed, AIProfileStatus: StockProfileAIStatusFailed},
		{Symbol: "000006", AIDecision: AssetAIDecisionSkippedUnneeded, AIProfileStatus: StockProfileAIStatusReady},
		// Legacy rows have no ai_queue_status; profile status still keeps them visible.
		{Symbol: "000007", AIDecision: AssetAIDecisionMissing, AIProfileStatus: StockProfileAIStatusRunning},
		{Symbol: "000008", AIDecision: AssetAIDecisionMissing},
		{Symbol: "000009"},
	}
	for i := range items {
		items[i].ID = "maintenance-progress-item-" + items[i].Symbol
		items[i].JobID = job.ID
		items[i].Status = AssetMaintenanceItemStatusCompleted
		items[i].DailyBarStatus = AssetDailyBarStatusSkipped
		items[i].DailyFlowStatus = AssetDailyFlowStatusReady
		items[i].BaseProfileStatus = AssetBaseProfileStatusUnchanged
		items[i].AnnouncementStatus = AssetAnnouncementStatusChecked
		items[i].StartedAt = now
		items[i].FinishedAt = now.Add(time.Second)
		items[i].CreatedAt = now
		if _, err := store.UpsertAssetMaintenanceItem(ctx, items[i]); err != nil {
			t.Fatalf("upsert item %s: %v", items[i].Symbol, err)
		}
	}

	svc := NewService(store, nil, nil)
	jobs := []StockV2UpdateJob{job}
	if err := svc.PopulateAssetMaintenanceProgress(ctx, jobs); err != nil {
		t.Fatalf("populate maintenance progress: %v", err)
	}
	got := jobs[0].MaintenanceProgress
	if got.Coverage.Status != AssetMaintenanceCoverageCovered || got.Coverage.Target != 9 || got.Coverage.Checked != 9 || got.Coverage.Pending != 0 || got.Coverage.Failed != 1 {
		t.Fatalf("coverage progress = %+v", got.Coverage)
	}
	if got.Assets.Status != AssetMaintenanceFreshnessRetrying || got.Assets.Fresh != 9 || got.Assets.MarketFresh != 9 || got.Assets.MessageFresh != 9 {
		t.Fatalf("asset progress = %+v", got.Assets)
	}
	if got.AIProfile.Status != AssetAIProgressStatusActive ||
		got.AIProfile.Requested != 7 || got.AIProfile.Pending != 1 ||
		got.AIProfile.Queued != 1 || got.AIProfile.Running != 2 ||
		got.AIProfile.Retrying != 1 || got.AIProfile.Completed != 1 ||
		got.AIProfile.Failed != 1 || got.AIProfile.Skipped != 1 ||
		got.AIProfile.Outstanding != 5 {
		t.Fatalf("ai progress = %+v", got.AIProfile)
	}
}

func TestAssetMaintenanceAIProgressStatus(t *testing.T) {
	tests := []struct {
		name string
		in   AssetMaintenanceAIProgress
		want string
	}{
		{name: "not required", want: AssetAIProgressStatusNotRequired},
		{name: "active", in: AssetMaintenanceAIProgress{Requested: 2, Outstanding: 1}, want: AssetAIProgressStatusActive},
		{name: "completed", in: AssetMaintenanceAIProgress{Requested: 2, Completed: 2}, want: AssetAIProgressStatusCompleted},
		{name: "completed with failures", in: AssetMaintenanceAIProgress{Requested: 2, Completed: 1, Failed: 1}, want: AssetAIProgressStatusCompletedWithFailures},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := assetMaintenanceAIProgressStatus(tt.in); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}
