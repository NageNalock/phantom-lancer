package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) RunStrategyGeneration(ctx context.Context, input StrategyGenerationInput) (AgentRun, error) {
	normalized, err := normalizeStrategyGenerationInput(input)
	if err != nil {
		return AgentRun{}, err
	}
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeStrategyGeneration)
	if err != nil {
		return AgentRun{}, err
	}
	model, err := s.resolveModel(ctx, taskProfile)
	if err != nil {
		return AgentRun{}, err
	}
	genCtx, err := s.BuildStrategyGenerationContext(ctx, normalized)
	if err != nil {
		return AgentRun{}, err
	}
	artifact, _ := json.Marshal(genCtx)
	triggerID := strategyGenerationTriggerID(normalized)
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeStrategyGeneration,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		ReasoningEffort:      taskProfile.ReasoningEffort,
		TriggerObjectType:    "strategy_generation",
		TriggerObjectID:      triggerID,
		RequestedBy:          normalized.RequestedBy,
		InputSummary:         strategyGenerationInputSummary(normalized),
		InputArtifactSummary: string(artifact),
	})
	if err != nil {
		return AgentRun{}, err
	}
	if s.agentExecutor != nil {
		go s.startStrategyGenerationRunAsync(context.Background(), run, ledger, genCtx, model.ModelName)
	}
	return run, nil
}

func (s *Service) BuildStrategyGenerationContext(ctx context.Context, input StrategyGenerationInput) (StrategyGenerationContext, error) {
	normalized, err := normalizeStrategyGenerationInput(input)
	if err != nil {
		return StrategyGenerationContext{}, err
	}
	out := StrategyGenerationContext{
		BuiltAt:          time.Now(),
		Input:            normalized,
		Mode:             normalized.Mode,
		Targets:          make([]StrategyGenerationInstrumentContext, 0, len(normalized.TargetInstruments)),
		FreshnessSummary: map[string]any{},
	}
	s.fillStrategyGenerationEmbeddingStatus(ctx, &out)
	if normalized.Mode == StrategyGenerationModePortfolio {
		out, err = s.buildPortfolioStrategyGenerationContext(ctx, normalized, out)
		if err != nil {
			return StrategyGenerationContext{}, err
		}
		if err := s.fillStrategyGenerationDecisionGates(ctx, &out); err != nil {
			return StrategyGenerationContext{}, err
		}
		return out, nil
	}
	for _, target := range normalized.TargetInstruments {
		item, err := s.strategyGenerationInstrumentContext(ctx, target)
		if err != nil {
			return StrategyGenerationContext{}, err
		}
		out.Targets = append(out.Targets, item)
	}
	if err := s.fillStrategyGenerationOpportunityContext(ctx, normalized, &out); err != nil {
		return StrategyGenerationContext{}, err
	}
	if err := s.fillStrategyGenerationDecisionGates(ctx, &out); err != nil {
		return StrategyGenerationContext{}, err
	}
	out.FreshnessSummary["targetCount"] = len(out.Targets)
	out.FreshnessSummary["builtAt"] = out.BuiltAt.Format(time.RFC3339)
	return out, nil
}

func (s *Service) fillStrategyGenerationEmbeddingStatus(ctx context.Context, out *StrategyGenerationContext) {
	status, err := s.GetEmbeddingStatus(ctx)
	if err != nil {
		out.FreshnessSummary["embeddingStatus"] = map[string]any{
			"available": false,
			"error":     safelog.Text(err.Error(), 240),
		}
		return
	}
	out.EmbeddingStatus = &status
	out.FreshnessSummary["embeddingStatus"] = map[string]any{
		"available": status.Available,
		"status":    status.Status,
		"modelId":   status.ModelID,
		"ready":     status.ReadyAssetCount,
		"missing":   status.MissingAssetCount,
		"stale":     status.StaleAssetCount,
		"failed":    status.FailedAssetCount,
	}
}

func (s *Service) fillStrategyGenerationOpportunityContext(ctx context.Context, input StrategyGenerationInput, out *StrategyGenerationContext) error {
	if strings.TrimSpace(input.OpportunityID) == "" && strings.TrimSpace(input.CandidateID) == "" && len(input.CandidateIDs) == 0 {
		return nil
	}
	if strings.TrimSpace(input.OpportunityID) != "" {
		opp, err := s.store.GetOpportunity(ctx, input.OpportunityID)
		if err != nil {
			return err
		}
		out.Opportunity = &opp
	}
	candidateIDs := append([]string(nil), input.CandidateIDs...)
	if len(candidateIDs) == 0 && strings.TrimSpace(input.CandidateID) != "" {
		candidateIDs = []string{input.CandidateID}
	}
	if len(candidateIDs) > 0 {
		out.OpportunityEvidenceByCandidate = map[string][]OpportunityEvidence{}
		for _, candidateID := range candidateIDs {
			candidate, err := s.store.GetOpportunityCandidate(ctx, candidateID)
			if err != nil {
				return err
			}
			if input.OpportunityID != "" && candidate.OpportunityID != input.OpportunityID {
				return ErrInvalidStrategyGenerationInput
			}
			out.OpportunityCandidates = append(out.OpportunityCandidates, candidate)
			evidence, err := s.store.ListOpportunityEvidence(ctx, OpportunityEvidenceListFilter{
				RunID: candidate.RunID, CandidateID: candidate.ID, Limit: 100,
			})
			if err != nil {
				return err
			}
			out.OpportunityEvidenceByCandidate[candidate.ID] = evidence
			out.OpportunityEvidence = append(out.OpportunityEvidence, evidence...)
			if out.Opportunity == nil {
				opp, err := s.store.GetOpportunity(ctx, candidate.OpportunityID)
				if err != nil {
					return err
				}
				out.Opportunity = &opp
			}
		}
		candidate := out.OpportunityCandidates[0]
		out.OpportunityCandidate = &candidate
		out.FreshnessSummary["opportunityEvidenceCount"] = len(out.OpportunityEvidence)
		out.FreshnessSummary["opportunityCandidateCount"] = len(out.OpportunityCandidates)
	}
	if out.Opportunity != nil {
		out.FreshnessSummary["opportunityId"] = out.Opportunity.ID
	}
	if out.OpportunityCandidate != nil {
		out.FreshnessSummary["opportunityCandidateId"] = out.OpportunityCandidate.ID
	}
	return nil
}

