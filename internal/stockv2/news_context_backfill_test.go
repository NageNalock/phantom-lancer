package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNewsContextBackfillWorkerUpdatePreservesOwnerPause(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	windowEnd := time.Now().In(time.Local).Truncate(time.Hour).Add(-time.Hour)
	windowStart := windowEnd.Add(-time.Hour)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "暂停竞态", EventAt: windowStart.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create news: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: windowEnd,
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	staleWorkerSnapshot := backfill
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, WindowStart: windowStart, WindowEnd: windowEnd,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create completed run: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link run: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID,
		Status: NewsContextRunItemCompleted, Disposition: NewsEventContextCovered, SourceAt: event.EventAt,
	}}); err != nil {
		t.Fatalf("persist completed checklist: %v", err)
	}

	paused, err := svc.PauseNewsContextBackfill(ctx)
	if err != nil {
		t.Fatalf("pause backfill: %v", err)
	}
	assertBackfillProgress := func(label string, item NewsContextBackfill, status string) {
		t.Helper()
		if item.Status != status || item.TotalNewsCount != 1 || item.ProcessedNewsCount != 1 ||
			item.RemainingNewsCount != 0 || item.MissingNewsCount != 0 {
			t.Fatalf("%s backfill=%+v", label, item)
		}
	}
	assertBackfillProgress("paused", paused, NewsContextBackfillStatusPaused)

	saved, err := svc.refreshAndSaveNewsContextBackfill(ctx, staleWorkerSnapshot)
	if err != nil {
		t.Fatalf("save stale worker snapshot: %v", err)
	}
	assertBackfillProgress("stale worker save", saved, NewsContextBackfillStatusPaused)
	staleWorkerSnapshot.Status = NewsContextBackfillStatusCompleted
	staleWorkerSnapshot.Phase = "completed"
	staleWorkerSnapshot.ErrorMessage = "stale completion"
	staleWorkerSnapshot.CompletedAt = time.Now()
	saved, err = svc.refreshAndSaveNewsContextBackfill(ctx, staleWorkerSnapshot)
	if err != nil {
		t.Fatalf("save stale finalizer snapshot: %v", err)
	}
	assertBackfillProgress("stale finalizer save", saved, NewsContextBackfillStatusPaused)
	if saved.Phase != "hourly" || saved.ErrorMessage != "" || !saved.CompletedAt.IsZero() {
		t.Fatalf("stale finalizer polluted paused state: %+v", saved)
	}
	staleWorkerSnapshot.Status = NewsContextBackfillStatusFailed
	staleWorkerSnapshot.Phase = "failed"
	staleWorkerSnapshot.ErrorMessage = "stale failure"
	staleWorkerSnapshot.CompletedAt = time.Time{}
	saved, err = svc.refreshAndSaveNewsContextBackfill(ctx, staleWorkerSnapshot)
	if err != nil {
		t.Fatalf("save stale failure snapshot: %v", err)
	}
	assertBackfillProgress("stale failure save", saved, NewsContextBackfillStatusPaused)
	if saved.Phase != "hourly" || saved.ErrorMessage != "" || !saved.CompletedAt.IsZero() {
		t.Fatalf("stale failure polluted paused state: %+v", saved)
	}
	completed, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: run.ID, Status: NewsContextRunItemCompleted,
	})
	if err != nil || completed != 1 {
		t.Fatalf("completed checklist count=%d err=%v", completed, err)
	}

	resumed, err := svc.ResumeNewsContextBackfill(ctx)
	if err != nil {
		t.Fatalf("resume backfill: %v", err)
	}
	svc.StopBackground()
	assertBackfillProgress("resumed", resumed, NewsContextBackfillStatusRunning)
	completed, err = svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: run.ID, Status: NewsContextRunItemCompleted,
	})
	if err != nil || completed != 1 {
		t.Fatalf("resumed checklist count=%d err=%v", completed, err)
	}
	saved, err = svc.refreshAndSaveNewsContextBackfill(ctx, staleWorkerSnapshot)
	if err != nil {
		t.Fatalf("save pre-pause worker after resume: %v", err)
	}
	if saved.Status != NewsContextBackfillStatusRunning || saved.Phase != resumed.Phase ||
		saved.ErrorMessage != "" || saved.OwnerRevision != resumed.OwnerRevision {
		t.Fatalf("stale worker overwrote resumed owner state: saved=%+v resumed=%+v", saved, resumed)
	}
}

