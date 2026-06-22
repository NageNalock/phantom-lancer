package stockv2

import (
	"context"
	"testing"
)

func TestAcceptProposedOperationRejectsBlockedGuardrails(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	saved, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputProposedOperation,
		ResultSummary: "现金不足的加仓提案",
		Result: map[string]any{"proposedOperation": map[string]any{
			"action": "add_position", "quantity": 200, "price": 61,
		}},
	})
	if err != nil {
		t.Fatalf("save proposed op: %v", err)
	}
	if got := reviewGuardrailsStatus(saved.Result); got != ExecutionGuardrailsStatusBlocked {
		t.Fatalf("guardrails status = %s, want blocked", got)
	}
	if _, err := svc.AcceptOperationReview(ctx, saved.ID, RequestApplyOperationReviewAction{}); err != ErrInvalidProposedOperation {
		t.Fatalf("accept error = %v, want ErrInvalidProposedOperation", err)
	}
}

func TestAcceptProposedOperationCreatesTransaction(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	portfolio, err := svc.store.GetPortfolio(ctx, hit.PortfolioID)
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	portfolio.MaxSinglePositionPct = 80
	if err := svc.store.UpdatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("update portfolio limit: %v", err)
	}
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	saved, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputProposedOperation,
		ResultSummary: "确认小额加仓",
		Result: map[string]any{"proposedOperation": map[string]any{
			"action": "add_position", "quantity": 10, "price": 61,
		}},
	})
	if err != nil {
		t.Fatalf("save proposed op: %v", err)
	}
	if got := reviewGuardrailsStatus(saved.Result); got != ExecutionGuardrailsStatusPass {
		t.Fatalf("guardrails status = %s, want pass", got)
	}
	accepted, err := svc.AcceptOperationReview(ctx, saved.ID, RequestApplyOperationReviewAction{})
	if err != nil {
		t.Fatalf("accept proposed op: %v", err)
	}
	if got := accepted.Result["acceptanceStatus"]; got != operationReviewAcceptanceAccepted {
		t.Fatalf("acceptanceStatus = %v, want accepted", got)
	}
	if accepted.Result["transactionId"] == "" {
		t.Fatalf("transactionId should be written: %+v", accepted.Result)
	}
	txs, err := svc.ListTransactions(ctx, hit.PortfolioID, 10)
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	if len(txs) != 1 || txs[0].Side != "buy" || txs[0].Quantity != 10 || txs[0].Price != 61 {
		t.Fatalf("transactions = %+v, want one buy", txs)
	}
	if _, err := svc.SaveOperationReviewResult(ctx, accepted.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputProposedOperation,
		ResultSummary: "尝试覆盖已确认结果",
		Result: map[string]any{"proposedOperation": map[string]any{
			"action": "add_position", "quantity": 5, "price": 61,
		}},
	}); err != ErrInvalidOperationReviewAction {
		t.Fatalf("save accepted review error = %v, want ErrInvalidOperationReviewAction", err)
	}
	if _, err := svc.AcceptOperationReview(ctx, saved.ID, RequestApplyOperationReviewAction{}); err != ErrInvalidOperationReviewAction {
		t.Fatalf("second accept error = %v, want ErrInvalidOperationReviewAction", err)
	}
	txs, err = svc.ListTransactions(ctx, hit.PortfolioID, 10)
	if err != nil {
		t.Fatalf("list txs after second accept: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("transactions after second accept = %+v, want still one", txs)
	}
	alerts, err := svc.ListAlerts(ctx, AlertListFilter{TaskType: MonitorTaskDataStrategyMonitor, Symbol: hit.Symbol, Limit: 10})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Status != AlertStatusResolved {
		t.Fatalf("alert = %+v, want resolved", alerts)
	}
}

