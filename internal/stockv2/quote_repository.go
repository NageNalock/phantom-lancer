package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Store) UpsertLatestQuote(ctx context.Context, quote StockV2QuoteLatest) error {
	query := `
		INSERT INTO stockv2_quotes_latest (
			symbol, market, name, last_price, prev_close, open_price, high_price,
			low_price, volume, amount, pct_change, amplitude, turnover_rate,
			volume_ratio, main_net_inflow, super_net_inflow, large_net_inflow,
			medium_net_inflow, small_net_inflow, main_net_inflow_pct,
			quote_at, fetched_at, source, status, error_message, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			amplitude = excluded.amplitude,
			turnover_rate = excluded.turnover_rate,
			volume_ratio = excluded.volume_ratio,
			main_net_inflow = excluded.main_net_inflow,
			super_net_inflow = excluded.super_net_inflow,
			large_net_inflow = excluded.large_net_inflow,
			medium_net_inflow = excluded.medium_net_inflow,
			small_net_inflow = excluded.small_net_inflow,
			main_net_inflow_pct = excluded.main_net_inflow_pct,
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
		quote.Amplitude,
		quote.TurnoverRate,
		quote.VolumeRatio,
		quote.MainNetInflow,
		quote.SuperNetInflow,
		quote.LargeNetInflow,
		quote.MediumNetInflow,
		quote.SmallNetInflow,
		quote.MainNetInflowPct,
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
		       high_price, low_price, volume, amount, pct_change, COALESCE(amplitude,0),
		       COALESCE(turnover_rate,0), COALESCE(volume_ratio,0), COALESCE(main_net_inflow,0), COALESCE(super_net_inflow,0),
		       COALESCE(large_net_inflow,0), COALESCE(medium_net_inflow,0), COALESCE(small_net_inflow,0),
		       COALESCE(main_net_inflow_pct,0), quote_at, fetched_at,
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

func (s *Store) InsertQuoteSnapshot(ctx context.Context, snapshot StockV2QuoteSnapshot) error {
	now := time.Now()
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = now
	}
	if snapshot.FetchedAt.IsZero() {
		snapshot.FetchedAt = snapshot.CollectedAt
	}
	if snapshot.QuoteAt.IsZero() {
		snapshot.QuoteAt = snapshot.FetchedAt
	}
	if snapshot.Status == "" {
		snapshot.Status = QuoteStatusFresh
	}
	if snapshot.Source == "" {
		snapshot.Source = QuoteSourceTencent
	}
	_, err := s.assetDB().ExecContext(ctx, `
		INSERT INTO stockv2_quote_snapshots (
			id, symbol, market, name, last_price, prev_close, open_price, high_price,
			low_price, volume, amount, pct_change, amplitude, turnover_rate,
			volume_ratio, main_net_inflow, super_net_inflow, large_net_inflow,
			medium_net_inflow, small_net_inflow, main_net_inflow_pct,
			quote_at, collected_at, source, status, error_message, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		generateID(),
		snapshot.Symbol,
		snapshot.Market,
		snapshot.Name,
		snapshot.LastPrice,
		snapshot.PrevClose,
		snapshot.OpenPrice,
		snapshot.HighPrice,
		snapshot.LowPrice,
		snapshot.Volume,
		snapshot.Amount,
		snapshot.PctChange,
		snapshot.Amplitude,
		snapshot.TurnoverRate,
		snapshot.VolumeRatio,
		snapshot.MainNetInflow,
		snapshot.SuperNetInflow,
		snapshot.LargeNetInflow,
		snapshot.MediumNetInflow,
		snapshot.SmallNetInflow,
		snapshot.MainNetInflowPct,
		snapshot.QuoteAt,
		snapshot.CollectedAt,
		snapshot.Source,
		snapshot.Status,
		snapshot.ErrorMessage,
		now,
	)
	return wrapError(err, "insert quote snapshot")
}

