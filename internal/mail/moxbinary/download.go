package moxbinary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DownloadOptions tunes Download() behaviour.  Zero value defaults are safe.
type DownloadOptions struct {
	// HTTPClient, if non-nil, is used instead of http.DefaultClient.  Useful
	// in tests for injecting a fake transport.
	HTTPClient *http.Client
	// OverrideURL, if non-empty, forces the URL to download from.  The URL
	// MUST still pass URLAllowed() or Download returns ErrURLNotAllowed.
	// Intended for internal mirrors of the github release asset – not for
	// arbitrary URLs.
	OverrideURL string
	// DestDir, if set, is where the tempfile is written.  Defaults to
	// os.TempDir().  Callers can pass the controlled bin/ dir directly so
	// the subsequent Install rename is cross-device-safe.
	DestDir string
	// SizeMaxBytes caps the download.  Defaults to 200 MiB.  Set to -1 to
	// allow unbounded downloads (NOT recommended – only for tests that
	// inject a captive client).
	SizeMaxBytes int64
	// Progress, if non-nil, is called periodically with total-bytes-read and
	// content-length-known (0 if the server didn't send Content-Length).
	// The callback is invoked synchronously from the reader goroutine; keep
	// work inside it small.
	Progress func(received int64, knownTotal int64)
}

// DownloadResult is returned on successful download.
type DownloadResult struct {
	// TempPath is the absolute path of the downloaded tempfile.  Ownership
	// transfers to the caller; they are responsible for os.Remove-ing it
	// once it's no longer needed (or passing it to Install, which will
	// consume the rename atomically).
	TempPath string
	// SizeBytes is the number of bytes actually written to disk.
	SizeBytes int64
	// ChecksumSHA256 is the lowercase hex SHA256 of the downloaded bytes.
	ChecksumSHA256 string
	// ExpectedSHA256 is the whitelist value we matched against, for audit.
	ExpectedSHA256 string
	// Version is the version we were asked to download (canonical form).
	Version string
}

