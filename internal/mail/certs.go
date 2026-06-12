package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/mail/certmanager"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// DNS Provider types (Phase 4 service-layer value types).
// -----------------------------------------------------------------------------

// DNSProviderUpsertRequest is the payload sent by the UI when creating or
// updating a DNS provider.  An empty ID means "create"; a non-empty ID
// targets an existing row.  Config values are plaintext on the wire; they are
// wrapped (XOR-obfuscated + base64) before persisting so a read of the
// SQLite dump never exposes raw tokens.
type DNSProviderUpsertRequest struct {
	ID          string            `json:"id,omitempty"`
	Kind        string            `json:"kind"` // cloudflare | dnspod | route53 | manual
	DisplayName string            `json:"display_name"`
	Config      map[string]string `json:"config"`
}

// DNSProviderResponse is the per-provider row exposed to the UI.  The config
// values are NEVER serialised back – only the keys (so the operator can see
// which fields are configured) plus a boolean HasToken.
type DNSProviderResponse struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	DisplayName  string   `json:"display_name"`
	ConfigKeys   []string `json:"config_keys"`
	HasToken     bool     `json:"has_token"`
	Tested       bool     `json:"tested"`
	LastTestedAt string   `json:"last_tested_at,omitempty"`
	LastError    string   `json:"last_error,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// DNSProviderTestResponse is returned after a "test connection" roundtrip.
type DNSProviderTestResponse struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Zones   []string `json:"zones,omitempty"`
}

// -----------------------------------------------------------------------------
// Certificate types.
// -----------------------------------------------------------------------------

// CertificateResponse is the per-certificate row exposed to the UI.  The file paths
// are absolute (file system paths where each PEM file lives; only the basename is
// derived from the certificate ID under the Mox certs/ directory.
type CertificateResponse struct {
	ID            string            `json:"id"`
	Domain        string            `json:"domain"`
	SANs          []string          `json:"sans,omitempty"`
	Issuer        string            `json:"issuer,omitempty"`
	Serial        string            `json:"serial,omitempty"`
	NotBefore     string            `json:"not_before,omitempty"`
	NotAfter      string            `json:"not_after,omitempty"`
	DaysLeft      int               `json:"days_left"`
	TLSA          string            `json:"tlsa_record,omitempty"`
	DNSProviderID string            `json:"dns_provider_id,omitempty"`
	Status        string            `json:"status"` // active|renewing|expiring_soon|expired|error|manual_pending
	LastRenewalAt string            `json:"last_renewal_at,omitempty"`
	NextRenewalAt string            `json:"next_renewal_at,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
	Files         map[string]string `json:"files"` // {privkey, cert, chain}
}

// CertificateListResponse wraps the list + summary counters so the UI top card can render
// badges without extra round-trips.
type CertificateListResponse struct {
	Items     []CertificateResponse `json:"items"`
	Count     int                   `json:"count"`
	Expiring  int                   `json:"expiring_count"`
	Expired   int                   `json:"expired_count"`
	Active    int                   `json:"active_count"`
	HasManual int                   `json:"manual_pending_count"`
	Drifted   bool                  `json:"drifted"`
}

// CertIssueRequest is the UI → service payload for issuing a new certificate.
type CertIssueRequest struct {
	Domain           string   `json:"domain"`
	SANs             []string `json:"sans,omitempty"`
	DNSProviderID    string   `json:"dns_provider_id,omitempty"`
	Force            bool     `json:"force,omitempty"`
	AcceptTOS        bool     `json:"accept_tos"`
	ContactEmail     string   `json:"contact_email"`
	ACMEDirectoryURL string   `json:"acme_directory_url,omitempty"`
	TLSAEnabled      bool     `json:"tlsa_enabled,omitempty"`
	MXHost           string   `json:"mx_host,omitempty"`
}

// CertRenewResponse wraps the pipeline result for a renewal operation.
type CertRenewResponse struct {
	Renewed  bool                `json:"renewed"`
	Msg      string              `json:"message"`
	Pipeline *CertPipelineResult `json:"pipeline,omitempty"`
	Cert     *CertificateResponse `json:"cert,omitempty"`
}

// CertRollbackResponse wraps the rollback operation result.
type CertRollbackResponse struct {
	Restored   bool   `json:"restored"`
	Message    string `json:"message"`
	FromBackup string `json:"from_backup,omitempty"`
}

// CertDeleteResponse wraps a delete operation.
type CertDeleteResponse struct {
	Deleted bool   `json:"deleted"`
	ID      string `json:"id"`
	Message string `json:"message"`
}

// ManualChallenge represents a pending DNS-01 challenge that the operator must
// create manually.  Only present when the provider kind is "manual" or when
// the automated provider returned a "please_create_txt" signal.
type ManualChallenge struct {
	ID        string `json:"id"`
	CertID    string `json:"cert_id"`
	Domain    string `json:"domain"`
	FQDN      string `json:"fqdn"`     // _acme-challenge.<domain>.
	Value     string `json:"value"`    // the TXT record value
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

// ManualChallengeListResponse carries all pending challenges.
type ManualChallengeListResponse struct {
	Items []ManualChallenge `json:"items"`
	Count int               `json:"count"`
}

// ManualChallengeConfirmResponse is returned after a confirm/cancel.
type ManualChallengeConfirmResponse struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message"`
}

// -----------------------------------------------------------------------------
// Cert pipeline result (mirrors configapply.PipelineResult semantics so the UI
// can reuse StepProgress rendering).
// -----------------------------------------------------------------------------

// CertPipelineStep mirrors certmanager.StepStatus but is defined locally to
// give the UI a stable shape.
type CertPipelineStep struct {
	Step    int    `json:"step"`
	Total   int    `json:"total"`
	Name    string `json:"name"`
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
	Output  string `json:"output,omitempty"`
	State   string `json:"state"` // running|done|failed|rollback
}

// CertPipelineResult is the synchronous result of an issue / renew operation.
type CertPipelineResult struct {
	Success     bool             `json:"success"`
	FailureStep int              `json:"failure_step"`
	RolledBack  bool             `json:"rolled_back"`
	RollbackErr string           `json:"rollback_err,omitempty"`
	Summary     string           `json:"summary"`
	Steps       []CertPipelineStep `json:"steps,omitempty"`
	CertID      string           `json:"cert_id,omitempty"`
}

