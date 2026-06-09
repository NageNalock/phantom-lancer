package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/ids"

	_ "github.com/mattn/go-sqlite3"
)

var ErrNotFound = errors.New("not found")

var codexGatewayRequestLogRetention = 5000

type Store struct {
	db *sql.DB
}

type Owner struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
}

type Session struct {
	ID            string
	OwnerID       string
	TokenHash     string
	CSRFTokenHash string
	Trusted       bool
	ExpiresAt     time.Time
	RevokedAt     sql.NullTime
}

type AuditEvent struct {
	ID          string         `json:"id"`
	EventType   string         `json:"eventType"`
	WorkspaceID string         `json:"workspaceId,omitempty"`
	RiskLevel   string         `json:"riskLevel,omitempty"`
	Summary     string         `json:"summary"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   string         `json:"createdAt"`
}

type RuntimeSettings struct {
	AllowedRoots []string `json:"allowedRoots"`
	CookieSecure bool     `json:"cookieSecure"`
	UpdatedAt    string   `json:"updatedAt,omitempty"`
}

type SystemUpdateCheck struct {
	ID                string `json:"id"`
	CurrentVersion    string `json:"currentVersion"`
	LatestVersion     string `json:"latestVersion,omitempty"`
	UpdateAvailable   bool   `json:"updateAvailable"`
	Comparable        bool   `json:"comparable"`
	CanApply          bool   `json:"canApply"`
	Reason            string `json:"reason,omitempty"`
	ReleaseID         string `json:"releaseId,omitempty"`
	ReleaseURL        string `json:"releaseUrl,omitempty"`
	PublishedAt       string `json:"publishedAt,omitempty"`
	AssetName         string `json:"assetName,omitempty"`
	AssetURL          string `json:"-"`
	AssetSizeBytes    int64  `json:"assetSizeBytes,omitempty"`
	ChecksumAssetURL  string `json:"-"`
	ChecksumAvailable bool   `json:"checksumAvailable"`
	PlatformSupported bool   `json:"platformSupported"`
	ETag              string `json:"-"`
	ErrorMessage      string `json:"errorMessage,omitempty"`
	CheckedAt         string `json:"checkedAt"`
}

type SystemUpdateJob struct {
	ID                string `json:"id"`
	CurrentVersion    string `json:"currentVersion"`
	TargetVersion     string `json:"targetVersion"`
	ReleaseID         string `json:"releaseId"`
	AssetName         string `json:"assetName"`
	Status            string `json:"status"`
	Phase             string `json:"phase"`
	BytesDownloaded   int64  `json:"bytesDownloaded"`
	TotalBytes        int64  `json:"totalBytes"`
	ChecksumSHA256    string `json:"checksumSha256,omitempty"`
	InstallBinaryPath string `json:"installBinaryPath,omitempty"`
	BackupBinaryPath  string `json:"backupBinaryPath,omitempty"`
	ErrorMessage      string `json:"errorMessage,omitempty"`
	CreatedAt         string `json:"createdAt"`
	StartedAt         string `json:"startedAt,omitempty"`
	CompletedAt       string `json:"completedAt,omitempty"`
}

type CodexGatewaySettings struct {
	ID                    string `json:"id"`
	Enabled               bool   `json:"enabled"`
	BaseURL               string `json:"baseUrl"`
	OAuthAuthURL          string `json:"oauthAuthUrl"`
	OAuthTokenURL         string `json:"oauthTokenUrl"`
	OAuthClientID         string `json:"oauthClientId"`
	OAuthRedirectURI      string `json:"oauthRedirectUri"`
	RequestTimeoutSeconds int    `json:"requestTimeoutSeconds"`
	RefreshMarginSeconds  int    `json:"refreshMarginSeconds"`
	DefaultInstructions   string `json:"defaultInstructions"`
	InstallationID        string `json:"installationId"`
	CreatedAt             string `json:"createdAt"`
	UpdatedAt             string `json:"updatedAt"`
}

type CodexGatewayAccount struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Status          string `json:"status"`
	ExpiresAt       string `json:"expiresAt,omitempty"`
	Plan            string `json:"plan,omitempty"`
	LastUsedAt      string `json:"lastUsedAt,omitempty"`
	LastCheckedAt   string `json:"lastCheckedAt,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	HasAccessToken  bool   `json:"hasAccessToken"`
	HasRefreshToken bool   `json:"hasRefreshToken"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type CodexGatewayAccountSecret struct {
	CodexGatewayAccount
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
}

type CodexGatewayAccountInput struct {
	Label        string
	Status       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    string
	Plan         string
}

type CodexGatewayAccountPatch struct {
	Label        *string
	Status       *string
	AccessToken  *string
	RefreshToken *string
	ExpiresAt    *string
	ClearExpires bool
	Plan         *string
	ClearPlan    bool
}

type CodexGatewayAPIKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type CodexGatewayAPIKeySecret struct {
	CodexGatewayAPIKey
	KeyHash string `json:"-"`
}

type CodexGatewayModel struct {
	ID          string   `json:"id"`
	Object      string   `json:"object"`
	DisplayName string   `json:"displayName"`
	OwnedBy     string   `json:"ownedBy"`
	Source      string   `json:"source"`
	Plans       []string `json:"plans,omitempty"`
	LastSeenAt  string   `json:"lastSeenAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

type CodexGatewayModelInput struct {
	ID          string
	DisplayName string
	OwnedBy     string
	Source      string
}

type CodexGatewayRequestLog struct {
	ID           string `json:"id"`
	RequestID    string `json:"requestId"`
	APIKind      string `json:"apiKind"`
	Model        string `json:"model,omitempty"`
	AccountID    string `json:"accountId,omitempty"`
	SourceIP     string `json:"sourceIp,omitempty"`
	StatusCode   int    `json:"statusCode"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorSource  string `json:"errorSource,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	LatencyMS    int    `json:"latencyMs"`
	Streamed     bool   `json:"streamed"`
	InputTokens  int    `json:"inputTokens,omitempty"`
	OutputTokens int    `json:"outputTokens,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

type CodexGatewayRequestLogInput struct {
	RequestID    string
	APIKind      string
	Model        string
	AccountID    string
	SourceIP     string
	StatusCode   int
	ErrorCode    string
	ErrorSource  string
	ErrorMessage string
	LatencyMS    int
	Streamed     bool
	InputTokens  int
	OutputTokens int
}

type V2RaySettings struct {
	ID                   string `json:"id"`
	Enabled              bool   `json:"enabled"`
	StartOnPhantomLaunch bool   `json:"startOnPhantomLaunch"`
	AssetDir             string `json:"assetDir"`
	ConfigMode           string `json:"configMode"`
	ConfigFormat         string `json:"configFormat"`
	PublicHost           string `json:"publicHost"`
	Listen               string `json:"listen"`
	Port                 int    `json:"port"`
	Protocol             string `json:"protocol"`
	Transport            string `json:"transport"`
	Security             string `json:"security"`
	WSPath               string `json:"wsPath"`
	TLSCertFile          string `json:"tlsCertFile"`
	TLSKeyFile           string `json:"tlsKeyFile"`
	SniffingEnabled      bool   `json:"sniffingEnabled"`
	BlockPrivateNetwork  bool   `json:"blockPrivateNetwork"`
	LogLevel             string `json:"logLevel"`
	RawConfigJSON        string `json:"rawConfigJson"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
}

type V2RayRemoteClient struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	UUID      string `json:"uuid,omitempty"`
	Email     string `json:"email"`
	Level     int    `json:"level"`
	AlterID   int    `json:"alterId"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	RevokedAt string `json:"revokedAt,omitempty"`
}

type V2RayConfigVersion struct {
	ID               string `json:"id"`
	SettingsHash     string `json:"settingsHash"`
	ConfigHash       string `json:"configHash"`
	ConfigJSONRedact string `json:"configJsonRedacted"`
	ValidationStatus string `json:"validationStatus"`
	ValidationOutput string `json:"validationOutput"`
	ActivatedAt      string `json:"activatedAt,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

type ImageProviderSettings struct {
	ID                    string `json:"id"`
	Provider              string `json:"provider"`
	XAIAPIKey             string `json:"-"`
	HasAPIKey             bool   `json:"hasApiKey"`
	MaskedAPIKey          string `json:"maskedApiKey"`
	DefaultModel          string `json:"defaultModel"`
	DefaultResponseFormat string `json:"defaultResponseFormat"`
	DefaultResolution     string `json:"defaultResolution"`
	DefaultAspectRatio    string `json:"defaultAspectRatio"`
	HistoryRetention      int    `json:"historyRetention"`
	CreatedAt             string `json:"createdAt"`
	UpdatedAt             string `json:"updatedAt"`
}

type ImageStorageSettings struct {
	ID                     string `json:"id"`
	Backend                string `json:"backend"`
	ObjectStorageProfileID string `json:"objectStorageProfileId,omitempty"`
	S3ProviderLabel        string `json:"s3ProviderLabel"`
	S3Bucket               string `json:"s3Bucket"`
	S3Region               string `json:"s3Region"`
	S3Endpoint             string `json:"s3Endpoint,omitempty"`
	S3Prefix               string `json:"s3Prefix"`
	S3ForcePathStyle       bool   `json:"s3ForcePathStyle"`
	S3AccessKeyID          string `json:"-"`
	S3SecretAccessKey      string `json:"-"`
	S3SessionToken         string `json:"-"`
	HasS3Credentials       bool   `json:"hasS3Credentials"`
	MaskedAccessKeyID      string `json:"maskedAccessKeyId"`
	S3AccessMode           string `json:"s3AccessMode"`
	FallbackToLocal        bool   `json:"fallbackToLocal"`
	CreatedAt              string `json:"createdAt"`
	UpdatedAt              string `json:"updatedAt"`
}

// ObjectStorageProfile is a reusable S3-compatible connection profile shared
// across modules (Images, Docker Registry). It only holds connection and
// credential material; module-level prefix/policy lives in each module's own
// settings. Secrets are never serialized to JSON responses.
type ObjectStorageProfile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	ProviderLabel     string `json:"providerLabel"`
	Bucket            string `json:"bucket"`
	Region            string `json:"region"`
	Endpoint          string `json:"endpoint"`
	ForcePathStyle    bool   `json:"forcePathStyle"`
	AccessKeyID       string `json:"-"`
	SecretAccessKey   string `json:"-"`
	SessionToken      string `json:"-"`
	HasCredentials    bool   `json:"hasCredentials"`
	MaskedAccessKeyID string `json:"maskedAccessKeyId"`
	Status            string `json:"status"`
	LastTestedAt      string `json:"lastTestedAt,omitempty"`
	LastError         string `json:"lastError,omitempty"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

type ImageAsset struct {
	ID                     string `json:"id"`
	AssetType              string `json:"assetType"`
	Status                 string `json:"status"`
	Private                bool   `json:"private"`
	Provider               string `json:"provider,omitempty"`
	Model                  string `json:"model,omitempty"`
	JobID                  string `json:"jobId,omitempty"`
	SourceRole             string `json:"sourceRole,omitempty"`
	Slot                   int    `json:"slot,omitempty"`
	PromptPreview          string `json:"promptPreview,omitempty"`
	RevisedPromptPreview   string `json:"revisedPromptPreview,omitempty"`
	OriginalFilename       string `json:"originalFilename,omitempty"`
	OriginalSourceRedacted string `json:"originalSourceRedacted,omitempty"`
	MimeType               string `json:"mimeType,omitempty"`
	Extension              string `json:"extension,omitempty"`
	SizeBytes              int64  `json:"sizeBytes,omitempty"`
	Width                  int    `json:"width,omitempty"`
	Height                 int    `json:"height,omitempty"`
	ChecksumSHA256         string `json:"checksumSha256,omitempty"`
	LocalName              string `json:"localName,omitempty"`
	URL                    string `json:"url,omitempty"`
	DownloadURL            string `json:"downloadUrl,omitempty"`
	StorageBackend         string `json:"storageBackend"`
	ObjectStorageProfileID string `json:"objectStorageProfileId,omitempty"`
	S3Bucket               string `json:"s3Bucket,omitempty"`
	S3Region               string `json:"s3Region,omitempty"`
	S3EndpointLabel        string `json:"s3EndpointLabel,omitempty"`
	S3Key                  string `json:"s3Key,omitempty"`
	S3ETag                 string `json:"s3Etag,omitempty"`
	PrivateAt              string `json:"privateAt,omitempty"`
	ArchivedAt             string `json:"archivedAt,omitempty"`
	DeletedAt              string `json:"deletedAt,omitempty"`
	DeletedReason          string `json:"deletedReason,omitempty"`
	LastError              string `json:"lastError,omitempty"`
	CreatedAt              string `json:"createdAt"`
	UpdatedAt              string `json:"updatedAt"`
}

type ImageGenerationJob struct {
	ID             string                  `json:"id"`
	Provider       string                  `json:"provider"`
	Status         string                  `json:"status"`
	Mode           string                  `json:"mode"`
	ModeLabel      string                  `json:"modeLabel"`
	Model          string                  `json:"model"`
	Endpoint       string                  `json:"endpoint,omitempty"`
	Prompt         string                  `json:"prompt"`
	AspectRatio    string                  `json:"aspectRatio,omitempty"`
	Resolution     string                  `json:"resolution,omitempty"`
	ResponseFormat string                  `json:"responseFormat"`
	ImageCount     int                     `json:"imageCount"`
	SourceCount    int                     `json:"sourceCount"`
	Usage          map[string]any          `json:"usage"`
	ErrorMessage   string                  `json:"errorMessage,omitempty"`
	CreatedAt      string                  `json:"createdAt"`
	StartedAt      string                  `json:"startedAt,omitempty"`
	CompletedAt    string                  `json:"completedAt,omitempty"`
	Sources        []ImageGenerationSource `json:"sources,omitempty"`
	Outputs        []ImageGenerationOutput `json:"outputs,omitempty"`
}

type ImageGenerationSource struct {
	ID          string `json:"id"`
	JobID       string `json:"jobId"`
	AssetID     string `json:"assetId,omitempty"`
	Slot        int    `json:"slot"`
	SourceType  string `json:"sourceType"`
	SourceLabel string `json:"sourceLabel"`
	MimeType    string `json:"mimeType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	URLRedacted string `json:"urlRedacted,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type ImageGenerationOutput struct {
	ID            string `json:"id"`
	JobID         string `json:"jobId"`
	AssetID       string `json:"assetId,omitempty"`
	Slot          int    `json:"slot"`
	RemoteURL     string `json:"remoteUrl,omitempty"`
	LocalName     string `json:"localName,omitempty"`
	URL           string `json:"url,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
	RevisedPrompt string `json:"revisedPrompt,omitempty"`
	Storage       string `json:"storage,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS owner_account (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS web_sessions (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  owner_id TEXT NOT NULL,
  csrf_token_hash TEXT NOT NULL,
  trusted INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  revoked_at TEXT
);
CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  workspace_id TEXT NOT NULL DEFAULT '',
  risk_level TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS system_update_checks (
  id TEXT PRIMARY KEY,
  current_version TEXT NOT NULL,
  latest_version TEXT NOT NULL DEFAULT '',
  update_available INTEGER NOT NULL DEFAULT 0,
  comparable INTEGER NOT NULL DEFAULT 0,
  can_apply INTEGER NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '',
  release_id TEXT NOT NULL DEFAULT '',
  release_url TEXT NOT NULL DEFAULT '',
  published_at TEXT NOT NULL DEFAULT '',
  asset_name TEXT NOT NULL DEFAULT '',
  asset_url TEXT NOT NULL DEFAULT '',
  asset_size_bytes INTEGER NOT NULL DEFAULT 0,
  checksum_asset_url TEXT NOT NULL DEFAULT '',
  checksum_available INTEGER NOT NULL DEFAULT 0,
  platform_supported INTEGER NOT NULL DEFAULT 0,
  etag TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  checked_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_system_update_checks_checked ON system_update_checks(checked_at DESC);
CREATE TABLE IF NOT EXISTS system_update_jobs (
  id TEXT PRIMARY KEY,
  current_version TEXT NOT NULL,
  target_version TEXT NOT NULL,
  release_id TEXT NOT NULL,
  asset_name TEXT NOT NULL,
  status TEXT NOT NULL,
  phase TEXT NOT NULL,
  bytes_downloaded INTEGER NOT NULL DEFAULT 0,
  total_bytes INTEGER NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  install_binary_path TEXT NOT NULL DEFAULT '',
  backup_binary_path TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_system_update_jobs_status ON system_update_jobs(status, created_at DESC);
CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  scope_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  event_type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(scope, scope_id, sequence)
);
CREATE TABLE IF NOT EXISTS docker_registry_credentials (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  secret_hash TEXT NOT NULL,
  scopes TEXT NOT NULL DEFAULT '',
  repository_prefix TEXT NOT NULL DEFAULT 'personal/',
  last_used_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  rotated_at TEXT NOT NULL DEFAULT '',
  revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_docker_registry_credentials_status ON docker_registry_credentials(status, created_at DESC);
CREATE TABLE IF NOT EXISTS docker_registry_repositories (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  tag_count INTEGER NOT NULL DEFAULT 0,
  last_pushed_at TEXT NOT NULL DEFAULT '',
  last_pulled_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS docker_registry_manifests (
  digest TEXT PRIMARY KEY,
  repository TEXT NOT NULL,
  media_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  content_path TEXT NOT NULL DEFAULT '',
  pushed_by TEXT NOT NULL DEFAULT '',
  pushed_at TEXT NOT NULL,
  deleted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_docker_registry_manifests_repo ON docker_registry_manifests(repository, pushed_at DESC);
CREATE TABLE IF NOT EXISTS docker_registry_tags (
  repository TEXT NOT NULL,
  tag TEXT NOT NULL,
  digest TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(repository, tag)
);
CREATE TABLE IF NOT EXISTS codex_gateway_settings (
  id TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  base_url TEXT NOT NULL DEFAULT 'https://chatgpt.com/backend-api',
  oauth_auth_url TEXT NOT NULL DEFAULT 'https://auth.openai.com/oauth/authorize',
  oauth_token_url TEXT NOT NULL DEFAULT 'https://auth.openai.com/oauth/token',
  oauth_client_id TEXT NOT NULL DEFAULT '',
  oauth_redirect_uri TEXT NOT NULL DEFAULT '',
  request_timeout_seconds INTEGER NOT NULL DEFAULT 600,
  refresh_margin_seconds INTEGER NOT NULL DEFAULT 300,
  default_instructions TEXT NOT NULL DEFAULT 'You are a helpful assistant.',
  installation_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS codex_gateway_accounts (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('active', 'disabled', 'invalid', 'rate_limited')),
  access_token_secret TEXT NOT NULL DEFAULT '',
  refresh_token_secret TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT '',
  last_used_at TEXT NOT NULL DEFAULT '',
  last_checked_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_gateway_accounts_status ON codex_gateway_accounts(status, last_used_at, created_at);
CREATE TABLE IF NOT EXISTS codex_gateway_api_keys (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
  last_used_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_gateway_api_keys_status ON codex_gateway_api_keys(status);
CREATE TABLE IF NOT EXISTS codex_gateway_models (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  owned_by TEXT NOT NULL DEFAULT 'codex',
  source TEXT NOT NULL CHECK (source IN ('static', 'upstream')),
  last_seen_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS codex_gateway_model_plans (
  plan TEXT NOT NULL,
  model_id TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY (plan, model_id)
);
CREATE INDEX IF NOT EXISTS idx_codex_gateway_model_plans_model ON codex_gateway_model_plans(model_id);
CREATE TABLE IF NOT EXISTS codex_gateway_request_logs (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  api_kind TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  account_id TEXT NOT NULL DEFAULT '',
  source_ip TEXT NOT NULL DEFAULT '',
  status_code INTEGER NOT NULL,
  error_code TEXT NOT NULL DEFAULT '',
  error_source TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  latency_ms INTEGER NOT NULL,
  streamed INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_gateway_request_logs_created ON codex_gateway_request_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_codex_gateway_request_logs_status ON codex_gateway_request_logs(status_code, created_at DESC);
CREATE TABLE IF NOT EXISTS v2ray_settings (
  id TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  start_on_phantom_launch INTEGER NOT NULL DEFAULT 0,
  asset_dir TEXT NOT NULL DEFAULT '',
  config_mode TEXT NOT NULL DEFAULT 'guided',
  config_format TEXT NOT NULL DEFAULT 'json',
  public_host TEXT NOT NULL DEFAULT '',
  listen TEXT NOT NULL DEFAULT '0.0.0.0',
  port INTEGER NOT NULL DEFAULT 10086,
  protocol TEXT NOT NULL DEFAULT 'vmess',
  transport TEXT NOT NULL DEFAULT 'tcp',
  security TEXT NOT NULL DEFAULT 'none',
  ws_path TEXT NOT NULL DEFAULT '',
  tls_cert_file TEXT NOT NULL DEFAULT '',
  tls_key_file TEXT NOT NULL DEFAULT '',
  sniffing_enabled INTEGER NOT NULL DEFAULT 0,
  block_private_network INTEGER NOT NULL DEFAULT 1,
  log_level TEXT NOT NULL DEFAULT 'warning',
  raw_config_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS v2ray_remote_clients (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  uuid TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL DEFAULT '',
  level INTEGER NOT NULL DEFAULT 0,
  alter_id INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS v2ray_config_versions (
  id TEXT PRIMARY KEY,
  settings_hash TEXT NOT NULL,
  config_hash TEXT NOT NULL,
  config_json_redacted TEXT NOT NULL,
  validation_status TEXT NOT NULL,
  validation_output TEXT NOT NULL DEFAULT '',
  activated_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS image_provider_settings (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL DEFAULT 'xai',
  xai_api_key TEXT NOT NULL DEFAULT '',
  default_model TEXT NOT NULL DEFAULT 'grok-imagine-image-quality',
  default_response_format TEXT NOT NULL DEFAULT 'url',
  default_resolution TEXT NOT NULL DEFAULT '',
  default_aspect_ratio TEXT NOT NULL DEFAULT '',
  history_retention INTEGER NOT NULL DEFAULT 500,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS image_generation_jobs (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL DEFAULT 'xai',
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  mode_label TEXT NOT NULL,
  model TEXT NOT NULL,
  endpoint TEXT NOT NULL DEFAULT '',
  prompt TEXT NOT NULL,
  aspect_ratio TEXT NOT NULL DEFAULT '',
  resolution TEXT NOT NULL DEFAULT '',
  response_format TEXT NOT NULL DEFAULT 'url',
  image_count INTEGER NOT NULL DEFAULT 1,
  source_count INTEGER NOT NULL DEFAULT 0,
  usage_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_image_generation_jobs_created_at ON image_generation_jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_generation_jobs_status ON image_generation_jobs(status, created_at DESC);
CREATE TABLE IF NOT EXISTS image_generation_sources (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  asset_id TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL,
  source_type TEXT NOT NULL,
  source_label TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  url_redacted TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_image_generation_sources_job ON image_generation_sources(job_id, slot);
CREATE TABLE IF NOT EXISTS image_generation_outputs (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  asset_id TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL,
  remote_url TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  revised_prompt TEXT NOT NULL DEFAULT '',
  storage TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_image_generation_outputs_job ON image_generation_outputs(job_id, slot);
CREATE TABLE IF NOT EXISTS image_assets (
  id TEXT PRIMARY KEY,
  asset_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'available',
  private INTEGER NOT NULL DEFAULT 0,
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  job_id TEXT NOT NULL DEFAULT '',
  source_role TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL DEFAULT 0,
  prompt_preview TEXT NOT NULL DEFAULT '',
  revised_prompt_preview TEXT NOT NULL DEFAULT '',
  original_filename TEXT NOT NULL DEFAULT '',
  original_source_redacted TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  extension TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  storage_backend TEXT NOT NULL DEFAULT 'local',
  s3_bucket TEXT NOT NULL DEFAULT '',
  s3_region TEXT NOT NULL DEFAULT '',
  s3_endpoint_label TEXT NOT NULL DEFAULT '',
  s3_key TEXT NOT NULL DEFAULT '',
  s3_etag TEXT NOT NULL DEFAULT '',
  private_at TEXT NOT NULL DEFAULT '',
  archived_at TEXT NOT NULL DEFAULT '',
  deleted_at TEXT NOT NULL DEFAULT '',
  deleted_reason TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_image_assets_created_at ON image_assets(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_type_created ON image_assets(asset_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_storage_created ON image_assets(storage_backend, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_status_created ON image_assets(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_private_created ON image_assets(private, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_job ON image_assets(job_id, slot);
CREATE INDEX IF NOT EXISTS idx_image_assets_checksum ON image_assets(checksum_sha256);
CREATE TABLE IF NOT EXISTS image_storage_settings (
  id TEXT PRIMARY KEY,
  backend TEXT NOT NULL DEFAULT 'local',
  s3_provider_label TEXT NOT NULL DEFAULT 'custom-s3',
  s3_bucket TEXT NOT NULL DEFAULT '',
  s3_region TEXT NOT NULL DEFAULT '',
  s3_endpoint TEXT NOT NULL DEFAULT '',
  s3_prefix TEXT NOT NULL DEFAULT 'phantom-lancer/images',
  s3_force_path_style INTEGER NOT NULL DEFAULT 0,
  s3_access_key_id TEXT NOT NULL DEFAULT '',
  s3_secret_access_key TEXT NOT NULL DEFAULT '',
  s3_session_token TEXT NOT NULL DEFAULT '',
  s3_access_mode TEXT NOT NULL DEFAULT 'proxy',
  fallback_to_local INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS object_storage_profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  provider_label TEXT NOT NULL DEFAULT 'custom-s3',
  bucket TEXT NOT NULL DEFAULT '',
  region TEXT NOT NULL DEFAULT '',
  endpoint TEXT NOT NULL DEFAULT '',
  force_path_style INTEGER NOT NULL DEFAULT 0,
  access_key_id TEXT NOT NULL DEFAULT '',
  secret_access_key TEXT NOT NULL DEFAULT '',
  session_token TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unconfigured',
  last_tested_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_object_storage_profiles_created ON object_storage_profiles(created_at DESC);
`)
	if err != nil {
		return err
	}
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		{"image_generation_sources", "asset_id", "TEXT NOT NULL DEFAULT ''"},
		{"image_generation_outputs", "asset_id", "TEXT NOT NULL DEFAULT ''"},
		{"image_assets", "private", "INTEGER NOT NULL DEFAULT 0"},
		{"image_assets", "private_at", "TEXT NOT NULL DEFAULT ''"},
		{"image_assets", "object_storage_profile_id", "TEXT NOT NULL DEFAULT ''"},
		{"image_storage_settings", "object_storage_profile_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, column.table, column.name, column.def); err != nil {
			return err
		}
	}
	if err := s.migrateImageStorageToObjectProfile(ctx); err != nil {
		return err
	}
	return s.migrateCodexCli(ctx)
}

// migrateImageStorageToObjectProfile is an additive, idempotent migration. When
// the legacy Images storage uses inline "s3" backend with credentials and has
// not yet been linked to an object storage profile, it creates a default
// profile from those values and re-points Images at it via "object_storage".
// Legacy inline columns are left intact for backward compatible reads.
func (s *Store) migrateImageStorageToObjectProfile(ctx context.Context) error {
	if err := s.EnsureImageStorageSettings(ctx); err != nil {
		return err
	}
	settings, err := s.GetImageStorageSettings(ctx)
	if err != nil {
		return err
	}
	if settings.Backend != "s3" || settings.ObjectStorageProfileID != "" {
		return nil
	}
	if settings.S3Bucket == "" || settings.S3Endpoint == "" || settings.S3AccessKeyID == "" || settings.S3SecretAccessKey == "" {
		// Incomplete legacy config; nothing safe to migrate.
		return nil
	}
	profile, err := s.CreateObjectStorageProfile(ctx, ObjectStorageProfile{
		Name:            "Images default object storage",
		ProviderLabel:   settings.S3ProviderLabel,
		Bucket:          settings.S3Bucket,
		Region:          settings.S3Region,
		Endpoint:        settings.S3Endpoint,
		ForcePathStyle:  settings.S3ForcePathStyle,
		AccessKeyID:     settings.S3AccessKeyID,
		SecretAccessKey: settings.S3SecretAccessKey,
		SessionToken:    settings.S3SessionToken,
		Status:          "untested",
	})
	if err != nil {
		return err
	}
	settings.Backend = "object_storage"
	settings.ObjectStorageProfileID = profile.ID
	_, err = s.UpdateImageStorageSettings(ctx, settings, false, false)
	return err
}

// migrateCodexCli creates the additive codex_cli_* schema for the rebuilt Codex
// CLI client module. It never drops or mutates legacy codex_* tables; legacy
// data is detected separately and surfaced as a diagnostic.
func (s *Store) migrateCodexCli(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS codex_cli_installations (
  id TEXT PRIMARY KEY,
  binary_path TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unavailable',
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  doctor_summary_json TEXT NOT NULL DEFAULT '{}',
  last_probe_error TEXT NOT NULL DEFAULT '',
  detected_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS codex_cli_workspaces (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  path TEXT NOT NULL,
  path_summary TEXT NOT NULL DEFAULT '',
  trust_state TEXT NOT NULL DEFAULT 'untrusted',
  default_model TEXT NOT NULL DEFAULT '',
  default_sandbox TEXT NOT NULL DEFAULT 'read-only',
  default_approval_policy TEXT NOT NULL DEFAULT 'on-request',
  network_policy_json TEXT NOT NULL DEFAULT '{}',
  pinned INTEGER NOT NULL DEFAULT 0,
  last_opened_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_workspaces_opened ON codex_cli_workspaces(last_opened_at DESC);
CREATE INDEX IF NOT EXISTS idx_codex_cli_workspaces_pinned ON codex_cli_workspaces(pinned DESC, last_opened_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_threads (
  id TEXT PRIMARY KEY,
  codex_thread_id TEXT NOT NULL DEFAULT '',
  workspace_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'idle',
  source_mode TEXT NOT NULL DEFAULT 'app_server',
  kind TEXT NOT NULL DEFAULT 'code',
  background INTEGER NOT NULL DEFAULT 0,
  background_source TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  sandbox_mode TEXT NOT NULL DEFAULT 'read-only',
  approval_policy TEXT NOT NULL DEFAULT 'on-request',
  pinned INTEGER NOT NULL DEFAULT 0,
  archived_at TEXT NOT NULL DEFAULT '',
  last_turn_id TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_threads_updated ON codex_cli_threads(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_codex_cli_threads_workspace ON codex_cli_threads(workspace_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_turns (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  codex_turn_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running',
  prompt_summary TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  sandbox_mode TEXT NOT NULL DEFAULT '',
  approval_policy TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '',
  usage_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_turns_thread ON codex_cli_turns(thread_id, created_at);
CREATE TABLE IF NOT EXISTS codex_cli_events (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  turn_id TEXT NOT NULL DEFAULT '',
  sequence INTEGER NOT NULL,
  event_type TEXT NOT NULL,
  codex_method TEXT NOT NULL DEFAULT '',
  item_type TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  text_preview TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(thread_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_events_thread ON codex_cli_events(thread_id, sequence);
CREATE TABLE IF NOT EXISTS codex_cli_approvals (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  turn_id TEXT NOT NULL DEFAULT '',
  codex_request_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  action_kind TEXT NOT NULL DEFAULT '',
  command_preview TEXT NOT NULL DEFAULT '',
  cwd_summary TEXT NOT NULL DEFAULT '',
  risk_level TEXT NOT NULL DEFAULT 'medium',
  request_payload_json TEXT NOT NULL DEFAULT '{}',
  decision TEXT NOT NULL DEFAULT '',
  decided_at TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_approvals_status ON codex_cli_approvals(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_codex_cli_approvals_thread ON codex_cli_approvals(thread_id, created_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_runs (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT 'exec',
  pid INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL DEFAULT '',
  last_heartbeat_at TEXT NOT NULL DEFAULT '',
  exited_at TEXT NOT NULL DEFAULT '',
  exit_code INTEGER NOT NULL DEFAULT 0,
  error_summary TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_runs_mode ON codex_cli_runs(mode, status);
CREATE TABLE IF NOT EXISTS codex_cli_attachments (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'image',
  filename TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  storage_path TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_attachments_thread ON codex_cli_attachments(thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_codex_cli_attachments_expires ON codex_cli_attachments(expires_at);
CREATE TABLE IF NOT EXISTS codex_cli_review_comments (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  turn_id TEXT NOT NULL DEFAULT '',
  workspace_id TEXT NOT NULL DEFAULT '',
  file_path TEXT NOT NULL DEFAULT '',
  old_line INTEGER NOT NULL DEFAULT 0,
  new_line INTEGER NOT NULL DEFAULT 0,
  hunk_header TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'open',
  created_at TEXT NOT NULL,
  resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_review_comments_thread ON codex_cli_review_comments(thread_id, created_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_commands (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL DEFAULT '',
  command_preview TEXT NOT NULL DEFAULT '',
  cwd_summary TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued',
  exit_code INTEGER NOT NULL DEFAULT 0,
  output_preview TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_commands_thread ON codex_cli_commands(thread_id, created_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_browser_sessions (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'open',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_browser_sessions_thread ON codex_cli_browser_sessions(thread_id, created_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_automations (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT 'thread_wakeup',
  thread_id TEXT NOT NULL DEFAULT '',
  workspace_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  prompt_summary TEXT NOT NULL DEFAULT '',
  schedule_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  default_sandbox TEXT NOT NULL DEFAULT 'read-only',
  default_approval_policy TEXT NOT NULL DEFAULT 'on-request',
  last_run_at TEXT NOT NULL DEFAULT '',
  next_run_at TEXT NOT NULL DEFAULT '',
  retry_count INTEGER NOT NULL DEFAULT 0,
  failure_backoff_until TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_automations_next ON codex_cli_automations(enabled, next_run_at);
CREATE TABLE IF NOT EXISTS codex_cli_automation_runs (
  id TEXT PRIMARY KEY,
  automation_id TEXT NOT NULL,
  thread_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  client_request_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued',
  started_at TEXT NOT NULL DEFAULT '',
  last_heartbeat_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  finding_summary TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '',
  triage_state TEXT NOT NULL DEFAULT 'open',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(automation_id, client_request_id)
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_automation_runs_triage ON codex_cli_automation_runs(triage_state, created_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_capability_cache (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'unknown',
  payload_json TEXT NOT NULL DEFAULT '{}',
  last_error TEXT NOT NULL DEFAULT '',
  probed_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_capability_cache_kind ON codex_cli_capability_cache(kind, updated_at DESC);
CREATE TABLE IF NOT EXISTS codex_cli_notifications (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL DEFAULT 'codex',
  scope_id TEXT NOT NULL DEFAULT '',
  event_type TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unread',
  severity TEXT NOT NULL DEFAULT 'neutral',
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_codex_cli_notifications_scope ON codex_cli_notifications(scope, status, created_at DESC);
`)
	if err != nil {
		return err
	}
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		{"codex_cli_workspaces", "pinned", "INTEGER NOT NULL DEFAULT 0"},
		{"codex_cli_threads", "background", "INTEGER NOT NULL DEFAULT 0"},
		{"codex_cli_threads", "background_source", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "kind", "TEXT NOT NULL DEFAULT 'code'"},
		{"codex_cli_automations", "retry_count", "INTEGER NOT NULL DEFAULT 0"},
		{"codex_cli_automations", "failure_backoff_until", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_automation_runs", "turn_id", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_automation_runs", "last_heartbeat_at", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, column.table, column.name, column.def); err != nil {
			return err
		}
	}
	return nil
}

// CodexCliLegacyTablesDetected reports whether any retired Codex client tables
// from the previous implementation still exist in the SQLite file. This is only
// surfaced as a diagnostic; it never blocks startup.
func (s *Store) CodexCliLegacyTablesDetected(ctx context.Context) ([]string, error) {
	legacy := []string{
		"codex_approvals",
		"codex_items",
		"codex_turns",
		"codex_sessions",
		"codex_capability_cache",
		"codex_exec_jobs",
		"workspaces",
	}
	found := []string{}
	for _, table := range legacy {
		var name string
		err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = append(found, name)
	}
	return found, nil
}

func (s *Store) ensureColumn(ctx context.Context, table, name, def string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, def))
	return err
}

func NormalizeRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	roots := make([]string, 0, len(settings.AllowedRoots))
	seen := make(map[string]bool, len(settings.AllowedRoots))
	for _, root := range settings.AllowedRoots {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	settings.AllowedRoots = roots
	return settings
}

func (s *Store) EnsureRuntimeSettings(ctx context.Context, defaults RuntimeSettings) error {
	defaults = NormalizeRuntimeSettings(defaults)
	if len(defaults.AllowedRoots) == 0 {
		return errors.New("at least one allowed root is required")
	}
	roots, _ := json.Marshal(defaults.AllowedRoots)
	now := now()
	values := map[string]string{
		"allowed_roots": string(roots),
		"cookie_secure": boolString(defaults.CookieSecure),
	}
	for key, value := range values {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value, updated_at) VALUES (?, ?, ?)`, key, value, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetRuntimeSettings(ctx context.Context) (RuntimeSettings, error) {
	settings := RuntimeSettings{}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, updated_at FROM settings WHERE key IN ('allowed_roots', 'cookie_secure')`)
	if err != nil {
		return RuntimeSettings{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value, updatedAt string
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return RuntimeSettings{}, err
		}
		if updatedAt > settings.UpdatedAt {
			settings.UpdatedAt = updatedAt
		}
		switch key {
		case "allowed_roots":
			_ = json.Unmarshal([]byte(value), &settings.AllowedRoots)
		case "cookie_secure":
			settings.CookieSecure = value == "true" || value == "1"
		}
	}
	if err := rows.Err(); err != nil {
		return RuntimeSettings{}, err
	}
	return NormalizeRuntimeSettings(settings), nil
}

func (s *Store) UpdateRuntimeSettings(ctx context.Context, settings RuntimeSettings) error {
	settings = NormalizeRuntimeSettings(settings)
	if len(settings.AllowedRoots) == 0 {
		return errors.New("at least one allowed root is required")
	}
	roots, _ := json.Marshal(settings.AllowedRoots)
	now := now()
	values := map[string]string{
		"allowed_roots": string(roots),
		"cookie_secure": boolString(settings.CookieSecure),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddSystemUpdateCheck(ctx context.Context, check SystemUpdateCheck) (SystemUpdateCheck, error) {
	id, err := ids.New("updc")
	if err != nil {
		return SystemUpdateCheck{}, err
	}
	check.ID = id
	check.CheckedAt = now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO system_update_checks (
  id, current_version, latest_version, update_available, comparable, can_apply, reason, release_id, release_url, published_at,
  asset_name, asset_url, asset_size_bytes, checksum_asset_url, checksum_available, platform_supported, etag, error_message, checked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		check.ID, check.CurrentVersion, check.LatestVersion, boolInt(check.UpdateAvailable), boolInt(check.Comparable), boolInt(check.CanApply), check.Reason,
		check.ReleaseID, check.ReleaseURL, check.PublishedAt, check.AssetName, check.AssetURL, check.AssetSizeBytes, check.ChecksumAssetURL,
		boolInt(check.ChecksumAvailable), boolInt(check.PlatformSupported), check.ETag, check.ErrorMessage, check.CheckedAt)
	if err != nil {
		return SystemUpdateCheck{}, err
	}
	return check, nil
}

func (s *Store) LatestSystemUpdateCheck(ctx context.Context) (SystemUpdateCheck, error) {
	check, err := scanSystemUpdateCheck(s.db.QueryRowContext(ctx, `SELECT id, current_version, latest_version, update_available, comparable, can_apply, reason, release_id, release_url, published_at, asset_name, asset_url, asset_size_bytes, checksum_asset_url, checksum_available, platform_supported, etag, error_message, checked_at FROM system_update_checks ORDER BY checked_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateCheck{}, ErrNotFound
	}
	return check, err
}

func (s *Store) CreateSystemUpdateJob(ctx context.Context, job SystemUpdateJob) (SystemUpdateJob, error) {
	id, err := ids.New("upd")
	if err != nil {
		return SystemUpdateJob{}, err
	}
	now := now()
	job.ID = id
	job.Status = strings.TrimSpace(job.Status)
	if job.Status == "" {
		job.Status = "running"
	}
	job.Phase = strings.TrimSpace(job.Phase)
	if job.Phase == "" {
		job.Phase = "created"
	}
	job.CreatedAt = now
	_, err = s.db.ExecContext(ctx, `
INSERT INTO system_update_jobs (
  id, current_version, target_version, release_id, asset_name, status, phase, bytes_downloaded, total_bytes,
  checksum_sha256, install_binary_path, backup_binary_path, error_message, created_at, started_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.CurrentVersion, job.TargetVersion, job.ReleaseID, job.AssetName, job.Status, job.Phase, job.BytesDownloaded, job.TotalBytes,
		job.ChecksumSHA256, job.InstallBinaryPath, job.BackupBinaryPath, job.ErrorMessage, job.CreatedAt, job.StartedAt, job.CompletedAt)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	return job, nil
}

func (s *Store) GetSystemUpdateJob(ctx context.Context, id string) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, `SELECT id, current_version, target_version, release_id, asset_name, status, phase, bytes_downloaded, total_bytes, checksum_sha256, install_binary_path, backup_binary_path, error_message, created_at, started_at, completed_at FROM system_update_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) LatestSystemUpdateJob(ctx context.Context) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, `SELECT id, current_version, target_version, release_id, asset_name, status, phase, bytes_downloaded, total_bytes, checksum_sha256, install_binary_path, backup_binary_path, error_message, created_at, started_at, completed_at FROM system_update_jobs ORDER BY created_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) ActiveSystemUpdateJob(ctx context.Context) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, `SELECT id, current_version, target_version, release_id, asset_name, status, phase, bytes_downloaded, total_bytes, checksum_sha256, install_binary_path, backup_binary_path, error_message, created_at, started_at, completed_at FROM system_update_jobs WHERE status IN ('queued', 'running', 'restarting') ORDER BY created_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) SaveSystemUpdateJob(ctx context.Context, job SystemUpdateJob) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE system_update_jobs
SET status = ?, phase = ?, bytes_downloaded = ?, total_bytes = ?, checksum_sha256 = ?, install_binary_path = ?, backup_binary_path = ?, error_message = ?, started_at = ?, completed_at = ?
WHERE id = ?`,
		job.Status, job.Phase, job.BytesDownloaded, job.TotalBytes, job.ChecksumSHA256, job.InstallBinaryPath, job.BackupBinaryPath, job.ErrorMessage, job.StartedAt, job.CompletedAt, job.ID)
	return err
}

func (s *Store) BackupDatabase(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("backup path is required")
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

func DefaultCodexGatewaySettings() CodexGatewaySettings {
	return CodexGatewaySettings{
		ID:                    "default",
		BaseURL:               "https://chatgpt.com/backend-api",
		OAuthAuthURL:          "https://auth.openai.com/oauth/authorize",
		OAuthTokenURL:         "https://auth.openai.com/oauth/token",
		RequestTimeoutSeconds: 600,
		RefreshMarginSeconds:  300,
		DefaultInstructions:   "You are a helpful assistant.",
	}
}

func NormalizeCodexGatewaySettings(settings CodexGatewaySettings) CodexGatewaySettings {
	defaults := DefaultCodexGatewaySettings()
	if strings.TrimSpace(settings.ID) == "" {
		settings.ID = "default"
	}
	settings.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if settings.BaseURL == "" {
		settings.BaseURL = defaults.BaseURL
	}
	settings.OAuthAuthURL = strings.TrimSpace(settings.OAuthAuthURL)
	if settings.OAuthAuthURL == "" {
		settings.OAuthAuthURL = defaults.OAuthAuthURL
	}
	settings.OAuthTokenURL = strings.TrimSpace(settings.OAuthTokenURL)
	if settings.OAuthTokenURL == "" {
		settings.OAuthTokenURL = defaults.OAuthTokenURL
	}
	settings.OAuthClientID = strings.TrimSpace(settings.OAuthClientID)
	settings.OAuthRedirectURI = strings.TrimSpace(settings.OAuthRedirectURI)
	if settings.RequestTimeoutSeconds <= 0 {
		settings.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if settings.RefreshMarginSeconds < 0 {
		settings.RefreshMarginSeconds = defaults.RefreshMarginSeconds
	}
	settings.DefaultInstructions = strings.TrimSpace(settings.DefaultInstructions)
	if settings.DefaultInstructions == "" {
		settings.DefaultInstructions = defaults.DefaultInstructions
	}
	settings.InstallationID = strings.TrimSpace(settings.InstallationID)
	return settings
}

func (s *Store) EnsureCodexGatewaySettings(ctx context.Context) error {
	defaults := DefaultCodexGatewaySettings()
	now := now()
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO codex_gateway_settings (
  id, enabled, base_url, oauth_auth_url, oauth_token_url, oauth_client_id, oauth_redirect_uri, request_timeout_seconds, refresh_margin_seconds, default_instructions, installation_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.ID, boolInt(defaults.Enabled), defaults.BaseURL, defaults.OAuthAuthURL, defaults.OAuthTokenURL, defaults.OAuthClientID, defaults.OAuthRedirectURI, defaults.RequestTimeoutSeconds, defaults.RefreshMarginSeconds, defaults.DefaultInstructions, defaults.InstallationID, now, now)
	return err
}

func (s *Store) GetCodexGatewaySettings(ctx context.Context) (CodexGatewaySettings, error) {
	if err := s.EnsureCodexGatewaySettings(ctx); err != nil {
		return CodexGatewaySettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, enabled, base_url, oauth_auth_url, oauth_token_url, oauth_client_id, oauth_redirect_uri, request_timeout_seconds, refresh_margin_seconds, default_instructions, installation_id, created_at, updated_at FROM codex_gateway_settings WHERE id = 'default'`)
	settings, err := scanCodexGatewaySettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultCodexGatewaySettings(), nil
	}
	return NormalizeCodexGatewaySettings(settings), err
}

func (s *Store) UpdateCodexGatewaySettings(ctx context.Context, settings CodexGatewaySettings) (CodexGatewaySettings, error) {
	existing, err := s.GetCodexGatewaySettings(ctx)
	if err != nil {
		return CodexGatewaySettings{}, err
	}
	settings = NormalizeCodexGatewaySettings(settings)
	settings.ID = "default"
	settings.CreatedAt = existing.CreatedAt
	if settings.CreatedAt == "" {
		settings.CreatedAt = now()
	}
	settings.UpdatedAt = now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_gateway_settings (
  id, enabled, base_url, oauth_auth_url, oauth_token_url, oauth_client_id, oauth_redirect_uri, request_timeout_seconds, refresh_margin_seconds, default_instructions, installation_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  enabled = excluded.enabled,
  base_url = excluded.base_url,
  oauth_auth_url = excluded.oauth_auth_url,
  oauth_token_url = excluded.oauth_token_url,
  oauth_client_id = excluded.oauth_client_id,
  oauth_redirect_uri = excluded.oauth_redirect_uri,
  request_timeout_seconds = excluded.request_timeout_seconds,
  refresh_margin_seconds = excluded.refresh_margin_seconds,
  default_instructions = excluded.default_instructions,
  installation_id = excluded.installation_id,
  updated_at = excluded.updated_at`,
		settings.ID, boolInt(settings.Enabled), settings.BaseURL, settings.OAuthAuthURL, settings.OAuthTokenURL, settings.OAuthClientID, settings.OAuthRedirectURI, settings.RequestTimeoutSeconds, settings.RefreshMarginSeconds, settings.DefaultInstructions, settings.InstallationID, settings.CreatedAt, settings.UpdatedAt)
	if err != nil {
		return CodexGatewaySettings{}, err
	}
	return s.GetCodexGatewaySettings(ctx)
}

func (s *Store) ListCodexGatewayAPIKeys(ctx context.Context) ([]CodexGatewayAPIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, status, last_used_at, created_at, updated_at FROM codex_gateway_api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexGatewayAPIKey{}
	for rows.Next() {
		key, err := scanCodexGatewayAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Store) CreateCodexGatewayAPIKey(ctx context.Context, name, keyHash string) (CodexGatewayAPIKey, error) {
	id, err := ids.New("cgkey")
	if err != nil {
		return CodexGatewayAPIKey{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Codex Gateway key"
	}
	now := now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO codex_gateway_api_keys (id, name, key_hash, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)`, id, name, keyHash, now, now)
	if err != nil {
		return CodexGatewayAPIKey{}, err
	}
	return s.GetCodexGatewayAPIKey(ctx, id)
}

func (s *Store) GetCodexGatewayAPIKey(ctx context.Context, id string) (CodexGatewayAPIKey, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, status, last_used_at, created_at, updated_at FROM codex_gateway_api_keys WHERE id = ?`, id)
	key, err := scanCodexGatewayAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexGatewayAPIKey{}, ErrNotFound
	}
	return key, err
}

func (s *Store) GetActiveCodexGatewayAPIKeyByHash(ctx context.Context, keyHash string) (CodexGatewayAPIKeySecret, error) {
	var key CodexGatewayAPIKeySecret
	err := s.db.QueryRowContext(ctx, `SELECT id, name, key_hash, status, last_used_at, created_at, updated_at FROM codex_gateway_api_keys WHERE key_hash = ? AND status = 'active'`, keyHash).Scan(&key.ID, &key.Name, &key.KeyHash, &key.Status, &key.LastUsedAt, &key.CreatedAt, &key.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexGatewayAPIKeySecret{}, ErrNotFound
	}
	return key, err
}

func (s *Store) MarkCodexGatewayAPIKeyUsed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_gateway_api_keys SET last_used_at = ?, updated_at = ? WHERE id = ?`, now(), now(), id)
	return err
}

func (s *Store) UpdateCodexGatewayAPIKeyStatus(ctx context.Context, id, status string) (CodexGatewayAPIKey, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE codex_gateway_api_keys SET status = ?, updated_at = ? WHERE id = ?`, status, now(), id)
	if err != nil {
		return CodexGatewayAPIKey{}, err
	}
	return s.GetCodexGatewayAPIKey(ctx, id)
}

func (s *Store) RotateCodexGatewayAPIKey(ctx context.Context, id, keyHash string) (CodexGatewayAPIKey, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_gateway_api_keys SET key_hash = ?, status = 'active', updated_at = ? WHERE id = ?`, keyHash, now(), id)
	if err != nil {
		return CodexGatewayAPIKey{}, err
	}
	return s.GetCodexGatewayAPIKey(ctx, id)
}

func (s *Store) DeleteCodexGatewayAPIKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_gateway_api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func NormalizeCodexGatewayAccountInput(input CodexGatewayAccountInput) CodexGatewayAccountInput {
	input.Label = strings.TrimSpace(input.Label)
	if input.Label == "" {
		input.Label = "Codex account"
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	input.AccessToken = strings.TrimSpace(input.AccessToken)
	input.RefreshToken = strings.TrimSpace(input.RefreshToken)
	input.ExpiresAt = strings.TrimSpace(input.ExpiresAt)
	input.Plan = strings.TrimSpace(input.Plan)
	return input
}

func (s *Store) ListCodexGatewayAccounts(ctx context.Context) ([]CodexGatewayAccount, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, label, status, expires_at, plan, last_used_at, last_checked_at, last_error,
  CASE WHEN access_token_secret != '' THEN 1 ELSE 0 END,
  CASE WHEN refresh_token_secret != '' THEN 1 ELSE 0 END,
  created_at, updated_at
FROM codex_gateway_accounts
ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexGatewayAccount{}
	for rows.Next() {
		account, err := scanCodexGatewayAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

func (s *Store) CreateCodexGatewayAccount(ctx context.Context, input CodexGatewayAccountInput) (CodexGatewayAccount, error) {
	input = NormalizeCodexGatewayAccountInput(input)
	id, err := ids.New("cgacct")
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	now := now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_gateway_accounts (id, label, status, access_token_secret, refresh_token_secret, expires_at, plan, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.Label, input.Status, input.AccessToken, input.RefreshToken, input.ExpiresAt, input.Plan, now, now)
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	return s.GetCodexGatewayAccount(ctx, id)
}

func (s *Store) GetCodexGatewayAccount(ctx context.Context, id string) (CodexGatewayAccount, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, label, status, expires_at, plan, last_used_at, last_checked_at, last_error,
  CASE WHEN access_token_secret != '' THEN 1 ELSE 0 END,
  CASE WHEN refresh_token_secret != '' THEN 1 ELSE 0 END,
  created_at, updated_at
FROM codex_gateway_accounts
WHERE id = ?`, id)
	account, err := scanCodexGatewayAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexGatewayAccount{}, ErrNotFound
	}
	return account, err
}

func (s *Store) GetCodexGatewayAccountSecret(ctx context.Context, id string) (CodexGatewayAccountSecret, error) {
	var secret CodexGatewayAccountSecret
	var hasAccess, hasRefresh int
	err := s.db.QueryRowContext(ctx, `
SELECT id, label, status, expires_at, plan, last_used_at, last_checked_at, last_error,
  CASE WHEN access_token_secret != '' THEN 1 ELSE 0 END,
  CASE WHEN refresh_token_secret != '' THEN 1 ELSE 0 END,
  created_at, updated_at, access_token_secret, refresh_token_secret
FROM codex_gateway_accounts
WHERE id = ?`, id).Scan(&secret.ID, &secret.Label, &secret.Status, &secret.ExpiresAt, &secret.Plan, &secret.LastUsedAt, &secret.LastCheckedAt, &secret.LastError, &hasAccess, &hasRefresh, &secret.CreatedAt, &secret.UpdatedAt, &secret.AccessToken, &secret.RefreshToken)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexGatewayAccountSecret{}, ErrNotFound
	}
	if err != nil {
		return CodexGatewayAccountSecret{}, err
	}
	secret.HasAccessToken = hasAccess == 1
	secret.HasRefreshToken = hasRefresh == 1
	return secret, nil
}

func (s *Store) UpdateCodexGatewayAccount(ctx context.Context, id string, patch CodexGatewayAccountPatch) (CodexGatewayAccount, error) {
	current, err := s.GetCodexGatewayAccountSecret(ctx, id)
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	label := current.Label
	status := current.Status
	accessToken := current.AccessToken
	refreshToken := current.RefreshToken
	expiresAt := current.ExpiresAt
	plan := current.Plan
	if patch.Label != nil {
		label = strings.TrimSpace(*patch.Label)
	}
	if strings.TrimSpace(label) == "" {
		label = current.Label
	}
	if patch.Status != nil {
		status = strings.TrimSpace(*patch.Status)
	}
	if patch.AccessToken != nil {
		accessToken = strings.TrimSpace(*patch.AccessToken)
	}
	if patch.RefreshToken != nil {
		refreshToken = strings.TrimSpace(*patch.RefreshToken)
	}
	if patch.ClearExpires {
		expiresAt = ""
	} else if patch.ExpiresAt != nil {
		expiresAt = strings.TrimSpace(*patch.ExpiresAt)
	}
	if patch.ClearPlan {
		plan = ""
	} else if patch.Plan != nil {
		plan = strings.TrimSpace(*patch.Plan)
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET label = ?, status = ?, access_token_secret = ?, refresh_token_secret = ?, expires_at = ?, plan = ?, updated_at = ?
WHERE id = ?`, label, status, accessToken, refreshToken, expiresAt, plan, now(), id)
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	return s.GetCodexGatewayAccount(ctx, id)
}

func (s *Store) DeleteCodexGatewayAccount(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_gateway_accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateCodexGatewayAccountTokens(ctx context.Context, id, accessToken, refreshToken, expiresAt string) (CodexGatewayAccountSecret, error) {
	_, err := s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET status = 'active', access_token_secret = ?, refresh_token_secret = ?, expires_at = ?, last_checked_at = ?, last_error = '', updated_at = ?
WHERE id = ?`, strings.TrimSpace(accessToken), strings.TrimSpace(refreshToken), strings.TrimSpace(expiresAt), now(), now(), id)
	if err != nil {
		return CodexGatewayAccountSecret{}, err
	}
	return s.GetCodexGatewayAccountSecret(ctx, id)
}

func (s *Store) MarkCodexGatewayAccountUsed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_gateway_accounts SET last_used_at = ?, updated_at = ? WHERE id = ?`, now(), now(), id)
	return err
}

func (s *Store) UpdateCodexGatewayAccountCheck(ctx context.Context, id, status, plan, lastError string) (CodexGatewayAccount, error) {
	if strings.TrimSpace(status) == "" {
		status = "active"
	}
	if strings.TrimSpace(plan) == "" {
		_, err := s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET status = ?, last_checked_at = ?, last_error = ?, updated_at = ?
WHERE id = ?`, status, now(), lastError, now(), id)
		if err != nil {
			return CodexGatewayAccount{}, err
		}
		return s.GetCodexGatewayAccount(ctx, id)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET status = ?, plan = ?, last_checked_at = ?, last_error = ?, updated_at = ?
WHERE id = ?`, status, strings.TrimSpace(plan), now(), lastError, now(), id)
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	return s.GetCodexGatewayAccount(ctx, id)
}

func (s *Store) SelectCodexGatewayAccountForModel(ctx context.Context, model string, excludeIDs []string) (CodexGatewayAccountSecret, error) {
	excluded := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excluded[strings.TrimSpace(id)] = true
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id
FROM codex_gateway_accounts
WHERE status = 'active' AND (access_token_secret != '' OR refresh_token_secret != '')
ORDER BY
  CASE WHEN last_used_at = '' THEN 0 ELSE 1 END,
  last_used_at ASC,
  created_at ASC`)
	if err != nil {
		return CodexGatewayAccountSecret{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return CodexGatewayAccountSecret{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CodexGatewayAccountSecret{}, err
	}
	rows.Close()
	for _, id := range ids {
		if excluded[id] {
			continue
		}
		secret, err := s.GetCodexGatewayAccountSecret(ctx, id)
		if err != nil {
			return CodexGatewayAccountSecret{}, err
		}
		ok, err := s.CodexGatewayAccountCanUseModel(ctx, secret.CodexGatewayAccount, model)
		if err != nil {
			return CodexGatewayAccountSecret{}, err
		}
		if ok {
			return secret, nil
		}
	}
	return CodexGatewayAccountSecret{}, ErrNotFound
}

func (s *Store) UpsertCodexGatewayModels(ctx context.Context, models []CodexGatewayModelInput) error {
	now := now()
	for _, model := range models {
		model = normalizeCodexGatewayModelInput(model)
		if model.ID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO codex_gateway_models (id, display_name, owned_by, source, last_seen_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  display_name = excluded.display_name,
  owned_by = excluded.owned_by,
  source = excluded.source,
  last_seen_at = excluded.last_seen_at,
  updated_at = excluded.updated_at`, model.ID, model.DisplayName, model.OwnedBy, model.Source, now, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertCodexGatewayModelsForPlan(ctx context.Context, plan string, models []CodexGatewayModelInput) error {
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return s.UpsertCodexGatewayModels(ctx, models)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := now()
	seen := map[string]bool{}
	for _, model := range models {
		model = normalizeCodexGatewayModelInput(model)
		if model.ID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO codex_gateway_models (id, display_name, owned_by, source, last_seen_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  display_name = excluded.display_name,
  owned_by = excluded.owned_by,
  source = excluded.source,
  last_seen_at = excluded.last_seen_at,
  updated_at = excluded.updated_at`, model.ID, model.DisplayName, model.OwnedBy, model.Source, now, now); err != nil {
			return err
		}
		seen[model.ID] = true
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM codex_gateway_model_plans WHERE plan = ?`, plan); err != nil {
		return err
	}
	for id := range seen {
		if _, err := tx.ExecContext(ctx, `INSERT INTO codex_gateway_model_plans (plan, model_id, last_seen_at) VALUES (?, ?, ?)`, plan, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func normalizeCodexGatewayModelInput(model CodexGatewayModelInput) CodexGatewayModelInput {
	model.ID = strings.TrimSpace(model.ID)
	model.DisplayName = strings.TrimSpace(model.DisplayName)
	if model.DisplayName == "" {
		model.DisplayName = model.ID
	}
	model.OwnedBy = strings.TrimSpace(model.OwnedBy)
	if model.OwnedBy == "" {
		model.OwnedBy = "codex"
	}
	model.Source = strings.TrimSpace(model.Source)
	if model.Source == "" {
		model.Source = "upstream"
	}
	return model
}

func (s *Store) ListCodexGatewayModels(ctx context.Context) ([]CodexGatewayModel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, display_name, owned_by, source, last_seen_at, updated_at FROM codex_gateway_models ORDER BY CASE WHEN source = 'static' THEN 0 ELSE 1 END, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexGatewayModel{}
	for rows.Next() {
		model, err := scanCodexGatewayModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, model)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	plans, err := s.codexGatewayModelPlans(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Plans = plans[out[i].ID]
	}
	return out, nil
}

func (s *Store) GetCodexGatewayModel(ctx context.Context, id string) (CodexGatewayModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, display_name, owned_by, source, last_seen_at, updated_at FROM codex_gateway_models WHERE id = ?`, strings.TrimSpace(id))
	model, err := scanCodexGatewayModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexGatewayModel{}, ErrNotFound
	}
	if err != nil {
		return CodexGatewayModel{}, err
	}
	model.Plans, err = s.CodexGatewayModelPlans(ctx, model.ID)
	return model, err
}

func (s *Store) CodexGatewayModelPlans(ctx context.Context, modelID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT plan FROM codex_gateway_model_plans WHERE model_id = ? ORDER BY plan ASC`, strings.TrimSpace(modelID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := []string{}
	for rows.Next() {
		var plan string
		if err := rows.Scan(&plan); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *Store) codexGatewayModelPlans(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT model_id, plan FROM codex_gateway_model_plans ORDER BY model_id ASC, plan ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := map[string][]string{}
	for rows.Next() {
		var modelID, plan string
		if err := rows.Scan(&modelID, &plan); err != nil {
			return nil, err
		}
		plans[modelID] = append(plans[modelID], plan)
	}
	return plans, rows.Err()
}

func (s *Store) CodexGatewayPlanModelCount(ctx context.Context, plan string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_gateway_model_plans WHERE plan = ?`, strings.TrimSpace(plan)).Scan(&count)
	return count, err
}

func (s *Store) CodexGatewayPlanSupportsModel(ctx context.Context, plan, modelID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM codex_gateway_model_plans WHERE plan = ? AND model_id = ? LIMIT 1`, strings.TrimSpace(plan), strings.TrimSpace(modelID)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists == 1, err
}

func (s *Store) CodexGatewayAccountCanUseModel(ctx context.Context, account CodexGatewayAccount, modelID string) (bool, error) {
	plan := strings.TrimSpace(account.Plan)
	if strings.TrimSpace(modelID) == "" || plan == "" {
		return true, nil
	}
	count, err := s.CodexGatewayPlanModelCount(ctx, plan)
	if err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil
	}
	return s.CodexGatewayPlanSupportsModel(ctx, plan, modelID)
}

func (s *Store) CreateCodexGatewayRequestLog(ctx context.Context, input CodexGatewayRequestLogInput) error {
	id, err := ids.New("cgreq")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
	INSERT INTO codex_gateway_request_logs (
	  id, request_id, api_kind, model, account_id, source_ip, status_code, error_code, error_source, error_message, latency_ms, streamed, input_tokens, output_tokens, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.RequestID, input.APIKind, input.Model, input.AccountID, input.SourceIP, input.StatusCode, input.ErrorCode, input.ErrorSource, input.ErrorMessage, input.LatencyMS, boolInt(input.Streamed), input.InputTokens, input.OutputTokens, now())
	if err != nil {
		return err
	}
	if err := pruneCodexGatewayRequestLogs(ctx, tx, codexGatewayRequestLogRetention); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PruneCodexGatewayRequestLogs(ctx context.Context, retention int) error {
	if retention <= 0 {
		retention = codexGatewayRequestLogRetention
	}
	return pruneCodexGatewayRequestLogs(ctx, s.db, retention)
}

type codexGatewayRequestLogPruner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func pruneCodexGatewayRequestLogs(ctx context.Context, execer codexGatewayRequestLogPruner, retention int) error {
	if retention <= 0 {
		return nil
	}
	_, err := execer.ExecContext(ctx, `
	DELETE FROM codex_gateway_request_logs
	WHERE id IN (
	  SELECT id FROM codex_gateway_request_logs
	  ORDER BY created_at DESC, id DESC
	  LIMIT -1 OFFSET ?
	)`, retention)
	return err
}

func (s *Store) ListCodexGatewayRequestLogs(ctx context.Context, limit int) ([]CodexGatewayRequestLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, request_id, api_kind, model, account_id, source_ip, status_code, error_code, error_source, error_message, latency_ms, streamed, input_tokens, output_tokens, created_at FROM codex_gateway_request_logs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexGatewayRequestLog{}
	for rows.Next() {
		log, err := scanCodexGatewayRequestLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

func (s *Store) CodexGatewayRecentRequestSummary(ctx context.Context, since time.Time) (int, int, error) {
	var total, failed int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0) FROM codex_gateway_request_logs WHERE created_at >= ?`, formatTime(since)).Scan(&total, &failed)
	return total, failed, err
}

func DefaultV2RaySettings() V2RaySettings {
	return V2RaySettings{
		ID:                  "default",
		ConfigMode:          "guided",
		ConfigFormat:        "json",
		Listen:              "0.0.0.0",
		Port:                10086,
		Protocol:            "vmess",
		Transport:           "tcp",
		Security:            "none",
		BlockPrivateNetwork: true,
		LogLevel:            "warning",
	}
}

func NormalizeV2RaySettings(settings V2RaySettings) V2RaySettings {
	if settings.ID == "" {
		settings.ID = "default"
	}
	settings.AssetDir = strings.TrimSpace(settings.AssetDir)
	settings.ConfigMode = strings.TrimSpace(settings.ConfigMode)
	if settings.ConfigMode == "" {
		settings.ConfigMode = "guided"
	}
	settings.ConfigFormat = strings.TrimSpace(settings.ConfigFormat)
	if settings.ConfigFormat == "" {
		settings.ConfigFormat = "json"
	}
	settings.PublicHost = strings.TrimSpace(settings.PublicHost)
	settings.Listen = strings.TrimSpace(settings.Listen)
	if settings.Listen == "" {
		settings.Listen = "0.0.0.0"
	}
	if settings.Port == 0 {
		settings.Port = 10086
	}
	settings.Protocol = strings.TrimSpace(strings.ToLower(settings.Protocol))
	if settings.Protocol == "" {
		settings.Protocol = "vmess"
	}
	settings.Transport = strings.TrimSpace(strings.ToLower(settings.Transport))
	if settings.Transport == "" {
		settings.Transport = "tcp"
	}
	settings.Security = strings.TrimSpace(strings.ToLower(settings.Security))
	if settings.Security == "" {
		settings.Security = "none"
	}
	settings.WSPath = strings.TrimSpace(settings.WSPath)
	settings.TLSCertFile = strings.TrimSpace(settings.TLSCertFile)
	settings.TLSKeyFile = strings.TrimSpace(settings.TLSKeyFile)
	settings.LogLevel = strings.TrimSpace(strings.ToLower(settings.LogLevel))
	if settings.LogLevel == "" {
		settings.LogLevel = "warning"
	}
	settings.RawConfigJSON = strings.TrimSpace(settings.RawConfigJSON)
	return settings
}

func (s *Store) EnsureV2RaySettings(ctx context.Context) error {
	defaults := DefaultV2RaySettings()
	now := now()
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO v2ray_settings (
  id, enabled, start_on_phantom_launch, asset_dir, config_mode, config_format, public_host, listen, port, protocol, transport, security, ws_path, tls_cert_file, tls_key_file, sniffing_enabled, block_private_network, log_level, raw_config_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.ID, boolInt(defaults.Enabled), boolInt(defaults.StartOnPhantomLaunch), defaults.AssetDir, defaults.ConfigMode, defaults.ConfigFormat, defaults.PublicHost, defaults.Listen, defaults.Port, defaults.Protocol, defaults.Transport, defaults.Security, defaults.WSPath, defaults.TLSCertFile, defaults.TLSKeyFile, boolInt(defaults.SniffingEnabled), boolInt(defaults.BlockPrivateNetwork), defaults.LogLevel, defaults.RawConfigJSON, now, now)
	return err
}

func (s *Store) GetV2RaySettings(ctx context.Context) (V2RaySettings, error) {
	if err := s.EnsureV2RaySettings(ctx); err != nil {
		return V2RaySettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, enabled, start_on_phantom_launch, asset_dir, config_mode, config_format, public_host, listen, port, protocol, transport, security, ws_path, tls_cert_file, tls_key_file, sniffing_enabled, block_private_network, log_level, raw_config_json, created_at, updated_at FROM v2ray_settings WHERE id = 'default'`)
	settings, err := scanV2RaySettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultV2RaySettings(), nil
	}
	return NormalizeV2RaySettings(settings), err
}

func (s *Store) UpdateV2RaySettings(ctx context.Context, settings V2RaySettings) (V2RaySettings, error) {
	settings = NormalizeV2RaySettings(settings)
	now := now()
	if settings.CreatedAt == "" {
		settings.CreatedAt = now
	}
	settings.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO v2ray_settings (
  id, enabled, start_on_phantom_launch, asset_dir, config_mode, config_format, public_host, listen, port, protocol, transport, security, ws_path, tls_cert_file, tls_key_file, sniffing_enabled, block_private_network, log_level, raw_config_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  enabled = excluded.enabled,
  start_on_phantom_launch = excluded.start_on_phantom_launch,
  asset_dir = excluded.asset_dir,
  config_mode = excluded.config_mode,
  config_format = excluded.config_format,
  public_host = excluded.public_host,
  listen = excluded.listen,
  port = excluded.port,
  protocol = excluded.protocol,
  transport = excluded.transport,
  security = excluded.security,
  ws_path = excluded.ws_path,
  tls_cert_file = excluded.tls_cert_file,
  tls_key_file = excluded.tls_key_file,
  sniffing_enabled = excluded.sniffing_enabled,
  block_private_network = excluded.block_private_network,
  log_level = excluded.log_level,
  raw_config_json = excluded.raw_config_json,
  updated_at = excluded.updated_at`,
		settings.ID, boolInt(settings.Enabled), boolInt(settings.StartOnPhantomLaunch), settings.AssetDir, settings.ConfigMode, settings.ConfigFormat, settings.PublicHost, settings.Listen, settings.Port, settings.Protocol, settings.Transport, settings.Security, settings.WSPath, settings.TLSCertFile, settings.TLSKeyFile, boolInt(settings.SniffingEnabled), boolInt(settings.BlockPrivateNetwork), settings.LogLevel, settings.RawConfigJSON, settings.CreatedAt, settings.UpdatedAt)
	if err != nil {
		return V2RaySettings{}, err
	}
	return s.GetV2RaySettings(ctx)
}

func (s *Store) ListV2RayRemoteClients(ctx context.Context, includeRevoked bool) ([]V2RayRemoteClient, error) {
	query := `SELECT id, label, uuid, email, level, alter_id, enabled, created_at, updated_at, revoked_at FROM v2ray_remote_clients`
	if !includeRevoked {
		query += ` WHERE revoked_at = ''`
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []V2RayRemoteClient{}
	for rows.Next() {
		client, err := scanV2RayRemoteClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, client)
	}
	return out, rows.Err()
}

func (s *Store) GetV2RayRemoteClient(ctx context.Context, id string) (V2RayRemoteClient, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, label, uuid, email, level, alter_id, enabled, created_at, updated_at, revoked_at FROM v2ray_remote_clients WHERE id = ?`, id)
	client, err := scanV2RayRemoteClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return V2RayRemoteClient{}, ErrNotFound
	}
	return client, err
}

func (s *Store) CreateV2RayRemoteClient(ctx context.Context, client V2RayRemoteClient) (V2RayRemoteClient, error) {
	id, err := ids.New("v2rc")
	if err != nil {
		return V2RayRemoteClient{}, err
	}
	now := now()
	client.ID = id
	client.Label = strings.TrimSpace(client.Label)
	if client.Label == "" {
		client.Label = "远程设备"
	}
	client.Email = strings.TrimSpace(client.Email)
	if client.Email == "" {
		client.Email = client.ID + "@phantom-lancer"
	}
	client.CreatedAt = now
	client.UpdatedAt = now
	client.Enabled = true
	_, err = s.db.ExecContext(ctx, `INSERT INTO v2ray_remote_clients (id, label, uuid, email, level, alter_id, enabled, created_at, updated_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		client.ID, client.Label, client.UUID, client.Email, client.Level, client.AlterID, boolInt(client.Enabled), client.CreatedAt, client.UpdatedAt, client.RevokedAt)
	return client, err
}

func (s *Store) UpdateV2RayRemoteClient(ctx context.Context, client V2RayRemoteClient) (V2RayRemoteClient, error) {
	existing, err := s.GetV2RayRemoteClient(ctx, client.ID)
	if err != nil {
		return V2RayRemoteClient{}, err
	}
	client.Label = strings.TrimSpace(client.Label)
	if client.Label == "" {
		client.Label = existing.Label
	}
	client.Email = strings.TrimSpace(client.Email)
	if client.Email == "" {
		client.Email = existing.Email
	}
	_, err = s.db.ExecContext(ctx, `UPDATE v2ray_remote_clients SET label = ?, email = ?, level = ?, alter_id = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		client.Label, client.Email, client.Level, client.AlterID, boolInt(client.Enabled), now(), client.ID)
	if err != nil {
		return V2RayRemoteClient{}, err
	}
	return s.GetV2RayRemoteClient(ctx, client.ID)
}

func (s *Store) RotateV2RayRemoteClient(ctx context.Context, id, uuid string) (V2RayRemoteClient, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE v2ray_remote_clients SET uuid = ?, updated_at = ? WHERE id = ? AND revoked_at = ''`, uuid, now(), id)
	if err != nil {
		return V2RayRemoteClient{}, err
	}
	return s.GetV2RayRemoteClient(ctx, id)
}

func (s *Store) RevokeV2RayRemoteClient(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE v2ray_remote_clients SET enabled = 0, revoked_at = ?, updated_at = ? WHERE id = ?`, now(), now(), id)
	return err
}

func (s *Store) AddV2RayConfigVersion(ctx context.Context, version V2RayConfigVersion) (V2RayConfigVersion, error) {
	id, err := ids.New("v2cfg")
	if err != nil {
		return V2RayConfigVersion{}, err
	}
	version.ID = id
	version.CreatedAt = now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO v2ray_config_versions (id, settings_hash, config_hash, config_json_redacted, validation_status, validation_output, activated_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.SettingsHash, version.ConfigHash, version.ConfigJSONRedact, version.ValidationStatus, version.ValidationOutput, version.ActivatedAt, version.CreatedAt)
	return version, err
}

func (s *Store) MarkV2RayConfigActivated(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE v2ray_config_versions SET activated_at = ? WHERE id = ?`, now(), id)
	return err
}

func DefaultImageProviderSettings() ImageProviderSettings {
	return ImageProviderSettings{
		ID:                    "default",
		Provider:              "xai",
		DefaultModel:          "grok-imagine-image-quality",
		DefaultResponseFormat: "url",
		HistoryRetention:      500,
	}
}

func NormalizeImageProviderSettings(settings ImageProviderSettings) ImageProviderSettings {
	if settings.ID == "" {
		settings.ID = "default"
	}
	settings.Provider = strings.TrimSpace(strings.ToLower(settings.Provider))
	if settings.Provider == "" {
		settings.Provider = "xai"
	}
	settings.XAIAPIKey = strings.TrimSpace(settings.XAIAPIKey)
	settings.DefaultModel = strings.TrimSpace(settings.DefaultModel)
	if settings.DefaultModel == "" {
		settings.DefaultModel = "grok-imagine-image-quality"
	}
	settings.DefaultResponseFormat = strings.TrimSpace(settings.DefaultResponseFormat)
	if settings.DefaultResponseFormat == "" {
		settings.DefaultResponseFormat = "url"
	}
	settings.DefaultResolution = strings.TrimSpace(settings.DefaultResolution)
	settings.DefaultAspectRatio = strings.TrimSpace(settings.DefaultAspectRatio)
	if settings.HistoryRetention <= 0 {
		settings.HistoryRetention = 500
	}
	if settings.HistoryRetention > 2000 {
		settings.HistoryRetention = 2000
	}
	settings.HasAPIKey = settings.XAIAPIKey != ""
	settings.MaskedAPIKey = maskStoredSecret(settings.XAIAPIKey)
	return settings
}

func (s *Store) EnsureImageProviderSettings(ctx context.Context) error {
	defaults := DefaultImageProviderSettings()
	now := now()
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO image_provider_settings (
  id, provider, xai_api_key, default_model, default_response_format, default_resolution, default_aspect_ratio, history_retention, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.ID, defaults.Provider, defaults.XAIAPIKey, defaults.DefaultModel, defaults.DefaultResponseFormat, defaults.DefaultResolution, defaults.DefaultAspectRatio, defaults.HistoryRetention, now, now)
	return err
}

func (s *Store) GetImageProviderSettings(ctx context.Context) (ImageProviderSettings, error) {
	if err := s.EnsureImageProviderSettings(ctx); err != nil {
		return ImageProviderSettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, provider, xai_api_key, default_model, default_response_format, default_resolution, default_aspect_ratio, history_retention, created_at, updated_at
FROM image_provider_settings WHERE id = 'default'`)
	settings, err := scanImageProviderSettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultImageProviderSettings(), nil
	}
	return NormalizeImageProviderSettings(settings), err
}

func (s *Store) UpdateImageProviderSettings(ctx context.Context, settings ImageProviderSettings, updateAPIKey bool, clearAPIKey bool) (ImageProviderSettings, error) {
	existing, err := s.GetImageProviderSettings(ctx)
	if err != nil {
		return ImageProviderSettings{}, err
	}
	settings = NormalizeImageProviderSettings(settings)
	settings.ID = "default"
	settings.Provider = "xai"
	if clearAPIKey {
		settings.XAIAPIKey = ""
	} else if updateAPIKey {
		settings.XAIAPIKey = strings.TrimSpace(settings.XAIAPIKey)
	} else {
		settings.XAIAPIKey = existing.XAIAPIKey
	}
	now := now()
	if existing.CreatedAt != "" {
		settings.CreatedAt = existing.CreatedAt
	}
	if settings.CreatedAt == "" {
		settings.CreatedAt = now
	}
	settings.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
INSERT INTO image_provider_settings (
  id, provider, xai_api_key, default_model, default_response_format, default_resolution, default_aspect_ratio, history_retention, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  provider = excluded.provider,
  xai_api_key = excluded.xai_api_key,
  default_model = excluded.default_model,
  default_response_format = excluded.default_response_format,
  default_resolution = excluded.default_resolution,
  default_aspect_ratio = excluded.default_aspect_ratio,
  history_retention = excluded.history_retention,
  updated_at = excluded.updated_at`,
		settings.ID, settings.Provider, settings.XAIAPIKey, settings.DefaultModel, settings.DefaultResponseFormat, settings.DefaultResolution, settings.DefaultAspectRatio, settings.HistoryRetention, settings.CreatedAt, settings.UpdatedAt)
	if err != nil {
		return ImageProviderSettings{}, err
	}
	return s.GetImageProviderSettings(ctx)
}

func DefaultImageStorageSettings() ImageStorageSettings {
	return ImageStorageSettings{
		ID:               "default",
		Backend:          "local",
		S3ProviderLabel:  "custom-s3",
		S3Prefix:         "phantom-lancer/images",
		S3AccessMode:     "proxy",
		FallbackToLocal:  true,
		HasS3Credentials: false,
	}
}

func NormalizeImageStorageSettings(settings ImageStorageSettings) ImageStorageSettings {
	if settings.ID == "" {
		settings.ID = "default"
	}
	settings.Backend = strings.TrimSpace(strings.ToLower(settings.Backend))
	if settings.Backend == "" {
		settings.Backend = "local"
	}
	// "s3" is the legacy inline backend kept for backward compatible reads;
	// "object_storage" references a shared object storage profile.
	if settings.Backend != "local" && settings.Backend != "s3" && settings.Backend != "object_storage" {
		settings.Backend = "local"
	}
	settings.S3ProviderLabel = strings.TrimSpace(settings.S3ProviderLabel)
	if settings.S3ProviderLabel == "" {
		settings.S3ProviderLabel = "custom-s3"
	}
	settings.S3Bucket = strings.TrimSpace(settings.S3Bucket)
	settings.S3Region = strings.TrimSpace(settings.S3Region)
	settings.S3Endpoint = strings.TrimSpace(settings.S3Endpoint)
	settings.S3Prefix = strings.Trim(strings.TrimSpace(settings.S3Prefix), "/")
	if settings.S3Prefix == "" {
		settings.S3Prefix = "phantom-lancer/images"
	}
	settings.S3AccessKeyID = strings.TrimSpace(settings.S3AccessKeyID)
	settings.S3SecretAccessKey = strings.TrimSpace(settings.S3SecretAccessKey)
	settings.S3SessionToken = strings.TrimSpace(settings.S3SessionToken)
	settings.S3AccessMode = strings.TrimSpace(strings.ToLower(settings.S3AccessMode))
	if settings.S3AccessMode != "presigned" {
		settings.S3AccessMode = "proxy"
	}
	settings.HasS3Credentials = settings.S3AccessKeyID != "" && settings.S3SecretAccessKey != ""
	settings.MaskedAccessKeyID = maskStoredSecret(settings.S3AccessKeyID)
	settings.ObjectStorageProfileID = strings.TrimSpace(settings.ObjectStorageProfileID)
	return settings
}

func (s *Store) EnsureImageStorageSettings(ctx context.Context) error {
	defaults := DefaultImageStorageSettings()
	now := now()
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO image_storage_settings (
  id, backend, object_storage_profile_id, s3_provider_label, s3_bucket, s3_region, s3_endpoint, s3_prefix, s3_force_path_style, s3_access_key_id, s3_secret_access_key, s3_session_token, s3_access_mode, fallback_to_local, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.ID, defaults.Backend, defaults.ObjectStorageProfileID, defaults.S3ProviderLabel, defaults.S3Bucket, defaults.S3Region, defaults.S3Endpoint, defaults.S3Prefix, boolInt(defaults.S3ForcePathStyle), defaults.S3AccessKeyID, defaults.S3SecretAccessKey, defaults.S3SessionToken, defaults.S3AccessMode, boolInt(defaults.FallbackToLocal), now, now)
	return err
}

func (s *Store) GetImageStorageSettings(ctx context.Context) (ImageStorageSettings, error) {
	if err := s.EnsureImageStorageSettings(ctx); err != nil {
		return ImageStorageSettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, backend, object_storage_profile_id, s3_provider_label, s3_bucket, s3_region, s3_endpoint, s3_prefix, s3_force_path_style, s3_access_key_id, s3_secret_access_key, s3_session_token, s3_access_mode, fallback_to_local, created_at, updated_at
FROM image_storage_settings WHERE id = 'default'`)
	settings, err := scanImageStorageSettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultImageStorageSettings(), nil
	}
	return NormalizeImageStorageSettings(settings), err
}

func (s *Store) UpdateImageStorageSettings(ctx context.Context, settings ImageStorageSettings, updateSecret bool, clearSecret bool) (ImageStorageSettings, error) {
	existing, err := s.GetImageStorageSettings(ctx)
	if err != nil {
		return ImageStorageSettings{}, err
	}
	settings = NormalizeImageStorageSettings(settings)
	settings.ID = "default"
	if clearSecret {
		settings.S3AccessKeyID = ""
		settings.S3SecretAccessKey = ""
		settings.S3SessionToken = ""
	} else if !updateSecret {
		settings.S3AccessKeyID = existing.S3AccessKeyID
		settings.S3SecretAccessKey = existing.S3SecretAccessKey
		settings.S3SessionToken = existing.S3SessionToken
	}
	now := now()
	settings.CreatedAt = existing.CreatedAt
	if settings.CreatedAt == "" {
		settings.CreatedAt = now
	}
	settings.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
INSERT INTO image_storage_settings (
  id, backend, object_storage_profile_id, s3_provider_label, s3_bucket, s3_region, s3_endpoint, s3_prefix, s3_force_path_style, s3_access_key_id, s3_secret_access_key, s3_session_token, s3_access_mode, fallback_to_local, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  backend = excluded.backend,
  object_storage_profile_id = excluded.object_storage_profile_id,
  s3_provider_label = excluded.s3_provider_label,
  s3_bucket = excluded.s3_bucket,
  s3_region = excluded.s3_region,
  s3_endpoint = excluded.s3_endpoint,
  s3_prefix = excluded.s3_prefix,
  s3_force_path_style = excluded.s3_force_path_style,
  s3_access_key_id = excluded.s3_access_key_id,
  s3_secret_access_key = excluded.s3_secret_access_key,
  s3_session_token = excluded.s3_session_token,
  s3_access_mode = excluded.s3_access_mode,
  fallback_to_local = excluded.fallback_to_local,
  updated_at = excluded.updated_at`,
		settings.ID, settings.Backend, settings.ObjectStorageProfileID, settings.S3ProviderLabel, settings.S3Bucket, settings.S3Region, settings.S3Endpoint, settings.S3Prefix, boolInt(settings.S3ForcePathStyle), settings.S3AccessKeyID, settings.S3SecretAccessKey, settings.S3SessionToken, settings.S3AccessMode, boolInt(settings.FallbackToLocal), settings.CreatedAt, settings.UpdatedAt)
	if err != nil {
		return ImageStorageSettings{}, err
	}
	return s.GetImageStorageSettings(ctx)
}

// ---- object storage profiles ----

const objectStorageProfileColumns = `id, name, provider_label, bucket, region, endpoint, force_path_style, access_key_id, secret_access_key, session_token, status, last_tested_at, last_error, created_at, updated_at`

func NormalizeObjectStorageProfile(profile ObjectStorageProfile) ObjectStorageProfile {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.ProviderLabel = strings.TrimSpace(profile.ProviderLabel)
	if profile.ProviderLabel == "" {
		profile.ProviderLabel = "custom-s3"
	}
	profile.Bucket = strings.TrimSpace(profile.Bucket)
	profile.Region = strings.TrimSpace(profile.Region)
	profile.Endpoint = strings.TrimSpace(profile.Endpoint)
	profile.AccessKeyID = strings.TrimSpace(profile.AccessKeyID)
	profile.SecretAccessKey = strings.TrimSpace(profile.SecretAccessKey)
	profile.SessionToken = strings.TrimSpace(profile.SessionToken)
	profile.Status = strings.TrimSpace(strings.ToLower(profile.Status))
	switch profile.Status {
	case "ok", "error", "untested", "unconfigured":
	default:
		profile.Status = "untested"
	}
	profile.HasCredentials = profile.AccessKeyID != "" && profile.SecretAccessKey != ""
	profile.MaskedAccessKeyID = maskStoredSecret(profile.AccessKeyID)
	return profile
}

func (s *Store) ListObjectStorageProfiles(ctx context.Context) ([]ObjectStorageProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+objectStorageProfileColumns+` FROM object_storage_profiles ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ObjectStorageProfile{}
	for rows.Next() {
		profile, err := scanObjectStorageProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, profile)
	}
	return out, rows.Err()
}

func (s *Store) GetObjectStorageProfile(ctx context.Context, id string) (ObjectStorageProfile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ObjectStorageProfile{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+objectStorageProfileColumns+` FROM object_storage_profiles WHERE id = ?`, id)
	profile, err := scanObjectStorageProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectStorageProfile{}, ErrNotFound
	}
	return profile, err
}

func (s *Store) CreateObjectStorageProfile(ctx context.Context, profile ObjectStorageProfile) (ObjectStorageProfile, error) {
	if profile.ID == "" {
		id, err := ids.New("ossprofile")
		if err != nil {
			return ObjectStorageProfile{}, err
		}
		profile.ID = id
	}
	profile = NormalizeObjectStorageProfile(profile)
	if profile.Status == "" {
		profile.Status = "untested"
	}
	now := now()
	profile.CreatedAt = now
	profile.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO object_storage_profiles (`+objectStorageProfileColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		profile.ID, profile.Name, profile.ProviderLabel, profile.Bucket, profile.Region, profile.Endpoint, boolInt(profile.ForcePathStyle), profile.AccessKeyID, profile.SecretAccessKey, profile.SessionToken, profile.Status, profile.LastTestedAt, profile.LastError, profile.CreatedAt, profile.UpdatedAt)
	if err != nil {
		return ObjectStorageProfile{}, err
	}
	return s.GetObjectStorageProfile(ctx, profile.ID)
}

// UpdateObjectStorageProfile updates a profile's connection fields. Secrets are
// only replaced when updateSecret is true; clearSecret wipes them.
func (s *Store) UpdateObjectStorageProfile(ctx context.Context, profile ObjectStorageProfile, updateSecret bool, clearSecret bool) (ObjectStorageProfile, error) {
	existing, err := s.GetObjectStorageProfile(ctx, profile.ID)
	if err != nil {
		return ObjectStorageProfile{}, err
	}
	profile = NormalizeObjectStorageProfile(profile)
	profile.ID = existing.ID
	if clearSecret {
		profile.AccessKeyID = ""
		profile.SecretAccessKey = ""
		profile.SessionToken = ""
	} else if !updateSecret {
		profile.AccessKeyID = existing.AccessKeyID
		profile.SecretAccessKey = existing.SecretAccessKey
		profile.SessionToken = existing.SessionToken
	}
	profile.HasCredentials = profile.AccessKeyID != "" && profile.SecretAccessKey != ""
	profile.CreatedAt = existing.CreatedAt
	profile.UpdatedAt = now()
	// When any connection-relevant field changes, the previous test result no
	// longer reflects the new target, so reset to untested and clear the stale
	// last_tested_at/last_error to avoid misleading "ok" status.
	connectionChanged := profile.Bucket != existing.Bucket ||
		profile.Region != existing.Region ||
		profile.Endpoint != existing.Endpoint ||
		profile.ForcePathStyle != existing.ForcePathStyle ||
		profile.AccessKeyID != existing.AccessKeyID ||
		profile.SecretAccessKey != existing.SecretAccessKey ||
		profile.SessionToken != existing.SessionToken
	if connectionChanged {
		profile.Status = "untested"
		profile.LastTestedAt = ""
		profile.LastError = ""
	} else {
		profile.Status = existing.Status
		profile.LastTestedAt = existing.LastTestedAt
		profile.LastError = existing.LastError
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE object_storage_profiles SET
  name = ?, provider_label = ?, bucket = ?, region = ?, endpoint = ?, force_path_style = ?,
  access_key_id = ?, secret_access_key = ?, session_token = ?, status = ?, last_tested_at = ?, last_error = ?, updated_at = ?
WHERE id = ?`,
		profile.Name, profile.ProviderLabel, profile.Bucket, profile.Region, profile.Endpoint, boolInt(profile.ForcePathStyle),
		profile.AccessKeyID, profile.SecretAccessKey, profile.SessionToken, profile.Status, profile.LastTestedAt, profile.LastError, profile.UpdatedAt, profile.ID)
	if err != nil {
		return ObjectStorageProfile{}, err
	}
	return s.GetObjectStorageProfile(ctx, profile.ID)
}

// SetObjectStorageProfileTestResult records the outcome of a connection test.
func (s *Store) SetObjectStorageProfileTestResult(ctx context.Context, id string, ok bool, errorSummary string) error {
	status := "ok"
	if !ok {
		status = "error"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE object_storage_profiles SET status = ?, last_tested_at = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		status, now(), errorSummary, now(), strings.TrimSpace(id))
	return err
}

// DeleteObjectStorageProfile removes a profile. Callers must verify there are no
// referencing modules first (see ObjectStorageProfileReferencedBy).
func (s *Store) DeleteObjectStorageProfile(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM object_storage_profiles WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ObjectStorageProfileReferencedBy returns the list of module identifiers that
// currently reference the given profile, so deletion can be blocked safely.
func (s *Store) ObjectStorageProfileReferencedBy(ctx context.Context, id string) ([]string, error) {
	id = strings.TrimSpace(id)
	refs := []string{}
	if id == "" {
		return refs, nil
	}
	imageSettings, err := s.GetImageStorageSettings(ctx)
	if err != nil {
		return nil, err
	}
	if imageSettings.Backend == "object_storage" && imageSettings.ObjectStorageProfileID == id {
		refs = append(refs, "images")
	}
	dockerSettings, err := s.GetDockerRegistrySettings(ctx)
	if err != nil {
		return nil, err
	}
	if dockerSettings.StorageBackend == "object_storage" && dockerSettings.ObjectStorageProfileID == id {
		refs = append(refs, "docker_registry")
	}
	return refs, nil
}

func scanObjectStorageProfile(row workspaceScanner) (ObjectStorageProfile, error) {
	var profile ObjectStorageProfile
	var forcePath int
	err := row.Scan(&profile.ID, &profile.Name, &profile.ProviderLabel, &profile.Bucket, &profile.Region, &profile.Endpoint, &forcePath, &profile.AccessKeyID, &profile.SecretAccessKey, &profile.SessionToken, &profile.Status, &profile.LastTestedAt, &profile.LastError, &profile.CreatedAt, &profile.UpdatedAt)
	if err != nil {
		return ObjectStorageProfile{}, err
	}
	profile.ForcePathStyle = forcePath == 1
	return NormalizeObjectStorageProfile(profile), nil
}

const imageAssetColumns = `id, asset_type, status, private, provider, model, job_id, source_role, slot, prompt_preview, revised_prompt_preview, original_filename, original_source_redacted, mime_type, extension, size_bytes, width, height, checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region, s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason, last_error, created_at, updated_at`

func (s *Store) CreateImageAsset(ctx context.Context, asset ImageAsset) (ImageAsset, error) {
	if asset.ID == "" {
		id, err := ids.New("imgasset")
		if err != nil {
			return ImageAsset{}, err
		}
		asset.ID = id
	}
	asset = NormalizeImageAsset(asset)
	now := now()
	if asset.CreatedAt == "" {
		asset.CreatedAt = now
	}
	asset.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO image_assets (
  id, asset_type, status, private, provider, model, job_id, source_role, slot, prompt_preview, revised_prompt_preview, original_filename, original_source_redacted, mime_type, extension, size_bytes, width, height, checksum_sha256, local_name, storage_backend, object_storage_profile_id, s3_bucket, s3_region, s3_endpoint_label, s3_key, s3_etag, private_at, archived_at, deleted_at, deleted_reason, last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, asset.AssetType, asset.Status, boolInt(asset.Private), asset.Provider, asset.Model, asset.JobID, asset.SourceRole, asset.Slot, asset.PromptPreview, asset.RevisedPromptPreview, asset.OriginalFilename, asset.OriginalSourceRedacted, asset.MimeType, asset.Extension, asset.SizeBytes, asset.Width, asset.Height, asset.ChecksumSHA256, asset.LocalName, asset.StorageBackend, asset.ObjectStorageProfileID, asset.S3Bucket, asset.S3Region, asset.S3EndpointLabel, asset.S3Key, asset.S3ETag, asset.PrivateAt, asset.ArchivedAt, asset.DeletedAt, asset.DeletedReason, asset.LastError, asset.CreatedAt, asset.UpdatedAt)
	if err != nil {
		return ImageAsset{}, err
	}
	return s.GetImageAsset(ctx, asset.ID)
}

func NormalizeImageAsset(asset ImageAsset) ImageAsset {
	asset.AssetType = strings.TrimSpace(asset.AssetType)
	if asset.AssetType == "" {
		asset.AssetType = "generated"
	}
	asset.Status = strings.TrimSpace(asset.Status)
	if asset.Status == "" {
		asset.Status = "available"
	}
	asset.Provider = strings.TrimSpace(asset.Provider)
	asset.Model = strings.TrimSpace(asset.Model)
	asset.JobID = strings.TrimSpace(asset.JobID)
	asset.SourceRole = strings.TrimSpace(asset.SourceRole)
	asset.PromptPreview = previewText(asset.PromptPreview, 220)
	asset.RevisedPromptPreview = previewText(asset.RevisedPromptPreview, 220)
	asset.OriginalFilename = previewText(filepathBase(asset.OriginalFilename), 160)
	asset.OriginalSourceRedacted = previewText(asset.OriginalSourceRedacted, 240)
	asset.MimeType = strings.TrimSpace(asset.MimeType)
	asset.Extension = strings.TrimSpace(asset.Extension)
	asset.LocalName = strings.TrimSpace(asset.LocalName)
	asset.StorageBackend = strings.TrimSpace(strings.ToLower(asset.StorageBackend))
	if asset.StorageBackend == "" {
		asset.StorageBackend = "local"
	}
	asset.S3Bucket = strings.TrimSpace(asset.S3Bucket)
	asset.S3Region = strings.TrimSpace(asset.S3Region)
	asset.S3EndpointLabel = strings.TrimSpace(asset.S3EndpointLabel)
	asset.S3Key = strings.TrimSpace(asset.S3Key)
	asset.S3ETag = strings.Trim(strings.TrimSpace(asset.S3ETag), `"`)
	return asset
}

func (s *Store) GetImageAsset(ctx context.Context, id string) (ImageAsset, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+imageAssetColumns+` FROM image_assets WHERE id = ?`, id)
	asset, err := scanImageAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ImageAsset{}, ErrNotFound
	}
	if err == nil {
		s.hydrateRemoteImageAssetURL(ctx, &asset)
	}
	return asset, err
}

func (s *Store) GetImageAssetByLocalName(ctx context.Context, name string) (ImageAsset, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+imageAssetColumns+` FROM image_assets WHERE local_name = ? AND status != 'deleted' ORDER BY created_at DESC LIMIT 1`, name)
	asset, err := scanImageAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ImageAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *Store) GetPublicImageAssetByChecksum(ctx context.Context, checksum string) (ImageAsset, error) {
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return ImageAsset{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+imageAssetColumns+` FROM image_assets WHERE checksum_sha256 = ? AND status = 'available' AND private = 0 ORDER BY created_at ASC LIMIT 1`, checksum)
	asset, err := scanImageAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ImageAsset{}, ErrNotFound
	}
	if err == nil {
		s.hydrateRemoteImageAssetURL(ctx, &asset)
	}
	return asset, err
}

func (s *Store) ListImageAssets(ctx context.Context, limit int, assetType, storageBackend, status, q, privacy string) ([]ImageAsset, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	query := `SELECT ` + imageAssetColumns + ` FROM image_assets`
	args := []any{}
	clauses := []string{}
	if assetType = strings.TrimSpace(assetType); assetType != "" && assetType != "all" {
		clauses = append(clauses, "asset_type = ?")
		args = append(args, assetType)
	}
	if storageBackend = strings.TrimSpace(storageBackend); storageBackend != "" && storageBackend != "all" {
		clauses = append(clauses, "storage_backend = ?")
		args = append(args, storageBackend)
	}
	if status = strings.TrimSpace(status); status == "" {
		clauses = append(clauses, "status = 'available'")
	} else if status != "all" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	switch strings.TrimSpace(strings.ToLower(privacy)) {
	case "private":
		clauses = append(clauses, "private = 1")
	case "all":
	default:
		clauses = append(clauses, "private = 0")
	}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + q + "%"
		clauses = append(clauses, "(prompt_preview LIKE ? OR revised_prompt_preview LIKE ? OR model LIKE ? OR job_id LIKE ? OR id LIKE ? OR original_filename LIKE ?)")
		args = append(args, like, like, like, like, like, like)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageAsset{}
	for rows.Next() {
		asset, err := scanImageAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range out {
		s.hydrateRemoteImageAssetURL(ctx, &out[index])
	}
	return out, nil
}

func (s *Store) hydrateRemoteImageAssetURL(ctx context.Context, asset *ImageAsset) {
	if asset == nil || asset.StorageBackend != "remote" {
		return
	}
	var remoteURL string
	_ = s.db.QueryRowContext(ctx, `
SELECT remote_url FROM image_generation_outputs
WHERE asset_id = ? OR (asset_id = '' AND job_id = ? AND slot = ?)
ORDER BY created_at DESC LIMIT 1`, asset.ID, asset.JobID, asset.Slot).Scan(&remoteURL)
	if strings.TrimSpace(remoteURL) != "" {
		asset.URL = remoteURL
	}
}

func (s *Store) UpdateImageAsset(ctx context.Context, asset ImageAsset) (ImageAsset, error) {
	asset = NormalizeImageAsset(asset)
	asset.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `
UPDATE image_assets SET
  status = ?, private = ?, private_at = ?, mime_type = ?, extension = ?, size_bytes = ?, width = ?, height = ?, checksum_sha256 = ?, local_name = ?, storage_backend = ?, object_storage_profile_id = ?, s3_bucket = ?, s3_region = ?, s3_endpoint_label = ?, s3_key = ?, s3_etag = ?, archived_at = ?, deleted_at = ?, deleted_reason = ?, last_error = ?, updated_at = ?
WHERE id = ?`,
		asset.Status, boolInt(asset.Private), asset.PrivateAt, asset.MimeType, asset.Extension, asset.SizeBytes, asset.Width, asset.Height, asset.ChecksumSHA256, asset.LocalName, asset.StorageBackend, asset.ObjectStorageProfileID, asset.S3Bucket, asset.S3Region, asset.S3EndpointLabel, asset.S3Key, asset.S3ETag, asset.ArchivedAt, asset.DeletedAt, asset.DeletedReason, asset.LastError, asset.UpdatedAt, asset.ID)
	if err != nil {
		return ImageAsset{}, err
	}
	return s.GetImageAsset(ctx, asset.ID)
}

func (s *Store) SetImageAssetPrivate(ctx context.Context, id string, private bool) (ImageAsset, error) {
	privateAt := ""
	if private {
		privateAt = now()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE image_assets SET private = ?, private_at = ?, updated_at = ? WHERE id = ? AND status != 'deleted'`, boolInt(private), privateAt, now(), id)
	if err != nil {
		return ImageAsset{}, err
	}
	return s.GetImageAsset(ctx, id)
}

func (s *Store) DeleteImageAsset(ctx context.Context, id, reason string) (ImageAsset, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE image_assets SET status = 'deleted', deleted_at = ?, deleted_reason = ?, updated_at = ? WHERE id = ?`, now(), previewText(reason, 200), now(), id)
	if err != nil {
		return ImageAsset{}, err
	}
	return s.GetImageAsset(ctx, id)
}

func (s *Store) LinkImageSourceAsset(ctx context.Context, sourceID, assetID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE image_generation_sources SET asset_id = ? WHERE id = ?`, assetID, sourceID)
	return err
}

func (s *Store) BackfillImageAssets(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT o.id, o.job_id, o.slot, o.local_name, o.remote_url, o.mime_type, o.revised_prompt, o.storage, o.size_bytes, o.created_at, j.provider, j.model, j.prompt
FROM image_generation_outputs o
JOIN image_generation_jobs j ON j.id = o.job_id
WHERE o.asset_id = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type legacyOutput struct {
		OutputID      string
		JobID         string
		Slot          int
		LocalName     string
		RemoteURL     string
		MimeType      string
		RevisedPrompt string
		Storage       string
		SizeBytes     int64
		CreatedAt     string
		Provider      string
		Model         string
		Prompt        string
	}
	legacy := []legacyOutput{}
	for rows.Next() {
		var item legacyOutput
		if err := rows.Scan(&item.OutputID, &item.JobID, &item.Slot, &item.LocalName, &item.RemoteURL, &item.MimeType, &item.RevisedPrompt, &item.Storage, &item.SizeBytes, &item.CreatedAt, &item.Provider, &item.Model, &item.Prompt); err != nil {
			return err
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range legacy {
		asset, err := s.CreateImageAsset(ctx, ImageAsset{
			AssetType:              "generated",
			Status:                 "available",
			Provider:               item.Provider,
			Model:                  item.Model,
			JobID:                  item.JobID,
			SourceRole:             "output",
			Slot:                   item.Slot,
			PromptPreview:          item.Prompt,
			RevisedPromptPreview:   item.RevisedPrompt,
			OriginalSourceRedacted: item.RemoteURL,
			MimeType:               item.MimeType,
			SizeBytes:              item.SizeBytes,
			LocalName:              item.LocalName,
			StorageBackend:         storageBackendForLegacy(item.Storage, item.LocalName, item.RemoteURL),
			CreatedAt:              item.CreatedAt,
		})
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE image_generation_outputs SET asset_id = ? WHERE id = ?`, asset.ID, item.OutputID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateImageGenerationJob(ctx context.Context, job ImageGenerationJob, sources []ImageGenerationSource) (ImageGenerationJob, error) {
	id, err := ids.New("imgjob")
	if err != nil {
		return ImageGenerationJob{}, err
	}
	now := now()
	job.ID = id
	if job.Provider == "" {
		job.Provider = "xai"
	}
	if job.Status == "" {
		job.Status = "queued"
	}
	if job.ResponseFormat == "" {
		job.ResponseFormat = "url"
	}
	job.SourceCount = len(sources)
	job.CreatedAt = now
	if job.Status == "running" && job.StartedAt == "" {
		job.StartedAt = now
	}
	usage := "{}"
	if len(job.Usage) > 0 {
		data, _ := json.Marshal(job.Usage)
		usage = string(data)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImageGenerationJob{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO image_generation_jobs (
  id, provider, status, mode, mode_label, model, endpoint, prompt, aspect_ratio, resolution, response_format, image_count, source_count, usage_json, error_message, created_at, started_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Provider, job.Status, job.Mode, job.ModeLabel, job.Model, job.Endpoint, job.Prompt, job.AspectRatio, job.Resolution, job.ResponseFormat, job.ImageCount, job.SourceCount, usage, job.ErrorMessage, job.CreatedAt, job.StartedAt, job.CompletedAt)
	if err != nil {
		return ImageGenerationJob{}, err
	}
	job.Sources = make([]ImageGenerationSource, 0, len(sources))
	for _, source := range sources {
		sourceID, err := ids.New("imgsrc")
		if err != nil {
			return ImageGenerationJob{}, err
		}
		source.ID = sourceID
		source.JobID = job.ID
		source.CreatedAt = now
		_, err = tx.ExecContext(ctx, `
INSERT INTO image_generation_sources (id, job_id, asset_id, slot, source_type, source_label, mime_type, size_bytes, url_redacted, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			source.ID, source.JobID, source.AssetID, source.Slot, source.SourceType, source.SourceLabel, source.MimeType, source.SizeBytes, source.URLRedacted, source.CreatedAt)
		if err != nil {
			return ImageGenerationJob{}, err
		}
		job.Sources = append(job.Sources, source)
	}
	if err := tx.Commit(); err != nil {
		return ImageGenerationJob{}, err
	}
	return job, nil
}

func (s *Store) StartImageGenerationJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE image_generation_jobs SET status = 'running', started_at = ?, error_message = '' WHERE id = ? AND status IN ('queued', 'running')`, now(), id)
	return err
}

func (s *Store) CompleteImageGenerationJob(ctx context.Context, id, endpoint string, usage map[string]any, outputs []ImageGenerationOutput) (ImageGenerationJob, error) {
	now := now()
	usageJSON, _ := json.Marshal(usage)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImageGenerationJob{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE image_generation_jobs SET status = 'success', endpoint = ?, usage_json = ?, completed_at = ?, error_message = '' WHERE id = ?`, endpoint, string(usageJSON), now, id)
	if err != nil {
		return ImageGenerationJob{}, err
	}
	for _, output := range outputs {
		outputID, err := ids.New("imgout")
		if err != nil {
			return ImageGenerationJob{}, err
		}
		output.ID = outputID
		output.JobID = id
		output.CreatedAt = now
		_, err = tx.ExecContext(ctx, `
INSERT INTO image_generation_outputs (id, job_id, asset_id, slot, remote_url, local_name, mime_type, revised_prompt, storage, size_bytes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			output.ID, output.JobID, output.AssetID, output.Slot, output.RemoteURL, output.LocalName, output.MimeType, output.RevisedPrompt, output.Storage, output.SizeBytes, output.CreatedAt)
		if err != nil {
			return ImageGenerationJob{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ImageGenerationJob{}, err
	}
	return s.GetImageGenerationJob(ctx, id)
}

func (s *Store) FailImageGenerationJob(ctx context.Context, id, endpoint, message string) (ImageGenerationJob, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE image_generation_jobs SET status = 'failed', endpoint = ?, error_message = ?, completed_at = ? WHERE id = ?`, endpoint, message, now(), id)
	if err != nil {
		return ImageGenerationJob{}, err
	}
	return s.GetImageGenerationJob(ctx, id)
}

func (s *Store) InterruptStaleImageGenerationJobs(ctx context.Context, message string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM image_generation_jobs WHERE status IN ('queued', 'running') ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE image_generation_jobs SET status = 'interrupted', error_message = ?, completed_at = ? WHERE status IN ('queued', 'running')`, message, now())
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) GetImageGenerationJob(ctx context.Context, id string) (ImageGenerationJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider, status, mode, mode_label, model, endpoint, prompt, aspect_ratio, resolution, response_format, image_count, source_count, usage_json, error_message, created_at, started_at, completed_at FROM image_generation_jobs WHERE id = ?`, id)
	job, err := scanImageGenerationJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ImageGenerationJob{}, ErrNotFound
	}
	if err != nil {
		return ImageGenerationJob{}, err
	}
	if err := s.attachImageJobRelations(ctx, &job); err != nil {
		return ImageGenerationJob{}, err
	}
	return job, nil
}

func (s *Store) ListImageGenerationJobs(ctx context.Context, limit int, status, mode string) ([]ImageGenerationJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	query := `SELECT id, provider, status, mode, mode_label, model, endpoint, prompt, aspect_ratio, resolution, response_format, image_count, source_count, usage_json, error_message, created_at, started_at, completed_at FROM image_generation_jobs`
	args := []any{}
	clauses := []string{}
	if status = strings.TrimSpace(status); status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if mode = strings.TrimSpace(mode); mode != "" {
		clauses = append(clauses, "mode = ?")
		args = append(args, mode)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []ImageGenerationJob{}
	for rows.Next() {
		job, err := scanImageGenerationJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.attachImageJobRelations(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) CountImageGenerationJobs(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_generation_jobs`).Scan(&count)
	return count, err
}

func (s *Store) PruneImageGenerationJobs(ctx context.Context, retention int) error {
	if retention <= 0 {
		retention = 500
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id FROM image_generation_jobs
WHERE id NOT IN (SELECT id FROM image_generation_jobs ORDER BY created_at DESC LIMIT ?)
ORDER BY created_at ASC`, retention)
	if err != nil {
		return err
	}
	defer rows.Close()
	var jobIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		jobIDs = append(jobIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(jobIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range jobIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM image_generation_sources WHERE job_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM image_generation_outputs WHERE job_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM image_generation_jobs WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) OwnerExists(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owner_account`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) CreateOwner(ctx context.Context, username, passwordHash string) (Owner, error) {
	id, err := ids.New("owner")
	if err != nil {
		return Owner{}, err
	}
	now := now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO owner_account (id, username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, username, passwordHash, now, now)
	if err != nil {
		return Owner{}, err
	}
	return Owner{ID: id, Username: username, PasswordHash: passwordHash}, nil
}

func (s *Store) GetOwnerByUsername(ctx context.Context, username string) (Owner, error) {
	var owner Owner
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM owner_account WHERE username = ?`, username).Scan(&owner.ID, &owner.Username, &owner.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Owner{}, ErrNotFound
	}
	return owner, err
}

func (s *Store) GetOwnerByID(ctx context.Context, id string) (Owner, error) {
	var owner Owner
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM owner_account WHERE id = ?`, id).Scan(&owner.ID, &owner.Username, &owner.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Owner{}, ErrNotFound
	}
	return owner, err
}

func (s *Store) CreateSession(ctx context.Context, ownerID, tokenHash, csrfHash string, trusted bool, expiresAt time.Time) (Session, error) {
	id, err := ids.New("sess")
	if err != nil {
		return Session{}, err
	}
	now := now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO web_sessions (id, token_hash, owner_id, csrf_token_hash, trusted, expires_at, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, tokenHash, ownerID, csrfHash, boolInt(trusted), formatTime(expiresAt), now, now)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, OwnerID: ownerID, TokenHash: tokenHash, CSRFTokenHash: csrfHash, Trusted: trusted, ExpiresAt: expiresAt}, nil
}

func (s *Store) GetSessionByHash(ctx context.Context, tokenHash string) (Session, error) {
	var session Session
	var expiresAt string
	var revoked sql.NullString
	var trusted int
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_id, token_hash, csrf_token_hash, trusted, expires_at, revoked_at FROM web_sessions WHERE token_hash = ?`, tokenHash).Scan(&session.ID, &session.OwnerID, &session.TokenHash, &session.CSRFTokenHash, &trusted, &expiresAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt = parsed
	session.Trusted = trusted == 1
	if revoked.Valid {
		if t, err := time.Parse(time.RFC3339Nano, revoked.String); err == nil {
			session.RevokedAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	return session, nil
}

func (s *Store) TouchSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE web_sessions SET last_seen_at = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) AddAudit(ctx context.Context, event AuditEvent) (AuditEvent, error) {
	id, err := ids.New("aud")
	if err != nil {
		return AuditEvent{}, err
	}
	event.ID = id
	event.CreatedAt = now()
	payload, _ := json.Marshal(event.Payload)
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_events (id, event_type, workspace_id, risk_level, summary, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, event.ID, event.EventType, event.WorkspaceID, event.RiskLevel, event.Summary, string(payload), event.CreatedAt)
	return event, err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_type, workspace_id, risk_level, summary, payload_json, created_at FROM audit_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		var payload string
		if err := rows.Scan(&event.ID, &event.EventType, &event.WorkspaceID, &event.RiskLevel, &event.Summary, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &event.Payload)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) AppendEvent(ctx context.Context, scope, scopeID, eventType string, payload map[string]any) (events.Event, error) {
	id, err := ids.New("evt")
	if err != nil {
		return events.Event{}, err
	}
	var seq int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE scope = ? AND scope_id = ?`, scope, scopeID).Scan(&seq); err != nil {
		return events.Event{}, err
	}
	createdAt := now()
	payloadJSON, _ := json.Marshal(payload)
	_, err = s.db.ExecContext(ctx, `INSERT INTO events (id, scope, scope_id, sequence, event_type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, scope, scopeID, seq, eventType, string(payloadJSON), createdAt)
	if err != nil {
		return events.Event{}, err
	}
	return events.Event{ID: id, Scope: scope, ScopeID: scopeID, Sequence: seq, Type: eventType, Payload: payload, CreatedAt: createdAt}, nil
}

func (s *Store) ListEvents(ctx context.Context, scope, scopeID string, after int64, limit int) ([]events.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, scope, scope_id, sequence, event_type, payload_json, created_at FROM events WHERE scope = ? AND scope_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, scope, scopeID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []events.Event{}
	for rows.Next() {
		var event events.Event
		var payload string
		if err := rows.Scan(&event.ID, &event.Scope, &event.ScopeID, &event.Sequence, &event.Type, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &event.Payload)
		out = append(out, event)
	}
	return out, rows.Err()
}

// PurgeLegacyCodexData is intentionally a no-op. The rebuilt Codex CLI client
// must not delete retired codex_* tables automatically; callers should use
// CodexCliLegacyTablesDetected and surface a diagnostic instead.
func (s *Store) PurgeLegacyCodexData(ctx context.Context) error {
	return nil
}

type workspaceScanner interface {
	Scan(dest ...any) error
}

func scanSystemUpdateCheck(row workspaceScanner) (SystemUpdateCheck, error) {
	var check SystemUpdateCheck
	var updateAvailable, comparable, canApply, checksumAvailable, platformSupported int
	err := row.Scan(
		&check.ID,
		&check.CurrentVersion,
		&check.LatestVersion,
		&updateAvailable,
		&comparable,
		&canApply,
		&check.Reason,
		&check.ReleaseID,
		&check.ReleaseURL,
		&check.PublishedAt,
		&check.AssetName,
		&check.AssetURL,
		&check.AssetSizeBytes,
		&check.ChecksumAssetURL,
		&checksumAvailable,
		&platformSupported,
		&check.ETag,
		&check.ErrorMessage,
		&check.CheckedAt,
	)
	if err != nil {
		return SystemUpdateCheck{}, err
	}
	check.UpdateAvailable = updateAvailable == 1
	check.Comparable = comparable == 1
	check.CanApply = canApply == 1
	check.ChecksumAvailable = checksumAvailable == 1
	check.PlatformSupported = platformSupported == 1
	return check, nil
}

func scanSystemUpdateJob(row workspaceScanner) (SystemUpdateJob, error) {
	var job SystemUpdateJob
	err := row.Scan(
		&job.ID,
		&job.CurrentVersion,
		&job.TargetVersion,
		&job.ReleaseID,
		&job.AssetName,
		&job.Status,
		&job.Phase,
		&job.BytesDownloaded,
		&job.TotalBytes,
		&job.ChecksumSHA256,
		&job.InstallBinaryPath,
		&job.BackupBinaryPath,
		&job.ErrorMessage,
		&job.CreatedAt,
		&job.StartedAt,
		&job.CompletedAt,
	)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	return job, nil
}

func scanCodexGatewaySettings(row workspaceScanner) (CodexGatewaySettings, error) {
	var settings CodexGatewaySettings
	var enabled int
	err := row.Scan(&settings.ID, &enabled, &settings.BaseURL, &settings.OAuthAuthURL, &settings.OAuthTokenURL, &settings.OAuthClientID, &settings.OAuthRedirectURI, &settings.RequestTimeoutSeconds, &settings.RefreshMarginSeconds, &settings.DefaultInstructions, &settings.InstallationID, &settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return CodexGatewaySettings{}, err
	}
	settings.Enabled = enabled == 1
	return settings, nil
}

func scanCodexGatewayAPIKey(row workspaceScanner) (CodexGatewayAPIKey, error) {
	var key CodexGatewayAPIKey
	err := row.Scan(&key.ID, &key.Name, &key.Status, &key.LastUsedAt, &key.CreatedAt, &key.UpdatedAt)
	if err != nil {
		return CodexGatewayAPIKey{}, err
	}
	return key, nil
}

func scanCodexGatewayAccount(row workspaceScanner) (CodexGatewayAccount, error) {
	var account CodexGatewayAccount
	var hasAccess, hasRefresh int
	err := row.Scan(&account.ID, &account.Label, &account.Status, &account.ExpiresAt, &account.Plan, &account.LastUsedAt, &account.LastCheckedAt, &account.LastError, &hasAccess, &hasRefresh, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	account.HasAccessToken = hasAccess == 1
	account.HasRefreshToken = hasRefresh == 1
	return account, nil
}

func scanCodexGatewayModel(row workspaceScanner) (CodexGatewayModel, error) {
	var model CodexGatewayModel
	err := row.Scan(&model.ID, &model.DisplayName, &model.OwnedBy, &model.Source, &model.LastSeenAt, &model.UpdatedAt)
	if err != nil {
		return CodexGatewayModel{}, err
	}
	model.Object = "model"
	return model, nil
}

func scanCodexGatewayRequestLog(row workspaceScanner) (CodexGatewayRequestLog, error) {
	var log CodexGatewayRequestLog
	var streamed int
	err := row.Scan(&log.ID, &log.RequestID, &log.APIKind, &log.Model, &log.AccountID, &log.SourceIP, &log.StatusCode, &log.ErrorCode, &log.ErrorSource, &log.ErrorMessage, &log.LatencyMS, &streamed, &log.InputTokens, &log.OutputTokens, &log.CreatedAt)
	if err != nil {
		return CodexGatewayRequestLog{}, err
	}
	log.Streamed = streamed == 1
	return log, nil
}

func scanV2RaySettings(row workspaceScanner) (V2RaySettings, error) {
	var settings V2RaySettings
	var enabled, startOnLaunch, sniffing, blockPrivate int
	err := row.Scan(&settings.ID, &enabled, &startOnLaunch, &settings.AssetDir, &settings.ConfigMode, &settings.ConfigFormat, &settings.PublicHost, &settings.Listen, &settings.Port, &settings.Protocol, &settings.Transport, &settings.Security, &settings.WSPath, &settings.TLSCertFile, &settings.TLSKeyFile, &sniffing, &blockPrivate, &settings.LogLevel, &settings.RawConfigJSON, &settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return V2RaySettings{}, err
	}
	settings.Enabled = enabled == 1
	settings.StartOnPhantomLaunch = startOnLaunch == 1
	settings.SniffingEnabled = sniffing == 1
	settings.BlockPrivateNetwork = blockPrivate == 1
	return settings, nil
}

func scanV2RayRemoteClient(row workspaceScanner) (V2RayRemoteClient, error) {
	var client V2RayRemoteClient
	var enabled int
	err := row.Scan(&client.ID, &client.Label, &client.UUID, &client.Email, &client.Level, &client.AlterID, &enabled, &client.CreatedAt, &client.UpdatedAt, &client.RevokedAt)
	if err != nil {
		return V2RayRemoteClient{}, err
	}
	client.Enabled = enabled == 1
	return client, nil
}

func scanImageProviderSettings(row workspaceScanner) (ImageProviderSettings, error) {
	var settings ImageProviderSettings
	err := row.Scan(&settings.ID, &settings.Provider, &settings.XAIAPIKey, &settings.DefaultModel, &settings.DefaultResponseFormat, &settings.DefaultResolution, &settings.DefaultAspectRatio, &settings.HistoryRetention, &settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return ImageProviderSettings{}, err
	}
	return NormalizeImageProviderSettings(settings), nil
}

func scanImageStorageSettings(row workspaceScanner) (ImageStorageSettings, error) {
	var settings ImageStorageSettings
	var forcePath, fallback int
	err := row.Scan(&settings.ID, &settings.Backend, &settings.ObjectStorageProfileID, &settings.S3ProviderLabel, &settings.S3Bucket, &settings.S3Region, &settings.S3Endpoint, &settings.S3Prefix, &forcePath, &settings.S3AccessKeyID, &settings.S3SecretAccessKey, &settings.S3SessionToken, &settings.S3AccessMode, &fallback, &settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return ImageStorageSettings{}, err
	}
	settings.S3ForcePathStyle = forcePath == 1
	settings.FallbackToLocal = fallback == 1
	return NormalizeImageStorageSettings(settings), nil
}

func scanImageAsset(row workspaceScanner) (ImageAsset, error) {
	var asset ImageAsset
	var private int
	err := row.Scan(&asset.ID, &asset.AssetType, &asset.Status, &private, &asset.Provider, &asset.Model, &asset.JobID, &asset.SourceRole, &asset.Slot, &asset.PromptPreview, &asset.RevisedPromptPreview, &asset.OriginalFilename, &asset.OriginalSourceRedacted, &asset.MimeType, &asset.Extension, &asset.SizeBytes, &asset.Width, &asset.Height, &asset.ChecksumSHA256, &asset.LocalName, &asset.StorageBackend, &asset.ObjectStorageProfileID, &asset.S3Bucket, &asset.S3Region, &asset.S3EndpointLabel, &asset.S3Key, &asset.S3ETag, &asset.PrivateAt, &asset.ArchivedAt, &asset.DeletedAt, &asset.DeletedReason, &asset.LastError, &asset.CreatedAt, &asset.UpdatedAt)
	if err != nil {
		return ImageAsset{}, err
	}
	asset.Private = private == 1
	if asset.Status == "" {
		asset.Status = "available"
	}
	if asset.StorageBackend == "" {
		asset.StorageBackend = "local"
	}
	asset.URL = "/api/images/library/assets/" + asset.ID + "/content"
	asset.DownloadURL = "/api/images/library/assets/" + asset.ID + "/download"
	return asset, nil
}

func scanImageGenerationJob(row workspaceScanner) (ImageGenerationJob, error) {
	var job ImageGenerationJob
	var usage string
	err := row.Scan(&job.ID, &job.Provider, &job.Status, &job.Mode, &job.ModeLabel, &job.Model, &job.Endpoint, &job.Prompt, &job.AspectRatio, &job.Resolution, &job.ResponseFormat, &job.ImageCount, &job.SourceCount, &usage, &job.ErrorMessage, &job.CreatedAt, &job.StartedAt, &job.CompletedAt)
	if err != nil {
		return ImageGenerationJob{}, err
	}
	_ = json.Unmarshal([]byte(usage), &job.Usage)
	if job.Usage == nil {
		job.Usage = map[string]any{}
	}
	return job, nil
}

func scanImageGenerationSource(row workspaceScanner) (ImageGenerationSource, error) {
	var source ImageGenerationSource
	err := row.Scan(&source.ID, &source.JobID, &source.AssetID, &source.Slot, &source.SourceType, &source.SourceLabel, &source.MimeType, &source.SizeBytes, &source.URLRedacted, &source.CreatedAt)
	if err != nil {
		return ImageGenerationSource{}, err
	}
	return source, nil
}

func scanImageGenerationOutput(row workspaceScanner) (ImageGenerationOutput, error) {
	var output ImageGenerationOutput
	err := row.Scan(&output.ID, &output.JobID, &output.AssetID, &output.Slot, &output.RemoteURL, &output.LocalName, &output.MimeType, &output.RevisedPrompt, &output.Storage, &output.SizeBytes, &output.CreatedAt)
	if err != nil {
		return ImageGenerationOutput{}, err
	}
	if output.Storage == "remote" && output.RemoteURL != "" {
		output.URL = output.RemoteURL
	} else if output.AssetID != "" {
		output.URL = "/api/images/library/assets/" + output.AssetID + "/content"
	} else if output.LocalName != "" {
		output.URL = "/api/images/assets/" + output.LocalName
	} else if output.RemoteURL != "" {
		output.URL = output.RemoteURL
	}
	return output, nil
}

func (s *Store) attachImageJobRelations(ctx context.Context, job *ImageGenerationJob) error {
	sourceRows, err := s.db.QueryContext(ctx, `SELECT id, job_id, asset_id, slot, source_type, source_label, mime_type, size_bytes, url_redacted, created_at FROM image_generation_sources WHERE job_id = ? ORDER BY slot ASC`, job.ID)
	if err != nil {
		return err
	}
	defer sourceRows.Close()
	job.Sources = []ImageGenerationSource{}
	for sourceRows.Next() {
		source, err := scanImageGenerationSource(sourceRows)
		if err != nil {
			return err
		}
		job.Sources = append(job.Sources, source)
	}
	if err := sourceRows.Err(); err != nil {
		return err
	}

	outputRows, err := s.db.QueryContext(ctx, `SELECT id, job_id, asset_id, slot, remote_url, local_name, mime_type, revised_prompt, storage, size_bytes, created_at FROM image_generation_outputs WHERE job_id = ? ORDER BY slot ASC`, job.ID)
	if err != nil {
		return err
	}
	defer outputRows.Close()
	job.Outputs = []ImageGenerationOutput{}
	for outputRows.Next() {
		output, err := scanImageGenerationOutput(outputRows)
		if err != nil {
			return err
		}
		job.Outputs = append(job.Outputs, output)
	}
	return outputRows.Err()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func maskStoredSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "********"
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}

func previewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func filepathBase(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, "/"); index >= 0 {
		return value[index+1:]
	}
	return value
}

func storageBackendForLegacy(storageValue, localName, remoteURL string) string {
	storageValue = strings.TrimSpace(strings.ToLower(storageValue))
	if storageValue == "s3" || storageValue == "local" || storageValue == "remote" {
		return storageValue
	}
	if localName != "" {
		return "local"
	}
	if remoteURL != "" {
		return "remote"
	}
	return "local"
}

func now() string {
	return formatTime(time.Now().UTC())
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
