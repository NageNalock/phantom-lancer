package codexgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

const (
	ErrorSourceClient  = "client"
	ErrorSourceAccount = "account"
	ErrorSourceOpenAI  = "openai"
	ErrorSourceService = "service"
)

type Service struct {
	Store            *storage.Store
	Log              *slog.Logger
	refreshMu        sync.Mutex
	refreshes        map[string]*refreshCall
	bgMu             sync.Mutex // guards bgCancel
	bgCancel         context.CancelFunc
	bgWg             sync.WaitGroup
	localCodexBinary string
	localCodexHome   string
	localCodexDir    string
	localCodexGate   chan struct{}
}

type refreshCall struct {
	done   chan struct{}
	secret storage.CodexGatewayAccountSecret
	err    error
}

func NewService(store *storage.Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{Store: store, Log: logger, refreshes: map[string]*refreshCall{}, localCodexGate: make(chan struct{}, 1)}
}

func (s *Service) WithLocalCodex(dataDir, binary, codexHome string) *Service {
	s.localCodexBinary = strings.TrimSpace(binary)
	if s.localCodexBinary == "" {
		s.localCodexBinary = "codex"
	}
	s.localCodexHome = strings.TrimSpace(codexHome)
	// ponytail: a fixed empty work directory keeps API turns away from user
	// workspaces without adding another owner setting. The only future upgrade
	// path is an explicit per-key workspace policy.
	s.localCodexDir = filepath.Join(strings.TrimSpace(dataDir), "codex-gateway", "work")
	return s
}

type LocalChatResult struct {
	Content   string
	ToolCalls json.RawMessage
	Usage     Usage
}

func (s *Service) RunLocalChat(ctx context.Context, req ChatCompletionRequest, defaultInstructions string) (LocalChatResult, error) {
	if s.localCodexBinary == "" || s.localCodexDir == "" {
		return LocalChatResult{}, RouteError{Status: http.StatusServiceUnavailable, Code: "local_codex_unavailable", Message: "本地 Codex Gateway 未配置"}
	}
	select {
	case s.localCodexGate <- struct{}{}:
		defer func() { <-s.localCodexGate }()
	case <-ctx.Done():
		return LocalChatResult{}, ctx.Err()
	}
	if err := os.MkdirAll(s.localCodexDir, 0o700); err != nil {
		return LocalChatResult{}, fmt.Errorf("prepare local Codex workdir: %w", err)
	}
	client, err := codexclient.StartAppServer(ctx, s.localCodexBinary, s.localCodexHome)
	if err != nil {
		return LocalChatResult{}, fmt.Errorf("start local Codex app-server: %w", err)
	}
	defer client.Close()
	if err := client.Initialize(ctx); err != nil {
		return LocalChatResult{}, fmt.Errorf("initialize local Codex app-server: %w", err)
	}

	threadRaw, err := client.Call(ctx, "thread/start", map[string]any{
		"cwd":              s.localCodexDir,
		"approvalPolicy":   "never",
		"sandbox":          "read-only",
		"model":            strings.TrimSpace(req.Model),
		"ephemeral":        true,
		"baseInstructions": localCodexBaseInstructions(defaultInstructions),
	})
	if err != nil {
		return LocalChatResult{}, fmt.Errorf("local Codex thread/start: %w", err)
	}
	threadID := localCodexObjectID(threadRaw, "thread")
	if threadID == "" {
		return LocalChatResult{}, errors.New("local Codex thread/start returned no thread id")
	}
	params := map[string]any{
		"threadId":       threadID,
		"input":          []map[string]any{{"type": "text", "text": localCodexChatPrompt(req)}},
		"approvalPolicy": "never",
		"sandboxPolicy":  map[string]any{"type": "readOnly"},
		"cwd":            s.localCodexDir,
		"model":          strings.TrimSpace(req.Model),
		"outputSchema":   localCodexOutputSchema(req),
	}
	if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
		params["effort"] = effort
	}
	if _, err := client.Call(ctx, "turn/start", params); err != nil {
		return LocalChatResult{}, fmt.Errorf("local Codex turn/start: %w", err)
	}
	return collectLocalCodexChat(ctx, client)
}

func localCodexBaseInstructions(fallback string) string {
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		fallback = "You are a helpful assistant."
	}
	return fallback + "\nYou are serving an OpenAI-compatible API. Do not inspect files, execute commands, modify data, or request approvals. Return only the JSON object required by the output schema."
}

func localCodexChatPrompt(req ChatCompletionRequest) string {
	payload := map[string]any{"messages": req.Messages}
	if len(req.Tools) > 0 && string(req.Tools) != "null" {
		var tools any
		if json.Unmarshal(req.Tools, &tools) == nil {
			payload["tools"] = tools
		}
	}
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		var choice any
		if json.Unmarshal(req.ToolChoice, &choice) == nil {
			payload["tool_choice"] = choice
		}
	}
	data, _ := json.Marshal(payload)
	return "Produce the next assistant response for this OpenAI chat request. If a supplied function is needed, return kind=tool_calls and encode its arguments as a valid JSON object string; never execute the function yourself. Otherwise return kind=message.\n\n" + string(data)
}

func localCodexOutputSchema(req ChatCompletionRequest) map[string]any {
	names := localCodexToolNames(req.Tools)
	kinds := []string{"message"}
	nameSchema := map[string]any{"type": "string"}
	if len(names) > 0 {
		kinds = append(kinds, "tool_calls")
		nameSchema["enum"] = names
	}
	properties := map[string]any{
		"kind":    map[string]any{"type": "string", "enum": kinds},
		"content": map[string]any{"type": "string"},
		"tool_calls": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":        map[string]any{"type": "string"},
					"name":      nameSchema,
					"arguments": map[string]any{"type": "string", "description": "JSON-encoded function arguments object."},
				},
				"required":             []string{"id", "name", "arguments"},
				"additionalProperties": false,
			},
		},
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             []string{"kind", "content", "tool_calls"},
		"additionalProperties": false,
	}
}

