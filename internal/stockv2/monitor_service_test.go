package stockv2

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListMonitorTasksReturnsBuiltin(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	tasks, err := svc.ListMonitorTasks(ctx)
	if err != nil {
		t.Fatalf("list monitor tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}
	enabledTasks := make(map[string]bool)
	for _, task := range tasks {
		if task.Definition.TaskType == "universe_update" {
			t.Fatalf("data asset maintenance should not be listed as a monitor task")
		}
		if task.Definition.TaskType == "daily_bars_sync" {
			t.Fatalf("standalone daily bars task should not be listed")
		}
		if task.Config.Enabled {
			enabledTasks[task.Definition.TaskType] = true
		}
	}
	if len(enabledTasks) != 1 || !enabledTasks[MonitorTaskLatestQuoteRefresh] {
		t.Fatalf("default enabled tasks = %#v, want only latest quote refresh", enabledTasks)
	}
}

func TestRemovedDataAssetMonitorTasks(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	if _, err := svc.RunMonitorTask(ctx, "universe_update", MonitorTriggerManual); !errors.Is(err, ErrInvalidMonitorTaskType) {
		t.Fatalf("run removed universe task err = %v, want invalid task type", err)
	}
	if _, err := svc.RunMonitorTask(ctx, "daily_bars_sync", MonitorTriggerManual); !errors.Is(err, ErrInvalidMonitorTaskType) {
		t.Fatalf("run removed daily bars task err = %v, want invalid task type", err)
	}
}

func TestUpdateMonitorTaskConfigPersistsNonEnabledFields(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	// 只改非 enabled 字段,避免触发后台调度 goroutine
	enabled := false
	interval := 120
	agent := true
	cooldown := 600
	task, err := svc.UpdateMonitorTaskConfig(ctx, MonitorTaskDataStrategyMonitor, RequestUpdateMonitorTaskConfig{
		Enabled:                 &enabled,
		IntervalSeconds:         &interval,
		AgentDoublecheckEnabled: &agent,
		CooldownSeconds:         &cooldown,
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if task.Config.IntervalSeconds != 120 {
		t.Fatalf("interval = %d, want 120", task.Config.IntervalSeconds)
	}
	if !task.Config.AgentDoublecheckEnabled {
		t.Fatalf("agent doublecheck should be enabled")
	}
	if task.Config.CooldownSeconds != 600 {
		t.Fatalf("cooldown = %d, want 600", task.Config.CooldownSeconds)
	}
	if task.Config.Enabled {
		t.Fatalf("enabled should remain false")
	}

	// 重新读取应一致
	reloaded, err := svc.GetMonitorTask(ctx, MonitorTaskDataStrategyMonitor)
	if err != nil {
		t.Fatalf("get monitor task: %v", err)
	}
	if reloaded.Config.IntervalSeconds != 120 || !reloaded.Config.AgentDoublecheckEnabled {
		t.Fatalf("reloaded config = %+v", reloaded.Config)
	}
}

func TestLatestQuoteRefreshUsesStateNotMonitorHistory(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, tencentQuoteLine("sz000001", "平安银行", "000001", "12.00", "11.00")), nil
	})})

	portfolio := createStrategyTestPortfolio(t, store, "portfolio-quote")
	if err := store.CreateHolding(ctx, StockV2Holding{
		ID:          "holding-quote",
		PortfolioID: portfolio.ID,
		Symbol:      "000001",
		Market:      "SZ",
		Name:        "平安银行",
		Quantity:    100,
		CostPrice:   10,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	run, err := svc.RunMonitorTask(ctx, MonitorTaskLatestQuoteRefresh, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run latest quote refresh: %v", err)
	}
	if run.TaskType != MonitorTaskLatestQuoteRefresh || run.Status != MonitorRunStatusCompleted || run.SuccessCount != 1 {
		t.Fatalf("synthetic run = %+v", run)
	}
	runs, err := svc.ListMonitorRuns(ctx, MonitorRunListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list monitor runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("monitor runs = %+v, want quote refresh excluded from history", runs)
	}
	state, statuses, err := svc.GetLatestQuoteRefreshState(ctx, 20)
	if err != nil {
		t.Fatalf("get quote refresh state: %v", err)
	}
	if state.Status != MonitorRunStatusCompleted || state.SuccessCount != 1 || state.ScannedCount != 1 {
		t.Fatalf("state = %+v", state)
	}
	if len(statuses) != 1 || statuses[0].Symbol != "000001" || statuses[0].Status != QuoteStatusFresh {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestLatestQuoteRefreshTimeoutPersistsFailedState(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})})

	portfolio := createStrategyTestPortfolio(t, store, "portfolio-quote-timeout")
	if err := store.CreateHolding(ctx, StockV2Holding{
		ID:          "holding-quote-timeout",
		PortfolioID: portfolio.ID,
		Symbol:      "000001",
		Market:      "SZ",
		Name:        "平安银行",
		Quantity:    100,
		CostPrice:   10,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	state, err := svc.RunLatestQuoteRefreshTask(runCtx, MonitorTriggerScheduled)
	if err == nil {
		t.Fatalf("run latest quote refresh err = nil, want timeout")
	}
	if state.Status != MonitorRunStatusFailed {
		t.Fatalf("returned state = %+v, want failed", state)
	}
	stored, err := store.GetQuoteRefreshTaskState(ctx, MonitorTaskLatestQuoteRefresh)
	if err != nil {
		t.Fatalf("get quote refresh state: %v", err)
	}
	if stored == nil || stored.Status != MonitorRunStatusFailed || stored.FinishedAt.IsZero() {
		t.Fatalf("stored state = %+v, want failed with finished_at", stored)
	}
	if !strings.Contains(stored.ErrorMessage, "deadline exceeded") {
		t.Fatalf("stored error = %q, want deadline exceeded", stored.ErrorMessage)
	}
}

func TestScheduledLatestQuoteRefreshBacksOffAfterFailure(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	calls := 0
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return stringResponse(http.StatusOK, tencentQuoteLine("sz000001", "平安银行", "000001", "10.50", "10.00")), nil
	})})

	portfolio := createStrategyTestPortfolio(t, store, "portfolio-quote-backoff")
	if err := store.CreateHolding(ctx, StockV2Holding{
		ID:          "holding-quote-backoff",
		PortfolioID: portfolio.ID,
		Symbol:      "000001",
		Market:      "SZ",
		Name:        "平安银行",
		Quantity:    100,
		CostPrice:   10,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	now := time.Now()
	if err := store.UpsertQuoteRefreshTaskState(ctx, QuoteRefreshTaskState{
		TaskType:     MonitorTaskLatestQuoteRefresh,
		Status:       MonitorRunStatusFailed,
		TriggerType:  MonitorTriggerScheduled,
		StartedAt:    now.Add(-2 * time.Minute),
		FinishedAt:   now,
		ScopeSummary: "scanned 1 symbols",
		ScannedCount: 1,
		ErrorMessage: "context deadline exceeded",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("seed quote refresh task state: %v", err)
	}

	svc.tickScheduledMonitors(ctx)
	if calls != 0 {
		t.Fatalf("scheduled refresh calls = %d, want 0 during failure backoff", calls)
	}
}

func TestRunDataStrategyMonitorProducesHit(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "000977", 61, 1.2, QuoteStatusFresh, time.Now())
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "突破策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "000977",
		Direction: StrategyDirectionWatch,
		GenerationMeta: map[string]any{
			"priceTriggers": map[string]any{"triggerPriceAbove": 60.0},
		},
		CreatedBy: StrategySourceManual,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	agent := true
	if _, err := svc.UpdateMonitorTaskConfig(ctx, MonitorTaskDataStrategyMonitor, RequestUpdateMonitorTaskConfig{
		AgentDoublecheckEnabled: &agent,
	}); err != nil {
		t.Fatalf("enable agent doublecheck: %v", err)
	}

	run, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run data strategy monitor: %v", err)
	}
	if run.Status != MonitorRunStatusCompleted {
		t.Fatalf("run status = %s, want completed", run.Status)
	}
	if run.HitCount < 1 {
		t.Fatalf("hit count = %d, want >= 1", run.HitCount)
	}
	if run.ReviewCount < 1 {
		t.Fatalf("review count = %d, want >= 1", run.ReviewCount)
	}
	if run.AlertCount < 1 {
		t.Fatalf("alert count = %d, want >= 1", run.AlertCount)
	}

	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskDataStrategyMonitor, Limit: 50})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no monitor hits produced")
	}
	if hits[0].Status != MonitorHitStatusAlerted {
		t.Fatalf("hit status = %s, want alerted", hits[0].Status)
	}
	if hits[0].Symbol != "000977" {
		t.Fatalf("hit symbol = %q", hits[0].Symbol)
	}
	if hits[0].AlertID == "" {
		t.Fatalf("hit alert id empty")
	}
	if hits[0].AgentDecisionID != "" {
		t.Fatalf("agent decision id = %q, want empty until executor exists", hits[0].AgentDecisionID)
	}
	if got := hits[0].Evidence["agentDoublecheck"]; got != "unavailable" {
		t.Fatalf("agent state = %v, want unavailable", got)
	}
	pipeline := mapFromAny(hits[0].Evidence["reviewPipeline"])
	if pipeline["reviewId"] == "" || pipeline["reviewCreated"] != true {
		t.Fatalf("review pipeline = %+v, want created review", pipeline)
	}
	if pipeline["agentStatus"] != "unavailable" {
		t.Fatalf("agent pipeline status = %v, want unavailable", pipeline["agentStatus"])
	}
	reviews, err := svc.ListOperationReviews(ctx, OperationReviewListFilter{HitID: hits[0].ID, Limit: 10})
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("review count for hit = %d, want 1", len(reviews))
	}
	alerts, err := svc.ListAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "000977", Limit: 10})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if alerts[0].TriggerSource != AlertTriggerSourceDegraded {
		t.Fatalf("trigger source = %s, want degraded", alerts[0].TriggerSource)
	}
	if alerts[0].MonitorHitID != hits[0].ID || alerts[0].ReviewID == "" {
		t.Fatalf("alert linkage = %+v, hit=%s", alerts[0], hits[0].ID)
	}
	if alerts[0].Evidence["degraded_reason"] != "agent_unavailable" {
		t.Fatalf("alert degraded reason = %v, want agent_unavailable", alerts[0].Evidence["degraded_reason"])
	}
}

