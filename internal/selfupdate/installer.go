package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"phantom-lancer/internal/safelog"
)

const installDatabaseBackupTimeout = 2 * time.Minute

type installResult struct {
	installPath          string
	backupPath           string
	supervisorInstalled  bool
	supervisorBackupPath string
}

func verifyStagedVersion(ctx context.Context, binaryPath, targetVersion string) error {
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := safelog.Text(string(output), 300)
		if detail == "" {
			return fmt.Errorf("staged binary version check failed: %w", err)
		}
		return fmt.Errorf("staged binary version check failed: %w; output=%s", err, detail)
	}
	if !strings.Contains(string(output), targetVersion) {
		return fmt.Errorf("staged binary version does not match target %s; output=%s", targetVersion, safelog.Text(string(output), 300))
	}
	return nil
}

func (s *Service) install(ctx context.Context, jobID, stagedBinary, stagedSupervisor, currentVersion string) (installResult, error) {
	installPath, err := s.installPath()
	if err != nil {
		return installResult{}, err
	}
	if s.log != nil {
		s.log.Info("system update install started", "job_id", jobID, "install_path", installPath, "current_version", currentVersion)
	}
	if err := ensureWritableDir(filepath.Dir(installPath)); err != nil {
		return installResult{}, err
	}
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "backups"), 0o700); err != nil {
		return installResult{}, err
	}
	dbBackup := filepath.Join(s.cfg.DataDir, "backups", "pre-update-"+jobID+".db")
	if s.log != nil {
		s.log.Info("system update database backup started", "job_id", jobID, "db_backup_path", dbBackup, "timeout_ms", installDatabaseBackupTimeout.Milliseconds())
	}
	s.append(ctx, jobID, "update.install.db_backup.started", map[string]any{"timeoutSeconds": int(installDatabaseBackupTimeout.Seconds())})
	backupCtx, cancelBackup := context.WithTimeout(ctx, installDatabaseBackupTimeout)
	defer cancelBackup()
	if err := s.store.BackupDatabase(backupCtx, dbBackup); err != nil {
		if s.log != nil {
			s.log.Warn("system update database backup failed", "job_id", jobID, "db_backup_path", dbBackup, "error", safelog.Error(err, 200))
		}
		s.append(ctx, jobID, "update.install.db_backup.failed", map[string]any{"error": safelog.Error(err, 200)})
		return installResult{}, fmt.Errorf("database backup failed: %w", err)
	}
	if s.log != nil {
		s.log.Info("system update database backup completed", "job_id", jobID, "db_backup_path", dbBackup)
	}
	s.append(ctx, jobID, "update.install.db_backup.completed", map[string]any{"path": dbBackup})

	backupDir := filepath.Join(s.cfg.DataDir, "updates", "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return installResult{}, err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	backupPath := filepath.Join(backupDir, "phantom-lancer-"+safeName(currentVersion)+"-"+ts)
	supervisorBackupPath := backupPath + ".supervisor"
	currentInfo, err := os.Stat(installPath)
	if err != nil {
		return installResult{}, fmt.Errorf("current binary is not readable: %w", err)
	}
	if !currentInfo.Mode().IsRegular() {
		return installResult{}, errors.New("current binary path is not a regular file")
	}
	if err := copyFile(installPath, backupPath, currentInfo.Mode().Perm()); err != nil {
		return installResult{}, fmt.Errorf("backup current binary failed: %w", err)
	}
	if s.log != nil {
		s.log.Info("system update current binary backed up", "job_id", jobID, "backup_path", backupPath, "mode", currentInfo.Mode().Perm().String())
	}

	// Best-effort backup of the existing supervisor binary (if any) so that
	// the supervisor's own rollback path can restore it atomically later.
	// Failure is non-fatal: the main binary's update is the critical path.
	currentSupervisorPath := filepath.Join(filepath.Dir(installPath), "phantom-supervisor")
	supervisorBackupWritten := false
	if sInfo, sErr := os.Stat(currentSupervisorPath); sErr == nil && sInfo.Mode().IsRegular() {
		if bErr := copyFile(currentSupervisorPath, supervisorBackupPath, sInfo.Mode().Perm()); bErr == nil {
			supervisorBackupWritten = true
			if s.log != nil {
				s.log.Info("system update current supervisor backed up", "job_id", jobID, "backup_path", supervisorBackupPath)
			}
		} else if s.log != nil {
			s.log.Warn("system update supervisor backup skipped", "job_id", jobID, "error", safelog.Error(bErr, 200))
		}
	}

	tempPath := filepath.Join(filepath.Dir(installPath), ".phantom-lancer."+jobID+".tmp")
	if err := copyFile(stagedBinary, tempPath, currentInfo.Mode().Perm()); err != nil {
		_ = os.Remove(tempPath)
		return installResult{}, fmt.Errorf("copy staged binary failed: %w", err)
	}
	if err := preserveOwnership(tempPath, currentInfo); err != nil {
		_ = os.Remove(tempPath)
		return installResult{}, fmt.Errorf("preserve binary ownership failed: %w", err)
	}
	if err := os.Rename(tempPath, installPath); err != nil {
		_ = os.Remove(tempPath)
		return installResult{}, fmt.Errorf("replace binary failed: %w", err)
	}
	if err := fsyncDir(filepath.Dir(installPath)); err != nil {
		return installResult{}, err
	}
	if s.log != nil {
		s.log.Info("system update binary replaced", "job_id", jobID, "install_path", installPath, "backup_path", backupPath)
	}
	// Install supervisor binary if present (Phase 1: in-place replacement;
	// the running supervisor process keeps the old fd, so no crash).
	// Supervisor install failure is deliberately non-fatal: the old binary
	// can still restart the new main program.
	result := installResult{
		installPath: installPath,
		backupPath:  backupPath,
	}
	if strings.TrimSpace(stagedSupervisor) != "" {
		if sErr := s.installSupervisor(jobID, stagedSupervisor, currentInfo); sErr != nil {
			if s.log != nil {
				s.log.Warn("system update supervisor install skipped", "job_id", jobID, "error", safelog.Error(sErr, 200))
			}
		} else {
			result.supervisorInstalled = true
			if supervisorBackupWritten {
				result.supervisorBackupPath = supervisorBackupPath
			}
		}
	} else if supervisorBackupWritten {
		// No new supervisor staged in this update, but an old one exists
		// and we snapshotted it — expose the backup so handoff/rollback
		// paths still have a known-good supervisor to fall back to.
		result.supervisorBackupPath = supervisorBackupPath
	}
	s.pruneBackups(backupDir)
	return result, nil
}