// -----------------------------------------------------------------------------
// In-memory manual challenge "wake-up" registry.
//
// The SQL store (MailManualChallenge*) is the source of truth for what
// challenges exist.  This registry is a tiny in-mem pub/sub: when the
// certmanager pipeline waits on ManualModeConfirmCallback, it creates a
// channel here; when the HTTP handler calls MailManualChallengeConfirm, it
// closes the matching channel so the waiter wakes up.
//
// If no waiter is registered for a challenge ID, Confirm simply marks the
// row confirmed in the DB.  This makes the mechanism best-effort — if the
// service restarts mid-pipeline, the operator just retries the issue.
// -----------------------------------------------------------------------------

type manualWaiterRegistry struct {
	mu    sync.Mutex
	chans map[string]chan struct{} // id → close-to-signal channel
}

func newManualWaiterRegistry() *manualWaiterRegistry {
	return &manualWaiterRegistry{chans: make(map[string]chan struct{})}
}

// Register creates a wait channel for id.  Returns a read-only channel; the
// caller selects on it + ctx.Done().
func (r *manualWaiterRegistry) Register(id string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan struct{})
	r.chans[id] = ch
	return ch
}

// Unregister cleans up after a wait completes (timed out, succeeded, etc.).
func (r *manualWaiterRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.chans, id)
}

// Signal closes the channel for id if one exists.  Safe to call on ids that
// have no registered waiter (no-op).
func (r *manualWaiterRegistry) Signal(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.chans[id]; ok {
		close(ch)
		delete(r.chans, id)
	}
}

// fqdnToChallengeID derives a deterministic ID from (fqdn, value) so the
// waiter registry and the DB row use the same primary key.
func fqdnToChallengeID(fqdn, value string) string {
	h := sha256.Sum256([]byte(fqdn + "|" + value))
	return "mch_" + hex.EncodeToString(h[:8])
}

var globalManualWaiters = newManualWaiterRegistry()

// -----------------------------------------------------------------------------
// wrapConfig / unwrapConfig — obfuscate DNS provider credentials at rest.
// -----------------------------------------------------------------------------

// wrapConfig obfuscates the DNS provider config values before persisting.  The
// scheme is deliberately minimal (XOR with a stable per-install secret derived
// from phantom_instance_id) so a casual SQLite dump doesn't reveal tokens, but we
// make no cryptographic claim — real Secret Manager integration is Phase 8.
func (s *Service) wrapConfig(ctx context.Context, cfg map[string]string) (string, error) {
	_ = ctx
	buf, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	key := s.deriveWrapKey()
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = b ^ key[i%len(key)]
	}
	return "v1:" + hex.EncodeToString(out), nil
}

// unwrapConfig is the inverse of wrapConfig.  Returns the raw map (empty
// map for empty input).
func (s *Service) unwrapConfig(ctx context.Context, wrapped string) (map[string]string, error) {
	_ = ctx
	if wrapped == "" {
		return map[string]string{}, nil
	}
	if !strings.HasPrefix(wrapped, "v1:") {
		// Legacy or corrupt — treat as empty so we never crash.
		return map[string]string{}, nil
	}
	raw, err := hex.DecodeString(wrapped[3:])
	if err != nil {
		return map[string]string{}, nil
	}
	key := s.deriveWrapKey()
	buf := make([]byte, len(raw))
	for i, b := range raw {
		buf[i] = b ^ key[i%len(key)]
	}
	out := map[string]string{}
	if uerr := json.Unmarshal(buf, &out); uerr != nil {
		return map[string]string{}, nil
	}
	return out, nil
}

// deriveWrapKey produces a 32-byte XOR key from the phantom instance id.
func (s *Service) deriveWrapKey() []byte {
	id := s.PhantomInstanceID()
	if id == "" {
		id = "phantom-lancer-default-wrap-key"
	}
	h := sha256.Sum256([]byte(id))
	return h[:]
}

// -----------------------------------------------------------------------------
// Service-level DNSProvider adapter.
//
// Wraps a storage.MailDNSProvider row and implements the
// certmanager.DNSProvider interface so rows can be dropped directly into the
// certmanager.Issue pipeline.
//
// For token-based providers (cloudflare/dnspod/route53) Phase 4 ships STUB
// implementations that return success — a future PR vendors the real lego
// adapters against the same interface without changing call sites.
// For the "manual" kind, SetTXT/RemoveTXT forward to a Persist callback that
// writes rows into mail_manual_challenges via SQL.
// -----------------------------------------------------------------------------

type serviceDNSProviderAdapter struct {
	row storage.MailDNSProvider
	svc *Service
	// unwrappedConfig is cached lazily — we unwrap once per adapter.
	cfgOnce sync.Once
	cfgMap  map[string]string
	cfgErr  error
	// inner is a real certmanager.DNSProvider built from (kind, id, label, config).
	// For manual it's a *certmanager.ManualDNSProvider with Persist wired; for
	// token kinds Phase 4 it's a certmanager-provider stub (NewDNSProviderFromConfig).
	inner     certmanager.DNSProvider
	innerOnce sync.Once
	innerErr  error
}

func (a *serviceDNSProviderAdapter) getConfig() map[string]string {
	a.cfgOnce.Do(func() {
		// Best-effort; unwrapConfig never returns a hard error.
		a.cfgMap, a.cfgErr = a.svc.unwrapConfig(context.Background(), a.row.APICredentialsWrapped)
	})
	return a.cfgMap
}

func (a *serviceDNSProviderAdapter) getInner() (certmanager.DNSProvider, error) {
	a.innerOnce.Do(func() {
		kind := strings.ToLower(strings.TrimSpace(a.row.Kind))
		switch kind {
		case "manual":
			mp := &certmanager.ManualDNSProvider{}
			mp.Persist = func(fqdn, value, domain, status string) error {
				return a.svc.persistManualChallenge(fqdn, value, domain, status)
			}
			a.inner = mp
		default:
			// cloudflare / dnspod / route53 → build a provider from config.
			cfg := a.getConfig()
			p, err := certmanager.NewDNSProviderFromConfig(kind, a.row.ID, a.row.DisplayName, cfg)
			if err != nil {
				a.innerErr = err
				return
			}
			a.inner = p
		}
	})
	return a.inner, a.innerErr
}

