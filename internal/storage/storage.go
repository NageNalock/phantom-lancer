package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/keywrap"
	"phantom-lancer/internal/safelog"

	sqlite3 "github.com/mattn/go-sqlite3"
)

var ErrNotFound = errors.New("not found")

// ErrCorruptSecrets is returned when a wrapped-secret column contains a
// value that looks like ciphertext (not a raw legacy token) but fails to
// decrypt under any configured master key. Callers should surface this to
// the operator as a key-mismatch or data-corruption incident, NOT as a
// transient upstream error.
var ErrCorruptSecrets = errors.New("wrapped secret cannot be decrypted; master key may have been rotated without migrating tokens")

var codexGatewayRequestLogRetention = 5000

// codexGatewayTokenKeeperInfo is the HKDF context label that binds the
// symmetric keeper used for upstream account tokens to a specific domain.
// Changing this value will render all previously stored tokens unreadable.
const codexGatewayTokenKeeperInfo = "codex-gateway-account-tokens-v1"

// mailKeeperInfo is the HKDF context label used for the Mail module's
// wrapped secrets: DNS provider tokens, ACME account keys, webhook
// secrets, and so on.  It is deliberately distinct from the gateway
// keeper so a key-rotation event for one module does not cascade into
// the other.
const mailKeeperInfo = "phantom-mail-v1"

// dockerRegistryCredentialKeeperInfo binds retrievable Docker registry
// credential secrets to a separate key domain from Gateway upstream tokens.
const dockerRegistryCredentialKeeperInfo = "docker-registry-credential-secrets-v1"

// masterKeySettingKey is the settings key under which the 32-byte master
// encryption key (base64-encoded) is stored.
const masterKeySettingKey = "system.crypto_master_key_v1"

type Store struct {
	db     *sql.DB
	dbPath string
	// log is the structured logger used for non-call-site notifications
	// (startup, key-source change, best-effort background migrations).
	// It is injected by storage.Open so storage output follows the same
	// handler as the rest of the service (JSON, rotation, level gating).
	// If nil, calls are no-ops.
	log *slog.Logger // gwTokenKeeper is the primary keeper derived from the master key
	// actually in effect (env key, if set; otherwise DB fallback key).
	// wrapGWToken always uses this keeper.
	gwTokenKeeper *keywrap.Keeper
	// gwTokenFallbackKeeper is non-nil only when env and DB both provided
	// a valid master key AND they differ. It lets unwrapGWToken recover
	// tokens wrapped under the *previous* key (e.g. an operator just
	// rotated from DB-stored → env-provided). Recovered tokens are
	// transparently re-wrapped with gwTokenKeeper on next write.
	gwTokenFallbackKeeper *keywrap.Keeper
	// dockerRegistrySecretKeeper wraps registry credential secrets used by
	// server-side Docker Engine pull operations against the embedded registry.
	dockerRegistrySecretKeeper *keywrap.Keeper
	// dockerRegistrySecretFallbackKeeper mirrors gwTokenFallbackKeeper for
	// registry secrets when operators rotate from a DB key to PHANTOM_MASTER_KEY.
	dockerRegistrySecretFallbackKeeper *keywrap.Keeper
	// gwTokenMasterSource records which key source is driving the
	// primary keeper. Only used for structured startup logging and
	// future key-rotation diagnostics.
	gwTokenMasterSource string

	// mailKeeper is the symmetric keeper used by the Mail module for
	// DNS provider credentials, ACME account keys, webhook HMAC
	// secrets, import-mode SMTP/IMAP passwords, etc.  It is derived
	// from the same master key as gwTokenKeeper but with a distinct
	// HKDF info label (see mailKeeperInfo).
	mailKeeper *keywrap.Keeper
	// mailFallbackKeeper mirrors gwTokenFallbackKeeper for mail secrets.
	// Non-nil only when env+DB keys differ.
	mailFallbackKeeper *keywrap.Keeper

	// dbStatsCache holds the last computed database size / per-table stats.
	// It is refreshed periodically by StartStatsCollector.
	dbStatsCache  DatabaseStats
	dbStatsAt      time.Time
	dbStatsMu      sync.RWMutex
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
	AllowedRoots      []string `json:"allowedRoots"`
	CookieSecure      bool     `json:"cookieSecure"`
	Addr              string   `json:"addr"`
	TLSEnabled        bool     `json:"tlsEnabled"`
	TLSCertFile       string   `json:"tlsCertFile"`
	TLSKeyFile        string   `json:"tlsKeyFile"`
	TLSOwnerUIDCheck  bool     `json:"tlsOwnerUidCheck"`
	HSTSEnabled       bool     `json:"hstsEnabled"`
	HSTSMaxAgeSeconds int      `json:"hstsMaxAgeSeconds"`
	UpdatedAt         string   `json:"updatedAt,omitempty"`
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
	ID                                string `json:"id"`
	Enabled                           bool   `json:"enabled"`
	BaseURL                           string `json:"baseUrl"`
	OAuthAuthURL                      string `json:"oauthAuthUrl"`
	OAuthTokenURL                     string `json:"oauthTokenUrl"`
	OAuthClientID                     string `json:"oauthClientId"`
	OAuthRedirectURI                  string `json:"oauthRedirectUri"`
	RequestTimeoutSeconds             int    `json:"requestTimeoutSeconds"`
	RefreshMarginSeconds              int    `json:"refreshMarginSeconds"`
	AccountHealthCheckIntervalSeconds int    `json:"accountHealthCheckIntervalSeconds"`
	DefaultInstructions               string `json:"defaultInstructions"`
	InstallationID                    string `json:"installationId"`
	CreatedAt                         string `json:"createdAt"`
	UpdatedAt                         string `json:"updatedAt"`
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

type ImagePrompt struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Prompt      string   `json:"prompt"`
	Mode        string   `json:"mode"`
	Model       string   `json:"model,omitempty"`
	AspectRatio string   `json:"aspectRatio,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
	ImageCount  int      `json:"imageCount"`
	Tags        []string `json:"tags,omitempty"`
	Status      string   `json:"status"`
	UseCount    int      `json:"useCount"`
	LastUsedAt  string   `json:"lastUsedAt,omitempty"`
	DeletedAt   string   `json:"deletedAt,omitempty"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
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

func Open(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db, dbPath: path, log: log}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.ensureMasterKey(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: master key: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// ensureMasterKey loads or generates the 32-byte master encryption key
// used for wrapped secrets. It is called during Open and initializes
// the lazy keepers used by the Store.
//
// Threat model (behavioural boundary, not a promise of at-rest security):
//
//   - Priority: PHANTOM_MASTER_KEY environment variable, base64-raw-URL
//     encoded (≥32 bytes decoded; the keywrap package accepts >=
//     keywrap.MinMasterKeyBytes so operators may supply larger keys
//     if required by policy). Useful for deployments that can inject
//     secrets and want key↔ciphertext separation even if the DB is
//     copied. The env value is NEVER written back to the settings
//     table. When this env is set and no DB key yet exists, the
//     service does NOT generate a DB key either — the pure-env
//     branch stays reachable so key material truly lives only in the
//     supervisor's environment.
//
//   - Fallback: a 32-byte random key stored in `settings.system.crypto_master_key_v1`.
//     This protects against ACCIDENTAL plaintext exposure in table scans
//     and SQL dumps (e.g. SELECT token FROM ... leaks ciphertext not
//     plaintext), but it does NOT defend against someone who copies the
//     entire SQLite database — key and ciphertext travel together.
//
//   - Not provided: OS key storage (Keychain / DPAPI / KMS). If the threat
//     model includes DB exfiltration, deploy via PHANTOM_MASTER_KEY env
//     and restrict that env var to the Phantom Lancer process only.
func (s *Store) ensureMasterKey(ctx context.Context) error {
	// ---- 1. Read candidate keys: env (priority) and DB (fallback). ----
	var envMaster, dbMaster []byte
	var envSource, dbSource string

	if env := strings.TrimSpace(os.Getenv("PHANTOM_MASTER_KEY")); env != "" {
		decoded, derr := base64.RawURLEncoding.DecodeString(env)
		if derr != nil {
			return fmt.Errorf("decode PHANTOM_MASTER_KEY: %w", derr)
		}
		if len(decoded) < keywrap.MinMasterKeyBytes {
			return fmt.Errorf("PHANTOM_MASTER_KEY must decode to >=%d bytes (got %d)", keywrap.MinMasterKeyBytes, len(decoded))
		}
		envMaster = decoded
		envSource = "env:PHANTOM_MASTER_KEY"
	}

	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, masterKeySettingKey).Scan(&encoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if encoded == "" && envMaster == nil {
		// Only auto-generate a DB-stored master key when the operator
		// has NOT supplied PHANTOM_MASTER_KEY via env. In a pure-env
		// deployment the operator owns the key lifecycle, and we must
		// avoid planting a second key alongside it that would never
		// be used but would confuse an operator inspecting settings.
		generated, gerr := keywrap.GenerateMasterKey()
		if gerr != nil {
			return fmt.Errorf("generate master key: %w", gerr)
		}
		if _, gerr := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
			masterKeySettingKey, generated, now()); gerr != nil {
			// Concurrent process may have inserted; read it back.
			if rerr := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, masterKeySettingKey).Scan(&encoded); rerr != nil {
				return fmt.Errorf("insert master key: %w (read fallback: %v)", gerr, rerr)
			}
		} else {
			encoded = generated
		}
	}
	if encoded != "" {
		decoded, derr := base64.RawURLEncoding.DecodeString(encoded)
		if derr != nil {
			return fmt.Errorf("decode master key: %w", derr)
		}
		if len(decoded) < keywrap.MinMasterKeyBytes {
			return fmt.Errorf("DB-stored master key must decode to >=%d bytes (got %d)", keywrap.MinMasterKeyBytes, len(decoded))
		}
		dbMaster = decoded
		dbSource = "db:settings." + masterKeySettingKey
	}

	// ---- 2. Choose primary keeper. ----
	var primaryMaster []byte
	var primarySource string
	var fallbackMaster []byte
	var fallbackSource string

	switch {
	case envMaster != nil && dbMaster != nil:
		if bytes.Equal(envMaster, dbMaster) {
			// Operator explicitly aligned env with DB; no migration needed.
			primaryMaster, primarySource = envMaster, envSource
		} else {
			// Env and DB differ. Env wins (operational preference), DB
			// becomes the transparent fallback so tokens wrapped under
			// the old DB key are still readable and get re-wrapped on
			// next update.
			primaryMaster, primarySource = envMaster, envSource
			fallbackMaster, fallbackSource = dbMaster, dbSource
		}
	case envMaster != nil:
		// Pure env deployment (no DB key present, or key not yet set).
		primaryMaster, primarySource = envMaster, envSource
	default:
		// No env; DB-only fallback.
		primaryMaster, primarySource = dbMaster, dbSource
	}

	primary, kerr := keywrap.NewKeeper(primaryMaster, codexGatewayTokenKeeperInfo)
	if kerr != nil {
		return kerr
	}
	s.gwTokenKeeper = primary
	registryPrimary, kerr := keywrap.NewKeeper(primaryMaster, dockerRegistryCredentialKeeperInfo)
	if kerr != nil {
		return kerr
	}
	s.dockerRegistrySecretKeeper = registryPrimary
	s.gwTokenMasterSource = primarySource

	if fallbackMaster != nil {
		fb, kerr := keywrap.NewKeeper(fallbackMaster, codexGatewayTokenKeeperInfo)
		if kerr != nil {
			return kerr
		}
		s.gwTokenFallbackKeeper = fb
		registryFB, kerr := keywrap.NewKeeper(fallbackMaster, dockerRegistryCredentialKeeperInfo)
		if kerr != nil {
			return kerr
		}
		s.dockerRegistrySecretFallbackKeeper = registryFB
	}

	// ---- 2b. Derive Mail module keepers. ----
	// We use a *different* HKDF info label than the gateway keeper so
	// rotating one domain cannot be cross-probed against ciphertext from
	// the other.  The fallback branch mirrors the gateway semantics.
	mailPrimary, kerr := keywrap.NewKeeper(primaryMaster, mailKeeperInfo)
	if kerr != nil {
		return kerr
	}
	s.mailKeeper = mailPrimary
	if fallbackMaster != nil {
		mailFb, kerr := keywrap.NewKeeper(fallbackMaster, mailKeeperInfo)
		if kerr != nil {
			return kerr
		}
		s.mailFallbackKeeper = mailFb
	}

	// ---- 3. Structured startup notification using the injected logger. ----
	if s.log != nil {
		attrs := []any{"source", primarySource}
		if fallbackSource != "" {
			attrs = append(attrs, "fallback_source", fallbackSource,
				"note", "legacy tokens wrapped by fallback key are transparently re-wrapped on next write")
		} else if primarySource == dbSource {
			attrs = append(attrs, "note",
				"key co-located with ciphertext; does not defend against a full SQLite DB copy")
		}
		s.log.InfoContext(ctx, "storage: wrapped-secret master ready", attrs...)
	}
	return nil
}

// wrappedTokenPrefix is the unambiguous marker that a stored access or
// refresh token has been encrypted by keywrap. It lets unwrapGWToken tell
// ciphertext apart from legacy raw secrets without any heuristic and
// without touching the underlying base64.
//
// Format evolution:
//   - <no prefix> : legacy plaintext (JWT / opaque / refresh token).
//   - "kw1:"     : keywrap v1 — 12-byte nonce || GCM ciphertext || 16-byte
//     tag, base64-raw-URL encoded (see keywrap.Keeper).
//
// If a future version needs a different primitive (e.g. XChaCha20-Poly1305,
// or a HKDF info change), bump the number to kw2 / kw3 … and keep reading
// the older prefixes for backward compatibility — NEVER introduce ambiguity
// between "no prefix means legacy plaintext" and "no prefix means a new
// ciphertext format".
const wrappedTokenPrefix = "kw1:"

// wrapGWToken encrypts an upstream token for storage. Empty strings are
// passed through untouched so presence checks remain cheap. The returned
// blob carries the kw1: prefix; see wrappedTokenPrefix.
func (s *Store) wrapGWToken(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	blob, err := s.gwTokenKeeper.Wrap(plain)
	if err != nil {
		return "", err
	}
	return wrappedTokenPrefix + blob, nil
}

// unwrapGWToken decrypts a wrapped upstream token. Empty strings are
// passed through untouched.
//
// Decoding rules:
//
//  1. Blob starts with wrappedTokenPrefix ("kw1:") → definitely ciphertext.
//     Strip the prefix, try the primary keeper, then the fallback keeper
//     (when env rotated in but some tokens are still DB-keyed). If both
//     fail we return an error — it means the master key no longer matches,
//     NOT that this is legacy plaintext.
//
//  2. No prefix → definitely legacy plaintext written before keywrap was
//     introduced. Return as-is. The next UpdateCodexGatewayAccount will
//     re-wrap with the kw1: prefix so read-time branching converges over
//     time.
//
// This makes the boundary unambiguous: there is zero chance of mistaking
// a long opaque refresh token for ciphertext, or of leaking ciphertext as
// a bearer token when the master key is rotated but we missed a fallback.
func (s *Store) unwrapGWToken(blob string) (string, error) {
	if blob == "" {
		return "", nil
	}
	if !strings.HasPrefix(blob, wrappedTokenPrefix) {
		// Legacy raw secret. Return verbatim.
		return blob, nil
	}
	raw := strings.TrimPrefix(blob, wrappedTokenPrefix)
	if pt, err := s.gwTokenKeeper.Unwrap(raw); err == nil {
		return pt, nil
	}
	if s.gwTokenFallbackKeeper != nil {
		if pt, err := s.gwTokenFallbackKeeper.Unwrap(raw); err == nil {
			return pt, nil
		}
	}
	return "", fmt.Errorf("keywrap: unwrap (kw1 prefix): ciphertext does not decrypt under primary%s master",
		func() string {
			if s.gwTokenFallbackKeeper != nil {
				return " or fallback"
			}
			return ""
		}())
}

// wrapMailSecret encrypts a Mail-module secret (DNS provider token, ACME
// account key, webhook HMAC secret, import-mode IMAP password, …) for
// storage.  It uses the same kw1: prefix + base64-raw-URL encoding as
// wrapGWToken so the redaction + DB-dump-safe story is shared across
// modules; the underlying key material is different (see mailKeeperInfo).
func (s *Store) wrapMailSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if s.mailKeeper == nil {
		return "", fmt.Errorf("mail keeper not initialised")
	}
	blob, err := s.mailKeeper.Wrap(plain)
	if err != nil {
		return "", err
	}
	return wrappedTokenPrefix + blob, nil
}

func (s *Store) wrapDockerRegistrySecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	blob, err := s.dockerRegistrySecretKeeper.Wrap(plain)
	if err != nil {
		return "", err
	}
	return wrappedTokenPrefix + blob, nil
}

// unwrapMailSecret is the inverse of wrapMailSecret.  Fallback semantics
// mirror unwrapGWToken: kw1 prefix + primary → fallback → legacy
// plaintext (unwrapped values written pre-keywrap, before Mail module
// existed – the branch is kept so a future schema change cannot be
// ambiguous).
func (s *Store) unwrapMailSecret(blob string) (string, error) {
	if blob == "" {
		return "", nil
	}
	if !strings.HasPrefix(blob, wrappedTokenPrefix) {
		return blob, nil
	}
	raw := strings.TrimPrefix(blob, wrappedTokenPrefix)
	if s.mailKeeper != nil {
		if pt, err := s.mailKeeper.Unwrap(raw); err == nil {
			return pt, nil
		}
	}
	if s.mailFallbackKeeper != nil {
		if pt, err := s.mailFallbackKeeper.Unwrap(raw); err == nil {
			return pt, nil
		}
	}
	return "", ErrCorruptSecrets
}

func (s *Store) unwrapDockerRegistrySecret(blob string) (string, error) {
	if blob == "" {
		return "", nil
	}
	if !strings.HasPrefix(blob, wrappedTokenPrefix) {
		// Reserved for pre-keywrap alpha data. Current released builds
		// never write plaintext registry secrets into this column.
		return blob, nil
	}
	raw := strings.TrimPrefix(blob, wrappedTokenPrefix)
	if pt, err := s.dockerRegistrySecretKeeper.Unwrap(raw); err == nil {
		return pt, nil
	}
	if s.dockerRegistrySecretFallbackKeeper != nil {
		if pt, err := s.dockerRegistrySecretFallbackKeeper.Unwrap(raw); err == nil {
			return pt, nil
		}
	}
	return "", fmt.Errorf("keywrap: unwrap (kw1 prefix): docker registry credential secret does not decrypt under primary%s master",
		func() string {
			if s.dockerRegistrySecretFallbackKeeper != nil {
				return " or fallback"
			}
			return ""
		}())
}

