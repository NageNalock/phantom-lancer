package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/storage"
)

type Service struct {
	store         *storage.Store
	client        *http.Client
	agentExecutor AgentExecutor
	now           func() time.Time
	bgMu          sync.Mutex
	bgCancel      context.CancelFunc
	bgWg          sync.WaitGroup
}

type MarketClock struct {
	Market         string `json:"market"`
	Timezone       string `json:"timezone"`
	Now            string `json:"now"`
	TradingDay     bool   `json:"tradingDay"`
	CalendarStatus string `json:"calendarStatus"`
	ActiveSession  bool   `json:"activeSession"`
	Session        string `json:"session"`
	NextActionHint string `json:"nextActionHint"`
}

type Snapshot struct {
	Summary             storage.StockDashboardSummary     `json:"summary"`
	DataHealth          storage.StockDataHealthSummary    `json:"dataHealth"`
	AgentTrace          storage.StockAgentTraceSummary    `json:"agentTrace"`
	MarketClock         MarketClock                       `json:"marketClock"`
	Portfolios          []PortfolioWithHoldings           `json:"portfolios"`
	Quotes              []storage.StockQuote              `json:"quotes"`
	DataSources         []storage.StockDataSource         `json:"dataSources"`
	DataAdapters        []StockDataAdapterStatus          `json:"dataAdapters"`
	Instruments         []storage.StockInstrument         `json:"instruments"`
	MarketData          []storage.StockMarketDataPoint    `json:"marketData"`
	DataCoverage        []storage.StockDataCoverage       `json:"dataCoverage"`
	NewsItems           []storage.StockNewsItem           `json:"newsItems"`
	DataTasks           []storage.StockDataTask           `json:"dataTasks"`
	Opportunities       []storage.StockOpportunity        `json:"opportunities"`
	Strategies          []storage.StockStrategy           `json:"strategies"`
	Watches             []storage.StockWatch              `json:"watches"`
	Alerts              []storage.StockAlert              `json:"alerts"`
	Reviews             []storage.StockReview             `json:"reviews"`
	TradeSignals        []storage.StockTradeSignal        `json:"tradeSignals"`
	ProposedOperations  []storage.StockProposedOperation  `json:"proposedOperations"`
	Operations          []storage.StockOperation          `json:"operations"`
	Memories            []storage.StockMemory             `json:"memories"`
	AgentProfiles       []storage.StockAgentModelProfile  `json:"agentProfiles"`
	AgentRuns           []storage.StockAgentRun           `json:"agentRuns"`
	AgentAuthorizations []storage.StockAgentAuthorization `json:"agentAuthorizations"`
	AgentSteps          []storage.StockAgentRunStep       `json:"agentSteps"`
	AgentClaims         []storage.StockAgentClaim         `json:"agentClaims"`
	StrategyPatches     []storage.StockStrategyPatch      `json:"strategyPatches"`
}

type PortfolioWithHoldings struct {
	storage.StockPortfolio
	Holdings         []storage.StockHolding `json:"holdings"`
	MarketValue      float64                `json:"marketValue"`
	TotalAssetValue  float64                `json:"totalAssetValue"`
	CashPct          float64                `json:"cashPct"`
	ConstraintStatus string                 `json:"constraintStatus"`
}

type WatchCheckResult struct {
	MarketClock MarketClock          `json:"marketClock"`
	Checked     int                  `json:"checked"`
	Skipped     int                  `json:"skipped"`
	Alerts      []storage.StockAlert `json:"alerts"`
	Notes       []string             `json:"notes"`
}

type ReviewResult struct {
	Review            storage.StockReview             `json:"review"`
	TradeSignal       *storage.StockTradeSignal       `json:"tradeSignal,omitempty"`
	ProposedOperation *storage.StockProposedOperation `json:"proposedOperation,omitempty"`
	AgentRun          *storage.StockAgentRun          `json:"agentRun,omitempty"`
	StrategyPatch     *storage.StockStrategyPatch     `json:"strategyPatch,omitempty"`
}

type DataTaskResult struct {
	Task          storage.StockDataTask          `json:"task"`
	Sources       []storage.StockDataSource      `json:"sources,omitempty"`
	Instruments   []storage.StockInstrument      `json:"instruments,omitempty"`
	Quotes        []storage.StockQuote           `json:"quotes,omitempty"`
	MarketData    []storage.StockMarketDataPoint `json:"marketData,omitempty"`
	NewsItems     []storage.StockNewsItem        `json:"newsItems,omitempty"`
	Opportunities []storage.StockOpportunity     `json:"opportunities,omitempty"`
	Alerts        []storage.StockAlert           `json:"alerts,omitempty"`
	Notes         []string                       `json:"notes,omitempty"`
}

type DataMaintenanceResult struct {
	Tasks         []storage.StockDataTask        `json:"tasks"`
	Sources       []storage.StockDataSource      `json:"sources,omitempty"`
	Quotes        []storage.StockQuote           `json:"quotes,omitempty"`
	MarketData    []storage.StockMarketDataPoint `json:"marketData,omitempty"`
	NewsItems     []storage.StockNewsItem        `json:"newsItems,omitempty"`
	Opportunities []storage.StockOpportunity     `json:"opportunities,omitempty"`
	Alerts        []storage.StockAlert           `json:"alerts,omitempty"`
	Notes         []string                       `json:"notes,omitempty"`
}

type AgentAuthorizationDecisionResult struct {
	Authorization storage.StockAgentAuthorization `json:"authorization"`
	Run           *storage.StockAgentRun          `json:"run,omitempty"`
	Step          *storage.StockAgentRunStep      `json:"step,omitempty"`
}

type reviewTraceResult struct {
	AgentRun      *storage.StockAgentRun
	StrategyPatch *storage.StockStrategyPatch
}

const (
	stockQuoteUserAgent               = "PhantomLancerStockWorkbench/0.1"
	stockQuoteMinRateLimitSeconds     = 30
	stockQuoteMaxFailureBackoff       = 15 * time.Minute
	stockQuoteFailureSummaryMaxLength = 500
)

type ServiceOption func(*Service)

func WithAgentExecutor(executor AgentExecutor) ServiceOption {
	return func(s *Service) {
		s.agentExecutor = executor
	}
}

