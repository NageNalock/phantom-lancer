package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	// ponytail: one latest judgment per current holding is enough for continuity.
	// Scan 50 completed runs to tolerate portfolio-scoped runs without copying an
	// unbounded history; add a keyed repository query if real usage exceeds this.
	portfolioSentinelPriorRunScanLimit = 50
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
	newsContextRunIDs := []string(nil)
	if id := strings.TrimSpace(req.NewsContextRunID); id != "" {
		newsContextRunIDs = []string{id}
	}
	return s.startPortfolioSentinelRunForNewsContexts(ctx, PortfolioSentinelTriggerManual, windowType, strings.TrimSpace(req.PortfolioID), startAt, endAt, strings.TrimSpace(req.Note), newsContextRunIDs, 0, true)
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
	return s.startPortfolioSentinelRunForNewsContexts(ctx, triggerType, windowType, portfolioID, startAt, endAt, note, nil, 0, async)
}

func (s *Service) startPortfolioSentinelRunForNewsContext(ctx context.Context, triggerType, windowType, portfolioID string, startAt, endAt time.Time, note, newsContextRunID string, async bool) (PortfolioSentinelRun, error) {
	return s.startPortfolioSentinelRunForNewsContexts(ctx, triggerType, windowType, portfolioID, startAt, endAt, note, []string{newsContextRunID}, 0, async)
}

