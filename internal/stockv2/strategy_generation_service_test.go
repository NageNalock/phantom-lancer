package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-empty-diagnosis")
	configureStrategyGenerationAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "空组合诊断完成",
		confidence: 0.7,
		result: map[string]any{
			"schema_version": StrategyGenerationReportSchemaVersion,
			"run_summary": map[string]any{
				"mode":               StrategyGenerationModePortfolio,
				"overall_conclusion": "组合为空，暂无单票策略草案。",
			},
			"drafts": []any{},
		},
	}

	run, err := svc.RunStrategyGeneration(ctx, StrategyGenerationInput{
		Mode:        StrategyGenerationModePortfolio,
		PortfolioID: portfolio.ID,
	}, "test")
	if err != nil {
		t.Fatalf("run strategy generation: %v", err)
	}
	run = waitAgentRunTerminal(t, svc, run.ID)
	if run.Status != AgentRunStatusCompleted {
		t.Fatalf("run status = %s error=%s", run.Status, run.ErrorMessage)
	}
	strategies, err := svc.ListStrategies(ctx, StrategyListFilter{PortfolioID: portfolio.ID, Limit: 20})
	if err != nil {
		t.Fatalf("list strategies: %v", err)
	}
	if len(strategies) != 0 {
		t.Fatalf("strategies = %+v, want none for empty holdings", strategies)
	}
}

func TestPortfolioStrategyDiagnosisCreatesDraftOnlyForMissingCoverage(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-diagnosis")
	seedStrategyGenerationHolding(t, svc, portfolio.ID, "300750", "宁德时代")
	seedStrategyGenerationHolding(t, svc, portfolio.ID, "000977", "浪潮信息")
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:        "宁德时代已有策略",
		Kind:        StrategyKindSymbolStrategy,
		Scope:       StrategyScopePortfolioBound,
		Source:      StrategySourceManual,
		Status:      StrategyStatusActive,
		Symbol:      "300750",
		Market:      "SZ",
		PortfolioID: portfolio.ID,
		Direction:   StrategyBiasBullish,
	}); err != nil {
		t.Fatalf("create existing strategy: %v", err)
	}
	configureStrategyGenerationAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "组合诊断完成",
		confidence: 0.8,
		result: map[string]any{
			"schema_version": StrategyGenerationReportSchemaVersion,
			"run_summary": map[string]any{
				"mode":               StrategyGenerationModePortfolio,
				"overall_conclusion": "已有策略不重复创建，缺失策略生成草案。",
			},
			"drafts": []any{
				strategyGenerationDraft("300750", "宁德时代", "已有策略只给 patch", "需要补充消息面条件", "减仓信号需人工 Review"),
				strategyGenerationDraft("000977", "浪潮信息", "补齐组合绑定策略", "观察算力主线", "触发减仓时进入 Review"),
			},
		},
	}

	run, err := svc.RunStrategyGeneration(ctx, StrategyGenerationInput{
		Mode:        StrategyGenerationModePortfolio,
		PortfolioID: portfolio.ID,
	}, "test")
	if err != nil {
		t.Fatalf("run strategy generation: %v", err)
	}
	run = waitAgentRunTerminal(t, svc, run.ID)
	if run.Status != AgentRunStatusCompleted {
		t.Fatalf("run status = %s error=%s", run.Status, run.ErrorMessage)
	}
	strategies, err := svc.ListStrategies(ctx, StrategyListFilter{PortfolioID: portfolio.ID, Limit: 20})
	if err != nil {
		t.Fatalf("list strategies: %v", err)
	}
	var draftsForMissing, strategiesForExisting int
	for _, item := range strategies {
		switch item.Strategy.Symbol {
		case "000977":
			if item.Strategy.Status == StrategyStatusDraft && item.Strategy.Source == StrategySourceAgent {
				draftsForMissing++
				if item.ActiveVersion == nil {
					t.Fatalf("draft missing active version pointer: %+v", item)
				}
				if item.ActiveVersion.GenerationMeta["playbook"] == nil {
					t.Fatalf("generationMeta missing playbook: %+v", item.ActiveVersion.GenerationMeta)
				}
				if sg := mapFromAny(item.ActiveVersion.GenerationMeta["strategyGeneration"]); sg["reviewRequest"] == "" {
					t.Fatalf("review request not saved in generationMeta: %+v", sg)
				}
			}
		case "300750":
			strategiesForExisting++
		}
	}
	if draftsForMissing != 1 {
		t.Fatalf("agent drafts for missing coverage = %d, want 1; strategies=%+v", draftsForMissing, strategies)
	}
	if strategiesForExisting != 1 {
		t.Fatalf("strategies for existing covered holding = %d, want 1 existing only; strategies=%+v", strategiesForExisting, strategies)
	}
	reviews, err := svc.ListOperationReviews(ctx, OperationReviewListFilter{PortfolioID: portfolio.ID, Limit: 20})
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("reviews = %+v, want no direct review creation from review_request", reviews)
	}
	txs, err := svc.ListTransactions(ctx, portfolio.ID, 20)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("transactions = %+v, want no direct trade/holding changes", txs)
	}
}