func NewService(store *storage.Store, opts ...ServiceOption) *Service {
	service := &Service{store: store, client: &http.Client{Timeout: 5 * time.Second}, now: time.Now}
	if store != nil {
		service.agentExecutor = NewCodexCLIExecutor(store)
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *Service) StartBackground(ctx context.Context) {
	s.bgMu.Lock()
	if s.bgCancel != nil {
		s.bgMu.Unlock()
		return
	}
	bgCtx, cancel := context.WithCancel(ctx)
	s.bgCancel = cancel
	s.bgWg.Add(1)
	s.bgMu.Unlock()

	go func() {
		defer s.bgWg.Done()
		s.runDataMaintenancePass(bgCtx)
		healthTicker := time.NewTicker(6 * time.Hour)
		defer healthTicker.Stop()
		watchTicker := time.NewTicker(30 * time.Second)
		defer watchTicker.Stop()
		for {
			select {
			case <-bgCtx.Done():
				return
			case <-healthTicker.C:
				s.runDataMaintenancePass(bgCtx)
			case <-watchTicker.C:
				s.runWatchSchedulerPass(bgCtx)
			}
		}
	}()
}

func (s *Service) Close() {
	s.bgMu.Lock()
	cancel := s.bgCancel
	s.bgCancel = nil
	s.bgMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	done := make(chan struct{})
	go func() {
		s.bgWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func (s *Service) runDataMaintenancePass(ctx context.Context) {
	passCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = s.RunDataMaintenance(passCtx, "background_data_scheduler")
	_, _ = s.CleanupAgentLedger(passCtx, 30, 500)
}

func (s *Service) runWatchSchedulerPass(ctx context.Context) {
	passCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = s.store.WakeSnoozedStockAlerts(passCtx, s.now().UTC())
	_, _ = s.RecordQuoteRefreshStatus(passCtx, "background_watch_scheduler")
	_, _ = s.CheckWatches(passCtx, false)
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	summary, err := s.store.StockDashboardSummary(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	portfolios, err := s.store.ListStockPortfolios(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	portfolioViews := make([]PortfolioWithHoldings, 0, len(portfolios))
	for _, portfolio := range portfolios {
		holdings, err := s.store.ListStockHoldings(ctx, portfolio.ID)
		if err != nil {
			return Snapshot{}, err
		}
		view := PortfolioWithHoldings{StockPortfolio: portfolio, Holdings: holdings}
		for i := range view.Holdings {
			view.MarketValue += view.Holdings[i].MarketValue
		}
		view.TotalAssetValue = portfolio.Cash + view.MarketValue
		if view.TotalAssetValue > 0 {
			view.CashPct = portfolio.Cash / view.TotalAssetValue
			for i := range view.Holdings {
				view.Holdings[i].PositionPct = view.Holdings[i].MarketValue / view.TotalAssetValue
				if portfolio.MaxSinglePositionPct > 0 && view.Holdings[i].PositionPct > portfolio.MaxSinglePositionPct {
					view.ConstraintStatus = "single_position_exceeded"
				}
			}
		}
		if view.ConstraintStatus == "" {
			view.ConstraintStatus = "ok"
		}
		portfolioViews = append(portfolioViews, view)
	}
	quotes, err := s.store.ListStockQuotes(ctx, 120)
	if err != nil {
		return Snapshot{}, err
	}
	dataHealth, err := s.store.StockDataHealthSummary(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	agentTrace, err := s.store.StockAgentTraceSummary(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	dataSources, err := s.store.ListStockDataSources(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	dataAdapters := StockDataAdapterStatuses()
	instruments, err := s.store.ListStockInstruments(ctx, 160)
	if err != nil {
		return Snapshot{}, err
	}
	marketData, err := s.store.ListStockMarketDataPoints(ctx, "", "", 160)
	if err != nil {
		return Snapshot{}, err
	}
	coverage, err := s.store.StockDataCoverage(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}
	newsItems, err := s.store.ListStockNewsItems(ctx, "", "", 160)
	if err != nil {
		return Snapshot{}, err
	}
	dataTasks, err := s.store.ListStockDataTasks(ctx, 120)
	if err != nil {
		return Snapshot{}, err
	}
	opportunities, err := s.store.ListStockOpportunities(ctx, "", 120)
	if err != nil {
		return Snapshot{}, err
	}
	strategies, err := s.store.ListStockStrategies(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	watches, err := s.store.ListStockWatches(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	alerts, err := s.store.ListStockAlerts(ctx, "", 120)
	if err != nil {
		return Snapshot{}, err
	}
	reviews, err := s.store.ListStockReviews(ctx, 120)
	if err != nil {
		return Snapshot{}, err
	}
	signals, err := s.store.ListStockTradeSignals(ctx, 120)
	if err != nil {
		return Snapshot{}, err
	}
	proposals, err := s.store.ListStockProposedOperations(ctx, "", 120)
	if err != nil {
		return Snapshot{}, err
	}
	operations, err := s.store.ListStockOperations(ctx, 120)
	if err != nil {
		return Snapshot{}, err
	}
	memories, err := s.store.ListStockMemories(ctx, 120)
	if err != nil {
		return Snapshot{}, err
	}
	agentProfiles, err := s.store.ListStockAgentModelProfiles(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	agentRuns, err := s.store.ListStockAgentRuns(ctx, 80)
	if err != nil {
		return Snapshot{}, err
	}
	agentAuthorizations, err := s.store.ListStockAgentAuthorizations(ctx, "", 80)
	if err != nil {
		return Snapshot{}, err
	}
	agentSteps, err := s.store.ListStockAgentRunSteps(ctx, "", 240)
	if err != nil {
		return Snapshot{}, err
	}
	agentClaims, err := s.store.ListStockAgentClaims(ctx, "", 240)
	if err != nil {
		return Snapshot{}, err
	}
	strategyPatches, err := s.store.ListStockStrategyPatches(ctx, "", 80)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Summary:             summary,
		DataHealth:          dataHealth,
		AgentTrace:          agentTrace,
		MarketClock:         s.MarketClock(),
		Portfolios:          portfolioViews,
		Quotes:              quotes,
		DataSources:         dataSources,
		DataAdapters:        dataAdapters,
		Instruments:         instruments,
		MarketData:          marketData,
		DataCoverage:        coverage,
		NewsItems:           newsItems,
		DataTasks:           dataTasks,
		Opportunities:       opportunities,
		Strategies:          strategies,
		Watches:             watches,
		Alerts:              alerts,
		Reviews:             reviews,
		TradeSignals:        signals,
		ProposedOperations:  proposals,
		Operations:          operations,
		Memories:            memories,
		AgentProfiles:       agentProfiles,
		AgentRuns:           agentRuns,
		AgentAuthorizations: agentAuthorizations,
		AgentSteps:          agentSteps,
		AgentClaims:         agentClaims,
		StrategyPatches:     strategyPatches,
	}, nil
}

func (s *Service) MarketClock() MarketClock {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	now := s.now().In(loc)
	tradingDay, calendarStatus := aShareTradingDay(now)
	minute := now.Hour()*60 + now.Minute()
	session := "closed"
	active := false
	switch {
	case !tradingDay:
		session = "holiday_or_weekend"
	case minute >= 9*60+15 && minute < 9*60+30:
		session = "call_auction"
	case minute >= 9*60+30 && minute < 11*60+30:
		session = "continuous_morning"
		active = true
	case minute >= 11*60+30 && minute < 13*60:
		session = "lunch_break"
	case minute >= 13*60 && minute < 15*60:
		session = "continuous_afternoon"
		active = true
	case minute >= 15*60 && minute < 15*60+30:
		session = "post_close"
	default:
		session = "closed"
	}
	hint := "非交易时段，只建议做人工检查或消息面整理"
	if active {
		hint = "A 股连续竞价时段，可执行价格类盯盘检查"
	} else if session == "call_auction" {
		hint = "集合竞价时段，价格信号需谨慎处理"
	}
	return MarketClock{
		Market:         "A-share",
		Timezone:       "Asia/Shanghai",
		Now:            now.Format(time.RFC3339Nano),
		TradingDay:     tradingDay,
		CalendarStatus: calendarStatus,
		ActiveSession:  active,
		Session:        session,
		NextActionHint: hint,
	}
}

func aShareTradingDay(t time.Time) (bool, string) {
	date := t.Format("2006-01-02")
	if dates := aShareClosedDates[t.Year()]; dates != nil {
		if dates[date] {
			return false, "exchange_calendar"
		}
		if isWeekday(t) {
			return true, "exchange_calendar"
		}
		return false, "exchange_calendar"
	}
	if isWeekday(t) {
		return true, "weekday_fallback"
	}
	return false, "weekday_fallback"
}

func isWeekday(t time.Time) bool {
	weekday := t.Weekday()
	return weekday >= time.Monday && weekday <= time.Friday
}

var aShareClosedDates = map[int]map[string]bool{
	2026: dateSet([]string{
		"2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04",
		"2026-02-14", "2026-02-15", "2026-02-16", "2026-02-17", "2026-02-18", "2026-02-19", "2026-02-20", "2026-02-21", "2026-02-22", "2026-02-23", "2026-02-28",
		"2026-04-04", "2026-04-05", "2026-04-06",
		"2026-05-01", "2026-05-02", "2026-05-03", "2026-05-04", "2026-05-05", "2026-05-09",
		"2026-06-19", "2026-06-20", "2026-06-21",
		"2026-09-20", "2026-09-25", "2026-09-26", "2026-09-27",
		"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06", "2026-10-07", "2026-10-10",
	}),
}

func dateSet(dates []string) map[string]bool {
	set := make(map[string]bool, len(dates))
	for _, date := range dates {
		set[date] = true
	}
	return set
}

func (s *Service) UpsertAgentModelProfile(ctx context.Context, profile storage.StockAgentModelProfile) (storage.StockAgentModelProfile, error) {
	return s.store.UpsertStockAgentModelProfile(ctx, profile)
}

func (s *Service) AcceptStrategyPatch(ctx context.Context, id string) (storage.StockStrategy, storage.StockStrategyPatch, error) {
	return s.store.ApplyStockStrategyPatch(ctx, id)
}

func (s *Service) RejectStrategyPatch(ctx context.Context, id string) (storage.StockStrategyPatch, error) {
	return s.store.UpdateStockStrategyPatchStatus(ctx, id, "rejected")
}

func (s *Service) ApproveAgentAuthorization(ctx context.Context, id string) (AgentAuthorizationDecisionResult, error) {
	auth, err := s.store.GetStockAgentAuthorization(ctx, id)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	if auth.Status != "pending" {
		return AgentAuthorizationDecisionResult{}, errors.New("agent authorization is not pending")
	}
	run, err := s.store.GetStockAgentRun(ctx, auth.RunID)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	profile, err := s.store.GetStockAgentModelProfile(ctx, auth.ProfileID)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	auth, err = s.store.UpdateStockAgentAuthorizationStatus(ctx, auth.ID, "approved", "approved", "")
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	_, _ = s.store.UpdateStockAgentRunStepStatus(ctx, run.ID, "agent_authorization", "completed", "用户已确认执行外部 Agent executor", mustJSON(map[string]any{"status": "approved", "authorization_id": auth.ID}))
	executorResult, executorAttempted := s.executeReviewAgentInput(ctx, profile, AgentExecutionInput{
		Profile:                 profile,
		TaskType:                defaultString(auth.TaskType, "review"),
		Protocol:                defaultString(auth.DecisionProtocol, run.DecisionProtocol),
		ReviewID:                auth.ReviewID,
		AlertID:                 run.AlertID,
		StrategyID:              run.StrategyID,
		Symbol:                  run.Symbol,
		Prompt:                  auth.PromptSnapshot,
		InputJSON:               auth.InputSnapshot,
		DeterministicOutputJSON: auth.OutputSnapshot,
	})
	if !executorAttempted {
		auth, _ = s.store.UpdateStockAgentAuthorizationStatus(ctx, auth.ID, "failed", "approved", "agent executor was not attempted")
		return AgentAuthorizationDecisionResult{Authorization: auth, Run: &run}, errors.New("agent executor was not attempted")
	}
	step, err := s.store.CreateStockAgentRunStep(ctx, storage.StockAgentRunStep{
		RunID:         run.ID,
		StepKey:       defaultString(executorResult.StepKey, "agent_executor"),
		Role:          defaultString(executorResult.Role, profile.Provider),
		Status:        defaultString(executorResult.Status, "failed"),
		InputJSON:     defaultString(executorResult.InputJSON, "{}"),
		OutputJSON:    defaultString(executorResult.OutputJSON, "{}"),
		ToolCallsJSON: defaultString(executorResult.ToolCallsJSON, "[]"),
		LatencyMs:     executorResult.LatencyMs,
		TokenEstimate: executorResult.TokenEstimate,
		Summary:       executorResult.Summary,
	})
	if err != nil {
		auth, _ = s.store.UpdateStockAgentAuthorizationStatus(ctx, auth.ID, "failed", "approved", err.Error())
		return AgentAuthorizationDecisionResult{Authorization: auth, Run: &run}, err
	}
	authStatus := "completed"
	if step.Status == "failed" {
		authStatus = "failed"
	}
	auth, err = s.store.UpdateStockAgentAuthorizationStatus(ctx, auth.ID, authStatus, "approved", executorResult.ErrorSummary)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	costSummary := mustJSON(map[string]any{
		"mode":              "agent_executor_after_stock_authorization",
		"executor_provider": profile.Provider,
		"executor_model":    profile.Model,
		"executor_status":   step.Status,
		"estimated_tokens":  step.TokenEstimate,
		"estimated_cost":    float64(step.TokenEstimate) / 1000 * storage.StockAgentEstimatedCostPerThousandTokens,
	})
	outputSnapshot := mustJSON(map[string]any{
		"system_guardrails_output": auth.OutputSnapshot,
		"agent_executor_output":    executorResult.OutputSnapshot,
		"agent_executor_status":    step.Status,
	})
	updatedRun, err := s.store.UpdateStockAgentRunExecution(ctx, run.ID, "completed", outputSnapshot, costSummary, run.Summary)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	graphJSON := updateRunGraphForAgentAuthorization(updatedRun.RunGraphJSON, auth.Status, &step)
	if err := s.store.UpdateStockAgentRunGraph(ctx, updatedRun.ID, graphJSON); err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	updatedRun.RunGraphJSON = graphJSON
	return AgentAuthorizationDecisionResult{Authorization: auth, Run: &updatedRun, Step: &step}, nil
}

func (s *Service) DenyAgentAuthorization(ctx context.Context, id string, reason string) (AgentAuthorizationDecisionResult, error) {
	auth, err := s.store.GetStockAgentAuthorization(ctx, id)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	if auth.Status != "pending" {
		return AgentAuthorizationDecisionResult{}, errors.New("agent authorization is not pending")
	}
	run, err := s.store.GetStockAgentRun(ctx, auth.RunID)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	auth, err = s.store.UpdateStockAgentAuthorizationStatus(ctx, auth.ID, "denied", "denied", reason)
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	step, _ := s.store.UpdateStockAgentRunStepStatus(ctx, run.ID, "agent_authorization", "denied", "用户拒绝执行外部 Agent executor", mustJSON(map[string]any{"status": "denied", "authorization_id": auth.ID, "reason": reason}))
	updatedRun, err := s.store.UpdateStockAgentRunExecution(ctx, run.ID, "authorization_denied", "", "", "用户拒绝执行外部 Agent executor，系统 guardrails 输出继续作为本次 Review 结果")
	if err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	graphJSON := updateRunGraphForAgentAuthorization(updatedRun.RunGraphJSON, auth.Status, nil)
	if err := s.store.UpdateStockAgentRunGraph(ctx, updatedRun.ID, graphJSON); err != nil {
		return AgentAuthorizationDecisionResult{}, err
	}
	updatedRun.RunGraphJSON = graphJSON
	result := AgentAuthorizationDecisionResult{Authorization: auth, Run: &updatedRun}
	if step.ID != "" {
		result.Step = &step
	}
	return result, nil
}

func (s *Service) CleanupAgentLedger(ctx context.Context, retentionDays, keepRuns int) (storage.StockAgentLedgerCleanupResult, error) {
	return s.store.CleanupStockAgentLedger(ctx, retentionDays, keepRuns)
}

func (s *Service) CreateStrategyFromOpportunity(ctx context.Context, opportunityID string, req storage.StockStrategy) (storage.StockOpportunity, storage.StockStrategy, error) {
	opportunity, err := s.store.GetStockOpportunity(ctx, opportunityID)
	if err != nil {
		return storage.StockOpportunity{}, storage.StockStrategy{}, err
	}
	if req.Title == "" {
		req.Title = opportunity.Title
	}
	req.Symbol = defaultString(req.Symbol, opportunity.Symbol)
	req.Market = defaultString(req.Market, opportunity.Market)
	req.Name = defaultString(req.Name, opportunity.Name)
	req.Source = "opportunity"
	if req.Thesis == "" {
		req.Thesis = opportunity.Thesis
	}
	if req.RiskNotes == "" {
		req.RiskNotes = opportunity.EvidenceSummary
	}
	created, err := s.store.CreateStockStrategy(ctx, req)
	if err != nil {
		return storage.StockOpportunity{}, storage.StockStrategy{}, err
	}
	linked, err := s.store.LinkStockOpportunityStrategy(ctx, opportunity.ID, created.ID)
	if err != nil {
		return storage.StockOpportunity{}, storage.StockStrategy{}, err
	}
	if _, err := s.store.CreateStockMemory(ctx, storage.StockMemory{
		Symbol:     opportunity.Symbol,
		ObjectType: "opportunity_strategy",
		ObjectID:   created.ID,
		Summary:    fmt.Sprintf("机会 %s 已生成策略 %s", opportunity.Title, created.Title),
	}); err != nil {
		return storage.StockOpportunity{}, storage.StockStrategy{}, err
	}
	return linked, created, nil
}

func (s *Service) CheckWatches(ctx context.Context, force bool) (WatchCheckResult, error) {
	_, _ = s.store.WakeSnoozedStockAlerts(ctx, s.now().UTC())
	clock := s.MarketClock()
	result := WatchCheckResult{MarketClock: clock}
	if !force && !clock.ActiveSession {
		result.Notes = append(result.Notes, "当前不是 A 股连续竞价时段，未执行价格类盯盘")
		return result, nil
	}
	watches, err := s.store.ListActiveStockWatches(ctx)
	if err != nil {
		return result, err
	}
	for _, watch := range watches {
		if !force && !watchDueForCheck(watch, s.now()) {
			result.Skipped++
			continue
		}
		result.Checked++
		quote, err := s.store.GetStockQuote(ctx, watch.Symbol)
		if err != nil {
			result.Skipped++
			result.Notes = append(result.Notes, fmt.Sprintf("%s 缺少行情快照", watch.Symbol))
			continue
		}
		if !quoteUsableForOperation(quote, s.now(), 15*time.Minute) {
			result.Skipped++
			result.Notes = append(result.Notes, fmt.Sprintf("%s 行情不可用于强触发: %s/%s", watch.Symbol, quote.DataFreshness, quote.TradableStatus))
			_ = s.store.TouchStockWatch(ctx, watch.ID)
			continue
		}
		triggered, reason := watchTriggered(watch, quote)
		_ = s.store.TouchStockWatch(ctx, watch.ID)
		if !triggered {
			continue
		}
		dedupeKey := watchAlertDedupeKey(watch, reason)
		exists, err := s.store.OpenStockAlertExists(ctx, dedupeKey)
		if err != nil {
			return result, err
		}
		if exists {
			result.Skipped++
			continue
		}
		cooldownUntil := s.now().Add(time.Duration(watch.CooldownSeconds) * time.Second).Format(time.RFC3339Nano)
		alert, err := s.store.CreateStockAlert(ctx, storage.StockAlert{
			WatchID:       watch.ID,
			StrategyID:    watch.StrategyID,
			PortfolioID:   watch.PortfolioID,
			Symbol:        watch.Symbol,
			Market:        watch.Market,
			Name:          firstNonEmpty(watch.Name, quote.Name),
			Level:         "strong",
			Status:        "new",
			SourceType:    "market_data",
			SourceRefID:   quote.Symbol,
			DedupeKey:     dedupeKey,
			CooldownUntil: cooldownUntil,
			Title:         fmt.Sprintf("%s 触发盯盘", watch.Symbol),
			Summary:       fmt.Sprintf("最新价 %.3f，%s", quote.LastPrice, reason),
			TriggerReason: reason,
		})
		if err != nil {
			return result, err
		}
		result.Alerts = append(result.Alerts, alert)
	}
	return result, nil
}

func (s *Service) UpsertDataSource(ctx context.Context, src storage.StockDataSource) (storage.StockDataSource, error) {
	if src.Status == "" || src.Quality == "" {
		status, quality, reason := sourceProbeResult(src)
		src.Status = defaultString(src.Status, status)
		src.Quality = defaultString(src.Quality, quality)
		src.FailureSummary = defaultString(src.FailureSummary, reason)
	}
	if src.AuthMode == "" {
		src.AuthMode = "none"
	}
	if src.RateLimitSeconds <= 0 {
		src.RateLimitSeconds = 60
	}
	return s.store.UpsertStockDataSource(ctx, src)
}

func (s *Service) RunDataSourceHealthCheck(ctx context.Context, source string) (DataTaskResult, error) {
	var sources []storage.StockDataSource
	var err error
	if strings.TrimSpace(source) == "" {
		sources, err = s.store.ListStockDataSources(ctx)
	} else {
		src, getErr := s.ensureDataSource(ctx, source, "market_data")
		if getErr != nil {
			return DataTaskResult{}, getErr
		}
		sources = []storage.StockDataSource{src}
	}
	if err != nil {
		return DataTaskResult{}, err
	}
	if len(sources) == 0 {
		src, err := s.ensureDataSource(ctx, "manual_seed", "market_data")
		if err != nil {
			return DataTaskResult{}, err
		}
		sources = []storage.StockDataSource{src}
	}
	checked := make([]storage.StockDataSource, 0, len(sources))
	failed := 0
	for _, src := range sources {
		status, quality, reason := sourceProbeResult(src)
		if status == "failed" || status == "disabled" || status == "auth_required" {
			failed++
			src.ConsecutiveFailures++
		} else {
			src.ConsecutiveFailures = 0
		}
		src.Status = status
		src.Quality = quality
		src.FailureSummary = reason
		updated, err := s.store.UpdateStockDataSourceHealth(ctx, src)
		if err != nil {
			return DataTaskResult{}, err
		}
		checked = append(checked, updated)
	}
	status := "completed"
	if failed > 0 && failed == len(checked) {
		status = "failed"
	} else if failed > 0 {
		status = "degraded"
	}
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "source_health_check",
		Source:         source,
		Status:         status,
		RequestedBy:    "system",
		InputJSON:      mustJSON(map[string]any{"source": source}),
		ResultJSON:     mustJSON(map[string]any{"checked": len(checked), "failed": failed}),
		ProcessedCount: len(checked),
		FailedCount:    failed,
		FailureSummary: failureSummary(failed, "部分数据源需要授权配置或已禁用"),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	return DataTaskResult{Task: task, Sources: checked}, nil
}

func (s *Service) RecordQuoteRefreshStatus(ctx context.Context, requestedBy string) (DataTaskResult, error) {
	symbols, err := s.quoteRefreshSymbols(ctx)
	if err != nil {
		return DataTaskResult{}, err
	}
	if len(symbols) > 0 {
		quotes, source, notes, nextRunAt, err := s.fetchManagedPublicQuotes(ctx, symbols)
		if err == nil && len(quotes) > 0 {
			var saved []storage.StockQuote
			failed := 0
			for _, quote := range quotes {
				created, saveErr := s.store.UpsertStockQuote(ctx, quote)
				if saveErr != nil {
					failed++
					notes = append(notes, saveErr.Error())
					continue
				}
				saved = append(saved, created)
			}
			status := taskStatus(len(saved), failed)
			if nextRunAt == "" {
				nextRunAt = s.nextQuoteProviderRunAt(ctx, source)
			}
			task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
				TaskType:       "quote_refresh",
				Source:         source,
				Status:         status,
				RequestedBy:    defaultString(requestedBy, "system"),
				InputJSON:      mustJSON(map[string]any{"symbols": symbols}),
				ResultJSON:     mustJSON(map[string]any{"refreshed": len(saved), "failed": failed, "notes": notes}),
				ProcessedCount: len(saved),
				FailedCount:    failed,
				FailureSummary: strings.Join(notes, "; "),
				NextRunAt:      nextRunAt,
			})
			if err != nil {
				return DataTaskResult{}, err
			}
			_, _ = s.recordQuoteProviderSuccess(ctx, source, task.CompletedAt, status, notes)
			return DataTaskResult{Task: task, Quotes: saved, Notes: notes}, nil
		}
		if err != nil {
			return s.recordBlockedQuoteRefreshWithSource(ctx, requestedBy, "public_quote_providers", "public quote refresh failed or rate limited: "+err.Error(), nextRunAt)
		}
	}
	return s.recordBlockedQuoteRefresh(ctx, requestedBy, "没有可刷新的盯盘股票；盯盘使用最近公开 provider 或手工/外部写入快照")
}

func (s *Service) recordBlockedQuoteRefresh(ctx context.Context, requestedBy, reason string) (DataTaskResult, error) {
	return s.recordBlockedQuoteRefreshWithSource(ctx, requestedBy, "manual_seed", reason, "")
}

func (s *Service) recordBlockedQuoteRefreshWithSource(ctx context.Context, requestedBy, source, reason, nextRunAt string) (DataTaskResult, error) {
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "quote_refresh",
		Source:         source,
		Status:         "blocked",
		RequestedBy:    defaultString(requestedBy, "system"),
		InputJSON:      mustJSON(map[string]any{"mode": "manual_snapshot"}),
		ResultJSON:     mustJSON(map[string]any{"refreshed": 0, "reason": reason}),
		FailureSummary: reason,
		NextRunAt:      nextRunAt,
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	return DataTaskResult{Task: task, Notes: []string{task.FailureSummary}}, nil
}

type publicQuoteProvider struct {
	source string
	fetch  func(context.Context, []string) ([]storage.StockQuote, []string, error)
}

func (s *Service) fetchManagedPublicQuotes(ctx context.Context, symbols []string) ([]storage.StockQuote, string, []string, string, error) {
	providers := []publicQuoteProvider{
		{source: "eastmoney_public_quote", fetch: s.fetchEastmoneyQuotes},
		{source: "sina_public_quote", fetch: s.fetchSinaQuotes},
	}
	var problems []string
	var nextAllowed []time.Time
	for _, provider := range providers {
		src, ready, reason, next, err := s.quoteProviderReady(ctx, provider.source)
		if err != nil {
			return nil, "", nil, "", err
		}
		if !ready {
			problems = append(problems, provider.source+": "+reason)
			if !next.IsZero() {
				nextAllowed = append(nextAllowed, next)
			}
			continue
		}
		quotes, notes, err := provider.fetch(ctx, symbols)
		if err != nil || len(quotes) == 0 {
			failure := "provider returned no quotes"
			if err != nil {
				failure = err.Error()
			}
			updated, updateErr := s.recordQuoteProviderFailure(ctx, src, failure)
			if updateErr == nil {
				if next, ok := parseStockTime(updated.NextAllowedAt); ok {
					nextAllowed = append(nextAllowed, next)
				}
			}
			problems = append(problems, provider.source+": "+failure)
			continue
		}
		return quotes, provider.source, notes, "", nil
	}
	if len(problems) == 0 {
		problems = append(problems, "no public quote provider available")
	}
	return nil, "", nil, earliestTimeString(nextAllowed), errors.New(strings.Join(problems, "; "))
}

func (s *Service) quoteProviderReady(ctx context.Context, source string) (storage.StockDataSource, bool, string, time.Time, error) {
	src, err := s.ensureDataSource(ctx, source, "market_data")
	if err != nil {
		return storage.StockDataSource{}, false, "", time.Time{}, err
	}
	if !src.Enabled || strings.EqualFold(src.AuthMode, "disabled") || strings.EqualFold(src.Status, "disabled") {
		return src, false, "data source disabled", time.Time{}, nil
	}
	if next, ok := parseStockTime(src.NextAllowedAt); ok && next.After(s.now().UTC()) {
		return src, false, "next allowed at " + next.Format(time.RFC3339Nano), next, nil
	}
	return src, true, "", time.Time{}, nil
}

func (s *Service) recordQuoteProviderSuccess(ctx context.Context, source, completedAt, taskStatus string, notes []string) (storage.StockDataSource, error) {
	src, err := s.ensureDataSource(ctx, source, "market_data")
	if err != nil {
		return storage.StockDataSource{}, err
	}
	base := s.now().UTC()
	if parsed, ok := parseStockTime(completedAt); ok {
		base = parsed.UTC()
	}
	status := "available"
	if taskStatus == "degraded" {
		status = "degraded"
	} else if taskStatus == "failed" {
		status = "failed"
	}
	return s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
		Source:              src.Source,
		Status:              status,
		Quality:             mapQuoteRefreshQuality(taskStatus),
		LastIngestedAt:      completedAt,
		NextAllowedAt:       base.Add(quoteProviderRateLimit(src.RateLimitSeconds)).Format(time.RFC3339Nano),
		ConsecutiveFailures: 0,
		FailureSummary:      limitText(strings.Join(notes, "; "), stockQuoteFailureSummaryMaxLength),
	})
}

func (s *Service) nextQuoteProviderRunAt(ctx context.Context, source string) string {
	src, err := s.ensureDataSource(ctx, source, "market_data")
	if err != nil {
		return ""
	}
	return s.now().UTC().Add(quoteProviderRateLimit(src.RateLimitSeconds)).Format(time.RFC3339Nano)
}

func (s *Service) recordQuoteProviderFailure(ctx context.Context, src storage.StockDataSource, reason string) (storage.StockDataSource, error) {
	failures := src.ConsecutiveFailures + 1
	status := "degraded"
	quality := "partial"
	if failures >= 3 {
		status = "failed"
		quality = "failed"
	}
	return s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
		Source:              src.Source,
		Status:              status,
		Quality:             quality,
		NextAllowedAt:       s.now().UTC().Add(quoteProviderBackoff(src.RateLimitSeconds, failures)).Format(time.RFC3339Nano),
		ConsecutiveFailures: failures,
		FailureSummary:      limitText(reason, stockQuoteFailureSummaryMaxLength),
	})
}

func quoteProviderRateLimit(rateLimitSeconds int) time.Duration {
	if rateLimitSeconds < stockQuoteMinRateLimitSeconds {
		rateLimitSeconds = stockQuoteMinRateLimitSeconds
	}
	return time.Duration(rateLimitSeconds) * time.Second
}

func quoteProviderBackoff(rateLimitSeconds, failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	shift := failures - 1
	if shift > 4 {
		shift = 4
	}
	backoff := quoteProviderRateLimit(rateLimitSeconds) * time.Duration(1<<shift)
	if backoff > stockQuoteMaxFailureBackoff {
		return stockQuoteMaxFailureBackoff
	}
	return backoff
}

func parseStockTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func earliestTimeString(values []time.Time) string {
	var earliest time.Time
	for _, value := range values {
		if value.IsZero() {
			continue
		}
		if earliest.IsZero() || value.Before(earliest) {
			earliest = value
		}
	}
	if earliest.IsZero() {
		return ""
	}
	return earliest.UTC().Format(time.RFC3339Nano)
}

func (s *Service) fetchEastmoneyQuotes(ctx context.Context, symbols []string) ([]storage.StockQuote, []string, error) {
	secids := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		secids = append(secids, eastmoneySecID(symbol))
	}
	url := "https://push2.eastmoney.com/api/qt/ulist.np/get?fltt=2&fields=f12,f13,f14,f2,f3,f4,f5,f6,f17,f18,f20,f21,f124,f297&secids=" + strings.Join(secids, ",")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("User-Agent", stockQuoteUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("eastmoney quote status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, nil, err
	}
	return parseEastmoneyQuoteResponse(body, s.now()), nil, nil
}

func eastmoneySecID(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	prefix := "1"
	if strings.HasPrefix(symbol, "0") || strings.HasPrefix(symbol, "3") {
		prefix = "0"
	} else if strings.HasPrefix(symbol, "8") || strings.HasPrefix(symbol, "4") {
		prefix = "0"
	}
	return prefix + "." + symbol
}

func parseEastmoneyQuoteResponse(body []byte, now time.Time) []storage.StockQuote {
	var payload struct {
		Data struct {
			Diff []struct {
				Symbol        string  `json:"f12"`
				MarketCode    int     `json:"f13"`
				Name          string  `json:"f14"`
				LastPrice     float64 `json:"f2"`
				Volume        float64 `json:"f5"`
				Amount        float64 `json:"f6"`
				PreviousClose float64 `json:"f18"`
				Timestamp     int64   `json:"f124"`
			} `json:"diff"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	var quotes []storage.StockQuote
	for _, item := range payload.Data.Diff {
		if item.Symbol == "" || item.LastPrice <= 0 {
			continue
		}
		quoteTime := now
		if item.Timestamp > 0 {
			quoteTime = time.Unix(item.Timestamp, 0)
		}
		market := "SH"
		if item.MarketCode == 0 {
			market = "SZ"
		}
		quotes = append(quotes, storage.StockQuote{
			Symbol:         item.Symbol,
			Market:         market,
			Name:           item.Name,
			LastPrice:      item.LastPrice,
			PreviousClose:  item.PreviousClose,
			Volume:         item.Volume,
			Amount:         item.Amount,
			DataTimestamp:  quoteTime.Format(time.RFC3339Nano),
			DataFreshness:  "fresh",
			TradableStatus: "tradable",
		})
	}
	return quotes
}

func (s *Service) quoteRefreshSymbols(ctx context.Context) ([]string, error) {
	watches, err := s.store.ListActiveStockWatches(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var symbols []string
	for _, watch := range watches {
		if watch.Symbol == "" || seen[watch.Symbol] {
			continue
		}
		seen[watch.Symbol] = true
		symbols = append(symbols, watch.Symbol)
	}
	return symbols, nil
}

func (s *Service) fetchSinaQuotes(ctx context.Context, symbols []string) ([]storage.StockQuote, []string, error) {
	codes := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		market := "sh"
		if strings.HasPrefix(symbol, "0") || strings.HasPrefix(symbol, "3") {
			market = "sz"
		} else if strings.HasPrefix(symbol, "8") || strings.HasPrefix(symbol, "4") {
			market = "bj"
		}
		codes = append(codes, market+strings.ToLower(symbol))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://hq.sinajs.cn/list="+strings.Join(codes, ","), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn/")
	req.Header.Set("User-Agent", stockQuoteUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("sina quote status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, nil, err
	}
	return parseSinaQuoteResponse(string(body), s.now()), nil, nil
}

func parseSinaQuoteResponse(body string, now time.Time) []storage.StockQuote {
	var quotes []storage.StockQuote
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=\"") {
			continue
		}
		prefix, payload, _ := strings.Cut(line, "=\"")
		payload = strings.TrimSuffix(payload, "\";")
		parts := strings.Split(payload, ",")
		if len(parts) < 32 || parts[0] == "" {
			continue
		}
		rawCode := prefix[strings.LastIndex(prefix, "_")+1:]
		symbol := strings.ToUpper(strings.TrimLeft(rawCode, "abcdefghijklmnopqrstuvwxyz"))
		lastPrice := parseFloat(parts[3])
		if lastPrice <= 0 {
			continue
		}
		quoteTime := now
		if parts[30] != "" && parts[31] != "" {
			if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", parts[30]+" "+parts[31], time.FixedZone("Asia/Shanghai", 8*3600)); err == nil {
				quoteTime = parsed
			}
		}
		market := strings.ToUpper(rawCode[:2])
		quotes = append(quotes, storage.StockQuote{
			Symbol:         symbol,
			Market:         market,
			Name:           parts[0],
			LastPrice:      lastPrice,
			PreviousClose:  parseFloat(parts[2]),
			Volume:         parseFloat(parts[8]),
			Amount:         parseFloat(parts[9]),
			DataTimestamp:  quoteTime.Format(time.RFC3339Nano),
			DataFreshness:  "fresh",
			TradableStatus: "tradable",
		})
	}
	return quotes
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func mapQuoteRefreshQuality(status string) string {
	switch status {
	case "completed":
		return "fresh"
	case "degraded":
		return "partial"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

func (s *Service) RefreshInstruments(ctx context.Context, source string, items []storage.StockInstrument) (DataTaskResult, error) {
	src, err := s.ensureDataSource(ctx, source, "market_data")
	if err != nil {
		return DataTaskResult{}, err
	}
	var saved []storage.StockInstrument
	var notes []string
	failed := 0
	for _, item := range items {
		item.Source = src.Source
		created, err := s.store.UpsertStockInstrument(ctx, item)
		if err != nil {
			failed++
			notes = append(notes, err.Error())
			continue
		}
		saved = append(saved, created)
	}
	status := taskStatus(len(saved), failed)
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "refresh_instruments",
		Source:         src.Source,
		Status:         status,
		RequestedBy:    "system",
		InputJSON:      mustJSON(map[string]any{"count": len(items), "source": src.Source}),
		ResultJSON:     mustJSON(map[string]any{"saved": len(saved), "failed": failed}),
		ProcessedCount: len(saved),
		FailedCount:    failed,
		FailureSummary: strings.Join(notes, "; "),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	if failed == 0 {
		_, _ = s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
			Source:         src.Source,
			Status:         "available",
			Quality:        "fresh",
			LastIngestedAt: task.CompletedAt,
		})
	}
	return DataTaskResult{Task: task, Instruments: saved, Notes: notes}, nil
}

func (s *Service) BackfillMarketData(ctx context.Context, source string, points []storage.StockMarketDataPoint) (DataTaskResult, error) {
	src, err := s.ensureDataSource(ctx, source, "market_data")
	if err != nil {
		return DataTaskResult{}, err
	}
	var saved []storage.StockMarketDataPoint
	var notes []string
	failed := 0
	for _, point := range points {
		point.Source = src.Source
		created, _, err := s.store.UpsertStockMarketDataPoint(ctx, point)
		if err != nil {
			failed++
			notes = append(notes, err.Error())
			continue
		}
		saved = append(saved, created)
	}
	status := taskStatus(len(saved), failed)
	symbol := ""
	if len(points) == 1 {
		symbol = points[0].Symbol
	}
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "market_data_backfill",
		Source:         src.Source,
		Symbol:         symbol,
		Status:         status,
		RequestedBy:    "system",
		InputJSON:      mustJSON(map[string]any{"count": len(points), "source": src.Source}),
		ResultJSON:     mustJSON(map[string]any{"saved": len(saved), "failed": failed}),
		ProcessedCount: len(saved),
		FailedCount:    failed,
		FailureSummary: strings.Join(notes, "; "),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	if failed == 0 {
		_, _ = s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
			Source:         src.Source,
			Status:         "available",
			Quality:        "fresh",
			LastIngestedAt: task.CompletedAt,
		})
	}
	return DataTaskResult{Task: task, MarketData: saved, Notes: notes}, nil
}

func (s *Service) IngestNews(ctx context.Context, source string, items []storage.StockNewsItem) (DataTaskResult, error) {
	src, err := s.ensureDataSource(ctx, source, "news")
	if err != nil {
		return DataTaskResult{}, err
	}
	var saved []storage.StockNewsItem
	var created []storage.StockNewsItem
	var notes []string
	failed := 0
	lastCursor := src.LastCursor
	for _, item := range items {
		item.Source = src.Source
		item.Title = limitText(item.Title, 240)
		item.Summary = limitText(item.Summary, 1200)
		item.RawPayload = limitText(item.RawPayload, 4096)
		if item.PublishedAt == "" {
			item.PublishedAt = s.now().Format(time.RFC3339Nano)
		}
		stored, isNew, err := s.store.UpsertStockNewsItem(ctx, item)
		if err != nil {
			failed++
			notes = append(notes, err.Error())
			continue
		}
		saved = append(saved, stored)
		if isNew {
			created = append(created, stored)
		}
		if stored.PublishedAt > lastCursor {
			lastCursor = stored.PublishedAt
		}
	}
	alerts, err := s.createNewsAlerts(ctx, created)
	if err != nil {
		return DataTaskResult{}, err
	}
	status := taskStatus(len(saved), failed)
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "news_ingestion",
		Source:         src.Source,
		Status:         status,
		RequestedBy:    "system",
		InputJSON:      mustJSON(map[string]any{"count": len(items), "source": src.Source}),
		ResultJSON:     mustJSON(map[string]any{"saved": len(saved), "new": len(created), "alerts": len(alerts), "failed": failed}),
		ProcessedCount: len(saved),
		FailedCount:    failed,
		FailureSummary: strings.Join(notes, "; "),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	if failed == 0 {
		_, _ = s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
			Source:         src.Source,
			Status:         "available",
			Quality:        "fresh",
			LastCursor:     lastCursor,
			LastIngestedAt: task.CompletedAt,
		})
	}
	return DataTaskResult{Task: task, NewsItems: saved, Alerts: alerts, Notes: notes}, nil
}

func (s *Service) ReviewAlert(ctx context.Context, alertID string) (ReviewResult, error) {
	alert, err := s.store.GetStockAlert(ctx, alertID)
	if err != nil {
		return ReviewResult{}, err
	}
	if existing, err := s.store.LatestStockReviewForAlert(ctx, alert.ID); err == nil {
		return s.existingReviewResult(ctx, existing)
	} else if !errors.Is(err, storage.ErrNotFound) {
		return ReviewResult{}, err
	}
	watch, err := s.store.GetStockWatch(ctx, alert.WatchID)
	if err != nil {
		return ReviewResult{}, err
	}
	strategy, err := s.store.GetStockStrategy(ctx, alert.StrategyID)
	if err != nil {
		return ReviewResult{}, err
	}
	quote, quoteErr := s.store.GetStockQuote(ctx, alert.Symbol)
	portfolio, portfolioErr := storage.StockPortfolio{}, error(nil)
	holdings := []storage.StockHolding(nil)
	if strategy.PortfolioID != "" {
		portfolio, portfolioErr = s.store.GetStockPortfolio(ctx, strategy.PortfolioID)
		if portfolioErr == nil {
			holdings, _ = s.store.ListStockHoldings(ctx, strategy.PortfolioID)
		}
	}
	inputProtocol, _ := agentDecisionProtocol(alert, strategy, portfolioErr)
	input := map[string]any{
		"schema_version":    "operation-review-input/v1",
		"source_type":       "watch_trigger",
		"source_ref_id":     alert.ID,
		"instrument":        map[string]any{"symbol": alert.Symbol, "market": alert.Market, "name": alert.Name},
		"strategy_snapshot": strategy,
		"trigger_context":   alert,
		"decision_protocol": inputProtocol,
	}
	if quoteErr == nil {
		input["market_snapshot"] = quote
	} else {
		input["data_quality"] = map[string]any{"quote": "missing"}
	}
	if portfolioErr == nil && strategy.PortfolioID != "" {
		input["portfolio_snapshot"] = portfolio
		input["holding_snapshot"] = holdings
	}
	inputJSON := mustJSON(input)
	if strategy.StrategyType == "account_agnostic" || strategy.PortfolioID == "" {
		summary := fmt.Sprintf("%s 触发账户无关交易信号，需绑定账户后才能生成操作建议", strategy.Symbol)
		review, err := s.createReviewSkeleton(ctx, alert, watch, strategy, inputJSON)
		if err != nil {
			return ReviewResult{}, err
		}
		review, err = s.store.UpdateStockReviewState(ctx, review.ID, "evidence_checking", "", "", "已读取触发上下文，正在核对行情和策略证据", "", false)
		if err != nil {
			return ReviewResult{}, err
		}
		output := map[string]any{
			"schema_version":       "operation-review-report/v1",
			"review_result":        "trade_signal",
			"summary":              summary,
			"confidence":           "medium",
			"data_quality_summary": dataQualitySummary(quote, quoteErr),
			"watch_action":         "continue_watch",
		}
		augmentReviewOutput(output, alert, strategy, quote, quoteErr, portfolio, portfolioErr, holdings, nil, "账户无关策略只输出 trade_signal，绑定账户前不生成可执行操作")
		review, err = s.store.UpdateStockReviewState(ctx, review.ID, "completed", "trade_signal", "not_applicable", summary, mustJSON(output), true)
		if err != nil {
			return ReviewResult{}, err
		}
		signal, err := s.store.CreateStockTradeSignal(ctx, storage.StockTradeSignal{
			ReviewID:       review.ID,
			StrategyID:     strategy.ID,
			Symbol:         strategy.Symbol,
			Market:         strategy.Market,
			Name:           strategy.Name,
			Direction:      normalizeDirection(strategy.Direction),
			PriceRange:     priceRange(strategy),
			TriggerSummary: alert.Summary,
			StopLoss:       strategy.StopLoss,
			TakeProfit:     strategy.TakeProfit,
			Status:         "active",
		})
		if err != nil {
			return ReviewResult{}, err
		}
		trace, err := s.createReviewTrace(ctx, alert, watch, strategy, quote, quoteErr, portfolio, portfolioErr, holdings, review, output, &signal, nil)
		if err != nil {
			return ReviewResult{}, err
		}
		_, _ = s.store.UpdateStockAlertStatus(ctx, alert.ID, "resolved")
		return ReviewResult{Review: review, TradeSignal: &signal, AgentRun: trace.AgentRun, StrategyPatch: trace.StrategyPatch}, nil
	}
	if portfolioErr != nil {
		return ReviewResult{}, portfolioErr
	}
	review, err := s.createReviewSkeleton(ctx, alert, watch, strategy, inputJSON)
	if err != nil {
		return ReviewResult{}, err
	}
	review, err = s.store.UpdateStockReviewState(ctx, review.ID, "evidence_checking", "", "", "已读取触发上下文，正在核对行情、账户和持仓证据", "", false)
	if err != nil {
		return ReviewResult{}, err
	}
	review, err = s.store.UpdateStockReviewState(ctx, review.ID, "guardrail_checking", "", "", "证据核对完成，正在执行账户约束检查", "", false)
	if err != nil {
		return ReviewResult{}, err
	}
	guarded := s.proposeOperation(ctx, strategy, portfolio, holdings, quote, quoteErr)
	output := map[string]any{
		"schema_version":       "operation-review-report/v1",
		"review_result":        guarded.reviewResult,
		"summary":              guarded.summary,
		"confidence":           guarded.confidence,
		"data_quality_summary": dataQualitySummary(quote, quoteErr),
		"guardrail_result":     guarded.guardrailResult,
		"watch_action":         "continue_watch",
	}
	augmentReviewOutput(output, alert, strategy, quote, quoteErr, portfolio, portfolioErr, holdings, guarded.operation, guarded.summary)
	review, err = s.store.UpdateStockReviewState(ctx, review.ID, "completed", guarded.reviewResult, guarded.guardrailResult, guarded.summary, mustJSON(output), true)
	if err != nil {
		return ReviewResult{}, err
	}
	var proposal *storage.StockProposedOperation
	if guarded.operation != nil {
		guarded.operation.ReviewID = review.ID
		created, err := s.store.CreateStockProposedOperation(ctx, *guarded.operation)
		if err != nil {
			return ReviewResult{}, err
		}
		proposal = &created
	}
	trace, err := s.createReviewTrace(ctx, alert, watch, strategy, quote, quoteErr, portfolio, portfolioErr, holdings, review, output, nil, proposal)
	if err != nil {
		return ReviewResult{}, err
	}
	nextAlertStatus := "resolved"
	if proposal != nil && proposal.Status == "pending_confirmation" {
		nextAlertStatus = "acknowledged"
	}
	_, _ = s.store.UpdateStockAlertStatus(ctx, alert.ID, nextAlertStatus)
	return ReviewResult{Review: review, ProposedOperation: proposal, AgentRun: trace.AgentRun, StrategyPatch: trace.StrategyPatch}, nil
}

func (s *Service) existingReviewResult(ctx context.Context, review storage.StockReview) (ReviewResult, error) {
	result := ReviewResult{Review: review}
	if signal, err := s.store.StockTradeSignalForReview(ctx, review.ID); err == nil {
		result.TradeSignal = &signal
	} else if !errors.Is(err, storage.ErrNotFound) {
		return ReviewResult{}, err
	}
	if op, err := s.store.StockProposedOperationForReview(ctx, review.ID); err == nil {
		result.ProposedOperation = &op
	} else if !errors.Is(err, storage.ErrNotFound) {
		return ReviewResult{}, err
	}
	if run, err := s.store.StockAgentRunForReview(ctx, review.ID); err == nil {
		result.AgentRun = &run
	} else if !errors.Is(err, storage.ErrNotFound) {
		return ReviewResult{}, err
	}
	if patch, err := s.store.StockStrategyPatchForReview(ctx, review.ID); err == nil {
		result.StrategyPatch = &patch
	} else if !errors.Is(err, storage.ErrNotFound) {
		return ReviewResult{}, err
	}
	return result, nil
}

func (s *Service) createReviewSkeleton(ctx context.Context, alert storage.StockAlert, watch storage.StockWatch, strategy storage.StockStrategy, inputJSON string) (storage.StockReview, error) {
	review, err := s.store.CreateStockReview(ctx, storage.StockReview{
		AlertID:         alert.ID,
		WatchID:         watch.ID,
		StrategyID:      strategy.ID,
		PortfolioID:     strategy.PortfolioID,
		Symbol:          strategy.Symbol,
		Market:          strategy.Market,
		Name:            strategy.Name,
		Status:          "context_building",
		ReviewResult:    "pending",
		InputJSON:       inputJSON,
		OutputJSON:      "{}",
		GuardrailResult: "pending",
		Summary:         "正在构建 Review 上下文",
	})
	if err != nil {
		return storage.StockReview{}, err
	}
	return review, nil
}

func (s *Service) createReviewTrace(ctx context.Context, alert storage.StockAlert, watch storage.StockWatch, strategy storage.StockStrategy, quote storage.StockQuote, quoteErr error, portfolio storage.StockPortfolio, portfolioErr error, holdings []storage.StockHolding, review storage.StockReview, output map[string]any, signal *storage.StockTradeSignal, proposal *storage.StockProposedOperation) (reviewTraceResult, error) {
	protocol, taskType := agentDecisionProtocol(alert, strategy, portfolioErr)
	profile, err := s.store.SelectStockAgentModelProfile(ctx, taskType, protocol)
	if errors.Is(err, storage.ErrNotFound) && (taskType != "review" || protocol != "single_review") {
		profile, err = s.store.SelectStockAgentModelProfile(ctx, "review", "single_review")
	}
	if err != nil {
		return reviewTraceResult{}, err
	}
	templates := reviewTraceStepTemplates(protocol, strategy.PortfolioID != "" && portfolioErr == nil)
	estimatedTokens := 0
	for _, template := range templates {
		estimatedTokens += template.tokenEstimate
	}
	promptSnapshot := stockReviewPrompt(protocol)
	var executorResult AgentExecutionResult
	executorAttempted := false
	pendingAuthorization := profile.Provider != "system" && profile.AuthMode == "confirm_required"
	if !pendingAuthorization {
		executorResult, executorAttempted = s.executeReviewAgent(ctx, profile, taskType, protocol, review, alert, strategy, promptSnapshot)
	}
	if executorAttempted {
		estimatedTokens += executorResult.TokenEstimate
		if executorResult.Prompt != "" {
			promptSnapshot = executorResult.Prompt
		}
	}
	estimatedCost := float64(estimatedTokens) / 1000 * storage.StockAgentEstimatedCostPerThousandTokens
	costSummary := map[string]any{"mode": "deterministic_system_rule", "estimated_tokens": estimatedTokens, "estimated_cost": estimatedCost}
	outputSnapshot := review.OutputJSON
	runStatus := "completed"
	if pendingAuthorization {
		runStatus = "pending_authorization"
		costSummary["mode"] = "pending_stock_authorization"
		costSummary["executor_provider"] = profile.Provider
		costSummary["executor_model"] = profile.Model
		costSummary["confirmation_required"] = true
	}
	if executorAttempted {
		costSummary["mode"] = "agent_executor_with_system_guardrails"
		costSummary["executor_provider"] = profile.Provider
		costSummary["executor_status"] = executorResult.Status
		if executorResult.ErrorSummary != "" {
			costSummary["executor_error"] = executorResult.ErrorSummary
		}
		outputSnapshot = mustJSON(map[string]any{
			"system_guardrails_output": review.OutputJSON,
			"agent_executor_output":    executorResult.OutputSnapshot,
			"agent_executor_status":    executorResult.Status,
		})
	}
	run, err := s.store.CreateStockAgentRun(ctx, storage.StockAgentRun{
		TriggerSource:     alert.SourceType,
		TriggerObjectType: "stock_alert",
		TriggerObjectID:   alert.ID,
		StrategyID:        strategy.ID,
		PortfolioID:       strategy.PortfolioID,
		WatchID:           watch.ID,
		AlertID:           alert.ID,
		ReviewID:          review.ID,
		Symbol:            strategy.Symbol,
		DecisionProtocol:  protocol,
		Status:            runStatus,
		Result:            review.ReviewResult,
		Confidence:        outputString(output, "confidence", "medium"),
		ModelProfileID:    profile.ID,
		Provider:          profile.Provider,
		Model:             profile.Model,
		PromptSnapshot:    promptSnapshot,
		InputSnapshot:     review.InputJSON,
		OutputSnapshot:    outputSnapshot,
		RunGraphJSON:      "{}",
		SkillSnapshotJSON: reviewSkillSnapshot(),
		ToolSnapshotJSON:  reviewToolSnapshot(),
		CostSummaryJSON:   mustJSON(costSummary),
		Summary:           review.Summary,
		RedactionSummary:  "prompt/input/output snapshots are line-redacted and size-capped before storage",
	})
	if err != nil {
		return reviewTraceResult{}, err
	}
	stepIDs := map[string]string{}
	stepStatuses := map[string]string{}
	for _, template := range templates {
		step, err := s.store.CreateStockAgentRunStep(ctx, storage.StockAgentRunStep{
			RunID:         run.ID,
			StepKey:       template.key,
			Role:          template.role,
			Status:        "completed",
			InputJSON:     mustJSON(map[string]any{"review_id": review.ID, "alert_id": alert.ID, "strategy_id": strategy.ID, "protocol": protocol}),
			OutputJSON:    mustJSON(map[string]any{"summary": template.summary, "review_result": review.ReviewResult, "guardrail_result": review.GuardrailResult}),
			ToolCallsJSON: mustJSON(template.tools),
			LatencyMs:     template.latencyMs,
			TokenEstimate: template.tokenEstimate,
			Summary:       template.summary,
		})
		if err != nil {
			return reviewTraceResult{}, err
		}
		stepIDs[step.StepKey] = step.ID
		stepStatuses[step.StepKey] = step.Status
	}
	if pendingAuthorization {
		auth, err := s.store.CreateStockAgentAuthorization(ctx, storage.StockAgentAuthorization{
			RunID:            run.ID,
			ReviewID:         review.ID,
			ProfileID:        profile.ID,
			TaskType:         taskType,
			DecisionProtocol: protocol,
			Provider:         profile.Provider,
			Model:            profile.Model,
			Symbol:           strategy.Symbol,
			Status:           "pending",
			Reason:           "该 profile 标记为 confirm_required，股票模块需要用户确认后才会调用外部 Agent executor。",
			PromptSnapshot:   promptSnapshot,
			InputSnapshot:    review.InputJSON,
			OutputSnapshot:   review.OutputJSON,
			RequestedBy:      "stock_review",
		})
		if err != nil {
			return reviewTraceResult{}, err
		}
		step, err := s.store.CreateStockAgentRunStep(ctx, storage.StockAgentRunStep{
			RunID:         run.ID,
			StepKey:       "agent_authorization",
			Role:          profile.Provider,
			Status:        "pending",
			InputJSON:     mustJSON(map[string]any{"review_id": review.ID, "authorization_id": auth.ID, "profile_id": profile.ID, "provider": profile.Provider, "model": profile.Model}),
			OutputJSON:    mustJSON(map[string]any{"status": "pending", "authorization_id": auth.ID, "reason": auth.Reason}),
			ToolCallsJSON: "[]",
			Summary:       "等待用户在股票模块确认后执行外部 Agent executor",
		})
		if err != nil {
			return reviewTraceResult{}, err
		}
		stepIDs[step.StepKey] = step.ID
		stepStatuses[step.StepKey] = step.Status
	}
	if executorAttempted {
		step, err := s.store.CreateStockAgentRunStep(ctx, storage.StockAgentRunStep{
			RunID:         run.ID,
			StepKey:       executorResult.StepKey,
			Role:          executorResult.Role,
			Status:        executorResult.Status,
			InputJSON:     defaultString(executorResult.InputJSON, "{}"),
			OutputJSON:    defaultString(executorResult.OutputJSON, "{}"),
			ToolCallsJSON: defaultString(executorResult.ToolCallsJSON, "[]"),
			LatencyMs:     executorResult.LatencyMs,
			TokenEstimate: executorResult.TokenEstimate,
			Summary:       executorResult.Summary,
		})
		if err != nil {
			return reviewTraceResult{}, err
		}
		stepIDs[step.StepKey] = step.ID
		stepStatuses[step.StepKey] = step.Status
	}
	if err := s.createReviewClaims(ctx, run.ID, stepIDs["evidence_auditor"], alert, strategy, quote, quoteErr, portfolio, portfolioErr, review); err != nil {
		return reviewTraceResult{}, err
	}
	var patch *storage.StockStrategyPatch
	if shouldCreateStrategyPatch(review, signal, proposal) {
		created, err := s.store.CreateStockStrategyPatch(ctx, storage.StockStrategyPatch{
			RunID:      run.ID,
			ReviewID:   review.ID,
			StrategyID: strategy.ID,
			PatchJSON: mustJSON(map[string]any{
				"riskNotesAppend": fmt.Sprintf("Review %s / %s: %s", review.ID, review.ReviewResult, review.Summary),
			}),
			Summary: "Agent Review 建议追加本次复盘摘要，等待人工确认",
			Status:  "pending_acceptance",
		})
		if err != nil {
			return reviewTraceResult{}, err
		}
		patch = &created
	}
	memory, err := s.store.CreateStockMemory(ctx, storage.StockMemory{
		PortfolioID: strategy.PortfolioID,
		Symbol:      strategy.Symbol,
		ObjectType:  "agent_run",
		ObjectID:    run.ID,
		Summary:     fmt.Sprintf("Agent trace %s 记录 %s，结果 %s", protocol, review.Summary, review.ReviewResult),
	})
	if err != nil {
		return reviewTraceResult{}, err
	}
	graphJSON := buildReviewRunGraph(run, alert, watch, strategy, quote, quoteErr, portfolio, portfolioErr, stepIDs, stepStatuses, patch, memory, signal, proposal)
	if err := s.store.UpdateStockAgentRunGraph(ctx, run.ID, graphJSON); err != nil {
		return reviewTraceResult{}, err
	}
	run.RunGraphJSON = graphJSON
	return reviewTraceResult{AgentRun: &run, StrategyPatch: patch}, nil
}

func shouldCreateStrategyPatch(review storage.StockReview, signal *storage.StockTradeSignal, proposal *storage.StockProposedOperation) bool {
	if review.GuardrailResult == "blocked" || review.GuardrailResult == "data_missing" || review.ReviewResult == "ignore" || review.ReviewResult == "degraded" {
		return true
	}
	return false
}

func (s *Service) executeReviewAgent(ctx context.Context, profile storage.StockAgentModelProfile, taskType, protocol string, review storage.StockReview, alert storage.StockAlert, strategy storage.StockStrategy, prompt string) (AgentExecutionResult, bool) {
	return s.executeReviewAgentInput(ctx, profile, AgentExecutionInput{
		Profile:                 profile,
		TaskType:                taskType,
		Protocol:                protocol,
		ReviewID:                review.ID,
		AlertID:                 alert.ID,
		StrategyID:              strategy.ID,
		Symbol:                  strategy.Symbol,
		Prompt:                  prompt,
		InputJSON:               review.InputJSON,
		DeterministicOutputJSON: review.OutputJSON,
	})
}

func (s *Service) executeReviewAgentInput(ctx context.Context, profile storage.StockAgentModelProfile, input AgentExecutionInput) (AgentExecutionResult, bool) {
	if profile.Provider == "system" {
		return AgentExecutionResult{}, false
	}
	input.Profile = profile
	result := AgentExecutionResult{
		StepKey:       "agent_executor",
		Role:          profile.Provider,
		Status:        "failed",
		InputJSON:     mustJSON(map[string]any{"review_id": input.ReviewID, "profile_id": profile.ID, "provider": profile.Provider, "model": profile.Model}),
		OutputJSON:    mustJSON(map[string]any{"status": "failed", "error": "agent executor is not configured"}),
		ToolCallsJSON: "[]",
		Summary:       "非 system profile 已被选中，但 executor 未配置，已回落到 system guardrails 输出",
	}
	if s.agentExecutor == nil {
		_, _ = s.store.UpdateStockAgentModelProfileRuntime(ctx, profile.ID, "degraded", "agent executor is not configured")
		return result, true
	}
	result, err := s.agentExecutor.ExecuteStockReview(ctx, AgentExecutionInput{
		Profile:                 profile,
		TaskType:                input.TaskType,
		Protocol:                input.Protocol,
		ReviewID:                input.ReviewID,
		AlertID:                 input.AlertID,
		StrategyID:              input.StrategyID,
		Symbol:                  input.Symbol,
		Prompt:                  input.Prompt,
		InputJSON:               input.InputJSON,
		DeterministicOutputJSON: input.DeterministicOutputJSON,
	})
	if err != nil {
		if result.StepKey == "" {
			result.StepKey = "agent_executor"
		}
		if result.Role == "" {
			result.Role = profile.Provider
		}
		if result.Status == "" {
			result.Status = "failed"
		}
		if result.Summary == "" {
			result.Summary = "Agent executor 执行失败，已回落到 system guardrails 输出"
		}
		_, _ = s.store.UpdateStockAgentModelProfileRuntime(ctx, profile.ID, "degraded", result.ErrorSummary)
		return result, true
	}
	_, _ = s.store.UpdateStockAgentModelProfileRuntime(ctx, profile.ID, "available", "")
	return result, true
}

func (s *Service) createReviewClaims(ctx context.Context, runID, evidenceStepID string, alert storage.StockAlert, strategy storage.StockStrategy, quote storage.StockQuote, quoteErr error, portfolio storage.StockPortfolio, portfolioErr error, review storage.StockReview) error {
	claims := []storage.StockAgentClaim{
		{
			RunID:              runID,
			StepID:             evidenceStepID,
			ClaimType:          "trigger",
			Text:               "提醒触发了股票 Review: " + firstNonEmpty(alert.TriggerReason, alert.Summary, alert.Title),
			EvidenceJSON:       mustJSON([]map[string]any{{"type": "stock_alert", "id": alert.ID, "source_type": alert.SourceType, "source_ref_id": alert.SourceRefID}}),
			VerificationStatus: "verified",
			Confidence:         "high",
			SourceRef:          "alert:" + alert.ID,
		},
		{
			RunID:              runID,
			StepID:             evidenceStepID,
			ClaimType:          "strategy",
			Text:               fmt.Sprintf("策略 %s 当前方向为 %s，Review 输出为 %s", strategy.Title, strategy.Direction, review.ReviewResult),
			EvidenceJSON:       mustJSON([]map[string]any{{"type": "stock_strategy", "id": strategy.ID, "version": strategy.CurrentVersion}}),
			VerificationStatus: "verified",
			Confidence:         "high",
			SourceRef:          "strategy:" + strategy.ID,
		},
		{
			RunID:              runID,
			StepID:             evidenceStepID,
			ClaimType:          "guardrail",
			Text:               "执行约束检查结果: " + defaultString(review.GuardrailResult, "not_applicable"),
			EvidenceJSON:       mustJSON([]map[string]any{{"type": "stock_review", "id": review.ID, "guardrail_result": review.GuardrailResult}}),
			VerificationStatus: "verified",
			Confidence:         "high",
			SourceRef:          "review:" + review.ID,
		},
	}
	dataStatus := "verified"
	dataConfidence := "high"
	if quoteErr != nil {
		dataStatus = "missing"
		dataConfidence = "low"
	}
	claims = append(claims, storage.StockAgentClaim{
		RunID:              runID,
		StepID:             evidenceStepID,
		ClaimType:          "market_data",
		Text:               dataQualitySummary(quote, quoteErr),
		EvidenceJSON:       mustJSON([]map[string]any{{"type": "stock_quote", "symbol": strategy.Symbol, "freshness": quote.DataFreshness, "tradable_status": quote.TradableStatus}}),
		VerificationStatus: dataStatus,
		Confidence:         dataConfidence,
		SourceRef:          "quote:" + strategy.Symbol,
	})
	if strategy.PortfolioID != "" {
		status := "verified"
		confidence := "high"
		if portfolioErr != nil {
			status = "missing"
			confidence = "low"
		}
		claims = append(claims, storage.StockAgentClaim{
			RunID:              runID,
			StepID:             evidenceStepID,
			ClaimType:          "portfolio",
			Text:               fmt.Sprintf("账户绑定策略使用账户快照 %s，现金 %.2f", firstNonEmpty(portfolio.Name, strategy.PortfolioID), portfolio.Cash),
			EvidenceJSON:       mustJSON([]map[string]any{{"type": "stock_portfolio", "id": strategy.PortfolioID, "cash": portfolio.Cash}}),
			VerificationStatus: status,
			Confidence:         confidence,
			SourceRef:          "portfolio:" + strategy.PortfolioID,
		})
	}
	for _, claim := range claims {
		if _, err := s.store.CreateStockAgentClaim(ctx, claim); err != nil {
			return err
		}
	}
	return nil
}

type reviewTraceStepTemplate struct {
	key           string
	role          string
	summary       string
	tools         []map[string]any
	latencyMs     int
	tokenEstimate int
}

func reviewTraceStepTemplates(protocol string, portfolioBound bool) []reviewTraceStepTemplate {
	steps := []reviewTraceStepTemplate{
		{key: "context_builder", role: "context_builder", summary: "读取提醒、策略、行情、账户快照，构造 operation-review-input/v1", tools: []map[string]any{{"name": "local_stock_store.read", "mode": "snapshot"}}, latencyMs: 20, tokenEstimate: 120},
		{key: "evidence_auditor", role: "evidence_auditor", summary: "核对行情新鲜度、交易状态、策略版本和触发来源", tools: []map[string]any{{"name": "stock_data_quality.check", "mode": "deterministic"}}, latencyMs: 30, tokenEstimate: 180},
	}
	if protocol == "analysis_with_challenge" || protocol == "portfolio_constrained_debate" {
		steps = append(steps,
			reviewTraceStepTemplate{key: "bull_reviewer", role: "bull_reviewer", summary: "记录支持策略方向的结构化理由，不保存隐藏思维链", tools: []map[string]any{{"name": "review_role_projection", "mode": "structured_rationale"}}, latencyMs: 35, tokenEstimate: 220},
			reviewTraceStepTemplate{key: "bear_reviewer", role: "bear_reviewer", summary: "记录反向风险、数据缺口和误触发可能性", tools: []map[string]any{{"name": "risk_challenge_projection", "mode": "structured_rationale"}}, latencyMs: 35, tokenEstimate: 220},
		)
	}
	if portfolioBound {
		steps = append(steps, reviewTraceStepTemplate{key: "portfolio_constraint_reviewer", role: "portfolio_constraint_reviewer", summary: "执行现金、可卖数量、单票上限和交易状态约束检查", tools: []map[string]any{{"name": "execution_guardrails.check", "mode": "deterministic"}}, latencyMs: 25, tokenEstimate: 160})
	}
	steps = append(steps,
		reviewTraceStepTemplate{key: "decision_manager", role: "decision_manager", summary: "归并证据、挑战意见和 guardrails，输出 Review 决策", tools: []map[string]any{{"name": "operation_review_schema.emit", "schema": "operation-review-report/v1"}}, latencyMs: 40, tokenEstimate: 260},
		reviewTraceStepTemplate{key: "report_formatter", role: "report_formatter", summary: "写入 Agent Decision Ledger、运行子图、claim ledger 和策略补丁候选", tools: []map[string]any{{"name": "stock_agent_ledger.write", "mode": "append_only"}}, latencyMs: 20, tokenEstimate: 100},
	)
	return steps
}

func agentDecisionProtocol(alert storage.StockAlert, strategy storage.StockStrategy, portfolioErr error) (string, string) {
	if strategy.PortfolioID != "" && portfolioErr == nil {
		return "portfolio_constrained_debate", "debate"
	}
	if alert.SourceType == "news_item" || alert.Level == "strong" || alert.Level == "urgent" {
		return "analysis_with_challenge", "review"
	}
	return "single_review", "review"
}

func stockReviewPrompt(protocol string) string {
	return strings.Join([]string{
		"股票 Agent Review 审计提示词。",
		"输入为 operation-review-input/v1，输出为 operation-review-report/v1。",
		"必须区分账户无关 trade_signal 和账户绑定 proposed_operation。",
		"策略变更只能生成 strategy_patch pending_acceptance，不能直接改正式策略。",
		"记录结构化依据、证据、反向风险、guardrails 结果和运行子图，不保存隐藏思维链。",
		"当前决策协议: " + protocol,
	}, "\n")
}

func reviewSkillSnapshot() string {
	return mustJSON(map[string]any{
		"a-stock-data": map[string]any{"role": "market_data_skill", "status": "available_when_configured"},
		"local_stock_data": map[string]any{
			"role":     "persisted_stock_context",
			"datasets": []string{"quotes", "instruments", "market_data", "news_items", "portfolios", "strategies", "watches"},
		},
	})
}

func reviewToolSnapshot() string {
	return mustJSON([]map[string]any{
		{"name": "local_stock_store.read", "purpose": "read persisted stock objects"},
		{"name": "codex.exec", "purpose": "optional non-system stock review executor with read-only sandbox"},
		{"name": "stock_data_quality.check", "purpose": "verify freshness and tradable status"},
		{"name": "execution_guardrails.check", "purpose": "block invalid account-bound operation proposals"},
		{"name": "stock_agent_ledger.write", "purpose": "persist audit trail, claims, patches and run graph"},
	})
}

func buildReviewRunGraph(run storage.StockAgentRun, alert storage.StockAlert, watch storage.StockWatch, strategy storage.StockStrategy, quote storage.StockQuote, quoteErr error, portfolio storage.StockPortfolio, portfolioErr error, stepIDs map[string]string, stepStatuses map[string]string, patch *storage.StockStrategyPatch, memory storage.StockMemory, signal *storage.StockTradeSignal, proposal *storage.StockProposedOperation) string {
	type node struct {
		ID     string `json:"id"`
		Label  string `json:"label"`
		Kind   string `json:"kind"`
		Status string `json:"status,omitempty"`
		RefID  string `json:"refId,omitempty"`
	}
	type edge struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label,omitempty"`
	}
	nodes := []node{
		{ID: "strategy", Label: "策略", Kind: "stock_strategy", Status: strategy.Status, RefID: strategy.ID},
		{ID: "watch", Label: "盯盘", Kind: "stock_watch", Status: watch.Status, RefID: watch.ID},
		{ID: "alert", Label: "Alert", Kind: "stock_alert", Status: alert.Status, RefID: alert.ID},
		{ID: "review", Label: "操作 Review", Kind: "stock_review", Status: "completed", RefID: run.ReviewID},
		{ID: "agent_run", Label: "Agent Decision Ledger", Kind: "agent_run", Status: run.Status, RefID: run.ID},
		{ID: "claims", Label: "Claim Ledger", Kind: "claim_ledger", Status: "recorded", RefID: run.ID},
		{ID: "memory", Label: "记忆回流", Kind: "stock_memory", Status: "recorded", RefID: memory.ID},
	}
	if patch != nil {
		nodes = append(nodes, node{ID: "strategy_patch", Label: "Strategy Patch", Kind: "strategy_patch", Status: patch.Status, RefID: patch.ID})
	}
	if alert.SourceType == "news_item" {
		nodes = append(nodes, node{ID: "news", Label: "消息面事件", Kind: "stock_news_item", Status: "matched", RefID: alert.SourceRefID})
	} else {
		status := "available"
		if quoteErr != nil {
			status = "missing"
		} else if quote.DataFreshness != "" {
			status = quote.DataFreshness
		}
		nodes = append(nodes, node{ID: "quote", Label: "行情快照", Kind: "stock_quote", Status: status, RefID: strategy.Symbol})
	}
	if strategy.PortfolioID != "" {
		status := "available"
		if portfolioErr != nil {
			status = "missing"
		}
		nodes = append(nodes, node{ID: "portfolio", Label: "账户/仓位快照", Kind: "stock_portfolio", Status: status, RefID: firstNonEmpty(portfolio.ID, strategy.PortfolioID)})
	}
	stepOrder := []string{"context_builder", "agent_authorization", "agent_executor", "evidence_auditor", "bull_reviewer", "bear_reviewer", "portfolio_constraint_reviewer", "decision_manager", "report_formatter"}
	var presentSteps []string
	for _, key := range stepOrder {
		if id := stepIDs[key]; id != "" {
			presentSteps = append(presentSteps, key)
			nodes = append(nodes, node{ID: "step:" + key, Label: key, Kind: "agent_step", Status: defaultString(stepStatuses[key], "completed"), RefID: id})
		}
	}
	if signal != nil {
		nodes = append(nodes, node{ID: "trade_signal", Label: "trade_signal", Kind: "stock_trade_signal", Status: signal.Status, RefID: signal.ID})
	}
	if proposal != nil {
		nodes = append(nodes, node{ID: "proposed_operation", Label: "proposed_operation", Kind: "stock_proposed_operation", Status: proposal.Status, RefID: proposal.ID})
	}
	edges := []edge{
		{From: "strategy", To: "watch", Label: "create_watch"},
		{From: "watch", To: "alert", Label: "trigger"},
		{From: "alert", To: "review", Label: "review"},
		{From: "strategy", To: "review", Label: "strategy_snapshot"},
		{From: "review", To: "agent_run", Label: "ledger"},
		{From: "agent_run", To: "claims", Label: "claim_audit"},
		{From: "agent_run", To: "memory", Label: "backflow"},
	}
	if patch != nil {
		edges = append(edges, edge{From: "agent_run", To: "strategy_patch", Label: "pending_acceptance"})
	}
	if alert.SourceType == "news_item" {
		edges = append(edges, edge{From: "news", To: "alert", Label: "match"})
	} else {
		edges = append(edges, edge{From: "quote", To: "alert", Label: "price_condition"})
	}
	if strategy.PortfolioID != "" {
		edges = append(edges, edge{From: "portfolio", To: "review", Label: "portfolio_snapshot"})
	}
	for i, key := range presentSteps {
		from := "agent_run"
		if i > 0 {
			from = "step:" + presentSteps[i-1]
		}
		edges = append(edges, edge{From: from, To: "step:" + key, Label: "run_step"})
	}
	if signal != nil {
		edges = append(edges, edge{From: "step:decision_manager", To: "trade_signal", Label: "emit"})
	}
	if proposal != nil {
		edges = append(edges, edge{From: "step:decision_manager", To: "proposed_operation", Label: "emit"})
	}
	graph := map[string]any{
		"schemaVersion":    "stock-agent-run-subgraph/v1",
		"runId":            run.ID,
		"decisionProtocol": run.DecisionProtocol,
		"nodes":            nodes,
		"edges":            edges,
	}
	return mustJSON(graph)
}

func updateRunGraphForAgentAuthorization(graphJSON, authorizationStatus string, executorStep *storage.StockAgentRunStep) string {
	type node struct {
		ID     string `json:"id"`
		Label  string `json:"label"`
		Kind   string `json:"kind"`
		Status string `json:"status,omitempty"`
		RefID  string `json:"refId,omitempty"`
	}
	type edge struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label,omitempty"`
	}
	var graph struct {
		SchemaVersion    string `json:"schemaVersion"`
		RunID            string `json:"runId"`
		DecisionProtocol string `json:"decisionProtocol"`
		Nodes            []node `json:"nodes"`
		Edges            []edge `json:"edges"`
	}
	if err := json.Unmarshal([]byte(graphJSON), &graph); err != nil {
		return graphJSON
	}
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "step:agent_authorization" {
			graph.Nodes[i].Status = authorizationStatus
		}
		if graph.Nodes[i].ID == "agent_run" {
			switch authorizationStatus {
			case "denied":
				graph.Nodes[i].Status = "authorization_denied"
			case "completed", "failed":
				graph.Nodes[i].Status = "completed"
			}
		}
	}
	if executorStep != nil && executorStep.ID != "" {
		replaced := false
		for i := range graph.Nodes {
			if graph.Nodes[i].ID == "step:agent_executor" {
				graph.Nodes[i].Status = executorStep.Status
				graph.Nodes[i].RefID = executorStep.ID
				replaced = true
				break
			}
		}
		if !replaced {
			graph.Nodes = append(graph.Nodes, node{ID: "step:agent_executor", Label: "agent_executor", Kind: "agent_step", Status: executorStep.Status, RefID: executorStep.ID})
		}
		hasEdge := false
		for _, existing := range graph.Edges {
			if existing.From == "step:agent_authorization" && existing.To == "step:agent_executor" {
				hasEdge = true
				break
			}
		}
		if !hasEdge {
			graph.Edges = append(graph.Edges, edge{From: "step:agent_authorization", To: "step:agent_executor", Label: "approved_execution"})
		}
	}
	return mustJSON(graph)
}

func outputString(output map[string]any, key, fallback string) string {
	value, ok := output[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

type operationProposal struct {
	reviewResult    string
	guardrailResult string
	confidence      string
	summary         string
	operation       *storage.StockProposedOperation
}

func (s *Service) proposeOperation(ctx context.Context, strategy storage.StockStrategy, portfolio storage.StockPortfolio, holdings []storage.StockHolding, quote storage.StockQuote, quoteErr error) operationProposal {
	if quoteErr != nil || !quoteUsableForOperation(quote, s.now(), 15*time.Minute) {
		return operationProposal{
			reviewResult:    "trade_signal",
			guardrailResult: "data_missing",
			confidence:      "low",
			summary:         "行情缺失、过期或不可交易，不能生成账户绑定操作建议，仅保留交易信号",
		}
	}
	action := directionToAction(strategy.Direction)
	if action == "watch" || action == "hold" {
		return operationProposal{
			reviewResult:    "trade_signal",
			guardrailResult: "not_applicable",
			confidence:      "medium",
			summary:         "策略方向为观察/持有，不生成具体操作建议",
		}
	}
	holding := findHolding(holdings, strategy.Symbol)
	totalAsset := portfolio.Cash
	for _, item := range holdings {
		totalAsset += item.MarketValue
	}
	if totalAsset <= 0 {
		totalAsset = portfolio.Cash
	}
	targetPct := strategy.TargetPositionPct
	if targetPct <= 0 {
		targetPct = math.Min(portfolio.MaxSinglePositionPct, 0.1)
	}
	if portfolio.MaxSinglePositionPct > 0 && targetPct > portfolio.MaxSinglePositionPct {
		return operationProposal{
			reviewResult:    "propose_operation",
			guardrailResult: "blocked",
			confidence:      "medium",
			summary:         "目标仓位超过单票仓位上限，已阻止操作建议",
			operation: &storage.StockProposedOperation{
				StrategyID:        strategy.ID,
				PortfolioID:       portfolio.ID,
				Symbol:            strategy.Symbol,
				Market:            strategy.Market,
				Name:              strategy.Name,
				Action:            action,
				Price:             quote.LastPrice,
				TargetPositionPct: targetPct,
				GuardrailResult:   "blocked",
				GuardrailReason:   "single_position_limit_exceeded",
				Status:            "blocked",
			},
		}
	}
	if action == "buy" || action == "add" {
		if !portfolio.AllowBuy || (action == "add" && !portfolio.AllowAdd) {
			return blockedProposal(strategy, portfolio, quote, action, targetPct, "buy_or_add_disabled")
		}
		targetValue := totalAsset * targetPct
		currentValue := holding.Quantity * quote.LastPrice
		need := targetValue - currentValue
		if need <= 0 {
			return operationProposal{reviewResult: "trade_signal", guardrailResult: "blocked", confidence: "medium", summary: "当前仓位已达到或超过目标仓位，不生成加仓建议"}
		}
		if need > portfolio.Cash {
			need = portfolio.Cash
		}
		quantity := normalizeBuyLot(need / quote.LastPrice)
		amount := quantity * quote.LastPrice
		if quantity <= 0 || amount <= 0 || portfolio.Cash+0.000001 < amount {
			return blockedProposal(strategy, portfolio, quote, action, targetPct, "cash_not_enough")
		}
		if portfolio.MaxSinglePositionPct > 0 {
			projectedPct := (currentValue + amount) / totalAsset
			if projectedPct > portfolio.MaxSinglePositionPct+0.000001 {
				return blockedProposal(strategy, portfolio, quote, action, targetPct, "lot_size_exceeds_single_position_limit")
			}
		}
		if reason := s.concentrationLimitReason(ctx, strategy, holdings, quote.LastPrice, quantity, totalAsset); reason != "" {
			return blockedProposal(strategy, portfolio, quote, action, targetPct, reason)
		}
		return operationProposal{
			reviewResult:    "propose_operation",
			guardrailResult: "passed",
			confidence:      "medium",
			summary:         fmt.Sprintf("通过 guardrails，建议%s %.0f 股，参考金额 %.2f", action, quantity, amount),
			operation: &storage.StockProposedOperation{
				StrategyID:        strategy.ID,
				PortfolioID:       portfolio.ID,
				Symbol:            strategy.Symbol,
				Market:            strategy.Market,
				Name:              strategy.Name,
				Action:            action,
				Quantity:          quantity,
				Price:             quote.LastPrice,
				Amount:            amount,
				TargetPositionPct: targetPct,
				GuardrailResult:   "passed",
				GuardrailReason:   "ok",
				Status:            "pending_confirmation",
			},
		}
	}
	if !portfolio.AllowSell || (action == "reduce" && !portfolio.AllowReduce) {
		return blockedProposal(strategy, portfolio, quote, action, targetPct, "sell_or_reduce_disabled")
	}
	if holding.Quantity <= 0 || holding.AvailableQuantity <= 0 {
		return blockedProposal(strategy, portfolio, quote, action, targetPct, "holding_empty_or_not_sellable")
	}
	quantity := holding.AvailableQuantity
	if action == "reduce" {
		quantity = normalizeALot(holding.AvailableQuantity * 0.5)
		if quantity <= 0 {
			quantity = holding.AvailableQuantity
		}
	}
	amount := quantity * quote.LastPrice
	return operationProposal{
		reviewResult:    "propose_operation",
		guardrailResult: "passed",
		confidence:      "medium",
		summary:         fmt.Sprintf("通过 guardrails，建议%s %.0f 股，参考金额 %.2f", action, quantity, amount),
		operation: &storage.StockProposedOperation{
			StrategyID:        strategy.ID,
			PortfolioID:       portfolio.ID,
			Symbol:            strategy.Symbol,
			Market:            strategy.Market,
			Name:              strategy.Name,
			Action:            action,
			Quantity:          quantity,
			Price:             quote.LastPrice,
			Amount:            amount,
			TargetPositionPct: targetPct,
			GuardrailResult:   "passed",
			GuardrailReason:   "ok",
			Status:            "pending_confirmation",
		},
	}
}

func (s *Service) concentrationLimitReason(ctx context.Context, strategy storage.StockStrategy, holdings []storage.StockHolding, price, addQuantity, totalAsset float64) string {
	if totalAsset <= 0 {
		return ""
	}
	instrument, err := s.store.GetStockInstrument(ctx, strategy.Symbol)
	if err != nil {
		return ""
	}
	industryValue := addQuantity * price
	themeValue := addQuantity * price
	for _, holding := range holdings {
		item, itemErr := s.store.GetStockInstrument(ctx, holding.Symbol)
		if itemErr != nil {
			continue
		}
		value := holding.Quantity * holding.LastPrice
		if instrument.Industry != "" && item.Industry == instrument.Industry {
			industryValue += value
		}
		if instrument.Concept != "" && item.Concept == instrument.Concept {
			themeValue += value
		}
	}
	if instrument.Industry != "" && industryValue/totalAsset > 0.45 {
		return "industry_concentration_limit_exceeded"
	}
	if instrument.Concept != "" && themeValue/totalAsset > 0.55 {
		return "theme_concentration_limit_exceeded"
	}
	return ""
}

func quoteUsableForOperation(quote storage.StockQuote, now time.Time, maxAge time.Duration) bool {
	if quote.LastPrice <= 0 || quote.DataFreshness != "fresh" || quote.TradableStatus != "tradable" {
		return false
	}
	timestamp := quote.DataTimestamp
	if timestamp == "" {
		timestamp = quote.UpdatedAt
	}
	if timestamp == "" {
		return false
	}
	quoteAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return false
	}
	return now.Sub(quoteAt) <= maxAge
}

func blockedProposal(strategy storage.StockStrategy, portfolio storage.StockPortfolio, quote storage.StockQuote, action string, targetPct float64, reason string) operationProposal {
	return operationProposal{
		reviewResult:    "propose_operation",
		guardrailResult: "blocked",
		confidence:      "medium",
		summary:         "执行约束阻止了账户绑定操作建议: " + reason,
		operation: &storage.StockProposedOperation{
			StrategyID:        strategy.ID,
			PortfolioID:       portfolio.ID,
			Symbol:            strategy.Symbol,
			Market:            strategy.Market,
			Name:              strategy.Name,
			Action:            action,
			Price:             quote.LastPrice,
			TargetPositionPct: targetPct,
			GuardrailResult:   "blocked",
			GuardrailReason:   reason,
			Status:            "blocked",
		},
	}
}

func watchTriggered(watch storage.StockWatch, quote storage.StockQuote) (bool, string) {
	if watch.TriggerPriceAbove > 0 && quote.LastPrice >= watch.TriggerPriceAbove {
		return true, fmt.Sprintf("价格 %.3f 上穿 %.3f", quote.LastPrice, watch.TriggerPriceAbove)
	}
	if watch.TriggerPriceBelow > 0 && quote.LastPrice <= watch.TriggerPriceBelow {
		return true, fmt.Sprintf("价格 %.3f 下破 %.3f", quote.LastPrice, watch.TriggerPriceBelow)
	}
	return false, ""
}

func watchAlertDedupeKey(watch storage.StockWatch, reason string) string {
	direction := "price"
	if strings.Contains(reason, "上穿") {
		direction = "price_above"
	} else if strings.Contains(reason, "下破") {
		direction = "price_below"
	}
	return fmt.Sprintf("watch:%s:%s", watch.ID, direction)
}

func watchDueForCheck(watch storage.StockWatch, now time.Time) bool {
	if watch.LastCheckedAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339Nano, watch.LastCheckedAt)
	if err != nil {
		return true
	}
	interval := watch.CheckIntervalSeconds
	if interval <= 0 {
		interval = 30
	}
	return now.Sub(last) >= time.Duration(interval)*time.Second
}

func (s *Service) ensureDataSource(ctx context.Context, source, sourceType string) (storage.StockDataSource, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "manual_seed"
	}
	src, err := s.store.GetStockDataSource(ctx, source)
	if err == nil {
		return src, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return storage.StockDataSource{}, err
	}
	status, quality, reason := sourceProbeResult(storage.StockDataSource{Source: source, SourceType: sourceType, AuthMode: "none", Enabled: true})
	return s.store.UpsertStockDataSource(ctx, storage.StockDataSource{
		Source:           source,
		DisplayName:      source,
		SourceType:       sourceType,
		AuthMode:         "none",
		Enabled:          true,
		Status:           status,
		Quality:          quality,
		FailureSummary:   reason,
		RateLimitSeconds: 60,
	})
}

func (s *Service) createNewsAlerts(ctx context.Context, items []storage.StockNewsItem) ([]storage.StockAlert, error) {
	if len(items) == 0 {
		return nil, nil
	}
	watches, err := s.store.ListActiveStockWatches(ctx)
	if err != nil {
		return nil, err
	}
	var alerts []storage.StockAlert
	for _, item := range items {
		for _, watch := range watches {
			strategy, strategyErr := s.store.GetStockStrategy(ctx, watch.StrategyID)
			if strategyErr != nil {
				strategy = storage.StockStrategy{}
			}
			if !newsMatchesWatch(item, watch, strategy) {
				continue
			}
			dedupeKey := fmt.Sprintf("news:%s:%s", item.ID, watch.ID)
			exists, err := s.store.OpenStockAlertExists(ctx, dedupeKey)
			if err != nil {
				return nil, err
			}
			if exists {
				continue
			}
			alert, err := s.store.CreateStockAlert(ctx, storage.StockAlert{
				WatchID:       watch.ID,
				StrategyID:    watch.StrategyID,
				PortfolioID:   watch.PortfolioID,
				Symbol:        firstNonEmpty(item.Symbol, watch.Symbol),
				Market:        firstNonEmpty(item.Market, watch.Market),
				Name:          watch.Name,
				Level:         newsAlertLevel(item.Importance),
				Status:        "new",
				SourceType:    "news_item",
				SourceRefID:   item.ID,
				DedupeKey:     dedupeKey,
				Title:         fmt.Sprintf("%s 消息面命中", firstNonEmpty(item.Symbol, watch.Symbol)),
				Summary:       firstNonEmpty(item.Summary, item.Title),
				TriggerReason: "消息面命中: " + item.Title,
			})
			if err != nil {
				return nil, err
			}
			alerts = append(alerts, alert)
		}
	}
	return alerts, nil
}

func newsMatchesWatch(item storage.StockNewsItem, watch storage.StockWatch, strategy storage.StockStrategy) bool {
	if item.Symbol != "" && strings.EqualFold(item.Symbol, watch.Symbol) {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{item.Title, item.Summary, item.Keywords}, " "))
	terms := []string{watch.Symbol, watch.Name, strategy.Symbol, strategy.Name, strategy.Title}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(haystack, term) {
			return true
		}
	}
	return false
}

