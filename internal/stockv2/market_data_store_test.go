package stockv2

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestMarketDataStoreGetDailyBarsDedupesSources(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	fetchedAt := time.Date(2026, 7, 9, 15, 5, 0, 0, time.UTC)
	bars := []StockV2DailyBar{
		{
			Symbol:    "002457",
			Market:    "SZ",
			TradeDate: "2026-07-09",
			Open:      15.16,
			High:      15.37,
			Low:       14.59,
			Close:     15.06,
			Volume:    632970,
			Adjusted:  DailyBarAdjustedNone,
			Source:    "tencent_fqkline",
			FetchedAt: fetchedAt,
			Quality:   DailyBarQualityOK,
		},
		{
			Symbol:       "002457",
			Market:       "SZ",
			TradeDate:    "2026-07-09",
			Open:         15.16,
			High:         15.37,
			Low:          14.59,
			Close:        15.06,
			Volume:       63297016,
			Amount:       946834366,
			TurnoverRate: 18.99,
			Adjusted:     DailyBarAdjustedNone,
			Source:       "baidu_kline",
			FetchedAt:    fetchedAt.Add(-time.Minute),
			Quality:      DailyBarQualityOK,
		},
		{
			Symbol:    "002457",
			Market:    "SZ",
			TradeDate: "2026-07-08",
			Open:      14.93,
			High:      16.3,
			Low:       14.14,
			Close:     15.48,
			Volume:    87073222,
			Amount:    1313594073,
			Adjusted:  DailyBarAdjustedNone,
			Source:    "baidu_kline",
			FetchedAt: fetchedAt,
			Quality:   DailyBarQualityOK,
		},
	}
	if err := store.UpsertDailyBars(ctx, bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	got, err := store.GetDailyBars(ctx, "002457", DailyBarAdjustedNone, "", "", 0)
	if err != nil {
		t.Fatalf("get bars: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[1].TradeDate != "2026-07-09" || got[1].Source != "baidu_kline" || got[1].Amount == 0 {
		t.Fatalf("unexpected deduped latest bar: %+v", got[1])
	}

	count, earliest, latest, source, _, err := store.GetDailyBarsStats(ctx, "002457", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if count != 2 || earliest != "2026-07-08" || latest != "2026-07-09" || source != "baidu_kline" {
		t.Fatalf("unexpected stats: count=%d earliest=%q latest=%q source=%q", count, earliest, latest, source)
	}

	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{{
		Symbol:    "002457",
		Market:    "SZ",
		TradeDate: "2026-07-10",
		Open:      15.06,
		High:      15.2,
		Low:       14.9,
		Close:     15.1,
		Volume:    100,
		Adjusted:  DailyBarAdjustedNone,
		Source:    "tencent_fqkline",
		FetchedAt: fetchedAt,
		Quality:   DailyBarQualityOK,
	}}); err != nil {
		t.Fatalf("upsert tencent-only bar: %v", err)
	}
	dates, err := store.GetDailyBarDates(ctx, "002457", DailyBarAdjustedNone, "2026-07-08", "2026-07-10")
	if err != nil {
		t.Fatalf("get dates: %v", err)
	}
	if strings.Join(dates, ",") != "2026-07-08,2026-07-09,2026-07-10" {
		t.Fatalf("dates = %v, want Tencent-only dates to count as covered", dates)
	}
	count, earliest, latest, source, _, err = store.GetDailyBarsStats(ctx, "002457", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("stats after Tencent-only bar: %v", err)
	}
	if count != 3 || earliest != "2026-07-08" || latest != "2026-07-10" || source != "tencent_fqkline" {
		t.Fatalf("unexpected stats after Tencent-only bar: count=%d earliest=%q latest=%q source=%q", count, earliest, latest, source)
	}
}

func TestMarketDataStorePartialDailyBarRemainsRepairable(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	partial := StockV2DailyBar{
		Symbol: "600000", Market: "SH", TradeDate: "2026-07-09",
		Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100,
		Amount: 1_000, AmountPresent: true, TurnoverRate: 1.2, TurnoverRatePresent: true,
		Adjusted: DailyBarAdjustedNone, Source: "10jqka_kline", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{partial}); err != nil {
		t.Fatal(err)
	}
	completeDates, err := store.GetCompleteDailyBarDates(ctx, "600000", DailyBarAdjustedNone, "2026-07-09", "2026-07-09", true)
	if err != nil || len(completeDates) != 0 {
		t.Fatalf("partial dates=%v err=%v, want repairable gap", completeDates, err)
	}
	stats, err := store.GetDailyBarsStatsDetailed(ctx, "600000", DailyBarAdjustedNone)
	if err != nil || stats.IncompleteCount != 1 {
		t.Fatalf("partial stats=%+v err=%v", stats, err)
	}

	partial.NetInflowPresent = true
	partial.MainNetInflowPresent = true
	partial.FetchedAt = partial.FetchedAt.Add(time.Minute)
	partial.Source = "10jqka_kline_repaired"
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{partial}); err != nil {
		t.Fatal(err)
	}
	completeDates, err = store.GetCompleteDailyBarDates(ctx, "600000", DailyBarAdjustedNone, "2026-07-09", "2026-07-09", true)
	if err != nil || strings.Join(completeDates, ",") != "2026-07-09" {
		t.Fatalf("repaired dates=%v err=%v", completeDates, err)
	}
	stats, err = store.GetDailyBarsStatsDetailed(ctx, "600000", DailyBarAdjustedNone)
	if err != nil || stats.IncompleteCount != 0 {
		t.Fatalf("repaired stats=%+v err=%v", stats, err)
	}
}

func TestMarketDataStoreRebuildsDailyBarQualityOnOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stock_market.duckdb")
	store, err := NewMarketDataStore(path)
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{{
		Symbol:    "600000",
		Market:    "SH",
		TradeDate: "2026-07-09",
		Open:      8.9,
		High:      9.1,
		Low:       8.8,
		Close:     9,
		Volume:    100,
		Adjusted:  DailyBarAdjustedNone,
		Source:    "tencent_fqkline",
		FetchedAt: time.Now(),
		Quality:   DailyBarQualityOK,
	}}); err != nil {
		t.Fatalf("upsert Tencent bar: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE stockv2_daily_bar_quality
		SET row_count = 0, earliest_date = '', latest_date = '', source = ''
		WHERE symbol = '600000' AND adjusted = 'none'
	`); err != nil {
		t.Fatalf("seed stale quality: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close market store: %v", err)
	}

	reopened, err := NewMarketDataStore(path)
	if err != nil {
		t.Fatalf("reopen market store: %v", err)
	}
	defer reopened.Close()
	count, earliest, latest, source, _, err := reopened.GetDailyBarsStats(ctx, "600000", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("stats after reopen: %v", err)
	}
	if count != 1 || earliest != "2026-07-09" || latest != "2026-07-09" || source != "tencent_fqkline" {
		t.Fatalf("rebuilt stats = %d/%s/%s/%s", count, earliest, latest, source)
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

func TestMarketDataStoreRemovesLegacyInstrumentStatus(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stock_market.duckdb")
	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE stockv2_instruments (
			id VARCHAR,
			symbol VARCHAR NOT NULL UNIQUE,
			market VARCHAR NOT NULL,
			instrument_type VARCHAR NOT NULL DEFAULT 'stock',
			name VARCHAR,
			industry VARCHAR,
			sector VARCHAR,
			concepts VARCHAR,
			list_date VARCHAR,
			delist_date VARCHAR,
			status VARCHAR DEFAULT 'active',
			last_update_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(id)
		);
		CREATE INDEX idx_stockv2_market_instruments_status ON stockv2_instruments(status);
		INSERT INTO stockv2_instruments (id, symbol, market, instrument_type, name, status, created_at, updated_at)
		VALUES ('inst-1', '000001', 'SZ', 'stock', '平安银行', 'active', now(), now());
	`)
	if closeErr := db.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("seed legacy duckdb instrument schema: %v", err)
	}

	store, err := NewMarketDataStore(path)
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	if duckDBColumnExists(t, store.db, "stockv2_instruments", "status") {
		t.Fatal("duckdb stockv2_instruments.status legacy column was not removed")
	}
	var name string
	if err := store.db.QueryRowContext(ctx, `SELECT name FROM stockv2_instruments WHERE symbol = '000001'`).Scan(&name); err != nil {
		t.Fatalf("query migrated duckdb instrument: %v", err)
	}
	if name != "平安银行" {
		t.Fatalf("migrated duckdb instrument name = %q", name)
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

func duckDBColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_name = ? AND column_name = ?
	`, table, column).Scan(&count); err != nil {
		t.Fatalf("check duckdb column %s.%s: %v", table, column, err)
	}
	return count > 0
}
