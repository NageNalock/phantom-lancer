package workspaces

import (
	"context"
	"os/exec"
	"time"
)

type GitStatus struct {
	IsGit  bool   `json:"isGit"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func ReadGitStatus(ctx context.Context, root string) GitStatus {
	if !IsGitRepository(root) {
		return GitStatus{IsGit: false}
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "git", "status", "--short", "--branch")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return GitStatus{IsGit: true, Output: string(out), Error: err.Error()}
	}
	return GitStatus{IsGit: true, Output: string(out)}
}
