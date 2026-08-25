package stockv2

import (
	"context"
	"database/sql"
	"fmt"
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
	var memoryLimit string
	if err := store.db.QueryRow(`SELECT current_setting('memory_limit')`).Scan(&memoryLimit); err != nil {
		t.Fatalf("read DuckDB memory setting: %v", err)
	}
	if memoryLimit != "768.0 MiB" {
		t.Fatalf("DuckDB memory_limit = %q, want 768.0 MiB", memoryLimit)
	}
}

func TestEmbeddingVectorSearchHandlesAllowedIDsAcrossBatches(t *testing.T) {
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	asset := EmbeddingAsset{
		VectorRef: "vector-target", ModelID: "model-a",
		ObjectType: EmbeddingObjectNewsThreadVersion, ObjectID: "target",
	}
	if err := store.UpsertEmbeddingVector(ctx, asset, []float64{1, 0, 0}); err != nil {
		t.Fatalf("upsert target vector: %v", err)
	}
	allowed := map[string]struct{}{"target": {}}
	for i := 0; i < 1000; i++ {
		allowed["missing-"+fmt.Sprint(i)] = struct{}{}
	}
	hits, err := store.searchEmbeddingVectors(ctx, asset.ModelID, asset.ObjectType, allowed, []float64{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("search filtered vectors: %v", err)
	}
	if len(hits) != 1 || hits[0].ObjectID != asset.ObjectID {
		t.Fatalf("hits=%+v, want only target", hits)
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

func TestMarketDataStoreBulkDailyBarQualityRefresh(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	bars := make([]StockV2DailyBar, 5500)
	for i := range bars {
		bars[i] = StockV2DailyBar{
			Symbol: fmt.Sprintf("%06d", i), Market: "SZ", TradeDate: "2026-08-24",
			Open: 10, High: 11, Low: 9, Close: 10.5, PrevClose: 10,
			Volume: 1000, Amount: 100000, Adjusted: DailyBarAdjustedNone,
			Source: "bulk_test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
		}
	}
	started := time.Now()
	if err := store.UpsertDailyBars(ctx, bars); err != nil {
		t.Fatalf("bulk upsert daily bars: %v", err)
	}
	var qualityRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_daily_bar_quality`).Scan(&qualityRows); err != nil {
		t.Fatalf("count quality rows: %v", err)
	}
	if qualityRows != len(bars) {
		t.Fatalf("quality rows = %d, want %d", qualityRows, len(bars))
	}
	count, _, latest, source, _, err := store.GetDailyBarsStats(ctx, "005499", DailyBarAdjustedNone)
	if err != nil || count != 1 || latest != "2026-08-24" || source != "bulk_test" {
		t.Fatalf("bulk quality count=%d latest=%q source=%q err=%v", count, latest, source, err)
	}
	t.Logf("5500-row bulk upsert completed in %s", time.Since(started))
}

func TestMarketDataStoreRefreshesQualityForInteriorHistoryGap(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()

	bar := func(day string) StockV2DailyBar {
		return StockV2DailyBar{
			Symbol: "600000", Market: "SH", TradeDate: day,
			Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1000,
			Adjusted: DailyBarAdjustedNone, Source: "gap_test",
			FetchedAt: time.Now(), Quality: DailyBarQualityOK,
		}
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{bar("2026-08-20"), bar("2026-08-22")}); err != nil {
		t.Fatalf("seed bars around gap: %v", err)
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{bar("2026-08-21")}); err != nil {
		t.Fatalf("fill history gap: %v", err)
	}
	count, earliest, latest, _, _, err := store.GetDailyBarsStats(ctx, "600000", DailyBarAdjustedNone)
	if err != nil || count != 3 || earliest != "2026-08-20" || latest != "2026-08-22" {
		t.Fatalf("gap quality count=%d earliest=%q latest=%q err=%v", count, earliest, latest, err)
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
		if tableExistsForTest(t, store.db, table) {
			t.Fatalf("sqlite %s should not exist", table)
		}
	}
	if got := countRowsForTest(t, store.db, "stockv2_quotes_latest"); got != 1 {
		t.Fatalf("sqlite stockv2_quotes_latest count = %d, want 1", got)
	}
	if tableExistsForTest(t, store.marketDB.db, "stockv2_quotes_latest") {
		t.Fatal("duckdb stockv2_quotes_latest should not exist")
	}
}

func tableExistsForTest(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = ?", table).Scan(&count); err == nil {
		return count > 0
	}
	return db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count) == nil && count > 0
}

func countRowsForTest(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
