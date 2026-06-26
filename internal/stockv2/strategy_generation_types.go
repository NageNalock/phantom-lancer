package stockv2

import (
	"errors"
	"time"
)

const (
	StrategyGenerationModeManualTarget     = "manual_target"
	StrategyGenerationModeSingleInstrument = "single_instrument"

	StrategyGenerationReportSchemaVersion = "strategy-generation-report/v1"

	StrategyGenerationDraftTypeNewStrategy   = "new_strategy"
	StrategyGenerationDraftTypeStrategyPatch = "strategy_patch"
	StrategyGenerationDraftTypeNoChange      = "no_change"

	StrategyGenerationRuleActionObserve       = "observe"
	StrategyGenerationRuleActionBuildPosition = "build_position"
	StrategyGenerationRuleActionAddPosition   = "add_position"
	StrategyGenerationRuleActionHold          = "hold"
	StrategyGenerationRuleActionReduce        = "reduce_position"
	StrategyGenerationRuleActionExit          = "exit_position"
)

var (
	ErrInvalidStrategyGenerationInput  = errors.New("invalid strategy generation input")
	ErrInvalidStrategyGenerationResult = errors.New("invalid strategy generation result")
)

type StrategyGenerationInput struct {
	SchemaVersion     string                               `json:"schemaVersion,omitempty"`
	Mode              string                               `json:"mode"`
	UserGoal          string                               `json:"userGoal,omitempty"`
	PortfolioID       string                               `json:"portfolioId,omitempty"`
	TargetInstruments []StrategyGenerationTargetInstrument `json:"targetInstruments,omitempty"`
	RequestedBy       string                               `json:"requestedBy,omitempty"`
}

type StrategyGenerationTargetInstrument struct {
	Symbol string `json:"symbol"`
	Market string `json:"market,omitempty"`
	Name   string `json:"name,omitempty"`
}

type StrategyGenerationContext struct {
	BuiltAt          time.Time                             `json:"builtAt"`
	Input            StrategyGenerationInput               `json:"input"`
	Targets          []StrategyGenerationInstrumentContext `json:"targets"`
	FreshnessSummary map[string]any                        `json:"freshnessSummary,omitempty"`
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

func validStrategyGenerationMode(mode string) bool {
	return mode == StrategyGenerationModeManualTarget || mode == StrategyGenerationModeSingleInstrument
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
