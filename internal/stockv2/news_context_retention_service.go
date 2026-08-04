package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const newsContextCleanupBatchSize = 200

type newsContextCleanupGateFailure struct {
	reason string
}

type newsContextCleanupBackfillPrecheckedKey struct{}

func (e newsContextCleanupGateFailure) Error() string {
	return e.reason
}

func newsContextCleanupBlocked(reason string) (bool, string, error) {
	reason = firstNonEmpty(strings.TrimSpace(reason), "消息清理安全校验未通过")
	return false, reason, newsContextCleanupGateFailure{reason: reason}
}

func (s *Service) tryStartNewsContextCleanup() error {
	s.newsContextMu.Lock()
	defer s.newsContextMu.Unlock()
	if s.newsCleanupRun {
		return ErrNewsContextCleanupRunning
	}
	if s.newsContextRun {
		return ErrNewsContextAlreadyRunning
	}
	s.newsCleanupRun = true
	return nil
}

func (s *Service) finishNewsContextCleanup() {
	s.newsContextMu.Lock()
	s.newsCleanupRun = false
	s.newsContextMu.Unlock()
}

func (s *Service) StartNewsContextCleanupRun(ctx context.Context, req RequestStartNewsContextCleanup) (NewsContextCleanupRun, error) {
	if err := s.tryStartNewsContextCleanup(); err != nil {
		return NewsContextCleanupRun{}, err
	}
	release := true
	defer func() {
		if release {
			s.finishNewsContextCleanup()
		}
	}()
	if running, err := s.store.HasRunningNewsContextCleanupRun(ctx); err != nil {
		return NewsContextCleanupRun{}, err
	} else if running {
		return NewsContextCleanupRun{}, ErrNewsContextCleanupRunning
	}
	if running, err := s.store.HasRunningNewsContextRun(ctx); err != nil {
		return NewsContextCleanupRun{}, err
	} else if running {
		return NewsContextCleanupRun{}, ErrNewsContextAlreadyRunning
	}
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return NewsContextCleanupRun{}, err
	}
	cutoff, err := newsContextCleanupCutoff(time.Now(), cfg.CleanupGraceSeconds, req.Before)
	if err != nil {
		return NewsContextCleanupRun{}, err
	}
	gate, err := s.newsContextCleanupGate(ctx, cutoff)
	if err != nil {
		return NewsContextCleanupRun{}, err
	}
	if gate.Blocked {
		return NewsContextCleanupRun{}, fmt.Errorf("%w: %s", ErrNewsContextReviewIncomplete, gate.Reason)
	}
	if err := s.validateNewsContextCleanupPrerequisites(ctx, false, cutoff); err != nil {
		return NewsContextCleanupRun{}, err
	}
	run, err := s.store.CreateNewsContextCleanupRun(ctx, NewsContextCleanupRun{
		ContextRunID: strings.TrimSpace(req.ContextRunID),
		Status:       NewsContextCleanupRunning,
		Phase:        "checking_gates",
		Cutoff:       cutoff,
		RequestedBy:  strings.TrimSpace(req.RequestedBy),
		StartedAt:    time.Now(),
	})
	if err != nil {
		return NewsContextCleanupRun{}, err
	}
	if !s.launchNewsContextWorker(run.ID, s.executeNewsContextCleanup) {
		return NewsContextCleanupRun{}, context.Canceled
	}
	release = false
	return run, nil
}

