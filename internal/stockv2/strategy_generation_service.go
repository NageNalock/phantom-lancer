package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
		Targets:          make([]StrategyGenerationInstrumentContext, 0, len(normalized.TargetInstruments)),
		FreshnessSummary: map[string]any{},
	}
	for _, target := range normalized.TargetInstruments {
		item, err := s.strategyGenerationInstrumentContext(ctx, target)
		if err != nil {
			return StrategyGenerationContext{}, err
		}
		out.Targets = append(out.Targets, item)
	}
	out.FreshnessSummary["targetCount"] = len(out.Targets)
	out.FreshnessSummary["builtAt"] = out.BuiltAt.Format(time.RFC3339)
	return out, nil
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
	rowCount, earliest, latest, source, lastErr, err := s.store.GetDailyBarsStats(ctx, instrument.Symbol, DailyBarAdjustedNone)
	if err == nil {
		item.DailyBars = &StrategyGenerationBarsSummary{
			Adjusted:  DailyBarAdjustedNone,
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

func normalizeStrategyGenerationInput(input StrategyGenerationInput) (StrategyGenerationInput, error) {
	input.Mode = strings.TrimSpace(input.Mode)
	if input.Mode == "" {
		input.Mode = StrategyGenerationModeManualTarget
	}
	if !validStrategyGenerationMode(input.Mode) {
		return StrategyGenerationInput{}, ErrInvalidStrategyGenerationInput
	}
	input.UserGoal = strings.TrimSpace(input.UserGoal)
	input.PortfolioID = strings.TrimSpace(input.PortfolioID)
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	targets := make([]StrategyGenerationTargetInstrument, 0, len(input.TargetInstruments))
	seen := map[string]bool{}
	for _, target := range input.TargetInstruments {
		target.Symbol = strings.TrimSpace(target.Symbol)
		target.Market = strings.TrimSpace(target.Market)
		target.Name = strings.TrimSpace(target.Name)
		if target.Symbol == "" || seen[target.Symbol] {
			continue
		}
		seen[target.Symbol] = true
		targets = append(targets, target)
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
	if input.PortfolioID != "" {
		return fmt.Sprintf("%s:portfolio=%s:symbols=%s", input.Mode, input.PortfolioID, strings.Join(parts, ","))
	}
	return strings.TrimSpace(input.Mode) + ":symbols=" + strings.Join(parts, ",")
}

func strategyGenerationInputSummary(input StrategyGenerationInput) string {
	return fmt.Sprintf("strategy_generation mode=%s targets=%s goal=%s", input.Mode, strategyGenerationTriggerID(input), input.UserGoal)
}

func strategyGenerationReportFromResult(raw map[string]any) (StrategyGenerationReport, error) {
	if len(raw) == 0 {
		return StrategyGenerationReport{}, ErrInvalidStrategyGenerationResult
	}
	if err := rejectLegacyStrategyGenerationPlaybookShape(raw); err != nil {
		return StrategyGenerationReport{}, err
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

func validateStrategyGenerationReport(report StrategyGenerationReport) error {
	if strings.TrimSpace(report.SchemaVersion) != StrategyGenerationReportSchemaVersion {
		return ErrInvalidStrategyGenerationResult
	}
	if !validStrategyGenerationMode(strings.TrimSpace(report.RunSummary.Mode)) {
		return ErrInvalidStrategyGenerationResult
	}
	if len(report.Drafts) == 0 {
		return ErrInvalidStrategyGenerationResult
	}
	for _, draft := range report.Drafts {
		if !validStrategyGenerationDraftType(strings.TrimSpace(draft.DraftType)) {
			return ErrInvalidStrategyGenerationResult
		}
		if draft.DraftType != StrategyGenerationDraftTypeNewStrategy {
			continue
		}
		if strings.TrimSpace(draft.Symbol) == "" || strings.TrimSpace(draft.Thesis) == "" {
			return ErrInvalidStrategyGenerationResult
		}
		if _, err := strategyGenerationDraftDirection(draft); err != nil {
			return ErrInvalidStrategyGenerationResult
		}
		if len(draft.Playbook.Rules) == 0 {
			return ErrInvalidStrategyGenerationResult
		}
		for _, rule := range draft.Playbook.Rules {
			if strings.TrimSpace(rule.ID) == "" || !validStrategyGenerationRuleAction(strings.TrimSpace(rule.Action)) {
				return ErrInvalidStrategyGenerationResult
			}
		}
	}
	return nil
}

func (s *Service) createDraftStrategiesFromStrategyGeneration(ctx context.Context, run AgentRun, submitted AgentTaskSubmittedResult, report StrategyGenerationReport) ([]StrategyWithVersion, error) {
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
			},
		},
		CreatedBy: StrategySourceAgent,
	}, nil
}

func strategyGenerationPortfolioIDFromTrigger(triggerID string) string {
	for _, part := range strings.Split(triggerID, ":") {
		if value, ok := strings.CutPrefix(part, "portfolio="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
