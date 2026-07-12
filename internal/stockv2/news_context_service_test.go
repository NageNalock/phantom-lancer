package stockv2

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestApplyNewsContextBatchIsIdempotentAndPersistsEvidence(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	run, event := seedNewsContextEventRun(t, svc, ctx)
	const agentRunID = "agent-news-context-test"
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, agentRunID, []string{event.ID}); err != nil {
		t.Fatalf("mark run item running: %v", err)
	}
	report := newsContextCreateThreadReport(run, event)

	first, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, agentRunID, run.WindowType, report)
	if err != nil {
		t.Fatalf("apply news context batch: %v", err)
	}
	if first.ProcessedCount != 1 || first.CreatedThreadCount != 1 || len(first.ChangedVersionIDs) != 1 {
		t.Fatalf("unexpected apply result: %+v", first)
	}
	if _, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, agentRunID, run.WindowType, report); err != nil {
		t.Fatalf("repeat idempotent batch: %v", err)
	}
	threads, err := svc.store.ListNewsThreads(ctx, NewsThreadListFilter{Limit: 10})
	if err != nil || len(threads) != 1 {
		t.Fatalf("threads=%+v err=%v", threads, err)
	}
	versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{ThreadID: threads[0].ID, Limit: 10})
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if err := svc.validatePortfolioSentinelNewsContextCoverage(ctx, run.ID, nil); !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
		t.Fatalf("missing portfolio review coverage error = %v", err)
	}
	if err := svc.validatePortfolioSentinelNewsContextCoverage(ctx, run.ID, []string{versions[0].ID}); err != nil {
		t.Fatalf("complete portfolio review coverage rejected: %v", err)
	}
	evidence, err := svc.store.ListNewsThreadEvidence(ctx, NewsThreadEvidenceListFilter{NewsEventID: event.ID, Limit: 10})
	if err != nil || len(evidence) != 1 || evidence[0].VersionID != versions[0].ID {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(items) != 1 || items[0].Status != NewsContextRunItemCompleted {
		t.Fatalf("run items=%+v err=%v", items, err)
	}
	storedRun, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || storedRun.ProcessedCount != 1 || storedRun.PendingCount != 0 {
		t.Fatalf("stored run=%+v err=%v", storedRun, err)
	}
}

func TestValidateNewsContextReportRejectsIncompleteCoverage(t *testing.T) {
	run := NewsContextRun{ID: "context-run", WindowType: NewsContextWindowFourHour}
	items := []NewsContextRunItem{
		{ObjectType: NewsContextRunItemNewsEvent, ObjectID: "event-1"},
		{ObjectType: NewsContextRunItemThread, ObjectID: "thread-1"},
	}
	report := NewsContextReport{
		SchemaVersion:    NewsContextResultSchemaVersion,
		RunID:            run.ID,
		WindowType:       run.WindowType,
		ProcessedNewsIDs: []string{"event-1"},
		NewsDecisions:    []NewsContextNewsDecision{{NewsEventID: "event-1", Disposition: "support"}},
	}
	if err := validateNewsContextReport(run, items, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("incomplete thread coverage error = %v", err)
	}
	report.UnchangedThreadIDs = []string{"thread-1"}
	if err := validateNewsContextReport(run, items, report); err != nil {
		t.Fatalf("complete coverage rejected: %v", err)
	}
}

func TestSeedDailyNewsContextRunItemsPagesEveryActiveThreadWithoutNews(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	tx, err := svc.store.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin thread seed: %v", err)
	}
	for i := 0; i < newsContextSeedPageSize+1; i++ {
		thread := normalizeNewsThread(NewsThread{
			ID: fmt.Sprintf("daily-thread-%04d", i), Title: fmt.Sprintf("每日主题 %d", i),
			CoreThesis: "验证每日全量分页复核", Stage: NewsThreadStageDormant,
			Status: NewsThreadStatusActive, FirstSeenAt: now.Add(-72 * time.Hour),
			LastChangedAt: now.Add(-48 * time.Hour), CreatedAt: now.Add(-72 * time.Hour),
		})
		if err := upsertNewsThreadTx(ctx, tx, thread); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed active thread %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit thread seed: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, WindowStart: now.Add(-24 * time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create daily run: %v", err)
	}
	if err := svc.seedNewsContextRunItems(ctx, &run); err != nil {
		t.Fatalf("seed daily run items: %v", err)
	}
	count, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: run.ID, ObjectType: NewsContextRunItemThread,
	})
	if err != nil || count != newsContextSeedPageSize+1 {
		t.Fatalf("daily thread item count=%d err=%v", count, err)
	}
	if run.InputCount != newsContextSeedPageSize+1 {
		t.Fatalf("daily input count=%d, want %d", run.InputCount, newsContextSeedPageSize+1)
	}
}

