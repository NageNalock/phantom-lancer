package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/codex"
	"phantom-lancer/internal/codexgateway"
	"phantom-lancer/internal/config"
	"phantom-lancer/internal/events"
	imagegen "phantom-lancer/internal/images"
	logcenter "phantom-lancer/internal/logs"
	"phantom-lancer/internal/selfupdate"
	"phantom-lancer/internal/storage"
	"phantom-lancer/internal/v2ray"
	"phantom-lancer/internal/workspaces"
)

const (
	sessionCookie = "pl_session"
	csrfCookie    = "pl_csrf"
	sessionTTL    = 7 * 24 * time.Hour
)

type Server struct {
	cfg            config.Config
	store          *storage.Store
	hub            *events.Hub
	codex          *codex.Service
	codexGateway   *codexgateway.Service
	v2ray          *v2ray.Service
	images         *imagegen.Service
	logs           *logcenter.Service
	updates        *selfupdate.Service
	staticFS       fs.FS
	log            *slog.Logger
	logins         *loginBackoff
	gatewayOAuth   *codexGatewayOAuthSessions
	privateUnlocks *loginBackoff
	updateConfirms *loginBackoff
	privateImages  *privateImageAccess
}

type sessionContext struct {
	Session storage.Session
}

func New(cfg config.Config, store *storage.Store, hub *events.Hub, codexSvc *codex.Service, codexGatewaySvc *codexgateway.Service, v2raySvc *v2ray.Service, imagesSvc *imagegen.Service, logsSvc *logcenter.Service, updateSvc *selfupdate.Service, staticFS fs.FS, logger *slog.Logger) (*Server, error) {
	return &Server{
		cfg:            cfg,
		store:          store,
		hub:            hub,
		codex:          codexSvc,
		codexGateway:   codexGatewaySvc,
		v2ray:          v2raySvc,
		images:         imagesSvc,
		logs:           logsSvc,
		updates:        updateSvc,
		staticFS:       staticFS,
		log:            logger,
		logins:         newLoginBackoff(cfg.LoginFailureThreshold),
		gatewayOAuth:   newCodexGatewayOAuthSessions(10 * time.Minute),
		privateUnlocks: newLoginBackoff(cfg.LoginFailureThreshold),
		updateConfirms: newLoginBackoff(cfg.LoginFailureThreshold),
		privateImages:  newPrivateImageAccess(),
	}, nil
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
	mux.HandleFunc("GET /api/workspaces", s.handleListWorkspaces)
	mux.HandleFunc("POST /api/workspaces", s.handleCreateWorkspace)
	mux.HandleFunc("GET /api/workspaces/", s.handleWorkspaceSubroutes)
	mux.HandleFunc("GET /api/codex/status", s.handleCodexStatus)
	mux.HandleFunc("GET /api/codex/models", s.handleCodexModels)
	mux.HandleFunc("GET /api/codex/capabilities", s.handleCodexCapabilities)
	mux.HandleFunc("GET /api/codex/sessions", s.handleListCodexSessions)
	mux.HandleFunc("POST /api/codex/sessions", s.handleCreateCodexSession)
	mux.HandleFunc("GET /api/codex/sessions/", s.handleCodexSessionSubroutes)
	mux.HandleFunc("POST /api/codex/sessions/", s.handleCodexSessionSubroutes)
	mux.HandleFunc("PATCH /api/codex/sessions/", s.handleCodexSessionSubroutes)
	mux.HandleFunc("POST /api/codex/exec-jobs", s.handleCreateExecJob)
	mux.HandleFunc("GET /api/codex/exec-jobs/", s.handleExecJobSubroutes)
	mux.HandleFunc("POST /api/codex/exec-jobs/", s.handleExecJobSubroutes)
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
	mux.HandleFunc("GET /api/events/history", s.handleEventHistory)
	mux.HandleFunc("GET /api/events/stream", s.handleEventStream)
	mux.HandleFunc("GET /api/approvals/pending", s.handlePendingApprovals)
	mux.HandleFunc("POST /api/approvals/", s.handleApprovalSubroutes)
	mux.HandleFunc("GET /api/audit/events", s.handleAuditEvents)
	mux.HandleFunc("GET /api/system/version", s.handleSystemVersion)
	mux.HandleFunc("GET /api/system/update/status", s.handleSystemUpdateStatus)
	mux.HandleFunc("POST /api/system/update/check", s.handleSystemUpdateCheck)
	mux.HandleFunc("POST /api/system/update/apply", s.handleSystemUpdateApply)
	mux.HandleFunc("GET /api/system/update/jobs/", s.handleSystemUpdateJobSubroutes)
	mux.HandleFunc("POST /api/system/update/jobs/", s.handleSystemUpdateJobSubroutes)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/logs/sources", s.handleListLogSources)
	mux.HandleFunc("GET /api/logs/sources/", s.handleLogSourceSubroutes)
	mux.HandleFunc("GET /api/images/status", s.handleImagesStatus)
	mux.HandleFunc("GET /api/images/settings", s.handleGetImagesSettings)
	mux.HandleFunc("PUT /api/images/settings", s.handleUpdateImagesSettings)
	mux.HandleFunc("GET /api/images/storage-settings", s.handleGetImageStorageSettings)
	mux.HandleFunc("PUT /api/images/storage-settings", s.handleUpdateImageStorageSettings)
	mux.HandleFunc("POST /api/images/storage-settings/test", s.handleTestImageStorageSettings)
	mux.HandleFunc("GET /api/images/library/private/status", s.handleImagePrivateStatus)
	mux.HandleFunc("POST /api/images/library/private/unlock", s.handleUnlockImagePrivate)
	mux.HandleFunc("POST /api/images/library/private/lock", s.handleLockImagePrivate)
	mux.HandleFunc("GET /api/images/jobs", s.handleListImageJobs)
	mux.HandleFunc("POST /api/images/jobs", s.handleCreateImageJob)
	mux.HandleFunc("GET /api/images/jobs/", s.handleImageJobSubroutes)
	mux.HandleFunc("GET /api/images/library/assets", s.handleListImageLibraryAssets)
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

	mux.Handle("/", s.staticHandler())
	return s.recover(mux)
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
	workspacesList, err := s.store.ListWorkspaces(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	audit, _ := s.store.ListAudit(r.Context(), 8)
	codexGatewayStatus, err := s.codexGateway.Status(r.Context())
	if err != nil {
		codexGatewayStatus = codexgateway.Status{LastError: err.Error()}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspaces": map[string]any{
			"total": len(workspacesList),
			"items": workspacesList,
		},
		"codex":            s.codex.Status(r.Context()),
		"codexGateway":     codexGatewayStatus,
		"images":           s.images.Status(r.Context()),
		"v2ray":            s.v2ray.Status(r.Context()),
		"pendingApprovals": 0,
		"recentActivity":   audit,
	})
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListWorkspaces(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Name            string   `json:"name"`
		RootPath        string   `json:"rootPath"`
		Description     string   `json:"description"`
		Tags            []string `json:"tags"`
		AllowCodexWrite bool     `json:"allowCodexWrite"`
		AllowNonGit     bool     `json:"allowNonGit"`
		CreateMissing   bool     `json:"createMissing"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings, err := s.store.GetRuntimeSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	allowedRoots, err := workspaces.NormalizeAllowedRoots(settings.AllowedRoots)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_runtime_settings", err.Error())
		return
	}
	normalized, err := workspaces.NormalizeWorkspacePath(allowedRoots, req.RootPath)
	createdDirectory := false
	if err != nil && req.CreateMissing && errors.Is(err, workspaces.ErrPathNotFound) {
		normalized, err = workspaces.NormalizeWorkspacePathForCreate(allowedRoots, req.RootPath)
		if err == nil {
			if mkdirErr := os.MkdirAll(normalized, 0o755); mkdirErr != nil {
				writeError(w, http.StatusBadRequest, "workspace_create_failed", mkdirErr.Error())
				return
			}
			createdDirectory = true
			normalized, err = workspaces.NormalizeWorkspacePath(allowedRoots, normalized)
		}
	}
	if err != nil {
		writeWorkspacePathError(w, err)
		return
	}
	existing, err := s.store.GetWorkspaceByRootPath(r.Context(), normalized)
	if err == nil {
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if !isNotFound(err) {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = workspaceNameFromPath(normalized)
	}
	if !req.AllowNonGit && !workspaces.IsGitRepository(normalized) {
		writeError(w, http.StatusBadRequest, "git_required", "该目录不是 Git 仓库；如需继续请勾选“允许非 Git 目录”")
		return
	}
	workspace, err := s.store.CreateWorkspace(r.Context(), storage.Workspace{
		Name:            req.Name,
		RootPath:        normalized,
		Description:     req.Description,
		Tags:            req.Tags,
		AllowCodexWrite: req.AllowCodexWrite,
		AllowNonGit:     req.AllowNonGit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "workspace.create",
		WorkspaceID: workspace.ID,
		Summary:     "已添加项目",
		Payload:     map[string]any{"name": workspace.Name, "rootPath": workspace.RootPath, "directoryCreated": createdDirectory},
	})
	writeJSON(w, http.StatusCreated, workspace)
}

func (s *Server) handleWorkspaceSubroutes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到项目路由")
		return
	}
	workspace, err := s.store.GetWorkspace(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace_not_found", "未找到项目")
		return
	}
	if len(parts) == 1 {
		writeJSON(w, http.StatusOK, workspace)
		return
	}
	if len(parts) == 2 && parts[1] == "status" {
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"git":       workspaces.ReadGitStatus(r.Context(), workspace.RootPath),
		})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "未找到项目路由")
}

func (s *Server) handleCodexStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.codex.Status(r.Context()))
}

func (s *Server) handleCodexModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	models, err := s.codex.ListModels(r.Context(), r.URL.Query().Get("includeHidden") == "true")
	if err != nil {
		writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) handleCodexCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	sections := map[string]any{}
	errors := map[string]string{}
	for _, item := range []struct {
		key    string
		method string
		params map[string]any
	}{
		{key: "models", method: "model/list", params: map[string]any{"limit": 80}},
		{key: "permissionProfiles", method: "permissionProfile/list", params: map[string]any{"limit": 80}},
		{key: "account", method: "account/read"},
		{key: "rateLimits", method: "account/rateLimits/read"},
		{key: "mcp", method: "mcpServerStatus/list", params: map[string]any{"limit": 80}},
		{key: "plugins", method: "plugin/list", params: map[string]any{}},
		{key: "skills", method: "skills/list", params: map[string]any{"cwds": []string{}}},
		{key: "hooks", method: "hooks/list", params: map[string]any{"cwds": []string{}}},
	} {
		result, err := s.codex.Capability(r.Context(), item.method, item.params)
		if err != nil {
			errors[item.key] = err.Error()
			continue
		}
		sections[item.key] = result
	}
	sections["config"] = map[string]any{"status": "deferred", "summary": "原始 Codex config 读取和写入需要单独的受控接口"}
	writeJSON(w, http.StatusOK, map[string]any{"sections": sections, "errors": errors})
}

func (s *Server) handleListCodexSessions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	includeArchived := r.URL.Query().Get("includeArchived") == "true"
	items, err := s.store.ListCodexSessions(r.Context(), r.URL.Query().Get("workspaceId"), includeArchived, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexSession(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		WorkspaceID       string `json:"workspaceId"`
		Title             string `json:"title"`
		Sandbox           string `json:"sandbox"`
		Model             string `json:"model"`
		ServiceTier       string `json:"serviceTier"`
		ApprovalPolicy    string `json:"approvalPolicy"`
		ApprovalsReviewer string `json:"approvalsReviewer"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Sandbox == "" {
		req.Sandbox = "read-only"
	}
	if !validSandbox(req.Sandbox) {
		writeError(w, http.StatusBadRequest, "invalid_sandbox", "只支持只读和工作区可写两种沙箱")
		return
	}
	req.ApprovalPolicy = normalizeApprovalPolicy(req.ApprovalPolicy)
	req.ApprovalsReviewer = normalizeApprovalsReviewer(req.ApprovalsReviewer)
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	workspace := storage.Workspace{}
	hasWorkspace := req.WorkspaceID != ""
	if hasWorkspace {
		var err error
		workspace, err = s.store.GetWorkspace(r.Context(), req.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusNotFound, "workspace_not_found", "未找到项目")
			return
		}
	}
	if req.Sandbox == "workspace-write" && (!hasWorkspace || !workspace.AllowCodexWrite) {
		writeError(w, http.StatusForbidden, "permission_denied", "workspace-write 需要选择已允许写入的项目")
		return
	}
	session, err := s.codex.CreateSession(r.Context(), workspace, strings.TrimSpace(req.Title), req.Sandbox, codex.SessionOptions{
		Model:             strings.TrimSpace(req.Model),
		ServiceTier:       strings.TrimSpace(req.ServiceTier),
		ApprovalPolicy:    req.ApprovalPolicy,
		ApprovalsReviewer: req.ApprovalsReviewer,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "codex.session.create",
		WorkspaceID: workspace.ID,
		RiskLevel:   riskForSandbox(req.Sandbox),
		Summary:     "已创建 Codex 会话",
		Payload:     map[string]any{"sessionId": session.ID, "threadId": session.CodexThreadID, "sandbox": session.Sandbox, "hasWorkspace": hasWorkspace},
	})
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleCodexSessionSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/codex/sessions/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到会话路由")
		return
	}
	session, err := s.store.GetCodexSession(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "session_not_found", "未找到 Codex 会话")
		return
	}
	workspace, hasWorkspace, err := s.sessionWorkspace(r.Context(), session)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace_not_found", "未找到项目")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		turns, _ := s.store.ListCodexTurns(r.Context(), session.ID, 200)
		items, _ := s.store.ListCodexItems(r.Context(), session.ID, 200)
		var workspacePayload any
		if hasWorkspace {
			workspacePayload = workspace
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session":   session,
			"workspace": workspacePayload,
			"turns":     turns,
			"items":     items,
		})
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "git" {
		if !hasWorkspace || workspace.RootPath == "" {
			writeError(w, http.StatusBadRequest, "workspace_required", "该会话没有绑定项目")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     workspaces.ReadGitStatus(r.Context(), workspace.RootPath),
			"diff":       workspaces.ReadGitDiff(r.Context(), workspace.RootPath, false),
			"stagedDiff": workspaces.ReadGitDiff(r.Context(), workspace.RootPath, true),
		})
		return
	}
	if r.Method == http.MethodPatch && len(parts) == 2 && parts[1] == "settings" {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		s.handleUpdateCodexSessionSettings(w, r, session, workspace, hasWorkspace)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if len(parts) == 2 && parts[1] == "turns" {
		s.handleCreateCodexTurn(w, r, session, workspace, hasWorkspace)
		return
	}
	if len(parts) == 3 && parts[1] == "git" && parts[2] == "actions" {
		s.handleCodexGitAction(w, r, session, workspace, hasWorkspace)
		return
	}
	if len(parts) == 2 && parts[1] == "interrupt" {
		interrupted, err := s.codex.InterruptSessionTurn(r.Context(), session)
		if err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.turn.interrupt",
			WorkspaceID: workspace.ID,
			Summary:     "已请求中断 Codex 回合",
			Payload:     map[string]any{"sessionId": session.ID, "turnId": session.LastTurnID, "interrupted": interrupted},
		})
		writeJSON(w, http.StatusOK, map[string]any{"interrupted": interrupted})
		return
	}
	if len(parts) == 2 && parts[1] == "archive" {
		if err := s.codex.ArchiveSession(r.Context(), session); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.session.archive",
			WorkspaceID: workspace.ID,
			Summary:     "已归档 Codex 会话",
			Payload:     map[string]any{"sessionId": session.ID, "threadId": session.CodexThreadID},
		})
		writeJSON(w, http.StatusOK, map[string]any{"archived": true})
		return
	}
	if len(parts) == 2 && parts[1] == "fork" {
		forked, err := s.codex.ForkSession(r.Context(), session, workspace)
		if err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.session.fork",
			WorkspaceID: workspace.ID,
			RiskLevel:   riskForSandbox(forked.Sandbox),
			Summary:     "已 fork Codex 会话",
			Payload:     map[string]any{"sessionId": session.ID, "forkedSessionId": forked.ID, "threadId": forked.CodexThreadID},
		})
		writeJSON(w, http.StatusCreated, forked)
		return
	}
	if len(parts) == 2 && parts[1] == "rollback" {
		var req struct {
			NumTurns int `json:"numTurns"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := s.codex.RollbackSession(r.Context(), session, req.NumTurns); err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.session.rollback",
			WorkspaceID: workspace.ID,
			RiskLevel:   "medium",
			Summary:     "已请求回滚 Codex 会话历史",
			Payload:     map[string]any{"sessionId": session.ID, "threadId": session.CodexThreadID, "numTurns": req.NumTurns},
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "compact" {
		if err := s.codex.CompactSession(r.Context(), session); err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.session.compact",
			WorkspaceID: workspace.ID,
			RiskLevel:   "low",
			Summary:     "已请求压缩 Codex 会话上下文",
			Payload:     map[string]any{"sessionId": session.ID, "threadId": session.CodexThreadID},
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "review" {
		var req struct {
			Target string `json:"target"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := s.codex.StartReview(r.Context(), session, strings.TrimSpace(req.Target)); err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.review.start",
			WorkspaceID: workspace.ID,
			RiskLevel:   "low",
			Summary:     "已启动 Codex review",
			Payload:     map[string]any{"sessionId": session.ID, "threadId": session.CodexThreadID, "target": strings.TrimSpace(req.Target)},
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "未找到会话路由")
}