func (s *Service) executeNewsContextCleanup(ctx context.Context, id string) {
	defer s.finishNewsContextCleanup()
	// One real MCP round-trip per current theme and cleanup run is sufficient;
	// persisted results remain observable but never bypass a later run's probe.
	ctx = context.WithValue(ctx, newsContextMCPVerificationCacheKey{}, newsContextMCPVerificationCache{})
	run, err := s.store.GetNewsContextCleanupRun(ctx, id)
	if err != nil {
		return
	}
	fail := func(cause error) {
		if s.newsContextWorkerShutdownCanceled(ctx) {
			return
		}
		run.Status = NewsContextCleanupFailed
		run.Phase = "failed"
		run.ErrorMessage = safelog.Text(cause.Error(), 500)
		run.FinishedAt = time.Now()
		_, _ = s.store.UpdateNewsContextCleanupRun(context.Background(), run)
		if cfg, cfgErr := s.GetNewsContextConfig(context.Background()); cfgErr == nil {
			cfg.LastCleanupAt = run.FinishedAt
			cfg.LastError = safelog.Text(cause.Error(), 500)
			_, _ = s.store.UpsertNewsContextConfig(context.Background(), cfg)
		}
	}
	// Check every candidate before deleting any content. A missing daily review,
	// index, or real MCP read blocks the whole run; deliberately protected news
	// may remain while unrelated, fully verified news is compacted.
	run.Phase = "checking_structure"
	afterID := ""
	for {
		gate, err := s.newsContextCleanupGate(ctx, run.Cutoff)
		if err != nil {
			fail(err)
			return
		}
		if gate.Blocked {
			fail(fmt.Errorf("%w: %s", ErrNewsContextReviewIncomplete, gate.Reason))
			return
		}
		candidates, err := s.store.ListNewsEventsForContextCleanup(ctx, run.Cutoff, afterID, newsContextCleanupBatchSize)
		if err != nil {
			fail(err)
			return
		}
		if len(candidates) == 0 {
			break
		}
		// ponytail: the batch-level check is enough during the no-delete preflight;
		// repeating the same historical source aggregate for 16k candidates would
		// turn a linear safety scan into quadratic work. Per-item delete rechecks do
		// not use this marker.
		preflightCtx := context.WithValue(ctx, newsContextCleanupBackfillPrecheckedKey{}, true)
		for _, candidate := range candidates {
			afterID = candidate.Event.ID
			run.ScannedCount++
			_, _, err := s.newsContextCleanupEligibilityWithMCP(preflightCtx, candidate, run.Cutoff, false, false)
			if err != nil {
				run.FailedCount++
				fail(s.recordNewsContextCleanupCandidateFailure(ctx, candidate, err))
				return
			}
		}
		if _, err := s.store.UpdateNewsContextCleanupRun(ctx, run); err != nil {
			fail(err)
			return
		}
		if len(candidates) < newsContextCleanupBatchSize {
			break
		}
	}

	// ponytail: finish every deterministic lineage/index check before the first
	// real semantic-search round trip. One late structural blocker must not make
	// an hourly cleanup repeat minutes of DuckDB vector work before failing.
	run.Phase = "verifying_retrieval"
	run.EligibleCount = 0
	run.ProtectedCount = 0
	afterID = ""
	for {
		candidates, err := s.store.ListNewsEventsForContextCleanup(ctx, run.Cutoff, afterID, newsContextCleanupBatchSize)
		if err != nil {
			fail(err)
			return
		}
		if len(candidates) == 0 {
			break
		}
		preflightCtx := context.WithValue(ctx, newsContextCleanupBackfillPrecheckedKey{}, true)
		for _, candidate := range candidates {
			afterID = candidate.Event.ID
			eligible, reason, err := s.newsContextCleanupEligibilityWithMCP(preflightCtx, candidate, run.Cutoff, false, true)
			if err != nil {
				run.FailedCount++
				fail(s.recordNewsContextCleanupCandidateFailure(ctx, candidate, err))
				return
			}
			if !eligible {
				run.ProtectedCount++
				_ = s.store.ProtectNewsEventForCleanup(ctx, candidate.Event.ID, reason)
				continue
			}
			run.EligibleCount++
		}
		if _, err := s.store.UpdateNewsContextCleanupRun(ctx, run); err != nil {
			fail(err)
			return
		}
		if len(candidates) < newsContextCleanupBatchSize {
			break
		}
	}

	run.Phase = "compacting"
	run.EligibleCount = 0
	run.ProtectedCount = 0
	afterID = ""
	for {
		candidates, err := s.store.ListNewsEventsForContextCleanup(ctx, run.Cutoff, afterID, newsContextCleanupBatchSize)
		if err != nil {
			fail(err)
			return
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			afterID = candidate.Event.ID
			released, compacted, _, err := s.compactNewsContextCandidateAtCutoff(ctx, candidate, run.Cutoff)
			if err != nil {
				run.FailedCount++
				fail(err)
				return
			}
			if !compacted {
				run.ProtectedCount++
				continue
			}
			run.EligibleCount++
			run.CompactedCount++
			run.ReleasedBytes += released
		}
		if _, err := s.store.UpdateNewsContextCleanupRun(ctx, run); err != nil {
			fail(err)
			return
		}
		if len(candidates) < newsContextCleanupBatchSize {
			break
		}
	}
	run.Status = NewsContextCleanupCompleted
	run.Phase = "completed"
	if run.FailedCount > 0 {
		run.Status = NewsContextCleanupPartial
		run.Phase = "compacting"
	}
	run.FinishedAt = time.Now()
	if _, err := s.store.UpdateNewsContextCleanupRun(ctx, run); err != nil {
		fail(err)
		return
	}
	cfg, err := s.GetNewsContextConfig(ctx)
	if err == nil {
		cfg.LastCleanupAt = run.FinishedAt
		_, _ = s.store.UpsertNewsContextConfig(ctx, cfg)
	}
}

