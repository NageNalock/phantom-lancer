package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStoreReopenPreservesCustomCodexCLIProviderAndSentinelBinding(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "stockv2.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO stockv2_agent_provider_profiles
			(id, provider_type, name, config_state, auth_state, availability, metadata_json, created_at, updated_at)
		VALUES
			('custom-coding', 'codex_cli', 'custom-coding', 'configured', 'authenticated', 'available',
			 '{"baseUrl":"https://example.test/responses","apiKey":"test-key"}', datetime('now'), datetime('now'));
		INSERT INTO stockv2_agent_model_profiles
			(id, provider_id, model_name, enabled, status, metadata_json, created_at, updated_at)
		VALUES
			('custom-coding-model', 'custom-coding', 'coding-model', 1, 'available',
			 '{"modelType":"chat"}', datetime('now'), datetime('now'));
		UPDATE stockv2_agent_task_profiles
		SET execution_mode='cli', primary_model_id='custom-coding-model',
		    fallback_model_id='', reasoning_effort='medium'
		WHERE task_type='portfolio_sentinel';
	`); err != nil {
		_ = store.Close()
		t.Fatalf("seed custom Coding Plan binding: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	provider, err := reopened.GetAgentProviderProfile(ctx, "custom-coding")
	if err != nil {
		t.Fatalf("get custom provider: %v", err)
	}
	if provider.ProviderType != AgentProviderTypeCodexCLI {
		t.Fatalf("provider type = %q, want codex_cli", provider.ProviderType)
	}
	profile, err := reopened.GetAgentTaskProfileByType(ctx, AgentTaskTypePortfolioSentinel)
	if err != nil {
		t.Fatalf("get sentinel task profile: %v", err)
	}
	if profile.ExecutionMode != AgentExecutionModeCLI ||
		profile.PrimaryModelID != "custom-coding-model" ||
		profile.ReasoningEffort != AgentReasoningEffortMedium {
		t.Fatalf("sentinel profile changed on reopen: %#v", profile)
	}
}

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
	if _, toolErr := agentAPIToolCallParams(codexSubmitResultTool, `{"taskID":`, "task-1"); toolErr == nil ||
		toolErr.Code != mcpErrInvalidParams ||
		!strings.Contains(toolErr.Message, "arguments must be valid JSON") ||
		!strings.Contains(toolErr.Message, "10 bytes") {
		t.Fatalf("malformed arguments error = %#v", toolErr)
	}

	params, toolErr := agentAPIToolCallParams(codexSubmitResultTool, `{"taskID":"task-1"}`, "task-1")
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

	params, toolErr = agentAPIToolCallParams(codexSubmitResultTool, `{"taskID":"task-1"}, `, "task-1")
	if toolErr != nil {
		t.Fatalf("trailing separator arguments: %v", toolErr)
	}
	if err := json.Unmarshal(params, &call); err != nil || string(call.Arguments) != `{"taskID":"task-1"}` {
		t.Fatalf("trailing separator params = %#v, err=%v", call, err)
	}
	if _, toolErr := agentAPIToolCallParams(codexSubmitResultTool, `{"taskID":"task-1"}, {"extra":true}`, "task-1"); toolErr == nil {
		t.Fatal("multiple top-level objects were accepted")
	}

	params, toolErr = agentAPIToolCallParams(codexSubmitResultTool, `{"result":{},"taskID":"wrong-task"}`, "task-1")
	if toolErr != nil {
		t.Fatalf("bind submit task id: %v", toolErr)
	}
	if err := json.Unmarshal(params, &call); err != nil {
		t.Fatalf("decode bound params: %v", err)
	}
	var arguments struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil || arguments.TaskID != "task-1" {
		t.Fatalf("bound task id = %q, err=%v", arguments.TaskID, err)
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

func TestApplyDeepSeekAPICompatibilityUsesJSONWithoutThinkingToolChoice(t *testing.T) {
	body := map[string]any{"tool_choice": "auto"}
	applyAgentAPIReasoning(body, true, AgentReasoningEffortMedium)
	applyDeepSeekAPICompatibility(body, AgentReasoningEffortMedium)
	want := map[string]any{
		"thinking":         map[string]string{"type": "enabled"},
		"reasoning_effort": "high",
		"response_format":  map[string]string{"type": "json_object"},
		"max_tokens":       agentAPIDeepSeekMaxTokens,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v; want %#v", body, want)
	}

	nonThinking := map[string]any{"tool_choice": "auto"}
	applyAgentAPIReasoning(nonThinking, true, AgentReasoningEffortLow)
	applyDeepSeekAPICompatibility(nonThinking, AgentReasoningEffortLow)
	if nonThinking["tool_choice"] != "auto" {
		t.Fatalf("non-thinking tool_choice = %#v; want auto", nonThinking["tool_choice"])
	}
	if !reflect.DeepEqual(nonThinking["response_format"], map[string]string{"type": "json_object"}) ||
		nonThinking["max_tokens"] != agentAPIDeepSeekMaxTokens {
		t.Fatalf("non-thinking JSON options = %#v", nonThinking)
	}
}

func TestDeepSeekThinkingDefaultsEnabled(t *testing.T) {
	if !deepSeekThinkingEnabled("") || !deepSeekThinkingEnabled(AgentReasoningEffortMedium) {
		t.Fatal("empty and medium efforts must use DeepSeek thinking mode")
	}
	if deepSeekThinkingEnabled(AgentReasoningEffortLow) {
		t.Fatal("low effort must disable DeepSeek thinking mode")
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
	if len(limited) != 3 {
		t.Fatalf("limited items = %d; want production-safe cap 3", len(limited))
	}
}

func TestLimitNewsContextBatchForCodexCLI(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-cli",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "test-model",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
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
	if len(limited) != newsContextCLIEventBatchSize {
		t.Fatalf("limited items = %d; want %d", len(limited), newsContextCLIEventBatchSize)
	}
}

func TestFailActiveAgentRunsMarksReadyAndRunning(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	ready, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		TriggerObjectType: "test", TriggerObjectID: "ready", Status: AgentRunStatusReady,
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "test", TriggerObjectID: "ready",
	})
	if err != nil {
		t.Fatalf("create ready run: %v", err)
	}
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
	if count, err := svc.store.FailActiveAgentRuns(ctx, "restart"); err != nil || count != 2 {
		t.Fatalf("FailActiveAgentRuns = %d, %v", count, err)
	}
	gotReady, _ := svc.store.GetAgentRun(ctx, ready.ID)
	gotRunning, _ := svc.store.GetAgentRun(ctx, running.ID)
	gotCompleted, _ := svc.store.GetAgentRun(ctx, completed.ID)
	if gotReady.Status != AgentRunStatusFailed || gotReady.ErrorMessage != "restart" || gotReady.FinishedAt.IsZero() {
		t.Fatalf("ready run = %#v", gotReady)
	}
	if gotRunning.Status != AgentRunStatusFailed || gotRunning.ErrorMessage != "restart" || gotRunning.FinishedAt.IsZero() {
		t.Fatalf("running run = %#v", gotRunning)
	}
	if gotCompleted.Status != AgentRunStatusCompleted {
		t.Fatalf("completed run status = %q", gotCompleted.Status)
	}
}

func TestAgentAPIJSONSubmissionContentAcceptsOnlyResultEnvelope(t *testing.T) {
	valid := `{"taskType":"news_event_review","result":{"schema_version":"1"}}`
	content, ok := agentAPIJSONSubmissionContent(valid)
	if !ok || content != valid {
		t.Fatalf("valid JSON submission = %q, %v", content, ok)
	}
	for _, invalid := range []string{
		"",
		`not-json`,
		`[]`,
		`{"message":"done"}`,
		`{"taskType":"news_event_review"}`,
		`{"result":{}}`,
	} {
		if _, ok := agentAPIJSONSubmissionContent(invalid); ok {
			t.Fatalf("accepted non-submission JSON %q", invalid)
		}
	}
}

func TestGenericAPINewsExecutionUsesDirectJSONWithoutTools(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "generic-direct-json",
		BaseURL:      "https://api.example.com",
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	run, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		ProviderID: provider.ID, TriggerObjectType: "test", TriggerObjectID: "generic-direct-json",
		Status: AgentRunStatusRunning, StartedAt: time.Now(),
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, ProviderID: provider.ID,
		TriggerObjectType: "test", TriggerObjectID: "generic-direct-json",
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	requestCount := 0
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		body, _ := io.ReadAll(req.Body)
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode API request: %v", err)
		}
		if _, ok := request["tools"]; ok {
			t.Fatalf("generic API news request exposed tools: %s", body)
		}
		if requestCount > 1 && !strings.Contains(string(body), "previous final JSON was rejected") {
			t.Fatalf("generic correction request lacks validation feedback: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"taskType\":\"news_event_review\",\"result\":{}}"}}]}`,
			)),
		}, nil
	})}

	output, err := newAgentAPIExecutor(svc).executePrompt(
		ctx, taskID, "return JSON", "generic-model", AgentReasoningEffortMedium,
		time.Second, agentAPIExecutionOptions{toolNames: []string{
			mcpToolSemanticSearchNewsThreads,
			mcpToolGetNewsThread,
			codexSubmitResultTool,
		}},
	)
	if err == nil {
		t.Fatal("execute error = nil; want invalid submission failure")
	}
	if requestCount != agentAPINewsContentMaxTurns || output.RequestCount != requestCount {
		t.Fatalf("request counts = transport %d, output %d; want %d",
			requestCount, output.RequestCount, agentAPINewsContentMaxTurns)
	}
	if output.RequestTrace[0].Purpose != "final_json" ||
		output.RequestTrace[0].Status != "result_rejected" {
		t.Fatalf("first request trace = %+v", output.RequestTrace[0])
	}
}

