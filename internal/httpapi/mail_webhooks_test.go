package httpapi

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
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"phantom-lancer/internal/config"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/logsampler"
	"phantom-lancer/internal/mail"
	"phantom-lancer/internal/storage"
)

// ---------------------------------------------------------------------------
// HMAC helpers.
// ---------------------------------------------------------------------------

// hmacSHA256 returns raw HMAC-SHA256(key, input).
func hmacSHA256(key, input string) []byte {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(input))
	return m.Sum(nil)
}

// genSecret produces a 64-hex-char (32-byte) random secret, suitable as the
// signing key for HMAC-SHA256.
func genSecret() []byte {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return []byte(hex.EncodeToString(buf))
}

// signWebhook produces the X-Mox-Signature header value (sha256=<hex>) over
// (timestampStr + "." + body) signed by secret.
func signWebhook(secret []byte, tsUnix int64, body []byte) string {
	ts := strconv.FormatInt(tsUnix, 10)
	input := ts + "." + string(body)
	return "sha256=" + hex.EncodeToString(hmacSHA256(string(secret), input))
}

// ---------------------------------------------------------------------------
// Test environment builder.
// ---------------------------------------------------------------------------

type webhookTestEnv struct {
	Srv        *Server
	Handler    http.Handler
	MailSvc    *mail.Service
	Secret     []byte
	WebhookID  string
}

