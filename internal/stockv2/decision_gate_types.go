package stockv2

import "time"

const (
	DecisionGateStatusPass          = "pass"
	DecisionGateStatusBlocked       = "blocked"
	DecisionGateStatusDegraded      = "degraded"
	DecisionGateStatusNotApplicable = "not_applicable"

	DecisionHealthHealthy       = "healthy"
	DecisionHealthDegraded      = "degraded"
	DecisionHealthBlocked       = "blocked"
	DecisionHealthNotApplicable = "not_applicable"

	DecisionGateCatalystPricing = "catalyst_pricing"
	DecisionGateFactorCrowding  = "factor_crowding"
	DecisionGateVolatility      = "volatility_calibration"
	DecisionGateEventCalendar   = "event_calendar"
)

// DecisionDataHealth is deliberately source-level rather than transport-level:
// it tells the owner whether a decision may be trusted without exposing secrets.
type DecisionDataHealth struct {
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	Required  bool      `json:"required"`
	AsOf      string    `json:"asOf,omitempty"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

type DecisionGateResult struct {
	Key          string         `json:"key"`
	Label        string         `json:"label"`
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	Reasons      []string       `json:"reasons,omitempty"`
	Metrics      map[string]any `json:"metrics,omitempty"`
	EvidenceRefs []string       `json:"evidenceRefs,omitempty"`
}

// DecisionGateSnapshot is the deterministic server verdict consumed by Agents.
// It never authorizes execution; it only narrows the advisory action space.
type DecisionGateSnapshot struct {
	ID             string               `json:"id,omitempty"`
	ContextType    string               `json:"contextType,omitempty"`
	ContextID      string               `json:"contextId,omitempty"`
	Symbol         string               `json:"symbol"`
	Market         string               `json:"market,omitempty"`
	InstrumentType string               `json:"instrumentType,omitempty"`
	TradeDate      string               `json:"tradeDate,omitempty"`
	DecisionDate   string               `json:"decisionDate,omitempty"`
	Status         string               `json:"status"`
	MarketRegime   string               `json:"marketRegime,omitempty"`
	AllowedActions []string             `json:"allowedActions,omitempty"`
	Gates          []DecisionGateResult `json:"gates"`
	DataHealth     []DecisionDataHealth `json:"dataHealth"`
	Metrics        map[string]any       `json:"metrics,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
}

type DecisionGateOutcome struct {
	SnapshotID      string    `json:"snapshotId"`
	Horizon         int       `json:"horizon"`
	DueTradeDate    string    `json:"dueTradeDate,omitempty"`
	ObservedDate    string    `json:"observedDate,omitempty"`
	ReturnPct       float64   `json:"returnPct,omitempty"`
	ExcessReturnPct float64   `json:"excessReturnPct,omitempty"`
	Status          string    `json:"status"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type decisionMarketEvent struct {
	Symbol      string
	EventType   string
	EventDate   string
	AnnouncedAt string
	Title       string
	Source      string
	FetchedAt   time.Time
}

type decisionFinancialFact struct {
	Symbol            string
	ReportPeriod      string
	Dataset           string
	AnnouncedAt       string
	Revenue           float64
	NetProfit         float64
	OperatingCashFlow float64
	ROE               float64
	GrossMargin       float64
	Source            string
	FetchedAt         time.Time
}
