package imapsync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"phantom-lancer/internal/storage"
)

// Manager owns the per-account IMAP sync goroutine map.  It is safe for
// concurrent use (all mutations are guarded by mu).  The Service layer
// constructs a single Manager via NewManager, wires it into
// Service.imapSyncManager during NewService, and calls StartAll / StopAll
// during StartBackground / Close.
//
// Loop behaviour per account:
//
//  1. Dial the upstream via Dial() → ClientIFace (stub in Phase 7).
//  2. For each subscribed folder: List → Select → UIDFetchSince.
//  3. Persist MailFolder + MailMessage via storage.Store (SKEL in Phase 7
//     – logs + no-ops).
//  4. Update account row: imap_sync_state, imap_last_* timestamps.
//  5. Idle (IDLEStart if supported, otherwise sleep).
//  6. On any error: flip state to error, exponential backoff (2s → 30s),
//     retry on next tick.
type Manager struct {
	mu sync.Mutex

	store *storage.Store
	log   *slog.Logger

	// cancel holds the per-account cancel func for the goroutine's child
	// context.  Present iff the account currently has a running loop.
	cancel map[string]context.CancelFunc

	// state holds the last-known State for each account that has ever been
	// started (even after Stop – StateStopped).  This lets State() return
	// stable answers for accounts that are not currently running.
	state map[string]State

	// wg lets StopAll block until every goroutine has exited.
	wg sync.WaitGroup

	// running holds the goroutine count for StartAll/StopAll bookkeeping.
	running atomic.Int64
}

// NewManager builds a Manager wired to the given storage + logger.
func NewManager(store *storage.Store, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		store:  store,
		log:    log.With("module", "mail.imapsync"),
		cancel: map[string]context.CancelFunc{},
		state:  map[string]State{},
	}
}

// ---- Public lifecycle API ------------------------------------------------

// Start launches a sync loop for cfg.AccountID if one is not already
// running.  The loop runs until Stop(cfg.AccountID) is called or the
// supplied parent context is cancelled.
//
// If the account is already running, Start returns an error with message
// "already running" and no second goroutine is spawned.  Callers that want
// a no-op-for-duplicates API should check State == syncing first.
func (m *Manager) Start(ctx context.Context, cfg AccountConfig) error {
	if cfg.AccountID == "" {
		return fmt.Errorf("imapsync: Start: AccountID is required")
	}
	m.mu.Lock()
	if _, running := m.cancel[cfg.AccountID]; running {
		m.mu.Unlock()
		return fmt.Errorf("imapsync: Start: account %s already running", cfg.AccountID)
	}
	child, cancel := context.WithCancel(ctx)
	m.cancel[cfg.AccountID] = cancel
	m.state[cfg.AccountID] = StateQueued
	m.wg.Add(1)
	m.running.Add(1)
	logger := m.log.With("account_id", cfg.AccountID, "address", cfg.Address, "host", cfg.ImapHost)
	m.mu.Unlock()

	go func() {
		defer func() {
			// All bookkeeping (running counter, cancel map, state map) MUST
			// complete before wg.Done() so StopAll observes consistent state
			// the moment wg.Wait() returns.  Previously wg.Done() ran first,
			// which let StopAll's tests race with CountRunning and State()
			// returning stale values (see 500x StopAll_Within2s stress).
			m.running.Add(-1)
			m.mu.Lock()
			delete(m.cancel, cfg.AccountID)
			m.state[cfg.AccountID] = StateStopped
			m.mu.Unlock()
			cancel()
			m.wg.Done()
		}()
		m.loop(child, cfg, logger)
	}()
	return nil
}

// Stop terminates the sync loop for the given account.  Idempotent.
func (m *Manager) Stop(accountID string) error {
	if accountID == "" {
		return fmt.Errorf("imapsync: Stop: account id is required")
	}
	m.mu.Lock()
	cancel, ok := m.cancel[accountID]
	if ok {
		m.state[accountID] = StateStopped
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	cancel()
	return nil
}

// StopAll terminates every running sync loop.  Used by Close().  Blocks
// until all goroutines have exited.
func (m *Manager) StopAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.cancel))
	for id, c := range m.cancel {
		cancels = append(cancels, c)
		m.state[id] = StateStopped
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	m.wg.Wait()
}

