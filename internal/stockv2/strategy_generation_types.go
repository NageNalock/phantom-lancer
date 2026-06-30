package stockv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StrategyGenerationModeManualTarget     = "manual_target"
	StrategyGenerationModeOpportunity      = "opportunity"
	StrategyGenerationModeSingleInstrument = "single_instrument"
	StrategyGenerationModePortfolio        = "portfolio_strategy_diagnosis"

	StrategyGenerationInputSchemaVersion  = "strategy-generation-input/v1"
	StrategyGenerationReportSchemaVersion = "strategy-generation-report/v1"
	StrategyGenerationOutputType          = AgentTaskTypeStrategyGeneration

	StrategyGenerationDraftTypeNewStrategy   = "new_strategy"
	StrategyGenerationDraftTypeStrategyPatch = "strategy_patch"
	StrategyGenerationDraftTypeNoChange      = "no_change"

	StrategyGenerationDraftNewStrategy = StrategyGenerationDraftTypeNewStrategy
	StrategyGenerationDraftPatch       = StrategyGenerationDraftTypeStrategyPatch
	StrategyGenerationDraftNoChange    = StrategyGenerationDraftTypeNoChange

	StrategyGenerationRuleActionObserve       = "observe"
	StrategyGenerationRuleActionBuildPosition = "build_position"
	StrategyGenerationRuleActionAddPosition   = "add_position"
	StrategyGenerationRuleActionHold          = "hold"
	StrategyGenerationRuleActionReduce        = "reduce_position"
	StrategyGenerationRuleActionExit          = "exit_position"

	StrategyGenerationActionObserve       = StrategyGenerationRuleActionObserve
	StrategyGenerationActionBuildPosition = StrategyGenerationRuleActionBuildPosition
	StrategyGenerationActionAddPosition   = StrategyGenerationRuleActionAddPosition
	StrategyGenerationActionHold          = StrategyGenerationRuleActionHold
	StrategyGenerationActionReduce        = StrategyGenerationRuleActionReduce
	StrategyGenerationActionExit          = StrategyGenerationRuleActionExit
)

var (
	ErrInvalidStrategyGenerationInput      = errors.New("invalid strategy generation input")
	ErrInvalidStrategyGenerationResult     = errors.New("invalid strategy generation result")
	ErrStrategyGenerationPortfolioRequired = errors.New("portfolioId is required for portfolio_strategy_diagnosis")
)

type StrategyGenerationInput struct {
	SchemaVersion     string                               `json:"schemaVersion,omitempty"`
	Mode              string                               `json:"mode"`
	UserGoal          string                               `json:"userGoal,omitempty"`
	UserIntent        string                               `json:"userIntent,omitempty"`
	PortfolioID       string                               `json:"portfolioId,omitempty"`
	TargetInstruments []StrategyGenerationTargetInstrument `json:"targetInstruments,omitempty"`
	RequestedBy       string                               `json:"requestedBy,omitempty"`
	OpportunityID     string                               `json:"opportunityId,omitempty"`
	CandidateID       string                               `json:"candidateId,omitempty"`
	TimeHorizon       string                               `json:"timeHorizon,omitempty"`
	AllowedActions    []string                             `json:"allowedActions,omitempty"`
	EvidenceScope     map[string]bool                      `json:"evidenceScope,omitempty"`
}

type StrategyGenerationTargetInstrument struct {
	Symbol   string `json:"symbol"`
	Market   string `json:"market,omitempty"`
	Name     string `json:"name,omitempty"`
	UserNote string `json:"userNote,omitempty"`
}

type StrategyGenerationContext struct {
	BuiltAt              time.Time                             `json:"builtAt"`
	Input                StrategyGenerationInput               `json:"input"`
	Mode                 string                                `json:"mode,omitempty"`
	Targets              []StrategyGenerationInstrumentContext `json:"targets"`
	FreshnessSummary     map[string]any                        `json:"freshnessSummary,omitempty"`
	Portfolio            *PortfolioReviewContext               `json:"portfolio,omitempty"`
	Diagnostics          *StrategyGenerationPortfolioDiagnosis `json:"diagnostics,omitempty"`
	Holdings             []StrategyGenerationHoldingContext    `json:"holdings,omitempty"`
	RecentReviews        []OperationReview                     `json:"recentReviews,omitempty"`
	Transactions         []StockV2Transaction                  `json:"transactions,omitempty"`
	MissingItems         []string                              `json:"missingItems,omitempty"`
	Opportunity          *Opportunity                          `json:"opportunity,omitempty"`
	OpportunityCandidate *OpportunityCandidate                 `json:"opportunityCandidate,omitempty"`
	OpportunityEvidence  []OpportunityEvidence                 `json:"opportunityEvidence,omitempty"`
	EmbeddingStatus      *EmbeddingStatus                      `json:"embeddingStatus,omitempty"`
}

