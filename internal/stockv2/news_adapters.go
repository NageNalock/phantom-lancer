package stockv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	financialJuiceStartupURL  = "https://live.financialjuice.com/FJService.asmx/Startup"
	maxNewsAdapterBodyBytes   = 2 << 20
	maxFinancialJuiceEndpoint = 16 << 10
	maxFinancialJuiceCookie   = 16 << 10
)

type FinancialJuiceAdapterConfig struct {
	Enabled  bool
	Cookie   string
	Endpoint string
	Client   *http.Client
	Now      func() time.Time
}

type FinancialJuiceRawNewsAdapter struct {
	cfg FinancialJuiceAdapterConfig
}

func NewFinancialJuiceRawNewsAdapter(cfg FinancialJuiceAdapterConfig) *FinancialJuiceRawNewsAdapter {
	if cfg.Endpoint == "" {
		cfg.Endpoint = financialJuiceStartupURL
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &FinancialJuiceRawNewsAdapter{cfg: cfg}
}

func (a *FinancialJuiceRawNewsAdapter) Source() string {
	return NewsSourceFinancialJuice
}

func (a *FinancialJuiceRawNewsAdapter) FetchRawNews(ctx context.Context) ([]RequestCreateRawNews, error) {
	if !a.cfg.Enabled {
		return nil, ErrNewsAdapterDisabled
	}
	cookie := strings.TrimSpace(a.cfg.Cookie)
	endpoint := strings.TrimSpace(a.cfg.Endpoint)
	if cookie == "" && !financialJuiceEndpointHasCredential(endpoint) {
		return nil, ErrFinancialJuiceCookieMissing
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, financialJuiceRequestURL(endpoint), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Origin", "https://www.financialjuice.com")
	req.Header.Set("Referer", "https://www.financialjuice.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PhantomLancer/stockv2)")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := a.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNewsAdapterBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrFinancialJuiceInvalidCredential
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("financialjuice fetch failed: status %d", resp.StatusCode)
	}
	return ParseFinancialJuiceRawNews(body, a.cfg.Now())
}

type financialJuiceNewsSourceAdapter struct {
	service *Service
}

func (a financialJuiceNewsSourceAdapter) SourceName() string {
	return NewsSourceFinancialJuice
}

func (a financialJuiceNewsSourceAdapter) FetchSince(ctx context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error) {
	if a.service == nil {
		return NewsSourceFetchResult{}, ErrNewsSourceAdapterNotFound
	}
	settings, err := a.service.GetSettings(ctx)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	if !settings.FinancialJuiceEnabled {
		return NewsSourceFetchResult{Disabled: true, FetchedAt: time.Now()}, nil
	}
	adapter := NewFinancialJuiceRawNewsAdapter(FinancialJuiceAdapterConfig{
		Enabled:  settings.FinancialJuiceEnabled,
		Endpoint: settings.FinancialJuiceEndpoint,
		Cookie:   settings.FinancialJuiceCookie,
		Client:   a.service.httpClient,
	})
	rawItems, err := adapter.FetchRawNews(ctx)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	fetchedAt := time.Now()
	if !cursor.Since.IsZero() {
		filtered := rawItems[:0]
		for _, raw := range rawItems {
			if raw.PublishedAt.IsZero() || raw.PublishedAt.After(cursor.Since) {
				filtered = append(filtered, raw)
			}
		}
		rawItems = filtered
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		items = append(items, rawNewsRequestPayload(raw))
	}
	return NewsSourceFetchResult{Items: items, FetchedAt: fetchedAt}, nil
}

func (a financialJuiceNewsSourceAdapter) NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error) {
	return rawNewsRequestFromAdapterPayload(NewsSourceFinancialJuice, payload, time.Now())
}

func (s *Service) FetchRawNewsFromSource(ctx context.Context, source string) (NewsPipelineRunResult, error) {
	return s.RunNewsPipelineOnce(ctx, source)
}

func ParseFinancialJuiceCookieInput(raw string) (string, error) {
	cfg, err := ParseFinancialJuiceCredentialInput(raw)
	if err != nil {
		return "", err
	}
	if cfg.Cookie == "" {
		return "", ErrFinancialJuiceCookieMissing
	}
	return cfg.Cookie, nil
}

type FinancialJuiceCredentialConfig struct {
	Endpoint string
	Cookie   string
}

