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
	"sort"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/safelog"
)

// Service 主业务服务
type Service struct {
	store        *Store
	log          *slog.Logger
	httpClient   *http.Client
	bgMu         sync.Mutex
	bgCancel     context.CancelFunc
	bgWg         sync.WaitGroup
	settings     StockV2Settings
	embeddingMu  sync.Mutex
	embeddingRun bool
	// ponytail: process-local single-flight is enough for the single deployed Go service;
	// move to a persisted lease only if multiple StockV2 workers are introduced.
	newsPipelineMu  sync.Mutex
	newsPipelineRun bool
	// ponytail: the deployment has one Go service, so a process-local lease is
	// sufficient to prevent the two full-market entry points from duplicating
	// the same network work. A persisted lease is only needed for multi-process.
	bulkMaintenanceMu  sync.Mutex
	bulkMaintenanceRun bool
	dailyBarJobMu      sync.Mutex
	quotePruneMu       sync.Mutex
	lastQuotePrune     time.Time
	assetBackoffMu     sync.Mutex
	assetBackoff       map[string]time.Time
	stockProfileMu     sync.Map
	resourceGateReader func() ResourceGateStatus

	universeSource     *UniverseDataSource
	thsDailyBars       *THSDailyBarsSource
	dailyBarsSource    *DailyBarsSource
	baiduDailyBars     *BaiduDailyBarsSource
	announcementSource *AnnouncementSource
	newsAdapters       map[string]NewsSourceAdapter
	newsLinker         NewsEventLinker

	// Agent 执行相关
	agentTaskPool     *agentTaskPool
	agentExecutor     AgentExecutor
	agentCodexCommand func(ctx context.Context, args ...string) ([]byte, error)
	agentMCPMu        sync.RWMutex
	agentMCPServer    *http.Server
	agentMCPURL       string
}