func localCodexToolNames(raw json.RawMessage) []string {
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	_ = json.Unmarshal(raw, &tools)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Function.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func localCodexObjectID(raw json.RawMessage, key string) string {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	if object, ok := payload[key].(map[string]any); ok {
		if id, ok := object["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func collectLocalCodexChat(ctx context.Context, client *codexclient.AppServerClient) (LocalChatResult, error) {
	var text strings.Builder
	usage := Usage{}
	for {
		select {
		case <-ctx.Done():
			return LocalChatResult{}, ctx.Err()
		case request, ok := <-client.Requests():
			if ok {
				_ = client.Respond(request.ID, map[string]any{"decision": "decline"})
			}
		case notification, ok := <-client.Notifications():
			if !ok {
				return LocalChatResult{}, errors.New("local Codex app-server closed before completion")
			}
			updateLocalCodexUsage(notification.Params, &usage)
			switch notification.Method {
			case "item/agentMessage/delta":
				var payload map[string]any
				if json.Unmarshal(notification.Params, &payload) == nil {
					if delta, ok := payload["delta"].(string); ok {
						text.WriteString(delta)
					}
				}
			case "item/completed":
				if text.Len() == 0 {
					text.WriteString(localCodexCompletedMessage(notification.Params))
				}
			case "turn/completed":
				if message := localCodexTurnFailure(notification.Params); message != "" {
					return LocalChatResult{}, errors.New(message)
				}
				return parseLocalCodexEnvelope(text.String(), usage)
			}
		case <-client.Done():
			return LocalChatResult{}, errors.New("local Codex app-server exited before completion")
		}
	}
}

func localCodexTurnFailure(raw json.RawMessage) string {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	turn, _ := payload["turn"].(map[string]any)
	if turn == nil {
		return ""
	}
	status, _ := turn["status"].(string)
	if status != "failed" && status != "interrupted" {
		return ""
	}
	if errValue, ok := turn["error"].(map[string]any); ok {
		if message, ok := errValue["message"].(string); ok && strings.TrimSpace(message) != "" {
			return "local Codex turn " + status + ": " + safelog.Text(message, 300)
		}
	}
	return "local Codex turn " + status
}

func localCodexCompletedMessage(raw json.RawMessage) string {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	item, _ := payload["item"].(map[string]any)
	if item == nil || item["type"] != "agentMessage" {
		return ""
	}
	for _, key := range []string{"text", "content"} {
		if value, ok := item[key].(string); ok {
			return value
		}
	}
	return ""
}

func parseLocalCodexEnvelope(raw string, usage Usage) (LocalChatResult, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	var envelope struct {
		Kind      string `json:"kind"`
		Content   string `json:"content"`
		ToolCalls []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &envelope); err != nil {
		return LocalChatResult{}, fmt.Errorf("decode local Codex result: %w", err)
	}
	result := LocalChatResult{Content: envelope.Content, Usage: usage}
	if envelope.Kind == "tool_calls" && len(envelope.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(envelope.ToolCalls))
		for i, call := range envelope.ToolCalls {
			id := strings.TrimSpace(call.ID)
			if id == "" {
				id = fmt.Sprintf("call_%d", i+1)
			}
			arguments := strings.TrimSpace(call.Arguments)
			if !json.Valid([]byte(arguments)) {
				arguments = "{}"
			}
			calls = append(calls, map[string]any{
				"id":       id,
				"type":     "function",
				"function": map[string]any{"name": call.Name, "arguments": arguments},
			})
		}
		result.ToolCalls, _ = json.Marshal(calls)
	}
	return result, nil
}

func updateLocalCodexUsage(raw json.RawMessage, usage *Usage) {
	var payload any
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				if numberValue, ok := nested.(float64); ok {
					switch key {
					case "inputTokens", "input_tokens":
						if int(numberValue) > usage.PromptTokens {
							usage.PromptTokens = int(numberValue)
						}
					case "outputTokens", "output_tokens":
						if int(numberValue) > usage.CompletionTokens {
							usage.CompletionTokens = int(numberValue)
						}
					}
				}
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(payload)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
}

func (s *Service) Ensure(ctx context.Context) error {
	if err := s.Store.EnsureCodexGatewaySettings(ctx); err != nil {
		return err
	}
	return s.SeedStaticModels(ctx)
}

func (s *Service) SeedStaticModels(ctx context.Context) error {
	inputs := []storage.CodexGatewayModelInput{}
	for _, model := range StaticModelInputs() {
		inputs = append(inputs, storage.CodexGatewayModelInput{
			ID:          model.ID,
			DisplayName: model.DisplayName,
			OwnedBy:     model.OwnedBy,
			Source:      model.Source,
		})
	}
	return s.Store.UpsertCodexGatewayModels(ctx, inputs)
}

type Status struct {
	Enabled            bool   `json:"enabled"`
	UpstreamMode       string `json:"upstreamMode"`
	PublicAPIKeys      int    `json:"publicApiKeys"`
	ActiveAccounts     int    `json:"activeAccounts"`
	TotalAccounts      int    `json:"totalAccounts"`
	Models             int    `json:"models"`
	RecentRequestCount int    `json:"recentRequestCount"`
	RecentFailureCount int    `json:"recentFailureCount"`
	LastError          string `json:"lastError,omitempty"`
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	settings, err := s.Store.GetCodexGatewaySettings(ctx)
	if err != nil {
		return Status{}, err
	}
	keys, err := s.Store.ListCodexGatewayAPIKeys(ctx)
	if err != nil {
		return Status{}, err
	}
	accounts, err := s.Store.ListCodexGatewayAccounts(ctx)
	if err != nil {
		return Status{}, err
	}
	models, err := s.ListModels(ctx)
	if err != nil {
		return Status{}, err
	}
	total, failed, err := s.Store.CodexGatewayRecentRequestSummary(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		return Status{}, err
	}
	status := Status{Enabled: settings.Enabled, UpstreamMode: settings.UpstreamMode, Models: len(models), RecentRequestCount: total, RecentFailureCount: failed}
	for _, key := range keys {
		if key.Status == "active" {
			status.PublicAPIKeys++
		}
	}
	for _, account := range accounts {
		status.TotalAccounts++
		if account.Status == "active" {
			status.ActiveAccounts++
		}
	}
	return status, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, name string) (storage.CodexGatewayAPIKey, string, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return storage.CodexGatewayAPIKey{}, "", err
	}
	key, err := s.Store.CreateCodexGatewayAPIKey(ctx, name, hash)
	return key, token, err
}

func (s *Service) RotateAPIKey(ctx context.Context, id string) (storage.CodexGatewayAPIKey, string, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return storage.CodexGatewayAPIKey{}, "", err
	}
	key, err := s.Store.RotateCodexGatewayAPIKey(ctx, id, hash)
	return key, token, err
}

