package stockv2

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBuildPortfolioSentinelContextIncludesHoldingsAndWindowNews(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-context")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-context",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedMonitorQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "test",
		Title:   "浪潮信息相关存储服务器链条承压",
		EventAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	if _, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		NewsEventID:     event.ID,
		Symbol:          "000977",
		Market:          "SZ",
		InstrumentName:  "浪潮信息",
		MatchMethod:     NewsLinkMatchExactSymbol,
		Score:           100,
		Reason:          "持仓代码命中",
		MonitorStatus:   NewsLinkMonitorStatusPending,
		NewsEventAt:     event.EventAt,
		NewsEventTitle:  event.Title,
		NewsEventSource: event.Source,
	}); err != nil {
		t.Fatalf("create news link candidate: %v", err)
	}
	oldEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "test",
		Title:   "浪潮信息窗口外旧消息",
		EventAt: now.Add(-4 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create old news event: %v", err)
	}
	if _, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		NewsEventID: oldEvent.ID,
		Symbol:      "000977",
		Market:      "SZ",
		MatchMethod: NewsLinkMatchExactSymbol,
		Score:       100,
	}); err != nil {
		t.Fatalf("create old news link candidate: %v", err)
	}
	run := PortfolioSentinelRun{
		ID:            "sentinel-context-run",
		PortfolioID:   portfolio.ID,
		TriggerType:   PortfolioSentinelTriggerManual,
		WindowType:    PortfolioSentinelWindowManual,
		WindowStartAt: now.Add(-2 * time.Hour),
		WindowEndAt:   now,
	}
	pack, err := svc.BuildPortfolioSentinelContext(ctx, run, "test note")
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(pack.Portfolios) != 1 || len(pack.Portfolios[0].Holdings) != 1 {
		t.Fatalf("portfolio context = %+v, want one holding", pack.Portfolios)
	}
	holding := pack.Portfolios[0].Holdings[0]
	if holding.Quote == nil || holding.Quote.PctChange != -3.2 {
		t.Fatalf("holding quote = %+v, want seeded quote", holding.Quote)
	}
	if len(holding.RawNews) != 0 {
		t.Fatalf("holding raw news len = %d, want 0", len(holding.RawNews))
	}
	if len(holding.News) != 1 || holding.News[0].ID != event.ID {
		t.Fatalf("holding news = %+v, want only linked window event", holding.News)
	}
	if len(holding.NewsLinks) != 1 || holding.NewsLinks[0].NewsEventID != event.ID {
		t.Fatalf("holding news links = %+v, want linked window candidate", holding.NewsLinks)
	}
	if len(pack.RawNews) != 0 || len(pack.NewsEvents) != 1 {
		t.Fatalf("pack raw=%d events=%d, want no raw and one event", len(pack.RawNews), len(pack.NewsEvents))
	}
}

