package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	NewsSourceJin10          = "jin10"
	jin10EndpointEnv         = "STOCKV2_JIN10_ENDPOINT"
	jin10TokenEnv            = "STOCKV2_JIN10_TOKEN"
	newsSourceMaxBodyBytes   = 2 << 20
	defaultNewsSourceTimeout = 10 * time.Second
	maxJin10CookieBytes      = 16 << 10
)

type Jin10NewsAdapter struct {
	endpoint   string
	token      string
	cookie     string
	xAppID     string
	xVersion   string
	httpClient *http.Client
}

type Jin10CurlConfig struct {
	Endpoint string
	Cookie   string
	XAppID   string
	XVersion string
}

func NewJin10NewsAdapterFromEnv(httpClient *http.Client) *Jin10NewsAdapter {
	return &Jin10NewsAdapter{
		endpoint:   strings.TrimSpace(os.Getenv(jin10EndpointEnv)),
		token:      strings.TrimSpace(os.Getenv(jin10TokenEnv)),
		httpClient: httpClient,
	}
}

type jin10NewsSourceAdapter struct {
	service  *Service
	fallback *Jin10NewsAdapter
}

func (a jin10NewsSourceAdapter) SourceName() string {
	return NewsSourceJin10
}

func (a jin10NewsSourceAdapter) FetchSince(ctx context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error) {
	if a.service == nil {
		if a.fallback == nil {
			return NewsSourceFetchResult{}, ErrNewsSourceAdapterNotFound
		}
		return a.fallback.FetchSince(ctx, cursor)
	}
	settings, err := a.service.GetSettings(ctx)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	if settings.Jin10Enabled && strings.TrimSpace(settings.Jin10Endpoint) != "" && strings.TrimSpace(settings.Jin10Cookie) != "" {
		adapter := &Jin10NewsAdapter{
			endpoint:   settings.Jin10Endpoint,
			cookie:     settings.Jin10Cookie,
			xAppID:     settings.Jin10XAppID,
			xVersion:   settings.Jin10XVersion,
			httpClient: a.service.httpClient,
		}
		return adapter.FetchSince(ctx, cursor)
	}
	if a.fallback != nil && strings.TrimSpace(a.fallback.endpoint) != "" {
		return a.fallback.FetchSince(ctx, cursor)
	}
	return NewsSourceFetchResult{Disabled: true, FetchedAt: time.Now()}, nil
}

func (a jin10NewsSourceAdapter) NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error) {
	adapter := a.fallback
	if adapter == nil {
		adapter = &Jin10NewsAdapter{}
	}
	return adapter.NormalizeRawPayload(payload)
}

func (a *Jin10NewsAdapter) SourceName() string {
	return NewsSourceJin10
}

func (a *Jin10NewsAdapter) FetchSince(ctx context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error) {
	if strings.TrimSpace(a.endpoint) == "" {
		return NewsSourceFetchResult{Disabled: true, FetchedAt: time.Now()}, nil
	}
	endpoint, err := url.Parse(a.endpoint)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	if !isJin10PublicHost(endpoint.Hostname()) {
		query := endpoint.Query()
		if cursor.Cursor != "" {
			query.Set("cursor", cursor.Cursor)
		}
		if !cursor.Since.IsZero() {
			query.Set("since", cursor.Since.UTC().Format(time.RFC3339))
		}
		endpoint.RawQuery = query.Encode()
	}

	reqCtx, cancel := context.WithTimeout(ctx, defaultNewsSourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://www.jin10.com")
	req.Header.Set("Referer", "https://www.jin10.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PhantomLancer/stockv2)")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	if a.cookie != "" {
		req.Header.Set("Cookie", a.cookie)
	}
	if a.xAppID != "" {
		req.Header.Set("x-app-id", a.xAppID)
	}
	if a.xVersion != "" {
		req.Header.Set("x-version", a.xVersion)
	}
	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, newsSourceMaxBodyBytes))
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NewsSourceFetchResult{}, fmt.Errorf("jin10 fetch returned %s", resp.Status)
	}
	items, nextCursor, err := parseNewsSourceItems(body)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	return NewsSourceFetchResult{Items: items, NextCursor: nextCursor, FetchedAt: time.Now()}, nil
}

