package moxbinary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// InstallOptions controls the behaviour of Install().
type InstallOptions struct {
	// Version is written into the `mox.version` sidecar file.  If empty,
	// Install will execute src once with argv=[mox version] to infer it.
	// Supplying it explicitly (e.g. from a DownloadResult.Version) skips
	// that subprocess.
	Version string
	// ChecksumSHA256, if set, is verified against the source file's SHA256
	// before we do anything else.  Pass it when the caller already has a
	// checksum from Download() so we don't hash again.
	ChecksumSHA256 string
	// Force overwrites the controlled `mox` binary even when a best-effort
	// "is binary in use" check says it is.  Operators set this via a UI
	// toggle; the default (false) returns ErrBinaryInUse.
	Force bool
}

// InstallResult records the outcome of a successful Install.
type InstallResult struct {
	// InstalledPath is the final absolute path of the installed binary.
	InstalledPath string
	// VersionSidecarPath is the absolute path of the mox.version JSON file
	// written alongside the binary.
	VersionSidecarPath string
	// PreviousPath is the absolute path of the backup we took of the prior
	// binary (if any).  Empty if there was no prior install.  The backup
	// lives at `mox.bak.<epoch>` and is kept so the operator can roll back
	// manually if the new version is broken.
	PreviousBackupPath string
	// InstalledVersion is the version string written to the sidecar.
	InstalledVersion string
	// InstalledChecksumSHA256 is the SHA256 of the final on-disk bytes.
	InstalledChecksumSHA256 string
	// ReplacedVersion, if non-empty, is the version we replaced (from the
	// prior sidecar).  Empty on a fresh install.
	ReplacedVersion string
}

// versionSidecar is the on-disk JSON schema for `mox.version`.  Keeping it
// structured (rather than a one-line string) lets us add fields later
// without breaking existing installs.
type versionSidecar struct {
	Version        string    `json:"version"`
	InstalledAt    time.Time `json:"installed_at"`
	InstalledBy    string    `json:"installed_by"` // always "phantom-lancer" for writes from this pkg
	ChecksumSHA256 string    `json:"checksum_sha256"`
	GOOS           string    `json:"goos"`
	GOARCH         string    `json:"goarch"`
	// Source is a free-form field for audit: "downloaded", "hint-path", ...
	Source string `json:"source,omitempty"`
}

// sidecarFilename is the name of the JSON sidecar written alongside the
// controlled mox binary.  The presence of this file is how Uninstall()
// decides "did we install this?".
const sidecarFilename = "mox.version"

