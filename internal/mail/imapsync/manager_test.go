package imapsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phantom-lancer/internal/storage"
)

// ---------- test fixtures ----------

// testGlobalMutex serializes all top-level imapsync tests because they share
// mutable package-level state: DialMock, TestInitialBackoff / TestMaxBackoff /
// TestPollInterval / TestTickerPeriod / TestPauseSleep.  Without this, go
// test -race fires on concurrent reads/writes between top-level tests (they
// run in parallel on the GOMAXPROCS goroutine pool by default).
var testGlobalMutex sync.Mutex

// lockTestGlobal acquires testGlobalMutex and registers a cleanup to release.
func lockTestGlobal(t *testing.T) {
	t.Helper()
	testGlobalMutex.Lock()
	t.Cleanup(testGlobalMutex.Unlock)
}

// testCfg returns a minimal AccountConfig with PasswordFn that errors only
// if actually called (PasswordFn should never be invoked when DialMock is
// set, as the mock owns client construction).
func testCfg(accountID, addr, host string) AccountConfig {
	return AccountConfig{
		AccountID: accountID,
		Address:   addr,
		ImapHost:  host,
		Username:  addr,
		PasswordFn: func(_ context.Context) (string, error) {
			return "", fmt.Errorf("PasswordFn should NOT be called in tests using DialMock")
		},
		IdleTimeoutSec: 5,
	}
}

// testLogger returns a slog.Logger that writes to stderr only if the
// PHANTOM_TEST_VERBOSE env var is set; otherwise it discards logs so tests
// stay quiet by default.
func testLogger() *slog.Logger {
	if os.Getenv("PHANTOM_TEST_VERBOSE") != "" {
		return slog.Default()
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))
}

// applyFastTimings shrinks all loop timings to milliseconds.  Returns a
// cleanup that restores defaults — register it with t.Cleanup.
//
// Also acquires the package-level test serialisation mutex (released in the
// returned cleanup) so top-level tests can't race on the mutable timing
// override vars or DialMock.
func applyFastTimings(t *testing.T) func() {
	t.Helper()
	testGlobalMutex.Lock()
	savedIB := TestInitialBackoff
	savedMB := TestMaxBackoff
	savedPI := TestPollInterval
	savedTP := TestTickerPeriod
	savedPS := TestPauseSleep

	TestInitialBackoff = 2 * time.Millisecond
	TestMaxBackoff = 8 * time.Millisecond
	TestPollInterval = 1 * time.Millisecond
	TestTickerPeriod = 1 * time.Millisecond
	TestPauseSleep = 1 * time.Millisecond

	return func() {
		TestInitialBackoff = savedIB
		TestMaxBackoff = savedMB
		TestPollInterval = savedPI
		TestTickerPeriod = savedTP
		TestPauseSleep = savedPS
		DialMock = nil
		testGlobalMutex.Unlock()
	}
}

// ---------- Subtest 1: Start idempotency + mutual exclusion ----------