func TestNewsContextBackfillFinalCoverageRejectsCoveredNewsWithoutEvidence(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Now().In(time.Local).Truncate(time.Hour)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "缺少精简证据", EventAt: cutoff.Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create historical news: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "finalizing", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create historical task: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, WindowStart: cutoff.Add(-time.Hour), WindowEnd: cutoff,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create historical run: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link historical run: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID,
		Status: NewsContextRunItemCompleted, Disposition: "support", SourceAt: event.EventAt,
	}}); err != nil {
		t.Fatalf("persist abnormal covered checklist: %v", err)
	}
	if _, err := svc.store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events
		SET context_status=?,context_run_id=?,context_covered_at=? WHERE id=?`,
		NewsEventContextCovered, run.ID, time.Now(), event.ID); err != nil {
		t.Fatalf("persist abnormal covered source state: %v", err)
	}
	backfill, err = svc.refreshNewsContextBackfillProgress(ctx, backfill)
	if err != nil || backfill.RemainingNewsCount != 0 || backfill.MissingNewsCount != 0 {
		t.Fatalf("refresh covered historical task=%+v err=%v", backfill, err)
	}
	missingEvidence, err := svc.store.CountNewsContextBackfillCoveredWithoutEvidence(ctx, backfill.ID)
	if err != nil || missingEvidence != 1 {
		t.Fatalf("covered without evidence=%d err=%v", missingEvidence, err)
	}
	if err := svc.validateNewsContextBackfillFinalCoverage(ctx, backfill); err == nil {
		t.Fatal("historical completion accepted covered news without compact evidence")
	}
}

func TestNewsContextCleanupRejectsBlockingBackfillBeforeCreatingRun(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       string
		missingCount int
	}{
		{name: "running", status: NewsContextBackfillStatusRunning},
		{name: "paused", status: NewsContextBackfillStatusPaused},
		{name: "failed", status: NewsContextBackfillStatusFailed},
		{name: "completed with missing news", status: NewsContextBackfillStatusCompleted, missingCount: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			if _, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
				Status: tt.status, Phase: "hourly", CutoffAt: time.Now().Add(-time.Hour),
				MissingNewsCount: tt.missingCount,
			}); err != nil {
				t.Fatalf("create blocking backfill: %v", err)
			}
			if _, err := svc.StartNewsContextCleanupRun(ctx, RequestStartNewsContextCleanup{RequestedBy: "test"}); !errors.Is(err, ErrNewsContextReviewIncomplete) {
				t.Fatalf("cleanup error=%v, want %v", err, ErrNewsContextReviewIncomplete)
			}
			count, err := svc.store.CountNewsContextCleanupRuns(ctx, NewsContextCleanupRunListFilter{})
			if err != nil || count != 0 {
				t.Fatalf("cleanup rows=%d err=%v, want no persisted run", count, err)
			}
		})
	}
}

func TestNewsContextBackfillPrepareFailureDoesNotLeaveRunningChild(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().In(time.Local).Truncate(time.Hour).Add(-2 * time.Hour)
	cutoff := start.Add(time.Hour)
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "准备失败", EventAt: start.Add(30 * time.Minute),
	}); err != nil {
		t.Fatalf("create historical news: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	if err := svc.store.marketDB.Close(); err != nil {
		t.Fatalf("close market db: %v", err)
	}
	if err := svc.startNewsContextBackfillWindow(ctx, backfill, NewsContextWindowHourly, start, cutoff); err == nil {
		t.Fatal("start historical window unexpectedly succeeded")
	}
	stored, err := svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil {
		t.Fatalf("get failed backfill: %v", err)
	}
	if stored.Status != NewsContextBackfillStatusFailed || stored.CurrentRunID == "" {
		t.Fatalf("backfill after prepare failure = %+v", stored)
	}
	run, err := svc.store.GetNewsContextRun(ctx, stored.CurrentRunID)
	if err != nil {
		t.Fatalf("get failed child: %v", err)
	}
	if run.Status != NewsContextRunStatusFailed {
		t.Fatalf("child status = %q, want failed", run.Status)
	}
	if running, err := svc.store.HasRunningNewsContextRun(ctx); err != nil || running {
		t.Fatalf("running child after prepare failure = %v, err=%v", running, err)
	}
}

func TestNewsContextBackfillFinalDailyIsReservedBeforeItCanYield(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Now().In(time.Local).Truncate(time.Hour).Add(-time.Hour)
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, Phase: newsContextRunPhaseAggregating,
		WindowStart: cutoff, WindowEnd: cutoff.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	backfill, err = svc.store.ReserveNewsContextBackfillFinalReviewRun(ctx, backfill, run)
	if err != nil {
		t.Fatalf("reserve final daily: %v", err)
	}
	owner, found, err := svc.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID)
	if err != nil || !found || owner.ID != backfill.ID || owner.CurrentRunID != run.ID || owner.FinalReviewRunID != run.ID {
		t.Fatalf("durable final owner=%+v found=%v err=%v", owner, found, err)
	}
	if _, historical, err := svc.store.NewsContextBackfillForRun(ctx, run.ID); err != nil || historical {
		t.Fatalf("final current daily must retain realtime review semantics: historical=%v err=%v", historical, err)
	}
	run.Status = NewsContextRunStatusRunning
	run.CurrentAgentRunID = "completed-fragment"
	if _, err := svc.store.UpdateNewsContextRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := svc.yieldNewsContextRunAfterFragment(ctx, run.ID); err != nil {
		t.Fatalf("yield final daily fragment: %v", err)
	}
	yielded, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != NewsContextRunStatusPending || yielded.Phase != "queued" || yielded.CurrentAgentRunID != "" {
		t.Fatalf("yielded final daily=%+v", yielded)
	}
}

func TestNewsContextBackfillFinalDailyReclaimsOnlyInactiveRealtimeClaims(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Now().In(time.Local).Add(-time.Hour).Truncate(time.Minute)
	failedOwner, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusFailed, WindowStart: cutoff, WindowEnd: cutoff.Add(10 * time.Minute),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	activeOwner, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusPending, WindowStart: cutoff.Add(10 * time.Minute), WindowEnd: cutoff.Add(20 * time.Minute),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]NewsEvent, 0, 3)
	for index, owner := range []string{failedOwner.ID, activeOwner.ID, "missing-realtime-owner"} {
		event, err := svc.CreateNewsEvent(ctx, NewsEvent{
			Source: "test", Title: fmt.Sprintf("遗留认领-%d", index),
			EventAt: cutoff.Add(time.Duration(25+index) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events
			SET context_status=?,context_run_id=? WHERE id=?`, NewsEventContextClaimed, owner, event.ID); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.startNewsContextBackfillFinalReview(ctx, backfill); !errors.Is(err, ErrNewsContextAlreadyRunning) {
		t.Fatalf("active realtime owner error=%v", err)
	}
	backfill, err = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil || backfill.Status != NewsContextBackfillStatusRunning || backfill.CurrentRunID == "" {
		t.Fatalf("backfill waiting for active owner=%+v err=%v", backfill, err)
	}
	finalRun, err := svc.store.GetNewsContextRun(ctx, backfill.CurrentRunID)
	if err != nil || finalRun.Status != NewsContextRunStatusPending || finalRun.Phase != "collecting" {
		t.Fatalf("final run while yielding=%+v err=%v", finalRun, err)
	}
	for index, event := range events {
		var owner string
		if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_run_id,'')
			FROM stockv2_news_events WHERE id=?`, event.ID).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		want := []string{failedOwner.ID, activeOwner.ID, "missing-realtime-owner"}[index]
		if owner != want {
			t.Fatalf("claim transferred before active owner yielded event=%s owner=%s want=%s", event.ID, owner, want)
		}
	}
	activeOwner.Status = NewsContextRunStatusFailed
	if _, err := svc.store.UpdateNewsContextRun(ctx, activeOwner); err != nil {
		t.Fatal(err)
	}
	finalRun, err = svc.preparePendingNewsContextRun(ctx, finalRun)
	if err != nil {
		t.Fatalf("prepare final run after owners stopped: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: finalRun.ID, ObjectType: NewsContextRunItemNewsEvent, Limit: 10,
	})
	if err != nil || len(items) != len(events) {
		t.Fatalf("final current manifest items=%+v err=%v", items, err)
	}
	for _, event := range events {
		var status, owner string
		if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),COALESCE(context_run_id,'')
			FROM stockv2_news_events WHERE id=?`, event.ID).Scan(&status, &owner); err != nil {
			t.Fatal(err)
		}
		if status != NewsEventContextClaimed || owner != finalRun.ID {
			t.Fatalf("final current claim event=%s status=%s owner=%s", event.ID, status, owner)
		}
	}
	if err := svc.store.ValidateNewsContextFinalReviewEventManifest(ctx, finalRun.ID,
		[]string{events[0].ID, events[1].ID, events[2].ID}); err != nil {
		t.Fatalf("verify final current event coverage: %v", err)
	}
}

