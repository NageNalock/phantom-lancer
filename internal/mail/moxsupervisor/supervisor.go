package moxsupervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// EnsurePaths creates every directory the supervisor needs.  It's safe to
// call multiple times; on repeated calls it becomes a no-op.
//
// Layout under MoxRoot:
//
//	bin/        mox binary install (managed by moxbinary pkg)
//	data/       mox --data directory
//	config/     mox config file
//	logs/       stdout + stderr append logs (rotated externally)
//	run/        marker + pidfile (tmpfs in production deployments)
func (s *Supervisor) EnsurePaths() error {
	if s.MoxRoot == "" {
		return fmt.Errorf("moxsupervisor: empty MoxRoot")
	}
	subs := []string{"bin", "data", "config", "logs", "run"}
	for _, sub := range subs {
		p := filepath.Join(s.MoxRoot, sub)
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("moxsupervisor: mkdir %s: %w", p, err)
		}
	}
	return nil
}

// Start launches Mox.  The full sequence is:
//
//  1. Import-read-only guard → ErrNotStarted.
//  2. Already-running check → ErrAlreadyRunning.
//  3. Crash-loop backoff: MayStart()? sleep for remaining backoff.
//  4. Generate fresh boot_id.
//  5. Preflight (binary + ports + config test).  Any failure wraps
//     ErrPreflightFailed with the first issue; callers can also call
//     Preflight() themselves for the full breakdown.
//  6. Open stdout/stderr append log files (O_CREATE | O_APPEND | O_WRONLY,
//     0600).
//  7. Build exec.Cmd with explicit argv (NO shell), SysProcAttr{Setpgid:true}
//     so we can signal the process group during stop.
//  8. cmd.Start() – if this fails, record the attempt in crash-loop backoff
//     and return the error.
//  9. Capture processStartNS, read kernel /proc starttime, write marker +
//     pidfile atomically.
//  10. Spawn the native wait goroutine which will eventually call
//      backoff.Observe and close waitDone.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// --- Early guards ---------------------------------------------------------
	if s.ImportReadOnly {
		return fmt.Errorf("%w: import read-only mode; lifecycle operations disabled", ErrNotStarted)
	}
	if s.cmd != nil && s.cmd.Process != nil && processExists(s.cmd.Process.Pid) {
		return fmt.Errorf("%w: pid %d is still alive", ErrAlreadyRunning, s.cmd.Process.Pid)
	}
	// If a previous wait goroutine is still cleaning up, wait for it to finish
	// outside the mutex so we can't deadlock (the goroutine acquires s.mu briefly
	// to clear s.waitGoroutine = false at its tail).  We release, wait,
	// then re-acquire and re-check the alive-condition above (if the operator beat us
	// and restarted under our feet).
	if s.waitGoroutine {
		s.mu.Unlock()
		s.waitWG.Wait()
		s.mu.Lock()
		// Re-check the primary alive guard now that we're back.
		if s.cmd != nil && s.cmd.Process != nil && processExists(s.cmd.Process.Pid) {
			return fmt.Errorf("%w: pid %d is still alive", ErrAlreadyRunning, s.cmd.Process.Pid)
		}
	}

	// --- Backoff --------------------------------------------------------------
	if allowed, remaining := s.backoff.MayStart(); !allowed {
		if remaining < 0 {
			return ErrCrashLoopExhausted
		}
		s.Log.Info("moxsupervisor: crash-loop backoff; sleeping before Start",
			"remaining", remaining.Round(time.Millisecond),
			"next_tier", s.backoff.NextDelay())
		// Release the mutex during the sleep so concurrent Status() / Adopt()
		// calls aren't blocked for seconds.
		s.mu.Unlock()
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.mu.Lock()
			return ctx.Err()
		case <-timer.C:
		}
		s.mu.Lock()
		// Re-check post-sleep (process could have been adopted in between).
		if s.cmd != nil && s.cmd.Process != nil && processExists(s.cmd.Process.Pid) {
			return fmt.Errorf("%w: pid %d appeared during backoff sleep", ErrAlreadyRunning, s.cmd.Process.Pid)
		}
	}

	// --- Preflight ------------------------------------------------------------
	pf := s.Preflight(ctx)
	if !pf.OK {
		first := "preflight failed"
		if len(pf.Issues) > 0 {
			first = pf.Issues[0]
		}
		// Categorise the most common errors so callers can translate to HTTP.
		switch {
		case !pf.Binary.Exists || !pf.Binary.Executable:
			return fmt.Errorf("%w: %s (%w)", ErrPreflightFailed, first, ErrBinaryMissing)
		case pf.Config.Ran && !pf.Config.OK:
			return fmt.Errorf("%w: %s (%w)", ErrPreflightFailed, first, ErrConfigTestFailed)
		default:
			return fmt.Errorf("%w: %s (%w)", ErrPreflightFailed, first, ErrPortConflict)
		}
	}

	// --- New boot_id ----------------------------------------------------------
	bootID, err := GenerateBootID()
	if err != nil {
		return fmt.Errorf("moxsupervisor: generate boot_id: %w", err)
	}
	s.bootID = bootID
	s.processStartNS = time.Now().UnixNano()

	// --- Open append logs -----------------------------------------------------
	stdoutF, err := openAppendLog(s.stdoutLogPath)
	if err != nil {
		return fmt.Errorf("moxsupervisor: open stdout log %s: %w", s.stdoutLogPath, err)
	}
	stderrF, err := openAppendLog(s.stderrLogPath)
	if err != nil {
		stdoutF.Close()
		return fmt.Errorf("moxsupervisor: open stderr log %s: %w", s.stderrLogPath, err)
	}

	// --- Build cmd ------------------------------------------------------------
	// Explicit argv – never shell.
	argv := []string{"mox"}
	if s.ConfigPath != "" {
		argv = append(argv, "-config", s.ConfigPath)
	}
	if s.DataDir != "" {
		argv = append(argv, "-data", s.DataDir)
	}
	argv = append(argv, "serve")

	cmd := &exec.Cmd{
		Path: s.BinaryPath,
		Args: argv,
		Env:  os.Environ(), // inherit, but never add shell helpers
		SysProcAttr: &syscall.SysProcAttr{
			Setpgid: true, // so tier 2/3 signals can reach the whole group
		},
		Stdout: stdoutF,
		Stderr: stderrF,
	}
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		stdoutF.Close()
		stderrF.Close()
		// Record the failed Start as a crash so repeated attempts don't
		// tight-loop on a broken binary.
		s.backoff.Observe(127, time.Since(startTime))
		return fmt.Errorf("moxsupervisor: exec mox serve: %w", err)
	}
	pid := cmd.Process.Pid
	s.cmd = cmd
	s.adopted = false
	s.waitDone = make(chan WaitResult, 1)
	s.waitGoroutine = true

	// --- Write marker + pidfile ----------------------------------------------
	startTicks, _, _ := readProcStartTime(pid)
	checksum := pf.Binary.ChecksumSHA256
	marker := Marker{
		Version:          markerVersion,
		PhantomInstance:  s.PhantomInstance,
		BootID:           s.bootID,
		PID:              pid,
		StartTimeNano:    s.processStartNS,
		ProcessStartTime: startTicks,
		BinaryPath:       s.BinaryPath,
		BinaryChecksum:   checksum,
		ConfigPath:       s.ConfigPath,
		DataDir:          s.DataDir,
		LaunchedAt:       startTime.UTC().Format(time.RFC3339Nano),
		LogStdout:        s.stdoutLogPath,
		LogStderr:        s.stderrLogPath,
	}
	if werr := writeMarker(s.markerPath, marker); werr != nil {
		s.Log.Warn("moxsupervisor: writing marker failed (process is still running)",
			"error", werr, "pid", pid)
		// Do NOT stop the process – the marker is advisory; if we crash
		// before the next Adopt() runs we lose orphan-adoption capability
		// but Mox itself is fine.
	}
	if werr := writePIDFile(s.pidPath, pid); werr != nil {
		s.Log.Warn("moxsupervisor: writing pidfile failed", "error", werr, "pid", pid)
	}

	// --- Spawn native wait goroutine -----------------------------------------
	// The wait goroutine runs WITHOUT s.mu held so it can deliver the exit
	// independently of concurrent Status()/Stop() calls.
	waitDone := s.waitDone
	s.waitWG.Add(1)
	go s.runNativeWaitGoroutine(cmd, startTime, stdoutF, stderrF, waitDone)

	s.Log.Info("moxsupervisor: mox started",
		"pid", pid,
		"boot_id", s.bootID,
		"binary", s.BinaryPath,
		"version", trimNewline(pf.Binary.Version))
	return nil
}

