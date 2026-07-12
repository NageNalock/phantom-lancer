package stockv2

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClosingQuoteDailyBarPreservesPresentZeroFields(t *testing.T) {
	tradeDate := "2026-07-10"
	quote := StockV2QuoteLatest{
		Symbol:               "600000",
		Market:               "SH",
		LastPrice:            10,
		PrevClose:            10,
		OpenPrice:            10,
		HighPrice:            10,
		LowPrice:             10,
		Volume:               100,
		QuoteAt:              time.Date(2026, 7, 10, 17, 0, 0, 0, chinaMarketTZ),
		FetchedAt:            time.Date(2026, 7, 10, 17, 1, 0, 0, chinaMarketTZ),
		Source:               QuoteSourceEastmoney,
		amountPresent:        true,
		turnoverRatePresent:  true,
		netInflowPresent:     true,
		mainNetInflowPresent: true,
	}
	bar, ok := closingQuoteDailyBar(quote, tradeDate)
	if !ok {
		t.Fatal("present zero-valued quote was rejected")
	}
	if !bar.AmountPresent || !bar.TurnoverRatePresent || !bar.NetInflowPresent || !bar.MainNetInflowPresent {
		t.Fatalf("presence flags were lost: %+v", bar)
	}
	if bar.Amount != 0 || bar.TurnoverRate != 0 || bar.NetInflow != 0 || bar.MainNetInflow != 0 || bar.Quality != DailyBarQualityOK {
		t.Fatalf("legitimate zero fields changed: %+v", bar)
	}
}

func TestPrefillClosingDailyBarsBatchUsesEightySymbolRequests(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	const tradeDate = "2026-07-10"
	instruments := make([]StockV2Instrument, 0, 81)
	for i := 0; i < 81; i++ {
		instruments = append(instruments, StockV2Instrument{Symbol: fmt.Sprintf("6%05d", i), Market: "SH", InstrumentType: InstrumentTypeStock})
	}
	var mu sync.Mutex
	requests := map[string]int{}
	buildTencentBody := func(codes []string) string {
		var body strings.Builder
		for _, code := range codes {
			fields := make([]string, 39)
			fields[1] = code
			fields[2] = strings.TrimPrefix(code, "sh")
			fields[3] = "10.00"
			fields[4] = "10.00"
			fields[5] = "10.00"
			fields[30] = "20260710170000"
			fields[32] = "0"
			fields[33] = "10.00"
			fields[34] = "10.00"
			fields[36] = "100"
			fields[37] = "0"
			fields[38] = "0"
			body.WriteString(`v_` + code + `="` + strings.Join(fields, "~") + `";` + "\n")
		}
		return body.String()
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requests[req.URL.Host]++
		mu.Unlock()
		switch req.URL.Host {
		case "push2his.eastmoney.com":
			return stringResponse(http.StatusBadGateway, "unavailable"), nil
		case "qt.gtimg.cn":
			codes := strings.Split(strings.TrimPrefix(req.URL.Path, "/q="), ",")
			return stringResponse(http.StatusOK, buildTencentBody(codes)), nil
		case "web.ifzq.gtimg.cn":
			return stringResponse(http.StatusOK, tencentIndexCalendarBody("2026-07-09", "2026-07-10")), nil
		default:
			t.Fatalf("unexpected per-symbol endpoint %q", req.URL.Host)
			return nil, nil
		}
	})}
	parsed, err := parseTencentLatestQuoteResponse([]byte(buildTencentBody([]string{"sh600000"})), map[string]quoteSymbol{
		"sh600000": {Symbol: "600000", Market: "SH", TencentCode: "sh600000"},
	}, time.Now())
	if err != nil || len(parsed) != 1 {
		t.Fatalf("parse sample Tencent closing quote: quotes=%+v err=%v", parsed, err)
	}
	if _, ok := closingQuoteDailyBar(parsed[0], tradeDate); !ok {
		t.Fatalf("sample Tencent closing quote was rejected: %+v", parsed[0])
	}
	svc := NewService(store, nil, client)
	batchCtx, result, err := svc.prefillClosingDailyBarsBatch(ctx, instruments, tradeDate)
	if err != nil {
		t.Fatalf("prefill closing bars: %v", err)
	}
	if result.Attempted != 81 || result.Upserted != 81 || result.Failed != 0 {
		t.Fatalf("result = %+v requests=%v", result, requests)
	}
	if instruments[0].Name != "sh600000" {
		t.Fatalf("instrument name was not refreshed from batch quote: %+v", instruments[0])
	}
	mu.Lock()
	eastmoneyCalls := requests["push2his.eastmoney.com"]
	tencentCalls := requests["qt.gtimg.cn"]
	calendarCalls := requests["web.ifzq.gtimg.cn"]
	mu.Unlock()
	if eastmoneyCalls != 1 || tencentCalls != 2 || calendarCalls != 1 {
		t.Fatalf("batch calls = eastmoney:%d tencent:%d calendar:%d", eastmoneyCalls, tencentCalls, calendarCalls)
	}

	bar, hasBar, attempted := dailyBarQuoteBatchStatus(batchCtx, "600000", tradeDate)
	if !attempted || !hasBar || bar.Source != dailyBarSourceTencentClosingQuote {
		t.Fatalf("batch context status = attempted:%v hasBar:%v bar:%+v", attempted, hasBar, bar)
	}
	if !bar.AmountPresent || !bar.TurnoverRatePresent || bar.Amount != 0 || bar.TurnoverRate != 0 {
		t.Fatalf("Tencent present zero fields = %+v", bar)
	}
	stored, err := store.GetDailyBars(ctx, "600000", DailyBarAdjustedNone, tradeDate, tradeDate, 0)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored bars = %+v err=%v", stored, err)
	}
	if !stored[0].AmountPresent || !stored[0].TurnoverRatePresent {
		t.Fatalf("stored presence flags = %+v", stored[0])
	}
	if stored[0].NetInflowPresent || stored[0].MainNetInflowPresent {
		t.Fatalf("Tencent fallback falsely claimed unavailable fund-flow fields: %+v", stored[0])
	}
}

