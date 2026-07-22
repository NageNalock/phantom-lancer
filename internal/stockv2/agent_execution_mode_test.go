package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentTaskExecutionModeValidatesProvider(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "api-provider",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "api-model",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	mode := AgentExecutionModeAPI
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		ExecutionMode:  &mode,
		PrimaryModelID: &model.ID,
	}); err != nil {
		t.Fatalf("bind API model: %v", err)
	}
	profile, err := svc.GetAgentTaskProfileByType(ctx, AgentTaskTypeNewsEventReview)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.ExecutionMode != AgentExecutionModeAPI {
		t.Fatalf("execution mode = %q", profile.ExecutionMode)
	}

	mode = AgentExecutionModeCLI
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		ExecutionMode: &mode,
	}); !errors.Is(err, ErrAgentExecutionModeModelMismatch) {
		t.Fatalf("CLI with API model error = %v", err)
	}
}

func TestAgentAPIToolNamesAreOpenAICompatible(t *testing.T) {
	name := agentAPIToolName(codexSubmitResultTool)
	if name != "stock_agent_submit_result" {
		t.Fatalf("tool name = %q", name)
	}
	if original, ok := agentAPIToolOriginalName(name); !ok || original != codexSubmitResultTool {
		t.Fatalf("reverse tool name = %q, %v", original, ok)
	}
}

