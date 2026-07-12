package stockv2

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const stockProfileAIInputSchemaVersion = 3

type stockProfileAIAnnouncementBaseline struct {
	revision      int64
	latestCreated time.Time
}

type stockProfileDataSummaryRow struct {
	Symbol        string  `json:"-"`
	TradeDate     string  `json:"tradeDate"`
	Close         float64 `json:"close"`
	MainNetInflow float64 `json:"mainNetInflow"`
}

func stockProfileDesiredInputVersion(
	symbol, baseHash string,
	announcementRevision int64,
	dataSummaryVersion string,
	manualGeneration int64,
) string {
	parts := []string{
		"stock-profile-ai:v3",
		strings.TrimSpace(symbol),
		strconv.Itoa(stockProfileAIInputSchemaVersion),
		strings.TrimSpace(baseHash),
		strconv.FormatInt(announcementRevision, 10),
		strings.TrimSpace(dataSummaryVersion),
		strconv.FormatInt(manualGeneration, 10),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *Store) ensureStockProfileAIStateSchema(ctx context.Context) error {
	for _, stmt := range []string{
		`ALTER TABLE stockv2_stock_profile_ai_states ADD COLUMN IF NOT EXISTS data_summary_version VARCHAR DEFAULT ''`,
		`ALTER TABLE stockv2_stock_profile_ai_versions ADD COLUMN IF NOT EXISTS data_summary_version VARCHAR DEFAULT ''`,
	} {
		if _, err := s.assetDB().ExecContext(ctx, stmt); err != nil {
			return wrapError(err, "migrate stock profile AI data summary version")
		}
	}
	return nil
}

type stockProfileAIDataQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// stockProfileDataSummaryVersionWithQuerier hashes the same five-day price and
// main-flow inputs exposed to the profile prompt. Fetched timestamps are omitted
// so an idempotent refresh cannot supersede an in-flight AI run.
func stockProfileDataSummaryVersionWithQuerier(
	ctx context.Context,
	q stockProfileAIDataQuerier,
	symbol string,
) (string, error) {
	versions, err := stockProfileDataSummaryVersionsWithQuerier(ctx, q, []string{symbol})
	if err != nil {
		return "", err
	}
	return versions[strings.TrimSpace(symbol)], nil
}

func stockProfileDataSummaryVersionsWithQuerier(
	ctx context.Context,
	q stockProfileAIDataQuerier,
	symbols []string,
) (map[string]string, error) {
	symbols = compactStringList(symbols, 500)
	versions := make(map[string]string, len(symbols))
	if len(symbols) == 0 {
		return versions, nil
	}
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := q.QueryContext(ctx, `
		WITH ranked AS (
			SELECT symbol, trade_date, close, main_net_inflow,
			       ROW_NUMBER() OVER (
				   PARTITION BY symbol, adjusted, trade_date
				   ORDER BY
					   CASE WHEN
						   COALESCE(open, 0) > 0 AND isfinite(COALESCE(open, 0)) AND
						   COALESCE(high, 0) > 0 AND isfinite(COALESCE(high, 0)) AND
						   COALESCE(low, 0) > 0 AND isfinite(COALESCE(low, 0)) AND
						   COALESCE(close, 0) > 0 AND isfinite(COALESCE(close, 0)) AND
						   COALESCE(volume, 0) > 0 AND isfinite(COALESCE(volume, 0)) AND
						   high >= greatest(open, close, low) AND low <= least(open, close, high) AND
						   (COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND
						   isfinite(COALESCE(amount, 0)) AND
						   (COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
						   COALESCE(turnover_rate, 0) >= 0 AND isfinite(COALESCE(turnover_rate, 0))
					   THEN 0 ELSE 1 END,
					   CASE WHEN
						   (COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND
						   isfinite(COALESCE(net_inflow, 0)) AND
						   (COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0) AND
						   isfinite(COALESCE(main_net_inflow, 0))
					   THEN 0 ELSE 1 END,
					   fetched_at DESC
			   ) AS rn
			FROM stockv2_daily_bars
			WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`) AND adjusted = 'none'
		), selected AS (
			SELECT symbol, trade_date, close, main_net_inflow,
			       ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY trade_date DESC) AS recent_rank
			FROM ranked
			WHERE rn = 1
		)
		SELECT symbol, strftime(trade_date, '%Y-%m-%d'),
		       CASE WHEN isfinite(COALESCE(close, 0)) THEN COALESCE(close, 0) ELSE 0 END,
		       CASE WHEN isfinite(COALESCE(main_net_inflow, 0)) THEN COALESCE(main_net_inflow, 0) ELSE 0 END
		FROM selected
		WHERE recent_rank <= 5
		ORDER BY symbol, trade_date DESC
	`, args...)
	if err != nil {
		return nil, wrapError(err, "load stock profile AI data summary")
	}
	defer rows.Close()
	data := make(map[string][]stockProfileDataSummaryRow, len(symbols))
	for rows.Next() {
		var item stockProfileDataSummaryRow
		if err := rows.Scan(&item.Symbol, &item.TradeDate, &item.Close, &item.MainNetInflow); err != nil {
			return nil, wrapError(err, "scan stock profile AI data summary")
		}
		data[item.Symbol] = append(data[item.Symbol], item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate stock profile AI data summary")
	}
	for _, symbol := range symbols {
		raw, err := json.Marshal(data[symbol])
		if err != nil {
			return nil, wrapError(err, "marshal stock profile AI data summary")
		}
		sum := sha256.Sum256(append([]byte("stock-profile-data-summary:v1\x00"), raw...))
		versions[symbol] = hex.EncodeToString(sum[:])
	}
	return versions, nil
}

// BackfillStockProfileAIStates upgrades profiles created before durable input
// versions existed. Existing summaries are retained as immutable versions; a
// summary is only attached to the current target when its successful timestamp
// covers both the current base profile and every announcement already stored.
func (s *Store) BackfillStockProfileAIStates(ctx context.Context) (int, error) {
	if err := s.ensureStockProfileAIStateSchema(ctx); err != nil {
		return 0, err
	}
	rows, err := s.assetDB().QueryContext(ctx, stockProfileSelectSQL()+`
		WHERE NOT EXISTS (
			SELECT 1 FROM stockv2_stock_profile_ai_states state
			WHERE state.symbol = stockv2_stock_profiles.symbol
		)
		ORDER BY symbol
	`)
	if err != nil {
		return 0, wrapError(err, "list legacy stock profiles for AI state backfill")
	}
	profiles, err := scanRows(rows, scanStockProfile, "scan legacy stock profile", "iterate legacy stock profiles")
	if err != nil || len(profiles) == 0 {
		return 0, err
	}

	announcementBaselines := make(map[string]stockProfileAIAnnouncementBaseline)
	announcementRows, err := s.assetDB().QueryContext(ctx, `
		SELECT symbol, COUNT(*), MAX(created_at)
		FROM stockv2_announcements
		GROUP BY symbol
	`)
	if err != nil {
		return 0, wrapError(err, "load announcement baselines for AI state backfill")
	}
	for announcementRows.Next() {
		var symbol string
		var baseline stockProfileAIAnnouncementBaseline
		var latestCreated sql.NullTime
		if err := announcementRows.Scan(&symbol, &baseline.revision, &latestCreated); err != nil {
			announcementRows.Close()
			return 0, wrapError(err, "scan announcement baseline for AI state backfill")
		}
		if latestCreated.Valid {
			baseline.latestCreated = latestCreated.Time
		}
		announcementBaselines[symbol] = baseline
	}
	if err := announcementRows.Close(); err != nil {
		return 0, err
	}
	if err := announcementRows.Err(); err != nil {
		return 0, wrapError(err, "iterate announcement baselines for AI state backfill")
	}

	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return 0, wrapError(err, "begin stock profile AI state backfill")
	}
	defer tx.Rollback()
	now := time.Now()
	backfilled := 0
	for _, profile := range profiles {
		if _, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, profile.Symbol); err != nil {
			return 0, err
		} else if exists {
			continue
		}
		baseHash := strings.TrimSpace(profile.BaseProfileHash)
		if baseHash == "" {
			baseHash = stockProfileAIInputHash(profile)
			baseUpdatedAt := profile.BaseProfileUpdatedAt
			if baseUpdatedAt.IsZero() {
				baseUpdatedAt = profile.UpdatedAt
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE stockv2_stock_profiles
				SET base_profile_hash = ?,
					base_profile_updated_at = COALESCE(base_profile_updated_at, ?),
					base_profile_checked_at = COALESCE(base_profile_checked_at, ?)
				WHERE symbol = ?
			`, baseHash, baseUpdatedAt, baseUpdatedAt, profile.Symbol); err != nil {
				return 0, wrapError(err, "backfill stock profile base hash")
			}
			profile.BaseProfileHash = baseHash
			profile.BaseProfileUpdatedAt = baseUpdatedAt
		}
		baseline := announcementBaselines[profile.Symbol]
		dataSummaryVersion, err := stockProfileDataSummaryVersionWithQuerier(ctx, tx, profile.Symbol)
		if err != nil {
			return 0, err
		}
		desiredVersion := stockProfileDesiredInputVersion(
			profile.Symbol, baseHash, baseline.revision, dataSummaryVersion, 0,
		)
		state := StockProfileAIState{
			Symbol: profile.Symbol, ProfileSchemaVersion: stockProfileAIInputSchemaVersion,
			BaseProfileHash: baseHash, AnnouncementRevision: baseline.revision,
			DataSummaryVersion:  dataSummaryVersion,
			DesiredInputVersion: desiredVersion, DesiredAt: now, CreatedAt: now, UpdatedAt: now,
		}
		hasSummary := normalizeStockProfileAIStatus(profile.AIProfileStatus) == StockProfileAIStatusReady &&
			!profile.AIProfileUpdatedAt.IsZero() &&
			(strings.TrimSpace(profile.BusinessSummaryZh) != "" || strings.TrimSpace(profile.ProfileTextZh) != "")
		baseFresh := profile.BaseProfileUpdatedAt.IsZero() || !profile.AIProfileUpdatedAt.Before(profile.BaseProfileUpdatedAt)
		announcementFresh := baseline.latestCreated.IsZero() || !profile.AIProfileUpdatedAt.Before(baseline.latestCreated)
		if hasSummary {
			resultJSON, resultHash, err := legacyStockProfileAIResult(profile)
			if err != nil {
				return 0, err
			}
			inputVersion := "legacy:" + resultHash
			if baseFresh && announcementFresh {
				inputVersion = desiredVersion
			}
			manifestJSON, _ := json.Marshal(map[string]any{
				"schema": stockProfileAIInputSchemaVersion, "migratedLegacySummary": true,
				"baseProfileHash": baseHash, "announcementRevision": baseline.revision,
				"dataSummaryVersion": dataSummaryVersion,
			})
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stockv2_stock_profile_ai_versions (
					symbol, input_version, base_profile_hash, announcement_revision, data_summary_version,
					input_manifest_json, result_json, result_hash, model_name,
					confidence, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(symbol, input_version) DO NOTHING
			`, profile.Symbol, inputVersion, baseHash, baseline.revision, dataSummaryVersion, string(manifestJSON),
				resultJSON, resultHash, nullableString(profile.AIProfileModel), profile.AIProfileConfidence,
				profile.AIProfileUpdatedAt); err != nil {
				return 0, wrapError(err, "backfill immutable stock profile AI version")
			}
			state.AppliedInputVersion = inputVersion
			state.AppliedAt = profile.AIProfileUpdatedAt
		}
		switch {
		case !hasSummary:
			state.DesiredTriggerReason = AssetAIDecisionMissing
		case !baseFresh:
			state.DesiredTriggerReason = AssetAIDecisionBaseChanged
		case !announcementFresh:
			state.DesiredTriggerReason = AssetAIDecisionAnnouncement
		default:
			state.DesiredTriggerReason = AssetAIDecisionSkippedUnneeded
		}
		state.DesiredPriority = stockProfileAIQueuePriority(state.DesiredTriggerReason)
		if err := upsertStockProfileAIStateWithExecer(ctx, tx, state); err != nil {
			return 0, err
		}
		if baseline.revision > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE stockv2_announcements
				SET symbol_revision = ?
				WHERE symbol = ? AND COALESCE(symbol_revision, 0) = 0
			`, baseline.revision, profile.Symbol); err != nil {
				return 0, wrapError(err, "backfill announcement symbol revision")
			}
		}
		backfilled++
	}
	if err := tx.Commit(); err != nil {
		return 0, wrapError(err, "commit stock profile AI state backfill")
	}
	return backfilled, nil
}

