package stockv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Service 主业务服务
type Service struct {
	store      *Store
	log        *slog.Logger
	httpClient *http.Client
	bgMu       sync.Mutex
	bgCancel   context.CancelFunc
	bgWg       sync.WaitGroup
	settings   StockV2Settings

	universeSource  *UniverseDataSource
	dailyBarsSource *DailyBarsSource

	// Agent 执行相关
	agentTaskPool *agentTaskPool
	agentExecutor AgentExecutor
}

// NewService 创建新的股票V2服务
func NewService(store *Store, log *slog.Logger, httpClient *http.Client) *Service {
	return &Service{
		store:           store,
		log:             log,
		httpClient:      httpClient,
		universeSource:  NewUniverseDataSource(nil, httpClient),
		dailyBarsSource: NewDailyBarsSource(nil, httpClient),
		agentTaskPool:   newAgentTaskPool(defaultCleanupInterval),
	}
}

// AgentExecutor 是 Agent 执行器接口。
type AgentExecutor interface {
	ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName string) (*AgentExecutorOutput, error)
}

// WithCodexCLIExecutor 注入 Codex CLI 执行器。
func (s *Service) WithCodexCLIExecutor(binary, codexHome, mcpURL string) *Service {
	s.agentExecutor = newCodexCLIExecutor(s.log, binary, codexHome, mcpURL, s.agentTaskPool)
	return s
}

// AgentTaskPool 返回内存任务池(给 MCP handler 用)。
func (s *Service) AgentTaskPool() *agentTaskPool {
	return s.agentTaskPool
}

// Initialize 初始化服务，加载数据源
func (s *Service) Initialize(ctx context.Context) error {
	// 加载配置
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("get settings failed: %w", err)
	}
	s.settings = settings

	// 设置数据源的服务引用
	s.universeSource.service = s
	s.dailyBarsSource.service = s

	return nil
}

// CreatePortfolio 创建投资组合
func (s *Service) CreatePortfolio(ctx context.Context, req RequestCreatePortfolio) (StockV2Portfolio, error) {
	// 验证风险等级
	if req.RiskLevel != "low" && req.RiskLevel != "medium" && req.RiskLevel != "high" {
		return StockV2Portfolio{}, ErrInvalidRiskLevel
	}

	// 验证参数
	if req.Name == "" {
		return StockV2Portfolio{}, errors.New("portfolio name is required")
	}

	// 创建投资组合
	portfolio := StockV2Portfolio{
		ID:                   generateID(),
		Name:                 req.Name,
		Description:          req.Description,
		Cash:                 req.Cash,
		RiskLevel:            req.RiskLevel,
		MaxSinglePositionPct: req.MaxSinglePositionPct,
		MaxDrawdownPct:       req.MaxDrawdownPct,
		AllowBuy:             req.AllowBuy,
		AllowAdd:             req.AllowAdd,
		AllowReduce:          req.AllowReduce,
		AllowSell:            req.AllowSell,
		Notes:                req.Notes,
	}

	if err := s.store.CreatePortfolio(ctx, portfolio); err != nil {
		return StockV2Portfolio{}, wrapError(err, "create portfolio")
	}

	return portfolio, nil
}

// UpdatePortfolio 更新投资组合
func (s *Service) UpdatePortfolio(ctx context.Context, id string, req RequestUpdatePortfolio) (StockV2Portfolio, error) {
	// 获取现有投资组合
	existing, err := s.store.GetPortfolio(ctx, id)
	if err != nil {
		return StockV2Portfolio{}, err
	}

	// 应用更新
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Cash != nil {
		existing.Cash = *req.Cash
	}
	if req.RiskLevel != nil {
		if *req.RiskLevel != "low" && *req.RiskLevel != "medium" && *req.RiskLevel != "high" {
			return StockV2Portfolio{}, ErrInvalidRiskLevel
		}
		existing.RiskLevel = *req.RiskLevel
	}
	if req.MaxSinglePositionPct != nil {
		existing.MaxSinglePositionPct = *req.MaxSinglePositionPct
	}
	if req.MaxDrawdownPct != nil {
		existing.MaxDrawdownPct = *req.MaxDrawdownPct
	}
	if req.AllowBuy != nil {
		existing.AllowBuy = *req.AllowBuy
	}
	if req.AllowAdd != nil {
		existing.AllowAdd = *req.AllowAdd
	}
	if req.AllowReduce != nil {
		existing.AllowReduce = *req.AllowReduce
	}
	if req.AllowSell != nil {
		existing.AllowSell = *req.AllowSell
	}
	if req.Notes != nil {
		existing.Notes = *req.Notes
	}

	// 更新
	if err := s.store.UpdatePortfolio(ctx, existing); err != nil {
		return StockV2Portfolio{}, wrapError(err, "update portfolio")
	}

	return existing, nil
}

