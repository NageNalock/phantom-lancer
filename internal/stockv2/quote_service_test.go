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

func TestQuoteHotTablesUseSQLite(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 30, 10, 0, 0, 0, chinaMarketTZ)
	quote := StockV2QuoteLatest{
		Symbol: "000001", Market: "SZ", Name: "平安银行", LastPrice: 10,
		QuoteAt: now, FetchedAt: now, Source: QuoteSourceTencent, Status: QuoteStatusFresh,
	}
	if err := store.UpsertLatestQuote(ctx, quote); err != nil {
		t.Fatalf("upsert latest quote: %v", err)
	}
	if err := store.InsertQuoteSnapshot(ctx, StockV2QuoteSnapshot{StockV2QuoteLatest: quote, CollectedAt: now}); err != nil {
		t.Fatalf("insert quote snapshot: %v", err)
	}
	if err := store.UpsertMinuteBars(ctx, []StockV2MinuteBar{{
		Symbol: "000001", Market: "SZ", MinuteAt: now,
		Open: 10, High: 10, Low: 10, Close: 10, Source: QuoteSourceTencentMinute,
	}}); err != nil {
		t.Fatalf("upsert minute bars: %v", err)
	}

	for _, table := range []string{"stockv2_quotes_latest", "stockv2_quote_snapshots", "stockv2_minute_bars"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s in sqlite: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("sqlite %s count = %d, want 1", table, count)
		}
	}
}

func TestPruneIntradayQuotesRunsAtLowFrequency(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, nil)

	oldAt := time.Date(2026, 6, 20, 10, 0, 0, 0, chinaMarketTZ)
	now := time.Date(2026, 6, 30, 10, 0, 0, 0, chinaMarketTZ)
	quote := StockV2QuoteLatest{
		Symbol: "000001", Market: "SZ", Name: "平安银行", LastPrice: 10,
		QuoteAt: oldAt, FetchedAt: oldAt, Source: QuoteSourceTencent, Status: QuoteStatusFresh,
	}
	if err := store.InsertQuoteSnapshot(ctx, StockV2QuoteSnapshot{StockV2QuoteLatest: quote, CollectedAt: oldAt}); err != nil {
		t.Fatalf("insert old snapshot: %v", err)
	}
	svc.pruneIntradayQuotesIfDue(ctx, now)
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_quote_snapshots`).Scan(&count); err != nil {
		t.Fatalf("count snapshots after first prune: %v", err)
	}
	if count != 0 {
		t.Fatalf("snapshot count after first prune = %d, want 0", count)
	}

	if err := store.InsertQuoteSnapshot(ctx, StockV2QuoteSnapshot{StockV2QuoteLatest: quote, CollectedAt: oldAt}); err != nil {
		t.Fatalf("insert second old snapshot: %v", err)
	}
	svc.pruneIntradayQuotesIfDue(ctx, now.Add(time.Hour))
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_quote_snapshots`).Scan(&count); err != nil {
		t.Fatalf("count snapshots after skipped prune: %v", err)
	}
	if count != 1 {
		t.Fatalf("snapshot count after skipped prune = %d, want 1", count)
	}
}

func TestParseTencentQuoteTimeUsesChinaMarketTimezone(t *testing.T) {
	got := parseTencentQuoteTime("20260618145503", time.Time{})
	if got.Format(time.RFC3339) != "2026-06-18T14:55:03+08:00" {
		t.Fatalf("quote time = %s", got.Format(time.RFC3339))
	}
}

