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

	"phantom-lancer/internal/safelog"
)

const (
	newsContextSchedulerInterval = 30 * time.Second
	newsContextSeedPageSize      = 500
	newsContextDefaultBatchSize  = 25
	newsContextMaxBatchSize      = 50
)

func defaultNewsContextConfig() NewsContextConfig {
	return NewsContextConfig{
		ID:                      NewsContextConfigIDDefault,
		Enabled:                 false,
		AutoCleanupEnabled:      false,
		HourlyEnabled:           true,
		FourHourEnabled:         true,
		DailyEnabled:            true,
		BatchSize:               newsContextDefaultBatchSize,
		HourlyIntervalSeconds:   3600,
		FourHourIntervalSeconds: 4 * 3600,
		DailyIntervalSeconds:    24 * 3600,
		CleanupGraceSeconds:     24 * 3600,
		UpdatedAt:               time.Now(),
	}
}

func normalizeNewsContextConfig(cfg NewsContextConfig) NewsContextConfig {
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = NewsContextConfigIDDefault
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = newsContextDefaultBatchSize
	}
	if cfg.BatchSize > newsContextMaxBatchSize {
		cfg.BatchSize = newsContextMaxBatchSize
	}
	if cfg.HourlyIntervalSeconds <= 0 {
		cfg.HourlyIntervalSeconds = 3600
	}
	if cfg.FourHourIntervalSeconds <= 0 {
		cfg.FourHourIntervalSeconds = 4 * 3600
	}
	if cfg.DailyIntervalSeconds <= 0 {
		cfg.DailyIntervalSeconds = 24 * 3600
	}
	if cfg.CleanupGraceSeconds < 3600 {
		cfg.CleanupGraceSeconds = 24 * 3600
	}
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
	return s.store.UpsertNewsContextConfig(ctx, defaultNewsContextConfig())
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
	updated, err := s.store.UpsertNewsContextConfig(ctx, cfg)
	if err != nil {
		return NewsContextConfig{}, err
	}
	if updated.Enabled {
		s.StartBackground(context.Background())
	}
	return updated, nil
}

func (s *Service) PatchNewsContextConfig(ctx context.Context, req RequestUpdateNewsContextConfig) (NewsContextConfig, error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return NewsContextConfig{}, err
	}
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
	if req.BatchSize != nil {
		if *req.BatchSize <= 0 || *req.BatchSize > newsContextMaxBatchSize {
			return NewsContextConfig{}, ErrInvalidNewsContextInput
		}
		cfg.BatchSize = *req.BatchSize
	}
	if req.Enabled != nil && *req.Enabled {
		for _, taskType := range []string{AgentTaskTypeNewsEventReview, AgentTaskTypePortfolioSentinel} {
			profile, profileErr := s.store.GetAgentTaskProfileByType(ctx, taskType)
			if profileErr != nil {
				return NewsContextConfig{}, fmt.Errorf("%w: required review model is not configured", ErrNewsContextPrerequisite)
			}
			if _, modelErr := s.resolveModel(ctx, profile); modelErr != nil {
				return NewsContextConfig{}, fmt.Errorf("%w: required review model is unavailable", ErrNewsContextPrerequisite)
			}
		}
		embedCfg, embedErr := s.embeddingConfigOrDefault(ctx)
		if embedErr != nil || !embedCfg.Enabled || strings.TrimSpace(embedCfg.EmbeddingModelID) == "" {
			return NewsContextConfig{}, fmt.Errorf("%w: theme embedding is unavailable", ErrNewsContextPrerequisite)
		}
	}
	if req.AutoCleanupEnabled != nil && *req.AutoCleanupEnabled {
		embedCfg, embedErr := s.embeddingConfigOrDefault(ctx)
		if embedErr != nil || !embedCfg.Enabled || strings.TrimSpace(embedCfg.EmbeddingModelID) == "" {
			return NewsContextConfig{}, fmt.Errorf("%w: theme embedding is unavailable", ErrNewsContextPrerequisite)
		}
	}
	return s.UpdateNewsContextConfig(ctx, cfg)
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
			if err := s.startDueNewsContextCleanup(ctx, time.Now()); err != nil &&
				!errors.Is(err, ErrNewsContextCleanupDisabled) &&
				!errors.Is(err, ErrNewsContextCleanupRunning) && s.log != nil {
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
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return err
	} else if running {
		return ErrNewsContextAlreadyRunning
	}
	windowType := ""
	switch {
	case cfg.DailyEnabled && (cfg.NextDailyAt.IsZero() || !cfg.NextDailyAt.After(now)):
		windowType = NewsContextWindowDaily
	case cfg.FourHourEnabled && (cfg.NextFourHourAt.IsZero() || !cfg.NextFourHourAt.After(now)):
		windowType = NewsContextWindowFourHour
	case cfg.HourlyEnabled && (cfg.NextHourlyAt.IsZero() || !cfg.NextHourlyAt.After(now)):
		windowType = NewsContextWindowHourly
	default:
		return nil
	}
	_, err = s.startNewsContextRun(ctx, RequestStartNewsContextRun{WindowType: windowType, RequestedBy: "system"}, NewsContextTriggerScheduled, true)
	return err
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
	if err := s.seedNewsContextRunItems(ctx, &run); err != nil {
		run.Status = NewsContextRunStatusFailed
		run.Phase = "collecting"
		run.ErrorMessage = safelog.Text(err.Error(), 500)
		run.FinishedAt = time.Now()
		_, _ = s.store.UpdateNewsContextRun(ctx, run)
		s.recordNewsContextRunFailure(ctx, run.WindowType, err)
		return run, err
	}
	run.Status = NewsContextRunStatusRunning
	run.Phase = "aggregating"
	run.StartedAt = time.Now()
	run, err = s.store.UpdateNewsContextRun(ctx, run)
	if err != nil {
		return NewsContextRun{}, err
	}
	release = false
	if async {
		go s.executeNewsContextRun(context.Background(), run.ID)
		return run, nil
	}
	s.executeNewsContextRun(ctx, run.ID)
	return s.store.GetNewsContextRun(ctx, run.ID)
}

