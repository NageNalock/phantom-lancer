package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/safelog"
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
		RestartMode:           s.cfg.RestartMode,
	}
	if p, err := s.installPath(); err == nil {
		status.InstallBinaryPath = p
	}
	if check, err := s.store.LatestSystemUpdateCheck(ctx); err == nil {
		status.LatestCheck = &check
	}
	if job, err := s.store.ActiveSystemUpdateJob(ctx); err == nil {
		status.ActiveJob = &job
		if job.BackupBinaryPath != "" {
			status.BackupBinaryPath = job.BackupBinaryPath
		}
	}
	if job, err := s.store.LatestSystemUpdateJob(ctx); err == nil {
		status.LatestJob = &job
		if status.BackupBinaryPath == "" && job.BackupBinaryPath != "" {
			status.BackupBinaryPath = job.BackupBinaryPath
		}
	}
	status.resolveSupervisorInfo(s.cfg.DataDir)
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
			ErrorMessage:   safelog.Text(err.Error(), 300),
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
	if s.log != nil {
		s.log.Info("system update job queued", "job_id", job.ID, "current_version", job.CurrentVersion, "target_version", job.TargetVersion, "asset_name", job.AssetName, "restart_mode", s.cfg.RestartMode)
	}
	s.append(ctx, job.ID, "update.job.created", map[string]any{"targetVersion": job.TargetVersion, "assetName": job.AssetName})

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()
	go s.run(runCtx, job.ID, check)

	return ApplyResult{Job: job, EventScope: eventScope, EventScopeID: job.ID}, nil
}

// Ensure reconciles any leftover pre-restart state from an interrupted previous run.
// It mirrors images.Service.Ensure — every in-flight job that was left
// behind before the restart handoff is marked failed + recorded in audit so
// Apply() can be retried. Restarting jobs are intentionally owned by
// ConfirmBoot(), because they need target-version confirmation or explicit
// boot-mismatch diagnostics rather than a generic stale-job cleanup.
func (s *Service) Ensure(ctx context.Context) error {
	ids, err := s.store.InterruptStaleSystemUpdateJobs(ctx, "服务重启，遗留的更新任务已被自动置为失败")
	if err != nil {
		return err
	}
	if len(ids) > 0 && s.log != nil {
		s.log.Warn("interrupted stale system update jobs at startup", "job_ids", ids)
	}
	for _, id := range ids {
		s.append(ctx, id, "update.job.interrupted", map[string]any{"cleanup": "startup"})
	}
	if len(ids) > 0 {
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
			EventType: "system.update.interrupted",
			RiskLevel: "medium",
			Summary:   fmt.Sprintf("启动时发现 %d 条遗留更新任务已被置为失败", len(ids)),
			Payload:   map[string]any{"jobIds": ids},
		})
	}
	return nil
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

