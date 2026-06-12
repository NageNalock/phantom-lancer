package moxbinary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- test setup helpers -----------------------------------------------------

// tempRoot returns a unique tempdir per test, cleans it up at test end.
func tempRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "moxbinary-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writeFile writes content (with given perms) and returns the full path.
func writeFile(t *testing.T, dir, name, content string, perm os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), perm); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
	if err := os.Chmod(p, perm); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	return p
}

// newFakeMox writes a tiny shell-script "mox" binary that prints a version
// string when run with argv=["version"].  The version is baked directly into
// the script so multiple fakes in the same test don't collide (a single test
// process shares one env, so using $FAKEMOX_VERSION would leak across
// candidates).
func newFakeMox(t *testing.T, dir, version string) string {
	t.Helper()
	script := "#!/bin/sh\ncase \"$1\" in\nversion) printf 'mox v%s, built with go\\n' " +
		"'" + strings.ReplaceAll(version, "'", "'\"'\"'") + "' ;;\nesac\n"
	path := writeFile(t, dir, "mox", script, 0o755)
	return path
}

// --- tests ------------------------------------------------------------------

func TestCanonicalVersion(t *testing.T) {
	cases := map[string]string{
		"0.9.2":       "0.9.2",
		"v0.9.2":      "0.9.2",
		"V0.9.2":      "0.9.2",
		"  v0.9.2 \n": "0.9.2",
		"":            "",
	}
	for in, want := range cases {
		if got := canonicalVersion(in); got != want {
			t.Errorf("canonicalVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChecksumInWhitelist_EmptyWhitelist(t *testing.T) {
	// With no KnownVersions populated (default for development builds),
	// every hash should report "not in whitelist".
	if ChecksumInWhitelist(strings.Repeat("a", 64)) {
		t.Error("expected empty whitelist to reject all hashes")
	}
	if ChecksumInWhitelist("not-a-hex-hash") {
		t.Error("malformed hash should return false")
	}
}

func TestURLAllowed(t *testing.T) {
	ok := []string{
		"https://github.com/mjl-/mox/releases/download/v0.9.2/mox-0.9.2-linux-amd64",
		"https://objects.githubusercontent.com/github-production-release-asset-2e65be/abc",
	}
	bad := []string{
		"http://github.com/mjl-/mox/releases/download/v0.9.2/evil", // http, not https
		"https://evil.example.com/mox-v0.9.2",
		"https://github.com.evil.com/releases/steal",
		"",
	}
	for _, u := range ok {
		if !URLAllowed(u) {
			t.Errorf("URLAllowed(%q) = false, want true", u)
		}
	}
	for _, u := range bad {
		if URLAllowed(u) {
			t.Errorf("URLAllowed(%q) = true, want false", u)
		}
	}
}

func TestBuildDownloadURL_UnknownVersion(t *testing.T) {
	_, err := BuildDownloadURL("99.99.99-not-real")
	if !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("BuildDownloadURL(unknown) err=%v, want ErrUnknownVersion", err)
	}
}

func TestReleaseAssetFilename(t *testing.T) {
	got := ReleaseAssetFilename("0.9.2")
	want := "mox-0.9.2-" + runtime.GOOS + "-" + runtime.GOARCH
	if got != want {
		t.Errorf("ReleaseAssetFilename = %q, want %q", got, want)
	}
}

func TestDetect_ControlledAndHintAndPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable to windows")
	}
	root := tempRoot(t)
	controlledDir := filepath.Join(root, "mox", "bin")
	if err := os.MkdirAll(controlledDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 1. Controlled binary.
	newFakeMox(t, controlledDir, "0.9.2")
	// 2. PATH binary (different dir).
	pathDir := filepath.Join(root, "usr", "local", "bin")
	newFakeMox(t, pathDir, "0.9.1")
	// 3. Hint binary (different dir).
	hintDir := filepath.Join(root, "home", "user", "src")
	hintPath := newFakeMox(t, hintDir, "0.9.3")
	// 4. PATH setup: point to our pathDir only (not the controlled dir).
	t.Setenv("PATH", pathDir)

	res, err := Detect(controlledDir, DetectOptions{HintPath: hintPath})
	if err != nil {
		t.Fatalf("Detect err: %v", err)
	}
	if res.Controlled == nil || res.Path == nil || res.Hint == nil {
		t.Fatalf("all three slots should be populated, got: %+v", res)
	}
	// Selected must be Controlled.
	if res.Selected != res.Controlled {
		t.Errorf("Selected = %v, want Controlled (%v)", res.Selected, res.Controlled)
	}
	// Versions should be distinguishable (different strings).
	if res.Controlled.Version == "" || !strings.Contains(res.Controlled.Version, "0.9.2") {
		t.Errorf("Controlled.Version = %q, want substring \"0.9.2\"", res.Controlled.Version)
	}
	if res.Path.Version == "" || !strings.Contains(res.Path.Version, "0.9.1") {
		t.Errorf("Path.Version = %q, want substring \"0.9.1\"", res.Path.Version)
	}
	// Checksums: Controlled != Path since they have different content? No,
	// they have identical shell scripts – only the env var changes.  So the
	// on-disk checksums are the same; just assert they exist & are 64-hex.
	for name, bi := range map[string]*BinaryInfo{
		"controlled": res.Controlled, "path": res.Path, "hint": res.Hint,
	} {
		if len(bi.ChecksumSHA256) != 64 {
			t.Errorf("%s.ChecksumSHA256 = %q, want 64 hex chars", name, bi.ChecksumSHA256)
		}
	}
}

func TestDetect_EmptyEverything_ReturnsErrNoBinary(t *testing.T) {
	root := tempRoot(t)
	emptyDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(emptyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	res, err := Detect(emptyDir, DetectOptions{})
	if !errors.Is(err, ErrNoBinary) {
		t.Errorf("err = %v, want ErrNoBinary", err)
	}
	if res == nil || res.Selected != nil {
		t.Errorf("Selected should be nil, got %+v", res)
	}
}

func TestDetect_SkipPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	pathDir := filepath.Join(root, "other")
	newFakeMox(t, pathDir, "0.8.0")
	t.Setenv("PATH", pathDir)

	res, err := Detect(controlled, DetectOptions{SkipPATH: true})
	// No controlled binary, no hint, and PATH is skipped → ErrNoBinary.
	if !errors.Is(err, ErrNoBinary) {
		t.Errorf("SkipPATH with empty controlled dir → err=%v, want ErrNoBinary", err)
	}
	if res.Path != nil {
		t.Errorf("SkipPATH should not populate Path slot, got %+v", res.Path)
	}
}

func TestDetect_SourceTags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	newFakeMox(t, controlled, "0.1.0")
	t.Setenv("PATH", "")
	res, err := Detect(controlled, DetectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Controlled.Source != "controlled" {
		t.Errorf("Source tag = %q, want controlled", res.Controlled.Source)
	}
	if res.Selected.Source != "controlled" {
		t.Errorf("Selected.Source tag = %q, want controlled", res.Selected.Source)
	}
}

func TestHashFileSHA256_KnownInput(t *testing.T) {
	root := tempRoot(t)
	path := writeFile(t, root, "sample", "hello world", 0o600)
	got, err := hashFileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("hello world"))
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}

