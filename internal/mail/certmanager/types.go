// Package certmanager implements the Phase 4 ACME DNS-01 certificate
// issuance and renewal pipeline for the Phantom Lancer Mail control plane.
//
// Design boundaries (hard constraints):
//
//   - DNS-01 ONLY.  HTTP-01 and TLS-ALPN-01 are explicitly disabled and
//     no code path ever enables them.
//   - Certificates are written directly by Phantom using a 4-step atomic
//     write (temp same-dir → chmod → Sync → Rename).  Shelling out to
//     cp/mv/chmod is never used.
//   - Ports 80/443 are never touched.  Mox listeners remain loopback-only.
//   - No nginx/caddy/load-balancer config is written.
//   - No automatic binary downloads or updates.
//   - Secrets (DNS tokens, ACME account keys) are never written to slog,
//     audit events, or the DB in plaintext (caller encrypts via
//     storage.wrapMailSecret before persistence).
//   - Zero coupling to internal/supervisor.
//   - Zero coupling to internal/mail/configapply (atomic write is copied
//     locally to atomic.go).
package certmanager

import (
	"context"
	"time"
)

// StepCount is the total number of ACME pipeline steps.
const StepCount = 11

// StepNames enumerates the 11 pipeline steps in order.  Step numbers in
// progress updates are 1-based (1..StepCount); index into this array with
// step-1.
var StepNames = [StepCount]string{
	0:  "ValidateInputs",
	1:  "SelectDNSProvider",
	2:  "ResolveOrCreateAccount",
	3:  "GenerateCSR+SAN",
	4:  "PresentDNSChallenge",
	5:  "ManualModeWaitOrProbePropagation",
	6:  "ACMEObtain",
	7:  "CleanupTXT",
	8:  "AtomicWriteCerts",
	9:  "MoxReload+L4L5Probe",
	10: "PersistDB+RenewalSchedule",
}

// StepStatus is a single progress event emitted by the Issue pipeline.
// Total is always StepCount; Step is 1-based.
type StepStatus struct {
	Step    int    `json:"step"`
	Total   int    `json:"total"`
	Name    string `json:"name"`
	State   string `json:"state"` // "running" | "done" | "failed" | "rollback"
	Message string `json:"message"`
	Output  string `json:"output,omitempty"`
}

// DNSProvider abstracts a DNS hosting provider capable of creating and
// deleting TXT records.  All implementations MUST honour ctx cancellation.
// A stub layer (providers.go) is shipped in Phase 4; a later PR will
// vendor github.com/go-acme/lego/v4/providers/dns/* adapters against the
// same interface without changing any call sites.
type DNSProvider interface {
	// ProviderID returns the stable DB id (e.g. "dnsp-xxx").
	ProviderID() string
	// DisplayName returns the human label shown in the UI.
	DisplayName() string
	// TestConnection performs a no-op round-trip (e.g. list zones) to
	// verify credentials.  Must return nil for the ManualDNSProvider.
	TestConnection(ctx context.Context) error
	// SetTXT creates or updates a TXT record at fqdn with value.  If a
	// TXT set already exists, the value is appended (never replaces).
	SetTXT(ctx context.Context, fqdn, value string) error
	// RemoveTXT removes the specific TXT record value from fqdn.
	RemoveTXT(ctx context.Context, fqdn, value string) error
}

// ManualModeHandler is the callback used when a domain has no real DNS
// token and must wait for the operator to create the challenge TXT by
// hand.  Implementations typically block on a UI "Confirm" action and
// return ctx.Canceled if the user abandons or the request times out.
type ManualModeHandler interface {
	WaitForConfirm(ctx context.Context, fqdn, value string) error
}

// ManualModeHandlerFunc is a convenience adapter so callers can pass a
// plain function instead of implementing the interface.
type ManualModeHandlerFunc func(ctx context.Context, fqdn, value string) error

// WaitForConfirm implements ManualModeHandler.
func (f ManualModeHandlerFunc) WaitForConfirm(ctx context.Context, fqdn, value string) error {
	return f(ctx, fqdn, value)
}

// Certificate mirrors the mail_certificates schema written by the
// PersistCertificate callback.  Only fields the pipeline owns are listed;
// the caller maps them to the full schema when persisting.
type Certificate struct {
	ID                 string    `json:"id"`
	Domain             string    `json:"domain"`
	SANDomains         []string  `json:"san_domains,omitempty"`
	Issuer             string    `json:"issuer"`
	Serial             string    `json:"serial"`
	NotBefore          time.Time `json:"not_before"`
	NotAfter           time.Time `json:"not_after"`
	PEMChain           string    `json:"pem_chain"`
	PEMCertificate     string    `json:"pem_certificate"`
	PEMPrivateKey      string    `json:"-"` // never serialised
	DNSProviderID      string    `json:"dns_provider_id"`
	LastRenewalAttempt time.Time `json:"last_renewal_attempt"`
	NextRenewal        time.Time `json:"next_renewal"`
	LastError          string    `json:"last_error"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TLSAInfo describes a TLSA record (RFC 6698) for DANE.  Defaults are
// Usage=3 (domain-issued cert), Selector=1 (SPKI), MatchingType=1
// (SHA-256) — a.k.a. "3 1 1", the recommended form for self-hosted MX.
type TLSAInfo struct {
	Port         int    `json:"port"`
	Usage        int    `json:"usage"`        // default 3
	Selector     int    `json:"selector"`     // default 1
	MatchingType int    `json:"matching_type"` // default 1
	HexDigest    string `json:"hex_digest"`
	FQDN         string `json:"fqdn"` // form: _<port>._tcp.<mx-host>.
}

// IssueResult is the terminal outcome of an Issue() invocation.  When
// Success=false and RollbackErr != "", the pipeline detected a post-write
// probe failure AND could not restore the previous certificate state —
// operators must treat this as a P1 incident.
type IssueResult struct {
	Success     bool        `json:"success"`
	CertPath    string      `json:"cert_path"`
	KeyPath     string      `json:"key_path"`
	ChainPath   string      `json:"chain_path"`
	NotBefore   time.Time   `json:"not_before"`
	NotAfter    time.Time   `json:"not_after"`
	TLSA        *TLSAInfo   `json:"tlsa,omitempty"`
	RollbackErr string      `json:"rollback_err,omitempty"`
	Message     string      `json:"message"`
	Step        int         `json:"step"` // last completed step (1-based)
	Steps       []StepStatus `json:"steps"`
}
