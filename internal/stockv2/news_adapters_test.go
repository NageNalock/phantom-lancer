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