func TestDailyUnchangedThreadCreatesReviewedCheckpoint(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	run, thread, snapshot := seedDailyUnchangedNewsThreadRun(t, svc, ctx)

	if snapshot.WindowType != NewsContextWindowDaily || snapshot.MaterialChange || snapshot.Stage != thread.Stage {
		t.Fatalf("daily snapshot=%+v", snapshot)
	}
	if snapshot.ResearchStatus != "failed" {
		t.Fatalf("daily snapshot research status=%q, want copied failed", snapshot.ResearchStatus)
	}
	storedThread, err := svc.store.GetNewsThread(ctx, thread.ID)
	if err != nil || storedThread.CurrentVersionID != snapshot.ID || !storedThread.LastChangedAt.Equal(thread.LastChangedAt) {
		t.Fatalf("stored daily thread=%+v err=%v", storedThread, err)
	}
	changes, err := svc.store.ListNewsContextChangedThreads(ctx, run.ID, 10, 0)
	if err != nil || len(changes) != 1 || changes[0].VersionID != snapshot.ID {
		t.Fatalf("daily review manifest=%+v err=%v", changes, err)
	}
	if _, found, err := svc.store.FindCompletedDailyNewsThreadVersionAfter(ctx, thread.ID, run.WindowStart); err != nil || found {
		t.Fatalf("pending daily checkpoint found=%v err=%v", found, err)
	}
	if err := svc.store.UpdateNewsThreadReviewStatusForRun(ctx, run.ID, NewsContextReviewCompleted, time.Now()); err != nil {
		t.Fatalf("complete daily review: %v", err)
	}
	checkpoint, found, err := svc.store.FindCompletedDailyNewsThreadVersionAfter(ctx, thread.ID, run.WindowStart)
	if err != nil || !found || checkpoint.ID != snapshot.ID {
		t.Fatalf("completed daily checkpoint=%+v found=%v err=%v", checkpoint, found, err)
	}
	storedRun, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get daily run: %v", err)
	}
	if err := svc.decorateNewsContextRun(ctx, &storedRun); err != nil {
		t.Fatalf("decorate daily run: %v", err)
	}
	if storedRun.UpdatedThreadCount != 0 || storedRun.UnchangedThreadCount != 1 {
		t.Fatalf("daily run counts updated=%d unchanged=%d", storedRun.UpdatedThreadCount, storedRun.UnchangedThreadCount)
	}
}

func TestPortfolioReviewBindsContextBeforeImmediateSubmission(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	run, _, _ := seedDailyUnchangedNewsThreadRun(t, svc, ctx)
	run.Status = NewsContextRunStatusWaitingReview
	run.ReviewStatus = NewsContextReviewPending
	run, err := svc.store.UpdateNewsContextRun(ctx, run)
	if err != nil {
		t.Fatalf("prepare context review run: %v", err)
	}
	configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool: svc.agentTaskPool, submit: true, summary: "immediate incomplete review",
		result: map[string]any{
			"schema_version": PortfolioSentinelReportSchemaVersion, "overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary": "missing checked versions", "portfolio_actions": []any{}, "affected_holdings": []any{},
		},
	}
	now := time.Now()
	sentinel, err := svc.startPortfolioSentinelRunForNewsContext(ctx, PortfolioSentinelTriggerManual,
		PortfolioSentinelWindowManual, "", now.Add(-time.Hour), now, "", run.ID, false)
	if err != nil {
		t.Fatalf("start immediate portfolio review: %v", err)
	}
	storedSentinel, err := svc.store.GetPortfolioSentinelRun(ctx, sentinel.ID)
	if err != nil || storedSentinel.Status != PortfolioSentinelStatusFailed {
		t.Fatalf("sentinel status=%q error=%q getErr=%v", storedSentinel.Status, storedSentinel.ErrorMessage, err)
	}
	linked, found, err := svc.store.FindNewsContextRunByReviewRunID(ctx, sentinel.ID)
	if err != nil || !found || linked.ID != run.ID || linked.ReviewStatus != NewsContextReviewRunning {
		t.Fatalf("linked context run=%+v found=%v err=%v", linked, found, err)
	}
}

