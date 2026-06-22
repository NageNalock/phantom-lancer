package stockv2

import (
	"context"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	newsIngestMinInterval = 5 * time.Second
	newsBackoffBase       = 30 * time.Second
	newsBackoffMax        = 15 * time.Minute
	defaultNewsBatchLimit = 50
)

func (s *Service) RunNewsIngestJob(ctx context.Context, source string) (NewsPipelineRunResult, error) {
	adapter := s.newsAdapter(source)
	if adapter == nil {
		return NewsPipelineRunResult{Source: source, Status: NewsSourceStatusFailed}, ErrNewsSourceAdapterNotFound
	}
	source = adapter.SourceName()
	result := NewsPipelineRunResult{Source: source}
	state := s.currentNewsSourceState(ctx, source)
	now := time.Now()

	if !state.BackoffUntil.IsZero() && state.BackoffUntil.After(now) {
		result.Status = NewsSourceStatusBackoff
		result.ErrorMessage = state.LastError
		return result, nil
	}
	if !state.LastFetchAt.IsZero() && now.Sub(state.LastFetchAt) < newsIngestMinInterval {
		result.Status = NewsSourceStatusRateLimited
		result.ErrorMessage = "rate limited"
		return result, nil
	}

	fetch, err := adapter.FetchSince(ctx, NewsSourceCursor{Cursor: state.Cursor, Since: state.LastSuccessAt})
	result.FetchedAt = fetch.FetchedAt
	if result.FetchedAt.IsZero() {
		result.FetchedAt = now
	}
	if fetch.Disabled {
		state.Enabled = false
		state.Status = NewsSourceStatusDisabled
		state.LastFetchAt = result.FetchedAt
		state.UpdatedAt = now
		_ = s.store.UpsertNewsSourceState(ctx, state)
		result.Status = NewsSourceStatusDisabled
		return result, nil
	}
	if err != nil {
		state = failedNewsSourceState(state, err, now)
		_ = s.store.UpsertNewsSourceState(ctx, state)
		result.Status = NewsSourceStatusFailed
		result.ErrorMessage = state.LastError
		return result, err
	}

	before, err := s.store.CountRawNews(ctx, RawNewsListFilter{Source: source})
	if err != nil {
		return result, err
	}
	for _, payload := range fetch.Items {
		req, normErr := adapter.NormalizeRawPayload(payload)
		if normErr != nil {
			continue
		}
		req.Source = source
		if req.FetchedAt.IsZero() {
			req.FetchedAt = result.FetchedAt
		}
		if _, err := s.CreateRawNews(ctx, req); err != nil {
			return result, err
		}
	}
	after, err := s.store.CountRawNews(ctx, RawNewsListFilter{Source: source})
	if err != nil {
		return result, err
	}
	result.FetchedCount = len(fetch.Items)
	result.RawInsertedCount = after - before
	if result.RawInsertedCount < 0 {
		result.RawInsertedCount = 0
	}
	result.Cursor = state.Cursor
	result.NextCursor = fetch.NextCursor
	if result.NextCursor == "" {
		result.NextCursor = result.FetchedAt.UTC().Format(time.RFC3339Nano)
	}

	state.Enabled = true
	state.Status = NewsSourceStatusIdle
	state.Cursor = result.NextCursor
	state.LastFetchAt = result.FetchedAt
	state.LastSuccessAt = result.FetchedAt
	state.LastError = ""
	state.LastErrorAt = time.Time{}
	state.ConsecutiveFailures = 0
	state.BackoffUntil = time.Time{}
	state.RawNewsCount += result.RawInsertedCount
	if err := s.store.UpsertNewsSourceState(ctx, state); err != nil {
		return result, err
	}
	result.Status = NewsSourceStatusIdle
	return result, nil
}