func (s *Service) newsContextCleanupEligible(ctx context.Context, candidate NewsContextCleanupCandidate) (bool, string, error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return false, "", err
	}
	cutoff, err := newsContextCleanupCutoff(time.Now(), cfg.CleanupGraceSeconds, "")
	if err != nil {
		return false, "", err
	}
	eligible, reason, err := s.newsContextCleanupEligibility(ctx, candidate, cutoff, false)
	var gateFailure newsContextCleanupGateFailure
	if errors.As(err, &gateFailure) {
		return false, reason, nil
	}
	return eligible, reason, err
}

func (s *Service) newsContextCleanupEligibility(ctx context.Context, candidate NewsContextCleanupCandidate, cutoff time.Time, cachedMCPOnly bool) (bool, string, error) {
	return s.newsContextCleanupEligibilityWithMCP(ctx, candidate, cutoff, cachedMCPOnly, true)
}

func (s *Service) newsContextCleanupEligibilityWithMCP(
	ctx context.Context,
	candidate NewsContextCleanupCandidate,
	cutoff time.Time,
	cachedMCPOnly bool,
	verifyMCP bool,
) (bool, string, error) {
	if prechecked, _ := ctx.Value(newsContextCleanupBackfillPrecheckedKey{}).(bool); !prechecked {
		gate, err := s.newsContextCleanupGate(ctx, cutoff)
		if err != nil {
			return false, "", err
		}
		if gate.Blocked {
			return newsContextCleanupBlocked(gate.Reason)
		}
	}
	if candidate.ContextStatus != NewsEventContextCovered && candidate.ContextStatus != NewsEventContextNoise {
		return newsContextCleanupBlocked("消息尚未形成明确归纳结论")
	}
	if candidate.ContextCoveredAt.IsZero() {
		return newsContextCleanupBlocked("归纳覆盖时间缺失")
	}
	// Portfolio Sentinel builds its in-memory news snapshot while the run is
	// already marked running. Blocking cleanup during that short window avoids
	// racing its reads. Monitor hits and operation reviews persist self-contained
	// news snapshots; opportunity and strategy runs consume themes/evidence rather
	// than dereferencing raw news IDs.
	sentinelRuns, err := s.store.ListPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{Status: PortfolioSentinelStatusRunning, Limit: 1})
	if err != nil {
		return false, "", err
	}
	if len(sentinelRuns) > 0 {
		return newsContextCleanupBlocked("组合哨兵正在使用消息原文")
	}
	protected, reason, err := s.store.NewsEventCleanupProtected(ctx, candidate.Event.ID)
	if err != nil {
		return false, "", err
	}
	if protected {
		return false, firstNonEmpty(reason, "消息仍被活跃对象引用"), nil
	}
	if candidate.ContextStatus == NewsEventContextNoise {
		ready, reason, err := s.noiseNewsContextCleanupReady(ctx, candidate)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return newsContextCleanupBlocked(reason)
		}
		return true, "", nil
	}
	evidence := make([]NewsThreadEvidence, 0, 500)
	for offset := 0; ; offset += 500 {
		page, err := s.store.ListNewsThreadEvidence(ctx, NewsThreadEvidenceListFilter{NewsEventID: candidate.Event.ID, Limit: 500, Offset: offset})
		if err != nil {
			return false, "", err
		}
		evidence = append(evidence, page...)
		if len(page) < 500 {
			break
		}
	}
	if len(evidence) == 0 {
		return newsContextCleanupBlocked("精简证据尚未保存")
	}
	evidenceVersions := make(map[string]NewsThreadVersion)
	threads := make(map[string]NewsThread)
	dailyVersions := make(map[string]NewsThreadVersion)
	historicalFinalReviewed := make(map[string]bool)
	for _, item := range evidence {
		if _, ok := evidenceVersions[item.VersionID]; !ok {
			version, err := s.store.GetNewsThreadVersion(ctx, item.VersionID)
			if err != nil {
				return newsContextCleanupBlocked("主题版本缺失")
			}
			// Historical conflicts stay in the audit trail. A clean reviewed daily
			// checkpoint after coverage is what proves they were subsequently resolved.
			evidenceVersions[item.VersionID] = version
		}
		if _, ok := threads[item.ThreadID]; !ok {
			thread, err := s.store.GetNewsThread(ctx, item.ThreadID)
			if err != nil {
				return newsContextCleanupBlocked("当前主题缺失")
			}
			dailyVersion, found, err := s.store.FindCompletedDailyNewsThreadVersionAfter(ctx, thread.ID, candidate.ContextCoveredAt)
			if err != nil {
				return false, "", err
			}
			if !found {
				dailyVersion, found, err = s.findNewsContextBackfillReviewedDailyVersion(ctx, candidate, thread.ID)
				if err != nil {
					return false, "", err
				}
				historicalFinalReviewed[thread.ID] = found
			}
			if !found {
				return newsContextCleanupBlocked("主题尚无覆盖该消息的已复核每日结论")
			}
			dailyRun, err := s.store.GetNewsContextRun(ctx, dailyVersion.RunID)
			if err != nil {
				if errors.Is(err, ErrNewsContextRunNotFound) {
					return newsContextCleanupBlocked("每日归纳运行记录缺失")
				}
				return false, "", err
			}
			if dailyRun.Status != NewsContextRunStatusCompleted ||
				(!historicalFinalReviewed[thread.ID] && dailyRun.ReviewStatus != NewsContextReviewCompleted) {
				return newsContextCleanupBlocked("每日归纳或影响复核尚未完成")
			}
			if reason := newsContextVersionProtectionReason(dailyVersion); reason != "" {
				return false, reason, nil
			}
			if thread.Status != NewsThreadStatusMerged && thread.Status != NewsThreadStatusArchived {
				currentVersion, err := s.store.GetNewsThreadVersion(ctx, thread.CurrentVersionID)
				if err != nil {
					return newsContextCleanupBlocked("当前主题版本缺失")
				}
				if reason := newsContextVersionProtectionReason(currentVersion); reason != "" {
					return false, reason, nil
				}
				if newsThreadVersionEffectiveTime(currentVersion).After(newsThreadVersionEffectiveTime(dailyVersion)) {
					return newsContextCleanupBlocked("最新主题变化尚未进入每日归纳和影响复核")
				}
			}
			threads[item.ThreadID] = thread
			dailyVersions[item.ThreadID] = dailyVersion
		}
	}
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return false, "", err
	}
	if !cfg.Enabled || strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		return newsContextCleanupBlocked("主题向量模型不可用")
	}
	for _, version := range evidenceVersions {
		if !newsThreadVersionEmbeddingIndexable(version) {
			continue
		}
		ready, err := s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThreadVersion, version.ID, NewsThreadVersionEmbeddingText(version), cfg.EmbeddingModelID)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return newsContextCleanupBlocked("历史主题版本向量尚未就绪")
		}
	}
	for threadID, thread := range threads {
		dailyVersion := dailyVersions[threadID]
		ready, err := s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThreadVersion, dailyVersion.ID, NewsThreadVersionEmbeddingText(dailyVersion), cfg.EmbeddingModelID)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return newsContextCleanupBlocked("每日主题结论向量尚未就绪")
		}
		if thread.Status == NewsThreadStatusMerged || thread.Status == NewsThreadStatusArchived {
			continue
		}
		ready, err = s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThread, thread.ID, NewsThreadEmbeddingText(thread), cfg.EmbeddingModelID)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return newsContextCleanupBlocked("当前主题向量尚未就绪")
		}
	}
	if !verifyMCP {
		return true, "", nil
	}
	for threadID, thread := range threads {
		verificationVersionID := thread.CurrentVersionID
		if thread.Status == NewsThreadStatusMerged || thread.Status == NewsThreadStatusArchived {
			verificationVersionID = dailyVersions[threadID].ID
		}
		if cachedMCPOnly {
			cache, ok := ctx.Value(newsContextMCPVerificationCacheKey{}).(newsContextMCPVerificationCache)
			if !ok || cache[thread.ID] != verificationVersionID {
				return false, "主题在清理复检期间发生变化，留待下次清理", nil
			}
			continue
		}
		var verifyErr error
		if thread.Status == NewsThreadStatusMerged || thread.Status == NewsThreadStatusArchived {
			_, verifyErr = s.verifyHistoricalNewsThreadMCP(ctx, thread.ID, verificationVersionID)
		} else {
			_, verifyErr = s.VerifyNewsThreadMCP(ctx, thread.ID)
		}
		if verifyErr != nil {
			verification, found, readErr := s.store.GetNewsContextMCPVerification(ctx, thread.ID)
			if readErr != nil {
				return false, "", readErr
			}
			if !found || verification.VersionID != verificationVersionID || verification.Status != NewsContextMCPVerificationFailed {
				return false, "", verifyErr
			}
			return newsContextCleanupBlocked("CLI 主题语义检索或详情读取验证失败")
		}
	}
	return true, "", nil
}

