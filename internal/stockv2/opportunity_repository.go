package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type opportunityStepDefinition struct {
	Key   string
	Title string
}

var defaultOpportunityDiscoverySteps = []opportunityStepDefinition{
	{Key: "understand_theme", Title: "主题理解"},
	{Key: "internal_recall", Title: "项目内资料召回"},
	{Key: "external_research", Title: "外部公开资料搜索"},
	{Key: "theme_chain", Title: "产业链 / 主题链条拆解"},
	{Key: "candidate_merge", Title: "候选合并与去噪"},
	{Key: "market_risk_check", Title: "行情与风险检查"},
	{Key: "candidate_ranking", Title: "候选排序"},
	{Key: "final_report", Title: "最终报告"},
}

func normalizedOpportunityLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizedOpportunityOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func opportunityFilterAdd(where *[]string, args *[]any, column, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, strings.TrimSpace(value))
}

func nullableOpportunityString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableOpportunityTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

const opportunitySelectSQL = `
	SELECT id, title, user_thesis, market_scope, instrument_scope, status,
	       COALESCE(created_by,''), created_at, updated_at
	FROM stockv2_opportunities
`

func scanOpportunity(row rowScanner) (Opportunity, error) {
	var item Opportunity
	if err := row.Scan(
		&item.ID, &item.Title, &item.UserThesis, &item.MarketScope, &item.InstrumentScope,
		&item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return Opportunity{}, err
	}
	return item, nil
}

func (s *Store) CreateOpportunity(ctx context.Context, item Opportunity) (Opportunity, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunities
			(id, title, user_thesis, market_scope, instrument_scope, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.Title, item.UserThesis, item.MarketScope, item.InstrumentScope, item.Status,
		nullableOpportunityString(item.CreatedBy), item.CreatedAt, item.UpdatedAt)
	return item, wrapError(err, "create opportunity")
}

func (s *Store) GetOpportunity(ctx context.Context, id string) (Opportunity, error) {
	item, err := scanOpportunity(s.db.QueryRowContext(ctx, opportunitySelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Opportunity{}, ErrOpportunityNotFound
		}
		return Opportunity{}, wrapError(err, "get opportunity")
	}
	return item, nil
}

func (s *Store) UpdateOpportunity(ctx context.Context, item Opportunity) (Opportunity, error) {
	item.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_opportunities
		SET title = ?, user_thesis = ?, market_scope = ?, instrument_scope = ?,
		    status = ?, created_by = ?, updated_at = ?
		WHERE id = ?
	`, item.Title, item.UserThesis, item.MarketScope, item.InstrumentScope, item.Status,
		nullableOpportunityString(item.CreatedBy), item.UpdatedAt, item.ID)
	if err != nil {
		return Opportunity{}, wrapError(err, "update opportunity")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Opportunity{}, ErrOpportunityNotFound
	}
	return item, nil
}

func (s *Store) DeleteOpportunity(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin delete opportunity")
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM stockv2_opportunity_results WHERE run_id IN (SELECT id FROM stockv2_opportunity_discovery_runs WHERE opportunity_id = ?)`,
		`DELETE FROM stockv2_opportunity_evidence WHERE run_id IN (SELECT id FROM stockv2_opportunity_discovery_runs WHERE opportunity_id = ?)`,
		`DELETE FROM stockv2_opportunity_candidates WHERE opportunity_id = ?`,
		`DELETE FROM stockv2_opportunity_discovery_steps WHERE run_id IN (SELECT id FROM stockv2_opportunity_discovery_runs WHERE opportunity_id = ?)`,
		`DELETE FROM stockv2_opportunity_discovery_runs WHERE opportunity_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			return wrapError(err, "delete opportunity children")
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stockv2_opportunities WHERE id = ?`, id)
	if err != nil {
		return wrapError(err, "delete opportunity")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrOpportunityNotFound
	}
	return wrapError(tx.Commit(), "commit delete opportunity")
}

func opportunityWhere(filter OpportunityListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	opportunityFilterAdd(&where, &args, "status", filter.Status)
	opportunityFilterAdd(&where, &args, "market_scope", filter.MarketScope)
	opportunityFilterAdd(&where, &args, "instrument_scope", filter.InstrumentScope)
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		where = append(where, "(LOWER(title) LIKE ? OR LOWER(user_thesis) LIKE ?)")
		args = append(args, pattern, pattern)
	}
	return strings.Join(where, " AND "), args
}

