// Package moxbinary handles discovery, pinned download, atomic installation
// and controlled uninstall of the Mox mail-server binary.
//
// This package exists separately from moxsupervisor (which owns runtime
// lifecycle) because the install/uninstall flow has fundamentally different
// concurrency and filesystem semantics:
//
//   - Detect() is safe to call at any time, even while a Mox process is live.
//   - Install() performs a temp-write-then-rename so that a running Mox is
//     never reading the binary while we overwrite it (POSIX unlink semantics
//     keep the old inode alive until the process exits; the next Start() will
//     pick up the freshly installed version).
//   - Uninstall() ONLY touches binaries under our controlled install dir;
//     PATH-discovered binaries are never deleted (C4 corollary).
//
// Hard constraints enforced here:
//   - No shell exec ever – every external call uses explicit argv slices.
//   - Downloads are URL-pinned to the github.com/mjl-/mox release page; the
//     "latest" tag is never accepted.
//   - Every downloaded binary is verified against a known-good SHA256
//     whitelist before the install rename happens.
package moxbinary

import (
	"errors"
	"fmt"
	"runtime"
	"time"
)

// --- Public error sentinels -------------------------------------------------
//
// Callers match on these via errors.Is to surface the right UI state.

var (
	// ErrNoBinary is returned by Detect() when neither the controlled install
	// dir nor PATH contain a `mox` executable.
	ErrNoBinary = errors.New("moxbinary: no mox binary found")

	// ErrUnknownVersion is returned by Download when the requested version is
	// not in the known-good checksums whitelist.
	ErrUnknownVersion = errors.New("moxbinary: requested version is not in the known-good whitelist")

	// ErrChecksumMismatch is returned when a downloaded file's SHA256 does
	// not match the pinned whitelist value.  The temp file is removed before
	// the error is returned so callers never accidentally install a bad
	// binary.
	ErrChecksumMismatch = errors.New("moxbinary: SHA256 of downloaded file does not match whitelist")

	// ErrURLNotAllowed means the caller tried to download from a URL that is
	// outside the release-asset prefix we whitelist.
	ErrURLNotAllowed = errors.New("moxbinary: download URL is outside the github.com/mjl-/mox release-asset prefix")

	// ErrNotControlled is returned by Uninstall when the candidate path is
	// not inside Phantom's controlled install directory.  We will never
	// unlink a PATH-discovered or OS-package binary.
	ErrNotControlled = errors.New("moxbinary: refusing to uninstall – binary is not inside Phantom's controlled install directory")

	// ErrBinaryInUse means Install attempted to swap a running binary and the
	// in-use check (best-effort via /proc + lsof) refused it.  Callers should
	// prompt the operator to stop Mox first.
	ErrBinaryInUse = errors.New("moxbinary: binary is in use by a running mox process; stop Mox then retry")

	// ErrDownloadTooLarge is returned by Download when the asset exceeds the
	// configured size cap (either via Content-Length up-front or mid-stream
	// via the streaming LimitReader).
	ErrDownloadTooLarge = errors.New("moxbinary: download exceeds size cap")
)

// --- Public structs ---------------------------------------------------------

// BinaryInfo describes a single discovered Mox binary.  Detect() returns a
// slice of these ordered by preference.
type BinaryInfo struct {
	// Path is the absolute filesystem path.
	Path string `json:"path"`
	// Version is the output of `mox version` with surrounding whitespace
	// trimmed.  Empty if version execution failed.
	Version string `json:"version"`
	// ChecksumSHA256 is the hex-encoded SHA256 of the file on disk.
	ChecksumSHA256 string `json:"checksum_sha256"`
	// SizeBytes is the file size in bytes.
	SizeBytes int64 `json:"size_bytes"`
	// ModTime is the last modification timestamp of the file on disk.
	ModTime time.Time `json:"mod_time"`
	// Source reports where this binary was found:
	//   "controlled" – under <moxRoot>/bin (the install target).
	//   "path"       – discovered via exec.LookPath on the caller's $PATH.
	//   "hint"       – the caller passed an explicit HintPath to Detect().
	Source string `json:"source"`
	// InWhitelist is true when ChecksumSHA256 matches one of the known-good
	// entries for this host's OS/arch.  Operators can use this to decide
	// whether to trust the binary or reinstall from pinned release.
	InWhitelist bool `json:"in_whitelist"`
}

// DetectedResult is returned by Detect().  It keeps the preferred candidates
// separated so UI can draw "Installed / on PATH" pills.
type DetectedResult struct {
	// Controlled is the binary in the Phantom-controlled install dir, or
	// nil if none has been installed yet.
	Controlled *BinaryInfo `json:"controlled,omitempty"`
	// Path is the first `mox` found on PATH, or nil.
	Path *BinaryInfo `json:"path,omitempty"`
	// Hint is the binary found at the explicit HintPath passed to Detect,
	// or nil if no HintPath was passed.
	Hint *BinaryInfo `json:"hint,omitempty"`
	// Selected is the best binary out of Controlled → Hint → Path.  The
	// service layer uses this as the default for Supervisor.BinaryPath.
	Selected *BinaryInfo `json:"selected,omitempty"`
}

// --- OS / arch helpers ------------------------------------------------------

// GOOSAlias maps Go's runtime.GOOS to the filenames used by upstream Mox
// releases.  Mox releases follow Go conventions (darwin/linux/freebsd) so
// this is a passthrough today, but we keep the indirection so a future
// upstream rename is a single-line fix.
func GOOSAlias() string { return runtime.GOOS }

// GOARCHAlias mirrors GOOSAlias for the architecture component.
func GOARCHAlias() string { return runtime.GOARCH }

// ReleaseAssetFilename returns the filename used by upstream Mox release
// pages for the current host.  Example on linux/amd64: "mox-0.9.2-linux-amd64".
func ReleaseAssetFilename(version string) string {
	return fmt.Sprintf("mox-%s-%s-%s", version, GOOSAlias(), GOARCHAlias())
}
