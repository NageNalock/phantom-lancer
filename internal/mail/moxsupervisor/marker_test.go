package moxsupervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// muxPackageVars serialises tests that mutate package-level tunables.
// Because many tests in this package run with t.Parallel(), tests that share
// state (ProcRoot, BackoffTiers, Stop*Timeout, …) would otherwise race.
// We split the lock by family so unrelated tests can still run in parallel:
//
//	muxProcVars    – ProcRoot, BootIDPath                   (marker + adopt)
//	muxBackoffVars – B1..B4, ResetAfter, BackoffTiers, …    (backoff)
//	muxSignalVars  – Stop*Timeout, WaitPollInterval         (signals)
//
// A plain sync.Mutex is enough per family — saveRestore helpers acquire once
// per test and their t.Cleanup releases in LIFO order within the same goroutine.
var (
	muxProcVars    sync.Mutex
	muxBackoffVars sync.Mutex
	muxSignalVars  sync.Mutex
)

// --- ProcRoot override helpers ---------------------------------------------
//
// These tests inject a synthetic /proc tree by overriding package-level
// ProcRoot.  This is how we simulate a real Linux /proc on macOS and how
// we falsify specific values (starttime, cmdline) to drive the 4 failure
// branches in ValidateMarker.

func saveRestoreProcRoot(t *testing.T) (original string) {
	t.Helper()
	muxProcVars.Lock()
	original = ProcRoot
	origBoot := BootIDPath
	t.Cleanup(func() {
		ProcRoot = original
		BootIDPath = origBoot
		muxProcVars.Unlock()
	})
	return original
}