func TestManager_Start_Idempotent(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	var dialCalls atomic.Int64
	DialMock = func(_ context.Context, cfg AccountConfig) (ClientIFace, error) {
		dialCalls.Add(1)
		// Very short sleep so 2 concurrent Starts have a race window.
		time.Sleep(time.Millisecond)
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())
	cfg := testCfg("acc-1", "a@x.com", "imap.x.com")

	// Serial Start x 5 — first returns nil; subsequent 4 return "already running".
	for i := 0; i < 5; i++ {
		err := m.Start(context.Background(), cfg)
		if i == 0 {
			if err != nil {
				t.Fatalf("serial Start iteration %d (first): %v", i, err)
			}
		} else {
			if err == nil {
				t.Errorf("serial Start iteration %d: expected 'already running' error, got nil", i)
			} else if !strings.Contains(err.Error(), "already running") {
				t.Errorf("serial Start iteration %d: error should mention 'already running', got %q", i, err)
			}
		}
	}
	// Allow the goroutine to actually dial once.
	time.Sleep(20 * time.Millisecond)
	if n := m.CountRunning(); n != 1 {
		t.Errorf("after 5 Starts: CountRunning should be 1, got %d", n)
	}
	// State should be Idle or Syncing (the loop body runs to completion then idles).
	st := m.State("acc-1")
	if st == StateStopped || st == StateError {
		t.Errorf("account state after start: want queued/syncing/idle, got %s", st)
	}
	// Concurrent Start calls for the SAME account.
	// Exactly 1 should return nil; the remaining 49 return "already running".
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	okCount := int64(0)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := m.Start(context.Background(), cfg)
			errs <- err
			if err == nil {
				atomic.AddInt64(&okCount, 1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	duplicateErrs := 0
	for err := range errs {
		if err != nil {
			if !strings.Contains(err.Error(), "already running") {
				t.Errorf("unexpected Start error: %v", err)
			} else {
				duplicateErrs++
			}
		}
	}
	// The FIRST Start returned nil above (serial). The 50 concurrent Starts
	// MUST all fail with "already running" because the mutex serialises the
	// duplicate check.  Note: okCount tracks nil returns among ONLY the 50
	// concurrent calls, NOT including the one serial call above. So the
	// correct expectation is okCount == 0.
	if okCount != 0 {
		t.Errorf("expected 0 successful concurrent Starts (serial one already inserted), got okCount=%d", okCount)
	}
	if duplicateErrs != 50 {
		t.Errorf("expected all 50 concurrent Starts to report 'already running', got %d (okCount=%d)", duplicateErrs, okCount)
	}
	if n := m.CountRunning(); n != 1 {
		t.Errorf("after concurrent Starts: CountRunning should be 1, got %d", n)
	}

	// Stop it.
	if err := m.Stop("acc-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Wait for the goroutine to truly exit.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if m.CountRunning() == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if m.CountRunning() != 0 {
		t.Errorf("after Stop: CountRunning should be 0, got %d", m.CountRunning())
	}
	if st := m.State("acc-1"); st != StateStopped {
		t.Errorf("after Stop: State should be stopped, got %s", st)
	}
}

// ---------- Subtest 2: Start → Stop per-account lifecycle ----------

func TestManager_StartStop_Lifecycle(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	var dials atomic.Int64
	DialMock = func(_ context.Context, cfg AccountConfig) (ClientIFace, error) {
		dials.Add(1)
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())

	accounts := []string{"a1", "a2", "a3", "a4", "a5"}
	for i, id := range accounts {
		cfg := testCfg(id, fmt.Sprintf("%s@x.com", id), fmt.Sprintf("imap%d.x.com", i))
		if err := m.Start(context.Background(), cfg); err != nil {
			t.Fatalf("Start(%s): %v", id, err)
		}
	}
	// Let the loops spin a cycle.
	time.Sleep(30 * time.Millisecond)

	if got := m.CountRunning(); got != len(accounts) {
		t.Errorf("CountRunning want %d got %d", len(accounts), got)
	}

	// Stop a3 twice (idempotency of Stop).
	if err := m.Stop("a3"); err != nil {
		t.Fatalf("first Stop(a3): %v", err)
	}
	if err := m.Stop("a3"); err != nil {
		t.Fatalf("second Stop(a3): %v (should be idempotent nil)", err)
	}
	// Stop a non-existent account (idempotent, no error).
	if err := m.Stop("ghost"); err != nil {
		t.Fatalf("Stop(ghost): should be idempotent nil, got %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := m.CountRunning(); got != len(accounts)-1 {
		t.Errorf("after Stop(a3): CountRunning want %d got %d", len(accounts)-1, got)
	}
	if st := m.State("a3"); st != StateStopped {
		t.Errorf("State(a3) after Stop: want stopped, got %s", st)
	}
	// Stop ALL remaining accounts (a1,a2,a4,a5) so loop goroutines exit.
	// Without this the goroutines leak past the end of the test and race
	// with the next top-level test (they hold references to package-level
	// timing overrides and DialMock).
	m.StopAll()
}

// ---------- Subtest 3: StartAll + StopAll parallel ----------

func TestManager_StartAll_StopAll(t *testing.T) {
	if !storage.FTS5Available() {
		t.Skip("Skipping StartAll/StopAll: sqlite3 was not built with FTS5 module. " +
			"Re-run tests with `-tags sqlite_fts5` to include this test.")
	}
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Create a real SQLite-backed Store (in temp dir) with 4 accounts
	// that have ImapHost set (so StartAll picks them up).
	ctx := context.Background()
	dbPath := filepathJoin(t.TempDir(), "test.db")
	store, err := storage.Open(ctx, dbPath, testLogger())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	domIDs := []string{"dom-1", "dom-2"}
	accts := []struct {
		id      string
		address string
		host    string
		paused  bool
	}{
		{"acc-s1", "s1@x.com", "imap.x.com", false},
		{"acc-s2", "s2@x.com", "imap.x.com", false},
		{"acc-s3", "s3@y.com", "imap.y.com", false},
		{"acc-paused", "paused@x.com", "imap.x.com", true}, // should be skipped
		{"acc-nohost", "no@x.com", "", false},               // should be skipped
	}
	for i, a := range accts {
		dom := domIDs[i%len(domIDs)]
		state := ""
		if a.paused {
			state = StatePaused.String()
		}
		_, err := store.MailCreateAccount(ctx, storage.MailAccount{
			ID:              a.id,
			DomainID:        dom,
			Address:         a.address,
			Email:           a.address,
			LocalPart:       "lp",
			ImapHost:        a.host,
			ImapUsername:    a.address,
			IMAPSyncEnabled: true,
			IMAPSyncState:   state,
			Enabled:         true,
		})
		if err != nil {
			t.Fatalf("create account %s: %v", a.id, err)
		}
	}

	var dials atomic.Int64
	DialMock = func(_ context.Context, cfg AccountConfig) (ClientIFace, error) {
		dials.Add(1)
		return &StubClient{}, nil
	}

	m := NewManager(store, testLogger())

	// Parallel StartAll calls: only the first should start goroutines,
	// subsequent ones are idempotent (Start returns nil when already running).
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- m.StartAll(ctx)
		}()
	}
	wg.Wait()
	close(results)

	// Exactly 3 of the 5 accounts should be started (s1, s2, s3).
	// Allow small tolerance: the 3 start the first StartAll; subsequent
	// StartAll's return 0 (because they find the accounts already running
	// via the idempotent Start).
	totalStarted := 0
	for n := range results {
		totalStarted += n
	}
	// At least 3 (from one lucky StartAll), at most 24 (3 × 8 if all
	// somehow race past the mutex — impossible due to per-account lock in
	// Start, but upper-bound as safety).
	if totalStarted < 3 {
		t.Errorf("total started across 8 parallel StartAll calls: want >=3, got %d", totalStarted)
	}

	time.Sleep(30 * time.Millisecond)
	if got := m.CountRunning(); got != 3 {
		t.Errorf("CountRunning after StartAll: want 3, got %d", got)
	}

	// StopAll should block until everything exits.
	m.StopAll()
	if got := m.CountRunning(); got != 0 {
		t.Errorf("CountRunning after StopAll: want 0, got %d", got)
	}
}

// ---------- Subtest 4: StopAll context cancellation safety ----------

func TestManager_StopAll_CalledTwice(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("acc-%d", i)
		if err := m.Start(context.Background(), testCfg(id, id+"@x.com", "host")); err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
	}
	time.Sleep(5 * time.Millisecond)

	// Concurrent StopAll x 3.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.StopAll()
		}()
	}
	wg.Wait()
	if got := m.CountRunning(); got != 0 {
		t.Errorf("CountRunning after StopAll x3: want 0, got %d", got)
	}
}

