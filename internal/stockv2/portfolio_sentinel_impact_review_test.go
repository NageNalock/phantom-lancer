package stockv2

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

func TestPortfolioSentinelFinalImpactReviewAcceptsCompleteFrozenScope(t *testing.T) {
	svc, cleanup, run, expected := seedPortfolioSentinelImpactReviewScope(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	contextRun, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		ID:           "news-context-final-impact-complete",
		WindowType:   NewsContextWindowDaily,
		Status:       NewsContextRunStatusWaitingReview,
		ReviewStatus: NewsContextReviewPending,
		WindowStart:  now.Add(-24 * time.Hour),
		WindowEnd:    now,
	})
	if err != nil {
		t.Fatalf("create news context run: %v", err)
	}
	if _, err := svc.store.BeginNewsContextReview(ctx, contextRun.ID, run.ID); err != nil {
		t.Fatalf("link final impact review: %v", err)
	}

	result, err := svc.ProcessPortfolioSentinelSubmittedResult(ctx, run.ID, AgentTaskSubmittedResult{
		OutputType: PortfolioSentinelOutputType,
		Result:     completePortfolioSentinelImpactReviewResult(expected),
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("process complete final impact review: %v", err)
	}
	if result.RunID != run.ID {
		t.Fatalf("result run = %q, want %q", result.RunID, run.ID)
	}
	reloaded, err := svc.store.GetPortfolioSentinelRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload sentinel run: %v", err)
	}
	if reloaded.Status != PortfolioSentinelStatusCompleted {
		t.Fatalf("sentinel status = %q, want completed", reloaded.Status)
	}
}

func TestPortfolioSentinelFinalImpactReviewRejectsAnyMissingDomain(t *testing.T) {
	svc, cleanup, run, expected := seedPortfolioSentinelImpactReviewScope(t)
	defer cleanup()
	ctx := context.Background()

	for _, field := range []string{"holding_ids", "monitor_ids", "alert_ids", "opportunity_ids", "strategy_ids"} {
		t.Run(field, func(t *testing.T) {
			fields := clonePortfolioSentinelImpactReviewIDs(expected)
			fields[field] = []string{}
			coverage := parsePortfolioSentinelImpactReviewCoverage(t, fields)
			err := svc.validatePortfolioSentinelImpactReviewCoverage(ctx, run.ID, coverage)
			if !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
				t.Fatalf("missing %s error = %v, want invalid sentinel result", field, err)
			}
		})
	}

	fields := clonePortfolioSentinelImpactReviewIDs(expected)
	delete(fields, "strategy_ids")
	coverage := parsePortfolioSentinelImpactReviewCoverage(t, fields)
	if err := svc.validatePortfolioSentinelImpactReviewCoverage(ctx, run.ID, coverage); !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
		t.Fatalf("omitted domain error = %v, want invalid sentinel result", err)
	}
}

func TestPortfolioSentinelFinalImpactReviewAcceptsExplicitEmptyDomains(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	if _, err := svc.store.db.ExecContext(context.Background(), `DELETE FROM stockv2_monitor_task_configs`); err != nil {
		t.Fatalf("clear monitor configs for empty-scope test: %v", err)
	}
	run := createPortfolioSentinelImpactReviewRun(t, svc.store, "sentinel-empty-impact-scope")
	if summary, err := svc.store.FreezePortfolioSentinelImpactReviewScope(context.Background(), run.ID); err != nil {
		t.Fatalf("freeze empty scope: %v", err)
	} else if summary != (PortfolioSentinelImpactReviewScopeSummary{}) {
		t.Fatalf("empty scope summary = %+v", summary)
	}
	coverage := parsePortfolioSentinelImpactReviewCoverage(t, map[string][]string{
		"holding_ids":     {},
		"monitor_ids":     {},
		"alert_ids":       {},
		"opportunity_ids": {},
		"strategy_ids":    {},
	})
	if err := svc.validatePortfolioSentinelImpactReviewCoverage(context.Background(), run.ID, coverage); err != nil {
		t.Fatalf("validate explicit empty scope: %v", err)
	}
}

func TestPortfolioSentinelFinalImpactReviewRejectsInventedIdentifier(t *testing.T) {
	svc, cleanup, run, expected := seedPortfolioSentinelImpactReviewScope(t)
	defer cleanup()
	fields := clonePortfolioSentinelImpactReviewIDs(expected)
	fields["holding_ids"] = []string{"invented-holding-id"}
	coverage := parsePortfolioSentinelImpactReviewCoverage(t, fields)
	if err := svc.validatePortfolioSentinelImpactReviewCoverage(context.Background(), run.ID, coverage); !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
		t.Fatalf("invented identifier error = %v, want invalid sentinel result", err)
	}
}