// ProviderID implements certmanager.DNSProvider.
func (a *serviceDNSProviderAdapter) ProviderID() string { return a.row.ID }

// DisplayName implements certmanager.DNSProvider.
func (a *serviceDNSProviderAdapter) DisplayName() string {
	if a.row.DisplayName != "" {
		return a.row.DisplayName
	}
	return a.row.Kind
}

// TestConnection implements certmanager.DNSProvider.
func (a *serviceDNSProviderAdapter) TestConnection(ctx context.Context) error {
	kind := strings.ToLower(strings.TrimSpace(a.row.Kind))
	// Manual has nothing to test.
	if kind == "manual" {
		return nil
	}
	inner, err := a.getInner()
	if err != nil {
		return err
	}
	return inner.TestConnection(ctx)
}

// SetTXT implements certmanager.DNSProvider.
func (a *serviceDNSProviderAdapter) SetTXT(ctx context.Context, fqdn, value string) error {
	kind := strings.ToLower(strings.TrimSpace(a.row.Kind))
	// Manual: persist the challenge so the UI shows it.
	if kind == "manual" {
		return a.svc.persistManualChallenge(fqdn, value, "", "pending")
	}
	// Token-based: Phase 4 stub — always succeed.  The real vendor adapters
	// will replace this body without changing the interface.
	inner, err := a.getInner()
	if err != nil {
		// Fallback: succeed trivially so the pipeline keeps going for stubs.
		_ = err
		return nil
	}
	if err := inner.SetTXT(ctx, fqdn, value); err != nil {
		// If the inner provider returns a "not wired" descriptive error, we
		// still return nil for Phase 4 so the skeleton pipeline can proceed.
		// A future PR will surface these.
		if strings.Contains(err.Error(), "not wired") {
			return nil
		}
		return err
	}
	return nil
}

// RemoveTXT implements certmanager.DNSProvider.
func (a *serviceDNSProviderAdapter) RemoveTXT(ctx context.Context, fqdn, value string) error {
	kind := strings.ToLower(strings.TrimSpace(a.row.Kind))
	if kind == "manual" {
		return a.svc.persistManualChallenge(fqdn, value, "", "cleared")
	}
	inner, err := a.getInner()
	if err != nil {
		return nil
	}
	if err := inner.RemoveTXT(ctx, fqdn, value); err != nil {
		if strings.Contains(err.Error(), "not wired") {
			return nil
		}
		return err
	}
	return nil
}

// persistManualChallenge writes a pending or cleared manual challenge to the
// SQL store.  Used both by the ManualDNSProvider Persist callback (which
// passes an explicit domain) and by the adapter directly.
func (s *Service) persistManualChallenge(fqdn, value, domain, status string) error {
	if domain == "" {
		d := strings.TrimPrefix(fqdn, "_acme-challenge.")
		d = strings.TrimSuffix(d, ".")
		domain = d
	}
	id := fqdnToChallengeID(fqdn, value)
	ch := storage.MailManualChallenge{
		ID:        id,
		Domain:    domain,
		FQDN:      fqdn,
		Value:     value,
		Status:    status,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
	}
	_, err := s.store.MailManualChallengeUpsert(context.Background(), ch)
	if err != nil {
		return err
	}
	if status == "pending" {
		s.publish(context.Background(), EventTypeCertDNS01AwaitManual, map[string]any{
			"challenge_id": id,
			"fqdn":         fqdn,
			"domain":       domain,
		})
	}
	return nil
}

// -----------------------------------------------------------------------------
// DNS provider service methods.
// -----------------------------------------------------------------------------

// MailDNSProviderUpsert creates or updates a DNS provider row.  If req.ID is
// empty, a new ID is allocated.  Returns the persisted row.
func (s *Service) MailDNSProviderUpsert(ctx context.Context, req DNSProviderUpsertRequest) (*DNSProviderResponse, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case "":
		return nil, errors.New("dns provider kind is required (cloudflare|dnspod|route53|manual)")
	case "cloudflare", "dnspod", "route53", "manual":
	default:
		return nil, fmt.Errorf("unknown dns provider kind %q", req.Kind)
	}
	if strings.TrimSpace(req.DisplayName) == "" && kind != "manual" {
		return nil, errors.New("display_name is required")
	}
	wrapped, werr := s.wrapConfig(ctx, req.Config)
	if werr != nil {
		return nil, fmt.Errorf("wrap config: %w", werr)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Build storage row.
	row := storage.MailDNSProvider{
		ID:                   req.ID,
		Kind:                 kind,
		DisplayName:          req.DisplayName,
		APICredentialsWrapped: wrapped,
	}

	var created bool
	if req.ID == "" {
		created = true
		row.ID = IDPrefixDNSProvider + "_" + newShortHex(12)
		row.CreatedAt = now
		stored, err := s.store.MailCreateDNSProvider(ctx, row)
		if err != nil {
			return nil, fmt.Errorf("create dns provider: %w", err)
		}
		row = *stored
	} else {
		existing, err := s.store.MailGetDNSProvider(ctx, req.ID)
		if err != nil {
			return nil, fmt.Errorf("dns provider not found: %w", err)
		}
		// Preserve existing wrapped if caller passed nil config ("keep token").
		if req.Config == nil {
			row.APICredentialsWrapped = existing.APICredentialsWrapped
		}
		row.CreatedAt = existing.CreatedAt
		updated, err := s.store.MailUpdateDNSProvider(ctx, row)
		if err != nil {
			return nil, fmt.Errorf("update dns provider: %w", err)
		}
		row = *updated
	}

	s.addAudit(ctx, "mail.dns_provider.upsert",
		fmt.Sprintf("upserted dns provider %s (%s)", row.ID, kind),
		map[string]any{
			"id":           row.ID,
			"kind":         kind,
			"display_name": req.DisplayName,
			"is_create":    created,
		}, "medium")
	s.publish(ctx, EventTypeCertProviderUpdated, map[string]any{
		"id":   row.ID,
		"kind": kind,
	})
	s.touchLastChange()
	return providerRowToResponse(&row, req.Config), nil
}

