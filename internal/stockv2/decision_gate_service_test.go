package stockv2

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDecisionGateBlocksOverheatedEntryButKeepsReduction(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	svc := NewService(store, nil, nil)
	defer svc.Close()
	bars := decisionTestBars(40, 10, .8)
	snapshot := svc.buildDecisionGateSnapshot(context.Background(), decisionGateBuildInput{
		ContextType: "test", ContextID: "gate", Symbol: "600000", Market: "SH",
		InstrumentType: InstrumentTypeStock, TradeDate: bars[len(bars)-1].TradeDate, Bars: bars,
		DecisionDate: bars[len(bars)-1].TradeDate, Quote: decisionTestQuote("600000", bars[len(bars)-1].Close),
		BenchmarkAvailable: true, ReferenceHealth: decisionReferenceHealth{
			EventOK: true, FinanceOK: true, Status: DecisionHealthHealthy, CheckedAt: time.Now(),
		},
		TradeCalendar: decisionTestTradeCalendar(bars[len(bars)-1].TradeDate),
	})
	if snapshot.Status != DecisionHealthBlocked {
		t.Fatalf("status=%s, want blocked; gates=%+v", snapshot.Status, snapshot.Gates)
	}
	if decisionActionAllowed(snapshot, StrategyGenerationRuleActionBuildPosition) {
		t.Fatal("overheated snapshot unexpectedly allows build_position")
	}
	if !decisionActionAllowed(snapshot, StrategyGenerationRuleActionReduce) {
		t.Fatal("risk reduction must remain allowed")
	}
}

