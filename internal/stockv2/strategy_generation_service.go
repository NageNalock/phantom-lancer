package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	StrategyGenerationInputSchemaVersion  = "strategy-generation-input/v1"
	StrategyGenerationReportSchemaVersion = "strategy-generation-report/v1"
	StrategyGenerationModePortfolio       = "portfolio_strategy_diagnosis"
	StrategyGenerationOutputType          = "strategy_generation"

	StrategyGenerationDraftNewStrategy = "new_strategy"
	StrategyGenerationDraftPatch       = "strategy_patch"
	StrategyGenerationDraftNoChange    = "no_change"

	StrategyGenerationActionObserve       = "observe"
	StrategyGenerationActionBuildPosition = "build_position"
	StrategyGenerationActionAddPosition   = "add_position"
	StrategyGenerationActionHold          = "hold"
	StrategyGenerationActionReduce        = "reduce_position"
	StrategyGenerationActionExit          = "exit_position"
)

var (
	ErrStrategyGenerationPortfolioRequired = errors.New("portfolioId is required for portfolio_strategy_diagnosis")
	ErrStrategyGenerationModeUnsupported   = errors.New("strategy generation mode is not supported yet")
	ErrInvalidStrategyGenerationResult     = errors.New("invalid strategy generation result")
)

type StrategyGenerationInput struct {
	SchemaVersion     string                               `json:"schema_version,omitempty"`
	Mode              string                               `json:"mode"`
	UserIntent        string                               `json:"user_intent,omitempty"`
	TargetInstruments []StrategyGenerationTargetInstrument `json:"target_instruments,omitempty"`
	OpportunityID     string                               `json:"opportunity_id,omitempty"`
	PortfolioID       string                               `json:"portfolio_id,omitempty"`
	TimeHorizon       string                               `json:"time_horizon,omitempty"`
	AllowedActions    []string                             `json:"allowed_actions,omitempty"`
	EvidenceScope     map[string]bool                      `json:"evidence_scope,omitempty"`
}

type StrategyGenerationTargetInstrument struct {
	Symbol   string `json:"symbol"`
	Market   string `json:"market,omitempty"`
	Name     string `json:"name,omitempty"`
	UserNote string `json:"user_note,omitempty"`
}

type StrategyGenerationContext struct {
	SchemaVersion  string                                 `json:"schema_version"`
	Mode           string                                 `json:"mode"`
	UserIntent     string                                 `json:"user_intent,omitempty"`
	TimeHorizon    string                                 `json:"time_horizon,omitempty"`
	AllowedActions []string                               `json:"allowed_actions,omitempty"`
	BuiltAt        time.Time                              `json:"builtAt"`
	Portfolio      PortfolioReviewContext                 `json:"portfolio"`
	Diagnostics    StrategyGenerationPortfolioDiagnostics `json:"diagnostics"`
	Holdings       []StrategyGenerationHoldingContext     `json:"holdings"`
	RecentReviews  []OperationReview                      `json:"recentReviews,omitempty"`
	Transactions   []StockV2Transaction                   `json:"transactions,omitempty"`
	Freshness      map[string]any                         `json:"freshness,omitempty"`
	MissingItems   []string                               `json:"missingItems,omitempty"`
}

type StrategyGenerationPortfolioDiagnostics struct {
	HoldingCount           int      `json:"holdingCount"`
	Cash                   float64  `json:"cash"`
	TotalAssetValue        float64  `json:"totalAssetValue"`
	CashPct                float64  `json:"cashPct"`
	LargestPositionSymbol  string   `json:"largestPositionSymbol,omitempty"`
	LargestPositionPct     float64  `json:"largestPositionPct,omitempty"`
	MissingStrategySymbols []string `json:"missingStrategySymbols,omitempty"`
	PatchCandidateSymbols  []string `json:"patchCandidateSymbols,omitempty"`
}