func (s *Service) strategyGenerationInstrumentContext(ctx context.Context, target StrategyGenerationTargetInstrument) (StrategyGenerationInstrumentContext, error) {
	symbol := strings.TrimSpace(target.Symbol)
	instrument, err := s.store.GetInstrument(ctx, symbol)
	if err != nil {
		return StrategyGenerationInstrumentContext{}, err
	}
	item := StrategyGenerationInstrumentContext{
		Instrument:    &instrument,
		DataFreshness: map[string]any{"symbol": instrument.Symbol},
	}
	if quotes, err := s.store.GetLatestQuotes(ctx, []string{instrument.Symbol}); err == nil && len(quotes) > 0 {
		quote := quotes[0]
		item.LatestQuote = &quote
		item.DataFreshness["latestQuoteAt"] = quote.QuoteAt.Format(time.RFC3339)
		item.DataFreshness["latestQuoteFetchedAt"] = quote.FetchedAt.Format(time.RFC3339)
		item.DataFreshness["latestQuoteStatus"] = quote.Status
	} else {
		item.DataFreshness["latestQuoteMissing"] = true
	}
	dailyBars := s.buildDailyBarsContext(ctx, instrument.Symbol)
	item.DataFreshness["dailyBars"] = dailyBarsFreshnessSummary(dailyBars)
	rowCount, earliest, latest, source, lastErr, err := s.store.GetDailyBarsStats(ctx, instrument.Symbol, DailyBarAdjustedQFQ)
	if err == nil {
		item.DailyBars = &StrategyGenerationBarsSummary{
			Adjusted:  DailyBarAdjustedQFQ,
			RowCount:  rowCount,
			Earliest:  earliest,
			Latest:    latest,
			Source:    source,
			LastError: lastErr,
			HasData:   rowCount > 0,
		}
		item.DataFreshness["dailyBarsRowCount"] = rowCount
		item.DataFreshness["dailyBarsLatest"] = latest
	} else {
		item.DataFreshness["dailyBarsError"] = "daily bars stats unavailable"
	}
	if profile, err := s.store.GetStockProfile(ctx, instrument.Symbol); err == nil {
		item.Profile = &profile
		item.DataFreshness["profileUpdatedAt"] = profile.UpdatedAt.Format(time.RFC3339)
	} else {
		item.DataFreshness["profileMissing"] = true
	}
	strategies, err := s.strategyGenerationExistingStrategies(ctx, instrument.Symbol)
	if err != nil {
		return StrategyGenerationInstrumentContext{}, err
	}
	item.ExistingStrategies = strategies
	item.DataFreshness["existingStrategyCount"] = len(strategies)
	return item, nil
}