func TestBuildPortfolioSentinelContextSuppressesLowPriorityNewsCandidates(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-news-filter")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-news-filter",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	strongEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "test",
		Title:   "浪潮信息签署重要订单",
		EventAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create strong event: %v", err)
	}
	if _, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		NewsEventID:     strongEvent.ID,
		Symbol:          "000977",
		Market:          "SZ",
		InstrumentName:  "浪潮信息",
		MatchMethod:     NewsLinkMatchExactName,
		Score:           95,
		Reason:          "命中标的名称 浪潮信息",
		MatchedTerms:    []string{"浪潮信息"},
		MonitorStatus:   NewsLinkMonitorStatusPending,
		NewsEventAt:     strongEvent.EventAt,
		NewsEventTitle:  strongEvent.Title,
		NewsEventSource: strongEvent.Source,
	}); err != nil {
		t.Fatalf("create strong candidate: %v", err)
	}
	for i := 0; i < 25; i++ {
		event, err := svc.CreateNewsEvent(ctx, NewsEvent{
			Source:  "test",
			Title:   fmt.Sprintf("泛 AI 风险弱关联新闻 %02d", i),
			EventAt: now.Add(-time.Duration(i+2) * time.Minute),
		})
		if err != nil {
			t.Fatalf("create weak event %d: %v", i, err)
		}
		if _, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
			NewsEventID:     event.ID,
			Symbol:          "000977",
			Market:          "SZ",
			InstrumentName:  "浪潮信息",
			MatchMethod:     NewsLinkMatchBoosted,
			Score:           50,
			Reason:          "命中画像文本 global；当前持仓 boost",
			MatchedTerms:    []string{"global"},
			MonitorStatus:   NewsLinkMonitorStatusPending,
			NewsEventAt:     event.EventAt,
			NewsEventTitle:  event.Title,
			NewsEventSource: event.Source,
		}); err != nil {
			t.Fatalf("create weak candidate %d: %v", i, err)
		}
	}
	run := PortfolioSentinelRun{
		ID:            "sentinel-news-filter-run",
		PortfolioID:   portfolio.ID,
		TriggerType:   PortfolioSentinelTriggerManual,
		WindowType:    PortfolioSentinelWindowManual,
		WindowStartAt: now.Add(-time.Hour),
		WindowEndAt:   now,
	}
	pack, err := svc.BuildPortfolioSentinelContext(ctx, run, "")
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	holding := pack.Portfolios[0].Holdings[0]
	if len(holding.NewsLinks) != 11 {
		t.Fatalf("news links = %d, want one strong plus ten weak samples", len(holding.NewsLinks))
	}
	if holding.NewsLinks[0].NewsEventID != strongEvent.ID {
		t.Fatalf("first retained candidate = %+v, want strong direct event first", holding.NewsLinks[0])
	}
	if got := intFromContextStat(pack.ContextStats["newsInputCandidateCount"]); got != 26 {
		t.Fatalf("newsInputCandidateCount = %d, want 26", got)
	}
	if got := intFromContextStat(pack.ContextStats["newsRetainedCandidateCount"]); got != 11 {
		t.Fatalf("newsRetainedCandidateCount = %d, want 11", got)
	}
	if got := intFromContextStat(pack.ContextStats["retainedLowPriorityNewsCount"]); got != 10 {
		t.Fatalf("retainedLowPriorityNewsCount = %d, want 10", got)
	}
	if got := intFromContextStat(pack.ContextStats["suppressedLowPriorityNewsCount"]); got != 15 {
		t.Fatalf("suppressedLowPriorityNewsCount = %d, want 15", got)
	}
}

func TestPortfolioSentinelReportNormalizesStringListFields(t *testing.T) {
	report, err := portfolioSentinelReportFromResult(map[string]any{
		"schema_version":     PortfolioSentinelReportSchemaVersion,
		"overall_risk_level": PortfolioSentinelRiskMedium,
		"run_summary":        "数据质量说明为对象数组也应可保存",
		"data_quality_notes": []any{
			map[string]any{"type": "freshness", "note": "报价新鲜"},
			"单条字符串说明",
		},
		"next_watch_focus": map[string]any{"summary": "继续观察 AI 链"},
		"affected_holdings": []any{
			map[string]any{
				"symbol":        "000977",
				"reasons":       []any{map[string]any{"severity": "high", "reason": "弱结构也要转成文本"}},
				"evidence_refs": map[string]any{"summary": "来源摘要"},
			},
		},
	})
	if err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if len(report.DataQualityNotes) != 2 || report.DataQualityNotes[0] != "[freshness] 报价新鲜" {
		t.Fatalf("data quality notes = %#v, want normalized strings", report.DataQualityNotes)
	}
	if len(report.NextWatchFocus) != 1 || report.NextWatchFocus[0] != "继续观察 AI 链" {
		t.Fatalf("next watch focus = %#v, want normalized string list", report.NextWatchFocus)
	}
	if len(report.AffectedHoldings) != 1 ||
		len(report.AffectedHoldings[0].Reasons) != 1 ||
		report.AffectedHoldings[0].Reasons[0] != "[high] 弱结构也要转成文本" ||
		report.AffectedHoldings[0].EvidenceRefs[0] != "来源摘要" {
		t.Fatalf("affected holdings = %#v, want normalized nested string lists", report.AffectedHoldings)
	}
}

