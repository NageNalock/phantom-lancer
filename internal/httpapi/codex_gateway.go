package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/codexgateway"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

func (s *Server) handleCodexGatewayStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	status, err := s.codexGateway.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGetCodexGatewaySettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	settings, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (s *Server) handleUpdateCodexGatewaySettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	current, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	var req struct {
		Enabled               *bool   `json:"enabled"`
		BaseURL               *string `json:"baseUrl"`
		OAuthAuthURL          *string `json:"oauthAuthUrl"`
		OAuthTokenURL         *string `json:"oauthTokenUrl"`
		OAuthClientID         *string `json:"oauthClientId"`
		OAuthRedirectURI      *string `json:"oauthRedirectUri"`
		RequestTimeoutSeconds *int    `json:"requestTimeoutSeconds"`
		RefreshMarginSeconds  *int    `json:"refreshMarginSeconds"`
		DefaultInstructions   *string `json:"defaultInstructions"`
		InstallationID        *string `json:"installationId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := current
	settings.ID = "default"
	if req.Enabled != nil {
		settings.Enabled = *req.Enabled
	}
	if req.BaseURL != nil {
		settings.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if req.OAuthAuthURL != nil {
		settings.OAuthAuthURL = strings.TrimSpace(*req.OAuthAuthURL)
	}
	if req.OAuthTokenURL != nil {
		settings.OAuthTokenURL = strings.TrimSpace(*req.OAuthTokenURL)
	}
	if req.OAuthClientID != nil {
		settings.OAuthClientID = strings.TrimSpace(*req.OAuthClientID)
	}
	if req.OAuthRedirectURI != nil {
		settings.OAuthRedirectURI = strings.TrimSpace(*req.OAuthRedirectURI)
	}
	if req.RequestTimeoutSeconds != nil {
		settings.RequestTimeoutSeconds = *req.RequestTimeoutSeconds
	}
	if req.RefreshMarginSeconds != nil {
		settings.RefreshMarginSeconds = *req.RefreshMarginSeconds
	}
	if req.DefaultInstructions != nil {
		settings.DefaultInstructions = strings.TrimSpace(*req.DefaultInstructions)
	}
	if req.InstallationID != nil {
		settings.InstallationID = strings.TrimSpace(*req.InstallationID)
	}
	if strings.TrimSpace(settings.BaseURL) == "" {
		settings.BaseURL = current.BaseURL
	}
	if !validOptionalURL(settings.BaseURL) || !validOptionalURL(settings.OAuthAuthURL) || !validOptionalURL(settings.OAuthTokenURL) || !validOptionalURL(settings.OAuthRedirectURI) {
		writeError(w, http.StatusBadRequest, "invalid_url", "Codex Gateway URL 设置不合法")
		return
	}
	updated, err := s.store.UpdateCodexGatewaySettings(r.Context(), settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.settings.updated",
		RiskLevel: "medium",
		Summary:   "已更新 Codex Gateway 设置",
		Payload: map[string]any{
			"enabled": updated.Enabled,
			"baseURL": safelog.URLLabel(updated.BaseURL),
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated})
}

func validOptionalURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	_, err := url.ParseRequestURI(value)
	return err == nil
}

func (s *Server) handleListCodexGatewayAPIKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	keys, err := s.store.ListCodexGatewayAPIKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": keys})
}

func (s *Server) handleCreateCodexGatewayAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	key, token, err := s.codexGateway.CreateAPIKey(r.Context(), req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.api_key.created",
		RiskLevel: "medium",
		Summary:   "已创建 Codex Gateway API key",
		Payload:   map[string]any{"keyId": key.ID, "name": key.Name},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "token": token})
}

func (s *Server) handleCodexGatewayAPIKeySubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/codex-gateway/api-keys/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 API key")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "rotate" && r.Method == http.MethodPost {
		key, token, err := s.codexGateway.RotateAPIKey(r.Context(), id)
		if err != nil {
			writeStoreError(w, err, "API key 轮换失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "codex_gateway.api_key.rotated",
			RiskLevel: "medium",
			Summary:   "已轮换 Codex Gateway API key",
			Payload:   map[string]any{"keyId": key.ID, "name": key.Name},
		})
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "token": token})
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not_found", "未找到 API key 路由")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Status string `json:"status"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Status != "active" && req.Status != "disabled" {
			writeError(w, http.StatusBadRequest, "invalid_status", "API key 状态不合法")
			return
		}
		key, err := s.store.UpdateCodexGatewayAPIKeyStatus(r.Context(), id, req.Status)
		if err != nil {
			writeStoreError(w, err, "API key 更新失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "codex_gateway.api_key.updated",
			RiskLevel: "medium",
			Summary:   "已更新 Codex Gateway API key",
			Payload:   map[string]any{"keyId": key.ID, "status": key.Status},
		})
		writeJSON(w, http.StatusOK, map[string]any{"key": key})
	case http.MethodDelete:
		if err := s.store.DeleteCodexGatewayAPIKey(r.Context(), id); err != nil {
			writeStoreError(w, err, "API key 删除失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "codex_gateway.api_key.deleted",
			RiskLevel: "medium",
			Summary:   "已删除 Codex Gateway API key",
			Payload:   map[string]any{"keyId": id},
		})
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleListCodexGatewayAccounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	accounts, err := s.store.ListCodexGatewayAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": accounts})
}

