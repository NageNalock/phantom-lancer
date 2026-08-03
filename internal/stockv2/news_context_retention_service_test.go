package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewsContextUnavailableProtectionReasonDoesNotClaimGlobalSearchOutage(t *testing.T) {
	reason := newsContextVersionProtectionReason(NewsThreadVersion{ResearchStatus: NewsContextResearchUnavailable})
	if reason == "" || strings.Contains(reason, "搜索不可用") || !strings.Contains(reason, "尚未完成公开资料核实") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestNewsContextCleanupDoesNotStartDuringAggregation(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if !svc.tryStartNewsContextRun() {
		t.Fatal("reserve aggregation execution slot")
	}
	defer svc.finishNewsContextRun()

	if _, err := svc.StartNewsContextCleanupRun(ctx, RequestStartNewsContextCleanup{RequestedBy: "test"}); !errors.Is(err, ErrNewsContextAlreadyRunning) {
		t.Fatalf("start cleanup during aggregation error=%v, want aggregation running", err)
	}
	if running, err := svc.store.HasRunningNewsContextCleanupRun(ctx); err != nil || running {
		t.Fatalf("cleanup persisted while aggregation runs: running=%v err=%v", running, err)
	}
}

func TestNewsContextCleanupGateIgnoresPendingNewsAfterCutoff(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "等待期内的新增消息", EventAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create recent pending event: %v", err)
	}

	gate, err := svc.newsContextCleanupGate(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("read cleanup gate: %v", err)
	}
	if gate.Blocked || gate.BacklogCount != 0 || gate.PendingCount != 0 {
		t.Fatalf("recent pending event blocked old-news cleanup: %+v", gate)
	}
}

func TestNewsContextCleanupGateBlocksPendingNewsBeforeCutoff(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	eventAt := now.Add(-48 * time.Hour)
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "清理截止点前的未归纳消息", EventAt: eventAt,
	}); err != nil {
		t.Fatalf("create old pending event: %v", err)
	}

	gate, err := svc.newsContextCleanupGate(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("read cleanup gate: %v", err)
	}
	if !gate.Blocked || gate.BacklogCount != 1 || gate.PendingCount != 1 {
		t.Fatalf("old pending event did not block cleanup: %+v", gate)
	}
	if gate.EarliestAt.IsZero() || gate.LatestAt.IsZero() || gate.Reason == "" {
		t.Fatalf("cleanup gate lacks observable backlog details: %+v", gate)
	}
}

func TestNewsContextCleanupStartsWithPendingNewsInsideGracePeriod(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "cleanup-recent-news-embedding")
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "等待期内不阻塞清理的消息", EventAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create recent pending event: %v", err)
	}

	run, err := svc.StartNewsContextCleanupRun(ctx, RequestStartNewsContextCleanup{RequestedBy: "test"})
	if err != nil {
		t.Fatalf("start cleanup with only recent pending news: %v", err)
	}
	if run.ID == "" || run.Cutoff.IsZero() {
		t.Fatalf("cleanup run lacks durable cutoff: %+v", run)
	}
}

func TestDueNewsContextCleanupThrottlesBlockedAttempts(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	cfg, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = true
	cfg.AutoCleanupEnabled = true
	cfg.LastCleanupAt = now.Add(-2 * time.Hour)
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: NewsContextWindowHourly,
		CutoffAt: now, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.startDueNewsContextCleanup(ctx, now); !errors.Is(err, ErrNewsContextReviewIncomplete) {
		t.Fatalf("first cleanup attempt error=%v, want review incomplete", err)
	}
	if err := svc.startDueNewsContextCleanup(ctx, now.Add(time.Minute)); err != nil {
		t.Fatalf("throttled cleanup attempt error=%v", err)
	}
	if count, err := svc.store.CountNewsContextCleanupRuns(ctx, NewsContextCleanupRunListFilter{}); err != nil || count != 0 {
		t.Fatalf("blocked cleanup runs=%d err=%v", count, err)
	}
}

func TestNewsContextCleanupStartRejectsMissingSafetyValidation(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "cleanup-start-gate-embedding")
	now := time.Now()
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "缺少每日复核的待清理消息", EventAt: now.Add(-48 * time.Hour),
		LinkStatus: NewsEventLinkStatusNoCandidate,
	})
	if err != nil {
		t.Fatalf("create cleanup event: %v", err)
	}
	if err := svc.store.MarkNewsEventContext(ctx, event.ID, NewsEventContextCovered,
		"missing-review-run", "", now.Add(-47*time.Hour)); err != nil {
		t.Fatalf("mark cleanup event covered: %v", err)
	}
	if _, err := svc.StartNewsContextCleanupRun(ctx, RequestStartNewsContextCleanup{
		RequestedBy: "test",
	}); !errors.Is(err, ErrNewsContextPrerequisite) {
		t.Fatalf("start cleanup without daily review error=%v", err)
	}
	if running, err := svc.store.HasRunningNewsContextCleanupRun(ctx); err != nil || running {
		t.Fatalf("cleanup persisted despite failed safety validation: running=%v err=%v", running, err)
	}
}

