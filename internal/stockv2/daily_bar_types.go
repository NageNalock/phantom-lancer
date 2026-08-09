package stockv2

import (
	"errors"
	"time"
)

// Daily Bars 日级历史行情相关类型。
//
// 设计语义边界（见 docs/stock-agent-workbench-v2-key-points-2026-06-18.md）：
//   - Daily Bar 是按交易日落盘的日 K 历史序列，用于趋势 / 技术面分析。
//   - 它不是 Latest Quote（最近一次报价，单条），也不是分钟级行情。
//   - 失败时绝不伪造：不写空 bar、不清零旧价、不把成本价 / 最新价伪装成日 K。
//   - 每条 bar 都带 source / fetchedAt / quality，便于后续 Agent 与 Review 评估可信度。

// 日 K 复权类型
const (
	DailyBarAdjustedNone = "none" // 不复权
	DailyBarAdjustedQFQ  = "qfq"  // 前复权
	DailyBarAdjustedHFQ  = "hfq"  // 后复权
)

// 日 K 查询 / 补拉的区间码
const (
	DailyBarRange6M = "6m"
	DailyBarRange1Y = "1y"
	DailyBarRange3Y = "3y"
	DailyBarRange5Y = "5y"
)

// 日 K 质量状态
const (
	DailyBarQualityOK      = "ok"      // 正常落盘
	DailyBarQualityPartial = "partial" // 仅部分区间
	DailyBarQualityStale   = "stale"   // 数据陈旧
	DailyBarQualityFailed  = "failed"  // 抓取失败
	DailyBarQualityEmpty   = "empty"   // 本地无数据
)

// 日 K 任务类型与模式
const (
	DailyBarJobTypeEnsure      = "daily_bars_ensure"      // 单只按需补拉
	DailyBarJobTypeIncremental = "daily_bars_incremental" // 持仓批量增量

	DailyBarJobModeSymbol = "symbol" // 单只
	DailyBarJobModeHot    = "hot"    // 热集合（本轮 = 持仓）
)

// StockV2DailyBar 单个交易日的日 K 行情
type StockV2DailyBar struct {
	ID           string    `json:"id"`
	Symbol       string    `json:"symbol"`
	Market       string    `json:"market,omitempty"` // SH / SZ / BJ
	TradeDate    string    `json:"tradeDate"`        // "2006-01-02"
	Open         float64   `json:"open"`
	High         float64   `json:"high"`
	Low          float64   `json:"low"`
	Close        float64   `json:"close"`
	PrevClose    float64   `json:"prevClose"` // 前一交易日收盘；首条无前日时为 0
	Volume       float64   `json:"volume"`    // 单位：手（数据源原值，未换算）
	Amount       float64   `json:"amount"`    // 成交额；当前数据源不提供时为 0
	PctChange    float64   `json:"pctChange"` // 涨跌幅 %，prevClose<=0 时为 0
	Adjusted     string    `json:"adjusted"`  // none | qfq | hfq
	Source       string    `json:"source"`    // 如 tencent_fqkline
	FetchedAt    time.Time `json:"fetchedAt"`
	Quality      string    `json:"quality"` // ok | partial | stale | failed
	ErrorMessage string    `json:"errorMessage,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// DailyBarsQuality 日 K 数据质量评估
type DailyBarsQuality struct {
	Symbol           string    `json:"symbol"`
	Adjusted         string    `json:"adjusted"`
	HasData          bool      `json:"hasData"`
	RowCount         int       `json:"rowCount"`
	EarliestDate     string    `json:"earliestDate"` // 本地最早 tradeDate
	LatestDate       string    `json:"latestDate"`   // 本地最近 tradeDate
	Stale            bool      `json:"stale"`        // 最近一条距今超出容忍窗口
	Meets250         bool      `json:"meets250"`     // 行数是否满足最近 ~250 个交易日
	LastErrorMessage string    `json:"lastErrorMessage,omitempty"`
	Source           string    `json:"source,omitempty"`
	CheckedAt        time.Time `json:"checkedAt"`
}

// DailyBarsEnsureResult 按需补拉结果
type DailyBarsEnsureResult struct {
	Symbol       string           `json:"symbol"`
	RangeCode    string           `json:"range"`
	Adjusted     string           `json:"adjusted"`
	Fetched      int              `json:"fetched"`      // 本次实际抓取条数（异步任务尚未完成时为 0）
	Skipped      bool             `json:"skipped"`      // 本地已满足，跳过抓取
	EarliestDate string           `json:"earliestDate"` // 任务识别的本地最早日期
	LatestDate   string           `json:"latestDate"`   // 任务识别的本地最近日期
	Quality      DailyBarsQuality `json:"quality"`
	JobID        string           `json:"jobId,omitempty"` // 异步任务 id
	JobRunning   bool             `json:"jobRunning"`      // 是否仍有异步任务在跑
	ErrorMessage string           `json:"errorMessage,omitempty"`
}

// DailyBarsJobRequest 触发日 K 任务的请求
type DailyBarsJobRequest struct {
	Mode          string `json:"mode"`                    // symbol | hot
	Symbol        string `json:"symbol,omitempty"`        // mode=symbol 时必填
	RangeCode     string `json:"range,omitempty"`         // 默认 1y
	Adjusted      string `json:"adjusted,omitempty"`      // 默认 none
	TriggerType   string `json:"triggerType,omitempty"`   // manual | scheduled | system
	TriggerSource string `json:"triggerSource,omitempty"` // web | auto-updater | agent
}

// StockV2DailyBarJob 日 K 任务记录
type StockV2DailyBarJob struct {
	ID             string          `json:"id"`
	JobType        string          `json:"jobType"`          // daily_bars_ensure | daily_bars_incremental
	Mode           string          `json:"mode"`             // symbol | hot
	Symbol         string          `json:"symbol,omitempty"` // mode=symbol 时的标的代码，用于去重与详情轮询
	Status         string          `json:"status"`           // running | completed | failed | cancelled
	TotalCount     int             `json:"totalCount"`
	ProcessedCount int             `json:"processedCount"`
	SuccessCount   int             `json:"successCount"`
	FailedCount    int             `json:"failedCount"`
	FailedItems    []UpdateFailure `json:"failedItems,omitempty"`
	RangeCode      string          `json:"range,omitempty"`
	Adjusted       string          `json:"adjusted,omitempty"`
	TriggerType    string          `json:"triggerType,omitempty"`
	TriggerSource  string          `json:"triggerSource,omitempty"`
	StartAt        time.Time       `json:"startAt"`
	EndAt          time.Time       `json:"endAt"`
	ErrorMessage   string          `json:"errorMessage,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}

// 日 K 相关错误
var (
	ErrDailyBarJobAlreadyRunning = errors.New("daily bars job already running")
	ErrDailyBarInvalidRange      = errors.New("invalid daily bars range")
	ErrDailyBarInvalidAdjusted   = errors.New("invalid daily bars adjusted")
	ErrDailyBarJobNotFound       = errors.New("daily bars job not found")
)
