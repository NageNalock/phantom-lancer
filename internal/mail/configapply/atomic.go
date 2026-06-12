package configapply

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path via a temp file in the same directory,
// chmod 0600, fsync, rename. Mirrors mail/actions.go:atomicWrite0600.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("configapply: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("configapply: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		_ = tmp.Close()
		return fmt.Errorf("configapply: write temp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		_ = tmp.Close()
		return fmt.Errorf("configapply: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		_ = tmp.Close()
		return fmt.Errorf("configapply: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("configapply: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("configapply: rename temp: %w", err)
	}
	return nil
}

// CopyAtomic copies src to dst using the WriteAtomic sequence.
func CopyAtomic(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("configapply: copy read %s: %w", src, err)
	}
	if err := WriteAtomic(dst, data); err != nil {
		return fmt.Errorf("configapply: copy write %s: %w", dst, err)
	}
	return nil
}
