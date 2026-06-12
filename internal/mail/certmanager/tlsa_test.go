package certmanager

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

// newSelfSignedCert is a pure-Go helper that generates a fresh ECDSA
// P-256 private key + a self-signed X.509 cert in PEM form.  Used so the
// TLSA tests are fully offline, deterministic per run, and never touch a
// CA or network.
//
// Returns (pemCert, pemKey, spkiDigestExpect) where spkiDigestExpect is the
// lowercase hex SHA-256 of the DER SubjectPublicKeyInfo, i.e. the TLSA
// 3 1 1 value that ComputeTLSA311 should return.
func newSelfSignedCert(t *testing.T) (pemCert, pemKey []byte, expectDigest string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	spkiDER, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("marshal PKIX pubkey: %v", err)
	}
	sum := sha256.Sum256(spkiDER)
	expectDigest = hex.EncodeToString(sum[:])

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.example.com"},
		DNSNames:     []string{"test.example.com", "mail.example.com"},
		NotBefore:    time.Now().UTC().Add(-1 * time.Hour),
		NotAfter:     time.Now().UTC().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	pemCert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal ec privkey: %v", err)
	}
	pemKey = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	return
}

// ---------- Subtest 1: ComputeTLSA311 against a known self-signed vector ----------

func TestTLSA_Compute311_KnownVector(t *testing.T) {
	pemCert, _, wantDigest := newSelfSignedCert(t)

	got, err := ComputeTLSA311(pemCert)
	if err != nil {
		t.Fatalf("ComputeTLSA311: %v", err)
	}
	if got != wantDigest {
		t.Fatalf("TLSA 3 1 1 mismatch:\n  want %s\n  got  %s", wantDigest, got)
	}
	if len(got) != 64 {
		t.Errorf("digest hex length: want 64, got %d", len(got))
	}
	if got != strings.ToLower(got) {
		t.Errorf("digest should be lowercase only, got %q", got)
	}
}

// ---------- Subtest 2: BuildTLSA full record (FQDN formatting + port) ----------

func TestTLSA_BuildTLSA(t *testing.T) {
	pemCert, _, expectDigest := newSelfSignedCert(t)
	cases := []struct {
		name     string
		mx       string
		port     int
		wantFQDN string // substring or exact
		wantErr  bool
	}{
		{
			name:     "mx_no_trailing_dot",
			mx:       "mail.example.com",
			port:     25,
			wantFQDN: "_25._tcp.mail.example.com.",
		},
		{
			name:     "mx_with_trailing_dot",
			mx:       "mx1.example.net.",
			port:     465,
			wantFQDN: "_465._tcp.mx1.example.net.",
		},
		{
			name:    "port_zero_invalid",
			mx:      "mail.example.com",
			port:    0,
			wantErr: true,
		},
		{
			name:    "port_above_65535",
			mx:      "mail.example.com",
			port:    70000,
			wantErr: true,
		},
		{
			name:    "empty_mx_host",
			mx:      "",
			port:    25,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rec, err := BuildTLSA(tc.mx, tc.port, pemCert)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got record=%+v", rec)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildTLSA: %v", err)
			}
			if rec.FQDN != tc.wantFQDN {
				t.Errorf("FQDN: want %q got %q", tc.wantFQDN, rec.FQDN)
			}
			if rec.Usage != 3 || rec.Selector != 1 || rec.MatchingType != 1 {
				t.Errorf("record tuple should be 3 1 1, got %d %d %d",
					rec.Usage, rec.Selector, rec.MatchingType)
			}
			if rec.Port != tc.port {
				t.Errorf("port: want %d, got %d", tc.port, rec.Port)
			}
			if rec.HexDigest != expectDigest {
				t.Errorf("hex digest mismatch:\n  want %s\n  got  %s", expectDigest, rec.HexDigest)
			}
		})
	}
}

// ---------- Subtest 3: Negative + edge cases (invalid PEM, garbage) ----------

