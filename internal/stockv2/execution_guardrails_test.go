package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestExecutionGuardrailsCashInsufficientBlocked(t *testing.T) {
	result := EvaluateExecutionGuardrails(guardrailsInputForTest(ProposedOperation{
		Action: ProposedOperationActionAddPosition,
		Symbol: "000977",
		Amount: 9000,
	}))
	assertGuardrailReason(t, result, ExecutionGuardrailsStatusBlocked, "cash_insufficient")
}

func TestExecutionGuardrailsEmptyHoldingSellBlocked(t *testing.T) {
	input := guardrailsInputForTest(ProposedOperation{
		Action:   ProposedOperationActionReducePosition,
		Symbol:   "600000",
		Quantity: 10,
	})
	result := EvaluateExecutionGuardrails(input)
	assertGuardrailReason(t, result, ExecutionGuardrailsStatusBlocked, "holding_empty")
}

func TestExecutionGuardrailsPositionLimitBlocked(t *testing.T) {
	result := EvaluateExecutionGuardrails(guardrailsInputForTest(ProposedOperation{
		Action: ProposedOperationActionAddPosition,
		Symbol: "000977",
		Amount: 100,
	}))
	assertGuardrailReason(t, result, ExecutionGuardrailsStatusBlocked, "position_limit_exceeded")
}

func TestExecutionGuardrailsStaleDataDegraded(t *testing.T) {
	input := guardrailsInputForTest(ProposedOperation{
		Action: ProposedOperationActionAddPosition,
		Symbol: "000977",
	})
	input.Quote.Status = QuoteStatusStale
	input.Snapshot.Status = PortfolioValuationStatusStale
	result := EvaluateExecutionGuardrails(input)
	if result.Status != ExecutionGuardrailsStatusDegraded {
		t.Fatalf("guardrail status = %s, want degraded; reasons = %+v", result.Status, result.Reasons)
	}
	assertGuardrailReason(t, result, ExecutionGuardrailsStatusDegraded, "quote_stale")
}

func TestSaveProposedOperationRunsGuardrails(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	updated, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType: OperationReviewOutputProposedOperation,
		Result: map[string]any{
			"proposedOperation": map[string]any{
				"action": "add_position",
				"symbol": "000977",
				"amount": 9000.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("save proposed operation: %v", err)
	}
	guardrails := mapFromAny(updated.Result["guardrails"])
	if guardrails["status"] != ExecutionGuardrailsStatusBlocked {
		t.Fatalf("guardrails = %+v, want blocked", guardrails)
	}
	if updated.Result["acceptanceStatus"] != "blocked" {
		t.Fatalf("acceptance status = %v, want blocked", updated.Result["acceptanceStatus"])
	}
}

func TestSaveProposedOperationWithoutPortfolioBlockedByGuardrails(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	now := time.Now()
	seedWatchQuote(t, svc, "000977", 61, 1.2, QuoteStatusFresh, now)
	run, err := svc.store.CreateMonitorRun(ctx, MonitorRun{
		ID:          "run-no-portfolio-review",
		TaskType:    MonitorTaskDataStrategyMonitor,
		Status:      MonitorRunStatusCompleted,
		TriggerType: MonitorTriggerManual,
		StartedAt:   now,
		FinishedAt:  now,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("create monitor run: %v", err)
	}
	hit, err := svc.store.CreateMonitorHit(ctx, MonitorHit{
		ID:        "hit-no-portfolio-review",
		RunID:     run.ID,
		TaskType:  MonitorTaskDataStrategyMonitor,
		Status:    MonitorHitStatusCandidate,
		Symbol:    "000977",
		Market:    "SZ",
		Title:     "账户无关监控命中",
		Summary:   "没有组合上下文时不允许绕过 guardrails 生成账户操作。",
		Evidence:  map[string]any{"matchedAction": "add_position"},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create monitor hit: %v", err)
	}
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	updated, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType: OperationReviewOutputProposedOperation,
		Result: map[string]any{
			"proposedOperation": map[string]any{
				"action": "add_position",
				"symbol": "000977",
				"amount": 1000.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("save proposed operation: %v", err)
	}
	guardrails := mapFromAny(updated.Result["guardrails"])
	if guardrails["status"] != ExecutionGuardrailsStatusBlocked {
		t.Fatalf("guardrails = %+v, want blocked", guardrails)
	}
	if updated.Result["acceptanceStatus"] != "blocked" {
		t.Fatalf("acceptance status = %v, want blocked", updated.Result["acceptanceStatus"])
	}
}

func TestStrategyPatchDoesNotUpdateActiveStrategyVersion(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	before, err := svc.GetStrategy(ctx, review.StrategyID)
	if err != nil {
		t.Fatalf("get strategy before patch: %v", err)
	}
	updated, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType: OperationReviewOutputStrategyPatch,
		Result: map[string]any{
			"strategyPatch": map[string]any{"thesis": "新的观察假设，只能等待人工确认。"},
		},
	})
	if err != nil {
		t.Fatalf("save strategy patch: %v", err)
	}
	after, err := svc.GetStrategy(ctx, review.StrategyID)
	if err != nil {
		t.Fatalf("get strategy after patch: %v", err)
	}
	if after.Strategy.ActiveVersionID != before.Strategy.ActiveVersionID {
		t.Fatalf("active version changed from %s to %s", before.Strategy.ActiveVersionID, after.Strategy.ActiveVersionID)
	}
	if updated.Result["strategyPatchStatus"] != "pending_acceptance" {
		t.Fatalf("strategy patch status = %v, want pending_acceptance", updated.Result["strategyPatchStatus"])
	}
}

func guardrailsInputForTest(op ProposedOperation) ExecutionGuardrailsInput {
	now := time.Now()
	return ExecutionGuardrailsInput{
		Operation: op,
		Portfolio: StockV2Portfolio{
			ID:                   "portfolio-guardrails",
			Cash:                 7000,
			MaxSinglePositionPct: 20,
		},
		Snapshot: &PortfolioSnapshot{
			PortfolioID:        "portfolio-guardrails",
			ValuationAt:        now,
			Cash:               7000,
			HoldingMarketValue: 3000,
			TotalAssetValue:    10000,
			PositionCount:      1,
			Status:             PortfolioValuationStatusFresh,
		},
		Holdings: []StockV2Holding{{
			PortfolioID:       "portfolio-guardrails",
			Symbol:            "000977",
			Market:            "SZ",
			Quantity:          50,
			AvailableQuantity: 50,
			LastPrice:         60,
			MarketValue:       3000,
		}},
		Quote: &StockV2QuoteLatest{
			Symbol:    "000977",
			Market:    "SZ",
			LastPrice: 60,
			Status:    QuoteStatusFresh,
			QuoteAt:   now,
			FetchedAt: now,
			Source:    QuoteSourceTencent,
		},
	}
}

func assertGuardrailReason(t *testing.T, result ExecutionGuardrailsResult, status, code string) {
	t.Helper()
	if result.Status != status {
		t.Fatalf("guardrail status = %s, want %s; reasons = %+v", result.Status, status, result.Reasons)
	}
	for _, reason := range result.Reasons {
		if reason.Code == code {
			return
		}
	}
	t.Fatalf("reason %q not found in %+v", code, result.Reasons)
}
