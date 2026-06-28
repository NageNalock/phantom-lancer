package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Service 主业务服务
type Service struct {
	store         *Store
	log           *slog.Logger
	httpClient    *http.Client
	bgMu          sync.Mutex
	bgCancel      context.CancelFunc
	bgWg          sync.WaitGroup
	settings      StockV2Settings
	baseProfileMu sync.Mutex

	universeSource  *UniverseDataSource
	dailyBarsSource *DailyBarsSource
	newsAdapters    map[string]NewsSourceAdapter
	newsLinker      NewsEventLinker

	// Agent 执行相关
	agentTaskPool     *agentTaskPool
	agentExecutor     AgentExecutor
	agentCodexCommand func(ctx context.Context, args ...string) ([]byte, error)
	agentMCPMu        sync.RWMutex
	agentMCPServer    *http.Server
	agentMCPURL       string
}

// NewService 创建新的股票V2服务
func NewService(store *Store, log *slog.Logger, httpClient *http.Client) *Service {
	svc := &Service{
		store:           store,
		log:             log,
		httpClient:      httpClient,
		universeSource:  NewUniverseDataSource(nil, httpClient),
		dailyBarsSource: NewDailyBarsSource(nil, httpClient),
		newsAdapters:    map[string]NewsSourceAdapter{},
		agentTaskPool:   newAgentTaskPool(defaultCleanupInterval),
		agentCodexCommand: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "codex", args...).CombinedOutput()
		},
	}
	svc.newsAdapters[NewsSourceJin10] = jin10NewsSourceAdapter{httpClient: httpClient}
	svc.newsAdapters[NewsSourceFinancialJuice] = financialJuiceNewsSourceAdapter{service: svc}
	return svc
}

func (s *Service) WithNewsSourceAdapter(adapter NewsSourceAdapter) *Service {
	if adapter == nil || strings.TrimSpace(adapter.SourceName()) == "" {
		return s
	}
	if s.newsAdapters == nil {
		s.newsAdapters = map[string]NewsSourceAdapter{}
	}
	s.newsAdapters[adapter.SourceName()] = adapter
	return s
}

func (s *Service) WithNewsEventLinker(linker NewsEventLinker) *Service {
	s.newsLinker = linker
	return s
}

// AgentExecutor 是 Agent 执行器接口。
type AgentExecutor interface {
	ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName string) (*AgentExecutorOutput, error)
	ExecuteStrategyGeneration(ctx context.Context, taskID string, pack StrategyGenerationContext, modelName string) (*AgentExecutorOutput, error)
	ExecuteStockProfileSummary(ctx context.Context, taskID string, profile StockProfile, modelName string) (*AgentExecutorOutput, error)
}

// WithCodexCLIExecutor 注入 Codex CLI 执行器。
func (s *Service) WithCodexCLIExecutor(binary, codexHome, mcpURL string) *Service {
	s.agentExecutor = newCodexCLIExecutor(s.log, binary, codexHome, mcpURL, s.agentTaskPool)
	if trimmed := strings.TrimSpace(binary); trimmed != "" {
		s.agentCodexCommand = func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, trimmed, args...).CombinedOutput()
		}
	}
	return s
}

type AgentMCPStatus struct {
	Enabled       bool     `json:"enabled"`
	ServerName    string   `json:"serverName"`
	Transport     string   `json:"transport"`
	URL           string   `json:"url,omitempty"`
	RequiredTools []string `json:"requiredTools"`
}

func (s *Service) StartAgentMCPServer() (string, error) {
	s.agentMCPMu.Lock()
	defer s.agentMCPMu.Unlock()
	if s.agentMCPServer != nil && s.agentMCPURL != "" {
		return s.agentMCPURL, nil
	}
	if s.agentTaskPool == nil {
		return "", errors.New("stockv2 agent task pool is not configured")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/stockv2/agent/mcp", s.handleAgentMCPRequest)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	url := "http://" + ln.Addr().String() + "/api/stockv2/agent/mcp"
	s.agentMCPServer = srv
	s.agentMCPURL = url

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && s.log != nil {
			s.log.Warn("stockv2 agent MCP loopback server stopped", "error", err)
		}
	}()
	return url, nil
}

