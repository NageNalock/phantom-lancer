package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: five seconds keeps the single-owner backfill moving without
	// turning this internal scheduler cadence into a second owner-tuned queue.
	newsContextSchedulerInterval = 5 * time.Second
	// ponytail: news-context aggregation performs mandatory public research and
	// emits a large audited result. This is a per-Agent-attempt deadline: a
	// progressing four-hour window may contain many batches and must not expire
	// merely because their combined duration exceeds one model call.
	newsContextAgentTimeout = 30 * time.Minute
	newsContextAgentTaskTTL = newsContextAgentTimeout + time.Minute
	// ponytail: a canceled model request still needs one short, bounded storage
	// window to finalize its AgentRun and avoid a false concurrent-running row.
	newsContextFinalizeTimeout = 45 * time.Second
	// ponytail: embeddings are repairable derived state. Bound the inline
	// best-effort refresh so it cannot consume the authoritative result
	// finalizer or fail an otherwise committed aggregation batch.
	newsContextIndexSyncTimeout  = 30 * time.Second
	newsContextTimeoutRetryLimit = 2
	// ponytail: two short exponential waits absorb transient provider failures
	// without turning one bad window into a hot retry loop.
	newsContextAutoRetryBaseDelay    = time.Minute
	newsContextSeedPageSize          = 500
	newsContextInputTextLimit        = 60_000
	newsContextAdditionalPromptLimit = 2_000
	// ponytail: normal scheduled aggregation owns a rolling one-day safety
	// window. Re-reading only pending or first-deferred rows is cheap, closes
	// ingestion/window-boundary races, and leaves older history to the explicit
	// backfill workflow instead of silently starting an unbounded catch-up.
	newsContextRealtimeCatchupLookback = 24 * time.Hour
	// ponytail: Codex CLI exited without submitting a 20-item result even when
	// its prompt was smaller than a successful batch; the automatic 10-item
	// retry completed. Fixed protocol limits avoid paying for known-unreliable
	// result coverage. Thread output is denser, so keep its limit lower; raise
	// either only after measured runs show reliable submit_result coverage.
	newsContextCLIEventBatchSize  = 10
	newsContextCLIThreadBatchSize = 8
	// ponytail: production 6-event runs still emitted invalid 12 KB
	// submit_result JSON about half the time, while observed 3-event retries
	// completed. Keep this fixed protocol limit until measured adaptive sizing
	// is needed.
	newsContextDeepSeekEventBatchSize  = 3
	newsContextDeepSeekThreadBatchSize = 12
)

func defaultNewsContextConfig() NewsContextConfig {
	return NewsContextConfig{
		ID:                      NewsContextConfigIDDefault,
		Enabled:                 false,
		AutoCleanupEnabled:      false,
		HourlyEnabled:           true,
		FourHourEnabled:         true,
		DailyEnabled:            true,
		HourlyIntervalSeconds:   3600,
		FourHourIntervalSeconds: 4 * 3600,
		DailyIntervalSeconds:    24 * 3600,
		CleanupGraceSeconds:     24 * 3600,
		AgentTimeoutSeconds:     int(newsContextAgentTimeout / time.Second),
		TimeoutRetryLimit:       newsContextTimeoutRetryLimit,
		RetryBackoffSeconds:     int(newsContextAutoRetryBaseDelay / time.Second),
		ReviewTimeoutSeconds:    int(execDefaultTimeout / time.Second),
		SchedulerPollSeconds:    int(newsContextSchedulerInterval / time.Second),
		UpdatedAt:               time.Now(),
	}
}

func normalizeNewsContextConfig(cfg NewsContextConfig) NewsContextConfig {
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = NewsContextConfigIDDefault
	}
	// ponytail: these values define the product's semantic hierarchy, not tuning
	// knobs. Keeping them fixed avoids a second configurable windowing system.
	cfg.HourlyIntervalSeconds = 3600
	cfg.FourHourIntervalSeconds = 4 * 3600
	cfg.DailyIntervalSeconds = 24 * 3600
	// ponytail: these are one coherent safety policy. Making only one value
	// editable would desynchronize process cleanup, task TTL, and retry behavior.
	cfg.AgentTimeoutSeconds = int(newsContextAgentTimeout / time.Second)
	cfg.TimeoutRetryLimit = newsContextTimeoutRetryLimit
	cfg.RetryBackoffSeconds = int(newsContextAutoRetryBaseDelay / time.Second)
	cfg.ReviewTimeoutSeconds = int(execDefaultTimeout / time.Second)
	cfg.SchedulerPollSeconds = int(newsContextSchedulerInterval / time.Second)
	if !validNewsContextCleanupGrace(cfg.CleanupGraceSeconds) {
		cfg.CleanupGraceSeconds = 24 * 3600
	}
	cfg.AdditionalResearchPrompt = strings.TrimSpace(cfg.AdditionalResearchPrompt)
	return cfg
}

func (s *Service) GetNewsContextConfig(ctx context.Context) (NewsContextConfig, error) {
	cfg, err := s.store.GetNewsContextConfig(ctx)
	if err == nil {
		return normalizeNewsContextConfig(cfg), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return NewsContextConfig{}, err
	}
	created, err := s.store.UpsertNewsContextConfig(ctx, defaultNewsContextConfig())
	return normalizeNewsContextConfig(created), err
}

func (s *Service) UpdateNewsContextConfig(ctx context.Context, cfg NewsContextConfig) (NewsContextConfig, error) {
	current, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return NewsContextConfig{}, err
	}
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = current.ID
	}
	cfg = normalizeNewsContextConfig(cfg)
	if cfg.Enabled && !cfg.FourHourEnabled {
		return NewsContextConfig{}, ErrInvalidNewsContextInput
	}
	if cfg.AutoCleanupEnabled && (!cfg.Enabled || !cfg.DailyEnabled) {
		return NewsContextConfig{}, ErrInvalidNewsContextInput
	}
	updated, err := s.store.UpsertNewsContextConfig(ctx, cfg)
	if err != nil {
		return NewsContextConfig{}, err
	}
	if updated.Enabled {
		s.StartBackground(context.Background())
	}
	return normalizeNewsContextConfig(updated), nil
}

func (s *Service) PatchNewsContextConfig(ctx context.Context, req RequestUpdateNewsContextConfig) (NewsContextConfig, error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return NewsContextConfig{}, err
	}
	wasEnabled := cfg.Enabled
	wasAutoCleanupEnabled := cfg.AutoCleanupEnabled
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.AutoCleanupEnabled != nil {
		cfg.AutoCleanupEnabled = *req.AutoCleanupEnabled
	}
	if req.HourlyEnabled != nil {
		cfg.HourlyEnabled = *req.HourlyEnabled
	}
	if req.FourHourEnabled != nil {
		cfg.FourHourEnabled = *req.FourHourEnabled
	}
	if req.DailyEnabled != nil {
		cfg.DailyEnabled = *req.DailyEnabled
	}
	if req.CleanupGraceSeconds != nil {
		if !validNewsContextCleanupGrace(*req.CleanupGraceSeconds) {
			return NewsContextConfig{}, ErrInvalidNewsContextInput
		}
		cfg.CleanupGraceSeconds = *req.CleanupGraceSeconds
	}
	if req.AdditionalResearchPrompt != nil {
		prompt := strings.TrimSpace(*req.AdditionalResearchPrompt)
		if utf8.RuneCountInString(prompt) > newsContextAdditionalPromptLimit {
			return NewsContextConfig{}, ErrInvalidNewsContextInput
		}
		cfg.AdditionalResearchPrompt = prompt
	}
	if cfg.Enabled && !cfg.FourHourEnabled {
		return NewsContextConfig{}, ErrInvalidNewsContextInput
	}
	if cfg.AutoCleanupEnabled && (!cfg.Enabled || !cfg.DailyEnabled) {
		return NewsContextConfig{}, ErrInvalidNewsContextInput
	}
	if req.Enabled != nil && *req.Enabled && !wasEnabled {
		for _, taskType := range []string{AgentTaskTypeNewsEventReview, AgentTaskTypePortfolioSentinel} {
			profile, profileErr := s.store.GetAgentTaskProfileByType(ctx, taskType)
			if profileErr != nil {
				return NewsContextConfig{}, fmt.Errorf("%w: required review model is not configured", ErrNewsContextPrerequisite)
			}
			if _, modelErr := s.resolveModel(ctx, profile); modelErr != nil {
				return NewsContextConfig{}, fmt.Errorf("%w: required review model is unavailable", ErrNewsContextPrerequisite)
			}
		}
		if _, _, embedErr := s.ensureEmbeddingModelReady(ctx); embedErr != nil {
			return NewsContextConfig{}, fmt.Errorf("%w: theme embedding is unavailable", ErrNewsContextPrerequisite)
		}
	}
	if req.AutoCleanupEnabled != nil && *req.AutoCleanupEnabled && !wasAutoCleanupEnabled {
		cutoff, err := newsContextCleanupCutoff(time.Now(), cfg.CleanupGraceSeconds, "")
		if err != nil {
			return NewsContextConfig{}, err
		}
		if err := s.validateNewsContextCleanupPrerequisites(ctx, true, cutoff); err != nil {
			return NewsContextConfig{}, err
		}
	}
	return s.UpdateNewsContextConfig(ctx, cfg)
}

func (s *Service) validateNewsContextCleanupPrerequisites(ctx context.Context, requireVerifiedMCP bool, cutoff time.Time) error {
	gate, err := s.newsContextCleanupGate(ctx, cutoff)
	if err != nil {
		return err
	}
	if gate.Blocked {
		return fmt.Errorf("%w: %s", ErrNewsContextPrerequisite, gate.Reason)
	}
	if _, _, err := s.ensureEmbeddingModelReady(ctx); err != nil {
		return fmt.Errorf("%w: theme embedding is unavailable", ErrNewsContextPrerequisite)
	}
	candidates, err := s.store.ListNewsEventsForContextCleanup(ctx, cutoff, "", 1)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	daily, err := s.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{
		WindowType: NewsContextWindowDaily, Status: NewsContextRunStatusCompleted,
		ReviewStatus: NewsContextReviewCompleted, Limit: 1,
	})
	if err != nil {
		return err
	}
	if len(daily) == 0 {
		return fmt.Errorf("%w: no completed daily aggregation and impact review", ErrNewsContextPrerequisite)
	}
	if requireVerifiedMCP {
		return s.validateNewsContextAutoCleanupSafety(ctx, cutoff)
	}
	return nil
}

func (s *Service) validateNewsContextAutoCleanupSafety(ctx context.Context, cutoff time.Time) error {
	// ponytail: reuse the exact cleanup safety decision instead of maintaining a
	// second unlock checklist. A fresh per-request cache still forces one real
	// semantic-search/detail round-trip for every unprotected current theme.
	ctx = context.WithValue(ctx, newsContextMCPVerificationCacheKey{}, newsContextMCPVerificationCache{})
	ctx = context.WithValue(ctx, newsContextCleanupBackfillPrecheckedKey{}, true)
	afterID := ""
	for {
		candidates, err := s.store.ListNewsEventsForContextCleanup(ctx,
			cutoff, afterID, newsContextCleanupBatchSize)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			afterID = candidate.Event.ID
			_, _, err := s.newsContextCleanupEligibility(ctx, candidate, cutoff, false)
			if err == nil {
				continue
			}
			var gateFailure newsContextCleanupGateFailure
			if errors.As(err, &gateFailure) {
				return fmt.Errorf("%w: %s", ErrNewsContextPrerequisite, gateFailure.Error())
			}
			return err
		}
		if len(candidates) < newsContextCleanupBatchSize {
			break
		}
	}
	gate, err := s.newsContextCleanupGate(ctx, cutoff)
	if err != nil {
		return err
	}
	if gate.Blocked {
		return fmt.Errorf("%w: historical news backfill changed during safety validation", ErrNewsContextPrerequisite)
	}
	return nil
}

func validNewsContextCleanupGrace(seconds int) bool {
	for _, days := range []int{1, 3, 7, 14, 30} {
		if seconds == days*24*3600 {
			return true
		}
	}
	return false
}

func (s *Service) tryStartNewsContextRun() bool {
	s.newsContextMu.Lock()
	defer s.newsContextMu.Unlock()
	if s.newsContextRun {
		return false
	}
	s.newsContextRun = true
	return true
}

func (s *Service) finishNewsContextRun() {
	s.newsContextMu.Lock()
	s.newsContextRun = false
	s.newsContextMu.Unlock()
}

func (s *Service) runNewsContextScheduler(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.reconcileNewsContextReviews(ctx)
			if err := s.startDueNewsContextRun(ctx, time.Now()); err != nil &&
				!errors.Is(err, ErrNewsContextFeatureDisabled) &&
				!errors.Is(err, ErrNewsContextAlreadyRunning) && s.log != nil {
				s.log.Warn("start scheduled news context run failed", "error", safelog.Text(err.Error(), 300))
			}
			if err := s.runNewsContextBackfillStep(ctx); err != nil &&
				!errors.Is(err, ErrNewsContextAlreadyRunning) && s.log != nil {
				s.log.Warn("advance news context backfill failed", "error", safelog.Text(err.Error(), 300))
			}
			if err := s.startDueNewsContextCleanup(ctx, time.Now()); err != nil &&
				!errors.Is(err, ErrNewsContextCleanupDisabled) &&
				!errors.Is(err, ErrNewsContextCleanupRunning) &&
				!errors.Is(err, ErrNewsContextAlreadyRunning) && s.log != nil {
				s.log.Warn("start scheduled news context cleanup failed", "error", safelog.Text(err.Error(), 300))
			}
			timer.Reset(newsContextSchedulerInterval)
		}
	}
}

