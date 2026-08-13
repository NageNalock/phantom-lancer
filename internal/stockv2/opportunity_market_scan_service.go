package stockv2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: a fixed wake-up cadence only observes durable state; making this
	// configurable would create a low-value tuning surface without changing scan frequency.
	opportunityMarketScanSchedulerInterval = 30 * time.Second
	// ponytail: source calls are optional enrichment and must not hold the daily
	// heavy-work slot indefinitely. Keep fixed with the fixed scan budgets.
	opportunityMarketScanQFQTimeout  = 20 * time.Second
	opportunityMarketScanQFQInterval = 200 * time.Millisecond
	// ponytail: two delayed attempts bound model cost while covering short provider outages.
	opportunityMarketScanMaxRetries      = 2
	opportunityMarketScanFirstRetryDelay = 5 * time.Minute
	opportunityMarketScanLastRetryDelay  = 30 * time.Minute
)

func (s *Service) GetOpportunityMarketScanRun(ctx context.Context, id string) (OpportunityMarketScanRun, error) {
	return s.store.GetOpportunityMarketScanRun(ctx, strings.TrimSpace(id))
}

func (s *Service) ListOpportunityMarketScanRuns(ctx context.Context, filter OpportunityMarketScanRunListFilter) ([]OpportunityMarketScanRun, error) {
	return s.store.ListOpportunityMarketScanRuns(ctx, filter)
}

func (s *Service) CountOpportunityMarketScanRuns(ctx context.Context, filter OpportunityMarketScanRunListFilter) (int, error) {
	return s.store.CountOpportunityMarketScanRuns(ctx, filter)
}

func (s *Service) ListOpportunityMarketScanCandidates(ctx context.Context, filter OpportunityMarketScanCandidateListFilter) ([]OpportunityMarketScanCandidate, error) {
	return s.store.ListOpportunityMarketScanCandidates(ctx, filter)
}

func (s *Service) CountOpportunityMarketScanCandidates(ctx context.Context, filter OpportunityMarketScanCandidateListFilter) (int, error) {
	return s.store.CountOpportunityMarketScanCandidates(ctx, filter)
}

func (s *Service) ProbeOpportunityMarketFundFlow(ctx context.Context) OpportunityMarketFundFlowProbe {
	started := time.Now()
	result := OpportunityMarketFundFlowProbe{Status: "failed"}
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err == nil {
		endDate := time.Now().Format("2006-01-02")
		var fetched opportunityFundFlowFetchResult
		fetched, err = s.fetchOpportunityMarketFundFlow(ctx, config, "000001", "SZ", endDate, 5)
		if err == nil {
			result.OK, result.Status, result.Source, result.Count = true, "available", fetched.Source, len(fetched.Points)
		}
	}
	if err != nil {
		result.Error = safelog.Error(err, 200)
	}
	result.Duration = time.Since(started).Milliseconds()
	return result
}

func (s *Service) GetOpportunityMarketScanStatus(ctx context.Context) (OpportunityMarketScanStatus, error) {
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil {
		return OpportunityMarketScanStatus{}, err
	}
	active, err := s.store.GetActiveOpportunityMarketScanRun(ctx)
	if err != nil {
		return OpportunityMarketScanStatus{}, err
	}
	runs, err := s.store.ListOpportunityMarketScanRuns(ctx, OpportunityMarketScanRunListFilter{Limit: 1})
	if err != nil {
		return OpportunityMarketScanStatus{}, err
	}
	var latest *OpportunityMarketScanRun
	if len(runs) > 0 {
		item := runs[0]
		latest = &item
	}
	raw, err := s.store.marketDB.LoadOpportunityMarketScanCoverage(ctx)
	if err != nil {
		return OpportunityMarketScanStatus{}, err
	}
	tradeDate, universe, covered := opportunityMarketScanCoverage(raw)
	ratio := 0.0
	if universe > 0 {
		ratio = float64(covered) / float64(universe)
	}
	ready := tradeDate != "" && ratio >= opportunityMarketScanMinimumCoverage
	blocked := ""
	if !ready {
		blocked = fmt.Sprintf("主板未复权日线覆盖率 %.1f%%，至少需要 %.0f%%", ratio*100, opportunityMarketScanMinimumCoverage*100)
	}
	return OpportunityMarketScanStatus{
		Config: config, ActiveRun: active, LatestRun: latest, LatestDataTradeDate: tradeDate,
		UniverseCount: universe, CoveredCount: covered, CoverageRatio: ratio, Ready: ready,
		BlockedReason: blocked, ScheduleDescription: "每个新交易日完成全市场数据维护后运行一次",
		MaxRetries: opportunityMarketScanMaxRetries,
		Budgets: map[string]int{
			"localPrefilter": opportunityMarketScanLocalLimit, "qfqAndQuote": opportunityMarketScanQFQLimit,
			"fundFlow": opportunityMarketScanFundFlowLimit, "agentResearch": opportunityMarketScanResearchLimit,
			"finalCandidates": opportunityMarketScanFinalLimit, "strategyDrafts": opportunityMarketScanStrategyLimit,
		},
		RecommendedModel: "GPT-5.6-Terra / Codex CLI / medium",
	}, nil
}

func opportunityMarketScanCoverage(raw []opportunityMarketScanRawMetric) (string, int, int) {
	latest := ""
	universe := 0
	for _, item := range raw {
		if !isOpportunityMainBoardInstrument(item.Instrument) {
			continue
		}
		universe++
		if item.TradeDate > latest {
			latest = item.TradeDate
		}
	}
	covered := 0
	for _, item := range raw {
		if isOpportunityMainBoardInstrument(item.Instrument) && item.RowCount >= 60 && item.TradeDate == latest {
			covered++
		}
	}
	return latest, universe, covered
}