// DeletePortfolio 删除投资组合
func (s *Service) DeletePortfolio(ctx context.Context, id string) error {
	return s.store.DeletePortfolio(ctx, id)
}

// GetPortfolio 获取投资组合
func (s *Service) GetPortfolio(ctx context.Context, id string) (PortfolioWithHoldings, error) {
	// 获取投资组合
	portfolio, err := s.store.GetPortfolio(ctx, id)
	if err != nil {
		return PortfolioWithHoldings{}, err
	}

	// 获取持仓
	holdings, err := s.store.ListHoldings(ctx, id)
	if err != nil {
		return PortfolioWithHoldings{}, wrapError(err, "get holdings")
	}

	// 计算当前库内持仓估值；显式刷新由 RefreshPortfolioValuation 负责。
	totalValue := 0.0
	for i := range holdings {
		totalValue += holdings[i].MarketValue
	}

	// 计算总资产和现金比例
	totalAssetValue := portfolio.Cash + totalValue
	cashPct := 0.0
	if totalAssetValue > 0 {
		cashPct = portfolio.Cash / totalAssetValue * 100
	}

	return PortfolioWithHoldings{
		StockV2Portfolio: portfolio,
		Holdings:         holdings,
		TotalValue:       totalValue,
		TotalAssetValue:  totalAssetValue,
		CashPct:          cashPct,
	}, nil
}

// ListPortfolios 列出所有投资组合
func (s *Service) ListPortfolios(ctx context.Context) ([]PortfolioWithHoldings, error) {
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]PortfolioWithHoldings, 0, len(portfolios))
	for _, portfolio := range portfolios {
		portfolioWithHoldings, err := s.GetPortfolio(ctx, portfolio.ID)
		if err != nil {
			s.log.Warn("get portfolio holdings failed", "portfolio_id", portfolio.ID, "error", err)
			continue
		}
		results = append(results, portfolioWithHoldings)
	}

	return results, nil
}