func (s *Store) ListOpportunities(ctx context.Context, filter OpportunityListFilter) ([]Opportunity, error) {
	where, args := opportunityWhere(filter)
	args = append(args, normalizedOpportunityLimit(filter.Limit), normalizedOpportunityOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY updated_at DESC LIMIT ? OFFSET ?`, opportunitySelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list opportunities")
	}
	defer rows.Close()
	items := make([]Opportunity, 0)
	for rows.Next() {
		item, err := scanOpportunity(rows)
		if err != nil {
			return nil, wrapError(err, "scan opportunity")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate opportunities")
	}
	return items, nil
}

func (s *Store) CountOpportunities(ctx context.Context, filter OpportunityListFilter) (int, error) {
	where, args := opportunityWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_opportunities WHERE %s`, where), args...).Scan(&total)
	return total, wrapError(err, "count opportunities")
}

const opportunityRunSelectSQL = `
	SELECT id, opportunity_id, COALESCE(agent_run_id,''), status, COALESCE(current_step_id,''),
	       step_total, step_completed, candidate_count, evidence_count, external_source_count,
	       started_at, finished_at, COALESCE(error_message,''), created_at, updated_at
	FROM stockv2_opportunity_discovery_runs
`

func scanOpportunityRun(row rowScanner) (OpportunityDiscoveryRun, error) {
	var item OpportunityDiscoveryRun
	var started, finished sql.NullTime
	if err := row.Scan(
		&item.ID, &item.OpportunityID, &item.AgentRunID, &item.Status, &item.CurrentStepID,
		&item.StepTotal, &item.StepCompleted, &item.CandidateCount, &item.EvidenceCount,
		&item.ExternalSourceCount, &started, &finished, &item.ErrorMessage,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	if started.Valid {
		item.StartedAt = started.Time
	}
	if finished.Valid {
		item.FinishedAt = finished.Time
	}
	return item, nil
}

func (s *Store) CreateOpportunityDiscoveryRun(ctx context.Context, run OpportunityDiscoveryRun, steps []OpportunityDiscoveryStep) (OpportunityDiscoveryRun, []OpportunityDiscoveryStep, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OpportunityDiscoveryRun{}, nil, wrapError(err, "begin create opportunity discovery run")
	}
	defer tx.Rollback()
	now := time.Now()
	if run.ID == "" {
		run.ID = generateID()
	}
	if run.Status == "" {
		run.Status = OpportunityDiscoveryRunStatusPending
	}
	run.StepTotal = len(steps)
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_discovery_runs
			(id, opportunity_id, agent_run_id, status, current_step_id, step_total, step_completed,
			 candidate_count, evidence_count, external_source_count, started_at, finished_at,
			 error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.OpportunityID, nullableOpportunityString(run.AgentRunID), run.Status,
		nullableOpportunityString(run.CurrentStepID), run.StepTotal, run.StepCompleted,
		run.CandidateCount, run.EvidenceCount, run.ExternalSourceCount,
		nullableOpportunityTime(run.StartedAt), nullableOpportunityTime(run.FinishedAt),
		nullableOpportunityString(run.ErrorMessage), run.CreatedAt, run.UpdatedAt); err != nil {
		return OpportunityDiscoveryRun{}, nil, wrapError(err, "insert opportunity discovery run")
	}
	for i := range steps {
		if steps[i].ID == "" {
			steps[i].ID = generateID()
		}
		steps[i].RunID = run.ID
		if steps[i].Status == "" {
			steps[i].Status = OpportunityDiscoveryStepStatusPending
		}
		if steps[i].Metadata == nil {
			steps[i].Metadata = map[string]any{}
		}
		if steps[i].CreatedAt.IsZero() {
			steps[i].CreatedAt = now
		}
		steps[i].UpdatedAt = now
		if err := insertOpportunityStepWithTx(ctx, tx, steps[i]); err != nil {
			return OpportunityDiscoveryRun{}, nil, err
		}
	}
	return run, steps, wrapError(tx.Commit(), "commit create opportunity discovery run")
}

