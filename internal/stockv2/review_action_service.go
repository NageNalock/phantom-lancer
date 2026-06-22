package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	operationReviewAcceptanceAccepted = "accepted"
	operationReviewAcceptanceRejected = "rejected"
	operationReviewAcceptanceDeferred = "deferred"
)

func (s *Service) AcceptOperationReview(ctx context.Context, id string, req RequestApplyOperationReviewAction) (OperationReview, error) {
	review, err := s.completedActionableReview(ctx, id)
	if err != nil {
		return OperationReview{}, err
	}
	if reviewAcceptanceTerminal(review.Result) {
		return OperationReview{}, ErrInvalidOperationReviewAction
	}
	switch review.OutputType {
	case OperationReviewOutputProposedOperation:
		return s.acceptProposedOperationReview(ctx, review, req)
	case OperationReviewOutputStrategyPatch:
		return s.acceptStrategyPatchReview(ctx, review, req)
	default:
		return OperationReview{}, ErrInvalidOperationReviewOutputType
	}
}

func (s *Service) RejectOperationReview(ctx context.Context, id string, req RequestApplyOperationReviewAction) (OperationReview, error) {
	review, err := s.completedActionableReview(ctx, id)
	if err != nil {
		return OperationReview{}, err
	}
	if reviewAcceptanceTerminal(review.Result) {
		return OperationReview{}, ErrInvalidOperationReviewAction
	}
	return s.saveOperationReviewAcceptance(ctx, review, operationReviewAcceptanceRejected, req.Reason, map[string]any{}, AlertStatusIgnored)
}

func (s *Service) DeferOperationReview(ctx context.Context, id string, req RequestApplyOperationReviewAction) (OperationReview, error) {
	review, err := s.completedActionableReview(ctx, id)
	if err != nil {
		return OperationReview{}, err
	}
	if reviewAcceptanceTerminal(review.Result) {
		return OperationReview{}, ErrInvalidOperationReviewAction
	}
	return s.saveOperationReviewAcceptance(ctx, review, operationReviewAcceptanceDeferred, req.Reason, map[string]any{}, AlertStatusAcknowledged)
}

func (s *Service) completedActionableReview(ctx context.Context, id string) (OperationReview, error) {
	review, err := s.store.GetOperationReview(ctx, strings.TrimSpace(id))
	if err != nil {
		return OperationReview{}, err
	}
	if review.Status != OperationReviewStatusCompleted {
		return OperationReview{}, ErrInvalidOperationReviewStatus
	}
	if review.OutputType != OperationReviewOutputProposedOperation && review.OutputType != OperationReviewOutputStrategyPatch {
		return OperationReview{}, ErrInvalidOperationReviewOutputType
	}
	return review, nil
}

func (s *Service) acceptProposedOperationReview(ctx context.Context, review OperationReview, req RequestApplyOperationReviewAction) (OperationReview, error) {
	if strings.TrimSpace(review.PortfolioID) == "" {
		return OperationReview{}, ErrInvalidProposedOperation
	}
	status := reviewGuardrailsStatus(review.Result)
	if status == "" || status == ExecutionGuardrailsStatusBlocked {
		return OperationReview{}, ErrInvalidProposedOperation
	}
	if status != ExecutionGuardrailsStatusPass && status != ExecutionGuardrailsStatusDegraded {
		return OperationReview{}, ErrInvalidProposedOperation
	}

	op, err := proposedOperationFromReviewResult(review.Result, review)
	if err != nil {
		return OperationReview{}, err
	}
	if op.PortfolioID == "" {
		op.PortfolioID = review.PortfolioID
	}
	if op.Symbol == "" {
		op.Symbol = review.Symbol
	}
	if op.Market == "" {
		op.Market = firstNonEmpty(review.Market, inferAStockMarket(op.Symbol))
	}
	op = normalizeProposedOperation(op)

	side, err := transactionSideFromProposedOperation(op.Action)
	if err != nil {
		return OperationReview{}, err
	}
	price, err := s.priceForReviewOperation(ctx, review, op, req.Price)
	if err != nil {
		return OperationReview{}, err
	}
	quantity := req.Quantity
	if quantity <= 0 {
		quantity = op.Quantity
	}
	if quantity <= 0 && op.Amount > 0 && price > 0 {
		quantity = op.Amount / price
	}
	if quantity <= 0 {
		return OperationReview{}, ErrInvalidProposedOperation
	}
	op.Quantity = quantity
	op.Price = price
	op.Amount = quantity * price
	input, err := s.executionGuardrailsInput(ctx, review, op)
	if err != nil {
		return OperationReview{}, err
	}
	finalGuardrails := EvaluateExecutionGuardrails(input)
	if finalGuardrails.Status == ExecutionGuardrailsStatusBlocked {
		return OperationReview{}, ErrInvalidProposedOperation
	}

	txReq := RequestRecordTransaction{
		Symbol:     op.Symbol,
		Market:     op.Market,
		Name:       s.nameForReviewOperation(ctx, review, op),
		Side:       side,
		Quantity:   quantity,
		Price:      price,
		ExecutedAt: strings.TrimSpace(req.ExecutedAt),
		Note:       reviewTransactionNote(review, finalGuardrails.Status),
	}
	tx, err := s.RecordTransaction(ctx, op.PortfolioID, txReq)
	if err != nil {
		return OperationReview{}, err
	}
	return s.saveOperationReviewAcceptance(ctx, review, operationReviewAcceptanceAccepted, "", map[string]any{
		"transactionId": tx.Transaction.ID,
		"transaction":   tx.Transaction,
		"guardrails":    finalGuardrails,
	}, AlertStatusResolved)
}

