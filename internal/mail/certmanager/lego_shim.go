package certmanager

import (
	"context"
	"fmt"
	"time"
)

// LegoClient abstracts the subset of github.com/go-acme/lego/v4 that the
// certmanager pipeline actually uses.  Keeping it as an interface means:
//
//   - Phase 4 ships a deterministic stub (LegoStubClient) so the full
//     11-step pipeline can be exercised against a zero-network test path.
//   - A later PR can vendor the real lego client (≈50MB transitive deps
//     including the Cloudflare/Route53 SDKs) with a single file change.
//   - Unit tests for the pipeline inject a fake that returns errors at
//     specific steps to exercise the rollback tiers.
//
// HTTP-01 and TLS-ALPN-01 are deliberately absent from the interface.
// Only DNS-01 is supported.
type LegoClient interface {
	// ObtainCertificate requests a certificate from the ACME directory.
	// The challengeCallback is invoked once per (domain, key-auth) pair;
	// it must present the DNS-01 TXT record and return a cleanup
	// function that removes it.  Callers MUST invoke the cleanup even on
	// error (best-effort) so stray _acme-challenge records don't linger.
	ObtainCertificate(
		ctx context.Context,
		domains []string,
		challengeCallback func(presentationFQDN, keyAuth string) (cleanup func() error, err error),
	) (pemPrivateKey, pemCertificate, pemIssuerChain []byte, err error)

	// RevokeCertificate revokes a previously issued cert.  The ACME
	// server's revocation reason is hard-coded to "unspecified" (0) —
	// certmanager never passes more specific reasons.
	RevokeCertificate(ctx context.Context, pem []byte) error
}

// ---- Stub implementation ----------------------------------------------------

// LegoStubClient returns deterministic PEM placeholders so downstream
// steps (atomic write, TLSA calculation, mox reload, DB persist) can be
// exercised without network access.  The PEM blocks are deliberately
// NOT cryptographically valid — anything that tries to parse them with
// x509.ParseCertificate will fail, which is the correct behaviour
// because the STUB is for pipeline-skeleton testing only.
type LegoStubClient struct {
	// Directory is the ACME directory URL (stub records it for logs).
	Directory string
	// ContactEmail is the ACME account contact (stub records it).
	ContactEmail string
	// AcceptTOS is mirrored from IssueConfig; the stub checks it.
	AcceptTOS bool
	// NotBefore / NotAfter are the returned validity window.  Defaults
	// to a 90-day certificate beginning at the call time when zero.
	NotBefore time.Time
	NotAfter  time.Time
}

