package mail

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/mail/moxcli"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Account service-layer value types.
// -----------------------------------------------------------------------------

// AccountCreateRequest is the UI -> service payload for creating a new
// mailbox account.  The password field is optional: if empty, a 18-byte
// CSPRNG password is generated and returned to the caller exactly once
// (it is never persisted to DB, audit, or logs).
type AccountCreateRequest struct {
	DomainID        string `json:"domain_id"`
	LocalPart       string `json:"local_part"`
	DisplayName     string `json:"display_name,omitempty"`
	Password        string `json:"password,omitempty"` // if empty, service generates
	QuotaMB         int64  `json:"quota_mb,omitempty"`
	IsAdmin         bool   `json:"is_admin,omitempty"`
	IMAPSyncEnabled bool   `json:"imap_sync_enabled,omitempty"`
	Status          string `json:"status,omitempty"` // default: "active"
}

// AccountCreateResponse wraps the persisted row plus the generated password.
// The GeneratedPassword field is ONLY valid on create and ALWAYS empty for
// any other response type.
type AccountCreateResponse struct {
	Account          storage.MailAccount `json:"account"`
	GeneratedPassword string             `json:"generated_password,omitempty"` // write-once
}

// AccountUpdateRequest carries mutable fields for an existing account.
type AccountUpdateRequest struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name,omitempty"`
	QuotaMB         int64  `json:"quota_mb,omitempty"`
	IsAdmin         bool   `json:"is_admin,omitempty"`
	IMAPSyncEnabled bool   `json:"imap_sync_enabled,omitempty"`
	Status          string `json:"status,omitempty"`
	Password        string `json:"password,omitempty"` // if set, runs SetAccountPassword too
}

// AccountResetResponse wraps a freshly-generated password after a reset.
type AccountResetResponse struct {
	AccountID        string `json:"account_id"`
	GeneratedPassword string `json:"generated_password"` // write-once
}

// AccountResponse is the per-row wire format used by list / get endpoints.
type AccountResponse struct {
	storage.MailAccount
	Drifted bool `json:"drifted"`
}

// AccountListResponse wraps the list plus summary counts so the UI top-card
// can render badges without a second round-trip.
type AccountListResponse struct {
	Items     []storage.MailAccount `json:"items"`
	Count     int                   `json:"count"`
	Active    int                   `json:"active_count"`
	Admins    int                   `json:"admin_count"`
	Drifted   bool                  `json:"drifted"`
	ImportRO  bool                  `json:"import_read_only"`
}

// looseEmail is used for UI-level validation only (the real RFC5321 grammar
// is validated by mox when the account is created).  The regex deliberately
// accepts a superset so corner-case addresses are not rejected at the API
// layer.
var looseEmail = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// --- Guard helpers ----------------------------------------------------------

// checkWriteGuard enforces the two write-precondition invariants required
// on every mutating method in this file:
//
//  1. config_drifted – if the drift detector reports the on-disk mox.conf
//     has drifted away from SQLite, refuse to mutate until the operator
//     resolves the conflict.
//  2. import_read_only – if settings.import_mode is true the service is
//     read-only (mirroring an external Mox install); writes are rejected.
func (s *Service) checkWriteGuard(ctx context.Context) error {
	if s.Drifted() {
		return &errCoded{code: "config_drifted", msg: "config drifted; resolve before writing"}
	}
	settings, err := s.store.MailGetSettings(ctx)
	if err == nil && settings != nil && settings.ImportMode {
		return &errCoded{code: "import_read_only", msg: "import mode: writes are disabled"}
	}
	return nil
}

// errCoded is a tiny error carrier so HTTP handlers can translate the
// service-layer rejection into a status code without brittle string matching.
type errCoded struct {
	code string
	msg  string
}

func (e *errCoded) Error() string {
	if e.code != "" {
		return e.code + ": " + e.msg
	}
	return e.msg
}

// ErrorCode returns the stable machine-readable code for this error.
// Returns "" for errors that do not carry a code.
func ErrorCode(err error) string {
	var ec *errCoded
	if errors.As(err, &ec) {
		return ec.code
	}
	return ""
}

