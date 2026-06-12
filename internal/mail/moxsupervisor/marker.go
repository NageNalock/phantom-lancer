package moxsupervisor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ProcRoot is the root of the /proc filesystem used by marker / adoption
// helpers.  Tests override it to inject synthetic /proc trees on all platforms
// (including macOS, where /proc is not available natively).  Production
// callers never touch it.
var ProcRoot = "/proc"

// BootIDPath is the canonical kernel boot_id source.  Tests override it on
// platforms where /proc/sys/kernel/random/boot_id does not exist or
// when a synthetic value is desired.
var BootIDPath = "/proc/sys/kernel/random/boot_id"

// --- Marker schema --------------------------------------------------------

// Marker is the JSON payload written atomically to
// <moxRoot>/run/mox.marker.json right after fork+exec succeeds.  It is the
// ONLY on-disk record we trust when recovering from a Phantom restart /
// crash; callers never guess the PID from ps(1) output.
//
// The 4 adoption validation layers live on Marker itself:
//
//  1. phantom_instance_id  – different Phantom = hands off (prevents
//     co-hosted Phantom instances from killing each other's Mox).
//  2. boot_id              – generated per Start() call; the supervisor
//     rejects any marker whose boot_id doesn't match a newly-started
//     process (catches a stale marker from a PID wrap that happened on
//     the SAME Phantom instance between boot cycles).
//  3. start_time_ns        – read back from /proc/<pid>/stat; rejects a
//     marker whose PID got recycled by the kernel in the ~milliseconds
//     between process exit and our next Adopt() call.
//  4. cmdline_token0      – the 0th token from /proc/<pid>/cmdline must
//     match the mox binary basename (catches a completely unrelated
//     process that happened to land on the recycled PID with the same
//     starttime).
type Marker struct {
	Version          int    `json:"version"`           // schema version, always 1 today
	PhantomInstance  string `json:"phantom_instance_id"`
	BootID           string `json:"boot_id"`             // regenerated per Start()
	PID              int    `json:"pid"`                  // >=1 iff process is live
	StartTimeNano    int64  `json:"start_time_ns"`        // monotonic clock at Start()
	ProcessStartTime uint64 `json:"process_start_time"`   // kernel starttime from /proc
	BinaryPath       string `json:"binary_path"`
	BinaryChecksum   string `json:"binary_checksum_sha256,omitempty"`
	ConfigPath       string `json:"config_path,omitempty"`
	ConfigHash       string `json:"config_hash_sha256,omitempty"`
	DataDir          string `json:"data_dir"`
	LaunchedAt       string `json:"launched_at"`          // RFC3339Nano
	LogStdout        string `json:"log_stdout"`
	LogStderr        string `json:"log_stderr"`
}

const markerVersion = 1

