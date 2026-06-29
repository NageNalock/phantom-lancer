package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	mcpToolSearchInstruments           = "stock_agent.search_instruments"
	mcpToolSearchStockProfiles         = "stock_agent.search_stock_profiles"
	mcpToolSemanticSearchStockProfiles = "stock_agent.semantic_search_stock_profiles"
	mcpToolGetStockProfile             = "stock_agent.get_stock_profile"
	mcpToolGetLatestQuotes             = "stock_agent.get_latest_quotes"
	mcpToolGetDailyBarsSummary         = "stock_agent.get_daily_bars_summary"
	mcpToolSearchNewsEvents            = "stock_agent.search_news_events"
	mcpToolSemanticSearchNewsEvents    = "stock_agent.semantic_search_news_events"
	mcpToolSearchNewsLinkCandidates    = "stock_agent.search_news_link_candidates"
	mcpToolListExistingStrategies      = "stock_agent.list_existing_strategies"
	mcpToolGetPortfolioContext         = "stock_agent.get_portfolio_context"
	mcpToolGetEmbeddingStatus          = "stock_agent.get_embedding_status"
	mcpToolStartDiscoveryStep          = "stock_agent.start_discovery_step"
	mcpToolFinishDiscoveryStep         = "stock_agent.finish_discovery_step"
	mcpToolFailDiscoveryStep           = "stock_agent.fail_discovery_step"
	mcpToolRecordExternalSource        = "stock_agent.record_external_source"
	mcpToolRecordEvidence              = "stock_agent.record_evidence"
	mcpToolRecordCandidate             = "stock_agent.record_candidate"
	mcpToolUpdateCandidate             = "stock_agent.update_candidate"
)

func stockAgentMCPRequiredTools() []string {
	return []string{
		codexSubmitResultTool,
		mcpToolSearchInstruments,
		mcpToolSearchStockProfiles,
		mcpToolSemanticSearchStockProfiles,
		mcpToolGetStockProfile,
		mcpToolGetLatestQuotes,
		mcpToolGetDailyBarsSummary,
		mcpToolSearchNewsEvents,
		mcpToolSemanticSearchNewsEvents,
		mcpToolSearchNewsLinkCandidates,
		mcpToolListExistingStrategies,
		mcpToolGetPortfolioContext,
		mcpToolGetEmbeddingStatus,
		mcpToolStartDiscoveryStep,
		mcpToolFinishDiscoveryStep,
		mcpToolFailDiscoveryStep,
		mcpToolRecordExternalSource,
		mcpToolRecordEvidence,
		mcpToolRecordCandidate,
		mcpToolUpdateCandidate,
	}
}

func (s *Service) HandleMCPRequest(raw []byte) []byte {
	var req mcpJSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return mcpErrorResponse(nil, mcpErrParseError, "Parse error", nil)
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return mcpErrorResponse(req.ID, mcpErrInvalidRequest, "Invalid request", nil)
	}
	var result any
	var err *mcpError
	switch req.Method {
	case "initialize":
		result, err = s.mcpInitialize(req.Params)
	case "tools/list":
		result, err = s.mcpToolsList(req.Params)
	case "tools/call":
		result, err = s.mcpToolsCall(req.Params)
	default:
		err = &mcpError{Code: mcpErrMethodNotFound, Message: "Method not found"}
	}
	if err != nil {
		return mcpErrorResponse(req.ID, err.Code, err.Message, err.Data)
	}
	return mcpSuccessResponse(req.ID, result)
}

func (s *Service) mcpInitialize(params json.RawMessage) (any, *mcpError) {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    mcpServerName,
			"version": mcpServerVersion,
		},
		"capabilities": map[string]any{"tools": map[string]any{}},
		"instructions": "StockV2 Agent MCP Server. Use project-internal tools for stock data and trace recording. External public research must use Codex CLI built-in search/browse, not this MCP server.",
	}, nil
}