func (s *Service) startPortfolioSentinelRunForNewsContexts(ctx context.Context, triggerType, windowType, portfolioID string, startAt, endAt time.Time, note string, newsContextRunIDs []string, reviewRetryCount int, async bool) (PortfolioSentinelRun, error) {
	newsContextRunIDs = uniqueNonEmptyStrings(newsContextRunIDs)
	if !validPortfolioSentinelWindowType(windowType) || startAt.IsZero() || endAt.IsZero() || !endAt.After(startAt) {
		return PortfolioSentinelRun{}, ErrInvalidPortfolioSentinelInput
	}
	busyPortfolioID, busyWindowType := portfolioID, windowType
	if len(newsContextRunIDs) > 0 {
		// A news-context review covers every current portfolio and therefore waits
		// for any sentinel run, not merely another manual-window run.
		busyPortfolioID, busyWindowType = "", ""
	}
	if running, err := s.store.HasRunningPortfolioSentinelRun(ctx, busyPortfolioID, busyWindowType); err != nil {
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
	if len(newsContextRunIDs) > 0 {
		if _, err := s.store.BeginNewsContextReviews(ctx, newsContextRunIDs, run.ID, reviewRetryCount); err != nil {
			return s.failPortfolioSentinelRun(ctx, run, err)
		}
		if _, err := s.store.FreezePortfolioSentinelImpactReviewScope(ctx, run.ID); err != nil {
			return s.failPortfolioSentinelRun(ctx, run, err)
		}
	}

	if err := s.preparePortfolioSentinelNews(ctx, run); err != nil && s.log != nil {
		s.log.Warn("portfolio sentinel news refresh skipped", "run_id", run.ID, "error", safelog.Text(err.Error(), 240))
	}
	contextPack, err := s.buildPortfolioSentinelContext(ctx, run, note, len(newsContextRunIDs) > 0)
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
	return s.buildPortfolioSentinelContext(ctx, run, note, false)
}

func (s *Service) buildPortfolioSentinelContext(ctx context.Context, run PortfolioSentinelRun, note string, newsContextReview bool) (PortfolioSentinelContext, error) {
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
	if newsContextReview {
		contextRuns, err := s.store.ListNewsContextRunsByReviewRunID(ctx, run.ID)
		if err != nil {
			return PortfolioSentinelContext{}, err
		}
		if len(contextRuns) == 0 {
			return PortfolioSentinelContext{}, ErrInvalidNewsContextInput
		}
		contextRunIDs := make([]string, 0, len(contextRuns))
		windowStart := contextRuns[0].WindowStart
		windowEnd := contextRuns[0].WindowEnd
		for _, contextRun := range contextRuns {
			contextRunIDs = append(contextRunIDs, contextRun.ID)
			if contextRun.WindowStart.Before(windowStart) {
				windowStart = contextRun.WindowStart
			}
			if contextRun.WindowEnd.After(windowEnd) {
				windowEnd = contextRun.WindowEnd
			}
		}
		changedCount, materialChangeCount, err := s.store.NewsContextChangedThreadCountsForRuns(ctx, contextRunIDs)
		if err != nil {
			return PortfolioSentinelContext{}, err
		}
		impactScope, err := s.store.GetPortfolioSentinelImpactReviewScopeSummary(ctx, run.ID)
		if err != nil {
			return PortfolioSentinelContext{}, err
		}
		out.NewsContext = &PortfolioSentinelNewsContext{
			RunID:                    run.ID,
			CoveredRunCount:          len(contextRuns),
			WindowStart:              windowStart,
			WindowEnd:                windowEnd,
			ChangedThreadCount:       changedCount,
			MaterialChangeCount:      materialChangeCount,
			RequiredMCPTool:          mcpToolListNewsContextChanges,
			ImpactReviewScope:        impactScope,
			ImpactReviewRequiredTool: mcpToolListPortfolioSentinelImpactReviewScope,
		}
	}
	selectedEvents := make([]NewsEvent, 0, cfg.MaxNewsItems)
	selectedEventIDs := make(map[string]struct{})
	newsLinkCount := 0
	newsTruncated := false
	newsFilterStats := portfolioSentinelNewsFilterStats{
		SuppressedLowPriorityTerms:   map[string]int{},
		SuppressedLowPrioritySymbols: map[string]int{},
	}

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
			// ponytail: the scheduled 30-second quote task remains the primary
			// feed; one holding-only refresh here closes the race between its last
			// poll and an intraday sentinel run without creating another job.
			if s.httpClient != nil {
				if refresh, refreshErr := s.RefreshLatestQuotes(ctx, symbols, "portfolio_sentinel"); refreshErr == nil {
					if valuationErr := s.RefreshPortfoliosFromLatestQuotes(ctx, refresh.Items); valuationErr == nil {
						if refreshedHoldings, reloadErr := s.store.ListHoldings(ctx, portfolio.ID); reloadErr == nil {
							holdings = refreshedHoldings
						}
					}
				}
			}
			quotes, err := s.store.GetLatestQuotes(ctx, symbols)
			if err != nil {
				return PortfolioSentinelContext{}, err
			}
			for _, q := range quotes {
				quoteBySymbol[q.Symbol] = q
			}
		}
		for _, holding := range holdings {
			holdingNews, holdingLinks, truncated, filterStats, err := s.portfolioSentinelHoldingNews(ctx, holding, run, cfg, selectedEventIDs, &selectedEvents)
			if err != nil {
				return PortfolioSentinelContext{}, err
			}
			newsFilterStats.merge(filterStats)
			newsLinkCount += len(holdingLinks)
			if truncated {
				newsTruncated = true
			}
			hctx := PortfolioSentinelHoldingContext{
				Holding:   holding,
				Freshness: map[string]any{},
				News:      holdingNews,
				NewsLinks: holdingLinks,
			}
			if quote, ok := quoteBySymbol[holding.Symbol]; ok {
				hctx.Quote = &quote
				hctx.Freshness["quote"] = quoteFreshnessSummary(quote)
			} else {
				hctx.Freshness["quote"] = map[string]any{"status": "missing"}
			}
			hctx.DailyBars = s.buildDailyBarsContext(ctx, holding.Symbol)
			hctx.Freshness["dailyBars"] = dailyBarsFreshnessSummary(hctx.DailyBars)
			hctx.MinuteBars = s.buildMinuteBarsContext(ctx, holding.Symbol)
			hctx.Freshness["minuteBars"] = minuteBarsFreshnessSummary(hctx.MinuteBars)
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
	out.PriorJudgments, err = s.portfolioSentinelPriorHoldingJudgments(ctx, run.ID, out.Portfolios)
	if err != nil {
		return PortfolioSentinelContext{}, err
	}
	out.Candidates, err = s.portfolioSentinelTrustedCandidates(ctx, allSymbols)
	if err != nil {
		return PortfolioSentinelContext{}, err
	}
	out.Themes, err = s.portfolioSentinelActiveThemes(ctx, allSymbols, out.Candidates)
	if err != nil {
		return PortfolioSentinelContext{}, err
	}
	if err := s.fillPortfolioSentinelDecisionGates(ctx, &out); err != nil {
		return PortfolioSentinelContext{}, err
	}
	if out.NewsContext != nil {
		// The aggregation already persisted compact themes and evidence. Re-copying
		// raw news bodies into the review ledger would defeat later source cleanup;
		// the agent must page through the complete changed-theme manifest via MCP.
		out.NewsEvents = nil
		out.RawNews = nil
		for portfolioIndex := range out.Portfolios {
			for holdingIndex := range out.Portfolios[portfolioIndex].Holdings {
				out.Portfolios[portfolioIndex].Holdings[holdingIndex].News = nil
				out.Portfolios[portfolioIndex].Holdings[holdingIndex].NewsLinks = nil
				out.Portfolios[portfolioIndex].Holdings[holdingIndex].RawNews = nil
			}
		}
	}
	out.ContextStats = portfolioSentinelContextStats(out)
	out.ContextStats["newsLinkCount"] = newsLinkCount
	out.ContextStats["newsTruncated"] = newsTruncated
	portfolioSentinelApplyNewsFilterStats(out.ContextStats, newsFilterStats)
	return out, nil
}

func (s *Service) portfolioSentinelPriorHoldingJudgments(
	ctx context.Context,
	currentRunID string,
	portfolios []PortfolioSentinelPortfolioContext,
) ([]PortfolioSentinelPriorJudgment, error) {
	targets := make(map[string]StockV2Holding)
	for _, portfolio := range portfolios {
		for _, holding := range portfolio.Holdings {
			key := portfolioSentinelPriorJudgmentKey(portfolio.Portfolio.ID, holding.Holding.Symbol)
			if key != "" {
				targets[key] = holding.Holding
			}
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	runs, err := s.store.ListPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{
		Status: PortfolioSentinelStatusCompleted,
		Limit:  portfolioSentinelPriorRunScanLimit,
	})
	if err != nil {
		return nil, err
	}
	latest := make(map[string]PortfolioSentinelPriorJudgment, len(targets))
	for _, run := range runs {
		if run.ID == currentRunID {
			continue
		}
		result, err := s.store.GetPortfolioSentinelResultByRunID(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		if result == nil {
			continue
		}
		report, err := portfolioSentinelReportFromResult(result.RawResult)
		if err != nil || !portfolioSentinelActionPlanSchema(report.SchemaVersion) {
			continue
		}
		affected := make(map[string]PortfolioSentinelAffectedHolding, len(report.AffectedHoldings))
		for _, item := range report.AffectedHoldings {
			affected[portfolioSentinelPriorJudgmentSymbolKey(item.Symbol, item.Market)] = item
			if strings.TrimSpace(item.Market) != "" {
				affected[portfolioSentinelPriorJudgmentSymbolKey(item.Symbol, "")] = item
			}
		}
		for _, plan := range report.ActionPlans {
			key := portfolioSentinelPriorJudgmentKey(plan.PortfolioID, plan.Symbol)
			holding, wanted := targets[key]
			if !wanted {
				continue
			}
			if existing, ok := latest[key]; ok && !run.FinishedAt.After(existing.SourceFinishedAt) {
				continue
			}
			item := PortfolioSentinelPriorJudgment{
				PortfolioID:       plan.PortfolioID,
				Symbol:            plan.Symbol,
				Market:            firstNonEmpty(plan.Market, holding.Market),
				Name:              firstNonEmpty(plan.Name, holding.Name),
				Action:            plan.Action,
				TriggerMode:       plan.TriggerMode,
				TriggerPolicy:     plan.TriggerPolicy,
				Conditions:        append([]PortfolioSentinelPlanCondition(nil), plan.Conditions...),
				Reason:            safelog.Text(strings.TrimSpace(plan.Reason), 1000),
				RiskNotes:         safelog.Text(strings.TrimSpace(plan.RiskNotes), 1000),
				Confidence:        plan.Confidence,
				SourceRunID:       run.ID,
				SourceFinishedAt:  run.FinishedAt,
				SourceWindowEndAt: run.WindowEndAt,
				ValidUntil:        plan.ValidUntil,
				AdvisoryOnly:      true,
				EvidenceClass:     "derived_state",
			}
			if plan.Sizing != nil {
				sizing := *plan.Sizing
				item.Sizing = &sizing
			}
			hit, ok := affected[portfolioSentinelPriorJudgmentSymbolKey(plan.Symbol, plan.Market)]
			if !ok {
				hit = affected[portfolioSentinelPriorJudgmentSymbolKey(plan.Symbol, "")]
			}
			if hit.Symbol != "" {
				item.RiskLevel = hit.RiskLevel
				for _, reason := range uniqueNonEmptyStrings(hit.Reasons) {
					if len(item.AffectedReasons) >= 8 {
						break
					}
					item.AffectedReasons = append(item.AffectedReasons, safelog.Text(reason, 500))
				}
			}
			latest[key] = item
		}
	}
	out := make([]PortfolioSentinelPriorJudgment, 0, len(latest))
	for _, item := range latest {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PortfolioID == out[j].PortfolioID {
			return strings.ToUpper(out[i].Symbol) < strings.ToUpper(out[j].Symbol)
		}
		return out[i].PortfolioID < out[j].PortfolioID
	})
	return out, nil
}

func portfolioSentinelPriorJudgmentKey(portfolioID, symbol string) string {
	portfolioID = strings.TrimSpace(portfolioID)
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if portfolioID == "" || symbol == "" {
		return ""
	}
	return portfolioID + "\x00" + symbol
}

func portfolioSentinelPriorJudgmentSymbolKey(symbol, market string) string {
	return strings.ToUpper(strings.TrimSpace(symbol)) + "\x00" + strings.ToUpper(strings.TrimSpace(market))
}

func (s *Service) portfolioSentinelTrustedCandidates(ctx context.Context, holdingSymbols []string) ([]PortfolioSentinelCandidateContext, error) {
	held := make(map[string]struct{}, len(holdingSymbols))
	for _, symbol := range holdingSymbols {
		held[strings.ToUpper(strings.TrimSpace(symbol))] = struct{}{}
	}
	type candidateState struct {
		item    PortfolioSentinelCandidateContext
		sources map[string]struct{}
	}
	bySymbol := map[string]*candidateState{}
	add := func(symbol, market, name, rationale, source string) {
		key := strings.ToUpper(strings.TrimSpace(symbol))
		if key == "" {
			return
		}
		if _, exists := held[key]; exists {
			return
		}
		state := bySymbol[key]
		if state == nil {
			state = &candidateState{
				item: PortfolioSentinelCandidateContext{
					Symbol:    strings.TrimSpace(symbol),
					Market:    strings.TrimSpace(market),
					Name:      strings.TrimSpace(name),
					Rationale: safelog.Text(strings.TrimSpace(rationale), 500),
				},
				sources: map[string]struct{}{},
			}
			bySymbol[key] = state
		}
		if source != "" {
			state.sources[source] = struct{}{}
		}
	}
	for _, status := range []string{
		OpportunityCandidateStatusShortlisted,
		OpportunityCandidateStatusStrategyRequested,
		OpportunityCandidateStatusStrategyGenerated,
	} {
		items, err := s.store.ListOpportunityCandidates(ctx, OpportunityCandidateListFilter{Status: status, Limit: 100})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			add(item.Symbol, item.Market, item.Name, item.Reason, "opportunity:"+status)
		}
	}
	strategies, err := s.store.ListStrategies(ctx, StrategyListFilter{Status: StrategyStatusActive, Limit: 200})
	if err != nil {
		return nil, err
	}
	for _, item := range strategies {
		if item.Strategy.Kind != StrategyKindSymbolStrategy {
			continue
		}
		rationale := ""
		if item.ActiveVersion != nil {
			rationale = item.ActiveVersion.Thesis
		}
		add(item.Strategy.Symbol, item.Strategy.Market, item.Strategy.Name, rationale, "active_strategy")
	}
	keys := make([]string, 0, len(bySymbol))
	for key := range bySymbol {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 50 {
		keys = keys[:50]
	}
	out := make([]PortfolioSentinelCandidateContext, 0, len(keys))
	for _, key := range keys {
		state := bySymbol[key]
		for source := range state.sources {
			state.item.Sources = append(state.item.Sources, source)
		}
		sort.Strings(state.item.Sources)
		out = append(out, state.item)
	}
	return out, nil
}

func (s *Service) portfolioSentinelActiveThemes(
	ctx context.Context,
	holdingSymbols []string,
	candidates []PortfolioSentinelCandidateContext,
) ([]PortfolioSentinelThemeContext, error) {
	relevant := map[string]struct{}{}
	for _, symbol := range holdingSymbols {
		relevant[strings.ToUpper(strings.TrimSpace(symbol))] = struct{}{}
	}
	for _, candidate := range candidates {
		relevant[strings.ToUpper(strings.TrimSpace(candidate.Symbol))] = struct{}{}
	}
	threads, err := s.store.ListNewsThreads(ctx, NewsThreadListFilter{Status: NewsThreadStatusActive, Limit: 100})
	if err != nil {
		return nil, err
	}
	out := make([]PortfolioSentinelThemeContext, 0, len(threads))
	for _, thread := range threads {
		overlaps := len(thread.Symbols) == 0
		for _, symbol := range thread.Symbols {
			if _, ok := relevant[strings.ToUpper(strings.TrimSpace(symbol))]; ok {
				overlaps = true
				break
			}
		}
		if !overlaps {
			continue
		}
		out = append(out, PortfolioSentinelThemeContext{
			ID:             thread.ID,
			Title:          thread.Title,
			Stage:          thread.Stage,
			Symbols:        thread.Symbols,
			LatestChange:   thread.LatestChange,
			MaterialChange: strings.TrimSpace(thread.LatestChange) != "",
		})
		if len(out) >= 30 {
			break
		}
	}
	return out, nil
}

func (s *Service) preparePortfolioSentinelNews(ctx context.Context, run PortfolioSentinelRun) error {
	if s == nil || s.store == nil {
		return nil
	}
	// ponytail: a sentinel run can use already persisted links while the
	// owner-requested historical backfill has priority. Rebuilding the full
	// market link snapshot here would otherwise overlap the backfill's
	// historical semantic search and exceed this host's memory headroom.
	if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
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

func (s *Service) portfolioSentinelHoldingNews(
	ctx context.Context,
	holding StockV2Holding,
	run PortfolioSentinelRun,
	cfg PortfolioSentinelConfig,
	selectedEventIDs map[string]struct{},
	selectedEvents *[]NewsEvent,
) ([]NewsEvent, []NewsLinkCandidate, bool, portfolioSentinelNewsFilterStats, error) {
	perHoldingLimit := cfg.MaxNewsPerHolding
	if perHoldingLimit <= 0 {
		perHoldingLimit = 50
	}
	scanLimit := portfolioSentinelNewsScanLimit(perHoldingLimit)
	candidates, err := s.store.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{
		Symbol: holding.Symbol,
		Market: holding.Market,
		Since:  run.WindowStartAt,
		Until:  run.WindowEndAt,
		Limit:  scanLimit,
	})
	if err != nil {
		return nil, nil, false, portfolioSentinelNewsFilterStats{}, err
	}
	candidates, filterStats := portfolioSentinelSelectNewsCandidates(candidates, perHoldingLimit)
	events := make([]NewsEvent, 0, len(candidates))
	links := make([]NewsLinkCandidate, 0, len(candidates))
	holdingEventIDs := make(map[string]struct{})
	truncated := false
	for _, candidate := range candidates {
		if _, ok := holdingEventIDs[candidate.NewsEventID]; ok {
			continue
		}
		event, err := s.store.GetNewsEvent(ctx, candidate.NewsEventID)
		if err != nil {
			if errors.Is(err, ErrNewsEventNotFound) {
				continue
			}
			return nil, nil, false, filterStats, err
		}
		holdingEventIDs[candidate.NewsEventID] = struct{}{}
		events = append(events, event)
		links = append(links, candidate)
		if _, ok := selectedEventIDs[event.ID]; !ok {
			if cfg.MaxNewsItems > 0 && len(*selectedEvents) >= cfg.MaxNewsItems {
				truncated = true
				continue
			}
			selectedEventIDs[event.ID] = struct{}{}
			*selectedEvents = append(*selectedEvents, event)
		}
	}
	return events, links, truncated, filterStats, nil
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
	finalRun, finalLedger, output, execErr := s.executePortfolioSentinelAgentAttempt(ctx, run, ledger, pack, modelName)
	if finalRun.Status == AgentRunStatusCompleted ||
		!portfolioSentinelFallbackEligible(ctx, finalRun, output, execErr) {
		return finalRun, finalLedger, execErr
	}
	fallbackModel, ok := s.portfolioSentinelFallbackModel(ctx, finalRun.ModelID)
	if !ok {
		return finalRun, finalLedger, execErr
	}
	if output != nil {
		if err := ensureExecutorProcessGroupStopped(output.ProcessGroupID); err != nil {
			return finalRun, finalLedger, fmt.Errorf("clean failed portfolio sentinel process: %w", err)
		}
	}
	resolution, err := s.resolveFallbackAgentTask(
		ctx,
		AgentTaskTypePortfolioSentinel,
		"portfolio_sentinel_run",
		finalRun.TriggerObjectID,
		"system",
	)
	if err != nil {
		return finalRun, finalLedger, portfolioSentinelAttemptError(execErr, err)
	}
	if resolution.Run == nil || resolution.DecisionLedger == nil || resolution.ModelID != fallbackModel.ID {
		return finalRun, finalLedger, portfolioSentinelAttemptError(execErr, errors.New("no portfolio sentinel fallback agent run created"))
	}
	if err := s.linkPortfolioSentinelFallbackAttempt(
		ctx,
		finalRun.TriggerObjectID,
		finalRun,
		finalLedger,
		*resolution.Run,
		*resolution.DecisionLedger,
	); err != nil {
		s.finalizeAgentRun(ctx, resolution.Run.ID, nil, err)
		return finalRun, finalLedger, portfolioSentinelAttemptError(execErr, err)
	}
	if s.log != nil {
		s.log.Warn(
			"falling back portfolio sentinel agent after recoverable model failure",
			"sentinel_run_id", finalRun.TriggerObjectID,
			"primary_agent_run_id", finalRun.ID,
			"primary_model_id", finalRun.ModelID,
			"fallback_agent_run_id", resolution.Run.ID,
			"fallback_model_id", resolution.ModelID,
			"error", safelog.Text(firstNonEmpty(finalRun.ErrorMessage, portfolioSentinelErrorString(execErr)), 240),
		)
	}
	fallbackRun, fallbackLedger, _, fallbackErr := s.executePortfolioSentinelAgentAttempt(
		ctx,
		*resolution.Run,
		*resolution.DecisionLedger,
		pack,
		resolution.ModelName,
	)
	return fallbackRun, fallbackLedger, fallbackErr
}

func (s *Service) executePortfolioSentinelAgentAttempt(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack PortfolioSentinelContext, modelName string) (AgentRun, AgentDecisionLedger, *AgentExecutorOutput, error) {
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
		return finalRun, finalLedger, nil, ErrAgentExecutorUnavailable
	}
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update portfolio sentinel run to running failed", "run_id", run.ID, "error", safelog.Text(err.Error(), 240))
	}
	// ponytail: keep the MCP submission slot alive slightly longer than the CLI
	// process so a result produced at the execution deadline cannot race expiry.
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", portfolioSentinelExecTimeout+30*time.Second)
	execOutput, execErr := s.agentExecutor.ExecutePortfolioSentinel(ctx, taskID, pack, modelName, run.ReasoningEffort)
	submitted := s.consumeAgentTaskSubmittedResult(taskID)
	audit := AgentCLIResearchAudit{}
	if execOutput != nil {
		audit = execOutput.ResearchAudit
	}
	if submitted != nil {
		_, validationErr := s.validatePortfolioSentinelSubmittedReport(ctx, run.TriggerObjectID, *submitted, audit)
		if validationErr != nil {
			// ponytail: one bounded retry feeds the exact server validation failure
			// back to the agent. More retries would turn a deterministic contract
			// repair into an unbounded model loop.
			pack.Note = strings.TrimSpace(pack.Note + "\nCORRECTIVE RETRY: the previous submission failed server validation: " + safelog.Text(validationErr.Error(), 500) + ". Correct the complete result and submit it once.")
			taskID, _ = s.agentTaskPool.createTask(run.TaskType, run.ID, "", portfolioSentinelExecTimeout+30*time.Second)
			retryOutput, retryErr := s.agentExecutor.ExecutePortfolioSentinel(ctx, taskID, pack, modelName, run.ReasoningEffort)
			execOutput = mergeAgentExecutorOutputs(execOutput, retryOutput)
			execErr = retryErr
			submitted = s.consumeAgentTaskSubmittedResult(taskID)
		}
	}
	s.restoreAgentTaskSubmittedResult(taskID, run.TaskType, run.ID, submitted)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execOutput, execErr
}

