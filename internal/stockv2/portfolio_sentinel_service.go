package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: 先用确定性分数阈值和弱候选限额控制噪音；如果后续需要更细的
	// 信息价值排序，再升级为按事件类型/语义分桶的 scorer。
	portfolioSentinelLowPriorityNewsDetailLimit = 10
	portfolioSentinelNewsScanMultiplier         = 4
	portfolioSentinelNewsScanMin                = 200
	portfolioSentinelNewsHighScoreThreshold     = 65
)

type portfolioSentinelNewsFilterStats struct {
	InputCandidates              int
	RetainedCandidates           int
	RetainedLowPriorityCount     int
	SuppressedLowPriorityCount   int
	SuppressedLowPriorityTerms   map[string]int
	SuppressedLowPrioritySymbols map[string]int
}

func (s *Service) GetPortfolioSentinelConfig(ctx context.Context) (PortfolioSentinelConfig, error) {
	cfg, err := s.store.GetPortfolioSentinelConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrPortfolioSentinelResultNotFound) {
			return defaultPortfolioSentinelConfig(), nil
		}
		return PortfolioSentinelConfig{}, err
	}
	return normalizePortfolioSentinelConfig(cfg), nil
}

func (s *Service) UpdatePortfolioSentinelConfig(ctx context.Context, req RequestUpdatePortfolioSentinelConfig) (PortfolioSentinelConfig, error) {
	cfg, err := s.GetPortfolioSentinelConfig(ctx)
	if err != nil {
		return PortfolioSentinelConfig{}, err
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.PreMarketEnabled != nil {
		cfg.PreMarketEnabled = *req.PreMarketEnabled
	}
	if req.MiddayEnabled != nil {
		cfg.MiddayEnabled = *req.MiddayEnabled
	}
	if req.PostCloseEnabled != nil {
		cfg.PostCloseEnabled = *req.PostCloseEnabled
	}
	if req.MaxNewsItems != nil {
		cfg.MaxNewsItems = *req.MaxNewsItems
	}
	if req.MaxNewsPerHolding != nil {
		cfg.MaxNewsPerHolding = *req.MaxNewsPerHolding
	}
	if req.AgentDoublecheckEnabled != nil {
		cfg.AgentDoublecheckEnabled = *req.AgentDoublecheckEnabled
	}
	return s.store.UpsertPortfolioSentinelConfig(ctx, normalizePortfolioSentinelConfig(cfg))
}

func (s *Service) RunPortfolioSentinel(ctx context.Context, req RequestRunPortfolioSentinel) (PortfolioSentinelRun, error) {
	windowType := strings.TrimSpace(req.WindowType)
	if windowType == "" {
		windowType = PortfolioSentinelWindowManual
	}
	startAt, endAt, err := portfolioSentinelWindowRange(windowType, req.StartAt, req.EndAt, time.Now())
	if err != nil {
		return PortfolioSentinelRun{}, err
	}
	return s.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, windowType, strings.TrimSpace(req.PortfolioID), startAt, endAt, strings.TrimSpace(req.Note), true)
}

func (s *Service) RunScheduledPortfolioSentinel(ctx context.Context, windowType string, now time.Time) (PortfolioSentinelRun, error) {
	cfg, err := s.GetPortfolioSentinelConfig(ctx)
	if err != nil {
		return PortfolioSentinelRun{}, err
	}
	if !portfolioSentinelWindowEnabled(cfg, windowType) {
		return PortfolioSentinelRun{}, ErrPortfolioSentinelScheduledDisabled
	}
	startAt, endAt, err := portfolioSentinelWindowRange(windowType, "", "", now)
	if err != nil {
		return PortfolioSentinelRun{}, err
	}
	return s.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerScheduled, windowType, "", startAt, endAt, "", true)
}

func (s *Service) startPortfolioSentinelRun(ctx context.Context, triggerType, windowType, portfolioID string, startAt, endAt time.Time, note string, async bool) (PortfolioSentinelRun, error) {
	if !validPortfolioSentinelWindowType(windowType) || startAt.IsZero() || endAt.IsZero() || !endAt.After(startAt) {
		return PortfolioSentinelRun{}, ErrInvalidPortfolioSentinelInput
	}
	if running, err := s.store.HasRunningPortfolioSentinelRun(ctx, portfolioID, windowType); err != nil {
		return PortfolioSentinelRun{}, err
	} else if running {
		return PortfolioSentinelRun{}, ErrPortfolioSentinelAlreadyRunning
	}
	if portfolioID != "" {
		if _, err := s.store.GetPortfolio(ctx, portfolioID); err != nil {
			return PortfolioSentinelRun{}, err
		}
	}
	run, err := s.store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		PortfolioID:   portfolioID,
		Status:        PortfolioSentinelStatusRunning,
		TriggerType:   triggerType,
		WindowType:    windowType,
		WindowStartAt: startAt,
		WindowEndAt:   endAt,
		StartedAt:     time.Now(),
	})
	if err != nil {
		return PortfolioSentinelRun{}, err
	}

	if err := s.preparePortfolioSentinelNews(ctx, run); err != nil && s.log != nil {
		s.log.Warn("portfolio sentinel news refresh skipped", "run_id", run.ID, "error", safelog.Text(err.Error(), 240))
	}
	contextPack, err := s.BuildPortfolioSentinelContext(ctx, run, note)
	if err != nil {
		return s.failPortfolioSentinelRun(ctx, run, err)
	}
	run.ScannedPortfolioCount = len(contextPack.Portfolios)
	for _, p := range contextPack.Portfolios {
		run.ScannedHoldingCount += len(p.Holdings)
	}
	run.NewsEventCount = len(contextPack.NewsEvents)
	run.RawNewsCount = len(contextPack.RawNews)
	run.QuoteCount, run.DailyBarSymbolCount, run.MinuteBarSymbolCount = portfolioSentinelDataCounts(contextPack)

	// 将持仓新闻候选（含 recallMethods、scoreBreakdown）持久化到 result contextSummary，
	// 使历史运行可追溯"为什么这条新闻进来了"。
	// 在 Agent 执行前预创建结果记录，Agent 返回后由 ProcessPortfolioSentinelSubmittedResult 更新。
	prelimResult := PortfolioSentinelResult{
		RunID:          run.ID,
		SchemaVersion:  PortfolioSentinelReportSchemaVersion,
		ContextSummary: buildHoldingNewsCandidateSummary(contextPack),
	}
	if _, err := s.store.CreatePortfolioSentinelResult(ctx, prelimResult); err != nil {
		if s.log != nil {
			s.log.Warn("preliminary sentinel result save failed (will retry later)", "run_id", run.ID, "error", safelog.Text(err.Error(), 240))
		}
	}

	resolution, err := s.ResolveAgentTask(ctx, AgentTaskTypePortfolioSentinel, "portfolio_sentinel_run", run.ID, "system")
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return run, err
	}
	if resolution.Run == nil || resolution.DecisionLedger == nil {
		err := errors.New("no portfolio sentinel agent run created")
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return run, err
	}
	run.AgentRunID = resolution.Run.ID
	run.DecisionLedgerID = resolution.DecisionLedger.ID
	if run, err = s.store.UpdatePortfolioSentinelRun(ctx, run); err != nil {
		return PortfolioSentinelRun{}, err
	}
	if s.agentExecutor != nil && resolution.Status == AgentResolutionStatusAuthorized {
		if async {
			go s.startPortfolioSentinelRunAsync(context.Background(), *resolution.Run, *resolution.DecisionLedger, contextPack, resolution.ModelName)
		} else {
			_, _, _ = s.executePortfolioSentinelRun(ctx, *resolution.Run, *resolution.DecisionLedger, contextPack, resolution.ModelName)
		}
	}
	return run, nil
}

