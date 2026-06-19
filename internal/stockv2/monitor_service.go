package stockv2

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// 监控服务:把系统固化的后台监控任务收敛成可观测对象。
// 复用 watch_evaluator 的规则判断(data_strategy scan),不重写规则逻辑;
// data_strategy / portfolio_risk 产生 MonitorHit(候选),不生成买卖建议、不改持仓。
// universe / quote / daily_bars 仅委托现有执行器并记录摘要行,不复制执行逻辑。

// ListMonitorTasks 返回系统内置任务定义 + 当前配置 + 最近一次运行摘要。
func (s *Service) ListMonitorTasks(ctx context.Context) ([]MonitorTask, error) {
	configs, err := s.store.ListMonitorTaskConfigs(ctx)
	if err != nil {
		return nil, err
	}
	defs := builtinMonitorTaskDefinitions()
	tasks := make([]MonitorTask, 0, len(defs))
	for _, def := range defs {
		cfg, ok := configs[def.TaskType]
		if !ok {
			cfg = def.DefaultConfig
		}
		latest, _ := s.latestTaskRunSummary(ctx, def.TaskType)
		tasks = append(tasks, MonitorTask{
			Definition: def,
			Config:     cfg,
			LatestRun:  latest,
		})
	}
	return tasks, nil
}

// GetMonitorTask 返回单个任务聚合视图。
func (s *Service) GetMonitorTask(ctx context.Context, taskType string) (MonitorTask, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorTask{}, ErrInvalidMonitorTaskType
	}
	cfg, err := s.store.GetMonitorTaskConfig(ctx, taskType)
	if err != nil {
		if errors.Is(err, ErrMonitorTaskNotFound) {
			cfg = def.DefaultConfig
		} else {
			return MonitorTask{}, err
		}
	}
	latest, _ := s.latestTaskRunSummary(ctx, taskType)
	return MonitorTask{Definition: def, Config: cfg, LatestRun: latest}, nil
}

// UpdateMonitorTaskConfig 修改任务配置(开关/周期/范围/敏感度/冷却/Agent 开关与预算)。
func (s *Service) UpdateMonitorTaskConfig(ctx context.Context, taskType string, req RequestUpdateMonitorTaskConfig) (MonitorTask, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorTask{}, ErrInvalidMonitorTaskType
	}
	current, err := s.store.GetMonitorTaskConfig(ctx, taskType)
	if err != nil {
		if errors.Is(err, ErrMonitorTaskNotFound) {
			current = def.DefaultConfig
		} else {
			return MonitorTask{}, err
		}
	}
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.IntervalSeconds != nil && *req.IntervalSeconds > 0 {
		current.IntervalSeconds = *req.IntervalSeconds
	}
	if req.Scope != nil {
		current.Scope = *req.Scope
	}
	if req.Sensitivity != nil {
		current.Sensitivity = *req.Sensitivity
	}
	if req.CooldownSeconds != nil && *req.CooldownSeconds >= 0 {
		current.CooldownSeconds = *req.CooldownSeconds
	}
	if req.AgentDoublecheckEnabled != nil {
		current.AgentDoublecheckEnabled = *req.AgentDoublecheckEnabled
	}
	if req.AgentBudget != nil && *req.AgentBudget >= 0 {
		current.AgentBudget = *req.AgentBudget
	}
	if err := s.store.UpsertMonitorTaskConfig(ctx, taskType, current); err != nil {
		return MonitorTask{}, err
	}
	// 启用任一监控任务时确保后台调度在运行(幂等);周期触发由 runScheduledMonitors 检查 enabled。
	if current.Enabled {
		s.StartBackground(context.Background())
	}
	latest, _ := s.latestTaskRunSummary(ctx, taskType)
	return MonitorTask{Definition: def, Config: current, LatestRun: latest}, nil
}

