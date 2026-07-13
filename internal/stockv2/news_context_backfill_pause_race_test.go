package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewsContextBackfillPausePreventsReservedFragmentFromStarting(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().In(time.Local).Truncate(time.Hour).Add(-2 * time.Hour)
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly",
		RangeStartAt: start, CutoffAt: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusPending, Phase: "collecting",
		WindowStart: start, WindowEnd: start.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := svc.store.ReserveNewsContextBackfillRun(ctx, backfill.ID, run); err != nil {
		t.Fatalf("reserve run: %v", err)
	}
	if _, err := svc.PauseNewsContextBackfill(ctx); err != nil {
		t.Fatalf("pause backfill: %v", err)
	}
	if _, err := svc.store.BeginNewsContextBackfillFragment(ctx, backfill.ID, run.ID); !errors.Is(err, ErrNewsContextBackfillState) {
		t.Fatalf("begin after pause error=%v", err)
	}
	stored, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || stored.Status != NewsContextRunStatusPending || stored.Phase != "collecting" {
		t.Fatalf("paused fragment changed run=%+v err=%v", stored, err)
	}
}

func TestNewsContextBackfillPausePreventsNewWindowReservation(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().In(time.Local).Truncate(time.Hour).Add(-2 * time.Hour)
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly",
		RangeStartAt: start, CutoffAt: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	if _, err := svc.PauseNewsContextBackfill(ctx); err != nil {
		t.Fatalf("pause backfill: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusPending, Phase: "collecting",
		WindowStart: start, WindowEnd: start.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := svc.store.ReserveNewsContextBackfillRun(ctx, backfill.ID, run); !errors.Is(err, ErrNewsContextBackfillState) {
		t.Fatalf("reserve after pause error=%v", err)
	}
	linked, err := svc.store.ListNewsContextBackfillRuns(ctx, backfill.ID, "")
	if err != nil || len(linked) != 0 {
		t.Fatalf("paused backfill linked runs=%+v err=%v", linked, err)
	}
}