func (s *Service) BuildPortfolioSentinelContext(ctx context.Context, run PortfolioSentinelRun, note string) (PortfolioSentinelContext, error) {
	cfg, _ := s.GetPortfolioSentinelConfig(ctx)
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		return PortfolioSentinelContext{}, err
	}
	if run.PortfolioID != "" {
		filtered := make([]StockV2Portfolio, 0, 1)
		for _, p := range portfolios {
			if p.ID == run.PortfolioID {
				filtered = append(filtered, p)
				break
			}
		}
		portfolios = filtered
	}
	out := PortfolioSentinelContext{
		SchemaVersion: PortfolioSentinelReportSchemaVersion,
		RunID:         run.ID,
		Window: PortfolioSentinelWindowContext{
			Type:          run.WindowType,
			TriggerType:   run.TriggerType,
			StartAt:       run.WindowStartAt,
			EndAt:         run.WindowEndAt,
			MarketSession: portfolioSentinelMarketSession(run.WindowType),
		},
		DataFreshness: map[string]any{},
		ContextStats:  map[string]any{},
		Note:          note,
	}
	selectedEvents := make([]NewsEvent, 0, cfg.MaxNewsItems)
	selectedEventIDs := make(map[string]struct{})
	newsLinkCount := 0
	newsTruncated := false
	newsFilterStats := portfolioSentinelNewsFilterStats{
		SuppressedLowPriorityTerms:   map[string]int{},
		SuppressedLowPrioritySymbols: map[string]int{},
	}

	// 预取窗口内所有 NewsEvent，避免每个持仓重复查询。
	// 分页加载直到窗口内全部加载完或达到合理上限。
	windowEvents := s.prefetchWindowNewsEvents(ctx, run.WindowStartAt, run.WindowEndAt)

	allSymbols := make([]string, 0)
	for _, portfolio := range portfolios {
		holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
		if err != nil {
			return PortfolioSentinelContext{}, err
		}
		portfolioCtx := PortfolioSentinelPortfolioContext{Portfolio: portfolio}
		if snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolio.ID, 1); err == nil && len(snapshots) > 0 {
			snapshot := snapshots[0]
			portfolioCtx.Snapshot = &snapshot
		} else if err != nil {
			return PortfolioSentinelContext{}, err
		}
		symbols := make([]string, 0, len(holdings))
		for _, h := range holdings {
			if strings.TrimSpace(h.Symbol) != "" {
				symbols = append(symbols, h.Symbol)
				allSymbols = append(allSymbols, h.Symbol)
			}
		}
		quoteBySymbol := map[string]StockV2QuoteLatest{}
		if len(symbols) > 0 {
			quotes, err := s.store.GetLatestQuotes(ctx, symbols)
			if err != nil {
				return PortfolioSentinelContext{}, err
			}
			for _, q := range quotes {
				quoteBySymbol[q.Symbol] = q
			}
		}
		for _, holding := range holdings {
			holdingNews, holdingLinks, sentinelCandidates, truncated, filterStats, err := s.portfolioSentinelHoldingNews(ctx, holding, run, cfg, selectedEventIDs, &selectedEvents, windowEvents)
			if err != nil {
				return PortfolioSentinelContext{}, err
			}
			newsFilterStats.merge(filterStats)
			newsLinkCount += len(holdingLinks)
			if truncated {
				newsTruncated = true
			}
			hctx := PortfolioSentinelHoldingContext{
				Holding:        holding,
				Freshness:      map[string]any{},
				News:           holdingNews,
				NewsLinks:      holdingLinks,
				NewsCandidates: sentinelCandidates,
			}
			if quote, ok := quoteBySymbol[holding.Symbol]; ok {
				hctx.Quote = &quote
				hctx.Freshness["quote"] = quoteFreshnessSummary(quote)
			} else {
				hctx.Freshness["quote"] = map[string]any{"status": "missing"}
			}
			hctx.DailyBars = s.buildDailyBarsContext(ctx, holding.Symbol)
			hctx.MinuteBars = s.buildMinuteBarsContext(ctx, holding.Symbol)
			if profile, err := s.store.GetStockProfile(ctx, holding.Symbol); err == nil {
				hctx.Profile = &profile
			} else if !errors.Is(err, ErrStockProfileNotFound) {
				return PortfolioSentinelContext{}, err
			}
			portfolioCtx.Holdings = append(portfolioCtx.Holdings, hctx)
		}
		out.Portfolios = append(out.Portfolios, portfolioCtx)
		if txs, err := s.store.ListTransactions(ctx, portfolio.ID, 20); err == nil {
			out.Transactions = append(out.Transactions, txs...)
		} else {
			return PortfolioSentinelContext{}, err
		}
	}
	out.NewsEvents = selectedEvents
	if len(allSymbols) > 0 {
		reviews, err := s.store.ListOperationReviews(ctx, OperationReviewListFilter{Limit: 50})
		if err != nil {
			return PortfolioSentinelContext{}, err
		}
		out.RecentReviews = reviews
	}
	out.ContextStats = portfolioSentinelContextStats(out)
	out.ContextStats["newsLinkCount"] = newsLinkCount
	out.ContextStats["newsTruncated"] = newsTruncated
	portfolioSentinelApplyNewsFilterStats(out.ContextStats, newsFilterStats)
	return out, nil
}

// buildHoldingNewsCandidateSummary 从 context pack 中提取每个持仓的新闻候选摘要，
// 持久化到 result.contextSummary，使历史运行可追溯每条新闻的召回方式和分数来源。
func buildHoldingNewsCandidateSummary(pack PortfolioSentinelContext) map[string]any {
	holdings := make([]map[string]any, 0)
	for _, p := range pack.Portfolios {
		for _, h := range p.Holdings {
			if len(h.NewsCandidates) == 0 {
				continue
			}
			candidates := make([]map[string]any, 0, len(h.NewsCandidates))
			for _, c := range h.NewsCandidates {
				cand := map[string]any{
					"newsEventId":       c.NewsEventID,
					"totalScore":        c.TotalScore,
					"scoreBreakdown":    c.ScoreBreakdown,
					"recallMethods":     c.RecallMethods,
					"entityMatchScore":  c.EntityMatchScore,
					"keywordMatchScore": c.KeywordMatchScore,
					"semanticScore":     c.SemanticScore,
					"nlcScore":          c.NewsLinkCandidateScore,
					"sourceQualityScore": c.SourceQualityScore,
					"freshnessScore":    c.FreshnessScore,
				}
				if c.NewsEvent != nil {
					cand["newsEvent"] = map[string]any{
						"id":       c.NewsEvent.ID,
						"title":    c.NewsEvent.Title,
						"source":   c.NewsEvent.Source,
						"summary":  c.NewsEvent.Summary,
						"eventAt":  c.NewsEvent.EventAt,
						"url":      c.NewsEvent.URL,
					}
				}
				candidates = append(candidates, cand)
			}
			symbol := h.Holding.Symbol
			name := h.Holding.Name
			holdings = append(holdings, map[string]any{
				"symbol":       symbol,
				"name":         name,
				"portfolioId":  p.Portfolio.ID,
				"portfolioName": p.Portfolio.Name,
				"candidates":   candidates,
			})
		}
	}
	return map[string]any{
		"holdingNewsCandidates": holdings,
	}
}

func (s *Service) preparePortfolioSentinelNews(ctx context.Context, run PortfolioSentinelRun) error {
	if s == nil || s.store == nil {
		return nil
	}
	if !s.tryStartNewsPipelineRun() {
		return nil
	}
	defer s.finishNewsPipelineRun()
	for _, source := range []string{NewsSourceJin10, NewsSourceFinancialJuice} {
		if _, err := s.RunNewsProcessingBatch(ctx, source, 200, 200); err != nil {
			return err
		}
	}
	return nil
}