func TestNewsContextCleanupRechecksThemeChangeBeforeCompaction(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.WithValue(context.Background(), newsContextMCPVerificationCacheKey{}, newsContextMCPVerificationCache{})
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt,
		[]string{"已写入每日主题结论的反证"}, []string{"已写入每日主题结论的后续验证问题"}, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)
	if eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate); err != nil || !eligible {
		t.Fatalf("initial cleanup check eligible=%v reason=%q err=%v", eligible, reason, err)
	}

	type result struct {
		released  int64
		compacted bool
		reason    string
		err       error
	}
	svc.newsContextMu.Lock()
	locked := true
	defer func() {
		if locked {
			svc.newsContextMu.Unlock()
		}
	}()
	started := make(chan struct{})
	done := make(chan result, 1)
	go func() {
		close(started)
		released, compacted, reason, err := svc.compactNewsContextCandidate(ctx, candidate)
		done <- result{released: released, compacted: compacted, reason: reason, err: err}
	}()
	<-started
	seedNewerUnresolvedCurrentVersion(t, svc, ctx, seed.thread.ID, daily)
	svc.newsContextMu.Unlock()
	locked = false

	select {
	case got := <-done:
		if got.err != nil || got.compacted || got.released != 0 || got.reason == "" {
			t.Fatalf("final cleanup result=%+v, want protected changed theme", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup final check did not release the execution slot")
	}
	stored, err := svc.store.GetNewsEvent(ctx, seed.event.ID)
	if err != nil || stored.Content == "" || stored.Summary == "" {
		t.Fatalf("news body was compacted after theme changed: event=%+v err=%v", stored, err)
	}
	var protectedReason string
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(protected_reason,'') FROM stockv2_news_events WHERE id=?`, seed.event.ID).Scan(&protectedReason); err != nil || protectedReason == "" {
		t.Fatalf("changed news was not protected: reason=%q err=%v", protectedReason, err)
	}
}

func TestNewsContextCleanupFinalCheckUsesRunMCPVerificationCache(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.WithValue(context.Background(), newsContextMCPVerificationCacheKey{}, newsContextMCPVerificationCache{})
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)
	if eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate); err != nil || !eligible {
		t.Fatalf("initial cleanup check eligible=%v reason=%q err=%v", eligible, reason, err)
	}
	if err := svc.agentMCPServer.Shutdown(ctx); err != nil {
		t.Fatalf("stop MCP transport after initial check: %v", err)
	}
	released, compacted, reason, err := svc.compactNewsContextCandidate(ctx, candidate)
	if err != nil || !compacted || released == 0 || reason != "" {
		t.Fatalf("cached final cleanup released=%d compacted=%v reason=%q err=%v", released, compacted, reason, err)
	}
}

func TestNewsContextCleanupDefersWhileEmbeddingMaintenanceUsesNewsBody(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.WithValue(context.Background(), newsContextMCPVerificationCacheKey{}, newsContextMCPVerificationCache{})
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)
	if eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate); err != nil || !eligible {
		t.Fatalf("initial cleanup check eligible=%v reason=%q err=%v", eligible, reason, err)
	}
	if !svc.beginEmbeddingMaintenance() {
		t.Fatal("reserve embedding maintenance slot")
	}
	released, compacted, reason, err := svc.compactNewsContextCandidate(ctx, candidate)
	if err != nil || compacted || released != 0 || !strings.Contains(reason, "向量") {
		t.Fatalf("cleanup during embedding maintenance released=%d compacted=%v reason=%q err=%v", released, compacted, reason, err)
	}
	if !svc.tryStartNewsContextRun() {
		t.Fatal("cleanup did not release aggregation execution slot")
	}
	svc.finishNewsContextRun()
	svc.endEmbeddingMaintenance()
	stored, err := svc.store.GetNewsEvent(ctx, seed.event.ID)
	if err != nil || stored.Content == "" {
		t.Fatalf("embedding maintenance race compacted news: event=%+v err=%v", stored, err)
	}
	cfg, err := svc.embeddingConfigOrDefault(ctx)
	if err != nil {
		t.Fatalf("read embedding config: %v", err)
	}
	asset, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, daily.ID, cfg.EmbeddingModelID)
	if err != nil {
		t.Fatalf("embedding asset removed while maintenance owns slot: %v", err)
	}
	if ready, err := svc.store.HasEmbeddingVector(ctx, asset.VectorRef); err != nil || !ready {
		t.Fatalf("embedding vector removed while maintenance owns slot: ready=%v err=%v", ready, err)
	}
}

func TestNewsContextCleanupChecksEverySafetyGateBeforeDeletingAnything(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	first := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, first.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)

	secondEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "第二条待清理消息", Summary: "第二条摘要", Content: "第二条原文",
		EventAt: first.Event.EventAt.Add(time.Minute), LinkStatus: NewsEventLinkStatusNoCandidate,
	})
	if err != nil {
		t.Fatalf("create second cleanup event: %v", err)
	}
	if err := svc.store.MarkNewsEventContext(ctx, secondEvent.ID, NewsEventContextCovered,
		first.ContextRunID, "", first.ContextCoveredAt); err != nil {
		t.Fatalf("mark second cleanup event covered: %v", err)
	}
	missingIndexVersion, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: seed.thread.ID, RunID: first.ContextRunID, AgentRunID: "retention-missing-index",
		WindowType: NewsContextWindowHourly, VersionNo: daily.VersionNo + 1,
		Title: seed.thread.Title, CoreThesis: seed.thread.CoreThesis, Stage: seed.thread.Stage,
		MaterialChange: true, ResearchStatus: NewsContextResearchCompleted,
		EffectiveAt: first.ContextCoveredAt.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create unindexed evidence version: %v", err)
	}
	if _, err := svc.store.CreateNewsThreadEvidence(ctx, NewsThreadEvidence{
		ThreadID: seed.thread.ID, VersionID: missingIndexVersion.ID, RunID: first.ContextRunID,
		NewsEventID: secondEvent.ID, Title: secondEvent.Title, Summary: secondEvent.Summary,
		Relation: "support", EventAt: secondEvent.EventAt,
	}); err != nil {
		t.Fatalf("create second cleanup evidence: %v", err)
	}

	run, err := svc.store.CreateNewsContextCleanupRun(ctx, NewsContextCleanupRun{
		Status: NewsContextCleanupRunning, Phase: "checking_gates",
		Cutoff: time.Now().Add(time.Hour), RequestedBy: "test",
	})
	if err != nil {
		t.Fatalf("create cleanup run: %v", err)
	}
	svc.executeNewsContextCleanup(ctx, run.ID)
	completed, err := svc.store.GetNewsContextCleanupRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get failed cleanup run: %v", err)
	}
	if completed.Status != NewsContextCleanupFailed || completed.CompactedCount != 0 || completed.ErrorMessage == "" {
		t.Fatalf("cleanup run=%+v, want failed preflight without compaction", completed)
	}
	for _, eventID := range []string{seed.event.ID, secondEvent.ID} {
		stored, err := svc.store.GetNewsEvent(ctx, eventID)
		if err != nil || stored.Content == "" || stored.Summary == "" {
			t.Fatalf("cleanup safety failure deleted event %s: event=%+v err=%v", eventID, stored, err)
		}
	}
}

func TestCompactNewsEventRollbackKeepsLinkCandidates(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		NewsEventID: seed.event.ID, Symbol: "TEST", MatchMethod: NewsLinkMatchExactSymbol,
	})
	if err != nil {
		t.Fatalf("seed completed link candidate: %v", err)
	}
	blocker, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "压缩状态占位", LinkStatus: NewsEventLinkStatusNoCandidate})
	if err != nil {
		t.Fatalf("create compacted blocker: %v", err)
	}
	if _, err := svc.store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events SET context_status=? WHERE id=?`, NewsEventContextCompacted, blocker.ID); err != nil {
		t.Fatalf("mark compacted blocker: %v", err)
	}
	if _, err := svc.store.marketDB.db.ExecContext(ctx, `CREATE UNIQUE INDEX fail_test_news_compaction ON stockv2_news_events(context_status)`); err != nil {
		t.Fatalf("create compaction failure index: %v", err)
	}
	defer svc.store.marketDB.db.ExecContext(context.Background(), `DROP INDEX IF EXISTS fail_test_news_compaction`)

	if _, err := svc.store.CompactNewsEvent(ctx, seed.event.ID); err == nil {
		t.Fatal("forced compact transaction unexpectedly succeeded")
	}
	if _, err := svc.store.GetNewsLinkCandidate(ctx, candidate.ID); err != nil {
		t.Fatalf("rolled-back compact lost link candidate: %v", err)
	}
	stored, err := svc.store.GetNewsEvent(ctx, seed.event.ID)
	if err != nil || stored.Content == "" || stored.Summary == "" {
		t.Fatalf("rolled-back compact lost news body: event=%+v err=%v", stored, err)
	}
}

