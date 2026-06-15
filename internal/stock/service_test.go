package stock

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phantom-lancer/internal/storage"
)

type recordingAgentExecutor struct {
	called bool
}

func (e *recordingAgentExecutor) ExecuteStockReview(context.Context, AgentExecutionInput) (AgentExecutionResult, error) {
	e.called = true
	return AgentExecutionResult{StepKey: "agent_executor", Role: "codex_cli", Status: "completed", Summary: "called"}, nil
}

func TestIngestNewsCreatesAlertForActiveWatch(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:        "Moutai policy watch",
		StrategyType: "account_agnostic",
		Symbol:       "600519",
		Market:       "SH",
		Name:         "贵州茅台",
		Direction:    "watch",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID}); err != nil {
		t.Fatalf("create watch: %v", err)
	}

	svc := NewService(store)
	result, err := svc.IngestNews(ctx, "jin10_manual", []storage.StockNewsItem{{
		SourceItemID: "n-1",
		Symbol:       "600519",
		Market:       "SH",
		Title:        "贵州茅台出现重要消息",
		Summary:      "用于测试消息面采集命中已有盯盘。",
		Importance:   "high",
		Quality:      "fresh",
	}})
	if err != nil {
		t.Fatalf("ingest news: %v", err)
	}
	if result.Task.Status != "completed" {
		t.Fatalf("task status = %q, want completed", result.Task.Status)
	}
	if len(result.NewsItems) != 1 {
		t.Fatalf("news items = %d, want 1", len(result.NewsItems))
	}
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.SourceType != "news_item" {
		t.Fatalf("alert source type = %q, want news_item", alert.SourceType)
	}
	if alert.SourceRefID != result.NewsItems[0].ID {
		t.Fatalf("alert source ref = %q, want %q", alert.SourceRefID, result.NewsItems[0].ID)
	}
}

func TestCreateStrategyFromOpportunity(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	opportunity, err := store.CreateStockOpportunity(ctx, storage.StockOpportunity{
		Title:           "机会生成策略",
		Symbol:          "600519",
		Market:          "SH",
		Name:            "贵州茅台",
		Thesis:          "事件驱动机会。",
		EvidenceSummary: "证据摘要。",
	})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	svc := NewService(store)
	linked, strategy, err := svc.CreateStrategyFromOpportunity(ctx, opportunity.ID, storage.StockStrategy{
		StrategyType: "account_agnostic",
		Direction:    "watch",
	})
	if err != nil {
		t.Fatalf("create strategy from opportunity: %v", err)
	}
	if strategy.Source != "opportunity" || strategy.Symbol != opportunity.Symbol || strategy.Thesis != opportunity.Thesis {
		t.Fatalf("strategy = %+v, opportunity = %+v", strategy, opportunity)
	}
	if linked.Status != "strategy_created" || linked.LinkedStrategyID != strategy.ID {
		t.Fatalf("linked opportunity = %+v, strategy = %+v", linked, strategy)
	}
}

