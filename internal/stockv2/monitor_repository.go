package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ============================ task configs ============================

func (s *Store) UpsertMonitorTaskConfig(ctx context.Context, taskType string, cfg MonitorTaskConfig) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_monitor_task_configs
			(task_type, enabled, interval_seconds, scope, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_type) DO UPDATE SET
			enabled = excluded.enabled,
			interval_seconds = excluded.interval_seconds,
			scope = excluded.scope,
			sensitivity = excluded.sensitivity,
			cooldown_seconds = excluded.cooldown_seconds,
			agent_doublecheck_enabled = excluded.agent_doublecheck_enabled,
			agent_budget = excluded.agent_budget,
			updated_at = excluded.updated_at
	`,
		taskType,
		boolToInt(cfg.Enabled),
		cfg.IntervalSeconds,
		nullableMonitorString(cfg.Scope),
		monitorStringOrDefault(cfg.Sensitivity, "normal"),
		cfg.CooldownSeconds,
		boolToInt(cfg.AgentDoublecheckEnabled),
		cfg.AgentBudget,
		time.Now(),
	)
	return wrapError(err, "upsert monitor task config")
}

func (s *Store) GetMonitorTaskConfig(ctx context.Context, taskType string) (MonitorTaskConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT enabled, interval_seconds, COALESCE(scope,''), sensitivity, cooldown_seconds,
		       agent_doublecheck_enabled, agent_budget
		FROM stockv2_monitor_task_configs
		WHERE task_type = ?
	`, taskType)
	cfg, err := scanMonitorTaskConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorTaskConfig{}, ErrMonitorTaskNotFound
		}
		return MonitorTaskConfig{}, wrapError(err, "get monitor task config")
	}
	return cfg, nil
}

func (s *Store) ListMonitorTaskConfigs(ctx context.Context) (map[string]MonitorTaskConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_type, enabled, interval_seconds, COALESCE(scope,''), sensitivity, cooldown_seconds,
		       agent_doublecheck_enabled, agent_budget
		FROM stockv2_monitor_task_configs
	`)
	if err != nil {
		return nil, wrapError(err, "list monitor task configs")
	}
	defer rows.Close()

	result := make(map[string]MonitorTaskConfig)
	for rows.Next() {
		var taskType string
		var enabled, interval, cooldown, agentDoublecheck, agentBudget int
		var scope, sensitivity string
		if err := rows.Scan(&taskType, &enabled, &interval, &scope, &sensitivity, &cooldown, &agentDoublecheck, &agentBudget); err != nil {
			return nil, wrapError(err, "scan monitor task config")
		}
		result[taskType] = MonitorTaskConfig{
			Enabled:                 enabled != 0,
			IntervalSeconds:         interval,
			Scope:                   scope,
			Sensitivity:             sensitivity,
			CooldownSeconds:         cooldown,
			AgentDoublecheckEnabled: agentDoublecheck != 0,
			AgentBudget:             agentBudget,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate monitor task configs")
	}
	return result, nil
}

func scanMonitorTaskConfig(row rowScanner) (MonitorTaskConfig, error) {
	var enabled, interval, cooldown, agentDoublecheck, agentBudget int
	var scope, sensitivity string
	if err := row.Scan(&enabled, &interval, &scope, &sensitivity, &cooldown, &agentDoublecheck, &agentBudget); err != nil {
		return MonitorTaskConfig{}, err
	}
	return MonitorTaskConfig{
		Enabled:                 enabled != 0,
		IntervalSeconds:         interval,
		Scope:                   scope,
		Sensitivity:             sensitivity,
		CooldownSeconds:         cooldown,
		AgentDoublecheckEnabled: agentDoublecheck != 0,
		AgentBudget:             agentBudget,
	}, nil
}

// ============================ runs ============================

func (s *Store) CreateMonitorRun(ctx context.Context, run MonitorRun) (MonitorRun, error) {
	now := time.Now()
	if run.ID == "" {
		run.ID = generateID()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.Metadata == nil {
		run.Metadata = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_monitor_runs
			(id, task_type, status, trigger_type, started_at, finished_at, scope_summary,
			 scanned_count, hit_count, alert_count, review_count, success_count, failed_count,
			 error_message, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.TaskType,
		run.Status,
		run.TriggerType,
		run.StartedAt,
		nullableMonitorTime(run.FinishedAt),
		nullableMonitorString(run.ScopeSummary),
		run.ScannedCount,
		run.HitCount,
		run.AlertCount,
		run.ReviewCount,
		run.SuccessCount,
		run.FailedCount,
		nullableMonitorString(run.ErrorMessage),
		marshalMap(run.Metadata),
		run.CreatedAt,
	)
	return run, wrapError(err, "create monitor run")
}

func (s *Store) UpdateMonitorRun(ctx context.Context, run MonitorRun) (MonitorRun, error) {
	if run.Metadata == nil {
		run.Metadata = map[string]any{}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_runs
		SET status = ?, finished_at = ?, scope_summary = ?, scanned_count = ?, hit_count = ?,
		    alert_count = ?, review_count = ?, success_count = ?, failed_count = ?,
		    error_message = ?, metadata_json = ?
		WHERE id = ?
	`,
		run.Status,
		nullableMonitorTime(run.FinishedAt),
		nullableMonitorString(run.ScopeSummary),
		run.ScannedCount,
		run.HitCount,
		run.AlertCount,
		run.ReviewCount,
		run.SuccessCount,
		run.FailedCount,
		nullableMonitorString(run.ErrorMessage),
		marshalMap(run.Metadata),
		run.ID,
	)
	if err != nil {
		return MonitorRun{}, wrapError(err, "update monitor run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return MonitorRun{}, ErrMonitorTaskNotFound
	}
	return run, nil
}

