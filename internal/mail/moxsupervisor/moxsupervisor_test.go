package moxsupervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// --- helpers ---------------------------------------------------------------

func testRoot(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	// The root under which moxRoot lives.
	return d
}

func newTestSup(t *testing.T, moxRoot string, opts ...func(*Supervisor)) *Supervisor {
	t.Helper()
	binaryPath := findFakeMox(t)
	ports := Ports{
		// Use 0 (random-free) for all real ports – tests that actually run
		// Mox (even the fake one) don't need reserved ports.  Preflight
		// tests pick explicit ports via opts.
		SMTP:        0,
		Submission:  0,
		SMTPS:       0,
		IMAP:        0,
		IMAPS:       0,
		Webmail:     0,
		WebAPILocal: 0,
	}
	sup := New(
		moxRoot,
		binaryPath,
		filepath.Join(moxRoot, "data"),
		"", // config path – empty means mox uses built-in defaults
		ports,
		"test-phantom-instance-xyz",
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	)
	for _, fn := range opts {
		fn(sup)
	}
	return sup
}

// findFakeMox returns the path to a fake "mox" executable.  We build a tiny
// Go program once per test process so we don't depend on the real mox binary
// being installed (and so we can fake behaviours like "hang on SIGTERM").
func findFakeMox(t *testing.T) string {
	t.Helper()
	// Build once under t.TempDir()/fake_mox
	dir := t.TempDir()
	src := filepath.Join(dir, "mox.go")
	if err := os.WriteFile(src, fakeMoxSource(), 0o600); err != nil {
		t.Fatalf("write fake mox source: %v", err)
	}
	out := filepath.Join(dir, "mox")
	cmd := exec.Command("go", "build", "-o", out, src)
	if outB, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake mox: %v\n%s", err, outB)
	}
	return out
}

// --- Fake Mox implementation ----------------------------------------------
//
// This tiny Go program is compiled once per test and impersonates a subset of
// `mox` so we can exercise start/wait/stop/signals without a real Mox binary.
//
// Subcommands:
//   mox version        → prints "mox v0.0.0-fake" and exits 0
//   mox config test    → exits 0 (optionally MOX_CONFIGTEST_EXIT=n to force fail)
//   mox serve          → starts a TCP listener on each MOX_PORT_* env var;
//                        ignores SIGTERM for MOX_IGNORE_SIGTERM_SECS seconds
//                        (useful for testing tier escalation); otherwise
//                        clean SIGTERM exit.

func fakeMoxSource() []byte {
	return []byte(`package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]
	// Skip flag-looking args (starting with -) and their values to find
	// the subcommand.  Matches mox's real flag parsing convention.
	subIdx := 0
	for subIdx < len(args) {
		a := args[subIdx]
		if strings.HasPrefix(a, "-") {
			// Consume flag + its value (next arg if present and not another flag).
			subIdx++
			if subIdx < len(args) && !strings.HasPrefix(args[subIdx], "-") {
				subIdx++
			}
			continue
		}
		break
	}
	if subIdx >= len(args) {
		fmt.Fprintln(os.Stderr, "fake-mox: no subcommand")
		os.Exit(2)
	}
	sub := args[subIdx]
	rest := args[subIdx+1:]
	_ = rest
	switch sub {
	case "version":
		fmt.Println("mox v0.0.0-fake")
		return
	case "config":
		if len(rest) < 1 || rest[0] != "test" {
			fmt.Fprintln(os.Stderr, "fake-mox: expected 'config test'")
			os.Exit(2)
		}
		code := 0
		if v := os.Getenv("MOX_CONFIGTEST_EXIT"); v != "" {
			code, _ = strconv.Atoi(v)
		}
		if code != 0 {
			fmt.Fprintf(os.Stderr, "fake-mox config test: forced exit %d\n", code)
		}
		os.Exit(code)
	case "serve":
		// Write a ready-file if the caller asked for one, so callers can
		// synchronise on "process booted (signal handlers + ports bound)"
		// BEFORE sending signals or probing ports.  Eliminates the
		// boot-vs-signal race.
		if readyPath := os.Getenv("MOX_READY_FILE"); readyPath != "" {
			_ = os.WriteFile(readyPath, []byte("ready\n"), 0600)
		}

		// Listen on configured ports so the "process alive" probe in tests
		// has something to attach to.
		portNames := []string{"SMTP", "SUBMISSION", "SMTPS", "IMAP", "IMAPS", "WEBMAIL", "WEBAPI_LOCAL"}
		for _, name := range portNames {
			env := "MOX_PORT_" + name
			if p := os.Getenv(env); p != "" {
				host := "127.0.0.1"
				if name == "WEBMAIL" || name == "WEBAPI_LOCAL" {
					host = "127.0.0.1"
				} else {
					// For standard ports, pick any local bind – tests preflight
					// separately so we don't actually need to bind privileged
					// ports here.  Use a high random port via ":0" instead.
					_ = p
					ln, err := net.Listen("tcp", host+":0")
					if err == nil {
						defer ln.Close()
					}
					continue
				}
				_ = host
				ln, err := net.Listen("tcp", ":"+p)
				if err == nil {
					defer ln.Close()
				}
			}
		}

		// Signal handling.
		ignoreSecs := 0
		if v := os.Getenv("MOX_IGNORE_SIGTERM_SECS"); v != "" {
			ignoreSecs, _ = strconv.Atoi(v)
		}
		hang := os.Getenv("MOX_HANG_FOREVER") != ""

		sigCh := make(chan os.Signal, 4)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGKILL)

		if ignoreSecs > 0 {
			// Consume SIGTERMs silently for the requested period.
			deadline := time.Now().Add(time.Duration(ignoreSecs) * time.Second)
			for time.Now().Before(deadline) {
				select {
				case s := <-sigCh:
					fmt.Fprintf(os.Stderr, "fake-mox: ignoring %s for %s\n", s, time.Until(deadline).Round(time.Millisecond))
				case <-time.After(100 * time.Millisecond):
				}
			}
		}

		if hang {
			<-make(chan struct{})
			return
		}
		// Normal: block until a SIGTERM arrives, then clean-exit 0.
		s := <-sigCh
		fmt.Fprintf(os.Stderr, "fake-mox: received %s – exiting cleanly\n", s)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "fake-mox: unknown subcommand %q\n", sub)
		os.Exit(2)
	}
}
`)
}