func (s *Service) strategyGenerationExistingStrategies(ctx context.Context, symbol string) ([]StrategyWithVersion, error) {
	var out []StrategyWithVersion
	for _, status := range []string{StrategyStatusActive, StrategyStatusDraft} {
		items, err := s.store.ListStrategies(ctx, StrategyListFilter{
			Kind:   StrategyKindSymbolStrategy,
			Symbol: symbol,
			Status: status,
			Limit:  20,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func (s *Service) buildPortfolioStrategyGenerationContext(ctx context.Context, input StrategyGenerationInput, out StrategyGenerationContext) (StrategyGenerationContext, error) {
	portfolio, err := s.store.GetPortfolio(ctx, input.PortfolioID)
	if err != nil {
		return StrategyGenerationContext{}, err
	}
	freshness := out.FreshnessSummary
	freshness["mode"] = input.Mode

	portfolioCtx := &PortfolioReviewContext{Portfolio: portfolio}
	out.Portfolio = portfolioCtx
	out.Diagnostics = &StrategyGenerationPortfolioDiagnosis{
		Cash: portfolio.Cash,
	}

	if snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolio.ID, 1); err != nil {
		return StrategyGenerationContext{}, err
	} else if len(snapshots) > 0 {
		snapshot := snapshots[0]
		portfolioCtx.Snapshot = &snapshot
		freshness["portfolioSnapshot"] = map[string]any{
			"status":              snapshot.Status,
			"valuationAt":         snapshot.ValuationAt,
			"staleQuoteCount":     snapshot.StaleQuoteCount,
			"estimatedQuoteCount": snapshot.EstimatedQuoteCount,
		}
	} else {
		out.MissingItems = append(out.MissingItems, "portfolioSnapshot")
		freshness["portfolioSnapshot"] = map[string]any{"status": "missing"}
	}

	holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
	if err != nil {
		return StrategyGenerationContext{}, err
	}
	portfolioCtx.Holdings = holdings
	out.Diagnostics.HoldingCount = len(holdings)
	freshness["holdings"] = map[string]any{"status": "present", "count": len(holdings)}

	reviews, err := s.store.ListOperationReviews(ctx, OperationReviewListFilter{PortfolioID: portfolio.ID, Limit: 50})
	if err != nil {
		return StrategyGenerationContext{}, err
	}
	out.RecentReviews = reviews
	freshness["reviews"] = map[string]any{"status": "present", "count": len(reviews)}

	transactions, err := s.store.ListTransactions(ctx, portfolio.ID, 50)
	if err != nil {
		return StrategyGenerationContext{}, err
	}
	out.Transactions = transactions
	freshness["transactions"] = map[string]any{"status": "present", "count": len(transactions)}
	freshness["operationRecords"] = map[string]any{"status": "missing", "reason": "transactions are included; no separate operation record repository"}
	out.MissingItems = append(out.MissingItems, "operationRecords")

	symbols := holdingSymbols(holdings)
	quotesBySymbol := map[string]StockV2QuoteLatest{}
	if len(symbols) > 0 {
		quotes, err := s.store.GetLatestQuotes(ctx, symbols)
		if err != nil {
			return StrategyGenerationContext{}, err
		}
		for _, quote := range quotes {
			quotesBySymbol[quote.Symbol] = quote
		}
	}

	totalAsset := portfolio.Cash
	if portfolioCtx.Snapshot != nil && portfolioCtx.Snapshot.TotalAssetValue > 0 {
		totalAsset = portfolioCtx.Snapshot.TotalAssetValue
	} else {
		for _, holding := range holdings {
			totalAsset += strategyGenerationHoldingMarketValue(holding, quotesBySymbol)
		}
	}
	out.Diagnostics.TotalAssetValue = totalAsset
	if totalAsset > 0 {
		out.Diagnostics.CashPct = portfolio.Cash / totalAsset * 100
	}

	for _, holding := range holdings {
		hctx, err := s.buildStrategyGenerationHoldingContext(ctx, portfolio.ID, holding, totalAsset, quotesBySymbol, reviews, transactions, &out.MissingItems)
		if err != nil {
			return StrategyGenerationContext{}, err
		}
		out.Holdings = append(out.Holdings, hctx)
		if hctx.PositionPct > out.Diagnostics.LargestPositionPct {
			out.Diagnostics.LargestPositionPct = hctx.PositionPct
			out.Diagnostics.LargestPositionSymbol = hctx.Symbol
		}
		if hctx.StrategyCoverage.NeedsNewStrategy {
			out.Diagnostics.MissingStrategySymbols = append(out.Diagnostics.MissingStrategySymbols, hctx.Symbol)
		}
		if hctx.StrategyCoverage.PatchCandidate {
			out.Diagnostics.PatchCandidateSymbols = append(out.Diagnostics.PatchCandidateSymbols, hctx.Symbol)
		}
	}
	sort.Strings(out.Diagnostics.MissingStrategySymbols)
	sort.Strings(out.Diagnostics.PatchCandidateSymbols)
	out.MissingItems = compactStrings(out.MissingItems)
	freshness["holdingCount"] = len(out.Holdings)
	return out, nil
}

func (s *Service) buildStrategyGenerationHoldingContext(
	ctx context.Context,
	portfolioID string,
	holding StockV2Holding,
	totalAsset float64,
	quotes map[string]StockV2QuoteLatest,
	reviews []OperationReview,
	transactions []StockV2Transaction,
	missing *[]string,
) (StrategyGenerationHoldingContext, error) {
	hctx := StrategyGenerationHoldingContext{
		Symbol:            strings.TrimSpace(holding.Symbol),
		Market:            strings.TrimSpace(holding.Market),
		Name:              strings.TrimSpace(holding.Name),
		CostPrice:         holding.CostPrice,
		Quantity:          holding.Quantity,
		AvailableQuantity: holding.AvailableQuantity,
		MarketValue:       holding.MarketValue,
		PnL:               holding.PnL,
		PositionPct:       holding.PositionPct,
		Holding:           holding,
		Freshness:         map[string]any{},
	}
	if quote, ok := quotes[hctx.Symbol]; ok {
		hctx.Quote = &quote
		hctx.Freshness["quote"] = quoteFreshnessSummary(quote)
		if quote.Status == QuoteStatusFresh && quote.LastPrice > 0 {
			hctx.CurrentPrice = quote.LastPrice
		} else {
			*missing = append(*missing, "quoteStale:"+hctx.Symbol)
		}
	} else {
		*missing = append(*missing, "quote:"+hctx.Symbol)
		hctx.Freshness["quote"] = map[string]any{"status": "missing"}
	}
	if hctx.MarketValue <= 0 && hctx.CurrentPrice > 0 {
		hctx.MarketValue = hctx.CurrentPrice * hctx.Quantity
	}
	if hctx.PnL == 0 && hctx.CurrentPrice > 0 && hctx.CostPrice > 0 {
		hctx.PnL = (hctx.CurrentPrice - hctx.CostPrice) * hctx.Quantity
	}
	if hctx.PositionPct <= 0 && totalAsset > 0 && hctx.MarketValue > 0 {
		hctx.PositionPct = hctx.MarketValue / totalAsset * 100
	}

	hctx.DailyBars = s.buildDailyBarsContext(ctx, hctx.Symbol)
	hctx.Freshness["dailyBars"] = dailyBarsFreshnessSummary(hctx.DailyBars)
	if hctx.DailyBars == nil || hctx.DailyBars.Count == 0 {
		*missing = append(*missing, "dailyBars:"+hctx.Symbol)
	}

	if profile, err := s.store.GetStockProfile(ctx, hctx.Symbol); err == nil {
		hctx.Profile = &profile
		hctx.Freshness["stockProfile"] = map[string]any{"status": "present", "profileVersion": profile.ProfileVersion}
	} else if errors.Is(err, ErrStockProfileNotFound) {
		*missing = append(*missing, "stockProfile:"+hctx.Symbol)
		hctx.Freshness["stockProfile"] = map[string]any{"status": "missing"}
	} else {
		return StrategyGenerationHoldingContext{}, err
	}

	coverage, err := s.strategyGenerationCoverage(ctx, portfolioID, hctx.Symbol)
	if err != nil {
		return StrategyGenerationHoldingContext{}, err
	}
	hctx.StrategyCoverage = coverage
	hctx.RecentReviews = filterReviewsBySymbol(reviews, hctx.Symbol, 5)
	hctx.Transactions = filterTransactionsBySymbol(transactions, hctx.Symbol, 5)
	return hctx, nil
}

func normalizeStrategyGenerationInput(input StrategyGenerationInput) (StrategyGenerationInput, error) {
	input.Mode = strings.TrimSpace(input.Mode)
	if input.Mode == "" {
		input.Mode = StrategyGenerationModeManualTarget
	}
	if !validStrategyGenerationMode(input.Mode) {
		return StrategyGenerationInput{}, ErrInvalidStrategyGenerationInput
	}
	input.UserGoal = strings.TrimSpace(input.UserGoal)
	input.UserIntent = strings.TrimSpace(input.UserIntent)
	if input.UserGoal == "" {
		input.UserGoal = input.UserIntent
	}
	input.PortfolioID = strings.TrimSpace(input.PortfolioID)
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	input.OpportunityID = strings.TrimSpace(input.OpportunityID)
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	candidateIDs := compactStringList(append(input.CandidateIDs, input.CandidateID), opportunityMarketScanStrategyLimit)
	input.CandidateIDs = candidateIDs
	if len(candidateIDs) > 0 {
		input.CandidateID = candidateIDs[0]
	}
	input.TimeHorizon = strings.TrimSpace(input.TimeHorizon)
	if len(input.AllowedActions) == 0 && input.Mode == StrategyGenerationModePortfolio {
		input.AllowedActions = []string{
			StrategyGenerationRuleActionObserve,
			StrategyGenerationRuleActionBuildPosition,
			StrategyGenerationRuleActionAddPosition,
			StrategyGenerationRuleActionHold,
			StrategyGenerationRuleActionReduce,
			StrategyGenerationRuleActionExit,
		}
	}
	targets := make([]StrategyGenerationTargetInstrument, 0, len(input.TargetInstruments))
	seen := map[string]bool{}
	for _, target := range input.TargetInstruments {
		target.Symbol = strings.TrimSpace(target.Symbol)
		target.Market = strings.TrimSpace(target.Market)
		target.Name = strings.TrimSpace(target.Name)
		target.UserNote = strings.TrimSpace(target.UserNote)
		if target.Symbol == "" || seen[target.Symbol] {
			continue
		}
		seen[target.Symbol] = true
		targets = append(targets, target)
	}
	if input.Mode == StrategyGenerationModePortfolio {
		if input.PortfolioID == "" {
			return StrategyGenerationInput{}, ErrStrategyGenerationPortfolioRequired
		}
		input.TargetInstruments = targets
		return input, nil
	}
	if input.Mode == StrategyGenerationModeSingleInstrument && len(targets) != 1 {
		return StrategyGenerationInput{}, ErrInvalidStrategyGenerationInput
	}
	if len(targets) == 0 {
		return StrategyGenerationInput{}, ErrInvalidStrategyGenerationInput
	}
	input.TargetInstruments = targets
	return input, nil
}

func strategyGenerationTriggerID(input StrategyGenerationInput) string {
	parts := make([]string, 0, len(input.TargetInstruments))
	for _, target := range input.TargetInstruments {
		parts = append(parts, target.Symbol)
	}
	if input.Mode == StrategyGenerationModePortfolio {
		if len(parts) == 0 {
			return fmt.Sprintf("%s:portfolio=%s", input.Mode, input.PortfolioID)
		}
		return fmt.Sprintf("%s:portfolio=%s:symbols=%s", input.Mode, input.PortfolioID, strings.Join(parts, ","))
	}
	if input.Mode == StrategyGenerationModeOpportunity && input.OpportunityID != "" {
		return fmt.Sprintf("%s:opportunity=%s:candidates=%s:symbols=%s", input.Mode, input.OpportunityID, strings.Join(input.CandidateIDs, ","), strings.Join(parts, ","))
	}
	if input.PortfolioID != "" {
		return fmt.Sprintf("%s:portfolio=%s:symbols=%s", input.Mode, input.PortfolioID, strings.Join(parts, ","))
	}
	return strings.TrimSpace(input.Mode) + ":symbols=" + strings.Join(parts, ",")
}

func strategyGenerationInputSummary(input StrategyGenerationInput) string {
	return fmt.Sprintf("strategy_generation mode=%s targets=%s goal=%s", input.Mode, strategyGenerationTriggerID(input), input.UserGoal)
}

func strategyGenerationReportFromResult(raw map[string]any) (StrategyGenerationReport, error) {
	return strategyGenerationReportFromResultForMode(raw, "")
}

func strategyGenerationReportFromResultForMode(raw map[string]any, expectedMode string) (StrategyGenerationReport, error) {
	if len(raw) == 0 {
		return StrategyGenerationReport{}, ErrInvalidStrategyGenerationResult
	}
	if err := rejectLegacyStrategyGenerationPlaybookShape(raw); err != nil {
		return StrategyGenerationReport{}, err
	}
	normalizeStrategyGenerationPlaybookPrefilters(raw)
	normalizeStrategyGenerationReportShape(raw)
	expectedMode = strings.TrimSpace(expectedMode)
	if expectedMode != "" {
		if !validStrategyGenerationMode(expectedMode) {
			return StrategyGenerationReport{}, fmt.Errorf("%w: invalid expected mode %q", ErrInvalidStrategyGenerationResult, expectedMode)
		}
		summary := mapFromAny(raw["run_summary"])
		raw["run_summary"] = summary
		if strings.TrimSpace(stringFromAny(summary["mode"])) == "" {
			summary["mode"] = expectedMode
		}
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return StrategyGenerationReport{}, err
	}
	var report StrategyGenerationReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return StrategyGenerationReport{}, err
	}
	if err := validateStrategyGenerationReport(report); err != nil {
		return StrategyGenerationReport{}, err
	}
	if expectedMode != "" && report.RunSummary.Mode != expectedMode {
		return StrategyGenerationReport{}, fmt.Errorf("%w: run_summary.mode %q does not match task mode %q", ErrInvalidStrategyGenerationResult, report.RunSummary.Mode, expectedMode)
	}
	return report, nil
}

func rejectLegacyStrategyGenerationPlaybookShape(raw map[string]any) error {
	for _, draftRaw := range sliceFromAny(raw["drafts"]) {
		draft := mapFromAny(draftRaw)
		playbook := mapFromAny(draft["playbook"])
		if _, ok := playbook["actions"]; ok {
			return ErrInvalidStrategyGenerationResult
		}
		for _, ruleRaw := range sliceFromAny(playbook["rules"]) {
			rule := mapFromAny(ruleRaw)
			if _, ok := rule["action_type"]; ok {
				return ErrInvalidStrategyGenerationResult
			}
		}
	}
	return nil
}

func normalizeStrategyGenerationPlaybookPrefilters(raw map[string]any) {
	for _, draftRaw := range sliceFromAny(raw["drafts"]) {
		draft := mapFromAny(draftRaw)
		playbook := mapFromAny(draft["playbook"])
		for _, ruleRaw := range sliceFromAny(playbook["rules"]) {
			rule := mapFromAny(ruleRaw)
			for _, key := range []string{"dataPrefilters", "portfolioPrefilters"} {
				switch value := rule[key].(type) {
				case nil:
					continue
				case []any, []map[string]any:
					continue
				case map[string]any:
					rule[key] = []any{value}
				case string:
					rule[key] = []any{}
				default:
					rule[key] = []any{}
				}
			}
		}
	}
}

func normalizeStrategyGenerationReportShape(raw map[string]any) {
	for _, draftRaw := range sliceFromAny(raw["drafts"]) {
		draft := mapFromAny(draftRaw)
		instrument := mapFromAny(draft["instrument"])
		target := mapFromAny(draft["target"])
		setStringIfEmpty(draft, "draft_type", draft["type"])
		setStringIfEmpty(draft, "symbol", instrument["symbol"])
		setStringIfEmpty(draft, "market", instrument["market"])
		setStringIfEmpty(draft, "name", instrument["name"])
		setStringIfEmpty(draft, "symbol", target["symbol"])
		setStringIfEmpty(draft, "market", target["market"])
		setStringIfEmpty(draft, "name", target["name"])
		setStringIfEmpty(draft, "strategy_bias", draft["direction"])

		playbook := mapFromAny(draft["playbook"])
		for _, ruleRaw := range sliceFromAny(playbook["rules"]) {
			rule := mapFromAny(ruleRaw)
			setStringIfEmpty(rule, "id", rule["rule_id"])
			setStringIfEmpty(rule, "action", rule["signal"])
			setStringIfEmpty(rule, "title", rule["name"])
			setStringIfEmpty(rule, "trigger", rule["condition"])
			setStringIfEmpty(rule, "preconditions", rule["on_false"])
			setStringIfEmpty(rule, "target", rule["on_true"])
			if strings.TrimSpace(stringFromAny(rule["action"])) != "" {
				continue
			}
			rule["action"] = strategyGenerationRuleActionFromActions(rule["actions"])
		}
	}
}

func setStringIfEmpty(target map[string]any, key string, value any) {
	if strings.TrimSpace(stringFromAny(target[key])) != "" {
		return
	}
	if text := strings.TrimSpace(stringFromAny(value)); text != "" {
		target[key] = text
	}
}

func strategyGenerationRuleActionFromActions(value any) string {
	for _, candidate := range sliceFromAny(value) {
		action := strings.TrimSpace(stringFromAny(candidate))
		if validStrategyGenerationRuleAction(action) {
			return action
		}
	}
	action := strings.TrimSpace(stringFromAny(value))
	if validStrategyGenerationRuleAction(action) {
		return action
	}
	return StrategyGenerationRuleActionObserve
}

func validateStrategyGenerationReport(report StrategyGenerationReport) error {
	if strings.TrimSpace(report.SchemaVersion) != StrategyGenerationReportSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %q", ErrInvalidStrategyGenerationResult, StrategyGenerationReportSchemaVersion)
	}
	if !validStrategyGenerationMode(strings.TrimSpace(report.RunSummary.Mode)) {
		return fmt.Errorf("%w: invalid run_summary.mode %q", ErrInvalidStrategyGenerationResult, report.RunSummary.Mode)
	}
	if len(report.Drafts) == 0 && report.RunSummary.Mode != StrategyGenerationModePortfolio && report.RunSummary.Mode != StrategyGenerationModeOpportunity {
		return fmt.Errorf("%w: mode %q requires at least one draft", ErrInvalidStrategyGenerationResult, report.RunSummary.Mode)
	}
	for draftIndex, draft := range report.Drafts {
		if !validStrategyGenerationDraftType(strings.TrimSpace(draft.DraftType)) {
			return fmt.Errorf("%w: drafts[%d].draft_type %q is invalid", ErrInvalidStrategyGenerationResult, draftIndex, draft.DraftType)
		}
		if draft.DraftType != StrategyGenerationDraftTypeNewStrategy {
			continue
		}
		if strings.TrimSpace(draft.Symbol) == "" || strings.TrimSpace(draft.Thesis) == "" {
			return fmt.Errorf("%w: drafts[%d] requires symbol and thesis", ErrInvalidStrategyGenerationResult, draftIndex)
		}
		if _, err := strategyGenerationDraftDirection(draft); err != nil {
			return fmt.Errorf("%w: drafts[%d] has invalid strategy_bias", ErrInvalidStrategyGenerationResult, draftIndex)
		}
		if len(draft.Playbook.Rules) == 0 {
			return fmt.Errorf("%w: drafts[%d].playbook.rules is empty", ErrInvalidStrategyGenerationResult, draftIndex)
		}
		for ruleIndex, rule := range draft.Playbook.Rules {
			if strings.TrimSpace(rule.ID) == "" || !validStrategyGenerationRuleAction(strings.TrimSpace(rule.Action)) {
				return fmt.Errorf("%w: drafts[%d].playbook.rules[%d] requires id and valid action", ErrInvalidStrategyGenerationResult, draftIndex, ruleIndex)
			}
		}
	}
	return nil
}

func (s *Service) createDraftStrategiesFromStrategyGeneration(ctx context.Context, run AgentRun, submitted AgentTaskSubmittedResult, report StrategyGenerationReport) ([]StrategyWithVersion, error) {
	s.applyDecisionGatesToStrategyReport(ctx, run, &report)
	if strings.TrimSpace(report.RunSummary.Mode) == StrategyGenerationModePortfolio {
		return s.createPortfolioStrategyDiagnosisDrafts(ctx, run, submitted, report)
	}
	if err := validateStrategyGenerationDraftTargets(run.TriggerObjectID, report.Drafts); err != nil {
		return nil, err
	}
	created := make([]StrategyWithVersion, 0, len(report.Drafts))
	for _, draft := range report.Drafts {
		if draft.DraftType != StrategyGenerationDraftTypeNewStrategy {
			continue
		}
		req, err := s.strategyCreateRequestFromGenerationDraft(run, submitted, report, draft)
		if err != nil {
			return nil, err
		}
		item, err := s.CreateStrategy(ctx, req)
		if err != nil {
			return nil, err
		}
		created = append(created, item)
	}
	return created, nil
}

// recoverInvalidStrategyGenerationRun reapplies a persisted, already-paid model
// result after deterministic schema normalization has been upgraded. It never
// retries provider, timeout, or missing-result failures.
func (s *Service) recoverInvalidStrategyGenerationRun(ctx context.Context, run AgentRun) (bool, error) {
	if run.TaskType != AgentTaskTypeStrategyGeneration || run.Status != AgentRunStatusFailed ||
		!strings.HasPrefix(strings.TrimSpace(run.ErrorMessage), ErrInvalidStrategyGenerationResult.Error()) ||
		strings.TrimSpace(run.DecisionLedgerID) == "" {
		return false, nil
	}
	ledger, err := s.store.GetAgentDecisionLedger(ctx, run.DecisionLedgerID)
	if err != nil {
		return false, err
	}
	raw := mapFromAny(ledger.StructuredOutput["result"])
	if len(raw) == 0 {
		return false, nil
	}
	report, err := strategyGenerationReportFromResultForMode(raw, strategyGenerationModeFromTrigger(run.TriggerObjectID))
	if err != nil {
		return false, nil
	}
	confidence, _ := numberFromAny(ledger.StructuredOutput["confidence"])
	submitted := AgentTaskSubmittedResult{
		OutputType:    strings.TrimSpace(stringFromAny(ledger.StructuredOutput["outputType"])),
		ResultSummary: strings.TrimSpace(stringFromAny(ledger.StructuredOutput["resultSummary"])),
		Result:        raw,
		Confidence:    confidence,
	}
	if submitted.OutputType == "" {
		submitted.OutputType = AgentTaskTypeStrategyGeneration
	}
	created, err := s.createDraftStrategiesFromStrategyGeneration(ctx, run, submitted, report)
	if err != nil {
		return false, err
	}
	createdSummaries := make([]map[string]string, 0, len(created))
	for _, item := range created {
		createdSummaries = append(createdSummaries, map[string]string{
			"id":     item.Strategy.ID,
			"symbol": item.Strategy.Symbol,
			"status": item.Strategy.Status,
		})
	}
	ledger.StructuredOutput["createdStrategies"] = createdSummaries
	ledger.StructuredOutput["strategyGenerationRecovered"] = true
	if _, err := s.store.UpdateAgentDecisionLedger(ctx, ledger); err != nil {
		return false, err
	}
	s.markStrategyGenerationCandidatesCreated(ctx, run, created)
	run.Status = AgentRunStatusCompleted
	run.ErrorMessage = ""
	run.Output = safelog.Text(submitted.ResultSummary, 2000)
	run.FinishedAt = time.Now()
	if _, err := s.store.UpdateAgentRun(ctx, run); err != nil {
		return false, err
	}
	return true, nil
}

func validateStrategyGenerationDraftTargets(triggerID string, drafts []StrategyGenerationDraft) error {
	allowedSymbols := strategyGenerationSymbolsFromTrigger(triggerID)
	if len(allowedSymbols) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(allowedSymbols))
	for _, symbol := range allowedSymbols {
		allowed[symbol] = true
	}
	seen := map[string]bool{}
	for _, draft := range drafts {
		if draft.DraftType != StrategyGenerationDraftTypeNewStrategy {
			continue
		}
		symbol := strings.TrimSpace(draft.Symbol)
		if !allowed[symbol] || seen[symbol] {
			return ErrInvalidStrategyGenerationResult
		}
		seen[symbol] = true
	}
	return nil
}

