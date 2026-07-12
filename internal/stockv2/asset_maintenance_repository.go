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

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) UpsertAssetMaintenanceItem(ctx context.Context, item AssetMaintenanceItem) (AssetMaintenanceItem, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.Status == "" {
		item.Status = AssetMaintenanceItemStatusRunning
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	statusesJSON, _ := json.Marshal(item.SourceStatuses)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_asset_maintenance_items (
			id, job_id, symbol, market, instrument_type, name, status,
			priority_reason, attempt_count, next_retry_at, checked_at, expected_latest_trade_date,
			daily_bar_status, daily_bar_gap_count, daily_bar_missing_facet_count, daily_flow_status,
			daily_bar_fetched, daily_bar_start, daily_bar_end,
			base_profile_status, base_profile_changed, base_profile_hash_before, base_profile_hash_after,
			announcement_status, announcements_new, major_announcements_new,
			ai_decision, ai_profile_status, ai_queue_status, ai_desired_input_version,
			agent_run_id, error_message, source_statuses_json,
			duration_ms, started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			job_id = excluded.job_id,
			symbol = excluded.symbol,
			market = excluded.market,
			instrument_type = excluded.instrument_type,
			name = excluded.name,
			status = excluded.status,
			priority_reason = COALESCE(NULLIF(excluded.priority_reason, ''), stockv2_asset_maintenance_items.priority_reason),
			attempt_count = MAX(stockv2_asset_maintenance_items.attempt_count, excluded.attempt_count),
			next_retry_at = excluded.next_retry_at,
			checked_at = excluded.checked_at,
			expected_latest_trade_date = COALESCE(NULLIF(excluded.expected_latest_trade_date, ''), stockv2_asset_maintenance_items.expected_latest_trade_date),
			daily_bar_status = excluded.daily_bar_status,
			daily_bar_gap_count = excluded.daily_bar_gap_count,
			daily_bar_missing_facet_count = excluded.daily_bar_missing_facet_count,
			daily_flow_status = excluded.daily_flow_status,
			daily_bar_fetched = excluded.daily_bar_fetched,
			daily_bar_start = excluded.daily_bar_start,
			daily_bar_end = excluded.daily_bar_end,
			base_profile_status = excluded.base_profile_status,
			base_profile_changed = excluded.base_profile_changed,
			base_profile_hash_before = excluded.base_profile_hash_before,
			base_profile_hash_after = excluded.base_profile_hash_after,
			announcement_status = excluded.announcement_status,
			announcements_new = excluded.announcements_new,
			major_announcements_new = excluded.major_announcements_new,
			ai_decision = excluded.ai_decision,
			ai_profile_status = excluded.ai_profile_status,
			ai_queue_status = excluded.ai_queue_status,
			ai_desired_input_version = excluded.ai_desired_input_version,
			agent_run_id = excluded.agent_run_id,
			error_message = excluded.error_message,
			source_statuses_json = excluded.source_statuses_json,
			duration_ms = excluded.duration_ms,
			finished_at = excluded.finished_at,
			updated_at = excluded.updated_at
	`, item.ID, item.JobID, item.Symbol, item.Market, item.InstrumentType, item.Name, item.Status,
		item.PriorityReason, item.AttemptCount, nullableTime(item.NextRetryAt), nullableTime(item.CheckedAt), item.ExpectedLatestDate,
		item.DailyBarStatus, item.DailyBarGapCount, item.DailyBarMissingFacets, item.DailyFlowStatus,
		item.DailyBarFetched, item.DailyBarStart, item.DailyBarEnd,
		item.BaseProfileStatus, boolInt(item.BaseProfileChanged), item.BaseProfileHashBefore, item.BaseProfileHashAfter,
		item.AnnouncementStatus, item.AnnouncementsNew, item.MajorAnnouncementsNew,
		item.AIDecision, item.AIProfileStatus, item.AIQueueStatus, item.AIDesiredInputVersion,
		item.AgentRunID, safelog.Text(item.ErrorMessage, 800), string(statusesJSON),
		item.DurationMs, item.StartedAt, nullableTime(item.FinishedAt), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return AssetMaintenanceItem{}, wrapError(err, "upsert asset maintenance item")
	}
	return item, nil
}

func (s *Store) UpdateAssetMaintenanceItemByAgentRun(ctx context.Context, agentRunID, status, aiStatus, errorMessage string, finishedAt time.Time) error {
	if strings.TrimSpace(agentRunID) == "" {
		return nil
	}
	sets := []string{"updated_at = ?"}
	args := []any{time.Now()}
	// The item status is the deterministic data-maintenance result. AI progress is
	// orthogonal and must not turn an already completed base pipeline back into a
	// generic partial/failed state.
	_ = status
	_ = errorMessage
	_ = finishedAt
	if aiStatus != "" {
		sets = append(sets, "ai_profile_status = ?")
		args = append(args, aiStatus)
		queueStatus := StockProfileAIQueueStatusRunning
		switch aiStatus {
		case StockProfileAIStatusReady:
			queueStatus = StockProfileAIQueueStatusCompleted
		case StockProfileAIStatusFailed:
			queueStatus = StockProfileAIQueueStatusFailed
		case StockProfileAIStatusQueued:
			queueStatus = StockProfileAIQueueStatusReady
		}
		sets = append(sets, "ai_queue_status = ?")
		args = append(args, queueStatus)
	}
	args = append(args, agentRunID)
	_, err := s.db.ExecContext(ctx, `UPDATE stockv2_asset_maintenance_items SET `+strings.Join(sets, ", ")+` WHERE agent_run_id = ?`, args...)
	return wrapError(err, "update asset maintenance item by agent run")
}

func (s *Store) SyncAssetMaintenanceItemAIQueue(ctx context.Context, itemID, symbol string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET ai_queue_status = (
				SELECT status FROM stockv2_stock_profile_ai_queue WHERE symbol = ?
			),
			ai_profile_status = CASE (
				SELECT status FROM stockv2_stock_profile_ai_queue WHERE symbol = ?
			)
				WHEN 'running' THEN 'running'
				WHEN 'completed' THEN 'ready'
				WHEN 'failed' THEN 'failed'
				ELSE 'queued'
			END,
			agent_run_id = (
				SELECT current_agent_run_id FROM stockv2_stock_profile_ai_queue WHERE symbol = ?
			),
			ai_desired_input_version = (
				SELECT desired_input_version FROM stockv2_stock_profile_ai_queue WHERE symbol = ?
			),
			updated_at = ?
		WHERE id = ?
		  AND EXISTS (SELECT 1 FROM stockv2_stock_profile_ai_queue WHERE symbol = ?)
	`, symbol, symbol, symbol, symbol, time.Now(), itemID, symbol)
	return wrapError(err, "sync asset maintenance item ai queue")
}

// SyncAssetMaintenanceItemsAIQueueByVersion repairs maintenance rows whose
// DuckDB outbox target committed but whose first SQLite enqueue failed.
func (s *Store) SyncAssetMaintenanceItemsAIQueueByVersion(
	ctx context.Context,
	symbol, desiredInputVersion string,
) error {
	queue, err := s.GetStockProfileAIQueueItem(ctx, symbol)
	if err != nil {
		return wrapError(err, "load reconciled stock profile AI queue item")
	}
	aiStatus, _ := stockProfileAIQueueState(queue)
	_, err = s.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET ai_decision = ?, ai_profile_status = ?, ai_queue_status = ?,
			agent_run_id = ?, updated_at = ?
		WHERE symbol = ? AND ai_desired_input_version = ? AND ai_decision = 'failed'
	`, queue.TriggerReason, aiStatus, queue.Status, nullableString(queue.CurrentAgentRunID),
		time.Now(), strings.TrimSpace(symbol), strings.TrimSpace(desiredInputVersion))
	return wrapError(err, "repair asset maintenance AI outbox state")
}

func (s *Store) ListAssetMaintenanceItems(ctx context.Context, filter AssetMaintenanceItemListFilter) ([]AssetMaintenanceItem, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if jobID := strings.TrimSpace(filter.JobID); jobID != "" {
		where = append(where, "job_id = ?")
		args = append(args, jobID)
	}
	if symbol := strings.TrimSpace(filter.Symbol); symbol != "" {
		where = append(where, "symbol = ?")
		args = append(args, symbol)
	}
	query := assetMaintenanceItemSelectSQL()
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "list asset maintenance items")
	}
	return scanRows(rows, scanAssetMaintenanceItem, "scan asset maintenance item", "iterate asset maintenance items")
}

func (s *Store) CountAssetMaintenanceItems(ctx context.Context, filter AssetMaintenanceItemListFilter) (int, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if jobID := strings.TrimSpace(filter.JobID); jobID != "" {
		where = append(where, "job_id = ?")
		args = append(args, jobID)
	}
	if symbol := strings.TrimSpace(filter.Symbol); symbol != "" {
		where = append(where, "symbol = ?")
		args = append(args, symbol)
	}
	query := "SELECT COUNT(*) FROM stockv2_asset_maintenance_items"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&total)
	return total, wrapError(err, "count asset maintenance items")
}

func (s *Store) LatestAssetMaintenanceItems(ctx context.Context, symbols []string) (map[string]AssetMaintenanceItem, error) {
	symbols = compactStringList(symbols, 200)
	if len(symbols) == 0 {
		return map[string]AssetMaintenanceItem{}, nil
	}
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := s.db.QueryContext(ctx, assetMaintenanceItemSelectSQL()+`
		WHERE id IN (
			SELECT id FROM (
				SELECT id, symbol, ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY created_at DESC) AS rn
				FROM stockv2_asset_maintenance_items
				WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
			) ranked WHERE rn = 1
		)
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list latest asset maintenance items")
	}
	items, err := scanRows(rows, scanAssetMaintenanceItem, "scan latest asset maintenance item", "iterate latest asset maintenance items")
	if err != nil {
		return nil, err
	}
	out := make(map[string]AssetMaintenanceItem, len(items))
	for _, item := range items {
		out[item.Symbol] = item
	}
	return out, nil
}