func (s *Service) mcpToolsList(params json.RawMessage) (any, *mcpError) {
	tools := make([]mcpTool, 0, len(stockAgentMCPRequiredTools()))
	for _, name := range stockAgentMCPRequiredTools() {
		tools = append(tools, mcpTool{
			Name:        name,
			Description: stockAgentMCPToolDescription(name),
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": true,
			},
		})
	}
	return map[string]any{"tools": tools}, nil
}

func stockAgentMCPToolDescription(name string) string {
	switch name {
	case codexSubmitResultTool:
		return "Submit the final structured result of a stock agent task to the main program. The main program validates before persistence."
	case mcpToolSemanticSearchStockProfiles, mcpToolSemanticSearchNewsEvents:
		return "Semantic vector search over StockV2 assets. Fails clearly when embedding is not configured, unavailable, or assets are not ready."
	case mcpToolRecordExternalSource:
		return "Record an external public source summary for the current opportunity discovery run. URL query and fragment are stripped."
	case mcpToolRecordEvidence, mcpToolRecordCandidate, mcpToolUpdateCandidate:
		return "Record or update opportunity discovery evidence/candidates for the current run."
	default:
		return "Query StockV2 internal project data for opportunity discovery."
	}
}

func (s *Service) mcpToolsCall(params json.RawMessage) (any, *mcpError) {
	var callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &callParams); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid params"}
	}
	switch strings.TrimSpace(callParams.Name) {
	case codexSubmitResultTool:
		return s.mcpSubmitResult(callParams.Arguments)
	case mcpToolSearchInstruments:
		return s.mcpSearchInstruments(callParams.Arguments)
	case mcpToolSearchStockProfiles:
		return s.mcpSearchStockProfiles(callParams.Arguments)
	case mcpToolSemanticSearchStockProfiles:
		return s.mcpSemanticSearch(callParams.Arguments, EmbeddingObjectStockProfile)
	case mcpToolGetStockProfile:
		return s.mcpGetStockProfile(callParams.Arguments)
	case mcpToolGetLatestQuotes:
		return s.mcpGetLatestQuotes(callParams.Arguments)
	case mcpToolGetDailyBarsSummary:
		return s.mcpGetDailyBarsSummary(callParams.Arguments)
	case mcpToolSearchNewsEvents:
		return s.mcpSearchNewsEvents(callParams.Arguments)
	case mcpToolSemanticSearchNewsEvents:
		return s.mcpSemanticSearch(callParams.Arguments, EmbeddingObjectNewsEvent)
	case mcpToolSearchNewsLinkCandidates:
		return s.mcpSearchNewsLinkCandidates(callParams.Arguments)
	case mcpToolListExistingStrategies:
		return s.mcpListExistingStrategies(callParams.Arguments)
	case mcpToolGetPortfolioContext:
		return s.mcpGetPortfolioContext(callParams.Arguments)
	case mcpToolGetEmbeddingStatus:
		status, err := s.GetEmbeddingStatus(contextFromMCP())
		return mcpResultOrError(status, err)
	case mcpToolStartDiscoveryStep:
		return s.mcpStartDiscoveryStep(callParams.Arguments)
	case mcpToolFinishDiscoveryStep:
		return s.mcpFinishDiscoveryStep(callParams.Arguments)
	case mcpToolFailDiscoveryStep:
		return s.mcpFailDiscoveryStep(callParams.Arguments)
	case mcpToolRecordExternalSource:
		return s.mcpRecordExternalSource(callParams.Arguments)
	case mcpToolRecordEvidence:
		return s.mcpRecordEvidence(callParams.Arguments)
	case mcpToolRecordCandidate:
		return s.mcpRecordCandidate(callParams.Arguments)
	case mcpToolUpdateCandidate:
		return s.mcpUpdateCandidate(callParams.Arguments)
	default:
		return nil, &mcpError{Code: mcpErrMethodNotFound, Message: "Tool not found: " + callParams.Name}
	}
}

