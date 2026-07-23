package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	newsContextBackfillEstimateTextLimit = 60_000
	newsContextBackfillIndexPageSize     = 10
	newsContextBackfillPhaseIndexing     = "indexing"
)

type newsContextBackfillInputSnapshot struct {
	Versions []NewsThreadVersion
}

func (s *Service) PreviewNewsContextBackfill(ctx context.Context) (NewsContextBackfillPreview, error) {
	cutoff := newsContextBackfillCutoff(time.Now())
	stats, err := s.store.NewsContextBackfillSourceStats(ctx, cutoff)
	if err != nil {
		return NewsContextBackfillPreview{}, err
	}
	preview := NewsContextBackfillPreview{
		TotalNewsCount:   stats.Total,
		PendingNewsCount: stats.Pending + stats.Deferred + stats.Claimed,
		EarliestNewsAt:   stats.EarliestAt,
		LatestNewsAt:     stats.LatestAt,
	}
	// ponytail: this is only an owner-facing estimate. Runtime chunks are built
	// from their actual serialized text and keep splitting until coverage is full.
	if stats.TextBytes > 0 {
		preview.EstimatedChunkCount = int((stats.TextBytes + newsContextBackfillEstimateTextLimit - 1) / newsContextBackfillEstimateTextLimit)
	}
	preview.BlockingReasons = s.newsContextBackfillPrerequisiteReasons(ctx)
	preview.PrerequisitesReady = len(preview.BlockingReasons) == 0
	return preview, nil
}

func (s *Service) newsContextBackfillPrerequisiteReasons(ctx context.Context) []string {
	reasons := make([]string, 0, 3)
	for _, task := range []struct {
		taskType string
		label    string
	}{
		{AgentTaskTypeNewsEventReview, "消息归纳模型尚未配置或不可用"},
		{AgentTaskTypePortfolioSentinel, "组合影响复核模型尚未配置或不可用"},
	} {
		profile, err := s.store.GetAgentTaskProfileByType(ctx, task.taskType)
		if err != nil {
			reasons = append(reasons, task.label)
			continue
		}
		if _, err := s.resolveModel(ctx, profile); err != nil {
			reasons = append(reasons, task.label)
		}
	}
	if _, _, err := s.ensureEmbeddingModelReady(ctx); err != nil {
		reasons = append(reasons, "主题向量模型尚未启用")
	}
	return uniqueNonEmptyStrings(reasons)
}

func newsContextBackfillCutoff(now time.Time) time.Time {
	return now.Truncate(time.Hour)
}

func (s *Service) GetNewsContextBackfill(ctx context.Context) (NewsContextBackfill, error) {
	item, err := s.store.GetLatestNewsContextBackfill(ctx)
	if err != nil {
		return item, err
	}
	// ponytail: high-frequency status reads use the worker's durable progress
	// snapshot. Exact manifest reconciliation remains at state transitions and
	// final safety gates instead of multiplying full-history work per client.
	item, err = s.attachNewsContextBackfillReviewCoverage(ctx, item)
	if err != nil {
		return item, err
	}
	return s.attachNewsContextBackfillStageProgress(ctx, item)
}

func (s *Service) StartNewsContextBackfill(ctx context.Context, req RequestStartNewsContextBackfill) (NewsContextBackfill, error) {
	if !s.tryStartNewsContextRun() {
		return NewsContextBackfill{}, ErrNewsContextAlreadyRunning
	}
	defer s.finishNewsContextRun()
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return NewsContextBackfill{}, err
	} else if running {
		return NewsContextBackfill{}, ErrNewsContextAlreadyRunning
	}
	if existing, found, err := s.store.GetBlockingNewsContextBackfill(ctx); err != nil {
		return NewsContextBackfill{}, err
	} else if found {
		return existing, ErrNewsContextBackfillAlreadyRunning
	}
	preview, err := s.PreviewNewsContextBackfill(ctx)
	if err != nil {
		return NewsContextBackfill{}, err
	}
	if !preview.PrerequisitesReady {
		return NewsContextBackfill{}, fmt.Errorf("%w: %s", ErrNewsContextPrerequisite, strings.Join(preview.BlockingReasons, "；"))
	}
	now := time.Now()
	if _, err := s.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status:      NewsContextBackfillStatusRunning,
		Phase:       "hourly",
		CutoffAt:    newsContextBackfillCutoff(now),
		RequestedBy: strings.TrimSpace(req.RequestedBy),
		StartedAt:   now,
	}); err != nil {
		return NewsContextBackfill{}, err
	}
	s.StartBackground(context.Background())
	return s.GetNewsContextBackfill(ctx)
}

func (s *Service) PauseNewsContextBackfill(ctx context.Context) (NewsContextBackfill, error) {
	item, err := s.store.GetLatestNewsContextBackfill(ctx)
	if err != nil {
		return item, err
	}
	if item.Status != NewsContextBackfillStatusRunning {
		return item, ErrNewsContextBackfillState
	}
	item.Status = NewsContextBackfillStatusPaused
	item.ErrorMessage = ""
	item, err = s.refreshAndSaveNewsContextBackfillOwner(ctx, item)
	if err != nil {
		return item, err
	}
	return s.attachNewsContextBackfillStageProgress(ctx, item)
}

func (s *Service) ResumeNewsContextBackfill(ctx context.Context) (NewsContextBackfill, error) {
	item, err := s.store.GetLatestNewsContextBackfill(ctx)
	if err != nil {
		return item, err
	}
	if item.Status != NewsContextBackfillStatusPaused {
		return item, ErrNewsContextBackfillState
	}
	item.Status = NewsContextBackfillStatusRunning
	item.Phase = firstNonEmpty(newsContextBackfillResumePhase(item), "hourly")
	item.ErrorMessage = ""
	item, err = s.refreshAndSaveNewsContextBackfillOwner(ctx, item)
	if err == nil {
		s.StartBackground(context.Background())
	}
	if err != nil {
		return item, err
	}
	return s.attachNewsContextBackfillStageProgress(ctx, item)
}

func (s *Service) RetryNewsContextBackfill(ctx context.Context) (NewsContextBackfill, error) {
	item, err := s.store.GetLatestNewsContextBackfill(ctx)
	if err != nil {
		return item, err
	}
	if item.Status != NewsContextBackfillStatusFailed && !(item.Status == NewsContextBackfillStatusCompleted && item.MissingNewsCount > 0) {
		return item, ErrNewsContextBackfillState
	}
	if item.CurrentRunID != "" {
		run, runErr := s.store.GetNewsContextRun(ctx, item.CurrentRunID)
		if runErr == nil && item.Phase == "final_review" && run.ReviewStatus == NewsContextReviewFailed {
			if _, err := s.RetryNewsContextRun(ctx, run.ID); err != nil {
				return item, err
			}
		}
		if runErr == nil && run.Status == NewsContextRunStatusFailed {
			if err := s.store.ResetFailedNewsContextRunItems(ctx, run.ID); err != nil {
				return item, err
			}
			run.Status = NewsContextRunStatusPending
			if run.WindowType == NewsContextWindowFourHour {
				run.Phase = "queued"
			} else {
				run.Phase = "collecting"
			}
			run.ErrorMessage = ""
			run.FinishedAt = time.Time{}
			if _, err := s.store.UpdateNewsContextRun(ctx, run); err != nil {
				return item, err
			}
		}
	}
	item.Status = NewsContextBackfillStatusRunning
	item.Phase = firstNonEmpty(newsContextBackfillResumePhase(item), "hourly")
	item.ErrorMessage = ""
	item.CompletedAt = time.Time{}
	item, err = s.refreshAndSaveNewsContextBackfillOwner(ctx, item)
	if err == nil {
		s.StartBackground(context.Background())
	}
	if err != nil {
		return item, err
	}
	return s.attachNewsContextBackfillStageProgress(ctx, item)
}

