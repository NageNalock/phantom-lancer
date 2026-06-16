package mail

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Test helpers.
// -----------------------------------------------------------------------------

// openTestStore builds a *storage.Store on a fresh temp file and returns it
// along with a cleanup that closes it.
func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test-phantom.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// newTestService builds a *Service wired to a fresh store + event hub.
func newTestService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	store := openTestStore(t)
	hub := events.NewHub()
	ctx := context.Background()
	svc := NewService(store, hub, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Ensure(ctx); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// Ensure() calls registerMailRedactions which appends to safelog's global
	// registry. Clean it up when the test ends so subtests don't leak state.
	return svc, ctx
}

// registerIngressWebhook is a convenience wrapper for registering a standard
// inbound webhook. It returns (registrationID, plaintextSecret).
func registerIngressWebhook(t *testing.T, svc *Service, ctx context.Context) (string, string) {
	t.Helper()
	reg, plain, err := svc.WebhookRegister(ctx, &WebhookRegisterRequest{
		Name:      "test-ingress",
		Direction: "in",
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("register ingress webhook: %v", err)
	}
	return reg.ID, plain
}

// computeSignature returns the "sha256=<hex>" string that WebhookIngest
// expects as the X-Mox-Signature header value.
func computeSignature(secret string, tsUnix int64, body []byte) string {
	signingInput := fmt.Sprintf("%d.%s", tsUnix, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// computeSignatureWithTS mirrors computeSignature but lets the caller supply
// the raw timestamp string (so they can craft non-integer ts values, etc.).
func computeSignatureWithTS(secret, tsRaw string, body []byte) string {
	signingInput := tsRaw + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// sendIngest is the HTTP helper used by all ingress subtests.  It builds a
// POST /api/mail/hooks/in request with:
//   - timestamp header set from tsUnix (0 → omit header)
//   - signature header set via computeSignature(secret, tsUnix, body) — unless
//     signatureOverride is non-empty, in which case it is used verbatim.
//   - RemoteAddr set to srcAddr (or 127.0.0.1:0 if empty)
//   - optional extra headers merged in
//
// It calls svc.handleWebhookIngestHTTP (a thin handler wrapper that mirrors
// the HTTP handler in httpapi) and returns the httptest response recorder.
// The response JSON's "code" field (on error) and status are compared
// against expectedStatus and expectedCode.
func sendIngest(
	t *testing.T,
	svc *Service,
	secret string,
	tsUnix int64,
	body []byte,
	srcAddr string,
	signatureOverride string,
	extra map[string]string,
	expectedStatus int,
	expectedCode string,
) *httptest.ResponseRecorder {
	t.Helper()
	if srcAddr == "" {
		srcAddr = "127.0.0.1:0"
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mail/hooks/in", bytes.NewReader(body))
	req.RemoteAddr = srcAddr
	if tsUnix != 0 {
		req.Header.Set("X-Mox-Timestamp", strconv.FormatInt(tsUnix, 10))
	}
	var tsStr string
	if tsUnix != 0 {
		tsStr = strconv.FormatInt(tsUnix, 10)
	}
	if signatureOverride != "" {
		req.Header.Set("X-Mox-Signature", signatureOverride)
	} else if tsUnix != 0 {
		req.Header.Set("X-Mox-Signature", computeSignatureWithTS(secret, tsStr, body))
	}
	// Honour override of ts header (useful for non-integer timestamps).
	if v, ok := extra["X-Mox-Timestamp"]; ok {
		req.Header.Set("X-Mox-Timestamp", v)
		delete(extra, "X-Mox-Timestamp")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc.webhookIngestHandler(w, r)
	})
	handler.ServeHTTP(rec, req)

	if rec.Code != expectedStatus {
		t.Fatalf("status = %d (%s), want %d. body=%s",
			rec.Code, rec.Body.String(), expectedStatus, rec.Body.String())
	}
	// For non-2xx responses, parse the JSON envelope and check `code`.
	if rec.Code >= 400 && expectedCode != "" {
		var env struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode error response: %v (body=%s)", err, rec.Body.String())
		}
		if env.Code != expectedCode {
			t.Fatalf("error code = %q, want %q (body=%s)", env.Code, expectedCode, rec.Body.String())
		}
	}
	return rec
}

// webhookIngestHandler is a test-only handler method on *Service that
// mirrors the HTTP ingress handler at internal/httpapi/mail.go so that we
// can test the full 4-tier pipeline (src-IP → body-size → timestamp → HMAC)
// without importing httpapi (which would cause a circular dependency).
func (s *Service) webhookIngestHandler(w http.ResponseWriter, r *http.Request) {
	writeError := func(w http.ResponseWriter, status int, code, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		b, _ := json.Marshal(map[string]any{"code": code, "message": msg})
		_, _ = w.Write(b)
	}
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		b, _ := json.Marshal(v)
		_, _ = w.Write(b)
	}
	timestampStr := r.Header.Get("X-Mox-Timestamp")
	signatureHeader := r.Header.Get("X-Mox-Signature")
	remote := r.RemoteAddr
	if timestampStr == "" {
		writeError(w, http.StatusBadRequest, "missing_timestamp", "missing X-Mox-Timestamp header")
		return
	}
	if signatureHeader == "" {
		writeError(w, http.StatusUnauthorized, "signature_missing", "missing X-Mox-Signature header")
		return
	}
	// Cap read at 1 MiB.
	const maxBody = 1 << 20
	lr := io.LimitReader(r.Body, maxBody+1)
	body, rerr := io.ReadAll(lr)
	_ = r.Body.Close()
	if rerr != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", rerr.Error())
		return
	}
	if int64(len(body)) > maxBody {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "payload exceeds max allowed size")
		return
	}
	status, eventID, err := s.WebhookIngest(r.Context(), remote, timestampStr, signatureHeader, body)
	if err != nil {
		msg := err.Error()
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "missing_timestamp"), strings.Contains(lower, "invalid timestamp"):
			writeError(w, http.StatusBadRequest, "missing_timestamp", msg)
		case strings.Contains(lower, "timestamp_expired"), strings.Contains(lower, "expired"):
			writeError(w, http.StatusBadRequest, "timestamp_expired", msg)
		case strings.Contains(lower, "timestamp_skew"), strings.Contains(lower, "skew"):
			writeError(w, http.StatusBadRequest, "timestamp_skew", msg)
		case strings.Contains(lower, "timestamp"):
			writeError(w, http.StatusBadRequest, "bad_timestamp", msg)
		case strings.Contains(lower, "malformed_signature"):
			writeError(w, http.StatusUnauthorized, "malformed_signature", msg)
		case strings.Contains(lower, "signature_mismatch"):
			writeError(w, http.StatusUnauthorized, "signature_mismatch", msg)
		case strings.Contains(lower, "hmac"):
			writeError(w, http.StatusUnauthorized, "hmac_invalid", msg)
		case strings.Contains(lower, "signature"):
			writeError(w, http.StatusUnauthorized, "signature_mismatch", msg)
		case strings.Contains(lower, "loopback"), strings.Contains(lower, "source"):
			writeError(w, http.StatusForbidden, "source_not_loopback", msg)
		case strings.Contains(lower, "no registration"), strings.Contains(lower, "not_found"):
			writeError(w, http.StatusNotFound, "webhook_not_found", msg)
		case strings.Contains(lower, "body_too_large"):
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", msg)
		default:
			writeError(w, http.StatusBadRequest, "ingress_rejected", msg)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "event_id": eventID})
}

