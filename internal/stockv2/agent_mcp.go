package stockv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 轻量 MCP server。仅实现必要方法: initialize / tools/list / tools/call。
// 工具: stock_agent.submit_result 以及 opportunity_discovery 的项目内查询/过程留痕工具。
//
// ponytail: 手工实现最小 JSON-RPC 2.0 + MCP 协议子集, 不引入 MCP SDK。
// 协议参考 Model Context Protocol spec, 仅实现股票 agent 所需的最小子集。

const mcpServerName = "phantom-lancer-stockv2"
const mcpServerVersion = "0.1.0"

// mcpJSONRPCRequest 是 JSON-RPC 2.0 请求
type mcpJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"` // number | string
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// mcpJSONRPCResponse 是 JSON-RPC 2.0 响应
type mcpJSONRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *mcpError `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCP 错误码 (JSON-RPC + MCP 定义)
const (
	mcpErrParseError     = -32700
	mcpErrInvalidRequest = -32600
	mcpErrMethodNotFound = -32601
	mcpErrInvalidParams  = -32602
	mcpErrInternal       = -32603
)

// mcpTool 定义一个 MCP tool
type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// submitResultParams 是 submit_result 工具的参数
type submitResultParams struct {
	TaskID   string                  `json:"taskID"`
	TaskType string                  `json:"taskType"`
	Result   submitResultParamsInner `json:"result"`
}

type submitResultParamsInner struct {
	OutputType    string         `json:"outputType"`
	ResultSummary string         `json:"resultSummary"`
	Result        map[string]any `json:"result"`
	Confidence    *float64       `json:"confidence"`
}

func (p *agentTaskPool) HandleMCPRequest(raw []byte) []byte {
	if p.service != nil {
		return p.service.HandleMCPRequest(raw)
	}
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
		result, err = p.mcpInitialize(req.Params)
	case "tools/list":
		result, err = p.mcpToolsList(req.Params)
	case "tools/call":
		result, err = p.mcpToolsCall(req.Params)
	default:
		err = &mcpError{Code: mcpErrMethodNotFound, Message: "Method not found"}
	}

	if err != nil {
		return mcpErrorResponse(req.ID, err.Code, err.Message, err.Data)
	}
	return mcpSuccessResponse(req.ID, result)
}

func (p *agentTaskPool) mcpInitialize(params json.RawMessage) (any, *mcpError) {
	// MCP 握手:返回 server info + capabilities
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    mcpServerName,
			"version": mcpServerVersion,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"instructions": p.mcpInstructions(),
	}, nil
}

func (p *agentTaskPool) mcpToolsList(params json.RawMessage) (any, *mcpError) {
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

	return map[string]any{
		"tools": tools,
	}, nil
}

func (p *agentTaskPool) mcpToolsCall(params json.RawMessage) (any, *mcpError) {
	var callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &callParams); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid params"}
	}
	if callParams.Name == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Tool name is required"}
	}

	switch callParams.Name {
	case "stock_agent.submit_result":
		return p.mcpSubmitResult(callParams.Arguments)
	case "stock_agent.search_instruments":
		return p.mcpSearchInstruments(callParams.Arguments)
	case "stock_agent.search_stock_profiles":
		return p.mcpSearchStockProfiles(callParams.Arguments)
	case "stock_agent.semantic_search_stock_profiles":
		return p.mcpSemanticSearchStockProfiles(callParams.Arguments)
	case "stock_agent.get_stock_profile":
		return p.mcpGetStockProfile(callParams.Arguments)
	case "stock_agent.get_latest_quotes":
		return p.mcpGetLatestQuotes(callParams.Arguments)
	case "stock_agent.get_daily_bars_summary":
		return p.mcpGetDailyBarsSummary(callParams.Arguments)
	case mcpToolGetDailyBars, mcpToolGetMinuteBars, mcpToolGetQuoteHistory,
		mcpToolGetFundFlowHistory, mcpToolGetDecisionEvidence, mcpToolGetMarketScanCandidates:
		svc, errResp := p.mcpService()
		if errResp != nil {
			return nil, errResp
		}
		switch callParams.Name {
		case mcpToolGetDailyBars:
			return svc.mcpGetDailyBars(callParams.Arguments)
		case mcpToolGetMinuteBars:
			return svc.mcpGetMinuteBars(callParams.Arguments)
		case mcpToolGetQuoteHistory:
			return svc.mcpGetQuoteHistory(callParams.Arguments)
		case mcpToolGetFundFlowHistory:
			return svc.mcpGetFundFlowHistory(callParams.Arguments)
		case mcpToolGetDecisionEvidence:
			return svc.mcpGetDecisionEvidence(callParams.Arguments)
		default:
			return svc.mcpGetMarketScanCandidates(callParams.Arguments)
		}
	case "stock_agent.search_news_events":
		return p.mcpSearchNewsEvents(callParams.Arguments)
	case "stock_agent.semantic_search_news_events":
		return p.mcpSemanticSearchNewsEvents(callParams.Arguments)
	case mcpToolSemanticSearchNewsThreads:
		return p.mcpSemanticSearchNewsThreads(callParams.Arguments)
	case mcpToolGetNewsThread:
		return p.mcpGetNewsThread(callParams.Arguments)
	case mcpToolListNewsContextChanges:
		return p.mcpListNewsContextChanges(callParams.Arguments)
	case mcpToolListPortfolioSentinelImpactReviewScope:
		return p.mcpListPortfolioSentinelImpactReviewScope(callParams.Arguments)
	case "stock_agent.search_news_link_candidates":
		return p.mcpSearchNewsLinkCandidates(callParams.Arguments)
	case "stock_agent.list_existing_strategies":
		return p.mcpListExistingStrategies(callParams.Arguments)
	case "stock_agent.get_portfolio_context":
		return p.mcpGetPortfolioContext(callParams.Arguments)
	case "stock_agent.get_embedding_status":
		return p.mcpGetEmbeddingStatus(callParams.Arguments)
	default:
		return nil, &mcpError{Code: mcpErrMethodNotFound, Message: "Tool not found: " + callParams.Name}
	}
}

func (p *agentTaskPool) mcpSubmitResult(args json.RawMessage) (any, *mcpError) {
	var params submitResultParams
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments: " + err.Error()}
	}
	params.TaskID = strings.TrimSpace(params.TaskID)
	params.TaskType = strings.TrimSpace(params.TaskType)
	params.Result.OutputType = strings.TrimSpace(params.Result.OutputType)
	if params.TaskID == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "taskID is required"}
	}
	if params.TaskType == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "taskType is required"}
	}
	if params.Result.OutputType == "" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "result.outputType is required"}
	}
	if !validAgentTaskOutputType(params.TaskType, params.Result.OutputType) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "invalid result.outputType"}
	}
	if params.TaskType == AgentTaskTypeStrategyGeneration &&
		(params.Result.Confidence == nil || !validStrategyGenerationConfidence(*params.Result.Confidence)) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "result.confidence must be greater than 0 and at most 1"}
	}
	if params.TaskType == AgentTaskTypeNewsEventReview {
		report, err := decodeNewsContextSubmittedResult(params.Result.Result)
		if err == nil && p.service != nil && p.service.store != nil {
			err = p.service.validateNewsContextTaskSubmission(contextFromMCP(), params.TaskID, report)
		}
		if err != nil {
			message := err.Error()
			if !errors.Is(err, ErrInvalidNewsContextResult) {
				message = "invalid news context result: " + message
			}
			return nil, &mcpError{Code: mcpErrInvalidParams, Message: message}
		}
	}
	if params.TaskType == AgentTaskTypeStockProfileSummary {
		if err := validateStockProfileSubmittedResult(params.Result.Result); err != nil {
			return nil, &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
		}
	}

	confidence := 0.0
	if params.Result.Confidence != nil {
		confidence = *params.Result.Confidence
	}
	result := AgentTaskSubmittedResult{
		OutputType:    params.Result.OutputType,
		ResultSummary: params.Result.ResultSummary,
		Result:        params.Result.Result,
		Confidence:    confidence,
	}

	status, err := p.submitResult(params.TaskID, params.TaskType, result)
	if err != nil {
		code := mcpErrInvalidParams
		if errors.Is(err, ErrTaskNotFound) {
			code = mcpErrInvalidParams
		}
		return nil, &mcpError{
			Code:    code,
			Message: err.Error(),
			Data:    map[string]string{"status": status},
		}
	}

	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": "Result accepted. The main program will validate and process it.",
			},
		},
		"isError": false,
	}, nil
}

func validateStockProfileSubmittedResult(value map[string]any) error {
	for _, field := range []string{"summaryZh", "summaryEn"} {
		if _, ok := value[field].(string); !ok {
			return fmt.Errorf("invalid stock profile result: result.%s must be a string", field)
		}
	}
	for _, field := range []string{
		"aliasesZh", "aliasesEn", "keywordsZh", "keywordsEn", "businessLinesZh", "businessLinesEn",
		"riskTagsZh", "riskTagsEn", "sourceNotes",
	} {
		items, ok := value[field].([]any)
		if !ok {
			return fmt.Errorf("invalid stock profile result: result.%s must be an array of strings", field)
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("invalid stock profile result: result.%s must contain only strings", field)
			}
		}
	}
	return nil
}

func validateNewsContextSubmittedResult(value map[string]any) error {
	_, err := decodeNewsContextSubmittedResult(value)
	return err
}

func decodeNewsContextSubmittedResult(value map[string]any) (NewsContextReport, error) {
	var report NewsContextReport
	raw, err := json.Marshal(value)
	if err != nil {
		return report, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return report, err
	}
	if report.SchemaVersion != NewsContextResultSchemaVersion {
		return report, fmt.Errorf("schema_version must be %q", NewsContextResultSchemaVersion)
	}
	if strings.TrimSpace(report.RunID) == "" || strings.TrimSpace(report.WindowType) == "" {
		return report, errors.New("run_id and window_type are required")
	}
	if report.ProcessedNewsIDs == nil || report.ReviewedThreadIDs == nil || report.UnchangedThreadIDs == nil ||
		report.NewsDecisions == nil || report.ThreadChanges == nil || report.SearchAudit == nil {
		return report, errors.New("all result arrays must be present; use [] when empty")
	}
	for index, decision := range report.NewsDecisions {
		if strings.TrimSpace(decision.NewsEventID) == "" || strings.TrimSpace(decision.Disposition) == "" {
			return report, fmt.Errorf("news_decisions[%d] requires news_event_id and disposition", index)
		}
	}
	for index, change := range report.ThreadChanges {
		if strings.TrimSpace(change.Action) == "" || strings.TrimSpace(change.Title) == "" ||
			strings.TrimSpace(change.CoreThesis) == "" || strings.TrimSpace(change.Stage) == "" {
			return report, fmt.Errorf("thread_changes[%d] requires action, title, core_thesis, and stage", index)
		}
	}
	if err := validateNewsContextSearchAudit(report.SearchAudit); err != nil {
		return report, err
	}
	if err := validateNewsContextDecisionEvidenceConsistency(report.ProcessedNewsIDs, report.NewsDecisions, report.ThreadChanges); err != nil {
		return report, err
	}
	return report, nil
}

func mcpSuccessResponse(id any, result any) json.RawMessage {
	resp := mcpJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	raw, _ := json.Marshal(resp)
	return json.RawMessage(raw)
}

func mcpErrorResponse(id any, code int, message string, data any) json.RawMessage {
	resp := mcpJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &mcpError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	raw, _ := json.Marshal(resp)
	return json.RawMessage(raw)
}
