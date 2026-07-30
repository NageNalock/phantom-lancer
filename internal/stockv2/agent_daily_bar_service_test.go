package stockv2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildDailyBarsContextRefreshesCompletedQFQOnceForConcurrentIntradayReads(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 30, 0, 0, chinaMarketTZ)
	var calls atomic.Int32
	var requestedEnd atomic.Value
	var requestedCount atomic.Value
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		parts := strings.Split(req.URL.Query().Get("param"), ",")
		if len(parts) >= 5 {
			requestedEnd.Store(parts[3])
			requestedCount.Store(parts[4])
		}
		return dailyBarsTestResponse(req, `{"code":0,"msg":"","data":{"sz000977":{"qfqday":[
			["2026-07-28","60","61","62","59","1000"],
			["2026-07-29","61","63","64","60","1200"]
		]}}}`), nil
	})}
	svc, cleanup := newAgentDailyBarsTestService(t, client)
	defer cleanup()
	ctx := context.Background()
	seedAgentDailyBarsQuote(t, svc.store, "000977", now)
	seedAgentDailyBar(t, svc.store, "000977", "2026-07-28", now.AddDate(0, 0, -2))

	results := make([]*DailyBarsContext, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = svc.buildDailyBarsContextAt(ctx, "000977", "", now)
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("daily bar requests = %d, want 1", got)
	}
	if got, _ := requestedEnd.Load().(string); got != "2026-07-29" {
		t.Fatalf("requested end = %q, want previous calendar day", got)
	}
	if got, _ := requestedCount.Load().(string); got != "365" {
		t.Fatalf("requested count = %q, want provider-safe yearly limit", got)
	}
	for _, got := range results {
		if got.Adjusted != DailyBarAdjustedQFQ || got.CoverageStatus != dailyBarsCoverageFreshPreviousClose {
			t.Fatalf("daily bars context = %+v", got)
		}
		if !got.CurrentSessionIncomplete || got.LatestTradeDate != "2026-07-29" {
			t.Fatalf("intraday coverage = %+v", got)
		}
	}
}

func TestBuildDailyBarsContextRefreshesAgainAfterCloseForCurrentSession(t *testing.T) {
	intraday := time.Date(2026, 7, 30, 10, 30, 0, 0, chinaMarketTZ)
	postClose := time.Date(2026, 7, 30, 15, 20, 0, 0, chinaMarketTZ)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call == 1 {
			return dailyBarsTestResponse(req, `{"code":0,"msg":"","data":{"sz000977":{"qfqday":[
				["2026-07-29","61","63","64","60","1200"]
			]}}}`), nil
		}
		return dailyBarsTestResponse(req, `{"code":0,"msg":"","data":{"sz000977":{"qfqday":[
			["2026-07-29","61","63","64","60","1200"],
			["2026-07-30","64","69","70","63","1800"]
		]}}}`), nil
	})}
	svc, cleanup := newAgentDailyBarsTestService(t, client)
	defer cleanup()
	ctx := context.Background()
	seedAgentDailyBarsQuote(t, svc.store, "000977", intraday)

	first := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, intraday)
	seedAgentDailyBarsQuote(t, svc.store, "000977", postClose)
	second := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, postClose)

	if calls.Load() != 2 {
		t.Fatalf("daily bar requests = %d, want intraday and post-close refresh", calls.Load())
	}
	if first.CoverageStatus != dailyBarsCoverageFreshPreviousClose || first.LatestTradeDate != "2026-07-29" {
		t.Fatalf("intraday context = %+v", first)
	}
	if second.CoverageStatus != dailyBarsCoverageFresh || second.LatestTradeDate != "2026-07-30" {
		t.Fatalf("post-close context = %+v", second)
	}
	if second.CurrentSessionIncomplete {
		t.Fatalf("post-close currentSessionIncomplete = true")
	}
}

func TestBuildDailyBarsContextKeepsExistingBarsAndCoolsDownAfterRefreshFailure(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 30, 0, 0, chinaMarketTZ)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("rate limited")),
			Request:    req,
		}, nil
	})}
	svc, cleanup := newAgentDailyBarsTestService(t, client)
	defer cleanup()
	ctx := context.Background()
	seedAgentDailyBarsQuote(t, svc.store, "000977", now)
	seedAgentDailyBar(t, svc.store, "000977", "2026-07-29", now.AddDate(0, 0, -1))

	first := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, now)
	second := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, now.Add(time.Minute))

	if calls.Load() != 1 {
		t.Fatalf("daily bar requests = %d, want one request during cooldown", calls.Load())
	}
	for _, got := range []*DailyBarsContext{first, second} {
		if got.CoverageStatus != dailyBarsCoverageRefreshFailed || got.Count == 0 || got.RefreshError == "" {
			t.Fatalf("degraded context = %+v", got)
		}
	}
	if !first.RefreshAttempted || second.RefreshAttempted {
		t.Fatalf("refresh attempted flags = first:%v second:%v", first.RefreshAttempted, second.RefreshAttempted)
	}
}