// RunMonitorTask 手动或调度触发一次监控任务。disabled(Runnable=false)任务不执行。
func (s *Service) RunMonitorTask(ctx context.Context, taskType, triggerType string) (MonitorRun, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorRun{}, ErrInvalidMonitorTaskType
	}
	if !def.Runnable {
		return MonitorRun{}, ErrMonitorTaskNotConfigured
	}
	if triggerType == "" {
		triggerType = MonitorTriggerManual
	}
	if taskType == MonitorTaskLatestQuoteRefresh {
		state, err := s.RunLatestQuoteRefreshTask(ctx, triggerType)
		if err != nil {
			return MonitorRun{}, err
		}
		return quoteRefreshTaskStateAsMonitorRun(state), nil
	}
	// 并发保护:同任务已有 running 则拒绝重复触发
	if latest, _ := s.store.GetLatestMonitorRun(ctx, taskType); latest != nil && latest.Status == MonitorRunStatusRunning {
		return MonitorRun{}, ErrMonitorTaskAlreadyRunning
	}

	now := time.Now()
	run := MonitorRun{
		ID:          generateID(),
		TaskType:    taskType,
		Status:      MonitorRunStatusRunning,
		TriggerType: triggerType,
		StartedAt:   now,
		CreatedAt:   now,
		Metadata:    map[string]any{},
	}
	created, err := s.store.CreateMonitorRun(ctx, run)
	if err != nil {
		return MonitorRun{}, err
	}

	cfg, cfgErr := s.store.GetMonitorTaskConfig(ctx, taskType)
	if cfgErr != nil {
		cfg = def.DefaultConfig
	}

	var final MonitorRun
	switch taskType {
	case MonitorTaskDataStrategyMonitor:
		final = s.runDataStrategyMonitor(ctx, created, cfg)
	case MonitorTaskPortfolioRiskMonitor:
		final = s.runPortfolioRiskMonitor(ctx, created, cfg)
	case MonitorTaskUniverseUpdate, MonitorTaskDailyBarsSync:
		final = s.delegateDataMonitorRun(ctx, created, taskType, triggerType)
	default:
		final = created
		final.Status = MonitorRunStatusFailed
		final.ErrorMessage = "task type not supported"
		final.FinishedAt = time.Now()
	}
	if _, err := s.store.UpdateMonitorRun(ctx, final); err != nil {
		return final, err
	}
	return final, nil
}

func (s *Service) latestTaskRunSummary(ctx context.Context, taskType string) (*MonitorRun, error) {
	if taskType == MonitorTaskLatestQuoteRefresh {
		state, err := s.store.GetQuoteRefreshTaskState(ctx, taskType)
		if err != nil || state == nil {
			return nil, err
		}
		run := quoteRefreshTaskStateAsMonitorRun(*state)
		return &run, nil
	}
	return s.store.GetLatestMonitorRun(ctx, taskType)
}

func (s *Service) GetMonitorRun(ctx context.Context, id string) (MonitorRun, error) {
	return s.store.GetMonitorRun(ctx, id)
}

func (s *Service) ListMonitorRuns(ctx context.Context, filter MonitorRunListFilter) ([]MonitorRun, error) {
	return s.store.ListMonitorRuns(ctx, filter)
}

func (s *Service) CountMonitorRuns(ctx context.Context, filter MonitorRunListFilter) (int, error) {
	return s.store.CountMonitorRuns(ctx, filter)
}

func (s *Service) ListMonitorHits(ctx context.Context, filter MonitorHitListFilter) ([]MonitorHit, error) {
	return s.store.ListMonitorHits(ctx, filter)
}

func (s *Service) CountMonitorHits(ctx context.Context, filter MonitorHitListFilter) (int, error) {
	return s.store.CountMonitorHits(ctx, filter)
}

// runDataStrategyMonitor 扫描 active 单票策略,用策略触发价规则评估最新行情/日K,命中产候选 hit。
// 复用 triggerConfigFromStrategy + watchRulesFromConfig + evaluateWatchRule,不重写判断逻辑。
func (s *Service) runDataStrategyMonitor(ctx context.Context, run MonitorRun, cfg MonitorTaskConfig) MonitorRun {
	run.Metadata["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	strategies, err := s.store.ListStrategies(ctx, StrategyListFilter{
		Kind:   StrategyKindSymbolStrategy,
		Status: StrategyStatusActive,
		Limit:  500,
	})
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}
	run.ScannedCount = len(strategies)
	for _, sw := range strategies {
		if sw.ActiveVersion == nil || sw.Strategy.Symbol == "" {
			continue
		}
		triggerConfig, err := s.triggerConfigFromStrategy(ctx, sw)
		if err != nil {
			run.FailedCount++
			continue
		}
		tempWatch := StockV2Watch{
			Symbol:        sw.Strategy.Symbol,
			Market:        sw.Strategy.Market,
			PortfolioID:   sw.Strategy.PortfolioID,
			TriggerPolicy: WatchTriggerPolicyAny,
			TriggerConfig: triggerConfig,
		}
		rules := watchRulesFromConfig(tempWatch)
		matched := false
		for _, rule := range rules {
			rr := s.evaluateWatchRule(ctx, tempWatch, rule, run.StartedAt)
			if rr.Status == WatchRunStatusMatched {
				hit := MonitorHit{
					RunID:      run.ID,
					TaskType:   MonitorTaskDataStrategyMonitor,
					Status:     MonitorHitStatusCandidate,
					StrategyID: sw.Strategy.ID,
					Symbol:     sw.Strategy.Symbol,
					Market:     sw.Strategy.Market,
					Title:      alertTitleForRule(rr),
					Summary:    rr.Reason,
					Evidence:   monitorEvidenceWithAgentState(rr.Evidence, cfg),
				}
				if _, err := s.store.CreateMonitorHit(ctx, hit); err != nil {
					run.FailedCount++
					continue
				}
				run.HitCount++
				matched = true
			}
		}
		if matched {
			run.SuccessCount++
		}
	}
	run.Status = MonitorRunStatusCompleted
	run.FinishedAt = time.Now()
	run.ScopeSummary = scopeSummaryFromCount(run.ScannedCount, "strategies")
	return run
}

