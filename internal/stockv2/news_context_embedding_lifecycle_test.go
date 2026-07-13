package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPruneTransientNewsContextEmbeddingsKeepsDurableVersions(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-prune")
	zero := 0
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{MaintainRateLimitMs: &zero}); err != nil {
		t.Fatalf("disable test rate delay: %v", err)
	}
	now := time.Now()
	versions := make([]NewsThreadVersion, 0, 3)
	for index, item := range []struct {
		window   string
		material bool
	}{
		{NewsContextWindowHourly, false},
		{NewsContextWindowFourHour, true},
		{NewsContextWindowDaily, false},
	} {
		version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
			ThreadID: "prune-thread", RunID: "prune-run", AgentRunID: "prune-agent-" + string(rune('a'+index)),
			WindowType: item.window, VersionNo: index + 1, Title: "算力主题", CoreThesis: "算力建设持续推进",
			Stage: NewsThreadStageEmerging, MaterialChange: item.material,
			ReviewStatus: NewsContextReviewNotRequired, EffectiveAt: now.Add(time.Duration(index) * time.Minute),
			CreatedAt: now.Add(24*time.Hour + time.Duration(index)*time.Minute),
		})
		if err != nil {
			t.Fatalf("create version %d: %v", index, err)
		}
		versions = append(versions, version)
	}
	ids := []string{versions[0].ID, versions[1].ID, versions[2].ID}
	if err := svc.SyncNewsContextEmbeddingObjects(ctx, nil, ids); err != nil {
		t.Fatalf("sync version embeddings: %v", err)
	}
	transient, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, versions[0].ID, model.ID)
	if err != nil {
		t.Fatalf("get transient asset: %v", err)
	}
	if err := svc.PruneTransientNewsContextEmbeddings(ctx, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("prune transient assets: %v", err)
	}
	if _, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, versions[0].ID, model.ID); !errors.Is(err, ErrEmbeddingAssetNotFound) {
		t.Fatalf("transient asset error=%v, want removed", err)
	}
	if ready, err := svc.store.HasEmbeddingVector(ctx, transient.VectorRef); err != nil || ready {
		t.Fatalf("transient vector ready=%v err=%v, want removed", ready, err)
	}
	if stored, err := svc.store.GetNewsThreadVersion(ctx, versions[0].ID); err != nil || stored.IndexStatus != NewsContextIndexPending {
		t.Fatalf("transient version=%+v err=%v, want durable conclusion without retained vector", stored, err)
	}
	for _, retained := range versions[1:] {
		asset, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, retained.ID, model.ID)
		if err != nil || asset.Status != EmbeddingAssetStatusReady {
			t.Fatalf("retained version %s asset=%+v err=%v", retained.ID, asset, err)
		}
	}
}

func TestNewsContextBackfillFinalIndexesYieldInPagesAndIncludeFinalDaily(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "embed-final-pages")
	until := time.Now().Add(2 * time.Hour)
	threads := make([]NewsThread, 0, newsContextBackfillIndexPageSize+2)
	for index := 0; index < newsContextBackfillIndexPageSize+2; index++ {
		thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
			Title: "最终索引主题", CoreThesis: "最终索引必须分页释放执行位",
			Stage: NewsThreadStageEmerging, Status: NewsThreadStatusActive,
		})
		if err != nil {
			t.Fatalf("create thread %d: %v", index, err)
		}
		threads = append(threads, thread)
	}
	finalDaily, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: threads[0].ID, RunID: "final-daily-run", AgentRunID: "final-daily-agent",
		WindowType: NewsContextWindowDaily, VersionNo: 1, Title: "最终当前主题",
		CoreThesis: "历史截止点之后的最终每日版本也必须建立索引",
		Stage:      NewsThreadStageSpreading, ReviewStatus: NewsContextReviewCompleted,
		EffectiveAt: until.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create final daily version: %v", err)
	}
	afterFinal, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: threads[0].ID, RunID: "future-run", AgentRunID: "future-agent",
		WindowType: NewsContextWindowDaily, VersionNo: 2, Title: "未来主题",
		CoreThesis: "最终复核结束以后才生效的版本不属于该任务安全门",
		Stage:      NewsThreadStageAccelerating, ReviewStatus: NewsContextReviewCompleted,
		EffectiveAt: until.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create future version: %v", err)
	}

	for page := 0; page < 2; page++ {
		_, ready, err := svc.ensureNewsContextBackfillFinalIndexes(ctx, until)
		if err != nil || ready {
			t.Fatalf("page %d ready=%v err=%v", page+1, ready, err)
		}
	}
	_, ready, err := svc.ensureNewsContextBackfillFinalIndexes(ctx, until)
	if err != nil || !ready {
		t.Fatalf("final index verification ready=%v err=%v", ready, err)
	}
	if _, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, finalDaily.ID, model.ID); err != nil {
		t.Fatalf("final daily version was not indexed: %v", err)
	}
	if _, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, afterFinal.ID, model.ID); !errors.Is(err, ErrEmbeddingAssetNotFound) {
		t.Fatalf("future version index error=%v, want excluded", err)
	}
}
