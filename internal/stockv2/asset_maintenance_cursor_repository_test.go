package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateSymbolsAfterCursorWraps(t *testing.T) {
	got := rotateSymbolsAfterCursor([]string{"000001", "000002", "000003"}, "000002")
	want := []string{"000003", "000001", "000002"}
	if len(got) != len(want) {
		t.Fatalf("rotated = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rotated = %#v, want %#v", got, want)
		}
	}
}

func TestAssetMaintenanceCursorPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stockv2.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAssetMaintenanceCursor(context.Background(), assetMaintenanceUniverseCursorScope, "300750"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.GetAssetMaintenanceCursor(context.Background(), assetMaintenanceUniverseCursorScope)
	if err != nil {
		t.Fatal(err)
	}
	if got != "300750" {
		t.Fatalf("cursor = %q, want 300750", got)
	}
}

func TestSelectAssetMaintenanceSymbolsHasNoImplicitFiveThousandCap(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now()
	_, err = store.marketDB.db.ExecContext(ctx, `
		INSERT INTO stockv2_instruments (
			id, symbol, market, instrument_type, name, industry, sector,
			concepts, list_date, delist_date, last_update_at, created_at, updated_at
		)
		SELECT
			'id-' || CAST(i AS VARCHAR),
			LPAD(CAST(100000 + i AS VARCHAR), 6, '0'),
			'SZ', 'stock', 'test', '', '', '[]', '', '', ?, ?, ?
		FROM range(0, 5001) AS t(i)
	`, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	svc.universeSource = nil
	got, err := svc.selectAssetMaintenanceSymbols(ctx, UniverseUpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 5001 {
		t.Fatalf("selected symbols = %d, want at least 5001", len(got))
	}
}

func TestSelectAssetMaintenanceSymbolsReservesCursorCapacity(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now()
	_, err = store.marketDB.db.ExecContext(ctx, `
		INSERT INTO stockv2_instruments (
			id, symbol, market, instrument_type, name, industry, sector,
			concepts, list_date, delist_date, last_update_at, created_at, updated_at
		)
		SELECT
			'id-' || CAST(i AS VARCHAR),
			LPAD(CAST(100000 + i AS VARCHAR), 6, '0'),
			'SZ', 'stock', 'test', '', '', '[]', '', '', ?, ?, ?
		FROM range(0, 6) AS t(i)
	`, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	svc.universeSource = nil
	first, err := svc.selectAssetMaintenanceSymbols(ctx, UniverseUpdateRequest{MaxSymbols: 4})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.selectAssetMaintenanceSymbols(ctx, UniverseUpdateRequest{MaxSymbols: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 4 || len(second) != 4 || second[3] == first[3] {
		t.Fatalf("first=%v second=%v, want one reserved cursor slot to rotate", first, second)
	}
}

func TestSelectAssetMaintenanceSymbolsHonorsSingleSymbolLimit(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now()
	_, err = store.marketDB.db.ExecContext(ctx, `
		INSERT INTO stockv2_instruments (
			id, symbol, market, instrument_type, name, industry, sector,
			concepts, list_date, delist_date, last_update_at, created_at, updated_at
		)
		SELECT
			'id-' || CAST(i AS VARCHAR),
			LPAD(CAST(100000 + i AS VARCHAR), 6, '0'),
			'SZ', 'stock', 'test', '', '', '[]', '', '', ?, ?, ?
		FROM range(0, 6) AS t(i)
	`, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	svc.universeSource = nil
	got, err := svc.selectAssetMaintenanceSymbols(ctx, UniverseUpdateRequest{MaxSymbols: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("selected symbols = %v, want exactly one", got)
	}
}

func TestAssetUniverseDiscoveryFallbackUsesShortRetry(t *testing.T) {
	if got := assetUniverseDiscoveryRefreshIntervalFor("fallback:65", 65); got != assetUniverseDiscoveryFallbackRetryInterval {
		t.Fatalf("fallback interval = %v, want %v", got, assetUniverseDiscoveryFallbackRetryInterval)
	}
	if got := assetUniverseDiscoveryRefreshIntervalFor("65", 65); got != assetUniverseDiscoveryFallbackRetryInterval {
		t.Fatalf("legacy fallback interval = %v, want %v", got, assetUniverseDiscoveryFallbackRetryInterval)
	}
	if got := assetUniverseDiscoveryRefreshIntervalFor("full:5200", 5200); got != assetUniverseDiscoveryRefreshInterval {
		t.Fatalf("full interval = %v, want %v", got, assetUniverseDiscoveryRefreshInterval)
	}
}

func TestAssetMaintenancePriorityUsesBaseProfileCheckTime(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "instrument-600000",
		Symbol:         "600000",
		Market:         "SH",
		InstrumentType: InstrumentTypeStock,
		Name:           "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertStockProfile(ctx, StockProfile{
		Symbol:               "600000",
		Market:               "SH",
		InstrumentType:       InstrumentTypeStock,
		Name:                 "test",
		BaseProfileUpdatedAt: now.Add(-30 * 24 * time.Hour),
		BaseProfileCheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.marketDB.db.ExecContext(ctx, `
		INSERT INTO stockv2_daily_bar_quality (
			symbol, adjusted, row_count, incomplete_count, earliest_date,
			latest_date, source, last_error, updated_at
		) VALUES (?, 'none', 250, 0, ?, ?, 'test', '', ?)
	`, "600000", now.AddDate(-1, 0, 0).Format("2006-01-02"), now.Format("2006-01-02"), now); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListAssetMaintenancePrioritySymbols(ctx, now.Add(-7*24*time.Hour), now.AddDate(0, 0, -7).Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("priority symbols = %v, want recent base check to suppress stale content-version priority", got)
	}
}

func TestSelectAssetMaintenanceSymbolsRotatesDiscoveredSymbolsNotYetStored(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.CacheDiscoveredUniverseSymbols(ctx, "test", []string{
		"000001", "000002", "000003", "000004", "000005", "000006",
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	svc.universeSource = nil
	first, err := svc.selectAssetMaintenanceSymbols(ctx, UniverseUpdateRequest{MaxSymbols: 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.selectAssetMaintenanceSymbols(ctx, UniverseUpdateRequest{MaxSymbols: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || len(second) != 3 || first[0] == second[0] {
		t.Fatalf("first=%v second=%v, want persistent rotation across discovered-only symbols", first, second)
	}
}