func (s *Service) portfolioSentinelFallbackModel(ctx context.Context, failedModelID string) (AgentModelProfile, bool) {
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypePortfolioSentinel)
	if err != nil || strings.TrimSpace(taskProfile.FallbackModelID) == "" {
		return AgentModelProfile{}, false
	}
	taskProfile.PrimaryModelID = ""
	model, err := s.resolveModel(ctx, taskProfile)
	return model, err == nil && model.ID != failedModelID
}

func portfolioSentinelFallbackEligible(
	ctx context.Context,
	run AgentRun,
	output *AgentExecutorOutput,
	execErr error,
) bool {
	if ctx.Err() != nil || run.Status != AgentRunStatusFailed {
		return false
	}
	if errors.Is(execErr, ErrAgentExecutorUnavailable) ||
		errors.Is(execErr, ErrAgentTaskRequiresCLI) ||
		errors.Is(execErr, ErrAgentExecutionModeModelMismatch) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(run.ErrorMessage + " " + portfolioSentinelErrorString(execErr)))
	if strings.Contains(message, "save portfolio sentinel result failed") &&
		!strings.Contains(message, ErrInvalidPortfolioSentinelResult.Error()) {
		// ponytail: persistence and publication failures are not model failures.
		// Retrying them with another paid model cannot safely repair storage.
		return false
	}
	if output != nil {
		// The attempt reached a model process and still failed before publication.
		// Portfolio sentinel publication is atomic, so one bounded fallback is safe.
		return true
	}
	return agentProviderUsageLimitFailure(execErr, output) ||
		strings.Contains(message, "no valid result submitted") ||
		strings.Contains(message, "without submitting") ||
		strings.Contains(message, "no result submitted") ||
		strings.Contains(message, ErrInvalidPortfolioSentinelResult.Error()) ||
		strings.Contains(message, "execution timed out") ||
		strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "api request failed") ||
		strings.Contains(message, "provider") ||
		strings.Contains(message, "codex")
}