func newsContextBackfillResumePhase(item NewsContextBackfill) string {
	switch item.Phase {
	case "hourly", "four_hour", "daily", "late_scan", newsContextBackfillPhaseIndexing, "final_review", "finalizing":
		return item.Phase
	}
	return "hourly"
}

func (s *Service) HasBlockingNewsContextBackfill(ctx context.Context) (bool, error) {
	if _, found, err := s.store.GetBlockingNewsContextBackfill(ctx); err != nil || found {
		return found, err
	}
	// ponytail: source state is the final cleanup authority. This single query
	// also blocks cleanup before the owner has created a backfill task and when
	// late historical news arrives after a previously completed task.
	stats, err := s.store.NewsContextBackfillSourceStats(ctx, newsContextBackfillCutoff(time.Now()))
	if err != nil {
		return false, err
	}
	return stats.Pending+stats.Deferred+stats.Claimed > 0, nil
}

func (s *Service) refreshAndSaveNewsContextBackfill(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	item, err := s.refreshNewsContextBackfillProgress(ctx, item)
	if err != nil {
		return item, err
	}
	return s.store.UpdateNewsContextBackfillWorker(ctx, item)
}

func (s *Service) refreshAndSaveNewsContextBackfillOwner(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	item, err := s.refreshNewsContextBackfillProgress(ctx, item)
	if err != nil {
		return item, err
	}
	return s.store.UpdateNewsContextBackfill(ctx, item)
}

func (s *Service) refreshNewsContextBackfillProgress(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	total, err := s.store.CountNewsContextBackfillManifest(ctx, item.ID)
	if err != nil {
		return item, err
	}
	rangeStart, err := s.store.NewsContextBackfillManifestRangeStart(ctx, item.ID, item.CutoffAt)
	if err != nil {
		return item, err
	}
	item.RangeStartAt = rangeStart
	processed, deferred, err := s.store.NewsContextBackfillRunProgress(ctx, item.ID)
	if err != nil {
		return item, err
	}
	remaining := total - processed - deferred
	if remaining < 0 {
		remaining = 0
	}
	item.TotalNewsCount = total
	item.ProcessedNewsCount = processed
	item.RemainingNewsCount = remaining
	item.MissingNewsCount = deferred
	chunks, err := s.store.CountCompletedNewsContextBackfillChunks(ctx, item.ID)
	if err != nil {
		return item, err
	}
	item.CompletedChunkCount = chunks
	return s.attachNewsContextBackfillReviewCoverage(ctx, item)
}

func (s *Service) attachNewsContextBackfillReviewCoverage(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	if strings.TrimSpace(item.FinalReviewRunID) == "" {
		return item, nil
	}
	dailyOutputs, linkedOutputs, err := s.store.NewsContextBackfillReviewCoverage(ctx, item.ID, item.FinalReviewRunID)
	if err != nil {
		return item, err
	}
	item.DailyOutputCount = dailyOutputs
	item.ReviewLinkedCount = linkedOutputs
	item.ReviewMissingCount = dailyOutputs - linkedOutputs
	if item.ReviewMissingCount < 0 {
		item.ReviewMissingCount = 0
	}
	return item, nil
}

func (s *Service) attachNewsContextBackfillStageProgress(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	windows, err := s.store.NewsContextBackfillWindowProgress(ctx, item.ID)
	if err != nil {
		return item, err
	}
	var currentRun *NewsContextRun
	if strings.TrimSpace(item.CurrentRunID) != "" {
		run, runErr := s.store.GetNewsContextRun(ctx, item.CurrentRunID)
		if runErr != nil {
			return item, runErr
		}
		currentRun = &run
	}
	item.StageProgress = buildNewsContextBackfillStageProgress(item, windows, currentRun)
	return item, nil
}

func buildNewsContextBackfillStageProgress(
	item NewsContextBackfill,
	windows map[string]newsContextBackfillWindowProgress,
	currentRun *NewsContextRun,
) []NewsContextBackfillStageProgress {
	progress := make([]NewsContextBackfillStageProgress, 0, 8)
	for _, window := range []struct {
		phase    string
		duration time.Duration
	}{
		{NewsContextWindowHourly, time.Hour},
		{NewsContextWindowFourHour, 4 * time.Hour},
		{NewsContextWindowDaily, 24 * time.Hour},
	} {
		counts := windows[window.phase]
		stage := NewsContextBackfillStageProgress{
			Phase:                window.phase,
			CompletedWindowCount: counts.CompletedWindowCount,
			TotalWindowCount:     newsContextBackfillExpectedChildren(item.RangeStartAt, item.CutoffAt, window.duration),
			ProcessedItemCount:   counts.ProcessedItemCount,
			TotalItemCount:       counts.TotalItemCount,
			PendingItemCount:     counts.PendingItemCount,
			AgentAttemptCount:    counts.AgentAttemptCount,
			AgentRetryCount:      counts.AgentFailedCount,
			ModelDurationSeconds: counts.ModelDurationSeconds,
		}
		if counts.CompletedDurationSeconds > counts.ModelDurationSeconds {
			stage.NonModelDurationSeconds = counts.CompletedDurationSeconds - counts.ModelDurationSeconds
		}
		stage.Status = newsContextBackfillWindowStageStatus(item, stage)
		if currentRun != nil && currentRun.TriggerType == NewsContextTriggerBackfill &&
			currentRun.WindowType == window.phase {
			attachNewsContextBackfillCurrentRun(&stage, *currentRun)
			attachNewsContextBackfillWindowForecast(&stage, counts)
		} else if stage.Status == NewsContextRunStatusCompleted {
			stage.OverallProgress = 1
		}
		progress = append(progress, stage)
	}

	lateScan := NewsContextBackfillStageProgress{Phase: "late_scan"}
	switch item.Phase {
	case "late_scan":
		lateScan.Status = newsContextBackfillActiveStageStatus(item)
	case "final_review", newsContextBackfillPhaseIndexing, "finalizing", "completed":
		lateScan.Status = NewsContextRunStatusCompleted
	default:
		lateScan.Status = NewsContextRunStatusPending
	}
	progress = append(progress, lateScan)

	finalDaily := NewsContextBackfillStageProgress{Phase: "final_daily", Status: NewsContextRunStatusPending}
	if currentRun != nil && currentRun.ID == item.FinalReviewRunID {
		attachNewsContextBackfillCurrentRun(&finalDaily, *currentRun)
		switch {
		case currentRun.Status == NewsContextRunStatusWaitingReview ||
			currentRun.ReviewStatus == NewsContextReviewRunning ||
			currentRun.ReviewStatus == NewsContextReviewCompleted ||
			currentRun.ReviewStatus == NewsContextReviewFailed:
			finalDaily.Status = NewsContextRunStatusCompleted
		case currentRun.Status == NewsContextRunStatusCompleted:
			finalDaily.Status = NewsContextRunStatusCompleted
		case currentRun.Status == NewsContextRunStatusFailed:
			finalDaily.Status = NewsContextRunStatusFailed
		default:
			finalDaily.Status = newsContextBackfillActiveStageStatus(item)
		}
	} else if item.Phase == "final_review" && strings.TrimSpace(item.FinalReviewRunID) == "" {
		finalDaily.Status = newsContextBackfillActiveStageStatus(item)
	} else if item.Phase == newsContextBackfillPhaseIndexing || item.Phase == "finalizing" || item.Phase == "completed" {
		finalDaily.Status = NewsContextRunStatusCompleted
	}
	progress = append(progress, finalDaily)

	indexing := NewsContextBackfillStageProgress{Phase: newsContextBackfillPhaseIndexing, Status: NewsContextRunStatusPending}
	switch {
	case item.Phase == newsContextBackfillPhaseIndexing:
		indexing.Status = newsContextBackfillActiveStageStatus(item)
	case item.Phase == "finalizing" || item.Phase == "completed":
		indexing.Status = NewsContextRunStatusCompleted
	case currentRun != nil && currentRun.ID == item.FinalReviewRunID &&
		(currentRun.Status == NewsContextRunStatusWaitingReview ||
			currentRun.ReviewStatus == NewsContextReviewRunning ||
			currentRun.ReviewStatus == NewsContextReviewCompleted ||
			currentRun.ReviewStatus == NewsContextReviewFailed):
		indexing.Status = NewsContextRunStatusCompleted
	}
	progress = append(progress, indexing)

	finalReview := NewsContextBackfillStageProgress{Phase: "final_review", Status: NewsContextRunStatusPending}
	if item.Phase == "finalizing" || item.Phase == "completed" {
		finalReview.Status = NewsContextRunStatusCompleted
	} else if currentRun != nil && currentRun.ID == item.FinalReviewRunID {
		switch currentRun.ReviewStatus {
		case NewsContextReviewCompleted:
			finalReview.Status = NewsContextRunStatusCompleted
		case NewsContextReviewFailed:
			finalReview.Status = NewsContextRunStatusFailed
		case NewsContextReviewPending, NewsContextReviewRunning:
			if currentRun.Status == NewsContextRunStatusWaitingReview || strings.TrimSpace(currentRun.ReviewRunID) != "" {
				finalReview.Status = newsContextBackfillActiveStageStatus(item)
			}
		}
	}
	progress = append(progress, finalReview)

	finalizing := NewsContextBackfillStageProgress{
		Phase:              "finalizing",
		Status:             NewsContextRunStatusPending,
		ProcessedItemCount: item.ReviewLinkedCount,
		TotalItemCount:     item.DailyOutputCount,
		PendingItemCount:   item.ReviewMissingCount,
	}
	if item.Phase == "finalizing" {
		finalizing.Status = newsContextBackfillActiveStageStatus(item)
	} else if item.Phase == "completed" || item.Status == NewsContextBackfillStatusCompleted {
		finalizing.Status = NewsContextRunStatusCompleted
	}
	progress = append(progress, finalizing)
	return progress
}