func (s *Service) lockStockProfile(symbol string) func() {
	key := strings.TrimSpace(symbol)
	value, _ := s.stockProfileMu.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// NewService 创建新的股票V2服务
func NewService(store *Store, log *slog.Logger, httpClient *http.Client) *Service {
	pool := newAgentTaskPool(defaultCleanupInterval)
	svc := &Service{
		store:              store,
		log:                log,
		httpClient:         httpClient,
		universeSource:     NewUniverseDataSource(nil, httpClient),
		thsDailyBars:       NewTHSDailyBarsSource(httpClient),
		dailyBarsSource:    NewDailyBarsSource(nil, httpClient),
		baiduDailyBars:     NewBaiduDailyBarsSource(httpClient),
		announcementSource: NewAnnouncementSource(httpClient),
		assetBackoff:       map[string]time.Time{},
		newsAdapters:       map[string]NewsSourceAdapter{},
		agentTaskPool:      pool,
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
	jobIDs, recoverErr := s.store.RecoverInterruptedAssetMaintenanceJobs(ctx, reason, time.Now())
	if recoverErr != nil {
		if s.log != nil {
			s.log.Warn("recover interrupted stock data asset maintenance failed", "error", safelog.Text(recoverErr.Error(), 240))
		}
	} else {
		for _, jobID := range jobIDs {
			job, err := s.store.GetUpdateJob(ctx, jobID)
			if err != nil {
				continue
			}
			if _, err := s.store.finalizeAssetMaintenanceJob(ctx, jobID, job.AssetStats, nil, job.WriteBytesEnd, job.PeakRSSBytes); err != nil && s.log != nil {
				s.log.Warn("finalize interrupted stock data asset maintenance failed", "job_id", jobID, "error", safelog.Text(err.Error(), 240))
			}
		}
	}
	failures := []struct {
		name string
		fn   func(context.Context, string) (int64, error)
	}{
		// Any legacy job without frozen targets, or a job whose recovery finalization
		// failed, is still made terminal here. Frozen retry items remain claimable.
		{name: "stock data asset maintenance fallback", fn: s.store.FailRunningUpdateJobs},
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
	ExecuteStockProfileSummary(ctx context.Context, taskID string, pack StockProfileSummaryContext, modelName string) (*AgentExecutorOutput, error)
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

// ExecuteUniverseUpdate 执行统一数据资产维护。
func (s *Service) ExecuteUniverseUpdate(ctx context.Context, req UniverseUpdateRequest) (StockV2UpdateJob, error) {
	s.bulkMaintenanceMu.Lock()
	defer s.bulkMaintenanceMu.Unlock()
	if s.bulkMaintenanceRun {
		return StockV2UpdateJob{}, ErrUpdateJobAlreadyRunning
	}
	// 检查是否有正在运行的更新任务
	recentJobs, err := s.store.ListUpdateJobs(ctx, 1)
	if err != nil {
		return StockV2UpdateJob{}, wrapError(err, "check recent jobs")
	}

	if len(recentJobs) > 0 && recentJobs[0].Status == "running" {
		return StockV2UpdateJob{}, ErrUpdateJobAlreadyRunning
	}

	gate := s.currentResourceGate()
	maintenanceConcurrency := maintenanceConcurrencyForResourceGate(gate)

	// 创建更新任务
	now := time.Now()
	job := StockV2UpdateJob{
		ID:              generateID(),
		TriggerType:     firstNonEmpty(req.TriggerType, "manual"),
		TriggerSource:   firstNonEmpty(req.TriggerSource, "user"),
		Status:          "running",
		Scope:           assetMaintenanceScope(req),
		SlotStart:       req.ScheduledSlotStart,
		CoverageStatus:  AssetMaintenanceCoveragePending,
		FreshnessStatus: AssetMaintenanceFreshnessPending,
		TotalCount:      0,
		ProcessedCount:  0,
		SuccessCount:    0,
		FailedCount:     0,
		StartAt:         now,
		CreatedAt:       now,
	}
	if maintenanceConcurrency == 0 {
		job.Status = "paused"
		job.CoverageStatus = AssetMaintenanceCoverageIncomplete
		job.FreshnessStatus = AssetMaintenanceFreshnessRetrying
		job.EndAt = now
		job.ErrorMessage = resourceGatePauseMessage(gate)
	}

	if err := s.store.CreateUpdateJob(ctx, job); err != nil {
		return StockV2UpdateJob{}, wrapError(err, "create update job")
	}
	if maintenanceConcurrency == 0 {
		return job, nil
	}
	s.bulkMaintenanceRun = true

	// 启动更新任务（异步，使用独立 context，不随请求结束而取消）
	go s.runUniverseUpdate(context.Background(), job, req)

	return job, nil
}

// runUniverseUpdate 运行统一数据资产维护任务
func (s *Service) runUniverseUpdate(ctx context.Context, job StockV2UpdateJob, req UniverseUpdateRequest) {
	jobID := job.ID
	defer func() {
		s.bulkMaintenanceMu.Lock()
		s.bulkMaintenanceRun = false
		s.bulkMaintenanceMu.Unlock()
	}()
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

	// 获取本轮维护标的。
	symbols, selectErr := s.selectAssetMaintenanceSymbols(ctx, &req)
	if selectErr != nil {
		s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
			ID:           jobID,
			Status:       "failed",
			EndAt:        time.Now(),
			ErrorMessage: selectErr.Error(),
		})
		return
	}
	if job.Scope == AssetMaintenanceScopeFullUniverse && !req.universeVerified && len(symbols) == 0 {
		reason := firstNonEmpty(req.universeError, "public universe discovery was not verified")
		s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
			ID:              jobID,
			Status:          "failed",
			CoverageStatus:  AssetMaintenanceCoverageIncomplete,
			FreshnessStatus: AssetMaintenanceFreshnessFailed,
			EndAt:           time.Now(),
			ErrorMessage:    "universe_unverified: " + reason,
		})
		return
	}
	totalCount := len(symbols)
	job.UniverseVerified = req.universeVerified
	if job.Scope == AssetMaintenanceScopeFullUniverse && !job.UniverseVerified {
		job.ErrorMessage = "universe_unverified: " + firstNonEmpty(req.universeError, "using the last cached generation")
	}
	calendarCheckedAt := time.Now()
	_, expectedLatestDate := assetMaintenanceDailyBarStartEnd(calendarCheckedAt)
	maintenanceCtx, authoritativeLatestDate, calendarErr := s.prepareReferenceTradingCalendar(
		ctx, expectedLatestDate, calendarCheckedAt,
	)
	if calendarErr != nil {
		// Calendar failure blocks the market facet, not independent F10 and
		// announcement maintenance. Keep the target freeze and make the task
		// freshness retryable while the low-cost calendar scheduler recovers.
		job.ExpectedLatestDate = expectedLatestDate
		if observed, _, _, observedErr := s.referenceTradingCalendarState(ctx, expectedLatestDate); observedErr == nil && observed.tradeDate != "" {
			job.ExpectedLatestDate = observed.tradeDate
		}
		calendarMessage := "trading_calendar_unavailable: " + calendarErr.Error()
		if job.ErrorMessage == "" {
			job.ErrorMessage = calendarMessage
		} else {
			job.ErrorMessage += "; " + calendarMessage
		}
		if err := s.store.SetAssetMaintenanceCursor(ctx, dailyBarReferenceCalendarRetryCursor, expectedLatestDate); err != nil && s.log != nil {
			s.log.Warn("persist reference calendar retry failed", "error", safelog.Error(err, 240))
		}
	} else {
		job.ExpectedLatestDate = authoritativeLatestDate
		_ = s.store.SetAssetMaintenanceCursor(ctx, dailyBarReferenceCalendarRetryCursor, "")
	}
	job.MessageCutoffAt = calendarCheckedAt
	snapshot := AssetUniverseSnapshot{
		UniverseHash: assetUniverseHash(symbols),
		TargetCount:  totalCount,
	}
	if job.Scope == AssetMaintenanceScopeFullUniverse {
		if job.UniverseVerified {
			snapshot, selectErr = s.store.EnsureAssetUniverseSnapshot(ctx, symbols, assetUniverseSnapshotSourceLive)
		} else {
			snapshot, selectErr = s.store.EnsureUnverifiedAssetUniverseSnapshot(ctx, symbols, "cached_public_universe")
		}
		if selectErr != nil {
			s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
				ID:              jobID,
				Status:          "failed",
				CoverageStatus:  AssetMaintenanceCoverageIncomplete,
				FreshnessStatus: AssetMaintenanceFreshnessFailed,
				EndAt:           time.Now(),
				ErrorMessage:    selectErr.Error(),
			})
			return
		}
	}
	job.UniverseSnapshotID = snapshot.ID
	job.UniverseHash = snapshot.UniverseHash
	job.TotalCount = totalCount
	if err := s.store.PrepareAssetMaintenanceJob(ctx, job, snapshot, symbols); err != nil {
		s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
			ID:              jobID,
			Status:          "failed",
			CoverageStatus:  AssetMaintenanceCoverageIncomplete,
			FreshnessStatus: AssetMaintenanceFreshnessFailed,
			EndAt:           time.Now(),
			ErrorMessage:    err.Error(),
		})
		return
	}
	if err := s.store.CommitAssetMaintenanceSelectionCursors(
		ctx, req.priorityCursorNext, req.universeCursorNext,
	); err != nil {
		s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
			ID:              jobID,
			Status:          "failed",
			CoverageStatus:  AssetMaintenanceCoverageIncomplete,
			FreshnessStatus: AssetMaintenanceFreshnessFailed,
			EndAt:           time.Now(),
			ErrorMessage:    err.Error(),
		})
		return
	}

	// 分批更新股票
	const batchSize = 500
	totalBatches := (totalCount + batchSize - 1) / batchSize
	var failedItems []UpdateFailure
	successCount := 0
	processedCount := 0
	assetStats := AssetMaintenanceStats{}
	var mu sync.Mutex
	announcementBatch := AnnouncementMarketsSyncResult{NewBySymbol: map[string][]StockV2Announcement{}}
	var announcementBatchErr error
	announcementPrefetched := totalCount >= 30
	if announcementPrefetched {
		announcementBatch, announcementBatchErr = s.SyncAnnouncementMarkets(ctx, AnnouncementMarketsSyncRequest{})
		if announcementBatchErr != nil && s.log != nil {
			s.log.Warn("stock announcement market sync failed",
				"job_id", jobID,
				"symbol_count", totalCount,
				"error", safelog.Text(announcementBatchErr.Error(), 300),
			)
		}
	}
	flushProgress := func(includeFailedItems bool) {
		mu.Lock()
		progress.ProcessedCount = processedCount
		progress.SuccessCount = successCount
		progress.ErrorCount = len(failedItems)
		if len(failedItems) > 0 {
			progress.LastError = failedItems[len(failedItems)-1].Reason
		}
		snapshotProgress := progress
		snapshotFailed := failedItems
		snapshotAssetStats := assetStats
		mu.Unlock()

		if err := s.store.UpdateUpdateProgress(ctx, snapshotProgress); err != nil {
			if s.log != nil {
				s.log.Warn("update maintenance progress failed", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "processed_count", processedCount, "failed_count", len(failedItems), "current_symbol", snapshotProgress.CurrentSymbol, "error", safelog.Text(err.Error(), 240))
			}
		}
		update := StockV2UpdateJob{
			ID:             jobID,
			TotalCount:     totalCount,
			CheckedCount:   snapshotProgress.ProcessedCount,
			ProcessedCount: snapshotProgress.ProcessedCount,
			SuccessCount:   snapshotProgress.SuccessCount,
			FailedCount:    snapshotProgress.ErrorCount,
			AssetStats:     snapshotAssetStats,
		}
		if includeFailedItems {
			update.FailedItems = snapshotFailed
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
		gate := s.currentResourceGate()
		maintenanceConcurrency := maintenanceConcurrencyForResourceGate(gate)
		if maintenanceConcurrency == 0 {
			flushProgress(true)
			pauseErr := s.store.PauseAssetMaintenanceJob(
				ctx, jobID, resourceGatePauseMessage(gate), time.Now(),
			)
			if pauseErr != nil {
				if s.log != nil {
					s.log.Warn("pause stock data asset maintenance failed", "job_id", jobID, "error", safelog.Text(pauseErr.Error(), 240))
				}
				// Preserve the frozen tail even if the richer paused-state transaction
				// failed. Recovery makes pending work claimable and finalization keeps
				// coverage incomplete instead of silently advancing the cursor tail.
				if _, recoverErr := s.store.RecoverInterruptedAssetMaintenanceJobs(
					ctx, "resource pause persistence failed: "+pauseErr.Error(), time.Now(),
				); recoverErr != nil {
					if s.log != nil {
						s.log.Error("recover stock maintenance tail after pause failure", "job_id", jobID, "error", safelog.Text(recoverErr.Error(), 240))
					}
				} else if _, finalizeErr := s.store.finalizeAssetMaintenanceJob(ctx, jobID, assetStats, failedItems, 0, 0); finalizeErr != nil && s.log != nil {
					s.log.Error("finalize recovered stock maintenance pause", "job_id", jobID, "error", safelog.Text(finalizeErr.Error(), 240))
				}
			}
			return
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

		workSymbols := batchSymbols
		recentAnnouncements := map[string][]StockV2Announcement{}
		var recentAnnouncementsErr error
		if announcementPrefetched {
			recentAnnouncements, recentAnnouncementsErr = s.store.ListRecentAnnouncementsBySymbols(ctx, workSymbols, 100)
			if recentAnnouncementsErr != nil && s.log != nil {
				s.log.Warn("load recent announcement context failed", "job_id", jobID, "batch", batch+1, "error", safelog.Text(recentAnnouncementsErr.Error(), 240))
			}
		}
		announcementContextErr := errors.Join(announcementBatchErr, recentAnnouncementsErr)

		// Existing instruments are already the durable universe cache. Only newly
		// discovered symbols need the Tencent master-data request; the closing quote
		// batch below refreshes current names together with daily fields.
		instruments, err := s.store.GetInstrumentsBySymbols(ctx, workSymbols)
		if err == nil {
			loaded := make(map[string]struct{}, len(instruments))
			for _, inst := range instruments {
				loaded[inst.Symbol] = struct{}{}
			}
			missing := make([]string, 0, len(workSymbols)-len(instruments))
			for _, symbol := range workSymbols {
				if _, ok := loaded[symbol]; !ok {
					missing = append(missing, symbol)
				}
			}
			if len(missing) > 0 {
				var fetched []StockV2Instrument
				fetched, err = s.universeSource.FetchStockUniverse(ctx, missing)
				instruments = append(instruments, fetched...)
			}
		}
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
			if markErr := s.store.MarkAssetMaintenanceItemsRetryWait(ctx, jobID, workSymbols, err.Error()); markErr != nil && s.log != nil {
				s.log.Warn("persist maintenance batch retry state failed", "job_id", jobID, "batch", batch+1, "error", safelog.Text(markErr.Error(), 240))
			}
			progress.CurrentBatchProgress = len(batchSymbols)
			progress.CurrentSymbol = workSymbols[len(workSymbols)-1]
			flushProgress(true)
			continue
		}

		batchCtx := maintenanceCtx
		quoteBatchError := ""
		_, closingDate := dailyBarRangeStartEnd(DailyBarRange1Y, time.Now())
		if nextCtx, _, prefillErr := s.prefillClosingDailyBarsBatch(maintenanceCtx, instruments, closingDate); prefillErr != nil {
			quoteBatchError = safelog.Text(prefillErr.Error(), 300)
			if s.log != nil {
				s.log.Warn("stock daily bar batch quote prefill failed", "job_id", jobID, "batch", batch+1, "symbol_count", len(instruments), "error", safelog.Text(prefillErr.Error(), 240))
			}
		} else {
			maintenanceCtx = nextCtx
			batchCtx = nextCtx
		}
		// 构造返回结果 map，对比找失败的。批量 quote 可能刷新名称，
		// 因此必须在 prefill 之后复制 instrument。
		resultMap := make(map[string]StockV2Instrument, len(instruments))
		for _, inst := range instruments {
			resultMap[inst.Symbol] = inst
		}

		// 保存股票数据，并把日 K、基础画像、公告和 AI 决策纳入同一个 per-symbol 管线。
		// ponytail: resource pressure reduces this to one worker; normal hosts use
		// two for network overlap without competing with the single DuckDB writer.
		const progressFlushInterval = 50 // 每 50 只 symbol flush 一次进度，减少 DuckDB 写入

		batchProcessed := progress.CurrentBatchProgress
		sem := make(chan struct{}, maintenanceConcurrency)
		var wg sync.WaitGroup
		ctxCancelled := false

		for _, sym := range workSymbols {
			if ctx.Err() != nil {
				ctxCancelled = true
				break
			}
			wg.Add(1)
			select {
			case sem <- struct{}{}: // acquire worker slot
			case <-ctx.Done():
				wg.Done()
				ctxCancelled = true
				break
			}
			if ctxCancelled {
				break
			}

			go func(symbol string) {
				defer wg.Done()
				defer func() { <-sem }() // release worker slot

				failedBefore := 0
				mu.Lock()
				progress.CurrentSymbol = symbol
				failedBefore = len(failedItems)
				mu.Unlock()

				inst, ok := resultMap[symbol]
				if !ok {
					mu.Lock()
					failedItems = append(failedItems, UpdateFailure{
						Symbol: symbol,
						Reason: "no data from source",
					})
					processedCount++
					batchProcessed++
					progress.CurrentBatchProgress = batchProcessed
					needFlush := processedCount%progressFlushInterval == 0
					mu.Unlock()
					if markErr := s.store.MarkAssetMaintenanceItemsRetryWait(ctx, jobID, []string{symbol}, "instrument missing from batch source"); markErr != nil && s.log != nil {
						s.log.Warn("persist missing maintenance instrument retry failed", "job_id", jobID, "symbol", symbol, "error", safelog.Text(markErr.Error(), 240))
					}
					if needFlush {
						flushProgress(true)
					}
					return
				}

				quoteError := quoteBatchError
				if quoteError == "" {
					quoteError = dailyBarQuoteBatchFailure(batchCtx, symbol)
				}
				var quotePrefetchErr error
				if quoteError != "" {
					quotePrefetchErr = errors.New(quoteError)
				}
				result, maintainErr := s.maintainAssetForInstrument(batchCtx, inst, assetMaintenanceOptions{
					JobID:                     jobID,
					ItemID:                    assetMaintenanceItemID(jobID, symbol),
					ExpectedLatestDate:        job.ExpectedLatestDate,
					TriggerSource:             job.TriggerSource,
					RequestedBy:               "system",
					ForceAI:                   req.ForceAI,
					AnnouncementsPrefetched:   announcementPrefetched,
					PrefetchedAnnouncements:   announcementBatch.NewBySymbol[symbol],
					RecentAnnouncements:       recentAnnouncements[symbol],
					AnnouncementPrefetchError: announcementContextErr,
					AnnouncementCheckedAt:     announcementBatch.FinishedAt,
					QuotePrefetchError:        quotePrefetchErr,
				})

				mu.Lock()
				assetStats = mergeAssetMaintenanceStats(assetStats, result.Item)
				if maintainErr != nil {
					failedItems = append(failedItems, UpdateFailure{
						Symbol: symbol,
						Reason: safelog.Text(maintainErr.Error(), 240),
					})
				} else {
					successCount++
				}
				processedCount++
				batchProcessed++
				progress.CurrentBatchProgress = batchProcessed
				failedChanged := len(failedItems) != failedBefore
				needFlush := processedCount%progressFlushInterval == 0 || failedChanged
				mu.Unlock()
				if needFlush {
					flushProgress(failedChanged)
				}

				if assetMaintenanceUsedRemoteProfile(result.Item.SourceStatuses) {
					if err := sleepJitter(ctx, 80*time.Millisecond, 60*time.Millisecond); err != nil {
						return // context cancelled
					}
				}
			}(sym)
		}
		wg.Wait()

		if ctxCancelled {
			s.store.UpdateUpdateJob(ctx, StockV2UpdateJob{
				ID:          jobID,
				Status:      "cancelled",
				EndAt:       time.Now(),
				FailedItems: failedItems,
			})
			return
		}

		// 批次结束时 flush 剩余进度
		flushProgress(true)

		// 批间延迟（避免风控）
		if batch < totalBatches-1 {
			if err := sleepJitter(ctx, 100*time.Millisecond, 100*time.Millisecond); err != nil {
				return // context cancelled
			}
		}
	}

	// 完成更新。coverage 与 freshness 独立结算，第三方失败不会伪装成绿色完成。
	if _, err := s.store.finalizeAssetMaintenanceJob(ctx, jobID, assetStats, failedItems, 0, 0); err != nil && s.log != nil {
		s.log.Error("finalize stock data asset maintenance failed", "job_id", jobID, "error", safelog.Text(err.Error(), 300))
	}
	if len(failedItems) > 0 && s.log != nil {
		s.log.Warn("stock data asset maintenance completed with item failures", "job_id", jobID, "trigger_type", job.TriggerType, "trigger_source", job.TriggerSource, "total_count", totalCount, "processed_count", processedCount, "success_count", successCount, "failed_count", len(failedItems), "failure_sample", stockV2FailureSample(failedItems, 5))
	}
	if err := s.store.PruneAssetMaintenanceHistory(ctx, time.Now()); err != nil && s.log != nil {
		s.log.Warn("prune asset maintenance history failed", "error", safelog.Text(err.Error(), 240))
	}
}

