//go:build unix

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLockHeld is returned when the lock file is already held by another process.
var ErrLockHeld = errors.New("supervisor lock file is already held by another process")

// LockFile represents an acquired exclusive advisory lock held via flock(2).
// The lock is released when Release is called or when the owning file
// descriptor is closed (process exit).
type LockFile struct {
	file *os.File
	path string
}

// AcquireLock attempts to create and exclusively lock path. The parent
// directory is created with 0700 if missing. Returns ErrLockHeld when the
// lock is owned by another process.
//
// The lock file is NOT deleted on release because deletion races with the
// open+flock of a concurrent process and can result in two lock-holders on
// different inodes.
func AcquireLock(path string) (*LockFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrLockHeld, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	// Best effort: stamp our PID into the lock file for diagnostics.
	payload := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	if _, werr := f.WriteAt(payload, 0); werr == nil {
		_ = f.Truncate(int64(len(payload)))
	}
	return &LockFile{file: f, path: path}, nil
}

// Release unlocks and closes the lock file. It is safe to call on a nil
// receiver or a receiver that has already been released.
func (l *LockFile) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	// Best-effort unlock: closing the fd releases the lock anyway on Unix.
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}

// Path returns the path of the locked file.
func (l *LockFile) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
