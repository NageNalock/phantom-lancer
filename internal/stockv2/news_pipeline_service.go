package stockv2

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	newsIngestMinInterval = 5 * time.Second
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
	if !state.Enabled {
		result.Status = NewsSourceStatusDisabled
		return result, nil
	}

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
		if s.log != nil {
			s.log.Warn("stockv2 news ingest failed", "source", source, "cursor", safelog.Text(state.Cursor, 120), "since", state.LastSuccessAt.Format(time.RFC3339Nano), "consecutive_failures", state.ConsecutiveFailures, "backoff_until", state.BackoffUntil.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 300))
		}
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
			if s.log != nil {
				s.log.Warn("stockv2 raw news create failed", "source", source, "source_id", safelog.Text(req.SourceID, 120), "fetched_at", req.FetchedAt.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 300))
			}
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
		_, createErr := s.CreateNewsEvent(ctx, newsEventFromRawNews(raw))
		if createErr != nil {
			_ = s.store.UpdateRawNewsStatus(ctx, raw.ID, NewsStatusFailed)
			if s.log != nil {
				s.log.Warn(
					"stockv2 raw news processing failed",
					"source", source,
					"raw_news_id", raw.ID,
					"source_id", safelog.Text(raw.SourceID, 80),
					"error", safelog.Text(createErr.Error(), 240),
				)
			}
			continue
		}
		if err := s.store.UpdateRawNewsStatus(ctx, raw.ID, NewsStatusProcessed); err != nil {
			return result, err
		}
		result.NormalizedCount++
	}

	events, err := s.store.ListPendingNewsEvents(ctx, source, eventLimit)
	if err != nil {
		return result, err
	}
	var snapshot newsLinkMatchSnapshot
	if s.newsLinker == nil && len(events) > 0 {
		snapshot, err = s.loadNewsLinkMatchSnapshot(ctx)
		if err != nil {
			return result, err
		}
	}
	for _, event := range events {
		if s.newsLinker == nil {
			candidates, linkErr := s.linkNewsEventWithSnapshot(ctx, event, snapshot)
			if linkErr != nil {
				_ = s.store.UpdateNewsEventLinkStatus(ctx, event.ID, NewsEventLinkStatusFailed, time.Now())
				if s.log != nil {
					s.log.Warn("stockv2 news event link failed", "source", source, "news_event_id", event.ID, "raw_news_id", event.RawNewsID, "event_source", event.Source, "event_title", safelog.Text(event.Title, 160), "linker", "default", "error", safelog.Text(linkErr.Error(), 300))
				}
				continue
			}
			result.LinkCandidateCount += len(candidates)
			continue
		}
		candidates, linkErr := s.newsLinker.LinkNewsEvent(ctx, event)
		if linkErr != nil {
			_ = s.store.UpdateNewsEventLinkStatus(ctx, event.ID, NewsEventLinkStatusFailed, time.Now())
			if s.log != nil {
				s.log.Warn("stockv2 news event link failed", "source", source, "news_event_id", event.ID, "raw_news_id", event.RawNewsID, "event_source", event.Source, "event_title", safelog.Text(event.Title, 160), "linker", "custom", "error", safelog.Text(linkErr.Error(), 300))
			}
			continue
		}
		for i := range candidates {
			candidates[i].NewsEventID = event.ID
			if candidates[i].RawNewsID == "" {
				candidates[i].RawNewsID = event.RawNewsID
			}
		}
		if err := s.store.UpsertNewsLinkCandidates(ctx, candidates); err != nil {
			if s.log != nil {
				s.log.Warn("stockv2 news link candidates save failed", "source", source, "news_event_id", event.ID, "raw_news_id", event.RawNewsID, "candidate_count", len(candidates), "error", safelog.Text(err.Error(), 300))
			}
			return result, err
		}
		result.LinkCandidateCount += len(candidates)
		status := NewsEventLinkStatusLinked
		if len(candidates) == 0 {
			status = NewsEventLinkStatusNoCandidate
		}
		if err := s.store.UpdateNewsEventLinkStatus(ctx, event.ID, status, time.Now()); err != nil {
			return result, err
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
	source = normalizeNewsSourceName(source)
	if !s.tryStartNewsPipelineRun() {
		return NewsPipelineRunResult{
			Source:       source,
			Status:       NewsSourceStatusRateLimited,
			FetchedAt:    time.Now(),
			ErrorMessage: "news pipeline already running",
		}, nil
	}
	defer s.finishNewsPipelineRun()

	state := s.currentNewsSourceState(ctx, source)
	now := time.Now()
	state.Status = NewsSourceStatusRunning
	state.LastRunAt = now
	state.LastRunStatus = NewsSourceStatusRunning
	state.LastRunError = ""
	_ = s.store.UpsertNewsSourceState(ctx, state)

	ingest, err := s.RunNewsIngestJob(ctx, source)
	if err != nil {
		s.recordNewsPipelineRunResult(ctx, source, ingest, err)
		return ingest, err
	}
	if ingest.Status == NewsSourceStatusDisabled || ingest.Status == NewsSourceStatusBackoff || ingest.Status == NewsSourceStatusRateLimited {
		s.recordNewsPipelineRunResult(ctx, source, ingest, nil)
		return ingest, nil
	}
	state = s.currentNewsSourceState(ctx, source)
	process, err := s.RunNewsProcessingBatch(ctx, source, state.BatchLimit, state.ProcessLimit)
	ingest.NormalizedCount = process.NormalizedCount
	ingest.LinkCandidateCount = process.LinkCandidateCount
	if err != nil {
		ingest.Status = NewsSourceStatusFailed
		ingest.ErrorMessage = safelog.Text(err.Error(), 400)
	}
	s.recordNewsPipelineRunResult(ctx, source, ingest, err)
	return ingest, err
}

func (s *Service) tryStartNewsPipelineRun() bool {
	s.newsPipelineMu.Lock()
	defer s.newsPipelineMu.Unlock()
	if s.newsPipelineRun {
		return false
	}
	s.newsPipelineRun = true
	return true
}

func (s *Service) finishNewsPipelineRun() {
	s.newsPipelineMu.Lock()
	s.newsPipelineRun = false
	s.newsPipelineMu.Unlock()
}

func (s *Service) GetNewsSourceState(ctx context.Context, source string) (NewsSourceState, bool, error) {
	return s.store.GetNewsSourceState(ctx, normalizeNewsSourceName(source))
}

func (s *Service) ListNewsSourceOverviews(ctx context.Context) ([]NewsSourceOverview, error) {
	sources := []string{NewsSourceJin10, NewsSourceFinancialJuice}
	out := make([]NewsSourceOverview, 0, len(sources))
	for _, source := range sources {
		state := s.currentNewsSourceState(ctx, source)
		out = append(out, s.newsSourceOverview(ctx, source, state))
	}
	return out, nil
}

func (s *Service) UpdateNewsSourceConfig(ctx context.Context, source string, patch NewsSourceConfigPatch) (NewsSourceOverview, error) {
	source = normalizeNewsSourceName(source)
	if s.newsAdapter(source) == nil {
		return NewsSourceOverview{}, ErrNewsSourceAdapterNotFound
	}
	if err := s.updateNewsSourceCredential(ctx, source, patch); err != nil {
		return NewsSourceOverview{}, err
	}
	state := s.currentNewsSourceState(ctx, source)
	if patch.Enabled != nil {
		state.Enabled = *patch.Enabled
	}
	if patch.PollIntervalSeconds != nil {
		state.PollIntervalSeconds = *patch.PollIntervalSeconds
	}
	if patch.JitterSeconds != nil {
		state.JitterSeconds = *patch.JitterSeconds
	}
	if patch.BatchLimit != nil {
		state.BatchLimit = *patch.BatchLimit
	}
	if patch.ProcessLimit != nil {
		state.ProcessLimit = *patch.ProcessLimit
	}
	if patch.BackoffBaseSeconds != nil {
		state.BackoffBaseSeconds = *patch.BackoffBaseSeconds
	}
	if patch.BackoffMaxSeconds != nil {
		state.BackoffMaxSeconds = *patch.BackoffMaxSeconds
	}
	state = normalizeNewsSourceStateDefaults(state)
	if patch.NextRunAt != nil {
		state.NextRunAt = *patch.NextRunAt
	} else if state.NextRunAt.IsZero() || state.NextRunAt.Before(time.Now()) {
		state.NextRunAt = nextNewsRunAt(state, time.Now())
	}
	if !state.Enabled {
		state.Status = NewsSourceStatusDisabled
		state.NextRunAt = time.Time{}
	} else if state.Status == "" || state.Status == NewsSourceStatusDisabled {
		state.Status = NewsSourceStatusIdle
	}
	if err := s.store.UpsertNewsSourceState(ctx, state); err != nil {
		return NewsSourceOverview{}, err
	}
	if state.Enabled {
		s.StartBackground(context.Background())
	} else if !s.settings.AutoUpdateEnabled && !s.settings.BaseProfileAutoMaintainEnabled && !s.hasEnabledNewsSources(ctx) {
		s.StopBackground()
	}
	return s.newsSourceOverview(ctx, source, state), nil
}

func (s *Service) newsSourceOverview(ctx context.Context, source string, state NewsSourceState) NewsSourceOverview {
	configured, reason := s.newsSourceConfigured(ctx, source)
	overview := NewsSourceOverview{State: state, Configured: configured, Reason: reason}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return overview
	}
	switch normalizeNewsSourceName(source) {
	case NewsSourceFinancialJuice:
		overview.CredentialSet = settings.FinancialJuiceCookieSet
	}
	return overview
}