func (s *Service) UpdateOpportunityMarketScanConfig(ctx context.Context, req RequestUpdateOpportunityMarketScanConfig) (OpportunityMarketScanStatus, error) {
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil {
		return OpportunityMarketScanStatus{}, err
	}
	if req.Enabled != nil {
		config.Enabled = *req.Enabled
	}
	if value := strings.TrimSpace(req.PrimaryFundFlowAPIKey); value != "" {
		config.PrimaryFundFlowAPIKey = value
	}
	if value := strings.TrimSpace(req.BackupFundFlowAPIKey); value != "" {
		config.BackupFundFlowAPIKey = value
	}
	if value := strings.TrimSpace(req.BackupFundFlowProxy); value != "" {
		if _, err := opportunityFundFlowBackupClient(value); err != nil {
			return OpportunityMarketScanStatus{}, err
		}
		config.BackupFundFlowProxy = value
	}
	if req.ClearPrimaryFundFlowAPIKey {
		config.PrimaryFundFlowAPIKey = ""
	}
	if req.ClearBackupFundFlowAPIKey {
		config.BackupFundFlowAPIKey = ""
	}
	if req.ClearBackupFundFlowProxy {
		config.BackupFundFlowProxy = ""
	}
	if _, err := s.store.SaveOpportunityMarketScanConfig(ctx, config); err != nil {
		return OpportunityMarketScanStatus{}, err
	}
	if config.Enabled {
		s.StartBackground(context.Background())
	}
	return s.GetOpportunityMarketScanStatus(ctx)
}

func (s *Service) launchOpportunityMarketScanWorker(runID string) bool {
	s.opportunityScanWorkerMu.Lock()
	if s.opportunityScanClosing {
		s.opportunityScanWorkerMu.Unlock()
		return false
	}
	s.opportunityScanWg.Add(1)
	ctx := s.opportunityScanCtx
	s.opportunityScanWorkerMu.Unlock()
	go func() {
		defer s.opportunityScanWg.Done()
		s.prepareOpportunityMarketScan(ctx, runID)
	}()
	return true
}

func (s *Service) StartOpportunityMarketScan(ctx context.Context, trigger, requestedBy string) (OpportunityMarketScanRun, error) {
	// ponytail: this service is a single process, so a narrow start mutex closes
	// the owner double-click/scheduler race without adding a distributed lease.
	s.opportunityScanStartMu.Lock()
	defer s.opportunityScanStartMu.Unlock()
	if active, err := s.store.GetActiveOpportunityMarketScanRun(ctx); err != nil {
		return OpportunityMarketScanRun{}, err
	} else if active != nil {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanAlreadyRunning
	}
	status, err := s.GetOpportunityMarketScanStatus(ctx)
	if err != nil {
		return OpportunityMarketScanRun{}, err
	}
	if !status.Ready {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanDataNotReady
	}
	if trigger != OpportunityMarketScanTriggerScheduled {
		trigger = OpportunityMarketScanTriggerManual
	}
	sourceUpdateJobID := ""
	if latestJob, latestErr := s.store.GetLatestUpdateJob(ctx); latestErr == nil && latestJob.Status == "completed" {
		sourceUpdateJobID = latestJob.ID
	}
	now := time.Now()
	run, err := s.store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: trigger, RequestedBy: strings.TrimSpace(requestedBy), Status: OpportunityMarketScanStatusPending,
		TradeDate: status.LatestDataTradeDate, SourceUpdateJobID: sourceUpdateJobID, UniverseCount: status.UniverseCount,
		CoveredCount: status.CoveredCount, StartedAt: now,
	})
	if err != nil {
		return OpportunityMarketScanRun{}, err
	}
	config := status.Config
	config.LastRunID, config.LastRunStatus, config.LastRunAt = run.ID, run.Status, now
	if trigger == OpportunityMarketScanTriggerScheduled {
		// ponytail: claim the trading date when the durable run is created so a
		// terminal failure cannot produce an unbounded 30-second launch loop.
		config.LastScannedTradeDate = run.TradeDate
	}
	_, _ = s.store.SaveOpportunityMarketScanConfig(ctx, config)
	s.launchOpportunityMarketScanWorker(run.ID)
	return run, nil
}

