package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/codexgateway"
	"phantom-lancer/internal/config"
	"phantom-lancer/internal/dockercontrol"
	"phantom-lancer/internal/events"
	imagegen "phantom-lancer/internal/images"
	logcenter "phantom-lancer/internal/logs"
	"phantom-lancer/internal/logsampler"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/selfupdate"
	"phantom-lancer/internal/storage"
	"phantom-lancer/internal/v2ray"
	"phantom-lancer/internal/workspaces"
)

const (
	sessionCookie = "pl_session"
	csrfCookie    = "pl_csrf"
	sessionTTL    = 7 * 24 * time.Hour

	// slowRequestThreshold is the wall-time duration after which a request is
	// logged at WARN level with latency included. The default is deliberately
	// higher than the typical 1s "slow" bar because the UI makes heavy use
	// of long-lived handlers: SSE streams (/api/events/stream), large image
	// asset downloads, codex thread event long-polls, and system-update
	// streaming downloads. Treating those as WARN would make normal
	// operation look like incidents.
	slowRequestThreshold = 15 * time.Second
)

// isSlowExemptPath reports whether a request path should be ignored by the
// slow-request WARN detector. It covers handlers that are legitimately
// long-lived by design and whose latency has no operational meaning.
// The list is intentionally small and literal; if a new streaming endpoint
// is added, add it here explicitly rather than broad prefix matching.
func isSlowExemptPath(path string) bool {
	switch {
	case path == "/api/events/stream":
		return true
	case strings.HasPrefix(path, "/api/images/library/assets/") && strings.HasSuffix(path, "/content"):
		return true
	// Legacy image asset download route predates the /library/assets
	// hierarchy; it dispatches through the same serveImageAssetContent
	// backend and can legitimately serve multi-gigabyte S3 blobs, so it
	// must be exempt too.
	case strings.HasPrefix(path, "/api/images/assets/"):
		return true
	case strings.HasPrefix(path, "/api/codex/threads/") && strings.HasSuffix(path, "/events"):
		return true
	case strings.HasPrefix(path, "/api/system/update/jobs/") && strings.HasSuffix(path, "/stream"):
		return true
	}
	return false
}

type Server struct {
	cfg            config.Config
	store          *storage.Store
	hub            *events.Hub
	codexGateway   *codexgateway.Service
	codex          *codexclient.Service
	v2ray          *v2ray.Service
	images         *imagegen.Service
	docker         *dockercontrol.Service
	logs           *logcenter.Service
	updates        *selfupdate.Service
	staticFS       fs.FS
	log            *slog.Logger
	logins         *loginBackoff
	gatewayOAuth   *codexGatewayOAuthSessions
	privateUnlocks *loginBackoff
	updateConfirms *loginBackoff
	privateImages  *privateImageAccess
	httpSrv        httpServerManager

	// telemetrySampler gates hot-path WARN/ERROR telemetry from the
	// requestTelemetry middleware so repeated 5xx bursts, scanner-driven
	// 4xx floods, or slow SSE stream endpoints never drown service logs.
	telemetrySampler *logsampler.Sampler

	// startedAt is recorded once at New() time so /api/system/status can
	// report uptime without touching any global. Stored as RFC3339Nano so
	// the string also round-trips back through time.Parse.
	startedAt string
	// dataDir is the snapshot of cfg.DataDir used by status handlers; we
	// cache the string on the struct to avoid a cfg pointer chase from the
	// hot-path poll endpoint.
	dataDir string
}

// httpServerManager is the minimum interface the httpapi handlers need from
// the server lifecycle manager.  It is satisfied by *httpserver.Manager and
// keeps the circular-dependency-free wiring between main and httpapi simple
// (main creates api then injects the manager via SetHTTPServerManager).
type httpServerManager interface {
	Addr() string
	SwapAddr(newAddr string) error
}

type sessionContext struct {
	Session storage.Session
}

func New(cfg config.Config, store *storage.Store, hub *events.Hub, codexGatewaySvc *codexgateway.Service, codexSvc *codexclient.Service, v2raySvc *v2ray.Service, imagesSvc *imagegen.Service, dockerSvc *dockercontrol.Service, logsSvc *logcenter.Service, updateSvc *selfupdate.Service, staticFS fs.FS, logger *slog.Logger) (*Server, error) {
	return &Server{
		cfg:              cfg,
		store:            store,
		hub:              hub,
		codexGateway:     codexGatewaySvc,
		codex:            codexSvc,
		v2ray:            v2raySvc,
		images:           imagesSvc,
		docker:           dockerSvc,
		logs:             logsSvc,
		updates:          updateSvc,
		staticFS:         staticFS,
		log:              logger,
		logins:           newLoginBackoff(cfg.LoginFailureThreshold),
		gatewayOAuth:     newCodexGatewayOAuthSessions(10 * time.Minute),
		privateUnlocks:   newLoginBackoff(cfg.LoginFailureThreshold),
		updateConfirms:   newLoginBackoff(cfg.LoginFailureThreshold),
		privateImages:    newPrivateImageAccess(),
		telemetrySampler: logsampler.New(2 * time.Second),
		startedAt:        time.Now().UTC().Format(time.RFC3339Nano),
		dataDir:          cfg.DataDir,
	}, nil
}

