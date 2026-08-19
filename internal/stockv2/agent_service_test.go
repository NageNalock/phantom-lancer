package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if model.ModelType != AgentModelTypeChat {
		t.Fatalf("default model type = %q, want chat", model.ModelType)
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

	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &modelID,
	}); err != nil {
		t.Fatalf("bind model before provider delete: %v", err)
	}
	if err := svc.DeleteAgentProviderProfile(ctx, agentProviderCodexCLIDefaultID); !errors.Is(err, ErrAgentProviderProtected) {
		t.Fatalf("delete default provider error = %v, want ErrAgentProviderProtected", err)
	}
	if err := svc.DeleteAgentProviderProfile(ctx, provider.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	if _, err := svc.GetAgentProviderProfile(ctx, provider.ID); !errors.Is(err, ErrAgentProviderNotFound) {
		t.Fatalf("get deleted provider error = %v, want ErrAgentProviderNotFound", err)
	}
	if _, err := svc.GetAgentModelProfile(ctx, model.ID); !errors.Is(err, ErrAgentModelNotFound) {
		t.Fatalf("get deleted provider model error = %v, want ErrAgentModelNotFound", err)
	}
	profile, err := svc.GetAgentTaskProfileByType(ctx, AgentTaskTypeOperationReview)
	if err != nil {
		t.Fatalf("get task profile after provider delete: %v", err)
	}
	if profile.PrimaryModelID != "" {
		t.Fatalf("primary model id after provider delete = %q, want empty", profile.PrimaryModelID)
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

func TestCustomCodexCLIProviderCatalogAndTestUseResponsesProtocol(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	var modelListCalled, responsesCalled bool
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
		case "/v1/responses":
			responsesCalled = true
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), `"model":"gpt-test"`) ||
				!strings.Contains(string(body), `"input":"Reply with OK."`) {
				t.Fatalf("responses request body = %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp-test","output":[]}`)),
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
	if !responsesCalled {
		t.Fatal("Responses endpoint not called")
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

func TestAgentEmbeddingModelProfileAndTestUseOpenAIEmbeddingsProtocol(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	var embeddingsCalled bool
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Authorization"), "Bearer secret-test-token"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}
		if req.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected provider path: %s", req.URL.Path)
		}
		embeddingsCalled = true
		body, _ := io.ReadAll(req.Body)
		text := string(body)
		if !strings.Contains(text, `"model":"embed-test"`) || !strings.Contains(text, `"input"`) {
			t.Fatalf("embedding request body = %s", text)
		}
		if strings.Contains(text, `"messages"`) {
			t.Fatalf("embedding request should not contain chat messages: %s", text)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1,0.2,0.3]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`)),
		}, nil
	})}

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		BaseURL:      "https://example.test/v1",
		APIKey:       "secret-test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "embed-test",
		Enabled:             true,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("create embedding model: %v", err)
	}
	if model.ModelType != AgentModelTypeEmbedding {
		t.Fatalf("model type = %q, want embedding", model.ModelType)
	}
	if model.EmbeddingProtocol != AgentEmbeddingProtocolOpenAI {
		t.Fatalf("embedding protocol = %q, want openai embeddings", model.EmbeddingProtocol)
	}
	if model.EmbeddingDimensions != 3 {
		t.Fatalf("embedding dimensions = %d, want 3", model.EmbeddingDimensions)
	}
	if got := stringFromAny(model.Metadata["modelType"]); got != AgentModelTypeEmbedding {
		t.Fatalf("metadata modelType = %q, want embedding", got)
	}

	result, err := svc.TestAgentModel(ctx, RequestTestAgentModel{
		ProviderID:          provider.ID,
		ModelName:           "embed-test",
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("test embedding model: %v", err)
	}
	if !embeddingsCalled {
		t.Fatal("embeddings endpoint not called")
	}
	if !result.OK || result.ModelType != AgentModelTypeEmbedding || result.EmbeddingDimensions != 3 {
		t.Fatalf("embedding test result = %#v, want ok with 3 dimensions", result)
	}
	updated, err := svc.GetAgentModelProfile(ctx, model.ID)
	if err != nil {
		t.Fatalf("get embedding model: %v", err)
	}
	if updated.Status != AgentModelStatusAvailable {
		t.Fatalf("embedding model status = %q, want available", updated.Status)
	}
}

