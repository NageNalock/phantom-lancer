package moxsupervisor

import (
	"sync"
	"time"
)

// Canonical per-tier backoff values from the design doc §5.1.4.
// Kept as package-level vars (not consts) so unit tests can shrink them
// to millisecond scale to keep tests fast.
var (
	B1 = 2 * time.Second   // first crash → 2s
	B2 = 10 * time.Second  // second → 10s
	B3 = 30 * time.Second  // third → 30s
	B4 = 120 * time.Second // fourth → 2m (last tier before FAILED)
	// ResetAfter is how long a process must run cleanly before crash-loop
	// accounting resets to zero.  Matches the design doc "10min 稳定运行清零".
	ResetAfter = 10 * time.Minute
)

// BackoffTiers is the ordered tier list.  The last tier ("B4") is the last
// observable non-terminal tier; a 5th consecutive crash reaches FAILED.
//
// Package-level variable so unit tests can swap in ms-scale values.
var BackoffTiers = []time.Duration{
	B1,
	B2,
	B3,
	B4,
}

// backoffTiers (internal name) is kept as an alias for backwards compat –
// existing references in this package use the same slice.
var backoffTiers = BackoffTiers

// StableThreshold is how long a process must run without an unclean exit
// before crash-loop accounting resets to zero.  Alias of ResetAfter.
var StableThreshold = ResetAfter

// backcompat alias
var stableThreshold = StableThreshold

// backoffFSM encapsulates the crash-loop state machine.  It is NOT safe for
// concurrent use; the Supervisor already holds mu when calling into it.
type backoffFSM struct {
	mu                sync.Mutex
	tier              int           // current index into backoffTiers, or len for FAILED
	consecutiveCrashes int          // reset after stableThreshold uptime
	lastCleanExit     time.Time     // last time an exit was code=0
	stableSince       time.Time     // when current run reached the stable threshold
	lastBackoffUntil  time.Time     // until which Start() must sleep
}

func newBackoffFSM() *backoffFSM {
	return &backoffFSM{tier: 0}
}

// State returns a snapshot suitable for persisting into runtime_state.
func (b *backoffFSM) State() (CrashLoopState, int, time.Time, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tier >= len(backoffTiers) {
		return CLFailed, b.consecutiveCrashes, b.stableSince, b.lastBackoffUntil
	}
	if b.tier > 0 {
		return CLBackoff, b.consecutiveCrashes, b.stableSince, b.lastBackoffUntil
	}
	return CLStable, b.consecutiveCrashes, b.stableSince, b.lastBackoffUntil
}

// Observe records an exit from a process.  exitCode=0 counts as a clean
// exit and begins / extends the stable-since timer.  Anything else
// increments the crash tier (unless stableThreshold has elapsed since the
// previous clean exit – in which case we treat it as a fresh incident).
func (b *backoffFSM) Observe(exitCode int, runDuration time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	clean := exitCode == 0
	if clean {
		b.lastCleanExit = time.Now()
		// If this run lasted >= stableThreshold we reset the counter.
		if runDuration >= stableThreshold {
			b.tier = 0
			b.consecutiveCrashes = 0
			b.stableSince = time.Now().Add(-stableThreshold)
			b.lastBackoffUntil = time.Time{}
		}
		return
	}

	// Uncle an exit.
	b.consecutiveCrashes++
	// Stability reset: if the PREVIOUS run was clean and lasted the full
	// stable threshold, we start counting at tier 1 again rather than
	// carrying forward the previous crash count.  This protects a
	// long-running process that suddenly starts crashing (e.g. after a
	// config reload) from inheriting week-old crash state.
	if !b.stableSince.IsZero() && time.Since(b.stableSince) >= stableThreshold {
		b.tier = 0
		b.consecutiveCrashes = 1
	}
	if b.tier < len(backoffTiers) {
		delay := backoffTiers[b.tier]
		b.lastBackoffUntil = time.Now().Add(delay)
		b.tier++
	}
	// else: tier is already at FAILED (>= len(backoffTiers)); keep it there
	// until the operator calls Reset.
}

// MayStart returns (true, 0) if it's OK to start a new process now, or
// (false, remainingBackoff) if the caller should sleep first.  If the FSM
// is in the terminal FAILED state it returns ErrCrashLoopExhausted-style
// info via a special sentinel in state.
func (b *backoffFSM) MayStart() (allowed bool, remaining time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tier >= len(backoffTiers) {
		return false, -1 // terminal FAILED
	}
	if !b.lastBackoffUntil.IsZero() {
		rem := time.Until(b.lastBackoffUntil)
		if rem > 0 {
			return false, rem
		}
	}
	return true, 0
}

// IsTerminal reports whether the FSM has reached the "failed" terminal
// state and requires manual intervention.
func (b *backoffFSM) IsTerminal() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tier >= len(backoffTiers)
}

// Reset clears all crash-loop state.  Called by the operator UI action
// "reset crash loop".
func (b *backoffFSM) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tier = 0
	b.consecutiveCrashes = 0
	b.stableSince = time.Time{}
	b.lastBackoffUntil = time.Time{}
}

// NextDelay returns the NEXT delay that will be applied (for UI display).
// Returns 0 if stable or terminal.
func (b *backoffFSM) NextDelay() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tier >= len(backoffTiers) {
		return 0
	}
	if b.tier == 0 {
		return backoffTiers[0]
	}
	return backoffTiers[b.tier-1]
}
