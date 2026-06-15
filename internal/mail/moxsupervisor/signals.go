package moxsupervisor

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Stop policy from §5.1.3 of the design doc.  These durations are deliberate
// – Mox typically exits in <2s, but slow machines / large mail spools / a
// queue full of outbound deliveries can push finalisation to the 20s range.
// We err on the side of patience before escalating.
//
// The durations are exported as package-level vars (not consts) so unit tests
// can replace them with millisecond-scale values to keep tests fast.
var (
	StopTier1PIDTimeout  = 30 * time.Second // graceful SIGTERM to pid
	StopTier2PGIDTimeout = 10 * time.Second // SIGTERM to whole process group
	StopTier3KillTimeout = 5 * time.Second  // SIGKILL (force) to pgid
	// WaitPollInterval controls how often waitAdoptedWithTimeout polls for
	// process exit.  Keep at 100ms for production (low CPU + fast enough
	// for 30s tier timeouts); tests override this for sub-second latency.
	WaitPollInterval = 100 * time.Millisecond
)

// Canonical names used by Phase 8 unit tests (aliases of the tier vars above).
// Tests override these directly instead of the tier-named vars.
var (
	StopTermTimeout    = StopTier1PIDTimeout  // 30s graceful SIGTERM to pid
	StopPGTermTimeout  = StopTier2PGIDTimeout // 10s SIGTERM to pgid
	StopKillTimeout    = StopTier3KillTimeout // 5s SIGKILL to pgid
)
// backcompat aliases – existing code may reference the stopTier* names
var (
	_ = StopTier1PIDTimeout
)
const (
	// Deprecated: historical names kept as compile-time hints.  The
	// exported vars above are the real tunables.
	stopTier1PIDTimeoutUnused  = 30 * time.Second
	stopTier2PGIDTimeoutUnused = 10 * time.Second
	stopTier3KillTimeoutUnused = 5 * time.Second
)

// StopResult reports how shutdown concluded.  It is used by the UI to show
// "graceful" vs "forced" and by audit to tag risk level.
type StopResult struct {
	ExitCode    int
	SignalUsed  syscall.Signal // 0 for natural exit
	Killed      bool          // true if we had to SIGKILL
	Duration    time.Duration
	Reason      string        // human-readable summary, for logs/audit
}

// signalGroup signals every member of the process group (pgid = -pid).  This
// includes Mox itself plus any child workers (ACME, delivery queue, ...).
// We always signal the group during tier 2+ to avoid orphaning workers whose
// parent has already exited.
func signalGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("moxsupervisor: signalGroup: invalid pid %d", pid)
	}
	// Primary path: signal the process group via -pid (covers mox + children).
	// On macOS (and some container configurations) signaling a process group
	// that lives in a different session returns EPERM even though the caller
	// owns the process.  Fall back to signaling the leader pid directly in
	// that case so the kill path still terminates the target.
	proc, err := os.FindProcess(-pid) // negative pid = pgid
	if err == nil {
		err = proc.Signal(sig)
	}
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EPERM) {
		return err
	}
	// EPERM fallback: signal just the leader pid.  This is slightly weaker
	// (grandchildren are not reaped synchronously) but it correctly kills
	// the target, which is the contract.
	proc2, ferr := os.FindProcess(pid)
	if ferr != nil {
		return fmt.Errorf("moxsupervisor: signalGroup: pgid EPERM fallback find pid %d: %w (orig pgid err: %v)", pid, ferr, err)
	}
	if serr := proc2.Signal(sig); serr != nil {
		return fmt.Errorf("moxsupervisor: signalGroup: pgid EPERM fallback signal pid %d: %w (orig pgid err: %v)", pid, serr, err)
	}
	return nil
}