func assetMaintenanceUsedRemoteProfile(statuses []AssetMaintenanceSourceStatus) bool {
	for _, status := range statuses {
		switch status.Source {
		case "eastmoney_company_survey", "eastmoney_business_analysis", "eastmoney_core_conception",
			"eastmoney_fund_basic", "eastmoney_fund_holdings":
			return true
		}
	}
	return false
}

func (s *Service) fetchDailyBarsForInstrument(ctx context.Context, inst StockV2Instrument) (bool, []StockV2DailyBar, []dailyBarNoTradeCoverage, error) {
	start, end := assetMaintenanceDailyBarStartEnd(time.Now())
	end = dailyBarBatchTargetDate(ctx, end)
	start, end = clampDailyBarRangeToInstrument(inst, start, end)
	if start == "" {
		return false, nil, nil, nil
	}
	startTime, err := time.Parse("2006-01-02", start)
	if err != nil {
		return false, nil, nil, err
	}
	endTime, err := time.Parse("2006-01-02", end)
	if err != nil {
		return false, nil, nil, err
	}
	tradingDates, err := s.observedTradingDates(ctx, start, end)
	if err != nil {
		return false, nil, nil, err
	}
	checkedRanges, err := s.store.ListDailyBarGapChecks(ctx, inst.Symbol, DailyBarAdjustedNone, start, end)
	if err != nil {
		return false, nil, nil, err
	}
	storedBars, err := s.store.GetDailyBars(ctx, inst.Symbol, DailyBarAdjustedNone, start, end, 0)
	if err != nil {
		return false, nil, nil, err
	}
	coverage := planDailyBarCoverageWithCalendar(
		storedBars, tradingDates, checkedRanges, inst.InstrumentType, start, end,
	)
	ranges := coverage.historicalKFetchRanges(tradingDates)
	if len(tradingDates) == 0 {
		dates := make([]string, 0, len(storedBars))
		for _, bar := range storedBars {
			if dailyBarCoreFacetsComplete(bar) {
				dates = append(dates, bar.TradeDate)
			}
		}
		ranges = planDailyBarMissingRanges(dates, startTime, endTime)
		ranges = subtractCheckedDailyBarRanges(ranges, checkedRanges)
	}
	ranges = excludeBatchClosingQuoteRetry(ctx, inst.Symbol, end, ranges)
	if len(ranges) == 0 {
		return false, nil, nil, nil
	}

	requestedTradingDates := dailyBarRequestedTradingDates(tradingDates, ranges)
	bars, observations, fetchErr := s.fetchDailyBarsForMissingRanges(ctx, inst, ranges, requestedTradingDates)
	if fetchErr != nil {
		return true, nil, nil, fetchErr
	}
	checkedAbsences := verifyDailyBarNoTradeCoverage(tradingDates, observations, ranges, time.Now())

	// 数据源成功但目标区间没有 bar，通常是停牌或尚未产生的新交易日。
	if len(bars) == 0 {
		return false, nil, checkedAbsences, nil
	}

	// ponytail: the full-universe path stores price/core facets now and leaves
	// historical flow to its bounded enrichment queue. Per-symbol Eastmoney flow
	// calls here would multiply a normal daily scan into thousands of requests.
	return true, bars, checkedAbsences, nil
}

