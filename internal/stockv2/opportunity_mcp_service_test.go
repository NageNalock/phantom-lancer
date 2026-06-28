package stockv2

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOpportunityMCPToolsExcludeExternalSearchAndFailSemanticWithoutEmbedding(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()

	resp := svc.HandleMCPRequest([]byte(`{"jsonrpc":"2.0","id":"tools","method":"tools/list"}`))
	var toolsDecoded struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &toolsDecoded); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range toolsDecoded.Result.Tools {
		names[tool.Name] = true
		if strings.Contains(tool.Name, "web_search") || strings.Contains(tool.Name, "web_fetch") {
			t.Fatalf("external search tool leaked into stock_agent MCP tools: %s", tool.Name)
		}
	}
	for _, want := range []string{"stock_agent.search_stock_profiles", "stock_agent.record_evidence", "stock_agent.submit_result"} {
		if !names[want] {
			t.Fatalf("tools=%+v, missing %s", toolsDecoded.Result.Tools, want)
		}
	}

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "semantic",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.semantic_search_stock_profiles",
			"arguments": map[string]any{
				"query": "AI model",
				"limit": 5,
			},
		},
	}
	payload, _ := json.Marshal(req)
	resp = svc.HandleMCPRequest(payload)
	var errorDecoded struct {
		Error struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &errorDecoded); err != nil {
		t.Fatalf("decode semantic error: %v", err)
	}
	if errorDecoded.Error.Code == 0 || errorDecoded.Error.Data["code"] != EmbeddingStatusModelNotConfigured {
		t.Fatalf("semantic error=%+v, want embedding_model_not_configured", errorDecoded.Error)
	}

	if status, err := svc.GetEmbeddingStatus(context.Background()); err != nil || status.Code != EmbeddingStatusModelNotConfigured {
		t.Fatalf("status=%+v err=%v, want model_not_configured", status, err)
	}
}