// prefetchWindowNewsEvents 预取窗口内全部 NewsEvent，分页加载直到窗口内全部加载完。
// 避免每个持仓重复查询 ListNewsEvents。
func (s *Service) prefetchWindowNewsEvents(ctx context.Context, windowStart, windowEnd time.Time) []NewsEvent {
	const pageSize = 500
	var all []NewsEvent
	offset := 0
	for {
		events, err := s.store.ListNewsEvents(ctx, NewsEventListFilter{
			Since:  windowStart,
			Until:  windowEnd,
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			if s.log != nil {
				s.log.Warn("prefetchWindowNewsEvents page failed", "offset", offset, "error", safelog.Text(err.Error(), 200))
			}
			break
		}
		if len(events) == 0 {
			break
		}
		all = append(all, events...)
		if len(events) < pageSize {
			break
		}
		offset += pageSize
		// 安全上限：避免极端情况内存膨胀
		if len(all) >= 5000 {
			break
		}
	}
	return all
}

func (s *Service) portfolioSentinelHoldingNews(
	ctx context.Context,
	holding StockV2Holding,
	run PortfolioSentinelRun,
	cfg PortfolioSentinelConfig,
	selectedEventIDs map[string]struct{},
	selectedEvents *[]NewsEvent,
	windowEvents []NewsEvent,
) ([]NewsEvent, []NewsLinkCandidate, []SentinelNewsCandidate, bool, portfolioSentinelNewsFilterStats, error) {
	perHoldingLimit := cfg.MaxNewsPerHolding
	if perHoldingLimit <= 0 {
		perHoldingLimit = 50
	}

	// 直接从窗口 NewsEvent 召回（不再依赖 NewsLinkCandidate 作为唯一入口）
	allCandidates, err := s.portfolioSentinelHoldingNewsDirect(ctx, holding, run, perHoldingLimit*portfolioSentinelNewsScanMultiplier, windowEvents)
	if err != nil {
		return nil, nil, nil, false, portfolioSentinelNewsFilterStats{}, err
	}

	// 应用高/低优先级分桶（保留旧逻辑的噪音抑制）
	candidates, filterStats := applySentinelCandidateBucketing(allCandidates, perHoldingLimit)

	// 加载实际的 NewsLinkCandidate 行（用于兼容旧接口展示）
	nlcByEvent := s.loadNewsLinkCandidatesForHolding(ctx, holding, run)

	events := make([]NewsEvent, 0, len(candidates))
	links := make([]NewsLinkCandidate, 0, len(candidates))
	truncated := false
	for _, candidate := range candidates {
		if candidate.NewsEvent == nil {
			continue
		}
		event := *candidate.NewsEvent
		events = append(events, event)
		// 使用实际的 NewsLinkCandidate 数据（如果有）
		if nlc, ok := nlcByEvent[event.ID]; ok {
			links = append(links, nlc)
		}
		if _, ok := selectedEventIDs[event.ID]; !ok {
			if cfg.MaxNewsItems > 0 && len(*selectedEvents) >= cfg.MaxNewsItems {
				truncated = true
				continue
			}
			selectedEventIDs[event.ID] = struct{}{}
			*selectedEvents = append(*selectedEvents, event)
		}
	}
	return events, links, candidates, truncated, filterStats, nil
}

// applySentinelCandidateBucketing 对多路打分后的候选应用高/低优先级分桶。
// 高优先级（实体匹配或高分 NLC）不受数量限制，低优先级限制为 10 条。
func applySentinelCandidateBucketing(candidates []SentinelNewsCandidate, detailLimit int) ([]SentinelNewsCandidate, portfolioSentinelNewsFilterStats) {
	stats := portfolioSentinelNewsFilterStats{
		InputCandidates:              len(candidates),
		SuppressedLowPriorityTerms:   map[string]int{},
		SuppressedLowPrioritySymbols: map[string]int{},
	}
	if detailLimit <= 0 {
		detailLimit = 50
	}
	if len(candidates) <= detailLimit && len(candidates) <= portfolioSentinelLowPriorityNewsDetailLimit {
		stats.RetainedCandidates = len(candidates)
		for _, c := range candidates {
			if isSentinelLowPriority(c) {
				stats.RetainedLowPriorityCount++
			}
		}
		return candidates, stats
	}
	lowLimit := portfolioSentinelLowPriorityNewsDetailLimit
	if lowLimit > detailLimit {
		lowLimit = detailLimit
	}
	highLimit := detailLimit - lowLimit
	if highLimit < 0 {
		highLimit = 0
	}
	high := make([]SentinelNewsCandidate, 0, detailLimit)
	low := make([]SentinelNewsCandidate, 0, lowLimit)
	for _, candidate := range candidates {
		if isSentinelLowPriority(candidate) {
			if len(low) < lowLimit {
				low = append(low, candidate)
				stats.RetainedLowPriorityCount++
			} else {
				stats.SuppressedLowPriorityCount++
				stats.SuppressedLowPrioritySymbols[strings.TrimSpace(candidate.Symbol)]++
				for _, method := range candidate.RecallMethods {
					stats.SuppressedLowPriorityTerms[method]++
				}
			}
			continue
		}
		if len(high) < highLimit {
			high = append(high, candidate)
		}
	}
	out := make([]SentinelNewsCandidate, 0, len(high)+len(low))
	out = append(out, high...)
	out = append(out, low...)
	stats.RetainedCandidates = len(out)
	return out, stats
}

// isSentinelLowPriority 判断候选是否为低优先级。
// 高优先级：实体匹配（代码/名称/别名）或 NLC 分数 >= 65（归一化 0.65）。
func isSentinelLowPriority(c SentinelNewsCandidate) bool {
	if c.EntityMatchScore > 0 {
		return false
	}
	if c.NewsLinkCandidateScore >= normalizeNLCSCore(portfolioSentinelNewsHighScoreThreshold) {
		return false
	}
	return true
}

// portfolioSentinelHoldingNewsDirect 直接从窗口 NewsEvent 表召回新闻，
// 对每条新闻计算与持仓的多路相关性分数，按总分排序取 TopK。
// 不再依赖 NewsLinkCandidate 作为唯一入口 — NLC 降级为辅助信号。
// windowEvents 是预取的窗口内全部 NewsEvent，避免每个持仓重复查询。
func (s *Service) portfolioSentinelHoldingNewsDirect(
	ctx context.Context,
	holding StockV2Holding,
	run PortfolioSentinelRun,
	limit int,
	windowEvents []NewsEvent,
) ([]SentinelNewsCandidate, error) {
	// 1. 使用预取的窗口 NewsEvent（BuildPortfolioSentinelContext 已一次性加载）
	events := windowEvents
	if len(events) == 0 {
		return nil, nil
	}

	// 2. 获取持仓画像（用于打分）
	profile, _ := s.store.GetStockProfile(ctx, holding.Symbol)

	// 3. 预取该持仓的 NewsLinkCandidate（辅助信号）
	nlcByEvent := s.loadNewsLinkCandidatesForHolding(ctx, holding, run)

	// 4. 对每条事件计算多路分数
	candidates := make([]SentinelNewsCandidate, 0, len(events))
	for i := range events {
		candidate := s.scoreNewsForHolding(ctx, &events[i], holding, profile, nlcByEvent)
		// 召回门：必须有"真实"召回信号（实体/关键词/NLC），来源和新鲜度只能加权
		if hasSentinelRecallReason(candidate) {
			candidates = append(candidates, candidate)
		}
	}

	// 5. 语义召回：用持仓画像文本在窗口 event IDs 内做向量搜索
	semanticHits := s.semanticSearchNewsForHolding(ctx, holding, profile, run, windowEvents)
	candidates = s.mergeSemanticCandidates(candidates, semanticHits, holding)

	// 6. 按总分排序，取 TopK
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].TotalScore == candidates[j].TotalScore {
			return candidates[i].NewsEventID < candidates[j].NewsEventID
		}
		return candidates[i].TotalScore > candidates[j].TotalScore
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// hasSentinelRecallReason 判断候选是否有"真实"召回信号。
// 来源质量和新鲜度只能加权排序，不能单独构成召回理由。
func hasSentinelRecallReason(c SentinelNewsCandidate) bool {
	return c.EntityMatchScore > 0 ||
		c.KeywordMatchScore > 0 ||
		c.NewsLinkCandidateScore > 0 ||
		c.SemanticScore > 0
}

// loadNewsLinkCandidatesForHolding 预取窗口内该持仓的 NewsLinkCandidate，
// 作为辅助信号（不再是唯一入口）。
func (s *Service) loadNewsLinkCandidatesForHolding(
	ctx context.Context,
	holding StockV2Holding,
	run PortfolioSentinelRun,
) map[string]NewsLinkCandidate {
	nlcs, err := s.store.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{
		Symbol: holding.Symbol,
		Market: holding.Market,
		Since:  run.WindowStartAt,
		Until:  run.WindowEndAt,
		Limit:  200,
	})
	if err != nil {
		return map[string]NewsLinkCandidate{}
	}
	out := make(map[string]NewsLinkCandidate, len(nlcs))
	for _, nlc := range nlcs {
		out[nlc.NewsEventID] = nlc
	}
	return out
}

