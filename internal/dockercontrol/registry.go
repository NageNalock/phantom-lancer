package dockercontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/authlimiter"
	"phantom-lancer/internal/objectstore"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

const (
	defaultRegistryQuota = 10 * 1024 * 1024 * 1024
	maxManifestBytes     = 8 * 1024 * 1024
	maxBlobUploadBytes   = 8 * 1024 * 1024 * 1024
	// registryGCGracePeriod protects freshly uploaded but not-yet-referenced
	// blobs from being reclaimed by GC before their manifest is pushed.
	registryGCGracePeriod = 1 * time.Hour
)

var (
	repositoryNamePattern = regexp.MustCompile(`^[a-z0-9]+([._-]?[a-z0-9]+)*(\/[a-z0-9]+([._-]?[a-z0-9]+)*)*$`)
	tagPattern            = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestPattern         = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// validateDigest enforces a strict sha256 digest form so a malicious reference
// can never be interpolated into a storage key and escape the registry root.
func validateDigest(digest string) error {
	if !digestPattern.MatchString(strings.TrimSpace(digest)) {
		return errors.New("invalid digest")
	}
	return nil
}

type RegistryStatus struct {
	Enabled            bool   `json:"enabled"`
	Ready              bool   `json:"ready"`
	PublicURL          string `json:"publicUrl,omitempty"`
	StorageBackend     string `json:"storageBackend"`
	StorageDir         string `json:"storageDir,omitempty"`
	ObjectPrefix       string `json:"objectPrefix,omitempty"`
	QuotaBytes         int64  `json:"quotaBytes"`
	UsageBytes         int64  `json:"usageBytes"`
	RepositoryCount    int    `json:"repositoryCount"`
	CredentialCount    int    `json:"credentialCount"`
	RequireTLS         bool   `json:"requireTls"`
	AllowAnonymousPull bool   `json:"allowAnonymousPull"`
	LastError          string `json:"lastError,omitempty"`
}

type RegistryCredentialSecret struct {
	Credential storage.DockerRegistryCredential `json:"credential"`
	Secret     string                           `json:"secret"`
}

type registryAudit struct {
	EventType string
	RiskLevel string
	Summary   string
	Payload   map[string]any
}

func (s *Service) RegistrySettings(ctx context.Context) (storage.DockerRegistrySettings, error) {
	settings, err := s.store.GetDockerRegistrySettings(ctx)
	if err != nil {
		return storage.DockerRegistrySettings{}, err
	}
	return s.normalizeRegistrySettings(settings), nil
}

func (s *Service) UpdateRegistrySettings(ctx context.Context, settings storage.DockerRegistrySettings) (storage.DockerRegistrySettings, error) {
	normalized := s.normalizeRegistrySettings(settings)
	if err := validateRegistrySettings(normalized); err != nil {
		return storage.DockerRegistrySettings{}, err
	}
	if normalized.StorageBackend == "object_storage" {
		if normalized.ObjectStorageProfileID == "" {
			return storage.DockerRegistrySettings{}, errors.New("object storage profile is required")
		}
		if _, err := s.registryObjectClient(ctx, normalized); err != nil {
			return storage.DockerRegistrySettings{}, err
		}
	}
	if normalized.StorageDir != "" {
		if err := s.validateRegistryStorageDir(normalized.StorageDir); err != nil {
			return storage.DockerRegistrySettings{}, err
		}
		if err := os.MkdirAll(normalized.StorageDir, 0o700); err != nil {
			return storage.DockerRegistrySettings{}, err
		}
	}
	return s.store.UpdateDockerRegistrySettings(ctx, normalized)
}

func (s *Service) RegistryStatus(ctx context.Context) RegistryStatus {
	settings, err := s.RegistrySettings(ctx)
	if err != nil {
		return RegistryStatus{LastError: safelog.Error(err, 200)}
	}
	repos, _ := s.store.ListDockerRegistryRepositories(ctx)
	creds, _ := s.store.ListDockerRegistryCredentials(ctx)
	usage := int64(0)
	if backend, backendErr := s.registryBackend(ctx, settings); backendErr == nil {
		usage = backend.Usage(ctx)
	}
	return RegistryStatus{
		Enabled:            settings.Enabled,
		Ready:              settings.Enabled,
		PublicURL:          settings.PublicURL,
		StorageBackend:     settings.StorageBackend,
		StorageDir:         settings.StorageDir,
		ObjectPrefix:       settings.ObjectPrefix,
		QuotaBytes:         settings.QuotaBytes,
		UsageBytes:         usage,
		RepositoryCount:    len(repos),
		CredentialCount:    len(creds),
		RequireTLS:         settings.RequireTLS,
		AllowAnonymousPull: settings.AllowAnonymousPull,
	}
}

func (s *Service) ListRegistryCredentials(ctx context.Context) ([]storage.DockerRegistryCredential, error) {
	return s.store.ListDockerRegistryCredentials(ctx)
}

func (s *Service) CreateRegistryCredential(ctx context.Context, name string, scopes []string, prefix string) (RegistryCredentialSecret, error) {
	secret, hash, err := auth.NewToken()
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	cred, err := s.store.CreateDockerRegistryCredential(ctx, storage.DockerRegistryCredential{
		Name:             strings.TrimSpace(name),
		Status:           "active",
		SecretHash:       hash,
		Scopes:           normalizeScopes(scopes),
		RepositoryPrefix: normalizeRepoPrefix(prefix),
	})
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	return RegistryCredentialSecret{Credential: cred, Secret: secret}, nil
}

func (s *Service) RotateRegistryCredential(ctx context.Context, id string) (RegistryCredentialSecret, error) {
	cred, err := s.store.GetDockerRegistryCredential(ctx, id)
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	secret, hash, err := auth.NewToken()
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	updated, err := s.store.UpdateDockerRegistryCredential(ctx, cred, hash)
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	return RegistryCredentialSecret{Credential: updated, Secret: secret}, nil
}

func (s *Service) UpdateRegistryCredential(ctx context.Context, cred storage.DockerRegistryCredential) (storage.DockerRegistryCredential, error) {
	cred.Scopes = normalizeScopes(cred.Scopes)
	cred.RepositoryPrefix = normalizeRepoPrefix(cred.RepositoryPrefix)
	return s.store.UpdateDockerRegistryCredential(ctx, cred, "")
}

func (s *Service) DeleteRegistryCredential(ctx context.Context, id string) error {
	return s.store.DeleteDockerRegistryCredential(ctx, id)
}

func (s *Service) ListRegistryRepositories(ctx context.Context) ([]storage.DockerRegistryRepository, error) {
	return s.store.ListDockerRegistryRepositories(ctx)
}

func (s *Service) ListRegistryTags(ctx context.Context, repo string) ([]storage.DockerRegistryTag, error) {
	if err := validateRepositoryName(repo); err != nil {
		return nil, err
	}
	return s.store.ListDockerRegistryTags(ctx, repo)
}

func (s *Service) GetRegistryManifest(ctx context.Context, repo, reference string) (storage.DockerRegistryManifest, error) {
	if err := validateRepositoryName(repo); err != nil {
		return storage.DockerRegistryManifest{}, err
	}
	return s.store.ResolveDockerRegistryManifest(ctx, repo, reference)
}

func (s *Service) DeleteRegistryTag(ctx context.Context, repo, tag string) error {
	if err := validateRepositoryName(repo); err != nil {
		return err
	}
	if !tagPattern.MatchString(tag) {
		return errors.New("invalid tag")
	}
	return s.store.DeleteDockerRegistryTag(ctx, repo, tag)
}

func (s *Service) DeleteRegistryManifest(ctx context.Context, repo, digest string) error {
	if err := validateRepositoryName(repo); err != nil {
		return err
	}
	if err := validateDigest(digest); err != nil {
		return err
	}
	settings, err := s.RegistrySettings(ctx)
	if err != nil {
		return err
	}
	backend, err := s.registryBackend(ctx, settings)
	if err != nil {
		return err
	}
	manifest, _ := s.store.ResolveDockerRegistryManifest(ctx, repo, digest)
	if err := s.store.DeleteDockerRegistryManifest(ctx, repo, digest); err != nil {
		return err
	}
	if manifest.ContentPath != "" {
		_ = backend.Delete(ctx, manifest.ContentPath)
	}
	s.auditRegistry(ctx, registryAudit{EventType: "docker.registry.manifest.deleted", RiskLevel: "high", Summary: "Registry manifest 已删除", Payload: registryPayload(repo, "", digest, "", 0)})
	s.append(ctx, "registry", "docker.registry.manifest.deleted", registryPayload(repo, "", digest, "", 0))
	return nil
}

func (s *Service) RegistryGCJob(ctx context.Context) (OperationResult, error) {
	return s.StartJob(ctx, "docker.registry.gc", "Registry GC", "critical", "registry", nil, func(runCtx context.Context, emit func(string, map[string]any)) error {
		// Run exclusively against registry writes: a concurrent push could
		// commit a blob whose manifest is not yet stored, which GC would then
		// mistake for unreferenced and delete.
		s.registryGC.Lock()
		defer s.registryGC.Unlock()
		s.append(runCtx, "registry", "docker.registry.gc.started", nil)
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": "进入 maintenance：已暂停 registry 写入，开始扫描存储"})
		reclaimed, removed, err := s.runRegistryGC(runCtx, emit)
		if err != nil {
			s.append(runCtx, "registry", "docker.registry.gc.failed", map[string]any{"error": safelog.Error(err, 200)})
			return err
		}
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": fmt.Sprintf("已回收 %d 个未引用 blob，清理 %d 个过期 upload 临时文件", reclaimed, removed)})
		s.append(runCtx, "registry", "docker.registry.gc.completed", map[string]any{"reclaimedBlobs": reclaimed, "removedUploads": removed})
		return nil
	})
}

// runRegistryGC performs the storage scan + reclaim. Callers must already hold
// s.registryGC for writing.
func (s *Service) runRegistryGC(runCtx context.Context, emit func(string, map[string]any)) (int, int, error) {
	settings, err := s.RegistrySettings(runCtx)
	if err != nil {
		return 0, 0, err
	}
	backend, err := s.registryBackend(runCtx, settings)
	if err != nil {
		return 0, 0, err
	}

	// 1) Drop soft-deleted manifest objects and purge their rows.
	deletedPaths, err := s.store.PurgeDeletedDockerRegistryManifests(runCtx)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range deletedPaths {
		_ = backend.Delete(runCtx, p)
	}

	// 2) Build the referenced blob set from active manifests (config + layers).
	referenced := map[string]bool{}
	manifests, err := s.store.ActiveDockerRegistryManifests(runCtx)
	if err != nil {
		return 0, 0, err
	}
	for _, m := range manifests {
		for digest := range s.manifestReferencedBlobs(runCtx, backend, m) {
			referenced[digest] = true
		}
	}

	// 3) Delete blobs that no active manifest references. Blobs written within
	// the grace window are skipped: a client may have just uploaded a layer
	// whose manifest has not been pushed yet, and reclaiming it would corrupt
	// the in-flight push.
	blobs, err := backend.ListBlobDigests(runCtx, settings)
	if err != nil {
		return 0, 0, err
	}
	reclaimed := 0
	for _, blob := range blobs {
		if referenced[blob.Digest] {
			continue
		}
		if !blob.Modified.IsZero() && time.Since(blob.Modified) < registryGCGracePeriod {
			continue
		}
		if err := backend.Delete(runCtx, blobKey(settings, blob.Digest)); err == nil {
			reclaimed++
		}
	}

	// 4) Clean expired upload temp files.
	uploads := filepath.Join(settings.StorageDir, "uploads")
	removed := 0
	_ = filepath.WalkDir(uploads, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && time.Since(info.ModTime()) > 24*time.Hour {
			if rmErr := os.Remove(path); rmErr == nil {
				removed++
			}
		}
		return nil
	})
	return reclaimed, removed, nil
}

