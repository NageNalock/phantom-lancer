package stockv2

import (
	"errors"
	"time"
)

const (
	WatchStatusActive   = "active"
	WatchStatusPaused   = "paused"
	WatchStatusArchived = "archived"

	WatchSourceManual           = "manual"
	WatchSourceStrategy         = "strategy"
	WatchSourcePortfolioMonitor = "portfolio_monitor"

	WatchTriggerPolicyAny = "any"
	WatchTriggerPolicyAll = "all"

	WatchScheduleManual        = "manual"
	WatchScheduleMarketSession = "market_session"
	WatchScheduleDaily         = "daily"

	AlertStatusOpen         = "open"
	AlertStatusAcknowledged = "acknowledged"
	AlertStatusIgnored      = "ignored"
	AlertStatusResolved     = "resolved"

	AlertLevelInfo     = "info"
	AlertLevelWarning  = "warning"
	AlertLevelCritical = "critical"
)

var (
	ErrWatchNotFound        = errors.New("watch not found")
	ErrAlertNotFound        = errors.New("alert not found")
	ErrWatchArchived        = errors.New("watch is archived")
	ErrWatchNotActive       = errors.New("watch is not active")
	ErrInvalidWatchName     = errors.New("invalid watch name")
	ErrInvalidWatchTarget   = errors.New("invalid watch target")
	ErrInvalidWatchStatus   = errors.New("invalid watch status")
	ErrInvalidWatchSource   = errors.New("invalid watch source")
	ErrInvalidWatchPolicy   = errors.New("invalid watch trigger policy")
	ErrInvalidWatchSchedule = errors.New("invalid watch schedule kind")
	ErrInvalidWatchCooldown = errors.New("invalid watch cooldown")
	ErrInvalidAlertTitle    = errors.New("invalid alert title")
	ErrInvalidAlertStatus   = errors.New("invalid alert status")
	ErrInvalidAlertLevel    = errors.New("invalid alert level")
)

type StockV2Watch struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Status            string         `json:"status"`
	Source            string         `json:"source"`
	Symbol            string         `json:"symbol,omitempty"`
	Market            string         `json:"market,omitempty"`
	PortfolioID       string         `json:"portfolioId,omitempty"`
	StrategyID        string         `json:"strategyId,omitempty"`
	StrategyVersionID string         `json:"strategyVersionId,omitempty"`
	TriggerPolicy     string         `json:"triggerPolicy"`
	TriggerConfig     map[string]any `json:"triggerConfig"`
	ScheduleKind      string         `json:"scheduleKind"`
	CooldownSeconds   int            `json:"cooldownSeconds"`
	LastCheckedAt     time.Time      `json:"lastCheckedAt,omitempty"`
	LastTriggeredAt   time.Time      `json:"lastTriggeredAt,omitempty"`
	LastRunStatus     string         `json:"lastRunStatus,omitempty"`
	LastRunReason     string         `json:"lastRunReason,omitempty"`
	ArchivedAt        time.Time      `json:"archivedAt,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}

type RequestCreateWatch struct {
	Name              string         `json:"name"`
	Status            string         `json:"status,omitempty"`
	Source            string         `json:"source,omitempty"`
	Symbol            string         `json:"symbol,omitempty"`
	Market            string         `json:"market,omitempty"`
	PortfolioID       string         `json:"portfolioId,omitempty"`
	StrategyID        string         `json:"strategyId,omitempty"`
	StrategyVersionID string         `json:"strategyVersionId,omitempty"`
	TriggerPolicy     string         `json:"triggerPolicy,omitempty"`
	TriggerConfig     map[string]any `json:"triggerConfig,omitempty"`
	ScheduleKind      string         `json:"scheduleKind,omitempty"`
	CooldownSeconds   int            `json:"cooldownSeconds,omitempty"`
}

type RequestUpdateWatch struct {
	Name              *string         `json:"name,omitempty"`
	Source            *string         `json:"source,omitempty"`
	Symbol            *string         `json:"symbol,omitempty"`
	Market            *string         `json:"market,omitempty"`
	PortfolioID       *string         `json:"portfolioId,omitempty"`
	StrategyID        *string         `json:"strategyId,omitempty"`
	StrategyVersionID *string         `json:"strategyVersionId,omitempty"`
	TriggerPolicy     *string         `json:"triggerPolicy,omitempty"`
	TriggerConfig     *map[string]any `json:"triggerConfig,omitempty"`
	ScheduleKind      *string         `json:"scheduleKind,omitempty"`
	CooldownSeconds   *int            `json:"cooldownSeconds,omitempty"`
}

type WatchListFilter struct {
	Status      string
	PortfolioID string
	StrategyID  string
	Symbol      string
	Limit       int
	Offset      int
}

type StockV2Alert struct {
	ID             string         `json:"id"`
	WatchID        string         `json:"watchId"`
	Status         string         `json:"status"`
	Level          string         `json:"level"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary,omitempty"`
	DedupeKey      string         `json:"dedupeKey,omitempty"`
	Evidence       map[string]any `json:"evidence,omitempty"`
	TriggeredAt    time.Time      `json:"triggeredAt"`
	AcknowledgedAt time.Time      `json:"acknowledgedAt,omitempty"`
	ResolvedAt     time.Time      `json:"resolvedAt,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

type RequestCreateAlert struct {
	WatchID     string         `json:"watchId"`
	Level       string         `json:"level,omitempty"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary,omitempty"`
	DedupeKey   string         `json:"dedupeKey,omitempty"`
	Evidence    map[string]any `json:"evidence,omitempty"`
	TriggeredAt time.Time      `json:"triggeredAt,omitempty"`
}

type AlertListFilter struct {
	Status  string
	WatchID string
	Limit   int
	Offset  int
}
