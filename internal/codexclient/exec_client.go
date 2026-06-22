package codexclient

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

// ExecOptions configures a single `codex exec --json` invocation.
type ExecOptions struct {
	Binary    string
	CodexHome string
	Cwd       string
	Sandbox   string
	Approval  string
	Model     string
	Prompt    string
	Images    []string
}

// ExecClient runs one-shot `codex exec --json` tasks as the degraded fallback
// when the app-server is unavailable.
type ExecClient struct{}

func NewExecClient() *ExecClient { return &ExecClient{} }

// BuildArgs constructs the argument vector. It never emits --yolo or
// --dangerously-bypass-approvals-and-sandbox.
func (c *ExecClient) BuildArgs(opts ExecOptions) []string {
	sandbox := strings.TrimSpace(opts.Sandbox)
	if sandbox == "" {
		sandbox = "read-only"
	}
	// ponytail: current Codex CLI exposes no approval flag; keep opts compatible and let CLI config handle approvals.
	args := []string{"exec", "--json", "--sandbox", sandbox}
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	for _, image := range opts.Images {
		if strings.TrimSpace(image) != "" {
			args = append(args, "--image", image)
		}
	}
	args = append(args, opts.Prompt)
	return args
}

// Run executes the task and streams parsed JSONL lines to onLine until the
// process exits. The raw process output is never persisted; callers map lines
// to stable events.
func (c *ExecClient) Run(ctx context.Context, opts ExecOptions, onLine func([]byte)) error {
	args := c.BuildArgs(opts)
	cmd := exec.CommandContext(ctx, opts.Binary, args...)
	cmd.Env = BuildChildEnv(opts.CodexHome)
	if strings.TrimSpace(opts.Cwd) != "" {
		cmd.Dir = opts.Cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		onLine(cp)
	}
	return cmd.Wait()
}
