package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEmbeddingEntrypointsRequireConfiguredAvailableEmbeddingModel(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()

	status, err := svc.GetEmbeddingStatus(ctx)
	if err != nil {
		t.Fatalf("get embedding status: %v", err)
	}
	if status.Ready || status.Code != EmbeddingStatusModelNotConfigured {
		t.Fatalf("status=%+v, want model_not_configured", status)
	}
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{}); !errors.Is(err, ErrEmbeddingModelNotConfigured) {
		t.Fatalf("rebuild without config error=%v, want ErrEmbeddingModelNotConfigured", err)
	}
	if _, err := svc.SemanticSearch(ctx, EmbeddingObjectStockProfile, "AI model", 5); !errors.Is(err, ErrEmbeddingModelNotConfigured) {
		t.Fatalf("semantic search without model error=%v, want ErrEmbeddingModelNotConfigured", err)
	}
	if _, err := svc.SemanticSearchStockProfiles(ctx, SemanticSearchRequest{Query: "AI"}); !errors.Is(err, ErrEmbeddingModelNotConfigured) {
		t.Fatalf("semantic stock search without config error=%v, want ErrEmbeddingModelNotConfigured", err)
	}

	provider := seedEmbeddingProvider(t, svc, ctx)
	chatModel, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "chat-model",
		Enabled:    true,
		Status:     AgentModelStatusAvailable,
		ModelType:  AgentModelTypeChat,
	})
	if err != nil {
		t.Fatalf("create chat model: %v", err)
	}
	enabled := true
	chatModelID := chatModel.ID
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID: &chatModelID,
		Enabled:          &enabled,
	}); !errors.Is(err, ErrEmbeddingModelInvalid) {
		t.Fatalf("bind chat model error=%v, want ErrEmbeddingModelInvalid", err)
	}

	disabledModel, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "disabled-embedding",
		Enabled:             false,
		Status:              AgentModelStatusAvailable,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("create disabled embedding model: %v", err)
	}
	disabledModelID := disabledModel.ID
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID: &disabledModelID,
		Enabled:          &enabled,
	}); !errors.Is(err, ErrEmbeddingModelUnavailable) {
		t.Fatalf("bind disabled embedding model error=%v, want ErrEmbeddingModelUnavailable", err)
	}

	zeroDimModel, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "zero-dim-embedding",
		Enabled:             true,
		Status:              AgentModelStatusAvailable,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 0,
	})
	if err != nil {
		t.Fatalf("create zero dim embedding model: %v", err)
	}
	zeroDimModelID := zeroDimModel.ID
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID: &zeroDimModelID,
		Enabled:          &enabled,
	}); !errors.Is(err, ErrEmbeddingDimensionsMismatch) {
		t.Fatalf("bind zero-dim embedding model error=%v, want ErrEmbeddingDimensionsMismatch", err)
	}
}

func TestEmbeddingRebuildAndSemanticSearchUseRealVectors(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-v1")

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池 储能 新能源车")
	upsertEmbeddingTestProfile(t, svc, "600519", "贵州茅台", "白酒 高端消费")

	result, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}})
	if err != nil {
		t.Fatalf("rebuild embeddings: %v", err)
	}
	if result.Succeeded != 2 || result.Success != 2 || result.Failed != 0 {
		t.Fatalf("rebuild result=%#v, want 2 success", result)
	}

	items, err := svc.SemanticSearchStockProfiles(ctx, SemanticSearchRequest{Query: "动力电池机会", Limit: 1})
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(items) != 1 || items[0].Profile.Symbol != "300750" {
		t.Fatalf("semantic results=%#v, want 300750 first", items)
	}
	if items[0].Asset.ModelID == "" ||
		items[0].Asset.ProviderID == "" ||
		items[0].Asset.EmbeddingDimensions != 3 ||
		items[0].Asset.TextHash == "" ||
		items[0].Asset.VectorRef == "" {
		t.Fatalf("embedding asset metadata incomplete: %#v", items[0].Asset)
	}
}