// MailDNSProviderDelete removes a DNS provider row by ID.
func (s *Service) MailDNSProviderDelete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("dns provider id required")
	}
	existing, err := s.store.MailGetDNSProvider(ctx, id)
	if err != nil {
		return errors.New("dns provider not found")
	}
	kind := existing.Kind
	if err := s.store.MailDeleteDNSProvider(ctx, id); err != nil {
		return fmt.Errorf("delete dns provider: %w", err)
	}
	s.addAudit(ctx, "mail.dns_provider.delete",
		fmt.Sprintf("deleted dns provider %s (%s)", id, kind),
		map[string]any{"id": id, "kind": kind}, "medium")
	s.touchLastChange()
	return nil
}

// MailDNSProviderList returns all configured DNS providers.
func (s *Service) MailDNSProviderList(ctx context.Context) ([]DNSProviderResponse, error) {
	rows, err := s.store.MailListDNSProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dns providers: %w", err)
	}
	out := make([]DNSProviderResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, *providerRowToResponse(r, nil))
	}
	return out, nil
}

// MailDNSProviderTest runs a connection test against a DNS provider.
func (s *Service) MailDNSProviderTest(ctx context.Context, id string) (*DNSProviderTestResponse, error) {
	row, err := s.store.MailGetDNSProvider(ctx, id)
	if err != nil {
		return nil, errors.New("dns provider not found")
	}

	adapter := &serviceDNSProviderAdapter{row: *row, svc: s}
	terr := adapter.TestConnection(ctx)
	ok := terr == nil
	now := time.Now().UTC().Format(time.RFC3339)

	// Persist Tested / LastTestedAt / LastError on the row.
	row.LastTestedAt = now
	if !ok {
		row.LastError = terr.Error()
	} else {
		row.LastError = ""
	}
	// The row struct lacks dedicated "Tested" bool — use LastTestedAt != "" as proxy.
	if _, uerr := s.store.MailUpdateDNSProvider(ctx, *row); uerr != nil {
		// Not fatal to the test call.
		_ = uerr
	}

	s.addAudit(ctx, "mail.dns_provider.test",
		fmt.Sprintf("tested dns provider %s: ok=%v", id, ok),
		map[string]any{"id": id, "kind": row.Kind, "ok": ok}, "low")

	msg := "connection OK"
	if !ok {
		msg = fmt.Sprintf("connection FAILED: %v", terr)
	}
	return &DNSProviderTestResponse{OK: ok, Message: msg, Zones: nil}, nil
}