// --- Helpers ----------------------------------------------------------------

// generatePassword produces an 18-byte CSPRNG token encoded with unpadded
// URL-safe base64 (24 printable characters).  The returned slice is never
// logged, persisted, or audited – it is only ever written into the pipe
// consumed by `mox setaccountpassword` and, optionally, returned to the
// HTTP caller exactly once in the response body.
func generatePassword() ([]byte, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("csprng: %w", err)
	}
	out := make([]byte, base64.RawURLEncoding.EncodedLen(18))
	base64.RawURLEncoding.Encode(out, buf)
	return out, nil
}

// newID returns a short, stable id for the given prefix.  ids.New is
// preferred; on any error we fall back to a hex token so the write path
// is never blocked by a utility-package hiccup.
func newID(prefix string) string {
	if id, err := ids.New(prefix); err == nil && id != "" {
		return id
	}
	return prefix + "_" + newShortHex(12)
}

// resolveMoxBinPath returns the effective mox binary path, falling back to
// the Runner when the settings row has not been populated yet.
func (s *Service) resolveMoxBinPath(ctx context.Context) string {
	if s.cli != nil && s.cli.BinaryPath != "" {
		return s.cli.BinaryPath
	}
	if settings, err := s.store.MailGetSettings(ctx); err == nil && settings != nil && settings.MoxBinaryPath != "" {
		return settings.MoxBinaryPath
	}
	if s.cli != nil {
		return s.cli.BinaryPath
	}
	return ""
}

// -----------------------------------------------------------------------------
// Service methods.
// -----------------------------------------------------------------------------

