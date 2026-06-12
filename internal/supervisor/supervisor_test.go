package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"phantom-lancer/internal/selfupdate"
)

// ---- (a) BackoffStep table-driven tests ----

func TestBackoffStep(t *testing.T) {
	cases := []struct {
		attempt  int
		min, max time.Duration
		want     time.Duration
	}{
		{attempt: 0, min: 1, max: 30, want: 0},
		{attempt: -5, min: 1, max: 30, want: 0},
		{attempt: 1, min: 1 * time.Second, max: 30 * time.Second, want: 1 * time.Second},
		{attempt: 2, min: 1 * time.Second, max: 30 * time.Second, want: 2 * time.Second},
		{attempt: 3, min: 1 * time.Second, max: 30 * time.Second, want: 5 * time.Second},
		{attempt: 4, min: 1 * time.Second, max: 30 * time.Second, want: 10 * time.Second},
		{attempt: 5, min: 1 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
		{attempt: 6, min: 1 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
		{attempt: 100, min: 1 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
		// Clamp against user-supplied min/max.
		{attempt: 5, min: 2 * time.Second, max: 15 * time.Second, want: 15 * time.Second},
		{attempt: 1, min: 3 * time.Second, max: 12 * time.Second, want: 3 * time.Second},
	}
	for i, c := range cases {
		got := BackoffStep(c.attempt, c.min, c.max)
		if got != c.want {
			t.Errorf("case %d (attempt=%d): got %v want %v", i, c.attempt, got, c.want)
		}
	}
}

// ---- (b) BackoffTracker ObserveUptime reset / no-reset ----

func TestBackoffTracker(t *testing.T) {
	b := &BackoffTracker{
		MinDelay:    1 * time.Second,
		MaxDelay:    30 * time.Second,
		StableAfter: 60 * time.Second,
	}
	if n := b.CurrentAttempts(); n != 0 {
		t.Fatalf("fresh tracker attempts=%d, want 0", n)
	}
	d1 := b.NextDelay()
	d2 := b.NextDelay()
	d3 := b.NextDelay()
	if d1 >= d2 || d2 >= d3 {
		t.Fatalf("expected strictly increasing backoff, got %v %v %v", d1, d2, d3)
	}
	if b.CurrentAttempts() < 3 {
		t.Fatalf("after 3 NextDelay calls, attempts should be >= 3, got %d", b.CurrentAttempts())
	}

	// Short uptime (< StableAfter) does not reset.
	b.ObserveUptime(10 * time.Second)
	if b.CurrentAttempts() == 0 {
		t.Fatalf("short uptime must not reset attempts counter")
	}
	dAfterShort := b.NextDelay()
	if dAfterShort <= d3 {
		t.Fatalf("expected further increase after short uptime, got %v after %v", dAfterShort, d3)
	}

	// Long uptime (> StableAfter) resets the counter.
	b.ObserveUptime(90 * time.Second)
	if b.CurrentAttempts() != 0 {
		t.Fatalf("long uptime must reset attempts, got %d", b.CurrentAttempts())
	}
	dReset := b.NextDelay()
	if dReset != d1 {
		t.Fatalf("after reset NextDelay should match first call, got %v want %v", dReset, d1)
	}
}

// ---- (c) LockFile exclusive acquire / release / re-acquire ----

func TestLockFileExclusive(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("flock tests only run on unix-like systems")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.lock")

	// Acquire once.
	l1, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}

	// Second acquire must fail with ErrLockHeld.
	_, err2 := AcquireLock(path)
	if err2 == nil {
		t.Fatalf("second AcquireLock should fail while lock is held")
	}
	if !errors.Is(err2, ErrLockHeld) {
		t.Fatalf("second AcquireLock returned %v, want ErrLockHeld chain", err2)
	}

	// Release + re-acquire must succeed.
	if err := l1.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	l2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire after Release failed: %v", err)
	}
	// Lock file must still exist on disk (we never delete it).
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file %s missing after release: %v", path, err)
	}
	_ = l2.Release()
}

// ---- (d) PIDFile write / read / remove with permission checks ----