func (s *Service) startDueNewsContextCleanup(ctx context.Context, now time.Time) error {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.AutoCleanupEnabled {
		return ErrNewsContextCleanupDisabled
	}
	// ponytail: an hourly cleanup check is sufficient because the safety grace is
	// at least one hour; no separate high-frequency queue is needed.
	if !cfg.LastCleanupAt.IsZero() && cfg.LastCleanupAt.Add(time.Hour).After(now) {
		return nil
	}
	s.newsContextMu.Lock()
	if !s.newsCleanupLastAttemptAt.IsZero() && s.newsCleanupLastAttemptAt.Add(time.Hour).After(now) {
		s.newsContextMu.Unlock()
		return nil
	}
	s.newsCleanupLastAttemptAt = now
	s.newsContextMu.Unlock()
	_, err = s.StartNewsContextCleanupRun(ctx, RequestStartNewsContextCleanup{RequestedBy: "system"})
	return err
}

func (s *Service) startDueNewsContextRun(ctx context.Context, now time.Time) error {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return ErrNewsContextFeatureDisabled
	}
	scheduleChanged := prepareNewsContextSchedule(&cfg, now)
	backfill, hasBlockingBackfill, err := s.store.GetBlockingNewsContextBackfill(ctx)
	if err != nil {
		return err
	}
	if hasBlockingBackfill && fastForwardNewsContextScheduleForBackfill(&cfg, backfill.CutoffAt) {
		scheduleChanged = true
	}
	if scheduleChanged {
		if cfg, err = s.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
			return err
		}
	}
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return err
	} else if running {
		return ErrNewsContextAlreadyRunning
	}
	for _, windowType := range []string{NewsContextWindowDaily, NewsContextWindowFourHour, NewsContextWindowHourly} {
		if !newsContextWindowEnabled(cfg, windowType) {
			continue
		}
		endAt := newsContextNextAt(cfg, windowType)
		if endAt.IsZero() || endAt.After(now) {
			continue
		}
		startAt, ok := newsContextScheduledWindow(windowType, endAt)
		if !ok {
			return ErrInvalidNewsContextInput
		}
		ready, impossible, err := s.newsContextParentWindowReadiness(ctx, cfg, windowType, startAt, endAt)
		if err != nil {
			return err
		}
		if impossible {
			setNewsContextNextAt(&cfg, windowType, firstCompletableNewsContextParentEnd(cfg, windowType))
			if cfg, err = s.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
				return err
			}
			continue
		}
		if !ready {
			continue
		}
		existing, err := s.store.getNewsContextRunByWindow(ctx, windowType, startAt, endAt)
		if err == nil {
			if existing.Status == NewsContextRunStatusCompleted || existing.Status == NewsContextRunStatusWaitingReview {
				setNewsContextNextAt(&cfg, windowType, nextNewsContextBoundary(windowType, endAt))
				if cfg, err = s.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
					return err
				}
			} else if existing.Status == NewsContextRunStatusPending {
				_, err = s.startNewsContextRun(ctx, RequestStartNewsContextRun{
					WindowType:  windowType,
					StartAt:     startAt.Format(time.RFC3339Nano),
					EndAt:       endAt.Format(time.RFC3339Nano),
					RequestedBy: "system",
				}, NewsContextTriggerScheduled, true)
				return err
			} else if existing.Status == NewsContextRunStatusFailed {
				due, scheduleErr := s.ensureNewsContextAutoRetryScheduled(ctx, &existing, now)
				if scheduleErr != nil {
					return scheduleErr
				}
				if due {
					_, err = s.retryNewsContextRun(ctx, existing.ID, true)
					return err
				}
			}
			// A terminal failure remains on its natural boundary so the missing
			// child window cannot be mistaken for completed parent coverage.
			continue
		}
		if !errors.Is(err, ErrNewsContextRunNotFound) {
			return err
		}
		_, err = s.startNewsContextRun(ctx, RequestStartNewsContextRun{
			WindowType:  windowType,
			StartAt:     startAt.Format(time.RFC3339Nano),
			EndAt:       endAt.Format(time.RFC3339Nano),
			RequestedBy: "system",
		}, NewsContextTriggerScheduled, true)
		return err
	}
	return nil
}

func (s *Service) StartNewsContextRun(ctx context.Context, req RequestStartNewsContextRun) (NewsContextRun, error) {
	return s.startNewsContextRun(ctx, req, NewsContextTriggerManual, true)
}

func (s *Service) startNewsContextRun(ctx context.Context, req RequestStartNewsContextRun, triggerType string, async bool) (NewsContextRun, error) {
	windowType := strings.TrimSpace(req.WindowType)
	if !validNewsContextWindowType(windowType) {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	if !s.tryStartNewsContextRun() {
		return NewsContextRun{}, ErrNewsContextAlreadyRunning
	}
	release := true
	defer func() {
		if release {
			s.finishNewsContextRun()
		}
	}()
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return NewsContextRun{}, err
	} else if running {
		return NewsContextRun{}, ErrNewsContextAlreadyRunning
	}
	endAt := parseNewsContextTime(req.EndAt)
	if endAt.IsZero() {
		endAt = time.Now()
	}
	startAt := parseNewsContextTime(req.StartAt)
	if startAt.IsZero() {
		startAt = endAt.Add(-newsContextWindowDuration(windowType))
	}
	if !endAt.After(startAt) {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	run, err := s.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType:    windowType,
		TriggerType:   triggerType,
		Status:        NewsContextRunStatusPending,
		Phase:         "collecting",
		WindowStart:   startAt,
		WindowEnd:     endAt,
		ReviewStatus:  NewsContextReviewNotRequired,
		CleanupStatus: NewsContextCleanupPending,
		RequestedBy:   strings.TrimSpace(req.RequestedBy),
	})
	if err != nil {
		return NewsContextRun{}, err
	}
	if run.Status != NewsContextRunStatusPending {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	run, err = s.preparePendingNewsContextRun(ctx, run)
	if err != nil {
		run.Status = NewsContextRunStatusFailed
		run.Phase = "collecting"
		run.ErrorMessage = safelog.Text(err.Error(), 500)
		run.FinishedAt = time.Now()
		s.scheduleNewsContextAutoRetry(context.Background(), &run, err, run.FinishedAt)
		_, _ = s.store.UpdateNewsContextRun(ctx, run)
		s.recordNewsContextRunFailure(ctx, run.WindowType, err)
		return run, err
	}
	if async {
		if !s.launchNewsContextWorker(run.ID, s.executeNewsContextRun) {
			return run, context.Canceled
		}
		release = false
		return run, nil
	}
	release = false
	s.executeNewsContextRun(ctx, run.ID)
	return s.store.GetNewsContextRun(ctx, run.ID)
}

func (s *Service) preparePendingNewsContextRun(ctx context.Context, run NewsContextRun) (NewsContextRun, error) {
	// A crash can leave a durable pending row after claims or manifest items were
	// written but before execution. Rebuilding a collecting manifest is lossless.
	if run.Status != NewsContextRunStatusPending {
		return run, ErrInvalidNewsContextInput
	}
	if err := s.store.ReleaseNewsContextEventClaims(ctx, run.ID); err != nil {
		return run, err
	}
	if err := s.store.ResetPendingNewsContextRunManifest(ctx, run.ID); err != nil {
		return run, err
	}
	var err error
	run, err = s.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		return run, err
	}
	if err := s.seedNewsContextRunItems(ctx, &run); err != nil {
		return run, err
	}
	run.Status = NewsContextRunStatusRunning
	switch run.WindowType {
	case NewsContextWindowHourly:
		run.Phase = newsContextRunPhaseCheckpoint
	case NewsContextWindowDaily:
		run.Phase = newsContextRunPhaseMaterialize
	default:
		run.Phase = newsContextRunPhaseAggregating
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	return s.store.UpdateNewsContextRun(ctx, run)
}

func (s *Service) seedNewsContextRunItems(ctx context.Context, run *NewsContextRun) error {
	switch run.WindowType {
	case NewsContextWindowHourly:
		// ponytail: the hour is now only a durable scheduling checkpoint. Raw
		// news remains pending until the enclosing four-hour model window owns it.
		return nil
	case NewsContextWindowDaily:
		sources, err := s.newsContextDailyInputVersions(ctx, *run)
		if err != nil {
			return err
		}
		versions, err := s.store.MaterializeNewsContextDailyVersions(ctx, run.ID, run.WindowEnd, sources)
		if err != nil {
			return err
		}
		if err := s.store.ReplaceNewsContextMaterializedThreadItems(ctx, run.ID, versions); err != nil {
			return err
		}
		run.InputCount = len(versions)
		run.ProcessedCount = len(versions)
		run.PendingCount = 0
		return nil
	}

	claimedEventIDs, historicalOnlyWindow, err := s.claimRealtimeNewsContextEvents(ctx, *run)
	if err != nil {
		return err
	}
	if err := s.store.RequeueNewsContextRunEventItems(ctx, run.ID, claimedEventIDs); err != nil {
		_ = s.store.ReleaseNewsContextEventClaims(ctx, run.ID)
		return err
	}
	run.InputCount += len(claimedEventIDs)
	if historicalOnlyWindow {
		// ponytail: a persisted empty checkpoint is enough to move the real-time
		// hierarchy past a backfill-owned window; it must not read old themes.
		run.PendingCount = run.InputCount
		return nil
	}
	run.PendingCount = run.InputCount
	return nil
}

func (s *Service) newsContextChildOutputVersions(ctx context.Context, parent NewsContextRun, childType string) ([]NewsThreadVersion, error) {
	latest := make(map[string]NewsThreadVersion)
	appendVersion := func(version NewsThreadVersion) {
		if version.ID == "" || version.ThreadID == "" || version.EffectiveAt.After(parent.WindowEnd) {
			return
		}
		current, found := latest[version.ThreadID]
		if !found || newsContextVersionAfter(version, current) {
			latest[version.ThreadID] = version
		}
	}
	for childStart := parent.WindowStart; childStart.Before(parent.WindowEnd); {
		childEnd := nextNewsContextBoundary(childType, childStart)
		if childEnd.After(parent.WindowEnd) {
			return nil, ErrInvalidNewsContextInput
		}
		child, err := s.store.getNewsContextRunByWindow(ctx, childType, childStart, childEnd)
		if err != nil {
			return nil, err
		}
		ids, err := s.store.ListNewsContextRunOutputVersionIDs(ctx, child.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			version, err := s.store.GetNewsThreadVersion(ctx, id)
			if err != nil {
				return nil, err
			}
			appendVersion(version)
		}
		for offset := 0; ; offset += newsContextSeedPageSize {
			created, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
				RunID: child.ID, Limit: newsContextSeedPageSize, Offset: offset,
			})
			if err != nil {
				return nil, err
			}
			for _, version := range created {
				appendVersion(version)
			}
			if len(created) < newsContextSeedPageSize {
				break
			}
		}
		childStart = childEnd
	}
	versions := make([]NewsThreadVersion, 0, len(latest))
	for _, version := range latest {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].ThreadID < versions[j].ThreadID
	})
	return versions, nil
}

func newsContextVersionAfter(left, right NewsThreadVersion) bool {
	return left.EffectiveAt.After(right.EffectiveAt) ||
		(left.EffectiveAt.Equal(right.EffectiveAt) && left.VersionNo > right.VersionNo) ||
		(left.EffectiveAt.Equal(right.EffectiveAt) && left.VersionNo == right.VersionNo && left.ID > right.ID)
}

func (s *Service) newsContextDailyInputVersions(ctx context.Context, run NewsContextRun) ([]NewsThreadVersion, error) {
	if backfill, finalReview, err := s.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID); err != nil {
		return nil, err
	} else if finalReview {
		return s.newsContextBackfillFinalInputVersions(ctx, backfill, run.WindowEnd)
	}
	if run.TriggerType == NewsContextTriggerScheduled {
		return s.newsContextChildOutputVersions(ctx, run, NewsContextWindowFourHour)
	}
	latest := make(map[string]NewsThreadVersion)
	for offset := 0; ; offset += newsContextSeedPageSize {
		page, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
			Since: run.WindowStart, Until: run.WindowEnd, Limit: newsContextSeedPageSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, version := range page {
			if current, found := latest[version.ThreadID]; !found || newsContextVersionAfter(version, current) {
				latest[version.ThreadID] = version
			}
		}
		if len(page) < newsContextSeedPageSize {
			break
		}
	}
	return sortedNewsContextVersions(latest), nil
}

func (s *Service) newsContextBackfillFinalInputVersions(ctx context.Context, backfill NewsContextBackfill, at time.Time) ([]NewsThreadVersion, error) {
	ids, err := s.store.ListNewsContextBackfillOutputVersionIDs(ctx, backfill.ID,
		NewsContextWindowDaily, backfill.RangeStartAt, backfill.CutoffAt)
	if err != nil {
		return nil, err
	}
	affected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		version, err := s.store.GetNewsThreadVersion(ctx, id)
		if err != nil {
			return nil, err
		}
		affected[version.ThreadID] = struct{}{}
	}
	latest := make(map[string]NewsThreadVersion, len(affected))
	for offset := 0; len(latest) < len(affected); offset += newsContextSeedPageSize {
		page, err := s.store.ListLatestNewsThreadVersionsAt(ctx, at, newsContextSeedPageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, version := range page {
			if _, ok := affected[version.ThreadID]; ok {
				latest[version.ThreadID] = version
			}
		}
		if len(page) < newsContextSeedPageSize {
			break
		}
	}
	return sortedNewsContextVersions(latest), nil
}

