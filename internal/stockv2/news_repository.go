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
			 raw_payload_json, content_hash, dedupe_key, quality, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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

func (s *Store) CreateNewsEvent(ctx context.Context, event StockV2NewsEvent) (StockV2NewsEvent, error) {
	now := time.Now()
	if event.ID == "" {
		event.ID = generateID()
	}
	if event.EventTime.IsZero() {
		event.EventTime = now
	}
	event.CreatedAt = now
	event.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO stockv2_news_events
			(id, raw_news_id, title, summary, snippet, language, source, event_time,
			 importance, tags_json, topics_json, dedupe_key, quality, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.ID,
		nullableNewsString(event.RawNewsID),
		event.Title,
		nullableNewsString(event.Summary),
		nullableNewsString(event.Snippet),
		nullableNewsString(event.Language),
		event.Source,
		event.EventTime,
		event.Importance,
		marshalStrings(event.Tags),
		marshalStrings(event.Topics),
		event.DedupeKey,
		event.Quality,
		event.Status,
		event.CreatedAt,
		event.UpdatedAt,
	)
	if err != nil {
		return StockV2NewsEvent{}, wrapError(err, "create news event")
	}
	return s.getNewsEventByDedupeKey(ctx, event.DedupeKey)
}

func (s *Store) GetNewsEvent(ctx context.Context, id string) (StockV2NewsEvent, error) {
	row := s.db.QueryRowContext(ctx, newsEventSelectSQL+" WHERE id = ?", id)
	event, err := scanNewsEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2NewsEvent{}, ErrNewsEventNotFound
		}
		return StockV2NewsEvent{}, wrapError(err, "get news event")
	}
	return event, nil
}

func (s *Store) getNewsEventByDedupeKey(ctx context.Context, dedupeKey string) (StockV2NewsEvent, error) {
	row := s.db.QueryRowContext(ctx, newsEventSelectSQL+" WHERE dedupe_key = ?", dedupeKey)
	event, err := scanNewsEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2NewsEvent{}, ErrNewsEventNotFound
		}
		return StockV2NewsEvent{}, wrapError(err, "get news event by dedupe key")
	}
	return event, nil
}

func (s *Store) ListNewsEvents(ctx context.Context, filter NewsEventListFilter) ([]StockV2NewsEvent, error) {
	where, args := newsEventFilterSQL(filter)
	args = append(args, normalizedNewsLimit(filter.Limit), normalizedNewsOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY event_time DESC, created_at DESC LIMIT ? OFFSET ?
	`, newsEventSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list news events")
	}
	defer rows.Close()
	items := make([]StockV2NewsEvent, 0)
	for rows.Next() {
		item, err := scanNewsEvent(rows)
		if err != nil {
			return nil, wrapError(err, "scan news event")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate news events")
	}
	return items, nil
}

func (s *Store) CountNewsEvents(ctx context.Context, filter NewsEventListFilter) (int, error) {
	where, args := newsEventFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_news_events WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count news events")
	}
	return count, nil
}

func (s *Store) ListUnprocessedNewsEvents(ctx context.Context, before time.Time, limit int) ([]StockV2NewsEvent, error) {
	filter := NewsEventListFilter{Status: NewsStatusNew, Limit: limit}
	where, args := newsEventFilterSQL(filter)
	if !before.IsZero() {
		where += " AND event_time <= ?"
		args = append(args, before)
	}
	args = append(args, normalizedNewsLimit(limit))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY event_time ASC, created_at ASC LIMIT ?
	`, newsEventSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list unprocessed news events")
	}
	defer rows.Close()
	items := make([]StockV2NewsEvent, 0)
	for rows.Next() {
		item, err := scanNewsEvent(rows)
		if err != nil {
			return nil, wrapError(err, "scan unprocessed news event")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate unprocessed news events")
}

func (s *Store) UpsertNewsLinkCandidate(ctx context.Context, item StockV2NewsLinkCandidate) (StockV2NewsLinkCandidate, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_link_candidates
			(id, news_event_id, symbol, market, match_method, score, reason, matched_terms_json,
			 status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(news_event_id, symbol, market, match_method) DO UPDATE SET
			score = excluded.score,
			reason = excluded.reason,
			matched_terms_json = excluded.matched_terms_json,
			status = excluded.status,
			updated_at = excluded.updated_at
	`,
		item.ID,
		item.NewsEventID,
		item.Symbol,
		item.Market,
		item.MatchMethod,
		item.Score,
		nullableNewsString(item.Reason),
		marshalStrings(item.MatchedTerms),
		item.Status,
		item.CreatedAt,
		item.UpdatedAt,
	)
	if err != nil {
		return StockV2NewsLinkCandidate{}, wrapError(err, "upsert news link candidate")
	}
	return s.getNewsLinkCandidateByKey(ctx, item.NewsEventID, item.Symbol, item.Market, item.MatchMethod)
}

func (s *Store) ListNewsLinkCandidates(ctx context.Context, filter NewsLinkCandidateListFilter) ([]StockV2NewsLinkCandidate, error) {
	where, args := newsLinkCandidateFilterSQL(filter)
	args = append(args, normalizedNewsLimit(filter.Limit), normalizedNewsOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY score DESC, updated_at DESC LIMIT ? OFFSET ?
	`, newsLinkCandidateSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list news link candidates")
	}
	defer rows.Close()
	items := make([]StockV2NewsLinkCandidate, 0)
	for rows.Next() {
		item, err := scanNewsLinkCandidate(rows)
		if err != nil {
			return nil, wrapError(err, "scan news link candidate")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate news link candidates")
	}
	return items, nil
}

func (s *Store) CountNewsLinkCandidates(ctx context.Context, filter NewsLinkCandidateListFilter) (int, error) {
	where, args := newsLinkCandidateFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_news_link_candidates WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count news link candidates")
	}
	return count, nil
}

func (s *Store) getNewsLinkCandidateByKey(ctx context.Context, eventID, symbol, market, matchMethod string) (StockV2NewsLinkCandidate, error) {
	row := s.db.QueryRowContext(ctx, newsLinkCandidateSelectSQL+`
		WHERE news_event_id = ? AND symbol = ? AND market = ? AND match_method = ?
	`, eventID, symbol, market, matchMethod)
	item, err := scanNewsLinkCandidate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2NewsLinkCandidate{}, ErrNewsLinkCandidateNotFound
		}
		return StockV2NewsLinkCandidate{}, wrapError(err, "get news link candidate by key")
	}
	return item, nil
}