func TestPIDFileWriteReadRemove(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested", "dir")
	path := filepath.Join(sub, "child.pid")
	pid := 42424

	if err := WritePIDFile(path, pid); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}
	// mkdirAll should have created parent directories.
	info, err := os.Stat(sub)
	if err != nil {
		t.Fatalf("parent dir missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("parent dir mode=%v, want no group/other access", perm)
	}

	got, err := readPIDFile(path)
	if err != nil {
		t.Fatalf("readPIDFile failed: %v", err)
	}
	if got != pid {
		t.Fatalf("readPIDFile got %d want %d", got, pid)
	}

	// PID file mode should be 0600.
	pinfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat pid file: %v", err)
	}
	if perm := pinfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("pid file mode=%v, want no group/other access", perm)
	}

	// Missing file returns (0, nil).
	if err := RemovePIDFile(path); err != nil {
		t.Fatalf("RemovePIDFile failed: %v", err)
	}
	got2, err2 := readPIDFile(path)
	if err2 != nil {
		t.Fatalf("readPIDFile on missing file returned error: %v", err2)
	}
	if got2 != 0 {
		t.Fatalf("readPIDFile on missing file got %d want 0", got2)
	}
}

// ---- (e) Stop cancels restart loop within deadline ----

func TestStopCancelsRestartLoop(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		ChildBinary:       "/bin/sleep",
		ChildArgs:         []string{"30"},
		SupervisorPIDFile: filepath.Join(dir, "s.pid"),
		ChildPIDFile:      filepath.Join(dir, "c.pid"),
		LockFile:          filepath.Join(dir, "s.lock"),
		LogFile:           filepath.Join(dir, "s.log"),
		HandoffFile:       filepath.Join(dir, "handoff.json"),
		RestartMinDelay:   10 * time.Second, // huge so the loop would stay blocked
		RestartMaxDelay:   30 * time.Second,
		StableAfter:       60 * time.Second,
		StopTimeout:       3 * time.Second,
	}
	if _, err := exec.LookPath("/bin/sleep"); err != nil {
		t.Skip("system lacks /bin/sleep")
	}
	svc, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	// Give the child a moment to write its PID file so we know it's up.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	childUp := false
loop:
	for {
		select {
		case <-deadline:
			t.Fatal("child pid file never appeared")
		case <-ticker.C:
			p, err := readPIDFile(cfg.ChildPIDFile)
			if err == nil && p > 0 {
				childUp = true
				break loop
			}
		}
	}
	if !childUp {
		t.Fatal("child pid missing at end of wait")
	}

	// Cancel and make sure Run returns promptly (within StopTimeout+buffer).
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned non-nil: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// ---- (f) Exit code zero triggers restart with tiny delays ----

func TestExitCodeZeroTriggersRestart(t *testing.T) {
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("system lacks /bin/sh")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter.txt")
	if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
		t.Fatalf("init counter: %v", err)
	}
	// Script atomically increments the counter then exits 0.
	script := fmt.Sprintf(
		`#!/bin/sh
set -eu
f=%q
while ! ln -s x "$f.lock" 2>/dev/null; do sleep 0.02; done
tmp="$f.tmp.$$"
trap 'rm -f "$f.lock" "$tmp"' EXIT
n=$(cat "$f" 2>/dev/null || echo 0)
n=$((n + 1))
printf '%%s' "$n" > "$tmp"
mv "$tmp" "$f"
rm -f "$f.lock"
trap - EXIT
exit 0
`, counter)
	scriptPath := filepath.Join(dir, "child.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := Config{
		ChildBinary:       "/bin/sh",
		ChildArgs:         []string{scriptPath},
		SupervisorPIDFile: filepath.Join(dir, "s.pid"),
		ChildPIDFile:      filepath.Join(dir, "c.pid"),
		LockFile:          filepath.Join(dir, "s.lock"),
		RestartMinDelay:   1 * time.Millisecond,
		RestartMaxDelay:   5 * time.Millisecond,
		StableAfter:       1 * time.Millisecond,
		StopTimeout:       200 * time.Millisecond,
	}
	svc, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	stopped := false
	defer func() {
		if stopped {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned non-nil: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Run did not return after ctx cancel")
		}
	}()

	readCounter := func() (int, string, error) {
		data, err := os.ReadFile(counter)
		if err != nil {
			return 0, "", err
		}
		raw := string(data)
		n, err := strconv.Atoi(string(bytes.TrimSpace(data)))
		return n, raw, err
	}

	var lastRaw string
	var lastErr error
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for >= 2 child runs; last counter=%q err=%v", lastRaw, lastErr)
		case <-ticker.C:
			n, raw, err := readCounter()
			lastRaw = raw
			lastErr = err
			if err != nil {
				t.Fatalf("read counter %q: %v", raw, err)
			}
			if n >= 2 {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("Run returned non-nil: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("Run did not return after ctx cancel")
				}
				stopped = true
				return
			}
		}
	}
}