func (s *Service) linkPortfolioSentinelFallbackAttempt(
	ctx context.Context,
	sentinelRunID string,
	primaryRun AgentRun,
	primaryLedger AgentDecisionLedger,
	fallbackRun AgentRun,
	fallbackLedger AgentDecisionLedger,
) error {
	result, err := s.store.GetPortfolioSentinelResultByRunID(ctx, sentinelRunID)
	if err != nil {
		return err
	}
	if result != nil {
		return errors.New("portfolio sentinel result already published")
	}
	sentinelRun, err := s.store.GetPortfolioSentinelRun(ctx, sentinelRunID)
	if err != nil {
		return err
	}
	if sentinelRun.Status == PortfolioSentinelStatusCompleted {
		return errors.New("portfolio sentinel run already completed")
	}
	primaryLedger.OutputArtifactSummary = safelog.Text(
		strings.TrimSpace(primaryLedger.OutputArtifactSummary)+
			"\nfallback_agent_run_id: "+fallbackRun.ID,
		16384,
	)
	if primaryLedger.RedactionSummary == nil {
		primaryLedger.RedactionSummary = map[string]any{}
	}
	if fallbackLedger.RedactionSummary == nil {
		fallbackLedger.RedactionSummary = map[string]any{}
	}
	primaryLedger.RedactionSummary["fallbackAgentRunId"] = fallbackRun.ID
	fallbackLedger.RedactionSummary["fallbackFromAgentRunId"] = primaryRun.ID
	if _, err := s.store.UpdateAgentDecisionLedger(ctx, primaryLedger); err != nil {
		return err
	}
	if _, err := s.store.UpdateAgentDecisionLedger(ctx, fallbackLedger); err != nil {
		return err
	}
	sentinelRun.AgentRunID = fallbackRun.ID
	sentinelRun.DecisionLedgerID = fallbackLedger.ID
	sentinelRun.Status = PortfolioSentinelStatusRunning
	sentinelRun.ErrorMessage = ""
	sentinelRun.FinishedAt = time.Time{}
	_, err = s.store.UpdatePortfolioSentinelRun(ctx, sentinelRun)
	return err
}

func portfolioSentinelAttemptError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

func portfolioSentinelErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) ProcessPortfolioSentinelSubmittedResult(
	ctx context.Context,
	runID string,
	submitted AgentTaskSubmittedResult,
	researchAudits ...AgentCLIResearchAudit,
) (PortfolioSentinelResult, error) {
	run, err := s.store.GetPortfolioSentinelRun(ctx, runID)
	if err != nil {
		return PortfolioSentinelResult{}, err
	}
	if existing, err := s.store.GetPortfolioSentinelResultByRunID(ctx, run.ID); err != nil {
		return PortfolioSentinelResult{}, err
	} else if existing != nil {
		return *existing, nil
	}
	audit := AgentCLIResearchAudit{}
	if len(researchAudits) > 0 {
		audit = researchAudits[0]
	}
	report, err := s.validatePortfolioSentinelSubmittedReport(ctx, run.ID, submitted, audit)
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}
	if report.SchemaVersion == PortfolioSentinelReportSchemaVersion &&
		(audit.LiveSearchEnabled || len(report.ActionPlans) > 0) {
		submitted.Result = portfolioSentinelReportMap(report)
	}
	publication, err := s.preparePortfolioSentinelPublication(ctx, run, report, PortfolioSentinelResult{
		RunID:         run.ID,
		SchemaVersion: report.SchemaVersion,
		Summary:       firstNonEmpty(report.RunSummary, submitted.ResultSummary),
		RiskLevel:     normalizePortfolioSentinelRiskLevel(report.OverallRiskLevel),
		RawResult:     submitted.Result,
		ContextSummary: map[string]any{
			"confidence":    submitted.Confidence,
			"researchAudit": audit,
		},
	})
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}
	result, err := s.store.publishPortfolioSentinelResult(ctx, publication)
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}
	return result, nil
}

func (s *Service) validatePortfolioSentinelSubmittedReport(
	ctx context.Context,
	runID string,
	submitted AgentTaskSubmittedResult,
	audit AgentCLIResearchAudit,
) (PortfolioSentinelReport, error) {
	run, err := s.store.GetPortfolioSentinelRun(ctx, runID)
	if err != nil {
		return PortfolioSentinelReport{}, err
	}
	report, err := portfolioSentinelReportFromResult(submitted.Result)
	if err != nil {
		return PortfolioSentinelReport{}, err
	}
	// Legacy v1 rows and pre-v2 in-process submissions have no CLI capability
	// audit. Every real v2 executor enables live search, which makes the stricter
	// schema and action-plan contract mandatory without rewriting old history.
	if audit.LiveSearchEnabled && report.SchemaVersion != PortfolioSentinelReportSchemaVersion {
		return PortfolioSentinelReport{}, fmt.Errorf("%w: new CLI runs must use %s", ErrInvalidPortfolioSentinelResult, PortfolioSentinelReportSchemaVersion)
	}
	if report.SchemaVersion == PortfolioSentinelReportSchemaVersion &&
		(audit.LiveSearchEnabled || len(report.ActionPlans) > 0) {
		if err := s.validatePortfolioSentinelActionPlans(ctx, run, report, audit); err != nil {
			return PortfolioSentinelReport{}, err
		}
		normalizePortfolioSentinelResearchLanguage(&report, audit)
	}
	contextRuns, err := s.store.ListNewsContextRunsByReviewRunID(ctx, run.ID)
	if err != nil {
		return PortfolioSentinelReport{}, err
	}
	if len(contextRuns) > 0 {
		if err := s.validatePortfolioSentinelNewsContextCoverage(ctx, run.ID, report.CheckedNewsThreadVersionIDs); err != nil {
			return PortfolioSentinelReport{}, err
		}
		if err := s.validatePortfolioSentinelImpactReviewCoverage(ctx, run.ID, report.ImpactReviewCoverage); err != nil {
			return PortfolioSentinelReport{}, err
		}
	}
	return report, nil
}

func mergeAgentExecutorOutputs(first, second *AgentExecutorOutput) *AgentExecutorOutput {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	second.RequestCount += first.RequestCount
	second.PromptTokens += first.PromptTokens
	second.OutputTokens += first.OutputTokens
	second.CachedTokens += first.CachedTokens
	second.CacheMissTokens += first.CacheMissTokens
	second.Duration += first.Duration
	second.ResearchAudit.LiveSearchEnabled = second.ResearchAudit.LiveSearchEnabled || first.ResearchAudit.LiveSearchEnabled
	second.ResearchAudit.WebSearchCount += first.ResearchAudit.WebSearchCount
	if second.ResearchAudit.MCPToolCalls == nil {
		second.ResearchAudit.MCPToolCalls = map[string]int{}
	}
	for name, count := range first.ResearchAudit.MCPToolCalls {
		second.ResearchAudit.MCPToolCalls[name] += count
	}
	if second.ResearchAudit.AgentToolCalls == nil {
		second.ResearchAudit.AgentToolCalls = map[string]int{}
	}
	for name, count := range first.ResearchAudit.AgentToolCalls {
		second.ResearchAudit.AgentToolCalls[name] += count
	}
	return second
}

func (s *Service) validatePortfolioSentinelNewsContextCoverage(ctx context.Context, reviewScopeID string, checkedVersionIDs []string) error {
	const pageSize = 200
	expected := map[string]struct{}{}
	for offset := 0; ; offset += pageSize {
		changes, _, err := s.ListNewsContextReviewChanges(ctx, reviewScopeID, pageSize, offset)
		if err != nil {
			return err
		}
		for _, change := range changes {
			if id := strings.TrimSpace(change.VersionID); id != "" {
				expected[id] = struct{}{}
			}
		}
		if len(changes) < pageSize {
			break
		}
	}
	checked := map[string]struct{}{}
	for _, id := range checkedVersionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, duplicate := checked[id]; duplicate {
			return fmt.Errorf("%w: duplicate checked news thread version", ErrInvalidPortfolioSentinelResult)
		}
		checked[id] = struct{}{}
	}
	if len(expected) != len(checked) {
		return fmt.Errorf("%w: checked %d of %d changed news thread versions", ErrInvalidPortfolioSentinelResult, len(checked), len(expected))
	}
	for id := range expected {
		if _, ok := checked[id]; !ok {
			return fmt.Errorf("%w: changed news thread version was not reviewed", ErrInvalidPortfolioSentinelResult)
		}
	}
	return nil
}