func newsAlertLevel(importance string) string {
	switch strings.ToLower(strings.TrimSpace(importance)) {
	case "urgent", "high":
		return "strong"
	case "low":
		return "info"
	default:
		return "medium"
	}
}

func sourceProbeResult(src storage.StockDataSource) (string, string, string) {
	authMode := strings.ToLower(strings.TrimSpace(src.AuthMode))
	if !src.Enabled || authMode == "disabled" {
		return "disabled", "failed", "数据源已禁用"
	}
	if authMode == "api_key" || authMode == "cookie" {
		return "auth_required", "partial", "需要用户自有授权配置，系统不会保存示例凭据"
	}
	return "available", "fresh", ""
}

func taskStatus(processed, failed int) string {
	if failed > 0 && processed == 0 {
		return "failed"
	}
	if failed > 0 {
		return "degraded"
	}
	return "completed"
}

func failureSummary(failed int, summary string) string {
	if failed > 0 {
		return summary
	}
	return ""
}

func limitText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func directionToAction(direction string) string {
	switch normalizeDirection(direction) {
	case "buy":
		return "buy"
	case "add":
		return "add"
	case "sell":
		return "sell"
	case "reduce":
		return "reduce"
	case "hold":
		return "hold"
	default:
		return "watch"
	}
}

func normalizeDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "buy", "add", "sell", "reduce", "hold", "watch":
		return strings.ToLower(strings.TrimSpace(direction))
	default:
		return "watch"
	}
}

func normalizeALot(quantity float64) float64 {
	if quantity <= 0 {
		return 0
	}
	return math.Floor(quantity/100) * 100
}

func normalizeBuyLot(quantity float64) float64 {
	if quantity <= 0 {
		return 0
	}
	return math.Ceil(quantity/100) * 100
}

func findHolding(items []storage.StockHolding, symbol string) storage.StockHolding {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	for _, item := range items {
		if strings.ToUpper(strings.TrimSpace(item.Symbol)) == symbol {
			return item
		}
	}
	return storage.StockHolding{}
}

func priceRange(strategy storage.StockStrategy) string {
	if strategy.EntryPriceLow > 0 && strategy.EntryPriceHigh > 0 {
		return fmt.Sprintf("%.3f - %.3f", strategy.EntryPriceLow, strategy.EntryPriceHigh)
	}
	if strategy.EntryPriceLow > 0 {
		return fmt.Sprintf(">= %.3f", strategy.EntryPriceLow)
	}
	if strategy.EntryPriceHigh > 0 {
		return fmt.Sprintf("<= %.3f", strategy.EntryPriceHigh)
	}
	return "未设定"
}

