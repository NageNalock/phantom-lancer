package stockv2

import (
	"errors"
	"time"
)

var (
	ErrStockProfileNotFound           = errors.New("stock profile not found")
	ErrInvalidStockProfileEnhancement = errors.New("invalid stock profile enhancement")
)

const (
	StockProfileAIStatusMissing       = "missing"
	StockProfileAIStatusQueued        = "queued"
	StockProfileAIStatusRunning       = "running"
	StockProfileAIStatusReady         = "ready"
	StockProfileAIStatusFailed        = "failed"
	StockProfileAIStatusNotConfigured = "not_configured"
)

const (
	StockProfileUpdateTriggerManual = "manual"
	StockProfileUpdateTriggerAuto   = "auto"

	StockProfileUpdateStatusQueued    = "queued"
	StockProfileUpdateStatusRunning   = "running"
	StockProfileUpdateStatusCompleted = "completed"
	StockProfileUpdateStatusPartial   = "partial"
	StockProfileUpdateStatusFailed    = "failed"

	StockProfileUpdateBaseStatusReady  = "ready"
	StockProfileUpdateBaseStatusFailed = "failed"

	StockProfileUpdateAIStatusQueued  = "queued"
	StockProfileUpdateAIStatusRunning = "running"

	StockProfileAIDecisionCalled               = "called"
	StockProfileAIDecisionSkippedUnchanged     = "skipped_unchanged"
	StockProfileAIDecisionSkippedNotConfigured = "skipped_not_configured"
	StockProfileAIDecisionSkippedUnavailable   = "skipped_unavailable"
	StockProfileAIDecisionFailed               = "failed"

	StockProfileSourceStatusSuccess = "success"
	StockProfileSourceStatusFailed  = "failed"
	StockProfileSourceStatusSkipped = "skipped"
)

const (
	StockProfileAIQueueStatusReady     = "ready"
	StockProfileAIQueueStatusRunning   = "running"
	StockProfileAIQueueStatusRetryWait = "retry_wait"
	StockProfileAIQueueStatusCompleted = "completed"
	StockProfileAIQueueStatusFailed    = "failed"
)

// StockProfile 是消息面高召回关联使用的静态文本资产。
// 它只来自标的主数据/基金元数据,不包含组合、成本、仓位和风险偏好等动态上下文。
type StockProfile struct {
	Symbol               string    `json:"symbol"`
	Market               string    `json:"market"`
	InstrumentType       string    `json:"instrumentType"`
	Name                 string    `json:"name"`
	Aliases              []string  `json:"aliases"`
	Industry             string    `json:"industry,omitempty"`
	Sectors              []string  `json:"sectors"`
	Concepts             []string  `json:"concepts"`
	Tags                 []string  `json:"tags"`
	BusinessSummary      string    `json:"businessSummary,omitempty"`
	ProfileText          string    `json:"profileText"`
	AliasesZh            []string  `json:"aliasesZh,omitempty"`
	AliasesEn            []string  `json:"aliasesEn,omitempty"`
	KeywordsZh           []string  `json:"keywordsZh,omitempty"`
	KeywordsEn           []string  `json:"keywordsEn,omitempty"`
	BusinessSummaryZh    string    `json:"businessSummaryZh,omitempty"`
	BusinessSummaryEn    string    `json:"businessSummaryEn,omitempty"`
	BusinessLinesZh      []string  `json:"businessLinesZh,omitempty"`
	BusinessLinesEn      []string  `json:"businessLinesEn,omitempty"`
	RiskTagsZh           []string  `json:"riskTagsZh,omitempty"`
	RiskTagsEn           []string  `json:"riskTagsEn,omitempty"`
	ProfileTextZh        string    `json:"profileTextZh,omitempty"`
	ProfileTextEn        string    `json:"profileTextEn,omitempty"`
	AIProfileStatus      string    `json:"aiProfileStatus,omitempty"`
	AIProfileModel       string    `json:"aiProfileModel,omitempty"`
	AIProfileConfidence  float64   `json:"aiProfileConfidence,omitempty"`
	AIProfileError       string    `json:"aiProfileError,omitempty"`
	AIProfileUpdatedAt   time.Time `json:"aiProfileUpdatedAt,omitempty"`
	AIProfileAttemptedAt time.Time `json:"aiProfileAttemptedAt,omitempty"`
	FundType             string    `json:"fundType,omitempty"`
	TrackingIndex        string    `json:"trackingIndex,omitempty"`
	Theme                string    `json:"theme,omitempty"`
	ConstituentHint      string    `json:"constituentHint,omitempty"`
	BaseProfileHash      string    `json:"baseProfileHash,omitempty"`
	BaseProfileUpdatedAt time.Time `json:"baseProfileUpdatedAt,omitempty"`
	BaseProfileCheckedAt time.Time `json:"baseProfileCheckedAt,omitempty"`
	ProfileVersion       int       `json:"profileVersion"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type StockProfileListFilter struct {
	Market         string
	InstrumentType string
	Keyword        string
	Limit          int
	Offset         int
}

type StockProfileSummary struct {
	Symbol               string    `json:"symbol"`
	Market               string    `json:"market,omitempty"`
	InstrumentType       string    `json:"instrumentType,omitempty"`
	Status               string    `json:"status"`
	BusinessSummary      string    `json:"businessSummary,omitempty"`
	BaseProfileUpdatedAt time.Time `json:"baseProfileUpdatedAt,omitempty"`
	BaseProfileCheckedAt time.Time `json:"baseProfileCheckedAt,omitempty"`
	AIProfileStatus      string    `json:"aiProfileStatus,omitempty"`
	AIProfileModel       string    `json:"aiProfileModel,omitempty"`
	AIProfileConfidence  float64   `json:"aiProfileConfidence,omitempty"`
	AIProfileUpdatedAt   time.Time `json:"aiProfileUpdatedAt,omitempty"`
	UpdatedAt            time.Time `json:"updatedAt,omitempty"`
}

type StockProfileSourceStatus struct {
	Source    string    `json:"source"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	FetchedAt time.Time `json:"fetchedAt,omitempty"`
}