func TestPortfolioSentinelMissingImpactCoverageKeepsNewsContextRetryable(t *testing.T) {
	svc, cleanup, run, expected := seedPortfolioSentinelImpactReviewScope(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	contextRun, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		ID:           "news-context-final-impact-missing",
		WindowType:   NewsContextWindowDaily,
		Status:       NewsContextRunStatusWaitingReview,
		ReviewStatus: NewsContextReviewPending,
		WindowStart:  now.Add(-48 * time.Hour),
		WindowEnd:    now.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create news context run: %v", err)
	}
	if _, err := svc.store.BeginNewsContextReview(ctx, contextRun.ID, run.ID); err != nil {
		t.Fatalf("link final impact review: %v", err)
	}
	fields := clonePortfolioSentinelImpactReviewIDs(expected)
	delete(fields, "alert_ids")
	_, err = svc.ProcessPortfolioSentinelSubmittedResult(ctx, run.ID, AgentTaskSubmittedResult{
		OutputType: PortfolioSentinelOutputType,
		Result:     completePortfolioSentinelImpactReviewResult(fields),
		Confidence: 0.8,
	})
	if !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
		t.Fatalf("missing impact coverage error = %v, want invalid sentinel result", err)
	}
	svc.reconcileNewsContextReviews(ctx)
	reloaded, err := svc.store.GetNewsContextRun(ctx, contextRun.ID)
	if err != nil {
		t.Fatalf("reload news context run: %v", err)
	}
	if reloaded.Status != NewsContextRunStatusWaitingReview || reloaded.ReviewStatus != NewsContextReviewFailed {
		t.Fatalf("news context after failed review = status %q review %q, want waiting_review/failed", reloaded.Status, reloaded.ReviewStatus)
	}
}

func TestPortfolioSentinelOrdinaryRunDoesNotRequireFinalImpactCoverage(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	run := createPortfolioSentinelImpactReviewRun(t, svc.store, "ordinary-sentinel-run")
	result, err := svc.ProcessPortfolioSentinelSubmittedResult(context.Background(), run.ID, AgentTaskSubmittedResult{
		OutputType: PortfolioSentinelOutputType,
		Result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "ordinary sentinel remains valid",
		},
		Confidence: 0.7,
	})
	if err != nil {
		t.Fatalf("process ordinary sentinel: %v", err)
	}
	if result.RunID != run.ID {
		t.Fatalf("result run = %q, want %q", result.RunID, run.ID)
	}
}

func TestPortfolioSentinelImpactReviewScopeToolPagesFrozenObjects(t *testing.T) {
	svc, cleanup, run, expected := seedPortfolioSentinelImpactReviewScope(t)
	defer cleanup()
	if !stringSliceContains(stockAgentMCPRequiredTools(), mcpToolListPortfolioSentinelImpactReviewScope) {
		t.Fatalf("required MCP tools do not include %s", mcpToolListPortfolioSentinelImpactReviewScope)
	}
	schema := stockAgentMCPToolInputSchema(mcpToolListPortfolioSentinelImpactReviewScope)
	required, _ := schema["required"].([]string)
	if len(required) != 2 || required[0] != "runId" || required[1] != "objectType" {
		t.Fatalf("impact review scope schema required = %#v", schema["required"])
	}
	items, total, err := svc.portfolioSentinelImpactReviewScopePage(context.Background(), run.ID, portfolioSentinelImpactObjectMonitors, 1, 0)
	if err != nil {
		t.Fatalf("page frozen monitors: %v", err)
	}
	if total != len(expected["monitor_ids"]) || len(items) != 1 || items[0]["id"] != expected["monitor_ids"][0] || items[0]["available"] != true {
		t.Fatalf("frozen monitor page = total %d, items %#v", total, items)
	}
}

