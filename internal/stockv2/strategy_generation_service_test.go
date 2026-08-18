package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStrategyGenerationFinalizeCreatesDraftStrategy(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	seedStrategyGenerationInstrument(t, svc, ctx, "302132")
	seedWatchQuote(t, svc, "302132", 68.5, 2.1, QuoteStatusFresh, time.Now())
	seedWatchDailyBar(t, svc, "302132", 66.8)
	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:          "302132",
		Market:          "SZ",
		InstrumentType:  InstrumentTypeStock,
		Name:            "中航成飞",
		Industry:        "军工",
		Concepts:        []string{"航空装备"},
		BusinessSummary: "航空装备制造",
		ProfileText:     "中航成飞是航空装备制造相关标的。",
	}); err != nil {
		t.Fatalf("upsert stock profile: %v", err)
	}

	portfolio := createStrategyTestPortfolio(t, svc.store, "pf-strategy-generation")
	model := seedStrategyGenerationModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStrategyGeneration, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind strategy generation model: %v", err)
	}

	run, err := svc.RunStrategyGeneration(ctx, StrategyGenerationInput{
		Mode:        StrategyGenerationModeManualTarget,
		UserGoal:    "生成中航成飞观察策略",
		PortfolioID: portfolio.ID,
		TargetInstruments: []StrategyGenerationTargetInstrument{{
			Symbol: "302132",
		}},
	})
	if err != nil {
		t.Fatalf("run strategy generation: %v", err)
	}
	if run.TaskType != AgentTaskTypeStrategyGeneration || run.Status != AgentRunStatusReady {
		t.Fatalf("run = %+v, want ready strategy_generation run", run)
	}

	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	if _, err := svc.agentTaskPool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStrategyGeneration,
		ResultSummary: "生成一条观察策略草案",
		Result:        strategyGenerationReportResult("302132"),
		Confidence:    0.72,
	}); err != nil {
		t.Fatalf("submit strategy generation result: %v", err)
	}
	svc.finalizeAgentRunWithOutput(ctx, run.ID, run.DecisionLedgerID, taskID, &AgentExecutorOutput{
		StdoutTail: "strategy generation ok",
		ExitCode:   0,
		Duration:   time.Millisecond,
	}, nil)

	finalRun, err := svc.GetAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get final run: %v", err)
	}
	if finalRun.Status != AgentRunStatusCompleted {
		t.Fatalf("final status = %q, want completed; error=%s", finalRun.Status, finalRun.ErrorMessage)
	}
	strategies, err := svc.ListStrategies(ctx, StrategyListFilter{
		Source: StrategySourceAgent,
		Status: StrategyStatusDraft,
		Symbol: "302132",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("list strategies: %v", err)
	}
	if len(strategies) != 1 {
		t.Fatalf("draft strategy count = %d, want 1", len(strategies))
	}
	created := strategies[0]
	if created.Strategy.Scope != StrategyScopePortfolioBound || created.Strategy.PortfolioID != portfolio.ID {
		t.Fatalf("strategy scope/portfolio = %s/%s, want portfolio_bound/%s", created.Strategy.Scope, created.Strategy.PortfolioID, portfolio.ID)
	}
	if created.Strategy.Source != StrategySourceAgent || created.Strategy.Status != StrategyStatusDraft {
		t.Fatalf("strategy source/status = %s/%s", created.Strategy.Source, created.Strategy.Status)
	}
	if created.ActiveVersion == nil {
		t.Fatal("active version nil")
	}
	meta := created.ActiveVersion.GenerationMeta
	if meta["source"] != AgentTaskTypeStrategyGeneration || meta["agentRunId"] != run.ID {
		t.Fatalf("generationMeta = %#v, want strategy_generation source and run id", meta)
	}
	playbook := mapFromAny(meta["playbook"])
	rules := sliceFromAny(playbook["rules"])
	if len(rules) != 1 {
		t.Fatalf("playbook.rules = %#v, want one rule", playbook["rules"])
	}
	rule := mapFromAny(rules[0])
	if rule["action"] != StrategyGenerationRuleActionObserve || rule["id"] != "observe_1" {
		t.Fatalf("rule = %#v", rule)
	}
	ledger, err := svc.GetAgentDecisionLedger(ctx, run.DecisionLedgerID)
	if err != nil {
		t.Fatalf("get ledger: %v", err)
	}
	if got := sliceFromAny(ledger.StructuredOutput["createdStrategies"]); len(got) != 1 {
		t.Fatalf("ledger createdStrategies = %#v, want one created strategy", ledger.StructuredOutput["createdStrategies"])
	}
	txs, err := svc.ListTransactions(ctx, portfolio.ID, 10)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("transactions = %#v, want none", txs)
	}
}

func TestStrategyGenerationRejectsLegacyPlaybookActions(t *testing.T) {
	report := strategyGenerationReportResult("302132")
	draft := sliceFromAny(report["drafts"])[0]
	playbook := mapFromAny(mapFromAny(draft)["playbook"])
	playbook["actions"] = []any{map[string]any{"action_type": "add"}}

	if _, err := strategyGenerationReportFromResult(report); err == nil {
		t.Fatal("legacy playbook.actions accepted, want error")
	}
}

