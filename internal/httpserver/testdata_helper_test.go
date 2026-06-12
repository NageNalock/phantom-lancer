package httpserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateSelfSigned creates a short-lived self-signed ECDSA P-256 certificate
// suitable for TLS tests.  It writes cert.pem and key.pem under dir and sets
// the key file to mode 0o600.
func generateSelfSigned(t *testing.T, dir, host string) (certPath, keyPath string) {
	t.Helper()
	return generateSelfSignedWithLifetime(t, dir, host, 24*time.Hour)
}

// generateSelfSignedWithLifetime is like generateSelfSigned but with a caller-
// supplied validity lifetime (useful for HotReload tests that must distinguish
// old vs new certificates by NotAfter).
func generateSelfSignedWithLifetime(t *testing.T, dir, host string, lifetime time.Duration) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("rand serial: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	} else {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"))
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	cf, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("write cert: %v", err)
	}
	defer cf.Close()
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem cert: %v", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	kf, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open key: %v", err)
	}
	defer kf.Close()
	if err := pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		t.Fatalf("pem key: %v", err)
	}
	return certPath, keyPath
}

func symlinkTarget(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.Symlink(src, dst); err != nil {
		t.Fatalf("symlink %s -> %s: %v", dst, src, err)
	}
}

// pathsSameFile reports whether a and b refer to the same underlying file
// (tolerates macOS /var → /private/var prefix normalization).
func pathsSameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}