func assetMaintenanceItemSelectSQL() string {
	return `
		SELECT id, COALESCE(job_id,''), symbol, COALESCE(market,''), COALESCE(instrument_type,''),
		       COALESCE(name,''), status, COALESCE(priority_reason,''), COALESCE(attempt_count,0),
		       next_retry_at, checked_at, COALESCE(expected_latest_trade_date,''),
		       COALESCE(daily_bar_status,''), COALESCE(daily_bar_gap_count,0),
		       COALESCE(daily_bar_missing_facet_count,0), COALESCE(daily_flow_status,''), COALESCE(daily_bar_fetched,0),
		       COALESCE(daily_bar_start,''), COALESCE(daily_bar_end,''), COALESCE(base_profile_status,''),
		       COALESCE(base_profile_changed,0), COALESCE(base_profile_hash_before,''), COALESCE(base_profile_hash_after,''),
		       COALESCE(announcement_status,''), COALESCE(announcements_new,0), COALESCE(major_announcements_new,0),
		       COALESCE(ai_decision,''), COALESCE(ai_profile_status,''), COALESCE(ai_queue_status,''),
		       COALESCE(ai_desired_input_version,''), COALESCE(agent_run_id,''),
		       COALESCE(error_message,''), COALESCE(source_statuses_json,'[]'), COALESCE(duration_ms,0),
		       started_at, finished_at, created_at, updated_at
		FROM stockv2_asset_maintenance_items`
}

