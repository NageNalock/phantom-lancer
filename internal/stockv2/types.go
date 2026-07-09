package stockv2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

const (
	InstrumentTypeStock        = "stock"
	InstrumentTypeExchangeFund = "exchange_fund"
)

// StockV2Instrument 标的主数据。第一阶段只区分股票与场内基金,不展开基金净值/申赎等专属字段。
type StockV2Instrument struct {
	ID             string    `json:"id"`
	Symbol         string    `json:"symbol"`
	Market         string    `json:"market"`
	InstrumentType string    `json:"instrumentType"`
	Name           string    `json:"name"`
	Industry       string    `json:"industry"`
	Sector         string    `json:"sector"`
	Concepts       []string  `json:"concepts"` // 概念信息数组
	ListDate       string    `json:"listDate"`
	DelistDate     string    `json:"delistDate"`
	Status         string    `json:"status"` // active, delisted, suspended
	LastUpdate     time.Time `json:"lastUpdate"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// StockV2Portfolio 投资组合/仓位
type StockV2Portfolio struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description,omitempty"`
	Cash                 float64   `json:"cash"`
	RiskLevel            string    `json:"riskLevel"`            // low, medium, high
	MaxSinglePositionPct float64   `json:"maxSinglePositionPct"` // 单票最大持仓比例
	MaxDrawdownPct       float64   `json:"maxDrawdownPct"`       // 最大回撤比例
	AllowBuy             bool      `json:"allowBuy"`
	AllowAdd             bool      `json:"allowAdd"`
	AllowReduce          bool      `json:"allowReduce"`
	AllowSell            bool      `json:"allowSell"`
	Notes                string    `json:"notes,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// StockV2PortfolioPatch 投资组合更新补丁
type StockV2PortfolioPatch struct {
	Name                 *string  `json:"name,omitempty"`
	Description          *string  `json:"description,omitempty"`
	Cash                 *float64 `json:"cash,omitempty"`
	RiskLevel            *string  `json:"riskLevel,omitempty"`
	MaxSinglePositionPct *float64 `json:"maxSinglePositionPct,omitempty"`
	MaxDrawdownPct       *float64 `json:"maxDrawdownPct,omitempty"`
	AllowBuy             *bool    `json:"allowBuy,omitempty"`
	AllowAdd             *bool    `json:"allowAdd,omitempty"`
	AllowReduce          *bool    `json:"allowReduce,omitempty"`
	AllowSell            *bool    `json:"allowSell,omitempty"`
	Notes                *string  `json:"notes,omitempty"`
}

// StockV2Holding 持仓记录
type StockV2Holding struct {
	ID                string    `json:"id"`
	PortfolioID       string    `json:"portfolioId"`
	Symbol            string    `json:"symbol"`
	Market            string    `json:"market,omitempty"`
	Name              string    `json:"name,omitempty"`
	Quantity          float64   `json:"quantity"`
	AvailableQuantity float64   `json:"availableQuantity"`
	CostPrice         float64   `json:"costPrice"`
	LastPrice         float64   `json:"lastPrice"`
	LastPriceAt       time.Time `json:"lastPriceAt,omitempty"`
	TradableStatus    string    `json:"tradableStatus"`
	MarketValue       float64   `json:"marketValue"`
	PnL               float64   `json:"pnl"`
	PositionPct       float64   `json:"positionPct"`
	AcquiredAt        time.Time `json:"acquiredAt"` // 建仓时间(初始导入或首次买入时间)
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// StockV2HoldingPatch 持仓更新补丁
type StockV2HoldingPatch struct {
	Quantity          *float64 `json:"quantity,omitempty"`
	AvailableQuantity *float64 `json:"availableQuantity,omitempty"`
	CostPrice         *float64 `json:"costPrice,omitempty"`
	LastPrice         *float64 `json:"lastPrice,omitempty"`
	AcquiredAt        *string  `json:"acquiredAt,omitempty"` // 建仓时间,支持 RFC3339 / datetime-local 格式
}

// StockV2Transaction 交易流水(买入/卖出)。每笔交易驱动持仓与现金变化,
// 是组合资产变化的唯一来源;持仓和现金不再凭空手工调整。
type StockV2Transaction struct {
	ID          string    `json:"id"`
	PortfolioID string    `json:"portfolioId"`
	Symbol      string    `json:"symbol"`
	Market      string    `json:"market,omitempty"`
	Name        string    `json:"name,omitempty"`
	Side        string    `json:"side"` // buy | sell
	Quantity    float64   `json:"quantity"`
	Price       float64   `json:"price"`      // 成交单价
	Amount      float64   `json:"amount"`     // = Quantity * Price(冗余便于展示)
	ExecutedAt  time.Time `json:"executedAt"` // 成交时间(可填过去某次实际买入)
	Note        string    `json:"note,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// TransactionResult 记录交易后的返回:更新后的组合、持仓与流水记录。
type TransactionResult struct {
	Transaction    StockV2Transaction `json:"transaction"`
	Portfolio      StockV2Portfolio   `json:"portfolio"`
	Holding        StockV2Holding     `json:"holding"`        // 卖出清仓时为零值
	HoldingCleared bool               `json:"holdingCleared"` // 卖出清仓导致持仓删除
}

// StockV2UpdateJob 更新任务记录
type StockV2UpdateJob struct {
	ID             string                `json:"id"`
	TriggerType    string                `json:"triggerType"`   // manual, scheduled
	TriggerSource  string                `json:"triggerSource"` // user, system
	Status         string                `json:"status"`        // running, completed, failed, cancelled
	TotalCount     int                   `json:"totalCount"`
	ProcessedCount int                   `json:"processedCount"`
	SuccessCount   int                   `json:"successCount"`
	FailedCount    int                   `json:"failedCount"`
	FailedItems    []UpdateFailure       `json:"failedItems,omitempty"` // 失败详情
	AssetStats     AssetMaintenanceStats `json:"assetStats,omitempty"`
	StartAt        time.Time             `json:"startAt"`
	EndAt          time.Time             `json:"endAt"`
	ErrorMessage   string                `json:"errorMessage,omitempty"`
	CreatedAt      time.Time             `json:"createdAt"`
}

// UpdateFailure 单只股票更新失败记录
type UpdateFailure struct {
	Symbol string `json:"symbol"`
	Reason string `json:"reason"`
}

// StockV2UpdateProgress 更新进度
type StockV2UpdateProgress struct {
	UpdateJobID          string    `json:"updateJobId"`
	ProcessedCount       int       `json:"processedCount"`
	SuccessCount         int       `json:"successCount"`
	CurrentBatch         int       `json:"currentBatch"`
	CurrentBatchProgress int       `json:"currentBatchProgress"`
	CurrentSymbol        string    `json:"currentSymbol"`
	ErrorCount           int       `json:"errorCount"`
	LastError            string    `json:"lastError,omitempty"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// StockV2Settings V2 配置
type StockV2Settings struct {
	ID                                 string    `json:"id"`
	AutoUpdateEnabled                  bool      `json:"autoUpdateEnabled"`
	UpdateIntervalSec                  int       `json:"updateIntervalSec"`
	ProxyEnabled                       bool      `json:"proxyEnabled"`
	ProxyType                          string    `json:"proxyType"`
	ProxyHost                          string    `json:"proxyHost"`
	ProxyPort                          int       `json:"proxyPort"`
	LastScheduledUpdate                time.Time `json:"lastScheduledUpdate"`
	DailyBarsAutoEnabled               bool      `json:"dailyBarsAutoEnabled"` // legacy: 旧版独立日 K 调度开关，V2 产品路径不再使用
	DailyBarsLastRun                   time.Time `json:"dailyBarsLastRun"`     // 最近一次统一维护或手动日 K 批任务完成时间
	FinancialJuiceEnabled              bool      `json:"-"`
	FinancialJuiceEndpoint             string    `json:"-"`
	FinancialJuiceCookie               string    `json:"-"`
	FinancialJuiceCookieSet            bool      `json:"-"`
	BaseProfileAutoMaintainEnabled     bool      `json:"baseProfileAutoMaintainEnabled"`
	BaseProfileMaintainIntervalSeconds int       `json:"baseProfileMaintainIntervalSeconds"`
	BaseProfileDeepUpdateBatchSize     int       `json:"baseProfileDeepUpdateBatchSize"`
	BaseProfileDeepUpdateAIBudget      int       `json:"baseProfileDeepUpdateAiBudget"`
	BaseProfileDeepUpdateRateLimitMs   int       `json:"baseProfileDeepUpdateRateLimitMs"`
	BaseProfileLastMaintainAt          time.Time `json:"baseProfileLastMaintainAt,omitempty"`
	BaseProfileNextMaintainAt          time.Time `json:"baseProfileNextMaintainAt,omitempty"`
	BaseProfileLastMaintainResult      string    `json:"baseProfileLastMaintainResult,omitempty"`
	CreatedAt                          time.Time `json:"createdAt"`
	UpdatedAt                          time.Time `json:"updatedAt"`
}

// StockV2SettingsPatch 配置更新补丁
type StockV2SettingsPatch struct {
	AutoUpdateEnabled    *bool   `json:"autoUpdateEnabled,omitempty"`
	UpdateIntervalSec    *int    `json:"updateIntervalSec,omitempty"`
	ProxyEnabled         *bool   `json:"proxyEnabled,omitempty"`
	ProxyType            *string `json:"proxyType,omitempty"`
	ProxyHost            *string `json:"proxyHost,omitempty"`
	ProxyPort            *int    `json:"proxyPort,omitempty"`
	DailyBarsAutoEnabled *bool   `json:"dailyBarsAutoEnabled,omitempty"` // legacy: 新流程使用 autoUpdateEnabled
}

// Repository 接口定义
type Repository interface {
	CreatePortfolio(ctx context.Context, portfolio StockV2Portfolio) error
	GetPortfolio(ctx context.Context, id string) (StockV2Portfolio, error)
	UpdatePortfolio(ctx context.Context, portfolio StockV2Portfolio) error
	DeletePortfolio(ctx context.Context, id string) error
	ListPortfolios(ctx context.Context) ([]StockV2Portfolio, error)

	CreateHolding(ctx context.Context, holding StockV2Holding) error
	GetHolding(ctx context.Context, id string) (StockV2Holding, error)
	UpdateHolding(ctx context.Context, holding StockV2Holding) error
	DeleteHolding(ctx context.Context, id string) error
	ListHoldings(ctx context.Context, portfolioID string) ([]StockV2Holding, error)

	CreateInstrument(ctx context.Context, instrument StockV2Instrument) error
	GetInstrument(ctx context.Context, symbol string) (StockV2Instrument, error)
	GetInstruments(ctx context.Context, limit int, offset int) ([]StockV2Instrument, error)
	SearchInstruments(ctx context.Context, keyword string, limit int) ([]StockV2Instrument, error)
	UpdateInstrument(ctx context.Context, instrument StockV2Instrument) error
	GetInstrumentsByMarket(ctx context.Context, market string) ([]StockV2Instrument, error)

	CreateUpdateJob(ctx context.Context, job StockV2UpdateJob) error
	GetUpdateJob(ctx context.Context, id string) (StockV2UpdateJob, error)
	GetLatestUpdateJob(ctx context.Context) (StockV2UpdateJob, error)
	UpdateUpdateJob(ctx context.Context, job StockV2UpdateJob) error
	ListUpdateJobs(ctx context.Context, limit int) ([]StockV2UpdateJob, error)

	GetUpdateProgress(ctx context.Context, updateJobID string) (StockV2UpdateProgress, error)
	UpdateUpdateProgress(ctx context.Context, progress StockV2UpdateProgress) error

	CreateOrUpdateSettings(ctx context.Context, settings StockV2Settings) error
	GetSettings(ctx context.Context) (StockV2Settings, error)
}

// Store 数据存储包装器

// API HTTP API 请求响应结构

// PortfolioWithHoldings 带持仓信息的投资组合
type PortfolioWithHoldings struct {
	StockV2Portfolio
	TotalValue      float64          `json:"totalValue"`
	TotalAssetValue float64          `json:"totalAssetValue"`
	CashPct         float64          `json:"cashPct"`
	Holdings        []StockV2Holding `json:"holdings"`
}

// Snapshot V2 UI 快照数据。
//
// 这个结构用于前端首屏、右侧概览和轻量刷新：它聚合组合、少量主数据、
// 最近更新任务和设置，便于页面一次请求恢复当前工作台上下文。它不是全量
// 标的主数据导出，也不能用 Instruments 的长度判断主数据是否完整。需要
// 全量或分页检查标的主数据时，应使用 /api/stockv2/instruments 返回的
// items/total/limit/offset。
type Snapshot struct {
	Portfolios      []PortfolioWithHoldings `json:"portfolios"`
	Instruments     []StockV2Instrument     `json:"instruments"`
	InstrumentTotal int                     `json:"instrumentTotal"`
	UpdateJobs      []StockV2UpdateJob      `json:"updateJobs"`
	Settings        StockV2Settings         `json:"settings"`
	LastUpdate      time.Time               `json:"lastUpdate"`
}

// UniverseUpdateRequest 标的主数据更新请求
type UniverseUpdateRequest struct {
	TriggerType   string   `json:"triggerType"`   // manual, scheduled
	TriggerSource string   `json:"triggerSource"` // user, system
	Symbols       []string `json:"symbols"`       // 可选，为空则更新全部
	MaxSymbols    int      `json:"maxSymbols,omitempty"`
	ForceAI       bool     `json:"forceAi,omitempty"`
}

// UniverseUpdateResponse 更新响应
type UniverseUpdateResponse struct {
	JobID   string `json:"jobId"`
	Message string `json:"message"`
}

// RequestCreatePortfolio 创建投资组合请求
type RequestCreatePortfolio struct {
	Name                 string  `json:"name"`
	Description          string  `json:"description,omitempty"`
	Cash                 float64 `json:"cash"`
	RiskLevel            string  `json:"riskLevel"`
	MaxSinglePositionPct float64 `json:"maxSinglePositionPct"`
	MaxDrawdownPct       float64 `json:"maxDrawdownPct"`
	AllowBuy             *bool   `json:"allowBuy,omitempty"`
	AllowAdd             *bool   `json:"allowAdd,omitempty"`
	AllowReduce          *bool   `json:"allowReduce,omitempty"`
	AllowSell            *bool   `json:"allowSell,omitempty"`
	Notes                string  `json:"notes,omitempty"`
}

// RequestUpdatePortfolio 更新投资组合请求
type RequestUpdatePortfolio struct {
	StockV2PortfolioPatch
}

// RequestCreateHolding 创建持仓请求
type RequestCreateHolding struct {
	Symbol     string  `json:"symbol"`
	Name       string  `json:"name,omitempty"`
	Market     string  `json:"market,omitempty"`
	Quantity   float64 `json:"quantity"`
	CostPrice  float64 `json:"costPrice"`
	AcquiredAt string  `json:"acquiredAt,omitempty"` // 建仓时间,空用现在;支持 RFC3339 / datetime-local 格式
}

// RequestUpdateHolding 更新持仓请求
type RequestUpdateHolding struct {
	StockV2HoldingPatch
}

// RequestRecordTransaction 记录一笔买入/卖出交易。ExecutedAt 为空表示用当前时间,
// 非空时支持 RFC3339 或 datetime-local("2006-01-02T15:04")格式,允许填过去的成交时间。
type RequestRecordTransaction struct {
	Symbol     string  `json:"symbol"`
	Market     string  `json:"market,omitempty"`
	Name       string  `json:"name,omitempty"`
	Side       string  `json:"side"` // buy | sell
	Quantity   float64 `json:"quantity"`
	Price      float64 `json:"price"`
	ExecutedAt string  `json:"executedAt,omitempty"`
	Note       string  `json:"note,omitempty"`
}

// RequestCreateOrUpdateSettings 配置请求
type RequestCreateOrUpdateSettings struct {
	AutoUpdateEnabled                  *bool   `json:"autoUpdateEnabled,omitempty"`
	UpdateIntervalSec                  *int    `json:"updateIntervalSec,omitempty"`
	ProxyEnabled                       *bool   `json:"proxyEnabled,omitempty"`
	ProxyType                          *string `json:"proxyType,omitempty"`
	ProxyHost                          *string `json:"proxyHost,omitempty"`
	ProxyPort                          *int    `json:"proxyPort,omitempty"`
	DailyBarsAutoEnabled               *bool   `json:"dailyBarsAutoEnabled,omitempty"` // legacy: 新流程使用 autoUpdateEnabled
	BaseProfileAutoMaintainEnabled     *bool   `json:"baseProfileAutoMaintainEnabled,omitempty"`
	BaseProfileMaintainIntervalSeconds *int    `json:"baseProfileMaintainIntervalSeconds,omitempty"`
	BaseProfileDeepUpdateBatchSize     *int    `json:"baseProfileDeepUpdateBatchSize,omitempty"`
	BaseProfileDeepUpdateAIBudget      *int    `json:"baseProfileDeepUpdateAiBudget,omitempty"`
	BaseProfileDeepUpdateRateLimitMs   *int    `json:"baseProfileDeepUpdateRateLimitMs,omitempty"`
}

// 错误定义
var (
	ErrPortfolioNotFound       = errors.New("portfolio not found")
	ErrHoldingNotFound         = errors.New("holding not found")
	ErrInstrumentNotFound      = errors.New("instrument not found")
	ErrUpdateJobNotFound       = errors.New("update job not found")
	ErrInvalidRiskLevel        = errors.New("invalid risk level")
	ErrPositionConstraint      = errors.New("position exceeds constraint")
	InsufficientFunds          = errors.New("insufficient funds")
	ErrUpdateJobAlreadyRunning = errors.New("update job already running")
	ErrTransactionNotFound     = errors.New("transaction not found")
	ErrInvalidTransactionSide  = errors.New("invalid transaction side")
	ErrInsufficientHolding     = errors.New("insufficient holding quantity")
)

// 生成ID的辅助函数（带随机后缀，避免批量生成时冲突）
func generateID() string {
	buf := make([]byte, 4)
	rand.Read(buf)
	return "id-" + time.Now().Format("20060102150405-") + hex.EncodeToString(buf)
}
