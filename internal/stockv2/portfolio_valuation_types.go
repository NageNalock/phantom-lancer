package stockv2

import "time"

const (
	PortfolioValuationSourceLatestQuote = "latest_quote"
	PortfolioValuationStatusFresh       = "fresh"
	PortfolioValuationStatusStale       = "stale"
	PortfolioValuationStatusEstimated   = "estimated"
	PortfolioValuationStatusFailed      = "failed"
)

type PortfolioSnapshot struct {
	ID                  string    `json:"id"`
	PortfolioID         string    `json:"portfolioId"`
	ValuationAt         time.Time `json:"valuationAt"`
	Cash                float64   `json:"cash"`
	HoldingMarketValue  float64   `json:"holdingMarketValue"`
	TotalAssetValue     float64   `json:"totalAssetValue"`
	CashPct             float64   `json:"cashPct"`
	PositionCount       int       `json:"positionCount"`
	StaleQuoteCount     int       `json:"staleQuoteCount"`
	EstimatedQuoteCount int       `json:"estimatedQuoteCount"`
	Source              string    `json:"source"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"createdAt"`
}

type PortfolioRefreshResult struct {
	PortfolioID    string            `json:"portfolioId"`
	RefreshedCount int               `json:"refreshedCount"`
	StaleCount     int               `json:"staleCount"`
	EstimatedCount int               `json:"estimatedCount"`
	FailedCount    int               `json:"failedCount"`
	FailedItems    []UpdateFailure   `json:"failedItems"`
	Snapshot       PortfolioSnapshot `json:"snapshot"`
	Holdings       []StockV2Holding  `json:"holdings"`
}

type RequestRefreshPortfolioValuation struct {
	TriggerSource string `json:"triggerSource"`
}