func ParseFinancialJuiceCredentialInput(raw string) (FinancialJuiceCredentialConfig, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return FinancialJuiceCredentialConfig{}, ErrFinancialJuiceCookieMissing
	}
	cfg := FinancialJuiceCredentialConfig{Endpoint: financialJuiceCredentialEndpointFromFields(shellFieldsLite(text))}
	for _, field := range shellFieldsLite(text) {
		if cookie, ok := cookieFromHeader(field); ok {
			cleaned, err := cleanFinancialJuiceCookie(cookie)
			if err != nil {
				return FinancialJuiceCredentialConfig{}, err
			}
			cfg.Cookie = cleaned
			return cfg, nil
		}
	}
	fields := shellFieldsLite(text)
	for i, field := range fields {
		lower := strings.ToLower(field)
		if (lower == "-h" || lower == "--header" || lower == "-b" || lower == "--cookie") && i+1 < len(fields) {
			next := fields[i+1]
			if lower == "-b" || lower == "--cookie" {
				cleaned, err := cleanFinancialJuiceCookie(next)
				if err != nil {
					return FinancialJuiceCredentialConfig{}, err
				}
				cfg.Cookie = cleaned
				return cfg, nil
			}
			if cookie, ok := cookieFromHeader(next); ok {
				cleaned, err := cleanFinancialJuiceCookie(cookie)
				if err != nil {
					return FinancialJuiceCredentialConfig{}, err
				}
				cfg.Cookie = cleaned
				return cfg, nil
			}
		}
		if strings.HasPrefix(lower, "--cookie=") {
			cleaned, err := cleanFinancialJuiceCookie(strings.TrimPrefix(field, field[:len("--cookie=")]))
			if err != nil {
				return FinancialJuiceCredentialConfig{}, err
			}
			cfg.Cookie = cleaned
			return cfg, nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if cookie, ok := cookieFromHeader(line); ok {
			cleaned, err := cleanFinancialJuiceCookie(cookie)
			if err != nil {
				return FinancialJuiceCredentialConfig{}, err
			}
			cfg.Cookie = cleaned
			return cfg, nil
		}
	}
	if cfg.Endpoint != "" {
		return cfg, nil
	}
	if strings.Contains(text, "=") && !strings.Contains(strings.ToLower(text), "curl ") {
		cleaned, err := cleanFinancialJuiceCookie(text)
		if err != nil {
			return FinancialJuiceCredentialConfig{}, err
		}
		cfg.Cookie = cleaned
		return cfg, nil
	}
	return FinancialJuiceCredentialConfig{}, ErrFinancialJuiceCookieMissing
}

func ParseFinancialJuiceRawNews(body []byte, fetchedAt time.Time) ([]RequestCreateRawNews, error) {
	body = bytes.TrimSpace(body)
	if financialJuiceResponseLooksHTML(body) {
		return nil, ErrFinancialJuiceInvalidCredential
	}
	if financialJuiceResponseLooksXML(body) {
		var envelope struct {
			Text string `xml:",chardata"`
		}
		if err := xml.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		body = []byte(strings.TrimSpace(envelope.Text))
	}
	return parseEnglishRawNewsPayload(NewsSourceFinancialJuice, body, fetchedAt)
}

func ParseAlphaVantageRawNews(body []byte, fetchedAt time.Time) ([]RequestCreateRawNews, error) {
	return parseEnglishRawNewsPayload(NewsSourceAlphaVantage, body, fetchedAt)
}

func ParseFMPRawNews(body []byte, fetchedAt time.Time) ([]RequestCreateRawNews, error) {
	return parseEnglishRawNewsPayload(NewsSourceFMP, body, fetchedAt)
}

func financialJuiceRequestURL(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	q := u.Query()
	if q.Get("TimeOffset") == "" {
		q.Set("TimeOffset", "8")
	}
	for _, key := range []string{"tabID", "oldID", "TickerID", "FeedCompanyID", "extraNID"} {
		if q.Get(key) == "" {
			q.Set(key, "0")
		}
	}
	if _, ok := q["strSearch"]; !ok {
		q.Set("strSearch", "")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func financialJuiceCredentialEndpointFromFields(fields []string) string {
	for _, field := range fields {
		endpoint, err := cleanFinancialJuiceEndpoint(field)
		if err == nil && financialJuiceEndpointHasCredential(endpoint) {
			return endpoint
		}
	}
	return ""
}

func cleanFinancialJuiceEndpoint(raw string) (string, error) {
	endpoint := strings.Trim(strings.TrimSpace(strings.TrimSuffix(raw, "\\")), `'"`)
	if endpoint == "" || len(endpoint) > maxFinancialJuiceEndpoint {
		return "", ErrFinancialJuiceCookieMissing
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || !isFinancialJuiceHost(parsed.Hostname()) {
		return "", ErrFinancialJuiceCookieMissing
	}
	if !strings.EqualFold(parsed.Path, "/FJService.asmx/Startup") {
		return "", ErrFinancialJuiceCookieMissing
	}
	return parsed.String(), nil
}

func financialJuiceEndpointHasCredential(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Path, "/FJService.asmx/Startup") &&
		parsed.Query().Get("info") != "" &&
		isFinancialJuiceHost(parsed.Hostname())
}

func financialJuiceResponseLooksHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 32)]))
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html")
}