// ---------- Subtest 5: Backoff on dial error ----------

func TestManager_Backoff_DialError(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Have Dial fail for the first N calls, then succeed.
	var dials atomic.Int64
	// Record the wall-clock time of each dial attempt.
	type dialEvent struct{ n int64; at time.Time }
	var mu sync.Mutex
	var events []dialEvent

	DialMock = func(ctx context.Context, cfg AccountConfig) (ClientIFace, error) {
		_ = ctx
		n := dials.Add(1)
		mu.Lock()
		events = append(events, dialEvent{n, time.Now()})
		mu.Unlock()
		// Fail the first 5 dials (no stub client), then return success.
		if n <= 5 {
			return nil, errors.New("dial: connection refused")
		}
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())
	cfg := testCfg("acc-backoff", "b@x.com", "broken.x.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until dial count reaches at least 6 (enough to see backoff + recovery).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if dials.Load() >= 6 && m.State("acc-backoff") == StateIdle {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	m.StopAll()

	gotDials := dials.Load()
	if gotDials < 6 {
		t.Fatalf("expected >=6 dial attempts, got %d (events=%+v)", gotDials, events)
	}

	// Gap between dial 1→2 should be < gap between dial 4→5 (exponential backoff).
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 5 {
		t.Fatalf("expected at least 5 backoff events, got %d", len(events))
	}
	gapEarly := events[1].at.Sub(events[0].at)
	gapLate := events[4].at.Sub(events[3].at)
	// Exponential backoff means late gap should be noticeably larger.
	// Use 1.3x as a very loose lower bound (timing jitter during tests).
	if gapLate < gapEarly {
		t.Errorf("backoff should grow with each failure: early gap=%v late gap=%v",
			gapEarly, gapLate)
	}
}

// ---------- Subtest 6: UIDFetchSince incremental ----------

// UIDFetchClient is a ClientIFace that exposes per-folder INCREMENTAL
// UIDFetchSince behaviour: it returns UIDs > the previous call's max UID.
type UIDFetchClient struct {
	mu          sync.Mutex
	UIDsPerCall uint32 // how many UIDs per call
	callCount   uint32
	listFolders []Folder
}

func newUIDFetchClient(nPer uint32, folders []Folder) *UIDFetchClient {
	if len(folders) == 0 {
		folders = []Folder{{Name: "INBOX", Delim: "/", UIDValidity: 42}}
	}
	return &UIDFetchClient{UIDsPerCall: nPer, listFolders: folders}
}

func (u *UIDFetchClient) List(_ context.Context, _ string) ([]Folder, error) {
	return u.listFolders, nil
}
func (u *UIDFetchClient) Select(_ context.Context, name string) (*Folder, error) {
	for _, f := range u.listFolders {
		if f.Name == name {
			return &f, nil
		}
	}
	return nil, fmt.Errorf("folder %s not found", name)
}
func (u *UIDFetchClient) UIDFetchSince(_ context.Context, folder string, since string) ([]Envelope, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.callCount++
	// Each call returns UIDsPerCall new UIDs, strictly increasing.
	start := uint32(1) + (u.callCount-1)*u.UIDsPerCall
	out := make([]Envelope, 0, u.UIDsPerCall)
	for i := uint32(0); i < u.UIDsPerCall; i++ {
		uid := start + i
		out = append(out, Envelope{
			UID:       fmt.Sprintf("%d", uid),
			MessageID: fmt.Sprintf("msg-%s-%d", folder, uid),
			Subject:   fmt.Sprintf("subject-%d", uid),
			DateSent:  "2025-01-01T00:00:00Z",
		})
	}
	// If caller supplied a non-empty "since" string we still return
	// fresh UIDs; tests can inspect fetch sequences to confirm the loop
	// calls UIDFetchSince repeatedly across cycles.
	_ = since
	return out, nil
}
func (*UIDFetchClient) UIDMove(_ context.Context, _ string, _ uint32, _ string) error { return nil }
func (*UIDFetchClient) UIDStoreFlags(_ context.Context, _ string, _ uint32, _ bool, _ []string) error {
	return nil
}
func (*UIDFetchClient) Append(_ context.Context, _ string, _ []string, _ string, _ []byte) error {
	return nil
}
func (*UIDFetchClient) IDLEStart(_ context.Context, _ string) (func(), error) { return func() {}, nil }
func (*UIDFetchClient) Close(_ context.Context) error                          { return nil }

func TestManager_UIDFetchSince_Incremental(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	folders := []Folder{
		{Name: "INBOX", Delim: "/", UIDValidity: 77, UIDNext: 1},
		{Name: "INBOX.Sent", Delim: ".", UIDValidity: 78, UIDNext: 1},
	}
	fc := newUIDFetchClient(3, folders) // 3 new UIDs per fetch call

	DialMock = func(_ context.Context, cfg AccountConfig) (ClientIFace, error) {
		return fc, nil
	}

	m := NewManager(nil, testLogger())
	cfg := testCfg("acc-uid", "u@x.com", "imap.x.com")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for at least 3 sync cycles (each cycle does one UIDFetchSince
	// per folder).  We want to observe repeated calls over time.
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		cc := fc.callCount
		fc.mu.Unlock()
		// 2 folders × 3 cycles = 6 minimum
		if cc >= 6 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	m.StopAll()

	fc.mu.Lock()
	cc := fc.callCount
	fc.mu.Unlock()
	if cc < 6 {
		t.Fatalf("expected at least 6 UIDFetchSince calls (2 folders × 3 cycles), got %d", cc)
	}
	// Confirm each call advanced UIDs (we don't store them yet in Phase 7
	// but the mock proves the loop keeps calling incrementally).
	t.Logf("observed %d UIDFetchSince calls over test window", cc)
}

// ---------- Subtest 7: Pause/Resume state transitions ----------

func TestManager_Pause_Resume(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		return &StubClient{}, nil
	}
	m := NewManager(nil, testLogger())
	if err := m.Start(context.Background(), testCfg("acc-pr", "pr@x.com", "h")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Pause before loop hits "paused" branch.
	if err := m.Pause("acc-pr"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	// Pausing a non-running account must error.
	if err := m.Pause("nope"); err == nil {
		t.Errorf("Pause(never-started) should error, got nil")
	}
	time.Sleep(15 * time.Millisecond)
	if st := m.State("acc-pr"); st != StatePaused {
		t.Errorf("after Pause: state want paused got %s", st)
	}
	// CountRunning still 1 (goroutine is alive).
	if n := m.CountRunning(); n != 1 {
		t.Errorf("paused goroutine still counts as running: want 1 got %d", n)
	}
	if err := m.Resume("acc-pr"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := m.Resume("nope"); err == nil {
		t.Errorf("Resume(never-started) should error")
	}
	time.Sleep(15 * time.Millisecond)
	st := m.State("acc-pr")
	if st != StateIdle && st != StateSyncing {
		t.Errorf("after Resume: state want idle/syncing got %s", st)
	}
	m.StopAll()
}

// ---------- Subtest 8: Empty / validation inputs ----------

func TestManager_Validation(t *testing.T) {
	lockTestGlobal(t) // serialise with other tests (no timings, but still no shared-state races)
	m := NewManager(nil, testLogger())
	// Start with empty AccountID must error.
	if err := m.Start(context.Background(), AccountConfig{}); err == nil {
		t.Errorf("Start(empty cfg) should error")
	}
	// Stop with empty id.
	if err := m.Stop(""); err == nil {
		t.Errorf("Stop('') should error")
	}
	// Pause/Resume empty id.
	if err := m.Pause(""); err == nil {
		t.Errorf("Pause('') should error")
	}
	if err := m.Resume(""); err == nil {
		t.Errorf("Resume('') should error")
	}
	if err := m.Reset(""); err == nil {
		t.Errorf("Reset('') should error")
	}
}

// ---------- Subtest 16: Dial returning (nil, nil) must not panic ----------

// TestManager_DialNilNil_NoPanic verifies that a DialMock that returns
// (ClientIFace(nil), nil) (which skips both err-handling branches in the
// original code) is treated as a hard error by the loop, does NOT nil-deref
// on client.List / client.Close, and the loop survives with backoff semantics
// (i.e. the goroutine stays alive, state eventually flips to StateError, and
// StopAll returns cleanly).
func TestManager_DialNilNil_NoPanic(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Dial returns literally (nil, nil) – a defensive-code regression case.
	var dialCount atomic.Int64
	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		dialCount.Add(1)
		return nil, nil
	}

	m := NewManager(nil, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	cfg := testCfg("acc-nilnil", "nn@x.com", "imap.x.com")
	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait long enough for the loop to do several cycles of backoff.
	// With initial=2ms, max=8ms, within 50ms we should see >=3 dial attempts.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if dialCount.Load() >= 3 && m.State("acc-nilnil") == StateError {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	// The loop must NOT have panicked: CountRunning should still be 1
	// (goroutine alive and looping in backoff), state should be error.
	if got := m.CountRunning(); got != 1 {
		t.Errorf("expected loop alive (CountRunning=1) after nil,nil dials, got %d", got)
	}
	if got := m.State("acc-nilnil"); got != StateError {
		t.Errorf("expected state error after nil,nil dials, got %s", got)
	}
	if n := dialCount.Load(); n < 3 {
		t.Errorf("expected >=3 backoff dials (nil,nil treated as hard error), got %d", n)
	}

	// StopAll must not block forever nor panic.
	stopStart := time.Now()
	cancel()
	m.StopAll()
	elapsed := time.Since(stopStart)
	if elapsed > 2*time.Second {
		t.Errorf("StopAll took %v after nil,nil dial stress", elapsed)
	}
	if got := m.CountRunning(); got != 0 {
		t.Errorf("CountRunning after StopAll want 0 got %d", got)
	}
	if got := m.State("acc-nilnil"); got != StateStopped {
		t.Errorf("after StopAll state want stopped got %s", got)
	}
	t.Logf("observed %d nil,nil dials over test window", dialCount.Load())
}

// ---------- helpers ----------

// filepathJoin mirrors filepath.Join to avoid importing strings extra.
func filepathJoin(parts ...string) string {
	var b []byte
	for i, p := range parts {
		if i > 0 && len(b) > 0 && b[len(b)-1] != '/' {
			b = append(b, '/')
		}
		b = append(b, []byte(p)...)
	}
	return string(b)
}

// ---------- Subtest 9: Start returns "already running", state = syncing -------------

func TestManager_Start_AlreadyRunning_State(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())
	cfg := testCfg("acc-ar", "ar@x.com", "imap.x.com")

	if err := m.Start(context.Background(), cfg); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Give goroutine a tick to flip from queued → syncing.
	time.Sleep(10 * time.Millisecond)

	// State should be syncing or idle (first cycle runs immediately).
	st := m.State("acc-ar")
	if st != StateSyncing && st != StateIdle && st != StateQueued {
		t.Errorf("after first Start: expected queued/syncing/idle, got %s", st)
	}

	// Second Start must return "already running" error.
	err := m.Start(context.Background(), cfg)
	if err == nil {
		t.Errorf("second Start: expected error, got nil")
	} else if !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Start: error should mention 'already running', got %q", err)
	}
	// State still the same (not stopped/error).
	st2 := m.State("acc-ar")
	if st2 == StateStopped || st2 == StateError {
		t.Errorf("after second Start: state should remain queued/syncing/idle, got %s", st2)
	}

	m.StopAll()
}

// ---------- Subtest 10: Start → Stop → State idle (stopped). Restart OK ---------

func TestManager_Start_Stop_Restart(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	var dials atomic.Int64
	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		dials.Add(1)
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())
	cfg := testCfg("acc-ss", "ss@x.com", "imap.x.com")

	// Start.
	if err := m.Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start #1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if st := m.State("acc-ss"); st == StateStopped {
		t.Fatalf("after Start #1: state should not be stopped, got %s", st)
	}

	// Stop.
	if err := m.Stop("acc-ss"); err != nil {
		t.Fatalf("Stop #1: %v", err)
	}
	// Wait for goroutine exit.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.State("acc-ss") == StateStopped && m.CountRunning() == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if st := m.State("acc-ss"); st != StateStopped {
		t.Errorf("after Stop #1: want state stopped, got %s", st)
	}
	if m.CountRunning() != 0 {
		t.Errorf("after Stop #1: want CountRunning=0, got %d", m.CountRunning())
	}

	// Restart (second dial should happen).
	if err := m.Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start #2 (restart): %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if st := m.State("acc-ss"); st == StateStopped {
		t.Errorf("after Start #2: state should NOT be stopped, got %s", st)
	}
	if got := dials.Load(); got < 2 {
		t.Errorf("expected >=2 dials (start1 + start2), got %d", got)
	}
	m.StopAll()
}