func legacyStockProfileAIResult(profile StockProfile) (string, string, error) {
	result, err := json.Marshal(map[string]any{
		"summaryZh": profile.BusinessSummaryZh, "summaryEn": profile.BusinessSummaryEn,
		"aliasesZh": profile.AliasesZh, "aliasesEn": profile.AliasesEn,
		"keywordsZh": profile.KeywordsZh, "keywordsEn": profile.KeywordsEn,
		"businessLinesZh": profile.BusinessLinesZh, "businessLinesEn": profile.BusinessLinesEn,
		"riskTagsZh": profile.RiskTagsZh, "riskTagsEn": profile.RiskTagsEn,
	})
	if err != nil {
		return "", "", wrapError(err, "marshal legacy stock profile AI result")
	}
	sum := sha256.Sum256(result)
	return string(result), hex.EncodeToString(sum[:]), nil
}

func (s *Store) EnsureStockProfileAITarget(
	ctx context.Context,
	symbol, baseHash, dataSummaryVersion, triggerReason string,
	force bool,
	requiredMessageCutoffAt time.Time,
) (StockProfileAIState, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return StockProfileAIState{}, errors.New("stock profile AI target requires symbol")
	}
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return StockProfileAIState{}, wrapError(err, "begin stock profile AI target")
	}
	defer tx.Rollback()
	state, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, symbol)
	if err != nil {
		return StockProfileAIState{}, err
	}
	if strings.TrimSpace(baseHash) == "" {
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(base_profile_hash,'') FROM stockv2_stock_profiles WHERE symbol = ?
		`, symbol).Scan(&baseHash); err != nil {
			return StockProfileAIState{}, wrapError(err, "load stock profile AI base hash")
		}
	}
	if strings.TrimSpace(baseHash) == "" {
		return StockProfileAIState{}, errors.New("stock profile AI target requires a base profile hash")
	}
	now := time.Now()
	if !exists {
		state = StockProfileAIState{
			Symbol:               symbol,
			ProfileSchemaVersion: stockProfileAIInputSchemaVersion,
			BaseProfileHash:      baseHash,
			CreatedAt:            now,
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM stockv2_announcements WHERE symbol = ?
		`, symbol).Scan(&state.AnnouncementRevision); err != nil {
			return StockProfileAIState{}, wrapError(err, "initialize stock profile announcement revision")
		}
	}
	state.BaseProfileHash = baseHash
	state.DataSummaryVersion = strings.TrimSpace(dataSummaryVersion)
	state.ProfileSchemaVersion = stockProfileAIInputSchemaVersion
	if force {
		state.ManualGeneration++
	}
	state.RequiredMessageCutoffAt = maxTime(state.RequiredMessageCutoffAt, requiredMessageCutoffAt)
	state.DesiredTriggerReason = firstNonEmpty(triggerReason, AssetAIDecisionMissing)
	state.DesiredPriority = max(state.DesiredPriority, stockProfileAIQueuePriority(state.DesiredTriggerReason))
	state.DesiredInputVersion = stockProfileDesiredInputVersion(
		state.Symbol, state.BaseProfileHash, state.AnnouncementRevision,
		state.DataSummaryVersion, state.ManualGeneration,
	)
	state.DesiredAt = now
	state.UpdatedAt = now
	if state.CreatedAt.IsZero() {
		state.CreatedAt = now
	}
	if err := upsertStockProfileAIStateWithExecer(ctx, tx, state); err != nil {
		return StockProfileAIState{}, err
	}
	if err := tx.Commit(); err != nil {
		return StockProfileAIState{}, wrapError(err, "commit stock profile AI target")
	}
	return state, nil
}