func financialJuiceResponseLooksXML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 32)]))
	return strings.HasPrefix(lower, "<?xml") || strings.HasPrefix(lower, "<string")
}

func isFinancialJuiceHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "financialjuice.com" || strings.HasSuffix(host, ".financialjuice.com")
}

func parseEnglishRawNewsPayload(source string, body []byte, fetchedAt time.Time) ([]RequestCreateRawNews, error) {
	var payload any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	var items []RequestCreateRawNews
	collectEnglishRawNews(source, payload, fetchedAt, &items)
	return dedupeRawNewsRequests(items), nil
}

func collectEnglishRawNews(source string, value any, fetchedAt time.Time, out *[]RequestCreateRawNews) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			collectEnglishRawNews(source, item, fetchedAt, out)
		}
	case map[string]any:
		if newsItems, ok := newsContainerItems(v); ok {
			collectEnglishRawNews(source, newsItems, fetchedAt, out)
			return
		}
		if req, ok := rawNewsRequestFromMap(source, v, fetchedAt); ok {
			*out = append(*out, req)
			return
		}
		for _, item := range v {
			collectEnglishRawNews(source, item, fetchedAt, out)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			return
		}
		var nested any
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&nested); err == nil {
			collectEnglishRawNews(source, nested, fetchedAt, out)
		}
	}
}

func newsContainerItems(item map[string]any) (any, bool) {
	for key, value := range item {
		if strings.EqualFold(key, "News") {
			return value, true
		}
	}
	return nil, false
}

func rawNewsRequestFromMap(source string, item map[string]any, fetchedAt time.Time) (RequestCreateRawNews, bool) {
	title := firstNewsString(item, "title", "Title", "headline", "Headline", "NewsTitle", "news_title")
	snippet := firstNewsString(item, "snippet", "Snippet", "summary", "Summary", "description", "Description", "text", "Text")
	content := firstNewsString(item, "content", "Content", "body", "Body", "text", "Text", "summary", "Summary")
	if title == "" && snippet != "" {
		title = shortenNewsText(snippet, 180)
	}
	if title == "" && content != "" {
		title = shortenNewsText(content, 180)
	}
	if title == "" && snippet == "" && content == "" {
		return RequestCreateRawNews{}, false
	}

	publishedAt := parseNewsPublishedAt(firstNewsString(item,
		"published_at", "publishedAt", "publishedDate", "PublishedDate", "time_published", "TimePublished",
		"date", "Date", "datetime", "DateTime", "time", "Time", "NewsTime", "CreatedDate", "DatePublished",
	), fetchedAt)
	newsURL := firstNewsString(item, "url", "URL", "link", "Link", "newsUrl", "NewsURL", "NewsUrl", "EURL", "RURL")
	sourceID := firstNewsString(item, "source_id", "sourceId", "SourceID", "id", "ID", "newsId", "NewsID", "NewsId", "NID")
	if sourceID == "" {
		sourceID = stableNewsSourceID(source, newsURL, title, publishedAt)
	}
	hash := rawNewsContentHash(source, sourceID, title, content, snippet, newsURL, publishedAt)
	return RequestCreateRawNews{
		Source:      source,
		SourceID:    sourceID,
		Language:    "en",
		Title:       title,
		Content:     content,
		Snippet:     snippet,
		PublishedAt: publishedAt,
		URL:         newsURL,
		FetchedAt:   fetchedAt,
		RawPayload:  copyNewsMap(item),
		DedupeKey:   rawNewsDedupeKey(source, sourceID, hash),
		Quality:     NewsQualityOK,
		Status:      NewsStatusNew,
	}, true
}

func rawNewsRequestPayload(item RequestCreateRawNews) map[string]any {
	payload := map[string]any{
		"source_id":    item.SourceID,
		"title":        item.Title,
		"snippet":      item.Snippet,
		"content":      item.Content,
		"url":          item.URL,
		"dedupe_key":   item.DedupeKey,
		"quality":      item.Quality,
		"raw_payload":  item.RawPayload,
		"published_at": item.PublishedAt.UTC().Format(time.RFC3339Nano),
		"fetched_at":   item.FetchedAt.UTC().Format(time.RFC3339Nano),
	}
	return payload
}

