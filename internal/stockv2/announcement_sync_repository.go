package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) GetAnnouncementSyncState(ctx context.Context, source, market string) (AnnouncementSyncState, bool, error) {
	source = strings.TrimSpace(source)
	market = strings.ToUpper(strings.TrimSpace(market))
	row := s.assetDB().QueryRowContext(ctx, `
		SELECT source, market, covered_through, latest_published_at, last_success_at,
		       last_window_start, last_window_end, last_page_count, last_fetched_count,
		       last_inserted_count, late_recheck_started_at, late_recheck_covered_through,
		       last_late_recheck_at, created_at, updated_at
		FROM stockv2_announcement_sync_states
		WHERE source = ? AND market = ?
	`, source, market)
	state, err := scanAnnouncementSyncState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnnouncementSyncState{Source: source, Market: market}, false, nil
	}
	if err != nil {
		return AnnouncementSyncState{}, false, wrapError(err, "get announcement sync state")
	}
	return state, true, nil
}

// CommitAnnouncementSyncBatch atomically stores all fetched announcements and
// advances every supplied market cursor. A failed page fetch must therefore
// return before this method is called.
func (s *Store) CommitAnnouncementSyncBatch(
	ctx context.Context,
	items []StockV2Announcement,
	states []AnnouncementSyncState,
) ([]StockV2Announcement, error) {
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin announcement sync batch")
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	upsertResult, err := upsertAnnouncementsWithTx(ctx, tx, items, now)
	if err != nil {
		return nil, err
	}
	newItems := upsertResult.Inserted

	insertedByMarket := make(map[string]int, len(states))
	for _, item := range newItems {
		insertedByMarket[strings.ToUpper(strings.TrimSpace(item.Market))]++
	}
	for i := range states {
		state := states[i]
		state.Source = firstNonEmpty(strings.TrimSpace(state.Source), StockV2AnnouncementSourceCninfo)
		state.Market = strings.ToUpper(strings.TrimSpace(state.Market))
		if state.Market == "" || state.CoveredThrough.IsZero() {
			return nil, fmt.Errorf("invalid announcement sync state")
		}
		if state.LastSuccessAt.IsZero() {
			state.LastSuccessAt = now
		}
		if state.CreatedAt.IsZero() {
			state.CreatedAt = now
		}
		state.UpdatedAt = now
		state.LastInsertedCount = insertedByMarket[state.Market]
		_, err = tx.ExecContext(ctx, `
			INSERT INTO stockv2_announcement_sync_states (
				source, market, covered_through, latest_published_at, last_success_at,
				last_window_start, last_window_end, last_page_count, last_fetched_count,
				last_inserted_count, late_recheck_started_at, late_recheck_covered_through,
				last_late_recheck_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, market) DO UPDATE SET
				covered_through = EXCLUDED.covered_through,
				latest_published_at = EXCLUDED.latest_published_at,
				last_success_at = EXCLUDED.last_success_at,
				last_window_start = EXCLUDED.last_window_start,
				last_window_end = EXCLUDED.last_window_end,
				last_page_count = EXCLUDED.last_page_count,
				last_fetched_count = EXCLUDED.last_fetched_count,
				last_inserted_count = EXCLUDED.last_inserted_count,
				late_recheck_started_at = COALESCE(
					stockv2_announcement_sync_states.late_recheck_started_at,
					EXCLUDED.late_recheck_started_at
				),
				late_recheck_covered_through = COALESCE(
					EXCLUDED.late_recheck_covered_through,
					stockv2_announcement_sync_states.late_recheck_covered_through
				),
				last_late_recheck_at = COALESCE(
					EXCLUDED.last_late_recheck_at,
					stockv2_announcement_sync_states.last_late_recheck_at
				),
				updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.covered_through >= stockv2_announcement_sync_states.covered_through
		`, state.Source, state.Market, state.CoveredThrough, nullableTime(state.LatestPublishedAt),
			state.LastSuccessAt, nullableTime(state.LastWindowStart), nullableTime(state.LastWindowEnd),
			state.LastPageCount, state.LastFetchedCount, state.LastInsertedCount,
			nullableTime(state.LateRecheckStartedAt), nullableTime(state.LateRecheckCoveredThrough),
			nullableTime(state.LastLateRecheckAt), state.CreatedAt, state.UpdatedAt)
		if err != nil {
			return nil, wrapError(err, "upsert announcement sync state")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapError(err, "commit announcement sync batch")
	}
	return newItems, nil
}

func scanAnnouncementSyncState(row rowScanner) (AnnouncementSyncState, error) {
	var state AnnouncementSyncState
	var coveredThrough, latestPublishedAt, lastSuccessAt sql.NullTime
	var lastWindowStart, lastWindowEnd sql.NullTime
	var lateRecheckStartedAt, lateRecheckCoveredThrough, lastLateRecheckAt sql.NullTime
	if err := row.Scan(
		&state.Source, &state.Market, &coveredThrough, &latestPublishedAt, &lastSuccessAt,
		&lastWindowStart, &lastWindowEnd, &state.LastPageCount, &state.LastFetchedCount,
		&state.LastInsertedCount, &lateRecheckStartedAt, &lateRecheckCoveredThrough,
		&lastLateRecheckAt, &state.CreatedAt, &state.UpdatedAt,
	); err != nil {
		return AnnouncementSyncState{}, err
	}
	if coveredThrough.Valid {
		state.CoveredThrough = coveredThrough.Time
	}
	if latestPublishedAt.Valid {
		state.LatestPublishedAt = latestPublishedAt.Time
	}
	if lastSuccessAt.Valid {
		state.LastSuccessAt = lastSuccessAt.Time
	}
	if lastWindowStart.Valid {
		state.LastWindowStart = lastWindowStart.Time
	}
	if lastWindowEnd.Valid {
		state.LastWindowEnd = lastWindowEnd.Time
	}
	if lateRecheckStartedAt.Valid {
		state.LateRecheckStartedAt = lateRecheckStartedAt.Time
	}
	if lateRecheckCoveredThrough.Valid {
		state.LateRecheckCoveredThrough = lateRecheckCoveredThrough.Time
	}
	if lastLateRecheckAt.Valid {
		state.LastLateRecheckAt = lastLateRecheckAt.Time
	}
	return state, nil
}