func newsContextBackfillWindowStageStatus(item NewsContextBackfill, stage NewsContextBackfillStageProgress) string {
	if item.Status == NewsContextBackfillStatusCompleted ||
		(stage.TotalWindowCount > 0 && stage.CompletedWindowCount >= stage.TotalWindowCount) {
		return NewsContextRunStatusCompleted
	}
	if item.Phase == stage.Phase {
		return newsContextBackfillActiveStageStatus(item)
	}
	return NewsContextRunStatusPending
}

func newsContextBackfillActiveStageStatus(item NewsContextBackfill) string {
	switch item.Status {
	case NewsContextBackfillStatusPaused:
		return NewsContextBackfillStatusPaused
	case NewsContextBackfillStatusFailed:
		return NewsContextRunStatusFailed
	case NewsContextBackfillStatusCompleted:
		return NewsContextRunStatusCompleted
	default:
		return NewsContextRunStatusRunning
	}
}

func attachNewsContextBackfillCurrentRun(stage *NewsContextBackfillStageProgress, run NewsContextRun) {
	stage.ProcessedItemCount = run.ProcessedCount
	stage.TotalItemCount = run.InputCount
	stage.PendingItemCount = run.PendingCount
	stage.CurrentWindowStart = run.WindowStart
	stage.CurrentWindowEnd = run.WindowEnd
	stage.CurrentRunPhase = run.Phase
	stage.CurrentWindowProgress = newsContextBackfillCurrentRunProgress(run)
	if !run.StartedAt.IsZero() {
		end := time.Now()
		if !run.FinishedAt.IsZero() {
			end = run.FinishedAt
		}
		if end.After(run.StartedAt) {
			stage.ElapsedSeconds = int64(end.Sub(run.StartedAt) / time.Second)
		}
	}
}

func newsContextBackfillCurrentRunProgress(run NewsContextRun) float64 {
	if run.Status == NewsContextRunStatusCompleted || run.Status == NewsContextRunStatusWaitingReview {
		return 1
	}
	ratio := 0.0
	if run.InputCount > 0 {
		ratio = float64(run.ProcessedCount) / float64(run.InputCount)
	}
	ratio = min(1, max(0, ratio))
	return ratio
}

func attachNewsContextBackfillWindowForecast(
	stage *NewsContextBackfillStageProgress,
	counts newsContextBackfillWindowProgress,
) {
	if stage.TotalWindowCount <= 0 {
		return
	}
	stage.OverallProgress = min(1, max(0,
		(float64(stage.CompletedWindowCount)+stage.CurrentWindowProgress)/float64(stage.TotalWindowCount),
	))
	if stage.Status != NewsContextRunStatusRunning && stage.Status != NewsContextBackfillStatusPaused {
		return
	}
	currentRemaining := int64(0)
	if stage.CurrentWindowProgress > 0 && stage.CurrentWindowProgress < 1 && stage.ElapsedSeconds > 0 {
		currentRemaining = int64(float64(stage.ElapsedSeconds) *
			(1 - stage.CurrentWindowProgress) / stage.CurrentWindowProgress)
	}
	averageWindowSeconds := int64(0)
	if counts.CompletedWindowCount > 0 {
		averageWindowSeconds = counts.CompletedDurationSeconds / int64(counts.CompletedWindowCount)
	}
	if currentRemaining == 0 {
		currentRemaining = averageWindowSeconds
	}
	remainingWindows := stage.TotalWindowCount - stage.CompletedWindowCount - 1
	if remainingWindows < 0 {
		remainingWindows = 0
	}
	stage.EstimatedRemainingSeconds = currentRemaining + int64(remainingWindows)*averageWindowSeconds
}

// runNewsContextBackfillStep is called only after scheduled daily, four-hour
// and hourly work had a chance to start. It launches one bounded checkpoint,
// materialization page, or four-hour model chunk.
func (s *Service) runNewsContextBackfillStep(ctx context.Context) error {
	item, err := s.store.GetLatestNewsContextBackfill(ctx)
	if errors.Is(err, ErrNewsContextBackfillNotFound) {
		return nil
	}
	if err != nil || item.Status != NewsContextBackfillStatusRunning {
		return err
	}
	if item.Phase == "finalizing" {
		s.startNewsContextBackfillFinalizer(item.ID)
		return nil
	}
	if item.Phase == newsContextBackfillPhaseIndexing {
		return s.startNewsContextBackfillFinalIndexing(item.ID)
	}
	if item.CurrentRunID != "" {
		return s.continueNewsContextBackfillRun(ctx, item)
	}
	switch item.Phase {
	case "four_hour":
		return s.startNextNewsContextBackfillFourHour(ctx, item)
	case "daily":
		return s.startNextNewsContextBackfillDaily(ctx, item)
	case "late_scan":
		return s.scanLateNewsContextBackfillEvents(ctx, item)
	case "final_review":
		return s.startNewsContextBackfillFinalReview(ctx, item)
	default:
		return s.startNextNewsContextBackfillHour(ctx, item)
	}
}