// MailAccountCreate creates a new mailbox account with the following
// invariants:
//
//  1. DB-FIRST (C3): the SQLite row is inserted BEFORE any side effect is
//     applied to the running mox install.  If the password step fails the
//     row is rolled back (deleted) so the DB does not contain an account
//     that is unreachable via authentication.
//  2. C6 password hygiene: the password bytes are stored in a single local
//     variable, NEVER written to logs/audit/DB, and delivered to the mox
//     subprocess ONLY via an os.Pipe connected to stdin.
//  3. The generated password is returned to the caller exactly once in the
//     response; callers are responsible for showing it to the operator.
func (s *Service) MailAccountCreate(ctx context.Context, req AccountCreateRequest) (*AccountCreateResponse, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if req.DomainID == "" {
		return nil, errors.New("domain_id is required")
	}
	if req.LocalPart == "" {
		return nil, errors.New("local_part is required")
	}
	// Load the domain to build the full address and verify it exists.
	dom, derr := s.store.MailGetDomain(ctx, req.DomainID)
	if derr != nil || dom == nil {
		return nil, fmt.Errorf("domain not found: %w", derr)
	}
	addr := strings.ToLower(strings.TrimSpace(req.LocalPart)) + "@" + strings.ToLower(strings.TrimSpace(dom.Domain))
	if !looseEmail.MatchString(addr) {
		return nil, fmt.Errorf("invalid resulting email: %q", addr)
	}

	// Step 1: resolve the password (CSPRNG fallback; req.Password kept ONLY
	// in a local []byte variable, never logged).
	var pwBuf []byte
	var generated string
	if strings.TrimSpace(req.Password) != "" {
		pwBuf = []byte(req.Password)
	} else {
		p, gerr := generatePassword()
		if gerr != nil {
			return nil, gerr
		}
		pwBuf = p
		generated = string(pwBuf)
	}

	// Step 2: build the storage row.
	ts := time.Now().UTC().Format(time.RFC3339)
	row := storage.MailAccount{
		ID:              newID(IDPrefixAccount),
		DomainID:        req.DomainID,
		LocalPart:       req.LocalPart,
		Address:         addr,
		Email:           addr,
		DisplayName:     req.DisplayName,
		PasswordMode:    "set",
		QuotaMB:         req.QuotaMB,
		IsAdmin:         req.IsAdmin,
		IMAPSyncEnabled: req.IMAPSyncEnabled,
		Status:          req.Status,
		CreatedAt:       ts,
		UpdatedAt:       ts,
	}
	if row.Status == "" {
		row.Status = "active"
	}

	// Step 3: DB-FIRST insert.
	saved, ierr := s.store.MailCreateAccount(ctx, row)
	if ierr != nil {
		return nil, fmt.Errorf("insert account: %w", ierr)
	}

	// Step 4: deliver the password to mox via stdin pipe.
	binPath := s.resolveMoxBinPath(ctx)
	if binPath == "" {
		// No mox binary configured is a soft-fail: we keep the DB row
		// (operator can retry later via Resync / ResetPassword).
		s.addAudit(ctx, EventTypeAccountCreated,
			fmt.Sprintf("created account %s (mox binary not configured yet; password not applied)", addr),
			map[string]any{
				"account_id": saved.ID,
				"domain_id":  saved.DomainID,
				"is_admin":   saved.IsAdmin,
			}, "medium")
		s.publish(ctx, EventTypeAccountCreated, map[string]any{
			"id":       saved.ID,
			"address":  addr,
			"domain":   dom.Domain,
			"is_admin": saved.IsAdmin,
		})
		return &AccountCreateResponse{Account: saved, GeneratedPassword: generated}, nil
	}

	perr := moxcli.SetAccountPassword(ctx, binPath, addr, pwBuf)
	if perr != nil {
		// Step 5 (rollback): DB-FIRST means delete the row we just inserted
		// so a partially-created account does not linger.
		_ = s.store.MailDeleteAccount(ctx, saved.ID)
		return nil, fmt.Errorf("mox setaccountpassword: %w", perr)
	}

	// Step 6: update timestamps + password_last_changed_at on success.
	saved.LastPasswordChangedAt = ts
	saved.UpdatedAt = ts
	if _, uerr := s.store.MailUpdateAccount(ctx, saved); uerr != nil {
		// Non-fatal: the account is still usable, the timestamp is just stale.
		s.log.WarnContext(ctx, "mail: update account ts after password set failed", "error", uerr)
	}

	// Step 7: audit + publish (NOTHING about the password reaches either).
	s.addAudit(ctx, EventTypeAccountCreated,
		fmt.Sprintf("created account %s", addr),
		map[string]any{
			"account_id": saved.ID,
			"domain_id":  saved.DomainID,
			"is_admin":   saved.IsAdmin,
			"quota_mb":   saved.QuotaMB,
		}, "medium")
	s.publish(ctx, EventTypeAccountCreated, map[string]any{
		"id":                saved.ID,
		"address":           addr,
		"domain":            dom.Domain,
		"is_admin":          saved.IsAdmin,
		"imap_sync_enabled": saved.IMAPSyncEnabled,
	})
	s.touchLastChange()

	// Step 8: return the generated password (if any) exactly once.
	return &AccountCreateResponse{Account: saved, GeneratedPassword: generated}, nil
}

// MailAccountUpdate mutates the mutable subset of an account row and, if a
// password is supplied, applies it through the mox CLI pipe.
func (s *Service) MailAccountUpdate(ctx context.Context, req AccountUpdateRequest) (*storage.MailAccount, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, errors.New("account id is required")
	}
	cur, gerr := s.store.MailGetAccount(ctx, req.ID)
	if gerr != nil {
		return nil, gerr
	}
	if cur.ID == "" {
		return nil, storage.ErrNotFound
	}
	applyUpdates(&cur, req)

	// Optional password change (C6: bytes only via os.Pipe).
	var newPassword []byte
	if strings.TrimSpace(req.Password) != "" {
		newPassword = []byte(req.Password)
	}
	binPath := s.resolveMoxBinPath(ctx)
	if len(newPassword) > 0 && binPath != "" {
		if err := moxcli.SetAccountPassword(ctx, binPath, cur.Address, newPassword); err != nil {
			return nil, fmt.Errorf("mox setaccountpassword: %w", err)
		}
		cur.LastPasswordChangedAt = time.Now().UTC().Format(time.RFC3339)
	}

	cur.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	saved, uerr := s.store.MailUpdateAccount(ctx, cur)
	if uerr != nil {
		return nil, fmt.Errorf("update account: %w", uerr)
	}
	s.addAudit(ctx, EventTypeAccountUpdated,
		fmt.Sprintf("updated account %s", saved.Address),
		map[string]any{
			"account_id":            saved.ID,
			"domain_id":             saved.DomainID,
			"password_changed":      len(newPassword) > 0,
			"display_name_changed":  req.DisplayName != "",
			"is_admin":              saved.IsAdmin,
			"status":                saved.Status,
		}, "medium")
	s.publish(ctx, EventTypeAccountUpdated, map[string]any{
		"id":       saved.ID,
		"address":  saved.Address,
		"is_admin": saved.IsAdmin,
		"status":   saved.Status,
	})
	if len(newPassword) > 0 {
		s.publish(ctx, EventTypeAccountPasswordChanged, map[string]any{
			"id":        saved.ID,
			"address":   saved.Address,
			"generated": false,
		})
	}
	s.touchLastChange()
	return &saved, nil
}