// runPortfolioRiskMonitor 扫描组合快照与持仓,检查单票权重与数据新鲜度,命中产候选 hit。
func (s *Service) runPortfolioRiskMonitor(ctx context.Context, run MonitorRun, cfg MonitorTaskConfig) MonitorRun {
	run.Metadata["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}
	run.ScannedCount = len(portfolios)
	for _, portfolio := range portfolios {
		snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolio.ID, 1)
		if err != nil || len(snapshots) == 0 {
			continue
		}
		snapshot := snapshots[0]
		// 数据过期:估值非 fresh 或存在 stale quote
		if snapshot.Status != PortfolioValuationStatusFresh || snapshot.StaleQuoteCount > 0 {
			hit := MonitorHit{
				RunID:       run.ID,
				TaskType:    MonitorTaskPortfolioRiskMonitor,
				Status:      MonitorHitStatusCandidate,
				PortfolioID: portfolio.ID,
				Title:       "组合行情数据过期",
				Summary:     "最新组合快照估值不新鲜或存在 stale quote,需先刷新行情。",
				Evidence:    monitorEvidenceWithAgentState(portfolioEvidence(snapshot, nil), cfg),
			}
			if _, err := s.store.CreateMonitorHit(ctx, hit); err == nil {
				run.HitCount++
			}
		}
		// 单票权重过高
		holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
		if err != nil {
			continue
		}
		limit := portfolio.MaxSinglePositionPct
		if limit <= 0 {
			limit = 20
		}
		for _, holding := range holdings {
			weight := holding.PositionPct
			if weight <= 0 && holding.MarketValue > 0 && snapshot.TotalAssetValue > 0 {
				weight = holding.MarketValue / snapshot.TotalAssetValue * 100
			}
			if weight > limit {
				hit := MonitorHit{
					RunID:       run.ID,
					TaskType:    MonitorTaskPortfolioRiskMonitor,
					Status:      MonitorHitStatusCandidate,
					PortfolioID: portfolio.ID,
					Symbol:      holding.Symbol,
					Market:      holding.Market,
					Title:       "单票仓位占比超限",
					Summary:     "持仓权重超过组合单票上限约束。",
					Evidence:    monitorEvidenceWithAgentState(portfolioEvidence(snapshot, &holding), cfg),
				}
				if _, err := s.store.CreateMonitorHit(ctx, hit); err == nil {
					run.HitCount++
				}
			}
		}
		run.SuccessCount++
	}
	run.Status = MonitorRunStatusCompleted
	run.FinishedAt = time.Now()
	run.ScopeSummary = scopeSummaryFromCount(run.ScannedCount, "portfolios")
	return run
}

// delegateDataMonitorRun 委托现有数据任务执行器(universe/daily_bars),
// 监控历史只记录触发摘要行,数据抓取细节仍留在各自的数据任务历史。
func (s *Service) delegateDataMonitorRun(ctx context.Context, run MonitorRun, taskType, triggerType string) MonitorRun {
	run.FinishedAt = time.Now()
	switch taskType {
	case MonitorTaskUniverseUpdate:
		run.Metadata["delegated"] = "universe_update"
		job, err := s.ExecuteUniverseUpdate(ctx, triggerType, "monitor")
		if err != nil {
			run.Status = MonitorRunStatusFailed
			run.ErrorMessage = err.Error()
			return run
		}
		run.Metadata["jobId"] = job.ID
		run.Status = MonitorRunStatusCompleted
		run.ScannedCount = 1
		run.ScopeSummary = "universe"
	case MonitorTaskDailyBarsSync:
		run.Metadata["delegated"] = "daily_bars_sync"
		job, err := s.RunDailyBarsJob(ctx, DailyBarsJobRequest{Mode: DailyBarJobModeHot, TriggerSource: "monitor"})
		if err != nil {
			run.Status = MonitorRunStatusFailed
			run.ErrorMessage = err.Error()
			return run
		}
		run.Metadata["jobId"] = job.ID
		run.Status = MonitorRunStatusCompleted
		run.ScannedCount = 1
		run.ScopeSummary = "daily_bars"
	}
	return run
}