func TestParseEastmoneyQuoteResponseIncludesFundFlow(t *testing.T) {
	body := []byte(`{"rc":0,"data":{"diff":[{"f2":5017,"f3":101,"f5":23566245,"f6":11822154350.0,"f7":179,"f8":1030,"f10":157,"f12":"510300","f13":1,"f14":"沪深300ETF华泰柏瑞","f15":5055,"f16":4966,"f17":4973,"f18":4967,"f62":336248832.0,"f66":1730917376.0,"f72":-1394668544.0,"f78":-286519582.0,"f84":-49829596.0,"f124":1782367424,"f184":284}]}}`)
	quotes, err := parseEastmoneyLatestQuoteResponse(body, map[string]quoteSymbol{
		"1.510300": {Symbol: "510300", Market: "SH", EastmoneyID: "1.510300"},
	}, time.Date(2026, 6, 25, 14, 0, 0, 0, chinaMarketTZ))
	if err != nil {
		t.Fatalf("parse eastmoney: %v", err)
	}
	if len(quotes) != 1 {
		t.Fatalf("quote count = %d, want 1", len(quotes))
	}
	got := quotes[0]
	if got.LastPrice != 5.017 || got.PrevClose != 4.967 || got.PctChange != 1.01 {
		t.Fatalf("quote price fields = %+v", got)
	}
	if got.MainNetInflow != 336248832 || got.Source != QuoteSourceEastmoney {
		t.Fatalf("quote flow/source = %+v", got)
	}
}

func TestRefreshLatestQuotesUsesEastmoneyMinuteBars(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	tradeDate := previousTestTradingDate()
	minuteBody := `{"rc":0,"data":{"code":"000001","market":0,"name":"平安银行","klines":["` +
		tradeDate + ` 09:30,10.00,10.10,10.11,9.99,1000,10000,1.20,1.00,0.10,0.05","` +
		tradeDate + ` 09:31,10.10,10.20,10.22,10.08,1200,12240,2.20,2.00,0.20,0.06"]}}`
	latestBody := `{"rc":0,"data":{"diff":[{"f2":1020,"f3":200,"f4":20,"f5":2200,"f6":22240.0,"f7":220,"f8":60,"f10":120,"f12":"000001","f13":0,"f14":"平安银行","f15":1022,"f16":999,"f17":1000,"f18":1000,"f62":12345.0,"f66":7000.0,"f72":3000.0,"f78":2000.0,"f84":-1000.0,"f124":1782367424,"f184":56}]}}`
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/api/qt/ulist.np/get"):
			return stringResponse(http.StatusOK, latestBody), nil
		case strings.Contains(req.URL.Path, "/api/qt/stock/kline/get"):
			return stringResponse(http.StatusOK, minuteBody), nil
		default:
			return stringResponse(http.StatusBadGateway, "unexpected endpoint"), nil
		}
	})})

	result, err := svc.RefreshLatestQuotes(ctx, []string{"000001"}, "test")
	if err != nil {
		t.Fatalf("refresh latest quotes: %v", err)
	}
	if result.RefreshedCount != 1 || result.FailedCount != 0 {
		t.Fatalf("result = %+v, want one refreshed", result)
	}
	if len(result.Items) != 1 || result.Items[0].Source != QuoteSourceEastmoneyMinute || result.Items[0].LastPrice != 10.20 {
		t.Fatalf("projected quote = %+v", result.Items)
	}
	if result.Items[0].MainNetInflow != 12345 {
		t.Fatalf("projected quote did not preserve fund flow: %+v", result.Items[0])
	}

	bars, err := svc.ListMinuteBars(ctx, "000001", 5, 10)
	if err != nil {
		t.Fatalf("list minute bars: %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("minute bar count = %d, want 2: %+v", len(bars), bars)
	}
	if bars[1].Close != 10.20 || bars[1].Source != QuoteSourceEastmoneyMinute {
		t.Fatalf("minute bars = %+v", bars)
	}
}