func (s *Service) strategyCreateRequestFromGenerationDraft(run AgentRun, submitted AgentTaskSubmittedResult, report StrategyGenerationReport, draft StrategyGenerationDraft) (RequestCreateStrategy, error) {
	direction, err := strategyGenerationDraftDirection(draft)
	if err != nil {
		return RequestCreateStrategy{}, err
	}
	symbol := strings.TrimSpace(draft.Symbol)
	market := strings.TrimSpace(draft.Market)
	if market == "" {
		market = inferAStockMarket(symbol)
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" {
		name = symbol
	}
	scope := StrategyScopeResearch
	portfolioID := strategyGenerationPortfolioIDFromTrigger(run.TriggerObjectID)
	if portfolioID != "" {
		scope = StrategyScopePortfolioBound
	}
	title := strings.TrimSpace(draft.Thesis)
	if title != "" && len([]rune(title)) > 60 {
		title = string([]rune(title)[:60])
	}
	if title == "" {
		title = "Agent strategy draft - " + symbol
	}
	playbook := draft.Playbook
	if strings.TrimSpace(playbook.Version) == "" {
		playbook.Version = "v1"
	}
	return RequestCreateStrategy{
		Name:            "Agent策略草案 - " + name,
		Kind:            StrategyKindSymbolStrategy,
		Scope:           scope,
		Source:          StrategySourceAgent,
		Status:          StrategyStatusDraft,
		Symbol:          symbol,
		Market:          market,
		PortfolioID:     portfolioID,
		Title:           title,
		Direction:       direction,
		Thesis:          strings.TrimSpace(draft.Thesis),
		EntryConditions: strategyGenerationEntryConditions(draft.Playbook),
		ExitConditions:  draft.InvalidConditions,
		RiskNotes:       strategyGenerationRiskNotes(draft),
		EvidenceRefs:    draft.EvidenceSummary,
		GenerationMeta: map[string]any{
			"source":     AgentTaskTypeStrategyGeneration,
			"playbook":   playbook,
			"agentRunId": run.ID,
			"strategyGeneration": map[string]any{
				"schemaVersion":            report.SchemaVersion,
				"mode":                     report.RunSummary.Mode,
				"draftType":                draft.DraftType,
				"confidence":               draft.Confidence,
				"resultSummary":            submitted.ResultSummary,
				"portfolioAwareSuggestion": draft.PortfolioAwareSuggestion,
				"runSummary":               report.RunSummary,
				"decisionBasis":            draft.DecisionBasis,
				"evidenceRefIds":           draft.EvidenceRefIDs,
				"gateSnapshotId":           draft.GateSnapshotID,
			},
		},
		CreatedBy: StrategySourceAgent,
	}, nil
}

