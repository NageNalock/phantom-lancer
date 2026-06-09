package storage

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"phantom-lancer/internal/ids"
)

type DockerRegistrySettings struct {
	Enabled                bool   `json:"enabled"`
	PublicURL              string `json:"publicUrl"`
	StorageBackend         string `json:"storageBackend"`
	ObjectStorageProfileID string `json:"objectStorageProfileId,omitempty"`
	ObjectPrefix           string `json:"objectPrefix"`
	StorageDir             string `json:"storageDir"`
	QuotaBytes             int64  `json:"quotaBytes"`
	RequireTLS             bool   `json:"requireTls"`
	AllowAnonymousPull     bool   `json:"allowAnonymousPull"`
	AllowInsecureLocal     bool   `json:"allowInsecureLocal"`
	UpdatedAt              string `json:"updatedAt,omitempty"`
}

type DockerRegistryCredential struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	Scopes           []string `json:"scopes"`
	RepositoryPrefix string   `json:"repositoryPrefix"`
	LastUsedAt       string   `json:"lastUsedAt,omitempty"`
	CreatedAt        string   `json:"createdAt"`
	RotatedAt        string   `json:"rotatedAt,omitempty"`
	RevokedAt        string   `json:"revokedAt,omitempty"`
	SecretHash       string   `json:"-"`
}

type DockerRegistryRepository struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SizeBytes    int64  `json:"sizeBytes"`
	TagCount     int    `json:"tagCount"`
	LastPushedAt string `json:"lastPushedAt,omitempty"`
	LastPulledAt string `json:"lastPulledAt,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type DockerRegistryManifest struct {
	Digest      string `json:"digest"`
	Repository  string `json:"repository"`
	MediaType   string `json:"mediaType"`
	SizeBytes   int64  `json:"sizeBytes"`
	ContentPath string `json:"-"`
	PushedBy    string `json:"pushedBy,omitempty"`
	PushedAt    string `json:"pushedAt"`
	DeletedAt   string `json:"deletedAt,omitempty"`
}

type DockerRegistryTag struct {
	Repository string                  `json:"repository"`
	Tag        string                  `json:"tag"`
	Digest     string                  `json:"digest"`
	CreatedAt  string                  `json:"createdAt"`
	UpdatedAt  string                  `json:"updatedAt"`
	DeletedAt  string                  `json:"deletedAt,omitempty"`
	Manifest   *DockerRegistryManifest `json:"manifest,omitempty"`
}

func DefaultDockerRegistrySettings() DockerRegistrySettings {
	return DockerRegistrySettings{
		StorageBackend: "local",
		ObjectPrefix:   "phantom-lancer/docker-registry",
		QuotaBytes:     10 * 1024 * 1024 * 1024,
		RequireTLS:     true,
	}
}

func NormalizeDockerRegistrySettings(settings DockerRegistrySettings) DockerRegistrySettings {
	defaults := DefaultDockerRegistrySettings()
	settings.PublicURL = strings.TrimSpace(settings.PublicURL)
	settings.StorageBackend = strings.TrimSpace(settings.StorageBackend)
	if settings.StorageBackend == "" {
		settings.StorageBackend = defaults.StorageBackend
	}
	if settings.StorageBackend != "local" && settings.StorageBackend != "object_storage" {
		settings.StorageBackend = defaults.StorageBackend
	}
	settings.ObjectStorageProfileID = strings.TrimSpace(settings.ObjectStorageProfileID)
	settings.ObjectPrefix = strings.Trim(strings.TrimSpace(settings.ObjectPrefix), "/")
	if settings.ObjectPrefix == "" {
		settings.ObjectPrefix = defaults.ObjectPrefix
	}
	settings.StorageDir = strings.TrimSpace(settings.StorageDir)
	if settings.QuotaBytes <= 0 {
		settings.QuotaBytes = defaults.QuotaBytes
	}
	return settings
}

func (s *Store) GetDockerRegistrySettings(ctx context.Context) (DockerRegistrySettings, error) {
	values, err := s.GetSettingsByPrefix(ctx, "docker.registry.")
	if err != nil {
		return DockerRegistrySettings{}, err
	}
	settings := DefaultDockerRegistrySettings()
	for key, value := range values {
		switch key {
		case "docker.registry.enabled":
			settings.Enabled = truthySetting(value)
		case "docker.registry.public_url":
			settings.PublicURL = value
		case "docker.registry.storage_backend":
			settings.StorageBackend = value
		case "docker.registry.object_storage_profile_id":
			settings.ObjectStorageProfileID = value
		case "docker.registry.object_prefix":
			settings.ObjectPrefix = value
		case "docker.registry.storage_dir":
			settings.StorageDir = value
		case "docker.registry.quota_bytes":
			settings.QuotaBytes = parseInt64Setting(value, settings.QuotaBytes)
		case "docker.registry.require_tls":
			settings.RequireTLS = truthySetting(value)
		case "docker.registry.allow_anonymous_pull":
			settings.AllowAnonymousPull = truthySetting(value)
		case "docker.registry.allow_insecure_local":
			settings.AllowInsecureLocal = truthySetting(value)
		}
	}
	return NormalizeDockerRegistrySettings(settings), nil
}

func (s *Store) UpdateDockerRegistrySettings(ctx context.Context, settings DockerRegistrySettings) (DockerRegistrySettings, error) {
	settings = NormalizeDockerRegistrySettings(settings)
	if err := s.PutSettings(ctx, map[string]string{
		"docker.registry.enabled":                   boolString(settings.Enabled),
		"docker.registry.public_url":                settings.PublicURL,
		"docker.registry.storage_backend":           settings.StorageBackend,
		"docker.registry.object_storage_profile_id": settings.ObjectStorageProfileID,
		"docker.registry.object_prefix":             settings.ObjectPrefix,
		"docker.registry.storage_dir":               settings.StorageDir,
		"docker.registry.quota_bytes":               int64String(settings.QuotaBytes),
		"docker.registry.require_tls":               boolString(settings.RequireTLS),
		"docker.registry.allow_anonymous_pull":      boolString(settings.AllowAnonymousPull),
		"docker.registry.allow_insecure_local":      boolString(settings.AllowInsecureLocal),
	}); err != nil {
		return DockerRegistrySettings{}, err
	}
	return s.GetDockerRegistrySettings(ctx)
}

func (s *Store) CreateDockerRegistryCredential(ctx context.Context, cred DockerRegistryCredential) (DockerRegistryCredential, error) {
	id, err := ids.New("dockcred")
	if err != nil {
		return DockerRegistryCredential{}, err
	}
	now := now()
	cred.ID = id
	cred.Status = defaultString(cred.Status, "active")
	cred.RepositoryPrefix = defaultString(strings.TrimSpace(cred.RepositoryPrefix), "personal/")
	cred.CreatedAt = now
	_, err = s.db.ExecContext(ctx, `INSERT INTO docker_registry_credentials (id, name, status, secret_hash, scopes, repository_prefix, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, cred.ID, cred.Name, cred.Status, cred.SecretHash, strings.Join(cred.Scopes, ","), cred.RepositoryPrefix, cred.CreatedAt)
	if err != nil {
		return DockerRegistryCredential{}, err
	}
	cred.SecretHash = ""
	return cred, nil
}