func rawNewsRequestFromAdapterPayload(source string, payload map[string]any, fallbackFetchedAt time.Time) (RequestCreateRawNews, error) {
	req, ok := rawNewsRequestFromMap(source, payload, fallbackFetchedAt)
	if !ok {
		return RequestCreateRawNews{}, ErrInvalidRawNewsContent
	}
	if fetchedAt := parseNewsPublishedAt(firstNewsString(payload, "fetched_at", "fetchedAt"), fallbackFetchedAt); !fetchedAt.IsZero() {
		req.FetchedAt = fetchedAt
	}
	if dedupeKey := firstNewsString(payload, "dedupe_key", "dedupeKey"); dedupeKey != "" {
		req.DedupeKey = dedupeKey
	}
	if quality := firstNewsString(payload, "quality", "Quality"); quality != "" {
		req.Quality = quality
	}
	if rawPayload, ok := payload["raw_payload"].(map[string]any); ok {
		req.RawPayload = rawPayload
	}
	return req, nil
}

func firstNewsString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			if text := newsValueString(value); text != "" {
				return text
			}
		}
	}
	lower := make(map[string]any, len(item))
	for key, value := range item {
		lower[strings.ToLower(key)] = value
	}
	for _, key := range keys {
		if value, ok := lower[strings.ToLower(key)]; ok {
			if text := newsValueString(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func newsValueString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func parseNewsPublishedAt(raw string, fetchedAt time.Time) time.Time {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}
	}
	if strings.HasPrefix(text, "/Date(") {
		end := strings.Index(text, ")/")
		if end > len("/Date(") {
			return epochNewsTime(text[len("/Date("):end])
		}
	}
	if num, err := strconv.ParseInt(text, 10, 64); err == nil {
		return epochNewsTime(strconv.FormatInt(num, 10))
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"20060102T150405",
		"20060102T1504",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t
		}
	}
	if len(text) == len("15:04:05") {
		if t, err := time.Parse("15:04:05", text); err == nil && !fetchedAt.IsZero() {
			y, m, d := fetchedAt.Date()
			return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, fetchedAt.Location())
		}
	}
	return time.Time{}
}

func epochNewsTime(raw string) time.Time {
	text := strings.TrimSpace(raw)
	if len(text) > 1 {
		if idx := strings.IndexAny(text[1:], "+-"); idx >= 0 {
			text = text[:idx+1]
		}
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return time.Time{}
	}
	if n > 1_000_000_000_000 {
		return time.UnixMilli(n)
	}
	if n > 1_000_000_000 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func stableNewsSourceID(source, newsURL, title string, publishedAt time.Time) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(source),
		strings.TrimSpace(newsURL),
		strings.TrimSpace(title),
		publishedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	return "hash-" + hex.EncodeToString(sum[:])[:16]
}

func dedupeRawNewsRequests(items []RequestCreateRawNews) []RequestCreateRawNews {
	out := make([]RequestCreateRawNews, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.TrimSpace(item.DedupeKey)
		if key == "" {
			key = item.Source + ":" + item.SourceID
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeNewsSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "financial_juice", "financialjuice", "fj":
		return NewsSourceFinancialJuice
	case "alpha", "alphavantage", "alpha_vantage":
		return NewsSourceAlphaVantage
	case "fmp", "financial_modeling_prep":
		return NewsSourceFMP
	default:
		return strings.ToLower(strings.TrimSpace(source))
	}
}

func cookieFromHeader(raw string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(raw, "\\"))
	idx := strings.Index(trimmed, ":")
	if idx < 0 {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(trimmed[:idx]), "cookie") {
		return "", false
	}
	return strings.TrimSpace(trimmed[idx+1:]), true
}

func cleanFinancialJuiceCookie(raw string) (string, error) {
	cookie := strings.Trim(strings.TrimSpace(raw), `'"`)
	if cookie == "" || !strings.Contains(cookie, "=") {
		return "", ErrFinancialJuiceCookieMissing
	}
	if strings.ContainsAny(cookie, "\r\n") {
		return "", errors.New("financialjuice cookie must be a single header line")
	}
	if len(cookie) > maxFinancialJuiceCookie {
		return "", errors.New("financialjuice cookie is too large")
	}
	return cookie, nil
}

func shellFieldsLite(raw string) []string {
	var fields []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
	}
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return fields
}

func shortenNewsText(value string, limit int) string {
	text := strings.TrimSpace(value)
	if len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func copyNewsMap(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for key, value := range item {
		out[key] = value
	}
	return out
}
