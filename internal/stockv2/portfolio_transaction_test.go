package stockv2

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordTransaction_BuyNewHolding(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)

	result, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Market: "SH", Name: "浦发银行",
		Side: "buy", Quantity: 100, Price: 10,
	})
	if err != nil {
		t.Fatalf("buy failed: %v", err)
	}
	if result.Transaction.Side != "buy" || result.Transaction.Amount != 1000 {
		t.Fatalf("transaction = %+v", result.Transaction)
	}
	if result.Holding.Quantity != 100 || result.Holding.CostPrice != 10 {
		t.Fatalf("holding = %+v", result.Holding)
	}
	if result.Portfolio.Cash != 9000 {
		t.Fatalf("portfolio cash = %v, want 9000", result.Portfolio.Cash)
	}
}

func TestRecordTransaction_BuyAddsPosition(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)

	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Market: "SH", Name: "浦发银行",
		Side: "buy", Quantity: 100, Price: 10,
	}); err != nil {
		t.Fatalf("first buy: %v", err)
	}
	result, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 12,
	})
	if err != nil {
		t.Fatalf("second buy: %v", err)
	}
	if result.Holding.Quantity != 200 {
		t.Fatalf("qty = %v, want 200", result.Holding.Quantity)
	}
	// 加权平均成本: (100*10 + 100*12) / 200 = 11
	if result.Holding.CostPrice != 11 {
		t.Fatalf("cost price = %v, want 11", result.Holding.CostPrice)
	}
	if result.Portfolio.Cash != 10000-1000-1200 {
		t.Fatalf("cash = %v, want 7800", result.Portfolio.Cash)
	}
}

func TestRecordTransaction_SellReduces(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)

	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 200, Price: 10,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}
	result, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "sell", Quantity: 50, Price: 12,
	})
	if err != nil {
		t.Fatalf("sell: %v", err)
	}
	if result.Holding.Quantity != 150 {
		t.Fatalf("qty = %v, want 150", result.Holding.Quantity)
	}
	// 卖出成本价不变
	if result.Holding.CostPrice != 10 {
		t.Fatalf("cost price = %v, want 10", result.Holding.CostPrice)
	}
	if result.Portfolio.Cash != 10000-2000+600 {
		t.Fatalf("cash = %v, want 8600", result.Portfolio.Cash)
	}
	if result.HoldingCleared {
		t.Fatal("holding should not be cleared")
	}

	// 流水应有 2 条
	txs, err := svc.ListTransactions(ctx, portfolio.ID, 10)
	if err != nil {
		t.Fatalf("list txs: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("txs count = %d, want 2", len(txs))
	}
}

func TestRecordTransaction_SellAllClears(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)
	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 10,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}
	result, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "sell", Quantity: 100, Price: 12,
	})
	if err != nil {
		t.Fatalf("sell all: %v", err)
	}
	if !result.HoldingCleared {
		t.Fatal("holding should be cleared")
	}
	if result.Portfolio.Cash != 10000-1000+1200 {
		t.Fatalf("cash = %v, want 10200", result.Portfolio.Cash)
	}

	holdings, err := store.ListHoldings(ctx, portfolio.ID)
	if err != nil {
		t.Fatalf("list holdings: %v", err)
	}
	if len(holdings) != 0 {
		t.Fatalf("holdings count = %d, want 0 after clear", len(holdings))
	}
}

func TestRecordTransaction_SellOverInsufficient(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)
	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 10,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	// 卖出 200,超过持仓 100
	_, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "sell", Quantity: 200, Price: 12,
	})
	if err == nil {
		t.Fatal("expected error for over-selling, got nil")
	}
	if err != ErrInsufficientHolding {
		t.Fatalf("error = %v, want ErrInsufficientHolding", err)
	}
}

func TestRecordTransaction_SellUnknown(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)
	_, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "000001", Side: "sell", Quantity: 10, Price: 10,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrInsufficientHolding {
		t.Fatalf("error = %v, want ErrInsufficientHolding", err)
	}
}

func TestRecordTransaction_InvalidSide(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)
	_, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "invalid", Quantity: 10, Price: 10,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrInvalidTransactionSide {
		t.Fatalf("error = %v, want ErrInvalidTransactionSide", err)
	}
}

