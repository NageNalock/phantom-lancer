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
			daily_bar_status, daily_bar_fetched, daily_bar_start, daily_bar_end,
			base_profile_status, base_profile_changed, base_profile_hash_before, base_profile_hash_after,
			announcement_status, announcements_new, major_announcements_new,
			ai_decision, ai_profile_status, agent_run_id, error_message, source_statuses_json,
			duration_ms, started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			job_id = excluded.job_id,
			symbol = excluded.symbol,
			market = excluded.market,
			instrument_type = excluded.instrument_type,
			name = excluded.name,
			status = excluded.status,
			daily_bar_status = excluded.daily_bar_status,
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
			agent_run_id = excluded.agent_run_id,
			error_message = excluded.error_message,
			source_statuses_json = excluded.source_statuses_json,
			duration_ms = excluded.duration_ms,
			finished_at = excluded.finished_at,
			updated_at = excluded.updated_at
	`, item.ID, item.JobID, item.Symbol, item.Market, item.InstrumentType, item.Name, item.Status,
		item.DailyBarStatus, item.DailyBarFetched, item.DailyBarStart, item.DailyBarEnd,
		item.BaseProfileStatus, boolInt(item.BaseProfileChanged), item.BaseProfileHashBefore, item.BaseProfileHashAfter,
		item.AnnouncementStatus, item.AnnouncementsNew, item.MajorAnnouncementsNew,
		item.AIDecision, item.AIProfileStatus, item.AgentRunID, safelog.Text(item.ErrorMessage, 800), string(statusesJSON),
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
	if status != "" {
		sets = append(sets, "status = ?")
		args = append(args, status)
	}
	if aiStatus != "" {
		sets = append(sets, "ai_profile_status = ?")
		args = append(args, aiStatus)
	}
	if errorMessage != "" {
		sets = append(sets, "error_message = ?")
		args = append(args, safelog.Text(errorMessage, 800))
	}
	if !finishedAt.IsZero() {
		sets = append(sets, "finished_at = ?")
		args = append(args, finishedAt)
	}
	args = append(args, agentRunID)
	_, err := s.db.ExecContext(ctx, `UPDATE stockv2_asset_maintenance_items SET `+strings.Join(sets, ", ")+` WHERE agent_run_id = ?`, args...)
	return wrapError(err, "update asset maintenance item by agent run")
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
		       COALESCE(name,''), status, COALESCE(daily_bar_status,''), COALESCE(daily_bar_fetched,0),
		       COALESCE(daily_bar_start,''), COALESCE(daily_bar_end,''), COALESCE(base_profile_status,''),
		       COALESCE(base_profile_changed,0), COALESCE(base_profile_hash_before,''), COALESCE(base_profile_hash_after,''),
		       COALESCE(announcement_status,''), COALESCE(announcements_new,0), COALESCE(major_announcements_new,0),
		       COALESCE(ai_decision,''), COALESCE(ai_profile_status,''), COALESCE(agent_run_id,''),
		       COALESCE(error_message,''), COALESCE(source_statuses_json,'[]'), COALESCE(duration_ms,0),
		       started_at, finished_at, created_at, updated_at
		FROM stockv2_asset_maintenance_items`
}

