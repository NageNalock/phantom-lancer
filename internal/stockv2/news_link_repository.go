package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateNewsEvent(ctx context.Context, event NewsEvent) (NewsEvent, error) {
	now := time.Now()
	if event.ID == "" {
		event.ID = generateID()
	}
	if strings.TrimSpace(event.DedupeKey) != "" {
		existing, found, err := s.getNewsEventByDedupeKey(ctx, event.DedupeKey)
		if err != nil {
			return NewsEvent{}, err
		}
		if found {
			if strings.TrimSpace(newsEventEmbeddingText(existing)) != "" {
				if err := s.EnsureEmbeddingWork(ctx, EmbeddingObjectNewsEvent, existing.ID); err != nil {
					return NewsEvent{}, err
				}
			}
			return existing, nil
		}
	}
	if event.EventAt.IsZero() {
		event.EventAt = now
	}
	if event.LinkStatus == "" {
		event.LinkStatus = NewsEventLinkStatusPending
	}
	event.CreatedAt = now
	event.UpdatedAt = now
	_, err := s.assetDB().ExecContext(ctx, `
		INSERT INTO stockv2_news_events (
			id, raw_news_id, source, external_id, title, summary, content, url,
			quality_status, dedupe_key, link_status, event_at, link_processed_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.RawNewsID, event.Source, event.ExternalID, event.Title,
		event.Summary, event.Content, event.URL, event.QualityStatus, event.DedupeKey,
		event.LinkStatus, event.EventAt, nullableTime(event.LinkProcessedAt),
		event.CreatedAt, event.UpdatedAt)
	if err != nil {
		return NewsEvent{}, wrapError(err, "create news event")
	}
	if strings.TrimSpace(newsEventEmbeddingText(event)) != "" {
		if err := s.EnsureEmbeddingWork(ctx, EmbeddingObjectNewsEvent, event.ID); err != nil {
			return NewsEvent{}, err
		}
	}
	return event, nil
}

func (s *Store) getNewsEventByDedupeKey(ctx context.Context, dedupeKey string) (NewsEvent, bool, error) {
	row := s.assetDB().QueryRowContext(ctx, newsEventSelectSQL()+` WHERE dedupe_key = ?`, strings.TrimSpace(dedupeKey))
	event, err := scanNewsEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsEvent{}, false, nil
		}
		return NewsEvent{}, false, wrapError(err, "get news event by dedupe key")
	}
	return event, true, nil
}

func (s *Store) GetNewsEvent(ctx context.Context, id string) (NewsEvent, error) {
	row := s.assetDB().QueryRowContext(ctx, newsEventSelectSQL()+` WHERE id = ?`, strings.TrimSpace(id))
	event, err := scanNewsEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsEvent{}, ErrNewsEventNotFound
		}
		return NewsEvent{}, wrapError(err, "get news event")
	}
	return event, nil
}

func (s *Store) ListPendingNewsEvents(ctx context.Context, source string, limit int) ([]NewsEvent, error) {
	source = strings.TrimSpace(source)
	if source != "" {
		rows, err := s.assetDB().QueryContext(ctx, newsEventSelectSQL()+`
			WHERE link_status = ? AND source = ?
			ORDER BY event_at ASC, created_at ASC
			LIMIT ?
		`, NewsEventLinkStatusPending, source, normalizedPageLimit(limit, 200))
		if err != nil {
			return nil, wrapError(err, "list pending news events")
		}
		return scanRows(rows, scanNewsEvent, "scan news event", "iterate news events")
	}
	rows, err := s.assetDB().QueryContext(ctx, newsEventSelectSQL()+`
		WHERE link_status = ?
		ORDER BY event_at ASC, created_at ASC
		LIMIT ?
	`, NewsEventLinkStatusPending, normalizedPageLimit(limit, 200))
	if err != nil {
		return nil, wrapError(err, "list pending news events")
	}
	return scanRows(rows, scanNewsEvent, "scan news event", "iterate news events")
}

func (s *Store) ListNewsEvents(ctx context.Context, filter NewsEventListFilter) ([]NewsEvent, error) {
	where, args := newsEventWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 200), normalizedPageOffset(filter.Offset))
	rows, err := s.assetDB().QueryContext(ctx, newsEventSelectSQL()+where+`
		ORDER BY `+newsEventTimeSQL+` DESC, created_at DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list news events")
	}
	return scanRows(rows, scanNewsEvent, "scan news event", "iterate news events")
}