func TestTLSA_Negative(t *testing.T) {
	cases := []struct {
		name string
		pem  []byte
	}{
		{"nil_pem", nil},
		{"empty_pem", []byte{}},
		{"not_pem_just_junk", []byte("hello, world! not a pem block")},
		{"pem_but_wrong_type", pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: []byte("not valid")})},
		{"pem_type_cert_but_invalid_der", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a valid x509 DER, sorry")})},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d, err := ComputeTLSA311(tc.pem)
			if err == nil {
				t.Errorf("expected error, got digest=%q (len=%d)", d, len(d))
			}
			if d != "" {
				t.Errorf("digest should be empty on error, got %q", d)
			}
			// BuildTLSA must also fail for these inputs (given valid host/port).
			rec, bErr := BuildTLSA("mx.example.com", 25, tc.pem)
			if bErr == nil {
				t.Errorf("BuildTLSA expected error, got rec=%+v", rec)
			}
		})
	}
}

// ---------- Subtest 4: Determinism (identical input → identical output) ----------

func TestTLSA_Determinism(t *testing.T) {
	pemCert, _, _ := newSelfSignedCert(t)
	// Call 5 times in rapid succession; all must return exactly the same value.
	var (
		firstDigest string
		firstFQDN   string
		firstPort   int
	)
	for i := 0; i < 5; i++ {
		d, err := ComputeTLSA311(pemCert)
		if err != nil {
			t.Fatalf("iteration %d ComputeTLSA311: %v", i, err)
		}
		rec, err := BuildTLSA("mail.foo", 993, pemCert)
		if err != nil {
			t.Fatalf("iteration %d BuildTLSA: %v", i, err)
		}
		if i == 0 {
			firstDigest = d
			firstFQDN = rec.FQDN
			firstPort = rec.Port
			continue
		}
		if d != firstDigest {
			t.Errorf("iteration %d ComputeTLSA311 not deterministic: got %s want %s",
				i, d, firstDigest)
		}
		if rec.HexDigest != firstDigest || rec.FQDN != firstFQDN || rec.Port != firstPort {
			t.Errorf("iteration %d BuildTLSA not deterministic: %+v", i, rec)
		}
	}
}

// ---------- Subtest 5: Re-use the same key material (same pubkey → same digest) ----------

func TestTLSA_SamePubkeySameDigest(t *testing.T) {
	// The same keypair used to issue two different certs (different SN / NotBefore)
	// should still yield the SAME TLSA 3 1 1 value, because TLSA 3 1 1 pins
	// the public key, not the full certificate.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	makeCert := func(sn int64, cn string) []byte {
		t.Helper()
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(sn),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().UTC().Add(-time.Hour),
			NotAfter:     time.Now().UTC().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
		if err != nil {
			t.Fatalf("create cert sn=%d: %v", sn, err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	d1, err := ComputeTLSA311(makeCert(1, "a.example.com"))
	if err != nil {
		t.Fatalf("digest1: %v", err)
	}
	d2, err := ComputeTLSA311(makeCert(2, "b.example.com"))
	if err != nil {
		t.Fatalf("digest2: %v", err)
	}
	if d1 != d2 {
		t.Errorf("same pubkey should produce same TLSA digest\nd1=%s\nd2=%s", d1, d2)
	}
	_ = errors.New // silence errors import if t.Fatalf already used
}

// ---------- Subtest 6: Different keys produce different digests ----------

func TestTLSA_DifferentKeysDifferentDigests(t *testing.T) {
	cert1, _, _ := newSelfSignedCert(t)
	cert2, _, _ := newSelfSignedCert(t)
	d1, err := ComputeTLSA311(cert1)
	if err != nil {
		t.Fatalf("digest1: %v", err)
	}
	d2, err := ComputeTLSA311(cert2)
	if err != nil {
		t.Fatalf("digest2: %v", err)
	}
	if d1 == d2 {
		t.Errorf("two unrelated certs must NOT have identical TLSA 3 1 1 digests: %s", d1)
	}
}

// ---------- Subtest 7: RSA-2048 self-signed cert inline generation ----------

// newRSASelfSignedCert generates an RSA 2048-bit self-signed cert.
// Returns (pemCert, pemKey, expectedDigest).
func newRSASelfSignedCert(t *testing.T) (pemCert, pemKey []byte, expectDigest string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	spkiDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal PKIX pubkey: %v", err)
	}
	sum := sha256.Sum256(spkiDER)
	expectDigest = hex.EncodeToString(sum[:])

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "rsa-2048.example.com"},
		DNSNames:     []string{"rsa-2048.example.com", "*.rsa-2048.example.com"},
		NotBefore:    time.Now().UTC().Add(-1 * time.Hour),
		NotAfter:     time.Now().UTC().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create rsa certificate: %v", err)
	}
	pemCert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privDER := x509.MarshalPKCS1PrivateKey(priv)
	pemKey = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privDER})
	return
}

