package selfupdate

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestResolveSupervisorStatusNoEnvNoPidFile(t *testing.T) {
	dir := t.TempDir()
	status := ResolveSupervisorStatus(dir)
	if status.UnderSupervisor {
		t.Fatalf("UnderSupervisor should be false")
	}
	if status.Alive {
		t.Fatalf("Alive should be false")
	}
	if status.PID != 0 {
		t.Fatalf("PID should be 0, got %d", status.PID)
	}
	if status.ChildPID != 0 {
		t.Fatalf("ChildPID should be 0, got %d", status.ChildPID)
	}
}

func TestResolveSupervisorStatusEnvPIDLive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PL_UNDER_SUPERVISOR", "1")
	t.Setenv("PL_SUPERVISOR_PID", strconv.Itoa(os.Getpid()))

	status := ResolveSupervisorStatus(dir)
	if !status.UnderSupervisor {
		t.Fatalf("UnderSupervisor should be true")
	}
	if !status.Alive {
		t.Fatalf("Alive should be true for self PID")
	}
	if status.PID != os.Getpid() {
		t.Fatalf("PID = %d want %d", status.PID, os.Getpid())
	}
	if status.PIDSource != "env" {
		t.Fatalf("PIDSource = %q want env", status.PIDSource)
	}
}

func TestResolveSupervisorStatusEnvPIDDead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PL_UNDER_SUPERVISOR", "1")
	// Pick a PID that is (almost certainly) not running. PID_MAX on darwin
	// is 99999 and on linux at least 2^15-1; a 2^31-ish value won't be
	// alive and Signal(0) returns ESRCH.
	deadPID := 2100000000
	t.Setenv("PL_SUPERVISOR_PID", strconv.Itoa(deadPID))

	status := ResolveSupervisorStatus(dir)
	if !status.UnderSupervisor {
		t.Fatalf("UnderSupervisor should be true")
	}
	if status.Alive {
		t.Fatalf("Alive should be false for dead PID")
	}
	if status.PID != deadPID {
		t.Fatalf("PID = %d want %d", status.PID, deadPID)
	}
	if status.LastError == "" {
		t.Fatalf("LastError should describe the signal failure")
	}
}

func TestResolveSupervisorStatusFallbackPidfile(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	supervisorPath := filepath.Join(runDir, "phantom-supervisor.pid")
	if err := os.WriteFile(supervisorPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	childPath := filepath.Join(runDir, "phantom-lancer.pid")
	childPID := 42424
	if err := os.WriteFile(childPath, []byte(strconv.Itoa(childPID)), 0o600); err != nil {
		t.Fatalf("write child pidfile: %v", err)
	}

	status := ResolveSupervisorStatus(dir)
	if !status.Alive {
		t.Fatalf("Alive should be true via pidfile, error: %s", status.LastError)
	}
	if status.PID != os.Getpid() {
		t.Fatalf("PID = %d want %d", status.PID, os.Getpid())
	}
	if status.PIDSource != "pidfile" {
		t.Fatalf("PIDSource = %q want pidfile", status.PIDSource)
	}
	if status.ChildPID != childPID {
		t.Fatalf("ChildPID = %d want %d", status.ChildPID, childPID)
	}
}

func TestResolveSupervisorStatusCustomPidFileEnvVars(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-alt")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	customSuper := filepath.Join(runDir, "super.pid")
	customChild := filepath.Join(runDir, "child.pid")
	if err := os.WriteFile(customSuper, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("write super: %v", err)
	}
	child := 12345
	if err := os.WriteFile(customChild, []byte(strconv.Itoa(child)), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}

	t.Setenv("PL_SUPERVISOR_PID_FILE", customSuper)
	t.Setenv("PL_CHILD_PID_FILE", customChild)
	status := ResolveSupervisorStatus("/nonexistent/dir")
	if !status.Alive || status.PID != os.Getpid() {
		t.Fatalf("custom env paths not honored: %+v", status)
	}
	if status.ChildPID != child {
		t.Fatalf("ChildPID = %d want %d", status.ChildPID, child)
	}
}

func TestResolveSupervisorStatusSignalZeroSameProcess(t *testing.T) {
	// Explicitly verify that Signal(0) against our own PID succeeds so the
	// tests above are meaningful across all supported unix platforms.
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess(self) failed: %v", err)
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("Signal(0) on self should succeed, got: %v", err)
	}
}

func TestSupervisorStatusSamplerTTLWires(t *testing.T) {
	dir := t.TempDir()
	s := NewSupervisorStatusSampler(500 * time.Millisecond)

	t.Setenv("PL_UNDER_SUPERVISOR", "0")
	t.Setenv("PL_SUPERVISOR_PID", "")
	first := s.Get(dir)

	// Mutate env — the sampler should still return the cached value within TTL.
	t.Setenv("PL_UNDER_SUPERVISOR", "1")
	t.Setenv("PL_SUPERVISOR_PID", strconv.Itoa(os.Getpid()))
	cached := s.Get(dir)
	if cached.UnderSupervisor != first.UnderSupervisor {
		t.Fatalf("cache should suppress re-read: first=%+v cached=%+v", first, cached)
	}

	// Reset forces a fresh probe — now UnderSupervisor should flip.
	s.Reset()
	fresh := s.Get(dir)
	if !fresh.UnderSupervisor {
		t.Fatalf("Reset did not force a fresh probe: %+v", fresh)
	}
	if !fresh.Alive {
		t.Fatalf("fresh probe should see self PID alive")
	}
}

func TestReadIntFileEdgeCases(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if v, err := readIntFile(missing); err == nil || !os.IsNotExist(err) {
		t.Fatalf("read missing: got (%d, %v) want ErrNotExist", v, err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("   \n\t"), 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if v, err := readIntFile(empty); err != nil || v != 0 {
		t.Fatalf("read whitespace-only: got (%d, %v) want (0, nil)", v, err)
	}
	valid := filepath.Join(dir, "v")
	if err := os.WriteFile(valid, []byte("  42424\n"), 0o600); err != nil {
		t.Fatalf("write valid: %v", err)
	}
	if v, err := readIntFile(valid); err != nil || v != 42424 {
		t.Fatalf("read valid: got (%d, %v) want (42424, nil)", v, err)
	}
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("notanumber"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := readIntFile(bad); err == nil {
		t.Fatalf("read bad int should fail")
	}
}

func TestTruncateError(t *testing.T) {
	long := "this is a very very very long error message that definitely exceeds the hundred and twenty character limit we set in the helper function for UI display purposes"
	short := truncateError(&fakeStringError{long}, 120)
	if len([]rune(short)) != 121 { // 120 chars + horizontal ellipsis
		t.Fatalf("truncate rune len = %d want 121, value=%q", len([]rune(short)), short)
	}
	runes := []rune(short)
	if runes[len(runes)-1] != '…' {
		t.Fatalf("missing trailing ellipsis: %q", short)
	}
	if got := truncateError(nil, 5); got != "" {
		t.Fatalf("nil error should be empty, got %q", got)
	}
	shorter := "small"
	if got := truncateError(&fakeStringError{shorter}, 120); got != shorter {
		t.Fatalf("short error preserved: got %q want %q", got, shorter)
	}
}

type fakeStringError struct{ s string }

func (f *fakeStringError) Error() string { return f.s }