func (s *Service) fetchDailyBarsForMissingRanges(
	ctx context.Context,
	inst StockV2Instrument,
	ranges []dailyBarMissingRange,
	expectedDates []string,
) ([]StockV2DailyBar, []dailyBarSourceRangeObservation, error) {
	if len(ranges) == 0 {
		return nil, nil, nil
	}

	// All sources are requested once for the smallest envelope covering every gap.
	// The returned rows are then filtered locally, avoiding one request per gap.
	startDate := ranges[0].Start
	endDate := ranges[len(ranges)-1].End
	var sourceErrs []error
	observations := make([]dailyBarSourceRangeObservation, 0, 3)
	bestByDate := make(map[string]StockV2DailyBar)
	observedRange := dailyBarMissingRange{Start: startDate, End: endDate}
	recordSuccess := func(source string, bars []StockV2DailyBar) {
		returnedDates := make([]string, 0, len(bars))
		for _, bar := range bars {
			if bar.TradeDate != "" {
				returnedDates = append(returnedDates, bar.TradeDate)
			}
		}
		observations = append(observations, dailyBarSourceRangeObservation{
			Source: source, Range: observedRange, Succeeded: true, ReturnedDates: returnedDates,
		})
	}
	mergeFetched := func(bars []StockV2DailyBar) {
		for _, bar := range filterBarsByRanges(bars, ranges) {
			if strings.TrimSpace(bar.Symbol) == "" {
				bar.Symbol = inst.Symbol
			}
			current, exists := bestByDate[bar.TradeDate]
			if !exists || dailyBarFetchedRowScore(bar) > dailyBarFetchedRowScore(current) {
				bestByDate[bar.TradeDate] = bar
			}
		}
	}
	result := func() []StockV2DailyBar {
		bars := make([]StockV2DailyBar, 0, len(bestByDate))
		for _, bar := range bestByDate {
			bars = append(bars, bar)
		}
		sort.Slice(bars, func(i, j int) bool { return bars[i].TradeDate < bars[j].TradeDate })
		return bars
	}
	complete := func() bool {
		if len(expectedDates) > 0 {
			for _, tradeDate := range expectedDates {
				bar, ok := bestByDate[tradeDate]
				if !ok || !dailyBarCoreFacetsComplete(bar) {
					return false
				}
			}
			return true
		}
		// ponytail: without an authoritative calendar, one complete row in each
		// requested gap is only an optimization signal. Strict readiness remains
		// blocked by the independent calendar gate until that calendar recovers.
		for _, requested := range ranges {
			covered := false
			for tradeDate, bar := range bestByDate {
				if tradeDate >= requested.Start && tradeDate <= requested.End && dailyBarCoreFacetsComplete(bar) {
					covered = true
					break
				}
			}
			if !covered {
				return false
			}
		}
		return true
	}

	if s.thsDailyBars != nil {
		fetched, err := s.thsDailyBars.FetchDailyBars(ctx, inst.Symbol, inst.Market, startDate, endDate)
		if err == nil {
			recordSuccess("10jqka_kline", fetched)
			mergeFetched(fetched)
			if complete() {
				return result(), observations, nil
			}
		} else {
			sourceErrs = append(sourceErrs, err)
		}
	}

	if s.dailyBarsSource != nil {
		fetched, err := s.dailyBarsSource.FetchDailyBars(ctx, inst.Symbol, inst.Market, startDate, endDate, DailyBarAdjustedNone, 1800)
		if err == nil {
			for i := range fetched {
				fetched[i].Symbol = inst.Symbol
			}
			recordSuccess("tencent_fqkline", fetched)
			mergeFetched(fetched)
			if complete() {
				// Tencent satisfied the requested core facet gap; Baidu remains the
				// final fallback and is not contacted on this successful path.
				return result(), observations, nil
			}
		} else {
			sourceErrs = append(sourceErrs, err)
		}
	}

	if s.baiduDailyBars != nil {
		fetched, err := s.baiduDailyBars.FetchDailyBars(ctx, inst.Symbol, inst.Market, inst.InstrumentType)
		if err == nil {
			for i := range fetched {
				fetched[i].Symbol = inst.Symbol
			}
			recordSuccess("baidu_kline", fetched)
			mergeFetched(fetched)
		} else {
			sourceErrs = append(sourceErrs, err)
		}
	}
	if len(bestByDate) > 0 {
		return result(), observations, nil
	}
	if len(observations) >= 2 {
		return nil, observations, nil
	}
	if joined := errors.Join(sourceErrs...); joined != nil {
		return nil, observations, fmt.Errorf("daily bar absence was not confirmed by two sources: %w", joined)
	}
	return nil, observations, errors.New("daily bar absence was not confirmed by two sources")
}