// scoreNewsForHolding 计算单条新闻与单个持仓的多路相关性分数。
func (s *Service) scoreNewsForHolding(
	ctx context.Context,
	event *NewsEvent,
	holding StockV2Holding,
	profile StockProfile,
	nlcByEvent map[string]NewsLinkCandidate,
) SentinelNewsCandidate {
	candidate := SentinelNewsCandidate{
		NewsEventID:    event.ID,
		NewsEvent:      event,
		Symbol:         holding.Symbol,
		ScoreBreakdown: map[string]float64{},
	}

	// 1. 实体匹配（代码/名称/别名）
	candidate.EntityMatchScore = scoreEntityMatch(event, holding, profile)
	if candidate.EntityMatchScore > 0 {
		candidate.RecallMethods = append(candidate.RecallMethods, "entity_match")
	}
	candidate.ScoreBreakdown["entity"] = candidate.EntityMatchScore

	// 2. 关键词匹配（主营/行业/概念）
	candidate.KeywordMatchScore = scoreKeywordMatch(event, profile)
	if candidate.KeywordMatchScore > 0 {
		candidate.RecallMethods = append(candidate.RecallMethods, "keyword")
	}
	candidate.ScoreBreakdown["keyword"] = candidate.KeywordMatchScore

	// 3. 既有 NewsLinkCandidate 分数（归一化到 0-1）
	if nlc, ok := nlcByEvent[event.ID]; ok {
		candidate.NewsLinkCandidateScore = normalizeNLCSCore(nlc.Score)
		candidate.RecallMethods = append(candidate.RecallMethods, "news_link")
	}
	candidate.ScoreBreakdown["news_link"] = candidate.NewsLinkCandidateScore

	// 4. 来源质量
	candidate.SourceQualityScore = scoreSourceQuality(event.Source)
	candidate.ScoreBreakdown["source_quality"] = candidate.SourceQualityScore

	// 5. 新鲜度
	candidate.FreshnessScore = scoreFreshness(event.EventAt)
	candidate.ScoreBreakdown["freshness"] = candidate.FreshnessScore

	// 加权合并（语义分数在 mergeSemanticCandidates 中补充）
	candidate.TotalScore =
		candidate.EntityMatchScore*0.35 +
			candidate.KeywordMatchScore*0.20 +
			candidate.NewsLinkCandidateScore*0.15 +
			candidate.SourceQualityScore*0.15 +
			candidate.FreshnessScore*0.15

	return candidate
}

// scoreEntityMatch 检查新闻文本中是否包含股票代码、名称或别名。
func scoreEntityMatch(event *NewsEvent, holding StockV2Holding, profile StockProfile) float64 {
	text := strings.ToLower(event.Title + " " + event.Summary + " " + event.Content)
	if text == " " {
		return 0
	}

	// 股票代码匹配（如 "300750"）
	symbol := strings.ToLower(strings.TrimSpace(holding.Symbol))
	if symbol != "" && strings.Contains(text, symbol) {
		return 1.0
	}

	// 名称匹配（如 "宁德时代"）
	name := strings.ToLower(strings.TrimSpace(profile.Name))
	if name != "" && strings.Contains(text, name) {
		return 0.9
	}

	// 别名匹配
	for _, alias := range profile.Aliases {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias != "" && strings.Contains(text, alias) {
			return 0.7
		}
	}
	for _, alias := range profile.AliasesZh {
		alias := strings.ToLower(strings.TrimSpace(alias))
		if alias != "" && strings.Contains(text, alias) {
			return 0.7
		}
	}

	return 0
}

// scoreKeywordMatch 检查新闻文本与画像关键词/行业/概念的重叠度。
func scoreKeywordMatch(event *NewsEvent, profile StockProfile) float64 {
	text := strings.ToLower(event.Title + " " + event.Summary + " " + event.Content)
	if text == " " {
		return 0
	}

	hitCount := 0
	totalKeywords := 0

	// 行业匹配
	if industry := strings.ToLower(strings.TrimSpace(profile.Industry)); industry != "" {
		totalKeywords++
		if strings.Contains(text, industry) {
			hitCount++
		}
	}

	// 中文关键词
	for _, kw := range profile.KeywordsZh {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" {
			continue
		}
		totalKeywords++
		if strings.Contains(text, kw) {
			hitCount++
		}
	}

	// 概念
	for _, concept := range profile.Concepts {
		concept = strings.ToLower(strings.TrimSpace(concept))
		if concept == "" {
			continue
		}
		totalKeywords++
		if strings.Contains(text, concept) {
			hitCount++
		}
	}

	// 标签
	for _, tag := range profile.Tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		totalKeywords++
		if strings.Contains(text, tag) {
			hitCount++
		}
	}

	if totalKeywords == 0 || hitCount == 0 {
		return 0
	}
	// 归一化：命中比例，但至少命中 2 个才给高分
	ratio := float64(hitCount) / float64(totalKeywords)
	if hitCount >= 3 {
		return 0.8
	}
	if hitCount >= 2 {
		return 0.5
	}
	return ratio * 0.3
}

// scoreSourceQuality 根据新闻来源给质量分。
func scoreSourceQuality(source string) float64 {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "jin10", "financialjuice":
		return 0.9
	case "cls", "cninfo", "sse":
		return 0.85
	case "eastmoney", "10jqka":
		return 0.7
	default:
		return 0.5
	}
}

// scoreFreshness 根据事件时间给新鲜度分（越近越高）。
func scoreFreshness(eventAt time.Time) float64 {
	if eventAt.IsZero() {
		return 0.3
	}
	age := time.Since(eventAt)
	switch {
	case age < 1*time.Hour:
		return 1.0
	case age < 4*time.Hour:
		return 0.85
	case age < 12*time.Hour:
		return 0.65
	case age < 24*time.Hour:
		return 0.45
	default:
		return 0.25
	}
}

