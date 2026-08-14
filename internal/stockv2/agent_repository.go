package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Agent 治理层持久化。风格对齐 monitor_repository.go:SQL fragment 常量 +
// scanXxx(rowScanner) helper + xxxFilterSQL builder。复用 rowScanner /
// wrapError / boolToInt / marshalMap / unmarshalMap,不重造。

// ============================ helpers ============================

func agentFilterAdd(where *[]string, args *[]any, column, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, strings.TrimSpace(value))
}

// ============================ provider profiles ============================

const agentProviderProfileSelectSQL = `
    SELECT id, provider_type, name, COALESCE(display_name,''), config_state, auth_state,
           availability, last_probe_at, COALESCE(last_probe_result,''), metadata_json,
           created_at, updated_at
    FROM stockv2_agent_provider_profiles
`

func scanAgentProviderProfile(row rowScanner) (AgentProviderProfile, error) {
	var p AgentProviderProfile
	var displayName, lastProbeResult, metadataJSON string
	var lastProbeAt sql.NullTime
	if err := row.Scan(
		&p.ID, &p.ProviderType, &p.Name, &displayName, &p.ConfigState, &p.AuthState,
		&p.Availability, &lastProbeAt, &lastProbeResult, &metadataJSON,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return p, err
	}
	p.DisplayName = displayName
	p.LastProbeResult = lastProbeResult
	if lastProbeAt.Valid {
		p.LastProbeAt = lastProbeAt.Time
	}
	p.Metadata = unmarshalMap(metadataJSON)
	return p, nil
}

func (s *Store) CreateAgentProviderProfile(ctx context.Context, profile AgentProviderProfile) (AgentProviderProfile, error) {
	now := time.Now()
	if profile.ID == "" {
		profile.ID = generateID()
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	if profile.Metadata == nil {
		profile.Metadata = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_agent_provider_profiles
			(id, provider_type, name, display_name, config_state, auth_state, availability,
			 last_probe_at, last_probe_result, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		profile.ID,
		profile.ProviderType,
		profile.Name,
		nullableString(profile.DisplayName),
		profile.ConfigState,
		profile.AuthState,
		profile.Availability,
		nullableTime(profile.LastProbeAt),
		nullableString(profile.LastProbeResult),
		marshalMap(profile.Metadata),
		profile.CreatedAt,
		profile.UpdatedAt,
	)
	return profile, wrapError(err, "create agent provider profile")
}

func (s *Store) GetAgentProviderProfile(ctx context.Context, id string) (AgentProviderProfile, error) {
	row := s.db.QueryRowContext(ctx, agentProviderProfileSelectSQL+" WHERE id = ?", id)
	profile, err := scanAgentProviderProfile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentProviderProfile{}, ErrAgentProviderNotFound
		}
		return AgentProviderProfile{}, wrapError(err, "get agent provider profile")
	}
	return profile, nil
}

func (s *Store) ListAgentProviderProfiles(ctx context.Context, filter AgentProviderProfileListFilter) ([]AgentProviderProfile, error) {
	where, args := agentProviderProfileFilterSQL(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentProviderProfileSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent provider profiles")
	}
	return scanRows(rows, scanAgentProviderProfile, "scan agent provider profile", "iterate agent provider profiles")
}

func (s *Store) CountAgentProviderProfiles(ctx context.Context, filter AgentProviderProfileListFilter) (int, error) {
	where, args := agentProviderProfileFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_agent_provider_profiles WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count agent provider profiles")
	}
	return count, nil
}