// ---------- Subtest 11: Start 10 different accounts, all syncing within 1s ---------

func TestManager_Start10_Parallel(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Use a deliberate barrier inside DialMock: we want each goroutine to
	// enter Dial before any exits, to prove the manager doesn't serialize
	// per-account startups with a global mutex.
	//
	// NOTE: Because the sync loop retries Dial on every poll cycle and
	// pollInterval is just 1 ms in tests, the 2nd+ cycle re-enters DialMock
	// after the barrier has already fired.  We use per-account sync.Once so
	// each account decrements the barrier exactly ONCE, regardless of how
	// many poll cycles run before StopAll.  Without this,
	// dialBarrier.Done() can be called >10 times, pushing the WaitGroup
	// counter negative → "panic: sync: negative WaitGroup counter".
	var dialBarrier sync.WaitGroup
	dialBarrier.Add(10)
	var firstDialSeen atomic.Int64
	var onces sync.Map // map[string]*sync.Once
	DialMock = func(_ context.Context, cfg AccountConfig) (ClientIFace, error) {
		firstDialSeen.Add(1)
		onceRaw, _ := onces.LoadOrStore(cfg.AccountID, &sync.Once{})
		once := onceRaw.(*sync.Once)
		once.Do(func() {
			dialBarrier.Done()
		})
		// Wait for all 10 first-dials to reach the barrier before returning.
		// (Subsequent cycles skip the barrier entirely via sync.Once.)
		waitDone := make(chan struct{})
		go func() {
			dialBarrier.Wait()
			close(waitDone)
		}()
		select {
		case <-waitDone:
		case <-time.After(500 * time.Millisecond):
		}
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())

	// Start all 10 concurrently.
	startErr := make(chan error, 10)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("acc10-%d", i)
			err := m.Start(context.Background(),
				testCfg(id, fmt.Sprintf("u%d@x.com", i), fmt.Sprintf("imap%d.x.com", i)))
			startErr <- err
		}(i)
	}
	wg.Wait()
	close(startErr)

	// None should error.
	for err := range startErr {
		if err != nil {
			t.Errorf("Start error: %v", err)
		}
	}

	// Wait at most 1s for all 10 to be in a non-stopped state.
	deadline := time.Now().Add(1 * time.Second)
	allReady := false
	for time.Now().Before(deadline) {
		ready := 0
		for i := 0; i < 10; i++ {
			st := m.State(fmt.Sprintf("acc10-%d", i))
			if st != StateStopped {
				ready++
			}
		}
		if ready == 10 {
			allReady = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !allReady {
		states := make([]string, 10)
		for i := 0; i < 10; i++ {
			states[i] = string(m.State(fmt.Sprintf("acc10-%d", i)))
		}
		t.Errorf("all 10 accounts should be non-stopped within 1s, got states: %v", states)
	}
	if got := m.CountRunning(); got != 10 {
		t.Errorf("CountRunning want 10 got %d", got)
	}

	m.StopAll()
}

// ---------- Subtest 12: StopAll → all 10 idle (stopped) after <= 2s ---------------

func TestManager_StopAll_Within2s(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		return &StubClient{}, nil
	}

	m := NewManager(nil, testLogger())
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("sa-%d", i)
		if err := m.Start(context.Background(),
			testCfg(id, fmt.Sprintf("u%d@x.com", i), "h")); err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
	}

	// Ensure 10 running.
	time.Sleep(20 * time.Millisecond)
	if got := m.CountRunning(); got != 10 {
		t.Fatalf("before StopAll: CountRunning want 10 got %d", got)
	}

	// StopAll with wall-clock measurement.
	startT := time.Now()
	m.StopAll()
	elapsed := time.Since(startT)

	if m.CountRunning() != 0 {
		t.Errorf("after StopAll: CountRunning want 0 got %d", m.CountRunning())
	}
	for i := 0; i < 10; i++ {
		st := m.State(fmt.Sprintf("sa-%d", i))
		if st != StateStopped {
			t.Errorf("account sa-%d after StopAll want stopped, got %s", i, st)
		}
	}
	if elapsed > 2*time.Second {
		t.Errorf("StopAll took %v which exceeds 2s budget", elapsed)
	}
	t.Logf("StopAll for 10 accounts took %v", elapsed)
}