func TestNewsContextBackfillFinalDailyCompletionPersistsIndexingBeforeReview(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := time.Now().In(time.Local).Truncate(time.Minute)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, Phase: newsContextRunPhaseConverging,
		WindowStart: end.Add(-24 * time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review", CutoffAt: run.WindowStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if backfill, err = svc.store.ReserveNewsContextBackfillFinalReviewRun(ctx, backfill, run); err != nil {
		t.Fatal(err)
	}
	if err := svc.completeNewsContextRun(ctx, &run, defaultNewsContextConfig()); err != nil {
		t.Fatalf("complete final current daily: %v", err)
	}
	stored, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || stored.Status != NewsContextRunStatusCompleted ||
		stored.ReviewStatus != NewsContextReviewPending || stored.Phase != newsContextBackfillPhaseIndexing ||
		stored.ReviewRunID != "" {
		t.Fatalf("final current daily before indexing=%+v err=%v", stored, err)
	}
	if count, err := svc.store.CountPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{}); err != nil || count != 0 {
		t.Fatalf("final current daily triggered early review count=%d err=%v", count, err)
	}
	if err := svc.continueNewsContextBackfillRun(ctx, backfill); err != nil {
		t.Fatalf("advance final current daily to indexing: %v", err)
	}
	backfill, err = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil || backfill.Phase != newsContextBackfillPhaseIndexing {
		t.Fatalf("persisted indexing parent=%+v err=%v", backfill, err)
	}
}