func (s *Service) updateNewsSourceCredential(ctx context.Context, source string, patch NewsSourceConfigPatch) error {
	clear := patch.ClearCredential != nil && *patch.ClearCredential
	input := ""
	if patch.CredentialInput != nil {
		input = strings.TrimSpace(*patch.CredentialInput)
	}
	if !clear && input == "" {
		return nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	switch normalizeNewsSourceName(source) {
	case NewsSourceJin10:
		return ErrNewsSourceCredentialUnsupported
	case NewsSourceFinancialJuice:
		if clear {
			settings.FinancialJuiceEndpoint = ""
			settings.FinancialJuiceCookie = ""
		}
		if input != "" {
			cfg, err := ParseFinancialJuiceCredentialInput(input)
			if err != nil {
				return err
			}
			settings.FinancialJuiceEndpoint = cfg.Endpoint
			settings.FinancialJuiceCookie = cfg.Cookie
		}
	default:
		return ErrNewsSourceAdapterNotFound
	}
	settings.FinancialJuiceCookieSet = strings.TrimSpace(settings.FinancialJuiceCookie) != "" || financialJuiceEndpointHasCredential(settings.FinancialJuiceEndpoint)
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		return wrapError(err, "save news source credential")
	}
	s.settings = settings
	return nil
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
	return normalizeNewsSourceStateDefaults(NewsSourceState{Source: source, Enabled: true, Status: NewsSourceStatusIdle, JitterSeconds: 60})
}

