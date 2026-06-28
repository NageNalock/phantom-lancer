package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) CreateOpportunity(ctx context.Context, opp Opportunity) (Opportunity, error) {
	now := time.Now()
	if opp.ID == "" {
		opp.ID = generateID()
	}
	if opp.Status == "" {
		opp.Status = OpportunityStatusDraft
	}
	if opp.MarketScope == "" {
		opp.MarketScope = "a_share"
	}
	if opp.InstrumentScope == "" {
		opp.InstrumentScope = "both"
	}
	opp.CreatedAt = now
	opp.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunities
			(id, title, user_thesis, market_scope, instrument_scope, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, opp.ID, opp.Title, opp.UserThesis, opp.MarketScope, opp.InstrumentScope, opp.Status, nullableAgentString(opp.CreatedBy), opp.CreatedAt, opp.UpdatedAt)
	return opp, wrapError(err, "create opportunity")
}

func (s *Store) GetOpportunity(ctx context.Context, id string) (Opportunity, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, COALESCE(user_thesis,''), market_scope, instrument_scope, status,
		       COALESCE(created_by,''), created_at, updated_at
		FROM stockv2_opportunities WHERE id = ?
	`, id)
	var opp Opportunity
	if err := row.Scan(&opp.ID, &opp.Title, &opp.UserThesis, &opp.MarketScope, &opp.InstrumentScope, &opp.Status, &opp.CreatedBy, &opp.CreatedAt, &opp.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Opportunity{}, ErrOpportunityNotFound
		}
		return Opportunity{}, wrapError(err, "get opportunity")
	}
	return opp, nil
}

func (s *Store) UpdateOpportunity(ctx context.Context, opp Opportunity) (Opportunity, error) {
	opp.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_opportunities
		SET title = ?, user_thesis = ?, market_scope = ?, instrument_scope = ?, status = ?, updated_at = ?
		WHERE id = ?
	`, opp.Title, opp.UserThesis, opp.MarketScope, opp.InstrumentScope, opp.Status, opp.UpdatedAt, opp.ID)
	return opp, wrapError(err, "update opportunity")
}

func (s *Store) CreateOpportunityDiscoveryRun(ctx context.Context, run OpportunityDiscoveryRun) (OpportunityDiscoveryRun, error) {
	now := time.Now()
	if run.ID == "" {
		run.ID = generateID()
	}
	if run.Status == "" {
		run.Status = OpportunityDiscoveryRunStatusPending
	}
	if run.StepTotal == 0 {
		run.StepTotal = 8
	}
	run.CreatedAt = now
	run.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_discovery_runs
			(id, opportunity_id, agent_run_id, status, current_step_id, step_total, step_completed,
			 candidate_count, evidence_count, external_source_count, started_at, finished_at, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.OpportunityID, nullableAgentString(run.AgentRunID), run.Status, nullableAgentString(run.CurrentStepID), run.StepTotal, run.StepCompleted, run.CandidateCount, run.EvidenceCount, run.ExternalSourceCount, nullableAgentTime(run.StartedAt), nullableAgentTime(run.FinishedAt), nullableAgentString(run.ErrorMessage), run.CreatedAt, run.UpdatedAt)
	return run, wrapError(err, "create opportunity discovery run")
}

func (s *Store) GetOpportunityDiscoveryRun(ctx context.Context, id string) (OpportunityDiscoveryRun, error) {
	row := s.db.QueryRowContext(ctx, opportunityDiscoveryRunSelectSQL()+` WHERE id = ?`, id)
	return scanOpportunityDiscoveryRun(row)
}

func (s *Store) GetOpportunityDiscoveryRunByAgentRunID(ctx context.Context, agentRunID string) (OpportunityDiscoveryRun, error) {
	row := s.db.QueryRowContext(ctx, opportunityDiscoveryRunSelectSQL()+` WHERE agent_run_id = ? ORDER BY created_at DESC LIMIT 1`, agentRunID)
	return scanOpportunityDiscoveryRun(row)
}

