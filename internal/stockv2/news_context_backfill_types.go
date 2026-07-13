package stockv2

import (
	"errors"
	"time"
)

const (
	NewsContextTriggerBackfill = "backfill"

	NewsContextBackfillStatusRunning   = "running"
	NewsContextBackfillStatusPaused    = "paused"
	NewsContextBackfillStatusFailed    = "failed"
	NewsContextBackfillStatusCompleted = "completed"

	NewsEventContextClaimed = "claimed"
)

var (
	ErrNewsContextBackfillNotFound       = errors.New("news context backfill not found")
	ErrNewsContextBackfillAlreadyRunning = errors.New("news context backfill already running")
	ErrNewsContextBackfillState          = errors.New("invalid news context backfill state")
)

// NewsContextBackfillPreview is computed from source news every time it is
// requested, so the owner sees late-arriving historical news before starting.
type NewsContextBackfillPreview struct {
	TotalNewsCount      int       `json:"totalNewsCount"`
	PendingNewsCount    int       `json:"pendingNewsCount"`
	EarliestNewsAt      time.Time `json:"earliestNewsAt,omitempty"`
	LatestNewsAt        time.Time `json:"latestNewsAt,omitempty"`
	EstimatedChunkCount int       `json:"estimatedChunkCount"`
	PrerequisitesReady  bool      `json:"prerequisitesReady"`
	BlockingReasons     []string  `json:"blockingReasons,omitempty"`
}

type NewsContextBackfill struct {
	ID                  string    `json:"id"`
	Status              string    `json:"status"`
	Phase               string    `json:"phase"`
	OwnerRevision       int64     `json:"-"`
	RangeStartAt        time.Time `json:"rangeStartAt,omitempty"`
	CutoffAt            time.Time `json:"cutoffAt"`
	TotalNewsCount      int       `json:"totalNewsCount"`
	ProcessedNewsCount  int       `json:"processedNewsCount"`
	RemainingNewsCount  int       `json:"remainingNewsCount"`
	MissingNewsCount    int       `json:"missingNewsCount"`
	CompletedChunkCount int       `json:"completedChunkCount"`
	CurrentWindowStart  time.Time `json:"currentWindowStart,omitempty"`
	CurrentWindowEnd    time.Time `json:"currentWindowEnd,omitempty"`
	CurrentRunID        string    `json:"currentRunId,omitempty"`
	FinalReviewRunID    string    `json:"finalReviewRunId,omitempty"`
	DailyOutputCount    int       `json:"historicalDailyOutputVersionCount"`
	ReviewLinkedCount   int       `json:"finalReviewLinkedVersionCount"`
	ReviewMissingCount  int       `json:"finalReviewMissingVersionCount"`
	ErrorMessage        string    `json:"errorMessage,omitempty"`
	RequestedBy         string    `json:"requestedBy,omitempty"`
	StartedAt           time.Time `json:"startedAt,omitempty"`
	UpdatedAt           time.Time `json:"updatedAt"`
	CompletedAt         time.Time `json:"completedAt,omitempty"`
}

type RequestStartNewsContextBackfill struct {
	RequestedBy string `json:"requestedBy,omitempty"`
}