// SetHTTPServerManager wires in the lifecycle manager that owns the HTTP
// listener.  Called once from main() after both the API server and the
// manager have been constructed (they have a circular dependency – the
// manager needs the Handler, handlers need the manager for hot-swap).
func (s *Server) SetHTTPServerManager(m httpServerManager) {
	s.httpSrv = m
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/auth/bootstrap-status", s.handleBootstrapStatus)
	mux.HandleFunc("POST /api/auth/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)

	mux.HandleFunc("GET /api/dashboard/summary", s.handleDashboard)
	mux.HandleFunc("GET /api/codex-gateway/status", s.handleCodexGatewayStatus)
	mux.HandleFunc("GET /api/codex-gateway/settings", s.handleGetCodexGatewaySettings)
	mux.HandleFunc("PUT /api/codex-gateway/settings", s.handleUpdateCodexGatewaySettings)
	mux.HandleFunc("GET /api/codex-gateway/api-keys", s.handleListCodexGatewayAPIKeys)
	mux.HandleFunc("POST /api/codex-gateway/api-keys", s.handleCreateCodexGatewayAPIKey)
	mux.HandleFunc("POST /api/codex-gateway/api-keys/", s.handleCodexGatewayAPIKeySubroutes)
	mux.HandleFunc("PATCH /api/codex-gateway/api-keys/", s.handleCodexGatewayAPIKeySubroutes)
	mux.HandleFunc("DELETE /api/codex-gateway/api-keys/", s.handleCodexGatewayAPIKeySubroutes)
	mux.HandleFunc("GET /api/codex-gateway/accounts", s.handleListCodexGatewayAccounts)
	mux.HandleFunc("POST /api/codex-gateway/accounts", s.handleCreateCodexGatewayAccount)
	mux.HandleFunc("GET /api/codex-gateway/accounts/export", s.handleExportCodexGatewayAccounts)
	mux.HandleFunc("POST /api/codex-gateway/accounts/import", s.handleImportCodexGatewayAccounts)
	mux.HandleFunc("POST /api/codex-gateway/accounts/oauth/start", s.handleStartCodexGatewayOAuth)
	mux.HandleFunc("POST /api/codex-gateway/accounts/oauth/relay", s.handleRelayCodexGatewayOAuth)
	mux.HandleFunc("GET /api/codex-gateway/accounts/oauth/callback", s.handleCodexGatewayOAuthCallback)
	mux.HandleFunc("PATCH /api/codex-gateway/accounts/", s.handleCodexGatewayAccountSubroutes)
	mux.HandleFunc("DELETE /api/codex-gateway/accounts/", s.handleCodexGatewayAccountSubroutes)
	mux.HandleFunc("POST /api/codex-gateway/accounts/", s.handleCodexGatewayAccountSubroutes)
	mux.HandleFunc("GET /api/codex-gateway/models", s.handleCodexGatewayModels)
	mux.HandleFunc("POST /api/codex-gateway/models/refresh", s.handleRefreshCodexGatewayModels)
	mux.HandleFunc("GET /api/codex-gateway/request-logs", s.handleCodexGatewayRequestLogs)
	mux.HandleFunc("POST /api/codex-gateway/chat-test", s.handleCodexGatewayChatTest)
	s.registerCodexRoutes(mux)
	mux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	mux.HandleFunc("PATCH /api/notifications/", s.handleNotificationSubroutes)
	mux.HandleFunc("POST /api/notifications/archive-read", s.handleArchiveReadNotifications)
	mux.HandleFunc("GET /api/events/history", s.handleEventHistory)
	mux.HandleFunc("GET /api/events/stream", s.handleEventStream)
	mux.HandleFunc("GET /api/audit/events", s.handleAuditEvents)
	mux.HandleFunc("GET /api/system/version", s.handleSystemVersion)
	mux.HandleFunc("GET /api/system/status", s.handleSystemStatus)
	mux.HandleFunc("GET /api/system/update/status", s.handleSystemUpdateStatus)
	mux.HandleFunc("POST /api/system/update/check", s.handleSystemUpdateCheck)
	mux.HandleFunc("POST /api/system/update/apply", s.handleSystemUpdateApply)
	mux.HandleFunc("GET /api/system/update/jobs/", s.handleSystemUpdateJobSubroutes)
	mux.HandleFunc("POST /api/system/update/jobs/", s.handleSystemUpdateJobSubroutes)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/settings/listen-addr", s.handleSwapListenAddr)
	mux.HandleFunc("GET /api/logs/sources", s.handleListLogSources)
	mux.HandleFunc("GET /api/logs/sources/", s.handleLogSourceSubroutes)
	mux.HandleFunc("GET /api/images/status", s.handleImagesStatus)
	mux.HandleFunc("GET /api/images/settings", s.handleGetImagesSettings)
	mux.HandleFunc("PUT /api/images/settings", s.handleUpdateImagesSettings)
	mux.HandleFunc("GET /api/images/storage-settings", s.handleGetImageStorageSettings)
	mux.HandleFunc("PUT /api/images/storage-settings", s.handleUpdateImageStorageSettings)
	mux.HandleFunc("POST /api/images/storage-settings/test", s.handleTestImageStorageSettings)
	mux.HandleFunc("GET /api/object-storage/profiles", s.handleListObjectStorageProfiles)
	mux.HandleFunc("POST /api/object-storage/profiles", s.handleCreateObjectStorageProfile)
	mux.HandleFunc("GET /api/object-storage/profiles/", s.handleObjectStorageProfileSubroutes)
	mux.HandleFunc("PATCH /api/object-storage/profiles/", s.handleObjectStorageProfileSubroutes)
	mux.HandleFunc("POST /api/object-storage/profiles/", s.handleObjectStorageProfileSubroutes)
	mux.HandleFunc("DELETE /api/object-storage/profiles/", s.handleObjectStorageProfileSubroutes)
	mux.HandleFunc("GET /api/docker/status", s.handleDockerStatus)
	mux.HandleFunc("GET /api/docker/overview", s.handleDockerOverview)
	mux.HandleFunc("POST /api/docker/probe", s.handleDockerProbe)
	mux.HandleFunc("GET /api/docker/host/events", s.handleDockerHostEvents)
	mux.HandleFunc("GET /api/docker/control-status", s.handleDockerControlStatus)
	mux.HandleFunc("PATCH /api/docker/settings", s.handleDockerUpdateSettings)
	mux.HandleFunc("POST /api/docker/install", s.handleDockerInstall)
	mux.HandleFunc("POST /api/docker/daemon/", s.handleDockerDaemonControl)
	mux.HandleFunc("GET /api/docker/registry/status", s.handleDockerRegistryStatus)
	mux.HandleFunc("GET /api/docker/registry/settings", s.handleDockerRegistrySettings)
	mux.HandleFunc("PUT /api/docker/registry/settings", s.handleDockerRegistrySettings)
	mux.HandleFunc("GET /api/docker/registry/repositories", s.handleDockerRegistryRepositories)
	mux.HandleFunc("GET /api/docker/registry/repositories/", s.handleDockerRegistryRepositorySubroutes)
	mux.HandleFunc("DELETE /api/docker/registry/repositories/", s.handleDockerRegistryRepositorySubroutes)
	mux.HandleFunc("GET /api/docker/registry/credentials", s.handleDockerRegistryCredentials)
	mux.HandleFunc("POST /api/docker/registry/credentials", s.handleDockerRegistryCredentials)
	mux.HandleFunc("PATCH /api/docker/registry/credentials/", s.handleDockerRegistryCredentialSubroutes)
	mux.HandleFunc("POST /api/docker/registry/credentials/", s.handleDockerRegistryCredentialSubroutes)
	mux.HandleFunc("DELETE /api/docker/registry/credentials/", s.handleDockerRegistryCredentialSubroutes)
	mux.HandleFunc("POST /api/docker/registry/gc", s.handleDockerRegistryGC)
	mux.HandleFunc("GET /api/docker/containers", s.handleDockerListContainers)
	mux.HandleFunc("POST /api/docker/containers", s.handleDockerCreateContainer)
	mux.HandleFunc("POST /api/docker/containers/", s.handleDockerContainerSubroutes)
	mux.HandleFunc("GET /api/docker/containers/", s.handleDockerContainerSubroutes)
	mux.HandleFunc("DELETE /api/docker/containers/", s.handleDockerContainerSubroutes)
	mux.HandleFunc("GET /api/docker/images", s.handleDockerListImages)
	mux.HandleFunc("POST /api/docker/images/pull", s.handleDockerPullImage)
	mux.HandleFunc("DELETE /api/docker/images/", s.handleDockerRemoveImage)
	mux.HandleFunc("GET /api/docker/volumes", s.handleDockerListVolumes)
	mux.HandleFunc("GET /api/docker/networks", s.handleDockerListNetworks)
	mux.HandleFunc("GET /api/images/library/private/status", s.handleImagePrivateStatus)
	mux.HandleFunc("POST /api/images/library/private/unlock", s.handleUnlockImagePrivate)
	mux.HandleFunc("POST /api/images/library/private/lock", s.handleLockImagePrivate)
	mux.HandleFunc("GET /api/images/jobs", s.handleListImageJobs)
	mux.HandleFunc("POST /api/images/jobs", s.handleCreateImageJob)
	mux.HandleFunc("GET /api/images/jobs/", s.handleImageJobSubroutes)
	mux.HandleFunc("GET /api/images/library/assets", s.handleListImageLibraryAssets)
	mux.HandleFunc("POST /api/images/library/assets", s.handleUploadImageLibraryAsset)
	mux.HandleFunc("GET /api/images/library/assets/", s.handleImageLibraryAssetSubroutes)
	mux.HandleFunc("DELETE /api/images/library/assets/", s.handleImageLibraryAssetSubroutes)
	mux.HandleFunc("POST /api/images/library/assets/", s.handleImageLibraryAssetSubroutes)
	mux.HandleFunc("GET /api/images/assets/", s.handleImageAsset)
	mux.HandleFunc("GET /api/v2ray/status", s.handleV2RayStatus)
	mux.HandleFunc("GET /api/v2ray/settings", s.handleGetV2RaySettings)
	mux.HandleFunc("PUT /api/v2ray/settings", s.handleUpdateV2RaySettings)
	mux.HandleFunc("POST /api/v2ray/validate", s.handleValidateV2Ray)
	mux.HandleFunc("POST /api/v2ray/control", s.handleControlV2Ray)
	mux.HandleFunc("POST /api/v2ray/clients", s.handleCreateV2RayClient)
	mux.HandleFunc("GET /api/v2ray/clients/", s.handleV2RayClientSubroutes)
	mux.HandleFunc("PUT /api/v2ray/clients/", s.handleV2RayClientSubroutes)
	mux.HandleFunc("POST /api/v2ray/clients/", s.handleV2RayClientSubroutes)
	mux.HandleFunc("DELETE /api/v2ray/clients/", s.handleV2RayClientSubroutes)
	mux.HandleFunc("GET /v1/models", s.handleCodexGatewayPublicModels)
	mux.HandleFunc("GET /v1/models/", s.handleCodexGatewayPublicModel)
	mux.HandleFunc("POST /v1/chat/completions", s.handleCodexGatewayChatCompletions)
	mux.HandleFunc("POST /v1/responses", s.handleCodexGatewayResponses)
	mux.HandleFunc("GET /v2/", s.handleDockerRegistryNative)
	mux.HandleFunc("HEAD /v2/", s.handleDockerRegistryNative)
	mux.HandleFunc("POST /v2/", s.handleDockerRegistryNative)
	mux.HandleFunc("PATCH /v2/", s.handleDockerRegistryNative)
	mux.HandleFunc("PUT /v2/", s.handleDockerRegistryNative)
	mux.HandleFunc("DELETE /v2/", s.handleDockerRegistryNative)

	mux.Handle("/", s.staticHandler())
	return s.requestTelemetry(s.recover(mux))
}

// requestTelemetry records access telemetry for anomalies and
// additionally records slow requests and server errors (5xx).
//
// Per AGENTS, successful HTTP requests (2xx/3xx/1xx) emit no service
// log — they exist only in the structured request metric counters
// kept by logsampler / the middleware sampler.
//
// Logging matrix:
//   - 5xx → ERROR, with status + latency + method + path + ip + user_agent
//   - 4xx (except 404/401/403/429 noise at DEBUG) → WARN, sampled
//   - slow request (any status, non-exempt path) → WARN, sampled
//
// The URL path recorded is from safelog.RequestPathLabel, which uses
// url.URL.EscapedPath() and unconditionally drops query and fragment so
// query-string secrets never reach the service log.
func (s *Server) requestTelemetry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			latency := time.Since(start)
			attrs := []any{
				"method", r.Method,
				"path", safelog.RequestPathLabel(r.URL),
				"status", rec.status,
				"bytes", rec.bytes,
				"latency_ms", latency.Milliseconds(),
				"ip", remoteIP(r),
				"ua", shortUA(r.UserAgent()),
			}
			switch {
			case rec.status >= 500:
				if s.telemetrySampler.Allow(fmt.Sprintf("%s|%s|%d", r.Method, safelog.RequestPathLabel(r.URL), rec.status)) {
					s.log.ErrorContext(r.Context(), "http server error", attrs...)

				}
			case rec.status == 404 || rec.status == 401 || rec.status == 403 || rec.status == 429:
				s.log.DebugContext(r.Context(), "http client noise", attrs...)
			case rec.status >= 400:
				if s.telemetrySampler.Allow(fmt.Sprintf("%s|%s|%d", r.Method, safelog.RequestPathLabel(r.URL), rec.status)) {
					s.log.WarnContext(r.Context(), "http client error", attrs...)

				}
			case latency >= slowRequestThreshold && !isSlowExemptPath(r.URL.Path):
				// Exempt known long-lived handlers (SSE event streams,
				// large binary asset downloads, codex thread event
				// long-poll, and system update job progress streams)
				// from the slow-request WARN: steady-state streaming
				// is not an incident. See isSlowExemptPath for the
				// list of exempt patterns.
				if s.telemetrySampler.Allow(fmt.Sprintf("%s|%s|%d", r.Method, safelog.RequestPathLabel(r.URL), rec.status)) {
					s.log.WarnContext(r.Context(), "http slow request", attrs...)

				}
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// statusRecorder wraps an http.ResponseWriter to capture the response status
// code and number of bytes written. Flush / Hijack are forwarded when the
// underlying writer supports them.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// remoteIP returns the client IP, preferring X-Forwarded-For (first entry)
// and X-Real-IP over the connection's RemoteAddr. The result is always safe
// to log: valid IPs are returned verbatim (naturally bounded in length),
// while malformed / injected header values are passed through safelog.Text
// with a 64-rune cap per AGENTS.md "长度做上限裁剪" constraint.
func remoteIP(r *http.Request) string {
	if xff := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); xff != "" {
		if net.ParseIP(xff) != nil {
			return xff
		}
		return safelog.Text(xff, 64)
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if net.ParseIP(xr) != nil {
			return xr
		}
		return safelog.Text(xr, 64)
	}
	if r.RemoteAddr == "" {
		return "-"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return safelog.Text(r.RemoteAddr, 64)
	}
	return host
}

// shortUA truncates User-Agent strings to 120 runes for logging.
func shortUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "-"
	}
	runes := []rune(ua)
	if len(runes) > 120 {
		return string(runes[:120]) + "..."
	}
	return ua
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	exists, err := s.store.OwnerExists(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ownerConfigured": exists})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	exists, err := s.store.OwnerExists(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "owner_exists", "管理员账号已存在")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "用户名不能为空")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	owner, err := s.store.CreateOwner(r.Context(), req.Username, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "owner.bootstrap",
		Summary:   "已创建管理员账号",
		Payload:   map[string]any{"username": owner.Username},
	})
	s.createSessionResponse(w, r, owner)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Trusted  bool   `json:"trusted"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	ip := clientIP(r)
	if decision := s.logins.Check(username, ip, time.Now()); decision.Limited {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "auth.login.rate_limited",
			RiskLevel: "medium",
			Summary:   "登录请求已被限流",
			Payload: map[string]any{
				"username":     username,
				"ip":           ip,
				"dimension":    decision.Dimension,
				"backoffUntil": decision.BackoffUntil.UTC().Format(time.RFC3339Nano),
			},
		})
		writeError(w, http.StatusTooManyRequests, "auth_backoff", "登录失败次数过多，请稍后再试")
		return
	}

	owner, err := s.store.GetOwnerByUsername(r.Context(), username)
	if err != nil || !auth.VerifyPassword(owner.PasswordHash, req.Password) {
		events := s.logins.RecordFailure(username, ip, time.Now())
		for _, event := range events {
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "auth.login.backoff_started",
				RiskLevel: "medium",
				Summary:   "登录失败触发退避",
				Payload: map[string]any{
					"username":     username,
					"ip":           ip,
					"dimension":    event.Dimension,
					"backoffUntil": event.BackoffUntil.UTC().Format(time.RFC3339Nano),
					"durationSec":  int(event.Duration.Seconds()),
				},
			})
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
		return
	}
	s.logins.RecordSuccess(username)
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "auth.login",
		Summary:   "已登录",
		Payload:   map[string]any{"trusted": req.Trusted, "ip": ip},
	})
	s.createSessionResponse(w, r, owner)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	_ = s.store.RevokeSession(r.Context(), ctx.Session.ID)
	secure := s.cookieSecure(r.Context())
	clearCookie(w, sessionCookie, secure)
	clearCookie(w, csrfCookie, secure)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id":        ctx.Session.ID,
			"trusted":   ctx.Session.Trusted,
			"expiresAt": ctx.Session.ExpiresAt.Format(time.RFC3339Nano),
		},
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	started := time.Now()
	statusCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	audit, _ := s.store.ListAudit(statusCtx, 8)
	codexGatewayStatus, err := s.codexGateway.Status(statusCtx)
	if err != nil {
		codexGatewayStatus = codexgateway.Status{LastError: err.Error()}
	}
	codexStatus, _ := s.codex.Status(statusCtx)
	writeJSON(w, http.StatusOK, map[string]any{
		"codexGateway":   codexGatewayStatus,
		"codex":          codexStatus,
		"images":         s.images.Status(statusCtx),
		"v2ray":          s.v2ray.Status(statusCtx),
		"recentActivity": audit,
	})
	if err := statusCtx.Err(); errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("dashboard summary status timeout", "summary", codexclient.Redact(err.Error(), 120), "durationMs", time.Since(started).Milliseconds())
	}
}

func (s *Server) handleEventHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	scopeID := r.URL.Query().Get("id")
	after := parseInt64(r.URL.Query().Get("after"))
	if scope == "" || scopeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "scope 和 id 不能为空")
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	items, err := s.store.ListEvents(r.Context(), scope, scopeID, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	scopeID := r.URL.Query().Get("id")
	if scope == "" || scopeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "scope 和 id 不能为空")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "当前环境不支持事件流")
		return
	}

	after := parseInt64(r.Header.Get("Last-Event-ID"))
	if queryAfter := r.URL.Query().Get("after"); queryAfter != "" {
		after = parseInt64(queryAfter)
	}
	backlog, _ := s.store.ListEvents(r.Context(), scope, scopeID, after, 500)
	for _, event := range backlog {
		writeSSE(w, event)
	}
	flusher.Flush()

	ch := s.hub.Subscribe(r.Context(), scope, scopeID)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, event)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	events, err := s.store.ListAudit(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}

func (s *Server) handleListLogSources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.logs.ListSources(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	logcenter.SortSources(items)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleLogSourceSubroutes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/logs/sources/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "tail" {
		writeError(w, http.StatusNotFound, "not_found", "未找到日志路由")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	response, err := s.logs.Tail(r.Context(), parts[0], logcenter.TailOptions{
		Limit:    logcenter.ParseLimit(r.URL.Query().Get("limit")),
		MaxBytes: logcenter.ParseMaxBytes(r.URL.Query().Get("maxBytes")),
		Level:    r.URL.Query().Get("level"),
		Query:    r.URL.Query().Get("q"),
	})
	if err != nil {
		if errors.Is(err, logcenter.ErrSourceNotFound) {
			writeError(w, http.StatusNotFound, "log_source_not_found", "未找到日志源")
			return
		}
		writeError(w, http.StatusInternalServerError, "log_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	settings, err := s.store.GetRuntimeSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file": map[string]any{
			"configPath":    s.cfg.ConfigPath,
			"addr":          s.currentListenAddr(),
			"dataDir":       s.cfg.DataDir,
			"dbPath":        s.cfg.DBPath,
			"logFile":       s.cfg.LogFile,
			"logMaxSizeMB":  s.cfg.LogMaxSizeMB,
			"logMaxFiles":   s.cfg.LogMaxFiles,
			"logMaxAgeDays": s.cfg.LogMaxAgeDays,
		},
		"runtime": settings,
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req storage.RuntimeSettings
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := storage.NormalizeRuntimeSettings(req)
	if settings.Addr != "" {
		writeError(w, http.StatusBadRequest, "use_listen_addr_endpoint", "修改监听地址请使用 POST /api/settings/listen-addr 接口")
		return
	}
	allowedRoots, err := workspaces.NormalizeAllowedRoots(settings.AllowedRoots)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_allowed_roots", err.Error())
		return
	}
	settings.AllowedRoots = allowedRoots
	if err := s.store.UpdateRuntimeSettings(r.Context(), settings); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	updated, err := s.store.GetRuntimeSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "settings.update",
		RiskLevel: "medium",
		Summary:   "已更新服务运行期配置",
		Payload: map[string]any{
			"allowedRoots": len(updated.AllowedRoots),
			"cookieSecure": updated.CookieSecure,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"runtime": updated})
}

// handleSwapListenAddr persists a new listen address to the database and then
// hot-swaps the running HTTP listener without a process restart.
//
// Sequence:
//  1. Validate addr (non-empty, host:port format)
//  2. Persist to DB FIRST – if the swap succeeds but the process later
//     crashes, a real restart must use the new address.
//  3. Call httpSrv.SwapAddr() – pre-binds new listener, atomically swaps,
//     drains old server.
//  4. If swap FAILS, roll back the DB write so the effective address
//     matches what is actually serving.
//  5. Audit-log the change.
func (s *Server) handleSwapListenAddr(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if s.httpSrv == nil {
		writeError(w, http.StatusServiceUnavailable, "manager_not_ready", "服务管理器未就绪")
		return
	}
	var body struct {
		Addr string `json:"addr"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Addr = strings.TrimSpace(body.Addr)
	if body.Addr == "" {
		writeError(w, http.StatusBadRequest, "invalid_addr", "监听地址不能为空")
		return
	}

	// ---- Step 1: Persist the new address to the DB ----------------------
	settings, err := s.store.GetRuntimeSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	previousAddr := settings.Addr
	settings.Addr = body.Addr
	if err := s.store.UpdateRuntimeSettings(r.Context(), settings); err != nil {
		writeError(w, http.StatusBadRequest, "persist_failed", err.Error())
		return
	}

	// ---- Step 2: Perform the synchronous hot-swap -----------------------
	if swapErr := s.httpSrv.SwapAddr(body.Addr); swapErr != nil {
		// Roll back: restore the DB value to the previous address.
		settings.Addr = previousAddr
		_ = s.store.UpdateRuntimeSettings(r.Context(), settings)
		writeError(w, http.StatusBadRequest, "swap_failed", swapErr.Error())
		return
	}

	// ---- Step 3: Audit log & response -----------------------------------
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "settings.listen_addr_changed",
		RiskLevel: "high",
		Summary:   "已更改 HTTP 监听地址",
		Payload: map[string]any{
			"old_addr": previousAddr,
			"new_addr": body.Addr,
		},
	})

	updated, err := s.store.GetRuntimeSettings(r.Context())
	if err != nil {
		updated = settings
	}
	// NOTE: this response travels back to the caller on the OLD connection,
	// which Shutdown() waits to drain.  The *next* request from the client
	// will arrive on the freshly-bound listener at body.Addr.
	writeJSON(w, http.StatusOK, map[string]any{
		"addr":    body.Addr,
		"runtime": updated,
	})
}

