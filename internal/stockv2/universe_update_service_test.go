package stockv2

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewServiceMarksInterruptedUniverseUpdateJobFailed(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	job := StockV2UpdateJob{
		ID:            "job-interrupted",
		TriggerType:   "manual",
		TriggerSource: "test",
		Status:        "running",
		StartAt:       time.Now().Add(-time.Hour),
	}
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatalf("create update job: %v", err)
	}

	svc := NewService(store, nil, nil)
	defer svc.StopBackground()

	got, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.ErrorMessage, "service restart") {
		t.Fatalf("error message = %q, want restart reason", got.ErrorMessage)
	}
}

func TestNewServiceMarksInterruptedStockProfileUpdateTaskPartial(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	startedAt := time.Now().Add(-time.Hour)
	running, err := store.CreateStockProfileUpdateTask(ctx, StockProfileUpdateTask{
		ID: "profile-interrupted", Symbol: "300750", TriggerSource: StockProfileUpdateTriggerAuto,
		Status: StockProfileUpdateStatusRunning, BaseProfileStatus: StockProfileUpdateBaseStatusReady,
		AIDecision: StockProfileAIDecisionCalled, AgentRunID: "agent-interrupted",
		AIProfileStatus: StockProfileUpdateAIStatusRunning, StartedAt: startedAt, CreatedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("create running profile task: %v", err)
	}
	completedAt := startedAt.Add(time.Minute)
	completed, err := store.CreateStockProfileUpdateTask(ctx, StockProfileUpdateTask{
		ID: "profile-completed", Symbol: "000001", TriggerSource: StockProfileUpdateTriggerAuto,
		Status: StockProfileUpdateStatusCompleted, BaseProfileStatus: StockProfileUpdateBaseStatusReady,
		AIDecision: StockProfileAIDecisionSkippedUnchanged, AIProfileStatus: StockProfileAIStatusReady,
		StartedAt: startedAt, FinishedAt: completedAt, CreatedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("create completed profile task: %v", err)
	}

	svc := NewService(store, nil, nil)
	defer svc.StopBackground()

	tasks, err := store.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list profile tasks: %v", err)
	}
	byID := make(map[string]StockProfileUpdateTask, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	recovered := byID[running.ID]
	if recovered.Status != StockProfileUpdateStatusPartial ||
		recovered.AIProfileStatus != StockProfileAIStatusFailed ||
		recovered.FinishedAt.IsZero() ||
		!strings.Contains(recovered.ErrorMessage, "service restart") ||
		!strings.Contains(recovered.AIProfileError, "service restart") {
		t.Fatalf("recovered profile task = %#v", recovered)
	}
	unchanged := byID[completed.ID]
	if unchanged.Status != StockProfileUpdateStatusCompleted || !unchanged.FinishedAt.Equal(completedAt) {
		t.Fatalf("completed profile task changed = %#v", unchanged)
	}
}

func TestNewServiceMarksInterruptedScheduledTasksFailed(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	startedAt := time.Now().Add(-time.Hour)
	if err := store.UpsertQuoteRefreshTaskState(ctx, QuoteRefreshTaskState{
		TaskType:     MonitorTaskLatestQuoteRefresh,
		Status:       MonitorRunStatusRunning,
		TriggerType:  MonitorTriggerScheduled,
		StartedAt:    startedAt,
		ScopeSummary: "scanned 4 symbols",
		ScannedCount: 4,
		UpdatedAt:    startedAt,
	}); err != nil {
		t.Fatalf("upsert quote refresh state: %v", err)
	}
	if err := store.CreateDailyBarJob(ctx, StockV2DailyBarJob{
		ID:            "daily-interrupted",
		JobType:       DailyBarJobTypeIncremental,
		Mode:          DailyBarJobModeUniverseIncremental,
		Status:        "running",
		TriggerType:   MonitorTriggerScheduled,
		TriggerSource: "test",
		StartAt:       startedAt,
		CreatedAt:     startedAt,
	}); err != nil {
		t.Fatalf("create daily bar job: %v", err)
	}
	if _, err := store.CreateMonitorRun(ctx, MonitorRun{
		ID:          "monitor-interrupted",
		TaskType:    MonitorTaskDataStrategyMonitor,
		Status:      MonitorRunStatusRunning,
		TriggerType: MonitorTriggerScheduled,
		StartedAt:   startedAt,
		CreatedAt:   startedAt,
	}); err != nil {
		t.Fatalf("create monitor run: %v", err)
	}
	if err := store.UpsertNewsSourceState(ctx, NewsSourceState{
		Source:              NewsSourceJin10,
		Enabled:             true,
		Status:              NewsSourceStatusRunning,
		PollIntervalSeconds: 600,
		BatchLimit:          50,
		ProcessLimit:        50,
		LastRunAt:           startedAt,
	}); err != nil {
		t.Fatalf("upsert news source state: %v", err)
	}

	svc := NewService(store, nil, nil)
	defer svc.StopBackground()

	quoteState, err := store.GetQuoteRefreshTaskState(ctx, MonitorTaskLatestQuoteRefresh)
	if err != nil {
		t.Fatalf("get quote refresh state: %v", err)
	}
	if quoteState == nil || quoteState.Status != MonitorRunStatusFailed {
		t.Fatalf("quote refresh status = %#v, want failed", quoteState)
	}
	if quoteState.FinishedAt.IsZero() || !strings.Contains(quoteState.ErrorMessage, "service restart") {
		t.Fatalf("quote refresh state = %#v, want finished restart failure", quoteState)
	}

	dailyJob, err := store.GetDailyBarJob(ctx, "daily-interrupted")
	if err != nil {
		t.Fatalf("get daily bar job: %v", err)
	}
	if dailyJob.Status != "failed" || dailyJob.EndAt.IsZero() || !strings.Contains(dailyJob.ErrorMessage, "service restart") {
		t.Fatalf("daily bar job = %#v, want failed restart state", dailyJob)
	}

	monitorRun, err := store.GetMonitorRun(ctx, "monitor-interrupted")
	if err != nil {
		t.Fatalf("get monitor run: %v", err)
	}
	if monitorRun.Status != MonitorRunStatusFailed || monitorRun.FinishedAt.IsZero() || !strings.Contains(monitorRun.ErrorMessage, "service restart") {
		t.Fatalf("monitor run = %#v, want failed restart state", monitorRun)
	}

	newsState, ok, err := store.GetNewsSourceState(ctx, NewsSourceJin10)
	if err != nil {
		t.Fatalf("get news source state: %v", err)
	}
	if !ok {
		t.Fatalf("news source state missing")
	}
	if newsState.Status != NewsSourceStatusIdle || newsState.LastRunStatus != NewsSourceStatusFailed || !strings.Contains(newsState.LastRunError, "service restart") {
		t.Fatalf("news source state = %#v, want idle with failed last run", newsState)
	}
}

func TestNewServiceRecoversInterruptedPortfolioSentinelReviews(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	newsRun, err := store.CreateNewsContextRun(ctx, NewsContextRun{
		ID:            "news-context-waiting-review",
		WindowType:    NewsContextWindowDaily,
		TriggerType:   NewsContextTriggerScheduled,
		Status:        NewsContextRunStatusWaitingReview,
		Phase:         "reviewing",
		WindowStart:   now.Add(-24 * time.Hour),
		WindowEnd:     now.Add(-time.Hour),
		ReviewStatus:  NewsContextReviewRunning,
		ReviewRunID:   "sentinel-bound-review",
		CleanupStatus: NewsContextCleanupPending,
		StartedAt:     now.Add(-2 * time.Hour),
		FinishedAt:    now.Add(-90 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create news context run: %v", err)
	}
	agentRun, ledger, err := store.CreateAgentRunWithLedger(ctx, AgentRun{
		ID:                "agent-bound-review",
		TaskType:          AgentTaskTypePortfolioSentinel,
		TriggerObjectType: "portfolio_sentinel_run",
		TriggerObjectID:   "sentinel-bound-review",
		Status:            AgentRunStatusRunning,
		StartedAt:         now.Add(-time.Hour),
	}, AgentDecisionLedger{
		ID:                "ledger-bound-review",
		TaskType:          AgentTaskTypePortfolioSentinel,
		TriggerObjectType: "portfolio_sentinel_run",
		TriggerObjectID:   "sentinel-bound-review",
	})
	if err != nil {
		t.Fatalf("create bound sentinel agent run: %v", err)
	}
	if _, err := store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		ID:               "sentinel-bound-review",
		AgentRunID:       agentRun.ID,
		DecisionLedgerID: ledger.ID,
		Status:           PortfolioSentinelStatusRunning,
		TriggerType:      PortfolioSentinelTriggerManual,
		WindowType:       PortfolioSentinelWindowManual,
		WindowStartAt:    newsRun.WindowStart,
		WindowEndAt:      newsRun.WindowEnd,
		StartedAt:        now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create bound sentinel: %v", err)
	}
	if _, err := store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		ID:            "sentinel-unbound",
		Status:        PortfolioSentinelStatusRunning,
		TriggerType:   PortfolioSentinelTriggerScheduled,
		WindowType:    PortfolioSentinelWindowPreMarket,
		WindowStartAt: now.Add(-2 * time.Hour),
		WindowEndAt:   now.Add(-time.Hour),
		StartedAt:     now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("create unbound sentinel: %v", err)
	}

	svc := NewService(store, nil, nil)
	defer svc.agentTaskPool.Close()

	recoveredNews, err := store.GetNewsContextRun(ctx, newsRun.ID)
	if err != nil {
		t.Fatalf("get recovered news context run: %v", err)
	}
	if recoveredNews.Status != NewsContextRunStatusWaitingReview || recoveredNews.ReviewStatus != NewsContextReviewPending ||
		recoveredNews.ReviewRunID != "" || recoveredNews.Phase != "waiting_review" || recoveredNews.ErrorMessage != "" {
		t.Fatalf("recovered news context run = %#v, want clean pending review", recoveredNews)
	}
	for _, id := range []string{"sentinel-bound-review", "sentinel-unbound"} {
		run, err := store.GetPortfolioSentinelRun(ctx, id)
		if err != nil {
			t.Fatalf("get recovered sentinel %s: %v", id, err)
		}
		if run.Status != PortfolioSentinelStatusFailed || run.FinishedAt.IsZero() || !strings.Contains(run.ErrorMessage, "service restart") {
			t.Fatalf("recovered sentinel %s = %#v, want interrupted failure", id, run)
		}
	}
	recoveredAgent, err := store.GetAgentRun(ctx, agentRun.ID)
	if err != nil {
		t.Fatalf("get recovered sentinel agent: %v", err)
	}
	if recoveredAgent.Status != AgentRunStatusFailed || recoveredAgent.FinishedAt.IsZero() || !strings.Contains(recoveredAgent.ErrorMessage, "service restart") {
		t.Fatalf("recovered sentinel agent = %#v, want interrupted failure", recoveredAgent)
	}
	if running, err := store.HasRunningPortfolioSentinelRun(ctx, "", ""); err != nil {
		t.Fatalf("check running sentinels: %v", err)
	} else if running {
		t.Fatal("interrupted sentinel still holds the running lock")
	}
}

func TestUniverseMaintenanceFreshSkipRequiresReadyDailyBars(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-fresh",
		Symbol:         "000001",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "平安银行",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert fresh instrument: %v", err)
	}
	if err := svc.store.UpsertDailyBars(ctx, readyDailyBarsForTest("000001", "SZ", time.Now())); err != nil {
		t.Fatalf("upsert ready bars: %v", err)
	}

	quality, err := svc.GetDailyBarsQuality(ctx, "000001", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get daily bar quality: %v", err)
	}
	skip, err := svc.shouldSkipFreshUniverseSymbol(ctx, "000001", time.Now(), svc.universeMaintenanceFreshnessWindow(), quality, true)
	if err != nil {
		t.Fatalf("fresh skip check: %v", err)
	}
	if !skip {
		t.Fatalf("fresh ready instrument was not skipped")
	}

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-missing-bars",
		Symbol:         "000002",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "万科A",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert missing bars instrument: %v", err)
	}
	quality, err = svc.GetDailyBarsQuality(ctx, "000002", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get missing daily bar quality: %v", err)
	}
	skip, err = svc.shouldSkipFreshUniverseSymbol(ctx, "000002", time.Now(), svc.universeMaintenanceFreshnessWindow(), quality, true)
	if err != nil {
		t.Fatalf("missing bars skip check: %v", err)
	}
	if skip {
		t.Fatalf("instrument with missing daily bars was skipped")
	}
}