func (s *Server) handleUpdateCodexSessionSettings(w http.ResponseWriter, r *http.Request, session storage.CodexSession, workspace storage.Workspace, hasWorkspace bool) {
	if session.Archived {
		writeError(w, http.StatusConflict, "session_archived", "该会话已归档")
		return
	}
	var req struct {
		Model             string `json:"model"`
		ServiceTier       string `json:"serviceTier"`
		ApprovalPolicy    string `json:"approvalPolicy"`
		ApprovalsReviewer string `json:"approvalsReviewer"`
		Sandbox           string `json:"sandbox"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Sandbox = strings.TrimSpace(req.Sandbox)
	if req.Sandbox == "" {
		req.Sandbox = session.Sandbox
	}
	if !validSandbox(req.Sandbox) {
		writeError(w, http.StatusBadRequest, "invalid_sandbox", "只支持只读和工作区可写两种沙箱")
		return
	}
	if req.Sandbox == "workspace-write" && (!hasWorkspace || !workspace.AllowCodexWrite) {
		writeError(w, http.StatusForbidden, "permission_denied", "workspace-write 需要选择已允许写入的项目")
		return
	}
	updated, err := s.codex.UpdateSessionSettings(r.Context(), session, workspace, strings.TrimSpace(req.Model), strings.TrimSpace(req.ServiceTier), normalizeApprovalPolicy(req.ApprovalPolicy), normalizeApprovalsReviewer(req.ApprovalsReviewer), req.Sandbox)
	if err != nil {
		writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "codex.session.settings.update",
		WorkspaceID: workspace.ID,
		RiskLevel:   riskForSandbox(updated.Sandbox),
		Summary:     "已更新 Codex 会话设置",
		Payload: map[string]any{
			"sessionId":         updated.ID,
			"threadId":          updated.CodexThreadID,
			"model":             updated.Model,
			"serviceTier":       updated.ServiceTier,
			"approvalPolicy":    updated.ApprovalPolicy,
			"approvalsReviewer": updated.ApprovalsReviewer,
			"sandbox":           updated.Sandbox,
		},
	})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) sessionWorkspace(ctx context.Context, session storage.CodexSession) (storage.Workspace, bool, error) {
	if strings.TrimSpace(session.WorkspaceID) == "" {
		return storage.Workspace{}, false, nil
	}
	workspace, err := s.store.GetWorkspace(ctx, session.WorkspaceID)
	return workspace, true, err
}

func (s *Server) handleCreateCodexTurn(w http.ResponseWriter, r *http.Request, session storage.CodexSession, workspace storage.Workspace, hasWorkspace bool) {
	if session.Archived {
		writeError(w, http.StatusConflict, "session_archived", "该会话已归档")
		return
	}
	if session.Sandbox == "workspace-write" && (!hasWorkspace || !workspace.AllowCodexWrite) {
		writeError(w, http.StatusForbidden, "permission_denied", "workspace-write 需要选择已允许写入的项目")
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
		Mode   string `json:"mode"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "提示词不能为空")
		return
	}
	if s.handleCodexComposerCommand(w, r, session, workspace, req.Prompt) {
		return
	}
	turn, action, err := s.codex.SendTurn(r.Context(), session, workspace, req.Prompt, req.Mode)
	if err != nil {
		writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "codex.turn.send",
		WorkspaceID: workspace.ID,
		RiskLevel:   riskForSandbox(session.Sandbox),
		Summary:     "已发送 Codex 对话",
		Payload:     map[string]any{"sessionId": session.ID, "turnId": turn.CodexTurnID, "action": action, "promptPreview": preview(req.Prompt, 120)},
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"turn": turn, "action": action})
}

