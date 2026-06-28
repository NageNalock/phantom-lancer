package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) RunOpportunityDiscovery(ctx context.Context, input OpportunityDiscoveryInput) (OpportunityDiscoveryRun, error) {
	normalized, err := normalizeOpportunityDiscoveryInput(input)
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
	discCtx, err := s.BuildOpportunityDiscoveryContext(ctx, normalized)
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	discoveryRun, err := s.store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{
		OpportunityID: discCtx.Opportunity.ID,
		Status:        OpportunityDiscoveryRunStatusPending,
		StepTotal:     8,
	})
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	discCtx.DiscoveryRun = discoveryRun
	artifact, _ := json.Marshal(discCtx)
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeOpportunityDiscovery,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "opportunity",
		TriggerObjectID:      discCtx.Opportunity.ID,
		RequestedBy:          normalized.RequestedBy,
		InputSummary:         opportunityDiscoveryInputSummary(discCtx.Opportunity),
		InputArtifactSummary: string(artifact),
	})
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	discoveryRun.AgentRunID = run.ID
	if discoveryRun, err = s.store.UpdateOpportunityDiscoveryRun(ctx, discoveryRun); err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	discCtx.DiscoveryRun = discoveryRun
	if s.agentExecutor != nil {
		if normalized.Async {
			go s.startOpportunityDiscoveryRunAsync(context.Background(), run, ledger, discCtx, model.ModelName)
		} else {
			if _, _, execErr := s.executeOpportunityDiscoveryRun(ctx, run, ledger, discCtx, model.ModelName); execErr != nil {
				finalRun, finalErr := s.store.GetOpportunityDiscoveryRun(ctx, discoveryRun.ID)
				if finalErr == nil {
					return finalRun, nil
				}
				return OpportunityDiscoveryRun{}, execErr
			}
			return s.store.GetOpportunityDiscoveryRun(ctx, discoveryRun.ID)
		}
	}
	return discoveryRun, nil
}

func (s *Service) BuildOpportunityDiscoveryContext(ctx context.Context, input OpportunityDiscoveryInput) (OpportunityDiscoveryContext, error) {
	normalized, err := normalizeOpportunityDiscoveryInput(input)
	if err != nil {
		return OpportunityDiscoveryContext{}, err
	}
	opp, err := s.store.GetOpportunity(ctx, normalized.OpportunityID)
	if err != nil {
		return OpportunityDiscoveryContext{}, err
	}
	embeddingStatus, err := s.GetEmbeddingStatus(ctx)
	if err != nil {
		embeddingStatus = map[string]any{"available": false, "status": "embedding_status_unavailable", "reason": "embedding status query failed"}
	}
	return OpportunityDiscoveryContext{
		BuiltAt:         time.Now(),
		Input:           normalized,
		Opportunity:     opp,
		EmbeddingStatus: embeddingStatus,
		FreshnessSummary: map[string]any{
			"builtAt":            time.Now().Format(time.RFC3339),
			"opportunityStatus":  opp.Status,
			"embeddingAvailable": embeddingStatus["available"],
		},
	}, nil
}

func (s *Service) runOpportunityDiscoveryCLIDebug(ctx context.Context, req RequestRunAgentCLIDebug, model AgentModelProfile) (AgentExecutionDetail, error) {
	opp, err := s.store.CreateOpportunity(ctx, Opportunity{
		Title:       "Agent CLI debug opportunity discovery",
		UserThesis:  "验证 Codex CLI 外部搜索、stock_agent MCP step/source/evidence/candidate 记录与 submit_result 回填全链路；请用中文返回。",
		MarketScope: "a_share",
		Status:      OpportunityStatusDraft,
		CreatedBy:   req.RequestedBy,
	})
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	discCtx, err := s.BuildOpportunityDiscoveryContext(ctx, OpportunityDiscoveryInput{
		OpportunityID: opp.ID,
		RequestedBy:   req.RequestedBy,
		Async:         req.Async,
	})
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	discoveryRun, err := s.store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{
		OpportunityID: opp.ID,
		Status:        OpportunityDiscoveryRunStatusPending,
		StepTotal:     8,
	})
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	discCtx.DiscoveryRun = discoveryRun
	artifact, _ := json.Marshal(discCtx)
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeOpportunityDiscovery,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "opportunity",
		TriggerObjectID:      opp.ID,
		RequestedBy:          req.RequestedBy,
		InputSummary:         "CLI debug: verify opportunity_discovery search, MCP step, external source, candidate, and submit_result chain in Chinese.",
		InputArtifactSummary: string(artifact),
	})
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	discoveryRun.AgentRunID = run.ID
	if discoveryRun, err = s.store.UpdateOpportunityDiscoveryRun(ctx, discoveryRun); err != nil {
		return AgentExecutionDetail{}, err
	}
	discCtx.DiscoveryRun = discoveryRun
	if req.Async {
		go s.startOpportunityDiscoveryRunAsync(context.Background(), run, ledger, discCtx, model.ModelName)
		return s.GetAgentExecutionDetail(ctx, run.ID)
	}
	if _, _, err := s.executeOpportunityDiscoveryRun(ctx, run, ledger, discCtx, model.ModelName); err != nil {
		detail, detailErr := s.GetAgentExecutionDetail(ctx, run.ID)
		if detailErr == nil {
			return detail, nil
		}
		return AgentExecutionDetail{}, err
	}
	return s.GetAgentExecutionDetail(ctx, run.ID)
}