func (s *Service) recordNewsContextCleanupCandidateFailure(
	ctx context.Context,
	candidate NewsContextCleanupCandidate,
	cause error,
) error {
	reason := strings.TrimSpace(cause.Error())
	_ = s.store.ProtectNewsEventForCleanup(ctx, candidate.Event.ID, reason)
	return fmt.Errorf("%w (event_id=%s, context_run_id=%s)", cause,
		strings.TrimSpace(candidate.Event.ID), strings.TrimSpace(candidate.ContextRunID))
}

func (s *Service) findNewsContextBackfillReviewedDailyVersion(ctx context.Context, candidate NewsContextCleanupCandidate, threadID string) (NewsThreadVersion, bool, error) {
	var empty NewsThreadVersion
	source, err := s.store.GetNewsContextRun(ctx, strings.TrimSpace(candidate.ContextRunID))
	if errors.Is(err, ErrNewsContextRunNotFound) {
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	backfills, err := s.store.ListNewsContextBackfillsForRun(ctx, source.ID)
	if err != nil {
		return empty, false, err
	}
	for _, backfill := range backfills {
		if backfill.Status != NewsContextBackfillStatusCompleted || backfill.MissingNewsCount != 0 ||
			backfill.RangeStartAt.IsZero() || source.WindowStart.Before(backfill.RangeStartAt) ||
			source.WindowEnd.After(backfill.CutoffAt) {
			continue
		}
		ready, _, err := s.newsContextCleanupHistoricalFinalReview(ctx, backfill)
		if err != nil {
			return empty, false, err
		}
		if !ready {
			continue
		}
		association, found, err := s.store.FindNewsContextBackfillReviewedVersionCoveringRun(ctx,
			backfill.ID, backfill.FinalReviewRunID, threadID, source.WindowStart, source.WindowEnd)
		if err != nil {
			return empty, false, err
		}
		if !found {
			continue
		}
		version, err := s.store.GetNewsThreadVersion(ctx, association.VersionID)
		if err != nil {
			return empty, false, err
		}
		if version.ThreadID != threadID || version.RunID != association.DailyRunID ||
			version.WindowType != NewsContextWindowDaily {
			continue
		}
		return version, true, nil
	}
	return empty, false, nil
}

func (s *Service) noiseNewsContextCleanupReady(ctx context.Context, candidate NewsContextCleanupCandidate) (bool, string, error) {
	source, err := s.store.GetNewsContextRun(ctx, strings.TrimSpace(candidate.ContextRunID))
	if errors.Is(err, ErrNewsContextRunNotFound) {
		return false, "噪音消息的直接处理运行缺失", nil
	}
	if err != nil {
		return false, "", err
	}
	if source.Status != NewsContextRunStatusCompleted {
		return false, "噪音消息的直接处理运行尚未完成", nil
	}
	if candidate.Event.EventAt.Before(source.WindowStart) || !candidate.Event.EventAt.Before(source.WindowEnd) {
		// ponytail: deferred news may be claimed by a later retry window. The
		// completed noise/duplicate item is stronger provenance than expanding the
		// natural window and keeps unrelated cross-window references blocked.
		processed, err := s.store.HasCompletedDiscardedNewsContextRunItem(ctx, source.ID, candidate.Event.ID)
		if err != nil {
			return false, "", err
		}
		if !processed {
			return false, "噪音消息不在直接处理运行的时间窗口内", nil
		}
	}
	if source.WindowType == NewsContextWindowDaily {
		if source.TriggerType == NewsContextTriggerBackfill {
			backfill, ready, reason, err := s.newsContextCleanupHistoricalBackfill(ctx, source)
			if err != nil || !ready {
				return false, reason, err
			}
			return s.newsContextCleanupHistoricalFinalReview(ctx, backfill)
		}
		if source.ReviewStatus != NewsContextReviewCompleted {
			return false, "直接每日归纳尚未完成影响复核", nil
		}
		return true, "", nil
	}
	child := source
	if source.WindowType == NewsContextWindowHourly {
		parent, ready, reason, err := s.newsContextCleanupParentRun(ctx, source, child, NewsContextWindowFourHour, false)
		if err != nil || !ready {
			return false, reason, err
		}
		child = parent
	} else if source.WindowType != NewsContextWindowFourHour {
		return false, "噪音消息的直接处理周期无效", nil
	}
	_, ready, reason, err := s.newsContextCleanupParentRun(ctx, source, child, NewsContextWindowDaily, true)
	return ready, reason, err
}

func (s *Service) newsContextCleanupParentRun(ctx context.Context, source, child NewsContextRun, parentType string, reviewRequired bool) (NewsContextRun, bool, string, error) {
	// Completed natural-window runs may be reused and linked into a backfill.
	// The durable link, rather than the original trigger label, owns lineage.
	if _, linked, err := s.store.NewsContextBackfillForRun(ctx, source.ID); err != nil {
		return NewsContextRun{}, false, "", err
	} else if linked {
		return s.newsContextCleanupHistoricalParentRun(ctx, source, child, parentType, reviewRequired)
	}
	if !newsContextRunUsesNaturalWindow(child) {
		return NewsContextRun{}, false, "下层归纳窗口不属于固定周期层级", nil
	}
	start := newsContextBoundaryAtOrBefore(parentType, child.WindowStart)
	end := nextNewsContextBoundary(parentType, start)
	if child.WindowStart.Before(start) || child.WindowEnd.After(end) {
		return NewsContextRun{}, false, "下层归纳窗口未完整落入父周期", nil
	}
	parent, err := s.store.getNewsContextRunByWindow(ctx, parentType, start, end)
	if errors.Is(err, ErrNewsContextRunNotFound) {
		return NewsContextRun{}, false, map[string]string{
			NewsContextWindowFourHour: "噪音消息缺少对应的已完成四小时归纳",
			NewsContextWindowDaily:    "噪音消息缺少对应的已复核每日归纳",
		}[parentType], nil
	}
	if err != nil {
		return NewsContextRun{}, false, "", err
	}
	if parent.Status != NewsContextRunStatusCompleted {
		return NewsContextRun{}, false, "父级归纳尚未完成", nil
	}
	// A manual daily may directly process news, but it cannot certify a separate
	// realtime lower-level run as its hierarchy parent.
	if parent.TriggerType == NewsContextTriggerBackfill ||
		(parentType == NewsContextWindowDaily && parent.TriggerType == NewsContextTriggerManual) {
		return NewsContextRun{}, false, "父级归纳不属于该消息的实际处理链", nil
	}
	if reviewRequired && parent.ReviewStatus != NewsContextReviewCompleted {
		return NewsContextRun{}, false, "每日归纳尚未完成影响复核", nil
	}
	return parent, true, "", nil
}

func (s *Service) newsContextCleanupHistoricalParentRun(ctx context.Context, source, child NewsContextRun, parentType string, finalReviewRequired bool) (NewsContextRun, bool, string, error) {
	backfill, ready, reason, err := s.newsContextCleanupHistoricalBackfill(ctx, source)
	if err != nil || !ready {
		return NewsContextRun{}, false, reason, err
	}
	if child.WindowStart.Before(backfill.RangeStartAt) || child.WindowEnd.After(backfill.CutoffAt) {
		return NewsContextRun{}, false, "下层归纳窗口超出历史补处理范围", nil
	}
	runs, err := s.store.ListNewsContextBackfillRuns(ctx, backfill.ID, parentType)
	if err != nil {
		return NewsContextRun{}, false, "", err
	}
	var parent NewsContextRun
	for _, run := range runs {
		// ponytail: membership in this backfill is already proven by the link
		// query; reused retry/scheduled checkpoints keep their original trigger.
		if run.Status != NewsContextRunStatusCompleted ||
			run.WindowStart.Before(backfill.RangeStartAt) || run.WindowEnd.After(backfill.CutoffAt) ||
			child.WindowStart.Before(run.WindowStart) || child.WindowEnd.After(run.WindowEnd) {
			continue
		}
		if parent.ID == "" || run.WindowEnd.Sub(run.WindowStart) < parent.WindowEnd.Sub(parent.WindowStart) {
			parent = run
		}
	}
	if parent.ID == "" {
		return NewsContextRun{}, false, map[string]string{
			NewsContextWindowFourHour: "噪音消息缺少同一历史补处理链的已完成四小时归纳",
			NewsContextWindowDaily:    "噪音消息缺少同一历史补处理链的已完成每日归纳",
		}[parentType], nil
	}
	if finalReviewRequired {
		ready, reason, err := s.newsContextCleanupHistoricalFinalReview(ctx, backfill)
		if err != nil || !ready {
			return NewsContextRun{}, false, reason, err
		}
	}
	return parent, true, "", nil
}

func (s *Service) newsContextCleanupHistoricalBackfill(ctx context.Context, source NewsContextRun) (NewsContextBackfill, bool, string, error) {
	backfill, found, err := s.store.NewsContextBackfillForRun(ctx, source.ID)
	if err != nil {
		return NewsContextBackfill{}, false, "", err
	}
	if !found {
		return NewsContextBackfill{}, false, "历史归纳运行未关联补处理任务", nil
	}
	if backfill.Status != NewsContextBackfillStatusCompleted || backfill.MissingNewsCount != 0 {
		return NewsContextBackfill{}, false, "历史补处理尚未完整完成", nil
	}
	if source.WindowStart.Before(backfill.RangeStartAt) || source.WindowEnd.After(backfill.CutoffAt) {
		return NewsContextBackfill{}, false, "直接处理运行超出历史补处理范围", nil
	}
	return backfill, true, "", nil
}

func (s *Service) newsContextCleanupHistoricalFinalReview(ctx context.Context, backfill NewsContextBackfill) (bool, string, error) {
	if strings.TrimSpace(backfill.FinalReviewRunID) == "" {
		return false, "历史补处理尚未保存最终当前复核运行", nil
	}
	run, err := s.store.GetNewsContextRun(ctx, backfill.FinalReviewRunID)
	if errors.Is(err, ErrNewsContextRunNotFound) {
		return false, "历史补处理的最终当前复核运行缺失", nil
	}
	if err != nil {
		return false, "", err
	}
	if run.WindowType != NewsContextWindowDaily ||
		(run.TriggerType != NewsContextTriggerManual && run.TriggerType != NewsContextTriggerRetry) ||
		!run.WindowStart.Equal(backfill.CutoffAt) || run.Status != NewsContextRunStatusCompleted ||
		run.ReviewStatus != NewsContextReviewCompleted {
		return false, "历史补处理的最终当前归纳或影响复核尚未完成", nil
	}
	return true, "", nil
}

func (s *Service) compactNewsContextCandidate(ctx context.Context, candidate NewsContextCleanupCandidate) (int64, bool, string, error) {
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return 0, false, "", err
	}
	cutoff, err := newsContextCleanupCutoff(time.Now(), cfg.CleanupGraceSeconds, "")
	if err != nil {
		return 0, false, "", err
	}
	return s.compactNewsContextCandidateAtCutoff(ctx, candidate, cutoff)
}

