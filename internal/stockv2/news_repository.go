package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	minNewsPollIntervalSeconds = 60
	minNewsBackoffBaseSeconds  = 30
)

func (s *Store) CreateRawNews(ctx context.Context, item StockV2RawNews) (StockV2RawNews, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.FetchedAt.IsZero() {
		item.FetchedAt = now
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	result, err := s.assetDB().ExecContext(ctx, `
		INSERT OR IGNORE INTO stockv2_raw_news
			(id, source, source_id, language, title, content, snippet, published_at, fetched_at,
			 url, raw_payload_json, content_hash, dedupe_key, quality, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		item.ID,
		item.Source,
		nullableNewsString(item.SourceID),
		nullableNewsString(item.Language),
		item.Title,
		nullableNewsString(item.Content),
		nullableNewsString(item.Snippet),
		nullableNewsTime(item.PublishedAt),
		item.FetchedAt,
		nullableNewsString(item.URL),
		marshalMap(item.RawPayload),
		item.ContentHash,
		item.DedupeKey,
		item.Quality,
		item.Status,
		item.CreatedAt,
		item.UpdatedAt,
	)
	if err != nil {
		return StockV2RawNews{}, wrapError(err, "create raw news")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return StockV2RawNews{}, wrapError(err, "check raw news affected rows")
	}
	if rows == 0 {
		existing, err := s.getRawNewsByDedupeKey(ctx, item.DedupeKey)
		if err != nil {
			return StockV2RawNews{}, err
		}
		if existing.Status == NewsStatusFailed && item.Status == NewsStatusNew {
			if err := s.UpdateRawNewsStatus(ctx, existing.ID, NewsStatusNew); err != nil {
				return StockV2RawNews{}, err
			}
			return s.GetRawNews(ctx, existing.ID)
		}
		return existing, nil
	}
	return s.getRawNewsByDedupeKey(ctx, item.DedupeKey)
}

func (s *Store) GetRawNews(ctx context.Context, id string) (StockV2RawNews, error) {
	row := s.assetDB().QueryRowContext(ctx, rawNewsSelectSQL+" WHERE id = ?", id)
	item, err := scanRawNews(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2RawNews{}, ErrRawNewsNotFound
		}
		return StockV2RawNews{}, wrapError(err, "get raw news")
	}
	return item, nil
}

func (s *Store) getRawNewsByDedupeKey(ctx context.Context, dedupeKey string) (StockV2RawNews, error) {
	row := s.assetDB().QueryRowContext(ctx, rawNewsSelectSQL+" WHERE dedupe_key = ?", dedupeKey)
	item, err := scanRawNews(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2RawNews{}, ErrRawNewsNotFound
		}
		return StockV2RawNews{}, wrapError(err, "get raw news by dedupe key")
	}
	return item, nil
}

func (s *Store) ListRawNews(ctx context.Context, filter RawNewsListFilter) ([]StockV2RawNews, error) {
	where, args := rawNewsFilterSQL(filter)
	args = append(args, normalizedNewsLimit(filter.Limit), normalizedNewsOffset(filter.Offset))
	rows, err := s.assetDB().QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY %s DESC, fetched_at DESC, created_at DESC LIMIT ? OFFSET ?
	`, rawNewsListSelectSQL, where, rawNewsTimeSQL), args...)
	if err != nil {
		return nil, wrapError(err, "list raw news")
	}
	defer rows.Close()
	items := make([]StockV2RawNews, 0)
	for rows.Next() {
		item, err := scanRawNews(rows)
		if err != nil {
			return nil, wrapError(err, "scan raw news")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate raw news")
	}
	return items, nil
}

func (s *Store) TruncateRawNewsBefore(ctx context.Context, before time.Time) (int, error) {
	if before.IsZero() {
		return 0, ErrInvalidRawNewsTruncateBefore
	}
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return 0, wrapError(err, "begin truncate raw news")
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT source, COUNT(*)
		FROM stockv2_raw_news
		WHERE %s < ?
		GROUP BY source
	`, rawNewsTimeSQL), before)
	if err != nil {
		return 0, wrapError(err, "count raw news truncate by source")
	}
	deletedBySource := make(map[string]int)
	for rows.Next() {
		var source string
		var count int
		if err := rows.Scan(&source, &count); err != nil {
			rows.Close()
			return 0, wrapError(err, "scan raw news truncate count")
		}
		deletedBySource[source] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, wrapError(err, "iterate raw news truncate count")
	}
	rows.Close()

	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM stockv2_raw_news
		WHERE %s < ?
	`, rawNewsTimeSQL), before)
	if err != nil {
		return 0, wrapError(err, "truncate raw news")
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, wrapError(err, "check truncated raw news rows")
	}
	now := time.Now()
	for source, count := range deletedBySource {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE stockv2_news_source_states
			SET raw_news_count = CASE WHEN raw_news_count > ? THEN raw_news_count - ? ELSE 0 END,
			    updated_at = ?
			WHERE source = ?
		`, count, count, now, source); err != nil {
			return 0, wrapError(err, "sync raw news source count after truncate")
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, wrapError(err, "commit truncate raw news")
	}
	return int(deleted), nil
}

func (s *Store) CountRawNews(ctx context.Context, filter RawNewsListFilter) (int, error) {
	where, args := rawNewsFilterSQL(filter)
	var count int
	if err := s.assetDB().QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_raw_news WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count raw news")
	}
	return count, nil
}

func (s *Store) ListUnprocessedRawNews(ctx context.Context, before time.Time, limit int) ([]StockV2RawNews, error) {
	filter := RawNewsListFilter{Status: NewsStatusNew, Limit: limit}
	where, args := rawNewsFilterSQL(filter)
	if !before.IsZero() {
		where += " AND fetched_at <= ?"
		args = append(args, before)
	}
	args = append(args, normalizedNewsLimit(limit))
	rows, err := s.assetDB().QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY fetched_at ASC, created_at ASC LIMIT ?
	`, rawNewsSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list unprocessed raw news")
	}
	defer rows.Close()
	items := make([]StockV2RawNews, 0)
	for rows.Next() {
		item, err := scanRawNews(rows)
		if err != nil {
			return nil, wrapError(err, "scan unprocessed raw news")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate unprocessed raw news")
}

func (s *Store) UpdateRawNewsStatus(ctx context.Context, id, status string) error {
	result, err := s.assetDB().ExecContext(ctx, `
		UPDATE stockv2_raw_news
		SET status = ?, updated_at = ?
		WHERE id = ?
	`, status, time.Now(), id)
	if err != nil {
		return wrapError(err, "update raw news status")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrRawNewsNotFound
	}
	return nil
}

func (s *Store) UpsertNewsSourceState(ctx context.Context, state NewsSourceState) error {
	if strings.TrimSpace(state.Source) == "" {
		return ErrNewsSourceAdapterNotFound
	}
	if state.Status == "" {
		state.Status = NewsSourceStatusIdle
	}
	state = normalizeNewsSourceStateDefaults(state)
	state.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_source_states
			(source, enabled, status, cursor, poll_interval_seconds, jitter_seconds,
			 batch_limit, process_limit, backoff_base_seconds, backoff_max_seconds,
			 next_run_at, last_run_at, last_run_status, last_run_error,
			 last_fetch_at, last_success_at, last_error_at, last_error,
			 consecutive_failures, backoff_until, raw_news_count, news_event_count,
			 link_candidate_count, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET
			enabled = excluded.enabled,
			status = excluded.status,
			cursor = excluded.cursor,
			poll_interval_seconds = excluded.poll_interval_seconds,
			jitter_seconds = excluded.jitter_seconds,
			batch_limit = excluded.batch_limit,
			process_limit = excluded.process_limit,
			backoff_base_seconds = excluded.backoff_base_seconds,
			backoff_max_seconds = excluded.backoff_max_seconds,
			next_run_at = excluded.next_run_at,
			last_run_at = excluded.last_run_at,
			last_run_status = excluded.last_run_status,
			last_run_error = excluded.last_run_error,
			last_fetch_at = excluded.last_fetch_at,
			last_success_at = excluded.last_success_at,
			last_error_at = excluded.last_error_at,
			last_error = excluded.last_error,
			consecutive_failures = excluded.consecutive_failures,
			backoff_until = excluded.backoff_until,
			raw_news_count = excluded.raw_news_count,
			news_event_count = excluded.news_event_count,
			link_candidate_count = excluded.link_candidate_count,
			updated_at = excluded.updated_at
	`,
		state.Source,
		boolToInt(state.Enabled),
		state.Status,
		nullableNewsString(state.Cursor),
		state.PollIntervalSeconds,
		state.JitterSeconds,
		state.BatchLimit,
		state.ProcessLimit,
		state.BackoffBaseSeconds,
		state.BackoffMaxSeconds,
		nullableNewsTime(state.NextRunAt),
		nullableNewsTime(state.LastRunAt),
		nullableNewsString(state.LastRunStatus),
		nullableNewsString(state.LastRunError),
		nullableNewsTime(state.LastFetchAt),
		nullableNewsTime(state.LastSuccessAt),
		nullableNewsTime(state.LastErrorAt),
		nullableNewsString(state.LastError),
		state.ConsecutiveFailures,
		nullableNewsTime(state.BackoffUntil),
		state.RawNewsCount,
		state.NewsEventCount,
		state.LinkCandidateCount,
		state.UpdatedAt,
	)
	return wrapError(err, "upsert news source state")
}

func (s *Store) GetNewsSourceState(ctx context.Context, source string) (NewsSourceState, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT source, enabled, status, COALESCE(cursor,''), last_fetch_at, last_success_at,
		       last_error_at, COALESCE(last_error,''), consecutive_failures, backoff_until,
		       raw_news_count, news_event_count, link_candidate_count, updated_at
		       , COALESCE(poll_interval_seconds, 600), COALESCE(jitter_seconds, 60),
		       COALESCE(batch_limit, 50), COALESCE(process_limit, 50),
		       COALESCE(backoff_base_seconds, 30), COALESCE(backoff_max_seconds, 900),
		       next_run_at, last_run_at, COALESCE(last_run_status,''), COALESCE(last_run_error,'')
		FROM stockv2_news_source_states
		WHERE source = ?
	`, strings.TrimSpace(source))
	state, err := scanNewsSourceState(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsSourceState{}, false, nil
		}
		return NewsSourceState{}, false, wrapError(err, "get news source state")
	}
	return state, true, nil
}

