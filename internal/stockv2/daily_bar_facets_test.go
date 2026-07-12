package stockv2

import (
	"context"
	"math"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestParseEastmoneyDailyFlowFacetsUsesRawFieldPresence(t *testing.T) {
	facets, err := parseEastmoneyDailyFlowFacets([]byte(`{
		"rc":0,
		"data":{"klines":[
			"2026-07-09,0,0,0",
			"2026-07-10,--,12,3"
		]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	zero := facets["2026-07-09"]
	if !zero.MainNetInflowPresent || !zero.NetInflowPresent || zero.MainNetInflow != 0 || zero.NetInflow != 0 {
		t.Fatalf("legitimate zero flow lost presence: %+v", zero)
	}
	missing := facets["2026-07-10"]
	if missing.MainNetInflowPresent || missing.NetInflowPresent {
		t.Fatalf("missing flow was claimed present: %+v", missing)
	}
}

func TestDailyBarCoreAndFlowCoverageAreIndependent(t *testing.T) {
	bar := StockV2DailyBar{
		Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100,
		AmountPresent: true, TurnoverRatePresent: true,
	}
	if !dailyBarCoreFacetsComplete(bar) {
		t.Fatalf("core OHLCV/amount/turnover should be complete: %+v", bar)
	}
	if dailyBarFlowFacetsComplete(bar) {
		t.Fatalf("missing flow was claimed complete: %+v", bar)
	}
	if dailyBarAnalysisFacetsComplete(bar, InstrumentTypeStock) {
		t.Fatal("stock analysis coverage should require the independent flow facet")
	}
	if !dailyBarAnalysisFacetsComplete(bar, InstrumentTypeExchangeFund) {
		t.Fatal("exchange fund analysis coverage must not require stock flow")
	}

	bar.NetInflowPresent = true
	bar.MainNetInflowPresent = true
	if !dailyBarAnalysisFacetsComplete(bar, InstrumentTypeStock) {
		t.Fatal("stock analysis coverage should be complete after flow repair")
	}
}

func TestDailyBarCoreCoverageRejectsInvalidOHLCV(t *testing.T) {
	base := StockV2DailyBar{
		Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100,
		AmountPresent: true, TurnoverRatePresent: true,
	}
	tests := []struct {
		name string
		bar  StockV2DailyBar
	}{
		{name: "missing volume", bar: func() StockV2DailyBar { b := base; b.Volume = 0; return b }()},
		{name: "high below close", bar: func() StockV2DailyBar { b := base; b.High = 10; return b }()},
		{name: "low above open", bar: func() StockV2DailyBar { b := base; b.Low = 10.1; return b }()},
		{name: "missing amount", bar: func() StockV2DailyBar { b := base; b.AmountPresent = false; return b }()},
		{name: "missing turnover", bar: func() StockV2DailyBar { b := base; b.TurnoverRatePresent = false; return b }()},
		{name: "nan amount", bar: func() StockV2DailyBar { b := base; b.Amount = math.NaN(); return b }()},
		{name: "infinite turnover", bar: func() StockV2DailyBar { b := base; b.TurnoverRate = math.Inf(1); return b }()},
		{name: "negative turnover", bar: func() StockV2DailyBar { b := base; b.TurnoverRate = -1; return b }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if dailyBarCoreFacetsComplete(tt.bar) {
				t.Fatalf("invalid core row was claimed complete: %+v", tt.bar)
			}
		})
	}
}

func TestDailyBarFlowCoverageRejectsNonFiniteValues(t *testing.T) {
	bar := StockV2DailyBar{
		NetInflow: math.NaN(), MainNetInflow: math.Inf(1),
		NetInflowPresent: true, MainNetInflowPresent: true,
	}
	if dailyBarFlowFacetsComplete(bar) {
		t.Fatalf("non-finite flow was claimed complete: %+v", bar)
	}
}

func TestEastmoneyDailyFlowFailureStartsSharedCooldown(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return stringResponse(http.StatusBadGateway, "unavailable"), nil
	})}
	svc := NewService(nil, nil, client)
	for _, symbol := range []string{"600000", "600001"} {
		_, _ = svc.fetchEastmoneyDailyFlowFacets(context.Background(), StockV2Instrument{Symbol: symbol, Market: "SH", InstrumentType: InstrumentTypeStock})
	}
	if requests != 1 {
		t.Fatalf("network requests=%d, want one before shared cooldown", requests)
	}
}

func TestRepairStoredDailyBarFlowFacetsAvoidsHistoricalRefetch(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inst := StockV2Instrument{ID: generateID(), Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock}
	if err := store.UpsertInstrument(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{{
		Symbol: "600000", Market: "SH", TradeDate: "2026-07-09",
		Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100,
		AmountPresent: true, TurnoverRatePresent: true,
		Adjusted: DailyBarAdjustedNone, Source: "10jqka_kline", FetchedAt: time.Now(), Quality: DailyBarQualityPartial,
	}}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return stringResponse(http.StatusOK, `{"rc":0,"data":{"klines":["2026-07-09,0,0,0"]}}`), nil
	})}
	svc := NewService(store, nil, client)
	attempted, err := svc.repairStoredDailyBarFlowFacets(ctx, inst, "2026-07-09", "2026-07-09")
	if err != nil || !attempted || requests != 1 {
		t.Fatalf("attempted=%v requests=%d err=%v", attempted, requests, err)
	}
	dates, err := store.GetCompleteDailyBarDates(ctx, "600000", DailyBarAdjustedNone, "2026-07-09", "2026-07-09", true)
	if err != nil || len(dates) != 1 {
		t.Fatalf("complete dates=%v err=%v", dates, err)
	}
}