func TestTLSA_RSA2048_InlineCert(t *testing.T) {
	pemCert, _, wantDigest := newRSASelfSignedCert(t)
	got, err := ComputeTLSA311(pemCert)
	if err != nil {
		t.Fatalf("ComputeTLSA311 RSA: %v", err)
	}
	if got != wantDigest {
		t.Errorf("RSA 2048 TLSA mismatch:\n  want %s\n  got  %s", wantDigest, got)
	}
	if len(got) != 64 {
		t.Errorf("RSA 2048 TLSA hex length: want 64 got %d (value=%q)", len(got), got)
	}
	if got != strings.ToLower(got) {
		t.Errorf("RSA 2048 TLSA should be lowercase, got %q", got)
	}
}

// ---------- Subtest 8: 100x determinism ----------

func TestTLSA_Determinism_100Times(t *testing.T) {
	cases := []struct {
		name  string
		genFn func(t *testing.T) (pemCert, _ []byte, _ string)
	}{
		{"ecdsa_p256", func(t *testing.T) (pemCert, _ []byte, _ string) {
			return newSelfSignedCert(t)
		}},
		{"rsa_2048", func(t *testing.T) (pemCert, _ []byte, _ string) {
			return newRSASelfSignedCert(t)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pemCert, _, expected := tc.genFn(t)
			first := ""
			for i := 0; i < 100; i++ {
				d, err := ComputeTLSA311(pemCert)
				if err != nil {
					t.Fatalf("iteration %d ComputeTLSA311: %v", i, err)
				}
				if i == 0 {
					first = d
				}
				if d != first {
					t.Errorf("iteration %d: not deterministic: got %s want %s", i, d, first)
				}
				if len(d) != 64 {
					t.Errorf("iteration %d: length want 64 got %d", i, len(d))
				}
			}
			if first != expected {
				t.Errorf("first digest doesn't match independently computed SPKI:\n  expected=%s\n  first   =%s", expected, first)
			}
		})
	}
}

// ---------- Subtest 9: Bad PEM corrupt (no panic) ----------

func TestTLSA_BadPEM_Corrupt_NoPanic(t *testing.T) {
	cases := []struct {
		name string
		pem  []byte
	}{
		{"corrupt_body", []byte("-----BEGIN CERTIFICATE-----\nINVALID\n-----END CERTIFICATE-----\n")},
		{"garbage_header_only", []byte("-----BEGIN CERTIFICATE-----\n")},
		{"truncated_base64", []byte("-----BEGIN CERTIFICATE-----\nabcdef\n")},
		{"wrong_block_type_privkey", []byte("-----BEGIN RSA PRIVATE KEY-----\nMGICAQACEQD...\n-----END RSA PRIVATE KEY-----\n")},
		{"binary_garbage", []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 0xFD}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic.
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ComputeTLSA311 panicked with %v for input %q", r, string(tc.pem))
				}
			}()
			d, err := ComputeTLSA311(tc.pem)
			if err == nil {
				t.Errorf("expected error for corrupt PEM, got digest=%q", d)
			}
			if d != "" {
				t.Errorf("digest should be empty on error, got %q", d)
			}
			// BuildTLSA must also not panic.
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("BuildTLSA panicked with %v for input %q", r, string(tc.pem))
				}
			}()
			rec, bErr := BuildTLSA("mx.example.com", 25, tc.pem)
			if bErr == nil {
				t.Errorf("BuildTLSA expected error, got rec=%+v", rec)
			}
		})
	}
}

