package stockv2

import (
	"context"
	"fmt"
)

const (
	dailyBarNoTradeStatusVerified         = "verified"
	dailyBarNoTradeStatusLegacyUnverified = "legacy_unverified"
)

func (s *Store) ensureDailyBarCoverageSchema(ctx context.Context) error {
	columns := []struct {
		name    string
		colType string
	}{
		{"source_a", "TEXT NOT NULL DEFAULT ''"},
		{"source_b", "TEXT NOT NULL DEFAULT ''"},
		{"evidence_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"status", "TEXT NOT NULL DEFAULT 'legacy_unverified'"},
		{"expires_at", "DATETIME"},
	}
	for _, column := range columns {
		if err := s.ensureColumn(ctx, "stockv2_daily_bar_gap_checks", column.name, column.colType); err != nil {
			return fmt.Errorf("add daily bar no-trade %s column: %w", column.name, err)
		}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_daily_bar_gap_checks
		SET status = 'legacy_unverified',
		    expires_at = COALESCE(expires_at, checked_at)
		WHERE COALESCE(source_a, '') = '' OR COALESCE(source_b, '') = '';
	`)
	return wrapError(err, "migrate daily bar no-trade coverage")
}