func dataQualitySummary(quote storage.StockQuote, err error) string {
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "缺少行情快照"
		}
		return "行情读取失败"
	}
	return fmt.Sprintf("行情 %s / 可交易状态 %s / 时间戳 %s", quote.DataFreshness, quote.TradableStatus, firstNonEmpty(quote.DataTimestamp, quote.UpdatedAt))
}

func augmentReviewOutput(output map[string]any, alert storage.StockAlert, strategy storage.StockStrategy, quote storage.StockQuote, quoteErr error, portfolio storage.StockPortfolio, portfolioErr error, holdings []storage.StockHolding, operation *storage.StockProposedOperation, decisionNote string) {
	evidence := []map[string]any{
		{"type": "alert", "id": alert.ID, "summary": firstNonEmpty(alert.TriggerReason, alert.Summary, alert.Title), "source_type": alert.SourceType},
		{"type": "strategy", "id": strategy.ID, "version": strategy.CurrentVersion, "direction": strategy.Direction},
	}
	counterEvidence := []map[string]any{}
	if quoteErr == nil {
		evidence = append(evidence, map[string]any{"type": "quote", "symbol": quote.Symbol, "price": quote.LastPrice, "freshness": quote.DataFreshness, "tradable_status": quote.TradableStatus, "timestamp": firstNonEmpty(quote.DataTimestamp, quote.UpdatedAt)})
		if !quoteUsableForOperation(quote, time.Now(), 15*time.Minute) {
			counterEvidence = append(counterEvidence, map[string]any{"type": "data_quality", "summary": "行情不可用于强操作或已过期"})
		}
	} else {
		counterEvidence = append(counterEvidence, map[string]any{"type": "data_quality", "summary": dataQualitySummary(quote, quoteErr)})
	}
	if strategy.PortfolioID != "" && portfolioErr == nil {
		evidence = append(evidence, map[string]any{"type": "portfolio", "id": portfolio.ID, "cash": portfolio.Cash, "holding_count": len(holdings), "max_single_position_pct": portfolio.MaxSinglePositionPct})
	}
	if operation != nil {
		output["proposed_operation"] = map[string]any{
			"action": operation.Action, "quantity": operation.Quantity, "price": operation.Price, "amount": operation.Amount,
			"target_position_pct": operation.TargetPositionPct, "guardrail_result": operation.GuardrailResult, "guardrail_reason": operation.GuardrailReason,
		}
	} else if output["review_result"] == "trade_signal" {
		output["trade_signal"] = map[string]any{"direction": normalizeDirection(strategy.Direction), "price_range": priceRange(strategy), "stop_loss": strategy.StopLoss, "take_profit": strategy.TakeProfit}
	}
	output["evidence"] = evidence
	output["counter_evidence"] = counterEvidence
	output["memory_updates"] = []map[string]any{{"object_type": "agent_run", "summary": "Review 完成后写入 Agent trace 和股票记忆"}}
	output["next_actions"] = reviewNextActions(outputString(output, "review_result", ""), operation, decisionNote)
}

func reviewNextActions(reviewResult string, operation *storage.StockProposedOperation, decisionNote string) []map[string]any {
	actions := []map[string]any{{"type": "continue_watch", "summary": "继续保留盯盘任务，等待下一次有效触发"}}
	if operation != nil && operation.Status == "pending_confirmation" {
		actions = append([]map[string]any{{"type": "manual_confirmation", "summary": "用户需二次确认或作废操作建议"}}, actions...)
	} else if reviewResult == "trade_signal" {
		actions = append([]map[string]any{{"type": "bind_portfolio_or_observe", "summary": "账户无关信号仅用于观察；需要账户绑定后才生成操作建议"}}, actions...)
	}
	if decisionNote != "" {
		actions = append(actions, map[string]any{"type": "decision_note", "summary": decisionNote})
	}
	return actions
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