// ConfirmBoot is called by main() early on startup. It reconciles the latest
// pending restarting job against the currently running build, performs a
// watchdog timeout check, and returns a rollback binary path when the
// service should immediately self-exec back to the previous known-good
// version. The returned string is empty when no automatic rollback is
// required.
func (s *Service) ConfirmBoot(ctx context.Context) (rollbackExecPath string) {
	// Reconcile persisted version metadata with the actual running binary.
	// When the binary was replaced manually (not via Apply()), the stored
	// LatestSystemUpdateCheck still carries an older `currentVersion` so the
	// UI shows "当前版本 = 旧版" even though the new binary is running. We
	// also bump `latestVersion` and clear stale update flags when the new
	// binary has already advanced past what we last saw upstream, so the
	// panel doesn't keep offering a non-existent update.
	s.reconcileManualUpgradeMetadata(ctx)
	defer s.deleteHandoff()

	job, err := s.store.LatestSystemUpdateJob(ctx)
	if err != nil || job.TargetVersion == "" {
		return ""
	}
	if job.Status != jobStatusRestarting {
		return ""
	}

	currentVersion := strings.TrimSpace(s.cfg.Build.Version)
	if currentVersion == "" {
		currentVersion = "unknown"
	}
	requestedAt, requestedAtSource, hasRequestedAt := restartRequestedAt(job)
	timeout := s.cfg.RestartTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	restartAge := time.Duration(0)
	timeoutElapsed := true
	if hasRequestedAt {
		restartAge = time.Since(requestedAt)
		timeoutElapsed = restartAge > timeout
	}
	if s.log != nil {
		s.log.Info(
			"confirming system update boot",
			"job_id", job.ID,
			"current_version", currentVersion,
			"target_version", job.TargetVersion,
			"status", job.Status,
			"phase", job.Phase,
			"restart_age_ms", restartAge.Milliseconds(),
			"restart_time_source", requestedAtSource,
			"restart_timeout_ms", timeout.Milliseconds(),
			"install_path", job.InstallBinaryPath,
			"backup_path", job.BackupBinaryPath,
		)
	}
	// Happy path: we're running the version the job was targeting — the
	// restart completed successfully.
	if job.TargetVersion == currentVersion {
		job.Status = jobStatusCompleted
		job.Phase = phaseCompleted
		job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
			if s.log != nil {
				s.log.Warn("confirm update boot failed", "error", err)
			}
			return ""
		}
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
			EventType: "system.update.completed",
			RiskLevel: "medium",
			Summary:   "系统更新已完成",
			Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.TargetVersion},
		})
		s.append(ctx, job.ID, "update.completed", map[string]any{"targetVersion": job.TargetVersion})
		if s.log != nil {
			s.log.Info("system update boot confirmed", "job_id", job.ID, "target_version", job.TargetVersion, "restart_age_ms", restartAge.Milliseconds())
		}
		return ""
	}

	// Watchdog path: the job was left in restarting state long enough that
	// the restart window has elapsed, and the running binary is NOT the
	// target version. This typically means the newly installed binary
	// crashed on startup and (for self-exec mode) the outer supervisor
	// respawned the original binary, or (for exit mode) the user manually
	// restarted the old one. When a valid backup exists we offer an
	// automatic rollback path.
	triggeredRollback := false
	if timeoutElapsed {
		if job.BackupBinaryPath != "" {
			if _, vbErr := validateBackupBinary(job.BackupBinaryPath); vbErr != nil {
				if s.log != nil {
					s.log.Warn("watchdog rollback backup validation failed", "job_id", job.ID, "backup_path", job.BackupBinaryPath, "error", safelog.Error(vbErr, 200))
				}
			} else {
				installP, ipErr := s.installPath()
				if ipErr != nil {
					if s.log != nil {
						s.log.Warn("watchdog rollback install path lookup failed", "job_id", job.ID, "error", safelog.Error(ipErr, 200))
					}
				} else if rbErr := RestoreBackup(installP, job.BackupBinaryPath); rbErr == nil {
					rollbackExecPath = installP
					triggeredRollback = true
					// Best-effort: also restore the sibling supervisor
					// backup (written by install() next to the main
					// backup at `<main>.supervisor`).
					supervisorBackup := job.BackupBinaryPath + ".supervisor"
					supervisorInstall := filepath.Join(filepath.Dir(installP), "phantom-supervisor")
					if sbInfo, sbErr := os.Stat(supervisorBackup); sbErr == nil && sbInfo.Mode().IsRegular() {
						if srbErr := RestoreBackup(supervisorInstall, supervisorBackup); srbErr != nil {
							if s.log != nil {
								s.log.Warn("watchdog rollback supervisor restore failed", "job_id", job.ID, "error", safelog.Error(srbErr, 200))
							}
						} else if s.log != nil {
							s.log.Info("watchdog rollback supervisor restored", "job_id", job.ID, "install_path", supervisorInstall, "backup_path", supervisorBackup)
						}
					}
					_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
						EventType: "system.update.rollback_auto",
						RiskLevel: "high",
						Summary:   "系统更新启动超时,已自动回滚到备份 binary",
						Payload: map[string]any{
							"jobId":          job.ID,
							"currentVersion": currentVersion,
							"targetVersion":  job.TargetVersion,
							"timeoutSeconds": int(timeout.Seconds()),
							"backupPath":     job.BackupBinaryPath,
						},
					})
					s.append(ctx, job.ID, "update.rollback.applied", map[string]any{"path": installP, "timeout": int(timeout.Seconds())})
					if s.log != nil {
						s.log.Warn("watchdog rollback restored backup binary", "job_id", job.ID, "install_path", installP, "backup_path", job.BackupBinaryPath, "restart_age_ms", restartAge.Milliseconds())
					}
				} else if s.log != nil {
					s.log.Warn("watchdog rollback restore failed", "job_id", job.ID, "install_path", installP, "backup_path", job.BackupBinaryPath, "error", safelog.Error(rbErr, 200))
				}
			}
		} else if s.log != nil {
			s.log.Warn("watchdog rollback skipped because no backup binary is recorded", "job_id", job.ID, "restart_age_ms", restartAge.Milliseconds())
		}
	} else if s.log != nil {
		s.log.Warn("system update boot version mismatch before watchdog timeout", "job_id", job.ID, "current_version", currentVersion, "target_version", job.TargetVersion, "restart_age_ms", restartAge.Milliseconds(), "restart_timeout_ms", timeout.Milliseconds())
	}

	// Always finalise the job; even if rollback failed we should not leave
	// it in restarting state forever.
	job.Status = jobStatusFailed
	msg := fmt.Sprintf("service restarted with version %s instead of target %s", currentVersion, job.TargetVersion)
	if triggeredRollback {
		msg += "; restart watchdog timed out, backup binary restored automatically"
	} else if !timeoutElapsed {
		msg += "; version mismatch was detected before the restart watchdog timeout while the non-target process is already serving"
	} else if rollbackExecPath == "" && job.BackupBinaryPath == "" {
		msg += "; no backup binary available for auto-rollback"
	}
	job.ErrorMessage = msg
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
		if s.log != nil {
			s.log.Warn("mark update boot mismatch failed", "job_id", job.ID, "error", safelog.Error(err, 200))
		}
		return rollbackExecPath
	}
	s.append(ctx, job.ID, "update.failed", map[string]any{"phase": job.Phase, "message": msg, "rollback": triggeredRollback})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.boot_mismatch",
		RiskLevel: "high",
		Summary:   "系统更新重启后版本不匹配",
		Payload:   map[string]any{"jobId": job.ID, "currentVersion": currentVersion, "targetVersion": job.TargetVersion, "autoRollback": triggeredRollback},
	})
	if s.log != nil {
		s.log.Warn("system update boot mismatch finalized", "job_id", job.ID, "current_version", currentVersion, "target_version", job.TargetVersion, "rollback", triggeredRollback, "message", safelog.Text(msg, 300))
	}
	return rollbackExecPath
}

