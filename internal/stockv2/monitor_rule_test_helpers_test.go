package stockv2

import (
	"context"
	"testing"
	"time"
)

func seedMonitorQuote(t *testing.T, svc *Service, symbol string, price, pct float64, status string, fetchedAt time.Time) {
	t.Helper()
	if err := svc.store.UpsertLatestQuote(context.Background(), StockV2QuoteLatest{
		Symbol:    symbol,
		Market:    inferAStockMarket(symbol),
		Name:      symbol,
		LastPrice: price,
		PrevClose: price / (1 + pct/100),
		PctChange: pct,
		QuoteAt:   fetchedAt,
		FetchedAt: fetchedAt,
		Source:    QuoteSourceTencent,
		Status:    status,
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
}

func seedMonitorDailyBar(t *testing.T, svc *Service, symbol string, close float64) {
	t.Helper()
	now := time.Now()
	if err := svc.store.UpsertDailyBars(context.Background(), []StockV2DailyBar{{
		ID:        "bar-" + symbol,
		Symbol:    symbol,
		Market:    inferAStockMarket(symbol),
		TradeDate: now.In(chinaMarketTZ).Format("2006-01-02"),
		Open:      close,
		High:      close,
		Low:       close,
		Close:     close,
		PrevClose: close,
		Adjusted:  DailyBarAdjustedNone,
		Source:    "test",
		FetchedAt: now,
		Quality:   "ok",
		CreatedAt: now,
		UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("upsert daily bar: %v", err)
	}
}
