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

func (s *Store) FreezePortfolioSentinelImpactReviewScope(ctx context.Context, runID string) (PortfolioSentinelImpactReviewScopeSummary, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return PortfolioSentinelImpactReviewScopeSummary{}, ErrPortfolioSentinelRunNotFound
	}
	now := time.Now()
	err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var found string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM stockv2_portfolio_sentinel_runs WHERE id=?`, runID).Scan(&found); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPortfolioSentinelRunNotFound
			}
			return wrapError(err, "get portfolio sentinel run for impact review scope")
		}
		// ponytail: one immutable, set-based SQLite snapshot is the smallest safe
		// boundary for all five review domains; no copied object payload or queue is needed.
		statements := []struct {
			objectType string
			selectSQL  string
			args       []any
		}{
			{portfolioSentinelImpactObjectHoldings, `SELECT id FROM stockv2_holdings`, nil},
			{portfolioSentinelImpactObjectMonitors, `
				SELECT 'task:' || task_type AS id FROM stockv2_monitor_task_configs
				UNION ALL
				SELECT 'watch:' || id AS id FROM stockv2_watches WHERE status<>?`, []any{WatchStatusArchived}},
			{portfolioSentinelImpactObjectAlerts, `SELECT id FROM stockv2_alerts WHERE status IN (?, ?)`, []any{AlertStatusOpen, AlertStatusAcknowledged}},
			{portfolioSentinelImpactObjectOpportunities, `SELECT id FROM stockv2_opportunities WHERE status<>?`, []any{OpportunityStatusClosed}},
			{portfolioSentinelImpactObjectStrategies, `SELECT id FROM stockv2_strategies WHERE status<>?`, []any{StrategyStatusArchived}},
		}
		for _, statement := range statements {
			args := []any{runID, statement.objectType, now}
			args = append(args, statement.args...)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stockv2_portfolio_sentinel_impact_review_scope
					(run_id, object_type, object_id, created_at)
				SELECT ?, ?, id, ? FROM (`+statement.selectSQL+`)
			`, args...); err != nil {
				return wrapError(err, "freeze portfolio sentinel impact review scope")
			}
		}
		return nil
	})
	if err != nil {
		return PortfolioSentinelImpactReviewScopeSummary{}, err
	}
	return s.GetPortfolioSentinelImpactReviewScopeSummary(ctx, runID)
}