func TestProcessMonitorHitDoesNotAlertBeforeAsyncAgentFinalResult(t *testing.T) {
	ctx, svc, cleanup, review := newOperationReviewAgentE2ETest(t)
	defer cleanup()

	hit, err := svc.store.GetMonitorHit(ctx, review.HitID)
	if err != nil {
		t.Fatalf("get hit: %v", err)
	}
	started := make(chan string, 1)
	release := make(chan struct{})
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		outputType: OperationReviewOutputContinueMonitoring,
		summary:    "继续观察",
		started:    started,
		release:    release,
	}

	post, err := svc.processCreatedMonitorHit(ctx, hit, MonitorTaskConfig{AgentDoublecheckEnabled: true, CooldownSeconds: 300})
	if err != nil {
		t.Fatalf("process hit: %v", err)
	}
	if post.AlertID != "" {
		t.Fatalf("alert id = %q, want empty before async agent final result", post.AlertID)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("agent executor did not start")
	}
	count, err := svc.CountAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: hit.Symbol})
	if err != nil {
		t.Fatalf("count alerts before release: %v", err)
	}
	if count != 0 {
		t.Fatalf("alert count before agent final result = %d, want 0", count)
	}

	close(release)
	run := waitAgentRunTerminal(t, svc, post.AgentRunID)
	if run.Status != AgentRunStatusCompleted {
		t.Fatalf("agent run status = %s, want completed", run.Status)
	}
	count, err = svc.CountAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: hit.Symbol})
	if err != nil {
		t.Fatalf("count alerts after release: %v", err)
	}
	if count != 0 {
		t.Fatalf("alert count after continue_monitoring = %d, want 0", count)
	}
}