func (s *Server) handleCreateCodexGatewayAccount(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Label        string `json:"label"`
		Status       string `json:"status"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    string `json:"expiresAt"`
		Plan         string `json:"plan"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AccessToken) == "" && strings.TrimSpace(req.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "invalid_account", "access token 或 refresh token 至少填写一个")
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if !validGatewayAccountStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "invalid_status", "账号状态不合法")
		return
	}
	account, err := s.store.CreateCodexGatewayAccount(r.Context(), storage.CodexGatewayAccountInput{
		Label: req.Label, Status: req.Status, AccessToken: normalizeBearer(req.AccessToken), RefreshToken: strings.TrimSpace(req.RefreshToken), ExpiresAt: req.ExpiresAt, Plan: req.Plan,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.account.created",
		RiskLevel: "medium",
		Summary:   "已创建 Codex Gateway 账号",
		Payload:   map[string]any{"accountId": account.ID, "status": account.Status, "hasRefreshToken": account.HasRefreshToken},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"account": account})
}

func (s *Server) handleCodexGatewayAccountSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/codex-gateway/accounts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到账号")
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		switch parts[1] {
		case "refresh":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
				return
			}
			account, err := s.codexGateway.RefreshAccount(r.Context(), id)
			if err != nil {
				writeStoreError(w, err, "账号刷新失败")
				return
			}
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "codex_gateway.account.refresh_requested",
				RiskLevel: "medium",
				Summary:   "已刷新 Codex Gateway 账号",
				Payload:   map[string]any{"accountId": id, "status": account.Status},
			})
			writeJSON(w, http.StatusOK, map[string]any{"account": account})
		case "check":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
				return
			}
			account, err := s.codexGateway.CheckAccount(r.Context(), id)
			if err != nil {
				writeStoreError(w, err, "账号检查失败")
				return
			}
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "codex_gateway.account.check_requested",
				RiskLevel: "low",
				Summary:   "已检查 Codex Gateway 账号",
				Payload:   map[string]any{"accountId": id, "status": account.Status, "plan": account.Plan},
			})
			writeJSON(w, http.StatusOK, map[string]any{"account": account})
		default:
			writeError(w, http.StatusNotFound, "not_found", "未找到账号路由")
		}
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not_found", "未找到账号路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Label        *string `json:"label"`
			Status       *string `json:"status"`
			AccessToken  *string `json:"accessToken"`
			RefreshToken *string `json:"refreshToken"`
			ExpiresAt    *string `json:"expiresAt"`
			ClearExpires bool    `json:"clearExpiresAt"`
			Plan         *string `json:"plan"`
			ClearPlan    bool    `json:"clearPlan"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Status != nil && !validGatewayAccountStatus(*req.Status) {
			writeError(w, http.StatusBadRequest, "invalid_status", "账号状态不合法")
			return
		}
		if req.AccessToken != nil {
			value := normalizeBearer(*req.AccessToken)
			req.AccessToken = &value
		}
		account, err := s.store.UpdateCodexGatewayAccount(r.Context(), id, storage.CodexGatewayAccountPatch{
			Label: req.Label, Status: req.Status, AccessToken: req.AccessToken, RefreshToken: req.RefreshToken, ExpiresAt: req.ExpiresAt, ClearExpires: req.ClearExpires, Plan: req.Plan, ClearPlan: req.ClearPlan,
		})
		if err != nil {
			writeStoreError(w, err, "账号更新失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "codex_gateway.account.updated",
			RiskLevel: "medium",
			Summary:   "已更新 Codex Gateway 账号",
			Payload:   map[string]any{"accountId": account.ID, "status": account.Status},
		})
		writeJSON(w, http.StatusOK, map[string]any{"account": account})
	case http.MethodDelete:
		if err := s.store.DeleteCodexGatewayAccount(r.Context(), id); err != nil {
			writeStoreError(w, err, "账号删除失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "codex_gateway.account.deleted",
			RiskLevel: "medium",
			Summary:   "已删除 Codex Gateway 账号",
			Payload:   map[string]any{"accountId": id},
		})
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func validGatewayAccountStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "active", "disabled", "invalid", "rate_limited":
		return true
	default:
		return false
	}
}

func normalizeBearer(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return strings.TrimSpace(value[len("bearer "):])
	}
	return value
}

func (s *Server) handleCodexGatewayModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	models, err := s.codexGateway.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": models})
}

func (s *Server) handleRefreshCodexGatewayModels(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.codexGateway.RefreshModelCatalog(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "model_refresh_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.models.refresh_requested",
		RiskLevel: "low",
		Summary:   "已刷新 Codex Gateway 模型目录",
		Payload:   map[string]any{"accounts": result.Accounts, "success": result.Success},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCodexGatewayRequestLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	logs, err := s.store.ListCodexGatewayRequestLogs(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": logs})
}

func (s *Server) handleCodexGatewayChatTest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := gatewayRequestID(r)
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var body struct {
		AccountID string `json:"accountId"`
		codexgateway.ChatCompletionRequest
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "JSON 请求体无效")
		return
	}
	if strings.TrimSpace(body.AccountID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_account", "请选择 Codex Gateway 账号")
		return
	}
	chatReq := body.ChatCompletionRequest
	chatReq.Stream = true
	if err := codexgateway.ValidateChatRequest(chatReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	settings, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	payload := codexgateway.ChatToResponsesPayload(chatReq, true, settings.DefaultInstructions)
	route, resp, failure, err := s.codexGateway.SendResponses(r.Context(), chatReq.Model, body.AccountID, payload)
	if err != nil {
		s.handleGatewayRouteError(w, r, requestID, start, "admin.chat_test", chatReq.Model, body.AccountID, route, err, true, false)
		return
	}
	if failure != nil {
		s.logGatewayRequest(r.Context(), requestID, "admin.chat_test", chatReq.Model, route.Account.ID, clientIP(r), failure.Status, failure.Code, codexgateway.ErrorSourceOpenAI, failure.Message, start, true, codexgateway.Usage{})
		writeError(w, failure.Status, failure.Code, failure.Message)
		return
	}
	defer resp.Body.Close()
	_ = s.store.MarkCodexGatewayAccountUsed(r.Context(), route.Account.ID)
	usage, streamErr := codexgateway.StreamChatFromResponses(w, resp.Body, chatReq.Model)
	status := http.StatusOK
	code := ""
	message := ""
	if streamErr != nil {
		status = http.StatusBadGateway
		code = "stream_error"
		message = streamErr.Error()
	}
	s.logGatewayRequest(r.Context(), requestID, "admin.chat_test", chatReq.Model, route.Account.ID, clientIP(r), status, code, "", message, start, true, usage)
}

func (s *Server) requireCodexGatewayPublicToken(w http.ResponseWriter, r *http.Request, requestID, apiKind, model string, start time.Time, streamed bool) bool {
	token := gatewayBearerToken(r)
	if _, ok, err := s.codexGateway.VerifyPublicToken(r.Context(), token); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "读取 API key 配置失败")
		return false
	} else if !ok {
		s.logGatewayRequest(r.Context(), requestID, apiKind, model, "", clientIP(r), http.StatusUnauthorized, "unauthorized", codexgateway.ErrorSourceClient, "无效 API key", start, streamed, codexgateway.Usage{})
		writeOpenAIError(w, http.StatusUnauthorized, "unauthorized", "无效 API key")
		return false
	}
	return true
}

func gatewayBearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if value == "" {
		return ""
	}
	parts := strings.Fields(value)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

func (s *Server) handleCodexGatewayPublicModels(w http.ResponseWriter, r *http.Request) {
	requestID := gatewayRequestID(r)
	start := time.Now()
	if !s.requireCodexGatewayPublicToken(w, r, requestID, "models", "", start, false) {
		return
	}
	settings, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "读取设置失败")
		return
	}
	if !settings.Enabled {
		writeOpenAIError(w, http.StatusServiceUnavailable, "gateway_disabled", "Codex Gateway 未启用")
		return
	}
	models, err := s.codexGateway.ListModels(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "读取模型目录失败")
		return
	}
	writeJSON(w, http.StatusOK, codexgateway.ModelList{Object: "list", Data: gatewayOpenAIModels(models)})
}

func (s *Server) handleCodexGatewayPublicModel(w http.ResponseWriter, r *http.Request) {
	requestID := gatewayRequestID(r)
	start := time.Now()
	modelID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	if !s.requireCodexGatewayPublicToken(w, r, requestID, "models", modelID, start, false) {
		return
	}
	settings, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "读取设置失败")
		return
	}
	if !settings.Enabled {
		writeOpenAIError(w, http.StatusServiceUnavailable, "gateway_disabled", "Codex Gateway 未启用")
		return
	}
	model, err := s.store.GetCodexGatewayModel(r.Context(), modelID)
	if errors.Is(err, storage.ErrNotFound) {
		writeOpenAIError(w, http.StatusNotFound, "not_found", "模型不存在")
		return
	}
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "读取模型目录失败")
		return
	}
	writeJSON(w, http.StatusOK, gatewayOpenAIModel(model))
}

func (s *Server) handleCodexGatewayChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := gatewayRequestID(r)
	if !s.requireCodexGatewayPublicToken(w, r, requestID, "chat.completions", "", start, false) {
		return
	}
	var body codexgateway.ChatCompletionRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		s.logGatewayRequest(r.Context(), requestID, "chat.completions", "", "", clientIP(r), http.StatusBadRequest, "invalid_json", codexgateway.ErrorSourceClient, err.Error(), start, false, codexgateway.Usage{})
		writeOpenAIError(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	if err := codexgateway.ValidateChatRequest(body); err != nil {
		s.logGatewayRequest(r.Context(), requestID, "chat.completions", body.Model, "", clientIP(r), http.StatusBadRequest, "invalid_request", codexgateway.ErrorSourceClient, err.Error(), start, body.Stream, codexgateway.Usage{})
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	settings, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "读取设置失败")
		return
	}
	payload := codexgateway.ChatToResponsesPayload(body, true, settings.DefaultInstructions)
	route, resp, failure, err := s.codexGateway.SendResponses(r.Context(), body.Model, "", payload)
	if err != nil {
		s.handleGatewayRouteError(w, r, requestID, start, "chat.completions", body.Model, "", route, err, body.Stream, true)
		return
	}
	if failure != nil {
		s.logGatewayRequest(r.Context(), requestID, "chat.completions", body.Model, route.Account.ID, clientIP(r), failure.Status, failure.Code, codexgateway.ErrorSourceOpenAI, failure.Message, start, body.Stream, codexgateway.Usage{})
		writeOpenAIError(w, failure.Status, failure.Code, failure.Message)
		return
	}
	defer resp.Body.Close()
	_ = s.store.MarkCodexGatewayAccountUsed(r.Context(), route.Account.ID)
	if body.Stream {
		usage, err := codexgateway.StreamChatFromResponses(w, resp.Body, body.Model)
		status := http.StatusOK
		code := ""
		message := ""
		if err != nil {
			status = http.StatusBadGateway
			code = "stream_error"
			message = err.Error()
		}
		s.logGatewayRequest(r.Context(), requestID, "chat.completions", body.Model, route.Account.ID, clientIP(r), status, code, "", message, start, true, usage)
		return
	}
	text, usage, err := codexgateway.CollectTextFromResponses(resp.Body)
	if err != nil {
		s.logGatewayRequest(r.Context(), requestID, "chat.completions", body.Model, route.Account.ID, clientIP(r), http.StatusBadGateway, "upstream_decode_error", codexgateway.ErrorSourceOpenAI, err.Error(), start, false, codexgateway.Usage{})
		writeOpenAIError(w, http.StatusBadGateway, "upstream_decode_error", "Codex 上游响应无法解析")
		return
	}
	s.logGatewayRequest(r.Context(), requestID, "chat.completions", body.Model, route.Account.ID, clientIP(r), http.StatusOK, "", "", "", start, false, usage)
	writeJSON(w, http.StatusOK, codexgateway.BuildChatResponse("chatcmpl_"+requestID, body.Model, text, usage))
}

func (s *Server) handleCodexGatewayResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := gatewayRequestID(r)
	if !s.requireCodexGatewayPublicToken(w, r, requestID, "responses", "", start, false) {
		return
	}
	var body map[string]any
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		s.logGatewayRequest(r.Context(), requestID, "responses", "", "", clientIP(r), http.StatusBadRequest, "invalid_json", codexgateway.ErrorSourceClient, err.Error(), start, false, codexgateway.Usage{})
		writeOpenAIError(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	payload, model, stream, err := codexgateway.NormalizeResponsesPayload(body)
	if err != nil {
		s.logGatewayRequest(r.Context(), requestID, "responses", model, "", clientIP(r), http.StatusBadRequest, "invalid_request", codexgateway.ErrorSourceClient, err.Error(), start, stream, codexgateway.Usage{})
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	route, resp, failure, err := s.codexGateway.SendResponses(r.Context(), model, "", payload)
	if err != nil {
		s.handleGatewayRouteError(w, r, requestID, start, "responses", model, "", route, err, stream, true)
		return
	}
	if failure != nil {
		s.logGatewayRequest(r.Context(), requestID, "responses", model, route.Account.ID, clientIP(r), failure.Status, failure.Code, codexgateway.ErrorSourceOpenAI, failure.Message, start, stream, codexgateway.Usage{})
		writeOpenAIError(w, failure.Status, failure.Code, failure.Message)
		return
	}
	defer resp.Body.Close()
	_ = s.store.MarkCodexGatewayAccountUsed(r.Context(), route.Account.ID)
	if stream {
		usage, err := codexgateway.RelaySSE(w, resp.Body)
		status := http.StatusOK
		code := ""
		message := ""
		if err != nil {
			status = http.StatusBadGateway
			code = "stream_error"
			message = err.Error()
		}
		s.logGatewayRequest(r.Context(), requestID, "responses", model, route.Account.ID, clientIP(r), status, code, "", message, start, true, usage)
		return
	}
	w.Header().Set("Content-Type", contentTypeOrJSON(resp.Header.Get("Content-Type")))
	w.WriteHeader(http.StatusOK)
	tee := &codexgateway.UsageCaptureWriter{ResponseWriter: w}
	_, copyErr := io.Copy(tee, resp.Body)
	status := http.StatusOK
	code := ""
	message := ""
	if copyErr != nil {
		status = http.StatusBadGateway
		code = "upstream_read_error"
		message = copyErr.Error()
	}
	s.logGatewayRequest(r.Context(), requestID, "responses", model, route.Account.ID, clientIP(r), status, code, "", message, start, false, tee.Usage())
}

func contentTypeOrJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "application/json; charset=utf-8"
	}
	return value
}

func (s *Server) handleGatewayRouteError(w http.ResponseWriter, r *http.Request, requestID string, start time.Time, apiKind, model, fallbackAccount string, route codexgateway.UpstreamRoute, err error, streamed bool, openAI bool) {
	var routeErr codexgateway.RouteError
	if errors.As(err, &routeErr) {
		accountID := route.Account.ID
		if accountID == "" {
			accountID = fallbackAccount
		}
		source := codexgateway.ClassifyErrorSource(routeErr.Code)
		s.logGatewayRequest(r.Context(), requestID, apiKind, model, accountID, clientIP(r), routeErr.Status, routeErr.Code, source, routeErr.Message, start, streamed, codexgateway.Usage{})
		if openAI {
			writeOpenAIError(w, routeErr.Status, routeErr.Code, routeErr.Message)
		} else {
			writeError(w, routeErr.Status, routeErr.Code, routeErr.Message)
		}
		return
	}
	accountID := route.Account.ID
	if accountID == "" {
		accountID = fallbackAccount
	}
	s.logGatewayRequest(r.Context(), requestID, apiKind, model, accountID, clientIP(r), http.StatusBadGateway, "upstream_transport_error", codexgateway.ErrorSourceOpenAI, err.Error(), start, streamed, codexgateway.Usage{})
	if openAI {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_transport_error", "Codex 上游请求失败")
	} else {
		writeError(w, http.StatusBadGateway, "upstream_transport_error", "Codex 上游请求失败")
	}
}

func (s *Server) logGatewayRequest(ctx context.Context, requestID, apiKind, model, accountID, sourceIP string, status int, code, source, message string, start time.Time, streamed bool, usage codexgateway.Usage) {
	s.codexGateway.LogRequest(context.WithoutCancel(ctx), storage.CodexGatewayRequestLogInput{
		RequestID: requestID, APIKind: apiKind, Model: model, AccountID: accountID, SourceIP: sourceIP, StatusCode: status, ErrorCode: code, ErrorSource: source, ErrorMessage: message, LatencyMS: int(time.Since(start) / time.Millisecond), Streamed: streamed, InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens,
	})
}

func gatewayRequestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Request-Id")); value != "" {
		return value
	}
	token, _, err := auth.NewToken()
	if err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	if len(token) > 16 {
		return token[:16]
	}
	return token
}

func gatewayOpenAIModels(models []storage.CodexGatewayModel) []codexgateway.Model {
	out := make([]codexgateway.Model, 0, len(models))
	for _, model := range models {
		out = append(out, gatewayOpenAIModel(model))
	}
	return out
}

func gatewayOpenAIModel(model storage.CodexGatewayModel) codexgateway.Model {
	ownedBy := model.OwnedBy
	if ownedBy == "" {
		ownedBy = "codex"
	}
	return codexgateway.Model{ID: model.ID, Object: "model", Created: 0, OwnedBy: ownedBy}
}

func writeOpenAIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, codexgateway.ErrorResponse{Error: codexgateway.ErrorBody{
		Message: message,
		Type:    codexgateway.OpenAIErrorType(status, code),
		Code:    code,
	}})
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", fallback)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", fallback)
}

type codexGatewayOAuthSession struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	CreatedAt    time.Time
	Exchanging   bool
}

type codexGatewayOAuthSessions struct {
	mu        sync.Mutex
	ttl       time.Duration
	sessions  map[string]codexGatewayOAuthSession
	completed map[string]time.Time
}

func newCodexGatewayOAuthSessions(ttl time.Duration) *codexGatewayOAuthSessions {
	return &codexGatewayOAuthSessions{ttl: ttl, sessions: map[string]codexGatewayOAuthSession{}, completed: map[string]time.Time{}}
}

func (s *codexGatewayOAuthSessions) create(redirectURI string) (codexGatewayOAuthSession, string, error) {
	challenge, err := codexgateway.NewPKCEChallenge()
	if err != nil {
		return codexGatewayOAuthSession{}, "", err
	}
	state, _, err := auth.NewToken()
	if err != nil {
		return codexGatewayOAuthSession{}, "", err
	}
	session := codexGatewayOAuthSession{State: state, CodeVerifier: challenge.CodeVerifier, RedirectURI: redirectURI, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(time.Now().UTC())
	s.sessions[state] = session
	return session, challenge.CodeChallenge, nil
}

func (s *codexGatewayOAuthSessions) acquire(state string) (codexGatewayOAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.gcLocked(now)
	session, ok := s.sessions[state]
	if !ok || now.Sub(session.CreatedAt) > s.ttl || session.Exchanging {
		return codexGatewayOAuthSession{}, false
	}
	session.Exchanging = true
	s.sessions[state] = session
	return session, true
}

func (s *codexGatewayOAuthSessions) release(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[state]
	if !ok {
		return
	}
	session.Exchanging = false
	s.sessions[state] = session
}

func (s *codexGatewayOAuthSessions) complete(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, state)
	s.completed[state] = time.Now().UTC()
}

func (s *codexGatewayOAuthSessions) isCompleted(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(time.Now().UTC())
	_, ok := s.completed[state]
	return ok
}

func (s *codexGatewayOAuthSessions) gcLocked(now time.Time) {
	for state, session := range s.sessions {
		if now.Sub(session.CreatedAt) > s.ttl {
			delete(s.sessions, state)
		}
	}
	for state, completedAt := range s.completed {
		if now.Sub(completedAt) > s.ttl {
			delete(s.completed, state)
		}
	}
}

func (s *Server) handleStartCodexGatewayOAuth(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	settings, err := s.store.GetCodexGatewaySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	redirectURI := strings.TrimSpace(settings.OAuthRedirectURI)
	if redirectURI == "" {
		redirectURI = requestOrigin(r) + "/api/codex-gateway/accounts/oauth/callback"
	}
	session, challenge, err := s.gatewayOAuth.create(redirectURI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "oauth_session_failed", "无法创建 OAuth 会话")
		return
	}
	authURL, err := codexgateway.BuildAuthorizationURL(codexgateway.OAuthAuthorizationOptions{
		AuthURL: settings.OAuthAuthURL, ClientID: settings.OAuthClientID, RedirectURI: session.RedirectURI, State: session.State, CodeChallenge: challenge,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "oauth_settings_invalid", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.account.oauth_started",
		RiskLevel: "medium",
		Summary:   "已启动 Codex Gateway OAuth 登录",
		Payload:   map[string]any{"redirectURI": session.RedirectURI, "expiresAt": session.CreatedAt.Add(s.gatewayOAuth.ttl).Format(time.RFC3339Nano)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"authUrl": authURL, "state": session.State, "redirectUri": session.RedirectURI, "expiresAt": session.CreatedAt.Add(s.gatewayOAuth.ttl).Format(time.RFC3339Nano)})
}

func (s *Server) handleRelayCodexGatewayOAuth(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		CallbackURL string `json:"callbackUrl"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	code, state, err := parseOAuthCallbackURL(req.CallbackURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_callback_url", err.Error())
		return
	}
	account, completed, err := s.exchangeGatewayOAuthCode(r.Context(), code, state)
	if err != nil {
		writeError(w, http.StatusBadGateway, "oauth_exchange_failed", "OAuth 授权码换取 token 失败")
		return
	}
	if completed {
		writeJSON(w, http.StatusOK, map[string]any{"completed": true})
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.account.oauth_imported",
		RiskLevel: "medium",
		Summary:   "已通过 OAuth 导入 Codex Gateway 账号",
		Payload:   map[string]any{"accountId": account.ID, "hasRefreshToken": account.HasRefreshToken},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"account": account})
}

