package stockv2

import (
	"errors"
	"time"
)

const (
	OperationReviewStatusPending   = "pending"
	OperationReviewStatusRunning   = "running"
	OperationReviewStatusCompleted = "completed"
	OperationReviewStatusFailed    = "failed"
	OperationReviewStatusClosed    = "closed"
)

const (
	OperationReviewOutputTradeSignal        = "trade_signal"
	OperationReviewOutputProposedOperation  = "proposed_operation"
	OperationReviewOutputStrategyPatch      = "strategy_patch"
	OperationReviewOutputIgnore             = "ignore"
	OperationReviewOutputContinueMonitoring = "continue_monitoring"
)

var (
	ErrOperationReviewNotFound          = errors.New("operation review not found")
	ErrInvalidOperationReviewStatus     = errors.New("invalid operation review status")
	ErrInvalidOperationReviewOutputType = errors.New("invalid operation review output type")
	ErrInvalidProposedOperation         = errors.New("invalid proposed operation")
	ErrInvalidOperationReviewAction     = errors.New("invalid operation review action")
)

type OperationReview struct {
	ID            string           `json:"id"`
	HitID         string           `json:"hitId"`
	RunID         string           `json:"runId,omitempty"`
	Status        string           `json:"status"`
	OutputType    string           `json:"outputType,omitempty"`
	StrategyID    string           `json:"strategyId,omitempty"`
	PortfolioID   string           `json:"portfolioId,omitempty"`
	Symbol        string           `json:"symbol,omitempty"`
	Market        string           `json:"market,omitempty"`
	InputContext  AgentContextPack `json:"inputContext"`
	Result        map[string]any   `json:"result,omitempty"`
	ResultSummary string           `json:"resultSummary,omitempty"`
	ErrorMessage  string           `json:"errorMessage,omitempty"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
	CompletedAt   time.Time        `json:"completedAt,omitempty"`
	ClosedAt      time.Time        `json:"closedAt,omitempty"`
}

type AgentContextPack struct {
	BuiltAt    time.Time               `json:"builtAt"`
	Hit        MonitorHit              `json:"hit"`
	Evidence   map[string]any          `json:"evidence,omitempty"`
	Strategy   *StrategyWithVersion    `json:"strategy,omitempty"`
	Quote      *StockV2QuoteLatest     `json:"quote,omitempty"`
	DailyBars  *DailyBarsContext       `json:"dailyBars,omitempty"`
	MinuteBars *MinuteBarsContext      `json:"minuteBars,omitempty"`
	Portfolio  *PortfolioReviewContext `json:"portfolio,omitempty"`
	NewsEvent  *NewsEvent              `json:"newsEvent,omitempty"`
	NewsLink   *NewsLinkCandidate      `json:"newsLinkCandidate,omitempty"`
	Profile    *StockProfile           `json:"stockProfile,omitempty"`
	Freshness  map[string]any          `json:"freshness,omitempty"`
}

type DailyBarsContext struct {
	Symbol                   string                   `json:"symbol,omitempty"`
	Adjusted                 string                   `json:"adjusted,omitempty"`
	Count                    int                      `json:"count"`
	LatestTradeDate          string                   `json:"latestTradeDate,omitempty"`
	LatestClose              float64                  `json:"latestClose,omitempty"`
	LatestFetchedAt          time.Time                `json:"latestFetchedAt,omitempty"`
	Quality                  string                   `json:"quality,omitempty"`
	CoverageStatus           string                   `json:"coverageStatus,omitempty"`
	CheckedAt                time.Time                `json:"checkedAt,omitempty"`
	RefreshAttempted         bool                     `json:"refreshAttempted,omitempty"`
	CurrentSessionIncomplete bool                     `json:"currentSessionIncomplete,omitempty"`
	RefreshError             string                   `json:"refreshError,omitempty"`
	Summary                  map[string]float64       `json:"summary,omitempty"`
	RecentBars               []DailyBarEvidencePoint  `json:"recentBars,omitempty"`
	MarketStructure          *MarketStructureEvidence `json:"marketStructure,omitempty"`
}

// DailyBarEvidencePoint is the bounded, identifier-free price/volume shape
// supplied to an Agent. QFQ points are trend evidence, never executable prices.
type DailyBarEvidencePoint struct {
	TradeDate string  `json:"tradeDate"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	PrevClose float64 `json:"prevClose,omitempty"`
	Volume    float64 `json:"volume,omitempty"`
	Amount    float64 `json:"amount,omitempty"`
	PctChange float64 `json:"pctChange"`
}