func insertOpportunityStepWithTx(ctx context.Context, tx *sql.Tx, step OpportunityDiscoveryStep) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_discovery_steps
			(id, run_id, step_key, step_title, status, order_index, input_summary,
			 output_summary, metadata_json, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, step.ID, step.RunID, step.StepKey, step.StepTitle, step.Status, step.OrderIndex,
		nullableOpportunityString(step.InputSummary), nullableOpportunityString(step.OutputSummary),
		marshalMap(step.Metadata), nullableOpportunityTime(step.StartedAt),
		nullableOpportunityTime(step.FinishedAt), step.CreatedAt, step.UpdatedAt)
	return wrapError(err, "insert opportunity discovery step")
}

func (s *Store) GetOpportunityDiscoveryRun(ctx context.Context, id string) (OpportunityDiscoveryRun, error) {
	item, err := scanOpportunityRun(s.db.QueryRowContext(ctx, opportunityRunSelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityDiscoveryRun{}, ErrDiscoveryRunNotFound
		}
		return OpportunityDiscoveryRun{}, wrapError(err, "get opportunity discovery run")
	}
	return item, nil
}

func (s *Store) GetOpportunityDiscoveryRunByAgentRunID(ctx context.Context, agentRunID string) (OpportunityDiscoveryRun, error) {
	item, err := scanOpportunityRun(s.db.QueryRowContext(ctx, opportunityRunSelectSQL+" WHERE agent_run_id = ?", agentRunID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityDiscoveryRun{}, ErrDiscoveryRunNotFound
		}
		return OpportunityDiscoveryRun{}, wrapError(err, "get opportunity discovery run by agent run")
	}
	return item, nil
}

func (s *Store) UpdateOpportunityDiscoveryRun(ctx context.Context, run OpportunityDiscoveryRun) (OpportunityDiscoveryRun, error) {
	run.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_opportunity_discovery_runs
		SET agent_run_id = ?, status = ?, current_step_id = ?, step_total = ?, step_completed = ?,
		    candidate_count = ?, evidence_count = ?, external_source_count = ?, started_at = ?,
		    finished_at = ?, error_message = ?, updated_at = ?
		WHERE id = ?
	`, nullableOpportunityString(run.AgentRunID), run.Status, nullableOpportunityString(run.CurrentStepID),
		run.StepTotal, run.StepCompleted, run.CandidateCount, run.EvidenceCount, run.ExternalSourceCount,
		nullableOpportunityTime(run.StartedAt), nullableOpportunityTime(run.FinishedAt),
		nullableOpportunityString(run.ErrorMessage), run.UpdatedAt, run.ID)
	if err != nil {
		return OpportunityDiscoveryRun{}, wrapError(err, "update opportunity discovery run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return OpportunityDiscoveryRun{}, ErrDiscoveryRunNotFound
	}
	return run, nil
}

func opportunityRunWhere(filter DiscoveryRunListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	opportunityFilterAdd(&where, &args, "opportunity_id", filter.OpportunityID)
	opportunityFilterAdd(&where, &args, "status", filter.Status)
	return strings.Join(where, " AND "), args
}

func (s *Store) ListOpportunityDiscoveryRuns(ctx context.Context, filter DiscoveryRunListFilter) ([]OpportunityDiscoveryRun, error) {
	where, args := opportunityRunWhere(filter)
	args = append(args, normalizedOpportunityLimit(filter.Limit), normalizedOpportunityOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, opportunityRunSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list opportunity discovery runs")
	}
	defer rows.Close()
	items := make([]OpportunityDiscoveryRun, 0)
	for rows.Next() {
		item, err := scanOpportunityRun(rows)
		if err != nil {
			return nil, wrapError(err, "scan opportunity discovery run")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate opportunity discovery runs")
	}
	return items, nil
}

func (s *Store) CountOpportunityDiscoveryRuns(ctx context.Context, filter DiscoveryRunListFilter) (int, error) {
	where, args := opportunityRunWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_opportunity_discovery_runs WHERE %s`, where), args...).Scan(&total)
	return total, wrapError(err, "count opportunity discovery runs")
}

const opportunityStepSelectSQL = `
	SELECT id, run_id, step_key, step_title, status, order_index, COALESCE(input_summary,''),
	       COALESCE(output_summary,''), metadata_json, started_at, finished_at, created_at, updated_at
	FROM stockv2_opportunity_discovery_steps
`

