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

func normalizedAgentLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizedAgentOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func nullableAgentString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableAgentTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

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
		nullableAgentString(profile.DisplayName),
		profile.ConfigState,
		profile.AuthState,
		profile.Availability,
		nullableAgentTime(profile.LastProbeAt),
		nullableAgentString(profile.LastProbeResult),
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
	args = append(args, normalizedAgentLimit(filter.Limit), normalizedAgentOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentProviderProfileSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent provider profiles")
	}
	defer rows.Close()
	items := make([]AgentProviderProfile, 0)
	for rows.Next() {
		p, err := scanAgentProviderProfile(rows)
		if err != nil {
			return nil, wrapError(err, "scan agent provider profile")
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate agent provider profiles")
	}
	return items, nil
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
		nullableAgentString(profile.DisplayName),
		profile.ConfigState,
		profile.AuthState,
		profile.Availability,
		nullableAgentTime(profile.LastProbeAt),
		nullableAgentString(profile.LastProbeResult),
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
           context_limit, confirm_required, metadata_json, created_at, updated_at
    FROM stockv2_agent_model_profiles
`

func scanAgentModelProfile(row rowScanner) (AgentModelProfile, error) {
	var m AgentModelProfile
	var displayName, metadataJSON string
	var enabled, confirmRequired int
	if err := row.Scan(
		&m.ID, &m.ProviderID, &m.ModelName, &displayName, &enabled, &m.Status, &m.CostLevel,
		&m.ContextLimit, &confirmRequired, &metadataJSON, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return m, err
	}
	m.DisplayName = displayName
	m.Enabled = enabled != 0
	m.ConfirmRequired = confirmRequired != 0
	m.Metadata = unmarshalMap(metadataJSON)
	return m, nil
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_agent_model_profiles
			(id, provider_id, model_name, display_name, enabled, status, cost_level,
			 context_limit, confirm_required, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		model.ID,
		model.ProviderID,
		model.ModelName,
		nullableAgentString(model.DisplayName),
		boolToInt(model.Enabled),
		model.Status,
		model.CostLevel,
		model.ContextLimit,
		boolToInt(model.ConfirmRequired),
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
	args = append(args, normalizedAgentLimit(filter.Limit), normalizedAgentOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentModelProfileSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent model profiles")
	}
	defer rows.Close()
	items := make([]AgentModelProfile, 0)
	for rows.Next() {
		m, err := scanAgentModelProfile(rows)
		if err != nil {
			return nil, wrapError(err, "scan agent model profile")
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate agent model profiles")
	}
	return items, nil
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
	model.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_model_profiles
		SET provider_id = ?, model_name = ?, display_name = ?, enabled = ?, status = ?,
		    cost_level = ?, context_limit = ?, confirm_required = ?, metadata_json = ?, updated_at = ?
		WHERE id = ?
	`,
		model.ProviderID,
		model.ModelName,
		nullableAgentString(model.DisplayName),
		boolToInt(model.Enabled),
		model.Status,
		model.CostLevel,
		model.ContextLimit,
		boolToInt(model.ConfirmRequired),
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
    SELECT id, task_type, COALESCE(primary_model_id,''), COALESCE(fallback_model_id,''),
           confirm_required, max_budget, created_at, updated_at
    FROM stockv2_agent_task_profiles
`

func scanAgentTaskProfile(row rowScanner) (AgentTaskProfile, error) {
	var t AgentTaskProfile
	var confirmRequired int
	if err := row.Scan(
		&t.ID, &t.TaskType, &t.PrimaryModelID, &t.FallbackModelID,
		&confirmRequired, &t.MaxBudget, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return t, err
	}
	t.ConfirmRequired = confirmRequired != 0
	return t, nil
}

// 无 Create:operation_review 由 schema 内 INSERT OR IGNORE 种入;
// 其余 task type 本轮不支持(service 层校验拒绝)。

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
	args = append(args, normalizedAgentLimit(filter.Limit), normalizedAgentOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at ASC LIMIT ? OFFSET ?`, agentTaskProfileSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent task profiles")
	}
	defer rows.Close()
	items := make([]AgentTaskProfile, 0)
	for rows.Next() {
		tp, err := scanAgentTaskProfile(rows)
		if err != nil {
			return nil, wrapError(err, "scan agent task profile")
		}
		items = append(items, tp)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate agent task profiles")
	}
	return items, nil
}

func (s *Store) CountAgentTaskProfiles(ctx context.Context, filter AgentTaskProfileListFilter) (int, error) {
	where, args := agentTaskProfileFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_agent_task_profiles WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count agent task profiles")
	}
	return count, nil
}

