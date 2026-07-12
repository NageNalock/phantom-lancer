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
	if _, err := svc.store.UpsertAnnouncements(ctx, []StockV2Announcement{{
		ID: "ann-v2", Source: StockV2AnnouncementSourceCninfo,
		Symbol: profile.Symbol, Market: profile.Market, AnnouncementID: "ann-v2",
		ContentHash: "announcement-v2", Title: "新公告", FetchedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
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

func TestStockProfileAIQueueSupersedesRunningInputWhenDailySummaryChanges(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行",
		BusinessSummary: "银行业务", BusinessSummaryZh: "银行业务",
		ProfileText: "银行业务", ProfileTextZh: "银行业务", BaseProfileHash: "base-v1",
		AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.store.UpsertDailyBars(ctx, []StockV2DailyBar{
		stockProfileAITestDailyBar(profile.Symbol, "2026-07-10", 10, 100),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.enqueueStockProfileAI(
		ctx, StockProfileSummaryContext{Profile: profile}, AssetAIDecisionMissing, "test", false,
	); err != nil {
		t.Fatal(err)
	}
	lease, err := svc.store.ClaimStockProfileAI(ctx, "old-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oldVersion := lease.ClaimedInputVersion
	if err := svc.store.UpsertDailyBars(ctx, []StockV2DailyBar{
		stockProfileAITestDailyBar(profile.Symbol, "2026-07-10", 11, 250),
	}); err != nil {
		t.Fatal(err)
	}

	staleProfile := profile
	staleProfile.BusinessSummaryZh = "旧日 K 结果不应落库"
	lease.ResultJSON = `{"summaryZh":"旧日 K 结果不应落库"}`
	lease.ResultHash = "stale-result"
	if _, err := svc.store.ApplyStockProfileAIResult(ctx, lease, staleProfile, `{}`); !errors.Is(err, ErrStockProfileAIQueueLeaseStale) {
		t.Fatalf("apply error = %v, want stale lease", err)
	}
	state, exists, err := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("state exists=%v err=%v", exists, err)
	}
	if state.DesiredInputVersion == oldVersion || state.DataSummaryVersion == "" {
		t.Fatalf("daily summary did not supersede target: %+v", state)
	}
	stored, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BusinessSummaryZh == staleProfile.BusinessSummaryZh {
		t.Fatalf("stale daily summary result changed profile: %+v", stored)
	}
	if err := svc.reconcileStockProfileAIQueueBatch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Status != StockProfileAIQueueStatusRunning ||
		queue.DesiredInputVersion != state.DesiredInputVersion ||
		queue.ClaimedInputVersion != oldVersion || queue.DesiredInputVersion == queue.ClaimedInputVersion {
		t.Fatalf("queue did not adopt fresh daily summary target: %+v", queue)
	}
}

func TestStockProfileAIQueueReplacesPreviousGeneratedTerms(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	configureStockProfileAgent(t, svc, ctx)
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "宁德时代",
		BusinessSummary: "动力电池业务", BusinessSummaryZh: "动力电池业务",
		Aliases: []string{"300750", "宁德时代"}, AliasesZh: []string{"宁德时代"},
		KeywordsZh: []string{"动力电池"}, BusinessLinesZh: []string{"电池系统"},
		RiskTagsZh: []string{"周期波动"}, Industry: "电池", Sectors: []string{"新能源"},
		Concepts: []string{"储能"}, Tags: []string{"高端制造"}, KeywordsEn: []string{"300750", "SZ"},
		ProfileText: "动力电池业务", ProfileTextZh: "动力电池业务",
		BaseProfileHash: "base-v1", AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.enqueueStockProfileAI(
		ctx, StockProfileSummaryContext{Profile: profile}, AssetAIDecisionMissing, "test", false,
	); err != nil {
		t.Fatal(err)
	}
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool: svc.agentTaskPool, submit: true, summary: "v1", confidence: 0.8,
		result: map[string]any{
			"summaryZh": "第一版总结", "aliasesZh": []any{"旧 AI 别名"},
			"keywordsZh":      []any{"动力电池", "储能", "旧 AI 关键词"},
			"businessLinesZh": []any{"电池系统", "旧 AI 业务"},
			"riskTagsZh":      []any{"周期波动", "旧 AI 风险"},
		},
	}
	if err := svc.processNextStockProfileAIQueueItem(ctx, "worker-v1"); err != nil {
		t.Fatal(err)
	}
	v1, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v1.ProfileTextZh, "旧 AI 关键词") {
		t.Fatalf("v1 terms were not applied: %+v", v1)
	}
	if _, err := svc.enqueueStockProfileAI(
		ctx, StockProfileSummaryContext{Profile: v1}, AssetAIDecisionManualForce, "test", true,
	); err != nil {
		t.Fatal(err)
	}
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool: svc.agentTaskPool, submit: true, summary: "v2", confidence: 0.9,
		result: map[string]any{
			"summaryZh": "第二版总结", "aliasesZh": []any{"新 AI 别名"},
			"keywordsZh": []any{"新 AI 关键词"}, "businessLinesZh": []any{"新 AI 业务"},
			"riskTagsZh": []any{"新 AI 风险"},
		},
	}
	if err := svc.processNextStockProfileAIQueueItem(ctx, "worker-v2"); err != nil {
		t.Fatal(err)
	}
	v2, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"旧 AI 别名", "旧 AI 关键词", "旧 AI 业务", "旧 AI 风险"} {
		if strings.Contains(v2.ProfileTextZh, stale) || strings.Contains(strings.Join(v2.Aliases, "\x00"), stale) {
			t.Fatalf("superseded AI term %q survived v2: %+v", stale, v2)
		}
	}
	for _, current := range []string{
		"动力电池", "电池系统", "周期波动", "电池", "新能源", "储能", "高端制造",
		"新 AI 别名", "新 AI 关键词", "新 AI 业务", "新 AI 风险",
	} {
		if !strings.Contains(v2.ProfileTextZh, current) {
			t.Fatalf("current term %q missing from v2: %+v", current, v2)
		}
	}
	if v2.BusinessSummaryZh != "第二版总结" {
		t.Fatalf("v2 summary = %q", v2.BusinessSummaryZh)
	}
	for _, stable := range []string{"300750", "SZ"} {
		if !strings.Contains(strings.Join(v2.KeywordsEn, "\x00"), stable) {
			t.Fatalf("stable English keyword %q missing from v2: %+v", stable, v2.KeywordsEn)
		}
	}
}

func stockProfileAITestDailyBar(symbol, tradeDate string, close, mainNetInflow float64) StockV2DailyBar {
	return StockV2DailyBar{
		Symbol: symbol, Market: "SH", TradeDate: tradeDate,
		Open: close, High: close + 1, Low: close - 1, Close: close, Volume: 100,
		Amount: close * 100, TurnoverRate: 1, NetInflow: mainNetInflow, MainNetInflow: mainNetInflow,
		AmountPresent: true, TurnoverRatePresent: true, NetInflowPresent: true, MainNetInflowPresent: true,
		Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
	}
}

func TestStockProfileAIQueueRebuildsContextWithPreviousSummary(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行",
		BusinessSummary: "银行业务", BusinessSummaryZh: "基础摘要",
		ProfileText: "基础摘要", ProfileTextZh: "基础摘要",
		BaseProfileHash: "base-v1", BaseProfileUpdatedAt: time.Now(),
		AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, exists, err := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("initial AI state exists=%v err=%v", exists, err)
	}
	resultJSON := `{"summaryZh":"上一次 AI 总结","summaryEn":"previous AI summary"}`
	enhanced, err := stockProfileWithEnhancement(
		profile, map[string]any{"summaryZh": "上一次 AI 总结", "summaryEn": "previous AI summary"},
		"previous-model", 0.8, time.Now().Add(-time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.ApplyStockProfileAIResult(ctx, StockProfileAIQueueLease{
		StockProfileAIQueueItem: StockProfileAIQueueItem{
			Symbol: profile.Symbol, ClaimedInputVersion: state.DesiredInputVersion,
			ResultJSON: resultJSON, ResultHash: "previous-hash", ResultModel: "previous-model",
			ResultConfidence: 0.8,
		},
	}, enhanced, `{}`); err != nil {
		t.Fatal(err)
	}
	profile, err = svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	profile.BusinessSummaryZh = "可变 profile 字段已被基础刷新覆盖"
	profile.ProfileTextZh = profile.BusinessSummaryZh
	if profile, err = svc.store.UpsertStockProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.enqueueStockProfileAI(
		ctx, StockProfileSummaryContext{Profile: profile}, AssetAIDecisionManualForce, "test", true,
	); err != nil {
		t.Fatal(err)
	}
	lease, err := svc.store.ClaimStockProfileAI(ctx, "context-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := svc.buildStockProfileAIContextForLease(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if pack.PreviousSummary.BusinessSummaryZh != "上一次 AI 总结" || pack.Profile.Symbol != profile.Symbol {
		t.Fatalf("rebuilt context = %+v", pack)
	}
}

func TestStockProfileAIReconcilerRotatesPersistedCursor(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	for _, symbol := range []string{"000001", "000002", "000003", "000004", "000005"} {
		if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
			Symbol: symbol, Market: "SZ", InstrumentType: InstrumentTypeStock,
			Name: symbol, BaseProfileHash: "base-" + symbol,
			AIProfileStatus: StockProfileAIStatusMissing,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.reconcileStockProfileAIQueueBatch(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if cursor, err := svc.store.GetAssetMaintenanceCursor(ctx, stockProfileAIReconcileCursorScope); err != nil || cursor != "000002" {
		t.Fatalf("first reconcile cursor=%q err=%v", cursor, err)
	}
	if err := svc.reconcileStockProfileAIQueueBatch(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if cursor, err := svc.store.GetAssetMaintenanceCursor(ctx, stockProfileAIReconcileCursorScope); err != nil || cursor != "000004" {
		t.Fatalf("second reconcile cursor=%q err=%v", cursor, err)
	}
	var count int
	if err := svc.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_stock_profile_ai_queue`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("reconciled queue rows=%d, want 4", count)
	}
}

func TestStockProfileAIReconcilerRepairsMaintenanceOutboxFailure(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
		Name: "浦发银行", BaseProfileHash: "base-v1", AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, exists, err := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("AI state exists=%v err=%v", exists, err)
	}
	if _, err := svc.store.UpsertAssetMaintenanceItem(ctx, AssetMaintenanceItem{
		ID: "outbox-item", JobID: "outbox-job", Symbol: profile.Symbol,
		Status: AssetMaintenanceItemStatusCompleted, AIDecision: AssetAIDecisionFailed,
		AIProfileStatus: StockProfileAIStatusFailed, AIQueueStatus: StockProfileAIQueueStatusFailed,
		AIDesiredInputVersion: state.DesiredInputVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.reconcileStockProfileAIQueueBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}
	items, err := svc.store.ListAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{Symbol: profile.Symbol, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("maintenance items=%+v err=%v", items, err)
	}
	if items[0].AIDecision != AssetAIDecisionMissing ||
		items[0].AIQueueStatus != StockProfileAIQueueStatusReady ||
		items[0].AIProfileStatus != StockProfileAIStatusQueued {
		t.Fatalf("repaired maintenance item=%+v", items[0])
	}
	progress, err := svc.store.GetAssetMaintenanceAIProgressByJobIDs(ctx, []string{"outbox-job"})
	if err != nil {
		t.Fatal(err)
	}
	if got := progress["outbox-job"]; got.Queued != 1 || got.Failed != 0 || got.Status != AssetAIProgressStatusActive {
		t.Fatalf("repaired AI progress=%+v", got)
	}
}

func TestStockProfileAIApplyUsesPerSymbolLock(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock,
		Name: "宁德时代", BusinessSummaryZh: "旧摘要", BaseProfileHash: "base-v1",
		AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, exists, err := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("AI state exists=%v err=%v", exists, err)
	}
	if _, err := svc.store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
		Symbol: profile.Symbol, Market: profile.Market, DesiredInputVersion: state.DesiredInputVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = 'apply_pending', claimed_input_version = desired_input_version,
		    result_json = '{"summaryZh":"新摘要"}', result_hash = 'hash',
		    result_model = 'model', result_confidence = 0.9
		WHERE symbol = ?
	`, profile.Symbol); err != nil {
		t.Fatal(err)
	}

	release := svc.lockStockProfile(profile.Symbol)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- svc.processNextStockProfileAIApply(ctx, "apply-worker") }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		queue, getErr := svc.store.GetStockProfileAIQueueItem(ctx, profile.Symbol)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if queue.Status == StockProfileAIQueueStatusApplying {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("apply did not claim queue item: %+v", queue)
		}
		time.Sleep(5 * time.Millisecond)
	}
	stored, err := svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BusinessSummaryZh == "新摘要" {
		t.Fatal("AI result applied while the base-profile symbol lock was held")
	}
	release()
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AI apply did not resume after releasing the symbol lock")
	}
	stored, err = svc.store.GetStockProfile(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BusinessSummaryZh != "新摘要" {
		t.Fatalf("applied profile=%+v", stored)
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
			Source: StockV2AnnouncementSourceCninfo, Status: AssetAnnouncementStatusFailed,
			Message: "exchange-wide sync failed", CheckedAt: time.Now(),
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
