package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config carries all tunables for a Supervisor. Paths are absolute.
type Config struct {
	// ChildBinary is the absolute path of the program to supervise.
	ChildBinary string
	// ChildArgs are extra argv values appended after ChildBinary.
	ChildArgs []string
	// ChildEnv is the base environment map (KEY=VALUE) layered on top of
	// os.Environ() before supervisor-specific env vars are injected.
	ChildEnv []string

	// SupervisorPIDFile is written with our own PID after acquiring the
	// lock. It is distinct from the child PID file so scripts can target
	// the supervisor and child separately.
	SupervisorPIDFile string
	ChildPIDFile      string

	// LockFile guards against running two supervisors that share the same
	// DataDir. Uses flock(2) on Unix.
	LockFile string

	// LogFile is where the supervisor writes its own JSONL rotating log.
	// It may be empty (logs then go to stdout only).
	LogFile string

	// HandoffFile is the location the self-update service writes. Empty
	// path disables the Phase 2 fast-fail / watchdog rollback behaviour.
	HandoffFile string

	// Restart backoff.
	RestartMinDelay time.Duration // default 1s
	RestartMaxDelay time.Duration // default 30s
	StableAfter     time.Duration // default 60s

	// StopTimeout is the window between SIGTERM and SIGKILL when the
	// supervisor is asked to stop a child.
	StopTimeout time.Duration
}

// applyDefaults fills in zero-value fields with safe defaults.
func (c *Config) applyDefaults() {
	if c.RestartMinDelay <= 0 {
		c.RestartMinDelay = 1 * time.Second
	}
	if c.RestartMaxDelay <= 0 {
		c.RestartMaxDelay = 30 * time.Second
	}
	if c.StableAfter <= 0 {
		c.StableAfter = 60 * time.Second
	}
	if c.StopTimeout <= 0 {
		c.StopTimeout = 10 * time.Second
	}
}

// Validate returns an error if required fields are missing or inconsistent.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.ChildBinary) == "" {
		return errors.New("supervisor config: ChildBinary is required")
	}
	if !filepath.IsAbs(c.ChildBinary) {
		// Accept relative paths but resolve them so downstream logging and
		// handoff records all see absolute paths consistently.
		if abs, err := filepath.Abs(c.ChildBinary); err == nil {
			c.ChildBinary = abs
		}
	}
	return nil
}

// Supervisor is the long-lived restart loop manager. Use New → Bootstrap →
// Run; Run does not return until ctx is cancelled or an unrecoverable error
// occurs.
type Supervisor struct {
	cfg Config
	log *slog.Logger

	mu           sync.Mutex
	stopping     bool
	childCmd     *exec.Cmd
	childStarted time.Time

	// lock is non-nil after a successful Bootstrap.
	lock *LockFile

	// Fast-fail / handoff bookkeeping (mutated only by Run goroutine).
	fastFailCount  int
	lastHandoffJob string
}

// ChildExit captures a single child termination event. Fields are derived
// from exec.Cmd.Wait() using unix WaitStatus helpers that work identically
// on darwin and linux.
type ChildExit struct {
	PID        int
	ExitCode   int // -1 if signalled without exit code
	Signal     int // 0 if exited normally
	SignalName string
	Uptime     time.Duration
	StartedAt  time.Time
	ExitedAt   time.Time
	// WaitErr is the raw error returned by Wait() (for diagnostics).
	WaitErr error
}

// New constructs a Supervisor from config, applying defaults and running
// validation. log may be nil; slog.Default is used in that case.
func New(cfg Config, log *slog.Logger) (*Supervisor, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Supervisor{cfg: cfg, log: log}, nil
}

// Bootstrap acquires the lock and writes the supervisor PID file. Must be
// called before Run. On partial failure, any resources already allocated
// are released before returning.
func (s *Supervisor) Bootstrap() error {
	if s.cfg.LockFile != "" {
		lock, err := AcquireLock(s.cfg.LockFile)
		if err != nil {
			return fmt.Errorf("acquire lock %s: %w", s.cfg.LockFile, err)
		}
		s.lock = lock
	}
	if s.cfg.SupervisorPIDFile != "" {
		if err := WritePIDFile(s.cfg.SupervisorPIDFile, os.Getpid()); err != nil {
			s.releaseLockBestEffort()
			return fmt.Errorf("write supervisor pid file: %w", err)
		}
	}
	s.info("supervisor started",
		"child_binary", s.cfg.ChildBinary,
		"child_args", strings.Join(s.cfg.ChildArgs, " "),
		"supervisor_pid", os.Getpid(),
	)
	return nil
}

// releaseLockBestEffort is used during bootstrap failures.
func (s *Supervisor) releaseLockBestEffort() {
	if s.lock != nil {
		_ = s.lock.Release()
		s.lock = nil
	}
}

