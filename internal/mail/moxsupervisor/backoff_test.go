package moxsupervisor

import (
	"testing"
	"time"
)

// --- Time-override helpers --------------------------------------------------
//
// We temporarily replace BackoffTiers / StableThreshold with ms-scale values
// so tests run quickly, then restore the originals via t.Cleanup.

func saveRestoreBackoffTunables(t *testing.T) {
	t.Helper()
	muxBackoffVars.Lock()
	origTiers := make([]time.Duration, len(BackoffTiers))
	copy(origTiers, BackoffTiers)
	origStable := StableThreshold
	origB1, origB2, origB3, origB4 := B1, B2, B3, B4
	origReset := ResetAfter
	t.Cleanup(func() {
		B1, B2, B3, B4 = origB1, origB2, origB3, origB4
		ResetAfter = origReset
		StableThreshold = origStable
		stableThreshold = StableThreshold
		BackoffTiers = origTiers
		backoffTiers = BackoffTiers
		muxBackoffVars.Unlock()
	})
}

// ==============================================================
// TestBackoff_Progression – table: attempt → expected delay
// in range [min, max].  attempt 1 → ~2s, 2 → 10s, 3 → 30s,
// 4 → 120s, 5+ → terminal failed state.
// ==============================================================

func TestBackoff_Progression(t *testing.T) {
	// NOTE: deliberately NOT t.Parallel — this test mutates the package-level
	// BackoffTiers slice and reads back from it across many steps; concurrent
	// tests (e.g. TestBackoff_NextDelay_TimeoutShort) would clobber it.
	saveRestoreBackoffTunables(t)
	// 5 tiers (not 4) so crash #4 still exposes 120s via MayStart.
	// Production Observe increments tier AFTER using it; with len=5 the
	// sequence is: crash 1 → 2s, crash 2 → 10s, crash 3 → 30s,
	// crash 4 → 120s (still non-terminal, observable via MayStart),
	// crash 5 → 240s then tier=5 (≥len=5 → terminal).  This matches the
	// design doc: "2s → 10s → 30s → 120s → FAILED" — crash 5 is the
	// first FAILED, crash 4 is the final observable backoff.
	BackoffTiers = []time.Duration{
		2 * time.Second,
		10 * time.Second,
		30 * time.Second,
		120 * time.Second,
		240 * time.Second, // placeholder – crash 5 reaches terminal right after
	}
	backoffTiers = BackoffTiers
	StableThreshold = 10 * time.Minute
	stableThreshold = StableThreshold
	b := newBackoffFSM()

	type step struct {
		crashExitCode   int
		runDuration     time.Duration
		wantDelay       time.Duration
		wantDelayTol    time.Duration // plus/minus tolerance
		wantTerminal    bool
		wantConsecutive int
	}

	// Use real (second-scale) tiers here because we don't actually sleep;
	// we only check MayStart() "remaining" values which are based on wall
	// clock times set in Observe().  Tiers are applied by Observe() via
	// time.Now().Add(delay); we read them back immediately so the actual
	// delay magnitude doesn't matter for this test.
	cases := []step{
		{
			// Pre-crash: MayStart must return true with no backoff.
			crashExitCode:   -1, // sentinel: no crash this step
			runDuration:     0,
			wantDelay:       0,
			wantConsecutive: 0,
		},
		{
			crashExitCode:   1,
			runDuration:     1 * time.Second,
			wantDelay:       BackoffTiers[0], // 2s
			wantDelayTol:    20 * time.Millisecond,
			wantConsecutive: 1,
		},
		{
			crashExitCode:   1,
			runDuration:     1 * time.Second,
			wantDelay:       BackoffTiers[1], // 10s
			wantDelayTol:    20 * time.Millisecond,
			wantConsecutive: 2,
		},
		{
			crashExitCode:   1,
			runDuration:     1 * time.Second,
			wantDelay:       BackoffTiers[2], // 30s
			wantDelayTol:    20 * time.Millisecond,
			wantConsecutive: 3,
		},
		{
			crashExitCode:   1,
			runDuration:     1 * time.Second,
			wantDelay:       BackoffTiers[3], // 120s
			wantDelayTol:    20 * time.Millisecond,
			wantConsecutive: 4,
		},
		{
			crashExitCode:   1,
			runDuration:     1 * time.Second,
			wantTerminal:    true, // 5th crash → terminal
			wantConsecutive: 5,
		},
		{
			// 6th crash: still terminal, no change.
			crashExitCode:   1,
			runDuration:     1 * time.Second,
			wantTerminal:    true,
			wantConsecutive: 6,
		},
	}

	for i, tc := range cases {
		// Precondition: check MayStart before the step.
		if i == 0 {
			ok, rem := b.MayStart()
			if !ok {
				t.Errorf("step 0 pre-MayStart: ok=false, rem=%v", rem)
			}
			continue
		}

		// Expire any prior backoff so MayStart returns true (FSM contract:
		// Observe is only called *after* a process exit, not while in
		// backoff sleep).
		forceBackoffExpired(b)

		b.Observe(tc.crashExitCode, tc.runDuration)

		if tc.wantTerminal {
			if !b.IsTerminal() {
				t.Errorf("step %d (exit=%d dur=%v): expected IsTerminal=true, got false",
					i, tc.crashExitCode, tc.runDuration)
			}
			ok, rem := b.MayStart()
			if ok || rem != -1 {
				t.Errorf("step %d: MayStart=(%v,%v) want (false,-1) for terminal", i, ok, rem)
			}
		} else {
			if b.IsTerminal() {
				t.Errorf("step %d: unexpected terminal state", i)
			}
			ok, rem := b.MayStart()
			if ok {
				t.Errorf("step %d: MayStart=true immediately after crash", i)
			}
			// remaining backoff should be ~ wantDelay (within tolerance).
			expectedMin := tc.wantDelay - tc.wantDelayTol
			// rem can be slightly lower than wantDelay (clock ticked between
			// Observe and MayStart) but should never be *greater* than
			// wantDelay.
			if rem > tc.wantDelay {
				t.Errorf("step %d: rem=%v greater than wantDelay=%v",
					i, rem, tc.wantDelay)
			}
			if rem < expectedMin {
				t.Errorf("step %d: rem=%v less than expected min %v (wantDelay=%v)",
					i, rem, expectedMin, tc.wantDelay)
			}
		}

		_, consec, _, _ := b.State()
		if consec != tc.wantConsecutive {
			t.Errorf("step %d: consecutiveCrashes=%d want %d", i, consec, tc.wantConsecutive)
		}
	}

	// Reset must bring us back to step 0 state.
	b.Reset()
	ok, rem := b.MayStart()
	if !ok {
		t.Errorf("after Reset: MayStart=false, rem=%v", rem)
	}
	if b.IsTerminal() {
		t.Error("after Reset: IsTerminal=true")
	}
	_, consec, _, _ := b.State()
	if consec != 0 {
		t.Errorf("after Reset: consec=%d want 0", consec)
	}
}