func sortedNewsContextVersions(latest map[string]NewsThreadVersion) []NewsThreadVersion {
	versions := make([]NewsThreadVersion, 0, len(latest))
	for _, version := range latest {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].ThreadID < versions[j].ThreadID })
	return versions
}

func (s *Service) claimRealtimeNewsContextEvents(ctx context.Context, run NewsContextRun) ([]string, bool, error) {
	if _, historical, err := s.store.NewsContextBackfillForRun(ctx, run.ID); err != nil {
		return nil, false, err
	} else if historical {
		ids, err := s.store.ClaimNewsContextEvents(ctx, run.ID, run.WindowStart, run.WindowEnd)
		return ids, false, err
	}
	if _, finalReview, err := s.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID); err != nil {
		return nil, false, err
	} else if finalReview {
		ids, err := s.store.ClaimNewsContextFinalReviewEvents(ctx, run.ID, run.WindowStart, run.WindowEnd)
		return ids, false, err
	}
	startAt := run.WindowStart
	for {
		cutoff, found, err := s.newsContextHistoricalOwnershipCutoff(ctx)
		if err != nil {
			return nil, false, err
		}
		if found {
			if !run.WindowEnd.After(cutoff) {
				return nil, true, nil
			}
			if startAt.Before(cutoff) {
				startAt = cutoff
			}
		}
		claim := s.store.ClaimNewsContextEvents
		if run.TriggerType == NewsContextTriggerScheduled {
			catchupStart := run.WindowStart.Add(-newsContextRealtimeCatchupLookback)
			if found && catchupStart.Before(cutoff) {
				catchupStart = cutoff
			}
			if catchupStart.Before(startAt) {
				startAt = catchupStart
			}
			claim = s.store.ClaimRealtimeNewsContextEvents
		}
		ids, err := claim(ctx, run.ID, startAt, run.WindowEnd)
		if err != nil {
			return nil, false, err
		}
		latest, latestFound, err := s.newsContextHistoricalOwnershipCutoff(ctx)
		if err != nil {
			_ = s.store.ReleaseNewsContextEventClaims(ctx, run.ID)
			return nil, false, err
		}
		if latestFound && startAt.Before(latest) {
			// The owner may start a backfill between the cutoff read and the
			// market-data claim. Release and retry at the newer ownership boundary.
			if err := s.store.ReleaseNewsContextEventClaims(ctx, run.ID); err != nil {
				return nil, false, err
			}
			startAt = latest
			continue
		}
		return ids, false, nil
	}
}

func (s *Service) newsContextHistoricalOwnershipCutoff(ctx context.Context) (time.Time, bool, error) {
	var cutoff time.Time
	backfill, blocking, err := s.store.GetBlockingNewsContextBackfill(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	if blocking {
		cutoff = backfill.CutoffAt
	}
	completedCutoff, completed, err := s.store.GetLatestCompletedNewsContextBackfillCutoff(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	if completed && (!blocking || completedCutoff.After(cutoff)) {
		cutoff = completedCutoff
	}
	// ponytail: one durable high-water mark is sufficient. News before it is
	// owned by a new backfill, while the exact cutoff remains realtime-owned.
	return cutoff, blocking || completed, nil
}

func (s *Service) executeNewsContextRun(ctx context.Context, runID string) {
	s.executeNewsContextRunWithBatchTimeout(ctx, runID, newsContextAgentTimeout)
}

func (s *Service) executeNewsContextRunWithBatchTimeout(ctx context.Context, runID string, batchTimeout time.Duration) {
	defer s.finishNewsContextRun()
	run, err := s.store.GetNewsContextRun(ctx, runID)
	if err != nil {
		return
	}
	fail := func(cause error) {
		if s.newsContextWorkerShutdownCanceled(ctx) {
			return
		}
		run.Status = NewsContextRunStatusFailed
		run.ErrorMessage = safelog.Text(cause.Error(), 500)
		run.FinishedAt = time.Now()
		run.Phase = "failed"
		s.scheduleNewsContextAutoRetry(context.Background(), &run, cause, run.FinishedAt)
		_, _ = s.store.UpdateNewsContextRun(context.Background(), run)
		s.recordNewsContextRunFailure(context.Background(), run.WindowType, cause)
	}
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		fail(err)
		return
	}
	if run.WindowType == NewsContextWindowHourly {
		if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
			fail(err)
		}
		return
	}
	if _, finalReview, err := s.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID); err != nil {
		fail(err)
		return
	} else if run.WindowType == NewsContextWindowDaily && finalReview {
		// The backfill's explicit paged indexing gate owns all current-theme
		// embeddings immediately after this deterministic materialization.
		if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
			fail(err)
		}
		return
	}
	if run.WindowType == NewsContextWindowDaily {
		// ponytail: daily versions are authoritative deterministic snapshots,
		// while embeddings are repairable derived state. Give the inline refresh
		// one bounded chance, then leave failed/pending cursors to maintenance;
		// the backfill final-review path above keeps its explicit full index gate.
		indexCtx, cancelIndex := context.WithTimeout(ctx, newsContextIndexSyncTimeout)
		indexErr := s.repairNewsContextRunEmbeddings(indexCtx, run.ID)
		cancelIndex()
		if indexErr != nil && s.log != nil {
			s.log.Warn("news context daily index deferred",
				"context_run_id", run.ID,
				"error", safelog.Text(indexErr.Error(), 300),
			)
		}
		if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
			fail(err)
		}
		return
	}
	if err := s.repairNewsContextRunEmbeddings(ctx, run.ID); err != nil {
		fail(err)
		return
	}
	// ponytail: once a provider reports an exhausted quota, keep this worker on
	// the configured fallback instead of paying for the same known failure on
	// every remaining batch in the run.
	fallbackOnly := false
	adaptiveBatchLimit := 0
	for {
		items, err := s.nextNewsContextRunItems(ctx, run.ID)
		if err != nil {
			fail(err)
			return
		}
		items, err = s.limitNewsContextBatchForProvider(ctx, items)
		if err != nil {
			fail(err)
			return
		}
		items = limitNewsContextBatchItems(items, adaptiveBatchLimit)
		if len(items) == 0 {
			break
		}
		if err := s.executeNewsContextBatchWithRetry(
			ctx, &run, cfg, items, &fallbackOnly, &adaptiveBatchLimit, batchTimeout,
		); err != nil {
			fail(err)
			return
		}
		run, err = s.store.GetNewsContextRun(ctx, run.ID)
		if err != nil {
			fail(err)
			return
		}
	}
	if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
		fail(err)
	}
}

func (s *Service) limitNewsContextBatchForProvider(ctx context.Context, items []NewsContextRunItem) ([]NewsContextRunItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	profile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeNewsEventReview)
	if err != nil {
		return nil, err
	}
	if profile.ExecutionMode != AgentExecutionModeAPI {
		limit := newsContextCLIEventBatchSize
		if items[0].ObjectType == NewsContextRunItemThread {
			limit = newsContextCLIThreadBatchSize
		}
		return limitNewsContextBatchItems(items, limit), nil
	}
	model, err := s.resolveModel(ctx, profile)
	if err != nil {
		return nil, err
	}
	provider, err := s.store.GetAgentProviderProfile(ctx, model.ProviderID)
	if err != nil {
		return nil, err
	}
	if !isDeepSeekAPI(agentProviderBaseURL(provider), model.ModelName) {
		return items, nil
	}
	limit := newsContextDeepSeekEventBatchSize
	if items[0].ObjectType == NewsContextRunItemThread {
		limit = newsContextDeepSeekThreadBatchSize
	}
	return limitNewsContextBatchItems(items, limit), nil
}

func (s *Service) yieldNewsContextRunAfterFragment(ctx context.Context, runID string) error {
	run, err := s.store.GetNewsContextRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = NewsContextRunStatusPending
	run.CurrentAgentRunID = ""
	run.ErrorMessage = ""
	run.FinishedAt = time.Time{}
	run.Phase = "queued"
	_, err = s.store.UpdateNewsContextRun(ctx, run)
	return err
}

func (s *Service) nextNewsContextRunItems(ctx context.Context, runID string) ([]NewsContextRunItem, error) {
	items := make([]NewsContextRunItem, 0)
	inputCharacters := 0
	group := make([]NewsContextRunItem, 0)
	groupCharacters := 0
	groupKey := ""
	flushGroup := func() bool {
		if len(group) == 0 {
			return false
		}
		if len(items) > 0 && inputCharacters+groupCharacters > newsContextInputTextLimit {
			return true
		}
		items = append(items, group...)
		inputCharacters += groupCharacters
		group = group[:0]
		groupCharacters = 0
		return inputCharacters >= newsContextInputTextLimit
	}
	for offset := 0; ; offset += newsContextSeedPageSize {
		page, err := s.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
			RunID: runID, Status: NewsContextRunItemPending,
			Limit: newsContextSeedPageSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range page {
			characters, err := s.newsContextRunItemPromptCharacters(ctx, item)
			if err != nil {
				return nil, err
			}
			key := newsContextRunItemGroupKey(item)
			if groupKey != "" && key != groupKey && flushGroup() {
				return items, nil
			}
			if len(group) == 0 {
				groupKey = key
			}
			if characters > newsContextInputTextLimit {
				return nil, fmt.Errorf("%w: compact news context item exceeds prompt safety limit", ErrInvalidNewsContextInput)
			}
			if len(group) > 0 && groupCharacters+characters > newsContextInputTextLimit {
				if len(items) > 0 {
					return items, nil
				}
				// The remaining versions stay pending and the next persisted slice
				// continues the same stable theme using the prior slice via MCP.
				return group, nil
			}
			group = append(group, item)
			groupCharacters += characters
		}
		if len(page) < newsContextSeedPageSize {
			_ = flushGroup()
			return items, nil
		}
	}
}

func (s *Service) newsContextRunItemPromptCharacters(ctx context.Context, item NewsContextRunItem) (int, error) {
	var value any
	switch item.ObjectType {
	case NewsContextRunItemNewsEvent:
		event, err := s.store.GetNewsEvent(ctx, item.ObjectID)
		if err != nil {
			return 0, err
		}
		event.Title = safelog.Text(event.Title, 500)
		event.Summary = safelog.Text(event.Summary, 1500)
		event.Content = safelog.Text(event.Content, 4000)
		event.URL = sanitizeOpportunityURL(event.URL)
		value = event
	case NewsContextRunItemThread:
		if item.VersionID != "" {
			version, err := s.store.GetNewsThreadVersion(ctx, item.VersionID)
			if err != nil {
				return 0, err
			}
			value = compactNewsThreadForPrompt(historicalNewsThreadSnapshot(version))
		} else {
			thread, err := s.store.GetNewsThread(ctx, item.ObjectID)
			if err != nil {
				return 0, err
			}
			value = compactNewsThreadForPrompt(thread)
		}
	default:
		return 0, ErrInvalidNewsContextInput
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	// ponytail: character length is a conservative, dependency-free prompt
	// guard. The run manifest still covers every item across as many calls as needed.
	return utf8.RuneCount(raw) + 128, nil
}

func (s *Service) recordNewsContextRunFailure(ctx context.Context, _ string, cause error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return
	}
	cfg.LastRunAt = time.Now()
	cfg.LastError = safelog.Text(cause.Error(), 500)
	// Keep the failed natural window due. Skipping it would create a permanent
	// hole in every parent window built on top of it.
	_, _ = s.store.UpsertNewsContextConfig(ctx, cfg)
}

