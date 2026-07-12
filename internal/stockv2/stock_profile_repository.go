package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Store) UpsertStockProfile(ctx context.Context, profile StockProfile) (StockProfile, error) {
	now := time.Now()
	profile.UpdatedAt = now
	profile.InstrumentType = normalizeInstrumentType(profile.InstrumentType)
	if profile.ProfileVersion <= 0 {
		profile.ProfileVersion = 1
	}
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = profile.Symbol
	}

	err := retryStockV2TransientWriteConflict(ctx, func() error {
		tx, beginErr := s.assetDB().BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback()
		if err := upsertStockProfileWithExecer(ctx, tx, profile); err != nil {
			return err
		}
		if err := syncStockProfileAIBaseWithTx(ctx, tx, profile.Symbol, profile.BaseProfileHash, now); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return StockProfile{}, wrapError(err, "upsert stock profile")
	}
	return profile, nil
}

type stockProfileExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func upsertStockProfileWithExecer(ctx context.Context, exec stockProfileExecer, profile StockProfile) error {
	aliasesJSON := marshalProfileStrings(profile.Aliases)
	sectorsJSON := marshalProfileStrings(profile.Sectors)
	conceptsJSON := marshalProfileStrings(profile.Concepts)
	tagsJSON := marshalProfileStrings(profile.Tags)
	aliasesZhJSON := marshalProfileStrings(profile.AliasesZh)
	aliasesEnJSON := marshalProfileStrings(profile.AliasesEn)
	keywordsZhJSON := marshalProfileStrings(profile.KeywordsZh)
	keywordsEnJSON := marshalProfileStrings(profile.KeywordsEn)
	businessLinesZhJSON := marshalProfileStrings(profile.BusinessLinesZh)
	businessLinesEnJSON := marshalProfileStrings(profile.BusinessLinesEn)
	riskTagsZhJSON := marshalProfileStrings(profile.RiskTagsZh)
	riskTagsEnJSON := marshalProfileStrings(profile.RiskTagsEn)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profiles (
			symbol, market, instrument_type, name, aliases_json, industry, sectors_json,
			concepts_json, tags_json, business_summary, profile_text, fund_type,
			tracking_index, theme, constituent_hint, profile_version,
			aliases_zh_json, aliases_en_json, keywords_zh_json, keywords_en_json,
			business_summary_zh, business_summary_en, business_lines_zh_json,
			business_lines_en_json, risk_tags_zh_json, risk_tags_en_json,
			profile_text_zh, profile_text_en, ai_profile_status, ai_profile_model,
			ai_profile_confidence, ai_profile_error, ai_profile_updated_at, ai_profile_attempted_at,
			base_profile_hash, base_profile_updated_at, base_profile_checked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			market = excluded.market, instrument_type = excluded.instrument_type,
			name = excluded.name, aliases_json = excluded.aliases_json,
			industry = excluded.industry, sectors_json = excluded.sectors_json,
			concepts_json = excluded.concepts_json, tags_json = excluded.tags_json,
			business_summary = excluded.business_summary, profile_text = excluded.profile_text,
			fund_type = excluded.fund_type, tracking_index = excluded.tracking_index,
			theme = excluded.theme, constituent_hint = excluded.constituent_hint,
			profile_version = excluded.profile_version,
			aliases_zh_json = excluded.aliases_zh_json, aliases_en_json = excluded.aliases_en_json,
			keywords_zh_json = excluded.keywords_zh_json, keywords_en_json = excluded.keywords_en_json,
			business_summary_zh = excluded.business_summary_zh, business_summary_en = excluded.business_summary_en,
			business_lines_zh_json = excluded.business_lines_zh_json,
			business_lines_en_json = excluded.business_lines_en_json,
			risk_tags_zh_json = excluded.risk_tags_zh_json, risk_tags_en_json = excluded.risk_tags_en_json,
			profile_text_zh = excluded.profile_text_zh, profile_text_en = excluded.profile_text_en,
			ai_profile_status = excluded.ai_profile_status, ai_profile_model = excluded.ai_profile_model,
			ai_profile_confidence = excluded.ai_profile_confidence, ai_profile_error = excluded.ai_profile_error,
			ai_profile_updated_at = excluded.ai_profile_updated_at,
			ai_profile_attempted_at = excluded.ai_profile_attempted_at,
			base_profile_hash = excluded.base_profile_hash,
			base_profile_updated_at = excluded.base_profile_updated_at,
			base_profile_checked_at = excluded.base_profile_checked_at,
			updated_at = excluded.updated_at
	`, profile.Symbol, profile.Market, profile.InstrumentType, profile.Name, aliasesJSON,
		profile.Industry, sectorsJSON, conceptsJSON, tagsJSON, profile.BusinessSummary,
		profile.ProfileText, profile.FundType, profile.TrackingIndex, profile.Theme,
		profile.ConstituentHint, profile.ProfileVersion, aliasesZhJSON, aliasesEnJSON,
		keywordsZhJSON, keywordsEnJSON, profile.BusinessSummaryZh, profile.BusinessSummaryEn,
		businessLinesZhJSON, businessLinesEnJSON, riskTagsZhJSON, riskTagsEnJSON,
		profile.ProfileTextZh, profile.ProfileTextEn, profile.AIProfileStatus, profile.AIProfileModel,
		profile.AIProfileConfidence, profile.AIProfileError, nullableTime(profile.AIProfileUpdatedAt),
		nullableTime(profile.AIProfileAttemptedAt), profile.BaseProfileHash,
		nullableTime(profile.BaseProfileUpdatedAt), nullableTime(profile.BaseProfileCheckedAt), profile.UpdatedAt)
	return wrapError(err, "upsert stock profile row")
}

func (s *Store) GetStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	row := s.assetDB().QueryRowContext(ctx, stockProfileSelectSQL()+` WHERE symbol = ?`, strings.TrimSpace(symbol))
	profile, err := scanStockProfile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockProfile{}, ErrStockProfileNotFound
		}
		return StockProfile{}, wrapError(err, "get stock profile")
	}
	return profile, nil
}

func (s *Store) UpdateStockProfileAIState(ctx context.Context, symbol, status, message string, markAttempt bool) error {
	now := time.Now()
	result, err := s.assetDB().ExecContext(ctx, `
		UPDATE stockv2_stock_profiles
		SET ai_profile_status = ?, ai_profile_error = ?,
			ai_profile_attempted_at = CASE WHEN ? THEN ? ELSE ai_profile_attempted_at END,
			updated_at = ?
		WHERE symbol = ?
	`, status, safelog.Text(message, 500), markAttempt, now, now, strings.TrimSpace(symbol))
	if err != nil {
		return wrapError(err, "update stock profile ai state")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrStockProfileNotFound
	}
	return nil
}

func (s *Store) ListStockProfiles(ctx context.Context, filter StockProfileListFilter) ([]StockProfile, error) {
	where, args := stockProfileWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.assetDB().QueryContext(ctx, stockProfileSelectSQL()+where+` ORDER BY updated_at DESC, symbol ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list stock profiles")
	}
	return scanRows(rows, scanStockProfile, "scan stock profile", "iterate stock profiles")
}

