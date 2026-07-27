package stockv2

import (
	"encoding/json"
	"fmt"
	"strings"
)

func agentStringListFromRaw(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	return agentStringListFromAny(decoded)
}

func agentStringListFromAny(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if text := strings.TrimSpace(v); text != "" {
			return []string{text}
		}
		return nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(agentTextFromAny(item)); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) == 0 {
			return nil
		}
		return parts
	default:
		if text := strings.TrimSpace(agentTextFromAny(v)); text != "" {
			return []string{text}
		}
		return nil
	}
}

func agentObjectListFromRaw(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	values := []any{decoded}
	if list, ok := decoded.([]any); ok {
		values = list
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			items = append(items, item)
			continue
		}
		if text := agentTextFromAny(value); text != "" {
			// ponytail: these fields are display-only free-form items; preserve weak model output
			// as a summary map instead of adding a second public result schema.
			items = append(items, map[string]any{"summary": text})
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func agentTextFromAny(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := agentTextFromAny(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		return agentTextFromMap(v)
	case float64, bool:
		return fmt.Sprint(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func agentTextFromMap(value map[string]any) string {
	textKeys := []string{"note", "summary", "reason", "detail", "title", "message", "description", "status"}
	parts := make([]string, 0, 4)
	for _, key := range []string{"severity", "priority", "type", "category", "item", "field"} {
		if text := strings.TrimSpace(stringFromAny(value[key])); text != "" {
			parts = append(parts, text)
		}
	}
	for _, key := range textKeys {
		if text := strings.TrimSpace(stringFromAny(value[key])); text != "" {
			if len(parts) > 0 {
				return "[" + strings.Join(parts, "/") + "] " + text
			}
			return text
		}
	}
	if payload, err := json.Marshal(value); err == nil {
		return string(payload)
	}
	return ""
}