func (s *Store) IncrementMonitorRunReviewCount(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_runs
		SET review_count = review_count + 1
		WHERE id = ?
	`, id)
	return wrapError(err, "increment monitor run review count")
}

func (s *Store) IncrementMonitorRunAlertCount(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_runs
		SET alert_count = alert_count + 1
		WHERE id = ?
	`, id)
	return wrapError(err, "increment monitor run alert count")
}

func (s *Store) GetMonitorRun(ctx context.Context, id string) (MonitorRun, error) {
	row := s.db.QueryRowContext(ctx, monitorRunSelectSQL+" WHERE id = ?", id)
	run, err := scanMonitorRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorRun{}, ErrMonitorTaskNotFound
		}
		return MonitorRun{}, wrapError(err, "get monitor run")
	}
	return run, nil
}

func (s *Store) GetLatestMonitorRun(ctx context.Context, taskType string) (*MonitorRun, error) {
	row := s.db.QueryRowContext(ctx, monitorRunSelectSQL+" WHERE task_type = ? ORDER BY started_at DESC, created_at DESC LIMIT 1", taskType)
	run, err := scanMonitorRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, wrapError(err, "get latest monitor run")
	}
	return &run, nil
}

func (s *Store) ListMonitorRuns(ctx context.Context, filter MonitorRunListFilter) ([]MonitorRun, error) {
	where, args := monitorRunFilterSQL(filter)
	limit := normalizedMonitorLimit(filter.Limit)
	offset := normalizedMonitorOffset(filter.Offset)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY started_at DESC, created_at DESC LIMIT ? OFFSET ?
	`, monitorRunSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list monitor runs")
	}
	defer rows.Close()
	items := make([]MonitorRun, 0)
	for rows.Next() {
		run, err := scanMonitorRun(rows)
		if err != nil {
			return nil, wrapError(err, "scan monitor run")
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate monitor runs")
	}
	return items, nil
}

func (s *Store) CountMonitorRuns(ctx context.Context, filter MonitorRunListFilter) (int, error) {
	where, args := monitorRunFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_monitor_runs WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count monitor runs")
	}
	return count, nil
}

// ============================ hits ============================

func (s *Store) CreateMonitorHit(ctx context.Context, hit MonitorHit) (MonitorHit, error) {
	if hit.ID == "" {
		hit.ID = generateID()
	}
	if hit.CreatedAt.IsZero() {
		hit.CreatedAt = time.Now()
	}
	if hit.Evidence == nil {
		hit.Evidence = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_monitor_hits
			(id, run_id, task_type, status, strategy_id, portfolio_id, symbol, market,
			 title, summary, evidence_json, agent_decision_id, alert_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		hit.ID,
		hit.RunID,
		hit.TaskType,
		hit.Status,
		nullableMonitorString(hit.StrategyID),
		nullableMonitorString(hit.PortfolioID),
		nullableMonitorString(hit.Symbol),
		nullableMonitorString(hit.Market),
		hit.Title,
		nullableMonitorString(hit.Summary),
		marshalMap(hit.Evidence),
		nullableMonitorString(hit.AgentDecisionID),
		nullableMonitorString(hit.AlertID),
		hit.CreatedAt,
	)
	return hit, wrapError(err, "create monitor hit")
}

func (s *Store) GetMonitorHit(ctx context.Context, id string) (MonitorHit, error) {
	row := s.db.QueryRowContext(ctx, monitorHitSelectSQL+" WHERE id = ?", id)
	hit, err := scanMonitorHit(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorHit{}, ErrMonitorHitNotFound
		}
		return MonitorHit{}, wrapError(err, "get monitor hit")
	}
	return hit, nil
}

func (s *Store) UpdateMonitorHitStatus(ctx context.Context, id, status string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_hits
		SET status = ?
		WHERE id = ?
	`, status, id)
	if err != nil {
		return wrapError(err, "update monitor hit status")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrMonitorHitNotFound
	}
	return nil
}

