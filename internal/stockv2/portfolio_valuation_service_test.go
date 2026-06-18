package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshPortfolioValuationUsesQuotesAndWritesSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	portfolio := StockV2Portfolio{
		ID:        "portfolio-1",
		Name:      "测试组合",
		Cash:      1000,
		RiskLevel: "medium",
	}
	if err := store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	oldPriceAt := time.Date(2026, 6, 17, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	for _, holding := range []StockV2Holding{
		{ID: "holding-fresh", PortfolioID: portfolio.ID, Symbol: "000001", Market: "SZ", Name: "平安银行", Quantity: 100, CostPrice: 10},
		{ID: "holding-stale", PortfolioID: portfolio.ID, Symbol: "600000", Market: "SH", Name: "浦发银行", Quantity: 10, CostPrice: 8, LastPrice: 9, LastPriceAt: oldPriceAt},
		{ID: "holding-estimated", PortfolioID: portfolio.ID, Symbol: "300001", Market: "SZ", Name: "特锐德", Quantity: 5, CostPrice: 20},
	} {
		if err := store.CreateHolding(ctx, holding); err != nil {
			t.Fatalf("create holding %s: %v", holding.Symbol, err)
		}
	}
	quoteAt := time.Date(2026, 6, 18, 14, 55, 3, 0, oldPriceAt.Location())
	if err := store.UpsertLatestQuote(ctx, StockV2QuoteLatest{
		Symbol:    "000001",
		Market:    "SZ",
		Name:      "平安银行",
		LastPrice: 12,
		PrevClose: 11,
		QuoteAt:   quoteAt,
		FetchedAt: quoteAt.Add(2 * time.Second),
		Source:    QuoteSourceTencent,
		Status:    QuoteStatusFresh,
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}

	svc := NewService(store, nil, nil)
	result, err := svc.RefreshPortfolioValuation(ctx, portfolio.ID, "test")
	if err != nil {
		t.Fatalf("refresh valuation: %v", err)
	}
	if result.RefreshedCount != 1 || result.StaleCount != 1 || result.EstimatedCount != 1 || result.FailedCount != 0 {
		t.Fatalf("result counts = %+v", result)
	}
	if result.Snapshot.HoldingMarketValue != 1390 || result.Snapshot.TotalAssetValue != 2390 {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}

	bySymbol := map[string]StockV2Holding{}
	for _, holding := range result.Holdings {
		bySymbol[holding.Symbol] = holding
	}
	if got := bySymbol["000001"]; got.LastPrice != 12 || got.MarketValue != 1200 || got.PnL != 200 || got.TradableStatus != PortfolioValuationStatusFresh {
		t.Fatalf("fresh holding = %+v", got)
	}
	if got := bySymbol["600000"]; got.LastPrice != 9 || got.LastPriceAt.IsZero() || got.TradableStatus != PortfolioValuationStatusStale {
		t.Fatalf("stale holding = %+v", got)
	}
	if got := bySymbol["300001"]; got.LastPrice != 20 || !got.LastPriceAt.Equal(result.Snapshot.ValuationAt) || got.TradableStatus != PortfolioValuationStatusEstimated {
		t.Fatalf("estimated holding = %+v", got)
	}

	snapshots, err := svc.GetPortfolioSnapshots(ctx, portfolio.ID, 20)
	if err != nil {
		t.Fatalf("get snapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != result.Snapshot.ID {
		t.Fatalf("snapshots = %+v, want created snapshot", snapshots)
	}
}