func TestAgentEmbeddingModelProfileAndTestUseVolcengineMultimodalProtocol(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	var multimodalCalled bool
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Authorization"), "Bearer secret-test-token"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}
		if req.URL.Path != "/api/v3/embeddings/multimodal" {
			t.Fatalf("unexpected provider path: %s", req.URL.Path)
		}
		multimodalCalled = true
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("embedding request body is not json: %v", err)
		}
		if payload["model"] != "doubao-embedding-vision-251215" {
			t.Fatalf("model = %#v, want doubao embedding vision", payload["model"])
		}
		input, ok := payload["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("input = %#v, want one typed part", payload["input"])
		}
		part, ok := input[0].(map[string]any)
		if !ok || part["type"] != "text" || strings.TrimSpace(fmt.Sprint(part["text"])) == "" {
			t.Fatalf("input part = %#v, want text part", input[0])
		}
		if _, ok := payload["dimensions"]; ok {
			t.Fatalf("dimensions should not be sent for volcengine multimodal: %#v", payload["dimensions"])
		}
		if _, ok := payload["encoding_format"]; ok {
			t.Fatalf("encoding_format should not be sent for volcengine multimodal: %#v", payload["encoding_format"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":{"embedding":[0.1,0.2,0.3,0.4]},"usage":{"prompt_tokens":3,"total_tokens":3}}`)),
		}, nil
	})}

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		BaseURL:      "https://ark.cn-beijing.volces.com/api/v3",
		APIKey:       "secret-test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "doubao-embedding-vision-251215",
		Enabled:             true,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingProtocol:   AgentEmbeddingProtocolVolcengineMultimodal,
		EmbeddingDimensions: 4,
	})
	if err != nil {
		t.Fatalf("create embedding model: %v", err)
	}
	if model.EmbeddingProtocol != AgentEmbeddingProtocolVolcengineMultimodal {
		t.Fatalf("embedding protocol = %q, want volcengine multimodal", model.EmbeddingProtocol)
	}

	result, err := svc.TestAgentModel(ctx, RequestTestAgentModel{
		ProviderID:          provider.ID,
		ModelName:           "doubao-embedding-vision-251215",
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingProtocol:   AgentEmbeddingProtocolVolcengineMultimodal,
		EmbeddingDimensions: 4,
	})
	if err != nil {
		t.Fatalf("test embedding model: %v", err)
	}
	if !multimodalCalled {
		t.Fatal("multimodal embeddings endpoint not called")
	}
	if !result.OK || result.ModelType != AgentModelTypeEmbedding || result.EmbeddingDimensions != 4 {
		t.Fatalf("embedding test result = %#v, want ok with 4 dimensions", result)
	}
}

func TestAgentTaskProfilesRejectEmbeddingModel(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "openai-embedding-only",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "embed-task-blocked",
		Enabled:    true,
		ModelType:  AgentModelTypeEmbedding,
	})
	if err != nil {
		t.Fatalf("create embedding model: %v", err)
	}
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &modelID,
	}); !errors.Is(err, ErrAgentModelTypeNotAllowed) {
		t.Fatalf("bind embedding model error = %v, want ErrAgentModelTypeNotAllowed", err)
	}
	if _, _, err := svc.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          AgentTaskTypeOperationReview,
		ProviderID:        provider.ID,
		ModelID:           model.ID,
		TriggerObjectType: "monitor_hit",
		TriggerObjectID:   "hit-embedding",
	}); !errors.Is(err, ErrAgentModelTypeNotAllowed) {
		t.Fatalf("create run with embedding model error = %v, want ErrAgentModelTypeNotAllowed", err)
	}

	chatModel, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "chat-bound",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create chat model: %v", err)
	}
	chatModelID := chatModel.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &chatModelID,
	}); err != nil {
		t.Fatalf("bind chat model: %v", err)
	}
	embeddingType := AgentModelTypeEmbedding
	if _, err := svc.UpdateAgentModelProfile(ctx, chatModel.ID, RequestUpdateAgentModelProfile{
		ModelType: &embeddingType,
	}); !errors.Is(err, ErrAgentModelTypeNotAllowed) {
		t.Fatalf("convert bound chat model to embedding error = %v, want ErrAgentModelTypeNotAllowed", err)
	}
}