// reconcileManualUpgradeMetadata bridges the most common manual-upgrade
// version-metadata "bug": when the binary is replaced out of band (not via
// the Apply flow), /api/system/update/status.latestCheck.currentVersion
// still reports the previous build's version (stored in SQLite), even
// though buildinfo.Current() (and /api/system/version) are correct. This
// confuses the operator ("UI says I'm still on 1.2.3 but I replaced with
// 1.3.0").
//
// The reconcile is a NOOP unless ALL of the following are true:
//   - no restarting/pending update job exists (so we're not mid Apply)
//   - the stored latestCheck.currentVersion differs from build.Version
//
// When that happens we:
//  1. clone the latest check (or create a minimal one if absent)
//  2. set currentVersion = build.Version
//  3. if build.Version is strictly newer than stored latestVersion, we
//     also set latestVersion = build.Version and clear update_available
//     / can_apply flags — the operator has obviously already obtained a
//     build newer than the last release scan, so showing "update to
//     older-than-me v1.2.0" would be worse than showing nothing.
//
// We never OVERWRITE a newer latestVersion, and we never touch release
// metadata (asset url, checksum) if latestVersion is preserved — that way
// the next Check() still short-circuits via ETag correctly.
func (s *Service) reconcileManualUpgradeMetadata(ctx context.Context) {
	buildVersion := strings.TrimSpace(s.cfg.Build.Version)
	if buildVersion == "" || buildVersion == "dev" || strings.Contains(buildVersion, "-dev") {
		return
	}
	if active, err := s.store.ActiveSystemUpdateJob(ctx); err == nil && active.ID != "" {
		return
	}
	latest, err := s.store.LatestSystemUpdateCheck(ctx)
	if err == nil && strings.TrimSpace(latest.CurrentVersion) == buildVersion {
		return
	}
	var next storage.SystemUpdateCheck
	if err == nil && latest.ID != "" {
		next = latest
	} else {
		next = storage.SystemUpdateCheck{
			PlatformSupported: supportedPlatform(),
		}
	}
	next.CurrentVersion = buildVersion
	// If the operator already advanced to a build newer than our last
	// upstream scan, treat the running version as the new baseline. This
	// avoids showing "有更新 (可降级到旧版本)" which is never useful.
	cmpBuildLatest, cmpOK := compareVersions(buildVersion, next.LatestVersion)
	if cmpOK && cmpBuildLatest > 0 {
		next.LatestVersion = buildVersion
		next.UpdateAvailable = false
		next.CanApply = false
		next.Comparable = true
		next.Reason = "manual_upgrade_detected"
		next.ReleaseID = ""
		next.ReleaseURL = ""
		next.PublishedAt = ""
		next.AssetName = ""
		next.AssetSizeBytes = 0
		next.AssetURL = ""
		next.ChecksumAssetURL = ""
		next.ChecksumAvailable = false
		next.ETag = ""
		next.ErrorMessage = ""
	} else if !cmpOK {
		next.Comparable = false
	}
	if _, addErr := s.store.AddSystemUpdateCheck(ctx, next); addErr != nil && s.log != nil {
		s.log.Warn("manual upgrade metadata reconcile failed",
			"current_version", next.CurrentVersion,
			"latest_version", next.LatestVersion,
			"error", safelog.Error(addErr, 200),
		)
	} else if s.log != nil {
		s.log.Info("manual upgrade metadata reconciled",
			"current_version", next.CurrentVersion,
			"latest_version", next.LatestVersion,
		)
	}
}