// ---------- Subtest 10: ECDSA P256 cert → still 64 hex (sha256 size fixed) ----------

func TestTLSA_ECDSA_P256_Length64(t *testing.T) {
	pemCert, _, _ := newSelfSignedCert(t) // ECDSA P-256
	for i := 0; i < 10; i++ {
		d, err := ComputeTLSA311(pemCert)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		// SHA-256 always produces 32 bytes → 64 hex chars, regardless of
		// the public key algorithm (RSA, ECDSA, Ed25519).
		if len(d) != 64 {
			t.Errorf("ECDSA P256 iteration %d: hex length want 64 got %d (d=%q)", i, len(d), d)
		}
		// All chars must be valid lowercase hex digits.
		for j, c := range d {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("char %d (%c) is not lowercase hex in %q", j, c, d)
				break
			}
		}
	}
}

// ---------- Subtest 11: RSA 4096 self-signed → still exactly 64 hex chars ----------

func TestTLSA_RSA4096_Still64(t *testing.T) {
	// Generate a 4096-bit RSA keypair. Even though the SPKI DER is much
	// larger (~550 bytes vs ~91 bytes for P-256), SHA-256 always produces
	// 32 bytes → 64 lowercase hex chars. TLSA digest length is independent
	// of key size.
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		t.Fatalf("generate RSA-4096 key: %v", err)
	}
	spkiDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal RSA-4096 PKIX pubkey: %v", err)
	}
	sum := sha256.Sum256(spkiDER)
	expectDigest := hex.EncodeToString(sum[:])

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(9001),
		Subject:      pkix.Name{CommonName: "rsa4096.example.com"},
		DNSNames:     []string{"rsa4096.example.com"},
		NotBefore:    time.Now().UTC().Add(-1 * time.Hour),
		NotAfter:     time.Now().UTC().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create RSA-4096 certificate: %v", err)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	got, err := ComputeTLSA311(pemCert)
	if err != nil {
		t.Fatalf("ComputeTLSA311 RSA-4096: %v", err)
	}
	if len(got) != 64 {
		t.Errorf("RSA-4096 TLSA hex length: want 64 got %d (value=%q)", len(got), got)
	}
	if got != expectDigest {
		t.Errorf("RSA-4096 TLSA mismatch:\n  want %s\n  got  %s", expectDigest, got)
	}
	if got != strings.ToLower(got) {
		t.Errorf("RSA-4096 TLSA should be lowercase, got %q", got)
	}
}

// ---------- Subtest 12: Ed25519 self-signed → still exactly 64 hex chars ----------

func TestTLSA_Ed25519_Still64(t *testing.T) {
	// Ed25519 SPKI is tiny (44 bytes DER) but SHA-256 still produces 64
	// lowercase hex chars. This test verifies ComputeTLSA311 supports
	// Ed25519 keys end-to-end.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	spkiDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal Ed25519 PKIX pubkey: %v", err)
	}
	sum := sha256.Sum256(spkiDER)
	expectDigest := hex.EncodeToString(sum[:])

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2024),
		Subject:      pkix.Name{CommonName: "ed25519.example.com"},
		DNSNames:     []string{"ed25519.example.com"},
		NotBefore:    time.Now().UTC().Add(-1 * time.Hour),
		NotAfter:     time.Now().UTC().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatalf("create Ed25519 certificate: %v", err)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	got, err := ComputeTLSA311(pemCert)
	if err != nil {
		t.Fatalf("ComputeTLSA311 Ed25519: %v", err)
	}
	if len(got) != 64 {
		t.Errorf("Ed25519 TLSA hex length: want 64 got %d (value=%q)", len(got), got)
	}
	if got != expectDigest {
		t.Errorf("Ed25519 TLSA mismatch:\n  want %s\n  got  %s", expectDigest, got)
	}
	if got != strings.ToLower(got) {
		t.Errorf("Ed25519 TLSA should be lowercase, got %q", got)
	}
}

// ---------- Subtest 13: Seven precisely-named malformed PEM cases ----------

