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
	if status, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID: &zeroDimModelID,
		Enabled:          &enabled,
	}); err != nil || status.Code != EmbeddingStatusAssetNotReady {
		t.Fatalf("bind zero-dim embedding model status=%+v error=%v, want asset_not_ready", status, err)
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

func TestEmbeddingRebuildDoesNotSelectUnchangedOptionalDimensionAssets(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModelWithDimensions(t, svc, "embed-dynamic", 0)

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	first, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}})
	if err != nil {
		t.Fatalf("first rebuild embeddings: %v", err)
	}
	if first.Success != 1 || first.Skipped != 0 {
		t.Fatalf("first rebuild result=%#v, want 1 success", first)
	}
	second, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}})
	if err != nil {
		t.Fatalf("second rebuild embeddings: %v", err)
	}
	if second.Total != 0 || second.Success != 0 || second.Skipped != 0 {
		t.Fatalf("second rebuild result=%#v, want unchanged ready asset not selected", second)
	}
}

func TestEmbeddingMaintenanceScansPastReadyUnchangedAssets(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	for i := 0; i < 205; i++ {
		profile := StockProfile{
			Symbol:         fmt.Sprintf("T%03d", i),
			Market:         "SZ",
			InstrumentType: InstrumentTypeStock,
			Name:           fmt.Sprintf("测试标的%03d", i),
			ProfileText:    fmt.Sprintf("测试画像 %03d", i),
		}
		if _, err := svc.store.UpsertStockProfile(ctx, profile); err != nil {
			t.Fatalf("upsert profile %s: %v", profile.Symbol, err)
		}
	}
	profiles, err := svc.store.ListStockProfiles(ctx, StockProfileListFilter{Limit: 205})
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(profiles) < 201 {
		t.Fatalf("profiles=%d, want at least 201", len(profiles))
	}
	missingSymbol := profiles[200].Symbol
	for i := 0; i < 200; i++ {
		profile := profiles[i]
		if _, err := svc.store.UpsertEmbeddingAsset(ctx, EmbeddingAsset{
			ObjectType:          EmbeddingObjectStockProfile,
			ObjectID:            profile.Symbol,
			TextHash:            hashEmbeddingText(stockProfileEmbeddingText(profile)),
			ModelID:             model.ID,
			ProviderID:          model.ProviderID,
			EmbeddingProtocol:   model.EmbeddingProtocol,
			EmbeddingDimensions: 3,
			VectorRef:           "ready-" + profile.Symbol,
			Status:              EmbeddingAssetStatusReady,
		}); err != nil {
			t.Fatalf("upsert ready asset %s: %v", profile.Symbol, err)
		}
	}

	result, err := svc.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{
		ObjectTypes: []string{EmbeddingObjectStockProfile},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("maintenance batch: %v", err)
	}
	if result.Total != 1 || result.Success != 1 || result.Skipped != 0 {
		t.Fatalf("result=%#v, want one missing asset processed after ready unchanged head", result)
	}
	if _, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectStockProfile, missingSymbol, model.ID); err != nil {
		t.Fatalf("missing profile %s was not embedded: %v", missingSymbol, err)
	}
}

func TestEmbeddingMaintenanceCreatesNewsEventAsset(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "test",
		Title:   "动力电池公司发布储能新品",
		Summary: "储能与新能源车产业链更新",
		Content: "电池材料订单增长",
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	result, err := svc.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{
		ObjectTypes: []string{EmbeddingObjectNewsEvent},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("maintenance batch: %v", err)
	}
	if result.Total != 1 || result.Success != 1 {
		t.Fatalf("result=%#v, want one news asset processed", result)
	}
	asset, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsEvent, event.ID, model.ID)
	if err != nil {
		t.Fatalf("get news embedding asset: %v", err)
	}
	if asset.Status != EmbeddingAssetStatusReady || asset.TextHash == "" {
		t.Fatalf("asset=%#v, want ready with text hash", asset)
	}
}

