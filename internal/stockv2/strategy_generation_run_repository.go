package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) CreateStrategyGenerationStep(ctx context.Context, step StrategyGenerationStepRun) (StrategyGenerationStepRun, error) {
	now := time.Now()
	if step.ID == "" {
		step.ID = generateID()
	}
	if step.CreatedAt.IsZero() {
		step.CreatedAt = now
	}
	step.UpdatedAt = now
	if step.StructuredOutput == nil {
		step.StructuredOutput = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_strategy_generation_steps (
			id, run_id, step_key, step_name, role, status, sequence_no,
			input_summary, output_summary, error_message, prompt, output_artifact_summary,
			structured_output_json, started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		step.ID,
		step.RunID,
		step.StepKey,
		step.StepName,
		step.Role,
		step.Status,
		step.SequenceNo,
		nullableString(step.InputSummary),
		nullableString(step.OutputSummary),
		nullableString(step.ErrorMessage),
		nullableString(step.Prompt),
		nullableString(step.OutputArtifactSummary),
		marshalMap(step.StructuredOutput),
		nullableTime(step.StartedAt),
		nullableTime(step.FinishedAt),
		step.CreatedAt,
		step.UpdatedAt,
	)
	if err != nil {
		return StrategyGenerationStepRun{}, wrapError(err, "create strategy generation step")
	}
	return step, nil
}

func (s *Store) UpdateStrategyGenerationStep(ctx context.Context, step StrategyGenerationStepRun) (StrategyGenerationStepRun, error) {
	if step.StructuredOutput == nil {
		step.StructuredOutput = map[string]any{}
	}
	step.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_strategy_generation_steps
		SET status = ?, output_summary = ?, error_message = ?, prompt = ?, output_artifact_summary = ?,
		    structured_output_json = ?, started_at = ?, finished_at = ?, updated_at = ?
		WHERE id = ?
	`,
		step.Status,
		nullableString(step.OutputSummary),
		nullableString(step.ErrorMessage),
		nullableString(step.Prompt),
		nullableString(step.OutputArtifactSummary),
		marshalMap(step.StructuredOutput),
		nullableTime(step.StartedAt),
		nullableTime(step.FinishedAt),
		step.UpdatedAt,
		step.ID,
	)
	if err != nil {
		return StrategyGenerationStepRun{}, wrapError(err, "update strategy generation step")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return StrategyGenerationStepRun{}, sql.ErrNoRows
	}
	return step, nil
}

func (s *Store) ListStrategyGenerationSteps(ctx context.Context, runID string) ([]StrategyGenerationStepRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, step_key, step_name, role, status, sequence_no,
		       COALESCE(input_summary,''), COALESCE(output_summary,''), COALESCE(error_message,''),
		       COALESCE(prompt,''), COALESCE(output_artifact_summary,''), COALESCE(structured_output_json,''),
		       started_at, finished_at, created_at, updated_at
		FROM stockv2_strategy_generation_steps
		WHERE run_id = ?
		ORDER BY sequence_no ASC, created_at ASC
	`, runID)
	if err != nil {
		return nil, wrapError(err, "list strategy generation steps")
	}
	defer rows.Close()
	out := make([]StrategyGenerationStepRun, 0)
	for rows.Next() {
		item, err := scanStrategyGenerationStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate strategy generation steps")
	}
	return out, nil
}

func scanStrategyGenerationStep(row rowScanner) (StrategyGenerationStepRun, error) {
	var item StrategyGenerationStepRun
	var structuredJSON string
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.StepKey,
		&item.StepName,
		&item.Role,
		&item.Status,
		&item.SequenceNo,
		&item.InputSummary,
		&item.OutputSummary,
		&item.ErrorMessage,
		&item.Prompt,
		&item.OutputArtifactSummary,
		&structuredJSON,
		&startedAt,
		&finishedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return StrategyGenerationStepRun{}, err
	}
	item.StructuredOutput = unmarshalMap(structuredJSON)
	if startedAt.Valid {
		item.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		item.FinishedAt = finishedAt.Time
	}
	return item, nil
}

func (s *Store) AddStrategyGenerationContextItem(ctx context.Context, item StrategyGenerationContextItem) (StrategyGenerationContextItem, error) {
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	if item.ContentJSON == nil {
		item.ContentJSON = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_strategy_generation_contexts (
			id, run_id, step_id, context_type, title, content_json, content_text, sequence_no, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		item.ID,
		item.RunID,
		nullableString(item.StepID),
		item.ContextType,
		nullableString(item.Title),
		marshalMap(item.ContentJSON),
		nullableString(item.ContentText),
		item.SequenceNo,
		item.CreatedAt,
	)
	if err != nil {
		return StrategyGenerationContextItem{}, wrapError(err, "add strategy generation context item")
	}
	return item, nil
}

func (s *Store) ListStrategyGenerationContextItems(ctx context.Context, runID string) ([]StrategyGenerationContextItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, COALESCE(step_id,''), context_type, COALESCE(title,''),
		       COALESCE(content_json,''), COALESCE(content_text,''), sequence_no, created_at
		FROM stockv2_strategy_generation_contexts
		WHERE run_id = ?
		ORDER BY sequence_no ASC, created_at ASC
	`, runID)
	if err != nil {
		return nil, wrapError(err, "list strategy generation contexts")
	}
	defer rows.Close()
	out := make([]StrategyGenerationContextItem, 0)
	for rows.Next() {
		var item StrategyGenerationContextItem
		var jsonText string
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.StepID,
			&item.ContextType,
			&item.Title,
			&jsonText,
			&item.ContentText,
			&item.SequenceNo,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.ContentJSON = unmarshalMap(jsonText)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate strategy generation contexts")
	}
	return out, nil
}

func ignoreStrategyGenerationStepStoreError(err error) error {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
