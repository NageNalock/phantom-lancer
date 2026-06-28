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

func opportunityDiscoveryMCPTools() []mcpTool {
	simple := func(desc string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"description":          desc,
			"additionalProperties": true,
		}
	}
	return []mcpTool{
		{Name: "stock_agent.search_instruments", Description: "Search StockV2 master data instruments by keyword, market, and instrumentType. Requires an opportunity_discovery taskID.", InputSchema: simple("taskID, keyword, market, instrumentType, limit")},
		{Name: "stock_agent.search_stock_profiles", Description: "Search StockV2 stock profiles by keyword. Requires an opportunity_discovery taskID.", InputSchema: simple("taskID, keyword, limit")},
		{Name: "stock_agent.get_stock_profile", Description: "Get one StockV2 stock profile by symbol. Requires an opportunity_discovery taskID.", InputSchema: simple("taskID, symbol")},
		{Name: "stock_agent.get_latest_quotes", Description: "Get latest local quotes for symbols. Requires an opportunity_discovery taskID.", InputSchema: simple("taskID, symbols")},
		{Name: "stock_agent.get_daily_bars_summary", Description: "Get local daily K summary for one symbol. Requires an opportunity_discovery taskID.", InputSchema: simple("taskID, symbol")},
		{Name: "stock_agent.list_existing_strategies", Description: "List existing strategies for a symbol. Requires an opportunity_discovery taskID.", InputSchema: simple("taskID, symbol")},
		{Name: "stock_agent.get_embedding_status", Description: "Return StockV2 embedding model binding and availability. This never falls back to keyword search.", InputSchema: simple("optional taskID")},
		{Name: "stock_agent.start_discovery_step", Description: "Mark an opportunity discovery step as running. Requires taskID, runID, opportunityID.", InputSchema: simple("taskID, runID, opportunityID, stepKey, stepTitle, orderIndex, inputSummary, metadata")},
		{Name: "stock_agent.finish_discovery_step", Description: "Mark an opportunity discovery step as completed. Requires taskID, runID, opportunityID.", InputSchema: simple("taskID, runID, opportunityID, stepID or stepKey, outputSummary, metadata")},
		{Name: "stock_agent.fail_discovery_step", Description: "Mark an opportunity discovery step as failed without failing the whole run. Requires taskID, runID, opportunityID.", InputSchema: simple("taskID, runID, opportunityID, stepID or stepKey, errorMessage, outputSummary, metadata")},
		{Name: "stock_agent.record_external_source", Description: "Persist one external public source used by the discovery run. Requires taskID, runID, opportunityID.", InputSchema: simple("taskID, runID, opportunityID, stepID, title, url, publisher, publishedAt, summary, relatedSymbols, confidence")},
		{Name: "stock_agent.record_evidence", Description: "Persist one evidence item used by the discovery run. Requires taskID, runID, opportunityID.", InputSchema: simple("taskID, runID, opportunityID, candidateID, sourceType, sourceRef, title, summary, url, publisher, publishedAt, confidence, metadata")},
		{Name: "stock_agent.record_candidate", Description: "Persist or replace one validated opportunity candidate. Symbol must exist in StockV2 master data. Requires taskID, runID, opportunityID.", InputSchema: simple("taskID, runID, opportunityID, symbol, relationType, rank, scores, confidence, reason, riskSummary, metadata")},
		{Name: "stock_agent.update_candidate", Description: "Patch one existing candidate in this discovery run. Requires taskID, runID, opportunityID and candidateID or symbol.", InputSchema: simple("taskID, runID, opportunityID, candidateID or symbol, status, rank, scores, reason, riskSummary, metadata")},
	}
}

