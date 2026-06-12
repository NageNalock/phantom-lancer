// Package moxsupervisor owns the lifecycle of a single Mox sidecar process.
//
// Hard boundaries (see plan §"不可触碰硬边界"):
//
//   - This package NEVER imports internal/supervisor.  It is a fully
//     independent module so that Mox lifecycle semantics (pgid-based signal
//     escalation, 4-layer orphan adoption, crash-loop backoff with a stable
//     threshold, marker-file ownership) stay isolated from Phantom's generic
//     child-process supervisor.
//   - All command execution uses explicit argv slices; the shell is NEVER
//     invoked – no "/bin/sh -c", no cmd.Shell(), no user-supplied args are
//     ever appended verbatim.
//   - Orphan adoption NEVER kills an external process.  If ANY one of the
//     four adoption checks fails, the supervisor returns
//     ErrAdoptionRejected and surfaces a warning to the UI.  The operator
//     must kill any mis-attached process manually.
//
// The supervisor is NOT thread-safe for concurrent Start/Stop/Restart calls;
// callers (mail.Service) hold a mutex before invoking any mutating method.
package moxsupervisor

import (
	"errors"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// --- Public error sentinels ------------------------------------------------
//
// Callers match on these via errors.Is so they can translate into the right
// HTTP error code / audit event without fragile string comparisons.

var (
	// ErrNotStarted is returned by Stop/Restart/Wait when the supervisor has
	// never launched a process.
	ErrNotStarted = errors.New("moxsupervisor: process has not been started")

	// ErrAlreadyRunning is returned by Start when a process is already live
	// (either owned by this supervisor instance, or successfully adopted at
	// boot time).
	ErrAlreadyRunning = errors.New("moxsupervisor: process already running")

	// ErrAdoptionRejected is returned by Adopt when any of the four
	// validation layers failed.  The supervisor does NOT touch the remote
	// process on this path; callers must surface the reason in the UI.
	ErrAdoptionRejected = errors.New("moxsupervisor: orphan adoption rejected")

	// ErrPreflightFailed wraps binary / port / configtest failures.  The
	// specific reason is recorded in the error chain.
	ErrPreflightFailed = errors.New("moxsupervisor: preflight failed")

	// ErrConfigTestFailed indicates `mox config test` returned non-zero.
	ErrConfigTestFailed = errors.New("moxsupervisor: mox config test failed")

	// ErrBinaryMissing means the configured mox binary does not exist or is
	// not executable.
	ErrBinaryMissing = errors.New("moxsupervisor: mox binary missing or not executable")

	// ErrPortConflict means one of the required ports is already bound.
	ErrPortConflict = errors.New("moxsupervisor: required port already in use")

	// ErrCrashLoopExhausted is returned by Start when crash-loop protection
	// has reached the "failed" terminal state.  The operator must either
	// ResetCrashLoop or resolve the underlying condition.
	ErrCrashLoopExhausted = errors.New("moxsupervisor: crash loop backoff exhausted; manual intervention required")
)

// --- State strings (shared between Go and UI) ----------------------------

// ObservedState is the coarse runtime state exposed on the dashboard pill.
// These strings are stable – don't rename without migrating UI labels too.
type ObservedState string

const (
	StateUnknown   ObservedState = "unknown"
	StateStopped   ObservedState = "stopped"
	StateStarting  ObservedState = "starting"
	StateRunning   ObservedState = "running"
	StateStopping  ObservedState = "stopping"
	StateRestart   ObservedState = "restarting"
	StateFailed    ObservedState = "failed"
	StateBackoff   ObservedState = "backoff" // crashed, sleeping before retry
	StateAdopted   ObservedState = "adopted" // orphan adopted from marker
	StateImportRO  ObservedState = "import"  // import read-only mode – no lifecycle
)

// CrashLoopState tracks the backoff FSM (§5.1.4 of the design doc).
type CrashLoopState string

const (
	CLStable   CrashLoopState = "stable"
	CLBackoff  CrashLoopState = "backing_off"
	CLFailed   CrashLoopState = "failed"
)

// Ports mirrors the 7 listening ports Mox exposes.  Zero values mean "don't
// preflight this port" – useful for import-only deployments where Mox binds
// on a different host than Phantom expects.
type Ports struct {
	SMTP         int `json:"smtp"`
	Submission   int `json:"submission"`
	SMTPS        int `json:"smtps"`
	IMAP         int `json:"imap"`
	IMAPS        int `json:"imaps"`
	Webmail      int `json:"webmail"`
	WebAPILocal  int `json:"webapi_local"`
}

// --- Supervisor -----------------------------------------------------------

// Supervisor owns one Mox process.  Construct with New.
type Supervisor struct {
	// MoxRoot is the base directory for marker/pidfile/logs.  It is the
	// value returned by mail.Service.MoxRoot().
	MoxRoot string
	// BinaryPath is the absolute path to the `mox` executable.
	BinaryPath string
	// DataDir is passed as `mox --data <DataDir> serve`.
	DataDir string
	// ConfigPath is the `-config` flag (may be empty for defaults).
	ConfigPath string
	// Ports – zero values are skipped in preflight.
	Ports Ports
	// PhantomInstance is the stable ID written into every marker file so
	// an orphan from a *different* Phantom instance is never adopted.
	PhantomInstance string
	// ImportReadOnly – when true, Start/Stop/Restart all return
	// ErrNotStarted immediately (C3: import mode = Phantom does NOT touch
	// the live process).  Adopt still works for UI status display.
	ImportReadOnly bool
	// Logger – if nil, slog.Default() is used.
	Log *slog.Logger

	mu             sync.Mutex
	cmd            *exec.Cmd
	waitDone       chan WaitResult
	waitWG         sync.WaitGroup // Done when the wait goroutine exits
	markerPath     string
	pidPath        string
	stdoutLogPath  string
	stderrLogPath  string
	backoff        *backoffFSM
	bootID         string // generated fresh each Start
	waitGoroutine  bool   // true while the wait goroutine is live
	adopted        bool   // true if current process was adopted, not started by us
	processStartNS int64  // monotonic starttime_ns captured at Start/Adopt
}

// WaitResult is delivered on the channel returned by Wait().  A single exit
// produces exactly one message; after that the channel is closed.
type WaitResult struct {
	ExitCode   int
	ExitErr    error // nil on clean exit
	ExitedAt   time.Time
	FromSignal bool // true if killed by a signal rather than natural exit
}

// New constructs a Supervisor.  It does not touch the filesystem; call
// EnsurePaths before Start.
func New(moxRoot, binaryPath, dataDir, configPath string, ports Ports, phantomInstance string, lg *slog.Logger) *Supervisor {
	if lg == nil {
		lg = slog.Default()
	}
	lg = lg.With("module", "moxsupervisor")
	return &Supervisor{
		MoxRoot:         moxRoot,
		BinaryPath:      binaryPath,
		DataDir:         dataDir,
		ConfigPath:      configPath,
		Ports:           ports,
		PhantomInstance: phantomInstance,
		Log:             lg,
		markerPath:      filepath.Join(moxRoot, "run", "mox.marker.json"),
		pidPath:         filepath.Join(moxRoot, "run", "mox.pid"),
		stdoutLogPath:   filepath.Join(moxRoot, "logs", "mox.stdout.log"),
		stderrLogPath:   filepath.Join(moxRoot, "logs", "mox.stderr.log"),
		backoff:         newBackoffFSM(),
	}
}
