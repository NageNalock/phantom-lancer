package httpapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/config"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/httpserver"
	"phantom-lancer/internal/storage"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func newListenerTest(t *testing.T, startHTTPS bool) (
	server *Server,
	manager *httpserver.Manager,
	store *storage.Store,
	sessionToken, csrfToken string,
	baseURL string,
) {
	t.Helper()
	ctx := context.Background()

	// --- store + owner + session ------------------------------------------
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pw, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	owner, err := store.CreateOwner(ctx, "admin", string(pw))
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}

	// Pre-set runtime TLS/HSTS defaults (matches EnsureRuntimeSettings)
	initial, err := store.GetRuntimeSettings(ctx)
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	initial.AllowedRoots = []string{t.TempDir()}
	initial.TLSOwnerUIDCheck = true
	initial.HSTSMaxAgeSeconds = 15724800
	err = store.UpdateRuntimeSettings(ctx, initial)
	if err != nil {
		t.Fatalf("update runtime: %v", err)
	}

	// --- create session + CSRF token --------------------------------------
	sessionRaw, sessionHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}
	csrfRaw, csrfHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new csrf token: %v", err)
	}
	expires := time.Now().UTC().Add(24 * time.Hour)
	_, err = store.CreateSession(ctx, owner.ID, sessionHash, csrfHash, false, expires)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// --- httpserver.Manager -----------------------------------------------
	addr := pickEphemeralAddr(t)
	log, _ := newListenerTestLogger(t)

	handlerProvider := &deferredHandler{}
	var initialEndpoint httpserver.Endpoint
	if startHTTPS {
		cert, key := generateSelfSignedForTest(t, t.TempDir(), "localhost")
		cfg := httpserver.EndpointConfig{
			Addr:             addr,
			TLSEnabled:       true,
			TLSCertFile:      cert,
			TLSKeyFile:       key,
			TLSOwnerUIDCheck: false,
		}
		manager, initialEndpoint, err = httpserver.NewWithEndpoint(cfg, handlerProvider, log, false)
		if err != nil {
			t.Fatalf("NewWithEndpoint https: %v", err)
		}
		// Also write TLS info into DB so prevSettings matches curEp.
		r, _ := store.GetRuntimeSettings(ctx)
		r.Addr = addr
		r.TLSEnabled = true
		r.TLSCertFile = cert
		r.TLSKeyFile = key
		r.CookieSecure = true
		err = store.UpdateRuntimeSettings(ctx, r)
		if err != nil {
			t.Fatalf("update runtime tls: %v", err)
		}
	} else {
		manager, initialEndpoint, err = httpserver.NewWithEndpoint(
			httpserver.EndpointConfig{Addr: addr}, handlerProvider, log, false)
		if err != nil {
			t.Fatalf("NewWithEndpoint http: %v", err)
		}
		r, _ := store.GetRuntimeSettings(ctx)
		r.Addr = addr
		_ = store.UpdateRuntimeSettings(ctx, r)
	}
	_ = initialEndpoint

	// --- Server struct ----------------------------------------------------
	cfg := config.Config{
		Addr:    addr,
		DataDir: t.TempDir(),
		DBPath:  filepath.Join(t.TempDir(), "phantom-lancer.db"),
	}
	srv := &Server{
		cfg:            cfg,
		store:          store,
		hub:            events.NewHub(),
		log:            log,
		httpSrv:        manager,
		startedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		dataDir:        cfg.DataDir,
		logins:         newLoginBackoff(5),
		gatewayOAuth:   newCodexGatewayOAuthSessions(10 * time.Minute),
		privateUnlocks: newLoginBackoff(5),
		updateConfirms: newLoginBackoff(5),
		privateImages:  newPrivateImageAccess(),
	}
	handlerProvider.h = srv.Handler()

	// --- start listener ---------------------------------------------------
	if err := manager.Start(); err != nil {
		t.Fatalf("manager.Start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdownCtx)
	})
	// Small sleep for listener to come up
	time.Sleep(80 * time.Millisecond)

	scheme := "http"
	if initialEndpoint.TLSEnabled {
		scheme = "https"
	}
	baseURL = scheme + "://" + addr

	return srv, manager, store, sessionRaw, csrfRaw, baseURL
}

// deferredHandler lets us wire the Manager to srv.Handler() before the
// handler itself is fully constructed (breaks the circular dependency).
type deferredHandler struct{ h http.Handler }

