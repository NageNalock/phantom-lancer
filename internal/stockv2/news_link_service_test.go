package stockv2

import (
	"context"
	"strings"
	"testing"
)

func TestLinkNewsEventMatchesStockName(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Status:         "active",
	})

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "宁德时代公告称动力电池订单增长"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one", candidates)
	}
	if candidates[0].Symbol != "300750" || candidates[0].MatchMethod != NewsLinkMatchExactName || candidates[0].Score != newsScoreExactName {
		t.Fatalf("candidate = %+v, want exact name match", candidates[0])
	}

	listed, err := svc.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{NewsEventID: event.ID, Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(listed) != 1 || listed[0].Symbol != "300750" {
		t.Fatalf("listed = %+v, want 300750 candidate", listed)
	}
}

func TestLinkNewsEventMatchesETFName(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-510300",
		Symbol:         "510300",
		Market:         "SH",
		InstrumentType: InstrumentTypeExchangeFund,
		Name:           "沪深300ETF",
		Status:         "active",
	})

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "沪深300ETF 午后成交额明显放大"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Symbol != "510300" || candidates[0].MatchMethod != NewsLinkMatchExactName {
		t.Fatalf("candidates = %+v, want ETF exact name match", candidates)
	}
}

func TestLinkNewsEventKeywordRecall(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Concepts:       []string{"锂电池", "储能"},
		Status:         "active",
	})

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "锂电池产业链价格出现回暖迹象"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want keyword candidate", candidates)
	}
	if candidates[0].MatchMethod != NewsLinkMatchKeyword || !newsTestContains(candidates[0].MatchedTerms, "锂电池") {
		t.Fatalf("candidate = %+v, want keyword 锂电池", candidates[0])
	}
}

func TestLinkNewsEventBoostsHoldingAndActiveStrategy(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	for _, inst := range []StockV2Instrument{
		{ID: "inst-300001", Symbol: "300001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "持仓机器人", Concepts: []string{"机器人"}, Status: "active"},
		{ID: "inst-300002", Symbol: "300002", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "策略机器人", Concepts: []string{"机器人"}, Status: "active"},
		{ID: "inst-300003", Symbol: "300003", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "普通机器人", Concepts: []string{"机器人"}, Status: "active"},
	} {
		seedNewsLinkProfile(t, svc, inst)
	}
	portfolio := StockV2Portfolio{ID: "portfolio-news-boost", Name: "消息测试组合", Cash: 10000, RiskLevel: "medium", AllowBuy: true, AllowAdd: true, AllowReduce: true, AllowSell: true}
	if err := svc.store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-300001",
		PortfolioID:       portfolio.ID,
		Symbol:            "300001",
		Market:            "SZ",
		Name:              "持仓机器人",
		Quantity:          100,
		AvailableQuantity: 100,
		CostPrice:         10,
		TradableStatus:    "tradable",
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "策略机器人跟踪",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "300002",
		Market:    "SZ",
		Title:     "策略机器人观察",
		Direction: StrategyDirectionWatch,
	}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "机器人产业链催化持续发酵"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	held := newsCandidateForSymbol(t, candidates, "300001")
	strategy := newsCandidateForSymbol(t, candidates, "300002")
	plain := newsCandidateForSymbol(t, candidates, "300003")
	if held.Score <= plain.Score || strategy.Score <= plain.Score {
		t.Fatalf("scores held=%v strategy=%v plain=%v, want boosts above plain", held.Score, strategy.Score, plain.Score)
	}
	if held.MatchMethod != NewsLinkMatchBoosted || strategy.MatchMethod != NewsLinkMatchBoosted || plain.MatchMethod == NewsLinkMatchBoosted {
		t.Fatalf("methods held=%s strategy=%s plain=%s, want only boosted candidates marked boosted", held.MatchMethod, strategy.MatchMethod, plain.MatchMethod)
	}
	if !strings.Contains(held.Reason, "当前持仓 boost") || !strings.Contains(strategy.Reason, "活跃策略 boost") {
		t.Fatalf("boost reasons held=%q strategy=%q", held.Reason, strategy.Reason)
	}
}

func TestLinkNewsEventMergesMultipleHitsForSameSymbol(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Concepts:       []string{"锂电池"},
		Status:         "active",
	})

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "宁德时代受益于锂电池需求改善"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want merged one", candidates)
	}
	candidate := candidates[0]
	if candidate.MatchMethod != NewsLinkMatchExactName || !newsTestContains(candidate.MatchedTerms, "宁德时代") || !newsTestContains(candidate.MatchedTerms, "锂电池") {
		t.Fatalf("candidate = %+v, want merged exact name + keyword terms", candidate)
	}
	if !strings.Contains(candidate.Reason, "命中标的名称") || !strings.Contains(candidate.Reason, "命中画像关键词") {
		t.Fatalf("reason = %q, want both reasons", candidate.Reason)
	}
}

func seedNewsLinkProfile(t *testing.T, svc *Service, instrument StockV2Instrument) {
	t.Helper()
	if err := svc.store.UpsertInstrument(context.Background(), instrument); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
	if _, err := svc.BuildStockProfile(context.Background(), instrument.Symbol); err != nil {
		t.Fatalf("build profile: %v", err)
	}
}

func createNewsLinkEvent(t *testing.T, svc *Service, event NewsEvent) NewsEvent {
	t.Helper()
	created, err := svc.CreateNewsEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	return created
}

func newsCandidateForSymbol(t *testing.T, candidates []NewsLinkCandidate, symbol string) NewsLinkCandidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Symbol == symbol {
			return candidate
		}
	}
	t.Fatalf("candidate for %s not found: %+v", symbol, candidates)
	return NewsLinkCandidate{}
}

func newsTestContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