func TestNewsContextBackfillIndexesBeforeCreatingFinalImpactReview(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := configureEmbeddingModel(t, svc, "backfill-final-order")
	configurePortfolioSentinelModelForTest(t, svc)
	zero := 0
	if _, err := svc.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{MaintainRateLimitMs: &zero}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().In(time.Local).Truncate(time.Minute)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusCompleted, Phase: newsContextBackfillPhaseIndexing,
		WindowStart: end.Add(-24 * time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewPending, CleanupStatus: NewsContextCleanupPending,
		FinishedAt: end,
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "最终顺序主题", CoreThesis: "索引完成以后才能影响复核",
		Stage: NewsThreadStageSpreading, Status: NewsThreadStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	historical, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "historical-material-run", AgentRunID: "historical-material-agent",
		WindowType: NewsContextWindowFourHour, VersionNo: 1, Title: thread.Title,
		CoreThesis: thread.CoreThesis, Stage: thread.Stage, MaterialChange: true,
		ReviewStatus: NewsContextReviewNotRequired, EffectiveAt: end.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := svc.store.GetNewsThreadVersion(ctx, historical.ID); err != nil || !stored.MaterialChange {
		t.Fatalf("historical material version=%+v err=%v", stored, err)
	}
	current, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: run.ID, AgentRunID: "final-current-agent",
		WindowType: NewsContextWindowDaily, VersionNo: 2, Title: thread.Title,
		CoreThesis: thread.CoreThesis, Stage: thread.Stage,
		ReviewStatus: NewsContextReviewPending, EffectiveAt: end,
	})
	if err != nil {
		t.Fatal(err)
	}
	thread.CurrentVersion = current.VersionNo
	thread.CurrentVersionID = current.ID
	thread.IndexStatus = NewsContextIndexPending
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review", CutoffAt: run.WindowStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	backfill, err = svc.store.ReserveNewsContextBackfillFinalReviewRun(ctx, backfill, run)
	if err != nil {
		t.Fatal(err)
	}
	backfill.Phase = newsContextBackfillPhaseIndexing
	if backfill, err = svc.store.UpdateNewsContextBackfillWorker(ctx, backfill); err != nil {
		t.Fatal(err)
	}

	objects := []struct {
		label      string
		objectType string
		objectID   string
	}{
		{"current theme", EmbeddingObjectNewsThread, thread.ID},
		{"material history", EmbeddingObjectNewsThreadVersion, historical.ID},
		{"current daily", EmbeddingObjectNewsThreadVersion, current.ID},
	}
	indexesReady := false
	for page := 0; page < 10 && !indexesReady; page++ {
		svc.processNewsContextBackfillFinalIndexPage(ctx, backfill.ID)
		if count, err := svc.store.CountPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{}); err != nil || count != 0 {
			t.Fatalf("impact review created before index verification page=%d count=%d err=%v", page, count, err)
		}
		indexesReady = true
		for _, object := range objects {
			if _, err := svc.store.GetEmbeddingAssetByObject(ctx, object.objectType, object.objectID, model.ID); errors.Is(err, ErrEmbeddingAssetNotFound) {
				indexesReady = false
			} else if err != nil {
				t.Fatalf("read pre-review index %s %s/%s: %v", object.label, object.objectType, object.objectID, err)
			}
		}
	}
	if !indexesReady {
		t.Fatal("final indexes did not become ready before review")
	}
	stillIndexing, err := svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil || stillIndexing.Phase != newsContextBackfillPhaseIndexing {
		t.Fatalf("backfill before index verification=%+v err=%v", stillIndexing, err)
	}

	svc.processNewsContextBackfillFinalIndexPage(ctx, backfill.ID)
	if count, err := svc.store.CountPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{}); err != nil || count != 1 {
		t.Fatalf("impact review after indexes count=%d err=%v", count, err)
	}
	reviewing, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || reviewing.ReviewRunID == "" || reviewing.ReviewStatus != NewsContextReviewRunning {
		t.Fatalf("final review run after indexes=%+v err=%v", reviewing, err)
	}
}

