package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Store) UpsertLatestQuote(ctx context.Context, quote StockV2QuoteLatest) error {
	query := `
		INSERT INTO stockv2_quotes_latest (
			symbol, market, name, last_price, prev_close, open_price, high_price,
			low_price, volume, amount, pct_change, quote_at, fetched_at, source,
			status, error_message, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			market = excluded.market,
			name = excluded.name,
			last_price = excluded.last_price,
			prev_close = excluded.prev_close,
			open_price = excluded.open_price,
			high_price = excluded.high_price,
			low_price = excluded.low_price,
			volume = excluded.volume,
			amount = excluded.amount,
			pct_change = excluded.pct_change,
			quote_at = excluded.quote_at,
			fetched_at = excluded.fetched_at,
			source = excluded.source,
			status = excluded.status,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`

	now := time.Now()
	if quote.FetchedAt.IsZero() {
		quote.FetchedAt = now
	}
	if quote.QuoteAt.IsZero() {
		quote.QuoteAt = quote.FetchedAt
	}
	if quote.Status == "" {
		quote.Status = QuoteStatusFresh
	}

	_, err := s.assetDB().ExecContext(ctx, query,
		quote.Symbol,
		quote.Market,
		quote.Name,
		quote.LastPrice,
		quote.PrevClose,
		quote.OpenPrice,
		quote.HighPrice,
		quote.LowPrice,
		quote.Volume,
		quote.Amount,
		quote.PctChange,
		quote.QuoteAt,
		quote.FetchedAt,
		quote.Source,
		quote.Status,
		quote.ErrorMessage,
		now,
		now,
	)
	return wrapError(err, "upsert latest quote")
}

func (s *Store) GetLatestQuotes(ctx context.Context, symbols []string) ([]StockV2QuoteLatest, error) {
	if len(symbols) == 0 {
		return []StockV2QuoteLatest{}, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(symbols)), ",")
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}

	query := fmt.Sprintf(`
		SELECT symbol, market, COALESCE(name,''), last_price, prev_close, open_price,
		       high_price, low_price, volume, amount, pct_change, quote_at, fetched_at,
		       source, status, COALESCE(error_message,'')
		FROM stockv2_quotes_latest
		WHERE symbol IN (%s)
	`, placeholders)

	rows, err := s.assetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "get latest quotes")
	}
	defer rows.Close()

	bySymbol := make(map[string]StockV2QuoteLatest, len(symbols))
	for rows.Next() {
		quote, err := scanLatestQuote(rows)
		if err != nil {
			return nil, wrapError(err, "scan latest quote")
		}
		bySymbol[quote.Symbol] = quote
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate latest quotes")
	}

	items := make([]StockV2QuoteLatest, 0, len(bySymbol))
	for _, symbol := range symbols {
		if quote, ok := bySymbol[symbol]; ok {
			items = append(items, quote)
		}
	}
	return items, nil
}

func (s *Store) MarkLatestQuoteFailed(ctx context.Context, symbol, reason string) (StockV2QuoteLatest, bool, error) {
	reason = safelog.Text(reason, 240)
	quote, err := s.getLatestQuote(ctx, symbol)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2QuoteLatest{}, false, nil
		}
		return StockV2QuoteLatest{}, false, wrapError(err, "get latest quote before failed mark")
	}

	now := time.Now()
	_, err = s.assetDB().ExecContext(ctx, `
		UPDATE stockv2_quotes_latest
		SET status = ?, error_message = ?, updated_at = ?
		WHERE symbol = ?
	`, QuoteStatusFailed, reason, now, symbol)
	if err != nil {
		return StockV2QuoteLatest{}, false, wrapError(err, "mark latest quote failed")
	}

	quote.Status = QuoteStatusFailed
	quote.ErrorMessage = reason
	return quote, true, nil
}

func (s *Store) UpsertQuoteRefreshStatus(ctx context.Context, status QuoteRefreshStatus) error {
	if strings.TrimSpace(status.Symbol) == "" {
		return nil
	}
	now := time.Now()
	if status.LastAttemptAt.IsZero() {
		status.LastAttemptAt = now
	}
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = now
	}
	if status.Source == "" {
		status.Source = QuoteSourceTencent
	}
	status.ErrorMessage = safelog.Text(status.ErrorMessage, 240)
	failures := 0
	if status.Status == QuoteStatusFailed {
		failures = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_quote_refresh_statuses
			(symbol, market, source, status, last_attempt_at, last_success_at, last_failure_at,
			 error_message, consecutive_failures, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			market = CASE WHEN excluded.market IS NULL OR excluded.market = '' THEN stockv2_quote_refresh_statuses.market ELSE excluded.market END,
			source = CASE WHEN excluded.source IS NULL OR excluded.source = '' THEN stockv2_quote_refresh_statuses.source ELSE excluded.source END,
			status = excluded.status,
			last_attempt_at = excluded.last_attempt_at,
			last_success_at = COALESCE(excluded.last_success_at, stockv2_quote_refresh_statuses.last_success_at),
			last_failure_at = COALESCE(excluded.last_failure_at, stockv2_quote_refresh_statuses.last_failure_at),
			error_message = excluded.error_message,
			consecutive_failures = CASE
				WHEN excluded.status = ? THEN stockv2_quote_refresh_statuses.consecutive_failures + 1
				ELSE 0
			END,
			updated_at = excluded.updated_at
	`,
		status.Symbol,
		nullableMonitorString(status.Market),
		nullableMonitorString(status.Source),
		status.Status,
		status.LastAttemptAt,
		nullableMonitorTime(status.LastSuccessAt),
		nullableMonitorTime(status.LastFailureAt),
		nullableMonitorString(status.ErrorMessage),
		failures,
		status.UpdatedAt,
		QuoteStatusFailed,
	)
	return wrapError(err, "upsert quote refresh status")
}

func (s *Store) UpsertQuoteRefreshTaskState(ctx context.Context, state QuoteRefreshTaskState) error {
	if strings.TrimSpace(state.TaskType) == "" {
		return nil
	}
	now := time.Now()
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = now
	}
	state.ErrorMessage = safelog.Text(state.ErrorMessage, 240)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_quote_refresh_task_state
			(task_type, status, trigger_type, started_at, finished_at, scope_summary,
			 scanned_count, success_count, failed_count, error_message, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_type) DO UPDATE SET
			status = excluded.status,
			trigger_type = excluded.trigger_type,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			scope_summary = excluded.scope_summary,
			scanned_count = excluded.scanned_count,
			success_count = excluded.success_count,
			failed_count = excluded.failed_count,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`,
		state.TaskType,
		state.Status,
		nullableMonitorString(state.TriggerType),
		nullableMonitorTime(state.StartedAt),
		nullableMonitorTime(state.FinishedAt),
		nullableMonitorString(state.ScopeSummary),
		state.ScannedCount,
		state.SuccessCount,
		state.FailedCount,
		nullableMonitorString(state.ErrorMessage),
		state.UpdatedAt,
	)
	return wrapError(err, "upsert quote refresh task state")
}