func (s *Service) validatePortfolioSentinelImpactReviewCoverage(ctx context.Context, sentinelRunID string, coverage *PortfolioSentinelImpactReviewCoverage) error {
	if coverage == nil || !coverage.hasAllExplicitFields() {
		return fmt.Errorf("%w: all five impact review coverage lists are required", ErrInvalidPortfolioSentinelResult)
	}
	checks := []struct {
		objectType string
		checkedIDs []string
	}{
		{portfolioSentinelImpactObjectHoldings, *coverage.HoldingIDs},
		{portfolioSentinelImpactObjectMonitors, *coverage.MonitorIDs},
		{portfolioSentinelImpactObjectAlerts, *coverage.AlertIDs},
		{portfolioSentinelImpactObjectOpportunities, *coverage.OpportunityIDs},
		{portfolioSentinelImpactObjectStrategies, *coverage.StrategyIDs},
	}
	for _, check := range checks {
		const pageSize = 200
		expectedIDs := make([]string, 0)
		for offset := 0; ; offset += pageSize {
			items, total, err := s.store.ListPortfolioSentinelImpactReviewScope(ctx, sentinelRunID, check.objectType, pageSize, offset)
			if err != nil {
				return err
			}
			expectedIDs = append(expectedIDs, items...)
			if len(expectedIDs) >= total {
				break
			}
		}
		if err := validatePortfolioSentinelExactIDSet(check.objectType, expectedIDs, check.checkedIDs); err != nil {
			return err
		}
	}
	return nil
}

func validatePortfolioSentinelExactIDSet(objectType string, expectedIDs, checkedIDs []string) error {
	expected := make(map[string]struct{}, len(expectedIDs))
	for _, rawID := range expectedIDs {
		id := strings.TrimSpace(rawID)
		if id != "" {
			expected[id] = struct{}{}
		}
	}
	checked := make(map[string]struct{}, len(checkedIDs))
	for _, rawID := range checkedIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return fmt.Errorf("%w: blank %s review identifier", ErrInvalidPortfolioSentinelResult, objectType)
		}
		if _, duplicate := checked[id]; duplicate {
			return fmt.Errorf("%w: duplicate %s review identifier", ErrInvalidPortfolioSentinelResult, objectType)
		}
		checked[id] = struct{}{}
	}
	if len(expected) != len(checked) {
		return fmt.Errorf("%w: checked %d of %d %s", ErrInvalidPortfolioSentinelResult, len(checked), len(expected), objectType)
	}
	for id := range expected {
		if _, ok := checked[id]; !ok {
			return fmt.Errorf("%w: %s object was not reviewed", ErrInvalidPortfolioSentinelResult, objectType)
		}
	}
	return nil
}

type portfolioSentinelPublication struct {
	run                       PortfolioSentinelRun
	result                    PortfolioSentinelResult
	monitorRun                MonitorRun
	items                     []portfolioSentinelPublicationItem
	planStrategies            []portfolioSentinelPlanStrategyPublication
	enableDataStrategyMonitor bool
}

type portfolioSentinelPublicationItem struct {
	hit         MonitorHit
	review      OperationReview
	alertConfig MonitorTaskConfig
}

type portfolioSentinelPlanStrategyPublication struct {
	strategy StockV2Strategy
	version  StockV2StrategyVersion
	create   bool
}

func (s *Service) preparePortfolioSentinelPublication(ctx context.Context, run PortfolioSentinelRun, report PortfolioSentinelReport, result PortfolioSentinelResult) (portfolioSentinelPublication, error) {
	now := time.Now()
	publication := portfolioSentinelPublication{
		run:    run,
		result: result,
		monitorRun: MonitorRun{
			ID:           generateID(),
			TaskType:     AgentTaskTypePortfolioSentinel,
			Status:       MonitorRunStatusCompleted,
			TriggerType:  run.TriggerType,
			StartedAt:    run.StartedAt,
			FinishedAt:   now,
			ScopeSummary: portfolioSentinelScopeSummary(run),
			ScannedCount: run.ScannedHoldingCount,
			Metadata: map[string]any{
				"portfolioSentinelRunId": run.ID,
				"riskLevel":              report.OverallRiskLevel,
			},
			CreatedAt: now,
		},
	}
	var err error
	publication.planStrategies, err = s.preparePortfolioSentinelPlanStrategies(ctx, run, report)
	if err != nil {
		return portfolioSentinelPublication{}, err
	}
	for _, plan := range report.ActionPlans {
		if plan.Action != PortfolioSentinelPlanHold && plan.TriggerMode == PortfolioSentinelTriggerConditional {
			publication.enableDataStrategyMonitor = true
			break
		}
	}
	actions := report.PortfolioActions
	for _, plan := range report.ActionPlans {
		if plan.Action == PortfolioSentinelPlanHold || plan.TriggerMode != PortfolioSentinelTriggerImmediate {
			continue
		}
		actionMap := portfolioSentinelActionPlanMap(plan)
		operation, operationErr := s.portfolioSentinelTriggeredOperation(ctx, MonitorHit{
			PortfolioID: plan.PortfolioID,
			Symbol:      plan.Symbol,
			Market:      plan.Market,
		}, actionMap)
		if operationErr != nil {
			return portfolioSentinelPublication{}, operationErr
		}
		actions = append(actions, PortfolioSentinelAction{
			Symbol:            plan.Symbol,
			Market:            plan.Market,
			PortfolioID:       plan.PortfolioID,
			OutputType:        OperationReviewOutputProposedOperation,
			ResultSummary:     plan.Reason,
			ProposedOperation: portfolioSentinelProposedOperationMap(operation),
			Reason:            plan.Reason,
			RiskNotes:         plan.RiskNotes,
			Confidence:        plan.Confidence,
		})
	}
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
		hit := MonitorHit{
			ID:          generateID(),
			RunID:       publication.monitorRun.ID,
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
				"portfolioSentinelRawResult": result.RawResult,
				"riskLevel":                  report.OverallRiskLevel,
				"action":                     action,
			},
			CreatedAt: now,
		}
		pack, err := s.BuildAgentContextPack(ctx, hit)
		if err != nil {
			return portfolioSentinelPublication{}, err
		}
		strategyID, portfolioID, symbol, market := reviewLinkage(hit, pack)
		reviewResult := portfolioSentinelActionReviewResult(action, outputType)
		review := OperationReview{
			ID:            generateID(),
			HitID:         hit.ID,
			RunID:         hit.RunID,
			Status:        OperationReviewStatusCompleted,
			OutputType:    outputType,
			StrategyID:    strategyID,
			PortfolioID:   portfolioID,
			Symbol:        symbol,
			Market:        market,
			InputContext:  pack,
			ResultSummary: safelog.Text(firstNonEmpty(action.ResultSummary, report.RunSummary), 800),
			CreatedAt:     now,
			UpdatedAt:     now,
			CompletedAt:   now,
		}
		if outputType == OperationReviewOutputProposedOperation {
			reviewResult, err = s.applyProposedOperationGuardrails(ctx, review, reviewResult)
			if err != nil {
				return portfolioSentinelPublication{}, err
			}
		}
		review.Result = reviewResult
		item := portfolioSentinelPublicationItem{hit: hit, review: review}
		if operationReviewOutputTriggersAlert(outputType) {
			item.alertConfig, err = s.monitorAlertTaskConfig(ctx, hit.TaskType)
			if err != nil {
				return portfolioSentinelPublication{}, err
			}
		}
		publication.items = append(publication.items, item)
	}
	publication.monitorRun.HitCount = len(publication.items)
	publication.monitorRun.ReviewCount = len(publication.items)
	return publication, nil
}