func scanOpportunityStep(row rowScanner) (OpportunityDiscoveryStep, error) {
	var item OpportunityDiscoveryStep
	var metadataJSON string
	var started, finished sql.NullTime
	if err := row.Scan(
		&item.ID, &item.RunID, &item.StepKey, &item.StepTitle, &item.Status, &item.OrderIndex,
		&item.InputSummary, &item.OutputSummary, &metadataJSON, &started, &finished,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return OpportunityDiscoveryStep{}, err
	}
	item.Metadata = unmarshalMap(metadataJSON)
	if started.Valid {
		item.StartedAt = started.Time
	}
	if finished.Valid {
		item.FinishedAt = finished.Time
	}
	return item, nil
}

func (s *Store) GetOpportunityDiscoveryStep(ctx context.Context, id string) (OpportunityDiscoveryStep, error) {
	item, err := scanOpportunityStep(s.db.QueryRowContext(ctx, opportunityStepSelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityDiscoveryStep{}, ErrDiscoveryStepNotFound
		}
		return OpportunityDiscoveryStep{}, wrapError(err, "get opportunity discovery step")
	}
	return item, nil
}

func (s *Store) GetOpportunityDiscoveryStepByKey(ctx context.Context, runID, stepKey string) (OpportunityDiscoveryStep, error) {
	item, err := scanOpportunityStep(s.db.QueryRowContext(ctx, opportunityStepSelectSQL+" WHERE run_id = ? AND step_key = ?", runID, stepKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityDiscoveryStep{}, ErrDiscoveryStepNotFound
		}
		return OpportunityDiscoveryStep{}, wrapError(err, "get opportunity discovery step by key")
	}
	return item, nil
}

