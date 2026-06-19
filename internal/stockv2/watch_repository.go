package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateWatch(ctx context.Context, watch StockV2Watch) (StockV2Watch, error) {
	now := time.Now()
	if watch.ID == "" {
		watch.ID = generateID()
	}
	watch.CreatedAt = now
	watch.UpdatedAt = now
	if watch.TriggerConfig == nil {
		watch.TriggerConfig = map[string]any{}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_watches (
			id, name, status, source, symbol, market, portfolio_id, strategy_id,
			strategy_version_id, trigger_policy, trigger_config_json, schedule_kind,
			cooldown_seconds, last_checked_at, last_triggered_at, last_run_status,
			last_run_reason, archived_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		watch.ID,
		watch.Name,
		watch.Status,
		watch.Source,
		nullableWatchString(watch.Symbol),
		nullableWatchString(watch.Market),
		nullableWatchString(watch.PortfolioID),
		nullableWatchString(watch.StrategyID),
		nullableWatchString(watch.StrategyVersionID),
		watch.TriggerPolicy,
		marshalMap(watch.TriggerConfig),
		watch.ScheduleKind,
		watch.CooldownSeconds,
		nullableWatchTime(watch.LastCheckedAt),
		nullableWatchTime(watch.LastTriggeredAt),
		nullableWatchString(watch.LastRunStatus),
		nullableWatchString(watch.LastRunReason),
		nullableWatchTime(watch.ArchivedAt),
		watch.CreatedAt,
		watch.UpdatedAt,
	)
	return watch, wrapError(err, "create watch")
}

func (s *Store) GetWatch(ctx context.Context, id string) (StockV2Watch, error) {
	row := s.db.QueryRowContext(ctx, watchSelectSQL+" WHERE id = ?", id)
	watch, err := scanWatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Watch{}, ErrWatchNotFound
		}
		return StockV2Watch{}, wrapError(err, "get watch")
	}
	return watch, nil
}

func (s *Store) ListWatches(ctx context.Context, filter WatchListFilter) ([]StockV2Watch, error) {
	where, args := watchFilterSQL(filter)
	limit := normalizedWatchLimit(filter.Limit)
	offset := normalizedWatchOffset(filter.Offset)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s
		WHERE %s
		ORDER BY updated_at DESC, created_at DESC
		LIMIT ? OFFSET ?
	`, watchSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list watches")
	}
	defer rows.Close()

	items := make([]StockV2Watch, 0)
	for rows.Next() {
		watch, err := scanWatch(rows)
		if err != nil {
			return nil, wrapError(err, "scan watch")
		}
		items = append(items, watch)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate watches")
	}
	return items, nil
}

func (s *Store) CountWatches(ctx context.Context, filter WatchListFilter) (int, error) {
	where, args := watchFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*)
		FROM stockv2_watches
		WHERE %s
	`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count watches")
	}
	return count, nil
}

