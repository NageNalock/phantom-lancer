package httpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// echoHandler returns 200 with "ok"
var echoHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, "ok")
})

func startThenCleanup(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Shutdown(ctx)
	})
	// Small sleep to let listener come up
	time.Sleep(80 * time.Millisecond)
}

func httpGet(t *testing.T, url string, client *http.Client) (int, string) {
	t.Helper()
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Sprintf("err: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// httpGetRetried attempts GET up to `attempts` times with 80ms backoff between
// tries.  Use after SwapEndpoint – the 2 s graceful drain is a hard deadline,
// but the new listener needs a small window before clients succeed (and the
// old listener's drain race with TLS handshake noise on macOS shouldn't fail
// the test).
func httpGetRetried(t *testing.T, url string, client *http.Client, wantCode int, attempts int) (int, string) {
	t.Helper()
	var lastCode int
	var lastBody string
	for i := 0; i < attempts; i++ {
		lastCode, lastBody = httpGet(t, url, client)
		if lastCode == wantCode {
			return lastCode, lastBody
		}
		time.Sleep(80 * time.Millisecond)
	}
	return lastCode, lastBody
}

func pickEphemeralAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick addr: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// ------------- NewWithEndpoint tests -------------

func TestNewWithEndpoint_HTTPOnly(t *testing.T) {
	log, _ := newTestLogger(t)
	addr := pickEphemeralAddr(t)
	m, ep, err := NewWithEndpoint(EndpointConfig{Addr: addr}, echoHandler, log, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ep.TLSEnabled || ep.Scheme != "http" {
		t.Errorf("expected http endpoint: %+v", ep)
	}
	startThenCleanup(t, m)
	code, body := httpGet(t, "http://"+addr+"/health", nil)
	if code != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("GET / = %d %q", code, body)
	}
	if got := m.Endpoint(); got.Addr != addr || got.Scheme != "http" {
		t.Errorf("endpoint = %+v", got)
	}
}

func TestNewWithEndpoint_HTTPSHappy(t *testing.T) {
	log, _ := newTestLogger(t)
	cert, key := generateSelfSigned(t, t.TempDir(), "localhost")
	addr := pickEphemeralAddr(t)
	cfg := EndpointConfig{
		Addr:              addr,
		TLSEnabled:        true,
		TLSCertFile:       cert,
		TLSKeyFile:        key,
		TLSOwnerUIDCheck:  false,
		HSTSEnabled:       true,
		HSTSMaxAgeSeconds: 15724800,
	}
	m, ep, err := NewWithEndpoint(cfg, echoHandler, log, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ep.TLSEnabled || ep.Scheme != "https" {
		t.Errorf("expected https endpoint: %+v", ep)
	}
	if ep.HSTSMaxAgeSeconds != 15724800 || !ep.HSTSEnabled {
		t.Errorf("HSTS not in endpoint: %+v", ep)
	}
	if len(ep.CertDNSNames) == 0 {
		t.Errorf("expected CertDNSNames populated, got %+v", ep)
	}
	startThenCleanup(t, m)

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
	code, body := httpGet(t, "https://"+addr+"/", client)
	if code != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("GET / = %d %q", code, body)
	}

	// Weak TLS client must be rejected (M3 strict crypto)
	badClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			// Force TLS 1.0 — server requires 1.2+
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS11},
		},
	}
	code, _ = httpGet(t, "https://"+addr+"/", badClient)
	if code != 0 {
		t.Errorf("expected TLS 1.1 client to fail, got code=%d", code)
	}
}