func TestBatchQuoteFailureDoesNotRetryCurrentDayPerSymbol(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.URL.Host {
		case "push2his.eastmoney.com", "qt.gtimg.cn", "web.ifzq.gtimg.cn":
			return stringResponse(http.StatusBadGateway, "unavailable"), nil
		default:
			t.Fatalf("unexpected per-symbol retry endpoint %q", req.URL.Host)
			return nil, nil
		}
	})}
	svc := NewService(store, nil, client)
	inst := StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock}
	batchCtx, result, err := svc.prefillClosingDailyBarsBatch(ctx, []StockV2Instrument{inst}, "2026-07-10")
	if err != nil {
		t.Fatalf("prefill: %v", err)
	}
	if result.Failed != 1 || requests != 3 {
		t.Fatalf("batch result=%+v requests=%d", result, requests)
	}
	if message := dailyBarQuoteBatchFailure(batchCtx, inst.Symbol); message == "" {
		t.Fatal("batch provider failure was not attached to the affected symbol")
	}
	fetched, err := svc.ensureOneSymbol(batchCtx, inst, "2026-07-10", "2026-07-10", DailyBarAdjustedNone)
	if err != nil || fetched != 0 {
		t.Fatalf("ensure after batch failure = fetched:%d err:%v", fetched, err)
	}
	if requests != 3 {
		t.Fatalf("current-day miss triggered %d requests, want two batch attempts plus one calendar anchor", requests)
	}
}

func TestPrefillClosingDailyBarsBatchHonorsPersistedCalendarRetryBackoff(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetAssetMaintenanceCursor(ctx, dailyBarReferenceCalendarRetryCursor, "2026-07-10"); err != nil {
		t.Fatalf("set calendar retry cursor: %v", err)
	}

	fields := make([]string, 39)
	fields[1] = "浦发银行"
	fields[2] = "600000"
	fields[3], fields[4], fields[5] = "10", "10", "10"
	fields[30] = "20260710170000"
	fields[33], fields[34], fields[36], fields[37], fields[38] = "10", "10", "100", "1", "0"
	tencentBody := `v_sh600000="` + strings.Join(fields, "~") + `";`
	calendarRequests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "push2his.eastmoney.com":
			return stringResponse(http.StatusBadGateway, "unavailable"), nil
		case "qt.gtimg.cn":
			return stringResponse(http.StatusOK, tencentBody), nil
		case "web.ifzq.gtimg.cn":
			calendarRequests++
			return stringResponse(http.StatusOK, tencentIndexCalendarBody("2026-07-10")), nil
		default:
			t.Fatalf("unexpected endpoint %q", req.URL.Host)
			return nil, nil
		}
	})}
	svc := NewService(store, nil, client)
	_, result, err := svc.prefillClosingDailyBarsBatch(ctx, []StockV2Instrument{{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
	}}, "2026-07-10")
	if err != nil {
		t.Fatalf("prefill during calendar backoff: %v", err)
	}
	if result.Upserted != 1 || calendarRequests != 0 {
		t.Fatalf("result=%+v calendar requests=%d, want quote upsert without duplicate index request", result, calendarRequests)
	}
}

