package moxcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// Runner is the central execution wrapper for the `mox` binary.
//
// Hard rule: every invocation uses exec.Command(binaryPath, args…)
// with shell DISABLED.  We never invoke sh -c or equivalent.
type Runner struct {
	BinaryPath string
	ConfigPath string
	DataDir    string
	// Stdin is an optional prefilled body; helpers usually prefer pipe.
	Stdin []byte
}

// cmd builds a *exec.Cmd with the common prefix args:
//
//	[-config, r.ConfigPath, -data-dir, r.DataDir, subcmd] + extraArgs
//
// Empty ConfigPath / DataDir → the matching flag is omitted.
func (r *Runner) cmd(ctx context.Context, subcmd string, extraArgs []string) *exec.Cmd {
	args := make([]string, 0, 8+len(extraArgs))
	if r.ConfigPath != "" {
		args = append(args, "-config", r.ConfigPath)
	}
	if r.DataDir != "" {
		args = append(args, "-data-dir", r.DataDir)
	}
	args = append(args, subcmd)
	args = append(args, extraArgs...)
	c := exec.CommandContext(ctx, r.BinaryPath, args...)
	return c
}

// run executes the command and returns captured stdout/stderr and exit code.
// exitCode is -1 if the error is unrelated to a nonzero exit.
func (r *Runner) run(ctx context.Context, subcmd string, extraArgs []string) (stdout, stderr string, exitCode int, err error) {
	cmd := r.cmd(ctx, subcmd, extraArgs)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if len(r.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(r.Stdin)
	}
	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if runErr == nil {
		return stdout, stderr, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return stdout, stderr, exitErr.ExitCode(), runErr
	}
	return stdout, stderr, -1, runErr
}

// runWithStdinPipe spawns the command with a writable stdin pipe,
// calls writeStdin in a goroutine, then waits.
//
// Used for `mox setaccountpassword` and `mox account add` so the
// password never reaches argv or /proc/<pid>/cmdline.
func (r *Runner) runWithStdinPipe(
	ctx context.Context,
	subcmd string,
	extraArgs []string,
	writeStdin func(io.WriteCloser) error,
) (stdout, stderr string, exitCode int, err error) {
	cmd := r.cmd(ctx, subcmd, extraArgs)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	pipe, pipeErr := cmd.StdinPipe()
	if pipeErr != nil {
		return "", "", -1, fmt.Errorf("moxcli: stdin pipe: %w", pipeErr)
	}

	if startErr := cmd.Start(); startErr != nil {
		_ = pipe.Close()
		return "", "", -1, fmt.Errorf("moxcli: cmd start: %w", startErr)
	}

	var writeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { _ = pipe.Close() }()
		writeErr = writeStdin(pipe)
	}()
	wg.Wait()

	waitErr := cmd.Wait()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if waitErr == nil {
		if writeErr != nil {
			return stdout, stderr, 0, fmt.Errorf("moxcli: write stdin: %w", writeErr)
		}
		return stdout, stderr, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		combined := waitErr
		if writeErr != nil {
			combined = fmt.Errorf("%w (write-stdin: %v)", waitErr, writeErr)
		}
		return stdout, stderr, exitErr.ExitCode(), combined
	}
	return stdout, stderr, -1, waitErr
}