func (s *Service) seedNewsContextRunItems(ctx context.Context, run *NewsContextRun) error {
	items := make([]NewsContextRunItem, 0, newsContextSeedPageSize)
	flush := func() error {
		if len(items) == 0 {
			return nil
		}
		for i := range items {
			items[i].RunID = run.ID
		}
		if err := s.store.AddNewsContextRunItems(ctx, items); err != nil {
			return err
		}
		run.InputCount += len(items)
		items = items[:0]
		return nil
	}
	// No lower bound here: late or previously failed news remains eligible and is
	// eventually consumed instead of being stranded outside a nominal window.
	for offset := 0; ; offset += newsContextSeedPageSize {
		events, err := s.store.ListNewsEventsPendingContext(ctx, run.WindowEnd, newsContextSeedPageSize, offset)
		if err != nil {
			return err
		}
		for _, event := range events {
			items = append(items, NewsContextRunItem{ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID, Status: NewsContextRunItemPending})
		}
		if err := flush(); err != nil {
			return err
		}
		if len(events) < newsContextSeedPageSize {
			break
		}
	}
	if run.WindowType != NewsContextWindowHourly {
		threadFilter := NewsThreadListFilter{
			Status: NewsThreadStatusActive,
			Limit:  newsContextSeedPageSize,
		}
		if run.WindowType == NewsContextWindowFourHour {
			threadFilter.Since = run.WindowStart
			threadFilter.Until = run.WindowEnd
		}
		for offset := 0; ; offset += newsContextSeedPageSize {
			// ponytail: daily reuses the existing run-item table as its complete
			// paged manifest; no separate snapshot queue is needed.
			threadFilter.Offset = offset
			threads, err := s.store.ListNewsThreads(ctx, threadFilter)
			if err != nil {
				return err
			}
			for _, thread := range threads {
				items = append(items, NewsContextRunItem{ObjectType: NewsContextRunItemThread, ObjectID: thread.ID, Status: NewsContextRunItemPending})
			}
			if err := flush(); err != nil {
				return err
			}
			if len(threads) < newsContextSeedPageSize {
				break
			}
		}
	}
	run.PendingCount = run.InputCount
	return nil
}

