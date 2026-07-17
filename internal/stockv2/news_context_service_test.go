package stockv2

import (
	"context"
	"encoding/json"
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
	report.NewsDecisions[0].ThreadID = "temporary-new-theme"

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
	if err != nil || len(items) != 1 || items[0].Status != NewsContextRunItemCompleted ||
		items[0].ThreadID != threads[0].ID || items[0].ThreadID == "temporary-new-theme" {
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
	report.NewsDecisions[0].Disposition = "noise"
	if err := validateNewsContextReport(run, items, report); err != nil {
		t.Fatalf("complete coverage rejected: %v", err)
	}
	report.NewsDecisions[0].Disposition = "anything"
	if err := validateNewsContextReport(run, items, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("unknown disposition error = %v", err)
	}
}

func TestNormalizeNewsContextThreadReviewOutcomesDropsOnlyCandidatesOutsideBatch(t *testing.T) {
	run := NewsContextRun{ID: "context-run", WindowType: NewsContextWindowHourly}
	items := []NewsContextRunItem{{ObjectType: NewsContextRunItemNewsEvent, ObjectID: "news-1"}}
	report := NewsContextReport{
		SchemaVersion:      NewsContextResultSchemaVersion,
		RunID:              run.ID,
		WindowType:         run.WindowType,
		ProcessedNewsIDs:   []string{"news-1"},
		UnchangedThreadIDs: []string{"semantic-search-candidate"},
		NewsDecisions: []NewsContextNewsDecision{{
			NewsEventID: "news-1", Disposition: "update", ThreadID: "existing-thread",
		}},
		ThreadChanges: []NewsContextThreadChange{{
			Action: "update", ThreadID: "existing-thread", Title: "既有主题", CoreThesis: "新增证据支持既有结论",
			Stage: NewsThreadStageSpreading, Confidence: 0.8, EvidenceNewsIDs: []string{"news-1"},
		}},
	}
	if ignored := normalizeNewsContextThreadReviewOutcomes(items, &report); ignored != 1 {
		t.Fatalf("ignored outcomes = %d, want 1", ignored)
	}
	if len(report.UnchangedThreadIDs) != 0 {
		t.Fatalf("outside unchanged outcomes were retained: %+v", report.UnchangedThreadIDs)
	}
	if err := validateNewsContextReport(run, items, report); err != nil {
		t.Fatalf("valid news-only update rejected after normalization: %v", err)
	}
}

func TestNormalizeNewsContextThreadReviewOutcomesKeepsManifestCoverageStrict(t *testing.T) {
	run := NewsContextRun{ID: "daily-context-run", WindowType: NewsContextWindowDaily}
	items := []NewsContextRunItem{{ObjectType: NewsContextRunItemThread, ObjectID: "manifest-thread"}}
	report := NewsContextReport{
		SchemaVersion:      NewsContextResultSchemaVersion,
		RunID:              run.ID,
		WindowType:         run.WindowType,
		UnchangedThreadIDs: []string{"semantic-search-candidate"},
	}
	if ignored := normalizeNewsContextThreadReviewOutcomes(items, &report); ignored != 1 {
		t.Fatalf("ignored outcomes = %d, want 1", ignored)
	}
	if err := validateNewsContextReport(run, items, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("missing manifest thread coverage error = %v", err)
	}
}

func TestValidateNewsContextReportRequiresOneMatchingThemeEvidencePerCoveredNews(t *testing.T) {
	run := NewsContextRun{ID: "context-run", WindowType: NewsContextWindowHourly}
	newsItem := NewsContextRunItem{ObjectType: NewsContextRunItemNewsEvent, ObjectID: "news-1"}
	change := func(action, threadID string, evidence ...string) NewsContextThreadChange {
		return NewsContextThreadChange{
			Action: action, ThreadID: threadID, Title: "主题", CoreThesis: "主题结论",
			Stage: NewsThreadStageEmerging, Confidence: 0.8, EvidenceNewsIDs: evidence,
		}
	}
	report := func(decisionThreadID string, changes ...NewsContextThreadChange) NewsContextReport {
		return NewsContextReport{
			SchemaVersion: NewsContextResultSchemaVersion, RunID: run.ID, WindowType: run.WindowType,
			ProcessedNewsIDs: []string{"news-1"},
			NewsDecisions: []NewsContextNewsDecision{{
				NewsEventID: "news-1", Disposition: "support", ThreadID: decisionThreadID,
			}},
			ThreadChanges: changes,
		}
	}
	for _, tt := range []struct {
		name    string
		items   []NewsContextRunItem
		report  NewsContextReport
		wantErr bool
	}{
		{name: "support without change or evidence", items: []NewsContextRunItem{newsItem}, report: report(""), wantErr: true},
		{name: "wrong existing thread", items: []NewsContextRunItem{newsItem, {
			ObjectType: NewsContextRunItemThread, ObjectID: "thread-old", ThreadID: "thread-old",
		}}, report: report("thread-wrong", change("update", "thread-old", "news-1")), wantErr: true},
		{name: "cross fragment evidence", items: []NewsContextRunItem{newsItem}, report: report("temp-new", change("create", "", "news-2")), wantErr: true},
		{name: "duplicate evidence", items: []NewsContextRunItem{newsItem}, report: report("", change("create", "", "news-1"), change("create", "", "news-1")), wantErr: true},
		{name: "noise cannot be evidence", items: []NewsContextRunItem{newsItem}, report: func() NewsContextReport {
			value := report("", change("create", "", "news-1"))
			value.NewsDecisions[0].Disposition = "noise"
			return value
		}(), wantErr: true},
		{name: "valid new thread temporary identity", items: []NewsContextRunItem{newsItem}, report: report("temp-new", change("create", "", "news-1"))},
		{name: "valid existing thread", items: []NewsContextRunItem{newsItem, {
			ObjectType: NewsContextRunItemThread, ObjectID: "thread-old", ThreadID: "thread-old",
		}}, report: report("thread-old", change("update", "thread-old", "news-1"))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewsContextReport(run, tt.items, tt.report)
			if tt.wantErr && !errors.Is(err, ErrInvalidNewsContextResult) {
				t.Fatalf("validation error=%v, want invalid result", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("valid evidence mapping rejected: %v", err)
			}
		})
	}
}

func TestApplyNewsContextBatchDefensivelyRejectsCrossFragmentEvidence(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	run, event := seedNewsContextEventRun(t, svc, ctx)
	outside, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "另一分片", EventAt: run.WindowEnd})
	if err != nil {
		t.Fatalf("create outside news: %v", err)
	}
	const agentRunID = "defensive-evidence-agent"
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, agentRunID, []string{event.ID}); err != nil {
		t.Fatalf("mark input running: %v", err)
	}
	report := newsContextCreateThreadReport(run, event)
	report.ThreadChanges[0].EvidenceNewsIDs = []string{outside.ID}
	if _, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, agentRunID, run.WindowType, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("repository accepted cross-fragment evidence: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(items) != 1 || items[0].Status != NewsContextRunItemRunning {
		t.Fatalf("rejected batch mutated checklist: items=%+v err=%v", items, err)
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

func TestShrinkNewsContextRetryBatchHalvesWithoutDroppingOrder(t *testing.T) {
	items := make([]NewsContextRunItem, 73)
	for i := range items {
		items[i].ObjectID = fmt.Sprintf("news-%02d", i)
	}
	second := shrinkNewsContextRetryBatch(items)
	third := shrinkNewsContextRetryBatch(second)
	if len(second) != 37 || len(third) != 19 {
		t.Fatalf("retry sizes = %d, %d; want 37, 19", len(second), len(third))
	}
	for i, item := range third {
		if item.ObjectID != items[i].ObjectID {
			t.Fatalf("retry order changed at %d: %q != %q", i, item.ObjectID, items[i].ObjectID)
		}
	}
}

func TestRetryableNewsContextBatchFailureRequiresStartedNoSubmitBoundary(t *testing.T) {
	processExit := errors.New("process exited (code 1) without submitting result")
	if !retryableNewsContextBatchFailure(processExit, &AgentExecutorOutput{ExitCode: 1}) {
		t.Fatal("started process exit without result must be retryable")
	}
	usageLimitOutput := &AgentExecutorOutput{
		ExitCode:   1,
		StdoutTail: `{"type":"turn.failed","error":{"message":"You've hit your usage limit. Visit the usage page to purchase more credits."}}`,
	}
	if retryableNewsContextBatchFailure(processExit, usageLimitOutput) {
		t.Fatal("provider usage limit must be terminal even when the process submitted no result")
	}
	if retryableNewsContextBatchFailure(processExit, nil) {
		t.Fatal("failure without executor output must remain terminal")
	}
	if retryableNewsContextBatchFailure(errors.New("store news context result: disk full"), &AgentExecutorOutput{ExitCode: 1}) {
		t.Fatal("storage failure must remain terminal")
	}
}

type retryNewsContextExecutor struct {
	pool                    *agentTaskPool
	sizes                   []int
	timeoutAttempts         int
	processExitAttempts     int
	invalidCoverageAttempts int
}

func (e *retryNewsContextExecutor) ExecuteNewsContextAggregation(
	_ context.Context,
	taskID string,
	pack NewsContextAggregationPack,
	_ string,
) (*AgentExecutorOutput, error) {
	e.sizes = append(e.sizes, len(pack.InputNewsEvents)+len(pack.InputThreads))
	if len(e.sizes) <= e.timeoutAttempts {
		return &AgentExecutorOutput{TimedOut: true, ExitCode: -1, Duration: newsContextAgentTimeout},
			fmt.Errorf("execution timed out after %s, no result submitted", newsContextAgentTimeout)
	}
	if len(e.sizes) <= e.processExitAttempts {
		return &AgentExecutorOutput{ExitCode: 1, Duration: time.Second},
			errors.New("process exited (code 1) without submitting result")
	}
	report := NewsContextReport{
		SchemaVersion: NewsContextResultSchemaVersion,
		RunID:         pack.RunID,
		WindowType:    pack.WindowType,
		SearchAudit: []NewsContextSearchAudit{{
			Question: "是否需要额外公开核实", Status: NewsContextResearchCompleted,
			Sources: []string{"https://example.com/public"}, Supported: []string{"本批仅包含测试噪音"},
		}},
	}
	events := pack.InputNewsEvents
	if len(e.sizes) <= e.invalidCoverageAttempts && len(events) > 0 {
		events = events[:len(events)-1]
	}
	for _, event := range events {
		report.ProcessedNewsIDs = append(report.ProcessedNewsIDs, event.ID)
		report.NewsDecisions = append(report.NewsDecisions, NewsContextNewsDecision{
			NewsEventID: event.ID, Disposition: NewsEventContextNoise, Reason: "测试噪音",
		})
	}
	raw, _ := json.Marshal(report)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if _, err := e.pool.submitResult(taskID, AgentTaskTypeNewsEventReview, AgentTaskSubmittedResult{
		OutputType: NewsContextOutputType, ResultSummary: "测试归纳完成", Result: result, Confidence: 1,
	}); err != nil {
		return nil, err
	}
	return &AgentExecutorOutput{ExitCode: 0, Duration: time.Millisecond}, nil
}

func TestExecuteNewsContextBatchRetriesWithSmallerPendingSlice(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		timeoutAttempts         int
		processExitAttempts     int
		invalidCoverageAttempts int
	}{
		{name: "timeout without submission", timeoutAttempts: newsContextTimeoutRetryLimit},
		{name: "process exit without submission", processExitAttempts: newsContextTimeoutRetryLimit},
		{name: "invalid submitted coverage", invalidCoverageAttempts: newsContextTimeoutRetryLimit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testExecuteNewsContextBatchRetriesWithSmallerPendingSlice(
				t, tt.timeoutAttempts, tt.processExitAttempts, tt.invalidCoverageAttempts,
			)
		})
	}
}