func (a *Jin10NewsAdapter) NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error) {
	dataPayload := payload
	if nested, ok := firstPayloadMap(payload, "data"); ok {
		dataPayload = nested
	}
	publishedAt := firstPayloadTime(payload, "published_at", "publishedAt", "time", "datetime", "created_at")
	if publishedAt.IsZero() {
		publishedAt = firstPayloadTime(dataPayload, "published_at", "publishedAt", "time", "datetime", "created_at")
	}
	req := RequestCreateRawNews{
		Source:      NewsSourceJin10,
		SourceID:    firstPayloadString(payload, "source_id", "sourceId", "id", "news_id"),
		Language:    firstPayloadString(payload, "language", "lang"),
		Title:       firstPayloadString(dataPayload, "title", "headline", "content", "text"),
		Content:     firstPayloadString(dataPayload, "content", "text", "message"),
		Snippet:     firstPayloadString(dataPayload, "snippet", "summary", "brief"),
		PublishedAt: publishedAt,
		RawPayload:  payload,
		Quality:     NewsQualityUnknown,
		Status:      NewsStatusNew,
	}
	if req.Language == "" {
		req.Language = "zh-CN"
	}
	if req.Title == "" && req.Content != "" {
		req.Title = req.Content
	}
	if req.Snippet == "" && req.Content != "" && req.Content != req.Title {
		req.Snippet = req.Content
	}
	if strings.TrimSpace(req.Title) == "" && strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.Snippet) == "" {
		return RequestCreateRawNews{}, ErrInvalidRawNewsContent
	}
	return req, nil
}

func ParseJin10CurlInput(raw string) (Jin10CurlConfig, error) {
	fields := shellFieldsLite(raw)
	var cfg Jin10CurlConfig
	for i, field := range fields {
		lower := strings.ToLower(field)
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			cfg.Endpoint = field
			continue
		}
		if (lower == "--url" || lower == "url") && i+1 < len(fields) {
			cfg.Endpoint = fields[i+1]
			continue
		}
		if strings.HasPrefix(lower, "--url=") {
			cfg.Endpoint = strings.TrimPrefix(field, field[:len("--url=")])
			continue
		}
		if (lower == "-b" || lower == "--cookie") && i+1 < len(fields) {
			cfg.Cookie = fields[i+1]
			continue
		}
		if strings.HasPrefix(lower, "--cookie=") {
			cfg.Cookie = strings.TrimPrefix(field, field[:len("--cookie=")])
			continue
		}
		if (lower == "-h" || lower == "--header") && i+1 < len(fields) {
			applyJin10Header(&cfg, fields[i+1])
			continue
		}
	}
	if cfg.Cookie == "" {
		for _, field := range fields {
			if cookie, ok := cookieFromHeader(field); ok {
				cfg.Cookie = cookie
				break
			}
		}
	}
	return cleanJin10CurlConfig(cfg)
}

func applyJin10Header(cfg *Jin10CurlConfig, raw string) {
	idx := strings.Index(raw, ":")
	if idx < 0 {
		return
	}
	key := strings.ToLower(strings.TrimSpace(raw[:idx]))
	value := strings.TrimSpace(raw[idx+1:])
	switch key {
	case "cookie":
		cfg.Cookie = value
	case "x-app-id":
		cfg.XAppID = value
	case "x-version":
		cfg.XVersion = value
	}
}