func (s *Store) UpdateAgentProviderProfile(ctx context.Context, profile AgentProviderProfile) (AgentProviderProfile, error) {
	if profile.Metadata == nil {
		profile.Metadata = map[string]any{}
	}
	profile.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_provider_profiles
		SET provider_type = ?, name = ?, display_name = ?, config_state = ?, auth_state = ?,
		    availability = ?, last_probe_at = ?, last_probe_result = ?, metadata_json = ?, updated_at = ?
		WHERE id = ?
	`,
		profile.ProviderType,
		profile.Name,
		nullableString(profile.DisplayName),
		profile.ConfigState,
		profile.AuthState,
		profile.Availability,
		nullableTime(profile.LastProbeAt),
		nullableString(profile.LastProbeResult),
		marshalMap(profile.Metadata),
		profile.UpdatedAt,
		profile.ID,
	)
	if err != nil {
		return AgentProviderProfile{}, wrapError(err, "update agent provider profile")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return AgentProviderProfile{}, ErrAgentProviderNotFound
	}
	return profile, nil
}

func (s *Store) DeleteAgentProviderProfile(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin delete agent provider profile")
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_agent_task_profiles
		SET primary_model_id = CASE WHEN primary_model_id IN (SELECT id FROM stockv2_agent_model_profiles WHERE provider_id = ?) THEN '' ELSE primary_model_id END,
		    fallback_model_id = CASE WHEN fallback_model_id IN (SELECT id FROM stockv2_agent_model_profiles WHERE provider_id = ?) THEN '' ELSE fallback_model_id END,
		    updated_at = ?
	`, id, id, time.Now()); err != nil {
		return wrapError(err, "clear agent task model bindings")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_agent_model_profiles WHERE provider_id = ?`, id); err != nil {
		return wrapError(err, "delete provider agent models")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stockv2_agent_provider_profiles WHERE id = ?`, id)
	if err != nil {
		return wrapError(err, "delete agent provider profile")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrAgentProviderNotFound
	}
	return wrapError(tx.Commit(), "commit delete agent provider profile")
}

func agentProviderProfileFilterSQL(filter AgentProviderProfileListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	agentFilterAdd(&where, &args, "provider_type", filter.ProviderType)
	agentFilterAdd(&where, &args, "config_state", filter.ConfigState)
	agentFilterAdd(&where, &args, "auth_state", filter.AuthState)
	agentFilterAdd(&where, &args, "availability", filter.Availability)
	return strings.Join(where, " AND "), args
}

// ============================ model profiles ============================

const agentModelProfileSelectSQL = `
    SELECT id, provider_id, model_name, COALESCE(display_name,''), enabled, status, cost_level,
           context_limit, metadata_json, created_at, updated_at
    FROM stockv2_agent_model_profiles
`

func scanAgentModelProfile(row rowScanner) (AgentModelProfile, error) {
	var m AgentModelProfile
	var displayName, metadataJSON string
	var enabled int
	if err := row.Scan(
		&m.ID, &m.ProviderID, &m.ModelName, &displayName, &enabled, &m.Status, &m.CostLevel,
		&m.ContextLimit, &metadataJSON, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return m, err
	}
	m.DisplayName = displayName
	m.Enabled = enabled != 0
	m.Metadata = unmarshalMap(metadataJSON)
	hydrateAgentModelProfile(&m)
	return m, nil
}

func hydrateAgentModelProfile(model *AgentModelProfile) {
	if model.Metadata == nil {
		model.Metadata = map[string]any{}
	}
	modelType := strings.TrimSpace(model.ModelType)
	if modelType == "" {
		modelType = stringFromAny(model.Metadata["modelType"])
	}
	if !validAgentModelType(modelType) || modelType == "" {
		modelType = AgentModelTypeChat
	}
	model.ModelType = modelType
	if model.ModelType != AgentModelTypeEmbedding {
		model.EmbeddingProtocol = ""
		model.EmbeddingDimensions = 0
		model.InputModalities = nil
		model.EncodingFormat = ""
		return
	}
	if strings.TrimSpace(model.EmbeddingProtocol) == "" {
		model.EmbeddingProtocol = stringFromAny(model.Metadata["embeddingProtocol"])
	}
	if strings.TrimSpace(model.EmbeddingProtocol) == "" {
		model.EmbeddingProtocol = AgentEmbeddingProtocolOpenAI
	}
	if model.EmbeddingDimensions <= 0 {
		if value, ok := numberFromAny(model.Metadata["embeddingDimensions"]); ok && value > 0 {
			model.EmbeddingDimensions = int(value)
		}
	}
	if len(model.InputModalities) == 0 {
		model.InputModalities = agentModelStringListFromAny(model.Metadata["inputModalities"])
	}
	if len(model.InputModalities) == 0 {
		model.InputModalities = []string{"text"}
	}
	if strings.TrimSpace(model.EncodingFormat) == "" {
		model.EncodingFormat = stringFromAny(model.Metadata["encodingFormat"])
	}
}

