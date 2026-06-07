package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

const eventScope = "system_update"

type Service struct {
	store *storage.Store
	hub   *events.Hub
	log   *slog.Logger
	cfg   Config

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewService(store *storage.Store, hub *events.Hub, logger *slog.Logger, cfg Config) *Service {
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	if cfg.HTTPClient == nil {
		timeout := cfg.DownloadTimeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		cfg.HTTPClient = &http.Client{Timeout: timeout}
	}
	return &Service{store: store, hub: hub, log: logger, cfg: cfg, cancels: make(map[string]context.CancelFunc)}
}

func (s *Service) Status(ctx context.Context) Status {
	status := Status{
		Enabled:               s.cfg.Enabled,
		Version:               s.cfg.Build,
		RestartTimeoutSeconds: int(s.cfg.RestartTimeout.Seconds()),
		SupportedPlatform:     supportedPlatform(),
	}
	if check, err := s.store.LatestSystemUpdateCheck(ctx); err == nil {
		status.LatestCheck = &check
	}
	if job, err := s.store.ActiveSystemUpdateJob(ctx); err == nil {
		status.ActiveJob = &job
	}
	if job, err := s.store.LatestSystemUpdateJob(ctx); err == nil {
		status.LatestJob = &job
	}
	return status
}

func (s *Service) Check(ctx context.Context) (storage.SystemUpdateCheck, error) {
	current := s.cfg.Build.Version
	if !s.cfg.Enabled {
		return s.store.AddSystemUpdateCheck(ctx, storage.SystemUpdateCheck{
			CurrentVersion: current,
			Reason:         "updates are disabled",
		})
	}

	var etag string
	var previous storage.SystemUpdateCheck
	if check, err := s.store.LatestSystemUpdateCheck(ctx); err == nil {
		previous = check
		etag = check.ETag
	}
	s.append(ctx, "default", "update.check.started", map[string]any{"currentVersion": current})
	check, notModified, err := s.fetchLatestRelease(ctx, etag)
	if notModified && previous.ID != "" {
		s.append(ctx, "default", "update.check.completed", map[string]any{"latestVersion": previous.LatestVersion, "notModified": true, "canApply": previous.CanApply})
		return previous, nil
	}
	if err != nil {
		failed, addErr := s.store.AddSystemUpdateCheck(ctx, storage.SystemUpdateCheck{
			CurrentVersion: current,
			ErrorMessage:   truncate(err.Error(), 300),
			Reason:         "release check failed",
			ETag:           etag,
		})
		s.append(ctx, "default", "update.check.completed", map[string]any{"error": failed.ErrorMessage})
		if addErr != nil {
			return storage.SystemUpdateCheck{}, addErr
		}
		return failed, err
	}
	stored, err := s.store.AddSystemUpdateCheck(ctx, check)
	if err != nil {
		return storage.SystemUpdateCheck{}, err
	}
	s.append(ctx, "default", "update.check.completed", map[string]any{
		"latestVersion":   stored.LatestVersion,
		"updateAvailable": stored.UpdateAvailable,
		"canApply":        stored.CanApply,
		"reason":          stored.Reason,
	})
	return stored, nil
}

func (s *Service) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	if !s.cfg.Enabled {
		return ApplyResult{}, errors.New("updates are disabled")
	}
	if !req.ConfirmServiceInterruption || !req.ConfirmTaskInterruption {
		return ApplyResult{}, errors.New("update interruption confirmation is required")
	}
	if active, err := s.store.ActiveSystemUpdateJob(ctx); err == nil {
		return ApplyResult{}, fmt.Errorf("update job already active: %s", active.ID)
	}
	check, err := s.store.LatestSystemUpdateCheck(ctx)
	if err != nil {
		return ApplyResult{}, errors.New("check for updates before applying")
	}
	if targetMismatch(check, req.TargetVersion, req.ReleaseID) {
		return ApplyResult{}, errors.New("requested update does not match the latest checked release")
	}
	if !check.CanApply {
		if check.Reason != "" {
			return ApplyResult{}, errors.New(check.Reason)
		}
		return ApplyResult{}, errors.New("latest release cannot be applied")
	}
	job, err := s.store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: check.CurrentVersion,
		TargetVersion:  check.LatestVersion,
		ReleaseID:      check.ReleaseID,
		AssetName:      check.AssetName,
		Status:         jobStatusQueued,
		Phase:          phaseCreated,
		TotalBytes:     check.AssetSizeBytes,
	})
	if err != nil {
		return ApplyResult{}, err
	}
	s.append(ctx, job.ID, "update.job.created", map[string]any{"targetVersion": job.TargetVersion, "assetName": job.AssetName})

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()
	go s.run(runCtx, job.ID, check)

	return ApplyResult{Job: job, EventScope: eventScope, EventScopeID: job.ID}, nil
}