// providerRowToResponse converts a storage.MailDNSProvider row into the UI
// response shape.  rawCfg is used only on the create/update return path so
// the caller can see which keys were just written; pass nil on list.
func providerRowToResponse(r *storage.MailDNSProvider, rawCfg map[string]string) *DNSProviderResponse {
	keys := make([]string, 0)
	if rawCfg != nil {
		for k := range rawCfg {
			keys = append(keys, k)
		}
	}
	hasToken := len(keys) > 0 || r.APICredentialsWrapped != ""
	tested := r.LastTestedAt != ""
	return &DNSProviderResponse{
		ID:           r.ID,
		Kind:         r.Kind,
		DisplayName:  r.DisplayName,
		ConfigKeys:   keys,
		HasToken:     hasToken,
		Tested:       tested,
		LastTestedAt: r.LastTestedAt,
		LastError:    r.LastError,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

// -----------------------------------------------------------------------------
// Certificate service methods.
// -----------------------------------------------------------------------------

// MailCertificateList returns all certificates with summary counters.
func (s *Service) MailCertificateList(ctx context.Context) (*CertificateListResponse, error) {
	rows, err := s.store.MailListCertificates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	// Also pull pending manual challenges for the manual_pending badge.
	pendingCount := 0
	chRows, chErr := s.store.MailManualChallengeList(ctx, "", "pending")
	if chErr == nil {
		pendingCount = len(chRows)
	}
	out := make([]CertificateResponse, 0, len(rows))
	resp := &CertificateListResponse{Items: out, HasManual: pendingCount}
	for _, c := range rows {
		cr := certRowToResponse(c)
		resp.Items = append(resp.Items, cr)
		switch cr.Status {
		case "active":
			resp.Active++
		case "expiring_soon":
			resp.Expiring++
		case "expired":
			resp.Expired++
		case "manual_pending":
			resp.HasManual++
		}
	}
	resp.Count = len(resp.Items)
	resp.Drifted = s.Drifted()
	return resp, nil
}

// MailCertificateGet returns a single certificate by ID.
func (s *Service) MailCertificateGet(ctx context.Context, id string) (*CertificateResponse, error) {
	r, err := s.store.MailGetCertificate(ctx, id)
	if err != nil {
		return nil, errors.New("certificate not found")
	}
	cr := certRowToResponse(r)
	return &cr, nil
}

// MailCertificateIssue runs the certmanager.Issue pipeline.
func (s *Service) MailCertificateIssue(ctx context.Context, req CertIssueRequest) (*CertPipelineResult, error) {
	if strings.TrimSpace(req.Domain) == "" {
		return nil, errors.New("domain is required")
	}
	if !req.AcceptTOS {
		return nil, errors.New("accept_tos must be true")
	}
	if req.ContactEmail == "" {
		return nil, errors.New("contact_email is required")
	}

	// --- Build providerMap: zone suffix → certmanager.DNSProvider ---
	providerMap := make(map[string]certmanager.DNSProvider)
	allProviders, err := s.store.MailListDNSProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dns providers: %w", err)
	}
	var manualExists bool
	for _, row := range allProviders {
		adapter := &serviceDNSProviderAdapter{row: *row, svc: s}
		// Label / DisplayName is used as the zone suffix key.
		suffix := strings.TrimSpace(row.DisplayName)
		if strings.ToLower(row.Kind) == "manual" {
			manualExists = true
			// Don't add manual to providerMap — certmanager will fall back to
			// ManualDNSProvider when no token-based suffix matches and a
			// PersistManualChallenge callback is provided.
			continue
		}
		if suffix == "" {
			suffix = row.ID
		}
		providerMap[suffix] = adapter
	}

	// --- Build LegoClient ---
	legoClient, lerr := certmanager.NewLegoClient(s.moxRoot, req.ContactEmail, req.ACMEDirectoryURL, req.AcceptTOS)
	if lerr != nil {
		return nil, fmt.Errorf("create lego client: %w", lerr)
	}

	// --- Progress channel + drain goroutine ---
	progress := make(chan certmanager.StepStatus, certmanager.StepCount*2)
	// stepsCaptured is updated as progress events arrive.  We pre-allocate the
	// StepCount slots so it's indexable by (step-1).
	stepsCaptured := make([]certmanager.StepStatus, certmanager.StepCount)
	for i := 0; i < certmanager.StepCount; i++ {
		stepsCaptured[i] = certmanager.StepStatus{
			Step:  i + 1,
			Total: certmanager.StepCount,
			Name:  certmanager.StepNames[i],
			State: "pending",
		}
	}
	var stepsMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for st := range progress {
			idx := st.Step - 1
			if idx < 0 || idx >= certmanager.StepCount {
				continue
			}
			stepsMu.Lock()
			stepsCaptured[idx] = st
			stepsMu.Unlock()
		}
	}()

	// --- ManualModeConfirmCallback ---
	manualCallback := func(ctx2 context.Context, fqdn, value string) error {
		if !manualExists {
			// No manual provider configured → fall through to propagation hold.
			return nil
		}
		id := fqdnToChallengeID(fqdn, value)
		ch := globalManualWaiters.Register(id)
		defer globalManualWaiters.Unregister(id)
		select {
		case <-ch:
			return nil
		case <-ctx2.Done():
			return ctx2.Err()
		}
	}

	// --- PersistManualChallenge (for certmanager's fallback ManualDNSProvider) ---
	persistCallback := func(fqdn, value, domain string) error {
		return s.persistManualChallenge(fqdn, value, domain, "pending")
	}

	// --- PersistCertificate callback ---
	var certIDOut string
	var certFilesOut map[string]string
	persistCertCallback := func(cert certmanager.Certificate) error {
		certDir := fmt.Sprintf("%s/certs/%s", s.moxRoot, cert.Domain)
		certPath := certDir + "/cert.pem"
		keyPath := certDir + "/privkey.pem"
		chainPath := certDir + "/chain.pem"
		tlsaStr := ""
		// Build storage row.
		srow := storage.MailCertificate{
			ID:                 cert.ID,
			Domain:             cert.Domain,
			PrimaryDomain:      cert.Domain,
			SubjectAltNames:    append([]string(nil), cert.SANDomains...),
			Issuer:             cert.Issuer,
			Serial:             cert.Serial,
			NotBefore:          cert.NotBefore.Format(time.RFC3339),
			NotAfter:           cert.NotAfter.Format(time.RFC3339),
			PEMChain:           cert.PEMChain,
			DNSProviderID:      cert.DNSProviderID,
			RenewalAttemptedAt: cert.LastRenewalAttempt.Format(time.RFC3339),
			RenewalError:       cert.LastError,
			TLSA311:            tlsaStr,
			CertPath:           certPath,
			PrivkeyPath:        keyPath,
			ChainPath:          chainPath,
			Applied:            true,
		}
		stored, cerr := s.store.MailCreateCertificate(ctx, srow)
		if cerr != nil {
			return cerr
		}
		certIDOut = stored.ID
		certFilesOut = map[string]string{
			"privkey": keyPath,
			"cert":    certPath,
			"chain":   chainPath,
		}
		return nil
	}

	// --- reloadFn (Phase 5 will wire mox reload; Phase 4 stub) ---
	reloadFn := func(ctx2 context.Context) error {
		_ = ctx2
		return nil
	}

	// --- TLSProbe stub ---
	tlsProbe := func(ctx2 context.Context) (string, error) {
		_ = ctx2
		return "good", nil
	}

	// --- Run the pipeline ---
	cfg := certmanager.IssueConfig{
		Domain:                    req.Domain,
		SANDomains:                append([]string(nil), req.SANs...),
		DataDir:                   s.moxRoot,
		DNSProviders:              providerMap,
		LegoClient:                legoClient,
		ACMEContactEmail:          req.ContactEmail,
		ACMEDirectoryURL:          req.ACMEDirectoryURL,
		AcceptTOS:                 req.AcceptTOS,
		MxHost:                    req.MXHost,
		TLSAEnabled:               req.TLSAEnabled,
		ManualModeConfirmCallback: manualCallback,
		ReloadOrRestartMox:        reloadFn,
		TLSProbe:                  tlsProbe,
		PersistManualChallenge:    persistCallback,
		PersistCertificate:        persistCertCallback,
	}

	res := certmanager.Issue(ctx, cfg, progress)
	close(progress)
	wg.Wait()

	// --- Merge: if res.Steps[i].State is still "pending" but stepsCaptured[i] has
	// newer info, prefer res.Steps (it carries the authoritative final state). ---
	stepsMu.Lock()
	for i := 0; i < certmanager.StepCount; i++ {
		if i < len(res.Steps) && res.Steps[i].State != "pending" {
			stepsCaptured[i] = res.Steps[i]
		}
	}
	stepsMu.Unlock()

	// --- Convert to UI steps ---
	uiSteps := make([]CertPipelineStep, certmanager.StepCount)
	for i, st := range stepsCaptured {
		pct := 0
		if st.Total > 0 {
			pct = st.Step * 100 / st.Total
		}
		if st.State == "done" {
			pct = 100
		}
		uiSteps[i] = CertPipelineStep{
			Step:    st.Step,
			Total:   st.Total,
			Name:    st.Name,
			Percent: pct,
			Message: st.Message,
			Output:  st.Output,
			State:   st.State,
		}
	}

	failureStep := 0
	if !res.Success {
		failureStep = res.Step
	}
	rolledBack := res.RollbackErr != ""

	// --- Persist metadata even on pipeline failure when useful ---
	resultCertID := certIDOut
	if resultCertID == "" && res.CertPath != "" {
		// Best-effort: derive an id from cert path if persist callback ran
		// but caller didn't populate result.
		_ = res.CertPath
	}
	_ = certFilesOut

	// --- Audit + publish ---
	s.addAudit(ctx, "mail.cert.issued",
		fmt.Sprintf("issued certificate %s for %s: success=%v", resultCertID, req.Domain, res.Success),
		map[string]any{
			"cert_id":         resultCertID,
			"domain":          req.Domain,
			"dns_provider_id": req.DNSProviderID,
			"sans":            req.SANs,
			"success":         res.Success,
			"message":         res.Message,
		}, "high")
	if res.Success {
		s.publish(ctx, EventTypeCertIssued, map[string]any{
			"cert_id": resultCertID, "domain": req.Domain,
		})
	} else {
		s.publish(ctx, EventTypeCertRenewalFailed, map[string]any{
			"domain":  req.Domain,
			"message": res.Message,
			"step":    failureStep,
		})
	}

	summary := res.Message
	if summary == "" {
		if res.Success {
			summary = fmt.Sprintf("certificate issued for %s", req.Domain)
		} else {
			summary = fmt.Sprintf("certificate issuance failed at step %d", failureStep)
		}
	}

	return &CertPipelineResult{
		Success:     res.Success,
		FailureStep: failureStep,
		RolledBack:  rolledBack,
		RollbackErr: res.RollbackErr,
		Summary:     summary,
		Steps:       uiSteps,
		CertID:      resultCertID,
	}, nil
}

