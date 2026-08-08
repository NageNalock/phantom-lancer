package stockv2

import (
	"encoding/json"
	"testing"
)

func TestDeepSeekDirectOutputSchemasBindTaskAndDomainResult(t *testing.T) {
	tests := []struct {
		name      string
		build     func() ([]byte, error)
		taskType  string
		resultKey string
	}{
		{name: "profile", build: func() ([]byte, error) { return stockProfileDirectOutputSchema("task-profile") }, taskType: AgentTaskTypeStockProfileSummary, resultKey: "summaryZh"},
		{name: "news", build: func() ([]byte, error) {
			return newsContextDirectOutputSchema("task-news", "run-news", NewsContextWindowFourHour)
		}, taskType: AgentTaskTypeNewsEventReview, resultKey: "thread_changes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := tt.build()
			if err != nil {
				t.Fatalf("build schema: %v", err)
			}
			var schema map[string]any
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatalf("decode schema: %v", err)
			}
			properties := schema["properties"].(map[string]any)
			if properties["taskType"].(map[string]any)["const"] != tt.taskType || schema["additionalProperties"] != false {
				t.Fatalf("task envelope schema = %#v", schema)
			}
			resultEnvelope := properties["result"].(map[string]any)["properties"].(map[string]any)
			domain := resultEnvelope["result"].(map[string]any)["properties"].(map[string]any)
			if _, ok := domain[tt.resultKey]; !ok {
				t.Fatalf("domain schema missing %q: %#v", tt.resultKey, domain)
			}
		})
	}
}
