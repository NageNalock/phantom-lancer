package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOperationReviewAgentE2EOutputTypes(t *testing.T) {
	cases := []struct {
		name       string
		outputType string
		result     map[string]any
		assert     func(t *testing.T, review OperationReview)
	}{
		{
			name:       "trade_signal",
			outputType: OperationReviewOutputTradeSignal,
			result: map[string]any{
				"tradeSignal": map[string]any{
					"direction":      "hold",
					"triggerSummary": "价格命中但仍需确认量能",
					"riskNotes":      "只作为账户无关信号",
				},
			},
		},
		{
			name:       "continue_monitoring",
			outputType: OperationReviewOutputContinueMonitoring,
			result: map[string]any{
				"reason":         "命中有效但动作条件不足",
				"nextWatchFocus": "继续观察突破后成交量",
			},
		},
		{
			name:       "proposed_operation_guardrails",
			outputType: OperationReviewOutputProposedOperation,
			result: map[string]any{
				"proposedOperation": map[string]any{
					"action": "add_position",
					"symbol": "000977",
					"amount": 9000.0,
				},
			},
			assert: func(t *testing.T, review OperationReview) {
				t.Helper()
				guardrails := mapFromAny(review.Result["guardrails"])
				if guardrails["status"] != ExecutionGuardrailsStatusBlocked {
					t.Fatalf("guardrails status = %v, want blocked; result = %+v", guardrails["status"], review.Result)
				}
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx, svc, cleanup, review := newOperationReviewAgentE2ETest(t)
			defer cleanup()
			svc.agentExecutor = fakeOperationReviewExecutor{
				pool:       svc.agentTaskPool,
				submit:     true,
				outputType: tt.outputType,
				summary:    "fake agent result",
				result:     tt.result,
				confidence: 0.8,
			}

			run, err := svc.RunAgentReviewForReview(ctx, review.ID, "test")
			if err != nil {
				t.Fatalf("run agent review: %v", err)
			}
			run = waitAgentRunTerminal(t, svc, run.ID)
			if run.Status != AgentRunStatusCompleted {
				t.Fatalf("run status = %s, want completed; error = %s", run.Status, run.ErrorMessage)
			}
			if !strings.Contains(run.Output, "fake agent result") {
				t.Fatalf("run output = %q, want fake result summary", run.Output)
			}

			ledger, err := svc.GetAgentDecisionLedger(ctx, run.DecisionLedgerID)
			if err != nil {
				t.Fatalf("get ledger: %v", err)
			}
			if ledger.StructuredOutput["outputType"] != tt.outputType {
				t.Fatalf("ledger outputType = %v, want %s", ledger.StructuredOutput["outputType"], tt.outputType)
			}

			reloaded, err := svc.GetOperationReview(ctx, review.ID)
			if err != nil {
				t.Fatalf("get review: %v", err)
			}
			if reloaded.Status != OperationReviewStatusCompleted || reloaded.OutputType != tt.outputType {
				t.Fatalf("review = %+v, want completed %s", reloaded, tt.outputType)
			}
			if reloaded.ResultSummary != "fake agent result" {
				t.Fatalf("review summary = %q", reloaded.ResultSummary)
			}
			if tt.assert != nil {
				tt.assert(t, reloaded)
			}
			if tt.outputType == OperationReviewOutputTradeSignal {
				alerts, err := svc.ListAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: "000977", Limit: 10})
				if err != nil {
					t.Fatalf("list alerts: %v", err)
				}
				if len(alerts) != 1 || alerts[0].TriggerSource != AlertTriggerSourceAgentConfirmed {
					t.Fatalf("alerts = %+v, want one agent confirmed alert", alerts)
				}
			}
		})
	}
}