func (s *Service) startNextNewsContextBackfillHour(ctx context.Context, item NewsContextBackfill) error {
	runs, err := s.newsContextBackfillRunMap(ctx, item)
	if err != nil {
		return err
	}
	for start := item.RangeStartAt; !start.Add(time.Hour).After(item.CutoffAt); start = start.Add(time.Hour) {
		end := start.Add(time.Hour)
		if run, ok := runs[newsContextBackfillWindowKey(NewsContextWindowHourly, start)]; ok &&
			run.Status == NewsContextRunStatusCompleted {
			continue
		}
		total, err := s.store.CountNewsContextBackfillManifestInRange(ctx, item.ID, start, end)
		if err != nil {
			return err
		}
		if total == 0 {
			run, err := s.completeEmptyNewsContextBackfillWindow(ctx, item, NewsContextWindowHourly, start, end)
			if err != nil {
				return err
			}
			runs[newsContextBackfillWindowKey(run.WindowType, run.WindowStart)] = run
			// ponytail: one empty checkpoint is one scheduler unit. Returning here
			// keeps realtime work from waiting behind an unbounded historical gap.
			_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
			return err
		}
		return s.startNewsContextBackfillWindow(ctx, item, NewsContextWindowHourly, start, end)
	}
	item.Phase = "four_hour"
	_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
	return err
}

func newsContextBackfillRunTime(run NewsContextRun) time.Time {
	if !run.FinishedAt.IsZero() {
		return run.FinishedAt
	}
	return run.UpdatedAt
}

func newsContextBackfillWindowKey(windowType string, start time.Time) string {
	return windowType + "\x00" + start.UTC().Format(time.RFC3339Nano)
}

func (s *Service) newsContextBackfillRunMap(ctx context.Context, item NewsContextBackfill) (map[string]NewsContextRun, error) {
	runs, err := s.store.ListNewsContextBackfillRuns(ctx, item.ID, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]NewsContextRun, len(runs))
	for _, run := range runs {
		key := newsContextBackfillWindowKey(run.WindowType, run.WindowStart)
		if current, ok := out[key]; !ok || newsContextBackfillRunTime(run).After(newsContextBackfillRunTime(current)) {
			out[key] = run
		}
	}
	return out, nil
}

func newsContextBackfillChildRuns(runs map[string]NewsContextRun, windowType string, start, end time.Time) ([]NewsContextRun, time.Time) {
	children := make([]NewsContextRun, 0)
	latest := time.Time{}
	for _, run := range runs {
		if run.WindowType != windowType || run.WindowStart.Before(start) || run.WindowEnd.After(end) || run.Status != NewsContextRunStatusCompleted {
			continue
		}
		children = append(children, run)
		if at := newsContextBackfillRunTime(run); at.After(latest) {
			latest = at
		}
	}
	return children, latest
}

func newsContextBackfillParentFresh(runs map[string]NewsContextRun, windowType string, start time.Time, childLatest time.Time) bool {
	run, ok := runs[newsContextBackfillWindowKey(windowType, start)]
	return ok && run.Status == NewsContextRunStatusCompleted &&
		(childLatest.IsZero() || !newsContextBackfillRunTime(run).Before(childLatest))
}

func (s *Service) startNextNewsContextBackfillFourHour(ctx context.Context, item NewsContextBackfill) error {
	runs, err := s.newsContextBackfillRunMap(ctx, item)
	if err != nil {
		return err
	}
	for start := item.RangeStartAt; start.Before(item.CutoffAt); start = start.Add(4 * time.Hour) {
		end := start.Add(4 * time.Hour)
		if end.After(item.CutoffAt) {
			end = item.CutoffAt
		}
		pending, err := s.store.CountPendingNewsContextBackfillEventsInRange(ctx, item.ID, start, end)
		if err != nil {
			return err
		}
		children, childLatest := newsContextBackfillChildRuns(runs, NewsContextWindowHourly, start, end)
		if len(children) != newsContextBackfillExpectedChildren(start, end, time.Hour) {
			item.Phase = "hourly"
			_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
			return err
		}
		if pending == 0 && newsContextBackfillParentFresh(runs, NewsContextWindowFourHour, start, childLatest) {
			continue
		}
		if pending == 0 {
			run, err := s.completeEmptyNewsContextBackfillWindow(ctx, item, NewsContextWindowFourHour, start, end)
			if err != nil {
				return err
			}
			runs[newsContextBackfillWindowKey(run.WindowType, run.WindowStart)] = run
			_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
			return err
		}
		return s.startNewsContextBackfillWindow(ctx, item, NewsContextWindowFourHour, start, end)
	}
	item.Phase = "daily"
	_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
	return err
}

func (s *Service) startNextNewsContextBackfillDaily(ctx context.Context, item NewsContextBackfill) error {
	runs, err := s.newsContextBackfillRunMap(ctx, item)
	if err != nil {
		return err
	}
	for start := item.RangeStartAt; start.Before(item.CutoffAt); start = start.Add(24 * time.Hour) {
		end := start.Add(24 * time.Hour)
		if end.After(item.CutoffAt) {
			end = item.CutoffAt
		}
		children, childLatest := newsContextBackfillChildRuns(runs, NewsContextWindowFourHour, start, end)
		if len(children) != newsContextBackfillExpectedChildren(start, end, 4*time.Hour) {
			item.Phase = "four_hour"
			_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
			return err
		}
		if newsContextBackfillParentFresh(runs, NewsContextWindowDaily, start, childLatest) {
			continue
		}
		snapshot, err := s.newsContextBackfillInputVersions(ctx, item, NewsContextWindowDaily, start, end)
		if err != nil {
			return err
		}
		if len(snapshot.Versions) == 0 {
			run, err := s.completeEmptyNewsContextBackfillWindow(ctx, item, NewsContextWindowDaily, start, end)
			if err != nil {
				return err
			}
			runs[newsContextBackfillWindowKey(run.WindowType, run.WindowStart)] = run
			_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
			return err
		}
		return s.startNewsContextBackfillWindow(ctx, item, NewsContextWindowDaily, start, end)
	}
	item.Phase = "late_scan"
	_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
	return err
}

func newsContextBackfillExpectedChildren(start, end time.Time, childWindow time.Duration) int {
	if childWindow <= 0 || !end.After(start) {
		return 0
	}
	// ponytail: historical cutoff is a complete hour but not necessarily a
	// four-hour/day boundary. One short trailing child closes that final range.
	duration := end.Sub(start)
	return int((duration + childWindow - 1) / childWindow)
}

func (s *Service) completeEmptyNewsContextBackfillWindow(ctx context.Context, item NewsContextBackfill, windowType string, start, end time.Time) (NewsContextRun, error) {
	run, err := s.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: windowType, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusPending, Phase: "collecting", WindowStart: start, WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
		RequestedBy: item.RequestedBy,
	})
	if err != nil {
		return run, err
	}
	if err := s.store.LinkNewsContextBackfillRun(ctx, item.ID, run.ID); err != nil {
		return run, err
	}
	count, err := s.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID})
	if err != nil {
		return run, err
	}
	if count != 0 {
		return run, errors.New("empty historical checkpoint has persisted inputs")
	}
	now := time.Now()
	run.TriggerType = NewsContextTriggerBackfill
	run.Status = NewsContextRunStatusCompleted
	run.Phase = "completed"
	run.ReviewStatus = NewsContextReviewNotRequired
	run.InputCount = 0
	run.PendingCount = 0
	run.ErrorMessage = ""
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.FinishedAt = now
	return s.store.UpdateNewsContextRun(ctx, run)
}