func TestBatchQuoteFailureKeepsHistoricalBootstrapRange(t *testing.T) {
	const symbol = "600000"
	const tradeDate = "2026-07-10"
	ctx := context.WithValue(context.Background(), dailyBarQuoteBatchContextKey{}, dailyBarQuoteBatchState{
		tradeDate:   tradeDate,
		probeFailed: true,
		attempted:   map[string]struct{}{symbol: {}},
	})
	ranges := excludeBatchClosingQuoteRetry(ctx, symbol, tradeDate, []dailyBarMissingRange{{
		Start: "2025-07-10",
		End:   tradeDate,
	}})
	if len(ranges) != 1 || ranges[0].Start != "2025-07-10" || ranges[0].End != "2026-07-09" {
		t.Fatalf("historical ranges = %+v, want bootstrap range through prior day", ranges)
	}
	currentOnly := excludeBatchClosingQuoteRetry(ctx, symbol, tradeDate, []dailyBarMissingRange{{
		Start: tradeDate,
		End:   tradeDate,
	}})
	if len(currentOnly) != 0 {
		t.Fatalf("current-day range = %+v, want no per-symbol retry", currentOnly)
	}
}

func TestClosingDateProbeAvoidsHolidayFullMarketAndPerSymbolRequests(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	instruments := make([]StockV2Instrument, 0, 81)
	seedBars := make([]StockV2DailyBar, 0, 81)
	for i := 0; i < 81; i++ {
		symbol := fmt.Sprintf("6%05d", i)
		instruments = append(instruments, StockV2Instrument{Symbol: symbol, Market: "SH", InstrumentType: InstrumentTypeStock})
		seedBars = append(seedBars, StockV2DailyBar{Symbol: symbol, Market: "SH", TradeDate: "2026-07-09", Open: 10, High: 10, Low: 10, Close: 10, Volume: 100, AmountPresent: true, TurnoverRatePresent: true, NetInflowPresent: true, MainNetInflowPresent: true, Adjusted: DailyBarAdjustedNone, Source: "seed", FetchedAt: time.Now(), Quality: DailyBarQualityOK})
	}
	if err := store.UpsertDailyBars(ctx, seedBars); err != nil {
		t.Fatalf("seed covered closing date: %v", err)
	}

	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.URL.Host {
		case "push2his.eastmoney.com":
			return stringResponse(http.StatusBadGateway, "unavailable"), nil
		case "qt.gtimg.cn":
			codes := strings.Split(strings.TrimPrefix(req.URL.Path, "/q="), ",")
			var body strings.Builder
			for _, code := range codes {
				fields := make([]string, 39)
				fields[2] = strings.TrimPrefix(code, "sh")
				fields[3], fields[4], fields[5] = "10", "10", "10"
				fields[30] = "20260709170000"
				fields[33], fields[34], fields[36], fields[37], fields[38] = "10", "10", "100", "1", "0"
				body.WriteString(`v_` + code + `="` + strings.Join(fields, "~") + `";` + "\n")
			}
			return stringResponse(http.StatusOK, body.String()), nil
		case "web.ifzq.gtimg.cn":
			return stringResponse(http.StatusOK, tencentIndexCalendarBody("2026-07-08", "2026-07-09")), nil
		default:
			t.Fatalf("unexpected per-symbol endpoint %q", req.URL.Host)
			return nil, nil
		}
	})}
	svc := NewService(store, nil, client)
	batchCtx, result, err := svc.prefillClosingDailyBarsBatch(ctx, instruments, "2026-07-10")
	if err != nil {
		t.Fatalf("holiday probe: %v", err)
	}
	if target := dailyBarBatchTargetDate(batchCtx, "2026-07-10"); target != "2026-07-09" {
		t.Fatalf("target trade date = %q, want observed 2026-07-09", target)
	}
	if requests != 3 || result.Upserted != 0 || result.Failed != 0 {
		t.Fatalf("holiday probe result=%+v requests=%d, want one batch, one fallback and one calendar anchor", result, requests)
	}
	fetched, err := svc.ensureOneSymbol(batchCtx, instruments[80], "2026-07-09", "2026-07-10", DailyBarAdjustedNone)
	if err != nil || fetched != 0 || requests != 3 {
		t.Fatalf("holiday ensure = fetched:%d requests:%d err:%v", fetched, requests, err)
	}
}