func TestRecordTransaction_ExecutedAtParsing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})
	portfolio := createTestPortfolio(t, store, "p-1", 10000)

	// 空字符串 → 用现在
	r1, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 10, Price: 10,
	})
	if err != nil {
		t.Fatalf("buy without executedAt: %v", err)
	}
	if r1.Transaction.ExecutedAt.IsZero() {
		t.Fatal("executedAt should not be zero")
	}

	// RFC3339
	past := "2020-06-15T10:30:00Z"
	r2, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 10, Price: 10, ExecutedAt: past,
	})
	if err != nil {
		t.Fatalf("buy with rfc3339: %v", err)
	}
	if got := r2.Transaction.ExecutedAt.UTC().Format(time.RFC3339); got != past {
		t.Fatalf("executedAt = %v, want %v", got, past)
	}

	// datetime-local 格式
	dl := "2021-07-20T09:00"
	r3, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 10, Price: 10, ExecutedAt: dl,
	})
	if err != nil {
		t.Fatalf("buy with datetime-local: %v", err)
	}
	if r3.Transaction.ExecutedAt.IsZero() {
		t.Fatal("executedAt should not be zero")
	}

	// 未来时间 → 报错
	future := time.Now().AddDate(0, 0, 10).Format(time.RFC3339)
	_, err = svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 10, Price: 10, ExecutedAt: future,
	})
	if err == nil {
		t.Fatal("expected error for future date, got nil")
	}
}

func TestRecordTransaction_NegativeCashAllowed(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 500)
	result, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 10, // 1000 > 500
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Portfolio.Cash != -500 {
		t.Fatalf("cash = %v, want -500", result.Portfolio.Cash)
	}
}

func TestBuildAssetCurve_NoDailyBars_Fallback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, &http.Client{})

	portfolio := createTestPortfolio(t, store, "p-1", 10000)
	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 10, ExecutedAt: "2024-01-15T10:00",
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	curve, err := svc.BuildAssetCurve(ctx, portfolio.ID, AssetCurveOptions{})
	if err != nil {
		t.Fatalf("build curve: %v", err)
	}
	// 无日K 时只有今天一个点(today),用最新价/成本价 fallback
	if len(curve.Points) == 0 {
		t.Fatal("expected at least today point")
	}
	if !curve.Estimated {
		t.Fatal("expected estimated=true when no daily bars")
	}
	if len(curve.Markers) != 1 {
		t.Fatalf("markers = %d, want 1", len(curve.Markers))
	}
	if curve.Markers[0].Side != "buy" || curve.Markers[0].Symbol != "600000" {
		t.Fatalf("marker = %+v", curve.Markers[0])
	}
}

func TestBuildAssetCurve_WithDailyBars(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, &http.Client{})

	fetchedAt := time.Date(2024, 6, 20, 15, 0, 0, 0, time.UTC)
	// 造两天日 K 数据
	if err := store.marketDB.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-17", Close: 9.5, Volume: 900, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-18", Close: 10, Volume: 1000, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-19", Close: 11, Volume: 1200, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	portfolio := createTestPortfolio(t, store, "p-1", 10000)
	// 6/17 买入 100 股 @ 10 → 6/18 收盘 10,6/19 收盘 11
	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 10, ExecutedAt: "2024-06-17T10:00:00Z",
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	curve, err := svc.BuildAssetCurve(ctx, portfolio.ID, AssetCurveOptions{})
	if err != nil {
		t.Fatalf("build curve: %v", err)
	}
	if len(curve.Points) < 2 {
		t.Fatalf("points count = %d, want >= 2", len(curve.Points))
	}
	if curve.Estimated {
		t.Fatal("expected estimated=false when daily bars available for window")
	}

	// 最后一个交易日(6/19)总资产 = 现金 9000 + 持仓 100*11 = 10100
	var june19 *AssetCurvePoint
	for i := range curve.Points {
		if curve.Points[i].Date == "2024-06-19" {
			jp := curve.Points[i]
			june19 = &jp
			break
		}
	}
	if june19 == nil {
		t.Fatal("2024-06-19 point not found")
	}
	if june19.Total != 10100 {
		t.Fatalf("6/19 total = %v, want 10100", june19.Total)
	}
	if june19.Cash != 9000 {
		t.Fatalf("6/19 cash = %v, want 9000", june19.Cash)
	}
	if june19.HoldingValue != 1100 {
		t.Fatalf("6/19 holding value = %v, want 1100", june19.HoldingValue)
	}
}