func scanAssetMaintenanceItem(row rowScanner) (AssetMaintenanceItem, error) {
	var item AssetMaintenanceItem
	var changed int
	var statusesJSON string
	var finishedAt sql.NullTime
	if err := row.Scan(
		&item.ID, &item.JobID, &item.Symbol, &item.Market, &item.InstrumentType,
		&item.Name, &item.Status, &item.DailyBarStatus, &item.DailyBarFetched,
		&item.DailyBarStart, &item.DailyBarEnd, &item.BaseProfileStatus,
		&changed, &item.BaseProfileHashBefore, &item.BaseProfileHashAfter,
		&item.AnnouncementStatus, &item.AnnouncementsNew, &item.MajorAnnouncementsNew,
		&item.AIDecision, &item.AIProfileStatus, &item.AgentRunID,
		&item.ErrorMessage, &statusesJSON, &item.DurationMs,
		&item.StartedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return AssetMaintenanceItem{}, err
	}
	item.BaseProfileChanged = changed != 0
	if finishedAt.Valid {
		item.FinishedAt = finishedAt.Time
	}
	_ = json.Unmarshal([]byte(statusesJSON), &item.SourceStatuses)
	return item, nil
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
	now := time.Now()
	inserted := 0
	for i := range items {
		item := items[i]
		if item.ID == "" {
			item.ID = generateID()
		}
		if item.Source == "" {
			item.Source = StockV2AnnouncementSourceCninfo
		}
		if item.FetchedAt.IsZero() {
			item.FetchedAt = now
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		var existingID string
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM stockv2_announcements
			WHERE source = ? AND symbol = ? AND content_hash = ?
		`, item.Source, item.Symbol, item.ContentHash).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return inserted, wrapError(err, "lookup announcement duplicate")
		}
		if existingID != "" {
			_, err = tx.ExecContext(ctx, `
				UPDATE stockv2_announcements
				SET market = ?, org_id = ?, title = ?, category = ?, announcement_id = ?,
				    pdf_url = ?, major = ?, major_reason = ?, published_at = ?,
				    fetched_at = ?, updated_at = ?
				WHERE id = ?
			`, item.Market, item.OrgID, item.Title, item.Category, item.AnnouncementID,
				item.PDFURL, item.Major, item.MajorReason, nullableTime(item.PublishedAt),
				item.FetchedAt, item.UpdatedAt, existingID)
		} else {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO stockv2_announcements (
					id, source, symbol, market, org_id, title, category, announcement_id,
					pdf_url, content_hash, major, major_reason, published_at,
					fetched_at, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, item.ID, item.Source, item.Symbol, item.Market, item.OrgID, item.Title,
				item.Category, item.AnnouncementID, item.PDFURL, item.ContentHash, item.Major,
				item.MajorReason, nullableTime(item.PublishedAt), item.FetchedAt, item.CreatedAt, item.UpdatedAt)
			inserted++
		}
		if err != nil {
			return inserted, wrapError(err, "upsert announcement")
		}
	}
	if err := tx.Commit(); err != nil {
		return inserted, wrapError(err, "commit announcements")
	}
	return inserted, nil
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
	rows, err := s.assetDB().QueryContext(ctx, `
		SELECT symbol,
		       COUNT(*),
		       SUM(CASE WHEN major THEN 1 ELSE 0 END),
		       MAX(published_at)
		FROM stockv2_announcements
		WHERE symbol IN (`+sqlPlaceholders(len(symbols))+`)
		GROUP BY symbol
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list announcement stats")
	}
	defer rows.Close()
	out := make(map[string]StockV2AssetSummary, len(symbols))
	for rows.Next() {
		var symbol string
		var total, major int
		var latest sql.NullTime
		if err := rows.Scan(&symbol, &total, &major, &latest); err != nil {
			return nil, wrapError(err, "scan announcement stats")
		}
		item := out[symbol]
		item.Symbol = symbol
		item.AnnouncementCount = total
		item.MajorAnnouncementCount = major
		if latest.Valid {
			item.LatestAnnouncementAt = latest.Time
		}
		out[symbol] = item
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate announcement stats")
	}
	for symbol, item := range out {
		var title string
		_ = s.assetDB().QueryRowContext(ctx, `
			SELECT COALESCE(title,'') FROM stockv2_announcements
			WHERE symbol = ? ORDER BY COALESCE(published_at, created_at) DESC, created_at DESC LIMIT 1
		`, symbol).Scan(&title)
		item.LatestAnnouncementTitle = title
		out[symbol] = item
	}
	return out, nil
}

func announcementSelectSQL() string {
	return `
		SELECT id, source, symbol, COALESCE(market,''), COALESCE(org_id,''), title,
		       COALESCE(category,''), COALESCE(announcement_id,''), COALESCE(pdf_url,''),
		       content_hash, major, COALESCE(major_reason,''), published_at,
		       fetched_at, created_at, updated_at
		FROM stockv2_announcements`
}

func scanAnnouncement(row rowScanner) (StockV2Announcement, error) {
	var item StockV2Announcement
	var publishedAt sql.NullTime
	if err := row.Scan(
		&item.ID, &item.Source, &item.Symbol, &item.Market, &item.OrgID, &item.Title,
		&item.Category, &item.AnnouncementID, &item.PDFURL, &item.ContentHash,
		&item.Major, &item.MajorReason, &publishedAt, &item.FetchedAt,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return StockV2Announcement{}, err
	}
	if publishedAt.Valid {
		item.PublishedAt = publishedAt.Time
	}
	return item, nil
}