// currentListenAddr reports the address on which the server is currently
// listening.  Falls back to the startup config value when the lifecycle
// manager has not been wired in yet (e.g. during unit tests).
func (s *Server) currentListenAddr() string {
	if s.httpSrv != nil {
		return s.httpSrv.Addr()
	}
	return s.cfg.Addr
}

func (s *Server) handleImagesStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.images.Status(r.Context()))
}

func (s *Server) handleGetImagesSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	settings, err := s.store.GetImageProviderSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings,
		"status":   s.images.Status(r.Context()),
	})
}

func (s *Server) handleUpdateImagesSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, imagegen.MaxSettingsBytes)
	var req struct {
		XAIAPIKey             string `json:"xaiApiKey"`
		ClearAPIKey           bool   `json:"clearApiKey"`
		DefaultModel          string `json:"defaultModel"`
		DefaultResponseFormat string `json:"defaultResponseFormat"`
		DefaultResolution     string `json:"defaultResolution"`
		DefaultAspectRatio    string `json:"defaultAspectRatio"`
		HistoryRetention      int    `json:"historyRetention"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := storage.ImageProviderSettings{
		DefaultModel:          req.DefaultModel,
		DefaultResponseFormat: req.DefaultResponseFormat,
		DefaultResolution:     req.DefaultResolution,
		DefaultAspectRatio:    req.DefaultAspectRatio,
		HistoryRetention:      req.HistoryRetention,
		XAIAPIKey:             req.XAIAPIKey,
	}
	updated, err := s.images.UpdateSettings(r.Context(), settings, strings.TrimSpace(req.XAIAPIKey) != "", req.ClearAPIKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "images_settings_invalid", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.settings.update",
		RiskLevel: "medium",
		Summary:   "已更新 Images provider 设置",
		Payload: map[string]any{
			"provider":         updated.Provider,
			"hasApiKey":        updated.HasAPIKey,
			"defaultModel":     updated.DefaultModel,
			"responseFormat":   updated.DefaultResponseFormat,
			"historyRetention": updated.HistoryRetention,
			"clearedApiKey":    req.ClearAPIKey,
			"updatedApiKey":    strings.TrimSpace(req.XAIAPIKey) != "",
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated, "status": s.images.Status(r.Context())})
}

func (s *Server) handleGetImageStorageSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	settings, err := s.images.StorageSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (s *Server) handleUpdateImageStorageSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Backend                string `json:"backend"`
		ObjectStorageProfileID string `json:"objectStorageProfileId"`
		S3ProviderLabel        string `json:"s3ProviderLabel"`
		S3Bucket               string `json:"s3Bucket"`
		S3Region               string `json:"s3Region"`
		S3Endpoint             string `json:"s3Endpoint"`
		S3Prefix               string `json:"s3Prefix"`
		S3ForcePathStyle       bool   `json:"s3ForcePathStyle"`
		S3AccessKeyID          string `json:"s3AccessKeyId"`
		S3SecretAccessKey      string `json:"s3SecretAccessKey"`
		S3SessionToken         string `json:"s3SessionToken"`
		S3AccessMode           string `json:"s3AccessMode"`
		FallbackToLocal        bool   `json:"fallbackToLocal"`
		ClearSecret            bool   `json:"clearSecret"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := storage.ImageStorageSettings{
		Backend:                req.Backend,
		ObjectStorageProfileID: req.ObjectStorageProfileID,
		S3ProviderLabel:        req.S3ProviderLabel,
		S3Bucket:               req.S3Bucket,
		S3Region:               req.S3Region,
		S3Endpoint:             req.S3Endpoint,
		S3Prefix:               req.S3Prefix,
		S3ForcePathStyle:       req.S3ForcePathStyle,
		S3AccessKeyID:          req.S3AccessKeyID,
		S3SecretAccessKey:      req.S3SecretAccessKey,
		S3SessionToken:         req.S3SessionToken,
		S3AccessMode:           req.S3AccessMode,
		FallbackToLocal:        req.FallbackToLocal,
	}
	updateSecret := strings.TrimSpace(req.S3AccessKeyID) != "" || strings.TrimSpace(req.S3SecretAccessKey) != "" || strings.TrimSpace(req.S3SessionToken) != ""
	updated, err := s.images.UpdateStorageSettings(r.Context(), settings, updateSecret, req.ClearSecret)
	if err != nil {
		writeError(w, http.StatusBadRequest, "images_storage_invalid", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.storage.settings.updated",
		RiskLevel: "medium",
		Summary:   "已更新 Images 存储设置",
		Payload: map[string]any{
			"backend":       updated.Backend,
			"profileId":     updated.ObjectStorageProfileID,
			"providerLabel": updated.S3ProviderLabel,
			"bucket":        updated.S3Bucket,
			"endpoint":      safelog.URLLabel(updated.S3Endpoint),
			"updatedSecret": updateSecret,
			"clearedSecret": req.ClearSecret,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated})
}

func (s *Server) handleTestImageStorageSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	settings, err := s.images.StorageSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if err := s.images.TestStorage(r.Context(), settings); err != nil {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "images.storage.tested",
			RiskLevel: "medium",
			Summary:   "Images 对象存储连接测试失败",
			Payload:   map[string]any{"backend": settings.Backend, "bucket": settings.S3Bucket, "endpoint": safelog.URLLabel(settings.S3Endpoint), "error": safelog.Error(err, 240)},
		})
		writeError(w, http.StatusBadGateway, "images_storage_test_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.storage.tested",
		RiskLevel: "low",
		Summary:   "Images 对象存储连接测试通过",
		Payload:   map[string]any{"backend": settings.Backend, "bucket": settings.S3Bucket, "endpoint": safelog.URLLabel(settings.S3Endpoint)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleImagePrivateStatus(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	unlocked, expiresAt := s.privateImages.IsUnlocked(ctx.Session.ID, time.Now())
	payload := map[string]any{"unlocked": unlocked}
	if unlocked {
		payload["expiresAt"] = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleUnlockImagePrivate(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	owner, err := s.store.GetOwnerByID(r.Context(), ctx.Session.OwnerID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "登录已过期")
		return
	}
	key := "private:" + owner.Username
	ip := clientIP(r)
	if decision := s.privateUnlocks.Check(key, ip, time.Now()); decision.Limited {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "images.private.rate_limited",
			RiskLevel: "medium",
			Summary:   "Images 私密收藏夹解锁已被限流",
			Payload: map[string]any{
				"ip":           ip,
				"dimension":    decision.Dimension,
				"backoffUntil": decision.BackoffUntil.UTC().Format(time.RFC3339Nano),
			},
		})
		writeError(w, http.StatusTooManyRequests, "images_private_backoff", "私密收藏夹密码错误次数过多，请稍后再试")
		return
	}
	if !auth.VerifyPassword(owner.PasswordHash, req.Password) {
		events := s.privateUnlocks.RecordFailure(key, ip, time.Now())
		for _, event := range events {
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "images.private.backoff_started",
				RiskLevel: "medium",
				Summary:   "Images 私密收藏夹解锁失败触发退避",
				Payload: map[string]any{
					"ip":           ip,
					"dimension":    event.Dimension,
					"backoffUntil": event.BackoffUntil.UTC().Format(time.RFC3339Nano),
					"durationSec":  int(event.Duration.Seconds()),
				},
			})
		}
		writeError(w, http.StatusUnauthorized, "images_private_invalid_password", "密码错误")
		return
	}
	s.privateUnlocks.RecordSuccess(key)
	expiresAt := s.privateImages.Unlock(ctx.Session.ID, time.Now())
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.private.unlocked",
		RiskLevel: "low",
		Summary:   "已解锁 Images 私密收藏夹",
		Payload:   map[string]any{"expiresAt": expiresAt.UTC().Format(time.RFC3339Nano)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"unlocked": true, "expiresAt": expiresAt.UTC().Format(time.RFC3339Nano)})
}