func (s *Service) executeNewsContextBatchWithRetry(
	ctx context.Context,
	run *NewsContextRun,
	cfg NewsContextConfig,
	items []NewsContextRunItem,
	fallbackOnly *bool,
	adaptiveBatchLimit *int,
	attemptTimeout time.Duration,
) error {
	batch := items
	for attempt := 0; attempt <= newsContextTimeoutRetryLimit; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		pack, err := s.buildNewsContextAggregationPack(ctx, *run, batch)
		if err != nil {
			return err
		}
		pack.AdditionalResearchPrompt = cfg.AdditionalResearchPrompt
		var resolution AgentTaskResolution
		if *fallbackOnly {
			resolution, err = s.resolveFallbackAgentTask(ctx, AgentTaskTypeNewsEventReview, "news_context_run", run.ID, "system")
		} else {
			resolution, err = s.ResolveAgentTask(ctx, AgentTaskTypeNewsEventReview, "news_context_run", run.ID, "system")
		}
		if err != nil {
			return err
		}
		if resolution.Run == nil || resolution.DecisionLedger == nil {
			return errors.New("no news context agent run created")
		}
		if _, err := s.store.MarkNewsContextRunItemsRunning(ctx, run.ID, resolution.Run.ID, newsContextRunItemObjectIDs(batch)); err != nil {
			return err
		}
		run.CurrentAgentRunID = resolution.Run.ID
		if *run, err = s.store.UpdateNewsContextRun(ctx, *run); err != nil {
			return err
		}
		attemptCtx := ctx
		cancelAttempt := func() {}
		if attemptTimeout > 0 {
			attemptCtx, cancelAttempt = context.WithTimeout(ctx, attemptTimeout)
		}
		finalAgentRun, _, output, execErr := s.executeNewsContextAgentRun(
			attemptCtx, *resolution.Run, *resolution.DecisionLedger, pack, resolution.ModelName,
		)
		attemptTimedOut := errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
		cancelAttempt()
		if finalAgentRun.Status == AgentRunStatusCompleted {
			// A valid submission persisted at the deadline is authoritative even
			// if the transport concurrently returned context cancellation.
			return nil
		}
		attemptErr := execErr
		if attemptTimedOut {
			if attemptErr == nil {
				attemptErr = context.DeadlineExceeded
			}
			attemptErr = fmt.Errorf("news context batch timed out after %s: %w", attemptTimeout, attemptErr)
		}
		if attemptErr == nil {
			attemptErr = errors.New(firstNonEmpty(finalAgentRun.ErrorMessage, "news context agent run failed"))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !*fallbackOnly && agentProviderUsageLimitFailure(attemptErr, output) {
			taskProfile, profileErr := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeNewsEventReview)
			if profileErr == nil {
				taskProfile.PrimaryModelID = ""
				fallbackModel, fallbackErr := s.resolveModel(ctx, taskProfile)
				if fallbackErr == nil && fallbackModel.ID != resolution.ModelID {
					if output != nil {
						if err := ensureExecutorProcessGroupStopped(output.ProcessGroupID); err != nil {
							return fmt.Errorf("clean failed news context process: %w", err)
						}
					}
					if err := s.store.ResetNewsContextRunItemsForAgent(ctx, run.ID, resolution.Run.ID); err != nil {
						return err
					}
					if s.log != nil {
						s.log.Warn("falling back news context agent after provider usage limit",
							"context_run_id", run.ID,
							"agent_run_id", resolution.Run.ID,
							"primary_model_id", resolution.ModelID,
							"fallback_model_id", fallbackModel.ID,
							"error", safelog.Text(attemptErr.Error(), 240),
						)
					}
					*fallbackOnly = true
					continue
				}
			}
		}
		if !retryableNewsContextBatchFailure(attemptErr, output) {
			return attemptErr
		}
		if output != nil {
			if err := ensureExecutorProcessGroupStopped(output.ProcessGroupID); err != nil {
				return fmt.Errorf("clean failed news context process: %w", err)
			}
		}
		if attempt == newsContextTimeoutRetryLimit {
			return attemptErr
		}
		if err := s.store.ResetNewsContextRunItemsForAgent(ctx, run.ID, resolution.Run.ID); err != nil {
			return err
		}
		next := shrinkNewsContextRetryBatch(batch)
		if adaptiveBatchLimit != nil && len(next) < len(batch) &&
			(*adaptiveBatchLimit == 0 || len(next) < *adaptiveBatchLimit) {
			// ponytail: the recoverable failure proves this size unsafe for the
			// current worker. Carry its smaller retry cap forward instead of
			// repeatedly paying for the same oversized provider failure.
			*adaptiveBatchLimit = len(next)
		}
		if s.log != nil {
			s.log.Warn("retrying news context batch after recoverable agent failure",
				"context_run_id", run.ID,
				"agent_run_id", resolution.Run.ID,
				"attempt", attempt+1,
				"max_attempts", newsContextTimeoutRetryLimit+1,
				"previous_item_count", len(batch),
				"next_item_count", len(next),
				"duration", outputDurationString(output),
				"error", safelog.Text(attemptErr.Error(), 240),
			)
		}
		batch = next
	}
	return errors.New("news context retry loop exhausted")
}

func retryableNewsContextBatchFailure(err error, output *AgentExecutorOutput) bool {
	if agentProviderUsageLimitFailure(err, output) {
		return false
	}
	if retryableNoSubmitTimeout(err, output) {
		return true
	}
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	apiRequest := output != nil && strings.HasPrefix(strings.TrimSpace(output.Command), "POST ")
	if apiRequest &&
		strings.Contains(message, "without submitting a valid result") &&
		strings.Contains(message, ErrInvalidNewsContextResult.Error()) {
		// ponytail: API-mode validation rejected the complete JSON before the
		// batch could mutate storage. Shrink and retry this exact safe boundary;
		// do not generalize arbitrary validation or persistence failures.
		return true
	}
	if apiRequest && (strings.Contains(message, "api returned http 502") ||
		(output.TimedOut && strings.Contains(message, "api request failed") &&
			strings.Contains(message, "context deadline exceeded"))) {
		// ponytail: an upstream API failure happened before the required submit
		// tool could mutate storage. Shrinking the same pending slice gives the
		// model room for tool results; keep cancellation, non-API, and post-submit
		// failures terminal.
		return true
	}
	if output != nil && (strings.Contains(message, "without submitting result") ||
		strings.Contains(message, "without submitting a result") ||
		strings.Contains(message, "no result submitted")) {
		// ponytail: a started Agent process that produced no result cannot have
		// mutated the batch. Retry only this explicit no-submit boundary; provider
		// preflight, storage, and dependency failures remain terminal.
		return true
	}
	// ponytail: malformed model output is safe to retry because the failed
	// result never reached ApplyNewsContextBatch. Keep storage and dependency
	// failures terminal instead of hiding them behind repeated Agent calls.
	return strings.Contains(err.Error(), "save news context result failed: "+ErrInvalidNewsContextResult.Error())
}

func agentProviderUsageLimitFailure(err error, output *AgentExecutorOutput) bool {
	message := ""
	if output != nil {
		message = agentProviderFailureMessage(output.StdoutTail)
	}
	if message == "" && err != nil {
		message = err.Error()
	}
	message = strings.ToLower(message)
	return strings.Contains(message, "usage limit") ||
		strings.Contains(message, "purchase more credits") ||
		strings.Contains(message, "insufficient balance") ||
		strings.Contains(message, "insufficient quota") ||
		strings.Contains(message, "api returned http 402")
}

func newsContextAutoRetryDelay(retryCount int) time.Duration {
	delay := newsContextAutoRetryBaseDelay
	for index := 0; index < retryCount && index < newsContextTimeoutRetryLimit; index++ {
		delay *= 2
	}
	return delay
}

func retryableNewsContextAttemptFailure(cause error) bool {
	if cause == nil {
		return false
	}
	message := strings.ToLower(cause.Error())
	if strings.Contains(message, "usage limit") || strings.Contains(message, "purchase more credits") ||
		strings.Contains(message, "insufficient balance") || strings.Contains(message, "insufficient quota") {
		return false
	}
	return errors.Is(cause, context.DeadlineExceeded) ||
		strings.Contains(message, "timed out") ||
		strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "without submitting") ||
		strings.Contains(message, "no result submitted") ||
		strings.Contains(message, "invalid news context result") ||
		strings.Contains(message, "invalid portfolio sentinel result") ||
		strings.Contains(message, "already running") ||
		strings.Contains(message, "interrupted by service restart") ||
		strings.Contains(message, "interrupted by service shutdown") ||
		strings.Contains(message, "api request failed") ||
		strings.Contains(message, "decode api response") ||
		strings.Contains(message, "response has no choices") ||
		strings.Contains(message, "api returned http 408") ||
		strings.Contains(message, "api returned http 429") ||
		strings.Contains(message, "api returned http 5")
}

func (s *Service) newsContextRunEligibleForAutoRetry(ctx context.Context, run NewsContextRun, cause error) bool {
	if run.RetryCount >= newsContextTimeoutRetryLimit {
		return false
	}
	retryable := retryableNewsContextAttemptFailure(cause)
	if !retryable && agentProviderUsageLimitFailure(cause, nil) {
		retryable = s.newsContextRunHasUnusedFallback(ctx, run)
	}
	if !retryable {
		return false
	}
	if _, historical, err := s.store.NewsContextBackfillForRun(ctx, run.ID); err != nil || historical {
		return false
	}
	if _, finalReview, err := s.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID); err != nil || finalReview {
		return false
	}
	return true
}

func (s *Service) newsContextRunHasUnusedFallback(ctx context.Context, run NewsContextRun) bool {
	if strings.TrimSpace(run.CurrentAgentRunID) == "" {
		return false
	}
	currentRun, err := s.store.GetAgentRun(ctx, run.CurrentAgentRunID)
	if err != nil {
		return false
	}
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeNewsEventReview)
	if err != nil {
		return false
	}
	taskProfile.PrimaryModelID = ""
	fallbackModel, err := s.resolveModel(ctx, taskProfile)
	return err == nil && fallbackModel.ID != currentRun.ModelID
}

func (s *Service) scheduleNewsContextAutoRetry(ctx context.Context, run *NewsContextRun, cause error, now time.Time) {
	run.NextRetryAt = time.Time{}
	if s.newsContextRunEligibleForAutoRetry(ctx, *run, cause) {
		run.NextRetryAt = now.Add(newsContextAutoRetryDelay(run.RetryCount))
	}
}

func (s *Service) ensureNewsContextAutoRetryScheduled(ctx context.Context, run *NewsContextRun, now time.Time) (bool, error) {
	if !run.NextRetryAt.IsZero() {
		return !run.NextRetryAt.After(now), nil
	}
	// ponytail: pre-migration failed rows have no retry timestamp. Only the
	// current natural boundary is inspected here, so one transient legacy
	// failure is resumed without scanning or reviving unrelated history.
	cause := errors.New(firstNonEmpty(run.ErrorMessage, "news context attempt failed"))
	if !s.newsContextRunEligibleForAutoRetry(ctx, *run, cause) {
		return false, nil
	}
	run.NextRetryAt = now
	updated, err := s.store.UpdateNewsContextRun(ctx, *run)
	if err != nil {
		return false, err
	}
	*run = updated
	return true, nil
}

func shrinkNewsContextRetryBatch(items []NewsContextRunItem) []NewsContextRunItem {
	if len(items) <= 1 {
		return items
	}
	end := (len(items) + 1) / 2
	boundaryKey := newsContextRunItemGroupKey(items[end-1])
	if newsContextRunItemGroupKey(items[end]) != boundaryKey {
		return items[:end]
	}
	start := end - 1
	for start > 0 && newsContextRunItemGroupKey(items[start-1]) == boundaryKey {
		start--
	}
	if start > 0 {
		return items[:start]
	}
	for end < len(items) && newsContextRunItemGroupKey(items[end]) == boundaryKey {
		end++
	}
	return items[:end]
}

func limitNewsContextBatchItems(items []NewsContextRunItem, limit int) []NewsContextRunItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	end := limit
	boundaryKey := newsContextRunItemGroupKey(items[end-1])
	for end < len(items) && newsContextRunItemGroupKey(items[end]) == boundaryKey {
		end++
	}
	return items[:end]
}

func newsContextRunItemGroupKey(item NewsContextRunItem) string {
	if item.ObjectType == NewsContextRunItemThread {
		return item.ObjectType + ":" + firstNonEmpty(item.ThreadID, item.ObjectID)
	}
	return item.ObjectType + ":" + item.ObjectID
}

func (s *Service) executeNewsContextAgentRun(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack NewsContextAggregationPack, modelName string) (AgentRun, AgentDecisionLedger, *AgentExecutorOutput, error) {
	if s.newsContextExecutor == nil {
		err := ErrAgentExecutorUnavailable
		finalizeCtx, cancelFinalize := newsContextFinalizeContext(ctx)
		s.finalizeAgentRun(finalizeCtx, run.ID, nil, err)
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(finalizeCtx, run.ID, ledger.ID)
		cancelFinalize()
		return finalRun, finalLedger, nil, err
	}
	running := run
	running.Status = AgentRunStatusRunning
	_, _ = s.store.UpdateAgentRun(ctx, running)
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", newsContextAgentTaskTTL)
	output, execErr := s.newsContextExecutor.ExecuteNewsContextAggregation(ctx, taskID, pack, modelName, run.ReasoningEffort)
	finalizeCtx, cancelFinalize := newsContextFinalizeContext(ctx)
	s.finalizeAgentRunWithOutput(finalizeCtx, run.ID, ledger.ID, taskID, output, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(finalizeCtx, run.ID, ledger.ID)
	cancelFinalize()
	return finalRun, finalLedger, output, execErr
}

func newsContextFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), newsContextFinalizeTimeout)
}

func (s *Service) buildNewsContextAggregationPack(ctx context.Context, run NewsContextRun, items []NewsContextRunItem) (NewsContextAggregationPack, error) {
	pack := NewsContextAggregationPack{
		RunID:       run.ID,
		WindowType:  run.WindowType,
		WindowStart: run.WindowStart,
		WindowEnd:   run.WindowEnd,
	}
	if _, historical, err := s.store.NewsContextBackfillForRun(ctx, run.ID); err != nil {
		return pack, err
	} else {
		pack.HistoricalReconstruction = historical
	}
	for _, item := range items {
		switch item.ObjectType {
		case NewsContextRunItemNewsEvent:
			event, err := s.store.GetNewsEvent(ctx, item.ObjectID)
			if err != nil {
				return pack, err
			}
			// ponytail: keep each item in the batch while bounding command-line prompt
			// size. The agent can use MCP/public search for more detail; later batches
			// continue until the persisted item set is exhausted.
			event.Title = safelog.Text(event.Title, 500)
			event.Summary = safelog.Text(event.Summary, 1500)
			event.Content = safelog.Text(event.Content, 4000)
			event.URL = sanitizeOpportunityURL(event.URL)
			pack.InputNewsEvents = append(pack.InputNewsEvents, event)
			if !pack.HistoricalReconstruction && newsContextEventRequiresResearch(event) {
				pack.RequiredResearch = true
				pack.ResearchReasons = append(pack.ResearchReasons, "重大、政策、公告或单一来源事项需要公开资料核实")
			}
		case NewsContextRunItemThread:
			if item.VersionID != "" {
				version, err := s.store.GetNewsThreadVersion(ctx, item.VersionID)
				if err != nil {
					return pack, err
				}
				pack.InputThreads = append(pack.InputThreads, compactNewsThreadForPrompt(historicalNewsThreadSnapshot(version)))
			} else {
				thread, err := s.store.GetNewsThread(ctx, item.ObjectID)
				if err != nil {
					return pack, err
				}
				pack.InputThreads = append(pack.InputThreads, compactNewsThreadForPrompt(thread))
			}
		default:
			return pack, ErrInvalidNewsContextInput
		}
	}
	pack.ResearchReasons = uniqueNonEmptyStrings(pack.ResearchReasons)
	return pack, nil
}

