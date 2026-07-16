package stockv2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOfflineEmbeddingMigrationResumesBeforeCleaningOldModel(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	oldModel := configureEmbeddingModel(t, svc, "embed-old")
	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	upsertEmbeddingTestProfile(t, svc, "600519", "贵州茅台", "白酒")
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{
		ObjectTypes: []string{EmbeddingObjectStockProfile},
		Limit:       2,
	}); err != nil {
		t.Fatalf("rebuild old model: %v", err)
	}
	target, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID:          oldModel.ProviderID,
		ModelName:           "BAAI/bge-m3",
		Enabled:             true,
		Status:              AgentModelStatusAvailable,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("create target model: %v", err)
	}

	firstCtx, cancel := context.WithCancel(ctx)
	_, err = svc.RunOfflineEmbeddingMigration(firstCtx, OfflineEmbeddingMigrationRequest{
		TargetModelID:         target.ID,
		BatchSize:             1,
		MaintainRateLimitMs:   0,
		MaxStalledBatches:     2,
		EnableAutoMaintenance: true,
	}, func(progress EmbeddingMigrationProgress) {
		if progress.Stage == "rebuild" && progress.BatchSucceeded == 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted migration error=%v, want context canceled", err)
	}
	oldAssets, err := svc.store.CountEmbeddingAssetsExceptModel(ctx, target.ID)
	if err != nil || oldAssets != 2 {
		t.Fatalf("old assets after interruption=%d err=%v, want 2 preserved", oldAssets, err)
	}
	targetReady, err := svc.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
		ModelID: target.ID,
		Status:  EmbeddingAssetStatusReady,
	})
	if err != nil || targetReady != 1 {
		t.Fatalf("target ready after interruption=%d err=%v, want one resumable asset", targetReady, err)
	}

	result, err := svc.RunOfflineEmbeddingMigration(ctx, OfflineEmbeddingMigrationRequest{
		TargetModelID:         target.ID,
		BatchSize:             1,
		MaintainRateLimitMs:   0,
		MaxStalledBatches:     2,
		EnableAutoMaintenance: true,
	}, nil)
	if err != nil {
		t.Fatalf("resume migration: %v", err)
	}
	if result.SourceCount != 2 || result.DeletedAssets != 2 || result.DeletedVectors != 2 {
		t.Fatalf("migration result=%+v, want two sources and two old rows removed", result)
	}
	if !result.Verification.Complete() || result.Verification.OtherModelAssets != 0 || result.Verification.OtherModelVectors != 0 {
		t.Fatalf("verification=%+v, want complete target-only index", result.Verification)
	}
	cfg, err := svc.store.GetEmbeddingConfig(ctx)
	if err != nil {
		t.Fatalf("get embedding config: %v", err)
	}
	if cfg.EmbeddingModelID != target.ID || !cfg.Enabled || !cfg.AutoMaintainEnabled {
		t.Fatalf("embedding config=%+v, want target enabled with maintenance", cfg)
	}
}

func TestGenerateEmbeddingRetriesTransientRateLimit(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	model := configureEmbeddingModel(t, svc, "embed-retry")
	var calls atomic.Int32
	svc.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Header:     http.Header{"Retry-After": []string{"0"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"rate_limited"}}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[1,0,0]}]}`)),
		}, nil
	})}
	vector, err := svc.generateEmbedding(context.Background(), model, "动力电池")
	if err != nil {
		t.Fatalf("generate embedding after retry: %v", err)
	}
	if calls.Load() != 2 || len(vector) != 3 {
		t.Fatalf("calls=%d vector=%v, want two attempts and three dimensions", calls.Load(), vector)
	}
}
