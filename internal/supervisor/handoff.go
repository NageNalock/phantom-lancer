package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"phantom-lancer/internal/selfupdate"
)

// Handoff constants match the values written by the main self-update service
// (internal/selfupdate/handoff.go) and stay in sync via shared constants.
const (
	// HandoffSchemaVersion is bumped whenever the on-disk JSON structure
	// changes in a way that would mislead an older supervisor.
	HandoffSchemaVersion = 1
	// HandoffFastFailThreshold is the number of consecutive short-lived
	// child exits within HandoffFastFailMaxUptime that trigger a rollback.
	HandoffFastFailThreshold = 3
	// HandoffFastFailMaxUptime is the longest uptime that still counts as a
	// "fast fail". A child running any longer than this reset the counter.
	HandoffFastFailMaxUptime = 10 * time.Second
)

// HandoffFile is the on-disk handshake payload used by the supervisor to
// detect that an update was just applied and to auto-restore the backup
// binary if the freshly installed version never finishes booting.
type HandoffFile struct {
	SchemaVersion        int    `json:"schemaVersion"`
	JobID                string `json:"jobId"`
	Source               string `json:"source"`
	CurrentVersion       string `json:"currentVersion"`
	TargetVersion        string `json:"targetVersion"`
	MainBinaryPath       string `json:"mainBinaryPath"`
	MainBackupPath       string `json:"mainBackupPath"`
	SupervisorBinaryPath string `json:"supervisorBinaryPath,omitempty"`
	SupervisorBackupPath string `json:"supervisorBackupPath,omitempty"`
	RequestedAt          string `json:"requestedAt"`
	RestartTimeoutSec    int    `json:"restartTimeoutSeconds"`
}

// ReadHandoff parses the handoff file at path. Returns (nil, nil) when the
// file does not exist. Returns an error for any parse/version problem.
func ReadHandoff(path string) (*HandoffFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read handoff file: %w", err)
	}
	h := &HandoffFile{}
	if err := json.Unmarshal(data, h); err != nil {
		return nil, fmt.Errorf("parse handoff file: %w", err)
	}
	if h.SchemaVersion < HandoffSchemaVersion {
		return nil, fmt.Errorf("handoff schema version %d is not supported (want >=%d)", h.SchemaVersion, HandoffSchemaVersion)
	}
	if h.SchemaVersion > HandoffSchemaVersion {
		// Future version: we don't understand newer schemas. Treat it as
		// absent so we don't make bad rollback decisions.
		return nil, nil
	}
	return h, nil
}

// parsedRequestedAt is memoized on the struct after the first call so the
// same timestamp parsing runs at most once per read.
func (h *HandoffFile) parsedRequestedAt() (time.Time, error) {
	if h == nil {
		return time.Time{}, fmt.Errorf("nil handoff")
	}
	return time.Parse(time.RFC3339, h.RequestedAt)
}

// timeout returns the restart watchdog duration parsed from the handoff.
// Falls back to 120s if the field is missing or zero (old schema).
func (h *HandoffFile) timeout() time.Duration {
	if h == nil || h.RestartTimeoutSec <= 0 {
		return 120 * time.Second
	}
	return time.Duration(h.RestartTimeoutSec) * time.Second
}

// IsExpired returns true if now is beyond the handoff's watchdog window.
// A parse failure on RequestedAt also returns true (safe default: treat
// corrupt timestamps as expired so we don't get stuck in a fast-fail loop).
func (h *HandoffFile) IsExpired(now time.Time) bool {
	if h == nil {
		return false
	}
	started, err := h.parsedRequestedAt()
	if err != nil {
		return true
	}
	return now.Sub(started) >= h.timeout()
}

// Remaining returns the time left before the watchdog fires. Non-positive
// values mean the window has elapsed (caller should also check IsExpired).
func (h *HandoffFile) Remaining(now time.Time) time.Duration {
	if h == nil {
		return 0
	}
	started, err := h.parsedRequestedAt()
	if err != nil {
		return 0
	}
	return h.timeout() - now.Sub(started)
}