func (s *Store) CountStockProfiles(ctx context.Context, filter StockProfileListFilter) (int, error) {
	where, args := stockProfileWhere(filter)
	var total int
	err := s.assetDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_stock_profiles`+where, args...).Scan(&total)
	return total, wrapError(err, "count stock profiles")
}

func (s *Store) ListStockProfileSummaries(ctx context.Context, symbols []string) (map[string]StockProfileSummary, error) {
	symbols = compactStringList(symbols, 200)
	if len(symbols) == 0 {
		return map[string]StockProfileSummary{}, nil
	}
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := s.assetDB().QueryContext(ctx, `
		SELECT symbol, COALESCE(market,''), COALESCE(instrument_type,'stock'),
		       COALESCE(profile_text,''), COALESCE(business_summary_zh,''),
		       COALESCE(business_summary,''), COALESCE(business_summary_en,''),
		       COALESCE(ai_profile_status,'missing'), COALESCE(ai_profile_model,''),
		       COALESCE(ai_profile_confidence,0), base_profile_updated_at, base_profile_checked_at,
		       ai_profile_updated_at, updated_at,
		       COALESCE((SELECT desired_input_version FROM stockv2_stock_profile_ai_states a WHERE a.symbol = stockv2_stock_profiles.symbol), ''),
		       COALESCE((SELECT applied_input_version FROM stockv2_stock_profile_ai_states a WHERE a.symbol = stockv2_stock_profiles.symbol), '')
		FROM stockv2_stock_profiles
		WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list stock profile summaries")
	}
	defer rows.Close()

	out := make(map[string]StockProfileSummary, len(symbols))
	for rows.Next() {
		var item StockProfileSummary
		var profileText, summaryZh, summary, summaryEn string
		var baseUpdatedAt, baseCheckedAt, aiUpdatedAt sql.NullTime
		if err := rows.Scan(
			&item.Symbol,
			&item.Market,
			&item.InstrumentType,
			&profileText,
			&summaryZh,
			&summary,
			&summaryEn,
			&item.AIProfileStatus,
			&item.AIProfileModel,
			&item.AIProfileConfidence,
			&baseUpdatedAt,
			&baseCheckedAt,
			&aiUpdatedAt,
			&item.UpdatedAt,
			&item.AIDesiredInputVersion,
			&item.AIAppliedInputVersion,
		); err != nil {
			return nil, wrapError(err, "scan stock profile summary")
		}
		item.Status = "ready"
		if strings.TrimSpace(profileText) == "" {
			item.Status = "partial"
		}
		item.BusinessSummary = firstNonEmpty(summaryZh, summary, summaryEn)
		if baseUpdatedAt.Valid {
			item.BaseProfileUpdatedAt = baseUpdatedAt.Time
		}
		if baseCheckedAt.Valid {
			item.BaseProfileCheckedAt = baseCheckedAt.Time
		}
		if aiUpdatedAt.Valid {
			item.AIProfileUpdatedAt = aiUpdatedAt.Time
		}
		if item.AIProfileStatus == "" {
			item.AIProfileStatus = StockProfileAIStatusMissing
		}
		out[item.Symbol] = item
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate stock profile summaries")
	}
	return out, nil
}

