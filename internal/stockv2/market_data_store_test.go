package stockv2

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMarketDataStoreUsesOneDuckDBWorker(t *testing.T) {
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	var threads int
	if err := store.db.QueryRow(`SELECT current_setting('threads')`).Scan(&threads); err != nil {
		t.Fatalf("read DuckDB thread setting: %v", err)
	}
	if threads != marketDataDuckDBThreads {
		t.Fatalf("DuckDB threads = %d, want %d", threads, marketDataDuckDBThreads)
	}
}

func TestMarketDataStoreUpsertQueryStats(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	fetchedAt := time.Date(2026, 6, 18, 15, 5, 0, 0, time.UTC)
	bars := []StockV2DailyBar{
		{
			Symbol:    "302132",
			Market:    "SZ",
			TradeDate: "2026-06-17",
			Open:      58.1,
			High:      60.2,
			Low:       57.8,
			Close:     60,
			Volume:    100,
			Adjusted:  DailyBarAdjustedNone,
			Source:    "unit_test",
			FetchedAt: fetchedAt,
			Quality:   DailyBarQualityOK,
		},
		{
			Symbol:    "302132",
			Market:    "SZ",
			TradeDate: "2026-06-18",
			Open:      60.1,
			High:      60.6,
			Low:       59.08,
			Close:     59.33,
			Volume:    120,
			Adjusted:  DailyBarAdjustedNone,
			Source:    "unit_test",
			FetchedAt: fetchedAt,
			Quality:   DailyBarQualityOK,
		},
	}
	if err := store.UpsertDailyBars(ctx, bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}
	bars[1].Close = 59.88
	if err := store.UpsertDailyBars(ctx, bars[1:]); err != nil {
		t.Fatalf("replace latest bar: %v", err)
	}

	got, err := store.GetDailyBars(ctx, "302132", DailyBarAdjustedNone, "", "", 1)
	if err != nil {
		t.Fatalf("get bars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].TradeDate != "2026-06-18" || got[0].Close != 59.88 {
		t.Fatalf("unexpected latest bar: %+v", got[0])
	}

	count, earliest, latest, source, lastErr, err := store.GetDailyBarsStats(ctx, "302132", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if count != 2 || earliest != "2026-06-17" || latest != "2026-06-18" || source != "unit_test" || lastErr != "" {
		t.Fatalf("unexpected stats: count=%d earliest=%q latest=%q source=%q lastErr=%q", count, earliest, latest, source, lastErr)
	}
}

func TestMarketDataStoreSelectsOneCompleteBarPerTradeDate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stock_market.duckdb")
	store, err := NewMarketDataStore(path)
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	day := "2026-07-10"
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: "600276", Market: "SH", TradeDate: day, Open: 54.6, High: 57.48, Low: 54.55, Close: 55.75, Volume: 1336189, Amount: 0, Adjusted: DailyBarAdjustedNone, Source: "tencent_fqkline", FetchedAt: time.Now(), Quality: DailyBarQualityOK},
		{Symbol: "600276", Market: "SH", TradeDate: day, Open: 54.6, High: 57.48, Low: 54.55, Close: 55.75, Volume: 133618942, Amount: 7492062000, Adjusted: DailyBarAdjustedNone, Source: "10jqka_kline", FetchedAt: time.Now().Add(-time.Minute), Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert multi-source bars: %v", err)
	}

	got, err := store.GetDailyBars(ctx, "600276", DailyBarAdjustedNone, "", "", 0)
	if err != nil {
		t.Fatalf("get bars: %v", err)
	}
	if len(got) != 1 || got[0].Source != "10jqka_kline" || got[0].Amount != 7492062000 {
		t.Fatalf("canonical bar = %+v, want complete 10jqka bar", got)
	}
	count, _, _, source, _, err := store.GetDailyBarsStats(ctx, "600276", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if count != 1 || source != "10jqka_kline" {
		t.Fatalf("logical stats count=%d source=%q, want 1 and 10jqka_kline", count, source)
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE stockv2_daily_bar_quality
		SET row_count = 2
		WHERE symbol = '600276' AND adjusted = 'none'
	`); err != nil {
		t.Fatalf("poison legacy quality row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM stockv2_market_schema_migrations
		WHERE id = ?
	`, dailyBarLogicalQualityMigration); err != nil {
		t.Fatalf("remove migration marker: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store before migration: %v", err)
	}

	store, err = NewMarketDataStore(path)
	if err != nil {
		t.Fatalf("reopen market store: %v", err)
	}
	defer store.Close()
	count, _, _, source, _, err = store.GetDailyBarsStats(ctx, "600276", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get migrated stats: %v", err)
	}
	if count != 1 || source != "10jqka_kline" {
		t.Fatalf("migrated logical stats count=%d source=%q, want 1 and 10jqka_kline", count, source)
	}
}

func TestNewStoreWithMarketDBMigratesLegacySQLiteDailyBars(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "stockv2.sqlite")
	marketPath := filepath.Join(dir, "stockv2", "stock_market.duckdb")

	legacyDB, err := sql.Open("sqlite3", sqlitePath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE stockv2_daily_bars (
			id TEXT PRIMARY KEY,
			symbol TEXT NOT NULL,
			market TEXT,
			trade_date TEXT NOT NULL,
			open REAL,
			high REAL,
			low REAL,
			close REAL,
			prev_close REAL,
			volume REAL,
			amount REAL,
			pct_change REAL,
			adjusted TEXT NOT NULL DEFAULT 'none',
			source TEXT,
			fetched_at DATETIME,
			quality TEXT,
			error_message TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(symbol, trade_date, adjusted, source)
		);
		INSERT INTO stockv2_daily_bars (
			id, symbol, market, trade_date, open, high, low, close, prev_close,
			volume, amount, pct_change, adjusted, source, fetched_at, quality,
			error_message, created_at, updated_at
		) VALUES (
			'legacy-1', '302132', 'SZ', '2026-06-18', 60.10, 60.60, 59.08, 59.33, 60.20,
			1200, 0, -1.45, 'none', 'legacy_sqlite', '2026-06-18T15:05:00Z', 'ok',
			'', '2026-06-18T15:05:00Z', '2026-06-18T15:05:00Z'
		);
	`); err != nil {
		legacyDB.Close()
		t.Fatalf("seed legacy sqlite: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	store, err := NewStoreWithMarketDB(sqlitePath, marketPath)
	if err != nil {
		t.Fatalf("new stockv2 store: %v", err)
	}
	defer store.Close()

	got, err := store.GetDailyBars(ctx, "302132", DailyBarAdjustedNone, "", "", 10)
	if err != nil {
		t.Fatalf("get migrated bars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Source != "legacy_sqlite" || got[0].Close != 59.33 {
		t.Fatalf("unexpected migrated bar: %+v", got[0])
	}
	if total, err := store.CountDailyBarJobs(ctx); err != nil || total != 0 {
		t.Fatalf("daily bar jobs compatibility total=%d err=%v", total, err)
	}
}

func TestStoreDataAssetsWriteToDuckDB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.sqlite"),
		filepath.Join(dir, "stockv2", "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-1",
		Symbol:         "302132",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "中航成飞",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
	if err := store.UpsertLatestQuote(ctx, StockV2QuoteLatest{
		Symbol:    "302132",
		Market:    "SZ",
		Name:      "中航成飞",
		LastPrice: 59.33,
		Source:    QuoteSourceTencent,
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	if _, err := store.UpsertStockProfile(ctx, StockProfile{
		Symbol:      "302132",
		Market:      "SZ",
		Name:        "中航成飞",
		ProfileText: "航空装备",
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	raw, err := store.CreateRawNews(ctx, StockV2RawNews{
		Source:      "jin10",
		Title:       "航空产业链消息",
		ContentHash: "hash-1",
		DedupeKey:   "jin10:1",
		Quality:     NewsQualityOK,
		Status:      NewsStatusNew,
	})
	if err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	event, err := store.CreateNewsEvent(ctx, NewsEvent{
		RawNewsID:     raw.ID,
		Source:        raw.Source,
		Title:         raw.Title,
		QualityStatus: NewsQualityOK,
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	if _, err := store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		NewsEventID:    event.ID,
		RawNewsID:      raw.ID,
		Symbol:         "302132",
		Market:         "SZ",
		InstrumentName: "中航成飞",
		MatchMethod:    NewsLinkMatchKeyword,
		Score:          0.9,
	}); err != nil {
		t.Fatalf("upsert news link candidate: %v", err)
	}

	for _, table := range []string{
		"stockv2_instruments",
		"stockv2_stock_profiles",
		"stockv2_raw_news",
		"stockv2_news_events",
		"stockv2_news_link_candidates",
	} {
		if got := countRowsForTest(t, store.marketDB.db, table); got != 1 {
			t.Fatalf("duckdb %s count = %d, want 1", table, got)
		}
		if got := countRowsForTest(t, store.db, table); got != 0 {
			t.Fatalf("sqlite %s count = %d, want 0", table, got)
		}
	}
	if got := countRowsForTest(t, store.db, "stockv2_quotes_latest"); got != 1 {
		t.Fatalf("sqlite stockv2_quotes_latest count = %d, want 1", got)
	}
	if got := countRowsForTest(t, store.marketDB.db, "stockv2_quotes_latest"); got != 0 {
		t.Fatalf("duckdb stockv2_quotes_latest count = %d, want 0", got)
	}
}

func countRowsForTest(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