func TestRunDataStrategyMonitorDoublecheckCreatesAgentRunWithoutExecutor(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	configureOperationReviewModelForMonitorTest(t, svc)
	seedWatchQuote(t, svc, "000977", 61, 1.2, QuoteStatusFresh, time.Now())
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "突破策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "000977",
		Direction: StrategyDirectionWatch,
		GenerationMeta: map[string]any{
			"priceTriggers": map[string]any{"triggerPriceAbove": 60.0},
		},
		CreatedBy: StrategySourceManual,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	agent := true
	if _, err := svc.UpdateMonitorTaskConfig(ctx, MonitorTaskDataStrategyMonitor, RequestUpdateMonitorTaskConfig{
		AgentDoublecheckEnabled: &agent,
	}); err != nil {
		t.Fatalf("enable agent doublecheck: %v", err)
	}

	run, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run data strategy monitor: %v", err)
	}
	if run.Status != MonitorRunStatusCompleted {
		t.Fatalf("run status = %s, want completed", run.Status)
	}
	if run.HitCount != 1 || run.ReviewCount != 1 || run.AlertCount != 1 || run.FailedCount != 0 {
		t.Fatalf("run counts = %+v, want one hit/review/alert and no failures", run)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskDataStrategyMonitor, Limit: 10})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0].AgentDecisionID == "" {
		t.Fatalf("agent decision id empty, want AgentRun id")
	}
	pipeline := mapFromAny(hits[0].Evidence["reviewPipeline"])
	if pipeline["agentRunId"] != hits[0].AgentDecisionID {
		t.Fatalf("pipeline agentRunId = %v, hit agentDecisionId = %s", pipeline["agentRunId"], hits[0].AgentDecisionID)
	}
	if pipeline["agentStatus"] != "enabled_no_executor" {
		t.Fatalf("agent status = %v, want enabled_no_executor", pipeline["agentStatus"])
	}
	agentRuns, err := svc.ListAgentRuns(ctx, AgentRunListFilter{
		TaskType: AgentTaskTypeOperationReview,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	if len(agentRuns) != 1 || agentRuns[0].Status != AgentRunStatusReady {
		t.Fatalf("agent runs = %+v, want one ready run", agentRuns)
	}
	alerts, err := svc.ListAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "000977", Limit: 10})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if alerts[0].TriggerSource != AlertTriggerSourceDegraded || alerts[0].AgentRunID != hits[0].AgentDecisionID {
		t.Fatalf("alert = %+v, want degraded linked to agent run", alerts[0])
	}
	if alerts[0].Evidence["degraded_reason"] != "agent_ready_without_executor" {
		t.Fatalf("degraded reason = %v, want agent_ready_without_executor", alerts[0].Evidence["degraded_reason"])
	}
}