func (s *Store) CreateStockProfileUpdateTask(ctx context.Context, task StockProfileUpdateTask) (StockProfileUpdateTask, error) {
	now := time.Now()
	if task.ID == "" {
		task.ID = generateID()
	}
	if task.TriggerSource == "" {
		task.TriggerSource = StockProfileUpdateTriggerManual
	}
	if task.Status == "" {
		task.Status = StockProfileUpdateStatusCompleted
	}
	if task.StartedAt.IsZero() {
		task.StartedAt = now
	}
	if task.FinishedAt.IsZero() && (task.Status == StockProfileUpdateStatusCompleted ||
		task.Status == StockProfileUpdateStatusPartial || task.Status == StockProfileUpdateStatusFailed) {
		task.FinishedAt = now
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	sourceStatusesJSON := marshalStockProfileSourceStatuses(task.SourceStatuses)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profile_update_tasks (
			id, symbol, market, trigger_source, trigger_reason, status,
			base_input_hash_before, base_input_hash_after, base_input_changed,
			base_profile_status, ai_decision, agent_run_id, ai_profile_status,
			ai_profile_error, source_statuses_json, error_message,
			started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		task.ID,
		task.Symbol,
		nullableString(task.Market),
		task.TriggerSource,
		nullableString(task.TriggerReason),
		task.Status,
		nullableString(task.BaseInputHashBefore),
		nullableString(task.BaseInputHashAfter),
		boolToInt(task.BaseInputChanged),
		nullableString(task.BaseProfileStatus),
		task.AIDecision,
		nullableString(task.AgentRunID),
		nullableString(task.AIProfileStatus),
		nullableString(task.AIProfileError),
		sourceStatusesJSON,
		nullableString(task.ErrorMessage),
		task.StartedAt,
		nullableTime(task.FinishedAt),
		task.CreatedAt,
		task.UpdatedAt,
	)
	if err != nil {
		return StockProfileUpdateTask{}, wrapError(err, "create stock profile update task")
	}
	return task, nil
}

func (s *Store) ListStockProfileUpdateTasks(ctx context.Context, filter StockProfileUpdateTaskListFilter) ([]StockProfileUpdateTask, error) {
	where, args := stockProfileUpdateTaskWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, stockProfileUpdateTaskSelectSQL()+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list stock profile update tasks")
	}
	return scanRows(rows, scanStockProfileUpdateTask, "scan stock profile update task", "iterate stock profile update tasks")
}

func (s *Store) HasPendingStockProfileUpdateTask(ctx context.Context, symbol string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM stockv2_stock_profile_update_tasks
			WHERE symbol = ? AND status IN ('queued', 'running')
		)
	`, strings.TrimSpace(symbol)).Scan(&exists)
	if err != nil {
		return false, wrapError(err, "check pending stock profile update task")
	}
	return exists == 1, nil
}

func (s *Store) UpdateStockProfileUpdateTaskAIResultByAgentRunID(ctx context.Context, agentRunID, taskStatus, aiStatus, aiError string) error {
	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID == "" {
		return nil
	}
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_update_tasks
		SET status = ?, ai_profile_status = ?, ai_profile_error = ?, finished_at = ?, updated_at = ?
		WHERE agent_run_id = ?
	`, taskStatus, nullableString(aiStatus), nullableString(safelog.Text(aiError, 500)), now, now, agentRunID)
	if err != nil {
		return wrapError(err, "update stock profile update task ai result")
	}
	return nil
}

