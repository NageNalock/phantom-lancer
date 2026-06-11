package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/storage"
)

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ---------------------------------------------------------------------------
// Test #0 (existing, preserved): version mismatch marks job failed.
// ---------------------------------------------------------------------------

func TestConfirmBootMarksRestartVersionMismatchFailed(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0",
		TargetVersion:  "v0.2.0",
		Status:         jobStatusRestarting,
		Phase:          phaseRestarting,
		CompletedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.1.0"},
		DownloadTimeout: time.Second,
		RestartTimeout:  30 * time.Second,
	})
	if got := service.ConfirmBoot(ctx); got != "" {
		t.Fatalf("ConfirmBoot() rollback path = %q, want empty on plain mismatch", got)
	}

	got, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.Status != jobStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, jobStatusFailed)
	}
	if got.ErrorMessage == "" {
		t.Fatal("expected version mismatch error message")
	}
	if !strings.Contains(got.ErrorMessage, "version mismatch") && !strings.Contains(got.ErrorMessage, "instead of target") {
		t.Fatalf("error_message = %q, want version mismatch detail", got.ErrorMessage)
	}
	if _, err := store.ActiveSystemUpdateJob(ctx); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("active job err = %v, want ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Test #1: version match marks restarting job as completed.
// ---------------------------------------------------------------------------

func TestConfirmBootMarksRestartVersionMatchCompleted(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0",
		TargetVersion:  "v0.2.0",
		Status:         jobStatusRestarting,
		Phase:          phaseRestarting,
		CompletedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.2.0"},
		DownloadTimeout: time.Second,
	})
	if got := service.ConfirmBoot(ctx); got != "" {
		t.Fatalf("ConfirmBoot() rollback path = %q, want empty on success", got)
	}

	got, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.Status != jobStatusCompleted {
		t.Fatalf("status = %q, want %q", got.Status, jobStatusCompleted)
	}
	if got.Phase != phaseCompleted {
		t.Fatalf("phase = %q, want %q", got.Phase, phaseCompleted)
	}
	if got.CompletedAt == "" {
		t.Fatal("expected completed_at to be populated")
	}
}

// ---------------------------------------------------------------------------
// Test #2: watchdog timeout + version mismatch + valid backup -> auto rollback.
// ---------------------------------------------------------------------------

func TestConfirmBootTimeoutTriggersRollback(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(tmpDir, "db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	installDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// Install binary (current version "new" payload that we'll pretend is bad).
	installPath := filepath.Join(installDir, "phantom-lancer")
	if err := os.WriteFile(installPath, []byte("NEW-BAD-BINARY\n"), 0o755); err != nil {
		t.Fatalf("write install binary: %v", err)
	}
	// Backup binary (older, working version).
	backupDir := filepath.Join(tmpDir, "updates", "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatalf("mkdir backup: %v", err)
	}
	backupPath := filepath.Join(backupDir, "phantom-lancer-v0.1.0-backup")
	backupContent := []byte("OLD-GOOD-BINARY\n")
	if err := os.WriteFile(backupPath, backupContent, 0o755); err != nil {
		t.Fatalf("write backup binary: %v", err)
	}

	const targetVersion = "v0.2.0"
	const currentVersion = "v0.1.0"
	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion:    currentVersion,
		TargetVersion:     targetVersion,
		Status:            jobStatusRestarting,
		Phase:             phaseRestarting,
		InstallBinaryPath: installPath,
		BackupBinaryPath:  backupPath,
		// Force the watchdog window to have already elapsed.
		CompletedAt: time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:             buildinfo.Info{Version: currentVersion},
		InstallBinaryPath: installPath,
		DataDir:           tmpDir,
		DownloadTimeout:   time.Second,
		RestartTimeout:    5 * time.Second, // far smaller than the 24h gap above
	})

	rollbackPath := service.ConfirmBoot(ctx)
	if rollbackPath == "" {
		t.Fatal("ConfirmBoot() rollback path is empty, expected auto-rollback")
	}
	if rollbackPath != installPath {
		t.Fatalf("rollback path = %q, want %q", rollbackPath, installPath)
	}

	// On-disk install binary now contains the backup content.
	gotBytes, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install binary after rollback: %v", err)
	}
	if string(gotBytes) != string(backupContent) {
		t.Fatalf("install content after rollback = %q, want %q", string(gotBytes), string(backupContent))
	}

	got, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.Status != jobStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, jobStatusFailed)
	}
	if !strings.Contains(got.ErrorMessage, "rollback") && !strings.Contains(got.ErrorMessage, "restored automatically") {
		t.Fatalf("error_message = %q, want it to mention rollback / auto-restore", got.ErrorMessage)
	}
}

