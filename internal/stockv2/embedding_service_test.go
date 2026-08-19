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

func TestNewsThreadSemanticMCPFailsWithoutEmbeddingInsteadOfKeywordFallback(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()

	resp := svc.AgentTaskPool().HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": mcpToolSemanticSearchNewsThreads,
			"arguments": map[string]any{
				"query": "板块轮换",
				"limit": 5,
			},
		},
	}))
	text := string(resp)
	if !strings.Contains(text, EmbeddingStatusModelNotConfigured) {
		t.Fatalf("response=%s, want explicit embedding not configured error", text)
	}
	if strings.Contains(text, "keyword") || strings.Contains(text, `"isError":false`) {
		t.Fatalf("response=%s, semantic failure must not fall back to keyword success", text)
	}
}

func TestSemanticSearchNewsThreadsReturnsOnlyActiveThreads(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-v1")

	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title:      "动力电池扩产脉络",
		CoreThesis: "动力电池供应链进入扩产阶段",
		Status:     NewsThreadStatusActive,
	})
	if err != nil {
		t.Fatalf("create news thread: %v", err)
	}
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectNewsThread}}); err != nil {
		t.Fatalf("rebuild news thread embedding: %v", err)
	}
	active, err := svc.SemanticSearchNewsThreads(ctx, SemanticSearchRequest{Query: "动力电池", Limit: 5})
	if err != nil {
		t.Fatalf("search active news thread: %v", err)
	}
	if len(active) != 1 || active[0].Thread.ID != thread.ID {
		t.Fatalf("active results=%#v, want thread %s", active, thread.ID)
	}

	thread.Status = NewsThreadStatusDormant
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatalf("mark news thread dormant: %v", err)
	}
	inactive, err := svc.SemanticSearchNewsThreads(ctx, SemanticSearchRequest{Query: "动力电池", Limit: 5})
	if !errors.Is(err, ErrEmbeddingAssetNotReady) {
		t.Fatalf("search with dormant vector error=%v, want asset not ready", err)
	}
	if len(inactive) != 0 {
		t.Fatalf("inactive results=%#v, want no non-active thread", inactive)
	}
}

