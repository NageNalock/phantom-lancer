package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPortfolioSentinelRetriesInvalidHoldingCoverageWithValidationFeedback(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-corrective-retry")
	for _, holding := range []StockV2Holding{
		{
			ID: "holding-etf", PortfolioID: portfolio.ID, Symbol: "588940", Market: "SH", Name: "科创50ETF富国",
			Quantity: 1000, AvailableQuantity: 1000, CostPrice: 0.9, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "holding-server", PortfolioID: portfolio.ID, Symbol: "000977", Market: "SZ", Name: "浪潮信息",
			Quantity: 100, AvailableQuantity: 100, CostPrice: 50, CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err := svc.store.CreateHolding(ctx, holding); err != nil {
			t.Fatalf("create holding %s: %v", holding.Symbol, err)
		}
	}
	configurePortfolioSentinelModelForTest(t, svc)
	executor := &correctivePortfolioSentinelExecutor{
		fakeOperationReviewExecutor: fakeOperationReviewExecutor{pool: svc.agentTaskPool},
		portfolioID:                 portfolio.ID,
	}
	svc.agentExecutor = executor

	run, err := svc.startPortfolioSentinelRun(
		ctx,
		PortfolioSentinelTriggerManual,
		PortfolioSentinelWindowManual,
		portfolio.ID,
		now.Add(-12*time.Hour),
		now,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("run sentinel: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get sentinel detail: %v", err)
	}
	if executor.calls != 2 {
		t.Fatalf("executor calls = %d, want one initial call and one corrective retry", executor.calls)
	}
	if len(executor.notes) != 2 || !strings.Contains(executor.notes[1], "every holding requires exactly one action plan") {
		t.Fatalf("corrective note = %#v, want exact server validation failure", executor.notes)
	}
	if detail.Run.Status != PortfolioSentinelStatusCompleted || detail.Result == nil {
		t.Fatalf("sentinel result = status %q result %#v error %q, want completed", detail.Run.Status, detail.Result, detail.Run.ErrorMessage)
	}
	report, err := portfolioSentinelReportFromResult(detail.Result.RawResult)
	if err != nil {
		t.Fatalf("parse stored report: %v", err)
	}
	if len(report.ActionPlans) != 2 {
		t.Fatalf("stored action plans = %#v, want both holdings", report.ActionPlans)
	}
}

func TestPortfolioSentinelFallsBackToSecondCLIModelAndKeepsBothAttempts(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-sentinel-fallback",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	primary, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "sentinel-primary",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create primary model: %v", err)
	}
	fallback, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "sentinel-fallback",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create fallback model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypePortfolioSentinel, RequestUpdateAgentTaskProfile{
		PrimaryModelID:  &primary.ID,
		FallbackModelID: &primary.ID,
	}); !errors.Is(err, ErrAgentFallbackMatchesPrimary) {
		t.Fatalf("same primary/fallback error = %v, want ErrAgentFallbackMatchesPrimary", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypePortfolioSentinel, RequestUpdateAgentTaskProfile{
		PrimaryModelID:  &primary.ID,
		FallbackModelID: &fallback.ID,
	}); err != nil {
		t.Fatalf("bind sentinel primary and fallback: %v", err)
	}
	executor := &fallbackPortfolioSentinelExecutor{
		fakeOperationReviewExecutor: fakeOperationReviewExecutor{pool: svc.agentTaskPool},
		primaryModel:                primary.ModelName,
		fallbackModel:               fallback.ModelName,
	}
	svc.agentExecutor = executor

	now := time.Now()
	run, err := svc.startPortfolioSentinelRun(
		ctx,
		PortfolioSentinelTriggerManual,
		PortfolioSentinelWindowManual,
		"",
		now.Add(-12*time.Hour),
		now,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("run sentinel with fallback: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get sentinel detail: %v", err)
	}
	if detail.Run.Status != PortfolioSentinelStatusCompleted || detail.Result == nil {
		t.Fatalf("sentinel status = %q result = %#v error = %q, want completed", detail.Run.Status, detail.Result, detail.Run.ErrorMessage)
	}
	if len(executor.models) != 2 || executor.models[0] != primary.ModelName || executor.models[1] != fallback.ModelName {
		t.Fatalf("executor models = %#v, want primary then fallback", executor.models)
	}
	if len(detail.AgentAttempts) != 2 {
		t.Fatalf("agent attempts = %d, want 2", len(detail.AgentAttempts))
	}
	primaryAttempt := detail.AgentAttempts[0]
	fallbackAttempt := detail.AgentAttempts[1]
	if primaryAttempt.Run.ModelID != primary.ID || primaryAttempt.Run.Status != AgentRunStatusFailed {
		t.Fatalf("primary attempt = %+v", primaryAttempt.Run)
	}
	if fallbackAttempt.Run.ModelID != fallback.ID || fallbackAttempt.Run.Status != AgentRunStatusCompleted {
		t.Fatalf("fallback attempt = %+v", fallbackAttempt.Run)
	}
	if detail.Run.AgentRunID != fallbackAttempt.Run.ID || detail.Run.DecisionLedgerID != fallbackAttempt.Run.DecisionLedgerID {
		t.Fatalf("sentinel final agent = %q/%q, want fallback %q/%q",
			detail.Run.AgentRunID, detail.Run.DecisionLedgerID, fallbackAttempt.Run.ID, fallbackAttempt.Run.DecisionLedgerID)
	}
	if fmt.Sprint(primaryAttempt.Run.CostEstimate["inputTokens"]) != "120" ||
		fmt.Sprint(fallbackAttempt.Run.CostEstimate["inputTokens"]) != "80" {
		t.Fatalf("attempt token audits = primary %#v fallback %#v", primaryAttempt.Run.CostEstimate, fallbackAttempt.Run.CostEstimate)
	}
	if primaryAttempt.Ledger == nil ||
		!strings.Contains(primaryAttempt.Ledger.OutputArtifactSummary, "fallback_agent_run_id: "+fallbackAttempt.Run.ID) {
		t.Fatalf("primary fallback audit = %#v", primaryAttempt.Ledger)
	}
	if fallbackAttempt.Ledger == nil ||
		fmt.Sprint(fallbackAttempt.Ledger.RedactionSummary["fallbackFromAgentRunId"]) != primaryAttempt.Run.ID {
		t.Fatalf("fallback source audit = %#v", fallbackAttempt.Ledger)
	}
}