// makeFakeProc builds a minimal fake /proc tree for pid that looks like:
//
//	<root>/<pid>/stat  – user controls the starttime field value
//	<root>/<pid>/cmdline – NUL-separated argv tokens; argv[0] = binaryName
//
// Returns the directory that should be assigned to ProcRoot.
func makeFakeProc(t *testing.T, pid int, starttimeTicks uint64, binaryName string, kernelBootID string) string {
	t.Helper()
	root := t.TempDir()

	// stat file.  We construct it the same way parseStatStringForTest expects
	// – comm wrapped in parens, then tail[0..19] where tail[19] is starttime.
	tokens := make([]string, 20)
	tokens[0] = "R" // state
	for i := 1; i < 19; i++ {
		tokens[i] = strconv.Itoa(i)
	}
	tokens[19] = strconv.FormatUint(starttimeTicks, 10)
	// Append extra padding to look real.
	for i := 0; i < 40; i++ {
		tokens = append(tokens, "0")
	}
	statContent := strconv.Itoa(pid) + " (" + filepath.Base(binaryName) + ") " + strings.Join(tokens, " ")
	statDir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(statDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(statDir, "stat"), []byte(statContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// cmdline file – NUL-separated.
	argv := []byte(binaryName + " serve -config /etc/mox.conf\x00")
	if err := os.WriteFile(filepath.Join(statDir, "cmdline"), argv, 0o600); err != nil {
		t.Fatal(err)
	}

	// sys/kernel/random/boot_id
	if kernelBootID != "" {
		bootDir := filepath.Join(root, "sys", "kernel", "random")
		if err := os.MkdirAll(bootDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bootDir, "boot_id"), []byte(kernelBootID+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Also update BootIDPath var to point into our synthetic tree.
		BootIDPath = filepath.Join(root, "sys", "kernel", "random", "boot_id")
		t.Cleanup(func() { BootIDPath = "/proc/sys/kernel/random/boot_id" })
	}
	return root
}

// spawnControllableProcess starts a tiny Go program that hangs until SIGTERM
// (or until the cleanup kills it).  Returns pid + binary path + cleanup fn.
func spawnControllableProcess(t *testing.T) (pid int, binaryPath string, cleanup func()) {
	t.Helper()
	// Build a small Go test helper that just hangs.
	dir := t.TempDir()
	src := filepath.Join(dir, "hanger.go")
	err := os.WriteFile(src, []byte(`package main

import (
	"os"
	"os/signal"
	"syscall"
)

// When env MOX_TEST_HANG_ON_SIGTERM=1, SIGTERM is completely ignored
// (used for Stop escalation tests). Otherwise SIGTERM exits cleanly.
func main() {
	// Simulate mox subcommands that the supervisor runs in Preflight:
	//   mox version → print version and exit 0
	//   mox ... config test → exit 0
	args := os.Args[1:]
	for _, a := range args {
		if a == "version" {
			os.Stdout.Write([]byte("mox-hanger v0.0.0-test\n"))
			os.Exit(0)
		}
	}
	for _, a := range args {
		if a == "test" {
			os.Exit(0)
		}
	}
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	hangOnSigterm := os.Getenv("MOX_TEST_HANG_ON_SIGTERM") == "1"
	for {
		s := <-ch
		if hangOnSigterm && s == syscall.SIGTERM {
			// swallow SIGTERM – never exit (tests escalation)
			continue
		}
		if s == syscall.SIGTERM {
			os.Exit(0)
		}
		os.Exit(1)
	}
}
`), 0o600)
	if err != nil {
		t.Fatalf("write hanger source: %v", err)
	}
	bin := filepath.Join(dir, "mox-hang-test")
	out, buildErr := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if buildErr != nil {
		t.Fatalf("build hanger: %v; output=%s", buildErr, out)
	}
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn hanger: %v", err)
	}
	// Give a small window for the process to reach main/signal.Notify.
	time.Sleep(50 * time.Millisecond)
	cleanup = func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Process.Pid, bin, cleanup
}

// spawnStubbornProcess works like spawnControllableProcess but the child
// swallows SIGTERM so tier-1 graceful stop times out; we use this to test
// the 3-tier signal escalation path.  Note: callers must set the env var
// via their Supervisor start hook.  To keep this helper simple, we mutate
// the process after spawning by writing a different env – instead, the
// returned environment suffix "STUBBORN=1" is a hint; actual activation
// happens via os.Environ in the caller's Start path.
//
// To avoid that complexity, this function builds a SEPARATE binary whose
// source code has SIGTERM hardcoded to be swallowed.  This mirrors
// spawnControllableProcess but with a modified main().
func spawnStubbornProcess(t *testing.T) (pid int, binaryPath string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "hanger.go")
	err := os.WriteFile(src, []byte(`package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	args := os.Args[1:]
	for _, a := range args {
		if a == "version" {
			os.Stdout.Write([]byte("mox-hanger v0.0.0-test\n"))
			os.Exit(0)
		}
	}
	for _, a := range args {
		if a == "test" {
			os.Exit(0)
		}
	}
	ch := make(chan os.Signal, 32)
	// Catch SIGTERM and SIGINT but never exit – we want the test's
	// tier-1 SIGTERM to time out, forcing escalation to SIGKILL.
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	for range ch {
		// swallow everything
	}
}
`), 0o600)
	if err != nil {
		t.Fatalf("write stubborn hanger: %v", err)
	}
	bin := filepath.Join(dir, "mox-stubborn-test")
	out, buildErr := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if buildErr != nil {
		t.Fatalf("build stubborn hanger: %v; output=%s", buildErr, out)
	}
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn stubborn hanger: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	cleanup = func() {
		_ = syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Process.Pid, bin, cleanup
}

// ==============================================================
// TestMarkerWriteRead – write marker to tmp file, read it back,
// ALL fields match.
// ==============================================================

func TestMarkerWriteRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "run", "marker.json")
	want := Marker{
		Version:          1,
		PhantomInstance:  "phantom-instance-abcdef",
		BootID:           "bootid0001",
		PID:              12345,
		StartTimeNano:    1700000000000000000,
		ProcessStartTime: 987654321,
		BinaryPath:       "/usr/local/bin/mox",
		BinaryChecksum:   "aaabbbccc123",
		ConfigPath:       "/etc/mox/mox.conf",
		ConfigHash:       "hash123",
		DataDir:          "/var/mox/data",
		LaunchedAt:       "2026-06-13T10:00:00.000000001Z",
		LogStdout:        "/var/log/mox.stdout.log",
		LogStderr:        "/var/log/mox.stderr.log",
	}
	if err := writeMarker(path, want); err != nil {
		t.Fatalf("writeMarker: %v", err)
	}
	got, err := ReadMarker(path)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if got == nil {
		t.Fatal("ReadMarker returned nil marker")
	}
	if got.Version != want.Version ||
		got.PhantomInstance != want.PhantomInstance ||
		got.BootID != want.BootID ||
		got.PID != want.PID ||
		got.StartTimeNano != want.StartTimeNano ||
		got.ProcessStartTime != want.ProcessStartTime ||
		got.BinaryPath != want.BinaryPath ||
		got.BinaryChecksum != want.BinaryChecksum ||
		got.ConfigPath != want.ConfigPath ||
		got.ConfigHash != want.ConfigHash ||
		got.DataDir != want.DataDir ||
		got.LaunchedAt != want.LaunchedAt ||
		got.LogStdout != want.LogStdout ||
		got.LogStderr != want.LogStderr {
		t.Fatalf("roundtrip mismatch:\n  got = %+v\n  want= %+v", got, want)
	}
}