func (s *Store) UpdateMonitorHitEvidence(ctx context.Context, id string, evidence map[string]any, agentDecisionID string) error {
	if evidence == nil {
		evidence = map[string]any{}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_hits
		SET evidence_json = ?, agent_decision_id = ?
		WHERE id = ?
	`, marshalMap(evidence), nullableMonitorString(agentDecisionID), id)
	if err != nil {
		return wrapError(err, "update monitor hit evidence")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrMonitorHitNotFound
	}
	return nil
}

func (s *Store) UpdateMonitorHitAlert(ctx context.Context, id, alertID, status string) error {
	if status == "" {
		status = MonitorHitStatusAlerted
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_hits
		SET alert_id = ?, status = ?
		WHERE id = ?
	`, nullableMonitorString(alertID), status, id)
	if err != nil {
		return wrapError(err, "update monitor hit alert")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrMonitorHitNotFound
	}
	return nil
}

func (s *Store) ListMonitorHits(ctx context.Context, filter MonitorHitListFilter) ([]MonitorHit, error) {
	where, args := monitorHitFilterSQL(filter)
	limit := normalizedMonitorLimit(filter.Limit)
	offset := normalizedMonitorOffset(filter.Offset)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, monitorHitSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list monitor hits")
	}
	defer rows.Close()
	items := make([]MonitorHit, 0)
	for rows.Next() {
		hit, err := scanMonitorHit(rows)
		if err != nil {
			return nil, wrapError(err, "scan monitor hit")
		}
		items = append(items, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate monitor hits")
	}
	return items, nil
}

func (s *Store) CountMonitorHits(ctx context.Context, filter MonitorHitListFilter) (int, error) {
	where, args := monitorHitFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_monitor_hits WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count monitor hits")
	}
	return count, nil
}

// ============================ SQL fragments & scans ============================

const monitorRunSelectSQL = `
	SELECT id, task_type, status, trigger_type, started_at, finished_at, COALESCE(scope_summary,''),
	       scanned_count, hit_count, alert_count, review_count, success_count, failed_count,
	       COALESCE(error_message,''), metadata_json, created_at
	FROM stockv2_monitor_runs
`

const monitorHitSelectSQL = `
	SELECT id, run_id, task_type, status, strategy_id, portfolio_id, symbol, market,
	       title, COALESCE(summary,''), evidence_json, agent_decision_id, alert_id, created_at
	FROM stockv2_monitor_hits
`

func scanMonitorRun(row rowScanner) (MonitorRun, error) {
	var run MonitorRun
	var finishedAt sql.NullTime
	var scopeSummary, errorMessage, metadataJSON sql.NullString
	if err := row.Scan(
		&run.ID, &run.TaskType, &run.Status, &run.TriggerType, &run.StartedAt, &finishedAt,
		&scopeSummary, &run.ScannedCount, &run.HitCount, &run.AlertCount, &run.ReviewCount,
		&run.SuccessCount, &run.FailedCount, &errorMessage, &metadataJSON, &run.CreatedAt,
	); err != nil {
		return run, err
	}
	if finishedAt.Valid {
		run.FinishedAt = finishedAt.Time
	}
	run.ScopeSummary = scopeSummary.String
	run.ErrorMessage = errorMessage.String
	run.Metadata = unmarshalMap(metadataJSON.String)
	return run, nil
}

func scanMonitorHit(row rowScanner) (MonitorHit, error) {
	var hit MonitorHit
	var strategyID, portfolioID, symbol, market, summary, evidenceJSON, agentDecisionID, alertID sql.NullString
	if err := row.Scan(
		&hit.ID, &hit.RunID, &hit.TaskType, &hit.Status, &strategyID, &portfolioID, &symbol, &market,
		&hit.Title, &summary, &evidenceJSON, &agentDecisionID, &alertID, &hit.CreatedAt,
	); err != nil {
		return hit, err
	}
	hit.StrategyID = strategyID.String
	hit.PortfolioID = portfolioID.String
	hit.Symbol = symbol.String
	hit.Market = market.String
	hit.Summary = summary.String
	hit.Evidence = unmarshalMap(evidenceJSON.String)
	hit.AgentDecisionID = agentDecisionID.String
	hit.AlertID = alertID.String
	return hit, nil
}

func monitorRunFilterSQL(filter MonitorRunListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		where = append(where, column+" = ?")
		args = append(args, strings.TrimSpace(value))
	}
	if strings.TrimSpace(filter.TaskType) == "" {
		where = append(where, "task_type <> ?")
		args = append(args, MonitorTaskLatestQuoteRefresh)
	} else if strings.TrimSpace(filter.TaskType) == MonitorTaskLatestQuoteRefresh {
		where = append(where, "1=0")
	} else {
		add("task_type", filter.TaskType)
	}
	add("status", filter.Status)
	return strings.Join(where, " AND "), args
}

func monitorHitFilterSQL(filter MonitorHitListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		where = append(where, column+" = ?")
		args = append(args, strings.TrimSpace(value))
	}
	add("run_id", filter.RunID)
	add("task_type", filter.TaskType)
	add("status", filter.Status)
	add("strategy_id", filter.StrategyID)
	add("portfolio_id", filter.PortfolioID)
	add("symbol", filter.Symbol)
	return strings.Join(where, " AND "), args
}

func normalizedMonitorLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizedMonitorOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func nullableMonitorString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableMonitorTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func monitorStringOrDefault(value, def string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return def
	}
	return v
}
