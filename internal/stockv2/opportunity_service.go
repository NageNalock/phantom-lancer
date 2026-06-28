package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) CreateOpportunity(ctx context.Context, req RequestCreateOpportunity) (Opportunity, error) {
	title := strings.TrimSpace(req.Title)
	thesis := strings.TrimSpace(req.UserThesis)
	if title == "" || thesis == "" {
		return Opportunity{}, ErrInvalidOpportunityInput
	}
	marketScope := strings.TrimSpace(req.MarketScope)
	if marketScope == "" {
		marketScope = OpportunityMarketScopeAShare
	}
	instrumentScope := strings.TrimSpace(req.InstrumentScope)
	if instrumentScope == "" {
		instrumentScope = OpportunityInstrumentScopeBoth
	}
	if !validOpportunityMarketScope(marketScope) || !validOpportunityInstrumentScope(instrumentScope) {
		return Opportunity{}, ErrInvalidOpportunityInput
	}
	return s.store.CreateOpportunity(ctx, Opportunity{
		Title:           title,
		UserThesis:      thesis,
		MarketScope:     marketScope,
		InstrumentScope: instrumentScope,
		Status:          OpportunityStatusDraft,
		CreatedBy:       strings.TrimSpace(req.CreatedBy),
	})
}

func (s *Service) GetOpportunity(ctx context.Context, id string) (Opportunity, error) {
	return s.store.GetOpportunity(ctx, strings.TrimSpace(id))
}

func (s *Service) ListOpportunities(ctx context.Context, filter OpportunityListFilter) ([]Opportunity, error) {
	return s.store.ListOpportunities(ctx, normalizeOpportunityListFilter(filter))
}

func (s *Service) CountOpportunities(ctx context.Context, filter OpportunityListFilter) (int, error) {
	return s.store.CountOpportunities(ctx, normalizeOpportunityListFilter(filter))
}

func (s *Service) UpdateOpportunity(ctx context.Context, id string, req RequestUpdateOpportunity) (Opportunity, error) {
	item, err := s.store.GetOpportunity(ctx, strings.TrimSpace(id))
	if err != nil {
		return Opportunity{}, err
	}
	if req.Title != nil {
		item.Title = strings.TrimSpace(*req.Title)
	}
	if req.UserThesis != nil {
		item.UserThesis = strings.TrimSpace(*req.UserThesis)
	}
	if req.MarketScope != nil {
		item.MarketScope = strings.TrimSpace(*req.MarketScope)
	}
	if req.InstrumentScope != nil {
		item.InstrumentScope = strings.TrimSpace(*req.InstrumentScope)
	}
	if req.Status != nil {
		item.Status = strings.TrimSpace(*req.Status)
	}
	if item.Title == "" || item.UserThesis == "" ||
		!validOpportunityMarketScope(item.MarketScope) ||
		!validOpportunityInstrumentScope(item.InstrumentScope) ||
		!validOpportunityStatus(item.Status) {
		return Opportunity{}, ErrInvalidOpportunityInput
	}
	return s.store.UpdateOpportunity(ctx, item)
}

func (s *Service) DeleteOpportunity(ctx context.Context, id string) error {
	return s.store.DeleteOpportunity(ctx, strings.TrimSpace(id))
}

func normalizeOpportunityListFilter(filter OpportunityListFilter) OpportunityListFilter {
	filter.Status = strings.TrimSpace(filter.Status)
	filter.MarketScope = strings.TrimSpace(filter.MarketScope)
	filter.InstrumentScope = strings.TrimSpace(filter.InstrumentScope)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return filter
}