// CreateHolding 创建持仓
func (s *Service) CreateHolding(ctx context.Context, portfolioID string, req RequestCreateHolding) (StockV2Holding, error) {
	// 校验投资组合存在
	if _, err := s.store.GetPortfolio(ctx, portfolioID); err != nil {
		return StockV2Holding{}, ErrPortfolioNotFound
	}

	// 从主数据补全股票名称和市场
	inst, _ := s.store.GetInstrument(ctx, req.Symbol)
	name := req.Name
	if name == "" && inst.Name != "" {
		name = inst.Name
	}
	market := req.Market
	if market == "" && inst.Market != "" {
		market = inst.Market
	}

	// 创建持仓
	now := time.Now()
	holding := StockV2Holding{
		ID:                generateID(),
		PortfolioID:       portfolioID,
		Symbol:            req.Symbol,
		Market:            market,
		Name:              name,
		Quantity:          req.Quantity,
		AvailableQuantity: req.Quantity,
		CostPrice:         req.CostPrice,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.store.CreateHolding(ctx, holding); err != nil {
		return StockV2Holding{}, wrapError(err, "create holding")
	}

	return holding, nil
}

// UpdateHolding 更新持仓
func (s *Service) UpdateHolding(ctx context.Context, portfolioID, holdingID string, req RequestUpdateHolding) (StockV2Holding, error) {
	// 获取持仓
	holding, err := s.store.GetHolding(ctx, holdingID)
	if err != nil {
		return StockV2Holding{}, ErrHoldingNotFound
	}

	// 更新持仓
	if req.Quantity != nil {
		holding.Quantity = *req.Quantity
	}
	if req.AvailableQuantity != nil {
		holding.AvailableQuantity = *req.AvailableQuantity
	}
	if req.CostPrice != nil {
		holding.CostPrice = *req.CostPrice
	}
	if req.LastPrice != nil {
		holding.LastPrice = *req.LastPrice
	}

	// 更新持仓
	if err := s.store.UpdateHolding(ctx, holding); err != nil {
		return StockV2Holding{}, wrapError(err, "update holding")
	}

	return holding, nil
}

// DeleteHolding 删除持仓
func (s *Service) DeleteHolding(ctx context.Context, portfolioID, holdingID string) error {
	// 检查持仓是否存在
	_, err := s.store.GetHolding(ctx, holdingID)
	if err != nil {
		return ErrHoldingNotFound
	}

	// 删除持仓
	if err := s.store.DeleteHolding(ctx, holdingID); err != nil {
		return wrapError(err, "delete holding")
	}

	// 更新投资组合现金（这里需要重新计算持仓价值）
	// 实际应该记录操作历史，这里简化处理

	return nil
}

// ListHoldings 列出投资组合的持仓
func (s *Service) ListHoldings(ctx context.Context, portfolioID string) ([]StockV2Holding, error) {
	holdings, err := s.store.ListHoldings(ctx, portfolioID)
	if err != nil {
		return nil, wrapError(err, "list holdings")
	}

	return holdings, nil
}

// ExecuteUniverseUpdate 执行股票主数据更新
func (s *Service) ExecuteUniverseUpdate(ctx context.Context, triggerType, triggerSource string) (StockV2UpdateJob, error) {
	// 检查是否有正在运行的更新任务
	recentJobs, err := s.store.ListUpdateJobs(ctx, 1)
	if err != nil {
		return StockV2UpdateJob{}, wrapError(err, "check recent jobs")
	}

	if len(recentJobs) > 0 && recentJobs[0].Status == "running" {
		return StockV2UpdateJob{}, ErrUpdateJobAlreadyRunning
	}

	// 创建更新任务
	job := StockV2UpdateJob{
		ID:             generateID(),
		TriggerType:    triggerType,
		TriggerSource:  triggerSource,
		Status:         "running",
		TotalCount:     0,
		ProcessedCount: 0,
		SuccessCount:   0,
		FailedCount:    0,
		StartAt:        time.Now(),
		CreatedAt:      time.Now(),
	}

	if err := s.store.CreateUpdateJob(ctx, job); err != nil {
		return StockV2UpdateJob{}, wrapError(err, "create update job")
	}

	// 启动更新任务（异步，使用独立 context，不随请求结束而取消）
	go s.runUniverseUpdate(context.Background(), job.ID)

	return job, nil
}

// runUniverseUpdate 运行股票主数据更新任务
func (s *Service) runUniverseUpdate(ctx context.Context, jobID string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("runUniverseUpdate panicked", "job_id", jobID, "panic", r)
			s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
				ID:           jobID,
				Status:       "failed",
				EndAt:        time.Now(),
				ErrorMessage: fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	// 更新进度
	progress := StockV2UpdateProgress{
		UpdateJobID: jobID,
		UpdatedAt:   time.Now(),
	}

	// 获取默认股票代码列表
	symbols := s.universeSource.GetDefaultSymbols()
	totalCount := len(symbols)

	// 更新任务总数量
	if err := s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
		ID:         jobID,
		TotalCount: totalCount,
	}); err != nil {
		s.log.Error("update job total count failed", "job_id", jobID, "error", err)
	}

	// 分批更新股票
	const batchSize = 500
	totalBatches := (totalCount + batchSize - 1) / batchSize
	var failedItems []UpdateFailure
	successCount := 0
	processedCount := 0

	for batch := 0; batch < totalBatches; batch++ {
		select {
		case <-ctx.Done():
			// 任务被取消
			s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
				ID:          jobID,
				Status:      "cancelled",
				EndAt:       time.Now(),
				FailedItems: failedItems,
			})
			return
		default:
			// 继续处理
		}

		start := batch * batchSize
		end := start + batchSize
		if end > totalCount {
			end = totalCount
		}

		batchSymbols := symbols[start:end]

		// 更新进度
		progress.CurrentBatch = batch + 1
		progress.CurrentBatchProgress = 0
		progress.CurrentSymbol = batchSymbols[0]
		s.store.UpdateUpdateProgress(ctx, progress)

		// 获取这批股票数据
		instruments, err := s.universeSource.FetchStockUniverse(ctx, batchSymbols)
		if err != nil {
			s.log.Error("fetch batch failed", "batch", batch, "error", err)
			// 整批失败，逐个记入失败列表
			for _, sym := range batchSymbols {
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: err.Error(),
				})
			}
			processedCount += len(batchSymbols)
			continue
		}

		// 构造返回结果 map，对比找失败的
		resultMap := make(map[string]StockV2Instrument, len(instruments))
		for _, inst := range instruments {
			resultMap[inst.Symbol] = inst
		}

		// 保存股票数据
		batchSuccess := 0
		for _, sym := range batchSymbols {
			inst, ok := resultMap[sym]
			if !ok {
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: "no data from source",
				})
				continue
			}

			if err := s.store.UpsertInstrument(ctx, inst); err != nil {
				s.log.Error("save instrument failed", "symbol", inst.Symbol, "error", err)
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: err.Error(),
				})
			} else {
				batchSuccess++
				progress.CurrentBatchProgress++
			}
		}

		successCount += batchSuccess
		processedCount += len(batchSymbols)

		// 更新总体进度
		progress.ProcessedCount = processedCount
		progress.SuccessCount = successCount
		s.store.UpdateUpdateProgress(ctx, progress)
		s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
			ID:             jobID,
			ProcessedCount: processedCount,
			SuccessCount:   successCount,
			FailedCount:    len(failedItems),
			FailedItems:    failedItems,
		})

		// 批间延迟（避免风控）
		if batch < totalBatches-1 {
			if err := sleepJitter(ctx, 100*time.Millisecond, 100*time.Millisecond); err != nil {
				return // context cancelled
			}
		}
	}

	// 完成更新
	s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
		ID:          jobID,
		Status:      "completed",
		EndAt:       time.Now(),
		FailedItems: failedItems,
	})

	// 清理过期更新历史（保留最近 100 条）
	if err := s.store.PruneUpdateJobs(ctx, 100); err != nil {
		s.log.Warn("prune update jobs failed", "error", err)
	}
}