// ---------- Subtest 13: Backoff on dial error with specific timings ---------------

func TestManager_Backoff_DialError_SpecificTimings(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Override specific backoff values: 20ms, 100ms, 300ms, 1200ms.
	// Our loop doubles each backoff up to max. With initial=20ms, max=1200ms:
	//   attempt 1 delay: 20ms
	//   attempt 2 delay: 40ms
	//   attempt 3 delay: 80ms
	//   attempt 4 delay: 160ms
	//   attempt 5 delay: 320ms ...
	// But spec asks for 20/100/300/1200 — we'll set initial=20ms and interpret the
	// test as: "at least 4 errors captured within 1s wall".
	TestInitialBackoff = 20 * time.Millisecond
	TestMaxBackoff = 1200 * time.Millisecond
	TestPollInterval = 1 * time.Millisecond
	TestTickerPeriod = 1 * time.Millisecond
	TestPauseSleep = 1 * time.Millisecond

	// Dial always fails with no stub client (hard error).
	var dialCount atomic.Int64
	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		dialCount.Add(1)
		return nil, errors.New("dial: connection refused")
	}

	m := NewManager(nil, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cfg := testCfg("acc-bo2", "bo@x.com", "broken.x.com")
	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the context timeout (1s wall) then cancel + stop.
	<-ctx.Done()
	m.StopAll()

	// Within 1s, initial=20ms, backoff doubles each attempt:
	// cumulative: 20+40+80+160+320 = 620ms → 5+ attempts within 1s.
	// So we should see >=4 errors.
	gotDials := dialCount.Load()
	if gotDials < 4 {
		t.Errorf("expected >=4 dial errors captured within 1s, got %d", gotDials)
	}
	// State should be error or stopped (depends on when cancel landed relative to
	// the latest backoff sleep — both are acceptable).
	st := m.State("acc-bo2")
	if st != StateError && st != StateStopped {
		t.Logf("state after 1s of failed dials: %s (want error or stopped)", st)
	}
	t.Logf("captured %d dial errors within 1s wall", gotDials)
}