func intFromContextStat(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func TestPortfolioSentinelAgentResultCreatesReviewWithGuardrails(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-review")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-review",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedMonitorQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	model := configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:    svc.agentTaskPool,
		submit:  true,
		summary: "存储链条风险升高",
		result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskHigh,
			"run_summary":        "隔夜海外存储链条大跌，当前持仓需要开盘前降风险。",
			"affected_holdings": []any{
				map[string]any{"symbol": "000977", "market": "SZ", "name": "浪潮信息", "risk_level": "high", "direction": "negative", "reasons": []any{"海外可比资产集体下跌"}},
			},
			"portfolio_actions": []any{
				map[string]any{
					"symbol":         "000977",
					"market":         "SZ",
					"portfolio_id":   portfolio.ID,
					"output_type":    OperationReviewOutputProposedOperation,
					"result_summary": "建议降至 5% 权重",
					"reason":         "海外存储链条风险传导",
					"confidence":     0.72,
					"proposed_operation": map[string]any{
						"action":      "reduce",
						"portfolioId": portfolio.ID,
						"symbol":      "000977",
						"market":      "SZ",
						"quantity":    100,
					},
				},
			},
		},
		confidence: 0.72,
	}
	_ = model
	run, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, portfolio.ID, now.Add(-12*time.Hour), now, "", false)
	if err != nil {
		t.Fatalf("run sentinel: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Result == nil || detail.Result.RiskLevel != PortfolioSentinelRiskHigh {
		t.Fatalf("result = %+v, want high risk", detail.Result)
	}
	if len(detail.Reviews) != 1 || detail.Reviews[0].OutputType != OperationReviewOutputProposedOperation {
		t.Fatalf("reviews = %+v, want proposed operation review", detail.Reviews)
	}
	if guardrails := mapFromAny(detail.Reviews[0].Result["guardrails"]); guardrails["status"] == "" {
		t.Fatalf("guardrails missing in review result: %+v", detail.Reviews[0].Result)
	}
}

func configurePortfolioSentinelModelForTest(t *testing.T, svc *Service) AgentModelProfile {
	t.Helper()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-sentinel",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-sentinel",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	primaryID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypePortfolioSentinel, RequestUpdateAgentTaskProfile{
		PrimaryModelID: &primaryID,
	}); err != nil {
		t.Fatalf("bind portfolio sentinel model: %v", err)
	}
	return model
}

// 无组合(空库)仍要完成一次运行:不进入需要扫描对象的派生逻辑,
// 不生成 Alert/Review,只保存 result(文档 14.1 / 兼容性:空库可用)。
func TestPortfolioSentinelRunWithoutPortfoliosCompletesWithoutDerivedObjects(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:    svc.agentTaskPool,
		submit:  true,
		summary: "无扫描对象",
		result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "当前无组合与持仓,无风险",
			"portfolio_actions":  []any{},
			"affected_holdings":  []any{},
		},
	}
	run, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, "", now.Add(-12*time.Hour), now, "", false)
	if err != nil {
		t.Fatalf("run sentinel without portfolios: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Run.ScannedPortfolioCount != 0 || detail.Run.ScannedHoldingCount != 0 {
		t.Fatalf("scanned = portfolios %d holdings %d, want zeros", detail.Run.ScannedPortfolioCount, detail.Run.ScannedHoldingCount)
	}
	if detail.Run.Status != PortfolioSentinelStatusCompleted {
		t.Fatalf("status = %s, want completed", detail.Run.Status)
	}
	if detail.Result == nil {
		t.Fatalf("result missing; low risk run should still save result")
	}
	if len(detail.Reviews) != 0 || len(detail.Alerts) != 0 || len(detail.Hits) != 0 {
		t.Fatalf("derived objects = reviews %d alerts %d hits %d, want none", len(detail.Reviews), len(detail.Alerts), len(detail.Hits))
	}
}

