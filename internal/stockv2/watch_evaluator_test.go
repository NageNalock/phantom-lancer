package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestRunWatchMatchedAndCooldownDedupe(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "000977", 61, 1.2, QuoteStatusFresh, time.Now())
	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:            "价格突破盯盘",
		Source:          WatchSourceManual,
		Symbol:          "000977",
		CooldownSeconds: 3600,
		TriggerConfig: map[string]any{"rules": []any{
			map[string]any{"key": "above_60", "type": WatchRulePriceAbove, "threshold": 60},
		}},
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}

	first, err := svc.RunWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("run watch: %v", err)
	}
	if first.Status != WatchRunStatusMatched || first.Alert == nil {
		t.Fatalf("first run = %+v", first)
	}
	second, err := svc.RunWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("run duplicate watch: %v", err)
	}
	if second.Alert == nil || second.Alert.ID != first.Alert.ID {
		t.Fatalf("second alert = %+v, want existing %q", second.Alert, first.Alert.ID)
	}
	alertCount, err := svc.CountAlerts(ctx, AlertListFilter{WatchID: watch.ID})
	if err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if alertCount != 1 {
		t.Fatalf("alert count = %d, want 1", alertCount)
	}
	updated, err := svc.GetWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("get watch: %v", err)
	}
	if updated.LastCheckedAt.IsZero() || updated.LastTriggeredAt.IsZero() {
		t.Fatalf("watch timestamps = %+v", updated)
	}
}

func TestRunWatchNotMatchedSkippedAndDegraded(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "600000", 10, -0.2, QuoteStatusFresh, time.Now())
	notMatched, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:   "未命中盯盘",
		Source: WatchSourceManual,
		Symbol: "600000",
		TriggerConfig: map[string]any{"rules": []any{
			map[string]any{"key": "below_9", "type": WatchRulePriceBelow, "threshold": 9},
		}},
	})
	if err != nil {
		t.Fatalf("create not matched watch: %v", err)
	}
	notMatchedRun, err := svc.RunWatch(ctx, notMatched.ID)
	if err != nil {
		t.Fatalf("run not matched watch: %v", err)
	}
	if notMatchedRun.Status != WatchRunStatusNotMatched || notMatchedRun.Alert != nil {
		t.Fatalf("not matched run = %+v", notMatchedRun)
	}

	skipped, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:   "暂停盯盘",
		Status: WatchStatusPaused,
		Source: WatchSourceManual,
		Symbol: "600000",
		TriggerConfig: map[string]any{"rules": []any{
			map[string]any{"type": WatchRulePriceAbove, "threshold": 9},
		}},
	})
	if err != nil {
		t.Fatalf("create skipped watch: %v", err)
	}
	skippedRun, err := svc.RunWatch(ctx, skipped.ID)
	if err != nil {
		t.Fatalf("run skipped watch: %v", err)
	}
	if skippedRun.Status != WatchRunStatusSkipped {
		t.Fatalf("skipped run = %+v", skippedRun)
	}

	degraded, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:   "缺数据盯盘",
		Source: WatchSourceManual,
		Symbol: "300999",
		TriggerConfig: map[string]any{"rules": []any{
			map[string]any{"type": WatchRulePriceAbove, "threshold": 1},
		}},
	})
	if err != nil {
		t.Fatalf("create degraded watch: %v", err)
	}
	degradedRun, err := svc.RunWatch(ctx, degraded.ID)
	if err != nil {
		t.Fatalf("run degraded watch: %v", err)
	}
	if degradedRun.Status != WatchRunStatusDegraded || len(degradedRun.RuleResults) != 1 {
		t.Fatalf("degraded run = %+v", degradedRun)
	}
}

func TestRunWatchAnyAllAggregationWithDailyBar(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	seedWatchQuote(t, svc, "000001", 70, 2.1, QuoteStatusFresh, time.Now())
	seedWatchDailyBar(t, svc, "000001", 40)
	rules := []any{
		map[string]any{"key": "above_60", "type": WatchRulePriceAbove, "threshold": 60},
		map[string]any{"key": "close_above_50", "type": WatchRuleDailyCloseAbove, "threshold": 50},
	}

	anyWatch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:          "任一命中",
		Source:        WatchSourceManual,
		Symbol:        "000001",
		TriggerPolicy: WatchTriggerPolicyAny,
		TriggerConfig: map[string]any{"rules": rules},
	})
	if err != nil {
		t.Fatalf("create any watch: %v", err)
	}
	anyRun, err := svc.RunWatch(ctx, anyWatch.ID)
	if err != nil {
		t.Fatalf("run any watch: %v", err)
	}
	if anyRun.Status != WatchRunStatusMatched {
		t.Fatalf("any run = %+v", anyRun)
	}

	allWatch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:          "全部命中",
		Source:        WatchSourceManual,
		Symbol:        "000001",
		TriggerPolicy: WatchTriggerPolicyAll,
		TriggerConfig: map[string]any{"rules": rules},
	})
	if err != nil {
		t.Fatalf("create all watch: %v", err)
	}
	allRun, err := svc.RunWatch(ctx, allWatch.ID)
	if err != nil {
		t.Fatalf("run all watch: %v", err)
	}
	if allRun.Status != WatchRunStatusNotMatched {
		t.Fatalf("all run = %+v", allRun)
	}
}