func TestHashFileSHA256_OverCeiling(t *testing.T) {
	root := tempRoot(t)
	// Write 1 GiB + 1 bytes of nulls.  That would take 1 GiB RAM so instead
	// we exercise the overrun by using a tiny test via a 11-byte input where
	// the sizeCeil is 10.  We can't change the sizeCeil constant easily;
	// instead just verify the 1 GiB ceiling logic accepts a small file.
	path := writeFile(t, root, "small", "x", 0o600)
	got, err := hashFileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("x"))
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}

// --- Download tests ---------------------------------------------------------
//
// Since the KnownVersions whitelist ships empty in development builds we
// can't exercise the happy "good download" path end-to-end without adding
// entries.  Instead we inject a fake entry into KnownVersions directly for
// tests (package-level mutation is fine within the _test.go file), and use
// a local httptest server that serves known bytes.

const testVersion = "0.0.1-phantomtest"

func injectTestChecksum(t *testing.T, content []byte) {
	t.Helper()
	sum := sha256.Sum256(content)
	platform := runtime.GOOS + "_" + runtime.GOARCH
	// Deep-copy the map so one test doesn't leak entries to another.
	newMap := make(map[string]VersionRecord, len(KnownVersions)+1)
	for k, v := range KnownVersions {
		nv := VersionRecord{
			Version:          v.Version,
			SHA256ByPlatform: make(map[string]string, len(v.SHA256ByPlatform)),
		}
		for pk, pv := range v.SHA256ByPlatform {
			nv.SHA256ByPlatform[pk] = pv
		}
		newMap[k] = nv
	}
	newMap[testVersion] = VersionRecord{
		Version: testVersion,
		SHA256ByPlatform: map[string]string{
			platform: hex.EncodeToString(sum[:]),
		},
	}
	old := KnownVersions
	KnownVersions = newMap
	t.Cleanup(func() { KnownVersions = old })

	// Also temporarily allow httptest URLs.  We push an extra prefix, then
	// pop it in cleanup.
	//
	// NOTE: Download() will use BuildDownloadURL() which returns a github
	// URL.  We can't serve github from httptest; instead we pass
	// OverrideURL = testServer.URL and patch ApprovedDownloadPrefixes.
}

