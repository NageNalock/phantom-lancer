package certmanager

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)


// writtenFile records a single cert file written by the pipeline so the
// rollback path knows where the live and .bak copies live.
type writtenFile struct {
	path string
	bak  string
	perm os.FileMode
}

// IssueConfig is the full input payload for the 11-step ACME DNS-01
// pipeline.  All optional callbacks default to conservative no-ops that
// either succeed trivially or return descriptive errors — callers are
// responsible for wiring the real ones at service boot.
type IssueConfig struct {
	// Domain is the primary CN.
	Domain string
	// SANDomains are extra names placed in the Subject Alternative Names
	// extension.  The primary Domain is always included in the CSR and
	// MUST NOT be duplicated here.
	SANDomains []string

	// DataDir is the Mox data directory.  Certificates land under
	// <DataDir>/certs/<Domain>/ with names privkey.pem, cert.pem,
	// chain.pem.
	DataDir string

	// DNSProviders maps zone suffix → DNSProvider.  The pipeline picks
	// the longest-matching suffix for each challenged domain.
	DNSProviders map[string]DNSProvider

	// LegoClient is the ACME client.  Callers may construct it via
	// NewLegoClient (which returns a stub in Phase 4) or inject a real
	// lego-wrapped implementation when that dependency is vendored.
	LegoClient LegoClient

	// ACMEContactEmail is the Let's Encrypt account contact.  Required.
	ACMEContactEmail string
	// ACMEDirectoryURL selects staging vs production.  Empty = Let's
	// Encrypt staging (safe for Phase 4 skeleton development).
	ACMEDirectoryURL string
	// AcceptTOS is a signed acknowledgement that the operator has read
	// and accepted the ACME terms of service.  MUST be true.
	AcceptTOS bool

	// MxHost is the MX hostname used for TLSA record construction.
	// Empty defaults to "mail.<primary domain>".
	MxHost string
	// TLSAEnabled controls whether we compute a 3 1 1 TLSA record and
	// populate IssueResult.TLSA.  When false, that field is nil.
	TLSAEnabled bool

	// ManualModeConfirmCallback is nil when tokens are configured.
	// When non-nil it is invoked once per pending manual challenge
	// (sequentially, not concurrently) in step 5.  Returning
	// ctx.Canceled aborts the pipeline.
	ManualModeConfirmCallback func(ctx context.Context, fqdn, value string) error

	// ReloadOrRestartMox is called after certificates are written
	// (step 9).  Returning an error triggers the step-9+ rollback path.
	ReloadOrRestartMox func(ctx context.Context) error
	// TLSProbe returns one of {"good", "warn", "critical", "error",
	// "unknown"}.  Anything other than "good" or "warn" triggers the
	// step-9 rollback.
	TLSProbe func(ctx context.Context) (overallState string, err error)

	// PersistManualChallenge is invoked by the ManualDNSProvider stub
	// when a zone has no token.  Signature:
	//   PersistManualChallenge(fqdn, value, domain string) error
	// (declared inline so callers don't have to import a separate type).
	PersistManualChallenge func(fqdn, value, domain string) error

	// PersistCertificate saves the issued Certificate struct to SQLite
	// at step 11.
	PersistCertificate func(cert Certificate) error

	// DeleteCertArtifacts is an optional last-resort cleanup used when
	// the mid-pipeline rollback fails and the caller wants the 3 PEM
	// files wiped.  May be nil — when nil, the rollback path simply
	// leaves the current (broken) files in place and returns
	// IssueResult.RollbackErr so an operator can inspect them.
	DeleteCertArtifacts func() error
}

