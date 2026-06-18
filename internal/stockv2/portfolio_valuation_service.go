package stockv2

import (
	"context"
	"time"
)

func (s *Service) RefreshPortfolioValuation(ctx context.Context, portfolioID string, triggerSource string) (PortfolioRefreshResult, error) {
	portfolio, err := s.store.GetPortfolio(ctx, portfolioID)
	if err != nil {
		return PortfolioRefreshResult{}, err
	}
	holdings, err := s.store.ListHoldings(ctx, portfolioID)
	if err != nil {
		return PortfolioRefreshResult{}, wrapError(err, "list holdings")
	}

	symbols := uniqueHoldingSymbols(holdings)
	quotes, err := s.store.GetLatestQuotes(ctx, symbols)
	if err != nil {
		return PortfolioRefreshResult{}, wrapError(err, "get latest quotes")
	}

	result := RecalculatePortfolioFromQuotes(portfolio, holdings, quotes, time.Now())
	if err := s.store.SavePortfolioValuation(ctx, result.Holdings, result.Snapshot); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) GetPortfolioSnapshots(ctx context.Context, portfolioID string, limit int) ([]PortfolioSnapshot, error) {
	return s.store.GetPortfolioSnapshots(ctx, portfolioID, limit)
}

func RecalculatePortfolioFromQuotes(portfolio StockV2Portfolio, holdings []StockV2Holding, quotes []StockV2QuoteLatest, valuationAt time.Time) PortfolioRefreshResult {
	if valuationAt.IsZero() {
		valuationAt = time.Now()
	}

	quotesBySymbol := make(map[string]StockV2QuoteLatest, len(quotes))
	for _, quote := range quotes {
		quotesBySymbol[quote.Symbol] = quote
	}

	result := PortfolioRefreshResult{
		PortfolioID: portfolio.ID,
		FailedItems: []UpdateFailure{},
		Holdings:    make([]StockV2Holding, 0, len(holdings)),
	}

	holdingMarketValue := 0.0
	for _, holding := range holdings {
		updated, quality, failed := recalculateHoldingFromQuote(holding, quotesBySymbol[holding.Symbol], valuationAt)
		if failed != "" {
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: holding.Symbol, Reason: failed})
			result.FailedCount++
		}
		switch quality {
		case PortfolioValuationStatusFresh:
			result.RefreshedCount++
		case PortfolioValuationStatusStale:
			result.StaleCount++
		case PortfolioValuationStatusEstimated:
			result.EstimatedCount++
		}
		holdingMarketValue += updated.MarketValue
		result.Holdings = append(result.Holdings, updated)
	}

	totalAssetValue := portfolio.Cash + holdingMarketValue
	cashPct := 0.0
	if totalAssetValue > 0 {
		cashPct = portfolio.Cash / totalAssetValue * 100
	}
	for i := range result.Holdings {
		if totalAssetValue > 0 {
			result.Holdings[i].PositionPct = result.Holdings[i].MarketValue / totalAssetValue * 100
		} else {
			result.Holdings[i].PositionPct = 0
		}
	}

	status := PortfolioValuationStatusFresh
	if result.FailedCount > 0 {
		status = PortfolioValuationStatusFailed
	} else if result.EstimatedCount > 0 {
		status = PortfolioValuationStatusEstimated
	} else if result.StaleCount > 0 {
		status = PortfolioValuationStatusStale
	}

	result.Snapshot = PortfolioSnapshot{
		ID:                  generateID(),
		PortfolioID:         portfolio.ID,
		ValuationAt:         valuationAt,
		Cash:                portfolio.Cash,
		HoldingMarketValue:  holdingMarketValue,
		TotalAssetValue:     totalAssetValue,
		CashPct:             cashPct,
		PositionCount:       len(holdings),
		StaleQuoteCount:     result.StaleCount,
		EstimatedQuoteCount: result.EstimatedCount,
		Source:              PortfolioValuationSourceLatestQuote,
		Status:              status,
		CreatedAt:           valuationAt,
	}
	return result
}

func recalculateHoldingFromQuote(holding StockV2Holding, quote StockV2QuoteLatest, valuationAt time.Time) (StockV2Holding, string, string) {
	price := 0.0
	priceAt := time.Time{}
	quality := PortfolioValuationStatusFailed
	failure := ""

	if quote.Symbol != "" && quote.Status == QuoteStatusFresh && quote.LastPrice > 0 {
		price = quote.LastPrice
		priceAt = quote.QuoteAt
		if priceAt.IsZero() {
			priceAt = quote.FetchedAt
		}
		quality = PortfolioValuationStatusFresh
	} else if holding.LastPrice > 0 {
		price = holding.LastPrice
		priceAt = holding.LastPriceAt
		if priceAt.IsZero() {
			priceAt = valuationAt
		}
		quality = PortfolioValuationStatusStale
	} else if holding.CostPrice > 0 {
		price = holding.CostPrice
		priceAt = valuationAt
		quality = PortfolioValuationStatusEstimated
	} else {
		failure = "no quote or usable fallback price"
	}

	if price > 0 {
		holding.LastPrice = price
		holding.LastPriceAt = priceAt
		holding.MarketValue = holding.Quantity * price
		holding.PnL = (price - holding.CostPrice) * holding.Quantity
		holding.TradableStatus = quality
	} else {
		holding.MarketValue = 0
		holding.PnL = 0
		holding.TradableStatus = PortfolioValuationStatusFailed
	}
	holding.UpdatedAt = valuationAt
	return holding, quality, failure
}

func uniqueHoldingSymbols(holdings []StockV2Holding) []string {
	seen := make(map[string]struct{}, len(holdings))
	symbols := make([]string, 0, len(holdings))
	for _, holding := range holdings {
		if holding.Symbol == "" {
			continue
		}
		if _, ok := seen[holding.Symbol]; ok {
			continue
		}
		seen[holding.Symbol] = struct{}{}
		symbols = append(symbols, holding.Symbol)
	}
	return symbols
}
