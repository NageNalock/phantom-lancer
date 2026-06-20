package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestCreateReviewFromMonitorHitBuildsContextPack(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)

	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	if review.Status != OperationReviewStatusPending {
		t.Fatalf("review status = %s, want pending", review.Status)
	}
	reloaded, err := svc.GetOperationReview(ctx, review.ID)
	if err != nil {
		t.Fatalf("reload review: %v", err)
	}
	if reloaded.HitID != hit.ID || reloaded.StrategyID == "" || reloaded.PortfolioID == "" || reloaded.Symbol != "000977" {
		t.Fatalf("review linkage = %+v", review)
	}
	if reloaded.InputContext.Hit.ID != hit.ID {
		t.Fatalf("context hit id = %q, want %q", reloaded.InputContext.Hit.ID, hit.ID)
	}
	if reloaded.InputContext.Strategy == nil || reloaded.InputContext.Strategy.ActiveVersion == nil {
		t.Fatalf("context strategy missing: %+v", reloaded.InputContext.Strategy)
	}
	if got := reloaded.InputContext.Strategy.ActiveVersion.GenerationMeta["strategyBias"]; got != StrategyBiasBullish {
		t.Fatalf("strategy bias = %v, want bullish", got)
	}
	if reloaded.InputContext.Quote == nil || reloaded.InputContext.Quote.LastPrice != 61 {
		t.Fatalf("context quote = %+v, want last price 61", reloaded.InputContext.Quote)
	}
	if reloaded.InputContext.Portfolio == nil || reloaded.InputContext.Portfolio.Snapshot == nil {
		t.Fatalf("portfolio snapshot missing: %+v", reloaded.InputContext.Portfolio)
	}
	if reloaded.InputContext.Portfolio.Snapshot.TotalAssetValue != 10000 {
		t.Fatalf("snapshot total = %.2f, want 10000", reloaded.InputContext.Portfolio.Snapshot.TotalAssetValue)
	}
}

func TestCreateReviewFromMonitorHitIsIdempotentWhileActive(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	first, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create first review: %v", err)
	}
	second, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create second review: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second review id = %q, want existing %q", second.ID, first.ID)
	}
	count, err := svc.CountOperationReviews(ctx, OperationReviewListFilter{HitID: hit.ID})
	if err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if count != 1 {
		t.Fatalf("review count = %d, want 1", count)
	}
	items, err := svc.ListOperationReviews(ctx, OperationReviewListFilter{HitID: hit.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("reviews = %+v, want first review only", items)
	}
	run, err := svc.store.GetMonitorRun(ctx, hit.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.ReviewCount != 1 {
		t.Fatalf("run review count = %d, want 1", run.ReviewCount)
	}
}

func TestSaveOperationReviewResultMarksHitReviewed(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	updated, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputIgnore,
		ResultSummary: "人工判断无需处理，继续观察。",
		Result:        map[string]any{"reason": "manual_ignore"},
	})
	if err != nil {
		t.Fatalf("save review result: %v", err)
	}
	if updated.Status != OperationReviewStatusCompleted || updated.OutputType != OperationReviewOutputIgnore {
		t.Fatalf("updated review = %+v", updated)
	}
	if updated.CompletedAt.IsZero() {
		t.Fatalf("completed_at should be set")
	}
	reloadedHit, err := svc.store.GetMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("reload hit: %v", err)
	}
	if reloadedHit.Status != MonitorHitStatusIgnored {
		t.Fatalf("hit status = %s, want ignored", reloadedHit.Status)
	}
}

func seedReviewMonitorHit(t *testing.T, svc *Service) MonitorHit {
	t.Helper()
	ctx := context.Background()
	now := time.Now()

	portfolio := StockV2Portfolio{
		ID:                   "portfolio-review",
		Name:                 "Review 组合",
		Cash:                 7000,
		RiskLevel:            "medium",
		MaxSinglePositionPct: 20,
		MaxDrawdownPct:       30,
		AllowBuy:             true,
		AllowAdd:             true,
		AllowReduce:          true,
		AllowSell:            true,
	}
	if err := svc.store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-review",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          50,
		AvailableQuantity: 50,
		CostPrice:         50,
		LastPrice:         60,
		LastPriceAt:       now,
		TradableStatus:    PortfolioValuationStatusFresh,
		MarketValue:       3000,
		PnL:               500,
		PositionPct:       30,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if err := svc.store.CreatePortfolioSnapshot(ctx, PortfolioSnapshot{
		ID:                 "snapshot-review",
		PortfolioID:        portfolio.ID,
		ValuationAt:        now,
		Cash:               7000,
		HoldingMarketValue: 3000,
		TotalAssetValue:    10000,
		CashPct:            70,
		PositionCount:      1,
		Source:             PortfolioValuationSourceLatestQuote,
		Status:             PortfolioValuationStatusFresh,
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	seedWatchQuote(t, svc, "000977", 61, 1.2, QuoteStatusFresh, now)
	seedWatchDailyBar(t, svc, "000977", 60)

	strategy, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:        "Review 策略",
		Kind:        StrategyKindSymbolStrategy,
		Scope:       StrategyScopePortfolioBound,
		Source:      StrategySourceManual,
		Status:      StrategyStatusActive,
		Symbol:      "000977",
		Market:      "SZ",
		PortfolioID: portfolio.ID,
		Direction:   StrategyBiasBullish,
		GenerationMeta: map[string]any{
			"strategyBias": StrategyBiasBullish,
			"playbook": map[string]any{
				"rules": []any{
					map[string]any{"id": "breakout", "action": "add_position", "title": "突破观察"},
				},
			},
		},
		CreatedBy: StrategySourceManual,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	run, err := svc.store.CreateMonitorRun(ctx, MonitorRun{
		ID:          "run-review",
		TaskType:    MonitorTaskDataStrategyMonitor,
		Status:      MonitorRunStatusCompleted,
		TriggerType: MonitorTriggerManual,
		StartedAt:   now,
		FinishedAt:  now,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("create monitor run: %v", err)
	}
	hit, err := svc.store.CreateMonitorHit(ctx, MonitorHit{
		ID:          "hit-review",
		RunID:       run.ID,
		TaskType:    MonitorTaskDataStrategyMonitor,
		Status:      MonitorHitStatusCandidate,
		StrategyID:  strategy.Strategy.ID,
		PortfolioID: portfolio.ID,
		Symbol:      "000977",
		Market:      "SZ",
		Title:       "策略动作候选: 加仓",
		Summary:     "价格突破预设观察线。",
		Evidence: map[string]any{
			"matchedAction":       "add_position",
			"matchedPrefilterKey": "break_60",
			"playbookRule":        map[string]any{"id": "breakout", "action": "add_position"},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create monitor hit: %v", err)
	}
	return hit
}
