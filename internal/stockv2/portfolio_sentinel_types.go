package stockv2

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	PortfolioSentinelOutputType          = AgentTaskTypePortfolioSentinel
	PortfolioSentinelReportSchemaVersion = "portfolio-sentinel-report/v2"
	portfolioSentinelLegacySchemaVersion = "portfolio-sentinel-report/v1"
	portfolioSentinelPlanValidity        = 7 * 24 * time.Hour
)

const (
	PortfolioSentinelPlanBuild  = "build_position"
	PortfolioSentinelPlanAdd    = "add_position"
	PortfolioSentinelPlanHold   = "hold"
	PortfolioSentinelPlanReduce = "reduce_position"
	PortfolioSentinelPlanExit   = "exit_position"

	PortfolioSentinelTriggerImmediate   = "immediate"
	PortfolioSentinelTriggerConditional = "conditional"

	PortfolioSentinelSizingAvailableQuantityPct = "available_quantity_pct"
	PortfolioSentinelSizingTargetPortfolioPct   = "target_portfolio_pct"
)

const (
	portfolioSentinelImpactObjectHoldings      = "holdings"
	portfolioSentinelImpactObjectMonitors      = "monitors"
	portfolioSentinelImpactObjectAlerts        = "alerts"
	portfolioSentinelImpactObjectOpportunities = "opportunities"
	portfolioSentinelImpactObjectStrategies    = "strategies"
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

type PortfolioSentinelActionPlanListFilter struct {
	PortfolioID    string
	Action         string
	IncludeExpired bool
}

type PortfolioSentinelActionPlanView struct {
	Plan          PortfolioSentinelActionPlan `json:"plan"`
	RunID         string                      `json:"runId"`
	ResultID      string                      `json:"resultId"`
	RunFinishedAt time.Time                   `json:"runFinishedAt,omitempty"`
	Status        string                      `json:"status"`
}

type RequestRunPortfolioSentinel struct {
	PortfolioID      string `json:"portfolioId,omitempty"`
	WindowType       string `json:"windowType,omitempty"`
	StartAt          string `json:"startAt,omitempty"`
	EndAt            string `json:"endAt,omitempty"`
	Note             string `json:"note,omitempty"`
	NewsContextRunID string `json:"newsContextRunId,omitempty"`
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
	Run           PortfolioSentinelRun            `json:"run"`
	Result        *PortfolioSentinelResult        `json:"result,omitempty"`
	Agent         *AgentRun                       `json:"agentRun,omitempty"`
	Ledger        *AgentDecisionLedger            `json:"decisionLedger,omitempty"`
	AgentAttempts []PortfolioSentinelAgentAttempt `json:"agentAttempts,omitempty"`
	Alerts        []StockV2Alert                  `json:"alerts,omitempty"`
	Hits          []MonitorHit                    `json:"monitorHits,omitempty"`
	Reviews       []OperationReview               `json:"reviews,omitempty"`
}

type PortfolioSentinelAgentAttempt struct {
	Run    AgentRun             `json:"run"`
	Ledger *AgentDecisionLedger `json:"ledger,omitempty"`
}

type PortfolioSentinelContext struct {
	SchemaVersion  string                              `json:"schemaVersion"`
	RunID          string                              `json:"runId"`
	Window         PortfolioSentinelWindowContext      `json:"window"`
	Portfolios     []PortfolioSentinelPortfolioContext `json:"portfolios"`
	Candidates     []PortfolioSentinelCandidateContext `json:"trustedCandidates,omitempty"`
	Themes         []PortfolioSentinelThemeContext     `json:"activeThemes,omitempty"`
	PriorJudgments []PortfolioSentinelPriorJudgment    `json:"priorHoldingJudgments,omitempty"`
	NewsEvents     []NewsEvent                         `json:"newsEvents,omitempty"`
	RawNews        []StockV2RawNews                    `json:"rawNews,omitempty"`
	RecentReviews  []OperationReview                   `json:"recentReviews,omitempty"`
	Transactions   []StockV2Transaction                `json:"recentTransactions,omitempty"`
	DataFreshness  map[string]any                      `json:"dataFreshness,omitempty"`
	ContextStats   map[string]any                      `json:"contextStats,omitempty"`
	NewsContext    *PortfolioSentinelNewsContext       `json:"newsContext,omitempty"`
	Note           string                              `json:"note,omitempty"`
}

type PortfolioSentinelPriorJudgment struct {
	PortfolioID       string                           `json:"portfolioId"`
	Symbol            string                           `json:"symbol"`
	Market            string                           `json:"market,omitempty"`
	Name              string                           `json:"name,omitempty"`
	Action            string                           `json:"action"`
	TriggerMode       string                           `json:"triggerMode,omitempty"`
	TriggerPolicy     string                           `json:"triggerPolicy,omitempty"`
	Conditions        []PortfolioSentinelPlanCondition `json:"conditions,omitempty"`
	Sizing            *PortfolioSentinelPlanSizing     `json:"sizing,omitempty"`
	Reason            string                           `json:"reason"`
	RiskNotes         string                           `json:"riskNotes,omitempty"`
	RiskLevel         string                           `json:"riskLevel,omitempty"`
	AffectedReasons   []string                         `json:"affectedReasons,omitempty"`
	Confidence        float64                          `json:"confidence,omitempty"`
	SourceRunID       string                           `json:"sourceRunId"`
	SourceFinishedAt  time.Time                        `json:"sourceFinishedAt"`
	SourceWindowEndAt time.Time                        `json:"sourceWindowEndAt,omitempty"`
	ValidUntil        time.Time                        `json:"validUntil,omitempty"`
	AdvisoryOnly      bool                             `json:"advisoryOnly"`
}

type PortfolioSentinelCandidateContext struct {
	Symbol    string   `json:"symbol"`
	Market    string   `json:"market,omitempty"`
	Name      string   `json:"name,omitempty"`
	Sources   []string `json:"sources"`
	Rationale string   `json:"rationale,omitempty"`
}

type PortfolioSentinelThemeContext struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Stage          string   `json:"stage,omitempty"`
	Symbols        []string `json:"symbols,omitempty"`
	LatestChange   string   `json:"latestChange,omitempty"`
	MaterialChange bool     `json:"materialChange"`
}