const rawNewsSelectSQL = `
	SELECT id, source, COALESCE(source_id,''), COALESCE(language,''), title,
	       COALESCE(content,''), COALESCE(snippet,''), published_at, fetched_at,
	       COALESCE(raw_payload_json,'{}'), content_hash, dedupe_key, quality, status, created_at, updated_at
	FROM stockv2_raw_news
`

const newsEventSelectSQL = `
	SELECT id, COALESCE(raw_news_id,''), title, COALESCE(summary,''), COALESCE(snippet,''),
	       COALESCE(language,''), source, event_time, importance, COALESCE(tags_json,'[]'), COALESCE(topics_json,'[]'),
	       dedupe_key, quality, status, created_at, updated_at
	FROM stockv2_news_events
`

const newsLinkCandidateSelectSQL = `
	SELECT id, news_event_id, symbol, COALESCE(market,''), match_method, score,
	       COALESCE(reason,''), COALESCE(matched_terms_json,'[]'), status, created_at, updated_at
	FROM stockv2_news_link_candidates
`

func scanRawNews(row rowScanner) (StockV2RawNews, error) {
	var item StockV2RawNews
	var publishedAt sql.NullTime
	var rawPayloadJSON string
	if err := row.Scan(
		&item.ID, &item.Source, &item.SourceID, &item.Language, &item.Title,
		&item.Content, &item.Snippet, &publishedAt, &item.FetchedAt,
		&rawPayloadJSON, &item.ContentHash, &item.DedupeKey, &item.Quality, &item.Status,
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

func scanNewsEvent(row rowScanner) (StockV2NewsEvent, error) {
	var event StockV2NewsEvent
	var tagsJSON, topicsJSON string
	if err := row.Scan(
		&event.ID, &event.RawNewsID, &event.Title, &event.Summary, &event.Snippet,
		&event.Language, &event.Source, &event.EventTime, &event.Importance, &tagsJSON,
		&topicsJSON, &event.DedupeKey, &event.Quality, &event.Status, &event.CreatedAt,
		&event.UpdatedAt,
	); err != nil {
		return event, err
	}
	event.Tags = unmarshalStrings(tagsJSON)
	event.Topics = unmarshalStrings(topicsJSON)
	return event, nil
}

func scanNewsLinkCandidate(row rowScanner) (StockV2NewsLinkCandidate, error) {
	var item StockV2NewsLinkCandidate
	var matchedTermsJSON string
	if err := row.Scan(
		&item.ID, &item.NewsEventID, &item.Symbol, &item.Market, &item.MatchMethod,
		&item.Score, &item.Reason, &matchedTermsJSON, &item.Status, &item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return item, err
	}
	item.MatchedTerms = unmarshalStrings(matchedTermsJSON)
	return item, nil
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

func newsEventFilterSQL(filter NewsEventListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	addNewsStringFilter(&where, &args, "source", filter.Source)
	addNewsStringFilter(&where, &args, "language", filter.Language)
	addNewsStringFilter(&where, &args, "status", filter.Status)
	addNewsStringFilter(&where, &args, "quality", filter.Quality)
	addNewsTimeWindow(&where, &args, "event_time", filter.Since, filter.Until)
	return strings.Join(where, " AND "), args
}

func newsLinkCandidateFilterSQL(filter NewsLinkCandidateListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	addNewsStringFilter(&where, &args, "news_event_id", filter.NewsEventID)
	addNewsStringFilter(&where, &args, "symbol", filter.Symbol)
	addNewsStringFilter(&where, &args, "market", filter.Market)
	addNewsStringFilter(&where, &args, "match_method", filter.MatchMethod)
	addNewsStringFilter(&where, &args, "status", filter.Status)
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

func nullableNewsTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