func (s *Service) StartOpportunityDiscoveryRun(ctx context.Context, opportunityID string, req RequestStartOpportunityDiscoveryRun) (OpportunityDiscoveryRun, error) {
	opp, err := s.store.GetOpportunity(ctx, strings.TrimSpace(opportunityID))
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeOpportunityDiscovery)
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	model, err := s.resolveModel(ctx, taskProfile)
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	artifact, _ := json.Marshal(map[string]any{
		"opportunity": opp,
		"boundary": map[string]any{
			"externalResearch": "codex_cli_builtin",
			"mainProgramMCP":   "internal_stock_data_and_trace_only",
		},
	})
	runRecord, _, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeOpportunityDiscovery,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "opportunity",
		TriggerObjectID:      opp.ID,
		RequestedBy:          req.RequestedBy,
		InputSummary:         fmt.Sprintf("opportunity_discovery title=%s thesis=%s", opp.Title, opp.UserThesis),
		InputArtifactSummary: string(artifact),
	})
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	steps := make([]OpportunityDiscoveryStep, 0, len(defaultOpportunityDiscoverySteps))
	for i, def := range defaultOpportunityDiscoverySteps {
		steps = append(steps, OpportunityDiscoveryStep{
			StepKey:    def.Key,
			StepTitle:  def.Title,
			Status:     OpportunityDiscoveryStepStatusPending,
			OrderIndex: i + 1,
			Metadata:   map[string]any{},
		})
	}
	run, _, err := s.store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{
		OpportunityID: opp.ID,
		AgentRunID:    runRecord.ID,
		Status:        OpportunityDiscoveryRunStatusPending,
	}, steps)
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	if opp.Status == OpportunityStatusDraft || opp.Status == OpportunityStatusCompleted {
		opp.Status = OpportunityStatusResearching
		_, _ = s.store.UpdateOpportunity(ctx, opp)
	}
	return run, nil
}

func (s *Service) GetOpportunityDiscoveryRun(ctx context.Context, id string) (OpportunityDiscoveryRun, error) {
	return s.store.GetOpportunityDiscoveryRun(ctx, strings.TrimSpace(id))
}

func (s *Service) ListOpportunityDiscoveryRuns(ctx context.Context, filter DiscoveryRunListFilter) ([]OpportunityDiscoveryRun, error) {
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return s.store.ListOpportunityDiscoveryRuns(ctx, filter)
}

func (s *Service) CountOpportunityDiscoveryRuns(ctx context.Context, filter DiscoveryRunListFilter) (int, error) {
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return s.store.CountOpportunityDiscoveryRuns(ctx, filter)
}

func (s *Service) ListOpportunityDiscoverySteps(ctx context.Context, filter DiscoveryStepListFilter) ([]OpportunityDiscoveryStep, error) {
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return s.store.ListOpportunityDiscoverySteps(ctx, filter)
}

func (s *Service) CountOpportunityDiscoverySteps(ctx context.Context, filter DiscoveryStepListFilter) (int, error) {
	return s.store.CountOpportunityDiscoverySteps(ctx, filter)
}

func (s *Service) StartOpportunityDiscoveryStep(ctx context.Context, runID, stepKey, inputSummary string, metadata map[string]any) (OpportunityDiscoveryStep, error) {
	step, err := s.store.GetOpportunityDiscoveryStepByKey(ctx, strings.TrimSpace(runID), strings.TrimSpace(stepKey))
	if err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	step.Status = OpportunityDiscoveryStepStatusRunning
	step.InputSummary = safelog.Text(inputSummary, 2000)
	step.Metadata = mergeMaps(step.Metadata, metadata)
	step.StartedAt = time.Now()
	step.FinishedAt = time.Time{}
	updated, err := s.store.UpdateOpportunityDiscoveryStep(ctx, step)
	if err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	run, err := s.store.GetOpportunityDiscoveryRun(ctx, updated.RunID)
	if err == nil {
		run.Status = OpportunityDiscoveryRunStatusRunning
		run.CurrentStepID = updated.ID
		if run.StartedAt.IsZero() {
			run.StartedAt = time.Now()
		}
		_, _ = s.store.UpdateOpportunityDiscoveryRun(ctx, run)
	}
	return updated, nil
}

func (s *Service) FinishOpportunityDiscoveryStep(ctx context.Context, runID, stepKey, outputSummary string, metadata map[string]any) (OpportunityDiscoveryStep, error) {
	step, err := s.store.GetOpportunityDiscoveryStepByKey(ctx, strings.TrimSpace(runID), strings.TrimSpace(stepKey))
	if err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	step.Status = OpportunityDiscoveryStepStatusCompleted
	step.OutputSummary = safelog.Text(outputSummary, 3000)
	step.Metadata = mergeMaps(step.Metadata, metadata)
	step.FinishedAt = time.Now()
	updated, err := s.store.UpdateOpportunityDiscoveryStep(ctx, step)
	if err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	_ = s.refreshOpportunityRunCounters(ctx, updated.RunID)
	return updated, nil
}