func (s *Store) GetStockProfileAIState(ctx context.Context, symbol string) (StockProfileAIState, bool, error) {
	return getStockProfileAIStateWithQuerier(ctx, s.assetDB(), strings.TrimSpace(symbol))
}

// RefreshPendingStockProfileAIDataSummary advances data-dependent targets only
// when work is already outstanding. Ready profiles are deliberately excluded:
// a normal new trading day alone must not enqueue a full-market AI rebuild.
func (s *Store) RefreshPendingStockProfileAIDataSummary(
	ctx context.Context,
	symbol string,
) (StockProfileAIState, bool, error) {
	symbol = strings.TrimSpace(symbol)
	dataSummaryVersion, err := stockProfileDataSummaryVersionWithQuerier(ctx, s.assetDB(), symbol)
	if err != nil {
		return StockProfileAIState{}, false, err
	}
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return StockProfileAIState{}, false, wrapError(err, "begin refresh stock profile AI data summary")
	}
	defer tx.Rollback()
	state, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, symbol)
	if err != nil || !exists {
		return state, false, err
	}
	if state.DesiredInputVersion == state.AppliedInputVersion {
		return state, false, nil
	}
	if state.ProfileSchemaVersion == stockProfileAIInputSchemaVersion &&
		state.DataSummaryVersion == dataSummaryVersion {
		return state, false, nil
	}
	now := time.Now()
	state.ProfileSchemaVersion = stockProfileAIInputSchemaVersion
	state.DataSummaryVersion = dataSummaryVersion
	state.DesiredInputVersion = stockProfileDesiredInputVersion(
		state.Symbol, state.BaseProfileHash, state.AnnouncementRevision,
		state.DataSummaryVersion, state.ManualGeneration,
	)
	state.DesiredAt = now
	state.UpdatedAt = now
	if err := upsertStockProfileAIStateWithExecer(ctx, tx, state); err != nil {
		return StockProfileAIState{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return StockProfileAIState{}, false, wrapError(err, "commit stock profile AI data summary")
	}
	return state, true, nil
}

