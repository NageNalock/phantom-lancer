package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshDailyBarCoverageQualityPersistsIndependentFacets(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	inst := StockV2Instrument{
		ID: generateID(), Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
	}
	if err := store.UpsertInstrument(ctx, inst); err != nil {
		t.Fatal(err)
	}
	calendar := []string{"2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09"}
	if err := store.UpsertObservedTradingDates(ctx, calendar, time.Now()); err != nil {
		t.Fatal(err)
	}

	valid := func(tradeDate string) StockV2DailyBar {
		return StockV2DailyBar{
			Symbol: "600000", Market: "SH", TradeDate: tradeDate,
			Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100,
			AmountPresent: true, TurnoverRatePresent: true,
			NetInflowPresent: true, MainNetInflowPresent: true,
			Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
		}
	}
	complete := valid(calendar[0])
	invalidCore := valid(calendar[1])
	invalidCore.High = 10 // below close: date exists, but SQL must reject its OHLC core facet.
	missingFlow := valid(calendar[2])
	missingFlow.NetInflowPresent = false
	missingFlow.MainNetInflowPresent = false
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{complete, invalidCore, missingFlow}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordVerifiedDailyBarNoTradeCoverage(ctx, inst.Symbol, DailyBarAdjustedNone, []dailyBarNoTradeCoverage{{
		Range:   dailyBarMissingRange{Start: calendar[3], End: calendar[3]},
		Sources: []string{"10jqka_kline", "tencent_fqkline"}, CheckedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	quality, err := store.RefreshDailyBarCoverageQuality(
		ctx, inst, DailyBarAdjustedNone, calendar[0], calendar[len(calendar)-1],
	)
	if err != nil {
		t.Fatal(err)
	}
	if quality.ExpectedDateCount != 4 || quality.CoveredDateCount != 4 ||
		quality.DateGapCount != 0 || quality.CoreGapCount != 1 ||
		quality.FlowGapCount != 1 || quality.VerifiedNoTradeCount != 1 {
		t.Fatalf("coverage quality = %+v", quality)
	}

	serviceQuality, err := NewService(store, nil, nil).GetDailyBarsQuality(ctx, inst.Symbol, DailyBarAdjustedNone)
	if err != nil {
		t.Fatal(err)
	}
	if !serviceQuality.CoverageKnown || serviceQuality.IncompleteCount != 2 || serviceQuality.FacetsComplete {
		t.Fatalf("service quality = %+v", serviceQuality)
	}

	completeDates, err := store.GetCompleteDailyBarDates(
		ctx, inst.Symbol, DailyBarAdjustedNone, calendar[0], calendar[2], true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(completeDates) != 1 || completeDates[0] != calendar[0] {
		t.Fatalf("SQL-complete dates = %v, want only %s", completeDates, calendar[0])
	}
}

func TestRefreshDailyBarCoverageQualityDoesNotRequireStockFlowForETF(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	inst := StockV2Instrument{
		ID: generateID(), Symbol: "510300", Market: "SH", InstrumentType: InstrumentTypeExchangeFund,
	}
	if err := store.UpsertInstrument(ctx, inst); err != nil {
		t.Fatal(err)
	}
	const tradeDate = "2026-07-09"
	if err := store.UpsertObservedTradingDates(ctx, []string{tradeDate}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{{
		Symbol: inst.Symbol, Market: inst.Market, TradeDate: tradeDate,
		Open: 4, High: 4.1, Low: 3.9, Close: 4.05, Volume: 100,
		AmountPresent: true, TurnoverRatePresent: true,
		Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
	}}); err != nil {
		t.Fatal(err)
	}
	quality, err := store.RefreshDailyBarCoverageQuality(
		ctx, inst, DailyBarAdjustedNone, tradeDate, tradeDate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if quality.FlowGapCount != 0 || quality.CoreGapCount != 0 || quality.DateGapCount != 0 {
		t.Fatalf("ETF coverage incorrectly requires stock flow: %+v", quality)
	}
	serviceQuality, err := NewService(store, nil, nil).GetDailyBarsQuality(ctx, inst.Symbol, DailyBarAdjustedNone)
	if err != nil {
		t.Fatal(err)
	}
	if !serviceQuality.FacetsComplete {
		t.Fatalf("ETF quality should be complete without stock flow: %+v", serviceQuality)
	}
}

func TestDailyBarCoreCoverageGapsRequireDurableRetryButFlowDoesNot(t *testing.T) {
	if err := dailyBarCoreCoverageRetryError(DailyBarCoverageQuality{ExpectedDateCount: 250, DateGapCount: 1}); err == nil {
		t.Fatal("missing trading date did not require retry")
	}
	if err := dailyBarCoreCoverageRetryError(DailyBarCoverageQuality{ExpectedDateCount: 250, CoreGapCount: 1}); err == nil {
		t.Fatal("incomplete OHLCV/amount/turnover did not require retry")
	}
	if err := dailyBarCoreCoverageRetryError(DailyBarCoverageQuality{ExpectedDateCount: 250, FlowGapCount: 10}); err != nil {
		t.Fatalf("flow-only repair leaked into the unbounded maintenance retry queue: %v", err)
	}
}
