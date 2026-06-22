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
	BuiltAt   time.Time               `json:"builtAt"`
	Hit       MonitorHit              `json:"hit"`
	Evidence  map[string]any          `json:"evidence,omitempty"`
	Strategy  *StrategyWithVersion    `json:"strategy,omitempty"`
	Quote     *StockV2QuoteLatest     `json:"quote,omitempty"`
	DailyBars *DailyBarsContext       `json:"dailyBars,omitempty"`
	Portfolio *PortfolioReviewContext `json:"portfolio,omitempty"`
	Freshness map[string]any          `json:"freshness,omitempty"`
}

type DailyBarsContext struct {
	Symbol          string             `json:"symbol,omitempty"`
	Adjusted        string             `json:"adjusted,omitempty"`
	Count           int                `json:"count"`
	LatestTradeDate string             `json:"latestTradeDate,omitempty"`
	LatestClose     float64            `json:"latestClose,omitempty"`
	LatestFetchedAt time.Time          `json:"latestFetchedAt,omitempty"`
	Quality         string             `json:"quality,omitempty"`
	Summary         map[string]float64 `json:"summary,omitempty"`
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