func TestDeepSeekNewsExecutionRetriesEmptyJSONResponseInSameConversation(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "deepseek-empty-json",
		BaseURL:      "https://api.deepseek.com",
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	run, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		ProviderID: provider.ID, TriggerObjectType: "test", TriggerObjectID: "empty-json",
		Status: AgentRunStatusRunning, StartedAt: time.Now(),
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, ProviderID: provider.ID,
		TriggerObjectType: "test", TriggerObjectID: "empty-json",
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	requestCount := 0
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		body, _ := io.ReadAll(req.Body)
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode API request: %v", err)
		}
		if _, ok := request["tools"]; ok {
			t.Fatalf("DeepSeek news direct JSON request exposed tools: %s", body)
		}
		if requestCount > 1 && !strings.Contains(string(body), "The previous response did not submit") {
			t.Fatalf("retry request does not contain the same-conversation submit reminder: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","reasoning_content":"done"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":40,"total_tokens":120}}`,
			)),
		}, nil
	})}

	output, err := newAgentAPIExecutor(svc).executePrompt(
		ctx, taskID, "return JSON", "deepseek-v4-pro", AgentReasoningEffortMedium,
		time.Second, agentAPIExecutionOptions{toolNames: []string{
			mcpToolSemanticSearchNewsThreads,
			mcpToolGetNewsThread,
			codexSubmitResultTool,
		}},
	)
	if err == nil || !strings.Contains(err.Error(), `stopped with "stop"`) {
		t.Fatalf("execute error = %v; want exhausted no-submit failure", err)
	}
	if requestCount != agentAPIDeepSeekNewsMaxTurns || output.RequestCount != requestCount {
		t.Fatalf("request counts = transport %d, output %d; want %d",
			requestCount, output.RequestCount, agentAPIDeepSeekNewsMaxTurns)
	}
	if len(output.RequestTrace) != requestCount {
		t.Fatalf("request trace count = %d, want %d", len(output.RequestTrace), requestCount)
	}
	for i, trace := range output.RequestTrace {
		if trace.Sequence != i+1 || trace.Turn != i+1 || trace.Attempt != 1 {
			t.Fatalf("trace[%d] identity = %+v", i, trace)
		}
		if trace.API != "POST /chat/completions" || trace.Purpose != "empty_response" ||
			trace.Status != "completed" || trace.HTTPStatus != http.StatusOK ||
			trace.FinishReason != "stop" {
			t.Fatalf("trace[%d] response metadata = %+v", i, trace)
		}
		if trace.InputTokens != 100 || trace.CacheHitTokens != 60 ||
			trace.CacheMissTokens != 40 || trace.OutputTokens != 20 ||
			trace.TotalTokens != 120 {
			t.Fatalf("trace[%d] usage = %+v", i, trace)
		}
	}
	if output.PromptTokens != 400 || output.CachedTokens != 240 ||
		output.CacheMissTokens != 160 || output.OutputTokens != 80 {
		t.Fatalf("aggregate usage = input %d, cache hit %d, cache miss %d, output %d",
			output.PromptTokens, output.CachedTokens, output.CacheMissTokens, output.OutputTokens)
	}
}