func (s *Store) ListDockerRegistryCredentials(ctx context.Context) ([]DockerRegistryCredential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, status, scopes, repository_prefix, last_used_at, created_at, rotated_at, revoked_at FROM docker_registry_credentials ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DockerRegistryCredential
	for rows.Next() {
		var cred DockerRegistryCredential
		var scopes string
		if err := rows.Scan(&cred.ID, &cred.Name, &cred.Status, &scopes, &cred.RepositoryPrefix, &cred.LastUsedAt, &cred.CreatedAt, &cred.RotatedAt, &cred.RevokedAt); err != nil {
			return nil, err
		}
		cred.Scopes = splitSettingList(scopes)
		out = append(out, cred)
	}
	return out, rows.Err()
}

func (s *Store) GetDockerRegistryCredentialByName(ctx context.Context, name string) (DockerRegistryCredential, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, status, secret_hash, scopes, repository_prefix, last_used_at, created_at, rotated_at, revoked_at FROM docker_registry_credentials WHERE name = ?`, name)
	return scanDockerRegistryCredential(row)
}

func (s *Store) GetDockerRegistryCredential(ctx context.Context, id string) (DockerRegistryCredential, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, status, secret_hash, scopes, repository_prefix, last_used_at, created_at, rotated_at, revoked_at FROM docker_registry_credentials WHERE id = ?`, id)
	return scanDockerRegistryCredential(row)
}

