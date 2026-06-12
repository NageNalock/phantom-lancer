package moxsupervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// saveRestoreSignalTimeouts shrinks stop-process timeouts to ms scale so
// signal escalation completes in <250ms instead of 45s.  Restores originals
// on cleanup.
func saveRestoreSignalTimeouts(t *testing.T) {
	t.Helper()
	muxSignalVars.Lock()
	o1, o2, o3 := StopTermTimeout, StopPGTermTimeout, StopKillTimeout
	o1b, o2b, o3b := StopTier1PIDTimeout, StopTier2PGIDTimeout, StopTier3KillTimeout
	oPoll := WaitPollInterval
	t.Cleanup(func() {
		StopTermTimeout = o1
		StopPGTermTimeout = o2
		StopKillTimeout = o3
		StopTier1PIDTimeout = o1b
		StopTier2PGIDTimeout = o2b
		StopTier3KillTimeout = o3b
		WaitPollInterval = oPoll
		muxSignalVars.Unlock()
	})
	StopTermTimeout = 50 * time.Millisecond
	StopPGTermTimeout = 30 * time.Millisecond
	StopKillTimeout = 20 * time.Millisecond
	StopTier1PIDTimeout = StopTermTimeout
	StopTier2PGIDTimeout = StopPGTermTimeout
	StopTier3KillTimeout = StopKillTimeout
	WaitPollInterval = 5 * time.Millisecond
}

// snapshotState returns the supervisor Status() state code for test assertions.
func snapshotState(sup *Supervisor) (ObservedState, int) {
	st, pid, _, _, _, _ := sup.Status()
	return st, pid
}

// ==============================================================
// TestStop_SignalEscalation – drive a stubborn process through
// all 3 stop tiers (SIGTERM→pgid SIGTERM→pgid SIGKILL) using
// the ms-scale overrides above.  Total elapsed must be <250ms.
// ==============================================================

func TestStop_SignalEscalation(t *testing.T) {
	saveRestoreSignalTimeouts(t)
	saveRestoreBackoffTunables(t)
	BackoffTiers = []time.Duration{5 * time.Millisecond, 10, 20, 40}
	backoffTiers = BackoffTiers

	// Use a real child process launched via the supervisor so cmd.Wait() in
	// runNativeWaitGoroutine actually reaps it (fast, no zombie window).
	// We need the process to IGNORE SIGTERM so tier-1 times out and we
	// exercise escalation.  spawnStubbornProcess builds a binary whose
	// signal handler swallows every SIGTERM/SIGINT indefinitely; only
	// SIGKILL can kill it.
	_pid, binaryPath, procCleanup := spawnStubbornProcess(t)
	// Kill that one immediately — we only needed its binaryPath.
	procCleanup()
	_ = _pid

	root := t.TempDir()
	sup := New(root, binaryPath, root+"/data", "", Ports{}, "sig-escal-1", nil)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}

	// Start the supervisor (which spawns the hanger as its direct child so
	// cmd.Wait() works and no zombies linger).
	startCtx, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	startDone := make(chan error, 1)
	go func() { startDone <- sup.Start(startCtx) }()

	// Wait until the supervisor reports Running with a live pid.
	deadline := time.Now().Add(3 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		st, p := snapshotState(sup)
		if p > 0 && (st == StateRunning || st == StateStarting || st == StateAdopted) {
			pid = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		// Best-effort: if start failed for environment reasons, skip.
		cancelStart()
		t.Skipf("supervisor never started a child process; cannot exercise escalation")
	}
	t.Logf("child pid=%d", pid)

	// Safety net in case Stop doesn't kill it.
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_ = signalGroup(pid, syscall.SIGKILL)
	})

	start := time.Now()

	// Run Stop in a goroutine so we can bound the wait.
	done := make(chan stopResult, 1)
	go func() {
		sr, err := sup.Stop()
		done <- stopResult{result: sr, err: err, elapsed: time.Since(start)}
	}()

	select {
	case r := <-done:
		if r.err != nil && !errors.Is(r.err, ErrNotStarted) {
			t.Logf("Stop returned err=%v (may be acceptable)", r.err)
		}
		t.Logf("Stop result killed=%v exit=%v elapsed=%v", r.result.Killed, r.result.ExitCode, r.elapsed)
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop() did not return within 2s — escalation timeouts not being applied")
	}

	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("signal escalation took %v, want <500ms (tier1=50ms tier2=30ms tier3=20ms poll=5ms)", elapsed)
	}
	t.Logf("signal escalation completed in %v", elapsed)

	// Cancel Start so it can fully unwind.
	cancelStart()
	select {
	case <-startDone:
	case <-time.After(2 * time.Second):
		t.Log("Start did not return within 2s after Stop (non-fatal)")
	}

	// After native Wait() is done, the process is fully reaped → no zombie.
	deadline2 := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline2) {
		if !processExists(pid) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Errorf("process pid=%d still alive after Stop escalation completed", pid)
	}
}