// 有持仓、无窗口新闻、Agent 低风险且无操作提案:只存 result,
// 不创建 MonitorHit/Review/Alert(文档 9.1 / 14.1)。
func TestPortfolioSentinelLowRiskRunOnlySavesResult(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-low-risk")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-low-risk",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedMonitorQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:   svc.agentTaskPool,
		submit: true,
		result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskLow,
			"run_summary":        "数据面正常,无需操作",
			"portfolio_actions":  []any{},
			"affected_holdings":  []any{},
		},
	}
	run, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, portfolio.ID, now.Add(-12*time.Hour), now, "", false)
	if err != nil {
		t.Fatalf("run low risk sentinel: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Run.ScannedHoldingCount != 1 {
		t.Fatalf("scanned holdings = %d, want 1", detail.Run.ScannedHoldingCount)
	}
	if detail.Result == nil || detail.Result.RiskLevel != PortfolioSentinelRiskLow {
		t.Fatalf("result = %+v, want low risk result", detail.Result)
	}
	if len(detail.Reviews) != 0 || len(detail.Alerts) != 0 {
		t.Fatalf("low risk run should not fan-out reviews/alerts; got reviews %d alerts %d", len(detail.Reviews), len(detail.Alerts))
	}
}

// Agent 回填多个 proposed operation 时,必须 fan-out 成多条单票 OperationReview(文档 9.3 / 14.1)。
func TestPortfolioSentinelMultipleProposedOperationsFanOutReviews(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-fanout")
	for _, h := range []struct {
		id, symbol, market, name string
	}{
		{"holding-fanout-1", "000977", "SZ", "浪潮信息"},
		{"holding-fanout-2", "601138", "SH", "工业富联"},
	} {
		if err := svc.store.CreateHolding(ctx, StockV2Holding{
			ID:                h.id,
			PortfolioID:       portfolio.ID,
			Symbol:            h.symbol,
			Market:            h.market,
			Name:              h.name,
			Quantity:          1000,
			AvailableQuantity: 1000,
			CostPrice:         50,
			AcquiredAt:        now.AddDate(0, 0, -10),
			CreatedAt:         now,
			UpdatedAt:         now,
		}); err != nil {
			t.Fatalf("create holding %s: %v", h.symbol, err)
		}
		seedMonitorQuote(t, svc, h.symbol, 48, -3.2, QuoteStatusFresh, now)
	}
	configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:   svc.agentTaskPool,
		submit: true,
		result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskHigh,
			"run_summary":        "海外存储链条大跌,建议降低同主题暴露",
			"portfolio_actions": []any{
				map[string]any{
					"symbol":         "000977",
					"market":         "SZ",
					"portfolio_id":   portfolio.ID,
					"output_type":    OperationReviewOutputProposedOperation,
					"result_summary": "建议降至 5% 权重",
					"reason":         "海外存储链条风险传导",
					"proposed_operation": map[string]any{
						"action": "reduce", "portfolioId": portfolio.ID, "symbol": "000977", "market": "SZ", "quantity": 100,
					},
				},
				map[string]any{
					"symbol":         "601138",
					"market":         "SH",
					"portfolio_id":   portfolio.ID,
					"output_type":    OperationReviewOutputProposedOperation,
					"result_summary": "建议降低 AI 服务器链条暴露",
					"reason":         "同主题风险传导",
					"proposed_operation": map[string]any{
						"action": "reduce", "portfolioId": portfolio.ID, "symbol": "601138", "market": "SH", "quantity": 100,
					},
				},
			},
		},
	}
	run, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, portfolio.ID, now.Add(-12*time.Hour), now, "", false)
	if err != nil {
		t.Fatalf("run fan-out sentinel: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Reviews) != 2 {
		t.Fatalf("reviews = %d, want 2 fan-out reviews", len(detail.Reviews))
	}
	symbols := map[string]bool{}
	for _, review := range detail.Reviews {
		if review.OutputType != OperationReviewOutputProposedOperation {
			t.Fatalf("review output type = %s, want proposed_operation", review.OutputType)
		}
		symbols[review.Symbol] = true
	}
	if !symbols["000977"] || !symbols["601138"] {
		t.Fatalf("fan-out symbols = %v, want 000977 and 601138 each as single-symbol review", symbols)
	}
}