func seedDailyUnchangedNewsThreadRun(t *testing.T, svc *Service, ctx context.Context) (NewsContextRun, NewsThread, NewsThreadVersion) {
	t.Helper()
	now := time.Now()
	baseVersion, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "daily-base-version-" + generateID(), ThreadID: "daily-thread-" + generateID(),
		RunID: "seed-run-" + generateID(), AgentRunID: "seed-agent-" + generateID(),
		WindowType: NewsContextWindowFourHour, VersionNo: 1, Title: "算力供给主题",
		CoreThesis: "供给约束仍需持续跟踪", Stage: NewsThreadStageSpreading,
		LatestChange: "前次核实失败", Confidence: 0.7, ResearchStatus: "failed",
		ReviewStatus: NewsContextReviewCompleted, IndexStatus: NewsContextIndexReady,
		CreatedAt: now.Add(-48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create base thread version: %v", err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		ID: baseVersion.ThreadID, Title: baseVersion.Title, CoreThesis: baseVersion.CoreThesis,
		Stage: baseVersion.Stage, LatestChange: baseVersion.LatestChange, Confidence: baseVersion.Confidence,
		Status: NewsThreadStatusActive, CurrentVersion: 1, CurrentVersionID: baseVersion.ID,
		ReviewStatus: NewsContextReviewCompleted, IndexStatus: NewsContextIndexReady,
		FirstSeenAt: now.Add(-72 * time.Hour), LastChangedAt: now.Add(-48 * time.Hour),
		CreatedAt: now.Add(-72 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create daily thread: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, WindowStart: now.Add(-24 * time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create daily context run: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: thread.ID, Status: NewsContextRunItemPending,
	}}); err != nil {
		t.Fatalf("add daily thread item: %v", err)
	}
	const agentRunID = "daily-agent-run"
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, agentRunID, []string{thread.ID}); err != nil {
		t.Fatalf("mark daily thread running: %v", err)
	}
	report := NewsContextReport{
		SchemaVersion: NewsContextResultSchemaVersion, RunID: run.ID, WindowType: run.WindowType,
		UnchangedThreadIDs: []string{thread.ID}, NewsDecisions: []NewsContextNewsDecision{}, SearchAudit: []NewsContextSearchAudit{},
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: run.ID, AgentRunID: agentRunID, Status: NewsContextRunItemRunning, Limit: 10,
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("daily running items=%+v err=%v", items, err)
	}
	if err := validateNewsContextReport(run, items, report); err != nil {
		t.Fatalf("validate daily unchanged report: %v", err)
	}
	if _, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, agentRunID, run.WindowType, report); err != nil {
		t.Fatalf("apply daily unchanged report: %v", err)
	}
	versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{ThreadID: thread.ID, RunID: run.ID, Limit: 10})
	if err != nil || len(versions) != 1 {
		t.Fatalf("daily snapshot versions=%+v err=%v", versions, err)
	}
	run, err = svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload daily context run: %v", err)
	}
	return run, thread, versions[0]
}

func seedNewsContextEventRun(t *testing.T, svc *Service, ctx context.Context) (NewsContextRun, NewsEvent) {
	t.Helper()
	now := time.Now()
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "半导体设备订单增长", Summary: "订单与政策催化同步改善",
		Content: "用于验证消息脉络压缩和证据保留。", URL: "https://example.com/news?id=private", EventAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, WindowStart: now.Add(-2 * time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create news context run: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID, Status: NewsContextRunItemPending,
	}}); err != nil {
		t.Fatalf("add news context run item: %v", err)
	}
	run.InputCount = 1
	run.PendingCount = 1
	run, err = svc.store.UpdateNewsContextRun(ctx, run)
	if err != nil {
		t.Fatalf("update news context run: %v", err)
	}
	return run, event
}

func newsContextCreateThreadReport(run NewsContextRun, event NewsEvent) NewsContextReport {
	return NewsContextReport{
		SchemaVersion: NewsContextResultSchemaVersion, RunID: run.ID, WindowType: run.WindowType,
		ProcessedNewsIDs: []string{event.ID},
		NewsDecisions:    []NewsContextNewsDecision{{NewsEventID: event.ID, Disposition: "create", Reason: "形成新主题"}},
		ThreadChanges: []NewsContextThreadChange{{
			Action: "create", Title: "半导体设备景气扩散", CoreThesis: "订单和政策共同推动产业链景气扩散",
			Stage: NewsThreadStageEmerging, LatestChange: "新增订单证据", MaterialChange: true, Confidence: 0.82,
			Industries: []string{"半导体设备"}, Symbols: []string{"TEST"}, Facts: []string{"订单增长"},
			Inferences: []string{"景气可能向上游扩散"}, CounterEvidence: []string{"仍需验证交付"},
			OpenQuestions: []string{"持续性如何"}, Catalysts: []string{"后续订单公告"},
			Invalidations: []string{"订单取消"}, EvidenceNewsIDs: []string{event.ID}, ResearchStatus: "completed",
		}},
		SearchAudit: []NewsContextSearchAudit{{Question: "订单是否有公开依据", Status: "verified", Sources: []string{"https://example.com/public"}}},
	}
}