// NewLegoClient constructs a LegoClient.  When the caller has NOT
// vendored the real lego library (Phase 4), a LegoStubClient is
// returned.  The signature is deliberately compatible with a future
// swap-in that returns the real client from the same factory.
func NewLegoClient(dir string, email string, acmeURL string, acceptTOS bool) (LegoClient, error) {
	if acmeURL == "" {
		acmeURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	if email == "" {
		return nil, fmt.Errorf("lego: ACME contact email is required (stub: ACME terms require a valid contact)")
	}
	if !acceptTOS {
		return nil, fmt.Errorf("lego: AcceptTOS must be true before ACME account can be created (directory=%s)", acmeURL)
	}
	return &LegoStubClient{
		Directory:    acmeURL,
		ContactEmail: email,
		AcceptTOS:    acceptTOS,
	}, nil
}

// stubPrivateKey is the PEM-encoded private-key placeholder.  The
// content is deliberately static so hashes are reproducible across test
// runs but the string marker clearly identifies it as a stub in audit.
const stubPrivateKey = "-----BEGIN RSA PRIVATE KEY-----\n" +
	"LEGO STUB (caller must vendor lego for real; directory=%DIR% email=%EMAIL%)\n" +
	"Content: static placeholder — NOT a valid RSA key\n" +
	"-----END RSA PRIVATE KEY-----\n"

// stubCertTemplate is the PEM-encoded certificate placeholder.
const stubCertTemplate = "-----BEGIN CERTIFICATE-----\n" +
	"LEGO STUB CERTIFICATE — NOT VALID X.509\n" +
	"Directory: %DIR%\n" +
	"Contact: %EMAIL%\n" +
	"CN: %CN%\n" +
	"SANs: %SANS%\n" +
	"NotBefore: %NB%\n" +
	"NotAfter: %NA%\n" +
	"Serial: %SERIAL%\n" +
	"Issuer: Phantom-Lancer-STUB-CA\n" +
	"-----END CERTIFICATE-----\n"

// stubChainTemplate is the PEM-encoded (self-signed) issuer-chain placeholder.
const stubChainTemplate = "-----BEGIN CERTIFICATE-----\n" +
	"LEGO STUB ISSUER CHAIN — NOT VALID X.509\n" +
	"Directory: %DIR%\n" +
	"Subject: Phantom-Lancer-STUB-CA\n" +
	"-----END CERTIFICATE-----\n"

// ObtainCertificate implements LegoClient for the stub.  It:
//  1. Validates inputs.
//  2. Invokes challengeCallback once per domain so the caller gets a
//     chance to run the present/cleanup cycle end-to-end.
//  3. Returns deterministic PEM placeholders.
func (s *LegoStubClient) ObtainCertificate(
	ctx context.Context,
	domains []string,
	challengeCallback func(presentationFQDN, keyAuth string) (cleanup func() error, err error),
) (pemPrivateKey, pemCertificate, pemIssuerChain []byte, err error) {
	_ = ctx
	if len(domains) == 0 {
		return nil, nil, nil, fmt.Errorf("lego stub: ObtainCertificate: empty domains list")
	}
	if challengeCallback == nil {
		return nil, nil, nil, fmt.Errorf("lego stub: ObtainCertificate: nil challengeCallback (DNS-01 only)")
	}

	// Step 1: present challenges, collect cleanup functions.
	cleanups := make([]func() error, 0, len(domains))
	defer func() {
		// On ANY return path (success or error), run cleanups in LIFO.
		// The real lego library does something very similar internally.
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				_ = cleanups[i]()
			}
		}
	}()
	for _, d := range domains {
		fqdn := "_acme-challenge." + d + "."
		keyAuth := "lego-stub-" + d
		clean, cerr := challengeCallback(fqdn, keyAuth)
		if cerr != nil {
			return nil, nil, nil, fmt.Errorf("lego stub: present DNS-01 for %s: %w", d, cerr)
		}
		if clean != nil {
			cleanups = append(cleanups, clean)
		}
	}

	// Step 2: build placeholder PEMs.
	nb := s.NotBefore
	if nb.IsZero() {
		nb = time.Now().UTC()
	}
	na := s.NotAfter
	if na.IsZero() {
		na = nb.Add(90 * 24 * time.Hour)
	}
	cn := domains[0]
	sans := stringsJoin(domains, ",")
	serial := fmt.Sprintf("stub-%d", nb.Unix())

	cert := stubCertTemplate
	cert = replaceToken(cert, "%DIR%", s.Directory)
	cert = replaceToken(cert, "%EMAIL%", s.ContactEmail)
	cert = replaceToken(cert, "%CN%", cn)
	cert = replaceToken(cert, "%SANS%", sans)
	cert = replaceToken(cert, "%NB%", nb.Format(time.RFC3339))
	cert = replaceToken(cert, "%NA%", na.Format(time.RFC3339))
	cert = replaceToken(cert, "%SERIAL%", serial)

	chain := stubChainTemplate
	chain = replaceToken(chain, "%DIR%", s.Directory)

	priv := stubPrivateKey
	priv = replaceToken(priv, "%DIR%", s.Directory)
	priv = replaceToken(priv, "%EMAIL%", s.ContactEmail)

	// Populate stub fields so the outer pipeline can harvest the validity window.
	s.NotBefore = nb
	s.NotAfter = na

	return []byte(priv), []byte(cert), []byte(chain), nil
}

// RevokeCertificate implements LegoClient.  The stub validates inputs
// and returns nil — a real ACME revoke is an irreversible action that
// tests should never trigger against the staging directory.
func (s *LegoStubClient) RevokeCertificate(ctx context.Context, pem []byte) error {
	_ = ctx
	if len(pem) == 0 {
		return fmt.Errorf("lego stub: RevokeCertificate: empty PEM")
	}
	if !s.AcceptTOS {
		return fmt.Errorf("lego stub: RevokeCertificate: AcceptTOS was false at construction")
	}
	return nil
}

// GenerateCSRStub returns a deterministic "CSR" placeholder.  The real
// pipeline doesn't need a raw CSR (lego generates its own internally
// via crypto/x509 when you call ObtainCertificate with a domains list),
// but the Step 4 "GenerateCSR+SAN" progress event is more satisfying if
// callers can pass something opaque into the progress output.
func GenerateCSRStub(domains []string) []byte {
	cn := ""
	if len(domains) > 0 {
		cn = domains[0]
	}
	sans := stringsJoin(domains, ",")
	return []byte(
		"-----BEGIN CERTIFICATE REQUEST-----\n" +
			"CSR STUB (Phase 4 pipeline skeleton) — NOT VALID PKCS#10\n" +
			"CN: " + cn + "\n" +
			"SANs: " + sans + "\n" +
			"-----END CERTIFICATE REQUEST-----\n",
	)
}

// ---- minimal string helpers (avoids "strings" import bloat; tiny file) ----

func replaceToken(s, tok, val string) string {
	// Use a simple manual replace so we don't pull strings.Replace for
	// two token substitutions.  Real code is welcome to use the stdlib;
	// this is a micro-optimisation for a stub file.
	out := make([]byte, 0, len(s)+len(val))
	i := 0
	for i < len(s) {
		if i+len(tok) <= len(s) && s[i:i+len(tok)] == tok {
			out = append(out, val...)
			i += len(tok)
			continue
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

func stringsJoin(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	n := 0
	for i, s := range items {
		n += len(s)
		if i > 0 {
			n += len(sep)
		}
	}
	buf := make([]byte, 0, n)
	for i, s := range items {
		if i > 0 {
			buf = append(buf, sep...)
		}
		buf = append(buf, s...)
	}
	return string(buf)
}