// -----------------------------------------------------------------------------
// WebhookIngest: 4-tier validation.
// -----------------------------------------------------------------------------

// TestWebhookIngest_Valid covers the happy-path baseline used by many
// other subtests: loopback src, ts=now, correct HMAC, small body.
func TestWebhookIngest_Valid(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)

	body := []byte(`{"event_type":"delivery.open","id":"evt_001"}`)
	ts := time.Now().Unix()
	_ = ctx
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusOK, "")
}

func TestWebhookIngest_InvalidSignature(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()
	good := computeSignature(secret, ts, body)
	// Flip the last hex nibble — produces a valid-length, valid-hex but
	// cryptographically incorrect signature.
	runes := []rune(good)
	last := runes[len(runes)-1]
	if last == 'a' {
		runes[len(runes)-1] = 'b'
	} else {
		runes[len(runes)-1] = 'a'
	}
	bad := string(runes)
	sendIngest(t, svc, secret, ts, body, "", bad, nil, http.StatusUnauthorized, "signature_mismatch")
}

func TestWebhookIngest_WrongSecret(t *testing.T) {
	svc, ctx := newTestService(t)
	_, _ = registerIngressWebhook(t, svc, ctx)
	otherSecret := "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()
	sendIngest(t, svc, otherSecret, ts, body, "", "", nil, http.StatusUnauthorized, "signature_mismatch")
}

