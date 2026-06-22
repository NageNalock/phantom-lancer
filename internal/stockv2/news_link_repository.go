package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) CreateNewsEvent(ctx context.Context, event NewsEvent) (NewsEvent, error) {
	now := time.Now()
	if event.ID == "" {
		event.ID = generateID()
	}
	if event.EventAt.IsZero() {
		event.EventAt = now
	}
	if event.LinkStatus == "" {
		event.LinkStatus = NewsEventLinkStatusPending
	}
	event.CreatedAt = now
	event.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_events (
			id, raw_news_id, source, external_id, title, summary, content, url,
			quality_status, dedupe_key, link_status, event_at, link_processed_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.RawNewsID, event.Source, event.ExternalID, event.Title,
		event.Summary, event.Content, event.URL, event.QualityStatus, event.DedupeKey,
		event.LinkStatus, event.EventAt, nullableNewsTime(event.LinkProcessedAt),
		event.CreatedAt, event.UpdatedAt)
	if err != nil {
		return NewsEvent{}, wrapError(err, "create news event")
	}
	return event, nil
}

func (s *Store) GetNewsEvent(ctx context.Context, id string) (NewsEvent, error) {
	row := s.db.QueryRowContext(ctx, newsEventSelectSQL()+` WHERE id = ?`, strings.TrimSpace(id))
	event, err := scanNewsEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsEvent{}, ErrNewsEventNotFound
		}
		return NewsEvent{}, wrapError(err, "get news event")
	}
	return event, nil
}

func (s *Store) ListPendingNewsEvents(ctx context.Context, limit int) ([]NewsEvent, error) {
	rows, err := s.db.QueryContext(ctx, newsEventSelectSQL()+`
		WHERE link_status = ?
		ORDER BY event_at ASC, created_at ASC
		LIMIT ?
	`, NewsEventLinkStatusPending, normalizedNewsBatchLimit(limit))
	if err != nil {
		return nil, wrapError(err, "list pending news events")
	}
	defer rows.Close()
	return collectNewsEvents(rows)
}

func (s *Store) UpdateNewsEventLinkStatus(ctx context.Context, id, status string, processedAt time.Time) error {
	if processedAt.IsZero() {
		processedAt = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_news_events
		SET link_status = ?, link_processed_at = ?, updated_at = ?
		WHERE id = ?
	`, status, processedAt, processedAt, id)
	if err != nil {
		return wrapError(err, "update news event link status")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check news event affected rows")
	}
	if rows == 0 {
		return ErrNewsEventNotFound
	}
	return nil
}

func (s *Store) UpsertNewsLinkCandidate(ctx context.Context, candidate NewsLinkCandidate) (NewsLinkCandidate, error) {
	now := time.Now()
	if candidate.ID == "" {
		candidate.ID = generateID()
	}
	if candidate.MonitorStatus == "" {
		candidate.MonitorStatus = NewsLinkMonitorStatusPending
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	candidate.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_link_candidates (
			id, news_event_id, raw_news_id, symbol, market, instrument_name,
			match_method, score, reason, matched_terms_json, monitor_status,
			monitor_hit_id, monitored_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(news_event_id, symbol) DO UPDATE SET
			raw_news_id = excluded.raw_news_id,
			market = excluded.market,
			instrument_name = excluded.instrument_name,
			match_method = excluded.match_method,
			score = excluded.score,
			reason = excluded.reason,
			matched_terms_json = excluded.matched_terms_json,
			updated_at = excluded.updated_at
	`, candidate.ID, candidate.NewsEventID, candidate.RawNewsID, candidate.Symbol,
		candidate.Market, candidate.InstrumentName, candidate.MatchMethod, candidate.Score,
		candidate.Reason, marshalProfileStrings(candidate.MatchedTerms), candidate.MonitorStatus,
		nullableNewsString(candidate.MonitorHitID), nullableNewsTime(candidate.MonitoredAt),
		candidate.CreatedAt, candidate.UpdatedAt)
	if err != nil {
		return NewsLinkCandidate{}, wrapError(err, "upsert news link candidate")
	}
	return candidate, nil
}

func (s *Store) GetNewsLinkCandidate(ctx context.Context, id string) (NewsLinkCandidate, error) {
	row := s.db.QueryRowContext(ctx, newsLinkCandidateSelectSQL()+` WHERE c.id = ?`, strings.TrimSpace(id))
	item, err := scanNewsLinkCandidate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsLinkCandidate{}, ErrNewsLinkCandidateNotFound
		}
		return NewsLinkCandidate{}, wrapError(err, "get news link candidate")
	}
	return item, nil
}

func (s *Store) ListPendingNewsLinkCandidates(ctx context.Context, limit int) ([]NewsLinkCandidate, error) {
	rows, err := s.db.QueryContext(ctx, newsLinkCandidateSelectSQL()+`
		WHERE COALESCE(c.monitor_status, ?) = ?
		ORDER BY c.updated_at ASC, c.score DESC, c.symbol ASC
		LIMIT ?
	`, NewsLinkMonitorStatusPending, NewsLinkMonitorStatusPending, normalizedNewsCandidateLimit(limit))
	if err != nil {
		return nil, wrapError(err, "list pending news link candidates")
	}
	defer rows.Close()
	return collectNewsLinkCandidates(rows)
}

