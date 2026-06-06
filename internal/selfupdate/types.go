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
}

type Status struct {
	Enabled               bool                       `json:"enabled"`
	Version               buildinfo.Info             `json:"version"`
	LatestCheck           *storage.SystemUpdateCheck `json:"latestCheck,omitempty"`
	ActiveJob             *storage.SystemUpdateJob   `json:"activeJob,omitempty"`
	LatestJob             *storage.SystemUpdateJob   `json:"latestJob,omitempty"`
	RestartTimeoutSeconds int                        `json:"restartTimeoutSeconds"`
	SupportedPlatform     bool                       `json:"supportedPlatform"`
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
