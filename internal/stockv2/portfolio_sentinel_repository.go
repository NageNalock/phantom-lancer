package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const portfolioSentinelRunSelectSQL = `
	SELECT id, COALESCE(portfolio_id,''), COALESCE(agent_run_id,''), COALESCE(decision_ledger_id,''),
	       status, trigger_type, window_type, window_start_at, window_end_at,
	       scanned_portfolio_count, scanned_holding_count, news_event_count, raw_news_count,
	       quote_count, daily_bar_symbol_count, minute_bar_symbol_count,
	       COALESCE(result_risk_level,''), generated_alert_count, generated_hit_count,
	       generated_review_count, COALESCE(error_message,''), started_at, finished_at,
	       created_at, updated_at
	FROM stockv2_portfolio_sentinel_runs
`

func (s *Store) UpsertPortfolioSentinelConfig(ctx context.Context, cfg PortfolioSentinelConfig) (PortfolioSentinelConfig, error) {
	if cfg.ID == "" {
		cfg.ID = "default"
	}
	if cfg.MaxNewsItems <= 0 {
		cfg.MaxNewsItems = 200
	}
	if cfg.MaxNewsPerHolding <= 0 {
		cfg.MaxNewsPerHolding = 50
	}
	cfg.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_portfolio_sentinel_config
			(id, enabled, pre_market_enabled, midday_enabled, post_close_enabled,
			 max_news_items, max_news_per_holding, agent_doublecheck_enabled, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			pre_market_enabled = excluded.pre_market_enabled,
			midday_enabled = excluded.midday_enabled,
			post_close_enabled = excluded.post_close_enabled,
			max_news_items = excluded.max_news_items,
			max_news_per_holding = excluded.max_news_per_holding,
			agent_doublecheck_enabled = excluded.agent_doublecheck_enabled,
			updated_at = excluded.updated_at
	`, cfg.ID, boolToInt(cfg.Enabled), boolToInt(cfg.PreMarketEnabled), boolToInt(cfg.MiddayEnabled),
		boolToInt(cfg.PostCloseEnabled), cfg.MaxNewsItems, cfg.MaxNewsPerHolding,
		boolToInt(cfg.AgentDoublecheckEnabled), cfg.UpdatedAt)
	if err != nil {
		return PortfolioSentinelConfig{}, wrapError(err, "upsert portfolio sentinel config")
	}
	return cfg, nil
}

func (s *Store) GetPortfolioSentinelConfig(ctx context.Context) (PortfolioSentinelConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, enabled, pre_market_enabled, midday_enabled, post_close_enabled,
		       max_news_items, max_news_per_holding, agent_doublecheck_enabled, updated_at
		FROM stockv2_portfolio_sentinel_config
		WHERE id = 'default'
	`)
	var cfg PortfolioSentinelConfig
	var enabled, preMarket, midday, postClose, doublecheck int
	if err := row.Scan(&cfg.ID, &enabled, &preMarket, &midday, &postClose, &cfg.MaxNewsItems, &cfg.MaxNewsPerHolding, &doublecheck, &cfg.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PortfolioSentinelConfig{}, ErrPortfolioSentinelResultNotFound
		}
		return PortfolioSentinelConfig{}, wrapError(err, "get portfolio sentinel config")
	}
	cfg.Enabled = enabled != 0
	cfg.PreMarketEnabled = preMarket != 0
	cfg.MiddayEnabled = midday != 0
	cfg.PostCloseEnabled = postClose != 0
	cfg.AgentDoublecheckEnabled = doublecheck != 0
	return cfg, nil
}