func (s *Service) AgentMCPStatus() AgentMCPStatus {
	s.agentMCPMu.RLock()
	defer s.agentMCPMu.RUnlock()
	return AgentMCPStatus{
		Enabled:       s.agentMCPServer != nil && s.agentMCPURL != "",
		ServerName:    codexStockAgentMCPName,
		Transport:     "loopback_http",
		URL:           s.agentMCPURL,
		RequiredTools: stockAgentMCPRequiredTools(),
	}
}

func (s *Service) handleAgentMCPRequest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	resp := s.HandleMCPRequest(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
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
	settings = s.normalizeDataAssetMaintenanceSettings(ctx, settings)
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

	// 从主数据补全标的名称和市场
	inst, _ := s.store.GetInstrument(ctx, req.Symbol)
	name := req.Name
	if name == "" && inst.Name != "" {
		name = inst.Name
	}
	market := req.Market
	if market == "" && inst.Market != "" {
		market = inst.Market
	}

	// 解析建仓时间
	acquiredAt, err := parseTransactionExecutedAt(req.AcquiredAt)
	if err != nil {
		return StockV2Holding{}, err
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
		AcquiredAt:        acquiredAt,
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
	if req.AcquiredAt != nil {
		acquiredAt, err := parseTransactionExecutedAt(*req.AcquiredAt)
		if err != nil {
			return StockV2Holding{}, err
		}
		holding.AcquiredAt = acquiredAt
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

// RecordTransaction 记录一笔买入/卖出交易,原子地写流水、调整组合现金、调整持仓。
// 买入扣现金(允许变负)、新建或加仓持仓(加权平均成本);卖出加现金、减仓,清仓则删除持仓,
// 超量/卖空返回 ErrInsufficientHolding。事务提交后刷新一次估值并写快照(失败仅告警)。
func (s *Service) RecordTransaction(ctx context.Context, portfolioID string, req RequestRecordTransaction) (TransactionResult, error) {
	side := strings.TrimSpace(req.Side)
	if side != "buy" && side != "sell" {
		return TransactionResult{}, ErrInvalidTransactionSide
	}
	symbol := strings.TrimSpace(req.Symbol)
	if symbol == "" || req.Quantity <= 0 || req.Price <= 0 {
		return TransactionResult{}, errors.New("symbol, quantity and price are required")
	}

	executedAt, err := parseTransactionExecutedAt(req.ExecutedAt)
	if err != nil {
		return TransactionResult{}, err
	}

	// 主数据补全 name/market(沿用 CreateHolding 逻辑)
	inst, _ := s.store.GetInstrument(ctx, symbol)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = inst.Name
	}
	market := strings.TrimSpace(req.Market)
	if market == "" {
		market = inst.Market
	}

	amount := req.Quantity * req.Price
	now := time.Now()
	result := TransactionResult{}

	if err := s.store.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		t := StockV2Transaction{
			ID:          generateID(),
			PortfolioID: portfolioID,
			Symbol:      symbol,
			Market:      market,
			Name:        name,
			Side:        side,
			Quantity:    req.Quantity,
			Price:       req.Price,
			Amount:      amount,
			ExecutedAt:  executedAt,
			Note:        strings.TrimSpace(req.Note),
			CreatedAt:   now,
		}
		if err := insertTransactionWithTx(ctx, tx, t); err != nil {
			return err
		}
		result.Transaction = t

		cash, err := getPortfolioCashWithTx(ctx, tx, portfolioID)
		if err != nil {
			return err
		}

		holding, found, err := getHoldingBySymbolWithTx(ctx, tx, portfolioID, symbol)
		if err != nil {
			return err
		}

		if side == "buy" {
			newCash := cash - amount
			if found {
				// 加仓:加权平均成本
				newQty := holding.Quantity + req.Quantity
				holding.CostPrice = (holding.CostPrice*holding.Quantity + amount) / newQty
				holding.Quantity = newQty
				holding.AvailableQuantity = holding.AvailableQuantity + req.Quantity
				holding.UpdatedAt = now
				if err := updateHoldingWithTx(ctx, tx, holding); err != nil {
					return err
				}
			} else {
				holding = StockV2Holding{
					ID:                generateID(),
					PortfolioID:       portfolioID,
					Symbol:            symbol,
					Market:            market,
					Name:              name,
					Quantity:          req.Quantity,
					AvailableQuantity: req.Quantity,
					CostPrice:         req.Price,
					AcquiredAt:        executedAt,
					CreatedAt:         now,
					UpdatedAt:         now,
				}
				if err := createHoldingWithTx(ctx, tx, holding); err != nil {
					return err
				}
			}
			result.Holding = holding
			return updatePortfolioCashWithTx(ctx, tx, portfolioID, newCash, now)
		}

		// 卖出
		if !found || holding.Quantity < req.Quantity-1e-9 {
			return ErrInsufficientHolding
		}
		newCash := cash + amount
		holding.Quantity -= req.Quantity
		holding.AvailableQuantity -= req.Quantity
		holding.UpdatedAt = now
		if holding.Quantity <= 1e-9 {
			if err := deleteHoldingWithTx(ctx, tx, holding.ID); err != nil {
				return err
			}
			result.HoldingCleared = true
		} else {
			if err := updateHoldingWithTx(ctx, tx, holding); err != nil {
				return err
			}
			result.Holding = holding
		}
		return updatePortfolioCashWithTx(ctx, tx, portfolioID, newCash, now)
	}); err != nil {
		return TransactionResult{}, err
	}

	// 交易已落库:重读完整组合(含最新 cash),再刷新估值拉最新价 + 写快照。
	if portfolio, err := s.store.GetPortfolio(ctx, portfolioID); err == nil {
		result.Portfolio = portfolio
	}
	if _, err := s.RefreshPortfolioValuation(ctx, portfolioID, "trade"); err != nil {
		if s.log != nil {
			s.log.Warn("refresh portfolio valuation after trade failed", "portfolioId", portfolioID, "err", err)
		}
	}
	return result, nil
}