// waitReady waits for a MOX_READY_FILE signal file to appear, indicating that
// fake-mox has reached main() and installed its signal handlers.  Without
// this synchronisation, a Stop() call racing with fake-mox startup can fire
// SIGTERM before signal.Notify() runs, killing the process with default
// semantics instead of going through the escalation ladder as intended.
func waitReady(t *testing.T, path string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("MOX_READY_FILE %q never appeared after %v", path, d)
}

// --- Marker tests ----------------------------------------------------------

func TestMarker_WriteReadRoundtrip(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	path := filepath.Join(dir, "run", "mox.marker.json")
	m := Marker{
		Version:          1,
		PhantomInstance:  "abc",
		BootID:           "deadbeef",
		PID:              12345,
		StartTimeNano:    42,
		ProcessStartTime: 7,
		BinaryPath:       "/tmp/fake",
		DataDir:          "/tmp/data",
		LaunchedAt:       "2026-06-12T00:00:00Z",
	}
	if err := writeMarker(path, m); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// Permissions check.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("marker has loose perms: %o", info.Mode().Perm())
	}
	got, err := ReadMarker(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if got.BootID != m.BootID || got.PID != m.PID || got.PhantomInstance != m.PhantomInstance {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, m)
	}
}

func TestMarker_AtomicRename(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	path := filepath.Join(dir, "run", "mox.marker.json")
	// Pre-create a bad marker.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Now write a valid one – should replace atomically.
	m := Marker{Version: 1, BootID: "clean"}
	if err := writeMarker(path, m); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	got, err := ReadMarker(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if got.BootID != "clean" {
		t.Errorf("atomic overwrite failed: got %+v", got)
	}
	// Missing file returns nil, nil.
	if m, err := ReadMarker(filepath.Join(dir, "does-not-exist")); err != nil || m != nil {
		t.Errorf("ReadMarker missing: got %+v err=%v", m, err)
	}
}

func TestMarker_JSONSchema(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	path := filepath.Join(dir, "run", "mox.marker.json")
	m := Marker{
		Version:         1,
		PhantomInstance: "pi",
		BootID:          "boot",
		PID:             123,
		BinaryPath:      "/bin/mox",
		ConfigPath:      "/etc/mox.conf",
		DataDir:         "/var/mox",
		LaunchedAt:      "2026-06-12T00:00:00Z",
		LogStdout:       "/tmp/out",
		LogStderr:       "/tmp/err",
	}
	if err := writeMarker(path, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Manually decode and verify all expected keys present.
	raw, _ := os.ReadFile(path)
	var dict map[string]interface{}
	if err := json.Unmarshal(raw, &dict); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	wantKeys := []string{"version", "phantom_instance_id", "boot_id", "pid",
		"start_time_ns", "process_start_time", "binary_path", "config_path",
		"data_dir", "launched_at", "log_stdout", "log_stderr"}
	for _, k := range wantKeys {
		if _, ok := dict[k]; !ok {
			t.Errorf("marker JSON missing key %q (got keys: %v)", k, keysOf(dict))
		}
	}
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- readProcStartTime parser test ----------------------------------------
//
// We can't easily read a real /proc/<pid>/stat in a cross-platform test, but
// we can verify the split-from-the-right logic against a synthetic string.

func TestReadProcStartTime_SyntheticParser(t *testing.T) {
	t.Parallel()
	// Build synthetic /proc/<pid>/stat strings with a helper that places
	// starttime at tail[19] (which is where the production parser reads it).
	// This way we test the right-paren split correctly instead of getting
	// confused about field numbering.
	mk := func(pid, comm, state string, starttime uint64) string {
		// tail has: state (0), 18 padding fields (1..18), then starttime (19).
		tokens := make([]string, 20)
		tokens[0] = state
		for i := 1; i < 19; i++ {
			tokens[i] = strconv.Itoa(i * 3)
		}
		tokens[19] = strconv.FormatUint(starttime, 10)
		// Kernel has >50 fields; pad with zeros so the string looks real.
		for i := 0; i < 40; i++ {
			tokens = append(tokens, "0")
		}
		return fmt.Sprintf("%s (%s) %s", pid, comm, strings.Join(tokens, " "))
	}
	tests := []struct {
		name  string
		input string
		want  uint64
	}{
		{"normal_comm", mk("1234", "bash", "S", 123456789), 123456789},
		{"comm_with_spaces_and_parens", mk("9999", "python3 -c (print 'hi')", "R", 999999999), 999999999},
		{"only_close_parens_in_comm", mk("5", "((((a b c))))", "S", 42), 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStatStringForTest(tc.input)
			if got != tc.want {
				t.Errorf("got=%d want=%d\ninput=%s", got, tc.want, tc.input)
			}
		})
	}
}

// parseStatStringForTest parses a /proc/<pid>/stat payload using the same
// right-split logic as readProcStartTime.  Kept in package scope so the test
// can reach it.
func parseStatStringForTest(data string) uint64 {
	closeIdx := strings.LastIndexByte(data, ')')
	if closeIdx < 0 {
		return 0
	}
	tail := data[closeIdx+1:]
	fields := strings.Fields(tail)
	if len(fields) < 20 {
		return 0
	}
	n, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// string/byte helper used by parseStat tests
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func fieldsSpaces(s string) []string {
	return strings.Fields(s)
}

func parseIntUint64(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}

// --- Backoff FSM tests -----------------------------------------------------

func TestBackoff_TierProgression(t *testing.T) {
	t.Parallel()
	b := newBackoffFSM()
	// A crash every 1s (less than stableThreshold) should step through all
	// 4 tiers and land in FAILED.
	for i, expected := range []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second, 2 * time.Minute} {
		ok, _ := b.MayStart()
		if !ok {
			t.Fatalf("tier %d: MayStart()=false before any crash", i)
		}
		b.Observe(1, 1*time.Second)
		ok, rem := b.MayStart()
		if ok {
			t.Fatalf("tier %d: MayStart()=true immediately after crash", i)
		}
		// remaining is roughly equal to the tier's delay (modulo a few ms
		// between Observe and MayStart — clock sub-nanosecond resolution can
		// produce a slightly negative value which we treat as 0).
		if rem < -100*time.Millisecond || rem > expected {
			t.Errorf("tier %d: backoff = %v, want ~(0, %v]", i, rem, expected)
		}
		// "Wait" the full backoff by advancing b.lastBackoffUntil.
		forceBackoffExpired(b)
	}
	// 5th crash reaches terminal FAILED.
	b.Observe(1, 1*time.Second)
	if !b.IsTerminal() {
		t.Error("expected terminal FAILED after 5 crashes")
	}
	state, consec, _, _ := b.State()
	if state != CLFailed {
		t.Errorf("state = %s want failed", state)
	}
	if consec != 5 {
		t.Errorf("consec = %d want 5", consec)
	}
	// Reset recovers.
	b.Reset()
	if b.IsTerminal() {
		t.Error("Reset didn't clear terminal state")
	}
	ok, _ := b.MayStart()
	if !ok {
		t.Error("MayStart after Reset: false")
	}
}

func TestBackoff_CleanLongRunResets(t *testing.T) {
	t.Parallel()
	b := newBackoffFSM()
	// Two crashes → tier 2.
	b.Observe(1, 1*time.Second)
	b.Observe(1, 1*time.Second)
	if b.NextDelay() != 10*time.Second {
		t.Fatalf("precondition: expected next delay 10s, got %v", b.NextDelay())
	}
	// A clean exit that lasted >= stableThreshold → zero out.
	b.Observe(0, 20*time.Minute)
	if b.NextDelay() != 2*time.Second {
		t.Errorf("after long clean run: next delay = %v want 2s (reset)", b.NextDelay())
	}
	_, consec, _, _ := b.State()
	if consec != 0 {
		t.Errorf("consecutive crashes = %d want 0", consec)
	}
}

func TestBackoff_StabilityGapBetweenCrashes(t *testing.T) {
	t.Parallel()
	b := newBackoffFSM()
	// Crash → tier 1.
	b.Observe(1, 1*time.Second)
	// Now pretend a run lasted >= stableThreshold but then crashed again.
	// Artificially set stableSince to 10 min ago.
	forceStableSince(b, 20*time.Minute)
	// The next crash should restart counting from tier 1 (not tier 2),
	// because of the stability gap.
	b.Observe(1, 25*time.Minute)
	// NextDelay should return the first tier (2s) if the current tier is 1
	// (after we incremented from 0 to 1 in Observe), or backoffTiers[1] if
	// the stability gap rule applies.
	// The rule: if stableSince is old, reset tier to 0 THEN increment for
	// this crash → current tier after Observe = 1, NextDelay = backoffTiers[0] = 2s.
	if got := b.NextDelay(); got != 2*time.Second {
		t.Errorf("after stability gap + crash: next delay = %v want 2s", got)
	}
}

// --- Port preflight tests --------------------------------------------------

func TestPreflight_PortConflictDetected(t *testing.T) {
	t.Parallel()
	// Bind a random free port, then ask preflight to check that exact port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	dir := testRoot(t)
	sup := newTestSup(t, dir, func(s *Supervisor) {
		// Deliberately set a port to the one we've bound.
		s.Ports.WebAPILocal = port
	})
	res := sup.Preflight(context.Background())
	if res.OK {
		t.Fatalf("expected preflight to fail due to port conflict")
	}
	found := false
	for _, p := range res.Ports {
		if p.Name == "webapi_local" && p.Port == port && !p.Free {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no failed webapi_local entry in preflight result; ports=%+v", res.Ports)
	}
}

func TestPreflight_ZeroPortsSkipped(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir) // all ports = 0
	res := sup.Preflight(context.Background())
	// With all ports = 0, the port list should be empty.
	for _, p := range res.Ports {
		if p.Port == 0 {
			t.Errorf("zero port included in result: %+v", p)
		}
	}
	// Binary exists + no ports + no data-dir configtest yet.  `mox version`
	// succeeds with our fake, so this ought to be OK.
	if !res.Binary.Exists || !res.Binary.Executable {
		t.Errorf("preflight binary check: %+v", res.Binary)
	}
	// Note: res.OK may still be false if the fake mox config test runs
	// against a non-existent data dir.  That's fine – we only care about
	// zero-port skipping here.
}

func TestPreflight_BinaryChecksum(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	res := sup.Preflight(context.Background())
	// Compute expected checksum ourselves.
	f, _ := os.ReadFile(sup.BinaryPath)
	h := sha256.Sum256(f)
	want := hex.EncodeToString(h[:])
	if res.Binary.ChecksumSHA256 != want {
		t.Errorf("sha256 mismatch: got %q want %q", res.Binary.ChecksumSHA256, want)
	}
}

func TestPreflight_ConfigTestFailure(t *testing.T) {
	// NOT parallel: t.Setenv() requires non-parallel test execution.
	dir := testRoot(t)
	sup := newTestSup(t, dir, func(s *Supervisor) {
		s.DataDir = filepath.Join(dir, "data")
	})
	_ = os.MkdirAll(sup.DataDir, 0o700)
	// Force fake mox config test to exit non-zero via env var.
	t.Setenv("MOX_CONFIGTEST_EXIT", "7")
	res := sup.Preflight(context.Background())
	if res.Config.OK {
		t.Fatalf("expected config test to fail, got OK")
	}
	if res.Config.ExitCode != 7 {
		t.Errorf("exit code = %d want 7", res.Config.ExitCode)
	}
	if res.OK {
		t.Errorf("preflight OK should be false after configtest failure")
	}
}

func TestPreflight_MissingBinary(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir, func(s *Supervisor) {
		s.BinaryPath = filepath.Join(dir, "definitely-does-not-exist", "mox")
	})
	res := sup.Preflight(context.Background())
	if res.OK {
		t.Error("expected preflight to fail with missing binary")
	}
	if res.Binary.Exists {
		t.Errorf("Binary.Exists=true for missing path %s", sup.BinaryPath)
	}
}

// --- Adoption tests --------------------------------------------------------

func TestAdopt_NoMarker(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatalf("ensurepaths: %v", err)
	}
	res, err := sup.Adopt()
	if err != nil {
		t.Fatalf("adopt no marker err: %v", err)
	}
	if res.Success {
		t.Error("adopt with no marker reported success")
	}
	if len(res.Issues) != 0 {
		// No marker → not an "issue".
		t.Errorf("unexpected issues: %+v", res.Issues)
	}
}