// normalizeNLCSCore 将 NewsLinkCandidate 的原始分数（通常 0-100）归一化到 0-1。
func normalizeNLCSCore(rawScore float64) float64 {
	if rawScore <= 0 {
		return 0
	}
	normalized := rawScore / 100.0
	if normalized > 1.0 {
		return 1.0
	}
	return normalized
}

// semanticSearchNewsForHolding 使用持仓画像文本对窗口内 news_event embedding 做反向语义搜索。
// 这是"持仓画像 → 新闻"的方向，与原来的"新闻 → 股票画像"互补。
// 关键：先确定窗口 NewsEvent IDs，再在这些 ID 内做向量搜索，避免全库 Top20 后过滤。
func (s *Service) semanticSearchNewsForHolding(
	ctx context.Context,
	holding StockV2Holding,
	profile StockProfile,
	run PortfolioSentinelRun,
	windowEvents []NewsEvent,
) []SemanticNewsEventResult {
	queryText := buildHoldingQueryText(profile)
	if strings.TrimSpace(queryText) == "" || len(windowEvents) == 0 {
		return nil
	}

	// 收集窗口内所有 NewsEvent ID
	eventIDs := make([]string, 0, len(windowEvents))
	for _, e := range windowEvents {
		eventIDs = append(eventIDs, e.ID)
	}

	// 在窗口 event IDs 范围内做向量搜索
	hits, err := s.SemanticSearchNewsEventsByIDs(ctx, queryText, eventIDs, 20, 0.3)
	if err != nil {
		return nil
	}
	return hits
}

// buildHoldingQueryText 从持仓画像构造语义搜索查询文本。
func buildHoldingQueryText(profile StockProfile) string {
	parts := []string{
		profile.Name,
		profile.Industry,
		profile.BusinessSummaryZh,
		strings.Join(profile.Concepts, " "),
		strings.Join(profile.Tags, " "),
		strings.Join(profile.KeywordsZh, " "),
		profile.ProfileTextZh,
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

// mergeSemanticCandidates 将语义搜索结果合并到已有候选列表中。
// 如果某条新闻已在候选列表中，补充其 SemanticScore；否则新增条目。
func (s *Service) mergeSemanticCandidates(
	existing []SentinelNewsCandidate,
	semanticHits []SemanticNewsEventResult,
	holding StockV2Holding,
) []SentinelNewsCandidate {
	if len(semanticHits) == 0 {
		return existing
	}

	byEventID := make(map[string]*SentinelNewsCandidate, len(existing))
	for i := range existing {
		byEventID[existing[i].NewsEventID] = &existing[i]
	}

	for _, hit := range semanticHits {
		if cand, ok := byEventID[hit.Event.ID]; ok {
			// 已存在，补充语义分数
			cand.SemanticScore = hit.Score
			cand.ScoreBreakdown["semantic"] = hit.Score
			cand.RecallMethods = append(cand.RecallMethods, "semantic")
			// 重新计算总分（加入语义维度）
			cand.TotalScore =
				cand.EntityMatchScore*0.30 +
					cand.KeywordMatchScore*0.15 +
					cand.SemanticScore*0.25 +
					cand.NewsLinkCandidateScore*0.10 +
					cand.SourceQualityScore*0.10 +
					cand.FreshnessScore*0.10
		} else {
			// 新条目（纯语义召回）
			event := hit.Event
			newCand := SentinelNewsCandidate{
				NewsEventID:       event.ID,
				NewsEvent:         &event,
				Symbol:            holding.Symbol,
				SemanticScore:     hit.Score,
				SourceQualityScore: scoreSourceQuality(event.Source),
				FreshnessScore:    scoreFreshness(event.EventAt),
				ScoreBreakdown:    map[string]float64{"semantic": hit.Score},
				RecallMethods:     []string{"semantic"},
			}
			newCand.ScoreBreakdown["source_quality"] = newCand.SourceQualityScore
			newCand.ScoreBreakdown["freshness"] = newCand.FreshnessScore
			newCand.TotalScore =
				newCand.SemanticScore*0.40 +
					newCand.SourceQualityScore*0.30 +
					newCand.FreshnessScore*0.30
			existing = append(existing, newCand)
		}
	}
	return existing
}

func sentinelCandidateReason(c SentinelNewsCandidate) string {
	reasons := make([]string, 0, len(c.RecallMethods))
	for _, m := range c.RecallMethods {
		switch m {
		case "entity_match":
			reasons = append(reasons, "实体匹配")
		case "keyword":
			reasons = append(reasons, "关键词匹配")
		case "semantic":
			reasons = append(reasons, "语义相似")
		case "news_link":
			reasons = append(reasons, "新闻关联候选")
		}
	}
	return strings.Join(reasons, ", ")
}

func sentinelCandidateMatchedTerms(c SentinelNewsCandidate) []string {
	terms := make([]string, 0, len(c.ScoreBreakdown))
	for dim, score := range c.ScoreBreakdown {
		if score > 0 {
			terms = append(terms, dim)
		}
	}
	return terms
}

func countLowPrioritySentinelCandidates(candidates []SentinelNewsCandidate) int {
	count := 0
	for _, c := range candidates {
		// 低优先级：只有语义或关键词匹配，没有实体或 NLC 命中
		hasEntity := c.EntityMatchScore > 0
		hasNLC := c.NewsLinkCandidateScore > 0
		if !hasEntity && !hasNLC {
			count++
		}
	}
	return count
}

func portfolioSentinelNewsScanLimit(detailLimit int) int {
	if detailLimit <= 0 {
		detailLimit = 50
	}
	limit := detailLimit * portfolioSentinelNewsScanMultiplier
	if limit < portfolioSentinelNewsScanMin {
		limit = portfolioSentinelNewsScanMin
	}
	if limit > 500 {
		limit = 500
	}
	return limit
}

func portfolioSentinelSelectNewsCandidates(candidates []NewsLinkCandidate, detailLimit int) ([]NewsLinkCandidate, portfolioSentinelNewsFilterStats) {
	stats := portfolioSentinelNewsFilterStats{
		InputCandidates:              len(candidates),
		SuppressedLowPriorityTerms:   map[string]int{},
		SuppressedLowPrioritySymbols: map[string]int{},
	}
	if detailLimit <= 0 {
		detailLimit = 50
	}
	if len(candidates) <= detailLimit && len(candidates) <= portfolioSentinelLowPriorityNewsDetailLimit {
		stats.RetainedCandidates = len(candidates)
		for _, candidate := range candidates {
			if portfolioSentinelLowPriorityNewsCandidate(candidate) {
				stats.RetainedLowPriorityCount++
			}
		}
		return candidates, stats
	}
	lowLimit := portfolioSentinelLowPriorityNewsDetailLimit
	if lowLimit > detailLimit {
		lowLimit = detailLimit
	}
	highLimit := detailLimit - lowLimit
	if highLimit < 0 {
		highLimit = 0
	}
	high := make([]NewsLinkCandidate, 0, detailLimit)
	low := make([]NewsLinkCandidate, 0, lowLimit)
	for _, candidate := range candidates {
		if portfolioSentinelLowPriorityNewsCandidate(candidate) {
			if len(low) < lowLimit {
				low = append(low, candidate)
				stats.RetainedLowPriorityCount++
			} else {
				stats.SuppressedLowPriorityCount++
				stats.SuppressedLowPrioritySymbols[strings.TrimSpace(candidate.Symbol)]++
				for _, term := range candidate.MatchedTerms {
					term = strings.TrimSpace(term)
					if term != "" {
						stats.SuppressedLowPriorityTerms[term]++
					}
				}
			}
			continue
		}
		if len(high) < highLimit {
			high = append(high, candidate)
		}
	}
	out := make([]NewsLinkCandidate, 0, len(high)+len(low))
	out = append(out, high...)
	out = append(out, low...)
	stats.RetainedCandidates = len(out)
	return out, stats
}

func portfolioSentinelLowPriorityNewsCandidate(candidate NewsLinkCandidate) bool {
	if candidate.Score >= portfolioSentinelNewsHighScoreThreshold {
		return false
	}
	reason := candidate.Reason
	if strings.Contains(reason, "命中股票代码") ||
		strings.Contains(reason, "命中标的名称") ||
		strings.Contains(reason, "命中别名") {
		return false
	}
	return true
}

func (s *portfolioSentinelNewsFilterStats) merge(other portfolioSentinelNewsFilterStats) {
	s.InputCandidates += other.InputCandidates
	s.RetainedCandidates += other.RetainedCandidates
	s.RetainedLowPriorityCount += other.RetainedLowPriorityCount
	s.SuppressedLowPriorityCount += other.SuppressedLowPriorityCount
	if s.SuppressedLowPriorityTerms == nil {
		s.SuppressedLowPriorityTerms = map[string]int{}
	}
	for term, count := range other.SuppressedLowPriorityTerms {
		s.SuppressedLowPriorityTerms[term] += count
	}
	if s.SuppressedLowPrioritySymbols == nil {
		s.SuppressedLowPrioritySymbols = map[string]int{}
	}
	for symbol, count := range other.SuppressedLowPrioritySymbols {
		s.SuppressedLowPrioritySymbols[symbol] += count
	}
}

func portfolioSentinelApplyNewsFilterStats(stats map[string]any, filter portfolioSentinelNewsFilterStats) {
	stats["newsInputCandidateCount"] = filter.InputCandidates
	stats["newsRetainedCandidateCount"] = filter.RetainedCandidates
	stats["retainedLowPriorityNewsCount"] = filter.RetainedLowPriorityCount
	stats["suppressedLowPriorityNewsCount"] = filter.SuppressedLowPriorityCount
	if filter.SuppressedLowPriorityCount > 0 {
		stats["topSuppressedLowPriorityTerms"] = portfolioSentinelTopCounts(filter.SuppressedLowPriorityTerms, 12)
		stats["suppressedLowPriorityBySymbol"] = portfolioSentinelTopCounts(filter.SuppressedLowPrioritySymbols, 12)
	}
}

func portfolioSentinelTopCounts(counts map[string]int, limit int) []map[string]any {
	type item struct {
		key   string
		count int
	}
	items := make([]item, 0, len(counts))
	for key, count := range counts {
		if strings.TrimSpace(key) == "" || count <= 0 {
			continue
		}
		items = append(items, item{key: key, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].key < items[j].key
		}
		return items[i].count > items[j].count
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"value": item.key, "count": item.count})
	}
	return out
}