func (s *Store) GetPortfolioSentinelImpactReviewScopeSummary(ctx context.Context, runID string) (PortfolioSentinelImpactReviewScopeSummary, error) {
	runID = strings.TrimSpace(runID)
	if _, err := s.GetPortfolioSentinelRun(ctx, runID); err != nil {
		return PortfolioSentinelImpactReviewScopeSummary{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT object_type, COUNT(*)
		FROM stockv2_portfolio_sentinel_impact_review_scope
		WHERE run_id=?
		GROUP BY object_type
	`, runID)
	if err != nil {
		return PortfolioSentinelImpactReviewScopeSummary{}, wrapError(err, "get portfolio sentinel impact review scope summary")
	}
	defer rows.Close()
	var out PortfolioSentinelImpactReviewScopeSummary
	for rows.Next() {
		var objectType string
		var count int
		if err := rows.Scan(&objectType, &count); err != nil {
			return PortfolioSentinelImpactReviewScopeSummary{}, wrapError(err, "scan portfolio sentinel impact review scope summary")
		}
		switch objectType {
		case portfolioSentinelImpactObjectHoldings:
			out.HoldingCount = count
		case portfolioSentinelImpactObjectMonitors:
			out.MonitorCount = count
		case portfolioSentinelImpactObjectAlerts:
			out.AlertCount = count
		case portfolioSentinelImpactObjectOpportunities:
			out.OpportunityCount = count
		case portfolioSentinelImpactObjectStrategies:
			out.StrategyCount = count
		}
	}
	return out, wrapError(rows.Err(), "iterate portfolio sentinel impact review scope summary")
}

func (s *Store) ListPortfolioSentinelImpactReviewScope(ctx context.Context, runID, objectType string, limit, offset int) ([]string, int, error) {
	runID = strings.TrimSpace(runID)
	objectType = strings.TrimSpace(objectType)
	if !validPortfolioSentinelImpactObjectType(objectType) {
		return nil, 0, ErrInvalidPortfolioSentinelInput
	}
	if _, err := s.GetPortfolioSentinelRun(ctx, runID); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stockv2_portfolio_sentinel_impact_review_scope
		WHERE run_id=? AND object_type=?
	`, runID, objectType).Scan(&total); err != nil {
		return nil, 0, wrapError(err, "count portfolio sentinel impact review scope")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT object_id FROM stockv2_portfolio_sentinel_impact_review_scope
		WHERE run_id=? AND object_type=?
		ORDER BY object_id
		LIMIT ? OFFSET ?
	`, runID, objectType, normalizedPageLimit(limit, 200), normalizedPageOffset(offset))
	if err != nil {
		return nil, 0, wrapError(err, "list portfolio sentinel impact review scope")
	}
	items, err := scanRows(rows, func(row rowScanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	}, "scan portfolio sentinel impact review scope", "iterate portfolio sentinel impact review scope")
	return items, total, err
}

func validPortfolioSentinelImpactObjectType(value string) bool {
	switch strings.TrimSpace(value) {
	case portfolioSentinelImpactObjectHoldings,
		portfolioSentinelImpactObjectMonitors,
		portfolioSentinelImpactObjectAlerts,
		portfolioSentinelImpactObjectOpportunities,
		portfolioSentinelImpactObjectStrategies:
		return true
	default:
		return false
	}
}

// FailRunningPortfolioSentinelRuns closes process-local executions lost during
// restart. A linked news-context run returns to pending review so the existing
// reconciler can create one fresh sentinel instead of treating aggregation as
// failed.
func (s *Store) FailRunningPortfolioSentinelRuns(ctx context.Context, reason string) (int64, error) {
	now := time.Now()
	var failed int64
	err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// ponytail: all three tables live in SQLite, so one set-based transaction is
		// the smallest complete recovery boundary; no durable recovery queue is needed.
		if _, err := tx.ExecContext(ctx, `UPDATE stockv2_agent_runs SET
			status=?, error_message=?, finished_at=?, updated_at=?
			WHERE id IN (
				SELECT agent_run_id FROM stockv2_portfolio_sentinel_runs
				WHERE status=? AND TRIM(COALESCE(agent_run_id,''))<>''
			) AND status<>?`, AgentRunStatusFailed, nullableString(reason), now, now,
			PortfolioSentinelStatusRunning, AgentRunStatusFailed); err != nil {
			return wrapError(err, "fail interrupted portfolio sentinel agent runs")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_runs SET
			status=?, review_status=?, review_run_id=NULL, phase=?, error_message=NULL, updated_at=?
			WHERE status=? AND review_run_id IN (
				SELECT id FROM stockv2_portfolio_sentinel_runs WHERE status=?
			)`, NewsContextRunStatusWaitingReview, NewsContextReviewPending, "waiting_review", now,
			NewsContextRunStatusWaitingReview, PortfolioSentinelStatusRunning); err != nil {
			return wrapError(err, "reset interrupted news context reviews")
		}
		result, err := tx.ExecContext(ctx, `UPDATE stockv2_portfolio_sentinel_runs SET
			status=?, error_message=?, finished_at=?, updated_at=? WHERE status=?`,
			PortfolioSentinelStatusFailed, nullableString(reason), now, now, PortfolioSentinelStatusRunning)
		if err != nil {
			return wrapError(err, "fail running portfolio sentinel runs")
		}
		failed, _ = result.RowsAffected()
		return nil
	})
	return failed, err
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