const rawNewsSelectSQL = `
	SELECT id, source, COALESCE(source_id,''), COALESCE(language,''), title,
	       COALESCE(content,''), COALESCE(snippet,''), published_at, fetched_at,
	       COALESCE(url,''), COALESCE(raw_payload_json,'{}'), content_hash, dedupe_key, quality, status, created_at, updated_at
	FROM stockv2_raw_news
`

const rawNewsListSelectSQL = `
	SELECT id, source, COALESCE(source_id,''), COALESCE(language,''), title,
	       COALESCE(content,''), COALESCE(snippet,''), published_at, fetched_at,
	       COALESCE(url,''), '{}', content_hash, dedupe_key, quality, status, created_at, updated_at
	FROM stockv2_raw_news
`

const rawNewsTimeSQL = `COALESCE(published_at, fetched_at)`

func scanRawNews(row rowScanner) (StockV2RawNews, error) {
	var item StockV2RawNews
	var publishedAt sql.NullTime
	var rawPayloadJSON string
	if err := row.Scan(
		&item.ID, &item.Source, &item.SourceID, &item.Language, &item.Title,
		&item.Content, &item.Snippet, &publishedAt, &item.FetchedAt,
		&item.URL, &rawPayloadJSON, &item.ContentHash, &item.DedupeKey, &item.Quality, &item.Status,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return item, err
	}
	if publishedAt.Valid {
		item.PublishedAt = publishedAt.Time
	}
	item.RawPayload = unmarshalMap(rawPayloadJSON)
	return item, nil
}