func (s *Service) startPortfolioSentinelRunAsync(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack PortfolioSentinelContext, modelName string) {
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("portfolio sentinel agent run panicked", "run_id", run.ID, "panic", r)
			}
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()
	if _, _, err := s.executePortfolioSentinelRun(ctx, run, ledger, pack, modelName); err != nil && s.log != nil {
		s.log.Warn("portfolio sentinel agent run finished with error", "run_id", run.ID, "ledger_id", ledger.ID, "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) executePortfolioSentinelRun(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack PortfolioSentinelContext, modelName string) (AgentRun, AgentDecisionLedger, error) {
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
		return finalRun, finalLedger, ErrAgentExecutorUnavailable
	}
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update portfolio sentinel run to running failed", "run_id", run.ID, "error", safelog.Text(err.Error(), 240))
	}
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecutePortfolioSentinel(ctx, taskID, pack, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execErr
}

func (s *Service) ProcessPortfolioSentinelSubmittedResult(ctx context.Context, runID string, submitted AgentTaskSubmittedResult) (PortfolioSentinelResult, error) {
	run, err := s.store.GetPortfolioSentinelRun(ctx, runID)
	if err != nil {
		return PortfolioSentinelResult{}, err
	}
	report, err := portfolioSentinelReportFromResult(submitted.Result)
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}

	// 优先更新预创建的结果记录（含 newsCandidates 摘要），保留 contextSummary 中的历史可追溯数据。
	existing, _ := s.store.GetPortfolioSentinelResultByRunID(ctx, run.ID)
	result := PortfolioSentinelResult{
		RunID:         run.ID,
		SchemaVersion: report.SchemaVersion,
		Summary:       firstNonEmpty(report.RunSummary, submitted.ResultSummary),
		RiskLevel:     normalizePortfolioSentinelRiskLevel(report.OverallRiskLevel),
		RawResult:     submitted.Result,
	}
	if existing != nil {
		result.ID = existing.ID
		result.CreatedAt = existing.CreatedAt
		// 合并 contextSummary：保留预存的 holdingNewsCandidates，加入 confidence
		result.ContextSummary = make(map[string]any, len(existing.ContextSummary)+1)
		for k, v := range existing.ContextSummary {
			result.ContextSummary[k] = v
		}
	} else {
		result.ContextSummary = map[string]any{}
	}
	result.ContextSummary["confidence"] = submitted.Confidence
	derived, err := s.derivePortfolioSentinelObjects(ctx, run, report, submitted.Result)
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}
	result.DerivedAlertIDs = derived.alertIDs
	result.DerivedMonitorHitIDs = derived.hitIDs
	result.DerivedReviewIDs = derived.reviewIDs
	if existing != nil {
		result, err = s.store.UpdatePortfolioSentinelResult(ctx, result)
	} else {
		result, err = s.store.CreatePortfolioSentinelResult(ctx, result)
	}
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}
	run.Status = PortfolioSentinelStatusCompleted
	run.ResultRiskLevel = result.RiskLevel
	run.GeneratedAlertCount = len(result.DerivedAlertIDs)
	run.GeneratedHitCount = len(result.DerivedMonitorHitIDs)
	run.GeneratedReviewCount = len(result.DerivedReviewIDs)
	run.FinishedAt = time.Now()
	run.ErrorMessage = ""
	if _, err := s.store.UpdatePortfolioSentinelRun(ctx, run); err != nil {
		return PortfolioSentinelResult{}, err
	}
	return result, nil
}

type portfolioSentinelDerivedObjects struct {
	alertIDs  []string
	hitIDs    []string
	reviewIDs []string
}