func (s *Server) handleLockImagePrivate(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	s.privateImages.Lock(ctx.Session.ID)
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.private.locked",
		RiskLevel: "low",
		Summary:   "已锁定 Images 私密收藏夹",
	})
	writeJSON(w, http.StatusOK, map[string]any{"unlocked": false})
}

func (s *Server) handleListImageJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	limit := 80
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	items, err := s.store.ListImageGenerationJobs(r.Context(), limit, r.URL.Query().Get("status"), r.URL.Query().Get("mode"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	count, _ := s.store.CountImageGenerationJobs(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": count})
}

func (s *Server) handleCreateImageJob(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, imagegen.MaxFormBytes)
	if err := r.ParseMultipartForm(imagegen.MaxFormBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_multipart", "图片生成表单无效或过大")
		return
	}
	request, err := imagegen.ParseMultipartRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, imageErrorCode(err), err.Error())
		return
	}
	if !s.requireUnlockedForPrivateImageSources(w, r, ctx, request) {
		return
	}
	job, err := s.images.CreateJob(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, imageErrorCode(err), err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.job.created",
		RiskLevel: "low",
		Summary:   "已创建 Images 生成任务",
		Payload: map[string]any{
			"jobId":       job.ID,
			"mode":        job.Mode,
			"model":       job.Model,
			"sourceCount": job.SourceCount,
			"imageCount":  job.ImageCount,
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) requireUnlockedForPrivateImageSources(w http.ResponseWriter, r *http.Request, ctx sessionContext, request imagegen.ImagineRequest) bool {
	for _, image := range request.Images {
		if image.SourceType != "library_asset" {
			continue
		}
		assetID := strings.TrimPrefix(image.URL, "asset:")
		asset, err := s.store.GetImageAsset(r.Context(), assetID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "image_asset_not_found", "未找到图片资产")
			return false
		}
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return false
		}
	}
	return true
}

