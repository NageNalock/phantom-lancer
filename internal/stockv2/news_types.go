package stockv2

import (
	"errors"
	"time"
)

const (
	NewsSourceFinancialJuice = "financialjuice"
	NewsSourceAlphaVantage   = "alpha_vantage"
	NewsSourceFMP            = "fmp"

	NewsStatusNew       = "new"
	NewsStatusProcessed = "processed"
	NewsStatusFailed    = "failed"
	NewsStatusIgnored   = "ignored"

	NewsQualityUnknown = "unknown"
	NewsQualityOK      = "ok"
	NewsQualityLow     = "low"

	NewsImportanceLow    = "low"
	NewsImportanceNormal = "normal"
	NewsImportanceHigh   = "high"

	NewsLinkCandidateStatusCandidate = "candidate"
	NewsLinkCandidateStatusConfirmed = "confirmed"
	NewsLinkCandidateStatusRejected  = "rejected"
	NewsLinkCandidateStatusIgnored   = "ignored"
)

var (
	ErrRawNewsNotFound             = errors.New("raw news not found")
	ErrNewsEventNotFound           = errors.New("news event not found")
	ErrNewsLinkCandidateNotFound   = errors.New("news link candidate not found")
	ErrInvalidRawNewsSource        = errors.New("raw news source is required")
	ErrInvalidRawNewsContent       = errors.New("raw news title or content is required")
	ErrInvalidNewsEventTitle       = errors.New("news event title is required")
	ErrInvalidNewsEventSource      = errors.New("news event source is required")
	ErrInvalidNewsLinkCandidate    = errors.New("invalid news link candidate")
	ErrInvalidNewsLinkCandidateKey = errors.New("news link candidate requires event, symbol and match method")
	ErrNewsAdapterDisabled         = errors.New("news adapter disabled")
	ErrUnsupportedNewsSource       = errors.New("unsupported news source")
	ErrFinancialJuiceCookieMissing = errors.New("financialjuice cookie missing")
)

type StockV2RawNews struct {
	ID          string         `json:"id"`
	Source      string         `json:"source"`
	SourceID    string         `json:"sourceId,omitempty"`
	Language    string         `json:"language,omitempty"`
	Title       string         `json:"title"`
	Content     string         `json:"content,omitempty"`
	Snippet     string         `json:"snippet,omitempty"`
	PublishedAt time.Time      `json:"publishedAt,omitempty"`
	URL         string         `json:"url,omitempty"`
	FetchedAt   time.Time      `json:"fetchedAt"`
	RawPayload  map[string]any `json:"rawPayload,omitempty"`
	ContentHash string         `json:"contentHash"`
	DedupeKey   string         `json:"dedupeKey"`
	Quality     string         `json:"quality"`
	Status      string         `json:"status"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type StockV2NewsEvent struct {
	ID         string    `json:"id"`
	RawNewsID  string    `json:"rawNewsId,omitempty"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary,omitempty"`
	Snippet    string    `json:"snippet,omitempty"`
	Language   string    `json:"language,omitempty"`
	Source     string    `json:"source"`
	EventTime  time.Time `json:"eventTime"`
	Importance string    `json:"importance"`
	Tags       []string  `json:"tags,omitempty"`
	Topics     []string  `json:"topics,omitempty"`
	DedupeKey  string    `json:"dedupeKey"`
	Quality    string    `json:"quality"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type StockV2NewsLinkCandidate struct {
	ID           string    `json:"id"`
	NewsEventID  string    `json:"newsEventId"`
	Symbol       string    `json:"symbol"`
	Market       string    `json:"market,omitempty"`
	MatchMethod  string    `json:"matchMethod"`
	Score        float64   `json:"score"`
	Reason       string    `json:"reason,omitempty"`
	MatchedTerms []string  `json:"matchedTerms,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type RequestCreateRawNews struct {
	Source      string         `json:"source"`
	SourceID    string         `json:"sourceId,omitempty"`
	Language    string         `json:"language,omitempty"`
	Title       string         `json:"title"`
	Content     string         `json:"content,omitempty"`
	Snippet     string         `json:"snippet,omitempty"`
	PublishedAt time.Time      `json:"publishedAt,omitempty"`
	URL         string         `json:"url,omitempty"`
	FetchedAt   time.Time      `json:"fetchedAt,omitempty"`
	RawPayload  map[string]any `json:"rawPayload,omitempty"`
	DedupeKey   string         `json:"dedupeKey,omitempty"`
	Quality     string         `json:"quality,omitempty"`
	Status      string         `json:"status,omitempty"`
}

type RequestCreateNewsEvent struct {
	RawNewsID  string    `json:"rawNewsId,omitempty"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary,omitempty"`
	Snippet    string    `json:"snippet,omitempty"`
	Language   string    `json:"language,omitempty"`
	Source     string    `json:"source"`
	EventTime  time.Time `json:"eventTime,omitempty"`
	Importance string    `json:"importance,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Topics     []string  `json:"topics,omitempty"`
	DedupeKey  string    `json:"dedupeKey,omitempty"`
	Quality    string    `json:"quality,omitempty"`
	Status     string    `json:"status,omitempty"`
}

type RequestUpsertNewsLinkCandidate struct {
	NewsEventID  string   `json:"newsEventId"`
	Symbol       string   `json:"symbol"`
	Market       string   `json:"market,omitempty"`
	MatchMethod  string   `json:"matchMethod"`
	Score        float64  `json:"score"`
	Reason       string   `json:"reason,omitempty"`
	MatchedTerms []string `json:"matchedTerms,omitempty"`
	Status       string   `json:"status,omitempty"`
}

type RawNewsListFilter struct {
	Source   string
	Language string
	Status   string
	Quality  string
	Since    time.Time
	Until    time.Time
	Limit    int
	Offset   int
}

type NewsEventListFilter struct {
	Source   string
	Language string
	Status   string
	Quality  string
	Since    time.Time
	Until    time.Time
	Limit    int
	Offset   int
}

type NewsLinkCandidateListFilter struct {
	NewsEventID string
	Symbol      string
	Market      string
	MatchMethod string
	Status      string
	Limit       int
	Offset      int
}