func (s *Service) derivePortfolioSentinelObjects(ctx context.Context, run PortfolioSentinelRun, report PortfolioSentinelReport, rawResult map[string]any) (portfolioSentinelDerivedObjects, error) {
	out := portfolioSentinelDerivedObjects{}
	monitorRun, err := s.store.CreateMonitorRun(ctx, MonitorRun{
		TaskType:     AgentTaskTypePortfolioSentinel,
		Status:       MonitorRunStatusCompleted,
		TriggerType:  run.TriggerType,
		StartedAt:    run.StartedAt,
		FinishedAt:   time.Now(),
		ScopeSummary: portfolioSentinelScopeSummary(run),
		ScannedCount: run.ScannedHoldingCount,
		Metadata: map[string]any{
			"portfolioSentinelRunId": run.ID,
			"riskLevel":              report.OverallRiskLevel,
		},
	})
	if err != nil {
		return out, err
	}
	actions := report.PortfolioActions
	if len(actions) == 0 && shouldCreatePortfolioSentinelRiskHit(report) {
		for _, affected := range report.AffectedHoldings {
			actions = append(actions, PortfolioSentinelAction{
				Symbol:        affected.Symbol,
				Market:        affected.Market,
				OutputType:    OperationReviewOutputContinueMonitoring,
				ResultSummary: strings.Join(affected.Reasons, "; "),
				Reason:        report.RunSummary,
			})
		}
	}
	for _, action := range actions {
		outputType := normalizePortfolioSentinelActionOutputType(action.OutputType)
		if outputType == "" || strings.TrimSpace(action.Symbol) == "" {
			continue
		}
		hit, err := s.store.CreateMonitorHit(ctx, MonitorHit{
			RunID:       monitorRun.ID,
			TaskType:    AgentTaskTypePortfolioSentinel,
			Status:      MonitorHitStatusCandidate,
			PortfolioID: firstNonEmpty(action.PortfolioID, run.PortfolioID),
			Symbol:      strings.TrimSpace(action.Symbol),
			Market:      strings.TrimSpace(action.Market),
			Title:       firstNonEmpty(action.ResultSummary, "组合哨兵风险命中"),
			Summary:     firstNonEmpty(action.Reason, report.RunSummary),
			Evidence: map[string]any{
				"source":                     AgentTaskTypePortfolioSentinel,
				"portfolioSentinelRunId":     run.ID,
				"portfolioSentinelRawResult": rawResult,
				"riskLevel":                  report.OverallRiskLevel,
				"action":                     action,
			},
		})
		if err != nil {
			return out, err
		}
		out.hitIDs = append(out.hitIDs, hit.ID)
		review, err := s.CreateReviewFromMonitorHit(ctx, hit.ID)
		if err != nil {
			return out, err
		}
		saveReq := RequestSaveOperationReviewResult{
			OutputType:    outputType,
			ResultSummary: firstNonEmpty(action.ResultSummary, report.RunSummary),
			Result:        portfolioSentinelActionReviewResult(action, outputType),
			Status:        OperationReviewStatusCompleted,
		}
		updated, err := s.saveOperationReviewResult(ctx, review.ID, saveReq, nil)
		if err != nil {
			return out, err
		}
		out.reviewIDs = append(out.reviewIDs, updated.ID)
		if alertID := s.alertIDForReview(ctx, updated); alertID != "" {
			out.alertIDs = append(out.alertIDs, alertID)
		}
	}
	monitorRun.HitCount = len(out.hitIDs)
	monitorRun.ReviewCount = len(out.reviewIDs)
	monitorRun.AlertCount = len(out.alertIDs)
	_, _ = s.store.UpdateMonitorRun(ctx, monitorRun)
	return out, nil
}

func (s *Service) GetPortfolioSentinelRunDetail(ctx context.Context, id string) (PortfolioSentinelRunDetail, error) {
	run, err := s.store.GetPortfolioSentinelRun(ctx, id)
	if err != nil {
		return PortfolioSentinelRunDetail{}, err
	}
	detail := PortfolioSentinelRunDetail{Run: run}
	if result, err := s.store.GetPortfolioSentinelResultByRunID(ctx, run.ID); err != nil {
		return PortfolioSentinelRunDetail{}, err
	} else if result != nil {
		detail.Result = result
		for _, id := range result.DerivedMonitorHitIDs {
			if hit, err := s.store.GetMonitorHit(ctx, id); err == nil {
				detail.Hits = append(detail.Hits, hit)
			}
		}
		for _, id := range result.DerivedReviewIDs {
			if review, err := s.store.GetOperationReview(ctx, id); err == nil {
				detail.Reviews = append(detail.Reviews, review)
			}
		}
		for _, id := range result.DerivedAlertIDs {
			if alert, err := s.store.GetAlert(ctx, id); err == nil {
				detail.Alerts = append(detail.Alerts, alert)
			}
		}
	}
	if run.AgentRunID != "" {
		if agentRun, err := s.store.GetAgentRun(ctx, run.AgentRunID); err == nil {
			detail.Agent = &agentRun
		} else if !errors.Is(err, ErrAgentRunNotFound) {
			return PortfolioSentinelRunDetail{}, err
		}
	}
	if run.DecisionLedgerID != "" {
		if ledger, err := s.store.GetAgentDecisionLedger(ctx, run.DecisionLedgerID); err == nil {
			detail.Ledger = &ledger
		} else if !errors.Is(err, ErrAgentDecisionLedgerNotFound) {
			return PortfolioSentinelRunDetail{}, err
		}
	}
	return detail, nil
}

func (s *Service) ListPortfolioSentinelRuns(ctx context.Context, filter PortfolioSentinelRunListFilter) ([]PortfolioSentinelRun, error) {
	return s.store.ListPortfolioSentinelRuns(ctx, filter)
}

func (s *Service) GetPortfolioSentinelResult(ctx context.Context, id string) (PortfolioSentinelResult, error) {
	return s.store.GetPortfolioSentinelResult(ctx, strings.TrimSpace(id))
}

func (s *Service) CountPortfolioSentinelRuns(ctx context.Context, filter PortfolioSentinelRunListFilter) (int, error) {
	return s.store.CountPortfolioSentinelRuns(ctx, filter)
}

func portfolioSentinelReportFromResult(result map[string]any) (PortfolioSentinelReport, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return PortfolioSentinelReport{}, err
	}
	var report PortfolioSentinelReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return PortfolioSentinelReport{}, err
	}
	if report.SchemaVersion != PortfolioSentinelReportSchemaVersion {
		return PortfolioSentinelReport{}, ErrInvalidPortfolioSentinelResult
	}
	report.OverallRiskLevel = normalizePortfolioSentinelRiskLevel(report.OverallRiskLevel)
	if strings.TrimSpace(report.RunSummary) == "" {
		return PortfolioSentinelReport{}, ErrInvalidPortfolioSentinelResult
	}
	return report, nil
}

func portfolioSentinelWindowRange(windowType, rawStart, rawEnd string, now time.Time) (time.Time, time.Time, error) {
	start := parsePortfolioSentinelTime(rawStart)
	end := parsePortfolioSentinelTime(rawEnd)
	if start.IsZero() && end.IsZero() {
		end = now
		start = now.Add(-12 * time.Hour)
	}
	if start.IsZero() {
		start = end.Add(-12 * time.Hour)
	}
	if end.IsZero() {
		end = now
	}
	if !end.After(start) || !validPortfolioSentinelWindowType(windowType) {
		return time.Time{}, time.Time{}, ErrInvalidPortfolioSentinelInput
	}
	return start, end, nil
}

func parsePortfolioSentinelTime(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func defaultPortfolioSentinelConfig() PortfolioSentinelConfig {
	return PortfolioSentinelConfig{
		ID:                      "default",
		PreMarketEnabled:        true,
		MiddayEnabled:           true,
		PostCloseEnabled:        true,
		MaxNewsItems:            200,
		MaxNewsPerHolding:       50,
		AgentDoublecheckEnabled: true,
		UpdatedAt:               time.Now(),
	}
}

func normalizePortfolioSentinelConfig(cfg PortfolioSentinelConfig) PortfolioSentinelConfig {
	if cfg.ID == "" {
		cfg.ID = "default"
	}
	if cfg.MaxNewsItems <= 0 || cfg.MaxNewsItems == 80 || cfg.MaxNewsItems > 500 {
		cfg.MaxNewsItems = 200
	}
	if cfg.MaxNewsPerHolding <= 0 || cfg.MaxNewsPerHolding == 20 || cfg.MaxNewsPerHolding > 100 {
		cfg.MaxNewsPerHolding = 50
	}
	return cfg
}

func portfolioSentinelWindowEnabled(cfg PortfolioSentinelConfig, windowType string) bool {
	if !cfg.Enabled {
		return false
	}
	switch windowType {
	case PortfolioSentinelWindowPreMarket:
		return cfg.PreMarketEnabled
	case PortfolioSentinelWindowMidday:
		return cfg.MiddayEnabled
	case PortfolioSentinelWindowPostClose:
		return cfg.PostCloseEnabled
	default:
		return false
	}
}

func validPortfolioSentinelWindowType(v string) bool {
	return v == PortfolioSentinelWindowManual || v == PortfolioSentinelWindowPreMarket || v == PortfolioSentinelWindowMidday || v == PortfolioSentinelWindowPostClose
}

func normalizePortfolioSentinelRiskLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case PortfolioSentinelRiskLow, PortfolioSentinelRiskMedium, PortfolioSentinelRiskHigh, PortfolioSentinelRiskCritical:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return PortfolioSentinelRiskLow
	}
}

