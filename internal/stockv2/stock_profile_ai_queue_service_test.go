package stockv2

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStockProfileAIQueueSupersedesStaleResult(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	configureStockProfileAgent(t, svc, ctx)
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:               "300750",
		Market:               "SZ",
		InstrumentType:       InstrumentTypeStock,
		Name:                 "宁德时代",
		BusinessSummaryZh:    "基础摘要",
		ProfileTextZh:        "基础画像",
		ProfileText:          "基础画像",
		BaseProfileHash:      "base-v1",
		BaseProfileUpdatedAt: time.Now(),
		AIProfileStatus:      StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	packV1 := StockProfileSummaryContext{Profile: profile}
	if _, err := svc.enqueueStockProfileAI(ctx, packV1, AssetAIDecisionMissing, "test", false); err != nil {
		t.Fatal(err)
	}

	started := make(chan string, 1)
	release := make(chan struct{})
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "old version",
		confidence: 0.8,
		result:     map[string]any{"summaryZh": "旧版本不应落库"},
		started:    started,
		release:    release,
	}
	done := make(chan error, 1)
	go func() { done <- svc.processNextStockProfileAIQueueItem(ctx, "worker-old") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old stock profile run did not start")
	}
	packV2 := packV1
	packV2.NewAnnouncements = []StockV2Announcement{{
		ID: "ann-v2", Symbol: profile.Symbol, ContentHash: "announcement-v2", Title: "新公告",
	}}
	if _, err := svc.enqueueStockProfileAI(ctx, packV2, AssetAIDecisionAnnouncement, "test", false); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Status != StockProfileAIQueueStatusReady || queue.DesiredInputVersion == queue.ClaimedInputVersion {
		t.Fatalf("queue after supersede = %+v", queue)
	}
	staleRun, err := svc.store.ListAgentRuns(ctx, AgentRunListFilter{
		TaskType: AgentTaskTypeStockProfileSummary, TriggerObjectID: profile.Symbol, Limit: 1,
	})
	if err != nil || len(staleRun) != 1 || staleRun[0].Status != AgentRunStatusSuperseded {
		t.Fatalf("stale runs = %+v err=%v", staleRun, err)
	}
	stored, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BusinessSummaryZh == "旧版本不应落库" || stored.AIProfileStatus != StockProfileAIStatusQueued {
		t.Fatalf("stale output changed profile: %+v", stored)
	}

	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "new version",
		confidence: 0.9,
		result:     map[string]any{"summaryZh": "新版本已落库"},
	}
	if err := svc.processNextStockProfileAIQueueItem(ctx, "worker-new"); err != nil {
		t.Fatal(err)
	}
	stored, err = svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BusinessSummaryZh != "新版本已落库" || stored.AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("new output was not applied: %+v", stored)
	}
}

func TestStockProfileAIQueueSupersedesStaleFailure(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	configureStockProfileAgent(t, svc, ctx)
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "宁德时代",
		BusinessSummary: "基础摘要", ProfileTextZh: "基础画像", ProfileText: "基础画像",
		BaseProfileHash: "base-v1", BaseProfileUpdatedAt: time.Now(), AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	packV1 := StockProfileSummaryContext{Profile: profile}
	if _, err := svc.enqueueStockProfileAI(ctx, packV1, AssetAIDecisionMissing, "test", false); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 1)
	release := make(chan struct{})
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool: svc.agentTaskPool, submit: false, execErr: errors.New("old input provider failed"),
		started: started, release: release,
	}
	done := make(chan error, 1)
	go func() { done <- svc.processNextStockProfileAIQueueItem(ctx, "worker-old") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old stock profile run did not start")
	}
	packV2 := packV1
	packV2.Profile.BaseProfileHash = "base-v2"
	if _, err := svc.enqueueStockProfileAI(ctx, packV2, AssetAIDecisionBaseChanged, "test", false); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runs, err := svc.store.ListAgentRuns(ctx, AgentRunListFilter{
		TaskType: AgentTaskTypeStockProfileSummary, TriggerObjectID: profile.Symbol, Limit: 1,
	})
	if err != nil || len(runs) != 1 || runs[0].Status != AgentRunStatusSuperseded {
		t.Fatalf("stale failed run = %+v err=%v", runs, err)
	}
	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Status != StockProfileAIQueueStatusReady || queue.AttemptCount != 0 {
		t.Fatalf("queue after stale failure = %+v", queue)
	}
	stored, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AIProfileStatus != StockProfileAIStatusQueued || strings.Contains(stored.AIProfileError, "old input provider failed") {
		t.Fatalf("stale failure changed profile = %+v", stored)
	}
}