// Install atomically copies `src` (a downloaded or user-supplied binary)
// into `<controlledDir>/mox` using the temp-write-then-rename pattern, and
// writes a `mox.version` sidecar alongside it.
//
// Steps:
//  1. Verify src is a regular file (not a symlink, not a directory).
//  2. If opts.ChecksumSHA256 is set, re-hash src and assert it matches.
//  3. (best-effort) If `controlledDir/mox` already exists, check whether
//     any running process has it open via /proc/self/maps walk.  If yes
//     AND !opts.Force, return ErrBinaryInUse.
//  4. Take a backup of the current install as `mox.bak.<epoch>` if present.
//  5. Copy src → `controlledDir/.mox.tmp.<rand>` (0700), fsync.
//  6. Atomic rename `controlledDir/mox`.
//  7. Write the sidecar JSON (temp + chmod 0600 + rename).
//
// POSIX semantics guarantee that a running Mox keeps executing the old
// inode even after step 6 – the running process is unaffected.  The next
// Start() will pick up the new binary.
func Install(ctx context.Context, src, controlledDir string, opts InstallOptions) (*InstallResult, error) {
	if controlledDir == "" {
		return nil, fmt.Errorf("moxbinary: empty controlledDir")
	}
	if src == "" {
		return nil, fmt.Errorf("moxbinary: empty src path")
	}
	if err := os.MkdirAll(controlledDir, 0o700); err != nil {
		return nil, fmt.Errorf("moxbinary: mkdir controlledDir: %w", err)
	}

	// Step 1: src must be a regular file.
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return nil, fmt.Errorf("moxbinary: lstat src %s: %w", src, err)
	}
	if !srcInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("moxbinary: src %s is not a regular file (mode=%s)", src, srcInfo.Mode())
	}

	// Step 2: optional checksum verification.
	if opts.ChecksumSHA256 != "" {
		actual, err := hashFileSHA256(src)
		if err != nil {
			return nil, fmt.Errorf("moxbinary: re-hash src: %w", err)
		}
		if !strings.EqualFold(actual, opts.ChecksumSHA256) {
			return nil, fmt.Errorf("%w: Install(src) hash %s does not match caller-provided %s",
				ErrChecksumMismatch, actual, opts.ChecksumSHA256)
		}
	}

	dstPath := filepath.Join(controlledDir, "mox")
	sidecarPath := filepath.Join(controlledDir, sidecarFilename)

	// Step 3: best-effort "is it in use?" check.  We don't mind false
	// negatives (operator will restart Mox anyway after install); false
	// positives are the annoyance we're avoiding with Force=true.
	if !opts.Force {
		if inUse, err := binaryInUse(ctx, dstPath); err == nil && inUse {
			return nil, ErrBinaryInUse
		}
		// err from binaryInUse is swallowed – /proc isn't available on every
		// OS, and we don't want to block install over it.
	}

	result := &InstallResult{}

	// Step 4: backup prior install + prior sidecar, if present.
	if info, err := os.Stat(dstPath); err == nil && info.Mode().IsRegular() {
		epoch := time.Now().Unix()
		backup := filepath.Join(controlledDir, "mox.bak."+strconv.FormatInt(epoch, 10))
		if err := copyRegularFile(dstPath, backup, 0o700); err != nil {
			return nil, fmt.Errorf("moxbinary: backup prior binary: %w", err)
		}
		result.PreviousBackupPath = backup
		// Also read prior sidecar for the result.
		if prev, err := readVersionSidecar(sidecarPath); err == nil && prev != nil {
			result.ReplacedVersion = prev.Version
		}
	}

	// Step 5: copy to a tempfile, fsync, chmod.
	tmp, err := os.CreateTemp(controlledDir, ".mox.install-*")
	if err != nil {
		return nil, fmt.Errorf("moxbinary: create install tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTmp := func() { _ = os.Remove(tmpName) }
	srcF, err := os.Open(src)
	if err != nil {
		tmp.Close()
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: open src: %w", err)
	}
	if _, err := copyBuffer(tmp, srcF, 1<<20); err != nil {
		srcF.Close()
		tmp.Close()
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: copy src → tmp: %w", err)
	}
	srcF.Close()
	// fsync tempfile before rename so a power loss can't leave us with a
	// zero-length `mox` on the next boot.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o700); err != nil {
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: chmod tmp: %w", err)
	}

	// Compute the final hash once – this is the authoritative value we
	// write into the sidecar (and return to callers for audit).
	finalHash, err := hashFileSHA256(tmpName)
	if err != nil {
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: hash tmp: %w", err)
	}

	// Step 6: atomic rename.
	if err := os.Rename(tmpName, dstPath); err != nil {
		cleanupTmp()
		return nil, fmt.Errorf("moxbinary: rename tmp → mox: %w", err)
	}

	// Step 7: write version sidecar (JSON, 0600, atomic).
	version := opts.Version
	if version == "" {
		// Run it – a short subprocess is fine here since we're already on
		// the install path.  Best-effort; if it fails we still leave the
		// binary in place but mark version as "unknown".
		v, err := runMoxVersion(dstPath, 10*time.Second)
		if err != nil {
			version = "unknown"
		} else {
			version = v
		}
	}
	sc := versionSidecar{
		Version:        version,
		InstalledAt:    time.Now().UTC(),
		InstalledBy:    "phantom-lancer",
		ChecksumSHA256: finalHash,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		Source:         "install:" + src,
	}
	if err := writeVersionSidecar(sidecarPath, sc); err != nil {
		// Sidecar failure is NOT fatal for the binary itself – the
		// operator can still run it.  We surface the error so the UI can
		// warn but keep InstallResult populated.
		return result, fmt.Errorf("moxbinary: write version sidecar (binary was installed): %w", err)
	}

	result.InstalledPath = dstPath
	result.VersionSidecarPath = sidecarPath
	result.InstalledVersion = sc.Version
	result.InstalledChecksumSHA256 = sc.ChecksumSHA256
	return result, nil
}

// --- sidecar I/O ------------------------------------------------------------