func (s *Store) UpdateOpportunityDiscoveryRun(ctx context.Context, run OpportunityDiscoveryRun) (OpportunityDiscoveryRun, error) {
	run.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_opportunity_discovery_runs
		SET opportunity_id = ?, agent_run_id = ?, status = ?, current_step_id = ?, step_total = ?,
		    step_completed = ?, candidate_count = ?, evidence_count = ?, external_source_count = ?,
		    started_at = ?, finished_at = ?, error_message = ?, updated_at = ?
		WHERE id = ?
	`, run.OpportunityID, nullableAgentString(run.AgentRunID), run.Status, nullableAgentString(run.CurrentStepID), run.StepTotal, run.StepCompleted, run.CandidateCount, run.EvidenceCount, run.ExternalSourceCount, nullableAgentTime(run.StartedAt), nullableAgentTime(run.FinishedAt), nullableAgentString(run.ErrorMessage), run.UpdatedAt, run.ID)
	return run, wrapError(err, "update opportunity discovery run")
}

func (s *Store) RefreshOpportunityDiscoveryRunCounts(ctx context.Context, runID string) (OpportunityDiscoveryRun, error) {
	run, err := s.GetOpportunityDiscoveryRun(ctx, runID)
	if err != nil {
		return OpportunityDiscoveryRun{}, err
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_opportunity_candidates WHERE run_id = ?`, runID).Scan(&run.CandidateCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_opportunity_evidence WHERE run_id = ?`, runID).Scan(&run.EvidenceCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_opportunity_evidence WHERE run_id = ? AND source_type = ?`, runID, OpportunityEvidenceSourceExternal).Scan(&run.ExternalSourceCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_opportunity_discovery_steps WHERE run_id = ? AND status = ?`, runID, OpportunityDiscoveryStepStatusCompleted).Scan(&run.StepCompleted)
	return s.UpdateOpportunityDiscoveryRun(ctx, run)
}

func opportunityDiscoveryRunSelectSQL() string {
	return `
		SELECT id, opportunity_id, COALESCE(agent_run_id,''), status, COALESCE(current_step_id,''),
		       step_total, step_completed, candidate_count, evidence_count, external_source_count,
		       started_at, finished_at, COALESCE(error_message,''), created_at, updated_at
		FROM stockv2_opportunity_discovery_runs
	`
}

func scanOpportunityDiscoveryRun(row rowScanner) (OpportunityDiscoveryRun, error) {
	var run OpportunityDiscoveryRun
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(&run.ID, &run.OpportunityID, &run.AgentRunID, &run.Status, &run.CurrentStepID, &run.StepTotal, &run.StepCompleted, &run.CandidateCount, &run.EvidenceCount, &run.ExternalSourceCount, &startedAt, &finishedAt, &run.ErrorMessage, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityDiscoveryRun{}, ErrOpportunityDiscoveryRunNotFound
		}
		return OpportunityDiscoveryRun{}, wrapError(err, "scan opportunity discovery run")
	}
	if startedAt.Valid {
		run.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		run.FinishedAt = finishedAt.Time
	}
	return run, nil
}

func (s *Store) UpsertOpportunityDiscoveryStep(ctx context.Context, step OpportunityDiscoveryStep) (OpportunityDiscoveryStep, error) {
	now := time.Now()
	if step.ID == "" {
		step.ID = generateID()
	}
	if step.Status == "" {
		step.Status = OpportunityDiscoveryStepStatusPending
	}
	if step.CreatedAt.IsZero() {
		step.CreatedAt = now
	}
	step.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_discovery_steps
			(id, run_id, step_key, step_title, status, order_index, input_summary, output_summary,
			 metadata_json, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, step_key) DO UPDATE SET
			step_title = excluded.step_title,
			status = excluded.status,
			order_index = excluded.order_index,
			input_summary = COALESCE(excluded.input_summary, input_summary),
			output_summary = COALESCE(excluded.output_summary, output_summary),
			metadata_json = excluded.metadata_json,
			started_at = COALESCE(excluded.started_at, started_at),
			finished_at = COALESCE(excluded.finished_at, finished_at),
			updated_at = excluded.updated_at
	`, step.ID, step.RunID, step.StepKey, step.StepTitle, step.Status, step.OrderIndex, nullableAgentString(step.InputSummary), nullableAgentString(step.OutputSummary), marshalMap(step.Metadata), nullableAgentTime(step.StartedAt), nullableAgentTime(step.FinishedAt), step.CreatedAt, step.UpdatedAt)
	if err != nil {
		return OpportunityDiscoveryStep{}, wrapError(err, "upsert opportunity discovery step")
	}
	return s.GetOpportunityDiscoveryStepByKey(ctx, step.RunID, step.StepKey)
}

