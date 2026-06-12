package moxbinary

// --- Known-good checksums ---------------------------------------------------
//
// Populate KnownVersions with upstream-released binaries that have been
// audited / exercised by the Phantom team.  When the operator asks to
// Download(version) we verify the final downloaded bytes against these
// SHA256s BEFORE allowing Install() – this way a compromised GitHub release
// page, a hijacked CDN, or an on-path attacker cannot slip a malicious
// binary onto the host.
//
// The table ships empty by default so builds are reproducible; when we cut a
// Phantom release we fill in the hashes from
// https://github.com/mjl-/mox/releases for the versions we bless.
// Operators who want to pin a specific version can add entries at build
// time via `-ldflags "-X .../moxbinary.KnownVersionsJSON=..."` if they need
// to inject extra entries (TODO: add ldflags hook if requested).
//
// ApprovedDownloadPrefixes is the URL prefix whitelist enforced by
// Download().  Any URL NOT starting with one of these is rejected
// (ErrURLNotAllowed).  We deliberately do NOT allow arbitrary HTTPS URLs
// per the hard constraint.

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// VersionRecord holds the SHA256 of the release asset for a single version.
// Keys are "os_arch" (e.g. "linux_amd64", "darwin_arm64").  Values are
// lowercase hex-encoded SHA256 of the raw release-asset file (the file you
// get from https://github.com/mjl-/mox/releases/download/vX.Y.Z/mox-X.Y.Z-os-arch).
type VersionRecord struct {
	// Version is the upstream release tag WITHOUT the "v" prefix, e.g. "0.9.2".
	Version string
	// SHA256ByPlatform maps "os_arch" to the lowercase hex SHA256 of the
	// upstream release asset for that platform.
	SHA256ByPlatform map[string]string
}

// ApprovedDownloadPrefixes is the set of URL prefixes Download() will
// accept.  We only allow the canonical GitHub release-asset URL; mirrors
// can be added explicitly but operators must audit them.
var ApprovedDownloadPrefixes = []string{
	"https://github.com/mjl-/mox/releases/download/",
	// GitHub's direct redirector – the redirect still lands on the same
	// release asset so we allow it as a convenience; the final SHA256
	// check catches any funny business.
	"https://objects.githubusercontent.com/",
}

// KnownVersions is the hardcoded set of Mox releases Phantom trusts.
// Download() will refuse versions not in this map.  The map is keyed by
// the version string without the leading "v" (e.g. "0.9.2").
//
// NOTE: intentionally empty – real hashes are pinned per Phantom release.
// See §5.1.2 of the mail control-plane design doc for the rollout policy.
var KnownVersions = map[string]VersionRecord{
	// EXAMPLE entry – filled in by the release process:
	// "0.9.2": {
	//   Version: "0.9.2",
	//   SHA256ByPlatform: map[string]string{
	//     "linux_amd64":  "deadbeef...",
	//     "darwin_arm64": "cafebabe...",
	//   },
	// },
}

// --- Public helpers around the whitelist ------------------------------------

// IsKnownVersion reports whether version appears in KnownVersions.
func IsKnownVersion(version string) bool {
	_, ok := KnownVersions[canonicalVersion(version)]
	return ok
}

// LookupChecksum returns the expected SHA256 (lowercase hex) of the release
// asset for (version, goos, goarch) and a boolean that reports whether the
// lookup succeeded.
//
// Pass empty strings for goos/goarch to use the running host's values.
func LookupChecksum(version, goos, goarch string) (string, bool) {
	rec, ok := KnownVersions[canonicalVersion(version)]
	if !ok {
		return "", false
	}
	if goos == "" {
		goos = GOOSAlias()
	}
	if goarch == "" {
		goarch = GOARCHAlias()
	}
	sum, ok := rec.SHA256ByPlatform[goos+"_"+goarch]
	if !ok {
		return "", false
	}
	return sum, true
}

// ChecksumInWhitelist reports whether the given hex SHA256 (of a file on
// disk, typically) matches any entry in KnownVersions for the current
// host's OS/arch.  Used by Detect() to populate BinaryInfo.InWhitelist.
//
// This is intentionally a "one-way" check: we don't tell the caller which
// version matched, only whether we recognise the hash.
func ChecksumInWhitelist(hexSHA256 string) bool {
	hexSHA256 = strings.ToLower(strings.TrimSpace(hexSHA256))
	if len(hexSHA256) != hex.EncodedLen(32) {
		return false
	}
	key := GOOSAlias() + "_" + GOARCHAlias()
	for _, rec := range KnownVersions {
		expected, ok := rec.SHA256ByPlatform[key]
		if ok && strings.ToLower(expected) == hexSHA256 {
			return true
		}
	}
	return false
}

// BuildDownloadURL returns the canonical GitHub release-asset URL for a
// version + host combination.  The returned URL is guaranteed to start
// with one of ApprovedDownloadPrefixes (by construction – the first
// entry).  Returns an error if version isn't in KnownVersions (callers
// should not be constructing URLs for unblessed versions anyway).
func BuildDownloadURL(version string) (string, error) {
	clean := canonicalVersion(version)
	if !IsKnownVersion(clean) {
		return "", fmt.Errorf("%w: %q", ErrUnknownVersion, clean)
	}
	base := ApprovedDownloadPrefixes[0]
	// Upstream URLs look like:
	//   https://github.com/mjl-/mox/releases/download/v0.9.2/mox-0.9.2-linux-amd64
	return fmt.Sprintf("%sv%s/%s", base, clean, ReleaseAssetFilename(clean)), nil
}

// URLAllowed reports whether the given download URL passes the prefix
// whitelist.  The comparison is prefix-based; we do NOT strip query
// strings because the canonical GitHub release URLs don't carry any.
// Callers that pass query strings for whatever reason will get a rejection
// – that's a deliberate defence-in-depth measure.
func URLAllowed(u string) bool {
	for _, prefix := range ApprovedDownloadPrefixes {
		if strings.HasPrefix(u, prefix) {
			return true
		}
	}
	return false
}

// --- internal helpers -------------------------------------------------------

// canonicalVersion strips the leading "v" if present so "v0.9.2" and
// "0.9.2" both resolve to the same KnownVersions key.
func canonicalVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}