// Pause flips the account into StatePaused.  The loop skips fetch/index
// but keeps the goroutine alive so Resume() works without a restart.
func (m *Manager) Pause(accountID string) error {
	if accountID == "" {
		return fmt.Errorf("imapsync: Pause: account id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cancel[accountID]
	if !ok {
		return fmt.Errorf("imapsync: Pause: account %s is not running", accountID)
	}
	m.state[accountID] = StatePaused
	return nil
}

// Resume flips a paused account back into StateSyncing.
func (m *Manager) Resume(accountID string) error {
	if accountID == "" {
		return fmt.Errorf("imapsync: Resume: account id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cancel[accountID]
	if !ok {
		return fmt.Errorf("imapsync: Resume: account %s is not running", accountID)
	}
	m.state[accountID] = StateSyncing
	return nil
}

// Reset clears any persisted checkpoint for the account so the next sync
// cycle does a full re-fetch from scratch.  Phase 7 stub.
func (m *Manager) Reset(accountID string) error {
	if accountID == "" {
		return fmt.Errorf("imapsync: Reset: account id is required")
	}
	m.log.Debug("imapsync: Reset (stub)", "account_id", accountID)
	return nil
}

// State returns the current state for the given account, or StateStopped
// if it has never been started.
func (m *Manager) State(accountID string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.state[accountID]
	if !ok {
		return StateStopped
	}
	return st
}

// CountRunning returns the number of currently live sync goroutines.
func (m *Manager) CountRunning() int { return int(m.running.Load()) }

// StartAll scans storage for accounts that have IMAPSyncEnabled=true and
// whose last-known state is not StatePaused, and calls Start on each.
// Errors on individual accounts are logged but never bubble up (a single
// bad row must never prevent the rest from starting).  Returns the number
// of accounts successfully started.
//
// In Phase 7 the storage layer does not yet expose a typed
// "IMAPSyncEnabled" helper, so StartAll enumerates accounts via
// MailListAccounts and skips rows that are missing IMAP configuration.
func (m *Manager) StartAll(ctx context.Context) int {
	accounts, err := m.store.MailListAccounts(ctx, "", "")
	if err != nil {
		m.log.WarnContext(ctx, "imapsync: StartAll list accounts failed", "error", err)
		return 0
	}
	started := 0
	for _, a := range accounts {
		// Phase 7 guard: only start if the account has an upstream host.
		if a.ImapHost == "" {
			continue
		}
		// If the account is explicitly marked paused, respect it.
		if a.IMAPSyncState == StatePaused.String() {
			continue
		}
		cfg := AccountConfig{
			AccountID: a.ID,
			Address:   a.Address,
			ImapHost:  a.ImapHost,
			Username:  a.ImapUsername,
			PasswordFn: func(_ context.Context) (string, error) {
				// Phase 7 stub: no credential store yet.  Dial uses StubClient
				// which doesn't need a password; the fn is never actually
				// invoked because Dial returns ErrNoAdapter.  Returning an
				// error here keeps C-6 happy: no plaintext password ever
				// lives in memory.
				return "", fmt.Errorf("imapsync: PasswordFn not wired in Phase 7")
			},
			MaxMsgSize:     a.IMAPSyncMaxSizeBytes,
			MaxTotalBytes:  0, // filled by follow-up work when per-account quotas exist
			IdleTimeoutSec: 120,
		}
		if err := m.Start(ctx, cfg); err != nil {
			m.log.WarnContext(ctx, "imapsync: StartAll per-account failed",
				"account_id", a.ID, "address", a.Address, "error", err)
			continue
		}
		started++
	}
	if started > 0 {
		m.log.InfoContext(ctx, "imapsync: StartAll", "started", started)
	}
	return started
}

// ---- Internal loop skeleton ---------------------------------------------

// Package-level timing variables used by the per-account loop.  Tests can
// override these to shrink loop durations from seconds to milliseconds so
// the backoff / poll / UID-fetch tests run in <5s instead of minutes.
//
// Production code leaves these at their default (zero), which causes the
// loop to fall back to the documented constants inside loop().
var (
	// TestInitialBackoff overrides the starting backoff (default 2s).
	TestInitialBackoff time.Duration = 0
	// TestMaxBackoff overrides the exponential backoff ceiling (default 30s).
	TestMaxBackoff time.Duration = 0
	// TestPollInterval overrides the idle-poll gap (default 60s).
	TestPollInterval time.Duration = 0
	// TestTickerPeriod overrides the first-cycle ticker (default 500ms).
	TestTickerPeriod time.Duration = 0
	// TestPauseSleep overrides the StatePaused idle sleep (default 5s).
	TestPauseSleep time.Duration = 0
)

// loop is the per-account goroutine body.  It is a skeleton that honours
// context cancellation, the paused state, and exponential backoff on
// errors.  The concrete fetch/index step is a stub in Phase 7 (logs +
// returns nil) so the goroutine machinery compiles and runs without a
// real IMAP client.
func (m *Manager) loop(ctx context.Context, cfg AccountConfig, log *slog.Logger) {
	backoff := 2 * time.Second
	if TestInitialBackoff > 0 {
		backoff = TestInitialBackoff
	}
	maxBackoff := 30 * time.Second
	if TestMaxBackoff > 0 {
		maxBackoff = TestMaxBackoff
	}
	pollInterval := 60 * time.Second
	if TestPollInterval > 0 {
		pollInterval = TestPollInterval
	}
	pauseSleep := 5 * time.Second
	if TestPauseSleep > 0 {
		pauseSleep = TestPauseSleep
	}

	log.DebugContext(ctx, "imapsync: loop start")

	tickerDur := 500 * time.Millisecond
	if TestTickerPeriod > 0 {
		tickerDur = TestTickerPeriod
	}
	ticker := time.NewTicker(tickerDur)
	defer ticker.Stop()

	// First "cycle" runs immediately on start-up; subsequent cycles wait
	// for the idle ticker OR for an error backoff.
	first := true

	for {
		// Fast-path: bail if context cancelled.
		select {
		case <-ctx.Done():
			log.DebugContext(ctx, "imapsync: loop exit (ctx cancelled)")
			return
		default:
		}

		// Check paused state – skip work, but keep ticking.
		if m.State(cfg.AccountID) == StatePaused {
			// Sleep a bit longer while paused to avoid hot loops.
			select {
			case <-ctx.Done():
				return
			case <-time.After(pauseSleep):
				continue
			}
		}

		if !first {
			// Sleep for the idle interval (or context cancel).
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
		first = false

		// --- Single sync cycle ------------------------------------------------
		m.setState(cfg.AccountID, StateSyncing)

		// Phase 7 concrete body is deliberately empty: we Dial via the
		// factory, log, and advance.  The real fetch/index phase comes
		// later when a go-imap adapter is wired.
		client, err := Dial(ctx, cfg)
		// Defensive nil-guard: DialMock / adapters may return (nil, nil)
		// which would otherwise skip the err-handling branch below and
		// fall through to a nil-deref on client.List / cycleClose.
		if client == nil && err == nil {
			err = fmt.Errorf("imapsync: Dial returned nil client with no error")
		}
		if err != nil {
			// ErrNoAdapter is soft – we still have a stub client.
			if client == nil {
				log.WarnContext(ctx, "imapsync: dial failed (backoff)", "error", err, "backoff", backoff)
				m.setState(cfg.AccountID, StateError)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff = minDur(backoff*2, maxBackoff)
				continue
			}
			// ErrNoAdapter with a usable stub client – fall through to the
			// (no-op) cycle.
		}
		// Close the client at the end of this cycle explicitly (defer inside
		// a loop would accumulate across iterations).  Guard against nil in
		// case any future error-handling path bypasses the early continue.
		cycleClose := func() {
			if client != nil {
				_ = client.Close(ctx)
			}
		}

		// --- Folder list cycle (stub) ---------------------------------------
		// Belt-and-suspenders: if somehow client is still nil here (e.g. a
		// future code path skips Dial), treat it as a dial error rather than
		// nil-dereferencing on client.List / Select / UIDFetchSince.
		if client == nil {
			log.WarnContext(ctx, "imapsync: nil client at start of cycle (backoff)", "backoff", backoff)
			m.setState(cfg.AccountID, StateError)
			cycleClose()
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = minDur(backoff*2, maxBackoff)
			continue
		}
		folders, err := client.List(ctx, "*")
		if err != nil {
			log.WarnContext(ctx, "imapsync: list failed (backoff)", "error", err, "backoff", backoff)
			m.setState(cfg.AccountID, StateError)
			cycleClose()
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = minDur(backoff*2, maxBackoff)
			continue
		}
		cycleCompleted := true
		for _, f := range folders {
			select {
			case <-ctx.Done():
				cycleClose()
				return
			default:
			}
			// Phase 7: select + fetch are no-ops via the StubClient.
			_, selErr := client.Select(ctx, f.Name)
			if selErr != nil {
				log.DebugContext(ctx, "imapsync: select stub (no-op)", "folder", f.Name)
			}
			// UIDFetchSince: fetch envelopes whose UID is newer than the
			// last checkpoint.  Tests mock this to observe incremental
			// UID fetching (UID1-3 on cycle 1, UID4 on cycle 2, etc.).
			_, feErr := client.UIDFetchSince(ctx, f.Name, "")
			if feErr != nil {
				log.WarnContext(ctx, "imapsync: UIDFetchSince failed (backoff)",
					"folder", f.Name, "error", feErr, "backoff", backoff)
				m.setState(cfg.AccountID, StateError)
				cycleCompleted = false
				break
			}
		}
		cycleClose()
		if !cycleCompleted {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = minDur(backoff*2, maxBackoff)
			continue
		}
		// Reset backoff on a successful cycle, flip to idle.
		backoff = 2 * time.Second
		m.setState(cfg.AccountID, StateIdle)
	}
}

// setState updates the in-memory state of a running account (if it still
// exists in the state map).  Used by loop.
func (m *Manager) setState(accountID string, st State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.state[accountID]; !ok {
		// Account was stopped already; don't resurrect state tracking.
		return
	}
	m.state[accountID] = st
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