func (s *Service) compactNewsContextCandidateAtCutoff(ctx context.Context, candidate NewsContextCleanupCandidate, cutoff time.Time) (int64, bool, string, error) {
	protect := func(reason string) (int64, bool, string, error) {
		if err := s.store.ProtectNewsEventForCleanup(ctx, candidate.Event.ID, reason); err != nil {
			return 0, false, "", err
		}
		return 0, false, reason, nil
	}
	if !s.tryStartNewsContextRun() {
		return protect("消息归纳正在运行，留待下次清理")
	}
	defer s.finishNewsContextRun()
	if !s.beginEmbeddingMaintenance() {
		return protect("向量维护正在运行，留待下次清理")
	}
	defer s.endEmbeddingMaintenance()
	// ponytail: the slow MCP probe ran before this short execution-slot check.
	// A cache miss means the theme changed, so the next cleanup run verifies it.
	eligible, reason, err := s.newsContextCleanupEligibility(ctx, candidate, cutoff, true)
	if err != nil {
		var gateFailure newsContextCleanupGateFailure
		if errors.As(err, &gateFailure) {
			return protect(firstNonEmpty(reason, gateFailure.reason))
		}
		return 0, false, reason, err
	}
	if !eligible {
		return protect(reason)
	}
	if err := s.store.ProtectNewsEventForCleanup(ctx, candidate.Event.ID, ""); err != nil {
		return 0, false, "", err
	}
	released, err := s.compactNewsContextEvent(ctx, candidate.Event)
	return released, err == nil, "", err
}

