package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// handoffFileName is the filename the supervisor watches to detect a pending
// update+restart handshake. It lives in DataDir/run next to the supervisor's
// lock and PID files.
const handoffFileName = "update-handoff.json"

// handoffSchemaVersion is incremented whenever the on-disk format changes.
// Old supervisor binaries will refuse to handle a handoff with a version
// they don't understand so a rollback is never attempted with stale rules.
const handoffSchemaVersion = 1

// handoffFile mirrors the JSON schema the supervisor reads. It must stay in
// sync with supervisor's HandoffFile.
type handoffFile struct {
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

// runDir returns the DataDir/run directory where runtime files live.
func (s *Service) runDir() string {
	return filepath.Join(s.cfg.DataDir, "run")
}

// handoffPath returns the absolute path of the update-handoff.json file.
func (s *Service) handoffPath() string {
	return filepath.Join(s.runDir(), handoffFileName)
}

// writeHandoff writes the update-handoff.json atomically. It never blocks
// the caller on error — write failures are logged but the update continues.
// source is either "update" (forward update) or "rollback" (manual rollback).
// For source=="rollback", currentVersion and targetVersion are swapped since
// the newly installed binary is the "old" one from the job's perspective.
func (s *Service) writeHandoff(jobID, source, currentVersion, targetVersion, installPath, backupPath string) {
	runDir := s.runDir()
	if err := os.MkdirAll(runDir, 0o700); err != nil && s.log != nil {
		s.log.Warn("system update handoff run dir create failed", "job_id", jobID, "error", err.Error())
		return
	}
	if currentVersion == "" {
		currentVersion = s.cfg.Build.Version
	}
	if source == "rollback" {
		// On rollback the binary we are about to restart is the one marked
		// CurrentVersion on the original job — it's now effective "target".
		currentVersion, targetVersion = targetVersion, currentVersion
	}
	supervisorBinaryPath := filepath.Join(filepath.Dir(installPath), "phantom-supervisor")
	if _, err := os.Stat(supervisorBinaryPath); err != nil {
		// Supervisor binary isn't present (e.g. first deploy of the new
		// feature); leave the field empty instead of leaking a stale path.
		supervisorBinaryPath = ""
	}
	timeoutSec := int(s.cfg.RestartTimeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	payload := handoffFile{
		SchemaVersion:        handoffSchemaVersion,
		JobID:                jobID,
		Source:               source,
		CurrentVersion:       currentVersion,
		TargetVersion:        targetVersion,
		MainBinaryPath:       installPath,
		MainBackupPath:       backupPath,
		SupervisorBinaryPath: supervisorBinaryPath,
		RequestedAt:          time.Now().UTC().Format(time.RFC3339),
		RestartTimeoutSec:    timeoutSec,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		if s.log != nil {
			s.log.Warn("system update handoff marshal failed", "job_id", jobID, "error", err.Error())
		}
		return
	}
	tmpPath := filepath.Join(runDir, handoffFileName+".tmp")
	if err := os.RemoveAll(tmpPath); err != nil {
		// RemoveAll on a missing path is a no-op; any real error here is
		// rare and the write below will likely fail too — which we'll log.
	}
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		if s.log != nil {
			s.log.Warn("system update handoff tmp open failed", "job_id", jobID, "error", err.Error())
		}
		return
	}
	writeErr := error(nil)
	_, werr := file.Write(data)
	if werr != nil {
		writeErr = fmt.Errorf("write tmp handoff: %w", werr)
	} else if ferr := file.Sync(); ferr != nil {
		writeErr = fmt.Errorf("sync tmp handoff: %w", ferr)
	} else if cerr := file.Close(); cerr != nil {
		writeErr = fmt.Errorf("close tmp handoff: %w", cerr)
	} else if rerr := os.Rename(tmpPath, s.handoffPath()); rerr != nil {
		writeErr = fmt.Errorf("rename tmp handoff: %w", rerr)
	} else if derr := fsyncDir(runDir); derr != nil {
		writeErr = fmt.Errorf("fsync run dir: %w", derr)
	}
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		if s.log != nil {
			s.log.Warn("system update handoff write failed", "job_id", jobID, "error", writeErr.Error())
		}
	} else if s.log != nil {
		s.log.Info("system update handoff written", "job_id", jobID, "target_version", targetVersion, "source", source)
	}
}

// deleteHandoff removes the handoff file. Called from ConfirmBoot via a
// defer so the marker goes away once the freshly installed binary has
// actually booted.
func (s *Service) deleteHandoff() {
	path := s.handoffPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) && s.log != nil {
		s.log.Warn("system update handoff remove failed", "error", err.Error())
	} else if err == nil && s.log != nil {
		s.log.Info("system update handoff cleared")
	}
}