func writeVersionSidecar(path string, sc versionSidecar) error {
	dir := filepath.Dir(path)
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	// atomic write: tmp → chmod 0600 → rename.
	tmp, err := os.CreateTemp(dir, ".mox.version-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, path)
}

func readVersionSidecar(path string) (*versionSidecar, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sc := &versionSidecar{}
	if err := json.Unmarshal(data, sc); err != nil {
		return nil, fmt.Errorf("moxbinary: parse %s: %w", path, err)
	}
	return sc, nil
}

// ReadVersionSidecar exposes the sidecar read operation to callers that
// want to display "installed by Phantom / version X" in the UI.
// It returns (nil, nil) if the sidecar does not exist (i.e. the binary in
// controlledDir was placed there by someone else).
func ReadVersionSidecar(controlledDir string) (*versionSidecar, error) {
	if controlledDir == "" {
		return nil, nil
	}
	return readVersionSidecar(filepath.Join(controlledDir, sidecarFilename))
}

// --- helpers ---------------------------------------------------------------

// copyRegularFile copies src to dst.  src must be a regular file (not a
// symlink; callers are expected to Lstat first).  Permissions on dst are
// `perm`.
func copyRegularFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := copyBuffer(out, in, 1<<20); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyBuffer is io.Copy reimplemented locally with an explicit buffer size
// and precise short-write detection.
func copyBuffer(dst *os.File, src *os.File, bufSize int) (int64, error) {
	buf := make([]byte, bufSize)
	var total int64
	for {
		nr, rerr := src.Read(buf)
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			total += int64(nw)
			if werr != nil {
				return total, werr
			}
			if nw != nr {
				return total, fmt.Errorf("short write: %d vs %d", nw, nr)
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// binaryInUse performs a best-effort check: does any process have dstPath
// open as an executable mapping?
//
// Implementation is OS-dependent:
//   - Linux: walk /proc/*/maps and look for a `r-xp` line whose pathname
//     matches dstPath exactly.
//   - macOS / other: return (false, errBinaryInUseUnsupported) which
//     callers convert to a no-op (not an error condition).
//
// Never returns an error for "false, not in use" – errors mean "we don't
// know" and callers treat that as "proceed".
func binaryInUse(ctx context.Context, dstPath string) (bool, error) {
	if runtime.GOOS != "linux" {
		return false, errBinaryInUseUnsupported
	}
	absPath, err := filepath.Abs(dstPath)
	if err != nil {
		return false, err
	}
	procDir := "/proc"
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return false, err
	}
	needle := []byte(absPath)
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}
		name := e.Name()
		if !isASCIIDigits(name) {
			continue
		}
		mapsPath := procDir + "/" + name + "/maps"
		data, rerr := os.ReadFile(mapsPath)
		if rerr != nil {
			continue
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			perm, pathStart, ok := parseMapsLine(line)
			if !ok {
				continue
			}
			// Text mappings are always r-x (read + exec).
			if !bytes.Contains(perm, []byte("x")) {
				continue
			}
			pathname := bytes.TrimSpace(line[pathStart:])
			if bytes.Equal(pathname, needle) {
				return true, nil
			}
		}
	}
	return false, nil
}

// parseMapsLine parses a single /proc/<pid>/maps line.  Returns (perm,
// pathStartByteOffset, ok).  pathStart is the index into `line` where the
// pathname begins (after the 5 fixed whitespace-separated fields: address,
// perms, offset, dev, inode).  For anonymous mappings there is no 6th field
// — pathStart will equal len(line) and trimming yields ""; callers handle
// this via bytes.TrimSpace on the tail.
//
// ok is false when the line has fewer than 5 fields (definitely malformed).
func parseMapsLine(line []byte) (perm []byte, pathStart int, ok bool) {
	// Fields (whitespace separated, min 5 required):
	//   address  perms  offset  dev  inode  [pathname]
	const minFields = 5
	n := 0
	i := 0
	for n < minFields && i < len(line) {
		// Skip leading whitespace.
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		// Advance over non-whitespace.
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		n++
		if n == 2 {
			perm = line[start:i]
		}
	}
	if n < minFields {
		return nil, 0, false
	}
	// Anything past field 5 (including leading whitespace that follows the
	// inode) is the optional pathname area.  Callers TrimSpace.
	return perm, i, true
}

// errBinaryInUseUnsupported is never surfaced across the package boundary;
// Install() swallows it and proceeds.
var errBinaryInUseUnsupported = errors.New("binary-in-use check not implemented on this OS")

// isASCIIDigits reports whether s is a non-empty string of ASCII digits.
func isASCIIDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