// info / warn are thin wrappers so callsites read naturally and stay
// grep-friendly. Missing logger is tolerated (noop) to keep tests simple.
func (s *Supervisor) info(msg string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Info(msg, args...)
}

func (s *Supervisor) warn(msg string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Warn(msg, args...)
}

func (s *Supervisor) error(msg string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Error(msg, args...)
}

// startChild spawns a new child and records it under the mutex. It writes
// the child PID file after a successful Start.
func (s *Supervisor) startChild() (*exec.Cmd, error) {
	cmd := exec.Command(s.cfg.ChildBinary, s.cfg.ChildArgs...)
	// Let the child manage its own output via its dedicated JSONL logger;
	// supervisor only cares about lifecycle events.
	cmd.Stdout = nil
	cmd.Stderr = nil

	env := os.Environ()
	if len(s.cfg.ChildEnv) > 0 {
		env = append(env, s.cfg.ChildEnv...)
	}
	env = append(env,
		"PL_UNDER_SUPERVISOR=1",
		fmt.Sprintf("PL_SUPERVISOR_PID=%d", os.Getpid()),
	)
	if s.cfg.LogFile != "" {
		env = append(env, "PL_SUPERVISOR_LOG_FILE="+s.cfg.LogFile)
	}
	if s.cfg.HandoffFile != "" {
		env = append(env, "PL_SUPERVISOR_HANDOFF_FILE="+s.cfg.HandoffFile)
	}
	cmd.Env = env
	// Start in its own process group so Ctrl-C on the supervisor (if any)
	// does not kill the child directly — signals go through the supervisor.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start child: %w", err)
	}

	started := time.Now()
	s.mu.Lock()
	s.childCmd = cmd
	s.childStarted = started
	s.mu.Unlock()

	if s.cfg.ChildPIDFile != "" {
		if perr := WritePIDFile(s.cfg.ChildPIDFile, cmd.Process.Pid); perr != nil {
			s.warn("supervisor child pid file write failed", "error", perr.Error())
		}
	}
	s.info("supervisor child started",
		"pid", cmd.Process.Pid,
		"binary", s.cfg.ChildBinary,
		"started_at", started.UTC().Format(time.RFC3339Nano),
	)
	return cmd, nil
}

// waitChild blocks until the command finishes and returns a populated
// ChildExit. It also removes the child PID file (best effort).
func (s *Supervisor) waitChild(cmd *exec.Cmd, startedAt time.Time) ChildExit {
	err := cmd.Wait()
	exitedAt := time.Now()
	ce := ChildExit{
		PID:       cmd.Process.Pid,
		Uptime:    exitedAt.Sub(startedAt),
		StartedAt: startedAt,
		ExitedAt:  exitedAt,
		WaitErr:   err,
	}
	if err == nil {
		ce.ExitCode = 0
	} else if exitErr := (&exec.ExitError{}); errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			switch {
			case ws.Exited():
				ce.ExitCode = ws.ExitStatus()
			case ws.Signaled():
				ce.Signal = int(ws.Signal())
				ce.SignalName = syscall.Signal(ws.Signal()).String()
				ce.ExitCode = -1
			default:
				ce.ExitCode = ws.ExitStatus()
			}
		} else {
			ce.ExitCode = exitErr.ExitCode()
		}
	} else {
		// Wait returned a non-ExitError, non-nil error: treat as abnormal
		// termination with a synthetic -128 code so log inspection is obvious.
		ce.ExitCode = -128
	}

	// Clear the active child record so stopChild becomes a no-op.
	s.mu.Lock()
	if s.childCmd == cmd {
		s.childCmd = nil
	}
	s.mu.Unlock()

	if s.cfg.ChildPIDFile != "" {
		_ = RemovePIDFile(s.cfg.ChildPIDFile)
	}
	return ce
}