func (s *Service) prepareOpportunityMarketScan(ctx context.Context, runID string) {
	if !s.tryStartOpportunityMarketScanWorker() {
		return
	}
	defer s.finishOpportunityMarketScanWorker()
	if !s.tryStartBackgroundHeavyWork() {
		s.deferOpportunityMarketScan(ctx, runID, time.Minute)
		return
	}
	defer s.finishBackgroundHeavyWork()
	run, err := s.store.GetOpportunityMarketScanRun(ctx, runID)
	if err != nil || !opportunityMarketScanStatusActive(run.Status) {
		return
	}
	if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
		s.deferOpportunityMarketScan(ctx, runID, 2*time.Minute)
		return
	}
	run.Status = OpportunityMarketScanStatusPrefiltering
	run.NextRetryAt = time.Time{}
	run.ErrorMessage = ""
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
	raw, err := s.store.marketDB.LoadOpportunityMarketScanMetrics(ctx)
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, false)
		return
	}
	tradeDate, universe, covered := opportunityMarketScanCoverage(raw)
	if universe == 0 || float64(covered)/float64(universe) < opportunityMarketScanMinimumCoverage {
		s.failOpportunityMarketScan(ctx, run, ErrOpportunityMarketScanDataNotReady, false)
		return
	}
	scored := scoreOpportunityMarketScanMetrics(raw)
	if len(scored) == 0 {
		s.failOpportunityMarketScan(ctx, run, errors.New("no candidates passed local prefilter"), false)
		return
	}
	if len(scored) > opportunityMarketScanLocalLimit {
		scored = scored[:opportunityMarketScanLocalLimit]
	}
	run.TradeDate, run.UniverseCount, run.CoveredCount, run.PrefilterCount = tradeDate, universe, covered, len(scored)
	_ = s.store.DeleteOpportunityMarketScanCandidates(ctx, run.ID)
	candidates := make([]OpportunityMarketScanCandidate, 0, len(scored))
	for i, item := range scored {
		metrics := opportunityMarketMetricsFromRaw(item)
		candidates = append(candidates, OpportunityMarketScanCandidate{
			ID: generateID(), ScanRunID: run.ID, Symbol: item.Instrument.Symbol, Market: item.Instrument.Market,
			Name: item.Instrument.Name, Industry: item.Instrument.Industry, Sector: item.Instrument.Sector,
			Concepts: item.Instrument.Concepts, Stage: OpportunityMarketScanCandidatePrefiltered,
			PrefilterRank: i + 1, PrefilterScore: item.PrefilterScore, FinalScore: item.PrefilterScore,
			Metrics: metrics, StrategyStatus: OpportunityMarketScanStrategySkipped,
		})
	}
	if err := s.store.UpsertOpportunityMarketScanCandidates(ctx, candidates); err != nil {
		s.failOpportunityMarketScan(ctx, run, err, false)
		return
	}
	run.Status = OpportunityMarketScanStatusEnriching
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
	candidates = s.enrichOpportunityMarketScan(ctx, &run, candidates)
	if len(candidates) == 0 {
		s.failOpportunityMarketScan(ctx, run, errors.New("no candidates survived enrichment"), false)
		return
	}
	if err := s.store.UpsertOpportunityMarketScanCandidates(ctx, candidates); err != nil {
		s.failOpportunityMarketScan(ctx, run, err, false)
		return
	}
	for _, candidate := range candidates {
		if candidate.Metrics.QFQAvailable || candidate.Metrics.QuoteAvailable || candidate.Metrics.FundFlowAvailable {
			run.EnrichedCount++
		}
		if candidate.Stage == OpportunityMarketScanCandidateResearch {
			run.ResearchCount++
		}
	}
	if run.ResearchCount == 0 {
		s.failOpportunityMarketScan(ctx, run, errors.New("no candidates survived the short-term overheating guard"), false)
		return
	}
	opp, err := s.ensureOpportunityMarketScanOpportunity(ctx)
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, false)
		return
	}
	run.OpportunityID = opp.ID
	updatedRun, updateErr := s.store.UpdateOpportunityMarketScanRun(ctx, run)
	if updateErr != nil {
		s.failOpportunityMarketScan(ctx, run, updateErr, false)
		return
	}
	run = updatedRun
	discovery, err := s.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{
		RequestedBy: run.RequestedBy, MarketScanRunID: run.ID,
	})
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, true)
		return
	}
	run.Status, run.DiscoveryRunID = OpportunityMarketScanStatusResearching, discovery.ID
	run.RetryCount, run.NextRetryAt, run.ErrorMessage = 0, time.Time{}, ""
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
}

func (s *Service) tryStartOpportunityMarketScanWorker() bool {
	s.opportunityScanMu.Lock()
	defer s.opportunityScanMu.Unlock()
	if s.opportunityScanRun {
		return false
	}
	s.opportunityScanRun = true
	return true
}

func (s *Service) finishOpportunityMarketScanWorker() {
	s.opportunityScanMu.Lock()
	s.opportunityScanRun = false
	s.opportunityScanMu.Unlock()
}

func opportunityMarketMetricsFromRaw(item opportunityMarketScanRawMetric) OpportunityMarketScanMetrics {
	return OpportunityMarketScanMetrics{
		TradeDate: item.TradeDate, Return5Pct: pctReturn(item.Close, item.Close5),
		Return20Pct: pctReturn(item.Close, item.Close20), Return60Pct: pctReturn(item.Close, item.Close60),
		MA20GapPct: pctReturn(item.Close, item.MA20), MA60GapPct: pctReturn(item.Close, item.MA60),
		VolumeRatio5To20: safeRatio(item.Volume5, item.Volume20), UpVolumeShare20: item.UpVolumeShare20,
		Volatility20: item.Volatility20, MedianAmount20: item.MedianAmount20,
		IndustryBreadth20: item.IndustryBreadth, LatestPrice: item.Close,
	}
}

