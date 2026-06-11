package codexclient

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

func TestParseCronAndMatch(t *testing.T) {
	if err := validateCronExpr("0 9 * * *"); err != nil {
		t.Fatalf("expected valid cron, got %v", err)
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "5-1 * * * *"} {
		if err := validateCronExpr(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}

	schedule, err := parseCron("30 8 * * 1-5")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Monday 2026-06-08 08:30 UTC should match weekday 08:30.
	monday := time.Date(2026, 6, 8, 8, 30, 0, 0, time.UTC)
	if !schedule.matches(monday) {
		t.Fatal("expected weekday 08:30 to match")
	}
	// Saturday must not match a Mon-Fri schedule.
	saturday := time.Date(2026, 6, 13, 8, 30, 0, 0, time.UTC)
	if schedule.matches(saturday) {
		t.Fatal("expected Saturday to be excluded")
	}
	// Wrong minute must not match.
	if schedule.matches(time.Date(2026, 6, 8, 8, 31, 0, 0, time.UTC)) {
		t.Fatal("expected 08:31 to be excluded")
	}
}

func TestParseCronDayOfMonthOrDayOfWeek(t *testing.T) {
	// When both DOM and DOW are restricted, standard cron matches if either does.
	schedule, err := parseCron("0 0 1 * 1")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-06-01 is a Monday: matches both, clearly true.
	if !schedule.matches(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("expected day 1 to match")
	}
	// 2026-06-08 is a Monday (DOW match) but not day 1: should still match.
	if !schedule.matches(time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("expected Monday to match via day-of-week")
	}
	// 2026-06-02 is a Tuesday and not day 1: should not match.
	if schedule.matches(time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("expected Tuesday non-first to be excluded")
	}
}

func TestNextCronTimeIsStrictlyAfter(t *testing.T) {
	from := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC) // already matches 0 9 * * *
	next, ok := nextCronTime("0 9 * * *", from)
	if !ok {
		t.Fatal("expected a next cron time")
	}
	if !next.After(from) {
		t.Fatalf("expected next %s to be strictly after %s", next, from)
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Fatalf("expected 09:00, got %s", next)
	}
	if next.Day() != 9 {
		t.Fatalf("expected next day to roll to the 9th, got %s", next)
	}
}

func TestNextAutomationTimeIntervalAndCron(t *testing.T) {
	from := "2026-06-08T09:00:00Z"
	got := nextAutomationTime(map[string]any{"intervalMinutes": float64(60)}, from)
	want := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if got != want {
		t.Fatalf("interval next = %s, want %s", got, want)
	}

	// Interval is clamped to the documented minimum (15 minutes).
	clamped := nextAutomationTime(map[string]any{"intervalMinutes": float64(1)}, from)
	wantClamped := time.Date(2026, 6, 8, 9, 15, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if clamped != wantClamped {
		t.Fatalf("clamped interval = %s, want %s", clamped, wantClamped)
	}

	cronNext := nextAutomationTime(map[string]any{"cron": "0 9 * * *"}, from)
	if parsed, err := time.Parse(time.RFC3339Nano, cronNext); err != nil || parsed.Hour() != 9 {
		t.Fatalf("cron next = %s (err %v)", cronNext, err)
	}
}

func newP2TestService(t *testing.T) (*Service, *storage.Store, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	return svc, store, dir
}

func TestTriageInboxExcludesArchivedThreadReviewComments(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newP2TestService(t)

	thread, err := store.CreateCodexCliThread(ctx, storage.CodexCliThread{Status: "idle", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCodexCliReviewComment(ctx, storage.CodexCliReviewComment{ThreadID: thread.ID, FilePath: "main.go", Body: "needs a guard", Status: "open"}); err != nil {
		t.Fatal(err)
	}

	inbox, err := svc.TriageInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.ReviewComments) != 1 {
		t.Fatalf("expected 1 open review comment before archive, got %d", len(inbox.ReviewComments))
	}

	if _, err := svc.ArchiveThread(ctx, thread.ID); err != nil {
		t.Fatal(err)
	}

	inbox, err = svc.TriageInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.ReviewComments) != 0 {
		t.Fatalf("expected archived thread review comments to leave triage, got %d", len(inbox.ReviewComments))
	}
}

func TestTriageInboxExcludesArchivedFailedTurns(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newP2TestService(t)

	thread, err := store.CreateCodexCliThread(ctx, storage.CodexCliThread{Status: "failed", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCodexCliTurn(ctx, storage.CodexCliTurn{ThreadID: thread.ID, Status: "failed", SandboxMode: "read-only", ApprovalPolicy: "on-request"}); err != nil {
		t.Fatal(err)
	}

	inbox, err := svc.TriageInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.FailedTurns) != 1 {
		t.Fatalf("expected 1 failed turn before archive, got %d", len(inbox.FailedTurns))
	}

	if _, err := svc.ArchiveThread(ctx, thread.ID); err != nil {
		t.Fatal(err)
	}
	inbox, err = svc.TriageInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.FailedTurns) != 0 {
		t.Fatalf("expected archived failed turns to leave triage, got %d", len(inbox.FailedTurns))
	}
}

func TestNotifyPublishesSummaryOnlyEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, _, _ := newP2TestService(t)

	sub := svc.hub.Subscribe(ctx, "codex.notifications", "default")

	svc.notify(ctx, storage.CodexCliNotification{
		Scope:     "codex.thread",
		ScopeID:   "thr-1",
		EventType: "codex.thread.turn_failed",
		Title:     "Codex turn failed",
		Summary:   "boom",
		Severity:  "danger",
		Payload:   map[string]any{"threadId": "thr-1", "secret": "should-not-ship"},
	})

	select {
	case event := <-sub:
		if event.Type != "codex.notification.created" {
			t.Fatalf("unexpected event type %q", event.Type)
		}
		if event.Payload["scope"] != "codex.thread" || event.Payload["severity"] != "danger" {
			t.Fatalf("unexpected summary payload: %v", event.Payload)
		}
		if _, leaked := event.Payload["secret"]; leaked {
			t.Fatal("notification SSE payload must stay summary-only")
		}
		if _, hasSummary := event.Payload["summary"]; hasSummary {
			t.Fatal("notification SSE payload must not carry the summary text")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a notification SSE event")
	}
}

func TestUpdateAutomationPatchPreservesUnsetFields(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newP2TestService(t)

	thread, err := store.CreateCodexCliThread(ctx, storage.CodexCliThread{Status: "idle", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateAutomation(ctx, AutomationInput{Kind: "thread_wakeup", ThreadID: thread.ID, Title: "nightly", Prompt: "summarize", Enabled: true, Schedule: map[string]any{"intervalMinutes": float64(120)}})
	if err != nil {
		t.Fatal(err)
	}

	disabled := false
	updated, err := svc.UpdateAutomation(ctx, created.ID, AutomationPatch{Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatal("expected enabled to be patched to false")
	}
	if updated.Title != "nightly" {
		t.Fatalf("expected title preserved, got %q", updated.Title)
	}
	if updated.PromptSummary != "summarize" {
		t.Fatalf("expected prompt preserved, got %q", updated.PromptSummary)
	}
	if got := scheduleInt(updated.Schedule, "intervalMinutes", 0, 0, 0); got != 120 {
		t.Fatalf("expected schedule preserved at 120, got %d", got)
	}
}
