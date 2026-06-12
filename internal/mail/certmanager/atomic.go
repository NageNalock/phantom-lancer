package certmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TestStepFail is a package-level test hook that lets unit tests inject a
// failure at a specific step inside writeAtomicWithPerm.  The default
// value of -1 disables the hook entirely (production behaviour).
//
// Step values (see writeAtomicWithPerm step numbers):
//
//	0 – fail after CreateTemp (the temp file exists on disk but is empty).
//	1 – fail after Write but before Chmod.
//	2 – fail after Chmod but before Sync.
//	3 – fail after Sync but before Close.
//	4 – fail after Close but before Rename (temp is fully written on disk).
//
// Any other value (including -1, the default) disables injection.
//
// After injecting the synthetic failure the value is decremented by 100 so
// the hook fires only once – callers should restore it in a t.Cleanup.
var TestStepFail int = -1

// TestStepWriteRecorder is a package-level test hook invoked AFTER each
// writeAtomicWithPerm step (0..4) completes successfully.  It lets unit
// tests verify that steps execute in the canonical order
// CreateTemp → Write → Chmod → Sync → Close → Rename (steps 0..4)
// regardless of which higher-level function called writeAtomicWithPerm.
//
// Set to nil (the default) to disable.  Callers should restore to nil in
// a t.Cleanup.
var TestStepWriteRecorder func(step int)

// ErrTestInjected is the sentinel returned by writeAtomicWithPerm when
// TestStepFail has fired.  Tests can errors.Is-assert against it.
var ErrTestInjected = errors.New("certmanager: injected test failure")

// writeAtomicWithPerm writes data to path via the canonical 4-step atomic
// sequence used everywhere in certmanager:
//
//  1. os.CreateTemp in the SAME directory as path (so rename is atomic).
//  2. Chmod the temp file to `perm`.  For private keys use 0600; for
//     certs/chain use 0644.
//  3. tmp.Sync() to flush dirty pages to storage.
//  4. os.Rename temp → path.
//
// This is the ONLY file-write primitive used for cert artifacts.  Shelling
// out to cp/mv/chmod is explicitly forbidden by the project's hard
// constraints.  Callers that need to write with 0600 use WriteAtomic0600;
// those that need 0644 use WriteAtomic0644.
func writeAtomicWithPerm(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("certmanager: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("certmanager: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	// ---- test hook: step 0 – fail right after CreateTemp ----
	if TestStepFail == 0 {
		TestStepFail -= 100
		_ = tmp.Close()
		cleanup()
		return ErrTestInjected
	}
	if TestStepWriteRecorder != nil {
		TestStepWriteRecorder(0)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		_ = tmp.Close()
		return fmt.Errorf("certmanager: write temp: %w", err)
	}

	// ---- test hook: step 1 – fail right after Write ----
	if TestStepFail == 1 {
		TestStepFail -= 100
		_ = tmp.Close()
		cleanup()
		return ErrTestInjected
	}
	if TestStepWriteRecorder != nil {
		TestStepWriteRecorder(1)
	}

	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		_ = tmp.Close()
		return fmt.Errorf("certmanager: chmod temp: %w", err)
	}

	// ---- test hook: step 2 – fail right after Chmod ----
	if TestStepFail == 2 {
		TestStepFail -= 100
		_ = tmp.Close()
		cleanup()
		return ErrTestInjected
	}
	if TestStepWriteRecorder != nil {
		TestStepWriteRecorder(2)
	}

	if err := tmp.Sync(); err != nil {
		cleanup()
		_ = tmp.Close()
		return fmt.Errorf("certmanager: fsync temp: %w", err)
	}

	// ---- test hook: step 3 – fail right after Sync ----
	if TestStepFail == 3 {
		TestStepFail -= 100
		_ = tmp.Close()
		cleanup()
		return ErrTestInjected
	}
	if TestStepWriteRecorder != nil {
		TestStepWriteRecorder(3)
	}

	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("certmanager: close temp: %w", err)
	}

	// ---- test hook: step 4 – fail right after Close, before Rename ----
	// This is the interesting case: the temp file is fully written on
	// disk but the rename never happens.  Cleanup must remove it.
	if TestStepFail == 4 {
		TestStepFail -= 100
		cleanup()
		return ErrTestInjected
	}
	if TestStepWriteRecorder != nil {
		TestStepWriteRecorder(4)
	}

	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("certmanager: rename temp: %w", err)
	}
	return nil
}

// WriteAtomic0600 writes a private-key PEM with owner-only read.
func WriteAtomic0600(path string, data []byte) error {
	return writeAtomicWithPerm(path, data, 0o600)
}

// WriteAtomic0644 writes a cert/chain PEM with world-readable perms.
func WriteAtomic0644(path string, data []byte) error {
	return writeAtomicWithPerm(path, data, 0o644)
}

// CopyAtomic copies src to dst using WriteAtomic0644.  Callers that need
// to preserve source permission bits must read src's mode and dispatch to
// WriteAtomic0600 / WriteAtomic0644 themselves.
func CopyAtomic(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("certmanager: copy stat %s: %w", src, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("certmanager: copy read %s: %w", src, err)
	}
	perm := info.Mode().Perm()
	// Treat anything with group/other-read bits as 0644; otherwise 0600.
	if perm&0o044 != 0 {
		if err := WriteAtomic0644(dst, data); err != nil {
			return fmt.Errorf("certmanager: copy write 0644 %s: %w", dst, err)
		}
	} else {
		if err := WriteAtomic0600(dst, data); err != nil {
			return fmt.Errorf("certmanager: copy write 0600 %s: %w", dst, err)
		}
	}
	return nil
}

// HashFile returns the lowercase hex SHA-256 of a file's contents.  Used
// by the pipeline to verify atomic writes landed correctly and to
// fingerprint certs for the drift detector.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("certmanager: hash read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