type StrategyGenerationHoldingContext struct {
	Symbol            string                             `json:"symbol"`
	Market            string                             `json:"market,omitempty"`
	Name              string                             `json:"name,omitempty"`
	CostPrice         float64                            `json:"costPrice"`
	Quantity          float64                            `json:"quantity"`
	AvailableQuantity float64                            `json:"availableQuantity"`
	CurrentPrice      float64                            `json:"currentPrice"`
	MarketValue       float64                            `json:"marketValue"`
	PnL               float64                            `json:"pnl"`
	PositionPct       float64                            `json:"positionPct"`
	Holding           StockV2Holding                     `json:"holding"`
	Quote             *StockV2QuoteLatest                `json:"quote,omitempty"`
	DailyBars         *DailyBarsContext                  `json:"dailyBars,omitempty"`
	Profile           *StockProfile                      `json:"stockProfile,omitempty"`
	StrategyCoverage  StrategyGenerationStrategyCoverage `json:"strategyCoverage"`
	RecentReviews     []OperationReview                  `json:"recentReviews,omitempty"`
	Transactions      []StockV2Transaction               `json:"transactions,omitempty"`
	Freshness         map[string]any                     `json:"freshness,omitempty"`
}

type StrategyGenerationStrategyCoverage struct {
	HasStrategy      bool                                `json:"hasStrategy"`
	HasActive        bool                                `json:"hasActive"`
	HasDraft         bool                                `json:"hasDraft"`
	HasPaused        bool                                `json:"hasPaused"`
	NeedsNewStrategy bool                                `json:"needsNewStrategy"`
	PatchCandidate   bool                                `json:"patchCandidate"`
	Strategies       []StrategyGenerationStrategySummary `json:"strategies,omitempty"`
}

type StrategyGenerationStrategySummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Scope     string `json:"scope,omitempty"`
	Source    string `json:"source,omitempty"`
	VersionID string `json:"versionId,omitempty"`
	Title     string `json:"title,omitempty"`
	Direction string `json:"direction,omitempty"`
}

func (s *Service) BuildStrategyGenerationContext(ctx context.Context, input StrategyGenerationInput) (StrategyGenerationContext, error) {
	input = normalizeStrategyGenerationInput(input)
	if input.Mode != StrategyGenerationModePortfolio {
		return StrategyGenerationContext{}, ErrStrategyGenerationModeUnsupported
	}
	if strings.TrimSpace(input.PortfolioID) == "" {
		return StrategyGenerationContext{}, ErrStrategyGenerationPortfolioRequired
	}
	portfolio, err := s.store.GetPortfolio(ctx, input.PortfolioID)
	if err != nil {
		return StrategyGenerationContext{}, err
	}

	freshness := map[string]any{"builtAt": time.Now().Format(time.RFC3339)}
	missing := make([]string, 0)
	markMissing := func(key string) {
		missing = append(missing, key)
		freshness[key] = map[string]any{"status": "missing"}
	}

	out := StrategyGenerationContext{
		SchemaVersion:  StrategyGenerationInputSchemaVersion,
		Mode:           input.Mode,
		UserIntent:     strings.TrimSpace(input.UserIntent),
		TimeHorizon:    strings.TrimSpace(input.TimeHorizon),
		AllowedActions: input.AllowedActions,
		BuiltAt:        time.Now(),
		Portfolio:      PortfolioReviewContext{Portfolio: portfolio},
		Freshness:      freshness,
	}

	if snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolio.ID, 1); err != nil {
		return StrategyGenerationContext{}, err
	} else if len(snapshots) > 0 {
		snapshot := snapshots[0]
		out.Portfolio.Snapshot = &snapshot
		freshness["portfolioSnapshot"] = map[string]any{
			"status":              snapshot.Status,
			"valuationAt":         snapshot.ValuationAt,
			"staleQuoteCount":     snapshot.StaleQuoteCount,
			"estimatedQuoteCount": snapshot.EstimatedQuoteCount,
		}
	} else {
		markMissing("portfolioSnapshot")
	}

	holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
	if err != nil {
		return StrategyGenerationContext{}, err
	}
	out.Portfolio.Holdings = holdings
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
	freshness["operationRecords"] = map[string]any{"status": "missing", "reason": "no separate operation record repository; transactions are included"}
	missing = append(missing, "operationRecords")

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
	if out.Portfolio.Snapshot != nil && out.Portfolio.Snapshot.TotalAssetValue > 0 {
		totalAsset = out.Portfolio.Snapshot.TotalAssetValue
	} else {
		for _, holding := range holdings {
			totalAsset += strategyGenerationHoldingMarketValue(holding, quotesBySymbol)
		}
	}
	out.Diagnostics.Cash = portfolio.Cash
	out.Diagnostics.TotalAssetValue = totalAsset
	if totalAsset > 0 {
		out.Diagnostics.CashPct = portfolio.Cash / totalAsset * 100
	}
	out.Diagnostics.HoldingCount = len(holdings)

	for _, holding := range holdings {
		hctx, err := s.buildStrategyGenerationHoldingContext(ctx, portfolio.ID, holding, totalAsset, quotesBySymbol, reviews, transactions, &missing)
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
	out.MissingItems = compactStrings(missing)
	return out, nil
}