func (s *Service) VerifyPublicToken(ctx context.Context, token string) (storage.CodexGatewayAPIKeySecret, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return storage.CodexGatewayAPIKeySecret{}, false, nil
	}
	key, err := s.Store.GetActiveCodexGatewayAPIKeyByHash(ctx, auth.HashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return storage.CodexGatewayAPIKeySecret{}, false, nil
	}
	if err != nil {
		return storage.CodexGatewayAPIKeySecret{}, false, err
	}
	_ = s.Store.MarkCodexGatewayAPIKeyUsed(context.WithoutCancel(ctx), key.ID)
	return key, true, nil
}

func (s *Service) ListModels(ctx context.Context) ([]storage.CodexGatewayModel, error) {
	models, err := s.Store.ListCodexGatewayModels(ctx)
	if err != nil {
		return nil, err
	}
	if len(models) > 0 {
		return models, nil
	}
	if err := s.SeedStaticModels(ctx); err != nil {
		return nil, err
	}
	return s.Store.ListCodexGatewayModels(ctx)
}

type ModelRefreshResult struct {
	Success  bool                        `json:"success"`
	Accounts int                         `json:"accounts"`
	Plans    map[string]int              `json:"plans"`
	Errors   map[string]string           `json:"errors"`
	Data     []storage.CodexGatewayModel `json:"data"`
}

func (s *Service) RefreshModelCatalog(ctx context.Context) (ModelRefreshResult, error) {
	accounts, err := s.Store.ListCodexGatewayAccounts(ctx)
	if err != nil {
		return ModelRefreshResult{}, err
	}
	result := ModelRefreshResult{Success: true, Plans: map[string]int{}, Errors: map[string]string{}}
	for _, account := range accounts {
		if account.Status != "active" {
			continue
		}
		route, err := s.prepareRouteForAccount(ctx, "", account.ID)
		if err != nil {
			result.Success = false
			result.Errors[account.ID] = SanitizeError(err)
			continue
		}
		if route.Account.AccessToken == "" {
			result.Success = false
			result.Errors[account.ID] = "missing access token"
			continue
		}
		count, err := s.fetchAndStoreModelsForAccount(ctx, route.Account, route.Runtime, route.Account.Plan)
		if err != nil {
			result.Success = false
			result.Errors[account.ID] = SanitizeError(err)
			continue
		}
		result.Accounts++
		plan := route.Account.Plan
		if strings.TrimSpace(plan) == "" {
			plan = "unknown"
		}
		result.Plans[plan] = count
	}
	data, err := s.ListModels(ctx)
	if err != nil {
		return ModelRefreshResult{}, err
	}
	result.Data = data
	return result, nil
}

type UpstreamRoute struct {
	Account storage.CodexGatewayAccountSecret
	Runtime Runtime
}

type RouteError struct {
	Status  int
	Code    string
	Message string
}

func (e RouteError) Error() string { return e.Message }

type UpstreamFailure struct {
	Status              int
	Code                string
	Message             string
	RetryAcrossAccounts bool
}

