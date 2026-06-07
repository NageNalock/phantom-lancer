package codexclient

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Status levels for the local codex CLI installation.
const (
	StatusReady       = "ready"
	StatusDegraded    = "degraded"
	StatusNeedsSetup  = "needs_setup"
	StatusUnavailable = "unavailable"
)

// Capabilities is a redacted summary of what the local codex CLI can do.
type Capabilities struct {
	BinaryFound  bool   `json:"binaryFound"`
	Version      string `json:"version"`
	AppServer    bool   `json:"appServer"`
	Exec         bool   `json:"exec"`
	Doctor       bool   `json:"doctor"`
	AuthState    string `json:"authState"` // logged_in | logged_out | unknown
	SandboxState string `json:"sandboxState"`
}

// DetectionResult is the outcome of probing the local environment.
type DetectionResult struct {
	BinaryPath     string         `json:"binaryPath"`
	Version        string         `json:"version"`
	Status         string         `json:"status"`
	Capabilities   Capabilities   `json:"capabilities"`
	DoctorSummary  map[string]any `json:"doctorSummary"`
	LastProbeError string         `json:"lastProbeError,omitempty"`
}

// Detector probes the local codex CLI binary and reports redacted capabilities.
type Detector struct {
	binaryPath func() string
	codexHome  func() string
}

func NewDetector(binaryPath func() string, codexHome func() string) *Detector {
	return &Detector{binaryPath: binaryPath, codexHome: codexHome}
}

// ResolveBinary returns the configured binary path or looks up `codex` in PATH.
func (d *Detector) ResolveBinary() string {
	if configured := strings.TrimSpace(d.binaryPath()); configured != "" {
		return configured
	}
	if path, err := exec.LookPath("codex"); err == nil {
		return path
	}
	return ""
}

// Detect runs a bounded sequence of probes against the codex CLI. It never
// records raw stderr, tokens or environment, only redacted summaries.
func (d *Detector) Detect(ctx context.Context) DetectionResult {
	result := DetectionResult{Status: StatusUnavailable, DoctorSummary: map[string]any{}}
	binary := d.ResolveBinary()
	if binary == "" {
		result.Status = StatusNeedsSetup
		result.LastProbeError = "codex binary not found in PATH"
		return result
	}
	result.BinaryPath = binary
	result.Capabilities.BinaryFound = true

	version, err := d.run(ctx, binary, 5*time.Second, "--version")
	if err != nil {
		result.Status = StatusUnavailable
		result.LastProbeError = Redact("version probe failed: "+err.Error(), 200)
		return result
	}
	result.Version = parseVersion(version)
	result.Capabilities.Version = result.Version

	// app-server help probe: presence of the subcommand indicates capability.
	if out, err := d.run(ctx, binary, 5*time.Second, "app-server", "--help"); err == nil {
		result.Capabilities.AppServer = true
		_ = out
	}
	// exec help probe.
	if _, err := d.run(ctx, binary, 5*time.Second, "exec", "--help"); err == nil {
		result.Capabilities.Exec = true
	}
	// doctor probe is best-effort; capture only redacted, bounded summary.
	if out, err := d.run(ctx, binary, 8*time.Second, "doctor"); err == nil {
		result.Capabilities.Doctor = true
		result.DoctorSummary["available"] = true
		result.Capabilities.AuthState = parseAuthState(out)
		result.Capabilities.SandboxState = parseSandboxState(out)
	} else {
		result.DoctorSummary["available"] = false
		result.Capabilities.AuthState = "unknown"
		result.Capabilities.SandboxState = "unknown"
	}

	result.Status = classifyStatus(result.Capabilities)
	return result
}

func (d *Detector) run(ctx context.Context, binary string, timeout time.Duration, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, binary, args...)
	cmd.Env = BuildChildEnv(d.codexHome())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func classifyStatus(caps Capabilities) string {
	if !caps.BinaryFound {
		return StatusNeedsSetup
	}
	if caps.AuthState == "logged_out" {
		return StatusNeedsSetup
	}
	if caps.AppServer {
		return StatusReady
	}
	if caps.Exec {
		return StatusDegraded
	}
	return StatusUnavailable
}

func parseVersion(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	line := strings.SplitN(out, "\n", 2)[0]
	fields := strings.Fields(line)
	for _, field := range fields {
		if len(field) > 0 && field[0] >= '0' && field[0] <= '9' {
			return field
		}
	}
	return strings.TrimSpace(line)
}

func parseAuthState(out string) string {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "logged in"), strings.Contains(lower, "authenticated"), strings.Contains(lower, "signed in"):
		return "logged_in"
	case strings.Contains(lower, "logged out"), strings.Contains(lower, "not logged in"), strings.Contains(lower, "not authenticated"), strings.Contains(lower, "please login"), strings.Contains(lower, "please log in"):
		return "logged_out"
	default:
		return "unknown"
	}
}

func parseSandboxState(out string) string {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "bubblewrap") && strings.Contains(lower, "not"):
		return "unavailable"
	case strings.Contains(lower, "sandbox") && strings.Contains(lower, "ok"):
		return "available"
	case strings.Contains(lower, "seatbelt"), strings.Contains(lower, "bubblewrap"):
		return "available"
	default:
		return "unknown"
	}
}
