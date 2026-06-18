package stockv2

import (
	"context"
	"database/sql"
	"path/filepath"
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
