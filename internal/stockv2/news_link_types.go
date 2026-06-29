package stockv2

import (
	"errors"
	"time"
)

const (
	NewsEventLinkStatusPending     = "pending"
	NewsEventLinkStatusLinked      = "linked"
	NewsEventLinkStatusNoCandidate = "no_candidate"
	NewsEventLinkStatusFailed      = "failed"

	NewsLinkMatchExactSymbol     = "exact_symbol"
	NewsLinkMatchExactName       = "exact_name"
	NewsLinkMatchAlias           = "alias"
	NewsLinkMatchKeyword         = "keyword"
	NewsLinkMatchProfileKeyword  = "profile_keyword"
	NewsLinkMatchSemanticProfile = "semantic_profile"
	NewsLinkMatchBoosted         = "boosted"

	NewsLinkMonitorStatusPending = "pending"
	NewsLinkMonitorStatusHit     = "hit"
	NewsLinkMonitorStatusSkipped = "skipped"
	NewsLinkMonitorStatusFailed  = "failed"
)

var (
	ErrNewsEventNotFound = errors.New("news event not found")
)

// NewsEvent 是标准化后的消息事件。它是 RawNews 之后的内部对象,候选关联前仍不代表
// 与任何股票存在事实关系。
type NewsEvent struct {
	ID              string    `json:"id"`
	RawNewsID       string    `json:"rawNewsId,omitempty"`
	Source          string    `json:"source"`
	ExternalID      string    `json:"externalId,omitempty"`
	Title           string    `json:"title"`
	Summary         string    `json:"summary,omitempty"`
	Content         string    `json:"content,omitempty"`
	URL             string    `json:"url,omitempty"`
	QualityStatus   string    `json:"qualityStatus,omitempty"`
	DedupeKey       string    `json:"dedupeKey,omitempty"`
	LinkStatus      string    `json:"linkStatus"`
	EventAt         time.Time `json:"eventAt"`
	LinkProcessedAt time.Time `json:"linkProcessedAt,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type NewsLinkCandidate struct {
	ID              string    `json:"id"`
	NewsEventID     string    `json:"newsEventId"`
	RawNewsID       string    `json:"rawNewsId,omitempty"`
	NewsEventTitle  string    `json:"newsEventTitle,omitempty"`
	NewsEventSource string    `json:"newsEventSource,omitempty"`
	NewsEventAt     time.Time `json:"newsEventAt,omitempty"`
	Symbol          string    `json:"symbol"`
	Market          string    `json:"market,omitempty"`
	InstrumentName  string    `json:"instrumentName,omitempty"`
	MatchMethod     string    `json:"matchMethod"`
	Score           float64   `json:"score"`
	Reason          string    `json:"reason"`
	MatchedTerms    []string  `json:"matchedTerms"`
	MonitorStatus   string    `json:"monitorStatus,omitempty"`
	MonitorHitID    string    `json:"monitorHitId,omitempty"`
	MonitoredAt     time.Time `json:"monitoredAt,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type NewsLinkCandidateListFilter struct {
	NewsEventID   string
	RawNewsID     string
	Source        string
	Symbol        string
	Market        string
	MatchMethod   string
	MonitorStatus string
	Query         string
	Limit         int
	Offset        int
}

type NewsEventListFilter struct {
	Source        string
	LinkStatus    string
	QualityStatus string
	Query         string
	Limit         int
	Offset        int
}

type LinkNewsEventsBatchResult struct {
	Total       int             `json:"total"`
	Linked      int             `json:"linked"`
	NoCandidate int             `json:"noCandidate"`
	Failed      int             `json:"failed"`
	Candidates  int             `json:"candidates"`
	FailedItems []UpdateFailure `json:"failedItems,omitempty"`
}