func TestMigrateLegacyStockProfileAIRunToDurableQueue(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{pool: svc.agentTaskPool, submit: true}

	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:               "600519",
		Market:               "SH",
		InstrumentType:       InstrumentTypeStock,
		Name:                 "贵州茅台",
		BusinessSummaryZh:    "白酒生产与销售",
		ProfileTextZh:        "旧基础画像",
		ProfileText:          "旧基础画像",
		BaseProfileHash:      "base-v1",
		BaseProfileUpdatedAt: time.Now(),
		AIProfileStatus:      StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyRun, _, _, err := svc.prepareStockProfileSummaryAgentRun(ctx, StockProfileSummaryContext{Profile: profile}, "legacy-test")
	if err != nil {
		t.Fatal(err)
	}

	svc.migrateLegacyStockProfileAIRuns(ctx)

	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Status != StockProfileAIQueueStatusReady || queue.TriggerReason != AssetAIDecisionMissing {
		t.Fatalf("migrated queue item = %+v", queue)
	}
	migratedRun, err := svc.store.GetAgentRun(ctx, legacyRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migratedRun.Status != AgentRunStatusSuperseded || migratedRun.FinishedAt.IsZero() {
		t.Fatalf("legacy run after migration = %+v", migratedRun)
	}
	stored, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AIProfileStatus != StockProfileAIStatusQueued {
		t.Fatalf("profile ai status = %q, want queued", stored.AIProfileStatus)
	}
}

func TestMigrateSatisfiedLegacyStockProfileAIRunClosesRelatedProgress(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{pool: svc.agentTaskPool, submit: true}

	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行",
		ProfileTextZh: "基础画像", ProfileText: "基础画像", BaseProfileHash: "base-v1",
		BaseProfileUpdatedAt: time.Now(), AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyRun, _, _, err := svc.prepareStockProfileSummaryAgentRun(ctx, StockProfileSummaryContext{Profile: profile}, "legacy-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.CreateStockProfileUpdateTask(ctx, StockProfileUpdateTask{
		Symbol: profile.Symbol, Status: StockProfileUpdateStatusRunning,
		AIProfileStatus: StockProfileAIStatusRunning, AgentRunID: legacyRun.ID,
	}); err != nil {
		t.Fatal(err)
	}
	asset, err := svc.store.UpsertAssetMaintenanceItem(ctx, AssetMaintenanceItem{
		Symbol: profile.Symbol, Status: AssetMaintenanceItemStatusCompleted,
		AIDecision: AssetAIDecisionMissing, AIProfileStatus: StockProfileAIStatusRunning,
		AgentRunID: legacyRun.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile.AIProfileStatus = StockProfileAIStatusReady
	profile.AIProfileUpdatedAt = time.Now().Add(time.Second)
	if _, err := svc.store.UpsertStockProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}

	svc.migrateLegacyStockProfileAIRuns(ctx)

	tasks, err := svc.store.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: profile.Symbol, Limit: 10})
	if err != nil || len(tasks) != 1 || tasks[0].Status != StockProfileUpdateStatusCompleted || tasks[0].AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("satisfied legacy task = %+v err=%v", tasks, err)
	}
	assets, err := svc.store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{Symbol: asset.Symbol, Limit: 10})
	if err != nil || len(assets) != 1 || assets[0].Status != AssetMaintenanceItemStatusCompleted ||
		assets[0].AIQueueStatus != StockProfileAIQueueStatusCompleted || assets[0].AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("satisfied legacy asset = %+v err=%v", assets, err)
	}
	run, err := svc.store.GetAgentRun(ctx, legacyRun.ID)
	if err != nil || run.Status != AgentRunStatusSuperseded {
		t.Fatalf("satisfied legacy run = %+v err=%v", run, err)
	}
}