func agentModelProfileForStore(model AgentModelProfile) AgentModelProfile {
	hydrateAgentModelProfile(&model)
	if model.Metadata == nil {
		model.Metadata = map[string]any{}
	}
	// ponytail: embedding 字段先复用 metadata_json;未来需要列表筛选或统计时再迁移成列。
	model.Metadata["modelType"] = model.ModelType
	if model.ModelType != AgentModelTypeEmbedding {
		delete(model.Metadata, "embeddingProtocol")
		delete(model.Metadata, "embeddingDimensions")
		delete(model.Metadata, "inputModalities")
		delete(model.Metadata, "encodingFormat")
		return model
	}
	model.Metadata["embeddingProtocol"] = model.EmbeddingProtocol
	if model.EmbeddingDimensions > 0 {
		model.Metadata["embeddingDimensions"] = model.EmbeddingDimensions
	} else {
		delete(model.Metadata, "embeddingDimensions")
	}
	if len(model.InputModalities) > 0 {
		model.Metadata["inputModalities"] = model.InputModalities
	} else {
		delete(model.Metadata, "inputModalities")
	}
	if strings.TrimSpace(model.EncodingFormat) != "" {
		model.Metadata["encodingFormat"] = strings.TrimSpace(model.EncodingFormat)
	} else {
		delete(model.Metadata, "encodingFormat")
	}
	return model
}

func agentModelStringListFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		return []string{text}
	default:
		return nil
	}
}

func (s *Store) CreateAgentModelProfile(ctx context.Context, model AgentModelProfile) (AgentModelProfile, error) {
	now := time.Now()
	if model.ID == "" {
		model.ID = generateID()
	}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now
	if model.Metadata == nil {
		model.Metadata = map[string]any{}
	}
	model = agentModelProfileForStore(model)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_agent_model_profiles
			(id, provider_id, model_name, display_name, enabled, status, cost_level,
			 context_limit, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		model.ID,
		model.ProviderID,
		model.ModelName,
		nullableString(model.DisplayName),
		boolToInt(model.Enabled),
		model.Status,
		model.CostLevel,
		model.ContextLimit,
		marshalMap(model.Metadata),
		model.CreatedAt,
		model.UpdatedAt,
	)
	return model, wrapError(err, "create agent model profile")
}

func (s *Store) GetAgentModelProfile(ctx context.Context, id string) (AgentModelProfile, error) {
	row := s.db.QueryRowContext(ctx, agentModelProfileSelectSQL+" WHERE id = ?", id)
	model, err := scanAgentModelProfile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentModelProfile{}, ErrAgentModelNotFound
		}
		return AgentModelProfile{}, wrapError(err, "get agent model profile")
	}
	return model, nil
}

func (s *Store) ListAgentModelProfiles(ctx context.Context, filter AgentModelProfileListFilter) ([]AgentModelProfile, error) {
	where, args := agentModelProfileFilterSQL(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentModelProfileSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent model profiles")
	}
	return scanRows(rows, scanAgentModelProfile, "scan agent model profile", "iterate agent model profiles")
}

func (s *Store) CountAgentModelProfiles(ctx context.Context, filter AgentModelProfileListFilter) (int, error) {
	where, args := agentModelProfileFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_agent_model_profiles WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count agent model profiles")
	}
	return count, nil
}