func scanNewsSourceState(row rowScanner) (NewsSourceState, error) {
	var state NewsSourceState
	var enabled int
	var lastFetchAt, lastSuccessAt, lastErrorAt, backoffUntil sql.NullTime
	var nextRunAt, lastRunAt sql.NullTime
	if err := row.Scan(
		&state.Source, &enabled, &state.Status, &state.Cursor, &lastFetchAt, &lastSuccessAt,
		&lastErrorAt, &state.LastError, &state.ConsecutiveFailures, &backoffUntil,
		&state.RawNewsCount, &state.NewsEventCount, &state.LinkCandidateCount, &state.UpdatedAt,
		&state.PollIntervalSeconds, &state.JitterSeconds, &state.BatchLimit, &state.ProcessLimit,
		&state.BackoffBaseSeconds, &state.BackoffMaxSeconds, &nextRunAt, &lastRunAt,
		&state.LastRunStatus, &state.LastRunError,
	); err != nil {
		return state, err
	}
	state.Enabled = enabled != 0
	if lastFetchAt.Valid {
		state.LastFetchAt = lastFetchAt.Time
	}
	if lastSuccessAt.Valid {
		state.LastSuccessAt = lastSuccessAt.Time
	}
	if lastErrorAt.Valid {
		state.LastErrorAt = lastErrorAt.Time
	}
	if backoffUntil.Valid {
		state.BackoffUntil = backoffUntil.Time
	}
	if nextRunAt.Valid {
		state.NextRunAt = nextRunAt.Time
	}
	if lastRunAt.Valid {
		state.LastRunAt = lastRunAt.Time
	}
	return normalizeNewsSourceStateDefaults(state), nil
}