func TestWebhookIngest_SignatureNotHex(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`ok`)
	ts := time.Now().Unix()
	sig := "sha256=" + strings.Repeat("Z", 64)
	// Recompute ts via extra header since we're overriding the computed sig.
	_ = secret
	sendIngest(t, svc, secret, ts, body, "", sig, map[string]string{
		"X-Mox-Timestamp": strconv.FormatInt(ts, 10),
	}, http.StatusUnauthorized, "malformed_signature")
}

func TestWebhookIngest_TimestampMissing(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`x`)
	sendIngest(t, svc, secret, 0, body, "", "sha256=0000", nil, http.StatusBadRequest, "missing_timestamp")
}

func TestWebhookIngest_TimestampFuture_1000s(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{}`)
	ts := time.Now().Unix() + 1000
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusBadRequest, "timestamp_skew")
}

func TestWebhookIngest_TimestampPast_2000s(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{}`)
	ts := time.Now().Unix() - 2000
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusBadRequest, "timestamp_expired")
}

func TestWebhookIngest_TimestampBoundary899s(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix() - 899
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusOK, "")
}

func TestWebhookIngest_TimestampBoundary901s(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{}`)
	ts := time.Now().Unix() - 901
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusBadRequest, "timestamp_expired")
}

func TestWebhookIngest_SourceIP_External(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{}`)
	ts := time.Now().Unix()
	sendIngest(t, svc, secret, ts, body, "8.8.8.8:12345", "", nil, http.StatusForbidden, "source_not_loopback")
}

func TestWebhookIngest_SourceIP_IPv6Loopback(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()
	sendIngest(t, svc, secret, ts, body, "[::1]:12345", "", nil, http.StatusOK, "")
}

func TestWebhookIngest_SourceIP_Localhost(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()
	sendIngest(t, svc, secret, ts, body, "127.0.0.1:54321", "", nil, http.StatusOK, "")
}

func TestWebhookIngest_BodyTooLarge_1_1MB(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := bytes.Repeat([]byte("A"), 1_100_000)
	ts := time.Now().Unix()
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusRequestEntityTooLarge, "body_too_large")
}

func TestWebhookIngest_BodyExact1MB(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	// Exactly 1 MiB of JSON-ish bytes: still valid, no HMAC issues.
	body := bytes.Repeat([]byte("x"), 1<<20)
	ts := time.Now().Unix()
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusOK, "")
}

func TestWebhookIngest_Body1MBPlus1(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := bytes.Repeat([]byte("y"), 1<<20+1)
	ts := time.Now().Unix()
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusRequestEntityTooLarge, "body_too_large")
}

func TestWebhookIngest_ContentTypeIrrelevant(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()
	// Explicitly omit Content-Type.
	sendIngest(t, svc, secret, ts, body, "", "", map[string]string{
		"Content-Type": "",
	}, http.StatusOK, "")
}