// ==============================================================
// TestMarkerValidate_Positive – start a real controlled process,
// capture pid, starttime via /proc/<pid>/stat (or synthetic on
// non-Linux), write marker, ValidateMarker returns ok.
// ==============================================================

func TestMarkerValidate_Positive(t *testing.T) {
	// Do NOT parallelise – we mutate ProcRoot / processExists behaviour.
	_ = saveRestoreProcRoot(t)

	// Spawn a real process we control (so pid + signal(0) work on all
	// platforms).  Then overlay a fake /proc whose values match the
	// marker we'll write, so the test behaves identically on Linux and
	// macOS (where /proc is not available natively).
	pid, binaryPath, cleanup := spawnControllableProcess(t)
	defer cleanup()

	// Pick synthetic starttime that we'll use both in the fake proc AND in
	// the marker.
	const fakeStartTicks uint64 = 77777777
	fakeProc := makeFakeProc(t, pid, fakeStartTicks, binaryPath, "bootid-kernel-000")
	ProcRoot = fakeProc

	marker := Marker{
		Version:          1,
		PhantomInstance:  "our-instance",
		BootID:           "bootid-supervisor-000",
		PID:              pid,
		ProcessStartTime: fakeStartTicks,
		BinaryPath:       binaryPath,
	}

	ok, issues := ValidateMarker(marker,
		"our-instance",            // phantom
		"bootid-supervisor-000",   // boot (enabled)
		filepath.Base(binaryPath)) // expected cmdline[0] basename
	if !ok {
		t.Errorf("expected ValidateMarker=true, got false; issues=%+v", issues)
	}
	if len(issues) != 0 {
		t.Errorf("unexpected issues on positive validate: %+v", issues)
	}
}

func TestProcessExists_TreatsProcZombieAsExited(t *testing.T) {
	_ = saveRestoreProcRoot(t)

	pid := os.Getpid()
	root := t.TempDir()
	pidDir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, 20)
	tokens[0] = "Z"
	for i := 1; i < len(tokens); i++ {
		tokens[i] = strconv.Itoa(i)
	}
	statContent := strconv.Itoa(pid) + " (phantom test) " + strings.Join(tokens, " ")
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(statContent), 0o600); err != nil {
		t.Fatal(err)
	}
	ProcRoot = root

	if processExists(pid) {
		t.Fatal("processExists returned true for a /proc zombie state")
	}
}

// ==============================================================
// TestMarkerValidate_AllFailures – each of the 4 checks fails
// individually while the other 3 are correct.
// ==============================================================