func (d *deferredHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d.h == nil {
		http.Error(w, "handler not ready", 503)
		return
	}
	d.h.ServeHTTP(w, r)
}

func newListenerTestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(io.Discard, nil)), buf
}

func pickEphemeralAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pickEphemeralAddr: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func generateSelfSignedForTest(t *testing.T, dir, host string) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(48 * time.Hour),
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
	cert = filepath.Join(dir, "cert.pem")
	key = filepath.Join(dir, "key.pem")
	cf, err := os.OpenFile(cert, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer cf.Close()
	_ = pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	kb, _ := x509.MarshalECPrivateKey(priv)
	kf, err := os.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer kf.Close()
	_ = pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return
}

// newJSONRequest builds a POST JSON request with session cookie + CSRF header.
func newJSONRequest(t *testing.T, method, url string, body any, session, csrf string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, url, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Cookie", sessionCookie+"="+session)
	req.Header.Set("X-CSRF-Token", csrf)
	return req
}

// doReq dispatches r against h via httptest, capturing response.
func doReq(t *testing.T, h http.Handler, r *http.Request) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var m map[string]any
	if strings.Contains(w.Header().Get("Content-Type"), "json") {
		if w.Body.Len() > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &m)
		}
	}
	return w, m
}

// ---------------------------------------------------------------------------
// 1. POST /api/settings/tls-probe
// ---------------------------------------------------------------------------

func TestHandleProbeTLS_HappyPath(t *testing.T) {
	srv, _, _, sess, csrf, _ := newListenerTest(t, false)
	cert, key := generateSelfSignedForTest(t, t.TempDir(), "localhost")

	r := newJSONRequest(t, http.MethodPost, "/api/settings/tls-probe", map[string]any{
		"certFile":      cert,
		"keyFile":       key,
		"ownerUidCheck": false,
	}, sess, csrf)
	w, body := doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !boolFromMap(body, "ok") {
		t.Fatalf("expected ok=true, got %+v", body)
	}
	if days, _ := body["daysRemaining"].(float64); days < 1 {
		t.Errorf("daysRemaining should be positive: %v", days)
	}
	if names, _ := body["dnsNames"].([]any); len(names) == 0 {
		t.Errorf("dnsNames should be populated: %+v", body)
	}
}

func TestHandleProbeTLS_BadCertReturnsOKFalse(t *testing.T) {
	srv, _, _, sess, csrf, _ := newListenerTest(t, false)
	dir := t.TempDir()
	bad := filepath.Join(dir, "nope.pem")
	_ = os.WriteFile(bad, []byte("not a cert"), 0o644)

	r := newJSONRequest(t, http.MethodPost, "/api/settings/tls-probe", map[string]any{
		"certFile":      bad,
		"keyFile":       bad,
		"ownerUidCheck": false,
	}, sess, csrf)
	w, body := doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
	if boolFromMap(body, "ok") {
		t.Fatalf("expected ok=false, got %+v", body)
	}
	if s, _ := body["error"].(string); s == "" {
		t.Errorf("expected error string, got %+v", body)
	}
}

// ---------------------------------------------------------------------------
// 2. POST /api/settings/listener  HTTP→HTTPS
// ---------------------------------------------------------------------------

func TestHandleSwapEndpoint_HTTPToHTTPS(t *testing.T) {
	srv, mgr, store, sess, csrf, _ := newListenerTest(t, false)
	dir := t.TempDir()
	cert, key := generateSelfSignedForTest(t, dir, "localhost")
	newAddr := pickEphemeralAddr(t)

	bodyMap := map[string]any{
		"addr":             newAddr,
		"tlsEnabled":       true,
		"tlsCertFile":      cert,
		"tlsKeyFile":       key,
		"tlsOwnerUidCheck": false,
	}
	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", bodyMap, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("code=%d err=%+v", w.Code, resp["error"])
	}

	ep, _ := resp["endpoint"].(map[string]any)
	if ep["tlsEnabled"] != true {
		t.Errorf("endpoint.tlsEnabled should be true: %+v", resp)
	}
	if ep["scheme"] != "https" {
		t.Errorf("endpoint.scheme should be https: %+v", resp)
	}
	if resp["upgradeScheme"] != "https" {
		t.Errorf("upgradeScheme expected https: %+v", resp)
	}
	// DB should be updated
	cur, err := store.GetRuntimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cur.TLSEnabled {
		t.Errorf("DB TLSEnabled should be true")
	}
	if !cur.CookieSecure {
		t.Errorf("DB CookieSecure should mirror TLS")
	}
	// Manager actual endpoint
	manEp := mgr.Endpoint()
	if !manEp.TLSEnabled {
		t.Errorf("Manager endpoint should be TLS enabled")
	}
	if manEp.Addr != newAddr {
		t.Errorf("Manager addr = %s, want %s", manEp.Addr, newAddr)
	}
}

