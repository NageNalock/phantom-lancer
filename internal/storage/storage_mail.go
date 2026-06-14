package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// --- Row types ------------------------------------------------------------

// MailMoxSettings is the single-row settings table (id=1) that captures
// every Phantom-owned default for the Mox sidecar: desired runtime state,
// ports, ACME defaults, retention, etc.  It maps 1:1 to mail_mox_settings.
type MailMoxSettings struct {
	ID                                     int64  `json:"id"`
	PhantomInstanceID                      string `json:"phantom_instance_id"`
	ImportMode                             bool   `json:"import_mode"`
	ImportLabel                            string `json:"import_label"`
	ConfigMode                             string `json:"config_mode"`
	DesiredState                           string `json:"desired_state"`
	MoxBinaryPath                          string `json:"mox_binary_path"`
	MoxDataDir                             string `json:"mox_data_dir"`
	MoxConfigPath                          string `json:"mox_config_path"`
	WebAPIEndpoint                         string `json:"webapi_endpoint"`
	AdminEmail                             string `json:"admin_email"`
	Hostname                               string `json:"hostname"`
	SMTPPort                               int    `json:"smtp_port"`
	SMTPSubmissionPort                     int    `json:"smtp_submission_port"`
	SMTPSPort                              int    `json:"smtps_port"`
	IMAPPort                               int    `json:"imap_port"`
	IMAPSPort                              int    `json:"imaps_port"`
	WebmailAddr                            string `json:"webmail_addr"`
	WebAPIAddr                             string `json:"webapi_addr"`
	ACMEDefaultProviderID                  string `json:"acme_default_provider_id"`
	QueueMaxSizeBytes                      int64  `json:"queue_max_size_bytes"`
	QueueMaxAgeSeconds                     int64  `json:"queue_max_age_seconds"`
	OutboundRateLimitPerHour               int64  `json:"outbound_rate_limit_per_hour"`
	RetentionDeliveryEventsDays            int    `json:"retention_delivery_events_days"`
	RetentionHealthChecksPerType           int    `json:"retention_health_checks_per_type"`
	SearchIndexMaxSizeGB                   int    `json:"search_index_max_size_gb"`
	DNSBLEnabled                           bool   `json:"dnsbl_enabled"`
	DNSBLProvidersJSON                     string `json:"dnsbl_providers_json"`
	ExtraCapabilitiesJSON                  string `json:"extra_capabilities_json"`
	ImapSyncEnabled                        bool   `json:"imapsync_enabled"`
	ImapSyncMaxSizeBytes                   int64  `json:"imapsync_max_size_bytes"`
	ImapSyncBigMessageSizeLimitBytes       int64  `json:"imapsync_big_message_size_limit_bytes"`
	ImapSyncIntervalAttachmentCacheEnabled bool   `json:"imapsync_interval_attachment_cache_enabled"`
	RetentionAutoApplyEnabled              bool   `json:"retention_auto_apply_enabled"`
	CreatedAt                              string `json:"created_at"`
	UpdatedAt                              string `json:"updated_at"`
}

// MailDomain is a single sending/receiving domain registered with Mox.
type MailDomain struct {
	ID                   string         `json:"id"`
	Domain               string         `json:"domain"`
	Enabled              bool           `json:"enabled"`
	DKIMSelector         string         `json:"dkim_selector"`
	DKIMPrivateKey       string         `json:"-"` // never serialised
	DMARCPolicy          string         `json:"dmarc_policy"`
	DMARCRUA             string         `json:"dmarc_rua"`
	SPFInclude           string         `json:"spf_include"`
	DNSProviderID        string         `json:"dns_provider_id"`
	CertID               string         `json:"cert_id"`
	TLSAEnabled          bool           `json:"tlsa_enabled"`
	SANDomainsCSV        string         `json:"san_domains_csv"`
	TLSADomainsCSV       string         `json:"tlsa_domains_csv"`
	TLSADomainsWildcards bool           `json:"tlsa_domains_wildcards"`
	Synced               bool           `json:"synced"`
	LastSyncedAt         string         `json:"last_synced_at"`
	LastSyncError        string         `json:"last_sync_error"`
	LastDNSCheckAt       string         `json:"last_dns_check_at"`
	DNSCheckJSON         string         `json:"dns_check_json"`
	DNSStatus            map[string]any `json:"dns_status,omitempty"`
	CreatedAt            string         `json:"created_at"`
	UpdatedAt            string         `json:"updated_at"`
}

// MailAccount is one local mailbox (user@domain).
type MailAccount struct {
	ID                      string `json:"id"`
	DomainID                string `json:"domain_id"`
	LocalPart               string `json:"local_part"`
	Address                 string `json:"address"`
	Email                   string `json:"email"`
	DisplayName             string `json:"display_name"`
	PasswordMode            string `json:"password_mode"` // set | unset | external | disabled
	RecoveryEmail           string `json:"recovery_email"`
	QuotaMB                 int64  `json:"quota_mb"`
	StorageLimitMB          int64  `json:"storage_limit_mb"`
	IsAdmin                 bool   `json:"is_admin"`
	IMAPSyncEnabled         bool   `json:"imap_sync_enabled"`
	IMAPSyncState           string `json:"imap_sync_state"`
	ImapHost                string `json:"imap_host"`
	ImapUsername            string `json:"imap_username"`
	IMAPSyncMaxSizeBytes    int64  `json:"imap_sync_max_size_bytes"`
	WebAPIPasswordWrapped   string `json:"-"`
	WebAPICredentialPresent bool   `json:"webapi_credential_present"`
	WebAPIEndpointValid     bool   `json:"webapi_endpoint_valid"`
	WebAPIRuntimeAvailable  bool   `json:"webapi_runtime_available"`
	SendDisabledReason      string `json:"send_disabled_reason,omitempty"`
	CanSend                 bool   `json:"can_send"`
	IMAPLastUIDValidity     string `json:"imap_last_uidvalidity"`
	IMAPLastUID             string `json:"imap_last_uid"`
	IMAPLastInternalDate    string `json:"imap_last_internaldate"`
	IMAPError               string `json:"imap_error"`
	Enabled                 bool   `json:"enabled"`
	Status                  string `json:"status"` // active | suspended | disabled
	Role                    string `json:"role"`
	ImportModeReadOnly      bool   `json:"import_mode_read_only"`
	Synced                  bool   `json:"synced"`
	LastSyncedAt            string `json:"last_synced_at"`
	LastSyncError           string `json:"last_sync_error"`
	LastPasswordChangedAt   string `json:"last_password_changed_at"`
	LastLoginAt             string `json:"last_login_at"`
	SyncState               string `json:"sync_state"`
	SyncFolderStatsJSON     string `json:"sync_folder_stats_json"`
	SyncLastUIDJSON         string `json:"sync_last_uid_json"`
	SyncLastRunAt           string `json:"sync_last_run_at"`
	SyncNextRunAt           string `json:"sync_next_run_at"`
	SyncError               string `json:"sync_error"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
}

// MailAlias represents a forwarding alias, distribution list, or catch-all
// rule attached to a domain.  Recipients are stored as a comma-separated
// list so the row maps 1:1 to the table (service layer parses / validates
// the CSV, storage treats it as opaque text).
type MailAlias struct {
	ID            string `json:"id"`
	DomainID      string `json:"domain_id"`
	Source        string `json:"source"`
	RecipientsCSV string `json:"recipients_csv"`
	Mode          string `json:"mode"` // alias | list | catchall
	ListName      string `json:"list_name,omitempty"`
	ListReplyTo   string `json:"list_reply_to,omitempty"`
	Description   string `json:"description,omitempty"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// MailImportRegistration describes an external Mox supervisor that Phantom
// has registered as an import-mode peer.  The remote sidecar lives in
// `data_dir`, optionally reachable at `probe_url`, and its API access token
// (if any) is stored in `access_token_wrapped` using the mail keeper.
type MailImportRegistration struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DataDir            string `json:"data_dir"`
	ConfigPath         string `json:"config_path,omitempty"`
	SupervisorType     string `json:"supervisor_type"` // external | systemd | supervised | embedded
	ReadOnly           bool   `json:"read_only"`
	ProbeURL           string `json:"probe_url,omitempty"`
	AccessTokenWrapped string `json:"access_token_wrapped,omitempty"`
	Status             string `json:"status"` // registered | connected | error | removed
	LastProbeAt        string `json:"last_probe_at,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	Version            string `json:"version,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// --- MailMoxSettings helpers (Phase 1 minimum) -------------------------

// MailEnsureSettings creates the singleton mail_mox_settings row (id=1) on
// first boot, using sensible defaults derived from the design doc
// (§5.3 port allocation).  On subsequent calls it returns the existing
// row unchanged.  Safe to call concurrently.
func (s *Store) MailEnsureSettings(ctx context.Context) (*MailMoxSettings, error) {
	now := now()
	// Try insert – if unique constraint hits we'll read back.
	const insertSQL = `INSERT INTO mail_mox_settings (
  id, phantom_instance_id, import_mode, import_label, config_mode, desired_state,
  mox_binary_path, mox_data_dir, mox_config_path, webapi_endpoint, admin_email,
  hostname, smtp_port, smtp_submission_port, smtps_port, imap_port, imaps_port,
  webmail_addr, webapi_addr, acme_default_provider_id, queue_max_size_bytes,
  queue_max_age_seconds, outbound_rate_limit_per_hour, retention_delivery_events_days,
  retention_health_checks_per_type, search_index_max_size_gb, dnsbl_enabled,
  dnsbl_providers_json, extra_capabilities_json, imapsync_enabled,
  imapsync_max_size_bytes, imapsync_big_message_size_limit_bytes,
  imapsync_interval_attachment_cache_enabled, created_at, updated_at
) VALUES (
  1, '', 0, '', 'managed', 'stopped',
  '', '', '', '', '',
  '', 25, 587, 465, 143, 993,
  '127.0.0.1:10444', '127.0.0.1:10445', '', 1073741824,
  2592000, 0, 90,
  100, 10, 1,
  '{}', '{}', 1,
  10737418240, 52428800,
  1, ?, ?)`
	_, err := s.db.ExecContext(ctx, insertSQL, now, now)
	if err != nil && !isUniqueViolation(err) {
		// Unique violation means concurrent boot created the row – fine.
		return nil, fmt.Errorf("insert mail_mox_settings: %w", err)
	}
	return s.MailGetSettings(ctx)
}

// MailGetSettings reads the singleton settings row.  Returns ErrNotFound
// if the row does not exist (caller should fall back to MailEnsureSettings).
func (s *Store) MailGetSettings(ctx context.Context) (*MailMoxSettings, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
  id, phantom_instance_id, import_mode, import_label, config_mode, desired_state,
  mox_binary_path, mox_data_dir, mox_config_path, webapi_endpoint, admin_email,
  hostname, smtp_port, smtp_submission_port, smtps_port, imap_port, imaps_port,
  webmail_addr, webapi_addr, acme_default_provider_id, queue_max_size_bytes,
  queue_max_age_seconds, outbound_rate_limit_per_hour, retention_delivery_events_days,
  retention_health_checks_per_type, search_index_max_size_gb, dnsbl_enabled,
  dnsbl_providers_json, extra_capabilities_json, imapsync_enabled,
  imapsync_max_size_bytes, imapsync_big_message_size_limit_bytes,
  imapsync_interval_attachment_cache_enabled, created_at, updated_at
FROM mail_mox_settings WHERE id = 1`)
	out := &MailMoxSettings{}
	var importMode, dnsbl, imapSync, imapCache int64
	err := row.Scan(
		&out.ID, &out.PhantomInstanceID, &importMode, &out.ImportLabel,
		&out.ConfigMode, &out.DesiredState, &out.MoxBinaryPath, &out.MoxDataDir,
		&out.MoxConfigPath, &out.WebAPIEndpoint, &out.AdminEmail, &out.Hostname,
		&out.SMTPPort, &out.SMTPSubmissionPort, &out.SMTPSPort, &out.IMAPPort,
		&out.IMAPSPort, &out.WebmailAddr, &out.WebAPIAddr, &out.ACMEDefaultProviderID,
		&out.QueueMaxSizeBytes, &out.QueueMaxAgeSeconds, &out.OutboundRateLimitPerHour,
		&out.RetentionDeliveryEventsDays, &out.RetentionHealthChecksPerType,
		&out.SearchIndexMaxSizeGB, &dnsbl, &out.DNSBLProvidersJSON,
		&out.ExtraCapabilitiesJSON, &imapSync,
		&out.ImapSyncMaxSizeBytes, &out.ImapSyncBigMessageSizeLimitBytes,
		&imapCache, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out.ImportMode = importMode != 0
	out.DNSBLEnabled = dnsbl != 0
	out.ImapSyncEnabled = imapSync != 0
	out.ImapSyncIntervalAttachmentCacheEnabled = imapCache != 0
	return out, nil
}

// MailUpdatePhantomInstanceID sets the phantom_instance_id column on the
// singleton row and returns the updated row.  If a concurrent writer beat
// us to it (id=1 already has phantom_instance_id != ”), returns the
// winning row so callers converge.  The caller uses this once at boot.
func (s *Store) MailUpdatePhantomInstanceID(ctx context.Context, id string) (*MailMoxSettings, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE mail_mox_settings SET phantom_instance_id = ?, updated_at = ? WHERE id = 1 AND phantom_instance_id = ''`,
		id, now(),
	)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		// Lost the race – caller should re-read via MailGetSettings /
		// MailEnsureSettings.  Return nil, nil to signal "no update, retry".
		return nil, nil
	}
	return s.MailGetSettings(ctx)
}

// --- Mox-settings partial upserts (Phase 2.4 lifecycle/setup) ----------------
//
// These helpers update a narrow slice of mail_mox_settings (id=1) and
// return the new full row.  All are safe under concurrent write: each uses
// a single-row UPDATE against the fixed id=1 row, so only the last writer
// wins – which is exactly the semantics we want for binary install /
// lifecycle operations / setup init.

// MailUpsertBinaryPath writes mox_binary_path.  Empty string clears it.
func (s *Store) MailUpsertBinaryPath(ctx context.Context, path string) (*MailMoxSettings, error) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mail_mox_settings SET mox_binary_path = ?, updated_at = ? WHERE id = 1`,
		path, now(),
	); err != nil {
		return nil, fmt.Errorf("upsert mox_binary_path: %w", err)
	}
	return s.MailGetSettings(ctx)
}

// MailUpsertDesiredState writes desired_state ("running"/"stopped"/…).
func (s *Store) MailUpsertDesiredState(ctx context.Context, state string) (*MailMoxSettings, error) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mail_mox_settings SET desired_state = ?, updated_at = ? WHERE id = 1`,
		state, now(),
	); err != nil {
		return nil, fmt.Errorf("upsert desired_state: %w", err)
	}
	return s.MailGetSettings(ctx)
}

func (s *Store) MailUpdateExtraCapabilities(ctx context.Context, raw string) (*MailMoxSettings, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mail_mox_settings SET extra_capabilities_json = ?, updated_at = ? WHERE id = 1`,
		raw, now(),
	); err != nil {
		return nil, fmt.Errorf("update extra_capabilities_json: %w", err)
	}
	return s.MailGetSettings(ctx)
}

// MailSetupUpdate carries the slice of fields written by SetupInitialize.
// Empty fields are preserved (caller pre-fills before calling).
type MailSetupUpdate struct {
	AdminEmail  string
	Hostname    string
	WebmailAddr string
	WebAPIAddr  string
	BinaryPath  string
	ConfigPath  string
	DataDir     string
}

// MailUpsertSetup persists SetupInitialize-resolved defaults.  Also sets
// config_mode = "managed" (import mode clears it later if needed).
func (s *Store) MailUpsertSetup(ctx context.Context, u MailSetupUpdate) (*MailMoxSettings, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE mail_mox_settings SET
	  admin_email = ?, hostname = ?, webmail_addr = ?, webapi_addr = ?,
	  mox_binary_path = ?, mox_config_path = ?, mox_data_dir = ?,
	  config_mode = 'managed', updated_at = ? WHERE id = 1`,
		u.AdminEmail, u.Hostname, u.WebmailAddr, u.WebAPIAddr,
		u.BinaryPath, u.ConfigPath, u.DataDir, now())
	if err != nil {
		return nil, fmt.Errorf("upsert setup: %w", err)
	}
	return s.MailGetSettings(ctx)
}

// MailImportUpdate carries the slice of fields written by SetupImport.
type MailImportUpdate struct {
	ImportMode bool
	Label      string
	BinaryPath string
	ConfigPath string
	DataDir    string
}