func TestPortfolioSentinelFallbackEligibilityRejectsPersistenceFailure(t *testing.T) {
	run := AgentRun{
		Status:       AgentRunStatusFailed,
		ErrorMessage: "save portfolio sentinel result failed: disk full",
	}
	if portfolioSentinelFallbackEligible(context.Background(), run, &AgentExecutorOutput{ExitCode: 0}, nil) {
		t.Fatal("persistence failure must not spend another model attempt")
	}
	if !portfolioSentinelFallbackEligible(
		context.Background(),
		AgentRun{Status: AgentRunStatusFailed, ErrorMessage: "no valid result submitted"},
		&AgentExecutorOutput{ExitCode: 1},
		errors.New("process exited (code 1) without submitting result"),
	) {
		t.Fatal("model process failure should allow one fallback")
	}
}

type fallbackPortfolioSentinelExecutor struct {
	fakeOperationReviewExecutor
	primaryModel  string
	fallbackModel string
	models        []string
}

func (e *fallbackPortfolioSentinelExecutor) ExecutePortfolioSentinel(
	_ context.Context,
	taskID string,
	_ PortfolioSentinelContext,
	modelName, _ string,
) (*AgentExecutorOutput, error) {
	e.models = append(e.models, modelName)
	if modelName == e.primaryModel {
		return &AgentExecutorOutput{
			Command:      "codex exec",
			StdoutTail:   "usage limit reached",
			ExitCode:     1,
			Duration:     time.Millisecond,
			PromptTokens: 120,
			OutputTokens: 5,
			RequestCount: 1,
			ResearchAudit: AgentCLIResearchAudit{
				LiveSearchEnabled: true,
				WebSearchCount:    2,
				MCPToolCalls:      map[string]int{"search": 1},
			},
		}, errors.New("usage limit reached")
	}
	if modelName != e.fallbackModel {
		return nil, fmt.Errorf("unexpected model %q", modelName)
	}
	_, _ = e.pool.submitResult(taskID, AgentTaskTypePortfolioSentinel, AgentTaskSubmittedResult{
		OutputType:    PortfolioSentinelOutputType,
		ResultSummary: "fallback sentinel completed",
		Result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "fallback sentinel completed",
			"portfolio_actions":  []any{},
			"affected_holdings":  []any{},
		},
		Confidence: 0.8,
	})
	return &AgentExecutorOutput{
		Command:      "codex exec",
		StdoutTail:   "fallback result",
		ExitCode:     0,
		Duration:     time.Millisecond,
		PromptTokens: 80,
		OutputTokens: 20,
		RequestCount: 1,
		ResearchAudit: AgentCLIResearchAudit{
			LiveSearchEnabled: true,
			WebSearchCount:    1,
			MCPToolCalls:      map[string]int{"search": 2},
		},
	}, nil
}

type correctivePortfolioSentinelExecutor struct {
	fakeOperationReviewExecutor
	portfolioID string
	calls       int
	notes       []string
}

func (e *correctivePortfolioSentinelExecutor) ExecutePortfolioSentinel(
	_ context.Context,
	taskID string,
	pack PortfolioSentinelContext,
	_, _ string,
) (*AgentExecutorOutput, error) {
	e.calls++
	e.notes = append(e.notes, pack.Note)
	plans := []any{
		map[string]any{
			"id":           "plan-etf-hold",
			"portfolio_id": e.portfolioID,
			"symbol":       "588940",
			"market":       "SH",
			"name":         "科创50ETF富国",
			"action":       PortfolioSentinelPlanHold,
			"reason":       "证据不足，继续观察。",
		},
	}
	if e.calls > 1 {
		plans = append(plans, map[string]any{
			"id":           "plan-server-hold",
			"portfolio_id": e.portfolioID,
			"symbol":       "000977",
			"market":       "SZ",
			"name":         "浪潮信息",
			"action":       PortfolioSentinelPlanHold,
			"reason":       "纠正覆盖缺失，继续观察。",
		})
	}
	_, _ = e.pool.submitResult(taskID, AgentTaskTypePortfolioSentinel, AgentTaskSubmittedResult{
		OutputType:    PortfolioSentinelOutputType,
		ResultSummary: "组合哨兵覆盖校验",
		Result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "测试组合哨兵持仓覆盖纠错。",
			"action_plans":       plans,
		},
		Confidence: 0.8,
	})
	return &AgentExecutorOutput{
		StdoutTail: "fake portfolio sentinel corrective result",
		ExitCode:   0,
		Duration:   time.Millisecond,
		ResearchAudit: AgentCLIResearchAudit{
			LiveSearchEnabled: true,
		},
	}, nil
}