// ---------------------------------------------------------------------------
// Test #3: Ensure() marks queued/running jobs as failed and leaves restarting
// jobs for ConfirmBoot().
// ---------------------------------------------------------------------------

func TestEnsureInterruptsStaleJobs(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	queued, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0", TargetVersion: "v0.2.0",
		Status: jobStatusQueued, Phase: phaseCreated,
	})
	if err != nil {
		t.Fatalf("create queued job: %v", err)
	}
	running, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0", TargetVersion: "v0.2.0",
		Status: jobStatusRunning, Phase: phaseDownloading,
	})
	if err != nil {
		t.Fatalf("create running job: %v", err)
	}
	restarting, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0", TargetVersion: "v0.2.0",
		Status: jobStatusRestarting, Phase: phaseRestarting,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create restarting job: %v", err)
	}
	// completed is already terminal, Ensure must not touch it.
	done, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0", TargetVersion: "v0.2.0",
		Status: jobStatusCompleted, Phase: phaseCompleted,
	})
	if err != nil {
		t.Fatalf("create completed job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.1.0"},
		DownloadTimeout: time.Second,
	})
	if err := service.Ensure(ctx); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	for _, id := range []string{queued.ID, running.ID} {
		j, err := store.GetSystemUpdateJob(ctx, id)
		if err != nil {
			t.Fatalf("get job %s: %v", id, err)
		}
		if j.Status != jobStatusFailed {
			t.Errorf("job %s status = %q, want %q", id, j.Status, jobStatusFailed)
		}
		if j.ErrorMessage == "" {
			t.Errorf("job %s expected error_message to be set by Ensure interrupt", id)
		}
	}

	if j, err := store.GetSystemUpdateJob(ctx, restarting.ID); err != nil {
		t.Fatalf("get restarting job: %v", err)
	} else if j.Status != jobStatusRestarting {
		t.Errorf("restarting job was mutated by Ensure: status = %q", j.Status)
	}

	if j, err := store.GetSystemUpdateJob(ctx, done.ID); err != nil {
		t.Fatalf("get completed job: %v", err)
	} else if j.Status != jobStatusCompleted {
		t.Errorf("completed job was mutated by Ensure: status = %q", j.Status)
	}
}

// ---------------------------------------------------------------------------
// Test #3b: ConfirmBoot ignores terminal jobs even when the phase still says
// restarting from an older failure/rollback flow.
// ---------------------------------------------------------------------------

func TestConfirmBootIgnoresFailedRestartingPhase(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0",
		TargetVersion:  "v0.2.0",
		Status:         jobStatusFailed,
		Phase:          phaseRestarting,
		ErrorMessage:   "previous failure",
		CompletedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.1.0"},
		DownloadTimeout: time.Second,
		RestartTimeout:  time.Second,
	})
	if got := service.ConfirmBoot(ctx); got != "" {
		t.Fatalf("ConfirmBoot() rollback path = %q, want empty", got)
	}
	got, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.ErrorMessage != "previous failure" {
		t.Fatalf("error_message = %q, want original message preserved", got.ErrorMessage)
	}
}

// ---------------------------------------------------------------------------
// Test #4: failJob (the helper used by the panic fence) records failure.
// ---------------------------------------------------------------------------

func TestFailJobMarksJobFailedWithPanicMessage(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0", TargetVersion: "v0.2.0",
		Status: jobStatusRunning, Phase: phaseDownloading,
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.1.0"},
		DownloadTimeout: time.Second,
	})
	payload := fmt.Sprintf("panic: %v\n\n%s", "something exploded", "goroutine 1 [running]:\nfoo.bar()\n")
	if err := service.failJob(ctx, job.ID, payload); err != nil {
		t.Fatalf("failJob: %v", err)
	}

	got, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != jobStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, jobStatusFailed)
	}
	if !strings.Contains(got.ErrorMessage, "panic:") {
		t.Fatalf("error_message missing panic marker: %q", got.ErrorMessage)
	}
}