// processGroupExists reports whether any member of pid's process group is
// still alive.  We use this to decide when a tier has concluded early.
func processGroupExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(-pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// waitProcessWithTimeout blocks until the cmd process exits OR until
// deadline elapses.  Returns (exitCode, exited, fromSignal).
//
// IMPORTANT: this function MUST NOT call cmd.Wait() –
// runNativeWaitGoroutine owns the single cmd.Wait() call.  Double-calling
// Wait on the same *exec.Cmd is undefined behaviour.  We instead poll via
// signal(0) ("does the process still exist?") which is cheap and correct
// for the tier-timeout scale (tens of seconds).
//
// exitCode / fromSignal are returned as best-effort (-1 / false) when the
// process has exited: the authoritative exit code comes from
// runNativeWaitGoroutine via WaitResult.
func waitProcessWithTimeout(cmd *exec.Cmd, deadline time.Time) (exitCode int, exited bool, fromSignal bool) {
	if cmd == nil || cmd.Process == nil {
		return -1, true, false
	}
	pid := cmd.Process.Pid
	if waitAdoptedWithTimeout(pid, deadline) {
		return -1, true, false
	}
	return -1, false, false
}

// waitAdoptedWithTimeout is the adopted-process version of
// waitProcessWithTimeout.  Since we didn't Start() the process from Go, we
// can't use cmd.Wait(); fall back to signal(0) polling at 100ms cadence
// which is plenty fast for the tier-timeout scale (tens of seconds).
func waitAdoptedWithTimeout(pid int, deadline time.Time) bool {
	tick := time.NewTicker(WaitPollInterval)
	defer tick.Stop()
	for range tick.C {
		if !processExists(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
	return !processExists(pid)
}

// stopProcess performs the three-tier shutdown.  It is intentionally
// *stateless* – it takes every value it needs as an argument so the caller
// can release the supervisor mutex across the ~45s three-tier escalation.
// That guarantees Status() never blocks on Stop().
func stopProcess(cmd *exec.Cmd, isAdopted bool, markerPath, pidPath string, lg *slog.Logger) (StopResult, error) {
	start := time.Now()
	if cmd == nil || cmd.Process == nil {
		return StopResult{}, ErrNotStarted
	}
	pid := cmd.Process.Pid
	if lg == nil {
		lg = slog.Default()
	}
	lg = lg.With("pid", pid, "adopted", isAdopted)

	// Tier 1: graceful SIGTERM to the PID itself.
	lg.Debug("moxsupervisor: stop tier 1 — SIGTERM pid")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	deadline1 := start.Add(StopTermTimeout)
	var exited bool
	var exitCode int
	var fromSig bool
	if isAdopted {
		exited = waitAdoptedWithTimeout(pid, deadline1)
	} else {
		exitCode, exited, fromSig = waitProcessWithTimeout(cmd, deadline1)
	}
	if exited {
		r := StopResult{ExitCode: exitCode, Killed: fromSig, Duration: time.Since(start)}
		if fromSig {
			r.SignalUsed = syscall.Signal(exitCode)
			r.Reason = fmt.Sprintf("exited via signal %s (tier 1)", r.SignalUsed)
		} else {
			r.Reason = "clean exit after SIGTERM (tier 1)"
		}
		cleanupMarkerAndPID(markerPath, pidPath)
		return r, nil
	}

	// Tier 2: SIGTERM to the whole process group.
	lg.Warn("moxsupervisor: stop tier 2 — escalating to SIGTERM pgid after tier-1 timeout", "tier1_elapsed", time.Since(start))
	if err := signalGroup(pid, syscall.SIGTERM); err != nil {
		// Maybe the process actually died between tier-1 timeout and now.
		if !processGroupExists(pid) && !isAdopted {
			_ = cmd.Process.Release()
			exitCode, _, _ = waitProcessWithTimeout(cmd, time.Now().Add(1*time.Second))
			r := StopResult{ExitCode: exitCode, Killed: false, Duration: time.Since(start)}
			r.Reason = "clean exit between tiers"
			cleanupMarkerAndPID(markerPath, pidPath)
			return r, nil
		}
		// Continue – worst case tier 3 will SIGKILL.
		lg.Warn("moxsupervisor: stop tier 2 signalGroup failed", "error", err)
	}
	deadline2 := time.Now().Add(StopPGTermTimeout)
	if isAdopted {
		exited = waitAdoptedWithTimeout(pid, deadline2)
	} else {
		exitCode, exited, fromSig = waitProcessWithTimeout(cmd, deadline2)
	}
	if exited {
		r := StopResult{ExitCode: exitCode, Killed: fromSig, Duration: time.Since(start), SignalUsed: syscall.SIGTERM}
		r.Reason = "exited after SIGTERM to process group (tier 2)"
		cleanupMarkerAndPID(markerPath, pidPath)
		return r, nil
	}

	// Tier 3: SIGKILL to the whole process group.  Non-negotiable – if we
	// reach this point Mox has refused two polite requests to exit and
	// something is wedged.  Record it at WARN so operators see the kill.
	lg.Warn("moxsupervisor: stop tier 3 — escalating to SIGKILL pgid", "elapsed", time.Since(start))
	if err := signalGroup(pid, syscall.SIGKILL); err != nil {
		lg.Error("moxsupervisor: stop tier 3 SIGKILL failed", "error", err)
	}
	deadline3 := time.Now().Add(StopKillTimeout)
	if isAdopted {
		waitAdoptedWithTimeout(pid, deadline3)
		exitCode = -1
		fromSig = true
	} else {
		exitCode, _, fromSig = waitProcessWithTimeout(cmd, deadline3)
	}
	r := StopResult{
		ExitCode:   exitCode,
		Killed:     true,
		SignalUsed: syscall.SIGKILL,
		Duration:   time.Since(start),
		Reason:     "SIGKILL to process group (tier 3) – process was wedged",
	}
	cleanupMarkerAndPID(markerPath, pidPath)
	lg.Warn("moxsupervisor: stop tier 3 completed",
		"pid", pid,
		"exit_code", exitCode,
		"killed", r.Killed,
		"signal", r.SignalUsed,
		"duration", r.Duration,
		"reason", r.Reason)
	return r, nil
}

// cleanupMarkerAndPID removes the marker + pidfile after a clean stop so the
// next boot correctly sees "nothing running".  We ignore errors here – if
// the disk is full the marker will be stale and Adopt() will correctly
// reject it next boot because the pid no longer exists.
func cleanupMarkerAndPID(markerPath, pidPath string) {
	if markerPath != "" {
		_ = os.Remove(markerPath)
	}
	if pidPath != "" {
		_ = os.Remove(pidPath)
	}
}