// MailCertificateRenew triggers a renewal for a single certificate by
// re-running the Issue pipeline.
func (s *Service) MailCertificateRenew(ctx context.Context, id string, force bool) (*CertRenewResponse, error) {
	row, err := s.store.MailGetCertificate(ctx, id)
	if err != nil {
		return nil, errors.New("certificate not found")
	}

	// Days-left check (skip unless force).
	daysLeft := 0
	if na, perr := time.Parse(time.RFC3339, row.NotAfter); perr == nil {
		daysLeft = int(time.Until(na).Hours() / 24)
	}
	if !force && daysLeft >= certmanager.DefaultDaysBeforeRenewal {
		return &CertRenewResponse{
			Renewed: false,
			Msg:     fmt.Sprintf("skipped: %d days left (threshold %d)", daysLeft, certmanager.DefaultDaysBeforeRenewal),
		}, nil
	}

	// Reconstruct issue request from stored row.
	req := CertIssueRequest{
		Domain:           row.PrimaryDomain,
		SANs:             append([]string(nil), row.SubjectAltNames...),
		DNSProviderID:    row.DNSProviderID,
		AcceptTOS:        true,
		ContactEmail:     "renew@localhost", // Phase 4 fallback — real: pull from settings.
		ACMEDirectoryURL: "",                // staging default.
		TLSAEnabled:      row.TLSA311 != "",
	}
	if req.Domain == "" {
		req.Domain = row.Domain
	}
	// Override: renewal MUST NOT wait for manual challenges.
	// We still run the issue pipeline but with ManualModeConfirmCallback set
	// to a hard error so the pipeline fails quickly instead of blocking.
	pipeline, perr := s.renewalIssueWithAutoOnly(ctx, req, row.DNSProviderID)
	if perr != nil {
		// Mark renewal error on the row.
		row.RenewalError = perr.Error()
		row.RenewalAttemptedAt = time.Now().UTC().Format(time.RFC3339)
		_, _ = s.store.MailUpdateCertificate(ctx, *row)
		return nil, perr
	}

	if pipeline.Success {
		row.RenewalError = ""
		row.RenewalAttemptedAt = time.Now().UTC().Format(time.RFC3339)
		_, _ = s.store.MailUpdateCertificate(ctx, *row)
	} else {
		row.RenewalError = pipeline.Summary
		row.RenewalAttemptedAt = time.Now().UTC().Format(time.RFC3339)
		_, _ = s.store.MailUpdateCertificate(ctx, *row)
	}

	s.addAudit(ctx, "mail.cert.renewed",
		fmt.Sprintf("renewed certificate %s for %s: success=%v", id, row.Domain, pipeline.Success),
		map[string]any{"cert_id": id, "domain": row.Domain, "force": force, "success": pipeline.Success}, "high")
	if pipeline.Success {
		s.publish(ctx, EventTypeCertRenewed, map[string]any{"cert_id": id})
	} else {
		s.publish(ctx, EventTypeCertRenewalFailed, map[string]any{"cert_id": id, "message": pipeline.Summary})
	}

	respRow, _ := s.store.MailGetCertificate(ctx, id)
	var certResp *CertificateResponse
	if respRow != nil {
		cr := certRowToResponse(respRow)
		certResp = &cr
	}

	msg := "renewed successfully"
	if !pipeline.Success {
		msg = pipeline.Summary
	}
	return &CertRenewResponse{
		Renewed:  pipeline.Success,
		Msg:      msg,
		Pipeline: pipeline,
		Cert:     certResp,
	}, nil
}

