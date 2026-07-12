package stockv2

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Service) CreateReviewFromMonitorHit(ctx context.Context, hitID string) (OperationReview, error) {
	hit, err := s.store.GetMonitorHit(ctx, strings.TrimSpace(hitID))
	if err != nil {
		return OperationReview{}, err
	}
	if active, err := s.store.GetActiveOperationReviewByHit(ctx, hit.ID); err != nil {
		return OperationReview{}, err
	} else if active != nil {
		return *active, nil
	}

	pack, err := s.BuildAgentContextPack(ctx, hit)
	if err != nil {
		return OperationReview{}, err
	}
	now := time.Now()
	strategyID, portfolioID, symbol, market := reviewLinkage(hit, pack)
	review, err := s.store.CreateOperationReview(ctx, OperationReview{
		ID:           generateID(),
		HitID:        hit.ID,
		RunID:        hit.RunID,
		Status:       OperationReviewStatusPending,
		StrategyID:   strategyID,
		PortfolioID:  portfolioID,
		Symbol:       symbol,
		Market:       market,
		InputContext: pack,
		Result:       map[string]any{},
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		if active, findErr := s.store.GetActiveOperationReviewByHit(ctx, hit.ID); findErr == nil && active != nil {
			return *active, nil
		}
		return OperationReview{}, err
	}
	if err := s.store.IncrementMonitorRunReviewCount(ctx, hit.RunID); err != nil {
		return OperationReview{}, err
	}
	return review, nil
}

func reviewLinkage(hit MonitorHit, pack AgentContextPack) (strategyID, portfolioID, symbol, market string) {
	strategyID = strings.TrimSpace(hit.StrategyID)
	portfolioID = strings.TrimSpace(hit.PortfolioID)
	symbol = strings.TrimSpace(hit.Symbol)
	market = strings.TrimSpace(hit.Market)
	if pack.Strategy != nil {
		if strategyID == "" {
			strategyID = pack.Strategy.Strategy.ID
		}
		if portfolioID == "" {
			portfolioID = pack.Strategy.Strategy.PortfolioID
		}
		if symbol == "" {
			symbol = pack.Strategy.Strategy.Symbol
		}
		if market == "" {
			market = pack.Strategy.Strategy.Market
		}
	}
	if pack.Portfolio != nil && portfolioID == "" {
		portfolioID = pack.Portfolio.Portfolio.ID
	}
	if pack.Quote != nil {
		if symbol == "" {
			symbol = pack.Quote.Symbol
		}
		if market == "" {
			market = pack.Quote.Market
		}
	}
	return strategyID, portfolioID, symbol, market
}

func (s *Service) BuildAgentContextPack(ctx context.Context, hit MonitorHit) (AgentContextPack, error) {
	freshness := map[string]any{}
	pack := AgentContextPack{
		BuiltAt:   time.Now(),
		Hit:       hit,
		Evidence:  copyStringAnyMap(hit.Evidence),
		Freshness: freshness,
	}
	symbol := strings.TrimSpace(hit.Symbol)
	portfolioID := strings.TrimSpace(hit.PortfolioID)

	if candidateID := stringFromAny(pack.Evidence["candidate_id"]); candidateID != "" {
		candidate, err := s.store.GetNewsLinkCandidate(ctx, candidateID)
		if err == nil {
			pack.NewsLink = &candidate
			if symbol == "" {
				symbol = candidate.Symbol
			}
		} else if errors.Is(err, ErrNewsLinkCandidateNotFound) {
			freshness["newsLinkCandidate"] = map[string]any{"status": "missing"}
		} else {
			return AgentContextPack{}, err
		}
	}
	newsEventID := stringFromAny(pack.Evidence["news_event_id"])
	if newsEventID == "" && pack.NewsLink != nil {
		newsEventID = pack.NewsLink.NewsEventID
	}
	if newsEventID != "" {
		event, err := s.store.GetNewsEvent(ctx, newsEventID)
		if err == nil {
			pack.NewsEvent = &event
			freshness["newsEvent"] = map[string]any{
				"status":        "present",
				"source":        event.Source,
				"eventAt":       event.EventAt,
				"qualityStatus": event.QualityStatus,
			}
		} else if errors.Is(err, ErrNewsEventNotFound) {
			freshness["newsEvent"] = map[string]any{"status": "missing"}
		} else {
			return AgentContextPack{}, err
		}
	}

	if hit.StrategyID != "" {
		if strategy, err := s.store.GetStrategy(ctx, hit.StrategyID); err == nil {
			pack.Strategy = &strategy
			if symbol == "" {
				symbol = strategy.Strategy.Symbol
			}
			if portfolioID == "" {
				portfolioID = strategy.Strategy.PortfolioID
			}
		} else if errors.Is(err, ErrStrategyNotFound) {
			freshness["strategy"] = map[string]any{"status": "missing"}
		} else {
			return AgentContextPack{}, err
		}
	}

	if symbol != "" {
		if quotes, err := s.store.GetLatestQuotes(ctx, []string{symbol}); err == nil {
			if len(quotes) > 0 {
				quote := quotes[0]
				pack.Quote = &quote
				freshness["quote"] = quoteFreshnessSummary(quote)
			} else {
				freshness["quote"] = map[string]any{"status": "missing"}
			}
		} else {
			return AgentContextPack{}, err
		}
		pack.DailyBars = s.buildDailyBarsContext(ctx, symbol)
		freshness["dailyBars"] = dailyBarsFreshnessSummary(pack.DailyBars)
		pack.MinuteBars = s.buildMinuteBarsContext(ctx, symbol)
		freshness["minuteBars"] = minuteBarsFreshnessSummary(pack.MinuteBars)
		if profile, err := s.store.GetStockProfile(ctx, symbol); err == nil {
			pack.Profile = &profile
			freshness["stockProfile"] = stockProfileFreshnessSummary(profile, time.Now())
		} else if errors.Is(err, ErrStockProfileNotFound) {
			freshness["stockProfile"] = map[string]any{"status": "missing", "ready": false}
		} else {
			return AgentContextPack{}, err
		}
	}

	if portfolioID != "" {
		portfolioCtx, status, err := s.buildPortfolioReviewContext(ctx, portfolioID)
		if err != nil {
			return AgentContextPack{}, err
		}
		pack.Portfolio = portfolioCtx
		freshness["portfolio"] = status
	}
	if symbol != "" {
		normalizedSymbols, err := NormalizeAssetReadinessSymbols([]string{symbol})
		if err != nil {
			return AgentContextPack{}, err
		}
		readinessBySymbol, err := s.EvaluateAssetReadinessBatch(ctx, normalizedSymbols, pack.BuiltAt)
		if err != nil {
			return AgentContextPack{}, err
		}
		readiness := readinessBySymbol[normalizedSymbols[0]]
		decision, err := DecideAssetReadiness(
			[]UnifiedAssetReadiness{readiness},
			AssetReadinessRequirementAnalysis,
			AssetReadinessModeAllowDegraded,
		)
		if err != nil {
			return AgentContextPack{}, err
		}
		// Operation reviews are risk-monitoring continuations: they stay available
		// in explicitly degraded mode, and the persisted input records every reason.
		freshness["assetReadiness"] = readiness
		freshness["assetReadinessDecision"] = decision
	}

	return pack, nil
}

func (s *Service) GetOperationReview(ctx context.Context, id string) (OperationReview, error) {
	return s.store.GetOperationReview(ctx, id)
}

func (s *Service) ListOperationReviews(ctx context.Context, filter OperationReviewListFilter) ([]OperationReview, error) {
	return s.store.ListOperationReviews(ctx, filter)
}

func (s *Service) CountOperationReviews(ctx context.Context, filter OperationReviewListFilter) (int, error) {
	return s.store.CountOperationReviews(ctx, filter)
}

func (s *Service) SaveOperationReviewResult(ctx context.Context, id string, req RequestSaveOperationReviewResult) (OperationReview, error) {
	return s.saveOperationReviewResult(ctx, id, req, nil)
}

func (s *Service) saveOperationReviewResult(ctx context.Context, id string, req RequestSaveOperationReviewResult, agentRun *AgentRun) (OperationReview, error) {
	current, err := s.store.GetOperationReview(ctx, id)
	if err != nil {
		return OperationReview{}, err
	}
	if reviewAcceptanceTerminal(current.Result) {
		return OperationReview{}, ErrInvalidOperationReviewAction
	}
	status, outputType, err := normalizeOperationReviewResult(req)
	if err != nil {
		return OperationReview{}, err
	}

	now := time.Now()
	current.Status = status
	current.OutputType = outputType
	result := req.Result
	if result == nil {
		result = map[string]any{}
	}
	switch outputType {
	case OperationReviewOutputProposedOperation:
		result, err = s.applyProposedOperationGuardrails(ctx, current, result)
		if err != nil {
			return OperationReview{}, err
		}
	case OperationReviewOutputStrategyPatch:
		result = reviewResultWithPendingStrategyPatch(result)
	}
	current.Result = result
	current.ResultSummary = strings.TrimSpace(req.ResultSummary)
	current.ErrorMessage = strings.TrimSpace(req.ErrorMessage)
	if status == OperationReviewStatusCompleted || status == OperationReviewStatusFailed {
		current.CompletedAt = now
	}
	if status == OperationReviewStatusClosed {
		current.ClosedAt = now
		if current.CompletedAt.IsZero() {
			current.CompletedAt = now
		}
	}

	updated, err := s.store.SaveOperationReviewResult(ctx, current)
	if err != nil {
		return OperationReview{}, err
	}
	if status == OperationReviewStatusCompleted || status == OperationReviewStatusClosed {
		hitStatus := MonitorHitStatusReviewed
		if outputType == OperationReviewOutputIgnore {
			hitStatus = MonitorHitStatusIgnored
		}
		if err := s.store.UpdateMonitorHitStatus(ctx, current.HitID, hitStatus); err != nil {
			return OperationReview{}, err
		}
		if err := s.syncMonitorAlertForReviewResult(ctx, updated, agentRun); err != nil {
			return OperationReview{}, err
		}
	}
	return updated, nil
}

func (s *Service) syncMonitorAlertForReviewResult(ctx context.Context, review OperationReview, agentRun *AgentRun) error {
	if strings.TrimSpace(review.HitID) == "" ||
		(review.Status != OperationReviewStatusCompleted && review.Status != OperationReviewStatusClosed) {
		return nil
	}
	hit, err := s.store.GetMonitorHit(ctx, review.HitID)
	if err != nil {
		return err
	}
	if operationReviewOutputTriggersAlert(review.OutputType) {
		cfg, err := s.monitorAlertTaskConfig(ctx, hit.TaskType)
		if err != nil {
			return err
		}
		if agentRun == nil {
			agentRun, _ = s.latestCompletedAgentRunForReview(ctx, review.ID)
		}
		triggerSource := AlertTriggerSourceManualReviewConfirmed
		if agentRun != nil {
			triggerSource = AlertTriggerSourceAgentConfirmed
		}
		evidence := copyStringAnyMap(hit.Evidence)
		evidence["reviewOutputType"] = review.OutputType
		evidence["reviewResultSummary"] = review.ResultSummary
		_, _, err = s.upsertMonitorAlert(ctx, hit, cfg, review, agentRun, triggerSource, "", evidence)
		if err != nil {
			return err
		}
		if hit.AlertID == "" {
			if err := s.store.IncrementMonitorRunAlertCount(ctx, hit.RunID); err != nil {
				return err
			}
		}
		if err := s.store.UpdateMonitorHitStatus(ctx, hit.ID, MonitorHitStatusReviewed); err != nil {
			return err
		}
		return nil
	}
	return s.suppressMonitorAlertForReviewResult(ctx, hit, review)
}

func operationReviewOutputTriggersAlert(outputType string) bool {
	return outputType == OperationReviewOutputTradeSignal ||
		outputType == OperationReviewOutputProposedOperation ||
		outputType == OperationReviewOutputStrategyPatch
}

func (s *Service) suppressMonitorAlertForReviewResult(ctx context.Context, hit MonitorHit, review OperationReview) error {
	if !operationReviewOutputSuppressesAlert(review.OutputType) || strings.TrimSpace(hit.AlertID) == "" {
		return nil
	}
	alert, err := s.store.GetAlert(ctx, hit.AlertID)
	if err != nil {
		if errors.Is(err, ErrAlertNotFound) {
			return nil
		}
		return err
	}
	now := time.Now()
	if review.OutputType == OperationReviewOutputIgnore {
		alert.Status = AlertStatusIgnored
		alert.AcknowledgedAt = now
	} else {
		alert.Status = AlertStatusResolved
		alert.ResolvedAt = now
	}
	alert.ReviewStatus = review.Status
	if alert.Evidence == nil {
		alert.Evidence = map[string]any{}
	}
	alert.Evidence["reviewOutputType"] = review.OutputType
	alert.Evidence["reviewResultSummary"] = review.ResultSummary
	alert.Evidence["trigger_decision"] = review.OutputType
	_, err = s.store.UpdateAlert(ctx, alert)
	return err
}

func operationReviewOutputSuppressesAlert(outputType string) bool {
	return outputType == OperationReviewOutputIgnore || outputType == OperationReviewOutputContinueMonitoring
}

func (s *Service) monitorAlertTaskConfig(ctx context.Context, taskType string) (MonitorTaskConfig, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorTaskConfig{}, ErrInvalidMonitorTaskType
	}
	cfg, err := s.store.GetMonitorTaskConfig(ctx, taskType)
	if err != nil {
		if errors.Is(err, ErrMonitorTaskNotFound) {
			return def.DefaultConfig, nil
		}
		return MonitorTaskConfig{}, err
	}
	return cfg, nil
}

