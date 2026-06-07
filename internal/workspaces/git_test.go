package workspaces

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeGitPathspecs(t *testing.T) {
	got, err := normalizeGitPathspecs([]string{" app.go ", "app.go", "src/../README.md", ""})
	if err != nil {
		t.Fatalf("normalizeGitPathspecs() error = %v", err)
	}
	want := []string{"app.go", "README.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("normalizeGitPathspecs() = %v, want %v", got, want)
	}

	for _, value := range []string{"/tmp/file", "../file", "src/../../file"} {
		if _, err := normalizeGitPathspecs([]string{value}); err == nil {
			t.Fatalf("normalizeGitPathspecs(%q) succeeded, want error", value)
		}
	}
}

func TestRunGitActionStageUnstageCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	root := t.TempDir()
	runGitTestCommand(t, root, "init")
	runGitTestCommand(t, root, "config", "user.email", "owner@example.test")
	runGitTestCommand(t, root, "config", "user.name", "Phantom Lancer Test")

	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage, err := RunGitAction(ctx, root, "stage", []string{"note.txt"}, "")
	if err != nil {
		t.Fatalf("stage error = %v", err)
	}
	if stage.Error != "" {
		t.Fatalf("stage git error = %s", stage.Error)
	}
	if !strings.Contains(stage.Status.Output, "A  note.txt") {
		t.Fatalf("stage status = %q, want staged file", stage.Status.Output)
	}

	unstage, err := RunGitAction(ctx, root, "unstage", []string{"note.txt"}, "")
	if err != nil {
		t.Fatalf("unstage error = %v", err)
	}
	if unstage.Error != "" {
		t.Fatalf("unstage git error = %s", unstage.Error)
	}
	if !strings.Contains(unstage.Status.Output, "?? note.txt") {
		t.Fatalf("unstage status = %q, want untracked file", unstage.Status.Output)
	}

	_, err = RunGitAction(ctx, root, "stage", []string{"note.txt"}, "")
	if err != nil {
		t.Fatalf("stage before commit error = %v", err)
	}
	commit, err := RunGitAction(ctx, root, "commit", nil, "add note")
	if err != nil {
		t.Fatalf("commit error = %v", err)
	}
	if commit.Error != "" {
		t.Fatalf("commit git error = %s", commit.Error)
	}
	if strings.Contains(commit.Status.Output, "note.txt") {
		t.Fatalf("commit status = %q, want clean status", commit.Status.Output)
	}
}

func runGitTestCommand(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