func (s *Store) UpdateAgentModelProfile(ctx context.Context, model AgentModelProfile) (AgentModelProfile, error) {
	if model.Metadata == nil {
		model.Metadata = map[string]any{}
	}
	model = agentModelProfileForStore(model)
	model.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_model_profiles
		SET provider_id = ?, model_name = ?, display_name = ?, enabled = ?, status = ?,
		    cost_level = ?, context_limit = ?, metadata_json = ?, updated_at = ?
		WHERE id = ?
	`,
		model.ProviderID,
		model.ModelName,
		nullableString(model.DisplayName),
		boolToInt(model.Enabled),
		model.Status,
		model.CostLevel,
		model.ContextLimit,
		marshalMap(model.Metadata),
		model.UpdatedAt,
		model.ID,
	)
	if err != nil {
		return AgentModelProfile{}, wrapError(err, "update agent model profile")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return AgentModelProfile{}, ErrAgentModelNotFound
	}
	return model, nil
}

func agentModelProfileFilterSQL(filter AgentModelProfileListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	agentFilterAdd(&where, &args, "provider_id", filter.ProviderID)
	agentFilterAdd(&where, &args, "status", filter.Status)
	agentFilterAdd(&where, &args, "cost_level", filter.CostLevel)
	if filter.Enabled != nil {
		if *filter.Enabled {
			where = append(where, "enabled = 1")
		} else {
			where = append(where, "enabled = 0")
		}
	}
	return strings.Join(where, " AND "), args
}

// ============================ task profiles ============================

const agentTaskProfileSelectSQL = `
    SELECT id, task_type, COALESCE(execution_mode,'cli'), COALESCE(primary_model_id,''), COALESCE(fallback_model_id,''),
	       COALESCE(reasoning_effort,''), max_budget, archive_enabled,
	       COALESCE(archive_object_storage_profile_id,''), created_at, updated_at
    FROM stockv2_agent_task_profiles
`

func scanAgentTaskProfile(row rowScanner) (AgentTaskProfile, error) {
	var t AgentTaskProfile
	if err := row.Scan(
		&t.ID, &t.TaskType, &t.ExecutionMode, &t.PrimaryModelID, &t.FallbackModelID,
		&t.ReasoningEffort, &t.MaxBudget, &t.ArchiveEnabled,
		&t.ArchiveObjectStorageProfileID, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return t, err
	}
	return t, nil
}

// 无 Create:task profiles 由 schema 内 INSERT OR IGNORE 种入;
// 支持任务由 service 层 supportedAgentTaskType 校验。

func (s *Store) GetAgentTaskProfile(ctx context.Context, id string) (AgentTaskProfile, error) {
	row := s.db.QueryRowContext(ctx, agentTaskProfileSelectSQL+" WHERE id = ?", id)
	tp, err := scanAgentTaskProfile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentTaskProfile{}, ErrAgentTaskProfileNotFound
		}
		return AgentTaskProfile{}, wrapError(err, "get agent task profile")
	}
	return tp, nil
}

func (s *Store) GetAgentTaskProfileByType(ctx context.Context, taskType string) (AgentTaskProfile, error) {
	row := s.db.QueryRowContext(ctx, agentTaskProfileSelectSQL+" WHERE task_type = ?", taskType)
	tp, err := scanAgentTaskProfile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentTaskProfile{}, ErrAgentTaskProfileNotFound
		}
		return AgentTaskProfile{}, wrapError(err, "get agent task profile by type")
	}
	return tp, nil
}

func (s *Store) ListAgentTaskProfiles(ctx context.Context, filter AgentTaskProfileListFilter) ([]AgentTaskProfile, error) {
	where, args := agentTaskProfileFilterSQL(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at ASC LIMIT ? OFFSET ?`, agentTaskProfileSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent task profiles")
	}
	return scanRows(rows, scanAgentTaskProfile, "scan agent task profile", "iterate agent task profiles")
}

func (s *Store) CountAgentTaskProfiles(ctx context.Context, filter AgentTaskProfileListFilter) (int, error) {
	where, args := agentTaskProfileFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_agent_task_profiles WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count agent task profiles")
	}
	return count, nil
}

