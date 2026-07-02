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

	"phantom-lancer/internal/safelog"
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
	embeddingMu   sync.Mutex
	embeddingRun  bool
	// ponytail: process-local single-flight is enough for the single deployed Go service;
	// move to a persisted lease only if multiple StockV2 workers are introduced.
	newsPipelineMu  sync.Mutex
	newsPipelineRun bool
	quotePruneMu    sync.Mutex
	lastQuotePrune  time.Time

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
	pool := newAgentTaskPool(defaultCleanupInterval)
	svc := &Service{
		store:           store,
		log:             log,
		httpClient:      httpClient,
		universeSource:  NewUniverseDataSource(nil, httpClient),
		dailyBarsSource: NewDailyBarsSource(nil, httpClient),
		newsAdapters:    map[string]NewsSourceAdapter{},
		agentTaskPool:   pool,
		agentCodexCommand: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "codex", args...).CombinedOutput()
		},
	}
	pool.service = svc
	svc.newsAdapters[NewsSourceJin10] = jin10NewsSourceAdapter{httpClient: httpClient}
	svc.newsAdapters[NewsSourceFinancialJuice] = financialJuiceNewsSourceAdapter{service: svc}
	svc.markInterruptedRunningTasks(context.Background())
	return svc
}

func (s *Service) markInterruptedRunningTasks(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	reason := "interrupted by service restart before completion"
	failures := []struct {
		name string
		fn   func(context.Context, string) (int64, error)
	}{
		{name: "stock data asset maintenance", fn: s.store.FailRunningUpdateJobs},
		{name: "daily bar jobs", fn: s.store.FailRunningDailyBarJobs},
		{name: "quote refresh task", fn: s.store.FailRunningQuoteRefreshTasks},
		{name: "monitor runs", fn: s.store.FailRunningMonitorRuns},
		{name: "news source runs", fn: s.store.FailRunningNewsSourceStates},
	}
	for _, failure := range failures {
		count, err := failure.fn(ctx, reason)
		if err != nil {
			if s.log != nil {
				s.log.Warn("mark interrupted stockv2 tasks failed", "task_group", failure.name, "error", safelog.Text(err.Error(), 240))
			}
			continue
		}
		if count > 0 && s.log != nil {
			s.log.Warn("marked interrupted stockv2 tasks", "task_group", failure.name, "count", count)
		}
	}
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
	ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName string) (*AgentExecutorOutput, error)
	ExecuteOpportunityDiscovery(ctx context.Context, taskID string, pack OpportunityDiscoveryContext, modelName string) (*AgentExecutorOutput, error)
	ExecutePortfolioSentinel(ctx context.Context, taskID string, pack PortfolioSentinelContext, modelName string) (*AgentExecutorOutput, error)
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

type pendingUniverseDailyBars struct {
	Instrument StockV2Instrument
	Bars       []StockV2DailyBar
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
			s.log.Warn("stockv2 agent MCP loopback server stopped", "error", safelog.Text(err.Error(), 240))
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
		AllowBuy:             boolDefault(req.AllowBuy, true),
		AllowAdd:             boolDefault(req.AllowAdd, true),
		AllowReduce:          boolDefault(req.AllowReduce, true),
		AllowSell:            boolDefault(req.AllowSell, true),
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

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
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
			s.log.Warn("get portfolio holdings failed", "portfolio_id", portfolio.ID, "error", safelog.Text(err.Error(), 240))
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
			s.log.Warn("refresh portfolio valuation after trade failed", "portfolio_id", portfolioID, "trigger_source", "trade", "error", safelog.Text(err.Error(), 240))
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
	go s.runUniverseUpdate(context.Background(), job)

	return job, nil
}