func TestScheduledUniverseUpdateSkipsWhenCurrentSlotCompleted(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 6, 30, 23, 5, 0, 0, loc)
	settings := svc.settings
	settings.AutoUpdateEnabled = true
	settings.UpdateIntervalSec = 3600
	settings.LastScheduledUpdate = now.AddDate(0, 0, -1)
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	svc.settings = settings

	if err := svc.store.CreateUpdateJob(ctx, StockV2UpdateJob{
		ID:             "job-recent-completed",
		TriggerType:    "manual",
		TriggerSource:  "test",
		Status:         "completed",
		TotalCount:     1,
		ProcessedCount: 1,
		SuccessCount:   1,
		StartAt:        now.Add(-10 * time.Minute),
		EndAt:          now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("create completed job: %v", err)
	}

	svc.checkAndExecuteScheduledUpdateAt(ctx, now)

	jobs, err := svc.store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("job count = %d, want 1", len(jobs))
	}
	gotSettings, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	wantSlot := time.Date(2026, 6, 30, scheduledUniverseUpdateHour, 0, 0, 0, loc)
	if !gotSettings.LastScheduledUpdate.Equal(wantSlot) {
		t.Fatalf("last scheduled update = %v, want slot %v", gotSettings.LastScheduledUpdate, wantSlot)
	}
}

