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
	mcpToolSearchInstruments                      = "stock_agent.search_instruments"
	mcpToolSearchStockProfiles                    = "stock_agent.search_stock_profiles"
	mcpToolSemanticSearchStockProfiles            = "stock_agent.semantic_search_stock_profiles"
	mcpToolGetStockProfile                        = "stock_agent.get_stock_profile"
	mcpToolGetLatestQuotes                        = "stock_agent.get_latest_quotes"
	mcpToolGetDailyBarsSummary                    = "stock_agent.get_daily_bars_summary"
	mcpToolGetDailyBars                           = "stock_agent.get_daily_bars"
	mcpToolGetMinuteBars                          = "stock_agent.get_minute_bars"
	mcpToolGetQuoteHistory                        = "stock_agent.get_quote_history"
	mcpToolGetFundFlowHistory                     = "stock_agent.get_fund_flow_history"
	mcpToolGetDecisionEvidence                    = "stock_agent.get_decision_evidence"
	mcpToolGetMarketScanCandidates                = "stock_agent.get_market_scan_candidates"
	mcpToolSearchNewsEvents                       = "stock_agent.search_news_events"
	mcpToolSemanticSearchNewsEvents               = "stock_agent.semantic_search_news_events"
	mcpToolSemanticSearchNewsThreads              = "stock_agent.semantic_search_news_threads"
	mcpToolGetNewsThread                          = "stock_agent.get_news_thread"
	mcpToolListNewsContextChanges                 = "stock_agent.list_news_context_changes"
	mcpToolListPortfolioSentinelImpactReviewScope = "stock_agent.list_portfolio_sentinel_impact_review_scope"
	mcpToolSearchNewsLinkCandidates               = "stock_agent.search_news_link_candidates"
	mcpToolListExistingStrategies                 = "stock_agent.list_existing_strategies"
	mcpToolGetPortfolioContext                    = "stock_agent.get_portfolio_context"
	mcpToolGetEmbeddingStatus                     = "stock_agent.get_embedding_status"
	mcpToolStartDiscoveryStep                     = "stock_agent.start_discovery_step"
	mcpToolFinishDiscoveryStep                    = "stock_agent.finish_discovery_step"
	mcpToolFailDiscoveryStep                      = "stock_agent.fail_discovery_step"
	mcpToolRecordExternalSource                   = "stock_agent.record_external_source"
	mcpToolRecordEvidence                         = "stock_agent.record_evidence"
	mcpToolRecordCandidate                        = "stock_agent.record_candidate"
	mcpToolUpdateCandidate                        = "stock_agent.update_candidate"
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
		mcpToolGetDailyBars,
		mcpToolGetMinuteBars,
		mcpToolGetQuoteHistory,
		mcpToolGetFundFlowHistory,
		mcpToolGetDecisionEvidence,
		mcpToolGetMarketScanCandidates,
		mcpToolSearchNewsEvents,
		mcpToolSemanticSearchNewsEvents,
		mcpToolSemanticSearchNewsThreads,
		mcpToolGetNewsThread,
		mcpToolListNewsContextChanges,
		mcpToolListPortfolioSentinelImpactReviewScope,
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
	case "resources/list":
		result = map[string]any{"resources": []any{}}
	case "resources/templates/list":
		result = map[string]any{"resourceTemplates": []any{}}
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
			InputSchema: stockAgentMCPToolInputSchema(name),
		})
	}
	return map[string]any{"tools": tools}, nil
}

