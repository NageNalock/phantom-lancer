package stockv2

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestAgentMCPServerUsesLoopbackHTTP(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.log = slog.Default()

	endpoint, err := svc.StartAgentMCPServer()
	if err != nil {
		t.Fatalf("StartAgentMCPServer: %v", err)
	}
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
		t.Fatalf("endpoint = %q, want loopback http", endpoint)
	}

	body := []byte(`{"jsonrpc":"2.0","id":"tools","method":"tools/list"}`)
	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post tools/list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var decoded struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	toolNames := map[string]bool{}
	toolSchemas := map[string]map[string]any{}
	for _, tool := range decoded.Result.Tools {
		toolNames[tool.Name] = true
		toolSchemas[tool.Name] = tool.InputSchema
	}
	for _, want := range []string{
		codexSubmitResultTool,
		"stock_agent.search_instruments",
		"stock_agent.record_candidate",
		"stock_agent.semantic_search_stock_profiles",
		mcpToolSemanticSearchNewsThreads,
		mcpToolGetNewsThread,
		mcpToolListNewsContextChanges,
	} {
		if !toolNames[want] {
			t.Fatalf("tools = %+v, missing %s", decoded.Result.Tools, want)
		}
	}
	for name, requiredField := range map[string]string{
		mcpToolSemanticSearchNewsThreads: "query",
		mcpToolGetNewsThread:             "threadId",
		mcpToolListNewsContextChanges:    "runId",
	} {
		required, _ := toolSchemas[name]["required"].([]any)
		if len(required) != 1 || required[0] != requiredField {
			t.Fatalf("tool %s schema = %#v, want required %s", name, toolSchemas[name], requiredField)
		}
	}
	properties, _ := toolSchemas[mcpToolSemanticSearchNewsThreads]["properties"].(map[string]any)
	if _, ok := properties["asOf"]; !ok {
		t.Fatalf("semantic theme schema = %#v, want optional asOf cutoff", toolSchemas[mcpToolSemanticSearchNewsThreads])
	}
	properties, _ = toolSchemas[mcpToolGetNewsThread]["properties"].(map[string]any)
	if _, ok := properties["asOf"]; !ok {
		t.Fatalf("theme detail schema = %#v, want optional asOf cutoff", toolSchemas[mcpToolGetNewsThread])
	}
}

func TestAgentTaskPoolNewsThreadSchemasExposeHistoricalCutoff(t *testing.T) {
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	tools := pool.mcpDataTools()
	for _, name := range []string{mcpToolSemanticSearchNewsThreads, mcpToolGetNewsThread} {
		found := false
		for _, tool := range tools {
			if tool.Name != name {
				continue
			}
			found = true
			schema, _ := tool.InputSchema.(map[string]any)
			properties, _ := schema["properties"].(map[string]any)
			if _, ok := properties["asOf"]; !ok {
				t.Fatalf("tool %s schema = %#v, want optional asOf cutoff", name, tool.InputSchema)
			}
		}
		if !found {
			t.Fatalf("tool %s not found", name)
		}
	}
}

func TestNextMCPPageOffset(t *testing.T) {
	if got := nextMCPPageOffset(0, 50, 50, 120); got != 50 {
		t.Fatalf("next offset = %#v, want 50", got)
	}
	if got := nextMCPPageOffset(100, 50, 20, 120); got != nil {
		t.Fatalf("last page next offset = %#v, want nil", got)
	}
}

func TestAgentMCPStatusReflectsLoopbackServer(t *testing.T) {
	svc := NewService(nil, slog.Default(), http.DefaultClient)
	defer svc.Close()

	before := svc.AgentMCPStatus()
	if before.Enabled || before.URL != "" {
		t.Fatalf("before = %+v, want disabled without URL", before)
	}

	endpoint, err := svc.StartAgentMCPServer()
	if err != nil {
		t.Fatalf("StartAgentMCPServer: %v", err)
	}
	after := svc.AgentMCPStatus()
	if !after.Enabled || after.URL != endpoint || after.Transport != "loopback_http" {
		t.Fatalf("after = %+v, endpoint = %q", after, endpoint)
	}
	if len(after.RequiredTools) != len(stockAgentMCPRequiredTools()) || !stringSliceContains(after.RequiredTools, codexSubmitResultTool) {
		t.Fatalf("required tools = %+v", after.RequiredTools)
	}
}