func TestProcessCreatedMonitorHitIsIdempotentForReviewAndAgentRun(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	configureOperationReviewModelForMonitorTest(t, svc)
	now := time.Now()
	run, err := svc.store.CreateMonitorRun(ctx, MonitorRun{
		ID:          "run-idempotent",
		TaskType:    MonitorTaskDataStrategyMonitor,
		Status:      MonitorRunStatusCompleted,
		TriggerType: MonitorTriggerManual,
		StartedAt:   now,
		FinishedAt:  now,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	hit, err := svc.store.CreateMonitorHit(ctx, MonitorHit{
		ID:        "hit-idempotent",
		RunID:     run.ID,
		TaskType:  MonitorTaskDataStrategyMonitor,
		Status:    MonitorHitStatusCandidate,
		Symbol:    "000977",
		Market:    "SZ",
		Title:     "幂等命中",
		Evidence:  map[string]any{"matchedAction": "watch"},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create hit: %v", err)
	}
	cfg := MonitorTaskConfig{AgentDoublecheckEnabled: true}
	first, err := svc.processCreatedMonitorHit(ctx, hit, cfg)
	if err != nil {
		t.Fatalf("first post process: %v", err)
	}
	second, err := svc.processCreatedMonitorHit(ctx, hit, cfg)
	if err != nil {
		t.Fatalf("second post process: %v", err)
	}
	if !first.ReviewCreated || second.ReviewCreated {
		t.Fatalf("review created flags first=%v second=%v", first.ReviewCreated, second.ReviewCreated)
	}
	if first.ReviewID == "" || second.ReviewID != first.ReviewID {
		t.Fatalf("review ids first=%q second=%q", first.ReviewID, second.ReviewID)
	}
	if first.AgentRunID == "" || second.AgentRunID != first.AgentRunID {
		t.Fatalf("agent run ids first=%q second=%q", first.AgentRunID, second.AgentRunID)
	}
	reviewCount, err := svc.CountOperationReviews(ctx, OperationReviewListFilter{HitID: hit.ID})
	if err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 1 {
		t.Fatalf("review count = %d, want 1", reviewCount)
	}
	agentRunCount, err := svc.CountAgentRuns(ctx, AgentRunListFilter{
		TaskType:          AgentTaskTypeOperationReview,
		TriggerObjectType: "operation_review",
		TriggerObjectID:   first.ReviewID,
	})
	if err != nil {
		t.Fatalf("count agent runs: %v", err)
	}
	if agentRunCount != 1 {
		t.Fatalf("agent run count = %d, want 1", agentRunCount)
	}
	alertCount, err := svc.CountAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "000977"})
	if err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if alertCount != 1 {
		t.Fatalf("alert count = %d, want 1", alertCount)
	}
}

func TestRunDataStrategyMonitorUsesPlaybookPrefilters(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "300750", 210, 2.4, QuoteStatusFresh, time.Now())
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "宁德时代剧本策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "300750",
		Direction: StrategyBiasBullish,
		GenerationMeta: map[string]any{
			"playbook": map[string]any{
				"version": "v1",
				"rules": []any{
					map[string]any{
						"id":     "add-on-breakout",
						"action": "add_position",
						"title":  "突破后加仓",
						"dataPrefilters": []any{
							map[string]any{"key": "break_200", "type": WatchRulePriceAbove, "threshold": 200.0},
						},
					},
				},
			},
			"priceTriggers": map[string]any{"triggerPriceAbove": 999.0},
		},
		CreatedBy: StrategySourceManual,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}

	run, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run monitor: %v", err)
	}
	if run.HitCount != 1 {
		t.Fatalf("hit count = %d, want 1", run.HitCount)
	}
	if run.AlertCount != 1 {
		t.Fatalf("alert count = %d, want 1", run.AlertCount)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskDataStrategyMonitor, Limit: 10})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one playbook hit", hits)
	}
	if got := hits[0].Evidence["matchedAction"]; got != "add_position" {
		t.Fatalf("matched action = %v, want add_position", got)
	}
	if got := hits[0].Evidence["matchedPrefilterKey"]; got != "break_200" {
		t.Fatalf("matched prefilter = %v, want break_200", got)
	}
	pipeline := mapFromAny(hits[0].Evidence["reviewPipeline"])
	if pipeline["reviewId"] == "" || pipeline["agentStatus"] != "skipped" {
		t.Fatalf("review pipeline = %+v, want review with skipped agent", pipeline)
	}
	alerts, err := svc.ListAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if alerts[0].TriggerSource != AlertTriggerSourceDeterministic {
		t.Fatalf("trigger source = %s, want deterministic", alerts[0].TriggerSource)
	}
	if alerts[0].Evidence["trigger_decision"] != "deterministic_policy" {
		t.Fatalf("trigger decision = %v, want deterministic_policy", alerts[0].Evidence["trigger_decision"])
	}
}