func (s *Store) MarkNewsLinkCandidateMonitorStatus(ctx context.Context, id, status, monitorHitID string, monitoredAt time.Time) error {
	if monitoredAt.IsZero() {
		monitoredAt = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_news_link_candidates
		SET monitor_status = ?, monitor_hit_id = ?, monitored_at = ?, updated_at = ?
		WHERE id = ?
	`, status, nullableNewsString(monitorHitID), monitoredAt, monitoredAt, strings.TrimSpace(id))
	if err != nil {
		return wrapError(err, "mark news link candidate monitor status")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check news link candidate affected rows")
	}
	if rows == 0 {
		return ErrNewsLinkCandidateNotFound
	}
	return nil
}

func (s *Store) ListNewsLinkCandidates(ctx context.Context, filter NewsLinkCandidateListFilter) ([]NewsLinkCandidate, error) {
	where, args := newsLinkCandidateWhere(filter)
	args = append(args, normalizedNewsCandidateLimit(filter.Limit), normalizedStockProfileOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, newsLinkCandidateSelectSQL()+where+`
		ORDER BY score DESC, updated_at DESC, symbol ASC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list news link candidates")
	}
	defer rows.Close()
	return collectNewsLinkCandidates(rows)
}

func collectNewsLinkCandidates(rows *sql.Rows) ([]NewsLinkCandidate, error) {
	items := make([]NewsLinkCandidate, 0)
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

func newsEventSelectSQL() string {
	return `
		SELECT id, COALESCE(raw_news_id,''), source, COALESCE(external_id,''),
		       title, COALESCE(summary,''), COALESCE(content,''), COALESCE(url,''),
		       COALESCE(quality_status,''), COALESCE(dedupe_key,''), link_status,
		       event_at, link_processed_at, created_at, updated_at
		FROM stockv2_news_events`
}

func collectNewsEvents(rows *sql.Rows) ([]NewsEvent, error) {
	items := make([]NewsEvent, 0)
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

func scanNewsEvent(row rowScanner) (NewsEvent, error) {
	var event NewsEvent
	var linkProcessedAt sql.NullTime
	if err := row.Scan(
		&event.ID,
		&event.RawNewsID,
		&event.Source,
		&event.ExternalID,
		&event.Title,
		&event.Summary,
		&event.Content,
		&event.URL,
		&event.QualityStatus,
		&event.DedupeKey,
		&event.LinkStatus,
		&event.EventAt,
		&linkProcessedAt,
		&event.CreatedAt,
		&event.UpdatedAt,
	); err != nil {
		return NewsEvent{}, err
	}
	if linkProcessedAt.Valid {
		event.LinkProcessedAt = linkProcessedAt.Time
	}
	return event, nil
}

func newsLinkCandidateSelectSQL() string {
	return `
		SELECT c.id, c.news_event_id, COALESCE(c.raw_news_id,''), c.symbol,
		       COALESCE(c.market,''), COALESCE(c.instrument_name,''), c.match_method,
		       c.score, COALESCE(c.reason,''), c.matched_terms_json,
		       COALESCE(c.monitor_status,''), COALESCE(c.monitor_hit_id,''), c.monitored_at,
		       c.created_at, c.updated_at
		FROM stockv2_news_link_candidates c`
}

func newsLinkCandidateWhere(filter NewsLinkCandidateListFilter) (string, []any) {
	parts := make([]string, 0, 5)
	args := make([]any, 0, 5)
	add := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, column+" = ?")
			args = append(args, value)
		}
	}
	add("c.news_event_id", filter.NewsEventID)
	add("c.raw_news_id", filter.RawNewsID)
	add("c.symbol", filter.Symbol)
	add("c.market", filter.Market)
	add("c.match_method", filter.MatchMethod)
	add("c.monitor_status", filter.MonitorStatus)
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func scanNewsLinkCandidate(row rowScanner) (NewsLinkCandidate, error) {
	var item NewsLinkCandidate
	var matchedTermsJSON string
	var monitoredAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.NewsEventID,
		&item.RawNewsID,
		&item.Symbol,
		&item.Market,
		&item.InstrumentName,
		&item.MatchMethod,
		&item.Score,
		&item.Reason,
		&matchedTermsJSON,
		&item.MonitorStatus,
		&item.MonitorHitID,
		&monitoredAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return NewsLinkCandidate{}, err
	}
	item.MatchedTerms = unmarshalProfileStrings(matchedTermsJSON)
	if monitoredAt.Valid {
		item.MonitoredAt = monitoredAt.Time
	}
	return item, nil
}

func normalizedNewsBatchLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizedNewsCandidateLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func nullableNewsTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