func (s *Service) newsContextCleanupGate(ctx context.Context, cutoff time.Time) (NewsContextCleanupGate, error) {
	gate := NewsContextCleanupGate{Cutoff: cutoff}
	if cutoff.IsZero() {
		return gate, ErrInvalidNewsContextInput
	}
	// ponytail: use the cleanup run's immutable cutoff as the only unresolved-news
	// boundary. New work inside the grace period remains visible but cannot keep
	// already-reviewed historical candidates locked forever.
	if _, found, err := s.store.GetBlockingNewsContextBackfill(ctx); err != nil {
		return gate, err
	} else if found {
		gate.ActiveBackfill = true
	}
	stats, err := s.store.NewsContextBackfillSourceStats(ctx, cutoff)
	if err != nil {
		return gate, err
	}
	gate.BacklogCount = stats.Total
	gate.PendingCount = stats.Pending
	gate.DeferredCount = stats.Deferred
	gate.ClaimedCount = stats.Claimed
	gate.EarliestAt = stats.EarliestAt
	gate.LatestAt = stats.LatestAt
	switch {
	case gate.ActiveBackfill:
		gate.Blocked = true
		gate.Reason = "历史新闻补处理正在运行"
	case gate.BacklogCount > 0:
		gate.Blocked = true
		gate.Reason = fmt.Sprintf("清理截止点前仍有 %d 条消息未完成归纳", gate.BacklogCount)
	}
	return gate, nil
}

