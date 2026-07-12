package stockv2

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewsContextCleanupCutoffCannotBypassGrace(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.Local)
	want := now.Add(-24 * time.Hour)
	for _, requested := range []string{"", now.Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano)} {
		got, err := newsContextCleanupCutoff(now, 24*3600, requested)
		if err != nil || !got.Equal(want) {
			t.Fatalf("requested=%q cutoff=%v err=%v, want %v", requested, got, err, want)
		}
	}
	older := now.Add(-48 * time.Hour)
	got, err := newsContextCleanupCutoff(now, 24*3600, older.Format(time.RFC3339Nano))
	if err != nil || !got.Equal(older) {
		t.Fatalf("older cutoff=%v err=%v, want %v", got, err, older)
	}
	if _, err := newsContextCleanupCutoff(now, 24*3600, "not-a-time"); err == nil {
		t.Fatal("invalid manual cutoff must be rejected")
	}
	if defaultNewsContextConfig().AutoCleanupEnabled {
		t.Fatal("automatic cleanup must remain disabled by default")
	}
}

func TestNewsContextResearchFailureDefersNewsAndPersistsReason(t *testing.T) {
	for _, tt := range []struct {
		name   string
		audit  NewsContextSearchAudit
		status string
	}{
		{name: "failed", status: NewsContextResearchFailed, audit: NewsContextSearchAudit{Question: "核实订单", Status: "failed", FailureReason: "search failed"}},
		{name: "unavailable", status: NewsContextResearchUnavailable, audit: NewsContextSearchAudit{Question: "核实订单", Status: "unavailable", FailureReason: "search unavailable"}},
		{name: "unresolved", status: NewsContextResearchUnresolved, audit: NewsContextSearchAudit{Question: "核实订单", Status: "verified", Sources: []string{"https://example.com/public"}, Unresolved: []string{"交付尚无官方确认"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			seed := seedNewsContextRetentionEvent(t, svc, ctx, []NewsContextSearchAudit{tt.audit}, nil, nil, "support")
			if seed.apply.DeferredCount != 1 || seed.apply.CoveredCount != 0 {
				t.Fatalf("apply result=%+v, want research-gated deferral", seed.apply)
			}
			var status, reason string
			if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,''), COALESCE(protected_reason,'')
				FROM stockv2_news_events WHERE id=?`, seed.event.ID).Scan(&status, &reason); err != nil {
				t.Fatalf("read research gate: %v", err)
			}
			if status != NewsEventContextDeferred || reason == "" {
				t.Fatalf("event status=%q reason=%q, want durable protection", status, reason)
			}
			versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{ThreadID: seed.thread.ID, Limit: 10})
			if err != nil || len(versions) != 1 || versions[0].ResearchStatus != tt.status {
				t.Fatalf("versions=%+v err=%v, want research status %q", versions, err, tt.status)
			}
			candidates, err := svc.store.ListNewsEventsForContextCleanup(ctx, time.Now().Add(time.Hour), "", 10)
			if err != nil || len(candidates) != 0 {
				t.Fatalf("research-gated cleanup candidates=%+v err=%v", candidates, err)
			}
		})
	}
}

func TestNewsContextCleanupRequiresReviewedDailyConclusion(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	// The hourly evidence deliberately contains an old conflict. A later clean
	// reviewed daily conclusion may resolve it without erasing the audit trail.
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), []string{"早期来源口径冲突"}, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
	if err != nil || eligible || !strings.Contains(reason, "每日") {
		t.Fatalf("hourly-only eligible=%v reason=%q err=%v", eligible, reason, err)
	}

	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)
	eligible, reason, err = svc.newsContextCleanupEligible(ctx, candidate)
	if err != nil || !eligible {
		t.Fatalf("complete daily gate eligible=%v reason=%q err=%v", eligible, reason, err)
	}
	released, err := svc.compactNewsContextEvent(ctx, seed.event)
	if err != nil || released == 0 {
		t.Fatalf("compact fully eligible news released=%d err=%v", released, err)
	}
	stored, err := svc.store.GetNewsEvent(ctx, seed.event.ID)
	if err != nil || stored.Content != "" || stored.Summary != "" || stored.URL != "" || stored.Title == "" {
		t.Fatalf("compacted receipt=%+v err=%v", stored, err)
	}
}

func TestNewsContextCleanupProtectsUnresolvedDailyOrCurrentTheme(t *testing.T) {
	for _, tt := range []struct {
		name             string
		dailyCounter     []string
		dailyQuestions   []string
		newerCurrentOnly bool
	}{
		{name: "daily conflict", dailyCounter: []string{"官方与媒体口径冲突"}},
		{name: "daily open question", dailyQuestions: []string{"仍需原文确认监管范围"}},
		{name: "newer current question", newerCurrentOnly: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
			candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
			daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, tt.dailyCounter, tt.dailyQuestions, NewsContextResearchCompleted)
			if tt.newerCurrentOnly {
				seedNewerUnresolvedCurrentVersion(t, svc, ctx, seed.thread.ID, daily)
			}
			eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
			if err != nil || eligible || reason == "" {
				t.Fatalf("eligible=%v reason=%q err=%v, want protected original", eligible, reason, err)
			}
		})
	}
}

type newsContextRetentionSeed struct {
	event  NewsEvent
	thread NewsThread
	apply  NewsContextBatchApplyResult
}

func seedNewsContextRetentionEvent(t *testing.T, svc *Service, ctx context.Context, audits []NewsContextSearchAudit, counterEvidence, openQuestions []string, disposition string) newsContextRetentionSeed {
	t.Helper()
	now := time.Now()
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "半导体设备订单", Summary: "订单变化摘要", Content: "需要经过安全门的新闻原文。",
		URL: "https://example.com/news?tracking=removed", EventAt: now.Add(-2 * time.Hour), LinkStatus: NewsEventLinkStatusNoCandidate,
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, WindowStart: now.Add(-3 * time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create hourly run: %v", err)
	}
	const agentRunID = "retention-hourly-agent"
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID, Status: NewsContextRunItemPending,
	}}); err != nil {
		t.Fatalf("add hourly item: %v", err)
	}
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, agentRunID, []string{event.ID}); err != nil {
		t.Fatalf("mark hourly item: %v", err)
	}
	report := NewsContextReport{
		SchemaVersion: NewsContextResultSchemaVersion, RunID: run.ID, WindowType: run.WindowType,
		ProcessedNewsIDs: []string{event.ID}, SearchAudit: audits,
		NewsDecisions: []NewsContextNewsDecision{{NewsEventID: event.ID, Disposition: disposition}},
		ThreadChanges: []NewsContextThreadChange{{
			Action: "create", Title: "半导体设备景气", CoreThesis: "订单改善推动景气", Stage: NewsThreadStageEmerging,
			Confidence: 0.8, Facts: []string{"订单改善"}, CounterEvidence: counterEvidence, OpenQuestions: openQuestions,
			EvidenceNewsIDs: []string{event.ID},
		}},
	}
	apply, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, agentRunID, run.WindowType, report)
	if err != nil {
		t.Fatalf("apply hourly report: %v", err)
	}
	threads, err := svc.store.ListNewsThreads(ctx, NewsThreadListFilter{Limit: 10})
	if err != nil || len(threads) != 1 {
		t.Fatalf("threads=%+v err=%v", threads, err)
	}
	return newsContextRetentionSeed{event: event, thread: threads[0], apply: apply}
}

func verifiedRetentionAudit() []NewsContextSearchAudit {
	return []NewsContextSearchAudit{{Question: "核实订单", Status: "verified", Sources: []string{"https://example.com/public"}}}
}

func retentionCleanupCandidate(t *testing.T, svc *Service, ctx context.Context, eventID string) NewsContextCleanupCandidate {
	t.Helper()
	items, err := svc.store.ListNewsEventsForContextCleanup(ctx, time.Now().Add(time.Hour), "", 10)
	if err != nil {
		t.Fatalf("list cleanup candidates: %v", err)
	}
	for _, item := range items {
		if item.Event.ID == eventID {
			return item
		}
	}
	t.Fatalf("cleanup candidate %s not found: %+v", eventID, items)
	return NewsContextCleanupCandidate{}
}

func seedReviewedDailyRetentionVersion(t *testing.T, svc *Service, ctx context.Context, original NewsThread, coveredAt time.Time, counterEvidence, openQuestions []string, researchStatus string) NewsThreadVersion {
	t.Helper()
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusCompleted, WindowStart: coveredAt.Add(-12 * time.Hour), WindowEnd: coveredAt.Add(12 * time.Hour),
		ReviewStatus: NewsContextReviewCompleted, CleanupStatus: NewsContextCleanupPending,
		StartedAt: coveredAt.Add(time.Minute), FinishedAt: coveredAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create reviewed daily run: %v", err)
	}
	version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: original.ID, RunID: run.ID, AgentRunID: "retention-daily-agent", WindowType: NewsContextWindowDaily,
		VersionNo: original.CurrentVersion + 1, Title: original.Title, CoreThesis: original.CoreThesis, Stage: original.Stage,
		Confidence: original.Confidence, Facts: original.Facts, CounterEvidence: counterEvidence, OpenQuestions: openQuestions,
		ResearchStatus: researchStatus, ReviewStatus: NewsContextReviewCompleted, CreatedAt: coveredAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create reviewed daily version: %v", err)
	}
	original.CurrentVersion = version.VersionNo
	original.CurrentVersionID = version.ID
	original.CounterEvidence = counterEvidence
	original.OpenQuestions = openQuestions
	original.ReviewStatus = NewsContextReviewCompleted
	original.LastReviewedAt = version.CreatedAt
	if _, err := svc.store.UpdateNewsThread(ctx, original); err != nil {
		t.Fatalf("update current thread to daily version: %v", err)
	}
	return version
}

func seedNewerUnresolvedCurrentVersion(t *testing.T, svc *Service, ctx context.Context, threadID string, daily NewsThreadVersion) {
	t.Helper()
	thread, err := svc.store.GetNewsThread(ctx, threadID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "later-hourly-run", AgentRunID: "later-hourly-agent", WindowType: NewsContextWindowHourly,
		VersionNo: daily.VersionNo + 1, Title: thread.Title, CoreThesis: thread.CoreThesis, Stage: thread.Stage,
		OpenQuestions: []string{"最新变化仍需原文确认"}, ResearchStatus: NewsContextResearchCompleted,
		ReviewStatus: NewsContextReviewNotRequired, CreatedAt: daily.CreatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create newer current version: %v", err)
	}
	thread.CurrentVersion = version.VersionNo
	thread.CurrentVersionID = version.ID
	thread.OpenQuestions = version.OpenQuestions
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatalf("update newer current thread: %v", err)
	}
}

func configureRetentionIndexes(t *testing.T, svc *Service, ctx context.Context, threadID string, daily NewsThreadVersion) {
	t.Helper()
	const modelID = "retention-embedding-model"
	if _, err := svc.store.UpsertEmbeddingConfig(ctx, EmbeddingConfig{ID: EmbeddingConfigIDDefault, EmbeddingModelID: modelID, Enabled: true}); err != nil {
		t.Fatalf("configure embedding: %v", err)
	}
	thread, err := svc.store.GetNewsThread(ctx, threadID)
	if err != nil {
		t.Fatalf("get indexed thread: %v", err)
	}
	for _, source := range []embeddingAssetSource{
		{ObjectType: EmbeddingObjectNewsThread, ObjectID: thread.ID, Text: NewsThreadEmbeddingText(thread)},
		{ObjectType: EmbeddingObjectNewsThreadVersion, ObjectID: daily.ID, Text: NewsThreadVersionEmbeddingText(daily)},
	} {
		asset := EmbeddingAsset{ObjectType: source.ObjectType, ObjectID: source.ObjectID, TextHash: hashEmbeddingText(source.Text),
			ModelID: modelID, EmbeddingDimensions: 2, VectorRef: "retention-vector-" + source.ObjectID, Status: EmbeddingAssetStatusReady}
		if err := svc.store.UpsertEmbeddingVector(ctx, asset, []float64{1, 0}); err != nil {
			t.Fatalf("save embedding vector: %v", err)
		}
		if _, err := svc.store.UpsertEmbeddingAsset(ctx, asset); err != nil {
			t.Fatalf("save embedding asset: %v", err)
		}
	}
	svc.agentMCPServer = &http.Server{}
	svc.agentMCPURL = "http://127.0.0.1/retention-test-mcp"
}