func TestDiscoverOpportunitiesFromNewsAndMarketData(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, _, err := store.UpsertStockNewsItem(ctx, storage.StockNewsItem{
		Source:       "jin10_manual",
		SourceItemID: "news-auto-1",
		Symbol:       "600519",
		Market:       "SH",
		Title:        "贵州茅台发布重要业绩消息",
		Summary:      "业绩改善，等待进一步验证。",
		Category:     "financial_report",
		Importance:   "high",
		Quality:      "fresh",
	}); err != nil {
		t.Fatalf("upsert news: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         "300750",
		Market:         "SZ",
		Name:           "宁德时代",
		LastPrice:      112,
		PreviousClose:  100,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	if _, _, err := store.UpsertStockMarketDataPoint(ctx, storage.StockMarketDataPoint{
		Symbol: "000001", Market: "SZ", Dataset: "daily_kline", DataDate: "2026-06-15", Open: 10, Close: 10.5, Quality: "fresh", Source: "manual_seed",
	}); err != nil {
		t.Fatalf("upsert market data: %v", err)
	}
	svc := NewService(store)
	result, err := svc.DiscoverOpportunities(ctx, "test")
	if err != nil {
		t.Fatalf("discover opportunities: %v", err)
	}
	if result.Task.TaskType != "opportunity_discovery" || result.Task.ProcessedCount == 0 {
		t.Fatalf("task = %+v, want discovery task with processed count", result.Task)
	}
	if len(result.Opportunities) != 3 {
		t.Fatalf("created opportunities = %d, want 3: %+v", len(result.Opportunities), result.Opportunities)
	}
	again, err := svc.DiscoverOpportunities(ctx, "test")
	if err != nil {
		t.Fatalf("discover opportunities again: %v", err)
	}
	if len(again.Opportunities) != 0 {
		t.Fatalf("second discovery created duplicates: %+v", again.Opportunities)
	}
}

func TestCollectMarketDataFromQuotesCreatesCollectionTask(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         "600519",
		Market:         "SH",
		Name:           "贵州茅台",
		LastPrice:      105,
		PreviousClose:  100,
		Volume:         1000,
		Amount:         105000,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	result, err := NewService(store).CollectMarketDataFromQuotes(ctx, "test")
	if err != nil {
		t.Fatalf("collect market data: %v", err)
	}
	if result.Task.TaskType != "market_data_collection" || result.Task.Status != "completed" {
		t.Fatalf("task = %+v, want completed market_data_collection", result.Task)
	}
	if len(result.MarketData) != 2 {
		t.Fatalf("market data points = %d, want quote_derived_kline and quote_snapshot", len(result.MarketData))
	}
	datasets := map[string]bool{}
	for _, point := range result.MarketData {
		datasets[point.Dataset] = true
	}
	if !datasets["quote_derived_kline"] || !datasets["quote_snapshot"] || datasets["daily_kline"] {
		t.Fatalf("datasets = %+v, want quote_derived_kline and quote_snapshot only", datasets)
	}
}

func TestParseSinaQuoteResponse(t *testing.T) {
	body := `var hq_str_sh600519="贵州茅台,10.000,9.500,10.500,11.000,9.000,10.400,10.500,1000,10500.000,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,2026-06-15,10:01:02,00,";`
	quotes := parseSinaQuoteResponse(body, time.Now())
	if len(quotes) != 1 {
		t.Fatalf("quotes = %d, want 1", len(quotes))
	}
	if quotes[0].Symbol != "600519" || quotes[0].LastPrice != 10.5 || quotes[0].Name != "贵州茅台" {
		t.Fatalf("quote = %+v", quotes[0])
	}
}

func TestParseEastmoneyQuoteResponse(t *testing.T) {
	body := []byte(`{"data":{"diff":[{"f12":"600519","f13":1,"f14":"贵州茅台","f2":10.5,"f5":1000,"f6":10500,"f18":9.5,"f124":1781488862}]}}`)
	quotes := parseEastmoneyQuoteResponse(body, time.Now())
	if len(quotes) != 1 {
		t.Fatalf("quotes = %d, want 1", len(quotes))
	}
	if quotes[0].Symbol != "600519" || quotes[0].Market != "SH" || quotes[0].LastPrice != 10.5 {
		t.Fatalf("quote = %+v", quotes[0])
	}
}

func TestQuoteRefreshRespectsProviderNextAllowedAt(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:        "rate limited quote watch",
		StrategyType: "account_agnostic",
		Symbol:       "600519",
		Market:       "SH",
		Name:         "贵州茅台",
		Direction:    "watch",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID}); err != nil {
		t.Fatalf("create watch: %v", err)
	}

	nextAllowed := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
	for _, source := range []string{"eastmoney_public_quote", "sina_public_quote"} {
		if _, err := store.UpsertStockDataSource(ctx, storage.StockDataSource{
			Source:           source,
			DisplayName:      source,
			SourceType:       "market_data",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "available",
			Quality:          "fresh",
			NextAllowedAt:    nextAllowed,
			RateLimitSeconds: 30,
		}); err != nil {
			t.Fatalf("upsert source %s: %v", source, err)
		}
	}

	result, err := NewService(store).RecordQuoteRefreshStatus(ctx, "test")
	if err != nil {
		t.Fatalf("refresh status: %v", err)
	}
	if result.Task.Status != "blocked" {
		t.Fatalf("task status = %q, want blocked", result.Task.Status)
	}
	if result.Task.NextRunAt == "" {
		t.Fatal("blocked task should expose nextRunAt from governed providers")
	}
	if !strings.Contains(result.Task.FailureSummary, "next allowed") {
		t.Fatalf("failure summary = %q, want next allowed reason", result.Task.FailureSummary)
	}
}