func newsContextEventRequiresResearch(event NewsEvent) bool {
	text := strings.ToLower(strings.Join([]string{event.Title, event.Summary, event.Content}, " "))
	for _, keyword := range []string{"政策", "监管", "公告", "财报", "停牌", "重组", "制裁", "关税", "supply", "filing", "regulation"} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func newsContextRunItemObjectIDs(items []NewsContextRunItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ObjectID)
	}
	return ids
}

func (s *Service) validateNewsContextTaskSubmission(
	ctx context.Context,
	taskID string,
	report NewsContextReport,
) error {
	entry, ok := s.agentTaskPool.getTask(taskID)
	if !ok {
		return ErrTaskNotFound
	}
	entry.mu.Lock()
	taskType := entry.taskType
	agentRunID := entry.agentRunID
	entry.mu.Unlock()
	if taskType != AgentTaskTypeNewsEventReview {
		return ErrInvalidNewsContextResult
	}
	agentRun, err := s.store.GetAgentRun(ctx, agentRunID)
	if err != nil {
		return err
	}
	if agentRun.TaskType != AgentTaskTypeNewsEventReview ||
		agentRun.TriggerObjectType != "news_context_run" ||
		strings.TrimSpace(agentRun.TriggerObjectID) == "" {
		return ErrInvalidNewsContextResult
	}
	logicalRun, err := s.store.GetNewsContextRun(ctx, agentRun.TriggerObjectID)
	if err != nil {
		return err
	}
	items, err := s.listAllNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: logicalRun.ID, AgentRunID: agentRun.ID, Status: NewsContextRunItemRunning,
	})
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%w: no running batch items for this task", ErrInvalidNewsContextResult)
	}
	normalizeNewsContextThreadReviewOutcomes(items, &report)
	if err := validateNewsContextReport(logicalRun, items, report); err != nil {
		return err
	}
	return s.validateNewsContextResearchAudit(ctx, logicalRun, items, report)
}

func (s *Service) ProcessNewsContextSubmittedResult(ctx context.Context, logicalRunID, agentRunID string, submitted AgentTaskSubmittedResult) (NewsContextBatchApplyResult, error) {
	logicalRun, err := s.store.GetNewsContextRun(ctx, logicalRunID)
	if err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	raw, err := json.Marshal(submitted.Result)
	if err != nil {
		return NewsContextBatchApplyResult{}, ErrInvalidNewsContextResult
	}
	var report NewsContextReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return NewsContextBatchApplyResult{}, fmt.Errorf("%w: %v", ErrInvalidNewsContextResult, err)
	}
	items, err := s.listAllNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: logicalRunID, AgentRunID: agentRunID, Status: NewsContextRunItemRunning,
	})
	if err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	ignoredThreadOutcomes := normalizeNewsContextThreadReviewOutcomes(items, &report)
	if ignoredThreadOutcomes > 0 && s.log != nil {
		s.log.Warn("ignored news context read-only thread outcomes outside batch",
			"context_run_id", logicalRunID,
			"agent_run_id", agentRunID,
			"ignored_count", ignoredThreadOutcomes,
		)
	}
	if err := validateNewsContextReport(logicalRun, items, report); err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	if err := s.validateNewsContextResearchAudit(ctx, logicalRun, items, report); err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	result, err := s.store.ApplyNewsContextBatch(ctx, logicalRunID, agentRunID, logicalRun.WindowType, report)
	if err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	_, historicalRun, lookupErr := s.store.NewsContextBackfillForRun(ctx, logicalRunID)
	if lookupErr != nil {
		return NewsContextBatchApplyResult{}, lookupErr
	}
	if result.UrgentReview && !historicalRun {
		latest, getErr := s.store.GetNewsContextRun(ctx, logicalRunID)
		if getErr != nil {
			return NewsContextBatchApplyResult{}, getErr
		}
		latest.ReviewStatus = NewsContextReviewPending
		if _, updateErr := s.store.UpdateNewsContextRun(ctx, latest); updateErr != nil {
			return NewsContextBatchApplyResult{}, updateErr
		}
	}
	indexCtx, cancelIndex := context.WithTimeout(ctx, newsContextIndexSyncTimeout)
	indexErr := s.SyncNewsContextEmbeddingObjects(indexCtx, result.ChangedThreadIDs, result.ChangedVersionIDs)
	cancelIndex()
	if indexErr != nil && s.log != nil {
		s.log.Warn("news context fragment index deferred",
			"context_run_id", logicalRunID,
			"agent_run_id", agentRunID,
			"thread_count", len(result.ChangedThreadIDs),
			"version_count", len(result.ChangedVersionIDs),
			"error", safelog.Text(indexErr.Error(), 300),
		)
	}
	return result, nil
}

func (s *Service) repairNewsContextRunEmbeddings(ctx context.Context, runID string) error {
	for {
		ready, err := s.repairNewsContextRunEmbeddingsPage(ctx, runID)
		if err != nil || ready {
			return err
		}
	}
}

func (s *Service) repairNewsContextRunEmbeddingsPage(ctx context.Context, runID string) (bool, error) {
	versions, err := s.store.ListNewsContextRunVersionsNeedingIndex(ctx, runID, newsContextBackfillIndexPageSize)
	if err != nil {
		return false, err
	}
	if len(versions) == 0 {
		return true, nil
	}
	threadIDs := make([]string, 0, len(versions))
	versionIDs := make([]string, 0, len(versions))
	for _, version := range versions {
		threadIDs = append(threadIDs, version.ThreadID)
		versionIDs = append(versionIDs, version.ID)
	}
	// ponytail: index_status is the durable crash-recovery cursor. Normal
	// batches sync only their changed IDs; the final backfill gate still checks
	// asset hashes and vectors across the complete retained result.
	if err := s.SyncNewsContextEmbeddingObjects(ctx, threadIDs, versionIDs); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Service) listAllNewsContextRunItems(ctx context.Context, filter NewsContextRunItemListFilter) ([]NewsContextRunItem, error) {
	items := make([]NewsContextRunItem, 0)
	for offset := 0; ; offset += newsContextSeedPageSize {
		filter.Limit = newsContextSeedPageSize
		filter.Offset = offset
		page, err := s.store.ListNewsContextRunItems(ctx, filter)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if len(page) < newsContextSeedPageSize {
			return items, nil
		}
	}
}

func (s *Service) validateNewsContextResearchAudit(ctx context.Context, run NewsContextRun, items []NewsContextRunItem, report NewsContextReport) error {
	if err := validateNewsContextSearchAudit(report.SearchAudit); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidNewsContextResult, err)
	}
	if _, historical, err := s.store.NewsContextBackfillForRun(ctx, run.ID); err != nil {
		return err
	} else if historical {
		// ponytail: historical reconstruction is bounded to persisted news and
		// point-in-time semantic theme lookup; public browsing made the backlog
		// unbounded without improving exact manifest coverage.
		if len(report.SearchAudit) != 0 {
			return fmt.Errorf("%w: historical reconstruction search audit must be empty", ErrInvalidNewsContextResult)
		}
		return nil
	}
	required := false
	for _, change := range report.ThreadChanges {
		if change.MaterialChange || len(change.CounterEvidence) > 0 {
			required = true
			break
		}
	}
	if !required {
		for _, item := range items {
			if item.ObjectType != NewsContextRunItemNewsEvent {
				continue
			}
			event, err := s.store.GetNewsEvent(ctx, item.ObjectID)
			if err != nil {
				return err
			}
			if newsContextEventRequiresResearch(event) {
				required = true
				break
			}
		}
	}
	if !required {
		return nil
	}
	if len(report.SearchAudit) == 0 {
		return fmt.Errorf("%w: public research audit is required", ErrInvalidNewsContextResult)
	}
	return nil
}

func normalizeNewsContextThreadReviewOutcomes(items []NewsContextRunItem, report *NewsContextReport) int {
	if report == nil {
		return 0
	}
	expected := make(map[string]struct{})
	for _, item := range items {
		if item.ObjectType == NewsContextRunItemThread {
			expected[firstNonEmpty(item.ThreadID, item.ObjectID)] = struct{}{}
		}
	}
	ignored := 0
	filter := func(ids []string) []string {
		kept := make([]string, 0, len(ids))
		for _, rawID := range ids {
			id := strings.TrimSpace(rawID)
			if _, ok := expected[id]; !ok {
				ignored++
				continue
			}
			kept = append(kept, id)
		}
		return kept
	}
	// ponytail: these arrays only persist review dispositions for manifest
	// thread items. A read-only semantic-search candidate cannot mutate state,
	// so discard it instead of losing an otherwise valid news batch. The raw
	// submitted result remains unchanged in the Agent decision ledger.
	report.ReviewedThreadIDs = filter(report.ReviewedThreadIDs)
	report.UnchangedThreadIDs = filter(report.UnchangedThreadIDs)
	return ignored
}

func validateNewsContextSearchAudit(audits []NewsContextSearchAudit) error {
	for index, audit := range audits {
		if strings.TrimSpace(audit.Question) == "" {
			return fmt.Errorf("search_audit[%d] requires question", index)
		}
		status := strings.TrimSpace(audit.Status)
		switch status {
		case "completed", "verified":
			if !hasNonEmptyNewsContextAuditSource(audit.Sources) {
				return fmt.Errorf("search_audit[%d] status %q requires sources", index, status)
			}
		case "failed", "unavailable":
			if strings.TrimSpace(audit.FailureReason) == "" {
				return fmt.Errorf("search_audit[%d] status %q requires failure_reason", index, status)
			}
		default:
			return fmt.Errorf("search_audit[%d] status must be completed, verified, failed, or unavailable", index)
		}
	}
	return nil
}

func hasNonEmptyNewsContextAuditSource(sources []string) bool {
	for _, source := range sources {
		if strings.TrimSpace(source) != "" {
			return true
		}
	}
	return false
}

func validateNewsContextReport(run NewsContextRun, items []NewsContextRunItem, report NewsContextReport) error {
	if report.SchemaVersion != NewsContextResultSchemaVersion || report.RunID != run.ID || report.WindowType != run.WindowType {
		return ErrInvalidNewsContextResult
	}
	if err := validateNewsContextBatchNewsCoverage(items, report); err != nil {
		return err
	}
	expectedThreads := map[string]bool{}
	for _, item := range items {
		switch item.ObjectType {
		case NewsContextRunItemThread:
			expectedThreads[firstNonEmpty(item.ThreadID, item.ObjectID)] = false
		}
	}
	threadOutcomes := make(map[string]string, len(expectedThreads))
	markThreadOutcome := func(id, outcome string) error {
		id = strings.TrimSpace(id)
		if _, ok := expectedThreads[id]; !ok {
			return fmt.Errorf("%w: thread %s was not part of this batch", ErrInvalidNewsContextResult, id)
		}
		if threadOutcomes[id] != "" {
			return fmt.Errorf("%w: thread %s has duplicate review outcomes", ErrInvalidNewsContextResult, id)
		}
		threadOutcomes[id] = outcome
		expectedThreads[id] = true
		return nil
	}
	for _, id := range report.ReviewedThreadIDs {
		if err := markThreadOutcome(id, "reviewed"); err != nil {
			return err
		}
	}
	for _, id := range report.UnchangedThreadIDs {
		if err := markThreadOutcome(id, "unchanged"); err != nil {
			return err
		}
	}
	for _, change := range report.ThreadChanges {
		action := strings.TrimSpace(change.Action)
		switch action {
		case "create", "update", "merge", "split", "restart":
		default:
			return fmt.Errorf("%w: unsupported thread change action", ErrInvalidNewsContextResult)
		}
		if strings.TrimSpace(change.Title) == "" || strings.TrimSpace(change.CoreThesis) == "" || !validNewsThreadStage(change.Stage) || change.Confidence < 0 || change.Confidence > 1 {
			return fmt.Errorf("%w: invalid thread change", ErrInvalidNewsContextResult)
		}
		if action == "create" && strings.TrimSpace(change.ThreadID) != "" {
			return fmt.Errorf("%w: new thread must not reuse an existing id", ErrInvalidNewsContextResult)
		}
		if action != "create" && strings.TrimSpace(change.ThreadID) == "" {
			return fmt.Errorf("%w: existing thread change requires thread id", ErrInvalidNewsContextResult)
		}
		if _, ok := expectedThreads[change.ThreadID]; ok {
			if err := markThreadOutcome(change.ThreadID, "changed"); err != nil {
				return err
			}
		}
		for _, id := range change.SourceThreadIDs {
			if _, ok := expectedThreads[id]; ok && id != change.ThreadID {
				if err := markThreadOutcome(id, "changed"); err != nil {
					return err
				}
			}
		}
	}
	for id, covered := range expectedThreads {
		if !covered {
			return fmt.Errorf("%w: thread %s was not reviewed", ErrInvalidNewsContextResult, id)
		}
	}
	return nil
}

