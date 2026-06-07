package workspaces

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type GitStatus struct {
	IsGit     bool   `json:"isGit"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

type GitDiff struct {
	IsGit     bool   `json:"isGit"`
	Staged    bool   `json:"staged"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

type GitActionResult struct {
	Action    string    `json:"action"`
	Output    string    `json:"output,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Error     string    `json:"error,omitempty"`
	Status    GitStatus `json:"status"`
}

func ReadGitStatus(ctx context.Context, root string) GitStatus {
	if !IsGitRepository(root) {
		return GitStatus{IsGit: false}
	}

	out, stderr, truncated, err := runGitCapped(ctx, root, 5*time.Second, 64*1024, "status", "--short", "--branch")
	if err != nil {
		return GitStatus{IsGit: true, Output: out, Truncated: truncated, Error: gitError(err, stderr)}
	}
	return GitStatus{IsGit: true, Output: out, Truncated: truncated}
}

func ReadGitDiff(ctx context.Context, root string, staged bool) GitDiff {
	if !IsGitRepository(root) {
		return GitDiff{IsGit: false, Staged: staged}
	}
	args := []string{"diff", "--no-ext-diff", "--"}
	if staged {
		args = []string{"diff", "--cached", "--no-ext-diff", "--"}
	}
	out, stderr, truncated, err := runGitCapped(ctx, root, 8*time.Second, 240*1024, args...)
	if err != nil {
		return GitDiff{IsGit: true, Staged: staged, Output: out, Truncated: truncated, Error: gitError(err, stderr)}
	}
	return GitDiff{IsGit: true, Staged: staged, Output: out, Truncated: truncated}
}

func RunGitAction(ctx context.Context, root, action string, paths []string, message string) (GitActionResult, error) {
	action = strings.TrimSpace(action)
	if !IsGitRepository(root) {
		return GitActionResult{}, errors.New("workspace is not a git repository")
	}
	normalizedPaths, err := normalizeGitPathspecs(paths)
	if err != nil {
		return GitActionResult{}, err
	}
	var args []string
	switch action {
	case "stage":
		if len(normalizedPaths) == 0 {
			normalizedPaths = []string{"."}
		}
		args = append([]string{"--literal-pathspecs", "add", "--"}, normalizedPaths...)
	case "unstage":
		if len(normalizedPaths) == 0 {
			normalizedPaths = []string{"."}
		}
		args = append([]string{"--literal-pathspecs", "reset", "--"}, normalizedPaths...)
	case "commit":
		message = strings.TrimSpace(message)
		if message == "" {
			return GitActionResult{}, errors.New("commit message is required")
		}
		if len(message) > 500 {
			return GitActionResult{}, errors.New("commit message is too long")
		}
		args = []string{"commit", "-m", message}
	default:
		return GitActionResult{}, errors.New("unsupported git action")
	}

	out, stderr, truncated, runErr := runGitCapped(ctx, root, 30*time.Second, 120*1024, args...)
	result := GitActionResult{
		Action:    action,
		Output:    out,
		Truncated: truncated,
		Status:    ReadGitStatus(ctx, root),
	}
	if runErr != nil {
		result.Error = gitError(runErr, stderr)
	}
	return result, nil
}

func normalizeGitPathspecs(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.ContainsRune(value, 0) {
			return nil, errors.New("path contains an invalid byte")
		}
		if filepath.IsAbs(value) {
			return nil, errors.New("absolute paths are not allowed")
		}
		cleaned := filepath.Clean(value)
		if cleaned == "." {
			if !seen[cleaned] {
				out = append(out, cleaned)
				seen[cleaned] = true
			}
			continue
		}
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return nil, errors.New("paths must stay inside the workspace")
		}
		cleaned = filepath.ToSlash(cleaned)
		if cleaned == "" {
			continue
		}
		if !seen[cleaned] {
			out = append(out, cleaned)
			seen[cleaned] = true
		}
	}
	return out, nil
}

func runGitCapped(ctx context.Context, root string, timeout time.Duration, limit int, args ...string) (string, string, bool, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "git", args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", "", false, err
	}

	var wg sync.WaitGroup
	var out, errOut string
	var outTruncated, errTruncated bool
	wg.Add(2)
	go func() {
		defer wg.Done()
		out, outTruncated = readCapped(stdout, limit)
	}()
	go func() {
		defer wg.Done()
		errOut, errTruncated = readCapped(stderr, 16*1024)
	}()
	waitErr := cmd.Wait()
	wg.Wait()
	if errTruncated {
		errOut += "...[truncated]"
	}
	if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) && waitErr != nil {
		return out, errOut, true, timeoutCtx.Err()
	}
	return out, errOut, outTruncated, waitErr
}

func readCapped(reader io.Reader, limit int) (string, bool) {
	if limit <= 0 {
		limit = 64 * 1024
	}
	var buffer bytes.Buffer
	chunk := make([]byte, 8192)
	written := 0
	truncated := false
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			if written < limit {
				keep := n
				if written+n > limit {
					keep = limit - written
					truncated = true
				}
				buffer.Write(chunk[:keep])
				written += keep
			} else {
				truncated = true
			}
		}
		if err != nil {
			break
		}
	}
	if truncated {
		buffer.WriteString("...[truncated]")
	}
	return buffer.String(), truncated
}

func gitError(err error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return stderr
	}
	return err.Error()
}