func newsContextCleanupCutoff(now time.Time, graceSeconds int, requested string) (time.Time, error) {
	if graceSeconds <= 0 {
		graceSeconds = 24 * 3600
	}
	latestSafe := now.Add(-time.Duration(graceSeconds) * time.Second)
	if strings.TrimSpace(requested) == "" {
		return latestSafe, nil
	}
	cutoff := parseNewsContextTime(requested)
	if cutoff.IsZero() {
		return time.Time{}, ErrInvalidNewsContextInput
	}
	if cutoff.After(latestSafe) {
		return latestSafe, nil
	}
	return cutoff, nil
}

func newsContextVersionProtectionReason(version NewsThreadVersion) string {
	switch strings.ToLower(strings.TrimSpace(version.ResearchStatus)) {
	case "", NewsContextResearchNotRequired, NewsContextResearchCompleted, "verified":
	case NewsContextResearchFailed:
		return "公开搜索失败，仍需保留原文"
	case NewsContextResearchUnavailable:
		return "该主题版本尚未完成公开资料核实，仍需保留原文"
	case NewsContextResearchUnresolved:
		return "公开搜索仍有未解决问题，需保留原文"
	default:
		return "主题公开资料核实尚未完成"
	}
	// ponytail: reviewed theme versions and compact evidence retain counter
	// evidence and open questions themselves. Treating any non-empty array as a
	// raw-news lock made every observed model result permanently uncleanable;
	// research status and the newer-than-daily gate above remain the safety
	// boundaries for unresolved or newly changed conclusions.
	return ""
}