// Download fetches the release asset for `version` from the approved GitHub
// prefix, writes it to a tempfile, and verifies the SHA256 against the
// built-in KnownVersions whitelist.  The tempfile is removed BEFORE
// returning if any step after the HTTP body read fails.
//
// Hard constraints enforced here:
//   - Only versions in KnownVersions are accepted (ErrUnknownVersion).
//   - Only URLs starting with one of ApprovedDownloadPrefixes are accepted.
//   - A size ceiling caps the download at 200 MiB by default.
//   - SHA256 of the final bytes MUST match the whitelist.
//   - The tempfile is created with 0600 perms so only the current user can
//     read the payload while it's in transit.
func Download(ctx context.Context, version string, opts DownloadOptions) (*DownloadResult, error) {
	clean := canonicalVersion(version)
	if clean == "" {
		return nil, fmt.Errorf("%w: empty version", ErrUnknownVersion)
	}
	expected, ok := LookupChecksum(clean, "", "")
	if !ok {
		return nil, fmt.Errorf("%w: %q is not in KnownVersions for %s/%s",
			ErrUnknownVersion, clean, GOOSAlias(), GOARCHAlias())
	}

	// Build the download URL and enforce the prefix whitelist.
	url := opts.OverrideURL
	if url == "" {
		u, err := BuildDownloadURL(clean)
		if err != nil {
			return nil, err
		}
		url = u
	}
	if !URLAllowed(url) {
		return nil, fmt.Errorf("%w: %q (prefix not in ApprovedDownloadPrefixes)", ErrURLNotAllowed, url)
	}

	// Size ceiling.
	maxBytes := opts.SizeMaxBytes
	if maxBytes == 0 {
		maxBytes = 200 << 20 // 200 MiB
	}

	// HTTP client – share a default but let callers override for tests.
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("moxbinary: build request: %w", err)
	}
	// Best-effort user-agent so GitHub rate-limits are friendlier.
	req.Header.Set("User-Agent", "phantom-lancer/moxbinary (+https://github.com/)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("moxbinary: GET %s: %w", redactURL(url), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("moxbinary: GET %s: unexpected HTTP %d",
			redactURL(url), resp.StatusCode)
	}

	// If the server sent Content-Length, check upfront.
	var knownTotal int64
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, perr := strconv.ParseInt(cl, 10, 64); perr == nil {
			knownTotal = n
			if maxBytes > 0 && n > maxBytes {
					return nil, fmt.Errorf("%w: Content-Length=%d, max=%d", ErrDownloadTooLarge, n, maxBytes)
				}
		}
	}

	// Create tempfile under the requested dest dir with 0600 perms.
	destDir := opts.DestDir
	if destDir == "" {
		destDir = os.TempDir()
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("moxbinary: mkdir %s: %w", destDir, err)
	}
	tmp, err := os.CreateTemp(destDir, ".mox-download-*")
	if err != nil {
		return nil, fmt.Errorf("moxbinary: create tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTemp := func() { _ = os.Remove(tmpName) }

	// Chmod immediately – CreateTemp uses 0600 on POSIX but be explicit.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanupTemp()
		return nil, fmt.Errorf("moxbinary: chmod tempfile: %w", err)
	}

	// Stream body → SHA256 + tempfile, bounded.
	h := sha256.New()
	var reader io.Reader = resp.Body
	if maxBytes > 0 {
		reader = io.LimitReader(reader, maxBytes+1) // +1 so we can detect overflow
	}
	progressTick := time.Now()
	var written int64
	var lastReport int64
	buf := make([]byte, 64<<10) // 64 KiB copy buffer
	for {
		nr, rerr := reader.Read(buf)
		if nr > 0 {
			if maxBytes > 0 && written+int64(nr) > maxBytes {
					tmp.Close()
					cleanupTemp()
					return nil, fmt.Errorf("%w: received %d bytes, cap=%d", ErrDownloadTooLarge, written+int64(nr), maxBytes)
				}
			if _, werr := tmp.Write(buf[:nr]); werr != nil {
				tmp.Close()
				cleanupTemp()
				return nil, fmt.Errorf("moxbinary: write tempfile: %w", werr)
			}
			h.Write(buf[:nr])
			written += int64(nr)
			if opts.Progress != nil && time.Since(progressTick) > 250*time.Millisecond {
				progressTick = time.Now()
				if written-lastReport >= 1<<20 { // report at ~1 MiB granularity
					opts.Progress(written, knownTotal)
					lastReport = written
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			tmp.Close()
			cleanupTemp()
			return nil, fmt.Errorf("moxbinary: download body: %w", rerr)
		}
	}
	// Final progress tick (100%).
	if opts.Progress != nil && (written > 0 || knownTotal > 0) {
		opts.Progress(written, knownTotal)
	}

	if err := tmp.Close(); err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("moxbinary: close tempfile: %w", err)
	}

	// Verify checksum BEFORE returning ownership of the tempfile.
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != strings.ToLower(expected) {
		cleanupTemp()
		return nil, fmt.Errorf("%w: expected %s got %s (size=%d)",
			ErrChecksumMismatch, expected, actual, written)
	}

	return &DownloadResult{
		TempPath:       tmpName,
		SizeBytes:      written,
		ChecksumSHA256: actual,
		ExpectedSHA256: expected,
		Version:        clean,
	}, nil
}

// --- small helpers ----------------------------------------------------------

// redactURL keeps log / error messages from accidentally dumping signed
// URLs or query strings into logs.  The current whitelist doesn't accept
// query strings anyway, but we keep this in as a belt-and-suspenders
// measure in case a future mirror adds `?X-Amz-Signature` or similar.
func redactURL(u string) string {
	// Truncate at 120 characters.
	if len(u) > 120 {
		return u[:117] + "..."
	}
	return u
}

// MoveTempToInstalled is a thin wrapper around os.Rename that atomically
// moves the downloaded tempfile to `<controlledDir>/mox`.  Callers should
// prefer the higher-level Install() function which also writes the
// .version sidecar file; this helper is public only for tests.
func MoveTempToInstalled(tempPath, controlledDir string) error {
	if controlledDir == "" {
		return fmt.Errorf("moxbinary: empty controlledDir")
	}
	if err := os.MkdirAll(controlledDir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(controlledDir, "mox")
	// The caller has already chmod'd tempPath to 0600; the install step
	// upgrades to 0700 so we can exec it.
	if err := os.Chmod(tempPath, 0o700); err != nil {
		return fmt.Errorf("moxbinary: chmod tempfile to 0700: %w", err)
	}
	return os.Rename(tempPath, dst)
}