func scanAssetMaintenanceItem(row rowScanner) (AssetMaintenanceItem, error) {
	var item AssetMaintenanceItem
	var changed int
	var statusesJSON string
	var nextRetryAt, checkedAt, finishedAt sql.NullTime
	if err := row.Scan(
		&item.ID, &item.JobID, &item.Symbol, &item.Market, &item.InstrumentType,
		&item.Name, &item.Status, &item.PriorityReason, &item.AttemptCount,
		&nextRetryAt, &checkedAt, &item.ExpectedLatestDate,
		&item.DailyBarStatus, &item.DailyBarGapCount, &item.DailyBarMissingFacets,
		&item.DailyFlowStatus, &item.DailyBarFetched,
		&item.DailyBarStart, &item.DailyBarEnd, &item.BaseProfileStatus,
		&changed, &item.BaseProfileHashBefore, &item.BaseProfileHashAfter,
		&item.AnnouncementStatus, &item.AnnouncementsNew, &item.MajorAnnouncementsNew,
		&item.AIDecision, &item.AIProfileStatus, &item.AIQueueStatus, &item.AIDesiredInputVersion, &item.AgentRunID,
		&item.ErrorMessage, &statusesJSON, &item.DurationMs,
		&item.StartedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return AssetMaintenanceItem{}, err
	}
	item.BaseProfileChanged = changed != 0
	if nextRetryAt.Valid {
		item.NextRetryAt = nextRetryAt.Time
	}
	if checkedAt.Valid {
		item.CheckedAt = checkedAt.Time
	}
	if finishedAt.Valid {
		item.FinishedAt = finishedAt.Time
	}
	_ = json.Unmarshal([]byte(statusesJSON), &item.SourceStatuses)
	return item, nil
}