func (s *Service) Cancel(ctx context.Context, id string) (storage.SystemUpdateJob, error) {
	job, err := s.store.GetSystemUpdateJob(ctx, id)
	if err != nil {
		return storage.SystemUpdateJob{}, err
	}
	if job.Phase == phaseInstalling || job.Phase == phaseRestarting || job.Status == jobStatusCompleted {
		return storage.SystemUpdateJob{}, errors.New("update can no longer be cancelled")
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	job.Status = jobStatusCancelled
	job.ErrorMessage = "用户已取消更新"
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
		return storage.SystemUpdateJob{}, err
	}
	s.append(ctx, job.ID, "update.cancelled", map[string]any{"phase": job.Phase})
	return job, nil
}

func (s *Service) ConfirmBoot(ctx context.Context) {
	job, err := s.store.LatestSystemUpdateJob(ctx)
	if err != nil || job.TargetVersion == "" {
		return
	}
	if job.Status != jobStatusRestarting && job.Phase != phaseRestarting {
		return
	}
	if job.TargetVersion != s.cfg.Build.Version {
		currentVersion := strings.TrimSpace(s.cfg.Build.Version)
		if currentVersion == "" {
			currentVersion = "unknown"
		}
		job.Status = jobStatusFailed
		job.ErrorMessage = fmt.Sprintf("service restarted with version %s instead of target %s", currentVersion, job.TargetVersion)
		job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
			if s.log != nil {
				s.log.Warn("mark update boot mismatch failed", "error", err)
			}
			return
		}
		s.append(ctx, job.ID, "update.failed", map[string]any{"phase": job.Phase, "message": job.ErrorMessage})
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
			EventType: "system.update.boot_mismatch",
			RiskLevel: "high",
			Summary:   "系统更新重启后版本不匹配",
			Payload:   map[string]any{"jobId": job.ID, "currentVersion": currentVersion, "targetVersion": job.TargetVersion},
		})
		return
	}
	job.Status = jobStatusCompleted
	job.Phase = phaseCompleted
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
		if s.log != nil {
			s.log.Warn("confirm update boot failed", "error", err)
		}
		return
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.completed",
		RiskLevel: "medium",
		Summary:   "系统更新已完成",
		Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.TargetVersion},
	})
}

func (s *Service) run(ctx context.Context, jobID string, check storage.SystemUpdateCheck) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, jobID)
		s.mu.Unlock()
	}()
	job, err := s.store.GetSystemUpdateJob(context.Background(), jobID)
	if err != nil {
		return
	}
	job.Status = jobStatusRunning
	job.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.save(context.Background(), job); err != nil {
		return
	}

	if err := s.execute(ctx, &job, check); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			job.Status = jobStatusCancelled
			job.ErrorMessage = "用户已取消更新"
			job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			_ = s.save(context.Background(), job)
			s.append(context.Background(), job.ID, "update.cancelled", map[string]any{"phase": job.Phase})
			return
		}
		s.fail(context.Background(), &job, err)
		return
	}
}

