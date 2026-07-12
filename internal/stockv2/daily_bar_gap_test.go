package stockv2

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPlanDailyBarMissingRanges(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	tests := []struct {
		name  string
		dates []string
		want  []dailyBarMissingRange
	}{
		{
			name: "no data fetches target window",
			want: []dailyBarMissingRange{{Start: "2026-07-01", End: "2026-07-10"}},
		},
		{
			name:  "tail gap starts after local latest",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-03"},
			want:  []dailyBarMissingRange{{Start: "2026-07-04", End: "2026-07-10"}},
		},
		{
			name:  "middle natural-day gap waits for exchange calendar",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-08", "2026-07-09", "2026-07-10"},
			want:  nil,
		},
		{
			name:  "complete natural window skips",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planDailyBarMissingRanges(tt.dates, start, end)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ranges=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanDailyBarMissingRangesWithObservedCalendar(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	t.Run("repairs observed middle trading day", func(t *testing.T) {
		calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
		dates := []string{"2026-07-01", "2026-07-02", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
		want := []dailyBarMissingRange{{Start: "2026-07-03", End: "2026-07-03"}}
		if got := planDailyBarMissingRangesWithCalendar(dates, calendar, start, end); !reflect.DeepEqual(got, want) {
			t.Fatalf("ranges=%v, want %v", got, want)
		}
	})
	t.Run("does not request statutory closure absent from calendar", func(t *testing.T) {
		calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07"}
		if got := planDailyBarMissingRangesWithCalendar(calendar, calendar, start, end); len(got) != 0 {
			t.Fatalf("ranges=%v, want no holiday request", got)
		}
	})
	t.Run("does not retry non-trading leading boundary", func(t *testing.T) {
		calendar := []string{"2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
		if got := planDailyBarMissingRangesWithCalendar(calendar, calendar, start, end); len(got) != 0 {
			t.Fatalf("ranges=%v, want no leading closure request", got)
		}
	})
	t.Run("fresh symbol with only closing quote keeps bootstrap envelope", func(t *testing.T) {
		calendar := []string{"2026-07-10"}
		dates := []string{"2026-07-10"}
		want := []dailyBarMissingRange{{Start: "2026-07-01", End: "2026-07-09"}}
		if got := planDailyBarMissingRangesWithCalendar(dates, calendar, start, end); !reflect.DeepEqual(got, want) {
			t.Fatalf("ranges=%v, want %v", got, want)
		}
	})
}

func TestPlanDailyBarCoverageSeparatesDateCoreAndFlowGaps(t *testing.T) {
	calendar := []string{
		"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06",
		"2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10",
	}
	complete := func(tradeDate string) StockV2DailyBar {
		return StockV2DailyBar{
			TradeDate: tradeDate,
			Open:      10, High: 11, Low: 9, Close: 10.5, Volume: 100,
			AmountPresent: true, TurnoverRatePresent: true,
			NetInflowPresent: true, MainNetInflowPresent: true,
		}
	}
	bars := []StockV2DailyBar{
		complete("2026-07-01"),
		complete("2026-07-02"),
		complete("2026-07-06"),
		{ // The date exists, but invalid OHLCV keeps it repairable by a K source.
			TradeDate: "2026-07-07", Open: 10, High: 9, Low: 11, Close: 10, Volume: 0,
			AmountPresent: true, TurnoverRatePresent: true,
		},
		{ // Core K is complete; only the independent flow facet is missing.
			TradeDate: "2026-07-08", Open: 10, High: 11, Low: 9, Close: 10, Volume: 100,
			AmountPresent: true, TurnoverRatePresent: true,
		},
		complete("2026-07-09"),
		complete("2026-07-10"),
	}
	plan := planDailyBarCoverageWithCalendar(
		bars,
		calendar,
		nil,
		InstrumentTypeStock,
		"2026-07-01",
		"2026-07-10",
	)
	if want := []dailyBarMissingRange{{Start: "2026-07-03", End: "2026-07-03"}}; !reflect.DeepEqual(plan.DateGaps, want) {
		t.Fatalf("date gaps=%v, want %v", plan.DateGaps, want)
	}
	if want := []dailyBarMissingRange{{Start: "2026-07-07", End: "2026-07-07"}}; !reflect.DeepEqual(plan.CoreFacetGaps, want) {
		t.Fatalf("core gaps=%v, want %v", plan.CoreFacetGaps, want)
	}
	if want := []dailyBarMissingRange{{Start: "2026-07-08", End: "2026-07-08"}}; !reflect.DeepEqual(plan.FlowFacetGaps, want) {
		t.Fatalf("flow gaps=%v, want %v", plan.FlowFacetGaps, want)
	}
	if want := []dailyBarMissingRange{
		{Start: "2026-07-03", End: "2026-07-03"},
		{Start: "2026-07-07", End: "2026-07-07"},
	}; !reflect.DeepEqual(plan.historicalKFetchRanges(calendar), want) {
		t.Fatalf("historical fetch ranges=%v, want %v", plan.historicalKFetchRanges(calendar), want)
	}
}

func TestPlanDailyBarCoverageMergesOnlyConsecutiveTradingGaps(t *testing.T) {
	calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07"}
	plan := dailyBarCoveragePlan{
		DateGaps: []dailyBarMissingRange{
			{Start: "2026-07-02", End: "2026-07-02"},
			{Start: "2026-07-03", End: "2026-07-06"},
		},
		CoreFacetGaps: []dailyBarMissingRange{{Start: "2026-07-07", End: "2026-07-07"}},
	}
	want := []dailyBarMissingRange{{Start: "2026-07-02", End: "2026-07-07"}}
	if got := plan.historicalKFetchRanges(calendar); !reflect.DeepEqual(got, want) {
		t.Fatalf("merged trading gaps=%v, want %v", got, want)
	}

	plan = dailyBarCoveragePlan{
		DateGaps: []dailyBarMissingRange{
			{Start: "2026-07-02", End: "2026-07-02"},
			{Start: "2026-07-06", End: "2026-07-06"},
		},
	}
	want = []dailyBarMissingRange{
		{Start: "2026-07-02", End: "2026-07-02"},
		{Start: "2026-07-06", End: "2026-07-06"},
	}
	if got := plan.historicalKFetchRanges([]string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("separated trading gaps=%v, want %v", got, want)
	}
}

func TestPlanDailyBarCoverageDoesNotRequireStockFlowForFund(t *testing.T) {
	bar := StockV2DailyBar{
		TradeDate: "2026-07-10",
		Open:      4, High: 4.1, Low: 3.9, Close: 4, Volume: 100,
		AmountPresent: true, TurnoverRatePresent: true,
	}
	plan := planDailyBarCoverageWithCalendar(
		[]StockV2DailyBar{bar},
		[]string{"2026-07-10"},
		nil,
		InstrumentTypeExchangeFund,
		"2026-07-10",
		"2026-07-10",
	)
	if len(plan.DateGaps) != 0 || len(plan.CoreFacetGaps) != 0 || len(plan.FlowFacetGaps) != 0 {
		t.Fatalf("fund coverage=%+v, want complete without stock flow", plan)
	}
}

func TestVerifyDailyBarNoTradeCoverageRequiresTwoSuccessfulSources(t *testing.T) {
	calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07"}
	requested := dailyBarMissingRange{Start: "2026-07-01", End: "2026-07-07"}
	observations := []dailyBarSourceRangeObservation{
		{Source: "10jqka", Range: requested, Succeeded: true, ReturnedDates: []string{"2026-07-01", "2026-07-07"}},
		{Source: "tencent", Range: requested, Succeeded: true, ReturnedDates: []string{"2026-07-01", "2026-07-07"}},
		{Source: "baidu", Range: requested, Succeeded: false},
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, chinaMarketTZ)
	got := verifyDailyBarNoTradeCoverage(calendar, observations, []dailyBarMissingRange{requested}, now)
	want := []dailyBarNoTradeCoverage{{
		Range:     dailyBarMissingRange{Start: "2026-07-02", End: "2026-07-06"},
		Sources:   []string{"10jqka", "tencent"},
		CheckedAt: now,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("verified no-trade coverage=%+v, want %+v", got, want)
	}

	got = verifyDailyBarNoTradeCoverage(calendar, observations[:1], []dailyBarMissingRange{requested}, now)
	if len(got) != 0 {
		t.Fatalf("one-source no-trade coverage=%+v, want none", got)
	}
}

func TestVerifyDailyBarNoTradeCoverageDoesNotExpandEnvelopeBeyondRequestedGaps(t *testing.T) {
	calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07"}
	envelope := dailyBarMissingRange{Start: "2026-07-01", End: "2026-07-07"}
	observations := []dailyBarSourceRangeObservation{
		{Source: "10jqka", Range: envelope, Succeeded: true},
		{Source: "tencent", Range: envelope, Succeeded: true},
	}
	requested := []dailyBarMissingRange{
		{Start: "2026-07-02", End: "2026-07-02"},
		{Start: "2026-07-06", End: "2026-07-06"},
	}
	got := verifyDailyBarNoTradeCoverage(
		calendar,
		observations,
		requested,
		time.Date(2026, 7, 10, 12, 0, 0, 0, chinaMarketTZ),
	)
	want := []dailyBarNoTradeCoverage{
		{Range: requested[0], Sources: []string{"10jqka", "tencent"}, CheckedAt: time.Date(2026, 7, 10, 12, 0, 0, 0, chinaMarketTZ)},
		{Range: requested[1], Sources: []string{"10jqka", "tencent"}, CheckedAt: time.Date(2026, 7, 10, 12, 0, 0, 0, chinaMarketTZ)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("verified no-trade coverage=%+v, want only requested gaps %+v", got, want)
	}
}

func TestClampDailyBarRangeToInstrumentLifecycle(t *testing.T) {
	inst := StockV2Instrument{ListDate: "20260703", DelistDate: "2026-07-08"}
	start, end := clampDailyBarRangeToInstrument(inst, "2026-07-01", "2026-07-10")
	if start != "2026-07-03" || end != "2026-07-08" {
		t.Fatalf("clamped range = %s..%s", start, end)
	}
	start, end = clampDailyBarRangeToInstrument(StockV2Instrument{ListDate: "2026-08-01"}, "2026-07-01", "2026-07-10")
	if start != "" || end != "" {
		t.Fatalf("future listing range = %s..%s, want empty", start, end)
	}
}

func TestDailyBarNegativeCoverageSubtractsOnlyStableAbsences(t *testing.T) {
	ranges := []dailyBarMissingRange{{Start: "2026-07-01", End: "2026-07-10"}}
	returned := []StockV2DailyBar{{TradeDate: "2026-07-02"}, {TradeDate: "2026-07-06"}}
	absent := subtractCheckedDailyBarRanges(ranges, []dailyBarMissingRange{
		{Start: "2026-07-01", End: "2026-07-01"},
		{Start: "2026-07-03", End: "2026-07-05"},
	})
	want := []dailyBarMissingRange{{Start: "2026-07-02", End: "2026-07-02"}, {Start: "2026-07-06", End: "2026-07-10"}}
	if !reflect.DeepEqual(absent, want) {
		t.Fatalf("remaining = %v, want %v", absent, want)
	}
	negative := stableDailyBarGapChecks(dailyBarRangesWithoutReturnedBars(ranges, returned), time.Date(2026, 7, 10, 12, 0, 0, 0, chinaMarketTZ))
	if len(negative) == 0 || negative[len(negative)-1].End > "2026-07-07" {
		t.Fatalf("negative coverage = %v, want capped at T-3", negative)
	}
}

func TestDailyBarGapChecksPersistAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stockv2.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []dailyBarMissingRange{{Start: "2026-06-01", End: "2026-06-05"}}
	if err := store.RecordVerifiedDailyBarNoTradeCoverage(ctx, "600000", DailyBarAdjustedNone, []dailyBarNoTradeCoverage{{
		Range: want[0], Sources: []string{"10jqka_kline", "tencent_fqkline"}, CheckedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.ListDailyBarGapChecks(ctx, "600000", DailyBarAdjustedNone, "2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted gap checks = %v, want %v", got, want)
	}
}

func TestDailyBarGapChecksExpire(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecordVerifiedDailyBarNoTradeCoverage(ctx, "600000", DailyBarAdjustedNone, []dailyBarNoTradeCoverage{{
		Range:   dailyBarMissingRange{Start: "2026-06-01", End: "2026-06-05"},
		Sources: []string{"10jqka_kline", "tencent_fqkline"}, CheckedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE stockv2_daily_bar_gap_checks
		SET expires_at = ? WHERE symbol = ?
	`, time.Now().Add(-time.Hour), "600000"); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListDailyBarGapChecks(ctx, "600000", DailyBarAdjustedNone, "2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expired negative coverage still active: %v", got)
	}
}
