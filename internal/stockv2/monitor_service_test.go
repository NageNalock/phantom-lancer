package stockv2

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
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
	if len(tasks) != 8 {
		t.Fatalf("task count = %d, want 8", len(tasks))
	}
	runnable := make(map[string]bool, len(tasks))
	enabledCount := 0
	for _, task := range tasks {
		runnable[task.Definition.TaskType] = task.Definition.Runnable
		if task.Config.Enabled {
			enabledCount++
		}
	}
	if enabledCount != 0 {
		t.Fatalf("default enabled count = %d, want 0", enabledCount)
	}
	if !runnable[MonitorTaskDataStrategyMonitor] || !runnable[MonitorTaskPortfolioRiskMonitor] {
		t.Fatalf("data_strategy / portfolio_risk must be runnable")
	}
	if runnable[MonitorTaskNewsStrategyMonitor] || runnable[MonitorTaskDailyFundamentalMonitor] || runnable[MonitorTaskDataQualityMonitor] {
		t.Fatalf("news / fundamental / quality must not be runnable this round")
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

	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskDataStrategyMonitor, Limit: 50})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no monitor hits produced")
	}
	if hits[0].Status != MonitorHitStatusCandidate {
		t.Fatalf("hit status = %s, want candidate", hits[0].Status)
	}
	if hits[0].Symbol != "000977" {
		t.Fatalf("hit symbol = %q", hits[0].Symbol)
	}
	if hits[0].AgentDecisionID != "" {
		t.Fatalf("agent decision id = %q, want empty until executor exists", hits[0].AgentDecisionID)
	}
	if got := hits[0].Evidence["agentDoublecheck"]; got != "enabled_no_executor" {
		t.Fatalf("agent state = %v, want enabled_no_executor", got)
	}
}

func TestRunPortfolioRiskMonitorProducesHit(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	portfolio := StockV2Portfolio{
		ID:                   "p-risk",
		Name:                 "风险组合",
		Cash:                 7000,
		RiskLevel:            "medium",
		MaxSinglePositionPct: 20,
	}
	if err := svc.store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "h-risk",
		PortfolioID:       "p-risk",
		Symbol:            "000001",
		Market:            "SZ",
		Name:              "持仓A",
		Quantity:          100,
		AvailableQuantity: 100,
		CostPrice:         30,
		LastPrice:         30,
		LastPriceAt:       time.Now(),
		MarketValue:       3000,
		PositionPct:       30,
		TradableStatus:    PortfolioValuationStatusFresh,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if err := svc.store.CreatePortfolioSnapshot(ctx, PortfolioSnapshot{
		ID:                 "s-risk",
		PortfolioID:        "p-risk",
		ValuationAt:        time.Now(),
		Cash:               7000,
		HoldingMarketValue: 3000,
		TotalAssetValue:    10000,
		CashPct:            70,
		PositionCount:      1,
		Source:             PortfolioValuationSourceLatestQuote,
		Status:             PortfolioValuationStatusFresh,
		CreatedAt:          time.Now(),
	}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	run, err := svc.RunMonitorTask(ctx, MonitorTaskPortfolioRiskMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run portfolio risk monitor: %v", err)
	}
	if run.Status != MonitorRunStatusCompleted {
		t.Fatalf("run status = %s, want completed", run.Status)
	}
	if run.HitCount < 1 {
		t.Fatalf("hit count = %d, want >= 1 (weight over limit)", run.HitCount)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskPortfolioRiskMonitor, PortfolioID: "p-risk", Limit: 50})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no portfolio risk hit for p-risk")
	}
}

func TestRunDisabledMonitorTaskRejected(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	if _, err := svc.RunMonitorTask(ctx, MonitorTaskNewsStrategyMonitor, MonitorTriggerManual); !errors.Is(err, ErrMonitorTaskNotConfigured) {
		t.Fatalf("err = %v, want ErrMonitorTaskNotConfigured", err)
	}
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