func TestNewWithEndpoint_HTTPSBadCertFallback(t *testing.T) {
	log, buf := newTestLogger(t)
	dir := t.TempDir()
	// cert with mismatched key
	badCert := filepath.Join(dir, "c.pem")
	badKey := filepath.Join(dir, "k.pem")
	if err := os.WriteFile(badCert, []byte("not a pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badKey, []byte("also not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := pickEphemeralAddr(t)
	cfg := EndpointConfig{
		Addr:             addr,
		TLSEnabled:       true,
		TLSCertFile:      badCert,
		TLSKeyFile:       badKey,
		TLSOwnerUIDCheck: false,
	}
	m, ep, err := NewWithEndpoint(cfg, echoHandler, log, false)
	if err != nil {
		t.Fatalf("bootStrict=false must not return err: %v", err)
	}
	if m == nil {
		t.Fatal("Manager must be non-nil (fallback HTTP)")
	}
	if ep.TLSEnabled || ep.Scheme != "http" {
		t.Errorf("fallback endpoint must be HTTP, got %+v", ep)
	}
	if !strings.Contains(buf.String(), "TLS_BOOT_FAILURE_HTTP_DOWNGRADE") {
		t.Errorf("CRITICAL log missing: %s", buf.String())
	}
	startThenCleanup(t, m)
	code, body := httpGet(t, "http://"+addr+"/", nil)
	if code != 200 {
		t.Fatalf("fallback http not reachable: %d %q", code, body)
	}
}

func TestNewWithEndpoint_HTTPSBadCertStrict(t *testing.T) {
	log, _ := newTestLogger(t)
	dir := t.TempDir()
	badCert := filepath.Join(dir, "c.pem")
	badKey := filepath.Join(dir, "k.pem")
	_ = os.WriteFile(badCert, []byte("x"), 0o644)
	_ = os.WriteFile(badKey, []byte("x"), 0o600)
	cfg := EndpointConfig{
		Addr:             pickEphemeralAddr(t),
		TLSEnabled:       true,
		TLSCertFile:      badCert,
		TLSKeyFile:       badKey,
		TLSOwnerUIDCheck: false,
	}
	m, _, err := NewWithEndpoint(cfg, echoHandler, log, true)
	if err == nil {
		t.Fatal("expected error for bootStrict=true with bad cert")
	}
	if m != nil {
		t.Errorf("Manager must be nil on strict boot failure")
	}
}

// ------------- SwapEndpoint tests -------------

func TestSwapEndpoint_FullCycleHTTPAndHTTPS(t *testing.T) {
	log, _ := newTestLogger(t)
	addrHTTP := pickEphemeralAddr(t)
	m, _, err := NewWithEndpoint(EndpointConfig{Addr: addrHTTP}, echoHandler, log, false)
	if err != nil {
		t.Fatal(err)
	}
	startThenCleanup(t, m)
	if c, _ := httpGet(t, "http://"+addrHTTP+"/", nil); c != 200 {
		t.Fatalf("initial HTTP failed: %d", c)
	}

	// Step 2: swap to HTTPS
	cert, key := generateSelfSigned(t, t.TempDir(), "localhost")
	addrHTTPS := pickEphemeralAddr(t)
	tlsCfg := EndpointConfig{
		Addr:              addrHTTPS,
		TLSEnabled:        true,
		TLSCertFile:       cert,
		TLSKeyFile:        key,
		TLSOwnerUIDCheck:  false,
		HSTSEnabled:       true,
		HSTSMaxAgeSeconds: 3600,
	}
	ep2, err := m.SwapEndpoint(tlsCfg)
	if err != nil {
		t.Fatalf("swap to https: %v", err)
	}
	if !ep2.TLSEnabled || ep2.Scheme != "https" {
		t.Errorf("expected https after swap: %+v", ep2)
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
	code, body := httpGetRetried(t, "https://"+addrHTTPS+"/", client, 200, 30)
	if code != 200 {
		t.Fatalf("HTTPS not reachable after swap: %d %q", code, body)
	}

	// Step 3: swap back to HTTP
	addrHTTP2 := pickEphemeralAddr(t)
	ep3, err := m.SwapEndpoint(EndpointConfig{Addr: addrHTTP2})
	if err != nil {
		t.Fatalf("swap back to http: %v", err)
	}
	if ep3.TLSEnabled || ep3.Scheme != "http" {
		t.Errorf("expected http after downgrade: %+v", ep3)
	}
	code, body = httpGetRetried(t, "http://"+addrHTTP2+"/", nil, 200, 30)
	if code != 200 {
		t.Fatalf("downgrade HTTP not reachable: %d %q", code, body)
	}
}

func TestSwapEndpoint_FastPathCertReload(t *testing.T) {
	log, _ := newTestLogger(t)
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	addr := pickEphemeralAddr(t)
	cfg := EndpointConfig{
		Addr:             addr,
		TLSEnabled:       true,
		TLSCertFile:      cert,
		TLSKeyFile:       key,
		TLSOwnerUIDCheck: false,
	}
	m, _, err := NewWithEndpoint(cfg, echoHandler, log, false)
	if err != nil {
		t.Fatal(err)
	}
	startThenCleanup(t, m)

	oldEp := m.Endpoint()
	// Overwrite cert/key with identical-content new files (same paths)
	b, _ := os.ReadFile(cert)
	k, _ := os.ReadFile(key)
	_ = os.WriteFile(cert, b, 0o644)
	_ = os.WriteFile(key, k, 0o600)

	newEp, err := m.SwapEndpoint(cfg)
	if err != nil {
		t.Fatalf("fast path swap err: %v", err)
	}
	// Addr must be identical (fast path: no rebind)
	if newEp.Addr != oldEp.Addr {
		t.Errorf("fast path should not rebind, old=%s new=%s", oldEp.Addr, newEp.Addr)
	}
	if newEp.CertReloadErr != "" {
		t.Errorf("fast path reload should be clean: %+v", newEp)
	}
}

func TestSwapEndpoint_SameAddrHTTPToHTTPS(t *testing.T) {
	log, _ := newTestLogger(t)
	addr := pickEphemeralAddr(t)
	m, _, err := NewWithEndpoint(EndpointConfig{Addr: addr}, echoHandler, log, false)
	if err != nil {
		t.Fatal(err)
	}
	startThenCleanup(t, m)
	if c, _ := httpGet(t, "http://"+addr+"/", nil); c != 200 {
		t.Fatalf("initial HTTP failed: %d", c)
	}

	cert, key := generateSelfSigned(t, t.TempDir(), "localhost")
	ep, err := m.SwapEndpoint(EndpointConfig{
		Addr:             addr,
		TLSEnabled:       true,
		TLSCertFile:      cert,
		TLSKeyFile:       key,
		TLSOwnerUIDCheck: false,
	})
	if err != nil {
		t.Fatalf("same-address HTTP->HTTPS swap should not report port in use: %v", err)
	}
	if !ep.TLSEnabled || ep.Scheme != "https" || ep.Addr != addr {
		t.Fatalf("unexpected endpoint after same-address swap: %+v", ep)
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
	code, body := httpGetRetried(t, "https://"+addr+"/", client, 200, 30)
	if code != 200 {
		t.Fatalf("HTTPS not reachable on same address after swap: %d %q", code, body)
	}
}

func TestSwapEndpoint_BindFailNoSideEffect(t *testing.T) {
	log, _ := newTestLogger(t)
	addr1 := pickEphemeralAddr(t)
	m, _, err := NewWithEndpoint(EndpointConfig{Addr: addr1}, echoHandler, log, false)
	if err != nil {
		t.Fatal(err)
	}
	startThenCleanup(t, m)
	if c, _ := httpGet(t, "http://"+addr1+"/", nil); c != 200 {
		t.Fatalf("initial HTTP failed: %d", c)
	}

	// Same-address no-op may succeed via the fast path; then use a privileged
	// port we generally cannot bind as a non-root process.
	_, err = m.SwapEndpoint(EndpointConfig{Addr: addr1})
	// With SO_REUSEADDR sometimes this succeeds on macOS. Use a
	// definitely-invalid numeric port (0 is invalid per SwapEndpoint validation
	// check, so use a privileged port we can't bind — 1 should work on modern
	// macOS as non-root).
	if err == nil {
		// Fallback: use a privileged port we definitely can't bind to.
		_, err = m.SwapEndpoint(EndpointConfig{Addr: "127.0.0.1:1"})
	}
	if err == nil {
		t.Skip("unable to find an un-bindable port on this host; skipping bind-fail test")
	}

	// Original listener must still work
	if c, _ := httpGet(t, "http://"+addr1+"/", nil); c != 200 {
		t.Errorf("old listener broken after failed swap: %d", c)
	}
}

// ------------- CertificateReloader tests -------------

func TestCertReloader_HotReload(t *testing.T) {
	log, _ := newTestLogger(t)
	dir := t.TempDir()
	cert, key := generateSelfSignedWithLifetime(t, dir, "localhost", 24*time.Hour)

	cr, err := NewCertReloader(cert, key, log)
	if err != nil {
		t.Fatalf("new reloader: %v", err)
	}
	defer cr.Close()
	oldAfter := cr.Snapshot().NotAfter

	// Overwrite with a 48-hour validity cert at the SAME paths
	cert2, key2 := generateSelfSignedWithLifetime(t, t.TempDir(), "localhost", 48*time.Hour)
	b, _ := os.ReadFile(cert2)
	k, _ := os.ReadFile(key2)
	if err := os.WriteFile(cert, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, k, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cr.LoadNow(); err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	newAfter := cr.Snapshot().NotAfter
	if !newAfter.After(oldAfter) {
		t.Errorf("NotAfter did not advance after reload: old=%v new=%v", oldAfter, newAfter)
	}
	// GetCertificate should still succeed
	if _, err := cr.GetCertificate(nil); err != nil {
		t.Errorf("GetCertificate after reload: %v", err)
	}
}

func TestCertReloader_BadNewCertPreservesOld(t *testing.T) {
	log, _ := newTestLogger(t)
	dir := t.TempDir()
	cert, key := generateSelfSigned(t, dir, "localhost")
	cr, err := NewCertReloader(cert, key, log)
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()
	oldCert, _ := cr.GetCertificate(nil)
	oldLeaf := oldCert.Certificate[0]

	// Make a *different* (cert, key) pair in a temp dir, then write only the
	// NEW cert on top of the OLD cert path, while keeping the OLD key path
	// intact.  This produces a valid PEM parse (ValidateTLSPaths won't reject
	// on PEM decoding alone), but LoadX509KeyPair will fail because public
	// keys don't match → exercises the G6 partial-write window.
	cert2, key2 := generateSelfSigned(t, t.TempDir(), "other-host")
	bNewCert, _ := os.ReadFile(cert2)
	bNewKey, _ := os.ReadFile(key2)
	_ = bNewKey
	// Overwrite with mismatched cert
	if err := os.WriteFile(cert, bNewCert, 0o644); err != nil {
		t.Fatal(err)
	}

	// G6: pendingReload < 3 → LoadX509KeyPair fails but lastErr is NOT set
	// (because ValidateTLSPaths also calls LoadX509KeyPair, it actually goes
	// through validate_failed recordFailure which does set lastErr.  The
	// important invariant is: old cert still served.)
	for i := 0; i < 2; i++ {
		_ = cr.LoadNow()
	}

	// Core invariant: the old (good) certificate is still returned on the hot
	// path even after reloads failed.
	gotCert, err := cr.GetCertificate(nil)
	if err != nil {
		t.Fatalf("old cert should still be served: %v", err)
	}
	if !bytes.Equal(gotCert.Certificate[0], oldLeaf) {
		t.Error("atomic cert was replaced by failed reload")
	}

	// Restore a consistent pair so reloader can recover.
	kOld, _ := os.ReadFile(key)
	_ = os.WriteFile(cert, bNewCert, 0o644)
	_ = os.WriteFile(key, kOld, 0o600)
	// actually key must pair with cert2 now.  Just write both.
	_ = os.WriteFile(key, bNewKey, 0o600)
	if err := cr.LoadNow(); err != nil {
		t.Errorf("recover after restore failed: %v", err)
	}
}

// ------------- Endpoint serialization -------------

func TestEndpoint_JSONFieldsMatchFrontend(t *testing.T) {
	ep := Endpoint{
		Addr:              "127.0.0.1:8443",
		TLSEnabled:        true,
		Scheme:            "https",
		CertFile:          "/etc/pl/tls/cert.pem",
		CertDNSNames:      []string{"localhost", "example.com"},
		CertNotBefore:     "2026-06-01T00:00:00Z",
		CertNotAfter:      "2026-12-01T00:00:00Z",
		CertReloadErr:     "", // exercises json ",omitempty"
		HSTSEnabled:       true,
		HSTSMaxAgeSeconds: 15724800,
	}
	b, err := json.Marshal(ep)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// CertReloadErr is omitempty: an empty value is NOT serialized (and that
	// matches the frontend's `certReloadErr?: string` optional field).
	want := []string{
		"addr", "tlsEnabled", "scheme", "certFile", "certDnsNames",
		"certNotBefore", "certNotAfter",
		"hstsEnabled", "hstsMaxAgeSeconds",
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("missing JSON field %q in %s", k, string(b))
		}
	}
	if _, present := m["certReloadErr"]; present {
		t.Errorf("certReloadErr should be omitted when empty, got %s", string(b))
	}
	// Set CertReloadErr non-empty → should appear
	ep.CertReloadErr = "boom"
	b2, _ := json.Marshal(ep)
	var m2 map[string]any
	_ = json.Unmarshal(b2, &m2)
	if v, ok := m2["certReloadErr"]; !ok || v.(string) != "boom" {
		t.Errorf("non-empty certReloadErr missing: %s", string(b2))
	}
	if m["scheme"] != "https" {
		t.Errorf("scheme = %v", m["scheme"])
	}
}

// ------------- cipher suite rejection (M3) -------------

func TestManager_RejectsWeakTLSClient(t *testing.T) {
	log, _ := newTestLogger(t)
	cert, key := generateSelfSigned(t, t.TempDir(), "localhost")
	addr := pickEphemeralAddr(t)
	m, _, err := NewWithEndpoint(EndpointConfig{
		Addr:             addr,
		TLSEnabled:       true,
		TLSCertFile:      cert,
		TLSKeyFile:       key,
		TLSOwnerUIDCheck: false,
	}, echoHandler, log, false)
	if err != nil {
		t.Fatal(err)
	}
	startThenCleanup(t, m)

	// Try to connect using only a non-AEAD (CBC) cipher suite and TLS 1.2.
	// The server's cipher list contains only 6 AEAD suites, so the handshake
	// must fail — no cipher overlap.  We also disable TLS 1.3 so the server
	// cannot "upgrade" past our narrow cipher list.
	weakClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MaxVersion:         tls.VersionTLS12,
				CipherSuites: []uint16{
					// CBC-only — not in the Manager's AEAD-only allowlist.
					tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
				},
			},
		},
	}
	code, msg := httpGet(t, "https://"+addr+"/", weakClient)
	if code != 0 {
		t.Errorf("weak CBC client should fail handshake, got code=%d msg=%s", code, msg)
	}

	// Also verify: TLS 1.1 client → rejected (server MinVersion=1.2)
	tls11Client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MaxVersion:         tls.VersionTLS11,
			},
		},
	}
	code, _ = httpGet(t, "https://"+addr+"/", tls11Client)
	if code != 0 {
		t.Errorf("TLS 1.1 client should fail handshake, got code=%d", code)
	}

	// Strong AEAD client should succeed
	strongClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS12,
				CipherSuites: []uint16{
					tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				},
			},
		},
	}
	if c, _ := httpGet(t, "https://"+addr+"/", strongClient); c != 200 {
		t.Errorf("strong AEAD client failed: %d", c)
	}
}
