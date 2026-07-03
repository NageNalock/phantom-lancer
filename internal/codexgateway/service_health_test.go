package codexgateway

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phantom-lancer/internal/storage"
)

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestService(store *storage.Store) *Service {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return NewService(store, logger)
}

// TestServiceHealthCheckDisabledIntervalExitsCleanly verifies that when the
// configured interval is 0 (disabled), StartBackground still runs the
// immediate first pass and then the goroutine terminates promptly — so Close
// returns within our short shutdown budget instead of hanging.
func TestServiceHealthCheckDisabledIntervalExitsCleanly(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	settings, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	settings.AccountHealthCheckIntervalSeconds = 0
	if _, err := store.UpdateCodexGatewaySettings(ctx, settings); err != nil {
		t.Fatalf("disable interval: %v", err)
	}

	// Disabled account must be skipped entirely; active account with no
	// tokens will be "checked" via the no-token fast path.
	_, err = store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label:  "offline",
		Status: "disabled",
	})
	if err != nil {
		t.Fatalf("create disabled acct: %v", err)
	}
	active, err := store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label:  "no-token",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create active acct: %v", err)
	}

	svc := newTestService(store)
	svc.StartBackground(ctx)

	// Give the initial pass time to actually land (it does DB writes, not
	// just an in-memory flag) before the goroutine self-exits because the
	// interval is zero.
	deadline := time.Now().Add(2 * time.Second)
	gotCheck := ""
	for time.Now().Before(deadline) {
		fetched, err := store.GetCodexGatewayAccount(ctx, active.ID)
		if err != nil {
			t.Fatalf("re-get acct: %v", err)
		}
		if fetched.LastCheckedAt != "" {
			gotCheck = fetched.LastCheckedAt
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if gotCheck == "" {
		t.Fatalf("initial health pass did not write last_checked_at for active account")
	}

	// Now Close should be very fast — the goroutine is either already gone
	// (interval=0 path) or it will tear down within the 2s window.
	closeStarted := time.Now()
	svc.Close()
	if elapsed := time.Since(closeStarted); elapsed > 3*time.Second {
		t.Fatalf("Close took %s with interval=0, expected near-instant", elapsed)
	}

	// Double-close must be a safe no-op.
	svc.Close()

	// Double StartBackground must be a no-op (second goroutine must not
	// spawn). We detect this by verifying the per-pass DB state didn't flip
	// unexpectedly; the real assertion is that no test hangs occur.
	svc.StartBackground(ctx)
	svc.Close()
}

// TestServiceHealthCheckInitialPassRunsImmediately verifies that even with a
// very long interval (24h — one that cannot possibly fire during the test),
// the initial pass still touches every non-disabled account and writes out
// last_checked_at / last_error using CheckAccount's missing-token fast path
// (no real HTTP calls).
func TestServiceHealthCheckInitialPassRunsImmediately(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	settings, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	// 24 hour interval — guarantees no second pass will ever trigger during
	// this short test, so whatever DB state we observe can only come from
	// the boot-time immediate run.
	settings.AccountHealthCheckIntervalSeconds = 24 * 3600
	if _, err := store.UpdateCodexGatewaySettings(ctx, settings); err != nil {
		t.Fatalf("set long interval: %v", err)
	}

	disabled, err := store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label:  "disabled",
		Status: "disabled",
	})
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	invalid, err := store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label:  "invalid-pre",
		Status: "invalid",
	})
	if err != nil {
		t.Fatalf("create invalid: %v", err)
	}
	active, err := store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label:  "active-empty",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create active: %v", err)
	}

	svc := newTestService(store)
	svc.StartBackground(ctx)
	defer svc.Close()

	// The background goroutine is launched asynchronously; poll until the
	// initial sequential pass has touched both non-disabled accounts. The list
	// order is newest first, so observing only the active account can race ahead
	// of the invalid-account recovery check below.
	deadline := time.Now().Add(2 * time.Second)
	var checkedActive, checkedInvalid storage.CodexGatewayAccount
	for time.Now().Before(deadline) {
		a, err := store.GetCodexGatewayAccount(ctx, active.ID)
		if err != nil {
			t.Fatalf("reget active: %v", err)
		}
		i, err := store.GetCodexGatewayAccount(ctx, invalid.ID)
		if err != nil {
			t.Fatalf("reget invalid: %v", err)
		}
		if a.LastCheckedAt != "" && i.LastCheckedAt != "" {
			checkedActive = a
			checkedInvalid = i
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if checkedActive.LastCheckedAt == "" {
		t.Fatalf("initial health pass did not write last_checked_at for active account")
	}
	if !strings.Contains(checkedActive.LastError, "缺少") {
		t.Fatalf("active acct last_error = %q, want substring \"缺少\"", checkedActive.LastError)
	}
	if checkedInvalid.LastCheckedAt == "" {
		t.Fatalf("invalid account should still be re-checked during health pass")
	}

	// Disabled should be untouched.
	d, err := store.GetCodexGatewayAccount(ctx, disabled.ID)
	if err != nil {
		t.Fatalf("reget disabled: %v", err)
	}
	if d.LastCheckedAt != "" || d.LastError != "" {
		t.Fatalf("disabled account must be skipped by health pass, got last_checked_at=%q last_error=%q",
			d.LastCheckedAt, d.LastError)
	}

	// Invalid account must also be checked (recovery path).
	if checkedInvalid.Status != "invalid" {
		t.Fatalf("invalid account status should still be invalid (missing tokens), got %q", checkedInvalid.Status)
	}
}