// runUniverseUpdate 运行统一数据资产维护任务
func (s *Service) runUniverseUpdate(ctx context.Context, job StockV2UpdateJob) {
	jobID := job.ID
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("runUniverseUpdate panicked", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "panic", r)
			}
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
		if s.log != nil {
			s.log.Error("update job total count failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "total_count", totalCount, "error", safelog.Text(err.Error(), 240))
		}
	}

	// 分批更新股票
	const batchSize = 500
	totalBatches := (totalCount + batchSize - 1) / batchSize
	var failedItems []UpdateFailure
	successCount := 0
	processedCount := 0
	freshnessWindow := s.universeMaintenanceFreshnessWindow()
	flushProgress := func(includeFailedItems bool) {
		progress.ProcessedCount = processedCount
		progress.SuccessCount = successCount
		progress.ErrorCount = len(failedItems)
		if len(failedItems) > 0 {
			progress.LastError = failedItems[len(failedItems)-1].Reason
		}
		if err := s.store.UpdateUpdateProgress(ctx, progress); err != nil {
			if s.log != nil {
				s.log.Warn("update maintenance progress failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "processed_count", processedCount, "failed_count", len(failedItems), "current_symbol", progress.CurrentSymbol, "error", safelog.Text(err.Error(), 240))
			}
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
			if s.log != nil {
				s.log.Warn("update maintenance job progress failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "processed_count", processedCount, "failed_count", len(failedItems), "error", safelog.Text(err.Error(), 240))
			}
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

		workSymbols := make([]string, 0, len(batchSymbols))
		batchNow := time.Now()
		qualityBySymbol, qualityErr := s.dailyBarsQualityForUniverseBatch(ctx, batchSymbols)
		if qualityErr != nil && s.log != nil {
			s.log.Warn("batch stock data asset daily bar quality failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "batch", batch+1, "symbol_count", len(batchSymbols), "error", safelog.Text(qualityErr.Error(), 240))
		}
		for _, sym := range batchSymbols {
			quality, hasQuality := qualityBySymbol[sym]
			if qualityErr != nil {
				hasQuality = false
			}
			skip, err := s.shouldSkipFreshUniverseSymbol(ctx, sym, batchNow, freshnessWindow, quality, hasQuality)
			if err != nil {
				if s.log != nil {
					s.log.Warn("check stock data asset freshness failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "symbol", sym, "freshness_window", freshnessWindow.String(), "error", safelog.Text(err.Error(), 240))
				}
				workSymbols = append(workSymbols, sym)
				continue
			}
			if skip {
				processedCount++
				successCount++
				progress.CurrentBatchProgress++
				progress.CurrentSymbol = sym
				continue
			}
			workSymbols = append(workSymbols, sym)
		}
		if progress.CurrentBatchProgress > 0 {
			flushProgress(false)
		}
		if len(workSymbols) == 0 {
			continue
		}

		// 获取这批股票数据
		instruments, err := s.universeSource.FetchStockUniverse(ctx, workSymbols)
		if err != nil {
			if s.log != nil {
				s.log.Error("stock data asset maintenance batch fetch failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "batch", batch+1, "total_batches", totalBatches, "batch_size", len(workSymbols), "first_symbol", workSymbols[0], "last_symbol", workSymbols[len(workSymbols)-1], "error", safelog.Text(err.Error(), 300))
			}
			// 整批失败，逐个记入失败列表
			for _, sym := range workSymbols {
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: safelog.Text(err.Error(), 240),
				})
			}
			processedCount += len(workSymbols)
			progress.CurrentBatchProgress = len(batchSymbols)
			progress.CurrentSymbol = workSymbols[len(workSymbols)-1]
			flushProgress(true)
			continue
		}

		// 构造返回结果 map，对比找失败的
		resultMap := make(map[string]StockV2Instrument, len(instruments))
		for _, inst := range instruments {
			resultMap[inst.Symbol] = inst
		}

		// 保存股票数据，并把历史日 K 覆盖纳入同一个数据资产维护任务。
		batchProcessed := progress.CurrentBatchProgress
		pendingDailyBars := make([]pendingUniverseDailyBars, 0, universeDailyBarsFlushSymbols)
		flushPendingDailyBars := func() bool {
			if len(pendingDailyBars) == 0 {
				return false
			}
			totalBars := 0
			for _, pending := range pendingDailyBars {
				totalBars += len(pending.Bars)
			}
			bars := make([]StockV2DailyBar, 0, totalBars)
			for _, pending := range pendingDailyBars {
				bars = append(bars, pending.Bars...)
			}
			if err := s.store.UpsertDailyBars(ctx, bars); err != nil {
				if s.log != nil {
					s.log.Error("stock data asset maintenance daily bars batch save failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "symbol_count", len(pendingDailyBars), "bar_count", len(bars), "error", safelog.Text(err.Error(), 300))
				}
				for _, pending := range pendingDailyBars {
					failedItems = append(failedItems, UpdateFailure{
						Symbol: pending.Instrument.Symbol,
						Reason: safelog.Text("daily bars save: "+truncateDailyBarErr(err.Error()), 240),
					})
				}
			} else {
				successCount += len(pendingDailyBars)
			}
			pendingDailyBars = pendingDailyBars[:0]
			return true
		}
		for _, sym := range workSymbols {
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
				if s.log != nil {
					s.log.Error("stock data asset maintenance save instrument failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "symbol", inst.Symbol, "market", inst.Market, "instrument_type", inst.InstrumentType, "error", safelog.Text(err.Error(), 300))
				}
				failedItems = append(failedItems, UpdateFailure{
					Symbol: sym,
					Reason: safelog.Text(err.Error(), 240),
				})
			} else {
				var err error
				var bars []StockV2DailyBar
				quality, hasQuality := qualityBySymbol[inst.Symbol]
				fetchedDailyBars, bars, err = s.fetchDailyBarsForInstrumentWithQuality(ctx, inst, quality, hasQuality && qualityErr == nil)
				if err != nil {
					if s.log != nil {
						s.log.Error("stock data asset maintenance daily bars failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "symbol", inst.Symbol, "market", inst.Market, "instrument_type", inst.InstrumentType, "error", safelog.Text(err.Error(), 300))
					}
					failedItems = append(failedItems, UpdateFailure{
						Symbol: sym,
						Reason: safelog.Text("daily bars: "+truncateDailyBarErr(err.Error()), 240),
					})
				} else if fetchedDailyBars {
					pendingDailyBars = append(pendingDailyBars, pendingUniverseDailyBars{
						Instrument: inst,
						Bars:       bars,
					})
					if len(pendingDailyBars) >= universeDailyBarsFlushSymbols {
						_ = flushPendingDailyBars()
					}
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
		if flushPendingDailyBars() {
			flushProgress(true)
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
	if len(failedItems) > 0 && s.log != nil {
		s.log.Warn("stock data asset maintenance completed with item failures", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "total_count", totalCount, "processed_count", processedCount, "success_count", successCount, "failed_count", len(failedItems), "failure_sample", stockV2FailureSample(failedItems, 5))
	}
	s.recordDailyBarsLastRun(ctx, endAt)

	// 清理过期更新历史（保留最近 100 条）
	if err := s.store.PruneUpdateJobs(ctx, 100); err != nil {
		s.log.Warn("prune update jobs failed", "retention_count", 100, "error", safelog.Text(err.Error(), 240))
	}
}

func (s *Service) maintainDailyBarsForInstrument(ctx context.Context, inst StockV2Instrument) (bool, error) {
	quality, err := s.GetDailyBarsQuality(ctx, inst.Symbol, DailyBarAdjustedNone)
	if err != nil {
		return false, err
	}
	return s.maintainDailyBarsForInstrumentWithQuality(ctx, inst, quality, true)
}

func (s *Service) maintainDailyBarsForInstrumentWithQuality(ctx context.Context, inst StockV2Instrument, quality DailyBarsQuality, hasQuality bool) (bool, error) {
	fetched, bars, err := s.fetchDailyBarsForInstrumentWithQuality(ctx, inst, quality, hasQuality)
	if err != nil || !fetched {
		return fetched, err
	}
	if err := s.store.UpsertDailyBars(ctx, bars); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) fetchDailyBarsForInstrumentWithQuality(ctx context.Context, inst StockV2Instrument, quality DailyBarsQuality, hasQuality bool) (bool, []StockV2DailyBar, error) {
	if !hasQuality {
		var err error
		quality, err = s.GetDailyBarsQuality(ctx, inst.Symbol, DailyBarAdjustedNone)
		if err != nil {
			return false, nil, err
		}
	}
	if !dailyBarsNeedsMaintenance(quality) {
		return false, nil, nil
	}

	start, end := dailyBarRangeStartEnd(DailyBarRange1Y, time.Now())
	if quality.HasData && quality.Meets250 && quality.Stale && quality.LatestDate != "" {
		start = quality.LatestDate
	}
	bars, err := s.dailyBarsSource.FetchDailyBars(ctx, inst.Symbol, inst.Market, start, end, DailyBarAdjustedNone, 1800)
	if err != nil {
		return true, nil, err
	}
	for i := range bars {
		bars[i].Symbol = inst.Symbol
	}
	return true, bars, nil
}

func dailyBarsNeedsMaintenance(q DailyBarsQuality) bool {
	return !q.HasData || !q.Meets250 || q.Stale
}

func (s *Service) universeMaintenanceFreshnessWindow() time.Duration {
	interval := time.Duration(s.settings.UpdateIntervalSec) * time.Second
	if interval <= 0 {
		interval = time.Hour
	}
	if interval > universeMaintenanceMaxFreshness {
		return universeMaintenanceMaxFreshness
	}
	return interval
}

func (s *Service) dailyBarsQualityForUniverseBatch(ctx context.Context, symbols []string) (map[string]DailyBarsQuality, error) {
	out := make(map[string]DailyBarsQuality, len(symbols))
	for start := 0; start < len(symbols); start += 100 {
		end := start + 100
		if end > len(symbols) {
			end = len(symbols)
		}
		qualities, err := s.GetDailyBarsQualityBatch(ctx, symbols[start:end], DailyBarAdjustedNone)
		if err != nil {
			return nil, err
		}
		for symbol, quality := range qualities {
			out[symbol] = quality
		}
	}
	return out, nil
}

func (s *Service) shouldSkipFreshUniverseSymbol(ctx context.Context, symbol string, now time.Time, freshness time.Duration, quality DailyBarsQuality, hasQuality bool) (bool, error) {
	inst, err := s.store.GetInstrument(ctx, symbol)
	if errors.Is(err, ErrInstrumentNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if inst.UpdatedAt.IsZero() || now.Sub(inst.UpdatedAt) > freshness {
		return false, nil
	}
	if !hasQuality {
		loaded, err := s.GetDailyBarsQuality(ctx, symbol, DailyBarAdjustedNone)
		if err != nil {
			return false, err
		}
		quality = loaded
	}
	// ponytail: existing updated_at plus daily-bar quality is enough for v1 fast-skip;
	// split per-source freshness if quote/profile cadences need independent SLAs.
	return !dailyBarsNeedsMaintenance(quality), nil
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
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return StockV2Settings{}, err
	}
	prevAuto := settings.AutoUpdateEnabled
	prevBaseProfile := settings.BaseProfileAutoMaintainEnabled
	prevInterval := settings.UpdateIntervalSec
	prevBaseProfileInterval := settings.BaseProfileMaintainIntervalSeconds
	prevNewsBG := s.hasEnabledNewsSources(ctx)
	now := time.Now()
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
		baseProfileIntervalChanged := prevBaseProfileInterval != settings.BaseProfileMaintainIntervalSeconds
		if !prevBaseProfile || (baseProfileIntervalChanged && settings.BaseProfileLastMaintainAt.IsZero()) {
			settings.BaseProfileNextMaintainAt = now
		} else if baseProfileIntervalChanged || settings.BaseProfileNextMaintainAt.IsZero() {
			next := settings.BaseProfileLastMaintainAt.Add(interval)
			if settings.BaseProfileLastMaintainAt.IsZero() || !next.After(now) {
				next = now
			}
			settings.BaseProfileNextMaintainAt = next
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
	embeddingBG := s.hasEmbeddingAutoMaintenanceEnabled(ctx)
	needBG := settings.AutoUpdateEnabled || settings.BaseProfileAutoMaintainEnabled || newsBG || embeddingBG
	prevNeedBG := prevAuto || prevBaseProfile || prevNewsBG || embeddingBG
	if needBG {
		restartBG := !prevNeedBG ||
			(prevAuto && prevInterval != settings.UpdateIntervalSec) ||
			(prevBaseProfile && prevBaseProfileInterval != settings.BaseProfileMaintainIntervalSeconds)
		if restartBG && prevNeedBG {
			s.StopBackground()
		}
		// 后台任务用独立 context，不随请求结束而取消；若已运行，StartBackground 会 no-op。
		s.StartBackground(context.Background())
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
			s.log.Warn("migrate legacy daily bars auto setting failed", "error", safelog.Text(err.Error(), 240))
		}
	}
	return settings
}

// GetInstruments 获取标的主数据
func (s *Service) GetInstruments(ctx context.Context, limit, offset int) ([]StockV2Instrument, error) {
	return s.store.GetInstruments(ctx, limit, offset)
}

func (s *Service) GetInstrumentsFiltered(ctx context.Context, market, instrumentType, profileStatus string, limit, offset int) ([]StockV2Instrument, error) {
	return s.store.GetInstrumentsFiltered(ctx, market, instrumentType, profileStatus, limit, offset)
}

// CountInstruments 获取标的主数据总数
func (s *Service) CountInstruments(ctx context.Context) (int, error) {
	return s.store.CountInstruments(ctx)
}

func (s *Service) CountInstrumentsFiltered(ctx context.Context, market, instrumentType, profileStatus string) (int, error) {
	return s.store.CountInstrumentsFiltered(ctx, market, instrumentType, profileStatus)
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

func (s *Service) SearchInstrumentsFiltered(ctx context.Context, keyword, market, instrumentType, profileStatus string, limit int) ([]StockV2Instrument, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return s.store.SearchInstrumentsFiltered(ctx, keyword, market, instrumentType, profileStatus, limit)
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

	// NewsLinkCandidate 保留策略：清理低价值旧候选，避免消息面弱关联无限增长。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runNewsLinkCandidateRetentionScheduler(bgCtx)
	}()

	// base profile 自动维护：只维护确定性画像，不触发全市场 AI。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runBaseProfileMaintenanceScheduler(bgCtx)
	}()

	// embedding asset 维护复用现有资产表和模型配置，不引入独立任务队列。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runEmbeddingMaintenanceScheduler(bgCtx)
	}()

	// embedding 向量紧凑存储迁移：前台只读 SQLite 进度，搬迁批次后台执行。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runEmbeddingVectorMigrationScheduler(bgCtx)
	}()

	// 组合哨兵按固定交易决策窗口触发,不复用旧 monitor runner。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runPortfolioSentinelScheduler(bgCtx)
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

const (
	scheduledUniverseUpdateHour      = 23
	scheduledUniverseUpdateWindowEnd = 6
	scheduledUpdaterPollInterval     = time.Minute
	universeDailyBarsFlushSymbols    = 50
)

// runScheduledUpdater 运行数据资产定时维护器
func (s *Service) runScheduledUpdater(ctx context.Context) {
	ticker := time.NewTicker(scheduledUpdaterPollInterval)
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
	s.checkAndExecuteScheduledUpdateAt(ctx, time.Now())
}

type scheduledUniverseUpdateDecision string

const (
	scheduledUniverseUpdateSkip    scheduledUniverseUpdateDecision = "skip"
	scheduledUniverseUpdateWait    scheduledUniverseUpdateDecision = "wait"
	scheduledUniverseUpdateConfirm scheduledUniverseUpdateDecision = "confirm"
	scheduledUniverseUpdateStart   scheduledUniverseUpdateDecision = "start"
)

func (s *Service) checkAndExecuteScheduledUpdateAt(ctx context.Context, now time.Time) {
	latest, latestOK := s.latestUniverseUpdateJob(ctx)
	decision, slotStart := decideScheduledUniverseUpdate(s.settings.LastScheduledUpdate, latest, latestOK, now)
	switch decision {
	case scheduledUniverseUpdateSkip, scheduledUniverseUpdateWait:
		return
	case scheduledUniverseUpdateConfirm:
		s.markScheduledUpdateChecked(ctx, now)
		return
	}

	// 执行维护
	if s.log != nil {
		s.log.Info("executing scheduled stock data asset maintenance", "trigger_type", "scheduled", "trigger_source", "auto-updater", "schedule", "daily_23:00", "slot_start", slotStart.Format(time.RFC3339Nano), "last_scheduled_update", s.settings.LastScheduledUpdate.Format(time.RFC3339Nano))
	}
	if _, err := s.ExecuteUniverseUpdate(ctx, "scheduled", "auto-updater"); err != nil {
		if s.log != nil {
			s.log.Error("scheduled update failed", "trigger_type", "scheduled", "trigger_source", "auto-updater", "schedule", "daily_23:00", "slot_start", slotStart.Format(time.RFC3339Nano), "last_scheduled_update", s.settings.LastScheduledUpdate.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 300))
		}
		return
	}
}

func decideScheduledUniverseUpdate(lastScheduled time.Time, latest StockV2UpdateJob, latestOK bool, now time.Time) (scheduledUniverseUpdateDecision, time.Time) {
	due, slotStart := shouldRunDailyUniverseUpdate(lastScheduled, now)
	if !due && !shouldRetryFailedUniverseUpdateSlot(latest, latestOK, slotStart, now) {
		return scheduledUniverseUpdateSkip, slotStart
	}
	if latestOK && latest.Status == "running" {
		return scheduledUniverseUpdateWait, slotStart
	}
	if latestOK && isRecentCompletedUniverseUpdate(latest, now, 24*time.Hour) {
		return scheduledUniverseUpdateConfirm, slotStart
	}
	return scheduledUniverseUpdateStart, slotStart
}

func shouldRunDailyUniverseUpdate(lastScheduled, now time.Time) (bool, time.Time) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	localNow := now.In(loc)
	hour := localNow.Hour()
	inWindow := hour >= scheduledUniverseUpdateHour || hour < scheduledUniverseUpdateWindowEnd
	if !inWindow {
		return false, time.Time{}
	}

	slotDay := localNow
	if hour < scheduledUniverseUpdateWindowEnd {
		slotDay = slotDay.AddDate(0, 0, -1)
	}
	slotStart := time.Date(slotDay.Year(), slotDay.Month(), slotDay.Day(), scheduledUniverseUpdateHour, 0, 0, 0, loc)
	if lastScheduled.IsZero() {
		return true, slotStart
	}
	return lastScheduled.In(loc).Before(slotStart), slotStart
}

func (s *Service) latestUniverseUpdateJob(ctx context.Context) (StockV2UpdateJob, bool) {
	latest, err := s.store.GetLatestUpdateJob(ctx)
	if errors.Is(err, ErrUpdateJobNotFound) {
		return StockV2UpdateJob{}, false
	}
	if err != nil {
		if s.log != nil {
			s.log.Warn("check latest stock data asset maintenance failed", "error", safelog.Text(err.Error(), 240))
		}
		return StockV2UpdateJob{}, false
	}
	return latest, true
}

func isRecentCompletedUniverseUpdate(latest StockV2UpdateJob, now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		interval = time.Hour
	}
	if latest.Status != "completed" {
		return false
	}
	return !latest.EndAt.IsZero() && now.Sub(latest.EndAt) >= 0 && now.Sub(latest.EndAt) < interval
}

func shouldRetryFailedUniverseUpdateSlot(latest StockV2UpdateJob, ok bool, slotStart, now time.Time) bool {
	if !ok || latest.Status != "failed" || slotStart.IsZero() {
		return false
	}
	createdAt := latest.CreatedAt.In(slotStart.Location())
	return !createdAt.Before(slotStart) && !createdAt.After(now.In(slotStart.Location()))
}

func (s *Service) markScheduledUpdateChecked(ctx context.Context, at time.Time) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("load settings before marking scheduled stock maintenance checked failed", "checked_at", at.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 240))
		}
		return
	}
	settings.LastScheduledUpdate = at
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		if s.log != nil {
			s.log.Warn("mark scheduled stock maintenance checked failed", "checked_at", at.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 240))
		}
		return
	}
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
		if s.log != nil {
			s.log.Warn("scheduled monitor config list failed", "error", safelog.Text(err.Error(), 240))
		}
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
		if taskType == MonitorTaskPortfolioSentinel {
			continue
		}
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
			if state != nil && quoteRefreshBackoffActive(*state, now) {
				continue
			}
			if state != nil && !state.StartedAt.IsZero() && now.Sub(state.StartedAt) < time.Duration(cfg.IntervalSeconds)*time.Second {
				continue
			}
			if _, err := s.RunLatestQuoteRefreshTask(ctx, MonitorTriggerScheduled); err != nil {
				if s.log != nil {
					s.log.Warn("scheduled quote refresh failed", "task_type", taskType, "trigger_type", MonitorTriggerScheduled, "interval_seconds", cfg.IntervalSeconds, "error", safelog.Text(err.Error(), 240))
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
				s.log.Warn("scheduled monitor run failed", "task_type", taskType, "trigger_type", MonitorTriggerScheduled, "interval_seconds", cfg.IntervalSeconds, "error", safelog.Text(err.Error(), 240))
			}
		}
	}
}