func cleanJin10CurlConfig(cfg Jin10CurlConfig) (Jin10CurlConfig, error) {
	cfg.Endpoint = strings.Trim(strings.TrimSpace(cfg.Endpoint), `'"`)
	cfg.Cookie = strings.Trim(strings.TrimSpace(cfg.Cookie), `'"`)
	cfg.XAppID = strings.Trim(strings.TrimSpace(cfg.XAppID), `'"`)
	cfg.XVersion = strings.Trim(strings.TrimSpace(cfg.XVersion), `'"`)
	if cfg.Endpoint == "" {
		return Jin10CurlConfig{}, ErrJin10ConfigMissing
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return Jin10CurlConfig{}, errors.New("invalid jin10 endpoint")
	}
	if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return Jin10CurlConfig{}, errors.New("jin10 endpoint must be http or https")
	}
	if !isJin10PublicHost(endpoint.Hostname()) {
		return Jin10CurlConfig{}, errors.New("jin10 endpoint must be under jin10.com")
	}
	if cfg.Cookie == "" || !strings.Contains(cfg.Cookie, "=") {
		return Jin10CurlConfig{}, ErrJin10ConfigMissing
	}
	if strings.ContainsAny(cfg.Cookie, "\r\n") || len(cfg.Cookie) > maxJin10CookieBytes {
		return Jin10CurlConfig{}, errors.New("jin10 cookie is invalid")
	}
	return cfg, nil
}

func isJin10PublicHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "jin10.com" || strings.HasSuffix(host, ".jin10.com")
}

func parseNewsSourceItems(body []byte) ([]map[string]any, string, error) {
	var direct []map[string]any
	if err := json.Unmarshal(body, &direct); err == nil {
		return direct, "", nil
	}
	var wrapped map[string]any
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, "", err
	}
	var items []map[string]any
	for _, key := range []string{"items", "data", "list", "result"} {
		if collected := collectNewsSourceMaps(wrapped[key], 0); len(collected) > 0 {
			items = collected
			break
		}
	}
	if len(items) == 0 {
		items = collectNewsSourceMaps(wrapped, 0)
	}
	nextCursor := firstPayloadString(wrapped, "nextCursor", "cursor")
	return items, nextCursor, nil
}

func collectNewsSourceMaps(value any, depth int) []map[string]any {
	if value == nil || depth > 3 {
		return nil
	}
	switch v := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			out = append(out, collectNewsSourceMaps(item, depth+1)...)
		}
		return out
	case []map[string]any:
		return v
	case map[string]any:
		if looksLikeNewsSourceItem(v) {
			return []map[string]any{v}
		}
		for _, key := range []string{"items", "data", "list", "result"} {
			if out := collectNewsSourceMaps(v[key], depth+1); len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

func looksLikeNewsSourceItem(item map[string]any) bool {
	return firstPayloadString(item, "id", "news_id", "time", "title", "content", "text", "message") != ""
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case int:
			return strconv.Itoa(typed)
		default:
			if text := strings.TrimSpace(fmt.Sprint(typed)); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstPayloadMap(payload map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		if item, ok := value.(map[string]any); ok {
			return item, true
		}
		if raw, ok := value.(string); ok {
			var item map[string]any
			if err := json.Unmarshal([]byte(raw), &item); err == nil {
				return item, true
			}
		}
	}
	return nil, false
}

func firstPayloadTime(payload map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if ts := parseNewsTimeString(typed); !ts.IsZero() {
				return ts
			}
		case float64:
			return unixNewsTime(typed)
		case int:
			return unixNewsTime(float64(typed))
		}
	}
	return time.Time{}
}

func parseNewsTimeString(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	} {
		if ts, err := time.Parse(layout, trimmed); err == nil {
			return ts
		}
	}
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return unixNewsTime(n)
	}
	return time.Time{}
}

func unixNewsTime(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 1e12 {
		return time.UnixMilli(int64(value))
	}
	if value > 1e10 {
		return time.UnixMilli(int64(value))
	}
	return time.Unix(int64(value), 0)
}