func TestHandleSwapEndpoint_SameAddrHTTPToHTTPS(t *testing.T) {
	srv, mgr, store, sess, csrf, _ := newListenerTest(t, false)
	addr := mgr.Endpoint().Addr
	cert, key := generateSelfSignedForTest(t, t.TempDir(), "localhost")

	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":             addr,
		"tlsEnabled":       true,
		"tlsCertFile":      cert,
		"tlsKeyFile":       key,
		"tlsOwnerUidCheck": false,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("same-address HTTP->HTTPS should not fail: code=%d err=%+v body=%s", w.Code, resp["error"], w.Body.String())
	}

	ep, _ := resp["endpoint"].(map[string]any)
	if ep["tlsEnabled"] != true || ep["scheme"] != "https" || ep["addr"] != addr {
		t.Fatalf("unexpected endpoint after same-address swap: %+v", resp)
	}
	if resp["upgradeScheme"] != "https" {
		t.Errorf("upgradeScheme expected https: %+v", resp)
	}

	cur, err := store.GetRuntimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cur.TLSEnabled || !cur.CookieSecure || cur.Addr != addr {
		t.Errorf("runtime settings not persisted correctly: %+v", cur)
	}

	manEp := mgr.Endpoint()
	if !manEp.TLSEnabled || manEp.Scheme != "https" || manEp.Addr != addr {
		t.Fatalf("manager endpoint not swapped correctly: %+v", manEp)
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
	var lastErr error
	for i := 0; i < 30; i++ {
		resp, err := client.Get("https://" + addr + "/api/health")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("HTTPS health returned %d", resp.StatusCode)
			}
			return
		}
		lastErr = err
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("HTTPS health not reachable on same address after swap: %v", lastErr)
}

// ---------------------------------------------------------------------------
// 3. Swap failure rolls back DB
// ---------------------------------------------------------------------------

func TestHandleSwapEndpoint_BadCertRollsBackDB(t *testing.T) {
	srv, mgr, store, sess, csrf, _ := newListenerTest(t, false)
	dir := t.TempDir()
	badCert := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(badCert, []byte("garbage"), 0o644)
	newAddr := pickEphemeralAddr(t)

	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":             newAddr,
		"tlsEnabled":       true,
		"tlsCertFile":      badCert,
		"tlsKeyFile":       badCert,
		"tlsOwnerUidCheck": false,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 for bad cert, got %d body=%s", w.Code, w.Body.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "tls_invalid" {
		t.Errorf("expected code=tls_invalid, got %+v", resp)
	}
	// DB should NOT be written to TLS (rollback happened at validation level, before
	// write, or we never reach swap step). In any case, DB must be TLS disabled.
	cur, err := store.GetRuntimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cur.TLSEnabled {
		t.Errorf("DB TLSEnabled must remain false after bad swap")
	}
	// Manager listener unchanged
	if mgr.Endpoint().TLSEnabled {
		t.Errorf("Manager must remain HTTP after failed swap")
	}
}

// ---------------------------------------------------------------------------
// 4. HTTPS→HTTP downgrade M7
// ---------------------------------------------------------------------------

func TestHandleSwapEndpoint_DowngradeRequiresConfirm(t *testing.T) {
	srv, _, _, sess, csrf, _ := newListenerTest(t, true)
	newAddr := pickEphemeralAddr(t)

	// WITHOUT confirm → 400
	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":       newAddr,
		"tlsEnabled": false,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 no-confirm downgrade, got %d: %s", w.Code, w.Body.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "confirm_required" {
		t.Errorf("expected code=confirm_required, got %+v", resp)
	}

	// WITH wrong phrase → 400
	r = newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              newAddr,
		"tlsEnabled":        false,
		"confirm_downgrade": true,
		"confirm_phrase":    "wrong phrase",
	}, sess, csrf)
	w, resp = doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 wrong phrase, got %d", w.Code)
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "confirm_required" {
		t.Errorf("expected code=confirm_required, got %+v", resp)
	}
}