func TestStrategyGenerationUsesRunConfidenceWhenDraftConfidenceIsMissing(t *testing.T) {
	raw := strategyGenerationReportResult("302132")
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	delete(draft, "confidence")

	if _, err := strategyGenerationReportFromResult(raw); !errors.Is(err, ErrInvalidStrategyGenerationResult) {
		t.Fatalf("missing draft confidence error = %v, want invalid strategy generation result", err)
	}
	report, err := strategyGenerationReportFromSubmittedResult(raw, StrategyGenerationModeManualTarget, 0.76)
	if err != nil {
		t.Fatalf("parse report with run confidence fallback: %v", err)
	}
	if report.Drafts[0].Confidence != 0.76 || report.Drafts[0].ConfidenceSource != StrategyGenerationConfidenceSourceRun {
		t.Fatalf("draft confidence = %v/%q, want 0.76/%q", report.Drafts[0].Confidence, report.Drafts[0].ConfidenceSource, StrategyGenerationConfidenceSourceRun)
	}
}

func TestStrategyGenerationRejectsExplicitZeroDraftConfidence(t *testing.T) {
	raw := strategyGenerationReportResult("302132")
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	draft["confidence"] = 0.0

	if _, err := strategyGenerationReportFromSubmittedResult(raw, StrategyGenerationModeManualTarget, 0.76); !errors.Is(err, ErrInvalidStrategyGenerationResult) {
		t.Fatalf("zero draft confidence error = %v, want invalid strategy generation result", err)
	}
}

func TestStrategyGenerationNormalizesStringPlaybookPrefilters(t *testing.T) {
	report := strategyGenerationReportResult("302132")
	draft := mapFromAny(sliceFromAny(report["drafts"])[0])
	playbook := mapFromAny(draft["playbook"])
	rule := mapFromAny(sliceFromAny(playbook["rules"])[0])
	rule["dataPrefilters"] = "quote freshness and price observation only"
	rule["portfolioPrefilters"] = map[string]any{"type": WatchRuleQuoteStale}

	parsed, err := strategyGenerationReportFromResult(report)
	if err != nil {
		t.Fatalf("strategyGenerationReportFromResult: %v", err)
	}
	got := parsed.Drafts[0].Playbook.Rules[0]
	if len(got.DataPrefilters) != 0 {
		t.Fatalf("dataPrefilters = %#v, want empty array after string normalization", got.DataPrefilters)
	}
	if len(got.PortfolioPrefilters) != 1 || got.PortfolioPrefilters[0]["type"] != WatchRuleQuoteStale {
		t.Fatalf("portfolioPrefilters = %#v, want single object wrapped in array", got.PortfolioPrefilters)
	}
}

func TestStrategyGenerationOpportunityAcceptsNoDraftsAndRestoresTaskMode(t *testing.T) {
	raw := map[string]any{
		"schema_version": StrategyGenerationReportSchemaVersion,
		"run_summary": map[string]any{
			"overall_conclusion": "候选证据不足，本轮不生成策略草案。",
			"key_conflicts":      []any{},
			"data_quality_notes": []any{},
		},
		"drafts": []any{},
	}
	report, err := strategyGenerationReportFromResultForMode(raw, StrategyGenerationModeOpportunity)
	if err != nil {
		t.Fatalf("parse valid no-draft opportunity report: %v", err)
	}
	if report.RunSummary.Mode != StrategyGenerationModeOpportunity || len(report.Drafts) != 0 {
		t.Fatalf("report=%+v, want opportunity mode with no drafts", report)
	}
}

func TestStrategyGenerationExpectedModeRejectsModelMismatch(t *testing.T) {
	raw := strategyGenerationReportResult("302132")
	if _, err := strategyGenerationReportFromResultForMode(raw, StrategyGenerationModeOpportunity); !errors.Is(err, ErrInvalidStrategyGenerationResult) {
		t.Fatalf("mode mismatch error=%v, want invalid strategy generation result", err)
	}
}

func TestStrategyGenerationReportNormalizesFormatterTargetAndActions(t *testing.T) {
	raw := strategyGenerationPortfolioReportResult([]string{"600276"})
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	delete(draft, "symbol")
	delete(draft, "market")
	delete(draft, "name")
	delete(draft, "strategy_bias")
	draft["target"] = map[string]any{
		"symbol": "600276",
		"market": "SH",
		"name":   "恒瑞医药",
	}
	draft["direction"] = "watch"
	playbook := mapFromAny(draft["playbook"])
	rule := mapFromAny(sliceFromAny(playbook["rules"])[0])
	delete(rule, "action")
	delete(rule, "title")
	rule["name"] = "观察触发"
	rule["actions"] = []any{"request_review", "observe"}

	report, err := strategyGenerationReportFromResult(raw)
	if err != nil {
		t.Fatalf("strategyGenerationReportFromResult: %v", err)
	}
	got := report.Drafts[0]
	if got.Symbol != "600276" || got.Market != "SH" || got.Name != "恒瑞医药" {
		t.Fatalf("draft target fields = %s/%s/%s, want normalized target fields", got.Symbol, got.Market, got.Name)
	}
	if got.StrategyBias != "watch" {
		t.Fatalf("strategy bias = %q, want watch", got.StrategyBias)
	}
	if got.Playbook.Rules[0].Action != StrategyGenerationRuleActionObserve {
		t.Fatalf("rule action = %q, want observe", got.Playbook.Rules[0].Action)
	}
	if got.Playbook.Rules[0].Title != "观察触发" {
		t.Fatalf("rule title = %q, want normalized name", got.Playbook.Rules[0].Title)
	}
}