func (s *Service) RunStrategyGeneration(ctx context.Context, input StrategyGenerationInput, requestedBy string) (AgentRun, error) {
	pack, err := s.BuildStrategyGenerationContext(ctx, input)
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
	inputArtifact, _ := json.Marshal(map[string]any{
		"task":    AgentTaskTypeStrategyGeneration,
		"context": pack,
	})
	portfolioID := pack.Portfolio.Portfolio.ID
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeStrategyGeneration,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "strategy_generation",
		TriggerObjectID:      portfolioID,
		RequestedBy:          requestedBy,
		InputSummary:         fmt.Sprintf("strategy_generation mode=%s portfolio_id=%s holdings=%d", pack.Mode, portfolioID, len(pack.Holdings)),
		InputArtifactSummary: string(inputArtifact),
	})
	if err != nil {
		return AgentRun{}, err
	}
	if s.agentExecutor != nil {
		go s.startStrategyGenerationAgentRunAsync(context.Background(), run, ledger, pack, model.ModelName)
	}
	return run, nil
}

func (s *Service) startStrategyGenerationAgentRunAsync(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack StrategyGenerationContext, modelName string) {
	defer func() {
		if r := recover(); r != nil {
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update strategy generation agent run to running failed", "run_id", run.ID, "error", err)
	}
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecuteStrategyGeneration(ctx, taskID, pack, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
}

func (s *Service) buildStrategyGenerationHoldingContext(ctx context.Context, portfolioID string, holding StockV2Holding, totalAsset float64, quotes map[string]StockV2QuoteLatest, reviews []OperationReview, transactions []StockV2Transaction, missing *[]string) (StrategyGenerationHoldingContext, error) {
	hctx := StrategyGenerationHoldingContext{
		Symbol:            strings.TrimSpace(holding.Symbol),
		Market:            strings.TrimSpace(holding.Market),
		Name:              strings.TrimSpace(holding.Name),
		CostPrice:         holding.CostPrice,
		Quantity:          holding.Quantity,
		AvailableQuantity: holding.AvailableQuantity,
		CurrentPrice:      holding.LastPrice,
		MarketValue:       holding.MarketValue,
		PnL:               holding.PnL,
		PositionPct:       holding.PositionPct,
		Holding:           holding,
		Freshness:         map[string]any{},
	}
	if quote, ok := quotes[hctx.Symbol]; ok {
		hctx.Quote = &quote
		hctx.CurrentPrice = quote.LastPrice
		hctx.Freshness["quote"] = quoteFreshnessSummary(quote)
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
	if coverage, err := s.strategyGenerationCoverage(ctx, portfolioID, hctx.Symbol); err == nil {
		hctx.StrategyCoverage = coverage
	} else {
		return StrategyGenerationHoldingContext{}, err
	}
	hctx.RecentReviews = filterReviewsBySymbol(reviews, hctx.Symbol, 5)
	hctx.Transactions = filterTransactionsBySymbol(transactions, hctx.Symbol, 5)
	return hctx, nil
}

func (s *Service) applyStrategyGenerationResult(ctx context.Context, run AgentRun, result map[string]any, confidence float64) ([]StrategyWithVersion, error) {
	if stringFromAny(result["schema_version"]) != StrategyGenerationReportSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version must be %s", ErrInvalidStrategyGenerationResult, StrategyGenerationReportSchemaVersion)
	}
	portfolioID := strings.TrimSpace(run.TriggerObjectID)
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
		if strings.TrimSpace(holding.Symbol) != "" {
			held[holding.Symbol] = holding
		}
	}

	created := make([]StrategyWithVersion, 0)
	for _, rawDraft := range anySlice(result["drafts"]) {
		draft := mapFromAny(rawDraft)
		symbol := strings.TrimSpace(stringFromAny(draft["symbol"]))
		if symbol == "" {
			continue
		}
		holding, ok := held[symbol]
		if !ok {
			continue
		}
		draftType, err := strategyGenerationDraftType(draft)
		if err != nil {
			return nil, err
		}
		if draftType != StrategyGenerationDraftNewStrategy {
			continue
		}
		coverage, err := s.strategyGenerationCoverage(ctx, portfolioID, symbol)
		if err != nil {
			return nil, err
		}
		if coverage.HasStrategy {
			continue
		}
		playbook, err := normalizeStrategyGenerationPlaybook(mapFromAny(draft["playbook"]))
		if err != nil {
			return nil, err
		}
		item, err := s.CreateStrategy(ctx, RequestCreateStrategy{
			Name:            strategyGenerationDraftName(portfolio, holding, draft),
			Kind:            StrategyKindSymbolStrategy,
			Scope:           StrategyScopePortfolioBound,
			Source:          StrategySourceAgent,
			Status:          StrategyStatusDraft,
			Symbol:          symbol,
			Market:          firstNonEmpty(stringFromAny(draft["market"]), holding.Market),
			PortfolioID:     portfolioID,
			Title:           firstNonEmpty(stringFromAny(draft["title"]), stringFromAny(draft["name"]), "Agent 组合诊断策略草案"),
			Direction:       normalizeStrategyGenerationBias(stringFromAny(draft["strategy_bias"])),
			Thesis:          strings.TrimSpace(stringFromAny(draft["thesis"])),
			EntryConditions: playbookRuleTriggers(playbook),
			ExitConditions:  stringsFromAny(draft["invalid_conditions"]),
			RiskNotes:       strings.Join(stringsFromAny(draft["risk_summary"]), "\n"),
			EvidenceRefs:    stringsFromAny(draft["evidence_summary"]),
			GenerationMeta: map[string]any{
				"source":        AgentTaskTypeStrategyGeneration,
				"mode":          StrategyGenerationModePortfolio,
				"runID":         run.ID,
				"portfolioId":   portfolioID,
				"schemaVersion": StrategyGenerationReportSchemaVersion,
				"playbook":      playbook,
				"strategyGeneration": map[string]any{
					"draftType":                StrategyGenerationDraftNewStrategy,
					"confidence":               firstNonZeroNumber(draft["confidence"], confidence),
					"runSummary":               result["run_summary"],
					"portfolioAwareSuggestion": mapFromAny(draft["portfolio_aware_suggestion"]),
					"reviewRequest":            stringFromAny(mapFromAny(draft["portfolio_aware_suggestion"])["review_request"]),
				},
			},
			CreatedBy: StrategySourceAgent,
		})
		if err != nil {
			return nil, err
		}
		created = append(created, item)
	}
	return created, nil
}

func normalizeStrategyGenerationInput(input StrategyGenerationInput) StrategyGenerationInput {
	input.SchemaVersion = firstNonEmpty(strings.TrimSpace(input.SchemaVersion), StrategyGenerationInputSchemaVersion)
	input.Mode = strings.TrimSpace(input.Mode)
	if input.Mode == "" {
		input.Mode = StrategyGenerationModePortfolio
	}
	input.PortfolioID = strings.TrimSpace(input.PortfolioID)
	if len(input.AllowedActions) == 0 {
		input.AllowedActions = []string{
			StrategyGenerationActionObserve,
			StrategyGenerationActionBuildPosition,
			StrategyGenerationActionAddPosition,
			StrategyGenerationActionHold,
			StrategyGenerationActionReduce,
			StrategyGenerationActionExit,
		}
	}
	return input
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
		if item.Strategy.PortfolioID != "" && item.Strategy.PortfolioID != portfolioID {
			continue
		}
		summary := StrategyGenerationStrategySummary{
			ID:     item.Strategy.ID,
			Name:   item.Strategy.Name,
			Status: item.Strategy.Status,
			Scope:  item.Strategy.Scope,
			Source: item.Strategy.Source,
		}
		if item.ActiveVersion != nil {
			summary.VersionID = item.ActiveVersion.ID
			summary.Title = item.ActiveVersion.Title
			summary.Direction = item.ActiveVersion.Direction
		}
		coverage.Strategies = append(coverage.Strategies, summary)
		switch item.Strategy.Status {
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

func normalizeStrategyGenerationPlaybook(playbook map[string]any) (map[string]any, error) {
	rawRules := anySlice(playbook["rules"])
	if len(rawRules) == 0 {
		return nil, fmt.Errorf("%w: playbook.rules is required", ErrInvalidStrategyGenerationResult)
	}
	rules := make([]any, 0, len(rawRules))
	for i, raw := range rawRules {
		rule := mapFromAny(raw)
		action := strings.TrimSpace(stringFromAny(rule["action"]))
		if !validStrategyGenerationRuleAction(action) {
			return nil, fmt.Errorf("%w: invalid playbook rule action %q", ErrInvalidStrategyGenerationResult, action)
		}
		id := strings.TrimSpace(stringFromAny(rule["id"]))
		if id == "" {
			id = fmt.Sprintf("%s_%d", action, i+1)
		}
		priority := i + 1
		if n, ok := numberFromAny(rule["priority"]); ok {
			priority = int(n)
		}
		rules = append(rules, map[string]any{
			"id":                  id,
			"action":              action,
			"title":               stringFromAny(rule["title"]),
			"trigger":             stringFromAny(rule["trigger"]),
			"preconditions":       stringFromAny(rule["preconditions"]),
			"target":              stringFromAny(rule["target"]),
			"risk":                stringFromAny(rule["risk"]),
			"dataPrefilters":      anySlice(rule["dataPrefilters"]),
			"portfolioPrefilters": anySlice(rule["portfolioPrefilters"]),
			"newsPrefilters":      anySlice(rule["newsPrefilters"]),
			"priority":            priority,
		})
	}
	version := strings.TrimSpace(stringFromAny(playbook["version"]))
	if version == "" {
		version = "v1"
	}
	return map[string]any{"version": version, "rules": rules}, nil
}

func validStrategyGenerationRuleAction(action string) bool {
	switch action {
	case StrategyGenerationActionObserve,
		StrategyGenerationActionBuildPosition,
		StrategyGenerationActionAddPosition,
		StrategyGenerationActionHold,
		StrategyGenerationActionReduce,
		StrategyGenerationActionExit:
		return true
	default:
		return false
	}
}

func strategyGenerationDraftType(draft map[string]any) (string, error) {
	switch strings.TrimSpace(stringFromAny(draft["draft_type"])) {
	case StrategyGenerationDraftNewStrategy:
		return StrategyGenerationDraftNewStrategy, nil
	case StrategyGenerationDraftPatch:
		return StrategyGenerationDraftPatch, nil
	case StrategyGenerationDraftNoChange:
		return StrategyGenerationDraftNoChange, nil
	default:
		return "", fmt.Errorf("%w: invalid draft_type", ErrInvalidStrategyGenerationResult)
	}
}

func normalizeStrategyGenerationBias(value string) string {
	switch strings.TrimSpace(value) {
	case StrategyBiasBullish:
		return StrategyBiasBullish
	case StrategyBiasBearish:
		return StrategyBiasBearish
	case StrategyBiasNeutral:
		return StrategyBiasNeutral
	default:
		return StrategyDirectionWatch
	}
}

func strategyGenerationDraftName(portfolio StockV2Portfolio, holding StockV2Holding, draft map[string]any) string {
	if name := strings.TrimSpace(stringFromAny(draft["strategy_name"])); name != "" {
		return name
	}
	display := firstNonEmpty(strings.TrimSpace(holding.Name), strings.TrimSpace(stringFromAny(draft["name"])), holding.Symbol)
	return fmt.Sprintf("%s Agent组合诊断策略草案 - %s", portfolio.Name, display)
}

func playbookRuleTriggers(playbook map[string]any) []string {
	out := make([]string, 0)
	for _, raw := range anySlice(playbook["rules"]) {
		rule := mapFromAny(raw)
		trigger := strings.TrimSpace(stringFromAny(rule["trigger"]))
		if trigger != "" {
			out = append(out, trigger)
		}
	}
	return out
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
	if quote, ok := quotes[holding.Symbol]; ok && quote.LastPrice > 0 {
		return quote.LastPrice * holding.Quantity
	}
	if holding.LastPrice > 0 {
		return holding.LastPrice * holding.Quantity
	}
	return 0
}

func anySlice(value any) []any {
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
		return []any{}
	}
}

func firstNonZeroNumber(values ...any) float64 {
	for _, value := range values {
		if n, ok := numberFromAny(value); ok && n != 0 {
			return n
		}
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