func (s *Server) handleCodexGitAction(w http.ResponseWriter, r *http.Request, session storage.CodexSession, workspace storage.Workspace, hasWorkspace bool) {
	if session.Archived {
		writeError(w, http.StatusConflict, "session_archived", "该会话已归档")
		return
	}
	if !hasWorkspace || workspace.RootPath == "" {
		writeError(w, http.StatusBadRequest, "workspace_required", "该会话没有绑定项目")
		return
	}
	if !workspace.AllowCodexWrite {
		writeError(w, http.StatusForbidden, "permission_denied", "该项目未允许 Codex/Git 写入")
		return
	}
	var req struct {
		Action  string   `json:"action"`
		Paths   []string `json:"paths"`
		Message string   `json:"message"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := workspaces.RunGitAction(r.Context(), workspace.RootPath, strings.TrimSpace(req.Action), req.Paths, req.Message)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_git_action", err.Error())
		return
	}
	risk := "low"
	if result.Action == "commit" {
		risk = "medium"
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "codex.git.action",
		WorkspaceID: workspace.ID,
		RiskLevel:   risk,
		Summary:     "已执行会话 Git 操作",
		Payload: map[string]any{
			"sessionId":      session.ID,
			"threadId":       session.CodexThreadID,
			"action":         result.Action,
			"pathCount":      len(req.Paths),
			"messagePreview": preview(req.Message, 120),
			"failed":         result.Error != "",
		},
	})
	s.appendCodexSessionEvent(r.Context(), session.ID, "git.action.completed", map[string]any{
		"action":    result.Action,
		"pathCount": len(req.Paths),
		"failed":    result.Error != "",
		"error":     result.Error,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCodexComposerCommand(w http.ResponseWriter, r *http.Request, session storage.CodexSession, workspace storage.Workspace, prompt string) bool {
	parts := strings.Fields(prompt)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return false
	}
	command := strings.ToLower(parts[0])
	switch command {
	case "/status":
		pending, _ := s.store.ListCodexApprovals(r.Context(), "pending", 100)
		count := 0
		for _, item := range pending {
			if item.SessionID == session.ID {
				count++
			}
		}
		s.appendCodexSessionEvent(r.Context(), session.ID, "composer.status", map[string]any{
			"threadId":          session.CodexThreadID,
			"status":            session.Status,
			"model":             session.Model,
			"serviceTier":       session.ServiceTier,
			"approvalPolicy":    session.ApprovalPolicy,
			"approvalsReviewer": session.ApprovalsReviewer,
			"sandbox":           session.Sandbox,
			"tokenUsage":        session.TokenUsage,
			"pendingApprovals":  count,
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"action": "status"})
		return true
	case "/review":
		if err := s.codex.StartReview(r.Context(), session, "uncommittedChanges"); err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return true
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType:   "codex.review.start",
			WorkspaceID: workspace.ID,
			RiskLevel:   "low",
			Summary:     "已通过 slash command 启动 Codex review",
			Payload:     map[string]any{"sessionId": session.ID, "threadId": session.CodexThreadID, "source": "slash"},
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"action": "review"})
		return true
	case "/mcp":
		result, err := s.codex.Capability(r.Context(), "mcpServerStatus/list", map[string]any{"limit": 80})
		payload := map[string]any{"threadId": session.CodexThreadID}
		if err != nil {
			payload["summary"] = "MCP 状态读取失败：" + err.Error()
		} else {
			payload["summary"] = "MCP 状态已读取"
			if data, ok := result["data"].([]any); ok {
				payload["count"] = len(data)
			}
		}
		s.appendCodexSessionEvent(r.Context(), session.ID, "composer.status", payload)
		writeJSON(w, http.StatusAccepted, map[string]any{"action": "mcp"})
		return true
	case "/goal":
		if session.CodexThreadID == "" {
			writeError(w, http.StatusConflict, "thread_missing", "会话还没有 Codex thread")
			return true
		}
		objective := strings.TrimSpace(strings.TrimPrefix(prompt, parts[0]))
		method := "thread/goal/get"
		params := map[string]any{"threadId": session.CodexThreadID}
		if objective != "" {
			method = "thread/goal/set"
			params["objective"] = objective
		}
		result, err := s.codex.Capability(r.Context(), method, params)
		if err != nil {
			writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
			return true
		}
		s.appendCodexSessionEvent(r.Context(), session.ID, "composer.status", map[string]any{"threadId": session.CodexThreadID, "summary": "Goal 已处理", "goal": result})
		writeJSON(w, http.StatusAccepted, map[string]any{"action": "goal"})
		return true
	default:
		return false
	}
}

func (s *Server) handleCreateExecJob(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		WorkspaceID string `json:"workspaceId"`
		Prompt      string `json:"prompt"`
		Sandbox     string `json:"sandbox"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "提示词不能为空")
		return
	}
	if req.Sandbox == "" {
		req.Sandbox = "read-only"
	}
	if !validSandbox(req.Sandbox) {
		writeError(w, http.StatusBadRequest, "invalid_sandbox", "MVP 只支持只读和工作区可写两种沙箱")
		return
	}
	workspace, err := s.store.GetWorkspace(r.Context(), req.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace_not_found", "未找到项目")
		return
	}
	if req.Sandbox == "workspace-write" && !workspace.AllowCodexWrite {
		writeError(w, http.StatusForbidden, "permission_denied", "该项目未允许 Codex 写入任务")
		return
	}
	job, err := s.store.CreateExecJob(r.Context(), workspace.ID, preview(req.Prompt, 120))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "codex.exec.start",
		WorkspaceID: workspace.ID,
		RiskLevel:   riskForSandbox(req.Sandbox),
		Summary:     "已启动 Codex 一次性任务",
		Payload:     map[string]any{"jobId": job.ID, "sandbox": req.Sandbox, "promptPreview": job.PromptPreview},
	})
	s.codex.StartExecJob(r.Context(), job, workspace, req.Prompt, req.Sandbox)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleExecJobSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/codex/exec-jobs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到任务路由")
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "interrupt" {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		interrupted := s.codex.Interrupt(parts[0])
		writeJSON(w, http.StatusOK, map[string]any{"interrupted": interrupted})
		return
	}
	job, err := s.store.GetExecJob(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "task_not_found", "未找到任务")
		return
	}
	writeJSON(w, http.StatusOK, job)
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

