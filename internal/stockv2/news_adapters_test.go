package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFinancialJuiceCookieParseAndSettingsSave(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.httpClient = emptyNewsHTTPClient()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	enabled := true
	input := `curl 'https://live.financialjuice.com/FJService.asmx/Startup?TimeOffset=8' \
  -H 'accept: application/json' \
  -H 'Cookie: example_session=placeholder; prefs=sample'`
	updated, err := svc.UpdateNewsSourceConfig(ctx, NewsSourceFinancialJuice, NewsSourceConfigPatch{
		Enabled:         &enabled,
		CredentialInput: &input,
	})
	if err != nil {
		t.Fatalf("save source config: %v", err)
	}
	if !updated.State.Enabled || !updated.CredentialSet || !updated.Configured {
		t.Fatalf("updated source = %+v", updated)
	}
	stored, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if stored.FinancialJuiceCookie != "example_session=placeholder; prefs=sample" {
		t.Fatalf("stored cookie = %q", stored.FinancialJuiceCookie)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if strings.Contains(string(encoded), "placeholder") || strings.Contains(string(encoded), "example_session") {
		t.Fatalf("settings JSON leaked cookie: %s", encoded)
	}
}

func TestFinancialJuiceInfoEndpointParseAndSettingsSave(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.httpClient = emptyNewsHTTPClient()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	enabled := true
	input := `curl 'https://live.financialjuice.com/FJService.asmx/Startup?info=%22token-placeholder%22&TimeOffset=8&tabID=0' \
  -H 'accept: application/json' \
  -H 'referer: https://www.financialjuice.com/'`
	updated, err := svc.UpdateNewsSourceConfig(ctx, NewsSourceFinancialJuice, NewsSourceConfigPatch{
		Enabled:         &enabled,
		CredentialInput: &input,
	})
	if err != nil {
		t.Fatalf("save source config: %v", err)
	}
	if !updated.State.Enabled || !updated.CredentialSet || !updated.Configured {
		t.Fatalf("updated source = %+v", updated)
	}
	stored, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if stored.FinancialJuiceCookie != "" {
		t.Fatalf("stored cookie = %q, want empty cookie for endpoint credential", stored.FinancialJuiceCookie)
	}
	if !strings.Contains(stored.FinancialJuiceEndpoint, "info=%22token-placeholder%22") {
		t.Fatalf("stored endpoint = %q, want info credential", stored.FinancialJuiceEndpoint)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if strings.Contains(string(encoded), "token-placeholder") || strings.Contains(string(encoded), "info=") {
		t.Fatalf("settings JSON leaked endpoint credential: %s", encoded)
	}

	cfg, err := ParseFinancialJuiceCredentialInput("https://live.financialjuice.com/FJService.asmx/Startup?info=%22token-placeholder%22")
	if err != nil {
		t.Fatalf("parse bare endpoint: %v", err)
	}
	if cfg.Cookie != "" || !strings.Contains(cfg.Endpoint, "info=%22token-placeholder%22") {
		t.Fatalf("bare endpoint config = %+v, want endpoint credential without cookie", cfg)
	}
}

func TestJin10SourceUsesDefaultFlashEndpointWithoutCurl(t *testing.T) {
	var sawURL string
	var sawAppID string
	var sawVersion string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawURL = r.URL.String()
		sawAppID = r.Header.Get("x-app-id")
		sawVersion = r.Header.Get("x-version")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"20260623230511003800","time":"2026-06-23 23:05:11","type":0,"data":{"content":"加拿大央行行长麦克勒姆：前瞻指引中的虚假精确性是无益的。"}}]}`)),
			Request:    r,
		}, nil
	})}
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.httpClient = client
	svc.newsAdapters[NewsSourceJin10] = jin10NewsSourceAdapter{httpClient: client}
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	enabled := true
	if _, err := svc.UpdateNewsSourceConfig(ctx, NewsSourceJin10, NewsSourceConfigPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("enable jin10 source: %v", err)
	}
	configured, reason := svc.newsSourceConfigured(ctx, NewsSourceJin10)
	if !configured {
		t.Fatalf("configured = false, reason = %q", reason)
	}
	result, err := svc.RunNewsIngestJob(ctx, NewsSourceJin10)
	if err != nil {
		t.Fatalf("run ingest: %v", err)
	}
	if result.FetchedCount != 1 || result.RawInsertedCount != 1 {
		t.Fatalf("run result = %+v", result)
	}
	if sawURL != defaultJin10FlashEndpoint {
		t.Fatalf("url = %q", sawURL)
	}
	if sawAppID != defaultJin10XAppID || sawVersion != defaultJin10XVersion {
		t.Fatalf("headers app/version = %q/%q", sawAppID, sawVersion)
	}
}

func emptyNewsHTTPClient() *http.Client {
	return &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
			Request:    r,
		}, nil
	})}
}

func TestJin10FetchUsesDefaultConfigAndParsesNestedPayload(t *testing.T) {
	var sawURL string
	var sawCookie string
	var sawAppID string
	var sawVersion string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawURL = r.URL.String()
		sawCookie = r.Header.Get("Cookie")
		sawAppID = r.Header.Get("x-app-id")
		sawVersion = r.Header.Get("x-version")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"jin10-1","data":{"content":"沪指午后走强","time":"2026-06-23 14:30:00"}}]}`)),
			Request:    r,
		}, nil
	})}
	adapter := &Jin10NewsAdapter{httpClient: client}
	result, err := adapter.FetchSince(context.Background(), NewsSourceCursor{Cursor: "ignored"})
	if err != nil {
		t.Fatalf("fetch jin10: %v", err)
	}
	if sawURL != defaultJin10FlashEndpoint {
		t.Fatalf("url = %q", sawURL)
	}
	if sawCookie != "" || sawAppID != defaultJin10XAppID || sawVersion != defaultJin10XVersion {
		t.Fatalf("headers cookie/app/version = %q/%q/%q", sawCookie, sawAppID, sawVersion)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(result.Items))
	}
	raw, err := adapter.NormalizeRawPayload(result.Items[0])
	if err != nil {
		t.Fatalf("normalize jin10: %v", err)
	}
	if raw.Title != "沪指午后走强" || raw.Language != "zh-CN" || raw.PublishedAt.IsZero() {
		t.Fatalf("raw news = %+v", raw)
	}
	if got := raw.PublishedAt.Format(time.RFC3339); got != "2026-06-23T14:30:00+08:00" {
		t.Fatalf("published_at = %s, want Jin10 local time +08:00", got)
	}
}

