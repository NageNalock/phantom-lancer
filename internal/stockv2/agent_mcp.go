package stockv2

import (
	"encoding/json"
	"errors"
	"strings"
)

// 轻量 MCP server。仅实现必要方法: initialize / tools/list / tools/call。
// 工具: stock_agent.submit_result。
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
	Confidence    float64        `json:"confidence"`
}

func (p *agentTaskPool) HandleMCPRequest(raw []byte) []byte {
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
	tools := []mcpTool{
		{
			Name:        "stock_agent.submit_result",
			Description: "Submit the final structured result of a stock agent task to the main program. Only call this ONCE when you have completed your analysis. Do not call it multiple times. The result will be validated by the main program before it is persisted.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"taskID": map[string]any{
						"type":        "string",
						"description": "The unique task ID provided to you in the prompt. This is required for authentication.",
					},
					"taskType": map[string]any{
						"type":        "string",
						"description": "The type of task you are performing (e.g., operation_review). Must match the task type from the prompt.",
					},
					"result": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"outputType": map[string]any{
								"type":        "string",
								"enum":        []string{"trade_signal", "proposed_operation", "strategy_patch", "ignore", "continue_monitoring", "stock_profile_summary", "strategy_generation", "opportunity_discovery"},
								"description": "The type of output result.",
							},
							"resultSummary": map[string]any{
								"type":        "string",
								"description": "A brief summary of your conclusion.",
							},
							"result": map[string]any{
								"type":                 "object",
								"description":          "Structured result object. Include facts, inferences, assumptions, freshnessAssessment, and evidenceAudit. Fields depend on outputType. Do not fabricate missing market, financial, or news data.",
								"additionalProperties": true,
							},
							"confidence": map[string]any{
								"type":        "number",
								"description": "Your confidence in this result, from 0.0 to 1.0.",
								"minimum":     0.0,
								"maximum":     1.0,
							},
						},
						"required": []string{"outputType"},
					},
				},
				"required": []string{"taskID", "taskType", "result"},
			},
		},
	}
	if p.service != nil && p.service.store != nil {
		tools = append(tools, p.mcpDataTools()...)
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
	case "stock_agent.search_news_events":
		return p.mcpSearchNewsEvents(callParams.Arguments)
	case "stock_agent.semantic_search_news_events":
		return p.mcpSemanticSearchNewsEvents(callParams.Arguments)
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

	result := AgentTaskSubmittedResult{
		OutputType:    params.Result.OutputType,
		ResultSummary: params.Result.ResultSummary,
		Result:        params.Result.Result,
		Confidence:    params.Result.Confidence,
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