// Issue runs the 11-step ACME DNS-01 pipeline end-to-end.  Progress is
// streamed on the `progress` channel (each StepStatus is sent once with
// state="running" then again with "done" or "failed" or "rollback").
// Callers MUST drain the channel or the pipeline blocks.  Pass nil if
// you don't want progress streaming (the helpers are no-op safe).
//
// The return IssueResult always carries a populated Steps slice of
// exactly StepCount entries so the UI can render the pipeline state
// deterministically.
func Issue(ctx context.Context, cfg IssueConfig, progress chan<- StepStatus) IssueResult {
	res := IssueResult{
		Steps: make([]StepStatus, StepCount),
	}
	// Pre-populate every step.
	for i := 0; i < StepCount; i++ {
		res.Steps[i] = StepStatus{
			Step:  i + 1,
			Total: StepCount,
			Name:  StepNames[i],
			State: "pending",
		}
	}

	// ---------- rollback state (populated by step 8) ----------
	var written []writtenFile
	// challenges accumulates pending TXT records so the step-5-7
	// failure tier can remove them on abort.
	type challenge struct {
		provider DNSProvider
		fqdn     string
		value    string
	}
	var challenges []challenge

	// ---------- helpers ----------
	updateStep := func(idx int, st StepStatus) {
		if idx < 0 || idx >= StepCount {
			return
		}
		res.Steps[idx] = st
		if progress != nil {
			select {
			case progress <- st:
			default:
				// Non-blocking send — if the caller isn't draining
				// we still want forward progress.
			}
		}
	}
	emit := func(idx int, state, msg, output string) {
		st := StepStatus{
			Step:    idx + 1,
			Total:   StepCount,
			Name:    StepNames[idx],
			State:   state,
			Message: msg,
			Output:  output,
		}
		updateStep(idx, st)
	}
	fail := func(idx int, msg, output string) IssueResult {
		emit(idx, "failed", msg, output)
		res.Message = fmt.Sprintf("%s: %s", StepNames[idx], msg)
		res.Step = idx + 1

		// Apply failure/rollback tiers per spec.
		switch {
		case idx <= 3: // step <= 4 (1-based)
			// Keep temp state for inspection; no cleanup.

		case idx >= 4 && idx <= 6: // steps 5-7: cleanup TXT, leave temp files
			for _, ch := range challenges {
				if ch.provider != nil {
					_ = ch.provider.RemoveTXT(ctx, ch.fqdn, ch.value)
				}
			}

		case idx >= 7 && idx <= 9: // steps 8-10: ROLLBACK
			rberr := doRollback(ctx, cfg, written, &res)
			if rberr != "" {
				res.RollbackErr = rberr
			}
		}
		res.Success = false
		return res
	}

	// =====================================================================
	// Step 0: ValidateInputs
	// =====================================================================
	emit(0, "running", "validating inputs", "")
	if !cfg.AcceptTOS {
		return fail(0, "AcceptTOS must be true (operator must acknowledge ACME terms of service)", "")
	}
	if strings.TrimSpace(cfg.Domain) == "" {
		return fail(0, "primary Domain is empty", "")
	}
	if cfg.ACMEContactEmail == "" {
		return fail(0, "ACMEContactEmail is empty", "")
	}
	if cfg.DataDir == "" {
		return fail(0, "DataDir (Mox data directory) is empty", "")
	}
	for _, san := range cfg.SANDomains {
		if !isValidFQDN(san) {
			return fail(0, fmt.Sprintf("SAN %q is not a valid FQDN", san), "")
		}
	}
	emit(0, "done", fmt.Sprintf("validated CN=%s SANs=%d", cfg.Domain, len(cfg.SANDomains)), "")

	// =====================================================================
	// Step 1: SelectDNSProvider
	// =====================================================================
	emit(1, "running", "selecting DNS providers per zone", "")
	allDomains := append([]string{cfg.Domain}, cfg.SANDomains...)
	// providerFor maps each domain → its selected provider.
	providerFor := make(map[string]DNSProvider, len(allDomains))
	var anyManual bool
	for _, d := range allDomains {
		prov := matchDNSProvider(d, cfg.DNSProviders)
		if prov == nil {
			// Fallback to ManualDNSProvider — if a Persist callback is
			// configured, hand it to the provider.
			mp := &ManualDNSProvider{}
			if cfg.PersistManualChallenge != nil {
				mp.Persist = func(fqdn, value, domain, status string) error {
					return cfg.PersistManualChallenge(fqdn, value, domain)
				}
			}
			prov = mp
		}
		if _, ok := prov.(*ManualDNSProvider); ok {
			anyManual = true
		}
		providerFor[d] = prov
	}
	emit(1, "done", fmt.Sprintf("resolved %d providers for %d domains (manual_mode=%v)",
		len(providerFor), len(allDomains), anyManual), "")

	// =====================================================================
	// Step 2: ResolveOrCreateAccount
	// =====================================================================
	emit(2, "running", "resolving or creating ACME account", "")
	lego := cfg.LegoClient
	if lego == nil {
		// Lazily construct a client — callers typically pre-populate
		// this but we fall back to the stub for convenience.
		var lerr error
		lego, lerr = NewLegoClient(cfg.DataDir, cfg.ACMEContactEmail, cfg.ACMEDirectoryURL, cfg.AcceptTOS)
		if lerr != nil {
			return fail(2, fmt.Sprintf("construct lego client: %v", lerr), "")
		}
	}
	acmeURL := cfg.ACMEDirectoryURL
	if acmeURL == "" {
		acmeURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	emit(2, "done", fmt.Sprintf("account ready — directory=%s contact=%s", acmeURL, cfg.ACMEContactEmail), "")

	// =====================================================================
	// Step 3: GenerateCSR+SAN
	// =====================================================================
	emit(3, "running", "generating CSR with CN+SANs", "")
	csrPEM := GenerateCSRStub(allDomains)
	emit(3, "done", fmt.Sprintf("CSR generated — CN=%s SANs=%d len=%d",
		cfg.Domain, len(cfg.SANDomains), len(csrPEM)), string(csrPEM))

	// =====================================================================
	// Step 4: PresentDNSChallenge
	// =====================================================================
	emit(4, "running", "presenting DNS-01 challenges", "")
	// Build the full (fqdn, keyauth) list that step 6 will feed to lego.
	type pendingChallenge struct {
		domain  string
		fqdn    string
		keyauth string
		prov    DNSProvider
	}
	pending := make([]pendingChallenge, 0, len(allDomains))
	for _, d := range allDomains {
		prov := providerFor[d]
		fqdn := "_acme-challenge." + d + "."
		keyauth := "lego-stub-" + d
		if err := prov.SetTXT(ctx, fqdn, keyauth); err != nil {
			// We're inside step 4's fail tier (<=4) — cleanup is up to
			// the fail() helper per tier rules, but we should still
			// record anything that was successfully presented so far.
			for _, p := range pending {
				challenges = append(challenges, challenge{p.prov, p.fqdn, p.keyauth})
			}
			return fail(4, fmt.Sprintf("SetTXT failed for %s: %v", d, err), "")
		}
		pending = append(pending, pendingChallenge{d, fqdn, keyauth, prov})
		challenges = append(challenges, challenge{prov, fqdn, keyauth})
	}
	emit(4, "done", fmt.Sprintf("presented %d TXT challenges", len(pending)), "")

	// =====================================================================
	// Step 5: ManualModeWaitOrProbePropagation
	// =====================================================================
	emit(5, "running", "manual-mode wait or propagation probe", "")
	if anyManual && cfg.ManualModeConfirmCallback != nil {
		for _, ch := range pending {
			if _, isManual := ch.prov.(*ManualDNSProvider); !isManual {
				continue
			}
			if err := cfg.ManualModeConfirmCallback(ctx, ch.fqdn, ch.keyauth); err != nil {
				if errors.Is(err, context.Canceled) {
					return fail(5, fmt.Sprintf("manual confirm canceled for %s", ch.domain), "")
				}
				return fail(5, fmt.Sprintf("manual confirm failed for %s: %v", ch.domain, err), "")
			}
		}
		emit(5, "done", "all manual challenges confirmed by operator", "")
	} else if anyManual {
		// Manual providers present but no confirm callback — this is a
		// misconfiguration.  Surface it as step-5 failure so the UI can
		// route the operator to the "Confirm challenges" panel.
		return fail(5, "manual DNS mode requires a ManualModeConfirmCallback", "")
	} else {
		// Token-based providers — best-effort propagation wait.
		// We sleep for a short fixed interval to let TXT records
		// propagate through authoritative servers.  A later PR can
		// replace this with real dig-like probing.
		select {
		case <-time.After(2 * time.Second):
			emit(5, "done", "propagation hold (2s) complete", "")
		case <-ctx.Done():
			return fail(5, fmt.Sprintf("propagation hold canceled: %v", ctx.Err()), "")
		}
	}

	// =====================================================================
	// Step 6: ACMEObtain
	// =====================================================================
	emit(6, "running", "requesting certificate from ACME directory", "")
	var pemKey, pemCert, pemChain []byte
	var obtainErr error
	cb := func(presentationFQDN, keyAuth string) (cleanup func() error, err error) {
		// The stub invokes this callback with presentationFQDN =
		// "_acme-challenge.<domain>." and keyAuth = "lego-stub-<domain>".
		// The real lego library does the same but with real key-auths.
		//
		// We've ALREADY presented in step 4, so here we just return a
		// no-op present + a proper RemoveTXT cleanup.  In a real
		// integration we'd present here (lego manages the lifecycle)
		// but for the 11-step spec we split it for human readability.
		domain := extractDomainFromFQDN(presentationFQDN)
		prov := providerFor[domain]
		if prov == nil {
			return nil, fmt.Errorf("acme: no DNS provider for domain %s (fqdn=%s)", domain, presentationFQDN)
		}
		cleanup = func() error {
			return prov.RemoveTXT(context.Background(), presentationFQDN, keyAuth)
		}
		return cleanup, nil
	}
	pemKey, pemCert, pemChain, obtainErr = lego.ObtainCertificate(ctx, allDomains, cb)
	if obtainErr != nil {
		return fail(6, fmt.Sprintf("ACME obtain failed: %v", obtainErr), "")
	}
	if len(pemKey) == 0 || len(pemCert) == 0 || len(pemChain) == 0 {
		return fail(6, "ACME obtain returned empty PEM artifact(s)", "")
	}
	emit(6, "done", fmt.Sprintf("certificate obtained — len(key)=%d len(cert)=%d len(chain)=%d",
		len(pemKey), len(pemCert), len(pemChain)), "")

	// =====================================================================
	// Step 7: CleanupTXT
	// =====================================================================
	emit(7, "running", "removing DNS-01 TXT records", "")
	cleanupErrors := []string{}
	for _, ch := range challenges {
		if err := ch.provider.RemoveTXT(ctx, ch.fqdn, ch.value); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("%s: %v", ch.fqdn, err))
		}
	}
	if len(cleanupErrors) > 0 {
		// Non-fatal: warn but continue.  A dangling _acme-challenge TXT
		// record is cosmetic.
		emit(7, "done", fmt.Sprintf("TXT cleanup complete (%d warnings: %s)",
			len(cleanupErrors), strings.Join(cleanupErrors, "; ")), "")
	} else {
		emit(7, "done", fmt.Sprintf("removed %d TXT records cleanly", len(challenges)), "")
	}
	challenges = nil // nothing more to clean in subsequent tiers

	// =====================================================================
	// Step 8: AtomicWriteCerts
	// =====================================================================
	emit(8, "running", "writing PEM artifacts atomically", "")
	certDir := filepath.Join(cfg.DataDir, "certs", cfg.Domain)
	keyPath := filepath.Join(certDir, "privkey.pem")
	certPath := filepath.Join(certDir, "cert.pem")
	chainPath := filepath.Join(certDir, "chain.pem")

	// Create .bak copies before overwriting so rollback can restore them.
	for _, p := range []string{keyPath, certPath, chainPath} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			bak := p + ".bak"
			if cperr := CopyAtomic(p, bak); cperr != nil {
				// Best-effort — if we can't back up and the write
				// fails, rollback will return RollbackErr.
				_ = cperr
			}
		}
	}

	// Order: privkey (0600) → chain (0644) → cert (0644).  Write the
	// private key FIRST so a partial-write crash leaves the chain and
	// cert in place (they are public material and safe to leave
	// dangling; the private key is what must always be 0600).
	if err := WriteAtomic0600(keyPath, pemKey); err != nil {
		return fail(8, fmt.Sprintf("atomic write privkey: %v", err), "")
	}
	written = append(written, writtenFile{keyPath, keyPath + ".bak", 0o600})

	if err := WriteAtomic0644(chainPath, pemChain); err != nil {
		return fail(8, fmt.Sprintf("atomic write chain: %v", err), "")
	}
	written = append(written, writtenFile{chainPath, chainPath + ".bak", 0o644})

	if err := WriteAtomic0644(certPath, pemCert); err != nil {
		return fail(8, fmt.Sprintf("atomic write cert: %v", err), "")
	}
	written = append(written, writtenFile{certPath, certPath + ".bak", 0o644})

	// TLSA computation (pure, no side effects beyond the result).
	var tlsa *TLSAInfo
	if cfg.TLSAEnabled {
		mxHost := cfg.MxHost
		if mxHost == "" {
			mxHost = "mail." + cfg.Domain
		}
		// The stub's pemCert is NOT real X.509 DER, so ComputeTLSA311
		// will fail for stubs.  Don't treat that as a pipeline failure
		// — just leave TLSA nil.  Callers can check TLSA == nil to
		// detect "stub certificate, compute again after real lego".
		if t, terr := BuildTLSA(mxHost, 25, pemCert); terr == nil {
			tlsa = t
		}
	}
	tlsaMsg := "off"
	if tlsa != nil {
		tlsaMsg = fmt.Sprintf("TLSA 3 1 1 %s %s", tlsa.FQDN, tlsa.HexDigest[:16]+"…")
	}
	emit(8, "done", fmt.Sprintf("wrote 3 PEM files to %s — %s", certDir, tlsaMsg), "")

	// =====================================================================
	// Step 9: MoxReload+L4L5Probe
	// =====================================================================
	emit(9, "running", "reloading mox and probing L4/L5", "")
	if cfg.ReloadOrRestartMox == nil {
		return fail(9, "ReloadOrRestartMox callback is nil — cannot apply new certificates", "")
	}
	if err := cfg.ReloadOrRestartMox(ctx); err != nil {
		return fail(9, fmt.Sprintf("mox reload/restart failed: %v", err), "")
	}
	if cfg.TLSProbe == nil {
		return fail(9, "TLSProbe callback is nil — cannot confirm new certificates are serving", "")
	}
	probeState, perr := cfg.TLSProbe(ctx)
	if perr != nil {
		return fail(9, fmt.Sprintf("TLS probe error: %v", perr), "")
	}
	switch probeState {
	case "good", "warn":
		// Acceptable.  "warn" is intentionally allowed (e.g. mox is
		// serving the new cert but the old one is still cached on one
		// front-end listener — transient).
		emit(9, "done", fmt.Sprintf("mox reloaded, L4/L5 probe state=%s", probeState), "")
	default:
		return fail(9, fmt.Sprintf("post-reload L4/L5 probe state=%s (not good/warn)", probeState), "")
	}

	// =====================================================================
	// Step 10: PersistDB+RenewalSchedule
	// =====================================================================
	emit(10, "running", "persisting certificate metadata + renewal schedule", "")
	// Extract NotBefore / NotAfter from the issued PEM.  For stub certs
	// we fall back to a 90-day window from Now().
	nb, na, issuer, serial := extractCertMetadata(pemCert)
	if na.IsZero() {
		nb = time.Now().UTC()
		na = nb.Add(90 * 24 * time.Hour)
	}
	next := NextRenewal(na, DefaultDaysBeforeRenewal)

	// Build a provider-id digest for the selected provider of the primary
	// domain — this is stored alongside the cert so the renewal runner
	// knows which provider to use when looping back.
	dnsProviderID := ""
	if prov := providerFor[cfg.Domain]; prov != nil {
		dnsProviderID = prov.ProviderID()
	}

	dbCert := Certificate{
		ID:                 fmt.Sprintf("cert-%d-%s", nb.Unix(), shortSanitize(cfg.Domain)),
		Domain:             cfg.Domain,
		SANDomains:         append([]string(nil), cfg.SANDomains...),
		Issuer:             issuer,
		Serial:             serial,
		NotBefore:          nb,
		NotAfter:           na,
		PEMChain:           string(pemChain),
		PEMCertificate:     string(pemCert),
		PEMPrivateKey:      string(pemKey),
		DNSProviderID:      dnsProviderID,
		LastRenewalAttempt: nb,
		NextRenewal:        next,
		LastError:          "",
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	if cfg.PersistCertificate != nil {
		if err := cfg.PersistCertificate(dbCert); err != nil {
			return fail(10, fmt.Sprintf("persist certificate to DB: %v", err), "")
		}
	}
	emit(10, "done", fmt.Sprintf("persisted cert %s (expires %s; next renewal %s)",
		dbCert.ID, na.Format(time.RFC3339), next.Format(time.RFC3339)), "")

	// ---------- Final result ----------
	res.Success = true
	res.Step = StepCount
	res.CertPath = certPath
	res.KeyPath = keyPath
	res.ChainPath = chainPath
	res.NotBefore = nb
	res.NotAfter = na
	res.TLSA = tlsa
	res.Message = fmt.Sprintf("certificate issued successfully for %s (expires %s)",
		cfg.Domain, na.Format("2006-01-02"))
	return res
}