func TestEmbeddingStatusBreakdownGroupsReadyAndMissingAssets(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-v1")

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "储能产业链更新"}); err != nil {
		t.Fatalf("create news event: %v", err)
	}
	if _, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "机器人产业链", UserThesis: "关注上游零部件"}); err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	if _, err := svc.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{
		ObjectTypes: []string{EmbeddingObjectStockProfile},
		Limit:       1,
	}); err != nil {
		t.Fatalf("maintenance batch: %v", err)
	}

	status, err := svc.GetEmbeddingStatus(ctx)
	if err != nil {
		t.Fatalf("get embedding status: %v", err)
	}
	breakdown := map[string]EmbeddingAssetBreakdown{}
	for _, item := range status.AssetBreakdown {
		breakdown[item.Category] = item
	}
	if got := breakdown[EmbeddingObjectStockProfile]; got.ReadyAssetCount != 1 || got.MissingAssetCount != 0 {
		t.Fatalf("stock profile breakdown=%+v, want ready=1 missing=0", got)
	}
	if got := breakdown[EmbeddingObjectNewsEvent]; got.ReadyAssetCount != 0 || got.MissingAssetCount != 1 {
		t.Fatalf("news event breakdown=%+v, want ready=0 missing=1", got)
	}
	if got := breakdown["other"]; got.ReadyAssetCount != 0 || got.MissingAssetCount != 1 {
		t.Fatalf("other breakdown=%+v, want ready=0 missing=1", got)
	}
}

func TestCountMissingEmbeddingSourcesByTypeUsesCurrentModelAssets(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	upsertEmbeddingTestProfile(t, svc, "600519", "贵州茅台", "白酒")
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "储能产业链更新"})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	if _, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "机器人产业链", UserThesis: "关注上游零部件"}); err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	if _, err := svc.store.UpsertEmbeddingAsset(ctx, EmbeddingAsset{
		ObjectType:          EmbeddingObjectStockProfile,
		ObjectID:            "300750",
		TextHash:            "hash",
		ModelID:             model.ID,
		ProviderID:          model.ProviderID,
		EmbeddingProtocol:   model.EmbeddingProtocol,
		EmbeddingDimensions: 3,
		VectorRef:           "ready-300750",
		Status:              EmbeddingAssetStatusReady,
	}); err != nil {
		t.Fatalf("upsert profile asset: %v", err)
	}
	if _, err := svc.store.UpsertEmbeddingAsset(ctx, EmbeddingAsset{
		ObjectType:          EmbeddingObjectNewsEvent,
		ObjectID:            event.ID,
		TextHash:            "old-hash",
		ModelID:             "old-model",
		EmbeddingDimensions: 3,
		VectorRef:           "old-news",
		Status:              EmbeddingAssetStatusReady,
	}); err != nil {
		t.Fatalf("upsert old news asset: %v", err)
	}

	counts, err := svc.store.CountMissingEmbeddingSourcesByType(ctx, normalizeEmbeddingObjectTypes(nil), model.ID)
	if err != nil {
		t.Fatalf("count missing: %v", err)
	}
	if counts[EmbeddingObjectStockProfile] != 1 || counts[EmbeddingObjectNewsEvent] != 1 || counts[EmbeddingObjectOpportunity] != 1 {
		t.Fatalf("counts=%+v, want profile=1 news=1 opportunity=1", counts)
	}
}

func TestStockProfileAIEnhancementMarksEmbeddingAssetStale(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	if _, err := svc.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}}); err != nil {
		t.Fatalf("maintenance batch: %v", err)
	}
	if _, err := svc.applyStockProfileEnhancementResult(ctx, "300750", map[string]any{
		"summaryZh":  "动力电池与储能龙头",
		"keywordsZh": []any{"储能"},
	}, "test-model", 0.8); err != nil {
		t.Fatalf("apply enhancement: %v", err)
	}
	asset, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectStockProfile, "300750", model.ID)
	if err != nil {
		t.Fatalf("get embedding asset: %v", err)
	}
	if asset.Status != EmbeddingAssetStatusStale {
		t.Fatalf("asset status=%s, want stale", asset.Status)
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
	return configureEmbeddingModelWithDimensions(t, svc, modelName, 3)
}

func configureEmbeddingModelWithDimensions(t *testing.T, svc *Service, modelName string, dimensions int) AgentModelProfile {
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
		EmbeddingDimensions: dimensions,
	})
	if err != nil {
		t.Fatalf("create embedding model: %v", err)
	}
	modelID := model.ID
	enabled := true
	rateLimit := 0
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID:    &modelID,
		Enabled:             &enabled,
		MaintainRateLimitMs: &rateLimit,
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