func TestScheduledUniverseUpdateStartsNextDayWhenPreviousSlotFinishedLessThanDayAgo(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 1, 23, 0, 30, 0, loc)
	previousSlot := time.Date(2026, 6, 30, scheduledUniverseUpdateHour, 0, 0, 0, loc)
	latest := StockV2UpdateJob{
		Status:    "completed",
		CreatedAt: previousSlot,
		StartAt:   previousSlot,
		EndAt:     previousSlot.Add(31 * time.Minute),
	}

	decision, slotStart := decideScheduledUniverseUpdate(previousSlot, latest, true, now)
	if decision != scheduledUniverseUpdateStart {
		t.Fatalf("decision = %s, want start for the next natural slot", decision)
	}
	wantSlot := time.Date(2026, 7, 1, scheduledUniverseUpdateHour, 0, 0, 0, loc)
	if !slotStart.Equal(wantSlot) {
		t.Fatalf("slot = %v, want %v", slotStart, wantSlot)
	}
}

func TestScheduledUniverseUpdateDecisionStartsWithoutConfirmingSlot(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 6, 30, 23, 5, 0, 0, loc)

	decision, _ := decideScheduledUniverseUpdate(now.AddDate(0, 0, -1), StockV2UpdateJob{}, false, now)
	if decision != scheduledUniverseUpdateStart {
		t.Fatalf("decision = %s, want start", decision)
	}
}