// UpdateAgentTaskProfile 只改 model 绑定/confirm/budget,task_type 为自然键不可变。
func (s *Store) UpdateAgentTaskProfile(ctx context.Context, profile AgentTaskProfile) (AgentTaskProfile, error) {
	profile.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_task_profiles
		SET primary_model_id = ?, fallback_model_id = ?, confirm_required = ?, max_budget = ?, updated_at = ?
		WHERE id = ?
	`,
		nullableAgentString(profile.PrimaryModelID),
		nullableAgentString(profile.FallbackModelID),
		boolToInt(profile.ConfirmRequired),
		profile.MaxBudget,
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

func agentTaskProfileFilterSQL(filter AgentTaskProfileListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	agentFilterAdd(&where, &args, "task_type", filter.TaskType)
	return strings.Join(where, " AND "), args
}

// ============================ authorizations ============================

const agentAuthorizationSelectSQL = `
    SELECT id, task_type, COALESCE(task_profile_id,''), COALESCE(provider_id,''),
           COALESCE(model_id,''), trigger_object_type, trigger_object_id, status,
           COALESCE(reason,''), COALESCE(requested_by,''), decided_at, COALESCE(decision_reason,''),
           created_at, updated_at
    FROM stockv2_agent_authorizations
`

func scanAgentAuthorization(row rowScanner) (AgentAuthorization, error) {
	var a AgentAuthorization
	var taskProfileID, providerID, modelID, reason, requestedBy, decisionReason string
	var decidedAt sql.NullTime
	if err := row.Scan(
		&a.ID, &a.TaskType, &taskProfileID, &providerID, &modelID,
		&a.TriggerObjectType, &a.TriggerObjectID, &a.Status,
		&reason, &requestedBy, &decidedAt, &decisionReason,
		&a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return a, err
	}
	a.TaskProfileID = taskProfileID
	a.ProviderID = providerID
	a.ModelID = modelID
	a.Reason = reason
	a.RequestedBy = requestedBy
	a.DecisionReason = decisionReason
	if decidedAt.Valid {
		a.DecidedAt = decidedAt.Time
	}
	return a, nil
}

func (s *Store) CreateAgentAuthorization(ctx context.Context, auth AgentAuthorization) (AgentAuthorization, error) {
	now := time.Now()
	if auth.ID == "" {
		auth.ID = generateID()
	}
	if auth.CreatedAt.IsZero() {
		auth.CreatedAt = now
	}
	auth.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_agent_authorizations
			(id, task_type, task_profile_id, provider_id, model_id, trigger_object_type,
			 trigger_object_id, status, reason, requested_by, decided_at, decision_reason,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		auth.ID,
		auth.TaskType,
		nullableAgentString(auth.TaskProfileID),
		nullableAgentString(auth.ProviderID),
		nullableAgentString(auth.ModelID),
		auth.TriggerObjectType,
		auth.TriggerObjectID,
		auth.Status,
		nullableAgentString(auth.Reason),
		nullableAgentString(auth.RequestedBy),
		nullableAgentTime(auth.DecidedAt),
		nullableAgentString(auth.DecisionReason),
		auth.CreatedAt,
		auth.UpdatedAt,
	)
	return auth, wrapError(err, "create agent authorization")
}

func (s *Store) GetAgentAuthorization(ctx context.Context, id string) (AgentAuthorization, error) {
	row := s.db.QueryRowContext(ctx, agentAuthorizationSelectSQL+" WHERE id = ?", id)
	auth, err := scanAgentAuthorization(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentAuthorization{}, ErrAgentAuthorizationNotFound
		}
		return AgentAuthorization{}, wrapError(err, "get agent authorization")
	}
	return auth, nil
}

func (s *Store) ListAgentAuthorizations(ctx context.Context, filter AgentAuthorizationListFilter) ([]AgentAuthorization, error) {
	where, args := agentAuthorizationFilterSQL(filter)
	args = append(args, normalizedAgentLimit(filter.Limit), normalizedAgentOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentAuthorizationSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent authorizations")
	}
	defer rows.Close()
	items := make([]AgentAuthorization, 0)
	for rows.Next() {
		a, err := scanAgentAuthorization(rows)
		if err != nil {
			return nil, wrapError(err, "scan agent authorization")
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate agent authorizations")
	}
	return items, nil
}

func (s *Store) CountAgentAuthorizations(ctx context.Context, filter AgentAuthorizationListFilter) (int, error) {
	where, args := agentAuthorizationFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_agent_authorizations WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count agent authorizations")
	}
	return count, nil
}

// UpdateAgentAuthorizationDecision 推进授权状态;approved/denied 时写 decided_at。
// decisionReason 已由 service 层脱敏。
func (s *Store) UpdateAgentAuthorizationDecision(ctx context.Context, id, status, decisionReason string) (AgentAuthorization, error) {
	now := time.Now()
	var decidedAt any
	if status == AgentAuthorizationStatusApproved || status == AgentAuthorizationStatusDenied {
		decidedAt = now
	} else {
		decidedAt = nil
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_agent_authorizations
		SET status = ?, decision_reason = ?, decided_at = ?, updated_at = ?
		WHERE id = ?
	`,
		status,
		nullableAgentString(decisionReason),
		decidedAt,
		now,
		id,
	)
	if err != nil {
		return AgentAuthorization{}, wrapError(err, "update agent authorization decision")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return AgentAuthorization{}, ErrAgentAuthorizationNotFound
	}
	return s.GetAgentAuthorization(ctx, id)
}