func TestAgentDefaultCodexCLIProviderCatalogUsesCodexDebugModels(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.GetAgentProviderProfile(ctx, agentProviderCodexCLIDefaultID)
	if err != nil {
		t.Fatalf("get default codex provider: %v", err)
	}
	if provider.ProviderType != AgentProviderTypeCodexCLI || provider.Name != "default" {
		t.Fatalf("default provider = %#v", provider)
	}
	if provider.BaseURL != "" || provider.APIKeySet {
		t.Fatalf("default provider exposes runtime config: baseURL=%q apiKeySet=%v", provider.BaseURL, provider.APIKeySet)
	}

	var calls [][]string
	svc.agentCodexCommand = func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return []byte(`warning: ignored
{"models":[
  {"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list","supported_in_api":true},
  {"slug":"codex-auto-review","display_name":"Codex Auto Review","visibility":"hide","supported_in_api":true}
]}`), nil
	}

	catalog, err := svc.ListAgentProviderModels(ctx, agentProviderCodexCLIDefaultID)
	if err != nil {
		t.Fatalf("list default codex models: %v", err)
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "debug models" {
		t.Fatalf("codex calls = %#v, want debug models", calls)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].ID != "gpt-5.5" {
		t.Fatalf("catalog items = %#v", catalog.Items)
	}
	if catalog.Items[0].Source != "codex_cli" {
		t.Fatalf("catalog source = %q", catalog.Items[0].Source)
	}

	result, err := svc.TestAgentModel(ctx, RequestTestAgentModel{
		ProviderID: agentProviderCodexCLIDefaultID,
		ModelName:  "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("test default codex model: %v", err)
	}
	if !result.OK {
		t.Fatalf("test result = %#v, want ok", result)
	}
}

func TestAgentDefaultCodexCLIProviderCatalogFallsBackToBundled(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	var calls [][]string
	svc.agentCodexCommand = func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) == 2 {
			return []byte("network unavailable"), errors.New("live catalog failed")
		}
		return []byte(`{"models":[{"slug":"gpt-5.4","display_name":"GPT-5.4","visibility":"list","supported_in_api":true}]}`), nil
	}

	catalog, err := svc.ListAgentProviderModels(ctx, agentProviderCodexCLIDefaultID)
	if err != nil {
		t.Fatalf("list default codex models with bundled fallback: %v", err)
	}
	if len(calls) != 2 || strings.Join(calls[1], " ") != "debug models --bundled" {
		t.Fatalf("codex calls = %#v, want live then bundled", calls)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].ID != "gpt-5.4" || catalog.Items[0].Source != "codex_cli_bundled" {
		t.Fatalf("catalog items = %#v", catalog.Items)
	}
	provider, err := svc.GetAgentProviderProfile(ctx, agentProviderCodexCLIDefaultID)
	if err != nil {
		t.Fatalf("get default provider after probe: %v", err)
	}
	if provider.Availability != AgentProviderAvailabilityDegraded {
		t.Fatalf("availability = %q, want degraded", provider.Availability)
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
	if profile.ReasoningEffort != "" {
		t.Fatalf("seeded reasoning effort = %q, want empty", profile.ReasoningEffort)
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
	reasoningEffort := AgentReasoningEffortHigh
	updatedProfile, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID:  &primaryID,
		ReasoningEffort: &reasoningEffort,
	})
	if err != nil {
		t.Fatalf("bind primary model: %v", err)
	}
	if updatedProfile.ReasoningEffort != AgentReasoningEffortHigh {
		t.Fatalf("updated reasoning effort = %q, want high", updatedProfile.ReasoningEffort)
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
	if resolution.ReasoningEffort != AgentReasoningEffortHigh {
		t.Fatalf("resolution reasoning effort = %q, want high", resolution.ReasoningEffort)
	}
	if resolution.Run == nil {
		t.Fatal("run nil, want non-nil")
	}
	if resolution.Run.Status != AgentRunStatusReady {
		t.Fatalf("run status = %q, want ready", resolution.Run.Status)
	}
	if resolution.Run.ReasoningEffort != AgentReasoningEffortHigh {
		t.Fatalf("run reasoning effort = %q, want high", resolution.Run.ReasoningEffort)
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
	persistedRun, err := svc.GetAgentRun(ctx, resolution.Run.ID)
	if err != nil {
		t.Fatalf("get persisted run: %v", err)
	}
	if persistedRun.ReasoningEffort != AgentReasoningEffortHigh {
		t.Fatalf("persisted run reasoning effort = %q, want high", persistedRun.ReasoningEffort)
	}
	invalidEffort := "extreme"
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		ReasoningEffort: &invalidEffort,
	}); !errors.Is(err, ErrInvalidAgentReasoningEffort) {
		t.Fatalf("invalid reasoning effort error = %v, want ErrInvalidAgentReasoningEffort", err)
	}
}