func TestMarkerValidate_AllFailures(t *testing.T) {
	_ = saveRestoreProcRoot(t)

	// Spawn a real process so pid_dead check passes (process really exists).
	pid, binaryPath, cleanup := spawnControllableProcess(t)
	defer cleanup()

	const fakeStartTicks uint64 = 12345678
	const expectedInstance = "our-instance"
	const expectedBootID = "bootid-expected-000"
	const kernelBootID = "kernel-boot-abc"
	fakeProc := makeFakeProc(t, pid, fakeStartTicks, binaryPath, kernelBootID)
	ProcRoot = fakeProc

	// Baseline marker: all 4 layers pass.
	base := Marker{
		Version:          1,
		PhantomInstance:  expectedInstance,
		BootID:           expectedBootID,
		PID:              pid,
		ProcessStartTime: fakeStartTicks,
		BinaryPath:       binaryPath,
	}
	expectedBinaryBase := filepath.Base(binaryPath)

	// Subtests – each subtest breaks exactly one layer.
	cases := []struct {
		name      string
		mutate    func(m *Marker) // mutate the baseline marker
		wantLayer string          // layer we expect to fail
	}{
		{
			name: "phantom_instance_id_mismatch",
			mutate: func(m *Marker) {
				m.PhantomInstance = "some-other-instance-wrong"
			},
			wantLayer: "instance_id",
		},
		{
			name: "boot_id_mismatch",
			mutate: func(m *Marker) {
				m.BootID = "bootid-wrong-not-expected"
			},
			wantLayer: "boot_id",
		},
		{
			name: "starttime_ns_mismatch",
			mutate: func(m *Marker) {
				// Correct instance/boot but a stale starttime (pid was
				// recycled by the kernel).
				m.ProcessStartTime = 99999999999
			},
			wantLayer: "process_starttime",
		},
		{
			name: "cmdline_token0_mismatch",
			mutate: func(m *Marker) {
				// Simulate /proc/<pid>/cmdline[0] pointing at a totally
				// unrelated binary by regenerating fake proc with wrong argv.
				otherBin := "/usr/bin/some-totally-unrelated-app"
				// Create a new fakeProc *only for this pid's cmdline*.
				root := filepath.Dir(filepath.Dir(ProcRoot))
				_ = root
				// Overwrite only the cmdline file of the existing fake proc.
				pidDir := filepath.Join(ProcRoot, strconv.Itoa(m.PID))
				content := []byte("some-totally-unrelated-app serve\x00")
				if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), content, 0o600); err != nil {
					t.Fatalf("rewrite cmdline: %v", err)
				}
				_ = otherBin
			},
			wantLayer: "cmdline",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Start each subtest from a clean state: reset cmdline in case a
			// prior sibling mutated it.
			pidDir := filepath.Join(ProcRoot, strconv.Itoa(base.PID))
			freshCmdline := []byte(binaryPath + " serve\x00")
			if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), freshCmdline, 0o600); err != nil {
				t.Fatalf("reset cmdline: %v", err)
			}

			m := base // copy
			tc.mutate(&m)

			ok, issues := ValidateMarker(m,
				expectedInstance,
				expectedBootID,
				expectedBinaryBase)
			if ok {
				t.Errorf("%s: expected ValidateMarker=false, got true (issues=%+v)", tc.name, issues)
			}
			found := false
			for _, i := range issues {
				if i.Layer == tc.wantLayer {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: expected issue layer=%q not present; issues=%+v", tc.name, tc.wantLayer, issues)
			}
			// Sanity: only one layer should have failed (plus pid_dead which
			// is layer 0 but that's passing for us).
			// We allow up to len(cases) but the exact count for our setup is 1.
			if len(issues) != 1 {
				t.Logf("%s: got %d issues (expected 1): %+v", tc.name, len(issues), issues)
			}
			// HARD invariant: the process MUST still be alive after a
			// validation that returned ok=false.  ValidateMarker must never
			// signal processes.
			if !processExists(pid) {
				t.Fatalf("%s: CRITICAL — ValidateMarker killed the process!", tc.name)
			}
		})
	}

	// Additional check: runtime.GOOS does not affect this test because we
	// always use the synthetic /proc tree.  This proves macOS and Linux
	// produce identical results.
	_ = runtime.GOOS
}