func (s *Service) run(ctx context.Context, jobID string, check storage.SystemUpdateCheck) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, jobID)
		s.mu.Unlock()
	}()
	// Panic fence: any bug in download/verify/extract/install that causes a
	// panic inside the goroutine MUST not leak a running-status job into the
	// database (which would block Apply forever).
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			if s.log != nil {
				s.log.Error("update run panicked", "panic", fmt.Sprintf("%v", r), "stack", string(stack))
			}
			job, getErr := s.store.GetSystemUpdateJob(context.Background(), jobID)
			if getErr != nil {
				return
			}
			_ = s.failJob(context.Background(), job.ID, fmt.Sprintf("panic: %v\n\n%s", r, string(stack)))
		}
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

// failJob writes the final failed state + audits it. Unlike the private
// fail(job, err) method which is meant to be called from inside run() while
// the job struct is in scope, failJob accepts a jobID and reloads so it can
// be called from the panic fence or other detached call sites.
func (s *Service) failJob(ctx context.Context, jobID, message string) error {
	job, err := s.store.GetSystemUpdateJob(ctx, jobID)
	if err != nil {
		return err
	}
	s.fail(ctx, &job, errors.New(truncate(message, 500)))
	return nil
}

func (s *Service) execute(ctx context.Context, job *storage.SystemUpdateJob, check storage.SystemUpdateCheck) error {
	stage, err := s.prepareStaging(job.ID)
	if err != nil {
		return err
	}
	// Clean up the per-job staging tree unconditionally when execute
	// returns. A cancelled, failed, or successful run all leave behind
	// ~MBs of archive otherwise.
	defer func() {
		if stage.dir != "" {
			_ = os.RemoveAll(stage.dir)
		}
	}()
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
	extractResult, err := extractBinaries(stage.archive, stage.stagedBinary)
	if err != nil {
		return err
	}
	stage.stagedSupervisor = extractResult.SupervisorBinary
	if err := verifyStagedVersion(ctx, stage.stagedBinary, job.TargetVersion); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.extract.completed", map[string]any{"targetVersion": job.TargetVersion, "hasSupervisorBinary": stage.stagedSupervisor != ""})

	job.Phase = phaseInstalling
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.install.started", map[string]any{"targetVersion": job.TargetVersion})
	installResult, err := s.install(ctx, job.ID, stage.stagedBinary, stage.stagedSupervisor, job.CurrentVersion)
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
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.save(ctx, *job); err != nil {
		return err
	}
	s.append(ctx, job.ID, "update.restart.requested", map[string]any{"targetVersion": job.TargetVersion, "restartMode": s.cfg.RestartMode})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.restart_requested",
		RiskLevel: "high",
		Summary:   "系统更新已请求服务重启",
		Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.TargetVersion},
	})
	if s.log != nil {
		s.log.Info(
			"system update restart requested",
			"job_id", job.ID,
			"current_version", job.CurrentVersion,
			"target_version", job.TargetVersion,
			"restart_mode", s.cfg.RestartMode,
			"install_path", installResult.installPath,
			"backup_path", installResult.backupPath,
			"restart_timeout_ms", s.cfg.RestartTimeout.Milliseconds(),
			"requires_external_supervisor", s.cfg.RestartMode == RestartModeExit,
		)
	}
	s.dispatchRestart(job.ID, job.TargetVersion, installResult.installPath, installResult.backupPath, installResult.supervisorBackupPath, "update")
	return nil
}