func TestNewsLinkCandidateUpsertRejectsCompactedEvent(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "已压缩消息", Content: "原文", LinkStatus: NewsEventLinkStatusNoCandidate,
	})
	if err != nil {
		t.Fatalf("create compactable event: %v", err)
	}
	if _, err := svc.store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events
		SET context_status=?, context_covered_at=? WHERE id=?`, NewsEventContextCovered, time.Now().Add(-time.Hour), event.ID); err != nil {
		t.Fatalf("mark event covered: %v", err)
	}
	if _, err := svc.store.CompactNewsEvent(ctx, event.ID); err != nil {
		t.Fatalf("compact event: %v", err)
	}
	item := NewsLinkCandidate{NewsEventID: event.ID, Symbol: "TEST", MatchMethod: NewsLinkMatchExactSymbol}
	if _, err := svc.store.UpsertNewsLinkCandidate(ctx, item); !errors.Is(err, ErrInvalidNewsLinkCandidate) {
		t.Fatalf("single upsert on compacted event error=%v", err)
	}
	active, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "未压缩消息"})
	if err != nil {
		t.Fatalf("create active event: %v", err)
	}
	valid := NewsLinkCandidate{NewsEventID: active.ID, Symbol: "VALID", MatchMethod: NewsLinkMatchExactSymbol}
	if err := svc.store.UpsertNewsLinkCandidates(ctx, []NewsLinkCandidate{valid, item}); !errors.Is(err, ErrInvalidNewsLinkCandidate) {
		t.Fatalf("bulk upsert on compacted event error=%v", err)
	}
	if items, err := svc.store.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{NewsEventID: event.ID, Limit: 10}); err != nil || len(items) != 0 {
		t.Fatalf("compacted event regained link candidates: items=%+v err=%v", items, err)
	}
	if items, err := svc.store.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{NewsEventID: active.ID, Limit: 10}); err != nil || len(items) != 0 {
		t.Fatalf("mixed batch was not rolled back: items=%+v err=%v", items, err)
	}
}

func TestNoiseNewsContextCleanupRejectsUnrelatedLaterDaily(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowFourHour, day.Add(8*time.Hour), day.Add(12*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
	seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day.Add(24*time.Hour), day.Add(48*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewCompleted)

	ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour)))
	if err != nil || ready || !strings.Contains(reason, "每日") {
		t.Fatalf("unrelated daily ready=%v reason=%q err=%v", ready, reason, err)
	}
}

func TestNoiseNewsContextCleanupRequiresCompleteRealtimeHierarchy(t *testing.T) {
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	t.Run("missing four-hour", func(t *testing.T) {
		svc, cleanup := newStrategyTestService(t)
		defer cleanup()
		ctx := context.Background()
		source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowHourly, day.Add(9*time.Hour), day.Add(10*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
		seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, day.Add(24*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewCompleted)

		ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour+time.Minute)))
		if err != nil || ready || !strings.Contains(reason, "四小时") {
			t.Fatalf("missing four-hour ready=%v reason=%q err=%v", ready, reason, err)
		}
	})
	t.Run("full chain", func(t *testing.T) {
		svc, cleanup := newStrategyTestService(t)
		defer cleanup()
		ctx := context.Background()
		source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowHourly, day.Add(9*time.Hour), day.Add(10*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
		seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowFourHour, day.Add(8*time.Hour), day.Add(12*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
		seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, day.Add(24*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewCompleted)

		ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour+time.Minute)))
		if err != nil || !ready || reason != "" {
			t.Fatalf("full hierarchy ready=%v reason=%q err=%v", ready, reason, err)
		}
	})
}

func TestNoiseNewsContextCleanupAllowsDirectReviewedDaily(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, day.Add(24*time.Hour), NewsContextTriggerManual, NewsContextRunStatusCompleted, NewsContextReviewCompleted)

	ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour)))
	if err != nil || !ready || reason != "" {
		t.Fatalf("direct daily ready=%v reason=%q err=%v", ready, reason, err)
	}
}

func TestNoiseNewsContextCleanupAllowsPersistedDeferredCrossWindowItem(t *testing.T) {
	for _, disposition := range []string{NewsEventContextNoise, "duplicate"} {
		t.Run(disposition, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
			source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowFourHour, day.Add(8*time.Hour), day.Add(12*time.Hour), NewsContextTriggerRetry, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
			seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, day.Add(24*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewCompleted)
			candidate := NewsContextCleanupCandidate{
				Event:        NewsEvent{ID: "deferred-cross-window-news", EventAt: day.Add(-time.Hour)},
				ContextRunID: source.ID,
			}
			if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
				RunID: source.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: candidate.Event.ID,
				Status: NewsContextRunItemCompleted, Disposition: disposition, SourceAt: candidate.Event.EventAt,
			}}); err != nil {
				t.Fatalf("save deferred discarded item: %v", err)
			}

			ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, candidate)
			if err != nil || !ready || reason != "" {
				t.Fatalf("deferred cross-window ready=%v reason=%q err=%v", ready, reason, err)
			}
		})
	}
}

func TestNoiseNewsContextCleanupRejectsUnprovenCrossWindowItem(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowFourHour, day.Add(8*time.Hour), day.Add(12*time.Hour), NewsContextTriggerRetry, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
	seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, day.Add(24*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewCompleted)

	ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, NewsContextCleanupCandidate{
		Event: NewsEvent{ID: "unproven-cross-window-news", EventAt: day.Add(-time.Hour)}, ContextRunID: source.ID,
	})
	if err != nil || ready || !strings.Contains(reason, "时间窗口") {
		t.Fatalf("unproven cross-window ready=%v reason=%q err=%v", ready, reason, err)
	}
}

func TestNoiseNewsContextCleanupRejectsFailedOrUnreviewedRuns(t *testing.T) {
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	for _, tt := range []struct {
		name         string
		sourceStatus string
		dailyStatus  string
		dailyReview  string
	}{
		{name: "failed source", sourceStatus: NewsContextRunStatusFailed, dailyStatus: NewsContextRunStatusCompleted, dailyReview: NewsContextReviewCompleted},
		{name: "failed daily", sourceStatus: NewsContextRunStatusCompleted, dailyStatus: NewsContextRunStatusFailed, dailyReview: NewsContextReviewCompleted},
		{name: "unreviewed daily", sourceStatus: NewsContextRunStatusCompleted, dailyStatus: NewsContextRunStatusCompleted, dailyReview: NewsContextReviewPending},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowFourHour, day.Add(8*time.Hour), day.Add(12*time.Hour), NewsContextTriggerScheduled, tt.sourceStatus, NewsContextReviewNotRequired)
			seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, day.Add(24*time.Hour), NewsContextTriggerScheduled, tt.dailyStatus, tt.dailyReview)

			ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour)))
			if err != nil || ready || reason == "" {
				t.Fatalf("failed gate ready=%v reason=%q err=%v", ready, reason, err)
			}
		})
	}
}

func TestNoiseNewsContextCleanupAllowsLinkedReusedHistoricalHierarchy(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	cutoff := day.Add(10 * time.Hour)
	finalReview := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, cutoff, cutoff.Add(time.Hour), NewsContextTriggerRetry, NewsContextRunStatusCompleted, NewsContextReviewCompleted)
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusCompleted, Phase: "completed", RangeStartAt: day,
		CutoffAt: cutoff, FinalReviewRunID: finalReview.ID, CompletedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create completed backfill: %v", err)
	}
	source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowHourly, day.Add(9*time.Hour), cutoff, NewsContextTriggerRetry, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
	fourHour := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowFourHour, day.Add(8*time.Hour), cutoff, NewsContextTriggerScheduled, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
	daily := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, cutoff, NewsContextTriggerBackfill, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
	for _, run := range []NewsContextRun{source, fourHour, daily} {
		if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
			t.Fatalf("link %s to backfill: %v", run.WindowType, err)
		}
	}

	ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour+time.Minute)))
	if err != nil || !ready || reason != "" {
		t.Fatalf("partial historical hierarchy ready=%v reason=%q err=%v", ready, reason, err)
	}
}

func TestNoiseNewsContextCleanupHistoricalChainRequiresFinalCurrentReview(t *testing.T) {
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	cutoff := day.Add(10 * time.Hour)
	for _, tt := range []struct {
		name   string
		status string
		review string
	}{
		{name: "failed", status: NewsContextRunStatusFailed, review: NewsContextReviewCompleted},
		{name: "unreviewed", status: NewsContextRunStatusCompleted, review: NewsContextReviewPending},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			finalReview := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, cutoff, cutoff.Add(time.Hour), NewsContextTriggerManual, tt.status, tt.review)
			backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
				Status: NewsContextBackfillStatusCompleted, Phase: "completed", RangeStartAt: day,
				CutoffAt: cutoff, FinalReviewRunID: finalReview.ID, CompletedAt: time.Now(),
			})
			if err != nil {
				t.Fatalf("create completed backfill: %v", err)
			}
			source := seedNoiseCleanupRun(t, svc, ctx, NewsContextWindowDaily, day, cutoff, NewsContextTriggerBackfill, NewsContextRunStatusCompleted, NewsContextReviewNotRequired)
			if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, source.ID); err != nil {
				t.Fatalf("link historical daily: %v", err)
			}

			ready, reason, err := svc.noiseNewsContextCleanupReady(ctx, noiseCleanupCandidate(source, day.Add(9*time.Hour)))
			if err != nil || ready || !strings.Contains(reason, "最终") {
				t.Fatalf("historical final gate ready=%v reason=%q err=%v", ready, reason, err)
			}
		})
	}
}

func seedNoiseCleanupRun(t *testing.T, svc *Service, ctx context.Context, windowType string, start, end time.Time, trigger, status, review string) NewsContextRun {
	t.Helper()
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: windowType, TriggerType: trigger, Status: status, WindowStart: start,
		WindowEnd: end, ReviewStatus: review, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create %s cleanup run: %v", windowType, err)
	}
	return run
}

func noiseCleanupCandidate(source NewsContextRun, eventAt time.Time) NewsContextCleanupCandidate {
	return NewsContextCleanupCandidate{Event: NewsEvent{EventAt: eventAt}, ContextRunID: source.ID}
}

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

func TestNewsContextResearchFailureProtectsVersionWithoutDeferringWholeBatch(t *testing.T) {
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
			seed := seedNewsContextRetentionEventWithResearchStatus(t, svc, ctx, []NewsContextSearchAudit{tt.audit}, nil, nil, "support", tt.status)
			if seed.apply.DeferredCount != 0 || seed.apply.CoveredCount != 1 {
				t.Fatalf("apply result=%+v, want covered news with version-level protection", seed.apply)
			}
			var status, reason string
			if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,''), COALESCE(protected_reason,'')
				FROM stockv2_news_events WHERE id=?`, seed.event.ID).Scan(&status, &reason); err != nil {
				t.Fatalf("read research gate: %v", err)
			}
			if status != NewsEventContextCovered || reason != "" {
				t.Fatalf("event status=%q reason=%q, want covered news without batch-wide deferral", status, reason)
			}
			versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{ThreadID: seed.thread.ID, Limit: 10})
			if err != nil || len(versions) != 1 || versions[0].ResearchStatus != tt.status {
				t.Fatalf("versions=%+v err=%v, want research status %q", versions, err, tt.status)
			}
			if protection := newsContextVersionProtectionReason(versions[0]); protection == "" {
				t.Fatalf("research status %q did not protect its theme version", tt.status)
			}
			candidates, err := svc.store.ListNewsEventsForContextCleanup(ctx, time.Now().Add(time.Hour), "", 10)
			if err != nil || len(candidates) != 1 {
				t.Fatalf("covered cleanup candidates=%+v err=%v", candidates, err)
			}
		})
	}
}