func (s *Store) UpdateOpportunityDiscoveryStep(ctx context.Context, step OpportunityDiscoveryStep) (OpportunityDiscoveryStep, error) {
	if step.Metadata == nil {
		step.Metadata = map[string]any{}
	}
	step.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_opportunity_discovery_steps
		SET status = ?, input_summary = ?, output_summary = ?, metadata_json = ?,
		    started_at = ?, finished_at = ?, updated_at = ?
		WHERE id = ?
	`, step.Status, nullableOpportunityString(step.InputSummary), nullableOpportunityString(step.OutputSummary),
		marshalMap(step.Metadata), nullableOpportunityTime(step.StartedAt),
		nullableOpportunityTime(step.FinishedAt), step.UpdatedAt, step.ID)
	if err != nil {
		return OpportunityDiscoveryStep{}, wrapError(err, "update opportunity discovery step")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return OpportunityDiscoveryStep{}, ErrDiscoveryStepNotFound
	}
	return step, nil
}

func opportunityStepWhere(filter DiscoveryStepListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	opportunityFilterAdd(&where, &args, "run_id", filter.RunID)
	opportunityFilterAdd(&where, &args, "status", filter.Status)
	return strings.Join(where, " AND "), args
}

func (s *Store) ListOpportunityDiscoverySteps(ctx context.Context, filter DiscoveryStepListFilter) ([]OpportunityDiscoveryStep, error) {
	where, args := opportunityStepWhere(filter)
	args = append(args, normalizedOpportunityLimit(filter.Limit), normalizedOpportunityOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY order_index ASC LIMIT ? OFFSET ?`, opportunityStepSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list opportunity discovery steps")
	}
	defer rows.Close()
	items := make([]OpportunityDiscoveryStep, 0)
	for rows.Next() {
		item, err := scanOpportunityStep(rows)
		if err != nil {
			return nil, wrapError(err, "scan opportunity discovery step")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate opportunity discovery steps")
}

func (s *Store) CountOpportunityDiscoverySteps(ctx context.Context, filter DiscoveryStepListFilter) (int, error) {
	where, args := opportunityStepWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_opportunity_discovery_steps WHERE %s`, where), args...).Scan(&total)
	return total, wrapError(err, "count opportunity discovery steps")
}

const opportunityCandidateSelectSQL = `
	SELECT id, opportunity_id, run_id, symbol, COALESCE(market,''), instrument_type, COALESCE(name,''),
	       relation_type, relevance_score, evidence_score, market_risk_score, confidence,
	       rank, status, COALESCE(reason,''), COALESCE(risk_summary,''), metadata_json,
	       created_at, updated_at
	FROM stockv2_opportunity_candidates
`

func scanOpportunityCandidate(row rowScanner) (OpportunityCandidate, error) {
	var item OpportunityCandidate
	var metadataJSON string
	if err := row.Scan(
		&item.ID, &item.OpportunityID, &item.RunID, &item.Symbol, &item.Market,
		&item.InstrumentType, &item.Name, &item.RelationType, &item.RelevanceScore,
		&item.EvidenceScore, &item.MarketRiskScore, &item.Confidence, &item.Rank,
		&item.Status, &item.Reason, &item.RiskSummary, &metadataJSON,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return OpportunityCandidate{}, err
	}
	item.Metadata = unmarshalMap(metadataJSON)
	return item, nil
}

func (s *Store) UpsertOpportunityCandidate(ctx context.Context, item OpportunityCandidate) (OpportunityCandidate, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_candidates
			(id, opportunity_id, run_id, symbol, market, instrument_type, name, relation_type,
			 relevance_score, evidence_score, market_risk_score, confidence, rank, status,
			 reason, risk_summary, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, symbol) DO UPDATE SET
			market = excluded.market,
			instrument_type = excluded.instrument_type,
			name = excluded.name,
			relation_type = excluded.relation_type,
			relevance_score = excluded.relevance_score,
			evidence_score = excluded.evidence_score,
			market_risk_score = excluded.market_risk_score,
			confidence = excluded.confidence,
			rank = excluded.rank,
			status = excluded.status,
			reason = excluded.reason,
			risk_summary = excluded.risk_summary,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, item.ID, item.OpportunityID, item.RunID, item.Symbol, item.Market, item.InstrumentType,
		item.Name, item.RelationType, item.RelevanceScore, item.EvidenceScore,
		item.MarketRiskScore, item.Confidence, item.Rank, item.Status,
		nullableOpportunityString(item.Reason), nullableOpportunityString(item.RiskSummary),
		marshalMap(item.Metadata), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return OpportunityCandidate{}, wrapError(err, "upsert opportunity candidate")
	}
	return s.GetOpportunityCandidateByRunSymbol(ctx, item.RunID, item.Symbol)
}

func (s *Store) GetOpportunityCandidate(ctx context.Context, id string) (OpportunityCandidate, error) {
	item, err := scanOpportunityCandidate(s.db.QueryRowContext(ctx, opportunityCandidateSelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityCandidate{}, ErrOpportunityCandidateNotFound
		}
		return OpportunityCandidate{}, wrapError(err, "get opportunity candidate")
	}
	return item, nil
}

func (s *Store) GetOpportunityCandidateByRunSymbol(ctx context.Context, runID, symbol string) (OpportunityCandidate, error) {
	item, err := scanOpportunityCandidate(s.db.QueryRowContext(ctx, opportunityCandidateSelectSQL+" WHERE run_id = ? AND symbol = ?", runID, symbol))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityCandidate{}, ErrOpportunityCandidateNotFound
		}
		return OpportunityCandidate{}, wrapError(err, "get opportunity candidate by symbol")
	}
	return item, nil
}

func (s *Store) UpdateOpportunityCandidate(ctx context.Context, item OpportunityCandidate) (OpportunityCandidate, error) {
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_opportunity_candidates
		SET status = ?, reason = ?, risk_summary = ?, metadata_json = ?, updated_at = ?
		WHERE id = ?
	`, item.Status, nullableOpportunityString(item.Reason), nullableOpportunityString(item.RiskSummary),
		marshalMap(item.Metadata), item.UpdatedAt, item.ID)
	if err != nil {
		return OpportunityCandidate{}, wrapError(err, "update opportunity candidate")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return OpportunityCandidate{}, ErrOpportunityCandidateNotFound
	}
	return item, nil
}

func opportunityCandidateWhere(filter OpportunityCandidateListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	opportunityFilterAdd(&where, &args, "opportunity_id", filter.OpportunityID)
	opportunityFilterAdd(&where, &args, "run_id", filter.RunID)
	opportunityFilterAdd(&where, &args, "symbol", filter.Symbol)
	opportunityFilterAdd(&where, &args, "status", filter.Status)
	return strings.Join(where, " AND "), args
}