func (s *Store) publishPortfolioSentinelResult(ctx context.Context, publication portfolioSentinelPublication) (PortfolioSentinelResult, error) {
	var published PortfolioSentinelResult
	err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		existing, err := scanPortfolioSentinelResult(tx.QueryRowContext(ctx, portfolioSentinelResultSelectSQL+" WHERE run_id = ?", publication.run.ID))
		if err == nil {
			published = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return wrapError(err, "get portfolio sentinel result before publish")
		}

		for _, item := range publication.planStrategies {
			strategy := item.strategy
			if item.create {
				activeVersionID := strategy.ActiveVersionID
				strategy.ActiveVersionID = ""
				if err := insertStrategyWithTx(ctx, tx, strategy); err != nil {
					return err
				}
				strategy.ActiveVersionID = activeVersionID
			} else {
				versionNo, err := nextStrategyVersionNo(ctx, tx, strategy.ID)
				if err != nil {
					return err
				}
				item.version.VersionNo = versionNo
			}
			if err := insertStrategyVersionWithTx(ctx, tx, item.version); err != nil {
				return err
			}
			if err := updateStrategyWithTx(ctx, tx, strategy); err != nil {
				return err
			}
		}
		if publication.enableDataStrategyMonitor {
			if _, err := tx.ExecContext(ctx, `
				UPDATE stockv2_monitor_task_configs
				SET enabled=1, updated_at=?
				WHERE task_type=?
			`, time.Now(), MonitorTaskDataStrategyMonitor); err != nil {
				return wrapError(err, "enable sentinel action plan monitor")
			}
		}

		monitorRun := publication.monitorRun
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stockv2_monitor_runs
				(id, task_type, status, trigger_type, started_at, finished_at, scope_summary,
				 scanned_count, hit_count, alert_count, review_count, success_count, failed_count,
				 error_message, metadata_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, monitorRun.ID, monitorRun.TaskType, monitorRun.Status, monitorRun.TriggerType,
			monitorRun.StartedAt, nullableTime(monitorRun.FinishedAt), nullableString(monitorRun.ScopeSummary),
			monitorRun.ScannedCount, monitorRun.HitCount, 0, monitorRun.ReviewCount,
			monitorRun.SuccessCount, monitorRun.FailedCount, nullableString(monitorRun.ErrorMessage),
			marshalMap(monitorRun.Metadata), monitorRun.CreatedAt); err != nil {
			return wrapError(err, "create portfolio sentinel monitor run")
		}

		result := publication.result
		result.DerivedAlertIDs = nil
		result.DerivedMonitorHitIDs = make([]string, 0, len(publication.items))
		result.DerivedReviewIDs = make([]string, 0, len(publication.items))
		for _, item := range publication.items {
			if err := insertPortfolioSentinelHitTx(ctx, tx, item.hit); err != nil {
				return err
			}
			if err := insertPortfolioSentinelReviewTx(ctx, tx, item.review); err != nil {
				return err
			}
			result.DerivedMonitorHitIDs = append(result.DerivedMonitorHitIDs, item.hit.ID)
			result.DerivedReviewIDs = append(result.DerivedReviewIDs, item.review.ID)

			hitStatus := MonitorHitStatusReviewed
			if item.review.OutputType == OperationReviewOutputIgnore {
				hitStatus = MonitorHitStatusIgnored
			}
			alertID := ""
			if operationReviewOutputTriggersAlert(item.review.OutputType) {
				alert, err := publishPortfolioSentinelAlertTx(ctx, tx, item, time.Now())
				if err != nil {
					return err
				}
				alertID = alert.ID
				result.DerivedAlertIDs = append(result.DerivedAlertIDs, alert.ID)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE stockv2_monitor_hits SET status=?, alert_id=? WHERE id=?
			`, hitStatus, nullableString(alertID), item.hit.ID); err != nil {
				return wrapError(err, "finish portfolio sentinel monitor hit")
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stockv2_monitor_runs SET alert_count=? WHERE id=?`, len(result.DerivedAlertIDs), monitorRun.ID); err != nil {
			return wrapError(err, "finish portfolio sentinel monitor run")
		}

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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stockv2_portfolio_sentinel_results (
				id, run_id, schema_version, summary, risk_level, raw_result_json,
				context_summary_json, derived_alert_ids_json, derived_monitor_hit_ids_json,
				derived_review_ids_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, result.ID, result.RunID, result.SchemaVersion, nullableString(result.Summary), nullableString(result.RiskLevel),
			marshalMap(result.RawResult), marshalMap(result.ContextSummary), marshalStrings(result.DerivedAlertIDs),
			marshalStrings(result.DerivedMonitorHitIDs), marshalStrings(result.DerivedReviewIDs), result.CreatedAt); err != nil {
			return wrapError(err, "create portfolio sentinel result")
		}

		finishedAt := time.Now()
		updated, err := tx.ExecContext(ctx, `
			UPDATE stockv2_portfolio_sentinel_runs
			SET status=?, result_risk_level=?, generated_alert_count=?, generated_hit_count=?,
			    generated_review_count=?, error_message=NULL, finished_at=?, updated_at=?
			WHERE id=?
		`, PortfolioSentinelStatusCompleted, nullableString(result.RiskLevel), len(result.DerivedAlertIDs),
			len(result.DerivedMonitorHitIDs), len(result.DerivedReviewIDs), finishedAt, finishedAt, publication.run.ID)
		if err != nil {
			return wrapError(err, "complete portfolio sentinel run")
		}
		if rows, _ := updated.RowsAffected(); rows == 0 {
			return ErrPortfolioSentinelRunNotFound
		}
		published = result
		return nil
	})
	if err != nil {
		return PortfolioSentinelResult{}, err
	}
	return published, nil
}