func TestRefreshLatestQuotesFallsBackToTencentMinuteBars(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	tradeDate := previousTestTradingDate()
	tencentDate := strings.ReplaceAll(tradeDate, "-", "")
	minuteBody := `{"code":0,"data":{"sz000001":{"data":{"data":["0930 10.10 1000 10000","0931 10.20 1200 12040"],"date":"` + tencentDate + `"},"qt":{"sz000001":["1","平安银行"]}}}}`
	latestBody := `{"rc":0,"data":{"diff":[{"f2":1020,"f3":200,"f4":20,"f5":1200,"f6":12040.0,"f7":220,"f8":60,"f10":120,"f12":"000001","f13":0,"f14":"平安银行","f15":1022,"f16":999,"f17":1000,"f18":1000,"f62":12345.0,"f66":7000.0,"f72":3000.0,"f78":2000.0,"f84":-1000.0,"f124":1782367424,"f184":56}]}}`
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/api/qt/ulist.np/get"):
			return stringResponse(http.StatusOK, latestBody), nil
		case strings.Contains(req.URL.Path, "/api/qt/stock/kline/get"):
			return stringResponse(http.StatusBadGateway, "bad gateway"), nil
		case strings.Contains(req.URL.Path, "/appstock/app/minute/query"):
			return stringResponse(http.StatusOK, minuteBody), nil
		default:
			return stringResponse(http.StatusBadGateway, "unexpected endpoint"), nil
		}
	})})

	result, err := svc.RefreshLatestQuotes(ctx, []string{"000001"}, "test")
	if err != nil {
		t.Fatalf("refresh latest quotes: %v", err)
	}
	if result.RefreshedCount != 1 || result.FailedCount != 0 {
		t.Fatalf("result = %+v, want one refreshed", result)
	}
	if len(result.Items) != 1 || result.Items[0].Source != QuoteSourceTencentMinute || result.Items[0].LastPrice != 10.20 {
		t.Fatalf("projected quote = %+v", result.Items)
	}

	bars, err := svc.ListMinuteBars(ctx, "000001", 5, 10)
	if err != nil {
		t.Fatalf("list minute bars: %v", err)
	}
	if len(bars) != 2 || bars[1].Volume != 200 || bars[1].Source != QuoteSourceTencentMinute {
		t.Fatalf("minute bars = %+v", bars)
	}
}

func TestRefreshLatestQuotesIgnoresNonTradingLatestMinute(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	tradeDate := previousTestTradingDate()
	seedAt, err := time.ParseInLocation("2006-01-02 15:04", tradeDate+" 16:14", chinaMarketTZ)
	if err != nil {
		t.Fatalf("parse seed time: %v", err)
	}
	if err := store.UpsertMinuteBars(ctx, []StockV2MinuteBar{{
		Symbol: "000001", Market: "SZ", MinuteAt: seedAt,
		Open: 10.30, High: 10.30, Low: 10.30, Close: 10.30,
		Source: QuoteSourceTencentMinute, SnapshotCount: 1,
	}}); err != nil {
		t.Fatalf("seed non-trading minute: %v", err)
	}

	tencentDate := strings.ReplaceAll(tradeDate, "-", "")
	minuteBody := `{"code":0,"data":{"sz000001":{"data":{"data":["0930 10.10 1000 10000","0931 10.20 1200 12040"],"date":"` + tencentDate + `"},"qt":{"sz000001":["1","平安银行"]}}}}`
	latestBody := `{"rc":0,"data":{"diff":[{"f2":1020,"f3":200,"f4":20,"f5":1200,"f6":12040.0,"f7":220,"f8":60,"f10":120,"f12":"000001","f13":0,"f14":"平安银行","f15":1022,"f16":999,"f17":1000,"f18":1000,"f62":12345.0,"f66":7000.0,"f72":3000.0,"f78":2000.0,"f84":-1000.0,"f124":1782367424,"f184":56}]}}`
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/api/qt/ulist.np/get"):
			return stringResponse(http.StatusOK, latestBody), nil
		case strings.Contains(req.URL.Path, "/appstock/app/minute/query"):
			return stringResponse(http.StatusOK, minuteBody), nil
		default:
			return stringResponse(http.StatusBadGateway, "unexpected endpoint"), nil
		}
	})})

	result, err := svc.RefreshLatestQuotes(ctx, []string{"000001"}, "test")
	if err != nil {
		t.Fatalf("refresh latest quotes: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Source != QuoteSourceTencentMinute {
		t.Fatalf("projected quote = %+v", result.Items)
	}
	bars, err := svc.ListMinuteBars(ctx, "000001", 5, 10)
	if err != nil {
		t.Fatalf("list minute bars: %v", err)
	}
	if len(bars) != 2 || bars[0].MinuteAt.In(chinaMarketTZ).Hour() != 9 {
		t.Fatalf("minute bars = %+v, want regular trading minutes only", bars)
	}
}

