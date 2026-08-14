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
		QuoteAvailable: true, BenchmarkAvailable: true, ReferenceHealth: decisionReferenceHealth{
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
		InstrumentType: InstrumentTypeStock, TradeDate: tradeDate, Bars: bars, QuoteAvailable: true,
		BenchmarkAvailable: true, ReferenceHealth: decisionReferenceHealth{
			EventOK: true, FinanceOK: true, Status: DecisionHealthHealthy, CheckedAt: time.Now(),
		},
		TradeCalendar: decisionTestTradeCalendar(tradeDate),
	})
	if gateStatus(snapshot.Gates, DecisionGateEventCalendar) != DecisionGateStatusBlocked {
		t.Fatalf("event gate=%+v, want blocked", snapshot.Gates)
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
	ensureDecisionGateAuditFields(&draft, snapshot)
	for _, want := range []string{"price", "factor", "event_calendar", "catalyst", "flow", "fundamental"} {
		if !containsString(draft.DecisionBasis, want) {
			t.Fatalf("decision basis=%v, missing %q", draft.DecisionBasis, want)
		}
	}
	if !containsString(draft.EvidenceRefIDs, snapshot.ID) {
		t.Fatalf("evidence refs=%v, missing snapshot %q", draft.EvidenceRefIDs, snapshot.ID)
	}
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