func tencentIndexCalendarBody(dates ...string) string {
	rows := make([]string, 0, len(dates))
	for _, tradeDate := range dates {
		rows = append(rows, fmt.Sprintf(`["%s","3000","3010","3020","2990","100000"]`, tradeDate))
	}
	return `{"code":0,"msg":"","data":{"sh000001":{"day":[` + strings.Join(rows, ",") + `]}}}`
}

func TestReferenceIndexCalendarPersistsWithoutStockBars(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return stringResponse(http.StatusOK, tencentIndexCalendarBody("2026-07-08", "2026-07-09", "2026-07-10")), nil
	})}
	if err := NewService(store, nil, client).refreshReferenceTradingCalendar(ctx, "2026-07-10"); err != nil {
		t.Fatal(err)
	}
	dates, err := store.GetObservedTradingDates(ctx, "2026-07-01", "2026-07-10")
	if err != nil || strings.Join(dates, ",") != "2026-07-08,2026-07-09,2026-07-10" || requests != 1 {
		t.Fatalf("calendar=%v requests=%d err=%v", dates, requests, err)
	}
}

func TestPrepareReferenceTradingCalendarUsesPersistentCooldown(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return stringResponse(http.StatusOK, tencentIndexCalendarBody("2026-07-08", "2026-07-09", "2026-07-10")), nil
	})}
	svc := NewService(store, nil, client)
	now := time.Now()
	prepared, latest, err := svc.prepareReferenceTradingCalendar(ctx, "2026-07-10", now)
	if err != nil || latest != "2026-07-10" || requests != 1 {
		t.Fatalf("first calendar prepare latest=%q requests=%d err=%v", latest, requests, err)
	}
	if got := dailyBarBatchTargetDate(prepared, "2026-07-10"); got != latest {
		t.Fatalf("prepared context target = %q, want %q", got, latest)
	}
	_, latest, err = svc.prepareReferenceTradingCalendar(ctx, "2026-07-10", now.Add(time.Minute))
	if err != nil || latest != "2026-07-10" || requests != 1 {
		t.Fatalf("cached calendar prepare latest=%q requests=%d err=%v", latest, requests, err)
	}
}

func TestPrepareReferenceTradingCalendarRejectsFailedRefresh(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertObservedTradingDates(ctx, []string{"2026-07-10"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "unavailable"), nil
	})}
	if _, _, err := NewService(store, nil, client).prepareReferenceTradingCalendar(
		ctx, "2026-07-10", time.Now(),
	); err == nil {
		t.Fatal("observed-only calendar survived a failed authoritative refresh")
	}
}