func TestOperationReviewAgentE2EFailureBranches(t *testing.T) {
	cases := []struct {
		name       string
		execErr    error
		submit     bool
		outputType string
		wantError  string
	}{
		{
			name:      "executor_error_without_mcp_result",
			execErr:   errors.New("cli failed"),
			submit:    false,
			wantError: "cli failed",
		},
		{
			name:      "cli_no_submit_result",
			submit:    false,
			wantError: "no valid result submitted",
		},
		{
			name:       "invalid_output_type",
			submit:     true,
			outputType: "made_up",
			wantError:  "no valid result submitted",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx, svc, cleanup, review := newOperationReviewAgentE2ETest(t)
			defer cleanup()
			svc.agentExecutor = fakeOperationReviewExecutor{
				pool:       svc.agentTaskPool,
				submit:     tt.submit,
				outputType: tt.outputType,
				summary:    "should not land",
				result:     map[string]any{"reason": "invalid"},
				execErr:    tt.execErr,
			}

			run, err := svc.RunAgentReviewForReview(ctx, review.ID, "test")
			if err != nil {
				t.Fatalf("run agent review: %v", err)
			}
			run = waitAgentRunTerminal(t, svc, run.ID)
			if run.Status != AgentRunStatusFailed {
				t.Fatalf("run status = %s, want failed", run.Status)
			}
			if !strings.Contains(run.ErrorMessage, tt.wantError) {
				t.Fatalf("run error = %q, want contains %q", run.ErrorMessage, tt.wantError)
			}
			reloaded, err := svc.GetOperationReview(ctx, review.ID)
			if err != nil {
				t.Fatalf("get review: %v", err)
			}
			if reloaded.OutputType != "" || reloaded.Status != OperationReviewStatusPending {
				t.Fatalf("review should not be finalized after failed run: %+v", reloaded)
			}
		})
	}
}

func TestOperationReviewAgentExecutorErrorWithMCPResultWins(t *testing.T) {
	ctx, svc, cleanup, review := newOperationReviewAgentE2ETest(t)
	defer cleanup()
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		outputType: OperationReviewOutputContinueMonitoring,
		summary:    "mcp result accepted despite cli error",
		result:     map[string]any{"reason": "MCP callback completed before CLI returned error"},
		execErr:    errors.New("cli exited non-zero after submit"),
	}

	run, err := svc.RunAgentReviewForReview(ctx, review.ID, "test")
	if err != nil {
		t.Fatalf("run agent review: %v", err)
	}
	run = waitAgentRunTerminal(t, svc, run.ID)
	if run.Status != AgentRunStatusCompleted {
		t.Fatalf("run status = %s, want completed; error = %s", run.Status, run.ErrorMessage)
	}
	reloaded, err := svc.GetOperationReview(ctx, review.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if reloaded.OutputType != OperationReviewOutputContinueMonitoring {
		t.Fatalf("review output type = %q, want continue_monitoring", reloaded.OutputType)
	}
}

func TestOperationReviewAgentRunIsIdempotentWhileActive(t *testing.T) {
	ctx, svc, cleanup, review := newOperationReviewAgentE2ETest(t)
	defer cleanup()
	started := make(chan string, 1)
	release := make(chan struct{})
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		outputType: OperationReviewOutputContinueMonitoring,
		summary:    "after release",
		result:     map[string]any{"reason": "idempotent"},
		started:    started,
		release:    release,
	}

	first, err := svc.RunAgentReviewForReview(ctx, review.ID, "test")
	if err != nil {
		t.Fatalf("run first agent review: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fake executor did not start")
	}
	second, err := svc.RunAgentReviewForReview(ctx, review.ID, "test")
	if err != nil {
		t.Fatalf("run second agent review: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second run id = %q, want existing %q", second.ID, first.ID)
	}
	count, err := svc.CountAgentRuns(ctx, AgentRunListFilter{
		TaskType:          AgentTaskTypeOperationReview,
		TriggerObjectType: "operation_review",
		TriggerObjectID:   review.ID,
	})
	if err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("run count = %d, want 1", count)
	}
	close(release)
	waitAgentRunTerminal(t, svc, first.ID)
}