type PortfolioSentinelNewsContext struct {
	RunID                    string                                    `json:"runId"`
	ChangedThreadCount       int                                       `json:"changedThreadCount"`
	MaterialChangeCount      int                                       `json:"materialChangeCount"`
	RequiredMCPTool          string                                    `json:"requiredMcpTool"`
	ImpactReviewScope        PortfolioSentinelImpactReviewScopeSummary `json:"impactReviewScope"`
	ImpactReviewRequiredTool string                                    `json:"impactReviewRequiredMcpTool"`
}

type PortfolioSentinelImpactReviewScopeSummary struct {
	HoldingCount     int `json:"holdingCount"`
	MonitorCount     int `json:"monitorCount"`
	AlertCount       int `json:"alertCount"`
	OpportunityCount int `json:"opportunityCount"`
	StrategyCount    int `json:"strategyCount"`
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
	NewsLinks  []NewsLinkCandidate `json:"newsLinks,omitempty"`
	RawNews    []StockV2RawNews    `json:"rawNews,omitempty"`
	Freshness  map[string]any      `json:"freshness,omitempty"`
}

type PortfolioSentinelReport struct {
	SchemaVersion               string                                 `json:"schema_version"`
	OverallRiskLevel            string                                 `json:"overall_risk_level"`
	RunSummary                  string                                 `json:"run_summary"`
	PositiveItems               []map[string]any                       `json:"positive_items,omitempty"`
	NegativeItems               []map[string]any                       `json:"negative_items,omitempty"`
	NoiseItems                  []map[string]any                       `json:"noise_items,omitempty"`
	AffectedHoldings            []PortfolioSentinelAffectedHolding     `json:"affected_holdings,omitempty"`
	PortfolioActions            []PortfolioSentinelAction              `json:"portfolio_actions,omitempty"`
	ActionPlans                 []PortfolioSentinelActionPlan          `json:"action_plans,omitempty"`
	ResearchAudit               []PortfolioSentinelResearchRecord      `json:"research_audit,omitempty"`
	ReviewRequests              []PortfolioSentinelReviewRequest       `json:"review_requests,omitempty"`
	DataQualityNotes            []string                               `json:"data_quality_notes,omitempty"`
	NextWatchFocus              []string                               `json:"next_watch_focus,omitempty"`
	CheckedNewsThreadVersionIDs []string                               `json:"checked_news_thread_version_ids,omitempty"`
	ImpactReviewCoverage        *PortfolioSentinelImpactReviewCoverage `json:"impact_review_coverage,omitempty"`
}

type PortfolioSentinelActionPlan struct {
	ID            string                           `json:"id"`
	PortfolioID   string                           `json:"portfolio_id"`
	Symbol        string                           `json:"symbol"`
	Market        string                           `json:"market,omitempty"`
	Name          string                           `json:"name,omitempty"`
	Action        string                           `json:"action"`
	TriggerMode   string                           `json:"trigger_mode"`
	TriggerPolicy string                           `json:"trigger_policy,omitempty"`
	Conditions    []PortfolioSentinelPlanCondition `json:"conditions,omitempty"`
	Sizing        *PortfolioSentinelPlanSizing     `json:"sizing,omitempty"`
	Reason        string                           `json:"reason"`
	RiskNotes     string                           `json:"risk_notes,omitempty"`
	Confidence    float64                          `json:"confidence,omitempty"`
	EvidenceRefs  []string                         `json:"evidence_refs,omitempty"`
	ResearchRefs  []string                         `json:"research_refs,omitempty"`
	MonitorWindow *PortfolioSentinelMonitorWindow  `json:"monitor_window,omitempty"`
	ValidUntil    time.Time                        `json:"valid_until,omitempty"`
}

