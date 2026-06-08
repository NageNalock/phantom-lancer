package codexclient

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"phantom-lancer/internal/storage"
)

var (
	// ErrPathOutOfBoundary indicates a workspace path is not inside any allowed root.
	ErrPathOutOfBoundary = errors.New("path is outside allowed roots")
	// ErrPathNotFound indicates the workspace directory does not exist.
	ErrPathNotFound = errors.New("path does not exist")
	// ErrPathNotDirectory indicates the workspace path is not a directory.
	ErrPathNotDirectory = errors.New("path is not a directory")
	// ErrPolicyViolation indicates the requested sandbox/approval combination is
	// not permitted for the workspace trust state.
	ErrPolicyViolation = errors.New("requested policy not allowed for workspace trust state")
)

// WorkspacePolicy bounds Codex execution to the globally allowed roots and
// enforces sandbox/approval policy based on workspace trust state.
type WorkspacePolicy struct {
	allowedRoots func() ([]string, error)
}

func NewWorkspacePolicy(allowedRoots func() ([]string, error)) *WorkspacePolicy {
	return &WorkspacePolicy{allowedRoots: allowedRoots}
}

// NormalizeWorkspacePath canonicalizes a requested path and verifies it exists,
// is a directory and falls inside an allowed root.
func (p *WorkspacePolicy) NormalizeWorkspacePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ErrPathNotFound
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrPathNotFound, path)
		}
		return "", err
	}
	cleaned := filepath.Clean(resolved)
	info, err := os.Stat(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrPathNotFound, path)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrPathNotDirectory, path)
	}
	if err := p.ensureInsideAllowedRoots(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func (p *WorkspacePolicy) ensureInsideAllowedRoots(path string) error {
	roots, err := p.allowedRoots()
	if err != nil {
		return err
	}
	for _, root := range roots {
		root = normalizeAllowedRoot(root)
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrPathOutOfBoundary, path)
}

func normalizeAllowedRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

// RunPolicy describes the resolved sandbox/approval/network combination for a turn.
type RunPolicy struct {
	Sandbox        string
	ApprovalPolicy string
	NetworkEnabled bool
}

// ResolveRunPolicy validates a requested sandbox/approval against the workspace
// trust state. It never produces full-access or yolo combinations.
func (p *WorkspacePolicy) ResolveRunPolicy(ws storage.CodexCliWorkspace, sandbox, approval string) (RunPolicy, error) {
	sandbox = strings.TrimSpace(sandbox)
	if sandbox == "" {
		sandbox = ws.DefaultSandbox
	}
	if sandbox == "" {
		sandbox = "read-only"
	}
	approval = strings.TrimSpace(approval)
	if approval == "" {
		approval = ws.DefaultApprovalPolicy
	}
	if approval == "" {
		approval = "on-request"
	}
	switch sandbox {
	case "read-only", "workspace-write":
	default:
		return RunPolicy{}, fmt.Errorf("%w: unsupported sandbox %q", ErrPolicyViolation, sandbox)
	}
	switch approval {
	case "on-request":
	default:
		return RunPolicy{}, fmt.Errorf("%w: unsupported approval policy %q; this console only supports on-request", ErrPolicyViolation, approval)
	}
	switch ws.TrustState {
	case "restricted", "untrusted":
		if sandbox != "read-only" {
			return RunPolicy{}, fmt.Errorf("%w: workspace is %s and only allows read-only", ErrPolicyViolation, ws.TrustState)
		}
	case "trusted":
		// trusted workspaces may use workspace-write + on-request.
	}
	network := false
	if ws.TrustState == "trusted" {
		if enabled, ok := ws.NetworkPolicy["enabled"].(bool); ok {
			network = enabled
		}
	}
	return RunPolicy{Sandbox: sandbox, ApprovalPolicy: approval, NetworkEnabled: network}, nil
}

// secretEnvHints are substrings used to drop any unknown secret-like environment
// variable when constructing the child process environment.
var secretEnvHints = []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "APIKEY", "API_KEY", "COOKIE", "PRIVATE_KEY", "ACCESS_KEY", "CREDENTIAL"}

// allowedEnvKeys are the environment variables explicitly forwarded to codex.
var allowedEnvKeys = []string{"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"}

// BuildChildEnv constructs an allowlisted environment for codex child processes.
// Phantom Lancer service secrets are never inherited; only a small known set of
// variables plus an optional explicit CODEX_HOME are forwarded.
func BuildChildEnv(codexHome string) []string {
	out := make([]string, 0, len(allowedEnvKeys)+1)
	for _, key := range allowedEnvKeys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			if isSecretEnvKey(key) {
				continue
			}
			out = append(out, key+"="+value)
		}
	}
	if strings.TrimSpace(codexHome) != "" {
		out = append(out, "CODEX_HOME="+codexHome)
	} else if value, ok := os.LookupEnv("CODEX_HOME"); ok && value != "" {
		out = append(out, "CODEX_HOME="+value)
	}
	return out
}

func isSecretEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, hint := range secretEnvHints {
		if strings.Contains(upper, hint) {
			return true
		}
	}
	return false
}
