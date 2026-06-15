package probes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// L1Marker mirrors the subset of moxsupervisor.Marker fields we need for
// L1 Process probe.  We deliberately keep a local copy rather than import
// moxsupervisor (which would pull the supervisor's *exec.Cmd machinery into
// the probes package, unnecessarily increasing coupling).
//
// Field names match the marker schema exactly so JSON unmarshal succeeds.
type L1Marker struct {
	Version          int    `json:"version"`
	PhantomInstance  string `json:"phantom_instance"`
	BootID           string `json:"boot_id"`
	PID              int    `json:"pid"`
	ProcessStartTime uint64 `json:"process_starttime"` // kernel clock ticks
	BinaryPath       string `json:"binary_path"`
	BinaryChecksum   string `json:"binary_checksum"`
	LaunchedAt       string `json:"launched_at"`
}

// L1Config holds the inputs required by L1ProcessProbe.  Empty fields make
// the probe return StateUnknown (callers should fill them after a
// successful Supervisor.Start).
type L1Config struct {
	// MarkerPath is the absolute path of moxsupervisor's marker file.
	MarkerPath string
	// ExpectedBinaryPath, if non-empty, is compared to the marker's
	// binary_path – a mismatch → StateYellow (operator may have swapped
	// binaries out from under us).
	ExpectedBinaryPath string
	// ExpectedInstance, if non-empty, is compared to the marker's
	// phantom_instance field.
	ExpectedInstance string
}

// L1ProcessProbe implements the L1 "process alive + no PID-wrap" probe.
//
// It does 3 checks in order, any failure degrades the result:
//
//  1. Marker file exists + parses → we know the claimed PID + boot_id.
//     If not → StateUnknown (Mox has never been started, or marker was
//     cleared by Stop/Uninstall).
//  2. kill(pid, 0) succeeds (signal 0 = "does pid exist?").  If not →
//     StateRed (marker is stale, process dead).
//  3. On Linux: /proc/<pid>/stat starttime field matches marker's value.
//     If mismatch → StateRed (PID wrapped; a completely different process
//     has the PID claimed by the stale marker).
//
// Additionally:
//  - ExpectedBinaryPath mismatch vs marker.binary_path → StateYellow.
//  - ExpectedInstance mismatch vs marker.phantom_instance → StateYellow
//    (another Phantom instance wrote the marker; operator error).
type L1ProcessProbe struct {
	cfg L1Config
}

// NewL1Process constructs a new L1ProcessProbe.
func NewL1Process(cfg L1Config) *L1ProcessProbe {
	return &L1ProcessProbe{cfg: cfg}
}

// Name implements Probe.
func (p *L1ProcessProbe) Name() string { return "l1_process" }

// Layer implements Probe.
func (p *L1ProcessProbe) Layer() int { return 1 }