func (s *Store) GetOpportunityDiscoveryStep(ctx context.Context, id string) (OpportunityDiscoveryStep, error) {
	row := s.db.QueryRowContext(ctx, opportunityDiscoveryStepSelectSQL()+` WHERE id = ?`, id)
	return scanOpportunityDiscoveryStep(row)
}

func (s *Store) GetOpportunityDiscoveryStepByKey(ctx context.Context, runID, stepKey string) (OpportunityDiscoveryStep, error) {
	row := s.db.QueryRowContext(ctx, opportunityDiscoveryStepSelectSQL()+` WHERE run_id = ? AND step_key = ?`, runID, stepKey)
	return scanOpportunityDiscoveryStep(row)
}

func opportunityDiscoveryStepSelectSQL() string {
	return `
		SELECT id, run_id, step_key, step_title, status, order_index, COALESCE(input_summary,''),
		       COALESCE(output_summary,''), metadata_json, started_at, finished_at, created_at, updated_at
		FROM stockv2_opportunity_discovery_steps
	`
}

func scanOpportunityDiscoveryStep(row rowScanner) (OpportunityDiscoveryStep, error) {
	var step OpportunityDiscoveryStep
	var metadata string
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(&step.ID, &step.RunID, &step.StepKey, &step.StepTitle, &step.Status, &step.OrderIndex, &step.InputSummary, &step.OutputSummary, &metadata, &startedAt, &finishedAt, &step.CreatedAt, &step.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityDiscoveryStep{}, ErrOpportunityDiscoveryStepNotFound
		}
		return OpportunityDiscoveryStep{}, wrapError(err, "scan opportunity discovery step")
	}
	step.Metadata = unmarshalMap(metadata)
	if startedAt.Valid {
		step.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		step.FinishedAt = finishedAt.Time
	}
	return step, nil
}

func (s *Store) CreateOpportunityEvidence(ctx context.Context, ev OpportunityEvidence) (OpportunityEvidence, error) {
	now := time.Now()
	if ev.ID == "" {
		ev.ID = generateID()
	}
	if ev.SourceType == "" {
		ev.SourceType = OpportunityEvidenceSourceAgent
	}
	ev.CreatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_evidence
			(id, run_id, candidate_id, source_type, source_ref, title, summary, url, publisher,
			 published_at, confidence, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ev.ID, ev.RunID, nullableAgentString(ev.CandidateID), ev.SourceType, nullableAgentString(ev.SourceRef), ev.Title, nullableAgentString(ev.Summary), nullableAgentString(ev.URL), nullableAgentString(ev.Publisher), nullableAgentTime(ev.PublishedAt), ev.Confidence, marshalMap(ev.Metadata), ev.CreatedAt)
	return ev, wrapError(err, "create opportunity evidence")
}