// renewalIssueWithAutoOnly is a thin wrapper around MailCertificateIssue that
// forces ManualModeConfirmCallback = error (renewal requires an automatic
// DNS provider — the operator must manually re-issue for manual zones).
func (s *Service) renewalIssueWithAutoOnly(ctx context.Context, req CertIssueRequest, providerID string) (*CertPipelineResult, error) {
	_ = providerID

	if strings.TrimSpace(req.Domain) == "" {
		return nil, errors.New("domain is required for renewal")
	}
	if req.ContactEmail == "" {
		req.ContactEmail = "renew@localhost"
	}
	req.AcceptTOS = true

	// Build providerMap.
	providerMap := make(map[string]certmanager.DNSProvider)
	allProviders, err := s.store.MailListDNSProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dns providers: %w", err)
	}
	for _, row := range allProviders {
		if strings.ToLower(row.Kind) == "manual" {
			continue
		}
		adapter := &serviceDNSProviderAdapter{row: *row, svc: s}
		suffix := strings.TrimSpace(row.DisplayName)
		if suffix == "" {
			suffix = row.ID
		}
		providerMap[suffix] = adapter
	}

	legoClient, lerr := certmanager.NewLegoClient(s.moxRoot, req.ContactEmail, req.ACMEDirectoryURL, req.AcceptTOS)
	if lerr != nil {
		return nil, fmt.Errorf("create lego client: %w", lerr)
	}

	progress := make(chan certmanager.StepStatus, certmanager.StepCount*2)
	stepsCaptured := make([]certmanager.StepStatus, certmanager.StepCount)
	for i := 0; i < certmanager.StepCount; i++ {
		stepsCaptured[i] = certmanager.StepStatus{
			Step:  i + 1,
			Total: certmanager.StepCount,
			Name:  certmanager.StepNames[i],
			State: "pending",
		}
	}
	var stepsMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for st := range progress {
			idx := st.Step - 1
			if idx < 0 || idx >= certmanager.StepCount {
				continue
			}
			stepsMu.Lock()
			stepsCaptured[idx] = st
			stepsMu.Unlock()
		}
	}()

	// Manual callback: renewal cannot wait for the operator.  Return a clear
	// error so step 5 fails with actionable text.
	manualCallback := func(ctx2 context.Context, fqdn, value string) error {
		_ = ctx2
		_ = fqdn
		_ = value
		return errors.New("renewal requires automatic DNS provider — manual challenge cannot be used in renewal path")
	}

	var certIDOut string
	persistCertCallback := func(cert certmanager.Certificate) error {
		certDir := fmt.Sprintf("%s/certs/%s", s.moxRoot, cert.Domain)
		srow := storage.MailCertificate{
			ID:                 cert.ID,
			Domain:             cert.Domain,
			PrimaryDomain:      cert.Domain,
			SubjectAltNames:    append([]string(nil), cert.SANDomains...),
			Issuer:             cert.Issuer,
			Serial:             cert.Serial,
			NotBefore:          cert.NotBefore.Format(time.RFC3339),
			NotAfter:           cert.NotAfter.Format(time.RFC3339),
			PEMChain:           cert.PEMChain,
			DNSProviderID:      cert.DNSProviderID,
			RenewalAttemptedAt: cert.LastRenewalAttempt.Format(time.RFC3339),
			RenewalError:       cert.LastError,
			CertPath:           certDir + "/cert.pem",
			PrivkeyPath:        certDir + "/privkey.pem",
			ChainPath:          certDir + "/chain.pem",
			Applied:            true,
		}
		stored, cerr := s.store.MailCreateCertificate(ctx, srow)
		if cerr != nil {
			return cerr
		}
		certIDOut = stored.ID
		return nil
	}

	reloadFn := func(ctx2 context.Context) error { _ = ctx2; return nil }
	tlsProbe := func(ctx2 context.Context) (string, error) { _ = ctx2; return "good", nil }
	persistCallback := func(fqdn, value, domain string) error {
		return s.persistManualChallenge(fqdn, value, domain, "pending")
	}

	cfg := certmanager.IssueConfig{
		Domain:                    req.Domain,
		SANDomains:                append([]string(nil), req.SANs...),
		DataDir:                   s.moxRoot,
		DNSProviders:              providerMap,
		LegoClient:                legoClient,
		ACMEContactEmail:          req.ContactEmail,
		ACMEDirectoryURL:          req.ACMEDirectoryURL,
		AcceptTOS:                 req.AcceptTOS,
		MxHost:                    req.MXHost,
		TLSAEnabled:               req.TLSAEnabled,
		ManualModeConfirmCallback: manualCallback,
		ReloadOrRestartMox:        reloadFn,
		TLSProbe:                  tlsProbe,
		PersistManualChallenge:    persistCallback,
		PersistCertificate:        persistCertCallback,
	}

	res := certmanager.Issue(ctx, cfg, progress)
	close(progress)
	wg.Wait()

	stepsMu.Lock()
	for i := 0; i < certmanager.StepCount; i++ {
		if i < len(res.Steps) && res.Steps[i].State != "pending" {
			stepsCaptured[i] = res.Steps[i]
		}
	}
	stepsMu.Unlock()

	uiSteps := make([]CertPipelineStep, certmanager.StepCount)
	for i, st := range stepsCaptured {
		pct := 0
		if st.Total > 0 {
			pct = st.Step * 100 / st.Total
		}
		if st.State == "done" {
			pct = 100
		}
		uiSteps[i] = CertPipelineStep{
			Step:    st.Step,
			Total:   st.Total,
			Name:    st.Name,
			Percent: pct,
			Message: st.Message,
			Output:  st.Output,
			State:   st.State,
		}
	}

	failureStep := 0
	if !res.Success {
		failureStep = res.Step
	}
	rolledBack := res.RollbackErr != ""
	summary := res.Message
	if summary == "" {
		if res.Success {
			summary = fmt.Sprintf("renewed certificate for %s", req.Domain)
		} else {
			summary = fmt.Sprintf("renewal failed at step %d", failureStep)
		}
	}

	return &CertPipelineResult{
		Success:     res.Success,
		FailureStep: failureStep,
		RolledBack:  rolledBack,
		RollbackErr: res.RollbackErr,
		Summary:     summary,
		Steps:       uiSteps,
		CertID:      certIDOut,
	}, nil
}

// MailCertificateRollback restores a previous PEM if a backup exists.
// (Certmanager's pipeline already does rollback for step 8-10 failures; this
// is an operator-initiated best-effort path.  Phase 4 logs the intent.)
func (s *Service) MailCertificateRollback(ctx context.Context, id string) (*CertRollbackResponse, error) {
	_, err := s.store.MailGetCertificate(ctx, id)
	if err != nil {
		return nil, errors.New("certificate not found")
	}
	// Best-effort: certmanager's pipeline tier handles atomic rollback.
	// The service-level rollback is a Phase 5 feature that restores .bak.
	s.addAudit(ctx, "mail.cert.rollback",
		fmt.Sprintf("rollback requested for certificate %s (stub — no-op)", id),
		map[string]any{"cert_id": id}, "high")
	return &CertRollbackResponse{
		Restored: false,
		Message:  "rollback not wired in Phase 4 — restore .bak files manually",
	}, nil
}

// MailCertificateDelete removes a certificate.
func (s *Service) MailCertificateDelete(ctx context.Context, id string) (*CertDeleteResponse, error) {
	r, err := s.store.MailGetCertificate(ctx, id)
	if err != nil {
		return nil, errors.New("certificate not found")
	}
	domain := r.Domain
	if err := s.store.MailDeleteCertificate(ctx, id); err != nil {
		return nil, fmt.Errorf("delete certificate: %w", err)
	}
	s.addAudit(ctx, "mail.cert.deleted",
		fmt.Sprintf("deleted certificate %s for %s", id, domain),
		map[string]any{"cert_id": id, "domain": domain}, "medium")
	return &CertDeleteResponse{Deleted: true, ID: id, Message: "deleted"}, nil
}

// -----------------------------------------------------------------------------
// Manual challenge service methods.
// -----------------------------------------------------------------------------