func failedNewsSourceState(state NewsSourceState, err error, now time.Time) NewsSourceState {
	state = normalizeNewsSourceStateDefaults(state)
	state.Enabled = true
	state.Status = NewsSourceStatusBackoff
	state.LastFetchAt = now
	state.LastErrorAt = now
	state.LastError = safelog.Text(err.Error(), 400)
	state.ConsecutiveFailures++
	backoff := time.Duration(state.BackoffBaseSeconds) * time.Second
	maxBackoff := time.Duration(state.BackoffMaxSeconds) * time.Second
	for i := 1; i < state.ConsecutiveFailures; i++ {
		backoff *= 2
		if backoff >= maxBackoff {
			backoff = maxBackoff
			break
		}
	}
	state.BackoffUntil = now.Add(backoff)
	return state
}

func (s *Service) recordNewsPipelineRunResult(ctx context.Context, source string, result NewsPipelineRunResult, runErr error) {
	state := s.currentNewsSourceState(ctx, source)
	wasBackoff := state.Status == NewsSourceStatusBackoff
	state.Status = result.Status
	state.LastRunAt = time.Now()
	state.LastRunStatus = result.Status
	if runErr != nil {
		state.LastRunError = safelog.Text(runErr.Error(), 400)
	} else {
		state.LastRunError = safelog.Text(result.ErrorMessage, 400)
	}
	if result.Status == NewsSourceStatusFailed && runErr != nil && !wasBackoff {
		state = failedNewsSourceState(state, runErr, state.LastRunAt)
	}
	if !state.BackoffUntil.IsZero() && state.BackoffUntil.After(state.LastRunAt) {
		state.NextRunAt = state.BackoffUntil
	} else {
		state.NextRunAt = nextNewsRunAt(state, state.LastRunAt)
	}
	_ = s.store.UpsertNewsSourceState(ctx, state)
}