func (s *Store) CountNewsEvents(ctx context.Context, filter NewsEventListFilter) (int, error) {
	where, args := newsEventWhere(filter)
	var count int
	if err := s.assetDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_events`+where, args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count news events")
	}
	return count, nil
}

func (s *Store) UpdateNewsEventLinkStatus(ctx context.Context, id, status string, processedAt time.Time) error {
	if processedAt.IsZero() {
		processedAt = time.Now()
	}
	var result sql.Result
	err := retryStockV2TransientWriteConflict(ctx, func() error {
		var execErr error
		result, execErr = s.assetDB().ExecContext(ctx, `
			UPDATE stockv2_news_events
			SET link_status = ?, link_processed_at = ?, updated_at = ?
			WHERE id = ?
		`, status, processedAt, processedAt, id)
		return execErr
	})
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
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	candidate.UpdatedAt = now
	err := retryStockV2TransientWriteConflict(ctx, func() error {
		result, execErr := s.assetDB().ExecContext(ctx, `
			INSERT INTO stockv2_news_link_candidates (
				id, news_event_id, raw_news_id, symbol, market, instrument_name,
				match_method, score, reason, matched_terms_json, created_at, updated_at
			) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			  FROM stockv2_news_events
			  WHERE id=? AND COALESCE(context_status,'pending')<>?
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
			candidate.Reason, marshalProfileStrings(candidate.MatchedTerms), candidate.CreatedAt,
			candidate.UpdatedAt, candidate.NewsEventID, NewsEventContextCompacted)
		if execErr != nil {
			return execErr
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil {
			return rowsErr
		} else if rows == 0 {
			return ErrInvalidNewsLinkCandidate
		}
		return nil
	})
	if err != nil {
		return NewsLinkCandidate{}, wrapError(err, "upsert news link candidate")
	}
	row := s.assetDB().QueryRowContext(ctx, newsLinkCandidateSelectSQL()+`
		WHERE c.news_event_id = ? AND c.symbol = ?
	`, candidate.NewsEventID, candidate.Symbol)
	item, err := scanNewsLinkCandidate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsLinkCandidate{}, ErrNewsLinkCandidateNotFound
		}
		return NewsLinkCandidate{}, wrapError(err, "get upserted news link candidate")
	}
	return item, nil
}

