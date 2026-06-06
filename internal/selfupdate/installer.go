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
	"time"
)

type installResult struct {
	installPath string
	backupPath  string
}

func verifyStagedVersion(ctx context.Context, binaryPath, targetVersion string) error {
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("staged binary version check failed: %w", err)
	}
	if !strings.Contains(string(output), targetVersion) {
		return fmt.Errorf("staged binary version does not match target %s", targetVersion)
	}
	return nil
}

func (s *Service) install(ctx context.Context, jobID, stagedBinary, currentVersion string) (installResult, error) {
	installPath, err := s.installPath()
	if err != nil {
		return installResult{}, err
	}
	if err := ensureWritableDir(filepath.Dir(installPath)); err != nil {
		return installResult{}, err
	}
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "backups"), 0o700); err != nil {
		return installResult{}, err
	}
	dbBackup := filepath.Join(s.cfg.DataDir, "backups", "pre-update-"+jobID+".db")
	if err := s.store.BackupDatabase(ctx, dbBackup); err != nil {
		return installResult{}, fmt.Errorf("database backup failed: %w", err)
	}

	backupDir := filepath.Join(s.cfg.DataDir, "updates", "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return installResult{}, err
	}
	backupPath := filepath.Join(backupDir, "phantom-lancer-"+safeName(currentVersion)+"-"+time.Now().UTC().Format("20060102T150405Z"))
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

	tempPath := filepath.Join(filepath.Dir(installPath), ".phantom-lancer."+jobID+".tmp")
	if err := copyFile(stagedBinary, tempPath, currentInfo.Mode().Perm()); err != nil {
		_ = os.Remove(tempPath)
		return installResult{}, fmt.Errorf("copy staged binary failed: %w", err)
	}
	if err := os.Rename(tempPath, installPath); err != nil {
		_ = os.Remove(tempPath)
		return installResult{}, fmt.Errorf("replace binary failed: %w", err)
	}
	if err := fsyncDir(filepath.Dir(installPath)); err != nil {
		return installResult{}, err
	}
	s.pruneBackups(backupDir)
	return installResult{installPath: installPath, backupPath: backupPath}, nil
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
