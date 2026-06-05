package codexgateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    string  `json:"code"`
	Param   *string `json:"param,omitempty"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	Functions           json.RawMessage `json:"functions,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ServiceTier         string          `json:"service_tier,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	User                string          `json:"user,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
}

type ChatMessage struct {
	Role         string          `json:"role"`
	Content      any             `json:"content"`
	Name         string          `json:"name,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	ToolCalls    json.RawMessage `json:"tool_calls,omitempty"`
	FunctionCall json.RawMessage `json:"function_call,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatCompletionChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []ChatChunkChoice `json:"choices"`
}

type ChatChunkChoice struct {
	Index        int               `json:"index"`
	Delta        map[string]string `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

func StaticModelInputs() []struct {
	ID          string
	DisplayName string
	OwnedBy     string
	Source      string
} {
	return []struct {
		ID          string
		DisplayName string
		OwnedBy     string
		Source      string
	}{
		{ID: "gpt-5.4", DisplayName: "gpt-5.4", OwnedBy: "codex", Source: "static"},
		{ID: "gpt-5.4-mini", DisplayName: "gpt-5.4-mini", OwnedBy: "codex", Source: "static"},
		{ID: "gpt-5.3-codex", DisplayName: "gpt-5.3-codex", OwnedBy: "codex", Source: "static"},
		{ID: "gpt-5-codex", DisplayName: "gpt-5-codex", OwnedBy: "codex", Source: "static"},
	}
}

func ValidateChatRequest(req ChatCompletionRequest) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	for i, message := range req.Messages {
		switch message.Role {
		case "system", "developer", "user", "assistant", "tool", "function":
		default:
			return fmt.Errorf("messages[%d].role is invalid", i)
		}
	}
	return nil
}

func ChatToResponsesPayload(req ChatCompletionRequest, stream bool, defaultInstructions string) map[string]any {
	defaultInstructions = strings.TrimSpace(defaultInstructions)
	if defaultInstructions == "" {
		defaultInstructions = "You are a helpful assistant."
	}
	payload := map[string]any{
		"model":        strings.TrimSpace(req.Model),
		"instructions": chatInstructions(req.Messages, defaultInstructions),
		"stream":       stream,
		"store":        false,
		"input":        chatInput(req.Messages),
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if len(req.Tools) > 0 && string(req.Tools) != "null" {
		var tools any
		if json.Unmarshal(req.Tools, &tools) == nil {
			payload["tools"] = tools
		}
	} else if len(req.Functions) > 0 && string(req.Functions) != "null" {
		if tools := legacyFunctionsToTools(req.Functions); len(tools) > 0 {
			payload["tools"] = tools
		}
	}
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		var toolChoice any
		if json.Unmarshal(req.ToolChoice, &toolChoice) == nil {
			payload["tool_choice"] = toolChoice
		}
	}
	if len(req.ResponseFormat) > 0 && string(req.ResponseFormat) != "null" {
		var responseFormat any
		if json.Unmarshal(req.ResponseFormat, &responseFormat) == nil {
			payload["text"] = map[string]any{"format": prepareResponseFormat(responseFormat)}
		}
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		payload["reasoning"] = map[string]any{"effort": strings.TrimSpace(req.ReasoningEffort), "summary": "auto"}
	}
	if strings.TrimSpace(req.ServiceTier) != "" {
		payload["service_tier"] = strings.TrimSpace(req.ServiceTier)
	}
	if req.MaxCompletionTokens != nil {
		payload["max_output_tokens"] = *req.MaxCompletionTokens
	} else if req.MaxTokens != nil {
		payload["max_output_tokens"] = *req.MaxTokens
	}
	if req.ParallelToolCalls != nil {
		payload["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if strings.TrimSpace(req.User) != "" {
		payload["user"] = strings.TrimSpace(req.User)
	}
	return payload
}

func NormalizeResponsesPayload(body map[string]any) (map[string]any, string, bool, error) {
	payload := make(map[string]any, len(body)+2)
	for key, value := range body {
		payload[key] = value
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, "", false, fmt.Errorf("model is required")
	}
	payload["model"] = model
	if instructions, ok := payload["instructions"].(string); ok {
		payload["instructions"] = instructions
	} else {
		payload["instructions"] = ""
	}
	input, err := normalizeResponsesInput(payload["input"])
	if err != nil {
		return nil, "", false, err
	}
	payload["input"] = input
	payload["store"] = false
	delete(payload, "max_output_tokens")
	stream, _ := payload["stream"].(bool)
	return payload, model, stream, nil
}

func BuildChatResponse(id, model, text string, usage Usage) ChatCompletionResponse {
	return ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
		Usage: usage,
	}
}

func BuildChatChunk(id, model, delta string, finishReason *string) ChatCompletionChunk {
	choice := ChatChunkChoice{Index: 0, Delta: map[string]string{}, FinishReason: finishReason}
	if delta != "" {
		choice.Delta["content"] = delta
	}
	return ChatCompletionChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{choice},
	}
}

func UsageFromResponses(value any) Usage {
	data, ok := value.(map[string]any)
	if !ok {
		return Usage{}
	}
	input := number(data["input_tokens"])
	output := number(data["output_tokens"])
	if input == 0 {
		input = number(data["prompt_tokens"])
	}
	if output == 0 {
		output = number(data["completion_tokens"])
	}
	return Usage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}
}

func chatInstructions(messages []ChatMessage, fallback string) string {
	parts := []string{}
	for _, message := range messages {
		if message.Role != "system" && message.Role != "developer" {
			continue
		}
		if text := contentText(message.Content); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, "\n\n")
}