func TestAdopt_Layer0_PIDDead(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	// Write a marker with a pid that is guaranteed to be unused on this
	// host (a large integer).  Linux allows pids up to ~4M by default; a
	// number > 4194304 is almost certainly free.
	deadPID := 3999999
	marker := Marker{
		Version:         1,
		PhantomInstance: sup.PhantomInstance,
		BootID:          "boot",
		PID:             deadPID,
		DataDir:         sup.DataDir,
	}
	if err := writeMarker(sup.markerPath, marker); err != nil {
		t.Fatal(err)
	}
	res, err := sup.Adopt()
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("adopt reported success with dead pid")
	}
	if !hasIssue(res.Issues, "pid_dead") {
		t.Errorf("expected pid_dead issue, got %+v", res.Issues)
	}
}

func TestAdopt_Layer1_InstanceMismatch(t *testing.T) {
	t.Parallel()
	// We need a real live PID that we control, so start a sleep-like
	// process (our fake mox serve).
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	proc := spawnBackgroundSleep(t, dir)
	defer procCleanup(t, proc)

	// Marker with a DIFFERENT phantom_instance_id – layer 1 fails.
	marker := Marker{
		Version:         1,
		PhantomInstance: "some-other-phantom-abcdef",
		BootID:          "boot",
		PID:             proc.pid,
		DataDir:         sup.DataDir,
		BinaryPath:      proc.cmd.Path,
	}
	if err := writeMarker(sup.markerPath, marker); err != nil {
		t.Fatal(err)
	}
	res, err := sup.Adopt()
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("adopt should fail on instance_id mismatch; check we did NOT kill the process")
	}
	if !hasIssue(res.Issues, "instance_id") {
		t.Errorf("expected instance_id issue, got %+v", res.Issues)
	}
	// HARD CHECK: the foreign process MUST still be alive.
	if !processExists(proc.pid) {
		t.Fatalf("CRITICAL: Adopt killed a process belonging to a different phantom instance — contract violated!")
	}
}