func quoteRefreshBackoffActive(state QuoteRefreshTaskState, now time.Time) bool {
	if state.Status != MonitorRunStatusFailed {
		return false
	}
	base := state.FinishedAt
	if base.IsZero() {
		base = state.UpdatedAt
	}
	if base.IsZero() {
		return false
	}
	return now.Before(base.Add(quoteRefreshFailureBackoff))
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

	instrumentTotal, err := s.store.CountInstruments(ctx)
	if err != nil {
		return Snapshot{}, wrapError(err, "count instruments")
	}

	// 获取首屏主数据样本。这里刻意限制为少量记录，避免 snapshot 变成
	// 大响应；真实总数和分页内容由 /api/stockv2/instruments 提供。
	instruments, err := s.GetInstruments(ctx, 20, 0)
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
		Portfolios:      portfolios,
		Instruments:     instruments,
		InstrumentTotal: instrumentTotal,
		UpdateJobs:      jobs,
		Settings:        settings,
		LastUpdate:      time.Now(),
	}, nil
}

func stockV2FailureSample(items []UpdateFailure, limit int) []UpdateFailure {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) < limit {
		limit = len(items)
	}
	out := make([]UpdateFailure, 0, limit)
	for _, item := range items[:limit] {
		out = append(out, UpdateFailure{
			Symbol: safelog.Text(item.Symbol, 80),
			Reason: safelog.Text(item.Reason, 240),
		})
	}
	return out
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
			s.log.Warn("stockv2 agent MCP loopback shutdown failed", "timeout", "2s", "error", safelog.Text(err.Error(), 240))
		}
		cancel()
	}
	// 关闭 agent task pool
	if s.agentTaskPool != nil {
		s.agentTaskPool.Close()
	}
	// 关闭底层 DB 连接
	if s.store != nil {
		if err := s.store.Close(); err != nil && s.log != nil {
			s.log.Warn("stockv2 store close failed", "error", safelog.Text(err.Error(), 240))
		}
	}
	return nil
}
