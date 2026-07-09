package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateAlert(ctx context.Context, alert StockV2Alert) (StockV2Alert, error) {
	now := time.Now()
	if alert.ID == "" {
		alert.ID = generateID()
	}
	if alert.TriggeredAt.IsZero() {
		alert.TriggeredAt = now
	}
	if alert.OccurrenceCount <= 0 {
		alert.OccurrenceCount = 1
	}
	if alert.FirstSeenAt.IsZero() {
		alert.FirstSeenAt = alert.TriggeredAt
	}
	if alert.LastSeenAt.IsZero() {
		alert.LastSeenAt = alert.TriggeredAt
	}
	alert.CreatedAt = now
	alert.UpdatedAt = now
	if alert.Evidence == nil {
		alert.Evidence = map[string]any{}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_alerts (
			id, monitor_hit_id, monitor_run_id, task_type, strategy_id,
			portfolio_id, symbol, market, review_id, review_status, agent_run_id,
			decision_ledger_id, trigger_source, status, level, title, summary,
			dedupe_key, evidence_json, occurrence_count, first_seen_at, last_seen_at,
			triggered_at, acknowledged_at, resolved_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		alert.ID,
		nullableString(alert.MonitorHitID),
		nullableString(alert.MonitorRunID),
		nullableString(alert.TaskType),
		nullableString(alert.StrategyID),
		nullableString(alert.PortfolioID),
		nullableString(alert.Symbol),
		nullableString(alert.Market),
		nullableString(alert.ReviewID),
		nullableString(alert.ReviewStatus),
		nullableString(alert.AgentRunID),
		nullableString(alert.DecisionLedgerID),
		nullableString(alert.TriggerSource),
		alert.Status,
		alert.Level,
		alert.Title,
		alert.Summary,
		nullableString(alert.DedupeKey),
		marshalMap(alert.Evidence),
		alert.OccurrenceCount,
		nullableTime(alert.FirstSeenAt),
		nullableTime(alert.LastSeenAt),
		alert.TriggeredAt,
		nullableTime(alert.AcknowledgedAt),
		nullableTime(alert.ResolvedAt),
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
	limit := normalizedPageLimit(filter.Limit, 200)
	offset := normalizedPageOffset(filter.Offset)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s
		WHERE %s
		ORDER BY COALESCE(last_seen_at, triggered_at) DESC, created_at DESC
		LIMIT ? OFFSET ?
	`, alertSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list alerts")
	}
	return scanRows(rows, scanAlert, "scan alert", "iterate alerts")
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
		SET monitor_hit_id = ?, monitor_run_id = ?, task_type = ?,
		    strategy_id = ?, portfolio_id = ?, symbol = ?, market = ?, review_id = ?,
		    review_status = ?, agent_run_id = ?, decision_ledger_id = ?,
		    trigger_source = ?, status = ?, level = ?, title = ?, summary = ?,
		    dedupe_key = ?, evidence_json = ?, occurrence_count = ?, first_seen_at = ?,
		    last_seen_at = ?, triggered_at = ?, acknowledged_at = ?, resolved_at = ?,
		    updated_at = ?
		WHERE id = ?
	`,
		nullableString(alert.MonitorHitID),
		nullableString(alert.MonitorRunID),
		nullableString(alert.TaskType),
		nullableString(alert.StrategyID),
		nullableString(alert.PortfolioID),
		nullableString(alert.Symbol),
		nullableString(alert.Market),
		nullableString(alert.ReviewID),
		nullableString(alert.ReviewStatus),
		nullableString(alert.AgentRunID),
		nullableString(alert.DecisionLedgerID),
		nullableString(alert.TriggerSource),
		alert.Status,
		alert.Level,
		alert.Title,
		alert.Summary,
		nullableString(alert.DedupeKey),
		marshalMap(alert.Evidence),
		alert.OccurrenceCount,
		nullableTime(alert.FirstSeenAt),
		nullableTime(alert.LastSeenAt),
		alert.TriggeredAt,
		nullableTime(alert.AcknowledgedAt),
		nullableTime(alert.ResolvedAt),
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

func (s *Store) FindLatestAlertByDedupeKey(ctx context.Context, dedupeKey string) (StockV2Alert, error) {
	row := s.db.QueryRowContext(ctx, alertSelectSQL+`
		WHERE dedupe_key = ?
		ORDER BY COALESCE(last_seen_at, triggered_at) DESC, created_at DESC
		LIMIT 1
	`, dedupeKey)
	alert, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Alert{}, ErrAlertNotFound
		}
		return StockV2Alert{}, wrapError(err, "find latest alert by dedupe key")
	}
	return alert, nil
}

const alertSelectSQL = `
	SELECT id, COALESCE(monitor_hit_id,''), COALESCE(monitor_run_id,''),
	       COALESCE(task_type,''), COALESCE(strategy_id,''), COALESCE(portfolio_id,''),
	       COALESCE(symbol,''), COALESCE(market,''), COALESCE(review_id,''),
	       COALESCE(review_status,''), COALESCE(agent_run_id,''), COALESCE(decision_ledger_id,''),
	       COALESCE(trigger_source,''), status, level, title, COALESCE(summary,''), dedupe_key,
	       evidence_json, COALESCE(occurrence_count, 1), first_seen_at, last_seen_at,
	       triggered_at, acknowledged_at, resolved_at, created_at, updated_at
	FROM stockv2_alerts
`

func scanAlert(row rowScanner) (StockV2Alert, error) {
	var alert StockV2Alert
	var dedupeKey, evidenceJSON sql.NullString
	var firstSeenAt, lastSeenAt, acknowledgedAt, resolvedAt sql.NullTime
	err := row.Scan(
		&alert.ID,
		&alert.MonitorHitID,
		&alert.MonitorRunID,
		&alert.TaskType,
		&alert.StrategyID,
		&alert.PortfolioID,
		&alert.Symbol,
		&alert.Market,
		&alert.ReviewID,
		&alert.ReviewStatus,
		&alert.AgentRunID,
		&alert.DecisionLedgerID,
		&alert.TriggerSource,
		&alert.Status,
		&alert.Level,
		&alert.Title,
		&alert.Summary,
		&dedupeKey,
		&evidenceJSON,
		&alert.OccurrenceCount,
		&firstSeenAt,
		&lastSeenAt,
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
	if alert.OccurrenceCount <= 0 {
		alert.OccurrenceCount = 1
	}
	if firstSeenAt.Valid {
		alert.FirstSeenAt = firstSeenAt.Time
	} else {
		alert.FirstSeenAt = alert.CreatedAt
	}
	if lastSeenAt.Valid {
		alert.LastSeenAt = lastSeenAt.Time
	} else {
		alert.LastSeenAt = alert.TriggeredAt
	}
	if acknowledgedAt.Valid {
		alert.AcknowledgedAt = acknowledgedAt.Time
	}
	if resolvedAt.Valid {
		alert.ResolvedAt = resolvedAt.Time
	}
	return alert, nil
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
	add("monitor_hit_id", filter.MonitorHitID)
	add("review_id", filter.ReviewID)
	add("task_type", filter.TaskType)
	add("symbol", filter.Symbol)
	add("portfolio_id", filter.PortfolioID)
	add("strategy_id", filter.StrategyID)
	return strings.Join(where, " AND "), args
}