func stockAgentMCPToolInputSchema(name string) map[string]any {
	properties := map[string]any{}
	required := []string(nil)
	additionalProperties := true
	switch name {
	case codexSubmitResultTool:
		properties = map[string]any{
			"taskID":   map[string]any{"type": "string"},
			"taskType": map[string]any{"type": "string"},
			"result": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"outputType", "resultSummary", "result", "confidence"},
				"properties": map[string]any{
					"outputType":    map[string]any{"type": "string"},
					"resultSummary": map[string]any{"type": "string"},
					"result":        map[string]any{"type": "object"},
					"confidence":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				},
			},
		}
		required = []string{"taskID", "taskType", "result"}
		additionalProperties = false
	case mcpToolSemanticSearchNewsThreads:
		properties = map[string]any{
			"query":    map[string]any{"type": "string", "description": "Theme, event, sector, or rotation question."},
			"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"minScore": map[string]any{"type": "number", "minimum": -1, "maximum": 1},
			"asOf":     map[string]any{"type": "string", "description": "Optional RFC3339 cutoff. Returns the actual latest theme snapshot at that time; ranking may use its nearest retained historical vector."},
		}
		required = []string{"query"}
		additionalProperties = false
	case mcpToolGetDailyBars:
		properties = map[string]any{
			"symbol":    map[string]any{"type": "string", "description": "Six-digit instrument symbol."},
			"adjusted":  map[string]any{"type": "string", "enum": []string{DailyBarAdjustedNone, DailyBarAdjustedQFQ, DailyBarAdjustedHFQ}},
			"startDate": map[string]any{"type": "string", "description": "Optional YYYY-MM-DD inclusive start."},
			"endDate":   map[string]any{"type": "string", "description": "Optional YYYY-MM-DD inclusive end."},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 250},
		}
		required = []string{"symbol"}
		additionalProperties = false
	case mcpToolGetMinuteBars:
		properties = map[string]any{
			"symbol": map[string]any{"type": "string", "description": "Six-digit instrument symbol."},
			"days":   map[string]any{"type": "integer", "minimum": 1, "maximum": 5},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1200},
		}
		required = []string{"symbol"}
		additionalProperties = false
	case mcpToolGetQuoteHistory:
		properties = map[string]any{
			"symbol": map[string]any{"type": "string", "description": "Six-digit instrument symbol."},
			"hours":  map[string]any{"type": "integer", "minimum": 1, "maximum": 120},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
		}
		required = []string{"symbol"}
		additionalProperties = false
	case mcpToolGetFundFlowHistory:
		properties = map[string]any{
			"symbol":    map[string]any{"type": "string", "description": "Six-digit A-share symbol."},
			"market":    map[string]any{"type": "string", "enum": []string{"SH", "SZ"}},
			"startDate": map[string]any{"type": "string", "description": "Optional YYYY-MM-DD inclusive start."},
			"endDate":   map[string]any{"type": "string", "description": "Optional YYYY-MM-DD inclusive end."},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 120},
			"refresh":   map[string]any{"type": "boolean", "description": "Force one bounded refresh through configured primary/backup sources."},
		}
		required = []string{"symbol"}
		additionalProperties = false
	case mcpToolGetDecisionEvidence:
		properties = map[string]any{
			"symbol":         map[string]any{"type": "string", "description": "Six-digit instrument symbol."},
			"asOf":           map[string]any{"type": "string", "description": "Optional YYYY-MM-DD point-in-time cutoff."},
			"contextType":    map[string]any{"type": "string", "description": "Optional decision snapshot context type."},
			"contextId":      map[string]any{"type": "string", "description": "Optional decision snapshot context id; requires contextType."},
			"financialLimit": map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
			"eventLimit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
		}
		required = []string{"symbol"}
		additionalProperties = false
	case mcpToolGetMarketScanCandidates:
		properties = map[string]any{
			"runId":  map[string]any{"type": "string", "description": "Optional scan run id; latest run is used when omitted."},
			"symbol": map[string]any{"type": "string", "description": "Optional exact symbol."},
			"stage":  map[string]any{"type": "string", "description": "Optional candidate stage."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		}
		additionalProperties = false
	case mcpToolGetNewsThread:
		properties = map[string]any{
			"threadId": map[string]any{"type": "string", "description": "Stable message-thread id."},
			"asOf":     map[string]any{"type": "string", "description": "Optional RFC3339 cutoff. Returns only the theme state, versions, and evidence effective at or before this time."},
		}
		required = []string{"threadId"}
		additionalProperties = false
	case mcpToolListNewsContextChanges:
		properties = map[string]any{
			"runId":  map[string]any{"type": "string", "description": "Aggregation run id."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"offset": map[string]any{"type": "integer", "minimum": 0},
		}
		required = []string{"runId"}
		additionalProperties = false
	case mcpToolListPortfolioSentinelImpactReviewScope:
		properties = map[string]any{
			"runId":      map[string]any{"type": "string", "description": "Portfolio sentinel run id."},
			"objectType": map[string]any{"type": "string", "enum": []string{portfolioSentinelImpactObjectHoldings, portfolioSentinelImpactObjectMonitors, portfolioSentinelImpactObjectAlerts, portfolioSentinelImpactObjectOpportunities, portfolioSentinelImpactObjectStrategies}},
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"offset":     map[string]any{"type": "integer", "minimum": 0},
		}
		required = []string{"runId", "objectType"}
		additionalProperties = false
	case mcpToolRecordExternalSource:
		properties = map[string]any{
			"taskID":      map[string]any{"type": "string"},
			"runId":       map[string]any{"type": "string"},
			"stepId":      map[string]any{"type": "string"},
			"title":       map[string]any{"type": "string"},
			"url":         map[string]any{"type": "string"},
			"publisher":   map[string]any{"type": "string"},
			"publishedAt": map[string]any{"type": "string", "description": "Source publication time in RFC3339 form when available."},
			"summary":     map[string]any{"type": "string"},
			"confidence":  map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"metadata":    map[string]any{"type": "object"},
		}
		required = []string{"taskID", "runId", "title", "url", "summary", "confidence"}
		additionalProperties = false
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": additionalProperties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stockAgentMCPToolDescription(name string) string {
	switch name {
	case codexSubmitResultTool:
		return "Submit the final structured result of a stock agent task to the main program. The main program validates before persistence."
	case mcpToolSemanticSearchStockProfiles, mcpToolSemanticSearchNewsEvents, mcpToolSemanticSearchNewsThreads:
		return "Semantic vector search over StockV2 assets. Fails clearly when embedding is not configured, unavailable, or assets are not ready."
	case mcpToolGetNewsThread:
		return "Read one complete StockV2 message thread, including its current version, history, evidence, relationships, review, and index state."
	case mcpToolListNewsContextChanges:
		return "Page through every message-thread change produced by one aggregation run. Use this for complete review coverage; semantic search cannot replace it."
	case mcpToolListPortfolioSentinelImpactReviewScope:
		return "Page through the immutable identifiers in one final impact-review scope. Read all five object types and report every identifier exactly once."
	case mcpToolGetDailyBars:
		return "Read bounded raw OHLCV daily bars. QFQ/HFQ rows are trend evidence only; use none for completed-session executable close levels."
	case mcpToolGetMinuteBars:
		return "Read bounded recent minute OHLCV and intraday main-flow bars collected by StockV2."
	case mcpToolGetQuoteHistory:
		return "Read bounded intraday quote snapshots including turnover, volume ratio, and size-bucket fund-flow fields."
	case mcpToolGetFundFlowHistory:
		return "Read bounded daily main-net-flow and turnover history; refreshes through configured sources only when needed and never exposes credentials."
	case mcpToolGetDecisionEvidence:
		return "Read deterministic decision gates, cached financial facts, corporate-event calendar, reference health, and aggregate fund-flow evidence for one symbol."
	case mcpToolGetMarketScanCandidates:
		return "Read bounded candidates and deterministic metrics from a market-scan run, including exclusion and decision reasons."
	case mcpToolRecordExternalSource:
		return "Record an external public source summary for the current opportunity discovery run. URL query and fragment are stripped."
	case mcpToolRecordEvidence, mcpToolRecordCandidate, mcpToolUpdateCandidate:
		return "Record or update opportunity discovery evidence/candidates for the current run."
	default:
		return "Query bounded StockV2 internal project data for stock analysis."
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
		return s.agentTaskPool.mcpSubmitResult(callParams.Arguments)
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
	case mcpToolGetDailyBars:
		return s.mcpGetDailyBars(callParams.Arguments)
	case mcpToolGetMinuteBars:
		return s.mcpGetMinuteBars(callParams.Arguments)
	case mcpToolGetQuoteHistory:
		return s.mcpGetQuoteHistory(callParams.Arguments)
	case mcpToolGetFundFlowHistory:
		return s.mcpGetFundFlowHistory(callParams.Arguments)
	case mcpToolGetDecisionEvidence:
		return s.mcpGetDecisionEvidence(callParams.Arguments)
	case mcpToolGetMarketScanCandidates:
		return s.mcpGetMarketScanCandidates(callParams.Arguments)
	case mcpToolSearchNewsEvents:
		return s.mcpSearchNewsEvents(callParams.Arguments)
	case mcpToolSemanticSearchNewsEvents:
		return s.mcpSemanticSearch(callParams.Arguments, EmbeddingObjectNewsEvent)
	case mcpToolSemanticSearchNewsThreads:
		return s.mcpSemanticSearchNewsThreads(callParams.Arguments)
	case mcpToolGetNewsThread:
		return s.mcpGetNewsThread(callParams.Arguments)
	case mcpToolListNewsContextChanges:
		return s.mcpListNewsContextChanges(callParams.Arguments)
	case mcpToolListPortfolioSentinelImpactReviewScope:
		return s.mcpListPortfolioSentinelImpactReviewScope(callParams.Arguments)
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
	ctx := contextFromMCP()
	adjusted := normalizeAgentDailyBarAdjusted(p.Adjusted)
	bars := s.buildDailyBarsContextAt(ctx, p.Symbol, adjusted, time.Now())
	count, earliest, latest, source, lastErr, err := s.store.GetDailyBarsStats(ctx, p.Symbol, adjusted)
	return mcpResultOrError(map[string]any{
		"symbol":                   p.Symbol,
		"adjusted":                 adjusted,
		"rowCount":                 count,
		"earliest":                 earliest,
		"latest":                   latest,
		"source":                   source,
		"lastError":                lastErr,
		"coverageStatus":           bars.CoverageStatus,
		"checkedAt":                bars.CheckedAt,
		"refreshAttempted":         bars.RefreshAttempted,
		"currentSessionIncomplete": bars.CurrentSessionIncomplete,
		"refreshError":             bars.RefreshError,
		"summary":                  bars.Summary,
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

func (s *Service) mcpSemanticSearchNewsThreads(args json.RawMessage) (any, *mcpError) {
	var req SemanticSearchRequest
	if err := json.Unmarshal(args, &req); err != nil || strings.TrimSpace(req.Query) == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "query is required"}
	}
	req.Query = strings.TrimSpace(req.Query)
	req.Limit = normalizedMCPLimit(req.Limit)
	items, err := s.SemanticSearchNewsThreads(contextFromMCP(), req)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(map[string]any{
		"items":      items,
		"count":      len(items),
		"searchType": "semantic_vector",
		"asOf":       strings.TrimSpace(req.AsOf),
		"notice":     "Similarity is retrieval only; it is not evidence of identity, causality, support, contradiction, or a trading conclusion.",
	}), nil
}

func (s *Service) mcpGetNewsThread(args json.RawMessage) (any, *mcpError) {
	var p struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
		AsOf     string `json:"asOf"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	id := strings.TrimSpace(firstNonEmptyOpportunity(p.ThreadID, p.ID))
	if id == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "threadId is required"}
	}
	detail, err := s.GetNewsThreadDetailAsOf(contextFromMCP(), id, p.AsOf)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(mcpNewsThreadDetail(detail)), nil
}

func (s *Service) mcpListNewsContextChanges(args json.RawMessage) (any, *mcpError) {
	var p struct {
		RunID  string `json:"runId"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	p.RunID = strings.TrimSpace(p.RunID)
	if p.RunID == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "runId is required"}
	}
	p.Limit = normalizedMCPLimit(p.Limit)
	p.Offset = normalizedPageOffset(p.Offset)
	items, total, err := s.ListNewsContextReviewChanges(contextFromMCP(), p.RunID, p.Limit, p.Offset)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(map[string]any{
		"items":      items,
		"total":      total,
		"limit":      p.Limit,
		"offset":     p.Offset,
		"nextOffset": nextMCPPageOffset(p.Offset, p.Limit, len(items), total),
	}), nil
}

func (s *Service) mcpListPortfolioSentinelImpactReviewScope(args json.RawMessage) (any, *mcpError) {
	var p struct {
		RunID      string `json:"runId"`
		ObjectType string `json:"objectType"`
		Limit      int    `json:"limit"`
		Offset     int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments"}
	}
	p.RunID = strings.TrimSpace(p.RunID)
	p.ObjectType = strings.TrimSpace(p.ObjectType)
	if p.RunID == "" || !validPortfolioSentinelImpactObjectType(p.ObjectType) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "runId and a valid objectType are required"}
	}
	p.Limit = mcpLimit(p.Limit, 100, 100)
	p.Offset = normalizedPageOffset(p.Offset)
	items, total, err := s.portfolioSentinelImpactReviewScopePage(contextFromMCP(), p.RunID, p.ObjectType, p.Limit, p.Offset)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(map[string]any{
		"objectType": p.ObjectType,
		"items":      items,
		"total":      total,
		"limit":      p.Limit,
		"offset":     p.Offset,
		"nextOffset": nextMCPPageOffset(p.Offset, p.Limit, len(items), total),
	}), nil
}