func (s *Service) SendResponses(ctx context.Context, model, accountID string, payload map[string]any) (UpstreamRoute, *http.Response, *UpstreamFailure, error) {
	explicitAccount := strings.TrimSpace(accountID) != ""
	excluded := []string{}
	var lastRoute UpstreamRoute
	var lastFailure *UpstreamFailure
	for attempt := 0; attempt < 12; attempt++ {
		route, err := s.prepareRouteForAccountExcluding(ctx, model, accountID, excluded)
		if err != nil {
			if lastFailure != nil {
				return lastRoute, nil, lastFailure, nil
			}
			s.Log.Warn("codex gateway route selection failed", "model", strings.TrimSpace(model), "explicit_account", explicitAccount, "error", safelog.Error(err, 200))
			return route, nil, nil, err
		}
		lastRoute = route
		attemptCtx, cancel := context.WithTimeout(ctx, route.Runtime.Timeout)
		started := time.Now()
		route, resp, err := s.doResponsesWithRefresh(attemptCtx, route, payload)
		if err != nil {
			cancel()
			s.Log.Warn("codex gateway upstream request failed", "account_id", route.Account.ID, "model", strings.TrimSpace(model), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
			return route, nil, nil, err
		}
		if resp.StatusCode < 400 {
			resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			return route, resp, nil, nil
		}
		message := readLimitedText(resp.Body, 4096)
		_ = resp.Body.Close()
		cancel()
		failure := classifyUpstreamFailure(resp.StatusCode, message)
		if strings.TrimSpace(failure.Message) == "" {
			failure.Message = fmt.Sprintf("Codex upstream returned HTTP %d", resp.StatusCode)
		}
		lastFailure = &failure
		s.Log.Warn("codex gateway upstream returned failure", "account_id", route.Account.ID, "model", strings.TrimSpace(model), "status", resp.StatusCode, "code", failure.Code, "retry_across_accounts", failure.RetryAcrossAccounts, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Text(failure.Message, 200))
		s.recordUpstreamFailure(context.WithoutCancel(ctx), route.Account.ID, failure)
		if !explicitAccount && failure.RetryAcrossAccounts {
			excluded = append(excluded, route.Account.ID)
			continue
		}
		return route, nil, &failure, nil
	}
	if lastFailure != nil {
		return lastRoute, nil, lastFailure, nil
	}
	return lastRoute, nil, nil, RouteError{Status: http.StatusServiceUnavailable, Code: "no_available_accounts", Message: "没有可用 Codex Gateway 账号"}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (s *Service) doResponsesWithRefresh(ctx context.Context, route UpstreamRoute, payload map[string]any) (UpstreamRoute, *http.Response, error) {
	resp, err := NewClient(route.Runtime).DoResponses(ctx, route.Account.AccessToken, payload)
	if err != nil {
		return route, nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return route, resp, nil
	}
	if route.Account.RefreshToken == "" {
		return route, resp, nil
	}
	drainAndClose(resp.Body)
	refreshed, err := s.refreshAccountAccessToken(ctx, route.Account, route.Runtime)
	if err != nil {
		_, _ = s.markRefreshFailure(context.WithoutCancel(ctx), route.Account.ID, err)
		return route, nil, RouteError{Status: http.StatusBadGateway, Code: "account_refresh_failed", Message: "Codex Gateway 账号刷新失败"}
	}
	route.Account = refreshed
	resp, err = NewClient(route.Runtime).DoResponses(ctx, route.Account.AccessToken, payload)
	return route, resp, err
}

func (s *Service) prepareRouteForAccount(ctx context.Context, model, accountID string) (UpstreamRoute, error) {
	return s.prepareRouteForAccountExcluding(ctx, model, accountID, nil)
}

func (s *Service) prepareRouteForAccountExcluding(ctx context.Context, model, accountID string, excluded []string) (UpstreamRoute, error) {
	account, err := s.routeAccount(ctx, model, accountID, excluded)
	if err != nil {
		return UpstreamRoute{}, err
	}
	settings, err := s.Store.GetCodexGatewaySettings(ctx)
	if err != nil {
		return UpstreamRoute{}, err
	}
	if !settings.Enabled {
		return UpstreamRoute{}, RouteError{Status: http.StatusServiceUnavailable, Code: "gateway_disabled", Message: "Codex Gateway 未启用"}
	}
	runtime := runtimeFromSettings(settings)
	if account.AccessToken == "" {
		if account.RefreshToken == "" {
			return UpstreamRoute{}, RouteError{Status: http.StatusServiceUnavailable, Code: "account_missing_token", Message: "账号没有可用 token"}
		}
		refreshed, err := s.refreshAccountAccessToken(ctx, account, runtime)
		if err != nil {
			_, _ = s.markRefreshFailure(context.WithoutCancel(ctx), account.ID, err)
			return UpstreamRoute{}, RouteError{Status: http.StatusBadGateway, Code: "account_refresh_failed", Message: "Codex Gateway 账号刷新失败"}
		}
		account = refreshed
	} else if due, expired := tokenRefreshDue(account, time.Duration(settings.RefreshMarginSeconds)*time.Second); due && account.RefreshToken != "" {
		refreshed, err := s.refreshAccountAccessToken(ctx, account, runtime)
		if err != nil {
			if expired {
				_, _ = s.markRefreshFailure(context.WithoutCancel(ctx), account.ID, err)
				return UpstreamRoute{}, RouteError{Status: http.StatusBadGateway, Code: "account_refresh_failed", Message: "Codex Gateway 账号刷新失败"}
			}
		} else {
			account = refreshed
		}
	}
	return UpstreamRoute{Account: account, Runtime: runtime}, nil
}

func (s *Service) routeAccount(ctx context.Context, model, accountID string, excluded []string) (storage.CodexGatewayAccountSecret, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		account, err := s.Store.SelectCodexGatewayAccountForModel(ctx, model, excluded)
		if errors.Is(err, storage.ErrNotFound) {
			message := "没有可用 Codex Gateway 账号"
			if strings.TrimSpace(model) != "" {
				message = "没有可用 Codex Gateway 账号支持该模型"
			}
			return storage.CodexGatewayAccountSecret{}, RouteError{Status: http.StatusServiceUnavailable, Code: "no_available_accounts", Message: message}
		}
		return account, err
	}
	account, err := s.Store.GetCodexGatewayAccountSecret(ctx, accountID)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.CodexGatewayAccountSecret{}, RouteError{Status: http.StatusNotFound, Code: "account_not_found", Message: "Codex Gateway 账号不存在"}
	}
	if err != nil {
		return storage.CodexGatewayAccountSecret{}, err
	}
	if account.Status != "active" {
		return storage.CodexGatewayAccountSecret{}, RouteError{Status: http.StatusBadRequest, Code: "account_inactive", Message: "Codex Gateway 账号不是 active 状态"}
	}
	ok, err := s.Store.CodexGatewayAccountCanUseModel(ctx, account.CodexGatewayAccount, model)
	if err != nil {
		return storage.CodexGatewayAccountSecret{}, err
	}
	if !ok {
		return storage.CodexGatewayAccountSecret{}, RouteError{Status: http.StatusBadRequest, Code: "model_not_supported", Message: "该账号 plan 不支持所选模型"}
	}
	return account, nil
}