func (s *Store) CreatePortfolioSentinelRun(ctx context.Context, run PortfolioSentinelRun) (PortfolioSentinelRun, error) {
	now := time.Now()
	if run.ID == "" {
		run.ID = generateID()
	}
	if run.Status == "" {
		run.Status = PortfolioSentinelStatusRunning
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_portfolio_sentinel_runs (
			id, portfolio_id, agent_run_id, decision_ledger_id, status, trigger_type,
			window_type, window_start_at, window_end_at, scanned_portfolio_count,
			scanned_holding_count, news_event_count, raw_news_count, quote_count,
			daily_bar_symbol_count, minute_bar_symbol_count, result_risk_level,
			generated_alert_count, generated_hit_count, generated_review_count,
			error_message, started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, nullableString(run.PortfolioID), nullableString(run.AgentRunID), nullableString(run.DecisionLedgerID),
		run.Status, run.TriggerType, run.WindowType, run.WindowStartAt, run.WindowEndAt,
		run.ScannedPortfolioCount, run.ScannedHoldingCount, run.NewsEventCount, run.RawNewsCount,
		run.QuoteCount, run.DailyBarSymbolCount, run.MinuteBarSymbolCount, nullableString(run.ResultRiskLevel),
		run.GeneratedAlertCount, run.GeneratedHitCount, run.GeneratedReviewCount,
		nullableString(run.ErrorMessage), run.StartedAt, nullableTime(run.FinishedAt), run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return PortfolioSentinelRun{}, wrapError(err, "create portfolio sentinel run")
	}
	return run, nil
}

func (s *Store) UpdatePortfolioSentinelRun(ctx context.Context, run PortfolioSentinelRun) (PortfolioSentinelRun, error) {
	run.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_portfolio_sentinel_runs
		SET portfolio_id = ?, agent_run_id = ?, decision_ledger_id = ?, status = ?,
		    scanned_portfolio_count = ?, scanned_holding_count = ?, news_event_count = ?,
		    raw_news_count = ?, quote_count = ?, daily_bar_symbol_count = ?,
		    minute_bar_symbol_count = ?, result_risk_level = ?, generated_alert_count = ?,
		    generated_hit_count = ?, generated_review_count = ?, error_message = ?,
		    finished_at = ?, updated_at = ?
		WHERE id = ?
	`, nullableString(run.PortfolioID), nullableString(run.AgentRunID), nullableString(run.DecisionLedgerID),
		run.Status, run.ScannedPortfolioCount, run.ScannedHoldingCount, run.NewsEventCount,
		run.RawNewsCount, run.QuoteCount, run.DailyBarSymbolCount, run.MinuteBarSymbolCount,
		nullableString(run.ResultRiskLevel), run.GeneratedAlertCount, run.GeneratedHitCount,
		run.GeneratedReviewCount, nullableString(run.ErrorMessage), nullableTime(run.FinishedAt), run.UpdatedAt, run.ID)
	if err != nil {
		return PortfolioSentinelRun{}, wrapError(err, "update portfolio sentinel run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return PortfolioSentinelRun{}, ErrPortfolioSentinelRunNotFound
	}
	return run, nil
}

func (s *Store) GetPortfolioSentinelRun(ctx context.Context, id string) (PortfolioSentinelRun, error) {
	row := s.db.QueryRowContext(ctx, portfolioSentinelRunSelectSQL+" WHERE id = ?", id)
	run, err := scanPortfolioSentinelRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PortfolioSentinelRun{}, ErrPortfolioSentinelRunNotFound
		}
		return PortfolioSentinelRun{}, wrapError(err, "get portfolio sentinel run")
	}
	return run, nil
}

func (s *Store) ListPortfolioSentinelRuns(ctx context.Context, filter PortfolioSentinelRunListFilter) ([]PortfolioSentinelRun, error) {
	where, args := portfolioSentinelRunFilterSQL(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY started_at DESC, created_at DESC LIMIT ? OFFSET ?`, portfolioSentinelRunSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list portfolio sentinel runs")
	}
	return scanRows(rows, scanPortfolioSentinelRun, "scan portfolio sentinel run", "iterate portfolio sentinel runs")
}

func (s *Store) CountPortfolioSentinelRuns(ctx context.Context, filter PortfolioSentinelRunListFilter) (int, error) {
	where, args := portfolioSentinelRunFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_portfolio_sentinel_runs WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count portfolio sentinel runs")
	}
	return count, nil
}