func TestJin10FetchEmptyDataWrapperDoesNotBecomeOKNews(t *testing.T) {
	adapter := &Jin10NewsAdapter{
		httpClient: &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[],"message":"OK","status":200}`)),
				Request:    r,
			}, nil
		})},
	}
	result, err := adapter.FetchSince(context.Background(), NewsSourceCursor{})
	if err != nil {
		t.Fatalf("fetch jin10: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want empty", result.Items)
	}
}

func TestFinancialJuiceAdapterDisabled(t *testing.T) {
	adapter := NewFinancialJuiceRawNewsAdapter(FinancialJuiceAdapterConfig{})
	if _, err := adapter.FetchRawNews(context.Background()); !errors.Is(err, ErrNewsAdapterDisabled) {
		t.Fatalf("FetchRawNews() err = %v, want ErrNewsAdapterDisabled", err)
	}
}

func TestFinancialJuiceMockFetchToRawNews(t *testing.T) {
	var sawCookie string
	var sawContentType string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawCookie = r.Header.Get("Cookie")
		sawContentType = r.Header.Get("Content-Type")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"d":"{\"News\":[{\"NewsID\":67890,\"Headline\":\"Fed officials discuss rate path\",\"Text\":\"Policy comments moved US yields.\",\"NewsTime\":\"2026-06-18T10:30:00Z\",\"URL\":\"https://example.test/fed\"}]}"}`)),
			Request:    r,
		}, nil
	})}

	adapter := NewFinancialJuiceRawNewsAdapter(FinancialJuiceAdapterConfig{
		Enabled:  true,
		Cookie:   "example_session=placeholder",
		Endpoint: "https://live.financialjuice.com/FJService.asmx/Startup",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 18, 10, 31, 0, 0, time.UTC) },
	})
	items, err := adapter.FetchRawNews(context.Background())
	if err != nil {
		t.Fatalf("fetch raw news: %v", err)
	}
	if sawCookie != "example_session=placeholder" {
		t.Fatalf("cookie header = %q", sawCookie)
	}
	if sawContentType != "application/json; charset=utf-8" {
		t.Fatalf("content-type header = %q", sawContentType)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	got := items[0]
	if got.Source != NewsSourceFinancialJuice || got.SourceID != "67890" || got.Language != "en" || got.Quality != NewsQualityOK {
		t.Fatalf("raw news = %+v", got)
	}
	if got.Title != "Fed officials discuss rate path" || got.Snippet != "Policy comments moved US yields." || got.URL != "https://example.test/fed" {
		t.Fatalf("raw news fields = %+v", got)
	}
	if got.PublishedAt.IsZero() {
		t.Fatalf("published_at was not parsed")
	}
}

