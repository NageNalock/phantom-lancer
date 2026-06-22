package stockv2

import (
	"context"
	"testing"
)

func TestRunNewsStrategyMonitorHitsHolding(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-news-holding",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Concepts:       []string{"锂电池"},
		Status:         "active",
	})
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-news-holding")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-news-300750",
		PortfolioID:       portfolio.ID,
		Symbol:            "300750",
		Market:            "SZ",
		Name:              "宁德时代",
		Quantity:          100,
		AvailableQuantity: 100,
		CostPrice:         200,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	event := createNewsLinkEvent(t, svc, NewsEvent{
		RawNewsID: "raw-news-holding",
		Source:    NewsSourceJin10,
		Title:     "锂电池产业链订单回暖",
	})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	candidate := newsCandidateForSymbol(t, candidates, "300750")

	run, err := svc.RunMonitorTask(ctx, MonitorTaskNewsStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run news monitor: %v", err)
	}
	if run.Status != MonitorRunStatusCompleted || run.HitCount != 1 || run.ReviewCount != 1 {
		t.Fatalf("run = %+v, want completed with one hit/review", run)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskNewsStrategyMonitor, Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one", hits)
	}
	if hits[0].PortfolioID != portfolio.ID {
		t.Fatalf("portfolio id = %q, want %q", hits[0].PortfolioID, portfolio.ID)
	}
	if hits[0].Evidence["candidate_id"] != candidate.ID || hits[0].Evidence["news_event_id"] != event.ID {
		t.Fatalf("hit evidence = %+v, want news linkage", hits[0].Evidence)
	}
	if hits[0].Evidence["match_method"] != candidate.MatchMethod || hits[0].Evidence["source"] != NewsSourceJin10 {
		t.Fatalf("hit evidence = %+v, want match/source fields", hits[0].Evidence)
	}
	monitored, err := svc.store.GetNewsLinkCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("get monitored candidate: %v", err)
	}
	if monitored.MonitorStatus != NewsLinkMonitorStatusHit || monitored.MonitorHitID != hits[0].ID {
		t.Fatalf("candidate monitor state = %+v, want hit linked to monitor hit", monitored)
	}
}

func TestRunNewsStrategyMonitorHitsActiveStrategy(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-news-strategy",
		Symbol:         "688012",
		Market:         "SH",
		InstrumentType: InstrumentTypeStock,
		Name:           "中微公司",
		Status:         "active",
	})
	strategy, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "中微公司跟踪",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "688012",
		Market:    "SH",
		Direction: StrategyDirectionWatch,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "中微公司发布新产品进展"})
	if _, err := svc.LinkNewsEvent(ctx, event.ID); err != nil {
		t.Fatalf("link news event: %v", err)
	}

	run, err := svc.RunMonitorTask(ctx, MonitorTaskNewsStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run news monitor: %v", err)
	}
	if run.HitCount != 1 {
		t.Fatalf("hit count = %d, want 1", run.HitCount)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskNewsStrategyMonitor, Symbol: "688012", Limit: 10})
	if err != nil {
		t.Fatalf("list hits: %v", err)
	}
	if len(hits) != 1 || hits[0].StrategyID != strategy.Strategy.ID {
		t.Fatalf("hits = %+v, want active strategy linkage %s", hits, strategy.Strategy.ID)
	}
}

func TestRunNewsStrategyMonitorSkipsLowQualityCandidate(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-news-low",
		Symbol:         "600000",
		Market:         "SH",
		InstrumentType: InstrumentTypeStock,
		Name:           "浦发银行",
		Status:         "active",
	})
	event := createNewsLinkEvent(t, svc, NewsEvent{Source: "test", Title: "浦发银行相关传闻", QualityStatus: NewsQualityLow})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	candidate := newsCandidateForSymbol(t, candidates, "600000")

	run, err := svc.RunMonitorTask(ctx, MonitorTaskNewsStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run news monitor: %v", err)
	}
	if run.HitCount != 0 {
		t.Fatalf("hit count = %d, want 0 for low quality event", run.HitCount)
	}
	monitored, err := svc.store.GetNewsLinkCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("get candidate: %v", err)
	}
	if monitored.MonitorStatus != NewsLinkMonitorStatusSkipped {
		t.Fatalf("monitor status = %s, want skipped", monitored.MonitorStatus)
	}
}

func TestNewsMonitorReviewContextIncludesNews(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	seedNewsLinkProfile(t, svc, StockV2Instrument{
		ID:             "inst-news-context",
		Symbol:         "002594",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "比亚迪",
		Status:         "active",
	})
	event := createNewsLinkEvent(t, svc, NewsEvent{
		RawNewsID:     "raw-news-context",
		Source:        NewsSourceJin10,
		Title:         "比亚迪海外销量继续增长",
		Summary:       "消息称新能源汽车出口保持高增速",
		QualityStatus: NewsImportanceHigh,
	})
	candidates, err := svc.LinkNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("link news event: %v", err)
	}
	candidate := newsCandidateForSymbol(t, candidates, "002594")
	run, err := svc.RunMonitorTask(ctx, MonitorTaskNewsStrategyMonitor, MonitorTriggerManual)
	if err != nil {
		t.Fatalf("run news monitor: %v", err)
	}
	if run.HitCount != 1 {
		t.Fatalf("hit count = %d, want 1", run.HitCount)
	}
	hits, err := svc.ListMonitorHits(ctx, MonitorHitListFilter{TaskType: MonitorTaskNewsStrategyMonitor, Symbol: "002594", Limit: 10})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %+v err=%v, want one", hits, err)
	}
	reviews, err := svc.ListOperationReviews(ctx, OperationReviewListFilter{HitID: hits[0].ID, Limit: 10})
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %+v, want one", reviews)
	}
	pack := reviews[0].InputContext
	if pack.NewsEvent == nil || pack.NewsEvent.ID != event.ID {
		t.Fatalf("news event context = %+v, want %s", pack.NewsEvent, event.ID)
	}
	if pack.NewsLink == nil || pack.NewsLink.ID != candidate.ID {
		t.Fatalf("news link context = %+v, want %s", pack.NewsLink, candidate.ID)
	}
	if pack.Profile == nil || pack.Profile.Symbol != "002594" {
		t.Fatalf("profile context = %+v, want 002594", pack.Profile)
	}
}
