package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

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
			holdingNews, holdingLinks, truncated, err := s.portfolioSentinelHoldingNews(ctx, holding, run, cfg, selectedEventIDs, &selectedEvents)
			if err != nil {
				return PortfolioSentinelContext{}, err
			}
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
	return out, nil
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

func (s *Service) portfolioSentinelHoldingNews(
	ctx context.Context,
	holding StockV2Holding,
	run PortfolioSentinelRun,
	cfg PortfolioSentinelConfig,
	selectedEventIDs map[string]struct{},
	selectedEvents *[]NewsEvent,
) ([]NewsEvent, []NewsLinkCandidate, bool, error) {
	perHoldingLimit := cfg.MaxNewsPerHolding
	if perHoldingLimit <= 0 {
		perHoldingLimit = 50
	}
	candidates, err := s.store.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{
		Symbol: holding.Symbol,
		Market: holding.Market,
		Since:  run.WindowStartAt,
		Until:  run.WindowEndAt,
		Limit:  perHoldingLimit,
	})
	if err != nil {
		return nil, nil, false, err
	}
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
			return nil, nil, false, err
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
	return events, links, truncated, nil
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
	result := PortfolioSentinelResult{
		RunID:          run.ID,
		SchemaVersion:  report.SchemaVersion,
		Summary:        firstNonEmpty(report.RunSummary, submitted.ResultSummary),
		RiskLevel:      normalizePortfolioSentinelRiskLevel(report.OverallRiskLevel),
		RawResult:      submitted.Result,
		ContextSummary: map[string]any{"confidence": submitted.Confidence},
	}
	derived, err := s.derivePortfolioSentinelObjects(ctx, run, report, submitted.Result)
	if err != nil {
		run, _ = s.store.UpdatePortfolioSentinelRun(ctx, markPortfolioSentinelRunFailed(run, err))
		return PortfolioSentinelResult{}, err
	}
	result.DerivedAlertIDs = derived.alertIDs
	result.DerivedMonitorHitIDs = derived.hitIDs
	result.DerivedReviewIDs = derived.reviewIDs
	result, err = s.store.CreatePortfolioSentinelResult(ctx, result)
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