func nextNewsRunAt(state NewsSourceState, now time.Time) time.Time {
	state = normalizeNewsSourceStateDefaults(state)
	delay := time.Duration(state.PollIntervalSeconds) * time.Second
	if state.JitterSeconds > 0 {
		delay += time.Duration(rand.Intn(state.JitterSeconds+1)) * time.Second
	}
	return now.Add(delay)
}

func (s *Service) runNewsSourceScheduler(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	s.tickNewsSourceScheduler(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickNewsSourceScheduler(ctx)
		}
	}
}

func (s *Service) tickNewsSourceScheduler(ctx context.Context) {
	now := time.Now()
	for _, source := range []string{NewsSourceJin10, NewsSourceFinancialJuice} {
		state, ok, err := s.store.GetNewsSourceState(ctx, source)
		if err != nil || !ok || !state.Enabled || state.Status == NewsSourceStatusRunning {
			continue
		}
		if !state.BackoffUntil.IsZero() && state.BackoffUntil.After(now) {
			if state.NextRunAt.IsZero() || state.NextRunAt.After(state.BackoffUntil) {
				state.NextRunAt = state.BackoffUntil
				_ = s.store.UpsertNewsSourceState(ctx, state)
			}
			continue
		}
		if state.NextRunAt.IsZero() {
			state.NextRunAt = nextNewsRunAt(state, now)
			_ = s.store.UpsertNewsSourceState(ctx, state)
			continue
		}
		if state.NextRunAt.After(now) {
			continue
		}
		if configured, _ := s.newsSourceConfigured(ctx, source); !configured {
			state.Status = NewsSourceStatusDisabled
			state.NextRunAt = nextNewsRunAt(state, now)
			_ = s.store.UpsertNewsSourceState(ctx, state)
			continue
		}
		if !s.tryStartBackgroundHeavyWork() {
			continue
		}
		go func(source string) {
			defer s.finishBackgroundHeavyWork()
			if _, err := s.RunNewsPipelineOnce(context.Background(), source); err != nil && s.log != nil {
				s.log.Warn("scheduled news pipeline failed", "source", source, "error", safelog.Text(err.Error(), 240))
			}
		}(source)
	}
}

func (s *Service) hasEnabledNewsSources(ctx context.Context) bool {
	for _, source := range []string{NewsSourceJin10, NewsSourceFinancialJuice} {
		state, ok, err := s.store.GetNewsSourceState(ctx, source)
		if err == nil && ok && state.Enabled {
			return true
		}
	}
	return false
}

func (s *Service) newsSourceConfigured(ctx context.Context, source string) (bool, string) {
	switch normalizeNewsSourceName(source) {
	case NewsSourceJin10:
		if s.newsAdapter(NewsSourceJin10) == nil {
			return false, "金十适配器不可用"
		}
		return true, ""
	case NewsSourceFinancialJuice:
		settings, err := s.GetSettings(ctx)
		if err != nil {
			return false, "读取设置失败"
		}
		if !settings.FinancialJuiceCookieSet {
			return false, "FinancialJuice 凭据未配置"
		}
		return true, ""
	default:
		return s.newsAdapter(source) != nil, ""
	}
}

func newsEventFromRawNews(raw StockV2RawNews) NewsEvent {
	eventTime := clampFutureNewsPublishedAt(raw.PublishedAt, raw.FetchedAt)
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
	return NewsEvent{
		RawNewsID:     raw.ID,
		Source:        raw.Source,
		ExternalID:    raw.SourceID,
		Title:         title,
		Summary:       raw.Snippet,
		Content:       raw.Content,
		URL:           raw.URL,
		QualityStatus: raw.Quality,
		DedupeKey:     raw.DedupeKey,
		LinkStatus:    NewsEventLinkStatusPending,
		EventAt:       eventTime,
	}
}

func normalizeNewsSourceName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return NewsSourceJin10
	}
	return source
}