func applyUpdates(cur *storage.MailAccount, req AccountUpdateRequest) {
	if req.DisplayName != "" {
		cur.DisplayName = req.DisplayName
	}
	if req.QuotaMB > 0 {
		cur.QuotaMB = req.QuotaMB
	}
	cur.IsAdmin = req.IsAdmin
	cur.IMAPSyncEnabled = req.IMAPSyncEnabled
	if req.Status != "" {
		cur.Status = req.Status
	}
}

// MailAccountDelete removes an account row and, if a CLI runner is wired,
// deletes the account from mox as well.
func (s *Service) MailAccountDelete(ctx context.Context, id string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("account id is required")
	}
	cur, gerr := s.store.MailGetAccount(ctx, id)
	if gerr != nil || cur.ID == "" {
		return storage.ErrNotFound
	}
	addr := cur.Address

	// Best-effort CLI delete; DB delete always proceeds so a stuck mox
	// process cannot prevent a user data removal request.
	if s.cli != nil {
		ok, _, _ := s.cli.AccountDelete(ctx, addr)
		_ = ok
	}
	if err := s.store.MailDeleteAccount(ctx, id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	s.addAudit(ctx, EventTypeAccountDeleted,
		fmt.Sprintf("deleted account %s", addr),
		map[string]any{"account_id": id, "address": addr},
		"high")
	s.publish(ctx, EventTypeAccountDeleted, map[string]any{
		"id":      id,
		"address": addr,
	})
	s.touchLastChange()
	return nil
}

// MailAccountList returns all accounts matching the optional domain filter.
func (s *Service) MailAccountList(ctx context.Context, domainID, status string) (*AccountListResponse, error) {
	rows, err := s.store.MailListAccounts(ctx, domainID, status)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	active := 0
	admins := 0
	for _, r := range rows {
		if r.Status == "" || r.Status == "active" {
			active++
		}
		if r.IsAdmin {
			admins++
		}
	}
	importRO := false
	if settings, serr := s.store.MailGetSettings(ctx); serr == nil && settings != nil {
		importRO = settings.ImportMode
	}
	return &AccountListResponse{
		Items:    rows,
		Count:    len(rows),
		Active:   active,
		Admins:   admins,
		Drifted:  s.Drifted(),
		ImportRO: importRO,
	}, nil
}

// MailAccountGet returns a single account row.
func (s *Service) MailAccountGet(ctx context.Context, id string) (*AccountResponse, error) {
	if id == "" {
		return nil, errors.New("account id is required")
	}
	cur, err := s.store.MailGetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.ID == "" {
		return nil, storage.ErrNotFound
	}
	return &AccountResponse{MailAccount: cur, Drifted: s.Drifted()}, nil
}