// ---------------------------------------------------------------------------
// Test #5: execute() removes the staging directory even on failure.
// ---------------------------------------------------------------------------

func TestExecuteCleansStagingDirOnFailure(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(tmpDir, "db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0", TargetVersion: "v0.2.0",
		ReleaseID: "none", AssetName: "bogus.tar.gz",
		Status:    jobStatusRunning,
		Phase:     phaseDownloading,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.1.0"},
		DataDir:         tmpDir,
		DownloadTimeout: time.Second,
	})

	// A check whose asset URL is empty/invalid — this will fail fast in
	// validateDownloadURL and trigger the defer cleanup.
	badCheck := storage.SystemUpdateCheck{
		LatestVersion: "v0.2.0",
		AssetURL:      "",
		AssetName:     "bogus.tar.gz",
	}

	// Pre-populate staging the way execute() would, so we can assert it
	// existed *before* execute ran.
	stage, err := service.prepareStaging(job.ID)
	if err != nil {
		t.Fatalf("prepareStaging: %v", err)
	}
	if info, err := os.Stat(stage.dir); err != nil || !info.IsDir() {
		t.Fatalf("staging dir should exist before execute: %v", err)
	}
	// Drop a marker file so the directory isn't empty (makes the eventual
	// RemoveAll slightly more representative of the real case).
	marker := filepath.Join(stage.dir, "partial.part")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// execute() always returns an error here because of the bad URL.
	execErr := service.execute(ctx, &job, badCheck)
	if execErr == nil {
		t.Fatal("execute should have failed with a bad URL")
	}

	if _, err := os.Stat(stage.dir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed after execute failure, stat err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test #6: Rollback rejects jobs without a valid backup binary.
// ---------------------------------------------------------------------------

func TestRollbackRequiresBackup(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion:    "v0.1.0",
		TargetVersion:     "v0.2.0",
		Status:            jobStatusFailed,
		Phase:             phaseInstalling,
		BackupBinaryPath:  "", // no backup
		InstallBinaryPath: "/tmp/nope",
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:             buildinfo.Info{Version: "v0.1.0"},
		InstallBinaryPath: "/tmp/nope",
		DataDir:           t.TempDir(),
		DownloadTimeout:   time.Second,
	})

	_, _, err = service.Rollback(ctx, job.ID)
	if err == nil {
		t.Fatal("Rollback should fail when backupBinaryPath is empty")
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Fatalf("error = %q, want backup mentioned", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Test #7: Rollback restores backup bytes onto the install path.
// ---------------------------------------------------------------------------

func TestRollbackRestoresBinary(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(tmpDir, "db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	installDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	installPath := filepath.Join(installDir, "phantom-lancer")
	newContent := []byte("NEW-VERSION\n")
	if err := os.WriteFile(installPath, newContent, 0o755); err != nil {
		t.Fatalf("write install binary: %v", err)
	}

	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	backupPath := filepath.Join(backupDir, "phantom-lancer-v0.1.0")
	oldContent := []byte("OLD-VERSION-RESTORE-ME\n")
	if err := os.WriteFile(backupPath, oldContent, 0o755); err != nil {
		t.Fatalf("write backup binary: %v", err)
	}

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion:    "v0.1.0",
		TargetVersion:     "v0.2.0",
		Status:            jobStatusFailed,
		Phase:             phaseRestarting,
		InstallBinaryPath: installPath,
		BackupBinaryPath:  backupPath,
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:             buildinfo.Info{Version: "v0.1.0"},
		InstallBinaryPath: installPath,
		DataDir:           tmpDir,
		DownloadTimeout:   time.Second,
	})

	result, execPath, err := service.Rollback(ctx, job.ID)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if execPath != installPath {
		t.Fatalf("execPath = %q, want %q", execPath, installPath)
	}
	if result.Status != jobStatusFailed {
		t.Fatalf("result status = %q, want %q", result.Status, jobStatusFailed)
	}
	if !strings.Contains(result.ErrorMessage, "rollback") {
		t.Fatalf("rollback error_message should mention rollback: %q", result.ErrorMessage)
	}

	// Crucially: install binary's bytes are now the backup's contents.
	got, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install binary after rollback: %v", err)
	}
	if string(got) != string(oldContent) {
		t.Fatalf("restored binary content = %q, want %q", string(got), string(oldContent))
	}

	// Re-Rollback must be idempotent and NOT double-append the rollback note.
	_, _, err = service.Rollback(ctx, job.ID)
	if err != nil {
		t.Fatalf("second Rollback should be idempotent, got error: %v", err)
	}
	again, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("re-read job: %v", err)
	}
	if c := strings.Count(again.ErrorMessage, "rollback applied"); c != 1 {
		t.Fatalf("\"rollback applied\" count in error_message = %d, want 1 (idempotency). message=%q", c, again.ErrorMessage)
	}
}

