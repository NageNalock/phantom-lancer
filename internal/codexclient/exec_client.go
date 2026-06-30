package codexclient

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
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
	// Run codex in its own process group (same pattern as supervisor.go): when
	// the main process exits we can reap descendants that inherited stdout.
	// Otherwise a stray grandchild keeps the stdout pipe write-end open and the
	// scanner below never sees EOF.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	pgid := 0
	if cmd.Process != nil {
		pgid = cmd.Process.Pid
	}
	scanDone := make(chan struct{}, 1)
	go func() {
		defer func() { scanDone <- struct{}{} }()
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
	}()
	err = cmd.Wait()
	// 主进程已退出，但继承 stdout 的孙子进程（如 stray 子进程）可能仍持有 pipe 写端，
	// 让 scanner 永不 EOF。杀整个进程组释放写端：pipe 里已写入的行仍会被 scanner 读出
	// 后再 EOF，不会丢行——区别于直接 Close 读端（会丢弃未读 buffer）。复用 supervisor
	// 的进程组清理模式。
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	waitExecScanDone(scanDone, stdout)
	return err
}

func waitExecScanDone(done <-chan struct{}, stdout interface{ Close() error }) {
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		// ponytail: normal JSONL drains immediately; close inherited pipes instead of hanging on stray descendants.
		_ = stdout.Close()
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
		}
	}
}
