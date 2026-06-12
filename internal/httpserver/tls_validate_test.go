package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTLSPaths_NormalPair(t *testing.T) {
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")

	cleanCert, cleanKey, leaf, err := ValidateTLSPaths(cert, key, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Returned paths may be normalized via EvalSymlinks (e.g. macOS adds /private
	// prefix).  Compare with os.SameFile semantics + suffix match rather than
	// exact string equality.
	if !pathsSameFile(cleanCert, cert) {
		t.Errorf("cleanCert = %q does not reference cert %q", cleanCert, cert)
	}
	if !pathsSameFile(cleanKey, key) {
		t.Errorf("cleanKey = %q does not reference key %q", cleanKey, key)
	}
	if leaf == nil {
		t.Fatal("leaf certificate is nil")
	}
	if leaf.Subject.CommonName != "localhost" {
		t.Errorf("leaf CN = %q", leaf.Subject.CommonName)
	}
}

func TestValidateTLSPaths_EmptyRejected(t *testing.T) {
	_, _, _, err := ValidateTLSPaths("", "", false)
	if err == nil {
		t.Error("expected error for empty paths")
	}
	_, _, _, err = ValidateTLSPaths("cert.pem", "  ", false)
	if err == nil {
		t.Error("expected error for whitespace key path")
	}
}

func TestValidateTLSPaths_NULByteRejected(t *testing.T) {
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	bad := cert + "\x00.pem"
	_, _, _, err := ValidateTLSPaths(bad, key, false)
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Errorf("expected NUL error, got %v", err)
	}
}

func TestValidateTLSPaths_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	linkCert := filepath.Join(dir, "link-cert.pem")
	symlinkTarget(t, cert, linkCert)

	_, _, _, err := ValidateTLSPaths(linkCert, key, false)
	if err == nil || !strings.Contains(err.Error(), "symlink_not_allowed") {
		t.Errorf("expected symlink error for cert, got %v", err)
	}

	// also test intermediate symlink via dir-link
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	realCert2 := filepath.Join(subdir, "c2.pem")
	realKey2 := filepath.Join(subdir, "k2.pem")
	// Copy files
	b, _ := os.ReadFile(cert)
	if err := os.WriteFile(realCert2, b, 0o644); err != nil {
		t.Fatal(err)
	}
	k, _ := os.ReadFile(key)
	if err := os.WriteFile(realKey2, k, 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "dirlink")
	symlinkTarget(t, subdir, linkDir)
	_, _, _, err = ValidateTLSPaths(filepath.Join(linkDir, "c2.pem"), realKey2, false)
	if err == nil || !strings.Contains(err.Error(), "symlink_not_allowed") {
		t.Errorf("expected intermediate dir symlink error, got %v", err)
	}
}

func TestValidateTLSPaths_WorldWritableKeyRejected(t *testing.T) {
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	if err := os.Chmod(key, 0o666); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ValidateTLSPaths(cert, key, false)
	if err == nil || !strings.Contains(err.Error(), "key_file_world_writable") {
		t.Errorf("expected world_writable error, got %v", err)
	}
}

func TestValidateTLSPaths_DirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	_, _, _, err := ValidateTLSPaths(dir, key, false)
	if err == nil {
		t.Error("expected error when cert path is directory")
	}
	_, _, _, err = ValidateTLSPaths(cert, dir, false)
	if err == nil {
		t.Error("expected error when key path is directory")
	}
}

func TestValidateTLSPaths_CertKeyMismatchRejected(t *testing.T) {
	cert, _ := generateSelfSigned(t, t.TempDir(), "host1")
	_, key2 := generateSelfSigned(t, t.TempDir(), "host2")

	_, _, _, err := ValidateTLSPaths(cert, key2, false)
	if err == nil {
		t.Error("expected error for mismatched cert/key pair")
	}
}

func TestValidateTLSPaths_OwnerCheckSkipsOnWindowsAndFalse(t *testing.T) {
	// With checkOwner=false, valid files should always succeed regardless of
	// ownership (which we cannot easily simulate without privileges), and no
	// error about "owner" should surface in the happy path.
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	_, _, _, err := ValidateTLSPaths(cert, key, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
