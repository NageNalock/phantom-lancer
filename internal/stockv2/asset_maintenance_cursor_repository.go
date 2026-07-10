package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const assetMaintenanceUniverseCursorScope = "stockv2_universe"
const assetMaintenancePriorityCursorScope = "stockv2_priority"

const assetUniverseDiscoveryCursorScope = "stockv2_universe_discovery"

func (s *Store) GetAssetMaintenanceCursor(ctx context.Context, scope string) (string, error) {
	var symbol string
	err := s.db.QueryRowContext(ctx, `
		SELECT cursor_symbol FROM stockv2_asset_maintenance_cursors WHERE scope = ?
	`, strings.TrimSpace(scope)).Scan(&symbol)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", wrapError(err, "get asset maintenance cursor")
	}
	return strings.TrimSpace(symbol), nil
}

func (s *Store) SetAssetMaintenanceCursor(ctx context.Context, scope, symbol string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_asset_maintenance_cursors (scope, cursor_symbol, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(scope) DO UPDATE SET cursor_symbol = excluded.cursor_symbol, updated_at = excluded.updated_at
	`, strings.TrimSpace(scope), strings.TrimSpace(symbol), time.Now())
	return wrapError(err, "set asset maintenance cursor")
}

func (s *Store) GetAssetMaintenanceCursorUpdatedAt(ctx context.Context, scope string) (time.Time, error) {
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT updated_at FROM stockv2_asset_maintenance_cursors WHERE scope = ?
	`, strings.TrimSpace(scope)).Scan(&updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, wrapError(err, "get asset maintenance cursor updated at")
	}
	return updatedAt, nil
}

func (s *Store) CacheDiscoveredUniverseSymbols(ctx context.Context, source string, symbols []string) error {
	symbols = compactStringList(symbols, len(symbols))
	if len(symbols) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin cache discovered universe symbols")
	}
	defer tx.Rollback()
	now := time.Now()
	for _, symbol := range symbols {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stockv2_universe_discovery_symbols (symbol, source, first_seen_at, last_seen_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(symbol) DO UPDATE SET source = excluded.source, last_seen_at = excluded.last_seen_at
		`, symbol, strings.TrimSpace(source), now, now); err != nil {
			return wrapError(err, "cache discovered universe symbol")
		}
	}
	return wrapError(tx.Commit(), "commit discovered universe symbols")
}

func (s *Store) ListDiscoveredUniverseSymbols(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol FROM stockv2_universe_discovery_symbols ORDER BY symbol`)
	if err != nil {
		return nil, wrapError(err, "list discovered universe symbols")
	}
	return scanStrings(rows, "scan discovered universe symbol", "iterate discovered universe symbols")
}

// ListAssetMaintenancePrioritySymbols is a single local scan for assets whose
// deterministic data is absent or stale. AI queue state is intentionally not
// part of this query: the durable AI queue owns its own retry lifecycle.
func (s *Store) ListAssetMaintenancePrioritySymbols(ctx context.Context, baseStaleBefore time.Time, dailyStaleBefore string) ([]string, error) {
	rows, err := s.assetDB().QueryContext(ctx, `
		SELECT i.symbol
		FROM stockv2_instruments i
		LEFT JOIN stockv2_stock_profiles p ON p.symbol = i.symbol
		LEFT JOIN stockv2_daily_bar_quality q ON q.symbol = i.symbol AND q.adjusted = 'none'
		WHERE p.symbol IS NULL
		   OR COALESCE(p.base_profile_checked_at, p.base_profile_updated_at) IS NULL
		   OR COALESCE(p.base_profile_checked_at, p.base_profile_updated_at) < ?
		   OR q.symbol IS NULL
		   OR q.row_count < 250
		   OR COALESCE(q.incomplete_count, q.row_count) > 0
		   OR COALESCE(q.latest_date, '') < ?
		ORDER BY
			CASE WHEN p.symbol IS NULL OR q.symbol IS NULL THEN 0 ELSE 1 END,
			i.symbol
	`, baseStaleBefore, dailyStaleBefore)
	if err != nil {
		return nil, wrapError(err, "list priority asset maintenance symbols")
	}
	return scanStrings(rows, "scan priority asset maintenance symbol", "iterate priority asset maintenance symbols")
}

func rotateSymbolsAfterCursor(symbols []string, cursor string) []string {
	if len(symbols) == 0 || strings.TrimSpace(cursor) == "" {
		return symbols
	}
	index := 0
	for i, symbol := range symbols {
		if symbol > cursor {
			index = i
			break
		}
		if i == len(symbols)-1 {
			index = len(symbols)
		}
	}
	out := make([]string, 0, len(symbols))
	out = append(out, symbols[index:]...)
	out = append(out, symbols[:index]...)
	return out
}

func rotateSymbolsAfterExactCursor(symbols []string, cursor string) []string {
	if len(symbols) == 0 || strings.TrimSpace(cursor) == "" {
		return symbols
	}
	for i, symbol := range symbols {
		if symbol != cursor {
			continue
		}
		out := make([]string, 0, len(symbols))
		out = append(out, symbols[i+1:]...)
		out = append(out, symbols[:i+1]...)
		return out
	}
	return symbols
}