func (s *Server) handleCodexGatewayOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code, state, err := parseOAuthCallbackURL(r.URL.String())
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(gatewayOAuthHTML(false, err.Error())))
		return
	}
	if _, _, err := s.exchangeGatewayOAuthCode(r.Context(), code, state); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(gatewayOAuthHTML(false, "OAuth 授权码换取 token 失败")))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(gatewayOAuthHTML(true, "")))
}

func (s *Server) exchangeGatewayOAuthCode(ctx context.Context, code, state string) (storage.CodexGatewayAccount, bool, error) {
	session, ok := s.gatewayOAuth.acquire(state)
	if !ok {
		if s.gatewayOAuth.isCompleted(state) {
			return storage.CodexGatewayAccount{}, true, nil
		}
		return storage.CodexGatewayAccount{}, false, errors.New("invalid or expired oauth session")
	}
	settings, err := s.store.GetCodexGatewaySettings(ctx)
	if err != nil {
		s.gatewayOAuth.release(state)
		return storage.CodexGatewayAccount{}, false, err
	}
	runtime := codexgateway.Runtime{
		BaseURL: settings.BaseURL, OAuthAuthURL: settings.OAuthAuthURL, OAuthTokenURL: settings.OAuthTokenURL, OAuthClientID: settings.OAuthClientID, Timeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second, InstallationID: settings.InstallationID,
	}
	exchangeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tokens, err := codexgateway.NewClient(runtime).ExchangeCode(exchangeCtx, code, session.CodeVerifier, session.RedirectURI)
	if err != nil {
		s.gatewayOAuth.release(state)
		return storage.CodexGatewayAccount{}, false, err
	}
	account, err := s.store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label: codexgateway.AccountOAuthLabel(tokens), Status: "active", AccessToken: strings.TrimSpace(tokens.AccessToken), RefreshToken: strings.TrimSpace(tokens.RefreshToken), ExpiresAt: gatewayExpiresAt(tokens.ExpiresIn),
	})
	if err != nil {
		s.gatewayOAuth.release(state)
		return storage.CodexGatewayAccount{}, false, err
	}
	s.gatewayOAuth.complete(state)
	return account, false, nil
}