func (s *Store) ListOpportunityCandidates(ctx context.Context, filter OpportunityCandidateListFilter) ([]OpportunityCandidate, error) {
	where, args := opportunityCandidateWhere(filter)
	args = append(args, normalizedOpportunityLimit(filter.Limit), normalizedOpportunityOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY rank ASC, relevance_score DESC, created_at DESC LIMIT ? OFFSET ?`, opportunityCandidateSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list opportunity candidates")
	}
	defer rows.Close()
	items := make([]OpportunityCandidate, 0)
	for rows.Next() {
		item, err := scanOpportunityCandidate(rows)
		if err != nil {
			return nil, wrapError(err, "scan opportunity candidate")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate opportunity candidates")
}

func (s *Store) CountOpportunityCandidates(ctx context.Context, filter OpportunityCandidateListFilter) (int, error) {
	where, args := opportunityCandidateWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_opportunity_candidates WHERE %s`, where), args...).Scan(&total)
	return total, wrapError(err, "count opportunity candidates")
}

const opportunityEvidenceSelectSQL = `
	SELECT id, run_id, COALESCE(candidate_id,''), source_type, COALESCE(source_ref,''),
	       title, COALESCE(summary,''), COALESCE(url,''), COALESCE(publisher,''),
	       published_at, confidence, metadata_json, created_at
	FROM stockv2_opportunity_evidence
`

func scanOpportunityEvidence(row rowScanner) (OpportunityEvidence, error) {
	var item OpportunityEvidence
	var metadataJSON string
	var published sql.NullTime
	if err := row.Scan(
		&item.ID, &item.RunID, &item.CandidateID, &item.SourceType, &item.SourceRef,
		&item.Title, &item.Summary, &item.URL, &item.Publisher, &published,
		&item.Confidence, &metadataJSON, &item.CreatedAt,
	); err != nil {
		return OpportunityEvidence{}, err
	}
	item.Metadata = unmarshalMap(metadataJSON)
	if published.Valid {
		item.PublishedAt = published.Time
	}
	return item, nil
}

func (s *Store) CreateOpportunityEvidence(ctx context.Context, item OpportunityEvidence) (OpportunityEvidence, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_evidence
			(id, run_id, candidate_id, source_type, source_ref, title, summary, url,
			 publisher, published_at, confidence, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.RunID, nullableOpportunityString(item.CandidateID), item.SourceType,
		nullableOpportunityString(item.SourceRef), item.Title, nullableOpportunityString(item.Summary),
		nullableOpportunityString(item.URL), nullableOpportunityString(item.Publisher),
		nullableOpportunityTime(item.PublishedAt), item.Confidence, marshalMap(item.Metadata),
		item.CreatedAt)
	return item, wrapError(err, "create opportunity evidence")
}

func opportunityEvidenceWhere(filter OpportunityEvidenceListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	opportunityFilterAdd(&where, &args, "run_id", filter.RunID)
	opportunityFilterAdd(&where, &args, "candidate_id", filter.CandidateID)
	opportunityFilterAdd(&where, &args, "source_type", filter.SourceType)
	return strings.Join(where, " AND "), args
}