func TestStrategyGenerationReportNormalizesObservedFormatterRuleAliases(t *testing.T) {
	raw := strategyGenerationReportResult("302132")
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	playbook := mapFromAny(draft["playbook"])
	rule := mapFromAny(sliceFromAny(playbook["rules"])[0])
	delete(rule, "id")
	delete(rule, "action")
	delete(rule, "trigger")
	delete(rule, "preconditions")
	delete(rule, "target")
	rule["rule_id"] = "observe_alias"
	rule["signal"] = StrategyGenerationRuleActionObserve
	rule["condition"] = "出现可核验的新证据"
	rule["on_false"] = "保持观察"
	rule["on_true"] = "提交人工 Review"

	report, err := strategyGenerationReportFromResult(raw)
	if err != nil {
		t.Fatalf("normalize observed formatter aliases: %v", err)
	}
	got := report.Drafts[0].Playbook.Rules[0]
	if got.ID != "observe_alias" || got.Action != StrategyGenerationRuleActionObserve || got.Trigger != "出现可核验的新证据" || got.Preconditions != "保持观察" || got.Target != "提交人工 Review" {
		t.Fatalf("normalized rule=%+v", got)
	}
}

func TestStrategyGenerationReportNormalizesObservedFormatterDraftAliases(t *testing.T) {
	raw := strategyGenerationReportResult("000831")
	mapFromAny(raw["run_summary"])["mode"] = StrategyGenerationModeOpportunity
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	delete(draft, "symbol")
	delete(draft, "market")
	delete(draft, "name")
	delete(draft, "draft_type")
	delete(draft, "thesis")
	draft["instrument"] = map[string]any{
		"symbol": "000831",
		"market": "SZ",
		"name":   "中国稀土",
	}
	draft["type"] = StrategyGenerationDraftTypeNewStrategy
	draft["rationale"] = "高纯稀土分离能力已验证，但仍需等待公司级盈利传导证据。"

	report, err := strategyGenerationReportFromResultForMode(raw, StrategyGenerationModeOpportunity)
	if err != nil {
		t.Fatalf("normalize observed formatter draft aliases: %v", err)
	}
	got := report.Drafts[0]
	if got.Symbol != "000831" || got.Market != "SZ" || got.Name != "中国稀土" || got.DraftType != StrategyGenerationDraftTypeNewStrategy || got.Thesis != draft["rationale"] {
		t.Fatalf("normalized draft=%+v", got)
	}
}

func TestRecoverInvalidStrategyGenerationRunReusesPersistedResult(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	seedStrategyGenerationInstrument(t, svc, ctx, "000831")

	raw := strategyGenerationReportResult("000831")
	mapFromAny(raw["run_summary"])["mode"] = StrategyGenerationModeOpportunity
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	delete(draft, "symbol")
	delete(draft, "market")
	delete(draft, "name")
	delete(draft, "draft_type")
	draft["instrument"] = map[string]any{"symbol": "000831", "market": "SZ", "name": "中国稀土"}
	draft["type"] = StrategyGenerationDraftTypeNewStrategy

	triggerID := strategyGenerationTriggerID(StrategyGenerationInput{
		Mode:              StrategyGenerationModeOpportunity,
		OpportunityID:     "opportunity-1",
		CandidateIDs:      []string{"candidate-1"},
		TargetInstruments: []StrategyGenerationTargetInstrument{{Symbol: "000831", Market: "SZ"}},
	})
	run, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType:          AgentTaskTypeStrategyGeneration,
		TriggerObjectType: "strategy_generation",
		TriggerObjectID:   triggerID,
		Status:            AgentRunStatusFailed,
		ErrorMessage:      `invalid strategy generation result: drafts[0].draft_type "" is invalid`,
	}, AgentDecisionLedger{
		TaskType:          AgentTaskTypeStrategyGeneration,
		TriggerObjectType: "strategy_generation",
		TriggerObjectID:   triggerID,
		StructuredOutput: map[string]any{
			"outputType":    AgentTaskTypeStrategyGeneration,
			"resultSummary": "生成一条机会策略草案",
			"result":        raw,
			"confidence":    0.62,
		},
	})
	if err != nil {
		t.Fatalf("create failed agent run: %v", err)
	}

	recovered, err := svc.recoverInvalidStrategyGenerationRun(ctx, run)
	if err != nil || !recovered {
		t.Fatalf("recover invalid strategy generation run: recovered=%t err=%v", recovered, err)
	}
	finalRun, err := svc.store.GetAgentRun(ctx, run.ID)
	if err != nil || finalRun.Status != AgentRunStatusCompleted || finalRun.ErrorMessage != "" {
		t.Fatalf("final run=%+v err=%v", finalRun, err)
	}
	strategies, err := svc.ListStrategies(ctx, StrategyListFilter{Source: StrategySourceAgent, Symbol: "000831", Limit: 10})
	if err != nil || len(strategies) != 1 {
		t.Fatalf("strategies=%+v err=%v, want one persisted draft", strategies, err)
	}
	ledger, err := svc.store.GetAgentDecisionLedger(ctx, run.DecisionLedgerID)
	if err != nil || len(sliceFromAny(ledger.StructuredOutput["createdStrategies"])) != 1 || ledger.StructuredOutput["strategyGenerationRecovered"] != true {
		t.Fatalf("ledger=%+v err=%v", ledger.StructuredOutput, err)
	}
	recovered, err = svc.recoverInvalidStrategyGenerationRun(ctx, finalRun)
	if err != nil || recovered {
		t.Fatalf("second recovery: recovered=%t err=%v, want no-op", recovered, err)
	}
}

