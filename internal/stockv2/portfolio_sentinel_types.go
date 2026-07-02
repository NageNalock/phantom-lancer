package stockv2

import (
	"errors"
	"time"
)

const (
	PortfolioSentinelOutputType          = AgentTaskTypePortfolioSentinel
	PortfolioSentinelReportSchemaVersion = "portfolio-sentinel-report/v1"
)

const (
	PortfolioSentinelStatusRunning   = "running"
	PortfolioSentinelStatusCompleted = "completed"
	PortfolioSentinelStatusFailed    = "failed"
)

const (
	PortfolioSentinelTriggerManual    = "manual"
	PortfolioSentinelTriggerScheduled = "scheduled"
)

const (
	PortfolioSentinelWindowManual    = "manual"
	PortfolioSentinelWindowPreMarket = "pre_market"
	PortfolioSentinelWindowMidday    = "midday"
	PortfolioSentinelWindowPostClose = "post_close"
)

const (
	PortfolioSentinelRiskLow      = "low"
	PortfolioSentinelRiskMedium   = "medium"
	PortfolioSentinelRiskHigh     = "high"
	PortfolioSentinelRiskCritical = "critical"
)

var (
	ErrPortfolioSentinelRunNotFound       = errors.New("portfolio sentinel run not found")
	ErrPortfolioSentinelResultNotFound    = errors.New("portfolio sentinel result not found")
	ErrInvalidPortfolioSentinelInput      = errors.New("invalid portfolio sentinel input")
	ErrInvalidPortfolioSentinelStatus     = errors.New("invalid portfolio sentinel status")
	ErrInvalidPortfolioSentinelResult     = errors.New("invalid portfolio sentinel result")
	ErrPortfolioSentinelAlreadyRunning    = errors.New("portfolio sentinel run already running")
	ErrPortfolioSentinelScheduledDisabled = errors.New("portfolio sentinel scheduled window disabled")
)

