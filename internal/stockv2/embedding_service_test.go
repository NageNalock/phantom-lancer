package stockv2

import (
	"context"
	"errors"
	"net/http"
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
	if _, err := svc.SemanticSearch(ctx, EmbeddingObjectStockProfile, "AI model", 5); !errors.Is(err, ErrEmbeddingModelNotConfigured) {
		t.Fatalf("semantic search without model error=%v, want ErrEmbeddingModelNotConfigured", err)
	}

	provider := seedEmbeddingProvider(t, svc, ctx)
	chatModel, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "chat-model",
		Enabled:    true,
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

func TestEmbeddingRebuildAndSemanticSearchUseBoundEmbeddingModel(t *testing.T) {
	store := newTestStore(t)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/v1/embeddings" {
			t.Fatalf("embedding path = %s, want /v1/embeddings", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization header not set")
		}
		return stringResponse(http.StatusOK, `{"data":[{"embedding":[1,0,0]}]}`), nil
	})}
	svc := NewService(store, nil, client)
	defer svc.Close()
	ctx := context.Background()

	provider := seedEmbeddingProvider(t, svc, ctx)
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "embedding-model",
		Enabled:             true,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("create embedding model: %v", err)
	}
	modelID := model.ID
	enabled := true
	status, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID: &modelID,
		Enabled:          &enabled,
	})
	if err != nil {
		t.Fatalf("update embedding config: %v", err)
	}
	if status.Code != EmbeddingStatusAssetNotReady {
		t.Fatalf("status=%+v, want asset_not_ready before rebuild", status)
	}
	if _, err := svc.SemanticSearch(ctx, EmbeddingObjectStockProfile, "AI model", 5); !errors.Is(err, ErrEmbeddingAssetNotReady) {
		t.Fatalf("semantic search before assets error=%v, want ErrEmbeddingAssetNotReady", err)
	}

	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:          "300750",
		Market:          "SZ",
		InstrumentType:  InstrumentTypeStock,
		Name:            "宁德时代",
		Industry:        "电力设备",
		Concepts:        []string{"AI data center power", "energy storage"},
		BusinessSummary: "battery and storage supplier",
		ProfileText:     "AI data center power chain candidate",
	}); err != nil {
		t.Fatalf("upsert stock profile: %v", err)
	}
	rebuild, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{
		ObjectTypes: []string{EmbeddingObjectStockProfile},
		Force:       true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("rebuild embedding assets: %v", err)
	}
	if rebuild.Succeeded != 1 || rebuild.Failed != 0 || calls != 1 {
		t.Fatalf("rebuild=%+v calls=%d, want one generated asset", rebuild, calls)
	}
	hits, err := svc.SemanticSearch(ctx, EmbeddingObjectStockProfile, "AI power", 5)
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(hits) != 1 || hits[0].Asset.ObjectID != "300750" || hits[0].Profile == nil {
		t.Fatalf("hits=%+v, want stock profile hit", hits)
	}
	if calls != 2 {
		t.Fatalf("embedding calls=%d, want rebuild + query", calls)
	}
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