func TestNewsContextResearchFailureDoesNotTaintUnrelatedVerifiedChange(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	seed := seedNewsContextRetentionEventWithResearchStatus(t, svc, ctx, []NewsContextSearchAudit{{
		Question: "核实另一主题", Status: NewsContextResearchFailed, FailureReason: "search failed",
	}}, nil, nil, "support", NewsContextResearchCompleted)
	versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{ThreadID: seed.thread.ID, Limit: 10})
	if err != nil || len(versions) != 1 || versions[0].ResearchStatus != NewsContextResearchCompleted {
		t.Fatalf("versions=%+v err=%v, want per-change verified status", versions, err)
	}
}

func TestNewsContextCleanupRequiresReviewedDailyConclusion(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
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
	verification, found, err := svc.store.GetNewsContextMCPVerification(ctx, seed.thread.ID)
	if err != nil || !found || verification.Status != NewsContextMCPVerificationReady || verification.VersionID != daily.ID || verification.VerifiedAt.IsZero() {
		t.Fatalf("MCP verification=%+v found=%v err=%v, want persisted current-version success", verification, found, err)
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

func TestNewsContextCleanupProtectsThemeChangeAfterReviewedDailyConclusion(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	seedNewerUnresolvedCurrentVersion(t, svc, ctx, seed.thread.ID, daily)
	eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
	if err != nil || eligible || !strings.Contains(reason, "最新主题变化") {
		t.Fatalf("eligible=%v reason=%q err=%v, want newer-than-daily protection", eligible, reason, err)
	}
}

func TestNewsContextCleanupDoesNotTrustRegisteredMCPToolsWithoutRealCalls(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)
	if eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate); err != nil || !eligible {
		t.Fatalf("initial real MCP verification eligible=%v reason=%q err=%v", eligible, reason, err)
	}
	if err := svc.agentMCPServer.Shutdown(ctx); err != nil {
		t.Fatalf("stop MCP transport: %v", err)
	}
	eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
	if err != nil || eligible || !strings.Contains(reason, "CLI") {
		t.Fatalf("eligible=%v reason=%q err=%v, want real MCP transport gate", eligible, reason, err)
	}
	verification, found, err := svc.store.GetNewsContextMCPVerification(ctx, seed.thread.ID)
	if err != nil || !found || verification.Status != NewsContextMCPVerificationFailed || verification.ErrorMessage == "" {
		t.Fatalf("verification=%+v found=%v err=%v, want persisted failure", verification, found, err)
	}
}