// GetUpdateJob 获取更新任务
func (s *Service) GetUpdateJob(ctx context.Context, jobID string) (StockV2UpdateJob, error) {
	return s.store.GetUpdateJob(ctx, jobID)
}

// GetLatestUpdateJob 获取最新的更新任务
func (s *Service) GetLatestUpdateJob(ctx context.Context) (StockV2UpdateJob, error) {
	return s.store.GetLatestUpdateJob(ctx)
}

// ListUpdateJobs 获取更新任务列表
func (s *Service) ListUpdateJobs(ctx context.Context, limit int) ([]StockV2UpdateJob, error) {
	return s.store.ListUpdateJobs(ctx, limit)
}

// GetUpdateProgress 获取更新进度
func (s *Service) GetUpdateProgress(ctx context.Context, jobID string) (StockV2UpdateProgress, error) {
	return s.store.GetUpdateProgress(ctx, jobID)
}

// CreateOrUpdateSettings 创建或更新配置
func (s *Service) CreateOrUpdateSettings(ctx context.Context, req RequestCreateOrUpdateSettings) (StockV2Settings, error) {
	// 应用更新
	settings := s.settings
	if req.AutoUpdateEnabled != nil {
		settings.AutoUpdateEnabled = *req.AutoUpdateEnabled
	}
	if req.UpdateIntervalSec != nil {
		settings.UpdateIntervalSec = *req.UpdateIntervalSec
	}
	if req.ProxyEnabled != nil {
		settings.ProxyEnabled = *req.ProxyEnabled
	}
	if req.ProxyType != nil {
		settings.ProxyType = *req.ProxyType
	}
	if req.ProxyHost != nil {
		settings.ProxyHost = *req.ProxyHost
	}
	if req.ProxyPort != nil {
		settings.ProxyPort = *req.ProxyPort
	}
	if req.DailyBarsAutoEnabled != nil {
		settings.DailyBarsAutoEnabled = *req.DailyBarsAutoEnabled
	}

	// 保存配置
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		return StockV2Settings{}, wrapError(err, "save settings")
	}

	// 更新本地配置
	prevAuto := s.settings.AutoUpdateEnabled
	prevDaily := s.settings.DailyBarsAutoEnabled
	prevInterval := s.settings.UpdateIntervalSec
	s.settings = settings

	// 主数据自动更新 或 日 K 定时增量 任一开启都需要后台调度。
	// 任一开关从关→开，或主数据周期变化时重启后台，以拾取新 ticker。
	needBG := settings.AutoUpdateEnabled || settings.DailyBarsAutoEnabled
	prevNeedBG := prevAuto || prevDaily
	if needBG {
		if !prevNeedBG || (prevAuto && prevInterval != settings.UpdateIntervalSec) {
			if prevNeedBG {
				s.StopBackground()
			}
			// 后台任务用独立 context，不随请求结束而取消
			s.StartBackground(context.Background())
		}
	} else {
		s.StopBackground()
	}

	return settings, nil
}