func TestAgentTaskProfilesSeedTasksAndExposeConfigurableTasks(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	profiles, err := svc.ListAgentTaskProfiles(ctx, AgentTaskProfileListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list task profiles: %v", err)
	}
	seen := make(map[string]bool)
	for _, profile := range profiles {
		seen[profile.TaskType] = true
	}
	for _, taskType := range []string{
		AgentTaskTypeOperationReview,
		AgentTaskTypeStrategyGeneration,
		AgentTaskTypeOpportunityDiscovery,
		AgentTaskTypePortfolioSentinel,
		AgentTaskTypeNewsEventReview,
		AgentTaskTypeStockProfileSummary,
	} {
		if !seen[taskType] {
			t.Fatalf("seeded task %q not found in %#v", taskType, seen)
		}
		if _, err := svc.GetAgentTaskProfileByType(ctx, taskType); err != nil {
			t.Fatalf("get seeded task %q: %v", taskType, err)
		}
	}

	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStrategyGeneration, RequestUpdateAgentTaskProfile{}); err != nil {
		t.Fatalf("update strategy_generation task: %v", err)
	}
	if _, err := svc.ResolveAgentTask(ctx, AgentTaskTypeStrategyGeneration, "manual", "x", "tester"); !errors.Is(err, ErrAgentModelNotAvailable) {
		t.Fatalf("resolve unbound strategy_generation error = %v, want ErrAgentModelNotAvailable", err)
	}
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-strategy-generation",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-strategy-generation",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStrategyGeneration, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("update strategy_generation task profile: %v", err)
	}
	resolution, err := svc.ResolveAgentTask(ctx, AgentTaskTypeStrategyGeneration, "manual", "x", "tester")
	if err != nil {
		t.Fatalf("resolve strategy_generation task: %v", err)
	}
	if resolution.Status != AgentResolutionStatusAuthorized || resolution.Run == nil {
		t.Fatalf("resolution = %+v, want authorized run", resolution)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{}); err != nil {
		t.Fatalf("update opportunity_discovery task: %v", err)
	}
	if _, err := svc.ResolveAgentTask(ctx, AgentTaskTypeOpportunityDiscovery, "opportunity", "x", "tester"); !errors.Is(err, ErrAgentModelNotAvailable) {
		t.Fatalf("resolve unbound opportunity_discovery error = %v, want ErrAgentModelNotAvailable", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity_discovery task profile: %v", err)
	}
	oppResolution, err := svc.ResolveAgentTask(ctx, AgentTaskTypeOpportunityDiscovery, "opportunity", "x", "tester")
	if err != nil {
		t.Fatalf("resolve opportunity_discovery task: %v", err)
	}
	if oppResolution.Status != AgentResolutionStatusAuthorized || oppResolution.Run == nil {
		t.Fatalf("opportunity discovery resolution = %+v, want authorized run", oppResolution)
	}

	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("update opportunity_discovery task profile: %v", err)
	}
	opportunityResolution, err := svc.ResolveAgentTask(ctx, AgentTaskTypeOpportunityDiscovery, "opportunity", "x", "tester")
	if err != nil {
		t.Fatalf("resolve opportunity_discovery task: %v", err)
	}
	if opportunityResolution.Status != AgentResolutionStatusAuthorized || opportunityResolution.Run == nil {
		t.Fatalf("opportunity resolution = %+v, want authorized run", opportunityResolution)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{}); err != nil {
		t.Fatalf("update news context task: %v", err)
	}
	if _, err := svc.ResolveAgentTask(ctx, AgentTaskTypeNewsEventReview, "news_context_run", "x", "tester"); !errors.Is(err, ErrAgentModelNotAvailable) {
		t.Fatalf("resolve unbound news context task error = %v, want ErrAgentModelNotAvailable", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind news context task profile: %v", err)
	}
	newsResolution, err := svc.ResolveAgentTask(ctx, AgentTaskTypeNewsEventReview, "news_context_run", "x", "tester")
	if err != nil {
		t.Fatalf("resolve news context task: %v", err)
	}
	if newsResolution.Status != AgentResolutionStatusAuthorized || newsResolution.Run == nil {
		t.Fatalf("news context resolution = %+v, want authorized run", newsResolution)
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
	if !strings.Contains(detail.Ledger.InputArtifactSummary, "googleNewsTodayZh") || !strings.Contains(detail.Ledger.InputArtifactSummary, "googleNewsDate") {
		t.Fatalf("input artifact summary = %q, want google news debug request", detail.Ledger.InputArtifactSummary)
	}
	result := mapFromAny(detail.Ledger.StructuredOutput["result"])
	if got := sliceFromAny(result["googleNewsTodayZh"]); len(got) == 0 {
		t.Fatalf("googleNewsTodayZh = %#v, want debug news items", result["googleNewsTodayZh"])
	}
}

func TestRunAgentCLIDebugAsyncReturnsRunBeforeCompletion(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-cli-debug-async",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-debug-async",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	svc.agentExecutor = fakeDebugAgentExecutor{pool: svc.agentTaskPool}

	detail, err := svc.RunAgentCLIDebug(ctx, RequestRunAgentCLIDebug{ModelID: model.ID, Async: true})
	if err != nil {
		t.Fatalf("run cli debug async: %v", err)
	}
	if detail.Run.ID == "" || detail.Run.TriggerObjectType != "agent_cli_debug" {
		t.Fatalf("detail = %+v, want debug run", detail.Run)
	}
	if detail.Run.Status != AgentRunStatusReady && detail.Run.Status != AgentRunStatusRunning && detail.Run.Status != AgentRunStatusCompleted {
		t.Fatalf("initial run status = %q", detail.Run.Status)
	}

	var final AgentExecutionDetail
	for i := 0; i < 20; i++ {
		final, err = svc.GetAgentExecutionDetail(ctx, detail.Run.ID)
		if err != nil {
			t.Fatalf("get async detail: %v", err)
		}
		if final.Run.Status == AgentRunStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Run.Status != AgentRunStatusCompleted {
		t.Fatalf("final run status = %q, want completed", final.Run.Status)
	}
}

func TestStrategyGenerationStepRetriesTimeoutWithoutSubmission(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	executor := &retryStrategyGenerationStepExecutor{
		fakeDebugAgentExecutor: fakeDebugAgentExecutor{pool: svc.agentTaskPool},
		failStep:               StrategyGenerationStepEvidenceCollector,
		attempts:               map[string]int{},
	}
	svc.agentExecutor = executor

	_, _, err := svc.executeStrategyGenerationPipeline(ctx, AgentRun{
		ID:       "run-strategy-retry-timeout",
		TaskType: AgentTaskTypeStrategyGeneration,
	}, StrategyGenerationContext{
		Mode:  StrategyGenerationModePortfolio,
		Input: StrategyGenerationInput{Mode: StrategyGenerationModePortfolio},
	}, "gpt-retry")
	if err != nil {
		t.Fatalf("execute strategy generation pipeline: %v", err)
	}
	if got := executor.attempts[StrategyGenerationStepEvidenceCollector]; got != 2 {
		t.Fatalf("evidence attempts = %d, want 2", got)
	}
	steps, err := svc.store.ListStrategyGenerationSteps(ctx, "run-strategy-retry-timeout")
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) == 0 || steps[0].Status != StrategyGenerationStepStatusCompleted {
		t.Fatalf("first step = %+v, want completed after retry", steps)
	}
}

func TestStrategyGenerationStepDoesNotRetryNonTimeoutError(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	executor := &retryStrategyGenerationStepExecutor{
		fakeDebugAgentExecutor: fakeDebugAgentExecutor{pool: svc.agentTaskPool},
		failStep:               StrategyGenerationStepEvidenceCollector,
		nonRetry:               true,
		attempts:               map[string]int{},
	}
	svc.agentExecutor = executor

	_, _, err := svc.executeStrategyGenerationPipeline(ctx, AgentRun{
		ID:       "run-strategy-no-retry",
		TaskType: AgentTaskTypeStrategyGeneration,
	}, StrategyGenerationContext{
		Mode:  StrategyGenerationModePortfolio,
		Input: StrategyGenerationInput{Mode: StrategyGenerationModePortfolio},
	}, "gpt-no-retry")
	if err == nil {
		t.Fatal("execute strategy generation pipeline err = nil, want error")
	}
	if got := executor.attempts[StrategyGenerationStepEvidenceCollector]; got != 1 {
		t.Fatalf("evidence attempts = %d, want 1", got)
	}
	steps, err := svc.store.ListStrategyGenerationSteps(ctx, "run-strategy-no-retry")
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) == 0 || steps[0].Status != StrategyGenerationStepStatusFailed {
		t.Fatalf("first step = %+v, want failed without retry", steps)
	}
}