func TestPortfolioSentinelPlanWindowState(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, chinaMarketTZ)
	action := map[string]any{
		"validUntil": now.Add(time.Hour).Format(time.RFC3339),
		"monitorWindow": map[string]any{
			"startsAt":  now.Add(-time.Minute).Format(time.RFC3339),
			"expiresAt": now.Add(time.Minute).Format(time.RFC3339),
		},
	}
	if got := portfolioSentinelPlanWindowState(action, now); got != "active" {
		t.Fatalf("active window state = %q", got)
	}
	action["monitorWindow"] = map[string]any{
		"startsAt":  now.Add(time.Minute).Format(time.RFC3339),
		"expiresAt": now.Add(time.Hour).Format(time.RFC3339),
	}
	if got := portfolioSentinelPlanWindowState(action, now); got != "pending" {
		t.Fatalf("pending window state = %q", got)
	}
	action["monitorWindow"] = map[string]any{
		"startsAt":  now.Add(-time.Hour).Format(time.RFC3339),
		"expiresAt": now.Format(time.RFC3339),
	}
	if got := portfolioSentinelPlanWindowState(action, now); got != "expired" {
		t.Fatalf("expired window state = %q", got)
	}
}

func TestQuoteRuleCrossed(t *testing.T) {
	before := StockV2QuoteLatest{LastPrice: 10, PctChange: -1}
	after := StockV2QuoteLatest{LastPrice: 9, PctChange: -3}
	tests := []struct {
		name string
		rule watchRule
		want bool
	}{
		{name: "price below crossed", rule: watchRule{Type: WatchRulePriceBelow, Threshold: 9.5}, want: true},
		{name: "price above not crossed", rule: watchRule{Type: WatchRulePriceAbove, Threshold: 10.5}},
		{name: "pct below crossed", rule: watchRule{Type: WatchRulePctChangeBelow, Threshold: -2}, want: true},
		{name: "price entered range", rule: watchRule{Type: WatchRulePriceBetween, Low: 8.5, High: 9.5}, want: true},
		{name: "daily close waits for scheduled scan", rule: watchRule{Type: WatchRuleDailyCloseBelow, Threshold: 9.5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := quoteRuleCrossed(test.rule, before, after); got != test.want {
				t.Fatalf("quoteRuleCrossed() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMonitorAlertDedupeCooldownUpdatesOccurrence(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "300750", 210, 2.4, QuoteStatusFresh, time.Now())
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "宁德时代剧本策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "300750",
		Direction: StrategyBiasBullish,
		GenerationMeta: map[string]any{
			"playbook": map[string]any{
				"version": "v1",
				"rules": []any{
					map[string]any{
						"id":     "add-on-breakout",
						"action": "add_position",
						"title":  "突破后加仓",
						"dataPrefilters": []any{
							map[string]any{"key": "break_200", "type": WatchRulePriceAbove, "threshold": 200.0},
						},
					},
				},
			},
		},
		CreatedBy: StrategySourceManual,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}

	first, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.AlertCount != 1 || second.AlertCount != 1 {
		t.Fatalf("alert counts first=%d second=%d, want 1/1", first.AlertCount, second.AlertCount)
	}
	alerts, err := svc.ListAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want one reused alert", len(alerts))
	}
	if alerts[0].OccurrenceCount != 2 {
		t.Fatalf("occurrence count = %d, want 2", alerts[0].OccurrenceCount)
	}
	if alerts[0].Evidence["lastRunId"] != second.ID {
		t.Fatalf("last run id = %v, want %s", alerts[0].Evidence["lastRunId"], second.ID)
	}
	if alerts[0].FirstSeenAt.IsZero() || alerts[0].LastSeenAt.IsZero() || alerts[0].LastSeenAt.Before(alerts[0].FirstSeenAt) {
		t.Fatalf("seen timestamps invalid: first=%s last=%s", alerts[0].FirstSeenAt, alerts[0].LastSeenAt)
	}
}

func TestRunUnknownMonitorTaskRejected(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	if _, err := svc.RunMonitorTask(ctx, "unknown_task_type", MonitorTriggerManual); !errors.Is(err, ErrInvalidMonitorTaskType) {
		t.Fatalf("err = %v, want ErrInvalidMonitorTaskType", err)
	}
}

func TestCollectMonitorSymbolsIncludesHoldingsAndStrategies(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	if err := svc.store.CreatePortfolio(ctx, StockV2Portfolio{ID: "p-symbols", Name: "热集合组合"}); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:          "h-symbols",
		PortfolioID: "p-symbols",
		Symbol:      "000001",
		Market:      "SZ",
		Name:        "持仓A",
		Quantity:    100,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "账户无关策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "600000",
		Direction: StrategyDirectionWatch,
		CreatedBy: StrategySourceManual,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}

	got := svc.collectMonitorSymbols(ctx)
	if !stringSliceContains(got, "000001") || !stringSliceContains(got, "600000") {
		t.Fatalf("monitor symbols = %v, want holding and active strategy symbols", got)
	}
}

func TestMonitorRunsAndHitsPagination(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "000977", 61, 1.2, QuoteStatusFresh, time.Now())
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "突破策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "000977",
		Direction: StrategyDirectionWatch,
		GenerationMeta: map[string]any{
			"priceTriggers": map[string]any{"triggerPriceAbove": 60.0},
		},
		CreatedBy: StrategySourceManual,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	count, err := svc.CountMonitorRuns(ctx, MonitorRunListFilter{TaskType: MonitorTaskDataStrategyMonitor})
	if err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 2 {
		t.Fatalf("run count = %d, want 2", count)
	}
	firstPage, err := svc.ListMonitorRuns(ctx, MonitorRunListFilter{TaskType: MonitorTaskDataStrategyMonitor, Limit: 1})
	if err != nil {
		t.Fatalf("list runs page: %v", err)
	}
	if len(firstPage) != 1 {
		t.Fatalf("paginated runs = %d, want 1", len(firstPage))
	}
	hitsCount, err := svc.CountMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskDataStrategyMonitor})
	if err != nil {
		t.Fatalf("count hits: %v", err)
	}
	if hitsCount < 2 {
		t.Fatalf("hit count = %d, want >= 2 (two runs)", hitsCount)
	}
}

func stringSliceContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func configureOperationReviewModelForMonitorTest(t *testing.T, svc *Service) AgentModelProfile {
	t.Helper()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-monitor",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-monitor",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	primaryID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &primaryID,
	}); err != nil {
		t.Fatalf("bind operation_review model: %v", err)
	}
	return model
}
