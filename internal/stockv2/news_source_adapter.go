package stockv2

import (
	"context"
	"encoding/json"
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
)

type Jin10NewsAdapter struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

func NewJin10NewsAdapterFromEnv(httpClient *http.Client) *Jin10NewsAdapter {
	return &Jin10NewsAdapter{
		endpoint:   strings.TrimSpace(os.Getenv(jin10EndpointEnv)),
		token:      strings.TrimSpace(os.Getenv(jin10TokenEnv)),
		httpClient: httpClient,
	}
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
	query := endpoint.Query()
	if cursor.Cursor != "" {
		query.Set("cursor", cursor.Cursor)
	}
	if !cursor.Since.IsZero() {
		query.Set("since", cursor.Since.UTC().Format(time.RFC3339))
	}
	endpoint.RawQuery = query.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, defaultNewsSourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return NewsSourceFetchResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
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
	req := RequestCreateRawNews{
		Source:      NewsSourceJin10,
		SourceID:    firstPayloadString(payload, "source_id", "sourceId", "id", "news_id"),
		Language:    firstPayloadString(payload, "language", "lang"),
		Title:       firstPayloadString(payload, "title", "headline"),
		Content:     firstPayloadString(payload, "content", "text", "message"),
		Snippet:     firstPayloadString(payload, "snippet", "summary", "brief"),
		PublishedAt: firstPayloadTime(payload, "published_at", "publishedAt", "time", "datetime", "created_at"),
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
	var wrapped struct {
		Items      []map[string]any `json:"items"`
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"nextCursor"`
		Cursor     string           `json:"cursor"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, "", err
	}
	items := wrapped.Items
	if len(items) == 0 {
		items = wrapped.Data
	}
	nextCursor := wrapped.NextCursor
	if nextCursor == "" {
		nextCursor = wrapped.Cursor
	}
	return items, nextCursor, nil
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