func TestAssetAIDecisionRetriesNotConfiguredAfterBackoff(t *testing.T) {
	profile := StockProfile{
		Symbol: "600000", AIProfileStatus: StockProfileAIStatusNotConfigured,
		AIProfileAttemptedAt: time.Now().Add(-assetAIBackoff - time.Minute),
	}
	if got := assetAIDecision(profile, StockProfile{}, AssetMaintenanceItem{}, nil, false); got != AssetAIDecisionRetry {
		t.Fatalf("decision = %q, want %q", got, AssetAIDecisionRetry)
	}
	profile.AIProfileAttemptedAt = time.Now()
	if got := assetAIDecision(profile, StockProfile{}, AssetMaintenanceItem{}, nil, false); got != AssetAIDecisionSkippedUnneeded {
		t.Fatalf("decision during backoff = %q", got)
	}
}

func TestStockProfileAIFailureKeepsLastSuccessfulSummaryTimestamp(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	lastSuccess := time.Now().Add(-24 * time.Hour).Truncate(time.Millisecond)
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行",
		ProfileText: "已有画像", ProfileTextZh: "已有画像", AIProfileStatus: StockProfileAIStatusReady,
		AIProfileUpdatedAt: lastSuccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.updateStockProfileAIState(ctx, profile.Symbol, StockProfileAIStatusFailed, "provider failed", true); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.AIProfileUpdatedAt.Equal(lastSuccess) || stored.AIProfileAttemptedAt.IsZero() {
		t.Fatalf("timestamps after failure: success=%v attempt=%v", stored.AIProfileUpdatedAt, stored.AIProfileAttemptedAt)
	}
	announcement := StockV2Announcement{PublishedAt: lastSuccess.Add(time.Hour)}
	if got := announcementsAfterAIProfile([]StockV2Announcement{announcement}, stored.AIProfileUpdatedAt); len(got) != 1 {
		t.Fatalf("failed attempt filtered unseen announcement: %+v", got)
	}
}

func TestStockProfileAIQueueDefersFailedAnnouncementContextWithoutConsumingAttempt(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行",
		ProfileText: "基础画像", ProfileTextZh: "基础画像", AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	pack := StockProfileSummaryContext{
		Profile: profile,
		SourceStatuses: []AssetMaintenanceSourceStatus{{
			Source: StockV2AnnouncementSourceCninfo, Status: AssetAnnouncementStatusFailed, CheckedAt: time.Now(),
		}},
	}
	if _, err := svc.enqueueStockProfileAIWithState(
		ctx, pack, AssetAIDecisionMissing, "test", false,
		StockProfileAIQueueStatusRetryWait, time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	svc.agentExecutor = fakeOperationReviewExecutor{pool: svc.agentTaskPool, submit: true}
	if err := svc.processNextStockProfileAIQueueItem(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Status != StockProfileAIQueueStatusRetryWait || queue.AttemptCount != 0 ||
		!strings.Contains(queue.LastError, "announcement context") {
		t.Fatalf("deferred queue = %+v", queue)
	}
	runs, err := svc.store.ListAgentRuns(ctx, AgentRunListFilter{TaskType: AgentTaskTypeStockProfileSummary, Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("agent runs = %+v err=%v, want none", runs, err)
	}
}

func TestStockProfileAIQueueWithoutExecutorClosesItem(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	configureStockProfileAgent(t, svc, ctx)
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行",
		ProfileText: "基础画像", ProfileTextZh: "基础画像", AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.enqueueStockProfileAI(ctx, StockProfileSummaryContext{Profile: profile}, AssetAIDecisionMissing, "test", false); err != nil {
		t.Fatal(err)
	}
	svc.agentExecutor = nil
	if err := svc.processNextStockProfileAIQueueItem(ctx, "worker"); err == nil {
		t.Fatal("process without executor succeeded")
	}
	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Status != StockProfileAIQueueStatusFailed || !strings.Contains(queue.LastError, ErrAgentExecutorUnavailable.Error()) {
		t.Fatalf("queue without executor = %+v, want terminal failed", queue)
	}
}