// GetAssetMaintenanceAIProgressByJobIDs aggregates queue state for the jobs
// already included in the StockV2 snapshot. It is intentionally one grouped DB
// query so UI polling never fans out into per-job or per-symbol requests.
func (s *Store) GetAssetMaintenanceAIProgressByJobIDs(ctx context.Context, jobIDs []string) (map[string]AssetMaintenanceAIProgress, error) {
	jobIDs = compactStringList(jobIDs, 100)
	out := make(map[string]AssetMaintenanceAIProgress, len(jobIDs))
	if len(jobIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		args = append(args, jobID)
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH classified AS (
			SELECT i.job_id,
			       COALESCE(i.ai_decision, '') AS ai_decision,
			       CASE
			         WHEN i.ai_queue_status IN ('running', 'apply_pending', 'applying') THEN 'running'
			         WHEN COALESCE(i.ai_queue_status, '') <> '' THEN i.ai_queue_status
			         WHEN r.status = 'ready' THEN 'ready'
			         WHEN r.status = 'running' THEN 'running'
			         WHEN r.status = 'completed' THEN 'completed'
			         WHEN r.status IN ('failed', 'superseded') THEN 'failed'
			         WHEN i.ai_profile_status = 'queued' THEN 'ready'
			         WHEN i.ai_profile_status = 'running' THEN 'running'
			         WHEN i.ai_profile_status = 'ready' THEN 'completed'
			         WHEN i.ai_profile_status = 'failed' THEN 'failed'
			         ELSE ''
			       END AS effective_state
			FROM stockv2_asset_maintenance_items i
			LEFT JOIN stockv2_agent_runs r ON r.id = i.agent_run_id
			WHERE i.job_id IN (`+sqlPlaceholders(len(jobIDs))+`)
		), scored AS (
			SELECT job_id, ai_decision, effective_state,
			       CASE WHEN ai_decision IN (
			         'called_missing', 'called_base_changed', 'called_announcement',
			         'called_retry', 'called_manual_force', 'failed'
			       ) OR (ai_decision NOT LIKE 'skipped_%' AND effective_state <> '') THEN 1 ELSE 0 END AS requested
			FROM classified
		)
		SELECT job_id,
		       SUM(requested) AS requested,
		       SUM(CASE WHEN requested = 1 AND effective_state = '' THEN 1 ELSE 0 END) AS pending,
		       SUM(CASE WHEN requested = 1 AND effective_state = 'ready' THEN 1 ELSE 0 END) AS queued,
		       SUM(CASE WHEN requested = 1 AND effective_state = 'running' THEN 1 ELSE 0 END) AS running,
		       SUM(CASE WHEN requested = 1 AND effective_state = 'retry_wait' THEN 1 ELSE 0 END) AS retrying,
		       SUM(CASE WHEN requested = 1 AND effective_state = 'completed' THEN 1 ELSE 0 END) AS completed,
		       SUM(CASE WHEN requested = 1 AND (effective_state = 'failed' OR (effective_state = '' AND ai_decision = 'failed')) THEN 1 ELSE 0 END) AS failed,
		       SUM(CASE WHEN ai_decision LIKE 'skipped_%' THEN 1 ELSE 0 END) AS skipped
		FROM scored
		GROUP BY job_id
	`, args...)
	if err != nil {
		return nil, wrapError(err, "aggregate asset maintenance ai progress")
	}
	defer rows.Close()
	for rows.Next() {
		var jobID string
		var progress AssetMaintenanceAIProgress
		if err := rows.Scan(
			&jobID, &progress.Requested, &progress.Pending, &progress.Queued,
			&progress.Running, &progress.Retrying, &progress.Completed,
			&progress.Failed, &progress.Skipped,
		); err != nil {
			return nil, wrapError(err, "scan asset maintenance ai progress")
		}
		progress.Outstanding = progress.Pending + progress.Queued + progress.Running + progress.Retrying
		progress.Status = assetMaintenanceAIProgressStatus(progress)
		out[jobID] = progress
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate asset maintenance ai progress")
	}
	return out, nil
}

func assetMaintenanceAIProgressStatus(progress AssetMaintenanceAIProgress) string {
	if progress.Requested == 0 {
		return AssetAIProgressStatusNotRequired
	}
	if progress.Outstanding > 0 {
		return AssetAIProgressStatusActive
	}
	if progress.Failed > 0 {
		return AssetAIProgressStatusCompletedWithFailures
	}
	return AssetAIProgressStatusCompleted
}

func (s *Store) UpsertAnnouncements(ctx context.Context, items []StockV2Announcement) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	db := s.assetDB()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, wrapError(err, "begin announcements tx")
	}
	defer func() { _ = tx.Rollback() }()
	result, err := upsertAnnouncementsWithTx(ctx, tx, items, time.Now())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, wrapError(err, "commit announcements")
	}
	return len(result.Inserted), nil
}

type announcementUpsertResult struct {
	Inserted []StockV2Announcement
}

func upsertAnnouncementsWithTx(
	ctx context.Context,
	tx *sql.Tx,
	items []StockV2Announcement,
	now time.Time,
) (announcementUpsertResult, error) {
	result := announcementUpsertResult{Inserted: make([]StockV2Announcement, 0, len(items))}
	affectedSymbols := make(map[string]struct{})
	affectedKeys := make(map[string][]string)
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		item := items[i]
		item.Source = firstNonEmpty(strings.TrimSpace(item.Source), StockV2AnnouncementSourceCninfo)
		item.Symbol = stockCodeOnly(item.Symbol)
		item.Market = strings.ToUpper(strings.TrimSpace(item.Market))
		item.ContentHash = strings.TrimSpace(item.ContentHash)
		item.Title = strings.TrimSpace(item.Title)
		if item.Symbol == "" || item.ContentHash == "" || item.Title == "" {
			return result, errors.New("announcement requires symbol, title, and content hash")
		}
		if item.ID == "" {
			item.ID = generateID()
		}
		if item.FetchedAt.IsZero() {
			item.FetchedAt = now
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		item.DedupeKey = announcementDedupeKey(item)
		item.LastSeenAt = now
		if item.FirstFetchedAt.IsZero() {
			item.FirstFetchedAt = item.FetchedAt
		}
		if item.BodyStatus == "" {
			item.BodyStatus = AnnouncementBodyStatusMetadataOnly
		}
		if _, exists := seen[item.DedupeKey]; exists {
			continue
		}
		seen[item.DedupeKey] = struct{}{}

		var existing struct {
			ID, ContentHash, Title, Category, PDFURL, MajorReason string
			Major                                                 bool
			PublishedAt, FetchedAt, FirstFetchedAt                sql.NullTime
		}
		lookupErr := tx.QueryRowContext(ctx, `
			SELECT id, content_hash, title, COALESCE(category,''), COALESCE(pdf_url,''),
			       major, COALESCE(major_reason,''), published_at, fetched_at, first_fetched_at
			FROM stockv2_announcements
			WHERE dedupe_key = ? OR (source = ? AND symbol = ? AND content_hash = ?)
			ORDER BY CASE WHEN dedupe_key = ? THEN 0 ELSE 1 END
			LIMIT 1
		`, item.DedupeKey, item.Source, item.Symbol, item.ContentHash, item.DedupeKey).
			Scan(&existing.ID, &existing.ContentHash, &existing.Title,
				&existing.Category, &existing.PDFURL, &existing.Major, &existing.MajorReason,
				&existing.PublishedAt, &existing.FetchedAt, &existing.FirstFetchedAt)
		isNew := errors.Is(lookupErr, sql.ErrNoRows)
		if lookupErr != nil && !isNew {
			return result, wrapError(lookupErr, "read announcement identity")
		}
		materialChange := isNew || existing.ContentHash != item.ContentHash || existing.Title != item.Title ||
			existing.Category != item.Category || existing.PDFURL != item.PDFURL || existing.Major != item.Major ||
			existing.MajorReason != item.MajorReason
		if isNew {
			_, err := tx.ExecContext(ctx, `
			INSERT INTO stockv2_announcements (
				id, source, symbol, market, org_id, title, category, announcement_id,
				pdf_url, content_hash, dedupe_key, symbol_revision, major, major_reason,
				published_at, fetched_at, first_fetched_at, last_seen_at, body_status,
				body_text_excerpt, body_hash, body_checked_at, body_error,
				body_attempt_count, body_next_attempt_at, body_content_bytes, created_at, updated_at
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				0,
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			)
		`, item.ID, item.Source, item.Symbol, item.Market, item.OrgID, item.Title,
				item.Category, item.AnnouncementID, item.PDFURL, item.ContentHash, item.DedupeKey,
				item.Major, item.MajorReason, nullableTime(item.PublishedAt), item.FetchedAt,
				item.FirstFetchedAt, item.LastSeenAt, item.BodyStatus, nullableString(item.BodyTextExcerpt),
				nullableString(item.BodyHash), nullableTime(item.BodyCheckedAt), nullableString(item.BodyError),
				item.BodyAttemptCount, nullableTime(item.BodyNextAttemptAt), item.BodyContentBytes,
				item.CreatedAt, item.UpdatedAt)
			if err != nil {
				return result, wrapError(err, "insert announcement")
			}
			result.Inserted = append(result.Inserted, item)
		} else {
			bodyChanged := existing.ContentHash != item.ContentHash || existing.PDFURL != item.PDFURL
			_, err := tx.ExecContext(ctx, `
				UPDATE stockv2_announcements
				SET market = ?, org_id = ?, title = ?, category = ?, announcement_id = ?,
					pdf_url = ?, content_hash = ?, dedupe_key = ?, major = ?, major_reason = ?, published_at = ?,
					last_seen_at = ?,
					body_status = CASE WHEN ? THEN 'metadata_only' ELSE body_status END,
					body_text_excerpt = CASE WHEN ? THEN NULL ELSE body_text_excerpt END,
					body_hash = CASE WHEN ? THEN NULL ELSE body_hash END,
					body_checked_at = CASE WHEN ? THEN NULL ELSE body_checked_at END,
					body_error = CASE WHEN ? THEN NULL ELSE body_error END,
					body_attempt_count = CASE WHEN ? THEN 0 ELSE body_attempt_count END,
					body_next_attempt_at = CASE WHEN ? THEN NULL ELSE body_next_attempt_at END,
					body_content_bytes = CASE WHEN ? THEN 0 ELSE body_content_bytes END,
					updated_at = ?
				WHERE id = ?
			`, item.Market, item.OrgID, item.Title, item.Category, item.AnnouncementID,
				item.PDFURL, item.ContentHash, item.DedupeKey, item.Major, item.MajorReason,
				nullableTime(item.PublishedAt), item.LastSeenAt,
				bodyChanged, bodyChanged, bodyChanged, bodyChanged, bodyChanged,
				bodyChanged, bodyChanged, bodyChanged,
				item.UpdatedAt, existing.ID)
			if err != nil {
				return result, wrapError(err, "update announcement")
			}
		}
		if materialChange {
			affectedSymbols[item.Symbol] = struct{}{}
			affectedKeys[item.Symbol] = append(affectedKeys[item.Symbol], item.DedupeKey)
		}
	}
	symbols := make([]string, 0, len(affectedSymbols))
	for symbol := range affectedSymbols {
		symbols = append(symbols, symbol)
	}
	revisions, err := bumpStockProfileAIAnnouncementRevisionsWithTx(ctx, tx, symbols, now)
	if err != nil {
		return result, err
	}
	for symbol, keys := range affectedKeys {
		revision, ok := revisions[symbol]
		if !ok {
			continue
		}
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx, `
					UPDATE stockv2_announcements SET symbol_revision = ? WHERE dedupe_key = ?
				`, revision, key); err != nil {
				return result, wrapError(err, "stamp announcement revision")
			}
		}
	}
	return result, nil
}

func announcementDedupeKey(item StockV2Announcement) string {
	if announcementID := strings.TrimSpace(item.AnnouncementID); announcementID != "" {
		return firstNonEmpty(item.Source, StockV2AnnouncementSourceCninfo) + ":id:" + announcementID
	}
	return firstNonEmpty(item.Source, StockV2AnnouncementSourceCninfo) + ":hash:" +
		strings.TrimSpace(item.Symbol) + ":" + strings.TrimSpace(item.ContentHash)
}

func (s *Store) ListAnnouncements(ctx context.Context, filter AnnouncementListFilter) ([]StockV2Announcement, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if symbol := strings.TrimSpace(filter.Symbol); symbol != "" {
		where = append(where, "symbol = ?")
		args = append(args, symbol)
	}
	if filter.MajorOnly {
		where = append(where, "major = TRUE")
	}
	query := announcementSelectSQL()
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY COALESCE(published_at, created_at) DESC, created_at DESC LIMIT ? OFFSET ?"
	args = append(args, normalizedPageLimit(filter.Limit, 100), normalizedPageOffset(filter.Offset))
	rows, err := s.assetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "list announcements")
	}
	return scanRows(rows, scanAnnouncement, "scan announcement", "iterate announcements")
}

func (s *Store) ListRecentAnnouncementsBySymbols(ctx context.Context, symbols []string, perSymbol int) (map[string][]StockV2Announcement, error) {
	symbols = compactStringList(symbols, 1000)
	if len(symbols) == 0 {
		return map[string][]StockV2Announcement{}, nil
	}
	if perSymbol <= 0 || perSymbol > 100 {
		perSymbol = 100
	}
	args := make([]any, 0, len(symbols)+1)
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	args = append(args, perSymbol)
	rows, err := s.assetDB().QueryContext(ctx, `
		SELECT id, source, symbol, market, org_id, title, category, announcement_id,
		       pdf_url, content_hash, dedupe_key, symbol_revision, major, major_reason,
		       published_at, fetched_at, first_fetched_at, last_seen_at, body_status,
		       body_text_excerpt, body_hash, body_checked_at, body_error,
		       body_attempt_count, body_next_attempt_at, body_content_bytes, created_at, updated_at
		FROM (
			SELECT id, source, symbol, COALESCE(market,'') AS market,
			       COALESCE(org_id,'') AS org_id, title, COALESCE(category,'') AS category,
			       COALESCE(announcement_id,'') AS announcement_id,
			       COALESCE(pdf_url,'') AS pdf_url, content_hash,
			       COALESCE(dedupe_key,'') AS dedupe_key,
			       COALESCE(symbol_revision,0) AS symbol_revision, major,
			       COALESCE(major_reason,'') AS major_reason, published_at,
			       fetched_at, first_fetched_at, last_seen_at,
			       COALESCE(body_status,'metadata_only') AS body_status,
			       COALESCE(body_text_excerpt,'') AS body_text_excerpt,
			       COALESCE(body_hash,'') AS body_hash, body_checked_at,
			       COALESCE(body_error,'') AS body_error,
			       COALESCE(body_attempt_count,0) AS body_attempt_count,
			       body_next_attempt_at,
			       COALESCE(body_content_bytes,0) AS body_content_bytes,
			       created_at, updated_at,
			       ROW_NUMBER() OVER (
			         PARTITION BY symbol
			         ORDER BY CASE
			           WHEN published_at IS NULL OR fetched_at > published_at THEN fetched_at
			           ELSE published_at
			         END DESC, fetched_at DESC, created_at DESC
			       ) AS rn
			FROM stockv2_announcements
			WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
		)
		WHERE rn <= ?
		ORDER BY symbol, CASE
		  WHEN published_at IS NULL OR fetched_at > published_at THEN fetched_at
		  ELSE published_at
		END DESC, fetched_at DESC, created_at DESC
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list recent announcements by symbols")
	}
	items, err := scanRows(rows, scanAnnouncement, "scan recent announcement", "iterate recent announcements")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]StockV2Announcement, len(symbols))
	for _, item := range items {
		out[item.Symbol] = append(out[item.Symbol], item)
	}
	return out, nil
}

