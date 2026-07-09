package stockv2

import (
	"errors"
	"time"
)

const (
	AlertStatusOpen         = "open"
	AlertStatusAcknowledged = "acknowledged"
	AlertStatusIgnored      = "ignored"
	AlertStatusResolved     = "resolved"

	AlertLevelInfo     = "info"
	AlertLevelWarning  = "warning"
	AlertLevelCritical = "critical"

	AlertTriggerSourceAgentConfirmed        = "agent_confirmed"
	AlertTriggerSourceManualReviewConfirmed = "manual_review_confirmed"
	AlertTriggerSourceDeterministic         = "deterministic"
	AlertTriggerSourceDegraded              = "degraded"
)

var (
	ErrAlertNotFound      = errors.New("alert not found")
	ErrInvalidAlertTitle  = errors.New("invalid alert title")
	ErrInvalidAlertStatus = errors.New("invalid alert status")
	ErrInvalidAlertLevel  = errors.New("invalid alert level")
)

type StockV2Alert struct {
	ID               string         `json:"id"`
	MonitorHitID     string         `json:"monitorHitId,omitempty"`
	MonitorRunID     string         `json:"monitorRunId,omitempty"`
	TaskType         string         `json:"taskType,omitempty"`
	StrategyID       string         `json:"strategyId,omitempty"`
	PortfolioID      string         `json:"portfolioId,omitempty"`
	Symbol           string         `json:"symbol,omitempty"`
	Market           string         `json:"market,omitempty"`
	ReviewID         string         `json:"reviewId,omitempty"`
	ReviewStatus     string         `json:"reviewStatus,omitempty"`
	AgentRunID       string         `json:"agentRunId,omitempty"`
	DecisionLedgerID string         `json:"decisionLedgerId,omitempty"`
	TriggerSource    string         `json:"triggerSource,omitempty"`
	Status           string         `json:"status"`
	Level            string         `json:"level"`
	Title            string         `json:"title"`
	Summary          string         `json:"summary,omitempty"`
	DedupeKey        string         `json:"dedupeKey,omitempty"`
	Evidence         map[string]any `json:"evidence,omitempty"`
	OccurrenceCount  int            `json:"occurrenceCount,omitempty"`
	FirstSeenAt      time.Time      `json:"firstSeenAt,omitempty"`
	LastSeenAt       time.Time      `json:"lastSeenAt,omitempty"`
	TriggeredAt      time.Time      `json:"triggeredAt"`
	AcknowledgedAt   time.Time      `json:"acknowledgedAt,omitempty"`
	ResolvedAt       time.Time      `json:"resolvedAt,omitempty"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

type AlertListFilter struct {
	Status       string
	MonitorHitID string
	ReviewID     string
	TaskType     string
	Symbol       string
	PortfolioID  string
	StrategyID   string
	Limit        int
	Offset       int
}