func insertPortfolioSentinelHitTx(ctx context.Context, tx *sql.Tx, hit MonitorHit) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_monitor_hits
			(id, run_id, task_type, status, strategy_id, portfolio_id, symbol, market,
			 title, summary, evidence_json, agent_decision_id, alert_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, hit.ID, hit.RunID, hit.TaskType, hit.Status, nullableString(hit.StrategyID),
		nullableString(hit.PortfolioID), nullableString(hit.Symbol), nullableString(hit.Market),
		hit.Title, nullableString(hit.Summary), marshalMap(hit.Evidence), nullableString(hit.AgentDecisionID),
		nullableString(hit.AlertID), hit.CreatedAt)
	return wrapError(err, "create portfolio sentinel monitor hit")
}

func insertPortfolioSentinelReviewTx(ctx context.Context, tx *sql.Tx, review OperationReview) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_operation_reviews (
			id, hit_id, run_id, status, output_type, strategy_id, portfolio_id, symbol, market,
			input_context_json, result_json, result_summary, error_message,
			created_at, updated_at, completed_at, closed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, review.ID, review.HitID, nullableString(review.RunID), review.Status, nullableString(review.OutputType),
		nullableString(review.StrategyID), nullableString(review.PortfolioID), nullableString(review.Symbol),
		nullableString(review.Market), marshalJSON(review.InputContext), marshalMap(review.Result),
		nullableString(review.ResultSummary), nullableString(review.ErrorMessage), review.CreatedAt, review.UpdatedAt,
		nullableTime(review.CompletedAt), nullableTime(review.ClosedAt))
	return wrapError(err, "create portfolio sentinel operation review")
}