func TestOperationReviewAgentRunFailsWhenReviewResultCannotBeSaved(t *testing.T) {
	ctx, svc, cleanup, review := newOperationReviewAgentE2ETest(t)
	defer cleanup()
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		outputType: OperationReviewOutputProposedOperation,
		summary:    "invalid proposed operation",
		result: map[string]any{
			"proposedOperation": map[string]any{
				"symbol": "000977",
				"amount": 1000.0,
			},
		},
	}

	run, err := svc.RunAgentReviewForReview(ctx, review.ID, "test")
	if err != nil {
		t.Fatalf("run agent review: %v", err)
	}
	run = waitAgentRunTerminal(t, svc, run.ID)
	if run.Status != AgentRunStatusFailed {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	if !strings.Contains(run.ErrorMessage, "save review result failed") {
		t.Fatalf("run error = %q, want review save failure", run.ErrorMessage)
	}
	reloaded, err := svc.GetOperationReview(ctx, review.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if reloaded.OutputType != "" {
		t.Fatalf("review output type = %q, want empty after failed save", reloaded.OutputType)
	}
}

func newOperationReviewAgentE2ETest(t *testing.T) (context.Context, *Service, func(), OperationReview) {
	t.Helper()
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	hit := seedReviewMonitorHit(t, svc)
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		cleanup()
		t.Fatalf("create review: %v", err)
	}
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "fake-codex-cli",
	})
	if err != nil {
		cleanup()
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "fake-model",
		Enabled:    true,
	})
	if err != nil {
		cleanup()
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &model.ID,
	}); err != nil {
		cleanup()
		t.Fatalf("update task profile: %v", err)
	}
	return ctx, svc, cleanup, review
}

func waitAgentRunTerminal(t *testing.T, svc *Service, runID string) AgentRun {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	var run AgentRun
	var err error
	for time.Now().Before(deadline) {
		run, err = svc.GetAgentRun(ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status == AgentRunStatusCompleted || run.Status == AgentRunStatusFailed {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent run %s did not finish; last status=%s error=%v", runID, run.Status, err)
	return AgentRun{}
}

type fakeOperationReviewExecutor struct {
	pool       *agentTaskPool
	submit     bool
	outputType string
	summary    string
	result     map[string]any
	confidence float64
	execErr    error
	started    chan<- string
	release    <-chan struct{}
}

func (f fakeOperationReviewExecutor) ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if f.started != nil {
		f.started <- taskID
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.submit {
		outputType := f.outputType
		if outputType == "" {
			outputType = OperationReviewOutputContinueMonitoring
		}
		_ = f.pool.HandleMCPRequest(mcpSubmitResultRequest(taskID, outputType, f.summary, f.result, f.confidence))
	}
	exitCode := 0
	stderr := ""
	if f.execErr != nil {
		exitCode = 1
		stderr = f.execErr.Error()
	}
	return &AgentExecutorOutput{
		StdoutTail: "fake stdout for " + pack.Hit.ID,
		StderrTail: stderr,
		ExitCode:   exitCode,
		Duration:   time.Millisecond,
	}, f.execErr
}

func (f fakeOperationReviewExecutor) ExecuteStockProfileSummary(ctx context.Context, taskID string, profile StockProfile, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := f.result
	if result == nil {
		result = map[string]any{"summaryZh": profile.BusinessSummary, "summaryEn": profile.Name}
	}
	if f.submit {
		_, _ = f.pool.submitResult(taskID, AgentTaskTypeStockProfileSummary, AgentTaskSubmittedResult{
			OutputType:    AgentTaskTypeStockProfileSummary,
			ResultSummary: f.summary,
			Result:        result,
			Confidence:    f.confidence,
		})
	}
	return &AgentExecutorOutput{
		StdoutTail: "fake profile stdout for " + profile.Symbol,
		ExitCode:   0,
		Duration:   time.Millisecond,
	}, f.execErr
}

func (f fakeOperationReviewExecutor) ExecuteStrategyGeneration(ctx context.Context, taskID string, pack StrategyGenerationContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := f.result
	if result == nil {
		mode := firstNonEmpty(pack.Input.Mode, pack.Mode, StrategyGenerationModePortfolio)
		result = map[string]any{
			"schema_version": "strategy-generation-report/v1",
			"run_summary":    map[string]any{"mode": mode},
			"drafts":         []any{},
		}
	}
	if f.submit {
		_, _ = f.pool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
			OutputType:    AgentTaskTypeStrategyGeneration,
			ResultSummary: f.summary,
			Result:        result,
			Confidence:    f.confidence,
		})
	}
	exitCode := 0
	stderr := ""
	if f.execErr != nil {
		exitCode = 1
		stderr = f.execErr.Error()
	}
	return &AgentExecutorOutput{
		StdoutTail: "fake strategy generation stdout",
		StderrTail: stderr,
		ExitCode:   exitCode,
		Duration:   time.Millisecond,
	}, f.execErr
}

func (f fakeOperationReviewExecutor) ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := map[string]any{
		"schema_version": StrategyGenerationStepOutputSchema,
		"step_key":       pack.StepKey,
		"role":           pack.Role,
		"summary":        "step ok",
	}
	if pack.StepKey == StrategyGenerationStepFormatter {
		result = f.result
		if result == nil {
			mode := firstNonEmpty(pack.Context.Input.Mode, pack.Context.Mode, StrategyGenerationModePortfolio)
			result = map[string]any{
				"schema_version": StrategyGenerationReportSchemaVersion,
				"run_summary":    map[string]any{"mode": mode},
				"drafts":         []any{},
			}
		}
	}
	if f.submit {
		_, _ = f.pool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
			OutputType:    AgentTaskTypeStrategyGeneration,
			ResultSummary: f.summary,
			Result:        result,
			Confidence:    f.confidence,
		})
	}
	exitCode := 0
	stderr := ""
	if f.execErr != nil {
		exitCode = 1
		stderr = f.execErr.Error()
	}
	return &AgentExecutorOutput{
		StdoutTail: "fake strategy generation step stdout",
		StderrTail: stderr,
		ExitCode:   exitCode,
		Duration:   time.Millisecond,
	}, f.execErr
}