func contextFromMCP() context.Context {
	return context.Background()
}

func (s *Service) mcpSubmitResult(args json.RawMessage) (any, *mcpError) {
	var params submitResultParams
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments: " + err.Error()}
	}
	params.TaskID = strings.TrimSpace(params.TaskID)
	params.TaskType = strings.TrimSpace(params.TaskType)
	params.Result.OutputType = strings.TrimSpace(params.Result.OutputType)
	if params.TaskID == "" || params.TaskType == "" || params.Result.OutputType == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "taskID, taskType and result.outputType are required"}
	}
	if !validAgentTaskOutputType(params.TaskType, params.Result.OutputType) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "invalid result.outputType"}
	}
	result := AgentTaskSubmittedResult{
		OutputType:    params.Result.OutputType,
		ResultSummary: params.Result.ResultSummary,
		Result:        params.Result.Result,
		Confidence:    params.Result.Confidence,
	}
	status, err := s.agentTaskPool.submitResult(params.TaskID, params.TaskType, result)
	if err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: err.Error(), Data: map[string]string{"status": status}}
	}
	return mcpTextResult("Result accepted. The main program will validate and process it."), nil
}

func (s *Service) mcpSearchInstruments(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Query          string `json:"query"`
		Keyword        string `json:"keyword"`
		Market         string `json:"market"`
		InstrumentType string `json:"instrumentType"`
		Limit          int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	items, err := s.SearchInstrumentsFiltered(contextFromMCP(), firstNonEmptyOpportunity(p.Query, p.Keyword), p.Market, p.InstrumentType, "", normalizedMCPLimit(p.Limit))
	return mcpResultOrError(items, err)
}

func (s *Service) mcpSearchStockProfiles(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Query          string `json:"query"`
		Keyword        string `json:"keyword"`
		Market         string `json:"market"`
		InstrumentType string `json:"instrumentType"`
		Limit          int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	items, err := s.store.ListStockProfiles(contextFromMCP(), StockProfileListFilter{
		Keyword:        firstNonEmptyOpportunity(p.Query, p.Keyword),
		Market:         p.Market,
		InstrumentType: p.InstrumentType,
		Limit:          normalizedMCPLimit(p.Limit),
	})
	return mcpResultOrError(items, err)
}

func (s *Service) mcpSemanticSearch(args json.RawMessage, objectType string) (any, *mcpError) {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "query is required"}
	}
	items, err := s.SemanticSearch(contextFromMCP(), objectType, p.Query, normalizedMCPLimit(p.Limit))
	return mcpResultOrError(items, err)
}

func (s *Service) mcpGetStockProfile(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Symbol) == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "symbol is required"}
	}
	item, err := s.store.GetStockProfile(contextFromMCP(), p.Symbol)
	return mcpResultOrError(item, err)
}

func (s *Service) mcpGetLatestQuotes(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbols []string `json:"symbols"`
		Symbol  string   `json:"symbol"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if strings.TrimSpace(p.Symbol) != "" {
		p.Symbols = append(p.Symbols, p.Symbol)
	}
	if len(p.Symbols) == 0 {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "symbols is required"}
	}
	items, err := s.store.GetLatestQuotes(contextFromMCP(), p.Symbols)
	return mcpResultOrError(items, err)
}

func (s *Service) mcpGetDailyBarsSummary(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol   string `json:"symbol"`
		Adjusted string `json:"adjusted"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Symbol) == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "symbol is required"}
	}
	adjusted := firstNonEmptyOpportunity(p.Adjusted, DailyBarAdjustedNone)
	count, earliest, latest, source, lastErr, err := s.store.GetDailyBarsStats(contextFromMCP(), p.Symbol, adjusted)
	return mcpResultOrError(map[string]any{
		"symbol":    p.Symbol,
		"adjusted":  adjusted,
		"rowCount":  count,
		"earliest":  earliest,
		"latest":    latest,
		"source":    source,
		"lastError": lastErr,
	}, err)
}

