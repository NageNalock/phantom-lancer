package certmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/crypto/acme"
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

// ---- Real ACME implementation ----------------------------------------------

type ACMEDNS01Client struct {
	client         *acme.Client
	accountKey     *ecdsa.PrivateKey
	certificateKey *ecdsa.PrivateKey
	Directory      string
	ContactEmail   string
	AcceptTOS      bool
}

// NewLegoClient constructs the default ACME DNS-01 client. The name is kept for
// API compatibility with older certmanager call sites, but the implementation is
// intentionally based on golang.org/x/crypto/acme and does not use HTTP-01,
// TLS-ALPN-01, or any lego stub path.
func NewLegoClient(dir string, email string, acmeURL string, acceptTOS bool) (LegoClient, error) {
	_ = dir
	if acmeURL == "" {
		acmeURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	if email == "" {
		return nil, fmt.Errorf("acme: contact email is required")
	}
	if !acceptTOS {
		return nil, fmt.Errorf("acme: AcceptTOS must be true before ACME account can be created (directory=%s)", acmeURL)
	}
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("acme: generate account key: %w", err)
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("acme: generate certificate key: %w", err)
	}
	return &ACMEDNS01Client{
		client: &acme.Client{
			Key:          accountKey,
			DirectoryURL: acmeURL,
			HTTPClient:   &http.Client{Timeout: 30 * time.Second},
			UserAgent:    "phantom-lancer-mail-certmanager",
		},
		accountKey:     accountKey,
		certificateKey: certKey,
		Directory:      acmeURL,
		ContactEmail:   email,
		AcceptTOS:      acceptTOS,
	}, nil
}

func (c *ACMEDNS01Client) ObtainCertificate(
	ctx context.Context,
	domains []string,
	challengeCallback func(presentationFQDN, keyAuth string) (cleanup func() error, err error),
) (pemPrivateKey, pemCertificate, pemIssuerChain []byte, err error) {
	if len(domains) == 0 {
		return nil, nil, nil, fmt.Errorf("acme: ObtainCertificate: empty domains list")
	}
	if challengeCallback == nil {
		return nil, nil, nil, fmt.Errorf("acme: ObtainCertificate: nil DNS-01 challenge callback")
	}
	if _, err := c.client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + c.ContactEmail}}, acme.AcceptTOS); err != nil {
		return nil, nil, nil, fmt.Errorf("acme: register account: %w", err)
	}
	order, err := c.client.AuthorizeOrder(ctx, acme.DomainIDs(domains...))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acme: authorize order: %w", err)
	}
	cleanups := make([]func() error, 0, len(order.AuthzURLs))
	defer func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				_ = cleanups[i]()
			}
		}
	}()
	for _, authzURL := range order.AuthzURLs {
		authz, err := c.client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("acme: get authorization: %w", err)
		}
		if authz.Status != acme.StatusPending {
			continue
		}
		identifier := authz.Identifier.Value
		chal := pickDNS01Challenge(authz.Challenges)
		if chal == nil {
			return nil, nil, nil, fmt.Errorf("acme: no dns-01 challenge for %s", identifier)
		}
		txtValue, err := c.client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("acme: compute dns-01 record for %s: %w", identifier, err)
		}
		fqdn := "_acme-challenge." + identifier + "."
		cleanup, err := challengeCallback(fqdn, txtValue)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("acme: present dns-01 for %s: %w", identifier, err)
		}
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
		if _, err := c.client.Accept(ctx, chal); err != nil {
			return nil, nil, nil, fmt.Errorf("acme: accept dns-01 for %s: %w", identifier, err)
		}
		if _, err := c.client.WaitAuthorization(ctx, authz.URI); err != nil {
			return nil, nil, nil, fmt.Errorf("acme: wait authorization for %s: %w", identifier, err)
		}
	}
	order, err = c.client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acme: wait order: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: append([]string(nil), domains...),
	}, c.certificateKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acme: create csr: %w", err)
	}
	chainDER, _, err := c.client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acme: finalize order: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(c.certificateKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acme: marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM, chainPEM := encodeCertificateChain(chainDER)
	if len(certPEM) == 0 {
		return nil, nil, nil, fmt.Errorf("acme: CA returned empty certificate chain")
	}
	if len(chainPEM) == 0 {
		chainPEM = certPEM
	}
	return keyPEM, certPEM, chainPEM, nil
}

func (c *ACMEDNS01Client) RevokeCertificate(ctx context.Context, pemBytes []byte) error {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("acme: revoke: PEM certificate is required")
	}
	return c.client.RevokeCert(ctx, c.accountKey, block.Bytes, acme.CRLReasonUnspecified)
}

func pickDNS01Challenge(challenges []*acme.Challenge) *acme.Challenge {
	for _, ch := range challenges {
		if ch != nil && ch.Type == "dns-01" {
			return ch
		}
	}
	return nil
}

func encodeCertificateChain(chainDER [][]byte) (certPEM, chainPEM []byte) {
	for i, der := range chainDER {
		block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		if i == 0 {
			certPEM = append(certPEM, block...)
			continue
		}
		chainPEM = append(chainPEM, block...)
	}
	return certPEM, chainPEM
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