// ListTransactions 列出组合的交易流水,limit<=0 不限(资产曲线回算用)。
func (s *Service) ListTransactions(ctx context.Context, portfolioID string, limit int) ([]StockV2Transaction, error) {
	return s.store.ListTransactions(ctx, portfolioID, limit)
}

// parseTransactionExecutedAt 解析成交时间:空→now;支持 RFC3339 与 datetime-local 等本地格式;
// 拒绝超过当前时间 5 分钟的未来时间(留时钟漂移容差)。
func parseTransactionExecutedAt(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now(), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			if d := time.Since(t); d < -5*time.Minute {
				return time.Time{}, errors.New("executedAt cannot be in the future")
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid executedAt: %q", raw)
}

// ExecuteUniverseUpdate 执行统一数据资产维护（标的 / 最新价 / 日 K 覆盖）
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

// runUniverseUpdate 运行统一数据资产维护任务
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

	// 获取默认标的代码列表
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
	flushProgress := func(includeFailedItems bool) {
		progress.ProcessedCount = processedCount
		progress.SuccessCount = successCount
		progress.ErrorCount = len(failedItems)
		if len(failedItems) > 0 {
			progress.LastError = failedItems[len(failedItems)-1].Reason
		}
		if err := s.store.UpdateUpdateProgress(ctx, progress); err != nil {
			s.log.Warn("update maintenance progress failed", "job_id", jobID, "error", err)
		}
		update := StockV2UpdateJob{
			ID:             jobID,
			TotalCount:     totalCount,
			ProcessedCount: processedCount,
			SuccessCount:   successCount,
			FailedCount:    len(failedItems),
		}
		if includeFailedItems {
			update.FailedItems = failedItems
		}
		if err := s.store.UpdateUpdateJob(ctx, update); err != nil {
			s.log.Warn("update maintenance job progress failed", "job_id", jobID, "error", err)
		}
	}

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
			progress.CurrentBatchProgress = len(batchSymbols)
			progress.CurrentSymbol = batchSymbols[len(batchSymbols)-1]
			flushProgress(true)
			continue
		}

		// 构造返回结果 map，对比找失败的
		resultMap := make(map[string]StockV2Instrument, len(instruments))
		for _, inst := range instruments {
			resultMap[inst.Symbol] = inst
		}

		// 保存股票数据，并把历史日 K 覆盖纳入同一个数据资产维护任务。
		batchProcessed := 0
		for _, sym := range batchSymbols {
			progress.CurrentSymbol = sym
			failedBefore := len(failedItems)
			fetchedDailyBars := false
			inst, ok := resultMap[sym]
			if !ok {
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: "no data from source",
				})
			} else if err := s.upsertInstrumentWithProfile(ctx, inst); err != nil {
				s.log.Error("save instrument failed", "symbol", inst.Symbol, "error", err)
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: err.Error(),
				})
			} else {
				var err error
				fetchedDailyBars, err = s.maintainDailyBarsForInstrument(ctx, inst)
				if err != nil {
					s.log.Error("maintain daily bars failed", "symbol", inst.Symbol, "error", err)
					failedItems = append(failedItems, UpdateFailure{
						Symbol: sym,
						Reason: "daily bars: " + truncateDailyBarErr(err.Error()),
					})
				} else {
					successCount++
				}
			}

			processedCount++
			batchProcessed++
			progress.CurrentBatchProgress = batchProcessed
			flushProgress(len(failedItems) != failedBefore)

			if fetchedDailyBars {
				if err := sleepJitter(ctx, 80*time.Millisecond, 60*time.Millisecond); err != nil {
					return
				}
			}
		}

		// 批间延迟（避免风控）
		if batch < totalBatches-1 {
			if err := sleepJitter(ctx, 100*time.Millisecond, 100*time.Millisecond); err != nil {
				return // context cancelled
			}
		}
	}

	endAt := time.Now()
	// 完成更新
	s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
		ID:             jobID,
		Status:         "completed",
		TotalCount:     totalCount,
		ProcessedCount: processedCount,
		SuccessCount:   successCount,
		FailedCount:    len(failedItems),
		EndAt:          endAt,
		FailedItems:    failedItems,
	})
	s.recordDailyBarsLastRun(ctx, endAt)

	// 清理过期更新历史（保留最近 100 条）
	if err := s.store.PruneUpdateJobs(ctx, 100); err != nil {
		s.log.Warn("prune update jobs failed", "error", err)
	}
	s.maybeRunBaseProfileMaintenance(ctx, "universe_update")
}