func (s *Store) CountAnnouncements(ctx context.Context, filter AnnouncementListFilter) (int, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if symbol := strings.TrimSpace(filter.Symbol); symbol != "" {
		where = append(where, "symbol = ?")
		args = append(args, symbol)
	}
	if filter.MajorOnly {
		where = append(where, "major = TRUE")
	}
	query := "SELECT COUNT(*) FROM stockv2_announcements"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	err := s.assetDB().QueryRowContext(ctx, query, args...).Scan(&total)
	return total, wrapError(err, "count announcements")
}

func (s *Store) LatestAnnouncementStats(ctx context.Context, symbols []string) (map[string]StockV2AssetSummary, error) {
	symbols = compactStringList(symbols, 200)
	if len(symbols) == 0 {
		return map[string]StockV2AssetSummary{}, nil
	}
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	// 使用窗口函数一次查询获取每个 symbol 的聚合统计 + 最新标题，
	// 避免 N+1 查询（原实现对每个 symbol 单独查一次标题）。
	rows, err := s.assetDB().QueryContext(ctx, `
		WITH agg AS (
				SELECT symbol,
				       COUNT(*) AS total_count,
				       SUM(CASE WHEN major THEN 1 ELSE 0 END) AS major_count,
				       SUM(CASE WHEN major AND COALESCE(NULLIF(body_status, ''), 'metadata_only') <> 'text_ready' THEN 1 ELSE 0 END) AS major_content_unavailable_count,
				       MAX(published_at) AS latest_published,
			       MAX(fetched_at) AS latest_fetched
			FROM stockv2_announcements
			WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
			GROUP BY symbol
		),
		ranked AS (
			SELECT symbol, title,
			       ROW_NUMBER() OVER (
			         PARTITION BY symbol
			         ORDER BY CASE
			           WHEN published_at IS NULL OR fetched_at > published_at THEN fetched_at
			           ELSE published_at
			         END DESC, fetched_at DESC, created_at DESC
			       ) AS rn
			FROM stockv2_announcements
			WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
		)
			SELECT a.symbol, a.total_count, a.major_count, a.major_content_unavailable_count,
			       a.latest_published, a.latest_fetched, COALESCE(r.title, '')
		FROM agg a
		LEFT JOIN ranked r ON r.symbol = a.symbol AND r.rn = 1
	`, append(args, args...)...)
	if err != nil {
		return nil, wrapError(err, "list announcement stats")
	}
	defer rows.Close()
	out := make(map[string]StockV2AssetSummary, len(symbols))
	for rows.Next() {
		var symbol string
		var total, major, majorContentUnavailable int
		var latest, latestFetched sql.NullTime
		var title string
		if err := rows.Scan(&symbol, &total, &major, &majorContentUnavailable, &latest, &latestFetched, &title); err != nil {
			return nil, wrapError(err, "scan announcement stats")
		}
		item := out[symbol]
		item.Symbol = symbol
		item.AnnouncementCount = total
		item.MajorAnnouncementCount = major
		item.MajorAnnouncementContentUnavailableCount = majorContentUnavailable
		if latest.Valid {
			item.LatestAnnouncementAt = latest.Time
		}
		if latestFetched.Valid {
			item.LatestAnnouncementFetchedAt = latestFetched.Time
		}
		item.LatestAnnouncementTitle = title
		out[symbol] = item
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate announcement stats")
	}
	return out, nil
}