// runNativeWaitGoroutine is the fresh-start counterpart to runWaitGoroutine
// for adopted processes.  It uses cmd.Wait() directly (no polling) and feeds
// crash-loop backoff before closing waitDone.
func (s *Supervisor) runNativeWaitGoroutine(cmd *exec.Cmd, startTime time.Time, stdoutF, stderrF *os.File, waitDone chan<- WaitResult) {
	defer s.waitWG.Done()
	// Wait() blocks until the process exits.  We must run this on the
	// goroutine, not inside Start(), because Start() must return quickly.
	runErr := cmd.Wait()
	runDuration := time.Since(startTime)

	// Close the log files once the child is gone.  Start()'s deferred mutex
	// unlock doesn't protect us here – we're on our own goroutine.
	if stdoutF != nil {
		_ = stdoutF.Close()
	}
	if stderrF != nil {
		_ = stderrF.Close()
	}

	exitCode := 0
	fromSignal := false
	var sigUsed syscall.Signal
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			if ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus); ok {
				if ws.Signaled() {
					exitCode = int(ws.Signal())
					fromSignal = true
					sigUsed = ws.Signal()
				} else {
					exitCode = ws.ExitStatus()
				}
			} else {
				exitCode = ee.ProcessState.ExitCode()
			}
		} else {
			exitCode = 1
		}
	}

	// Update crash-loop backoff.  backoff is internally synchronised; we
	// don't need s.mu around it.
	s.backoff.Observe(exitCode, runDuration)

	s.mu.Lock()
	s.waitGoroutine = false
	boot := s.bootID
	wasAdopted := s.adopted
	markerPath := s.markerPath
	pidPath := s.pidPath
	s.mu.Unlock()

	s.Log.Info("moxsupervisor: mox exited",
		"pid", cmd.Process.Pid,
		"boot_id", boot,
		"adopted", wasAdopted,
		"exit_code", exitCode,
		"from_signal", fromSignal,
		"signal", fmt.Sprintf("%v", sigUsed),
		"run_duration", runDuration.Round(time.Millisecond))

	result := WaitResult{
		ExitCode:   exitCode,
		ExitErr:    runErr,
		ExitedAt:   time.Now(),
		FromSignal: fromSignal,
	}
	// marker + pidfile clean-up happens in Stop() for operator-initiated
	// stops; for natural exits we clean them up here so the next Start()
	// doesn't see a stale marker.
	cleanupMarkerAndPID(markerPath, pidPath)

	select {
	case waitDone <- result:
	default:
	}
	close(waitDone)
}