func (s *Service) executeNewsContextRun(ctx context.Context, runID string) {
	defer s.finishNewsContextRun()
	run, err := s.store.GetNewsContextRun(ctx, runID)
	if err != nil {
		return
	}
	fail := func(cause error) {
		run.Status = NewsContextRunStatusFailed
		run.ErrorMessage = safelog.Text(cause.Error(), 500)
		run.FinishedAt = time.Now()
		run.Phase = "failed"
		_, _ = s.store.UpdateNewsContextRun(context.Background(), run)
		s.recordNewsContextRunFailure(context.Background(), run.WindowType, cause)
	}
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		fail(err)
		return
	}
	for {
		items, err := s.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
			RunID:  run.ID,
			Status: NewsContextRunItemPending,
			Limit:  cfg.BatchSize,
		})
		if err != nil {
			fail(err)
			return
		}
		if len(items) == 0 {
			break
		}
		pack, err := s.buildNewsContextAggregationPack(ctx, run, items)
		if err != nil {
			fail(err)
			return
		}
		resolution, err := s.ResolveAgentTask(ctx, AgentTaskTypeNewsEventReview, "news_context_run", run.ID, "system")
		if err != nil {
			fail(err)
			return
		}
		if resolution.Run == nil || resolution.DecisionLedger == nil {
			fail(errors.New("no news context agent run created"))
			return
		}
		if _, err := s.store.MarkNewsContextRunItemsRunning(ctx, run.ID, resolution.Run.ID, newsContextRunItemObjectIDs(items)); err != nil {
			fail(err)
			return
		}
		run.CurrentAgentRunID = resolution.Run.ID
		if run, err = s.store.UpdateNewsContextRun(ctx, run); err != nil {
			fail(err)
			return
		}
		if _, _, err := s.executeNewsContextAgentRun(ctx, *resolution.Run, *resolution.DecisionLedger, pack, resolution.ModelName); err != nil {
			fail(err)
			return
		}
		finalAgentRun, err := s.store.GetAgentRun(ctx, resolution.Run.ID)
		if err != nil || finalAgentRun.Status != AgentRunStatusCompleted {
			if err == nil {
				err = errors.New(firstNonEmpty(finalAgentRun.ErrorMessage, "news context agent run failed"))
			}
			fail(err)
			return
		}
		run, err = s.store.GetNewsContextRun(ctx, run.ID)
		if err != nil {
			return
		}
	}
	if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
		fail(err)
	}
}

func (s *Service) recordNewsContextRunFailure(ctx context.Context, windowType string, cause error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	cfg.LastRunAt = now
	cfg.LastError = safelog.Text(cause.Error(), 500)
	switch windowType {
	case NewsContextWindowDaily:
		cfg.NextDailyAt = now.Add(time.Duration(cfg.DailyIntervalSeconds) * time.Second)
	case NewsContextWindowFourHour:
		cfg.NextFourHourAt = now.Add(time.Duration(cfg.FourHourIntervalSeconds) * time.Second)
	default:
		cfg.NextHourlyAt = now.Add(time.Duration(cfg.HourlyIntervalSeconds) * time.Second)
	}
	_, _ = s.store.UpsertNewsContextConfig(ctx, cfg)
}

func (s *Service) executeNewsContextAgentRun(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack NewsContextAggregationPack, modelName string) (AgentRun, AgentDecisionLedger, error) {
	if s.newsContextExecutor == nil {
		err := ErrAgentExecutorUnavailable
		s.finalizeAgentRun(ctx, run.ID, nil, err)
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
		return finalRun, finalLedger, err
	}
	running := run
	running.Status = AgentRunStatusRunning
	_, _ = s.store.UpdateAgentRun(ctx, running)
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	output, execErr := s.newsContextExecutor.ExecuteNewsContextAggregation(ctx, taskID, pack, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, output, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execErr
}

func (s *Service) buildNewsContextAggregationPack(ctx context.Context, run NewsContextRun, items []NewsContextRunItem) (NewsContextAggregationPack, error) {
	pack := NewsContextAggregationPack{
		RunID:       run.ID,
		WindowType:  run.WindowType,
		WindowStart: run.WindowStart,
		WindowEnd:   run.WindowEnd,
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
			if newsContextEventRequiresResearch(event) {
				pack.RequiredResearch = true
				pack.ResearchReasons = append(pack.ResearchReasons, "重大、政策、公告或单一来源事项需要公开资料核实")
			}
		case NewsContextRunItemThread:
			thread, err := s.store.GetNewsThread(ctx, item.ObjectID)
			if err != nil {
				return pack, err
			}
			pack.InputThreads = append(pack.InputThreads, thread)
		default:
			return pack, ErrInvalidNewsContextInput
		}
	}
	if len(pack.InputThreads) == 0 && run.WindowType == NewsContextWindowHourly {
		pack.RecentThreads, _ = s.store.ListNewsThreads(ctx, NewsThreadListFilter{Status: NewsThreadStatusActive, Limit: 50})
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
	items, err := s.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: logicalRunID, AgentRunID: agentRunID, Status: NewsContextRunItemRunning, Limit: newsContextMaxBatchSize,
	})
	if err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	if err := validateNewsContextReport(logicalRun, items, report); err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	if err := s.validateNewsContextResearchAudit(ctx, items, report); err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	result, err := s.store.ApplyNewsContextBatch(ctx, logicalRunID, agentRunID, logicalRun.WindowType, report)
	if err != nil {
		return NewsContextBatchApplyResult{}, err
	}
	if result.UrgentReview {
		latest, getErr := s.store.GetNewsContextRun(ctx, logicalRunID)
		if getErr != nil {
			return NewsContextBatchApplyResult{}, getErr
		}
		latest.ReviewStatus = NewsContextReviewPending
		if _, updateErr := s.store.UpdateNewsContextRun(ctx, latest); updateErr != nil {
			return NewsContextBatchApplyResult{}, updateErr
		}
	}
	return result, nil
}