func (s *Store) UpsertNewsLinkCandidates(ctx context.Context, candidates []NewsLinkCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	now := time.Now()
	var statement strings.Builder
	statement.WriteString(`
		INSERT INTO stockv2_news_link_candidates (
			id, news_event_id, raw_news_id, symbol, market, instrument_name,
			match_method, score, reason, matched_terms_json, created_at, updated_at
		)
		SELECT incoming.id, incoming.news_event_id, incoming.raw_news_id, incoming.symbol,
			incoming.market, incoming.instrument_name, incoming.match_method, incoming.score,
			incoming.reason, incoming.matched_terms_json, incoming.created_at, incoming.updated_at
		FROM (VALUES
	`)
	args := make([]any, 0, len(candidates)*12+1)
	for i := range candidates {
		if i > 0 {
			statement.WriteString(",")
		}
		statement.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		candidate := &candidates[i]
		if candidate.ID == "" {
			candidate.ID = generateID()
		}
		if candidate.CreatedAt.IsZero() {
			candidate.CreatedAt = now
		}
		candidate.UpdatedAt = now
		args = append(args, candidate.ID, candidate.NewsEventID, candidate.RawNewsID, candidate.Symbol,
			candidate.Market, candidate.InstrumentName, candidate.MatchMethod, candidate.Score,
			candidate.Reason, marshalProfileStrings(candidate.MatchedTerms), candidate.CreatedAt,
			candidate.UpdatedAt)
	}
	statement.WriteString(`
		) AS incoming (
			id, news_event_id, raw_news_id, symbol, market, instrument_name,
			match_method, score, reason, matched_terms_json, created_at, updated_at
		)
		INNER JOIN stockv2_news_events event ON event.id = incoming.news_event_id
		WHERE COALESCE(event.context_status, 'pending') <> ?
		ON CONFLICT(news_event_id, symbol) DO UPDATE SET
			raw_news_id = excluded.raw_news_id,
			market = excluded.market,
			instrument_name = excluded.instrument_name,
			match_method = excluded.match_method,
			score = excluded.score,
			reason = excluded.reason,
			matched_terms_json = excluded.matched_terms_json,
			updated_at = excluded.updated_at
	`)
	args = append(args, NewsEventContextCompacted)
	err := retryStockV2TransientWriteConflict(ctx, func() error {
		tx, err := s.assetDB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		result, err := tx.ExecContext(ctx, statement.String(), args...)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != int64(len(candidates)) {
			return ErrInvalidNewsLinkCandidate
		}
		return tx.Commit()
	})
	if err != nil {
		return wrapError(err, "upsert news link candidates")
	}
	return nil
}

func (s *Store) GetNewsLinkCandidate(ctx context.Context, id string) (NewsLinkCandidate, error) {
	row := s.assetDB().QueryRowContext(ctx, newsLinkCandidateSelectSQL()+` WHERE c.id = ?`, strings.TrimSpace(id))
	item, err := scanNewsLinkCandidate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewsLinkCandidate{}, ErrNewsLinkCandidateNotFound
		}
		return NewsLinkCandidate{}, wrapError(err, "get news link candidate")
	}
	return item, nil
}

func (s *Store) ListNewsLinkCandidates(ctx context.Context, filter NewsLinkCandidateListFilter) ([]NewsLinkCandidate, error) {
	where, args := newsLinkCandidateWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.assetDB().QueryContext(ctx, newsLinkCandidateSelectSQL()+where+`
		ORDER BY `+newsLinkCandidateEventTimeSQL+` DESC, c.created_at DESC, c.score DESC, c.symbol ASC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, wrapError(err, "list news link candidates")
	}
	return scanRows(rows, scanNewsLinkCandidate, "scan news link candidate", "iterate news link candidates")
}

func (s *Store) CountNewsLinkCandidates(ctx context.Context, filter NewsLinkCandidateListFilter) (int, error) {
	where, args := newsLinkCandidateWhere(filter)
	var count int
	if err := s.assetDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_link_candidates c LEFT JOIN stockv2_news_events e ON e.id = c.news_event_id`+where, args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count news link candidates")
	}
	return count, nil
}

type NewsLinkCandidateRetentionResult struct {
	DeletedLegacyMatcher int `json:"deletedLegacyMatcher"`
	DeletedLowConfidence int `json:"deletedLowConfidence"`
	DeletedTotal         int `json:"deletedTotal"`
}

func (s *Store) PruneNewsLinkCandidates(ctx context.Context, now time.Time) (NewsLinkCandidateRetentionResult, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var result NewsLinkCandidateRetentionResult
	deleted, err := s.deleteNewsLinkCandidates(ctx, `
		match_method = ? AND COALESCE(updated_at, created_at) < ?
	`, NewsLinkMatchProfileKeyword, now.AddDate(0, 0, -14))
	if err != nil {
		return result, err
	}
	result.DeletedLegacyMatcher = deleted

	deleted, err = s.deleteNewsLinkCandidates(ctx, `
		match_method IN (?, ?)
		AND score < ?
		AND COALESCE(updated_at, created_at) < ?
	`, NewsLinkMatchSemanticProfile, NewsLinkMatchKeyword,
		55.0, now.AddDate(0, 0, -3))
	if err != nil {
		return result, err
	}
	result.DeletedLowConfidence = deleted
	result.DeletedTotal = result.DeletedLegacyMatcher + result.DeletedLowConfidence
	return result, nil
}