// UpdateAgentTaskProfile 只改模型执行配置，task_type 为自然键不可变。
func (s *Store) UpdateAgentTaskProfile(ctx context.Context, profile AgentTaskProfile) (AgentTaskProfile, error) {
	profile.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_task_profiles
		SET execution_mode = ?, primary_model_id = ?, fallback_model_id = ?, reasoning_effort = ?, max_budget = ?,
		    archive_enabled = ?, archive_object_storage_profile_id = ?, updated_at = ?
		WHERE id = ?
	`,
		profile.ExecutionMode,
		nullableString(profile.PrimaryModelID),
		nullableString(profile.FallbackModelID),
		profile.ReasoningEffort,
		profile.MaxBudget,
		profile.ArchiveEnabled,
		profile.ArchiveObjectStorageProfileID,
		profile.UpdatedAt,
		profile.ID,
	)
	if err != nil {
		return AgentTaskProfile{}, wrapError(err, "update agent task profile")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return AgentTaskProfile{}, ErrAgentTaskProfileNotFound
	}
	return profile, nil
}

func (s *Store) AgentTraceObjectStorageProfileReferenced(ctx context.Context, profileID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stockv2_agent_task_profiles
		WHERE archive_enabled = 1 AND archive_object_storage_profile_id = ?
	`, strings.TrimSpace(profileID)).Scan(&count)
	return count > 0, wrapError(err, "check agent trace object storage profile reference")
}

func agentTaskProfileFilterSQL(filter AgentTaskProfileListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	agentFilterAdd(&where, &args, "task_type", filter.TaskType)
	return strings.Join(where, " AND "), args
}

// ============================ runs + decision ledger ============================

const agentRunSelectSQL = `
    SELECT id, task_type, COALESCE(execution_mode,'cli'), COALESCE(provider_id,''), COALESCE(model_id,''),
           COALESCE(reasoning_effort,''), trigger_object_type, trigger_object_id, status, cost_estimate_json,
           COALESCE(error_message,''), COALESCE(output,''), COALESCE(decision_ledger_id,''),
           started_at, finished_at, created_at, updated_at
    FROM stockv2_agent_runs
`

func scanAgentRun(row rowScanner) (AgentRun, error) {
	var r AgentRun
	var providerID, modelID, errorMessage, output, decisionLedgerID, costEstimateJSON string
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(
		&r.ID, &r.TaskType, &r.ExecutionMode, &providerID, &modelID,
		&r.ReasoningEffort, &r.TriggerObjectType, &r.TriggerObjectID, &r.Status, &costEstimateJSON,
		&errorMessage, &output, &decisionLedgerID,
		&startedAt, &finishedAt, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return r, err
	}
	r.ProviderID = providerID
	r.ModelID = modelID
	r.ErrorMessage = errorMessage
	r.Output = output
	r.DecisionLedgerID = decisionLedgerID
	r.CostEstimate = unmarshalMap(costEstimateJSON)
	if startedAt.Valid {
		r.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		r.FinishedAt = finishedAt.Time
	}
	return r, nil
}

func (s *Store) GetAgentRun(ctx context.Context, id string) (AgentRun, error) {
	row := s.db.QueryRowContext(ctx, agentRunSelectSQL+" WHERE id = ?", id)
	run, err := scanAgentRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentRun{}, ErrAgentRunNotFound
		}
		return AgentRun{}, wrapError(err, "get agent run")
	}
	return run, nil
}

func (s *Store) ListAgentRuns(ctx context.Context, filter AgentRunListFilter) ([]AgentRun, error) {
	where, args := agentRunFilterSQL(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentRunSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent runs")
	}
	return scanRows(rows, scanAgentRun, "scan agent run", "iterate agent runs")
}

func (s *Store) CountAgentRuns(ctx context.Context, filter AgentRunListFilter) (int, error) {
	where, args := agentRunFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_agent_runs WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count agent runs")
	}
	return count, nil
}

func agentRunFilterSQL(filter AgentRunListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	agentFilterAdd(&where, &args, "task_type", filter.TaskType)
	agentFilterAdd(&where, &args, "status", filter.Status)
	agentFilterAdd(&where, &args, "provider_id", filter.ProviderID)
	agentFilterAdd(&where, &args, "model_id", filter.ModelID)
	agentFilterAdd(&where, &args, "trigger_object_type", filter.TriggerObjectType)
	agentFilterAdd(&where, &args, "trigger_object_id", filter.TriggerObjectID)
	return strings.Join(where, " AND "), args
}

