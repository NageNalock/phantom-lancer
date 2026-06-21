package stockv2

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAgentProviderAndModelProfileCRUD 验收 1:Provider/Model profile 增改读。
func TestAgentProviderAndModelProfileCRUD(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	// provider 创建:留空枚举走默认值。
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "openai-default",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if provider.ID == "" {
		t.Fatal("provider id empty")
	}
	if provider.ConfigState != AgentProviderConfigStateConfigured {
		t.Fatalf("default configState = %q, want configured", provider.ConfigState)
	}
	if provider.AuthState != AgentProviderAuthStateUnknown {
		t.Fatalf("default authState = %q, want unknown", provider.AuthState)
	}
	if provider.Availability != AgentProviderAvailabilityUnknown {
		t.Fatalf("default availability = %q, want unknown", provider.Availability)
	}

	// provider 更新:patch name/availability。
	newName := "openai-prod"
	newAvail := AgentProviderAvailabilityAvailable
	updated, err := svc.UpdateAgentProviderProfile(ctx, provider.ID, RequestUpdateAgentProviderProfile{
		Name:         &newName,
		Availability: &newAvail,
	})
	if err != nil {
		t.Fatalf("update provider: %v", err)
	}
	if updated.Name != newName {
		t.Fatalf("name = %q, want %q", updated.Name, newName)
	}
	if updated.Availability != newAvail {
		t.Fatalf("availability = %q, want %q", updated.Availability, newAvail)
	}
	got, err := svc.GetAgentProviderProfile(ctx, provider.ID)
	if err != nil {
		t.Fatalf("get provider: %v", err)
	}
	if got.Name != newName || got.Availability != newAvail {
		t.Fatalf("read-back provider mismatch: name=%q avail=%q", got.Name, got.Availability)
	}

	// model 创建:绑定 provider,enabled 默认开,status/costLevel 走默认。
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-test",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if model.Status != AgentModelStatusAvailable {
		t.Fatalf("default model status = %q, want available", model.Status)
	}
	if model.CostLevel != AgentModelCostLevelMedium {
		t.Fatalf("default cost level = %q, want medium", model.CostLevel)
	}

	// model 更新。
	highCost := AgentModelCostLevelHigh
	updatedModel, err := svc.UpdateAgentModelProfile(ctx, model.ID, RequestUpdateAgentModelProfile{
		CostLevel: &highCost,
	})
	if err != nil {
		t.Fatalf("update model: %v", err)
	}
	if updatedModel.CostLevel != highCost {
		t.Fatalf("costLevel = %q, want %q", updatedModel.CostLevel, highCost)
	}

	// model 绑定到不存在的 provider 应失败。
	if _, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: "no-such-provider",
		ModelName:  "orphan",
		Enabled:    true,
	}); err == nil {
		t.Fatal("create model with missing provider: want error, got nil")
	}
}

func TestAgentProviderOpenAICompatibleRuntimeConfig(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		DisplayName:  "OpenAI Compatible",
		BaseURL:      "https://example.test/v1",
		APIKey:       "secret-test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if provider.Name == "" {
		t.Fatal("auto-generated provider name is empty")
	}
	if provider.BaseURL != "https://example.test/v1" {
		t.Fatalf("baseURL = %q, want configured endpoint", provider.BaseURL)
	}
	if !provider.APIKeySet {
		t.Fatal("APIKeySet = false, want true")
	}
	if provider.Metadata != nil {
		t.Fatalf("public provider metadata = %#v, want nil", provider.Metadata)
	}
	raw, err := svc.store.GetAgentProviderProfile(ctx, provider.ID)
	if err != nil {
		t.Fatalf("get raw provider: %v", err)
	}
	if got := agentProviderAPIKey(raw); got != "secret-test-token" {
		t.Fatalf("stored api key mismatch: %q", got)
	}
}

