package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGetNewsThreadDetailAsOfExcludesFutureStateAndEvidence(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	oldAt := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	cutoff := oldAt.Add(time.Hour)
	futureAt := cutoff.Add(time.Hour)

	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		ID: "historical-detail-thread", Title: "未来主题", CoreThesis: "未来结论",
		Stage: NewsThreadStageSpreading, Status: NewsThreadStatusActive,
		CurrentVersion: 2, CurrentVersionID: "historical-detail-v2",
		ReviewStatus: NewsContextReviewCompleted, IndexStatus: NewsContextIndexReady,
		FirstSeenAt: oldAt, LastChangedAt: futureAt,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	oldVersion, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "historical-detail-v1", ThreadID: thread.ID, RunID: "historical-run-1",
		WindowType: NewsContextWindowHourly, VersionNo: 1, Title: "历史主题",
		CoreThesis: "历史结论", Stage: NewsThreadStageEmerging, LatestChange: "历史变化",
		ReviewStatus: NewsContextReviewNotRequired, IndexStatus: NewsContextIndexReady,
		EffectiveAt: oldAt, CreatedAt: futureAt.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create old version: %v", err)
	}
	futureVersion, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "historical-detail-v2", ThreadID: thread.ID, RunID: "historical-run-2",
		WindowType: NewsContextWindowHourly, VersionNo: 2, Title: "未来主题",
		CoreThesis: "未来结论", Stage: NewsThreadStageSpreading, LatestChange: "未来变化",
		ReviewStatus: NewsContextReviewCompleted, IndexStatus: NewsContextIndexReady,
		EffectiveAt: futureAt, CreatedAt: futureAt,
	})
	if err != nil {
		t.Fatalf("create future version: %v", err)
	}
	for _, item := range []NewsThreadEvidence{
		{ID: "historical-evidence", ThreadID: thread.ID, VersionID: oldVersion.ID, RunID: oldVersion.RunID, Title: "历史证据", EventAt: oldAt},
		{ID: "future-version-evidence", ThreadID: thread.ID, VersionID: futureVersion.ID, RunID: futureVersion.RunID, Title: "未来版本证据", EventAt: futureAt},
		{ID: "future-event-evidence", ThreadID: thread.ID, VersionID: oldVersion.ID, RunID: oldVersion.RunID, Title: "未来时间证据", EventAt: futureAt},
	} {
		if _, err := svc.store.CreateNewsThreadEvidence(ctx, item); err != nil {
			t.Fatalf("create evidence %s: %v", item.ID, err)
		}
	}

	detail, err := svc.GetNewsThreadDetailAsOf(ctx, thread.ID, cutoff.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("historical detail: %v", err)
	}
	if detail.Theme.CurrentVersionID != oldVersion.ID || detail.Theme.Title != "历史主题" || detail.Theme.CoreThesis != "历史结论" {
		t.Fatalf("historical theme = %+v, want old snapshot", detail.Theme)
	}
	if len(detail.Versions) != 1 || detail.Versions[0].ID != oldVersion.ID {
		t.Fatalf("historical versions = %+v, want only old version", detail.Versions)
	}
	if len(detail.Evidence) != 1 || detail.Evidence[0].ID != "historical-evidence" {
		t.Fatalf("historical evidence = %+v, want only evidence known by cutoff", detail.Evidence)
	}

	current, err := svc.GetNewsThreadDetailAsOf(ctx, thread.ID, "")
	if err != nil {
		t.Fatalf("current detail: %v", err)
	}
	if current.Theme.CurrentVersionID != futureVersion.ID || current.Theme.Title != "未来主题" {
		t.Fatalf("current theme = %+v, want unchanged current-detail behavior", current.Theme)
	}
	if _, err := svc.GetNewsThreadDetailAsOf(ctx, thread.ID, "not-a-time"); !errors.Is(err, ErrInvalidNewsContextInput) {
		t.Fatalf("invalid cutoff error = %v, want invalid news context input", err)
	}
}