// installSupervisor atomically replaces the supervisor binary that lives in
// the same directory as the main install path. currentInfo provides the
// reference ownership/permissions (the main and supervisor binaries are
// expected to share those).
func (s *Service) installSupervisor(jobID, stagedSupervisor string, currentInfo os.FileInfo) error {
	installPath, err := s.installPath()
	if err != nil {
		return err
	}
	supervisorInstallPath := filepath.Join(filepath.Dir(installPath), "phantom-supervisor")
	supervisorTmp := filepath.Join(filepath.Dir(supervisorInstallPath), ".phantom-supervisor."+jobID+".tmp")
	_ = os.Remove(supervisorTmp)
	mode := currentInfo.Mode().Perm()
	if mode&0o111 == 0 {
		mode = mode | 0o755
	}
	if err := copyFile(stagedSupervisor, supervisorTmp, mode); err != nil {
		_ = os.Remove(supervisorTmp)
		return fmt.Errorf("copy staged supervisor failed: %w", err)
	}
	if err := preserveOwnership(supervisorTmp, currentInfo); err != nil {
		_ = os.Remove(supervisorTmp)
		return fmt.Errorf("preserve supervisor ownership failed: %w", err)
	}
	if err := os.Rename(supervisorTmp, supervisorInstallPath); err != nil {
		_ = os.Remove(supervisorTmp)
		return fmt.Errorf("replace supervisor binary failed: %w", err)
	}
	if err := fsyncDir(filepath.Dir(supervisorInstallPath)); err != nil {
		return err
	}
	if s.log != nil {
		s.log.Info("system update supervisor binary replaced", "job_id", jobID, "install_path", supervisorInstallPath)
	}
	return nil
}