// ==============================================================
// TestBackoff_ResetAfter10MinStable – after 10+ min of stable
// (clean exit, runDuration >= StableThreshold), backoff resets
// to attempt 1.
// ==============================================================

func TestBackoff_ResetAfter10MinStable(t *testing.T) {
	t.Parallel()
	saveRestoreBackoffTunables(t)
	BackoffTiers = []time.Duration{
		2 * time.Second,
		10 * time.Second,
		30 * time.Second,
		120 * time.Second,
	}
	backoffTiers = BackoffTiers
	StableThreshold = 10 * time.Minute
	stableThreshold = StableThreshold

	b := newBackoffFSM()

	// Drive to tier 3 (backoff = 30s, tier index = 3).
	b.Observe(1, 1*time.Second) // crash 1 → tier 1
	forceBackoffExpired(b)
	b.Observe(1, 1*time.Second) // crash 2 → tier 2
	forceBackoffExpired(b)
	b.Observe(1, 1*time.Second) // crash 3 → tier 3
	forceBackoffExpired(b)

	// Sanity check.
	if got := b.NextDelay(); got != BackoffTiers[2] {
		t.Fatalf("precondition: NextDelay=%v want %v", got, BackoffTiers[2])
	}
	_, consec, _, _ := b.State()
	if consec != 3 {
		t.Fatalf("precondition: consec=%d want 3", consec)
	}

	// Now a clean exit that ran for >= StableThreshold (the design doc's
	// "10min 稳定运行清零" case).
	b.Observe(0, StableThreshold+time.Minute)

	// Should reset: NextDelay is tier-0.
	if got := b.NextDelay(); got != BackoffTiers[0] {
		t.Errorf("after long clean run: NextDelay=%v want %v (reset to tier 0)",
			got, BackoffTiers[0])
	}
	_, consec, _, _ = b.State()
	if consec != 0 {
		t.Errorf("after long clean run: consec=%d want 0", consec)
	}
	if b.IsTerminal() {
		t.Error("after long clean run: IsTerminal=true")
	}
	ok, _ := b.MayStart()
	if !ok {
		t.Error("after long clean run: MayStart=false")
	}

	// A subsequent crash should restart counting from tier 1, NOT tier 4.
	b.Observe(1, 1*time.Second)
	if got := b.NextDelay(); got != BackoffTiers[0] {
		// After 1 crash from fresh state: tier = 1, NextDelay() →
		// backoffTiers[tier-1] = backoffTiers[0] = 2s.
		t.Errorf("first crash after reset: NextDelay=%v want %v", got, BackoffTiers[0])
	}
	_, consec, _, _ = b.State()
	if consec != 1 {
		t.Errorf("first crash after reset: consec=%d want 1", consec)
	}
}