func validateNewsContextBatchNewsCoverage(items []NewsContextRunItem, report NewsContextReport) error {
	expectedNews := make(map[string]bool)
	for _, item := range items {
		if item.ObjectType == NewsContextRunItemNewsEvent {
			expectedNews[item.ObjectID] = false
		}
	}
	for _, id := range report.ProcessedNewsIDs {
		if _, ok := expectedNews[id]; !ok || expectedNews[id] {
			return fmt.Errorf("%w: invalid or duplicate processed news id", ErrInvalidNewsContextResult)
		}
		expectedNews[id] = true
	}
	for id, processed := range expectedNews {
		if !processed {
			return fmt.Errorf("%w: news %s was not covered", ErrInvalidNewsContextResult, id)
		}
	}
	return validateNewsContextDecisionEvidenceConsistency(report.ProcessedNewsIDs, report.NewsDecisions, report.ThreadChanges)
}

func validateNewsContextDecisionEvidenceConsistency(processedNewsIDs []string, newsDecisions []NewsContextNewsDecision, threadChanges []NewsContextThreadChange) error {
	expectedNews := make(map[string]bool, len(processedNewsIDs))
	for _, id := range processedNewsIDs {
		id = strings.TrimSpace(id)
		if id == "" || expectedNews[id] {
			return fmt.Errorf("%w: invalid or duplicate processed news id", ErrInvalidNewsContextResult)
		}
		expectedNews[id] = true
	}
	decisions := make(map[string]NewsContextNewsDecision, len(newsDecisions))
	for _, decision := range newsDecisions {
		if _, valid := normalizeNewsContextDisposition(decision.Disposition); !valid {
			return fmt.Errorf("%w: unsupported news decision disposition", ErrInvalidNewsContextResult)
		}
		decision.NewsEventID = strings.TrimSpace(decision.NewsEventID)
		if !expectedNews[decision.NewsEventID] || decisions[decision.NewsEventID].NewsEventID != "" {
			return fmt.Errorf("%w: invalid news decision coverage", ErrInvalidNewsContextResult)
		}
		decisions[decision.NewsEventID] = decision
	}
	for id := range expectedNews {
		if decisions[id].NewsEventID == "" {
			return fmt.Errorf("%w: news %s was not covered", ErrInvalidNewsContextResult, id)
		}
	}

	changeForEvidence := make(map[string]int, len(expectedNews))
	identityOwner := make(map[string]int)
	createIdentity := make(map[int]string)
	for index, change := range threadChanges {
		if strings.TrimSpace(change.Action) == "create" {
			continue
		}
		threadID := strings.TrimSpace(change.ThreadID)
		if threadID == "" {
			continue
		}
		if owner, exists := identityOwner[threadID]; exists && owner != index {
			return fmt.Errorf("%w: thread identity is used by multiple changes", ErrInvalidNewsContextResult)
		}
		identityOwner[threadID] = index
	}
	for index, change := range threadChanges {
		for _, rawEventID := range change.EvidenceNewsIDs {
			eventID := strings.TrimSpace(rawEventID)
			if _, ok := expectedNews[eventID]; !ok {
				return fmt.Errorf("%w: evidence news was not part of this batch", ErrInvalidNewsContextResult)
			}
			if _, duplicate := changeForEvidence[eventID]; duplicate {
				return fmt.Errorf("%w: news evidence belongs to multiple thread changes", ErrInvalidNewsContextResult)
			}
			decision, ok := decisions[eventID]
			if !ok {
				return fmt.Errorf("%w: evidence news has no decision", ErrInvalidNewsContextResult)
			}
			disposition, _ := normalizeNewsContextDisposition(decision.Disposition)
			if disposition == NewsEventContextNoise || disposition == "duplicate" || disposition == NewsEventContextDeferred {
				return fmt.Errorf("%w: noise, duplicate, or deferred news cannot be theme evidence", ErrInvalidNewsContextResult)
			}
			decisionThreadID := strings.TrimSpace(decision.ThreadID)
			if strings.TrimSpace(change.Action) == "create" {
				if decisionThreadID != "" {
					if current := createIdentity[index]; current != "" && current != decisionThreadID {
						return fmt.Errorf("%w: one new thread uses multiple temporary identities", ErrInvalidNewsContextResult)
					}
					if owner, exists := identityOwner[decisionThreadID]; exists && owner != index {
						return fmt.Errorf("%w: temporary thread identity maps to multiple changes", ErrInvalidNewsContextResult)
					}
					createIdentity[index] = decisionThreadID
					identityOwner[decisionThreadID] = index
				}
			} else if evidenceThreadID := strings.TrimSpace(change.ThreadID); decisionThreadID == "" || decisionThreadID != evidenceThreadID {
				return fmt.Errorf("%w: news %s decision thread %q does not match evidence thread %q", ErrInvalidNewsContextResult, eventID, decisionThreadID, evidenceThreadID)
			}
			changeForEvidence[eventID] = index
		}
	}
	for eventID, decision := range decisions {
		disposition, _ := normalizeNewsContextDisposition(decision.Disposition)
		_, hasEvidence := changeForEvidence[eventID]
		if disposition == NewsEventContextNoise || disposition == "duplicate" || disposition == NewsEventContextDeferred {
			if hasEvidence {
				return fmt.Errorf("%w: non-theme news cannot be evidence", ErrInvalidNewsContextResult)
			}
			continue
		}
		if !hasEvidence {
			return fmt.Errorf("%w: covered news %s has no theme evidence", ErrInvalidNewsContextResult, eventID)
		}
	}
	return nil
}

func (s *Service) completeNewsContextRun(ctx context.Context, run *NewsContextRun, _ NewsContextConfig) error {
	pending, err := s.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Status: NewsContextRunItemPending})
	if err != nil {
		return err
	}
	failed, err := s.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Status: NewsContextRunItemFailed})
	if err != nil {
		return err
	}
	run.PendingCount = pending
	if failed > 0 || pending > 0 {
		return fmt.Errorf("news context run has %d failed and %d pending items", failed, pending)
	}
	_, historicalRun, err := s.store.NewsContextBackfillForRun(ctx, run.ID)
	if err != nil {
		return err
	}
	finalReviewOwner, finalReviewRun, err := s.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID)
	if err != nil {
		return err
	}
	deferFinalReview := finalReviewRun && finalReviewOwner.Status != NewsContextBackfillStatusCompleted
	if !historicalRun && run.WindowType == NewsContextWindowDaily {
		if err := s.PruneTransientNewsContextEmbeddings(ctx, run.WindowEnd); err != nil {
			return fmt.Errorf("prune temporary news context indexes: %w", err)
		}
	}
	reviewRequired := !historicalRun && !deferFinalReview && (run.ReviewStatus == NewsContextReviewPending ||
		(run.WindowType == NewsContextWindowDaily && run.InputCount > 0) ||
		(run.WindowType == NewsContextWindowFourHour && run.MaterialChangeCount > 0))
	switch run.WindowType {
	case NewsContextWindowHourly:
		run.Phase = "checkpointed"
	case NewsContextWindowDaily:
		run.Phase = "materialized"
	default:
		run.Phase = "completed"
	}
	run.Status = NewsContextRunStatusCompleted
	run.ReviewStatus = NewsContextReviewNotRequired
	if deferFinalReview {
		// ponytail: the parent backfill is the durable ordering cursor. Keeping the
		// run completed-but-pending prevents the generic review reconciler from
		// creating a sentinel before the paged index gate has finished.
		run.Phase = newsContextBackfillPhaseIndexing
		run.ReviewStatus = NewsContextReviewPending
	} else if reviewRequired {
		run.Status = NewsContextRunStatusWaitingReview
		run.ReviewStatus = NewsContextReviewPending
		run.Phase = "waiting_review"
		// The aggregation/materialization attempt and its downstream impact
		// review each receive the same bounded retry budget.
		run.RetryCount = 0
	}
	run.FinishedAt = time.Now()
	run.CurrentAgentRunID = ""
	run.NextRetryAt = time.Time{}
	updated, err := s.store.UpdateNewsContextRun(ctx, *run)
	if err != nil {
		return err
	}
	*run = updated
	if !reviewRequired && !deferFinalReview {
		if err := s.store.UpdateNewsThreadReviewStatusForRun(ctx, run.ID, NewsContextReviewNotRequired, run.FinishedAt); err != nil {
			return err
		}
	}
	if !historicalRun {
		latestConfig, err := s.GetNewsContextConfig(ctx)
		if err != nil {
			return err
		}
		latestConfig.LastRunAt = run.FinishedAt
		latestConfig.LastError = ""
		if run.TriggerType == NewsContextTriggerScheduled && newsContextRunUsesNaturalWindow(*run) {
			currentNext := newsContextNextAt(latestConfig, run.WindowType)
			if currentNext.IsZero() || !currentNext.After(run.WindowEnd) {
				setNewsContextNextAt(&latestConfig, run.WindowType, nextNewsContextBoundary(run.WindowType, run.WindowEnd))
			}
		}
		if _, err := s.store.UpsertNewsContextConfig(ctx, latestConfig); err != nil {
			return err
		}
	}
	if reviewRequired {
		s.triggerNewsContextReview(ctx, run)
	}
	return nil
}

func (s *Service) triggerNewsContextReview(ctx context.Context, run *NewsContextRun) {
	note := "复核最近一次消息脉络归纳对当前持仓、监控、提醒、机会和策略的影响。"
	if cfg, err := s.GetNewsContextConfig(ctx); err == nil && cfg.AdditionalResearchPrompt != "" {
		note += " 附加关注重点（只能增加检查项，不能覆盖固定安全规则）：" + cfg.AdditionalResearchPrompt
	}
	_, err := s.RunPortfolioSentinel(ctx, RequestRunPortfolioSentinel{
		WindowType:       PortfolioSentinelWindowManual,
		StartAt:          run.WindowStart.Format(time.RFC3339Nano),
		EndAt:            run.WindowEnd.Format(time.RFC3339Nano),
		Note:             note,
		NewsContextRunID: run.ID,
	})
	// The sentinel start path binds its run id before it can launch the agent.
	// Reload here so an immediate submission cannot be made invisible by a stale
	// in-memory copy overwriting that association.
	if latest, getErr := s.store.GetNewsContextRun(ctx, run.ID); getErr == nil {
		*run = latest
	}
	if err != nil {
		s.failNewsContextReview(context.Background(), run, fmt.Errorf("start portfolio review failed: %w", err))
		return
	}
}

func (s *Service) failNewsContextReview(ctx context.Context, run *NewsContextRun, cause error) {
	run.ReviewStatus = NewsContextReviewFailed
	run.Status = NewsContextRunStatusWaitingReview
	run.Phase = "review_failed"
	run.ErrorMessage = safelog.Text(cause.Error(), 500)
	s.scheduleNewsContextAutoRetry(ctx, run, cause, time.Now())
	_ = s.store.UpdateNewsThreadReviewStatusForRun(ctx, run.ID, NewsContextReviewFailed, time.Now())
	_, _ = s.store.UpdateNewsContextRun(ctx, *run)
}

func (s *Service) reconcileNewsContextReviews(ctx context.Context) {
	pending, err := s.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{ReviewStatus: NewsContextReviewPending, Limit: 200})
	if err == nil {
		for i := range pending {
			if pending[i].Status == NewsContextRunStatusWaitingReview && strings.TrimSpace(pending[i].ReviewRunID) == "" {
				s.triggerNewsContextReview(ctx, &pending[i])
			}
		}
	}
	runs, err := s.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{ReviewStatus: NewsContextReviewRunning, Limit: 200})
	if err != nil {
		return
	}
	for _, run := range runs {
		if strings.TrimSpace(run.ReviewRunID) == "" {
			continue
		}
		sentinel, err := s.store.GetPortfolioSentinelRun(ctx, run.ReviewRunID)
		if err != nil {
			continue
		}
		switch sentinel.Status {
		case PortfolioSentinelStatusCompleted:
			run.ReviewStatus = NewsContextReviewCompleted
			run.Status = NewsContextRunStatusCompleted
			run.Phase = "completed"
			run.ErrorMessage = ""
			if err := s.store.UpdateNewsThreadReviewStatusForRun(ctx, run.ID, NewsContextReviewCompleted, time.Now()); err != nil {
				s.failNewsContextReview(ctx, &run, fmt.Errorf("update theme review status failed: %w", err))
				continue
			}
			_, _ = s.store.UpdateNewsContextRun(ctx, run)
		case PortfolioSentinelStatusFailed:
			s.failNewsContextReview(ctx, &run, errors.New(firstNonEmpty(sentinel.ErrorMessage, "portfolio review failed")))
		}
	}
	s.retryDueNewsContextReviews(ctx, time.Now())
}

func (s *Service) RetryNewsContextRun(ctx context.Context, id string) (NewsContextRun, error) {
	return s.retryNewsContextRun(ctx, id, false)
}

