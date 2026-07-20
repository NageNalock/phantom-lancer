package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestBuildNewsContextBackfillStageProgressShowsEveryStage(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	item := NewsContextBackfill{
		Status:       NewsContextBackfillStatusRunning,
		Phase:        NewsContextWindowDaily,
		RangeStartAt: start,
		CutoffAt:     start.Add(48 * time.Hour),
		CurrentRunID: "daily-current",
	}
	windows := map[string]newsContextBackfillWindowProgress{
		NewsContextWindowHourly: {
			CompletedWindowCount: 48,
			ProcessedItemCount:   1000, TotalItemCount: 1000,
		},
		NewsContextWindowFourHour: {
			CompletedWindowCount: 12,
			ProcessedItemCount:   300, TotalItemCount: 300,
		},
		NewsContextWindowDaily: {
			CompletedWindowCount: 1,
			ProcessedItemCount:   180, TotalItemCount: 220, PendingItemCount: 40,
			CompletedDurationSeconds: 3600,
		},
	}
	current := NewsContextRun{
		ID: "daily-current", WindowType: NewsContextWindowDaily,
		TriggerType: NewsContextTriggerBackfill, Status: NewsContextRunStatusRunning,
		Phase: newsContextRunPhaseConverging, WindowStart: start.Add(24 * time.Hour),
		WindowEnd: start.Add(48 * time.Hour), InputCount: 220,
		ProcessedCount: 180, PendingCount: 40, StartedAt: time.Now().Add(-time.Hour),
	}

	progress := buildNewsContextBackfillStageProgress(item, windows, &current)
	if len(progress) != 8 {
		t.Fatalf("stage count=%d, want 8: %+v", len(progress), progress)
	}
	assertStage := func(index int, phase, status string) NewsContextBackfillStageProgress {
		t.Helper()
		stage := progress[index]
		if stage.Phase != phase || stage.Status != status {
			t.Fatalf("stage[%d]=%+v, want phase=%s status=%s", index, stage, phase, status)
		}
		return stage
	}
	hourly := assertStage(0, NewsContextWindowHourly, NewsContextRunStatusCompleted)
	if hourly.CompletedWindowCount != 48 || hourly.TotalWindowCount != 48 {
		t.Fatalf("hourly progress=%+v", hourly)
	}
	assertStage(1, NewsContextWindowFourHour, NewsContextRunStatusCompleted)
	daily := assertStage(2, NewsContextWindowDaily, NewsContextRunStatusRunning)
	if daily.CompletedWindowCount != 1 || daily.TotalWindowCount != 2 ||
		daily.ProcessedItemCount != 180 || daily.TotalItemCount != 220 || daily.PendingItemCount != 40 ||
		daily.CurrentRunPhase != newsContextRunPhaseConverging ||
		daily.CurrentWindowProgress <= 0.9 || daily.OverallProgress <= 0.95 ||
		daily.ElapsedSeconds < 3500 || daily.EstimatedRemainingSeconds <= 0 {
		t.Fatalf("daily progress=%+v", daily)
	}
	for index, phase := range []string{"late_scan", "final_daily", newsContextBackfillPhaseIndexing, "final_review", "finalizing"} {
		assertStage(index+3, phase, NewsContextRunStatusPending)
	}
}

func TestBuildNewsContextBackfillStageProgressSeparatesFinalDailyAndReview(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	item := NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review",
		RangeStartAt: start, CutoffAt: start.Add(24 * time.Hour),
		CurrentRunID: "final", FinalReviewRunID: "final",
		DailyOutputCount: 12, ReviewLinkedCount: 0, ReviewMissingCount: 12,
	}
	current := NewsContextRun{
		ID: "final", WindowType: NewsContextWindowDaily,
		TriggerType: NewsContextTriggerManual, Status: NewsContextRunStatusWaitingReview,
		Phase: "waiting_review", ReviewStatus: NewsContextReviewRunning,
		WindowStart: item.CutoffAt, WindowEnd: item.CutoffAt.Add(time.Hour),
		InputCount: 50, ProcessedCount: 50,
	}

	progress := buildNewsContextBackfillStageProgress(item, nil, &current)
	for _, expected := range []struct {
		index  int
		phase  string
		status string
	}{
		{3, "late_scan", NewsContextRunStatusCompleted},
		{4, "final_daily", NewsContextRunStatusCompleted},
		{5, newsContextBackfillPhaseIndexing, NewsContextRunStatusCompleted},
		{6, "final_review", NewsContextRunStatusRunning},
		{7, "finalizing", NewsContextRunStatusPending},
	} {
		stage := progress[expected.index]
		if stage.Phase != expected.phase || stage.Status != expected.status {
			t.Fatalf("stage[%d]=%+v, want phase=%s status=%s", expected.index, stage, expected.phase, expected.status)
		}
	}
	if progress[7].TotalItemCount != 12 || progress[7].PendingItemCount != 12 {
		t.Fatalf("finalizing progress=%+v", progress[7])
	}
}

func TestNewsContextBackfillAgentProgressAggregatesModelAttempts(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	started := time.Now().Add(-2 * time.Minute)
	finished := started.Add(time.Minute)
	if _, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "news_context_run",
		TriggerObjectID: "context-progress", Status: AgentRunStatusFailed,
		StartedAt: started, FinishedAt: finished,
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "news_context_run",
		TriggerObjectID: "context-progress",
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := svc.store.NewsContextBackfillAgentProgress(ctx, "context-progress")
	if err != nil {
		t.Fatal(err)
	}
	if progress.AttemptCount != 1 || progress.FailedCount != 1 ||
		progress.DurationSeconds < 59 || progress.DurationSeconds > 61 {
		t.Fatalf("agent progress=%+v", progress)
	}
}