func dailyBarFetchedRowScore(bar StockV2DailyBar) int {
	score := 0
	if dailyBarOHLCVComplete(bar) {
		score += 8
	}
	if dailyBarAmountPresent(bar) {
		score += 2
	}
	if dailyBarTurnoverPresent(bar) {
		score += 2
	}
	if dailyBarNetInflowPresent(bar) {
		score++
	}
	if dailyBarMainNetInflowPresent(bar) {
		score++
	}
	return score
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
	prevNewsBG := s.hasEnabledNewsSources(ctx)
	if req.AutoUpdateEnabled != nil {
		settings.AutoUpdateEnabled = *req.AutoUpdateEnabled
	}

	// 保存配置
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		return StockV2Settings{}, wrapError(err, "save settings")
	}
	// 更新本地配置
	s.settings = settings

	// 数据资产自动维护、消息源或 embedding 自动维护任一开启都需要后台调度。
	// 任一开关从关→开，或数据资产周期变化时重启后台，以拾取新 ticker。
	newsBG := s.hasEnabledNewsSources(ctx)
	embeddingBG := s.hasEmbeddingAutoMaintenanceEnabled(ctx)
	needBG := settings.AutoUpdateEnabled || newsBG || embeddingBG
	prevNeedBG := prevAuto || prevNewsBG || embeddingBG
	if needBG {
		restartBG := !prevNeedBG
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
	return s.store.GetSettings(ctx)
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
		s.runStockProfileAIQueueBackground(bgCtx)
	}()

	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runDailyBarFlowRepairScheduler(bgCtx)
	}()

	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runAssetMaintenanceRetryScheduler(bgCtx)
	}()

	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runMajorAnnouncementBodyScheduler(bgCtx)
	}()

	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runReferenceTradingCalendarScheduler(bgCtx)
	}()

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

	// RawNews 是短期 staging 数据；只保留 4 小时，长期分析使用 NewsEvent/LinkCandidate。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runRawNewsRetentionScheduler(bgCtx)
	}()

	// Legacy base-profile scheduler is intentionally not started; unified asset
	// maintenance now owns base profile refresh and AI summary decisions.

	// embedding asset 维护复用现有资产表和模型配置，不引入独立任务队列。
	s.bgWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		s.runEmbeddingMaintenanceScheduler(bgCtx)
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
	due, slotStart := shouldRunDailyUniverseUpdate(s.settings.LastScheduledUpdate, now)
	if !due {
		return
	}
	latest, latestOK := s.currentUniverseUpdateJob(ctx, slotStart)
	gate := s.currentResourceGate()
	if latestOK && latest.Status == "paused" && latest.SlotStart.Equal(slotStart) &&
		(latest.TotalCount > 0 || gate.State == ResourceGatePaused) {
		return
	}
	if latestOK && referenceTradingCalendarProviderBackoffActive(latest, slotStart, now) {
		return
	}
	if latestOK && !latest.UniverseVerified {
		lastDiscoveryAttempt, err := s.store.GetAssetMaintenanceCursorUpdatedAt(ctx, assetUniverseDiscoveryCursorScope)
		if err == nil && shouldWaitForUniverseDiscoveryRetry(latest, slotStart, lastDiscoveryAttempt, now) {
			return
		}
	}
	decision, _ := decideScheduledUniverseUpdate(s.settings.LastScheduledUpdate, latest, latestOK, now)
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
	if _, err := s.ExecuteUniverseUpdate(ctx, UniverseUpdateRequest{
		TriggerType: "scheduled", TriggerSource: "auto-updater", ScheduledSlotStart: slotStart,
	}); err != nil {
		if s.log != nil {
			s.log.Error("scheduled update failed", "trigger_type", "scheduled", "trigger_source", "auto-updater", "schedule", "daily_23:00", "slot_start", slotStart.Format(time.RFC3339Nano), "last_scheduled_update", s.settings.LastScheduledUpdate.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 300))
		}
		return
	}
}

