package stockv2

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"phantom-lancer/internal/safelog"
)

type dailyBarFlowRepairState struct {
	Symbol        string
	AttemptCount  int
	NextRetryAt   time.Time
	LastAttemptAt time.Time
	LastError     string
}

func (s *Store) ListDailyBarFlowGapCoverage(ctx context.Context, limit int) ([]DailyBarCoverageQuality, error) {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	rows, err := s.marketDB.db.QueryContext(ctx, `
		SELECT symbol, adjusted, instrument_type, window_start_date, window_end_date,
		       expected_date_count, covered_date_count, date_gap_count, core_gap_count,
		       flow_gap_count, verified_no_trade_count, COALESCE(expected_latest_date, ''),
		       checked_at
		FROM stockv2_daily_bar_coverage_quality
		WHERE instrument_type = 'stock' AND flow_gap_count > 0
		ORDER BY checked_at, symbol
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, wrapError(err, "list daily bar flow gap coverage")
	}
	defer rows.Close()
	items := make([]DailyBarCoverageQuality, 0)
	for rows.Next() {
		var item DailyBarCoverageQuality
		if err := rows.Scan(
			&item.Symbol, &item.Adjusted, &item.InstrumentType,
			&item.WindowStartDate, &item.WindowEndDate,
			&item.ExpectedDateCount, &item.CoveredDateCount,
			&item.DateGapCount, &item.CoreGapCount, &item.FlowGapCount,
			&item.VerifiedNoTradeCount, &item.ExpectedLatestDate, &item.CheckedAt,
		); err != nil {
			return nil, wrapError(err, "scan daily bar flow gap coverage")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate daily bar flow gap coverage")
}

func (s *Store) ListDailyBarFlowRepairStates(ctx context.Context) (map[string]dailyBarFlowRepairState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol, attempt_count, next_retry_at, last_attempt_at, COALESCE(last_error, '')
		FROM stockv2_daily_bar_flow_repair_states
	`)
	if err != nil {
		return nil, wrapError(err, "list daily bar flow repair states")
	}
	defer rows.Close()
	out := make(map[string]dailyBarFlowRepairState)
	for rows.Next() {
		var item dailyBarFlowRepairState
		var nextRetryAt, lastAttemptAt sql.NullTime
		if err := rows.Scan(&item.Symbol, &item.AttemptCount, &nextRetryAt, &lastAttemptAt, &item.LastError); err != nil {
			return nil, wrapError(err, "scan daily bar flow repair state")
		}
		if nextRetryAt.Valid {
			item.NextRetryAt = nextRetryAt.Time
		}
		if lastAttemptAt.Valid {
			item.LastAttemptAt = lastAttemptAt.Time
		}
		out[item.Symbol] = item
	}
	return out, wrapError(rows.Err(), "iterate daily bar flow repair states")
}

func (s *Store) UpsertDailyBarFlowRepairState(ctx context.Context, item dailyBarFlowRepairState, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_daily_bar_flow_repair_states (
			symbol, attempt_count, next_retry_at, last_attempt_at, last_error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			attempt_count = excluded.attempt_count,
			next_retry_at = excluded.next_retry_at,
			last_attempt_at = excluded.last_attempt_at,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, item.Symbol, item.AttemptCount, nullableTime(item.NextRetryAt), nullableTime(item.LastAttemptAt),
		safelog.Text(item.LastError, 400), now)
	return wrapError(err, "upsert daily bar flow repair state")
}

func (s *Store) DeleteDailyBarFlowRepairState(ctx context.Context, symbol string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_daily_bar_flow_repair_states WHERE symbol = ?`, symbol)
	return wrapError(err, "delete daily bar flow repair state")
}