func (s *Store) RebuildRecentMinuteBars(ctx context.Context, symbols []string, since time.Time) error {
	for _, symbol := range symbols {
		snapshots, err := s.listQuoteSnapshots(ctx, symbol, since)
		if err != nil {
			return err
		}
		bars := buildMinuteBarsFromSnapshots(snapshots)
		if len(bars) == 0 {
			continue
		}
		if err := s.upsertMinuteBars(ctx, bars); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertMinuteBars(ctx context.Context, bars []StockV2MinuteBar) error {
	return s.upsertMinuteBars(ctx, bars)
}

func (s *Store) GetLatestMinuteBar(ctx context.Context, symbol string) (StockV2MinuteBar, bool, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return StockV2MinuteBar{}, false, nil
	}
	row := s.assetDB().QueryRowContext(ctx, `
		SELECT symbol, COALESCE(market,''), minute_at, open, high, low, close,
		       prev_close, volume, amount, pct_change, main_net_inflow,
		       snapshot_count, COALESCE(source,''), created_at, updated_at
		FROM stockv2_minute_bars
		WHERE symbol = ?
		ORDER BY minute_at DESC
		LIMIT 1
	`, symbol)
	var bar StockV2MinuteBar
	if err := row.Scan(
		&bar.Symbol,
		&bar.Market,
		&bar.MinuteAt,
		&bar.Open,
		&bar.High,
		&bar.Low,
		&bar.Close,
		&bar.PrevClose,
		&bar.Volume,
		&bar.Amount,
		&bar.PctChange,
		&bar.MainNetInflow,
		&bar.SnapshotCount,
		&bar.Source,
		&bar.CreatedAt,
		&bar.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2MinuteBar{}, false, nil
		}
		return StockV2MinuteBar{}, false, wrapError(err, "get latest minute bar")
	}
	return bar, true, nil
}

func (s *Store) ListMinuteBars(ctx context.Context, symbol string, since time.Time, limit int) ([]StockV2MinuteBar, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return []StockV2MinuteBar{}, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	query := `
		SELECT symbol, COALESCE(market,''), minute_at, open, high, low, close,
		       prev_close, volume, amount, pct_change, main_net_inflow,
		       snapshot_count, COALESCE(source,''), created_at, updated_at
		FROM stockv2_minute_bars
		WHERE symbol = ?`
	args := []any{symbol}
	if !since.IsZero() {
		query += ` AND minute_at >= ?`
		args = append(args, since)
	}
	query += ` ORDER BY minute_at ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.assetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "list minute bars")
	}
	defer rows.Close()

	var bars []StockV2MinuteBar
	for rows.Next() {
		var bar StockV2MinuteBar
		if err := rows.Scan(
			&bar.Symbol,
			&bar.Market,
			&bar.MinuteAt,
			&bar.Open,
			&bar.High,
			&bar.Low,
			&bar.Close,
			&bar.PrevClose,
			&bar.Volume,
			&bar.Amount,
			&bar.PctChange,
			&bar.MainNetInflow,
			&bar.SnapshotCount,
			&bar.Source,
			&bar.CreatedAt,
			&bar.UpdatedAt,
		); err != nil {
			return nil, wrapError(err, "scan minute bar")
		}
		if isAStockRegularTradingMinute(bar.MinuteAt) {
			bars = append(bars, bar)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate minute bars")
	}
	return bars, nil
}

func (s *Store) PruneIntradayQuotes(ctx context.Context, before time.Time) error {
	if before.IsZero() {
		return nil
	}
	if _, err := s.assetDB().ExecContext(ctx, `DELETE FROM stockv2_quote_snapshots WHERE collected_at < ?`, before); err != nil {
		return wrapError(err, "prune quote snapshots")
	}
	if _, err := s.assetDB().ExecContext(ctx, `DELETE FROM stockv2_minute_bars WHERE minute_at < ?`, before); err != nil {
		return wrapError(err, "prune minute bars")
	}
	return nil
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
		nullableString(status.Market),
		nullableString(status.Source),
		status.Status,
		status.LastAttemptAt,
		nullableTime(status.LastSuccessAt),
		nullableTime(status.LastFailureAt),
		nullableString(status.ErrorMessage),
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
		nullableString(state.TriggerType),
		nullableTime(state.StartedAt),
		nullableTime(state.FinishedAt),
		nullableString(state.ScopeSummary),
		state.ScannedCount,
		state.SuccessCount,
		state.FailedCount,
		nullableString(state.ErrorMessage),
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
	return scanRows(rows, scanQuoteRefreshStatus, "scan quote refresh status", "iterate quote refresh statuses")
}

func (s *Store) getLatestQuote(ctx context.Context, symbol string) (StockV2QuoteLatest, error) {
	row := s.assetDB().QueryRowContext(ctx, `
		SELECT symbol, market, COALESCE(name,''), last_price, prev_close, open_price,
		       high_price, low_price, volume, amount, pct_change, COALESCE(amplitude,0),
		       COALESCE(turnover_rate,0), COALESCE(volume_ratio,0), COALESCE(main_net_inflow,0), COALESCE(super_net_inflow,0),
		       COALESCE(large_net_inflow,0), COALESCE(medium_net_inflow,0), COALESCE(small_net_inflow,0),
		       COALESCE(main_net_inflow_pct,0), quote_at, fetched_at,
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
		&quote.Amplitude,
		&quote.TurnoverRate,
		&quote.VolumeRatio,
		&quote.MainNetInflow,
		&quote.SuperNetInflow,
		&quote.LargeNetInflow,
		&quote.MediumNetInflow,
		&quote.SmallNetInflow,
		&quote.MainNetInflowPct,
		&quote.QuoteAt,
		&quote.FetchedAt,
		&quote.Source,
		&quote.Status,
		&quote.ErrorMessage,
	)
	return quote, err
}

