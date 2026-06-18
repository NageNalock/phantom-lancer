package stockv2

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshLatestQuotesKeepsOldQuoteOnTencentFailure(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertInstrument(ctx, StockV2Instrument{
		ID:     "inst-000001",
		Symbol: "000001",
		Market: "SZ",
		Name:   "平安银行",
		Status: "active",
	}); err != nil {
		t.Fatalf("seed instrument: %v", err)
	}
	beforeCount, err := store.CountInstruments(ctx)
	if err != nil {
		t.Fatalf("count instruments before: %v", err)
	}

	status := http.StatusOK
	body := tencentQuoteLine("sz000001", "平安银行", "000001", "10.50", "10.00")
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(status, body), nil
	})})

	first, err := svc.RefreshLatestQuotes(ctx, []string{"000001"}, "test")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first.RefreshedCount != 1 || first.FailedCount != 0 {
		t.Fatalf("first result = %+v, want one refreshed and no failures", first)
	}
	if len(first.Items) != 1 || first.Items[0].LastPrice != 10.50 || first.Items[0].Status != QuoteStatusFresh {
		t.Fatalf("first items = %+v", first.Items)
	}

	status = http.StatusBadGateway
	body = "bad gateway"
	second, err := svc.RefreshLatestQuotes(ctx, []string{"000001"}, "test")
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.RefreshedCount != 0 || second.FailedCount != 1 {
		t.Fatalf("second result = %+v, want failed refresh", second)
	}
	if len(second.Items) != 1 || second.Items[0].LastPrice != 10.50 || second.Items[0].Status != QuoteStatusFailed {
		t.Fatalf("second items = %+v, want old price marked failed", second.Items)
	}

	quotes, err := svc.GetLatestQuotes(ctx, []string{"000001"})
	if err != nil {
		t.Fatalf("get latest quotes: %v", err)
	}
	if len(quotes) != 1 || quotes[0].LastPrice != 10.50 || quotes[0].Status != QuoteStatusFailed {
		t.Fatalf("stored quotes = %+v, want old price retained as failed", quotes)
	}
	afterCount, err := store.CountInstruments(ctx)
	if err != nil {
		t.Fatalf("count instruments after: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("instrument count changed from %d to %d", beforeCount, afterCount)
	}
}

func tencentQuoteLine(code, name, symbol, last, prevClose string) string {
	fields := make([]string, 38)
	fields[0] = "51"
	fields[1] = name
	fields[2] = symbol
	fields[3] = last
	fields[4] = prevClose
	fields[5] = "10.10"
	fields[6] = "1000"
	fields[30] = "20260618145503"
	fields[32] = "5.00"
	fields[33] = "10.80"
	fields[34] = "10.00"
	fields[36] = "1000"
	fields[37] = "10500"
	return `v_` + code + `="` + strings.Join(fields, "~") + `";`
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stringResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    &http.Request{Method: http.MethodGet},
	}
}

func TestParseTencentQuoteTimeUsesChinaMarketTimezone(t *testing.T) {
	got := parseTencentQuoteTime("20260618145503", time.Time{})
	if got.Format(time.RFC3339) != "2026-06-18T14:55:03+08:00" {
		t.Fatalf("quote time = %s", got.Format(time.RFC3339))
	}
}