func (s *Store) HasRunningPortfolioSentinelRun(ctx context.Context, portfolioID, windowType string) (bool, error) {
	where := []string{"status = ?"}
	args := []any{PortfolioSentinelStatusRunning}
	if strings.TrimSpace(portfolioID) != "" {
		where = append(where, "portfolio_id = ?")
		args = append(args, strings.TrimSpace(portfolioID))
	}
	if strings.TrimSpace(windowType) != "" {
		where = append(where, "window_type = ?")
		args = append(args, strings.TrimSpace(windowType))
	}
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_portfolio_sentinel_runs WHERE %s`, strings.Join(where, " AND ")), args...).Scan(&count); err != nil {
		return false, wrapError(err, "has running portfolio sentinel run")
	}
	return count > 0, nil
}

func (s *Store) CreatePortfolioSentinelResult(ctx context.Context, result PortfolioSentinelResult) (PortfolioSentinelResult, error) {
	if result.ID == "" {
		result.ID = generateID()
	}
	if result.SchemaVersion == "" {
		result.SchemaVersion = PortfolioSentinelReportSchemaVersion
	}
	if result.RawResult == nil {
		result.RawResult = map[string]any{}
	}
	if result.ContextSummary == nil {
		result.ContextSummary = map[string]any{}
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_portfolio_sentinel_results (
			id, run_id, schema_version, summary, risk_level, raw_result_json,
			context_summary_json, derived_alert_ids_json, derived_monitor_hit_ids_json,
			derived_review_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, result.ID, result.RunID, result.SchemaVersion, nullableString(result.Summary), nullableString(result.RiskLevel),
		marshalMap(result.RawResult), marshalMap(result.ContextSummary), marshalStrings(result.DerivedAlertIDs),
		marshalStrings(result.DerivedMonitorHitIDs), marshalStrings(result.DerivedReviewIDs), result.CreatedAt)
	if err != nil {
		return PortfolioSentinelResult{}, wrapError(err, "create portfolio sentinel result")
	}
	return result, nil
}

func (s *Store) GetPortfolioSentinelResult(ctx context.Context, id string) (PortfolioSentinelResult, error) {
	row := s.db.QueryRowContext(ctx, portfolioSentinelResultSelectSQL+" WHERE id = ?", id)
	result, err := scanPortfolioSentinelResult(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PortfolioSentinelResult{}, ErrPortfolioSentinelResultNotFound
		}
		return PortfolioSentinelResult{}, wrapError(err, "get portfolio sentinel result")
	}
	return result, nil
}

func (s *Store) GetPortfolioSentinelResultByRunID(ctx context.Context, runID string) (*PortfolioSentinelResult, error) {
	row := s.db.QueryRowContext(ctx, portfolioSentinelResultSelectSQL+" WHERE run_id = ?", runID)
	result, err := scanPortfolioSentinelResult(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, wrapError(err, "get portfolio sentinel result by run")
	}
	return &result, nil
}

func scanPortfolioSentinelRun(row rowScanner) (PortfolioSentinelRun, error) {
	var run PortfolioSentinelRun
	var finishedAt sql.NullTime
	if err := row.Scan(
		&run.ID, &run.PortfolioID, &run.AgentRunID, &run.DecisionLedgerID, &run.Status,
		&run.TriggerType, &run.WindowType, &run.WindowStartAt, &run.WindowEndAt,
		&run.ScannedPortfolioCount, &run.ScannedHoldingCount, &run.NewsEventCount, &run.RawNewsCount,
		&run.QuoteCount, &run.DailyBarSymbolCount, &run.MinuteBarSymbolCount, &run.ResultRiskLevel,
		&run.GeneratedAlertCount, &run.GeneratedHitCount, &run.GeneratedReviewCount, &run.ErrorMessage,
		&run.StartedAt, &finishedAt, &run.CreatedAt, &run.UpdatedAt,
	); err != nil {
		return run, err
	}
	if finishedAt.Valid {
		run.FinishedAt = finishedAt.Time
	}
	return run, nil
}

const portfolioSentinelResultSelectSQL = `
	SELECT id, run_id, schema_version, COALESCE(summary,''), COALESCE(risk_level,''),
	       raw_result_json, context_summary_json, derived_alert_ids_json,
	       derived_monitor_hit_ids_json, derived_review_ids_json, created_at
	FROM stockv2_portfolio_sentinel_results
`

func scanPortfolioSentinelResult(row rowScanner) (PortfolioSentinelResult, error) {
	var result PortfolioSentinelResult
	var rawJSON, ctxJSON, alertIDsJSON, hitIDsJSON, reviewIDsJSON string
	if err := row.Scan(&result.ID, &result.RunID, &result.SchemaVersion, &result.Summary, &result.RiskLevel,
		&rawJSON, &ctxJSON, &alertIDsJSON, &hitIDsJSON, &reviewIDsJSON, &result.CreatedAt); err != nil {
		return result, err
	}
	result.RawResult = unmarshalMap(rawJSON)
	result.ContextSummary = unmarshalMap(ctxJSON)
	result.DerivedAlertIDs = unmarshalStrings(alertIDsJSON)
	result.DerivedMonitorHitIDs = unmarshalStrings(hitIDsJSON)
	result.DerivedReviewIDs = unmarshalStrings(reviewIDsJSON)
	return result, nil
}

func portfolioSentinelRunFilterSQL(filter PortfolioSentinelRunListFilter) (string, []any) {
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
	add("trigger_type", filter.TriggerType)
	add("window_type", filter.WindowType)
	add("portfolio_id", filter.PortfolioID)
	return strings.Join(where, " AND "), args
}