func (s *Store) listQuoteSnapshots(ctx context.Context, symbol string, since time.Time) ([]StockV2QuoteSnapshot, error) {
	query := `
		SELECT symbol, COALESCE(market,''), COALESCE(name,''), last_price, prev_close,
		       open_price, high_price, low_price, volume, amount, pct_change, amplitude,
		       turnover_rate, volume_ratio, main_net_inflow, super_net_inflow,
		       large_net_inflow, medium_net_inflow, small_net_inflow,
		       main_net_inflow_pct, quote_at, collected_at, source, status,
		       COALESCE(error_message,'')
		FROM stockv2_quote_snapshots
		WHERE symbol = ?`
	args := []any{symbol}
	if !since.IsZero() {
		query += ` AND collected_at >= ?`
		args = append(args, since.Add(-10*time.Minute))
	}
	query += ` ORDER BY collected_at ASC`

	rows, err := s.assetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "list quote snapshots")
	}
	defer rows.Close()

	var out []StockV2QuoteSnapshot
	for rows.Next() {
		var snap StockV2QuoteSnapshot
		if err := rows.Scan(
			&snap.Symbol,
			&snap.Market,
			&snap.Name,
			&snap.LastPrice,
			&snap.PrevClose,
			&snap.OpenPrice,
			&snap.HighPrice,
			&snap.LowPrice,
			&snap.Volume,
			&snap.Amount,
			&snap.PctChange,
			&snap.Amplitude,
			&snap.TurnoverRate,
			&snap.VolumeRatio,
			&snap.MainNetInflow,
			&snap.SuperNetInflow,
			&snap.LargeNetInflow,
			&snap.MediumNetInflow,
			&snap.SmallNetInflow,
			&snap.MainNetInflowPct,
			&snap.QuoteAt,
			&snap.CollectedAt,
			&snap.Source,
			&snap.Status,
			&snap.ErrorMessage,
		); err != nil {
			return nil, wrapError(err, "scan quote snapshot")
		}
		snap.FetchedAt = snap.CollectedAt
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate quote snapshots")
	}
	return out, nil
}