func TestRetryReferenceTradingCalendarOnlyRunsForPersistedFailure(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return stringResponse(http.StatusOK, tencentIndexCalendarBody("2026-07-10")), nil
	})}
	svc := NewService(store, nil, client)
	if err := svc.retryReferenceTradingCalendar(ctx); err != nil || requests != 0 {
		t.Fatalf("idle retry requests=%d err=%v", requests, err)
	}
	if err := store.SetAssetMaintenanceCursor(ctx, dailyBarReferenceCalendarRetryCursor, "2026-07-10"); err != nil {
		t.Fatal(err)
	}
	if err := svc.retryReferenceTradingCalendar(ctx); err != nil || requests != 0 {
		t.Fatalf("fresh retry cursor requests=%d err=%v, want cooldown", requests, err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_cursors SET updated_at = ? WHERE scope = ?
	`, time.Now().Add(-dailyBarReferenceCalendarProviderBackoff), dailyBarReferenceCalendarRetryCursor); err != nil {
		t.Fatalf("expire calendar retry cursor: %v", err)
	}
	if err := svc.retryReferenceTradingCalendar(ctx); err != nil || requests != 1 {
		t.Fatalf("expired retry requests=%d err=%v", requests, err)
	}
	if cursor, err := store.GetAssetMaintenanceCursor(ctx, dailyBarReferenceCalendarRetryCursor); err != nil || cursor != "" {
		t.Fatalf("retry cursor=%q err=%v", cursor, err)
	}
}

func TestDailyBarStorePrefersPresentZeroFieldsAndBuildsCalendar(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()
	fetchedAt := time.Date(2026, 7, 10, 18, 0, 0, 0, time.UTC)
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: "600000", Market: "SH", TradeDate: "2026-07-10", Open: 10, High: 10, Low: 10, Close: 10, Volume: 100, Adjusted: DailyBarAdjustedNone, Source: "tencent_fqkline", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2026-07-10", Open: 11, High: 11, Low: 11, Close: 11, Volume: 100, AmountPresent: true, TurnoverRatePresent: true, NetInflowPresent: true, MainNetInflowPresent: true, Adjusted: DailyBarAdjustedNone, Source: dailyBarSourceEastmoneyClosingQuote, FetchedAt: fetchedAt.Add(-time.Minute), Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}
	bars, err := store.GetDailyBars(ctx, "600000", DailyBarAdjustedNone, "2026-07-10", "2026-07-10", 0)
	if err != nil || len(bars) != 1 {
		t.Fatalf("bars=%+v err=%v", bars, err)
	}
	if bars[0].Close != 11 || !bars[0].AmountPresent || !bars[0].TurnoverRatePresent || !bars[0].NetInflowPresent || !bars[0].MainNetInflowPresent {
		t.Fatalf("present zero row was not preferred: %+v", bars[0])
	}
	calendar, err := store.GetObservedTradingDates(ctx, "2026-07-01", "2026-07-10")
	if err != nil || strings.Join(calendar, ",") != "2026-07-10" {
		t.Fatalf("calendar=%v err=%v", calendar, err)
	}
}

func TestEnsureDailyBarsUsesObservedCalendarForMiddleGapAndSkipsClosure(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	seed := func(symbol string, dates ...string) {
		t.Helper()
		bars := make([]StockV2DailyBar, 0, len(dates))
		for _, tradeDate := range dates {
			bars = append(bars, StockV2DailyBar{Symbol: symbol, Market: "SH", TradeDate: tradeDate, Open: 10, High: 10, Low: 10, Close: 10, Volume: 100, AmountPresent: true, TurnoverRatePresent: true, NetInflowPresent: true, MainNetInflowPresent: true, Adjusted: DailyBarAdjustedNone, Source: "seed", FetchedAt: time.Now(), Quality: DailyBarQualityOK})
		}
		if err := store.UpsertDailyBars(ctx, bars); err != nil {
			t.Fatalf("seed %s: %v", symbol, err)
		}
	}
	calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07"}
	seed("600001", calendar...)
	seed("600000", "2026-07-01", "2026-07-02", "2026-07-06", "2026-07-07")

	thsRequests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "d.10jqka.com.cn":
			thsRequests++
			return dailyBarHTTPResponse(req, http.StatusOK, `callback({"data":"20260703,10.00,11.00,9.00,10.50,100,1000,0"})`), nil
		case "push2his.eastmoney.com":
			return stringResponse(http.StatusOK, `{"rc":0,"data":{"klines":[]}}`), nil
		default:
			t.Fatalf("unexpected endpoint %q", req.URL.Host)
			return nil, nil
		}
	})}
	svc := NewService(store, nil, client)
	fetched, err := svc.ensureOneSymbol(ctx, StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock}, "2026-07-01", "2026-07-07", DailyBarAdjustedNone)
	if err != nil || fetched != 1 || thsRequests != 1 {
		t.Fatalf("middle gap ensure = fetched:%d ths:%d err:%v", fetched, thsRequests, err)
	}
	dates, err := store.GetDailyBarDates(ctx, "600000", DailyBarAdjustedNone, "2026-07-01", "2026-07-07")
	if err != nil || strings.Join(dates, ",") != strings.Join(calendar, ",") {
		t.Fatalf("dates=%v err=%v", dates, err)
	}

	fetched, err = svc.ensureOneSymbol(ctx, StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock}, "2026-07-01", "2026-07-07", DailyBarAdjustedNone)
	if err != nil || fetched != 0 || thsRequests != 1 {
		t.Fatalf("closure-safe ensure = fetched:%d ths:%d err:%v", fetched, thsRequests, err)
	}
}