func (s *Service) RunLatestQuoteRefreshTask(ctx context.Context, triggerType string) (QuoteRefreshTaskState, error) {
	if triggerType == "" {
		triggerType = MonitorTriggerManual
	}
	if current, _ := s.store.GetQuoteRefreshTaskState(ctx, MonitorTaskLatestQuoteRefresh); current != nil && current.Status == MonitorRunStatusRunning {
		return QuoteRefreshTaskState{}, ErrMonitorTaskAlreadyRunning
	}

	startedAt := time.Now()
	symbols := s.collectMonitorSymbols(ctx)
	state := QuoteRefreshTaskState{
		TaskType:     MonitorTaskLatestQuoteRefresh,
		Status:       MonitorRunStatusRunning,
		TriggerType:  triggerType,
		StartedAt:    startedAt,
		ScopeSummary: scopeSummaryFromCount(len(symbols), "symbols"),
		ScannedCount: len(symbols),
		UpdatedAt:    startedAt,
	}
	if err := s.store.UpsertQuoteRefreshTaskState(ctx, state); err != nil {
		return QuoteRefreshTaskState{}, err
	}

	if len(symbols) == 0 {
		finishedAt := time.Now()
		state.Status = MonitorRunStatusCompleted
		state.FinishedAt = finishedAt
		state.UpdatedAt = finishedAt
		if err := s.store.UpsertQuoteRefreshTaskState(ctx, state); err != nil {
			return state, err
		}
		return state, nil
	}

	result, err := s.RefreshLatestQuotes(ctx, symbols, "monitor")
	finishedAt := time.Now()
	state.FinishedAt = finishedAt
	state.UpdatedAt = finishedAt
	state.SuccessCount = result.RefreshedCount
	state.FailedCount = result.FailedCount
	if err != nil {
		state.Status = MonitorRunStatusFailed
		state.ErrorMessage = err.Error()
	} else {
		state.Status = MonitorRunStatusCompleted
	}
	if saveErr := s.store.UpsertQuoteRefreshTaskState(ctx, state); saveErr != nil {
		if err != nil {
			return state, err
		}
		return state, saveErr
	}
	return state, err
}

func quoteRefreshTaskStateAsMonitorRun(state QuoteRefreshTaskState) MonitorRun {
	return MonitorRun{
		ID:           state.TaskType,
		TaskType:     state.TaskType,
		Status:       state.Status,
		TriggerType:  state.TriggerType,
		StartedAt:    state.StartedAt,
		FinishedAt:   state.FinishedAt,
		ScopeSummary: state.ScopeSummary,
		ScannedCount: state.ScannedCount,
		SuccessCount: state.SuccessCount,
		FailedCount:  state.FailedCount,
		ErrorMessage: state.ErrorMessage,
		CreatedAt:    state.StartedAt,
		Metadata:     map[string]any{"stateOnly": true},
	}
}

func (s *Service) collectMonitorSymbols(ctx context.Context) []string {
	seen := make(map[string]struct{})
	symbols := make([]string, 0)
	addSymbol := func(symbol string) {
		if symbol == "" {
			return
		}
		if _, ok := seen[symbol]; ok {
			return
		}
		seen[symbol] = struct{}{}
		symbols = append(symbols, symbol)
	}

	portfolios, err := s.store.ListPortfolios(ctx)
	if err == nil {
		for _, portfolio := range portfolios {
			holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
			if err != nil {
				continue
			}
			for _, holding := range holdings {
				addSymbol(holding.Symbol)
			}
		}
	}

	strategies, err := s.store.ListStrategies(ctx, StrategyListFilter{
		Kind:   StrategyKindSymbolStrategy,
		Status: StrategyStatusActive,
		Limit:  500,
	})
	if err == nil {
		for _, strategy := range strategies {
			addSymbol(strategy.Strategy.Symbol)
		}
	}
	return symbols
}

func monitorAgentDecisionState(cfg MonitorTaskConfig) string {
	if cfg.AgentDoublecheckEnabled {
		return "enabled_no_executor"
	}
	return "not_enabled"
}

func monitorEvidenceWithAgentState(evidence map[string]any, cfg MonitorTaskConfig) map[string]any {
	next := make(map[string]any, len(evidence)+1)
	for key, value := range evidence {
		next[key] = value
	}
	next["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	return next
}

func scopeSummaryFromCount(count int, unit string) string {
	if count <= 0 {
		return ""
	}
	return "scanned " + strconv.Itoa(count) + " " + unit
}