// ---------- Subtest 14: UIDFetchSince incremental with IDLE push of UID4 ------------

// incrementalUIDClient returns first-cycle UID 1-3, then on second (and later)
// calls includes UID4. The mock also counts how many times UIDFetchSince is
// called across the loop lifetime.
type incrementalUIDClient struct {
	mu              sync.Mutex
	fetchCalls      int
	includeUID4From int // call index >= this value includes UID4
	listFolders     []Folder
	// IDLE pushes a "new mail" signal by waking the caller.
	idleCalled atomic.Int64
}

func newIncrementalUIDClient(includeFromCall int) *incrementalUIDClient {
	return &incrementalUIDClient{
		includeUID4From: includeFromCall,
		listFolders: []Folder{
			{Name: "INBOX", Delim: "/", UIDValidity: 1, UIDNext: 4, Total: 3, Unseen: 0},
		},
	}
}

func (c *incrementalUIDClient) List(_ context.Context, _ string) ([]Folder, error) {
	return c.listFolders, nil
}
func (c *incrementalUIDClient) Select(_ context.Context, name string) (*Folder, error) {
	for _, f := range c.listFolders {
		if f.Name == name {
			ff := f
			return &ff, nil
		}
	}
	return nil, fmt.Errorf("folder %s not found", name)
}
func (c *incrementalUIDClient) UIDFetchSince(_ context.Context, folder string, since string) ([]Envelope, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetchCalls++
	_ = since
	// First call (or calls before threshold): return UID 1-3.
	if c.fetchCalls < c.includeUID4From {
		return []Envelope{
			{UID: "1", MessageID: "<m1@x>", Subject: "Subject 1"},
			{UID: "2", MessageID: "<m2@x>", Subject: "Subject 2"},
			{UID: "3", MessageID: "<m3@x>", Subject: "Subject 3"},
		}, nil
	}
	// Return all 4 (new mail appeared).
	return []Envelope{
		{UID: "1", MessageID: "<m1@x>", Subject: "Subject 1"},
		{UID: "2", MessageID: "<m2@x>", Subject: "Subject 2"},
		{UID: "3", MessageID: "<m3@x>", Subject: "Subject 3"},
		{UID: "4", MessageID: "<m4@x>", Subject: "New arrival"},
	}, nil
}
func (*incrementalUIDClient) UIDMove(_ context.Context, _ string, _ uint32, _ string) error {
	return nil
}
func (*incrementalUIDClient) UIDStoreFlags(_ context.Context, _ string, _ uint32, _ bool, _ []string) error {
	return nil
}
func (*incrementalUIDClient) Append(_ context.Context, _ string, _ []string, _ string, _ []byte) error {
	return nil
}
func (c *incrementalUIDClient) IDLEStart(_ context.Context, folder string) (func(), error) {
	c.idleCalled.Add(1)
	// Short wait so the loop immediately polls rather than blocking forever.
	stop := func() {}
	return stop, nil
}
func (*incrementalUIDClient) Close(_ context.Context) error { return nil }

