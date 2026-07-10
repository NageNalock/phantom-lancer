package stockv2

import (
	"context"
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