func (s *Store) RefreshPendingStockProfileAIDataSummaries(
	ctx context.Context,
	targets []StockProfileAIState,
) ([]StockProfileAIState, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	symbols := make([]string, 0, len(targets))
	for _, target := range targets {
		symbols = append(symbols, target.Symbol)
	}
	versions, err := stockProfileDataSummaryVersionsWithQuerier(ctx, s.assetDB(), symbols)
	if err != nil {
		return nil, err
	}
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin refresh stock profile AI data summaries")
	}
	defer tx.Rollback()
	now := time.Now()
	out := make([]StockProfileAIState, 0, len(targets))
	for _, target := range targets {
		state, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, target.Symbol)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		dataSummaryVersion := versions[state.Symbol]
		if state.DesiredInputVersion != state.AppliedInputVersion &&
			(state.ProfileSchemaVersion != stockProfileAIInputSchemaVersion ||
				state.DataSummaryVersion != dataSummaryVersion) {
			state.ProfileSchemaVersion = stockProfileAIInputSchemaVersion
			state.DataSummaryVersion = dataSummaryVersion
			state.DesiredInputVersion = stockProfileDesiredInputVersion(
				state.Symbol, state.BaseProfileHash, state.AnnouncementRevision,
				state.DataSummaryVersion, state.ManualGeneration,
			)
			state.DesiredAt = now
			state.UpdatedAt = now
			if err := upsertStockProfileAIStateWithExecer(ctx, tx, state); err != nil {
				return nil, err
			}
		}
		out = append(out, state)
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapError(err, "commit stock profile AI data summaries")
	}
	return out, nil
}