func (s *Service) FailOpportunityDiscoveryStep(ctx context.Context, runID, stepKey, outputSummary string, metadata map[string]any) (OpportunityDiscoveryStep, error) {
	step, err := s.store.GetOpportunityDiscoveryStepByKey(ctx, strings.TrimSpace(runID), strings.TrimSpace(stepKey))
	if err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	step.Status = OpportunityDiscoveryStepStatusFailed
	step.OutputSummary = safelog.Text(outputSummary, 3000)
	step.Metadata = mergeMaps(step.Metadata, metadata)
	step.FinishedAt = time.Now()
	updated, err := s.store.UpdateOpportunityDiscoveryStep(ctx, step)
	if err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	run, err := s.store.GetOpportunityDiscoveryRun(ctx, updated.RunID)
	if err == nil {
		run.Status = OpportunityDiscoveryRunStatusFailed
		run.ErrorMessage = safelog.Text(outputSummary, 500)
		run.FinishedAt = time.Now()
		_, _ = s.store.UpdateOpportunityDiscoveryRun(ctx, run)
	}
	return updated, nil
}

func mergeMaps(base, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

func (s *Service) ListOpportunityEvidence(ctx context.Context, filter OpportunityEvidenceListFilter) ([]OpportunityEvidence, error) {
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return s.store.ListOpportunityEvidence(ctx, filter)
}

func (s *Service) CountOpportunityEvidence(ctx context.Context, filter OpportunityEvidenceListFilter) (int, error) {
	return s.store.CountOpportunityEvidence(ctx, filter)
}

func (s *Service) RecordOpportunityEvidence(ctx context.Context, item OpportunityEvidence) (OpportunityEvidence, error) {
	run, err := s.store.GetOpportunityDiscoveryRun(ctx, strings.TrimSpace(item.RunID))
	if err != nil {
		return OpportunityEvidence{}, err
	}
	item.RunID = run.ID
	item.SourceType = strings.TrimSpace(item.SourceType)
	item.SourceRef = strings.TrimSpace(item.SourceRef)
	item.Title = strings.TrimSpace(item.Title)
	item.Summary = safelog.Text(item.Summary, 3000)
	item.URL = sanitizeOpportunityURL(item.URL)
	item.Publisher = strings.TrimSpace(item.Publisher)
	if item.Title == "" || item.SourceType == "" || item.Confidence < 0 || item.Confidence > 1 {
		return OpportunityEvidence{}, ErrInvalidOpportunityInput
	}
	if item.CandidateID != "" {
		candidate, err := s.store.GetOpportunityCandidate(ctx, item.CandidateID)
		if err != nil {
			return OpportunityEvidence{}, err
		}
		if candidate.RunID != item.RunID {
			return OpportunityEvidence{}, ErrInvalidOpportunityInput
		}
	}
	created, err := s.store.CreateOpportunityEvidence(ctx, item)
	if err != nil {
		return OpportunityEvidence{}, err
	}
	_ = s.refreshOpportunityRunCounters(ctx, item.RunID)
	return created, nil
}

func sanitizeOpportunityURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return safelog.Text(trimmed, 500)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return safelog.Text(parsed.String(), 500)
}

func (s *Service) ListOpportunityCandidates(ctx context.Context, filter OpportunityCandidateListFilter) ([]OpportunityCandidate, error) {
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return s.store.ListOpportunityCandidates(ctx, filter)
}

func (s *Service) CountOpportunityCandidates(ctx context.Context, filter OpportunityCandidateListFilter) (int, error) {
	return s.store.CountOpportunityCandidates(ctx, filter)
}

func (s *Service) GetOpportunityCandidate(ctx context.Context, id string) (OpportunityCandidate, error) {
	return s.store.GetOpportunityCandidate(ctx, strings.TrimSpace(id))
}

func (s *Service) RecordOpportunityCandidate(ctx context.Context, item OpportunityCandidate) (OpportunityCandidate, error) {
	run, err := s.store.GetOpportunityDiscoveryRun(ctx, strings.TrimSpace(item.RunID))
	if err != nil {
		return OpportunityCandidate{}, err
	}
	inst, err := s.store.GetInstrument(ctx, strings.TrimSpace(item.Symbol))
	if err != nil {
		if errors.Is(err, ErrInstrumentNotFound) {
			return OpportunityCandidate{}, ErrOpportunitySymbolNotFound
		}
		return OpportunityCandidate{}, err
	}
	item.OpportunityID = run.OpportunityID
	item.RunID = run.ID
	item.Symbol = inst.Symbol
	item.Market = inst.Market
	item.Name = inst.Name
	item.InstrumentType = inst.InstrumentType
	if item.InstrumentType == "" {
		item.InstrumentType = InstrumentTypeStock
	}
	if item.Status == "" {
		item.Status = OpportunityCandidateStatusCandidate
	}
	if item.RelationType == "" {
		item.RelationType = OpportunityRelationWeak
	}
	if err := validateOpportunityCandidate(item); err != nil {
		return OpportunityCandidate{}, err
	}
	created, err := s.store.UpsertOpportunityCandidate(ctx, item)
	if err != nil {
		return OpportunityCandidate{}, err
	}
	_ = s.refreshOpportunityRunCounters(ctx, item.RunID)
	return created, nil
}