func (s *Service) scanLateNewsContextBackfillEvents(ctx context.Context, item NewsContextBackfill) error {
	requeued, err := s.store.RequeueFirstNewsContextBackfillDeferrals(ctx, item.ID)
	if err != nil {
		return err
	}
	inserted, err := s.store.AppendNewsContextBackfillManifest(ctx, item.ID, item.CutoffAt)
	if err != nil {
		return err
	}
	if inserted > 0 || requeued > 0 {
		item.Phase = "four_hour"
	} else {
		item.Phase = "final_review"
	}
	_, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
	return err
}

func (s *Service) continueNewsContextBackfillRun(ctx context.Context, item NewsContextBackfill) error {
	run, err := s.store.GetNewsContextRun(ctx, item.CurrentRunID)
	if err != nil {
		return s.failNewsContextBackfill(ctx, item, err)
	}
	if item.Phase == "final_review" {
		if run.Status == NewsContextRunStatusWaitingReview && run.ReviewStatus == NewsContextReviewPending && strings.TrimSpace(run.ReviewRunID) == "" {
			s.triggerNewsContextReview(ctx, &run)
			if run.ReviewStatus == NewsContextReviewFailed {
				return s.failNewsContextBackfill(ctx, item, errors.New(firstNonEmpty(run.ErrorMessage, "final impact review failed")))
			}
			return nil
		}
		if run.ReviewStatus == NewsContextReviewFailed {
			return s.failNewsContextBackfill(ctx, item, errors.New(firstNonEmpty(run.ErrorMessage, "final impact review failed")))
		}
		switch run.Status {
		case NewsContextRunStatusCompleted:
			if run.ReviewStatus == NewsContextReviewCompleted {
				return s.beginNewsContextBackfillFinalization(ctx, item)
			}
			if strings.TrimSpace(run.ReviewRunID) == "" &&
				(run.ReviewStatus == NewsContextReviewPending || run.ReviewStatus == NewsContextReviewNotRequired) {
				item.Phase = newsContextBackfillPhaseIndexing
				_, err := s.refreshAndSaveNewsContextBackfill(ctx, item)
				return err
			}
			return nil
		case NewsContextRunStatusFailed:
			return s.failNewsContextBackfill(ctx, item, errors.New(firstNonEmpty(run.ErrorMessage, "final review failed")))
		case NewsContextRunStatusPending:
			return s.resumeNewsContextRun(ctx, run, false)
		default:
			return nil
		}
	}
	switch run.Status {
	case NewsContextRunStatusCompleted:
		return s.advanceNewsContextBackfillAfterRun(ctx, item, run)
	case NewsContextRunStatusFailed:
		return s.failNewsContextBackfill(ctx, item, errors.New(firstNonEmpty(run.ErrorMessage, "historical aggregation failed")))
	case NewsContextRunStatusPending:
		return s.resumeNewsContextRun(ctx, run, true)
	default:
		return nil
	}
}

func newsContextBackfillWindowPhase(windowType string) string {
	switch windowType {
	case NewsContextWindowFourHour:
		return "four_hour"
	case NewsContextWindowDaily:
		return "daily"
	default:
		return "hourly"
	}
}

func (s *Service) advanceNewsContextBackfillAfterRun(ctx context.Context, item NewsContextBackfill, run NewsContextRun) error {
	item.CurrentRunID = ""
	item.CurrentWindowStart = time.Time{}
	item.CurrentWindowEnd = time.Time{}
	_, err := s.refreshAndSaveNewsContextBackfill(ctx, item)
	return err
}

func (s *Service) resumeNewsContextRun(ctx context.Context, run NewsContextRun, backfillChunk bool) error {
	if !s.tryStartNewsContextRun() {
		return ErrNewsContextAlreadyRunning
	}
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		s.finishNewsContextRun()
		return err
	} else if running {
		s.finishNewsContextRun()
		return ErrNewsContextAlreadyRunning
	}
	var err error
	if backfillChunk {
		backfill, found, err := s.store.NewsContextBackfillForRun(ctx, run.ID)
		if err != nil || !found {
			s.finishNewsContextRun()
			if err != nil {
				return err
			}
			return ErrNewsContextBackfillNotFound
		}
		run, err = s.store.BeginNewsContextBackfillFragment(ctx, backfill.ID, run.ID)
		if errors.Is(err, ErrNewsContextBackfillState) {
			s.finishNewsContextRun()
			return nil
		}
		if err != nil {
			s.finishNewsContextRun()
			return err
		}
		if run.Phase == "collecting" {
			if err := s.prepareNewsContextBackfillRun(ctx, backfill, &run); err != nil {
				_ = s.failNewsContextBackfillRun(ctx, run, err)
				s.finishNewsContextRun()
				return err
			}
		}
	} else {
		if run.Phase == "collecting" {
			run, err = s.preparePendingNewsContextRun(ctx, run)
		} else {
			run.Status = NewsContextRunStatusRunning
			if run.Phase != "indexing" {
				switch run.WindowType {
				case NewsContextWindowHourly:
					run.Phase = newsContextRunPhaseCheckpoint
				case NewsContextWindowDaily:
					run.Phase = newsContextRunPhaseMaterialize
				default:
					run.Phase = newsContextRunPhaseAggregating
				}
			}
			run.ErrorMessage = ""
			run, err = s.store.UpdateNewsContextRun(ctx, run)
		}
		if err != nil {
			if errors.Is(err, ErrNewsContextAlreadyRunning) {
				s.finishNewsContextRun()
				return err
			}
			_ = s.failNewsContextBackfillRun(ctx, run, err)
			s.finishNewsContextRun()
			return err
		}
	}
	if backfillChunk {
		if !s.launchNewsContextWorker(run.ID, s.executeNewsContextBackfillChunk) {
			s.finishNewsContextRun()
			return context.Canceled
		}
	} else {
		if !s.launchNewsContextWorker(run.ID, s.executeNewsContextRun) {
			s.finishNewsContextRun()
			return context.Canceled
		}
	}
	return nil
}

func (s *Service) startNewsContextBackfillWindow(ctx context.Context, item NewsContextBackfill, windowType string, start, end time.Time) error {
	if !s.tryStartNewsContextRun() {
		return ErrNewsContextAlreadyRunning
	}
	release := true
	defer func() {
		if release {
			s.finishNewsContextRun()
		}
	}()
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return err
	} else if running {
		return ErrNewsContextAlreadyRunning
	}
	run, err := s.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: windowType, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusPending, Phase: "collecting", WindowStart: start,
		WindowEnd: end, ReviewStatus: NewsContextReviewNotRequired,
		CleanupStatus: NewsContextCleanupPending, RequestedBy: item.RequestedBy,
	})
	if err != nil {
		return err
	}
	if run.Status == NewsContextRunStatusRunning || run.Status == NewsContextRunStatusWaitingReview {
		return ErrNewsContextAlreadyRunning
	}
	run.TriggerType = NewsContextTriggerBackfill
	run.Status = NewsContextRunStatusPending
	run.Phase = "collecting"
	run.ReviewStatus = NewsContextReviewNotRequired
	run.ErrorMessage = ""
	run.FinishedAt = time.Time{}
	if _, err := s.store.UpdateNewsContextRun(ctx, run); err != nil {
		return err
	}
	item, err = s.store.ReserveNewsContextBackfillRun(ctx, item.ID, run)
	if errors.Is(err, ErrNewsContextBackfillState) {
		return nil
	}
	if err != nil {
		return err
	}
	run, err = s.store.BeginNewsContextBackfillFragment(ctx, item.ID, run.ID)
	if errors.Is(err, ErrNewsContextBackfillState) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.prepareNewsContextBackfillRun(ctx, item, &run); err != nil {
		_ = s.failNewsContextBackfillRun(ctx, run, err)
		return err
	}
	if !s.launchNewsContextWorker(run.ID, s.executeNewsContextBackfillChunk) {
		return context.Canceled
	}
	release = false
	return nil
}