func TestNewsContextBackfillResponseShowsFinalReviewCoverage(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Now().In(time.Local).Truncate(time.Hour)
	finalReview, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusCompleted, WindowStart: cutoff, WindowEnd: cutoff.Add(time.Hour),
		ReviewStatus: NewsContextReviewCompleted, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "finalizing", CutoffAt: cutoff,
		FinalReviewRunID: finalReview.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	type output struct {
		run       NewsContextRun
		threadID  string
		versionID string
	}
	outputs := make([]output, 0, 2)
	for index := 0; index < 2; index++ {
		start := cutoff.Add(time.Duration(index-2) * 24 * time.Hour)
		run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
			WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerBackfill,
			Status: NewsContextRunStatusCompleted, WindowStart: start, WindowEnd: start.Add(24 * time.Hour),
			ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
			t.Fatal(err)
		}
		item := output{run: run, threadID: fmt.Sprintf("coverage-theme-%d", index), versionID: fmt.Sprintf("coverage-version-%d", index)}
		if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
			RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: item.versionID,
			ThreadID: item.threadID, VersionID: item.versionID, Status: NewsContextRunItemCompleted,
		}}); err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, item)
	}
	link := func(item output) {
		t.Helper()
		if err := svc.store.UpsertNewsContextBackfillReviewedVersions(ctx, backfill.ID, finalReview.ID,
			[]newsContextBackfillReviewedVersion{{DailyRunID: item.run.ID, ThreadID: item.threadID,
				VersionID: item.versionID, FinalReviewRunID: finalReview.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	link(outputs[0])
	observed, err := svc.GetNewsContextBackfill(ctx)
	if err != nil || observed.DailyOutputCount != 2 || observed.ReviewLinkedCount != 1 || observed.ReviewMissingCount != 1 {
		t.Fatalf("partial review coverage=%+v err=%v", observed, err)
	}
	link(outputs[1])
	observed, err = svc.GetNewsContextBackfill(ctx)
	if err != nil || observed.DailyOutputCount != 2 || observed.ReviewLinkedCount != 2 || observed.ReviewMissingCount != 0 {
		t.Fatalf("complete review coverage=%+v err=%v", observed, err)
	}
}

func TestOrdinaryDailyCompletionStillCreatesImpactReviewImmediately(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	configurePortfolioSentinelModelForTest(t, svc)
	end := time.Now().Truncate(time.Minute)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, Phase: newsContextRunPhaseConverging,
		WindowStart: end.Add(-24 * time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.completeNewsContextRun(ctx, &run, defaultNewsContextConfig()); err != nil {
		t.Fatalf("complete ordinary daily: %v", err)
	}
	if count, err := svc.store.CountPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{}); err != nil || count != 1 {
		t.Fatalf("ordinary daily impact review count=%d err=%v", count, err)
	}
	stored, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || stored.ReviewRunID == "" || stored.ReviewStatus != NewsContextReviewRunning {
		t.Fatalf("ordinary daily review=%+v err=%v", stored, err)
	}
}

func TestNewsContextBackfillFinalizationDoesNotRescanAfterImpactReview(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Now().In(time.Local).Truncate(time.Hour)
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	late, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "复核后到达的旧消息", EventAt: cutoff.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !svc.tryStartNewsContextRun() {
		t.Fatal("reserve realtime execution slot")
	}
	defer svc.finishNewsContextRun()
	if err := svc.beginNewsContextBackfillFinalization(ctx, backfill); err != nil {
		t.Fatalf("begin finalization: %v", err)
	}
	count, err := svc.store.CountNewsContextBackfillManifestInRange(ctx, backfill.ID,
		cutoff.Add(-time.Hour), cutoff)
	if err != nil || count != 0 {
		t.Fatalf("post-review news was appended count=%d err=%v", count, err)
	}
	var status, runID string
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),COALESCE(context_run_id,'')
		FROM stockv2_news_events WHERE id=?`, late.ID).Scan(&status, &runID); err != nil {
		t.Fatal(err)
	}
	if status != NewsEventContextPending || runID != "" {
		t.Fatalf("post-review late news was claimed status=%q run=%q", status, runID)
	}
	svc.newsBackfillMu.Lock()
	finalizerRunning := svc.newsBackfillRun
	svc.newsBackfillMu.Unlock()
	if finalizerRunning {
		t.Fatal("finalizer bypassed the occupied realtime execution slot")
	}
}

func TestNewsContextBackfillRefreshMovesRangeStartForEarlierLateNews(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().In(time.Local)
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Add(-24 * time.Hour)
	laterAt := cutoff.Add(-2 * time.Hour)
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "initial", EventAt: laterAt}); err != nil {
		t.Fatalf("create initial news: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	initialStart := backfill.RangeStartAt
	earlierAt := cutoff.Add(-50 * time.Hour)
	earlier, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "late earlier", EventAt: earlierAt})
	if err != nil {
		t.Fatalf("create earlier late news: %v", err)
	}
	if inserted, err := svc.store.AppendNewsContextBackfillManifest(ctx, backfill.ID, cutoff); err != nil || inserted != 1 {
		t.Fatalf("append earlier late news inserted=%d err=%v", inserted, err)
	}
	refreshed, err := svc.refreshAndSaveNewsContextBackfill(ctx, backfill)
	if err != nil {
		t.Fatalf("refresh backfill: %v", err)
	}
	earlierLocal := earlierAt.In(time.Local)
	wantStart := time.Date(earlierLocal.Year(), earlierLocal.Month(), earlierLocal.Day(), 0, 0, 0, 0, time.Local)
	if !refreshed.RangeStartAt.Equal(wantStart) || !refreshed.RangeStartAt.Before(initialStart) {
		t.Fatalf("range start=%v initial=%v want=%v", refreshed.RangeStartAt, initialStart, wantStart)
	}

	hourStart := time.Date(earlierLocal.Year(), earlierLocal.Month(), earlierLocal.Day(), earlierLocal.Hour(), 0, 0, 0, time.Local)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusPending, WindowStart: hourStart, WindowEnd: hourStart.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create earlier hourly run: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link earlier hourly run: %v", err)
	}
	claimed, err := svc.store.ClaimNewsContextBackfillEvents(ctx, backfill.ID, run.ID, hourStart, hourStart.Add(time.Hour))
	if err != nil || len(claimed) != 1 || claimed[0] != earlier.ID {
		t.Fatalf("claimed=%v err=%v, want earlier news %s", claimed, err, earlier.ID)
	}
}

func TestNewsContextBackfillManifestCovers16001EventsExactlyOnce(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().In(time.Local).Truncate(time.Hour).Add(-2 * time.Hour)
	cutoff := start.Add(time.Hour)

	tx, err := svc.store.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin news insert: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO stockv2_news_events
		(id,source,title,link_status,event_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatalf("prepare news insert: %v", err)
	}
	for i := 0; i < 16_001; i++ {
		id := fmt.Sprintf("bulk-news-%05d", i)
		at := start.Add(time.Duration(i) * time.Millisecond)
		if _, err := stmt.ExecContext(ctx, id, "test", id, NewsEventLinkStatusPending, at, at, at); err != nil {
			t.Fatalf("insert news %d: %v", i, err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit news: %v", err)
	}

	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create frozen backfill: %v", err)
	}
	if backfill.TotalNewsCount != 16_001 {
		t.Fatalf("manifest total=%d, want 16001", backfill.TotalNewsCount)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, WindowStart: start, WindowEnd: cutoff,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link run: %v", err)
	}
	if err := insertCompletedBackfillItemsForTest(ctx, svc, backfill.ID, run.ID); err != nil {
		t.Fatalf("complete manifest: %v", err)
	}
	refreshed, err := svc.refreshNewsContextBackfillProgress(ctx, backfill)
	if err != nil {
		t.Fatalf("refresh progress: %v", err)
	}
	if refreshed.ProcessedNewsCount != 16_001 || refreshed.RemainingNewsCount != 0 || refreshed.MissingNewsCount != 0 {
		t.Fatalf("progress=%+v", refreshed)
	}

	duplicateRun, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, WindowStart: start.Add(time.Nanosecond), WindowEnd: cutoff.Add(time.Nanosecond),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create duplicate run: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, duplicateRun.ID); err != nil {
		t.Fatalf("link duplicate run: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: duplicateRun.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: "bulk-news-00000",
		Status: NewsContextRunItemCompleted, Disposition: NewsEventContextCovered,
	}}); err != nil {
		t.Fatalf("add duplicate completion: %v", err)
	}
	refreshed, err = svc.refreshNewsContextBackfillProgress(ctx, backfill)
	if err != nil {
		t.Fatalf("refresh duplicate progress: %v", err)
	}
	if refreshed.MissingNewsCount != 1 || refreshed.ProcessedNewsCount != 16_000 {
		t.Fatalf("duplicate completion was not rejected: %+v", refreshed)
	}
}

func TestNewsContextBackfillHourlyManifestHasNoNewsCountLimit(t *testing.T) {
	for _, count := range []int{51, 501, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			start := time.Now().In(time.Local).Truncate(time.Hour).Add(-2 * time.Hour)
			cutoff := start.Add(time.Hour)
			tx, err := svc.store.marketDB.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			stmt, err := tx.PrepareContext(ctx, `INSERT INTO stockv2_news_events
				(id,source,title,link_status,event_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("news-%04d", i)
				at := start.Add(time.Duration(i) * time.Millisecond)
				if _, err := stmt.ExecContext(ctx, id, "test", id, NewsEventLinkStatusPending, at, at, at); err != nil {
					t.Fatal(err)
				}
			}
			_ = stmt.Close()
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
				Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
			})
			if err != nil {
				t.Fatal(err)
			}
			run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
				WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
				Status: NewsContextRunStatusPending, WindowStart: start, WindowEnd: cutoff,
				ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
				t.Fatal(err)
			}
			ids, err := svc.store.ClaimNewsContextBackfillEvents(ctx, backfill.ID, run.ID, start, cutoff)
			if err != nil || len(ids) != count {
				t.Fatalf("claimed=%d want=%d err=%v", len(ids), count, err)
			}
			if err := svc.store.RequeueNewsContextRunEventItems(ctx, run.ID, ids); err != nil {
				t.Fatal(err)
			}
			got, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID})
			if err != nil || got != count {
				t.Fatalf("run items=%d want=%d err=%v", got, count, err)
			}
			seen := make(map[string]struct{}, count)
			chunks := 0
			for {
				items, err := svc.nextNewsContextRunItems(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(items) == 0 {
					break
				}
				chunks++
				characters := 0
				for _, item := range items {
					if _, duplicate := seen[item.ObjectID]; duplicate {
						t.Fatalf("news %s appeared in multiple automatic chunks", item.ObjectID)
					}
					seen[item.ObjectID] = struct{}{}
					size, err := svc.newsContextRunItemPromptCharacters(ctx, item)
					if err != nil {
						t.Fatal(err)
					}
					characters += size
					if err := svc.store.CompleteNewsContextRunItem(ctx, run.ID, item.ObjectID,
						NewsEventContextNoise, "", ""); err != nil {
						t.Fatal(err)
					}
				}
				if characters > newsContextInputTextLimit {
					t.Fatalf("automatic chunk characters=%d limit=%d", characters, newsContextInputTextLimit)
				}
			}
			if len(seen) != count || chunks == 0 {
				t.Fatalf("automatic chunks=%d covered=%d want=%d", chunks, len(seen), count)
			}
			refreshed, err := svc.refreshNewsContextBackfillProgress(ctx, backfill)
			if err != nil || refreshed.ProcessedNewsCount != count || refreshed.RemainingNewsCount != 0 || refreshed.MissingNewsCount != 0 {
				t.Fatalf("automatic chunk coverage=%+v err=%v", refreshed, err)
			}
			if count == 1001 && chunks < 2 {
				t.Fatalf("1001 news unexpectedly fit one automatic text chunk")
			}
		})
	}
}