func (s *Service) fail(ctx context.Context, job *storage.SystemUpdateJob, err error) {
	job.Status = jobStatusFailed
	job.ErrorMessage = safelog.Text(err.Error(), 500)
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
		s.log.Warn("system update failed", "job_id", job.ID, "phase", job.Phase, "error", job.ErrorMessage)
	}
}

// Rollback atomically restores the backup binary referenced by the given
// (completed or failed) update job, updates the job row, and returns both
// the refreshed job record and the absolute path of the binary that has
// just been restored to (the caller may wish to syscall.Exec that path).
//
// Only jobs with a validated backup binary on disk can be rolled back.
func (s *Service) Rollback(ctx context.Context, jobID string) (storage.SystemUpdateJob, string, error) {
	job, err := s.store.GetSystemUpdateJob(ctx, jobID)
	if err != nil {
		return storage.SystemUpdateJob{}, "", err
	}
	if job.Status == jobStatusQueued || job.Status == jobStatusRunning || job.Status == jobStatusRestarting {
		return storage.SystemUpdateJob{}, "", errors.New("cannot rollback a job that is still in progress; cancel it first")
	}
	if _, verr := validateBackupBinary(job.BackupBinaryPath); verr != nil {
		return storage.SystemUpdateJob{}, "", fmt.Errorf("rollback: backup binary invalid or missing: %w", verr)
	}
	installPath, ierr := s.installPath()
	if ierr != nil {
		return storage.SystemUpdateJob{}, "", ierr
	}
	if err := RestoreBackup(installPath, job.BackupBinaryPath); err != nil {
		return storage.SystemUpdateJob{}, "", err
	}
	// Also restore the supervisor binary if a sibling backup exists. The
	// backup is laid down at the same time as the main binary backup (at
	// `<mainBackup>.supervisor`), so its presence mirrors the main backup's
	// validity. Failure is logged but non-fatal: the main binary is already
	// restored; an old/new supervisor mismatch is recoverable via the next
	// update or manual deploy.
	supervisorBackupPath := job.BackupBinaryPath + ".supervisor"
	supervisorInstallPath := filepath.Join(filepath.Dir(installPath), "phantom-supervisor")
	if sInfo, sErr := os.Stat(supervisorBackupPath); sErr == nil && sInfo.Mode().IsRegular() {
		if rErr := RestoreBackup(supervisorInstallPath, supervisorBackupPath); rErr != nil {
			if s.log != nil {
				s.log.Warn("system update rollback supervisor restore failed", "job_id", job.ID, "error", safelog.Error(rErr, 200))
			}
		} else if s.log != nil {
			s.log.Info("system update rollback supervisor restored", "job_id", job.ID, "install_path", supervisorInstallPath, "backup_path", supervisorBackupPath)
		}
	}
	suffix := " + rollback applied"
	alreadyRolledBack := strings.Contains(job.ErrorMessage, "rollback applied")
	if alreadyRolledBack {
		// Idempotent: the rollback note is already on the job, don't double-append.
	} else if strings.TrimSpace(job.ErrorMessage) == "" {
		job.ErrorMessage = "rollback applied"
	} else {
		job.ErrorMessage = job.ErrorMessage + suffix
	}
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.SaveSystemUpdateJob(ctx, job); err != nil {
		return storage.SystemUpdateJob{}, "", err
	}
	s.append(ctx, job.ID, "update.rollback.applied", map[string]any{"path": installPath, "source": "manual"})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "system.update.rollback",
		RiskLevel: "high",
		Summary:   "已手动回滚到更新前的 binary",
		Payload: map[string]any{
			"jobId":           job.ID,
			"currentVersion":  job.TargetVersion,
			"rollbackVersion": job.CurrentVersion,
			"backupPath":      job.BackupBinaryPath,
			"installPath":     installPath,
		},
	})
	if alreadyRolledBack {
		if s.log != nil {
			s.log.Info("system update rollback already applied; restart dispatch skipped", "job_id", job.ID, "install_path", installPath)
		}
	} else {
		s.append(ctx, job.ID, "update.restart.requested", map[string]any{"targetVersion": job.CurrentVersion, "restartMode": s.cfg.RestartMode, "source": "rollback"})
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
			EventType: "system.update.restart_requested",
			RiskLevel: "high",
			Summary:   "系统回滚已请求服务重启",
			Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.CurrentVersion, "source": "rollback"},
		})
		rollbackSupervisorBackup := supervisorBackupPathFor(job.BackupBinaryPath)
		s.writeHandoff(job.ID, "rollback", job.TargetVersion, job.CurrentVersion, installPath, job.BackupBinaryPath, rollbackSupervisorBackup)
		s.dispatchRestart(job.ID, job.CurrentVersion, installPath, job.BackupBinaryPath, rollbackSupervisorBackup, "rollback")
	}
	return job, installPath, nil
}