func decideScheduledUniverseUpdate(lastScheduled time.Time, latest StockV2UpdateJob, latestOK bool, now time.Time) (scheduledUniverseUpdateDecision, time.Time) {
	due, slotStart := shouldRunDailyUniverseUpdate(lastScheduled, now)
	retryIncompleteSlot := latestOK && latest.Status == "failed" &&
		latest.Scope == AssetMaintenanceScopeFullUniverse &&
		latest.CoverageStatus != AssetMaintenanceCoverageCovered && latest.SlotStart.Equal(slotStart)
	if !due && !retryIncompleteSlot {
		return scheduledUniverseUpdateSkip, slotStart
	}
	if latestOK && latest.Status == "running" {
		return scheduledUniverseUpdateWait, slotStart
	}
	if latestOK && referenceTradingCalendarProviderBackoffActive(latest, slotStart, now) {
		return scheduledUniverseUpdateWait, slotStart
	}
	if latestOK && latest.Scope == AssetMaintenanceScopeFullUniverse &&
		latest.CoverageStatus == AssetMaintenanceCoverageCovered && latest.SlotStart.Equal(slotStart) {
		return scheduledUniverseUpdateConfirm, slotStart
	}
	return scheduledUniverseUpdateStart, slotStart
}

func shouldWaitForUniverseDiscoveryRetry(latest StockV2UpdateJob, slotStart, lastAttempt, now time.Time) bool {
	return latest.Scope == AssetMaintenanceScopeFullUniverse && !latest.UniverseVerified &&
		latest.SlotStart.Equal(slotStart) && !lastAttempt.IsZero() &&
		now.Before(lastAttempt.Add(assetUniverseDiscoveryFallbackRetryInterval))
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

func (s *Service) currentUniverseUpdateJob(ctx context.Context, slotStart time.Time) (StockV2UpdateJob, bool) {
	slot, exists, err := s.store.GetAssetMaintenanceSlot(ctx, slotStart)
	if err != nil {
		if s.log != nil {
			s.log.Warn("check stock data maintenance slot failed", "slot_start", slotStart.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 240))
		}
		return StockV2UpdateJob{}, false
	}
	if exists && slot.JobID != "" {
		job, jobErr := s.store.GetUpdateJob(ctx, slot.JobID)
		if jobErr == nil {
			return job, true
		}
		if !errors.Is(jobErr, ErrUpdateJobNotFound) && s.log != nil {
			s.log.Warn("check stock data maintenance slot job failed", "job_id", slot.JobID, "error", safelog.Text(jobErr.Error(), 240))
		}
	}
	slotJob, err := s.store.GetLatestUniverseUpdateJobForSlot(ctx, slotStart)
	if err == nil {
		return slotJob, true
	}
	if !errors.Is(err, ErrUpdateJobNotFound) && s.log != nil {
		s.log.Warn("check stock data maintenance slot update job failed", "slot_start", slotStart.Format(time.RFC3339Nano), "error", safelog.Text(err.Error(), 240))
	}
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
	if latest.Status == "running" {
		return latest, true
	}
	return StockV2UpdateJob{}, false
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