func (s *Service) latestCompletedAgentRunForReview(ctx context.Context, reviewID string) (*AgentRun, error) {
	runs, err := s.store.ListAgentRuns(ctx, AgentRunListFilter{
		TaskType:          AgentTaskTypeOperationReview,
		Status:            AgentRunStatusCompleted,
		TriggerObjectType: "operation_review",
		TriggerObjectID:   reviewID,
		Limit:             1,
	})
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

func (s *Service) applyProposedOperationGuardrails(ctx context.Context, review OperationReview, result map[string]any) (map[string]any, error) {
	op, err := proposedOperationFromReviewResult(result, review)
	if err != nil {
		return nil, err
	}
	input, err := s.executionGuardrailsInput(ctx, review, op)
	if err != nil {
		return nil, err
	}
	guardrails := EvaluateExecutionGuardrails(input)
	next := copyStringAnyMap(result)
	next["proposedOperation"] = input.Operation
	next["guardrails"] = guardrails
	switch guardrails.Status {
	case ExecutionGuardrailsStatusBlocked:
		next["acceptanceStatus"] = "blocked"
	case ExecutionGuardrailsStatusDegraded:
		next["acceptanceStatus"] = "pending_guardrail_review"
	default:
		next["acceptanceStatus"] = "pending_confirmation"
	}
	return next, nil
}

func (s *Service) executionGuardrailsInput(ctx context.Context, review OperationReview, op ProposedOperation) (ExecutionGuardrailsInput, error) {
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

	input := ExecutionGuardrailsInput{Operation: op}
	if op.PortfolioID != "" {
		if portfolio, err := s.store.GetPortfolio(ctx, op.PortfolioID); err == nil {
			input.Portfolio = portfolio
			if holdings, err := s.store.ListHoldings(ctx, op.PortfolioID); err == nil {
				input.Holdings = holdings
			} else {
				return input, err
			}
			if snapshots, err := s.store.GetPortfolioSnapshots(ctx, op.PortfolioID, 1); err == nil && len(snapshots) > 0 {
				input.Snapshot = &snapshots[0]
			} else if err != nil {
				return input, err
			}
		} else if !errors.Is(err, ErrPortfolioNotFound) {
			return input, err
		}
	}
	if op.Symbol != "" {
		quotes, err := s.store.GetLatestQuotes(ctx, []string{op.Symbol})
		if err != nil {
			return input, err
		}
		if len(quotes) > 0 {
			input.Quote = &quotes[0]
		}
	}
	return input, nil
}

func proposedOperationFromReviewResult(result map[string]any, review OperationReview) (ProposedOperation, error) {
	raw := mapFromAny(result["proposedOperation"])
	if len(raw) == 0 {
		raw = mapFromAny(result["operation"])
	}
	if len(raw) == 0 {
		raw = result
	}
	op := ProposedOperation{
		Action:      firstRuleString(raw, "action", "operation", "type"),
		PortfolioID: firstNonEmpty(firstRuleString(raw, "portfolioId", "portfolioID"), review.PortfolioID),
		Symbol:      firstNonEmpty(firstRuleString(raw, "symbol"), review.Symbol),
		Market:      firstNonEmpty(firstRuleString(raw, "market"), review.Market),
		Quantity:    firstRuleNumber(raw, "quantity", "shares"),
		Amount:      firstRuleNumber(raw, "amount", "notional"),
		Price:       firstRuleNumber(raw, "price", "limitPrice", "estimatedPrice"),
	}
	op = normalizeProposedOperation(op)
	if op.Action == "" {
		return ProposedOperation{}, ErrInvalidProposedOperation
	}
	return op, nil
}

func reviewResultWithPendingStrategyPatch(result map[string]any) map[string]any {
	next := copyStringAnyMap(result)
	if _, ok := next["acceptanceStatus"]; !ok {
		next["acceptanceStatus"] = "pending"
	}
	if _, ok := next["strategyPatchStatus"]; !ok {
		next["strategyPatchStatus"] = "pending_acceptance"
	}
	return next
}

func (s *Service) buildDailyBarsContext(ctx context.Context, symbol string) *DailyBarsContext {
	bars, err := s.store.GetDailyBars(ctx, symbol, DailyBarAdjustedNone, "", "", 20)
	if err != nil || len(bars) == 0 {
		return &DailyBarsContext{Symbol: symbol, Adjusted: DailyBarAdjustedNone}
	}
	latest := bars[len(bars)-1]
	summary := map[string]float64{
		"latestClose": latest.Close,
	}
	high := bars[0].High
	low := bars[0].Low
	for _, bar := range bars {
		if bar.High > high {
			high = bar.High
		}
		if bar.Low < low {
			low = bar.Low
		}
	}
	summary["rangeHigh"] = high
	summary["rangeLow"] = low
	return &DailyBarsContext{
		Symbol:          symbol,
		Adjusted:        DailyBarAdjustedNone,
		Count:           len(bars),
		LatestTradeDate: latest.TradeDate,
		LatestClose:     latest.Close,
		LatestFetchedAt: latest.FetchedAt,
		Quality:         latest.Quality,
		Summary:         summary,
	}
}

func (s *Service) buildMinuteBarsContext(ctx context.Context, symbol string) *MinuteBarsContext {
	bars, err := s.store.ListMinuteBars(ctx, symbol, time.Now().AddDate(0, 0, -5), 1200)
	if err != nil || len(bars) == 0 {
		return &MinuteBarsContext{Symbol: symbol}
	}
	latest := bars[len(bars)-1]
	high := bars[0].High
	low := bars[0].Low
	totalVolume := 0.0
	totalNetInflow := 0.0
	for _, bar := range bars {
		if bar.High > high {
			high = bar.High
		}
		if bar.Low < low {
			low = bar.Low
		}
		totalVolume += bar.Volume
		totalNetInflow += bar.MainNetInflow
	}
	return &MinuteBarsContext{
		Symbol:          symbol,
		Count:           len(bars),
		LatestMinuteAt:  latest.MinuteAt,
		LatestClose:     latest.Close,
		LatestVolume:    latest.Volume,
		LatestNetInflow: latest.MainNetInflow,
		Source:          latest.Source,
		Summary: map[string]float64{
			"rangeHigh":       high,
			"rangeLow":        low,
			"totalVolume":     totalVolume,
			"totalNetInflow":  totalNetInflow,
			"latestPctChange": latest.PctChange,
		},
	}
}

func (s *Service) buildPortfolioReviewContext(ctx context.Context, portfolioID string) (*PortfolioReviewContext, map[string]any, error) {
	portfolio, err := s.store.GetPortfolio(ctx, portfolioID)
	if err != nil {
		if errors.Is(err, ErrPortfolioNotFound) {
			return nil, map[string]any{"status": "missing"}, nil
		}
		return nil, nil, err
	}
	out := &PortfolioReviewContext{Portfolio: portfolio}
	status := map[string]any{"status": "present"}
	if snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolioID, 1); err == nil && len(snapshots) > 0 {
		snapshot := snapshots[0]
		out.Snapshot = &snapshot
		status = map[string]any{
			"status":              snapshot.Status,
			"valuationAt":         snapshot.ValuationAt,
			"staleQuoteCount":     snapshot.StaleQuoteCount,
			"estimatedQuoteCount": snapshot.EstimatedQuoteCount,
		}
	} else if err != nil {
		return nil, nil, err
	} else {
		status["snapshot"] = "missing"
	}
	if holdings, err := s.store.ListHoldings(ctx, portfolioID); err == nil {
		out.Holdings = holdings
	} else {
		return nil, nil, err
	}
	return out, status, nil
}

