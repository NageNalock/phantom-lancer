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
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		FinancialJuiceEnabled:     &enabled,
		FinancialJuiceCookieInput: &input,
	})
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if !updated.FinancialJuiceEnabled || !updated.FinancialJuiceCookieSet {
		t.Fatalf("updated settings = %+v", updated)
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
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		FinancialJuiceEnabled:     &enabled,
		FinancialJuiceCookieInput: &input,
	})
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if !updated.FinancialJuiceEnabled || !updated.FinancialJuiceCookieSet {
		t.Fatalf("updated settings = %+v", updated)
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

func TestJin10CurlParseAndSettingsSave(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.httpClient = emptyNewsHTTPClient()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	enabled := true
	input := `curl 'https://www.jin10.com/tv/index/list?app=jin10' \
  -H 'accept: */*' \
  -H 'x-app-id: app-placeholder' \
  -H 'x-version: 2.1' \
  -b 'did=placeholder-device; x-token=placeholder-token'`
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		Jin10Enabled:   &enabled,
		Jin10CurlInput: &input,
	})
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if !updated.Jin10Enabled || !updated.Jin10EndpointSet || !updated.Jin10CookieSet {
		t.Fatalf("updated settings = %+v", updated)
	}
	state, ok, err := svc.GetNewsSourceState(ctx, NewsSourceJin10)
	if err != nil {
		t.Fatalf("get jin10 source state: %v", err)
	}
	if !ok || !state.Enabled || state.NextRunAt.IsZero() {
		t.Fatalf("jin10 state = %+v, ok=%v; want enabled state with next run", state, ok)
	}
	stored, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if stored.Jin10Endpoint != "https://www.jin10.com/tv/index/list?app=jin10" {
		t.Fatalf("stored endpoint = %q", stored.Jin10Endpoint)
	}
	if stored.Jin10Cookie != "did=placeholder-device; x-token=placeholder-token" {
		t.Fatalf("stored cookie = %q", stored.Jin10Cookie)
	}
	if stored.Jin10XAppID != "app-placeholder" || stored.Jin10XVersion != "2.1" {
		t.Fatalf("stored headers = %q/%q", stored.Jin10XAppID, stored.Jin10XVersion)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	for _, secretFragment := range []string{"placeholder-token", "placeholder-device", "app-placeholder"} {
		if strings.Contains(string(encoded), secretFragment) {
			t.Fatalf("settings JSON leaked private config %q: %s", secretFragment, encoded)
		}
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

func TestJin10FetchUsesCurlConfigAndParsesNestedPayload(t *testing.T) {
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
	adapter := &Jin10NewsAdapter{
		endpoint:   "https://www.jin10.com/tv/index/list?app=jin10",
		cookie:     "did=placeholder-device; x-token=placeholder-token",
		xAppID:     "app-placeholder",
		xVersion:   "2.1",
		httpClient: client,
	}
	result, err := adapter.FetchSince(context.Background(), NewsSourceCursor{Cursor: "ignored"})
	if err != nil {
		t.Fatalf("fetch jin10: %v", err)
	}
	if sawURL != "https://www.jin10.com/tv/index/list?app=jin10" {
		t.Fatalf("url = %q", sawURL)
	}
	if sawCookie != "did=placeholder-device; x-token=placeholder-token" || sawAppID != "app-placeholder" || sawVersion != "2.1" {
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
}

func TestFinancialJuiceAdapterDisabled(t *testing.T) {
	adapter := NewFinancialJuiceRawNewsAdapter(FinancialJuiceAdapterConfig{})
	if _, err := adapter.FetchRawNews(context.Background()); !errors.Is(err, ErrNewsAdapterDisabled) {
		t.Fatalf("FetchRawNews() err = %v, want ErrNewsAdapterDisabled", err)
	}
}

func TestFinancialJuiceMockFetchToRawNews(t *testing.T) {
	var sawCookie string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawCookie = r.Header.Get("Cookie")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"d":"{\"News\":[{\"NewsID\":12345,\"Headline\":\"Fed officials discuss rate path\",\"Text\":\"Policy comments moved US yields.\",\"NewsTime\":\"2026-06-18T10:30:00Z\",\"URL\":\"https://example.test/fed\"}]}"}`)),
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
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	got := items[0]
	if got.Source != NewsSourceFinancialJuice || got.SourceID != "12345" || got.Language != "en" || got.Quality != NewsQualityOK {
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
	if sawCookie != "" {
		t.Fatalf("cookie header = %q, want empty cookie", sawCookie)
	}
}

func TestFinancialJuiceRunsThroughNewsPipeline(t *testing.T) {
	var sawCookie string
	client := &http.Client{Transport: newsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawCookie = r.Header.Get("Cookie")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"d":"{\"News\":[{\"NewsID\":12345,\"Headline\":\"Fed officials discuss rate path\",\"Text\":\"Policy comments moved US yields.\",\"NewsTime\":\"2026-06-18T10:30:00Z\",\"URL\":\"https://example.test/fed\"}]}"}`)),
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
	if _, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		FinancialJuiceEnabled:     &enabled,
		FinancialJuiceCookieInput: &cookie,
	}); err != nil {
		t.Fatalf("save settings: %v", err)
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