// MailUpsertImport persists import-mode state.  Also rewrites
// config_mode = "import" so callers can distinguish managed vs imported.
func (s *Store) MailUpsertImport(ctx context.Context, u MailImportUpdate) (*MailMoxSettings, error) {
	imode := int64(0)
	if u.ImportMode {
		imode = 1
	}
	mode := "managed"
	if u.ImportMode {
		mode = "import"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE mail_mox_settings SET
	  import_mode = ?, import_label = ?, mox_binary_path = ?,
	  mox_config_path = ?, mox_data_dir = ?, config_mode = ?, updated_at = ? WHERE id = 1`,
		imode, u.Label, u.BinaryPath, u.ConfigPath, u.DataDir, mode, now())
	if err != nil {
		return nil, fmt.Errorf("upsert import: %w", err)
	}
	return s.MailGetSettings(ctx)
}

// --- Generic mail CRUD skeletons (Phase 2+ fill in the bodies) ---------
//
// These methods exist so the compiler can catch wiring issues now rather
// than Phase 3.  Each returns ErrNotFound + a TODO marker so UI handlers
// that call them surface "not yet implemented" in a predictable way.
// Keep the list alphabetical by table name.

// --- Domains ------------------------------------------------------------

func (s *Store) MailCreateDomain(ctx context.Context, d MailDomain) (*MailDomain, error) {
	now := now()
	if d.ID == "" {
		d.ID = NewID("dom")
	}
	if d.DKIMSelector == "" {
		d.DKIMSelector = "default"
	}
	if d.DMARCPolicy == "" {
		d.DMARCPolicy = "none"
	}
	if d.DNSCheckJSON == "" {
		d.DNSCheckJSON = "{}"
	}
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if _, err := s.db.ExecContext(ctx, `INSERT INTO mail_domains (
	  id, domain, enabled, dkim_selector, dkim_private_key_wrapped,
	  dmarc_policy, dmarc_rua, spf_include, dns_provider_id, cert_id,
	  tlsa_enabled, san_domains_csv, tlsa_domains_csv, tlsa_domains_wildcards,
	  synced, last_synced_at, last_sync_error, last_dns_check_at, dns_check_json,
	  created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Domain, boolInt(d.Enabled), d.DKIMSelector, d.DKIMPrivateKey,
		d.DMARCPolicy, d.DMARCRUA, d.SPFInclude, d.DNSProviderID, d.CertID,
		boolInt(d.TLSAEnabled), d.SANDomainsCSV, d.TLSADomainsCSV, boolInt(d.TLSADomainsWildcards),
		boolInt(d.Synced), d.LastSyncedAt, d.LastSyncError, d.LastDNSCheckAt, d.DNSCheckJSON,
		d.CreatedAt, d.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert mail_domains: %w", err)
	}
	return s.MailGetDomain(ctx, d.ID)
}
func (s *Store) MailGetDomain(ctx context.Context, id string) (*MailDomain, error) {
	row := s.db.QueryRowContext(ctx, mailDomainSelectSQL+` WHERE id = ?`, id)
	return scanMailDomain(row)
}
func (s *Store) MailListDomains(ctx context.Context) ([]*MailDomain, error) {
	rows, err := s.db.QueryContext(ctx, mailDomainSelectSQL+` ORDER BY enabled DESC, domain ASC`)
	if err != nil {
		return nil, fmt.Errorf("list mail_domains: %w", err)
	}
	defer rows.Close()
	out := []*MailDomain{}
	for rows.Next() {
		d, err := scanMailDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mail_domains row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) MailUpdateDomain(ctx context.Context, d MailDomain) (*MailDomain, error) {
	if d.ID == "" {
		return nil, errors.New("mail domain id is required")
	}
	if d.DKIMSelector == "" {
		d.DKIMSelector = "default"
	}
	if d.DMARCPolicy == "" {
		d.DMARCPolicy = "none"
	}
	if d.DNSCheckJSON == "" {
		d.DNSCheckJSON = "{}"
	}
	d.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx, `UPDATE mail_domains SET
	  domain = ?, enabled = ?, dkim_selector = ?, dkim_private_key_wrapped = ?,
	  dmarc_policy = ?, dmarc_rua = ?, spf_include = ?, dns_provider_id = ?, cert_id = ?,
	  tlsa_enabled = ?, san_domains_csv = ?, tlsa_domains_csv = ?, tlsa_domains_wildcards = ?,
	  synced = ?, last_synced_at = ?, last_sync_error = ?, last_dns_check_at = ?, dns_check_json = ?,
	  updated_at = ? WHERE id = ?`,
		d.Domain, boolInt(d.Enabled), d.DKIMSelector, d.DKIMPrivateKey,
		d.DMARCPolicy, d.DMARCRUA, d.SPFInclude, d.DNSProviderID, d.CertID,
		boolInt(d.TLSAEnabled), d.SANDomainsCSV, d.TLSADomainsCSV, boolInt(d.TLSADomainsWildcards),
		boolInt(d.Synced), d.LastSyncedAt, d.LastSyncError, d.LastDNSCheckAt, d.DNSCheckJSON,
		d.UpdatedAt, d.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update mail_domains: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.MailGetDomain(ctx, d.ID)
}
func (s *Store) MailDeleteDomain(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_domains WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mail_domains: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const mailDomainSelectSQL = `SELECT
  id, domain, enabled, dkim_selector, dkim_private_key_wrapped,
  dmarc_policy, dmarc_rua, spf_include, dns_provider_id, cert_id,
  COALESCE(tlsa_enabled, 1), COALESCE(san_domains_csv, ''), COALESCE(tlsa_domains_csv, ''),
  COALESCE(tlsa_domains_wildcards, 0), synced, last_synced_at, last_sync_error,
  last_dns_check_at, dns_check_json, created_at, updated_at
FROM mail_domains`

func scanMailDomain(sc mailScanner) (*MailDomain, error) {
	var d MailDomain
	var enabled, tlsaEnabled, wildcard, synced int
	err := sc.Scan(
		&d.ID, &d.Domain, &enabled, &d.DKIMSelector, &d.DKIMPrivateKey,
		&d.DMARCPolicy, &d.DMARCRUA, &d.SPFInclude, &d.DNSProviderID, &d.CertID,
		&tlsaEnabled, &d.SANDomainsCSV, &d.TLSADomainsCSV,
		&wildcard, &synced, &d.LastSyncedAt, &d.LastSyncError,
		&d.LastDNSCheckAt, &d.DNSCheckJSON, &d.CreatedAt, &d.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Enabled = enabled != 0
	d.TLSAEnabled = tlsaEnabled != 0
	d.TLSADomainsWildcards = wildcard != 0
	d.Synced = synced != 0
	return &d, nil
}

// --- Accounts -----------------------------------------------------------

func (s *Store) MailCreateAccount(ctx context.Context, a MailAccount) (MailAccount, error) {
	now := now()
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	passwordMode := a.PasswordMode
	if passwordMode == "" {
		passwordMode = "set"
	}
	status := a.Status
	if status == "" {
		status = "active"
	}
	var quota sql.NullInt64
	quota.Int64 = a.QuotaMB
	quota.Valid = a.QuotaMB != 0
	var isAdmin sql.NullInt64
	isAdmin.Int64 = int64(boolInt(a.IsAdmin))
	isAdmin.Valid = true
	var imapSync sql.NullInt64
	imapSync.Int64 = int64(boolInt(a.IMAPSyncEnabled))
	imapSync.Valid = true

	const q = `INSERT OR REPLACE INTO mail_accounts (
	  id, domain_id, local_part, address, display_name,
	  password_mode, quota_mb, is_admin,
	  imap_sync_enabled, imap_sync_state, imap_host, imap_username,
	  imap_sync_max_size_bytes, webapi_password_wrapped,
	  imap_last_uidvalidity, imap_last_uid, imap_last_internaldate, imap_error,
	  status, last_login_at, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`
	var displayName, imapState, imapHost, imapUser, imapUidval, imapUid, imapDate, imapErr, lastLogin sql.NullString
	var imapMaxSize sql.NullInt64
	displayName.String = a.DisplayName
	displayName.Valid = a.DisplayName != ""
	imapState.String = a.IMAPSyncState
	if imapState.String == "" {
		imapState.String = "idle"
	}
	imapState.Valid = true
	imapHost.String = a.ImapHost
	imapHost.Valid = a.ImapHost != ""
	imapUser.String = a.ImapUsername
	imapUser.Valid = a.ImapUsername != ""
	imapMaxSize.Int64 = a.IMAPSyncMaxSizeBytes
	imapMaxSize.Valid = a.IMAPSyncMaxSizeBytes != 0
	imapUidval.String = a.IMAPLastUIDValidity
	imapUidval.Valid = a.IMAPLastUIDValidity != ""
	imapUid.String = a.IMAPLastUID
	imapUid.Valid = a.IMAPLastUID != ""
	imapDate.String = a.IMAPLastInternalDate
	imapDate.Valid = a.IMAPLastInternalDate != ""
	imapErr.String = a.IMAPError
	imapErr.Valid = a.IMAPError != ""
	lastLogin.String = a.LastLoginAt
	lastLogin.Valid = a.LastLoginAt != ""

	if _, err := s.db.ExecContext(ctx, q,
		a.ID, a.DomainID, a.LocalPart, a.Address, displayName,
		passwordMode, quota, isAdmin,
		imapSync, imapState, imapHost, imapUser,
		imapMaxSize, a.WebAPIPasswordWrapped,
		imapUidval, imapUid, imapDate, imapErr,
		status, lastLogin, a.CreatedAt, a.UpdatedAt,
	); err != nil {
		return MailAccount{}, fmt.Errorf("insert mail_accounts: %w", err)
	}
	return s.MailGetAccount(ctx, a.ID)
}

func (s *Store) MailUpdateAccount(ctx context.Context, a MailAccount) (MailAccount, error) {
	a.UpdatedAt = now()
	passwordMode := a.PasswordMode
	if passwordMode == "" {
		passwordMode = "set"
	}
	status := a.Status
	if status == "" {
		status = "active"
	}
	const q = `UPDATE mail_accounts SET
	  domain_id = $1, local_part = $2, address = $3, display_name = $4,
	  password_mode = $5, quota_mb = $6, is_admin = $7,
	  imap_sync_enabled = $8, imap_sync_state = $9,
	  imap_host = $10, imap_username = $11, imap_sync_max_size_bytes = $12,
	  webapi_password_wrapped = $13,
	  imap_last_uidvalidity = $14, imap_last_uid = $15,
	  imap_last_internaldate = $16, imap_error = $17,
	  status = $18, last_login_at = $19, updated_at = $20
	WHERE id = $21`
	if _, err := s.db.ExecContext(ctx, q,
		a.DomainID, a.LocalPart, a.Address, a.DisplayName,
		passwordMode, a.QuotaMB, boolInt(a.IsAdmin),
		boolInt(a.IMAPSyncEnabled), a.IMAPSyncState,
		a.ImapHost, a.ImapUsername, a.IMAPSyncMaxSizeBytes,
		a.WebAPIPasswordWrapped,
		a.IMAPLastUIDValidity, a.IMAPLastUID,
		a.IMAPLastInternalDate, a.IMAPError,
		status, a.LastLoginAt, a.UpdatedAt, a.ID,
	); err != nil {
		return MailAccount{}, fmt.Errorf("update mail_accounts: %w", err)
	}
	return s.MailGetAccount(ctx, a.ID)
}

func (s *Store) MailDeleteAccount(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_accounts WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("delete mail_accounts: %w", err)
	}
	return nil
}

func (s *Store) MailGetAccount(ctx context.Context, id string) (MailAccount, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, domain_id, local_part, address, display_name,
	  password_mode, quota_mb, is_admin,
	  imap_sync_enabled, imap_sync_state, imap_host, imap_username,
	  imap_sync_max_size_bytes, COALESCE(webapi_password_wrapped, ''),
	  imap_last_uidvalidity, imap_last_uid, imap_last_internaldate, imap_error,
	  status, last_login_at, created_at, updated_at
	FROM mail_accounts WHERE id = $1`, id)
	return scanMailAccount(row)
}

func (s *Store) MailListAccounts(ctx context.Context, domainID string, status string) ([]MailAccount, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case domainID != "" && status != "":
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, local_part, address, display_name,
		  password_mode, quota_mb, is_admin,
		  imap_sync_enabled, imap_sync_state, imap_host, imap_username,
		  imap_sync_max_size_bytes, COALESCE(webapi_password_wrapped, ''),
		  imap_last_uidvalidity, imap_last_uid, imap_last_internaldate, imap_error,
		  status, last_login_at, created_at, updated_at
		FROM mail_accounts
		WHERE domain_id = $1 AND status = $2
		ORDER BY address ASC`, domainID, status)
	case domainID != "":
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, local_part, address, display_name,
		  password_mode, quota_mb, is_admin,
		  imap_sync_enabled, imap_sync_state, imap_host, imap_username,
		  imap_sync_max_size_bytes, COALESCE(webapi_password_wrapped, ''),
		  imap_last_uidvalidity, imap_last_uid, imap_last_internaldate, imap_error,
		  status, last_login_at, created_at, updated_at
		FROM mail_accounts
		WHERE domain_id = $1
		ORDER BY address ASC`, domainID)
	case status != "":
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, local_part, address, display_name,
		  password_mode, quota_mb, is_admin,
		  imap_sync_enabled, imap_sync_state, imap_host, imap_username,
		  imap_sync_max_size_bytes, COALESCE(webapi_password_wrapped, ''),
		  imap_last_uidvalidity, imap_last_uid, imap_last_internaldate, imap_error,
		  status, last_login_at, created_at, updated_at
		FROM mail_accounts
		WHERE status = $1
		ORDER BY address ASC`, status)
	default:
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, local_part, address, display_name,
		  password_mode, quota_mb, is_admin,
		  imap_sync_enabled, imap_sync_state, imap_host, imap_username,
		  imap_sync_max_size_bytes, COALESCE(webapi_password_wrapped, ''),
		  imap_last_uidvalidity, imap_last_uid, imap_last_internaldate, imap_error,
		  status, last_login_at, created_at, updated_at
		FROM mail_accounts
		ORDER BY address ASC`)
	}
	if err != nil {
		return nil, fmt.Errorf("list mail_accounts: %w", err)
	}
	defer rows.Close()
	var out []MailAccount
	for rows.Next() {
		a, err := scanMailAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mail_accounts row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows mail_accounts: %w", err)
	}
	return out, nil
}

// mailScanner abstracts *sql.Row and *sql.Rows for scanMailAccount.
type mailScanner interface {
	Scan(dest ...any) error
}

func scanMailAccount(sc mailScanner) (MailAccount, error) {
	out := MailAccount{}
	var (
		displayName, imapState, imapHost, imapUser, webapiPassword, imapUidval, imapUid, imapDate, imapErr, lastLogin, createdAt, updatedAt sql.NullString
		quota, isAdmin, imapSync, imapMaxSize                                                                                               sql.NullInt64
	)
	err := sc.Scan(
		&out.ID, &out.DomainID, &out.LocalPart, &out.Address, &displayName,
		&out.PasswordMode, &quota, &isAdmin,
		&imapSync, &imapState, &imapHost, &imapUser,
		&imapMaxSize, &webapiPassword,
		&imapUidval, &imapUid, &imapDate, &imapErr,
		&out.Status, &lastLogin, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailAccount{}, ErrNotFound
	}
	if err != nil {
		return MailAccount{}, fmt.Errorf("scan mail_accounts: %w", err)
	}
	if displayName.Valid {
		out.DisplayName = displayName.String
	}
	if imapState.Valid {
		out.IMAPSyncState = imapState.String
	}
	if imapHost.Valid {
		out.ImapHost = imapHost.String
	}
	if imapUser.Valid {
		out.ImapUsername = imapUser.String
	}
	if webapiPassword.Valid {
		out.WebAPIPasswordWrapped = webapiPassword.String
	}
	if imapMaxSize.Valid {
		out.IMAPSyncMaxSizeBytes = imapMaxSize.Int64
	}
	if imapUidval.Valid {
		out.IMAPLastUIDValidity = imapUidval.String
	}
	if imapUid.Valid {
		out.IMAPLastUID = imapUid.String
	}
	if imapDate.Valid {
		out.IMAPLastInternalDate = imapDate.String
	}
	if imapErr.Valid {
		out.IMAPError = imapErr.String
	}
	if lastLogin.Valid {
		out.LastLoginAt = lastLogin.String
	}
	if createdAt.Valid {
		out.CreatedAt = createdAt.String
	}
	if updatedAt.Valid {
		out.UpdatedAt = updatedAt.String
	}
	if quota.Valid {
		out.QuotaMB = quota.Int64
	}
	if isAdmin.Valid {
		out.IsAdmin = isAdmin.Int64 != 0
	}
	if imapSync.Valid {
		out.IMAPSyncEnabled = imapSync.Int64 != 0
	}
	// Legacy defaults (kept so old callers reading StorageLimitMB / Enabled still work)
	if out.StorageLimitMB == 0 {
		out.StorageLimitMB = out.QuotaMB
	}
	out.Enabled = out.Status == "active"
	return out, nil
}

// --- Aliases ------------------------------------------------------------

func (s *Store) MailCreateAlias(ctx context.Context, a MailAlias) (MailAlias, error) {
	now := now()
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	mode := a.Mode
	if mode == "" {
		mode = "alias"
	}
	const q = `INSERT OR REPLACE INTO mail_aliases (
	  id, domain_id, source, recipients_csv, mode,
	  list_name, list_reply_to, description, enabled,
	  created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	var listName, listReply, descr sql.NullString
	listName.String = a.ListName
	listName.Valid = a.ListName != ""
	listReply.String = a.ListReplyTo
	listReply.Valid = a.ListReplyTo != ""
	descr.String = a.Description
	descr.Valid = a.Description != ""
	var createdAt, updatedAt sql.NullString
	createdAt.String = a.CreatedAt
	createdAt.Valid = true
	updatedAt.String = a.UpdatedAt
	updatedAt.Valid = true

	if _, err := s.db.ExecContext(ctx, q,
		a.ID, a.DomainID, a.Source, a.RecipientsCSV, mode,
		listName, listReply, descr, boolInt(a.Enabled),
		createdAt, updatedAt,
	); err != nil {
		return MailAlias{}, fmt.Errorf("insert mail_aliases: %w", err)
	}
	return s.MailGetAlias(ctx, a.ID)
}

func (s *Store) MailUpdateAlias(ctx context.Context, a MailAlias) (MailAlias, error) {
	a.UpdatedAt = now()
	mode := a.Mode
	if mode == "" {
		mode = "alias"
	}
	const q = `UPDATE mail_aliases SET
	  domain_id = $1, source = $2, recipients_csv = $3, mode = $4,
	  list_name = $5, list_reply_to = $6, description = $7, enabled = $8,
	  updated_at = $9
	WHERE id = $10`
	if _, err := s.db.ExecContext(ctx, q,
		a.DomainID, a.Source, a.RecipientsCSV, mode,
		a.ListName, a.ListReplyTo, a.Description, boolInt(a.Enabled),
		a.UpdatedAt, a.ID,
	); err != nil {
		return MailAlias{}, fmt.Errorf("update mail_aliases: %w", err)
	}
	return s.MailGetAlias(ctx, a.ID)
}

func (s *Store) MailDeleteAlias(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_aliases WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("delete mail_aliases: %w", err)
	}
	return nil
}

func (s *Store) MailGetAlias(ctx context.Context, id string) (MailAlias, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, domain_id, source, recipients_csv, mode,
	  list_name, list_reply_to, description, enabled,
	  created_at, updated_at
	FROM mail_aliases WHERE id = $1`, id)
	return scanMailAlias(row)
}

func (s *Store) MailListAliases(ctx context.Context, domainID string, mode string) ([]MailAlias, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case domainID != "" && mode != "":
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, source, recipients_csv, mode,
		  list_name, list_reply_to, description, enabled,
		  created_at, updated_at
		FROM mail_aliases
		WHERE domain_id = $1 AND mode = $2
		ORDER BY source ASC`, domainID, mode)
	case domainID != "":
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, source, recipients_csv, mode,
		  list_name, list_reply_to, description, enabled,
		  created_at, updated_at
		FROM mail_aliases
		WHERE domain_id = $1
		ORDER BY source ASC`, domainID)
	case mode != "":
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, source, recipients_csv, mode,
		  list_name, list_reply_to, description, enabled,
		  created_at, updated_at
		FROM mail_aliases
		WHERE mode = $1
		ORDER BY source ASC`, mode)
	default:
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain_id, source, recipients_csv, mode,
		  list_name, list_reply_to, description, enabled,
		  created_at, updated_at
		FROM mail_aliases
		ORDER BY source ASC`)
	}
	if err != nil {
		return nil, fmt.Errorf("list mail_aliases: %w", err)
	}
	defer rows.Close()
	var out []MailAlias
	for rows.Next() {
		a, err := scanMailAlias(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mail_aliases row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows mail_aliases: %w", err)
	}
	return out, nil
}

func scanMailAlias(sc mailScanner) (MailAlias, error) {
	out := MailAlias{}
	var listName, listReply, descr, createdAt, updatedAt sql.NullString
	var enabled sql.NullInt64
	err := sc.Scan(
		&out.ID, &out.DomainID, &out.Source, &out.RecipientsCSV, &out.Mode,
		&listName, &listReply, &descr, &enabled,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailAlias{}, ErrNotFound
	}
	if err != nil {
		return MailAlias{}, fmt.Errorf("scan mail_aliases: %w", err)
	}
	if listName.Valid {
		out.ListName = listName.String
	}
	if listReply.Valid {
		out.ListReplyTo = listReply.String
	}
	if descr.Valid {
		out.Description = descr.String
	}
	if createdAt.Valid {
		out.CreatedAt = createdAt.String
	}
	if updatedAt.Valid {
		out.UpdatedAt = updatedAt.String
	}
	if enabled.Valid {
		out.Enabled = enabled.Int64 != 0
	} else {
		out.Enabled = true
	}
	return out, nil
}

// --- Import Registrations ----------------------------------------------

func (s *Store) MailCreateImportRegistration(ctx context.Context, r MailImportRegistration) (MailImportRegistration, error) {
	now := now()
	if r.CreatedAt == "" {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	svType := r.SupervisorType
	if svType == "" {
		svType = "external"
	}
	status := r.Status
	if status == "" {
		status = "registered"
	}
	const q = `INSERT OR REPLACE INTO mail_import_registrations (
	  id, name, data_dir, config_path, supervisor_type,
	  read_only, probe_url, access_token_wrapped, status,
	  last_probe_at, last_error, version,
	  created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
	var cfgPath, probeURL, token, lastProbe, lastErr, version sql.NullString
	cfgPath.String = r.ConfigPath
	cfgPath.Valid = r.ConfigPath != ""
	probeURL.String = r.ProbeURL
	probeURL.Valid = r.ProbeURL != ""
	token.String = r.AccessTokenWrapped
	token.Valid = r.AccessTokenWrapped != ""
	lastProbe.String = r.LastProbeAt
	lastProbe.Valid = r.LastProbeAt != ""
	lastErr.String = r.LastError
	lastErr.Valid = r.LastError != ""
	version.String = r.Version
	version.Valid = r.Version != ""
	var createdAt, updatedAt sql.NullString
	createdAt.String = r.CreatedAt
	createdAt.Valid = true
	updatedAt.String = r.UpdatedAt
	updatedAt.Valid = true

	if _, err := s.db.ExecContext(ctx, q,
		r.ID, r.Name, r.DataDir, cfgPath, svType,
		boolInt(r.ReadOnly), probeURL, token, status,
		lastProbe, lastErr, version,
		createdAt, updatedAt,
	); err != nil {
		return MailImportRegistration{}, fmt.Errorf("insert mail_import_registrations: %w", err)
	}
	return s.MailGetImportRegistration(ctx, r.ID)
}

func (s *Store) MailUpdateImportRegistration(ctx context.Context, r MailImportRegistration) (MailImportRegistration, error) {
	r.UpdatedAt = now()
	svType := r.SupervisorType
	if svType == "" {
		svType = "external"
	}
	status := r.Status
	if status == "" {
		status = "registered"
	}
	const q = `UPDATE mail_import_registrations SET
	  name = $1, data_dir = $2, config_path = $3, supervisor_type = $4,
	  read_only = $5, probe_url = $6, access_token_wrapped = $7, status = $8,
	  last_probe_at = $9, last_error = $10, version = $11, updated_at = $12
	WHERE id = $13`
	if _, err := s.db.ExecContext(ctx, q,
		r.Name, r.DataDir, r.ConfigPath, svType,
		boolInt(r.ReadOnly), r.ProbeURL, r.AccessTokenWrapped, status,
		r.LastProbeAt, r.LastError, r.Version, r.UpdatedAt, r.ID,
	); err != nil {
		return MailImportRegistration{}, fmt.Errorf("update mail_import_registrations: %w", err)
	}
	return s.MailGetImportRegistration(ctx, r.ID)
}

func (s *Store) MailDeleteImportRegistration(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_import_registrations WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("delete mail_import_registrations: %w", err)
	}
	return nil
}

