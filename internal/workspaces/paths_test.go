package workspaces

import (
	"testing"
)

func TestNormalizeAllowedRootsCanonicalizesExistingDir(t *testing.T) {
	root := t.TempDir()
	got, err := NormalizeAllowedRoots([]string{root})
	if err != nil {
		t.Fatalf("NormalizeAllowedRoots() error = %v", err)
	}
	if len(got) != 1 || got[0] == "" {
		t.Fatalf("NormalizeAllowedRoots() = %v, want single canonical path", got)
	}
}

func TestNormalizeAllowedRootsRejectsMissingDir(t *testing.T) {
	_, err := NormalizeAllowedRoots([]string{"/this/path/should/not/exist/pl"})
	if err == nil {
		t.Fatalf("NormalizeAllowedRoots() error = nil, want error for missing dir")
	}
}