func TestDeepSeekNewsCorrectionTurnOmitsLookupTools(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "deepseek-correct-json",
		BaseURL:      "https://api.deepseek.com",
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	run, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		ProviderID: provider.ID, TriggerObjectType: "test", TriggerObjectID: "correct-json",
		Status: AgentRunStatusRunning, StartedAt: time.Now(),
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, ProviderID: provider.ID,
		TriggerObjectType: "test", TriggerObjectID: "correct-json",
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	requestCount := 0
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		body, _ := io.ReadAll(req.Body)
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode API request: %v", err)
		}
		if _, ok := request["tools"]; ok {
			t.Fatalf("direct JSON request %d exposed tools: %s", requestCount, body)
		}
		if requestCount > 1 {
			messages, _ := request["messages"].([]any)
			encodedMessages, _ := json.Marshal(messages)
			if !strings.Contains(string(encodedMessages), "previous final JSON was rejected") {
				t.Fatalf("correction request %d lacks validation feedback: %s", requestCount, body)
			}
		}
		content := ""
		if requestCount == 1 {
			// This is a submission envelope, but the empty result fails the normal
			// MCP validation boundary and triggers an in-session correction.
			content = `{"taskType":"news_event_review","result":{}}`
		}
		response, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": content},
			}},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(string(response))),
		}, nil
	})}

	output, err := newAgentAPIExecutor(svc).executePrompt(
		ctx, taskID, "return JSON", "deepseek-v4-pro", AgentReasoningEffortMedium,
		time.Second, agentAPIExecutionOptions{toolNames: []string{
			mcpToolSemanticSearchNewsThreads,
			mcpToolGetNewsThread,
			codexSubmitResultTool,
		}},
	)
	if err == nil {
		t.Fatal("execute error = nil; want exhausted invalid submission")
	}
	if requestCount != agentAPIDeepSeekNewsMaxTurns || output.RequestCount != requestCount {
		t.Fatalf("request counts = transport %d, output %d; want %d",
			requestCount, output.RequestCount, agentAPIDeepSeekNewsMaxTurns)
	}
	if output.RequestTrace[0].Purpose != "final_json" ||
		output.RequestTrace[0].Status != "result_rejected" {
		t.Fatalf("first request trace = %+v", output.RequestTrace[0])
	}
}