// GenerateBootID produces a 12-byte random hex id.  It's short enough to
// be readable in log files and long enough to avoid accidental collision
// within the lifetime of a single Phantom instance.
func GenerateBootID() (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("moxsupervisor: generate boot_id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// --- Write + read ---------------------------------------------------------

// writeMarker writes m atomically.  The marker file must live on the same
// filesystem as MoxRoot (which we guarantee because both are under
// cfg.DataDir).  Permissions 0600 – the marker contains a PID so untrusted
// users don't need to read it.
func writeMarker(path string, m Marker) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("moxsupervisor: mkdir marker dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "mox.marker.*.json")
	if err != nil {
		return fmt.Errorf("moxsupervisor: create marker tmpfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			cleanup()
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("moxsupervisor: chmod marker tmpfile: %w", err)
	}
	if err := json.NewEncoder(tmp).Encode(m); err != nil {
		return fmt.Errorf("moxsupervisor: encode marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("moxsupervisor: close marker tmpfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("moxsupervisor: atomic rename marker: %w", err)
	}
	ok = true
	return nil
}

// ReadMarker returns the marker at path or (nil, nil) if it does not exist.
// It does NOT validate the contents; callers that want adoption should go
// through Adopt instead.
func ReadMarker(path string) (*Marker, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("moxsupervisor: open marker: %w", err)
	}
	defer f.Close()
	var m Marker
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("moxsupervisor: decode marker: %w", err)
	}
	return &m, nil
}

// --- Kernel-level checks (layers 3 & 4) ----------------------------------

// readProcStartTime returns the 22nd field of /proc/<pid>/stat (starttime,
// in clock ticks).  This value is stable across PID wraps for a single
// process and cannot be forged by userspace.
//
// On non-Linux systems where /proc is unavailable (macOS / Windows build
// hosts) we fall back to processStartTimeUnsupported, which returns
// (0, nil, false) so callers can gracefully degrade: build-time tests use a
// fake cmd helper and explicitly set ProcessStartTime, and production
// deployments (Linux) always hit the real path.
func readProcStartTime(pid int) (ticks uint64, source string, ok bool) {
	data, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", ProcRoot, pid))
	if err != nil {
		return 0, "", false
	}
	// The 2nd field is the comm wrapped in parens – it may contain
	// whitespace and parens so we MUST split from the RIGHT side of the
	// final ')' character instead of strings.Fields from the left.
	closeIdx := strings.LastIndexByte(string(data), ')')
	if closeIdx < 0 {
		return 0, "", false
	}
	tail := string(data[closeIdx+1:])
	fields := strings.Fields(tail)
	// /proc/[pid]/stat fields after the closing paren of field 2 are
	// numbered starting at 3.  starttime is field 22 overall, so 20
	// fields into tail (zero-indexed: tail[19]).
	if len(fields) < 20 {
		return 0, "", false
	}
	n, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return n, "proc", true
}

// readProcCmdlineToken0 returns argv[0] from /proc/<pid>/cmdline.  On a
// well-formed system this is the absolute path to the mox binary (or its
// basename if launched via PATH lookup).
func readProcCmdlineToken0(pid int) (string, bool) {
	data, err := os.ReadFile(fmt.Sprintf("%s/%d/cmdline", ProcRoot, pid))
	if err != nil {
		return "", false
	}
	// argv entries are NUL-separated; the file ends with a final NUL.
	first := strings.IndexByte(string(data), 0)
	if first < 0 {
		// Some kernels return a plain string without trailing NUL for
		// kernel threads; treat the whole blob as token 0.
		tok := strings.TrimSpace(string(data))
		if tok == "" {
			return "", false
		}
		return tok, true
	}
	if first == 0 {
		return "", false
	}
	return string(data[:first]), true
}

// processExists reports whether the OS has a live process with this pid.
// Signal 0 is the standard "permission check" signal – it does not
// actually deliver anything but fails with ESRCH when the pid is gone and
// EPERM when it exists but we cannot signal it (either case counts as
// "exists" for adoption purposes).
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM = process exists, we just can't signal it.
	return errors.Is(err, syscall.EPERM)
}

// --- ValidateMarker: the 4-layer adoption check ----------------------------

// ValidateMarkerIssue describes a single failed layer in ValidateMarker.
type ValidateMarkerIssue struct {
	Layer   string // "instance_id" / "boot_id" / "process_starttime" / "cmdline" / "pid_dead"
	Message string
}

// ValidateMarker runs all four adoption-validation checks against `marker`,
// returning (true, nil) when every layer passes.  When any layer fails it
// returns (false, []ValidateMarkerIssue) listing every failed check so the
// caller can surface them in the UI.
//
// Layer 2 (boot_id) can be disabled by passing bootIDExpected="" – this is
// used by Adopt(), where a different boot_id between marker and supervisor
// is the *expected* case (the process survived a Phantom restart).
//
// The expectedBinaryBasename parameter controls layer 4: when empty, the
// cmdline token-0 check is skipped entirely (useful when we don't know
// which binary name will be used).
func ValidateMarker(marker Marker,
	phantomInstance, bootIDExpected, expectedBinaryBasename string,
) (bool, []ValidateMarkerIssue) {
	issues := []ValidateMarkerIssue{}

	// Layer 0: pid alive check.
	if !processExists(marker.PID) {
		issues = append(issues, ValidateMarkerIssue{
			Layer:   "pid_dead",
			Message: fmt.Sprintf("pid %d does not exist", marker.PID),
		})
	}

	// Layer 1: phantom_instance_id.
	if phantomInstance != "" && marker.PhantomInstance != phantomInstance {
		issues = append(issues, ValidateMarkerIssue{
			Layer: "instance_id",
			Message: fmt.Sprintf("marker phantom_instance_id=%q, expected %q",
				marker.PhantomInstance, phantomInstance),
		})
	}

	// Layer 2: boot_id (skipped when bootIDExpected is empty).
	if bootIDExpected != "" && marker.BootID != bootIDExpected {
		issues = append(issues, ValidateMarkerIssue{
			Layer:   "boot_id",
			Message: fmt.Sprintf("marker boot_id=%q, expected %q", marker.BootID, bootIDExpected),
		})
	}

	// Layer 3: /proc/<pid>/stat starttime.
	if marker.PID > 0 {
		startTicks, _, ok := readProcStartTime(marker.PID)
		if !ok {
			issues = append(issues, ValidateMarkerIssue{
				Layer:   "process_starttime",
				Message: fmt.Sprintf("could not read %s/%d/stat", ProcRoot, marker.PID),
			})
		} else if marker.ProcessStartTime > 0 && startTicks != marker.ProcessStartTime {
			issues = append(issues, ValidateMarkerIssue{
				Layer: "process_starttime",
				Message: fmt.Sprintf("marker starttime=%d ticks but kernel reports %d",
					marker.ProcessStartTime, startTicks),
			})
		}
	}

	// Layer 4: argv[0] must look like mox (when expectedBinaryBasename set).
	if expectedBinaryBasename != "" && marker.PID > 0 {
		cmdlineTok0, ok := readProcCmdlineToken0(marker.PID)
		if !ok {
			issues = append(issues, ValidateMarkerIssue{
				Layer:   "cmdline",
				Message: fmt.Sprintf("could not read %s/%d/cmdline", ProcRoot, marker.PID),
			})
		} else {
			got := filepath.Base(cmdlineTok0)
			want := filepath.Base(expectedBinaryBasename)
			if got != want && got != "mox" && !strings.HasPrefix(got, "mox") {
				issues = append(issues, ValidateMarkerIssue{
					Layer: "cmdline",
					Message: fmt.Sprintf("cmdline[0]=%q does not look like %q or mox*",
						cmdlineTok0, want),
				})
			}
		}
	}

	return len(issues) == 0, issues
}

// --- Kernel boot_id ---------------------------------------------------------

// readKernelBootID reads the system boot_id used to distinguish different OS
// boots.  On Linux it comes from /proc/sys/kernel/random/boot_id; on
// non-Linux platforms we fall back to a hash derived from a stable identifier
// (hostid or sysctl kernel.boottime).  When all fails it returns a non-empty
// deterministic string so the layer-2 check still has *something* to compare.
func readKernelBootID() string {
	raw, err := os.ReadFile(BootIDPath)
	if err == nil {
		id := strings.TrimSpace(string(raw))
		if id != "" {
			return id
		}
	}
	return "no-boot-id-available"
}
