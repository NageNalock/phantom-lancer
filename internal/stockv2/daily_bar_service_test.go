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

func TestDailyBarsNeedsMaintenance(t *testing.T) {
	tests := []struct {
		name            string
		q               DailyBarsQuality
		targetTradeDate string
		want            bool
	}{
		{name: "missing", q: DailyBarsQuality{}, want: true},
		{name: "partial", q: DailyBarsQuality{HasData: true, RowCount: 120, Meets250: false}, want: true},
		{name: "stale", q: DailyBarsQuality{HasData: true, RowCount: 260, Meets250: true, Stale: true}, want: true},
		{name: "ready", q: DailyBarsQuality{HasData: true, RowCount: 260, Meets250: true, LatestDate: "2026-08-07"}, targetTradeDate: "2026-08-07", want: false},
		{name: "behind published session", q: DailyBarsQuality{HasData: true, RowCount: 260, Meets250: true, LatestDate: "2026-08-05"}, targetTradeDate: "2026-08-07", want: true},
		{name: "holiday does not refetch", q: DailyBarsQuality{HasData: true, RowCount: 260, Meets250: true, LatestDate: "2026-08-07", Stale: true}, targetTradeDate: "2026-08-07", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dailyBarsNeedsMaintenance(tt.q, tt.targetTradeDate); got != tt.want {
				t.Fatalf("dailyBarsNeedsMaintenance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUniverseDailyBarsTargetTradeDateUsesLatestCompletedSession(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"code":0,"msg":"","data":{"sz000001":{"day":[
					["2026-08-07","11.2","11.1","11.3","11.0","1000"],
					["2026-08-10","11.1","11.3","11.4","11.0","1200"]
				]}}
			}`)),
			Request: req,
		}, nil
	})}
	svc := &Service{dailyBarsSource: NewDailyBarsSource(nil, client)}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)

	if got := svc.universeDailyBarsTargetTradeDate(context.Background(), time.Date(2026, 8, 10, 14, 0, 0, 0, loc)); got != "2026-08-07" {
		t.Fatalf("pre-close target = %q, want 2026-08-07", got)
	}
	if got := svc.universeDailyBarsTargetTradeDate(context.Background(), time.Date(2026, 8, 10, 16, 0, 0, 0, loc)); got != "2026-08-10" {
		t.Fatalf("post-close target = %q, want 2026-08-10", got)
	}
}

func TestUniverseDailyBarsTargetTradeDateFallsBackToCompletedCalendarDate(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotImplemented,
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Request:    req,
		}, nil
	})}
	svc := &Service{dailyBarsSource: NewDailyBarsSource(nil, client)}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	got := svc.universeDailyBarsTargetTradeDate(context.Background(), time.Date(2026, 8, 25, 12, 0, 0, 0, loc))
	if got != "2026-08-24" {
		t.Fatalf("fallback target = %q, want completed calendar date", got)
	}
}

func TestUniverseHistoryBackfillStopsAtCompletedTradeDate(t *testing.T) {
	requestedParam := ""
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedParam = req.URL.Query().Get("param")
		return &http.Response{
			StatusCode: http.StatusNotImplemented,
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Request:    req,
		}, nil
	})}
	svc := &Service{dailyBarsSource: NewDailyBarsSource(nil, client)}
	_, _, _ = svc.fetchDailyBarsForInstrumentWithQuality(context.Background(), StockV2Instrument{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
	}, "2026-08-24", DailyBarsQuality{HasData: true, Meets250: false}, true)
	if !strings.Contains(requestedParam, ",2026-08-24,") {
		t.Fatalf("fqkline param = %q, want completed trade date end", requestedParam)
	}
}

func TestGetDailyBarsQualityBatchReturnsDataAndJobErrors(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, nil)
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	today := now.Format("2006-01-02")

	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: "300750", Market: "SZ", TradeDate: yesterday, Close: 100, Adjusted: DailyBarAdjustedNone, Source: QuoteSourceTencent, FetchedAt: now.Add(-time.Hour), Quality: DailyBarQualityOK},
		{Symbol: "300750", Market: "SZ", TradeDate: today, Close: 101, Adjusted: DailyBarAdjustedNone, Source: QuoteSourceTencent, FetchedAt: now, Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert daily bars: %v", err)
	}
	if err := store.CreateDailyBarJob(ctx, StockV2DailyBarJob{
		ID:           "job-600519",
		JobType:      DailyBarJobTypeEnsure,
		Mode:         DailyBarJobModeSymbol,
		Symbol:       "600519",
		Status:       "failed",
		FailedCount:  1,
		ErrorMessage: "fetch failed",
		Adjusted:     DailyBarAdjustedNone,
		StartAt:      now,
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("create daily bar job: %v", err)
	}

	got, err := svc.GetDailyBarsQualityBatch(ctx, []string{"300750", "600519", "300750"}, DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get quality batch: %v", err)
	}
	if got["300750"].RowCount != 2 || got["300750"].LatestDate != today || got["300750"].Source != QuoteSourceTencent {
		t.Fatalf("quality for 300750 = %+v", got["300750"])
	}
	if got["600519"].HasData || got["600519"].LastErrorMessage != "fetch failed" {
		t.Fatalf("quality for 600519 = %+v", got["600519"])
	}
}