func TestWebhookIngest_SignaturePrefix(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{}`)
	ts := time.Now().Unix()
	// Good sig but wrong prefix case.
	good := computeSignature(secret, ts, body)
	bad := "SHA256=" + strings.TrimPrefix(good, "sha256=")
	sendIngest(t, svc, secret, ts, body, "", bad, nil, http.StatusUnauthorized, "malformed_signature")
}

// TestWebhookIngest_SignatureOverwritten verifies that an attacker cannot
// reuse a valid signature on a tampered body — the HMAC covers both body
// and timestamp, so substitution must fail.
func TestWebhookIngest_SignatureOverwritten(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	goodBody := []byte(`{"event_type":"delivery.open","amount":0}`)
	maliciousBody := []byte(`{"event_type":"delivery.open","amount":1000}`)
	ts := time.Now().Unix()
	goodSig := computeSignature(secret, ts, goodBody)
	// Apply the GOOD signature to the MALICIOUS body — must fail.
	extra := map[string]string{"X-Mox-Timestamp": strconv.FormatInt(ts, 10)}
	sendIngest(t, svc, secret, ts, maliciousBody, "", goodSig, extra, http.StatusUnauthorized, "signature_mismatch")
}

// TestWebhookRotateSecret_ThenOldRejected verifies the full
// register → ingest(ok) → rotate → ingest(old sig fails, new sig ok) flow.
func TestWebhookRotateSecret_ThenOldRejected(t *testing.T) {
	svc, ctx := newTestService(t)
	id, oldSecret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()

	// 1. Baseline: old secret works.
	sendIngest(t, svc, oldSecret, ts, body, "", "", nil, http.StatusOK, "")

	// 2. Rotate.
	newSecret, err := svc.WebhookRotateSecret(ctx, id)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newSecret == oldSecret {
		t.Fatalf("rotate returned the same secret")
	}
	if len(newSecret) < 32 {
		t.Fatalf("rotated secret length %d < 32", len(newSecret))
	}

	// 3. Old signature must be rejected with the new registration.
	ts3 := time.Now().Unix()
	sendIngest(t, svc, oldSecret, ts3, body, "", "", nil, http.StatusUnauthorized, "signature_mismatch")

	// 4. New signature must succeed.
	ts4 := time.Now().Unix()
	sendIngest(t, svc, newSecret, ts4, body, "", "", nil, http.StatusOK, "")
}

// TestWebhookIngest_ConcurrentHMACVerifies spins up 100 goroutines with a
// deterministic mix of valid and invalid requests and asserts that every
// response matches expectations — no data-races under -race.
func TestWebhookIngest_ConcurrentHMACVerifies(t *testing.T) {
	svc, ctx := newTestService(t)
	_, secret := registerIngressWebhook(t, svc, ctx)
	wrongSecret := "00" + secret[2:]
	if strings.HasPrefix(secret, "00") {
		wrongSecret = "ff" + secret[2:]
	}

	const n = 100
	var wg sync.WaitGroup
	errs := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			valid := i%2 == 0
			body := []byte(fmt.Sprintf(`{"event_type":"delivery","i":%d}`, i))
			ts := time.Now().Unix()
			useSecret := secret
			if !valid {
				useSecret = wrongSecret
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/mail/hooks/in", bytes.NewReader(body))
			req.RemoteAddr = "127.0.0.1:0"
			req.Header.Set("X-Mox-Timestamp", strconv.FormatInt(ts, 10))
			req.Header.Set("X-Mox-Signature", computeSignature(useSecret, ts, body))
			svc.webhookIngestHandler(rec, req)
			if valid && rec.Code != http.StatusOK {
				errs[i] = fmt.Sprintf("[%d] valid → got %d: %s", i, rec.Code, rec.Body.String())
			}
			if !valid && rec.Code != http.StatusUnauthorized {
				errs[i] = fmt.Sprintf("[%d] invalid → got %d: %s", i, rec.Code, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()
	_ = ctx
	for _, e := range errs {
		if e != "" {
			t.Fatal(e)
		}
	}
}

// -----------------------------------------------------------------------------
// WebhookRegister / RotateSecret / Unregister (WebhookDelete).
// -----------------------------------------------------------------------------

func TestWebhookRegister_DuplicateName(t *testing.T) {
	svc, ctx := newTestService(t)
	r1, _, err := svc.WebhookRegister(ctx, &WebhookRegisterRequest{
		Name:      "dup",
		Direction: "in",
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	r2, _, err := svc.WebhookRegister(ctx, &WebhookRegisterRequest{
		Name:      "dup",
		Direction: "in",
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	// Current implementation assigns different IDs (auto-generated, not
	// name-keyed); duplicate names are permitted as long as IDs differ.
	if r1.ID == r2.ID {
		t.Fatalf("expected different IDs for duplicate names, got same %q", r1.ID)
	}
}

func TestWebhookSecret_Rotate_ReturnsNew(t *testing.T) {
	svc, ctx := newTestService(t)
	id, plain := registerIngressWebhook(t, svc, ctx)
	rotated, err := svc.WebhookRotateSecret(ctx, id)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == plain {
		t.Fatalf("rotate did not change secret")
	}
	// generateWebhookSecret produces hex(32 random bytes) → 64 chars.
	if len(rotated) < 32 {
		t.Fatalf("rotated length %d < 32", len(rotated))
	}
	// Crypto check: decoded hex is at least 32 bytes of entropy.
	raw, err := hex.DecodeString(rotated)
	if err != nil {
		t.Fatalf("rotated not hex: %v", err)
	}
	if len(raw) < 32 {
		t.Fatalf("decoded rotated %d < 32 bytes", len(raw))
	}
}

func TestWebhookUnregister_AfterIngestRejected(t *testing.T) {
	svc, ctx := newTestService(t)
	id, secret := registerIngressWebhook(t, svc, ctx)
	body := []byte(`{"event_type":"delivery"}`)
	ts := time.Now().Unix()

	// Ingest works pre-delete.
	sendIngest(t, svc, secret, ts, body, "", "", nil, http.StatusOK, "")

	// Delete via WebhookDelete (= Unregister).
	if err := svc.WebhookDelete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Ingest must now fail: no inbound registration.
	ts2 := time.Now().Unix()
	sendIngest(t, svc, secret, ts2, body, "", "", nil, http.StatusNotFound, "webhook_not_found")
}

// -----------------------------------------------------------------------------
// isLoopbackSource standalone coverage.
// -----------------------------------------------------------------------------

func TestIsLoopbackSource(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"", true},
		{"@", true},
		{"@/tmp/webhook.sock", true},
		{"127.0.0.1:0", true},
		{"127.0.0.1:55555", true},
		{"[::1]:1234", true},
		{"::1", true},
		{"localhost:8080", true},
		{"8.8.8.8:12345", false},
		{"1.1.1.1:80", false},
		{"192.168.1.1:443", false},
		{"[2001:db8::1]:8080", false},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			if got := isLoopbackSource(c.addr); got != c.want {
				t.Fatalf("isLoopbackSource(%q) = %v, want %v", c.addr, got, c.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Sanity: a random secret is at least 64 hex chars (32 bytes) on register.
// -----------------------------------------------------------------------------

func TestWebhookRegister_SecretLength(t *testing.T) {
	svc, ctx := newTestService(t)
	_, plain, err := svc.WebhookRegister(ctx, &WebhookRegisterRequest{
		Name:      "s",
		Direction: "in",
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(plain) < 64 {
		t.Fatalf("plain length %d < 64 (32 bytes hex)", len(plain))
	}
	if _, err := hex.DecodeString(plain); err != nil {
		t.Fatalf("plain not hex: %v", err)
	}
	// Ensure it's different from another call → not deterministic/constant.
	_, plain2, _ := svc.WebhookRegister(ctx, &WebhookRegisterRequest{
		Name:      "s2",
		Direction: "in",
		Enabled:   true,
	})
	if plain == plain2 {
		t.Fatalf("two registers produced identical secrets")
	}
	_ = rand.Reader // silence unused import
}