func rawNewsFilterSQL(filter RawNewsListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	addNewsStringFilter(&where, &args, "source", filter.Source)
	addNewsStringFilter(&where, &args, "language", filter.Language)
	addNewsStringFilter(&where, &args, "status", filter.Status)
	addNewsStringFilter(&where, &args, "quality", filter.Quality)
	addNewsTimeWindow(&where, &args, rawNewsTimeSQL, filter.Since, filter.Until)
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		where = append(where, "(LOWER(title) LIKE ? OR LOWER(snippet) LIKE ? OR LOWER(content) LIKE ? OR LOWER(source_id) LIKE ? OR LOWER(dedupe_key) LIKE ?)")
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	return strings.Join(where, " AND "), args
}

func normalizeNewsSourceStateDefaults(state NewsSourceState) NewsSourceState {
	if state.PollIntervalSeconds <= 0 {
		state.PollIntervalSeconds = 600
	} else if state.PollIntervalSeconds < minNewsPollIntervalSeconds {
		state.PollIntervalSeconds = minNewsPollIntervalSeconds
	}
	if state.JitterSeconds < 0 {
		state.JitterSeconds = 0
	}
	if state.BatchLimit <= 0 {
		state.BatchLimit = 50
	}
	if state.BatchLimit > 200 {
		state.BatchLimit = 200
	}
	if state.ProcessLimit <= 0 {
		state.ProcessLimit = 50
	}
	if state.ProcessLimit > 200 {
		state.ProcessLimit = 200
	}
	if state.BackoffBaseSeconds <= 0 {
		state.BackoffBaseSeconds = 30
	} else if state.BackoffBaseSeconds < minNewsBackoffBaseSeconds {
		state.BackoffBaseSeconds = minNewsBackoffBaseSeconds
	}
	if state.BackoffMaxSeconds <= 0 {
		state.BackoffMaxSeconds = 900
	} else if state.BackoffMaxSeconds < minNewsBackoffBaseSeconds {
		state.BackoffMaxSeconds = minNewsBackoffBaseSeconds
	}
	if state.BackoffMaxSeconds < state.BackoffBaseSeconds {
		state.BackoffMaxSeconds = state.BackoffBaseSeconds
	}
	return state
}

func addNewsStringFilter(where *[]string, args *[]any, column, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, strings.TrimSpace(value))
}

func addNewsTimeWindow(where *[]string, args *[]any, column string, since, until time.Time) {
	if !since.IsZero() {
		*where = append(*where, column+" >= ?")
		*args = append(*args, since)
	}
	if !until.IsZero() {
		*where = append(*where, column+" <= ?")
		*args = append(*args, until)
	}
}

func normalizedNewsLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizedNewsOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func nullableNewsString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