func normalizePortfolioSentinelActionOutputType(v string) string {
	switch strings.TrimSpace(v) {
	case OperationReviewOutputTradeSignal, OperationReviewOutputProposedOperation, OperationReviewOutputIgnore, OperationReviewOutputContinueMonitoring:
		return strings.TrimSpace(v)
	default:
		return OperationReviewOutputContinueMonitoring
	}
}

func portfolioSentinelActionReviewResult(action PortfolioSentinelAction, outputType string) map[string]any {
	result := map[string]any{
		"facts":       []any{},
		"inferences":  []any{action.Reason},
		"assumptions": []any{},
		"reason":      action.Reason,
		"riskNotes":   action.RiskNotes,
		"confidence":  action.Confidence,
	}
	if outputType == OperationReviewOutputProposedOperation {
		result["proposedOperation"] = action.ProposedOperation
	}
	return result
}

func shouldCreatePortfolioSentinelRiskHit(report PortfolioSentinelReport) bool {
	risk := normalizePortfolioSentinelRiskLevel(report.OverallRiskLevel)
	return risk == PortfolioSentinelRiskHigh || risk == PortfolioSentinelRiskCritical
}

func portfolioSentinelScopeSummary(run PortfolioSentinelRun) string {
	if run.PortfolioID != "" {
		return fmt.Sprintf("portfolio=%s window=%s", run.PortfolioID, run.WindowType)
	}
	return "all portfolios window=" + run.WindowType
}

func portfolioSentinelMarketSession(windowType string) string {
	switch windowType {
	case PortfolioSentinelWindowPreMarket:
		return "pre_market"
	case PortfolioSentinelWindowMidday:
		return "midday_break"
	case PortfolioSentinelWindowPostClose:
		return "post_close"
	default:
		return "closed_or_unknown"
	}
}

func portfolioSentinelDataCounts(ctx PortfolioSentinelContext) (quotes, daily, minute int) {
	for _, p := range ctx.Portfolios {
		for _, h := range p.Holdings {
			if h.Quote != nil {
				quotes++
			}
			if h.DailyBars != nil && h.DailyBars.Count > 0 {
				daily++
			}
			if h.MinuteBars != nil && h.MinuteBars.Count > 0 {
				minute++
			}
		}
	}
	return quotes, daily, minute
}

func portfolioSentinelContextStats(ctx PortfolioSentinelContext) map[string]any {
	holdings := 0
	for _, p := range ctx.Portfolios {
		holdings += len(p.Holdings)
	}
	quotes, daily, minute := portfolioSentinelDataCounts(ctx)
	return map[string]any{
		"portfolioCount":   len(ctx.Portfolios),
		"holdingCount":     holdings,
		"newsEventCount":   len(ctx.NewsEvents),
		"rawNewsCount":     len(ctx.RawNews),
		"quoteCount":       quotes,
		"dailyBarSymbols":  daily,
		"minuteBarSymbols": minute,
	}
}

func (s *Service) failPortfolioSentinelRun(ctx context.Context, run PortfolioSentinelRun, err error) (PortfolioSentinelRun, error) {
	run = markPortfolioSentinelRunFailed(run, err)
	updated, updateErr := s.store.UpdatePortfolioSentinelRun(ctx, run)
	if updateErr != nil {
		return updated, updateErr
	}
	return updated, err
}

func markPortfolioSentinelRunFailed(run PortfolioSentinelRun, err error) PortfolioSentinelRun {
	run.Status = PortfolioSentinelStatusFailed
	run.FinishedAt = time.Now()
	if err != nil {
		run.ErrorMessage = safelog.Text(err.Error(), 500)
	}
	return run
}

func (s *Service) markPortfolioSentinelAgentRunFailed(ctx context.Context, run AgentRun, message string) {
	if run.TaskType != AgentTaskTypePortfolioSentinel || run.TriggerObjectType != "portfolio_sentinel_run" || strings.TrimSpace(run.TriggerObjectID) == "" {
		return
	}
	sentinelRun, err := s.store.GetPortfolioSentinelRun(ctx, run.TriggerObjectID)
	if err != nil {
		return
	}
	sentinelRun.Status = PortfolioSentinelStatusFailed
	sentinelRun.ErrorMessage = safelog.Text(message, 500)
	sentinelRun.FinishedAt = time.Now()
	_, _ = s.store.UpdatePortfolioSentinelRun(ctx, sentinelRun)
}

func (s *Service) runPortfolioSentinelScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.tickPortfolioSentinelScheduler(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.tickPortfolioSentinelScheduler(ctx, now)
		}
	}
}

func (s *Service) tickPortfolioSentinelScheduler(ctx context.Context, now time.Time) {
	cfg, err := s.GetPortfolioSentinelConfig(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("portfolio sentinel config load failed", "error", safelog.Text(err.Error(), 240))
		}
		return
	}
	if !cfg.Enabled {
		return
	}
	windowType, slotStart, ok := portfolioSentinelScheduledWindow(now)
	if !ok || !portfolioSentinelWindowEnabled(cfg, windowType) {
		return
	}
	if s.portfolioSentinelSlotAlreadyRan(ctx, windowType, slotStart) {
		return
	}
	if _, err := s.RunScheduledPortfolioSentinel(ctx, windowType, now); err != nil &&
		!errors.Is(err, ErrPortfolioSentinelAlreadyRunning) &&
		!errors.Is(err, ErrPortfolioSentinelScheduledDisabled) && s.log != nil {
		s.log.Warn("scheduled portfolio sentinel failed", "window_type", windowType, "slot_start", slotStart.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 240))
	}
}

func (s *Service) portfolioSentinelSlotAlreadyRan(ctx context.Context, windowType string, slotStart time.Time) bool {
	items, err := s.store.ListPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{
		WindowType: windowType,
		Limit:      10,
	})
	if err != nil {
		return false
	}
	slotEnd := slotStart.Add(24 * time.Hour)
	for _, item := range items {
		if item.TriggerType != PortfolioSentinelTriggerScheduled {
			continue
		}
		if !item.StartedAt.Before(slotStart) && item.StartedAt.Before(slotEnd) &&
			(item.Status == PortfolioSentinelStatusRunning || item.Status == PortfolioSentinelStatusCompleted) {
			return true
		}
	}
	return false
}

func portfolioSentinelScheduledWindow(now time.Time) (string, time.Time, bool) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	n := now.In(loc)
	candidates := []struct {
		window string
		hour   int
		minute int
	}{
		{PortfolioSentinelWindowPreMarket, 8, 40},
		{PortfolioSentinelWindowMidday, 12, 15},
		{PortfolioSentinelWindowPostClose, 21, 0},
	}
	for _, c := range candidates {
		slot := time.Date(n.Year(), n.Month(), n.Day(), c.hour, c.minute, 0, 0, loc)
		if !n.Before(slot) && n.Before(slot.Add(10*time.Minute)) {
			return c.window, slot, true
		}
	}
	return "", time.Time{}, false
}
