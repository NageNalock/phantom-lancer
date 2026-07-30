package stockv2

import (
	"context"
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
