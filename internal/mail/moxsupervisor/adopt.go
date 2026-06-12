package moxsupervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AdoptionIssue records a single failed layer in Adopt.  All four checks
// always run so the UI can show the exact reason adoption was refused
// rather than a generic "cannot adopt" message.
type AdoptionIssue struct {
	Layer   string // "instance_id" / "boot_id" / "process_starttime" / "cmdline" / "pid_dead"
	Message string
}

// AdoptionResult is returned by Adopt.  When success=false, Issues lists
// every check that failed (one or more).  The caller MUST NOT treat a
// partial pass as "good enough" – any single failure means we keep our
// hands off the process.
type AdoptionResult struct {
	Success   bool
	Marker    *Marker
	ProcessID int
	StartNano int64
	Issues    []AdoptionIssue
}

// Adopt tries to adopt the running Mox process recorded in s.markerPath.
// It is safe to call on every boot.
//
// Contract:
//   - Returns (res, nil) when the adoption decision was computed without
//     filesystem errors.  res.Success=false is a normal outcome (no marker,
//     or validation failed), NOT an error.
//   - Returns (nil, err) only when the marker file itself is unreadable,
//     which indicates disk-level corruption – callers should log at ERROR
//     and prompt the operator to clear <moxRoot>/run/.
//
// On success the Supervisor takes ownership of the process: subsequent
// Stop() / Restart() calls will signal it, and crash-loop backoff starts
// from "adopted" (not "stable") so the first post-adoption crash is
// treated as a regular failure.
func (s *Supervisor) Adopt() (*AdoptionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adoptLocked()
}

