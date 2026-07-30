package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestPortfolioSentinelResultPublicationRollsBackEveryDerivedObject(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createAtomicPublishSentinelRun(t, svc.store, "sentinel-atomic-rollback", now)

	monitorRun := MonitorRun{
		ID:          "sentinel-atomic-monitor-run",
		TaskType:    AgentTaskTypePortfolioSentinel,
		Status:      MonitorRunStatusCompleted,
		TriggerType: PortfolioSentinelTriggerManual,
		StartedAt:   now,
		FinishedAt:  now,
		HitCount:    2,
		ReviewCount: 2,
		Metadata:    map[string]any{"portfolioSentinelRunId": run.ID},
		CreatedAt:   now,
	}
	firstHit := MonitorHit{
		ID:        "sentinel-atomic-hit-1",
		RunID:     monitorRun.ID,
		TaskType:  AgentTaskTypePortfolioSentinel,
		Status:    MonitorHitStatusCandidate,
		Symbol:    "000001",
		Title:     "原子发布测试",
		Evidence:  map[string]any{"source": AgentTaskTypePortfolioSentinel},
		CreatedAt: now,
	}
	secondHit := firstHit
	secondHit.ID = "sentinel-atomic-hit-2"
	review := OperationReview{
		ID:            "sentinel-atomic-duplicate-review",
		HitID:         firstHit.ID,
		RunID:         monitorRun.ID,
		Status:        OperationReviewStatusCompleted,
		OutputType:    OperationReviewOutputTradeSignal,
		Symbol:        firstHit.Symbol,
		Result:        map[string]any{"reason": "test"},
		ResultSummary: "原子发布测试",
		CreatedAt:     now,
		UpdatedAt:     now,
		CompletedAt:   now,
	}
	secondReview := review
	secondReview.HitID = secondHit.ID

	_, err := svc.store.publishPortfolioSentinelResult(ctx, portfolioSentinelPublication{
		run: run,
		result: PortfolioSentinelResult{
			RunID:         run.ID,
			SchemaVersion: PortfolioSentinelReportSchemaVersion,
			Summary:       "should roll back",
			RiskLevel:     PortfolioSentinelRiskHigh,
			RawResult:     map[string]any{"test": true},
		},
		monitorRun: monitorRun,
		items: []portfolioSentinelPublicationItem{
			{hit: firstHit, review: review, alertConfig: MonitorTaskConfig{CooldownSeconds: 3600}},
			{hit: secondHit, review: secondReview, alertConfig: MonitorTaskConfig{CooldownSeconds: 3600}},
		},
	})
	if err == nil {
		t.Fatal("publish error = nil, want duplicate review failure")
	}

	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_monitor_runs WHERE id=?`, monitorRun.ID, 0)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_monitor_hits WHERE run_id=?`, monitorRun.ID, 0)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_operation_reviews WHERE run_id=?`, monitorRun.ID, 0)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_alerts WHERE monitor_run_id=?`, monitorRun.ID, 0)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_portfolio_sentinel_results WHERE run_id=?`, run.ID, 0)
	reloaded, getErr := svc.store.GetPortfolioSentinelRun(ctx, run.ID)
	if getErr != nil {
		t.Fatalf("get sentinel run: %v", getErr)
	}
	if reloaded.Status != PortfolioSentinelStatusRunning {
		t.Fatalf("sentinel status = %q, want running after rollback", reloaded.Status)
	}
}

func TestPortfolioSentinelRepeatedResultSubmissionIsIdempotent(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createAtomicPublishSentinelRun(t, svc.store, "sentinel-idempotent-publish", now)
	submitted := AgentTaskSubmittedResult{
		OutputType: PortfolioSentinelOutputType,
		Result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskHigh,
			"run_summary":        "同一结果只能发布一次",
			"portfolio_actions": []any{map[string]any{
				"symbol":         "000001",
				"market":         "SZ",
				"output_type":    OperationReviewOutputTradeSignal,
				"result_summary": "触发一次提醒",
				"reason":         "幂等测试",
			}},
		},
		Confidence: 0.8,
	}
	first, err := svc.ProcessPortfolioSentinelSubmittedResult(ctx, run.ID, submitted)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	second, err := svc.ProcessPortfolioSentinelSubmittedResult(ctx, run.ID, submitted)
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("result ids = %q, %q; want same result", first.ID, second.ID)
	}
	if len(first.DerivedMonitorHitIDs) != 1 || len(first.DerivedReviewIDs) != 1 || len(first.DerivedAlertIDs) != 1 {
		t.Fatalf("first derived ids = hits %v reviews %v alerts %v", first.DerivedMonitorHitIDs, first.DerivedReviewIDs, first.DerivedAlertIDs)
	}

	hit, err := svc.store.GetMonitorHit(ctx, first.DerivedMonitorHitIDs[0])
	if err != nil {
		t.Fatalf("get derived hit: %v", err)
	}
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_monitor_runs WHERE id=?`, hit.RunID, 1)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_monitor_hits WHERE run_id=?`, hit.RunID, 1)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_operation_reviews WHERE run_id=?`, hit.RunID, 1)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_alerts WHERE monitor_run_id=?`, hit.RunID, 1)
	assertAtomicPublishRowCount(t, svc.store, `SELECT COUNT(*) FROM stockv2_portfolio_sentinel_results WHERE run_id=?`, run.ID, 1)
}

func TestPortfolioSentinelV2PublishesCurrentConditionalPlanStrategy(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "sentinel-v2-plan")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "sentinel-v2-holding",
		PortfolioID:       portfolio.ID,
		Symbol:            "000001",
		Market:            "SZ",
		Name:              "平安银行",
		Quantity:          1000,
		AvailableQuantity: 800,
		MarketValue:       10000,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	run := createAtomicPublishSentinelRun(t, svc.store, "sentinel-v2-plan-run", now)
	run.PortfolioID = portfolio.ID
	if _, err := svc.store.UpdatePortfolioSentinelRun(ctx, run); err != nil {
		t.Fatalf("bind sentinel run portfolio: %v", err)
	}
	submitted := AgentTaskSubmittedResult{
		OutputType: PortfolioSentinelOutputType,
		Result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskHigh,
			"run_summary":        "跌破风险位时降低仓位",
			"action_plans": []any{map[string]any{
				"id":             "plan-reduce-000001",
				"portfolio_id":   portfolio.ID,
				"symbol":         "000001",
				"market":         "SZ",
				"action":         PortfolioSentinelPlanReduce,
				"trigger_mode":   PortfolioSentinelTriggerConditional,
				"trigger_policy": WatchTriggerPolicyAll,
				"conditions": []any{map[string]any{
					"key": "risk-price", "type": WatchRulePriceBelow, "threshold": 9.5,
				}},
				"sizing":        map[string]any{"mode": PortfolioSentinelSizingAvailableQuantityPct, "value": 50},
				"reason":        "价格跌破已验证风险位",
				"research_refs": []any{"research-1"},
			}},
			"research_audit": []any{map[string]any{
				"id": "research-1", "kind": "web_search", "source": "https://example.com/public?utm_source=test#fragment",
				"claim": "公开信息已复核",
			}},
		},
	}
	result, err := svc.ProcessPortfolioSentinelSubmittedResult(ctx, run.ID, submitted, AgentCLIResearchAudit{
		LiveSearchEnabled: true,
		WebSearchCount:    1,
	})
	if err != nil {
		t.Fatalf("publish v2 plan: %v", err)
	}
	if result.SchemaVersion != PortfolioSentinelReportSchemaVersion {
		t.Fatalf("schema = %q", result.SchemaVersion)
	}
	researchAudit := arrayFromAny(result.RawResult["research_audit"])
	if len(researchAudit) != 1 || firstRuleString(mapFromAny(researchAudit[0]), "source") != "https://example.com/public" {
		t.Fatalf("research audit source was not query-stripped: %+v", researchAudit)
	}
	plans, err := svc.ListPortfolioSentinelActionPlans(ctx, PortfolioSentinelActionPlanListFilter{PortfolioID: portfolio.ID})
	if err != nil {
		t.Fatalf("list current plans: %v", err)
	}
	if len(plans) != 1 || plans[0].Plan.ValidUntil.IsZero() || plans[0].Plan.Sizing == nil {
		t.Fatalf("plans = %+v, want one bounded current plan", plans)
	}
	strategies, err := svc.store.ListStrategies(ctx, StrategyListFilter{
		Kind: StrategyKindPortfolioMonitor, Source: StrategySourceAgent, PortfolioID: portfolio.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list plan strategies: %v", err)
	}
	if len(strategies) != 1 || strategies[0].ActiveVersion == nil ||
		stringFromAny(strategies[0].ActiveVersion.GenerationMeta["template"]) != "portfolio_sentinel_action_plan_v2" {
		t.Fatalf("strategies = %+v, want active sentinel plan strategy", strategies)
	}
	monitorConfig, err := svc.store.GetMonitorTaskConfig(ctx, MonitorTaskDataStrategyMonitor)
	if err != nil || !monitorConfig.Enabled {
		t.Fatalf("data strategy monitor config = %+v, err=%v; want enabled", monitorConfig, err)
	}
	seedWatchQuote(t, svc, "000001", 9.0, -2.0, QuoteStatusFresh, now)
	firstMonitorRun, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil || firstMonitorRun.HitCount != 1 || firstMonitorRun.ReviewCount != 1 {
		t.Fatalf("first plan monitor run = %+v, err=%v; want one hit and review", firstMonitorRun, err)
	}
	reviews, err := svc.store.ListOperationReviews(ctx, OperationReviewListFilter{
		StrategyID: strategies[0].Strategy.ID,
		OutputType: OperationReviewOutputProposedOperation,
		Limit:      10,
	})
	if err != nil || len(reviews) != 1 {
		allReviews, _ := svc.store.ListOperationReviews(ctx, OperationReviewListFilter{Limit: 10})
		t.Fatalf("triggered reviews = %+v, all=%+v, monitor=%+v, err=%v", reviews, allReviews, firstMonitorRun, err)
	}
	operation := mapFromAny(reviews[0].Result["proposedOperation"])
	if quantity := firstRuleNumber(operation, "quantity"); quantity != 400 {
		t.Fatalf("triggered quantity = %.2f, want 50%% of current available 800", quantity)
	}
	secondMonitorRun, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil || secondMonitorRun.HitCount != 0 {
		t.Fatalf("second plan monitor run = %+v, err=%v; plan must trigger once", secondMonitorRun, err)
	}
	plans, err = svc.ListPortfolioSentinelActionPlans(ctx, PortfolioSentinelActionPlanListFilter{PortfolioID: portfolio.ID})
	if err != nil || len(plans) != 1 || plans[0].Status != "triggered" {
		t.Fatalf("triggered plan status = %+v, err=%v", plans, err)
	}
}

func TestPortfolioSentinelPublishedResultMigrationCompletesLegacyRun(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createAtomicPublishSentinelRun(t, svc.store, "sentinel-published-run-migration", now)
	if _, err := svc.store.CreatePortfolioSentinelResult(ctx, PortfolioSentinelResult{
		RunID: run.ID, RiskLevel: PortfolioSentinelRiskHigh,
		DerivedAlertIDs: []string{"alert-1"}, DerivedMonitorHitIDs: []string{"hit-1", "hit-2"},
		DerivedReviewIDs: []string{"review-1"}, CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed legacy published result: %v", err)
	}
	if err := svc.store.migratePortfolioSentinelPublishedRunState(ctx); err != nil {
		t.Fatalf("migrate published run: %v", err)
	}
	reloaded, err := svc.store.GetPortfolioSentinelRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get migrated run: %v", err)
	}
	if reloaded.Status != PortfolioSentinelStatusCompleted || reloaded.ResultRiskLevel != PortfolioSentinelRiskHigh ||
		reloaded.GeneratedAlertCount != 1 || reloaded.GeneratedHitCount != 2 || reloaded.GeneratedReviewCount != 1 ||
		reloaded.FinishedAt.IsZero() {
		t.Fatalf("migrated run=%+v", reloaded)
	}
}

func createAtomicPublishSentinelRun(t *testing.T, store *Store, id string, now time.Time) PortfolioSentinelRun {
	t.Helper()
	run, err := store.CreatePortfolioSentinelRun(context.Background(), PortfolioSentinelRun{
		ID:            id,
		Status:        PortfolioSentinelStatusRunning,
		TriggerType:   PortfolioSentinelTriggerManual,
		WindowType:    PortfolioSentinelWindowManual,
		WindowStartAt: now.Add(-time.Hour),
		WindowEndAt:   now,
		StartedAt:     now,
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatalf("create sentinel run: %v", err)
	}
	return run
}

func assertAtomicPublishRowCount(t *testing.T, store *Store, query string, arg any, want int) {
	t.Helper()
	var got int
	if err := store.db.QueryRowContext(context.Background(), query, arg).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d for %s", got, want, query)
	}
}
