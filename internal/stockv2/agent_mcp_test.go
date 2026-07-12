package stockv2

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMCP_Initialize(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
			Capabilities struct {
				Tools any `json:"tools"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %s, want 2.0", result.JSONRPC)
	}
	if result.Result.ServerInfo.Name != mcpServerName {
		t.Errorf("server name = %s, want %s", result.Result.ServerInfo.Name, mcpServerName)
	}
}

func TestMCP_ToolsList(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/list",
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		Result  struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	names := map[string]bool{}
	for _, tool := range result.Result.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"stock_agent.submit_result",
		"stock_agent.start_discovery_step",
		"stock_agent.record_external_source",
		"stock_agent.record_candidate",
		"stock_agent.get_embedding_status",
	} {
		if !names[want] {
			t.Fatalf("tool %s not found in %#v", want, names)
		}
	}
}

func TestMCP_SubmitResult(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, _ := p.createTask(AgentTaskTypeOperationReview, "run-1", "review-1", 5*time.Minute)

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeOperationReview,
				"result": map[string]any{
					"outputType":    "ignore",
					"resultSummary": "no action needed",
					"result":        map[string]any{"reason": "low confidence"},
					"confidence":    0.7,
				},
			},
		},
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Result.IsError {
		t.Error("isError should be false")
	}
	if len(result.Result.Content) == 0 || result.Result.Content[0].Text == "" {
		t.Error("content should not be empty")
	}

	// 验证任务状态
	entry, ok := p.getTask(taskID)
	if !ok {
		t.Fatal("task should exist")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.status != agentTaskStatusSubmitted {
		t.Errorf("status = %s, want submitted", entry.status)
	}
	if entry.submittedResult == nil || entry.submittedResult.OutputType != "ignore" {
		t.Error("submitted result mismatch")
	}
}

func TestMCP_SubmitNewsContextResult(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, entry := p.createTask(AgentTaskTypeNewsEventReview, "run-news-context", "", 5*time.Minute)
	resp := p.HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": codexSubmitResultTool,
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeNewsEventReview,
				"result": map[string]any{
					"outputType":    NewsContextOutputType,
					"resultSummary": "消息脉络归纳完成",
					"result":        map[string]any{"schema_version": "news-context-result/v1"},
					"confidence":    0.8,
				},
			},
		},
	}))
	if strings.Contains(string(resp), `"error"`) {
		t.Fatalf("response=%s, want accepted result", resp)
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.status != agentTaskStatusSubmitted || entry.submittedResult == nil || entry.submittedResult.OutputType != NewsContextOutputType {
		t.Fatalf("entry=%+v, want submitted news context result", entry)
	}
}

func TestMCP_SubmitResult_InvalidTaskID(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   "nonexistent",
				"taskType": AgentTaskTypeOperationReview,
				"result": map[string]any{
					"outputType": "ignore",
				},
			},
		},
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(resp, &result)
	if result.Error == nil {
		t.Error("expected error response")
	}
}

func TestMCP_SubmitResult_MissingTaskID(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   "   ",
				"taskType": AgentTaskTypeOperationReview,
				"result": map[string]any{
					"outputType": "ignore",
				},
			},
		},
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error == nil || result.Error.Code != mcpErrInvalidParams {
		t.Fatalf("expected invalid params error, got: %+v", result.Error)
	}
}

func TestMCP_SubmitResult_InvalidOutputTypeRejected(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, _ := p.createTask(AgentTaskTypeOperationReview, "run-1", "review-1", 5*time.Minute)
	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeOperationReview,
				"result": map[string]any{
					"outputType": "made_up_action",
				},
			},
		},
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error == nil || result.Error.Code != mcpErrInvalidParams {
		t.Fatalf("expected invalid params error, got: %+v", result.Error)
	}

	entry, ok := p.getTask(taskID)
	if !ok {
		t.Fatal("task should exist")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.status != agentTaskStatusWaiting {
		t.Fatalf("status = %s, want waiting", entry.status)
	}
}

func TestMCP_SubmitResult_StrategyGenerationAccepted(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, _ := p.createTask(AgentTaskTypeStrategyGeneration, "run-1", "", 5*time.Minute)
	resp := p.HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeStrategyGeneration,
				"result": map[string]any{
					"outputType":    AgentTaskTypeStrategyGeneration,
					"resultSummary": "draft strategy ready",
					"result": map[string]any{
						"schema_version": StrategyGenerationReportSchemaVersion,
						"run_summary": map[string]any{
							"mode": StrategyGenerationModeManualTarget,
						},
						"drafts": []any{map[string]any{
							"symbol":     "302132",
							"draft_type": StrategyGenerationDraftTypeNewStrategy,
							"thesis":     "test thesis",
							"playbook": map[string]any{
								"rules": []any{map[string]any{"id": "observe_1", "action": StrategyGenerationRuleActionObserve}},
							},
						}},
					},
					"confidence": 0.7,
				},
			},
		},
	}))
	var result struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	entry, ok := p.getTask(taskID)
	if !ok {
		t.Fatal("task should exist")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.status != agentTaskStatusSubmitted || entry.submittedResult.OutputType != AgentTaskTypeStrategyGeneration {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestMCP_SubmitResult_StrategyGenerationRejectsIllegalOutputType(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, _ := p.createTask(AgentTaskTypeStrategyGeneration, "run-1", "", 5*time.Minute)
	resp := p.HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeStrategyGeneration,
				"result": map[string]any{
					"outputType": OperationReviewOutputProposedOperation,
				},
			},
		},
	}))
	var result struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error == nil || result.Error.Code != mcpErrInvalidParams {
		t.Fatalf("expected invalid params error, got: %+v", result.Error)
	}
	entry, ok := p.getTask(taskID)
	if !ok {
		t.Fatal("task should exist")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.status != agentTaskStatusWaiting {
		t.Fatalf("status = %s, want waiting", entry.status)
	}
}

func TestMCP_SubmitResult_RejectsTaskTypeMismatch(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, _ := p.createTask(AgentTaskTypeStrategyGeneration, "run-1", "", 5*time.Minute)
	resp := p.HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeOperationReview,
				"result": map[string]any{
					"outputType": OperationReviewOutputContinueMonitoring,
				},
			},
		},
	}))
	var result struct {
		Error *struct {
			Code int            `json:"code"`
			Data map[string]any `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error == nil || result.Error.Code != mcpErrInvalidParams {
		t.Fatalf("expected invalid params error, got: %+v", result.Error)
	}
	if result.Error.Data["status"] != "invalid_task" {
		t.Fatalf("error data = %+v, want invalid_task status", result.Error.Data)
	}

	entry, ok := p.getTask(taskID)
	if !ok {
		t.Fatal("task should exist")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.status != agentTaskStatusWaiting || entry.submittedResult != nil {
		t.Fatalf("entry after mismatch = %+v, want waiting without result", entry)
	}
}

func TestMCP_SubmitResult_DuplicateSubmitKeepsFirstResult(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	taskID, _ := p.createTask(AgentTaskTypeStrategyGeneration, "run-1", "", 5*time.Minute)
	first := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeStrategyGeneration,
				"result": map[string]any{
					"outputType":    AgentTaskTypeStrategyGeneration,
					"resultSummary": "first result",
					"result":        map[string]any{"schema_version": StrategyGenerationReportSchemaVersion},
					"confidence":    0.8,
				},
			},
		},
	})
	firstResp := p.HandleMCPRequest(first)
	var firstResult struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(firstResp, &firstResult); err != nil {
		t.Fatalf("unmarshal first response: %v\nresp: %s", err, string(firstResp))
	}
	if firstResult.Error != nil {
		t.Fatalf("first submit returned error: %s", string(firstResp))
	}
	second := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.submit_result",
			"arguments": map[string]any{
				"taskID":   taskID,
				"taskType": AgentTaskTypeStrategyGeneration,
				"result": map[string]any{
					"outputType":    AgentTaskTypeStrategyGeneration,
					"resultSummary": "second result",
					"result":        map[string]any{"schema_version": StrategyGenerationReportSchemaVersion},
				},
			},
		},
	})
	resp := p.HandleMCPRequest(second)
	var result struct {
		Error *struct {
			Code int            `json:"code"`
			Data map[string]any `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v\nresp: %s", err, string(resp))
	}
	if result.Error == nil || result.Error.Code != mcpErrInvalidParams || result.Error.Data["status"] != "duplicate" {
		t.Fatalf("duplicate response = %+v", result.Error)
	}

	entry, ok := p.getTask(taskID)
	if !ok {
		t.Fatal("task should exist")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.submitCount != 1 || entry.submittedResult == nil || entry.submittedResult.ResultSummary != "first result" {
		t.Fatalf("entry after duplicate = %+v, want first result only", entry)
	}
}

func TestMCP_MethodNotFound(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "nonexistent_method",
	})

	resp := p.HandleMCPRequest(req)
	var result struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(resp, &result)
	if result.Error == nil || result.Error.Code != mcpErrMethodNotFound {
		t.Errorf("expected method not found error, got: %v", result.Error)
	}
}

func TestMCP_InvalidJSON(t *testing.T) {
	p := newAgentTaskPool(defaultCleanupInterval)
	defer p.Close()

	resp := p.HandleMCPRequest([]byte("not json"))
	var result struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(resp, &result)
	if result.Error == nil || result.Error.Code != mcpErrParseError {
		t.Errorf("expected parse error, got: %v", result.Error)
	}
}

// 辅助: map -> JSON bytes
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
