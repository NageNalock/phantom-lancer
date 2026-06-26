package stockv2

import (
	"context"
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
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
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
		Status:         "active",
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
							"dataPrefilters":      []any{map[string]any{"type": "latest_quote", "symbol": symbol}},
							"portfolioPrefilters": []any{},
							"newsPrefilters":      []any{},
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
