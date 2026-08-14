package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ensureOpportunityMarketScanSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_opportunity_market_scan_config (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			last_scanned_trade_date TEXT,
			last_run_id TEXT,
			last_run_status TEXT,
			last_run_at DATETIME,
			last_success_at DATETIME,
			last_error TEXT,
			updated_at DATETIME NOT NULL
		);
		INSERT OR IGNORE INTO stockv2_opportunity_market_scan_config (id, enabled, updated_at)
		VALUES ('default', 0, datetime('now'));

		CREATE TABLE IF NOT EXISTS stockv2_opportunity_market_scan_runs (
			id TEXT PRIMARY KEY,
			trigger_type TEXT NOT NULL,
			requested_by TEXT,
			status TEXT NOT NULL,
			trade_date TEXT,
			source_update_job_id TEXT,
			opportunity_id TEXT,
			discovery_run_id TEXT,
			strategy_agent_run_id TEXT,
			universe_count INTEGER NOT NULL DEFAULT 0,
			covered_count INTEGER NOT NULL DEFAULT 0,
			prefilter_count INTEGER NOT NULL DEFAULT 0,
			enriched_count INTEGER NOT NULL DEFAULT 0,
			research_count INTEGER NOT NULL DEFAULT 0,
			final_candidate_count INTEGER NOT NULL DEFAULT 0,
			strategy_requested_count INTEGER NOT NULL DEFAULT 0,
			strategy_created_count INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at DATETIME,
			error_message TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_market_scan_runs_status
			ON stockv2_opportunity_market_scan_runs(status, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_market_scan_runs_trade_date
			ON stockv2_opportunity_market_scan_runs(trade_date, trigger_type);

		CREATE TABLE IF NOT EXISTS stockv2_opportunity_market_scan_candidates (
			id TEXT PRIMARY KEY,
			scan_run_id TEXT NOT NULL,
			symbol TEXT NOT NULL,
			market TEXT NOT NULL,
			name TEXT NOT NULL,
			industry TEXT,
			sector TEXT,
			concepts_json TEXT NOT NULL DEFAULT '[]',
			stage TEXT NOT NULL,
			prefilter_rank INTEGER NOT NULL DEFAULT 0,
			final_rank INTEGER NOT NULL DEFAULT 0,
			prefilter_score REAL NOT NULL DEFAULT 0,
			final_score REAL NOT NULL DEFAULT 0,
			flow_score REAL NOT NULL DEFAULT 0,
			theme_score REAL NOT NULL DEFAULT 0,
			risk_penalty REAL NOT NULL DEFAULT 0,
			metrics_json TEXT NOT NULL DEFAULT '{}',
			exclusion_reason TEXT,
			opportunity_candidate_id TEXT,
			strategy_status TEXT,
			strategy_id TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(scan_run_id, symbol),
			FOREIGN KEY(scan_run_id) REFERENCES stockv2_opportunity_market_scan_runs(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_market_scan_candidates_run_rank
			ON stockv2_opportunity_market_scan_candidates(scan_run_id, final_rank, prefilter_rank);
		CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_market_scan_candidates_symbol
			ON stockv2_opportunity_market_scan_candidates(symbol, updated_at DESC);
	`)
	if err != nil {
		return wrapError(err, "ensure opportunity market scan schema")
	}
	if err := s.ensureDecisionGateSchema(ctx); err != nil {
		return err
	}
	columns := []struct{ table, name, definition string }{
		{"stockv2_opportunity_market_scan_config", "primary_fund_flow_api_key", "TEXT"},
		{"stockv2_opportunity_market_scan_config", "backup_fund_flow_api_key", "TEXT"},
		{"stockv2_opportunity_market_scan_config", "backup_fund_flow_proxy", "TEXT"},
		{"stockv2_opportunity_market_scan_runs", "fund_flow_requested_count", "INTEGER NOT NULL DEFAULT 0"},
		{"stockv2_opportunity_market_scan_runs", "fund_flow_available_count", "INTEGER NOT NULL DEFAULT 0"},
		{"stockv2_opportunity_market_scan_runs", "fund_flow_source", "TEXT"},
		{"stockv2_opportunity_market_scan_runs", "fund_flow_used", "INTEGER NOT NULL DEFAULT 0"},
		{"stockv2_opportunity_market_scan_runs", "fund_flow_status", "TEXT"},
		{"stockv2_opportunity_market_scan_runs", "fund_flow_error", "TEXT"},
	}
	for _, column := range columns {
		if err := s.ensureOpportunityMarketScanColumn(ctx, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	// Completed historical research rows not selected by the Agent are terminal
	// review outcomes, not pending work.
	_, err = s.db.ExecContext(ctx, `UPDATE stockv2_opportunity_market_scan_candidates
		SET stage=?, exclusion_reason=CASE WHEN COALESCE(exclusion_reason,'')='' THEN 'Agent 复核未入选' ELSE exclusion_reason END
		WHERE stage=? AND scan_run_id IN (
			SELECT id FROM stockv2_opportunity_market_scan_runs WHERE status IN (?, ?)
		)`, OpportunityMarketScanCandidateReviewedOut, OpportunityMarketScanCandidateResearch,
		OpportunityMarketScanStatusCompleted, OpportunityMarketScanStatusPartial)
	return wrapError(err, "backfill opportunity market reviewed outcomes")
}

func (s *Store) ensureOpportunityMarketScanColumn(ctx context.Context, table, name, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return wrapError(err, "inspect opportunity market scan schema")
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return wrapError(err, "scan opportunity market scan schema")
		}
		if columnName == name {
			return nil
		}
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, definition))
	return wrapError(err, "extend opportunity market scan schema")
}

func (s *Store) GetOpportunityMarketScanConfig(ctx context.Context) (OpportunityMarketScanConfig, error) {
	var item OpportunityMarketScanConfig
	var lastRunAt, lastSuccessAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id, enabled, COALESCE(last_scanned_trade_date,''),
		COALESCE(last_run_id,''), COALESCE(last_run_status,''), last_run_at, last_success_at,
		COALESCE(last_error,''), updated_at, COALESCE(primary_fund_flow_api_key,''),
		COALESCE(backup_fund_flow_api_key,''), COALESCE(backup_fund_flow_proxy,'')
		FROM stockv2_opportunity_market_scan_config WHERE id=?`, OpportunityMarketScanConfigID).Scan(
		&item.ID, &item.Enabled, &item.LastScannedTradeDate, &item.LastRunID,
		&item.LastRunStatus, &lastRunAt, &lastSuccessAt, &item.LastError, &item.UpdatedAt,
		&item.PrimaryFundFlowAPIKey, &item.BackupFundFlowAPIKey, &item.BackupFundFlowProxy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OpportunityMarketScanConfig{}, ErrOpportunityMarketScanConfigNotFound
	}
	if err != nil {
		return OpportunityMarketScanConfig{}, wrapError(err, "get opportunity market scan config")
	}
	if lastRunAt.Valid {
		item.LastRunAt = lastRunAt.Time
	}
	if lastSuccessAt.Valid {
		item.LastSuccessAt = lastSuccessAt.Time
	}
	item.PrimaryFundFlowConfigured = item.PrimaryFundFlowAPIKey != ""
	item.BackupFundFlowConfigured = item.BackupFundFlowAPIKey != ""
	item.BackupFundFlowProxyConfigured = item.BackupFundFlowProxy != ""
	return item, nil
}

func (s *Store) SaveOpportunityMarketScanConfig(ctx context.Context, item OpportunityMarketScanConfig) (OpportunityMarketScanConfig, error) {
	if item.ID == "" {
		item.ID = OpportunityMarketScanConfigID
	}
	item.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO stockv2_opportunity_market_scan_config
		(id, enabled, last_scanned_trade_date, last_run_id, last_run_status, last_run_at,
		 last_success_at, last_error, updated_at, primary_fund_flow_api_key,
		 backup_fund_flow_api_key, backup_fund_flow_proxy) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Enabled, nullableString(item.LastScannedTradeDate), nullableString(item.LastRunID),
		nullableString(item.LastRunStatus), nullableTime(item.LastRunAt), nullableTime(item.LastSuccessAt),
		nullableString(item.LastError), item.UpdatedAt, nullableString(item.PrimaryFundFlowAPIKey),
		nullableString(item.BackupFundFlowAPIKey), nullableString(item.BackupFundFlowProxy))
	return item, wrapError(err, "save opportunity market scan config")
}

const opportunityMarketScanRunSelectSQL = `SELECT id, trigger_type, COALESCE(requested_by,''), status,
	COALESCE(trade_date,''), COALESCE(source_update_job_id,''), COALESCE(opportunity_id,''),
	COALESCE(discovery_run_id,''), COALESCE(strategy_agent_run_id,''), universe_count, covered_count,
	prefilter_count, enriched_count, research_count, final_candidate_count,
	strategy_requested_count, strategy_created_count, retry_count, next_retry_at,
	fund_flow_requested_count, fund_flow_available_count, COALESCE(fund_flow_source,''),
	fund_flow_used, COALESCE(fund_flow_status,''), COALESCE(fund_flow_error,''),
	COALESCE(error_message,''), started_at, finished_at, created_at, updated_at
	FROM stockv2_opportunity_market_scan_runs`

func scanOpportunityMarketScanRun(row rowScanner) (OpportunityMarketScanRun, error) {
	var item OpportunityMarketScanRun
	var nextRetry, started, finished sql.NullTime
	err := row.Scan(&item.ID, &item.TriggerType, &item.RequestedBy, &item.Status,
		&item.TradeDate, &item.SourceUpdateJobID, &item.OpportunityID, &item.DiscoveryRunID,
		&item.StrategyAgentRunID, &item.UniverseCount, &item.CoveredCount, &item.PrefilterCount,
		&item.EnrichedCount, &item.ResearchCount, &item.FinalCandidateCount,
		&item.StrategyRequestedCount, &item.StrategyCreatedCount, &item.RetryCount,
		&nextRetry,
		&item.FundFlowRequestedCount, &item.FundFlowAvailableCount, &item.FundFlowSource,
		&item.FundFlowUsed, &item.FundFlowStatus, &item.FundFlowError,
		&item.ErrorMessage, &started, &finished, &item.CreatedAt, &item.UpdatedAt)
	if nextRetry.Valid {
		item.NextRetryAt = nextRetry.Time
	}
	if started.Valid {
		item.StartedAt = started.Time
	}
	if finished.Valid {
		item.FinishedAt = finished.Time
	}
	return item, err
}

func (s *Store) CreateOpportunityMarketScanRun(ctx context.Context, item OpportunityMarketScanRun) (OpportunityMarketScanRun, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.Status == "" {
		item.Status = OpportunityMarketScanStatusPending
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO stockv2_opportunity_market_scan_runs
		(id, trigger_type, requested_by, status, trade_date, source_update_job_id, opportunity_id,
		 discovery_run_id, strategy_agent_run_id, universe_count, covered_count, prefilter_count,
		 enriched_count, research_count, final_candidate_count, strategy_requested_count,
		 strategy_created_count, retry_count, next_retry_at, error_message, started_at, finished_at,
		 created_at, updated_at, fund_flow_requested_count, fund_flow_available_count, fund_flow_source,
		 fund_flow_used, fund_flow_status, fund_flow_error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.TriggerType, nullableString(item.RequestedBy), item.Status, nullableString(item.TradeDate),
		nullableString(item.SourceUpdateJobID), nullableString(item.OpportunityID), nullableString(item.DiscoveryRunID),
		nullableString(item.StrategyAgentRunID), item.UniverseCount, item.CoveredCount, item.PrefilterCount,
		item.EnrichedCount, item.ResearchCount, item.FinalCandidateCount, item.StrategyRequestedCount,
		item.StrategyCreatedCount, item.RetryCount, nullableTime(item.NextRetryAt), nullableString(item.ErrorMessage),
		nullableTime(item.StartedAt), nullableTime(item.FinishedAt), item.CreatedAt, item.UpdatedAt,
		item.FundFlowRequestedCount, item.FundFlowAvailableCount, nullableString(item.FundFlowSource),
		item.FundFlowUsed, nullableString(item.FundFlowStatus), nullableString(item.FundFlowError))
	return item, wrapError(err, "create opportunity market scan run")
}

func (s *Store) UpdateOpportunityMarketScanRun(ctx context.Context, item OpportunityMarketScanRun) (OpportunityMarketScanRun, error) {
	item.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_opportunity_market_scan_runs SET
		trigger_type=?, requested_by=?, status=?, trade_date=?, source_update_job_id=?, opportunity_id=?,
		discovery_run_id=?, strategy_agent_run_id=?, universe_count=?, covered_count=?, prefilter_count=?,
		enriched_count=?, research_count=?, final_candidate_count=?, strategy_requested_count=?,
		strategy_created_count=?, retry_count=?, next_retry_at=?, error_message=?, started_at=?, finished_at=?,
		fund_flow_requested_count=?, fund_flow_available_count=?, fund_flow_source=?, fund_flow_used=?,
		fund_flow_status=?, fund_flow_error=?, updated_at=? WHERE id=?`, item.TriggerType, nullableString(item.RequestedBy), item.Status,
		nullableString(item.TradeDate), nullableString(item.SourceUpdateJobID), nullableString(item.OpportunityID),
		nullableString(item.DiscoveryRunID), nullableString(item.StrategyAgentRunID), item.UniverseCount,
		item.CoveredCount, item.PrefilterCount, item.EnrichedCount, item.ResearchCount,
		item.FinalCandidateCount, item.StrategyRequestedCount, item.StrategyCreatedCount, item.RetryCount,
		nullableTime(item.NextRetryAt), nullableString(item.ErrorMessage), nullableTime(item.StartedAt),
		nullableTime(item.FinishedAt), item.FundFlowRequestedCount, item.FundFlowAvailableCount,
		nullableString(item.FundFlowSource), item.FundFlowUsed, nullableString(item.FundFlowStatus),
		nullableString(item.FundFlowError), item.UpdatedAt, item.ID)
	if err != nil {
		return OpportunityMarketScanRun{}, wrapError(err, "update opportunity market scan run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanRunNotFound
	}
	return item, nil
}

func (s *Store) GetOpportunityMarketScanRun(ctx context.Context, id string) (OpportunityMarketScanRun, error) {
	item, err := scanOpportunityMarketScanRun(s.db.QueryRowContext(ctx, opportunityMarketScanRunSelectSQL+" WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanRunNotFound
	}
	return item, wrapError(err, "get opportunity market scan run")
}

func (s *Store) GetOpportunityMarketScanRunByDiscoveryRunID(ctx context.Context, id string) (OpportunityMarketScanRun, error) {
	item, err := scanOpportunityMarketScanRun(s.db.QueryRowContext(ctx, opportunityMarketScanRunSelectSQL+" WHERE discovery_run_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanRunNotFound
	}
	return item, wrapError(err, "get opportunity market scan run by discovery run")
}

func (s *Store) ListOpportunityMarketScanRuns(ctx context.Context, filter OpportunityMarketScanRunListFilter) ([]OpportunityMarketScanRun, error) {
	where := "1=1"
	args := []any{}
	if strings.TrimSpace(filter.Status) != "" {
		where = "status=?"
		args = append(args, strings.TrimSpace(filter.Status))
	}
	args = append(args, normalizedPageLimit(filter.Limit, 100), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, opportunityMarketScanRunSelectSQL+" WHERE "+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, wrapError(err, "list opportunity market scan runs")
	}
	return scanRows(rows, scanOpportunityMarketScanRun, "scan opportunity market scan run", "iterate opportunity market scan runs")
}

func (s *Store) CountOpportunityMarketScanRuns(ctx context.Context, filter OpportunityMarketScanRunListFilter) (int, error) {
	query := "SELECT COUNT(*) FROM stockv2_opportunity_market_scan_runs"
	args := []any{}
	if strings.TrimSpace(filter.Status) != "" {
		query += " WHERE status=?"
		args = append(args, strings.TrimSpace(filter.Status))
	}
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, wrapError(err, "count opportunity market scan runs")
}

func (s *Store) GetActiveOpportunityMarketScanRun(ctx context.Context) (*OpportunityMarketScanRun, error) {
	row := s.db.QueryRowContext(ctx, opportunityMarketScanRunSelectSQL+` WHERE status IN (?,?,?,?,?) ORDER BY created_at DESC LIMIT 1`,
		OpportunityMarketScanStatusPending, OpportunityMarketScanStatusPrefiltering,
		OpportunityMarketScanStatusEnriching, OpportunityMarketScanStatusResearching,
		OpportunityMarketScanStatusDrafting)
	item, err := scanOpportunityMarketScanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrapError(err, "get active opportunity market scan run")
	}
	return &item, nil
}

func (s *Store) UpsertOpportunityMarketScanCandidates(ctx context.Context, items []OpportunityMarketScanCandidate) error {
	if len(items) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now()
		for _, item := range items {
			if item.ID == "" {
				item.ID = generateID()
			}
			if item.CreatedAt.IsZero() {
				item.CreatedAt = now
			}
			item.UpdatedAt = now
			concepts, _ := json.Marshal(item.Concepts)
			metrics, _ := json.Marshal(item.Metrics)
			_, err := tx.ExecContext(ctx, `INSERT INTO stockv2_opportunity_market_scan_candidates
				(id, scan_run_id, symbol, market, name, industry, sector, concepts_json, stage,
				 prefilter_rank, final_rank, prefilter_score, final_score, flow_score, theme_score,
				 risk_penalty, metrics_json, exclusion_reason, opportunity_candidate_id,
				 strategy_status, strategy_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(scan_run_id, symbol) DO UPDATE SET market=excluded.market, name=excluded.name,
				 industry=excluded.industry, sector=excluded.sector, concepts_json=excluded.concepts_json,
				 stage=excluded.stage, prefilter_rank=excluded.prefilter_rank, final_rank=excluded.final_rank,
				 prefilter_score=excluded.prefilter_score, final_score=excluded.final_score,
				 flow_score=excluded.flow_score, theme_score=excluded.theme_score,
				 risk_penalty=excluded.risk_penalty, metrics_json=excluded.metrics_json,
				 exclusion_reason=excluded.exclusion_reason,
				 opportunity_candidate_id=excluded.opportunity_candidate_id,
				 strategy_status=excluded.strategy_status, strategy_id=excluded.strategy_id,
				 updated_at=excluded.updated_at`, item.ID, item.ScanRunID, item.Symbol, item.Market,
				item.Name, nullableString(item.Industry), nullableString(item.Sector), string(concepts), item.Stage,
				item.PrefilterRank, item.FinalRank, item.PrefilterScore, item.FinalScore, item.FlowScore,
				item.ThemeScore, item.RiskPenalty, string(metrics), nullableString(item.ExclusionReason),
				nullableString(item.OpportunityCandidateID), nullableString(item.StrategyStatus),
				nullableString(item.StrategyID), item.CreatedAt, item.UpdatedAt)
			if err != nil {
				return wrapError(err, "upsert opportunity market scan candidate")
			}
		}
		return nil
	})
}

const opportunityMarketScanCandidateSelectSQL = `SELECT id, scan_run_id, symbol, market, name,
	COALESCE(industry,''), COALESCE(sector,''), concepts_json, stage, prefilter_rank, final_rank,
	prefilter_score, final_score, flow_score, theme_score, risk_penalty, metrics_json,
	COALESCE(exclusion_reason,''), COALESCE(opportunity_candidate_id,''),
	COALESCE(strategy_status,''), COALESCE(strategy_id,''), created_at, updated_at
	FROM stockv2_opportunity_market_scan_candidates`

func scanOpportunityMarketScanCandidate(row rowScanner) (OpportunityMarketScanCandidate, error) {
	var item OpportunityMarketScanCandidate
	var conceptsJSON, metricsJSON string
	err := row.Scan(&item.ID, &item.ScanRunID, &item.Symbol, &item.Market, &item.Name,
		&item.Industry, &item.Sector, &conceptsJSON, &item.Stage, &item.PrefilterRank,
		&item.FinalRank, &item.PrefilterScore, &item.FinalScore, &item.FlowScore,
		&item.ThemeScore, &item.RiskPenalty, &metricsJSON, &item.ExclusionReason,
		&item.OpportunityCandidateID, &item.StrategyStatus, &item.StrategyID,
		&item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal([]byte(conceptsJSON), &item.Concepts)
		_ = json.Unmarshal([]byte(metricsJSON), &item.Metrics)
	}
	return item, err
}

func (s *Store) ListOpportunityMarketScanCandidates(ctx context.Context, filter OpportunityMarketScanCandidateListFilter) ([]OpportunityMarketScanCandidate, error) {
	where := []string{"scan_run_id=?"}
	args := []any{strings.TrimSpace(filter.ScanRunID)}
	if strings.TrimSpace(filter.Stage) != "" {
		where = append(where, "stage=?")
		args = append(args, strings.TrimSpace(filter.Stage))
	}
	if strings.TrimSpace(filter.DecisionStatus) != "" {
		where = append(where, "COALESCE(json_extract(metrics_json,'$.decisionStatus'),'')=?")
		args = append(args, strings.TrimSpace(filter.DecisionStatus))
	}
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, opportunityMarketScanCandidateSelectSQL+" WHERE "+strings.Join(where, " AND ")+` ORDER BY
		CASE WHEN final_rank > 0 THEN 0 ELSE 1 END, final_rank ASC, prefilter_rank ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list opportunity market scan candidates")
	}
	return scanRows(rows, scanOpportunityMarketScanCandidate, "scan opportunity market scan candidate", "iterate opportunity market scan candidates")
}

func (s *Store) CountOpportunityMarketScanCandidates(ctx context.Context, filter OpportunityMarketScanCandidateListFilter) (int, error) {
	where := []string{"scan_run_id=?"}
	args := []any{strings.TrimSpace(filter.ScanRunID)}
	if strings.TrimSpace(filter.Stage) != "" {
		where = append(where, "stage=?")
		args = append(args, strings.TrimSpace(filter.Stage))
	}
	if strings.TrimSpace(filter.DecisionStatus) != "" {
		where = append(where, "COALESCE(json_extract(metrics_json,'$.decisionStatus'),'')=?")
		args = append(args, strings.TrimSpace(filter.DecisionStatus))
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM stockv2_opportunity_market_scan_candidates WHERE "+strings.Join(where, " AND "), args...).Scan(&count)
	return count, wrapError(err, "count opportunity market scan candidates")
}

func (s *Store) DeleteOpportunityMarketScanCandidates(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_opportunity_market_scan_candidates WHERE scan_run_id=?`, runID)
	return wrapError(err, "delete opportunity market scan candidates")
}

func (s *Store) GetOpportunityByCreatedBy(ctx context.Context, createdBy string) (Opportunity, error) {
	item, err := scanOpportunity(s.db.QueryRowContext(ctx, opportunitySelectSQL+" WHERE created_by=? ORDER BY created_at LIMIT 1", createdBy))
	if errors.Is(err, sql.ErrNoRows) {
		return Opportunity{}, ErrOpportunityNotFound
	}
	return item, wrapError(err, fmt.Sprintf("get opportunity by creator %s", createdBy))
}