func (s *Service) RunNewsProcessingBatch(ctx context.Context, source string, rawLimit, eventLimit int) (NewsPipelineRunResult, error) {
	source = normalizeNewsSourceName(source)
	if rawLimit <= 0 {
		rawLimit = defaultNewsBatchLimit
	}
	if eventLimit <= 0 {
		eventLimit = defaultNewsBatchLimit
	}
	result := NewsPipelineRunResult{Source: source, Status: NewsSourceStatusIdle, FetchedAt: time.Now()}

	rawItems, err := s.store.ListRawNews(ctx, RawNewsListFilter{Source: source, Status: NewsStatusNew, Limit: rawLimit})
	if err != nil {
		return result, err
	}
	for _, raw := range rawItems {
		_, createErr := s.CreateNewsEvent(ctx, newsEventRequestFromRawNews(raw))
		if createErr != nil {
			_ = s.store.UpdateRawNewsStatus(ctx, raw.ID, NewsStatusFailed)
			continue
		}
		if err := s.store.UpdateRawNewsStatus(ctx, raw.ID, NewsStatusProcessed); err != nil {
			return result, err
		}
		result.NormalizedCount++
	}

	if s.newsLinker != nil {
		events, err := s.store.ListNewsEvents(ctx, NewsEventListFilter{Source: source, Status: NewsStatusNew, Limit: eventLimit})
		if err != nil {
			return result, err
		}
		for _, event := range events {
			candidates, linkErr := s.newsLinker.LinkNewsEvent(ctx, event)
			if linkErr != nil {
				_ = s.store.UpdateNewsEventStatus(ctx, event.ID, NewsStatusFailed)
				continue
			}
			for _, candidate := range candidates {
				candidate.NewsEventID = event.ID
				if _, err := s.UpsertNewsLinkCandidate(ctx, candidate); err != nil {
					return result, err
				}
				result.LinkCandidateCount++
			}
			if err := s.store.UpdateNewsEventStatus(ctx, event.ID, NewsStatusProcessed); err != nil {
				return result, err
			}
		}
	}

	state := s.currentNewsSourceState(ctx, source)
	state.Status = NewsSourceStatusIdle
	state.NewsEventCount += result.NormalizedCount
	state.LinkCandidateCount += result.LinkCandidateCount
	if err := s.store.UpsertNewsSourceState(ctx, state); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) RunNewsPipelineOnce(ctx context.Context, source string) (NewsPipelineRunResult, error) {
	ingest, err := s.RunNewsIngestJob(ctx, source)
	if err != nil {
		return ingest, err
	}
	if ingest.Status == NewsSourceStatusDisabled || ingest.Status == NewsSourceStatusBackoff || ingest.Status == NewsSourceStatusRateLimited {
		return ingest, nil
	}
	process, err := s.RunNewsProcessingBatch(ctx, source, defaultNewsBatchLimit, defaultNewsBatchLimit)
	ingest.NormalizedCount = process.NormalizedCount
	ingest.LinkCandidateCount = process.LinkCandidateCount
	if err != nil {
		ingest.Status = NewsSourceStatusFailed
		ingest.ErrorMessage = safelog.Text(err.Error(), 400)
	}
	return ingest, err
}

func (s *Service) GetNewsSourceState(ctx context.Context, source string) (NewsSourceState, bool, error) {
	return s.store.GetNewsSourceState(ctx, normalizeNewsSourceName(source))
}

func (s *Service) newsAdapter(source string) NewsSourceAdapter {
	source = normalizeNewsSourceName(source)
	if source == "" {
		source = NewsSourceJin10
	}
	if s.newsAdapters == nil {
		return nil
	}
	return s.newsAdapters[source]
}

func (s *Service) currentNewsSourceState(ctx context.Context, source string) NewsSourceState {
	state, ok, err := s.store.GetNewsSourceState(ctx, source)
	if err == nil && ok {
		return state
	}
	return NewsSourceState{Source: source, Enabled: true, Status: NewsSourceStatusIdle}
}

func failedNewsSourceState(state NewsSourceState, err error, now time.Time) NewsSourceState {
	state.Enabled = true
	state.Status = NewsSourceStatusBackoff
	state.LastFetchAt = now
	state.LastErrorAt = now
	state.LastError = safelog.Text(err.Error(), 400)
	state.ConsecutiveFailures++
	backoff := newsBackoffBase
	for i := 1; i < state.ConsecutiveFailures; i++ {
		backoff *= 2
		if backoff >= newsBackoffMax {
			backoff = newsBackoffMax
			break
		}
	}
	state.BackoffUntil = now.Add(backoff)
	return state
}

func newsEventRequestFromRawNews(raw StockV2RawNews) RequestCreateNewsEvent {
	eventTime := raw.PublishedAt
	if eventTime.IsZero() {
		eventTime = raw.FetchedAt
	}
	title := raw.Title
	if title == "" {
		title = raw.Snippet
	}
	if title == "" {
		title = raw.Content
	}
	return RequestCreateNewsEvent{
		RawNewsID: raw.ID,
		Title:     title,
		Summary:   raw.Snippet,
		Snippet:   raw.Snippet,
		Language:  raw.Language,
		Source:    raw.Source,
		EventTime: eventTime,
		Quality:   raw.Quality,
		Status:    NewsStatusNew,
	}
}

func normalizeNewsSourceName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return NewsSourceJin10
	}
	return source
}