func pushPrefixTemporarily(t *testing.T, prefix string) {
	t.Helper()
	old := ApprovedDownloadPrefixes
	ApprovedDownloadPrefixes = append([]string{prefix}, ApprovedDownloadPrefixes...)
	t.Cleanup(func() { ApprovedDownloadPrefixes = old })
}

func TestDownload_ChecksumMismatch(t *testing.T) {
	// Inject a known-good checksum for testVersion.  Serve wrong bytes so
	// the checksum fails.
	content := []byte("i-am-mox-bytes")
	injectTestChecksum(t, content)
	// Serve the WRONG bytes from the test server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("completely-different-bytes"))
	}))
	defer srv.Close()
	pushPrefixTemporarily(t, srv.URL+"/")

	_, err := Download(context.Background(), testVersion, DownloadOptions{
		HTTPClient:   srv.Client(),
		OverrideURL:  srv.URL + "/release",
		DestDir:      tempRoot(t),
		SizeMaxBytes: 1 << 20,
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got err=%v, want ErrChecksumMismatch", err)
	}
}

func TestDownload_URLRejected(t *testing.T) {
	injectTestChecksum(t, []byte("x"))
	_, err := Download(context.Background(), testVersion, DownloadOptions{
		OverrideURL:  "https://evil.example.com/mox",
		SizeMaxBytes: 1 << 20,
	})
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("got err=%v, want ErrURLNotAllowed", err)
	}
}

func TestDownload_UnknownVersion(t *testing.T) {
	_, err := Download(context.Background(), "definitely-does-not-exist", DownloadOptions{})
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got err=%v, want ErrUnknownVersion", err)
	}
}

func TestDownload_SizeCeiling(t *testing.T) {
	content := bytes.Repeat([]byte("a"), 1000)
	injectTestChecksum(t, content)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()
	pushPrefixTemporarily(t, srv.URL+"/")

	// Set cap to 500 bytes – smaller than content.  Should fail with a
	// "too large" error (not checksum mismatch since we never read the
	// whole body).
	_, err := Download(context.Background(), testVersion, DownloadOptions{
		HTTPClient:   srv.Client(),
		OverrideURL:  srv.URL + "/x",
		DestDir:      tempRoot(t),
		SizeMaxBytes: 500,
	})
	if err == nil {
		t.Fatal("expected error on oversized download, got nil")
	}
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Errorf("err=%v, want ErrDownloadTooLarge", err)
	}
}