func (s *Service) embeddingObjectCurrentAndReady(ctx context.Context, objectType, objectID, text, modelID string) (bool, error) {
	asset, err := s.store.GetEmbeddingAssetByObject(ctx, objectType, objectID, modelID)
	if err != nil {
		if errors.Is(err, ErrEmbeddingAssetNotFound) {
			return false, nil
		}
		return false, err
	}
	metadataReady := asset.Status == EmbeddingAssetStatusReady &&
		asset.TextHash == hashEmbeddingText(text) &&
		strings.TrimSpace(asset.VectorRef) != ""
	if !metadataReady {
		return false, nil
	}
	return s.store.HasEmbeddingVector(ctx, asset.VectorRef)
}

func (s *Service) compactNewsContextEvent(ctx context.Context, event NewsEvent) (int64, error) {
	// Cross-database cleanup is deliberately staged and idempotent: a failure
	// leaves the compact source untouched, and the next run resumes at the first
	// unfinished stage.
	for {
		assets, err := s.store.ListEmbeddingAssets(ctx, EmbeddingAssetListFilter{
			ObjectType: EmbeddingObjectNewsEvent,
			ObjectID:   event.ID,
			Limit:      200,
		})
		if err != nil {
			return 0, err
		}
		if len(assets) == 0 {
			break
		}
		for _, asset := range assets {
			if asset.VectorRef != "" {
				if err := s.store.DeleteEmbeddingVector(ctx, asset.VectorRef); err != nil {
					return 0, fmt.Errorf("delete news event vector: %w", err)
				}
			}
			if err := s.store.DeleteEmbeddingAsset(ctx, asset.ID); err != nil && !errors.Is(err, ErrEmbeddingAssetNotFound) {
				return 0, err
			}
		}
		if len(assets) < 200 {
			break
		}
	}
	return s.store.CompactNewsEvent(ctx, event.ID)
}

func newsContextContainsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (s *Service) ListNewsContextCleanupRuns(ctx context.Context, filter NewsContextCleanupRunListFilter) ([]NewsContextCleanupRun, error) {
	items, err := s.store.ListNewsContextCleanupRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	for i := range items {
		decorateNewsContextCleanupRun(&items[i])
	}
	return items, nil
}

func decorateNewsContextCleanupRun(run *NewsContextCleanupRun) {
	run.Kind = "cleanup"
	run.TotalNewsCount = run.ScannedCount
	run.RetainedCount = run.ProtectedCount + run.FailedCount
	switch run.Status {
	case NewsContextCleanupCompleted:
		run.CoverageStatus = "complete"
	case NewsContextCleanupPartial:
		run.CoverageStatus = "partial"
		run.Retryable = true
		run.FailedStage = run.Phase
	case NewsContextCleanupFailed:
		run.CoverageStatus = "failed"
		run.Retryable = true
		run.FailedStage = run.Phase
	default:
		run.CoverageStatus = "pending"
	}
}

func (s *Service) RetryNewsContextCleanupRun(ctx context.Context, id string) (NewsContextCleanupRun, error) {
	run, err := s.store.GetNewsContextCleanupRun(ctx, strings.TrimSpace(id))
	if err != nil {
		return NewsContextCleanupRun{}, err
	}
	if run.Status != NewsContextCleanupFailed && run.Status != NewsContextCleanupPartial {
		return NewsContextCleanupRun{}, ErrInvalidNewsContextInput
	}
	return s.StartNewsContextCleanupRun(ctx, RequestStartNewsContextCleanup{
		ContextRunID: run.ContextRunID,
		Before:       run.Cutoff.Format(time.RFC3339Nano),
		RequestedBy:  "retry",
	})
}

func (s *Service) CountNewsContextCleanupRuns(ctx context.Context, filter NewsContextCleanupRunListFilter) (int, error) {
	return s.store.CountNewsContextCleanupRuns(ctx, filter)
}