func announcementSelectSQL() string {
	return `
		SELECT id, source, symbol, COALESCE(market,''), COALESCE(org_id,''), title,
		       COALESCE(category,''), COALESCE(announcement_id,''), COALESCE(pdf_url,''),
		       content_hash, COALESCE(dedupe_key,''), COALESCE(symbol_revision,0),
		       major, COALESCE(major_reason,''), published_at, fetched_at,
		       first_fetched_at, last_seen_at, COALESCE(body_status,'metadata_only'),
		       COALESCE(body_text_excerpt,''), COALESCE(body_hash,''), body_checked_at,
		       COALESCE(body_error,''), COALESCE(body_attempt_count,0), body_next_attempt_at,
		       COALESCE(body_content_bytes,0), created_at, updated_at
		FROM stockv2_announcements`
}

func scanAnnouncement(row rowScanner) (StockV2Announcement, error) {
	var item StockV2Announcement
	var publishedAt, firstFetchedAt, lastSeenAt, bodyCheckedAt, bodyNextAttemptAt sql.NullTime
	if err := row.Scan(
		&item.ID, &item.Source, &item.Symbol, &item.Market, &item.OrgID, &item.Title,
		&item.Category, &item.AnnouncementID, &item.PDFURL, &item.ContentHash,
		&item.DedupeKey, &item.SymbolRevision, &item.Major, &item.MajorReason,
		&publishedAt, &item.FetchedAt, &firstFetchedAt, &lastSeenAt,
		&item.BodyStatus, &item.BodyTextExcerpt, &item.BodyHash, &bodyCheckedAt, &item.BodyError,
		&item.BodyAttemptCount, &bodyNextAttemptAt, &item.BodyContentBytes,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return StockV2Announcement{}, err
	}
	if publishedAt.Valid {
		item.PublishedAt = publishedAt.Time
	}
	if firstFetchedAt.Valid {
		item.FirstFetchedAt = firstFetchedAt.Time
	}
	if lastSeenAt.Valid {
		item.LastSeenAt = lastSeenAt.Time
	}
	if bodyCheckedAt.Valid {
		item.BodyCheckedAt = bodyCheckedAt.Time
	}
	if bodyNextAttemptAt.Valid {
		item.BodyNextAttemptAt = bodyNextAttemptAt.Time
	}
	return item, nil
}
