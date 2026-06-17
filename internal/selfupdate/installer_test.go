package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneDatabaseBackups(t *testing.T) {
	dir := t.TempDir()
	backupsDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create 5 pre-update DB backups with different mod times.
	backupNames := []string{
		"pre-update-job1.db",
		"pre-update-job2.db",
		"pre-update-job3.db",
		"pre-update-job4.db",
		"pre-update-job5.db",
	}
	for i, name := range backupNames {
		path := filepath.Join(backupsDir, name)
		if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		// Stagger mtimes: job1 is oldest, job5 is newest.
		mt := time.Now().Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	// Also drop some non-pre-update files to make sure we don't touch them.
	otherFiles := []string{
		"manual-backup.db",
		"pre-update-job6.db.tmp",
		"readme.txt",
	}
	for _, name := range otherFiles {
		path := filepath.Join(backupsDir, name)
		if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Prune to keep 3.
	svc := &Service{cfg: Config{DataDir: dir, BackupRetention: 3}}
	svc.pruneDatabaseBackups()

	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	// We should have 3 pre-update DBs + 3 other files = 6 total.
	if len(entries) != 6 {
		t.Errorf("expected 6 remaining files, got %d", len(entries))
		for _, e := range entries {
			t.Logf("  remaining: %s", e.Name())
		}
	}

	// Verify the 2 oldest pre-update backups are gone.
	gone := []string{"pre-update-job1.db", "pre-update-job2.db"}
	for _, name := range gone {
		path := filepath.Join(backupsDir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %s to be pruned", name)
		}
	}

	// Verify the 3 newest pre-update backups are kept.
	kept := []string{"pre-update-job3.db", "pre-update-job4.db", "pre-update-job5.db"}
	for _, name := range kept {
		path := filepath.Join(backupsDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to be kept: %v", name, err)
		}
	}

	// Verify non-pre-update files are untouched.
	for _, name := range otherFiles {
		path := filepath.Join(backupsDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s (non-pre-update) to be untouched: %v", name, err)
		}
	}
}

func TestPruneDatabaseBackupsZeroRetention(t *testing.T) {
	dir := t.TempDir()
	backupsDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupsDir, "pre-update-a.db"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// BackupRetention <= 0 means don't prune.
	svc := &Service{cfg: Config{DataDir: dir, BackupRetention: 0}}
	svc.pruneDatabaseBackups()

	entries, _ := os.ReadDir(backupsDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 file with zero retention (no prune), got %d", len(entries))
	}
}

func TestPruneDatabaseBackupsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	backupsDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	svc := &Service{cfg: Config{DataDir: dir, BackupRetention: 3}}
	svc.pruneDatabaseBackups() // should not panic

	entries, _ := os.ReadDir(backupsDir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}
}

func TestPruneDatabaseBackupsNoDir(t *testing.T) {
	// backups/ dir doesn't exist at all — should be a no-op, not an error.
	dir := t.TempDir()

	svc := &Service{cfg: Config{DataDir: dir, BackupRetention: 3}}
	svc.pruneDatabaseBackups() // should not panic
}
