package stockv2

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestLinkNewsEventUsesSemanticProfileRecall(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	configureEmbeddingModel(t, svc, "embed-v1")
	upsertEmbeddingTestProfile(t, svc, "300750", "宁德时代", "动力电池")
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}}); err != nil {
		t.Fatalf("rebuild embeddings: %v", err)
	}

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "电池订单增长带动储能产业链"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one semantic candidate", candidates)
	}
	if candidates[0].Symbol != "300750" || candidates[0].MatchMethod != NewsLinkMatchSemanticProfile {
		t.Fatalf("candidate = %+v, want semantic profile 300750", candidates[0])
	}
}

func TestLinkNewsEventIgnoresGenericEnglishProfileTextTerms(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:          "600000",
		Market:          "SH",
		Name:            "浦发银行",
		ProfileText:     "bank is on the market for retail credit",
		AIProfileStatus: StockProfileAIStatusReady,
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	event := createNewsLinkEvent(t, svc, NewsEvent{
		Source: "test",
		Title:  "BoE's Mann: the question is whether there will be upside surprises in fiscal policy.",
	})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want generic English terms ignored", candidates)
	}
}

func TestLinkNewsEventKeepsSpecificEnglishProfileTextTerm(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:          "688001",
		Market:          "SH",
		Name:            "半导体设备",
		ProfileText:     "semiconductor lithography equipment",
		AIProfileStatus: StockProfileAIStatusReady,
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "Semiconductor equipment demand improves"})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	if len(candidates) != 1 || candidates[0].MatchMethod != NewsLinkMatchProfileKeyword || !newsTestContains(candidates[0].MatchedTerms, "semiconductor") {
		t.Fatalf("candidates = %+v, want specific English profile keyword", candidates)
	}
}

func TestListPendingNewsLinkCandidatesPrioritizesHighConfidence(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "消息面队列排序"})
	for _, candidate := range []NewsLinkCandidate{
		{
			NewsEventID:    event.ID,
			Symbol:         "600000",
			Market:         "SH",
			InstrumentName: "低分噪音",
			MatchMethod:    NewsLinkMatchProfileKeyword,
			Score:          newsScoreProfileKeyword,
			Reason:         "低分画像文本",
			MatchedTerms:   []string{"market"},
		},
		{
			NewsEventID:    event.ID,
			Symbol:         "300750",
			Market:         "SZ",
			InstrumentName: "高分明确命中",
			MatchMethod:    NewsLinkMatchExactName,
			Score:          newsScoreExactName,
			Reason:         "命中标的名称",
			MatchedTerms:   []string{"高分明确命中"},
		},
		{
			NewsEventID:    event.ID,
			Symbol:         "162719",
			Market:         "SZ",
			InstrumentName: "一般语义召回",
			MatchMethod:    NewsLinkMatchSemanticProfile,
			Score:          61,
			Reason:         "语义召回画像",
			MatchedTerms:   []string{"一般语义召回"},
		},
	} {
		if _, err := svc.store.UpsertNewsLinkCandidate(ctx, candidate); err != nil {
			t.Fatalf("upsert candidate %s: %v", candidate.Symbol, err)
		}
	}

	pending, err := svc.store.ListPendingNewsLinkCandidates(ctx, 2)
	if err != nil {
		t.Fatalf("list pending candidates: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %+v, want two", pending)
	}
	if pending[0].Symbol != "300750" || pending[1].Symbol != "162719" {
		t.Fatalf("pending order = %+v, want high-confidence candidates before low-score profile keyword", pending)
	}
}

