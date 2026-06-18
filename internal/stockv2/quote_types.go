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

type RequestRefreshLatestQuotes struct {
	Symbols       []string `json:"symbols"`
	TriggerSource string   `json:"triggerSource"`
}

var (
	ErrQuoteSymbolsRequired = errors.New("quote symbols are required")
	ErrTooManyQuoteSymbols  = errors.New("too many quote symbols")
	ErrInvalidQuoteSymbol   = errors.New("invalid quote symbol")
)