func TestRefreshLatestQuotesRecordsMinuteDegradedWarning(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/api/qt/ulist.np/get"):
			return stringResponse(http.StatusBadGateway, "bad gateway"), nil
		case strings.Contains(req.URL.Host, "qt.gtimg.cn"):
			return stringResponse(http.StatusOK, tencentQuoteLine("sz000001", "平安银行", "000001", "10.50", "10.00")), nil
		case strings.Contains(req.URL.Path, "/appstock/app/minute/query"), strings.Contains(req.URL.Path, "/api/qt/stock/kline/get"):
			return stringResponse(http.StatusBadGateway, "minute source down"), nil
		default:
			return stringResponse(http.StatusBadGateway, "unexpected endpoint"), nil
		}
	})})

	result, err := svc.RefreshLatestQuotes(ctx, []string{"000001"}, "instrument_detail")
	if err != nil {
		t.Fatalf("refresh latest quotes: %v", err)
	}
	if result.RefreshedCount != 1 || result.FailedCount != 0 {
		t.Fatalf("result = %+v, want latest quote fallback success", result)
	}
	if len(result.Items) != 1 || result.Items[0].Source != QuoteSourceTencent {
		t.Fatalf("fallback quote = %+v", result.Items)
	}

	_, statuses, err := svc.GetLatestQuoteRefreshState(ctx, 10)
	if err != nil {
		t.Fatalf("get refresh state: %v", err)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0].ErrorMessage, "minute sync degraded") {
		t.Fatalf("status = %+v, want minute degraded warning", statuses)
	}
}

func previousTestTradingDate() string {
	d := time.Now().In(chinaMarketTZ).AddDate(0, 0, -1)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d.Format("2006-01-02")
}

func TestBuildMinuteBarsFromSnapshotsUsesCumulativeDeltas(t *testing.T) {
	base := time.Date(2026, 6, 25, 10, 0, 10, 0, chinaMarketTZ)
	snapshots := []StockV2QuoteSnapshot{
		{StockV2QuoteLatest: StockV2QuoteLatest{Symbol: "600000", Market: "SH", LastPrice: 8.80, PrevClose: 8.70, Volume: 1000, Amount: 8800, MainNetInflow: 100, QuoteAt: base, Source: QuoteSourceEastmoney}, CollectedAt: base},
		{StockV2QuoteLatest: StockV2QuoteLatest{Symbol: "600000", Market: "SH", LastPrice: 8.84, PrevClose: 8.70, Volume: 1200, Amount: 10608, MainNetInflow: 130, QuoteAt: base.Add(20 * time.Second), Source: QuoteSourceEastmoney}, CollectedAt: base.Add(20 * time.Second)},
		{StockV2QuoteLatest: StockV2QuoteLatest{Symbol: "600000", Market: "SH", LastPrice: 8.86, PrevClose: 8.70, Volume: 1500, Amount: 13290, MainNetInflow: 170, QuoteAt: base.Add(1 * time.Minute), Source: QuoteSourceEastmoney}, CollectedAt: base.Add(1 * time.Minute)},
		{StockV2QuoteLatest: StockV2QuoteLatest{Symbol: "600000", Market: "SH", LastPrice: 8.86, PrevClose: 8.70, Volume: 1500, Amount: 13290, MainNetInflow: 170, QuoteAt: time.Date(2026, 6, 25, 15, 29, 0, 0, chinaMarketTZ), Source: QuoteSourceEastmoney}, CollectedAt: time.Date(2026, 6, 25, 15, 29, 0, 0, chinaMarketTZ)},
	}
	bars := buildMinuteBarsFromSnapshots(snapshots)
	if len(bars) != 2 {
		t.Fatalf("minute bars = %d, want 2", len(bars))
	}
	if bars[0].Open != 8.80 || bars[0].Close != 8.84 || bars[0].Volume != 200 || bars[0].MainNetInflow != 30 {
		t.Fatalf("first minute bar = %+v", bars[0])
	}
	if bars[1].Open != 8.86 || bars[1].Volume != 300 || bars[1].MainNetInflow != 40 {
		t.Fatalf("second minute bar = %+v", bars[1])
	}
}