func normalizeOperationReviewResult(req RequestSaveOperationReviewResult) (string, string, error) {
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = OperationReviewStatusCompleted
	}
	if !validOperationReviewStatus(status) {
		return "", "", ErrInvalidOperationReviewStatus
	}
	outputType := strings.TrimSpace(req.OutputType)
	if outputType == "" && status == OperationReviewStatusCompleted {
		return "", "", ErrInvalidOperationReviewOutputType
	}
	if outputType != "" && !validOperationReviewOutputType(outputType) {
		return "", "", ErrInvalidOperationReviewOutputType
	}
	return status, outputType, nil
}

func validOperationReviewStatus(status string) bool {
	return status == OperationReviewStatusPending ||
		status == OperationReviewStatusRunning ||
		status == OperationReviewStatusCompleted ||
		status == OperationReviewStatusFailed ||
		status == OperationReviewStatusClosed
}

func validOperationReviewOutputType(outputType string) bool {
	return outputType == OperationReviewOutputTradeSignal ||
		outputType == OperationReviewOutputProposedOperation ||
		outputType == OperationReviewOutputStrategyPatch ||
		outputType == OperationReviewOutputIgnore ||
		outputType == OperationReviewOutputContinueMonitoring
}

func copyStringAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func quoteFreshnessSummary(quote StockV2QuoteLatest) map[string]any {
	return map[string]any{
		"status":    quote.Status,
		"source":    quote.Source,
		"quoteAt":   quote.QuoteAt,
		"fetchedAt": quote.FetchedAt,
	}
}

func dailyBarsFreshnessSummary(ctx *DailyBarsContext) map[string]any {
	if ctx == nil || ctx.Count == 0 {
		return map[string]any{"status": "missing"}
	}
	status := ctx.Quality
	if status == "" {
		status = "present"
	}
	return map[string]any{
		"status":          status,
		"latestTradeDate": ctx.LatestTradeDate,
		"latestFetchedAt": ctx.LatestFetchedAt,
		"count":           ctx.Count,
	}
}

func minuteBarsFreshnessSummary(ctx *MinuteBarsContext) map[string]any {
	if ctx == nil || ctx.Count == 0 {
		return map[string]any{"status": "missing"}
	}
	return map[string]any{
		"status":         "present",
		"latestMinuteAt": ctx.LatestMinuteAt,
		"count":          ctx.Count,
		"source":         ctx.Source,
	}
}