func TestRunWatchPortfolioSymbolWeightAbove(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	portfolio := StockV2Portfolio{ID: "portfolio-weight", Name: "权重组合", Cash: 7000, RiskLevel: "medium"}
	if err := svc.store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-weight",
		PortfolioID:       portfolio.ID,
		Symbol:            "000001",
		Market:            "SZ",
		Name:              "平安银行",
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
		ID:                 "snapshot-weight",
		PortfolioID:        portfolio.ID,
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
	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:        "组合权重盯盘",
		Source:      WatchSourcePortfolioMonitor,
		PortfolioID: portfolio.ID,
		Symbol:      "000001",
		TriggerConfig: map[string]any{"rules": []any{
			map[string]any{"key": "weight_above_20", "type": WatchRulePortfolioSymbolWeightOver, "threshold": 20},
		}},
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	run, err := svc.RunWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("run watch: %v", err)
	}
	if run.Status != WatchRunStatusMatched || run.Alert == nil {
		t.Fatalf("portfolio run = %+v", run)
	}
}

func TestRunWatchPortfolioSymbolWeightBelow(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	portfolio := StockV2Portfolio{ID: "portfolio-weight-below", Name: "低权重组合", Cash: 9000, RiskLevel: "medium"}
	if err := svc.store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-weight-below",
		PortfolioID:       portfolio.ID,
		Symbol:            "300750",
		Market:            "SZ",
		Name:              "宁德时代",
		Quantity:          10,
		AvailableQuantity: 10,
		CostPrice:         100,
		LastPrice:         100,
		LastPriceAt:       time.Now(),
		MarketValue:       1000,
		PositionPct:       10,
		TradableStatus:    PortfolioValuationStatusFresh,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if err := svc.store.CreatePortfolioSnapshot(ctx, PortfolioSnapshot{
		ID:                 "snapshot-weight-below",
		PortfolioID:        portfolio.ID,
		ValuationAt:        time.Now(),
		Cash:               9000,
		HoldingMarketValue: 1000,
		TotalAssetValue:    10000,
		CashPct:            90,
		PositionCount:      1,
		Source:             PortfolioValuationSourceLatestQuote,
		Status:             PortfolioValuationStatusFresh,
		CreatedAt:          time.Now(),
	}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:        "低仓位盯盘",
		Source:      WatchSourcePortfolioMonitor,
		PortfolioID: portfolio.ID,
		Symbol:      "300750",
		TriggerConfig: map[string]any{"rules": []any{
			map[string]any{"key": "weight_below_20", "type": WatchRulePortfolioSymbolWeightBelow, "threshold": 20},
		}},
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	run, err := svc.RunWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("run watch: %v", err)
	}
	if run.Status != WatchRunStatusMatched || run.Alert == nil {
		t.Fatalf("portfolio below run = %+v", run)
	}
}

func seedWatchQuote(t *testing.T, svc *Service, symbol string, price, pct float64, status string, fetchedAt time.Time) {
	t.Helper()
	if err := svc.store.UpsertLatestQuote(context.Background(), StockV2QuoteLatest{
		Symbol:    symbol,
		Market:    inferAStockMarket(symbol),
		Name:      symbol,
		LastPrice: price,
		PrevClose: price / (1 + pct/100),
		PctChange: pct,
		QuoteAt:   fetchedAt,
		FetchedAt: fetchedAt,
		Source:    QuoteSourceTencent,
		Status:    status,
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
}

func seedWatchDailyBar(t *testing.T, svc *Service, symbol string, close float64) {
	t.Helper()
	now := time.Now()
	if err := svc.store.UpsertDailyBars(context.Background(), []StockV2DailyBar{{
		ID:        "bar-" + symbol,
		Symbol:    symbol,
		Market:    inferAStockMarket(symbol),
		TradeDate: now.In(chinaMarketTZ).Format("2006-01-02"),
		Open:      close,
		High:      close,
		Low:       close,
		Close:     close,
		PrevClose: close,
		Adjusted:  DailyBarAdjustedNone,
		Source:    "test",
		FetchedAt: now,
		Quality:   "ok",
		CreatedAt: now,
		UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("upsert daily bar: %v", err)
	}
}