// looksLegacyPlaintext is a last-resort classifier used ONLY by the
// GetCodexGatewayAccountSecret read path when unwrapGWToken returned an
// error — which, after the kw1: prefix scheme, can only happen if the
// stored column actually starts with kw1: but no configured master key
// can decrypt it. In that specific case we still want one more check
// before returning ErrCorruptSecrets, because the earliest alpha build of
// keywrap (pre-v1 launch) wrote ciphertext WITHOUT the kw1: prefix. Any
// ciphertext from that era should NOT be misidentified as a plaintext
// long opaque token — but since that alpha code never shipped, we can
// treat "has kw1 prefix + failed unwrap" as the unique signal for
// corruption and drop this classifier entirely once we are confident no
// alpha-era DBs remain in the wild.
//
// Rules are intentionally conservative and match the old heuristic; callers
// MUST gate this on the "kw1 unwrap failed" path and never use it on
// un-prefixed blobs (those are 100% legacy plaintext per unwrapGWToken).
func looksLegacyPlaintext(value string) bool {
	_ = value
	return false
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
  secret_ciphertext TEXT NOT NULL DEFAULT '',
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
  account_health_check_interval_seconds INTEGER NOT NULL DEFAULT 43200,
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
CREATE TABLE IF NOT EXISTS image_prompt_library (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  prompt TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'text_to_image',
  model TEXT NOT NULL DEFAULT '',
  aspect_ratio TEXT NOT NULL DEFAULT '',
  resolution TEXT NOT NULL DEFAULT '',
  image_count INTEGER NOT NULL DEFAULT 1,
  tags_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'active',
  use_count INTEGER NOT NULL DEFAULT 0,
  last_used_at TEXT NOT NULL DEFAULT '',
  deleted_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_image_prompt_library_status_updated ON image_prompt_library(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_prompt_library_mode_updated ON image_prompt_library(mode, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_prompt_library_last_used ON image_prompt_library(last_used_at DESC);
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
CREATE TABLE IF NOT EXISTS mail_accounts (
  id TEXT PRIMARY KEY,
  domain_id TEXT NOT NULL,
  local_part TEXT NOT NULL,
  address TEXT NOT NULL UNIQUE,
  display_name TEXT,
  password_mode TEXT NOT NULL DEFAULT 'set',
  quota_mb INTEGER DEFAULT 0,
  is_admin INTEGER DEFAULT 0,
  imap_sync_enabled INTEGER DEFAULT 1,
  imap_sync_state TEXT DEFAULT 'idle',
  imap_last_uidvalidity TEXT,
  imap_last_uid TEXT,
  imap_last_internaldate TEXT,
  imap_error TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  enabled INTEGER NOT NULL DEFAULT 1,
  last_login_at TEXT,
  created_at TEXT,
  updated_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_mail_accounts_domain_id ON mail_accounts(domain_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_accounts_address ON mail_accounts(address);
CREATE TABLE IF NOT EXISTS mail_aliases (
  id TEXT PRIMARY KEY,
  domain_id TEXT NOT NULL,
  source TEXT NOT NULL,
  recipients_csv TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'alias',
  list_name TEXT,
  list_reply_to TEXT,
  description TEXT,
  enabled INTEGER DEFAULT 1,
  created_at TEXT,
  updated_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_mail_aliases_domain_id ON mail_aliases(domain_id);
CREATE INDEX IF NOT EXISTS idx_mail_aliases_source ON mail_aliases(source);
CREATE TABLE IF NOT EXISTS mail_import_registrations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  data_dir TEXT NOT NULL,
  config_path TEXT,
  supervisor_type TEXT DEFAULT 'external',
  read_only INTEGER NOT NULL DEFAULT 1,
  probe_url TEXT,
  access_token_wrapped TEXT,
  status TEXT DEFAULT 'registered',
  last_probe_at TEXT,
  last_error TEXT,
  version TEXT,
  created_at TEXT,
  updated_at TEXT
);
CREATE TABLE IF NOT EXISTS media_provider_settings (
  provider TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 1,
  api_key TEXT NOT NULL DEFAULT '',
  api_key_masked TEXT NOT NULL DEFAULT '',
  default_image_model TEXT NOT NULL DEFAULT '',
  default_video_model TEXT NOT NULL DEFAULT '',
  default_image_params_json TEXT NOT NULL DEFAULT '{}',
  default_video_params_json TEXT NOT NULL DEFAULT '{}',
  last_tested_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS media_generation_jobs (
  id TEXT PRIMARY KEY,
  media_type TEXT NOT NULL,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  mode_label TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  endpoint TEXT NOT NULL DEFAULT '',
  prompt TEXT NOT NULL,
  parameters_json TEXT NOT NULL DEFAULT '{}',
  source_count INTEGER NOT NULL DEFAULT 0,
  output_count INTEGER NOT NULL DEFAULT 0,
  provider_task_id TEXT NOT NULL DEFAULT '',
  provider_video_id TEXT NOT NULL DEFAULT '',
  provider_status TEXT NOT NULL DEFAULT '',
  progress INTEGER NOT NULL DEFAULT 0,
  usage_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_media_generation_jobs_created ON media_generation_jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_generation_jobs_status ON media_generation_jobs(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_generation_jobs_media_type ON media_generation_jobs(media_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_generation_jobs_provider ON media_generation_jobs(provider, created_at DESC);
CREATE TABLE IF NOT EXISTS media_generation_sources (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  asset_id TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL,
  source_type TEXT NOT NULL,
  source_label TEXT NOT NULL DEFAULT '',
  source_role TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  url_redacted TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_generation_sources_job ON media_generation_sources(job_id, slot);
CREATE TABLE IF NOT EXISTS media_generation_outputs (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  asset_id TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL,
  media_type TEXT NOT NULL,
  remote_url_redacted TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  revised_prompt TEXT NOT NULL DEFAULT '',
  storage TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_generation_outputs_job ON media_generation_outputs(job_id, slot);
CREATE TABLE IF NOT EXISTS media_assets (
  id TEXT PRIMARY KEY,
  media_type TEXT NOT NULL,
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
  duration_seconds REAL NOT NULL DEFAULT 0,
  frame_rate INTEGER NOT NULL DEFAULT 0,
  frame_count INTEGER NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  storage_backend TEXT NOT NULL DEFAULT 'local',
  object_storage_profile_id TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_media_assets_created ON media_assets(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_media_type_created ON media_assets(media_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_storage_created ON media_assets(storage_backend, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_status_created ON media_assets(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_private_created ON media_assets(private, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_job ON media_assets(job_id, slot);
CREATE INDEX IF NOT EXISTS idx_media_assets_checksum ON media_assets(checksum_sha256);
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
		{"codex_gateway_settings", "account_health_check_interval_seconds", "INTEGER NOT NULL DEFAULT 43200"},
		{"docker_registry_credentials", "secret_ciphertext", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, column.table, column.name, column.def); err != nil {
			return err
		}
	}
	if err := s.migrateImageStorageToObjectProfile(ctx); err != nil {
		return err
	}
	if err := s.migrateCodexCli(ctx); err != nil {
		return err
	}
	if err := s.migrateStock(ctx); err != nil {
		return err
	}
	if err := s.migrateStockData(ctx); err != nil {
		return err
	}
	if err := s.migrateStockAgent(ctx); err != nil {
		return err
	}
	return s.migrateMail(ctx)
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
		{"codex_cli_threads", "execution_mode", "TEXT NOT NULL DEFAULT 'workspace'"},
		{"codex_cli_threads", "worktree_path", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "worktree_summary", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "base_branch", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "branch_name", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "worktree_status", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "merge_status", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_threads", "discarded_at", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_automations", "retry_count", "INTEGER NOT NULL DEFAULT 0"},
		{"codex_cli_automations", "failure_backoff_until", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_automation_runs", "turn_id", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_automation_runs", "last_heartbeat_at", "TEXT NOT NULL DEFAULT ''"},
		// Approval broker restart-recovery columns: persisted JSON-RPC id
		// lets us re-hydrate the in-memory reply map after boot;
		// recovery_status flags approvals whose codex app-server died on
		// restart (they are still decidable for audit, but no reply is
		// sent to Codex).
		{"codex_cli_approvals", "jsonrpc_request_id_json", "TEXT NOT NULL DEFAULT ''"},
		{"codex_cli_approvals", "recovery_status", "TEXT NOT NULL DEFAULT 'live'"},
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

// migrateMail creates the additive mail_* schema for the Mox sidecar
// control plane.  All CREATE TABLE statements are idempotent (IF NOT
// EXISTS) and additive columns are installed via the same ensureColumn
// helper used by the rest of storage.
//
// Design boundary: the Mail module stores only Phantom-owned metadata
// (domains, accounts, aliases, certificates, runtime state, delivery
// events, probe results, IMAP sync indices).  Actual MIME messages,
// queue bodies, and Mox auto-generated files live on disk under
// <data>/mail/mox/data and are NOT row-stored here.  This keeps SQLite
// bounded in size.
//
// FTS5 note: mail_fts is an *external* contentless FTS5 table.  Inserts
// / deletes are written from storage_mail.go's MailInsertMessage /
// MailDeleteMessage helpers (they MUST be the only writers), NOT via
// triggers, because contentless tables require the caller to supply the
// rowid themselves.
// stripFTS5 removes from sql every statement or statement-group that depends
// on the SQLite FTS5 virtual-table module.  The goal is to keep the rest of
// the mail schema bootable even when the binary was not compiled with the
// -tags sqlite_fts5 build tag.  Removed regions:
//
//  1. Any block starting with a line matching `CREATE VIRTUAL TABLE ... USING
//     fts5(` and ending at the next line whose trimmed content is `);` (the
//     closing of the virtual-table declaration).
//  2. Any block starting with a line matching `CREATE TRIGGER ...
//     mail_messages_p7_(ai|au|ad)` and ending at the next bare `END;` line.
//
// The function is intentionally lenient about leading whitespace on every
// line because the additive schema sections in migrateMail mix 0-indent and
// 1-tab-indent SQL depending on which phase introduced the section.
func stripFTS5(sql string) string {
	lines := strings.Split(sql, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		// Case (1): virtual table start.
		if strings.Contains(trimmed, "CREATE VIRTUAL TABLE") && strings.Contains(trimmed, "USING fts5") {
			// Consume lines until we find one whose trimmed body is exactly ");".
			for i < len(lines) {
				if strings.TrimSpace(lines[i]) == ");" {
					break
				}
				i++
			}
			// Don't emit the ");" line either — leave it out entirely.
			continue
		}
		// Case (2): FTS5 p7 triggers (3 in a row, each starts with CREATE TRIGGER
		// with the mail_messages_p7 prefix).
		if strings.Contains(trimmed, "CREATE TRIGGER") &&
			(strings.Contains(trimmed, "mail_messages_p7_ai") ||
				strings.Contains(trimmed, "mail_messages_p7_au") ||
				strings.Contains(trimmed, "mail_messages_p7_ad")) {
			for i < len(lines) {
				if strings.TrimSpace(lines[i]) == "END;" {
					break
				}
				i++
			}
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

func (s *Store) migrateMail(ctx context.Context) error {
	// The mail schema contains CREATE VIRTUAL TABLE ... USING fts5() statements
	// plus FTS5-backed triggers.  These require the SQLite driver to be built
	// with the -tags sqlite_fts5 build tag; without it the statements fail at
	// Exec time.  We therefore split the schema into (a) a BASE schema that is
	// always applied and (b) an FTS5-only schema that is applied only when
	// the driver reports FTS5 available.  This lets tests and production
	// binaries that omit the build tag still boot and operate (search falls
	// back to LIKE).
	baseSQL := `
CREATE TABLE IF NOT EXISTS mail_mox_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  phantom_instance_id TEXT NOT NULL DEFAULT '',
  import_mode INTEGER NOT NULL DEFAULT 0,
  import_label TEXT NOT NULL DEFAULT '',
  config_mode TEXT NOT NULL DEFAULT 'managed',
  desired_state TEXT NOT NULL DEFAULT 'stopped',
  mox_binary_path TEXT NOT NULL DEFAULT '',
  mox_data_dir TEXT NOT NULL DEFAULT '',
  mox_config_path TEXT NOT NULL DEFAULT '',
  webapi_endpoint TEXT NOT NULL DEFAULT '',
  admin_email TEXT NOT NULL DEFAULT '',
  hostname TEXT NOT NULL DEFAULT '',
  smtp_port INTEGER NOT NULL DEFAULT 25,
  smtp_submission_port INTEGER NOT NULL DEFAULT 587,
  smtps_port INTEGER NOT NULL DEFAULT 465,
  imap_port INTEGER NOT NULL DEFAULT 143,
  imaps_port INTEGER NOT NULL DEFAULT 993,
  webmail_addr TEXT NOT NULL DEFAULT '127.0.0.1:10444',
  webapi_addr TEXT NOT NULL DEFAULT '127.0.0.1:10445',
  acme_default_provider_id TEXT NOT NULL DEFAULT '',
  queue_max_size_bytes INTEGER NOT NULL DEFAULT 1073741824,
  queue_max_age_seconds INTEGER NOT NULL DEFAULT 2592000,
  outbound_rate_limit_per_hour INTEGER NOT NULL DEFAULT 0,
  retention_delivery_events_days INTEGER NOT NULL DEFAULT 90,
  retention_health_checks_per_type INTEGER NOT NULL DEFAULT 100,
  search_index_max_size_gb INTEGER NOT NULL DEFAULT 10,
  dnsbl_enabled INTEGER NOT NULL DEFAULT 1,
  dnsbl_providers_json TEXT NOT NULL DEFAULT '{}',
  extra_capabilities_json TEXT NOT NULL DEFAULT '{}',
  imapsync_enabled INTEGER NOT NULL DEFAULT 1,
  imapsync_max_size_bytes INTEGER NOT NULL DEFAULT 10737418240,
  imapsync_big_message_size_limit_bytes INTEGER NOT NULL DEFAULT 52428800,
  imapsync_interval_attachment_cache_enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mail_domains (
  id TEXT PRIMARY KEY,
  domain TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  dkim_selector TEXT NOT NULL DEFAULT 'default',
  dkim_private_key_wrapped TEXT NOT NULL DEFAULT '',
  dmarc_policy TEXT NOT NULL DEFAULT 'p=none',
  dmarc_rua TEXT NOT NULL DEFAULT '',
  spf_include TEXT NOT NULL DEFAULT '',
  dns_provider_id TEXT NOT NULL DEFAULT '',
  cert_id TEXT NOT NULL DEFAULT '',
  synced INTEGER NOT NULL DEFAULT 0,
  last_synced_at TEXT NOT NULL DEFAULT '',
  last_sync_error TEXT NOT NULL DEFAULT '',
  last_dns_check_at TEXT NOT NULL DEFAULT '',
  dns_check_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_domains_enabled ON mail_domains(enabled, updated_at DESC);
CREATE TABLE IF NOT EXISTS mail_accounts (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  recovery_email TEXT NOT NULL DEFAULT '',
  storage_limit_mb INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  role TEXT NOT NULL DEFAULT 'user',
  import_mode_read_only INTEGER NOT NULL DEFAULT 0,
  synced INTEGER NOT NULL DEFAULT 0,
  last_synced_at TEXT NOT NULL DEFAULT '',
  last_sync_error TEXT NOT NULL DEFAULT '',
  last_password_changed_at TEXT NOT NULL DEFAULT '',
  sync_state TEXT NOT NULL DEFAULT 'idle',
  sync_folder_stats_json TEXT NOT NULL DEFAULT '{}',
  sync_last_uid_json TEXT NOT NULL DEFAULT '{}',
  sync_last_run_at TEXT NOT NULL DEFAULT '',
  sync_next_run_at TEXT NOT NULL DEFAULT '',
  sync_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_accounts_enabled ON mail_accounts(enabled, updated_at DESC);
CREATE TABLE IF NOT EXISTS mail_addresses (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  localpart TEXT NOT NULL DEFAULT '',
  domain TEXT NOT NULL,
  address TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL DEFAULT 'primary',
  enabled INTEGER NOT NULL DEFAULT 1,
  synced INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_addresses_account ON mail_addresses(account_id, kind);
CREATE INDEX IF NOT EXISTS idx_mail_addresses_domain ON mail_addresses(domain);
CREATE TABLE IF NOT EXISTS mail_aliases (
  id TEXT PRIMARY KEY,
  alias_address TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT 'standard',
  enabled INTEGER NOT NULL DEFAULT 1,
  synced INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_aliases_enabled ON mail_aliases(enabled);
CREATE TABLE IF NOT EXISTS mail_alias_recipients (
  id TEXT PRIMARY KEY,
  alias_id TEXT NOT NULL,
  recipient_address TEXT NOT NULL,
  position INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (alias_id, recipient_address)
);
CREATE INDEX IF NOT EXISTS idx_mail_alias_recipients_alias ON mail_alias_recipients(alias_id, position);
CREATE TABLE IF NOT EXISTS mail_certificates (
  id TEXT PRIMARY KEY,
  domain TEXT NOT NULL UNIQUE,
  issuer TEXT NOT NULL DEFAULT '',
  serial TEXT NOT NULL DEFAULT '',
  not_before TEXT NOT NULL DEFAULT '',
  not_after TEXT NOT NULL DEFAULT '',
  pem_chain TEXT NOT NULL DEFAULT '',
  dns_provider_id TEXT NOT NULL DEFAULT '',
  last_renewal_attempt TEXT NOT NULL DEFAULT '',
  next_renewal TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_certificates_expiry ON mail_certificates(not_after);
CREATE INDEX IF NOT EXISTS idx_mail_certificates_next_renewal ON mail_certificates(next_renewal);
CREATE TABLE IF NOT EXISTS mail_dns_providers (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'manual',
  api_endpoint TEXT NOT NULL DEFAULT '',
  zone_id TEXT NOT NULL DEFAULT '',
  api_credentials_wrapped TEXT NOT NULL DEFAULT '',
  last_tested_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mail_runtime_state (
  id TEXT PRIMARY KEY,
  pid INTEGER NOT NULL DEFAULT 0,
  start_time_ns INTEGER NOT NULL DEFAULT 0,
  boot_id TEXT NOT NULL DEFAULT '',
  binary_path TEXT NOT NULL DEFAULT '',
  binary_version TEXT NOT NULL DEFAULT '',
  binary_checksum_sha256 TEXT NOT NULL DEFAULT '',
  config_hash_sha256 TEXT NOT NULL DEFAULT '',
  config_drifted INTEGER NOT NULL DEFAULT 0,
  drift_detected_at TEXT NOT NULL DEFAULT '',
  observed_state TEXT NOT NULL DEFAULT 'unknown',
  observed_at TEXT NOT NULL DEFAULT '',
  last_exit_code INTEGER NOT NULL DEFAULT 0,
  last_exit_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  crash_loop_state TEXT NOT NULL DEFAULT 'stable',
  crash_loop_backoff_until TEXT NOT NULL DEFAULT '',
  crash_loop_stable_since TEXT NOT NULL DEFAULT '',
  consecutive_crashes INTEGER NOT NULL DEFAULT 0,
  probes_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mail_mox_health_checks (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT '',
  scope_id TEXT NOT NULL DEFAULT '',
  level TEXT NOT NULL DEFAULT 'L1',
  status TEXT NOT NULL DEFAULT 'unknown',
  severity TEXT NOT NULL DEFAULT 'neutral',
  summary TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_health_kind ON mail_mox_health_checks(kind, started_at DESC, severity);
CREATE INDEX IF NOT EXISTS idx_mail_health_scope ON mail_mox_health_checks(scope, scope_id, started_at DESC);
CREATE TABLE IF NOT EXISTS mail_delivery_events (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  direction TEXT NOT NULL,
  from_domain TEXT NOT NULL DEFAULT '',
  to_domain TEXT NOT NULL DEFAULT '',
  message_id_hash_sha256 TEXT NOT NULL DEFAULT '',
  subject_snippet TEXT NOT NULL DEFAULT '',
  smtp_code INTEGER NOT NULL DEFAULT 0,
  smtp_enhanced_code TEXT NOT NULL DEFAULT '',
  redacted_error TEXT NOT NULL DEFAULT '',
  queue_id TEXT NOT NULL DEFAULT '',
  remote_mx TEXT NOT NULL DEFAULT '',
  attempt INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'pending',
  recipient_hash TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_delivery_time ON mail_delivery_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_delivery_kind ON mail_delivery_events(kind, created_at DESC);
CREATE TABLE IF NOT EXISTS mail_queue_entries (
  id TEXT PRIMARY KEY,
  queue_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  from_domain TEXT NOT NULL DEFAULT '',
  recipient TEXT NOT NULL DEFAULT '',
  subject_snippet TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL DEFAULT '',
  last_error_snippet TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'hold',
  synced INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_queue_state ON mail_queue_entries(state, next_attempt_at);
CREATE TABLE IF NOT EXISTS mail_suppressions (
  id TEXT PRIMARY KEY,
  address TEXT NOT NULL UNIQUE,
  recipient_hash TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'manual',
  expires_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mail_webhooks (
  id TEXT PRIMARY KEY,
  direction TEXT NOT NULL,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  hmac_secret_wrapped TEXT NOT NULL DEFAULT '',
  events_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1,
  last_delivery_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mail_messages (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  folder_name TEXT NOT NULL,
  imap_uidvalidity INTEGER NOT NULL DEFAULT 0,
  imap_uid INTEGER NOT NULL DEFAULT 0,
  message_id_header TEXT NOT NULL DEFAULT '',
  in_reply_to TEXT NOT NULL DEFAULT '',
  "references" TEXT NOT NULL DEFAULT '',
  from_addresses_json TEXT NOT NULL DEFAULT '[]',
  to_addresses_json TEXT NOT NULL DEFAULT '[]',
  cc_addresses_json TEXT NOT NULL DEFAULT '[]',
  bcc_addresses_json TEXT NOT NULL DEFAULT '[]',
  reply_to_json TEXT NOT NULL DEFAULT '[]',
  subject TEXT NOT NULL DEFAULT '',
  internal_date TEXT NOT NULL DEFAULT '',
  received_at TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  seen INTEGER NOT NULL DEFAULT 0,
  flagged INTEGER NOT NULL DEFAULT 0,
  answered INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  draft INTEGER NOT NULL DEFAULT 0,
  extra_flags_json TEXT NOT NULL DEFAULT '[]',
  preview_plain_text TEXT NOT NULL DEFAULT '',
  attachments_json TEXT NOT NULL DEFAULT '[]',
  raw_available INTEGER NOT NULL DEFAULT 1,
  sync_checkpoint TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_messages_folder ON mail_messages(account_id, folder_name, imap_uidvalidity, imap_uid);
CREATE INDEX IF NOT EXISTS idx_mail_messages_date ON mail_messages(account_id, received_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_messages_imap_key ON mail_messages(account_id, folder_name, imap_uidvalidity, imap_uid);
CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts USING fts5(
  subject,
  body,
  sender_name,
  sender_addr,
  recipient_addr,
  content='',
  tokenize='unicode61 remove_diacritics 2'
);
CREATE TABLE IF NOT EXISTS mail_folders (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  name TEXT NOT NULL,
  delimiter TEXT NOT NULL DEFAULT '/',
  parent_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'custom',   -- inbox/sent/drafts/trash/archive/custom
  uid_next TEXT NOT NULL DEFAULT '1',
  uid_validity TEXT NOT NULL DEFAULT '1',
  total_messages INTEGER NOT NULL DEFAULT 0,
  unread_messages INTEGER NOT NULL DEFAULT 0,
  flagged_messages INTEGER NOT NULL DEFAULT 0,
  deleted_messages INTEGER NOT NULL DEFAULT 0,
  subscribed INTEGER NOT NULL DEFAULT 1,
  selectable INTEGER NOT NULL DEFAULT 1,
  unread_count INTEGER NOT NULL DEFAULT 0,
  total_count INTEGER NOT NULL DEFAULT 0,
  highest_mod_seq INTEGER,
  last_synced_at TEXT NOT NULL DEFAULT '',
  last_sync_error TEXT NOT NULL DEFAULT '',
  sync_checkpoint TEXT,
  sync_state TEXT NOT NULL DEFAULT 'idle',
  attributes_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(account_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mail_folders_account ON mail_folders(account_id);
CREATE TABLE IF NOT EXISTS mail_drafts (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  subject TEXT,
  from_addresses_json TEXT,
  to_addresses_json TEXT,
  cc_addresses_json TEXT,
  bcc_addresses_json TEXT,
  reply_to_addresses_json TEXT,
  body_html TEXT,
  body_text TEXT,
  attachments_json TEXT,
  in_reply_to TEXT,
  message_id_header TEXT,
  saved_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_drafts_account ON mail_drafts(account_id);
CREATE TABLE IF NOT EXISTS mail_backups (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  archive_path TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  include_data INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'pending',
  error_message TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_backups_created ON mail_backups(created_at DESC);
CREATE TABLE IF NOT EXISTS mail_import_registry (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  mox_data_dir TEXT NOT NULL,
  mox_config_path TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'read_only',
  webapi_endpoint TEXT NOT NULL DEFAULT '',
  last_connected_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
	-- Phase 4: certmanager tables.  mail_certificates and mail_dns_providers
	-- are defined earlier in migrateMail() with the slim schema matching CRUD.
	-- Missing columns on older installations are added via ensureColumn below.
	CREATE TABLE IF NOT EXISTS mail_dns_providers (
	  id TEXT PRIMARY KEY,
	  kind TEXT NOT NULL DEFAULT 'manual',
	  display_name TEXT NOT NULL DEFAULT '',
	  config_json TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS mail_manual_challenges (
	  id TEXT PRIMARY KEY,
	  domain TEXT NOT NULL DEFAULT '',
	  fqdn TEXT NOT NULL UNIQUE,
	  value TEXT NOT NULL DEFAULT '',
	  status TEXT NOT NULL DEFAULT 'pending',
	  created_at TEXT NOT NULL DEFAULT '',
	  expires_at TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_mail_manual_challenges_status ON mail_manual_challenges(status, created_at);
	-- Phase 6: webhook + delivery + queue + suppression + rate + DNSBL schema.
	CREATE TABLE IF NOT EXISTS mail_webhook_registrations (
	  id TEXT PRIMARY KEY,
	  name TEXT NOT NULL,
	  direction TEXT NOT NULL DEFAULT 'in',
	  url TEXT NOT NULL DEFAULT '',
	  wrapped_secret TEXT NOT NULL DEFAULT '',
	  signing_alg TEXT NOT NULL DEFAULT 'hmac-sha256',
	  source_cidr TEXT NOT NULL DEFAULT '127.0.0.1/32,::1/128',
	  max_body_bytes INTEGER NOT NULL DEFAULT 1048576,
	  timestamp_skew_seconds INTEGER NOT NULL DEFAULT 900,
	  event_mask TEXT NOT NULL DEFAULT '*',
	  enabled INTEGER NOT NULL DEFAULT 1,
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS mail_webhook_events (
	  id TEXT PRIMARY KEY,
	  registration_id TEXT NOT NULL DEFAULT '',
	  direction TEXT NOT NULL DEFAULT 'in',
	  event_type TEXT NOT NULL DEFAULT '',
	  payload_hash TEXT NOT NULL DEFAULT '',
	  payload_size INTEGER NOT NULL DEFAULT 0,
	  source_addr TEXT NOT NULL DEFAULT '',
	  hmac_valid INTEGER NOT NULL DEFAULT 0,
	  timestamp_skew_ms INTEGER NOT NULL DEFAULT 0,
	  status TEXT NOT NULL DEFAULT 'received',
	  error_reason TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mail_webhook_events_registration ON mail_webhook_events(registration_id);
	CREATE INDEX IF NOT EXISTS idx_mail_webhook_events_type ON mail_webhook_events(event_type);
	CREATE INDEX IF NOT EXISTS idx_mail_webhook_events_created ON mail_webhook_events(created_at DESC);
	CREATE TABLE IF NOT EXISTS mail_delivery_events (
	  id TEXT PRIMARY KEY,
	  from_domain TEXT NOT NULL DEFAULT '',
	  to_domain TEXT NOT NULL DEFAULT '',
	  message_id_hash TEXT NOT NULL DEFAULT '',
	  subject_snippet TEXT NOT NULL DEFAULT '',
	  direction TEXT NOT NULL DEFAULT 'out',
	  smtp_code INTEGER NOT NULL DEFAULT 0,
	  smtp_enhanced TEXT NOT NULL DEFAULT '',
	  redacted_error TEXT NOT NULL DEFAULT '',
	  status TEXT NOT NULL DEFAULT 'pending',
	  attempt_count INTEGER NOT NULL DEFAULT 0,
	  first_attempt_at TEXT NOT NULL DEFAULT '',
	  last_attempt_at TEXT NOT NULL DEFAULT '',
	  completed_at TEXT NOT NULL DEFAULT '',
	  recipient_hash TEXT NOT NULL DEFAULT '',
	  queue_msg_id INTEGER NOT NULL DEFAULT 0,
	  from_id TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mail_delivery_status ON mail_delivery_events(status);
	CREATE INDEX IF NOT EXISTS idx_mail_delivery_from_domain ON mail_delivery_events(from_domain);
	CREATE INDEX IF NOT EXISTS idx_mail_delivery_to_domain ON mail_delivery_events(to_domain);
	CREATE INDEX IF NOT EXISTS idx_mail_delivery_created ON mail_delivery_events(created_at DESC);
	CREATE TABLE IF NOT EXISTS mail_queue_summary (
	  bucket TEXT PRIMARY KEY,
	  count INTEGER NOT NULL DEFAULT 0,
	  last_updated TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS mail_suppressions (
	  id TEXT PRIMARY KEY,
	  recipient_hash TEXT NOT NULL UNIQUE,
	  domain_id TEXT NOT NULL DEFAULT '',
	  reason TEXT NOT NULL DEFAULT '',
	  smtp_code INTEGER NOT NULL DEFAULT 0,
	  source TEXT NOT NULL DEFAULT '',
	  added_at TEXT NOT NULL DEFAULT '',
	  expires_at TEXT NOT NULL DEFAULT '',
	  active INTEGER NOT NULL DEFAULT 1,
	  created_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_suppressions_recipient ON mail_suppressions(recipient_hash);
	CREATE TABLE IF NOT EXISTS mail_outbound_rate_counters (
	  scope TEXT NOT NULL,
	  window TEXT NOT NULL,
	  window_start TEXT NOT NULL,
	  count INTEGER NOT NULL DEFAULT 0,
	  bytes INTEGER NOT NULL DEFAULT 0,
	  failed INTEGER NOT NULL DEFAULT 0,
	  PRIMARY KEY (scope, window, window_start)
	) WITHOUT ROWID;
	CREATE TABLE IF NOT EXISTS mail_outbound_thresholds (
	  scope TEXT PRIMARY KEY,
	  send_1m_warn INTEGER NOT NULL DEFAULT 1000,
	  send_1m_crit INTEGER NOT NULL DEFAULT 5000,
	  send_1h_warn INTEGER NOT NULL DEFAULT 50000,
	  send_1h_crit INTEGER NOT NULL DEFAULT 200000,
	  bounce_rate_pct_warn REAL NOT NULL DEFAULT 5.0,
	  bounce_rate_pct_crit REAL NOT NULL DEFAULT 20.0,
	  updated_at TEXT NOT NULL
	);
	-- Phase 7: IMAP sync folders, messages, FTS5, search-index health.
	-- mail_folders is defined earlier in migrateMail() with the union schema.
	-- Indexes on upgraded columns are declared below (post-ensureColumn).
	CREATE TABLE IF NOT EXISTS mail_message_parts (
	  id TEXT PRIMARY KEY,
	  folder_id TEXT NOT NULL,
	  message_id TEXT NOT NULL,
	  part_id TEXT NOT NULL DEFAULT '',
	  content_type TEXT NOT NULL DEFAULT 'text/plain',
	  content_transfer_encoding TEXT NOT NULL DEFAULT '',
	  charset TEXT NOT NULL DEFAULT '',
	  filename TEXT NOT NULL DEFAULT '',
	  content_id TEXT NOT NULL DEFAULT '',
	  disposition TEXT NOT NULL DEFAULT '',
	  size_bytes INTEGER NOT NULL DEFAULT 0,
	  body_cache_path TEXT NOT NULL DEFAULT '',
	  body_hash_sha256 TEXT NOT NULL DEFAULT '',
	  decoded_text TEXT NOT NULL DEFAULT '',
	  is_attachment INTEGER NOT NULL DEFAULT 0,
	  is_inline INTEGER NOT NULL DEFAULT 0,
	  created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mail_message_parts_message ON mail_message_parts(message_id);
	CREATE INDEX IF NOT EXISTS idx_mail_message_parts_folder ON mail_message_parts(folder_id);
	CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts5 USING fts5(
	  subject,
	  body,
	  sender_name,
	  sender_addr,
	  recipient_addr,
	  content='mail_message_parts',
	  content_rowid='rowid',
	  tokenize='unicode61 remove_diacritics 2'
	);
	CREATE TABLE IF NOT EXISTS mail_search_index_health (
	  account_id TEXT PRIMARY KEY,
	  messages_indexed INTEGER NOT NULL DEFAULT 0,
	  messages_pending INTEGER NOT NULL DEFAULT 0,
	  messages_missing INTEGER NOT NULL DEFAULT 0,
	  attachments_indexed INTEGER NOT NULL DEFAULT 0,
	  attachments_pending INTEGER NOT NULL DEFAULT 0,
	  index_size_bytes INTEGER NOT NULL DEFAULT 0,
	  last_rebuild_at TEXT NOT NULL DEFAULT '',
	  last_optimize_at TEXT NOT NULL DEFAULT '',
	  last_verify_at TEXT NOT NULL DEFAULT '',
	  last_error TEXT NOT NULL DEFAULT '',
	  status TEXT NOT NULL DEFAULT 'healthy',
	  updated_at TEXT NOT NULL
	);
	-- ==== Phase 7 (revised IMAP sync + FTS5 search schema) ====
	CREATE TABLE IF NOT EXISTS mail_folders_p7 (
	  id TEXT PRIMARY KEY,
	  account_id TEXT NOT NULL,
	  name TEXT NOT NULL,
	  path TEXT NOT NULL DEFAULT '',
	  delim TEXT NOT NULL DEFAULT '/',
	  attributes_csv TEXT NOT NULL DEFAULT '',
	  uid_validity TEXT NOT NULL DEFAULT '',
	  uid_next INTEGER NOT NULL DEFAULT 1,
	  total_messages INTEGER NOT NULL DEFAULT 0,
	  unseen_count INTEGER NOT NULL DEFAULT 0,
	  subscribed INTEGER NOT NULL DEFAULT 1,
	  last_synced_at TEXT NOT NULL DEFAULT '',
	  imap_error TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL,
	  UNIQUE(account_id, path)
	);
	CREATE INDEX IF NOT EXISTS idx_mail_folders_p7_account_id ON mail_folders_p7(account_id);
	CREATE TABLE IF NOT EXISTS mail_messages_p7 (
	  id TEXT PRIMARY KEY,
	  account_id TEXT NOT NULL,
	  folder_id TEXT NOT NULL,
	  uid INTEGER NOT NULL,
	  mox_msg_id INTEGER NOT NULL DEFAULT 0,
	  message_id_hash TEXT NOT NULL DEFAULT '',
	  subject TEXT NOT NULL DEFAULT '',
	  from_list_csv TEXT NOT NULL DEFAULT '',
	  to_list_csv TEXT NOT NULL DEFAULT '',
	  cc_list_csv TEXT NOT NULL DEFAULT '',
	  bcc_list_csv TEXT NOT NULL DEFAULT '',
	  reply_to_csv TEXT NOT NULL DEFAULT '',
	  date_sent TEXT NOT NULL DEFAULT '',
	  internaldate TEXT NOT NULL DEFAULT '',
	  flags_csv TEXT NOT NULL DEFAULT '',
	  size_bytes INTEGER NOT NULL DEFAULT 0,
	  has_attachment INTEGER NOT NULL DEFAULT 0,
	  attachments_json TEXT NOT NULL DEFAULT '[]',
	  preview_text TEXT NOT NULL DEFAULT '',
	  body_text TEXT NOT NULL DEFAULT '',
	  charset TEXT NOT NULL DEFAULT 'utf-8',
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL,
	  UNIQUE(account_id, folder_id, uid)
	);
	CREATE INDEX IF NOT EXISTS idx_mail_messages_p7_account_date ON mail_messages_p7(account_id, date_sent);
	CREATE INDEX IF NOT EXISTS idx_mail_messages_p7_folder_uid ON mail_messages_p7(folder_id, uid);
	CREATE INDEX IF NOT EXISTS idx_mail_messages_p7_message_id_hash ON mail_messages_p7(message_id_hash);
	CREATE INDEX IF NOT EXISTS idx_mail_messages_p7_folder_date ON mail_messages_p7(folder_id, date_sent);
	CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts5_p7 USING fts5(
	  subject,
	  from_list,
	  to_list,
	  body_text,
	  preview_text,
	  tokenize='unicode61 remove_diacritics 2'
	);
	CREATE TRIGGER IF NOT EXISTS mail_messages_p7_ai AFTER INSERT ON mail_messages_p7 BEGIN
	  INSERT INTO mail_fts5_p7(rowid, subject, from_list, to_list, body_text, preview_text)
	    VALUES (new.rowid, new.subject, new.from_list_csv, new.to_list_csv, new.body_text, new.preview_text);
	END;
	CREATE TRIGGER IF NOT EXISTS mail_messages_p7_au AFTER UPDATE ON mail_messages_p7 BEGIN
	  DELETE FROM mail_fts5_p7 WHERE rowid = old.rowid;
	  INSERT INTO mail_fts5_p7(rowid, subject, from_list, to_list, body_text, preview_text)
	    VALUES (new.rowid, new.subject, new.from_list_csv, new.to_list_csv, new.body_text, new.preview_text);
	END;
	CREATE TRIGGER IF NOT EXISTS mail_messages_p7_ad AFTER DELETE ON mail_messages_p7 BEGIN
	  DELETE FROM mail_fts5_p7 WHERE rowid = old.rowid;
	END;
	CREATE TABLE IF NOT EXISTS mail_index_health_p7 (
	  account_id TEXT PRIMARY KEY,
	  total_messages INTEGER NOT NULL DEFAULT 0,
	  total_size_bytes INTEGER NOT NULL DEFAULT 0,
	  sync_state TEXT NOT NULL DEFAULT 'idle',
	  last_full_sync_at TEXT NOT NULL DEFAULT '',
	  last_incr_sync_at TEXT NOT NULL DEFAULT '',
	  last_error TEXT NOT NULL DEFAULT '',
	  updated_at TEXT NOT NULL
	);
	-- ==== Phase 8: backup schedules, retention rules.
	CREATE TABLE IF NOT EXISTS mail_backup_schedules (
	  id TEXT PRIMARY KEY,
	  name TEXT NOT NULL DEFAULT '',
	  scope TEXT NOT NULL DEFAULT 'global',
	  scope_id TEXT NOT NULL DEFAULT '',
	  schedule_kind TEXT NOT NULL DEFAULT 'full',
	  cadence_cron TEXT NOT NULL DEFAULT '0 3 * * 0',
	  cron_expression TEXT NOT NULL DEFAULT '',
	  timezone TEXT NOT NULL DEFAULT 'UTC',
	  keep_revisions INTEGER NOT NULL DEFAULT 0,
	  retention_days INTEGER NOT NULL DEFAULT 30,
	  contains_config INTEGER NOT NULL DEFAULT 1,
	  contains_data INTEGER NOT NULL DEFAULT 1,
	  encryption_mode TEXT NOT NULL DEFAULT 'none',
	  encrypt_password_hash TEXT NOT NULL DEFAULT '',
	  storage_target TEXT NOT NULL DEFAULT '',
	  target_url TEXT NOT NULL DEFAULT '',
	  target_credentials_json TEXT NOT NULL DEFAULT '',
	  pre_run_hook TEXT NOT NULL DEFAULT '',
	  post_run_hook TEXT NOT NULL DEFAULT '',
	  next_run_at TEXT NOT NULL DEFAULT '',
	  last_run_at TEXT NOT NULL DEFAULT '',
	  last_status TEXT NOT NULL DEFAULT '',
	  last_backup_id TEXT NOT NULL DEFAULT '',
	  last_error TEXT NOT NULL DEFAULT '',
	  note TEXT NOT NULL DEFAULT '',
	  enabled INTEGER NOT NULL DEFAULT 1,
	  paused INTEGER NOT NULL DEFAULT 0,
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL,
	  UNIQUE(scope, scope_id, schedule_kind)
	);
	CREATE INDEX IF NOT EXISTS idx_mail_backup_schedules_enabled ON mail_backup_schedules(enabled);
	CREATE INDEX IF NOT EXISTS idx_mail_backup_schedules_scope ON mail_backup_schedules(scope, scope_id);
	CREATE INDEX IF NOT EXISTS idx_mail_backup_schedules_next ON mail_backup_schedules(next_run_at);
	CREATE TABLE IF NOT EXISTS mail_retention_rules (
	  id TEXT PRIMARY KEY,
	  name TEXT NOT NULL DEFAULT '',
	  rule_kind TEXT NOT NULL DEFAULT 'custom',
	  target_kind TEXT NOT NULL DEFAULT 'delivery_events',
	  scope TEXT NOT NULL DEFAULT 'global',
	  scope_id TEXT NOT NULL DEFAULT '',
	  category TEXT NOT NULL DEFAULT 'all',
	  days INTEGER NOT NULL DEFAULT 90,
	  keep_min_count INTEGER NOT NULL DEFAULT 0,
	  prune_empty_folders INTEGER NOT NULL DEFAULT 0,
	  hard_delete INTEGER NOT NULL DEFAULT 0,
	  enabled INTEGER NOT NULL DEFAULT 1,
	  description TEXT NOT NULL DEFAULT '',
	  note TEXT NOT NULL DEFAULT '',
	  last_run_at TEXT NOT NULL DEFAULT '',
	  last_pruned_count INTEGER NOT NULL DEFAULT 0,
	  last_pruned_at TEXT NOT NULL DEFAULT '',
	  last_error TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mail_retention_enabled ON mail_retention_rules(enabled);
	-- ==== Phase 8 Part A: detailed retention rules, backup artifacts, schedules ====
	CREATE TABLE IF NOT EXISTS mail_log_retention_rules (
	  id TEXT PRIMARY KEY,
	  scope TEXT NOT NULL DEFAULT 'global',
	  scope_id TEXT NOT NULL DEFAULT '',
	  category TEXT NOT NULL DEFAULT 'all',
	  keep_days INTEGER NOT NULL DEFAULT 0,
	  prune_empty_folders INTEGER NOT NULL DEFAULT 0,
	  hard_delete INTEGER NOT NULL DEFAULT 0,
	  note TEXT NOT NULL DEFAULT '',
	  enabled INTEGER NOT NULL DEFAULT 1,
	  last_pruned_at TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL,
	  UNIQUE(scope, scope_id, category)
	);
	CREATE INDEX IF NOT EXISTS idx_mail_log_retention_scope ON mail_log_retention_rules(scope, scope_id);
	CREATE TABLE IF NOT EXISTS mail_backups_new (
	  id TEXT PRIMARY KEY,
	  scope TEXT NOT NULL DEFAULT 'global',
	  scope_id TEXT NOT NULL DEFAULT '',
	  kind TEXT NOT NULL DEFAULT 'full',
	  display_name TEXT NOT NULL DEFAULT '',
	  file_path TEXT NOT NULL DEFAULT '',
	  file_size_bytes INTEGER NOT NULL DEFAULT 0,
	  checksum_sha256 TEXT NOT NULL DEFAULT '',
	  contains_config INTEGER NOT NULL DEFAULT 0,
	  contains_data INTEGER NOT NULL DEFAULT 0,
	  encryption_mode TEXT NOT NULL DEFAULT 'none',
	  note TEXT NOT NULL DEFAULT '',
	  status TEXT NOT NULL DEFAULT 'pending',
	  error_message TEXT NOT NULL DEFAULT '',
	  created_by TEXT NOT NULL DEFAULT '',
	  expires_at TEXT NOT NULL DEFAULT '',
	  started_at TEXT NOT NULL DEFAULT '',
	  completed_at TEXT NOT NULL DEFAULT '',
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mail_backups_new_scope ON mail_backups_new(scope, scope_id);
	CREATE INDEX IF NOT EXISTS idx_mail_backups_new_created ON mail_backups_new(created_at);
	CREATE INDEX IF NOT EXISTS idx_mail_backups_new_expires ON mail_backups_new(expires_at);
	-- (mail_backup_schedules is defined earlier in this block with the union schema;
	-- its indexes are also declared there to avoid ordering issues.)
`
	// stripFTS5 removes every CREATE VIRTUAL TABLE ... USING fts5() block and
	// every trigger that references mail_fts5_p7 from the schema SQL.  These
	// statements require the FTS5 compile-time module which is only present
	// when the binary is built with -tags sqlite_fts5.  Stripping them is
	// preferable to matching them exactly (which would be brittle against
	// differing leading whitespace between Phase 1-5 and Phase 6-8 sections).
	//
	// NOTE: we probe availability FIRST, strip only when the probe reports
	// false.  Modern builds of go-sqlite3 on macos ship the FTS5 loadable
	// extension compiled-in without needing the -tags sqlite_fts5 build tag,
	// so `FTS5Available()` can return true on an untagged binary.  Stripping
	// in that case would leave us searching a non-existent table.
	fts5OK := FTS5Available()
	if !fts5OK {
		baseSQL = stripFTS5(baseSQL)
	}
	_, err := s.db.ExecContext(ctx, baseSQL)
	if err != nil {
		return fmt.Errorf("mail migrate: create tables (base): %w", err)
	}
	if fts5OK {
		fts5SQL := `
	CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts USING fts5(
	  subject,
	  body,
	  sender_name,
	  sender_addr,
	  recipient_addr,
	  content='',
	  tokenize='unicode61 remove_diacritics 2'
	);
	CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts5 USING fts5(
	  subject,
	  body,
	  sender_name,
	  sender_addr,
	  recipient_addr,
	  content='mail_message_parts',
	  content_rowid='rowid',
	  tokenize='unicode61 remove_diacritics 2'
	);
	CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts5_p7 USING fts5(
	  subject,
	  from_list,
	  to_list,
	  body_text,
	  preview_text,
	  tokenize='unicode61 remove_diacritics 2'
	);
	CREATE TRIGGER IF NOT EXISTS mail_messages_p7_ai AFTER INSERT ON mail_messages_p7 BEGIN
	  INSERT INTO mail_fts5_p7(rowid, subject, from_list, to_list, body_text, preview_text)
	    VALUES (new.rowid, new.subject, new.from_list_csv, new.to_list_csv, new.body_text, new.preview_text);
	END;
	CREATE TRIGGER IF NOT EXISTS mail_messages_p7_au AFTER UPDATE ON mail_messages_p7 BEGIN
	  DELETE FROM mail_fts5_p7 WHERE rowid = old.rowid;
	  INSERT INTO mail_fts5_p7(rowid, subject, from_list, to_list, body_text, preview_text)
	    VALUES (new.rowid, new.subject, new.from_list_csv, new.to_list_csv, new.body_text, new.preview_text);
	END;
	CREATE TRIGGER IF NOT EXISTS mail_messages_p7_ad AFTER DELETE ON mail_messages_p7 BEGIN
	  DELETE FROM mail_fts5_p7 WHERE rowid = old.rowid;
	END;
	`
		if _, err := s.db.ExecContext(ctx, fts5SQL); err != nil {
			return fmt.Errorf("mail migrate: create fts5 tables/triggers: %w", err)
		}
	}

	// Additive-column evolution for mail_* tables.  Keep this list ordered
	// by the phase that introduced the column so git blame + bisection
	// stay readable.
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		// ---- Phase 4 (certmanager) ------------------------------
		// mail_certificates - Phase 4 spec columns added on top of
		// any pre-existing schema from earlier phases.
		{"mail_certificates", "domain", "TEXT NOT NULL DEFAULT ''"},
		{"mail_certificates", "pem_chain", "TEXT NOT NULL DEFAULT ''"},
		{"mail_certificates", "dns_provider_id", "TEXT NOT NULL DEFAULT ''"},
		{"mail_certificates", "last_renewal_attempt", "TEXT NOT NULL DEFAULT ''"},
		{"mail_certificates", "next_renewal", "TEXT NOT NULL DEFAULT ''"},
		{"mail_certificates", "last_error", "TEXT NOT NULL DEFAULT ''"},
		// mail_dns_providers - Phase 4 normalised schema fields.
		{"mail_dns_providers", "display_name", "TEXT NOT NULL DEFAULT ''"},
		{"mail_dns_providers", "config_json", "TEXT NOT NULL DEFAULT ''"},
		// mail_domains - TLSA toggle, DNS provider FK, SAN CSV, DANE TLSA overrides.
		{"mail_domains", "tlsa_enabled", "INTEGER NOT NULL DEFAULT 1"},
		{"mail_domains", "san_domains_csv", "TEXT NOT NULL DEFAULT ''"},
		{"mail_domains", "tlsa_domains_csv", "TEXT NOT NULL DEFAULT ''"},
		{"mail_domains", "tlsa_domains_wildcards", "INTEGER NOT NULL DEFAULT 0"},
		// ---- Phase 5 (accounts / aliases / import registrations) ----
		// mail_mox_settings - Phase 5 import + quota defaults.
		{"mail_mox_settings", "import_mode", "INTEGER DEFAULT 0"},
		{"mail_mox_settings", "import_registration_id", "TEXT"},
		{"mail_mox_settings", "import_read_only", "INTEGER DEFAULT 1"},
		{"mail_mox_settings", "default_account_quota_mb", "INTEGER DEFAULT 0"},
		{"mail_mox_settings", "max_accounts_per_domain", "INTEGER DEFAULT 0"},
		// mail_accounts - Phase 5 normalised columns.
		{"mail_accounts", "domain_id", "TEXT NOT NULL DEFAULT ''"},
		{"mail_accounts", "local_part", "TEXT NOT NULL DEFAULT ''"},
		{"mail_accounts", "address", "TEXT NOT NULL DEFAULT ''"},
		{"mail_accounts", "password_mode", "TEXT NOT NULL DEFAULT 'set'"},
		{"mail_accounts", "quota_mb", "INTEGER DEFAULT 0"},
		{"mail_accounts", "is_admin", "INTEGER DEFAULT 0"},
		{"mail_accounts", "imap_sync_enabled", "INTEGER DEFAULT 1"},
		{"mail_accounts", "imap_sync_state", "TEXT DEFAULT 'idle'"},
		{"mail_accounts", "imap_last_uidvalidity", "TEXT"},
		{"mail_accounts", "imap_last_uid", "TEXT"},
		{"mail_accounts", "imap_last_internaldate", "TEXT"},
		{"mail_accounts", "imap_error", "TEXT"},
		{"mail_accounts", "status", "TEXT NOT NULL DEFAULT 'active'"},
		{"mail_accounts", "last_login_at", "TEXT"},
		// mail_accounts - Phase 7 IMAP sync upstream connection details.
		{"mail_accounts", "imap_host", "TEXT"},
		{"mail_accounts", "imap_username", "TEXT"},
		{"mail_accounts", "imap_sync_max_size_bytes", "INTEGER DEFAULT 0"},
		{"mail_accounts", "webapi_password_wrapped", "TEXT NOT NULL DEFAULT ''"},
		{"mail_messages_p7", "mox_msg_id", "INTEGER NOT NULL DEFAULT 0"},
		// mail_aliases - Phase 5 normalised columns.
		{"mail_aliases", "domain_id", "TEXT NOT NULL DEFAULT ''"},
		{"mail_aliases", "source", "TEXT NOT NULL DEFAULT ''"},
		{"mail_aliases", "recipients_csv", "TEXT NOT NULL DEFAULT ''"},
		{"mail_aliases", "list_name", "TEXT"},
		{"mail_aliases", "list_reply_to", "TEXT"},
		// ---- Phase 6 (webhooks / delivery / rate / DNSBL) ----
		// mail_mox_settings - Phase 6 webhook + delivery + DNSBL toggles.
		{"mail_mox_settings", "webhook_inbound_enabled", "INTEGER DEFAULT 0"},
		{"mail_mox_settings", "default_webhook_id", "TEXT"},
		{"mail_mox_settings", "delivery_retention_days", "INTEGER DEFAULT 90"},
		{"mail_mox_settings", "suppression_auto_prune_days", "INTEGER DEFAULT 180"},
		{"mail_mox_settings", "outbound_rate_default_scope", "TEXT DEFAULT 'global'"},
		{"mail_mox_settings", "dnsbl_check_enabled", "INTEGER DEFAULT 1"},
		{"mail_mox_settings", "dnsbl_sources_csv", "TEXT DEFAULT 'zen.spamhaus.org,bl.spamcop.net,dbl.spamhaus.org,combined.mail-abuse.org,uribl.swinog.ch'"},
		{"mail_delivery_events", "queue_msg_id", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_delivery_events", "from_id", "TEXT NOT NULL DEFAULT ''"},
		// ---- Phase 7 (IMAP sync / FTS5 search) ----
		// mail_mox_settings - Phase 7 IMAP sync toggles + size limits.
		{"mail_mox_settings", "imapsync_enabled", "INTEGER DEFAULT 1"},
		{"mail_mox_settings", "imapsync_max_size_bytes", "INTEGER DEFAULT 10737418240"},
		{"mail_mox_settings", "imapsync_big_message_size_limit_bytes", "INTEGER DEFAULT 52428800"},
		{"mail_mox_settings", "imapsync_interval_attachment_cache_enabled", "INTEGER DEFAULT 1"},
		// ---- Phase 7 (revised IMAP sync toggles + size limits + idle) ----
		{"mail_mox_settings", "imapsync_max_total_bytes", "INTEGER DEFAULT 10737418240"},
		{"mail_mox_settings", "imapsync_big_message_limit_bytes", "INTEGER DEFAULT 52428800"},
		{"mail_mox_settings", "imapsync_attachment_cache_enabled", "INTEGER DEFAULT 1"},
		{"mail_mox_settings", "imapsync_idle_timeout_seconds", "INTEGER DEFAULT 1800"},
		// ---- Phase 8 (backups / retention / hard-delete) ----
		// mail_backups - columns added on top of the legacy schema.
		{"mail_backups", "scope", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backups", "schedule_id", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backups", "retention_days", "INTEGER DEFAULT 0"},
		{"mail_backups", "expires_at", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backups", "note", "TEXT NOT NULL DEFAULT ''"},
		// mail_mox_settings - Phase 8 backup + retention defaults.
		{"mail_mox_settings", "backup_enabled", "INTEGER DEFAULT 0"},
		{"mail_mox_settings", "backup_default_scope", "TEXT DEFAULT 'config'"},
		{"mail_mox_settings", "backup_default_retention_days", "INTEGER DEFAULT 30"},
		{"mail_mox_settings", "retention_auto_apply_enabled", "INTEGER DEFAULT 1"},
		{"mail_mox_settings", "danger_hard_delete_enabled", "INTEGER DEFAULT 0"},
		// ---- Phase 8 Part A: cross-grade mail_backup_schedules (P8B minimal -> full) ----
		{"mail_backup_schedules", "scope_id", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "schedule_kind", "TEXT NOT NULL DEFAULT 'full'"},
		{"mail_backup_schedules", "cadence_cron", "TEXT NOT NULL DEFAULT '0 3 * * 0'"},
		{"mail_backup_schedules", "timezone", "TEXT NOT NULL DEFAULT 'UTC'"},
		{"mail_backup_schedules", "keep_revisions", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_backup_schedules", "contains_config", "INTEGER NOT NULL DEFAULT 1"},
		{"mail_backup_schedules", "contains_data", "INTEGER NOT NULL DEFAULT 1"},
		{"mail_backup_schedules", "encryption_mode", "TEXT NOT NULL DEFAULT 'none'"},
		{"mail_backup_schedules", "encrypt_password_hash", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "storage_target", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "target_url", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "target_credentials_json", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "pre_run_hook", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "post_run_hook", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "last_status", "TEXT NOT NULL DEFAULT ''"},
		{"mail_backup_schedules", "paused", "INTEGER NOT NULL DEFAULT 0"},
		// ---- Phase 8 Part A: cross-grade mail_retention_rules scope/category fields ----
		{"mail_retention_rules", "scope", "TEXT NOT NULL DEFAULT 'global'"},
		{"mail_retention_rules", "scope_id", "TEXT NOT NULL DEFAULT ''"},
		{"mail_retention_rules", "category", "TEXT NOT NULL DEFAULT 'all'"},
		{"mail_retention_rules", "prune_empty_folders", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_retention_rules", "hard_delete", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_retention_rules", "last_pruned_at", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, column.table, column.name, column.def); err != nil {
			return err
		}
	}

	return nil
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
	settings.Addr = strings.TrimSpace(settings.Addr)
	settings.TLSCertFile = strings.TrimSpace(settings.TLSCertFile)
	settings.TLSKeyFile = strings.TrimSpace(settings.TLSKeyFile)
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
		"allowed_roots":             string(roots),
		"cookie_secure":             boolString(defaults.CookieSecure),
		"http_tls_enabled":          boolString(defaults.TLSEnabled),
		"http_tls_cert_file":        defaults.TLSCertFile,
		"http_tls_key_file":         defaults.TLSKeyFile,
		"http_tls_owner_uid_check":  boolString(defaults.TLSOwnerUIDCheck || true),
		"http_hsts_enabled":         boolString(defaults.HSTSEnabled),
		"http_hsts_max_age_seconds": strconv.Itoa(defaults.HSTSMaxAgeSeconds),
	}
	if defaults.Addr != "" {
		values["http_addr"] = defaults.Addr
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
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, updated_at FROM settings WHERE key IN (
		'allowed_roots', 'cookie_secure', 'http_addr',
		'http_tls_enabled', 'http_tls_cert_file', 'http_tls_key_file',
		'http_tls_owner_uid_check', 'http_hsts_enabled', 'http_hsts_max_age_seconds'
	)`)
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
		case "http_addr":
			settings.Addr = value
		case "http_tls_enabled":
			settings.TLSEnabled = value == "true" || value == "1"
		case "http_tls_cert_file":
			settings.TLSCertFile = value
		case "http_tls_key_file":
			settings.TLSKeyFile = value
		case "http_tls_owner_uid_check":
			settings.TLSOwnerUIDCheck = !(value == "false" || value == "0")
		case "http_hsts_enabled":
			settings.HSTSEnabled = value == "true" || value == "1"
		case "http_hsts_max_age_seconds":
			if n, e := strconv.Atoi(value); e == nil {
				settings.HSTSMaxAgeSeconds = n
			}
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
	if settings.Addr != "" {
		_, portStr, err := net.SplitHostPort(settings.Addr)
		if err != nil {
			return fmt.Errorf("invalid listen address %q: %w", settings.Addr, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid listen port %q: must be 1-65535", portStr)
		}
	}
	if settings.TLSEnabled {
		if settings.TLSCertFile == "" || settings.TLSKeyFile == "" {
			return errors.New("tls enabled but cert file or key file is empty")
		}
	}
	if settings.HSTSEnabled && settings.HSTSMaxAgeSeconds < 0 {
		return errors.New("hsts max age must be >= 0")
	}
	roots, _ := json.Marshal(settings.AllowedRoots)
	now := now()
	values := map[string]string{
		"allowed_roots":             string(roots),
		"cookie_secure":             boolString(settings.CookieSecure),
		"http_tls_enabled":          boolString(settings.TLSEnabled),
		"http_tls_cert_file":        settings.TLSCertFile,
		"http_tls_key_file":         settings.TLSKeyFile,
		"http_tls_owner_uid_check":  boolString(settings.TLSOwnerUIDCheck),
		"http_hsts_enabled":         boolString(settings.HSTSEnabled),
		"http_hsts_max_age_seconds": strconv.Itoa(settings.HSTSMaxAgeSeconds),
	}
	if settings.Addr != "" {
		values["http_addr"] = settings.Addr
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

// InterruptStaleSystemUpdateJobs marks any pre-restart in-flight update job as
// failed with the supplied message. It is called at startup so a process crash
// during download/verify/install cannot leave a "queued"/"running" job that
// blocks all future Apply() calls indefinitely. "restarting" jobs are left for
// selfupdate.ConfirmBoot so the boot-confirmation path can record exact
// version-mismatch / rollback diagnostics.
func (s *Store) InterruptStaleSystemUpdateJobs(ctx context.Context, message string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM system_update_jobs WHERE status IN ('queued', 'running') ORDER BY created_at ASC`)
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
	if strings.TrimSpace(message) == "" {
		message = "服务启动时发现遗留更新任务，已置为失败"
	}
	_, err = s.db.ExecContext(ctx, `UPDATE system_update_jobs SET status = 'failed', error_message = ?, completed_at = ? WHERE status IN ('queued', 'running')`, message, now())
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) BackupDatabase(ctx context.Context, path string) error {
	return s.BackupDatabaseWithProgress(ctx, path, nil, 0, 0)
}

func (s *Store) DBPath() string { return s.dbPath }

func (s *Store) DatabaseSizeBytes() (int64, error) {
	if s.dbPath == "" || s.dbPath == ":memory:" {
		return 0, nil
	}
	info, err := os.Stat(s.dbPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DatabaseStatsCacheTTL is how long cached stats are considered fresh
// before a reader will trigger a refresh. The background collector
// runs on its own cadence; this TTL only affects on-demand reads.
const DatabaseStatsCacheTTL = 90 * time.Second

// DatabaseStatsFreshnessGoal is the target interval for the background
// stats collector. Stats are approximately this fresh.
const DatabaseStatsFreshnessGoal = 60 * time.Second

// DatabaseTableStat describes size and metadata for one table.
type DatabaseTableStat struct {
	Name            string `json:"name"`
	SizeBytes       int64  `json:"sizeBytes"`
	IndexSizeBytes  int64  `json:"indexSizeBytes,omitempty"`
	PageCount       int    `json:"pageCount,omitempty"`
	Description     string `json:"description,omitempty"`
}

// DatabaseStats is a point-in-time snapshot of database size distribution.
type DatabaseStats struct {
	TotalBytes int64               `json:"totalBytes"`
	Tables     []DatabaseTableStat `json:"tables"`
	UpdatedAt  string              `json:"updatedAt"`
}

// tableDescriptionMap maps well-known table names to human-readable
// Chinese descriptions shown in the dashboard tooltip.
//
// Tables not listed here will get a module-level generic description
// inferred from their prefix.
var tableDescriptionMap = map[string]string{
	// Core system
	"owner_account": "管理员账户（登录认证）",
	"web_sessions":  "Web 会话 / Cookie",
	"audit_events":  "审计日志（操作记录）",
	"settings":      "系统设置（KV 存储）",
	"events":        "事件流（SSE 推送持久化）",

	// System update
	"system_update_checks": "系统更新检查记录",
	"system_update_jobs":   "系统更新任务",

	// Codex Gateway
	"codex_gateway_settings":      "Codex Gateway 全局设置",
	"codex_gateway_accounts":      "上游 LLM 账户",
	"codex_gateway_api_keys":      "API Key（下游鉴权）",
	"codex_gateway_models":        "模型配置",
	"codex_gateway_model_plans":   "模型套餐 / 价格方案",
	"codex_gateway_request_logs":  "API 请求日志",
	"codex_gateway_usage_records": "用量计费记录",

	// Codex CLI
	"codex_cli_installations": "Codex CLI 安装信息",
	"codex_cli_workspaces":    "工作区",
	"codex_cli_threads":       "对话线程",
	"codex_cli_turns":         "对话回合",
	"codex_cli_events":        "事件记录",
	"codex_cli_attachments":   "附件",
	"codex_cli_approvals":     "工具调用审批",
	"codex_cli_runs":          "代码执行运行",
	"codex_cli_commands":      "命令历史",
	"codex_cli_notifications": "通知",
	"codex_cli_automations":   "自动化配置",
	"codex_cli_automation_runs": "自动化运行记录",

	// Images / Media
	"image_provider_settings": "图片生成提供商设置",
	"image_generation_jobs":   "图片生成任务",
	"image_generation_sources": "图片生成源文件",
	"image_generation_outputs": "图片生成输出",
	"image_assets":            "图片素材库",
	"image_storage_settings":  "图片存储设置",
	"image_prompt_library":    "提示词库",
	"media_provider_settings": "媒体 / 视频生成设置",
	"media_generation_jobs":   "媒体生成任务",
	"media_generation_outputs": "媒体生成输出",
	"media_assets":            "媒体素材库",
	"object_storage_profiles": "对象存储配置",

	// Mail
	"mail_mox_settings":   "Mox 邮件服务全局设置",
	"mail_domains":        "邮件域名",
	"mail_accounts":       "邮箱账户",
	"mail_aliases":        "邮件别名",
	"mail_addresses":      "地址簿 / 联系人",
	"mail_certificates":   "TLS 证书",
	"mail_dns_providers":  "DNS 提供商配置",
	"mail_messages_p7":    "邮件消息（正文+元数据）",
	"mail_message_parts":  "邮件内容片段",
	"mail_folders_p7":     "邮件文件夹",
	"mail_index_health_p7": "邮件索引健康状态",
	"mail_fts5_p7":        "邮件全文搜索索引",
	"mail_webhooks":       "Webhook 配置",
	"mail_webhook_events": "Webhook 事件日志",
	"mail_delivery_events": "投递事件",
	"mail_queue_entries":  "投递队列条目",
	"mail_drafts":         "邮件草稿",
	"mail_backups":        "邮件备份记录",
	"mail_backup_schedules": "邮件备份计划",
	"mail_retention_rules": "邮件保留规则",
	"mail_mox_health_checks": "Mox 健康检查记录",
	"mail_runtime_state":  "运行时状态",

	// V2Ray
	"v2ray_settings":         "V2Ray 全局设置",
	"v2ray_remote_clients":   "远程客户端",
	"v2ray_config_versions":  "配置版本历史",

	// Docker
	"docker_registry_credentials": "Docker Registry 凭据",
	"docker_registry_repositories": "Docker 仓库",
	"docker_registry_manifests":   "Docker 镜像清单",
	"docker_registry_tags":        "Docker 标签",

	// Stock
	"stock_portfolios":     "投资组合",
	"stock_holdings":       "持仓",
	"stock_quotes":         "股票报价",
	"stock_opportunities":  "投资机会",
	"stock_strategies":     "策略",
	"stock_watches":        "监控 / 观察列表",
	"stock_alerts":         "告警",
	"stock_reviews":        "AI 评审",
	"stock_trade_signals":  "交易信号",
	"stock_operations":     "操作记录",
	"stock_memories":       "笔记 / 记忆",
	"stock_instruments":    "股票标的基础信息",
	"stock_market_data_points": "行情数据点",
	"stock_news_items":     "新闻条目",
	"stock_data_sources":   "数据源",
	"stock_data_tasks":     "数据任务",
	"stock_agent_runs":     "AI 代理运行记录",
	"stock_agent_run_steps": "AI 代理运行步骤",
}

// describeTable returns a human-readable description for a table,
// or a module-level fallback if the table isn't in the map.
func describeTable(name string) string {
	if desc, ok := tableDescriptionMap[name]; ok {
		return desc
	}
	switch {
	case strings.HasPrefix(name, "codex_gateway_"):
		return "Codex Gateway 模块表"
	case strings.HasPrefix(name, "codex_cli_"):
		return "Codex CLI 模块表"
	case strings.HasPrefix(name, "mail_"):
		return "邮件模块表"
	case strings.HasPrefix(name, "stock_"):
		return "股票模块表"
	case strings.HasPrefix(name, "image_") || strings.HasPrefix(name, "media_") || strings.HasPrefix(name, "object_storage_"):
		return "多媒体模块表"
	case strings.HasPrefix(name, "v2ray_"):
		return "V2Ray 模块表"
	case strings.HasPrefix(name, "docker_"):
		return "Docker 模块表"
	case strings.HasPrefix(name, "system_"):
		return "系统模块表"
	case strings.HasPrefix(name, "__") || strings.HasPrefix(name, "sqlite_"):
		return "SQLite 内部表"
	}
	return "数据表"
}

// DatabaseStats returns cached database statistics (total size + per-table
// breakdown). If the cache is older than DatabaseStatsCacheTTL it triggers
// a synchronous refresh.
func (s *Store) DatabaseStats(ctx context.Context) (DatabaseStats, error) {
	s.dbStatsMu.RLock()
	cached := s.dbStatsCache
	at := s.dbStatsAt
	s.dbStatsMu.RUnlock()
	if !at.IsZero() && time.Since(at) < DatabaseStatsCacheTTL {
		return cached, nil
	}
	return s.refreshDatabaseStats(ctx)
}

// refreshDatabaseStats recomputes stats and updates the cache.
func (s *Store) refreshDatabaseStats(ctx context.Context) (DatabaseStats, error) {
	stats, err := s.computeDatabaseStats(ctx)
	if err != nil {
		return DatabaseStats{}, err
	}
	s.dbStatsMu.Lock()
	s.dbStatsCache = stats
	s.dbStatsAt = time.Now()
	s.dbStatsMu.Unlock()
	return stats, nil
}

// computeDatabaseStats calculates per-table sizes.
//
// Strategy:
//   - Total size comes from the file system (os.Stat), which is accurate.
//   - Per-table sizes are estimated by walking every user table, counting
//     rows, and multiplying by an estimated row size derived from column
//     types.  Index overhead is approximated with a per-table multiplier.
//   - Estimated table sizes are then normalized so their sum equals the
//     total database size (minus a small fixed overhead for SQLite
//     internals).  This keeps the pie chart visually consistent with the
//     file size the user sees elsewhere.
//
// This approach avoids requiring the dbstat virtual table (which isn't
// compiled into the stock go-sqlite3 build) and stays fast enough to run
// once per minute even on multi-hundred-MB databases.
func (s *Store) computeDatabaseStats(ctx context.Context) (DatabaseStats, error) {
	totalBytes, err := s.DatabaseSizeBytes()
	if err != nil {
		return DatabaseStats{}, fmt.Errorf("get db file size: %w", err)
	}
	if s.dbPath == ":memory:" || totalBytes == 0 {
		return DatabaseStats{
			TotalBytes: totalBytes,
			Tables:     []DatabaseTableStat{},
			UpdatedAt:  now(),
		}, nil
	}

	// Use a separate, read-only connection so the stats query doesn't
	// contend with the single main-pool connection during writes.
	statDB, err := sql.Open("sqlite3", s.dbPath+"?mode=ro")
	if err != nil {
		// Fallback: just return total size with empty table list.
		return DatabaseStats{
			TotalBytes: totalBytes,
			Tables:     []DatabaseTableStat{},
			UpdatedAt:  now(),
		}, nil
	}
	defer statDB.Close()
	statDB.SetMaxOpenConns(1)
	_, _ = statDB.ExecContext(ctx, `PRAGMA busy_timeout = 500`)

	// 1. List all user tables (exclude internal / FTS / virtual tables).
	// Note: underscore in LIKE is a single-char wildcard, so we must
	// escape literal underscores.
	rows, err := statDB.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		  AND name NOT LIKE '%\_fts%' ESCAPE '\'
		  AND name NOT LIKE 'sqlite\_stat%' ESCAPE '\'
		  AND name NOT LIKE '\_\_%' ESCAPE '\'
		ORDER BY name
	`)
	if err != nil {
		return DatabaseStats{
			TotalBytes: totalBytes,
			Tables:     []DatabaseTableStat{},
			UpdatedAt:  now(),
		}, nil
	}
	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tableNames = append(tableNames, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DatabaseStats{
			TotalBytes: totalBytes,
			Tables:     []DatabaseTableStat{},
			UpdatedAt:  now(),
		}, nil
	}

	// 2. For each table, estimate row size from column types + row count.
	type tableEstimate struct {
		name        string
		rowCount    int64
		estRowBytes int
	}
	estimates := make([]tableEstimate, 0, len(tableNames))
	var totalEstimated int64

	for _, name := range tableNames {
		// Get column info to estimate row size.
		colRows, err := statDB.QueryContext(ctx, "PRAGMA table_info("+quoteIdentifier(name)+")")
		if err != nil {
			continue
		}
		var rowSizeEst int
		for colRows.Next() {
			var (
				cid       int
				colName   string
				colType   string
				notNull   int
				dfltValue sql.NullString
				pk        int
			)
			if err := colRows.Scan(&cid, &colName, &colType, &notNull, &dfltValue, &pk); err != nil {
				continue
			}
			rowSizeEst += estimateColumnBytes(colType, colName)
		}
		colRows.Close()
		if rowSizeEst == 0 {
			rowSizeEst = 64 // conservative fallback
		}
		// Minimum 4 bytes per row (page overhead is captured separately).
		if rowSizeEst < 4 {
			rowSizeEst = 4
		}

		// Count rows — best-effort, skip if too slow.
		var count int64
		countRow := statDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdentifier(name))
		if err := countRow.Scan(&count); err != nil {
			count = 0
		}
		if count < 0 {
			count = 0
		}

		estimates = append(estimates, tableEstimate{
			name:        name,
			rowCount:    count,
			estRowBytes: rowSizeEst,
		})
		totalEstimated += count * int64(rowSizeEst)
	}

	// 3. Count indexes per table to estimate index overhead.
	indexCount := make(map[string]int)
	for _, name := range tableNames {
		idxRows, err := statDB.QueryContext(ctx, "PRAGMA index_list("+quoteIdentifier(name)+")")
		if err != nil {
			continue
		}
		n := 0
		for idxRows.Next() {
			n++
		}
		idxRows.Close()
		indexCount[name] = n
	}

	// 4. Allocate total bytes across tables proportional to estimated size.
	//    Reserve ~8% for SQLite page overhead / internal tables / free pages.
	const overheadFraction = 0.08
	allocatable := int64(float64(totalBytes) * (1.0 - overheadFraction))
	if allocatable <= 0 {
		allocatable = totalBytes
	}

	tables := make([]DatabaseTableStat, 0, len(estimates))
	for _, est := range estimates {
		var sizeBytes int64
		if totalEstimated > 0 && est.rowCount > 0 {
			share := float64(est.rowCount*int64(est.estRowBytes)) / float64(totalEstimated)
			sizeBytes = int64(share * float64(allocatable))
		} else {
			sizeBytes = 0
		}
		// Allocate ~30% of table size to indexes as a rough estimate,
		// scaled by the number of indexes.
		idxCount := indexCount[est.name]
		var indexBytes int64
		if idxCount > 0 && sizeBytes > 0 {
			// 1 index ≈ 25% of table size; 2+ indexes ~45%.
			if idxCount == 1 {
				indexBytes = int64(float64(sizeBytes) * 0.25)
			} else if idxCount == 2 {
				indexBytes = int64(float64(sizeBytes) * 0.40)
			} else {
				indexBytes = int64(float64(sizeBytes) * 0.55)
			}
		}
		pageCount := 0
		if sizeBytes > 0 {
			pageCount = int(sizeBytes / 4096) // assume default 4k page
		}
		tables = append(tables, DatabaseTableStat{
			Name:           est.name,
			SizeBytes:      sizeBytes,
			IndexSizeBytes: indexBytes,
			PageCount:      pageCount,
			Description:    describeTable(est.name),
		})
	}

	sortTableStats(tables)

	return DatabaseStats{
		TotalBytes: totalBytes,
		Tables:     tables,
		UpdatedAt:  now(),
	}, nil
}

// estimateColumnBytes returns a rough estimate of bytes per row for a
// column, based on its declared type and name.  These are intentionally
// conservative — the results are normalized against the real file size
// before display, so absolute accuracy doesn't matter, only relative
// sizing between tables.
func estimateColumnBytes(colType, colName string) int {
	upper := strings.ToUpper(strings.TrimSpace(colType))
	switch {
	case strings.Contains(upper, "INT") || strings.Contains(upper, "INTEGER") || strings.Contains(upper, "BIGINT"):
		return 8
	case strings.Contains(upper, "REAL") || strings.Contains(upper, "FLOAT") || strings.Contains(upper, "DOUBLE"):
		return 8
	case strings.Contains(upper, "BOOL") || strings.Contains(upper, "BOOLEAN") || strings.Contains(upper, "TINYINT"):
		return 1
	case strings.Contains(upper, "BLOB") || strings.Contains(upper, "BINARY"):
		return 256 // blobs vary wildly; assume moderate
	case strings.Contains(upper, "TEXT") || strings.Contains(upper, "VARCHAR") || strings.Contains(upper, "CHAR"):
		// Guesses based on common column names.
		name := strings.ToLower(colName)
		switch {
		case strings.Contains(name, "payload") || strings.Contains(name, "body") || strings.Contains(name, "content") || strings.Contains(name, "summary"):
			return 512
		case strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "hash") || strings.Contains(name, "password"):
			return 128
		case strings.Contains(name, "id") || name == "key" || strings.Contains(name, "type") || strings.Contains(name, "status") || strings.Contains(name, "state"):
			return 24
		case strings.Contains(name, "url") || strings.Contains(name, "path") || strings.Contains(name, "email") || strings.Contains(name, "name") || strings.Contains(name, "title"):
			return 64
		case strings.Contains(name, "json") || strings.Contains(name, "data") || strings.Contains(name, "config") || strings.Contains(name, "settings"):
			return 256
		default:
			return 32
		}
	case strings.Contains(upper, "DATETIME") || strings.Contains(upper, "DATE") || strings.Contains(upper, "TIME"):
		return 24
	case strings.Contains(upper, "DECIMAL") || strings.Contains(upper, "NUMERIC"):
		return 12
	default:
		return 24
	}
}

// quoteIdentifier wraps an identifier in double quotes for safe use in
// dynamic SQL (table names etc.).  It doubles any embedded double quotes.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func sortTableStats(tables []DatabaseTableStat) {
	for i := 1; i < len(tables); i++ {
		key := tables[i]
		keyTotal := key.SizeBytes + key.IndexSizeBytes
		j := i - 1
		for j >= 0 {
			jTotal := tables[j].SizeBytes + tables[j].IndexSizeBytes
			if jTotal >= keyTotal {
				break
			}
			tables[j+1] = tables[j]
			j--
		}
		tables[j+1] = key
	}
}

// StartStatsCollector starts a background goroutine that periodically
// refreshes the database size / table stats cache. It runs until ctx is
// cancelled. The first refresh happens immediately.
//
// Stats are best-effort: if the DB is busy, the refresh is skipped and
// retried on the next tick.
func (s *Store) StartStatsCollector(ctx context.Context) {
	go s.statsCollectorLoop(ctx)
}

func (s *Store) statsCollectorLoop(ctx context.Context) {
	// First refresh shortly after startup.
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return
	}
	s.refreshDatabaseStats(ctx)

	ticker := time.NewTicker(DatabaseStatsFreshnessGoal)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Use a short timeout so we don't stack up if the DB is busy.
			refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = s.refreshDatabaseStats(refreshCtx)
			cancel()
		}
	}
}

type BackupProgress func(remaining, pageCount int)

func (s *Store) BackupDatabaseWithProgress(ctx context.Context, path string, progress BackupProgress, stepsPerTick int, stepSleep time.Duration) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("backup path is required")
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup path already exists: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if s.dbPath == "" || s.dbPath == ":memory:" {
		_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
		return err
	}
	tmpPath := path + ".tmp"
	_ = os.Remove(tmpPath)
	if err := s.backupDatabaseOnline(ctx, tmpPath, progress, stepsPerTick, stepSleep); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return syncDir(filepath.Dir(path))
}

func (s *Store) backupDatabaseOnline(ctx context.Context, path string, progress BackupProgress, stepsPerTick int, stepSleep time.Duration) error {
	if stepsPerTick <= 0 {
		stepsPerTick = 64
	}
	sourceDB, err := sql.Open("sqlite3", s.dbPath)
	if err != nil {
		return err
	}
	defer sourceDB.Close()
	sourceDB.SetMaxOpenConns(1)
	destDB, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer destDB.Close()
	destDB.SetMaxOpenConns(1)
	if err := sourceDB.PingContext(ctx); err != nil {
		return err
	}
	if err := destDB.PingContext(ctx); err != nil {
		return err
	}
	_, _ = sourceDB.ExecContext(ctx, `PRAGMA busy_timeout = 1000`)
	_, _ = destDB.ExecContext(ctx, `PRAGMA busy_timeout = 1000`)
	sourceConn, err := sourceDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer sourceConn.Close()
	destConn, err := destDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer destConn.Close()
	err = destConn.Raw(func(dest any) error {
		destSQLite, ok := dest.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("backup destination is not a sqlite connection")
		}
		return sourceConn.Raw(func(source any) error {
			sourceSQLite, ok := source.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("backup source is not a sqlite connection")
			}
			backup, err := destSQLite.Backup("main", sourceSQLite, "main")
			if err != nil {
				return err
			}
			for {
				if err := ctx.Err(); err != nil {
					_ = backup.Finish()
					return err
				}
				done, stepErr := backup.Step(stepsPerTick)
				if progress != nil {
					progress(backup.Remaining(), backup.PageCount())
				}
				if stepErr != nil {
					_ = backup.Finish()
					return stepErr
				}
				if done {
					return backup.Finish()
				}
				if stepSleep > 0 {
					select {
					case <-ctx.Done():
						_ = backup.Finish()
						return ctx.Err()
					case <-time.After(stepSleep):
					}
				} else {
					select {
					case <-ctx.Done():
						_ = backup.Finish()
						return ctx.Err()
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
		})
	})
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func DefaultCodexGatewaySettings() CodexGatewaySettings {
	return CodexGatewaySettings{
		ID:                                "default",
		BaseURL:                           "https://chatgpt.com/backend-api",
		OAuthAuthURL:                      "https://auth.openai.com/oauth/authorize",
		OAuthTokenURL:                     "https://auth.openai.com/oauth/token",
		OAuthClientID:                     "app_EMoamEEZ73f0CkXaXp7hrann",
		OAuthRedirectURI:                  "http://localhost:1455/auth/callback",
		RequestTimeoutSeconds:             600,
		RefreshMarginSeconds:              300,
		AccountHealthCheckIntervalSeconds: 43200,
		DefaultInstructions:               "You are a helpful assistant.",
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
	if settings.OAuthClientID == "" {
		settings.OAuthClientID = defaults.OAuthClientID
	}
	settings.OAuthRedirectURI = strings.TrimSpace(settings.OAuthRedirectURI)
	if settings.OAuthRedirectURI == "" {
		settings.OAuthRedirectURI = defaults.OAuthRedirectURI
	}
	if settings.RequestTimeoutSeconds <= 0 {
		settings.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if settings.RefreshMarginSeconds < 0 {
		settings.RefreshMarginSeconds = defaults.RefreshMarginSeconds
	}
	const maxHealthInterval = 30 * 24 * 3600
	if settings.AccountHealthCheckIntervalSeconds < 0 {
		settings.AccountHealthCheckIntervalSeconds = defaults.AccountHealthCheckIntervalSeconds
	} else if settings.AccountHealthCheckIntervalSeconds > maxHealthInterval {
		settings.AccountHealthCheckIntervalSeconds = maxHealthInterval
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
  id, enabled, base_url, oauth_auth_url, oauth_token_url, oauth_client_id, oauth_redirect_uri, request_timeout_seconds, refresh_margin_seconds, account_health_check_interval_seconds, default_instructions, installation_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.ID, boolInt(defaults.Enabled), defaults.BaseURL, defaults.OAuthAuthURL, defaults.OAuthTokenURL, defaults.OAuthClientID, defaults.OAuthRedirectURI, defaults.RequestTimeoutSeconds, defaults.RefreshMarginSeconds, defaults.AccountHealthCheckIntervalSeconds, defaults.DefaultInstructions, defaults.InstallationID, now, now)
	return err
}

func (s *Store) GetCodexGatewaySettings(ctx context.Context) (CodexGatewaySettings, error) {
	if err := s.EnsureCodexGatewaySettings(ctx); err != nil {
		return CodexGatewaySettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, enabled, base_url, oauth_auth_url, oauth_token_url, oauth_client_id, oauth_redirect_uri, request_timeout_seconds, refresh_margin_seconds, account_health_check_interval_seconds, default_instructions, installation_id, created_at, updated_at FROM codex_gateway_settings WHERE id = 'default'`)
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
  id, enabled, base_url, oauth_auth_url, oauth_token_url, oauth_client_id, oauth_redirect_uri, request_timeout_seconds, refresh_margin_seconds, account_health_check_interval_seconds, default_instructions, installation_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  enabled = excluded.enabled,
  base_url = excluded.base_url,
  oauth_auth_url = excluded.oauth_auth_url,
  oauth_token_url = excluded.oauth_token_url,
  oauth_client_id = excluded.oauth_client_id,
  oauth_redirect_uri = excluded.oauth_redirect_uri,
  request_timeout_seconds = excluded.request_timeout_seconds,
  refresh_margin_seconds = excluded.refresh_margin_seconds,
  account_health_check_interval_seconds = excluded.account_health_check_interval_seconds,
  default_instructions = excluded.default_instructions,
  installation_id = excluded.installation_id,
  updated_at = excluded.updated_at`,
		settings.ID, boolInt(settings.Enabled), settings.BaseURL, settings.OAuthAuthURL, settings.OAuthTokenURL, settings.OAuthClientID, settings.OAuthRedirectURI, settings.RequestTimeoutSeconds, settings.RefreshMarginSeconds, settings.AccountHealthCheckIntervalSeconds, settings.DefaultInstructions, settings.InstallationID, settings.CreatedAt, settings.UpdatedAt)
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
	accessBlob, err := s.wrapGWToken(input.AccessToken)
	if err != nil {
		return CodexGatewayAccount{}, fmt.Errorf("wrap access token: %w", err)
	}
	refreshBlob, err := s.wrapGWToken(input.RefreshToken)
	if err != nil {
		return CodexGatewayAccount{}, fmt.Errorf("wrap refresh token: %w", err)
	}
	id, err := ids.New("cgacct")
	if err != nil {
		return CodexGatewayAccount{}, err
	}
	now := now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_gateway_accounts (id, label, status, access_token_secret, refresh_token_secret, expires_at, plan, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.Label, input.Status, accessBlob, refreshBlob, input.ExpiresAt, input.Plan, now, now)
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
	var accessBlob, refreshBlob string
	err := s.db.QueryRowContext(ctx, `
SELECT id, label, status, expires_at, plan, last_used_at, last_checked_at, last_error,
  CASE WHEN access_token_secret != '' THEN 1 ELSE 0 END,
  CASE WHEN refresh_token_secret != '' THEN 1 ELSE 0 END,
  created_at, updated_at, access_token_secret, refresh_token_secret
FROM codex_gateway_accounts
WHERE id = ?`, id).Scan(&secret.ID, &secret.Label, &secret.Status, &secret.ExpiresAt, &secret.Plan, &secret.LastUsedAt, &secret.LastCheckedAt, &secret.LastError, &hasAccess, &hasRefresh, &secret.CreatedAt, &secret.UpdatedAt, &accessBlob, &refreshBlob)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexGatewayAccountSecret{}, ErrNotFound
	}
	if err != nil {
		return CodexGatewayAccountSecret{}, err
	}
	secret.HasAccessToken = hasAccess == 1
	secret.HasRefreshToken = hasRefresh == 1

	// Unwrap, split by kw1: prefix.
	//
	// Safe behaviour matrix (fully determined by the kw1: prefix alone
	// (see unwrapGWToken docstring for the precise rules):
	//
	//   * no prefix → legacy plaintext (return verbatim, transparent
	//     stored before keywrap was introduced). Next
	//     UpdateCodexGatewayAccount will re-wrap with kw1: prefix).
	//   * kw1: prefix + unwrap ok → ciphertext, return plaintext.
	//   * kw1: prefix + unwrap failed → master key mismatch or corrupted
	//     ciphertext → ErrCorruptSecrets; caller should surface this
	//     explicitly rather than sending garbage upstream.
	if accessBlob != "" {
		pt, uerr := s.unwrapGWToken(accessBlob)
		switch {
		case uerr == nil:
			secret.AccessToken = pt
		case strings.HasPrefix(accessBlob, wrappedTokenPrefix):
			return CodexGatewayAccountSecret{}, fmt.Errorf("%w: access token for account %s (%s)", ErrCorruptSecrets, secret.ID, uerr)
		default:
			// Legacy raw secret (no prefix),  // Legacy prefix);  // legacy  // no prefix = legacy raw secret.
			secret.AccessToken = accessBlob
			if s.log != nil {
				s.log.Debug("storage: codex gateway access token read as legacy plaintext; will re-wrap with kw1: prefix on next update", "account_id", secret.ID)
			}
		}
	}
	if refreshBlob != "" {
		pt, uerr := s.unwrapGWToken(refreshBlob)
		switch {
		case uerr == nil:
			secret.RefreshToken = pt
		case strings.HasPrefix(refreshBlob, wrappedTokenPrefix):
			return CodexGatewayAccountSecret{}, fmt.Errorf("%w: refresh token for account %s (%s)", ErrCorruptSecrets, secret.ID, uerr)
		default:
			secret.RefreshToken = refreshBlob
			if s.log != nil {
				s.log.Debug("storage: codex gateway refresh token read as legacy plaintext; will re-wrap with kw1: prefix on next update", "account_id", secret.ID)
			}
		}
	}
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
	tokensChanged := patch.AccessToken != nil || patch.RefreshToken != nil
	if !tokensChanged {
		_, err = s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET label = ?, status = ?, expires_at = ?, plan = ?, updated_at = ?
WHERE id = ?`, label, status, expiresAt, plan, now(), id)
		if err != nil {
			return CodexGatewayAccount{}, err
		}
		return s.GetCodexGatewayAccount(ctx, id)
	}
	accessBlob, werr := s.wrapGWToken(accessToken)
	if werr != nil {
		return CodexGatewayAccount{}, fmt.Errorf("wrap access token: %w", werr)
	}
	refreshBlob, werr := s.wrapGWToken(refreshToken)
	if werr != nil {
		return CodexGatewayAccount{}, fmt.Errorf("wrap refresh token: %w", werr)
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET label = ?, status = ?, access_token_secret = ?, refresh_token_secret = ?, expires_at = ?, plan = ?, updated_at = ?
WHERE id = ?`, label, status, accessBlob, refreshBlob, expiresAt, plan, now(), id)
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
	accessBlob, err := s.wrapGWToken(strings.TrimSpace(accessToken))
	if err != nil {
		return CodexGatewayAccountSecret{}, fmt.Errorf("wrap access token: %w", err)
	}
	refreshBlob, err := s.wrapGWToken(strings.TrimSpace(refreshToken))
	if err != nil {
		return CodexGatewayAccountSecret{}, fmt.Errorf("wrap refresh token: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE codex_gateway_accounts
SET status = 'active', access_token_secret = ?, refresh_token_secret = ?, expires_at = ?, last_checked_at = ?, last_error = '', updated_at = ?
WHERE id = ?`, accessBlob, refreshBlob, strings.TrimSpace(expiresAt), now(), now(), id)
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

const imagePromptColumns = `id, title, description, prompt, mode, model, aspect_ratio, resolution, image_count, tags_json, status, use_count, last_used_at, deleted_at, created_at, updated_at`

func NormalizeImagePrompt(prompt ImagePrompt) ImagePrompt {
	prompt.ID = strings.TrimSpace(prompt.ID)
	prompt.Title = previewText(prompt.Title, 120)
	prompt.Description = previewText(prompt.Description, 1000)
	prompt.Prompt = strings.TrimSpace(prompt.Prompt)
	prompt.Mode = strings.TrimSpace(prompt.Mode)
	if prompt.Mode == "" {
		prompt.Mode = "text_to_image"
	}
	prompt.Model = strings.TrimSpace(prompt.Model)
	prompt.AspectRatio = strings.TrimSpace(prompt.AspectRatio)
	prompt.Resolution = strings.TrimSpace(prompt.Resolution)
	if prompt.ImageCount <= 0 {
		prompt.ImageCount = 1
	}
	prompt.Tags = normalizeImagePromptTags(prompt.Tags)
	prompt.Status = strings.TrimSpace(strings.ToLower(prompt.Status))
	if prompt.Status == "" {
		prompt.Status = "active"
	}
	if prompt.Status != "active" && prompt.Status != "deleted" {
		prompt.Status = "active"
	}
	prompt.LastUsedAt = strings.TrimSpace(prompt.LastUsedAt)
	prompt.DeletedAt = strings.TrimSpace(prompt.DeletedAt)
	return prompt
}

func normalizeImagePromptTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = previewText(tag, 32)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func (s *Store) CreateImagePrompt(ctx context.Context, prompt ImagePrompt) (ImagePrompt, error) {
	if prompt.ID == "" {
		id, err := ids.New("imgprompt")
		if err != nil {
			return ImagePrompt{}, err
		}
		prompt.ID = id
	}
	prompt = NormalizeImagePrompt(prompt)
	now := now()
	if prompt.CreatedAt == "" {
		prompt.CreatedAt = now
	}
	prompt.UpdatedAt = now
	tagsJSON, _ := json.Marshal(prompt.Tags)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO image_prompt_library (
  id, title, description, prompt, mode, model, aspect_ratio, resolution, image_count, tags_json, status, use_count, last_used_at, deleted_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		prompt.ID, prompt.Title, prompt.Description, prompt.Prompt, prompt.Mode, prompt.Model, prompt.AspectRatio, prompt.Resolution, prompt.ImageCount, string(tagsJSON), prompt.Status, prompt.UseCount, prompt.LastUsedAt, prompt.DeletedAt, prompt.CreatedAt, prompt.UpdatedAt)
	if err != nil {
		return ImagePrompt{}, err
	}
	return s.GetImagePrompt(ctx, prompt.ID)
}

func (s *Store) GetImagePrompt(ctx context.Context, id string) (ImagePrompt, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+imagePromptColumns+` FROM image_prompt_library WHERE id = ?`, id)
	prompt, err := scanImagePrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ImagePrompt{}, ErrNotFound
	}
	return prompt, err
}

func (s *Store) ListImagePrompts(ctx context.Context, limit int, q, mode, status string) ([]ImagePrompt, error) {
	if limit <= 0 || limit > 300 {
		limit = 120
	}
	query := `SELECT ` + imagePromptColumns + ` FROM image_prompt_library`
	args := []any{}
	clauses := []string{}
	if status = strings.TrimSpace(strings.ToLower(status)); status == "" {
		clauses = append(clauses, "status = 'active'")
	} else if status != "all" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if mode = strings.TrimSpace(mode); mode != "" && mode != "all" {
		clauses = append(clauses, "mode = ?")
		args = append(args, mode)
	}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + q + "%"
		clauses = append(clauses, "(title LIKE ? OR description LIKE ? OR prompt LIKE ? OR tags_json LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImagePrompt{}
	for rows.Next() {
		prompt, err := scanImagePrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, prompt)
	}
	return out, rows.Err()
}

func (s *Store) UpdateImagePrompt(ctx context.Context, id string, prompt ImagePrompt) (ImagePrompt, error) {
	existing, err := s.GetImagePrompt(ctx, id)
	if err != nil {
		return ImagePrompt{}, err
	}
	if existing.Status == "deleted" {
		return ImagePrompt{}, ErrNotFound
	}
	prompt = NormalizeImagePrompt(prompt)
	prompt.ID = existing.ID
	prompt.Status = existing.Status
	prompt.UseCount = existing.UseCount
	prompt.LastUsedAt = existing.LastUsedAt
	prompt.DeletedAt = existing.DeletedAt
	prompt.CreatedAt = existing.CreatedAt
	prompt.UpdatedAt = now()
	tagsJSON, _ := json.Marshal(prompt.Tags)
	_, err = s.db.ExecContext(ctx, `
UPDATE image_prompt_library SET
  title = ?, description = ?, prompt = ?, mode = ?, model = ?, aspect_ratio = ?, resolution = ?, image_count = ?, tags_json = ?, status = ?, use_count = ?, last_used_at = ?, deleted_at = ?, updated_at = ?
WHERE id = ?`,
		prompt.Title, prompt.Description, prompt.Prompt, prompt.Mode, prompt.Model, prompt.AspectRatio, prompt.Resolution, prompt.ImageCount, string(tagsJSON), prompt.Status, prompt.UseCount, prompt.LastUsedAt, prompt.DeletedAt, prompt.UpdatedAt, prompt.ID)
	if err != nil {
		return ImagePrompt{}, err
	}
	return s.GetImagePrompt(ctx, prompt.ID)
}

func (s *Store) DeleteImagePrompt(ctx context.Context, id string) (ImagePrompt, error) {
	timestamp := now()
	_, err := s.db.ExecContext(ctx, `UPDATE image_prompt_library SET status = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ? AND status != 'deleted'`, timestamp, timestamp, id)
	if err != nil {
		return ImagePrompt{}, err
	}
	return s.GetImagePrompt(ctx, id)
}

func (s *Store) UseImagePrompt(ctx context.Context, id string) (ImagePrompt, error) {
	timestamp := now()
	result, err := s.db.ExecContext(ctx, `UPDATE image_prompt_library SET use_count = use_count + 1, last_used_at = ?, updated_at = ? WHERE id = ? AND status = 'active'`, timestamp, timestamp, id)
	if err != nil {
		return ImagePrompt{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ImagePrompt{}, ErrNotFound
	}
	return s.GetImagePrompt(ctx, id)
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
	out, _, err := s.ListImageAssetsPage(ctx, limit, 0, assetType, storageBackend, status, q, privacy)
	return out, err
}

func (s *Store) ListImageAssetsPage(ctx context.Context, limit, offset int, assetType, storageBackend, status, q, privacy string) ([]ImageAsset, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + imageAssetColumns + ` FROM image_assets`
	args, whereSQL := imageAssetFilterArgs(assetType, storageBackend, status, q, privacy)
	if whereSQL != "" {
		query += " WHERE " + whereSQL
	}
	countQuery := `SELECT COUNT(*) FROM image_assets`
	if whereSQL != "" {
		countQuery += " WHERE " + whereSQL
	}
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ImageAsset{}
	for rows.Next() {
		asset, err := scanImageAsset(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for index := range out {
		s.hydrateRemoteImageAssetURL(ctx, &out[index])
	}
	return out, total, nil
}

func imageAssetFilterArgs(assetType, storageBackend, status, q, privacy string) ([]any, string) {
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
		return args, strings.Join(clauses, " AND ")
	}
	return args, ""
}

func (s *Store) hydrateRemoteImageAssetURL(ctx context.Context, asset *ImageAsset) {
	if asset == nil || asset.StorageBackend != "remote" {
		return
	}
	var remoteURL string
	if err := s.db.QueryRowContext(ctx, `
SELECT remote_url FROM image_generation_outputs
WHERE asset_id = ? OR (asset_id = '' AND job_id = ? AND slot = ?)
ORDER BY created_at DESC LIMIT 1`, asset.ID, asset.JobID, asset.Slot).Scan(&remoteURL); err != nil && !errors.Is(err, sql.ErrNoRows) {
		if s.log != nil {
			s.log.DebugContext(ctx, "storage: remote asset URL hydrate failed", "asset_id", asset.ID, "error", safelog.Error(err, 160))
		}
	}
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

func (s *Store) ArchiveImageAssetToS3(ctx context.Context, asset ImageAsset) (ImageAsset, error) {
	asset = NormalizeImageAsset(asset)
	asset.StorageBackend = "s3"
	asset.LocalName = ""
	asset.LastError = ""
	asset.UpdatedAt = now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImageAsset{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
UPDATE image_assets SET
  status = ?, private = ?, private_at = ?, mime_type = ?, extension = ?, size_bytes = ?, width = ?, height = ?, checksum_sha256 = ?, local_name = ?, storage_backend = ?, object_storage_profile_id = ?, s3_bucket = ?, s3_region = ?, s3_endpoint_label = ?, s3_key = ?, s3_etag = ?, archived_at = ?, deleted_at = ?, deleted_reason = ?, last_error = ?, updated_at = ?
WHERE id = ?`,
		asset.Status, boolInt(asset.Private), asset.PrivateAt, asset.MimeType, asset.Extension, asset.SizeBytes, asset.Width, asset.Height, asset.ChecksumSHA256, asset.LocalName, asset.StorageBackend, asset.ObjectStorageProfileID, asset.S3Bucket, asset.S3Region, asset.S3EndpointLabel, asset.S3Key, asset.S3ETag, asset.ArchivedAt, asset.DeletedAt, asset.DeletedReason, asset.LastError, asset.UpdatedAt, asset.ID)
	if err != nil {
		return ImageAsset{}, err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE image_generation_outputs
SET local_name = '', mime_type = ?, storage = 's3', size_bytes = ?
WHERE asset_id = ? OR (asset_id = '' AND job_id = ? AND slot = ?)`,
		asset.MimeType, asset.SizeBytes, asset.ID, asset.JobID, asset.Slot)
	if err != nil {
		return ImageAsset{}, err
	}
	if err := tx.Commit(); err != nil {
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
	out, _, err := s.ListImageGenerationJobsPage(ctx, limit, 0, status, mode)
	return out, err
}

func (s *Store) ListImageGenerationJobsPage(ctx context.Context, limit, offset int, status, mode string) ([]ImageGenerationJob, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, provider, status, mode, mode_label, model, endpoint, prompt, aspect_ratio, resolution, response_format, image_count, source_count, usage_json, error_message, created_at, started_at, completed_at FROM image_generation_jobs`
	args, whereSQL := imageGenerationJobFilterArgs(status, mode)
	if whereSQL != "" {
		query += " WHERE " + whereSQL
	}
	countQuery := `SELECT COUNT(*) FROM image_generation_jobs`
	if whereSQL != "" {
		countQuery += " WHERE " + whereSQL
	}
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	out := []ImageGenerationJob{}
	for rows.Next() {
		job, err := scanImageGenerationJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	for i := range out {
		if err := s.attachImageJobRelations(ctx, &out[i]); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

func imageGenerationJobFilterArgs(status, mode string) ([]any, string) {
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
	if len(clauses) == 0 {
		return args, ""
	}
	return args, strings.Join(clauses, " AND ")
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

// RevokeAllSessions 标记所有未撤销的会话为已撤销。
// 兼容历史数据中 revoked_at 为空字符串或 NULL 两种情况。
func (s *Store) RevokeAllSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = ? WHERE revoked_at IS NULL OR revoked_at = ''`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) AddAudit(ctx context.Context, event AuditEvent) (AuditEvent, error) {
	id, err := ids.New("aud")
	if err != nil {
		return AuditEvent{}, err
	}
	event.ID = id
	event.CreatedAt = now()
	// Canonical audit redaction: always run Summary + every string in
	// Payload through safelog.Redact before marshalling. The whole
	// point of this step is defence-in-depth — if a caller forgets to
	// redact a sensitive field, the write path still catches it.
	// EventType / RiskLevel / WorkspaceID are enum-style fields and
	// intentionally excluded from redaction.
	event.Summary = safelog.Redact(event.Summary)
	event.Payload = redactAuditPayload(event.Payload)
	payload, _ := json.Marshal(event.Payload)
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_events (id, event_type, workspace_id, risk_level, summary, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, event.ID, event.EventType, event.WorkspaceID, event.RiskLevel, event.Summary, string(payload), event.CreatedAt)
	return event, err
}

// redactAuditPayload recursively walks a free-form audit payload map
// applying two layers of defence-in-depth:
//
//   - KEY-AWARE REDACTION: if the map key case-insensitively matches a
//     secret-like name (password, token, secret, apiKey, authorization,
//     cookie, session, csrf …), the value is replaced entirely with a
//     redaction marker. This catches cases where the raw value itself
//     doesn't match safelog.Redact's regex surface (e.g. a bare
//     "password": "hunter2", or a non-prefixed opaque token).
//
//   - VALUE-AWARE REDACTION: for all surviving string values, run
//     safelog.Redact so Bearer/Authorization/Authorization headers,
//     AWS signatures, api_key="..." k/v pairs etc. are caught even if
//     the enclosing key is innocuous ("headers", "message", "raw_err").
//
// The function mutates the map in place and returns it for convenience;
// nil inputs return nil. Unknown value types are preserved untouched.
func redactAuditPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	for key, value := range payload {
		if isSecretKey(key) {
			payload[key] = keyAwareRedactedMarker(key)
			continue
		}
		payload[key] = redactAuditValue(key, value)
	}
	return payload
}

// sensitiveAuditKeys are payload key names (case-insensitive match after
// underscore/hyphen stripping) that are always redacted regardless of
// their value shape. Keep this list broad; false positives only mean a
// benign field is replaced with a marker, whereas misses leak secrets.
var sensitiveAuditKeys = map[string]struct{}{
	"password":         {},
	"passwd":           {},
	"pwd":              {},
	"token":            {},
	"accesstoken":      {},
	"refreshtoken":     {},
	"bearertoken":      {},
	"secret":           {},
	"secretkey":        {},
	"apikey":           {},
	"apisecret":        {},
	"authorization":    {},
	"auth":             {},
	"cookie":           {},
	"setcookie":        {},
	"session":          {},
	"sessionid":        {},
	"csrftoken":        {},
	"csrf":             {},
	"privatekey":       {},
	"privatekeybase64": {},
	"privatekeypem":    {},
	"signingkey":       {},
	"jwt":              {},
	"credential":       {},
	"credentials":      {},
}

// isSecretKey normalises a key to lower-case letters only and checks
// against sensitiveAuditKeys. The normalisation strips spaces, dashes,
// underscores and dots so `api_key`, `Api-Key`, `api.key` all match the
// `apikey` entry.
func isSecretKey(key string) bool {
	key = strings.ToLower(key)
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch r {
		case '-', '_', '.', ' ':
			// strip
		default:
			b.WriteRune(r)
		}
	}
	_, ok := sensitiveAuditKeys[b.String()]
	return ok
}

// keyAwareRedactedMarker produces a short, grep-friendly redaction
// marker that preserves which key was redacted so operators can still
// tell "which field was here" from the audit JSON without leaking
// contents.
func keyAwareRedactedMarker(key string) string {
	return fmt.Sprintf("[redacted:%s]", key)
}

// redactAuditValue walks a (possibly nested) value from an audit
// payload. The parent key is threaded through so recursive map nodes
// can re-apply key-aware redaction at every depth.
func redactAuditValue(parentKey string, value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return safelog.Redact(v)
	case []string:
		for i, s := range v {
			v[i] = safelog.Redact(s)
		}
		return v
	case []any:
		for i, item := range v {
			// Slice items have no unique key; use "parentKey[i]" as
			// an informative pseudo-key for any nested maps inside.
			v[i] = redactAuditValue(fmt.Sprintf("%s[%d]", parentKey, i), item)
		}
		return v
	case map[string]any:
		// Nested maps re-enter redactAuditPayload so every inner key
		// gets the key-aware check too.
		return redactAuditPayload(v)
	}
	// Preserve booleans, numbers, and other JSON-safe scalar types
	// without modification — these never carry secrets.
	return value
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

// ---- audit_events retention ----

// DefaultAuditRetentionDays is the retention window applied when the
// `system.audit_retention_days` setting has not been explicitly set.
// 0 means "never prune"; callers should check for 0 and skip pruning.
const DefaultAuditRetentionDays = 365

// DefaultAuditRetentionBatchSize bounds the number of rows deleted in a
// single DELETE statement so we never hold a write lock on the
// audit_events table for too long on large databases. The pruner loops
// until no further rows match the cutoff.
const DefaultAuditRetentionBatchSize = 1000

const auditRetentionDaysSettingKey = "system.audit_retention_days"

// GetAuditRetentionDays returns the configured audit retention in days.
// A return value of 0 means retention is disabled and rows are never
// pruned. Missing/unparseable settings fall back to
// DefaultAuditRetentionDays.
func (s *Store) GetAuditRetentionDays(ctx context.Context) int {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, auditRetentionDaysSettingKey)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefaultAuditRetentionDays
		}
		return DefaultAuditRetentionDays
	}
	var days int
	if _, err := fmt.Sscanf(raw, "%d", &days); err != nil || days < 0 {
		return DefaultAuditRetentionDays
	}
	return days
}

// DeleteAuditEventsOlderThan removes audit_events rows with created_at
// strictly earlier than `cutoff`. Deletion happens in batches bounded
// by batchSize (defaulting to DefaultAuditRetentionBatchSize) to avoid
// holding an exclusive write lock for the whole table. Returns the
// total number of rows deleted across all batches.
func (s *Store) DeleteAuditEventsOlderThan(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = DefaultAuditRetentionBatchSize
	}
	cutoffStr := cutoff.UTC().Format(time.RFC3339Nano)
	var total int64
	for {
		res, err := s.db.ExecContext(ctx, `
DELETE FROM audit_events
WHERE id IN (
    SELECT id FROM audit_events
    WHERE created_at < ?
    ORDER BY created_at ASC, id ASC
    LIMIT ?
)`, cutoffStr, batchSize)
		if err != nil {
			return total, err
		}
		affected, _ := res.RowsAffected()
		total += affected
		if affected < int64(batchSize) {
			return total, nil
		}
		// Yield to other writers between batches. SQLite's busy
		// timeout covers us in the overwhelming majority of cases,
		// but an explicit sleep ensures GC never starves the app
		// during a large backfill.
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
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

func (s *Store) ListRecentEventsByScope(ctx context.Context, scope string, limit int) ([]events.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, scope, scope_id, sequence, event_type, payload_json, created_at FROM events WHERE scope = ? ORDER BY created_at DESC, scope_id DESC, sequence DESC LIMIT ?`, scope, limit)
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
	err := row.Scan(&settings.ID, &enabled, &settings.BaseURL, &settings.OAuthAuthURL, &settings.OAuthTokenURL, &settings.OAuthClientID, &settings.OAuthRedirectURI, &settings.RequestTimeoutSeconds, &settings.RefreshMarginSeconds, &settings.AccountHealthCheckIntervalSeconds, &settings.DefaultInstructions, &settings.InstallationID, &settings.CreatedAt, &settings.UpdatedAt)
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

func scanImagePrompt(row workspaceScanner) (ImagePrompt, error) {
	var prompt ImagePrompt
	var tagsJSON string
	err := row.Scan(&prompt.ID, &prompt.Title, &prompt.Description, &prompt.Prompt, &prompt.Mode, &prompt.Model, &prompt.AspectRatio, &prompt.Resolution, &prompt.ImageCount, &tagsJSON, &prompt.Status, &prompt.UseCount, &prompt.LastUsedAt, &prompt.DeletedAt, &prompt.CreatedAt, &prompt.UpdatedAt)
	if err != nil {
		return ImagePrompt{}, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &prompt.Tags)
	return NormalizeImagePrompt(prompt), nil
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

// NowISO returns the current UTC time as an RFC3339Nano string.  It is the
// public counterpart of the unexported now() helper so other packages can
// produce timestamps that compare equal to the DB columns.
func NowISO() string { return now() }

// DB exposes the underlying *sql.DB to callers that need raw query access
// (e.g. the mail Service for ad-hoc folder scans).  Callers MUST close
// returned rows and MUST not hold connections longer than one request.
func (s *Store) DB() *sql.DB { return s.db }

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// WrapMailSecret is the exported counterpart of the unexported wrapMailSecret,
// exposed for the mail.Service layer so it can wrap HMAC secrets and other
// mail-module tokens without reaching into package-internal methods.
func (s *Store) WrapMailSecret(plain string) (string, error) {
	return s.wrapMailSecret(plain)
}

// UnwrapMailSecret is the exported counterpart of unwrapMailSecret.
func (s *Store) UnwrapMailSecret(blob string) (string, error) {
	return s.unwrapMailSecret(blob)
}