func portfolioSentinelActionPlanMap(plan PortfolioSentinelActionPlan) map[string]any {
	raw, _ := json.Marshal(plan)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func portfolioSentinelProposedOperationMap(operation ProposedOperation) map[string]any {
	raw, _ := json.Marshal(operation)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func portfolioSentinelReportMap(report PortfolioSentinelReport) map[string]any {
	raw, err := json.Marshal(report)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Service) preparePortfolioSentinelPlanStrategies(
	ctx context.Context,
	run PortfolioSentinelRun,
	report PortfolioSentinelReport,
) ([]portfolioSentinelPlanStrategyPublication, error) {
	byPortfolio := map[string][]PortfolioSentinelActionPlan{}
	for _, plan := range report.ActionPlans {
		byPortfolio[plan.PortfolioID] = append(byPortfolio[plan.PortfolioID], plan)
	}
	if run.PortfolioID != "" {
		if _, exists := byPortfolio[run.PortfolioID]; !exists {
			byPortfolio[run.PortfolioID] = nil
		}
	} else {
		portfolios, err := s.store.ListPortfolios(ctx)
		if err != nil {
			return nil, err
		}
		for _, portfolio := range portfolios {
			if _, exists := byPortfolio[portfolio.ID]; !exists {
				byPortfolio[portfolio.ID] = nil
			}
		}
	}
	portfolioIDs := make([]string, 0, len(byPortfolio))
	for portfolioID := range byPortfolio {
		portfolioIDs = append(portfolioIDs, portfolioID)
	}
	sort.Strings(portfolioIDs)
	out := make([]portfolioSentinelPlanStrategyPublication, 0, len(portfolioIDs))
	now := time.Now()
	for _, portfolioID := range portfolioIDs {
		existing, err := s.findPortfolioSentinelPlanStrategy(ctx, portfolioID)
		if err != nil {
			return nil, err
		}
		rules := make([]map[string]any, 0)
		evidenceRefs := make([]string, 0)
		for _, plan := range byPortfolio[portfolioID] {
			evidenceRefs = append(evidenceRefs, plan.EvidenceRefs...)
			if plan.Action == PortfolioSentinelPlanHold || plan.TriggerMode != PortfolioSentinelTriggerConditional {
				continue
			}
			prefilters := make([]map[string]any, 0, len(plan.Conditions))
			for _, condition := range plan.Conditions {
				ruleType := normalizeWatchRuleType(condition.Type)
				prefilter := map[string]any{
					"key":  condition.Key,
					"type": ruleType,
				}
				if plan.MonitorWindow != nil && !plan.MonitorWindow.StartsAt.IsZero() {
					prefilter["activeAfter"] = plan.MonitorWindow.StartsAt.Format(time.RFC3339)
					prefilter["observationMode"] = portfolioSentinelObservationMode(ruleType)
				}
				if condition.Threshold != nil {
					prefilter["threshold"] = *condition.Threshold
				}
				if condition.Low != 0 {
					prefilter["low"] = condition.Low
				}
				if condition.High != 0 {
					prefilter["high"] = condition.High
				}
				prefilters = append(prefilters, prefilter)
			}
			rules = append(rules, map[string]any{
				"id":                          plan.ID,
				"action":                      plan.Action,
				"title":                       firstNonEmpty(plan.Name, plan.Symbol),
				"symbol":                      plan.Symbol,
				"market":                      plan.Market,
				"portfolioId":                 plan.PortfolioID,
				"triggerPolicy":               plan.TriggerPolicy,
				"dataPrefilters":              prefilters,
				"sizing":                      plan.Sizing,
				"reason":                      plan.Reason,
				"riskNotes":                   plan.RiskNotes,
				"evidenceRefs":                plan.EvidenceRefs,
				"researchRefs":                plan.ResearchRefs,
				"monitorWindow":               plan.MonitorWindow,
				"validUntil":                  plan.ValidUntil,
				"portfolioSentinelActionPlan": "true",
				"portfolioSentinelRunId":      run.ID,
			})
		}
		strategy := StockV2Strategy{
			ID:          generateID(),
			Name:        "组合哨兵持仓操作计划",
			Kind:        StrategyKindPortfolioMonitor,
			Scope:       StrategyScopePortfolioBound,
			Source:      StrategySourceAgent,
			Status:      StrategyStatusActive,
			PortfolioID: portfolioID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		create := true
		versionNo := 1
		if existing != nil {
			strategy = existing.Strategy
			strategy.Status = StrategyStatusActive
			strategy.UpdatedAt = now
			create = false
			if existing.ActiveVersion != nil {
				versionNo = existing.ActiveVersion.VersionNo + 1
			}
		}
		version := StockV2StrategyVersion{
			ID:           generateID(),
			StrategyID:   strategy.ID,
			VersionNo:    versionNo,
			Title:        "组合哨兵操作计划",
			Direction:    StrategyDirectionWatch,
			Thesis:       report.RunSummary,
			RiskNotes:    strings.Join(report.DataQualityNotes, "；"),
			EvidenceRefs: uniqueNonEmptyStrings(evidenceRefs),
			GenerationMeta: map[string]any{
				"source":                 AgentTaskTypePortfolioSentinel,
				"template":               "portfolio_sentinel_action_plan_v2",
				"portfolioSentinelRunId": run.ID,
				"validUntil":             now.Add(portfolioSentinelPlanValidity),
				"playbook":               map[string]any{"rules": rules},
			},
			CreatedBy: AgentTaskTypePortfolioSentinel,
			CreatedAt: now,
		}
		strategy.ActiveVersionID = version.ID
		out = append(out, portfolioSentinelPlanStrategyPublication{strategy: strategy, version: version, create: create})
	}
	return out, nil
}

func portfolioSentinelObservationMode(ruleType string) string {
	switch ruleType {
	case WatchRulePriceAbove, WatchRulePriceBelow, WatchRulePriceBetween,
		WatchRulePctChangeAbove, WatchRulePctChangeBelow:
		return watchObservationFutureCross
	case WatchRuleDailyCloseAbove, WatchRuleDailyCloseBelow:
		return watchObservationFutureClose
	default:
		return watchObservationCurrentState
	}
}

func (s *Service) findPortfolioSentinelPlanStrategy(ctx context.Context, portfolioID string) (*StrategyWithVersion, error) {
	items, err := s.store.ListStrategies(ctx, StrategyListFilter{
		Kind:        StrategyKindPortfolioMonitor,
		Source:      StrategySourceAgent,
		PortfolioID: portfolioID,
		Limit:       100,
	})
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ActiveVersion == nil {
			continue
		}
		if stringFromAny(item.ActiveVersion.GenerationMeta["template"]) == "portfolio_sentinel_action_plan_v2" {
			return &item, nil
		}
	}
	return nil, nil
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
	agentRuns, err := s.store.ListAgentRuns(ctx, AgentRunListFilter{
		TaskType:          AgentTaskTypePortfolioSentinel,
		TriggerObjectType: "portfolio_sentinel_run",
		TriggerObjectID:   run.ID,
		Limit:             10,
	})
	if err != nil {
		return PortfolioSentinelRunDetail{}, err
	}
	// ListAgentRuns is newest-first; attempts read more naturally in execution order.
	for index := len(agentRuns) - 1; index >= 0; index-- {
		attempt := PortfolioSentinelAgentAttempt{Run: agentRuns[index]}
		if ledgerID := strings.TrimSpace(agentRuns[index].DecisionLedgerID); ledgerID != "" {
			ledger, ledgerErr := s.store.GetAgentDecisionLedger(ctx, ledgerID)
			if ledgerErr != nil && !errors.Is(ledgerErr, ErrAgentDecisionLedgerNotFound) {
				return PortfolioSentinelRunDetail{}, ledgerErr
			}
			if ledgerErr == nil {
				attempt.Ledger = &ledger
			}
		}
		detail.AgentAttempts = append(detail.AgentAttempts, attempt)
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

func (s *Service) ListPortfolioSentinelActionPlans(
	ctx context.Context,
	filter PortfolioSentinelActionPlanListFilter,
) ([]PortfolioSentinelActionPlanView, error) {
	runs, err := s.store.ListPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{
		Status: PortfolioSentinelStatusCompleted,
		Limit:  200,
	})
	if err != nil {
		return nil, err
	}
	allPortfolioIDs := make([]string, 0)
	if portfolios, listErr := s.store.ListPortfolios(ctx); listErr != nil {
		return nil, listErr
	} else {
		for _, portfolio := range portfolios {
			allPortfolioIDs = append(allPortfolioIDs, portfolio.ID)
		}
	}
	seenPortfolio := map[string]struct{}{}
	out := make([]PortfolioSentinelActionPlanView, 0)
	now := time.Now()
	for _, run := range runs {
		result, err := s.store.GetPortfolioSentinelResultByRunID(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		if result == nil || !portfolioSentinelActionPlanSchema(result.SchemaVersion) {
			continue
		}
		report, err := portfolioSentinelReportFromResult(result.RawResult)
		if err != nil {
			continue
		}
		touched := map[string]struct{}{}
		if run.PortfolioID != "" {
			touched[run.PortfolioID] = struct{}{}
		} else {
			for _, portfolioID := range allPortfolioIDs {
				touched[portfolioID] = struct{}{}
			}
		}
		for _, plan := range report.ActionPlans {
			portfolioID := strings.TrimSpace(plan.PortfolioID)
			if portfolioID == "" {
				continue
			}
			if _, older := seenPortfolio[portfolioID]; older {
				continue
			}
			if filter.PortfolioID != "" && portfolioID != filter.PortfolioID {
				continue
			}
			if filter.Action != "" && plan.Action != filter.Action {
				continue
			}
			status := "active"
			if !plan.ValidUntil.IsZero() && !now.Before(plan.ValidUntil) {
				status = "expired"
			} else if plan.Action != PortfolioSentinelPlanHold && plan.TriggerMode == PortfolioSentinelTriggerImmediate {
				status = "proposed"
			} else if plan.Action != PortfolioSentinelPlanHold {
				if strategy, strategyErr := s.findPortfolioSentinelPlanStrategy(ctx, portfolioID); strategyErr != nil {
					return nil, strategyErr
				} else if strategy != nil {
					triggered, triggerErr := s.portfolioSentinelPlanAlreadyTriggered(ctx, strategy.Strategy.ID, map[string]any{
						"id":                     plan.ID,
						"portfolioSentinelRunId": run.ID,
					})
					if triggerErr != nil {
						return nil, triggerErr
					}
					if triggered {
						status = "triggered"
					}
				}
			}
			if status == "expired" && !filter.IncludeExpired {
				continue
			}
			currentAction := plan.Action
			contingencyAction := ""
			if status == "expired" || (plan.TriggerMode == PortfolioSentinelTriggerConditional && status == "active") {
				currentAction = PortfolioSentinelPlanHold
			}
			if plan.TriggerMode == PortfolioSentinelTriggerConditional && (status == "active" || status == "expired") {
				contingencyAction = plan.Action
			}
			var gate *DecisionGateSnapshot
			if snapshot, gateErr := s.store.GetLatestDecisionGateSnapshot(ctx, "portfolio_sentinel", run.ID, plan.Symbol); gateErr == nil {
				gate = &snapshot
			}
			out = append(out, PortfolioSentinelActionPlanView{
				Plan:              plan,
				RunID:             run.ID,
				ResultID:          result.ID,
				RunFinishedAt:     run.FinishedAt,
				Status:            status,
				CurrentAction:     currentAction,
				ContingencyAction: contingencyAction,
				DecisionGate:      gate,
			})
		}
		for portfolioID := range touched {
			seenPortfolio[portfolioID] = struct{}{}
		}
	}
	return out, nil
}

func portfolioSentinelReportFromResult(result map[string]any) (PortfolioSentinelReport, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return PortfolioSentinelReport{}, err
	}
	var report PortfolioSentinelReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return PortfolioSentinelReport{}, fmt.Errorf("%w: %v", ErrInvalidPortfolioSentinelResult, err)
	}
	if report.SchemaVersion != PortfolioSentinelReportSchemaVersion &&
		report.SchemaVersion != portfolioSentinelLegacySchemaVersionV2 &&
		report.SchemaVersion != portfolioSentinelLegacySchemaVersionV1 {
		return PortfolioSentinelReport{}, ErrInvalidPortfolioSentinelResult
	}
	report.OverallRiskLevel = normalizePortfolioSentinelRiskLevel(report.OverallRiskLevel)
	if strings.TrimSpace(report.RunSummary) == "" {
		return PortfolioSentinelReport{}, ErrInvalidPortfolioSentinelResult
	}
	return report, nil
}

func portfolioSentinelActionPlanSchema(version string) bool {
	return version == PortfolioSentinelReportSchemaVersion || version == portfolioSentinelLegacySchemaVersionV2
}

func (s *Service) validatePortfolioSentinelActionPlans(
	ctx context.Context,
	run PortfolioSentinelRun,
	report PortfolioSentinelReport,
	audit AgentCLIResearchAudit,
) error {
	held := map[string]StockV2Holding{}
	portfolioIDs := map[string]struct{}{}
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		return err
	}
	for _, portfolio := range portfolios {
		if run.PortfolioID != "" && portfolio.ID != run.PortfolioID {
			continue
		}
		portfolioIDs[portfolio.ID] = struct{}{}
		holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
		if err != nil {
			return err
		}
		for _, holding := range holdings {
			held[portfolio.ID+"\x00"+strings.ToUpper(strings.TrimSpace(holding.Symbol))] = holding
		}
	}
	candidates, err := s.portfolioSentinelTrustedCandidates(ctx, nil)
	if err != nil {
		return err
	}
	trusted := map[string]struct{}{}
	for _, candidate := range candidates {
		trusted[strings.ToUpper(strings.TrimSpace(candidate.Symbol))] = struct{}{}
	}
	portfolioOutlookIDs := make(map[string]struct{}, len(report.PortfolioOutlooks))
	for index := range report.PortfolioOutlooks {
		outlook := &report.PortfolioOutlooks[index]
		outlook.PortfolioID = strings.TrimSpace(outlook.PortfolioID)
		outlook.PortfolioName = safelog.Text(outlook.PortfolioName, 200)
		if _, ok := portfolioIDs[outlook.PortfolioID]; !ok {
			return fmt.Errorf("%w: portfolio outlook references an unknown portfolio", ErrInvalidPortfolioSentinelResult)
		}
		if _, duplicate := portfolioOutlookIDs[outlook.PortfolioID]; duplicate {
			return fmt.Errorf("%w: duplicate portfolio outlook", ErrInvalidPortfolioSentinelResult)
		}
		if err := validateModelPortfolioHorizonOutlooks(outlook.HorizonOutlooks); err != nil {
			return fmt.Errorf("%w: portfolio %s: %v", ErrInvalidPortfolioSentinelResult, outlook.PortfolioID, err)
		}
		outlook.HorizonOutlooks = sanitizeModelPortfolioHorizonOutlooks(outlook.HorizonOutlooks)
		portfolioOutlookIDs[outlook.PortfolioID] = struct{}{}
	}
	for portfolioID := range portfolioIDs {
		if _, ok := portfolioOutlookIDs[portfolioID]; !ok {
			return fmt.Errorf("%w: every reviewed portfolio requires exactly one portfolio outlook", ErrInvalidPortfolioSentinelResult)
		}
	}
	researchIDs := map[string]struct{}{}
	if len(report.ResearchAudit) > 100 {
		return fmt.Errorf("%w: too many research_audit items", ErrInvalidPortfolioSentinelResult)
	}
	for index := range report.ResearchAudit {
		item := &report.ResearchAudit[index]
		id := strings.TrimSpace(item.ID)
		safeID := safelog.Text(id, 160)
		source, ok := portfolioSentinelPublicSource(item.Source)
		if id == "" || safeID != id || !ok || strings.TrimSpace(item.Claim) == "" {
			return fmt.Errorf("%w: incomplete research_audit item", ErrInvalidPortfolioSentinelResult)
		}
		if _, duplicate := researchIDs[safeID]; duplicate {
			return fmt.Errorf("%w: duplicate research_audit id", ErrInvalidPortfolioSentinelResult)
		}
		researchIDs[safeID] = struct{}{}
		item.ID = safeID
		item.Kind = safelog.Text(item.Kind, 80)
		item.Query = safelog.Text(item.Query, 500)
		item.Source = source
		item.SourceTitle = safelog.Text(item.SourceTitle, 300)
		item.PublishedAt = safelog.Text(item.PublishedAt, 80)
		item.CheckedAt = safelog.Text(item.CheckedAt, 80)
		item.Claim = safelog.Text(item.Claim, 1000)
	}
	covered := map[string]struct{}{}
	actionable := false
	now := time.Now()
	for index := range report.ActionPlans {
		plan := &report.ActionPlans[index]
		plan.ID = strings.TrimSpace(plan.ID)
		plan.PortfolioID = strings.TrimSpace(plan.PortfolioID)
		plan.Symbol = strings.TrimSpace(plan.Symbol)
		plan.Market = strings.TrimSpace(plan.Market)
		plan.Action = strings.TrimSpace(plan.Action)
		plan.TriggerMode = strings.TrimSpace(plan.TriggerMode)
		plan.Name = safelog.Text(plan.Name, 200)
		plan.Reason = safelog.Text(plan.Reason, 1200)
		plan.RiskNotes = safelog.Text(plan.RiskNotes, 1200)
		if safelog.Text(plan.ID, 160) != plan.ID || len(plan.Conditions) > 10 ||
			len(plan.ResearchRefs) > 20 || len(plan.EvidenceRefs) > 50 {
			return fmt.Errorf("%w: action plan exceeds bounded fields", ErrInvalidPortfolioSentinelResult)
		}
		for conditionIndex := range plan.Conditions {
			plan.Conditions[conditionIndex].Key = strings.TrimSpace(plan.Conditions[conditionIndex].Key)
			plan.Conditions[conditionIndex].Type = normalizeWatchRuleType(plan.Conditions[conditionIndex].Type)
		}
		if plan.TriggerPolicy == "" {
			plan.TriggerPolicy = WatchTriggerPolicyAll
		}
		if plan.ID == "" || plan.PortfolioID == "" || plan.Symbol == "" || strings.TrimSpace(plan.Reason) == "" {
			return fmt.Errorf("%w: incomplete action plan", ErrInvalidPortfolioSentinelResult)
		}
		if err := validateModelHorizonOutlooks(plan.HorizonOutlooks); err != nil {
			return fmt.Errorf("%w: action plan %s: %v", ErrInvalidPortfolioSentinelResult, plan.ID, err)
		}
		plan.HorizonOutlooks = sanitizeModelHorizonOutlooks(plan.HorizonOutlooks)
		if _, ok := portfolioIDs[plan.PortfolioID]; !ok {
			return fmt.Errorf("%w: action plan references an unknown portfolio", ErrInvalidPortfolioSentinelResult)
		}
		key := plan.PortfolioID + "\x00" + strings.ToUpper(plan.Symbol)
		if _, duplicate := covered[key]; duplicate {
			return fmt.Errorf("%w: duplicate action plan for portfolio symbol", ErrInvalidPortfolioSentinelResult)
		}
		covered[key] = struct{}{}
		_, isHeld := held[key]
		gateDowngraded := s.applyDecisionGateToPortfolioPlan(ctx, run.ID, plan)
		if err := validatePortfolioSentinelPlanShape(*plan, isHeld); err != nil {
			return err
		}
		if !isHeld {
			if plan.Action != PortfolioSentinelPlanBuild && !(gateDowngraded && plan.Action == PortfolioSentinelPlanHold) {
				return fmt.Errorf("%w: non-held symbol may only use build_position", ErrInvalidPortfolioSentinelResult)
			}
			if _, ok := trusted[strings.ToUpper(plan.Symbol)]; !ok {
				return fmt.Errorf("%w: build_position symbol is outside the trusted candidate pool", ErrInvalidPortfolioSentinelResult)
			}
		}
		plan.ValidUntil = now.Add(portfolioSentinelPlanValidity)
		if plan.Action != PortfolioSentinelPlanHold && plan.TriggerMode == PortfolioSentinelTriggerConditional {
			plan.MonitorWindow = &PortfolioSentinelMonitorWindow{
				Kind:      "continuous_until_expiry",
				StartsAt:  now,
				ExpiresAt: plan.ValidUntil,
			}
		}
		if plan.Action != PortfolioSentinelPlanHold {
			actionable = true
			if len(plan.ResearchRefs) == 0 {
				return fmt.Errorf("%w: actionable plan is missing research_refs", ErrInvalidPortfolioSentinelResult)
			}
			for _, ref := range plan.ResearchRefs {
				if _, ok := researchIDs[strings.TrimSpace(ref)]; !ok {
					return fmt.Errorf("%w: actionable plan references unknown research evidence", ErrInvalidPortfolioSentinelResult)
				}
			}
		}
	}
	for key := range held {
		if _, ok := covered[key]; !ok {
			return fmt.Errorf("%w: every holding requires exactly one action plan", ErrInvalidPortfolioSentinelResult)
		}
	}
	if actionable && !portfolioSentinelHasExternalResearch(audit) {
		return fmt.Errorf("%w: actionable plans require observed public search or research Agent retrieval", ErrInvalidPortfolioSentinelResult)
	}
	return nil
}

func portfolioSentinelPublicSource(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return safelog.Text(parsed.String(), 1000), true
}

func portfolioSentinelHasExternalResearch(audit AgentCLIResearchAudit) bool {
	if audit.WebSearchCount > 0 {
		return true
	}
	for name, count := range audit.MCPToolCalls {
		if count <= 0 {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(name))
		if strings.HasPrefix(normalized, "stock_agent.") {
			continue
		}
		for _, marker := range []string{"search", "research", "browse", "fetch"} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	for name, count := range audit.AgentToolCalls {
		if count <= 0 {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(name))
		for _, marker := range []string{"search", "research", "browse", "web"} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	return false
}

func normalizePortfolioSentinelResearchLanguage(report *PortfolioSentinelReport, audit AgentCLIResearchAudit) {
	if report == nil || !portfolioSentinelHasExternalResearch(audit) {
		return
	}
	replacement := "外部搜索已执行但未返回可用于该判断的针对性证据"
	if len(report.ResearchAudit) > 0 {
		replacement = "已有外部公开资料检索记录，但未取得可用于该判断的针对性证据"
	}
	normalize := func(value string) string {
		replacer := strings.NewReplacer(
			"外部搜索不可用", replacement,
			"外部检索不可用", replacement,
			"无法使用外部搜索", replacement,
			"无法进行外部搜索", replacement,
		)
		return replacer.Replace(value)
	}
	report.RunSummary = normalize(report.RunSummary)
	for index := range report.DataQualityNotes {
		report.DataQualityNotes[index] = normalize(report.DataQualityNotes[index])
	}
	for index := range report.AffectedHoldings {
		for reasonIndex := range report.AffectedHoldings[index].Reasons {
			report.AffectedHoldings[index].Reasons[reasonIndex] = normalize(report.AffectedHoldings[index].Reasons[reasonIndex])
		}
	}
	for index := range report.ActionPlans {
		report.ActionPlans[index].Reason = normalize(report.ActionPlans[index].Reason)
		report.ActionPlans[index].RiskNotes = normalize(report.ActionPlans[index].RiskNotes)
	}
}

func validatePortfolioSentinelPlanShape(plan PortfolioSentinelActionPlan, isHeld bool) error {
	switch plan.Action {
	case PortfolioSentinelPlanBuild, PortfolioSentinelPlanAdd, PortfolioSentinelPlanHold,
		PortfolioSentinelPlanReduce, PortfolioSentinelPlanExit:
	default:
		return fmt.Errorf("%w: unsupported action plan action", ErrInvalidPortfolioSentinelResult)
	}
	if isHeld && plan.Action == PortfolioSentinelPlanBuild {
		return fmt.Errorf("%w: held symbol must use add_position instead of build_position", ErrInvalidPortfolioSentinelResult)
	}
	if plan.Action == PortfolioSentinelPlanHold {
		if plan.Sizing != nil {
			return fmt.Errorf("%w: hold action must not include sizing", ErrInvalidPortfolioSentinelResult)
		}
		return nil
	}
	if plan.TriggerMode != PortfolioSentinelTriggerImmediate && plan.TriggerMode != PortfolioSentinelTriggerConditional {
		return fmt.Errorf("%w: actionable plan requires immediate or conditional trigger_mode", ErrInvalidPortfolioSentinelResult)
	}
	if plan.TriggerMode == PortfolioSentinelTriggerConditional && len(plan.Conditions) == 0 {
		return fmt.Errorf("%w: conditional action plan requires conditions", ErrInvalidPortfolioSentinelResult)
	}
	if plan.TriggerPolicy != WatchTriggerPolicyAny && plan.TriggerPolicy != WatchTriggerPolicyAll {
		return fmt.Errorf("%w: invalid trigger_policy", ErrInvalidPortfolioSentinelResult)
	}
	for _, condition := range plan.Conditions {
		key := strings.TrimSpace(condition.Key)
		if key == "" || safelog.Text(key, 120) != key || !validPortfolioSentinelCondition(condition) {
			return fmt.Errorf("%w: invalid deterministic plan condition", ErrInvalidPortfolioSentinelResult)
		}
	}
	if plan.Sizing == nil || plan.Sizing.Value <= 0 || plan.Sizing.Value > 100 {
		return fmt.Errorf("%w: actionable plan requires sizing value in (0,100]", ErrInvalidPortfolioSentinelResult)
	}
	expectedMode := PortfolioSentinelSizingTargetPortfolioPct
	if plan.Action == PortfolioSentinelPlanReduce || plan.Action == PortfolioSentinelPlanExit {
		expectedMode = PortfolioSentinelSizingAvailableQuantityPct
	}
	if plan.Sizing.Mode != expectedMode {
		return fmt.Errorf("%w: action plan sizing mode does not match action", ErrInvalidPortfolioSentinelResult)
	}
	if plan.Action == PortfolioSentinelPlanExit && plan.Sizing.Value != 100 {
		return fmt.Errorf("%w: exit_position must use 100%% available quantity", ErrInvalidPortfolioSentinelResult)
	}
	return nil
}

func validPortfolioSentinelCondition(condition PortfolioSentinelPlanCondition) bool {
	switch normalizeWatchRuleType(condition.Type) {
	case WatchRulePriceAbove, WatchRulePriceBelow, WatchRulePctChangeAbove,
		WatchRulePctChangeBelow, WatchRuleDailyCloseAbove, WatchRuleDailyCloseBelow,
		WatchRulePortfolioSymbolWeightOver, WatchRulePortfolioSymbolWeightBelow:
		return condition.Threshold != nil
	case WatchRulePriceBetween:
		return condition.Low > 0 && condition.High > condition.Low
	default:
		return false
	}
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
		"portfolioCount":     len(ctx.Portfolios),
		"holdingCount":       holdings,
		"priorJudgmentCount": len(ctx.PriorJudgments),
		"newsEventCount":     len(ctx.NewsEvents),
		"rawNewsCount":       len(ctx.RawNews),
		"quoteCount":         quotes,
		"dailyBarSymbols":    daily,
		"minuteBarSymbols":   minute,
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