func validateOpportunityCandidate(item OpportunityCandidate) error {
	if strings.TrimSpace(item.Symbol) == "" ||
		!validOpportunityCandidateStatus(item.Status) ||
		!validOpportunityRelationType(item.RelationType) ||
		!score0To100(item.RelevanceScore) ||
		!score0To100(item.EvidenceScore) ||
		!score0To100(item.MarketRiskScore) ||
		item.Confidence < 0 || item.Confidence > 1 {
		return ErrInvalidOpportunityCandidate
	}
	return nil
}

func score0To100(value float64) bool {
	return value >= 0 && value <= 100
}

func (s *Service) UpdateOpportunityCandidate(ctx context.Context, id string, req RequestUpdateOpportunityCandidate) (OpportunityCandidate, error) {
	item, err := s.store.GetOpportunityCandidate(ctx, strings.TrimSpace(id))
	if err != nil {
		return OpportunityCandidate{}, err
	}
	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if !validOpportunityCandidateStatus(status) {
			return OpportunityCandidate{}, ErrInvalidOpportunityCandidate
		}
		item.Status = status
	}
	if req.Reason != nil {
		item.Reason = safelog.Text(*req.Reason, 3000)
	}
	if req.RiskSummary != nil {
		item.RiskSummary = safelog.Text(*req.RiskSummary, 3000)
	}
	if req.Metadata != nil {
		item.Metadata = req.Metadata
	}
	return s.store.UpdateOpportunityCandidate(ctx, item)
}

func (s *Service) GetOpportunityResultByRunID(ctx context.Context, runID string) (OpportunityResult, error) {
	return s.store.GetOpportunityResultByRunID(ctx, strings.TrimSpace(runID))
}

func (s *Service) ProcessOpportunityDiscoverySubmittedResult(ctx context.Context, runID string, submitted AgentTaskSubmittedResult) (OpportunityResult, error) {
	run, err := s.store.GetOpportunityDiscoveryRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		return OpportunityResult{}, err
	}
	report, raw, err := s.opportunityDiscoveryReportFromResult(ctx, run, submitted.Result)
	if err != nil {
		run.Status = OpportunityDiscoveryRunStatusFailed
		run.ErrorMessage = safelog.Text(err.Error(), 500)
		run.FinishedAt = time.Now()
		_, _ = s.store.UpdateOpportunityDiscoveryRun(ctx, run)
		return OpportunityResult{}, err
	}
	for _, candidate := range report.Candidates {
		inst, err := s.store.GetInstrument(ctx, strings.TrimSpace(candidate.Symbol))
		if err != nil {
			if errors.Is(err, ErrInstrumentNotFound) {
				return OpportunityResult{}, ErrOpportunitySymbolNotFound
			}
			return OpportunityResult{}, err
		}
		relationType := strings.TrimSpace(candidate.RelationType)
		if relationType == "" {
			relationType = OpportunityRelationWeak
		}
		instrumentType := strings.TrimSpace(candidate.InstrumentType)
		if instrumentType == "" {
			instrumentType = inst.InstrumentType
		}
		_, err = s.RecordOpportunityCandidate(ctx, OpportunityCandidate{
			OpportunityID:   run.OpportunityID,
			RunID:           run.ID,
			Symbol:          inst.Symbol,
			Market:          firstNonEmptyOpportunity(candidate.Market, inst.Market),
			InstrumentType:  instrumentType,
			Name:            firstNonEmptyOpportunity(candidate.Name, inst.Name),
			RelationType:    relationType,
			RelevanceScore:  candidate.RelevanceScore,
			EvidenceScore:   candidate.EvidenceScore,
			MarketRiskScore: candidate.MarketRiskScore,
			Confidence:      candidate.Confidence,
			Rank:            candidate.Rank,
			Status:          OpportunityCandidateStatusCandidate,
			Reason:          safelog.Text(candidate.Reason, 3000),
			RiskSummary:     safelog.Text(candidate.RiskSummary, 3000),
			Metadata: map[string]any{
				"suggestedStrategyIntent": safelog.Text(candidate.SuggestedStrategyIntent, 1000),
				"source":                  "submit_result",
			},
		})
		if err != nil {
			return OpportunityResult{}, err
		}
	}
	result, err := s.store.UpsertOpportunityResult(ctx, OpportunityResult{
		RunID:                 run.ID,
		Summary:               safelog.Text(report.Summary, 3000),
		Conclusion:            safelog.Text(firstNonEmptyOpportunity(report.Conclusion, report.Summary), 3000),
		RecommendedNextAction: safelog.Text(report.RecommendedNextAction, 1000),
		RawResult:             raw,
	})
	if err != nil {
		return OpportunityResult{}, err
	}
	run.Status = OpportunityDiscoveryRunStatusCompleted
	run.FinishedAt = time.Now()
	run.ErrorMessage = ""
	_, _ = s.store.UpdateOpportunityDiscoveryRun(ctx, run)
	_ = s.refreshOpportunityRunCounters(ctx, run.ID)
	if opp, err := s.store.GetOpportunity(ctx, run.OpportunityID); err == nil {
		opp.Status = OpportunityStatusCompleted
		_, _ = s.store.UpdateOpportunity(ctx, opp)
	}
	return result, nil
}

