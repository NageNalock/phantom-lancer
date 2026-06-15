package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStockConfirmOperationResolvesAlert(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	portfolio, err := store.CreateStockPortfolio(ctx, StockPortfolio{Name: "A-share test", Cash: 100000})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	strategy, err := store.CreateStockStrategy(ctx, StockStrategy{
		Title:        "Bound add",
		StrategyType: "account_bound",
		PortfolioID:  portfolio.ID,
		Symbol:       "600519",
		Direction:    "add",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, StockWatch{StrategyID: strategy.ID})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	alert, err := store.CreateStockAlert(ctx, StockAlert{
		WatchID:     watch.ID,
		StrategyID:  strategy.ID,
		PortfolioID: portfolio.ID,
		Symbol:      strategy.Symbol,
		Level:       "strong",
		Status:      "new",
		DedupeKey:   "test-alert",
		Title:       "triggered",
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	review, err := store.CreateStockReview(ctx, StockReview{
		AlertID:         alert.ID,
		WatchID:         watch.ID,
		StrategyID:      strategy.ID,
		PortfolioID:     portfolio.ID,
		Symbol:          strategy.Symbol,
		Status:          "completed",
		ReviewResult:    "propose_operation",
		GuardrailResult: "passed",
		Summary:         "ok",
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	proposal, err := store.CreateStockProposedOperation(ctx, StockProposedOperation{
		ReviewID:        review.ID,
		StrategyID:      strategy.ID,
		PortfolioID:     portfolio.ID,
		Symbol:          strategy.Symbol,
		Action:          "add",
		Quantity:        100,
		Price:           10,
		Amount:          1000,
		GuardrailResult: "passed",
		Status:          "pending_confirmation",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, StockQuote{
		Symbol:         strategy.Symbol,
		LastPrice:      10,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	if _, err := store.ConfirmStockProposedOperation(ctx, proposal.ID, proposal.Price, proposal.Quantity, "confirmed"); err != nil {
		t.Fatalf("confirm operation: %v", err)
	}
	resolved, err := store.GetStockAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if resolved.Status != "resolved" {
		t.Fatalf("alert status = %q, want resolved", resolved.Status)
	}
	if resolved.ResolvedAt == "" {
		t.Fatal("resolved alert should record resolved_at")
	}
}

func TestStockPortfolioAndHoldingConstraintsArePreserved(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	portfolio, err := store.CreateStockPortfolio(ctx, StockPortfolio{
		Name:        "sell only",
		Cash:        1000,
		AllowBuy:    false,
		AllowAdd:    true,
		AllowReduce: true,
		AllowSell:   true,
	})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if portfolio.AllowBuy || !portfolio.AllowAdd || !portfolio.AllowReduce || !portfolio.AllowSell {
		t.Fatalf("portfolio permissions were not preserved: %+v", portfolio)
	}
	holding, err := store.UpsertStockHolding(ctx, StockHolding{
		PortfolioID:       portfolio.ID,
		Symbol:            "600519",
		Quantity:          100,
		AvailableQuantity: 0,
		TradableStatus:    "tradable",
	})
	if err != nil {
		t.Fatalf("upsert holding: %v", err)
	}
	if holding.AvailableQuantity != 0 {
		t.Fatalf("available quantity = %.2f, want 0", holding.AvailableQuantity)
	}
}

func TestStockOpportunityLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	opportunity, err := store.CreateStockOpportunity(ctx, StockOpportunity{
		Title:           "AI 算力事件机会",
		SourceType:      "manual",
		Symbol:          "300750",
		Market:          "SZ",
		Theme:           "AI 算力",
		Thesis:          "事件驱动，观察回踩。",
		EvidenceSummary: "消息和资金流共振。",
		Confidence:      "medium",
	})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	items, err := store.ListStockOpportunities(ctx, "candidate", 10)
	if err != nil {
		t.Fatalf("list opportunities: %v", err)
	}
	if len(items) != 1 || items[0].ID != opportunity.ID {
		t.Fatalf("items = %+v, want created opportunity", items)
	}
	linked, err := store.LinkStockOpportunityStrategy(ctx, opportunity.ID, "stst_test")
	if err != nil {
		t.Fatalf("link opportunity: %v", err)
	}
	if linked.Status != "strategy_created" || linked.LinkedStrategyID != "stst_test" {
		t.Fatalf("linked opportunity = %+v", linked)
	}
}

func TestStockAgentProfileSelectionPrefersEnabledExecutor(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if _, err := store.UpsertStockAgentModelProfile(ctx, StockAgentModelProfile{
		Name:             "External",
		Provider:         "codex_cli",
		Model:            "default",
		TaskType:         "review",
		DecisionProtocol: "single_review",
		AuthMode:         "user_config",
		Enabled:          true,
		Status:           "available",
	}); err != nil {
		t.Fatalf("enabled non-system profile should be accepted when executor provider is supported: %v", err)
	}
	profile, err := store.SelectStockAgentModelProfile(ctx, "review", "single_review")
	if err != nil {
		t.Fatalf("select profile: %v", err)
	}
	if profile.Provider != "codex_cli" || profile.AuthMode != "user_config" || profile.Status != "available" {
		t.Fatalf("selected profile = %+v, want available codex_cli executor", profile)
	}
}

func TestStockWatchAndAlertLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, StockStrategy{
		Title:             "watch lifecycle",
		StrategyType:      "account_agnostic",
		Symbol:            "600519",
		TriggerPriceAbove: 10,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, StockWatch{StrategyID: strategy.ID})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	updatedWatch, err := store.UpdateStockWatch(ctx, watch.ID, StockWatch{
		Status:               "paused",
		CheckIntervalSeconds: 45,
		CooldownSeconds:      120,
		TriggerPriceAbove:    12,
	})
	if err != nil {
		t.Fatalf("update watch: %v", err)
	}
	if updatedWatch.Status != "paused" || updatedWatch.CheckIntervalSeconds != 45 || updatedWatch.CooldownSeconds != 120 || updatedWatch.TriggerPriceAbove != 12 {
		t.Fatalf("updated watch = %+v", updatedWatch)
	}
	zero := 0.0
	clearedWatch, err := store.UpdateStockWatchFields(ctx, watch.ID, StockWatchUpdate{TriggerPriceAbove: &zero, TriggerPriceBelow: &zero})
	if err != nil {
		t.Fatalf("clear watch triggers: %v", err)
	}
	if clearedWatch.TriggerPriceAbove != 0 || clearedWatch.TriggerPriceBelow != 0 {
		t.Fatalf("cleared triggers = above %.3f below %.3f, want 0/0", clearedWatch.TriggerPriceAbove, clearedWatch.TriggerPriceBelow)
	}

	alert, err := store.CreateStockAlert(ctx, StockAlert{
		WatchID:     watch.ID,
		StrategyID:  strategy.ID,
		Symbol:      strategy.Symbol,
		Status:      "new",
		DedupeKey:   "lifecycle",
		Title:       "lifecycle",
		SourceType:  "market_data",
		SourceRefID: strategy.Symbol,
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	snoozed, err := store.UpdateStockAlertLifecycle(ctx, alert.ID, "snoozed", 60)
	if err != nil {
		t.Fatalf("snooze alert: %v", err)
	}
	if snoozed.Status != "snoozed" || snoozed.CooldownUntil == "" || snoozed.AcknowledgedAt == "" {
		t.Fatalf("snoozed alert = %+v", snoozed)
	}
	changed, err := store.WakeSnoozedStockAlerts(ctx, time.Now().UTC().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("wake snoozed: %v", err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d, want 1", changed)
	}
	woken, err := store.GetStockAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if woken.Status != "new" {
		t.Fatalf("woken status = %q, want new", woken.Status)
	}
}

func TestStockConfirmOperationUsesFullPortfolioForPositionLimit(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	portfolio, err := store.CreateStockPortfolio(ctx, StockPortfolio{
		Name:                 "full portfolio",
		Cash:                 100000,
		MaxSinglePositionPct: 0.2,
		AllowBuy:             true,
		AllowAdd:             true,
		AllowReduce:          true,
		AllowSell:            true,
	})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if _, err := store.UpsertStockHolding(ctx, StockHolding{
		PortfolioID:       portfolio.ID,
		Symbol:            "000001",
		Quantity:          90000,
		AvailableQuantity: 90000,
		LastPrice:         10,
		TradableStatus:    "tradable",
	}); err != nil {
		t.Fatalf("upsert other holding: %v", err)
	}
	strategy, err := store.CreateStockStrategy(ctx, StockStrategy{
		Title:        "new ticket",
		StrategyType: "account_bound",
		PortfolioID:  portfolio.ID,
		Symbol:       "600519",
		Direction:    "buy",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	review, err := store.CreateStockReview(ctx, StockReview{
		StrategyID:      strategy.ID,
		PortfolioID:     portfolio.ID,
		Symbol:          strategy.Symbol,
		Status:          "completed",
		ReviewResult:    "propose_operation",
		GuardrailResult: "passed",
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	proposal, err := store.CreateStockProposedOperation(ctx, StockProposedOperation{
		ReviewID:        review.ID,
		StrategyID:      strategy.ID,
		PortfolioID:     portfolio.ID,
		Symbol:          strategy.Symbol,
		Action:          "buy",
		Quantity:        10000,
		Price:           10,
		Amount:          100000,
		GuardrailResult: "passed",
		Status:          "pending_confirmation",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, StockQuote{
		Symbol:         strategy.Symbol,
		LastPrice:      10,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	if _, err := store.ConfirmStockProposedOperation(ctx, proposal.ID, proposal.Price, proposal.Quantity, "confirmed"); err != nil {
		t.Fatalf("confirm should use full portfolio denominator: %v", err)
	}
}

func TestStockCancelProposedOperationResolvesAlertAndWritesMemory(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	portfolio, err := store.CreateStockPortfolio(ctx, StockPortfolio{Name: "cancel account", Cash: 100000})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	strategy, err := store.CreateStockStrategy(ctx, StockStrategy{
		Title:        "cancel strategy",
		StrategyType: "account_bound",
		PortfolioID:  portfolio.ID,
		Symbol:       "600519",
		Direction:    "buy",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, StockWatch{StrategyID: strategy.ID})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	alert, err := store.CreateStockAlert(ctx, StockAlert{WatchID: watch.ID, StrategyID: strategy.ID, PortfolioID: portfolio.ID, Symbol: strategy.Symbol, Status: "acknowledged", DedupeKey: "cancel"})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	review, err := store.CreateStockReview(ctx, StockReview{AlertID: alert.ID, WatchID: watch.ID, StrategyID: strategy.ID, PortfolioID: portfolio.ID, Symbol: strategy.Symbol, Status: "completed", ReviewResult: "propose_operation", GuardrailResult: "passed"})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	proposal, err := store.CreateStockProposedOperation(ctx, StockProposedOperation{ReviewID: review.ID, StrategyID: strategy.ID, PortfolioID: portfolio.ID, Symbol: strategy.Symbol, Action: "buy", Quantity: 100, Price: 10, Amount: 1000, GuardrailResult: "passed", Status: "pending_confirmation"})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	cancelled, err := store.CancelStockProposedOperation(ctx, proposal.ID, "skip")
	if err != nil {
		t.Fatalf("cancel proposal: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}
	resolved, err := store.GetStockAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if resolved.Status != "resolved" {
		t.Fatalf("alert status = %q, want resolved", resolved.Status)
	}
	memories, err := store.ListStockMemories(ctx, 10)
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if len(memories) != 1 || memories[0].ObjectType != "proposed_operation_cancelled" {
		t.Fatalf("memories = %+v", memories)
	}
}