func TestAcceptStrategyPatchCreatesNewVersion(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	before, err := svc.GetStrategy(ctx, hit.StrategyID)
	if err != nil {
		t.Fatalf("get strategy: %v", err)
	}
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	saved, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputStrategyPatch,
		ResultSummary: "调整策略 thesis",
		Result: map[string]any{"strategyPatch": map[string]any{
			"title":           "Review 策略 v2",
			"thesis":          "突破后等待回踩确认。",
			"direction":       StrategyBiasBullish,
			"entryConditions": []any{"放量突破", "回踩不破"},
			"exitConditions":  []any{"跌破支撑"},
			"riskNotes":       "假突破风险",
			"generationMeta":  map[string]any{"source": "review"},
		}},
	})
	if err != nil {
		t.Fatalf("save patch: %v", err)
	}
	accepted, err := svc.AcceptOperationReview(ctx, saved.ID, RequestApplyOperationReviewAction{})
	if err != nil {
		t.Fatalf("accept patch: %v", err)
	}
	if got := accepted.Result["acceptanceStatus"]; got != operationReviewAcceptanceAccepted {
		t.Fatalf("acceptanceStatus = %v, want accepted", got)
	}
	after, err := svc.GetStrategy(ctx, hit.StrategyID)
	if err != nil {
		t.Fatalf("reload strategy: %v", err)
	}
	if after.Strategy.ActiveVersionID == before.Strategy.ActiveVersionID {
		t.Fatalf("active version id unchanged: %s", after.Strategy.ActiveVersionID)
	}
	if after.ActiveVersion == nil || after.ActiveVersion.Thesis != "突破后等待回踩确认。" {
		t.Fatalf("active version = %+v, want patched thesis", after.ActiveVersion)
	}
	if accepted.Result["newVersionId"] != after.Strategy.ActiveVersionID {
		t.Fatalf("newVersionId = %v, active = %s", accepted.Result["newVersionId"], after.Strategy.ActiveVersionID)
	}
}

func TestRejectProposedOperationDoesNotCreateTransaction(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	portfolio, err := svc.store.GetPortfolio(ctx, hit.PortfolioID)
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	portfolio.MaxSinglePositionPct = 80
	if err := svc.store.UpdatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("update portfolio limit: %v", err)
	}
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	saved, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputProposedOperation,
		ResultSummary: "暂不加仓",
		Result: map[string]any{"proposedOperation": map[string]any{
			"action": "add_position", "quantity": 10, "price": 61,
		}},
	})
	if err != nil {
		t.Fatalf("save proposed op: %v", err)
	}
	rejected, err := svc.RejectOperationReview(ctx, saved.ID, RequestApplyOperationReviewAction{Reason: "人工不同意"})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got := rejected.Result["acceptanceStatus"]; got != operationReviewAcceptanceRejected {
		t.Fatalf("acceptanceStatus = %v, want rejected", got)
	}
	if _, err := svc.SaveOperationReviewResult(ctx, rejected.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputProposedOperation,
		ResultSummary: "尝试覆盖已作废结果",
		Result: map[string]any{"proposedOperation": map[string]any{
			"action": "add_position", "quantity": 5, "price": 61,
		}},
	}); err != ErrInvalidOperationReviewAction {
		t.Fatalf("save rejected review error = %v, want ErrInvalidOperationReviewAction", err)
	}
	txs, err := svc.ListTransactions(ctx, hit.PortfolioID, 10)
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("transactions = %+v, want none", txs)
	}
}

func TestDeferStrategyPatchDoesNotCreateVersion(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	hit := seedReviewMonitorHit(t, svc)
	before, err := svc.GetStrategy(ctx, hit.StrategyID)
	if err != nil {
		t.Fatalf("get strategy: %v", err)
	}
	review, err := svc.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		t.Fatalf("create patch review: %v", err)
	}
	saved, err := svc.SaveOperationReviewResult(ctx, review.ID, RequestSaveOperationReviewResult{
		OutputType:    OperationReviewOutputStrategyPatch,
		ResultSummary: "补丁延后",
		Result: map[string]any{"strategyPatch": map[string]any{
			"thesis": "延后观察的新 thesis",
		}},
	})
	if err != nil {
		t.Fatalf("save patch: %v", err)
	}
	deferred, err := svc.DeferOperationReview(ctx, saved.ID, RequestApplyOperationReviewAction{Reason: "等待收盘"})
	if err != nil {
		t.Fatalf("defer: %v", err)
	}
	if got := deferred.Result["acceptanceStatus"]; got != operationReviewAcceptanceDeferred {
		t.Fatalf("acceptanceStatus = %v, want deferred", got)
	}
	after, err := svc.GetStrategy(ctx, hit.StrategyID)
	if err != nil {
		t.Fatalf("reload strategy: %v", err)
	}
	if after.Strategy.ActiveVersionID != before.Strategy.ActiveVersionID {
		t.Fatalf("active version changed on defer: before %s after %s", before.Strategy.ActiveVersionID, after.Strategy.ActiveVersionID)
	}
}