func (s *Service) createPortfolioStrategyDiagnosisDrafts(ctx context.Context, run AgentRun, submitted AgentTaskSubmittedResult, report StrategyGenerationReport) ([]StrategyWithVersion, error) {
	portfolioID := strategyGenerationPortfolioIDFromTrigger(run.TriggerObjectID)
	if portfolioID == "" && !strings.Contains(run.TriggerObjectID, ":") {
		portfolioID = strings.TrimSpace(run.TriggerObjectID)
	}
	if portfolioID == "" {
		return nil, ErrStrategyGenerationPortfolioRequired
	}
	portfolio, err := s.store.GetPortfolio(ctx, portfolioID)
	if err != nil {
		return nil, err
	}
	holdings, err := s.store.ListHoldings(ctx, portfolioID)
	if err != nil {
		return nil, err
	}
	held := map[string]StockV2Holding{}
	for _, holding := range holdings {
		symbol := strings.TrimSpace(holding.Symbol)
		if symbol != "" {
			held[symbol] = holding
		}
	}

	created := make([]StrategyWithVersion, 0)
	for _, draft := range report.Drafts {
		if draft.DraftType != StrategyGenerationDraftTypeNewStrategy {
			continue
		}
		symbol := strings.TrimSpace(draft.Symbol)
		holding, ok := held[symbol]
		if !ok {
			continue
		}
		coverage, err := s.strategyGenerationCoverage(ctx, portfolioID, symbol)
		if err != nil {
			return nil, err
		}
		if coverage.HasStrategy {
			continue
		}
		req, err := s.strategyCreateRequestFromGenerationDraft(run, submitted, report, draft)
		if err != nil {
			return nil, err
		}
		req.Name = strategyGenerationPortfolioDraftName(portfolio, holding, draft)
		req.Scope = StrategyScopePortfolioBound
		req.PortfolioID = portfolioID
		req.Market = firstNonEmpty(req.Market, holding.Market, inferAStockMarket(symbol))
		req.GenerationMeta["portfolioId"] = portfolioID
		if sg := mapFromAny(req.GenerationMeta["strategyGeneration"]); sg != nil {
			sg["reviewRequest"] = draft.PortfolioAwareSuggestion.ReviewRequest
			req.GenerationMeta["strategyGeneration"] = sg
		}
		item, err := s.CreateStrategy(ctx, req)
		if err != nil {
			return nil, err
		}
		created = append(created, item)
	}
	return created, nil
}