func (s *Service) prepareNewsContextBackfillRun(ctx context.Context, item NewsContextBackfill, run *NewsContextRun) error {
	switch run.WindowType {
	case NewsContextWindowHourly:
		count, err := s.store.CountNewsContextBackfillManifestInRange(ctx, item.ID, run.WindowStart, run.WindowEnd)
		if err != nil {
			return err
		}
		run.InputCount = count
		run.ProcessedCount = count
		run.PendingCount = 0
		run.Phase = newsContextRunPhaseCheckpoint
	case NewsContextWindowFourHour:
		ids, claimErr := s.store.ClaimNewsContextBackfillEvents(ctx, item.ID, run.ID, run.WindowStart, run.WindowEnd)
		if claimErr != nil {
			return claimErr
		}
		if err := s.store.RequeueNewsContextRunEventItems(ctx, run.ID, ids); err != nil {
			_ = s.store.ReleaseNewsContextEventClaims(ctx, run.ID)
			return err
		}
		if err := s.store.refreshNewsContextRunCounts(ctx, run.ID); err != nil {
			return err
		}
		updated, err := s.store.GetNewsContextRun(ctx, run.ID)
		if err != nil {
			return err
		}
		run.InputCount = updated.InputCount
		run.ProcessedCount = updated.ProcessedCount
		run.PendingCount = updated.PendingCount
		run.Phase = newsContextRunPhaseAggregating
	case NewsContextWindowDaily:
		snapshot, err := s.newsContextBackfillInputVersions(ctx, item, run.WindowType, run.WindowStart, run.WindowEnd)
		if err != nil {
			return err
		}
		versions, err := s.store.MaterializeNewsContextDailyVersions(ctx, run.ID, run.WindowEnd, snapshot.Versions)
		if err != nil {
			return err
		}
		if err := s.store.ReplaceNewsContextMaterializedThreadItems(ctx, run.ID, versions); err != nil {
			return err
		}
		run.InputCount = len(versions)
		run.ProcessedCount = len(versions)
		run.PendingCount = 0
		run.CreatedThreadCount = 0
		run.UpdatedThreadCount = 0
		run.MaterialChangeCount = 0
		run.ConflictCount = 0
		run.ResearchCount = 0
		run.Phase = newsContextRunPhaseMaterialize
	default:
		return ErrInvalidNewsContextInput
	}
	run.TriggerType = NewsContextTriggerBackfill
	run.Status = NewsContextRunStatusRunning
	run.ReviewStatus = NewsContextReviewNotRequired
	run.ErrorMessage = ""
	run.FinishedAt = time.Time{}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	updated, err := s.store.UpdateNewsContextRun(ctx, *run)
	if err != nil {
		return err
	}
	*run = updated
	return nil
}

func (s *Service) failNewsContextBackfillRun(ctx context.Context, run NewsContextRun, cause error) error {
	if cause == nil {
		return nil
	}
	if latest, err := s.store.GetNewsContextRun(ctx, run.ID); err == nil {
		run = latest
	}
	run.Status = NewsContextRunStatusFailed
	run.Phase = "failed"
	run.ErrorMessage = safelog.Text(cause.Error(), 500)
	run.FinishedAt = time.Now()
	_, _ = s.store.UpdateNewsContextRun(ctx, run)
	backfill, found, err := s.store.NewsContextBackfillForRun(ctx, run.ID)
	if err == nil && !found {
		backfill, found, err = s.store.NewsContextBackfillForFinalReviewRun(ctx, run.ID)
	}
	if err == nil && found {
		_ = s.failNewsContextBackfill(ctx, backfill, cause)
	}
	return cause
}

func (s *Service) newsContextBackfillInputVersions(ctx context.Context, item NewsContextBackfill, windowType string, start, end time.Time) (newsContextBackfillInputSnapshot, error) {
	if windowType != NewsContextWindowDaily {
		return newsContextBackfillInputSnapshot{}, ErrInvalidNewsContextInput
	}
	ids, err := s.store.ListNewsContextBackfillOutputVersionIDs(ctx, item.ID, NewsContextWindowFourHour, start, end)
	if err != nil {
		return newsContextBackfillInputSnapshot{}, err
	}
	latest := make(map[string]NewsThreadVersion)
	appendVersion := func(version NewsThreadVersion) {
		if version.ID == "" || version.ThreadID == "" || version.EffectiveAt.After(end) {
			return
		}
		if current, found := latest[version.ThreadID]; !found || newsContextVersionAfter(version, current) {
			latest[version.ThreadID] = version
		}
	}
	for _, id := range ids {
		version, err := s.store.GetNewsThreadVersion(ctx, id)
		if err != nil {
			return newsContextBackfillInputSnapshot{}, err
		}
		appendVersion(version)
	}
	return newsContextBackfillInputSnapshot{Versions: sortedNewsContextVersions(latest)}, nil
}

func (s *Service) executeNewsContextBackfillChunk(ctx context.Context, runID string) {
	defer s.finishNewsContextRun()
	run, err := s.store.GetNewsContextRun(ctx, runID)
	if err != nil {
		return
	}
	fail := func(cause error) {
		if s.newsContextWorkerShutdownCanceled(ctx) {
			return
		}
		if latest, getErr := s.store.GetNewsContextRun(context.Background(), run.ID); getErr == nil {
			run = latest
		}
		run.Status = NewsContextRunStatusFailed
		run.Phase = "failed"
		run.ErrorMessage = safelog.Text(cause.Error(), 500)
		run.FinishedAt = time.Now()
		_, _ = s.store.UpdateNewsContextRun(context.Background(), run)
		if backfill, found, lookupErr := s.store.NewsContextBackfillForRun(context.Background(), run.ID); lookupErr == nil && found {
			_ = s.failNewsContextBackfill(context.Background(), backfill, cause)
		}
	}
	if run.WindowType != NewsContextWindowFourHour {
		cfg, cfgErr := s.GetNewsContextConfig(ctx)
		if cfgErr != nil {
			fail(cfgErr)
			return
		}
		if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
			fail(err)
		}
		return
	}
	indexesReady, err := s.repairNewsContextRunEmbeddingsPage(ctx, run.ID)
	if err != nil {
		fail(err)
		return
	}
	if !indexesReady {
		run.Status = NewsContextRunStatusPending
		run.ErrorMessage = ""
		run.FinishedAt = time.Time{}
		if _, err := s.store.UpdateNewsContextRun(ctx, run); err != nil {
			fail(err)
			return
		}
		if backfill, found, lookupErr := s.store.NewsContextBackfillForRun(ctx, run.ID); lookupErr == nil && found {
			_, _ = s.refreshAndSaveNewsContextBackfill(ctx, backfill)
		}
		return
	}
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
	if len(items) == 0 {
		cfg, cfgErr := s.GetNewsContextConfig(ctx)
		if cfgErr != nil {
			fail(cfgErr)
			return
		}
		if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
			fail(err)
		}
		return
	}
	batchCfg, _ := s.GetNewsContextConfig(ctx)
	if err := s.executeNewsContextBatchWithRetry(ctx, &run, batchCfg, items); err != nil {
		fail(err)
		return
	}
	run, err = s.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		fail(err)
		return
	}
	run.CurrentAgentRunID = ""
	pending, err := s.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Status: NewsContextRunItemPending})
	if err != nil {
		fail(err)
		return
	}
	if pending > 0 {
		run.Status = NewsContextRunStatusPending
		run.Phase = "queued"
		if _, err := s.store.UpdateNewsContextRun(ctx, run); err != nil {
			fail(err)
		}
		if backfill, found, lookupErr := s.store.NewsContextBackfillForRun(context.Background(), run.ID); lookupErr == nil && found {
			_, _ = s.refreshAndSaveNewsContextBackfill(context.Background(), backfill)
		}
		return
	}
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		fail(err)
		return
	}
	if err := s.completeNewsContextRun(ctx, &run, cfg); err != nil {
		fail(err)
	}
}