func (s *Store) deleteNewsLinkCandidates(ctx context.Context, where string, args ...any) (int, error) {
	result, err := s.assetDB().ExecContext(ctx, `DELETE FROM stockv2_news_link_candidates WHERE `+where, args...)
	if err != nil {
		return 0, wrapError(err, "delete news link candidates")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, wrapError(err, "check deleted news link candidates")
	}
	if rows > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("delete news link candidates affected rows overflow: %d", rows)
	}
	return int(rows), nil
}

func newsEventSelectSQL() string {
	return `
		SELECT id, COALESCE(raw_news_id,''), source, COALESCE(external_id,''),
		       title, COALESCE(summary,''), COALESCE(content,''), COALESCE(url,''),
		       COALESCE(quality_status,''), COALESCE(dedupe_key,''), link_status,
		       event_at, link_processed_at, created_at, updated_at
		FROM stockv2_news_events`
}

const newsEventTimeSQL = `COALESCE(event_at, created_at)`

const newsLinkCandidateEventTimeSQL = `COALESCE(e.event_at, c.created_at)`

func newsEventWhere(filter NewsEventListFilter) (string, []any) {
	parts := make([]string, 0, 5)
	args := make([]any, 0, 5)
	add := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, column+" = ?")
			args = append(args, value)
		}
	}
	add("source", filter.Source)
	add("link_status", filter.LinkStatus)
	add("quality_status", filter.QualityStatus)
	if filter.ExcludeCompacted {
		parts = append(parts, "COALESCE(context_status, 'pending') <> ?")
		args = append(args, NewsEventContextCompacted)
	}
	addNewsTimeWindow(&parts, &args, newsEventTimeSQL, filter.Since, filter.Until)
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		parts = append(parts, "(LOWER(title) LIKE ? OR LOWER(summary) LIKE ? OR LOWER(content) LIKE ? OR LOWER(external_id) LIKE ?)")
		args = append(args, pattern, pattern, pattern, pattern)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
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
		SELECT c.id, c.news_event_id, COALESCE(c.raw_news_id,''),
		       COALESCE(e.title,''), COALESCE(e.source,''), e.event_at,
		       c.symbol,
		       COALESCE(c.market,''), COALESCE(c.instrument_name,''), c.match_method,
		       c.score, COALESCE(c.reason,''), c.matched_terms_json,
		       c.created_at, c.updated_at
		FROM stockv2_news_link_candidates c
		LEFT JOIN stockv2_news_events e ON e.id = c.news_event_id`
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
	add("e.source", filter.Source)
	add("c.symbol", filter.Symbol)
	add("c.market", filter.Market)
	add("c.match_method", filter.MatchMethod)
	addNewsTimeWindow(&parts, &args, newsLinkCandidateEventTimeSQL, filter.Since, filter.Until)
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		parts = append(parts, "(LOWER(c.symbol) LIKE ? OR LOWER(c.instrument_name) LIKE ? OR LOWER(c.reason) LIKE ? OR LOWER(e.title) LIKE ? OR LOWER(e.summary) LIKE ?)")
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func scanNewsLinkCandidate(row rowScanner) (NewsLinkCandidate, error) {
	var item NewsLinkCandidate
	var matchedTermsJSON string
	var newsEventAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.NewsEventID,
		&item.RawNewsID,
		&item.NewsEventTitle,
		&item.NewsEventSource,
		&newsEventAt,
		&item.Symbol,
		&item.Market,
		&item.InstrumentName,
		&item.MatchMethod,
		&item.Score,
		&item.Reason,
		&matchedTermsJSON,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return NewsLinkCandidate{}, err
	}
	item.MatchedTerms = unmarshalProfileStrings(matchedTermsJSON)
	if newsEventAt.Valid {
		item.NewsEventAt = newsEventAt.Time
	}
	return item, nil
}