func TestStrategyGenerationFormatterRetriesInvalidStructure(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	executor := &retryStrategyGenerationStepExecutor{
		fakeDebugAgentExecutor: fakeDebugAgentExecutor{pool: svc.agentTaskPool},
		invalidFormatterFirst:  true,
		attempts:               map[string]int{},
	}
	svc.agentExecutor = executor

	_, _, err := svc.executeStrategyGenerationPipeline(context.Background(), AgentRun{
		ID: "run-strategy-retry-formatter", TaskType: AgentTaskTypeStrategyGeneration,
		TriggerObjectID: "portfolio_strategy_diagnosis:portfolio=test",
	}, StrategyGenerationContext{
		Mode:  StrategyGenerationModePortfolio,
		Input: StrategyGenerationInput{Mode: StrategyGenerationModePortfolio},
	}, "gpt-retry")
	if err != nil {
		t.Fatalf("execute strategy generation pipeline: %v", err)
	}
	if got := executor.attempts[StrategyGenerationStepFormatter]; got != 2 {
		t.Fatalf("formatter attempts = %d, want 2", got)
	}
	if !executor.formatterRetryHadCorrection {
		t.Fatal("formatter retry did not receive the validation correction")
	}
}

func TestFinalizeAgentRunFailsWhenReviewSaveFails(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-review-save",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-review-save",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	run, ledger, err := svc.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          AgentTaskTypeOperationReview,
		ProviderID:        provider.ID,
		ModelID:           model.ID,
		TriggerObjectType: "operation_review",
		TriggerObjectID:   "missing-review",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, run.TriggerObjectID, time.Minute)
	if _, err := svc.agentTaskPool.submitResult(taskID, AgentTaskTypeOperationReview, AgentTaskSubmittedResult{
		OutputType:    OperationReviewOutputContinueMonitoring,
		ResultSummary: "valid agent output",
		Result:        map[string]any{"reason": "review save should fail"},
		Confidence:    0.9,
	}); err != nil {
		t.Fatalf("submit result: %v", err)
	}

	svc.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, &AgentExecutorOutput{
		ExitCode:   0,
		Duration:   time.Millisecond,
		StdoutTail: "ok",
	}, nil)

	got, err := svc.GetAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != AgentRunStatusFailed || !strings.Contains(got.ErrorMessage, "save review result failed") {
		t.Fatalf("run = %+v, want failed review-save error", got)
	}
}