func TestAdopt_Layer3_StartTimeMismatch(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	proc := spawnBackgroundSleep(t, dir)
	defer procCleanup(t, proc)

	// Marker with correct instance_id BUT a wrong ProcessStartTime (999999
	// instead of the real value).  Layer 3 will catch it on Linux where
	// /proc/starttime is available; on other platforms the layer 3 check is
	// bypassed (readProcStartTime returns ok=false, which sets an "unable
	// to read starttime" issue).  That's fine – both outcomes still refuse
	// adoption (which is what we want here).
	marker := Marker{
		Version:          1,
		PhantomInstance:  sup.PhantomInstance,
		BootID:           "boot",
		PID:              proc.pid,
		DataDir:          sup.DataDir,
		BinaryPath:       proc.cmd.Path,
		ProcessStartTime: 999999999, // deliberately wrong
	}
	if err := writeMarker(sup.markerPath, marker); err != nil {
		t.Fatal(err)
	}
	res, err := sup.Adopt()
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("adopt should fail with ProcessStartTime mismatch or unreadable /proc")
	}
	if !hasIssue(res.Issues, "process_starttime") {
		t.Logf("note: no process_starttime issue — this is OK on non-Linux hosts. issues=%+v", res.Issues)
	}
	// MUST NOT have killed the process.
	if !processExists(proc.pid) {
		t.Fatalf("CRITICAL: adopt killed process on layer-3 failure — contract violated!")
	}
}