func TestFinancialJuiceMockFetchUsesInfoEndpointWithoutCookie(t *testing.T) {
	var sawURL string
	var sawCookie string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawURL = r.URL.String()
		sawCookie = r.Header.Get("Cookie")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"d":"{\"News\":[]}"}`)),
			Request:    r,
		}, nil
	})}

	adapter := NewFinancialJuiceRawNewsAdapter(FinancialJuiceAdapterConfig{
		Enabled:  true,
		Endpoint: "https://live.financialjuice.com/FJService.asmx/Startup?info=%22token-placeholder%22&TimeOffset=8",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 18, 10, 31, 0, 0, time.UTC) },
	})
	if _, err := adapter.FetchRawNews(context.Background()); err != nil {
		t.Fatalf("fetch raw news: %v", err)
	}
	if !strings.Contains(sawURL, "info=%22token-placeholder%22") {
		t.Fatalf("request URL = %q, want info credential", sawURL)
	}
	if strings.Contains(sawURL, "TimeOffset=8") || !strings.Contains(sawURL, "TimeOffset=0") {
		t.Fatalf("request URL = %q, want normalized UTC TimeOffset", sawURL)
	}
	if sawCookie != "" {
		t.Fatalf("cookie header = %q, want empty cookie", sawCookie)
	}
}

func TestFinancialJuiceTimeOffsetUsesLocalUTCOffset(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	got := financialJuiceTimeOffset(time.Date(2026, 6, 24, 16, 0, 0, 0, loc))
	if got != "8" {
		t.Fatalf("financialJuiceTimeOffset = %q, want 8", got)
	}
}

func TestNewsPublishedAtClampsFutureDisplayTime(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	fetchedAt := time.Date(2026, 6, 24, 0, 40, 0, 0, loc)

	for _, raw := range []string{"08:40:00", "2026-06-24T08:40:00", "2026-06-24 08:40:00"} {
		got := parseNewsPublishedAt(raw, fetchedAt)
		if !got.Equal(fetchedAt) {
			t.Fatalf("parseNewsPublishedAt(%q) = %s, want fetch time %s", raw, got, fetchedAt)
		}
	}
}

func TestFinancialJuiceHTMLResponseIsCredentialInvalid(t *testing.T) {
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(`<!doctype html><html><body>login</body></html>`)),
			Request:    r,
		}, nil
	})}

	adapter := NewFinancialJuiceRawNewsAdapter(FinancialJuiceAdapterConfig{
		Enabled:  true,
		Endpoint: "https://live.financialjuice.com/FJService.asmx/Startup?info=%22token-placeholder%22&TimeOffset=8",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 18, 10, 31, 0, 0, time.UTC) },
	})
	if _, err := adapter.FetchRawNews(context.Background()); !errors.Is(err, ErrFinancialJuiceInvalidCredential) {
		t.Fatalf("FetchRawNews() err = %v, want ErrFinancialJuiceInvalidCredential", err)
	}
}

func TestFinancialJuiceXMLResponseToRawNews(t *testing.T) {
	items, err := ParseFinancialJuiceRawNews([]byte(`<?xml version="1.0" encoding="utf-8"?>