func (s *Store) ListOpportunityEvidence(ctx context.Context, filter OpportunityEvidenceListFilter) ([]OpportunityEvidence, error) {
	where, args := opportunityEvidenceWhere(filter)
	args = append(args, normalizedOpportunityLimit(filter.Limit), normalizedOpportunityOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, opportunityEvidenceSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list opportunity evidence")
	}
	defer rows.Close()
	items := make([]OpportunityEvidence, 0)
	for rows.Next() {
		item, err := scanOpportunityEvidence(rows)
		if err != nil {
			return nil, wrapError(err, "scan opportunity evidence")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate opportunity evidence")
}

func (s *Store) CountOpportunityEvidence(ctx context.Context, filter OpportunityEvidenceListFilter) (int, error) {
	where, args := opportunityEvidenceWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_opportunity_evidence WHERE %s`, where), args...).Scan(&total)
	return total, wrapError(err, "count opportunity evidence")
}

const opportunityResultSelectSQL = `
	SELECT id, run_id, COALESCE(summary,''), COALESCE(conclusion,''),
	       COALESCE(recommended_next_action,''), raw_result_json, created_at
	FROM stockv2_opportunity_results
`

func scanOpportunityResult(row rowScanner) (OpportunityResult, error) {
	var item OpportunityResult
	var rawJSON string
	if err := row.Scan(&item.ID, &item.RunID, &item.Summary, &item.Conclusion, &item.RecommendedNextAction, &rawJSON, &item.CreatedAt); err != nil {
		return OpportunityResult{}, err
	}
	item.RawResult = unmarshalMap(rawJSON)
	return item, nil
}

func (s *Store) UpsertOpportunityResult(ctx context.Context, item OpportunityResult) (OpportunityResult, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.RawResult == nil {
		item.RawResult = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_results
			(id, run_id, summary, conclusion, recommended_next_action, raw_result_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			summary = excluded.summary,
			conclusion = excluded.conclusion,
			recommended_next_action = excluded.recommended_next_action,
			raw_result_json = excluded.raw_result_json
	`, item.ID, item.RunID, nullableOpportunityString(item.Summary), nullableOpportunityString(item.Conclusion),
		nullableOpportunityString(item.RecommendedNextAction), marshalMap(item.RawResult), item.CreatedAt)
	if err != nil {
		return OpportunityResult{}, wrapError(err, "upsert opportunity result")
	}
	return s.GetOpportunityResultByRunID(ctx, item.RunID)
}

func (s *Store) GetOpportunityResultByRunID(ctx context.Context, runID string) (OpportunityResult, error) {
	item, err := scanOpportunityResult(s.db.QueryRowContext(ctx, opportunityResultSelectSQL+" WHERE run_id = ?", runID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityResult{}, ErrOpportunityResultNotFound
		}
		return OpportunityResult{}, wrapError(err, "get opportunity result")
	}
	return item, nil
}

func (s *Store) GetEmbeddingConfig(ctx context.Context) (EmbeddingConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(embedding_model_id,''), enabled, last_probe_at,
		       COALESCE(last_probe_status,''), COALESCE(last_error,''), updated_at
		FROM stockv2_embedding_config
		WHERE id = ?
	`, EmbeddingConfigIDDefault)
	var item EmbeddingConfig
	var enabled int
	var lastProbe sql.NullTime
	if err := row.Scan(&item.ID, &item.EmbeddingModelID, &enabled, &lastProbe, &item.LastProbeStatus, &item.LastError, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EmbeddingConfig{}, ErrEmbeddingConfigNotFound
		}
		return EmbeddingConfig{}, wrapError(err, "get embedding config")
	}
	item.Enabled = enabled != 0
	if lastProbe.Valid {
		item.LastProbeAt = lastProbe.Time
	}
	return item, nil
}

func (s *Store) UpsertEmbeddingConfig(ctx context.Context, item EmbeddingConfig) (EmbeddingConfig, error) {
	if item.ID == "" {
		item.ID = EmbeddingConfigIDDefault
	}
	item.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_config
			(id, embedding_model_id, enabled, last_probe_at, last_probe_status, last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			embedding_model_id = excluded.embedding_model_id,
			enabled = excluded.enabled,
			last_probe_at = excluded.last_probe_at,
			last_probe_status = excluded.last_probe_status,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, item.ID, nullableOpportunityString(item.EmbeddingModelID), boolToInt(item.Enabled),
		nullableOpportunityTime(item.LastProbeAt), nullableOpportunityString(item.LastProbeStatus),
		nullableOpportunityString(item.LastError), item.UpdatedAt)
	if err != nil {
		return EmbeddingConfig{}, wrapError(err, "upsert embedding config")
	}
	return s.GetEmbeddingConfig(ctx)
}

const embeddingAssetSelectSQL = `
	SELECT id, object_type, object_id, text_hash, COALESCE(text_summary,''), model_id,
	       COALESCE(provider_id,''), COALESCE(embedding_protocol,''), embedding_dimensions,
	       COALESCE(vector_ref,''), status, COALESCE(error_message,''), created_at, updated_at
	FROM stockv2_embedding_assets
`

func scanEmbeddingAsset(row rowScanner) (EmbeddingAsset, error) {
	var item EmbeddingAsset
	if err := row.Scan(
		&item.ID, &item.ObjectType, &item.ObjectID, &item.TextHash, &item.TextSummary,
		&item.ModelID, &item.ProviderID, &item.EmbeddingProtocol, &item.EmbeddingDimensions,
		&item.VectorRef, &item.Status, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return EmbeddingAsset{}, err
	}
	return item, nil
}

func (s *Store) UpsertEmbeddingAsset(ctx context.Context, item EmbeddingAsset) (EmbeddingAsset, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_assets
			(id, object_type, object_id, text_hash, text_summary, model_id, provider_id,
			 embedding_protocol, embedding_dimensions, vector_ref, status, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_type, object_id, model_id) DO UPDATE SET
			text_hash = excluded.text_hash,
			text_summary = excluded.text_summary,
			provider_id = excluded.provider_id,
			embedding_protocol = excluded.embedding_protocol,
			embedding_dimensions = excluded.embedding_dimensions,
			vector_ref = excluded.vector_ref,
			status = excluded.status,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`, item.ID, item.ObjectType, item.ObjectID, item.TextHash, nullableOpportunityString(item.TextSummary),
		item.ModelID, nullableOpportunityString(item.ProviderID), nullableOpportunityString(item.EmbeddingProtocol),
		item.EmbeddingDimensions, nullableOpportunityString(item.VectorRef), item.Status,
		nullableOpportunityString(item.ErrorMessage), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return EmbeddingAsset{}, wrapError(err, "upsert embedding asset")
	}
	return s.GetEmbeddingAssetByObject(ctx, item.ObjectType, item.ObjectID, item.ModelID)
}