func (s *Service) acceptStrategyPatchReview(ctx context.Context, review OperationReview, _ RequestApplyOperationReviewAction) (OperationReview, error) {
	if strings.TrimSpace(review.StrategyID) == "" {
		return OperationReview{}, ErrInvalidOperationReviewAction
	}
	strategy, err := s.store.GetStrategy(ctx, review.StrategyID)
	if err != nil {
		return OperationReview{}, err
	}
	if strategy.ActiveVersion == nil {
		return OperationReview{}, ErrStrategyVersionNotFound
	}
	patch := strategyPatchFromReviewResult(review.Result)
	if len(patch) == 0 {
		return OperationReview{}, ErrInvalidOperationReviewAction
	}

	next := *strategy.ActiveVersion
	applyStrategyPatch(&next, patch)
	if err := validateStrategyDirection(next.Direction); err != nil {
		return OperationReview{}, err
	}
	next.ID = ""
	next.StrategyID = strategy.Strategy.ID
	next.VersionNo = 0
	next.CreatedAt = time.Time{}
	next.CreatedBy = "review"
	if agentRun, err := s.latestCompletedAgentRunForReview(ctx, review.ID); err == nil && agentRun != nil {
		next.CreatedBy = "agent_review"
	}

	updated, err := s.store.UpdateStrategyWithVersion(ctx, strategy.Strategy, &next)
	if err != nil {
		return OperationReview{}, err
	}
	newVersionID := ""
	if updated.ActiveVersion != nil {
		newVersionID = updated.ActiveVersion.ID
	}
	return s.saveOperationReviewAcceptance(ctx, review, operationReviewAcceptanceAccepted, "", map[string]any{
		"newVersionId":        newVersionID,
		"strategyPatchStatus": "accepted",
	}, AlertStatusResolved)
}

func (s *Service) saveOperationReviewAcceptance(ctx context.Context, review OperationReview, status, reason string, extra map[string]any, alertStatus string) (OperationReview, error) {
	now := time.Now()
	result := copyStringAnyMap(review.Result)
	result["acceptanceStatus"] = status
	switch status {
	case operationReviewAcceptanceAccepted:
		result["acceptedAt"] = now
	case operationReviewAcceptanceRejected:
		result["rejectedAt"] = now
		result["rejectReason"] = strings.TrimSpace(reason)
		if review.OutputType == OperationReviewOutputStrategyPatch {
			result["strategyPatchStatus"] = "rejected"
		}
	case operationReviewAcceptanceDeferred:
		result["deferredAt"] = now
		result["deferReason"] = strings.TrimSpace(reason)
		if review.OutputType == OperationReviewOutputStrategyPatch {
			result["strategyPatchStatus"] = "deferred"
		}
	}
	for key, value := range extra {
		result[key] = value
	}
	review.Result = result
	updated, err := s.store.SaveOperationReviewResult(ctx, review)
	if err != nil {
		return OperationReview{}, err
	}
	if strings.TrimSpace(review.HitID) != "" {
		if err := s.store.UpdateMonitorHitStatus(ctx, review.HitID, MonitorHitStatusReviewed); err != nil {
			return OperationReview{}, err
		}
	}
	if alertStatus != "" {
		if err := s.updateReviewAlertStatus(ctx, review, alertStatus); err != nil {
			return OperationReview{}, err
		}
	}
	return updated, nil
}

func (s *Service) updateReviewAlertStatus(ctx context.Context, review OperationReview, status string) error {
	if strings.TrimSpace(review.HitID) == "" {
		return nil
	}
	hit, err := s.store.GetMonitorHit(ctx, review.HitID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hit.AlertID) == "" {
		return nil
	}
	_, err = s.setAlertStatus(ctx, hit.AlertID, status)
	if errors.Is(err, ErrAlertNotFound) {
		return nil
	}
	return err
}

