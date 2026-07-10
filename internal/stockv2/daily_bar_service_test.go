package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyBarRangeStartEndUsesAvailableTradeDate(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	tests := []struct {
		name    string
		now     time.Time
		wantEnd string
	}{
		{
			name:    "before close data uses previous weekday",
			now:     time.Date(2026, 7, 10, 0, 17, 0, 0, loc),
			wantEnd: "2026-07-09",
		},
		{
			name:    "after close data uses same weekday",
			now:     time.Date(2026, 7, 10, 16, 45, 0, 0, loc),
			wantEnd: "2026-07-10",
		},
		{
			name:    "weekend rolls back to friday",
			now:     time.Date(2026, 7, 12, 12, 0, 0, 0, loc),
			wantEnd: "2026-07-10",
		},
		{
			name:    "monday before close rolls back to friday",
			now:     time.Date(2026, 7, 13, 9, 0, 0, 0, loc),
			wantEnd: "2026-07-10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotEnd := dailyBarRangeStartEnd(DailyBarRange1Y, tt.now)
			if gotEnd != tt.wantEnd {
				t.Fatalf("end=%s, want %s", gotEnd, tt.wantEnd)
			}
		})
	}
}

func TestAssetMaintenanceDailyBarWindowCanReachTwoHundredFiftySessions(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 18, 0, 0, 0, loc)
	start, end := assetMaintenanceDailyBarStartEnd(now)
	startAt, err := time.ParseInLocation("2006-01-02", start, loc)
	if err != nil {
		t.Fatal(err)
	}
	endAt, err := time.ParseInLocation("2006-01-02", end, loc)
	if err != nil {
		t.Fatal(err)
	}
	if days := int(endAt.Sub(startAt).Hours() / 24); days != assetMaintenanceDailyBarWindowDays || days <= dailyBarRangeDays(DailyBarRange1Y) {
		t.Fatalf("maintenance window=%d days (%s..%s), want %d and wider than UI 1y", days, start, end, assetMaintenanceDailyBarWindowDays)
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

func TestExchangeFundQualityDoesNotRequireStockFlowFacets(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inst := StockV2Instrument{ID: generateID(), Symbol: "510300", Market: "SH", InstrumentType: InstrumentTypeExchangeFund}
	if err := store.UpsertInstrument(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{{
		Symbol: "510300", Market: "SH", TradeDate: time.Now().Format("2006-01-02"),
		Open: 4, High: 4.1, Low: 3.9, Close: 4, Volume: 100,
		AmountPresent: true, TurnoverRatePresent: true,
		Adjusted: DailyBarAdjustedNone, Source: "fund_test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
	}}); err != nil {
		t.Fatal(err)
	}
	quality, err := NewService(store, nil, nil).GetDailyBarsQuality(ctx, "510300", DailyBarAdjustedNone)
	if err != nil {
		t.Fatal(err)
	}
	if !quality.FacetsComplete || quality.IncompleteCount != 0 {
		t.Fatalf("fund quality=%+v, want core facets complete without stock flow", quality)
	}
}