func (s *Store) GetEmbeddingAssetByObject(ctx context.Context, objectType, objectID, modelID string) (EmbeddingAsset, error) {
	item, err := scanEmbeddingAsset(s.db.QueryRowContext(ctx, embeddingAssetSelectSQL+" WHERE object_type = ? AND object_id = ? AND model_id = ?", objectType, objectID, modelID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EmbeddingAsset{}, ErrEmbeddingAssetNotFound
		}
		return EmbeddingAsset{}, wrapError(err, "get embedding asset by object")
	}
	return item, nil
}

func (s *Store) GetEmbeddingAssetByVectorRef(ctx context.Context, vectorRef string) (EmbeddingAsset, error) {
	item, err := scanEmbeddingAsset(s.db.QueryRowContext(ctx, embeddingAssetSelectSQL+" WHERE vector_ref = ?", vectorRef))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EmbeddingAsset{}, ErrEmbeddingAssetNotFound
		}
		return EmbeddingAsset{}, wrapError(err, "get embedding asset by vector ref")
	}
	return item, nil
}

func embeddingAssetWhere(filter EmbeddingAssetListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	opportunityFilterAdd(&where, &args, "object_type", filter.ObjectType)
	opportunityFilterAdd(&where, &args, "object_id", filter.ObjectID)
	opportunityFilterAdd(&where, &args, "model_id", filter.ModelID)
	opportunityFilterAdd(&where, &args, "status", filter.Status)
	if filter.Dimensions > 0 {
		where = append(where, "embedding_dimensions = ?")
		args = append(args, filter.Dimensions)
	}
	return strings.Join(where, " AND "), args
}

func (s *Store) ListEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) ([]EmbeddingAsset, error) {
	where, args := embeddingAssetWhere(filter)
	args = append(args, normalizedOpportunityLimit(filter.Limit), normalizedOpportunityOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY updated_at DESC LIMIT ? OFFSET ?`, embeddingAssetSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list embedding assets")
	}
	defer rows.Close()
	items := make([]EmbeddingAsset, 0)
	for rows.Next() {
		item, err := scanEmbeddingAsset(rows)
		if err != nil {
			return nil, wrapError(err, "scan embedding asset")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate embedding assets")
}

func (s *Store) CountEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) (int, error) {
	where, args := embeddingAssetWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_embedding_assets WHERE %s`, where), args...).Scan(&total)
	return total, wrapError(err, "count embedding assets")
}

func (s *Store) CountEmbeddingAssetsByStatus(ctx context.Context, modelID string) (ready, stale, failed int, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM stockv2_embedding_assets
		WHERE model_id = ?
		GROUP BY status
	`, modelID)
	if err != nil {
		return 0, 0, 0, wrapError(err, "count embedding assets by status")
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return 0, 0, 0, wrapError(err, "scan embedding asset status count")
		}
		switch status {
		case EmbeddingAssetStatusReady:
			ready = count
		case EmbeddingAssetStatusStale:
			stale = count
		case EmbeddingAssetStatusFailed:
			failed = count
		}
	}
	return ready, stale, failed, wrapError(rows.Err(), "iterate embedding asset status counts")
}
