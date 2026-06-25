package stockv2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	NewsSourceJin10           = "jin10"
	defaultJin10FlashEndpoint = "https://flash-api.jin10.com/get_flash_list?channel=-8200&vip=1"
	defaultJin10XAppID        = "bVBF4FyRTn5NJF5n"
	defaultJin10XVersion      = "1.0.0"
	newsSourceMaxBodyBytes    = 2 << 20
	defaultNewsSourceTimeout  = 10 * time.Second
)

var jin10Location = time.FixedZone("Asia/Shanghai", 8*60*60)

type Jin10NewsAdapter struct {
	httpClient *http.Client
}

type jin10NewsSourceAdapter struct {
	httpClient *http.Client
}

func (a jin10NewsSourceAdapter) SourceName() string {
	return NewsSourceJin10
}

func (a jin10NewsSourceAdapter) FetchSince(ctx context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error) {
	return (&Jin10NewsAdapter{httpClient: a.httpClient}).FetchSince(ctx, cursor)
}

func (a jin10NewsSourceAdapter) NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error) {
	return (&Jin10NewsAdapter{}).NormalizeRawPayload(payload)
}

func (a *Jin10NewsAdapter) SourceName() string {
	return NewsSourceJin10
}

func (a *Jin10NewsAdapter) FetchSince(ctx context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, defaultNewsSourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, defaultJin10FlashEndpoint, nil)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://www.jin10.com")
	req.Header.Set("Referer", "https://www.jin10.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PhantomLancer/stockv2)")
	req.Header.Set("x-app-id", defaultJin10XAppID)
	req.Header.Set("x-version", defaultJin10XVersion)
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
	hasWrappedItems := false
	for _, key := range []string{"items", "data", "list", "result"} {
		value, ok := wrapped[key]
		if !ok {
			continue
		}
		hasWrappedItems = true
		if collected := collectNewsSourceMaps(value, 0); len(collected) > 0 {
			items = collected
			break
		}
	}
	if len(items) == 0 && !hasWrappedItems {
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
	} {
		if ts, err := time.Parse(layout, trimmed); err == nil {
			return ts
		}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if ts, err := time.ParseInLocation(layout, trimmed, jin10Location); err == nil {
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