func (s *Store) UpdateWatch(ctx context.Context, watch StockV2Watch) (StockV2Watch, error) {
	watch.UpdatedAt = time.Now()
	if watch.TriggerConfig == nil {
		watch.TriggerConfig = map[string]any{}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_watches
		SET name = ?, status = ?, source = ?, symbol = ?, market = ?,
		    portfolio_id = ?, strategy_id = ?, strategy_version_id = ?,
		    trigger_policy = ?, trigger_config_json = ?, schedule_kind = ?,
		    cooldown_seconds = ?, last_checked_at = ?, last_triggered_at = ?,
		    last_run_status = ?, last_run_reason = ?, archived_at = ?, updated_at = ?
		WHERE id = ?
	`,
		watch.Name,
		watch.Status,
		watch.Source,
		nullableWatchString(watch.Symbol),
		nullableWatchString(watch.Market),
		nullableWatchString(watch.PortfolioID),
		nullableWatchString(watch.StrategyID),
		nullableWatchString(watch.StrategyVersionID),
		watch.TriggerPolicy,
		marshalMap(watch.TriggerConfig),
		watch.ScheduleKind,
		watch.CooldownSeconds,
		nullableWatchTime(watch.LastCheckedAt),
		nullableWatchTime(watch.LastTriggeredAt),
		nullableWatchString(watch.LastRunStatus),
		nullableWatchString(watch.LastRunReason),
		nullableWatchTime(watch.ArchivedAt),
		watch.UpdatedAt,
		watch.ID,
	)
	if err != nil {
		return StockV2Watch{}, wrapError(err, "update watch")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return StockV2Watch{}, wrapError(err, "check watch affected rows")
	}
	if rows == 0 {
		return StockV2Watch{}, ErrWatchNotFound
	}
	return watch, nil
}

func (s *Store) FindNonArchivedStrategyWatch(ctx context.Context, strategyID string) (StockV2Watch, error) {
	row := s.db.QueryRowContext(ctx, watchSelectSQL+`
		WHERE strategy_id = ? AND status != ?
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 1
	`, strategyID, WatchStatusArchived)
	watch, err := scanWatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Watch{}, ErrWatchNotFound
		}
		return StockV2Watch{}, wrapError(err, "find strategy watch")
	}
	return watch, nil
}

func (s *Store) FindNonArchivedPortfolioMonitorWatch(ctx context.Context, portfolioID string) (StockV2Watch, error) {
	row := s.db.QueryRowContext(ctx, watchSelectSQL+`
		WHERE portfolio_id = ? AND source = ? AND status != ?
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 1
	`, portfolioID, WatchSourcePortfolioMonitor, WatchStatusArchived)
	watch, err := scanWatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Watch{}, ErrWatchNotFound
		}
		return StockV2Watch{}, wrapError(err, "find portfolio monitor watch")
	}
	return watch, nil
}

func (s *Store) CreateAlert(ctx context.Context, alert StockV2Alert) (StockV2Alert, error) {
	now := time.Now()
	if alert.ID == "" {
		alert.ID = generateID()
	}
	if alert.TriggeredAt.IsZero() {
		alert.TriggeredAt = now
	}
	alert.CreatedAt = now
	alert.UpdatedAt = now
	if alert.Evidence == nil {
		alert.Evidence = map[string]any{}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_alerts (
			id, watch_id, status, level, title, summary, dedupe_key,
			evidence_json, triggered_at, acknowledged_at, resolved_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		alert.ID,
		alert.WatchID,
		alert.Status,
		alert.Level,
		alert.Title,
		alert.Summary,
		nullableWatchString(alert.DedupeKey),
		marshalMap(alert.Evidence),
		alert.TriggeredAt,
		nullableWatchTime(alert.AcknowledgedAt),
		nullableWatchTime(alert.ResolvedAt),
		alert.CreatedAt,
		alert.UpdatedAt,
	)
	return alert, wrapError(err, "create alert")
}

func (s *Store) GetAlert(ctx context.Context, id string) (StockV2Alert, error) {
	row := s.db.QueryRowContext(ctx, alertSelectSQL+" WHERE id = ?", id)
	alert, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Alert{}, ErrAlertNotFound
		}
		return StockV2Alert{}, wrapError(err, "get alert")
	}
	return alert, nil
}

func (s *Store) ListAlerts(ctx context.Context, filter AlertListFilter) ([]StockV2Alert, error) {
	where, args := alertFilterSQL(filter)
	limit := normalizedWatchLimit(filter.Limit)
	offset := normalizedWatchOffset(filter.Offset)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s
		WHERE %s
		ORDER BY triggered_at DESC, created_at DESC
		LIMIT ? OFFSET ?
	`, alertSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list alerts")
	}
	defer rows.Close()

	items := make([]StockV2Alert, 0)
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, wrapError(err, "scan alert")
		}
		items = append(items, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate alerts")
	}
	return items, nil
}

func (s *Store) CountAlerts(ctx context.Context, filter AlertListFilter) (int, error) {
	where, args := alertFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*)
		FROM stockv2_alerts
		WHERE %s
	`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count alerts")
	}
	return count, nil
}

func (s *Store) UpdateAlert(ctx context.Context, alert StockV2Alert) (StockV2Alert, error) {
	alert.UpdatedAt = time.Now()
	if alert.Evidence == nil {
		alert.Evidence = map[string]any{}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_alerts
		SET watch_id = ?, status = ?, level = ?, title = ?, summary = ?,
		    dedupe_key = ?, evidence_json = ?, triggered_at = ?,
		    acknowledged_at = ?, resolved_at = ?, updated_at = ?
		WHERE id = ?
	`,
		alert.WatchID,
		alert.Status,
		alert.Level,
		alert.Title,
		alert.Summary,
		nullableWatchString(alert.DedupeKey),
		marshalMap(alert.Evidence),
		alert.TriggeredAt,
		nullableWatchTime(alert.AcknowledgedAt),
		nullableWatchTime(alert.ResolvedAt),
		alert.UpdatedAt,
		alert.ID,
	)
	if err != nil {
		return StockV2Alert{}, wrapError(err, "update alert")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return StockV2Alert{}, wrapError(err, "check alert affected rows")
	}
	if rows == 0 {
		return StockV2Alert{}, ErrAlertNotFound
	}
	return alert, nil
}

