package workspaces

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeWorkspacePathAllowsAllowedRoot(t *testing.T) {
	root := t.TempDir()
	allowedRoots, err := NormalizeAllowedRoots([]string{root})
	if err != nil {
		t.Fatalf("NormalizeAllowedRoots() error = %v", err)
	}

	got, err := NormalizeWorkspacePath(allowedRoots, root)
	if err != nil {
		t.Fatalf("NormalizeWorkspacePath() error = %v", err)
	}
	if got != allowedRoots[0] {
		t.Fatalf("NormalizeWorkspacePath() = %q, want %q", got, allowedRoots[0])
	}
}

func TestNormalizeWorkspacePathAllowsChildDirectory(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "project")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	allowedRoots, err := NormalizeAllowedRoots([]string{root})
	if err != nil {
		t.Fatalf("NormalizeAllowedRoots() error = %v", err)
	}

	got, err := NormalizeWorkspacePath(allowedRoots, child)
	if err != nil {
		t.Fatalf("NormalizeWorkspacePath() error = %v", err)
	}
	expected, err := filepath.EvalSymlinks(child)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if got != expected {
		t.Fatalf("NormalizeWorkspacePath() = %q, want %q", got, expected)
	}
}

func TestNormalizeWorkspacePathRejectsOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	allowedRoots, err := NormalizeAllowedRoots([]string{root})
	if err != nil {
		t.Fatalf("NormalizeAllowedRoots() error = %v", err)
	}

	_, err = NormalizeWorkspacePath(allowedRoots, outside)
	if !errors.Is(err, ErrPathOutOfBoundary) {
		t.Fatalf("NormalizeWorkspacePath() error = %v, want ErrPathOutOfBoundary", err)
	}
}

func TestNormalizeWorkspacePathForCreateAllowsMissingChild(t *testing.T) {
	root := t.TempDir()
	allowedRoots, err := NormalizeAllowedRoots([]string{root})
	if err != nil {
		t.Fatalf("NormalizeAllowedRoots() error = %v", err)
	}
	target := filepath.Join(root, "new", "project")

	got, err := NormalizeWorkspacePathForCreate(allowedRoots, target)
	if err != nil {
		t.Fatalf("NormalizeWorkspacePathForCreate() error = %v", err)
	}
	expected := filepath.Join(allowedRoots[0], "new", "project")
	if got != expected {
		t.Fatalf("NormalizeWorkspacePathForCreate() = %q, want %q", got, expected)
	}
}

func TestNormalizeWorkspacePathForCreateRejectsOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "new-project")
	allowedRoots, err := NormalizeAllowedRoots([]string{root})
	if err != nil {
		t.Fatalf("NormalizeAllowedRoots() error = %v", err)
	}

	_, err = NormalizeWorkspacePathForCreate(allowedRoots, outside)
	if !errors.Is(err, ErrPathOutOfBoundary) {
		t.Fatalf("NormalizeWorkspacePathForCreate() error = %v, want ErrPathOutOfBoundary", err)
	}
}