func (s *Service) enrichOpportunityMarketScan(ctx context.Context, run *OpportunityMarketScanRun, candidates []OpportunityMarketScanCandidate) []OpportunityMarketScanCandidate {
	qfqLimit := min(len(candidates), opportunityMarketScanQFQLimit)
	for i := 0; i < qfqLimit; i++ {
		candidate := &candidates[i]
		_, _, latest, _, _, _ := s.store.GetDailyBarsStats(ctx, candidate.Symbol, DailyBarAdjustedQFQ)
		if latest < run.TradeDate {
			end, _ := time.Parse("2006-01-02", run.TradeDate)
			start := end.AddDate(-2, 0, 0).Format("2006-01-02")
			fetchCtx, cancel := context.WithTimeout(ctx, opportunityMarketScanQFQTimeout)
			_, fetchErr := s.ensureOneSymbol(fetchCtx, candidate.Symbol, candidate.Market, start, run.TradeDate, DailyBarAdjustedQFQ)
			cancel()
			// ponytail: a fixed, short pause is sufficient for this bounded serial batch;
			// a configurable limiter would add machinery without improving the 60-symbol ceiling.
			select {
			case <-ctx.Done():
				return candidates
			case <-time.After(opportunityMarketScanQFQInterval):
			}
			if fetchErr != nil {
				continue
			}
		}
		bars, err := s.store.GetDailyBars(ctx, candidate.Symbol, DailyBarAdjustedQFQ, "", run.TradeDate, 120)
		if err == nil && len(bars) >= 61 {
			applyOpportunityQFQMetrics(&candidate.Metrics, bars)
		}
	}
	symbols := make([]string, 0, qfqLimit)
	for i := 0; i < qfqLimit; i++ {
		symbols = append(symbols, candidates[i].Symbol)
	}
	if specs, _, err := normalizeQuoteSymbols(symbols); err == nil {
		quotes, _ := s.fetchLatestQuotesForSpecsWithFailures(ctx, specs)
		bySymbol := map[string]StockV2QuoteLatest{}
		for _, quote := range quotes {
			bySymbol[quote.Symbol] = quote
		}
		for i := 0; i < qfqLimit; i++ {
			if quote, ok := bySymbol[candidates[i].Symbol]; ok {
				candidates[i].Metrics.LatestPrice = quote.LastPrice
				candidates[i].Metrics.LatestPctChange = quote.PctChange
				candidates[i].Metrics.LatestTurnoverRate = quote.TurnoverRate
				candidates[i].Metrics.LatestMainFlowPct = quote.MainNetInflowPct
				candidates[i].Metrics.QuoteAvailable = quote.LastPrice > 0
			}
		}
	}
	flowLimit := min(len(candidates), opportunityMarketScanFundFlowLimit)
	run.FundFlowRequestedCount = flowLimit
	config, configErr := s.store.GetOpportunityMarketScanConfig(ctx)
	flowCtx, cancelFlow := context.WithTimeout(ctx, opportunityFundFlowStageTimeout)
	defer cancelFlow()
	var flowErrors []string
	for i := 0; i < flowLimit; i++ {
		if configErr != nil {
			candidates[i].Metrics.FundFlowStatus = "run_degraded"
			continue
		}
		flow, err := s.fetchOpportunityMarketFundFlow(flowCtx, config, candidates[i].Symbol, candidates[i].Market, run.TradeDate, 120)
		if err == nil {
			applyOpportunityFundFlow(&candidates[i].Metrics, flow.Points, flow.Source)
			run.FundFlowAvailableCount++
			if run.FundFlowSource == "" {
				run.FundFlowSource = flow.Source
			} else if run.FundFlowSource != flow.Source {
				run.FundFlowSource = "mixed"
			}
			continue
		}
		candidates[i].Metrics.FundFlowStatus = opportunityFundFlowFailureStatus(err)
		if len(flowErrors) < 3 {
			flowErrors = append(flowErrors, safelog.Error(err, 140))
		}
	}
	for i := flowLimit; i < len(candidates); i++ {
		candidates[i].Metrics.FundFlowStatus = "not_requested"
	}
	run.FundFlowUsed = flowLimit > 0 && float64(run.FundFlowAvailableCount)/float64(flowLimit) >= opportunityMarketScanMinimumCoverage
	if run.FundFlowUsed {
		run.FundFlowStatus = "available"
	} else if configErr != nil || (!config.PrimaryFundFlowConfigured && !config.BackupFundFlowConfigured) {
		run.FundFlowStatus = "not_configured"
	} else {
		run.FundFlowStatus = "run_degraded"
	}
	if len(flowErrors) > 0 {
		run.FundFlowError = strings.Join(flowErrors, "; ")
	}
	if run.FundFlowUsed {
		scoreOpportunityFundFlowPercentiles(candidates[:flowLimit])
	}
	threads := s.activeOpportunityMarketScanThreads(ctx)
	for i := range candidates {
		candidates[i].ThemeScore, candidates[i].Metrics.ThemeSignals = opportunityMarketThemeScore(candidates[i], threads)
		candidates[i].Metrics.FundFlowUsed = run.FundFlowUsed && candidates[i].Metrics.FundFlowAvailable
		quality, applicable := 0.0, 0.0
		applicable += 35
		if candidates[i].Metrics.QFQAvailable {
			quality += 35
		}
		applicable += 20
		if candidates[i].Metrics.QuoteAvailable {
			quality += 20
		}
		if run.FundFlowUsed {
			applicable += 20
			if candidates[i].Metrics.FundFlowAvailable {
				quality += 20
			}
		}
		if applicable > 0 {
			quality = quality / applicable * 100
		}
		risk := math.Max(0, candidates[i].Metrics.Return5Pct-18)*1.5 + math.Max(0, candidates[i].Metrics.Return20Pct-35)
		candidates[i].RiskPenalty = math.Min(risk, 35)
		quoteScore := clampScore(50 + candidates[i].Metrics.LatestPctChange*2)
		if run.FundFlowUsed {
			candidates[i].FinalScore = clampScore(candidates[i].PrefilterScore*0.45 + candidates[i].FlowScore*0.30 + quoteScore*0.10 + candidates[i].ThemeScore*0.10 + quality*0.05 - candidates[i].RiskPenalty)
		} else {
			// ponytail: when the source-wide coverage gate fails, renormalize the
			// proven dimensions instead of inventing a neutral flow score.
			candidates[i].FinalScore = clampScore((candidates[i].PrefilterScore*0.45+quoteScore*0.10+candidates[i].ThemeScore*0.10+quality*0.05)/0.70 - candidates[i].RiskPenalty)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].FinalScore == candidates[j].FinalScore {
			return candidates[i].Symbol < candidates[j].Symbol
		}
		return candidates[i].FinalScore > candidates[j].FinalScore
	})
	researchCount := 0
	for i := range candidates {
		candidates[i].FinalRank = i + 1
		if candidates[i].Metrics.Return5Pct > 18 || candidates[i].Metrics.Return20Pct > 35 {
			candidates[i].Stage = OpportunityMarketScanCandidateExcluded
			candidates[i].ExclusionReason = "前复权短期涨幅超过风险边界"
		} else if researchCount < opportunityMarketScanResearchLimit {
			candidates[i].Stage = OpportunityMarketScanCandidateResearch
			researchCount++
		} else {
			candidates[i].Stage = OpportunityMarketScanCandidateExcluded
			candidates[i].ExclusionReason = "低于本轮模型复核预算"
		}
	}
	return candidates
}

