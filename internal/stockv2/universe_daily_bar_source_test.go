package stockv2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseTushareDailyBars(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC)
	result := tushareDatasetResult{
		Fields: []string{"ts_code", "trade_date", "open", "high", "low", "close", "pre_close", "change", "pct_chg", "vol", "amount"},
		Items: [][]any{
			{"600000.SH", "20260824", 10.0, 10.8, 9.9, 10.5, 10.1, 0.4, 3.9604, 1234.5, 6789.25},
			{"bad", "20260824", 1, 1, 1, 1, 1, 0, 0, 1, 1},
		},
		Source: opportunityFundFlowSourcePrimary,
	}
	bars, err := parseTushareDailyBars(result, fetchedAt)
	if err != nil {
		t.Fatalf("parse Tushare daily bars: %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("bar count = %d, want 1", len(bars))
	}
	got := bars[0]
	if got.Symbol != "600000" || got.Market != "SH" || got.TradeDate != "2026-08-24" || got.Adjusted != DailyBarAdjustedNone {
		t.Fatalf("bar identity = %+v", got)
	}
	if got.Volume != 1234.5 || got.Amount != 6789250 || got.PctChange != 3.9604 || got.Source != "tushare_daily_"+opportunityFundFlowSourcePrimary {
		t.Fatalf("bar values = %+v", got)
	}
}

func TestParseTushareDailyBarsRejectsMissingFields(t *testing.T) {
	_, err := parseTushareDailyBars(tushareDatasetResult{Fields: []string{"ts_code"}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "trade_date") {
		t.Fatalf("error = %v, want missing trade_date", err)
	}
}

func TestDailyBarsSourceOpensCircuitAfterRepeatedHTTPFailures(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusNotImplemented, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	source := NewDailyBarsSource(nil, client)
	for attempt := 1; attempt <= dailyBarsSourceCircuitThreshold; attempt++ {
		_, err := source.FetchDailyBars(context.Background(), "600000", "SH", "2026-08-01", "2026-08-24", DailyBarAdjustedNone, 40)
		if attempt < dailyBarsSourceCircuitThreshold && (err == nil || errors.Is(err, ErrDailyBarsSourceCircuitOpen)) {
			t.Fatalf("attempt %d error = %v, want ordinary HTTP failure", attempt, err)
		}
		if attempt == dailyBarsSourceCircuitThreshold && !errors.Is(err, ErrDailyBarsSourceCircuitOpen) {
			t.Fatalf("attempt %d error = %v, want open circuit", attempt, err)
		}
	}
	_, err := source.FetchDailyBars(context.Background(), "600001", "SH", "2026-08-01", "2026-08-24", DailyBarAdjustedNone, 40)
	if !errors.Is(err, ErrDailyBarsSourceCircuitOpen) || requests != dailyBarsSourceCircuitThreshold {
		t.Fatalf("blocked request error=%v requests=%d", err, requests)
	}
}

func TestTencentBatchDailyBarAndUniverseIncrement(t *testing.T) {
	fields := make([]string, 53)
	fields[3], fields[4], fields[5], fields[6] = "10.50", "10.10", "10.00", "1234.5"
	fields[30], fields[32], fields[33], fields[34], fields[37] = "20260824153000", "3.96", "10.80", "9.90", "678.925"
	bar := tencentBatchDailyBar(fields, "600000", "SH", time.Now())
	if bar == nil || bar.TradeDate != "2026-08-24" || bar.Amount != 6789250 {
		t.Fatalf("Tencent batch daily bar = %+v", bar)
	}

	svc := &Service{dailyBarsSource: NewDailyBarsSource(nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatal("fqkline should not be called for an incremental completed series")
		return nil, nil
	})})}
	fetched, bars, err := svc.fetchDailyBarsForInstrumentWithQuality(context.Background(), StockV2Instrument{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, sourceDailyBar: bar,
	}, "2026-08-24", DailyBarsQuality{HasData: true, Meets250: true, LatestDate: "2026-08-21"}, true)
	if err != nil || !fetched || len(bars) != 1 || bars[0].Source != "tencent_batch_quote" {
		t.Fatalf("incremental result fetched=%v bars=%+v err=%v", fetched, bars, err)
	}
}