func (s *Server) appendCodexSessionEvent(ctx context.Context, sessionID, eventType string, payload map[string]any) {
	event, err := s.store.AppendEvent(ctx, "codex_session", sessionID, eventType, sanitizeEventPayload(payload))
	if err == nil {
		s.hub.Publish(event)
	}
}

func (s *Server) handlePendingApprovals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListCodexApprovals(r.Context(), "pending", 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleApprovalSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/approvals/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "resolve" {
		writeError(w, http.StatusNotFound, "not_found", "未找到审批路由")
		return
	}
	approval, err := s.store.GetCodexApproval(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "approval_not_found", "未找到审批请求")
		return
	}
	if approval.Status != "pending" {
		writeError(w, http.StatusConflict, "approval_resolved", "审批请求已处理")
		return
	}
	var req struct {
		Action  string         `json:"action"`
		Payload map[string]any `json:"payload"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
	if err := s.codex.ResolveApproval(r.Context(), approval, req.Action, req.Payload); err != nil {
		writeError(w, http.StatusBadGateway, "codex_app_server_failed", err.Error())
		return
	}
	status := approvalStatusFromAction(req.Action)
	decision := map[string]any{"action": req.Action}
	for key, value := range req.Payload {
		decision[key] = value
	}
	updated, err := s.store.ResolveCodexApproval(r.Context(), approval.ID, status, decision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType:   "codex.approval.resolve",
		WorkspaceID: "",
		RiskLevel:   approval.RiskLevel,
		Summary:     "已处理 Codex 审批请求",
		Payload:     map[string]any{"approvalId": approval.ID, "sessionId": approval.SessionID, "requestType": approval.RequestType, "status": status},
	})
	writeJSON(w, http.StatusOK, updated)
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
			"addr":          s.cfg.Addr,
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
	s.codex.Configure(settings.CodexBinary, settings.CodexHome)
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
			"codexBinary":  updated.CodexBinary,
			"codexHome":    updated.CodexHome,
			"cookieSecure": updated.CookieSecure,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"runtime": updated})
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
		Backend           string `json:"backend"`
		S3ProviderLabel   string `json:"s3ProviderLabel"`
		S3Bucket          string `json:"s3Bucket"`
		S3Region          string `json:"s3Region"`
		S3Endpoint        string `json:"s3Endpoint"`
		S3Prefix          string `json:"s3Prefix"`
		S3ForcePathStyle  bool   `json:"s3ForcePathStyle"`
		S3AccessKeyID     string `json:"s3AccessKeyId"`
		S3SecretAccessKey string `json:"s3SecretAccessKey"`
		S3SessionToken    string `json:"s3SessionToken"`
		S3AccessMode      string `json:"s3AccessMode"`
		FallbackToLocal   bool   `json:"fallbackToLocal"`
		ClearSecret       bool   `json:"clearSecret"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := storage.ImageStorageSettings{
		Backend:           req.Backend,
		S3ProviderLabel:   req.S3ProviderLabel,
		S3Bucket:          req.S3Bucket,
		S3Region:          req.S3Region,
		S3Endpoint:        req.S3Endpoint,
		S3Prefix:          req.S3Prefix,
		S3ForcePathStyle:  req.S3ForcePathStyle,
		S3AccessKeyID:     req.S3AccessKeyID,
		S3SecretAccessKey: req.S3SecretAccessKey,
		S3SessionToken:    req.S3SessionToken,
		S3AccessMode:      req.S3AccessMode,
		FallbackToLocal:   req.FallbackToLocal,
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
			"providerLabel": updated.S3ProviderLabel,
			"bucket":        updated.S3Bucket,
			"endpoint":      updated.S3Endpoint,
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
			Payload:   map[string]any{"backend": settings.Backend, "bucket": settings.S3Bucket, "endpoint": settings.S3Endpoint, "error": err.Error()},
		})
		writeError(w, http.StatusBadGateway, "images_storage_test_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "images.storage.tested",
		RiskLevel: "low",
		Summary:   "Images 对象存储连接测试通过",
		Payload:   map[string]any{"backend": settings.Backend, "bucket": settings.S3Bucket, "endpoint": settings.S3Endpoint},
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
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "status": s.images.Status(r.Context())})
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
				Payload:   map[string]any{"assetId": asset.ID, "jobId": asset.JobID, "error": err.Error()},
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
	fullPath, ok := s.images.Assets.AssetPath(name)
	if !ok {
		writeError(w, http.StatusNotFound, "image_asset_not_found", "未找到图片资产")
		return
	}
	asset, err := s.store.GetImageAssetByLocalName(r.Context(), name)
	if err == nil && asset.Private && !s.requireImagePrivateUnlocked(w, ctx) {
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, fullPath)
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
				s.log.Error("panic recovered", "panic", recovered)
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