func TestBuildDailyBarsContextMarksPostCloseSourceLagAndRetriesAfterCooldown(t *testing.T) {
	now := time.Date(2026, 7, 30, 15, 20, 0, 0, chinaMarketTZ)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return dailyBarsTestResponse(req, `{"code":0,"msg":"","data":{"sz000977":{"qfqday":[
			["2026-07-29","61","63","64","60","1200"]
		]}}}`), nil
	})}
	svc, cleanup := newAgentDailyBarsTestService(t, client)
	defer cleanup()
	ctx := context.Background()
	seedAgentDailyBarsQuote(t, svc.store, "000977", now)

	first := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, now)
	second := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, now.Add(time.Minute))
	third := svc.buildDailyBarsContextAt(ctx, "000977", DailyBarAdjustedQFQ, now.Add(agentDailyBarsRetryCooldown+time.Second))

	if calls.Load() != 2 {
		t.Fatalf("daily bar requests = %d, want retry only after cooldown", calls.Load())
	}
	for _, got := range []*DailyBarsContext{first, second, third} {
		if got.CoverageStatus != dailyBarsCoverageSourceLagging || got.LatestTradeDate != "2026-07-29" {
			t.Fatalf("source-lag context = %+v", got)
		}
	}
	if second.RefreshAttempted || !third.RefreshAttempted {
		t.Fatalf("refresh attempted flags = second:%v third:%v", second.RefreshAttempted, third.RefreshAttempted)
	}
}

func TestMCPDailyBarsSummaryDefaultsToQFQAndReturnsCoverage(t *testing.T) {
	svc, cleanup := newAgentDailyBarsTestService(t, nil)
	defer cleanup()
	now := time.Now().In(chinaMarketTZ)
	seedAgentDailyBar(t, svc.store, "000977", now.AddDate(0, 0, -1).Format("2006-01-02"), now)

	result, mcpErr := svc.mcpGetDailyBarsSummary(json.RawMessage(`{"symbol":"000977"}`))
	if mcpErr != nil {
		t.Fatalf("mcp daily bars summary: %v", mcpErr)
	}
	content := result.(map[string]any)["content"].([]map[string]any)
	var body map[string]any
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &body); err != nil {
		t.Fatalf("decode MCP content: %v", err)
	}
	if body["adjusted"] != DailyBarAdjustedQFQ {
		t.Fatalf("adjusted = %v, want qfq", body["adjusted"])
	}
	if body["coverageStatus"] != dailyBarsCoverageFreshLatest {
		t.Fatalf("coverageStatus = %v, want fresh latest available", body["coverageStatus"])
	}
}

func newAgentDailyBarsTestService(t *testing.T, client *http.Client) (*Service, func()) {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, client)
	return svc, func() {
		svc.StopBackground()
		_ = store.Close()
	}
}

func seedAgentDailyBarsQuote(t *testing.T, store *Store, symbol string, quoteAt time.Time) {
	t.Helper()
	if err := store.UpsertLatestQuote(context.Background(), StockV2QuoteLatest{
		Symbol:    symbol,
		Market:    "SZ",
		Name:      "测试标的",
		LastPrice: 69,
		PrevClose: 63,
		QuoteAt:   quoteAt,
		FetchedAt: quoteAt,
		Source:    QuoteSourceTencent,
		Status:    QuoteStatusFresh,
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
}

func seedAgentDailyBar(t *testing.T, store *Store, symbol, tradeDate string, fetchedAt time.Time) {
	t.Helper()
	if err := store.UpsertDailyBars(context.Background(), []StockV2DailyBar{{
		Symbol:    symbol,
		Market:    "SZ",
		TradeDate: tradeDate,
		Open:      60,
		High:      64,
		Low:       59,
		Close:     63,
		Volume:    1000,
		Adjusted:  DailyBarAdjustedQFQ,
		Source:    QuoteSourceTencent,
		FetchedAt: fetchedAt,
		Quality:   DailyBarQualityOK,
	}}); err != nil {
		t.Fatalf("upsert daily bar: %v", err)
	}
}

func dailyBarsTestResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