func (p *agentTaskPool) mcpOpportunityToolsCall(name string, args json.RawMessage) (any, bool, *mcpError) {
	switch name {
	case "stock_agent.search_instruments":
		result, err := p.mcpSearchInstruments(args)
		return result, true, err
	case "stock_agent.search_stock_profiles":
		result, err := p.mcpSearchStockProfiles(args)
		return result, true, err
	case "stock_agent.get_stock_profile":
		result, err := p.mcpGetStockProfile(args)
		return result, true, err
	case "stock_agent.get_latest_quotes":
		result, err := p.mcpGetLatestQuotes(args)
		return result, true, err
	case "stock_agent.get_daily_bars_summary":
		result, err := p.mcpGetDailyBarsSummary(args)
		return result, true, err
	case "stock_agent.list_existing_strategies":
		result, err := p.mcpListExistingStrategies(args)
		return result, true, err
	case "stock_agent.get_embedding_status":
		result, err := p.mcpGetEmbeddingStatus(args)
		return result, true, err
	case "stock_agent.start_discovery_step":
		result, err := p.mcpStartDiscoveryStep(args)
		return result, true, err
	case "stock_agent.finish_discovery_step":
		result, err := p.mcpFinishDiscoveryStep(args)
		return result, true, err
	case "stock_agent.fail_discovery_step":
		result, err := p.mcpFailDiscoveryStep(args)
		return result, true, err
	case "stock_agent.record_external_source":
		result, err := p.mcpRecordExternalSource(args)
		return result, true, err
	case "stock_agent.record_evidence":
		result, err := p.mcpRecordEvidence(args)
		return result, true, err
	case "stock_agent.record_candidate":
		result, err := p.mcpRecordCandidate(args)
		return result, true, err
	case "stock_agent.update_candidate":
		result, err := p.mcpUpdateCandidate(args)
		return result, true, err
	default:
		return nil, false, nil
	}
}

type discoveryScopeParams struct {
	TaskID           string `json:"taskID"`
	RunID            string `json:"runID"`
	RunIDAlt         string `json:"runId"`
	OpportunityID    string `json:"opportunityID"`
	OpportunityIDAlt string `json:"opportunityId"`
}

func (p discoveryScopeParams) normalized() discoveryScopeParams {
	p.TaskID = strings.TrimSpace(p.TaskID)
	p.RunID = firstNonEmpty(strings.TrimSpace(p.RunID), strings.TrimSpace(p.RunIDAlt))
	p.OpportunityID = firstNonEmpty(strings.TrimSpace(p.OpportunityID), strings.TrimSpace(p.OpportunityIDAlt))
	return p
}

func (p *agentTaskPool) requireOpportunityTask(taskID string) (*agentTaskEntry, *mcpError) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "taskID is required"}
	}
	entry, ok := p.getTask(taskID)
	if !ok {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: ErrTaskNotFound.Error()}
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if time.Now().After(entry.deadline) || entry.status == agentTaskStatusExpired {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: ErrTaskExpired.Error()}
	}
	if entry.taskType != AgentTaskTypeOpportunityDiscovery {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: ErrTaskTypeMismatch.Error()}
	}
	if entry.status != agentTaskStatusWaiting {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "agent task is not accepting discovery updates"}
	}
	return entry, nil
}

func (p *agentTaskPool) requireDiscoveryRun(scope discoveryScopeParams) (*agentTaskEntry, OpportunityDiscoveryRun, *mcpError) {
	scope = scope.normalized()
	entry, err := p.requireOpportunityTask(scope.TaskID)
	if err != nil {
		return nil, OpportunityDiscoveryRun{}, err
	}
	if p.service == nil || p.service.store == nil {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInternal, Message: "stock service is not configured"}
	}
	if scope.RunID == "" {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "runID is required"}
	}
	if scope.OpportunityID == "" {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "opportunityID is required"}
	}
	run, storeErr := p.service.store.GetOpportunityDiscoveryRun(context.Background(), scope.RunID)
	if storeErr != nil {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: storeErr.Error()}
	}
	entry.mu.Lock()
	agentRunID := entry.agentRunID
	discoveryRunID := entry.discoveryRunID
	opportunityID := entry.opportunityID
	entry.mu.Unlock()
	if discoveryRunID != "" && discoveryRunID != run.ID {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "runID does not match task"}
	}
	if opportunityID != "" && opportunityID != run.OpportunityID {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "opportunityID does not match task"}
	}
	if run.AgentRunID != "" && agentRunID != "" && run.AgentRunID != agentRunID {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "agentRunID does not match task"}
	}
	if run.OpportunityID != scope.OpportunityID {
		return nil, OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "opportunityID does not match discovery run"}
	}
	return entry, run, nil
}

func (p *agentTaskPool) mcpSearchInstruments(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID         string `json:"taskID"`
		Keyword        string `json:"keyword"`
		Market         string `json:"market"`
		InstrumentType string `json:"instrumentType"`
		Limit          int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
		return nil, err
	}
	if err := p.requireStockService(); err != nil {
		return nil, err
	}
	items, err := p.service.SearchInstrumentsFiltered(context.Background(), params.Keyword, params.Market, params.InstrumentType, limitForMCP(params.Limit, 20, 100))
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	return mcpDataResult("instruments found", map[string]any{"items": items, "count": len(items)}), nil
}