// manifestReferencedBlobs returns the digest set a manifest keeps alive: the
// manifest digest itself plus any config/layer/manifest digests it embeds.
func (s *Service) manifestReferencedBlobs(ctx context.Context, backend registryBackend, manifest storage.DockerRegistryManifest) map[string]bool {
	out := map[string]bool{}
	if digestPattern.MatchString(manifest.Digest) {
		out[manifest.Digest] = true
	}
	if manifest.ContentPath == "" {
		return out
	}
	_, _, body, err := backend.Open(ctx, manifest.ContentPath, "")
	if err != nil {
		return out
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxManifestBytes))
	if err != nil {
		return out
	}
	var parsed struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return out
	}
	if digestPattern.MatchString(parsed.Config.Digest) {
		out[parsed.Config.Digest] = true
	}
	for _, layer := range parsed.Layers {
		if digestPattern.MatchString(layer.Digest) {
			out[layer.Digest] = true
		}
	}
	for _, child := range parsed.Manifests {
		if digestPattern.MatchString(child.Digest) {
			out[child.Digest] = true
		}
	}
	return out
}

// missingManifestBlob parses an image manifest and returns the first
// config/layer digest whose blob is not present in storage, or "" when all
// referenced blobs exist. Manifest-list/index children are other manifests, not
// blobs, so they are not validated here.
func (s *Service) missingManifestBlob(ctx context.Context, backend registryBackend, settings storage.DockerRegistrySettings, body []byte) string {
	var parsed struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	check := func(digest string) bool {
		if !digestPattern.MatchString(digest) {
			return true
		}
		_, _, err := backend.Head(ctx, blobKey(settings, digest))
		return err == nil
	}
	if parsed.Config.Digest != "" && !check(parsed.Config.Digest) {
		return parsed.Config.Digest
	}
	for _, layer := range parsed.Layers {
		if layer.Digest != "" && !check(layer.Digest) {
			return layer.Digest
		}
	}
	return ""
}

