package certmanager

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
)

// ComputeTLSA311 returns the lowercase hex SHA-256 of the DER-encoded
// SubjectPublicKeyInfo for the given PEM certificate.  This is the
// 3 1 1 TLSA record (Usage=3, Selector=1, Matching=1), which is the
// canonical DANE form for self-hosted MX servers that use an arbitrary
// CA (including Let's Encrypt) or self-signed material.
//
// Returns a non-nil error if pemCertificate does not contain a parseable
// PEM block or the block does not decode to a valid x509 certificate.
func ComputeTLSA311(pemCertificate []byte) (hexDigest string, err error) {
	block, _ := pem.Decode(pemCertificate)
	if block == nil {
		return "", fmt.Errorf("tlsa: PEM decode failed — no certificate block found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("tlsa: parse x509 certificate: %w", err)
	}
	// MarshalPKIXPublicKey returns the DER of SubjectPublicKeyInfo, which
	// is exactly what Selector=1 (SPKI) mandates.
	spkiDER, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return "", fmt.Errorf("tlsa: marshal SPKI: %w", err)
	}
	sum := sha256.Sum256(spkiDER)
	return hex.EncodeToString(sum[:]), nil
}

// BuildTLSA constructs a TLSAInfo record for the given MX host and port
// by computing the 3 1 1 digest.  The FQDN is formed per RFC 6698 §7.2:
//
//	_<port>._tcp.<mx-host>.
//
// A trailing dot is always appended to <mx-host> if not already present
// so the record is fully qualified.  The port value is clamped to
// 1–65535; values outside this range return an error.
func BuildTLSA(fqdnMXHost string, port int, pemCert []byte) (*TLSAInfo, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("tlsa: port %d out of range 1-65535", port)
	}
	if fqdnMXHost == "" {
		return nil, fmt.Errorf("tlsa: empty MX host")
	}
	digest, err := ComputeTLSA311(pemCert)
	if err != nil {
		return nil, err
	}
	host := fqdnMXHost
	if len(host) == 0 || host[len(host)-1] != '.' {
		host = host + "."
	}
	tlsaFQDN := fmt.Sprintf("_%d._tcp.%s", port, host)
	return &TLSAInfo{
		Port:         port,
		Usage:        3,
		Selector:     1,
		MatchingType: 1,
		HexDigest:    digest,
		FQDN:         tlsaFQDN,
	}, nil
}
