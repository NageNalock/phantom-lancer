package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// SupervisorStatus represents the real-time liveness information of the
// outer phantom-supervisor process. All fields are optional (zero-value
// when unavailable) so callers can render a UI that degrades gracefully
// when the supervisor is not present.
type SupervisorStatus struct {
	// UnderSupervisor is true when the current process was launched by
	// phantom-supervisor (PL_UNDER_SUPERVISOR env var). When false the
	// remaining fields will typically also be zero.
	UnderSupervisor bool `json:"underSupervisor"`

	// Alive is true when we successfully performed a kill-0 check against
	// SupervisorPID AND the signal was accepted by that PID.
	Alive bool `json:"alive"`

	// PID is the supervisor PID discovered from PL_SUPERVISOR_PID or from
	// the fallback supervisor.pid file in the run directory. 0 when the
	// supervisor could not be located.
	PID int `json:"pid,omitempty"`

	// PIDSource describes how PID was resolved. One of "env", "pidfile",
	// or the empty string when unknown / UnderSupervisor is false.
	PIDSource string `json:"pidSource,omitempty"`

	// ChildPID is the currently running child (phantom-lancer) PID as
	// reported by the supervisor. 0 when the supervisor has not started
	// a child yet or we cannot read the child pidfile.
	ChildPID int `json:"childPID,omitempty"`

	// LastError contains a short description of any problem encountered
	// while resolving the status (kill-0 rejected, pidfile missing or
	// unparseable, …). Callers can surface this in a diagnostic tooltip.
	LastError string `json:"lastError,omitempty"`
}

// ResolveSupervisorStatus returns a freshly probed SupervisorStatus.
//
// Resolution order for the supervisor PID:
//  1. PL_SUPERVISOR_PID environment variable (written by start.sh/supervisor)
//  2. PL_SUPERVISOR_PID_FILE env-var path OR $DataDir/run/phantom-supervisor.pid
//
// Once a PID is found we use kill - 0 via Signal(0) to confirm the process
// actually exists and we have permission to signal it, which means the PID
// is both alive and our real ancestor. On darwin/linux Signal(0) is cheap
// and does not actually deliver any signal.
//
// The child PID is read from PL_CHILD_PID_FILE env-var path OR
// $DataDir/run/phantom-lancer.pid when present.
func ResolveSupervisorStatus(dataDir string) SupervisorStatus {
	status := SupervisorStatus{
		UnderSupervisor: os.Getenv("PL_UNDER_SUPERVISOR") == "1",
	}
	runDir := filepath.Join(dataDir, "run")
	supervisorPIDFile := os.Getenv("PL_SUPERVISOR_PID_FILE")
	if supervisorPIDFile == "" {
		supervisorPIDFile = filepath.Join(runDir, "phantom-supervisor.pid")
	}
	childPIDFile := os.Getenv("PL_CHILD_PID_FILE")
	if childPIDFile == "" {
		childPIDFile = filepath.Join(runDir, "phantom-lancer.pid")
	}

	// ---- resolve supervisor PID ----------------------------------------
	pid := 0
	source := ""
	if raw := strings.TrimSpace(os.Getenv("PL_SUPERVISOR_PID")); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p > 0 {
			pid = p
			source = "env"
		} else {
			status.LastError = "PL_SUPERVISOR_PID is not a valid integer"
		}
	}
	if pid <= 0 {
		if p, err := readIntFile(supervisorPIDFile); err == nil && p > 0 {
			pid = p
			source = "pidfile"
		} else if err != nil && status.LastError == "" && !errors.Is(err, os.ErrNotExist) {
			status.LastError = "supervisor pidfile: " + truncateError(err, 120)
		}
	}
	status.PID = pid
	status.PIDSource = source

	// ---- liveness probe via Signal(0) ----------------------------------
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err != nil {
			// On unix FindProcess never actually fails; guard anyway.
			status.Alive = false
			if status.LastError == "" {
				status.LastError = "FindProcess: " + truncateError(err, 120)
			}
		} else {
			if sigErr := proc.Signal(syscall.Signal(0)); sigErr == nil {
				status.Alive = true
			} else {
				status.Alive = false
				if status.LastError == "" {
					status.LastError = "signal 0: " + truncateError(sigErr, 120)
				}
			}
		}
	} else if status.UnderSupervisor {
		// The env says we are under a supervisor but we could not resolve
		// its PID; this is worth flagging to the UI.
		if status.LastError == "" {
			status.LastError = "supervisor PID could not be resolved"
		}
	}

	// ---- child PID (best-effort) ---------------------------------------
	if p, err := readIntFile(childPIDFile); err == nil && p > 0 {
		status.ChildPID = p
	}
	return status
}

// readIntFile reads a small ASCII integer file (PID files, counters, …).
// Returns 0, ErrNotExist when the file is missing. Strips surrounding
// whitespace/newlines before parsing.
func readIntFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, nil
	}
	value, perr := strconv.Atoi(text)
	if perr != nil {
		return 0, perr
	}
	return value, nil
}

// truncateError shortens error strings for UI/JSON display. It also strips
// timestamps and trailing newlines.
func truncateError(err error, max int) string {
	if err == nil {
		return ""
	}
	out := strings.TrimSpace(err.Error())
	if len(out) <= max {
		return out
	}
	return out[:max] + "…"
}