func (s *Service) portfolioSentinelImpactReviewScopePage(ctx context.Context, runID, objectType string, limit, offset int) ([]map[string]any, int, error) {
	ids, total, err := s.store.ListPortfolioSentinelImpactReviewScope(ctx, runID, objectType, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		item := map[string]any{"id": id, "available": true}
		switch objectType {
		case portfolioSentinelImpactObjectHoldings:
			value, err := s.store.GetHolding(ctx, id)
			if errors.Is(err, ErrHoldingNotFound) {
				item["available"] = false
			} else if err != nil {
				return nil, 0, err
			} else {
				item["portfolioId"] = value.PortfolioID
				item["symbol"] = value.Symbol
				item["market"] = value.Market
				item["name"] = value.Name
				item["quantity"] = value.Quantity
				item["marketValue"] = value.MarketValue
				item["pnl"] = value.PnL
			}
		case portfolioSentinelImpactObjectMonitors:
			if taskType, ok := strings.CutPrefix(id, "task:"); ok {
				value, err := s.GetMonitorTask(ctx, taskType)
				if errors.Is(err, ErrInvalidMonitorTaskType) || errors.Is(err, ErrMonitorTaskNotFound) {
					item["available"] = false
				} else if err != nil {
					return nil, 0, err
				} else {
					item["kind"] = "system_task"
					item["label"] = value.Definition.Label
					item["description"] = value.Definition.Description
					item["category"] = value.Definition.Category
					item["enabled"] = value.Config.Enabled
					item["intervalSeconds"] = value.Config.IntervalSeconds
					item["scope"] = value.Config.Scope
					item["sensitivity"] = value.Config.Sensitivity
					item["cooldownSeconds"] = value.Config.CooldownSeconds
					item["agentDoublecheckEnabled"] = value.Config.AgentDoublecheckEnabled
				}
			} else if watchID, ok := strings.CutPrefix(id, "watch:"); ok {
				value, err := s.store.GetWatch(ctx, watchID)
				if errors.Is(err, ErrWatchNotFound) {
					item["available"] = false
				} else if err != nil {
					return nil, 0, err
				} else {
					item["kind"] = "watch"
					item["name"] = value.Name
					item["status"] = value.Status
					item["source"] = value.Source
					item["symbol"] = value.Symbol
					item["market"] = value.Market
					item["portfolioId"] = value.PortfolioID
					item["strategyId"] = value.StrategyID
					item["strategyVersionId"] = value.StrategyVersionID
					item["triggerPolicy"] = value.TriggerPolicy
					item["triggerConfig"] = value.TriggerConfig
					item["scheduleKind"] = value.ScheduleKind
					item["cooldownSeconds"] = value.CooldownSeconds
				}
			} else {
				item["available"] = false
			}
		case portfolioSentinelImpactObjectAlerts:
			value, err := s.store.GetAlert(ctx, id)
			if errors.Is(err, ErrAlertNotFound) {
				item["available"] = false
			} else if err != nil {
				return nil, 0, err
			} else {
				item["status"] = value.Status
				item["level"] = value.Level
				item["title"] = value.Title
				item["summary"] = value.Summary
				item["watchId"] = value.WatchID
				item["portfolioId"] = value.PortfolioID
				item["strategyId"] = value.StrategyID
				item["symbol"] = value.Symbol
				item["market"] = value.Market
			}
		case portfolioSentinelImpactObjectOpportunities:
			value, err := s.store.GetOpportunity(ctx, id)
			if errors.Is(err, ErrOpportunityNotFound) {
				item["available"] = false
			} else if err != nil {
				return nil, 0, err
			} else {
				item["title"] = value.Title
				item["userThesis"] = value.UserThesis
				item["status"] = value.Status
				item["marketScope"] = value.MarketScope
				item["instrumentScope"] = value.InstrumentScope
			}
		case portfolioSentinelImpactObjectStrategies:
			value, err := s.store.GetStrategy(ctx, id)
			if errors.Is(err, ErrStrategyNotFound) {
				item["available"] = false
			} else if err != nil {
				return nil, 0, err
			} else {
				item["name"] = value.Strategy.Name
				item["status"] = value.Strategy.Status
				item["kind"] = value.Strategy.Kind
				item["scope"] = value.Strategy.Scope
				item["symbol"] = value.Strategy.Symbol
				item["market"] = value.Strategy.Market
				item["portfolioId"] = value.Strategy.PortfolioID
				item["activeVersionId"] = value.Strategy.ActiveVersionID
				if value.ActiveVersion != nil {
					item["direction"] = value.ActiveVersion.Direction
					item["thesis"] = value.ActiveVersion.Thesis
					item["riskNotes"] = value.ActiveVersion.RiskNotes
				}
			}
		}
		items = append(items, item)
	}
	return items, total, nil
}

func nextMCPPageOffset(offset, limit, count, total int) any {
	next := offset + count
	if count == 0 || next >= total || limit <= 0 {
		return nil
	}
	return next
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
		errors.Is(err, ErrEmbeddingAssetNotReady) ||
		errors.Is(err, ErrInvalidEmbeddingRequest) {
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
	if errors.Is(err, ErrNewsThreadNotFound) ||
		errors.Is(err, ErrNewsContextRunNotFound) ||
		errors.Is(err, ErrInvalidNewsContextInput) ||
		errors.Is(err, ErrPortfolioSentinelRunNotFound) ||
		errors.Is(err, ErrInvalidPortfolioSentinelInput) {
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