type PortfolioSentinelRun struct {
	ID                    string    `json:"id"`
	PortfolioID           string    `json:"portfolioId,omitempty"`
	AgentRunID            string    `json:"agentRunId,omitempty"`
	DecisionLedgerID      string    `json:"decisionLedgerId,omitempty"`
	Status                string    `json:"status"`
	TriggerType           string    `json:"triggerType"`
	WindowType            string    `json:"windowType"`
	WindowStartAt         time.Time `json:"windowStartAt"`
	WindowEndAt           time.Time `json:"windowEndAt"`
	ScannedPortfolioCount int       `json:"scannedPortfolioCount"`
	ScannedHoldingCount   int       `json:"scannedHoldingCount"`
	NewsEventCount        int       `json:"newsEventCount"`
	RawNewsCount          int       `json:"rawNewsCount"`
	QuoteCount            int       `json:"quoteCount"`
	DailyBarSymbolCount   int       `json:"dailyBarSymbolCount"`
	MinuteBarSymbolCount  int       `json:"minuteBarSymbolCount"`
	ResultRiskLevel       string    `json:"resultRiskLevel,omitempty"`
	GeneratedAlertCount   int       `json:"generatedAlertCount"`
	GeneratedHitCount     int       `json:"generatedHitCount"`
	GeneratedReviewCount  int       `json:"generatedReviewCount"`
	ErrorMessage          string    `json:"errorMessage,omitempty"`
	StartedAt             time.Time `json:"startedAt"`
	FinishedAt            time.Time `json:"finishedAt,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

type PortfolioSentinelResult struct {
	ID                   string         `json:"id"`
	RunID                string         `json:"runId"`
	SchemaVersion        string         `json:"schemaVersion"`
	Summary              string         `json:"summary,omitempty"`
	RiskLevel            string         `json:"riskLevel,omitempty"`
	RawResult            map[string]any `json:"rawResult,omitempty"`
	ContextSummary       map[string]any `json:"contextSummary,omitempty"`
	DerivedAlertIDs      []string       `json:"derivedAlertIds,omitempty"`
	DerivedMonitorHitIDs []string       `json:"derivedMonitorHitIds,omitempty"`
	DerivedReviewIDs     []string       `json:"derivedReviewIds,omitempty"`
	CreatedAt            time.Time      `json:"createdAt"`
}

type PortfolioSentinelConfig struct {
	ID                      string    `json:"id"`
	Enabled                 bool      `json:"enabled"`
	PreMarketEnabled        bool      `json:"preMarketEnabled"`
	MiddayEnabled           bool      `json:"middayEnabled"`
	PostCloseEnabled        bool      `json:"postCloseEnabled"`
	MaxNewsItems            int       `json:"maxNewsItems"`
	MaxNewsPerHolding       int       `json:"maxNewsPerHolding"`
	AgentDoublecheckEnabled bool      `json:"agentDoublecheckEnabled"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

type PortfolioSentinelRunListFilter struct {
	Status      string
	TriggerType string
	WindowType  string
	PortfolioID string
	Limit       int
	Offset      int
}

type RequestRunPortfolioSentinel struct {
	PortfolioID string `json:"portfolioId,omitempty"`
	WindowType  string `json:"windowType,omitempty"`
	StartAt     string `json:"startAt,omitempty"`
	EndAt       string `json:"endAt,omitempty"`
	Note        string `json:"note,omitempty"`
}

type RequestUpdatePortfolioSentinelConfig struct {
	Enabled                 *bool `json:"enabled,omitempty"`
	PreMarketEnabled        *bool `json:"preMarketEnabled,omitempty"`
	MiddayEnabled           *bool `json:"middayEnabled,omitempty"`
	PostCloseEnabled        *bool `json:"postCloseEnabled,omitempty"`
	MaxNewsItems            *int  `json:"maxNewsItems,omitempty"`
	MaxNewsPerHolding       *int  `json:"maxNewsPerHolding,omitempty"`
	AgentDoublecheckEnabled *bool `json:"agentDoublecheckEnabled,omitempty"`
}

type PortfolioSentinelRunDetail struct {
	Run     PortfolioSentinelRun     `json:"run"`
	Result  *PortfolioSentinelResult `json:"result,omitempty"`
	Agent   *AgentRun                `json:"agentRun,omitempty"`
	Ledger  *AgentDecisionLedger     `json:"decisionLedger,omitempty"`
	Alerts  []StockV2Alert           `json:"alerts,omitempty"`
	Hits    []MonitorHit             `json:"monitorHits,omitempty"`
	Reviews []OperationReview        `json:"reviews,omitempty"`
}

type PortfolioSentinelContext struct {
	SchemaVersion string                              `json:"schemaVersion"`
	RunID         string                              `json:"runId"`
	Window        PortfolioSentinelWindowContext      `json:"window"`
	Portfolios    []PortfolioSentinelPortfolioContext `json:"portfolios"`
	NewsEvents    []NewsEvent                         `json:"newsEvents,omitempty"`
	RawNews       []StockV2RawNews                    `json:"rawNews,omitempty"`
	RecentReviews []OperationReview                   `json:"recentReviews,omitempty"`
	Transactions  []StockV2Transaction                `json:"recentTransactions,omitempty"`
	DataFreshness map[string]any                      `json:"dataFreshness,omitempty"`
	ContextStats  map[string]any                      `json:"contextStats,omitempty"`
	Note          string                              `json:"note,omitempty"`
}

type PortfolioSentinelWindowContext struct {
	Type          string    `json:"type"`
	TriggerType   string    `json:"triggerType"`
	StartAt       time.Time `json:"startAt"`
	EndAt         time.Time `json:"endAt"`
	MarketSession string    `json:"marketSession,omitempty"`
}

type PortfolioSentinelPortfolioContext struct {
	Portfolio StockV2Portfolio                  `json:"portfolio"`
	Snapshot  *PortfolioSnapshot                `json:"snapshot,omitempty"`
	Holdings  []PortfolioSentinelHoldingContext `json:"holdings,omitempty"`
}

type PortfolioSentinelHoldingContext struct {
	Holding    StockV2Holding      `json:"holding"`
	Quote      *StockV2QuoteLatest `json:"quote,omitempty"`
	DailyBars  *DailyBarsContext   `json:"dailyBars,omitempty"`
	MinuteBars *MinuteBarsContext  `json:"minuteBars,omitempty"`
	Profile    *StockProfile       `json:"stockProfile,omitempty"`
	News       []NewsEvent         `json:"news,omitempty"`
	RawNews    []StockV2RawNews    `json:"rawNews,omitempty"`
	Freshness  map[string]any      `json:"freshness,omitempty"`
}

type PortfolioSentinelReport struct {
	SchemaVersion    string                             `json:"schema_version"`
	OverallRiskLevel string                             `json:"overall_risk_level"`
	RunSummary       string                             `json:"run_summary"`
	PositiveItems    []map[string]any                   `json:"positive_items,omitempty"`
	NegativeItems    []map[string]any                   `json:"negative_items,omitempty"`
	NoiseItems       []map[string]any                   `json:"noise_items,omitempty"`
	AffectedHoldings []PortfolioSentinelAffectedHolding `json:"affected_holdings,omitempty"`
	PortfolioActions []PortfolioSentinelAction          `json:"portfolio_actions,omitempty"`
	ReviewRequests   []PortfolioSentinelReviewRequest   `json:"review_requests,omitempty"`
	DataQualityNotes []string                           `json:"data_quality_notes,omitempty"`
	NextWatchFocus   []string                           `json:"next_watch_focus,omitempty"`
}

type PortfolioSentinelAffectedHolding struct {
	Symbol       string   `json:"symbol"`
	Market       string   `json:"market,omitempty"`
	Name         string   `json:"name,omitempty"`
	RiskLevel    string   `json:"risk_level,omitempty"`
	Direction    string   `json:"direction,omitempty"`
	Reasons      []string `json:"reasons,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type PortfolioSentinelAction struct {
	Symbol            string         `json:"symbol"`
	Market            string         `json:"market,omitempty"`
	PortfolioID       string         `json:"portfolio_id,omitempty"`
	OutputType        string         `json:"output_type"`
	ResultSummary     string         `json:"result_summary,omitempty"`
	ProposedOperation map[string]any `json:"proposed_operation,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	RiskNotes         string         `json:"risk_notes,omitempty"`
	Confidence        float64        `json:"confidence,omitempty"`
}

type PortfolioSentinelReviewRequest struct {
	Symbol       string   `json:"symbol"`
	Market       string   `json:"market,omitempty"`
	PortfolioID  string   `json:"portfolio_id,omitempty"`
	Title        string   `json:"title,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	RiskLevel    string   `json:"risk_level,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}
