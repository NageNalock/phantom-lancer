package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

type githubRelease struct {
	ID          int64         `json:"id"`
	TagName     string        `json:"tag_name"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func (s *Service) fetchLatestRelease(ctx context.Context, etag string) (storage.SystemUpdateCheck, bool, error) {
	apiBase := strings.TrimRight(s.cfg.APIBaseURL, "/")
	if apiBase == "" {
		apiBase = defaultAPIBaseURL
	}
	requestURL := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, strings.Trim(s.cfg.Repository, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return storage.SystemUpdateCheck{}, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "phantom-lancer-self-update")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	started := time.Now()
	if s.log != nil {
		s.log.Debug("github release check started", "repository", strings.Trim(s.cfg.Repository, "/"), "api_host", safelog.HostLabel(requestURL), "has_etag", etag != "")
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		if s.log != nil {
			s.log.Warn("github release check failed", "repository", strings.Trim(s.cfg.Repository, "/"), "api_host", safelog.HostLabel(requestURL), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		return storage.SystemUpdateCheck{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		if s.log != nil {
			s.log.Debug("github release check completed", "repository", strings.Trim(s.cfg.Repository, "/"), "status", resp.StatusCode, "not_modified", true, "latency_ms", time.Since(started).Milliseconds())
		}
		return storage.SystemUpdateCheck{}, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		if s.log != nil {
			s.log.Warn("github release check returned failure", "repository", strings.Trim(s.cfg.Repository, "/"), "status", resp.StatusCode, "latency_ms", time.Since(started).Milliseconds())
		}
		return storage.SystemUpdateCheck{}, false, fmt.Errorf("github release check failed: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseResponseBytes+1))
	if err != nil {
		return storage.SystemUpdateCheck{}, false, err
	}
	if len(data) > maxReleaseResponseBytes {
		return storage.SystemUpdateCheck{}, false, errors.New("github release response too large")
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return storage.SystemUpdateCheck{}, false, err
	}
	check, err := s.checkFromRelease(release)
	if err != nil {
		if s.log != nil {
			s.log.Warn("github release check parse failed", "repository", strings.Trim(s.cfg.Repository, "/"), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		return storage.SystemUpdateCheck{}, false, err
	}
	check.ETag = resp.Header.Get("ETag")
	if s.log != nil {
		s.log.Debug("github release check completed", "repository", strings.Trim(s.cfg.Repository, "/"), "status", resp.StatusCode, "latest_version", check.LatestVersion, "can_apply", check.CanApply, "latency_ms", time.Since(started).Milliseconds())
	}
	return check, false, nil
}

func (s *Service) checkFromRelease(release githubRelease) (storage.SystemUpdateCheck, error) {
	currentVersion := s.cfg.Build.Version
	check := storage.SystemUpdateCheck{
		CurrentVersion: currentVersion,
		LatestVersion:  strings.TrimSpace(release.TagName),
		ReleaseID:      fmt.Sprintf("%d", release.ID),
		ReleaseURL:     strings.TrimSpace(release.HTMLURL),
		PublishedAt:    strings.TrimSpace(release.PublishedAt),
	}
	if release.Draft || release.Prerelease {
		check.Reason = "latest release is draft or prerelease"
		return check, nil
	}
	if !isReleaseTag(check.LatestVersion) {
		check.Reason = "latest release tag is not v-prefixed semver"
		return check, nil
	}

	asset, checksum := matchAssets(release.Assets, s.cfg.AssetName)
	check.PlatformSupported = runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
	if asset.Name != "" {
		check.AssetName = asset.Name
		check.AssetURL = asset.BrowserDownloadURL
		check.AssetSizeBytes = asset.Size
	}
	if checksum.Name != "" {
		check.ChecksumAssetURL = checksum.BrowserDownloadURL
		check.ChecksumAvailable = true
	}
	comparison, comparable := compareVersions(currentVersion, check.LatestVersion)
	check.Comparable = comparable
	check.UpdateAvailable = !comparable || comparison < 0
	if comparable && comparison >= 0 {
		check.Reason = "current version is up to date"
		return check, nil
	}
	if !check.PlatformSupported {
		check.Reason = "current platform is not supported by the release asset"
		return check, nil
	}
	if check.AssetURL == "" {
		check.Reason = "matching release asset is missing"
		return check, nil
	}
	if check.ChecksumAssetURL == "" {
		check.Reason = "checksum asset is missing"
		return check, nil
	}
	if err := validateDownloadURL(check.AssetURL, s.cfg.AllowInsecureDownloads); err != nil {
		check.Reason = "release asset URL is not allowed"
		return check, nil
	}
	if err := validateDownloadURL(check.ChecksumAssetURL, s.cfg.AllowInsecureDownloads); err != nil {
		check.Reason = "checksum asset URL is not allowed"
		return check, nil
	}
	check.CanApply = true
	return check, nil
}

func matchAssets(assets []githubAsset, assetName string) (githubAsset, githubAsset) {
	var archive githubAsset
	var checksum githubAsset
	checksumName := assetName + ".sha256"
	for _, asset := range assets {
		if asset.Name == assetName {
			archive = asset
		}
		if asset.Name == checksumName {
			checksum = asset
		}
	}
	return archive, checksum
}

func isReleaseTag(value string) bool {
	if !strings.HasPrefix(value, "v") {
		return false
	}
	_, ok := parseVersion(value)
	return ok
}

func validateDownloadURL(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if allowInsecure && parsed.Scheme == "http" {
		return nil
	}
	return errors.New("download URL must use HTTPS")
}