func (s *Service) startNewsContextBackfillFinalReview(ctx context.Context, item NewsContextBackfill) error {
	if item.CurrentRunID != "" {
		return nil
	}
	item, err := s.refreshNewsContextBackfillProgress(ctx, item)
	if err != nil {
		return err
	}
	if item.RemainingNewsCount > 0 || item.MissingNewsCount > 0 {
		return s.failNewsContextBackfill(ctx, item, errors.New("historical news manifest is not covered exactly once"))
	}
	if !s.tryStartNewsContextRun() {
		return ErrNewsContextAlreadyRunning
	}
	release := true
	defer func() {
		if release {
			s.finishNewsContextRun()
		}
	}()
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return err
	} else if running {
		return ErrNewsContextAlreadyRunning
	}
	now := time.Now()
	startAt := item.CutoffAt
	if startAt.After(now) {
		startAt = now
	}
	run, err := s.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, Phase: "collecting", WindowStart: startAt,
		WindowEnd: now, ReviewStatus: NewsContextReviewNotRequired,
		CleanupStatus: NewsContextCleanupPending, RequestedBy: "system",
	})
	if err != nil {
		return s.failNewsContextBackfill(ctx, item, err)
	}
	if run.Status != NewsContextRunStatusPending {
		return s.failNewsContextBackfill(ctx, item, ErrNewsContextAlreadyRunning)
	}
	item, err = s.store.ReserveNewsContextBackfillFinalReviewRun(ctx, item, run)
	if errors.Is(err, ErrNewsContextBackfillState) {
		return nil
	}
	if err != nil {
		return s.failNewsContextBackfill(ctx, item, err)
	}
	run, err = s.preparePendingNewsContextRun(ctx, run)
	if err != nil {
		if errors.Is(err, ErrNewsContextAlreadyRunning) {
			return err
		}
		return s.failNewsContextBackfillRun(ctx, run, err)
	}
	if !s.launchNewsContextWorker(run.ID, s.executeNewsContextRun) {
		return context.Canceled
	}
	release = false
	return nil
}

func (s *Service) startNewsContextBackfillFinalIndexing(id string) error {
	if !s.tryStartNewsContextRun() {
		return ErrNewsContextAlreadyRunning
	}
	if running, err := s.store.HasRunningNewsContextRun(context.Background()); err != nil {
		s.finishNewsContextRun()
		return err
	} else if running {
		s.finishNewsContextRun()
		return ErrNewsContextAlreadyRunning
	}
	go func() {
		defer s.finishNewsContextRun()
		s.processNewsContextBackfillFinalIndexPage(context.Background(), id)
	}()
	return nil
}

func (s *Service) processNewsContextBackfillFinalIndexPage(ctx context.Context, id string) {
	item, err := s.store.GetNewsContextBackfill(ctx, id)
	if err != nil || item.Status != NewsContextBackfillStatusRunning || item.Phase != newsContextBackfillPhaseIndexing {
		return
	}
	fail := func(cause error) {
		_ = s.failNewsContextBackfill(context.Background(), item, cause)
	}
	if item.CurrentRunID == "" || item.CurrentRunID != item.FinalReviewRunID {
		fail(errors.New("historical final current daily run is not durably linked"))
		return
	}
	finalReview, err := s.store.GetNewsContextRun(ctx, item.FinalReviewRunID)
	if err != nil {
		fail(err)
		return
	}
	if finalReview.Status != NewsContextRunStatusCompleted ||
		(finalReview.ReviewStatus != NewsContextReviewPending && finalReview.ReviewStatus != NewsContextReviewNotRequired) ||
		strings.TrimSpace(finalReview.ReviewRunID) != "" {
		fail(errors.New("historical final current daily run is not ready for indexing"))
		return
	}
	threadIDs, indexesReady, err := s.ensureNewsContextBackfillFinalIndexes(ctx, finalReview.WindowEnd)
	if err != nil {
		fail(err)
		return
	}
	if !indexesReady {
		return
	}
	if len(threadIDs) == 0 {
		fail(errors.New("historical news produced no searchable current theme"))
		return
	}
	item.Phase = "final_review"
	item.ErrorMessage = ""
	item, err = s.refreshAndSaveNewsContextBackfill(ctx, item)
	if err != nil {
		fail(err)
		return
	}
	if item.Status != NewsContextBackfillStatusRunning || item.Phase != "final_review" {
		return
	}
	finalReview, err = s.store.GetNewsContextRun(ctx, finalReview.ID)
	if err != nil {
		fail(err)
		return
	}
	if finalReview.Status != NewsContextRunStatusCompleted || strings.TrimSpace(finalReview.ReviewRunID) != "" {
		return
	}
	finalReview.Status = NewsContextRunStatusWaitingReview
	finalReview.ReviewStatus = NewsContextReviewPending
	finalReview.Phase = "waiting_review"
	finalReview.ErrorMessage = ""
	finalReview, err = s.store.UpdateNewsContextRun(ctx, finalReview)
	if err != nil {
		fail(err)
		return
	}
	s.triggerNewsContextReview(ctx, &finalReview)
	if finalReview.ReviewStatus == NewsContextReviewFailed {
		fail(errors.New(firstNonEmpty(finalReview.ErrorMessage, "final impact review failed")))
	}
}

func (s *Service) beginNewsContextBackfillFinalization(ctx context.Context, item NewsContextBackfill) error {
	item.CurrentRunID = ""
	item.CurrentWindowStart = time.Time{}
	item.CurrentWindowEnd = time.Time{}
	item.Phase = "finalizing"
	item.ErrorMessage = ""
	if _, err := s.refreshAndSaveNewsContextBackfill(ctx, item); err != nil {
		return err
	}
	s.startNewsContextBackfillFinalizer(item.ID)
	return nil
}

func (s *Service) startNewsContextBackfillFinalizer(id string) {
	if !s.tryStartNewsContextRun() {
		return
	}
	s.newsBackfillMu.Lock()
	if s.newsBackfillRun {
		s.newsBackfillMu.Unlock()
		s.finishNewsContextRun()
		return
	}
	s.newsBackfillRun = true
	s.newsBackfillMu.Unlock()
	go func() {
		defer func() {
			s.finishNewsContextRun()
			s.newsBackfillMu.Lock()
			s.newsBackfillRun = false
			s.newsBackfillMu.Unlock()
		}()
		s.finalizeNewsContextBackfill(context.Background(), id)
	}()
}

