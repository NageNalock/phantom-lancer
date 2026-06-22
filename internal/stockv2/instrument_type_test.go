package stockv2

import (
	"context"
	"path/filepath"
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
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert exchange fund: %v", err)
	}
	if err := store.UpsertInstrument(ctx, StockV2Instrument{
		ID:     "inst-000001",
		Symbol: "000001",
		Market: "SZ",
		Name:   "平安银行",
		Status: "active",
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