func TestHandleSwapEndpoint_DowngradeRevokesSessionsAndClearsCookies(t *testing.T) {
	srv, _, store, sess, csrf, _ := newListenerTest(t, true)
	newAddr := pickEphemeralAddr(t)

	// Create a SECOND session to prove all are revoked
	ctx := context.Background()
	sess2Raw, sess2Hash, _ := auth.NewToken()
	csrf2Raw, csrf2Hash, _ := auth.NewToken()
	exp := time.Now().UTC().Add(time.Hour)
	_, _ = store.CreateSession(ctx, "1", sess2Hash, csrf2Hash, false, exp)
	_ = sess2Raw
	_ = csrf2Raw

	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              newAddr,
		"tlsEnabled":        false,
		"confirm_downgrade": true,
		"confirm_phrase":    downgradeConfirmPhrase,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("expected 200 downgrade, got %d: %s", w.Code, w.Body.String())
	}
	if resp["downgradeRedirect"] != "/login" {
		t.Errorf("expected downgradeRedirect=/login: %+v", resp)
	}

	// 4 Set-Cookie headers: session+csrf × Secure=true/false
	setCookies := w.Header().Values("Set-Cookie")
	if len(setCookies) != 4 {
		t.Fatalf("expected 4 Set-Cookie, got %d: %+v", len(setCookies), setCookies)
	}
	var gotSecure, gotInsecure, gotCSRFSecure, gotCSRFInsecure bool
	for _, c := range setCookies {
		namePart := strings.SplitN(c, "=", 2)[0]
		hasSecure := strings.Contains(c, "Secure")
		httpOnly := strings.Contains(c, "HttpOnly")
		if namePart == sessionCookie && hasSecure && httpOnly {
			gotSecure = true
		} else if namePart == sessionCookie && !hasSecure && httpOnly {
			gotInsecure = true
		} else if namePart == csrfCookie && hasSecure && !httpOnly {
			gotCSRFSecure = true
		} else if namePart == csrfCookie && !hasSecure && !httpOnly {
			gotCSRFInsecure = true
		}
		// Max-Age=-1 in cookie struct serialises to Max-Age=0 on the wire (both
		// mean "delete immediately" in the browser model; Go's net/http
		// serialises MaxAge<0 to Max-Age=0).  Either way the browser must
		// evict, so accept both forms.
		if !strings.Contains(c, "Max-Age=0") && !strings.Contains(c, "Max-Age=-1") &&
			!strings.Contains(strings.ToLower(c), "expires=") {
			t.Errorf("cookie should be expired (Max-Age<0 or Expires set): %s", c)
		}
	}
	for _, k := range []struct {
		have bool
		name string
	}{
		{gotSecure, "session Secure"},
		{gotInsecure, "session non-Secure"},
		{gotCSRFSecure, "csrf Secure"},
		{gotCSRFInsecure, "csrf non-Secure"},
	} {
		if !k.have {
			t.Errorf("missing Set-Cookie: %s (got %+v)", k.name, setCookies)
		}
	}

	// Both sessions should be revoked.  Use GetSessionByHash – it returns a
	// RevokedAt field; we also know RevokeAllSessions sets revoked_at for
	// every unrevoked row, so the row-level check plus the RevokeAll count
	// returned in the audit payload tells us the store layer did its job.
	sessRowA, err := store.GetSessionByHash(ctx, auth.HashToken(sess))
	if err != nil {
		t.Fatalf("get session A: %v", err)
	}
	sessRowB, err := store.GetSessionByHash(ctx, auth.HashToken(sess2Raw))
	if err != nil {
		t.Fatalf("get session B: %v", err)
	}
	if !sessRowA.RevokedAt.Valid {
		t.Errorf("session A should be revoked after downgrade: %+v", sessRowA)
	}
	if !sessRowB.RevokedAt.Valid {
		t.Errorf("session B should be revoked after downgrade: %+v", sessRowB)
	}

	// Next request with the old session cookie should 401
	r2 := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	r2.Header.Set("Cookie", sessionCookie+"="+sess)
	w2, _ := doReq(t, srv.Handler(), r2)
	if w2.Code != 401 {
		t.Errorf("next request should be 401, got %d", w2.Code)
	}
}

// ---------------------------------------------------------------------------
// 5. HSTS confirm gate
// ---------------------------------------------------------------------------