func (s *Service) finalizeNewsContextBackfill(ctx context.Context, id string) {
	item, err := s.store.GetNewsContextBackfill(ctx, id)
	if err != nil || item.Status != NewsContextBackfillStatusRunning || item.Phase != "finalizing" {
		return
	}
	fail := func(cause error) {
		_ = s.failNewsContextBackfill(context.Background(), item, cause)
	}
	finalReview, err := s.store.GetNewsContextRun(ctx, item.FinalReviewRunID)
	if err != nil || finalReview.Status != NewsContextRunStatusCompleted || finalReview.ReviewStatus != NewsContextReviewCompleted {
		if err == nil {
			err = errors.New("historical final impact review is incomplete")
		}
		fail(err)
		return
	}
	threadIDs, indexesReady, err := s.verifyNewsContextBackfillFinalIndexes(ctx, finalReview.WindowEnd)
	if err != nil {
		fail(err)
		return
	}
	if !indexesReady {
		fail(errors.New("historical final indexes changed after impact review"))
		return
	}
	reviewedOutputsReady, err := s.ensureNewsContextBackfillReviewedDailyOutputs(ctx, item, finalReview)
	if err != nil {
		fail(err)
		return
	}
	if !reviewedOutputsReady {
		return
	}
	if len(threadIDs) == 0 {
		fail(errors.New("historical news produced no searchable current theme"))
		return
	}
	if err := s.VerifyNewsContextMCPProbe(ctx, threadIDs[0]); err != nil {
		fail(fmt.Errorf("verify news context MCP: %w", err))
		return
	}
	if err := s.PruneTransientNewsContextEmbeddings(ctx, item.CutoffAt); err != nil {
		fail(fmt.Errorf("prune temporary news context indexes: %w", err))
		return
	}
	latest, err := s.store.GetNewsContextBackfill(ctx, item.ID)
	if err != nil || latest.Status != NewsContextBackfillStatusRunning {
		return
	}
	item = latest
	item, err = s.refreshNewsContextBackfillProgress(ctx, item)
	if err != nil {
		fail(err)
		return
	}
	if err := s.validateNewsContextBackfillFinalCoverage(ctx, item); err != nil {
		fail(err)
		return
	}
	item.Status = NewsContextBackfillStatusCompleted
	item.Phase = "completed"
	item.ErrorMessage = ""
	item.CompletedAt = time.Now()
	if _, err := s.store.UpdateNewsContextBackfillWorker(ctx, item); err != nil && s.log != nil {
		s.log.Warn("complete news context backfill failed", "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) ensureNewsContextBackfillReviewedDailyOutputs(ctx context.Context, item NewsContextBackfill, finalReview NewsContextRun) (bool, error) {
	const pageSize = 100
	outputs, err := s.store.ListPendingNewsContextBackfillDailyOutputs(ctx, item.ID, finalReview.ID, pageSize)
	if err != nil {
		return false, err
	}
	if len(outputs) == 0 {
		return true, nil
	}
	reviewed := make([]newsContextBackfillReviewedVersion, 0, len(outputs))
	for _, output := range outputs {
		version, err := s.store.GetNewsThreadVersion(ctx, output.VersionID)
		if err != nil {
			return false, err
		}
		reviewed = append(reviewed, newsContextBackfillReviewedVersion{
			DailyRunID: output.DailyRunID, ThreadID: version.ThreadID,
			VersionID: version.ID, FinalReviewRunID: finalReview.ID,
		})
	}
	// ponytail: one small idempotent page is enough to make final-review
	// coverage durable without adding a second progress cursor. A larger history
	// can keep paging on later scheduler ticks.
	if err := s.store.UpsertNewsContextBackfillReviewedVersions(ctx, item.ID, finalReview.ID, reviewed); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Service) validateNewsContextBackfillFinalCoverage(ctx context.Context, item NewsContextBackfill) error {
	if item.RemainingNewsCount > 0 || item.MissingNewsCount > 0 {
		return errors.New("historical news coverage is incomplete")
	}
	missingEvidence, err := s.store.CountNewsContextBackfillCoveredWithoutEvidence(ctx, item.ID)
	if err != nil {
		return err
	}
	if missingEvidence > 0 {
		return fmt.Errorf("historical news coverage has %d covered items without compact evidence", missingEvidence)
	}
	return nil
}

func (s *Service) ensureNewsContextBackfillFinalIndexes(ctx context.Context, until time.Time) ([]string, bool, error) {
	return s.newsContextBackfillFinalIndexes(ctx, until, true)
}

func (s *Service) verifyNewsContextBackfillFinalIndexes(ctx context.Context, until time.Time) ([]string, bool, error) {
	return s.newsContextBackfillFinalIndexes(ctx, until, false)
}

func (s *Service) newsContextBackfillFinalIndexes(ctx context.Context, until time.Time, repair bool) ([]string, bool, error) {
	_, embeddingCfg, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return nil, false, err
	}
	threadIDs := make([]string, 0)
	missingThreadIDs := make([]string, 0)
	missingVersionIDs := make([]string, 0)
	workCount := func() int { return len(missingThreadIDs) + len(missingVersionIDs) }
	for offset := 0; ; offset += newsContextSeedPageSize {
		threads, err := s.store.ListNewsThreads(ctx, NewsThreadListFilter{Status: NewsThreadStatusActive, Limit: newsContextSeedPageSize, Offset: offset})
		if err != nil {
			return nil, false, err
		}
		for _, thread := range threads {
			threadIDs = append(threadIDs, thread.ID)
			if workCount() >= newsContextBackfillIndexPageSize {
				continue
			}
			ready := false
			if thread.IndexStatus == NewsContextIndexReady {
				ready, err = s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThread, thread.ID,
					NewsThreadEmbeddingText(thread), embeddingCfg.EmbeddingModelID)
				if err != nil {
					return nil, false, err
				}
			}
			if !ready {
				missingThreadIDs = append(missingThreadIDs, thread.ID)
			}
		}
		if len(threads) < newsContextSeedPageSize {
			break
		}
	}
	if workCount() < newsContextBackfillIndexPageSize {
		for offset := 0; ; offset += newsContextSeedPageSize {
			// ponytail: DuckDB timestamps use microsecond precision, so one
			// microsecond turns the repository's exclusive bound into an inclusive
			// final-window bound without adding another query option.
			versions, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{Until: until.Add(time.Microsecond), Limit: newsContextSeedPageSize, Offset: offset})
			if err != nil {
				return nil, false, err
			}
			for _, version := range versions {
				if !newsThreadVersionEmbeddingIndexable(version) {
					continue
				}
				ready := false
				if version.IndexStatus == NewsContextIndexReady {
					ready, err = s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThreadVersion, version.ID,
						NewsThreadVersionEmbeddingText(version), embeddingCfg.EmbeddingModelID)
					if err != nil {
						return nil, false, err
					}
				}
				if !ready {
					missingVersionIDs = append(missingVersionIDs, version.ID)
					if workCount() >= newsContextBackfillIndexPageSize {
						break
					}
				}
			}
			if workCount() >= newsContextBackfillIndexPageSize || len(versions) < newsContextSeedPageSize {
				break
			}
		}
	}
	if workCount() > 0 {
		if !repair {
			return uniqueNonEmptyStrings(threadIDs), false, nil
		}
		// ponytail: one small persisted index page is the pre-review execution
		// unit. Ready asset state is the restart cursor, so realtime work waits for
		// at most this page and no extra progress table is needed.
		if err := s.SyncNewsContextEmbeddingObjects(ctx, missingThreadIDs, missingVersionIDs); err != nil {
			return nil, false, err
		}
		return uniqueNonEmptyStrings(threadIDs), false, nil
	}
	return uniqueNonEmptyStrings(threadIDs), true, nil
}

func (s *Service) failNewsContextBackfill(ctx context.Context, item NewsContextBackfill, cause error) error {
	item.Status = NewsContextBackfillStatusFailed
	item.ErrorMessage = safelog.Text(cause.Error(), 500)
	_, updateErr := s.refreshAndSaveNewsContextBackfill(ctx, item)
	if updateErr != nil {
		return updateErr
	}
	return cause
}