func TestNewsContextCleanupRetiredThemeUsesReviewedHistoricalMCPVersion(t *testing.T) {
	for _, status := range []string{NewsThreadStatusMerged, NewsThreadStatusArchived} {
		t.Run(status, func(t *testing.T) {
			svc, cleanup := newEmbeddingTestService(t)
			defer cleanup()
			ctx := context.Background()
			candidate, thread, daily := seedRetiredNewsContextRetention(t, svc, ctx, status)

			eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
			if err != nil || !eligible || reason != "" {
				t.Fatalf("retired theme eligible=%v reason=%q err=%v", eligible, reason, err)
			}
			verification, found, err := svc.store.GetNewsContextMCPVerification(ctx, thread.ID)
			if err != nil || !found || verification.Status != NewsContextMCPVerificationReady || verification.VersionID != daily.ID || verification.VerifiedAt.IsZero() {
				t.Fatalf("historical MCP verification=%+v found=%v err=%v", verification, found, err)
			}
			summary, err := svc.GetNewsContextSummary(ctx)
			if err != nil || summary.MCPVerificationStatus != NewsContextMCPVerificationReady || !summary.MCPLastVerifiedAt.Equal(verification.CheckedAt) {
				t.Fatalf("MCP summary=%+v err=%v, want latest successful verification", summary, err)
			}
		})
	}
}

