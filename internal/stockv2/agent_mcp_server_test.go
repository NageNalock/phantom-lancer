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
	svc := NewService(nil, slog.Default(), http.DefaultClient)
	defer svc.Close()

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
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	toolNames := map[string]bool{}
	for _, tool := range decoded.Result.Tools {
		toolNames[tool.Name] = true
	}
	for _, want := range []string{codexSubmitResultTool, "stock_agent.search_instruments", "stock_agent.record_candidate", "stock_agent.semantic_search_stock_profiles"} {
		if !toolNames[want] {
			t.Fatalf("tools = %+v, missing %s", decoded.Result.Tools, want)
		}
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