func TestAgentProviderModelCatalogAndTestUseOpenAICompatibleProtocol(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	var modelListCalled, chatCalled bool
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Authorization"), "Bearer secret-test-token"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}
		switch req.URL.Path {
		case "/v1/models":
			modelListCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-test"},{"id":"gpt-mini"}]}`)),
			}, nil
		case "/v1/chat/completions":
			chatCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test","choices":[{"message":{"content":"ok"}}]}`)),
			}, nil
		default:
			t.Fatalf("unexpected provider path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		BaseURL:      "https://example.test/v1",
		APIKey:       "secret-test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	catalog, err := svc.ListAgentProviderModels(ctx, provider.ID)
	if err != nil {
		t.Fatalf("list provider models: %v", err)
	}
	if !modelListCalled {
		t.Fatal("model list endpoint not called")
	}
	if len(catalog.Items) != 2 || catalog.Items[0].ID != "gpt-test" {
		t.Fatalf("catalog items = %#v", catalog.Items)
	}

	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-test",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	unavailable := AgentModelStatusUnavailable
	if _, err := svc.UpdateAgentModelProfile(ctx, model.ID, RequestUpdateAgentModelProfile{Status: &unavailable}); err != nil {
		t.Fatalf("make model unavailable: %v", err)
	}
	result, err := svc.TestAgentModel(ctx, RequestTestAgentModel{
		ProviderID: provider.ID,
		ModelName:  "gpt-test",
	})
	if err != nil {
		t.Fatalf("test model: %v", err)
	}
	if !chatCalled {
		t.Fatal("chat completions endpoint not called")
	}
	if !result.OK {
		t.Fatalf("test result = %#v, want ok", result)
	}
	updated, err := svc.GetAgentModelProfile(ctx, model.ID)
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	if updated.Status != AgentModelStatusAvailable {
		t.Fatalf("model status = %q, want available", updated.Status)
	}
}

// TestResolveAgentTaskOperationReviewDefaultModel 验收 2:
// operation_review 默认种入 + 绑定可用 primary model → Resolve 返回 authorized run。
func TestResolveAgentTaskOperationReviewDefaultModel(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	// 默认 task profile 已由 schema 种入。
	profile, err := svc.GetAgentTaskProfileByType(ctx, AgentTaskTypeOperationReview)
	if err != nil {
		t.Fatalf("get seeded task profile: %v", err)
	}
	if profile.TaskType != AgentTaskTypeOperationReview {
		t.Fatalf("task type = %q, want operation_review", profile.TaskType)
	}

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "openai-resolve",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-resolve",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}

	primaryID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &primaryID,
	}); err != nil {
		t.Fatalf("bind primary model: %v", err)
	}

	resolution, err := svc.ResolveAgentTask(ctx, AgentTaskTypeOperationReview, "monitor_hit", "hit-1", "tester")
	if err != nil {
		t.Fatalf("resolve agent task: %v", err)
	}
	if resolution.Status != AgentResolutionStatusAuthorized {
		t.Fatalf("status = %q, want authorized", resolution.Status)
	}
	if resolution.ModelID != model.ID {
		t.Fatalf("modelId = %q, want %q", resolution.ModelID, model.ID)
	}
	if resolution.Run == nil {
		t.Fatal("run nil, want non-nil")
	}
	if resolution.Run.Status != AgentRunStatusReady {
		t.Fatalf("run status = %q, want ready", resolution.Run.Status)
	}
	if resolution.Run.Output != "" {
		t.Fatalf("run output = %q, want empty (no fake conclusion this round)", resolution.Run.Output)
	}
	if resolution.Run.DecisionLedgerID == "" {
		t.Fatal("run decisionLedgerId empty")
	}
	if resolution.DecisionLedger == nil {
		t.Fatal("decisionLedger nil, want non-nil")
	}

	// ledger 持久化可读回,且本轮不写假结构化输出。
	ledger, err := svc.GetAgentDecisionLedger(ctx, resolution.Run.DecisionLedgerID)
	if err != nil {
		t.Fatalf("get decision ledger: %v", err)
	}
	if ledger.ID != resolution.DecisionLedger.ID {
		t.Fatalf("ledger id mismatch: %q vs %q", ledger.ID, resolution.DecisionLedger.ID)
	}
	if len(ledger.StructuredOutput) != 0 {
		t.Fatalf("structuredOutput = %v, want empty (no fake output)", ledger.StructuredOutput)
	}
}