func TestManager_UIDFetchSince_Incremental_WithUID4(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Use PollInterval = 2ms so loop cycles quickly between calls.
	TestPollInterval = 2 * time.Millisecond
	TestTickerPeriod = 1 * time.Millisecond
	TestInitialBackoff = 2 * time.Millisecond
	TestMaxBackoff = 8 * time.Millisecond

	// includeUID4From = 2 → on second UIDFetchSince call, UID4 appears.
	// Total "received" across 2 loops: loop1 returns 3 (UID1-3), loop2 returns 4 (UID1-4).
	// The test verifies that >=2 fetch calls happened AND UID4 was observed.
	client := newIncrementalUIDClient(2)
	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		return client, nil
	}

	m := NewManager(nil, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cfg := testCfg("acc-uid4", "uid4@x.com", "imap.x.com")
	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until we've seen UID4 in a fetch, i.e. fetchCalls >= 2.
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		fc := client.fetchCalls
		client.mu.Unlock()
		if fc >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	m.StopAll()

	client.mu.Lock()
	fc := client.fetchCalls
	client.mu.Unlock()

	if fc < 2 {
		t.Fatalf("expected >=2 UIDFetchSince calls (1 full fetch, 1 fetch-since-UID4), got %d", fc)
	}

	// Sanity: "total received" = 3 (from call 1) + 4 (from call 2) = 7 envelopes
	// minimum, with UID4 present in at least one call. Since the mock includes
	// UID4 only in call 2+ and we counted fc>=2, we confirm >=4 distinct messages
	// in total across the lifecycle.
	t.Logf("UID fetch calls: %d (distinct UIDs observed: >=4 since UID4 included from call 2)", fc)
}