func strategyGenerationPortfolioIDFromTrigger(triggerID string) string {
	for _, part := range strings.Split(triggerID, ":") {
		if value, ok := strings.CutPrefix(part, "portfolio="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func strategyGenerationModeFromTrigger(triggerID string) string {
	mode, _, _ := strings.Cut(strings.TrimSpace(triggerID), ":")
	if validStrategyGenerationMode(mode) {
		return mode
	}
	return ""
}

func strategyGenerationCandidateIDsFromTrigger(triggerID string) []string {
	for _, part := range strings.Split(triggerID, ":") {
		if value, ok := strings.CutPrefix(part, "candidates="); ok {
			return compactStringList(strings.Split(value, ","), opportunityMarketScanStrategyLimit)
		}
		// DEPRECATED: remove after 2026-09-10, when runs created before the
		// batch-candidate release have aged out of the UI history.
		if value, ok := strings.CutPrefix(part, "candidate="); ok && strings.TrimSpace(value) != "" {
			return []string{strings.TrimSpace(value)}
		}
	}
	return nil
}

func strategyGenerationSymbolsFromTrigger(triggerID string) []string {
	for _, part := range strings.Split(triggerID, ":") {
		if value, ok := strings.CutPrefix(part, "symbols="); ok {
			return compactStringList(strings.Split(value, ","), 100)
		}
	}
	return nil
}

func (s *Service) markStrategyGenerationCandidatesCreated(ctx context.Context, run AgentRun, created []StrategyWithVersion) {
	bySymbol := make(map[string]string, len(created))
	for _, item := range created {
		bySymbol[strings.TrimSpace(item.Strategy.Symbol)] = item.Strategy.ID
	}
	for _, candidateID := range strategyGenerationCandidateIDsFromTrigger(run.TriggerObjectID) {
		candidate, err := s.store.GetOpportunityCandidate(ctx, candidateID)
		if err != nil {
			continue
		}
		strategyID, ok := bySymbol[candidate.Symbol]
		if !ok {
			continue
		}
		candidate.Status = OpportunityCandidateStatusStrategyGenerated
		if candidate.Metadata == nil {
			candidate.Metadata = map[string]any{}
		}
		candidate.Metadata["strategyGenerationRunId"] = run.ID
		candidate.Metadata["strategyId"] = strategyID
		_, _ = s.store.UpdateOpportunityCandidate(ctx, candidate)
	}
}

func strategyGenerationDraftDirection(draft StrategyGenerationDraft) (string, error) {
	direction := strings.TrimSpace(draft.StrategyBias)
	if direction == "" {
		return StrategyDirectionWatch, nil
	}
	if err := validateStrategyDirection(direction); err != nil {
		return "", err
	}
	return direction, nil
}

func strategyGenerationEntryConditions(playbook StrategyGenerationPlaybook) []string {
	out := make([]string, 0, len(playbook.Rules))
	for _, rule := range playbook.Rules {
		value := strings.TrimSpace(rule.Title)
		if value == "" {
			value = strings.TrimSpace(rule.Trigger)
		}
		if value == "" {
			value = strings.TrimSpace(rule.ID)
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func strategyGenerationRiskNotes(draft StrategyGenerationDraft) string {
	parts := make([]string, 0, len(draft.RiskSummary)+len(draft.InvalidConditions))
	parts = append(parts, draft.RiskSummary...)
	if len(draft.InvalidConditions) > 0 {
		parts = append(parts, "Invalid conditions: "+strings.Join(draft.InvalidConditions, "; "))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (s *Service) strategyGenerationCoverage(ctx context.Context, portfolioID, symbol string) (StrategyGenerationStrategyCoverage, error) {
	items, err := s.store.ListStrategies(ctx, StrategyListFilter{
		Kind:   StrategyKindSymbolStrategy,
		Symbol: strings.TrimSpace(symbol),
		Limit:  200,
	})
	if err != nil {
		return StrategyGenerationStrategyCoverage{}, err
	}
	coverage := StrategyGenerationStrategyCoverage{}
	for _, item := range items {
		strategy := item.Strategy
		if strategy.Status == StrategyStatusArchived {
			continue
		}
		if strategy.PortfolioID != "" && strategy.PortfolioID != portfolioID {
			continue
		}
		summary := StrategyGenerationStrategySummary{
			ID:     strategy.ID,
			Name:   strategy.Name,
			Status: strategy.Status,
			Scope:  strategy.Scope,
			Source: strategy.Source,
		}
		if item.ActiveVersion != nil {
			summary.VersionID = item.ActiveVersion.ID
			summary.Title = item.ActiveVersion.Title
			summary.Direction = item.ActiveVersion.Direction
		}
		coverage.Strategies = append(coverage.Strategies, summary)
		switch strategy.Status {
		case StrategyStatusActive:
			coverage.HasActive = true
			coverage.HasStrategy = true
		case StrategyStatusDraft:
			coverage.HasDraft = true
			coverage.HasStrategy = true
		case StrategyStatusPaused:
			coverage.HasPaused = true
			coverage.HasStrategy = true
		}
	}
	coverage.NeedsNewStrategy = !coverage.HasStrategy
	coverage.PatchCandidate = coverage.HasActive || coverage.HasDraft || coverage.HasPaused
	return coverage, nil
}

func strategyGenerationPortfolioDraftName(portfolio StockV2Portfolio, holding StockV2Holding, draft StrategyGenerationDraft) string {
	display := firstNonEmpty(strings.TrimSpace(holding.Name), strings.TrimSpace(draft.Name), holding.Symbol)
	if portfolio.Name == "" {
		return "Agent组合诊断策略草案 - " + display
	}
	return fmt.Sprintf("%s Agent组合诊断策略草案 - %s", portfolio.Name, display)
}

func holdingSymbols(holdings []StockV2Holding) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(holdings))
	for _, holding := range holdings {
		symbol := strings.TrimSpace(holding.Symbol)
		if symbol == "" || seen[symbol] {
			continue
		}
		seen[symbol] = true
		out = append(out, symbol)
	}
	return out
}

func strategyGenerationHoldingMarketValue(holding StockV2Holding, quotes map[string]StockV2QuoteLatest) float64 {
	if holding.MarketValue > 0 {
		return holding.MarketValue
	}
	if quote, ok := quotes[holding.Symbol]; ok && quote.Status == QuoteStatusFresh && quote.LastPrice > 0 {
		return quote.LastPrice * holding.Quantity
	}
	return 0
}

func compactStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func filterReviewsBySymbol(items []OperationReview, symbol string, limit int) []OperationReview {
	out := make([]OperationReview, 0)
	for _, item := range items {
		if item.Symbol != symbol {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	return out
}

func filterTransactionsBySymbol(items []StockV2Transaction, symbol string, limit int) []StockV2Transaction {
	out := make([]StockV2Transaction, 0)
	for _, item := range items {
		if item.Symbol != symbol {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	return out
}

func sliceFromAny(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func strategyGenerationSaveError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidStrategyGenerationResult) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrInvalidStrategyGenerationResult, err)
}