func (s *Service) retryNewsContextRun(ctx context.Context, id string, automatic bool) (NewsContextRun, error) {
	run, err := s.store.GetNewsContextRun(ctx, strings.TrimSpace(id))
	if err != nil {
		return NewsContextRun{}, err
	}
	if run.Status != NewsContextRunStatusFailed && run.ReviewStatus != NewsContextReviewFailed {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	if automatic {
		if run.RetryCount >= newsContextTimeoutRetryLimit || run.NextRetryAt.IsZero() || run.NextRetryAt.After(time.Now()) {
			return NewsContextRun{}, ErrInvalidNewsContextInput
		}
		run.RetryCount++
	}
	run.NextRetryAt = time.Time{}
	if run.ReviewStatus == NewsContextReviewFailed && run.ProcessedCount >= run.InputCount {
		run.ReviewStatus = NewsContextReviewPending
		run.ReviewRunID = ""
		run.Status = NewsContextRunStatusWaitingReview
		run.Phase = "waiting_review"
		run.ErrorMessage = ""
		if run, err = s.store.UpdateNewsContextRun(ctx, run); err != nil {
			return NewsContextRun{}, err
		}
		s.triggerNewsContextReview(ctx, &run)
		return run, nil
	}
	if !s.tryStartNewsContextRun() {
		return NewsContextRun{}, ErrNewsContextAlreadyRunning
	}
	if run.WindowType != NewsContextWindowFourHour {
		run.Status = NewsContextRunStatusPending
		run.Phase = "collecting"
		run.ErrorMessage = ""
		run.FinishedAt = time.Time{}
		if run, err = s.store.UpdateNewsContextRun(ctx, run); err != nil {
			s.finishNewsContextRun()
			return NewsContextRun{}, err
		}
		run, err = s.preparePendingNewsContextRun(ctx, run)
		if err != nil {
			run.Status = NewsContextRunStatusFailed
			run.Phase = "collecting"
			run.ErrorMessage = safelog.Text(err.Error(), 500)
			run.FinishedAt = time.Now()
			s.scheduleNewsContextAutoRetry(context.Background(), &run, err, run.FinishedAt)
			_, _ = s.store.UpdateNewsContextRun(context.Background(), run)
			s.finishNewsContextRun()
			return NewsContextRun{}, err
		}
		if !s.launchNewsContextWorker(run.ID, s.executeNewsContextRun) {
			s.finishNewsContextRun()
			return run, context.Canceled
		}
		return run, nil
	}
	if err := s.store.ResetFailedNewsContextRunItems(ctx, run.ID); err != nil {
		s.finishNewsContextRun()
		return NewsContextRun{}, err
	}
	run.Status = NewsContextRunStatusRunning
	run.TriggerType = NewsContextTriggerRetry
	run.Phase = newsContextRunPhaseAggregating
	run.ErrorMessage = ""
	run.FinishedAt = time.Time{}
	run, err = s.store.UpdateNewsContextRun(ctx, run)
	if err != nil {
		s.finishNewsContextRun()
		return NewsContextRun{}, err
	}
	if !s.launchNewsContextWorker(run.ID, s.executeNewsContextRun) {
		s.finishNewsContextRun()
		return run, context.Canceled
	}
	return run, nil
}

func (s *Service) retryDueNewsContextReviews(ctx context.Context, now time.Time) {
	for offset := 0; ; offset += 200 {
		failed, err := s.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{
			ReviewStatus: NewsContextReviewFailed,
			Limit:        200,
			Offset:       offset,
		})
		if err != nil {
			return
		}
		for index := range failed {
			due, scheduleErr := s.ensureNewsContextAutoRetryScheduled(ctx, &failed[index], now)
			if scheduleErr != nil || !due {
				continue
			}
			if _, retryErr := s.retryNewsContextRun(ctx, failed[index].ID, true); retryErr != nil && s.log != nil {
				s.log.Warn("retry failed news context review failed",
					"context_run_id", failed[index].ID,
					"error", safelog.Text(retryErr.Error(), 240),
				)
			}
			// Portfolio impact reviews are single-flight. Start at most one per tick.
			return
		}
		if len(failed) < 200 {
			return
		}
	}
}

func (s *Service) GetNewsThread(ctx context.Context, id string) (NewsThread, error) {
	return s.store.GetNewsThread(ctx, strings.TrimSpace(id))
}

func (s *Service) ListNewsThreads(ctx context.Context, filter NewsThreadListFilter) ([]NewsThread, error) {
	items, err := s.store.ListNewsThreads(ctx, filter)
	if err != nil {
		return nil, err
	}
	cfg, cfgErr := s.embeddingConfigOrDefault(ctx)
	if cfgErr != nil || strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		return items, nil
	}
	for i := range items {
		if items[i].IndexStatus == NewsContextIndexFailed {
			continue
		}
		asset, assetErr := s.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, items[i].ID, cfg.EmbeddingModelID)
		if assetErr != nil {
			items[i].IndexStatus = NewsContextIndexPending
			continue
		}
		items[i].IndexStatus = asset.Status
		items[i].IndexError = asset.ErrorMessage
		if asset.Status == EmbeddingAssetStatusReady && asset.TextHash != hashEmbeddingText(NewsThreadEmbeddingText(items[i])) {
			items[i].IndexStatus = NewsContextIndexStale
		}
	}
	return items, nil
}

func (s *Service) CountNewsThreads(ctx context.Context, filter NewsThreadListFilter) (int, error) {
	return s.store.CountNewsThreads(ctx, filter)
}

func (s *Service) GetNewsThreadDetail(ctx context.Context, id string) (NewsThreadDetail, error) {
	thread, err := s.store.GetNewsThread(ctx, strings.TrimSpace(id))
	if err != nil {
		return NewsThreadDetail{}, err
	}
	versions := make([]NewsThreadVersion, 0, 200)
	for offset := 0; ; offset += 200 {
		page, pageErr := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{ThreadID: thread.ID, Limit: 200, Offset: offset})
		if pageErr != nil {
			return NewsThreadDetail{}, pageErr
		}
		versions = append(versions, page...)
		if len(page) < 200 {
			break
		}
	}
	evidence := make([]NewsThreadEvidence, 0, 500)
	for offset := 0; ; offset += 500 {
		page, pageErr := s.store.ListNewsThreadEvidence(ctx, NewsThreadEvidenceListFilter{ThreadID: thread.ID, Limit: 500, Offset: offset})
		if pageErr != nil {
			return NewsThreadDetail{}, pageErr
		}
		evidence = append(evidence, page...)
		if len(page) < 500 {
			break
		}
	}
	detail := NewsThreadDetail{Theme: thread, Versions: versions, Evidence: evidence, IndexStatus: thread.IndexStatus, IndexError: thread.IndexError}
	mcp := s.AgentMCPStatus()
	mcpToolsReady := newsContextContainsString(mcp.RequiredTools, "stock_agent.semantic_search_news_threads") &&
		newsContextContainsString(mcp.RequiredTools, "stock_agent.get_news_thread")
	if !mcp.Enabled {
		detail.MCPError = "本地股票检索服务尚未启动"
	} else if !mcpToolsReady {
		detail.MCPError = "消息脉络检索工具注册不完整"
	}
	cfg, cfgErr := s.embeddingConfigOrDefault(ctx)
	if cfgErr == nil && strings.TrimSpace(cfg.EmbeddingModelID) != "" {
		asset, assetErr := s.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThread, thread.ID, cfg.EmbeddingModelID)
		if assetErr == nil && asset.Status == EmbeddingAssetStatusReady && strings.TrimSpace(asset.VectorRef) != "" {
			detail.MCPReadable = mcp.Enabled && mcpToolsReady
			if thread.IndexStatus != NewsContextIndexFailed {
				if asset.TextHash == hashEmbeddingText(NewsThreadEmbeddingText(thread)) {
					detail.IndexStatus = NewsContextIndexReady
					detail.IndexError = ""
				} else {
					detail.IndexStatus = NewsContextIndexStale
				}
			}
		} else if assetErr == nil && thread.IndexStatus != NewsContextIndexFailed {
			detail.IndexStatus = asset.Status
			detail.IndexError = asset.ErrorMessage
		}
	}
	if verification, found, verificationErr := s.store.GetNewsContextMCPVerification(ctx, thread.ID); verificationErr == nil && found {
		detail.MCPVerification = &verification
		detail.MCPVerified = verification.Status == NewsContextMCPVerificationReady && verification.VersionID == thread.CurrentVersionID
		if verification.VersionID == thread.CurrentVersionID && verification.Status == NewsContextMCPVerificationFailed {
			detail.MCPError = verification.ErrorMessage
		}
	}
	if detail.IndexStatus != NewsContextIndexReady {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "当前主题版本尚未完成向量索引")
	}
	if thread.ReviewStatus != NewsContextReviewCompleted && thread.ReviewStatus != NewsContextReviewNotRequired {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "最新主题版本尚未完成影响复核")
	}
	if strings.TrimSpace(thread.CurrentVersionID) != "" {
		if currentVersion, versionErr := s.store.GetNewsThreadVersion(ctx, thread.CurrentVersionID); versionErr == nil {
			if reason := newsContextVersionProtectionReason(currentVersion); reason != "" {
				detail.ProtectedReasons = append(detail.ProtectedReasons, reason)
			}
		}
	}
	if !detail.MCPReadable {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "CLI 尚不能稳定检索并读取当前主题")
	} else if !detail.MCPVerified {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "当前主题版本尚未通过真实 CLI 检索验证")
	}
	detail.ProtectedReasons = uniqueNonEmptyStrings(detail.ProtectedReasons)
	return detail, nil
}

func (s *Service) ListNewsContextChangedThreads(ctx context.Context, runID string, limit, offset int) ([]NewsThreadChange, int, error) {
	runID = strings.TrimSpace(runID)
	items, err := s.store.ListNewsContextChangedThreads(ctx, runID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountNewsThreadVersions(ctx, NewsThreadVersionListFilter{RunID: runID})
	return items, total, err
}

func (s *Service) ListNewsContextRuns(ctx context.Context, filter NewsContextRunListFilter) ([]NewsContextRun, error) {
	items, err := s.store.ListNewsContextRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := s.decorateNewsContextRun(ctx, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Service) CountNewsContextRuns(ctx context.Context, filter NewsContextRunListFilter) (int, error) {
	return s.store.CountNewsContextRuns(ctx, filter)
}

func (s *Service) GetNewsContextSummary(ctx context.Context) (NewsContextSummary, error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return NewsContextSummary{}, err
	}
	current, processed, compacted, protected, released, err := s.store.NewsContextStorageStats(ctx)
	if err != nil {
		return NewsContextSummary{}, err
	}
	themeCount, err := s.store.CountNewsThreads(ctx, NewsThreadListFilter{})
	if err != nil {
		return NewsContextSummary{}, err
	}
	activeCount, err := s.store.CountNewsThreads(ctx, NewsThreadListFilter{Status: NewsThreadStatusActive})
	if err != nil {
		return NewsContextSummary{}, err
	}
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	changedCount, _ := s.store.CountNewsThreads(ctx, NewsThreadListFilter{Since: dayStart})
	pendingReview, _ := s.store.CountNewsContextRuns(ctx, NewsContextRunListFilter{ReviewStatus: NewsContextReviewPending})
	runningReview, _ := s.store.CountNewsContextRuns(ctx, NewsContextRunListFilter{ReviewStatus: NewsContextReviewRunning})
	summary := NewsContextSummary{
		Config:             cfg,
		ThemeCount:         themeCount,
		ActiveThemeCount:   activeCount,
		ChangedThemeCount:  changedCount,
		CurrentNewsCount:   current,
		ProcessedNewsCount: processed,
		CompactedNewsCount: compacted,
		ProtectedNewsCount: protected,
		ReleasedBytes:      released,
		PendingReviewCount: pendingReview + runningReview,
		UpdatedAt:          now,
	}
	cleanupCutoff, err := newsContextCleanupCutoff(now, cfg.CleanupGraceSeconds, "")
	if err != nil {
		return NewsContextSummary{}, err
	}
	summary.CleanupGate, err = s.newsContextCleanupGate(ctx, cleanupCutoff)
	if err != nil {
		return NewsContextSummary{}, err
	}
	pendingNews, _ := s.store.CountNewsEventsByContextStatus(ctx, NewsEventContextPending)
	deferredNews, _ := s.store.CountNewsEventsByContextStatus(ctx, NewsEventContextDeferred)
	coveredNews, _ := s.store.CountNewsEventsByContextStatus(ctx, NewsEventContextCovered)
	noiseNews, _ := s.store.CountNewsEventsByContextStatus(ctx, NewsEventContextNoise)
	summary.PendingNewsCount = pendingNews + deferredNews
	summary.PendingCleanupCount = coveredNews + noiseNews - protected
	if summary.PendingCleanupCount < 0 {
		summary.PendingCleanupCount = 0
	}
	if runs, listErr := s.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{Limit: 1}); listErr == nil && len(runs) > 0 {
		if decorateErr := s.decorateNewsContextRun(ctx, &runs[0]); decorateErr == nil {
			summary.LatestRun = &runs[0]
		}
	}
	if runs, listErr := s.store.ListNewsContextCleanupRuns(ctx, NewsContextCleanupRunListFilter{Limit: 1}); listErr == nil && len(runs) > 0 {
		decorateNewsContextCleanupRun(&runs[0])
		summary.LatestCleanup = &runs[0]
	}
	summary.ReadyIndexCount, _ = s.store.CountNewsThreads(ctx, NewsThreadListFilter{IndexStatus: NewsContextIndexReady})
	summary.MissingIndexCount, _ = s.store.CountNewsThreads(ctx, NewsThreadListFilter{IndexStatus: NewsContextIndexPending})
	summary.StaleIndexCount, _ = s.store.CountNewsThreads(ctx, NewsThreadListFilter{IndexStatus: NewsContextIndexStale})
	summary.FailedIndexCount, _ = s.store.CountNewsThreads(ctx, NewsThreadListFilter{IndexStatus: NewsContextIndexFailed})
	embedCfg, embedErr := s.embeddingConfigOrDefault(ctx)
	if embedErr == nil && (!embedCfg.Enabled || strings.TrimSpace(embedCfg.EmbeddingModelID) == "") && themeCount > 0 {
		embedErr = ErrEmbeddingModelUnavailable
	}
	switch {
	case embedErr != nil:
		summary.IndexStatus = NewsContextIndexFailed
		summary.IndexError = safelog.Text(embedErr.Error(), 300)
	case summary.FailedIndexCount > 0:
		summary.IndexStatus = NewsContextIndexFailed
	case summary.MissingIndexCount > 0 || summary.StaleIndexCount > 0:
		summary.IndexStatus = NewsContextIndexStale
	case summary.ReadyIndexCount > 0:
		summary.IndexStatus = NewsContextIndexReady
	default:
		summary.IndexStatus = NewsContextIndexPending
	}
	mcp := s.AgentMCPStatus()
	summary.MCPEnabled = mcp.Enabled
	summary.MCPToolsReady = newsContextContainsString(mcp.RequiredTools, "stock_agent.semantic_search_news_threads") &&
		newsContextContainsString(mcp.RequiredTools, "stock_agent.get_news_thread") &&
		newsContextContainsString(mcp.RequiredTools, "stock_agent.list_news_context_changes")
	mcpErrors := make([]string, 0, 2)
	if verification, found, verificationErr := s.store.GetLatestNewsContextMCPVerification(ctx); verificationErr != nil {
		mcpErrors = append(mcpErrors, safelog.Text(verificationErr.Error(), 300))
	} else if found {
		summary.MCPLastVerifiedAt = verification.CheckedAt
		summary.MCPVerificationStatus = verification.Status
		if verification.Status == NewsContextMCPVerificationFailed {
			mcpErrors = append(mcpErrors, verification.ErrorMessage)
		}
	}
	if !summary.MCPEnabled {
		mcpErrors = append(mcpErrors, "本地股票检索服务尚未启动")
	} else if !summary.MCPToolsReady {
		mcpErrors = append(mcpErrors, "消息脉络检索工具注册不完整")
	}
	summary.MCPError = strings.Join(uniqueNonEmptyStrings(mcpErrors), "；")
	return summary, nil
}