// TestTLSA_MalformedPEM_SevenCases exercises seven named malformed PEM
// scenarios.  Each subtest MUST: (a) NOT panic, (b) return an error,
// (c) return an empty digest.  BuildTLSA is also exercised per case.
func TestTLSA_MalformedPEM_SevenCases(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{
			name: "empty",
			data: []byte{},
		},
		{
			name: "random_bytes",
			data: []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77},
		},
		{
			name: "pem_header_but_no_block",
			data: []byte("-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n"),
		},
		{
			name: "wrong_pem_type",
			data: pem.EncodeToMemory(&pem.Block{
				Type: "RSA PRIVATE KEY",
				Bytes: []byte{0x30, 0x82, 0x01, 0x55, 0x02, 0x01, 0x00},
			}),
		},
		{
			name: "truncated_base64",
			data: []byte("-----BEGIN CERTIFICATE-----\nabcdefghi\njk"),
		},
		{
			name: "purely_textual",
			data: []byte("The quick brown fox jumps over the lazy dog. " +
				"This is a plain English sentence. There is no PEM here " +
				"whatsoever. Just regular text, really."),
		},
		{
			name: "utf8_bom_and_junk",
			data: append([]byte{0xEF, 0xBB, 0xBF, // UTF-8 BOM
				0xFF, 0xFE, 0xFD, 0xFC}, // garbage bytes
				[]byte("partial -----BEGIN CERT----- header garbage")...),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// --- ComputeTLSA311 ---
			noPanic := true
			func() {
				defer func() {
					if r := recover(); r != nil {
						noPanic = false
						t.Errorf("ComputeTLSA311 panicked: %v", r)
					}
				}()
				d, err := ComputeTLSA311(tc.data)
				if err == nil {
					t.Errorf("ComputeTLSA311 expected error for %q, got digest=%q (len=%d)",
						tc.name, d, len(d))
				}
				if d != "" {
					t.Errorf("ComputeTLSA311 digest should be empty on error, got %q", d)
				}
			}()
			if !noPanic {
				return
			}

			// --- BuildTLSA (with valid host/port but bad cert bytes) ---
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("BuildTLSA panicked: %v", r)
					}
				}()
				rec, err := BuildTLSA("mx.example.com", 25, tc.data)
				if err == nil {
					t.Errorf("BuildTLSA expected error for %q, got rec=%+v", tc.name, rec)
				}
			}()
		})
	}
}

// ---------- Subtest 14: 20 different ECDSA P256 certs → pairwise different digests ----------