// proposed operation 命中的标的不在持仓中时,guardrails 应判 blocked:
// Review 仍保存但 status=blocked,且不写交易记录(文档 14.1 / 9.3)。
func TestPortfolioSentinelGuardrailsBlockedReviewHasNoTransaction(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-blocked")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-blocked",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedMonitorQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:   svc.agentTaskPool,
		submit: true,
		result: map[string]any{
			"schema_version":     PortfolioSentinelReportSchemaVersion,
			"overall_risk_level": PortfolioSentinelRiskHigh,
			"run_summary":        "建议减仓一个当前未持有的标的",
			"portfolio_actions": []any{
				map[string]any{
					"symbol":         "000999", // 不在持仓中,触发 holding_empty guardrail
					"market":         "SZ",
					"portfolio_id":   portfolio.ID,
					"output_type":    OperationReviewOutputProposedOperation,
					"result_summary": "建议减仓",
					"reason":         "风险传导",
					"proposed_operation": map[string]any{
						"action": "reduce", "portfolioId": portfolio.ID, "symbol": "000999", "market": "SZ", "quantity": 100,
					},
				},
			},
		},
	}
	run, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, portfolio.ID, now.Add(-12*time.Hour), now, "", false)
	if err != nil {
		t.Fatalf("run blocked sentinel: %v", err)
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, run.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Reviews) != 1 {
		t.Fatalf("reviews = %d, want 1 saved even when guardrails blocked", len(detail.Reviews))
	}
	guardrails, _ := detail.Reviews[0].Result["guardrails"].(map[string]any)
	if status, _ := guardrails["status"].(string); status != ExecutionGuardrailsStatusBlocked {
		t.Fatalf("guardrails status = %v, want blocked", guardrails["status"])
	}
	txs, err := svc.store.ListTransactions(ctx, portfolio.ID, 50)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("blocked review must not write transaction; got %d", len(txs))
	}
}

// Agent 不可用(executor 返回错误)时,run 标 failed,不落 result,不生成伪结论(文档 4.2 / 14.1)。
func TestPortfolioSentinelAgentUnavailableFailsRun(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-sentinel-unavailable")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-sentinel-unavailable",
		PortfolioID:       portfolio.ID,
		Symbol:            "000977",
		Market:            "SZ",
		Name:              "浪潮信息",
		Quantity:          1000,
		AvailableQuantity: 1000,
		CostPrice:         50,
		AcquiredAt:        now.AddDate(0, 0, -10),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	seedMonitorQuote(t, svc, "000977", 48, -3.2, QuoteStatusFresh, now)
	configurePortfolioSentinelModelForTest(t, svc)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:    svc.agentTaskPool,
		execErr: errors.New("agent offline"),
	}
	if _, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, portfolio.ID, now.Add(-12*time.Hour), now, "", false); err != nil {
		t.Fatalf("run unavailable sentinel: %v", err)
	}
	// startPortfolioSentinelRun 同步执行 executor,失败经 finalize 钩子把 sentinel run 标 failed;取 DB 最新状态断言。
	ran, err := svc.store.ListPortfolioSentinelRuns(ctx, PortfolioSentinelRunListFilter{Limit: 5})
	if err != nil || len(ran) != 1 {
		t.Fatalf("list runs: %v (len=%d)", err, len(ran))
	}
	detail, err := svc.GetPortfolioSentinelRunDetail(ctx, ran[0].ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Run.Status != PortfolioSentinelStatusFailed {
		t.Fatalf("status = %s, want failed", detail.Run.Status)
	}
	if detail.Run.ErrorMessage == "" {
		t.Fatalf("failed run should keep error message")
	}
	if detail.Result != nil {
		t.Fatalf("failed run should not save result, got %+v", detail.Result)
	}
	if len(detail.Reviews) != 0 {
		t.Fatalf("failed run should not fan-out reviews, got %d", len(detail.Reviews))
	}
}