type StrategyGenerationInstrumentContext struct {
	Instrument         *StockV2Instrument             `json:"instrument,omitempty"`
	LatestQuote        *StockV2QuoteLatest            `json:"latestQuote,omitempty"`
	DailyBars          *StrategyGenerationBarsSummary `json:"dailyBars,omitempty"`
	Profile            *StockProfile                  `json:"profile,omitempty"`
	ExistingStrategies []StrategyWithVersion          `json:"existingStrategies,omitempty"`
	DataFreshness      map[string]any                 `json:"dataFreshness,omitempty"`
}

type StrategyGenerationBarsSummary struct {
	Adjusted  string `json:"adjusted"`
	RowCount  int    `json:"rowCount"`
	Earliest  string `json:"earliest,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Source    string `json:"source,omitempty"`
	LastError string `json:"lastError,omitempty"`
	HasData   bool   `json:"hasData"`
}

type StrategyGenerationReport struct {
	SchemaVersion string                       `json:"schema_version"`
	RunSummary    StrategyGenerationRunSummary `json:"run_summary"`
	Drafts        []StrategyGenerationDraft    `json:"drafts"`
}

type StrategyGenerationRunSummary struct {
	Mode              string   `json:"mode"`
	OverallConclusion string   `json:"overall_conclusion,omitempty"`
	KeyConflicts      []string `json:"key_conflicts,omitempty"`
	DataQualityNotes  []string `json:"data_quality_notes,omitempty"`
}

type StrategyGenerationDraft struct {
	Symbol                   string                                     `json:"symbol"`
	Market                   string                                     `json:"market,omitempty"`
	Name                     string                                     `json:"name,omitempty"`
	DraftType                string                                     `json:"draft_type"`
	StrategyBias             string                                     `json:"strategy_bias,omitempty"`
	Thesis                   string                                     `json:"thesis,omitempty"`
	Confidence               float64                                    `json:"confidence,omitempty"`
	EvidenceSummary          []string                                   `json:"evidence_summary,omitempty"`
	RiskSummary              []string                                   `json:"risk_summary,omitempty"`
	InvalidConditions        []string                                   `json:"invalid_conditions,omitempty"`
	Playbook                 StrategyGenerationPlaybook                 `json:"playbook"`
	PortfolioAwareSuggestion StrategyGenerationPortfolioAwareSuggestion `json:"portfolio_aware_suggestion,omitempty"`
}

type StrategyGenerationPlaybook struct {
	Version string                           `json:"version,omitempty"`
	Rules   []StrategyGenerationPlaybookRule `json:"rules"`
}

type StrategyGenerationPlaybookRule struct {
	ID                  string           `json:"id"`
	Action              string           `json:"action"`
	Title               string           `json:"title,omitempty"`
	Trigger             string           `json:"trigger,omitempty"`
	Preconditions       string           `json:"preconditions,omitempty"`
	Target              string           `json:"target,omitempty"`
	Risk                string           `json:"risk,omitempty"`
	DataPrefilters      []map[string]any `json:"dataPrefilters,omitempty"`
	PortfolioPrefilters []map[string]any `json:"portfolioPrefilters,omitempty"`
	NewsPrefilters      []map[string]any `json:"newsPrefilters,omitempty"`
	Priority            int              `json:"priority,omitempty"`
}

type StrategyGenerationPortfolioAwareSuggestion struct {
	TradeSignal        string `json:"trade_signal,omitempty"`
	TargetPositionHint string `json:"target_position_hint,omitempty"`
	ReviewRequest      string `json:"review_request,omitempty"`
}

func (s *StrategyGenerationPortfolioAwareSuggestion) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = StrategyGenerationPortfolioAwareSuggestion{
		TradeSignal:        strategyGenerationStringField(raw["trade_signal"]),
		TargetPositionHint: strategyGenerationStringField(raw["target_position_hint"]),
		ReviewRequest:      strategyGenerationReviewRequestField(raw["review_request"]),
	}
	return nil
}

func strategyGenerationStringField(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	return ""
}

func strategyGenerationReviewRequestField(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(strategyGenerationReviewRequestText(decoded))
}

func strategyGenerationReviewRequestText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := strategyGenerationReviewRequestText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		reason := strings.TrimSpace(stringFromAny(v["reason"]))
		priority := strings.TrimSpace(stringFromAny(v["priority"]))
		requestType := strings.TrimSpace(stringFromAny(v["type"]))
		prefixParts := make([]string, 0, 2)
		if priority != "" {
			prefixParts = append(prefixParts, priority)
		}
		if requestType != "" {
			prefixParts = append(prefixParts, requestType)
		}
		if reason != "" {
			if len(prefixParts) > 0 {
				return "[" + strings.Join(prefixParts, "/") + "] " + reason
			}
			return reason
		}
		if payload, err := json.Marshal(v); err == nil {
			return string(payload)
		}
		return ""
	case float64, bool:
		return fmt.Sprint(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

type StrategyGenerationPortfolioDiagnosis struct {
	HoldingCount           int      `json:"holdingCount"`
	Cash                   float64  `json:"cash"`
	TotalAssetValue        float64  `json:"totalAssetValue"`
	CashPct                float64  `json:"cashPct"`
	LargestPositionSymbol  string   `json:"largestPositionSymbol,omitempty"`
	LargestPositionPct     float64  `json:"largestPositionPct,omitempty"`
	MissingStrategySymbols []string `json:"missingStrategySymbols,omitempty"`
	PatchCandidateSymbols  []string `json:"patchCandidateSymbols,omitempty"`
}

type StrategyGenerationHoldingContext struct {
	Symbol            string                             `json:"symbol"`
	Market            string                             `json:"market,omitempty"`
	Name              string                             `json:"name,omitempty"`
	CostPrice         float64                            `json:"costPrice"`
	Quantity          float64                            `json:"quantity"`
	AvailableQuantity float64                            `json:"availableQuantity"`
	CurrentPrice      float64                            `json:"currentPrice"`
	MarketValue       float64                            `json:"marketValue"`
	PnL               float64                            `json:"pnl"`
	PositionPct       float64                            `json:"positionPct"`
	Holding           StockV2Holding                     `json:"holding"`
	Quote             *StockV2QuoteLatest                `json:"quote,omitempty"`
	DailyBars         *DailyBarsContext                  `json:"dailyBars,omitempty"`
	Profile           *StockProfile                      `json:"stockProfile,omitempty"`
	StrategyCoverage  StrategyGenerationStrategyCoverage `json:"strategyCoverage"`
	RecentReviews     []OperationReview                  `json:"recentReviews,omitempty"`
	Transactions      []StockV2Transaction               `json:"transactions,omitempty"`
	Freshness         map[string]any                     `json:"freshness,omitempty"`
}

type StrategyGenerationStrategyCoverage struct {
	HasStrategy      bool                                `json:"hasStrategy"`
	HasActive        bool                                `json:"hasActive"`
	HasDraft         bool                                `json:"hasDraft"`
	HasPaused        bool                                `json:"hasPaused"`
	NeedsNewStrategy bool                                `json:"needsNewStrategy"`
	PatchCandidate   bool                                `json:"patchCandidate"`
	Strategies       []StrategyGenerationStrategySummary `json:"strategies,omitempty"`
}

type StrategyGenerationStrategySummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Scope     string `json:"scope,omitempty"`
	Source    string `json:"source,omitempty"`
	VersionID string `json:"versionId,omitempty"`
	Title     string `json:"title,omitempty"`
	Direction string `json:"direction,omitempty"`
}

func validStrategyGenerationMode(mode string) bool {
	return mode == StrategyGenerationModeManualTarget ||
		mode == StrategyGenerationModeOpportunity ||
		mode == StrategyGenerationModeSingleInstrument ||
		mode == StrategyGenerationModePortfolio
}

func validStrategyGenerationDraftType(draftType string) bool {
	return draftType == StrategyGenerationDraftTypeNewStrategy ||
		draftType == StrategyGenerationDraftTypeStrategyPatch ||
		draftType == StrategyGenerationDraftTypeNoChange
}

func validStrategyGenerationRuleAction(action string) bool {
	switch action {
	case StrategyGenerationRuleActionObserve,
		StrategyGenerationRuleActionBuildPosition,
		StrategyGenerationRuleActionAddPosition,
		StrategyGenerationRuleActionHold,
		StrategyGenerationRuleActionReduce,
		StrategyGenerationRuleActionExit:
		return true
	default:
		return false
	}
}