func TestNewsContextCleanupRetiredHistoricalThemeUsesItsBackfillFinalReview(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	source, err := svc.store.GetNewsContextRun(ctx, candidate.ContextRunID)
	if err != nil {
		t.Fatalf("get historical source run: %v", err)
	}
	source.TriggerType = NewsContextTriggerBackfill
	source.Status = NewsContextRunStatusCompleted
	source.Phase = "completed"
	if source, err = svc.store.UpdateNewsContextRun(ctx, source); err != nil {
		t.Fatalf("complete historical source run: %v", err)
	}

	cutoff := source.WindowEnd.Add(time.Hour)
	daily, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, WindowStart: source.WindowStart, WindowEnd: cutoff,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create historical daily run: %v", err)
	}
	version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: seed.thread.ID, RunID: daily.ID, AgentRunID: "historical-daily-agent",
		WindowType: NewsContextWindowDaily, VersionNo: seed.thread.CurrentVersion + 1,
		Title: seed.thread.Title, CoreThesis: seed.thread.CoreThesis, Stage: seed.thread.Stage,
		Confidence: seed.thread.Confidence, Facts: seed.thread.Facts,
		ResearchStatus: NewsContextResearchCompleted, ReviewStatus: NewsContextReviewNotRequired,
		CreatedAt: candidate.ContextCoveredAt.Add(time.Minute), EffectiveAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create historical daily version: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: daily.ID, ObjectType: NewsContextRunItemThread, ObjectID: seed.thread.ID,
		Status: NewsContextRunItemCompleted, ThreadID: seed.thread.ID, VersionID: version.ID,
	}}); err != nil {
		t.Fatalf("save historical daily output: %v", err)
	}
	finalReview, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusCompleted, WindowStart: cutoff, WindowEnd: cutoff.Add(time.Hour),
		ReviewStatus: NewsContextReviewCompleted, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create final current review: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusCompleted, Phase: "completed",
		RangeStartAt: source.WindowStart, CutoffAt: cutoff, FinalReviewRunID: finalReview.ID,
		CompletedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create completed historical task: %v", err)
	}
	for _, run := range []NewsContextRun{source, daily} {
		if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
			t.Fatalf("link historical %s run: %v", run.WindowType, err)
		}
	}
	ready, err := svc.ensureNewsContextBackfillReviewedDailyOutputs(ctx, backfill, finalReview)
	if err != nil || ready {
		t.Fatalf("persist first reviewed-output page ready=%v err=%v", ready, err)
	}
	if ready, err = svc.ensureNewsContextBackfillReviewedDailyOutputs(ctx, backfill, finalReview); err != nil || !ready {
		t.Fatalf("finish reviewed-output pages ready=%v err=%v", ready, err)
	}

	thread, err := svc.store.GetNewsThread(ctx, seed.thread.ID)
	if err != nil {
		t.Fatalf("get historical theme: %v", err)
	}
	thread.CurrentVersion = version.VersionNo
	thread.CurrentVersionID = version.ID
	thread.Status = NewsThreadStatusArchived
	thread.ReviewStatus = NewsContextReviewNotRequired
	if thread, err = svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatalf("archive historical theme: %v", err)
	}
	configureRetentionIndexes(t, svc, ctx, thread.ID, version)
	cfg, err := svc.embeddingConfigOrDefault(ctx)
	if err != nil {
		t.Fatalf("get embedding config: %v", err)
	}
	asset, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, thread.ID, cfg.EmbeddingModelID)
	if err != nil {
		t.Fatalf("get retired theme index: %v", err)
	}
	if err := svc.store.DeleteEmbeddingVector(ctx, asset.VectorRef); err != nil {
		t.Fatalf("delete retired theme vector: %v", err)
	}
	if err := svc.store.DeleteEmbeddingAsset(ctx, asset.ID); err != nil {
		t.Fatalf("delete retired theme asset: %v", err)
	}

	eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
	if err != nil || !eligible || reason != "" {
		t.Fatalf("reviewed retired historical theme eligible=%v reason=%q err=%v", eligible, reason, err)
	}
}