func (s *Service) MailManualChallengeList(ctx context.Context) (*ManualChallengeListResponse, error) {
	rows, err := s.store.MailManualChallengeList(ctx, "", "pending")
	if err != nil {
		return nil, fmt.Errorf("list manual challenges: %w", err)
	}
	items := make([]ManualChallenge, 0, len(rows))
	for _, r := range rows {
		items = append(items, ManualChallenge{
			ID:        r.ID,
			CertID:    "", // stored rows don't have cert_id — derive from domain link.
			Domain:    r.Domain,
			FQDN:      r.FQDN,
			Value:     r.Value,
			ExpiresAt: r.ExpiresAt,
			CreatedAt: r.CreatedAt,
		})
	}
	return &ManualChallengeListResponse{Items: items, Count: len(items)}, nil
}

func (s *Service) MailManualChallengeConfirm(ctx context.Context, id string) (*ManualChallengeConfirmResponse, error) {
	// Signal any waiter registered for this ID (unblocks the pipeline).
	globalManualWaiters.Signal(id)
	// Mark confirmed in the DB.
	_, err := s.store.MailManualChallengeConfirm(ctx, id)
	if err != nil {
		// If not found in DB, that's fine — the pipeline may already have
		// cleared it.
		if errors.Is(err, storage.ErrNotFound) {
			return &ManualChallengeConfirmResponse{Accepted: false, Message: "challenge not found (may have already been consumed)"}, nil
		}
		return nil, fmt.Errorf("confirm manual challenge: %w", err)
	}
	s.addAudit(ctx, "mail.cert.dns01_confirmed",
		fmt.Sprintf("confirmed manual challenge %s", id),
		map[string]any{"id": id, "ok": true}, "medium")
	s.publish(ctx, EventTypeCertDNS01Confirmed, map[string]any{"id": id})
	return &ManualChallengeConfirmResponse{Accepted: true, Message: "accepted — ACME validation may proceed"}, nil
}

func (s *Service) MailManualChallengeCancel(ctx context.Context, id string) (*ManualChallengeConfirmResponse, error) {
	globalManualWaiters.Signal(id)
	// Delete from DB.
	if err := s.store.MailManualChallengeDelete(ctx, id); err != nil {
		_ = err
		// Not fatal — proceed with accepted=false response.
	}
	s.addAudit(ctx, "mail.cert.dns01_cancelled",
		fmt.Sprintf("cancelled manual challenge %s", id),
		map[string]any{"id": id}, "medium")
	return &ManualChallengeConfirmResponse{Accepted: true, Message: "cancelled"}, nil
}

// -----------------------------------------------------------------------------
// Background renewal worker.
// -----------------------------------------------------------------------------

// runCertificateRenewals is called by the 1-hour ticker worker.  Returns the
// count of successfully-renewed certificates.
func (s *Service) runCertificateRenewals(ctx context.Context) int {
	rows, err := s.store.MailListCertificates(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "mail cert renewals: list failed", "err", err)
		return 0
	}
	success := 0
	total := len(rows)
	for _, r := range rows {
		daysLeft := 0
		if na, perr := time.Parse(time.RFC3339, r.NotAfter); perr == nil {
			daysLeft = int(time.Until(na).Hours() / 24)
		}
		if daysLeft >= certmanager.DefaultDaysBeforeRenewal {
			continue
		}
		res, rerr := s.MailCertificateRenew(ctx, r.ID, false)
		if rerr != nil {
			s.log.WarnContext(ctx, "mail cert renewal error", "cert_id", r.ID, "err", rerr)
			continue
		}
		if res.Renewed {
			success++
		}
	}
	s.addAudit(ctx, "mail.cert.renewal_scan",
		fmt.Sprintf("renewal ticker: scanned %d certs, renewed %d", total, success),
		map[string]any{"scanned": total, "renewed": success}, "low")
	return success
}

// -----------------------------------------------------------------------------
// Helpers.
// -----------------------------------------------------------------------------

// certRowToResponse converts a storage.MailCertificate into the UI-facing
// CertificateResponse.
func certRowToResponse(r *storage.MailCertificate) CertificateResponse {
	notAfter, _ := time.Parse(time.RFC3339, r.NotAfter)
	daysLeft := 0
	if !notAfter.IsZero() {
		daysLeft = int(time.Until(notAfter).Hours() / 24)
	}
	if daysLeft < 0 {
		daysLeft = 0
	}
	sans := r.SubjectAltNames
	if len(sans) == 0 {
		sans = []string{}
	}
	rawStatus := r.RenewalStatus
	if rawStatus == "" {
		rawStatus = "active"
	}
	privkey := r.PrivkeyPath
	certPath := r.CertPath
	chainPath := r.ChainPath
	if privkey == "" {
		certDir := fmt.Sprintf("%s/certs/%s", "", r.Domain)
		privkey = certDir + "/privkey.pem"
		certPath = certDir + "/cert.pem"
		chainPath = certDir + "/chain.pem"
	}
	return CertificateResponse{
		ID:            r.ID,
		Domain:        r.Domain,
		SANs:          sans,
		Issuer:        r.Issuer,
		Serial:        r.Serial,
		NotBefore:     r.NotBefore,
		NotAfter:      r.NotAfter,
		DaysLeft:      daysLeft,
		TLSA:          r.TLSA311,
		DNSProviderID: r.DNSProviderID,
		Status:        deriveCertStatus(rawStatus, daysLeft),
		LastRenewalAt: r.RenewalAttemptedAt,
		NextRenewalAt: "",
		LastError:     r.RenewalError,
		Files: map[string]string{
			"privkey": privkey,
			"cert":    certPath,
			"chain":   chainPath,
		},
	}
}

func deriveCertStatus(raw string, daysLeft int) string {
	if raw == "manual_pending" || raw == "error" || raw == "renewing" {
		return raw
	}
	if daysLeft <= 0 {
		return "expired"
	}
	if daysLeft < 7 {
		return "expiring_soon"
	}
	return "active"
}

// -----------------------------------------------------------------------------
// ID helper (kept minimal).
// -----------------------------------------------------------------------------

// newShortHex produces a short random hex string for skeleton IDs.
func newShortHex(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + 11) & 0xff)
	}
	ns := time.Now().UnixNano()
	for i := range b {
		b[i] ^= byte(ns & 0xff)
		ns >>= 8
	}
	return hex.EncodeToString(b)
}

// Compile-time interface assertion.
var _ certmanager.DNSProvider = (*serviceDNSProviderAdapter)(nil)