func scoreOpportunityFundFlowPercentiles(candidates []OpportunityMarketScanCandidate) {
	var available []*OpportunityMarketScanCandidate
	for i := range candidates {
		if candidates[i].Metrics.FundFlowAvailable {
			available = append(available, &candidates[i])
		}
	}
	if len(available) == 0 {
		return
	}
	percentile := func(candidate *OpportunityMarketScanCandidate, value func(*OpportunityMarketScanCandidate) float64) float64 {
		below := 0
		for _, other := range available {
			if value(other) < value(candidate) {
				below++
			}
		}
		if len(available) == 1 {
			return 100
		}
		return float64(below) / float64(len(available)-1) * 100
	}
	for _, candidate := range available {
		ratio := percentile(candidate, func(item *OpportunityMarketScanCandidate) float64 { return item.Metrics.MainFlowRatio20 })
		positiveDays := percentile(candidate, func(item *OpportunityMarketScanCandidate) float64 { return float64(item.Metrics.PositiveFlowDays20) })
		candidate.FlowScore = ratio*0.7 + positiveDays*0.3
	}
}

func applyOpportunityQFQMetrics(metrics *OpportunityMarketScanMetrics, bars []StockV2DailyBar) {
	if len(bars) < 61 {
		return
	}
	last := len(bars) - 1
	metrics.TradeDate, metrics.LatestPrice, metrics.QFQAvailable = bars[last].TradeDate, bars[last].Close, true
	metrics.Return5Pct = pctReturn(bars[last].Close, bars[last-5].Close)
	metrics.Return20Pct = pctReturn(bars[last].Close, bars[last-20].Close)
	metrics.Return60Pct = pctReturn(bars[last].Close, bars[last-60].Close)
	var ma20, ma60, vol5, vol20, upVolume, allVolume, mean, variance float64
	amounts := make([]float64, 0, 20)
	for i := last; i > last-60; i-- {
		ma60 += bars[i].Close
		if i > last-20 {
			ma20 += bars[i].Close
			vol20 += bars[i].Volume
			mean += bars[i].PctChange
			amounts = append(amounts, opportunityDailyBarAmount(bars[i]))
			allVolume += bars[i].Volume
			if bars[i].PctChange > 0 {
				upVolume += bars[i].Volume
			}
		}
		if i > last-5 {
			vol5 += bars[i].Volume
		}
	}
	ma20 /= 20
	ma60 /= 60
	mean /= 20
	for i := last; i > last-20; i-- {
		variance += math.Pow(bars[i].PctChange-mean, 2)
	}
	sort.Float64s(amounts)
	metrics.MA20GapPct, metrics.MA60GapPct = pctReturn(bars[last].Close, ma20), pctReturn(bars[last].Close, ma60)
	metrics.VolumeRatio5To20, metrics.UpVolumeShare20 = safeRatio(vol5/5, vol20/20), safeRatio(upVolume, allVolume)
	metrics.Volatility20, metrics.MedianAmount20 = math.Sqrt(variance/19), amounts[len(amounts)/2]
}

func opportunityDailyBarAmount(bar StockV2DailyBar) float64 {
	if bar.Amount > 0 {
		return bar.Amount
	}
	if bar.Close <= 0 || bar.Volume <= 0 {
		return 0
	}
	// ponytail: the current Tencent K-line source reports volume in hands and no
	// amount. This local estimate preserves liquidity ranking without another
	// full-market provider request; add an explicit volume-unit field if a second
	// amount-less source with different units is introduced.
	return bar.Close * bar.Volume * 100
}

func (s *Service) activeOpportunityMarketScanThreads(ctx context.Context) []NewsThread {
	var out []NewsThread
	for offset := 0; ; offset += 200 {
		items, err := s.store.ListNewsThreads(ctx, NewsThreadListFilter{Status: NewsThreadStatusActive, Limit: 200, Offset: offset})
		if err != nil {
			return out
		}
		out = append(out, items...)
		if len(items) < 200 {
			return out
		}
	}
}

func opportunityMarketThemeScore(candidate OpportunityMarketScanCandidate, threads []NewsThread) (float64, []string) {
	score := 0.0
	var signals []string
	for _, thread := range threads {
		direct := stringListContains(thread.Symbols, candidate.Symbol) || stringListContains(thread.Leaders, candidate.Symbol) ||
			stringListContains(thread.Followers, candidate.Symbol) || stringListContains(thread.NextCandidates, candidate.Symbol)
		related := stringListContainsFold(thread.Industries, candidate.Industry) || stringListContainsFold(thread.Industries, candidate.Sector)
		for _, concept := range candidate.Concepts {
			related = related || stringListContainsFold(thread.Industries, concept)
		}
		if direct {
			score = math.Max(score, 100)
		} else if related {
			score = math.Max(score, 55)
		} else {
			continue
		}
		if len(signals) < 5 {
			signals = append(signals, thread.Title)
		}
	}
	return score, signals
}

