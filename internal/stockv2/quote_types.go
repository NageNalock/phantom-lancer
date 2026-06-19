package stockv2

import (
	"errors"
	"time"
)

const (
	QuoteStatusFresh     = "fresh"
	QuoteStatusStale     = "stale"
	QuoteStatusFailed    = "failed"
	QuoteStatusEstimated = "estimated"
	QuoteSourceTencent   = "tencent"
)

// StockV2QuoteLatest is the latest-state quote for one instrument. It is not
// a minute bar or historical series.
type StockV2QuoteLatest struct {
	Symbol       string    `json:"symbol"`
	Market       string    `json:"market"`
	Name         string    `json:"name"`
	LastPrice    float64   `json:"lastPrice"`
	PrevClose    float64   `json:"prevClose"`
	OpenPrice    float64   `json:"openPrice"`
	HighPrice    float64   `json:"highPrice"`
	LowPrice     float64   `json:"lowPrice"`
	Volume       float64   `json:"volume"`
	Amount       float64   `json:"amount"`
	PctChange    float64   `json:"pctChange"`
	QuoteAt      time.Time `json:"quoteAt"`
	FetchedAt    time.Time `json:"fetchedAt"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
}

type QuoteRefreshResult struct {
	Items          []StockV2QuoteLatest `json:"items"`
	RefreshedCount int                  `json:"refreshedCount"`
	FailedCount    int                  `json:"failedCount"`
	FailedItems    []UpdateFailure      `json:"failedItems"`
	FetchedAt      time.Time            `json:"fetchedAt"`
}

// QuoteRefreshStatus is the update-only refresh state for one symbol. It is
// deliberately not a history table: high-frequency quote refresh should update
// this row instead of appending monitor runs.
type QuoteRefreshStatus struct {
	Symbol              string    `json:"symbol"`
	Market              string    `json:"market,omitempty"`
	Source              string    `json:"source,omitempty"`
	Status              string    `json:"status"`
	LastAttemptAt       time.Time `json:"lastAttemptAt"`
	LastSuccessAt       time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt       time.Time `json:"lastFailureAt,omitempty"`
	ErrorMessage        string    `json:"errorMessage,omitempty"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type QuoteRefreshTaskState struct {
	TaskType     string    `json:"taskType"`
	Status       string    `json:"status"`
	TriggerType  string    `json:"triggerType,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	FinishedAt   time.Time `json:"finishedAt,omitempty"`
	ScopeSummary string    `json:"scopeSummary,omitempty"`
	ScannedCount int       `json:"scannedCount"`
	SuccessCount int       `json:"successCount"`
	FailedCount  int       `json:"failedCount"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type RequestRefreshLatestQuotes struct {
	Symbols       []string `json:"symbols"`
	TriggerSource string   `json:"triggerSource"`
}

var (
	ErrQuoteSymbolsRequired = errors.New("quote symbols are required")
	ErrTooManyQuoteSymbols  = errors.New("too many quote symbols")
	ErrInvalidQuoteSymbol   = errors.New("invalid quote symbol")
)
