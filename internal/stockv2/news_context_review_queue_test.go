package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewsContextReviewQueueCoalescesPendingAndLegacyFailures(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.HourlyEnabled = false
	cfg.FourHourEnabled = false
	cfg.DailyEnabled = false
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save review queue config: %v", err)
	}
	pending := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "current-review-window", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-4 * time.Hour), WindowEnd: now,
	})
	legacyFailure := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "legacy-busy-review", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewFailed,
		WindowStart: now.Add(-8 * time.Hour), WindowEnd: now.Add(-4 * time.Hour),
		RetryCount: newsContextTimeoutRetryLimit, ErrorMessage: "portfolio sentinel run already running",
	})
	configurePortfolioSentinelModelForTest(t, svc)

	svc.reconcileNewsContextReviews(ctx)

	pending, err := svc.store.GetNewsContextRun(ctx, pending.ID)
	if err != nil {
		t.Fatalf("reload pending review: %v", err)
	}
	legacyFailure, err = svc.store.GetNewsContextRun(ctx, legacyFailure.ID)
	if err != nil {
		t.Fatalf("reload legacy review: %v", err)
	}
	if pending.ReviewStatus != NewsContextReviewRunning || pending.ReviewRunID == "" {
		t.Fatalf("pending review = %+v", pending)
	}
	if legacyFailure.ReviewStatus != NewsContextReviewRunning || legacyFailure.ReviewRunID != pending.ReviewRunID {
		t.Fatalf("legacy review = %+v, pending review id = %q", legacyFailure, pending.ReviewRunID)
	}
	if pending.RetryCount != 0 || legacyFailure.RetryCount != 0 {
		t.Fatalf("fresh consolidated retry counts = %d/%d, want 0/0", pending.RetryCount, legacyFailure.RetryCount)
	}
	if err := svc.decorateNewsContextRun(ctx, &pending); err != nil {
		t.Fatalf("decorate merged review: %v", err)
	}
	if pending.ReviewCoverageCount != 2 {
		t.Fatalf("review coverage count = %d, want 2", pending.ReviewCoverageCount)
	}
}

func TestNewsContextReviewQueueRecoversLegacySingleFlightFailureWithoutRetryCharge(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.HourlyEnabled = false
	cfg.FourHourEnabled = false
	cfg.DailyEnabled = false
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save review queue config: %v", err)
	}
	run := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "legacy-single-flight-review", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewFailed,
		WindowStart: now.Add(-4 * time.Hour), WindowEnd: now,
		RetryCount: newsContextTimeoutRetryLimit, ErrorMessage: "start portfolio review failed: portfolio sentinel run already running",
	})
	configurePortfolioSentinelModelForTest(t, svc)

	svc.reconcileNewsContextReviews(ctx)

	reloaded, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload recovered legacy review: %v", err)
	}
	if reloaded.ReviewStatus != NewsContextReviewRunning || reloaded.ReviewRunID == "" || reloaded.RetryCount != 0 {
		t.Fatalf("recovered legacy review = %+v", reloaded)
	}
}

func TestNewsContextReviewQueueWaitsForCatchupAndRunningSentinel(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.HourlyEnabled = false
	cfg.FourHourEnabled = true
	cfg.DailyEnabled = false
	cfg.NextFourHourAt = now.Add(-time.Second)
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save catch-up config: %v", err)
	}
	run := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "waiting-review-window", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-4 * time.Hour), WindowEnd: now,
	})
	failed := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "retryable-review-window", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewFailed,
		WindowStart: now.Add(-8 * time.Hour), WindowEnd: now.Add(-4 * time.Hour),
		ErrorMessage: "API returned HTTP 429", NextRetryAt: now.Add(-time.Minute),
	})

	svc.reconcileNewsContextReviews(ctx)
	assertNewsContextReviewStillQueued(t, svc, run.ID)
	assertNewsContextReviewFailureUnchanged(t, svc, failed.ID)

	cfg.NextFourHourAt = now.Add(4 * time.Hour)
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("advance catch-up cursor: %v", err)
	}
	busy, err := svc.store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		ID: "busy-sentinel", Status: PortfolioSentinelStatusRunning,
		TriggerType: PortfolioSentinelTriggerManual, WindowType: PortfolioSentinelWindowManual,
		WindowStartAt: now.Add(-time.Hour), WindowEndAt: now, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("create busy sentinel: %v", err)
	}
	svc.reconcileNewsContextReviews(ctx)
	assertNewsContextReviewStillQueued(t, svc, run.ID)
	assertNewsContextReviewFailureUnchanged(t, svc, failed.ID)
	busy.Status = PortfolioSentinelStatusCompleted
	busy.FinishedAt = now
	if _, err := svc.store.UpdatePortfolioSentinelRun(ctx, busy); err != nil {
		t.Fatalf("finish busy sentinel: %v", err)
	}
	configurePortfolioSentinelModelForTest(t, svc)
	svc.reconcileNewsContextReviews(ctx)
	reloaded, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || reloaded.ReviewStatus != NewsContextReviewRunning || reloaded.ReviewRunID == "" {
		t.Fatalf("review after queue released = %+v, err=%v", reloaded, err)
	}
	failed, err = svc.store.GetNewsContextRun(ctx, failed.ID)
	if err != nil || failed.ReviewStatus != NewsContextReviewRunning || failed.ReviewRunID != reloaded.ReviewRunID || failed.RetryCount != 0 {
		t.Fatalf("failed review after queue released = %+v, current=%+v err=%v", failed, reloaded, err)
	}
}

