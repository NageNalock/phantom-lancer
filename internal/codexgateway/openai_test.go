package codexgateway

import (
	"encoding/json"
	"testing"
)

func TestChatToResponsesPayloadConvertsChatMessages(t *testing.T) {
	maxTokens := 120
	req := ChatCompletionRequest{
		Model: " gpt-5-codex ",
		Messages: []ChatMessage{
			{Role: "system", Content: "system guidance"},
			{Role: "developer", Content: "developer guidance"},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "describe this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/image.png", "detail": "low"}},
			}},
			{Role: "assistant", Content: "I will call a tool.", ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"codex\"}"}}]`)},
			{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
		},
		Stream:          true,
		MaxTokens:       &maxTokens,
		ReasoningEffort: "medium",
	}

	payload := ChatToResponsesPayload(req, true, "fallback")
	if payload["model"] != "gpt-5-codex" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if payload["instructions"] != "system guidance\n\ndeveloper guidance" {
		t.Fatalf("instructions = %#v", payload["instructions"])
	}
	if payload["store"] != false || payload["stream"] != true {
		t.Fatalf("store/stream = %#v/%#v", payload["store"], payload["stream"])
	}
	if payload["max_output_tokens"] != maxTokens {
		t.Fatalf("max_output_tokens = %#v", payload["max_output_tokens"])
	}

	input, ok := payload["input"].([]map[string]any)
	if !ok || len(input) != 4 {
		t.Fatalf("input = %#v", payload["input"])
	}
	userContent, ok := input[0]["content"].([]map[string]any)
	if !ok || len(userContent) != 2 {
		t.Fatalf("user content = %#v", input[0]["content"])
	}
	if userContent[0]["type"] != "input_text" || userContent[1]["type"] != "input_image" {
		t.Fatalf("converted content = %#v", userContent)
	}
	if input[2]["type"] != "function_call" || input[2]["call_id"] != "call_1" || input[3]["type"] != "function_call_output" {
		t.Fatalf("tool conversion = %#v", input)
	}
}

func TestNormalizeResponsesPayloadKeepsResponsesShape(t *testing.T) {
	body := map[string]any{
		"model":             " gpt-5-codex ",
		"input":             "hello",
		"stream":            true,
		"store":             true,
		"max_output_tokens": 300,
	}

	payload, model, stream, err := NormalizeResponsesPayload(body)
	if err != nil {
		t.Fatalf("NormalizeResponsesPayload: %v", err)
	}
	if model != "gpt-5-codex" || !stream {
		t.Fatalf("model/stream = %q/%v", model, stream)
	}
	if payload["store"] != false {
		t.Fatalf("store = %#v", payload["store"])
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens should be removed: %#v", payload)
	}
	if payload["instructions"] != "" {
		t.Fatalf("instructions default = %#v", payload["instructions"])
	}
}

func TestNormalizeResponsesPayloadRequiresModel(t *testing.T) {
	if _, _, _, err := NormalizeResponsesPayload(map[string]any{"input": "hello"}); err == nil {
		t.Fatal("expected model validation error")
	}
}