func TestEmbeddingModelSwitchMarksOldVectorsStale(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model1 := configureEmbeddingModel(t, svc, "embed-v1")
	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}}); err != nil {
		t.Fatalf("rebuild v1: %v", err)
	}

	model2, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          model1.ProviderID,
		ModelName:           "embed-v2",
		Enabled:             true,
		Status:              AgentModelStatusAvailable,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("create model2: %v", err)
	}
	enabled := true
	model2ID := model2.ID
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{EmbeddingModelID: &model2ID, Enabled: &enabled}); err != nil {
		t.Fatalf("switch embedding config: %v", err)
	}
	status, err := svc.GetEmbeddingStatus(ctx)
	if err != nil {
		t.Fatalf("embedding status: %v", err)
	}
	if status.Code != EmbeddingStatusAssetNotReady || status.StaleAssetCount == 0 {
		t.Fatalf("status=%+v, want asset_not_ready with stale old vectors", status)
	}
	if _, err := svc.SemanticSearchStockProfiles(ctx, SemanticSearchRequest{Query: "动力电池"}); !errors.Is(err, ErrEmbeddingAssetNotReady) {
		t.Fatalf("semantic search after model switch error=%v, want ErrEmbeddingAssetNotReady", err)
	}
}

func TestEmbeddingMCPResponsesDoNotExposeSecretsOrSensitiveURLQuery(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-v1")

	event, err := svc.store.CreateNewsEvent(ctx, NewsEvent{
		Source:  "example",
		Title:   "AI model released",
		Summary: "Public summary",
		URL:     "https://example.test/news?id=1&token=secret-query-token",
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	if event.ID == "" {
		t.Fatal("event id empty")
	}

	statusResp := svc.AgentTaskPool().HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "stock_agent.get_embedding_status",
			"arguments": map[string]any{},
		},
	}))
	newsResp := svc.AgentTaskPool().HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stock_agent.search_news_events",
			"arguments": map[string]any{
				"query": "AI",
				"limit": 5,
			},
		},
	}))
	combined := string(statusResp) + string(newsResp)
	if strings.Contains(combined, "secret-test-token") || strings.Contains(combined, "secret-query-token") {
		t.Fatalf("MCP response leaked secret data: %s", combined)
	}
	if strings.Contains(combined, "apiKey") || strings.Contains(combined, "cookie") || strings.Contains(combined, ".duckdb") {
		t.Fatalf("MCP response exposed sensitive field/path: %s", combined)
	}
	if strings.Contains(combined, "?id=1") || strings.Contains(combined, "token=") {
		t.Fatalf("MCP response did not strip URL query: %s", combined)
	}
}

func newEmbeddingTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	svc, cleanup := newStrategyTestService(t)
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Authorization"), "Bearer secret-test-token"; got != want {
			t.Fatalf("authorization header=%q, want %q", got, want)
		}
		if req.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected embedding path: %s", req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("embedding request is not json: %v", err)
		}
		input := strings.TrimSpace(stringFromAny(payload["input"]))
		vector := []float64{0, 0, 1}
		if strings.Contains(input, "电池") || strings.Contains(strings.ToLower(input), "battery") {
			vector = []float64{1, 0, 0}
		}
		if strings.Contains(input, "白酒") || strings.Contains(strings.ToLower(input), "liquor") {
			vector = []float64{0, 1, 0}
		}
		raw, _ := json.Marshal(map[string]any{"data": []map[string]any{{"embedding": vector}}})
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(string(raw))),
		}, nil
	})}
	return svc, cleanup
}

func configureEmbeddingModel(t *testing.T, svc *Service, modelName string) AgentModelProfile {
	t.Helper()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "embedding-provider",
		BaseURL:      "https://example.test/v1",
		APIKey:       "secret-test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          provider.ID,
		ModelName:           modelName,
		Enabled:             true,
		Status:              AgentModelStatusAvailable,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("create embedding model: %v", err)
	}
	modelID := model.ID
	enabled := true
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID: &modelID,
		Enabled:          &enabled,
	}); err != nil {
		t.Fatalf("bind embedding model: %v", err)
	}
	return model
}

func seedEmbeddingProvider(t *testing.T, svc *Service, ctx context.Context) AgentProviderProfile {
	t.Helper()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "embedding-provider",
		BaseURL:      "https://embedding.test/v1",
		APIKey:       "test-key",
	})
	if err != nil {
		t.Fatalf("create embedding provider: %v", err)
	}
	return provider
}

func upsertEmbeddingTestProfile(t *testing.T, svc *Service, symbol, name, text string) {
	t.Helper()
	_, err := svc.store.UpsertStockProfile(context.Background(), StockProfile{
		Symbol:         symbol,
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           name,
		ProfileText:    text,
		Concepts:       []string{text},
	})
	if err != nil {
		t.Fatalf("upsert stock profile %s: %v", symbol, err)
	}
}