func stringListContains(items []string, expected string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == strings.TrimSpace(expected) {
			return true
		}
	}
	return false
}

func stringListContainsFold(items []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), expected) {
			return true
		}
	}
	return false
}

func clampScore(value float64) float64 { return math.Max(0, math.Min(100, value)) }

func (s *Service) ensureOpportunityMarketScanOpportunity(ctx context.Context) (Opportunity, error) {
	opp, err := s.store.GetOpportunityByCreatedBy(ctx, OpportunityMarketScanCreatedBy)
	if err == nil {
		return opp, nil
	}
	if !errors.Is(err, ErrOpportunityNotFound) {
		return Opportunity{}, err
	}
	return s.CreateOpportunity(ctx, RequestCreateOpportunity{
		Title: "A股主板市场机会扫描", UserThesis: "基于全市场本地行情、资金流和消息脉络发现值得进一步验证的非持仓机会。",
		MarketScope: OpportunityMarketScopeAShare, InstrumentScope: OpportunityInstrumentScopeStock,
		CreatedBy: OpportunityMarketScanCreatedBy,
	})
}

func (s *Service) deferOpportunityMarketScan(ctx context.Context, runID string, delay time.Duration) {
	run, err := s.store.GetOpportunityMarketScanRun(ctx, runID)
	if err != nil {
		return
	}
	run.Status, run.NextRetryAt = OpportunityMarketScanStatusPending, time.Now().Add(delay)
	run.ErrorMessage = "等待后台重任务执行槽"
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
}

func (s *Service) failOpportunityMarketScan(ctx context.Context, run OpportunityMarketScanRun, err error, retryable bool) {
	run.ErrorMessage = safelog.Error(err, 500)
	if retryable && run.RetryCount < opportunityMarketScanMaxRetries {
		delays := []time.Duration{opportunityMarketScanFirstRetryDelay, opportunityMarketScanLastRetryDelay}
		run.RetryCount++
		run.NextRetryAt = time.Now().Add(delays[run.RetryCount-1])
	} else {
		run.Status, run.FinishedAt, run.NextRetryAt = OpportunityMarketScanStatusFailed, time.Now(), time.Time{}
	}
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
	s.updateOpportunityMarketScanConfigFromRun(ctx, run)
	if s.log != nil {
		s.log.Warn("opportunity market scan stage failed",
			"run_id", run.ID, "status", run.Status, "retry_count", run.RetryCount,
			"retry_scheduled", !run.NextRetryAt.IsZero(), "error", safelog.Text(run.ErrorMessage, 300))
	}
}

func (s *Service) updateOpportunityMarketScanConfigFromRun(ctx context.Context, run OpportunityMarketScanRun) {
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil {
		return
	}
	config.LastRunID, config.LastRunStatus, config.LastError = run.ID, run.Status, run.ErrorMessage
	if run.Status == OpportunityMarketScanStatusCompleted || run.Status == OpportunityMarketScanStatusPartial {
		config.LastSuccessAt = run.FinishedAt
		config.LastScannedTradeDate = run.TradeDate
	}
	_, _ = s.store.SaveOpportunityMarketScanConfig(ctx, config)
}

func (s *Service) runOpportunityMarketScanScheduler(ctx context.Context) {
	ticker := time.NewTicker(opportunityMarketScanSchedulerInterval)
	defer ticker.Stop()
	s.tickOpportunityMarketScanScheduler(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickOpportunityMarketScanScheduler(ctx)
		}
	}
}

func (s *Service) tickOpportunityMarketScanScheduler(ctx context.Context) {
	active, err := s.store.GetActiveOpportunityMarketScanRun(ctx)
	if err != nil {
		return
	}
	if active != nil {
		s.advanceOpportunityMarketScan(ctx, *active)
		return
	}
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil || !config.Enabled {
		return
	}
	latestJob, err := s.store.GetLatestUpdateJob(ctx)
	if err != nil || latestJob.Status != "completed" {
		return
	}
	status, err := s.GetOpportunityMarketScanStatus(ctx)
	if err != nil || !status.Ready || status.LatestDataTradeDate == config.LastScannedTradeDate {
		return
	}
	_, _ = s.StartOpportunityMarketScan(ctx, OpportunityMarketScanTriggerScheduled, "system:scheduler")
}

func (s *Service) advanceOpportunityMarketScan(ctx context.Context, run OpportunityMarketScanRun) {
	if !run.NextRetryAt.IsZero() && time.Now().Before(run.NextRetryAt) {
		return
	}
	switch run.Status {
	case OpportunityMarketScanStatusPending, OpportunityMarketScanStatusPrefiltering, OpportunityMarketScanStatusEnriching:
		s.launchOpportunityMarketScanWorker(run.ID)
	case OpportunityMarketScanStatusResearching:
		s.advanceOpportunityMarketResearch(ctx, run)
	case OpportunityMarketScanStatusDrafting:
		s.advanceOpportunityMarketDrafting(ctx, run)
	}
}