func TestScheduledUniverseUpdateDecisionWaitsWhileJobRunning(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 6, 30, 23, 5, 0, 0, loc)
	latest := StockV2UpdateJob{Status: "running", CreatedAt: now.Add(-time.Minute)}

	decision, _ := decideScheduledUniverseUpdate(now.AddDate(0, 0, -1), latest, true, now)
	if decision != scheduledUniverseUpdateWait {
		t.Fatalf("decision = %s, want wait", decision)
	}
}

func TestScheduledUniverseUpdateDecisionRetriesFailedCurrentSlot(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	slotStart := time.Date(2026, 6, 30, 23, 0, 0, 0, loc)
	now := slotStart.Add(48 * time.Minute)
	latest := StockV2UpdateJob{
		Status:    "failed",
		CreatedAt: slotStart.Add(30 * time.Second),
		EndAt:     now.Add(-time.Minute),
	}

	decision, _ := decideScheduledUniverseUpdate(slotStart.Add(30*time.Second), latest, true, now)
	if decision != scheduledUniverseUpdateStart {
		t.Fatalf("decision = %s, want start", decision)
	}
}

func TestScheduledUniverseUpdateOnlyRunsInNightWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	settings := svc.settings
	settings.AutoUpdateEnabled = true
	settings.LastScheduledUpdate = time.Time{}
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	svc.settings = settings

	svc.checkAndExecuteScheduledUpdateAt(ctx, time.Date(2026, 6, 30, 14, 0, 0, 0, loc))
	jobs, err := svc.store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("job count = %d, want 0 outside night window", len(jobs))
	}
}

func readyDailyBarsForTest(symbol, market string, now time.Time) []StockV2DailyBar {
	bars := make([]StockV2DailyBar, 0, dailyBarsAgentTarget+10)
	start := now.AddDate(0, 0, -(dailyBarsAgentTarget + 9))
	for i := 0; i < dailyBarsAgentTarget+10; i++ {
		tradeDate := start.AddDate(0, 0, i).Format("2006-01-02")
		bars = append(bars, StockV2DailyBar{
			Symbol:    symbol,
			Market:    market,
			TradeDate: tradeDate,
			Open:      10,
			High:      11,
			Low:       9,
			Close:     10,
			Adjusted:  DailyBarAdjustedNone,
			Source:    "unit_test",
			FetchedAt: now,
			Quality:   DailyBarQualityOK,
		})
	}
	return bars
}