func TestFinalizeAgentRunFailureIncludesStderrHint(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-stderr-hint",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-stderr-hint",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	run, ledger, err := svc.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          AgentTaskTypeOperationReview,
		ProviderID:        provider.ID,
		ModelID:           model.ID,
		TriggerObjectType: "operation_review",
		TriggerObjectID:   "review-stderr-hint",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, run.TriggerObjectID, time.Minute)

	svc.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, &AgentExecutorOutput{
		Command:         "codex exec --json <prompt:42 chars>",
		Prompt:          "stock profile prompt for 300750",
		ExitCode:        2,
		Duration:        time.Millisecond,
		StderrTail:      "first line\nunknown model gpt-stderr-hint\n",
		RequestCount:    1,
		PromptTokens:    25,
		CachedTokens:    10,
		CacheMissTokens: 15,
		OutputTokens:    5,
		RequestTrace: []AgentAPIRequestTrace{{
			Sequence: 1, Turn: 1, Attempt: 1,
			API: "POST /chat/completions", Purpose: "chat_completion", Status: "failed",
			HTTPStatus: http.StatusBadGateway, DurationMS: 100, Error: "provider unavailable",
		}},
	}, errors.New("process exited (code 2) without submitting result"))

	got, err := svc.GetAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != AgentRunStatusFailed {
		t.Fatalf("run status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.ErrorMessage, "process exited (code 2)") || !strings.Contains(got.ErrorMessage, "unknown model gpt-stderr-hint") {
		t.Fatalf("error message = %q, want exit code and stderr hint", got.ErrorMessage)
	}
	requests, ok := got.CostEstimate["requests"].([]any)
	if !ok || len(requests) != 1 {
		t.Fatalf("cost estimate requests = %#v, want one durable request trace", got.CostEstimate["requests"])
	}
	if got.CostEstimate["requestCount"] != float64(1) ||
		got.CostEstimate["inputTokens"] != float64(25) ||
		got.CostEstimate["cachedTokens"] != float64(10) ||
		got.CostEstimate["cacheMissTokens"] != float64(15) ||
		got.CostEstimate["outputTokens"] != float64(5) {
		t.Fatalf("cost estimate = %#v", got.CostEstimate)
	}
	detail, err := svc.GetAgentExecutionDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Ledger == nil || !strings.Contains(detail.Ledger.OutputArtifactSummary, "stderr_tail:") {
		t.Fatalf("ledger output = %+v, want stderr tail", detail.Ledger)
	}
	if !strings.Contains(detail.Ledger.OutputArtifactSummary, "command:\ncodex exec --json <prompt:42 chars>") {
		t.Fatalf("ledger output = %q, want command summary", detail.Ledger.OutputArtifactSummary)
	}
	if !strings.Contains(detail.Ledger.Prompt, "stock profile prompt for 300750") {
		t.Fatalf("ledger prompt = %q, want executor prompt", detail.Ledger.Prompt)
	}
}