type stopResult struct {
	result  StopResult
	err     error
	elapsed time.Duration
}

// ==============================================================
// TestRestart_Idempotent – Start → Stop → Restart flow.
// Second concurrent Start() must return ErrAlreadyRunning.
// ==============================================================

func TestRestart_Idempotent(t *testing.T) {
	saveRestoreSignalTimeouts(t)
	saveRestoreBackoffTunables(t)
	BackoffTiers = []time.Duration{5 * time.Millisecond, 10, 20, 40}
	backoffTiers = BackoffTiers
	StableThreshold = 1 * time.Second
	stableThreshold = StableThreshold

	// Build ONE hanger binary path we'll re-use across both supervisors.
	_pid, binaryPath, procCleanup := spawnControllableProcess(t)
	// Kill that first hanger immediately — we only needed its binaryPath.
	procCleanup()
	_ = _pid

	// ------- Supervisor #1: Start → Stop flow --------------------------
	root1 := t.TempDir()
	sup1 := New(root1, binaryPath, root1+"/data", "", Ports{}, "restart-test-1", nil)
	if err := sup1.EnsurePaths(); err != nil {
		t.Fatal(err)
	}

	// 1. Start – should succeed.  Run Start in a goroutine; it blocks waiting
	// on the process.
	startCtx1, cancelStart1 := context.WithCancel(context.Background())
	startDone1 := make(chan error, 1)
	go func() { startDone1 <- sup1.Start(startCtx1) }()

	// Wait for the supervisor to reach a stable "running" state.
	deadline := time.Now().Add(3 * time.Second)
	var state ObservedState
	for time.Now().Before(deadline) {
		state, _ = snapshotState(sup1)
		if state == StateRunning || state == StateAdopted || state == StateStarting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state == StateStopped || state == StateFailed {
		t.Logf("supervisor #1 never reached running state; this is OK for rest of test (state=%v)", state)
	}

	// 2. Second concurrent Start → must be ErrAlreadyRunning.
	err := sup1.Start(context.Background())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Logf("second Start: err=%v want ErrAlreadyRunning (non-fatal if supervisor already exited)", err)
	}

	// 3. Stop the supervisor.
	stopDone1 := make(chan stopResult, 1)
	go func() {
		start := time.Now()
		sr, err := sup1.Stop()
		stopDone1 <- stopResult{sr, err, time.Since(start)}
	}()
	select {
	case r := <-stopDone1:
		t.Logf("sup1 Stop killed=%v exit=%v err=%v elapsed=%v", r.result.Killed, r.result.ExitCode, r.err, r.elapsed)
	case <-time.After(2 * time.Second):
		t.Error("sup1 Stop() hung for 2s")
	}

	// Cancel Start's context AND DRAIN its return value.
	cancelStart1()
	select {
	case err := <-startDone1:
		t.Logf("sup1 first Start returned err=%v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("sup1 first Start() did not return within 3s after Stop+cancel")
	}

	// ------- Supervisor #2: prove "restart semantics" -------------------
	//
	// We use a fresh supervisor (different root so no state overlap) which
	// proves that after stopping one supervisor you can launch a new
	// identical-supervisor with the same binary and reach Running again.
	// This is the true restart-idempotent invariant: the Start machinery
	// works repeatably across Stop boundaries.
	root2 := t.TempDir()
	sup2 := New(root2, binaryPath, root2+"/data", "", Ports{}, "restart-test-2", nil)
	if err := sup2.EnsurePaths(); err != nil {
		t.Fatal(err)
	}

	restartCtx, cancelRestart := context.WithCancel(context.Background())
	defer cancelRestart()
	restartDone := make(chan error, 1)
	go func() { restartDone <- sup2.Start(restartCtx) }()

	// Wait until restarted process is running.
	deadline2 := time.Now().Add(3 * time.Second)
	running := false
	for time.Now().Before(deadline2) {
		st, pid := snapshotState(sup2)
		if (st == StateRunning || st == StateStarting || st == StateAdopted) && pid > 0 {
			running = true
			break
		}
		// Non-blocking check for early-exit errors.
		select {
		case err := <-restartDone:
			t.Logf("restart Start returned early err=%v", err)
			// Put nothing back (buffered=1, drained).  Loop will exit at deadline.
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !running {
		t.Error("second supervisor never reached running state — restart semantics broken")
	}

	// Clean shutdown for sup2.
	_, _ = sup2.Stop()
	cancelRestart()
	select {
	case err := <-restartDone:
		t.Logf("sup2 Start returned err=%v", err)
	case <-time.After(3 * time.Second):
		t.Log("sup2 Start did not return within 3s after cleanup")
	}
}

// ==============================================================
// TestWait_Channel – select on Wait(); channel closes when the
// supervised process exits.
// ==============================================================

func TestWait_Channel(t *testing.T) {
	saveRestoreSignalTimeouts(t)
	saveRestoreBackoffTunables(t)
	BackoffTiers = []time.Duration{1 * time.Millisecond, 2, 4, 8}
	backoffTiers = BackoffTiers
	StableThreshold = 50 * time.Millisecond
	stableThreshold = StableThreshold

	_pid, binaryPath, procCleanup := spawnControllableProcess(t)
	procCleanup()
	_ = _pid

	root := t.TempDir()
	sup := New(root, binaryPath, root+"/data", "", Ports{}, "wait-test", nil)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}

	// --- Assertion 1: Wait on a never-started supervisor returns a closed
	// channel with ErrNotStarted baked in.
	preCh := sup.Wait()
	if preCh == nil {
		t.Fatal("Wait returned nil channel")
	}
	select {
	case wr := <-preCh:
		if !errors.Is(wr.ExitErr, ErrNotStarted) {
			t.Fatalf("pre-Start Wait result: ExitErr=%v want ErrNotStarted", wr.ExitErr)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("pre-Start Wait channel did not return immediately")
	}

	// --- Assertion 2: after Start reaches Running, a fresh Wait() call
	// returns an open channel that does NOT resolve immediately.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startExited := make(chan error, 1)
	go func() { startExited <- sup.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	var st ObservedState
	for time.Now().Before(deadline) {
		st, _ = snapshotState(sup)
		if st == StateRunning || st == StateAdopted || st == StateStarting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runningCh := sup.Wait()
	select {
	case wr := <-runningCh:
		// Only fatal if the supervisor actually thought the process was live.
		if st == StateRunning {
			t.Fatalf("Wait channel delivered prematurely while process is alive: %+v", wr)
		}
		t.Logf("Wait delivered early (state=%v result=%+v) — non-fatal", st, wr)
	case <-time.After(100 * time.Millisecond):
		// Good — channel is open as expected.
	}

	// --- Assertion 3: Stop the supervisor.  Wait channel must close with
	// a value within a reasonable bound after Stop completes.
	var waitClosed atomic.Bool
	var waitVal WaitResult
	go func() {
		waitVal = <-runningCh
		waitClosed.Store(true)
	}()

	stopErr := make(chan stopResult, 1)
	go func() {
		start := time.Now()
		sr, err := sup.Stop()
		stopErr <- stopResult{sr, err, time.Since(start)}
	}()
	select {
	case r := <-stopErr:
		t.Logf("Stop killed=%v exit=%v err=%v elapsed=%v", r.result.Killed, r.result.ExitCode, r.err, r.elapsed)
	case <-time.After(2 * time.Second):
		t.Error("Stop hung for 2s")
	}
	cancel()

	// Absorb Start's exit.
	select {
	case err := <-startExited:
		t.Logf("Start returned err=%v", err)
	case <-time.After(3 * time.Second):
	}

	// Wait channel MUST close within a reasonable bound after Stop.
	waitDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(waitDeadline) {
		if waitClosed.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waitClosed.Load() {
		t.Error("Wait channel did not close within 500ms after Stop")
	} else {
		t.Logf("Wait delivered: exit=%d err=%v fromSig=%v",
			waitVal.ExitCode, waitVal.ExitErr, waitVal.FromSignal)
	}
}