func reviewGuardrailsStatus(result map[string]any) string {
	return firstRuleString(mapFromAny(result["guardrails"]), "status")
}

func reviewAcceptanceTerminal(result map[string]any) bool {
	status := firstRuleString(result, "acceptanceStatus")
	return status == operationReviewAcceptanceAccepted || status == operationReviewAcceptanceRejected
}

func transactionSideFromProposedOperation(action string) (string, error) {
	switch normalizeProposedOperationAction(action) {
	case ProposedOperationActionBuildPosition, ProposedOperationActionAddPosition:
		return "buy", nil
	case ProposedOperationActionReducePosition, ProposedOperationActionExitPosition:
		return "sell", nil
	default:
		return "", ErrInvalidProposedOperation
	}
}

func (s *Service) priceForReviewOperation(ctx context.Context, review OperationReview, op ProposedOperation, override float64) (float64, error) {
	if override > 0 {
		return override, nil
	}
	if op.Price > 0 {
		return op.Price, nil
	}
	if op.Symbol != "" {
		quotes, err := s.store.GetLatestQuotes(ctx, []string{op.Symbol})
		if err != nil {
			return 0, err
		}
		if len(quotes) > 0 && quotes[0].LastPrice > 0 {
			return quotes[0].LastPrice, nil
		}
	}
	raw := reviewOperationRaw(review.Result)
	if price := firstRuleNumber(raw, "priceBasis"); price > 0 {
		return price, nil
	}
	if price := numericTokenFromString(firstRuleString(raw, "priceBasis", "price_basis")); price > 0 {
		return price, nil
	}
	return 0, ErrInvalidProposedOperation
}

func (s *Service) nameForReviewOperation(ctx context.Context, review OperationReview, op ProposedOperation) string {
	raw := reviewOperationRaw(review.Result)
	if name := firstRuleString(raw, "name"); name != "" {
		return name
	}
	if op.Symbol == "" {
		return ""
	}
	instrument, err := s.store.GetInstrument(ctx, op.Symbol)
	if err == nil {
		return instrument.Name
	}
	return ""
}

func reviewOperationRaw(result map[string]any) map[string]any {
	if raw := mapFromAny(result["proposedOperation"]); len(raw) > 0 {
		return raw
	}
	if raw := mapFromAny(result["operation"]); len(raw) > 0 {
		return raw
	}
	return result
}

func reviewTransactionNote(review OperationReview, guardrailsStatus string) string {
	parts := []string{
		"source=operation_review",
		"reviewId=" + review.ID,
		"hitId=" + review.HitID,
		"guardrails=" + guardrailsStatus,
	}
	if summary := strings.TrimSpace(review.ResultSummary); summary != "" {
		parts = append(parts, "summary="+summary)
	}
	return strings.Join(parts, "; ")
}

func numericTokenFromString(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var b strings.Builder
	started := false
	for _, r := range value {
		if (r >= '0' && r <= '9') || r == '.' || (r == '-' && !started) {
			started = true
			b.WriteRune(r)
			continue
		}
		if started {
			break
		}
	}
	n, _ := strconv.ParseFloat(b.String(), 64)
	return n
}

func strategyPatchFromReviewResult(result map[string]any) map[string]any {
	if patch := mapFromAny(result["strategyPatch"]); len(patch) > 0 {
		return patch
	}
	if patch := mapFromAny(result["patch"]); len(patch) > 0 {
		return patch
	}
	return result
}

func applyStrategyPatch(version *StockV2StrategyVersion, patch map[string]any) {
	if title := firstRuleString(patch, "title"); title != "" {
		version.Title = title
	}
	if thesis := firstRuleString(patch, "thesis"); thesis != "" {
		version.Thesis = thesis
	}
	if direction := firstRuleString(patch, "direction"); direction != "" {
		version.Direction = direction
	}
	if entries, ok := stringListFromAny(patch["entryConditions"]); ok {
		version.EntryConditions = entries
	}
	if exits, ok := stringListFromAny(patch["exitConditions"]); ok {
		version.ExitConditions = exits
	}
	if notes := firstRuleString(patch, "riskNotes"); notes != "" {
		version.RiskNotes = notes
	}
	if meta := mapFromAny(patch["generationMeta"]); len(meta) > 0 {
		version.GenerationMeta = copyStringAnyMap(meta)
	}
}

func stringListFromAny(value any) ([]string, bool) {
	switch v := value.(type) {
	case []string:
		return cleanStringList(v), true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		fields := strings.FieldsFunc(v, func(r rune) bool {
			return r == '\n' || r == ',' || r == '，' || r == ';' || r == '；'
		})
		return cleanStringList(fields), true
	default:
		return nil, false
	}
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if v := strings.TrimSpace(value); v != "" {
			out = append(out, v)
		}
	}
	return out
}