func (s *Store) UpsertOpportunityCandidate(ctx context.Context, c OpportunityCandidate) (OpportunityCandidate, error) {
	now := time.Now()
	if c.ID == "" {
		c.ID = generateID()
	}
	if c.Status == "" {
		c.Status = OpportunityCandidateStatusCandidate
	}
	if c.InstrumentType == "" {
		c.InstrumentType = InstrumentTypeStock
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
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
	`, c.ID, c.OpportunityID, c.RunID, c.Symbol, c.Market, c.InstrumentType, c.Name, c.RelationType, c.RelevanceScore, c.EvidenceScore, c.MarketRiskScore, c.Confidence, c.Rank, c.Status, nullableAgentString(c.Reason), nullableAgentString(c.RiskSummary), marshalMap(c.Metadata), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return OpportunityCandidate{}, wrapError(err, "upsert opportunity candidate")
	}
	return s.GetOpportunityCandidateByRunSymbol(ctx, c.RunID, c.Symbol)
}

func (s *Store) GetOpportunityCandidate(ctx context.Context, id string) (OpportunityCandidate, error) {
	row := s.db.QueryRowContext(ctx, opportunityCandidateSelectSQL()+` WHERE id = ?`, id)
	return scanOpportunityCandidate(row)
}

func (s *Store) GetOpportunityCandidateByRunSymbol(ctx context.Context, runID, symbol string) (OpportunityCandidate, error) {
	row := s.db.QueryRowContext(ctx, opportunityCandidateSelectSQL()+` WHERE run_id = ? AND symbol = ?`, runID, symbol)
	return scanOpportunityCandidate(row)
}

func (s *Store) ListOpportunityCandidates(ctx context.Context, runID string) ([]OpportunityCandidate, error) {
	rows, err := s.db.QueryContext(ctx, opportunityCandidateSelectSQL()+` WHERE run_id = ? ORDER BY rank ASC, relevance_score DESC, created_at ASC`, runID)
	if err != nil {
		return nil, wrapError(err, "list opportunity candidates")
	}
	defer rows.Close()
	var out []OpportunityCandidate
	for rows.Next() {
		item, err := scanOpportunityCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, wrapError(rows.Err(), "iterate opportunity candidates")
}

func opportunityCandidateSelectSQL() string {
	return `
		SELECT id, opportunity_id, run_id, symbol, market, instrument_type, name, relation_type,
		       relevance_score, evidence_score, market_risk_score, confidence, rank, status,
		       COALESCE(reason,''), COALESCE(risk_summary,''), metadata_json, created_at, updated_at
		FROM stockv2_opportunity_candidates
	`
}

func scanOpportunityCandidate(row rowScanner) (OpportunityCandidate, error) {
	var c OpportunityCandidate
	var metadata string
	if err := row.Scan(&c.ID, &c.OpportunityID, &c.RunID, &c.Symbol, &c.Market, &c.InstrumentType, &c.Name, &c.RelationType, &c.RelevanceScore, &c.EvidenceScore, &c.MarketRiskScore, &c.Confidence, &c.Rank, &c.Status, &c.Reason, &c.RiskSummary, &metadata, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OpportunityCandidate{}, ErrOpportunityCandidateNotFound
		}
		return OpportunityCandidate{}, wrapError(err, "scan opportunity candidate")
	}
	c.Metadata = unmarshalMap(metadata)
	return c, nil
}

func (s *Store) CreateOpportunityDiscoveryResult(ctx context.Context, result OpportunityDiscoveryResult) (OpportunityDiscoveryResult, error) {
	now := time.Now()
	if result.ID == "" {
		result.ID = generateID()
	}
	result.CreatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_opportunity_results
			(id, run_id, summary, conclusion, recommended_next_action, raw_result_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			summary = excluded.summary,
			conclusion = excluded.conclusion,
			recommended_next_action = excluded.recommended_next_action,
			raw_result_json = excluded.raw_result_json
	`, result.ID, result.RunID, nullableAgentString(result.Summary), nullableAgentString(result.Conclusion), nullableAgentString(result.RecommendedNextAction), marshalMap(result.RawResult), result.CreatedAt)
	return result, wrapError(err, "create opportunity discovery result")
}

func (s *Store) GetEmbeddingConfig(ctx context.Context) (EmbeddingConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(embedding_model_id,''), enabled, last_probe_at,
		       COALESCE(last_probe_status,''), COALESCE(last_error,''), updated_at
		FROM stockv2_embedding_config WHERE id = 'default'
	`)
	var cfg EmbeddingConfig
	var enabled int
	var lastProbeAt sql.NullTime
	if err := row.Scan(&cfg.ID, &cfg.EmbeddingModelID, &enabled, &lastProbeAt, &cfg.LastProbeStatus, &cfg.LastError, &cfg.UpdatedAt); err != nil {
		return EmbeddingConfig{}, wrapError(err, "get embedding config")
	}
	cfg.Enabled = enabled != 0
	if lastProbeAt.Valid {
		cfg.LastProbeAt = lastProbeAt.Time
	}
	return cfg, nil
}

func (s *Store) UpdateEmbeddingConfig(ctx context.Context, cfg EmbeddingConfig) (EmbeddingConfig, error) {
	if cfg.ID == "" {
		cfg.ID = "default"
	}
	cfg.UpdatedAt = time.Now()
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
	`, cfg.ID, nullableAgentString(cfg.EmbeddingModelID), boolToInt(cfg.Enabled), nullableAgentTime(cfg.LastProbeAt), nullableAgentString(cfg.LastProbeStatus), nullableAgentString(cfg.LastError), cfg.UpdatedAt)
	return cfg, wrapError(err, "update embedding config")
}