<string xmlns="http://tempuri.org/">{"News":[{"NewsID":12345,"Title":"BoC remarks cross the wire","Text":"Central bank comments moved CAD.","NewsTime":"2026-06-18T10:30:00Z","URL":"https://example.test/boc"}]}</string>`), time.Date(2026, 6, 18, 10, 31, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parse xml response: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	got := items[0]
	if got.SourceID != "12345" || got.Title != "BoC remarks cross the wire" || got.Snippet != "Central bank comments moved CAD." {
		t.Fatalf("raw news = %+v", got)
	}
}

func TestFinancialJuiceContainerSummaryDoesNotBecomeNews(t *testing.T) {
	items, err := ParseFinancialJuiceRawNews([]byte(`{"d":"{\"News\":[{\"NewsID\":12345,\"Title\":\"Real headline\",\"Text\":\"Real text\",\"DatePublished\":\"2026-06-18T10:30:00Z\"}],\"Summary\":\"<div>not a news item</div>\"}"}`), time.Date(2026, 6, 18, 10, 31, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	if items[0].Title != "Real headline" || items[0].SourceID != "12345" {
		t.Fatalf("raw news = %+v", items[0])
	}
}

func TestFinancialJuiceRunsThroughNewsPipeline(t *testing.T) {
	var sawCookie string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawCookie = r.Header.Get("Cookie")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"d":"{\"News\":[{\"NewsID\":98765,\"Headline\":\"Fed officials discuss rate path\",\"Text\":\"Policy comments moved US yields.\",\"NewsTime\":\"2026-06-18T10:30:00Z\",\"URL\":\"https://example.test/fed\"}]}"}`)),
			Request:    r,
		}, nil
	})}
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.httpClient = client
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	enabled := true
	cookie := "example_session=placeholder"
	if _, err := svc.UpdateNewsSourceConfig(ctx, NewsSourceFinancialJuice, NewsSourceConfigPatch{
		Enabled:         &enabled,
		CredentialInput: &cookie,
	}); err != nil {
		t.Fatalf("save source config: %v", err)
	}
	svc.WithNewsEventLinker(NewsEventLinkerFunc(func(ctx context.Context, event NewsEvent) ([]NewsLinkCandidate, error) {
		return []NewsLinkCandidate{{
			Symbol:      "FED001",
			Market:      "US",
			MatchMethod: "test",
			Score:       0.8,
			Reason:      event.Title,
		}}, nil
	}))

	result, err := svc.RunNewsPipelineOnce(ctx, NewsSourceFinancialJuice)
	if err != nil {
		t.Fatalf("run financialjuice pipeline: %v", err)
	}
	if sawCookie != cookie {
		t.Fatalf("cookie header = %q, want configured cookie", sawCookie)
	}
	if result.FetchedCount != 1 || result.RawInsertedCount != 1 || result.NormalizedCount != 1 || result.LinkCandidateCount != 1 {
		t.Fatalf("pipeline result = %+v, want full ingest/process/link", result)
	}
	state, ok, err := svc.GetNewsSourceState(ctx, NewsSourceFinancialJuice)
	if err != nil {
		t.Fatalf("get source state: %v", err)
	}
	if !ok || state.RawNewsCount != 1 || state.NewsEventCount != 1 || state.LinkCandidateCount != 1 {
		t.Fatalf("state = %+v, ok=%v", state, ok)
	}
	candidates, err := svc.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{Symbol: "FED001"})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].NewsEventID == "" {
		t.Fatalf("candidates = %+v, want linked FinancialJuice candidate", candidates)
	}
}

func TestMockAlphaVantageAndFMPResponseToRawNews(t *testing.T) {
	fetchedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	alpha, err := ParseAlphaVantageRawNews([]byte(`{
	  "feed": [{
	    "title": "Apple supplier shares rise",
	    "url": "https://example.test/aapl",
	    "time_published": "20260618T113000",
	    "summary": "The move followed new AI server demand.",
	    "source": "Example Wire"
	  }]
	}`), fetchedAt)
	if err != nil {
		t.Fatalf("parse alpha vantage: %v", err)
	}
	fmp, err := ParseFMPRawNews([]byte(`[
	  {
	    "publishedDate": "2026-06-18 11:45:00",
	    "title": "Nvidia extends gains",
	    "text": "Semiconductor momentum continued.",
	    "url": "https://example.test/nvda",
	    "site": "Example News"
	  }
	]`), fetchedAt)
	if err != nil {
		t.Fatalf("parse fmp: %v", err)
	}
	if len(alpha) != 1 || len(fmp) != 1 {
		t.Fatalf("alpha len = %d, fmp len = %d", len(alpha), len(fmp))
	}
	for _, item := range []RequestCreateRawNews{alpha[0], fmp[0]} {
		if item.Language != "en" || item.Quality != NewsQualityOK {
			t.Fatalf("language/quality = %s/%s", item.Language, item.Quality)
		}
		if item.SourceID == "" || item.DedupeKey == "" || item.URL == "" || item.PublishedAt.IsZero() {
			t.Fatalf("missing normalized fields: %+v", item)
		}
	}
	if alpha[0].Source != NewsSourceAlphaVantage || fmp[0].Source != NewsSourceFMP {
		t.Fatalf("sources = %s/%s", alpha[0].Source, fmp[0].Source)
	}
}

func TestRawNewsAdapterDedupeHashStable(t *testing.T) {
	body := []byte(`{"feed":[{"title":"Stable item","url":"https://example.test/stable","time_published":"20260618T113000","summary":"Same payload."}]}`)
	fetchedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	first, err := ParseAlphaVantageRawNews(body, fetchedAt)
	if err != nil {
		t.Fatalf("parse first: %v", err)
	}
	second, err := ParseAlphaVantageRawNews(body, fetchedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("parse second: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("parsed lengths = %d/%d", len(first), len(second))
	}
	if first[0].SourceID != second[0].SourceID || first[0].DedupeKey != second[0].DedupeKey {
		t.Fatalf("dedupe changed: first=%+v second=%+v", first[0], second[0])
	}
}

type newsRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn newsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
