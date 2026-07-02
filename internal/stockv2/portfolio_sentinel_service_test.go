package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestBuildPortfolioSentinelContextIncludesHoldingsAndWindowNews(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-context")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-context",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedWatchQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	if _, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:      "test",
		Title:       "海外存储股大跌拖累浪潮信息相关链条",
		PublishedAt: now.Add(-time.Hour),
		FetchedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "test",
		Title:   "浪潮信息相关存储服务器链条承压",
		EventAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create news event: %v", err)
	}
	run := PortfolioSentinelRun{
		ID:            "sentinel-context-run",
		PortfolioID:   portfolio.ID,
		TriggerType:   PortfolioSentinelTriggerManual,
		WindowType:    PortfolioSentinelWindowManual,
		WindowStartAt: now.Add(-2 * time.Hour),
		WindowEndAt:   now,
	}
	pack, err := svc.BuildPortfolioSentinelContext(ctx, run, "test note")
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(pack.Portfolios) != 1 || len(pack.Portfolios[0].Holdings) != 1 {
		t.Fatalf("portfolio context = %+v, want one holding", pack.Portfolios)
	}
	holding := pack.Portfolios[0].Holdings[0]
	if holding.Quote == nil || holding.Quote.PctChange != -3.2 {
		t.Fatalf("holding quote = %+v, want seeded quote", holding.Quote)
	}
	if len(holding.RawNews) == 0 || len(holding.News) == 0 {
		t.Fatalf("holding news raw=%d events=%d, want matched news", len(holding.RawNews), len(holding.News))
	}
}

func TestPortfolioSentinelAgentResultCreatesReviewWithGuardrails(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-review")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-review",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedWatchQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	model := configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:    svc.agentTaskPool,
		submit:  true,
		summary: "存储链条风险升高",
		result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskHigh,
			"run_summary":        "隔夜海外存储链条大跌，当前持仓需要开盘前降风险。",
			"affected_holdings": []any{
				map[string]any{"symbol": "000977", "market": "SZ", "name": "浪潮信息", "risk_level": "high", "direction": "negative", "reasons": []any{"海外可比资产集体下跌"}},
			},
			"portfolio_actions": []any{
				map[string]any{
					"symbol":         "000977",
					"market":         "SZ",
					"portfolio_id":   portfolio.ID,
					"output_type":    OperationReviewOutputProposedOperation,
					"result_summary": "建议降至 5% 权重",
					"reason":         "海外存储链条风险传导",
					"confidence":     0.72,
					"proposed_operation": map[string]any{
						"action":      "reduce",
						"portfolioId": portfolio.ID,
						"symbol":      "000977",
						"market":      "SZ",
						"quantity":    100,
					},
				},
			},
		},
		confidence: 0.72,
	}
	_ = model
	run, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, portfolio.ID, now.Add(-12*time.Hour), now, "", false)
	if err != nil {
		t.Fatalf("run sentinel: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Result == nil || detail.Result.RiskLevel != PortfolioSentinelRiskHigh {
		t.Fatalf("result = %+v, want high risk", detail.Result)
	}
	if len(detail.Reviews) != 1 || detail.Reviews[0].OutputType != OperationReviewOutputProposedOperation {
		t.Fatalf("reviews = %+v, want proposed operation review", detail.Reviews)
	}
	if guardrails := mapFromAny(detail.Reviews[0].Result["guardrails"]); guardrails["status"] == "" {
		t.Fatalf("guardrails missing in review result: %+v", detail.Reviews[0].Result)
	}
}

func configurePortfolioSentinelModelForTest(t *testing.T, svc *Service) AgentModelProfile {
	t.Helper()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-sentinel",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-sentinel",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	primaryID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypePortfolioSentinel, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &primaryID,
	}); err != nil {
		t.Fatalf("bind portfolio sentinel model: %v", err)
	}
	return model
}