func (s *Store) FindLatestAlertByDedupeKey(ctx context.Context, watchID, dedupeKey string) (StockV2Alert, error) {
	row := s.db.QueryRowContext(ctx, alertSelectSQL+`
		WHERE watch_id = ? AND dedupe_key = ?
		ORDER BY triggered_at DESC, created_at DESC
		LIMIT 1
	`, watchID, dedupeKey)
	alert, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Alert{}, ErrAlertNotFound
		}
		return StockV2Alert{}, wrapError(err, "find latest alert by dedupe key")
	}
	return alert, nil
}

const watchSelectSQL = `
	SELECT id, name, status, source, symbol, market, portfolio_id, strategy_id,
	       strategy_version_id, trigger_policy, trigger_config_json, schedule_kind,
	       cooldown_seconds, last_checked_at, last_triggered_at,
	       COALESCE(last_run_status,''), COALESCE(last_run_reason,''), archived_at,
	       created_at, updated_at
	FROM stockv2_watches
`

const alertSelectSQL = `
	SELECT id, watch_id, status, level, title, COALESCE(summary,''), dedupe_key,
	       evidence_json, triggered_at, acknowledged_at, resolved_at, created_at, updated_at
	FROM stockv2_alerts
`

func scanWatch(row rowScanner) (StockV2Watch, error) {
	var watch StockV2Watch
	var symbol, market, portfolioID, strategyID, strategyVersionID sql.NullString
	var triggerConfigJSON sql.NullString
	var lastCheckedAt, lastTriggeredAt, archivedAt sql.NullTime
	err := row.Scan(
		&watch.ID,
		&watch.Name,
		&watch.Status,
		&watch.Source,
		&symbol,
		&market,
		&portfolioID,
		&strategyID,
		&strategyVersionID,
		&watch.TriggerPolicy,
		&triggerConfigJSON,
		&watch.ScheduleKind,
		&watch.CooldownSeconds,
		&lastCheckedAt,
		&lastTriggeredAt,
		&watch.LastRunStatus,
		&watch.LastRunReason,
		&archivedAt,
		&watch.CreatedAt,
		&watch.UpdatedAt,
	)
	if err != nil {
		return watch, err
	}
	watch.Symbol = symbol.String
	watch.Market = market.String
	watch.PortfolioID = portfolioID.String
	watch.StrategyID = strategyID.String
	watch.StrategyVersionID = strategyVersionID.String
	watch.TriggerConfig = unmarshalMap(triggerConfigJSON.String)
	if lastCheckedAt.Valid {
		watch.LastCheckedAt = lastCheckedAt.Time
	}
	if lastTriggeredAt.Valid {
		watch.LastTriggeredAt = lastTriggeredAt.Time
	}
	if archivedAt.Valid {
		watch.ArchivedAt = archivedAt.Time
	}
	return watch, nil
}

func scanAlert(row rowScanner) (StockV2Alert, error) {
	var alert StockV2Alert
	var dedupeKey, evidenceJSON sql.NullString
	var acknowledgedAt, resolvedAt sql.NullTime
	err := row.Scan(
		&alert.ID,
		&alert.WatchID,
		&alert.Status,
		&alert.Level,
		&alert.Title,
		&alert.Summary,
		&dedupeKey,
		&evidenceJSON,
		&alert.TriggeredAt,
		&acknowledgedAt,
		&resolvedAt,
		&alert.CreatedAt,
		&alert.UpdatedAt,
	)
	if err != nil {
		return alert, err
	}
	alert.DedupeKey = dedupeKey.String
	alert.Evidence = unmarshalMap(evidenceJSON.String)
	if acknowledgedAt.Valid {
		alert.AcknowledgedAt = acknowledgedAt.Time
	}
	if resolvedAt.Valid {
		alert.ResolvedAt = resolvedAt.Time
	}
	return alert, nil
}

func watchFilterSQL(filter WatchListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		where = append(where, column+" = ?")
		args = append(args, strings.TrimSpace(value))
	}
	add("status", filter.Status)
	add("portfolio_id", filter.PortfolioID)
	add("strategy_id", filter.StrategyID)
	add("symbol", filter.Symbol)
	return strings.Join(where, " AND "), args
}

func alertFilterSQL(filter AlertListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		where = append(where, column+" = ?")
		args = append(args, strings.TrimSpace(value))
	}
	add("status", filter.Status)
	add("watch_id", filter.WatchID)
	return strings.Join(where, " AND "), args
}

func normalizedWatchLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizedWatchOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func nullableWatchString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableWatchTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
