package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WritePIDFile atomically writes pid to path with mode 0600. The parent
// directory is created with 0700 if it does not exist.
func WritePIDFile(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir pid dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write tmp pid file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename pid file: %w", err)
	}
	return fsyncDir(filepath.Dir(path))
}

// RemovePIDFile removes the PID file, ignoring not-exist errors.
func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pid file: %w", err)
	}
	return nil
}

// readPIDFile reads a PID from a file. Returns 0 with no error if the file
// does not exist. Only package-internal + same-package tests use it.
func readPIDFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("parse pid file: %w", err)
	}
	return pid, nil
}

// fsyncDir flushes a directory entry so that rename/create operations are
// durable on disk. This mirrors the same helper used by the selfupdate
// installer to guarantee consistency after crashes.
func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir for fsync: %w", err)
	}
	defer f.Close()
	return f.Sync()
}