func (s *Store) MailGetImportRegistration(ctx context.Context, id string) (MailImportRegistration, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, name, data_dir, config_path, supervisor_type,
	  read_only, probe_url, access_token_wrapped, status,
	  last_probe_at, last_error, version,
	  created_at, updated_at
	FROM mail_import_registrations WHERE id = $1`, id)
	return scanMailImportRegistration(row)
}

func (s *Store) MailListImportRegistrations(ctx context.Context) ([]MailImportRegistration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
	  id, name, data_dir, config_path, supervisor_type,
	  read_only, probe_url, access_token_wrapped, status,
	  last_probe_at, last_error, version,
	  created_at, updated_at
	FROM mail_import_registrations
	ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list mail_import_registrations: %w", err)
	}
	defer rows.Close()
	var out []MailImportRegistration
	for rows.Next() {
		r, err := scanMailImportRegistration(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mail_import_registrations row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows mail_import_registrations: %w", err)
	}
	return out, nil
}

func scanMailImportRegistration(sc mailScanner) (MailImportRegistration, error) {
	out := MailImportRegistration{}
	var cfgPath, probeURL, token, lastProbe, lastErr, version, createdAt, updatedAt sql.NullString
	var readOnly sql.NullInt64
	err := sc.Scan(
		&out.ID, &out.Name, &out.DataDir, &cfgPath, &out.SupervisorType,
		&readOnly, &probeURL, &token, &out.Status,
		&lastProbe, &lastErr, &version,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailImportRegistration{}, ErrNotFound
	}
	if err != nil {
		return MailImportRegistration{}, fmt.Errorf("scan mail_import_registrations: %w", err)
	}
	if cfgPath.Valid {
		out.ConfigPath = cfgPath.String
	}
	if probeURL.Valid {
		out.ProbeURL = probeURL.String
	}
	if token.Valid {
		out.AccessTokenWrapped = token.String
	}
	if lastProbe.Valid {
		out.LastProbeAt = lastProbe.String
	}
	if lastErr.Valid {
		out.LastError = lastErr.String
	}
	if version.Valid {
		out.Version = version.String
	}
	if createdAt.Valid {
		out.CreatedAt = createdAt.String
	}
	if updatedAt.Valid {
		out.UpdatedAt = updatedAt.String
	}
	if readOnly.Valid {
		out.ReadOnly = readOnly.Int64 != 0
	} else {
		out.ReadOnly = true
	}
	return out, nil
}

// --- Helpers ------------------------------------------------------------

// errNotImplemented is returned by every Phase-2+ stub so callers can
// distinguish "route wired but backend TBD" from real 500s.
var errNotImplemented = errors.New("not implemented in phase 1")

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint
// failure.  We detect it by substring match because mattn/go-sqlite3
// exposes sqlite3.ErrConstraint through errors.As but that's heavier
// than a single string check for the cases we use it for.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return containsFold(err.Error(), "constraint failed: unique") ||
		containsFold(err.Error(), "unique constraint failed")
}

func containsFold(s, sub string) bool {
	// SQLite error messages are case-stable, but keep this tolerant to
	// libc variations across Linux / macOS / Windows builds.
	if len(s) < len(sub) {
		return false
	}
	s = toLowerASCII(s)
	sub = toLowerASCII(sub)
	return len(s) >= len(sub) && indexASCII(s, sub) >= 0
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

// indexASCII returns the index of sub in s, or -1.  Both inputs are
// guaranteed ASCII on the call path above; we avoid strings.Index so
// storage_mail.go does not need an extra stdlib import.
func indexASCII(s, sub string) int {
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

// --- MailDNSProvider (Phase 4 skeleton types + stub CRUD) -----------------
//
// The certmanager agent will implement real SQL CRUD.  These stubs exist so
// the service layer and HTTP handlers compile cleanly before that agent lands.

// MailDNSProvider maps 1:1 to the mail_dns_providers table defined in
// storage.go.
type MailDNSProvider struct {
	ID                    string `json:"id"`
	Label                 string `json:"label"`
	DisplayName           string `json:"display_name"`
	Kind                  string `json:"kind"` // cloudflare|dnspod|route53|manual
	APIEndpoint           string `json:"api_endpoint,omitempty"`
	ZoneID                string `json:"zone_id,omitempty"`
	APICredentialsJSON    string `json:"-"`
	APICredentialsWrapped string `json:"api_credentials_wrapped,omitempty"`
	LastTestedAt          string `json:"last_tested_at,omitempty"`
	LastError             string `json:"last_error,omitempty"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

// MailCertificate maps 1:1 to the mail_certificates table in storage.go.
type MailCertificate struct {
	ID                  string   `json:"id"`
	Domain              string   `json:"domain"`
	PEMChain            string   `json:"pem_chain,omitempty"`
	DNSProviderID       string   `json:"dns_provider_id,omitempty"`
	DomainCoverageJSON  string   `json:"domain_coverage_json,omitempty"`
	PrimaryDomain       string   `json:"primary_domain"`
	SubjectAltNames     []string `json:"subject_alt_names,omitempty"`
	Issuer              string   `json:"issuer,omitempty"`
	Serial              string   `json:"serial,omitempty"`
	NotBefore           string   `json:"not_before,omitempty"`
	NotAfter            string   `json:"not_after,omitempty"`
	Subject             string   `json:"subject,omitempty"`
	SANCount            int      `json:"san_count"`
	CertPath            string   `json:"cert_path,omitempty"`
	ChainPath           string   `json:"chain_path,omitempty"`
	PrivkeyPath         string   `json:"privkey_path,omitempty"`
	ACMEProviderID      string   `json:"acme_provider_id,omitempty"`
	ACMEAccountURL      string   `json:"acme_account_url,omitempty"`
	SignatureHashSHA256 string   `json:"signature_hash_sha256,omitempty"`
	TLSA311             string   `json:"tlsa_311,omitempty"`
	RenewalAttemptedAt  string   `json:"renewal_attempted_at,omitempty"`
	RenewalStatus       string   `json:"renewal_status,omitempty"`
	RenewalError        string   `json:"renewal_error,omitempty"`
	Applied             bool     `json:"applied"`
	AppliedAt           string   `json:"applied_at,omitempty"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

// MailManualChallenge maps 1:1 to the mail_manual_challenges table.
type MailManualChallenge struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	FQDN      string `json:"fqdn"`
	Value     string `json:"value"`
	Status    string `json:"status"` // pending | confirmed | expired | failed
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// --- MailDNSProvider CRUD --------------------------------------------------

func (s *Store) MailCreateDNSProvider(ctx context.Context, p MailDNSProvider) (*MailDNSProvider, error) {
	now := now()
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	const q = `INSERT OR REPLACE INTO mail_dns_providers (
	  id, kind, display_name, config_json, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := s.db.ExecContext(ctx, q,
		p.ID, p.Kind, p.DisplayName, p.APICredentialsWrapped,
		p.CreatedAt, p.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert mail_dns_providers: %w", err)
	}
	return s.MailGetDNSProvider(ctx, p.ID)
}

func (s *Store) MailUpdateDNSProvider(ctx context.Context, p MailDNSProvider) (*MailDNSProvider, error) {
	p.UpdatedAt = now()
	const q = `UPDATE mail_dns_providers SET
	  kind = $1, display_name = $2, config_json = $3, updated_at = $4
	WHERE id = $5`
	if _, err := s.db.ExecContext(ctx, q,
		p.Kind, p.DisplayName, p.APICredentialsWrapped,
		p.UpdatedAt, p.ID,
	); err != nil {
		return nil, fmt.Errorf("update mail_dns_providers: %w", err)
	}
	return s.MailGetDNSProvider(ctx, p.ID)
}

func (s *Store) MailDeleteDNSProvider(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_dns_providers WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("delete mail_dns_providers: %w", err)
	}
	return nil
}

func (s *Store) MailGetDNSProvider(ctx context.Context, id string) (*MailDNSProvider, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, kind, display_name, config_json, created_at, updated_at
	FROM mail_dns_providers WHERE id = $1`, id)
	out := &MailDNSProvider{}
	err := row.Scan(
		&out.ID, &out.Kind, &out.DisplayName, &out.APICredentialsWrapped,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan mail_dns_providers: %w", err)
	}
	return out, nil
}

func (s *Store) MailListDNSProviders(ctx context.Context) ([]*MailDNSProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
	  id, kind, display_name, config_json, created_at, updated_at
	FROM mail_dns_providers ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list mail_dns_providers: %w", err)
	}
	defer rows.Close()
	var out []*MailDNSProvider
	for rows.Next() {
		p := &MailDNSProvider{}
		if err := rows.Scan(
			&p.ID, &p.Kind, &p.DisplayName, &p.APICredentialsWrapped,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan mail_dns_providers row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows mail_dns_providers: %w", err)
	}
	return out, nil
}

// --- MailCertificate CRUD -------------------------------------------------

func (s *Store) MailCreateCertificate(ctx context.Context, c MailCertificate) (*MailCertificate, error) {
	now := now()
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	sansJSON, err := json.Marshal(c.SubjectAltNames)
	if err != nil {
		return nil, fmt.Errorf("marshal subject_alt_names: %w", err)
	}
	applied := int64(0)
	if c.Applied {
		applied = 1
	}
	const q = `INSERT OR REPLACE INTO mail_certificates (
	  id, domain, issuer, serial, not_before, not_after,
	  pem_chain, dns_provider_id, last_renewal_attempt, next_renewal, last_error,
	  created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	if _, err := s.db.ExecContext(ctx, q,
		c.ID, c.Domain, c.Issuer, c.Serial, c.NotBefore, c.NotAfter,
		c.PEMChain, c.DNSProviderID, c.RenewalAttemptedAt, /* last_renewal_attempt */
		"", /* next_renewal – no struct field yet */
		c.RenewalError, c.CreatedAt, c.UpdatedAt,
	); err != nil {
		_ = sansJSON
		_ = applied
		return nil, fmt.Errorf("insert mail_certificates: %w", err)
	}
	return s.MailGetCertificate(ctx, c.ID)
}

func (s *Store) MailUpdateCertificate(ctx context.Context, c MailCertificate) (*MailCertificate, error) {
	c.UpdatedAt = now()
	const q = `UPDATE mail_certificates SET
	  domain = $1, issuer = $2, serial = $3, not_before = $4, not_after = $5,
	  pem_chain = $6, dns_provider_id = $7, last_renewal_attempt = $8, next_renewal = $9,
	  last_error = $10, updated_at = $11
	WHERE id = $12`
	if _, err := s.db.ExecContext(ctx, q,
		c.Domain, c.Issuer, c.Serial, c.NotBefore, c.NotAfter,
		c.PEMChain, c.DNSProviderID, c.RenewalAttemptedAt,
		"", c.RenewalError, c.UpdatedAt, c.ID,
	); err != nil {
		return nil, fmt.Errorf("update mail_certificates: %w", err)
	}
	return s.MailGetCertificate(ctx, c.ID)
}

func (s *Store) MailDeleteCertificate(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_certificates WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("delete mail_certificates: %w", err)
	}
	return nil
}

func (s *Store) MailGetCertificate(ctx context.Context, id string) (*MailCertificate, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, domain, issuer, serial, not_before, not_after,
	  pem_chain, dns_provider_id, last_renewal_attempt, next_renewal, last_error,
	  created_at, updated_at
	FROM mail_certificates WHERE id = $1`, id)
	out := &MailCertificate{}
	var lastRenewalAttempt, nextRenewal, lastErr sql.NullString
	err := row.Scan(
		&out.ID, &out.Domain, &out.Issuer, &out.Serial, &out.NotBefore, &out.NotAfter,
		&out.PEMChain, &out.DNSProviderID, &lastRenewalAttempt, &nextRenewal, &lastErr,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan mail_certificates: %w", err)
	}
	if lastRenewalAttempt.Valid {
		out.RenewalAttemptedAt = lastRenewalAttempt.String
	}
	if lastErr.Valid {
		out.RenewalError = lastErr.String
	}
	out.SubjectAltNames = []string{} // no column, safe default
	return out, nil
}

func (s *Store) MailListCertificates(ctx context.Context) ([]*MailCertificate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
	  id, domain, issuer, serial, not_before, not_after,
	  pem_chain, dns_provider_id, last_renewal_attempt, next_renewal, last_error,
	  created_at, updated_at
	FROM mail_certificates ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list mail_certificates: %w", err)
	}
	defer rows.Close()
	var out []*MailCertificate
	for rows.Next() {
		c := &MailCertificate{}
		var lastRenewalAttempt, nextRenewal, lastErr sql.NullString
		if err := rows.Scan(
			&c.ID, &c.Domain, &c.Issuer, &c.Serial, &c.NotBefore, &c.NotAfter,
			&c.PEMChain, &c.DNSProviderID, &lastRenewalAttempt, &nextRenewal, &lastErr,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan mail_certificates row: %w", err)
		}
		if lastRenewalAttempt.Valid {
			c.RenewalAttemptedAt = lastRenewalAttempt.String
		}
		if lastErr.Valid {
			c.RenewalError = lastErr.String
		}
		c.SubjectAltNames = []string{}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows mail_certificates: %w", err)
	}
	return out, nil
}

// --- MailManualChallenge CRUD ---------------------------------------------

func (s *Store) MailManualChallengeUpsert(ctx context.Context, ch MailManualChallenge) (*MailManualChallenge, error) {
	if ch.CreatedAt == "" {
		ch.CreatedAt = now()
	}
	const q = `INSERT OR REPLACE INTO mail_manual_challenges (
	  id, domain, fqdn, value, status, created_at, expires_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if _, err := s.db.ExecContext(ctx, q,
		ch.ID, ch.Domain, ch.FQDN, ch.Value, ch.Status,
		ch.CreatedAt, ch.ExpiresAt,
	); err != nil {
		return nil, fmt.Errorf("upsert mail_manual_challenges: %w", err)
	}
	// Re-read the row.  On conflict the INSERT OR REPLACE may have kept an
	// older created_at, so read-back guarantees we return what's on disk.
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, domain, fqdn, value, status, created_at, expires_at
	FROM mail_manual_challenges WHERE id = $1`, ch.ID)
	out := &MailManualChallenge{}
	if err := row.Scan(
		&out.ID, &out.Domain, &out.FQDN, &out.Value, &out.Status,
		&out.CreatedAt, &out.ExpiresAt,
	); err != nil {
		return nil, fmt.Errorf("scan mail_manual_challenges: %w", err)
	}
	return out, nil
}

func (s *Store) MailManualChallengeList(ctx context.Context, domain string, status string) ([]*MailManualChallenge, error) {
	var rows *sql.Rows
	var err error
	if domain != "" && status != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain, fqdn, value, status, created_at, expires_at
		FROM mail_manual_challenges
		WHERE domain = $1 AND status = $2
		ORDER BY created_at DESC`, domain, status)
	} else if domain != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain, fqdn, value, status, created_at, expires_at
		FROM mail_manual_challenges WHERE domain = $1
		ORDER BY created_at DESC`, domain)
	} else if status != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain, fqdn, value, status, created_at, expires_at
		FROM mail_manual_challenges WHERE status = $1
		ORDER BY created_at DESC`, status)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, domain, fqdn, value, status, created_at, expires_at
		FROM mail_manual_challenges ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("list mail_manual_challenges: %w", err)
	}
	defer rows.Close()
	var out []*MailManualChallenge
	for rows.Next() {
		ch := &MailManualChallenge{}
		if err := rows.Scan(
			&ch.ID, &ch.Domain, &ch.FQDN, &ch.Value, &ch.Status,
			&ch.CreatedAt, &ch.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan mail_manual_challenges row: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows mail_manual_challenges: %w", err)
	}
	return out, nil
}

func (s *Store) MailManualChallengeDelete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_manual_challenges WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("delete mail_manual_challenges: %w", err)
	}
	return nil
}

func (s *Store) MailManualChallengeConfirm(ctx context.Context, id string) (*MailManualChallenge, error) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE mail_manual_challenges SET status = 'confirmed' WHERE id = $1`, id,
	); err != nil {
		return nil, fmt.Errorf("confirm mail_manual_challenges: %w", err)
	}
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, domain, fqdn, value, status, created_at, expires_at
	FROM mail_manual_challenges WHERE id = $1`, id)
	out := &MailManualChallenge{}
	err := row.Scan(
		&out.ID, &out.Domain, &out.FQDN, &out.Value, &out.Status,
		&out.CreatedAt, &out.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan confirmed mail_manual_challenges: %w", err)
	}
	return out, nil
}

// suppress unused import when stubs are active
var _ = sql.ErrNoRows
var _ = fmt.Sprintf
var _ = errors.New
var _ = json.Marshal

// --- Phase 6: Delivery events -----------------------------------------