func TestAgentRunFailureMessagePrefersCodexJSONError(t *testing.T) {
	output := &AgentExecutorOutput{
		StdoutTail: strings.Join([]string{
			`{"type":"thread.started","thread_id":"test"}`,
			`{"type":"error","message":"You've hit your usage limit. Try again later."}`,
			`{"type":"turn.failed","error":{"message":"You've hit your usage limit. Try again later."}}`,
		}, "\n"),
		StderrTail: "Reading additional input from stdin...\n",
	}
	got := agentRunFailureMessage("process exited (code 1) without submitting result", output)
	if !strings.Contains(got, "usage limit") {
		t.Fatalf("failure message = %q, want provider usage limit", got)
	}
	if strings.Contains(got, "additional input") {
		t.Fatalf("failure message = %q, must not prefer incidental stderr", got)
	}
}

func TestAgentRunFailureMessageDropsCodexStdinNoticeWithoutProviderError(t *testing.T) {
	const base = "no result submitted: output_last_message: invalid character 'A'"
	got := agentRunFailureMessage(base, &AgentExecutorOutput{
		StderrTail: "Reading additional input from stdin...\n",
	})
	if got != base {
		t.Fatalf("failure message = %q, want %q", got, base)
	}
}

type fakeDebugAgentExecutor struct {
	pool *agentTaskPool
}

type retryStrategyGenerationStepExecutor struct {
	fakeDebugAgentExecutor
	failStep                    string
	nonRetry                    bool
	invalidFormatterFirst       bool
	formatterRetryHadCorrection bool
	attempts                    map[string]int
}

func (f fakeDebugAgentExecutor) ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := map[string]any{"debug": true, "model": modelName, "hitTitle": pack.Hit.Title}
	if pack.Hit.TaskType == "agent_cli_debug" {
		result["googleNewsSearchStatus"] = "ok"
		result["googleNewsTodayZh"] = []any{
			map[string]any{
				"title":       "今日谷歌新闻调试样例",
				"source":      "Google News",
				"publishedAt": stringFromAny(pack.Evidence["googleNewsDate"]),
				"url":         "https://news.google.com/",
				"summaryZh":   "用于验证 debug CLI 回填字段是否展示,真实运行时由 Codex 搜索工具填充。",
			},
		}
		result["searchAudit"] = map[string]any{
			"toolUsed":  "fake",
			"query":     "Google News today",
			"checkedAt": time.Now().Format(time.RFC3339),
		}
	}
	_, err := f.pool.submitResult(taskID, AgentTaskTypeOperationReview, AgentTaskSubmittedResult{
		OutputType:    OperationReviewOutputContinueMonitoring,
		ResultSummary: "debug ok",
		Result:        result,
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

func (f fakeDebugAgentExecutor) ExecuteStrategyGeneration(ctx context.Context, taskID string, pack StrategyGenerationContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	mode := firstNonEmpty(pack.Input.Mode, pack.Mode, StrategyGenerationModePortfolio)
	_, err := f.pool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStrategyGeneration,
		ResultSummary: "strategy generation ok",
		Result: map[string]any{
			"schema_version": StrategyGenerationReportSchemaVersion,
			"run_summary":    map[string]any{"mode": mode},
			"drafts":         []any{},
		},
		Confidence: 1,
	})
	if err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{
		StdoutTail:    "strategy generation stdout",
		ExitCode:      0,
		Duration:      time.Millisecond,
		RawTranscript: "strategy generation stdout",
	}, nil
}

func (f fakeDebugAgentExecutor) ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	result := map[string]any{
		"schema_version": StrategyGenerationStepOutputSchema,
		"step_key":       pack.StepKey,
		"role":           pack.Role,
		"summary":        "step ok",
	}
	if pack.StepKey == StrategyGenerationStepFormatter {
		mode := firstNonEmpty(pack.Context.Input.Mode, pack.Context.Mode, StrategyGenerationModePortfolio)
		result = map[string]any{
			"schema_version": StrategyGenerationReportSchemaVersion,
			"run_summary":    map[string]any{"mode": mode},
			"drafts":         []any{},
		}
	}
	_, err := f.pool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStrategyGeneration,
		ResultSummary: "strategy generation step ok",
		Result:        result,
		Confidence:    1,
	})
	if err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{
		StdoutTail:    "strategy generation step stdout",
		ExitCode:      0,
		Duration:      time.Millisecond,
		RawTranscript: "strategy generation step stdout",
	}, nil
}