// GetSettings 获取配置
func (s *Service) GetSettings(ctx context.Context) (StockV2Settings, error) {
	return s.store.GetSettings(ctx)
}

// GetInstruments 获取股票主数据
func (s *Service) GetInstruments(ctx context.Context, limit, offset int) ([]StockV2Instrument, error) {
	return s.store.GetInstruments(ctx, limit, offset)
}

// CountInstruments 获取股票主数据总数
func (s *Service) CountInstruments(ctx context.Context) (int, error) {
	return s.store.CountInstruments(ctx)
}

// GetInstrumentsByMarket 根据市场获取股票列表
func (s *Service) GetInstrumentsByMarket(ctx context.Context, market string) ([]StockV2Instrument, error) {
	return s.store.GetInstrumentsByMarket(ctx, market)
}

// SearchInstruments 搜索股票（按代码或名称模糊匹配）
func (s *Service) SearchInstruments(ctx context.Context, keyword string, limit int) ([]StockV2Instrument, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return s.store.SearchInstruments(ctx, keyword, limit)
}

// StartBackground 启动后台任务
func (s *Service) StartBackground(ctx context.Context) {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()

	if s.bgCancel != nil {
		return // 已经在运行
	}

	bgCtx, cancel := context.WithCancel(ctx)
	s.bgCancel = cancel

	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runScheduledUpdater(bgCtx)
	}()

	// 日 K 每日定时增量调度（独立 goroutine，受 DailyBarsAutoEnabled 开关控制）
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runDailyBarsScheduler(bgCtx)
	}()

	// 监控任务周期调度（独立 goroutine，按各 monitor task 的 enabled/interval 触发）
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runScheduledMonitors(bgCtx)
	}()
}

// StopBackground 停止后台任务
func (s *Service) StopBackground() {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()

	if s.bgCancel != nil {
		s.bgCancel()
		s.bgCancel = nil
	}

	s.bgWg.Wait()
}

// runScheduledUpdater 运行定时更新器
func (s *Service) runScheduledUpdater(ctx context.Context) {
	interval := time.Duration(s.settings.UpdateIntervalSec) * time.Second
	if interval <= 0 {
		interval = 1 * time.Hour // 防御：0 或负值时给个默认值
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.settings.AutoUpdateEnabled {
				continue
			}

			// 检查是否需要执行更新
			s.checkAndExecuteScheduledUpdate(ctx)
		}
	}
}

// checkAndExecuteScheduledUpdate 检查并执行定时更新
func (s *Service) checkAndExecuteScheduledUpdate(ctx context.Context) {
	now := time.Now()
	interval := time.Duration(s.settings.UpdateIntervalSec) * time.Second

	// 检查是否到了更新时间
	if now.Sub(s.settings.LastScheduledUpdate) < interval {
		return
	}

	// 执行更新
	s.log.Info("executing scheduled universe update")
	if _, err := s.ExecuteUniverseUpdate(ctx, "scheduled", "auto-updater"); err != nil {
		s.log.Error("scheduled update failed", "error", err)
		return
	}

	// 更新最后一次执行时间
	settings, _ := s.GetSettings(ctx)
	settings.LastScheduledUpdate = now
	s.store.CreateOrUpdateSettings(ctx, settings)
	s.settings = settings
}