// writeRegistryError emits a Docker Registry V2 error response envelope.
func writeRegistryError(w http.ResponseWriter, status int, code, message, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{
			"code":    code,
			"message": message,
			"detail":  shortDigest(detail),
		}},
	})
}

func (s *Service) ServeRegistry(w http.ResponseWriter, r *http.Request) {
	settings, err := s.RegistrySettings(r.Context())
	if err != nil || !settings.Enabled {
		http.NotFound(w, r)
		return
	}
	cred, ok := s.authenticateRegistry(w, r, settings)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		return
	}
	if path == "_catalog" {
		s.handleRegistryCatalog(w, r)
		return
	}
	if strings.HasSuffix(path, "/tags/list") {
		s.handleRegistryTagsList(w, r, strings.TrimSuffix(path, "/tags/list"))
		return
	}
	if strings.Contains(path, "/blobs/uploads/") {
		s.handleBlobUpload(w, r, settings, cred, path)
		return
	}
	if strings.Contains(path, "/blobs/") {
		s.handleBlob(w, r, settings, cred, path)
		return
	}
	if strings.Contains(path, "/manifests/") {
		s.handleManifest(w, r, settings, cred, path)
		return
	}
	http.NotFound(w, r)
}

func (s *Service) handleRegistryCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	names, err := s.store.ListDockerRegistryRepositoryNames(r.Context())
	if err != nil {
		http.Error(w, "catalog unavailable", http.StatusInternalServerError)
		return
	}
	writeRegistryJSON(w, map[string]any{"repositories": names})
}

func (s *Service) handleRegistryTagsList(w http.ResponseWriter, r *http.Request, repo string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if validateRepositoryName(repo) != nil {
		http.NotFound(w, r)
		return
	}
	tags, err := s.store.ListDockerRegistryTags(r.Context(), repo)
	if err != nil {
		http.Error(w, "tags unavailable", http.StatusInternalServerError)
		return
	}
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Tag)
	}
	writeRegistryJSON(w, map[string]any{"name": repo, "tags": names})
}

func writeRegistryJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Service) authenticateRegistry(w http.ResponseWriter, r *http.Request, settings storage.DockerRegistrySettings) (storage.DockerRegistryCredential, bool) {
	ip := authlimiter.ClientIP(r)
	now := time.Now()
	hasAuthHeader := r.Header.Get("Authorization") != ""
	user, pass, ok := r.BasicAuth()
	if !ok {
		if settings.AllowAnonymousPull && registryReadOnlyRequest(r) {
			return storage.DockerRegistryCredential{ID: "anonymous", Status: "active", Scopes: []string{"registry.pull"}, RepositoryPrefix: ""}, true
		}
		// Docker clients first probe /v2/ with no Authorization to learn
		// the WWW-Authenticate challenge. That's the normal handshake, so
		// it must NOT count as a login failure against the limiter. We
		// still surface a low-severity audit event so operators can tell
		// challenge traffic apart from real errors.
		if hasAuthHeader {
			// Header was present but malformed (not Basic, or not
			// base64-decodable). Treat it as one failed attempt.
			_ = s.onRegistryAuthFailure(r.Context(), "", ip, now, "malformed_basic_auth")
		} else {
			// No Authorization header at all: this is the Docker client's
			// handshake probe that elicits the WWW-Authenticate challenge.
			// It's pure protocol noise, NOT a security event, so we skip
			// the audit row entirely and rely on the 401 telemetry to
			// surface challenge traffic at aggregate volume.
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Phantom Lancer Registry"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return storage.DockerRegistryCredential{}, false
	}
	// Pre-check: if this (username, ip) pair is already in backoff,
	// short-circuit before touching the credential lookup / hash verify.
	if decision := s.registryAuthBackoff.Check(user, ip, now); decision.Limited {
		// Rate-limited hits can be generated by noisy misconfigured
		// clients and vulnerability scanners at hundreds/sec. Gate
		// both the audit row and the service Warn to one event per
		// (dimension, user, ip) per 2s so the audit table and log
		// file stay useful during storms. backoff_started is still
		// emitted per threshold-crossing in onRegistryAuthFailure.
		if s.logSampler.Allow("docker:registry:rate-limited:" + decision.Dimension + ":" + user + ":" + ip) {
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: "docker.registry.auth.rate_limited", RiskLevel: "high", Summary: "Registry 鉴权触发限流", Payload: map[string]any{"username": user, "ip": ip, "dimension": decision.Dimension, "backoff_until": decision.BackoffUntil.UTC().Format(time.RFC3339Nano)}})
			s.log.Warn("docker registry auth rate limited", "username", user, "ip", ip, "dimension", decision.Dimension, "backoff_until", decision.BackoffUntil)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Phantom Lancer Registry"`)
		w.Header().Set("Retry-After", retryAfterFromBackoff(decision.BackoffUntil, now))
		http.Error(w, "too many failed authentications", http.StatusTooManyRequests)
		return storage.DockerRegistryCredential{}, false
	}
	cred, err := s.store.GetDockerRegistryCredentialByName(r.Context(), user)
	if err != nil || cred.Status != "active" || cred.SecretHash != auth.HashToken(pass) {
		_ = s.onRegistryAuthFailure(r.Context(), user, ip, now, reasonForRegistryAuthFailure(err, cred))
		w.Header().Set("WWW-Authenticate", `Basic realm="Phantom Lancer Registry"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return storage.DockerRegistryCredential{}, false
	}
	repo := registryRepoFromPath(strings.TrimPrefix(r.URL.Path, "/v2/"))
	if repo != "" && !strings.HasPrefix(repo, strings.TrimPrefix(cred.RepositoryPrefix, "/")) {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: "docker.registry.auth.forbidden", RiskLevel: "medium", Summary: "Registry 仓库范围拒绝", Payload: map[string]any{"credentialId": cred.ID, "username": cred.Name, "repository": safelog.Redact(repo), "prefix": safelog.Redact(cred.RepositoryPrefix), "method": r.Method, "path": safelog.URLLabel(r.URL.Path), "ip": ip}})
		s.log.Warn("docker registry auth repository scope denied", "username", cred.Name, "repository", safelog.Redact(repo), "prefix", safelog.Redact(cred.RepositoryPrefix))
		http.Error(w, "repository scope denied", http.StatusForbidden)
		return storage.DockerRegistryCredential{}, false
	}
	// Successful authentication: clear the per-username counter so a
	// legitimate user is never punished after a bad password.
	s.registryAuthBackoff.RecordSuccess(user)
	_ = s.store.TouchDockerRegistryCredential(r.Context(), cred.ID)
	// Succeeded auth is low-severity and very hot (daemon hits Basic on
	// every layer fetch). Gate to one row per credential per hour so the
	// Audit view isn't swamped by noise from busy registries.
	if s.registryAuthSuccessSampler.Allow(cred.ID) {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: "docker.registry.auth.succeeded", RiskLevel: "low", Summary: "Registry 鉴权成功", Payload: map[string]any{"credentialId": cred.ID, "username": cred.Name, "method": r.Method, "path": safelog.URLLabel(r.URL.Path), "ip": ip, "anonymous": false}})
	}
	return cred, true
}

// onRegistryAuthFailure records one failed registry Basic Auth attempt
// against the shared limiter and emits a failed-auth audit plus per-
// dimension backoff-started audits when a threshold is crossed. Returns
// the AddAudit error for callers that want to `_ = ` suppress it.
func (s *Service) onRegistryAuthFailure(ctx context.Context, username, ip string, now time.Time, reason string) error {
	events := s.registryAuthBackoff.RecordFailure(username, ip, now)
	// Failed Basic Auth attempts are a common scanner noise
	var err error
	// pattern. Sample the per-request audit + Warn to one per
	// (ip, username) per 2s so a brute-forcer does not dominate
	// the audit table or the service log. Threshold-crossing
	// backoff_started events below are still emitted un-sampled
	// so escalation is visible.
	if s.logSampler.Allow("docker:registry:failed:" + ip + ":" + username) {
		_, err = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "docker.registry.auth.failed", RiskLevel: "medium", Summary: "Registry 鉴权失败", Payload: map[string]any{"username": username, "ip": ip, "reason": reason}})
		s.log.Warn("docker registry auth failed", "username", username, "ip", ip, "reason", reason)
	}
	for _, ev := range events {
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "docker.registry.auth.backoff_started", RiskLevel: "high", Summary: "Registry 鉴权触发退避", Payload: map[string]any{"username": username, "ip": ip, "dimension": ev.Dimension, "duration": ev.Duration.String(), "backoff_until": ev.BackoffUntil.UTC().Format(time.RFC3339Nano)}})
		s.log.Warn("docker registry auth backoff started", "username", username, "ip", ip, "dimension", ev.Dimension, "duration_ms", ev.Duration.Milliseconds())
	}
	return err
}

func reasonForRegistryAuthFailure(err error, cred storage.DockerRegistryCredential) string {
	if err != nil {
		return "credential_lookup_failed"
	}
	if cred.Name == "" {
		return "unknown_username"
	}
	if cred.Status != "active" {
		return "credential_status_" + cred.Status
	}
	return "wrong_secret"
}

// retryAfterFromBackoff returns the Retry-After header value (integer
// seconds) clamped to the range [1, 86400] so a stray zero / negative
// backoff never leaks "0" to clients.
func retryAfterFromBackoff(backoffUntil, now time.Time) string {
	seconds := int(backoffUntil.Sub(now).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 86400 {
		seconds = 86400
	}
	return strconv.Itoa(seconds)
}

func (s *Service) handleManifest(w http.ResponseWriter, r *http.Request, settings storage.DockerRegistrySettings, cred storage.DockerRegistryCredential, path string) {
	repo, ref, ok := splitRegistryPath(path, "/manifests/")
	if !ok || validateRepositoryName(repo) != nil {
		http.NotFound(w, r)
		return
	}
	backend, err := s.registryBackend(r.Context(), settings)
	if err != nil {
		http.Error(w, "registry storage unavailable", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodPut:
		if !hasScope(cred.Scopes, "registry.push") {
			http.Error(w, "push scope required", http.StatusForbidden)
			return
		}
		// Serialize against GC so a manifest cannot be committed while GC is
		// reclaiming blobs based on a stale referenced set.
		s.registryGC.RLock()
		defer s.registryGC.RUnlock()
		body, err := io.ReadAll(io.LimitReader(r.Body, maxManifestBytes+1))
		if err != nil || len(body) > maxManifestBytes {
			http.Error(w, "manifest too large", http.StatusRequestEntityTooLarge)
			return
		}
		digest := sha256Digest(body)
		mediaType := r.Header.Get("Content-Type")
		if mediaType == "" {
			mediaType = "application/vnd.oci.image.manifest.v1+json"
		}
		// Reject manifests that reference config/layer blobs not yet uploaded,
		// so a pull can never resolve a manifest to a missing layer.
		if missing := s.missingManifestBlob(r.Context(), backend, settings, body); missing != "" {
			writeRegistryError(w, http.StatusBadRequest, "MANIFEST_BLOB_UNKNOWN", "manifest references an unknown blob", missing)
			return
		}
		if err := s.checkRegistryQuota(r.Context(), settings, int64(len(body))); err != nil {
			http.Error(w, "registry quota exceeded", http.StatusInsufficientStorage)
			return
		}
		contentPath := manifestKey(settings, digest)
		if err := backend.PutBytes(r.Context(), contentPath, body, mediaType); err != nil {
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
			return
		}
		tag := ""
		if !strings.HasPrefix(ref, "sha256:") && tagPattern.MatchString(ref) {
			tag = ref
		}
		_ = s.store.UpsertDockerRegistryManifest(r.Context(), storage.DockerRegistryManifest{Digest: digest, Repository: repo, MediaType: mediaType, SizeBytes: int64(len(body)), ContentPath: contentPath, PushedBy: cred.ID}, tag)
		payload := registryPayload(repo, tag, digest, cred.ID, int64(len(body)))
		s.auditRegistry(r.Context(), registryAudit{EventType: "docker.registry.manifest.pushed", RiskLevel: "medium", Summary: "Registry manifest 已推送", Payload: payload})
		s.append(r.Context(), "registry", "docker.registry.manifest.pushed", payload)
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Location", "/v2/"+repo+"/manifests/"+digest)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet, http.MethodHead:
		manifest, err := s.store.ResolveDockerRegistryManifest(r.Context(), repo, ref)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Docker-Content-Digest", manifest.Digest)
		w.Header().Set("Content-Type", manifest.MediaType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", manifest.SizeBytes))
		if r.Method == http.MethodHead {
			return
		}
		contentType, _, body, err := backend.Open(r.Context(), manifest.ContentPath, "")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer body.Close()
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		payload := registryPayload(repo, ref, manifest.Digest, cred.ID, manifest.SizeBytes)
		s.auditRegistry(r.Context(), registryAudit{EventType: "docker.registry.manifest.pulled", RiskLevel: "low", Summary: "Registry manifest 已读取", Payload: payload})
		s.append(r.Context(), "registry", "docker.registry.manifest.pulled", payload)
		_, _ = io.Copy(w, body)
	case http.MethodDelete:
		if !hasScope(cred.Scopes, "registry.delete") {
			http.Error(w, "delete scope required", http.StatusForbidden)
			return
		}
		if err := s.DeleteRegistryManifest(r.Context(), repo, ref); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Service) handleBlob(w http.ResponseWriter, r *http.Request, settings storage.DockerRegistrySettings, cred storage.DockerRegistryCredential, path string) {
	_, digest, ok := splitRegistryPath(path, "/blobs/")
	if !ok || validateDigest(digest) != nil {
		http.NotFound(w, r)
		return
	}
	backend, err := s.registryBackend(r.Context(), settings)
	if err != nil {
		http.Error(w, "registry storage unavailable", http.StatusInternalServerError)
		return
	}
	key := blobKey(settings, digest)
	if r.Method == http.MethodHead {
		if _, size, err := backend.Head(r.Context(), key); err == nil {
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Accept-Ranges", "bytes")
	rangeHeader := strings.TrimSpace(r.Header.Get("Range"))
	totalSize := int64(0)
	if _, size, headErr := backend.Head(r.Context(), key); headErr == nil {
		totalSize = size
	}
	contentType, size, body, err := backend.Open(r.Context(), key, rangeHeader)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer body.Close()
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	if rangeHeader != "" && totalSize > 0 {
		if start, length, ok := parseByteRange(rangeHeader, totalSize); ok {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, totalSize))
			w.WriteHeader(http.StatusPartialContent)
		}
	}
	_, _ = io.Copy(w, body)
}

func (s *Service) handleBlobUpload(w http.ResponseWriter, r *http.Request, settings storage.DockerRegistrySettings, cred storage.DockerRegistryCredential, path string) {
	repo, uploadID, ok := splitRegistryPath(path, "/blobs/uploads/")
	if !ok || validateRepositoryName(repo) != nil {
		http.NotFound(w, r)
		return
	}
	if !hasScope(cred.Scopes, "registry.push") {
		http.Error(w, "push scope required", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		// Cross-repository blob mount: if the requested blob already exists in
		// storage, link it without re-uploading.
		mount := r.URL.Query().Get("mount")
		if mount != "" && validateDigest(mount) == nil {
			if backend, err := s.registryBackend(r.Context(), settings); err == nil {
				if _, _, headErr := backend.Head(r.Context(), blobKey(settings, mount)); headErr == nil {
					w.Header().Set("Docker-Content-Digest", mount)
					w.Header().Set("Location", "/v2/"+repo+"/blobs/"+mount)
					w.WriteHeader(http.StatusCreated)
					return
				}
			}
		}
		id, _, err := auth.NewToken()
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		location := "/v2/" + repo + "/blobs/uploads/" + url.PathEscape(id)
		w.Header().Set("Docker-Upload-UUID", id)
		w.Header().Set("Location", location)
		w.Header().Set("Range", "0-0")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	uploadPath := filepath.Join(settings.StorageDir, "uploads", filepath.Base(uploadID))
	if r.Method == http.MethodGet {
		// Upload status: report the current committed offset.
		offset := int64(0)
		if info, statErr := os.Stat(uploadPath); statErr == nil {
			offset = info.Size()
		}
		w.Header().Set("Docker-Upload-UUID", uploadID)
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+url.PathEscape(uploadID))
		if offset > 0 {
			w.Header().Set("Range", fmt.Sprintf("0-%d", offset-1))
		} else {
			w.Header().Set("Range", "0-0")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := os.MkdirAll(filepath.Dir(uploadPath), 0o700); err != nil {
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		file, err := os.OpenFile(uploadPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		_, copyErr := io.Copy(file, io.LimitReader(r.Body, maxBlobUploadBytes))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		total := int64(0)
		if info, statErr := os.Stat(uploadPath); statErr == nil {
			total = info.Size()
		}
		w.Header().Set("Docker-Upload-UUID", uploadID)
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+url.PathEscape(uploadID))
		// Range reports the cumulative committed byte offset, not just this chunk.
		if total > 0 {
			w.Header().Set("Range", fmt.Sprintf("0-%d", total-1))
		} else {
			w.Header().Set("Range", "0-0")
		}
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		digest := r.URL.Query().Get("digest")
		if validateDigest(digest) != nil {
			http.Error(w, "digest required", http.StatusBadRequest)
			return
		}
		file, err := os.OpenFile(uploadPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		if _, err := io.Copy(file, io.LimitReader(r.Body, maxBlobUploadBytes)); err != nil {
			_ = file.Close()
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		_ = file.Close()
		actual, err := fileDigest(uploadPath)
		if err != nil || actual != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		backend, err := s.registryBackend(r.Context(), settings)
		if err != nil {
			http.Error(w, "storage failed", http.StatusInternalServerError)
			return
		}
		// Serialize the final blob commit against GC so a freshly committed
		// blob cannot be reclaimed before its manifest references it.
		s.registryGC.RLock()
		defer s.registryGC.RUnlock()
		if info, statErr := os.Stat(uploadPath); statErr == nil {
			if err := s.checkRegistryQuota(r.Context(), settings, info.Size()); err != nil {
				http.Error(w, "registry quota exceeded", http.StatusInsufficientStorage)
				return
			}
		}
		if err := backend.PutFile(r.Context(), blobKey(settings, digest), uploadPath, "application/octet-stream"); err != nil {
			http.Error(w, "storage failed", http.StatusInternalServerError)
			return
		}
		_ = os.Remove(uploadPath)
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Location", "/v2/"+repo+"/blobs/"+digest)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		_ = os.Remove(uploadPath)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Service) normalizeRegistrySettings(settings storage.DockerRegistrySettings) storage.DockerRegistrySettings {
	settings = storage.NormalizeDockerRegistrySettings(settings)
	if settings.StorageDir == "" {
		settings.StorageDir = filepath.Join(s.dataDir(), "docker", "registry")
	} else if !filepath.IsAbs(settings.StorageDir) {
		settings.StorageDir = filepath.Join(s.dataDir(), settings.StorageDir)
	}
	settings.StorageDir = filepath.Clean(settings.StorageDir)
	if settings.QuotaBytes <= 0 {
		settings.QuotaBytes = defaultRegistryQuota
	}
	return settings
}

func (s *Service) validateRegistryStorageDir(path string) error {
	root, err := filepath.Abs(filepath.Join(s.dataDir(), "docker"))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errors.New("registry storage dir must be under data_dir/docker")
	}
	return nil
}

func (s *Service) dataDir() string {
	if s.registryDataDir != "" {
		return s.registryDataDir
	}
	return ".phantom-data"
}

// blobEntry is a stored blob digest with its last-modified time, used by GC to
// skip recently written blobs that may not yet be referenced by a manifest.
type blobEntry struct {
	Digest   string
	Modified time.Time
}

type registryBackend interface {
	PutBytes(context.Context, string, []byte, string) error
	PutFile(context.Context, string, string, string) error
	Open(context.Context, string, string) (string, int64, io.ReadCloser, error)
	Head(context.Context, string) (string, int64, error)
	Delete(context.Context, string) error
	Usage(context.Context) int64
	// ListBlobDigests enumerates blob digests currently present in storage.
	ListBlobDigests(context.Context, storage.DockerRegistrySettings) ([]blobEntry, error)
}

func (s *Service) registryBackend(ctx context.Context, settings storage.DockerRegistrySettings) (registryBackend, error) {
	if settings.StorageBackend == "object_storage" {
		client, err := s.registryObjectClient(ctx, settings)
		if err != nil {
			return nil, err
		}
		return objectRegistryBackend{client: client, prefix: settings.ObjectPrefix}, nil
	}
	return localRegistryBackend{root: settings.StorageDir}, nil
}

func (s *Service) registryObjectClient(ctx context.Context, settings storage.DockerRegistrySettings) (*objectstore.Client, error) {
	profile, err := s.store.GetObjectStorageProfile(ctx, settings.ObjectStorageProfileID)
	if err != nil {
		return nil, err
	}
	return objectstore.New(profile)
}

type localRegistryBackend struct {
	root string
}

// safePath joins key under root and verifies the cleaned result stays inside
// root, so a crafted key can never escape data_dir/docker/registry.
func (b localRegistryBackend) safePath(key string) (string, error) {
	key = strings.Trim(strings.TrimSpace(key), "/")
	target := filepath.Join(b.root, filepath.FromSlash(key))
	root, err := filepath.Abs(b.root)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", errors.New("registry object key escapes storage root")
	}
	return abs, nil
}

func (b localRegistryBackend) PutBytes(ctx context.Context, key string, data []byte, contentType string) error {
	path, err := b.safePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (b localRegistryBackend) PutFile(ctx context.Context, key, sourcePath, contentType string) error {
	target, err := b.safePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.Rename(sourcePath, target)
}

func (b localRegistryBackend) Open(ctx context.Context, key, rangeHeader string) (string, int64, io.ReadCloser, error) {
	path, err := b.safePath(key)
	if err != nil {
		return "", 0, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", 0, nil, err
	}
	if start, length, ok := parseByteRange(rangeHeader, info.Size()); ok {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			_ = file.Close()
			return "", 0, nil, err
		}
		return "application/octet-stream", length, limitedReadCloser{Reader: io.LimitReader(file, length), Closer: file}, nil
	}
	return "application/octet-stream", info.Size(), file, nil
}

func (b localRegistryBackend) Head(ctx context.Context, key string) (string, int64, error) {
	path, err := b.safePath(key)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return "application/octet-stream", info.Size(), nil
}

func (b localRegistryBackend) Delete(ctx context.Context, key string) error {
	path, err := b.safePath(key)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (b localRegistryBackend) Usage(ctx context.Context) int64 {
	return dirUsage(b.root)
}

// ListBlobDigests walks the local blob tree and reconstructs sha256 digests
// from the layout blobs/sha256/<aa>/<hex>.
func (b localRegistryBackend) ListBlobDigests(ctx context.Context, settings storage.DockerRegistrySettings) ([]blobEntry, error) {
	blobRoot := filepath.Join(b.root, filepath.FromSlash(strings.Trim(registryObjectKey(settings, "blobs/sha256"), "/")))
	out := []blobEntry{}
	_ = filepath.WalkDir(blobRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if digestPattern.MatchString("sha256:" + name) {
			entry := blobEntry{Digest: "sha256:" + name}
			if info, statErr := d.Info(); statErr == nil {
				entry.Modified = info.ModTime()
			}
			out = append(out, entry)
		}
		return nil
	})
	return out, nil
}

type objectRegistryBackend struct {
	client *objectstore.Client
	prefix string
}

func (b objectRegistryBackend) PutBytes(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := b.client.Put(ctx, key, data, contentType)
	return err
}

func (b objectRegistryBackend) PutFile(ctx context.Context, key, sourcePath, contentType string) error {
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	_, err = b.client.PutReader(ctx, key, file, info.Size(), contentType)
	return err
}

func (b objectRegistryBackend) Open(ctx context.Context, key, rangeHeader string) (string, int64, io.ReadCloser, error) {
	return b.client.Open(ctx, key, rangeHeader)
}

func (b objectRegistryBackend) Head(ctx context.Context, key string) (string, int64, error) {
	return b.client.Head(ctx, key)
}

func (b objectRegistryBackend) Delete(ctx context.Context, key string) error {
	err := b.client.Delete(ctx, key)
	if objectstore.IsNotFound(err) {
		return nil
	}
	return err
}

func (b objectRegistryBackend) Usage(ctx context.Context) int64 {
	items, err := b.client.List(ctx, strings.Trim(b.prefix, "/"), 1000)
	if err != nil {
		return 0
	}
	var total int64
	for _, item := range items {
		total += item.Size
	}
	return total
}

// ListBlobDigests enumerates blob digests under the object prefix's blobs path.
func (b objectRegistryBackend) ListBlobDigests(ctx context.Context, settings storage.DockerRegistrySettings) ([]blobEntry, error) {
	prefix := strings.Trim(registryObjectKey(settings, "blobs/sha256"), "/")
	items, err := b.client.List(ctx, prefix, 10000)
	if err != nil {
		return nil, err
	}
	out := []blobEntry{}
	for _, item := range items {
		name := item.Key
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if digestPattern.MatchString("sha256:" + name) {
			out = append(out, blobEntry{Digest: "sha256:" + name, Modified: item.Modified})
		}
	}
	return out, nil
}

func validateRegistrySettings(settings storage.DockerRegistrySettings) error {
	if settings.PublicURL != "" {
		parsed, err := url.Parse(settings.PublicURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" {
			return errors.New("registry public url must be a plain http/https URL")
		}
		if settings.RequireTLS && parsed.Scheme != "https" {
			return errors.New("require TLS needs an https public url")
		}
		if !settings.RequireTLS && parsed.Scheme == "http" && !settings.AllowInsecureLocal {
			return errors.New("insecure registry requires explicit local insecure allowance")
		}
		if !settings.RequireTLS && parsed.Scheme == "http" && !isLocalHost(parsed.Hostname()) {
			return errors.New("insecure registry public url must use localhost or 127.0.0.1")
		}
	}
	if strings.Contains(settings.ObjectPrefix, "..") || strings.ContainsAny(settings.ObjectPrefix, "\\\x00") || strings.HasPrefix(settings.ObjectPrefix, "/") {
		return errors.New("invalid object prefix")
	}
	return nil
}

func isLocalHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func registryReadOnlyRequest(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

func (s *Service) checkRegistryQuota(ctx context.Context, settings storage.DockerRegistrySettings, incoming int64) error {
	if settings.QuotaBytes <= 0 {
		return nil
	}
	backend, err := s.registryBackend(ctx, settings)
	if err != nil {
		return err
	}
	if backend.Usage(ctx)+incoming > settings.QuotaBytes {
		return errors.New("registry quota exceeded")
	}
	return nil
}

func (s *Service) auditRegistry(ctx context.Context, event registryAudit) {
	if s.store == nil {
		return
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: event.EventType, RiskLevel: event.RiskLevel, Summary: event.Summary, Payload: event.Payload})
}

func registryPayload(repo, tag, digest, credID string, size int64) map[string]any {
	payload := map[string]any{
		"repository": safelog.Text(repo, 200),
		"digest":     shortDigest(digest),
		"bytes":      size,
	}
	if tag != "" {
		payload["tag"] = safelog.Text(tag, 128)
	}
	if credID != "" {
		payload["credentialId"] = safelog.Text(credID, 80)
	}
	return payload
}

func shortDigest(digest string) string {
	digest = strings.TrimSpace(digest)
	if strings.HasPrefix(digest, "sha256:") && len(digest) > len("sha256:")+16 {
		return digest[:len("sha256:")+16]
	}
	return safelog.Text(digest, 80)
}

func splitRegistryPath(path, marker string) (string, string, bool) {
	left, right, ok := strings.Cut(path, marker)
	if !ok || left == "" || right == "" {
		return "", "", false
	}
	return left, right, true
}

func registryRepoFromPath(path string) string {
	for _, marker := range []string{"/manifests/", "/blobs/", "/blobs/uploads/"} {
		if repo, _, ok := splitRegistryPath(path, marker); ok {
			return repo
		}
	}
	return ""
}

func validateRepositoryName(name string) error {
	if name == "" || len(name) > 255 || !repositoryNamePattern.MatchString(name) {
		return errors.New("invalid repository name")
	}
	return nil
}

func manifestKey(settings storage.DockerRegistrySettings, digest string) string {
	return registryObjectKey(settings, "manifests/"+strings.TrimPrefix(digest, "sha256:")+".json")
}

func blobKey(settings storage.DockerRegistrySettings, digest string) string {
	hexPart := strings.TrimPrefix(digest, "sha256:")
	prefix := hexPart
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return registryObjectKey(settings, "blobs/sha256/"+prefix+"/"+hexPart)
}

func registryObjectKey(settings storage.DockerRegistrySettings, suffix string) string {
	prefix := strings.Trim(settings.ObjectPrefix, "/")
	if prefix == "" {
		return strings.Trim(suffix, "/")
	}
	return prefix + "/" + strings.Trim(suffix, "/")
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

func parseByteRange(header string, size int64) (int64, int64, bool) {
	header = strings.TrimSpace(header)
	if size <= 0 || !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	startRaw, endRaw, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startRaw), 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if strings.TrimSpace(endRaw) != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(endRaw), 10, 64)
		if err != nil || parsed < start {
			return 0, 0, false
		}
		if parsed < end {
			end = parsed
		}
	}
	return start, end - start + 1, true
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func dirUsage(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func normalizeScopes(scopes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		switch scope {
		case "registry.pull", "registry.push", "registry.delete", "registry.admin":
			if !seen[scope] {
				seen[scope] = true
				out = append(out, scope)
			}
		}
	}
	if len(out) == 0 {
		return []string{"registry.pull", "registry.push"}
	}
	return out
}

func normalizeRepoPrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "personal/"
	}
	return prefix + "/"
}

func hasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want || scope == "registry.admin" {
			return true
		}
	}
	return false
}

func manifestMediaType(header string) string {
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil || mediaType == "" {
		return header
	}
	return mediaType
}