// Wait returns a channel that emits a single WaitResult when the process
// next exits, then closes.  If the process isn't running, it returns an
// already-closed channel with a pre-populated ErrNotStarted result.
func (s *Supervisor) Wait() <-chan WaitResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waitDone == nil {
		// Never-started: return a closed channel with ErrNotStarted baked in.
		ch := make(chan WaitResult, 1)
		ch <- WaitResult{ExitCode: -1, ExitErr: ErrNotStarted, ExitedAt: time.Now()}
		close(ch)
		return ch
	}
	return s.waitDone
}

// Stop performs the three-tier shutdown.  It is safe to call on a supervisor
// that was never started (returns ErrNotStarted).
//
// Concurrency note: Stop() deliberately releases the supervisor mutex for
// the ~45s three-tier escalation, so concurrent Status() / Preflight() calls
// never block.  Stop() is NOT safe to call concurrently with Start() or
// itself; the caller (mail.Service) serialises mutating calls).
func (s *Supervisor) Stop() (StopResult, error) {
	// --- Phase A: snapshot under the lock – copy every value we'll need so we
	// can release mu and let Status() breathe during the 45s tiers.
	s.mu.Lock()
	if s.ImportReadOnly {
		s.mu.Unlock()
		return StopResult{}, fmt.Errorf("%w: import read-only mode", ErrNotStarted)
	}
	if s.cmd == nil || s.cmd.Process == nil {
		s.mu.Unlock()
		return StopResult{}, ErrNotStarted
	}
	snapshotCmd := s.cmd
	pid := s.cmd.Process.Pid
	isAdopted := s.adopted
	boot := s.bootID
	// Cheap, visible flag: mark as "stopping" via processStartNS=0 so
	// Status() will not mistake a still-alive process as "running" while
	// we signal it.  (It will report it as StateStopping via processStartNS=0.
	s.processStartNS = 0
	s.mu.Unlock()

	if s.Log != nil {
		s.Log.Info("moxsupervisor: initiating stop",
			"pid", pid,
			"boot_id", boot,
			"adopted", isAdopted)
	}

	// --- Phase B: perform the three tiers OUTSIDE s.mu so Status() never
	// blocks on us.
	res, err := stopProcess(snapshotCmd, isAdopted, s.markerPath, s.pidPath, s.Log)
	if err != nil {
		return res, err
	}

	// --- Phase C: clean up state once the process is definitely gone.
	s.mu.Lock()
	defer s.mu.Unlock()
	// Guard: if a concurrent Start() somehow raced (shouldn't happen per the
	// non-thread-safe contract), only clear when it's still our cmd.
	if s.cmd == snapshotCmd {
		s.cmd = nil
		s.adopted = false
		s.bootID = ""
		// Operator-initiated stop = clean exit, not a crash.
		s.backoff.Reset()
	}
	return res, nil
}

// Restart is Stop + Start.  If Stop fails we surface the error and do not
// attempt Start (the caller should inspect Status() to decide whether to
// retry).
func (s *Supervisor) Restart(ctx context.Context) (StopResult, error) {
	sr, err := s.Stop()
	if err != nil && !errors.Is(err, ErrNotStarted) {
		return sr, fmt.Errorf("moxsupervisor: restart (stop phase): %w", err)
	}
	if err := s.Start(ctx); err != nil {
		return sr, fmt.Errorf("moxsupervisor: restart (start phase): %w", err)
	}
	return sr, nil
}

