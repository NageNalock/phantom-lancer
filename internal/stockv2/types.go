package stockv2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// StockV2Instrument 股票主数据
type StockV2Instrument struct {
	ID          string    `json:"id"`
	Symbol      string    `json:"symbol"`
	Market      string    `json:"market"`
	Name        string    `json:"name"`
	Industry    string    `json:"industry"`
	Sector      string    `json:"sector"`
	Concepts    []string  `json:"concepts"` // 概念信息数组
	ListDate    string    `json:"listDate"`
	DelistDate  string    `json:"delistDate"`
	Status      string    `json:"status"`    // active, delisted, suspended
	LastUpdate  time.Time `json:"lastUpdate"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// StockV2Portfolio 投资组合/仓位
type StockV2Portfolio struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description,omitempty"`
	Cash                 float64   `json:"cash"`
	RiskLevel            string    `json:"riskLevel"`          // low, medium, high
	MaxSinglePositionPct float64   `json:"maxSinglePositionPct"` // 单票最大持仓比例
	MaxDrawdownPct       float64   `json:"maxDrawdownPct"`     // 最大回撤比例
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
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// StockV2HoldingPatch 持仓更新补丁
type StockV2HoldingPatch struct {
	Quantity          *float64 `json:"quantity,omitempty"`
	AvailableQuantity *float64 `json:"availableQuantity,omitempty"`
	CostPrice         *float64 `json:"costPrice,omitempty"`
	LastPrice         *float64 `json:"lastPrice,omitempty"`
}

// StockV2UpdateJob 更新任务记录
type StockV2UpdateJob struct {
	ID             string            `json:"id"`
	TriggerType    string            `json:"triggerType"`    // manual, scheduled
	TriggerSource  string            `json:"triggerSource"`  // user, system
	Status         string            `json:"status"`         // running, completed, failed, cancelled
	TotalCount     int               `json:"totalCount"`
	ProcessedCount int               `json:"processedCount"`
	SuccessCount   int               `json:"successCount"`
	FailedCount    int               `json:"failedCount"`
	FailedItems    []UpdateFailure   `json:"failedItems,omitempty"` // 失败详情
	StartAt        time.Time         `json:"startAt"`
	EndAt          time.Time         `json:"endAt"`
	ErrorMessage   string            `json:"errorMessage,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
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
	ID                   string    `json:"id"`
	AutoUpdateEnabled   bool      `json:"autoUpdateEnabled"`
	UpdateIntervalSec   int       `json:"updateIntervalSec"`
	ProxyEnabled        bool      `json:"proxyEnabled"`
	ProxyType           string    `json:"proxyType"`
	ProxyHost           string    `json:"proxyHost"`
	ProxyPort           int       `json:"proxyPort"`
	LastScheduledUpdate time.Time `json:"lastScheduledUpdate"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// StockV2SettingsPatch 配置更新补丁
type StockV2SettingsPatch struct {
	AutoUpdateEnabled *bool  `json:"autoUpdateEnabled,omitempty"`
	UpdateIntervalSec *int   `json:"updateIntervalSec,omitempty"`
	ProxyEnabled      *bool  `json:"proxyEnabled,omitempty"`
	ProxyType         *string `json:"proxyType,omitempty"`
	ProxyHost         *string `json:"proxyHost,omitempty"`
	ProxyPort         *int    `json:"proxyPort,omitempty"`
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
	TotalValue      float64            `json:"totalValue"`
	TotalAssetValue float64            `json:"totalAssetValue"`
	CashPct         float64            `json:"cashPct"`
	Holdings        []StockV2Holding   `json:"holdings"`
}

// Snapshot V2 快照数据
type Snapshot struct {
	Portfolios     []PortfolioWithHoldings `json:"portfolios"`
	Instruments    []StockV2Instrument     `json:"instruments"`
	UpdateJobs     []StockV2UpdateJob     `json:"updateJobs"`
	Settings       StockV2Settings        `json:"settings"`
	LastUpdate     time.Time              `json:"lastUpdate"`
}

// UniverseUpdateRequest 股票主数据更新请求
type UniverseUpdateRequest struct {
	TriggerType    string `json:"triggerType"`  // manual, scheduled
	TriggerSource  string `json:"triggerSource"` // user, system
	Symbols        []string `json:"symbols"` // 可选，为空则更新全部
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
	AllowBuy             bool    `json:"allowBuy"`
	AllowAdd             bool    `json:"allowAdd"`
	AllowReduce          bool    `json:"allowReduce"`
	AllowSell            bool    `json:"allowSell"`
	Notes                string  `json:"notes,omitempty"`
}

// RequestUpdatePortfolio 更新投资组合请求
type RequestUpdatePortfolio struct {
	StockV2PortfolioPatch
}

// RequestCreateHolding 创建持仓请求
type RequestCreateHolding struct {
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name,omitempty"`
	Market            string  `json:"market,omitempty"`
	Quantity          float64 `json:"quantity"`
	CostPrice         float64 `json:"costPrice"`
}

// RequestUpdateHolding 更新持仓请求
type RequestUpdateHolding struct {
	StockV2HoldingPatch
}

// RequestCreateOrUpdateSettings 配置请求
type RequestCreateOrUpdateSettings struct {
	AutoUpdateEnabled *bool   `json:"autoUpdateEnabled,omitempty"`
	UpdateIntervalSec *int    `json:"updateIntervalSec,omitempty"`
	ProxyEnabled      *bool   `json:"proxyEnabled,omitempty"`
	ProxyType         *string `json:"proxyType,omitempty"`
	ProxyHost         *string `json:"proxyHost,omitempty"`
	ProxyPort         *int    `json:"proxyPort,omitempty"`
}

// 错误定义
var (
	ErrPortfolioNotFound      = errors.New("portfolio not found")
	ErrHoldingNotFound        = errors.New("holding not found")
	ErrInstrumentNotFound    = errors.New("instrument not found")
	ErrUpdateJobNotFound      = errors.New("update job not found")
	ErrInvalidRiskLevel       = errors.New("invalid risk level")
	ErrPositionConstraint     = errors.New("position exceeds constraint")
	InsufficientFunds         = errors.New("insufficient funds")
	ErrUpdateJobAlreadyRunning = errors.New("update job already running")
)

// 生成ID的辅助函数（带随机后缀，避免批量生成时冲突）
func generateID() string {
	buf := make([]byte, 4)
	rand.Read(buf)
	return "id-" + time.Now().Format("20060102150405-") + hex.EncodeToString(buf)
}