// ---------------------------------------------------------------------------
// Test #8: Rollback reuses the restart dispatch path in self-exec mode.
// ---------------------------------------------------------------------------

func TestRollbackDispatchesRestartForSelfExec(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(tmpDir, "db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	installPath := filepath.Join(tmpDir, "bin", "phantom-lancer")
	if err := os.MkdirAll(filepath.Dir(installPath), 0o700); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(installPath, []byte("NEW-VERSION\n"), 0o755); err != nil {
		t.Fatalf("write install binary: %v", err)
	}
	backupPath := filepath.Join(tmpDir, "backups", "phantom-lancer-v0.1.0")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("OLD-VERSION\n"), 0o755); err != nil {
		t.Fatalf("write backup binary: %v", err)
	}

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion:    "v0.1.0",
		TargetVersion:     "v0.2.0",
		Status:            jobStatusFailed,
		Phase:             phaseRestarting,
		InstallBinaryPath: installPath,
		BackupBinaryPath:  backupPath,
		ErrorMessage:      "new version failed",
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	restarted := make(chan struct{}, 1)
	var preparedPath string
	service := NewService(store, nil, nil, Config{
		Build:             buildinfo.Info{Version: "v0.2.0"},
		InstallBinaryPath: installPath,
		DataDir:           tmpDir,
		DownloadTimeout:   time.Second,
		RestartMode:       RestartModeSelfExec,
		PrepareSelfExec: func(path string) {
			preparedPath = path
		},
		RequestRestart: func() {
			restarted <- struct{}{}
		},
	})

	if _, execPath, err := service.Rollback(ctx, job.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	} else if execPath != installPath {
		t.Fatalf("execPath = %q, want %q", execPath, installPath)
	}
	if preparedPath != installPath {
		t.Fatalf("preparedPath = %q, want %q", preparedPath, installPath)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("rollback did not request restart")
	}
}

// ---------------------------------------------------------------------------
// Test #9: Status() surfaces the restart mode + install/backup paths.
// (Replaces the "self-exec callback invoked" plan item with a test that
// exercises the same public surface, because reaching the install path end
// requires a full round-trip of download→verify→extract.)
// ---------------------------------------------------------------------------

func TestStatusSurfacesRestartModeAndPaths(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	tmpDir := t.TempDir()
	installPath := filepath.Join(tmpDir, "bin", "phantom-lancer")
	if err := os.MkdirAll(filepath.Dir(installPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(installPath, []byte("bin"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}

	backupPath := filepath.Join(tmpDir, "backups", "old")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		t.Fatalf("mkdir backup: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("bak"), 0o755); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	_, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion:    "v0.1.0",
		TargetVersion:     "v0.2.0",
		Status:            jobStatusCompleted,
		Phase:             phaseCompleted,
		BackupBinaryPath:  backupPath,
		InstallBinaryPath: installPath,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Enabled:           true,
		RestartMode:       RestartModeSelfExec,
		Build:             buildinfo.Info{Version: "v0.1.0"},
		InstallBinaryPath: installPath,
		DataDir:           tmpDir,
		DownloadTimeout:   time.Second,
		RestartTimeout:    15 * time.Second,
	})

	st := service.Status(ctx)
	if st.RestartMode != RestartModeSelfExec {
		t.Errorf("RestartMode = %q, want %q", st.RestartMode, RestartModeSelfExec)
	}
	if st.InstallBinaryPath != installPath {
		t.Errorf("InstallBinaryPath = %q, want %q", st.InstallBinaryPath, installPath)
	}
	if st.BackupBinaryPath != backupPath {
		t.Errorf("BackupBinaryPath = %q, want %q", st.BackupBinaryPath, backupPath)
	}
	if st.RestartTimeoutSeconds != 15 {
		t.Errorf("RestartTimeoutSeconds = %d, want 15", st.RestartTimeoutSeconds)
	}
}