func TestBuildAssetCurve_AcquiredHolding_ManualPosition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, &http.Client{})

	fetchedAt := time.Date(2024, 6, 20, 15, 0, 0, 0, time.UTC)
	if err := store.marketDB.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-17", Close: 10, Volume: 1000, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-18", Close: 10.5, Volume: 1100, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-19", Close: 11, Volume: 1200, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	portfolio := createTestPortfolio(t, store, "p-1", 10000)

	// 手动建仓(不走 RecordTransaction):100 股 @ 10,建仓时间 2024-06-17
	acquiredAt := time.Date(2024, 6, 17, 9, 30, 0, 0, time.UTC)
	holding := StockV2Holding{
		ID:                "h-1",
		PortfolioID:       portfolio.ID,
		Symbol:            "600000",
		Market:            "SH",
		Name:              "浦发银行",
		Quantity:          100,
		AvailableQuantity: 100,
		CostPrice:         10,
		AcquiredAt:        acquiredAt,
	}
	if err := store.CreateHolding(ctx, holding); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	curve, err := svc.BuildAssetCurve(ctx, portfolio.ID, AssetCurveOptions{})
	if err != nil {
		t.Fatalf("build curve: %v", err)
	}
	if len(curve.Points) == 0 {
		t.Fatal("expected curve points, got 0")
	}
	if curve.Estimated {
		t.Fatal("expected estimated=false when daily bars cover acquire date")
	}
	// 0 笔交易 → 0 个 marker(建仓不打买卖标记)
	if len(curve.Markers) != 0 {
		t.Fatalf("markers = %d, want 0 (acquire events don't produce markers)", len(curve.Markers))
	}

	// 6/19 总资产 = 现金 10000 + 持仓 100*11 = 11100
	var june19 *AssetCurvePoint
	for i := range curve.Points {
		if curve.Points[i].Date == "2024-06-19" {
			jp := curve.Points[i]
			june19 = &jp
			break
		}
	}
	if june19 == nil {
		t.Fatal("2024-06-19 point not found")
	}
	if june19.Total != 11100 {
		t.Fatalf("6/19 total = %v, want 11100", june19.Total)
	}
	if june19.Cash != 10000 {
		t.Fatalf("6/19 cash = %v, want 10000 (acquire doesn't change cash)", june19.Cash)
	}
	if june19.HoldingValue != 1100 {
		t.Fatalf("6/19 holding value = %v, want 1100", june19.HoldingValue)
	}
}

func TestBuildAssetCurve_AcquiredHolding_PlusLaterBuy(t *testing.T) {
	// 验证:初始建仓 + 后续买入交易,资产曲线重放正确
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, &http.Client{})

	fetchedAt := time.Date(2024, 6, 20, 15, 0, 0, 0, time.UTC)
	if err := store.marketDB.UpsertDailyBars(ctx, []StockV2DailyBar{
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-15", Close: 9, Volume: 900, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-17", Close: 10, Volume: 1000, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
		{Symbol: "600000", Market: "SH", TradeDate: "2024-06-19", Close: 11, Volume: 1200, Adjusted: DailyBarAdjustedNone, Source: "test", FetchedAt: fetchedAt, Quality: DailyBarQualityOK},
	}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	portfolio := createTestPortfolio(t, store, "p-1", 10000)

	// 6/15 手动建仓 100 股 @ 9(不动现金)
	acquiredAt := time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC)
	holding := StockV2Holding{
		ID:                "h-1",
		PortfolioID:       portfolio.ID,
		Symbol:            "600000",
		Market:            "SH",
		Name:              "浦发银行",
		Quantity:          100,
		AvailableQuantity: 100,
		CostPrice:         9,
		AcquiredAt:        acquiredAt,
	}
	if err := store.CreateHolding(ctx, holding); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	// 6/17 又买入 100 股 @ 10(扣现金 1000)
	if _, err := svc.RecordTransaction(ctx, portfolio.ID, RequestRecordTransaction{
		Symbol: "600000", Side: "buy", Quantity: 100, Price: 10,
		ExecutedAt: "2024-06-17T10:00:00Z",
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	curve, err := svc.BuildAssetCurve(ctx, portfolio.ID, AssetCurveOptions{})
	if err != nil {
		t.Fatalf("build curve: %v", err)
	}

	// 1 笔交易 → 1 个 marker
	if len(curve.Markers) != 1 {
		t.Fatalf("markers = %d, want 1", len(curve.Markers))
	}
	if curve.Markers[0].Side != "buy" {
		t.Fatalf("marker side = %s, want buy", curve.Markers[0].Side)
	}

	// 6/19: 200 股 * 11 = 2200,现金 = 10000 - 1000 = 9000,总资产 = 11200
	var june19 *AssetCurvePoint
	for i := range curve.Points {
		if curve.Points[i].Date == "2024-06-19" {
			jp := curve.Points[i]
			june19 = &jp
			break
		}
	}
	if june19 == nil {
		t.Fatal("2024-06-19 point not found")
	}
	if june19.Cash != 9000 {
		t.Fatalf("6/19 cash = %v, want 9000", june19.Cash)
	}
	if june19.HoldingValue != 2200 {
		t.Fatalf("6/19 holding value = %v, want 2200", june19.HoldingValue)
	}
	if june19.Total != 11200 {
		t.Fatalf("6/19 total = %v, want 11200", june19.Total)
	}
}

// --- helpers ---

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestPortfolio(t *testing.T, store *Store, id string, cash float64) StockV2Portfolio {
	t.Helper()
	portfolio := StockV2Portfolio{
		ID: id, Name: "test-" + id, Cash: cash, RiskLevel: "medium",
	}
	if err := store.CreatePortfolio(context.Background(), portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	return portfolio
}