func (s *Service) mcpSearchNewsEvents(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Query         string `json:"query"`
		Source        string `json:"source"`
		LinkStatus    string `json:"linkStatus"`
		QualityStatus string `json:"qualityStatus"`
		Limit         int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	items, err := s.store.ListNewsEvents(contextFromMCP(), NewsEventListFilter{
		Query:         p.Query,
		Source:        p.Source,
		LinkStatus:    p.LinkStatus,
		QualityStatus: p.QualityStatus,
		Limit:         normalizedMCPLimit(p.Limit),
	})
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mcpNewsEventSummary(item))
	}
	return mcpJSONResult(out), nil
}

func (s *Service) mcpSearchNewsLinkCandidates(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Query       string `json:"query"`
		Symbol      string `json:"symbol"`
		Market      string `json:"market"`
		MatchMethod string `json:"matchMethod"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	items, err := s.store.ListNewsLinkCandidates(contextFromMCP(), NewsLinkCandidateListFilter{
		Query:       p.Query,
		Symbol:      p.Symbol,
		Market:      p.Market,
		MatchMethod: p.MatchMethod,
		Limit:       normalizedMCPLimit(p.Limit),
	})
	return mcpResultOrError(items, err)
}

func (s *Service) mcpListExistingStrategies(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol string `json:"symbol"`
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	items, err := s.store.ListStrategies(contextFromMCP(), StrategyListFilter{
		Symbol: p.Symbol,
		Status: p.Status,
		Limit:  normalizedMCPLimit(p.Limit),
	})
	return mcpResultOrError(items, err)
}

func (s *Service) mcpGetPortfolioContext(args json.RawMessage) (any, *mcpError) {
	var p struct {
		PortfolioID string `json:"portfolioId"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.PortfolioID) == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "portfolioId is required"}
	}
	item, err := s.GetPortfolio(contextFromMCP(), p.PortfolioID)
	return mcpResultOrError(item, err)
}

type mcpDiscoveryStepParams struct {
	TaskID        string         `json:"taskID"`
	RunID         string         `json:"runId"`
	StepKey       string         `json:"stepKey"`
	InputSummary  string         `json:"inputSummary"`
	OutputSummary string         `json:"outputSummary"`
	Metadata      map[string]any `json:"metadata"`
}

func (s *Service) mcpStartDiscoveryStep(args json.RawMessage) (any, *mcpError) {
	var p mcpDiscoveryStepParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	item, serviceErr := s.StartOpportunityDiscoveryStep(contextFromMCP(), p.RunID, p.StepKey, p.InputSummary, p.Metadata)
	return mcpResultOrError(item, serviceErr)
}

func (s *Service) mcpFinishDiscoveryStep(args json.RawMessage) (any, *mcpError) {
	var p mcpDiscoveryStepParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	item, serviceErr := s.FinishOpportunityDiscoveryStep(contextFromMCP(), p.RunID, p.StepKey, p.OutputSummary, p.Metadata)
	return mcpResultOrError(item, serviceErr)
}

func (s *Service) mcpFailDiscoveryStep(args json.RawMessage) (any, *mcpError) {
	var p mcpDiscoveryStepParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	item, serviceErr := s.FailOpportunityDiscoveryStep(contextFromMCP(), p.RunID, p.StepKey, p.OutputSummary, p.Metadata)
	return mcpResultOrError(item, serviceErr)
}