func (s *Service) advanceOpportunityMarketResearch(ctx context.Context, run OpportunityMarketScanRun) {
	discovery, err := s.store.GetOpportunityDiscoveryRun(ctx, run.DiscoveryRunID)
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, true)
		return
	}
	if discovery.Status == OpportunityDiscoveryRunStatusFailed {
		if !run.NextRetryAt.IsZero() {
			next, startErr := s.StartOpportunityDiscoveryRun(ctx, run.OpportunityID, RequestStartOpportunityDiscoveryRun{
				RequestedBy: run.RequestedBy, MarketScanRunID: run.ID,
			})
			if startErr != nil {
				s.failOpportunityMarketScan(ctx, run, startErr, false)
				return
			}
			run.DiscoveryRunID, run.NextRetryAt, run.ErrorMessage = next.ID, time.Time{}, ""
			_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
			return
		}
		if run.RetryCount < opportunityMarketScanMaxRetries {
			run.RetryCount++
			run.ErrorMessage = discovery.ErrorMessage
			run.NextRetryAt = time.Now().Add([]time.Duration{opportunityMarketScanFirstRetryDelay, opportunityMarketScanLastRetryDelay}[run.RetryCount-1])
			_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
			return
		}
		s.failOpportunityMarketScan(ctx, run, errors.New(discovery.ErrorMessage), false)
		return
	}
	if discovery.Status != OpportunityDiscoveryRunStatusCompleted {
		return
	}
	oppCandidates, err := s.store.ListOpportunityCandidates(ctx, OpportunityCandidateListFilter{RunID: discovery.ID, Limit: opportunityMarketScanFinalLimit})
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, true)
		return
	}
	scanCandidates, _ := s.store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Limit: opportunityMarketScanLocalLimit})
	scanBySymbol := map[string]*OpportunityMarketScanCandidate{}
	for i := range scanCandidates {
		scanBySymbol[scanCandidates[i].Symbol] = &scanCandidates[i]
	}
	var eligible []OpportunityCandidate
	for _, candidate := range oppCandidates {
		if item := scanBySymbol[candidate.Symbol]; item != nil {
			item.Stage, item.OpportunityCandidateID = OpportunityMarketScanCandidateFinal, candidate.ID
		}
		if candidate.EvidenceScore >= 55 && candidate.Confidence >= .55 {
			eligible = append(eligible, candidate)
		}
	}
	for i := range scanCandidates {
		if scanCandidates[i].Stage == OpportunityMarketScanCandidateResearch {
			scanCandidates[i].Stage = OpportunityMarketScanCandidateReviewedOut
			if scanCandidates[i].ExclusionReason == "" {
				scanCandidates[i].ExclusionReason = "Agent 复核未入选"
			}
		}
	}
	_ = s.store.UpsertOpportunityMarketScanCandidates(ctx, scanCandidates)
	run.FinalCandidateCount = len(oppCandidates)
	if len(eligible) == 0 {
		run.Status, run.FinishedAt, run.ErrorMessage = OpportunityMarketScanStatusCompleted, time.Now(), ""
		_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
		s.updateOpportunityMarketScanConfigFromRun(ctx, run)
		return
	}
	if len(eligible) > opportunityMarketScanStrategyLimit {
		eligible = eligible[:opportunityMarketScanStrategyLimit]
	}
	for _, candidate := range eligible {
		if item := scanBySymbol[candidate.Symbol]; item != nil {
			item.StrategyStatus = OpportunityMarketScanStrategyPending
		}
	}
	agentRun, err := s.startOpportunityMarketStrategyGeneration(ctx, run, eligible)
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, true)
		return
	}
	for i := range eligible {
		eligible[i].Status = OpportunityCandidateStatusStrategyRequested
		if eligible[i].Metadata == nil {
			eligible[i].Metadata = map[string]any{}
		}
		eligible[i].Metadata["strategyGenerationRunId"] = agentRun.ID
		_, _ = s.store.UpdateOpportunityCandidate(ctx, eligible[i])
	}
	_ = s.store.UpsertOpportunityMarketScanCandidates(ctx, scanCandidates)
	run.Status, run.StrategyAgentRunID, run.StrategyRequestedCount = OpportunityMarketScanStatusDrafting, agentRun.ID, len(eligible)
	run.RetryCount, run.NextRetryAt, run.ErrorMessage = 0, time.Time{}, ""
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
}