func (s *Store) UpdateDockerRegistryCredential(ctx context.Context, cred DockerRegistryCredential, rotateHash string) (DockerRegistryCredential, error) {
	existing, err := s.GetDockerRegistryCredential(ctx, cred.ID)
	if err != nil {
		return DockerRegistryCredential{}, err
	}
	if strings.TrimSpace(cred.Name) == "" {
		cred.Name = existing.Name
	}
	if strings.TrimSpace(cred.Status) == "" {
		cred.Status = existing.Status
	}
	if len(cred.Scopes) == 0 {
		cred.Scopes = existing.Scopes
	}
	if strings.TrimSpace(cred.RepositoryPrefix) == "" {
		cred.RepositoryPrefix = existing.RepositoryPrefix
	}
	if rotateHash != "" {
		_, err = s.db.ExecContext(ctx, `UPDATE docker_registry_credentials SET name = ?, status = ?, secret_hash = ?, scopes = ?, repository_prefix = ?, rotated_at = ? WHERE id = ?`, cred.Name, cred.Status, rotateHash, strings.Join(cred.Scopes, ","), cred.RepositoryPrefix, now(), cred.ID)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE docker_registry_credentials SET name = ?, status = ?, scopes = ?, repository_prefix = ?, revoked_at = CASE WHEN ? = 'revoked' THEN ? ELSE revoked_at END WHERE id = ?`, cred.Name, cred.Status, strings.Join(cred.Scopes, ","), cred.RepositoryPrefix, cred.Status, now(), cred.ID)
	}
	if err != nil {
		return DockerRegistryCredential{}, err
	}
	updated, err := s.GetDockerRegistryCredential(ctx, cred.ID)
	if err == nil {
		updated.SecretHash = ""
	}
	return updated, err
}

func (s *Store) DeleteDockerRegistryCredential(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM docker_registry_credentials WHERE id = ?`, id)
	return err
}

func (s *Store) TouchDockerRegistryCredential(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE docker_registry_credentials SET last_used_at = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) UpsertDockerRegistryManifest(ctx context.Context, manifest DockerRegistryManifest, tag string) error {
	now := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	repoID, err := stableRepoID(manifest.Repository)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO docker_registry_repositories (id, name, size_bytes, tag_count, last_pushed_at, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET size_bytes = size_bytes + excluded.size_bytes, last_pushed_at = excluded.last_pushed_at, updated_at = excluded.updated_at`, repoID, manifest.Repository, manifest.SizeBytes, now, now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO docker_registry_manifests (digest, repository, media_type, size_bytes, content_path, pushed_by, pushed_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(digest) DO UPDATE SET media_type = excluded.media_type, size_bytes = excluded.size_bytes, content_path = excluded.content_path, pushed_by = excluded.pushed_by, pushed_at = excluded.pushed_at, deleted_at = ''`, manifest.Digest, manifest.Repository, manifest.MediaType, manifest.SizeBytes, manifest.ContentPath, manifest.PushedBy, now)
	if err != nil {
		return err
	}
	if tag != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO docker_registry_tags (repository, tag, digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(repository, tag) DO UPDATE SET digest = excluded.digest, updated_at = excluded.updated_at, deleted_at = ''`, manifest.Repository, tag, manifest.Digest, now, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE docker_registry_repositories SET tag_count = (SELECT COUNT(*) FROM docker_registry_tags WHERE repository = ? AND deleted_at = '') WHERE name = ?`, manifest.Repository, manifest.Repository)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListDockerRegistryRepositories(ctx context.Context) ([]DockerRegistryRepository, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, size_bytes, tag_count, last_pushed_at, last_pulled_at, created_at, updated_at FROM docker_registry_repositories ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DockerRegistryRepository
	for rows.Next() {
		var repo DockerRegistryRepository
		if err := rows.Scan(&repo.ID, &repo.Name, &repo.SizeBytes, &repo.TagCount, &repo.LastPushedAt, &repo.LastPulledAt, &repo.CreatedAt, &repo.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, repo)
	}
	return out, rows.Err()
}

func (s *Store) ListDockerRegistryTags(ctx context.Context, repo string) ([]DockerRegistryTag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.repository, t.tag, t.digest, t.created_at, t.updated_at, t.deleted_at, m.media_type, m.size_bytes, m.pushed_by, m.pushed_at FROM docker_registry_tags t LEFT JOIN docker_registry_manifests m ON m.digest = t.digest WHERE t.repository = ? AND t.deleted_at = '' ORDER BY t.updated_at DESC`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DockerRegistryTag
	for rows.Next() {
		var tag DockerRegistryTag
		var mediaType, pushedBy, pushedAt string
		var size int64
		if err := rows.Scan(&tag.Repository, &tag.Tag, &tag.Digest, &tag.CreatedAt, &tag.UpdatedAt, &tag.DeletedAt, &mediaType, &size, &pushedBy, &pushedAt); err != nil {
			return nil, err
		}
		tag.Manifest = &DockerRegistryManifest{Digest: tag.Digest, Repository: tag.Repository, MediaType: mediaType, SizeBytes: size, PushedBy: pushedBy, PushedAt: pushedAt}
		out = append(out, tag)
	}
	return out, rows.Err()
}

func (s *Store) ResolveDockerRegistryManifest(ctx context.Context, repo, reference string) (DockerRegistryManifest, error) {
	var digest string
	if strings.HasPrefix(reference, "sha256:") {
		digest = reference
	} else if err := s.db.QueryRowContext(ctx, `SELECT digest FROM docker_registry_tags WHERE repository = ? AND tag = ? AND deleted_at = ''`, repo, reference).Scan(&digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DockerRegistryManifest{}, ErrNotFound
		}
		return DockerRegistryManifest{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT digest, repository, media_type, size_bytes, content_path, pushed_by, pushed_at, deleted_at FROM docker_registry_manifests WHERE digest = ? AND repository = ? AND deleted_at = ''`, digest, repo)
	return scanDockerRegistryManifest(row)
}