func TestNewsContextReviewChangesKeepLatestThemeVersionAndAuditCount(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	first := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "review-window-one", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-8 * time.Hour), WindowEnd: now.Add(-4 * time.Hour),
	})
	second := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "review-window-two", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-4 * time.Hour), WindowEnd: now,
	})
	threadID := "merged-review-thread"
	older, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "merged-review-version-one", ThreadID: threadID, RunID: first.ID,
		AgentRunID: "merged-review-agent-one", WindowType: NewsContextWindowFourHour,
		VersionNo: 1, Title: "旧主题版本", Stage: NewsThreadStageSpreading,
		MaterialChange: true, CreatedAt: now.Add(-6 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create older review version: %v", err)
	}
	latest, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ID: "merged-review-version-two", ThreadID: threadID, RunID: second.ID,
		AgentRunID: "merged-review-agent-two", WindowType: NewsContextWindowFourHour,
		VersionNo: 2, Title: "最新主题版本", Stage: NewsThreadStageAccelerating,
		MaterialChange: false, CreatedAt: now.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create latest review version: %v", err)
	}
	if _, err := svc.store.BeginNewsContextReviews(ctx, []string{first.ID, second.ID}, "merged-review-scope", 0); err != nil {
		t.Fatalf("bind merged review scope: %v", err)
	}

	changes, total, err := svc.ListNewsContextReviewChanges(ctx, "merged-review-scope", 50, 0)
	if err != nil {
		t.Fatalf("list merged review changes: %v", err)
	}
	if total != 1 || len(changes) != 1 {
		t.Fatalf("merged changes total=%d items=%+v", total, changes)
	}
	if changes[0].VersionID != latest.ID || changes[0].VersionID == older.ID ||
		changes[0].ChangeCount != 2 || !changes[0].MaterialChange {
		t.Fatalf("merged change = %+v", changes[0])
	}
	if err := svc.validatePortfolioSentinelNewsContextCoverage(ctx, "merged-review-scope", []string{latest.ID}); err != nil {
		t.Fatalf("validate merged latest coverage: %v", err)
	}
	if err := svc.validatePortfolioSentinelNewsContextCoverage(ctx, "merged-review-scope", []string{older.ID}); !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
		t.Fatalf("stale merged coverage error = %v, want invalid result", err)
	}
}

func TestNewsContextReviewReconcileCompletesEveryLinkedWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	first := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "completed-review-window-one", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-8 * time.Hour), WindowEnd: now.Add(-4 * time.Hour),
	})
	second := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "completed-review-window-two", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-4 * time.Hour), WindowEnd: now,
	})
	sentinel, err := svc.store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		ID: "completed-merged-sentinel", Status: PortfolioSentinelStatusCompleted,
		TriggerType: PortfolioSentinelTriggerManual, WindowType: PortfolioSentinelWindowManual,
		WindowStartAt: first.WindowStart, WindowEndAt: second.WindowEnd,
		StartedAt: now.Add(-time.Minute), FinishedAt: now,
	})
	if err != nil {
		t.Fatalf("create completed merged sentinel: %v", err)
	}
	if _, err := svc.store.BeginNewsContextReviews(ctx, []string{first.ID, second.ID}, sentinel.ID, 0); err != nil {
		t.Fatalf("bind completed merged sentinel: %v", err)
	}

	svc.reconcileNewsContextReviews(ctx)

	for _, runID := range []string{first.ID, second.ID} {
		run, err := svc.store.GetNewsContextRun(ctx, runID)
		if err != nil || run.Status != NewsContextRunStatusCompleted || run.ReviewStatus != NewsContextReviewCompleted {
			t.Fatalf("completed linked review %s = %+v, err=%v", runID, run, err)
		}
	}
}

func TestBeginNewsContextReviewsRollsBackPartialBatch(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	run := createNewsContextReviewQueueRun(t, svc, NewsContextRun{
		ID: "atomic-review-window", WindowType: NewsContextWindowFourHour,
		Status: NewsContextRunStatusWaitingReview, ReviewStatus: NewsContextReviewPending,
		WindowStart: now.Add(-4 * time.Hour), WindowEnd: now,
	})
	if _, err := svc.store.BeginNewsContextReviews(ctx, []string{run.ID, "missing-review-window"}, "partial-review-scope", 0); err == nil {
		t.Fatal("partial review batch unexpectedly succeeded")
	}
	reloaded, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload rolled back review: %v", err)
	}
	if reloaded.ReviewStatus != NewsContextReviewPending || reloaded.ReviewRunID != "" {
		t.Fatalf("partial review batch was not rolled back: %+v", reloaded)
	}
}

func createNewsContextReviewQueueRun(t *testing.T, svc *Service, run NewsContextRun) NewsContextRun {
	t.Helper()
	run.TriggerType = NewsContextTriggerScheduled
	run.CleanupStatus = NewsContextCleanupPending
	created, err := svc.store.CreateNewsContextRun(context.Background(), run)
	if err != nil {
		t.Fatalf("create review queue run %s: %v", run.ID, err)
	}
	return created
}

func assertNewsContextReviewStillQueued(t *testing.T, svc *Service, runID string) {
	t.Helper()
	run, err := svc.store.GetNewsContextRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("reload queued review: %v", err)
	}
	if run.ReviewStatus != NewsContextReviewPending || run.ReviewRunID != "" || run.RetryCount != 0 {
		t.Fatalf("queued review changed while blocked: %+v", run)
	}
}

func assertNewsContextReviewFailureUnchanged(t *testing.T, svc *Service, runID string) {
	t.Helper()
	run, err := svc.store.GetNewsContextRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("reload failed review: %v", err)
	}
	if run.ReviewStatus != NewsContextReviewFailed || run.ReviewRunID != "" || run.RetryCount != 0 {
		t.Fatalf("failed review consumed retry while blocked: %+v", run)
	}
}
