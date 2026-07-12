package stockv2

import (
	"context"
	"fmt"
)

// ensureAnnouncementSyncIntegritySchema keeps the late-publication verifier
// additive for existing DuckDB assets. Historical rows intentionally remain
// NULL and therefore not ready until the rolling verifier has covered them.
func (s *Store) ensureAnnouncementSyncIntegritySchema(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("ensure announcement sync integrity schema: store is nil")
	}
	columns := []string{
		"late_recheck_started_at",
		"late_recheck_covered_through",
		"last_late_recheck_at",
	}
	if s.marketDB == nil || s.marketDB.db == nil {
		for _, column := range columns {
			if err := s.ensureColumn(ctx, "stockv2_announcement_sync_states", column, "DATETIME"); err != nil {
				return fmt.Errorf("ensure announcement sync %s column: %w", column, err)
			}
		}
		return nil
	}
	for _, column := range columns {
		statement := "ALTER TABLE stockv2_announcement_sync_states ADD COLUMN IF NOT EXISTS " + column + " TIMESTAMP"
		if _, err := s.assetDB().ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure announcement sync integrity schema: %w", err)
		}
	}
	return nil
}
