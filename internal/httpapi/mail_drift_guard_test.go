package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
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
	"phantom-lancer/internal/logsampler"
	"phantom-lancer/internal/mail"
	"phantom-lancer/internal/storage"
)

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func hashContent256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// Test setup helper.
// ---------------------------------------------------------------------------

type driftTestEnv struct {
	Srv           *Server
	Handler       http.Handler
	Store         *storage.Store
	MailSvc       *mail.Service
	SessionToken  string
	CSRFToken     string
	MoxRoot       string
	MoxConfigPath string
	TmpDir        string
}

func newDriftTestEnv(t *testing.T, driftedState bool) *driftTestEnv {
	t.Helper()
	ctx := context.Background()

	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	_ = os.MkdirAll(dataDir, 0o755)

	// --- storage + owner + session -----------------------------------------
	store, err := storage.Open(ctx, filepath.Join(tmpDir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pw, _ := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	owner, err := store.CreateOwner(ctx, "admin", string(pw))
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	sessionRaw, sessionHash, _ := auth.NewToken()
	csrfRaw, csrfHash, _ := auth.NewToken()
	expires := time.Now().UTC().Add(24 * time.Hour)
	if _, err := store.CreateSession(ctx, owner.ID, sessionHash, csrfHash, false, expires); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// --- runtime settings (prevents NPEs in middleware) --------------------
	rs, err := store.GetRuntimeSettings(ctx)
	if err != nil {
		t.Fatalf("runtime settings: %v", err)
	}
	rs.AllowedRoots = []string{tmpDir}
	_ = store.UpdateRuntimeSettings(ctx, rs)

	// --- mail.Service with real mox.conf on disk ---------------------------
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailSvc := mail.NewService(store, events.NewHub(), dataDir, logger)

	moxRoot := filepath.Join(dataDir, "mail", "mox")
	moxConfigDir := filepath.Join(moxRoot, "config")
	_ = os.MkdirAll(moxConfigDir, 0o755)
	moxConfigPath := filepath.Join(moxConfigDir, "mox.conf")
	seedBytes := []byte("Hostname: mx.test\nAdminAddress: admin@mx.test\nSMTPPort: 25\n")
	if err := os.WriteFile(moxConfigPath, seedBytes, 0o644); err != nil {
		t.Fatalf("seed mox.conf: %v", err)
	}
	_ = os.MkdirAll(filepath.Join(moxRoot, "bin"), 0o755)
	_ = os.MkdirAll(filepath.Join(moxRoot, "data"), 0o755)

	if err := mailSvc.Ensure(ctx); err != nil {
		t.Fatalf("mailSvc.Ensure: %v", err)
	}

	if driftedState {
		if err := os.WriteFile(moxConfigPath,
			[]byte("OPERATOR HAND EDIT — drift me!\n"+string(seedBytes)), 0o644); err != nil {
			t.Fatalf("mutate for drift: %v", err)
		}
		if drifted, _, err := mailSvc.DriftDetector().Refresh(); err != nil {
			t.Fatalf("refresh drift: %v", err)
		} else if !drifted {
			t.Fatalf("precondition: Refresh did not flag drift")
		}
	}

	cfg := config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: dataDir,
		DBPath:  filepath.Join(tmpDir, "phantom-lancer.db"),
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &Server{
		cfg:              cfg,
		store:            store,
		hub:              events.NewHub(),
		log:              log,
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

	return &driftTestEnv{
		Srv:           srv,
		Handler:       srv.Handler(),
		Store:         store,
		MailSvc:       mailSvc,
		SessionToken:  sessionRaw,
		CSRFToken:     csrfRaw,
		MoxRoot:       moxRoot,
		MoxConfigPath: moxConfigPath,
		TmpDir:        tmpDir,
	}
}

func (e *driftTestEnv) do(t *testing.T, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := newJSONRequest(t, method, path, body, e.SessionToken, e.CSRFToken)
	w := httptest.NewRecorder()
	e.Handler.ServeHTTP(w, r)
	var m map[string]any
	ct := w.Header().Get("Content-Type")
	if strings.Contains(ct, "json") && w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &m)
	}
	return w, m
}

// errField extracts the nested `error.{code,message}` envelope that writeError
// produces, so assertions can read fields regardless of the outer wrapper.
func errField(body map[string]any, key string) string {
	if body == nil {
		return ""
	}
	if err, ok := body["error"].(map[string]any); ok {
		if v, ok := err[key].(string); ok {
			return v
		}
	}
	// Fallback: check top-level (back-compat with any unwrapped responses).
	if v, ok := body[key].(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// A) Write APIs return 409 when config drifted.
// ---------------------------------------------------------------------------

type writeAPICase struct {
	Name   string
	Method string
	Path   string
	Body   any
}

var writeAPITable = []writeAPICase{
	{"Domains POST", http.MethodPost, "/api/mail/domains", map[string]any{"domain": "new.test"}},
	{"Domains PUT", http.MethodPut, "/api/mail/domains/dom_notexist", map[string]any{"domain": "updated.test"}},
	{"Domains DELETE", http.MethodDelete, "/api/mail/domains/dom_notexist", nil},
	{"Accounts POST", http.MethodPost, "/api/mail/accounts", map[string]any{"email": "a@mx.test", "password": "pw123456"}},
	{"Accounts PATCH", http.MethodPatch, "/api/mail/accounts/acc_notexist", map[string]any{"display_name": "X"}},
	{"Accounts DELETE", http.MethodDelete, "/api/mail/accounts/acc_notexist", nil},
	{"Aliases POST", http.MethodPost, "/api/mail/aliases", map[string]any{"alias_addr": "info@mx.test", "recipients": []string{"a@mx.test"}}},
	{"Aliases PATCH", http.MethodPatch, "/api/mail/aliases/ali_notexist", map[string]any{"enabled": false}},
	{"Aliases DELETE", http.MethodDelete, "/api/mail/aliases/ali_notexist", nil},
	{"Certificates POST", http.MethodPost, "/api/mail/certificates", map[string]any{"domains": []string{"mx.test"}, "method": "acme_http01"}},
	{"Certificates DELETE", http.MethodDelete, "/api/mail/certificates/cert_notexist", nil},
	{"Deliveries retry", http.MethodPost, "/api/mail/deliveries/del_notexist/retry", map[string]any{}},
	{"Deliveries DELETE", http.MethodDelete, "/api/mail/deliveries/del_notexist", nil},
	{"Deliveries prune", http.MethodPost, "/api/mail/deliveries/prune", map[string]any{"older_than_days": 30}},
	{"Imports POST", http.MethodPost, "/api/mail/imports", map[string]any{"host": "imap.old.tld", "account": "a@old.tld"}},
	{"Imports DELETE", http.MethodDelete, "/api/mail/imports/imp_notexist", nil},
	{"Domain enable", http.MethodPost, "/api/mail/domains/dom_notexist/enable", nil},
	{"Domain disable", http.MethodPost, "/api/mail/domains/dom_notexist/disable", nil},
	{"Config apply", http.MethodPost, "/api/mail/config/apply", map[string]any{}},
	{"Setup import", http.MethodPost, "/api/mail/setup/import", map[string]any{}},
	{"Account reset password", http.MethodPost, "/api/mail/accounts/acc_notexist/reset-password", map[string]any{"password": "newpw1234"}},
	{"Account disable", http.MethodPost, "/api/mail/accounts/acc_notexist/disable", nil},
}

func TestDrift_WriteAPIReturn409(t *testing.T) {
	env := newDriftTestEnv(t, true)
	if !env.MailSvc.Drifted() {
		t.Fatal("precondition: mail must be drifted")
	}
	for _, tc := range writeAPITable {
		t.Run(tc.Name, func(t *testing.T) {
			w, body := env.do(t, tc.Method, tc.Path, tc.Body)
			if w.Code != http.StatusConflict {
				t.Errorf("%s %s → code=%d want 409. body=%s",
					tc.Method, tc.Path, w.Code, w.Body.String())
				return
			}
			code := errField(body, "code")
			if code != "config_drifted" {
				t.Errorf("code=%q want config_drifted. body=%+v", code, body)
			}
			msg := errField(body, "message")
			if msg == "" {
				t.Errorf("expected non-empty message, body=%+v", body)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// B) Read-only APIs are unaffected by drift.
// ---------------------------------------------------------------------------

func TestDrift_ReadOnlyAPIs_Unaffected(t *testing.T) {
	env := newDriftTestEnv(t, true)
	if !env.MailSvc.Drifted() {
		t.Fatal("precondition: drift must be on")
	}
	readonly := []struct {
		Name string
		Path string
	}{
		{"domains list", "/api/mail/domains"},
		{"accounts list", "/api/mail/accounts"},
		{"aliases list", "/api/mail/aliases"},
		{"certificates list", "/api/mail/certificates"},
		{"deliveries list", "/api/mail/deliveries"},
		{"imports list", "/api/mail/imports"},
	}
	for _, tc := range readonly {
		t.Run(tc.Name, func(t *testing.T) {
			r := newJSONRequest(t, http.MethodGet, tc.Path, nil, env.SessionToken, env.CSRFToken)
			w := httptest.NewRecorder()
			env.Handler.ServeHTTP(w, r)
			// Assertion: must NOT return 409 drift guard.
			if w.Code == http.StatusConflict {
				t.Fatalf("GET %s returned 409 drift; readonly endpoints must skip drift check. body=%s",
					tc.Path, w.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// C) Resolve drift → write APIs work again.
// ---------------------------------------------------------------------------

func TestResolveDrift_After_UnblocksWrites(t *testing.T) {
	env := newDriftTestEnv(t, true)
	if !env.MailSvc.Drifted() {
		t.Fatal("precondition: drift must be on")
	}

	// 1. Confirm write endpoint is blocked (sanity).
	w, _ := env.do(t, http.MethodPost, "/api/mail/domains", map[string]any{"domain": "a.test"})
	if w.Code != http.StatusConflict {
		t.Fatalf("precondition check: code=%d want 409", w.Code)
	}

	// 2. Resolve drift (simulate overwrite_from_db action): rewrite disk to
	//    DB-canonical content, then call SetSynced with the new hash.
	newContent := []byte("Hostname: mx.test\nAdminAddress: admin@mx.test\n# re-synced\n")
	if err := os.WriteFile(env.MoxConfigPath, newContent, 0o644); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	newHash := hashContent256(newContent)
	env.MailSvc.DriftDetector().SetSynced(newHash)
	if env.MailSvc.Drifted() {
		t.Fatalf("post-resolve Drifted() should be false")
	}

	// 3. Same write endpoint now returns non-409.
	w2, body2 := env.do(t, http.MethodPost, "/api/mail/domains", map[string]any{"domain": "a.test"})
	if w2.Code == http.StatusConflict {
		t.Fatalf("resolve drift did not unblock writes; code=%d body=%s",
			w2.Code, w2.Body.String())
	}
	if code := errField(body2, "code"); code == "config_drifted" {
		t.Fatalf("body still shows config_drifted after resolve: %+v", body2)
	}
}