func (s *Server) handleImageJobSubroutes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/images/jobs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到图片任务")
		return
	}
	job, err := s.store.GetImageGenerationJob(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "image_job_not_found", "未找到图片任务")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleListImageLibraryAssets(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	limit := 80
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	privacy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("privacy")))
	if (privacy == "private" || privacy == "all") && !s.requireImagePrivateUnlocked(w, ctx) {
		return
	}
	items, err := s.store.ListImageAssets(r.Context(), limit, r.URL.Query().Get("type"), r.URL.Query().Get("storage"), r.URL.Query().Get("status"), r.URL.Query().Get("q"), privacy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleUploadImageLibraryAsset(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, imagegen.MaxFormBytes)
	if err := r.ParseMultipartForm(imagegen.MaxFormBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_multipart", "图片上传表单无效或过大")
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "image_file_missing", "请选择要上传的图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, imagegen.MaxImageBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "image_file_invalid", err.Error())
		return
	}
	filename := ""
	if header != nil {
		filename = header.Filename
	}
	result, err := s.images.UploadLibraryAsset(r.Context(), filename, data, "")
	if err != nil {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "images.asset.upload_failed",
			RiskLevel: "medium",
			Summary:   "Images 图片手动上传失败",
			Payload:   map[string]any{"error": safelog.Error(err, 240)},
		})
		writeError(w, http.StatusBadRequest, imageErrorCode(err), err.Error())
		return
	}
	eventType := "images.asset.uploaded"
	summary := "已上传 Images 图片资产"
	if result.Duplicate {
		eventType = "images.asset.deduplicated"
		summary = "Images 图片上传命中去重"
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: eventType,
		RiskLevel: "low",
		Summary:   summary,
		Payload: map[string]any{
			"assetId":   result.Asset.ID,
			"duplicate": result.Duplicate,
			"storage":   result.Asset.StorageBackend,
			"bytes":     result.Asset.SizeBytes,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"asset": result.Asset, "duplicate": result.Duplicate})
}

func (s *Server) handleImageLibraryAssetSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/images/library/assets/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "image_asset_not_found", "未找到图片资产")
		return
	}
	asset, err := s.store.GetImageAsset(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "image_asset_not_found", "未找到图片资产")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"asset": asset})
		case http.MethodDelete:
			if !s.requireCSRF(w, r, ctx.Session) {
				return
			}
			if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
				return
			}
			deleted, err := s.images.DeleteAsset(r.Context(), asset.ID)
			if err != nil {
				_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
					EventType: "images.asset.delete_failed",
					RiskLevel: "medium",
					Summary:   "Images 图片资产删除失败",
					Payload:   map[string]any{"assetId": asset.ID, "jobId": asset.JobID, "storage": asset.StorageBackend, "error": err.Error()},
				})
				writeError(w, http.StatusBadGateway, "image_asset_delete_failed", err.Error())
				return
			}
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "images.asset.deleted",
				RiskLevel: "medium",
				Summary:   "已删除 Images 图片资产",
				Payload:   map[string]any{"assetId": asset.ID, "jobId": asset.JobID, "storage": asset.StorageBackend},
			})
			writeJSON(w, http.StatusOK, map[string]any{"asset": deleted})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
		}
		return
	}
	switch parts[1] {
	case "content":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return
		}
		s.serveImageAssetContent(w, r, asset, false)
	case "download":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return
		}
		s.serveImageAssetContent(w, r, asset, true)
	case "private":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req struct {
			Private bool `json:"private"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return
		}
		updated, err := s.store.SetImageAssetPrivate(r.Context(), asset.ID, req.Private)
		if err != nil {
			writeError(w, http.StatusBadRequest, "image_asset_private_failed", err.Error())
			return
		}
		eventType := "images.asset.private.added"
		summary := "已加入 Images 私密收藏夹"
		if !req.Private {
			eventType = "images.asset.private.removed"
			summary = "已移出 Images 私密收藏夹"
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: eventType,
			RiskLevel: "medium",
			Summary:   summary,
			Payload:   map[string]any{"assetId": updated.ID, "jobId": updated.JobID},
		})
		writeJSON(w, http.StatusOK, map[string]any{"asset": updated})
	case "archive-s3":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
			return
		}
		archived, err := s.images.ArchiveAssetToS3(r.Context(), asset.ID)
		if err != nil {
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "images.asset.archive_failed",
				RiskLevel: "medium",
				Summary:   "Images 图片资产归档到 S3 失败",
				Payload:   map[string]any{"assetId": asset.ID, "jobId": asset.JobID, "error": safelog.Error(err, 240)},
			})
			writeError(w, http.StatusBadGateway, "image_asset_archive_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "images.asset.archived.s3",
			RiskLevel: "medium",
			Summary:   "已将 Images 图片资产归档到 S3",
			Payload:   map[string]any{"assetId": archived.ID, "jobId": archived.JobID, "bucket": archived.S3Bucket},
		})
		writeJSON(w, http.StatusOK, map[string]any{"asset": archived})
	default:
		writeError(w, http.StatusNotFound, "image_asset_not_found", "未找到图片资产")
	}
}

func (s *Server) requireImagePrivateUnlocked(w http.ResponseWriter, ctx sessionContext) bool {
	if unlocked, _ := s.privateImages.IsUnlocked(ctx.Session.ID, time.Now()); unlocked {
		return true
	}
	writeError(w, http.StatusForbidden, "images_private_locked", "请先输入密码解锁私密收藏夹")
	return false
}

func (s *Server) serveImageAssetContent(w http.ResponseWriter, r *http.Request, asset storage.ImageAsset, download bool) {
	if asset.Status == "deleted" {
		writeError(w, http.StatusGone, "image_asset_deleted", "图片资产已删除")
		return
	}
	mimeType, data, err := s.images.ReadAsset(r.Context(), asset)
	if err != nil {
		writeError(w, http.StatusNotFound, "image_asset_not_found", "未找到图片资产")
		return
	}
	if mimeType == "" {
		mimeType = asset.MimeType
	}
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if download {
		ext := asset.Extension
		if ext == "" {
			ext = imageExtensionFromMime(mimeType)
		}
		filename := fmt.Sprintf("phantom-image-%s%s", asset.ID, ext)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	}
	http.ServeContent(w, r, asset.ID, time.Now(), bytes.NewReader(data))
}

func (s *Server) handleImageAsset(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/images/assets/")
	// Lookup order:
	//  1. By asset ID — this is the canonical lookup and covers every
	//     asset regardless of storage backend (local, S3, or remote).
	//     S3-archived assets have an empty LocalName, so lookup #2
	//     would otherwise miss them.
	//  2. By LocalName — preserved for historical URLs that referenced
	//     the on-disk file name of pre-archival assets.
	var asset storage.ImageAsset
	var found bool
	if a, err := s.store.GetImageAsset(r.Context(), name); err == nil {
		asset = a
		found = true
	} else if a, err := s.store.GetImageAssetByLocalName(r.Context(), name); err == nil {
		asset = a
		found = true
	}
	if !found {
		writeError(w, http.StatusNotFound, "image_asset_not_found", "未找到图片资产")
		return
	}
	if asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
		return
	}
	// serveImageAssetContent is backend-aware: it dispatches through
	// s.images.ReadAsset which handles S3, remote HTTP, and local disk
	// uniformly. Using it here (instead of http.ServeFile + AssetPath)
	// closes the backend coverage gap for the legacy `/api/images/assets/*`
	// route so S3-archived assets are reachable.
	s.serveImageAssetContent(w, r, asset, false)
}

func (s *Server) handleV2RayStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.v2ray.Status(r.Context()))
}

func (s *Server) handleGetV2RaySettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	settings, clients, ok := s.v2raySettingsPayload(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings,
		"clients":  clients,
		"status":   s.v2ray.Status(r.Context()),
	})
}

func (s *Server) handleUpdateV2RaySettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req storage.V2RaySettings
	if !decodeJSON(w, r, &req) {
		return
	}
	existing, err := s.store.GetV2RaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	req.ID = "default"
	req.Enabled = existing.Enabled
	updated, err := s.store.UpdateV2RaySettings(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "v2ray.settings.update",
		RiskLevel: v2rayRisk(updated),
		Summary:   "已更新 V2Ray 设置",
		Payload: map[string]any{
			"listen":    updated.Listen,
			"port":      updated.Port,
			"transport": updated.Transport,
			"security":  updated.Security,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated, "status": s.v2ray.Status(r.Context())})
}

func (s *Server) handleValidateV2Ray(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Settings storage.V2RaySettings `json:"settings"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := req.Settings
	if settings.ID == "" && settings.Listen == "" && settings.Port == 0 {
		var err error
		settings, err = s.store.GetV2RaySettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	clients, err := s.store.ListV2RayRemoteClients(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	result, err := s.v2ray.Validate(r.Context(), settings, clients)
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "v2ray.config.validate",
		RiskLevel: v2rayRisk(settings),
		Summary:   "已校验 V2Ray 配置",
		Payload:   map[string]any{"ok": result.OK, "configHash": result.ConfigHash, "message": result.Message},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "v2ray_config_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleControlV2Ray(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var (
		status v2ray.Status
		err    error
	)
	switch req.Action {
	case "start":
		status, err = s.v2ray.Start(r.Context())
	case "stop":
		status, err = s.v2ray.Stop(r.Context())
	case "restart":
		status, err = s.v2ray.Restart(r.Context())
	default:
		writeError(w, http.StatusBadRequest, "invalid_action", "只支持 start、stop、restart")
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "v2ray.service." + req.Action,
		RiskLevel: "high",
		Summary:   "已执行 V2Ray 服务控制",
		Payload:   map[string]any{"action": req.Action, "state": status.State, "endpoint": status.Endpoint, "error": errorMessage(err)},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "v2ray_control_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCreateV2RayClient(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req storage.V2RayRemoteClient
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.UUID) == "" {
		uuid, err := v2ray.GenerateUUID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "uuid_failed", err.Error())
			return
		}
		req.UUID = uuid
	}
	client, err := s.store.CreateV2RayRemoteClient(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "v2ray.client.create",
		RiskLevel: "medium",
		Summary:   "已添加 V2Ray 远程设备",
		Payload:   map[string]any{"clientId": client.ID, "label": client.Label},
	})
	writeJSON(w, http.StatusCreated, client)
}

func (s *Server) handleV2RayClientSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2ray/clients/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到远程设备")
		return
	}
	client, err := s.store.GetV2RayRemoteClient(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "v2ray_client_not_found", "未找到远程设备")
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "export" {
		exported, err := s.v2ray.ExportClient(r.Context(), client)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, exported)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "rotate" {
		uuid, err := v2ray.GenerateUUID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "uuid_failed", err.Error())
			return
		}
		updated, err := s.store.RotateV2RayRemoteClient(r.Context(), client.ID, uuid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "v2ray.client.rotate",
			RiskLevel: "high",
			Summary:   "已轮换 V2Ray 远程设备 UUID",
			Payload:   map[string]any{"clientId": updated.ID, "label": updated.Label},
		})
		writeJSON(w, http.StatusOK, updated)
		return
	}
	if r.Method == http.MethodPut && len(parts) == 1 {
		var req storage.V2RayRemoteClient
		if !decodeJSON(w, r, &req) {
			return
		}
		req.ID = client.ID
		updated, err := s.store.UpdateV2RayRemoteClient(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "v2ray.client.update",
			RiskLevel: "medium",
			Summary:   "已更新 V2Ray 远程设备",
			Payload:   map[string]any{"clientId": updated.ID, "label": updated.Label, "enabled": updated.Enabled},
		})
		writeJSON(w, http.StatusOK, updated)
		return
	}
	if r.Method == http.MethodDelete && len(parts) == 1 {
		if err := s.store.RevokeV2RayRemoteClient(r.Context(), client.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "v2ray.client.revoke",
			RiskLevel: "high",
			Summary:   "已撤销 V2Ray 远程设备",
			Payload:   map[string]any{"clientId": client.ID, "label": client.Label},
		})
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "未找到远程设备路由")
}

func (s *Server) v2raySettingsPayload(w http.ResponseWriter, r *http.Request) (storage.V2RaySettings, []storage.V2RayRemoteClient, bool) {
	settings, err := s.store.GetV2RaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return storage.V2RaySettings{}, nil, false
	}
	clients, err := s.store.ListV2RayRemoteClients(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return storage.V2RaySettings{}, nil, false
	}
	return settings, clients, true
}

func (s *Server) createSessionResponse(w http.ResponseWriter, r *http.Request, owner storage.Owner) {
	token, tokenHash, err := auth.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	csrfToken, csrfHash, err := auth.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	expiresAt := time.Now().UTC().Add(sessionTTL)
	session, err := s.store.CreateSession(r.Context(), owner.ID, tokenHash, csrfHash, false, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	secure := s.cookieSecure(r.Context())
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expiresAt,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expiresAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"owner":     map[string]any{"id": owner.ID, "username": owner.Username},
		"session":   map[string]any{"id": session.ID, "expiresAt": expiresAt.Format(time.RFC3339Nano)},
		"csrfToken": csrfToken,
	})
}

func (s *Server) cookieSecure(ctx context.Context) bool {
	settings, err := s.store.GetRuntimeSettings(ctx)
	if err != nil {
		return s.cfg.CookieSecure
	}
	return settings.CookieSecure
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (sessionContext, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
		return sessionContext{}, false
	}
	session, err := s.store.GetSessionByHash(r.Context(), auth.HashToken(cookie.Value))
	if err != nil || session.RevokedAt.Valid || time.Now().UTC().After(session.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "登录已过期")
		return sessionContext{}, false
	}
	_ = s.store.TouchSession(r.Context(), session.ID)
	return sessionContext{Session: session}, true
}

func (s *Server) requireCSRF(w http.ResponseWriter, r *http.Request, session storage.Session) bool {
	header := r.Header.Get("X-CSRF-Token")
	if header == "" {
		writeError(w, http.StatusForbidden, "csrf_required", "缺少 CSRF token")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(auth.HashToken(header)), []byte(session.CSRFTokenHash)) != 1 {
		writeError(w, http.StatusForbidden, "csrf_invalid", "CSRF token 无效")
		return false
	}
	return true
}

func (s *Server) staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "未找到路由")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(s.staticFS, name)
		if err != nil {
			name = "index.html"
			data, err = fs.ReadFile(s.staticFS, name)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "static_missing", "前端资源缺失")
				return
			}
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// Best-effort stack capture — 12 deep frames is enough to
				// identify the source of most handler panics.
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				stack := string(buf[:n])
				s.log.Error("panic recovered",
					"panic", recovered,
					"method", r.Method,
					"path", safelog.RequestPathLabel(r.URL),
					"ip", remoteIP(r),
					"ua", shortUA(r.UserAgent()),
					"stack", stack,
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "JSON 请求体无效")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeSSE(w http.ResponseWriter, event events.Event) {
	payload, _ := json.Marshal(event)
	fmt.Fprintf(w, "id: %d\n", event.Sequence)
	fmt.Fprintf(w, "event: %s\n", event.Type)
	fmt.Fprintf(w, "data: %s\n\n", payload)
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: name == sessionCookie,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func firstQuery(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := r.URL.Query().Get(key); value != "" {
			return value
		}
	}
	return ""
}

func v2rayRisk(settings storage.V2RaySettings) string {
	if settings.Listen == "0.0.0.0" || settings.Security == "none" || !settings.BlockPrivateNetwork {
		return "high"
	}
	return "medium"
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func imageErrorCode(err error) string {
	if err == nil {
		return "internal_error"
	}
	message := err.Error()
	if errors.Is(err, imagegen.ErrAPIKeyMissing) || message == imagegen.ErrAPIKeyMissing.Error() {
		return "api_key_missing"
	}
	switch {
	case strings.Contains(message, "prompt is required"):
		return "prompt_required"
	case strings.Contains(message, "prompt is too long"):
		return "prompt_too_long"
	case strings.Contains(message, "model name"):
		return "model_invalid"
	case strings.Contains(message, "mode is invalid"):
		return "mode_invalid"
	case strings.Contains(message, "source image"), strings.Contains(message, "source images"), strings.Contains(message, "requires two or three"):
		return "source_count_invalid"
	case strings.Contains(message, "aspect ratio"):
		return "aspect_ratio_unsupported"
	case strings.Contains(message, "resolution"):
		return "resolution_unsupported"
	case strings.Contains(message, "response format"):
		return "response_format_unsupported"
	case strings.Contains(message, "image count"):
		return "image_count_invalid"
	case strings.Contains(message, "larger than"), strings.Contains(message, "too large"):
		return "image_too_large"
	case strings.Contains(message, "jpeg"), strings.Contains(message, "png"), strings.Contains(message, "webp"):
		return "image_mime_unsupported"
	case strings.Contains(message, "url"):
		return "image_url_invalid"
	case strings.Contains(message, "xAI request failed"):
		return "provider_failed"
	default:
		return "images_request_failed"
	}
}

func imageExtensionFromMime(mimeType string) string {
	switch strings.TrimSpace(strings.ToLower(mimeType)) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}