func (p *agentTaskPool) mcpSearchStockProfiles(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID  string `json:"taskID"`
		Keyword string `json:"keyword"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
		return nil, err
	}
	if err := p.requireStockService(); err != nil {
		return nil, err
	}
	items, err := p.service.store.ListStockProfiles(context.Background(), StockProfileListFilter{Keyword: strings.TrimSpace(params.Keyword), Limit: limitForMCP(params.Limit, 20, 100)})
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	return mcpDataResult("stock profiles found", map[string]any{"items": items, "count": len(items)}), nil
}

func (p *agentTaskPool) mcpGetStockProfile(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID string `json:"taskID"`
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
		return nil, err
	}
	if err := p.requireStockService(); err != nil {
		return nil, err
	}
	profile, err := p.service.store.GetStockProfile(context.Background(), strings.TrimSpace(params.Symbol))
	if err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	return mcpDataResult("stock profile found", map[string]any{"profile": profile}), nil
}

func (p *agentTaskPool) mcpGetLatestQuotes(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID  string   `json:"taskID"`
		Symbols []string `json:"symbols"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
		return nil, err
	}
	if err := p.requireStockService(); err != nil {
		return nil, err
	}
	quotes, err := p.service.store.GetLatestQuotes(context.Background(), params.Symbols)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	return mcpDataResult("latest quotes found", map[string]any{"items": quotes, "count": len(quotes)}), nil
}

func (p *agentTaskPool) mcpGetDailyBarsSummary(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID string `json:"taskID"`
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
		return nil, err
	}
	if err := p.requireStockService(); err != nil {
		return nil, err
	}
	rowCount, earliest, latest, source, lastErr, err := p.service.store.GetDailyBarsStats(context.Background(), strings.TrimSpace(params.Symbol), DailyBarAdjustedNone)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	return mcpDataResult("daily bars summary found", map[string]any{
		"symbol":    strings.TrimSpace(params.Symbol),
		"adjusted":  DailyBarAdjustedNone,
		"rowCount":  rowCount,
		"earliest":  earliest,
		"latest":    latest,
		"source":    source,
		"lastError": lastErr,
		"hasData":   rowCount > 0,
	}), nil
}