func testExecuteNewsContextBatchRetriesWithSmallerPendingSlice(
	t *testing.T,
	timeoutAttempts, processExitAttempts, invalidCoverageAttempts int,
) {
	t.Helper()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI, Name: "news-context-retry",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID, ModelName: "news-context-retry-model", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &model.ID,
	}); err != nil {
		t.Fatalf("bind task model: %v", err)
	}
	executor := &retryNewsContextExecutor{
		pool: svc.agentTaskPool, timeoutAttempts: timeoutAttempts,
		processExitAttempts: processExitAttempts, invalidCoverageAttempts: invalidCoverageAttempts,
	}
	svc.newsContextExecutor = executor

	now := time.Now().Truncate(time.Second)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, WindowStart: now.Add(-time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create context run: %v", err)
	}
	runItems := make([]NewsContextRunItem, 0, 5)
	for i := 0; i < 5; i++ {
		event, err := svc.CreateNewsEvent(ctx, NewsEvent{
			Source: "test", Title: fmt.Sprintf("普通测试新闻 %d", i), Summary: "无重要市场影响",
			Content: "用于验证失败后缩小批次。", EventAt: now.Add(time.Duration(i-10) * time.Minute),
		})
		if err != nil {
			t.Fatalf("create event %d: %v", i, err)
		}
		runItems = append(runItems, NewsContextRunItem{
			RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID, Status: NewsContextRunItemPending,
		})
	}
	if err := svc.store.AddNewsContextRunItems(ctx, runItems); err != nil {
		t.Fatalf("add run items: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list run items: %v", err)
	}
	if err := svc.executeNewsContextBatchWithRetry(ctx, &run, defaultNewsContextConfig(), items); err != nil {
		t.Fatalf("execute retry batch: %v", err)
	}
	if got := fmt.Sprint(executor.sizes); got != "[5 3 2]" {
		t.Fatalf("attempt sizes = %s, want [5 3 2]", got)
	}
	pending, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Status: NewsContextRunItemPending})
	if err != nil {
		t.Fatalf("count pending items: %v", err)
	}
	completed, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Status: NewsContextRunItemCompleted})
	if err != nil {
		t.Fatalf("count completed items: %v", err)
	}
	if pending != 3 || completed != 2 {
		t.Fatalf("item counts pending=%d completed=%d, want 3 and 2", pending, completed)
	}
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