func (f fakeOperationReviewExecutor) ExecuteOpportunityDiscovery(ctx context.Context, taskID string, pack OpportunityDiscoveryContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := f.result
	if result == nil {
		result = map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": pack.Opportunity.ID,
			"summary":        "fake opportunity discovery",
			"candidates":     []any{},
		}
	}
	if f.submit {
		_, _ = f.pool.submitResult(taskID, AgentTaskTypeOpportunityDiscovery, AgentTaskSubmittedResult{
			OutputType:    OpportunityDiscoveryOutputType,
			ResultSummary: f.summary,
			Result:        result,
			Confidence:    f.confidence,
		})
	}
	exitCode := 0
	stderr := ""
	if f.execErr != nil {
		exitCode = 1
		stderr = f.execErr.Error()
	}
	return &AgentExecutorOutput{
		StdoutTail: "fake opportunity discovery stdout",
		StderrTail: stderr,
		ExitCode:   exitCode,
		Duration:   time.Millisecond,
	}, f.execErr
}

func (f fakeOperationReviewExecutor) ExecutePortfolioSentinel(ctx context.Context, taskID string, pack PortfolioSentinelContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := f.result
	if result == nil {
		result = map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "fake portfolio sentinel",
			"portfolio_actions":  []any{},
			"affected_holdings":  []any{},
		}
	}
	if f.submit {
		_, _ = f.pool.submitResult(taskID, AgentTaskTypePortfolioSentinel, AgentTaskSubmittedResult{
			OutputType:    PortfolioSentinelOutputType,
			ResultSummary: f.summary,
			Result:        result,
			Confidence:    f.confidence,
		})
	}
	exitCode := 0
	stderr := ""
	if f.execErr != nil {
		exitCode = 1
		stderr = f.execErr.Error()
	}
	return &AgentExecutorOutput{
		StdoutTail: "fake portfolio sentinel stdout",
		StderrTail: stderr,
		ExitCode:   exitCode,
		Duration:   time.Millisecond,
	}, f.execErr
}

func mcpSubmitResultRequest(taskID, outputType, summary string, result map[string]any, confidence float64) []byte {
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeOperationReview,
				"result": map[string]any{
					"outputType":    outputType,
					"resultSummary": summary,
					"result":        result,
					"confidence":    confidence,
				},
			},
		},
	})
	return raw
}