const agentDecisionLedgerSelectSQL = `
    SELECT id, COALESCE(run_id,''), COALESCE(provider_id,''), COALESCE(model_id,''),
           task_type, trigger_object_type, trigger_object_id, COALESCE(input_summary,''),
           COALESCE(prompt,''), COALESCE(input_artifact_summary,''),
           COALESCE(output_artifact_summary,''), structured_output_json, redaction_summary_json,
           created_at, updated_at
    FROM stockv2_agent_decision_ledgers
`

func scanAgentDecisionLedger(row rowScanner) (AgentDecisionLedger, error) {
	var l AgentDecisionLedger
	var runID, providerID, modelID, inputSummary, prompt, inputArtifact, outputArtifact, structuredJSON, redactionJSON string
	if err := row.Scan(
		&l.ID, &runID, &providerID, &modelID,
		&l.TaskType, &l.TriggerObjectType, &l.TriggerObjectID, &inputSummary,
		&prompt, &inputArtifact, &outputArtifact, &structuredJSON, &redactionJSON,
		&l.CreatedAt, &l.UpdatedAt,
	); err != nil {
		return l, err
	}
	l.RunID = runID
	l.ProviderID = providerID
	l.ModelID = modelID
	l.InputSummary = inputSummary
	l.Prompt = prompt
	l.InputArtifactSummary = inputArtifact
	l.OutputArtifactSummary = outputArtifact
	l.StructuredOutput = unmarshalMap(structuredJSON)
	l.RedactionSummary = unmarshalMap(redactionJSON)
	return l, nil
}

func (s *Store) GetAgentDecisionLedger(ctx context.Context, id string) (AgentDecisionLedger, error) {
	row := s.db.QueryRowContext(ctx, agentDecisionLedgerSelectSQL+" WHERE id = ?", id)
	ledger, err := scanAgentDecisionLedger(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentDecisionLedger{}, ErrAgentDecisionLedgerNotFound
		}
		return AgentDecisionLedger{}, wrapError(err, "get agent decision ledger")
	}
	return ledger, nil
}

// CreateAgentRunWithLedger 事务原子写入 run 与其决策账本,防半写丢失。
// 先写 ledger 拿到 id,再写 run(decision_ledger_id = ledger.id),commit。
// 对齐 CreateStrategyWithVersion 的事务模式。run 创建后先进入 ready,
// 后续由 Agent executor 推进状态并写回 stdout/MCP 结果。
func (s *Store) CreateAgentRunWithLedger(ctx context.Context, run AgentRun, ledger AgentDecisionLedger) (AgentRun, AgentDecisionLedger, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRun{}, AgentDecisionLedger{}, wrapError(err, "begin create agent run transaction")
	}
	defer tx.Rollback()

	now := time.Now()
	if ledger.ID == "" {
		ledger.ID = generateID()
	}
	if ledger.CreatedAt.IsZero() {
		ledger.CreatedAt = now
	}
	ledger.UpdatedAt = now
	if ledger.StructuredOutput == nil {
		ledger.StructuredOutput = map[string]any{}
	}
	if ledger.RedactionSummary == nil {
		ledger.RedactionSummary = map[string]any{}
	}
	if run.ID == "" {
		run.ID = generateID()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.UpdatedAt = now
	if run.CostEstimate == nil {
		run.CostEstimate = map[string]any{}
	}
	run.DecisionLedgerID = ledger.ID
	ledger.RunID = run.ID

	if err := insertAgentDecisionLedgerWithTx(ctx, tx, ledger); err != nil {
		return AgentRun{}, AgentDecisionLedger{}, err
	}
	if err := insertAgentRunWithTx(ctx, tx, run); err != nil {
		return AgentRun{}, AgentDecisionLedger{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRun{}, AgentDecisionLedger{}, wrapError(err, "commit create agent run")
	}
	return run, ledger, nil
}

func insertAgentRunWithTx(ctx context.Context, tx *sql.Tx, run AgentRun) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_agent_runs
			(id, task_type, execution_mode, provider_id, model_id, reasoning_effort, trigger_object_type, trigger_object_id,
			 status, cost_estimate_json, error_message, output, decision_ledger_id,
			 started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.TaskType,
		run.ExecutionMode,
		nullableString(run.ProviderID),
		nullableString(run.ModelID),
		run.ReasoningEffort,
		run.TriggerObjectType,
		run.TriggerObjectID,
		run.Status,
		marshalMap(run.CostEstimate),
		nullableString(run.ErrorMessage),
		run.Output,
		nullableString(run.DecisionLedgerID),
		nullableTime(run.StartedAt),
		nullableTime(run.FinishedAt),
		run.CreatedAt,
		run.UpdatedAt,
	)
	return wrapError(err, "insert agent run")
}