func (s *Store) upsertMinuteBars(ctx context.Context, bars []StockV2MinuteBar) error {
	now := time.Now()
	for _, bar := range bars {
		if bar.Symbol == "" || bar.MinuteAt.IsZero() {
			continue
		}
		if bar.CreatedAt.IsZero() {
			bar.CreatedAt = now
		}
		if bar.UpdatedAt.IsZero() {
			bar.UpdatedAt = now
		}
		_, err := s.assetDB().ExecContext(ctx, `
			INSERT INTO stockv2_minute_bars (
				symbol, market, minute_at, open, high, low, close, prev_close,
				volume, amount, pct_change, main_net_inflow, snapshot_count,
				source, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(symbol, minute_at) DO UPDATE SET
				market = excluded.market,
				open = excluded.open,
				high = excluded.high,
				low = excluded.low,
				close = excluded.close,
				prev_close = excluded.prev_close,
				volume = excluded.volume,
				amount = excluded.amount,
				pct_change = excluded.pct_change,
				main_net_inflow = excluded.main_net_inflow,
				snapshot_count = excluded.snapshot_count,
				source = excluded.source,
				updated_at = excluded.updated_at
		`,
			bar.Symbol,
			bar.Market,
			bar.MinuteAt,
			bar.Open,
			bar.High,
			bar.Low,
			bar.Close,
			bar.PrevClose,
			bar.Volume,
			bar.Amount,
			bar.PctChange,
			bar.MainNetInflow,
			bar.SnapshotCount,
			bar.Source,
			bar.CreatedAt,
			bar.UpdatedAt,
		)
		if err != nil {
			return wrapError(err, "upsert minute bar")
		}
	}
	return nil
}

func buildMinuteBarsFromSnapshots(snapshots []StockV2QuoteSnapshot) []StockV2MinuteBar {
	if len(snapshots) == 0 {
		return nil
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshots[i].CollectedAt.Before(snapshots[j].CollectedAt)
	})
	type bucket struct {
		bar       StockV2MinuteBar
		lastSeen  time.Time
		seenPrice bool
	}
	buckets := map[time.Time]*bucket{}
	var prev *StockV2QuoteSnapshot
	for i := range snapshots {
		snap := snapshots[i]
		if snap.Symbol == "" || snap.LastPrice <= 0 {
			continue
		}
		t := snap.QuoteAt
		if t.IsZero() {
			t = snap.CollectedAt
		}
		if !isAStockRegularTradingMinute(t) {
			continue
		}
		minute := t.Truncate(time.Minute)
		b := buckets[minute]
		if b == nil {
			b = &bucket{bar: StockV2MinuteBar{
				Symbol:    snap.Symbol,
				Market:    snap.Market,
				MinuteAt:  minute,
				Open:      snap.LastPrice,
				High:      snap.LastPrice,
				Low:       snap.LastPrice,
				Close:     snap.LastPrice,
				PrevClose: snap.PrevClose,
				Source:    snap.Source,
				CreatedAt: snap.CollectedAt,
				UpdatedAt: snap.CollectedAt,
			}, seenPrice: true}
			buckets[minute] = b
		}
		if snap.LastPrice > b.bar.High {
			b.bar.High = snap.LastPrice
		}
		if snap.LastPrice < b.bar.Low || b.bar.Low == 0 {
			b.bar.Low = snap.LastPrice
		}
		if snap.CollectedAt.After(b.lastSeen) || b.lastSeen.IsZero() {
			b.lastSeen = snap.CollectedAt
			b.bar.Close = snap.LastPrice
			b.bar.PctChange = snap.PctChange
			b.bar.Source = snap.Source
			b.bar.UpdatedAt = snap.CollectedAt
		}
		if prev != nil && prev.Symbol == snap.Symbol {
			b.bar.Volume += nonNegativeDelta(prev.Volume, snap.Volume)
			b.bar.Amount += nonNegativeDelta(prev.Amount, snap.Amount)
			b.bar.MainNetInflow += snap.MainNetInflow - prev.MainNetInflow
		}
		b.bar.SnapshotCount++
		prev = &snap
	}
	keys := make([]time.Time, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	bars := make([]StockV2MinuteBar, 0, len(keys))
	for _, key := range keys {
		if buckets[key].seenPrice {
			bars = append(bars, buckets[key].bar)
		}
	}
	return bars
}

func isAStockRegularTradingMinute(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	local := t.In(chinaMarketTZ)
	weekday := local.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	hour, minute, _ := local.Clock()
	minutes := hour*60 + minute
	return (minutes >= 9*60+30 && minutes <= 11*60+30) ||
		(minutes >= 13*60 && minutes <= 15*60)
}

func nonNegativeDelta(prev, current float64) float64 {
	if current <= 0 || prev <= 0 || current < prev {
		return 0
	}
	return current - prev
}