// portfolioSentinelScheduledWindow 在盘前/午间/盘后三个 10 分钟 slot 内返回对应窗口,其他时间不触发(文档 5.2 / 12.1)。
func TestPortfolioSentinelScheduledWindowSlots(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load shanghai location: %v", err)
	}
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"pre_market slot start", time.Date(2026, 7, 2, 8, 40, 0, 0, loc), PortfolioSentinelWindowPreMarket},
		{"pre_market slot mid", time.Date(2026, 7, 2, 8, 45, 0, 0, loc), PortfolioSentinelWindowPreMarket},
		{"midday slot", time.Date(2026, 7, 2, 12, 15, 0, 0, loc), PortfolioSentinelWindowMidday},
		{"post_close slot", time.Date(2026, 7, 2, 21, 5, 0, 0, loc), PortfolioSentinelWindowPostClose},
		{"before any slot", time.Date(2026, 7, 2, 7, 0, 0, 0, loc), ""},
		{"after pre_market slot", time.Date(2026, 7, 2, 8, 50, 0, 0, loc), ""},
		{"between slots", time.Date(2026, 7, 2, 15, 0, 0, 0, loc), ""},
	}
	for _, c := range cases {
		got, _, ok := portfolioSentinelScheduledWindow(c.now)
		if c.want == "" {
			if ok {
				t.Fatalf("%s: expected no slot, got %s", c.name, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Fatalf("%s: got ok=%v window=%s, want %s", c.name, ok, got, c.want)
		}
	}
}

// 同一 windowType + 当日 slot 已有 scheduled completed run 时,不应重复触发(文档 12.1 / 14.2)。
func TestPortfolioSentinelSlotAlreadyRanDedupes(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	slotStart := time.Date(2026, 7, 2, 8, 40, 0, 0, loc)
	if _, err := svc.store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		TriggerType:   PortfolioSentinelTriggerScheduled,
		WindowType:    PortfolioSentinelWindowPreMarket,
		Status:        PortfolioSentinelStatusCompleted,
		StartedAt:     slotStart.Add(2 * time.Minute),
		WindowStartAt: slotStart,
		WindowEndAt:   slotStart.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed completed scheduled run: %v", err)
	}
	if !svc.portfolioSentinelSlotAlreadyRan(ctx, PortfolioSentinelWindowPreMarket, slotStart) {
		t.Fatalf("expected pre_market slot already ran")
	}
	if svc.portfolioSentinelSlotAlreadyRan(ctx, PortfolioSentinelWindowMidday, slotStart) {
		t.Fatalf("midday slot should not be considered already ran")
	}
}

// 同一 portfolio/window 已有 running run 时,startPortfolioSentinelRun 返回 ErrPortfolioSentinelAlreadyRunning(文档 12.2)。
func TestPortfolioSentinelHasRunningRunGuardsConcurrency(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	if running, err := svc.store.HasRunningPortfolioSentinelRun(ctx, "", PortfolioSentinelWindowManual); err != nil || running {
		t.Fatalf("expected no running run initially, got running=%v err=%v", running, err)
	}
	if _, err := svc.store.CreatePortfolioSentinelRun(ctx, PortfolioSentinelRun{
		TriggerType:   PortfolioSentinelTriggerManual,
		WindowType:    PortfolioSentinelWindowManual,
		Status:        PortfolioSentinelStatusRunning,
		StartedAt:     now,
		WindowStartAt: now.Add(-time.Hour),
		WindowEndAt:   now,
	}); err != nil {
		t.Fatalf("seed running run: %v", err)
	}
	if running, err := svc.store.HasRunningPortfolioSentinelRun(ctx, "", PortfolioSentinelWindowManual); err != nil || !running {
		t.Fatalf("expected running run to be detected, got running=%v err=%v", running, err)
	}
	_, err := svc.startPortfolioSentinelRun(ctx, PortfolioSentinelTriggerManual, PortfolioSentinelWindowManual, "", now.Add(-time.Hour), now, "", false)
	if !errors.Is(err, ErrPortfolioSentinelAlreadyRunning) {
		t.Fatalf("expected ErrPortfolioSentinelAlreadyRunning, got %v", err)
	}
}