type PortfolioSentinelMonitorWindow struct {
	Kind      string    `json:"kind"`
	StartsAt  time.Time `json:"starts_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PortfolioSentinelPlanCondition struct {
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	Threshold *float64 `json:"threshold,omitempty"`
	Low       float64  `json:"low,omitempty"`
	High      float64  `json:"high,omitempty"`
}

type PortfolioSentinelPlanSizing struct {
	Mode  string  `json:"mode"`
	Value float64 `json:"value"`
}

type PortfolioSentinelResearchRecord struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Query       string `json:"query,omitempty"`
	Source      string `json:"source"`
	SourceTitle string `json:"source_title,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	CheckedAt   string `json:"checked_at,omitempty"`
	Claim       string `json:"claim"`
}

type PortfolioSentinelImpactReviewCoverage struct {
	HoldingIDs     *[]string `json:"holding_ids"`
	MonitorIDs     *[]string `json:"monitor_ids"`
	AlertIDs       *[]string `json:"alert_ids"`
	OpportunityIDs *[]string `json:"opportunity_ids"`
	StrategyIDs    *[]string `json:"strategy_ids"`
}

func (c *PortfolioSentinelImpactReviewCoverage) hasAllExplicitFields() bool {
	return c != nil && c.HoldingIDs != nil && c.MonitorIDs != nil && c.AlertIDs != nil && c.OpportunityIDs != nil && c.StrategyIDs != nil
}

func (r *PortfolioSentinelReport) UnmarshalJSON(data []byte) error {
	type reportAlias PortfolioSentinelReport
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	// ponytail: only the historically free-form list fields are normalized.
	// Parse every other field through the real report type so compatibility
	// cannot silently discard action plans, research, or review coverage.
	strict := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		switch key {
		case "positive_items", "negative_items", "noise_items",
			"data_quality_notes", "next_watch_focus", "checked_news_thread_version_ids":
			continue
		default:
			strict[key] = value
		}
	}
	strictData, err := json.Marshal(strict)
	if err != nil {
		return err
	}
	var parsed reportAlias
	if err := json.Unmarshal(strictData, &parsed); err != nil {
		return err
	}
	parsed.PositiveItems = agentObjectListFromRaw(raw["positive_items"])
	parsed.NegativeItems = agentObjectListFromRaw(raw["negative_items"])
	parsed.NoiseItems = agentObjectListFromRaw(raw["noise_items"])
	parsed.DataQualityNotes = agentStringListFromRaw(raw["data_quality_notes"])
	parsed.NextWatchFocus = agentStringListFromRaw(raw["next_watch_focus"])
	parsed.CheckedNewsThreadVersionIDs = agentStringListFromRaw(raw["checked_news_thread_version_ids"])
	*r = PortfolioSentinelReport(parsed)
	return nil
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

func (h *PortfolioSentinelAffectedHolding) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	type holdingAlias PortfolioSentinelAffectedHolding
	var parsed holdingAlias
	var relaxed struct {
		Symbol    string `json:"symbol"`
		Market    string `json:"market,omitempty"`
		Name      string `json:"name,omitempty"`
		RiskLevel string `json:"risk_level,omitempty"`
		Direction string `json:"direction,omitempty"`
	}
	if err := json.Unmarshal(data, &relaxed); err != nil {
		return err
	}
	parsed = holdingAlias{
		Symbol:    relaxed.Symbol,
		Market:    relaxed.Market,
		Name:      relaxed.Name,
		RiskLevel: relaxed.RiskLevel,
		Direction: relaxed.Direction,
	}
	parsed.Reasons = agentStringListFromRaw(raw["reasons"])
	parsed.EvidenceRefs = agentStringListFromRaw(raw["evidence_refs"])
	*h = PortfolioSentinelAffectedHolding(parsed)
	return nil
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

func (r *PortfolioSentinelReviewRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	type requestAlias PortfolioSentinelReviewRequest
	var parsed requestAlias
	var relaxed struct {
		Symbol      string `json:"symbol"`
		Market      string `json:"market,omitempty"`
		PortfolioID string `json:"portfolio_id,omitempty"`
		Title       string `json:"title,omitempty"`
		Summary     string `json:"summary,omitempty"`
		RiskLevel   string `json:"risk_level,omitempty"`
	}
	if err := json.Unmarshal(data, &relaxed); err != nil {
		return err
	}
	parsed = requestAlias{
		Symbol:      relaxed.Symbol,
		Market:      relaxed.Market,
		PortfolioID: relaxed.PortfolioID,
		Title:       relaxed.Title,
		Summary:     relaxed.Summary,
		RiskLevel:   relaxed.RiskLevel,
	}
	parsed.EvidenceRefs = agentStringListFromRaw(raw["evidence_refs"])
	*r = PortfolioSentinelReviewRequest(parsed)
	return nil
}