// =========================================================================
// rollback
// =========================================================================

// doRollback restores each .bak copy over the live file (atomic rename),
// reloads mox, re-runs the TLS probe, and returns a human-readable
// summary.  When rollback itself fails the error is returned as the
// RollbackErr string so the IssueResult documents that the system is
// in an inconsistent state.
func doRollback(ctx context.Context, cfg IssueConfig, written []writtenFile, res *IssueResult) string {
	// 1. Restore each written file from .bak if a .bak exists.
	for _, wf := range written {
		if info, err := os.Stat(wf.bak); err != nil || info.IsDir() {
			// No backup exists — nothing to restore for this file.
			// If we wrote a partial file, delete it so the previous
			// state (missing file) is visible to operators.
			if info != nil && !info.IsDir() {
				continue
			}
			_ = os.Remove(wf.path)
			continue
		}
		if err := CopyAtomic(wf.bak, wf.path); err != nil {
			return fmt.Sprintf("rollback: restore %s from %s: %v", wf.path, wf.bak, err)
		}
	}
	// 2. Reload mox so it picks up the old certs.
	if cfg.ReloadOrRestartMox != nil {
		if err := cfg.ReloadOrRestartMox(ctx); err != nil {
			return fmt.Sprintf("rollback: reload mox after restore: %v", err)
		}
	}
	// 3. Confirm with the TLS probe.
	if cfg.TLSProbe != nil {
		state, err := cfg.TLSProbe(ctx)
		if err != nil {
			return fmt.Sprintf("rollback: post-restore probe error: %v", err)
		}
		if state != "good" && state != "warn" {
			return fmt.Sprintf("rollback: post-restore probe state=%s (not good/warn)", state)
		}
	}
	// 4. Mark the triggering step as "rollback" in the StepStatus trace so
	// the UI can render the tier correctly.
	for i, st := range res.Steps {
		if st.State == "failed" {
			res.Steps[i].State = "rollback"
			res.Steps[i].Message = st.Message + " [rolled back]"
			break
		}
	}
	return ""
}