func (f *retryStrategyGenerationStepExecutor) ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	f.attempts[pack.StepKey]++
	if pack.StepKey == StrategyGenerationStepFormatter && f.attempts[pack.StepKey] > 1 {
		for _, instruction := range pack.Instructions {
			if strings.Contains(instruction, "CORRECTIVE FORMATTER RETRY") {
				f.formatterRetryHadCorrection = true
			}
		}
	}
	if pack.StepKey == f.failStep && f.attempts[pack.StepKey] == 1 {
		if f.nonRetry {
			return &AgentExecutorOutput{ExitCode: 2, Duration: time.Second, StderrTail: "invalid model"}, errors.New("process exited (code 2) without submitting result")
		}
		return &AgentExecutorOutput{TimedOut: true, ExitCode: -1, Duration: execDefaultTimeout, StderrTail: "Reading additional input from stdin..."}, fmt.Errorf("execution timed out after %s, no result submitted", execDefaultTimeout)
	}
	if pack.StepKey == StrategyGenerationStepFormatter && f.invalidFormatterFirst && f.attempts[pack.StepKey] == 1 {
		_, err := f.pool.submitResult(taskID, AgentTaskTypeStrategyGeneration, AgentTaskSubmittedResult{
			OutputType: StrategyGenerationOutputType, ResultSummary: "invalid formatter result", Confidence: 0.7,
			Result: map[string]any{
				"schema_version": StrategyGenerationReportSchemaVersion,
				"run_summary":    map[string]any{"mode": StrategyGenerationModePortfolio},
				"drafts": []any{map[string]any{
					"symbol": "002837", "draft_type": StrategyGenerationDraftTypeNewStrategy,
					"thesis": "test", "confidence": 0.7, "horizon_outlooks": []any{},
				}},
			},
		})
		return &AgentExecutorOutput{ExitCode: 0, Duration: time.Millisecond}, err
	}
	return f.fakeDebugAgentExecutor.ExecuteStrategyGenerationStep(ctx, taskID, pack, modelName, reasoningEffort)
}

func (f fakeDebugAgentExecutor) ExecuteOpportunityDiscovery(ctx context.Context, taskID string, pack OpportunityDiscoveryContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	_, err := f.pool.submitResult(taskID, AgentTaskTypeOpportunityDiscovery, AgentTaskSubmittedResult{
		OutputType:    OpportunityDiscoveryOutputType,
		ResultSummary: "opportunity discovery ok",
		Result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": pack.Opportunity.ID,
			"summary":        "debug opportunity discovery ok",
			"candidates":     []any{},
		},
		Confidence: 1,
	})
	if err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{
		StdoutTail:    "opportunity discovery stdout",
		ExitCode:      0,
		Duration:      time.Millisecond,
		RawTranscript: "opportunity discovery stdout",
	}, nil
}

func (f fakeDebugAgentExecutor) ExecutePortfolioSentinel(ctx context.Context, taskID string, pack PortfolioSentinelContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	_, err := f.pool.submitResult(taskID, AgentTaskTypePortfolioSentinel, AgentTaskSubmittedResult{
		OutputType:    PortfolioSentinelOutputType,
		ResultSummary: "portfolio sentinel ok",
		Result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "debug portfolio sentinel ok",
			"portfolio_actions":  []any{},
			"affected_holdings":  []any{},
		},
		Confidence: 1,
	})
	if err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{
		StdoutTail:    "portfolio sentinel stdout",
		ExitCode:      0,
		Duration:      time.Millisecond,
		RawTranscript: "portfolio sentinel stdout",
	}, nil
}

func (f fakeDebugAgentExecutor) ExecuteStockProfileSummary(ctx context.Context, taskID string, profile StockProfile, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	_, err := f.pool.submitResult(taskID, AgentTaskTypeStockProfileSummary, AgentTaskSubmittedResult{
		OutputType:    AgentTaskTypeStockProfileSummary,
		ResultSummary: "profile ok",
		Result:        map[string]any{"summaryZh": profile.BusinessSummary, "summaryEn": profile.Name},
		Confidence:    1,
	})
	if err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{
		StdoutTail:    "profile stdout",
		ExitCode:      0,
		Duration:      time.Millisecond,
		RawTranscript: "profile stdout",
	}, nil
}