func TestPruneNewsLinkCandidatesKeepsHighValueRecords(t *testing.T) {
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	ctx := context.Background()
	event := createNewsLinkEvent(t, svc, NewsEvent{ID: "event-retention", Source: "test", Title: "通用市场消息"})
	old := time.Now().AddDate(0, 0, -15)

	candidates := []NewsLinkCandidate{
		{
			ID: "old-skipped", NewsEventID: event.ID, Symbol: "600001", Market: "SH", InstrumentName: "低价值已跳过",
			MatchMethod: NewsLinkMatchProfileKeyword, Score: 40, MonitorStatus: NewsLinkMonitorStatusSkipped,
		},
		{
			ID: "old-low-pending", NewsEventID: event.ID, Symbol: "600002", Market: "SH", InstrumentName: "低价值待处理",
			MatchMethod: NewsLinkMatchSemanticProfile, Score: 45, MonitorStatus: NewsLinkMonitorStatusPending,
		},
		{
			ID: "old-hit", NewsEventID: event.ID, Symbol: "600003", Market: "SH", InstrumentName: "已命中",
			MatchMethod: NewsLinkMatchSemanticProfile, Score: 40, MonitorStatus: NewsLinkMonitorStatusHit,
		},
		{
			ID: "old-high", NewsEventID: event.ID, Symbol: "600004", Market: "SH", InstrumentName: "高分候选",
			MatchMethod: NewsLinkMatchSemanticProfile, Score: 90, MonitorStatus: NewsLinkMonitorStatusPending,
		},
	}
	for _, candidate := range candidates {
		if _, err := svc.store.UpsertNewsLinkCandidate(ctx, candidate); err != nil {
			t.Fatalf("upsert candidate %s: %v", candidate.ID, err)
		}
	}
	if _, err := svc.store.assetDB().ExecContext(ctx, `
		UPDATE stockv2_news_link_candidates
		SET created_at = ?, updated_at = ?
		WHERE id IN ('old-skipped', 'old-low-pending', 'old-hit', 'old-high')
	`, old, old); err != nil {
		t.Fatalf("age candidates: %v", err)
	}

	result, err := svc.store.PruneNewsLinkCandidates(ctx, time.Now())
	if err != nil {
		t.Fatalf("prune candidates: %v", err)
	}
	if result.DeletedTotal != 2 {
		t.Fatalf("deleted total = %d, result = %#v", result.DeletedTotal, result)
	}
	for _, id := range []string{"old-hit", "old-high"} {
		if _, err := svc.store.GetNewsLinkCandidate(ctx, id); err != nil {
			t.Fatalf("expected %s to remain: %v", id, err)
		}
	}
	for _, id := range []string{"old-skipped", "old-low-pending"} {
		if _, err := svc.store.GetNewsLinkCandidate(ctx, id); !errors.Is(err, ErrNewsLinkCandidateNotFound) {
			t.Fatalf("expected %s deleted, err = %v", id, err)
		}
	}
}

func TestNewsEventAndLinkCandidateFiltersUseTimeWindow(t *testing.T) {
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	inWindow := createNewsLinkEvent(t, svc, NewsEvent{ID: "event-window-in", Source: "test", Title: "窗口内消息", EventAt: now.Add(-time.Hour)})
	outWindow := createNewsLinkEvent(t, svc, NewsEvent{ID: "event-window-out", Source: "test", Title: "窗口外消息", EventAt: now.Add(-5 * time.Hour)})
	for _, candidate := range []NewsLinkCandidate{
		{ID: "candidate-window-in", NewsEventID: inWindow.ID, Symbol: "000977", Market: "SZ", MatchMethod: NewsLinkMatchExactSymbol, Score: 100},
		{ID: "candidate-window-out", NewsEventID: outWindow.ID, Symbol: "000977", Market: "SZ", MatchMethod: NewsLinkMatchExactSymbol, Score: 100},
	} {
		if _, err := svc.store.UpsertNewsLinkCandidate(ctx, candidate); err != nil {
			t.Fatalf("upsert candidate %s: %v", candidate.ID, err)
		}
	}

	events, err := svc.ListNewsEvents(ctx, NewsEventListFilter{Since: now.Add(-2 * time.Hour), Until: now, Limit: 10})
	if err != nil {
		t.Fatalf("list news events: %v", err)
	}
	if len(events) != 1 || events[0].ID != inWindow.ID {
		t.Fatalf("events = %+v, want only in-window event", events)
	}
	candidates, err := svc.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{Symbol: "000977", Since: now.Add(-2 * time.Hour), Until: now, Limit: 10})
	if err != nil {
		t.Fatalf("list news link candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].NewsEventID != inWindow.ID {
		t.Fatalf("candidates = %+v, want only in-window candidate", candidates)
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