func TestAgentAPIToolCallParamsRejectsMalformedArguments(t *testing.T) {
	if _, toolErr := agentAPIToolCallParams(codexSubmitResultTool, `{"taskID":`); toolErr == nil ||
		toolErr.Code != mcpErrInvalidParams ||
		!strings.Contains(toolErr.Message, "arguments must be valid JSON") ||
		!strings.Contains(toolErr.Message, "10 bytes") {
		t.Fatalf("malformed arguments error = %#v", toolErr)
	}

	params, toolErr := agentAPIToolCallParams(codexSubmitResultTool, `{"taskID":"task-1"}`)
	if toolErr != nil {
		t.Fatalf("valid arguments: %v", toolErr)
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if call.Name != codexSubmitResultTool || string(call.Arguments) != `{"taskID":"task-1"}` {
		t.Fatalf("params = %#v", call)
	}
}

func TestAgentAPINewsContextToolsAndSchemaAreTaskSpecific(t *testing.T) {
	pack := NewsContextAggregationPack{
		RunID:      "run-exact",
		WindowType: NewsContextWindowDaily,
		InputNewsEvents: []NewsEvent{
			{ID: "news-1"},
			{ID: "news-2"},
		},
		InputThreads: []NewsContextPromptThread{{ID: "thread-1", ThemeID: "thread-1"}},
	}
	schema := agentAPINewsContextSubmitResultSchema("task-exact", pack)
	tools := agentAPITools([]string{
		mcpToolSemanticSearchNewsThreads,
		mcpToolGetNewsThread,
		codexSubmitResultTool,
	}, schema)
	if len(tools) != 3 {
		t.Fatalf("news tools = %d; want 3", len(tools))
	}
	wantNames := []string{
		agentAPIToolName(mcpToolSemanticSearchNewsThreads),
		agentAPIToolName(mcpToolGetNewsThread),
		agentAPIToolName(codexSubmitResultTool),
	}
	for index, tool := range tools {
		function := tool["function"].(map[string]any)
		if function["name"] != wantNames[index] {
			t.Fatalf("tool[%d] name = %v; want %q", index, function["name"], wantNames[index])
		}
	}
	submitFunction := tools[len(tools)-1]["function"].(map[string]any)
	if !reflect.DeepEqual(submitFunction["parameters"], schema) {
		t.Fatal("submit tool did not receive the task-specific schema")
	}

	properties := schema["properties"].(map[string]any)
	taskIDSchema := properties["taskID"].(map[string]any)
	if !reflect.DeepEqual(taskIDSchema["enum"], []string{"task-exact"}) {
		t.Fatalf("task id enum = %#v", taskIDSchema["enum"])
	}
	resultProperties := properties["result"].(map[string]any)["properties"].(map[string]any)
	reportProperties := resultProperties["result"].(map[string]any)["properties"].(map[string]any)
	processedItems := reportProperties["processed_news_ids"].(map[string]any)["items"].(map[string]any)
	if !reflect.DeepEqual(processedItems["enum"], []string{"news-1", "news-2"}) {
		t.Fatalf("processed news enum = %#v", processedItems["enum"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("top-level additionalProperties = %#v", schema["additionalProperties"])
	}

	threadOnlyPack := NewsContextAggregationPack{
		RunID:        "run-thread-only",
		WindowType:   NewsContextWindowDaily,
		InputThreads: []NewsContextPromptThread{{ID: "thread-only-1", ThemeID: "thread-only-1"}},
	}
	threadOnlySchema := agentAPINewsContextSubmitResultSchema("task-thread-only", threadOnlyPack)
	threadOnlyTools := agentAPITools([]string{codexSubmitResultTool}, threadOnlySchema)
	if len(threadOnlyTools) != 1 {
		t.Fatalf("thread-only tools = %d; want only submit", len(threadOnlyTools))
	}
	threadOnlyProperties := threadOnlySchema["properties"].(map[string]any)
	threadOnlyResult := threadOnlyProperties["result"].(map[string]any)["properties"].(map[string]any)
	threadOnlyReport := threadOnlyResult["result"].(map[string]any)["properties"].(map[string]any)
	changeItems := threadOnlyReport["thread_changes"].(map[string]any)["items"].(map[string]any)
	changeProperties := changeItems["properties"].(map[string]any)
	if !reflect.DeepEqual(changeProperties["thread_id"].(map[string]any)["enum"], []string{"", "thread-only-1"}) {
		t.Fatalf("thread-only change id enum = %#v", changeProperties["thread_id"])
	}
}

func TestApplyAgentAPIReasoningMapsDeepSeekThinkingMode(t *testing.T) {
	tests := []struct {
		name     string
		deepSeek bool
		effort   string
		want     map[string]any
	}{
		{name: "empty preserves provider default", deepSeek: true, want: map[string]any{}},
		{name: "deepseek low disables thinking", deepSeek: true, effort: AgentReasoningEffortLow, want: map[string]any{
			"thinking": map[string]string{"type": "disabled"},
		}},
		{name: "deepseek medium maps high", deepSeek: true, effort: AgentReasoningEffortMedium, want: map[string]any{
			"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "high",
		}},
		{name: "deepseek max remains max", deepSeek: true, effort: AgentReasoningEffortMax, want: map[string]any{
			"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "max",
		}},
		{name: "other provider keeps requested effort", effort: AgentReasoningEffortLow, want: map[string]any{
			"reasoning_effort": AgentReasoningEffortLow,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{}
			applyAgentAPIReasoning(body, test.deepSeek, test.effort)
			if !reflect.DeepEqual(body, test.want) {
				t.Fatalf("body = %#v; want %#v", body, test.want)
			}
		})
	}
}

func TestIsDeepSeekAPIUsesModelOrExactHostBoundary(t *testing.T) {
	for _, test := range []struct {
		baseURL string
		model   string
		want    bool
	}{
		{baseURL: "https://api.deepseek.com", model: "custom", want: true},
		{baseURL: "https://gateway.example.com/v1", model: "deepseek-chat", want: true},
		{baseURL: "https://deepseek.com.evil.example/v1", model: "custom", want: false},
		{baseURL: "https://api.example.com/deepseek/v1", model: "custom", want: false},
	} {
		if got := isDeepSeekAPI(test.baseURL, test.model); got != test.want {
			t.Fatalf("isDeepSeekAPI(%q, %q) = %v; want %v", test.baseURL, test.model, got, test.want)
		}
	}
}

func TestLimitNewsContextBatchForDeepSeekAPI(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "deepseek-api",
		BaseURL:      "https://api.deepseek.com",
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "deepseek-chat",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	mode := AgentExecutionModeAPI
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		ExecutionMode:  &mode,
		PrimaryModelID: &model.ID,
	}); err != nil {
		t.Fatalf("bind model: %v", err)
	}
	items := make([]NewsContextRunItem, 40)
	for index := range items {
		items[index] = NewsContextRunItem{ObjectType: NewsContextRunItemNewsEvent, ObjectID: generateID()}
	}
	limited, err := svc.limitNewsContextBatchForProvider(ctx, items)
	if err != nil {
		t.Fatalf("limit batch: %v", err)
	}
	if len(limited) != newsContextDeepSeekEventBatchSize {
		t.Fatalf("limited items = %d; want %d", len(limited), newsContextDeepSeekEventBatchSize)
	}
}

func TestFailRunningAgentRunsMarksOnlyActiveRuns(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	running, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		TriggerObjectType: "test", TriggerObjectID: "running", Status: AgentRunStatusRunning,
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "test", TriggerObjectID: "running",
	})
	if err != nil {
		t.Fatalf("create running run: %v", err)
	}
	completed, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		TriggerObjectType: "test", TriggerObjectID: "completed", Status: AgentRunStatusCompleted, FinishedAt: now,
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "test", TriggerObjectID: "completed",
	})
	if err != nil {
		t.Fatalf("create completed run: %v", err)
	}
	if count, err := svc.store.FailRunningAgentRuns(ctx, "restart"); err != nil || count != 1 {
		t.Fatalf("FailRunningAgentRuns = %d, %v", count, err)
	}
	gotRunning, _ := svc.store.GetAgentRun(ctx, running.ID)
	gotCompleted, _ := svc.store.GetAgentRun(ctx, completed.ID)
	if gotRunning.Status != AgentRunStatusFailed || gotRunning.ErrorMessage != "restart" || gotRunning.FinishedAt.IsZero() {
		t.Fatalf("running run = %#v", gotRunning)
	}
	if gotCompleted.Status != AgentRunStatusCompleted {
		t.Fatalf("completed run status = %q", gotCompleted.Status)
	}
}