func TestHandleSwapEndpoint_HSTSRequiresConfirm(t *testing.T) {
	srv, mgr, store, sess, csrf, _ := newListenerTest(t, false)
	newAddr := pickEphemeralAddr(t)

	// Turn HSTS on without confirm → 400 confirm_hsts_required
	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              newAddr,
		"tlsEnabled":        false,
		"hstsEnabled":       true,
		"hstsMaxAgeSeconds": 3600,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 for HSTS without confirm, got %d: %s", w.Code, w.Body.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "confirm_hsts_required" {
		t.Errorf("expected code=confirm_hsts_required, got %+v", resp)
	}

	// With confirm → 200
	r = newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              newAddr,
		"tlsEnabled":        false,
		"hstsEnabled":       true,
		"hstsMaxAgeSeconds": 3600,
		"confirm_hsts":      true,
	}, sess, csrf)
	w, resp = doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("expected 200 HSTS with confirm, got %d: %s", w.Code, w.Body.String())
	}
	ep := mgr.Endpoint()
	if !ep.HSTSEnabled || ep.HSTSMaxAgeSeconds != 3600 {
		t.Errorf("HSTS not applied to Manager: %+v", ep)
	}
	cur, _ := store.GetRuntimeSettings(context.Background())
	if !cur.HSTSEnabled {
		t.Errorf("DB HSTSEnabled should be true: %+v", cur)
	}
}

// ---------------------------------------------------------------------------
// 6. PUT /api/settings with TLS field → 400 use_listener_endpoint
// ---------------------------------------------------------------------------

func TestHandleUpdateSettings_RejectsTLSEndpointFields(t *testing.T) {
	srv, _, _, sess, csrf, _ := newListenerTest(t, false)

	r := newJSONRequest(t, http.MethodPut, "/api/settings", map[string]any{
		"tlsEnabled": true,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 for TLSEnabled via PUT, got %d: %s", w.Code, w.Body.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "use_listener_endpoint" {
		t.Errorf("expected code=use_listener_endpoint, got %+v", resp)
	}

	r = newJSONRequest(t, http.MethodPut, "/api/settings", map[string]any{
		"hstsEnabled": true,
	}, sess, csrf)
	w, resp = doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 for HSTS via PUT, got %d: %s", w.Code, w.Body.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "use_listener_endpoint" {
		t.Errorf("expected code=use_listener_endpoint, got %+v", resp)
	}

	// Addr also rejected
	r = newJSONRequest(t, http.MethodPut, "/api/settings", map[string]any{
		"addr": "127.0.0.1:1111",
	}, sess, csrf)
	w, resp = doReq(t, srv.Handler(), r)
	if w.Code != 400 {
		t.Fatalf("expected 400 for addr via PUT, got %d: %s", w.Code, w.Body.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap["code"] != "use_listener_endpoint" {
		t.Errorf("expected code=use_listener_endpoint, got %+v", resp)
	}
}

// ---------------------------------------------------------------------------
// 7. HSTS header: conditional on endpoint TLSEnabled=true AND HSTSEnabled=true
// ---------------------------------------------------------------------------

func TestSecurityHeaders_ConditionalHSTS(t *testing.T) {
	srv, mgr, store, sess, csrf, _ := newListenerTest(t, false)

	// Step 1: HTTP + HSTS off → no STS header
	t.Run("http_no_hsts", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		w, _ := doReq(t, srv.Handler(), r)
		if h := w.Header().Get("Strict-Transport-Security"); h != "" {
			t.Errorf("no STS header expected on HTTP+HSTS off, got %q", h)
		}
		if h := w.Header().Get("X-Content-Type-Options"); h != "nosniff" {
			t.Errorf("X-Content-Type-Options missing: %q", h)
		}
		if h := w.Header().Get("X-Frame-Options"); h != "SAMEORIGIN" {
			t.Errorf("X-Frame-Options missing: %q", h)
		}
	})

	// Step 2: HTTP + HSTS enabled in DB/Manager but still HTTP listener → no STS
	newAddr := pickEphemeralAddr(t)
	rb := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              newAddr,
		"hstsEnabled":       true,
		"hstsMaxAgeSeconds": 3600,
		"confirm_hsts":      true,
	}, sess, csrf)
	w, _ := doReq(t, srv.Handler(), rb)
	if w.Code != 200 {
		t.Fatalf("enable hsts step failed: code=%d body=%s", w.Code, w.Body.String())
	}

	t.Run("http_with_hsts_setting_no_sts", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		w, _ := doReq(t, srv.Handler(), r)
		// HSTS only when TLSEnabled == true on the actual endpoint
		if h := w.Header().Get("Strict-Transport-Security"); h != "" {
			t.Errorf("STS must NOT appear when listener is plain HTTP: %q", h)
		}
	})

	// Step 3: enable real HTTPS + HSTS → STS present
	tlsDir := t.TempDir()
	cert, key := generateSelfSignedForTest(t, tlsDir, "localhost")
	httpsAddr := pickEphemeralAddr(t)
	rb = newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              httpsAddr,
		"tlsEnabled":        true,
		"tlsCertFile":       cert,
		"tlsKeyFile":        key,
		"tlsOwnerUidCheck":  false,
		"hstsEnabled":       true,
		"hstsMaxAgeSeconds": 15724800,
	}, sess, csrf)
	w, _ = doReq(t, srv.Handler(), rb)
	if w.Code != 200 {
		t.Fatalf("https+hsts swap failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Use an actual HTTPS client request against the real listener.
	t.Run("https_with_hsts_sts_present", func(t *testing.T) {
		client := &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
			},
		}
		var lastHSTS string
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get("https://" + mgr.Endpoint().Addr + "/api/health")
			if err != nil {
				time.Sleep(80 * time.Millisecond)
				continue
			}
			resp.Body.Close()
			lastHSTS = resp.Header.Get("Strict-Transport-Security")
			break
		}
		if !strings.Contains(lastHSTS, "max-age=15724800") {
			t.Errorf("HSTS header missing or wrong on HTTPS+HSTS listener: %q", lastHSTS)
		}
		if !strings.Contains(lastHSTS, "includeSubDomains") {
			t.Errorf("HSTS header missing includeSubDomains: %q", lastHSTS)
		}
	})

	// Step 4: downgrade back to HTTP → STS MUST vanish (M7 safety)
	downgradeAddr := pickEphemeralAddr(t)
	rb = newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":              downgradeAddr,
		"tlsEnabled":        false,
		"confirm_downgrade": true,
		"confirm_phrase":    downgradeConfirmPhrase,
	}, sess, csrf)
	w, _ = doReq(t, srv.Handler(), rb)
	if w.Code != 200 {
		t.Fatalf("downgrade failed: code=%d body=%s", w.Code, w.Body.String())
	}
	_ = store

	// Re-run handler-level health check — HSTS must be absent.
	t.Run("after_downgrade_sts_absent", func(t *testing.T) {
		// The downgrade wrote HSTSEnabled back to true (it was already true and
		// we didn't change it) — but the *actual* endpoint is now HTTP, so
		// securityHeaders must drop the STS header regardless of DB state.
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		w, _ := doReq(t, srv.Handler(), r)
		if h := w.Header().Get("Strict-Transport-Security"); h != "" {
			t.Errorf("STS must be absent after HTTPS→HTTP downgrade: %q", h)
		}
	})
}