// MailAccountResetPassword generates a new 18-byte CSPRNG password and
// applies it via the mox CLI pipe (C6).  The password is returned to the
// caller exactly once – never logged, audited, or persisted.
func (s *Service) MailAccountResetPassword(ctx context.Context, id string) (*AccountResetResponse, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("account id is required")
	}
	cur, gerr := s.store.MailGetAccount(ctx, id)
	if gerr != nil || cur.ID == "" {
		return nil, storage.ErrNotFound
	}
	binPath := s.resolveMoxBinPath(ctx)
	if binPath == "" {
		return nil, errors.New("mox binary not configured; cannot reset password")
	}
	pwBuf, perr := generatePassword()
	if perr != nil {
		return nil, perr
	}
	if err := moxcli.SetAccountPassword(ctx, binPath, cur.Address, pwBuf); err != nil {
		return nil, fmt.Errorf("mox setaccountpassword: %w", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	cur.LastPasswordChangedAt = ts
	cur.UpdatedAt = ts
	if _, uerr := s.store.MailUpdateAccount(ctx, cur); uerr != nil {
		s.log.WarnContext(ctx, "mail: update account ts after password reset failed", "error", uerr)
	}
	s.addAudit(ctx, EventTypeAccountPasswordChanged,
		fmt.Sprintf("reset password for %s", cur.Address),
		map[string]any{
			"account_id": id,
			"address":    cur.Address,
			"generated":  true,
		}, "high")
	s.publish(ctx, EventTypeAccountPasswordChanged, map[string]any{
		"id":        id,
		"address":   cur.Address,
		"generated": true,
	})
	s.touchLastChange()
	// Return the password ONLY in the response body; do not log it anywhere.
	return &AccountResetResponse{
		AccountID:         id,
		GeneratedPassword: string(pwBuf),
	}, nil
}

// MailAccountDisable suspends an account (status = "suspended") and flips
// Enabled=false in the DB.  The mox CLI side is not wired in Phase 5 so the
// account remains technically present on-disk; the DB row is authoritative.
func (s *Service) MailAccountDisable(ctx context.Context, id string) (*storage.MailAccount, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("account id is required")
	}
	cur, gerr := s.store.MailGetAccount(ctx, id)
	if gerr != nil || cur.ID == "" {
		return nil, storage.ErrNotFound
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	cur.Status = "suspended"
	cur.Enabled = false
	cur.UpdatedAt = ts
	saved, uerr := s.store.MailUpdateAccount(ctx, cur)
	if uerr != nil {
		return nil, fmt.Errorf("disable account: %w", uerr)
	}
	s.addAudit(ctx, EventTypeAccountUpdated,
		fmt.Sprintf("disabled account %s (status=suspended)", saved.Address),
		map[string]any{
			"account_id": saved.ID,
			"address":    saved.Address,
			"status":     saved.Status,
		}, "high")
	s.publish(ctx, EventTypeAccountUpdated, map[string]any{
		"id":      saved.ID,
		"address": saved.Address,
		"status":  saved.Status,
		"action":  "disabled",
	})
	s.touchLastChange()
	return &saved, nil
}

// MailAccountResyncIMAP kicks off an IMAP sync refresh for accounts that
// have IMAPSyncEnabled=true.  In Phase 7 this delegates to the per-account
// goroutine managed by Service.imapSyncManager – the Manager.Start() method
// is idempotent, so calling this repeatedly simply ensures a loop is running
// and returns the refreshed account row.
func (s *Service) MailAccountResyncIMAP(ctx context.Context, id string) (*storage.MailAccount, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("account id is required")
	}
	cur, gerr := s.store.MailGetAccount(ctx, id)
	if gerr != nil || cur.ID == "" {
		return nil, storage.ErrNotFound
	}
	if !cur.IMAPSyncEnabled && cur.ImapHost == "" {
		return nil, errors.New("imap sync is not configured for this account")
	}
	if err := s.MailImapSyncStart(ctx, id); err != nil {
		return nil, fmt.Errorf("start imap sync: %w", err)
	}
	refreshed, rerr := s.store.MailGetAccount(ctx, id)
	if rerr != nil {
		return nil, rerr
	}
	s.addAudit(ctx, "mail.sync.started",
		fmt.Sprintf("started IMAP resync for %s", refreshed.Address),
		map[string]any{"account_id": id, "address": refreshed.Address},
		"low")
	s.publish(ctx, EventTypeSyncStarted, map[string]any{
		"id":      id,
		"address": refreshed.Address,
	})
	return &refreshed, nil
}