func TestBuildPortfolioStrategyDiagnosisRequiresPortfolioID(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	_, err := svc.BuildStrategyGenerationContext(context.Background(), StrategyGenerationInput{
		Mode: StrategyGenerationModePortfolio,
	})
	if !errors.Is(err, ErrStrategyGenerationPortfolioRequired) {
		t.Fatalf("error = %v, want ErrStrategyGenerationPortfolioRequired", err)
	}
}

func TestBuildPortfolioStrategyDiagnosisContextMarksQuoteQuality(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	portfolio := createStrategyTestPortfolio(t, svc.store, "pf-context-quality")
	now := time.Now()
	for _, holding := range []StockV2Holding{
		{
			ID:                "h-fresh-context",
			PortfolioID:       portfolio.ID,
			Symbol:            "600000",
			Market:            "SH",
			Name:              "浦发银行",
			Quantity:          100,
			AvailableQuantity: 100,
			CostPrice:         10,
		},
		{
			ID:                "h-stale-context",
			PortfolioID:       portfolio.ID,
			Symbol:            "000001",
			Market:            "SZ",
			Name:              "平安银行",
			Quantity:          100,
			AvailableQuantity: 100,
			CostPrice:         8,
		},
		{
			ID:                "h-missing-context",
			PortfolioID:       portfolio.ID,
			Symbol:            "300001",
			Market:            "SZ",
			Name:              "特锐德",
			Quantity:          100,
			AvailableQuantity: 100,
			CostPrice:         15,
			LastPrice:         88,
			LastPriceAt:       now.Add(-24 * time.Hour),
		},
	} {
		if err := svc.store.CreateHolding(ctx, holding); err != nil {
			t.Fatalf("create holding %s: %v", holding.Symbol, err)
		}
	}
	seedWatchQuote(t, svc, "600000", 11, 1.2, QuoteStatusFresh, now)
	seedWatchQuote(t, svc, "000001", 9, -0.3, QuoteStatusStale, now.Add(-2*time.Hour))
	seedWatchDailyBar(t, svc, "600000", 10.8)

	genCtx, err := svc.BuildStrategyGenerationContext(ctx, StrategyGenerationInput{
		Mode:        StrategyGenerationModePortfolio,
		UserGoal:    "诊断当前组合",
		PortfolioID: portfolio.ID,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if genCtx.Diagnostics == nil || genCtx.Diagnostics.HoldingCount != 3 {
		t.Fatalf("diagnostics = %+v, want three holdings", genCtx.Diagnostics)
	}
	bySymbol := map[string]StrategyGenerationHoldingContext{}
	for _, holding := range genCtx.Holdings {
		bySymbol[holding.Symbol] = holding
	}
	fresh := bySymbol["600000"]
	if fresh.Quote == nil || fresh.CurrentPrice != 11 || fresh.DailyBars == nil || fresh.DailyBars.Count == 0 {
		t.Fatalf("fresh holding context = %+v, want quote price and daily bars", fresh)
	}
	stale := bySymbol["000001"]
	if stale.Quote == nil || stale.CurrentPrice != 0 {
		t.Fatalf("stale holding context = %+v, want quote present but no currentPrice", stale)
	}
	if quoteFreshness := mapFromAny(stale.Freshness["quote"]); quoteFreshness["status"] != QuoteStatusStale {
		t.Fatalf("stale quote freshness = %+v, want stale", quoteFreshness)
	}
	missing := bySymbol["300001"]
	if missing.Quote != nil || missing.CurrentPrice != 0 {
		t.Fatalf("missing quote context = %+v, want no quote and no fabricated currentPrice", missing)
	}
	for _, want := range []string{"quote:300001", "quoteStale:000001", "dailyBars:000001", "dailyBars:300001"} {
		if !containsString(genCtx.MissingItems, want) {
			t.Fatalf("missingItems = %v, want %s", genCtx.MissingItems, want)
		}
	}
}

func TestPortfolioStrategyDiagnosisEmptyHoldingsNoDrafts(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	portfolio := createStrategyTestPortfolio(t, svc.store, "pf-empty-diagnosis")
	configureStrategyGenerationModel(t, svc, ctx)

	run, err := svc.RunStrategyGeneration(ctx, StrategyGenerationInput{
		Mode:        StrategyGenerationModePortfolio,
		UserGoal:    "诊断当前组合",
		PortfolioID: portfolio.ID,
	})
	if err != nil {
		t.Fatalf("run strategy generation: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	if _, err := svc.agentTaskPool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStrategyGeneration,
		ResultSummary: "空组合诊断",
		Result: map[string]any{
			"schema_version": StrategyGenerationReportSchemaVersion,
			"run_summary": map[string]any{
				"mode":               StrategyGenerationModePortfolio,
				"overall_conclusion": "当前组合没有持仓。",
				"key_conflicts":      []any{},
				"data_quality_notes": []any{},
			},
			"drafts": []any{},
		},
		Confidence: 0.7,
	}); err != nil {
		t.Fatalf("submit result: %v", err)
	}
	svc.finalizeAgentRunWithOutput(ctx, run.ID, run.DecisionLedgerID, taskID, &AgentExecutorOutput{ExitCode: 0, Duration: time.Millisecond}, nil)

	finalRun, err := svc.GetAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if finalRun.Status != AgentRunStatusCompleted {
		t.Fatalf("status = %s, want completed; error=%s", finalRun.Status, finalRun.ErrorMessage)
	}
	strategies, err := svc.ListStrategies(ctx, StrategyListFilter{Source: StrategySourceAgent, Status: StrategyStatusDraft, Limit: 10})
	if err != nil {
		t.Fatalf("list strategies: %v", err)
	}
	if len(strategies) != 0 {
		t.Fatalf("draft strategies = %d, want 0", len(strategies))
	}
}

func TestStrategyGenerationReportAcceptsStructuredReviewRequests(t *testing.T) {
	raw := strategyGenerationPortfolioReportResult([]string{"600276"})
	draft := mapFromAny(sliceFromAny(raw["drafts"])[0])
	draft["portfolio_aware_suggestion"] = map[string]any{
		"trade_signal":         "hold",
		"target_position_hint": "",
		"review_request": []any{
			map[string]any{
				"priority": "high",
				"type":     "data_validation",
				"reason":   "Validate 600276 cost basis before trade-enabled use.",
			},
			map[string]any{
				"priority": "medium",
				"type":     "account_permission",
				"reason":   "Portfolio flags disable buy/add/reduce/sell.",
			},
		},
	}

	report, err := strategyGenerationReportFromResult(raw)
	if err != nil {
		t.Fatalf("parse strategy generation report: %v", err)
	}
	got := report.Drafts[0].PortfolioAwareSuggestion.ReviewRequest
	if !strings.Contains(got, "[high/data_validation] Validate 600276 cost basis") ||
		!strings.Contains(got, "[medium/account_permission] Portfolio flags disable") {
		t.Fatalf("review request = %q, want normalized structured requests", got)
	}
}

func TestBackfillStrategyGenerationConfidenceRepairsOnlyOmittedDraftValue(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	seedStrategyGenerationInstrument(t, svc, ctx, "600000")
	seedStrategyGenerationInstrument(t, svc, ctx, "600001")

	run, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType:          AgentTaskTypeStrategyGeneration,
		TriggerObjectType: "strategy_generation",
		TriggerObjectID:   StrategyGenerationModeManualTarget + ":symbols=600000,600001",
		Status:            AgentRunStatusCompleted,
	}, AgentDecisionLedger{
		TaskType:          AgentTaskTypeStrategyGeneration,
		TriggerObjectType: "strategy_generation",
		TriggerObjectID:   StrategyGenerationModeManualTarget + ":symbols=600000,600001",
		StructuredOutput: map[string]any{
			"confidence": 0.68,
			"result": map[string]any{
				"drafts": []any{
					map[string]any{"symbol": "600000"},
					map[string]any{"symbol": "600001", "confidence": 0.0},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create historical run: %v", err)
	}
	for _, symbol := range []string{"600000", "600001"} {
		if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
			Name:      "Agent策略草案 - " + symbol,
			Kind:      StrategyKindSymbolStrategy,
			Scope:     StrategyScopeResearch,
			Source:    StrategySourceAgent,
			Status:    StrategyStatusDraft,
			Symbol:    symbol,
			Market:    "SH",
			Direction: StrategyDirectionWatch,
			Thesis:    "历史置信度迁移测试",
			GenerationMeta: map[string]any{
				"source":     AgentTaskTypeStrategyGeneration,
				"agentRunId": run.ID,
				"strategyGeneration": map[string]any{
					"confidence": 0.0,
				},
			},
			CreatedBy: StrategySourceAgent,
		}); err != nil {
			t.Fatalf("create historical strategy %s: %v", symbol, err)
		}
	}

	if err := svc.store.backfillStrategyGenerationConfidence(ctx); err != nil {
		t.Fatalf("backfill confidence: %v", err)
	}
	if err := svc.store.backfillStrategyGenerationConfidence(ctx); err != nil {
		t.Fatalf("repeat backfill confidence: %v", err)
	}
	for _, tc := range []struct {
		symbol     string
		confidence float64
		source     string
	}{
		{symbol: "600000", confidence: 0.68, source: StrategyGenerationConfidenceSourceRun},
		{symbol: "600001", confidence: 0, source: ""},
	} {
		items, err := svc.ListStrategies(ctx, StrategyListFilter{Source: StrategySourceAgent, Symbol: tc.symbol, Limit: 10})
		if err != nil || len(items) != 1 || items[0].ActiveVersion == nil {
			t.Fatalf("list strategy %s: items=%+v err=%v", tc.symbol, items, err)
		}
		generation := mapFromAny(items[0].ActiveVersion.GenerationMeta["strategyGeneration"])
		confidence, _ := numberFromAny(generation["confidence"])
		if confidence != tc.confidence || stringFromAny(generation["confidenceSource"]) != tc.source {
			t.Fatalf("strategy %s confidence = %v/%q, want %v/%q", tc.symbol, confidence, stringFromAny(generation["confidenceSource"]), tc.confidence, tc.source)
		}
	}
}

// LLM 偶尔会把 key_conflicts / data_quality_notes 输出成对象数组或单字符串；
// RunSummary 必须容错为 []string，否则整个 report 反序列化失败会导致运行被判 failed。
func TestStrategyGenerationRunSummaryToleratesMixedConflictShapes(t *testing.T) {
	raw := `{"mode":"portfolio_strategy_diagnosis","overall_conclusion":"ok","key_conflicts":["quote stale vs daily bar",{"field":"price","conflict":"latest quote disagrees with daily close","resolution":"adopted daily close"}],"data_quality_notes":"single note"}`
	var summary StrategyGenerationRunSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("unmarshal run summary with mixed key_conflicts: %v", err)
	}
	if summary.Mode != "portfolio_strategy_diagnosis" {
		t.Fatalf("mode = %q", summary.Mode)
	}
	if len(summary.KeyConflicts) != 2 {
		t.Fatalf("key conflicts count = %d, want 2", len(summary.KeyConflicts))
	}
	if summary.KeyConflicts[0] != "quote stale vs daily bar" {
		t.Fatalf("key conflicts[0] = %q", summary.KeyConflicts[0])
	}
	if !strings.Contains(summary.KeyConflicts[1], "price") {
		t.Fatalf("key conflicts[1] = %q, want object flattened to text containing 'price'", summary.KeyConflicts[1])
	}
	if len(summary.DataQualityNotes) != 1 || summary.DataQualityNotes[0] != "single note" {
		t.Fatalf("data quality notes = %v", summary.DataQualityNotes)
	}
}

func TestStrategyGenerationDraftActivationFeedsDataMonitor(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	seedStrategyGenerationInstrument(t, svc, ctx, "300750")
	seedWatchQuote(t, svc, "300750", 210, 2.4, QuoteStatusFresh, time.Now())
	configureStrategyGenerationModel(t, svc, ctx)

	run, err := svc.RunStrategyGeneration(ctx, StrategyGenerationInput{
		Mode:     StrategyGenerationModeManualTarget,
		UserGoal: "生成宁德时代突破策略",
		TargetInstruments: []StrategyGenerationTargetInstrument{{
			Symbol: "300750",
			Market: "SZ",
			Name:   "宁德时代",
		}},
	})
	if err != nil {
		t.Fatalf("run strategy generation: %v", err)
	}
	// This test covers activation-to-monitor wiring rather than decision data.
	// Make its gate fixture explicitly healthy so the fail-closed production
	// boundary does not turn an unrelated integration test into no_change.
	gates, err := svc.store.ListDecisionGateSnapshots(ctx, "strategy_generation", run.TriggerObjectID)
	if err != nil || len(gates) != 1 {
		t.Fatalf("decision gates = %#v, err=%v", gates, err)
	}
	gate := gates[0]
	gate.Status = DecisionHealthHealthy
	gate.AllowedActions = []string{StrategyGenerationRuleActionAddPosition}
	for i := range gate.Gates {
		gate.Gates[i].Status = DecisionGateStatusPass
	}
	for i := range gate.DataHealth {
		gate.DataHealth[i].Status = DecisionHealthHealthy
	}
	if _, err := svc.store.SaveDecisionGateSnapshot(ctx, gate); err != nil {
		t.Fatalf("save healthy decision gate: %v", err)
	}
	report := strategyGenerationReportResult("300750")
	draft := mapFromAny(sliceFromAny(report["drafts"])[0])
	draft["name"] = "宁德时代"
	playbook := mapFromAny(draft["playbook"])
	rule := mapFromAny(sliceFromAny(playbook["rules"])[0])
	rule["action"] = StrategyGenerationRuleActionAddPosition
	rule["title"] = "突破后加仓观察"
	rule["dataPrefilters"] = []any{map[string]any{"key": "break_200", "type": WatchRulePriceAbove, "threshold": 200.0}}
	rule["portfolioPrefilters"] = []any{}

	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	if _, err := svc.agentTaskPool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStrategyGeneration,
		ResultSummary: "生成突破策略草案",
		Result:        report,
		Confidence:    0.74,
	}); err != nil {
		t.Fatalf("submit result: %v", err)
	}
	svc.finalizeAgentRunWithOutput(ctx, run.ID, run.DecisionLedgerID, taskID, &AgentExecutorOutput{ExitCode: 0, Duration: time.Millisecond}, nil)

	drafts, err := svc.ListStrategies(ctx, StrategyListFilter{Source: StrategySourceAgent, Status: StrategyStatusDraft, Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d, want 1", len(drafts))
	}
	meta := drafts[0].ActiveVersion.GenerationMeta
	if meta["source"] != AgentTaskTypeStrategyGeneration {
		t.Fatalf("generationMeta source = %v, want strategy_generation", meta["source"])
	}
	reviewsBefore, err := svc.ListOperationReviews(ctx, OperationReviewListFilter{Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list reviews before monitor: %v", err)
	}
	if len(reviewsBefore) != 0 {
		t.Fatalf("reviews before monitor = %#v, want none", reviewsBefore)
	}

	activated, err := svc.ActivateStrategy(ctx, drafts[0].Strategy.ID)
	if err != nil {
		t.Fatalf("activate draft: %v", err)
	}
	if activated.Strategy.Status != StrategyStatusActive {
		t.Fatalf("activated status = %s", activated.Strategy.Status)
	}
	monitorRun, err := svc.RunMonitorTask(ctx, MonitorTaskDataStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run data monitor: %v", err)
	}
	if monitorRun.HitCount != 1 {
		t.Fatalf("monitor hit count = %d, want 1", monitorRun.HitCount)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if got := hits[0].Evidence["matchedAction"]; got != StrategyGenerationRuleActionAddPosition {
		t.Fatalf("matched action = %v, want add_position", got)
	}
	if got := hits[0].Evidence["matchedPrefilterKey"]; got != "break_200" {
		t.Fatalf("matched prefilter = %v, want break_200", got)
	}
}

func TestPortfolioStrategyDiagnosisCreatesDraftOnlyForMissingCoverage(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	portfolio := createStrategyTestPortfolio(t, svc.store, "pf-diagnosis")
	seedStrategyGenerationInstrument(t, svc, ctx, "600000")
	seedStrategyGenerationInstrument(t, svc, ctx, "000001")
	seedStrategyGenerationHolding(t, svc, ctx, portfolio.ID, "600000", "浦发银行", 100, 10)
	seedStrategyGenerationHolding(t, svc, ctx, portfolio.ID, "000001", "平安银行", 200, 8)
	seedWatchQuote(t, svc, "600000", 11, 1.2, QuoteStatusFresh, time.Now())
	seedWatchQuote(t, svc, "000001", 9, 0.8, QuoteStatusFresh, time.Now())
	seedWatchDailyBar(t, svc, "600000", 10.8)
	seedWatchDailyBar(t, svc, "000001", 8.8)
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:        "已有策略",
		Kind:        StrategyKindSymbolStrategy,
		Scope:       StrategyScopePortfolioBound,
		Source:      StrategySourceManual,
		Status:      StrategyStatusActive,
		Symbol:      "000001",
		PortfolioID: portfolio.ID,
		Direction:   StrategyDirectionWatch,
		Thesis:      "已有组合策略覆盖。",
	}); err != nil {
		t.Fatalf("create existing strategy: %v", err)
	}
	configureStrategyGenerationModel(t, svc, ctx)

	run, err := svc.RunStrategyGeneration(ctx, StrategyGenerationInput{
		Mode:        StrategyGenerationModePortfolio,
		UserGoal:    "诊断当前组合",
		PortfolioID: portfolio.ID,
	})
	if err != nil {
		t.Fatalf("run strategy generation: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	if _, err := svc.agentTaskPool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStrategyGeneration,
		ResultSummary: "组合诊断建议",
		Result:        strategyGenerationPortfolioReportResult([]string{"600000", "000001"}),
		Confidence:    0.76,
	}); err != nil {
		t.Fatalf("submit result: %v", err)
	}
	svc.finalizeAgentRunWithOutput(ctx, run.ID, run.DecisionLedgerID, taskID, &AgentExecutorOutput{ExitCode: 0, Duration: time.Millisecond}, nil)

	finalRun, err := svc.GetAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if finalRun.Status != AgentRunStatusCompleted {
		t.Fatalf("status = %s, want completed; error=%s", finalRun.Status, finalRun.ErrorMessage)
	}
	drafts, err := svc.ListStrategies(ctx, StrategyListFilter{Source: StrategySourceAgent, Status: StrategyStatusDraft, Limit: 10})
	if err != nil {
		t.Fatalf("list draft strategies: %v", err)
	}
	if len(drafts) != 1 || drafts[0].Strategy.Symbol != "600000" {
		t.Fatalf("drafts = %+v, want one draft for 600000", drafts)
	}
	sg := mapFromAny(drafts[0].ActiveVersion.GenerationMeta["strategyGeneration"])
	if sg["reviewRequest"] == "" {
		t.Fatalf("strategyGeneration meta missing reviewRequest: %#v", sg)
	}
	txs, err := svc.ListTransactions(ctx, portfolio.ID, 10)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("transactions = %#v, want none", txs)
	}
	reviews, err := svc.ListOperationReviews(ctx, OperationReviewListFilter{PortfolioID: portfolio.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("reviews = %#v, want none", reviews)
	}
}

