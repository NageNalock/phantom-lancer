package stockv2

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestInferInstrumentMarketAndTypeSupportsExchangeFunds(t *testing.T) {
	cases := []struct {
		symbol         string
		wantMarket     string
		wantInstrument string
	}{
		{"510300", "SH", InstrumentTypeExchangeFund},
		{"159915", "SZ", InstrumentTypeExchangeFund},
		{"161725", "SZ", InstrumentTypeExchangeFund},
		{"600000", "SH", InstrumentTypeStock},
		{"000001", "SZ", InstrumentTypeStock},
		{"839008", "BJ", InstrumentTypeStock},
	}

	for _, tc := range cases {
		gotMarket, gotInstrument := inferInstrumentMarketAndType(tc.symbol)
		if gotMarket != tc.wantMarket || gotInstrument != tc.wantInstrument {
			t.Fatalf("infer(%s) = %s/%s, want %s/%s", tc.symbol, gotMarket, gotInstrument, tc.wantMarket, tc.wantInstrument)
		}
	}
}

func TestParseTencentLineMarksExchangeFund(t *testing.T) {
	uds := &UniverseDataSource{}
	meta := map[string]instrumentCodeMeta{
		"sh510300": {Market: "SH", InstrumentType: InstrumentTypeExchangeFund},
	}

	inst, err := uds.parseTencentLine(tencentQuoteLine("sh510300", "沪深300ETF", "510300", "4.20", "4.10"), meta)
	if err != nil {
		t.Fatalf("parse tencent line: %v", err)
	}
	if inst == nil {
		t.Fatal("parse tencent line returned nil instrument")
	}
	if inst.Symbol != "510300" || inst.Market != "SH" || inst.InstrumentType != InstrumentTypeExchangeFund {
		t.Fatalf("instrument = %+v, want 510300 SH exchange_fund", inst)
	}
}

func TestParseTencentLineMarksRetiredInstrumentInactive(t *testing.T) {
	uds := &UniverseDataSource{}
	fields := make([]string, 41)
	fields[1] = "已终止基金"
	fields[2] = "501023"
	fields[40] = "D"

	inst, err := uds.parseTencentLine(`v_sh501023="`+strings.Join(fields, "~")+`"`, nil)
	if err != nil {
		t.Fatalf("parse retired Tencent instrument: %v", err)
	}
	if inst == nil || !inst.sourceInactive {
		t.Fatalf("instrument = %+v, want transient inactive marker", inst)
	}
}

func TestProcessSymbolsNormalizesPrefixedExchangeFunds(t *testing.T) {
	uds := &UniverseDataSource{}

	codes, meta := uds.processSymbols([]string{"sh510300", "SZ159915", "000001"})
	if len(codes) != 3 {
		t.Fatalf("codes = %#v, want three normalized codes", codes)
	}
	if codes[0] != "sh510300" || codes[1] != "sz159915" || codes[2] != "sz000001" {
		t.Fatalf("codes = %#v, want normalized tencent codes", codes)
	}
	if meta["sh510300"].InstrumentType != InstrumentTypeExchangeFund || meta["sz159915"].InstrumentType != InstrumentTypeExchangeFund {
		t.Fatalf("meta = %#v, want exchange fund types for fund codes", meta)
	}
}

func TestInstrumentTypeRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-510300",
		Symbol:         "510300",
		Market:         "SH",
		InstrumentType: InstrumentTypeExchangeFund,
		Name:           "沪深300ETF",
	}); err != nil {
		t.Fatalf("upsert exchange fund: %v", err)
	}
	if err := store.UpsertInstrument(ctx, StockV2Instrument{
		ID:     "inst-000001",
		Symbol: "000001",
		Market: "SZ",
		Name:   "平安银行",
	}); err != nil {
		t.Fatalf("upsert stock: %v", err)
	}

	fund, err := store.GetInstrument(ctx, "510300")
	if err != nil {
		t.Fatalf("get fund: %v", err)
	}
	if fund.InstrumentType != InstrumentTypeExchangeFund {
		t.Fatalf("fund type = %q, want %q", fund.InstrumentType, InstrumentTypeExchangeFund)
	}

	stock, err := store.GetInstrument(ctx, "000001")
	if err != nil {
		t.Fatalf("get stock: %v", err)
	}
	if stock.InstrumentType != InstrumentTypeStock {
		t.Fatalf("stock type = %q, want default %q", stock.InstrumentType, InstrumentTypeStock)
	}
}

func TestInstrumentListFiltersMarketAndType(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	for _, inst := range []StockV2Instrument{
		{ID: "inst-510300", Symbol: "510300", Market: "SH", InstrumentType: InstrumentTypeExchangeFund, Name: "沪深300ETF"},
		{ID: "inst-159915", Symbol: "159915", Market: "SZ", InstrumentType: InstrumentTypeExchangeFund, Name: "创业板ETF"},
		{ID: "inst-000001", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行"},
	} {
		if err := store.UpsertInstrument(ctx, inst); err != nil {
			t.Fatalf("upsert %s: %v", inst.Symbol, err)
		}
	}

	funds, err := store.GetInstrumentsFiltered(ctx, "SZ", InstrumentTypeExchangeFund, "", 10, 0)
	if err != nil {
		t.Fatalf("list filtered funds: %v", err)
	}
	if len(funds) != 1 || funds[0].Symbol != "159915" {
		t.Fatalf("filtered funds = %+v, want only 159915", funds)
	}
	count, err := store.CountInstrumentsFiltered(ctx, "SZ", InstrumentTypeExchangeFund, "")
	if err != nil {
		t.Fatalf("count filtered funds: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestInstrumentListFiltersProfileStatus(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	for _, inst := range []StockV2Instrument{
		{ID: "inst-300750", Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "宁德时代"},
		{ID: "inst-600519", Symbol: "600519", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "贵州茅台"},
	} {
		if err := store.UpsertInstrument(ctx, inst); err != nil {
			t.Fatalf("upsert %s: %v", inst.Symbol, err)
		}
	}
	if _, err := store.UpsertStockProfile(ctx, StockProfile{
		Symbol:          "300750",
		Market:          "SZ",
		InstrumentType:  InstrumentTypeStock,
		Name:            "宁德时代",
		ProfileText:     "动力电池 储能",
		AIProfileStatus: StockProfileAIStatusReady,
	}); err != nil {
		t.Fatalf("upsert stock profile: %v", err)
	}

	ready, err := store.GetInstrumentsFiltered(ctx, "", "", "ai_ready", 10, 0)
	if err != nil {
		t.Fatalf("list ai_ready instruments: %v", err)
	}
	if len(ready) != 1 || ready[0].Symbol != "300750" {
		t.Fatalf("ai_ready instruments = %+v, want only 300750", ready)
	}
	missingCount, err := store.CountInstrumentsFiltered(ctx, "", "", "basic_missing")
	if err != nil {
		t.Fatalf("count basic_missing instruments: %v", err)
	}
	if missingCount != 1 {
		t.Fatalf("basic_missing count = %d, want 1", missingCount)
	}
}