// Run implements Probe.
func (p *L1ProcessProbe) Run(ctx context.Context) (r Result) {
	r = Result{Name: p.Name(), Layer: p.Layer(), State: StateUnknown, StartedAt: time.Now()}
	defer func() { r.Duration = time.Since(r.StartedAt) }()

	if p.cfg.MarkerPath == "" {
		r.Message = "marker path not configured; mox has never been started"
		return r
	}

	// Read marker.
	m, err := readL1Marker(p.cfg.MarkerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.Message = "marker file missing; mox is stopped"
			return r
		}
		r.State = StateRed
		r.Message = fmt.Sprintf("unreadable marker file: %v", err)
		r.Err = err
		return r
	}

	if m.Version < 1 {
		r.State = StateRed
		r.Message = fmt.Sprintf("marker schema version %d is too old", m.Version)
		return r
	}
	if m.PID <= 0 {
		r.State = StateRed
		r.Message = fmt.Sprintf("marker reports invalid pid=%d", m.PID)
		return r
	}

	// 2. Process alive via signal(0).
	proc, ferr := os.FindProcess(m.PID)
	if ferr != nil {
		r.State = StateRed
		r.Message = fmt.Sprintf("FindProcess(pid=%d): %v", m.PID, ferr)
		r.Err = ferr
		return r
	}
	if kerr := proc.Signal(syscall.Signal(0)); kerr != nil {
		// EPERM means another user's process exists at this PID → PID wrap
		// without /proc (rare but possible on non-Linux).
		if errors.Is(kerr, syscall.ESRCH) {
			r.State = StateRed
			r.Message = fmt.Sprintf("pid=%d from marker no longer exists (ESRCH)", m.PID)
			return r
		}
		if errors.Is(kerr, syscall.EPERM) {
			r.State = StateYellow
			r.Message = fmt.Sprintf("pid=%d exists but EPERM (different UID; PID wrap possible)", m.PID)
			return r
		}
		r.State = StateRed
		r.Message = fmt.Sprintf("signal(0) pid=%d: %v", m.PID, kerr)
		r.Err = kerr
		return r
	}

	// 3. starttime check (Linux only).
	if runtime.GOOS == "linux" && m.ProcessStartTime > 0 {
		startTicks, src, ok := readProcStartTimeTicks(m.PID)
		if !ok {
			// Non-fatal: couldn't read /proc.  Downgrade to Yellow so the
			// operator can see the guard is weakened.
			r.State = StateYellow
			r.Message = fmt.Sprintf("pid=%d alive but /proc starttime unavailable (%s); PID-wrap guard disabled", m.PID, src)
			return r
		}
		if startTicks != m.ProcessStartTime {
			r.State = StateRed
			r.Message = fmt.Sprintf("pid=%d /proc starttime=%d != marker starttime=%d — PID WRAP DETECTED (%s)",
				m.PID, startTicks, m.ProcessStartTime, src)
			return r
		}
	}

	// --- Optional cross-checks (degrade to Yellow, never Red) ---------------
	warnings := []string{}
	if p.cfg.ExpectedBinaryPath != "" && !pathsEqualish(p.cfg.ExpectedBinaryPath, m.BinaryPath) {
		warnings = append(warnings,
			fmt.Sprintf("marker.binary_path=%q differs from configured %q", m.BinaryPath, p.cfg.ExpectedBinaryPath))
	}
	if p.cfg.ExpectedInstance != "" && m.PhantomInstance != "" && m.PhantomInstance != p.cfg.ExpectedInstance {
		warnings = append(warnings,
			fmt.Sprintf("marker.instance=%q differs from configured %q", m.PhantomInstance, p.cfg.ExpectedInstance))
	}

	if len(warnings) > 0 {
		r.State = StateYellow
		r.Message = strings.Join(warnings, "; ") + " (process is alive)"
		return r
	}

	r.State = StateGreen
	r.Message = fmt.Sprintf("pid=%d alive; marker boot_id=%s", m.PID, truncate(m.BootID, 12))
	return r
}

// --- helper functions -------------------------------------------------------

func readL1Marker(path string) (*L1Marker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &L1Marker{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parse marker JSON: %w", err)
	}
	return m, nil
}

// readProcStartTimeTicks reads the `starttime` field (field 20,
// 1-indexed per proc(5)) from /proc/<pid>/stat.  Returns (starttime-ticks,
// source, ok).  On Linux field 20 is after the closing `)` of the comm
// field (which can contain spaces / parens); we split FROM THE RIGHT as
// defence-in-depth.
func readProcStartTimeTicks(pid int) (uint64, string, bool) {
	if runtime.GOOS != "linux" {
		return 0, "non-linux", false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Sprintf("read /proc/%d/stat: %v", pid, err), false
	}
	// Find the LAST ')' and start tokenising from there – comm is bounded
	// by (…) and may contain embedded parens, so the LAST ')' is reliable.
	idx := strings.LastIndexByte(string(data), ')')
	if idx < 0 {
		return 0, "malformed /proc/<pid>/stat: no closing paren", false
	}
	tail := strings.TrimLeft(string(data[idx+1:]), " ")
	fields := strings.Fields(tail)
	// After "comm", the remaining fields in order are:
	//   state ppid pgrp session tty_nr tpgid flags minflt cminflt majflt
	//   cmajflt utime stime cutime cstime priority nice num_threads
	//   itrealvalue starttime vsize rss ...
	// starttime is the 20th field after state (i.e. fields[19]).
	// The tail tokens begin at "state" so fields[0]=state, fields[19]=starttime.
	if len(fields) < 20 {
		return 0, fmt.Sprintf("too few tail tokens (%d)", len(fields)), false
	}
	v, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Sprintf("parse starttime %q: %v", fields[19], err), false
	}
	return v, "linux /proc/<pid>/stat field[19]", true
}

func pathsEqualish(a, b string) bool {
	if a == b {
		return true
	}
	aa, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	return aa == bb
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