// TestTLSA_DifferentCerts_DifferentDigests generates 20 unique ECDSA P-256
// self-signed certs (distinct SN/CN) and asserts every pair produces a
// different TLSA 3 1 1 digest.  The probability of a SHA-256 collision for
// 20 random inputs is ~4e-74 (birthday bound), so a collision would be a
// genuine bug in ComputeTLSA311 (e.g. returning a constant).
func TestTLSA_DifferentCerts_DifferentDigests(t *testing.T) {
	const N = 20
	digests := make([]string, 0, N)
	certs := make([][]byte, 0, N)

	for i := 0; i < N; i++ {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("iter %d: gen key: %v", i, err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(int64(i + 1)),
			Subject:      pkix.Name{CommonName: fmt.Sprintf("node-%d.example.com", i)},
			NotBefore:    time.Now().UTC().Add(-1 * time.Hour),
			NotAfter:     time.Now().UTC().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
		if err != nil {
			t.Fatalf("iter %d: create cert: %v", i, err)
		}
		pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		d, err := ComputeTLSA311(pemCert)
		if err != nil {
			t.Fatalf("iter %d: ComputeTLSA311: %v", i, err)
		}
		if len(d) != 64 {
			t.Errorf("iter %d: hex length want 64 got %d", i, len(d))
		}
		digests = append(digests, d)
		certs = append(certs, pemCert)
	}

	// O(N^2) pairwise uniqueness – trivial for N=20 (190 comparisons).
	for i := 0; i < N; i++ {
		for j := i + 1; j < N; j++ {
			if digests[i] == digests[j] {
				t.Errorf("collision: cert[%d] and cert[%d] have identical digest %s",
					i, j, digests[i])
			}
		}
	}

	// Also spot-check: BuildTLSA per cert produces the same digest as ComputeTLSA311.
	for i, pemCert := range certs {
		rec, err := BuildTLSA("mx.test", 587, pemCert)
		if err != nil {
			t.Fatalf("cert %d BuildTLSA: %v", i, err)
		}
		if rec.HexDigest != digests[i] {
			t.Errorf("cert %d: BuildTLSA.HexDigest=%s != ComputeTLSA311=%s",
				i, rec.HexDigest, digests[i])
		}
	}
}

// ---------- Subtest 15: BuildTLSA FQDN semantics + exhaustive port validation ----------

// TestTLSA_BuildTLSA_FQDN_Semantics asserts the strict FQDN format
// `_<port>._tcp.<host>.` (with trailing dot), Usage=3, Selector=1,
// MatchingType=1, and clamps ports to the 1-65535 range.
func TestTLSA_BuildTLSA_FQDN_Semantics(t *testing.T) {
	pemCert, _, expectDigest := newSelfSignedCert(t)

	rec, err := BuildTLSA("mail.example.com", 587, pemCert)
	if err != nil {
		t.Fatalf("BuildTLSA: %v", err)
	}

	// (a) FQDN format.
	const wantFQDN = "_587._tcp.mail.example.com."
	if rec.FQDN != wantFQDN {
		t.Errorf("FQDN: want %q got %q", wantFQDN, rec.FQDN)
	}
	if !strings.HasSuffix(rec.FQDN, ".") {
		t.Errorf("FQDN must end with a trailing dot, got %q", rec.FQDN)
	}
	if !strings.HasPrefix(rec.FQDN, "_587._tcp.") {
		t.Errorf("FQDN must start with _587._tcp., got %q", rec.FQDN)
	}

	// (b) TLSA 3-1-1 triple.
	if rec.Usage != 3 {
		t.Errorf("Usage: want 3 got %d", rec.Usage)
	}
	if rec.Selector != 1 {
		t.Errorf("Selector: want 1 got %d", rec.Selector)
	}
	if rec.MatchingType != 1 {
		t.Errorf("MatchingType: want 1 got %d", rec.MatchingType)
	}
	if rec.HexDigest != expectDigest {
		t.Errorf("HexDigest:\n  want %s\n  got  %s", expectDigest, rec.HexDigest)
	}

	// (c) Exhaustive bad-port table: every port outside 1..65535 must error.
	badPorts := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"just_above_max", 65536},
		{"far_above_max", 70000},
		{"way_above_max", 100000},
	}
	for _, bp := range badPorts {
		bp := bp
		t.Run(fmt.Sprintf("bad_port_%s_%d", bp.name, bp.port), func(t *testing.T) {
			r, err := BuildTLSA("mail.example.com", bp.port, pemCert)
			if err == nil {
				t.Fatalf("expected error for port=%d, got record=%+v", bp.port, r)
			}
		})
	}

	// (d) Empty MX host must error.
	t.Run("empty_mx_host", func(t *testing.T) {
		r, err := BuildTLSA("", 25, pemCert)
		if err == nil {
			t.Fatalf("expected error for empty MX, got record=%+v", r)
		}
	})

	// (e) Sanity: valid boundary ports 1 and 65535 MUST succeed.
	for _, p := range []int{1, 65535} {
		p := p
		t.Run(fmt.Sprintf("boundary_port_%d", p), func(t *testing.T) {
			r, err := BuildTLSA("mx.boundary.test", p, pemCert)
			if err != nil {
				t.Fatalf("port %d should succeed, got err=%v", p, err)
			}
			wantF := fmt.Sprintf("_%d._tcp.mx.boundary.test.", p)
			if r.FQDN != wantF {
				t.Errorf("port %d: FQDN want %q got %q", p, wantF, r.FQDN)
			}
			if r.Usage != 3 || r.Selector != 1 || r.MatchingType != 1 {
				t.Errorf("port %d: should be 3 1 1, got %d %d %d",
					p, r.Usage, r.Selector, r.MatchingType)
			}
		})
	}
}
