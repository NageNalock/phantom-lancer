package selfupdate

import (
	"net/http"
	"time"

	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/storage"
)

const (
	jobStatusQueued     = "queued"
	jobStatusRunning    = "running"
	jobStatusRestarting = "restarting"
	jobStatusCompleted  = "completed"
	jobStatusFailed     = "failed"
	jobStatusCancelled  = "cancelled"

	phaseCreated     = "created"
	phaseDownloading = "downloading"
	phaseVerifying   = "verifying"
	phaseExtracting  = "extracting"
	phaseInstalling  = "installing"
	phaseRestarting  = "restarting"
	phaseCompleted   = "completed"
)

// RestartMode enumerates how the service transitions into a freshly
// installed binary.
const (
	RestartModeExit     = "exit"      // exit cleanly and rely on an external supervisor to re-launch
	RestartModeNone     = "none"      // leave the old process running; operator restarts manually
	RestartModeSelfExec = "self-exec" // in-place syscall.Exec — works without any supervisor
)

const (
	defaultAPIBaseURL       = "https://api.github.com"
	maxReleaseResponseBytes = 1 << 20
	maxChecksumBytes        = 4 << 10
	maxArchiveBytes         = 512 << 20
)

type Config struct {
	Enabled                bool
	Repository             string
	AssetName              string
	RestartMode            string
	InstallBinaryPath      string
	DataDir                string
	BackupRetention        int
	DownloadTimeout        time.Duration
	RestartTimeout         time.Duration
	APIBaseURL             string
	AllowInsecureDownloads bool
	HTTPClient             *http.Client
	Build                  buildinfo.Info
	RequestRestart         func()
	// PrepareSelfExec is invoked once a fresh binary has been atomically
	// installed and RestartMode is RestartModeSelfExec. The callee typically
	// stashes the absolute path so main() can hand it to syscall.Exec after
	// orderly resource shutdown.
	PrepareSelfExec func(newExecPath string)
}

type Status struct {
	Enabled               bool                       `json:"enabled"`
	Version               buildinfo.Info             `json:"version"`
	LatestCheck           *storage.SystemUpdateCheck `json:"latestCheck,omitempty"`
	ActiveJob             *storage.SystemUpdateJob   `json:"activeJob,omitempty"`
	LatestJob             *storage.SystemUpdateJob   `json:"latestJob,omitempty"`
	RestartTimeoutSeconds int                        `json:"restartTimeoutSeconds"`
	SupportedPlatform     bool                       `json:"supportedPlatform"`
	RestartMode           string                     `json:"restartMode"`
	InstallBinaryPath     string                     `json:"installBinaryPath,omitempty"`
	BackupBinaryPath      string                     `json:"backupBinaryPath,omitempty"`

	// Supervisor contains the real-time liveness information of the outer
	// phantom-supervisor process. Populated by the status endpoint via
	// ResolveSupervisorStatus; always present (non-nil) so the UI can
	// render a supervisor card without nil-checking each field.
	Supervisor *SupervisorStatus `json:"supervisor,omitempty"`

	// Legacy flat fields kept for older UI code that still reads them
	// directly. They are populated from Supervisor above and kept in sync.
	UnderSupervisor bool `json:"underSupervisor"`
	SupervisorPID   int  `json:"supervisorPID,omitempty"`
}

// resolveSupervisorInfo populates Status.Supervisor (and the
// backward-compatible UnderSupervisor/SupervisorPID fields) by performing
// a real liveness check against the supervisor PID.
func (s *Status) resolveSupervisorInfo(dataDir string) {
	live := ResolveSupervisorStatus(dataDir)
	s.Supervisor = &live
	s.UnderSupervisor = live.UnderSupervisor
	s.SupervisorPID = live.PID
}

type ApplyRequest struct {
	TargetVersion              string
	ReleaseID                  string
	ConfirmServiceInterruption bool
	ConfirmTaskInterruption    bool
}

type ApplyResult struct {
	Job          storage.SystemUpdateJob `json:"job"`
	EventScope   string                  `json:"eventScope"`
	EventScopeID string                  `json:"eventScopeId"`
}