func TestNewsContextCleanupRetiredThemeProtectsOnHistoricalMCPFailure(t *testing.T) {
	for _, tt := range []struct {
		name       string
		failedTool string
		errorPart  string
	}{
		{name: "search", failedTool: mcpToolSemanticSearchNewsThreads, errorPart: "semantic theme lookup failed"},
		{name: "detail", failedTool: mcpToolGetNewsThread, errorPart: "theme detail lookup failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newEmbeddingTestService(t)
			defer cleanup()
			ctx := context.Background()
			candidate, thread, daily := seedRetiredNewsContextRetention(t, svc, ctx, NewsThreadStatusArchived)
			replaceRetentionMCPWithToolFailure(t, svc, tt.failedTool)

			eligible, reason, err := svc.newsContextCleanupEligible(ctx, candidate)
			if err != nil || eligible || !strings.Contains(reason, "CLI") {
				t.Fatalf("retired theme eligible=%v reason=%q err=%v, want protected", eligible, reason, err)
			}
			verification, found, err := svc.store.GetNewsContextMCPVerification(ctx, thread.ID)
			if err != nil || !found || verification.Status != NewsContextMCPVerificationFailed || verification.VersionID != daily.ID || !strings.Contains(verification.ErrorMessage, tt.errorPart) {
				t.Fatalf("historical MCP failure=%+v found=%v err=%v", verification, found, err)
			}
			summary, err := svc.GetNewsContextSummary(ctx)
			if err != nil || summary.MCPVerificationStatus != NewsContextMCPVerificationFailed || !summary.MCPLastVerifiedAt.Equal(verification.CheckedAt) || !strings.Contains(summary.MCPError, tt.errorPart) {
				t.Fatalf("MCP failure summary=%+v err=%v", summary, err)
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
	return seedNewsContextRetentionEventWithResearchStatus(t, svc, ctx, audits, counterEvidence, openQuestions, disposition, NewsContextResearchCompleted)
}

func seedNewsContextRetentionEventWithResearchStatus(t *testing.T, svc *Service, ctx context.Context, audits []NewsContextSearchAudit, counterEvidence, openQuestions []string, disposition, researchStatus string) newsContextRetentionSeed {
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
			EvidenceNewsIDs: []string{event.ID}, ResearchStatus: researchStatus,
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
	model := configureEmbeddingModel(t, svc, "retention-embedding-model")
	thread, err := svc.store.GetNewsThread(ctx, threadID)
	if err != nil {
		t.Fatalf("get indexed thread: %v", err)
	}
	for _, source := range []embeddingAssetSource{
		{ObjectType: EmbeddingObjectNewsThread, ObjectID: thread.ID, Text: NewsThreadEmbeddingText(thread)},
		{ObjectType: EmbeddingObjectNewsThreadVersion, ObjectID: daily.ID, Text: NewsThreadVersionEmbeddingText(daily)},
	} {
		asset := EmbeddingAsset{ObjectType: source.ObjectType, ObjectID: source.ObjectID, TextHash: hashEmbeddingText(source.Text),
			ModelID: model.ID, ProviderID: model.ProviderID, EmbeddingProtocol: model.EmbeddingProtocol,
			EmbeddingDimensions: 3, VectorRef: "retention-vector-" + source.ObjectID, Status: EmbeddingAssetStatusReady}
		if err := svc.store.UpsertEmbeddingVector(ctx, asset, []float64{0, 0, 1}); err != nil {
			t.Fatalf("save embedding vector: %v", err)
		}
		if _, err := svc.store.UpsertEmbeddingAsset(ctx, asset); err != nil {
			t.Fatalf("save embedding asset: %v", err)
		}
	}
	if _, err := svc.StartAgentMCPServer(); err != nil {
		t.Fatalf("start retention MCP server: %v", err)
	}
}

func seedRetiredNewsContextRetention(t *testing.T, svc *Service, ctx context.Context, status string) (NewsContextCleanupCandidate, NewsThread, NewsThreadVersion) {
	t.Helper()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)

	thread, err := svc.store.GetNewsThread(ctx, seed.thread.ID)
	if err != nil {
		t.Fatalf("get theme before retirement: %v", err)
	}
	thread.Status = status
	thread, err = svc.store.UpdateNewsThread(ctx, thread)
	if err != nil {
		t.Fatalf("retire theme: %v", err)
	}
	cfg, err := svc.embeddingConfigOrDefault(ctx)
	if err != nil {
		t.Fatalf("get embedding config: %v", err)
	}
	asset, err := svc.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, thread.ID, cfg.EmbeddingModelID)
	if err != nil {
		t.Fatalf("get current theme index: %v", err)
	}
	if err := svc.store.DeleteEmbeddingVector(ctx, asset.VectorRef); err != nil {
		t.Fatalf("delete retired current theme vector: %v", err)
	}
	if err := svc.store.DeleteEmbeddingAsset(ctx, asset.ID); err != nil {
		t.Fatalf("delete retired current theme index: %v", err)
	}
	return candidate, thread, daily
}

func replaceRetentionMCPWithToolFailure(t *testing.T, svc *Service, failedTool string) {
	t.Helper()
	if err := svc.agentMCPServer.Shutdown(context.Background()); err != nil {
		t.Fatalf("stop original MCP server: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read MCP request: %v", err)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Params.Name == failedTool {
			_, _ = w.Write(mcpErrorResponse(request.ID, mcpErrInternal, "forced MCP tool failure", nil))
			return
		}
		_, _ = w.Write(svc.HandleMCPRequest(raw))
	}))
	t.Cleanup(server.Close)
	svc.agentMCPMu.Lock()
	svc.agentMCPServer = server.Config
	svc.agentMCPURL = server.URL
	svc.agentMCPMu.Unlock()
}