func TestBuildStrategyGenerationPromptRequiresMCPAndRules(t *testing.T) {
	prompt := buildStrategyGenerationPrompt("task-test", StrategyGenerationContext{
		BuiltAt: time.Now(),
		Input: StrategyGenerationInput{
			Mode:              StrategyGenerationModeSingleInstrument,
			TargetInstruments: []StrategyGenerationTargetInstrument{{Symbol: "302132"}},
		},
	}, "http://127.0.0.1:1/mcp")
	for _, want := range []string{
		"stock_agent.submit_result",
		"taskType\":\"strategy_generation",
		"outputType\":\"strategy_generation",
		"playbook.rules[]",
		"Do not output proposed_operation",
		"stock_agent.get_embedding_status",
		"stock_agent.semantic_search_stock_profiles",
		"stock_agent.semantic_search_news_events",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSingleOpportunityStrategyPromptDoesNotRequirePortfolio(t *testing.T) {
	prompt := buildStrategyGenerationPrompt("task-opportunity", StrategyGenerationContext{
		Input: StrategyGenerationInput{
			Mode:              StrategyGenerationModeOpportunity,
			TargetInstruments: []StrategyGenerationTargetInstrument{{Symbol: "600000"}},
		},
		OpportunityCandidates: []OpportunityCandidate{{ID: "candidate-1", Symbol: "600000"}},
	}, "http://127.0.0.1:1/mcp")
	if !strings.Contains(prompt, "intentionally may omit portfolioId") || !strings.Contains(prompt, "does not require portfolio context") {
		t.Fatalf("single opportunity prompt must not require portfolio context")
	}
}

func seedStrategyGenerationInstrument(t *testing.T, svc *Service, ctx context.Context, symbol string) {
	t.Helper()
	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-" + symbol,
		Symbol:         symbol,
		Market:         inferAStockMarket(symbol),
		InstrumentType: InstrumentTypeStock,
		Name:           "中航成飞",
		Industry:       "军工",
		Sector:         "制造业",
		Concepts:       []string{"航空装备"},
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
}

func seedStrategyGenerationModel(t *testing.T, svc *Service, ctx context.Context) AgentModelProfile {
	t.Helper()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-strategy-generation-test",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-strategy-generation-test",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	return model
}

func configureStrategyGenerationModel(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	model := seedStrategyGenerationModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStrategyGeneration, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind strategy generation model: %v", err)
	}
}

func seedStrategyGenerationHolding(t *testing.T, svc *Service, ctx context.Context, portfolioID, symbol, name string, quantity, costPrice float64) {
	t.Helper()
	if _, err := svc.CreateHolding(ctx, portfolioID, RequestCreateHolding{
		Symbol:    symbol,
		Name:      name,
		Market:    inferAStockMarket(symbol),
		Quantity:  quantity,
		CostPrice: costPrice,
	}); err != nil {
		t.Fatalf("create holding %s: %v", symbol, err)
	}
}

func strategyGenerationReportResult(symbol string) map[string]any {
	return map[string]any{
		"schema_version": StrategyGenerationReportSchemaVersion,
		"run_summary": map[string]any{
			"mode":               StrategyGenerationModeManualTarget,
			"overall_conclusion": "可先观察，等待量价和消息确认。",
			"key_conflicts":      []any{},
			"data_quality_notes": []any{"本地测试数据"},
		},
		"drafts": []any{
			map[string]any{
				"symbol":             symbol,
				"market":             inferAStockMarket(symbol),
				"name":               "中航成飞",
				"draft_type":         StrategyGenerationDraftTypeNewStrategy,
				"strategy_bias":      StrategyBiasBullish,
				"thesis":             "航空装备景气方向明确，但需要等待量价和消息确认。",
				"confidence":         0.72,
				"evidence_summary":   []any{"主数据、最新行情、日 K 摘要、画像均可用"},
				"risk_summary":       []any{"回撤和消息不确定性仍需监控"},
				"invalid_conditions": []any{"跌破关键观察位"},
				"horizon_outlooks":   testModelHorizonOutlooks(68.5),
				"playbook": map[string]any{
					"version": "v1",
					"rules": []any{
						map[string]any{
							"id":                  "observe_1",
							"action":              StrategyGenerationRuleActionObserve,
							"title":               "观察量价确认",
							"trigger":             "放量突破近期高点",
							"preconditions":       "行情数据新鲜",
							"target":              "进入 Review",
							"risk":                "假突破",
							"horizon":             ModelHorizonShort,
							"forecast_basis":      "短期上涨概率与目标价同时改善",
							"dataPrefilters":      []any{map[string]any{"type": "latest_quote", "symbol": symbol}},
							"portfolioPrefilters": []any{},
							"priority":            1,
						},
					},
				},
				"portfolio_aware_suggestion": map[string]any{
					"trade_signal":         "observe",
					"target_position_hint": "",
					"review_request":       "触发后进入 OperationReview",
				},
			},
		},
	}
}

func strategyGenerationPortfolioReportResult(symbols []string) map[string]any {
	drafts := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		report := strategyGenerationReportResult(symbol)
		draft := mapFromAny(sliceFromAny(report["drafts"])[0])
		draft["name"] = symbol
		draft["market"] = inferAStockMarket(symbol)
		draft["portfolio_aware_suggestion"] = map[string]any{
			"trade_signal":         "observe",
			"target_position_hint": "",
			"review_request":       "需要人工 Review，但不能直接创建操作提案",
		}
		drafts = append(drafts, draft)
	}
	return map[string]any{
		"schema_version": StrategyGenerationReportSchemaVersion,
		"run_summary": map[string]any{
			"mode":               StrategyGenerationModePortfolio,
			"overall_conclusion": "组合诊断完成。",
			"key_conflicts":      []any{},
			"data_quality_notes": []any{},
		},
		"drafts": drafts,
	}
}