func TestConcentrationGuardrailBlocksIndustryOverLimit(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	portfolio, err := store.CreateStockPortfolio(ctx, storage.StockPortfolio{
		Name:                 "concentration",
		Cash:                 100000,
		MaxSinglePositionPct: 0.5,
		AllowBuy:             true,
		AllowAdd:             true,
		AllowReduce:          true,
		AllowSell:            true,
	})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{Symbol: "000001", Name: "旧银行", Industry: "银行", Concept: "金融", Status: "listed", Quality: "fresh"}); err != nil {
		t.Fatalf("instrument old: %v", err)
	}
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{Symbol: "600000", Name: "新银行", Industry: "银行", Concept: "金融", Status: "listed", Quality: "fresh"}); err != nil {
		t.Fatalf("instrument new: %v", err)
	}
	if _, err := store.UpsertStockHolding(ctx, storage.StockHolding{PortfolioID: portfolio.ID, Symbol: "000001", Quantity: 40000, AvailableQuantity: 40000, LastPrice: 10, TradableStatus: "tradable"}); err != nil {
		t.Fatalf("holding: %v", err)
	}
	strategy := storage.StockStrategy{ID: "stst_test", PortfolioID: portfolio.ID, Symbol: "600000", Market: "SH", Name: "新银行", Direction: "buy", TargetPositionPct: 0.2}
	holdings, err := store.ListStockHoldings(ctx, portfolio.ID)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	svc := NewService(store)
	result := svc.proposeOperation(ctx, strategy, portfolio, holdings, storage.StockQuote{
		Symbol: "600000", LastPrice: 10, DataFreshness: "fresh", TradableStatus: "tradable", DataTimestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil)
	if result.guardrailResult != "blocked" || result.operation == nil || result.operation.GuardrailReason != "industry_concentration_limit_exceeded" {
		t.Fatalf("result = %+v", result)
	}
}

func TestReviewAlertCreatesAgentTraceWithoutPatchForCleanTradeSignal(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:        "AI policy watch",
		StrategyType: "account_agnostic",
		Symbol:       "300750",
		Market:       "SZ",
		Name:         "宁德时代",
		Direction:    "buy",
		Thesis:       "消息面催化后观察买入机会。",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	alert, err := store.CreateStockAlert(ctx, storage.StockAlert{
		WatchID:       watch.ID,
		StrategyID:    strategy.ID,
		Symbol:        strategy.Symbol,
		Market:        strategy.Market,
		Name:          strategy.Name,
		Level:         "strong",
		Status:        "new",
		SourceType:    "news_item",
		SourceRefID:   "news-test",
		DedupeKey:     "news-test:watch",
		Title:         "宁德时代消息面命中",
		Summary:       "重要消息触发账户无关策略 Review。",
		TriggerReason: "消息面命中",
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}

	svc := NewService(store)
	result, err := svc.ReviewAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("review alert: %v", err)
	}
	if result.TradeSignal == nil {
		t.Fatal("review should create an account-agnostic trade signal")
	}
	if result.AgentRun == nil {
		t.Fatal("review should create an agent run")
	}
	if result.AgentRun.DecisionProtocol != "analysis_with_challenge" {
		t.Fatalf("decision protocol = %q, want analysis_with_challenge", result.AgentRun.DecisionProtocol)
	}
	if result.StrategyPatch != nil {
		t.Fatalf("clean trade signal should not create strategy patch: %+v", result.StrategyPatch)
	}
	steps, err := store.ListStockAgentRunSteps(ctx, result.AgentRun.ID, 20)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) < 5 {
		t.Fatalf("steps = %d, want at least 5", len(steps))
	}
	claims, err := store.ListStockAgentClaims(ctx, result.AgentRun.ID, 20)
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}
	if len(claims) < 4 {
		t.Fatalf("claims = %d, want at least 4", len(claims))
	}
	summary, err := store.StockAgentTraceSummary(ctx)
	if err != nil {
		t.Fatalf("trace summary: %v", err)
	}
	if summary.RunCount != 1 || summary.PendingPatchCount != 0 {
		t.Fatalf("trace summary = %+v, want one run and no pending patch", summary)
	}
	resolved, err := store.GetStockAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if resolved.Status != "resolved" {
		t.Fatalf("alert status = %q, want resolved after signal-only review", resolved.Status)
	}
	again, err := svc.ReviewAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("review alert again: %v", err)
	}
	if again.Review.ID != result.Review.ID {
		t.Fatalf("repeat review id = %q, want %q", again.Review.ID, result.Review.ID)
	}
	if again.TradeSignal == nil || again.TradeSignal.ID != result.TradeSignal.ID {
		t.Fatalf("repeat review did not return existing trade signal: %+v", again.TradeSignal)
	}
	if again.AgentRun == nil || again.AgentRun.ID != result.AgentRun.ID {
		t.Fatalf("repeat review did not return existing agent run: %+v", again.AgentRun)
	}
	afterRepeat, err := store.StockAgentTraceSummary(ctx)
	if err != nil {
		t.Fatalf("trace summary after repeat: %v", err)
	}
	if afterRepeat.RunCount != 1 {
		t.Fatalf("repeat review created extra run: %+v", afterRepeat)
	}
}