func (s *Service) installPath() (string, error) {
	if strings.TrimSpace(s.cfg.InstallBinaryPath) != "" {
		return filepath.Clean(s.cfg.InstallBinaryPath), nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return path, nil
}

func ensureWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("install directory is not a directory")
	}
	probe, err := os.CreateTemp(dir, ".phantom-lancer-write-test-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

func copyFile(from, to string, mode os.FileMode) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	if err := target.Chmod(mode); err != nil {
		return err
	}
	return target.Sync()
}

func preserveOwnership(path string, reference os.FileInfo) error {
	if os.Geteuid() != 0 {
		return nil
	}
	stat, ok := reference.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(path, int(stat.Uid), int(stat.Gid))
}

func fsyncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func (s *Service) pruneBackups(dir string) {
	if s.cfg.BackupRetention <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= s.cfg.BackupRetention {
		return
	}
	type item struct {
		name string
		time time.Time
	}
	items := []item{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		items = append(items, item{name: entry.Name(), time: info.ModTime()})
	}
	if len(items) <= s.cfg.BackupRetention {
		return
	}
	for len(items) > s.cfg.BackupRetention {
		oldest := 0
		for index := 1; index < len(items); index++ {
			if items[index].time.Before(items[oldest].time) {
				oldest = index
			}
		}
		_ = os.Remove(filepath.Join(dir, items[oldest].name))
		items = append(items[:oldest], items[oldest+1:]...)
	}
}

// validateBackupBinary confirms a candidate backup path points at a regular,
// executable file. It returns the stat record so callers can preserve
// ownership/mode during restore.
func validateBackupBinary(path string) (os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("backup binary path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("backup binary is not readable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("backup binary path is not a regular file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("backup binary %q is not executable", path)
	}
	return info, nil
}

// RestoreBackup atomically overwrites installPath with the contents of
// backupPath, preserving the backup's ownership/permissions and fsync'ing
// the install directory afterwards. The behaviour mirrors install() so
// rollback has the same durability guarantees.
func RestoreBackup(installPath, backupPath string) error {
	if strings.TrimSpace(installPath) == "" || strings.TrimSpace(backupPath) == "" {
		return errors.New("RestoreBackup: both paths must be provided")
	}
	backupInfo, err := validateBackupBinary(backupPath)
	if err != nil {
		return err
	}
	if err := ensureWritableDir(filepath.Dir(installPath)); err != nil {
		return err
	}
	installInfo, err := os.Stat(installPath)
	if err != nil {
		// Allow installing into a path that doesn't yet exist (rare, but
		// possible after a partial failure); fall back to backup mode.
		installInfo = backupInfo
	}
	tempPath := filepath.Join(filepath.Dir(installPath), ".phantom-lancer.rollback.tmp")
	_ = os.Remove(tempPath)
	if err := copyFile(backupPath, tempPath, installInfo.Mode().Perm()|0o111); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("copy backup binary failed: %w", err)
	}
	// Preserve ownership the way the original install path had it.
	if installInfo != nil {
		if err := preserveOwnership(tempPath, installInfo); err != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("preserve restored binary ownership failed: %w", err)
		}
	}
	if err := os.Rename(tempPath, installPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace binary from backup failed: %w", err)
	}
	return fsyncDir(filepath.Dir(installPath))
}