func insertCompletedBackfillItemsForTest(ctx context.Context, svc *Service, backfillID, runID string) error {
	return svc.store.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT news_event_id,event_at
			FROM stockv2_news_context_backfill_news WHERE backfill_id=? ORDER BY event_at,news_event_id`, backfillID)
		if err != nil {
			return err
		}
		type item struct {
			id string
			at time.Time
		}
		items := make([]item, 0, 16_001)
		for rows.Next() {
			var value item
			if err := rows.Scan(&value.id, &value.at); err != nil {
				rows.Close()
				return err
			}
			items = append(items, value)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO stockv2_news_context_run_items
			(id,run_id,object_type,object_id,status,disposition,source_at,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := time.Now()
		for _, item := range items {
			if _, err := stmt.ExecContext(ctx, generateID(), runID, NewsContextRunItemNewsEvent,
				item.id, NewsContextRunItemCompleted, NewsEventContextCovered, item.at, now, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func TestNewsContextBackfillClaimIsManifestBoundIdempotentAndOrdered(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().In(time.Local).Truncate(time.Hour).Add(-2 * time.Hour)
	cutoff := start.Add(time.Hour)
	for _, offset := range []time.Duration{30 * time.Minute, 5 * time.Minute, 20 * time.Minute} {
		if _, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: offset.String(), EventAt: start.Add(offset)}); err != nil {
			t.Fatalf("create news: %v", err)
		}
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusPending, WindowStart: start, WindowEnd: cutoff,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link run: %v", err)
	}
	if count, err := svc.store.CountNewsContextBackfillManifest(ctx, backfill.ID); err != nil || count != 3 {
		t.Fatalf("manifest count=%d err=%v", count, err)
	}
	if oldest, found, err := svc.store.OldestPendingNewsContextBackfillEventAt(ctx, backfill.ID); err != nil || !found {
		t.Fatalf("oldest=%v found=%v err=%v", oldest, found, err)
	} else {
		t.Logf("claim window=%v..%v oldest=%v", start, cutoff, oldest)
	}
	first, err := svc.store.ClaimNewsContextBackfillEvents(ctx, backfill.ID, run.ID, start, cutoff)
	if err != nil || len(first) != 3 {
		t.Fatalf("first claim=%v err=%v", first, err)
	}
	second, err := svc.store.ClaimNewsContextBackfillEvents(ctx, backfill.ID, run.ID, start, cutoff)
	if err != nil || fmt.Sprint(second) != fmt.Sprint(first) {
		t.Fatalf("idempotent claim=%v want=%v err=%v", second, first, err)
	}
	if err := svc.store.RequeueNewsContextRunEventItems(ctx, run.ID, first); err != nil {
		t.Fatalf("persist run items: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(items) != 3 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for i := 1; i < len(items); i++ {
		if items[i].SourceAt.Before(items[i-1].SourceAt) {
			t.Fatalf("items not ordered by event time: %+v", items)
		}
	}
	if _, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "late", EventAt: start.Add(40 * time.Minute)}); err != nil {
		t.Fatalf("create late news: %v", err)
	}
	before, _ := svc.store.CountNewsContextBackfillManifest(ctx, backfill.ID)
	if before != 3 {
		t.Fatalf("late news entered frozen manifest early: %d", before)
	}
	if added, err := svc.store.AppendNewsContextBackfillManifest(ctx, backfill.ID, cutoff); err != nil || added != 1 {
		t.Fatalf("append late news added=%d err=%v", added, err)
	}
}

func TestHistoricalThreadSnapshotDoesNotReadOrReplaceFutureCurrentVersion(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "未来当前主题", CoreThesis: "未来结论", Stage: NewsThreadStageAccelerating,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	oldVersion, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "old-hour", AgentRunID: "old-agent", WindowType: NewsContextWindowHourly,
		VersionNo: 1, Title: "历史主题", CoreThesis: "历史结论", Stage: NewsThreadStageEmerging,
		EffectiveAt: day.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create old version: %v", err)
	}
	futureVersion, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "future-day", AgentRunID: "future-agent", WindowType: NewsContextWindowDaily,
		VersionNo: 2, Title: "未来当前主题", CoreThesis: "未来结论", Stage: NewsThreadStageAccelerating,
		EffectiveAt: day.Add(72 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create future version: %v", err)
	}
	thread.CurrentVersion = futureVersion.VersionNo
	thread.CurrentVersionID = futureVersion.ID
	thread.Title = futureVersion.Title
	thread.CoreThesis = futureVersion.CoreThesis
	thread.Stage = futureVersion.Stage
	thread.LastChangedAt = futureVersion.EffectiveAt
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatalf("promote future current: %v", err)
	}

	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "daily", RangeStartAt: day, CutoffAt: day.Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusRunning, WindowStart: day, WindowEnd: day.Add(24 * time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create historical daily: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatalf("link historical daily: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: thread.ID,
		ThreadID: thread.ID, VersionID: oldVersion.ID, Status: NewsContextRunItemPending,
	}}); err != nil {
		t.Fatalf("add historical input: %v", err)
	}
	agentRunID := "historical-daily-agent"
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, agentRunID, []string{thread.ID}); err != nil {
		t.Fatalf("mark historical input: %v", err)
	}
	pack, err := svc.buildNewsContextAggregationPack(ctx, run, []NewsContextRunItem{{
		ObjectType: NewsContextRunItemThread, ObjectID: thread.ID, VersionID: oldVersion.ID,
	}})
	if err != nil || len(pack.InputThreads) != 1 || pack.InputThreads[0].Title != oldVersion.Title {
		t.Fatalf("historical pack=%+v err=%v", pack.InputThreads, err)
	}
	_, err = svc.store.ApplyNewsContextBatch(ctx, run.ID, agentRunID, NewsContextWindowDaily, NewsContextReport{
		SchemaVersion: NewsContextResultSchemaVersion, RunID: run.ID, WindowType: NewsContextWindowDaily,
		UnchangedThreadIDs: []string{thread.ID},
	})
	if err != nil {
		t.Fatalf("apply historical unchanged: %v", err)
	}
	versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(versions) != 1 || versions[0].Title != oldVersion.Title || versions[0].CoreThesis != oldVersion.CoreThesis {
		t.Fatalf("historical checkpoint=%+v err=%v", versions, err)
	}
	current, err := svc.store.GetNewsThread(ctx, thread.ID)
	if err != nil || current.CurrentVersionID != futureVersion.ID || current.Stage != futureVersion.Stage {
		t.Fatalf("future current regressed: %+v err=%v", current, err)
	}
}

func TestNewsContextBackfillPersistsEveryEmptyHierarchyWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "covered seed", EventAt: day.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: day.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstHour, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, WindowStart: day, WindowEnd: day.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, firstHour.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: firstHour.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID,
		Status: NewsContextRunItemCompleted, Disposition: NewsEventContextCovered, SourceAt: event.EventAt,
	}}); err != nil {
		t.Fatal(err)
	}
	previousCount := 1
	for step := 0; step < 30; step++ {
		backfill, _ = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
		if backfill.Phase != "hourly" {
			break
		}
		if err := svc.startNextNewsContextBackfillHour(ctx, backfill); err != nil {
			t.Fatalf("complete empty hour step %d: %v", step, err)
		}
		hours, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowHourly)
		if err != nil || len(hours)-previousCount > 1 {
			t.Fatalf("hour step persisted %d units, err=%v", len(hours)-previousCount, err)
		}
		previousCount = len(hours)
	}
	backfill, _ = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	hours, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowHourly)
	if err != nil || len(hours) != 24 || backfill.Phase != "four_hour" {
		t.Fatalf("hours=%d phase=%s err=%v", len(hours), backfill.Phase, err)
	}
	previousCount = 0
	for step := 0; step < 10; step++ {
		backfill, _ = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
		if backfill.Phase != "four_hour" {
			break
		}
		if err := svc.startNextNewsContextBackfillFourHour(ctx, backfill); err != nil {
			t.Fatalf("complete empty four-hour step %d: %v", step, err)
		}
		fourHours, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowFourHour)
		if err != nil || len(fourHours)-previousCount > 1 {
			t.Fatalf("four-hour step persisted %d units, err=%v", len(fourHours)-previousCount, err)
		}
		previousCount = len(fourHours)
	}
	backfill, _ = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	fourHours, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowFourHour)
	if err != nil || len(fourHours) != 6 || backfill.Phase != "daily" {
		t.Fatalf("fourHours=%d phase=%s err=%v", len(fourHours), backfill.Phase, err)
	}
	if err := svc.startNextNewsContextBackfillDaily(ctx, backfill); err != nil {
		t.Fatalf("complete empty day: %v", err)
	}
	backfill, _ = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	days, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowDaily)
	if err != nil || len(days) != 1 || backfill.Phase != "daily" {
		t.Fatalf("days=%d phase=%s err=%v", len(days), backfill.Phase, err)
	}
	if err := svc.startNextNewsContextBackfillDaily(ctx, backfill); err != nil {
		t.Fatalf("advance after empty day: %v", err)
	}
	backfill, _ = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if backfill.Phase != "late_scan" {
		t.Fatalf("phase after empty daily checkpoint=%s", backfill.Phase)
	}
}

func TestFinalCurrentDailyUsesDirectActiveThemesForNonAlignedWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "当前主题", CoreThesis: "最终完整复核必须读取当前主题", Stage: NewsThreadStageEmerging,
	})
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().Truncate(time.Minute).Add(37 * time.Second)
	start := end.Add(-3*time.Hour - 17*time.Minute)
	run, err := svc.startNewsContextRun(ctx, RequestStartNewsContextRun{
		WindowType: NewsContextWindowDaily, StartAt: start.Format(time.RFC3339Nano),
		EndAt: end.Format(time.RFC3339Nano), RequestedBy: "system",
	}, NewsContextTriggerManual, false)
	if err != nil {
		t.Fatalf("start non-aligned final daily: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ObjectType == NewsContextRunItemThread && (item.ObjectID == thread.ID || item.ThreadID == thread.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-aligned final daily did not seed current active theme: %+v", items)
	}
}