func seedPortfolioSentinelImpactReviewScope(t *testing.T) (*Service, func(), PortfolioSentinelRun, map[string][]string) {
	t.Helper()
	svc, cleanup := newStrategyTestService(t)
	ctx := context.Background()
	portfolio := createStrategyTestPortfolio(t, svc.store, "impact-review-portfolio")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID: "impact-holding", PortfolioID: portfolio.ID, Symbol: "000001", Market: "SZ", Name: "测试持仓", Quantity: 100,
	}); err != nil {
		cleanup()
		t.Fatalf("create impact holding: %v", err)
	}
	watch, err := svc.store.CreateWatch(ctx, StockV2Watch{
		ID: "impact-monitor", Name: "测试监控", Status: WatchStatusActive, Source: WatchSourceManual,
		Symbol: "000001", Market: "SZ", PortfolioID: portfolio.ID, TriggerPolicy: WatchTriggerPolicyAny,
		TriggerConfig: map[string]any{}, ScheduleKind: WatchScheduleManual,
	})
	if err != nil {
		cleanup()
		t.Fatalf("create impact monitor: %v", err)
	}
	if _, err := svc.store.CreateAlert(ctx, StockV2Alert{
		ID: "impact-alert", WatchID: watch.ID, PortfolioID: portfolio.ID, Symbol: "000001", Market: "SZ",
		Status: AlertStatusOpen, Level: AlertLevelWarning, Title: "测试提醒",
	}); err != nil {
		cleanup()
		t.Fatalf("create impact alert: %v", err)
	}
	if _, err := svc.store.CreateOpportunity(ctx, Opportunity{
		ID: "impact-opportunity", Title: "测试机会", UserThesis: "测试逻辑", MarketScope: OpportunityMarketScopeAShare,
		InstrumentScope: OpportunityInstrumentScopeStock, Status: OpportunityStatusResearching,
	}); err != nil {
		cleanup()
		t.Fatalf("create impact opportunity: %v", err)
	}
	if _, err := svc.store.CreateStrategyWithVersion(ctx, StockV2Strategy{
		ID: "impact-strategy", Name: "测试策略", Kind: StrategyKindSymbolStrategy, Scope: StrategyScopeResearch,
		Source: StrategySourceManual, Status: StrategyStatusActive, Symbol: "000001", Market: "SZ",
	}, StockV2StrategyVersion{Title: "测试策略版本", Direction: StrategyDirectionWatch, Thesis: "测试逻辑"}); err != nil {
		cleanup()
		t.Fatalf("create impact strategy: %v", err)
	}
	monitorConfigs, err := svc.store.ListMonitorTaskConfigs(ctx)
	if err != nil {
		cleanup()
		t.Fatalf("list impact monitor configs: %v", err)
	}
	monitorIDs := make([]string, 0, len(monitorConfigs)+1)
	for taskType := range monitorConfigs {
		monitorIDs = append(monitorIDs, "task:"+taskType)
	}
	monitorIDs = append(monitorIDs, "watch:"+watch.ID)
	sort.Strings(monitorIDs)

	run := createPortfolioSentinelImpactReviewRun(t, svc.store, "sentinel-final-impact-run")
	summary, err := svc.store.FreezePortfolioSentinelImpactReviewScope(ctx, run.ID)
	if err != nil {
		cleanup()
		t.Fatalf("freeze impact review scope: %v", err)
	}
	wantSummary := PortfolioSentinelImpactReviewScopeSummary{
		HoldingCount: 1, MonitorCount: len(monitorIDs), AlertCount: 1, OpportunityCount: 1, StrategyCount: 1,
	}
	if summary != wantSummary {
		cleanup()
		t.Fatalf("impact scope summary = %+v, want %+v", summary, wantSummary)
	}
	return svc, cleanup, run, map[string][]string{
		"holding_ids":     {"impact-holding"},
		"monitor_ids":     monitorIDs,
		"alert_ids":       {"impact-alert"},
		"opportunity_ids": {"impact-opportunity"},
		"strategy_ids":    {"impact-strategy"},
	}
}

func createPortfolioSentinelImpactReviewRun(t *testing.T, store *Store, id string) PortfolioSentinelRun {
	t.Helper()
	now := time.Now()
	run, err := store.CreatePortfolioSentinelRun(context.Background(), PortfolioSentinelRun{
		ID: id, Status: PortfolioSentinelStatusRunning, TriggerType: PortfolioSentinelTriggerManual,
		WindowType: PortfolioSentinelWindowManual, WindowStartAt: now.Add(-time.Hour), WindowEndAt: now, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("create portfolio sentinel run: %v", err)
	}
	return run
}

func completePortfolioSentinelImpactReviewResult(fields map[string][]string) map[string]any {
	return map[string]any{
		"schema_version":                  PortfolioSentinelReportSchemaVersion,
		"overall_risk_level":              PortfolioSentinelRiskLow,
		"run_summary":                     "all frozen impact objects reviewed",
		"checked_news_thread_version_ids": []string{},
		"impact_review_coverage":          clonePortfolioSentinelImpactReviewIDs(fields),
	}
}

func parsePortfolioSentinelImpactReviewCoverage(t *testing.T, fields map[string][]string) *PortfolioSentinelImpactReviewCoverage {
	t.Helper()
	report, err := portfolioSentinelReportFromResult(completePortfolioSentinelImpactReviewResult(fields))
	if err != nil {
		t.Fatalf("parse impact review coverage: %v", err)
	}
	return report.ImpactReviewCoverage
}

func clonePortfolioSentinelImpactReviewIDs(fields map[string][]string) map[string][]string {
	out := make(map[string][]string, len(fields))
	for field, ids := range fields {
		out[field] = append([]string{}, ids...)
	}
	return out
}