func (s *Service) mcpRecordExternalSource(args json.RawMessage) (any, *mcpError) {
	var p struct {
		TaskID      string         `json:"taskID"`
		RunID       string         `json:"runId"`
		StepID      string         `json:"stepId"`
		Title       string         `json:"title"`
		URL         string         `json:"url"`
		Publisher   string         `json:"publisher"`
		PublishedAt string         `json:"publishedAt"`
		Summary     string         `json:"summary"`
		Confidence  float64        `json:"confidence"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	meta := mergeMaps(p.Metadata, map[string]any{"stepId": p.StepID})
	item, err := s.RecordOpportunityEvidence(contextFromMCP(), OpportunityEvidence{
		RunID:       p.RunID,
		SourceType:  OpportunityEvidenceSourceExternal,
		SourceRef:   sanitizeOpportunityURL(p.URL),
		Title:       p.Title,
		Summary:     p.Summary,
		URL:         p.URL,
		Publisher:   p.Publisher,
		PublishedAt: parseMCPTime(p.PublishedAt),
		Confidence:  p.Confidence,
		Metadata:    meta,
	})
	return mcpResultOrError(item, err)
}

func (s *Service) mcpRecordEvidence(args json.RawMessage) (any, *mcpError) {
	var p struct {
		TaskID      string         `json:"taskID"`
		RunID       string         `json:"runId"`
		CandidateID string         `json:"candidateId"`
		SourceType  string         `json:"sourceType"`
		SourceRef   string         `json:"sourceRef"`
		Title       string         `json:"title"`
		Summary     string         `json:"summary"`
		URL         string         `json:"url"`
		Publisher   string         `json:"publisher"`
		PublishedAt string         `json:"publishedAt"`
		Confidence  float64        `json:"confidence"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	item, err := s.RecordOpportunityEvidence(contextFromMCP(), OpportunityEvidence{
		RunID:       p.RunID,
		CandidateID: p.CandidateID,
		SourceType:  firstNonEmptyOpportunity(p.SourceType, OpportunityEvidenceSourceAgentNote),
		SourceRef:   p.SourceRef,
		Title:       p.Title,
		Summary:     p.Summary,
		URL:         p.URL,
		Publisher:   p.Publisher,
		PublishedAt: parseMCPTime(p.PublishedAt),
		Confidence:  p.Confidence,
		Metadata:    p.Metadata,
	})
	return mcpResultOrError(item, err)
}

func (s *Service) mcpRecordCandidate(args json.RawMessage) (any, *mcpError) {
	var p struct {
		TaskID          string         `json:"taskID"`
		RunID           string         `json:"runId"`
		Symbol          string         `json:"symbol"`
		RelationType    string         `json:"relationType"`
		RelevanceScore  float64        `json:"relevanceScore"`
		EvidenceScore   float64        `json:"evidenceScore"`
		MarketRiskScore float64        `json:"marketRiskScore"`
		Confidence      float64        `json:"confidence"`
		Rank            int            `json:"rank"`
		Reason          string         `json:"reason"`
		RiskSummary     string         `json:"riskSummary"`
		Metadata        map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	item, err := s.RecordOpportunityCandidate(contextFromMCP(), OpportunityCandidate{
		RunID:           p.RunID,
		Symbol:          p.Symbol,
		RelationType:    p.RelationType,
		RelevanceScore:  p.RelevanceScore,
		EvidenceScore:   p.EvidenceScore,
		MarketRiskScore: p.MarketRiskScore,
		Confidence:      p.Confidence,
		Rank:            p.Rank,
		Reason:          p.Reason,
		RiskSummary:     p.RiskSummary,
		Metadata:        p.Metadata,
	})
	return mcpResultOrError(item, err)
}

func (s *Service) mcpUpdateCandidate(args json.RawMessage) (any, *mcpError) {
	var p struct {
		TaskID      string         `json:"taskID"`
		RunID       string         `json:"runId"`
		CandidateID string         `json:"candidateId"`
		Status      *string        `json:"status"`
		Reason      *string        `json:"reason"`
		RiskSummary *string        `json:"riskSummary"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	if _, err := s.validateDiscoveryTask(p.TaskID, p.RunID); err != nil {
		return nil, err
	}
	candidate, err := s.store.GetOpportunityCandidate(contextFromMCP(), p.CandidateID)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	if candidate.RunID != p.RunID {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: ErrOpportunityTaskMismatch.Error()}
	}
	item, serviceErr := s.UpdateOpportunityCandidate(contextFromMCP(), p.CandidateID, RequestUpdateOpportunityCandidate{
		Status:      p.Status,
		Reason:      p.Reason,
		RiskSummary: p.RiskSummary,
		Metadata:    p.Metadata,
	})
	return mcpResultOrError(item, serviceErr)
}

func (s *Service) validateDiscoveryTask(taskID, runID string) (OpportunityDiscoveryRun, *mcpError) {
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	if taskID == "" || runID == "" {
		return OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: "taskID and runId are required"}
	}
	entry, ok := s.agentTaskPool.getTask(taskID)
	if !ok || entry.taskType != AgentTaskTypeOpportunityDiscovery {
		return OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: ErrOpportunityTaskMismatch.Error()}
	}
	run, err := s.store.GetOpportunityDiscoveryRun(contextFromMCP(), runID)
	if err != nil {
		return OpportunityDiscoveryRun{}, mcpErrorFromError(err)
	}
	if run.AgentRunID != entry.agentRunID {
		return OpportunityDiscoveryRun{}, &mcpError{Code: mcpErrInvalidParams, Message: ErrOpportunityTaskMismatch.Error()}
	}
	return run, nil
}

func parseMCPTime(raw string) time.Time {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
		return t
	}
	return time.Time{}
}

func normalizedMCPLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func mcpResultOrError(value any, err error) (any, *mcpError) {
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(value), nil
}

func mcpJSONResult(value any) any {
	data, _ := json.Marshal(value)
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(data)}},
		"isError": false,
	}
}

func mcpTextResult(text string) any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
	}
}

func mcpErrorFromError(err error) *mcpError {
	code := mcpErrInternal
	if errors.Is(err, ErrEmbeddingDisabled) ||
		errors.Is(err, ErrEmbeddingModelNotConfigured) ||
		errors.Is(err, ErrEmbeddingModelUnavailable) ||
		errors.Is(err, ErrEmbeddingModelInvalid) ||
		errors.Is(err, ErrEmbeddingDimensionsMismatch) ||
		errors.Is(err, ErrEmbeddingAssetNotReady) {
		code = mcpErrInvalidParams
	}
	if errors.Is(err, ErrInvalidOpportunityInput) ||
		errors.Is(err, ErrInvalidOpportunityCandidate) ||
		errors.Is(err, ErrInvalidOpportunityResult) ||
		errors.Is(err, ErrOpportunityUnsafeResult) ||
		errors.Is(err, ErrOpportunitySymbolNotFound) ||
		errors.Is(err, ErrOpportunityTaskMismatch) ||
		errors.Is(err, ErrOpportunityNotFound) ||
		errors.Is(err, ErrDiscoveryRunNotFound) ||
		errors.Is(err, ErrDiscoveryStepNotFound) ||
		errors.Is(err, ErrOpportunityCandidateNotFound) {
		code = mcpErrInvalidParams
	}
	return &mcpError{Code: code, Message: err.Error(), Data: mcpErrorData(err)}
}

func mcpErrorData(err error) map[string]string {
	switch {
	case errors.Is(err, ErrEmbeddingDisabled):
		return map[string]string{"code": EmbeddingStatusDisabled}
	case errors.Is(err, ErrEmbeddingModelNotConfigured):
		return map[string]string{"code": EmbeddingStatusModelNotConfigured}
	case errors.Is(err, ErrEmbeddingModelUnavailable), errors.Is(err, ErrEmbeddingModelInvalid), errors.Is(err, ErrEmbeddingDimensionsMismatch):
		return map[string]string{"code": EmbeddingStatusModelUnavailable}
	case errors.Is(err, ErrEmbeddingAssetNotReady):
		return map[string]string{"code": EmbeddingStatusAssetNotReady}
	default:
		return map[string]string{"code": fmt.Sprintf("%T", err)}
	}
}