func TestDecisionGateEventProtectionUsesPointInTimeEvent(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	bars := decisionTestBars(40, 10, .05)
	tradeDate := bars[len(bars)-1].TradeDate
	base, _ := time.Parse("2006-01-02", tradeDate)
	eventDate := base.AddDate(0, 0, 1).Format("2006-01-02")
	if err := store.UpsertDecisionMarketEvents(ctx, []decisionMarketEvent{{
		Symbol: "600000", EventType: "disclosure_date", EventDate: eventDate,
		AnnouncedAt: tradeDate, Title: "定期报告披露", Source: "test", FetchedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot := svc.buildDecisionGateSnapshot(ctx, decisionGateBuildInput{
		ContextType: "test", ContextID: "event", Symbol: "600000", Market: "SH",
		InstrumentType: InstrumentTypeStock, TradeDate: tradeDate, DecisionDate: tradeDate, Bars: bars,
		Quote:              decisionTestQuote("600000", bars[len(bars)-1].Close),
		BenchmarkAvailable: true, ReferenceHealth: decisionReferenceHealth{
			EventOK: true, FinanceOK: true, Status: DecisionHealthHealthy, CheckedAt: time.Now(),
		},
		TradeCalendar: decisionTestTradeCalendar(tradeDate),
	})
	if gateStatus(snapshot.Gates, DecisionGateEventCalendar) != DecisionGateStatusBlocked {
		t.Fatalf("event gate=%+v, want blocked", snapshot.Gates)
	}
}

func TestDecisionPostDisclosurePriceConfirmationAllowsOpportunityReview(t *testing.T) {
	event := decisionMarketEvent{EventType: "disclosure_date", EventDate: "2026-08-31"}
	quoteAt := time.Date(2026, 8, 31, 10, 30, 0, 0, chinaMarketTZ)
	quote := StockV2QuoteLatest{LastPrice: 10.6, OpenPrice: 10.2, PctChange: 4, QuoteAt: quoteAt, Status: QuoteStatusFresh}
	if !decisionPostDisclosureConfirmed(event, "2026-08-31", &quote) {
		t.Fatal("positive post-disclosure price confirmation should permit opportunity evaluation")
	}
	quote.PctChange = -2
	if decisionPostDisclosureConfirmed(event, "2026-08-31", &quote) {
		t.Fatal("negative price action must remain inside the event guardrail")
	}
}

func TestDecisionGateStrategyReportDowngradesBlockedBuyToNoChange(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	run := AgentRun{TriggerObjectID: "manual_target:symbols=600000"}
	snapshot, err := store.SaveDecisionGateSnapshot(ctx, DecisionGateSnapshot{
		ContextType: "strategy_generation", ContextID: run.TriggerObjectID, Symbol: "600000", Market: "SH",
		Status: DecisionHealthBlocked, AllowedActions: []string{StrategyGenerationRuleActionHold, StrategyGenerationRuleActionReduce},
		Gates:     []DecisionGateResult{{Key: DecisionGateVolatility, Label: "波动与 ATR 校准", Status: DecisionGateStatusBlocked, Summary: "过热"}},
		CreatedAt: time.Now(),
	})
	if err != nil || snapshot.ID == "" {
		t.Fatal(err)
	}
	report := StrategyGenerationReport{RunSummary: StrategyGenerationRunSummary{Mode: StrategyGenerationModeManualTarget}, Drafts: []StrategyGenerationDraft{{
		Symbol: "600000", DraftType: StrategyGenerationDraftTypeNewStrategy,
		Playbook: StrategyGenerationPlaybook{Rules: []StrategyGenerationPlaybookRule{{ID: "buy", Action: StrategyGenerationRuleActionBuildPosition}}},
	}}}
	svc.applyDecisionGatesToStrategyReport(ctx, run, &report)
	if report.Drafts[0].DraftType != StrategyGenerationDraftTypeNoChange || len(report.Drafts[0].Playbook.Rules) != 0 {
		t.Fatalf("draft=%+v, want no_change", report.Drafts[0])
	}
}

func TestDecisionGateDowngradesSentinelBuildWithoutRejectingRiskReport(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	if _, err := store.SaveDecisionGateSnapshot(ctx, DecisionGateSnapshot{
		ContextType: "portfolio_sentinel", ContextID: "run-1", Symbol: "600000", Market: "SH",
		Status: DecisionHealthBlocked, AllowedActions: []string{StrategyGenerationRuleActionHold},
		Gates: []DecisionGateResult{{Key: DecisionGateEventCalendar, Label: "事件日历保护", Status: DecisionGateStatusBlocked, Summary: "财报窗口"}},
	}); err != nil {
		t.Fatal(err)
	}
	threshold := 10.0
	plan := PortfolioSentinelActionPlan{Symbol: "600000", Action: PortfolioSentinelPlanBuild,
		Conditions: []PortfolioSentinelPlanCondition{{Key: "entry", Type: WatchRulePriceBelow, Threshold: &threshold}},
		Sizing:     &PortfolioSentinelPlanSizing{Mode: PortfolioSentinelSizingTargetPortfolioPct, Value: 5}, Reason: "候选建仓"}
	if downgraded := svc.applyDecisionGateToPortfolioPlan(ctx, "run-1", &plan); !downgraded || plan.Action != PortfolioSentinelPlanHold || plan.Sizing != nil || len(plan.Conditions) != 0 {
		t.Fatalf("plan=%+v downgraded=%t", plan, downgraded)
	}
}

func TestParseDecisionReferenceRows(t *testing.T) {
	result := tushareDatasetResult{
		Fields: []string{"ann_date", "end_date", "revenue", "n_income_attr_p"},
		Items:  [][]any{{"20260801", "20260630", 100.0, 12.0}},
		Source: "test",
	}
	items := parseDecisionFinancialFacts("600000", "income", result, time.Now())
	if len(items) != 1 || items[0].ReportPeriod != "2026-06-30" || items[0].NetProfit != 12 {
		t.Fatalf("facts=%+v", items)
	}
}

func TestDecisionFinancialFactsMergeDatasetsWithoutErasingLoss(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	items := []decisionFinancialFact{
		{Symbol: "600000", ReportPeriod: "2026-06-30", Dataset: "income", AnnouncedAt: "2026-08-01", Revenue: 100, NetProfit: -12, Source: "primary", FetchedAt: time.Now()},
		{Symbol: "600000", ReportPeriod: "2026-06-30", Dataset: "cashflow", AnnouncedAt: "2026-08-02", OperatingCashFlow: 9, Source: "backup", FetchedAt: time.Now()},
		{Symbol: "600000", ReportPeriod: "2026-06-30", Dataset: "fina_indicator", AnnouncedAt: "2026-08-02", ROE: -3, Source: "backup", FetchedAt: time.Now()},
	}
	if err := store.UpsertDecisionFinancialFacts(ctx, items); err != nil {
		t.Fatal(err)
	}
	fact, err := store.GetLatestDecisionFinancialFact(ctx, "600000", "2026-08-03")
	if err != nil || fact.NetProfit != -12 || fact.OperatingCashFlow != 9 || fact.ROE != -3 || fact.Source != "mixed" {
		t.Fatalf("fact=%+v err=%v", fact, err)
	}
}

func TestDecisionTradeSessionDistanceUsesExchangeCalendar(t *testing.T) {
	calendar := []decisionTradeDay{
		{Date: "2026-10-05", Open: true},
		{Date: "2026-10-06", Open: false},
		{Date: "2026-10-07", Open: true},
		{Date: "2026-10-08", Open: true},
	}
	if distance, ok := decisionTradeSessionDistance("2026-10-05", "2026-10-08", calendar); !ok || distance != 2 {
		t.Fatalf("distance=%d ok=%t, want 2 true", distance, ok)
	}
	if distance, ok := decisionTradeSessionDistance("2026-10-08", "2026-10-05", calendar); !ok || distance != -2 {
		t.Fatalf("reverse distance=%d ok=%t, want -2 true", distance, ok)
	}
}

func TestDecisionBasisRequiresFinancialFactsForEarningsThesis(t *testing.T) {
	if !decisionBasisNeedsFinancialFacts([]string{"订单和利润改善构成主要建仓依据"}) {
		t.Fatal("earnings-led thesis must require point-in-time financial facts")
	}
	if decisionBasisNeedsFinancialFacts([]string{"价格站上 MA20 且量价确认"}) {
		t.Fatal("price-only thesis must not require financial facts")
	}
}

func TestEnsureDecisionGateAuditFieldsKeepsServerEvidence(t *testing.T) {
	draft := StrategyGenerationDraft{Thesis: "订单和利润改善", DecisionBasis: []string{"catalyst"}}
	snapshot := DecisionGateSnapshot{
		ID: "gate-snapshot-1",
		Gates: []DecisionGateResult{{
			Key: DecisionGateCatalystPricing, Status: DecisionGateStatusPass,
		}},
		DataHealth: []DecisionDataHealth{{Key: "fund_flow", Status: DecisionHealthHealthy}},
	}
	ensureDecisionGateAuditFields(&draft, snapshot, map[string]bool{"evidence-1": true})
	for _, want := range []string{"price", "factor", "event_calendar", "catalyst", "flow", "fundamental"} {
		if !containsString(draft.DecisionBasis, want) {
			t.Fatalf("decision basis=%v, missing %q", draft.DecisionBasis, want)
		}
	}
	if !containsString(draft.EvidenceRefIDs, snapshot.ID) {
		t.Fatalf("evidence refs=%v, missing snapshot %q", draft.EvidenceRefIDs, snapshot.ID)
	}
	draft.EvidenceRefIDs = []string{"evidence-1", "other-candidate-evidence"}
	ensureDecisionGateAuditFields(&draft, snapshot, map[string]bool{"evidence-1": true})
	if !containsString(draft.EvidenceRefIDs, "evidence-1") || containsString(draft.EvidenceRefIDs, "other-candidate-evidence") {
		t.Fatalf("cross-candidate evidence was not filtered: %v", draft.EvidenceRefIDs)
	}
}

func TestPortfolioSentinelFundFlowPreflightReusesEvidenceAndSkipsETF(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	svc := NewService(store, nil, nil)
	defer svc.Close()
	resolutions, err := svc.preparePortfolioSentinelFundFlow(context.Background(), OpportunityMarketScanConfig{},
		[]portfolioSentinelFundFlowTarget{
			{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, RequiredAsOf: "2026-08-31"},
			{Symbol: "510300", Market: "SH", InstrumentType: InstrumentTypeExchangeFund, RequiredAsOf: "2026-08-31"},
		}, map[string]OpportunityMarketScanMetrics{
			"600000": {FundFlowAvailable: true, FundFlowAsOf: "2026-08-31", FundFlowSource: "test", MainFlowRatio20: 1.25},
		})
	if err != nil {
		t.Fatal(err)
	}
	if !resolutions["600000"].Available || resolutions["600000"].Evidence.MainFlowRatio20 != 1.25 {
		t.Fatalf("stock resolution=%+v", resolutions["600000"])
	}
	if !resolutions["510300"].NotApplicable || resolutions["510300"].Available {
		t.Fatalf("ETF resolution=%+v", resolutions["510300"])
	}
}

func TestDecisionFundFlowCacheRoundTripAndFreshness(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	want := decisionFundFlowEvidence{Symbol: "600000", Market: "SH", AsOf: "2026-08-31",
		MainNetInflow5: 10, MainNetInflow20: 20, MainNetInflow60: 30, MainFlowRatio20: 1.5,
		PositiveFlowDays20: 12, Source: "test", FetchedAt: time.Now()}
	if err := store.UpsertDecisionFundFlowEvidence(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDecisionFundFlowEvidence(ctx, want.Symbol)
	if err != nil || got.AsOf != want.AsOf || got.MainFlowRatio20 != want.MainFlowRatio20 || got.Source != want.Source {
		t.Fatalf("evidence=%+v err=%v", got, err)
	}
	if !decisionFundFlowAsOfUsable(got.AsOf, "2026-08-31") || decisionFundFlowAsOfUsable(got.AsOf, "2026-09-01") {
		t.Fatalf("unexpected freshness for %+v", got)
	}
}

func TestDecisionFundFlowPointsRoundTripNewestBounded(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	store := svc.store
	ctx := context.Background()
	now := time.Now()
	points := []opportunityFundFlowPoint{
		{TradeDate: "2026-08-31", MainNet: 10, Turnover: 100},
		{TradeDate: "2026-09-01", MainNet: -20, Turnover: 200},
		{TradeDate: "2026-09-02", MainNet: -30, Turnover: 300},
	}
	if err := store.UpsertDecisionFundFlowPoints(ctx, "000977", "SZ", "fixture", points, now); err != nil {
		t.Fatalf("upsert fund-flow points: %v", err)
	}
	items, err := store.ListDecisionFundFlowPoints(ctx, "000977", "", "", 2)
	if err != nil {
		t.Fatalf("list fund-flow points: %v", err)
	}
	if len(items) != 2 || items[0].TradeDate != "2026-09-01" || items[1].MainNet != -30 || items[1].Source != "fixture" {
		t.Fatalf("items = %+v, want newest two in ascending order", items)
	}
}

func TestDecisionGateMarksStockFundFlowHealthyAndETFNotApplicable(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	svc := NewService(store, nil, nil)
	defer svc.Close()
	bars := decisionTestBars(40, 10, .05)
	tradeDate := bars[len(bars)-1].TradeDate
	base := decisionGateBuildInput{ContextType: "test", ContextID: "flow", Market: "SH", TradeDate: tradeDate,
		DecisionDate: tradeDate, Bars: bars, BenchmarkAvailable: true,
		ReferenceHealth: decisionReferenceHealth{EventOK: true, FinanceOK: true, Status: DecisionHealthHealthy, CheckedAt: time.Now()},
		TradeCalendar:   decisionTestTradeCalendar(tradeDate)}
	stock := base
	stock.Symbol, stock.InstrumentType, stock.Quote = "600000", InstrumentTypeStock, decisionTestQuote("600000", bars[len(bars)-1].Close)
	stock.FlowAvailable, stock.FlowAsOf, stock.FlowSource, stock.MainFlowRatio20 = true, tradeDate, "test", 1.2
	stockSnapshot := svc.buildDecisionGateSnapshot(context.Background(), stock)
	stockFlow := decisionDataHealth(stockSnapshot.DataHealth, "fund_flow")
	if stockFlow.Status != DecisionHealthHealthy || stockFlow.AsOf != tradeDate || stockFlow.Source != "test" {
		t.Fatalf("stock flow health=%+v", stockFlow)
	}
	etf := base
	etf.Symbol, etf.InstrumentType, etf.Quote = "510300", InstrumentTypeExchangeFund, decisionTestQuote("510300", bars[len(bars)-1].Close)
	etf.FlowNotApplicable = true
	etfSnapshot := svc.buildDecisionGateSnapshot(context.Background(), etf)
	if got := decisionDataHealth(etfSnapshot.DataHealth, "fund_flow"); got.Status != DecisionHealthNotApplicable {
		t.Fatalf("ETF flow health=%+v", got)
	}
}

func TestDecisionBarFeaturesDetectPostBreakoutDistribution(t *testing.T) {
	bars := make([]StockV2DailyBar, 40)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	for i := range bars {
		bars[i] = StockV2DailyBar{
			Symbol: "000977", TradeDate: start.AddDate(0, 0, i).Format("2006-01-02"),
			Open: 100, High: 102, Low: 99, Close: 101, PrevClose: 101,
			Volume: 100, Amount: 1000, Source: "fixture", Adjusted: DailyBarAdjustedQFQ,
		}
	}
	for i := 1; i < 36; i++ {
		bars[i].PrevClose = bars[i-1].Close
	}
	bars[36].Open, bars[36].High, bars[36].Low, bars[36].Close = 102, 111, 102, 111
	bars[36].PrevClose, bars[36].PctChange, bars[36].Volume, bars[36].Amount = 101, 9.9, 300, 3000
	bars[37].Open, bars[37].High, bars[37].Low, bars[37].Close = 112, 113, 110, 111
	bars[37].PrevClose, bars[37].PctChange, bars[37].Volume, bars[37].Amount = 111, 0, 220, 2200
	bars[38].Open, bars[38].High, bars[38].Low, bars[38].Close = 113, 114, 111, 112
	bars[38].PrevClose, bars[38].PctChange, bars[38].Volume, bars[38].Amount = 111, 0.9, 230, 2300
	bars[39].Open, bars[39].High, bars[39].Low, bars[39].Close = 113, 114, 111, 111.5
	bars[39].PrevClose, bars[39].PctChange, bars[39].Volume, bars[39].Amount = 112, -0.45, 240, 2400

	features := calculateDecisionBarFeatures(bars)
	if !features.Valid || !features.PostBreakoutDistribution || !features.PotentialSupplyPressure {
		t.Fatalf("features = %+v, want post-breakout supply pressure", features)
	}
	if features.LowCloseDays3 != 3 || features.Return3Pct <= 0 || features.Return3Pct >= 1 || features.VolumeRatio3ToPrior <= 1.5 {
		t.Fatalf("features = %+v, want bounded high-volume stall metrics", features)
	}

	for i := 36; i < len(bars); i++ {
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = 101, 104, 100, 103.5
		bars[i].PrevClose, bars[i].PctChange, bars[i].Volume, bars[i].Amount = 101, 2.4, 100, 1000
	}
	normal := calculateDecisionBarFeatures(bars)
	if normal.PotentialSupplyPressure || normal.PostBreakoutDistribution || normal.HighVolumeStall {
		t.Fatalf("normal features = %+v, want no supply-pressure flag", normal)
	}
}

func TestDecisionGateBlocksNewRiskOnMarketStructurePressure(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	bars := make([]StockV2DailyBar, 40)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	for i := range bars {
		bars[i] = StockV2DailyBar{Symbol: "000977", TradeDate: start.AddDate(0, 0, i).Format("2006-01-02"),
			Open: 100, High: 102, Low: 99, Close: 101, PrevClose: 101, Volume: 100, Source: "fixture"}
	}
	bars[36] = StockV2DailyBar{Symbol: "000977", TradeDate: bars[36].TradeDate, Open: 102, High: 111, Low: 102, Close: 111, PrevClose: 101, PctChange: 9.9, Volume: 300, Source: "fixture"}
	bars[37] = StockV2DailyBar{Symbol: "000977", TradeDate: bars[37].TradeDate, Open: 112, High: 113, Low: 110, Close: 111, PrevClose: 111, Volume: 220, Source: "fixture"}
	bars[38] = StockV2DailyBar{Symbol: "000977", TradeDate: bars[38].TradeDate, Open: 113, High: 114, Low: 111, Close: 112, PrevClose: 111, PctChange: .9, Volume: 230, Source: "fixture"}
	bars[39] = StockV2DailyBar{Symbol: "000977", TradeDate: bars[39].TradeDate, Open: 113, High: 114, Low: 111, Close: 111.5, PrevClose: 112, PctChange: -.45, Volume: 240, Source: "fixture"}

	snapshot := svc.buildDecisionGateSnapshot(context.Background(), decisionGateBuildInput{
		Symbol: "000977", Market: "SZ", Bars: bars, InstrumentType: InstrumentTypeExchangeFund,
		BenchmarkAvailable: true, MarketRegime: "neutral",
	})
	if decisionActionAllowed(snapshot, StrategyGenerationRuleActionAddPosition) {
		t.Fatalf("snapshot = %+v, want add_position blocked", snapshot)
	}
	found := false
	for _, gate := range snapshot.Gates {
		if gate.Key == DecisionGateMarketStructure {
			found = gate.Status == DecisionGateStatusBlocked && len(gate.Reasons) > 0
		}
	}
	if !found {
		t.Fatalf("snapshot = %+v, want blocked market-structure gate", snapshot)
	}
}

func TestStrategyGenerationDraftSkipReasonExplainsGateAndPatch(t *testing.T) {
	blocked := StrategyGenerationDraft{
		DraftType:   StrategyGenerationDraftTypeNoChange,
		RiskSummary: []string{"确定性门阻断：财务事实不完整"},
	}
	if got := strategyGenerationDraftSkipReason(blocked); got != "确定性门阻断：财务事实不完整" {
		t.Fatalf("blocked reason=%q", got)
	}
	patch := StrategyGenerationDraft{DraftType: StrategyGenerationDraftTypeStrategyPatch}
	if got := strategyGenerationDraftSkipReason(patch); got != "Agent 建议修补已有策略，本轮不新建草案" {
		t.Fatalf("patch reason=%q", got)
	}
}

func decisionDataHealth(items []DecisionDataHealth, key string) DecisionDataHealth {
	for _, item := range items {
		if item.Key == key {
			return item
		}
	}
	return DecisionDataHealth{}
}

func decisionTestBars(count int, start, dailyStep float64) []StockV2DailyBar {
	out := make([]StockV2DailyBar, 0, count)
	date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	closeValue := start
	for len(out) < count {
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			date = date.AddDate(0, 0, 1)
			continue
		}
		previous := closeValue
		closeValue += dailyStep
		out = append(out, StockV2DailyBar{
			Symbol: "600000", Market: "SH", TradeDate: date.Format("2006-01-02"),
			Open: previous, High: closeValue + .1, Low: previous - .1, Close: closeValue,
			PrevClose: previous, PctChange: pctReturn(closeValue, previous), Adjusted: DailyBarAdjustedQFQ,
			Source: "test", Quality: "ok",
		})
		date = date.AddDate(0, 0, 1)
	}
	return out
}

func decisionTestQuote(symbol string, price float64) *StockV2QuoteLatest {
	now := time.Now()
	return &StockV2QuoteLatest{Symbol: symbol, Market: inferAStockMarket(symbol), LastPrice: price,
		OpenPrice: price, PrevClose: price, QuoteAt: now, FetchedAt: now, Source: "test", Status: QuoteStatusFresh}
}

func decisionTestTradeCalendar(centerDate string) []decisionTradeDay {
	center, _ := time.Parse("2006-01-02", centerDate)
	items := make([]decisionTradeDay, 0, 31)
	for offset := -15; offset <= 15; offset++ {
		date := center.AddDate(0, 0, offset)
		items = append(items, decisionTradeDay{Date: date.Format("2006-01-02"), Open: date.Weekday() != time.Saturday && date.Weekday() != time.Sunday})
	}
	return items
}

func gateStatus(items []DecisionGateResult, key string) string {
	for _, item := range items {
		if item.Key == key {
			return item.Status
		}
	}
	return fmt.Sprintf("missing:%s", key)
}