func normalizeOpportunityDiscoveryInput(input OpportunityDiscoveryInput) (OpportunityDiscoveryInput, error) {
	input.OpportunityID = strings.TrimSpace(input.OpportunityID)
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	if input.OpportunityID == "" {
		return OpportunityDiscoveryInput{}, errors.New("opportunityId is required")
	}
	return input, nil
}

func opportunityDiscoveryInputSummary(opp Opportunity) string {
	return safelog.Text(fmt.Sprintf("Opportunity discovery: %s thesis=%s", opp.Title, opp.UserThesis), 1000)
}

func (s *Service) startOpportunityDiscoveryRunAsync(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	discCtx OpportunityDiscoveryContext,
	modelName string,
) {
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("opportunity discovery agent run panicked", "run_id", run.ID, "panic", r)
			}
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}
	if _, _, err := s.executeOpportunityDiscoveryRun(ctx, run, ledger, discCtx, modelName); err != nil && s.log != nil {
		s.log.Warn("opportunity discovery agent run finished with error", "run_id", run.ID, "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) executeOpportunityDiscoveryRun(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	discCtx OpportunityDiscoveryContext,
	modelName string,
) (AgentRun, AgentDecisionLedger, error) {
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
		return finalRun, finalLedger, ErrAgentExecutorUnavailable
	}
	now := time.Now()
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update opportunity discovery run to running failed", "run_id", run.ID, "error", err)
	}
	discoveryRun := discCtx.DiscoveryRun
	discoveryRun.Status = OpportunityDiscoveryRunStatusRunning
	if discoveryRun.StartedAt.IsZero() {
		discoveryRun.StartedAt = now
	}
	if _, err := s.store.UpdateOpportunityDiscoveryRun(ctx, discoveryRun); err != nil && s.log != nil {
		s.log.Warn("update opportunity discovery run state failed", "discovery_run_id", discoveryRun.ID, "error", err)
	}
	if opp, err := s.store.GetOpportunity(ctx, discoveryRun.OpportunityID); err == nil && opp.Status == OpportunityStatusDraft {
		opp.Status = OpportunityStatusResearching
		_, _ = s.store.UpdateOpportunity(ctx, opp)
	}
	taskID, _ := s.agentTaskPool.createOpportunityDiscoveryTask(run.ID, discoveryRun.ID, discoveryRun.OpportunityID, 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecuteOpportunityDiscovery(ctx, taskID, discCtx, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execErr
}

func (s *Service) saveOpportunityDiscoveryResult(ctx context.Context, run AgentRun, submitted AgentTaskSubmittedResult) ([]OpportunityCandidate, error) {
	discoveryRun, err := s.store.GetOpportunityDiscoveryRunByAgentRunID(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	report, err := opportunityDiscoveryReportFromResult(submitted.Result)
	if err != nil {
		return nil, err
	}
	if report.OpportunityID != discoveryRun.OpportunityID || report.OpportunityID != run.TriggerObjectID {
		return nil, errors.New("opportunity_id does not match run")
	}
	if containsForbiddenOpportunityResultKey(submitted.Result) {
		return nil, errors.New("opportunity discovery result must not contain operation or strategy activation instructions")
	}
	saved := make([]OpportunityCandidate, 0, len(report.Candidates))
	for _, item := range report.Candidates {
		inst, err := s.store.GetInstrument(ctx, strings.TrimSpace(item.Symbol))
		if err != nil {
			return nil, fmt.Errorf("candidate symbol %s is not in StockV2 master data", item.Symbol)
		}
		candidate := OpportunityCandidate{
			OpportunityID:   discoveryRun.OpportunityID,
			RunID:           discoveryRun.ID,
			Symbol:          inst.Symbol,
			Market:          inst.Market,
			InstrumentType:  inst.InstrumentType,
			Name:            inst.Name,
			RelationType:    firstNonEmpty(item.RelationType, "theme_related"),
			RelevanceScore:  item.RelevanceScore,
			EvidenceScore:   item.EvidenceScore,
			MarketRiskScore: item.MarketRiskScore,
			Confidence:      item.Confidence,
			Rank:            item.Rank,
			Status:          OpportunityCandidateStatusCandidate,
			Reason:          safelog.Text(item.Reason, 2000),
			RiskSummary:     safelog.Text(item.RiskSummary, 1000),
			Metadata: map[string]any{
				"source":                  "submit_result",
				"suggestedStrategyIntent": safelog.Text(item.SuggestedStrategyIntent, 1000),
			},
		}
		if candidate.Rank <= 0 {
			candidate.Rank = 999
		}
		if err := validateOpportunityCandidateScores(candidate); err != nil {
			return nil, err
		}
		created, err := s.store.UpsertOpportunityCandidate(ctx, candidate)
		if err != nil {
			return nil, err
		}
		saved = append(saved, created)
	}
	if _, err := s.store.CreateOpportunityDiscoveryResult(ctx, OpportunityDiscoveryResult{
		RunID:                 discoveryRun.ID,
		Summary:               safelog.Text(firstNonEmpty(report.Summary, submitted.ResultSummary), 2000),
		Conclusion:            safelog.Text(report.Summary, 2000),
		RecommendedNextAction: safelog.Text(report.RecommendedNextAction, 1000),
		RawResult:             submitted.Result,
	}); err != nil {
		return nil, err
	}
	discoveryRun.Status = OpportunityDiscoveryRunStatusCompleted
	discoveryRun.ErrorMessage = ""
	discoveryRun.FinishedAt = time.Now()
	if _, err := s.store.UpdateOpportunityDiscoveryRun(ctx, discoveryRun); err != nil {
		return nil, err
	}
	if _, err := s.store.RefreshOpportunityDiscoveryRunCounts(ctx, discoveryRun.ID); err != nil {
		return nil, err
	}
	if opp, err := s.store.GetOpportunity(ctx, discoveryRun.OpportunityID); err == nil {
		opp.Status = OpportunityStatusCompleted
		_, _ = s.store.UpdateOpportunity(ctx, opp)
	}
	return saved, nil
}

func (s *Service) markOpportunityDiscoveryRunFailed(ctx context.Context, run AgentRun, message string) {
	if run.TaskType != AgentTaskTypeOpportunityDiscovery {
		return
	}
	discoveryRun, err := s.store.GetOpportunityDiscoveryRunByAgentRunID(ctx, run.ID)
	if err != nil {
		return
	}
	discoveryRun.Status = OpportunityDiscoveryRunStatusFailed
	discoveryRun.ErrorMessage = safelog.Text(message, 500)
	discoveryRun.FinishedAt = time.Now()
	_, _ = s.store.UpdateOpportunityDiscoveryRun(ctx, discoveryRun)
}

type opportunityDiscoveryReport struct {
	SchemaVersion         string                             `json:"schema_version"`
	OpportunityID         string                             `json:"opportunity_id"`
	Summary               string                             `json:"summary"`
	RecommendedNextAction string                             `json:"recommended_next_action"`
	Candidates            []opportunityDiscoveryCandidateOut `json:"candidates"`
}

type opportunityDiscoveryCandidateOut struct {
	Symbol                  string  `json:"symbol"`
	Market                  string  `json:"market"`
	Name                    string  `json:"name"`
	InstrumentType          string  `json:"instrument_type"`
	RelationType            string  `json:"relation_type"`
	Rank                    int     `json:"rank"`
	RelevanceScore          float64 `json:"relevance_score"`
	EvidenceScore           float64 `json:"evidence_score"`
	MarketRiskScore         float64 `json:"market_risk_score"`
	Confidence              float64 `json:"confidence"`
	Reason                  string  `json:"reason"`
	RiskSummary             string  `json:"risk_summary"`
	SuggestedStrategyIntent string  `json:"suggested_strategy_intent"`
}

func opportunityDiscoveryReportFromResult(result map[string]any) (opportunityDiscoveryReport, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return opportunityDiscoveryReport{}, err
	}
	var report opportunityDiscoveryReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return opportunityDiscoveryReport{}, err
	}
	if report.SchemaVersion != OpportunityDiscoveryReportSchemaVersion {
		return opportunityDiscoveryReport{}, errors.New("schema_version must be opportunity-discovery-report/v1")
	}
	report.OpportunityID = strings.TrimSpace(report.OpportunityID)
	if report.OpportunityID == "" {
		return opportunityDiscoveryReport{}, errors.New("opportunity_id is required")
	}
	return report, nil
}

func containsForbiddenOpportunityResultKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch normalized {
			case "proposed_operation", "proposedoperation", "operation_review", "operationreview", "holding_change", "holdingchange", "activate_strategy", "activatestrategy":
				return true
			}
			if containsForbiddenOpportunityResultKey(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsForbiddenOpportunityResultKey(item) {
				return true
			}
		}
	}
	return false
}