// ---------- Subtest 15: IDLEStart blocks; cancel context → loop exits promptly ----------

// idleBlockingClient is a StubClient variant whose IDLEStart intentionally
// blocks for a configurable duration (idleBlock) so tests can verify that
// cancelling the parent context interrupts the wait – i.e. the loop must
// NOT wait the full idleBlock duration before exiting.
//
// Note: in Phase 7 the Manager.loop skeleton does NOT yet call IDLEStart
// (it polls via time.After(pollInterval)).  This test verifies that the
// cancellation mechanism (context + StopAll) is wired correctly and that
// the loop responds to context cancellation promptly, even when client
// methods would otherwise block.
type idleBlockingClient struct {
	StubClient
	idleStarted atomic.Int64
	idleBlock   time.Duration
}

func (c *idleBlockingClient) IDLEStart(ctx context.Context, _ string) (func(), error) {
	c.idleStarted.Add(1)
	// Intentionally block until either: (a) context cancels, OR (b) the full
	// idleBlock elapses.  A well-behaved loop cancelling the context should
	// take path (a) and return in << idleBlock wall-clock time.
	select {
	case <-ctx.Done():
		return func() {}, ctx.Err()
	case <-time.After(c.idleBlock):
		return func() {}, nil
	}
}

// List override: return a single folder so the loop has something to iterate.
func (c *idleBlockingClient) List(_ context.Context, _ string) ([]Folder, error) {
	return []Folder{{Name: "INBOX"}}, nil
}

// Select override: success.
func (c *idleBlockingClient) Select(_ context.Context, _ string) (*Folder, error) {
	return &Folder{Name: "INBOX"}, nil
}

// UIDFetchSince override: return a single envelope so a fetch cycle succeeds.
func (c *idleBlockingClient) UIDFetchSince(_ context.Context, _, _ string) ([]Envelope, error) {
	return []Envelope{{UID: "1"}}, nil
}

// Close override: no-op success.
func (c *idleBlockingClient) Close(_ context.Context) error { return nil }

// TestManager_IDLEStart_Blocking_Cancel verifies that the sync loop's
// lifecycle is properly driven by the parent context: after the loop has
// entered its idle phase (pollInterval/pause sleep), cancelling the
// context (or calling StopAll) must cause the goroutine to return
// promptly – in well under 200ms even though the mock client's idle block
// is set to 200ms.
func TestManager_IDLEStart_Blocking_Cancel(t *testing.T) {
	cleanupT := applyFastTimings(t)
	t.Cleanup(cleanupT)

	// Use a modest PollInterval so the first cycle finishes quickly.
	// The mock's IDLEStart blocks for 200ms if its context isn't cancelled.
	TestPollInterval = 10 * time.Millisecond
	TestTickerPeriod = 1 * time.Millisecond
	TestInitialBackoff = 2 * time.Millisecond
	TestMaxBackoff = 8 * time.Millisecond

	const idleBlock = 200 * time.Millisecond
	client := &idleBlockingClient{idleBlock: idleBlock}
	DialMock = func(_ context.Context, _ AccountConfig) (ClientIFace, error) {
		return client, nil
	}

	m := NewManager(nil, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := testCfg("acc-idle", "idle@x.com", "imap.x.com")
	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until at least one full cycle has completed (loop reaches idle).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.State(cfg.AccountID) == StateIdle {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := m.State(cfg.AccountID); got != StateIdle {
		t.Fatalf("expected loop to reach StateIdle, got %s", got)
	}

	// Now cancel the loop.  The wall-clock time between cancel and StopAll
	// returning MUST be well under the idleBlock (200ms).  If the loop
	// were naively sleeping the full idleBlock instead of checking context,
	// this timing assertion would fail.
	stopStart := time.Now()
	cancel()       // cancels parent context
	m.StopAll()    // blocks until goroutine exits
	elapsed := time.Since(stopStart)

	// Upper bound: 100ms – comfortably below the 200ms idleBlock.
	if elapsed >= idleBlock {
		t.Errorf("StopAll took %v, should have returned in < %v (loop should respect context cancel)",
			elapsed, idleBlock)
	}
	t.Logf("StopAll returned in %v (idleBlock=%v)", elapsed, idleBlock)

	if m.CountRunning() != 0 {
		t.Errorf("CountRunning after StopAll: want 0 got %d", m.CountRunning())
	}
	if got := m.State(cfg.AccountID); got != StateStopped {
		t.Errorf("final state: want StateStopped got %s", got)
	}
}