func TestGetNewsThreadDetailAsOfUsesValidBackfillFinalReviewAssociation(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	effectiveAt := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
	daily, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, ReviewStatus: NewsContextReviewNotRequired,
		WindowStart: effectiveAt.Add(-24 * time.Hour), WindowEnd: effectiveAt,
	})
	if err != nil {
		t.Fatalf("create historical daily run: %v", err)
	}
	finalReview, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusCompleted, ReviewStatus: NewsContextReviewCompleted,
		WindowStart: effectiveAt, WindowEnd: effectiveAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create final review run: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusCompleted, Phase: "completed",
		RangeStartAt: effectiveAt.Add(-24 * time.Hour), CutoffAt: effectiveAt,
		FinalReviewRunID: finalReview.ID, CompletedAt: effectiveAt.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, daily.ID); err != nil {
		t.Fatalf("link historical daily run: %v", err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		ID: "final-reviewed-history-thread", Title: "历史主题", CoreThesis: "历史结论",
		Stage: NewsThreadStageEmerging, Status: NewsThreadStatusActive,
		CurrentVersion: 1, CurrentVersionID: "final-reviewed-history-version",
		ReviewStatus: NewsContextReviewPending, IndexStatus: NewsContextIndexReady,
		FirstSeenAt: effectiveAt, LastChangedAt: effectiveAt,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: thread.CurrentVersionID, ThreadID: thread.ID, RunID: daily.ID,
		WindowType: NewsContextWindowDaily, VersionNo: 1, Title: thread.Title,
		CoreThesis: thread.CoreThesis, Stage: thread.Stage, ReviewStatus: NewsContextReviewPending,
		IndexStatus: NewsContextIndexReady, EffectiveAt: effectiveAt,
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := svc.store.UpsertNewsContextBackfillReviewedVersions(ctx, backfill.ID, finalReview.ID,
		[]newsContextBackfillReviewedVersion{{
			DailyRunID: daily.ID, ThreadID: thread.ID, VersionID: version.ID,
			FinalReviewRunID: finalReview.ID,
		}}); err != nil {
		t.Fatalf("link version to final review: %v", err)
	}

	detail, err := svc.GetNewsThreadDetailAsOf(ctx, thread.ID, effectiveAt.Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("historical detail: %v", err)
	}
	if detail.Theme.ReviewStatus != NewsContextReviewCompleted || len(detail.Versions) != 1 ||
		detail.Versions[0].ReviewStatus != NewsContextReviewCompleted {
		t.Fatalf("review status theme=%q versions=%+v, want completed", detail.Theme.ReviewStatus, detail.Versions)
	}
	if reasons := strings.Join(detail.ProtectedReasons, "|"); strings.Contains(reasons, "影响复核") {
		t.Fatalf("valid final review association remained protected: %s", reasons)
	}

	if _, err := svc.store.db.ExecContext(ctx, `UPDATE stockv2_news_context_runs SET review_status=? WHERE id=?`,
		NewsContextReviewPending, finalReview.ID); err != nil {
		t.Fatalf("invalidate final review: %v", err)
	}
	detail, err = svc.GetNewsThreadDetailAsOf(ctx, thread.ID, effectiveAt.Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("historical detail after invalid review: %v", err)
	}
	if detail.Theme.ReviewStatus != NewsContextReviewPending ||
		!strings.Contains(strings.Join(detail.ProtectedReasons, "|"), "影响复核") {
		t.Fatalf("invalid final review association was accepted: %+v", detail)
	}
}

func TestNewsThreadEvidenceReportsOriginalNewsCleanupState(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	retained, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "保留原文", EventAt: now})
	if err != nil {
		t.Fatalf("create retained news: %v", err)
	}
	compacted, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "已清理原文", EventAt: now})
	if err != nil {
		t.Fatalf("create compacted news: %v", err)
	}
	if _, err := svc.store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events
		SET context_status=?,compacted_at=? WHERE id=?`, NewsEventContextCompacted, now, compacted.ID); err != nil {
		t.Fatalf("mark compacted news: %v", err)
	}
	for _, item := range []NewsThreadEvidence{
		{ID: "retained-evidence", ThreadID: "evidence-theme", VersionID: "evidence-version", RunID: "evidence-run", NewsEventID: retained.ID, Title: "保留"},
		{ID: "compacted-evidence", ThreadID: "evidence-theme", VersionID: "evidence-version", RunID: "evidence-run", NewsEventID: compacted.ID, Title: "清理"},
		{ID: "unknown-evidence", ThreadID: "evidence-theme", VersionID: "evidence-version", RunID: "evidence-run", Title: "外部证据"},
	} {
		if _, err := svc.store.CreateNewsThreadEvidence(ctx, item); err != nil {
			t.Fatalf("create evidence %s: %v", item.ID, err)
		}
	}
	items, err := svc.store.ListNewsThreadEvidence(ctx, NewsThreadEvidenceListFilter{ThreadID: "evidence-theme", Limit: 10})
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	byID := make(map[string]NewsThreadEvidence, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	if value := byID["retained-evidence"].OriginalNewsDeleted; value == nil || *value {
		t.Fatalf("retained original state=%v, want false", value)
	}
	if value := byID["compacted-evidence"].OriginalNewsDeleted; value == nil || !*value {
		t.Fatalf("compacted original state=%v, want true", value)
	}
	if value := byID["unknown-evidence"].OriginalNewsDeleted; value != nil {
		t.Fatalf("unknown original state=%v, want omitted", *value)
	}
	for id, expected := range map[string]string{
		"retained-evidence":  `"originalNewsDeleted":false`,
		"compacted-evidence": `"originalNewsDeleted":true`,
	} {
		payload, err := json.Marshal(byID[id])
		if err != nil || !strings.Contains(string(payload), expected) {
			t.Fatalf("evidence %s json=%s err=%v, want %s", id, payload, err, expected)
		}
	}
	payload, err := json.Marshal(byID["unknown-evidence"])
	if err != nil || strings.Contains(string(payload), "originalNewsDeleted") {
		t.Fatalf("unknown evidence json=%s err=%v, want field omitted", payload, err)
	}
}

func TestNewsThreadRelationJSONUsesStrength(t *testing.T) {
	payload, err := json.Marshal(NewsThreadRelation{Strength: 0.72})
	if err != nil || !strings.Contains(string(payload), `"strength":0.72`) || strings.Contains(string(payload), "confidence") {
		t.Fatalf("relation json=%s err=%v", payload, err)
	}
}
