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

func (s *Service) tryStartNewsContextCleanup() bool {
	s.newsCleanupMu.Lock()
	defer s.newsCleanupMu.Unlock()
	if s.newsCleanupRun {
		return false
	}
	s.newsCleanupRun = true
	return true
}

func (s *Service) finishNewsContextCleanup() {
	s.newsCleanupMu.Lock()
	s.newsCleanupRun = false
	s.newsCleanupMu.Unlock()
}

func (s *Service) StartNewsContextCleanupRun(ctx context.Context, req RequestStartNewsContextCleanup) (NewsContextCleanupRun, error) {
	if !s.tryStartNewsContextCleanup() {
		return NewsContextCleanupRun{}, ErrNewsContextCleanupRunning
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
	cfg, err := s.GetNewsContextConfig(ctx)
	if err != nil {
		return NewsContextCleanupRun{}, err
	}
	cutoff, err := newsContextCleanupCutoff(time.Now(), cfg.CleanupGraceSeconds, req.Before)
	if err != nil {
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
	release = false
	go s.executeNewsContextCleanup(context.Background(), run.ID)
	return run, nil
}

func (s *Service) executeNewsContextCleanup(ctx context.Context, id string) {
	defer s.finishNewsContextCleanup()
	run, err := s.store.GetNewsContextCleanupRun(ctx, id)
	if err != nil {
		return
	}
	fail := func(cause error) {
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
	afterID := ""
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
			run.ScannedCount++
			eligible, reason, err := s.newsContextCleanupEligible(ctx, candidate)
			if err != nil {
				run.FailedCount++
				run.ErrorMessage = safelog.Text(err.Error(), 500)
				continue
			}
			if !eligible {
				run.ProtectedCount++
				_ = s.store.ProtectNewsEventForCleanup(ctx, candidate.Event.ID, reason)
				continue
			}
			run.EligibleCount++
			_ = s.store.ProtectNewsEventForCleanup(ctx, candidate.Event.ID, "")
			released, err := s.compactNewsContextEvent(ctx, candidate.Event)
			if err != nil {
				run.FailedCount++
				run.ErrorMessage = safelog.Text(err.Error(), 500)
				continue
			}
			run.CompactedCount++
			run.ReleasedBytes += released
		}
		run.Phase = "compacting"
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
	if candidate.ContextStatus != NewsEventContextCovered && candidate.ContextStatus != NewsEventContextNoise {
		return false, "消息尚未形成明确归纳结论", nil
	}
	if candidate.ContextCoveredAt.IsZero() {
		return false, "归纳覆盖时间缺失", nil
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
		return false, "组合哨兵正在使用消息原文", nil
	}
	protected, reason, err := s.store.NewsEventCleanupProtected(ctx, candidate.Event.ID)
	if err != nil {
		return false, "", err
	}
	if protected {
		return false, firstNonEmpty(reason, "消息仍被活跃对象引用"), nil
	}
	if candidate.ContextStatus == NewsEventContextNoise {
		ready, err := s.store.HasCompletedDailyNewsContextCheckpointAfter(ctx, candidate.ContextCoveredAt)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return false, "噪音消息尚未通过每日归纳和影响复核", nil
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
		return false, "精简证据尚未保存", nil
	}
	evidenceVersions := make(map[string]NewsThreadVersion)
	threads := make(map[string]NewsThread)
	dailyVersions := make(map[string]NewsThreadVersion)
	for _, item := range evidence {
		if _, ok := evidenceVersions[item.VersionID]; !ok {
			version, err := s.store.GetNewsThreadVersion(ctx, item.VersionID)
			if err != nil {
				return false, "主题版本缺失", nil
			}
			// Historical conflicts stay in the audit trail. A clean reviewed daily
			// checkpoint after coverage is what proves they were subsequently resolved.
			evidenceVersions[item.VersionID] = version
		}
		if _, ok := threads[item.ThreadID]; !ok {
			thread, err := s.store.GetNewsThread(ctx, item.ThreadID)
			if err != nil {
				return false, "当前主题缺失", nil
			}
			dailyVersion, found, err := s.store.FindCompletedDailyNewsThreadVersionAfter(ctx, thread.ID, candidate.ContextCoveredAt)
			if err != nil {
				return false, "", err
			}
			if !found {
				return false, "主题尚无覆盖该消息的已复核每日结论", nil
			}
			dailyRun, err := s.store.GetNewsContextRun(ctx, dailyVersion.RunID)
			if err != nil {
				if errors.Is(err, ErrNewsContextRunNotFound) {
					return false, "每日归纳运行记录缺失", nil
				}
				return false, "", err
			}
			if dailyRun.Status != NewsContextRunStatusCompleted || dailyRun.ReviewStatus != NewsContextReviewCompleted {
				return false, "每日归纳或影响复核尚未完成", nil
			}
			if reason := newsContextVersionProtectionReason(dailyVersion); reason != "" {
				return false, reason, nil
			}
			if thread.Status != NewsThreadStatusMerged && thread.Status != NewsThreadStatusArchived {
				currentVersion, err := s.store.GetNewsThreadVersion(ctx, thread.CurrentVersionID)
				if err != nil {
					return false, "当前主题版本缺失", nil
				}
				if reason := newsContextVersionProtectionReason(currentVersion); reason != "" {
					return false, reason, nil
				}
				if currentVersion.CreatedAt.After(dailyVersion.CreatedAt) {
					return false, "最新主题变化尚未进入每日归纳和影响复核", nil
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
		return false, "主题向量模型不可用", nil
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
			return false, "历史主题版本向量尚未就绪", nil
		}
	}
	for threadID, thread := range threads {
		dailyVersion := dailyVersions[threadID]
		ready, err := s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThreadVersion, dailyVersion.ID, NewsThreadVersionEmbeddingText(dailyVersion), cfg.EmbeddingModelID)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return false, "每日主题结论向量尚未就绪", nil
		}
		if thread.Status == NewsThreadStatusMerged || thread.Status == NewsThreadStatusArchived {
			continue
		}
		ready, err = s.embeddingObjectCurrentAndReady(ctx, EmbeddingObjectNewsThread, thread.ID, NewsThreadEmbeddingText(thread), cfg.EmbeddingModelID)
		if err != nil {
			return false, "", err
		}
		if !ready {
			return false, "当前主题向量尚未就绪", nil
		}
	}
	mcp := s.AgentMCPStatus()
	if !mcp.Enabled || !newsContextContainsString(mcp.RequiredTools, "stock_agent.semantic_search_news_threads") || !newsContextContainsString(mcp.RequiredTools, "stock_agent.get_news_thread") {
		return false, "CLI 主题检索尚不可用", nil
	}
	return true, "", nil
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
		return "公开搜索不可用，仍需保留原文"
	case NewsContextResearchUnresolved:
		return "公开搜索仍有未解决问题，需保留原文"
	default:
		return "主题公开资料核实尚未完成"
	}
	if len(version.CounterEvidence) > 0 {
		return "主题仍有未解决的重大来源冲突"
	}
	if len(version.OpenQuestions) > 0 {
		return "主题仍有需要原文的重要未决问题"
	}
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
	if err := s.store.DeleteNewsLinkCandidatesByEvent(ctx, event.ID); err != nil {
		return 0, err
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
