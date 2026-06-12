package probes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// L2Config inputs required by the L2ControlProbe.
type L2Config struct {
	// BinaryPath is the absolute path of the `mox` executable.
	BinaryPath string
	// ConfigPath is the path of the mox.conf file (passed via -config).
	ConfigPath string
	// DataDir is the mox data directory (passed via -data).
	DataDir string
	// Timeout bounds the `mox config list` subprocess.  Defaults to 15s if
	// zero (config list can take a second even on idle Mox because it
	// parses the full config).
	Timeout time.Duration
}

// L2ControlProbe runs `mox config list` against the configured mox binary
// and reports whether mox can read its own configuration.
//
// Exit code 0 + any stdout → StateGreen.
// Exit code != 0 → StateRed (mox will refuse to Start with a broken conf).
// Missing binary/config → StateUnknown.
//
// The probe is deliberately conservative: even if `config list` output is
// empty or weird, success (exit 0) is enough.  Callers that want deeper
// validation (e.g. domains present, TLS configured) should add domain-
// specific checks (L6 DNS, L8 TLS, etc.) rather than fattening L2.
type L2ControlProbe struct {
	cfg L2Config
}

// NewL2Control constructs a new L2ControlProbe.
func NewL2Control(cfg L2Config) *L2ControlProbe { return &L2ControlProbe{cfg: cfg} }

// Name implements Probe.
func (p *L2ControlProbe) Name() string { return "l2_control" }

// Layer implements Probe.
func (p *L2ControlProbe) Layer() int { return 2 }

// Run implements Probe.
func (p *L2ControlProbe) Run(ctx context.Context) (r Result) {
	r = Result{Name: p.Name(), Layer: p.Layer(), State: StateUnknown, StartedAt: time.Now()}
	defer func() { r.Duration = time.Since(r.StartedAt) }()

	if p.cfg.BinaryPath == "" || p.cfg.ConfigPath == "" || p.cfg.DataDir == "" {
		r.Message = "binary/config/data not configured (Mox setup not initialised)"
		return r
	}
	timeout := p.cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := []string{"mox"}
	if p.cfg.ConfigPath != "" {
		argv = append(argv, "-config", p.cfg.ConfigPath)
	}
	if p.cfg.DataDir != "" {
		argv = append(argv, "-data", p.cfg.DataDir)
	}
	argv = append(argv, "config", "list")

	cmd := exec.CommandContext(runCtx, p.cfg.BinaryPath, argv[1:]...)
	// NOTE: exec.CommandContext sets cmd.Path to BinaryPath AND cmd.Args to
	// [BinaryPath, argv[1:]...].  That's the correct argv shape – argv[0]
	// will be BinaryPath on the child.
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err == nil {
		lines := strings.Count(strings.TrimSpace(stdout.String()), "\n")
		r.State = StateGreen
		r.Message = fmt.Sprintf("mox config list succeeded (%d domains/sections)", lines+1)
		return r
	}

	// Classify failure.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// Mox `config list` returns exit code 1 on syntax errors with a
		// useful stderr message.  Prefer stderr over stdout in the UI.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = fmt.Sprintf("exit code %d with no output", ee.ExitCode())
		} else if len(msg) > 240 {
			msg = msg[:240] + "…"
		}
		r.State = StateRed
		r.Message = "mox config list: " + msg
		r.Err = err
		return r
	}

	// Non-ExitError: exec failed (binary missing, permission denied, etc.)
	if strings.Contains(err.Error(), "permission denied") {
		r.Message = fmt.Sprintf("cannot execute %s: permission denied", p.cfg.BinaryPath)
		r.Err = err
		return r
	}
	if strings.Contains(err.Error(), "no such file or directory") || strings.Contains(err.Error(), "executable file not found") {
		r.Message = fmt.Sprintf("binary %s missing or not executable", p.cfg.BinaryPath)
		r.Err = err
		return r
	}
	r.State = StateUnknown
	r.Message = fmt.Sprintf("mox config list failed: %v", err)
	r.Err = err
	return r
}