func TestSyncNewsContextEmbeddingObjectsIndexesExactFragment(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	model := configureEmbeddingModel(t, svc, "embed-fragment")
	rateLimit := 2 * int(time.Second/time.Millisecond)
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{MaintainRateLimitMs: &rateLimit}); err != nil {
		t.Fatalf("set maintenance rate delay: %v", err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "算力建设脉络", CoreThesis: "数据中心扩建带动算力产业链", Stage: NewsThreadStageEmerging,
		Status: NewsThreadStatusActive,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "fragment-run", AgentRunID: "fragment-agent", WindowType: NewsContextWindowHourly,
		VersionNo: 1, Title: thread.Title, CoreThesis: thread.CoreThesis, Stage: thread.Stage,
		ReviewStatus: NewsContextReviewNotRequired,
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := svc.SyncNewsContextEmbeddingObjects(ctx, []string{thread.ID}, []string{version.ID}); err != nil {
		t.Fatalf("sync fragment embeddings: %v", err)
	}
	for _, expected := range []struct {
		objectType string
		objectID   string
		text       string
	}{
		{EmbeddingObjectNewsThread, thread.ID, NewsThreadEmbeddingText(thread)},
		{EmbeddingObjectNewsThreadVersion, version.ID, NewsThreadVersionEmbeddingText(version)},
	} {
		asset, err := svc.store.GetEmbeddingAssetByObject(ctx, expected.objectType, expected.objectID, model.ID)
		if err != nil {
			t.Fatalf("get synced asset %s: %v", expected.objectID, err)
		}
		if asset.Status != EmbeddingAssetStatusReady || asset.TextHash != hashEmbeddingText(expected.text) || asset.VectorRef == "" {
			t.Fatalf("synced asset=%+v, want exact ready content", asset)
		}
		if ready, err := svc.store.HasEmbeddingVector(ctx, asset.VectorRef); err != nil || !ready {
			t.Fatalf("synced vector ready=%v err=%v", ready, err)
		}
	}
}

func TestSemanticSearchNewsThreadsAsOfReturnsLatestEligibleHistoricalVersion(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-history")
	zero := 0
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{MaintainRateLimitMs: &zero}); err != nil {
		t.Fatalf("disable test rate delay: %v", err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "动力电池脉络", CoreThesis: "动力电池处于扩产初期", Stage: NewsThreadStageEmerging,
		Status: NewsThreadStatusActive,
	})
	if err != nil {
		t.Fatalf("create historical thread: %v", err)
	}
	firstAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	first, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "history-day-1", AgentRunID: "history-agent-1", WindowType: NewsContextWindowDaily,
		VersionNo: 1, Title: thread.Title, CoreThesis: thread.CoreThesis, Stage: NewsThreadStageEmerging,
		ReviewStatus: NewsContextReviewCompleted, EffectiveAt: firstAt, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create first historical version: %v", err)
	}
	secondAt := firstAt.Add(24 * time.Hour)
	second, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "history-day-2", AgentRunID: "history-agent-2", WindowType: NewsContextWindowDaily,
		VersionNo: 2, Title: thread.Title, CoreThesis: "动力电池扩产进入加速阶段", Stage: NewsThreadStageAccelerating,
		ReviewStatus: NewsContextReviewCompleted, EffectiveAt: secondAt, CreatedAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create second historical version: %v", err)
	}
	thread.CoreThesis = second.CoreThesis
	thread.Stage = second.Stage
	thread.CurrentVersion = second.VersionNo
	thread.CurrentVersionID = second.ID
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatalf("update current historical thread: %v", err)
	}
	if err := svc.SyncNewsContextEmbeddingObjects(ctx, []string{thread.ID}, []string{first.ID, second.ID}); err != nil {
		t.Fatalf("sync historical embeddings: %v", err)
	}
	cutoff := firstAt.Add(12 * time.Hour).Format(time.RFC3339Nano)
	items, err := svc.SemanticSearchNewsThreads(ctx, SemanticSearchRequest{Query: "动力电池", Limit: 5, AsOf: cutoff})
	if err != nil {
		t.Fatalf("historical semantic search: %v", err)
	}
	if len(items) != 1 || items[0].Version == nil || items[0].Version.ID != first.ID || items[0].Thread.CurrentVersionID != first.ID {
		t.Fatalf("historical items=%+v, want first version only", items)
	}
	batch, err := svc.semanticSearchNewsThreadsAtBatch(ctx, []SemanticSearchRequest{
		{Query: "动力电池", Limit: 5, AsOf: cutoff},
		{Query: "battery expansion", Limit: 5, AsOf: cutoff},
	})
	if err != nil {
		t.Fatalf("batch historical semantic search: %v", err)
	}
	if len(batch) != 2 || len(batch[0]) != 1 || len(batch[1]) != 1 ||
		batch[0][0].Version == nil || batch[0][0].Version.ID != first.ID ||
		batch[1][0].Version == nil || batch[1][0].Version.ID != first.ID {
		t.Fatalf("batch historical items=%+v, want first version for both queries", batch)
	}
	if _, err := svc.SemanticSearchNewsThreads(ctx, SemanticSearchRequest{Query: "动力电池", AsOf: "invalid"}); !errors.Is(err, ErrInvalidEmbeddingRequest) {
		t.Fatalf("invalid asOf error=%v, want invalid request", err)
	}
}