// TestCreateAgentRunRecordRedactsSecrets 验收 4:
// DecisionLedger 写入时脱敏,secret 明文不残留。
func TestCreateAgentRunRecordRedactsSecrets(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "openai-redact",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-redact",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}

	secretInput := "review hit hit-3\nAuthorization: Bearer sk-live-abcdef\napi_key=supersecret"
	secretPrompt := "请基于以下信息复核: Authorization: Bearer sk-live-abcdef, api_key=supersecret"

	run, ledger, err := svc.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          AgentTaskTypeOperationReview,
		ProviderID:        provider.ID,
		ModelID:           model.ID,
		TriggerObjectType: "monitor_hit",
		TriggerObjectID:   "hit-3",
		RequestedBy:       "tester",
		InputSummary:      secretInput,
		Prompt:            secretPrompt,
	})
	if err != nil {
		t.Fatalf("create agent run record: %v", err)
	}
	if run.Output != "" {
		t.Fatalf("run output = %q, want empty (no fake conclusion)", run.Output)
	}

	// 脱敏断言:明文 secret 不残留,且出现 [redacted]。
	combined := ledger.InputSummary + "\n" + ledger.Prompt
	if strings.Contains(combined, "sk-live-abcdef") {
		t.Fatalf("inputSummary/prompt still contains bearer secret: %q", combined)
	}
	if strings.Contains(combined, "supersecret") {
		t.Fatalf("inputSummary/prompt still contains api_key secret: %q", combined)
	}
	if !strings.Contains(combined, "[redacted]") {
		t.Fatalf("inputSummary/prompt missing [redacted] marker: %q", combined)
	}

	// 持久化读回与返回一致,且同样不含明文。
	got, err := svc.GetAgentDecisionLedger(ctx, ledger.ID)
	if err != nil {
		t.Fatalf("get decision ledger: %v", err)
	}
	persisted := got.InputSummary + "\n" + got.Prompt
	if strings.Contains(persisted, "sk-live-abcdef") || strings.Contains(persisted, "supersecret") {
		t.Fatalf("persisted ledger contains plaintext secret: %q", persisted)
	}
	if redacted, _ := got.RedactionSummary["inputSummaryRedacted"].(bool); !redacted {
		t.Fatalf("redactionSummary.inputSummaryRedacted = %v, want true", got.RedactionSummary["inputSummaryRedacted"])
	}
}

func TestRunAgentCLIDebugPersistsOutputAndSubmittedResult(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-cli-debug",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-debug",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	svc.agentExecutor = fakeDebugAgentExecutor{pool: svc.agentTaskPool}

	detail, err := svc.RunAgentCLIDebug(ctx, RequestRunAgentCLIDebug{ModelID: model.ID})
	if err != nil {
		t.Fatalf("run cli debug: %v", err)
	}
	if detail.Run.Status != AgentRunStatusCompleted {
		t.Fatalf("run status = %q, want completed", detail.Run.Status)
	}
	if detail.Run.TriggerObjectType != "agent_cli_debug" {
		t.Fatalf("trigger object type = %q", detail.Run.TriggerObjectType)
	}
	if detail.Ledger == nil {
		t.Fatal("ledger nil")
	}
	if !strings.Contains(detail.Ledger.OutputArtifactSummary, "debug stdout") {
		t.Fatalf("output artifact summary = %q, want stdout tail", detail.Ledger.OutputArtifactSummary)
	}
	if got := detail.Ledger.StructuredOutput["outputType"]; got != OperationReviewOutputContinueMonitoring {
		t.Fatalf("structured output type = %v", got)
	}
}

type fakeDebugAgentExecutor struct {
	pool *agentTaskPool
}

func (f fakeDebugAgentExecutor) ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName string) (*AgentExecutorOutput, error) {
	_, err := f.pool.submitResult(taskID, AgentTaskTypeOperationReview, AgentTaskSubmittedResult{
		OutputType:    OperationReviewOutputContinueMonitoring,
		ResultSummary: "debug ok",
		Result:        map[string]any{"debug": true, "model": modelName, "hitTitle": pack.Hit.Title},
		Confidence:    1,
	})
	if err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{
		StdoutTail:    "debug stdout",
		StderrTail:    "",
		ExitCode:      0,
		TimedOut:      false,
		Duration:      time.Millisecond,
		RawTranscript: "debug stdout",
	}, nil
}