func (s *Store) CountStockProfileUpdateTasks(ctx context.Context, filter StockProfileUpdateTaskListFilter) (int, error) {
	where, args := stockProfileUpdateTaskWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_stock_profile_update_tasks`+where, args...).Scan(&total)
	return total, wrapError(err, "count stock profile update tasks")
}

func stockProfileSelectSQL() string {
	return `
		SELECT symbol, market, COALESCE(instrument_type,'stock'), name, aliases_json,
		       COALESCE(industry,''), sectors_json, concepts_json, tags_json,
		       COALESCE(business_summary,''), profile_text, COALESCE(fund_type,''),
		       COALESCE(tracking_index,''), COALESCE(theme,''), COALESCE(constituent_hint,''),
		       profile_version,
		       COALESCE(aliases_zh_json,'[]'), COALESCE(aliases_en_json,'[]'),
		       COALESCE(keywords_zh_json,'[]'), COALESCE(keywords_en_json,'[]'),
		       COALESCE(business_summary_zh,''), COALESCE(business_summary_en,''),
		       COALESCE(business_lines_zh_json,'[]'), COALESCE(business_lines_en_json,'[]'),
		       COALESCE(risk_tags_zh_json,'[]'), COALESCE(risk_tags_en_json,'[]'),
		       COALESCE(profile_text_zh,''), COALESCE(profile_text_en,''),
		       COALESCE(ai_profile_status,'missing'), COALESCE(ai_profile_model,''),
		       COALESCE(ai_profile_confidence,0), COALESCE(ai_profile_error,''),
		       ai_profile_updated_at, ai_profile_attempted_at,
		       COALESCE(base_profile_hash,''), base_profile_updated_at, base_profile_checked_at, updated_at
		FROM stockv2_stock_profiles`
}

func stockProfileWhere(filter StockProfileListFilter) (string, []any) {
	parts := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if market := strings.TrimSpace(filter.Market); market != "" {
		parts = append(parts, "market = ?")
		args = append(args, market)
	}
	if instrumentType := strings.TrimSpace(filter.InstrumentType); instrumentType != "" {
		parts = append(parts, "instrument_type = ?")
		args = append(args, normalizeInstrumentType(instrumentType))
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		parts = append(parts, "(LOWER(symbol) LIKE ? OR LOWER(name) LIKE ? OR LOWER(profile_text) LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func scanStockProfile(scanner rowScanner) (StockProfile, error) {
	var profile StockProfile
	var aliasesJSON, sectorsJSON, conceptsJSON, tagsJSON string
	var aliasesZhJSON, aliasesEnJSON, keywordsZhJSON, keywordsEnJSON string
	var businessLinesZhJSON, businessLinesEnJSON, riskTagsZhJSON, riskTagsEnJSON string
	var aiProfileUpdatedAt, aiProfileAttemptedAt, baseProfileUpdatedAt, baseProfileCheckedAt sql.NullTime
	if err := scanner.Scan(
		&profile.Symbol,
		&profile.Market,
		&profile.InstrumentType,
		&profile.Name,
		&aliasesJSON,
		&profile.Industry,
		&sectorsJSON,
		&conceptsJSON,
		&tagsJSON,
		&profile.BusinessSummary,
		&profile.ProfileText,
		&profile.FundType,
		&profile.TrackingIndex,
		&profile.Theme,
		&profile.ConstituentHint,
		&profile.ProfileVersion,
		&aliasesZhJSON,
		&aliasesEnJSON,
		&keywordsZhJSON,
		&keywordsEnJSON,
		&profile.BusinessSummaryZh,
		&profile.BusinessSummaryEn,
		&businessLinesZhJSON,
		&businessLinesEnJSON,
		&riskTagsZhJSON,
		&riskTagsEnJSON,
		&profile.ProfileTextZh,
		&profile.ProfileTextEn,
		&profile.AIProfileStatus,
		&profile.AIProfileModel,
		&profile.AIProfileConfidence,
		&profile.AIProfileError,
		&aiProfileUpdatedAt,
		&aiProfileAttemptedAt,
		&profile.BaseProfileHash,
		&baseProfileUpdatedAt,
		&baseProfileCheckedAt,
		&profile.UpdatedAt,
	); err != nil {
		return StockProfile{}, err
	}
	profile.InstrumentType = normalizeInstrumentType(profile.InstrumentType)
	profile.Aliases = unmarshalProfileStrings(aliasesJSON)
	profile.Sectors = unmarshalProfileStrings(sectorsJSON)
	profile.Concepts = unmarshalProfileStrings(conceptsJSON)
	profile.Tags = unmarshalProfileStrings(tagsJSON)
	profile.AliasesZh = unmarshalProfileStrings(aliasesZhJSON)
	profile.AliasesEn = unmarshalProfileStrings(aliasesEnJSON)
	profile.KeywordsZh = unmarshalProfileStrings(keywordsZhJSON)
	profile.KeywordsEn = unmarshalProfileStrings(keywordsEnJSON)
	profile.BusinessLinesZh = unmarshalProfileStrings(businessLinesZhJSON)
	profile.BusinessLinesEn = unmarshalProfileStrings(businessLinesEnJSON)
	profile.RiskTagsZh = unmarshalProfileStrings(riskTagsZhJSON)
	profile.RiskTagsEn = unmarshalProfileStrings(riskTagsEnJSON)
	if aiProfileUpdatedAt.Valid {
		profile.AIProfileUpdatedAt = aiProfileUpdatedAt.Time
	}
	if aiProfileAttemptedAt.Valid {
		profile.AIProfileAttemptedAt = aiProfileAttemptedAt.Time
	}
	if baseProfileUpdatedAt.Valid {
		profile.BaseProfileUpdatedAt = baseProfileUpdatedAt.Time
	}
	if baseProfileCheckedAt.Valid {
		profile.BaseProfileCheckedAt = baseProfileCheckedAt.Time
	}
	if profile.ProfileVersion <= 0 {
		profile.ProfileVersion = 1
	}
	if profile.AIProfileStatus == "" {
		profile.AIProfileStatus = StockProfileAIStatusMissing
	}
	return profile, nil
}

func marshalProfileStrings(items []string) string {
	data, _ := json.Marshal(items)
	return string(data)
}

func unmarshalProfileStrings(raw string) []string {
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []string{}
	}
	return items
}

func stockProfileUpdateTaskSelectSQL() string {
	return `
		SELECT id, symbol, COALESCE(market,''), trigger_source, COALESCE(trigger_reason,''),
		       status, COALESCE(base_input_hash_before,''), COALESCE(base_input_hash_after,''),
		       base_input_changed, COALESCE(base_profile_status,''), ai_decision,
		       COALESCE(agent_run_id,''), COALESCE(ai_profile_status,''), COALESCE(ai_profile_error,''),
		       COALESCE(source_statuses_json,'[]'), COALESCE(error_message,''),
		       started_at, finished_at, created_at, updated_at
		FROM stockv2_stock_profile_update_tasks`
}

func stockProfileUpdateTaskWhere(filter StockProfileUpdateTaskListFilter) (string, []any) {
	args := make([]any, 0, 1)
	if symbol := strings.TrimSpace(filter.Symbol); symbol != "" {
		return " WHERE symbol = ?", append(args, symbol)
	}
	return "", args
}

func scanStockProfileUpdateTask(scanner rowScanner) (StockProfileUpdateTask, error) {
	var task StockProfileUpdateTask
	var changed int
	var sourceStatusesJSON string
	var finishedAt sql.NullTime
	if err := scanner.Scan(
		&task.ID,
		&task.Symbol,
		&task.Market,
		&task.TriggerSource,
		&task.TriggerReason,
		&task.Status,
		&task.BaseInputHashBefore,
		&task.BaseInputHashAfter,
		&changed,
		&task.BaseProfileStatus,
		&task.AIDecision,
		&task.AgentRunID,
		&task.AIProfileStatus,
		&task.AIProfileError,
		&sourceStatusesJSON,
		&task.ErrorMessage,
		&task.StartedAt,
		&finishedAt,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return StockProfileUpdateTask{}, err
	}
	task.BaseInputChanged = changed != 0
	task.SourceStatuses = unmarshalStockProfileSourceStatuses(sourceStatusesJSON)
	if finishedAt.Valid {
		task.FinishedAt = finishedAt.Time
	}
	return task, nil
}

func marshalStockProfileSourceStatuses(items []StockProfileSourceStatus) string {
	if items == nil {
		items = []StockProfileSourceStatus{}
	}
	data, _ := json.Marshal(items)
	return string(data)
}

func unmarshalStockProfileSourceStatuses(raw string) []StockProfileSourceStatus {
	var items []StockProfileSourceStatus
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []StockProfileSourceStatus{}
	}
	return items
}

func parseStockProfileTaskTime(value string) time.Time {
	text := strings.TrimSpace(value)
	if text == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