// handleHandoffAfterExit is the Phase 2 "fast-fail + watchdog rollback"
// hook. It is called by Run() every time the child exits naturally (i.e.
// not because of our own shutdown signal).
//
// The returned bool tells the caller whether a rollback was applied — in
// that case the Run loop resets its backoff tracker to give the restored
// binary a clean slate.
func (s *Supervisor) handleHandoffAfterExit(ce ChildExit) (rolledBack bool, err error) {
	h, err := ReadHandoff(s.cfg.HandoffFile)
	if err != nil {
		s.warn("supervisor handoff parse failed", "error", err.Error())
		// Malformed handoff should not suppress restarts; proceed without
		// any fast-fail bookkeeping.
		s.fastFailCount = 0
		s.lastHandoffJob = ""
		return false, nil
	}
	if h == nil {
		// No handoff in flight — reset bookkeeping.
		s.fastFailCount = 0
		s.lastHandoffJob = ""
		return false, nil
	}

	// Different handoff job means we've moved on to a new update.
	if h.JobID != s.lastHandoffJob {
		s.lastHandoffJob = h.JobID
		s.fastFailCount = 0
	}

	if ce.Uptime < HandoffFastFailMaxUptime {
		s.fastFailCount++
		s.info("supervisor handoff fast fail counted",
			"job_id", h.JobID,
			"uptime_ms", ce.Uptime.Milliseconds(),
			"attempt", s.fastFailCount,
		)
	} else {
		s.fastFailCount = 0
	}

	now := time.Now()
	fastFailHit := s.fastFailCount >= HandoffFastFailThreshold
	timeoutHit := h.IsExpired(now)

	if !fastFailHit && !timeoutHit {
		return false, nil
	}

	// Threshold crossed. Validate rollback inputs before touching disk.
	if h.MainBackupPath == "" || h.MainBinaryPath == "" {
		s.warn("supervisor handoff rollback missing paths",
			"job_id", h.JobID,
			"backup_path", h.MainBackupPath,
			"install_path", h.MainBinaryPath,
		)
		// Don't keep banging on a broken handoff; let the counter grow but
		// don't block the normal backoff restart cycle.
		return false, nil
	}

	reason := "fast_fail"
	if timeoutHit {
		reason = "timeout"
	}
	s.info("supervisor rollback threshold reached",
		"job_id", h.JobID,
		"reason", reason,
		"attempts", s.fastFailCount,
		"remaining_ms", h.Remaining(now).Milliseconds(),
	)
	if rerr := selfupdate.RestoreBackup(h.MainBinaryPath, h.MainBackupPath); rerr != nil {
		s.warn("supervisor rollback restore failed", "job_id", h.JobID, "error", rerr.Error())
		return false, fmt.Errorf("restore backup: %w", rerr)
	}
	s.info("supervisor rollback restored backup",
		"job_id", h.JobID,
		"install_path", h.MainBinaryPath,
		"backup_path", h.MainBackupPath,
	)
	// Best-effort: also restore the supervisor binary itself if the handoff
	// carries a non-empty supervisor backup. Both binaries ship together,
	// so leaving a newer supervisor running with an older main binary can
	// expose version-skew protocol bugs. Failure is logged only — the main
	// binary rollback already took us back to safety.
	if strings.TrimSpace(h.SupervisorBinaryPath) != "" && strings.TrimSpace(h.SupervisorBackupPath) != "" {
		if info, statErr := os.Stat(h.SupervisorBackupPath); statErr == nil && info.Mode().IsRegular() {
			if sErr := selfupdate.RestoreBackup(h.SupervisorBinaryPath, h.SupervisorBackupPath); sErr != nil {
				s.warn("supervisor rollback supervisor-binary restore failed",
					"job_id", h.JobID,
					"install_path", h.SupervisorBinaryPath,
					"backup_path", h.SupervisorBackupPath,
					"error", sErr.Error(),
				)
			} else if s.log != nil {
				s.info("supervisor rollback restored supervisor backup",
					"job_id", h.JobID,
					"install_path", h.SupervisorBinaryPath,
					"backup_path", h.SupervisorBackupPath,
				)
			}
		}
	}
	// Intentionally do NOT delete the handoff file: the restored old
	// binary's ConfirmBoot() is the authority on the job's final state.
	s.fastFailCount = 0
	return true, nil
}
