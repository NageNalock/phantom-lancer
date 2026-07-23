package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type shutdownBlockingNewsContextExecutor struct {
	started chan struct{}
	stopped chan struct{}
}

func (e *shutdownBlockingNewsContextExecutor) ExecuteNewsContextAggregation(
	ctx context.Context,
	_ string,
	_ NewsContextAggregationPack,
	_ string,
	_ string,
) (*AgentExecutorOutput, error) {
	close(e.started)
	<-ctx.Done()
	close(e.stopped)
	return &AgentExecutorOutput{ExitCode: -1}, ctx.Err()
}

func TestServiceCloseWaitsForNewsContextWorkerBeforeClosingStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stockv2.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, nil)
	closed := false
	defer func() {
		if !closed {
			_ = svc.Close()
		}
	}()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI, Name: "shutdown-test",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID, ModelName: "shutdown-test-model", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &model.ID,
	}); err != nil {
		t.Fatalf("bind model: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "关闭期间保留的新闻", Content: "验证服务等待归纳协程退出。", EventAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowFourHour, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusRunning, Phase: newsContextRunPhaseAggregating,
		WindowStart: now.Add(-time.Hour), WindowEnd: now,
		InputCount: 1, PendingCount: 1, ReviewStatus: NewsContextReviewNotRequired,
		CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID,
		Status: NewsContextRunItemPending, SourceAt: event.EventAt,
	}}); err != nil {
		t.Fatalf("add run item: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "four_hour",
		RangeStartAt: run.WindowStart, CutoffAt: run.WindowEnd,
		TotalNewsCount: 1, RemainingNewsCount: 1, CurrentRunID: run.ID,
		CurrentWindowStart: run.WindowStart, CurrentWindowEnd: run.WindowEnd,
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link run: %v", err)
	}

	executor := &shutdownBlockingNewsContextExecutor{started: make(chan struct{}), stopped: make(chan struct{})}
	svc.newsContextExecutor = executor
	if !svc.tryStartNewsContextRun() {
		t.Fatal("reserve news context execution")
	}
	if !svc.launchNewsContextWorker(run.ID, svc.executeNewsContextRun) {
		t.Fatal("launch news context execution")
	}
	select {
	case <-executor.started:
	case <-time.After(5 * time.Second):
		t.Fatal("news context executor did not start")
	}

	done := make(chan error, 1)
	go func() { done <- svc.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("close service: %v", err)
		}
		closed = true
	case <-time.After(5 * time.Second):
		t.Fatal("service close did not wait for and stop news context execution")
	}
	select {
	case <-executor.stopped:
	default:
		t.Fatal("service closed before news context executor stopped")
	}

	reopened, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if recovered.Status != NewsContextRunStatusPending || recovered.Phase != newsContextRunPhaseAggregating ||
		recovered.CurrentAgentRunID != "" || recovered.ErrorMessage != "" {
		t.Fatalf("recovered run = %+v", recovered)
	}
	items, err := reopened.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(items) != 1 || items[0].Status != NewsContextRunItemPending || items[0].AgentRunID != "" {
		t.Fatalf("recovered items = %+v, err=%v", items, err)
	}
	recoveredBackfill, err := reopened.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil || recoveredBackfill.Status != NewsContextBackfillStatusRunning || recoveredBackfill.ErrorMessage != "" {
		t.Fatalf("recovered backfill = %+v, err=%v", recoveredBackfill, err)
	}
}
