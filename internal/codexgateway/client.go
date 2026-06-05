package codexgateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Runtime struct {
	BaseURL        string
	OAuthAuthURL   string
	OAuthTokenURL  string
	OAuthClientID  string
	Timeout        time.Duration
	InstallationID string
}

type Client struct {
	runtime Runtime
	http    *http.Client
}

func NewClient(runtime Runtime) Client {
	timeout := runtime.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return Client{runtime: runtime, http: &http.Client{Timeout: timeout, Transport: transport}}
}

type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

type OAuthRefreshError struct {
	StatusCode  int
	Code        string
	Description string
}

func (e OAuthRefreshError) Error() string {
	if e.Code != "" && e.Description != "" {
		return fmt.Sprintf("oauth token refresh failed (%s): %s", e.Code, e.Description)
	}
	if e.Code != "" {
		return fmt.Sprintf("oauth token refresh failed (%s)", e.Code)
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("oauth token refresh failed with HTTP %d", e.StatusCode)
	}
	return "oauth token refresh failed"
}

func (e OAuthRefreshError) Permanent() bool {
	switch e.Code {
	case "invalid_grant", "invalid_token", "access_denied", "refresh_token_expired", "refresh_token_reused":
		return true
	default:
		return false
	}
}

type PKCEChallenge struct {
	CodeVerifier  string
	CodeChallenge string
}