func chatInput(messages []ChatMessage) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			continue
		case "assistant":
			text := contentText(message.Content)
			if text != "" || len(message.ToolCalls) == 0 && len(message.FunctionCall) == 0 {
				input = append(input, map[string]any{"role": "assistant", "content": text})
			}
			input = appendToolCalls(input, message.ToolCalls)
			input = appendLegacyFunctionCall(input, message.FunctionCall)
		case "tool":
			callID := strings.TrimSpace(message.ToolCallID)
			if callID == "" {
				callID = "unknown"
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": callID, "output": contentText(message.Content)})
		case "function":
			name := strings.TrimSpace(message.Name)
			if name == "" {
				name = "unknown"
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": "fc_" + name, "output": contentText(message.Content)})
		default:
			input = append(input, map[string]any{"role": "user", "content": inputContent(message.Content)})
		}
	}
	if len(input) == 0 {
		input = append(input, map[string]any{"role": "user", "content": ""})
	}
	return input
}

func inputContent(content any) any {
	switch typed := content.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		hasImage := false
		for _, item := range typed {
			part, ok := item.(map[string]any)
			if ok && part["type"] == "image_url" {
				hasImage = true
				break
			}
		}
		if !hasImage {
			return contentText(content)
		}
		parts := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if converted := convertContentPart(part); converted != nil {
				parts = append(parts, converted)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return parts
	default:
		data, err := json.Marshal(content)
		if err != nil {
			return fmt.Sprint(content)
		}
		return string(data)
	}
}

func convertContentPart(part map[string]any) map[string]any {
	switch part["type"] {
	case "text":
		text, _ := part["text"].(string)
		return map[string]any{"type": "input_text", "text": text}
	case "input_text", "input_image":
		return part
	case "image_url":
		out := map[string]any{"type": "input_image"}
		switch value := part["image_url"].(type) {
		case string:
			out["image_url"] = value
		case map[string]any:
			if rawURL, ok := value["url"].(string); ok {
				out["image_url"] = rawURL
			}
			if detail, ok := value["detail"].(string); ok {
				out["detail"] = detail
			}
		}
		if _, ok := out["image_url"]; !ok {
			return nil
		}
		return out
	default:
		return part
	}
}

func contentText(content any) string {
	switch typed := content.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		parts := []string{}
		for _, item := range typed {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := record["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		data, err := json.Marshal(content)
		if err != nil {
			return fmt.Sprint(content)
		}
		return string(data)
	}
}

func appendToolCalls(input []map[string]any, raw json.RawMessage) []map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return input
	}
	var calls []map[string]any
	if json.Unmarshal(raw, &calls) != nil {
		return input
	}
	for _, call := range calls {
		callID, _ := call["id"].(string)
		function, _ := call["function"].(map[string]any)
		name, _ := function["name"].(string)
		arguments, _ := function["arguments"].(string)
		if strings.TrimSpace(callID) == "" || strings.TrimSpace(name) == "" {
			continue
		}
		input = append(input, map[string]any{"type": "function_call", "call_id": callID, "name": name, "arguments": arguments})
	}
	return input
}

func appendLegacyFunctionCall(input []map[string]any, raw json.RawMessage) []map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return input
	}
	var call map[string]any
	if json.Unmarshal(raw, &call) != nil {
		return input
	}
	name, _ := call["name"].(string)
	arguments, _ := call["arguments"].(string)
	if strings.TrimSpace(name) == "" {
		return input
	}
	return append(input, map[string]any{"type": "function_call", "call_id": "fc_" + name, "name": name, "arguments": arguments})
}

func legacyFunctionsToTools(raw json.RawMessage) []map[string]any {
	var functions []map[string]any
	if json.Unmarshal(raw, &functions) != nil {
		return nil
	}
	tools := []map[string]any{}
	for _, fn := range functions {
		if _, ok := fn["name"].(string); ok {
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
	}
	return tools
}

func prepareResponseFormat(value any) any {
	format, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if schemaBlock, ok := format["json_schema"].(map[string]any); ok {
		if schema, ok := schemaBlock["schema"].(map[string]any); ok {
			schemaBlock["schema"] = injectAdditionalProperties(schema)
		}
	}
	if schema, ok := format["schema"].(map[string]any); ok {
		format["schema"] = injectAdditionalProperties(schema)
	}
	return format
}

func injectAdditionalProperties(node map[string]any) map[string]any {
	if node["type"] == "object" {
		if _, ok := node["additionalProperties"]; !ok {
			node["additionalProperties"] = false
		}
	}
	for _, key := range []string{"properties", "$defs", "definitions"} {
		children, ok := node[key].(map[string]any)
		if !ok {
			continue
		}
		for childKey, child := range children {
			if childMap, ok := child.(map[string]any); ok {
				children[childKey] = injectAdditionalProperties(childMap)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "not"} {
		if child, ok := node[key].(map[string]any); ok {
			node[key] = injectAdditionalProperties(child)
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		children, ok := node[key].([]any)
		if !ok {
			continue
		}
		for i, child := range children {
			if childMap, ok := child.(map[string]any); ok {
				children[i] = injectAdditionalProperties(childMap)
			}
		}
	}
	return node
}

func normalizeResponsesInput(value any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return []map[string]any{}, nil
	case string:
		return []map[string]any{{"role": "user", "content": typed}}, nil
	case []any:
		return typed, nil
	default:
		return nil, fmt.Errorf("input must be an array or string")
	}
}

func number(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