func (s *Service) opportunityDiscoveryReportFromResult(ctx context.Context, run OpportunityDiscoveryRun, raw map[string]any) (OpportunityDiscoveryReport, map[string]any, error) {
	if len(raw) == 0 {
		return OpportunityDiscoveryReport{}, nil, ErrInvalidOpportunityResult
	}
	if opportunityResultHasForbiddenAction(raw) {
		return OpportunityDiscoveryReport{}, nil, ErrOpportunityUnsafeResult
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return OpportunityDiscoveryReport{}, nil, err
	}
	var report OpportunityDiscoveryReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return OpportunityDiscoveryReport{}, nil, ErrInvalidOpportunityResult
	}
	if strings.TrimSpace(report.SchemaVersion) != OpportunityDiscoveryReportSchemaVersion ||
		strings.TrimSpace(report.OpportunityID) != run.OpportunityID ||
		strings.TrimSpace(report.Summary) == "" {
		return OpportunityDiscoveryReport{}, nil, ErrInvalidOpportunityResult
	}
	for _, candidate := range report.Candidates {
		if strings.TrimSpace(candidate.Symbol) == "" ||
			!score0To100(candidate.RelevanceScore) ||
			!score0To100(candidate.EvidenceScore) ||
			!score0To100(candidate.MarketRiskScore) ||
			candidate.Confidence < 0 || candidate.Confidence > 1 {
			return OpportunityDiscoveryReport{}, nil, ErrInvalidOpportunityResult
		}
		if _, err := s.store.GetInstrument(ctx, strings.TrimSpace(candidate.Symbol)); err != nil {
			if errors.Is(err, ErrInstrumentNotFound) {
				return OpportunityDiscoveryReport{}, nil, ErrOpportunitySymbolNotFound
			}
			return OpportunityDiscoveryReport{}, nil, err
		}
	}
	if err := validateSemanticRecallTrace(raw); err != nil {
		return OpportunityDiscoveryReport{}, nil, err
	}
	return report, raw, nil
}

func opportunityResultHasForbiddenAction(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbiddenOpportunityResultKey(key) {
				return true
			}
			if opportunityResultHasForbiddenAction(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if opportunityResultHasForbiddenAction(child) {
				return true
			}
		}
	}
	return false
}

func forbiddenOpportunityResultKey(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
	case "proposedoperation", "proposedorder", "order", "orderticket", "tradeorder",
		"holdingupdate", "positionchange", "portfoliomutation", "activatestrategy",
		"strategyactivation":
		return true
	default:
		return false
	}
}