func agentAuthorizationFilterSQL(filter AgentAuthorizationListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	agentFilterAdd(&where, &args, "task_type", filter.TaskType)
	agentFilterAdd(&where, &args, "status", filter.Status)
	agentFilterAdd(&where, &args, "trigger_object_type", filter.TriggerObjectType)
	agentFilterAdd(&where, &args, "trigger_object_id", filter.TriggerObjectID)
	return strings.Join(where, " AND "), args
}

// ============================ runs + decision ledger ============================

const agentRunSelectSQL = `
    SELECT id, task_type, COALESCE(provider_id,''), COALESCE(model_id,''),
           trigger_object_type, trigger_object_id, status, cost_estimate_json,
           COALESCE(error_message,''), COALESCE(output,''), COALESCE(decision_ledger_id,''),
           COALESCE(authorization_id,''), started_at, finished_at, created_at, updated_at
    FROM stockv2_agent_runs
`

func scanAgentRun(row rowScanner) (AgentRun, error) {
	var r AgentRun
	var providerID, modelID, errorMessage, output, decisionLedgerID, authorizationID, costEstimateJSON string
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(
		&r.ID, &r.TaskType, &providerID, &modelID,
		&r.TriggerObjectType, &r.TriggerObjectID, &r.Status, &costEstimateJSON,
		&errorMessage, &output, &decisionLedgerID, &authorizationID,
		&startedAt, &finishedAt, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return r, err
	}
	r.ProviderID = providerID
	r.ModelID = modelID
	r.ErrorMessage = errorMessage
	r.Output = output
	r.DecisionLedgerID = decisionLedgerID
	r.AuthorizationID = authorizationID
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
	args = append(args, normalizedAgentLimit(filter.Limit), normalizedAgentOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, agentRunSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list agent runs")
	}
	defer rows.Close()
	items := make([]AgentRun, 0)
	for rows.Next() {
		r, err := scanAgentRun(rows)
		if err != nil {
			return nil, wrapError(err, "scan agent run")
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate agent runs")
	}
	return items, nil
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
// 对齐 CreateStrategyWithVersion 的事务模式。本轮 run 不真实调用模型,
// 不写假 output;Status 由 service 设为 ready。
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
			(id, task_type, provider_id, model_id, trigger_object_type, trigger_object_id,
			 status, cost_estimate_json, error_message, output, decision_ledger_id,
			 authorization_id, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.TaskType,
		nullableAgentString(run.ProviderID),
		nullableAgentString(run.ModelID),
		run.TriggerObjectType,
		run.TriggerObjectID,
		run.Status,
		marshalMap(run.CostEstimate),
		nullableAgentString(run.ErrorMessage),
		run.Output,
		nullableAgentString(run.DecisionLedgerID),
		nullableAgentString(run.AuthorizationID),
		nullableAgentTime(run.StartedAt),
		nullableAgentTime(run.FinishedAt),
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
		nullableAgentString(ledger.RunID),
		nullableAgentString(ledger.ProviderID),
		nullableAgentString(ledger.ModelID),
		ledger.TaskType,
		ledger.TriggerObjectType,
		ledger.TriggerObjectID,
		nullableAgentString(ledger.InputSummary),
		nullableAgentString(ledger.Prompt),
		nullableAgentString(ledger.InputArtifactSummary),
		nullableAgentString(ledger.OutputArtifactSummary),
		marshalMap(ledger.StructuredOutput),
		marshalMap(ledger.RedactionSummary),
		ledger.CreatedAt,
		ledger.UpdatedAt,
	)
	return wrapError(err, "insert agent decision ledger")
}