type MarketStructureEvidence struct {
	WindowTradingDays        int     `json:"windowTradingDays"`
	Return3Pct               float64 `json:"return3Pct"`
	VolumeRatio3ToPrior      float64 `json:"volumeRatio3ToPrior,omitempty"`
	AmountRatio3ToPrior      float64 `json:"amountRatio3ToPrior,omitempty"`
	LatestCloseLocationPct   float64 `json:"latestCloseLocationPct"`
	LowCloseDays3            int     `json:"lowCloseDays3"`
	PriorBreakoutTradeDate   string  `json:"priorBreakoutTradeDate,omitempty"`
	PriorBreakoutReturnPct   float64 `json:"priorBreakoutReturnPct,omitempty"`
	HighVolumeStall          bool    `json:"highVolumeStall"`
	PostBreakoutDistribution bool    `json:"postBreakoutDistribution"`
	PotentialSupplyPressure  bool    `json:"potentialSupplyPressure"`
}

type MinuteBarsContext struct {
	Symbol                string             `json:"symbol,omitempty"`
	Count                 int                `json:"count"`
	SessionDate           string             `json:"sessionDate,omitempty"`
	LatestMinuteAt        time.Time          `json:"latestMinuteAt,omitempty"`
	LatestClose           float64            `json:"latestClose,omitempty"`
	LatestVolume          float64            `json:"latestVolume,omitempty"`
	LatestNetInflow       float64            `json:"latestNetInflow,omitempty"`
	SessionOpen           float64            `json:"sessionOpen,omitempty"`
	SessionHigh           float64            `json:"sessionHigh,omitempty"`
	SessionLow            float64            `json:"sessionLow,omitempty"`
	PrevClose             float64            `json:"prevClose,omitempty"`
	SessionPctChange      float64            `json:"sessionPctChange,omitempty"`
	ReturnFromOpenPct     float64            `json:"returnFromOpenPct,omitempty"`
	Momentum5Pct          float64            `json:"momentum5Pct,omitempty"`
	Momentum15Pct         float64            `json:"momentum15Pct,omitempty"`
	SessionVolume         float64            `json:"sessionVolume,omitempty"`
	SessionAmount         float64            `json:"sessionAmount,omitempty"`
	First15MinuteAmount   float64            `json:"first15MinuteAmount,omitempty"`
	SameMinuteAmountRatio float64            `json:"sameMinuteAmountRatio,omitempty"`
	VWAP                  float64            `json:"vwap,omitempty"`
	RangePositionPct      float64            `json:"rangePositionPct,omitempty"`
	FlowAvailable         bool               `json:"flowAvailable"`
	SessionMainNetInflow  float64            `json:"sessionMainNetInflow,omitempty"`
	Source                string             `json:"source,omitempty"`
	Summary               map[string]float64 `json:"summary,omitempty"`
}

type PortfolioReviewContext struct {
	Portfolio StockV2Portfolio   `json:"portfolio"`
	Snapshot  *PortfolioSnapshot `json:"snapshot,omitempty"`
	Holdings  []StockV2Holding   `json:"holdings,omitempty"`
}

type OperationReviewListFilter struct {
	Status      string
	OutputType  string
	HitID       string
	RunID       string
	StrategyID  string
	PortfolioID string
	Symbol      string
	Limit       int
	Offset      int
}

type RequestSaveOperationReviewResult struct {
	OutputType    string         `json:"outputType"`
	Result        map[string]any `json:"result,omitempty"`
	ResultSummary string         `json:"resultSummary,omitempty"`
	Status        string         `json:"status,omitempty"`
	ErrorMessage  string         `json:"errorMessage,omitempty"`
}

type RequestApplyOperationReviewAction struct {
	Reason     string  `json:"reason,omitempty"`
	ExecutedAt string  `json:"executedAt,omitempty"`
	Price      float64 `json:"price,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
}