func configureStrategyGenerationAgent(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "fake-strategy-generation-codex",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "fake-strategy-model",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStrategyGeneration, RequestUpdateAgentTaskProfile{PrimaryModelID: &model.ID}); err != nil {
		t.Fatalf("bind strategy_generation model: %v", err)
	}
}

func seedStrategyGenerationHolding(t *testing.T, svc *Service, portfolioID, symbol, name string) {
	t.Helper()
	ctx := context.Background()
	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-" + symbol,
		Symbol:         symbol,
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           name,
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
	if _, err := svc.CreateHolding(ctx, portfolioID, RequestCreateHolding{
		Symbol:    symbol,
		Market:    "SZ",
		Name:      name,
		Quantity:  100,
		CostPrice: 10,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if err := svc.store.UpsertLatestQuote(ctx, StockV2QuoteLatest{
		Symbol:    symbol,
		Market:    "SZ",
		Name:      name,
		LastPrice: 12,
		QuoteAt:   time.Now(),
		FetchedAt: time.Now(),
		Source:    "test",
		Status:    QuoteStatusFresh,
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	if err := svc.store.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: symbol, Market: "SZ", TradeDate: "2026-06-24", Close: 10, High: 11, Low: 9, Volume: 1000, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: time.Now(), Quality: DailyBarQualityOK},
		{Symbol: symbol, Market: "SZ", TradeDate: "2026-06-25", Close: 12, High: 12.5, Low: 9.5, Volume: 1200, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: time.Now(), Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert daily bars: %v", err)
	}
	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:      symbol,
		Market:      "SZ",
		Name:        name,
		ProfileText: name + " 测试画像",
		UpdatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("upsert stock profile: %v", err)
	}
}

func strategyGenerationDraft(symbol, name, thesis, risk, reviewRequest string) map[string]any {
	return map[string]any{
		"symbol":        symbol,
		"market":        "SZ",
		"name":          name,
		"draft_type":    StrategyGenerationDraftNewStrategy,
		"strategy_bias": StrategyBiasBullish,
		"thesis":        thesis,
		"confidence":    0.72,
		"evidence_summary": []any{
			"持仓已纳入组合诊断",
		},
		"risk_summary": []any{risk},
		"invalid_conditions": []any{
			"核心逻辑证伪",
		},
		"playbook": map[string]any{
			"version": "v1",
			"rules": []any{
				map[string]any{
					"id":                  "observe_1",
					"action":              StrategyGenerationActionObserve,
					"title":               "观察",
					"trigger":             "价格或消息触发后复核",
					"preconditions":       "行情数据新鲜",
					"target":              "继续观察",
					"risk":                risk,
					"dataPrefilters":      []any{},
					"portfolioPrefilters": []any{},
					"newsPrefilters":      []any{},
					"priority":            1,
				},
			},
		},
		"portfolio_aware_suggestion": map[string]any{
			"trade_signal":         StrategyGenerationActionObserve,
			"target_position_hint": "",
			"review_request":       reviewRequest,
		},
	}
}