func runtimeFromSettings(settings storage.CodexGatewaySettings) Runtime {
	return Runtime{
		BaseURL:        settings.BaseURL,
		OAuthAuthURL:   settings.OAuthAuthURL,
		OAuthTokenURL:  settings.OAuthTokenURL,
		OAuthClientID:  settings.OAuthClientID,
		Timeout:        time.Duration(settings.RequestTimeoutSeconds) * time.Second,
		InstallationID: settings.InstallationID,
	}
}

func (s *Service) RefreshAccount(ctx context.Context, id string) (storage.CodexGatewayAccount, error) {
	secret, err := s.Store.GetCodexGatewayAccountSecret(ctx, id)
	if err != nil {
		return storage.CodexGatewayAccount{}, err
	}
	if secret.RefreshToken == "" {
		return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "invalid", "", "缺少 refresh token")
	}
	settings, err := s.Store.GetCodexGatewaySettings(ctx)
	if err != nil {
		return storage.CodexGatewayAccount{}, err
	}
	runtime := runtimeFromSettings(settings)
	runtime.Timeout = minDuration(runtime.Timeout, 30*time.Second)
	started := time.Now()
	s.Log.Info("codex gateway account refresh started", "account_id", id, "token_endpoint", safelog.URLLabel(runtime.OAuthTokenURL))
	if _, err := s.refreshAccountAccessToken(ctx, secret, runtime); err != nil {
		s.Log.Warn("codex gateway account refresh failed", "account_id", id, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		return s.markRefreshFailure(ctx, id, err)
	}
	s.Log.Info("codex gateway account refresh completed", "account_id", id, "latency_ms", time.Since(started).Milliseconds())
	return s.CheckAccount(ctx, id)
}

func (s *Service) CheckAccount(ctx context.Context, id string) (storage.CodexGatewayAccount, error) {
	secret, err := s.Store.GetCodexGatewayAccountSecret(ctx, id)
	if err != nil {
		return storage.CodexGatewayAccount{}, err
	}
	settings, err := s.Store.GetCodexGatewaySettings(ctx)
	if err != nil {
		return storage.CodexGatewayAccount{}, err
	}
	runtime := runtimeFromSettings(settings)
	runtime.Timeout = minDuration(runtime.Timeout, 20*time.Second)
	started := time.Now()
	s.Log.Info("codex gateway account check started", "account_id", id, "usage_endpoint", safelog.URLLabel(usageEndpoint(runtime.BaseURL)))
	if secret.AccessToken == "" {
		if secret.RefreshToken == "" {
			s.Log.Warn("codex gateway account check failed", "account_id", id, "reason", "missing_tokens")
			return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "invalid", "", "缺少 access token 和 refresh token")
		}
		refreshed, err := s.refreshAccountAccessToken(ctx, secret, runtime)
		if err != nil {
			s.Log.Warn("codex gateway account check refresh failed", "account_id", id, "error", safelog.Error(err, 200))
			return s.markRefreshFailure(ctx, id, err)
		}
		secret = refreshed
	} else if due, expired := tokenRefreshDue(secret, time.Duration(settings.RefreshMarginSeconds)*time.Second); due && secret.RefreshToken != "" {
		refreshed, err := s.refreshAccountAccessToken(ctx, secret, runtime)
		if err != nil && expired {
			s.Log.Warn("codex gateway account check refresh failed", "account_id", id, "expired", true, "error", safelog.Error(err, 200))
			return s.markRefreshFailure(ctx, id, err)
		}
		if err == nil {
			secret = refreshed
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, runtime.Timeout)
	defer cancel()
	status, body, err := NewClient(runtime).CheckUsage(checkCtx, secret.AccessToken)
	if err != nil {
		s.Log.Warn("codex gateway account check failed", "account_id", id, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "invalid", "", SanitizeError(err))
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if secret.RefreshToken != "" {
			refreshed, err := s.refreshAccountAccessToken(ctx, secret, runtime)
			if err != nil {
				return s.markRefreshFailure(ctx, id, err)
			}
			secret = refreshed
			status, body, err = NewClient(runtime).CheckUsage(checkCtx, secret.AccessToken)
			if err != nil {
				s.Log.Warn("codex gateway account recheck failed", "account_id", id, "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
				return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "invalid", "", SanitizeError(err))
			}
			if status < 400 {
				plan := extractPlan(body)
				s.tryRefreshModels(ctx, secret, runtime, plan)
				s.Log.Info("codex gateway account check completed", "account_id", id, "status", "active", "plan", plan, "latency_ms", time.Since(started).Milliseconds())
				return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "active", plan, "")
			}
		}
		s.Log.Warn("codex gateway account check rejected", "account_id", id, "status", status, "latency_ms", time.Since(started).Milliseconds())
		return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "invalid", "", "上游拒绝账号凭证")
	}
	if status == http.StatusTooManyRequests {
		s.Log.Warn("codex gateway account check rate limited", "account_id", id, "status", status, "latency_ms", time.Since(started).Milliseconds())
		return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "rate_limited", "", "上游返回限流")
	}
	if status >= 400 {
		s.Log.Warn("codex gateway account check returned failure", "account_id", id, "status", status, "latency_ms", time.Since(started).Milliseconds())
		return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "invalid", "", fmt.Sprintf("上游检查失败: HTTP %d", status))
	}
	plan := extractPlan(body)
	s.tryRefreshModels(ctx, secret, runtime, plan)
	s.Log.Info("codex gateway account check completed", "account_id", id, "status", "active", "plan", plan, "latency_ms", time.Since(started).Milliseconds())
	return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, "active", plan, "")
}