func (s *Service) advanceOpportunityMarketDrafting(ctx context.Context, run OpportunityMarketScanRun) {
	agentRun, err := s.store.GetAgentRun(ctx, run.StrategyAgentRunID)
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, true)
		return
	}
	if agentRun.Status == AgentRunStatusFailed {
		if !run.NextRetryAt.IsZero() {
			items, listErr := s.store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Limit: opportunityMarketScanLocalLimit})
			if listErr != nil {
				s.failOpportunityMarketScan(ctx, run, listErr, false)
				return
			}
			var opportunityCandidates []OpportunityCandidate
			for _, item := range items {
				if item.StrategyStatus != OpportunityMarketScanStrategyPending || item.OpportunityCandidateID == "" {
					continue
				}
				candidate, candidateErr := s.store.GetOpportunityCandidate(ctx, item.OpportunityCandidateID)
				if candidateErr == nil {
					opportunityCandidates = append(opportunityCandidates, candidate)
				}
			}
			next, startErr := s.startOpportunityMarketStrategyGeneration(ctx, run, opportunityCandidates)
			if startErr != nil {
				s.failOpportunityMarketScan(ctx, run, startErr, false)
				return
			}
			run.StrategyAgentRunID, run.NextRetryAt, run.ErrorMessage = next.ID, time.Time{}, ""
			_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
			return
		}
		if run.RetryCount < opportunityMarketScanMaxRetries {
			run.RetryCount++
			run.ErrorMessage = agentRun.ErrorMessage
			run.NextRetryAt = time.Now().Add([]time.Duration{opportunityMarketScanFirstRetryDelay, opportunityMarketScanLastRetryDelay}[run.RetryCount-1])
			_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
			return
		}
		run.Status, run.FinishedAt = OpportunityMarketScanStatusPartial, time.Now()
		_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
		s.updateOpportunityMarketScanConfigFromRun(ctx, run)
		return
	}
	if agentRun.Status != AgentRunStatusCompleted {
		return
	}
	ledger, err := s.store.GetAgentDecisionLedger(ctx, agentRun.DecisionLedgerID)
	if err != nil {
		s.failOpportunityMarketScan(ctx, run, err, true)
		return
	}
	created := sliceFromAny(ledger.StructuredOutput["createdStrategies"])
	createdBySymbol := map[string]string{}
	for _, raw := range created {
		item := mapFromAny(raw)
		createdBySymbol[stringFromAny(item["symbol"])] = stringFromAny(item["id"])
	}
	candidates, _ := s.store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Limit: opportunityMarketScanLocalLimit})
	for i := range candidates {
		if id := createdBySymbol[candidates[i].Symbol]; id != "" {
			candidates[i].StrategyStatus, candidates[i].StrategyID = OpportunityMarketScanStrategyGenerated, id
		} else if candidates[i].StrategyStatus == OpportunityMarketScanStrategyPending {
			candidates[i].StrategyStatus = OpportunityMarketScanStrategySkipped
		}
	}
	_ = s.store.UpsertOpportunityMarketScanCandidates(ctx, candidates)
	run.StrategyCreatedCount = len(created)
	run.Status, run.FinishedAt, run.ErrorMessage = OpportunityMarketScanStatusCompleted, time.Now(), ""
	_, _ = s.store.UpdateOpportunityMarketScanRun(ctx, run)
	s.updateOpportunityMarketScanConfigFromRun(ctx, run)
}

func (s *Service) startOpportunityMarketStrategyGeneration(ctx context.Context, run OpportunityMarketScanRun, candidates []OpportunityCandidate) (AgentRun, error) {
	if len(candidates) == 0 {
		return AgentRun{}, ErrInvalidStrategyGenerationInput
	}
	if len(candidates) > opportunityMarketScanStrategyLimit {
		candidates = candidates[:opportunityMarketScanStrategyLimit]
	}
	s.refreshOpportunityMarketStrategyQuotes(ctx, candidates)
	input := StrategyGenerationInput{
		SchemaVersion: StrategyGenerationInputSchemaVersion,
		Mode:          StrategyGenerationModeOpportunity,
		UserGoal:      "为经过证据复核的主板机会生成未激活建仓策略草案；条件必须可由现有数据面策略监控校验。",
		RequestedBy:   run.RequestedBy,
		OpportunityID: run.OpportunityID,
		AllowedActions: []string{
			StrategyGenerationRuleActionObserve,
			StrategyGenerationRuleActionBuildPosition,
		},
		EvidenceScope: map[string]bool{"stockProfile": true, "recentNews": true, "quote": true, "dailyBars": true, "opportunity": true},
	}
	for _, candidate := range candidates {
		input.CandidateIDs = append(input.CandidateIDs, candidate.ID)
		input.TargetInstruments = append(input.TargetInstruments, StrategyGenerationTargetInstrument{
			Symbol: candidate.Symbol, Market: candidate.Market, Name: candidate.Name, UserNote: candidate.Reason,
		})
	}
	return s.RunStrategyGeneration(ctx, input)
}

func (s *Service) refreshOpportunityMarketStrategyQuotes(ctx context.Context, candidates []OpportunityCandidate) {
	symbols := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if symbol := strings.TrimSpace(candidate.Symbol); symbol != "" {
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 {
		return
	}
	// ponytail: refresh only the at-most-three strategy targets here. The wider
	// scan quote batch is scoring evidence and is not a substitute for persisted,
	// timestamped executable-price context used by strategy generation and MCP.
	_, _ = s.RefreshLatestQuotes(ctx, symbols, "opportunity-market-scan")
}

func (s *Service) RetryOpportunityMarketScanRun(ctx context.Context, id, requestedBy string) (OpportunityMarketScanRun, error) {
	s.opportunityScanStartMu.Lock()
	defer s.opportunityScanStartMu.Unlock()
	run, err := s.store.GetOpportunityMarketScanRun(ctx, strings.TrimSpace(id))
	if err != nil {
		return OpportunityMarketScanRun{}, err
	}
	if opportunityMarketScanStatusActive(run.Status) {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanInvalidState
	}
	if active, err := s.store.GetActiveOpportunityMarketScanRun(ctx); err != nil {
		return OpportunityMarketScanRun{}, err
	} else if active != nil {
		return OpportunityMarketScanRun{}, ErrOpportunityMarketScanAlreadyRunning
	}
	run.Status, run.RequestedBy, run.RetryCount = OpportunityMarketScanStatusPending, strings.TrimSpace(requestedBy), 0
	run.NextRetryAt, run.FinishedAt, run.ErrorMessage = time.Time{}, time.Time{}, ""
	run.DiscoveryRunID, run.StrategyAgentRunID = "", ""
	run.PrefilterCount, run.EnrichedCount, run.ResearchCount = 0, 0, 0
	run.FinalCandidateCount, run.StrategyRequestedCount, run.StrategyCreatedCount = 0, 0, 0
	run, err = s.store.UpdateOpportunityMarketScanRun(ctx, run)
	if err == nil {
		s.launchOpportunityMarketScanWorker(run.ID)
	}
	return run, err
}
