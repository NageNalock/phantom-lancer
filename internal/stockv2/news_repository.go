package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
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
	_, err := s.db.ExecContext(ctx, `
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
	return s.getRawNewsByDedupeKey(ctx, item.DedupeKey)
}

func (s *Store) GetRawNews(ctx context.Context, id string) (StockV2RawNews, error) {
	row := s.db.QueryRowContext(ctx, rawNewsSelectSQL+" WHERE id = ?", id)
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
	row := s.db.QueryRowContext(ctx, rawNewsSelectSQL+" WHERE dedupe_key = ?", dedupeKey)
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
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY fetched_at DESC, created_at DESC LIMIT ? OFFSET ?
	`, rawNewsSelectSQL, where), args...)
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

func (s *Store) CountRawNews(ctx context.Context, filter RawNewsListFilter) (int, error) {
	where, args := rawNewsFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_raw_news WHERE %s`, where), args...).Scan(&count); err != nil {
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
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
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
	result, err := s.db.ExecContext(ctx, `
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
	state.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_source_states
			(source, enabled, status, cursor, last_fetch_at, last_success_at, last_error_at,
			 last_error, consecutive_failures, backoff_until, raw_news_count, news_event_count,
			 link_candidate_count, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET
			enabled = excluded.enabled,
			status = excluded.status,
			cursor = excluded.cursor,
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
	if err := row.Scan(
		&state.Source, &enabled, &state.Status, &state.Cursor, &lastFetchAt, &lastSuccessAt,
		&lastErrorAt, &state.LastError, &state.ConsecutiveFailures, &backoffUntil,
		&state.RawNewsCount, &state.NewsEventCount, &state.LinkCandidateCount, &state.UpdatedAt,
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
	return state, nil
}

func rawNewsFilterSQL(filter RawNewsListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	addNewsStringFilter(&where, &args, "source", filter.Source)
	addNewsStringFilter(&where, &args, "language", filter.Language)
	addNewsStringFilter(&where, &args, "status", filter.Status)
	addNewsStringFilter(&where, &args, "quality", filter.Quality)
	addNewsTimeWindow(&where, &args, "fetched_at", filter.Since, filter.Until)
	return strings.Join(where, " AND "), args
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
