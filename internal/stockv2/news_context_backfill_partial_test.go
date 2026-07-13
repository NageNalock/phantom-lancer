package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestNewsContextBackfillBuildsTrailingPartialParentsToCutoff(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	day := time.Date(2026, 7, 13, 0, 0, 0, 0, time.Local)
	cutoff := day.Add(10 * time.Hour)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "partial hierarchy seed", EventAt: day.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create seed event: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly",
		RangeStartAt: day, CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	firstHour, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusCompleted, Phase: "completed",
		WindowStart: day, WindowEnd: day.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create first hourly checkpoint: %v", err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, firstHour.ID); err != nil {
		t.Fatalf("link first hourly checkpoint: %v", err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: firstHour.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID,
		Status: NewsContextRunItemCompleted, Disposition: NewsEventContextNoise, SourceAt: event.EventAt,
	}}); err != nil {
		t.Fatalf("complete first hourly checklist: %v", err)
	}

	backfill = advanceNewsContextBackfillTestPhase(t, svc, backfill, "four_hour", svc.startNextNewsContextBackfillHour)
	backfill = advanceNewsContextBackfillTestPhase(t, svc, backfill, "daily", svc.startNextNewsContextBackfillFourHour)
	fourHourBeforeDaily, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowFourHour)
	if err != nil || len(fourHourBeforeDaily) != 3 {
		t.Fatalf("four-hour checkpoints before daily=%d err=%v", len(fourHourBeforeDaily), err)
	}
	backfill = advanceNewsContextBackfillTestPhase(t, svc, backfill, "late_scan", svc.startNextNewsContextBackfillDaily)

	hourly, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowHourly)
	if err != nil || len(hourly) != 10 {
		t.Fatalf("hourly checkpoints=%d err=%v", len(hourly), err)
	}
	fourHour, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowFourHour)
	if err != nil || len(fourHour) != 3 {
		all, _ := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, "")
		t.Fatalf("four-hour checkpoints=%d all=%+v err=%v", len(fourHour), all, err)
	}
	if got := fourHour[len(fourHour)-1]; !got.WindowStart.Equal(day.Add(8*time.Hour)) || !got.WindowEnd.Equal(cutoff) {
		t.Fatalf("trailing four-hour checkpoint=%v..%v", got.WindowStart, got.WindowEnd)
	}
	daily, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, NewsContextWindowDaily)
	if err != nil || len(daily) != 1 {
		t.Fatalf("daily checkpoints=%d err=%v", len(daily), err)
	}
	if !daily[0].WindowStart.Equal(day) || !daily[0].WindowEnd.Equal(cutoff) {
		t.Fatalf("partial daily checkpoint=%v..%v", daily[0].WindowStart, daily[0].WindowEnd)
	}
	if backfill.Phase != "late_scan" {
		t.Fatalf("daily phase=%s", backfill.Phase)
	}
}

func advanceNewsContextBackfillTestPhase(
	t *testing.T,
	svc *Service,
	backfill NewsContextBackfill,
	wantPhase string,
	step func(context.Context, NewsContextBackfill) error,
) NewsContextBackfill {
	t.Helper()
	ctx := context.Background()
	for attempt := 0; attempt < 64 && backfill.Phase != wantPhase; attempt++ {
		if err := step(ctx, backfill); err != nil {
			t.Fatalf("advance historical phase %s -> %s: %v", backfill.Phase, wantPhase, err)
		}
		var err error
		backfill, err = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
		if err != nil {
			t.Fatalf("reload historical phase %s: %v", wantPhase, err)
		}
	}
	if backfill.Phase != wantPhase {
		t.Fatalf("historical phase=%s, want %s", backfill.Phase, wantPhase)
	}
	return backfill
}
