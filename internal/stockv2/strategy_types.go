package stockv2

import (
	"errors"
	"time"
)

const (
	StrategyKindSymbolStrategy   = "symbol_strategy"
	StrategyKindPortfolioMonitor = "portfolio_monitor"

	StrategyScopeResearch       = "research"
	StrategyScopePortfolioBound = "portfolio_bound"

	StrategySourceManual         = "manual"
	StrategySourceSystemTemplate = "system_template"
	StrategySourceAgent          = "agent"

	StrategyStatusDraft    = "draft"
	StrategyStatusActive   = "active"
	StrategyStatusPaused   = "paused"
	StrategyStatusArchived = "archived"

	StrategyDirectionWatch      = "watch"
	StrategyDirectionBuySignal  = "buy_signal"
	StrategyDirectionSellSignal = "sell_signal"
	StrategyDirectionHold       = "hold"
)

var (
	ErrStrategyNotFound         = errors.New("strategy not found")
	ErrStrategyVersionNotFound  = errors.New("strategy version not found")
	ErrStrategyArchived         = errors.New("strategy is archived")
	ErrInvalidStrategyName      = errors.New("invalid strategy name")
	ErrInvalidStrategySymbol    = errors.New("invalid strategy symbol")
	ErrInvalidStrategyKind      = errors.New("invalid strategy kind")
	ErrInvalidStrategyScope     = errors.New("invalid strategy scope")
	ErrInvalidStrategySource    = errors.New("invalid strategy source")
	ErrInvalidStrategyStatus    = errors.New("invalid strategy status")
	ErrInvalidStrategyDirection = errors.New("invalid strategy direction")
)

type StockV2Strategy struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	Scope           string    `json:"scope"`
	Source          string    `json:"source"`
	Status          string    `json:"status"`
	Symbol          string    `json:"symbol,omitempty"`
	Market          string    `json:"market,omitempty"`
	PortfolioID     string    `json:"portfolioId,omitempty"`
	ActiveVersionID string    `json:"activeVersionId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	ArchivedAt      time.Time `json:"archivedAt,omitempty"`
}

type StockV2StrategyVersion struct {
	ID              string         `json:"id"`
	StrategyID      string         `json:"strategyId"`
	VersionNo       int            `json:"versionNo"`
	Title           string         `json:"title,omitempty"`
	Direction       string         `json:"direction,omitempty"`
	Thesis          string         `json:"thesis,omitempty"`
	EntryConditions []string       `json:"entryConditions,omitempty"`
	ExitConditions  []string       `json:"exitConditions,omitempty"`
	RiskNotes       string         `json:"riskNotes,omitempty"`
	EvidenceRefs    []string       `json:"evidenceRefs,omitempty"`
	GenerationMeta  map[string]any `json:"generationMeta,omitempty"`
	CreatedBy       string         `json:"createdBy,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
}

type StrategyWithVersion struct {
	Strategy      StockV2Strategy         `json:"strategy"`
	ActiveVersion *StockV2StrategyVersion `json:"activeVersion,omitempty"`
}

type RequestCreateStrategy struct {
	Name            string         `json:"name"`
	Kind            string         `json:"kind"`
	Scope           string         `json:"scope"`
	Source          string         `json:"source"`
	Status          string         `json:"status"`
	Symbol          string         `json:"symbol,omitempty"`
	Market          string         `json:"market,omitempty"`
	PortfolioID     string         `json:"portfolioId,omitempty"`
	Title           string         `json:"title,omitempty"`
	Direction       string         `json:"direction,omitempty"`
	Thesis          string         `json:"thesis,omitempty"`
	EntryConditions []string       `json:"entryConditions,omitempty"`
	ExitConditions  []string       `json:"exitConditions,omitempty"`
	RiskNotes       string         `json:"riskNotes,omitempty"`
	EvidenceRefs    []string       `json:"evidenceRefs,omitempty"`
	GenerationMeta  map[string]any `json:"generationMeta,omitempty"`
	CreatedBy       string         `json:"createdBy,omitempty"`
}

type RequestUpdateStrategy struct {
	Name            *string         `json:"name,omitempty"`
	Scope           *string         `json:"scope,omitempty"`
	Symbol          *string         `json:"symbol,omitempty"`
	Market          *string         `json:"market,omitempty"`
	PortfolioID     *string         `json:"portfolioId,omitempty"`
	Title           *string         `json:"title,omitempty"`
	Direction       *string         `json:"direction,omitempty"`
	Thesis          *string         `json:"thesis,omitempty"`
	EntryConditions *[]string       `json:"entryConditions,omitempty"`
	ExitConditions  *[]string       `json:"exitConditions,omitempty"`
	RiskNotes       *string         `json:"riskNotes,omitempty"`
	EvidenceRefs    *[]string       `json:"evidenceRefs,omitempty"`
	GenerationMeta  *map[string]any `json:"generationMeta,omitempty"`
	CreatedBy       *string         `json:"createdBy,omitempty"`
}

type RequestCreatePortfolioMonitorStrategy struct {
	Name      string `json:"name,omitempty"`
	Title     string `json:"title,omitempty"`
	Thesis    string `json:"thesis,omitempty"`
	RiskNotes string `json:"riskNotes,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
}

type StrategyListFilter struct {
	Kind        string
	Scope       string
	Source      string
	Status      string
	Symbol      string
	PortfolioID string
	Limit       int
	Offset      int
}