func (p *agentTaskPool) mcpListExistingStrategies(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID string `json:"taskID"`
		Symbol string `json:"symbol"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
		return nil, err
	}
	if err := p.requireStockService(); err != nil {
		return nil, err
	}
	items, err := p.service.store.ListStrategies(context.Background(), StrategyListFilter{Symbol: strings.TrimSpace(params.Symbol), Limit: limitForMCP(params.Limit, 20, 100)})
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	return mcpDataResult("strategies found", map[string]any{"items": items, "count": len(items)}), nil
}

func (p *agentTaskPool) mcpGetEmbeddingStatus(args json.RawMessage) (any, *mcpError) {
	var params struct {
		TaskID string `json:"taskID"`
	}
	_ = json.Unmarshal(args, &params)
	if strings.TrimSpace(params.TaskID) != "" {
		if _, err := p.requireOpportunityTask(params.TaskID); err != nil {
			return nil, err
		}
	}
	if p.service == nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: "stock service is not configured"}
	}
	status, err := p.service.GetEmbeddingStatus(context.Background())
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	return mcpDataResult("embedding status checked", status), nil
}

func (p *agentTaskPool) mcpStartDiscoveryStep(args json.RawMessage) (any, *mcpError) {
	var params struct {
		discoveryScopeParams
		StepKey      string         `json:"stepKey"`
		StepTitle    string         `json:"stepTitle"`
		OrderIndex   int            `json:"orderIndex"`
		InputSummary string         `json:"inputSummary"`
		Metadata     map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	_, run, mcpErr := p.requireDiscoveryRun(params.discoveryScopeParams)
	if mcpErr != nil {
		return nil, mcpErr
	}
	stepKey := strings.TrimSpace(params.StepKey)
	if stepKey == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "stepKey is required"}
	}
	title := firstNonEmpty(strings.TrimSpace(params.StepTitle), stepKey)
	step, err := p.service.store.UpsertOpportunityDiscoveryStep(context.Background(), OpportunityDiscoveryStep{
		RunID:        run.ID,
		StepKey:      stepKey,
		StepTitle:    safelog.Text(title, 200),
		Status:       OpportunityDiscoveryStepStatusRunning,
		OrderIndex:   params.OrderIndex,
		InputSummary: safelog.Text(params.InputSummary, 1000),
		Metadata:     sanitizeDiscoveryMetadata(params.Metadata),
		StartedAt:    time.Now(),
	})
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	run.Status = OpportunityDiscoveryRunStatusRunning
	run.CurrentStepID = step.ID
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	if _, err := p.service.store.UpdateOpportunityDiscoveryRun(context.Background(), run); err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	p.markOpportunityResearching(context.Background(), run.OpportunityID)
	return mcpDataResult("discovery step started", map[string]any{"step": step}), nil
}

func (p *agentTaskPool) mcpFinishDiscoveryStep(args json.RawMessage) (any, *mcpError) {
	return p.mcpCompleteDiscoveryStep(args, OpportunityDiscoveryStepStatusCompleted)
}

func (p *agentTaskPool) mcpFailDiscoveryStep(args json.RawMessage) (any, *mcpError) {
	return p.mcpCompleteDiscoveryStep(args, OpportunityDiscoveryStepStatusFailed)
}

func (p *agentTaskPool) mcpCompleteDiscoveryStep(args json.RawMessage, status string) (any, *mcpError) {
	var params struct {
		discoveryScopeParams
		StepID        string         `json:"stepID"`
		StepIDAlt     string         `json:"stepId"`
		StepKey       string         `json:"stepKey"`
		OutputSummary string         `json:"outputSummary"`
		ErrorMessage  string         `json:"errorMessage"`
		Metadata      map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	_, run, mcpErr := p.requireDiscoveryRun(params.discoveryScopeParams)
	if mcpErr != nil {
		return nil, mcpErr
	}
	step, mcpErr := p.loadDiscoveryStep(run.ID, firstNonEmpty(params.StepID, params.StepIDAlt), params.StepKey)
	if mcpErr != nil {
		return nil, mcpErr
	}
	step.Status = status
	step.OutputSummary = safelog.Text(firstNonEmpty(params.OutputSummary, params.ErrorMessage), 1000)
	step.Metadata = sanitizeDiscoveryMetadata(mergeDiscoveryMetadata(step.Metadata, params.Metadata, params.ErrorMessage))
	step.FinishedAt = time.Now()
	updated, err := p.service.store.UpsertOpportunityDiscoveryStep(context.Background(), step)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	run.CurrentStepID = updated.ID
	if _, err := p.service.store.UpdateOpportunityDiscoveryRun(context.Background(), run); err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	_, _ = p.service.store.RefreshOpportunityDiscoveryRunCounts(context.Background(), run.ID)
	text := "discovery step finished"
	if status == OpportunityDiscoveryStepStatusFailed {
		text = "discovery step failed"
	}
	return mcpDataResult(text, map[string]any{"step": updated}), nil
}

func (p *agentTaskPool) mcpRecordExternalSource(args json.RawMessage) (any, *mcpError) {
	var params struct {
		discoveryScopeParams
		StepID         string         `json:"stepID"`
		StepIDAlt      string         `json:"stepId"`
		Title          string         `json:"title"`
		URL            string         `json:"url"`
		Publisher      string         `json:"publisher"`
		PublishedAt    string         `json:"publishedAt"`
		Summary        string         `json:"summary"`
		RelatedSymbols []string       `json:"relatedSymbols"`
		Confidence     float64        `json:"confidence"`
		Metadata       map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	_, run, mcpErr := p.requireDiscoveryRun(params.discoveryScopeParams)
	if mcpErr != nil {
		return nil, mcpErr
	}
	publishedAt, err := parseMCPOptionalTime(params.PublishedAt)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	cleanURL := sanitizeDiscoveryURL(params.URL)
	meta := sanitizeDiscoveryMetadata(params.Metadata)
	meta["stepID"] = firstNonEmpty(params.StepID, params.StepIDAlt)
	meta["relatedSymbols"] = params.RelatedSymbols
	ev, err := p.service.store.CreateOpportunityEvidence(context.Background(), OpportunityEvidence{
		RunID:       run.ID,
		SourceType:  OpportunityEvidenceSourceExternal,
		SourceRef:   cleanURL,
		Title:       safelog.Text(firstNonEmpty(params.Title, cleanURL, "external source"), 500),
		Summary:     safelog.Text(params.Summary, 2000),
		URL:         cleanURL,
		Publisher:   safelog.Text(params.Publisher, 200),
		PublishedAt: publishedAt,
		Confidence:  params.Confidence,
		Metadata:    meta,
	})
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	_, _ = p.service.store.RefreshOpportunityDiscoveryRunCounts(context.Background(), run.ID)
	return mcpDataResult("external source recorded", map[string]any{"evidence": ev}), nil
}

func (p *agentTaskPool) mcpRecordEvidence(args json.RawMessage) (any, *mcpError) {
	var params struct {
		discoveryScopeParams
		CandidateID    string         `json:"candidateID"`
		CandidateIDAlt string         `json:"candidateId"`
		SourceType     string         `json:"sourceType"`
		SourceRef      string         `json:"sourceRef"`
		Title          string         `json:"title"`
		Summary        string         `json:"summary"`
		URL            string         `json:"url"`
		Publisher      string         `json:"publisher"`
		PublishedAt    string         `json:"publishedAt"`
		Confidence     float64        `json:"confidence"`
		Metadata       map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	_, run, mcpErr := p.requireDiscoveryRun(params.discoveryScopeParams)
	if mcpErr != nil {
		return nil, mcpErr
	}
	candidateID := firstNonEmpty(params.CandidateID, params.CandidateIDAlt)
	if candidateID != "" {
		if _, mcpErr := p.requireCandidateInRun(run, candidateID); mcpErr != nil {
			return nil, mcpErr
		}
	}
	publishedAt, err := parseMCPOptionalTime(params.PublishedAt)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	sourceType := firstNonEmpty(params.SourceType, OpportunityEvidenceSourceAgent)
	cleanURL := sanitizeDiscoveryURL(params.URL)
	ev, err := p.service.store.CreateOpportunityEvidence(context.Background(), OpportunityEvidence{
		RunID:       run.ID,
		CandidateID: candidateID,
		SourceType:  sourceType,
		SourceRef:   safelog.Text(firstNonEmpty(params.SourceRef, cleanURL), 1000),
		Title:       safelog.Text(firstNonEmpty(params.Title, "evidence"), 500),
		Summary:     safelog.Text(params.Summary, 2000),
		URL:         cleanURL,
		Publisher:   safelog.Text(params.Publisher, 200),
		PublishedAt: publishedAt,
		Confidence:  params.Confidence,
		Metadata:    sanitizeDiscoveryMetadata(params.Metadata),
	})
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	_, _ = p.service.store.RefreshOpportunityDiscoveryRunCounts(context.Background(), run.ID)
	return mcpDataResult("evidence recorded", map[string]any{"evidence": ev}), nil
}

func (p *agentTaskPool) mcpRecordCandidate(args json.RawMessage) (any, *mcpError) {
	var params candidateMCPParams
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	_, run, mcpErr := p.requireDiscoveryRun(params.discoveryScopeParams)
	if mcpErr != nil {
		return nil, mcpErr
	}
	candidate, mcpErr := p.candidateFromMCPParams(run, params, nil)
	if mcpErr != nil {
		return nil, mcpErr
	}
	saved, err := p.service.store.UpsertOpportunityCandidate(context.Background(), candidate)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	_, _ = p.service.store.RefreshOpportunityDiscoveryRunCounts(context.Background(), run.ID)
	return mcpDataResult("candidate recorded", map[string]any{"candidate": saved}), nil
}

func (p *agentTaskPool) mcpUpdateCandidate(args json.RawMessage) (any, *mcpError) {
	var params candidateMCPParams
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, invalidMCPArgs(err)
	}
	_, run, mcpErr := p.requireDiscoveryRun(params.discoveryScopeParams)
	if mcpErr != nil {
		return nil, mcpErr
	}
	existing, mcpErr := p.loadCandidateForPatch(run, params)
	if mcpErr != nil {
		return nil, mcpErr
	}
	candidate, mcpErr := p.candidateFromMCPParams(run, params, &existing)
	if mcpErr != nil {
		return nil, mcpErr
	}
	saved, err := p.service.store.UpsertOpportunityCandidate(context.Background(), candidate)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: err.Error()}
	}
	_, _ = p.service.store.RefreshOpportunityDiscoveryRunCounts(context.Background(), run.ID)
	return mcpDataResult("candidate updated", map[string]any{"candidate": saved}), nil
}

type candidateMCPParams struct {
	discoveryScopeParams
	CandidateID                  string             `json:"candidateID"`
	CandidateIDAlt               string             `json:"candidateId"`
	Symbol                       string             `json:"symbol"`
	Market                       string             `json:"market"`
	Name                         string             `json:"name"`
	InstrumentType               string             `json:"instrumentType"`
	InstrumentTypeSnake          string             `json:"instrument_type"`
	RelationType                 string             `json:"relationType"`
	RelationTypeSnake            string             `json:"relation_type"`
	RelevanceScore               *float64           `json:"relevanceScore"`
	RelevanceScoreSnake          *float64           `json:"relevance_score"`
	EvidenceScore                *float64           `json:"evidenceScore"`
	EvidenceScoreSnake           *float64           `json:"evidence_score"`
	MarketRiskScore              *float64           `json:"marketRiskScore"`
	MarketRiskScoreSnake         *float64           `json:"market_risk_score"`
	Scores                       map[string]float64 `json:"scores"`
	Confidence                   *float64           `json:"confidence"`
	Rank                         *int               `json:"rank"`
	Status                       string             `json:"status"`
	Reason                       string             `json:"reason"`
	RiskSummary                  string             `json:"riskSummary"`
	RiskSummarySnake             string             `json:"risk_summary"`
	SuggestedStrategyIntent      string             `json:"suggestedStrategyIntent"`
	SuggestedStrategyIntentSnake string             `json:"suggested_strategy_intent"`
	Metadata                     map[string]any     `json:"metadata"`
}

func (p *agentTaskPool) candidateFromMCPParams(run OpportunityDiscoveryRun, params candidateMCPParams, existing *OpportunityCandidate) (OpportunityCandidate, *mcpError) {
	symbol := strings.TrimSpace(params.Symbol)
	if existing != nil && symbol == "" {
		symbol = existing.Symbol
	}
	if symbol == "" {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: "symbol is required"}
	}
	inst, err := p.service.store.GetInstrument(context.Background(), symbol)
	if err != nil {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: "candidate symbol is not in StockV2 master data: " + symbol}
	}
	c := OpportunityCandidate{
		OpportunityID:  run.OpportunityID,
		RunID:          run.ID,
		Symbol:         inst.Symbol,
		Market:         inst.Market,
		InstrumentType: inst.InstrumentType,
		Name:           inst.Name,
		RelationType:   firstNonEmpty(params.RelationType, params.RelationTypeSnake, "theme_related"),
		Status:         firstNonEmpty(params.Status, OpportunityCandidateStatusCandidate),
		Reason:         safelog.Text(params.Reason, 2000),
		RiskSummary:    safelog.Text(firstNonEmpty(params.RiskSummary, params.RiskSummarySnake), 1000),
		Metadata:       sanitizeDiscoveryMetadata(params.Metadata),
	}
	if existing != nil {
		c.ID = existing.ID
		c.RelationType = firstNonEmpty(params.RelationType, params.RelationTypeSnake, existing.RelationType, c.RelationType)
		c.Status = firstNonEmpty(params.Status, existing.Status, c.Status)
		c.Reason = firstNonEmpty(c.Reason, existing.Reason)
		c.RiskSummary = firstNonEmpty(c.RiskSummary, existing.RiskSummary)
		c.Rank = existing.Rank
		c.RelevanceScore = existing.RelevanceScore
		c.EvidenceScore = existing.EvidenceScore
		c.MarketRiskScore = existing.MarketRiskScore
		c.Confidence = existing.Confidence
		c.Metadata = mergeDiscoveryMetadata(existing.Metadata, c.Metadata, "")
	}
	if c.InstrumentType == "" && (params.InstrumentType != "" || params.InstrumentTypeSnake != "") {
		c.InstrumentType = firstNonEmpty(params.InstrumentType, params.InstrumentTypeSnake, c.InstrumentType)
	}
	if c.Market == "" && params.Market != "" {
		c.Market = params.Market
	}
	if c.Name == "" && params.Name != "" {
		c.Name = params.Name
	}
	if params.Rank != nil {
		c.Rank = *params.Rank
	}
	if c.Rank <= 0 {
		c.Rank = 999
	}
	if value, ok := candidateScore(params.RelevanceScore, params.RelevanceScoreSnake, params.Scores, "relevance_score", "relevanceScore", "relevance"); ok {
		c.RelevanceScore = value
	}
	if value, ok := candidateScore(params.EvidenceScore, params.EvidenceScoreSnake, params.Scores, "evidence_score", "evidenceScore", "evidence"); ok {
		c.EvidenceScore = value
	}
	if value, ok := candidateScore(params.MarketRiskScore, params.MarketRiskScoreSnake, params.Scores, "market_risk_score", "marketRiskScore", "marketRisk", "risk"); ok {
		c.MarketRiskScore = value
	}
	if params.Confidence != nil {
		c.Confidence = *params.Confidence
	}
	if err := validateOpportunityCandidateScores(c); err != nil {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	intent := firstNonEmpty(params.SuggestedStrategyIntent, params.SuggestedStrategyIntentSnake)
	if intent != "" {
		if c.Metadata == nil {
			c.Metadata = map[string]any{}
		}
		c.Metadata["suggestedStrategyIntent"] = safelog.Text(intent, 1000)
	}
	if !validOpportunityCandidateStatus(c.Status) {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: "invalid candidate status"}
	}
	return c, nil
}

func (p *agentTaskPool) loadCandidateForPatch(run OpportunityDiscoveryRun, params candidateMCPParams) (OpportunityCandidate, *mcpError) {
	candidateID := firstNonEmpty(params.CandidateID, params.CandidateIDAlt)
	if candidateID != "" {
		return p.requireCandidateInRun(run, candidateID)
	}
	symbol := strings.TrimSpace(params.Symbol)
	if symbol == "" {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: "candidateID or symbol is required"}
	}
	c, err := p.service.store.GetOpportunityCandidateByRunSymbol(context.Background(), run.ID, symbol)
	if err != nil {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	if c.OpportunityID != run.OpportunityID {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: "candidate does not match opportunity"}
	}
	return c, nil
}

func (p *agentTaskPool) requireCandidateInRun(run OpportunityDiscoveryRun, candidateID string) (OpportunityCandidate, *mcpError) {
	c, err := p.service.store.GetOpportunityCandidate(context.Background(), strings.TrimSpace(candidateID))
	if err != nil {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	if c.RunID != run.ID || c.OpportunityID != run.OpportunityID {
		return OpportunityCandidate{}, &mcpError{Code: mcpErrInvalidParams, Message: "candidate does not match discovery run"}
	}
	return c, nil
}

func (p *agentTaskPool) loadDiscoveryStep(runID, stepID, stepKey string) (OpportunityDiscoveryStep, *mcpError) {
	stepID = strings.TrimSpace(stepID)
	stepKey = strings.TrimSpace(stepKey)
	var (
		step OpportunityDiscoveryStep
		err  error
	)
	if stepID != "" {
		step, err = p.service.store.GetOpportunityDiscoveryStep(context.Background(), stepID)
	} else if stepKey != "" {
		step, err = p.service.store.GetOpportunityDiscoveryStepByKey(context.Background(), runID, stepKey)
	} else {
		return OpportunityDiscoveryStep{}, &mcpError{Code: mcpErrInvalidParams, Message: "stepID or stepKey is required"}
	}
	if err != nil {
		return OpportunityDiscoveryStep{}, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	if step.RunID != runID {
		return OpportunityDiscoveryStep{}, &mcpError{Code: mcpErrInvalidParams, Message: "step does not match discovery run"}
	}
	return step, nil
}

func (p *agentTaskPool) markOpportunityResearching(ctx context.Context, opportunityID string) {
	opp, err := p.service.store.GetOpportunity(ctx, opportunityID)
	if err != nil {
		return
	}
	if opp.Status == OpportunityStatusDraft {
		opp.Status = OpportunityStatusResearching
		_, _ = p.service.store.UpdateOpportunity(ctx, opp)
	}
}

func (s *Service) GetEmbeddingStatus(ctx context.Context) (map[string]any, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("stock service is not configured")
	}
	cfg, err := s.store.GetEmbeddingConfig(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"available": false,
		"status":    "embedding_model_not_configured",
		"reason":    "no StockV2 embedding model is bound",
	}
	if strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		return out, nil
	}
	out["embeddingModelId"] = cfg.EmbeddingModelID
	if !cfg.Enabled {
		out["status"] = "embedding_model_disabled"
		out["reason"] = "bound embedding model is disabled in embedding config"
		return out, nil
	}
	model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
	if err != nil {
		out["status"] = "embedding_model_unavailable"
		out["reason"] = "bound embedding model profile was not found"
		return out, nil
	}
	out["providerId"] = model.ProviderID
	out["modelName"] = model.ModelName
	out["modelType"] = model.ModelType
	out["embeddingProtocol"] = model.EmbeddingProtocol
	out["embeddingDimensions"] = model.EmbeddingDimensions
	out["enabled"] = model.Enabled
	out["modelStatus"] = model.Status
	if model.ModelType != AgentModelTypeEmbedding || !model.Enabled || model.Status != AgentModelStatusAvailable {
		out["status"] = "embedding_model_unavailable"
		out["reason"] = "bound model is not an enabled available embedding model"
		return out, nil
	}
	out["available"] = true
	out["status"] = "available"
	out["reason"] = "embedding model is bound and available"
	return out, nil
}

func invalidMCPArgs(err error) *mcpError {
	return &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments: " + err.Error()}
}

func (p *agentTaskPool) requireStockService() *mcpError {
	if p.service == nil || p.service.store == nil {
		return &mcpError{Code: mcpErrInternal, Message: "stock service is not configured"}
	}
	return nil
}

func mcpDataResult(text string, data any) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
		"data":    data,
	}
}

func limitForMCP(value, fallback, max int) int {
	if value <= 0 {
		value = fallback
	}
	if value > max {
		value = max
	}
	return value
}

func parseMCPOptionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid publishedAt: %q", raw)
}

func sanitizeDiscoveryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme != "" && u.Host != "" {
		u.RawQuery = ""
		u.Fragment = ""
		return safelog.Text(u.String(), 1000)
	}
	return safelog.Text(raw, 1000)
}

func sanitizeDiscoveryMetadata(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		key := safelog.Text(strings.TrimSpace(k), 100)
		if key == "" {
			continue
		}
		out[key] = sanitizeDiscoveryValue(v, 0)
	}
	return out
}

func sanitizeDiscoveryValue(v any, depth int) any {
	if depth > 2 {
		return safelog.Text(fmt.Sprint(v), 500)
	}
	switch typed := v.(type) {
	case string:
		return safelog.Text(typed, 1000)
	case []any:
		limit := len(typed)
		if limit > 50 {
			limit = 50
		}
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, sanitizeDiscoveryValue(typed[i], depth+1))
		}
		return out
	case map[string]any:
		return sanitizeDiscoveryMetadata(typed)
	default:
		return typed
	}
}

func mergeDiscoveryMetadata(base, patch map[string]any, errMsg string) map[string]any {
	out := sanitizeDiscoveryMetadata(base)
	for k, v := range sanitizeDiscoveryMetadata(patch) {
		out[k] = v
	}
	if strings.TrimSpace(errMsg) != "" {
		out["errorMessage"] = safelog.Text(errMsg, 1000)
	}
	return out
}

func candidateScore(a, b *float64, scores map[string]float64, keys ...string) (float64, bool) {
	if a != nil {
		return *a, true
	}
	if b != nil {
		return *b, true
	}
	for _, key := range keys {
		if value, ok := scores[key]; ok {
			return value, true
		}
	}
	return 0, false
}

func validateOpportunityCandidateScores(c OpportunityCandidate) error {
	for name, value := range map[string]float64{
		"relevance_score":   c.RelevanceScore,
		"evidence_score":    c.EvidenceScore,
		"market_risk_score": c.MarketRiskScore,
	} {
		if value < 0 || value > 100 {
			return fmt.Errorf("%s must be between 0 and 100", name)
		}
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return errors.New("confidence must be between 0 and 1")
	}
	return nil
}

func validOpportunityCandidateStatus(status string) bool {
	switch status {
	case OpportunityCandidateStatusCandidate,
		OpportunityCandidateStatusShortlisted,
		OpportunityCandidateStatusRejected,
		OpportunityCandidateStatusStrategyRequested,
		OpportunityCandidateStatusStrategyGenerated:
		return true
	default:
		return false
	}
}