type StockProfileUpdateTask struct {
	ID                  string                     `json:"id"`
	Symbol              string                     `json:"symbol"`
	Market              string                     `json:"market,omitempty"`
	TriggerSource       string                     `json:"triggerSource"`
	TriggerReason       string                     `json:"triggerReason,omitempty"`
	Status              string                     `json:"status"`
	BaseInputHashBefore string                     `json:"baseInputHashBefore,omitempty"`
	BaseInputHashAfter  string                     `json:"baseInputHashAfter,omitempty"`
	BaseInputChanged    bool                       `json:"baseInputChanged"`
	BaseProfileStatus   string                     `json:"baseProfileStatus,omitempty"`
	AIDecision          string                     `json:"aiDecision"`
	AgentRunID          string                     `json:"agentRunId,omitempty"`
	AIProfileStatus     string                     `json:"aiProfileStatus,omitempty"`
	AIProfileError      string                     `json:"aiProfileError,omitempty"`
	SourceStatuses      []StockProfileSourceStatus `json:"sourceStatuses,omitempty"`
	ErrorMessage        string                     `json:"errorMessage,omitempty"`
	StartedAt           time.Time                  `json:"startedAt"`
	FinishedAt          time.Time                  `json:"finishedAt,omitempty"`
	CreatedAt           time.Time                  `json:"createdAt"`
	UpdatedAt           time.Time                  `json:"updatedAt"`
}

type StockProfileUpdateTaskListFilter struct {
	Symbol string
	Limit  int
	Offset int
}

// StockProfileAIQueueItem is the durable, coalescing work record for one symbol.
// A single row per symbol prevents duplicate Agent executions while allowing a
// newer input version to supersede work that is already running.
type StockProfileAIQueueItem struct {
	Symbol                string    `json:"symbol"`
	Market                string    `json:"market,omitempty"`
	Status                string    `json:"status"`
	Priority              int       `json:"priority"`
	TriggerReason         string    `json:"triggerReason,omitempty"`
	RequestedBy           string    `json:"requestedBy,omitempty"`
	DesiredInputVersion   string    `json:"desiredInputVersion"`
	ClaimedInputVersion   string    `json:"claimedInputVersion,omitempty"`
	CompletedInputVersion string    `json:"completedInputVersion,omitempty"`
	PayloadJSON           string    `json:"-"`
	CurrentAgentRunID     string    `json:"currentAgentRunId,omitempty"`
	AttemptCount          int       `json:"attemptCount"`
	AvailableAt           time.Time `json:"availableAt"`
	LeaseOwner            string    `json:"leaseOwner,omitempty"`
	LeaseToken            string    `json:"-"`
	LeaseExpiresAt        time.Time `json:"leaseExpiresAt,omitempty"`
	CompletedAt           time.Time `json:"completedAt,omitempty"`
	LastError             string    `json:"lastError,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

type StockProfileAIQueueLease struct {
	StockProfileAIQueueItem
}

type StockProfileAIQueueStats struct {
	Ready     int `json:"ready"`
	Running   int `json:"running"`
	Retrying  int `json:"retrying"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}