func (s *Store) ListStockProfileAIStatesBySymbols(
	ctx context.Context,
	symbols []string,
) (map[string]StockProfileAIState, error) {
	symbols = compactStringList(symbols, 500)
	out := make(map[string]StockProfileAIState, len(symbols))
	if len(symbols) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := s.assetDB().QueryContext(ctx, stockProfileAIStateSelect+`
		WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list stock profile AI states")
	}
	defer rows.Close()
	for rows.Next() {
		state, err := scanStockProfileAIState(rows)
		if err != nil {
			return nil, wrapError(err, "scan stock profile AI state")
		}
		out[state.Symbol] = state
	}
	return out, wrapError(rows.Err(), "iterate stock profile AI states")
}

func (s *Store) ListPendingStockProfileAITargetsAfter(
	ctx context.Context,
	cursorSymbol string,
	limit int,
) ([]StockProfileAIState, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	cursorSymbol = strings.TrimSpace(cursorSymbol)
	rows, err := s.assetDB().QueryContext(ctx, stockProfileAIStateSelect+`
		WHERE COALESCE(desired_input_version,'') <> ''
		  AND COALESCE(desired_input_version,'') <> COALESCE(applied_input_version,'')
		ORDER BY CASE WHEN ? = '' OR symbol > ? THEN 0 ELSE 1 END, symbol
		LIMIT ?
	`, cursorSymbol, cursorSymbol, limit)
	if err != nil {
		return nil, wrapError(err, "list pending stock profile AI targets")
	}
	return scanRows(rows, scanStockProfileAIState, "scan pending stock profile AI target", "iterate pending stock profile AI targets")
}

func (s *Store) GetStockProfileAIVersion(
	ctx context.Context,
	symbol, inputVersion string,
) (StockProfileAIVersion, bool, error) {
	var version StockProfileAIVersion
	var announcementCutoffAt sql.NullTime
	err := s.assetDB().QueryRowContext(ctx, `
		SELECT symbol, input_version, base_profile_hash, announcement_revision,
		       COALESCE(data_summary_version,''),
		       announcement_cutoff_at, COALESCE(previous_input_version,''),
		       input_manifest_json, result_json, result_hash, COALESCE(model_name,''),
		       confidence, COALESCE(agent_run_id,''), created_at
		FROM stockv2_stock_profile_ai_versions
		WHERE symbol = ? AND input_version = ?
	`, strings.TrimSpace(symbol), strings.TrimSpace(inputVersion)).Scan(
		&version.Symbol, &version.InputVersion, &version.BaseProfileHash,
		&version.AnnouncementRevision, &version.DataSummaryVersion,
		&announcementCutoffAt, &version.PreviousInputVersion,
		&version.InputManifestJSON, &version.ResultJSON, &version.ResultHash, &version.ModelName,
		&version.Confidence, &version.AgentRunID, &version.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return StockProfileAIVersion{}, false, nil
	}
	if err != nil {
		return StockProfileAIVersion{}, false, wrapError(err, "get stock profile AI version")
	}
	if announcementCutoffAt.Valid {
		version.AnnouncementCutoffAt = announcementCutoffAt.Time
	}
	return version, true, nil
}

func (s *Store) StockProfileAITargetCurrent(ctx context.Context, symbol, inputVersion string) (bool, error) {
	var current bool
	err := s.assetDB().QueryRowContext(ctx, `
		SELECT COALESCE(desired_input_version,'') = ?
		FROM stockv2_stock_profile_ai_states WHERE symbol = ?
	`, strings.TrimSpace(inputVersion), strings.TrimSpace(symbol)).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return current, wrapError(err, "check stock profile AI target")
}

func (s *Store) ApplyStockProfileAIResult(
	ctx context.Context,
	lease StockProfileAIQueueLease,
	profile StockProfile,
	inputManifestJSON string,
) (StockProfileAIVersion, error) {
	if len(lease.ResultJSON) == 0 || len(lease.ResultJSON) > stockProfileAIQueueResultMaxBytes {
		return StockProfileAIVersion{}, errors.New("stock profile AI staged result is invalid")
	}
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return StockProfileAIVersion{}, wrapError(err, "begin apply stock profile AI result")
	}
	defer tx.Rollback()
	state, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, lease.Symbol)
	if err != nil {
		return StockProfileAIVersion{}, err
	}
	if !exists || state.DesiredInputVersion != lease.ClaimedInputVersion {
		return StockProfileAIVersion{}, ErrStockProfileAIQueueLeaseStale
	}
	dataSummaryVersion, err := stockProfileDataSummaryVersionWithQuerier(ctx, tx, lease.Symbol)
	if err != nil {
		return StockProfileAIVersion{}, err
	}
	if state.ProfileSchemaVersion != stockProfileAIInputSchemaVersion ||
		state.DataSummaryVersion != dataSummaryVersion {
		now := time.Now()
		state.ProfileSchemaVersion = stockProfileAIInputSchemaVersion
		state.DataSummaryVersion = dataSummaryVersion
		state.DesiredInputVersion = stockProfileDesiredInputVersion(
			state.Symbol, state.BaseProfileHash, state.AnnouncementRevision,
			state.DataSummaryVersion, state.ManualGeneration,
		)
		state.DesiredAt = now
		state.UpdatedAt = now
		if err := upsertStockProfileAIStateWithExecer(ctx, tx, state); err != nil {
			return StockProfileAIVersion{}, err
		}
		if err := tx.Commit(); err != nil {
			return StockProfileAIVersion{}, wrapError(err, "commit superseding stock profile AI data summary")
		}
		return StockProfileAIVersion{}, ErrStockProfileAIQueueLeaseStale
	}
	if err := upsertStockProfileWithExecer(ctx, tx, profile); err != nil {
		return StockProfileAIVersion{}, err
	}
	now := time.Now()
	version := StockProfileAIVersion{
		Symbol:               lease.Symbol,
		InputVersion:         lease.ClaimedInputVersion,
		BaseProfileHash:      state.BaseProfileHash,
		AnnouncementRevision: state.AnnouncementRevision,
		DataSummaryVersion:   state.DataSummaryVersion,
		AnnouncementCutoffAt: state.RequiredMessageCutoffAt,
		PreviousInputVersion: state.AppliedInputVersion,
		InputManifestJSON:    firstNonEmpty(strings.TrimSpace(inputManifestJSON), "{}"),
		ResultJSON:           lease.ResultJSON,
		ResultHash:           lease.ResultHash,
		ModelName:            lease.ResultModel,
		Confidence:           lease.ResultConfidence,
		AgentRunID:           lease.CurrentAgentRunID,
		CreatedAt:            now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profile_ai_versions (
			symbol, input_version, base_profile_hash, announcement_revision, data_summary_version,
			announcement_cutoff_at, previous_input_version, input_manifest_json,
			result_json, result_hash, model_name, confidence, agent_run_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, input_version) DO NOTHING
	`, version.Symbol, version.InputVersion, version.BaseProfileHash, version.AnnouncementRevision,
		version.DataSummaryVersion, nullableTime(version.AnnouncementCutoffAt), nullableString(version.PreviousInputVersion),
		version.InputManifestJSON, version.ResultJSON, version.ResultHash, nullableString(version.ModelName),
		version.Confidence, nullableString(version.AgentRunID), version.CreatedAt); err != nil {
		return StockProfileAIVersion{}, wrapError(err, "insert stock profile AI version")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_states
		SET applied_input_version = ?, applied_at = ?, updated_at = ?
		WHERE symbol = ? AND desired_input_version = ?
	`, version.InputVersion, now, now, version.Symbol, version.InputVersion)
	if err != nil {
		return StockProfileAIVersion{}, wrapError(err, "advance stock profile AI applied version")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return StockProfileAIVersion{}, err
		}
		return StockProfileAIVersion{}, ErrStockProfileAIQueueLeaseStale
	}
	if err := tx.Commit(); err != nil {
		return StockProfileAIVersion{}, wrapError(err, "commit stock profile AI result")
	}
	return version, nil
}

type stockProfileAIStateQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type stockProfileAIStateExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const stockProfileAIStateSelect = `
	SELECT symbol, profile_schema_version, base_profile_hash, announcement_revision,
	       COALESCE(data_summary_version,''), manual_generation, required_message_cutoff_at,
	       COALESCE(desired_input_version,''), COALESCE(desired_trigger_reason,''),
	       desired_priority, desired_at, COALESCE(applied_input_version,''), applied_at,
	       created_at, updated_at
	FROM stockv2_stock_profile_ai_states `

func getStockProfileAIStateWithQuerier(
	ctx context.Context,
	q stockProfileAIStateQuerier,
	symbol string,
) (StockProfileAIState, bool, error) {
	state, err := scanStockProfileAIState(q.QueryRowContext(ctx, stockProfileAIStateSelect+` WHERE symbol = ?`, symbol))
	if errors.Is(err, sql.ErrNoRows) {
		return StockProfileAIState{}, false, nil
	}
	if err != nil {
		return StockProfileAIState{}, false, wrapError(err, "get stock profile AI state")
	}
	return state, true, nil
}

func upsertStockProfileAIStateWithExecer(ctx context.Context, exec stockProfileAIStateExecer, state StockProfileAIState) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profile_ai_states (
			symbol, profile_schema_version, base_profile_hash, announcement_revision,
			data_summary_version, manual_generation, required_message_cutoff_at, desired_input_version,
			desired_trigger_reason, desired_priority, desired_at,
			applied_input_version, applied_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			profile_schema_version = excluded.profile_schema_version,
			base_profile_hash = excluded.base_profile_hash,
			announcement_revision = excluded.announcement_revision,
			data_summary_version = excluded.data_summary_version,
			manual_generation = excluded.manual_generation,
			required_message_cutoff_at = excluded.required_message_cutoff_at,
			desired_input_version = excluded.desired_input_version,
			desired_trigger_reason = excluded.desired_trigger_reason,
			desired_priority = excluded.desired_priority,
			desired_at = excluded.desired_at,
			applied_input_version = excluded.applied_input_version,
			applied_at = excluded.applied_at,
			updated_at = excluded.updated_at
	`, state.Symbol, state.ProfileSchemaVersion, state.BaseProfileHash, state.AnnouncementRevision,
		state.DataSummaryVersion, state.ManualGeneration, nullableTime(state.RequiredMessageCutoffAt), nullableString(state.DesiredInputVersion),
		nullableString(state.DesiredTriggerReason), state.DesiredPriority, nullableTime(state.DesiredAt),
		nullableString(state.AppliedInputVersion), nullableTime(state.AppliedAt), state.CreatedAt, state.UpdatedAt)
	return wrapError(err, "upsert stock profile AI state")
}

