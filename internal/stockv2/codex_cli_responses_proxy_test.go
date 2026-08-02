package stockv2

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransformCodexResponsesRequestFlattensNamespaceToolsAndPriorCalls(t *testing.T) {
	body := []byte(`{
		"model":"ark-code-latest",
		"tools":[{
			"type":"namespace",
			"name":"stock_agent",
			"tools":[{"type":"function","name":"submit_result","description":"submit","parameters":{"type":"object"}}]
		}],
		"input":[{"type":"function_call","namespace":"stock_agent","name":"submit_result","call_id":"call-1","arguments":"{}"}]
	}`)
	transformed, mapping, err := transformCodexResponsesRequest(body)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}
	if len(mapping) != 1 {
		t.Fatalf("mapping = %#v, want one tool", mapping)
	}

	var payload map[string]any
	if err := json.Unmarshal(transformed, &payload); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	tools := payload["tools"].([]any)
	tool := tools[0].(map[string]any)
	encoded := stringFromAny(tool["name"])
	if tool["type"] != "function" || encoded == "submit_result" || len(encoded) > codexResponsesToolNameMaxBytes {
		t.Fatalf("flattened tool = %#v", tool)
	}
	if got := mapping[encoded]; got != (codexNamespaceToolName{Namespace: "stock_agent", Name: "submit_result"}) {
		t.Fatalf("mapping[%q] = %#v", encoded, got)
	}
	call := payload["input"].([]any)[0].(map[string]any)
	if call["name"] != encoded {
		t.Fatalf("prior call name = %#v, want %q", call["name"], encoded)
	}
	if _, ok := call["namespace"]; ok {
		t.Fatalf("prior call retained namespace: %#v", call)
	}

	response := []byte(`{"type":"response.output_item.added","item":{"type":"function_call","name":"` + encoded + `","call_id":"call-2","arguments":"{}"}}`)
	restored, err := transformCodexResponsesPayload(response, mapping)
	if err != nil {
		t.Fatalf("restore response: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(restored, &event); err != nil {
		t.Fatalf("decode restored response: %v", err)
	}
	item := event["item"].(map[string]any)
	if item["namespace"] != "stock_agent" || item["name"] != "submit_result" {
		t.Fatalf("restored function call = %#v", item)
	}
}

func TestCodexCLIResponsesProxyInjectsProviderKeyAndStreamsRestoredNamespace(t *testing.T) {
	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/coding/v3/responses" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Fatalf("upstream authorization = %q", got)
		}
		upstreamBody, _ = io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(upstreamBody, &payload); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		tool := payload["tools"].([]any)[0].(map[string]any)
		encoded := stringFromAny(tool["name"])
		if tool["type"] != "function" || strings.Contains(string(upstreamBody), `"type":"namespace"`) {
			t.Fatalf("upstream tools were not flattened: %s", upstreamBody)
		}
		textFormat := payload["text"].(map[string]any)["format"].(map[string]any)
		if textFormat["type"] != "json_schema" || textFormat["name"] != "portfolio_sentinel" {
			t.Fatalf("upstream output format = %#v", textFormat)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.added","item":{"type":"function_call","name":"`+encoded+`","call_id":"call-1","arguments":"{}"}}`+"\n\n")
	}))
	defer upstream.Close()

	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.httpClient = upstream.Client()
	provider, err := svc.CreateAgentProviderProfile(context.Background(), RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		DisplayName:  "Coding Plan",
		BaseURL:      upstream.URL + "/api/coding/v3",
		APIKey:       "provider-secret",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := svc.StartAgentMCPServer(); err != nil {
		t.Fatalf("start Agent loopback server: %v", err)
	}
	proxyBaseURL, err := svc.agentCodexCLIProxyBaseURL(provider.ID)
	if err != nil {
		t.Fatalf("proxy URL: %v", err)
	}
	requestBody := []byte(`{"model":"ark-code-latest","stream":true,"text":{"format":{"type":"json_schema","name":"portfolio_sentinel","schema":{"type":"object"}}},"tools":[{"type":"namespace","name":"stock_agent","tools":[{"type":"function","name":"submit_result","parameters":{"type":"object"}}]}],"input":"test"}`)
	request, err := http.NewRequest(http.MethodPost, proxyBaseURL+"/responses", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer must-not-be-forwarded")
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d, body = %s", response.StatusCode, responseBody)
	}
	if !strings.Contains(string(responseBody), `"namespace":"stock_agent"`) ||
		!strings.Contains(string(responseBody), `"name":"submit_result"`) {
		t.Fatalf("proxy response did not restore namespace: %s", responseBody)
	}
	for _, secret := range []string{"provider-secret", "must-not-be-forwarded"} {
		if strings.Contains(string(responseBody), secret) || strings.Contains(string(upstreamBody), secret) {
			t.Fatalf("secret %q leaked into request or response body", secret)
		}
	}
}

func TestCodexCLIResponsesProxyRejectsNonCLIProvider(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	provider, err := svc.CreateAgentProviderProfile(context.Background(), RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		BaseURL:      "https://example.test/v1",
		APIKey:       "test-secret",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := svc.StartAgentMCPServer(); err != nil {
		t.Fatalf("start Agent loopback server: %v", err)
	}
	proxyBaseURL, err := svc.agentCodexCLIProxyBaseURL(provider.ID)
	if err != nil {
		t.Fatalf("proxy URL: %v", err)
	}
	response, err := http.Post(proxyBaseURL+"/responses", "application/json", strings.NewReader(`{"input":"test"}`))
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}