func publishPortfolioSentinelAlertTx(ctx context.Context, tx *sql.Tx, item portfolioSentinelPublicationItem, now time.Time) (StockV2Alert, error) {
	hit, review := item.hit, item.review
	triggerSource := AlertTriggerSourceManualReviewConfirmed
	evidence := monitorAlertEvidence(hit, review, nil, triggerSource, "", hit.Evidence)
	dedupeKey := monitorAlertDedupeKey(hit, evidence)
	if dedupeKey != "" {
		existing, err := scanAlert(tx.QueryRowContext(ctx, alertSelectSQL+`
			WHERE dedupe_key = ?
			ORDER BY COALESCE(last_seen_at, triggered_at) DESC, created_at DESC
			LIMIT 1
		`, dedupeKey))
		if err == nil && monitorAlertWithinCooldown(existing, now, item.alertConfig.CooldownSeconds) {
			existing.MonitorHitID = hit.ID
			existing.MonitorRunID = hit.RunID
			existing.TaskType = hit.TaskType
			existing.StrategyID = hit.StrategyID
			existing.PortfolioID = hit.PortfolioID
			existing.Symbol = hit.Symbol
			existing.Market = hit.Market
			existing.ReviewID = review.ID
			existing.ReviewStatus = review.Status
			existing.TriggerSource = triggerSource
			existing.Level = monitorAlertLevel(hit, triggerSource)
			existing.Summary = hit.Summary
			existing.OccurrenceCount++
			existing.LastSeenAt = now
			existing.TriggeredAt = now
			existing.UpdatedAt = now
			existing.Evidence = mergeMonitorAlertEvidence(existing.Evidence, evidence, hit)
			if err := updatePortfolioSentinelAlertTx(ctx, tx, existing); err != nil {
				return StockV2Alert{}, err
			}
			return existing, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return StockV2Alert{}, wrapError(err, "find portfolio sentinel alert by dedupe key")
		}
	}

	alert := StockV2Alert{
		ID:              generateID(),
		MonitorHitID:    hit.ID,
		MonitorRunID:    hit.RunID,
		TaskType:        hit.TaskType,
		StrategyID:      hit.StrategyID,
		PortfolioID:     hit.PortfolioID,
		Symbol:          hit.Symbol,
		Market:          hit.Market,
		ReviewID:        review.ID,
		ReviewStatus:    review.Status,
		TriggerSource:   triggerSource,
		Status:          AlertStatusOpen,
		Level:           monitorAlertLevel(hit, triggerSource),
		Title:           strings.TrimSpace(hit.Title),
		Summary:         strings.TrimSpace(hit.Summary),
		DedupeKey:       dedupeKey,
		Evidence:        mergeMonitorAlertEvidence(nil, evidence, hit),
		OccurrenceCount: 1,
		FirstSeenAt:     now,
		LastSeenAt:      now,
		TriggeredAt:     now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if alert.Title == "" {
		alert.Title = "监控提醒"
	}
	if err := insertPortfolioSentinelAlertTx(ctx, tx, alert); err != nil {
		return StockV2Alert{}, err
	}
	return alert, nil
}

func insertPortfolioSentinelAlertTx(ctx context.Context, tx *sql.Tx, alert StockV2Alert) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_alerts (
			id, watch_id, monitor_hit_id, monitor_run_id, task_type, strategy_id,
			portfolio_id, symbol, market, review_id, review_status, agent_run_id,
			decision_ledger_id, trigger_source, status, level, title, summary,
			dedupe_key, evidence_json, occurrence_count, first_seen_at, last_seen_at,
			triggered_at, acknowledged_at, resolved_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, alert.ID, nullableString(alert.WatchID), nullableString(alert.MonitorHitID), nullableString(alert.MonitorRunID),
		nullableString(alert.TaskType), nullableString(alert.StrategyID), nullableString(alert.PortfolioID),
		nullableString(alert.Symbol), nullableString(alert.Market), nullableString(alert.ReviewID),
		nullableString(alert.ReviewStatus), nullableString(alert.AgentRunID), nullableString(alert.DecisionLedgerID),
		nullableString(alert.TriggerSource), alert.Status, alert.Level, alert.Title, nullableString(alert.Summary),
		nullableString(alert.DedupeKey), marshalMap(alert.Evidence), alert.OccurrenceCount, nullableTime(alert.FirstSeenAt),
		nullableTime(alert.LastSeenAt), alert.TriggeredAt, nullableTime(alert.AcknowledgedAt), nullableTime(alert.ResolvedAt),
		alert.CreatedAt, alert.UpdatedAt)
	return wrapError(err, "create portfolio sentinel alert")
}

func updatePortfolioSentinelAlertTx(ctx context.Context, tx *sql.Tx, alert StockV2Alert) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_alerts
		SET watch_id=?, monitor_hit_id=?, monitor_run_id=?, task_type=?, strategy_id=?,
		    portfolio_id=?, symbol=?, market=?, review_id=?, review_status=?, agent_run_id=?,
		    decision_ledger_id=?, trigger_source=?, status=?, level=?, title=?, summary=?,
		    dedupe_key=?, evidence_json=?, occurrence_count=?, first_seen_at=?, last_seen_at=?,
		    triggered_at=?, acknowledged_at=?, resolved_at=?, updated_at=?
		WHERE id=?
	`, nullableString(alert.WatchID), nullableString(alert.MonitorHitID), nullableString(alert.MonitorRunID),
		nullableString(alert.TaskType), nullableString(alert.StrategyID), nullableString(alert.PortfolioID),
		nullableString(alert.Symbol), nullableString(alert.Market), nullableString(alert.ReviewID),
		nullableString(alert.ReviewStatus), nullableString(alert.AgentRunID), nullableString(alert.DecisionLedgerID),
		nullableString(alert.TriggerSource), alert.Status, alert.Level, alert.Title, nullableString(alert.Summary),
		nullableString(alert.DedupeKey), marshalMap(alert.Evidence), alert.OccurrenceCount, nullableTime(alert.FirstSeenAt),
		nullableTime(alert.LastSeenAt), alert.TriggeredAt, nullableTime(alert.AcknowledgedAt), nullableTime(alert.ResolvedAt),
		alert.UpdatedAt, alert.ID)
	if err != nil {
		return wrapError(err, "update portfolio sentinel alert")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrAlertNotFound
	}
	return nil
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