func syncStockProfileAIBaseWithTx(ctx context.Context, tx *sql.Tx, symbol, baseHash string, now time.Time) error {
	baseHash = strings.TrimSpace(baseHash)
	if baseHash == "" {
		return nil
	}
	state, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, symbol)
	if err != nil {
		return err
	}
	if exists && state.BaseProfileHash == baseHash &&
		(state.ProfileSchemaVersion == stockProfileAIInputSchemaVersion ||
			(state.DesiredInputVersion != "" && state.DesiredInputVersion == state.AppliedInputVersion)) {
		return nil
	}
	if !exists {
		state = StockProfileAIState{
			Symbol:               symbol,
			ProfileSchemaVersion: stockProfileAIInputSchemaVersion,
			CreatedAt:            now,
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_announcements WHERE symbol = ?`, symbol).
			Scan(&state.AnnouncementRevision); err != nil {
			return wrapError(err, "initialize stock profile AI announcement revision")
		}
	}
	state.BaseProfileHash = baseHash
	state.ProfileSchemaVersion = stockProfileAIInputSchemaVersion
	dataSummaryVersion, err := stockProfileDataSummaryVersionWithQuerier(ctx, tx, symbol)
	if err != nil {
		return err
	}
	state.DataSummaryVersion = dataSummaryVersion
	state.DesiredTriggerReason = AssetAIDecisionBaseChanged
	if strings.TrimSpace(state.AppliedInputVersion) == "" {
		state.DesiredTriggerReason = AssetAIDecisionMissing
	}
	state.DesiredPriority = max(state.DesiredPriority, stockProfileAIQueuePriority(state.DesiredTriggerReason))
	state.DesiredInputVersion = stockProfileDesiredInputVersion(
		state.Symbol, state.BaseProfileHash, state.AnnouncementRevision,
		state.DataSummaryVersion, state.ManualGeneration,
	)
	state.DesiredAt = now
	state.UpdatedAt = now
	return upsertStockProfileAIStateWithExecer(ctx, tx, state)
}

func bumpStockProfileAIAnnouncementRevisionsWithTx(
	ctx context.Context,
	tx *sql.Tx,
	symbols []string,
	now time.Time,
) (map[string]int64, error) {
	symbols = canonicalUniverseSymbols(symbols)
	revisions := make(map[string]int64, len(symbols))
	for _, symbol := range symbols {
		state, exists, err := getStockProfileAIStateWithQuerier(ctx, tx, symbol)
		if err != nil {
			return nil, err
		}
		if !exists {
			var baseHash string
			if err := tx.QueryRowContext(ctx, `
				SELECT COALESCE(base_profile_hash,'') FROM stockv2_stock_profiles WHERE symbol = ?
			`, symbol).Scan(&baseHash); errors.Is(err, sql.ErrNoRows) {
				continue
			} else if err != nil {
				return nil, wrapError(err, "load profile for announcement AI revision")
			}
			if strings.TrimSpace(baseHash) == "" {
				continue
			}
			state = StockProfileAIState{
				Symbol:               symbol,
				ProfileSchemaVersion: stockProfileAIInputSchemaVersion,
				BaseProfileHash:      baseHash,
				CreatedAt:            now,
			}
		}
		state.AnnouncementRevision++
		state.ProfileSchemaVersion = stockProfileAIInputSchemaVersion
		dataSummaryVersion, err := stockProfileDataSummaryVersionWithQuerier(ctx, tx, symbol)
		if err != nil {
			return nil, err
		}
		state.DataSummaryVersion = dataSummaryVersion
		state.DesiredTriggerReason = AssetAIDecisionAnnouncement
		state.DesiredPriority = max(state.DesiredPriority, stockProfileAIQueuePriority(AssetAIDecisionAnnouncement))
		state.DesiredInputVersion = stockProfileDesiredInputVersion(
			state.Symbol, state.BaseProfileHash, state.AnnouncementRevision,
			state.DataSummaryVersion, state.ManualGeneration,
		)
		state.DesiredAt = now
		state.UpdatedAt = now
		if err := upsertStockProfileAIStateWithExecer(ctx, tx, state); err != nil {
			return nil, err
		}
		revisions[symbol] = state.AnnouncementRevision
	}
	return revisions, nil
}

func scanStockProfileAIState(scanner rowScanner) (StockProfileAIState, error) {
	var state StockProfileAIState
	var cutoff, desiredAt, appliedAt sql.NullTime
	err := scanner.Scan(
		&state.Symbol, &state.ProfileSchemaVersion, &state.BaseProfileHash,
		&state.AnnouncementRevision, &state.DataSummaryVersion, &state.ManualGeneration, &cutoff,
		&state.DesiredInputVersion, &state.DesiredTriggerReason, &state.DesiredPriority,
		&desiredAt, &state.AppliedInputVersion, &appliedAt, &state.CreatedAt, &state.UpdatedAt,
	)
	if cutoff.Valid {
		state.RequiredMessageCutoffAt = cutoff.Time
	}
	if desiredAt.Valid {
		state.DesiredAt = desiredAt.Time
	}
	if appliedAt.Valid {
		state.AppliedAt = appliedAt.Time
	}
	return state, err
}

func maxTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}
