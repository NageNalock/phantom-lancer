package moxbinary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DetectOptions tunes Detect() behaviour.  The zero value is sensible: scan
// controlled dir + PATH, no explicit hint, 5s per `mox version` exec.
type DetectOptions struct {
	// HintPath, if set, is an explicit binary path the operator asked us to
	// prefer (e.g. from a form field).  Detect() will verify it exists, is
	// executable, and return it as the Hint candidate.  Relative paths are
	// resolved to absolute.
	HintPath string
	// ExtraPATH, if non-empty, is appended to the $PATH walk so callers can
	// inject homebrew / nix / local-install directories without having to
	// mutate their process environment.
	ExtraPATH []string
	// VersionTimeout limits how long each `mox version` subprocess is
	// allowed to run.  Defaults to 5s if zero.  (A pathological binary that
	// hangs on `version` shouldn't stall Detect.)
	VersionTimeout time.Duration
	// SkipPATH, when true, skips the $PATH walk entirely.  Useful for the
	// controlled-install flow where the operator only cares about binaries
	// Phantom itself installed.
	SkipPATH bool
}

// Detect discovers installed Mox binaries in priority order:
//
//  1. `<controlledDir>/mox` — the binary Phantom installs into.
//  2. `opts.HintPath` — if the operator pointed us at a specific binary.
//  3. Each dir in $PATH (+ opts.ExtraPATH), stopping at the first `mox` that
//     is a regular file.
//
// Every candidate is stat'd, checksum'd, and `mox version`'d (best-effort;
// failures of `version` do NOT abort Detect – the Version field will be
// empty and the UI can warn about it).
//
// controlledDir is usually `<moxRoot>/bin`; passing "" is allowed but means
// the Controlled slot will always be nil.
//
// Detect is the ONLY place we exec `mox version`.  No shell – direct argv.
func Detect(controlledDir string, opts DetectOptions) (*DetectedResult, error) {
	if opts.VersionTimeout <= 0 {
		opts.VersionTimeout = 5 * time.Second
	}

	res := &DetectedResult{}

	// 1. Controlled install.
	if controlledDir != "" {
		p := filepath.Join(controlledDir, "mox")
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			res.Controlled = probeBinary(p, "controlled", opts.VersionTimeout)
		}
	}

	// 2. Hint path.
	if opts.HintPath != "" {
		abs, err := filepath.Abs(opts.HintPath)
		if err == nil {
			if info, err := os.Lstat(abs); err == nil && info.Mode().IsRegular() {
				res.Hint = probeBinary(abs, "hint", opts.VersionTimeout)
			}
		}
	}

	// 3. PATH walk.
	if !opts.SkipPATH {
		dirs := pathDirs(os.Getenv("PATH"))
		dirs = append(dirs, opts.ExtraPATH...)
		seen := make(map[string]struct{}, len(dirs))
		for _, d := range dirs {
			if d == "" {
				continue
			}
			// Resolve once and dedupe so `.:.` doesn't probe the same dir twice.
			abs, err := filepath.Abs(d)
			if err != nil {
				abs = d
			}
			if _, dup := seen[abs]; dup {
				continue
			}
			seen[abs] = struct{}{}
			// Don't re-probe the controlled dir if it happens to be on PATH.
			if controlledDir != "" && pathsEqual(abs, controlledDir) {
				continue
			}
			p := filepath.Join(abs, "mox")
			if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
				res.Path = probeBinary(p, "path", opts.VersionTimeout)
				// Stop at the first PATH hit – exec.LookPath semantics.
				break
			}
		}
	}

	// Populate Selected in preference order.
	switch {
	case res.Controlled != nil:
		res.Selected = res.Controlled
	case res.Hint != nil:
		res.Selected = res.Hint
	case res.Path != nil:
		res.Selected = res.Path
	}

	if res.Selected == nil {
		// ErrNoBinary is informational; callers errors.Is() on it to decide
		// between "this is expected (fresh install)" and "something is wrong".
		return res, ErrNoBinary
	}
	return res, nil
}

// probeBinary collects metadata for a single candidate.  The returned
// *BinaryInfo is always non-nil for a readable file; Version may be empty
// if running `mox version` timed out or returned a non-zero exit.
func probeBinary(path, source string, versionTimeout time.Duration) *BinaryInfo {
	info, err := os.Stat(path)
	bi := &BinaryInfo{Path: path, Source: source}
	if err == nil {
		bi.SizeBytes = info.Size()
		bi.ModTime = info.ModTime()
	}
	if sum, err := hashFileSHA256(path); err == nil {
		bi.ChecksumSHA256 = sum
		bi.InWhitelist = ChecksumInWhitelist(sum)
	}
	// Best-effort version call.  A binary that crashes or hangs on `version`
	// is still installable; the UI will show "version unknown" and the
	// operator can decide whether to trust it.
	bi.Version, _ = runMoxVersion(path, versionTimeout)
	return bi
}

// runMoxVersion executes the candidate with argv=[mox version] and returns
// the trimmed stdout.  A context timeout bounds the runtime (defence in
// depth against a malicious binary fingerprinting its callers).
//
// Hard: no shell, explicit argv, bounded timeout.
func runMoxVersion(binPath string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "version")
	// Args[0] is conventionally set to the binary name.  exec.CommandContext
	// already does this via Path so argv on the child side looks exactly
	// like `mox version` – no surprises.
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		// If we got any stdout anyway, prefer returning that + nil error so
		// the UI has something to show.  Some Mox builds print version then
		// exit 1 when `-data` is missing / config is invalid (a known
		// upstream quirk).
		if out == "" {
			return "", fmt.Errorf("mox version: %w (stderr=%q)", err,
				strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	return out, nil
}

// hashFileSHA256 streams path through SHA256, bounded by a 1 GiB ceiling.
// Any candidate larger than 1 GiB is clearly corrupt / an imposter and
// we fail the hash rather than reading a multi-gigabyte file into RAM.
func hashFileSHA256(path string) (string, error) {
	const sizeCeil = 1 << 30 // 1 GiB
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, sizeCeil+1))
	if err != nil {
		return "", err
	}
	if n > sizeCeil {
		return "", fmt.Errorf("candidate %s is >1 GiB; refusing to hash (imposter binary?)", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// --- path helpers -----------------------------------------------------------

func pathDirs(pathEnv string) []string {
	if pathEnv == "" {
		return nil
	}
	return strings.Split(pathEnv, string(os.PathListSeparator))
}

func pathsEqual(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		aa = a
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		bb = b
	}
	return aa == bb
}