func TestSemanticSearchNewsThreadsAsOfHydratesLatestVersionAfterTransientIndexPruned(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-history-pruned")
	zero := 0
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{MaintainRateLimitMs: &zero}); err != nil {
		t.Fatalf("disable test rate delay: %v", err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		ID: "history-pruned-thread", Title: "机器人脉络", CoreThesis: "机器人进入量产准备期",
		Stage: NewsThreadStageEmerging, Status: NewsThreadStatusActive,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	dayAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	daily, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "history-pruned-daily", ThreadID: thread.ID, RunID: "history-pruned-run-daily",
		WindowType: NewsContextWindowDaily, VersionNo: 1, Title: thread.Title,
		CoreThesis: thread.CoreThesis, Stage: thread.Stage, ReviewStatus: NewsContextReviewCompleted,
		EffectiveAt: dayAt,
	})
	if err != nil {
		t.Fatalf("create daily version: %v", err)
	}
	hourly, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "history-pruned-hourly", ThreadID: thread.ID, RunID: "history-pruned-run-hourly",
		WindowType: NewsContextWindowHourly, VersionNo: 2, Title: thread.Title,
		CoreThesis: "机器人量产验证出现新进展", Stage: NewsThreadStageSpreading,
		ReviewStatus: NewsContextReviewNotRequired, EffectiveAt: dayAt.Add(6 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create hourly version: %v", err)
	}
	thread.CoreThesis = hourly.CoreThesis
	thread.Stage = hourly.Stage
	thread.CurrentVersion = hourly.VersionNo
	thread.CurrentVersionID = hourly.ID
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatalf("update current thread: %v", err)
	}
	if err := svc.SyncNewsContextEmbeddingObjects(ctx, []string{thread.ID}, []string{daily.ID, hourly.ID}); err != nil {
		t.Fatalf("sync historical versions: %v", err)
	}
	if err := svc.PruneTransientNewsContextEmbeddings(ctx, dayAt.Add(7*time.Hour)); err != nil {
		t.Fatalf("prune transient version: %v", err)
	}

	asOf := dayAt.Add(7 * time.Hour).Format(time.RFC3339Nano)
	items, err := svc.SemanticSearchNewsThreads(ctx, SemanticSearchRequest{Query: "机器人量产", Limit: 5, AsOf: asOf})
	if err != nil {
		t.Fatalf("historical semantic search: %v", err)
	}
	if len(items) != 1 || items[0].Version == nil || items[0].Version.ID != hourly.ID ||
		items[0].Thread.CurrentVersionID != hourly.ID || items[0].RetrievalVersionID != daily.ID ||
		items[0].Asset.ObjectID != daily.ID {
		t.Fatalf("historical result=%+v, want latest hourly snapshot ranked by retained daily vector", items)
	}
	detail, err := svc.GetNewsThreadDetailAsOf(ctx, thread.ID, asOf)
	if err != nil || detail.Theme.CurrentVersionID != items[0].Thread.CurrentVersionID {
		t.Fatalf("historical detail=%+v err=%v, want search/detail version agreement", detail.Theme, err)
	}
}

func TestSyncNewsContextEmbeddingObjectsStopsAfterFirstFailure(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-fragment-failure")
	versions := make([]NewsThreadVersion, 0, 2)
	for index := 0; index < 2; index++ {
		version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
			ThreadID: "fragment-failure-thread", RunID: "fragment-failure-run",
			AgentRunID: "fragment-failure-agent-" + fmt.Sprint(index), WindowType: NewsContextWindowHourly,
			VersionNo: index + 1, Title: "机器人主题", CoreThesis: "机器人产业链进展",
			Stage: NewsThreadStageEmerging, ReviewStatus: NewsContextReviewNotRequired,
		})
		if err != nil {
			t.Fatalf("create failure version: %v", err)
		}
		versions = append(versions, version)
	}
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("embedding provider unavailable")
	})}
	err := svc.SyncNewsContextEmbeddingObjects(ctx, nil, []string{versions[0].ID, versions[1].ID})
	if err == nil {
		t.Fatal("sync error=nil, want fragment to stop")
	}
	first, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, versions[0].ID, model.ID)
	if err != nil || first.Status != EmbeddingAssetStatusFailed {
		t.Fatalf("first failed asset=%+v err=%v", first, err)
	}
	if _, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, versions[1].ID, model.ID); !errors.Is(err, ErrEmbeddingAssetNotFound) {
		t.Fatalf("second asset error=%v, want next object untouched", err)
	}
}

