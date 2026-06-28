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
	names := map[string]bool{}
	for _, tool := range decoded.Result.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{codexSubmitResultTool, "stock_agent.start_discovery_step", "stock_agent.record_candidate"} {
		if !names[want] {
			t.Fatalf("tools = %+v, want %s", decoded.Result.Tools, want)
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
	required := map[string]bool{}
	for _, tool := range after.RequiredTools {
		required[tool] = true
	}
	if !required[codexSubmitResultTool] || !required["stock_agent.start_discovery_step"] || !required["stock_agent.record_candidate"] {
		t.Fatalf("required tools = %+v", after.RequiredTools)
	}
}