func TestAgentAPIChatCompletionRecordsHTTPRetry(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	requestCount := 0
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		if requestCount == 1 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Body:       io.NopCloser(strings.NewReader(`{"error":"temporary provider failure"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"taskType\":\"news_event_review\",\"result\":{}}"}}],"usage":{"prompt_tokens":25,"completion_tokens":5,"total_tokens":30}}`,
			)),
		}, nil
	})}

	_, traces, err := newAgentAPIExecutor(svc).chatCompletion(
		context.Background(),
		"https://api.example.com",
		"test-token",
		map[string]any{"model": "test-model", "messages": []any{}},
		3,
	)
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("trace count = %d, want 2", len(traces))
	}
	if traces[0].Turn != 3 || traces[0].Attempt != 1 ||
		traces[0].Status != "retrying" || traces[0].HTTPStatus != http.StatusInternalServerError ||
		!strings.Contains(traces[0].Error, "temporary provider failure") {
		t.Fatalf("retry trace = %+v", traces[0])
	}
	if traces[1].Turn != 3 || traces[1].Attempt != 2 ||
		traces[1].Purpose != "final_json" || traces[1].Status != "completed" ||
		traces[1].InputTokens != 25 || traces[1].OutputTokens != 5 ||
		traces[1].TotalTokens != 30 {
		t.Fatalf("success trace = %+v", traces[1])
	}
}