func TestNewsThreadRefreshFailureKeepsPreviousVectorMCPReadable(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	svc.agentMCPServer = &http.Server{}
	svc.agentMCPURL = "http://127.0.0.1/stockv2-test-mcp"
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title:      "动力电池扩产脉络",
		CoreThesis: "动力电池供应链进入扩产阶段",
		Stage:      NewsThreadStageEmerging,
		Status:     NewsThreadStatusActive,
	})
	if err != nil {
		t.Fatalf("create news thread: %v", err)
	}
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectNewsThread}}); err != nil {
		t.Fatalf("build initial news thread embedding: %v", err)
	}
	before, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, thread.ID, model.ID)
	if err != nil {
		t.Fatalf("get initial news thread embedding: %v", err)
	}

	now := time.Now()
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, WindowStart: now.Add(-time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create news context run: %v", err)
	}
	_, err = svc.store.ApplyNewsContextBatch(ctx, run.ID, "agent-refresh-failure", run.WindowType, NewsContextReport{
		RunID: run.ID, WindowType: run.WindowType,
		ThreadChanges: []NewsContextThreadChange{{
			Action: "update", ThreadID: thread.ID, Title: thread.Title,
			CoreThesis: "动力电池供应链出现扩产新证据", Stage: NewsThreadStageSpreading,
			LatestChange: "新增扩产证据", MaterialChange: true, Confidence: 0.8,
		}},
	})
	if err != nil {
		t.Fatalf("apply news thread update: %v", err)
	}
	stillReady, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, thread.ID, model.ID)
	if err != nil {
		t.Fatalf("get previous embedding after topic update: %v", err)
	}
	if stillReady.Status != EmbeddingAssetStatusReady || stillReady.VectorRef != before.VectorRef {
		t.Fatalf("previous embedding became unavailable before replacement: before=%#v after=%#v", before, stillReady)
	}
	pendingDetail, err := svc.GetNewsThreadDetail(ctx, thread.ID)
	if err != nil {
		t.Fatalf("get pending thread detail: %v", err)
	}
	if pendingDetail.IndexStatus != NewsContextIndexStale || !pendingDetail.MCPReadable {
		t.Fatalf("pending detail=%#v, want stale current content with previous MCP vector readable", pendingDetail)
	}

	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		if strings.Contains(stringFromAny(payload["input"]), "新证据") {
			return nil, errors.New("embedding provider unavailable for refreshed topic")
		}
		raw, _ := json.Marshal(map[string]any{"data": []map[string]any{{"embedding": []float64{1, 0, 0}}}})
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	result, err := svc.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectNewsThread}})
	if err != nil {
		t.Fatalf("failed refresh should return observable result: %v", err)
	}
	if result.Failed != 1 || result.Success != 0 {
		t.Fatalf("refresh result=%#v, want one failure", result)
	}
	afterFailure, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, thread.ID, model.ID)
	if err != nil {
		t.Fatalf("get embedding after failed refresh: %v", err)
	}
	if afterFailure.Status != EmbeddingAssetStatusReady || afterFailure.VectorRef != before.VectorRef || afterFailure.TextHash != before.TextHash {
		t.Fatalf("failed refresh replaced previous embedding: before=%#v after=%#v", before, afterFailure)
	}
	failedDetail, err := svc.GetNewsThreadDetail(ctx, thread.ID)
	if err != nil {
		t.Fatalf("get failed thread detail: %v", err)
	}
	if failedDetail.IndexStatus != NewsContextIndexFailed || !failedDetail.MCPReadable || failedDetail.IndexError == "" {
		t.Fatalf("failed detail=%#v, want visible failure with previous MCP vector readable", failedDetail)
	}

	response := string(svc.HandleMCPRequest(mustJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": mcpToolSemanticSearchNewsThreads, "arguments": map[string]any{"query": "动力电池", "limit": 5}},
	})))
	if !strings.Contains(response, `"isError":false`) || !strings.Contains(response, thread.ID) || !strings.Contains(response, before.VectorRef) {
		t.Fatalf("MCP response lost previous ready vector after failed refresh: %s", response)
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

func TestEmbeddingRefreshSwapsVectorThenRemovesOldVector(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}}); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	before, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectStockProfile, "300750", model.ID)
	if err != nil {
		t.Fatalf("get first asset: %v", err)
	}

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池 储能订单更新")
	result, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}})
	if err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	if result.Success != 1 || result.Failed != 0 {
		t.Fatalf("second rebuild result=%#v, want one success", result)
	}
	after, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectStockProfile, "300750", model.ID)
	if err != nil {
		t.Fatalf("get refreshed asset: %v", err)
	}
	if after.VectorRef == before.VectorRef || after.TextHash == before.TextHash {
		t.Fatalf("asset was not swapped: before=%#v after=%#v", before, after)
	}
	var oldCount int
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_vectors_v2 WHERE vector_ref = ?`, before.VectorRef).Scan(&oldCount); err != nil {
		t.Fatalf("count old vector: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old vector count=%d, want 0", oldCount)
	}
}

func TestEmbeddingRefreshFailurePreservesLastReadyAsset(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-v1")

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}}); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	before, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectStockProfile, "300750", model.ID)
	if err != nil {
		t.Fatalf("get first asset: %v", err)
	}

	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池 新证据")
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("embedding provider unavailable")
	})}
	result, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}})
	if err != nil {
		t.Fatalf("failed refresh should return observable batch result: %v", err)
	}
	if result.Failed != 1 || result.Success != 0 {
		t.Fatalf("failed refresh result=%#v, want one failure", result)
	}
	after, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectStockProfile, "300750", model.ID)
	if err != nil {
		t.Fatalf("get preserved asset: %v", err)
	}
	if after.Status != EmbeddingAssetStatusReady || after.VectorRef != before.VectorRef || after.TextHash != before.TextHash {
		t.Fatalf("last ready asset was overwritten: before=%#v after=%#v", before, after)
	}
}

func TestEmbeddingMaintenanceProcessesQueuedMissingAssetWithoutFullScan(t *testing.T) {
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
	ready, err := svc.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
		ObjectType: EmbeddingObjectStockProfile,
		ModelID:    model.ID,
		Status:     EmbeddingAssetStatusReady,
	})
	if err != nil {
		t.Fatalf("count ready profile assets: %v", err)
	}
	if ready != 201 {
		t.Fatalf("ready profile assets=%d, want 201 after one queued item", ready)
	}
}

func TestEmbeddingWorkRevisionPreservesConcurrentSourceChange(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()

	if err := svc.store.QueueEmbeddingWork(ctx, EmbeddingObjectStockProfile, "300750"); err != nil {
		t.Fatalf("queue first revision: %v", err)
	}
	items, err := svc.store.ListEmbeddingWorkItems(ctx, []string{EmbeddingObjectStockProfile}, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("list first revision: items=%+v err=%v", items, err)
	}
	first := items[0]
	if err := svc.store.EnsureEmbeddingWork(ctx, EmbeddingObjectStockProfile, "300750"); err != nil {
		t.Fatalf("ensure existing revision: %v", err)
	}
	items, err = svc.store.ListEmbeddingWorkItems(ctx, []string{EmbeddingObjectStockProfile}, 1)
	if err != nil || len(items) != 1 || items[0].Revision != first.Revision {
		t.Fatalf("ensure changed revision: items=%+v err=%v", items, err)
	}
	if err := svc.store.QueueEmbeddingWork(ctx, EmbeddingObjectStockProfile, "300750"); err != nil {
		t.Fatalf("queue concurrent revision: %v", err)
	}
	if err := svc.store.CompleteEmbeddingWork(ctx, first); err != nil {
		t.Fatalf("complete stale revision: %v", err)
	}
	items, err = svc.store.ListEmbeddingWorkItems(ctx, []string{EmbeddingObjectStockProfile}, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("new revision was lost: items=%+v err=%v", items, err)
	}
	if items[0].Revision <= first.Revision {
		t.Fatalf("revision=%d, want newer than %d", items[0].Revision, first.Revision)
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
	result, err := svc.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}})
	if err != nil {
		t.Fatalf("maintenance batch: %v", err)
	}
	if result.Success != 1 {
		t.Fatalf("maintenance result=%#v, want one successful embedding asset", result)
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
	autoMaintain := false
	rateLimit := 0
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID:    &modelID,
		Enabled:             &enabled,
		AutoMaintainEnabled: &autoMaintain,
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