// Status returns a snapshot of the current running state.  It never blocks.
// The observed-state heuristic is:
//
//   - ImportReadOnly            → StateImportRO
//   - waitGoroutine running:
//       process still alive     → StateRunning (if adopted, StateAdopted)
//       or starting...
//   - cmd != nil but waitDone closed with error → StateFailed
//   - backoff tier > 0         → StateBackoff
//   - terminal backoff         → StateFailed
//   - otherwise                → StateStopped
func (s *Supervisor) Status() (ObservedState, int, string, CrashLoopState, int, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ImportReadOnly {
		return StateImportRO, 0, "", CLStable, 0, 0
	}
	clState, consec, _, until := s.backoff.State()
	nextDelay := s.backoff.NextDelay()

	var pid int
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}
	if s.cmd != nil && s.cmd.Process != nil && processExists(pid) {
		// Stop() sets processStartNS=0 before releasing mu to signal "an
		// operator stop is in progress".  This check must win over the
		// "running/adopted" states so the UI pill shows Stopping instead of
		// a misleading Running during the 45s three-tier escalation.
		if s.processStartNS == 0 {
			return StateStopping, pid, s.bootID, clState, consec, nextDelay
		}
		// Process is live.
		if s.adopted {
			return StateAdopted, pid, s.bootID, clState, consec, nextDelay
		}
		_ = until
		return StateRunning, pid, s.bootID, clState, consec, nextDelay
	}
	// Process is not live.  Figure out which non-running state we're in.
	if s.cmd == nil {
		// Stop() has already been called and cleaned up – regardless of
		// backoff state, the operator asked us to stop so we report Stopped.
		return StateStopped, 0, "", CLStable, 0, 0
	}
	if s.backoff.IsTerminal() {
		return StateFailed, pid, s.bootID, CLFailed, consec, nextDelay
	}
	// If we just started (<5s ago) and the goroutine hasn't had time to
	// observe a crash yet, report "starting" rather than jumping straight
	// to backoff.
	if s.cmd != nil && s.waitGoroutine && s.processStartNS > 0 {
		age := time.Since(time.Unix(0, s.processStartNS))
		if age < 5*time.Second {
			return StateStarting, pid, s.bootID, clState, consec, nextDelay
		}
	}
	if clState == CLBackoff {
		return StateBackoff, pid, s.bootID, CLBackoff, consec, nextDelay
	}
	return StateStopped, pid, s.bootID, CLStable, consec, nextDelay
}

// ResetCrashLoop clears the crash-loop FSM.  Called by the UI action
// "reset crash loop" after the operator has fixed the underlying condition.
func (s *Supervisor) ResetCrashLoop() {
	s.backoff.Reset()
}

// Close is called during Phantom orderly shutdown.  It stops Mox if it's
// running and logs any error; callers typically call it from a defer.
func (s *Supervisor) Close() error {
	if s == nil {
		return nil
	}
	if s.ImportReadOnly {
		// Never touch import-mode processes.
		return nil
	}
	// Use Stop() rather than reaching in directly – it performs the full
	// three-tier escalation and cleans up markers.
	_, err := s.Stop()
	if errors.Is(err, ErrNotStarted) {
		return nil
	}
	return err
}

// --- helpers --------------------------------------------------------------

// openAppendLog opens a log file for append writes with 0600 permissions.
func openAppendLog(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// writePIDFile writes pid as a newline-terminated ASCII decimal.  Used by
// shell wrappers / Prometheus exporters; the marker file is the canonical
// record.
func writePIDFile(path string, pid int) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "mox.pid.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			cleanup()
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.WriteString(strconv.Itoa(pid) + "\n"); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// channelClosed reports whether ch is closed (non-blocking).  Used for
// best-effort sanity checks; its result is inherently racy in concurrent
// code so callers never depend on it for correctness.
func channelClosed[T any](ch chan T) bool {
	select {
	case _, ok := <-ch:
		return !ok
	default:
		return false
	}
}

func trimNewline(s string) string {
	s = stringTrimRight(s, "\n")
	s = stringTrimRight(s, "\r")
	return s
}

func stringTrimRight(s, cutset string) string {
	// Tiny helper to avoid importing "strings" twice (types.go already has
	// it, but supervisor.go has its own import list and keeping it here
	// avoids fragile cross-file dependency order issues).
	for len(s) > 0 {
		for _, c := range cutset {
			if rune(s[len(s)-1]) == c {
				s = s[:len(s)-1]
				goto next
			}
		}
		break
	next:
	}
	return s
}