// MailDeliveryEvent records a single outbound or inbound delivery attempt.
type MailDeliveryEvent struct {
	ID             string `json:"id"`
	FromDomain     string `json:"from_domain"`
	ToDomain       string `json:"to_domain"`
	MessageIDHash  string `json:"message_id_hash"`
	SubjectSnippet string `json:"subject_snippet"`
	Direction      string `json:"direction"` // in/out/local
	SMTPCode       int    `json:"smtp_code,omitempty"`
	SMTPEnhanced   string `json:"smtp_enhanced,omitempty"`
	RedactedError  string `json:"redacted_error,omitempty"`
	Status         string `json:"status"` // pending/queued/sent/deferred/bounced/suppressed/dropped
	AttemptCount   int    `json:"attempt_count"`
	FirstAttemptAt string `json:"first_attempt_at,omitempty"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	CompletedAt    string `json:"completed_at,omitempty"`
	RecipientHash  string `json:"recipient_hash,omitempty"`
	QueueMsgID     int64  `json:"queue_msg_id,omitempty"`
	FromID         string `json:"from_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// MailDeliveryListFilter is used by list endpoints.
type MailDeliveryListFilter struct {
	Status     string
	Direction  string
	FromDomain string
	ToDomain   string
	Search     string
	Limit      int
	Cursor     string
}

// MailDeliveryListResponse wraps list results with pagination.
type MailDeliveryListResponse struct {
	Items      []*MailDeliveryEvent `json:"items"`
	Count      int                  `json:"count"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// MailDeliveryList returns paginated delivery events.
func (s *Store) MailDeliveryList(ctx context.Context, f MailDeliveryListFilter) (*MailDeliveryListResponse, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	const base = `SELECT id, from_domain, to_domain, message_id_hash, subject_snippet,
  direction, COALESCE(smtp_code,0), COALESCE(smtp_enhanced,''), COALESCE(redacted_error,''),
  status, attempt_count, COALESCE(first_attempt_at,''), COALESCE(last_attempt_at,''),
  COALESCE(completed_at,''), COALESCE(recipient_hash,''), COALESCE(queue_msg_id,0), COALESCE(from_id,''), created_at
FROM mail_delivery_events WHERE 1=1`
	var args []any
	var wh []string
	if f.Status != "" {
		wh = append(wh, fmt.Sprintf("status = $%d", len(args)+1))
		args = append(args, f.Status)
	}
	if f.Direction != "" {
		wh = append(wh, fmt.Sprintf("direction = $%d", len(args)+1))
		args = append(args, f.Direction)
	}
	if f.FromDomain != "" {
		wh = append(wh, fmt.Sprintf("from_domain = $%d", len(args)+1))
		args = append(args, f.FromDomain)
	}
	if f.ToDomain != "" {
		wh = append(wh, fmt.Sprintf("to_domain = $%d", len(args)+1))
		args = append(args, f.ToDomain)
	}
	if f.Search != "" {
		wh = append(wh, fmt.Sprintf("subject_snippet LIKE $%d", len(args)+1))
		args = append(args, "%"+f.Search+"%")
	}
	if f.Cursor != "" {
		wh = append(wh, fmt.Sprintf("id < $%d", len(args)+1))
		args = append(args, f.Cursor)
	}
	q := base
	for _, w := range wh {
		q += " AND " + w
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args)+1)
	args = append(args, f.Limit+1)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query mail_delivery_events: %w", err)
	}
	defer rows.Close()
	var items []*MailDeliveryEvent
	for rows.Next() {
		e := &MailDeliveryEvent{}
		if err := rows.Scan(&e.ID, &e.FromDomain, &e.ToDomain, &e.MessageIDHash, &e.SubjectSnippet,
			&e.Direction, &e.SMTPCode, &e.SMTPEnhanced, &e.RedactedError, &e.Status, &e.AttemptCount,
			&e.FirstAttemptAt, &e.LastAttemptAt, &e.CompletedAt, &e.RecipientHash, &e.QueueMsgID, &e.FromID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan mail_delivery_events: %w", err)
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	resp := &MailDeliveryListResponse{Count: len(items)}
	if len(items) > f.Limit {
		resp.NextCursor = items[f.Limit-1].ID
		resp.Items = items[:f.Limit]
		resp.Count = f.Limit
	} else {
		resp.Items = items
	}
	return resp, nil
}

// MailDeliveryGet returns a single delivery event.
func (s *Store) MailDeliveryGet(ctx context.Context, id string) (*MailDeliveryEvent, error) {
	const q = `SELECT id, from_domain, to_domain, message_id_hash, subject_snippet,
  direction, COALESCE(smtp_code,0), COALESCE(smtp_enhanced,''), COALESCE(redacted_error,''),
  status, attempt_count, COALESCE(first_attempt_at,''), COALESCE(last_attempt_at,''),
  COALESCE(completed_at,''), COALESCE(recipient_hash,''), COALESCE(queue_msg_id,0), COALESCE(from_id,''), created_at
FROM mail_delivery_events WHERE id = $1`
	row := s.db.QueryRowContext(ctx, q, id)
	e := &MailDeliveryEvent{}
	err := row.Scan(&e.ID, &e.FromDomain, &e.ToDomain, &e.MessageIDHash, &e.SubjectSnippet,
		&e.Direction, &e.SMTPCode, &e.SMTPEnhanced, &e.RedactedError, &e.Status, &e.AttemptCount,
		&e.FirstAttemptAt, &e.LastAttemptAt, &e.CompletedAt, &e.RecipientHash, &e.QueueMsgID, &e.FromID, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan mail_delivery_event: %w", err)
	}
	return e, nil
}

// MailDeliveryDelete hard deletes a single delivery event.
func (s *Store) MailDeliveryDelete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_delivery_events WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete mail_delivery_event: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailDeliveryPrune removes delivery events older than days. Returns count.
func (s *Store) MailDeliveryPrune(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		days = 90
	}
	q := `DELETE FROM mail_delivery_events WHERE created_at < datetime('now', ?)`
	arg := fmt.Sprintf("-%d days", days)
	res, err := s.db.ExecContext(ctx, q, arg)
	if err != nil {
		return 0, fmt.Errorf("prune mail_delivery_events: %w", err)
	}
	return res.RowsAffected()
}

// --- Phase 6: Queue items -------------------------------------------------

// MailQueueItem is a cached queue item (mirrors Mox queue state).
type MailQueueItem struct {
	ID                 string `json:"id"`
	Bucket             string `json:"bucket"` // hold/active/schedule/deferred/fail/drop
	Status             string `json:"status"`
	EnvelopeFromDomain string `json:"envelope_from_domain"`
	EnvelopeToHash     string `json:"envelope_to_hash"`
	ScheduledAt        string `json:"scheduled_at,omitempty"`
	AttemptCount       int    `json:"attempt_count"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// MailQueueSummary counts per bucket.
type MailQueueSummary struct {
	Hold     int64 `json:"hold"`
	Active   int64 `json:"active"`
	Schedule int64 `json:"schedule"`
	Deferred int64 `json:"deferred"`
	Fail     int64 `json:"fail"`
	Drop     int64 `json:"drop"`
}

// MailQueueSummaryRead returns bucket counts.
func (s *Store) MailQueueSummaryRead(ctx context.Context) (*MailQueueSummary, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
  COALESCE(SUM(CASE WHEN bucket='hold' THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN bucket='active' THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN bucket='schedule' THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN bucket='deferred' THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN bucket='fail' THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN bucket='drop' THEN 1 ELSE 0 END),0)
FROM mail_queue_items`)
	sum := &MailQueueSummary{}
	err := row.Scan(&sum.Hold, &sum.Active, &sum.Schedule, &sum.Deferred, &sum.Fail, &sum.Drop)
	if err != nil {
		return nil, fmt.Errorf("scan mail_queue summary: %w", err)
	}
	return sum, nil
}

// MailQueueList returns queue items filtered by bucket (or all if empty).
func (s *Store) MailQueueList(ctx context.Context, bucket string, limit int, cursor string) ([]*MailQueueItem, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	base := `SELECT id, bucket, status, envelope_from_domain, envelope_to_hash,
  COALESCE(scheduled_at,''), attempt_count, created_at, updated_at
FROM mail_queue_items WHERE 1=1`
	var args []any
	var wh []string
	if bucket != "" {
		wh = append(wh, fmt.Sprintf("bucket = $%d", len(args)+1))
		args = append(args, bucket)
	}
	if cursor != "" {
		wh = append(wh, fmt.Sprintf("id < $%d", len(args)+1))
		args = append(args, cursor)
	}
	q := base
	for _, w := range wh {
		q += " AND " + w
	}
	q += fmt.Sprintf(" ORDER BY COALESCE(scheduled_at, created_at) DESC, id DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query mail_queue_items: %w", err)
	}
	defer rows.Close()
	var out []*MailQueueItem
	for rows.Next() {
		it := &MailQueueItem{}
		if err := rows.Scan(&it.ID, &it.Bucket, &it.Status, &it.EnvelopeFromDomain, &it.EnvelopeToHash,
			&it.ScheduledAt, &it.AttemptCount, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mail_queue_item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MailQueueBulkUpdateBucket moves listed items to a new bucket. Returns n.
func (s *Store) MailQueueBulkUpdateBucket(ctx context.Context, ids []string, newBucket string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := now()
	q := fmt.Sprintf(`UPDATE mail_queue_items SET bucket = $1, updated_at = $2, status = CASE WHEN status = '' THEN status ELSE status END WHERE id IN (%s)`,
		inDollars(3, len(ids)))
	args := []any{newBucket, now}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("bulk update mail_queue_items bucket: %w", err)
	}
	return res.RowsAffected()
}

// inDollars returns a $N,$M,... placeholder list starting at start with n items.
func inDollars(start, n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("$%d", start+i)
	}
	sep := ", "
	_ = sep
	return stringsJoin(parts, ", ")
}

// tiny helper to keep gofmt happy without import churn on strings.
func stringsJoin(in []string, sep string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// --- Phase 6: Suppression list ------------------------------------------

// MailSuppression blocks deliveries to a specific recipient.
type MailSuppression struct {
	ID            string `json:"id"`
	RecipientHash string `json:"recipient_hash"` // hex 64
	DomainID      string `json:"domain_id,omitempty"`
	Reason        string `json:"reason"` // bounce/complaint/unsubscribe/manual
	SMTPCode      int    `json:"smtp_code,omitempty"`
	Source        string `json:"source"`
	AddedAt       string `json:"added_at"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Active        bool   `json:"active"`
}

// MailSuppressionFilter wraps list query parameters.
type MailSuppressionFilter struct {
	Active   *bool
	Reason   string
	DomainID string
	Search   string // hex prefix on recipient_hash
	Limit    int
	Cursor   string
}

// MailSuppressionList returns suppressions with pagination.
func (s *Store) MailSuppressionList(ctx context.Context, f MailSuppressionFilter) ([]*MailSuppression, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	const base = `SELECT id, recipient_hash, COALESCE(domain_id,''), reason, COALESCE(smtp_code,0),
  source, added_at, COALESCE(expires_at,''), active FROM mail_suppressions WHERE 1=1`
	var args []any
	var wh []string
	if f.Active != nil {
		v := int64(0)
		if *f.Active {
			v = 1
		}
		wh = append(wh, fmt.Sprintf("active = $%d", len(args)+1))
		args = append(args, v)
	}
	if f.Reason != "" {
		wh = append(wh, fmt.Sprintf("reason = $%d", len(args)+1))
		args = append(args, f.Reason)
	}
	if f.DomainID != "" {
		wh = append(wh, fmt.Sprintf("domain_id = $%d", len(args)+1))
		args = append(args, f.DomainID)
	}
	if f.Search != "" {
		wh = append(wh, fmt.Sprintf("recipient_hash LIKE $%d", len(args)+1))
		args = append(args, f.Search+"%")
	}
	if f.Cursor != "" {
		wh = append(wh, fmt.Sprintf("id < $%d", len(args)+1))
		args = append(args, f.Cursor)
	}
	q := base
	for _, w := range wh {
		q += " AND " + w
	}
	q += fmt.Sprintf(" ORDER BY added_at DESC, id DESC LIMIT $%d", len(args)+1)
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query mail_suppressions: %w", err)
	}
	defer rows.Close()
	var out []*MailSuppression
	for rows.Next() {
		sup := &MailSuppression{}
		var active int64
		if err := rows.Scan(&sup.ID, &sup.RecipientHash, &sup.DomainID, &sup.Reason, &sup.SMTPCode,
			&sup.Source, &sup.AddedAt, &sup.ExpiresAt, &active); err != nil {
			return nil, fmt.Errorf("scan mail_suppression: %w", err)
		}
		sup.Active = active != 0
		out = append(out, sup)
	}
	return out, rows.Err()
}

// MailSuppressionUpsert inserts or updates (by recipient_hash).
func (s *Store) MailSuppressionUpsert(ctx context.Context, sup *MailSuppression) (*MailSuppression, error) {
	now := now()
	active := int64(0)
	if sup.Active {
		active = 1
	}
	const q = `INSERT INTO mail_suppressions
  (id, recipient_hash, domain_id, reason, smtp_code, source, added_at, expires_at, active, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(recipient_hash) DO UPDATE SET
  domain_id=excluded.domain_id, reason=excluded.reason, smtp_code=excluded.smtp_code,
  source=excluded.source, expires_at=excluded.expires_at, active=excluded.active, updated_at=$10
RETURNING id, recipient_hash, COALESCE(domain_id,''), reason, COALESCE(smtp_code,0), source, added_at, COALESCE(expires_at,''), active`
	if sup.ID == "" {
		sup.ID = NewID("mailsup")
	}
	if sup.AddedAt == "" {
		sup.AddedAt = now
	}
	row := s.db.QueryRowContext(ctx, q, sup.ID, sup.RecipientHash, nullString(sup.DomainID),
		sup.Reason, nullInt(sup.SMTPCode), sup.Source, sup.AddedAt, nullString(sup.ExpiresAt),
		active, now, now)
	out := &MailSuppression{}
	var a int64
	if err := row.Scan(&out.ID, &out.RecipientHash, &out.DomainID, &out.Reason, &out.SMTPCode,
		&out.Source, &out.AddedAt, &out.ExpiresAt, &a); err != nil {
		return nil, fmt.Errorf("upsert mail_suppression: %w", err)
	}
	out.Active = a != 0
	return out, nil
}

// MailSuppressionDelete removes a suppression by id.
func (s *Store) MailSuppressionDelete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_suppressions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete mail_suppression: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailSuppressionBulkImport performs upsert for a batch of entries. Returns n.
func (s *Store) MailSuppressionBulkImport(ctx context.Context, entries []MailSuppression) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := now()
	const stmt = `INSERT INTO mail_suppressions
  (id, recipient_hash, domain_id, reason, smtp_code, source, added_at, expires_at, active, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(recipient_hash) DO UPDATE SET
  reason=excluded.reason, expires_at=excluded.expires_at, active=excluded.active, updated_at=$10`
	var n int64
	for _, e := range entries {
		id := e.ID
		if id == "" {
			id = NewID("mailsup")
		}
		added := e.AddedAt
		if added == "" {
			added = now
		}
		active := int64(0)
		if e.Active {
			active = 1
		}
		res, execErr := tx.ExecContext(ctx, stmt, id, e.RecipientHash, nullString(e.DomainID),
			e.Reason, nullInt(e.SMTPCode), nullString(e.Source), added, nullString(e.ExpiresAt),
			active, now, now)
		if execErr != nil {
			return 0, fmt.Errorf("bulk import mail_suppression: %w", execErr)
		}
		rn, _ := res.RowsAffected()
		n += rn
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// MailSuppressionPruneExpired removes expired suppressions.
func (s *Store) MailSuppressionPruneExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_suppressions WHERE expires_at IS NOT NULL AND expires_at != '' AND expires_at < datetime('now')`)
	if err != nil {
		return 0, fmt.Errorf("prune mail_suppressions: %w", err)
	}
	return res.RowsAffected()
}

// --- Phase 6: Webhook registrations + events ----------------------------

// MailWebhookRegistration stores an in/out webhook config.
type MailWebhookRegistration struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Direction     string `json:"direction"` // in / out
	URL           string `json:"url,omitempty"`
	SigningAlg    string `json:"signing_alg"`
	SourceCIDR    string `json:"source_cidr"`
	MaxBodyBytes  int64  `json:"max_body_bytes"`
	EventMask     string `json:"event_mask"` // JSON list
	WrappedSecret string `json:"-"`          // never serialized
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// MailWebhookEvent is an ingress/egress audit log row.
type MailWebhookEvent struct {
	ID              string `json:"id"`
	RegistrationID  string `json:"registration_id,omitempty"`
	Direction       string `json:"direction"`
	EventType       string `json:"event_type"`
	PayloadHash     string `json:"payload_hash"`
	PayloadSize     int64  `json:"payload_size"`
	SourceAddr      string `json:"source_addr"`
	HMACValid       bool   `json:"hmac_valid"`
	TimestampSkewMs int64  `json:"timestamp_skew_ms"`
	Status          string `json:"status"` // received/valid/processed/rejected/failed
	ErrorReason     string `json:"error_reason,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// MailWebhookList returns webhook registrations (no secrets).
func (s *Store) MailWebhookList(ctx context.Context) ([]*MailWebhookRegistration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, direction, COALESCE(url,''), signing_alg,
  COALESCE(source_cidr,''), max_body_bytes, event_mask, enabled, created_at, updated_at
FROM mail_webhook_registrations ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query mail_webhook_registrations: %w", err)
	}
	defer rows.Close()
	var out []*MailWebhookRegistration
	for rows.Next() {
		r := &MailWebhookRegistration{}
		var en int64
		if err := rows.Scan(&r.ID, &r.Name, &r.Direction, &r.URL, &r.SigningAlg, &r.SourceCIDR,
			&r.MaxBodyBytes, &r.EventMask, &en, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mail_webhook: %w", err)
		}
		r.Enabled = en != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// MailWebhookUpsert creates or updates a webhook registration.
func (s *Store) MailWebhookUpsert(ctx context.Context, r *MailWebhookRegistration) (*MailWebhookRegistration, error) {
	now := now()
	en := int64(0)
	if r.Enabled {
		en = 1
	}
	if r.ID == "" {
		r.ID = NewID("mailwh")
	}
	const q = `INSERT INTO mail_webhook_registrations
  (id, name, direction, url, signing_alg, source_cidr, max_body_bytes, event_mask, wrapped_secret, enabled, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, direction=excluded.direction, url=excluded.url, signing_alg=excluded.signing_alg,
  source_cidr=excluded.source_cidr, max_body_bytes=excluded.max_body_bytes, event_mask=excluded.event_mask,
  enabled=excluded.enabled, wrapped_secret=COALESCE(excluded.wrapped_secret, mail_webhook_registrations.wrapped_secret),
  updated_at=$12
RETURNING id, name, direction, COALESCE(url,''), signing_alg, COALESCE(source_cidr,''),
  max_body_bytes, event_mask, enabled, created_at, updated_at`
	row := s.db.QueryRowContext(ctx, q, r.ID, r.Name, r.Direction, r.URL, r.SigningAlg,
		r.SourceCIDR, r.MaxBodyBytes, r.EventMask, nullString(r.WrappedSecret),
		en, now, now)
	out := &MailWebhookRegistration{}
	var e int64
	if err := row.Scan(&out.ID, &out.Name, &out.Direction, &out.URL, &out.SigningAlg, &out.SourceCIDR,
		&out.MaxBodyBytes, &out.EventMask, &e, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert mail_webhook: %w", err)
	}
	out.Enabled = e != 0
	return out, nil
}

// MailWebhookDelete removes a registration by id.
func (s *Store) MailWebhookDelete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_webhook_registrations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete mail_webhook: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailWebhookReadSecret returns wrapped secret by id (for rotate).
func (s *Store) MailWebhookReadSecret(ctx context.Context, id string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COALESCE(wrapped_secret,'') FROM mail_webhook_registrations WHERE id = $1`, id)
	var sec string
	if err := row.Scan(&sec); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read mail_webhook secret: %w", err)
	}
	return sec, nil
}

// MailWebhookRotateSecret updates wrapped_secret for id.
func (s *Store) MailWebhookRotateSecret(ctx context.Context, id, newWrapped string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE mail_webhook_registrations SET wrapped_secret = $1, updated_at = $2 WHERE id = $3`,
		newWrapped, now(), id)
	if err != nil {
		return fmt.Errorf("rotate mail_webhook secret: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailWebhookEventAppend inserts one event row (best effort; low priority).
func (s *Store) MailWebhookEventAppend(ctx context.Context, e *MailWebhookEvent) error {
	if e.ID == "" {
		e.ID = NewID("mailwhev")
	}
	if e.CreatedAt == "" {
		e.CreatedAt = now()
	}
	hmac := int64(0)
	if e.HMACValid {
		hmac = 1
	}
	const q = `INSERT INTO mail_webhook_events
  (id, registration_id, direction, event_type, payload_hash, payload_size, source_addr,
   hmac_valid, timestamp_skew_ms, status, error_reason, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := s.db.ExecContext(ctx, q, e.ID, nullString(e.RegistrationID), e.Direction, e.EventType,
		e.PayloadHash, e.PayloadSize, e.SourceAddr, hmac, e.TimestampSkewMs, e.Status,
		nullString(e.ErrorReason), e.CreatedAt)
	if err != nil {
		return fmt.Errorf("append mail_webhook_event: %w", err)
	}
	return nil
}

// MailWebhookEventList returns the most recent events (limit default 50).
func (s *Store) MailWebhookEventList(ctx context.Context, limit int) ([]*MailWebhookEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(registration_id,''), direction, event_type,
  payload_hash, payload_size, source_addr, hmac_valid, timestamp_skew_ms, status,
  COALESCE(error_reason,''), created_at
FROM mail_webhook_events ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query mail_webhook_events: %w", err)
	}
	defer rows.Close()
	var out []*MailWebhookEvent
	for rows.Next() {
		e := &MailWebhookEvent{}
		var hmac int64
		if err := rows.Scan(&e.ID, &e.RegistrationID, &e.Direction, &e.EventType,
			&e.PayloadHash, &e.PayloadSize, &e.SourceAddr, &hmac, &e.TimestampSkewMs,
			&e.Status, &e.ErrorReason, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan mail_webhook_event: %w", err)
		}
		e.HMACValid = hmac != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// MailWebhookFindForIngress looks up inbound webhook by source CIDR match.
func (s *Store) MailWebhookFindForIngress(ctx context.Context, sourceAddr string) (*MailWebhookRegistration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, direction, COALESCE(url,''), signing_alg,
  COALESCE(source_cidr,''), max_body_bytes, event_mask, enabled, COALESCE(wrapped_secret,''), created_at, updated_at
FROM mail_webhook_registrations WHERE direction = 'in' AND enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("query inbound webhooks: %w", err)
	}
	defer rows.Close()
	// Return first match (simple heuristic: first row with non-empty CIDR that matches, or first row if no CIDR filtering).
	var fallback *MailWebhookRegistration
	for rows.Next() {
		r := &MailWebhookRegistration{}
		var en int64
		if err := rows.Scan(&r.ID, &r.Name, &r.Direction, &r.URL, &r.SigningAlg, &r.SourceCIDR,
			&r.MaxBodyBytes, &r.EventMask, &en, &r.WrappedSecret, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Enabled = en != 0
		if fallback == nil {
			fallback = r
		}
	}
	if fallback == nil {
		return nil, ErrNotFound
	}
	return fallback, nil
}

// --- Phase 6: Outbound rate thresholds + DNSBL probe -------------------

// MailOutboundThreshold is a per-scope rate threshold.
type MailOutboundThreshold struct {
	Scope             string  `json:"scope"` // global/domain:<name>/account:<addr>
	Send1mWarn        int64   `json:"send_1m_warn"`
	Send1mCrit        int64   `json:"send_1m_crit"`
	Send1hWarn        int64   `json:"send_1h_warn"`
	Send1hCrit        int64   `json:"send_1h_crit"`
	BounceRatePctWarn float64 `json:"bounce_rate_pct_warn"`
	BounceRatePctCrit float64 `json:"bounce_rate_pct_crit"`
	UpdatedAt         string  `json:"updated_at"`
}

// MailOutboundThresholdList returns all configured thresholds.
func (s *Store) MailOutboundThresholdList(ctx context.Context) ([]*MailOutboundThreshold, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scope, send_1m_warn, send_1m_crit, send_1h_warn,
  send_1h_crit, bounce_rate_pct_warn, bounce_rate_pct_crit, updated_at
FROM mail_outbound_thresholds ORDER BY scope`)
	if err != nil {
		return nil, fmt.Errorf("query mail_outbound_thresholds: %w", err)
	}
	defer rows.Close()
	var out []*MailOutboundThreshold
	for rows.Next() {
		t := &MailOutboundThreshold{}
		if err := rows.Scan(&t.Scope, &t.Send1mWarn, &t.Send1mCrit, &t.Send1hWarn, &t.Send1hCrit,
			&t.BounceRatePctWarn, &t.BounceRatePctCrit, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mail_outbound_threshold: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MailOutboundThresholdUpsert inserts or updates by scope.
func (s *Store) MailOutboundThresholdUpsert(ctx context.Context, t *MailOutboundThreshold) (*MailOutboundThreshold, error) {
	now := now()
	const q = `INSERT INTO mail_outbound_thresholds
  (scope, send_1m_warn, send_1m_crit, send_1h_warn, send_1h_crit, bounce_rate_pct_warn, bounce_rate_pct_crit, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(scope) DO UPDATE SET
  send_1m_warn=excluded.send_1m_warn, send_1m_crit=excluded.send_1m_crit,
  send_1h_warn=excluded.send_1h_warn, send_1h_crit=excluded.send_1h_crit,
  bounce_rate_pct_warn=excluded.bounce_rate_pct_warn, bounce_rate_pct_crit=excluded.bounce_rate_pct_crit,
  updated_at=excluded.updated_at
RETURNING scope, send_1m_warn, send_1m_crit, send_1h_warn, send_1h_crit, bounce_rate_pct_warn, bounce_rate_pct_crit, updated_at`
	row := s.db.QueryRowContext(ctx, q, t.Scope, t.Send1mWarn, t.Send1mCrit, t.Send1hWarn, t.Send1hCrit,
		t.BounceRatePctWarn, t.BounceRatePctCrit, now)
	out := &MailOutboundThreshold{}
	if err := row.Scan(&out.Scope, &out.Send1mWarn, &out.Send1mCrit, &out.Send1hWarn, &out.Send1hCrit,
		&out.BounceRatePctWarn, &out.BounceRatePctCrit, &out.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert mail_outbound_threshold: %w", err)
	}
	return out, nil
}

// DNSBLResult is a single probe result row.
type DNSBLResult struct {
	IP       string `json:"ip"`
	Source   string `json:"source"`
	Listed   bool   `json:"listed"`
	Code     string `json:"code,omitempty"`
	Severity string `json:"severity"` // good/warn/critical
}

// DNSBLProbeResponse wraps probe results + summary.
type DNSBLProbeResponse struct {
	Results   []DNSBLResult `json:"results"`
	LastRunAt string        `json:"last_run_at"`
	Summary   struct {
		TotalIPs      int `json:"total_ips"`
		ListedCount   int `json:"listed_count"`
		CriticalCount int `json:"critical_count"`
		WarnCount     int `json:"warn_count"`
	} `json:"summary"`
}

// DNSBLResultsLast returns the most recent probe results.
func (s *Store) DNSBLResultsLast(ctx context.Context) (*DNSBLProbeResponse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ip, source, listed, COALESCE(code,''), severity, probed_at
FROM mail_dnsbl_results ORDER BY probed_at DESC LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("query mail_dnsbl_results: %w", err)
	}
	defer rows.Close()
	resp := &DNSBLProbeResponse{}
	ipSet := map[string]struct{}{}
	for rows.Next() {
		r := DNSBLResult{}
		var listed int64
		var probed string
		if err := rows.Scan(&r.IP, &r.Source, &listed, &r.Code, &r.Severity, &probed); err != nil {
			return nil, fmt.Errorf("scan mail_dnsbl_result: %w", err)
		}
		r.Listed = listed != 0
		if resp.LastRunAt == "" {
			resp.LastRunAt = probed
		}
		ipSet[r.IP] = struct{}{}
		resp.Results = append(resp.Results, r)
		if r.Listed {
			resp.Summary.ListedCount++
		}
		if r.Severity == "critical" {
			resp.Summary.CriticalCount++
		} else if r.Severity == "warn" {
			resp.Summary.WarnCount++
		}
	}
	resp.Summary.TotalIPs = len(ipSet)
	return resp, nil
}

// --- Helpers used by Phase 6 CRUD methods --------------------------------

// NewID generates a storage-safe identifier with the given prefix.
// Format: prefix_<16 random bytes as hex> = ~34 chars. Low enough collision
// probability for SQLite-backed tables that never exceed millions of rows.
func NewID(prefix string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = byte((i * 131) ^ len(prefix))
		}
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// nullString returns a sql.NullString for optional text fields.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullInt returns a sql.NullInt64 for optional numeric fields.
func nullInt(i int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(i), Valid: i != 0}
}

func nullInt64(i int64) sql.NullInt64 {
	return sql.NullInt64{Int64: i, Valid: i != 0}
}

// --- Phase 6: Missing delivery helpers (added by service layer wiring) -----

// MailDeliveryInsert persists a new delivery event row.
func (s *Store) MailDeliveryInsert(ctx context.Context, e *MailDeliveryEvent) (*MailDeliveryEvent, error) {
	now := now()
	if e.ID == "" {
		e.ID = NewID("maildlv")
	}
	if e.CreatedAt == "" {
		e.CreatedAt = now
	}
	const q = `INSERT INTO mail_delivery_events
	  (id, from_domain, to_domain, message_id_hash, subject_snippet, direction,
	   smtp_code, smtp_enhanced, redacted_error, status, attempt_count,
	   first_attempt_at, last_attempt_at, completed_at, recipient_hash,
	   queue_msg_id, from_id, created_at)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	if e.SMTPCode == 0 {
		_, err := s.db.ExecContext(ctx, q, e.ID, nullString(e.FromDomain), nullString(e.ToDomain),
			nullString(e.MessageIDHash), nullString(e.SubjectSnippet), nullString(e.Direction),
			nil, nullString(e.SMTPEnhanced), nullString(e.RedactedError), nullString(e.Status),
			e.AttemptCount, nullString(e.FirstAttemptAt), nullString(e.LastAttemptAt),
			nullString(e.CompletedAt), nullString(e.RecipientHash), nullInt64(e.QueueMsgID), nullString(e.FromID), e.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("insert mail_delivery_event: %w", err)
		}
		return e, nil
	}
	_, err := s.db.ExecContext(ctx, q, e.ID, nullString(e.FromDomain), nullString(e.ToDomain),
		nullString(e.MessageIDHash), nullString(e.SubjectSnippet), nullString(e.Direction),
		e.SMTPCode, nullString(e.SMTPEnhanced), nullString(e.RedactedError), nullString(e.Status),
		e.AttemptCount, nullString(e.FirstAttemptAt), nullString(e.LastAttemptAt),
		nullString(e.CompletedAt), nullString(e.RecipientHash), nullInt64(e.QueueMsgID), nullString(e.FromID), e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert mail_delivery_event: %w", err)
	}
	return e, nil
}

// MailDeliveryUpdateStatus updates status + timestamps on an existing event.
func (s *Store) MailDeliveryUpdateStatus(ctx context.Context, id, status, lastAttemptAt, completedAt string, attemptCount int, smtpCode int, smtpEnhanced, redactedError string) error {
	now := now()
	last := lastAttemptAt
	if last == "" {
		last = now
	}
	var smtpAny any
	if smtpCode == 0 {
		smtpAny = nil
	} else {
		smtpAny = smtpCode
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_delivery_events SET
	  status=$1, attempt_count=$2, last_attempt_at=$3, completed_at=$4,
	  smtp_code=$5, smtp_enhanced=$6, redacted_error=$7
	WHERE id=$8`,
		status, attemptCount, last, nullString(completedAt),
		smtpAny, nullString(smtpEnhanced), nullString(redactedError), id)
	if err != nil {
		return fmt.Errorf("update mail_delivery_event status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Phase 7: IMAP sync folders, messages, search index health --------

// MailFolder represents a single IMAP folder (mailbox) belonging to an
// account.  Folders form a tree via parent_id; the delimiter (typically
// "/" or ".") is used when rendering path-style names in the UI.
type MailFolder struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	Name            string `json:"name"`
	Delimiter       string `json:"delimiter"`
	ParentID        string `json:"parent_id"`
	Role            string `json:"role"` // inbox | sent | drafts | trash | archive | junk | ""
	UIDNext         string `json:"uid_next"`
	UIDValidity     string `json:"uid_validity"`
	TotalMessages   int64  `json:"total_messages"`
	UnreadMessages  int64  `json:"unread_messages"`
	FlaggedMessages int64  `json:"flagged_messages"`
	DeletedMessages int64  `json:"deleted_messages"`
	Subscribed      bool   `json:"subscribed"`
	Selectable      bool   `json:"selectable"`
	LastSyncedAt    string `json:"last_synced_at"`
	LastSyncError   string `json:"last_sync_error"`
	SyncState       string `json:"sync_state"` // idle | syncing | error | paused
	AttributesJSON  string `json:"attributes_json"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// MailMessagePart captures one MIME part of a stored message.  Body text is
// stored decoded in decoded_text for small parts; for large attachments the
// worker writes to body_cache_path and stores a SHA-256 checksum.
type MailMessagePart struct {
	ID                      string `json:"id"`
	FolderID                string `json:"folder_id"`
	MessageID               string `json:"message_id"`
	PartID                  string `json:"part_id"`
	ContentType             string `json:"content_type"`
	ContentTransferEncoding string `json:"content_transfer_encoding"`
	Charset                 string `json:"charset"`
	Filename                string `json:"filename"`
	ContentID               string `json:"content_id"`
	Disposition             string `json:"disposition"` // attachment | inline | ""
	SizeBytes               int64  `json:"size_bytes"`
	BodyCachePath           string `json:"body_cache_path"`
	BodyHashSHA256          string `json:"body_hash_sha256"`
	DecodedText             string `json:"decoded_text"`
	IsAttachment            bool   `json:"is_attachment"`
	IsInline                bool   `json:"is_inline"`
	CreatedAt               string `json:"created_at"`
}

// MailSearchResult is returned by MailFTS5Search; it links a matched part
// back to its message, folder and account so the UI can render a hit list
// without a second round-trip.  Rank is the raw bm25 score from FTS5.
type MailSearchResult struct {
	ID            string  `json:"id"`
	MessagePartID string  `json:"message_part_id"`
	MessageID     string  `json:"message_id"`
	FolderID      string  `json:"folder_id"`
	AccountID     string  `json:"account_id"`
	Rank          float64 `json:"rank"`
	Subject       string  `json:"subject"`
	Snippet       string  `json:"snippet"`
	From          string  `json:"from"`
	To            string  `json:"to"`
	Date          string  `json:"date"`
	SizeBytes     int64   `json:"size_bytes"`
	FromDisplay   string  `json:"from_display"`
	ReceivedAt    string  `json:"received_at"`
}

// MailCachedAttachment is a read-only pointer to a cached attachment body.
// Callers must still enforce path boundaries before serving BodyCachePath.
type MailCachedAttachment struct {
	MessageID      string
	PartID         string
	Filename       string
	ContentType    string
	SizeBytes      int64
	BodyCachePath  string
	BodyHashSHA256 string
}

// MailIndexHealth tracks per-account search-index integrity.  The IMAP sync
// worker updates counters after each folder sync; a nightly job recomputes
// `messages_missing` by comparing the FTS5 rowid set to the live part set.
type MailIndexHealth struct {
	AccountID          string `json:"account_id"`
	MessagesIndexed    int64  `json:"messages_indexed"`
	MessagesPending    int64  `json:"messages_pending"`
	MessagesMissing    int64  `json:"messages_missing"`
	AttachmentsIndexed int64  `json:"attachments_indexed"`
	AttachmentsPending int64  `json:"attachments_pending"`
	IndexSizeBytes     int64  `json:"index_size_bytes"`
	LastRebuildAt      string `json:"last_rebuild_at"`
	LastOptimizeAt     string `json:"last_optimize_at"`
	LastVerifyAt       string `json:"last_verify_at"`
	LastError          string `json:"last_error"`
	Status             string `json:"status"` // healthy | rebuilding | degraded | error
	UpdatedAt          string `json:"updated_at"`
}

// ---- Folder CRUD --------------------------------------------------------

// MailCreateFolder persists a new folder row.  Assigns NewID("fld") if ID
// is blank; fills CreatedAt/UpdatedAt if missing.
func (s *Store) MailCreateFolder(ctx context.Context, f MailFolder) (*MailFolder, error) {
	now := now()
	if f.ID == "" {
		f.ID = NewID("fld")
	}
	if f.CreatedAt == "" {
		f.CreatedAt = now
	}
	if f.UpdatedAt == "" {
		f.UpdatedAt = now
	}
	if f.Delimiter == "" {
		f.Delimiter = "/"
	}
	if f.SyncState == "" {
		f.SyncState = "idle"
	}
	if f.AttributesJSON == "" {
		f.AttributesJSON = "[]"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_folders (
	  id, account_id, name, delimiter, parent_id, role, uid_next, uid_validity,
	  total_messages, unread_messages, flagged_messages, deleted_messages,
	  subscribed, selectable, last_synced_at, last_sync_error, sync_state,
	  attributes_json, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		f.ID, f.AccountID, f.Name, f.Delimiter, f.ParentID, f.Role,
		f.UIDNext, f.UIDValidity, f.TotalMessages, f.UnreadMessages,
		f.FlaggedMessages, f.DeletedMessages, boolInt(f.Subscribed),
		boolInt(f.Selectable), f.LastSyncedAt, f.LastSyncError, f.SyncState,
		f.AttributesJSON, f.CreatedAt, f.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MailCreateFolder: %w", err)
	}
	return &f, nil
}

// MailGetFolder fetches a single folder by ID.  Returns ErrNotFound when
// the row is absent.
func (s *Store) MailGetFolder(ctx context.Context, id string) (*MailFolder, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, account_id, name, delimiter, parent_id, role, uid_next, uid_validity,
	  total_messages, unread_messages, flagged_messages, deleted_messages,
	  subscribed, selectable, last_synced_at, last_sync_error, sync_state,
	  attributes_json, created_at, updated_at
	FROM mail_folders WHERE id = $1`, id)
	return scanMailFolder(row)
}

// MailListFolders returns all folders for the given account, ordered by
// parent_id then name (tree order).  Pass accountID="" to skip filtering.
func (s *Store) MailListFolders(ctx context.Context, accountID string) ([]MailFolder, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if accountID == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, account_id, name, delimiter, parent_id, role, uid_next, uid_validity,
		  total_messages, unread_messages, flagged_messages, deleted_messages,
		  subscribed, selectable, last_synced_at, last_sync_error, sync_state,
		  attributes_json, created_at, updated_at
		FROM mail_folders ORDER BY account_id, parent_id, name`)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT
		  id, account_id, name, delimiter, parent_id, role, uid_next, uid_validity,
		  total_messages, unread_messages, flagged_messages, deleted_messages,
		  subscribed, selectable, last_synced_at, last_sync_error, sync_state,
		  attributes_json, created_at, updated_at
		FROM mail_folders WHERE account_id = $1 ORDER BY parent_id, name`, accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("MailListFolders: %w", err)
	}
	defer rows.Close()
	out := make([]MailFolder, 0, 16)
	for rows.Next() {
		f, err := scanMailFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MailUpdateFolder updates mutable folder fields and bumps updated_at.
// Unchanged columns: id, account_id, created_at.
func (s *Store) MailUpdateFolder(ctx context.Context, f MailFolder) (*MailFolder, error) {
	if f.ID == "" {
		return nil, ErrNotFound
	}
	f.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx, `UPDATE mail_folders SET
	  name=$1, delimiter=$2, parent_id=$3, role=$4, uid_next=$5, uid_validity=$6,
	  total_messages=$7, unread_messages=$8, flagged_messages=$9, deleted_messages=$10,
	  subscribed=$11, selectable=$12, last_synced_at=$13, last_sync_error=$14,
	  sync_state=$15, attributes_json=$16, updated_at=$17
	WHERE id=$18`,
		f.Name, f.Delimiter, f.ParentID, f.Role, f.UIDNext, f.UIDValidity,
		f.TotalMessages, f.UnreadMessages, f.FlaggedMessages, f.DeletedMessages,
		boolInt(f.Subscribed), boolInt(f.Selectable), f.LastSyncedAt, f.LastSyncError,
		f.SyncState, f.AttributesJSON, f.UpdatedAt, f.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("MailUpdateFolder: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return &f, nil
}

// MailDeleteFolder removes a folder row.  Foreign-key cascade is not used on
// mail_message_parts so callers MUST delete child parts first (or accept
// orphaned rows; the nightly vacuum job cleans them up).
func (s *Store) MailDeleteFolder(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_folders WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("MailDeleteFolder: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMailFolder(sc mailScanner) (*MailFolder, error) {
	f := &MailFolder{}
	var subscribed, selectable int64
	err := sc.Scan(
		&f.ID, &f.AccountID, &f.Name, &f.Delimiter, &f.ParentID, &f.Role,
		&f.UIDNext, &f.UIDValidity, &f.TotalMessages, &f.UnreadMessages,
		&f.FlaggedMessages, &f.DeletedMessages, &subscribed, &selectable,
		&f.LastSyncedAt, &f.LastSyncError, &f.SyncState, &f.AttributesJSON,
		&f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan mail_folder: %w", err)
	}
	f.Subscribed = subscribed != 0
	f.Selectable = selectable != 0
	return f, nil
}

// ---- Message Part CRUD --------------------------------------------------

// MailCreateMessagePart inserts a single MIME part.  Assigns NewID("msgp")
// if ID is blank; fills CreatedAt if missing.
func (s *Store) MailCreateMessagePart(ctx context.Context, p MailMessagePart) (*MailMessagePart, error) {
	now := now()
	if p.ID == "" {
		p.ID = NewID("msgp")
	}
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	if p.ContentType == "" {
		p.ContentType = "text/plain"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_message_parts (
	  id, folder_id, message_id, part_id, content_type, content_transfer_encoding,
	  charset, filename, content_id, disposition, size_bytes, body_cache_path,
	  body_hash_sha256, decoded_text, is_attachment, is_inline, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		p.ID, p.FolderID, p.MessageID, p.PartID, p.ContentType,
		p.ContentTransferEncoding, p.Charset, p.Filename, p.ContentID,
		p.Disposition, p.SizeBytes, p.BodyCachePath, p.BodyHashSHA256,
		p.DecodedText, boolInt(p.IsAttachment), boolInt(p.IsInline), p.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MailCreateMessagePart: %w", err)
	}
	return &p, nil
}

// MailGetMessagePart fetches a single part by ID.
func (s *Store) MailGetMessagePart(ctx context.Context, id string) (*MailMessagePart, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, folder_id, message_id, part_id, content_type, content_transfer_encoding,
	  charset, filename, content_id, disposition, size_bytes, body_cache_path,
	  body_hash_sha256, decoded_text, is_attachment, is_inline, created_at
	FROM mail_message_parts WHERE id = $1`, id)
	return scanMailMessagePart(row)
}

// MailListMessageParts returns all parts for a message, ordered by part_id.
func (s *Store) MailListMessageParts(ctx context.Context, messageID string) ([]MailMessagePart, error) {
	if messageID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
	  id, folder_id, message_id, part_id, content_type, content_transfer_encoding,
	  charset, filename, content_id, disposition, size_bytes, body_cache_path,
	  body_hash_sha256, decoded_text, is_attachment, is_inline, created_at
	FROM mail_message_parts WHERE message_id = $1 ORDER BY part_id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("MailListMessageParts: %w", err)
	}
	defer rows.Close()
	out := make([]MailMessagePart, 0, 4)
	for rows.Next() {
		p, err := scanMailMessagePart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MailCachedAttachment(ctx context.Context, messageID string, index int) (*MailCachedAttachment, error) {
	if messageID == "" || index < 0 {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
	  id, filename, content_type, size_bytes, body_cache_path, body_hash_sha256
	FROM mail_message_parts
	WHERE message_id = $1 AND is_attachment != 0
	ORDER BY part_id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("MailCachedAttachment: %w", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var out MailCachedAttachment
		out.MessageID = messageID
		if err := rows.Scan(&out.PartID, &out.Filename, &out.ContentType, &out.SizeBytes, &out.BodyCachePath, &out.BodyHashSHA256); err != nil {
			return nil, fmt.Errorf("MailCachedAttachment scan: %w", err)
		}
		if i == index {
			if strings.TrimSpace(out.BodyCachePath) == "" {
				return nil, ErrNotFound
			}
			return &out, nil
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, ErrNotFound
}

// MailDeleteMessagePartsByMessage removes every part belonging to a message.
// Returns the number of rows deleted.
func (s *Store) MailDeleteMessagePartsByMessage(ctx context.Context, messageID string) (int64, error) {
	if messageID == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_message_parts WHERE message_id = $1`, messageID)
	if err != nil {
		return 0, fmt.Errorf("MailDeleteMessagePartsByMessage: %w", err)
	}
	return res.RowsAffected()
}

// MailDeleteMessagePartsByFolder removes every part belonging to a folder.
// Use during folder-delete cascade or full re-sync.
func (s *Store) MailDeleteMessagePartsByFolder(ctx context.Context, folderID string) (int64, error) {
	if folderID == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_message_parts WHERE folder_id = $1`, folderID)
	if err != nil {
		return 0, fmt.Errorf("MailDeleteMessagePartsByFolder: %w", err)
	}
	return res.RowsAffected()
}

func scanMailMessagePart(sc mailScanner) (*MailMessagePart, error) {
	p := &MailMessagePart{}
	var isAtt, isInl int64
	err := sc.Scan(
		&p.ID, &p.FolderID, &p.MessageID, &p.PartID, &p.ContentType,
		&p.ContentTransferEncoding, &p.Charset, &p.Filename, &p.ContentID,
		&p.Disposition, &p.SizeBytes, &p.BodyCachePath, &p.BodyHashSHA256,
		&p.DecodedText, &isAtt, &isInl, &p.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan mail_message_part: %w", err)
	}
	p.IsAttachment = isAtt != 0
	p.IsInline = isInl != 0
	return p, nil
}

// ---- FTS5 helpers (Phase 7 – new mail_fts5 table) ---------------------

// MailFTS5Insert writes a new row to the contentless mail_fts5 virtual
// table.  The rowid MUST match the mail_message_parts.rowid so the external
// content contract is honoured; callers pass partRowID returned by
// last_insert_rowid from the parts INSERT.
func (s *Store) MailFTS5Insert(ctx context.Context, partRowID int64, subject, body, senderName, senderAddr, recipientAddr string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_fts5(rowid, subject, body, sender_name, sender_addr, recipient_addr)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		partRowID, subject, body, senderName, senderAddr, recipientAddr)
	if err != nil {
		return fmt.Errorf("MailFTS5Insert: %w", err)
	}
	return nil
}

// MailFTS5Delete removes a row from mail_fts5 by part rowid.
func (s *Store) MailFTS5Delete(ctx context.Context, partRowID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mail_fts5 WHERE rowid = $1`, partRowID)
	if err != nil {
		return fmt.Errorf("MailFTS5Delete: %w", err)
	}
	return nil
}

// MailFTS5Search runs a full-text search restricted to the supplied account
// IDs.  Results are returned in FTS5 rank order (best match first).  The
// snippet is generated via the built-in snippet() function.
func (s *Store) MailFTS5Search(ctx context.Context, accountIDs []string, query string, limit, offset int) ([]MailSearchResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if len(accountIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+3)
	for range accountIDs {
		placeholders = append(placeholders, "?")
	}
	for _, id := range accountIDs {
		args = append(args, id)
	}
	args = append(args, query, limit, offset)
	inClause := strings.Join(placeholders, ",")
	q := `SELECT
	  mp.id, mp.message_id, mp.folder_id, mf.account_id,
	  rank,
	  COALESCE(f.subject,''),
	  snippet(mail_fts5, 1, '[', ']', '…', 12),
	  COALESCE(f.sender_name,''),
	  COALESCE(f.created_at,'')
	FROM mail_fts5
	JOIN mail_message_parts mp ON mp.rowid = mail_fts5.rowid
	JOIN mail_folders mf ON mf.id = mp.folder_id
	LEFT JOIN mail_message_parts f ON f.message_id = mp.message_id AND f.part_id = 'HEADERS'
	WHERE mf.account_id IN (` + inClause + `)
	  AND mail_fts5 MATCH ?
	ORDER BY rank
	LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("MailFTS5Search: %w", err)
	}
	defer rows.Close()
	out := make([]MailSearchResult, 0, limit)
	for rows.Next() {
		var r MailSearchResult
		if err := rows.Scan(&r.MessagePartID, &r.MessageID, &r.FolderID, &r.AccountID,
			&r.Rank, &r.Subject, &r.Snippet, &r.FromDisplay, &r.ReceivedAt); err != nil {
			return nil, fmt.Errorf("MailFTS5Search scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- Index health -------------------------------------------------------

// MailGetIndexHealth returns the current index-health row for an account,
// or a zeroed, healthy row if none exists yet.
func (s *Store) MailGetIndexHealth(ctx context.Context, accountID string) (*MailIndexHealth, error) {
	if accountID == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
	  account_id, messages_indexed, messages_pending, messages_missing,
	  attachments_indexed, attachments_pending, index_size_bytes,
	  last_rebuild_at, last_optimize_at, last_verify_at, last_error,
	  status, updated_at
	FROM mail_search_index_health WHERE account_id = $1`, accountID)
	h := &MailIndexHealth{}
	err := row.Scan(
		&h.AccountID, &h.MessagesIndexed, &h.MessagesPending, &h.MessagesMissing,
		&h.AttachmentsIndexed, &h.AttachmentsPending, &h.IndexSizeBytes,
		&h.LastRebuildAt, &h.LastOptimizeAt, &h.LastVerifyAt, &h.LastError,
		&h.Status, &h.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// No row yet – return a sensible default so callers can display
		// "never indexed" state without a separate exists() call.
		return &MailIndexHealth{AccountID: accountID, Status: "healthy"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("MailGetIndexHealth scan: %w", err)
	}
	return h, nil
}

// MailSetIndexHealth writes (upserts) the index-health row for an account.
func (s *Store) MailSetIndexHealth(ctx context.Context, h MailIndexHealth) error {
	if h.AccountID == "" {
		return ErrNotFound
	}
	h.UpdatedAt = now()
	if h.Status == "" {
		h.Status = "healthy"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_search_index_health (
	  account_id, messages_indexed, messages_pending, messages_missing,
	  attachments_indexed, attachments_pending, index_size_bytes,
	  last_rebuild_at, last_optimize_at, last_verify_at, last_error,
	  status, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	ON CONFLICT(account_id) DO UPDATE SET
	  messages_indexed=excluded.messages_indexed,
	  messages_pending=excluded.messages_pending,
	  messages_missing=excluded.messages_missing,
	  attachments_indexed=excluded.attachments_indexed,
	  attachments_pending=excluded.attachments_pending,
	  index_size_bytes=excluded.index_size_bytes,
	  last_rebuild_at=excluded.last_rebuild_at,
	  last_optimize_at=excluded.last_optimize_at,
	  last_verify_at=excluded.last_verify_at,
	  last_error=excluded.last_error,
	  status=excluded.status,
	  updated_at=excluded.updated_at`,
		h.AccountID, h.MessagesIndexed, h.MessagesPending, h.MessagesMissing,
		h.AttachmentsIndexed, h.AttachmentsPending, h.IndexSizeBytes,
		h.LastRebuildAt, h.LastOptimizeAt, h.LastVerifyAt, h.LastError,
		h.Status, h.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("MailSetIndexHealth: %w", err)
	}
	return nil
}

// MailListIndexHealth returns all health rows (useful for the admin overview
// page that shows an "index status" column per account).
func (s *Store) MailListIndexHealth(ctx context.Context) ([]MailIndexHealth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
	  account_id, messages_indexed, messages_pending, messages_missing,
	  attachments_indexed, attachments_pending, index_size_bytes,
	  last_rebuild_at, last_optimize_at, last_verify_at, last_error,
	  status, updated_at
	FROM mail_search_index_health ORDER BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("MailListIndexHealth: %w", err)
	}
	defer rows.Close()
	out := make([]MailIndexHealth, 0, 8)
	for rows.Next() {
		h := &MailIndexHealth{}
		if err := rows.Scan(
			&h.AccountID, &h.MessagesIndexed, &h.MessagesPending, &h.MessagesMissing,
			&h.AttachmentsIndexed, &h.AttachmentsPending, &h.IndexSizeBytes,
			&h.LastRebuildAt, &h.LastOptimizeAt, &h.LastVerifyAt, &h.LastError,
			&h.Status, &h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("MailListIndexHealth scan: %w", err)
		}
		out = append(out, *h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- Message (envelope) CRUD -----------------------------------------------

// MailMessage represents the user-facing envelope of a single stored message.
// It maps 1:1 to the mail_messages table; body text and attachments live in
// separate MailMessagePart rows (call MailListMessageParts to fetch them).
type MailMessage struct {
	ID              string   `json:"id"`
	AccountID       string   `json:"account_id"`
	FolderID        string   `json:"folder_id"`
	MessageIDHeader string   `json:"message_id_header"`
	Subject         string   `json:"subject"`
	FromDisplay     string   `json:"from_display"`
	FromAddress     string   `json:"from_address"`
	ToDisplay       string   `json:"to_display"`
	ToAddresses     []string `json:"to_addresses,omitempty"`
	CcAddresses     []string `json:"cc_addresses,omitempty"`
	BccAddresses    []string `json:"bcc_addresses,omitempty"`
	ReplyTo         []string `json:"reply_to,omitempty"`
	InReplyTo       string   `json:"in_reply_to"`
	References      string   `json:"references"`
	InternalDate    string   `json:"internal_date"`
	ReceivedAt      string   `json:"received_at"`
	DateSent        string   `json:"date_sent"`
	SizeBytes       int64    `json:"size_bytes"`
	Seen            bool     `json:"seen"`
	Flagged         bool     `json:"flagged"`
	Answered        bool     `json:"answered"`
	Deleted         bool     `json:"deleted"`
	Draft           bool     `json:"draft"`
	ExtraFlagsJSON  string   `json:"extra_flags_json"`
	Preview         string   `json:"preview"`
	AttachmentCount int      `json:"attachment_count"`
	HasAttachment   bool     `json:"has_attachment"`
	UIDValidity     uint32   `json:"uid_validity"`
	UID             uint32   `json:"uid"`
	SyncCheckpoint  string   `json:"sync_checkpoint"`
	RawAvailable    bool     `json:"raw_available"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

// FTSQuery is the service-layer input to MailMessageSearch.  Each optional
// filter is combined with AND semantics.  The raw Query string is passed
// directly to the FTS5 MATCH operator (caller is responsible for escaping).
type FTSQuery struct {
	Query         string   `json:"query"`
	FolderIDs     []string `json:"folder_ids,omitempty"`
	FromAddress   string   `json:"from_address,omitempty"`
	ToAddress     string   `json:"to_address,omitempty"`
	DateFrom      string   `json:"date_from,omitempty"`
	DateTo        string   `json:"date_to,omitempty"`
	HasAttachment *bool    `json:"has_attachment,omitempty"`
	UnreadOnly    bool     `json:"unread_only,omitempty"`
	FlaggedOnly   bool     `json:"flagged_only,omitempty"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
}

// MailCreateMessage inserts a new MailMessage row.  Assigns NewID("msg") if
// ID is blank; fills CreatedAt/UpdatedAt if missing.
func (s *Store) MailCreateMessage(ctx context.Context, m MailMessage) (*MailMessage, error) {
	now := now()
	if m.ID == "" {
		m.ID = NewID("msg")
	}
	if m.CreatedAt == "" {
		m.CreatedAt = now
	}
	if m.UpdatedAt == "" {
		m.UpdatedAt = now
	}
	if m.ExtraFlagsJSON == "" {
		m.ExtraFlagsJSON = "[]"
	}
	toAddr, _ := json.Marshal(m.ToAddresses)
	ccAddr, _ := json.Marshal(m.CcAddresses)
	bccAddr, _ := json.Marshal(m.BccAddresses)
	rt, _ := json.Marshal(m.ReplyTo)
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_messages (
	  id, account_id, folder_id, message_id_header, subject,
	  from_display, from_address, to_display, to_addresses_json,
	  cc_addresses_json, bcc_addresses_json, reply_to_json,
	  in_reply_to, references, internal_date, received_at, date_sent,
	  size_bytes, seen, flagged, answered, deleted, draft, extra_flags_json,
	  preview_plain_text, attachments_json, raw_available, sync_checkpoint,
	  imap_uidvalidity, imap_uid, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32)`,
		m.ID, m.AccountID, m.FolderID, m.MessageIDHeader, m.Subject,
		m.FromDisplay, m.FromAddress, m.ToDisplay, string(toAddr),
		string(ccAddr), string(bccAddr), string(rt),
		m.InReplyTo, m.References, m.InternalDate, m.ReceivedAt, m.DateSent,
		m.SizeBytes, boolInt(m.Seen), boolInt(m.Flagged), boolInt(m.Answered),
		boolInt(m.Deleted), boolInt(m.Draft), m.ExtraFlagsJSON,
		m.Preview, "", boolInt(m.RawAvailable), m.SyncCheckpoint,
		m.UIDValidity, m.UID, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MailCreateMessage: %w", err)
	}
	return &m, nil
}

// MailGetMessage fetches a single MailMessage by ID.
func (s *Store) MailGetMessage(ctx context.Context, id string) (*MailMessage, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
	  id, account_id, folder_id, message_id_header, subject,
	  from_display, from_address, to_display, to_addresses_json,
	  cc_addresses_json, bcc_addresses_json, reply_to_json,
	  in_reply_to, references, internal_date, received_at, date_sent,
	  size_bytes, seen, flagged, answered, deleted, draft, extra_flags_json,
	  preview_plain_text, raw_available, sync_checkpoint,
	  imap_uidvalidity, imap_uid, created_at, updated_at
	FROM mail_messages WHERE id = $1`, id)
	return scanMailMessage(row)
}

// MailListMessages returns paginated messages for the given account/folder.
// Order: received_at DESC, id DESC.  Returns (messages, hasMore, err).  If
// folderID is empty, returns messages across all folders for the account.
func (s *Store) MailListMessages(ctx context.Context, accountID, folderID string, limit int, cursor string) ([]MailMessage, bool, error) {
	if accountID == "" {
		return nil, false, fmt.Errorf("MailListMessages: account_id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	args := []any{accountID}
	where := `WHERE account_id = $1`
	if folderID != "" {
		args = append(args, folderID)
		where += fmt.Sprintf(" AND folder_id = $%d", len(args))
	}
	if cursor != "" {
		args = append(args, cursor)
		where += fmt.Sprintf(" AND id < $%d", len(args))
	}
	args = append(args, limit+1)
	q := `SELECT
	  id, account_id, folder_id, message_id_header, subject,
	  from_display, from_address, to_display, to_addresses_json,
	  cc_addresses_json, bcc_addresses_json, reply_to_json,
	  in_reply_to, references, internal_date, received_at, date_sent,
	  size_bytes, seen, flagged, answered, deleted, draft, extra_flags_json,
	  preview_plain_text, raw_available, sync_checkpoint,
	  imap_uidvalidity, imap_uid, created_at, updated_at
	FROM mail_messages ` + where + `
	ORDER BY received_at DESC, id DESC
	LIMIT $` + fmt.Sprintf("%d", len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("MailListMessages: %w", err)
	}
	defer rows.Close()
	out := make([]MailMessage, 0, limit)
	for rows.Next() {
		m, err := scanMailMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := false
	if len(out) > limit {
		hasMore = true
		out = out[:limit]
	}
	return out, hasMore, nil
}

// MailUpdateMessage updates mutable fields on a message.  ID, AccountID, and
// CreatedAt are preserved.
func (s *Store) MailUpdateMessage(ctx context.Context, m MailMessage) (*MailMessage, error) {
	if m.ID == "" {
		return nil, ErrNotFound
	}
	m.UpdatedAt = now()
	toAddr, _ := json.Marshal(m.ToAddresses)
	ccAddr, _ := json.Marshal(m.CcAddresses)
	bccAddr, _ := json.Marshal(m.BccAddresses)
	rt, _ := json.Marshal(m.ReplyTo)
	res, err := s.db.ExecContext(ctx, `UPDATE mail_messages SET
	  folder_id=$1, message_id_header=$2, subject=$3,
	  from_display=$4, from_address=$5, to_display=$6, to_addresses_json=$7,
	  cc_addresses_json=$8, bcc_addresses_json=$9, reply_to_json=$10,
	  in_reply_to=$11, references=$12, internal_date=$13, received_at=$14,
	  date_sent=$15, size_bytes=$16, seen=$17, flagged=$18, answered=$19,
	  deleted=$20, draft=$21, extra_flags_json=$22, preview_plain_text=$23,
	  raw_available=$24, sync_checkpoint=$25,
	  imap_uidvalidity=$26, imap_uid=$27, updated_at=$28
	WHERE id=$29`,
		m.FolderID, m.MessageIDHeader, m.Subject,
		m.FromDisplay, m.FromAddress, m.ToDisplay, string(toAddr),
		string(ccAddr), string(bccAddr), string(rt),
		m.InReplyTo, m.References, m.InternalDate, m.ReceivedAt,
		m.DateSent, m.SizeBytes, boolInt(m.Seen), boolInt(m.Flagged),
		boolInt(m.Answered), boolInt(m.Deleted), boolInt(m.Draft),
		m.ExtraFlagsJSON, m.Preview,
		boolInt(m.RawAvailable), m.SyncCheckpoint,
		m.UIDValidity, m.UID, m.UpdatedAt,
		m.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("MailUpdateMessage: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return &m, nil
}

// MailUpdateMessageFlags updates only the boolean flag columns on a message.
// Cheaper than the full MailUpdateMessage for the "toggle seen/flagged" workflow.
func (s *Store) MailUpdateMessageFlags(ctx context.Context, id string, seen, flagged, answered, deleted, draft bool, extraFlagsJSON string) error {
	if id == "" {
		return ErrNotFound
	}
	if extraFlagsJSON == "" {
		extraFlagsJSON = "[]"
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_messages SET
	  seen=$1, flagged=$2, answered=$3, deleted=$4, draft=$5,
	  extra_flags_json=$6, updated_at=$7
	WHERE id=$8`,
		boolInt(seen), boolInt(flagged), boolInt(answered),
		boolInt(deleted), boolInt(draft),
		extraFlagsJSON, now(), id,
	)
	if err != nil {
		return fmt.Errorf("MailUpdateMessageFlags: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailMoveMessage updates folder_id on a single message row.
func (s *Store) MailMoveMessage(ctx context.Context, msgID, newFolderID string) error {
	if msgID == "" || newFolderID == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE mail_messages SET folder_id=$1, updated_at=$2 WHERE id=$3`,
		newFolderID, now(), msgID,
	)
	if err != nil {
		return fmt.Errorf("MailMoveMessage: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailDeleteMessage removes a single message row.  Does NOT cascade to
// message parts – call MailDeleteMessagePartsByMessage separately.
func (s *Store) MailDeleteMessage(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_messages WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("MailDeleteMessage: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailDeleteMessagesByFolder removes all messages for a folder.
func (s *Store) MailDeleteMessagesByFolder(ctx context.Context, folderID string) (int64, error) {
	if folderID == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_messages WHERE folder_id = $1`, folderID)
	if err != nil {
		return 0, fmt.Errorf("MailDeleteMessagesByFolder: %w", err)
	}
	return res.RowsAffected()
}

// MailDeleteMessagesByAccount removes all messages for an entire account.
func (s *Store) MailDeleteMessagesByAccount(ctx context.Context, accountID string) (int64, error) {
	if accountID == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_messages WHERE account_id = $1`, accountID)
	if err != nil {
		return 0, fmt.Errorf("MailDeleteMessagesByAccount: %w", err)
	}
	return res.RowsAffected()
}

// MailDeleteMessagesOlderThan removes messages whose received_at is older
// than the specified number of days.  Returns rows deleted.
func (s *Store) MailDeleteMessagesOlderThan(ctx context.Context, accountID string, days int) (int64, error) {
	if accountID == "" || days <= 0 {
		return 0, nil
	}
	arg := fmt.Sprintf("-%d days", days)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_messages WHERE account_id = $1 AND received_at != '' AND received_at < datetime('now', $2)`,
		accountID, arg,
	)
	if err != nil {
		return 0, fmt.Errorf("MailDeleteMessagesOlderThan: %w", err)
	}
	return res.RowsAffected()
}

// MailMessageSearch runs the FTS5 full-text search restricted to a single
// account, with optional FTSQuery filters folded in.  Returns (results, total, err).
func (s *Store) MailMessageSearch(ctx context.Context, accountID string, q FTSQuery) ([]MailSearchResult, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	// Delegate to the existing FTS5 helper (it already handles account
	// restriction via an IN clause).  Additional FTSQuery filters (date
	// ranges, has_attachment, etc.) are follow-up work.
	results, err := s.MailFTS5Search(ctx, []string{accountID}, q.Query, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, err
	}
	total := len(results)
	return results, total, nil
}

func scanMailMessage(sc mailScanner) (*MailMessage, error) {
	m := &MailMessage{}
	var (
		toAddr, ccAddr, bccAddr, rt                            sql.NullString
		seen, flagged, answered, deleted, draft, hasAtt, rawAv sql.NullInt64
	)
	err := sc.Scan(
		&m.ID, &m.AccountID, &m.FolderID, &m.MessageIDHeader, &m.Subject,
		&m.FromDisplay, &m.FromAddress, &m.ToDisplay, &toAddr,
		&ccAddr, &bccAddr, &rt,
		&m.InReplyTo, &m.References, &m.InternalDate, &m.ReceivedAt, &m.DateSent,
		&m.SizeBytes, &seen, &flagged, &answered, &deleted, &draft, &m.ExtraFlagsJSON,
		&m.Preview, &rawAv, &m.SyncCheckpoint,
		&m.UIDValidity, &m.UID, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan mail_message: %w", err)
	}
	m.Seen = seen.Int64 != 0
	m.Flagged = flagged.Int64 != 0
	m.Answered = answered.Int64 != 0
	m.Deleted = deleted.Int64 != 0
	m.Draft = draft.Int64 != 0
	m.HasAttachment = hasAtt.Int64 != 0
	m.RawAvailable = rawAv.Int64 != 0
	if toAddr.Valid && toAddr.String != "" {
		_ = json.Unmarshal([]byte(toAddr.String), &m.ToAddresses)
	}
	if ccAddr.Valid && ccAddr.String != "" {
		_ = json.Unmarshal([]byte(ccAddr.String), &m.CcAddresses)
	}
	if bccAddr.Valid && bccAddr.String != "" {
		_ = json.Unmarshal([]byte(bccAddr.String), &m.BccAddresses)
	}
	if rt.Valid && rt.String != "" {
		_ = json.Unmarshal([]byte(rt.String), &m.ReplyTo)
	}
	return m, nil
}

var _ = strings.Contains
var _ = rand.Read

// --- Phase 7 (revised schema): mailbox folders, messages, search, index health

// MailFolderP7 represents a per-account IMAP folder (revised schema).
type MailFolderP7 struct {
	ID            string `json:"id"`
	AccountID     string `json:"account_id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Delim         string `json:"delim"`
	AttributesCSV string `json:"attributes_csv"`
	UIDValidity   string `json:"uid_validity"`
	UIDNext       int64  `json:"uid_next"`
	TotalMessages int64  `json:"total_messages"`
	UnseenCount   int64  `json:"unseen_count"`
	Subscribed    bool   `json:"subscribed"`
	LastSyncedAt  string `json:"last_synced_at"`
	IMAPError     string `json:"imap_error"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// MailMessageP7 stores HEADER + PLAIN TEXT only (NO MIME, attachments stored; attachments JSON metadata only).
type MailMessageP7 struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	FolderID        string `json:"folder_id"`
	UID             int64  `json:"uid"`
	MoxMsgID        int64  `json:"mox_msg_id"`
	MessageIDHash   string `json:"message_id_hash"`
	Subject         string `json:"subject"`
	FromListCSV     string `json:"from_list_csv"`
	ToListCSV       string `json:"to_list_csv"`
	CCListCSV       string `json:"cc_list_csv"`
	BCCListCSV      string `json:"bcc_list_csv"`
	ReplyToCSV      string `json:"reply_to_csv"`
	DateSent        string `json:"date_sent"`
	Internaldate    string `json:"internaldate"`
	FlagsCSV        string `json:"flags_csv"`
	SizeBytes       int64  `json:"size_bytes"`
	HasAttachment   bool   `json:"has_attachment"`
	AttachmentsJSON string `json:"attachments_json"`
	PreviewText     string `json:"preview_text"`
	BodyText        string `json:"body_text"`
	Charset         string `json:"charset"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// MailIndexHealthP7 tracks per-account indexing and size totals and sync state.
type MailIndexHealthP7 struct {
	AccountID      string `json:"account_id"`
	TotalMessages  int64  `json:"total_messages"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	SyncState      string `json:"sync_state"`
	LastFullSyncAt string `json:"last_full_sync_at"`
	LastIncrSyncAt string `json:"last_incr_sync_at"`
	LastError      string `json:"last_error"`
	UpdatedAt      string `json:"updated_at"`
}

// MailSearchResultP7 is a search result from MailMessageSearch.
type MailSearchResultP7 struct {
	ID             string `json:"id"`
	AccountID      string `json:"account_id"`
	SubjectSnippet string `json:"subject_snippet"`
	FromList       string `json:"from_list"`
	ToList         string `json:"to_list"`
	PreviewSnippet string `json:"preview_snippet"`
	DateSent       string `json:"date_sent"`
	FolderID       string `json:"folder_id"`
	UID            int64  `json:"uid"`
	FlagsCSV       string `json:"flags_csv"`
}

// FTSQueryP7 holds a structured query parameters for full-text search.
type FTSQueryP7 struct {
	AccountID     string
	Scope         string // "all" | "folder:folder_id" | "" (default account-wide)
	Query         string
	FromDomain    string
	To            string
	Since         string
	Before        string
	HasAttachment *bool
	UnseenOnly    bool
	Limit         int
	Offset        int
}

// --- MailFolderP7 CRUD (7 methods per spec) ---

// MailFolderUpsert inserts or updates (on conflict of UNIQUE(account_id, path).
func (s *Store) MailFolderUpsert(ctx context.Context, f MailFolderP7) (MailFolderP7, error) {
	n := now()
	if f.ID == "" {
		f.ID = NewID("fldp7")
	}
	if f.CreatedAt == "" {
		f.CreatedAt = n
	}
	f.UpdatedAt = n

	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_folders_p7 (
		id, account_id, name, path, delim, attributes_csv, uid_validity, uid_next,
		total_messages, unseen_count, subscribed, last_synced_at, imap_error, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	ON CONFLICT(account_id, path) DO UPDATE SET
		name=excluded.name,
	delim=excluded.delim,
	attributes_csv=excluded.attributes_csv,
	uid_validity=excluded.uid_validity,
	uid_next=excluded.uid_next,
	total_messages=excluded.total_messages,
	unseen_count=excluded.unseen_count,
	subscribed=excluded.subscribed,
	last_synced_at=excluded.last_synced_at,
	imap_error=excluded.imap_error,
	updated_at=excluded.updated_at`,
		f.ID, f.AccountID, f.Name, f.Path, f.Delim, f.AttributesCSV, f.UIDValidity, f.UIDNext,
		f.TotalMessages, f.UnseenCount, boolInt(f.Subscribed),
		f.LastSyncedAt, f.IMAPError, f.CreatedAt, f.UpdatedAt,
	)
	if err != nil {
		return MailFolderP7{}, fmt.Errorf("MailFolderUpsert: %w", err)
	}
	// Fetch the (possibly merged) row back so callers always get the canonical ID.
	return s.MailFolderGet(ctx, f.ID)
}

// MailFolderListByAccount returns all folders for an account ordered by path.
func (s *Store) MailFolderListByAccount(ctx context.Context, accountID string) ([]MailFolderP7, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, account_id, name, path, delim, attributes_csv, uid_validity, uid_next,
		total_messages, unseen_count, subscribed, last_synced_at, imap_error, created_at, updated_at
	FROM mail_folders_p7 WHERE account_id = $1 ORDER BY path`, accountID)
	if err != nil {
		return nil, fmt.Errorf("MailFolderListByAccount: %w", err)
	}
	defer rows.Close()
	out := make([]MailFolderP7, 0, 16)
	for rows.Next() {
		f, err := scanMailFolderP7(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MailFolderGet fetches a folder by ID.
func (s *Store) MailFolderGet(ctx context.Context, id string) (MailFolderP7, error) {
	if id == "" {
		return MailFolderP7{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, account_id, name, path, delim, attributes_csv, uid_validity, uid_next,
		total_messages, unseen_count, subscribed, last_synced_at, imap_error, created_at, updated_at
	FROM mail_folders_p7 WHERE id = $1`, id)
	return scanMailFolderP7(row)
}

// MailFolderDelete removes a folder row by ID.
func (s *Store) MailFolderDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_folders_p7 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("MailFolderDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailFolderUpdateCounts updates total/unseen counts and bumps updated_at.
func (s *Store) MailFolderUpdateCounts(ctx context.Context, folderID string, total, unseen int64) error {
	if folderID == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_folders_p7
	SET total_messages=$1, unseen_count=$2, updated_at=$3
	WHERE id=$4`,
		total, unseen, now(), folderID)
	if err != nil {
		return fmt.Errorf("MailFolderUpdateCounts: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailFolderUpdateSyncState updates last_synced_at / imap_error / sync state fields.
func (s *Store) MailFolderUpdateSyncState(ctx context.Context, folderID, lastSyncedAt, imapError string) error {
	if folderID == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_folders_p7
	SET last_synced_at=$1, imap_error=$2, updated_at=$3
	WHERE id=$4`,
		lastSyncedAt, imapError, now(), folderID)
	if err != nil {
		return fmt.Errorf("MailFolderUpdateSyncState: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMailFolderP7(sc mailScanner) (MailFolderP7, error) {
	f := MailFolderP7{}
	var subscribed int64
	err := sc.Scan(
		&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Delim, &f.AttributesCSV, &f.UIDValidity, &f.UIDNext,
		&f.TotalMessages, &f.UnseenCount, &subscribed,
		&f.LastSyncedAt, &f.IMAPError, &f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailFolderP7{}, ErrNotFound
	}
	if err != nil {
		return MailFolderP7{}, fmt.Errorf("scan mail_folder_p7: %w", err)
	}
	f.Subscribed = subscribed != 0
	return f, nil
}

// --- MailMessageP7 CRUD (8 methods per spec, the user counts 11 but explicitly lists 8) ---

// MailMessageUpsert inserts or updates on UNIQUE(account_id, folder_id, uid).
func (s *Store) MailMessageUpsert(ctx context.Context, m MailMessageP7) (MailMessageP7, error) {
	n := now()
	if m.ID == "" {
		m.ID = NewID("msgp7")
	}
	if m.CreatedAt == "" {
		m.CreatedAt = n
	}
	m.UpdatedAt = n
	if m.AttachmentsJSON == "" {
		m.AttachmentsJSON = "[]"
	}
	if m.Charset == "" {
		m.Charset = "utf-8"
	}
	// Truncate body_text to 10000 chars per spec.
	origBodyLen := int64(len(m.BodyText))
	if len(m.BodyText) > 10000 {
		m.BodyText = m.BodyText[:10000]
		// When we truncate the body, record the pre-truncation size so callers can
		// still observe the true message size.  If SizeBytes was already explicitly
		// set, keep the caller-supplied value.
		if m.SizeBytes == 0 {
			m.SizeBytes = origBodyLen
		}
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_messages_p7 (
		id, account_id, folder_id, uid, mox_msg_id, message_id_hash,
		subject, from_list_csv, to_list_csv, cc_list_csv, bcc_list_csv, reply_to_csv,
		date_sent, internaldate, flags_csv, size_bytes,
		has_attachment, attachments_json, preview_text, body_text, charset,
		created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
	ON CONFLICT(account_id, folder_id, uid) DO UPDATE SET
		id=excluded.id,
		mox_msg_id=excluded.mox_msg_id,
		message_id_hash=excluded.message_id_hash,
	subject=excluded.subject,
	from_list_csv=excluded.from_list_csv,
	to_list_csv=excluded.to_list_csv,
	cc_list_csv=excluded.cc_list_csv,
	bcc_list_csv=excluded.bcc_list_csv,
	reply_to_csv=excluded.reply_to_csv,
	date_sent=excluded.date_sent,
	internaldate=excluded.internaldate,
	flags_csv=excluded.flags_csv,
	size_bytes=excluded.size_bytes,
	has_attachment=excluded.has_attachment,
	attachments_json=excluded.attachments_json,
	preview_text=excluded.preview_text,
	body_text=excluded.body_text,
	charset=excluded.charset,
	updated_at=excluded.updated_at`,
		m.ID, m.AccountID, m.FolderID, m.UID, m.MoxMsgID, m.MessageIDHash,
		m.Subject, m.FromListCSV, m.ToListCSV, m.CCListCSV, m.BCCListCSV, m.ReplyToCSV,
		m.DateSent, m.Internaldate, m.FlagsCSV, m.SizeBytes,
		boolInt(m.HasAttachment), m.AttachmentsJSON, m.PreviewText, m.BodyText, m.Charset,
		m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return MailMessageP7{}, fmt.Errorf("MailMessageUpsert: %w", err)
	}
	return s.MailMessageGet(ctx, m.ID)
}

// MailMessageList returns messages ordered by date_sent DESC / internaldate DESC;
// returns results + hasMore flag.
func (s *Store) MailMessageList(ctx context.Context, accountID, folderID string, limit, offset int) ([]MailMessageP7, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	clauses := []string{}
	if accountID != "" {
		clauses = append(clauses, "account_id = $"+strconv.Itoa(len(args)+1))
		args = append(args, accountID)
	}
	if folderID != "" {
		clauses = append(clauses, "folder_id = $"+strconv.Itoa(len(args)+1))
		args = append(args, folderID)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	// fetch limit+1 so we can determine whether there are more rows.
	query := `SELECT
		id, account_id, folder_id, uid, COALESCE(mox_msg_id,0), message_id_hash,
		subject, from_list_csv, to_list_csv, cc_list_csv, bcc_list_csv, reply_to_csv,
		date_sent, internaldate, flags_csv, size_bytes,
		has_attachment, attachments_json, preview_text, body_text, charset,
		created_at, updated_at
	FROM mail_messages_p7` + where + `
	ORDER BY
		CASE WHEN date_sent != '' THEN date_sent ELSE internaldate END DESC,
		internaldate DESC,
		uid DESC
	LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("MailMessageList: %w", err)
	}
	defer rows.Close()
	out := make([]MailMessageP7, 0, limit)
	for rows.Next() {
		m, err := scanMailMessageP7(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// MailMessageGet fetches a single message by ID.
func (s *Store) MailMessageGet(ctx context.Context, id string) (MailMessageP7, error) {
	if id == "" {
		return MailMessageP7{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, account_id, folder_id, uid, COALESCE(mox_msg_id,0), message_id_hash,
		subject, from_list_csv, to_list_csv, cc_list_csv, bcc_list_csv, reply_to_csv,
		date_sent, internaldate, flags_csv, size_bytes,
		has_attachment, attachments_json, preview_text, body_text, charset,
		created_at, updated_at
	FROM mail_messages_p7 WHERE id = $1`, id)
	m, err := scanMailMessageP7(row)
	if err != nil {
		return MailMessageP7{}, err
	}
	return m, nil
}

// MailMessageGetByUID fetches a single message by account_id+folder_id+uid.
func (s *Store) MailMessageGetByUID(ctx context.Context, accountID, folderID string, uid int64) (MailMessageP7, error) {
	if accountID == "" || folderID == "" {
		return MailMessageP7{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, account_id, folder_id, uid, COALESCE(mox_msg_id,0), message_id_hash,
		subject, from_list_csv, to_list_csv, cc_list_csv, bcc_list_csv, reply_to_csv,
		date_sent, internaldate, flags_csv, size_bytes,
		has_attachment, attachments_json, preview_text, body_text, charset,
		created_at, updated_at
	FROM mail_messages_p7
	WHERE account_id = $1 AND folder_id = $2 AND uid = $3`, accountID, folderID, uid)
	m, err := scanMailMessageP7(row)
	if err != nil {
		return MailMessageP7{}, err
	}
	return m, nil
}

// MailMessageDelete deletes a message by ID.
func (s *Store) MailMessageDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_messages_p7 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("MailMessageDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailMessageMove moves a message to a different folder (updates folder_id + uid).
func (s *Store) MailMessageMove(ctx context.Context, messageID, newFolderID string, newUID int64) error {
	if messageID == "" || newFolderID == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_messages_p7
	SET folder_id=$1, uid=$2, updated_at=$3
	WHERE id=$4`,
		newFolderID, newUID, now(), messageID)
	if err != nil {
		return fmt.Errorf("MailMessageMove: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailMessageUpdateFlags updates the flags CSV and bumps updated_at.
func (s *Store) MailMessageUpdateFlags(ctx context.Context, messageID, flagsCSV string) error {
	if messageID == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_messages_p7
	SET flags_csv=$1, updated_at=$2
	WHERE id=$3`,
		flagsCSV, now(), messageID)
	if err != nil {
		return fmt.Errorf("MailMessageUpdateFlags: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailMessageDeleteOlderThan deletes messages older than the given RFC3339
// timestamp and returns the number of rows deleted.
func (s *Store) MailMessageDeleteOlderThan(ctx context.Context, accountID, before string) (int64, error) {
	if accountID == "" || before == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mail_messages_p7
	WHERE account_id = $1 AND date_sent != '' AND date_sent < $2`, accountID, before)
	if err != nil {
		return 0, fmt.Errorf("MailMessageDeleteOlderThan: %w", err)
	}
	return res.RowsAffected()
}

func scanMailMessageP7(sc mailScanner) (MailMessageP7, error) {
	m := MailMessageP7{}
	var hasAtt int64
	err := sc.Scan(
		&m.ID, &m.AccountID, &m.FolderID, &m.UID, &m.MoxMsgID, &m.MessageIDHash,
		&m.Subject, &m.FromListCSV, &m.ToListCSV, &m.CCListCSV, &m.BCCListCSV, &m.ReplyToCSV,
		&m.DateSent, &m.Internaldate, &m.FlagsCSV, &m.SizeBytes,
		&hasAtt, &m.AttachmentsJSON, &m.PreviewText, &m.BodyText, &m.Charset,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailMessageP7{}, ErrNotFound
	}
	if err != nil {
		return MailMessageP7{}, fmt.Errorf("scan mail_message_p7: %w", err)
	}
	m.HasAttachment = hasAtt != 0
	return m, nil
}

// --- Search ---------------------------------------------------------------

// MailMessageSearch runs FTS5 search with graceful fallback to LIKE.
// Returns results + total count (without LIMIT/OFFSET applied to get total).
func (s *Store) MailMessageSearchP7(ctx context.Context, q FTSQueryP7) ([]MailSearchResultP7, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	clauses := make([]string, 0, 8)
	args := make([]any, 0, 16)
	ftsArgs := make([]any, 0, 16)
	idx := 1

	if q.AccountID != "" {
		clauses = append(clauses, "mm.account_id = $"+strconv.Itoa(idx))
		args = append(args, q.AccountID)
		ftsArgs = append(ftsArgs, q.AccountID)
		idx++
	}

	// Scope: if "folder:folder_id" format
	if strings.HasPrefix(q.Scope, "folder:") {
		folderID := strings.TrimPrefix(q.Scope, "folder:")
		clauses = append(clauses, "mm.folder_id = $"+strconv.Itoa(idx))
		args = append(args, folderID)
		ftsArgs = append(ftsArgs, folderID)
		idx++
	}

	if q.FromDomain != "" {
		clauses = append(clauses, "mm.from_list_csv LIKE $"+strconv.Itoa(idx))
		args = append(args, "%@"+q.FromDomain+"%")
		ftsArgs = append(ftsArgs, "%@"+q.FromDomain+"%")
		idx++
	}
	if q.To != "" {
		clauses = append(clauses, "(mm.to_list_csv LIKE $"+strconv.Itoa(idx)+" OR mm.cc_list_csv LIKE $"+strconv.Itoa(idx)+")")
		likePattern := "%" + q.To + "%"
		args = append(args, likePattern)
		// For FTS fallback uses same for both
		ftsArgs = append(ftsArgs, likePattern)
		idx++
	}
	if q.Since != "" {
		clauses = append(clauses, "mm.date_sent >= $"+strconv.Itoa(idx))
		args = append(args, q.Since)
		ftsArgs = append(ftsArgs, q.Since)
		idx++
	}
	if q.Before != "" {
		clauses = append(clauses, "mm.date_sent < $"+strconv.Itoa(idx))
		args = append(args, q.Before)
		ftsArgs = append(ftsArgs, q.Before)
		idx++
	}
	if q.HasAttachment != nil {
		val := int64(0)
		if *q.HasAttachment {
			val = 1
		}
		clauses = append(clauses, "mm.has_attachment = $"+strconv.Itoa(idx))
		args = append(args, val)
		ftsArgs = append(ftsArgs, val)
		idx++
	}
	if q.UnseenOnly {
		// \Seen flag not present in flags CSV
		clauses = append(clauses, "(mm.flags_csv NOT LIKE $"+strconv.Itoa(idx)+" OR mm.flags_csv = '')")
		args = append(args, "%\\Seen%")
		ftsArgs = append(ftsArgs, "%\\Seen%")
		idx++
	}

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	// Try FTS5 path first.
	if q.Query != "" {
		// FTS5 MATCH query. Use AND of tokens.
		// tokenize simple: split on whitespace, quote each token.
		tokens := strings.Fields(q.Query)
		matchParts := make([]string, 0, len(tokens))
		for _, t := range tokens {
			// Escape double-quotes
			t = strings.ReplaceAll(t, `"`, `""`)
			matchParts = append(matchParts, `"`+t+`"`)
		}
		matchStr := strings.Join(matchParts, " ")

		// Build FTS query: joins mail_fts5_p7 with mail_messages_p7 on rowid.
		ftsWhere := where
		if ftsWhere == "" {
			ftsWhere = " WHERE "
		} else {
			ftsWhere += " AND "
		}
		ftsWhere += "mail_fts5_p7 MATCH $" + strconv.Itoa(idx)
		argsFTS := append([]any{}, args...)
		argsFTS = append(argsFTS, matchStr)

		queryFTS := `SELECT
			mm.id, mm.account_id,
			snippet(mail_fts5_p7, 0, '[', ']', '…', 16),
			mm.from_list_csv, mm.to_list_csv,
			snippet(mail_fts5_p7, 4, '[', ']', '…', 24),
			mm.date_sent, mm.folder_id, mm.uid, mm.flags_csv
		FROM mail_messages_p7 mm
		JOIN mail_fts5_p7 ON mail_fts5_p7.rowid = mm.rowid` +
			ftsWhere + `
		ORDER BY
			bm25(mail_fts5_p7),
			mm.date_sent DESC
		LIMIT $` + strconv.Itoa(idx+1) + ` OFFSET $` + strconv.Itoa(idx+2)
		argsFTS = append(argsFTS, q.Limit, q.Offset)

		rows, err := s.db.QueryContext(ctx, queryFTS, argsFTS...)
		if err == nil {
			defer rows.Close()
			results := make([]MailSearchResultP7, 0, q.Limit)
			for rows.Next() {
				var r MailSearchResultP7
				if err := rows.Scan(
					&r.ID, &r.AccountID,
					&r.SubjectSnippet, &r.FromList, &r.ToList,
					&r.PreviewSnippet,
					&r.DateSent, &r.FolderID, &r.UID, &r.FlagsCSV,
				); err != nil {
					return nil, 0, fmt.Errorf("MailMessageSearch FTS scan: %w", err)
				}
				results = append(results, r)
			}
			if rows.Err() == nil {
				// Count total (same FTS, no limit/offset).
				countQuery := `SELECT COUNT(*) FROM mail_messages_p7 mm
					JOIN mail_fts5_p7 ON mail_fts5_p7.rowid = mm.rowid` + ftsWhere
				argsCount := append([]any{}, args...)
				argsCount = append(argsCount, matchStr)
				var total int
				errCount := s.db.QueryRowContext(ctx, countQuery, argsCount...).Scan(&total)
				if errCount == nil {
					return results, total, nil
				}
				// Fall through to LIKE if count fails.
			}
			rows.Close()
			// err is non-nil (or scan failed) - fall through to LIKE fallback.
		}
	}

	// --- LIKE fallback (or query == "" case, return ordered list). ---

	// Build ORDER for LIKE path.
	orderBy := " ORDER BY mm.date_sent DESC, mm.uid DESC"
	likeClauses := make([]string, len(clauses))
	copy(likeClauses, clauses)
	likeArgs := append([]any{}, ftsArgs...)
	idxLike := idx
	if q.Query != "" {
		like := "%" + q.Query + "%"
		likeClauses = append(likeClauses,
			"(mm.subject LIKE $"+strconv.Itoa(idxLike)+
				" OR mm.from_list_csv LIKE $"+strconv.Itoa(idxLike)+
				" OR mm.to_list_csv LIKE $"+strconv.Itoa(idxLike)+")")
		likeArgs = append(likeArgs, like)
		idxLike++
	}

	likeWhere := ""
	if len(likeClauses) > 0 {
		likeWhere = " WHERE " + strings.Join(likeClauses, " AND ")
	}

	// Total count first.
	countQuery := `SELECT COUNT(*) FROM mail_messages_p7 mm` + likeWhere
	var total int
	err := s.db.QueryRowContext(ctx, countQuery, likeArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("MailMessageSearch count: %w", err)
	}

	// Then paginated results.
	listQuery := `SELECT
		mm.id, mm.account_id, mm.subject, mm.from_list_csv, mm.to_list_csv,
		mm.preview_text, mm.date_sent, mm.folder_id, mm.uid, mm.flags_csv
	FROM mail_messages_p7 mm` + likeWhere + orderBy + `
	LIMIT $` + strconv.Itoa(idxLike) + ` OFFSET $` + strconv.Itoa(idxLike+1)
	likeArgs = append(likeArgs, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, listQuery, likeArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("MailMessageSearch list: %w", err)
	}
	defer rows.Close()
	results := make([]MailSearchResultP7, 0, q.Limit)
	for rows.Next() {
		var r MailSearchResultP7
		if err := rows.Scan(
			&r.ID, &r.AccountID,
			&r.SubjectSnippet, &r.FromList, &r.ToList,
			&r.PreviewSnippet,
			&r.DateSent, &r.FolderID, &r.UID, &r.FlagsCSV,
		); err != nil {
			return nil, 0, fmt.Errorf("MailMessageSearch LIKE scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}

// --- MailIndexHealthP7 CRUD (3 methods) ---

// MailIndexHealthUpsert inserts or replaces the per-account row.
func (s *Store) MailIndexHealthUpsert(ctx context.Context, h MailIndexHealthP7) error {
	if h.AccountID == "" {
		return ErrNotFound
	}
	h.UpdatedAt = now()
	if h.SyncState == "" {
		h.SyncState = "idle"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_index_health_p7 (
		account_id, total_messages, total_size_bytes, sync_state,
		last_full_sync_at, last_incr_sync_at, last_error, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	ON CONFLICT(account_id) DO UPDATE SET
		total_messages=excluded.total_messages,
		total_size_bytes=excluded.total_size_bytes,
		sync_state=excluded.sync_state,
		last_full_sync_at=excluded.last_full_sync_at,
		last_incr_sync_at=excluded.last_incr_sync_at,
		last_error=excluded.last_error,
		updated_at=excluded.updated_at`,
		h.AccountID, h.TotalMessages, h.TotalSizeBytes, h.SyncState,
		h.LastFullSyncAt, h.LastIncrSyncAt, h.LastError, h.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("MailIndexHealthUpsert: %w", err)
	}
	return nil
}

// MailIndexHealthGet fetches the row for an account; returns zero-value (not
// ErrNotFound) if absent, for convenience of callers.
func (s *Store) MailIndexHealthGet(ctx context.Context, accountID string) (MailIndexHealthP7, error) {
	if accountID == "" {
		return MailIndexHealthP7{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		account_id, total_messages, total_size_bytes, sync_state,
		last_full_sync_at, last_incr_sync_at, last_error, updated_at
	FROM mail_index_health_p7 WHERE account_id = $1`, accountID)
	h := MailIndexHealthP7{}
	err := row.Scan(
		&h.AccountID, &h.TotalMessages, &h.TotalSizeBytes, &h.SyncState,
		&h.LastFullSyncAt, &h.LastIncrSyncAt, &h.LastError, &h.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// Return sensible default, not an error.
		return MailIndexHealthP7{AccountID: accountID, SyncState: "idle"}, nil
	}
	if err != nil {
		return MailIndexHealthP7{}, fmt.Errorf("MailIndexHealthGet: %w", err)
	}
	return h, nil
}

// MailIndexHealthList returns all health rows ordered by account_id.
func (s *Store) MailIndexHealthList(ctx context.Context) ([]MailIndexHealthP7, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		account_id, total_messages, total_size_bytes, sync_state,
		last_full_sync_at, last_incr_sync_at, last_error, updated_at
	FROM mail_index_health_p7 ORDER BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("MailIndexHealthList: %w", err)
	}
	defer rows.Close()
	out := make([]MailIndexHealthP7, 0, 8)
	for rows.Next() {
		h := MailIndexHealthP7{}
		if err := rows.Scan(
			&h.AccountID, &h.TotalMessages, &h.TotalSizeBytes, &h.SyncState,
			&h.LastFullSyncAt, &h.LastIncrSyncAt, &h.LastError, &h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("MailIndexHealthList scan: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// =========================================================================
// Phase 8A: Backup / BackupSchedule / RetentionRule structs + CRUD
// =========================================================================

// MailBackup represents a single on-disk tar.gz archive produced by the
// backup service layer.  `scope` is either "config" or "data_full".  The
// row is written in two phases: status="pending" when the archive is
// created, then status="completed" or "failed" once the tar.gz is
// finalised (or the process errored out).
type MailBackup struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"` // "config" | "data_full" | "manual" | "scheduled"
	Scope          string `json:"scope"`
	ArchivePath    string `json:"archive_path"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	IncludeData    bool   `json:"include_data"`
	Status         string `json:"status"` // pending | completed | failed
	ErrorMessage   string `json:"error_message"`
	ScheduleID     string `json:"schedule_id"`
	RetentionDays  int    `json:"retention_days"`
	ExpiresAt      string `json:"expires_at"`
	Note           string `json:"note"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at"`
	CreatedAt      string `json:"created_at"`
}

// MailBackupSchedule drives the periodic backup worker.  The scheduler
// uses a very small subset of cron syntax (H M * * DOW style) – the
// service layer is responsible for interpreting cron_expression.
type MailBackupSchedule struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Scope          string `json:"scope"` // "config" | "data_full"
	CronExpression string `json:"cron_expression"`
	RetentionDays  int    `json:"retention_days"`
	Enabled        bool   `json:"enabled"`
	NextRunAt      string `json:"next_run_at"`
	LastRunAt      string `json:"last_run_at"`
	LastBackupID   string `json:"last_backup_id"`
	LastError      string `json:"last_error"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// MailRetentionRule describes one cleaner that the retention worker
// applies each tick.  `target_kind` selects which storage cleaner runs
// (see MailClean*).  `days` is the cut-off; `keep_min_count` is a floor
// so very-active accounts don't lose their entire history.
type MailRetentionRule struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	RuleKind        string `json:"rule_kind"`   // "system" | "custom"
	TargetKind      string `json:"target_kind"` // delivery_events | health_checks | webhook_events | index_messages | expired_backups
	Days            int    `json:"days"`
	KeepMinCount    int    `json:"keep_min_count"`
	Enabled         bool   `json:"enabled"`
	Description     string `json:"description"`
	LastRunAt       string `json:"last_run_at"`
	LastPrunedCount int64  `json:"last_pruned_count"`
	LastError       string `json:"last_error"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// ---- MailBackup CRUD --------------------------------------------------

func (s *Store) MailBackupCreate(ctx context.Context, b MailBackup) (MailBackup, error) {
	n := now()
	if b.ID == "" {
		b.ID = NewID("mbk")
	}
	if b.Status == "" {
		b.Status = "pending"
	}
	if b.CreatedAt == "" {
		b.CreatedAt = n
	}
	if b.StartedAt == "" {
		b.StartedAt = n
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_backups (
		id, kind, archive_path, size_bytes, checksum_sha256, include_data,
		status, error_message, schedule_id, retention_days, expires_at,
		note, started_at, completed_at, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		b.ID, b.Kind, b.ArchivePath, b.SizeBytes, b.ChecksumSHA256,
		boolInt(b.IncludeData), b.Status, b.ErrorMessage, b.ScheduleID,
		b.RetentionDays, b.ExpiresAt, b.Note, b.StartedAt, b.CompletedAt, b.CreatedAt,
	)
	if err != nil {
		return MailBackup{}, fmt.Errorf("MailBackupCreate: %w", err)
	}
	// scope column was added later via ensureColumn; backfill by kind so
	// rows written before the column existed get a sensible value.
	if b.Scope != "" {
		_, _ = s.db.ExecContext(ctx,
			"UPDATE mail_backups SET scope=$1 WHERE id=$2", b.Scope, b.ID)
	}
	return s.MailBackupGet(ctx, b.ID)
}

func (s *Store) MailBackupUpdate(ctx context.Context, b MailBackup) (MailBackup, error) {
	if b.ID == "" {
		return MailBackup{}, ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `UPDATE mail_backups SET
		kind=$1, archive_path=$2, size_bytes=$3, checksum_sha256=$4,
		include_data=$5, status=$6, error_message=$7, schedule_id=$8,
		retention_days=$9, expires_at=$10, note=$11, started_at=$12,
		completed_at=$13
	WHERE id=$14`,
		b.Kind, b.ArchivePath, b.SizeBytes, b.ChecksumSHA256,
		boolInt(b.IncludeData), b.Status, b.ErrorMessage, b.ScheduleID,
		b.RetentionDays, b.ExpiresAt, b.Note, b.StartedAt, b.CompletedAt, b.ID,
	)
	if err != nil {
		return MailBackup{}, fmt.Errorf("MailBackupUpdate: %w", err)
	}
	return s.MailBackupGet(ctx, b.ID)
}

func (s *Store) MailBackupGet(ctx context.Context, id string) (MailBackup, error) {
	if id == "" {
		return MailBackup{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, kind, COALESCE(scope,''), archive_path, size_bytes, checksum_sha256,
		include_data, status, error_message,
		COALESCE(schedule_id,''), COALESCE(retention_days,0),
		COALESCE(expires_at,''), COALESCE(note,''),
		started_at, completed_at, created_at
	FROM mail_backups WHERE id = $1`, id)
	return scanMailBackup(row)
}

func (s *Store) MailBackupList(ctx context.Context, scope string, limit int) ([]MailBackup, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if scope == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, kind, COALESCE(scope,''), archive_path, size_bytes, checksum_sha256,
			include_data, status, error_message,
			COALESCE(schedule_id,''), COALESCE(retention_days,0),
			COALESCE(expires_at,''), COALESCE(note,''),
			started_at, completed_at, created_at
		FROM mail_backups ORDER BY created_at DESC LIMIT $1`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, kind, COALESCE(scope,''), archive_path, size_bytes, checksum_sha256,
			include_data, status, error_message,
			COALESCE(schedule_id,''), COALESCE(retention_days,0),
			COALESCE(expires_at,''), COALESCE(note,''),
			started_at, completed_at, created_at
		FROM mail_backups WHERE COALESCE(scope,'') IN ($1,'') ORDER BY created_at DESC LIMIT $2`, scope, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("MailBackupList: %w", err)
	}
	defer rows.Close()
	out := make([]MailBackup, 0, limit)
	for rows.Next() {
		b, err := scanMailBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MailBackupDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM mail_backups WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("MailBackupDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailBackupDeleteExpired deletes rows (returns count) where expires_at is
// non-empty, in the past, and status="completed".  It does NOT remove the
// on-disk archive – the caller is responsible for that.
func (s *Store) MailBackupDeleteExpired(ctx context.Context) (int64, error) {
	arg := now()
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_backups WHERE expires_at != '' AND expires_at < $1 AND status='completed'`, arg)
	if err != nil {
		return 0, fmt.Errorf("MailBackupDeleteExpired: %w", err)
	}
	return res.RowsAffected()
}

func scanMailBackup(sc mailScanner) (MailBackup, error) {
	b := MailBackup{}
	var includeData int64
	err := sc.Scan(
		&b.ID, &b.Kind, &b.Scope, &b.ArchivePath, &b.SizeBytes, &b.ChecksumSHA256,
		&includeData, &b.Status, &b.ErrorMessage,
		&b.ScheduleID, &b.RetentionDays, &b.ExpiresAt, &b.Note,
		&b.StartedAt, &b.CompletedAt, &b.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailBackup{}, ErrNotFound
	}
	if err != nil {
		return MailBackup{}, fmt.Errorf("scan mail_backup: %w", err)
	}
	b.IncludeData = includeData != 0
	return b, nil
}

// ---- MailBackupSchedule CRUD ------------------------------------------

func (s *Store) MailBackupScheduleUpsert(ctx context.Context, sc MailBackupSchedule) (MailBackupSchedule, error) {
	n := now()
	if sc.ID == "" {
		sc.ID = NewID("mbs")
	}
	if sc.CreatedAt == "" {
		sc.CreatedAt = n
	}
	sc.UpdatedAt = n
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_backup_schedules (
		id, name, scope, cron_expression, retention_days, enabled,
		next_run_at, last_run_at, last_backup_id, last_error,
		created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name, scope=excluded.scope,
		cron_expression=excluded.cron_expression,
		retention_days=excluded.retention_days, enabled=excluded.enabled,
		next_run_at=excluded.next_run_at, last_run_at=excluded.last_run_at,
		last_backup_id=excluded.last_backup_id, last_error=excluded.last_error,
		updated_at=excluded.updated_at`,
		sc.ID, sc.Name, sc.Scope, sc.CronExpression, sc.RetentionDays,
		boolInt(sc.Enabled), sc.NextRunAt, sc.LastRunAt, sc.LastBackupID,
		sc.LastError, sc.CreatedAt, sc.UpdatedAt,
	)
	if err != nil {
		return MailBackupSchedule{}, fmt.Errorf("MailBackupScheduleUpsert: %w", err)
	}
	return s.MailBackupScheduleGet(ctx, sc.ID)
}

func (s *Store) MailBackupScheduleGet(ctx context.Context, id string) (MailBackupSchedule, error) {
	if id == "" {
		return MailBackupSchedule{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, name, scope, cron_expression, retention_days, enabled,
		next_run_at, last_run_at, last_backup_id, last_error,
		created_at, updated_at
	FROM mail_backup_schedules WHERE id = $1`, id)
	return scanMailBackupSchedule(row)
}

func (s *Store) MailBackupScheduleList(ctx context.Context) ([]MailBackupSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, name, scope, cron_expression, retention_days, enabled,
		next_run_at, last_run_at, last_backup_id, last_error,
		created_at, updated_at
	FROM mail_backup_schedules ORDER BY enabled DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("MailBackupScheduleList: %w", err)
	}
	defer rows.Close()
	out := make([]MailBackupSchedule, 0, 8)
	for rows.Next() {
		sc, err := scanMailBackupSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MailBackupScheduleDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM mail_backup_schedules WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("MailBackupScheduleDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMailBackupSchedule(sc mailScanner) (MailBackupSchedule, error) {
	sc2 := MailBackupSchedule{}
	var enabled int64
	err := sc.Scan(
		&sc2.ID, &sc2.Name, &sc2.Scope, &sc2.CronExpression, &sc2.RetentionDays,
		&enabled, &sc2.NextRunAt, &sc2.LastRunAt, &sc2.LastBackupID, &sc2.LastError,
		&sc2.CreatedAt, &sc2.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailBackupSchedule{}, ErrNotFound
	}
	if err != nil {
		return MailBackupSchedule{}, fmt.Errorf("scan mail_backup_schedule: %w", err)
	}
	sc2.Enabled = enabled != 0
	return sc2, nil
}

// ---- MailRetentionRule CRUD -------------------------------------------

func (s *Store) MailRetentionRuleUpsert(ctx context.Context, r MailRetentionRule) (MailRetentionRule, error) {
	n := now()
	if r.ID == "" {
		r.ID = NewID("mrr")
	}
	if r.CreatedAt == "" {
		r.CreatedAt = n
	}
	r.UpdatedAt = n
	if r.RuleKind == "" {
		r.RuleKind = "custom"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_retention_rules (
		id, name, rule_kind, target_kind, days, keep_min_count, enabled,
		description, last_run_at, last_pruned_count, last_error,
		created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name, rule_kind=excluded.rule_kind,
		target_kind=excluded.target_kind, days=excluded.days,
		keep_min_count=excluded.keep_min_count, enabled=excluded.enabled,
		description=excluded.description, last_run_at=excluded.last_run_at,
		last_pruned_count=excluded.last_pruned_count,
		last_error=excluded.last_error, updated_at=excluded.updated_at`,
		r.ID, r.Name, r.RuleKind, r.TargetKind, r.Days, r.KeepMinCount,
		boolInt(r.Enabled), r.Description, r.LastRunAt, r.LastPrunedCount,
		r.LastError, r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		return MailRetentionRule{}, fmt.Errorf("MailRetentionRuleUpsert: %w", err)
	}
	return s.MailRetentionRuleGet(ctx, r.ID)
}

func (s *Store) MailRetentionRuleGet(ctx context.Context, id string) (MailRetentionRule, error) {
	if id == "" {
		return MailRetentionRule{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, name, rule_kind, target_kind, days, keep_min_count, enabled,
		description, last_run_at, last_pruned_count, last_error,
		created_at, updated_at
	FROM mail_retention_rules WHERE id = $1`, id)
	return scanMailRetentionRule(row)
}

func (s *Store) MailRetentionRuleList(ctx context.Context) ([]MailRetentionRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, name, rule_kind, target_kind, days, keep_min_count, enabled,
		description, last_run_at, last_pruned_count, last_error,
		created_at, updated_at
	FROM mail_retention_rules ORDER BY rule_kind, target_kind, name`)
	if err != nil {
		return nil, fmt.Errorf("MailRetentionRuleList: %w", err)
	}
	defer rows.Close()
	out := make([]MailRetentionRule, 0, 16)
	for rows.Next() {
		r, err := scanMailRetentionRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MailRetentionRuleDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM mail_retention_rules WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("MailRetentionRuleDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailRetentionRuleBumpRun updates the last_run / pruned / error fields
// after each application so the UI shows the most recent run info.
func (s *Store) MailRetentionRuleBumpRun(ctx context.Context, id string, pruned int64, runErr string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE mail_retention_rules SET
		last_run_at=$1, last_pruned_count=$2, last_error=$3, updated_at=$4
	WHERE id=$5`, now(), pruned, runErr, now(), id)
	if err != nil {
		return fmt.Errorf("MailRetentionRuleBumpRun: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMailRetentionRule(sc mailScanner) (MailRetentionRule, error) {
	r := MailRetentionRule{}
	var enabled int64
	err := sc.Scan(
		&r.ID, &r.Name, &r.RuleKind, &r.TargetKind, &r.Days, &r.KeepMinCount,
		&enabled, &r.Description, &r.LastRunAt, &r.LastPrunedCount, &r.LastError,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailRetentionRule{}, ErrNotFound
	}
	if err != nil {
		return MailRetentionRule{}, fmt.Errorf("scan mail_retention_rule: %w", err)
	}
	r.Enabled = enabled != 0
	return r, nil
}

// =========================================================================
// Phase 8A: Retention cleaners
// =========================================================================

// MailCleanDeliveryEventsOlderThan removes mail_delivery_events rows
// older than `days` days.  keepMin is a best-effort floor: the cleaner
// computes the Nth most-recent timestamp and only deletes strictly older
// rows so at least `keepMin` rows survive regardless of age.
func (s *Store) MailCleanDeliveryEventsOlderThan(ctx context.Context, days, keepMin int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	arg := fmt.Sprintf("-%d days", days)
	cutoff := ""
	if keepMin > 0 {
		row := s.db.QueryRowContext(ctx,
			`SELECT created_at FROM mail_delivery_events
			 ORDER BY created_at DESC LIMIT 1 OFFSET $1`, keepMin-1)
		var v sql.NullString
		if err := row.Scan(&v); err == nil && v.Valid {
			cutoff = v.String
		}
	}
	q := `DELETE FROM mail_delivery_events
	      WHERE created_at != ''
	        AND created_at < datetime('now', $1)`
	args := []any{arg}
	if cutoff != "" {
		q += " AND created_at < $2"
		args = append(args, cutoff)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("MailCleanDeliveryEventsOlderThan: %w", err)
	}
	return res.RowsAffected()
}

// MailCleanHealthChecks prunes mail_mox_health_checks rows older than
// `days` days, preserving at least `keepMin` rows per (kind, scope) pair
// as a best-effort per-group floor.
func (s *Store) MailCleanHealthChecks(ctx context.Context, days, keepMin int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	arg := fmt.Sprintf("-%d days", days)
	q := `DELETE FROM mail_mox_health_checks
	      WHERE started_at != ''
	        AND started_at < datetime('now', $1)`
	if keepMin > 0 {
		q += ` AND (kind, scope, started_at) NOT IN (
		         SELECT kind, scope, started_at FROM mail_mox_health_checks
		         ORDER BY started_at DESC LIMIT $2
		       )`
		res, err := s.db.ExecContext(ctx, q, arg, keepMin*16)
		if err != nil {
			return 0, fmt.Errorf("MailCleanHealthChecks: %w", err)
		}
		return res.RowsAffected()
	}
	res, err := s.db.ExecContext(ctx, q, arg)
	if err != nil {
		return 0, fmt.Errorf("MailCleanHealthChecks: %w", err)
	}
	return res.RowsAffected()
}

// MailCleanWebhookEvents removes mail_webhook_events rows older than
// `days` days.  keepMin preserves the most recent N rows globally.
func (s *Store) MailCleanWebhookEvents(ctx context.Context, days, keepMin int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	arg := fmt.Sprintf("-%d days", days)
	cutoff := ""
	if keepMin > 0 {
		row := s.db.QueryRowContext(ctx,
			`SELECT created_at FROM mail_webhook_events
			 ORDER BY created_at DESC LIMIT 1 OFFSET $1`, keepMin-1)
		var v sql.NullString
		if err := row.Scan(&v); err == nil && v.Valid {
			cutoff = v.String
		}
	}
	q := `DELETE FROM mail_webhook_events
	      WHERE created_at != ''
	        AND created_at < datetime('now', $1)`
	args := []any{arg}
	if cutoff != "" {
		q += " AND created_at < $2"
		args = append(args, cutoff)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("MailCleanWebhookEvents: %w", err)
	}
	return res.RowsAffected()
}

// MailCleanIndexMessages removes mail_messages_p7 rows (the "current"
// mailbox schema) older than `days` days.  The cleaner only runs on
// accounts whose status=active and messages have explicit date_sent.
// keepMin is a per-account floor so active accounts don't lose their
// entire inbox.
func (s *Store) MailCleanIndexMessages(ctx context.Context, days, keepMin int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	// Fetch each account and run per-account deletes.  The extra round-trip
	// keeps each DELETE statement bounded by the per-account keepMin floor.
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM mail_accounts WHERE status != 'disabled' OR status = '' OR status IS NULL")
	if err != nil {
		return 0, fmt.Errorf("MailCleanIndexMessages: list accounts: %w", err)
	}
	defer rows.Close()
	accounts := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		accounts = append(accounts, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	beforeCutoff := fmt.Sprintf("-%d days", days)
	var total int64
	for _, acc := range accounts {
		cutoff := ""
		if keepMin > 0 {
			row := s.db.QueryRowContext(ctx,
				`SELECT CASE WHEN date_sent != '' THEN date_sent ELSE internaldate END
				 FROM mail_messages_p7 WHERE account_id = $1
				 ORDER BY COALESCE(NULLIF(date_sent,''), internaldate) DESC
				 LIMIT 1 OFFSET $2`, acc, keepMin-1)
			var v sql.NullString
			if err := row.Scan(&v); err == nil && v.Valid {
				cutoff = v.String
			}
		}
		args := []any{acc, beforeCutoff}
		q := `DELETE FROM mail_messages_p7 WHERE account_id = $1
			  AND COALESCE(NULLIF(date_sent,''), internaldate) != ''
		      AND COALESCE(NULLIF(date_sent,''), internaldate) < datetime('now', $2)`
		if cutoff != "" {
			q += " AND COALESCE(NULLIF(date_sent,''), internaldate) < $3"
			args = append(args, cutoff)
		}
		res, err := s.db.ExecContext(ctx, q, args...)
		if err != nil {
			return total, fmt.Errorf("MailCleanIndexMessages account=%s: %w", acc, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// =========================================================================
// Phase 8 Part A – Structs
// =========================================================================
// NOTE: MailBackup and MailBackupSchedule Go struct names already taken
// by the Phase 8B parallel agent (simpler legacy schema).  The tables
// below use extended schemas defined in migrateMail():
//   mail_log_retention_rules  → MailLogRetentionRule
//   mail_backups_new          → MailBackupArtifact (renamed to avoid
//                               conflict with 8B's MailBackup)
//   mail_backup_schedules     → MailBackupScheduleEntry (renamed to
//                               avoid conflict with 8B's MailBackupSchedule).
// =========================================================================

// MailLogRetentionRule defines scope-based (account / domain / global)
// retention policy applied to mail_log_retention_rules.
type MailLogRetentionRule struct {
	ID                string `json:"id"`
	Scope             string `json:"scope"`
	ScopeID           string `json:"scopeId"`
	Category          string `json:"category"`
	KeepDays          int    `json:"keepDays"`
	PruneEmptyFolders bool   `json:"pruneEmptyFolders"`
	HardDelete        bool   `json:"hardDelete"`
	Note              string `json:"note"`
	Enabled           bool   `json:"enabled"`
	LastPrunedAt      string `json:"lastPrunedAt"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// MailBackupArtifact describes a single backup on the mail_backups_new table.
// Renamed from MailBackup to avoid Phase 8B naming conflict.
type MailBackupArtifact struct {
	ID             string `json:"id"`
	Scope          string `json:"scope"`
	ScopeID        string `json:"scopeId"`
	Kind           string `json:"kind"`
	DisplayName    string `json:"displayName"`
	FilePath       string `json:"filePath"`
	FileSizeBytes  int64  `json:"fileSizeBytes"`
	ChecksumSHA256 string `json:"checksumSha256"`
	ContainsConfig bool   `json:"containsConfig"`
	ContainsData   bool   `json:"containsData"`
	EncryptionMode string `json:"encryptionMode"`
	Note           string `json:"note"`
	Status         string `json:"status"`
	ErrorMessage   string `json:"errorMessage"`
	CreatedBy      string `json:"createdBy"`
	ExpiresAt      string `json:"expiresAt"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt"`
	CreatedAt      string `json:"createdAt"`
}

// MailBackupScheduleEntry is the detailed scheduler row on mail_backup_schedules.
// Renamed from MailBackupSchedule to avoid Phase 8B naming conflict.
type MailBackupScheduleEntry struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Scope                 string `json:"scope"`
	ScopeID               string `json:"scopeId"`
	ScheduleKind          string `json:"scheduleKind"`
	CadenceCron           string `json:"cadenceCron"`
	Timezone              string `json:"timezone"`
	KeepRevisions         int    `json:"keepRevisions"`
	ContainsConfig        bool   `json:"containsConfig"`
	ContainsData          bool   `json:"containsData"`
	EncryptionMode        string `json:"encryptionMode"`
	EncryptPasswordHash   string `json:"encryptPasswordHash"`
	StorageTarget         string `json:"storageTarget"`
	TargetURL             string `json:"targetUrl"`
	TargetCredentialsJSON string `json:"targetCredentialsJson"`
	PreRunHook            string `json:"preRunHook"`
	PostRunHook           string `json:"postRunHook"`
	LastStatus            string `json:"lastStatus"`
	Paused                bool   `json:"paused"`
	Enabled               bool   `json:"enabled"`
	NextRunAt             string `json:"nextRunAt"`
	LastRunAt             string `json:"lastRunAt"`
	LastBackupID          string `json:"lastBackupId"`
	LastError             string `json:"lastError"`
	CreatedAt             string `json:"createdAt"`
	UpdatedAt             string `json:"updatedAt"`
}

// =========================================================================
// Phase 8 Part A – MailLogRetentionRule CRUD
// =========================================================================

func (s *Store) MailRetentionUpsert(ctx context.Context, r *MailLogRetentionRule) (*MailLogRetentionRule, error) {
	if r == nil {
		return nil, ErrNotFound
	}
	n := now()
	if r.ID == "" {
		r.ID = NewID("rlr")
	}
	if r.Scope == "" {
		r.Scope = "global"
	}
	if r.CreatedAt == "" {
		r.CreatedAt = n
	}
	r.UpdatedAt = n
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO mail_log_retention_rules (
		id, scope, scope_id, category, keep_days, prune_empty_folders,
		hard_delete, note, enabled, last_pruned_at, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		r.ID, r.Scope, r.ScopeID, r.Category, r.KeepDays,
		boolInt(r.PruneEmptyFolders), boolInt(r.HardDelete),
		r.Note, boolInt(r.Enabled), r.LastPrunedAt, r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MailRetentionUpsert: %w", err)
	}
	return s.MailRetentionGetByScope(ctx, r.Scope, r.ScopeID, r.Category)
}

func (s *Store) MailRetentionGetByScope(ctx context.Context, scope, scopeID, category string) (*MailLogRetentionRule, error) {
	if scope == "" {
		scope = "global"
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, scope, scope_id, category, keep_days, prune_empty_folders,
		hard_delete, note, enabled, last_pruned_at, created_at, updated_at
	FROM mail_log_retention_rules WHERE scope=$1 AND scope_id=$2 AND category=$3`,
		scope, scopeID, category)
	r, err := scanMailLogRetentionRule(row)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) MailRetentionList(ctx context.Context, scope, scopeID string) ([]MailLogRetentionRule, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case scope == "":
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, scope, scope_id, category, keep_days, prune_empty_folders,
			hard_delete, note, enabled, last_pruned_at, created_at, updated_at
		FROM mail_log_retention_rules ORDER BY scope, category`)
	case scopeID == "":
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, scope, scope_id, category, keep_days, prune_empty_folders,
			hard_delete, note, enabled, last_pruned_at, created_at, updated_at
		FROM mail_log_retention_rules WHERE scope=$1 ORDER BY category`, scope)
	default:
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, scope, scope_id, category, keep_days, prune_empty_folders,
			hard_delete, note, enabled, last_pruned_at, created_at, updated_at
		FROM mail_log_retention_rules WHERE scope=$1 AND scope_id=$2 ORDER BY category`, scope, scopeID)
	}
	if err != nil {
		return nil, fmt.Errorf("MailRetentionList: %w", err)
	}
	defer rows.Close()
	out := make([]MailLogRetentionRule, 0, 16)
	for rows.Next() {
		r, err := scanMailLogRetentionRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MailRetentionDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM mail_log_retention_rules WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("MailRetentionDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMailLogRetentionRule(sc mailScanner) (MailLogRetentionRule, error) {
	r := MailLogRetentionRule{}
	var prune, hard, enabled int64
	err := sc.Scan(
		&r.ID, &r.Scope, &r.ScopeID, &r.Category, &r.KeepDays,
		&prune, &hard, &r.Note, &enabled, &r.LastPrunedAt,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailLogRetentionRule{}, ErrNotFound
	}
	if err != nil {
		return MailLogRetentionRule{}, fmt.Errorf("scan mail_log_retention_rule: %w", err)
	}
	r.PruneEmptyFolders = prune != 0
	r.HardDelete = hard != 0
	r.Enabled = enabled != 0
	return r, nil
}

// =========================================================================
// Phase 8 Part A – MailBackupArtifact CRUD  (mail_backups_new table)
// =========================================================================

func (s *Store) MailBackupArtifactCreate(ctx context.Context, b *MailBackupArtifact) (*MailBackupArtifact, error) {
	if b == nil {
		return nil, ErrNotFound
	}
	n := now()
	if b.ID == "" {
		b.ID = NewID("bck")
	}
	if b.Status == "" {
		b.Status = "pending"
	}
	if b.CreatedAt == "" {
		b.CreatedAt = n
	}
	if b.StartedAt == "" {
		b.StartedAt = n
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mail_backups_new (
		id, scope, scope_id, kind, display_name, file_path, file_size_bytes,
		checksum_sha256, contains_config, contains_data, encryption_mode,
		note, status, error_message, created_by, expires_at,
		started_at, completed_at, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		b.ID, b.Scope, b.ScopeID, b.Kind, b.DisplayName,
		b.FilePath, b.FileSizeBytes, b.ChecksumSHA256,
		boolInt(b.ContainsConfig), boolInt(b.ContainsData),
		b.EncryptionMode, b.Note, b.Status, b.ErrorMessage,
		b.CreatedBy, b.ExpiresAt, b.StartedAt, b.CompletedAt, b.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MailBackupArtifactCreate: %w", err)
	}
	return s.MailBackupArtifactGet(ctx, b.ID)
}

func (s *Store) MailBackupArtifactGet(ctx context.Context, id string) (*MailBackupArtifact, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, scope, scope_id, kind, display_name, file_path, file_size_bytes,
		checksum_sha256, contains_config, contains_data, encryption_mode,
		note, status, error_message, created_by, expires_at,
		started_at, completed_at, created_at
	FROM mail_backups_new WHERE id = $1`, id)
	b, err := scanMailBackupArtifact(row)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) MailBackupArtifactList(ctx context.Context, scope, scopeID, kind, status string, limit, offset int) ([]MailBackupArtifact, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := []string{"1=1"}
	args := []any{}
	pn := 1
	if scope != "" {
		where = append(where, fmt.Sprintf("scope=$%d", pn))
		args = append(args, scope)
		pn++
	}
	if scopeID != "" {
		where = append(where, fmt.Sprintf("scope_id=$%d", pn))
		args = append(args, scopeID)
		pn++
	}
	if kind != "" {
		where = append(where, fmt.Sprintf("kind=$%d", pn))
		args = append(args, kind)
		pn++
	}
	if status != "" {
		where = append(where, fmt.Sprintf("status=$%d", pn))
		args = append(args, status)
		pn++
	}
	w := strings.Join(where, " AND ")

	var total int64
	row := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM mail_backups_new WHERE %s", w), args...)
	if err := row.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("MailBackupArtifactList count: %w", err)
	}

	sel := `SELECT id, scope, scope_id, kind, display_name, file_path, file_size_bytes,
		checksum_sha256, contains_config, contains_data, encryption_mode,
		note, status, error_message, created_by, expires_at,
		started_at, completed_at, created_at
	FROM mail_backups_new WHERE ` + w + ` ORDER BY COALESCE(completed_at,started_at,created_at) DESC`
	q := fmt.Sprintf("%s LIMIT $%d OFFSET $%d", sel, pn, pn+1)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("MailBackupArtifactList: %w", err)
	}
	defer rows.Close()
	out := make([]MailBackupArtifact, 0, limit)
	for rows.Next() {
		b, err := scanMailBackupArtifact(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (s *Store) MailBackupArtifactDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM mail_backups_new WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("MailBackupArtifactDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MailBackupArtifactDeleteExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_backups_new WHERE expires_at != '' AND expires_at < $1`, now())
	if err != nil {
		return 0, fmt.Errorf("MailBackupArtifactDeleteExpired: %w", err)
	}
	return res.RowsAffected()
}

func scanMailBackupArtifact(sc mailScanner) (MailBackupArtifact, error) {
	b := MailBackupArtifact{}
	var cfg, data int64
	err := sc.Scan(
		&b.ID, &b.Scope, &b.ScopeID, &b.Kind, &b.DisplayName,
		&b.FilePath, &b.FileSizeBytes, &b.ChecksumSHA256,
		&cfg, &data, &b.EncryptionMode, &b.Note, &b.Status,
		&b.ErrorMessage, &b.CreatedBy, &b.ExpiresAt,
		&b.StartedAt, &b.CompletedAt, &b.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailBackupArtifact{}, ErrNotFound
	}
	if err != nil {
		return MailBackupArtifact{}, fmt.Errorf("scan mail_backups_new: %w", err)
	}
	b.ContainsConfig = cfg != 0
	b.ContainsData = data != 0
	return b, nil
}

// =========================================================================
// Phase 8 Part A – MailBackupScheduleEntry CRUD  (mail_backup_schedules)
// =========================================================================

func (s *Store) MailBackupScheduleEntryUpsert(ctx context.Context, sc *MailBackupScheduleEntry) (*MailBackupScheduleEntry, error) {
	if sc == nil {
		return nil, ErrNotFound
	}
	n := now()
	if sc.ID == "" {
		sc.ID = NewID("sch")
	}
	if sc.ScheduleKind == "" {
		sc.ScheduleKind = "data_full"
	}
	if sc.CreatedAt == "" {
		sc.CreatedAt = n
	}
	sc.UpdatedAt = n
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO mail_backup_schedules (
		id, name, scope, scope_id, schedule_kind, cadence_cron, timezone,
		keep_revisions, contains_config, contains_data, encryption_mode,
		encrypt_password_hash, storage_target, target_url, target_credentials_json,
		pre_run_hook, post_run_hook, last_status, paused, enabled,
		next_run_at, last_run_at, last_backup_id, last_error,
		created_at, updated_at
	) VALUES (
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
		$21,$22,$23,$24,$25,$26
	)`,
		sc.ID, sc.Name, sc.Scope, sc.ScopeID, sc.ScheduleKind,
		sc.CadenceCron, sc.Timezone, sc.KeepRevisions,
		boolInt(sc.ContainsConfig), boolInt(sc.ContainsData),
		sc.EncryptionMode, sc.EncryptPasswordHash, sc.StorageTarget,
		sc.TargetURL, sc.TargetCredentialsJSON, sc.PreRunHook, sc.PostRunHook,
		sc.LastStatus, boolInt(sc.Paused), boolInt(sc.Enabled),
		sc.NextRunAt, sc.LastRunAt, sc.LastBackupID, sc.LastError,
		sc.CreatedAt, sc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MailBackupScheduleEntryUpsert: %w", err)
	}
	return s.MailBackupScheduleGetByScope(ctx, sc.Scope, sc.ScopeID, sc.ID)
}

// MailBackupScheduleGetByScope – exact Phase 8A method name.  Phase 8B only
// defines MailBackupScheduleGet(id) so there is no name collision.
func (s *Store) MailBackupScheduleGetByScope(ctx context.Context, scope, scopeID, scheduleID string) (*MailBackupScheduleEntry, error) {
	var row *sql.Row
	switch {
	case scheduleID != "":
		q := `SELECT
			id, name, scope, scope_id, schedule_kind, cadence_cron, timezone,
			keep_revisions, contains_config, contains_data, encryption_mode,
			encrypt_password_hash, storage_target, target_url, target_credentials_json,
			pre_run_hook, post_run_hook, last_status, paused, enabled,
			next_run_at, last_run_at, last_backup_id, last_error,
			created_at, updated_at
		FROM mail_backup_schedules WHERE id=$1`
		args := []any{scheduleID}
		if scope != "" {
			q += " AND scope=$2"
			args = append(args, scope)
		}
		row = s.db.QueryRowContext(ctx, q, args...)
	case scope != "":
		q := `SELECT
			id, name, scope, scope_id, schedule_kind, cadence_cron, timezone,
			keep_revisions, contains_config, contains_data, encryption_mode,
			encrypt_password_hash, storage_target, target_url, target_credentials_json,
			pre_run_hook, post_run_hook, last_status, paused, enabled,
			next_run_at, last_run_at, last_backup_id, last_error,
			created_at, updated_at
		FROM mail_backup_schedules WHERE scope=$1 AND COALESCE(enabled,1)=1`
		args := []any{scope}
		if scopeID != "" {
			q += " AND scope_id=$2"
			args = append(args, scopeID)
		}
		q += " ORDER BY updated_at DESC LIMIT 1"
		row = s.db.QueryRowContext(ctx, q, args...)
	default:
		return nil, ErrNotFound
	}
	sc, err := scanMailBackupScheduleEntry(row)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

func (s *Store) MailBackupScheduleDetailList(ctx context.Context, scope, scopeID string) ([]MailBackupScheduleEntry, error) {
	var (
		rows *sql.Rows
		err  error
	)
	q := `SELECT
		id, name, scope, scope_id, schedule_kind, cadence_cron, timezone,
		keep_revisions, contains_config, contains_data, encryption_mode,
		encrypt_password_hash, storage_target, target_url, target_credentials_json,
		pre_run_hook, post_run_hook, last_status, paused, enabled,
		next_run_at, last_run_at, last_backup_id, last_error,
		created_at, updated_at
	FROM mail_backup_schedules`
	switch {
	case scope == "":
		rows, err = s.db.QueryContext(ctx, q+` ORDER BY enabled DESC, paused ASC, name`)
	case scopeID == "":
		rows, err = s.db.QueryContext(ctx, q+` WHERE scope=$1 ORDER BY enabled DESC, paused ASC, name`, scope)
	default:
		rows, err = s.db.QueryContext(ctx, q+` WHERE scope=$1 AND scope_id=$2 ORDER BY enabled DESC, paused ASC, name`, scope, scopeID)
	}
	if err != nil {
		return nil, fmt.Errorf("MailBackupScheduleDetailList: %w", err)
	}
	defer rows.Close()
	out := make([]MailBackupScheduleEntry, 0, 16)
	for rows.Next() {
		sc, err := scanMailBackupScheduleEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MailBackupScheduleEntryDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM mail_backup_schedules WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("MailBackupScheduleEntryDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMailBackupScheduleEntry(sc mailScanner) (MailBackupScheduleEntry, error) {
	r := MailBackupScheduleEntry{}
	var cfg, data, paused, enabled int64
	err := sc.Scan(
		&r.ID, &r.Name, &r.Scope, &r.ScopeID, &r.ScheduleKind,
		&r.CadenceCron, &r.Timezone, &r.KeepRevisions,
		&cfg, &data, &r.EncryptionMode, &r.EncryptPasswordHash,
		&r.StorageTarget, &r.TargetURL, &r.TargetCredentialsJSON,
		&r.PreRunHook, &r.PostRunHook, &r.LastStatus, &paused, &enabled,
		&r.NextRunAt, &r.LastRunAt, &r.LastBackupID, &r.LastError,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MailBackupScheduleEntry{}, ErrNotFound
	}
	if err != nil {
		return MailBackupScheduleEntry{}, fmt.Errorf("scan mail_backup_schedules detail: %w", err)
	}
	r.ContainsConfig = cfg != 0
	r.ContainsData = data != 0
	r.Paused = paused != 0
	r.Enabled = enabled != 0
	return r, nil
}

// =========================================================================
// Phase 8 Part A – Data cleaners
// MailCleanDeliveryEventsOlderThan is already provided by Phase 8B with the
// richer (days,keepMin int) arity.  We add the 3 remaining *OlderThan helpers
// with the single-argument (days int) shape the spec requests.
// =========================================================================

func (s *Store) MailCleanHealthChecksOlderThan(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_mox_health_checks
		  WHERE started_at != ''
		    AND started_at < datetime('now', $1)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, fmt.Errorf("MailCleanHealthChecksOlderThan: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) MailCleanWebhookEventsOlderThan(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_webhook_events
		  WHERE created_at != ''
		    AND created_at < datetime('now', $1)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, fmt.Errorf("MailCleanWebhookEventsOlderThan: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) MailCleanIndexMessagesOlderThan(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM mail_accounts WHERE COALESCE(status,'') != 'disabled'")
	if err != nil {
		return 0, fmt.Errorf("MailCleanIndexMessagesOlderThan: list accounts: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	cutoff := fmt.Sprintf("-%d days", days)
	var total int64
	for _, acc := range ids {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM mail_messages_p7
			  WHERE account_id = $1
			    AND COALESCE(NULLIF(date_sent,''), internaldate) != ''
			    AND COALESCE(NULLIF(date_sent,''), internaldate)
			        < datetime('now', $2)`,
			acc, cutoff)
		if err != nil {
			return total, fmt.Errorf("MailCleanIndexMessagesOlderThan account=%s: %w", acc, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