func (s *Store) DeleteDockerRegistryTag(ctx context.Context, repo, tag string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE docker_registry_tags SET deleted_at = ? WHERE repository = ? AND tag = ?`, now(), repo, tag)
	return err
}

func (s *Store) DeleteDockerRegistryManifest(ctx context.Context, repo, digest string) error {
	now := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE docker_registry_manifests SET deleted_at = ? WHERE repository = ? AND digest = ?`, now, repo, digest); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE docker_registry_tags SET deleted_at = ? WHERE repository = ? AND digest = ?`, now, repo, digest); err != nil {
		return err
	}
	return tx.Commit()
}

// ListDockerRegistryRepositoryNames returns repository names that still have at
// least one active manifest, for the native /v2/_catalog endpoint.
func (s *Store) ListDockerRegistryRepositoryNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT repository FROM docker_registry_manifests WHERE deleted_at = '' ORDER BY repository ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ActiveDockerRegistryManifests returns digest + content path for every
// non-deleted manifest, used by GC to compute the referenced blob set.
func (s *Store) ActiveDockerRegistryManifests(ctx context.Context) ([]DockerRegistryManifest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT digest, repository, media_type, size_bytes, content_path, pushed_by, pushed_at, deleted_at FROM docker_registry_manifests WHERE deleted_at = ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DockerRegistryManifest{}
	for rows.Next() {
		var m DockerRegistryManifest
		if err := rows.Scan(&m.Digest, &m.Repository, &m.MediaType, &m.SizeBytes, &m.ContentPath, &m.PushedBy, &m.PushedAt, &m.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PurgeDeletedDockerRegistryManifests permanently removes soft-deleted manifest
// rows and returns their content paths so GC can drop the backing objects.
func (s *Store) PurgeDeletedDockerRegistryManifests(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT content_path FROM docker_registry_manifests WHERE deleted_at != '' AND content_path != ''`)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM docker_registry_tags WHERE deleted_at != ''`); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM docker_registry_manifests WHERE deleted_at != ''`); err != nil {
		return nil, err
	}
	return paths, nil
}

func scanDockerRegistryCredential(row *sql.Row) (DockerRegistryCredential, error) {
	var cred DockerRegistryCredential
	var scopes string
	if err := row.Scan(&cred.ID, &cred.Name, &cred.Status, &cred.SecretHash, &scopes, &cred.RepositoryPrefix, &cred.LastUsedAt, &cred.CreatedAt, &cred.RotatedAt, &cred.RevokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DockerRegistryCredential{}, ErrNotFound
		}
		return DockerRegistryCredential{}, err
	}
	cred.Scopes = splitSettingList(scopes)
	return cred, nil
}

func scanDockerRegistryManifest(row *sql.Row) (DockerRegistryManifest, error) {
	var manifest DockerRegistryManifest
	if err := row.Scan(&manifest.Digest, &manifest.Repository, &manifest.MediaType, &manifest.SizeBytes, &manifest.ContentPath, &manifest.PushedBy, &manifest.PushedAt, &manifest.DeletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DockerRegistryManifest{}, ErrNotFound
		}
		return DockerRegistryManifest{}, err
	}
	return manifest, nil
}

func stableRepoID(name string) (string, error) {
	return ids.New("dockrepo")
}

func splitSettingList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func truthySetting(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseInt64Setting(value string, fallback int64) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