func writeWorkspacePathError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspaces.ErrPathNotFound):
		writeError(w, http.StatusBadRequest, "workspace_path_missing", "目录不存在；如需由 Phantom Lancer 创建，请勾选“目录不存在时创建”")
	case errors.Is(err, workspaces.ErrPathNotDirectory):
		writeError(w, http.StatusBadRequest, "workspace_path_invalid", "路径必须是目录")
	case errors.Is(err, workspaces.ErrPathOutOfBoundary):
		writeError(w, http.StatusBadRequest, "path_out_of_boundary", "路径必须落在允许根目录内")
	default:
		writeError(w, http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
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

func preview(value string, max int) string {
	value = redactPreviewText(strings.TrimSpace(value))
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

var previewSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)((?:password|token|secret|api[_-]?key)=)[^\s&]+`), `${1}[redacted]`},
	{regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`), `[redacted]`},
}

func redactPreviewText(value string) string {
	for _, item := range previewSecretPatterns {
		value = item.pattern.ReplaceAllString(value, item.replacement)
	}
	return value
}

func sanitizeEventPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	if sanitized, ok := redactEventValue(payload).(map[string]any); ok {
		return sanitized
	}
	return map[string]any{}
}

func redactEventValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveEventKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactEventValue(item)
		}
		return out
	case []any:
		limit := len(typed)
		if limit > 100 {
			limit = 100
		}
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, redactEventValue(typed[i]))
		}
		return out
	case string:
		redacted := redactPreviewText(typed)
		if len(redacted) > 4000 {
			return redacted[:4000] + "...[truncated]"
		}
		return redacted
	default:
		return typed
	}
}

func sensitiveEventKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	for _, marker := range []string{"apikey", "authorization", "cookie", "csrftoken", "sessiontoken", "password", "secret", "accesstoken", "refreshtoken", "privatekey", "presigned"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func workspaceNameFromPath(rootPath string) string {
	name := strings.TrimSpace(filepath.Base(rootPath))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "新项目"
	}
	return name
}

func riskForSandbox(sandbox string) string {
	if sandbox == "workspace-write" {
		return "medium"
	}
	return "low"
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

func validSandbox(sandbox string) bool {
	return sandbox == "read-only" || sandbox == "workspace-write"
}

func normalizeApprovalPolicy(value string) string {
	switch strings.TrimSpace(value) {
	case "untrusted", "on-request":
		return strings.TrimSpace(value)
	default:
		return "on-request"
	}
}

func normalizeApprovalsReviewer(value string) string {
	switch strings.TrimSpace(value) {
	case "user", "auto_review":
		return strings.TrimSpace(value)
	default:
		return "user"
	}
}

func approvalStatusFromAction(action string) string {
	switch strings.TrimSpace(action) {
	case "allow", "accept", "allow_session", "allowForSession", "acceptForSession":
		return "approved"
	case "cancel":
		return "cancelled"
	default:
		return "declined"
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}
