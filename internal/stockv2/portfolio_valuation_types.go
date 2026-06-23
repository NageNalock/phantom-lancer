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

// AssetCurvePoint 资产曲线的单个交易日点:由交易流水 + 历史日 K 回算得出。
type AssetCurvePoint struct {
	Date         string  `json:"date"`         // "2006-01-02"
	Cash         float64 `json:"cash"`         // 当日现金(交易重放后)
	HoldingValue float64 `json:"holdingValue"` // 当日持仓市值
	Total        float64 `json:"total"`        // = Cash + HoldingValue
}

// AssetCurveMarker 资产曲线上的买卖标记,每个交易点钉到「≤ 成交日的最近交易日」。
type AssetCurveMarker struct {
	Date     string  `json:"date"`
	Side     string  `json:"side"` // buy | sell
	Symbol   string  `json:"symbol"`
	Name     string  `json:"name,omitempty"`
	Quantity float64 `json:"quantity"`
	Price    float64 `json:"price"`
	Amount   float64 `json:"amount"`
	Total    float64 `json:"total"` // 当日总资产(便于 tooltip)
}

// AssetCurveResponse 资产曲线响应。
type AssetCurveResponse struct {
	PortfolioID string             `json:"portfolioId"`
	Points      []AssetCurvePoint  `json:"points"`
	Markers     []AssetCurveMarker `json:"markers"`
	Start       string             `json:"start"`     // "2006-01-02"
	End         string             `json:"end"`       // "2006-01-02"
	Estimated   bool               `json:"estimated"` // 有 symbol 缺日 K 用了估算价
}

// AssetCurveOptions 资产曲线查询参数。Days>0 时只返回最近 N 天(仍重放窗口外交易以对齐初值)。
type AssetCurveOptions struct {
	Days int
}