func insertAgentDecisionLedgerWithTx(ctx context.Context, tx *sql.Tx, ledger AgentDecisionLedger) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_agent_decision_ledgers
			(id, run_id, provider_id, model_id, task_type, trigger_object_type, trigger_object_id,
			 input_summary, prompt, input_artifact_summary, output_artifact_summary,
			 structured_output_json, redaction_summary_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		ledger.ID,
		nullableString(ledger.RunID),
		nullableString(ledger.ProviderID),
		nullableString(ledger.ModelID),
		ledger.TaskType,
		ledger.TriggerObjectType,
		ledger.TriggerObjectID,
		nullableString(ledger.InputSummary),
		nullableString(ledger.Prompt),
		nullableString(ledger.InputArtifactSummary),
		nullableString(ledger.OutputArtifactSummary),
		marshalMap(ledger.StructuredOutput),
		marshalMap(ledger.RedactionSummary),
		ledger.CreatedAt,
		ledger.UpdatedAt,
	)
	return wrapError(err, "insert agent decision ledger")
}

// ============================ updates ============================

func (s *Store) UpdateAgentRun(ctx context.Context, run AgentRun) (AgentRun, error) {
	if run.CostEstimate == nil {
		run.CostEstimate = map[string]any{}
	}
	run.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_runs
		SET status = ?, cost_estimate_json = ?, error_message = ?, output = ?,
		    finished_at = ?, updated_at = ?
		WHERE id = ?
	`,
		run.Status,
		marshalMap(run.CostEstimate),
		nullableString(run.ErrorMessage),
		nullableString(run.Output),
		nullableTime(run.FinishedAt),
		run.UpdatedAt,
		run.ID,
	)
	if err != nil {
		return AgentRun{}, wrapError(err, "update agent run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return AgentRun{}, ErrAgentRunNotFound
	}
	return run, nil
}

func (s *Store) FailActiveAgentRuns(ctx context.Context, reason string) (int64, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_runs
		SET status=?, error_message=?, finished_at=?, updated_at=?
		WHERE status IN (?, ?)
	`, AgentRunStatusFailed, nullableString(reason), now, now,
		AgentRunStatusReady, AgentRunStatusRunning)
	if err != nil {
		return 0, wrapError(err, "fail interrupted active agent runs")
	}
	count, _ := result.RowsAffected()
	return count, nil
}

func (s *Store) UpdateAgentDecisionLedger(ctx context.Context, ledger AgentDecisionLedger) (AgentDecisionLedger, error) {
	if ledger.StructuredOutput == nil {
		ledger.StructuredOutput = map[string]any{}
	}
	if ledger.RedactionSummary == nil {
		ledger.RedactionSummary = map[string]any{}
	}
	ledger.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_decision_ledgers
		SET prompt = ?, output_artifact_summary = ?, structured_output_json = ?, redaction_summary_json = ?,
		    updated_at = ?
		WHERE id = ?
	`,
		nullableString(ledger.Prompt),
		nullableString(ledger.OutputArtifactSummary),
		marshalMap(ledger.StructuredOutput),
		marshalMap(ledger.RedactionSummary),
		ledger.UpdatedAt,
		ledger.ID,
	)
	if err != nil {
		return AgentDecisionLedger{}, wrapError(err, "update agent decision ledger")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return AgentDecisionLedger{}, ErrAgentDecisionLedgerNotFound
	}
	return ledger, nil
}