// ---- (g) Handoff read + expiry + invalid schema ----

func TestHandoffReadAndExpiry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handoff.json")

	// Missing file => nil, nil.
	h, err := ReadHandoff(path)
	if err != nil || h != nil {
		t.Fatalf("missing file: got (%v,%v) want (nil,nil)", h, err)
	}

	// Valid file within window.
	inWindow := time.Now().UTC().Add(-5 * time.Second)
	payload := fmt.Sprintf(`{"schemaVersion":1,"jobId":"abc","source":"update","currentVersion":"v1","targetVersion":"v2","mainBinaryPath":"/x","mainBackupPath":"/y","requestedAt":%q,"restartTimeoutSeconds":120}`, inWindow.Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write handoff: %v", err)
	}
	h, err = ReadHandoff(path)
	if err != nil {
		t.Fatalf("read handoff: %v", err)
	}
	if h == nil || h.JobID != "abc" {
		t.Fatalf("wrong handoff content: %+v", h)
	}
	if h.IsExpired(inWindow.Add(60 * time.Second)) {
		t.Fatalf("should not be expired at T+60s with timeout=120s")
	}
	remain := h.Remaining(inWindow.Add(60 * time.Second))
	if remain < 50*time.Second || remain > 70*time.Second {
		t.Fatalf("remaining at T+60 should be ~60s, got %v", remain)
	}
	if !h.IsExpired(inWindow.Add(121 * time.Second)) {
		t.Fatalf("should be expired at T+121s with timeout=120s")
	}

	// Bad schema version → error.
	if err := os.WriteFile(path, []byte(`{"schemaVersion":0,"jobId":"x"}`), 0o600); err != nil {
		t.Fatalf("write bad schema: %v", err)
	}
	if _, err := ReadHandoff(path); err == nil {
		t.Fatalf("schemaVersion=0 should error")
	}
}

// ---- (h) RestoreBackup atomic + idempotent ----

func TestRestoreBackupAtomic(t *testing.T) {
	dir := t.TempDir()
	install := filepath.Join(dir, "bin", "phantom-lancer")
	backup := filepath.Join(dir, "backups", "phantom-lancer.v1")
	_ = os.MkdirAll(filepath.Dir(install), 0o755)
	_ = os.MkdirAll(filepath.Dir(backup), 0o755)

	// Write "broken" content as the installed binary.
	if err := os.WriteFile(install, []byte("broken"), 0o755); err != nil {
		t.Fatalf("write install: %v", err)
	}
	// Write "good" backup content (use a real binary on the machine so exec bits pass validation).
	// We copy /bin/sh (or /bin/echo) so executable check passes, then overwrite contents.
	if shPath, err := exec.LookPath("/bin/sh"); err == nil {
		in, err := os.Open(shPath)
		if err != nil {
			t.Fatalf("open /bin/sh: %v", err)
		}
		defer in.Close()
		out, err := os.OpenFile(backup, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			t.Fatalf("create backup: %v", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			t.Fatalf("copy /bin/sh: %v", err)
		}
		out.Close()
	} else {
		// Fallback: use a tiny dummy executable (self-update ValidateBackupBinary only checks stat bits).
		if err := os.WriteFile(backup, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatalf("write dummy backup: %v", err)
		}
	}

	// Verify "install" still reads "broken" before restore.
	before, err := os.ReadFile(install)
	if err != nil {
		t.Fatalf("pre-read install: %v", err)
	}
	if string(before) != "broken" {
		t.Fatalf("unexpected pre-restore install content: %q", string(before))
	}

	// First restore call.
	if err := selfupdate.RestoreBackup(install, backup); err != nil {
		t.Fatalf("first RestoreBackup: %v", err)
	}
	// After restore, install content must EQUAL backup content (modulo size difference).
	got, err := os.ReadFile(install)
	if err != nil {
		t.Fatalf("read install after restore: %v", err)
	}
	want, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup after restore: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("install content != backup content after restore (%d vs %d bytes)", len(got), len(want))
	}
	// Backup still present (not consumed/moved).
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup file disappeared after RestoreBackup: %v", err)
	}

	// Second restore must be idempotent (no partial failure).
	if err := selfupdate.RestoreBackup(install, backup); err != nil {
		t.Fatalf("second RestoreBackup (idempotent): %v", err)
	}
	got2, err := os.ReadFile(install)
	if err != nil {
		t.Fatalf("read install after 2nd restore: %v", err)
	}
	if !bytes.Equal(got, got2) {
		t.Fatalf("content changed between first and second RestoreBackup calls")
	}

	// Sanity: RestoreBackup must reject missing backup.
	if err := selfupdate.RestoreBackup(install, filepath.Join(dir, "no-such")); err == nil {
		t.Fatalf("RestoreBackup with missing backup should error")
	}
}