func newWebhookTestEnv(t *testing.T) *webhookTestEnv {
	t.Helper()
	ctx := context.Background()

	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	_ = os.MkdirAll(dataDir, 0o755)

	store, err := storage.Open(ctx, filepath.Join(tmpDir, "p.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Mail mox root skeleton (prevents NPEs in mail.Service.Ensure).
	moxRoot := filepath.Join(dataDir, "mail", "mox")
	_ = os.MkdirAll(filepath.Join(moxRoot, "config"), 0o755)
	_ = os.WriteFile(filepath.Join(moxRoot, "config", "mox.conf"),
		[]byte("Hostname: mx.test\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(moxRoot, "bin"), 0o755)
	_ = os.MkdirAll(filepath.Join(moxRoot, "data"), 0o755)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailSvc := mail.NewService(store, events.NewHub(), dataDir, logger)
	if err := mailSvc.Ensure(ctx); err != nil {
		t.Fatalf("mailSvc.Ensure: %v", err)
	}

	// Register an inbound webhook and capture its one-time plaintext secret.
	reg, secretPlain, err := mailSvc.WebhookRegister(ctx, &mail.WebhookRegisterRequest{
		Name:      "test-ingress",
		Direction: "in",
		Enabled:   true,
		EventMask: []string{"*"},
	})
	if err != nil {
		t.Fatalf("WebhookRegister: %v", err)
	}

	cfg := config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: dataDir,
		DBPath:  filepath.Join(tmpDir, "p.db"),
	}
	srv := &Server{
		cfg:              cfg,
		store:            store,
		hub:              events.NewHub(),
		log:              logger,
		startedAt:        time.Now().UTC().Format(time.RFC3339Nano),
		dataDir:          cfg.DataDir,
		logins:           newLoginBackoff(5),
		gatewayOAuth:     newCodexGatewayOAuthSessions(10 * time.Minute),
		privateUnlocks:   newLoginBackoff(5),
		updateConfirms:   newLoginBackoff(5),
		privateImages:    newPrivateImageAccess(),
		telemetrySampler: &logsampler.Sampler{Always: true},
		mail:             mailSvc,
	}

	return &webhookTestEnv{
		Srv:       srv,
		Handler:   srv.Handler(),
		MailSvc:   mailSvc,
		Secret:    []byte(secretPlain),
		WebhookID: reg.ID,
	}
}

// sendIngest sends a webhook POST request and returns the HTTP status code
// plus parsed JSON response.
func sendIngest(t *testing.T, handler http.Handler, secret []byte, tsUnix int64, body []byte,
	remoteAddr string, headers map[string]string) (int, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/mail/hooks/in", bytes.NewReader(body))
	req.RemoteAddr = remoteAddr

	// Timestamp + signature (only when a secret is supplied).
	if len(secret) > 0 {
		ts := strconv.FormatInt(tsUnix, 10)
		sig := signWebhook(secret, tsUnix, body)
		req.Header.Set("X-Mox-Timestamp", ts)
		req.Header.Set("X-Mox-Signature", sig)
	}

	// Extra headers override any defaults set above.
	for k, v := range headers {
		if k == "X-Mox-Timestamp" {
			req.Header.Del("X-Mox-Timestamp")
		}
		if k == "X-Mox-Signature" {
			req.Header.Del("X-Mox-Signature")
		}
		req.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	respBody := map[string]any{}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &respBody)
	}
	return w.Code, respBody
}

// errCode extracts the nested `error.code` field from the JSON envelope used
// by writeError (the canonical error shape in this package).
func errCode(body map[string]any) string {
	if body == nil {
		return ""
	}
	if e, ok := body["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Webhook ingress: table-driven tests.
// ---------------------------------------------------------------------------

type webhookTestCase struct {
	Name           string
	SecretOverride []byte
	RemoteAddr     string
	Body           []byte
	WantStatus     int
	Headers        map[string]string
	WantErrCode    string
	TSOffset       int64 // relative to wall time
	ExplicitTs     *string
	BodyFactory    func(baseBody []byte) []byte
	Prepare        func(t *testing.T, env *webhookTestEnv) map[string]any
}

func TestWebhookIngest(t *testing.T) {
	env := newWebhookTestEnv(t)
	baseBody := []byte(`{"event_type":"delivery.new","recipient":"a@b"}`)
	now := time.Now().Unix()

	cases := []webhookTestCase{
		// (a) Valid happy path.
		{
			Name:       "a_Valid_200",
			RemoteAddr: "127.0.0.1:12345",
			Body:       bytes.Repeat([]byte("A"), 100),
			TSOffset:   0,
			WantStatus: http.StatusOK,
		},
		// (b) Single bit flip in hex sig → hmac_invalid.
		{
			Name:       "b_InvalidSignature_bitFlip",
			RemoteAddr: "127.0.0.1:12345",
			TSOffset:   0,
			WantStatus: http.StatusUnauthorized,
			WantErrCode: "hmac_invalid",
			Headers: map[string]string{
				"X-Mox-Signature": "sha256=" + hex.EncodeToString(
					bytes.Repeat([]byte{0xff}, 32)),
			},
			Prepare: func(t *testing.T, env *webhookTestEnv) map[string]any {
				// We must pre-populate the X-Mox-Timestamp header too since
				// we're overriding the signature header.
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				return map[string]any{"ts": ts}
			},
		},
		// (c) Wrong secret → hmac_invalid (sig valid length, valid hex, cryptographically wrong).
		{
			Name:           "c_WrongSecret_401",
			SecretOverride: genSecret(),
			RemoteAddr:     "127.0.0.1:12345",
			Body:           baseBody,
			TSOffset:       0,
			WantStatus:     http.StatusUnauthorized,
			WantErrCode:    "hmac_invalid",
		},
		// (d) Signature not valid hex → hmac_invalid.
		{
			Name:       "d_SignatureNotHex_401",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusUnauthorized,
			WantErrCode: "hmac_invalid",
			Headers: map[string]string{
				"X-Mox-Signature": "sha256=ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
			},
			Prepare: func(t *testing.T, env *webhookTestEnv) map[string]any {
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				return map[string]any{"ts": ts}
			},
		},
		// (e) Missing timestamp → bad_timestamp.
		{
			Name:       "e_MissingTimestamp_400",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			WantStatus: http.StatusBadRequest,
			WantErrCode: "bad_timestamp",
			Headers: map[string]string{
				"X-Mox-Timestamp": "",
				"X-Mox-Signature": "sha256=0000000000000000000000000000000000000000000000000000000000000000",
			},
		},
		// (f) ts = now + 1000s → bad_timestamp (skew > 900).
		{
			Name:       "f_Future_1000s_400",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   1000,
			WantStatus: http.StatusBadRequest,
			WantErrCode: "bad_timestamp",
		},
		// (g) ts = now - 2000s → bad_timestamp (expired).
		{
			Name:       "g_Past_2000s_400",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   -2000,
			WantStatus: http.StatusBadRequest,
			WantErrCode: "bad_timestamp",
		},
		// (h) ts = now - 899s → 200 (within window).
		{
			Name:       "h_Boundary_899s_OK",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   -899,
			WantStatus: http.StatusOK,
		},
		// (i) ts = now - 901s → 400 (just outside window → expired).
		{
			Name:       "i_Boundary_901s_EXPIRED",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   -901,
			WantStatus: http.StatusBadRequest,
			WantErrCode: "bad_timestamp",
		},
		// (j) External source → source_blocked.
		{
			Name:       "j_SrcExternal_8_8_8_8",
			RemoteAddr: "8.8.8.8:12345",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusForbidden,
			WantErrCode: "source_blocked",
		},
		// (k) IPv6 loopback → 200.
		{
			Name:       "k_SrcIPv6Loopback_OK",
			RemoteAddr: "[::1]:1234",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusOK,
		},
		// (l) 127.0.0.1:54321 → 200.
		{
			Name:       "l_Src127_0_0_1_OK",
			RemoteAddr: "127.0.0.1:54321",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusOK,
		},
		// (m) 1.1 MB body → too_large (HTTP layer catches via LimitReader).
		{
			Name:       "m_BodyTooLarge_1_1MB",
			RemoteAddr: "127.0.0.1:12345",
			TSOffset:   0,
			WantStatus: http.StatusRequestEntityTooLarge,
			WantErrCode: "too_large",
			BodyFactory: func(_ []byte) []byte {
				return bytes.Repeat([]byte("x"), 1_100_000)
			},
		},
		// (n) Exactly 1 MiB body → 200 (allowed).
		{
			Name:       "n_BodyExact1MB_OK",
			RemoteAddr: "127.0.0.1:12345",
			TSOffset:   0,
			WantStatus: http.StatusOK,
			BodyFactory: func(_ []byte) []byte {
				return bytes.Repeat([]byte("A"), 1<<20)
			},
		},
		// (o) 1 MiB + 1 → too_large.
		{
			Name:       "o_Body1MBPlus1_413",
			RemoteAddr: "127.0.0.1:12345",
			TSOffset:   0,
			WantStatus: http.StatusRequestEntityTooLarge,
			WantErrCode: "too_large",
			BodyFactory: func(_ []byte) []byte {
				return bytes.Repeat([]byte("B"), 1<<20+1)
			},
		},
		// (p) Case-sensitive sig prefix: SHA256=... → hmac_invalid (malformed → treated as invalid).
		{
			Name:       "p_SigPrefixCase_Uppercase_401",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusUnauthorized,
			WantErrCode: "hmac_invalid",
			Headers: map[string]string{
				// Cryptographically valid sig but wrong case prefix.
				"X-Mox-Signature": func() string {
					ts := strconv.FormatInt(now, 10)
					input := ts + "." + string(baseBody)
					raw := hmacSHA256(string(env.Secret), input)
					return "SHA256=" + hex.EncodeToString(raw)
				}(),
				"X-Mox-Timestamp": strconv.FormatInt(now, 10),
			},
		},
		// (q) Sig computed for body A but body B sent → hmac_invalid (tamper proof).
		{
			Name:       "q_SigBodySwap_401",
			RemoteAddr: "127.0.0.1:12345",
			Body:       []byte(`{"event_type":"delivery.new","amount":1000}`),
			TSOffset:   0,
			WantStatus: http.StatusUnauthorized,
			WantErrCode: "hmac_invalid",
			Headers: map[string]string{
				// Sign the OLD body, not the Body above.
				"X-Mox-Signature": signWebhook(env.Secret, now, baseBody),
				"X-Mox-Timestamp": strconv.FormatInt(now, 10),
			},
		},
		// (r) RotateSecret — old sig → hmac_invalid; new sig → 200.
		{
			Name:       "r_RotateSecret_old401_new200",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusUnauthorized, // first request with OLD secret rejects
			WantErrCode: "hmac_invalid",
			Prepare: func(t *testing.T, env *webhookTestEnv) map[string]any {
				newSecret, err := env.MailSvc.WebhookRotateSecret(context.Background(), env.WebhookID)
				if err != nil {
					t.Fatalf("rotate: %v", err)
				}
				return map[string]any{"newSecret": newSecret}
			},
		},
		// (t) Unregister → subsequent ingest 403 source_blocked (because no
		// registration exists → "no registration" matches source_blocked).
		{
			Name:       "t_UnregisterThenReject_403",
			RemoteAddr: "127.0.0.1:12345",
			Body:       baseBody,
			TSOffset:   0,
			WantStatus: http.StatusForbidden,
			WantErrCode: "source_blocked",
			Prepare: func(t *testing.T, env *webhookTestEnv) map[string]any {
				if err := env.MailSvc.WebhookDelete(context.Background(), env.WebhookID); err != nil {
					t.Fatalf("delete: %v", err)
				}
				return nil
			},
		},
	}

	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			localEnv := env
			// Cases that mutate the registration need a fresh env to avoid
			// leaking state into sibling subtests.
			switch tc.Name {
			case "r_RotateSecret_old401_new200", "t_UnregisterThenReject_403",
				"q_SigBodySwap_401", "p_SigPrefixCase_Uppercase_401",
				"b_InvalidSignature_bitFlip", "d_SignatureNotHex_401":
				localEnv = newWebhookTestEnv(t)
			}

			// Build payload.
			payload := baseBody
			if tc.Body != nil {
				payload = tc.Body
			}
			if tc.BodyFactory != nil {
				payload = tc.BodyFactory(payload)
			}

			// Merge headers.
			hdrs := map[string]string{}
			for k, v := range tc.Headers {
				hdrs[k] = v
			}
			if tc.ExplicitTs != nil {
				hdrs["X-Mox-Timestamp"] = *tc.ExplicitTs
			}

			// Run prepare hook.
			var prepOut map[string]any
			if tc.Prepare != nil {
				prepOut = tc.Prepare(t, localEnv)
			}

			// Populate X-Mox-Timestamp from prepare (for signature override cases).
			if prepOut != nil {
				if ts, ok := prepOut["ts"].(string); ok {
					hdrs["X-Mox-Timestamp"] = ts
				}
			}

			// Choose secret.
			secret := localEnv.Secret
			if tc.SecretOverride != nil {
				secret = tc.SecretOverride
			}

			// --- Rotate: first request with OLD secret → then with NEW ---
			if tc.Name == "r_RotateSecret_old401_new200" {
				newSecRaw, _ := prepOut["newSecret"].(string)
				if newSecRaw == "" {
					t.Fatal("rotate: new secret missing from prepare output")
				}
				rotNow := time.Now().Unix()
				code, body := sendIngest(t, localEnv.Handler, secret, rotNow+tc.TSOffset, payload, tc.RemoteAddr, hdrs)
				if code != tc.WantStatus {
					t.Fatalf("rotate[old] status=%d want=%d body=%v", code, tc.WantStatus, body)
				}
				if ec := errCode(body); ec != tc.WantErrCode {
					t.Fatalf("rotate[old] errCode=%q want=%q body=%v", ec, tc.WantErrCode, body)
				}
				// Second request with NEW secret → 200.
				newTs := time.Now().Unix()
				code2, body2 := sendIngest(t, localEnv.Handler, []byte(newSecRaw), newTs, payload, tc.RemoteAddr, nil)
				if code2 != http.StatusOK {
					t.Fatalf("rotate[new] status=%d want=200 body=%v", code2, body2)
				}
				return
			}

			ts := time.Now().Unix() + tc.TSOffset
			code, body := sendIngest(t, localEnv.Handler, secret, ts, payload, tc.RemoteAddr, hdrs)
			if code != tc.WantStatus {
				t.Fatalf("status=%d want=%d body=%v", code, tc.WantStatus, body)
			}
			if tc.WantErrCode != "" {
				if ec := errCode(body); ec != tc.WantErrCode {
					t.Fatalf("errCode=%q want=%q body=%v", ec, tc.WantErrCode, body)
				}
			}
		})
	}

	// --- (s) Concurrent 100 goroutines, deterministic 80 valid / 20 invalid. ---
	t.Run("s_Concurrent100_deterministic", func(t *testing.T) {
		localEnv := newWebhookTestEnv(t)
		const N = 100
		var (
			wg      sync.WaitGroup
			errOnce sync.Once
			first   string
		)
		setErr := func(s string) { errOnce.Do(func() { first = s }) }

		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// 80 valid, 20 invalid (i%5 == 0 → invalid).
				valid := (i % 5) != 0
				ts := time.Now().Unix()
				payload := []byte(fmt.Sprintf(`{"event_type":"delivery.new","seq":%d}`, i))
				hdrs := map[string]string{}
				secret := localEnv.Secret
				if !valid {
					hdrs["X-Mox-Signature"] = "sha256=" + hex.EncodeToString(bytes.Repeat([]byte{0x00}, 32))
					hdrs["X-Mox-Timestamp"] = strconv.FormatInt(ts, 10)
					// Don't re-sign (skip default signer → force empty secret).
					secret = nil
				}
				code, _ := sendIngest(t, localEnv.Handler, secret, ts, payload, "127.0.0.1:4242", hdrs)
				if valid && code != http.StatusOK {
					setErr(fmt.Sprintf("valid i=%d got %d", i, code))
				}
				if !valid && code != http.StatusUnauthorized {
					setErr(fmt.Sprintf("invalid i=%d got %d", i, code))
				}
			}(i)
		}
		wg.Wait()
		if first != "" {
			t.Fatalf("concurrent fail: %s", first)
		}
	})

	// --- Extra: crypto sanity on signWebhook — verify constant-time compare ---
	t.Run("Sanity_signWebhook_isDeterministic", func(t *testing.T) {
		sec := genSecret()
		body := []byte(`{"x":1}`)
		ts := int64(1700000000)
		a := signWebhook(sec, ts, body)
		b := signWebhook(sec, ts, body)
		if a != b {
			t.Fatalf("signWebhook non-deterministic: %q vs %q", a, b)
		}
		// Flipping one byte → different sig.
		body2 := []byte(`{"x":2}`)
		c := signWebhook(sec, ts, body2)
		if a == c {
			t.Fatal("signWebhook produced identical sig for different bodies")
		}
	})
}