func (s *Store) GetQuoteRefreshTaskState(ctx context.Context, taskType string) (*QuoteRefreshTaskState, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT task_type, status, COALESCE(trigger_type,''), started_at, finished_at,
		       COALESCE(scope_summary,''), scanned_count, success_count, failed_count,
		       COALESCE(error_message,''), updated_at
		FROM stockv2_quote_refresh_task_state
		WHERE task_type = ?
	`, taskType)
	state, err := scanQuoteRefreshTaskState(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, wrapError(err, "get quote refresh task state")
	}
	return &state, nil
}

func (s *Store) ListQuoteRefreshStatuses(ctx context.Context, limit int) ([]QuoteRefreshStatus, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol, COALESCE(market,''), COALESCE(source,''), status,
		       last_attempt_at, last_success_at, last_failure_at,
		       COALESCE(error_message,''), consecutive_failures, updated_at
		FROM stockv2_quote_refresh_statuses
		ORDER BY updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, wrapError(err, "list quote refresh statuses")
	}
	defer rows.Close()

	items := make([]QuoteRefreshStatus, 0)
	for rows.Next() {
		item, err := scanQuoteRefreshStatus(rows)
		if err != nil {
			return nil, wrapError(err, "scan quote refresh status")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate quote refresh statuses")
	}
	return items, nil
}

func (s *Store) getLatestQuote(ctx context.Context, symbol string) (StockV2QuoteLatest, error) {
	row := s.assetDB().QueryRowContext(ctx, `
		SELECT symbol, market, COALESCE(name,''), last_price, prev_close, open_price,
		       high_price, low_price, volume, amount, pct_change, quote_at, fetched_at,
		       source, status, COALESCE(error_message,'')
		FROM stockv2_quotes_latest
		WHERE symbol = ?
	`, symbol)
	return scanLatestQuote(row)
}

func scanQuoteRefreshTaskState(row rowScanner) (QuoteRefreshTaskState, error) {
	var state QuoteRefreshTaskState
	var startedAt, finishedAt sql.NullTime
	var triggerType, scopeSummary, errorMessage sql.NullString
	if err := row.Scan(
		&state.TaskType,
		&state.Status,
		&triggerType,
		&startedAt,
		&finishedAt,
		&scopeSummary,
		&state.ScannedCount,
		&state.SuccessCount,
		&state.FailedCount,
		&errorMessage,
		&state.UpdatedAt,
	); err != nil {
		return state, err
	}
	state.TriggerType = triggerType.String
	if startedAt.Valid {
		state.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		state.FinishedAt = finishedAt.Time
	}
	state.ScopeSummary = scopeSummary.String
	state.ErrorMessage = errorMessage.String
	return state, nil
}

func scanQuoteRefreshStatus(row rowScanner) (QuoteRefreshStatus, error) {
	var status QuoteRefreshStatus
	var lastSuccessAt, lastFailureAt sql.NullTime
	if err := row.Scan(
		&status.Symbol,
		&status.Market,
		&status.Source,
		&status.Status,
		&status.LastAttemptAt,
		&lastSuccessAt,
		&lastFailureAt,
		&status.ErrorMessage,
		&status.ConsecutiveFailures,
		&status.UpdatedAt,
	); err != nil {
		return status, err
	}
	if lastSuccessAt.Valid {
		status.LastSuccessAt = lastSuccessAt.Time
	}
	if lastFailureAt.Valid {
		status.LastFailureAt = lastFailureAt.Time
	}
	return status, nil
}

func scanLatestQuote(row rowScanner) (StockV2QuoteLatest, error) {
	var quote StockV2QuoteLatest
	err := row.Scan(
		&quote.Symbol,
		&quote.Market,
		&quote.Name,
		&quote.LastPrice,
		&quote.PrevClose,
		&quote.OpenPrice,
		&quote.HighPrice,
		&quote.LowPrice,
		&quote.Volume,
		&quote.Amount,
		&quote.PctChange,
		&quote.QuoteAt,
		&quote.FetchedAt,
		&quote.Source,
		&quote.Status,
		&quote.ErrorMessage,
	)
	return quote, err
}