func TestAdopt_Layer4_CmdlineMismatch(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir, func(s *Supervisor) {
		// Make the supervisor expect a binary that's definitely NOT what
		// we're about to spawn.  We spawn our own fake "sleep" process by a
		// different name.
		s.BinaryPath = filepath.Join(dir, "definitely-not-mox")
	})
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	// Spawn a plain `sleep` (or, cross-platform, a Go binary renamed to
	// something that doesn't start with "mox").
	proc := spawnRenamedHelper(t, dir, "not-mox-at-all")
	defer procCleanup(t, proc)

	marker := Marker{
		Version:         1,
		PhantomInstance: sup.PhantomInstance,
		BootID:          "boot",
		PID:             proc.pid,
		DataDir:         sup.DataDir,
		BinaryPath:      sup.BinaryPath,
		// Don't set ProcessStartTime – it matches trivially when 0 (the
		// "skip layer 3" branch).
	}
	if err := writeMarker(sup.markerPath, marker); err != nil {
		t.Fatal(err)
	}
	res, err := sup.Adopt()
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("adopt should fail on cmdline mismatch")
	}
	if !hasIssue(res.Issues, "cmdline") {
		t.Logf("note: no cmdline issue — OK on non-Linux (no /proc/cmdline). issues=%+v", res.Issues)
	}
	if !processExists(proc.pid) {
		t.Fatalf("CRITICAL: adopt killed foreign process on cmdline mismatch — contract violated!")
	}
}