func (s *Supervisor) adoptLocked() (*AdoptionResult, error) {
	result := &AdoptionResult{Issues: []AdoptionIssue{}}
	marker, err := ReadMarker(s.markerPath)
	if err != nil {
		return nil, err
	}
	if marker == nil {
		// No marker = nothing to adopt.  This is the common cold-boot case.
		result.Success = false
		return result, nil
	}
	result.Marker = marker
	result.ProcessID = marker.PID

	// Layer 0: is the pid even alive?  If not, the marker is stale – no
	// adoption, but we leave an issue so callers can surface "stale marker"
	// warnings and optionally clean up.
	if !processExists(marker.PID) {
		result.Issues = append(result.Issues, AdoptionIssue{
			Layer:   "pid_dead",
			Message: fmt.Sprintf("PID %d in marker no longer exists; marker is stale", marker.PID),
		})
		return result, nil
	}

	// Layer 1: phantom_instance_id.
	if s.PhantomInstance != "" && marker.PhantomInstance != s.PhantomInstance {
		result.Issues = append(result.Issues, AdoptionIssue{
			Layer: "instance_id",
			Message: fmt.Sprintf("marker belongs to phantom_instance_id=%q but we are %q — refusing to co-opt another Phantom's Mox",
				marker.PhantomInstance, s.PhantomInstance),
		})
	}

	// Layer 2: boot_id is intentionally skipped during adoption.  boot_id
	// is regenerated per Start(), so a different boot_id is the EXPECTED
	// case for a pre-existing process adopted after a Phantom restart.
	// Layer 2 is only enforced *within a single Supervisor lifetime* –
	// after a successful adoption we update the marker's boot_id to our
	// own so any in-lifetime stale marker can be detected.

	// Layer 3: /proc/<pid>/stat starttime.
	startTicks, source, ok := readProcStartTime(marker.PID)
	if !ok {
		result.Issues = append(result.Issues, AdoptionIssue{
			Layer:   "process_starttime",
			Message: fmt.Sprintf("could not read /proc/%d/stat (OS does not expose it); refusing adoption to avoid PID-wrap false positive", marker.PID),
		})
	} else if marker.ProcessStartTime > 0 && startTicks != marker.ProcessStartTime {
		result.Issues = append(result.Issues, AdoptionIssue{
			Layer: "process_starttime",
			Message: fmt.Sprintf("marker says starttime=%d (ticks) but kernel says %d (%s); PID has wrapped – refusing adoption",
				marker.ProcessStartTime, startTicks, source),
		})
	}

	// Layer 4: argv[0] must match mox basename.  Compare basenames, not
	// full paths, to tolerate PATH-based launches vs absolute-path launches.
	cmdlineTok0, ok := readProcCmdlineToken0(marker.PID)
	if !ok {
		result.Issues = append(result.Issues, AdoptionIssue{
			Layer:   "cmdline",
			Message: fmt.Sprintf("could not read /proc/%d/cmdline; refusing adoption", marker.PID),
		})
	} else if s.BinaryPath != "" {
		want := filepath.Base(s.BinaryPath)
		got := filepath.Base(cmdlineTok0)
		// Also accept argv[0] == "mox" even if BinaryPath is a versioned
		// install like "mox-v0.9.2" – the on-disk file is named "mox" and
		// argv[0] usually reflects that.
		if got != want && got != "mox" && !strings.HasPrefix(got, "mox") {
			result.Issues = append(result.Issues, AdoptionIssue{
				Layer: "cmdline",
				Message: fmt.Sprintf("cmdline token 0 = %q does not look like mox (expected %q or mox*); refusing adoption",
					cmdlineTok0, want),
			})
		}
	}

	if len(result.Issues) > 0 {
		return result, nil
	}

	// All checks passed.  Take ownership.
	newBoot, err := GenerateBootID()
	if err != nil {
		return nil, fmt.Errorf("adopt: generate new boot_id: %w", err)
	}
	s.bootID = newBoot
	s.processStartNS = time.Now().UnixNano()
	// Build a synthetic *exec.Cmd-like handle using os.FindProcess so
	// Stop()/signal() work identically whether we started or adopted.
	proc, ferr := os.FindProcess(marker.PID)
	if ferr != nil {
		result.Issues = append(result.Issues, AdoptionIssue{
			Layer:   "pid_dead",
			Message: fmt.Sprintf("os.FindProcess(%d) failed post-adoption: %v", marker.PID, ferr),
		})
		return result, nil
	}
	s.cmd = &exec.Cmd{Process: proc, ProcessState: nil}
	s.adopted = true
	s.waitDone = make(chan WaitResult, 1)
	s.waitGoroutine = true
	waitDone := s.waitDone
	// Spawn the wait goroutine asynchronously – we can't block Adopt()
	// which is called from the main boot path.
	s.waitWG.Add(1)
	go s.runWaitGoroutine(marker.PID, proc, waitDone)

	// Rewrite the marker with the new boot_id so the next Start() sees
	// this as the authoritative record.
	marker.BootID = newBoot
	marker.LaunchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if werr := writeMarker(s.markerPath, *marker); werr != nil {
		s.Log.Warn("adopt: rewriting marker with new boot_id failed", "error", werr, "path", s.markerPath)
	}

	result.Success = true
	result.StartNano = s.processStartNS
	if s.Log != nil {
		s.Log.Info("moxsupervisor: adopted orphan mox process",
			"pid", marker.PID,
			"boot_id", newBoot,
			"process_starttime", startTicks,
			"cmdline_token0", cmdlineTok0,
		)
	}
	return result, nil
}

// runWaitGoroutine is spawned for adopted processes so Wait() semantics
// (single message, then close) match the fresh-start path.  For adopted
// processes we cannot call cmd.Wait() (we didn't Start() it from Go), so
// we poll Signal(0) up to 1s intervals.
func (s *Supervisor) runWaitGoroutine(pid int, proc *os.Process, waitDone chan<- WaitResult) {
	defer s.waitWG.Done()
	defer close(waitDone)
	pollInterval := 1 * time.Second
	// On Linux, adopt-from-marker is rare enough that a 1s poll is fine;
	// the fresh-start path uses the native Wait() path (zero polling).
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !processExists(pid) {
			break
		}
	}
	// Try to reap the exit code – best-effort, only works when the pid
	// is our direct child (which for adopted processes it never is, since
	// the kernel re-parented orphans to init).  We therefore don't trust
	// the returned state.
	_ = proc.Release()
	s.mu.Lock()
	adopted := s.adopted
	boot := s.bootID
	s.waitGoroutine = false
	s.mu.Unlock()
	s.Log.Info("moxsupervisor: adopted process exited (detected via poll)",
		"pid", pid, "boot_id", boot, "adopted", adopted)
	select {
	case waitDone <- WaitResult{
		ExitCode: -1, // unknown
		ExitedAt:   time.Now(),
	}:
	default:
	}
}