// =========================================================================
// selection + validation helpers
// =========================================================================

// matchDNSProvider returns the provider whose zone suffix is the longest
// match for domain.  Callers pass a map of "suffix → provider"; if no
// suffix matches the function returns nil so the caller can fall back
// to ManualDNSProvider.
func matchDNSProvider(domain string, providers map[string]DNSProvider) DNSProvider {
	if providers == nil {
		return nil
	}
	// Normalize: trim leading dots, trim trailing dot.
	d := strings.TrimPrefix(strings.TrimSuffix(domain, "."), ".")
	var best DNSProvider
	var bestLen int
	for suffix, p := range providers {
		s := strings.TrimPrefix(strings.TrimSuffix(suffix, "."), ".")
		if s == "" {
			continue
		}
		if len(s) < bestLen {
			continue
		}
		if d == s || strings.HasSuffix(d, "."+s) {
			best = p
			bestLen = len(s)
		}
	}
	return best
}

// fqdnRe is a conservative validator: labels separated by dots, each
// label 1-63 alphanumeric-plus-hyphen chars, hyphen not at the edges.
// It's deliberately permissive (IDN callers must punycode before the
// pipeline) — the goal is to catch blatant typos, not enforce RFCs.
var fqdnRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// isValidFQDN reports whether d is a syntactically plausible FQDN.
func isValidFQDN(d string) bool {
	d = strings.TrimSpace(strings.TrimSuffix(d, "."))
	if d == "" || len(d) > 253 {
		return false
	}
	return fqdnRe.MatchString(d)
}

// extractDomainFromFQDN strips the "_acme-challenge." prefix and any
// trailing "." from a presentation FQDN to recover the managed domain.
func extractDomainFromFQDN(fqdn string) string {
	d := strings.TrimPrefix(fqdn, "_acme-challenge.")
	d = strings.TrimSuffix(d, ".")
	return d
}

// shortSanitize returns an alphanumeric slug suitable for use in ids.
func shortSanitize(s string) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < 48; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 32)
		case c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '.' || c == '-' || c == '_':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

// extractCertMetadata attempts to parse a real X.509 PEM block and
// recover the validity window + issuer + serial.  For stub PEMs that
// aren't real DER the function returns zero-valued NotBefore/NotAfter
// so the outer pipeline can fall back to "now + 90 days".
func extractCertMetadata(pemCert []byte) (notBefore, notAfter time.Time, issuer, serial string) {
	block, _ := pem.Decode(pemCert)
	if block == nil {
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	notBefore = cert.NotBefore.UTC()
	notAfter = cert.NotAfter.UTC()
	issuer = cert.Issuer.String()
	if cert.SerialNumber != nil {
		serial = cert.SerialNumber.Text(16)
	} else {
		serial = new(big.Int).String()
	}
	return
}