func (s *Service) refreshAccountAccessToken(ctx context.Context, secret storage.CodexGatewayAccountSecret, runtime Runtime) (storage.CodexGatewayAccountSecret, error) {
	if secret.RefreshToken == "" {
		return storage.CodexGatewayAccountSecret{}, OAuthRefreshError{Code: "missing_refresh_token", Description: "missing refresh_token"}
	}
	if strings.TrimSpace(secret.ID) == "" {
		return s.refreshAccountAccessTokenOnce(ctx, secret, runtime)
	}
	s.refreshMu.Lock()
	if s.refreshes == nil {
		s.refreshes = map[string]*refreshCall{}
	}
	if call := s.refreshes[secret.ID]; call != nil {
		s.refreshMu.Unlock()
		select {
		case <-ctx.Done():
			return storage.CodexGatewayAccountSecret{}, ctx.Err()
		case <-call.done:
			return call.secret, call.err
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	s.refreshes[secret.ID] = call
	s.refreshMu.Unlock()

	call.secret, call.err = s.refreshAccountAccessTokenOnce(ctx, secret, runtime)
	s.refreshMu.Lock()
	delete(s.refreshes, secret.ID)
	s.refreshMu.Unlock()
	close(call.done)
	return call.secret, call.err
}

func (s *Service) refreshAccountAccessTokenOnce(ctx context.Context, secret storage.CodexGatewayAccountSecret, runtime Runtime) (storage.CodexGatewayAccountSecret, error) {
	if latest, err := s.Store.GetCodexGatewayAccountSecret(ctx, secret.ID); err == nil && latest.RefreshToken != "" && latest.RefreshToken != secret.RefreshToken {
		if latest.AccessToken != "" {
			return latest, nil
		}
		secret.RefreshToken = latest.RefreshToken
	}
	refreshCtx, cancel := context.WithTimeout(ctx, minDuration(runtime.Timeout, 30*time.Second))
	defer cancel()
	tokens, err := NewClient(runtime).RefreshAccessToken(refreshCtx, secret.RefreshToken)
	if err != nil {
		return storage.CodexGatewayAccountSecret{}, err
	}
	nextRefresh := strings.TrimSpace(tokens.RefreshToken)
	if nextRefresh == "" {
		nextRefresh = secret.RefreshToken
	}
	return s.Store.UpdateCodexGatewayAccountTokens(ctx, secret.ID, strings.TrimSpace(tokens.AccessToken), nextRefresh, ExpiresAtFromSeconds(tokens.ExpiresIn))
}

func (s *Service) markRefreshFailure(ctx context.Context, id string, err error) (storage.CodexGatewayAccount, error) {
	status := "invalid"
	var oauthErr OAuthRefreshError
	if errors.As(err, &oauthErr) && oauthErr.StatusCode == http.StatusTooManyRequests {
		status = "rate_limited"
	}
	return s.Store.UpdateCodexGatewayAccountCheck(ctx, id, status, "", SanitizeError(err))
}

func (s *Service) tryRefreshModels(ctx context.Context, secret storage.CodexGatewayAccountSecret, runtime Runtime, plan string) {
	if secret.AccessToken == "" {
		return
	}
	if _, err := s.fetchAndStoreModelsForAccount(ctx, secret, runtime, plan); err != nil {
		s.Log.Warn("failed to refresh codex gateway models", "account_id", secret.ID, "base_host", safelog.HostLabel(runtime.BaseURL), "error", safelog.Error(err, 200))
	}
}

func (s *Service) fetchAndStoreModelsForAccount(ctx context.Context, secret storage.CodexGatewayAccountSecret, runtime Runtime, plan string) (int, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, minDuration(runtime.Timeout, 30*time.Second))
	defer cancel()
	started := time.Now()
	models, err := NewClient(runtime).FetchModels(fetchCtx, secret.AccessToken)
	if err != nil {
		s.Log.Warn("codex gateway model fetch failed", "account_id", secret.ID, "base_host", safelog.HostLabel(runtime.BaseURL), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		return 0, err
	}
	if err := s.SeedStaticModels(ctx); err != nil {
		return 0, err
	}
	inputs := make([]storage.CodexGatewayModelInput, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		display := strings.TrimSpace(model.DisplayName)
		if display == "" {
			display = id
		}
		inputs = append(inputs, storage.CodexGatewayModelInput{ID: id, DisplayName: display, OwnedBy: "codex", Source: "upstream"})
	}
	if strings.TrimSpace(plan) != "" {
		err := s.Store.UpsertCodexGatewayModelsForPlan(ctx, strings.TrimSpace(plan), inputs)
		if err == nil {
			s.Log.Info("codex gateway model fetch completed", "account_id", secret.ID, "plan", strings.TrimSpace(plan), "models", len(inputs), "latency_ms", time.Since(started).Milliseconds())
		}
		return len(inputs), err
	}
	err = s.Store.UpsertCodexGatewayModels(ctx, inputs)
	if err == nil {
		s.Log.Info("codex gateway model fetch completed", "account_id", secret.ID, "models", len(inputs), "latency_ms", time.Since(started).Milliseconds())
	}
	return len(inputs), err
}

func classifyUpstreamFailure(status int, message string) UpstreamFailure {
	lower := strings.ToLower(message)
	switch {
	case status == http.StatusTooManyRequests:
		return UpstreamFailure{Status: status, Code: "rate_limit_exceeded", Message: message, RetryAcrossAccounts: true}
	case status == http.StatusPaymentRequired:
		return UpstreamFailure{Status: status, Code: "quota_exhausted", Message: message, RetryAcrossAccounts: true}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return UpstreamFailure{Status: status, Code: "authentication_error", Message: message, RetryAcrossAccounts: true}
	case status == http.StatusNotFound && strings.TrimSpace(message) == "":
		return UpstreamFailure{Status: http.StatusBadGateway, Code: "upstream_blocked", Message: "Codex 上游阻断了请求", RetryAcrossAccounts: true}
	case status >= 400 && status < 500 && strings.Contains(lower, "model") &&
		(strings.Contains(lower, "not supported") || strings.Contains(lower, "not_supported") || strings.Contains(lower, "not available") || strings.Contains(lower, "not_available")):
		return UpstreamFailure{Status: status, Code: "model_not_supported", Message: message, RetryAcrossAccounts: true}
	case status >= 500:
		return UpstreamFailure{Status: status, Code: "upstream_error", Message: message, RetryAcrossAccounts: true}
	default:
		return UpstreamFailure{Status: status, Code: "invalid_request", Message: message}
	}
}

func (s *Service) recordUpstreamFailure(ctx context.Context, accountID string, failure UpstreamFailure) {
	msg := CleanErrorMessage(failure.Message, 300)
	switch failure.Code {
	case "rate_limit_exceeded", "quota_exhausted":
		_, _ = s.Store.UpdateCodexGatewayAccountCheck(ctx, accountID, "rate_limited", "", msg)
	case "authentication_error":
		_, _ = s.Store.UpdateCodexGatewayAccountCheck(ctx, accountID, "invalid", "", msg)
	case "model_not_supported":
		_, _ = s.Store.UpdateCodexGatewayAccountCheck(ctx, accountID, "active", "", msg)
	}
}

func tokenRefreshDue(account storage.CodexGatewayAccountSecret, margin time.Duration) (bool, bool) {
	expires := strings.TrimSpace(account.ExpiresAt)
	if expires == "" {
		return false, false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		expiresAt, err = time.Parse(time.RFC3339, expires)
		if err != nil {
			return false, false
		}
	}
	now := time.Now().UTC()
	expired := !expiresAt.After(now)
	return !expiresAt.After(now.Add(margin)), expired
}

func ExpiresAtFromSeconds(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return time.Now().UTC().Add(time.Duration(seconds) * time.Second).Format(time.RFC3339Nano)
}

func extractPlan(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if plan := firstPlanString(payload); plan != "" {
		return plan
	}
	for _, key := range []string{"account", "user", "quota", "usage", "organization"} {
		if nested, ok := payload[key].(map[string]any); ok {
			if plan := firstPlanString(nested); plan != "" {
				return plan
			}
		}
	}
	return ""
}

func firstPlanString(payload map[string]any) string {
	for _, key := range []string{"plan", "plan_type", "chatgpt_plan_type", "account_plan", "tier"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Service) LogRequest(ctx context.Context, input storage.CodexGatewayRequestLogInput) {
	if input.RequestID == "" {
		input.RequestID = randomToken(8)
	}
	if input.APIKind == "" {
		input.APIKind = "unknown"
	}
	if input.ErrorCode != "" && input.ErrorSource == "" {
		input.ErrorSource = ClassifyErrorSource(input.ErrorCode)
	}
	if input.ErrorMessage != "" {
		input.ErrorMessage = CleanErrorMessage(input.ErrorMessage, 600)
	}
	if err := s.Store.CreateCodexGatewayRequestLog(ctx, input); err != nil {
		s.Log.Warn("failed to write codex gateway request log", "error", err)
	}
}

func ClassifyErrorSource(code string) string {
	switch strings.TrimSpace(code) {
	case "invalid_json", "invalid_request":
		return ErrorSourceClient
	case "no_available_accounts", "account_not_found", "account_inactive", "account_missing_token", "account_refresh_failed", "model_not_supported":
		return ErrorSourceAccount
	case "rate_limit_exceeded", "quota_exhausted", "authentication_error", "upstream_blocked", "upstream_error":
		return ErrorSourceOpenAI
	case "internal_error", "store_error", "gateway_disabled":
		return ErrorSourceService
	default:
		if strings.HasPrefix(code, "upstream_") || strings.Contains(code, "openai") {
			return ErrorSourceOpenAI
		}
		return ErrorSourceService
	}
}

func OpenAIErrorType(status int, code string) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication_error"
	case status == http.StatusTooManyRequests || code == "rate_limit_exceeded":
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}

func SanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return CleanErrorMessage(err.Error(), 300)
}

func CleanErrorMessage(message string, maxRunes int) string {
	message = strings.TrimSpace(message)
	if extracted := extractStructuredErrorMessage(message); extracted != "" {
		message = extracted
	}
	message = safelog.Redact(message)
	message = strings.Join(strings.Fields(message), " ")
	if maxRunes > 0 {
		runes := []rune(message)
		if len(runes) > maxRunes {
			message = string(runes[:maxRunes])
		}
	}
	return message
}

func extractStructuredErrorMessage(message string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(message), &payload) != nil {
		return ""
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		if value, ok := nested["message"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	for _, key := range []string{"message", "detail", "error"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func AccountOAuthLabel(tokens OAuthTokenResponse) string {
	for _, token := range []string{tokens.IDToken, tokens.AccessToken} {
		if label := jwtStringClaim(token, "email", "preferred_username", "name", "sub"); label != "" {
			return label
		}
	}
	return "Codex OAuth " + time.Now().UTC().Format("20060102-150405")
}

func jwtStringClaim(token string, keys ...string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		body, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}

func readLimitedText(body io.Reader, limit int64) string {
	data, _ := io.ReadAll(io.LimitReader(body, limit))
	var payload map[string]any
	if json.Unmarshal(data, &payload) == nil {
		if errObj, ok := payload["error"].(map[string]any); ok {
			if message, ok := errObj["message"].(string); ok {
				return message
			}
		}
		if message, ok := payload["message"].(string); ok {
			return message
		}
		if detail, ok := payload["detail"].(string); ok {
			return detail
		}
	}
	return strings.TrimSpace(string(data))
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

// StartBackground launches a periodic account health-check goroutine. The
// first pass runs immediately (so boot reveals stale refresh tokens within
// seconds) and subsequent passes are scheduled from the DB setting
// account_health_check_interval_seconds, re-read each iteration so the
// interval can be tuned without a restart. A value of 0 disables the loop
// after the initial pass completes.
func (s *Service) StartBackground(ctx context.Context) {
	s.bgMu.Lock()
	if s.bgCancel != nil {
		s.bgMu.Unlock()
		return
	}
	bgCtx, cancel := context.WithCancel(ctx)
	s.bgCancel = cancel
	s.bgWg.Add(1)
	s.bgMu.Unlock()

	go func() {
		defer s.bgWg.Done()
		defer cancel()

		settings, err := s.Store.GetCodexGatewaySettings(bgCtx)
		if err != nil {
			s.Log.Info("codex gateway health-check settings unavailable, using defaults", "error", safelog.Error(err, 200))
			settings = storage.DefaultCodexGatewaySettings()
		}
		s.runHealthCheckPass(bgCtx, settings)

		for {
			if bgCtx.Err() != nil {
				return
			}
			settings, err := s.Store.GetCodexGatewaySettings(bgCtx)
			if err != nil {
				s.Log.Warn("codex gateway health-check settings read failed, retrying later", "error", safelog.Error(err, 200))
				settings = storage.DefaultCodexGatewaySettings()
			}
			interval := time.Duration(settings.AccountHealthCheckIntervalSeconds) * time.Second
			if interval <= 0 {
				s.Log.Info("codex gateway health-check loop disabled (interval=0); exiting goroutine")
				return
			}
			const minInterval = 10 * time.Second
			if interval < minInterval {
				s.Log.Warn("codex gateway health-check interval too small, clamping to minimum", "configured", interval.String(), "minimum", minInterval.String())
				interval = minInterval
			}
			select {
			case <-bgCtx.Done():
				return
			case <-time.After(interval):
			}
			s.runHealthCheckPass(bgCtx, settings)
		}
	}()
}

// Close signals the background goroutine to exit and waits up to 2 seconds
// for it to drain. Intended for orderlyClose before self-update exec.
func (s *Service) Close() {
	s.bgMu.Lock()
	cancel := s.bgCancel
	s.bgCancel = nil
	s.bgMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()

	done := make(chan struct{})
	go func() {
		s.bgWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.Log.Warn("codex gateway background goroutine did not exit within 2s shutdown window, continuing anyway")
	}
}

// runHealthCheckPass performs one sequential pass over every non-disabled
// account calling CheckAccount. Sequential (not concurrent) on purpose:
// refresh + usage hits two upstream endpoints per account and we do not
// want to burst against the OpenAI OAuth/codex rate limits. Disabled
// accounts are skipped; invalid/rate_limited ones are included so the pass
// can detect recovery (e.g. a rate-limit window rolling off).
func (s *Service) runHealthCheckPass(ctx context.Context, settings storage.CodexGatewaySettings) {
	startedAt := time.Now()
	passLog := s.Log.With("phase", "account_health_check_pass")
	passLog.Info("codex gateway account health-check pass started")
	accounts, err := s.Store.ListCodexGatewayAccounts(ctx)
	if err != nil {
		passLog.Warn("codex gateway health-check failed to list accounts", "error", safelog.Error(err, 300))
		return
	}
	total := len(accounts)
	checked := 0
	skippedDisabled := 0
	for _, acct := range accounts {
		if ctx.Err() != nil {
			passLog.Warn("codex gateway health-check pass interrupted by shutdown", "checked", checked, "total", total)
			return
		}
		if acct.Status == "disabled" {
			skippedDisabled++
			continue
		}
		acctCtx, acctCancel := context.WithTimeout(ctx, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
		_, aerr := s.CheckAccount(acctCtx, acct.ID)
		acctCancel()
		checked++
		if aerr != nil && !errors.Is(aerr, context.Canceled) && !errors.Is(aerr, context.DeadlineExceeded) {
			// CheckAccount already emitted per-account diagnostics; emit a
			// pass-level summary line so noisy logs can be filtered without
			// digging into per-account records.
			passLog.Warn("codex gateway health-check account flagged", "account_id", acct.ID, "error", safelog.Error(aerr, 200))
		}
	}
	passLog.Info("codex gateway account health-check pass completed",
		"checked", checked,
		"skipped_disabled", skippedDisabled,
		"total", total,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
}