func validateSemanticRecallTrace(raw map[string]any) error {
	for _, candidateRaw := range sliceFromAny(raw["candidates"]) {
		candidate := mapFromAny(candidateRaw)
		method := strings.ToLower(firstNonEmptyOpportunity(
			stringFromAny(candidate["recall_method"]),
			stringFromAny(candidate["recallMode"]),
			stringFromAny(candidate["match_method"]),
			stringFromAny(candidate["matchMethod"]),
		))
		if !strings.Contains(method, "semantic") && !strings.Contains(method, "vector") {
			continue
		}
		modelID := firstNonEmptyOpportunity(stringFromAny(candidate["embedding_model_id"]), stringFromAny(candidate["embeddingModelId"]))
		assetIDs := append(sliceStringsFromAny(candidate["embedding_asset_ids"]), sliceStringsFromAny(candidate["embeddingAssetIds"])...)
		assetIDs = append(assetIDs, sliceStringsFromAny(candidate["vector_asset_ids"])...)
		if strings.TrimSpace(modelID) == "" || len(assetIDs) == 0 {
			return ErrInvalidOpportunityResult
		}
	}
	return nil
}

func sliceStringsFromAny(value any) []string {
	items := make([]string, 0)
	for _, raw := range sliceFromAny(value) {
		if text := strings.TrimSpace(stringFromAny(raw)); text != "" {
			items = append(items, text)
		}
	}
	return items
}

func (s *Service) GenerateStrategyFromOpportunityCandidate(ctx context.Context, candidateID string, req RequestGenerateStrategyFromOpportunityCandidate) (AgentRun, error) {
	candidate, err := s.store.GetOpportunityCandidate(ctx, strings.TrimSpace(candidateID))
	if err != nil {
		return AgentRun{}, err
	}
	goal := strings.TrimSpace(req.UserGoal)
	if goal == "" {
		goal = fmt.Sprintf("从机会候选生成策略草案：%s %s。相关原因：%s 风险：%s", candidate.Symbol, candidate.Name, candidate.Reason, candidate.RiskSummary)
	}
	run, err := s.RunStrategyGeneration(ctx, StrategyGenerationInput{
		SchemaVersion: StrategyGenerationInputSchemaVersion,
		Mode:          StrategyGenerationModeOpportunity,
		UserGoal:      goal,
		RequestedBy:   req.RequestedBy,
		OpportunityID: candidate.OpportunityID,
		CandidateID:   candidate.ID,
		TimeHorizon:   strings.TrimSpace(req.TimeHorizon),
		TargetInstruments: []StrategyGenerationTargetInstrument{{
			Symbol:   candidate.Symbol,
			Market:   candidate.Market,
			Name:     candidate.Name,
			UserNote: candidate.Reason,
		}},
		EvidenceScope: map[string]bool{
			"stockProfile": true,
			"recentNews":   true,
			"quote":        true,
			"dailyBars":    true,
			"opportunity":  true,
		},
	})
	if err != nil {
		return AgentRun{}, err
	}
	candidate.Status = OpportunityCandidateStatusStrategyRequested
	if candidate.Metadata == nil {
		candidate.Metadata = map[string]any{}
	}
	candidate.Metadata["strategyGenerationRunId"] = run.ID
	_, _ = s.store.UpdateOpportunityCandidate(ctx, candidate)
	return run, nil
}

func (s *Service) refreshOpportunityRunCounters(ctx context.Context, runID string) error {
	run, err := s.store.GetOpportunityDiscoveryRun(ctx, runID)
	if err != nil {
		return err
	}
	steps, err := s.store.ListOpportunityDiscoverySteps(ctx, DiscoveryStepListFilter{RunID: runID, Limit: 200})
	if err != nil {
		return err
	}
	completed := 0
	for _, step := range steps {
		if step.Status == OpportunityDiscoveryStepStatusCompleted {
			completed++
		}
	}
	candidateCount, err := s.store.CountOpportunityCandidates(ctx, OpportunityCandidateListFilter{RunID: runID})
	if err != nil {
		return err
	}
	evidenceCount, err := s.store.CountOpportunityEvidence(ctx, OpportunityEvidenceListFilter{RunID: runID})
	if err != nil {
		return err
	}
	externalCount, err := s.store.CountOpportunityEvidence(ctx, OpportunityEvidenceListFilter{RunID: runID, SourceType: OpportunityEvidenceSourceExternal})
	if err != nil {
		return err
	}
	run.StepTotal = len(steps)
	run.StepCompleted = completed
	run.CandidateCount = candidateCount
	run.EvidenceCount = evidenceCount
	run.ExternalSourceCount = externalCount
	_, err = s.store.UpdateOpportunityDiscoveryRun(ctx, run)
	return err
}

func firstNonEmptyOpportunity(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
