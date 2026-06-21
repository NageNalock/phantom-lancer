package stockv2

import (
	"encoding/json"
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
			Server          struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"server"`
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
	if result.Result.Server.Name != mcpServerName {
		t.Errorf("server name = %s, want %s", result.Result.Server.Name, mcpServerName)
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
	if len(result.Result.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(result.Result.Tools))
	}
	if result.Result.Tools[0].Name != "stock_agent.submit_result" {
		t.Errorf("tool name = %s, want stock_agent.submit_result", result.Result.Tools[0].Name)
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