func (s *Service) dispatchRestart(jobID, targetVersion, installPath, backupPath, supervisorBackupPath, source string) {
	if s.log != nil {
		s.log.Info(
			"system update restart dispatch",
			"job_id", jobID,
			"source", source,
			"target_version", targetVersion,
			"restart_mode", s.cfg.RestartMode,
			"install_path", installPath,
			"backup_path", backupPath,
			"supervisor_backup_path", supervisorBackupPath,
			"requires_external_supervisor", s.cfg.RestartMode == RestartModeExit,
		)
	}
	// Write the handoff marker BEFORE dispatching the restart so even an
	// immediate exit still leaves the marker on disk for the outer
	// supervisor (or a manual restart) to pick up.
	s.writeHandoff(jobID, source, "", targetVersion, installPath, backupPath, supervisorBackupPath)
	switch s.cfg.RestartMode {
	case RestartModeExit:
		s.requestRestartAfterDelay()
	case RestartModeSelfExec:
		if s.cfg.PrepareSelfExec != nil {
			s.cfg.PrepareSelfExec(installPath)
		}
		s.requestRestartAfterDelay()
	case RestartModeNone:
		// Operator handles restart; UI shows a manual-restart CTA when
		// it sees phase=restarting combined with mode=none.
		if s.log != nil {
			s.log.Warn("system update installed but restart mode is none", "job_id", jobID, "source", source, "target_version", targetVersion, "install_path", installPath)
		}
	default:
		// Unknown mode: behave like "none" rather than silently exiting.
		if s.log != nil {
			s.log.Warn("system update installed but restart mode is unknown", "job_id", jobID, "source", source, "restart_mode", s.cfg.RestartMode, "target_version", targetVersion)
		}
	}
}

func (s *Service) requestRestartAfterDelay() {
	if s.cfg.RequestRestart == nil {
		return
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.cfg.RequestRestart()
	}()
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

func restartRequestedAt(job storage.SystemUpdateJob) (time.Time, string, bool) {
	candidates := []struct {
		name  string
		value string
	}{
		{name: "completed_at", value: job.CompletedAt},
		{name: "started_at", value: job.StartedAt},
		{name: "created_at", value: job.CreatedAt},
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, candidate.value)
		if err == nil {
			return parsed, candidate.name, true
		}
	}
	return time.Time{}, "missing", false
}

func shortChecksum(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

// supervisorBackupPathFor returns the conventional sibling path where the
// installer snapshots the supervisor binary next to the main binary backup.
// It returns an empty string when the main backup path is empty (no backup
// was taken, therefore a supervisor backup cannot exist either).
func supervisorBackupPathFor(mainBackupPath string) string {
	if strings.TrimSpace(mainBackupPath) == "" {
		return ""
	}
	return mainBackupPath + ".supervisor"
}

func targetMismatch(check storage.SystemUpdateCheck, targetVersion, releaseID string) bool {
	return strings.TrimSpace(targetVersion) != check.LatestVersion || strings.TrimSpace(releaseID) != check.ReleaseID
}