func (s *Service) execute(ctx context.Context, job *storage.SystemUpdateJob, check storage.SystemUpdateCheck) error {
	stage, err := s.prepareStaging(job.ID)
	if err != nil {
		return err
	}
	job.Phase = phaseDownloading
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.download.started", map[string]any{"assetName": job.AssetName, "totalBytes": job.TotalBytes})
	if err := s.download(ctx, check.AssetURL, stage.archivePart, job, maxArchiveBytes); err != nil {
		return err
	}
	if err := rename(stage.archivePart, stage.archive); err != nil {
		return err
	}
	if err := s.downloadChecksum(ctx, check.ChecksumAssetURL, stage.checksum); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.download.completed", map[string]any{"bytes": job.BytesDownloaded})

	job.Phase = phaseVerifying
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	sum, err := verifyChecksum(stage.archive, stage.checksum)
	if err != nil {
		return err
	}
	job.ChecksumSHA256 = sum
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.verify.completed", map[string]any{"checksum": shortChecksum(sum)})

	job.Phase = phaseExtracting
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	if err := extractBinary(stage.archive, stage.stagedBinary); err != nil {
		return err
	}
	if err := verifyStagedVersion(ctx, stage.stagedBinary, job.TargetVersion); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.extract.completed", map[string]any{"targetVersion": job.TargetVersion})

	job.Phase = phaseInstalling
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	installResult, err := s.install(ctx, job.ID, stage.stagedBinary, job.CurrentVersion)
	if err != nil {
		return err
	}
	job.InstallBinaryPath = installResult.installPath
	job.BackupBinaryPath = installResult.backupPath
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.install",
		RiskLevel: "high",
		Summary:   "系统更新包已安装",
		Payload: map[string]any{
			"jobId":          job.ID,
			"currentVersion": job.CurrentVersion,
			"targetVersion":  job.TargetVersion,
			"assetName":      job.AssetName,
			"checksum":       shortChecksum(job.ChecksumSHA256),
		},
	})
	s.append(ctx, job.ID, "update.install.completed", map[string]any{"targetVersion": job.TargetVersion})

	job.Status = jobStatusRestarting
	job.Phase = phaseRestarting
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.restart.requested", map[string]any{"targetVersion": job.TargetVersion})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.restart_requested",
		RiskLevel: "high",
		Summary:   "系统更新已请求服务重启",
		Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.TargetVersion},
	})
	if s.cfg.RestartMode == "exit" && s.cfg.RequestRestart != nil {
		go func() {
			time.Sleep(300 * time.Millisecond)
			s.cfg.RequestRestart()
		}()
	}
	return nil
}

func (s *Service) fail(ctx context.Context, job *storage.SystemUpdateJob, err error) {
	job.Status = jobStatusFailed
	job.ErrorMessage = truncate(err.Error(), 500)
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_ = s.save(ctx, *job)
	s.append(ctx, job.ID, "update.failed", map[string]any{"phase": job.Phase, "message": job.ErrorMessage})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.failed",
		RiskLevel: "high",
		Summary:   "系统更新失败",
		Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.TargetVersion, "phase": job.Phase, "error": job.ErrorMessage},
	})
	if s.log != nil {
		s.log.Warn("system update failed", "jobId", job.ID, "phase", job.Phase, "error", job.ErrorMessage)
	}
}

func (s *Service) save(ctx context.Context, job storage.SystemUpdateJob) error {
	if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.job.updated", map[string]any{"status": job.Status, "phase": job.Phase, "bytesDownloaded": job.BytesDownloaded, "totalBytes": job.TotalBytes})
	return nil
}

func (s *Service) append(ctx context.Context, scopeID, eventType string, payload map[string]any) {
	event, err := s.store.AppendEvent(ctx, eventScope, scopeID, eventType, payload)
	if err == nil && s.hub != nil {
		s.hub.Publish(event)
	}
}

func (s *Service) httpClient() *http.Client {
	if s.cfg.HTTPClient != nil {
		return s.cfg.HTTPClient
	}
	return http.DefaultClient
}

func supportedPlatform() bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func shortChecksum(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func targetMismatch(check storage.SystemUpdateCheck, targetVersion, releaseID string) bool {
	return strings.TrimSpace(targetVersion) != check.LatestVersion || strings.TrimSpace(releaseID) != check.ReleaseID
}
