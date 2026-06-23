package stockv2

import (
	"context"
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

	NewsSourceStatusDisabled    = "disabled"
	NewsSourceStatusIdle        = "idle"
	NewsSourceStatusRunning     = "running"
	NewsSourceStatusBackoff     = "backoff"
	NewsSourceStatusFailed      = "failed"
	NewsSourceStatusRateLimited = "rate_limited"
)

var (
	ErrRawNewsNotFound             = errors.New("raw news not found")
	ErrNewsLinkCandidateNotFound   = errors.New("news link candidate not found")
	ErrInvalidRawNewsSource        = errors.New("raw news source is required")
	ErrInvalidRawNewsContent       = errors.New("raw news title or content is required")
	ErrInvalidNewsEventTitle       = errors.New("news event title is required")
	ErrInvalidNewsEventSource      = errors.New("news event source is required")
	ErrInvalidNewsLinkCandidate    = errors.New("invalid news link candidate")
	ErrInvalidNewsLinkCandidateKey = errors.New("news link candidate requires event, symbol and match method")
	ErrNewsAdapterDisabled         = errors.New("news adapter disabled")
	ErrUnsupportedNewsSource       = errors.New("unsupported news source")
	ErrJin10ConfigMissing          = errors.New("jin10 curl config missing")
	ErrFinancialJuiceCookieMissing = errors.New("financialjuice cookie missing")
	ErrNewsSourceAdapterNotFound   = errors.New("news source adapter not found")
)

type NewsSourceCursor struct {
	Cursor string    `json:"cursor,omitempty"`
	Since  time.Time `json:"since,omitempty"`
}

type NewsSourceFetchResult struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
	FetchedAt  time.Time        `json:"fetchedAt,omitempty"`
	Disabled   bool             `json:"disabled,omitempty"`
}

type NewsSourceAdapter interface {
	SourceName() string
	FetchSince(ctx context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error)
	NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error)
}

type NewsEventLinker interface {
	LinkNewsEvent(ctx context.Context, event NewsEvent) ([]NewsLinkCandidate, error)
}

type NewsEventLinkerFunc func(ctx context.Context, event NewsEvent) ([]NewsLinkCandidate, error)

func (f NewsEventLinkerFunc) LinkNewsEvent(ctx context.Context, event NewsEvent) ([]NewsLinkCandidate, error) {
	return f(ctx, event)
}

type NewsSourceState struct {
	Source              string    `json:"source"`
	Enabled             bool      `json:"enabled"`
	Status              string    `json:"status"`
	Cursor              string    `json:"cursor,omitempty"`
	PollIntervalSeconds int       `json:"pollIntervalSeconds"`
	JitterSeconds       int       `json:"jitterSeconds"`
	BatchLimit          int       `json:"batchLimit"`
	ProcessLimit        int       `json:"processLimit"`
	BackoffBaseSeconds  int       `json:"backoffBaseSeconds"`
	BackoffMaxSeconds   int       `json:"backoffMaxSeconds"`
	NextRunAt           time.Time `json:"nextRunAt,omitempty"`
	LastRunAt           time.Time `json:"lastRunAt,omitempty"`
	LastRunStatus       string    `json:"lastRunStatus,omitempty"`
	LastRunError        string    `json:"lastRunError,omitempty"`
	LastFetchAt         time.Time `json:"lastFetchAt,omitempty"`
	LastSuccessAt       time.Time `json:"lastSuccessAt,omitempty"`
	LastErrorAt         time.Time `json:"lastErrorAt,omitempty"`
	LastError           string    `json:"lastError,omitempty"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	BackoffUntil        time.Time `json:"backoffUntil,omitempty"`
	RawNewsCount        int       `json:"rawNewsCount"`
	NewsEventCount      int       `json:"newsEventCount"`
	LinkCandidateCount  int       `json:"linkCandidateCount"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type NewsSourceOverview struct {
	State      NewsSourceState `json:"state"`
	Configured bool            `json:"configured"`
	Reason     string          `json:"reason,omitempty"`
}

type NewsSourceConfigPatch struct {
	Enabled             *bool      `json:"enabled,omitempty"`
	PollIntervalSeconds *int       `json:"pollIntervalSeconds,omitempty"`
	JitterSeconds       *int       `json:"jitterSeconds,omitempty"`
	BatchLimit          *int       `json:"batchLimit,omitempty"`
	ProcessLimit        *int       `json:"processLimit,omitempty"`
	BackoffBaseSeconds  *int       `json:"backoffBaseSeconds,omitempty"`
	BackoffMaxSeconds   *int       `json:"backoffMaxSeconds,omitempty"`
	NextRunAt           *time.Time `json:"nextRunAt,omitempty"`
}

type NewsPipelineRunResult struct {
	Source             string    `json:"source"`
	Status             string    `json:"status"`
	FetchedAt          time.Time `json:"fetchedAt,omitempty"`
	FetchedCount       int       `json:"fetchedCount"`
	RawInsertedCount   int       `json:"rawInsertedCount"`
	NormalizedCount    int       `json:"normalizedCount"`
	LinkCandidateCount int       `json:"linkCandidateCount"`
	Cursor             string    `json:"cursor,omitempty"`
	NextCursor         string    `json:"nextCursor,omitempty"`
	ErrorMessage       string    `json:"errorMessage,omitempty"`
}

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

type RawNewsListFilter struct {
	Source   string
	Language string
	Status   string
	Quality  string
	Query    string
	Since    time.Time
	Until    time.Time
	Limit    int
	Offset   int
}