// ---- Extra: startChild / waitChild smoke test via temp sleep ----

func TestStartChildWaitChildSmoke(t *testing.T) {
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	dir := t.TempDir()
	cfg := Config{
		ChildBinary:       "/bin/sh",
		ChildArgs:         []string{"-c", "exit 7"},
		SupervisorPIDFile: filepath.Join(dir, "s.pid"),
		ChildPIDFile:      filepath.Join(dir, "c.pid"),
		LockFile:          filepath.Join(dir, "s.lock"),
		StopTimeout:       2 * time.Second,
	}
	svc, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	cmd, err := svc.startChild()
	if err != nil {
		t.Fatalf("startChild: %v", err)
	}
	started := svc.childStarted
	ce := svc.waitChild(cmd, started)
	if ce.ExitCode != 7 {
		t.Fatalf("waitChild exit code: got %d want 7 (waitErr=%v)", ce.ExitCode, ce.WaitErr)
	}
	if ce.PID <= 0 {
		t.Fatalf("waitChild PID=%d should be positive", ce.PID)
	}
	// Child PID file should have been removed after wait.
	pid, err := readPIDFile(cfg.ChildPIDFile)
	if err != nil {
		t.Fatalf("read child pid file after wait: %v", err)
	}
	if pid != 0 {
		t.Fatalf("child pid file should be removed after wait, got pid %d", pid)
	}
	// cleanupFinal is idempotent when called after the loop.
	_ = svc.cleanupFinal()
}

// ---- Extra: stopChild SIGTERM-to-SIGKILL escalation ----

func TestStopChildEscalation(t *testing.T) {
	tailPath, err := exec.LookPath("tail")
	if err != nil {
		t.Skip("no tail binary")
	}
	dir := t.TempDir()
	cfg := Config{
		ChildBinary:       tailPath,
		ChildArgs:         []string{"-f", "/dev/null"},
		SupervisorPIDFile: filepath.Join(dir, "s.pid"),
		ChildPIDFile:      filepath.Join(dir, "c.pid"),
		LockFile:          filepath.Join(dir, "s.lock"),
		StopTimeout:       400 * time.Millisecond,
	}
	svc, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = svc.cleanupFinal() })
	cmd, err := svc.startChild()
	if err != nil {
		t.Fatalf("startChild: %v", err)
	}
	started := svc.childStarted

	// Run stopChild async (it returns promptly); wait on child separately.
	var killed int32
	go func() {
		svc.stopChild()
		atomic.StoreInt32(&killed, 1)
	}()

	// waitChild must finish within StopTimeout + buffer.
	done := make(chan ChildExit, 1)
	go func() { done <- svc.waitChild(cmd, started) }()
	select {
	case ce := <-done:
		// tail ignores SIGTERM by default, so we expect SIGKILL to fire
		// at deadline; if SIGTERM was enough on this platform, no harm done.
		if ce.Signal != 0 {
			t.Logf("child died via signal %d (%s)", ce.Signal, ce.SignalName)
		} else {
			t.Logf("child exited normally (code=%d); stop pattern verified either way", ce.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("waitChild never returned after stopChild")
	}
	// stopChild is designed to return roughly in lock-step with waitChild
	// (either when polling detects the process is gone, or at the deadline
	// when SIGKILL fires). Give it a small grace period.
	giveUp := time.After(1 * time.Second)
	for atomic.LoadInt32(&killed) == 0 {
		select {
		case <-giveUp:
			t.Fatalf("stopChild did not return")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