// stopChild sends SIGTERM, waits up to StopTimeout, then escalates to
// SIGKILL. Safe to call even when there is no active child.
func (s *Supervisor) stopChild() {
	s.mu.Lock()
	cmd := s.childCmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	s.info("supervisor stopping child", "pid", pid, "timeout_ms", s.cfg.StopTimeout.Milliseconds())
	// Send SIGTERM to the process group (pgid == pid because of Setpgid=true).
	if terr := syscall.Kill(-pid, syscall.SIGTERM); terr != nil && !errors.Is(terr, os.ErrProcessDone) {
		s.warn("supervisor sigterm failed", "pid", pid, "error", terr.Error())
	}
	deadline := time.After(s.cfg.StopTimeout)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		for {
			select {
			case <-tick.C:
				if perr := syscall.Kill(pid, 0); perr != nil {
					return
				}
			case <-deadline:
				return
			}
		}
	}()
	<-pollDone
	// After timeout we escalate; if the process still exists, SIGKILL it.
	if perr := syscall.Kill(pid, 0); perr == nil {
		s.warn("supervisor child did not stop within timeout — sending sigkill", "pid", pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// Run is the main loop. It only returns when ctx is cancelled (nil error),
// when startChild repeatedly can't even exec the binary (error), or when an
// unrecoverable IO error occurs (e.g. broken PID file dir).
func (s *Supervisor) Run(ctx context.Context) error {
	defer s.cleanupFinal()

	backoff := &BackoffTracker{
		MinDelay:    s.cfg.RestartMinDelay,
		MaxDelay:    s.cfg.RestartMaxDelay,
		StableAfter: s.cfg.StableAfter,
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		cmd, startErr := s.startChild()
		if startErr != nil {
			// If we can't exec the binary, record the attempt but don't
			// spin wildly. If this keeps failing indefinitely we'd still
			// saturate the backoff table and sleep up to MaxDelay each
			// tick — bounded churn, no hot loop.
			s.warn("supervisor child start failed",
				"binary", s.cfg.ChildBinary,
				"error", startErr.Error(),
				"attempt", backoff.CurrentAttempts()+1,
			)
			backoff.ObserveUptime(0) // start-failure counts as "no uptime"
			delay := backoff.NextDelay()
			if !s.sleepOrStop(ctx, delay) {
				return nil
			}
			continue
		}

		startedAt := s.childStarted // snapshot after startChild set it
		waitDone := make(chan ChildExit, 1)
		go func() { waitDone <- s.waitChild(cmd, startedAt) }()

		select {
		case <-ctx.Done():
			// Cancel path: stop child → consume wait → return.
			s.mu.Lock()
			s.stopping = true
			s.mu.Unlock()
			s.stopChild()
			// Wait for the goroutine to finish so we never leak it.
			<-waitDone
			return nil
		case ce := <-waitDone:
			s.logChildExit(ce)
			s.mu.Lock()
			stopping := s.stopping
			s.mu.Unlock()
			if stopping {
				return nil
			}
			// Phase 2 fast-fail rollback hook. Even if the handoff file is
			// absent the call is cheap (two stat + return).
			rolledBack, herr := s.handleHandoffAfterExit(ce)
			if herr != nil {
				s.warn("supervisor handoff handler errored", "error", herr.Error())
			}
			if rolledBack {
				// Restored binary — give the process a clean backoff slate.
				backoff = &BackoffTracker{
					MinDelay:    s.cfg.RestartMinDelay,
					MaxDelay:    s.cfg.RestartMaxDelay,
					StableAfter: s.cfg.StableAfter,
				}
				s.info("supervisor rollback complete; backoff reset")
			} else {
				backoff.ObserveUptime(ce.Uptime)
			}
			delay := backoff.NextDelay()
			s.info("supervisor restart scheduled",
				"delay_ms", delay.Milliseconds(),
				"attempts", backoff.CurrentAttempts(),
			)
			if !s.sleepOrStop(ctx, delay) {
				return nil
			}
		}
	}
}

// logChildExit writes a single structured log line for each child exit.
func (s *Supervisor) logChildExit(ce ChildExit) {
	if ce.Signal != 0 {
		s.info("supervisor child exited (signalled)",
			"pid", ce.PID,
			"signal_num", ce.Signal,
			"signal", ce.SignalName,
			"uptime_ms", ce.Uptime.Milliseconds(),
		)
	} else {
		s.info("supervisor child exited",
			"pid", ce.PID,
			"exit_code", ce.ExitCode,
			"uptime_ms", ce.Uptime.Milliseconds(),
		)
	}
}

// sleepOrStop blocks for delay or until ctx is cancelled. Returns true if
// the full delay elapsed (caller should continue restarting), false if ctx
// was cancelled first.
func (s *Supervisor) sleepOrStop(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		// Still respect ctx cancellation.
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// cleanupFinal releases resources we acquired during Bootstrap / Run. It is
// called from a defer in Run and also exposed as a public no-op via close.
// Returns the first error (PID file removal) but continues cleaning the rest.
func (s *Supervisor) cleanupFinal() error {
	var firstErr error
	if s.cfg.ChildPIDFile != "" {
		if err := RemovePIDFile(s.cfg.ChildPIDFile); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.cfg.SupervisorPIDFile != "" {
		if err := RemovePIDFile(s.cfg.SupervisorPIDFile); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.lock != nil {
		if err := s.lock.Release(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.lock = nil
	}
	s.info("supervisor cleanup finished")
	return firstErr
}