func (s *Service) maintainDailyBarsForInstrument(ctx context.Context, inst StockV2Instrument) (bool, error) {
	quality, err := s.GetDailyBarsQuality(ctx, inst.Symbol, DailyBarAdjustedNone)
	if err != nil {
		return false, err
	}
	if !dailyBarsNeedsMaintenance(quality) {
		return false, nil
	}

	start, end := dailyBarRangeStartEnd(DailyBarRange1Y, time.Now())
	if quality.HasData && quality.Meets250 && quality.Stale && quality.LatestDate != "" {
		start = quality.LatestDate
	}
	_, err = s.ensureOneSymbol(ctx, inst.Symbol, inst.Market, start, end, DailyBarAdjustedNone)
	return true, err
}

func dailyBarsNeedsMaintenance(q DailyBarsQuality) bool {
	return !q.HasData || !q.Meets250 || q.Stale
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
	prevAuto := s.settings.AutoUpdateEnabled
	prevBaseProfile := s.settings.BaseProfileAutoMaintainEnabled
	prevInterval := s.settings.UpdateIntervalSec
	prevBaseProfileInterval := s.settings.BaseProfileMaintainIntervalSeconds
	prevNewsBG := s.hasEnabledNewsSources(ctx)
	settings := s.settings
	if req.AutoUpdateEnabled != nil {
		settings.AutoUpdateEnabled = *req.AutoUpdateEnabled
	}
	if req.UpdateIntervalSec != nil {
		settings.UpdateIntervalSec = *req.UpdateIntervalSec
	}
	if req.DailyBarsAutoEnabled != nil {
		if *req.DailyBarsAutoEnabled {
			settings.AutoUpdateEnabled = true
		}
		settings.DailyBarsAutoEnabled = false
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
	if req.BaseProfileAutoMaintainEnabled != nil {
		settings.BaseProfileAutoMaintainEnabled = *req.BaseProfileAutoMaintainEnabled
	}
	if req.BaseProfileMaintainIntervalSeconds != nil {
		settings.BaseProfileMaintainIntervalSeconds = *req.BaseProfileMaintainIntervalSeconds
	}
	if settings.BaseProfileMaintainIntervalSeconds <= 0 {
		settings.BaseProfileMaintainIntervalSeconds = 86400
	}
	if req.BaseProfileDeepUpdateBatchSize != nil {
		settings.BaseProfileDeepUpdateBatchSize = *req.BaseProfileDeepUpdateBatchSize
	}
	if req.BaseProfileDeepUpdateAIBudget != nil {
		settings.BaseProfileDeepUpdateAIBudget = *req.BaseProfileDeepUpdateAIBudget
	}
	if req.BaseProfileDeepUpdateRateLimitMs != nil {
		settings.BaseProfileDeepUpdateRateLimitMs = *req.BaseProfileDeepUpdateRateLimitMs
	}
	settings.BaseProfileDeepUpdateBatchSize = normalizeStockProfileDeepUpdateBatchSize(settings.BaseProfileDeepUpdateBatchSize)
	settings.BaseProfileDeepUpdateAIBudget = normalizeStockProfileDeepUpdateAIBudget(settings.BaseProfileDeepUpdateAIBudget)
	settings.BaseProfileDeepUpdateRateLimitMs = normalizeStockProfileDeepUpdateRateLimitMs(settings.BaseProfileDeepUpdateRateLimitMs)
	if settings.BaseProfileAutoMaintainEnabled {
		interval := time.Duration(settings.BaseProfileMaintainIntervalSeconds) * time.Second
		if !prevBaseProfile {
			settings.BaseProfileNextMaintainAt = time.Now()
		} else if settings.BaseProfileLastMaintainAt.IsZero() {
			settings.BaseProfileNextMaintainAt = time.Now().Add(interval)
		} else {
			settings.BaseProfileNextMaintainAt = settings.BaseProfileLastMaintainAt.Add(interval)
		}
	} else {
		settings.BaseProfileNextMaintainAt = time.Time{}
	}

	// 保存配置
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		return StockV2Settings{}, wrapError(err, "save settings")
	}
	// 更新本地配置
	s.settings = settings

	// 数据资产自动维护 / base profile 自动维护任一开启都需要后台调度。
	// 任一开关从关→开，或数据资产周期变化时重启后台，以拾取新 ticker。
	newsBG := s.hasEnabledNewsSources(ctx)
	needBG := settings.AutoUpdateEnabled || settings.BaseProfileAutoMaintainEnabled || newsBG
	prevNeedBG := prevAuto || prevBaseProfile || prevNewsBG
	if needBG {
		if !prevNeedBG ||
			(prevAuto && prevInterval != settings.UpdateIntervalSec) ||
			(prevBaseProfile && prevBaseProfileInterval != settings.BaseProfileMaintainIntervalSeconds) {
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
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return StockV2Settings{}, err
	}
	return s.normalizeDataAssetMaintenanceSettings(ctx, settings), nil
}

func (s *Service) normalizeDataAssetMaintenanceSettings(ctx context.Context, settings StockV2Settings) StockV2Settings {
	if !settings.DailyBarsAutoEnabled {
		return settings
	}
	if !settings.AutoUpdateEnabled {
		settings.AutoUpdateEnabled = true
	}
	// ponytail: one-time compatibility shim for the removed standalone daily-bar scheduler.
	settings.DailyBarsAutoEnabled = false
	if s.store != nil {
		if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil && s.log != nil {
			s.log.Warn("migrate legacy daily bars auto setting failed", "error", err)
		}
	}
	return settings
}

// GetInstruments 获取标的主数据
func (s *Service) GetInstruments(ctx context.Context, limit, offset int) ([]StockV2Instrument, error) {
	return s.store.GetInstruments(ctx, limit, offset)
}

func (s *Service) GetInstrumentsFiltered(ctx context.Context, market, instrumentType string, limit, offset int) ([]StockV2Instrument, error) {
	return s.store.GetInstrumentsFiltered(ctx, market, instrumentType, limit, offset)
}

// CountInstruments 获取标的主数据总数
func (s *Service) CountInstruments(ctx context.Context) (int, error) {
	return s.store.CountInstruments(ctx)
}

func (s *Service) CountInstrumentsFiltered(ctx context.Context, market, instrumentType string) (int, error) {
	return s.store.CountInstrumentsFiltered(ctx, market, instrumentType)
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

func (s *Service) SearchInstrumentsFiltered(ctx context.Context, keyword, market, instrumentType string, limit int) ([]StockV2Instrument, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return s.store.SearchInstrumentsFiltered(ctx, keyword, market, instrumentType, limit)
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

	// 监控任务周期调度（独立 goroutine，按各 monitor task 的 enabled/interval 触发）
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runScheduledMonitors(bgCtx)
	}()

	// 消息面源调度（按各 source 的 next_run_at / backoff 独立触发）。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runNewsSourceScheduler(bgCtx)
	}()

	// base profile 自动维护：只维护确定性画像，不触发全市场 AI。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runBaseProfileMaintenanceScheduler(bgCtx)
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

// runScheduledUpdater 运行数据资产定时维护器
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

// checkAndExecuteScheduledUpdate 检查并执行数据资产维护
func (s *Service) checkAndExecuteScheduledUpdate(ctx context.Context) {
	now := time.Now()
	interval := time.Duration(s.settings.UpdateIntervalSec) * time.Second

	// 检查是否到了更新时间
	if now.Sub(s.settings.LastScheduledUpdate) < interval {
		return
	}

	// 执行维护
	s.log.Info("executing scheduled stock data asset maintenance")
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
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	s.tickScheduledMonitors(ctx)
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
	storedConfigs, err := s.store.ListMonitorTaskConfigs(ctx)
	if err != nil {
		return
	}
	configs := make(map[string]MonitorTaskConfig)
	for _, def := range builtinMonitorTaskDefinitions() {
		cfg := def.DefaultConfig
		if stored, ok := storedConfigs[def.TaskType]; ok {
			cfg = stored
		}
		configs[def.TaskType] = cfg
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
				if s.log != nil {
					s.log.Warn("scheduled quote refresh failed", "error", err)
				}
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
			if s.log != nil {
				s.log.Warn("scheduled monitor run failed", "task_type", taskType, "error", err)
			}
		}
	}
}

// Snapshot 获取 V2 工作台快照数据。
//
// Snapshot 服务于页面首屏恢复和侧栏概览，所以只带足够 UI 展示的轻量数据：
// 组合/持仓、最近任务、设置，以及一小段主数据样本。不要把它当作全量主
// 数据接口；判断标的主数据是否完整时，应调用 GetInstruments/CountInstruments
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
	s.agentMCPMu.Lock()
	mcpServer := s.agentMCPServer
	s.agentMCPServer = nil
	s.agentMCPURL = ""
	s.agentMCPMu.Unlock()
	if mcpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := mcpServer.Shutdown(ctx); err != nil && s.log != nil {
			s.log.Warn("stockv2 agent MCP loopback shutdown failed", "error", err)
		}
		cancel()
	}
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