func (s *Service) validateNewsContextResearchAudit(ctx context.Context, items []NewsContextRunItem, report NewsContextReport) error {
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
	for _, audit := range report.SearchAudit {
		status := strings.TrimSpace(audit.Status)
		if strings.TrimSpace(audit.Question) == "" || (status != "completed" && status != "verified" && status != "failed" && status != "unavailable") {
			return fmt.Errorf("%w: invalid public research audit", ErrInvalidNewsContextResult)
		}
		if (status == "completed" || status == "verified") && len(audit.Sources) == 0 {
			return fmt.Errorf("%w: successful public research requires sources", ErrInvalidNewsContextResult)
		}
		if (status == "failed" || status == "unavailable") && strings.TrimSpace(audit.FailureReason) == "" {
			return fmt.Errorf("%w: failed public research requires a reason", ErrInvalidNewsContextResult)
		}
	}
	return nil
}

func validateNewsContextReport(run NewsContextRun, items []NewsContextRunItem, report NewsContextReport) error {
	if report.SchemaVersion != NewsContextResultSchemaVersion || report.RunID != run.ID || report.WindowType != run.WindowType {
		return ErrInvalidNewsContextResult
	}
	expectedNews := map[string]bool{}
	expectedThreads := map[string]bool{}
	for _, item := range items {
		switch item.ObjectType {
		case NewsContextRunItemNewsEvent:
			expectedNews[item.ObjectID] = false
		case NewsContextRunItemThread:
			expectedThreads[item.ObjectID] = false
		}
	}
	for _, id := range report.ProcessedNewsIDs {
		if _, ok := expectedNews[id]; !ok || expectedNews[id] {
			return fmt.Errorf("%w: invalid or duplicate processed news id", ErrInvalidNewsContextResult)
		}
		expectedNews[id] = true
	}
	seenDecision := map[string]bool{}
	for _, decision := range report.NewsDecisions {
		if _, ok := expectedNews[decision.NewsEventID]; !ok || seenDecision[decision.NewsEventID] || strings.TrimSpace(decision.Disposition) == "" {
			return fmt.Errorf("%w: invalid news decision coverage", ErrInvalidNewsContextResult)
		}
		seenDecision[decision.NewsEventID] = true
	}
	for id, covered := range expectedNews {
		if !covered || !seenDecision[id] {
			return fmt.Errorf("%w: news %s was not covered", ErrInvalidNewsContextResult, id)
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
		if run.WindowType == NewsContextWindowDaily && threadOutcomes[id] == "reviewed" {
			return fmt.Errorf("%w: daily thread %s requires a stage change or explicit unchanged conclusion", ErrInvalidNewsContextResult, id)
		}
	}
	return nil
}

func (s *Service) completeNewsContextRun(ctx context.Context, run *NewsContextRun, cfg NewsContextConfig) error {
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
	reviewRequired := run.ReviewStatus == NewsContextReviewPending || run.WindowType == NewsContextWindowDaily ||
		(run.WindowType == NewsContextWindowFourHour && run.MaterialChangeCount > 0)
	run.Phase = "completed"
	run.Status = NewsContextRunStatusCompleted
	run.ReviewStatus = NewsContextReviewNotRequired
	if reviewRequired {
		run.Status = NewsContextRunStatusWaitingReview
		run.ReviewStatus = NewsContextReviewPending
		run.Phase = "waiting_review"
	}
	run.FinishedAt = time.Now()
	run.CurrentAgentRunID = ""
	updated, err := s.store.UpdateNewsContextRun(ctx, *run)
	if err != nil {
		return err
	}
	*run = updated
	if !reviewRequired {
		if err := s.store.UpdateNewsThreadReviewStatusForRun(ctx, run.ID, NewsContextReviewNotRequired, run.FinishedAt); err != nil {
			return err
		}
	}
	cfg.LastRunAt = run.FinishedAt
	cfg.LastError = ""
	switch run.WindowType {
	case NewsContextWindowHourly:
		cfg.NextHourlyAt = run.FinishedAt.Add(time.Duration(cfg.HourlyIntervalSeconds) * time.Second)
	case NewsContextWindowFourHour:
		cfg.NextFourHourAt = run.FinishedAt.Add(time.Duration(cfg.FourHourIntervalSeconds) * time.Second)
	case NewsContextWindowDaily:
		cfg.NextDailyAt = run.FinishedAt.Add(time.Duration(cfg.DailyIntervalSeconds) * time.Second)
	}
	_, _ = s.store.UpsertNewsContextConfig(ctx, cfg)
	go s.maintainAllNewsContextEmbeddings(context.Background())
	if reviewRequired {
		s.triggerNewsContextReview(ctx, run)
	}
	return nil
}

func (s *Service) maintainAllNewsContextEmbeddings(ctx context.Context) {
	for {
		result, err := s.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{
			ObjectTypes: []string{EmbeddingObjectNewsThread, EmbeddingObjectNewsThreadVersion},
			Limit:       newsContextMaxBatchSize,
		})
		if err != nil || result.Status == "running" || result.Total == 0 || result.Failed > 0 {
			return
		}
		if result.Total < newsContextMaxBatchSize {
			return
		}
	}
}