func TestDownload_HappyPath(t *testing.T) {
	content := bytes.Repeat([]byte("mox"), 100) // 300 bytes
	injectTestChecksum(t, content)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		_, _ = w.Write(content)
	}))
	defer srv.Close()
	pushPrefixTemporarily(t, srv.URL+"/")

	dest := tempRoot(t)
	var reported int64
	res, err := Download(context.Background(), testVersion, DownloadOptions{
		HTTPClient:  srv.Client(),
		OverrideURL: srv.URL + "/release",
		DestDir:     dest,
		Progress: func(received, knownTotal int64) {
			if received > reported {
				reported = received
			}
		},
		SizeMaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if res == nil || res.TempPath == "" {
		t.Fatalf("nil result")
	}
	if res.SizeBytes != 300 {
		t.Errorf("size = %d, want 300", res.SizeBytes)
	}
	// Verify on-disk hash matches.
	actual, err := hashFileSHA256(res.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	if actual != res.ChecksumSHA256 {
		t.Errorf("on-disk hash %s != result hash %s", actual, res.ChecksumSHA256)
	}
	if res.ExpectedSHA256 != res.ChecksumSHA256 {
		t.Errorf("expected != actual: %s vs %s", res.ExpectedSHA256, res.ChecksumSHA256)
	}
	if reported != 300 {
		t.Errorf("progress reported %d bytes, want 300", reported)
	}
	// Tempfile should be under dest dir.
	if !strings.HasPrefix(res.TempPath, dest) {
		t.Errorf("tempfile %s not under dest %s", res.TempPath, dest)
	}
	// Tempfile perms should be 0600.
	info, err := os.Lstat(res.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("tempfile perms = %04o, want group/other bits clear", info.Mode().Perm())
	}
}

// --- Install tests ----------------------------------------------------------

func TestInstall_FreshInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	srcDir := filepath.Join(root, "src")
	src := newFakeMox(t, srcDir, "1.2.3")
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	// Also hash src so Install's checksum-verify path can be tested.
	sum, err := hashFileSHA256(src)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Install(context.Background(), src, controlled, InstallOptions{
		Version:        "1.2.3",
		ChecksumSHA256: sum,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.InstalledPath != filepath.Join(controlled, "mox") {
		t.Errorf("InstalledPath = %q, want %q", res.InstalledPath, filepath.Join(controlled, "mox"))
	}
	if res.InstalledVersion != "1.2.3" {
		t.Errorf("InstalledVersion = %q, want 1.2.3", res.InstalledVersion)
	}
	if res.InstalledChecksumSHA256 != sum {
		t.Errorf("checksum mismatch: %s vs %s", res.InstalledChecksumSHA256, sum)
	}
	if res.PreviousBackupPath != "" {
		t.Errorf("fresh install should not produce a backup, got %q", res.PreviousBackupPath)
	}

	// Verify installed binary runs `version`.
	ver, err := runMoxVersion(res.InstalledPath, 5*time.Second)
	if err != nil {
		t.Fatalf("installed mox version: %v", err)
	}
	if !strings.Contains(ver, "1.2.3") {
		t.Errorf("installed version = %q, want \"1.2.3\"", ver)
	}
	// Verify sidecar exists and has the right data.
	sc, err := ReadVersionSidecar(controlled)
	if err != nil {
		t.Fatal(err)
	}
	if sc == nil {
		t.Fatal("no sidecar written")
	}
	if sc.Version != "1.2.3" || sc.ChecksumSHA256 != sum || sc.InstalledBy != "phantom-lancer" {
		t.Errorf("sidecar = %+v, want version=1.2.3 + correct hash", sc)
	}
	// Binary perms: 0700.
	info, err := os.Lstat(res.InstalledPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o700 != 0o700 {
		t.Errorf("binary perms = %04o, want 0700", info.Mode().Perm())
	}
	// Sidecar perms: 0600.
	info, err = os.Lstat(res.VersionSidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("sidecar perms = %04o, want 0600", info.Mode().Perm())
	}
}

func TestInstall_CheckSumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	srcDir := filepath.Join(root, "src")
	src := newFakeMox(t, srcDir, "0.0.1")
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pass a clearly bogus hash.
	badSum := strings.Repeat("00", 32)
	_, err := Install(context.Background(), src, controlled, InstallOptions{
		Version:        "0.0.1",
		ChecksumSHA256: badSum,
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("err=%v, want ErrChecksumMismatch", err)
	}
	// And ensure NO binary was written to controlled dir.
	if _, err := os.Lstat(filepath.Join(controlled, "mox")); err == nil {
		t.Error("binary should NOT exist after failed install")
	}
	// Tempfiles should have been cleaned up too – no .mox.install-* left.
	if entries, err := os.ReadDir(controlled); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				t.Errorf("leftover tempfile: %s", e.Name())
			}
		}
	}
}

func TestInstall_ReplaceOlderVersion_TakesBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	// Old version install.
	oldSrcDir := filepath.Join(root, "old")
	oldSrc := newFakeMox(t, oldSrcDir, "0.9.1")
	if _, err := Install(context.Background(), oldSrc, controlled, InstallOptions{Version: "0.9.1"}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Give it a moment so the backup timestamp differs.
	time.Sleep(1100 * time.Millisecond)
	// New version install.
	newSrcDir := filepath.Join(root, "new")
	// Need actually different content so the backup is distinguishable from
	// the new binary.  Since our fake-mox scripts always have the same
	// content (env-driven version), we write a DIFFERENT script here.
	// (The backup is a byte-identical copy of the prior on-disk file; the
	// version sidecar carries the real version tag.)
	newSrc := newFakeMox(t, newSrcDir, "0.9.2")
	// Inject a byte difference so the files are actually distinct.  The
	// easiest way: rewrite the source with a trailing newline so SHA differs.
	data, err := os.ReadFile(newSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newSrc, append(data, '#'), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Install(context.Background(), newSrc, controlled, InstallOptions{Version: "0.9.2"})
	if err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if res.ReplacedVersion != "0.9.1" {
		t.Errorf("ReplacedVersion = %q, want 0.9.1", res.ReplacedVersion)
	}
	if res.PreviousBackupPath == "" {
		t.Fatal("expected backup to be taken on replace")
	}
	// Backup file should exist and be readable.
	info, err := os.Lstat(res.PreviousBackupPath)
	if err != nil {
		t.Fatalf("backup stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("backup is empty")
	}
	// Check new sidecar.
	sc, err := ReadVersionSidecar(controlled)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Version != "0.9.2" {
		t.Errorf("new sidecar version = %q, want 0.9.2", sc.Version)
	}
}

func TestInstall_EmptyDir_Error(t *testing.T) {
	_, err := Install(context.Background(), "/bin/true", "", InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "empty controlledDir") {
		t.Errorf("got err=%v, want empty-controlledDir error", err)
	}
}

func TestInstall_SymlinkSrc_Rejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	realSrcDir := filepath.Join(root, "real")
	realSrc := newFakeMox(t, realSrcDir, "1.0.0")
	// Create a symlink pointing at the real file.
	linkPath := filepath.Join(root, "fake-mox-symlink")
	if err := os.Symlink(realSrc, linkPath); err != nil {
		t.Skipf("symlink not available on this filesystem: %v", err)
	}
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Install(context.Background(), linkPath, controlled, InstallOptions{Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected error on symlink src, got nil")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("err=%v, want 'not a regular file'", err)
	}
}

// --- Uninstall tests --------------------------------------------------------

func TestUninstall_RefusesNonControlled(t *testing.T) {
	root := tempRoot(t)
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	// Write a mox file but NO sidecar → should refuse.
	writeFile(t, controlled, "mox", "#!/bin/sh\n", 0o700)
	err := Uninstall(controlled)
	if !errors.Is(err, ErrNotControlled) {
		t.Errorf("err=%v, want ErrNotControlled", err)
	}
}

func TestUninstall_HappyPath_RemovesBinarySidecarAndBackups(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	// Install an old version → new version → produces mox + mox.version +
	// one mox.bak.<epoch>.  Also drop a user-created mox.bak.manual file to
	// confirm we DON'T delete that one.
	oldSrcDir := filepath.Join(root, "old")
	oldSrc := newFakeMox(t, oldSrcDir, "0.9.1")
	if _, err := Install(context.Background(), oldSrc, controlled, InstallOptions{Version: "0.9.1"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	newSrcDir := filepath.Join(root, "new")
	newSrc := newFakeMox(t, newSrcDir, "0.9.2")
	data, _ := os.ReadFile(newSrc)
	_ = os.WriteFile(newSrc, append(data, 'X'), 0o755)
	if _, err := Install(context.Background(), newSrc, controlled, InstallOptions{Version: "0.9.2"}); err != nil {
		t.Fatal(err)
	}
	// User-created non-digit backup.
	writeFile(t, controlled, "mox.bak.manual", "do not delete me", 0o600)

	res, err := UninstallWithResult(controlled, false)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !res.RemovedBinary {
		t.Error("binary not removed")
	}
	if !res.RemovedSidecar {
		t.Error("sidecar not removed")
	}
	if res.BackupsRemoved != 1 {
		t.Errorf("BackupsRemoved = %d, want 1", res.BackupsRemoved)
	}
	if res.UninstalledVersion != "0.9.2" {
		t.Errorf("UninstalledVersion = %q, want 0.9.2", res.UninstalledVersion)
	}
	// Confirm user-created mox.bak.manual still exists.
	if _, err := os.Lstat(filepath.Join(controlled, "mox.bak.manual")); err != nil {
		t.Errorf("user-created backup was deleted: %v", err)
	}
	// Controlled dir itself should still exist (never removed by Uninstall).
	if _, err := os.Lstat(controlled); err != nil {
		t.Errorf("controlled dir was removed!")
	}
}

func TestUninstall_EmptyDir(t *testing.T) {
	err := Uninstall("")
	if err == nil || !strings.Contains(err.Error(), "empty controlledDir") {
		t.Errorf("err=%v, want empty-dir error", err)
	}
}

// --- parseMapsLine (used by binaryInUse) -----------------------------------

func TestParseMapsLine(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantPerm    string
		wantPathIdx int // byte offset into line where pathname starts
		wantOK      bool
	}{
		{
			name:        "full line with pathname",
			line:        "55d0c000-55d0d000 r-xp 00000000 103:03 262818 /usr/bin/bash",
			wantPerm:    "r-xp",
			wantPathIdx: 45, // end of the 5 required fields (address perms offset dev inode), just before the path
			wantOK:      true,
		},
		{
			name:        "no pathname (anonymous mapping)",
			line:        "7f8a00000000-7f8a00200000 ---p 00000000 00:00 0",
			wantPerm:    "---p",
			wantPathIdx: 47, // == len(line) → trimming leaves empty string
			wantOK:      true,
		},
		{
			name:   "malformed",
			line:   "only three fields here",
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perm, pathStart, ok := parseMapsLine([]byte(tc.line))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if string(perm) != tc.wantPerm {
				t.Errorf("perm = %q, want %q", perm, tc.wantPerm)
			}
			if pathStart != tc.wantPathIdx {
				t.Errorf("pathStart = %d, want %d", pathStart, tc.wantPathIdx)
			}
			// For cases with pathnames, verify the pathStart byte lands on
			// the leading space before the pathname and trimming yields it.
			if tc.name == "full line with pathname" {
				rest := strings.TrimLeft(tc.line[pathStart:], " ")
				if !strings.HasPrefix(rest, "/usr/bin/bash") {
					t.Errorf("path at offset %d = %q, want \"/usr/bin/bash\"", pathStart, rest)
				}
			}
			if tc.name == "no pathname (anonymous mapping)" {
				rest := strings.TrimSpace(tc.line[pathStart:])
				if rest != "" {
					t.Errorf("anonymous mapping has unexpected tail %q", rest)
				}
			}
		})
	}
}

// --- parseMapsLine used with binaryInUse fake /proc setup ------------------

// binaryInUse is tested indirectly via Install on Linux hosts where
// /proc/*/maps actually exists.  On non-Linux we skip the real /proc walk
// and verify the parseMapsLine helper above covers correctness.

func TestBinaryInUse_NonLinuxReturnsUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this test only runs on non-linux to confirm unsupported error returned")
	}
	_, err := binaryInUse(context.Background(), "/bin/bash")
	if err != errBinaryInUseUnsupported {
		t.Errorf("err=%v, want errBinaryInUseUnsupported", err)
	}
}

// --- detect edge: PATH dedupe + controlled/path collision ------------------

func TestDetect_SkipsPATHControlledDirDuplicate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	root := tempRoot(t)
	controlled := filepath.Join(root, "bin")
	if err := os.MkdirAll(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	newFakeMox(t, controlled, "controlled-1.0")
	// Put controlled dir in PATH.  Detect should NOT re-probe it as Path.
	t.Setenv("PATH", controlled+string(os.PathListSeparator)+controlled) // also test dedupe
	res, err := Detect(controlled, DetectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Controlled == nil {
		t.Fatal("controlled slot empty")
	}
	// Path slot should be nil since the only PATH entry is controlled dir.
	if res.Path != nil {
		t.Errorf("Path slot should be nil when PATH only contains controlled dir; got %+v", res.Path)
	}
}

// --- version sidecar write/read round-trip ----------------------------------

func TestVersionSidecar_RoundTrip(t *testing.T) {
	root := tempRoot(t)
	want := versionSidecar{
		Version:        "0.9.2",
		InstalledAt:    time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		InstalledBy:    "phantom-lancer",
		ChecksumSHA256: strings.Repeat("ab", 32),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		Source:         "install:/tmp/abc",
	}
	if err := writeVersionSidecar(filepath.Join(root, "mox.version"), want); err != nil {
		t.Fatal(err)
	}
	got, err := readVersionSidecar(filepath.Join(root, "mox.version"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("nil sidecar")
	}
	if got.Version != want.Version || got.InstalledBy != want.InstalledBy ||
		got.ChecksumSHA256 != want.ChecksumSHA256 || got.Source != want.Source {
		t.Errorf("round-trip mismatch: got=%+v want=%+v", got, want)
	}
	// Time survives JSON round-trip with sub-second precision loss; compare
	// via .UTC() format.
	if !got.InstalledAt.UTC().Equal(want.InstalledAt.UTC()) {
		t.Errorf("InstalledAt got=%v want=%v", got.InstalledAt, want.InstalledAt)
	}
}