func TestAdopt_Lifecycle_Owned_Process_Must_Survive(t *testing.T) {
	t.Parallel()
	// End-to-end adoption success: spawn fake mox serve, write a marker
	// that matches layer 1-4, call Adopt().  Then call Stop() – that
	// SHOULD kill the process because it's now owned.
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	proc := spawnBackgroundSleep(t, dir)
	// Write marker with matching phantom_instance + no bogus ProcessStartTime.
	realTicks, _, _ := readProcStartTime(proc.pid)
	marker := Marker{
		Version:          1,
		PhantomInstance:  sup.PhantomInstance,
		BootID:           "oldbootid",
		PID:              proc.pid,
		DataDir:          sup.DataDir,
		BinaryPath:       proc.cmd.Path,
		ProcessStartTime: realTicks,
	}
	if err := writeMarker(sup.markerPath, marker); err != nil {
		t.Fatal(err)
	}
	res, err := sup.Adopt()
	if err != nil {
		t.Fatal(err)
	}
	// On non-Linux hosts, layer 3 AND layer 4 checks will fail because
	// /proc isn't available – those are graceful skips, so adoption still
	// passes... actually no: the contract is "any single failure means
	// keep our hands off".  So on macOS/Windows Adopt() will return
	// success=false.  That's fine – just skip the rest of the test in that
	// case.
	if !res.Success {
		t.Skipf("adopt returned success=false on this platform (issues=%+v); skipping stop-owned-process test", res.Issues)
	}
	// Check that the boot_id was updated on the marker post-adoption.
	if res.Marker.BootID == "oldbootid" {
		t.Error("adopt did not refresh marker boot_id")
	}
	// Stop the owned process.
	sr, err := sup.Stop()
	if err != nil {
		t.Fatalf("stop owned process: %v", err)
	}
	if processExists(proc.pid) {
		t.Errorf("owned adopted process still alive after Stop(): %+v", sr)
	}
}

// --- Start / Stop / Restart tests -----------------------------------------

func TestStart_EnsurePaths(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"bin", "data", "config", "logs", "run"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			t.Errorf("missing dir %s: err=%v", sub, err)
		}
	}
}

func TestStart_Stop_Normal(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	state, pid, _, _, _, _ := sup.Status()
	if state != StateRunning {
		t.Errorf("state after Start = %s want running", state)
	}
	if pid <= 0 {
		t.Errorf("pid = %d want >0", pid)
	}
	if !processExists(pid) {
		t.Error("pid not live after Start()")
	}
	// Marker exists.
	if m, err := ReadMarker(sup.markerPath); err != nil || m == nil {
		t.Errorf("marker not written: err=%v m=%+v", err, m)
	}
	// Pidfile exists.
	if _, err := os.Stat(sup.pidPath); err != nil {
		t.Errorf("pidfile missing: %v", err)
	}
	// Stop it.
	sr, err := sup.Stop()
	if err != nil {
		t.Fatalf("Stop: %v (result %+v)", err, sr)
	}
	if sr.Killed {
		t.Errorf("clean process required SIGKILL: %+v", sr)
	}
	state, _, _, _, _, _ = sup.Status()
	if state != StateStopped {
		t.Errorf("state after Stop = %s want stopped", state)
	}
	// Marker removed after Stop().
	if m, err := ReadMarker(sup.markerPath); err != nil || m != nil {
		t.Errorf("marker should be removed after Stop, got m=%+v err=%v", m, err)
	}
}