func (s *Service) decorateNewsContextRun(ctx context.Context, run *NewsContextRun) error {
	run.Kind = "aggregation"
	unchanged, err := s.store.CountNewsContextRunThreadDisposition(ctx, run.ID, "unchanged")
	if err != nil {
		return err
	}
	run.UnchangedThreadCount = unchanged
	if run.ResearchCount > 0 {
		run.ExternalResearchStatus = "recorded"
	} else {
		run.ExternalResearchStatus = "not_required"
	}
	if run.InputCount > 0 {
		run.Progress = float64(run.ProcessedCount) / float64(run.InputCount)
	}
	switch run.Status {
	case NewsContextRunStatusCompleted, NewsContextRunStatusWaitingReview:
		run.CoverageStatus = "complete"
	case NewsContextRunStatusFailed:
		run.CoverageStatus = "failed"
		run.Retryable = true
		run.FailedStage = run.Phase
	default:
		run.CoverageStatus = "pending"
	}
	if run.ReviewStatus == NewsContextReviewFailed {
		run.Retryable = true
		run.FailedStage = run.Phase
	}
	run.RetryLimit = newsContextTimeoutRetryLimit
	failed := run.Status == NewsContextRunStatusFailed || run.ReviewStatus == NewsContextReviewFailed
	run.AutoRetryExhausted = failed && run.NextRetryAt.IsZero()
	return nil
}

func (s *Service) NewsContextRotationSignals(ctx context.Context) (NewsContextRotationSignals, error) {
	threads := make([]NewsThread, 0, 500)
	for offset := 0; ; offset += 500 {
		page, err := s.store.ListNewsThreads(ctx, NewsThreadListFilter{Status: NewsThreadStatusActive, Limit: 500, Offset: offset})
		if err != nil {
			return NewsContextRotationSignals{}, err
		}
		threads = append(threads, page...)
		if len(page) < 500 {
			break
		}
	}
	sort.SliceStable(threads, func(i, j int) bool {
		if threads[i].Confidence == threads[j].Confidence {
			return threads[i].LastChangedAt.After(threads[j].LastChangedAt)
		}
		return threads[i].Confidence > threads[j].Confidence
	})
	out := NewsContextRotationSignals{
		UpdatedAt:           time.Now(),
		DataStatus:          "news_only",
		Summary:             "消息脉络用于发现叙事扩散、加速、退潮和潜在接力，仍需结合行情、资金和持仓约束确认。",
		ConfirmationSignals: []string{"板块成交与相对强度同步改善", "龙头与跟随标的形成扩散", "后续催化按预期兑现"},
		InvalidationSignals: []string{"主题反证持续增加", "龙头先于板块转弱", "关键催化落空或政策方向反转"},
	}
	for _, thread := range threads {
		thread.ThemeID = thread.ID
		thread.Summary = thread.CoreThesis
		thread.DataConfirmation = "news_only"
		thread.ConfirmationSignals = append([]string(nil), thread.Catalysts...)
		thread.InvalidationSignals = append([]string(nil), thread.Invalidations...)
		switch thread.Stage {
		case NewsThreadStageAccelerating:
			out.Accelerating = append(out.Accelerating, thread)
			out.Mainline = append(out.Mainline, thread)
		case NewsThreadStageOverheated:
			out.Mainline = append(out.Mainline, thread)
		case NewsThreadStageDiverging, NewsThreadStageRetreating:
			out.Retreating = append(out.Retreating, thread)
		case NewsThreadStageEmerging, NewsThreadStageRestarting, NewsThreadStageSpreading:
			out.NextCandidates = append(out.NextCandidates, thread)
		}
	}
	return out, nil
}

func parseNewsContextTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if value, err := time.ParseInLocation(layout, strings.TrimSpace(raw), time.Local); err == nil {
			return value
		}
	}
	return time.Time{}
}

func prepareNewsContextSchedule(cfg *NewsContextConfig, now time.Time) bool {
	if cfg == nil || now.IsZero() {
		return false
	}
	changed := false
	align := func(windowType string, value *time.Time) {
		if value.IsZero() {
			return
		}
		// Old releases persisted completion-time-plus-duration values. Replaying
		// from the preceding full boundary is safe because claims only take still
		// pending news; rounding upward could strand an entire natural window.
		aligned := newsContextBoundaryAtOrBefore(windowType, *value)
		if !aligned.Equal(*value) {
			*value = aligned
			changed = true
		}
	}
	if cfg.HourlyEnabled {
		align(NewsContextWindowHourly, &cfg.NextHourlyAt)
		if cfg.NextHourlyAt.IsZero() {
			cfg.NextHourlyAt = newsContextBoundaryAtOrBefore(NewsContextWindowHourly, now)
			changed = true
		}
	}
	if cfg.FourHourEnabled {
		align(NewsContextWindowFourHour, &cfg.NextFourHourAt)
		if cfg.NextFourHourAt.IsZero() {
			if cfg.HourlyEnabled {
				cfg.NextFourHourAt = firstCompletableNewsContextParentEnd(*cfg, NewsContextWindowFourHour)
			} else {
				cfg.NextFourHourAt = newsContextBoundaryAtOrBefore(NewsContextWindowFourHour, now)
			}
			changed = true
		}
	}
	if cfg.DailyEnabled {
		align(NewsContextWindowDaily, &cfg.NextDailyAt)
		if cfg.NextDailyAt.IsZero() {
			if cfg.FourHourEnabled {
				cfg.NextDailyAt = firstCompletableNewsContextParentEnd(*cfg, NewsContextWindowDaily)
			} else {
				cfg.NextDailyAt = newsContextBoundaryAtOrBefore(NewsContextWindowDaily, now)
			}
			changed = true
		}
	}
	return changed
}

func fastForwardNewsContextScheduleForBackfill(cfg *NewsContextConfig, cutoff time.Time) bool {
	if cfg == nil || cutoff.IsZero() {
		return false
	}
	changed := false
	for _, windowType := range []string{NewsContextWindowHourly, NewsContextWindowFourHour, NewsContextWindowDaily} {
		if !newsContextWindowEnabled(*cfg, windowType) {
			continue
		}
		// ponytail: the frozen cutoff is the sole ownership boundary. Moving the
		// existing cursors once avoids a second queue and months of empty runs.
		firstStart := newsContextBoundaryAtOrBefore(windowType, cutoff)
		hasSmallerCadence := (windowType == NewsContextWindowFourHour && cfg.HourlyEnabled) ||
			(windowType == NewsContextWindowDaily && (cfg.HourlyEnabled || cfg.FourHourEnabled))
		if hasSmallerCadence {
			firstStart = newsContextBoundaryAtOrAfter(windowType, cutoff)
		}
		firstEnd := nextNewsContextBoundary(windowType, firstStart)
		if current := newsContextNextAt(*cfg, windowType); current.IsZero() || current.Before(firstEnd) {
			setNewsContextNextAt(cfg, windowType, firstEnd)
			changed = true
		}
	}
	return changed
}

func newsContextWindowEnabled(cfg NewsContextConfig, windowType string) bool {
	switch windowType {
	case NewsContextWindowDaily:
		return cfg.DailyEnabled
	case NewsContextWindowFourHour:
		return cfg.FourHourEnabled
	default:
		return cfg.HourlyEnabled
	}
}

func newsContextNextAt(cfg NewsContextConfig, windowType string) time.Time {
	switch windowType {
	case NewsContextWindowDaily:
		return cfg.NextDailyAt
	case NewsContextWindowFourHour:
		return cfg.NextFourHourAt
	default:
		return cfg.NextHourlyAt
	}
}

func setNewsContextNextAt(cfg *NewsContextConfig, windowType string, value time.Time) {
	switch windowType {
	case NewsContextWindowDaily:
		cfg.NextDailyAt = value
	case NewsContextWindowFourHour:
		cfg.NextFourHourAt = value
	default:
		cfg.NextHourlyAt = value
	}
}

func newsContextBoundaryAtOrBefore(windowType string, value time.Time) time.Time {
	local := value.In(value.Location())
	hour := local.Hour()
	switch windowType {
	case NewsContextWindowDaily:
		hour = 0
	case NewsContextWindowFourHour:
		hour = hour / 4 * 4
	}
	return time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, local.Location())
}

func newsContextBoundaryAtOrAfter(windowType string, value time.Time) time.Time {
	floor := newsContextBoundaryAtOrBefore(windowType, value)
	if floor.Before(value) {
		return nextNewsContextBoundary(windowType, floor)
	}
	return floor
}

func nextNewsContextBoundary(windowType string, boundary time.Time) time.Time {
	local := boundary.In(boundary.Location())
	switch windowType {
	case NewsContextWindowDaily:
		return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, local.Location())
	case NewsContextWindowFourHour:
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour()+4, 0, 0, 0, local.Location())
	default:
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour()+1, 0, 0, 0, local.Location())
	}
}

func previousNewsContextBoundary(windowType string, boundary time.Time) time.Time {
	local := boundary.In(boundary.Location())
	switch windowType {
	case NewsContextWindowDaily:
		return time.Date(local.Year(), local.Month(), local.Day()-1, 0, 0, 0, 0, local.Location())
	case NewsContextWindowFourHour:
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour()-4, 0, 0, 0, local.Location())
	default:
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour()-1, 0, 0, 0, local.Location())
	}
}

func newsContextScheduledWindow(windowType string, endAt time.Time) (time.Time, bool) {
	if !validNewsContextWindowType(windowType) || endAt.IsZero() || !newsContextBoundaryAtOrBefore(windowType, endAt).Equal(endAt) {
		return time.Time{}, false
	}
	startAt := previousNewsContextBoundary(windowType, endAt)
	return startAt, endAt.After(startAt)
}

func newsContextRunUsesNaturalWindow(run NewsContextRun) bool {
	startAt, ok := newsContextScheduledWindow(run.WindowType, run.WindowEnd)
	return ok && startAt.Equal(run.WindowStart)
}

func firstCompletableNewsContextParentEnd(cfg NewsContextConfig, parentType string) time.Time {
	childType := NewsContextWindowHourly
	if parentType == NewsContextWindowDaily {
		childType = NewsContextWindowFourHour
	}
	childStart, ok := newsContextScheduledWindow(childType, newsContextNextAt(cfg, childType))
	if !ok {
		return time.Time{}
	}
	parentStart := newsContextBoundaryAtOrAfter(parentType, childStart)
	return nextNewsContextBoundary(parentType, parentStart)
}

func (s *Service) newsContextParentWindowReadiness(ctx context.Context, cfg NewsContextConfig, parentType string, startAt, endAt time.Time) (bool, bool, error) {
	childType := ""
	switch {
	case parentType == NewsContextWindowFourHour && cfg.HourlyEnabled:
		childType = NewsContextWindowHourly
	case parentType == NewsContextWindowDaily && cfg.FourHourEnabled:
		childType = NewsContextWindowFourHour
	default:
		return true, false, nil
	}
	nextChildAt := newsContextNextAt(cfg, childType)
	allComplete := true
	for childStart := startAt; childStart.Before(endAt); {
		childEnd := nextNewsContextBoundary(childType, childStart)
		if childEnd.After(endAt) {
			return false, false, ErrInvalidNewsContextInput
		}
		complete, err := s.store.IsNewsContextWindowComplete(ctx, childType, childStart, childEnd)
		if err != nil {
			return false, false, err
		}
		if !complete {
			allComplete = false
			if !nextChildAt.IsZero() && childEnd.Before(nextChildAt) {
				return false, true, nil
			}
		}
		childStart = childEnd
	}
	return allComplete, false, nil
}

func newsContextWindowDuration(windowType string) time.Duration {
	switch windowType {
	case NewsContextWindowFourHour:
		return 4 * time.Hour
	case NewsContextWindowDaily:
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
