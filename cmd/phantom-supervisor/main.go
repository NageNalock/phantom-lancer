// phantom-supervisor is a tiny, dedicated process supervisor for the
// phantom-lancer binary. It handles automatic restart with exponential
// backoff, a lock file to prevent double-starts, signal propagation, and
// a fast-fail watchdog that restores the previous binary after a failed
// self-update (Phase 2 handoff protocol).
//
// It is intentionally small and uses no internal/config package so it can
// be built and shipped independently of the main binary's runtime state.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/logging"
	"phantom-lancer/internal/supervisor"
)

// splitArgs separates flag arguments meant for the supervisor from the
// child command. Everything before `--` is supervisor flags, everything
// after `--` is the child binary + its arguments.
func splitArgs(args []string) (supArgs []string, childCmd []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func main() {
	supArgs, childCmd := splitArgs(os.Args[1:])

	// Handle --version (and -version) before any flag parsing so an unknown
	// flag error doesn't mask it when invoked without --.
	for _, arg := range supArgs {
		if arg == "--version" || arg == "-v" || arg == "-version" {
			fmt.Printf("phantom-supervisor %s (%s/%s) built %s\n",
				buildinfo.Current().Version,
				buildinfo.Current().OS,
				buildinfo.Current().Arch,
				buildinfo.Current().Date,
			)
			return
		}
	}

	fs := flag.NewFlagSet("phantom-supervisor", flag.ExitOnError)
	pidFile := fs.String("pid-file", "", "supervisor PID file path (default: $DATA_DIR/run/phantom-supervisor.pid)")
	childPIDFile := fs.String("child-pid-file", "", "child PID file path (default: $DATA_DIR/run/phantom-lancer.pid)")
	lockFile := fs.String("lock-file", "", "supervisor lock file path (default: $DATA_DIR/run/phantom-supervisor.lock)")
	logFile := fs.String("log-file", "", "supervisor JSONL log file (rotating)")
	handoffFile := fs.String("handoff-file", "", "update handoff JSON file path (default: $DATA_DIR/run/update-handoff.json)")
	restartMinDelay := fs.Duration("restart-min-delay", 1*time.Second, "minimum restart backoff delay")
	restartMaxDelay := fs.Duration("restart-max-delay", 30*time.Second, "maximum restart backoff delay")
	stableAfter := fs.Duration("stable-after", 60*time.Second, "uptime threshold that resets the backoff counter")
	stopTimeout := fs.Duration("stop-timeout", 10*time.Second, "time between SIGTERM and SIGKILL when stopping the child")

	if err := fs.Parse(supArgs); err != nil {
		// flag.ExitOnError handles this; safeguard.
		os.Exit(2)
	}

	if len(childCmd) == 0 {
		fmt.Fprintln(os.Stderr, "phantom-supervisor: child command required. Usage: phantom-supervisor [flags] -- <binary> [args...]")
		fmt.Fprintln(os.Stderr, "Use -help to list supported flags.")
		os.Exit(2)
	}

	// Resolve defaults when path fields are blank — if a data directory
	// hint is present via env (PL_DATA_DIR), use that as the anchor.
	dataDir := os.Getenv("PL_DATA_DIR")
	defaultFile := func(supplied, fallback string) string {
		if strings.TrimSpace(supplied) != "" {
			return supplied
		}
		if dataDir == "" {
			return fallback
		}
		return filepath.Join(dataDir, "run", fallback)
	}

	resolvePIDFile := defaultFile(*pidFile, "phantom-supervisor.pid")
	resolveChildPIDFile := defaultFile(*childPIDFile, "phantom-lancer.pid")
	resolveLockFile := defaultFile(*lockFile, "phantom-supervisor.lock")
	resolveHandoffFile := defaultFile(*handoffFile, "update-handoff.json")

	// Ensure parent directories exist for all output paths.
	mkdirFor := func(paths ...string) {
		for _, p := range paths {
			if strings.TrimSpace(p) == "" {
				continue
			}
			dir := filepath.Dir(p)
			if dir == "" || dir == "." {
				continue
			}
			_ = os.MkdirAll(dir, 0o700)
		}
	}
	mkdirFor(resolvePIDFile, resolveChildPIDFile, resolveLockFile, resolveHandoffFile, *logFile)

	// The supervisor gets its own rotating JSONL log with independent file
	// rotation keys to avoid two processes (supervisor + child) clobbering
	// the same log while rotating.
	logHandle, logErr := logging.NewLogger(logging.Config{
		Path:        *logFile,
		MaxSizeMB:   32,
		MaxFiles:    5,
		MaxAgeDays:  14,
		WriteStdout: strings.TrimSpace(*logFile) == "",
	})
	logger := (*slog.Logger)(nil)
	if logErr == nil {
		logger = logHandle.Logger
	} else {
		fmt.Fprintf(os.Stderr, "phantom-supervisor: could not open log file %q (%v); falling back to stdout\n", *logFile, logErr)
		fallback, ferr := logging.NewLogger(logging.Config{WriteStdout: true})
		if ferr == nil {
			logger = fallback.Logger
		}
	}

	cfg := supervisor.Config{
		ChildBinary:       childCmd[0],
		ChildArgs:         childCmd[1:],
		SupervisorPIDFile: resolvePIDFile,
		ChildPIDFile:      resolveChildPIDFile,
		LockFile:          resolveLockFile,
		LogFile:           *logFile,
		HandoffFile:       resolveHandoffFile,
		RestartMinDelay:   *restartMinDelay,
		RestartMaxDelay:   *restartMaxDelay,
		StableAfter:       *stableAfter,
		StopTimeout:       *stopTimeout,
	}
	svc, err := supervisor.New(cfg, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "phantom-supervisor: invalid config: %v\n", err)
		if logHandle != nil {
			_ = logHandle.Close()
		}
		os.Exit(1)
	}
	if err := svc.Bootstrap(); err != nil {
		fmt.Fprintf(os.Stderr, "phantom-supervisor: bootstrap failed: %v\n", err)
		if logHandle != nil {
			_ = logHandle.Close()
		}
		// Distinguish lock-held from other bootstrap errors.
		if errors.Is(err, supervisor.ErrLockHeld) {
			os.Exit(5)
		}
		os.Exit(1)
	}

	// Signal handling: SIGINT and SIGTERM trigger graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go func() {
		s := <-sigCh
		if logger != nil {
			logger.Info("supervisor signal received, shutting down", "signal", s.String())
		}
		cancelRun()
		// Drain any repeat signals silently; our Run is already on the way out.
	}()

	if runErr := svc.Run(runCtx); runErr != nil {
		fmt.Fprintf(os.Stderr, "phantom-supervisor: run error: %v\n", runErr)
		if logHandle != nil {
			_ = logHandle.Close()
		}
		os.Exit(1)
	}
	if logHandle != nil {
		_ = logHandle.Close()
	}
}