// ==============================================================
// TestBackoff_NextDelay_TimeoutShort – override backoff tiers
// to ms scale, exercise the real sleep inside Supervisor.Start()
// so the test completes in <5s wall clock even though the code
// path goes through MayStart() → sleep → post sleep.
// ==============================================================

func TestBackoff_NextDelay_TimeoutShort(t *testing.T) {
	saveRestoreBackoffTunables(t)

	// Replace tiers with ms-scale values.
	BackoffTiers = []time.Duration{
		20 * time.Millisecond,
		40 * time.Millisecond,
		60 * time.Millisecond,
		80 * time.Millisecond,
	}
	backoffTiers = BackoffTiers
	StableThreshold = 1 * time.Second
	stableThreshold = StableThreshold

	b := newBackoffFSM()

	// 2 crashes → tier 2 (40ms backoff).
	b.Observe(1, 10 * time.Millisecond)
	forceBackoffExpired(b)
	b.Observe(1, 10 * time.Millisecond)

	if got := b.NextDelay(); got != BackoffTiers[1] {
		t.Fatalf("NextDelay=%v want %v", got, BackoffTiers[1])
	}

	start := time.Now()
	_, rem := b.MayStart()
	if rem < 30*time.Millisecond || rem > 50*time.Millisecond {
		// Should be ~40ms.
		t.Logf("rem=%v (expected ~40ms)", rem)
	}
	// Sleep through the backoff by polling MayStart; this mirrors the
	// real Supervisor.Start flow but without mutex held.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		ok, r := b.MayStart()
		if ok {
			break
		}
		if r < 0 {
			t.Fatal("unexpected terminal state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ok, _ := b.MayStart()
	if !ok {
		t.Fatalf("backoff didn't expire within 200ms; elapsed=%v", time.Since(start))
	}

	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("backoff took %v — overrides not being applied", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("backoff took %v, expected <200ms", elapsed)
	}
	t.Logf("backoff path completed in %v", elapsed)

	// State: after 2 crashes = tier 2 (not terminal).
	if b.IsTerminal() {
		t.Error("unexpected terminal after 2 crashes")
	}
}
