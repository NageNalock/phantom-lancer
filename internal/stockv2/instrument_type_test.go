package stockv2

import (
	"context"
	"database/sql"
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

func TestStoreInitMigratesOldStockV2ColumnsBeforeIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stockv2.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE stockv2_portfolios (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE stockv2_instruments (
			id TEXT PRIMARY KEY,
			symbol TEXT NOT NULL UNIQUE,
			market TEXT NOT NULL,
			name TEXT,
			industry TEXT,
			sector TEXT,
			concepts TEXT,
			list_date TEXT,
			delist_date TEXT,
			status TEXT DEFAULT 'active',
			last_update_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		INSERT INTO stockv2_instruments (
			id, symbol, market, name, status, created_at, updated_at
		) VALUES (
			'old-inst-1', '000001', 'SZ', '平安银行', 'active', '2026-06-18 09:30:00', '2026-06-18 09:31:00'
		);
		CREATE TABLE stockv2_settings (
			id TEXT PRIMARY KEY,
			auto_update_enabled INTEGER DEFAULT 0,
			update_interval_sec INTEGER DEFAULT 3600,
			proxy_enabled INTEGER DEFAULT 0,
			proxy_type TEXT,
			proxy_host TEXT,
			proxy_port INTEGER,
			last_scheduled_update DATETIME,
			daily_bars_last_run DATETIME,
			financial_juice_enabled INTEGER DEFAULT 0,
			financial_juice_endpoint TEXT,
			financial_juice_cookie TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		INSERT INTO stockv2_settings (
			id, auto_update_enabled, update_interval_sec, proxy_enabled, proxy_type, proxy_host, proxy_port,
			last_scheduled_update, daily_bars_last_run, financial_juice_enabled, financial_juice_endpoint,
			financial_juice_cookie, created_at, updated_at
		) VALUES (
			'1', 1, 7200, 1, 'http', '127.0.0.1', 7890,
			'2026-06-18 09:30:00', '2026-06-18 09:35:00', 1, 'https://example.invalid/feed',
			'cookie=value', '2026-06-18 09:00:00', '2026-06-18 09:01:00'
		);
		CREATE TABLE stockv2_stock_profiles (
			symbol TEXT PRIMARY KEY,
			market TEXT NOT NULL,
			name TEXT NOT NULL,
			aliases_json TEXT NOT NULL DEFAULT '[]',
			industry TEXT,
			sectors_json TEXT NOT NULL DEFAULT '[]',
			concepts_json TEXT NOT NULL DEFAULT '[]',
			tags_json TEXT NOT NULL DEFAULT '[]',
			business_summary TEXT,
			profile_text TEXT NOT NULL,
			fund_type TEXT,
			tracking_index TEXT,
			theme TEXT,
			constituent_hint TEXT,
			profile_version INTEGER NOT NULL DEFAULT 1,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE stockv2_news_events (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			title TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE stockv2_news_link_candidates (
			id TEXT PRIMARY KEY,
			news_event_id TEXT NOT NULL,
			raw_news_id TEXT,
			symbol TEXT NOT NULL,
			market TEXT,
			instrument_name TEXT,
			match_method TEXT NOT NULL,
			score REAL NOT NULL DEFAULT 0,
			reason TEXT,
			matched_terms_json TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(news_event_id, symbol)
		);
		INSERT INTO stockv2_news_events (id, source, title, created_at, updated_at)
		VALUES ('old-news-event', 'jin10', '旧消息', '2026-06-18 09:30:00', '2026-06-18 09:31:00');
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed old schema: %v", err)
	}
	_ = db.Close()

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("new store should migrate old schema: %v", err)
	}
	defer store.Close()

	for _, tc := range []struct {
		table  string
		column string
	}{
		{"stockv2_instruments", "instrument_type"},
		{"stockv2_stock_profiles", "instrument_type"},
		{"stockv2_news_events", "raw_news_id"},
		{"stockv2_news_events", "external_id"},
		{"stockv2_news_events", "summary"},
		{"stockv2_news_events", "content"},
		{"stockv2_news_events", "url"},
		{"stockv2_news_events", "quality_status"},
		{"stockv2_news_events", "dedupe_key"},
		{"stockv2_news_events", "link_status"},
		{"stockv2_news_events", "event_at"},
		{"stockv2_news_events", "link_processed_at"},
		{"stockv2_news_link_candidates", "monitor_status"},
		{"stockv2_news_link_candidates", "monitor_hit_id"},
		{"stockv2_news_link_candidates", "monitored_at"},
	} {
		if !testColumnExists(t, store.db, tc.table, tc.column) {
			t.Fatalf("%s.%s was not migrated", tc.table, tc.column)
		}
	}
	var eventAt string
	if err := store.db.QueryRow(`SELECT event_at FROM stockv2_news_events WHERE id = 'old-news-event'`).Scan(&eventAt); err != nil {
		t.Fatalf("query migrated event_at: %v", err)
	}
	if eventAt == "" {
		t.Fatal("event_at was not backfilled")
	}
	if testColumnExists(t, store.db, "stockv2_instruments", "status") {
		t.Fatal("stockv2_instruments.status legacy column was not removed")
	}
	for _, column := range []string{"update_interval_sec", "proxy_enabled", "proxy_type", "proxy_host", "proxy_port", "daily_bars_last_run"} {
		if testColumnExists(t, store.db, "stockv2_settings", column) {
			t.Fatalf("stockv2_settings.%s legacy column was not removed", column)
		}
	}
	var instrumentName string
	if err := store.db.QueryRow(`SELECT name FROM stockv2_instruments WHERE symbol = '000001'`).Scan(&instrumentName); err != nil {
		t.Fatalf("query migrated instrument: %v", err)
	}
	if instrumentName != "平安银行" {
		t.Fatalf("migrated instrument name = %q", instrumentName)
	}
	settings, err := store.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("get migrated settings: %v", err)
	}
	if !settings.AutoUpdateEnabled || settings.LastScheduledUpdate.IsZero() || !settings.FinancialJuiceEnabled || settings.FinancialJuiceEndpoint == "" || !settings.FinancialJuiceCookieSet {
		t.Fatalf("migrated settings = %+v, want active settings and financialjuice preserved", settings)
	}
}

func testColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	return count > 0
}