func (s *Service) triggerNewsContextReview(ctx context.Context, run *NewsContextRun) {
	_, err := s.RunPortfolioSentinel(ctx, RequestRunPortfolioSentinel{
		WindowType:       PortfolioSentinelWindowManual,
		StartAt:          run.WindowStart.Format(time.RFC3339Nano),
		EndAt:            run.WindowEnd.Format(time.RFC3339Nano),
		Note:             "复核最近一次消息脉络归纳对当前持仓、监控和策略的影响。",
		NewsContextRunID: run.ID,
	})
	// The sentinel start path binds its run id before it can launch the agent.
	// Reload here so an immediate submission cannot be made invisible by a stale
	// in-memory copy overwriting that association.
	if latest, getErr := s.store.GetNewsContextRun(ctx, run.ID); getErr == nil {
		*run = latest
	}
	if err != nil {
		run.ReviewStatus = NewsContextReviewFailed
		run.Phase = "review_failed"
		run.ErrorMessage = safelog.Text("start portfolio review failed: "+err.Error(), 500)
		_, _ = s.store.UpdateNewsContextRun(context.Background(), *run)
		return
	}
}

func (s *Service) reconcileNewsContextReviews(ctx context.Context) {
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
				run.ReviewStatus = NewsContextReviewFailed
				run.Status = NewsContextRunStatusWaitingReview
				run.ErrorMessage = safelog.Text("update theme review status failed: "+err.Error(), 500)
			}
			_, _ = s.store.UpdateNewsContextRun(ctx, run)
		case PortfolioSentinelStatusFailed:
			run.ReviewStatus = NewsContextReviewFailed
			run.Phase = "review_failed"
			run.ErrorMessage = safelog.Text(sentinel.ErrorMessage, 500)
			_ = s.store.UpdateNewsThreadReviewStatusForRun(ctx, run.ID, NewsContextReviewFailed, time.Now())
			_, _ = s.store.UpdateNewsContextRun(ctx, run)
		}
	}
}

func (s *Service) RetryNewsContextRun(ctx context.Context, id string) (NewsContextRun, error) {
	run, err := s.store.GetNewsContextRun(ctx, strings.TrimSpace(id))
	if err != nil {
		return NewsContextRun{}, err
	}
	if run.Status != NewsContextRunStatusFailed && run.ReviewStatus != NewsContextReviewFailed {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
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
	if err := s.store.ResetFailedNewsContextRunItems(ctx, run.ID); err != nil {
		s.finishNewsContextRun()
		return NewsContextRun{}, err
	}
	run.Status = NewsContextRunStatusRunning
	run.TriggerType = NewsContextTriggerRetry
	run.Phase = "aggregating"
	run.ErrorMessage = ""
	run.FinishedAt = time.Time{}
	run, err = s.store.UpdateNewsContextRun(ctx, run)
	if err != nil {
		s.finishNewsContextRun()
		return NewsContextRun{}, err
	}
	go s.executeNewsContextRun(context.Background(), run.ID)
	return run, nil
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
	if !summary.MCPEnabled {
		summary.MCPError = "本地股票检索服务尚未启动"
	} else if !summary.MCPToolsReady {
		summary.MCPError = "消息脉络检索工具注册不完整"
	}
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