func NewPKCEChallenge() (PKCEChallenge, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return PKCEChallenge{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf[:])
	sum := sha256.Sum256([]byte(verifier))
	return PKCEChallenge{CodeVerifier: verifier, CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

type OAuthAuthorizationOptions struct {
	AuthURL       string
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
}

func BuildAuthorizationURL(options OAuthAuthorizationOptions) (string, error) {
	authURL := strings.TrimSpace(options.AuthURL)
	if authURL == "" {
		return "", errors.New("oauth authorization url is required")
	}
	clientID := strings.TrimSpace(options.ClientID)
	if clientID == "" {
		return "", errors.New("oauth client id is required")
	}
	redirectURI := strings.TrimSpace(options.RedirectURI)
	if redirectURI == "" {
		return "", errors.New("oauth redirect uri is required")
	}
	if strings.TrimSpace(options.State) == "" || strings.TrimSpace(options.CodeChallenge) == "" {
		return "", errors.New("state and code_challenge are required")
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		return "", fmt.Errorf("parse oauth authorization url: %w", err)
	}
	values := parsed.Query()
	values.Set("response_type", "code")
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", "openid profile email offline_access")
	values.Set("code_challenge", options.CodeChallenge)
	values.Set("code_challenge_method", "S256")
	values.Set("id_token_add_organizations", "true")
	values.Set("codex_cli_simplified_flow", "true")
	values.Set("state", options.State)
	values.Set("originator", "codex_cli_rs")
	parsed.RawQuery = strings.ReplaceAll(values.Encode(), "+", "%20")
	return parsed.String(), nil
}

func (c Client) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (OAuthTokenResponse, error) {
	tokenURL := strings.TrimSpace(c.runtime.OAuthTokenURL)
	if tokenURL == "" {
		return OAuthTokenResponse{}, errors.New("oauth token url is required")
	}
	clientID := strings.TrimSpace(c.runtime.OAuthClientID)
	if clientID == "" {
		return OAuthTokenResponse{}, errors.New("oauth client id is required")
	}
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("client_id", clientID)
	values.Set("code", strings.TrimSpace(code))
	values.Set("redirect_uri", strings.TrimSpace(redirectURI))
	values.Set("code_verifier", strings.TrimSpace(codeVerifier))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.doOAuthTokenRequest(req)
}

func (c Client) RefreshAccessToken(ctx context.Context, refreshToken string) (OAuthTokenResponse, error) {
	tokenURL := strings.TrimSpace(c.runtime.OAuthTokenURL)
	if tokenURL == "" {
		return OAuthTokenResponse{}, errors.New("oauth token url is required")
	}
	clientID := strings.TrimSpace(c.runtime.OAuthClientID)
	if clientID == "" {
		return OAuthTokenResponse{}, errors.New("oauth client id is required")
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("client_id", clientID)
	values.Set("refresh_token", strings.TrimSpace(refreshToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.doOAuthTokenRequest(req)
}

func (c Client) doOAuthTokenRequest(req *http.Request) (OAuthTokenResponse, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &payload)
		return OAuthTokenResponse{}, OAuthRefreshError{StatusCode: resp.StatusCode, Code: payload.Error, Description: payload.ErrorDescription}
	}
	var tokens OAuthTokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return OAuthTokenResponse{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return OAuthTokenResponse{}, OAuthRefreshError{StatusCode: resp.StatusCode, Code: "invalid_response", Description: "missing access_token"}
	}
	return tokens, nil
}

func (c Client) DoResponses(ctx context.Context, accessToken string, payload map[string]any) (*http.Response, error) {
	requestID := randomToken(16)
	body, err := json.Marshal(copyPayloadWithMetadata(payload, requestID, c.installationID()))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesEndpoint(c.runtime.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")
	if stream, _ := payload["stream"].(bool); stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	req.Header.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
	req.Header.Set("x-openai-internal-codex-residency", "us")
	req.Header.Set("x-client-request-id", requestID)
	req.Header.Set("x-codex-installation-id", c.installationID())
	return c.http.Do(req)
}

func (c Client) CheckUsage(ctx context.Context, accessToken string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageEndpoint(c.runtime.BaseURL), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

type ModelInfo struct {
	ID          string
	DisplayName string
}

func (c Client) FetchModels(ctx context.Context, accessToken string) ([]ModelInfo, error) {
	var lastErr error
	for _, endpoint := range modelEndpoints(c.runtime.BaseURL) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
		req.Header.Set("Accept", "application/json")
		req.Header.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
		req.Header.Set("x-openai-internal-codex-residency", "us")
		req.Header.Set("x-client-request-id", randomToken(16))
		req.Header.Set("x-codex-installation-id", c.installationID())
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("models endpoint returned HTTP %d", resp.StatusCode)
			continue
		}
		models := parseModelsPayload(body)
		if len(models) > 0 {
			return models, nil
		}
		lastErr = errors.New("models endpoint returned no models")
	}
	if lastErr == nil {
		lastErr = errors.New("models endpoint unavailable")
	}
	return nil, lastErr
}

func (c Client) installationID() string {
	value := strings.TrimSpace(c.runtime.InstallationID)
	if value == "" {
		return "phantom-lancer"
	}
	return value
}

func copyPayloadWithMetadata(payload map[string]any, requestID, installationID string) map[string]any {
	out := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		out[key] = value
	}
	metadata := map[string]any{}
	if existing, ok := out["client_metadata"].(map[string]any); ok {
		for key, value := range existing {
			metadata[key] = value
		}
	}
	metadata["x-codex-installation-id"] = installationID
	metadata["x-client-request-id"] = requestID
	out["client_metadata"] = metadata
	return out
}

func responsesEndpoint(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/codex/responses") || strings.HasSuffix(base, "/v1/responses") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/responses"
	}
	return base + "/codex/responses"
}

func usageEndpoint(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/codex/responses") {
		return strings.TrimSuffix(base, "/responses") + "/usage"
	}
	if strings.HasSuffix(base, "/v1/responses") {
		return strings.TrimSuffix(base, "/v1/responses") + "/codex/usage"
	}
	if strings.HasSuffix(base, "/v1") {
		return strings.TrimSuffix(base, "/v1") + "/codex/usage"
	}
	return base + "/codex/usage"
}

func modelEndpoints(base string) []string {
	root := apiRoot(base)
	return []string{root + "/codex/models?client_version=phantom-lancer", root + "/models", root + "/sentinel/chat-requirements"}
}

func apiRoot(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/codex/responses", "/v1/responses", "/v1"} {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return base
}

func parseModelsPayload(body []byte) []ModelInfo {
	var payload any
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	record, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	items := firstModelArray(record)
	seen := map[string]bool{}
	models := []ModelInfo{}
	var visit func([]any)
	visit = func(values []any) {
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if nested, ok := item["models"].([]any); ok {
				visit(nested)
				continue
			}
			id := firstString(item, "slug", "id", "name")
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			display := firstString(item, "display_name", "displayName", "name")
			if display == "" || display == id {
				display = id
			}
			models = append(models, ModelInfo{ID: id, DisplayName: display})
		}
	}
	visit(items)
	return models
}

func firstModelArray(record map[string]any) []any {
	if chat, ok := record["chat_models"].(map[string]any); ok {
		if models, ok := chat["models"].([]any); ok {
			return models
		}
	}
	for _, key := range []string{"models", "data", "categories"} {
		if models, ok := record[key].([]any); ok {
			return models
		}
	}
	return nil
}

func firstString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := record[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func randomToken(byteLen int) string {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