// runScheduledMonitors 周期检查各监控任务的 enabled/interval，到点触发对应 scan。
// 实际是否执行由各 task config 的 enabled 控制；未启用或未到周期则跳过。
func (s *Service) runScheduledMonitors(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickScheduledMonitors(ctx)
		}
	}
}

func (s *Service) tickScheduledMonitors(ctx context.Context) {
	configs, err := s.store.ListMonitorTaskConfigs(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for taskType, cfg := range configs {
		if !cfg.Enabled || cfg.IntervalSeconds <= 0 {
			continue
		}
		def, ok := monitorTaskDefinition(taskType)
		if !ok || !def.Runnable {
			continue
		}
		if taskType == MonitorTaskLatestQuoteRefresh {
			state, _ := s.store.GetQuoteRefreshTaskState(ctx, taskType)
			if state != nil && state.Status == MonitorRunStatusRunning {
				continue
			}
			if state != nil && !state.StartedAt.IsZero() && now.Sub(state.StartedAt) < time.Duration(cfg.IntervalSeconds)*time.Second {
				continue
			}
			if _, err := s.RunLatestQuoteRefreshTask(ctx, MonitorTriggerScheduled); err != nil {
				s.log.Warn("scheduled quote refresh failed", "error", err)
			}
			continue
		}
		latest, _ := s.store.GetLatestMonitorRun(ctx, taskType)
		if latest != nil && latest.Status == MonitorRunStatusRunning {
			continue
		}
		if latest != nil && !latest.StartedAt.IsZero() && now.Sub(latest.StartedAt) < time.Duration(cfg.IntervalSeconds)*time.Second {
			continue
		}
		if _, err := s.RunMonitorTask(ctx, taskType, MonitorTriggerScheduled); err != nil {
			s.log.Warn("scheduled monitor run failed", "task_type", taskType, "error", err)
		}
	}
}

// Snapshot 获取 V2 工作台快照数据。
//
// Snapshot 服务于页面首屏恢复和侧栏概览，所以只带足够 UI 展示的轻量数据：
// 组合/持仓、最近任务、设置，以及一小段主数据样本。不要把它当作全量主
// 数据接口；判断股票主数据是否完整时，应调用 GetInstruments/CountInstruments
// 或 HTTP 层的 /api/stockv2/instruments 分页接口。
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	// 获取投资组合和持仓
	portfolios, err := s.ListPortfolios(ctx)
	if err != nil {
		return Snapshot{}, wrapError(err, "get portfolios")
	}

	// 获取首屏主数据样本。这里刻意限制为 1000 条，避免 snapshot 变成
	// 大响应；全量数量和分页内容由 /api/stockv2/instruments 提供。
	instruments, err := s.GetInstruments(ctx, 1000, 0)
	if err != nil {
		return Snapshot{}, wrapError(err, "get instruments")
	}

	// 获取更新任务
	jobs, err := s.ListUpdateJobs(ctx, 10)
	if err != nil {
		return Snapshot{}, wrapError(err, "get update jobs")
	}

	// 获取配置
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return Snapshot{}, wrapError(err, "get settings")
	}

	return Snapshot{
		Portfolios:  portfolios,
		Instruments: instruments,
		UpdateJobs:  jobs,
		Settings:    settings,
		LastUpdate:  time.Now(),
	}, nil
}

// Close 关闭服务，清理资源
func (s *Service) Close() error {
	// 停止后台任务
	s.bgMu.Lock()
	if s.bgCancel != nil {
		s.bgCancel()
		s.bgWg.Wait()
	}
	s.bgMu.Unlock()
	// 关闭 agent task pool
	if s.agentTaskPool != nil {
		s.agentTaskPool.Close()
	}
	// 关闭底层 DB 连接
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			s.log.Warn("stockv2 store close failed", "error", err)
		}
	}
	return nil
}
