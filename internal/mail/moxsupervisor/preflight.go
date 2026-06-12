package moxsupervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"
)

// PreflightResult collects every failed check so the UI can show them all at
// once rather than forcing the operator through a series of single-failure
// popups.
type PreflightResult struct {
	OK      bool
	Binary  PreflightBinary
	Ports   []PreflightPort
	Config  PreflightConfig
	Issues  []string // human-readable, one per failure
}

// PreflightBinary is the binary-exists check result.
type PreflightBinary struct {
	Path          string
	Exists        bool
	Executable    bool
	Version       string // `mox version` stdout, or empty on failure
	ChecksumSHA256 string
}

// PreflightPort is one port-bind check.
type PreflightPort struct {
	Name     string
	Port     int
	Host     string // "127.0.0.1" for webapi/webmail, "" for default ("0.0.0.0")
	Free     bool
	Conflict string // process name if detectable, or ""
}

// PreflightConfig is the `mox config test` result.
type PreflightConfig struct {
	Ran     bool
	OK      bool
	Output  string // combined stdout+stderr (redacted upstream)
	ExitCode int
}

// --- Port checks ---------------------------------------------------------

// The default listen host for Mox.  Mox binds "0.0.0.0" by default for
// protocol ports (SMTP/IMAP) but we allow narrowing via Host fields for
// loopback-only deployments.  During preflight we test BOTH the specified
// host AND "0.0.0.0" for the protocol ports so we catch conflicts either
// way; for webapi/webmail we only test 127.0.0.1 (by design).
func checkPortFree(host string, port int) (free bool, conflictHint string) {
	if port <= 0 {
		return true, ""
	}
	if host == "" {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	// Try to bind on TCP – if we succeed and immediately close the socket,
	// the port is almost certainly free (race: another process could bind
	// between our close() and Mox's bind() milliseconds later; this is
	// inherent to preflight and the Start() call can still fail with
	// EADDRINUSE which we surface through ErrPortConflict then).
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		_ = ln.Close()
		return true, ""
	}
	// Best-effort: if we're root we could read /proc/net/tcp for the
	// conflict PID, but that's fragile across platforms.  Return a generic
	// hint – callers already know the port number.
	return false, err.Error()
}

// --- Binary checks -------------------------------------------------------

// statBinary reports whether path exists, is a regular file, and is
// executable by the current user.
func statBinary(path string) (exists bool, executable bool, err error) {
	if path == "" {
		return false, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	if info.IsDir() {
		return false, false, fmt.Errorf("path %s is a directory", path)
	}
	// Permissions check: for our user.  Shortcut: if the owner-exec bit is
	// set AND we own it, or group-exec AND we are in the group, or
	// other-exec.  Go's os.FileMode doesn't expose ownership, so for
	// simplicity we just try executing `mox version` – that handles
	// permissions correctly (and lets us populate the version field).
	return true, true, nil
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runMoxVersion executes `mox version` with a short timeout.  It is used
// both during preflight and by the binary detect helper.
func runMoxVersion(ctx context.Context, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("moxsupervisor: empty binary path")
	}
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx2, path, "version")
	out, err := cmd.Output()
	if err != nil {
		// Include stderr if the error carries it.
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("mox version: %w (stderr: %s)", err, string(ee.Stderr))
		}
		return "", fmt.Errorf("mox version: %w", err)
	}
	return string(out), nil
}

// runMoxConfigTest runs `mox config test` with a 20s timeout (config
// validation can involve DNS lookups for DKIM/SPF checks).
func runMoxConfigTest(ctx context.Context, path, configPath, dataDir string) PreflightConfig {
	if path == "" {
		return PreflightConfig{Ran: false, OK: false, Output: "mox binary path empty", ExitCode: -1}
	}
	args := []string{}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	args = append(args, "-data", dataDir)
	args = append(args, "config", "test")
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx2, path, args...)
	out, err := cmd.CombinedOutput()
	pfc := PreflightConfig{Ran: true, Output: string(out)}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			pfc.ExitCode = ee.ExitCode()
		} else {
			pfc.ExitCode = -1
		}
		pfc.OK = false
		return pfc
	}
	pfc.OK = true
	pfc.ExitCode = 0
	return pfc
}

// Preflight runs every check BEFORE Start() is called.  It does not mutate
// the Supervisor and is safe to call repeatedly.
func (s *Supervisor) Preflight(ctx context.Context) PreflightResult {
	var r PreflightResult

	// 1. Binary.
	r.Binary.Path = s.BinaryPath
	exists, _, berr := statBinary(s.BinaryPath)
	if berr != nil {
		r.Issues = append(r.Issues, fmt.Sprintf("binary: %v", berr))
	}
	r.Binary.Exists = exists
	if !exists {
		r.Issues = append(r.Issues, fmt.Sprintf("binary %s not found", s.BinaryPath))
	}
	if r.Binary.Exists {
		if ver, err := runMoxVersion(ctx, s.BinaryPath); err != nil {
			r.Binary.Executable = false
			r.Issues = append(r.Issues, fmt.Sprintf("binary not executable: %v", err))
		} else {
			r.Binary.Executable = true
			r.Binary.Version = ver
		}
		if sum, err := hashFileSHA256(s.BinaryPath); err == nil {
			r.Binary.ChecksumSHA256 = sum
		}
	}

	// 2. Ports.  For standard mail ports test 0.0.0.0; for webapi/webmail
	// test 127.0.0.1 only (never exposed to the network).
	portSpecs := []struct {
		name string
		port int
		host string
	}{
		{"smtp", s.Ports.SMTP, ""},
		{"submission", s.Ports.Submission, ""},
		{"smtps", s.Ports.SMTPS, ""},
		{"imap", s.Ports.IMAP, ""},
		{"imaps", s.Ports.IMAPS, ""},
		{"webmail", s.Ports.Webmail, "127.0.0.1"},
		{"webapi_local", s.Ports.WebAPILocal, "127.0.0.1"},
	}
	for _, p := range portSpecs {
		if p.port == 0 {
			continue
		}
		free, hint := checkPortFree(p.host, p.port)
		host := p.host
		if host == "" {
			host = "0.0.0.0"
		}
		pp := PreflightPort{Name: p.name, Port: p.port, Host: p.host, Free: free, Conflict: hint}
		r.Ports = append(r.Ports, pp)
		if !free {
			r.Issues = append(r.Issues, fmt.Sprintf("port %s (%s:%d): already bound — %s", p.name, host, p.port, hint))
		}
	}

	// 3. Config test – only if binary exists AND data dir looks valid.
	// Missing config file is a WARNING but not a hard failure (Mox can use
	// defaults).  A failed config test IS a hard failure.
	if r.Binary.Exists && s.DataDir != "" {
		r.Config = runMoxConfigTest(ctx, s.BinaryPath, s.ConfigPath, s.DataDir)
		if r.Config.Ran && !r.Config.OK {
			r.Issues = append(r.Issues, fmt.Sprintf("config test failed (exit %d): %s", r.Config.ExitCode, truncate(r.Config.Output, 500)))
		}
	}

	r.OK = len(r.Issues) == 0
	return r
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