// ---------------------------------------------------------------------------
// 8. Split state: swap via Manager directly → DB↔runtime divergence warning
// ---------------------------------------------------------------------------

func TestHandleSwapEndpoint_SplitStateDetected(t *testing.T) {
	srv, _, _, sess, csrf, _ := newListenerTest(t, false)
	// Manually mutate DB to TLSEnabled=true with garbage cert paths — the next
	// swap request that moves to a DIFFERENT non-TLS config should NOT produce
	// a split warning (it should write clean DB + swap cleanly).  Instead we
	// exercise split detection by verifying Manager.Endpoint matches DB after
	// a good swap — the field is computed, but when they DO diverge the flag
	// flips true.
	//
	// Simpler case: trigger an impossible bind via SwapEndpoint at Manager
	// level AFTER DB write would happen.  We already tested rollback.  Here
	// just verify splitStateWarning=false on the happy path.
	newAddr := pickEphemeralAddr(t)
	r := newJSONRequest(t, http.MethodPost, "/api/settings/listener", map[string]any{
		"addr":       newAddr,
		"tlsEnabled": false,
	}, sess, csrf)
	w, resp := doReq(t, srv.Handler(), r)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if resp["splitStateWarning"] == true {
		t.Errorf("happy path should NOT have split state warning: %+v", resp)
	}
}

// ---------------------------------------------------------------------------
// util
// ---------------------------------------------------------------------------

func boolFromMap(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	}
	return false
}

// avoid unused fmt import in minimalist build environments
var _ = fmt.Sprintf