func parseOAuthCallbackURL(rawURL string) (string, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", errors.New("回调 URL 不能为空")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", errors.New("回调 URL 不合法")
	}
	query := parsed.Query()
	if oauthErr := strings.TrimSpace(query.Get("error")); oauthErr != "" {
		description := strings.TrimSpace(query.Get("error_description"))
		if description == "" {
			description = oauthErr
		}
		return "", "", fmt.Errorf("OAuth 返回错误: %s", description)
	}
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	if code == "" || state == "" {
		return "", "", errors.New("回调 URL 缺少 code 或 state")
	}
	return code, state, nil
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}

func gatewayOAuthHTML(success bool, message string) string {
	if success {
		return `<!doctype html><html><head><meta charset="utf-8"><title>Codex Gateway login</title></head><body><p>Codex Gateway account imported.</p><script>if(window.opener){try{window.opener.postMessage({type:"codex-gateway-oauth-success"},"*")}catch(e){}}try{window.close()}catch(e){}</script></body></html>`
	}
	escaped := html.EscapeString(message)
	data, _ := json.Marshal(message)
	return `<!doctype html><html><head><meta charset="utf-8"><title>Codex Gateway login</title></head><body><p>` + escaped + `</p><script>if(window.opener){try{window.opener.postMessage({type:"codex-gateway-oauth-error",error:` + string(data) + `},"*")}catch(e){}}</script></body></html>`
}

func gatewayExpiresAt(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return time.Now().UTC().Add(time.Duration(seconds) * time.Second).Format(time.RFC3339Nano)
}