func TestReviewAlertConfirmRequiredCreatesPendingAuthorization(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertStockAgentModelProfile(ctx, storage.StockAgentModelProfile{
		Name:             "Confirm first",
		Provider:         "codex_cli",
		Model:            "default",
		TaskType:         "review",
		DecisionProtocol: "analysis_with_challenge",
		AuthMode:         "confirm_required",
		Enabled:          true,
		Status:           "available",
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:        "confirm required strategy",
		StrategyType: "account_agnostic",
		Symbol:       "300750",
		Market:       "SZ",
		Name:         "宁德时代",
		Direction:    "watch",
		Thesis:       "消息面策略。",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	alert, err := store.CreateStockAlert(ctx, storage.StockAlert{
		WatchID:       watch.ID,
		StrategyID:    strategy.ID,
		Symbol:        strategy.Symbol,
		Market:        strategy.Market,
		Name:          strategy.Name,
		Level:         "strong",
		Status:        "new",
		SourceType:    "news_item",
		SourceRefID:   "news-confirm-required",
		DedupeKey:     "news-confirm-required:watch",
		Title:         "宁德时代消息面命中",
		Summary:       "重要消息触发 confirm_required Review。",
		TriggerReason: "消息面命中",
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	executor := &recordingAgentExecutor{}
	result, err := NewService(store, WithAgentExecutor(executor)).ReviewAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("review alert: %v", err)
	}
	if executor.called {
		t.Fatal("confirm_required profile should not execute before stock module authorization")
	}
	if result.AgentRun == nil || result.AgentRun.Status != "pending_authorization" {
		t.Fatalf("agent run = %+v, want pending_authorization", result.AgentRun)
	}
	auths, err := store.ListStockAgentAuthorizations(ctx, "pending", 10)
	if err != nil {
		t.Fatalf("list authorizations: %v", err)
	}
	if len(auths) != 1 || auths[0].RunID != result.AgentRun.ID {
		t.Fatalf("authorizations = %+v, want one pending authorization for run %s", auths, result.AgentRun.ID)
	}
	steps, err := store.ListStockAgentRunSteps(ctx, result.AgentRun.ID, 20)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	seenAuthorization := false
	for _, step := range steps {
		if step.StepKey == "agent_executor" {
			t.Fatalf("agent_executor step should not exist before approval: %+v", step)
		}
		if step.StepKey == "agent_authorization" && step.Status == "pending" {
			seenAuthorization = true
		}
	}
	if !seenAuthorization {
		t.Fatalf("steps = %+v, want pending agent_authorization step", steps)
	}
}

func TestReviewAlertCreatesStrategyPatchForBlockedOperation(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	portfolio, err := store.CreateStockPortfolio(ctx, storage.StockPortfolio{
		Name:                 "blocked account",
		Cash:                 100000,
		MaxSinglePositionPct: 0.05,
		AllowBuy:             true,
		AllowAdd:             true,
		AllowReduce:          true,
		AllowSell:            true,
	})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:             "oversized add",
		StrategyType:      "account_bound",
		PortfolioID:       portfolio.ID,
		Symbol:            "300750",
		Market:            "SZ",
		Name:              "宁德时代",
		Direction:         "buy",
		TargetPositionPct: 0.2,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         strategy.Symbol,
		Market:         strategy.Market,
		Name:           strategy.Name,
		LastPrice:      100,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	alert, err := store.CreateStockAlert(ctx, storage.StockAlert{
		WatchID:       watch.ID,
		StrategyID:    strategy.ID,
		PortfolioID:   portfolio.ID,
		Symbol:        strategy.Symbol,
		Market:        strategy.Market,
		Name:          strategy.Name,
		Level:         "strong",
		Status:        "new",
		SourceType:    "market_data",
		SourceRefID:   strategy.Symbol,
		DedupeKey:     "blocked-watch",
		Title:         "blocked",
		Summary:       "blocked",
		TriggerReason: "价格触发",
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}

	svc := NewService(store)
	result, err := svc.ReviewAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("review alert: %v", err)
	}
	if result.ProposedOperation == nil || result.ProposedOperation.GuardrailResult != "blocked" {
		t.Fatalf("proposal = %+v, want blocked proposal", result.ProposedOperation)
	}
	if result.StrategyPatch == nil {
		t.Fatal("blocked review should create a pending strategy patch")
	}
	updated, accepted, err := svc.AcceptStrategyPatch(ctx, result.StrategyPatch.ID)
	if err != nil {
		t.Fatalf("accept patch: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("accepted patch status = %q, want accepted", accepted.Status)
	}
	if updated.CurrentVersion != 2 {
		t.Fatalf("strategy version = %d, want 2", updated.CurrentVersion)
	}
	if !strings.Contains(updated.RiskNotes, result.Review.ID) {
		t.Fatalf("strategy risk notes should include review id %q, got %q", result.Review.ID, updated.RiskNotes)
	}
}

func TestCheckWatchesDedupesByCooldownWindow(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:             "price watch",
		StrategyType:      "account_agnostic",
		Symbol:            "600519",
		Market:            "SH",
		Name:              "贵州茅台",
		Direction:         "watch",
		TriggerPriceAbove: 10,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID, CooldownSeconds: 900})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         strategy.Symbol,
		Market:         strategy.Market,
		Name:           strategy.Name,
		LastPrice:      11,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	svc := NewService(store)
	first, err := svc.CheckWatches(ctx, true)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if len(first.Alerts) != 1 {
		t.Fatalf("first alerts = %d, want 1", len(first.Alerts))
	}
	if first.Alerts[0].CooldownUntil == "" {
		t.Fatal("alert should record cooldown_until")
	}
	second, err := svc.CheckWatches(ctx, true)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(second.Alerts) != 0 {
		t.Fatalf("second alerts = %d, want 0", len(second.Alerts))
	}
	if second.Skipped == 0 {
		t.Fatalf("watch %s should be skipped by open dedupe", watch.ID)
	}
}

func TestCheckWatchesSkipsStaleTimestampEvenWhenMarkedFresh(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:             "stale quote watch",
		StrategyType:      "account_agnostic",
		Symbol:            "600519",
		Market:            "SH",
		Direction:         "watch",
		TriggerPriceAbove: 10,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID}); err != nil {
		t.Fatalf("create watch: %v", err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, loc)
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         strategy.Symbol,
		LastPrice:      11,
		DataTimestamp:  now.Add(-30 * time.Minute).Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	svc := NewService(store)
	svc.now = func() time.Time { return now }
	result, err := svc.CheckWatches(ctx, true)
	if err != nil {
		t.Fatalf("check watches: %v", err)
	}
	if len(result.Alerts) != 0 || result.Skipped == 0 {
		t.Fatalf("result = %+v, want stale timestamp skip", result)
	}
}

func TestCheckWatchesRespectsWatchInterval(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:             "interval watch",
		StrategyType:      "account_agnostic",
		Symbol:            "600519",
		Market:            "SH",
		Direction:         "watch",
		TriggerPriceAbove: 10,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID, CheckIntervalSeconds: 365 * 24 * 60 * 60})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	if err := store.TouchStockWatch(ctx, watch.ID); err != nil {
		t.Fatalf("touch watch: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         strategy.Symbol,
		LastPrice:      11,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}
	svc := NewService(store)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	svc.now = func() time.Time {
		return time.Date(2026, 6, 15, 10, 0, 0, 0, loc)
	}
	result, err := svc.CheckWatches(ctx, false)
	if err != nil {
		t.Fatalf("check watches: %v", err)
	}
	if result.Checked != 0 || result.Skipped == 0 {
		t.Fatalf("result = %+v, want interval skip", result)
	}
}

func TestMarketClockUsesExchangeCalendar(t *testing.T) {
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	svc := NewService(store)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	svc.now = func() time.Time {
		return time.Date(2026, 2, 16, 10, 0, 0, 0, loc)
	}
	clock := svc.MarketClock()
	if clock.TradingDay || clock.CalendarStatus != "exchange_calendar" || clock.Session != "holiday_or_weekend" {
		t.Fatalf("clock = %+v, want exchange holiday", clock)
	}
}