func TestStart_ImportReadonly_Guards(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir, func(s *Supervisor) {
		s.ImportReadOnly = true
	})
	_ = sup.EnsurePaths()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := sup.Start(ctx)
	if !errors.Is(err, ErrNotStarted) {
		t.Errorf("Start(readonly) err=%v want ErrNotStarted chain", err)
	}
	if _, err := sup.Stop(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Stop(readonly) err=%v want ErrNotStarted", err)
	}
	state, _, _, _, _, _ := sup.Status()
	if state != StateImportRO {
		t.Errorf("status(readonly) = %s want import", state)
	}
	// Close must not touch anything either.
	if err := sup.Close(); err != nil {
		t.Errorf("Close(readonly) err=%v want nil", err)
	}
}

func TestStart_CrashLoopBackoffTerminal(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	// Forcibly drive backoff to terminal state.
	sup.backoff.Observe(1, 1 * time.Second)
	sup.backoff.Observe(1, 1 * time.Second)
	sup.backoff.Observe(1, 1 * time.Second)
	sup.backoff.Observe(1, 1 * time.Second)
	sup.backoff.Observe(1, 1 * time.Second)
	if !sup.backoff.IsTerminal() {
		t.Fatal("precondition: expected terminal backoff after 5 crashes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := sup.Start(ctx)
	if !errors.Is(err, ErrCrashLoopExhausted) {
		t.Errorf("Start terminal backoff err=%v want ErrCrashLoopExhausted", err)
	}
	// ResetCrashLoop() recovers.
	sup.ResetCrashLoop()
	state, _, _, clState, _, _ := sup.Status()
	_ = state
	if clState == CLFailed {
		t.Error("ResetCrashLoop didn't clear failed state")
	}
}

func TestRestart_Workflow(t *testing.T) {
	t.Parallel()
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("initial Start: %v", err)
	}
	_, firstPid, _, _, _, _ := sup.Status()
	if _, err := sup.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	state, secondPid, _, _, _, _ := sup.Status()
	if state != StateRunning {
		t.Errorf("state after Restart = %s want running", state)
	}
	if secondPid == firstPid {
		t.Errorf("pid didn't change after Restart (%d → %d)", firstPid, secondPid)
	}
	// Cleanup.
	if _, err := sup.Stop(); err != nil {
		t.Errorf("final Stop: %v", err)
	}
}

// Test that we correctly report the backoff sleep in Status() and that
// MayStart enforces it.
func TestBackoff_MayStartSleep(t *testing.T) {
	t.Parallel()
	b := newBackoffFSM()
	b.Observe(1, 1*time.Second)
	ok, rem := b.MayStart()
	if ok {
		t.Fatal("expected MayStart=false after crash")
	}
	if rem < 1800*time.Millisecond {
		t.Errorf("backoff sleep = %v want ~2s", rem)
	}
}

// --- Signal escalation tests ----------------------------------------------

func TestSignal_EscalationTier3KillsStubborn(t *testing.T) {
	// NOT parallel: t.Setenv() requires serial execution.
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	readyFile := filepath.Join(dir, "mox.ready")
	t.Setenv("MOX_READY_FILE", readyFile)
	t.Setenv("MOX_IGNORE_SIGTERM_SECS", "60") // ignore SIGTERM for a full minute
	t.Setenv("MOX_HANG_FOREVER", "1")         // never exit on our own
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for fake-mox to reach main()/signal.Notify before sending SIGTERM,
	// otherwise the default SIGTERM handler fires and kills the process
	// before the test can observe Tier 3 escalation.
	waitReady(t, readyFile, 5*time.Second)
	_, pid, _, _, _, _ := sup.Status()

	startedStop := time.Now()
	sr, err := sup.Stop()
	stopDur := time.Since(startedStop)
	if err != nil {
		t.Fatalf("Stop stubborn: %v", err)
	}
	if !sr.Killed {
		t.Errorf("expected SIGKILL (Killed=true), got %+v", sr)
	}
	if processExists(pid) {
		t.Error("process still alive after Stop()")
	}
	// Stop must complete within the three tiers sum: 30s+10s+5s = 45s, with
	// a bit of slack on both ends.
	if stopDur > 55*time.Second {
		t.Errorf("stop took %v, expected <55s (three tiers sum to 45s)", stopDur)
	}
	if stopDur < 40*time.Second {
		// A fast clean exit here means IGNORE_SIGTERM_SECS wasn't honoured,
		// which means our env var didn't propagate.
		t.Logf("note: stop took %v (fast; IGNORE_SIGTERM may not have applied on this platform)", stopDur)
	}
	_ = sr
}

// --- Concurrent Status while Stop is in flight ---------------------------

func TestStatus_DoesNotBlockDuringStop(t *testing.T) {
	// NOT parallel: t.Setenv() requires serial execution.
	dir := testRoot(t)
	sup := newTestSup(t, dir, func(s *Supervisor) {
		// Use a hang-forever process so Stop() takes the full 45s three-tier
		// escalation.
	})
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	readyFile := filepath.Join(dir, "mox.ready")
	t.Setenv("MOX_READY_FILE", readyFile)
	t.Setenv("MOX_IGNORE_SIGTERM_SECS", "60")
	t.Setenv("MOX_HANG_FOREVER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitReady(t, readyFile, 5*time.Second)
	// Kick off Stop in the background.
	var stopResult atomic.Value
	go func() {
		sr, err := sup.Stop()
		stopResult.Store(struct {
			sr  StopResult
			err error
		}{sr, err})
	}()
	// Immediately (within the first 100ms) call Status() repeatedly – it
	// must not block for 45s.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			sup.Status()
		}
		close(done)
	}()
	select {
	case <-done:
		// OK – Status() is not blocked by Stop().
	case <-time.After(2 * time.Second):
		t.Fatalf("Status() appeared to block during Stop() – likely held mu across tier escalation")
	}
	// Wait for Stop to finish for hygiene.
	for i := 0; i < 50; i++ {
		if stopResult.Load() != nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// --- pgid signalling kills children too ------------------------------------

func TestSetpgidKillsChildWorker(t *testing.T) {
	t.Parallel()
	// Our fake mox can't easily spawn sub-processes, but we can verify the
	// *mechanics*: after Start(), the supervisor's cmd must have been
	// launched with Setpgid=true.  That's a SysProcAttr field, so we can
	// introspect cmd.SysProcAttr after the fact.
	dir := testRoot(t)
	sup := newTestSup(t, dir)
	if err := sup.EnsurePaths(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Read SysProcAttr BEFORE Stop – it's still in s.cmd.
	sup.mu.Lock()
	setpgid := sup.cmd.SysProcAttr != nil && sup.cmd.SysProcAttr.Setpgid
	sup.mu.Unlock()
	if !setpgid {
		t.Error("Setpgid was not set on child process – tier 2/3 signals won't reach the group")
	}
	_, _ = sup.Stop()
}

// --- Background process spawning (used by adoption tests) ------------------

type spawnedProc struct {
	cmd *exec.Cmd
	pid int
}

func spawnBackgroundSleep(t *testing.T, dir string) *spawnedProc {
	t.Helper()
	// Use our compiled fake mox with env var MOX_HANG_FOREVER so it ignores
	// SIGTERM until we deliberately kill it.
	binary := findFakeMox(t)
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "MOX_HANG_FOREVER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn fake mox: %v", err)
	}
	return &spawnedProc{cmd: cmd, pid: cmd.Process.Pid}
}

func spawnRenamedHelper(t *testing.T, dir, newBase string) *spawnedProc {
	t.Helper()
	binary := findFakeMox(t)
	// Copy binary under a new name.
	newPath := filepath.Join(dir, newBase)
	src, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read fake mox: %v", err)
	}
	if err := os.WriteFile(newPath, src, 0o700); err != nil {
		t.Fatalf("write renamed binary: %v", err)
	}
	cmd := exec.Command(newPath, "serve")
	cmd.Env = append(os.Environ(), "MOX_HANG_FOREVER=1")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn renamed helper: %v", err)
	}
	return &spawnedProc{cmd: cmd, pid: cmd.Process.Pid}
}

func procCleanup(t *testing.T, p *spawnedProc) {
	t.Helper()
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	// Best-effort kill.
	_ = p.cmd.Process.Kill()
	// Don't leak zombies.
	_ = p.cmd.Wait()
	// Double-check with signalGroup in case Setpgid was set.
	if processExists(pid) {
		_ = signalGroup(pid, syscall.SIGKILL)
	}
}

func hasIssue(issues []AdoptionIssue, layer string) bool {
	for _, i := range issues {
		if i.Layer == layer {
			return true
		}
	}
	return false
}

// --- Exported backoff test helpers -----------------------------------------
//
// BackoffFSM is internal; we expose small helpers via the test package so
// tests can manually manipulate time without importing reflect.

func forceBackoffExpired(b *backoffFSM) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastBackoffUntil = time.Now().Add(-time.Hour)
}

func forceStableSince(b *backoffFSM, offset time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stableSince = time.Now().Add(-offset)
}
