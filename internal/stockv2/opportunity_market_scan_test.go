package stockv2

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestOpportunityMarketScanMainBoardBoundary(t *testing.T) {
	tests := []struct {
		item StockV2Instrument
		want bool
	}{
		{StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行"}, true},
		{StockV2Instrument{Symbol: "605001", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "威奥股份"}, true},
		{StockV2Instrument{Symbol: "002230", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "科大讯飞"}, true},
		{StockV2Instrument{Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "宁德时代"}, false},
		{StockV2Instrument{Symbol: "688981", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "中芯国际"}, false},
		{StockV2Instrument{Symbol: "600001", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "ST测试"}, false},
		{StockV2Instrument{Symbol: "600002", Market: "SH", InstrumentType: InstrumentTypeExchangeFund, Name: "测试ETF"}, false},
	}
	for _, tt := range tests {
		if got := isOpportunityMainBoardInstrument(tt.item); got != tt.want {
			t.Errorf("isOpportunityMainBoardInstrument(%s %s)=%v, want %v", tt.item.Symbol, tt.item.Name, got, tt.want)
		}
	}
}

func TestOpportunityMarketScanMetricsAndCoverage(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "main-a", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "主板甲", Industry: "银行"},
		{ID: "main-b", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "主板乙", Industry: "银行"},
		{ID: "growth", Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "创业板", Industry: "电力设备"},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatalf("upsert instrument: %v", err)
		}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	var bars []StockV2DailyBar
	for _, symbol := range []string{"600000", "000001", "300750"} {
		market := inferAStockMarket(symbol)
		for i := 0; i < 65; i++ {
			closePrice := 10 + float64(i)*0.05
			bars = append(bars, StockV2DailyBar{
				ID: generateID(), Symbol: symbol, Market: market,
				TradeDate: start.AddDate(0, 0, i).Format("2006-01-02"),
				Open:      closePrice - .02, High: closePrice + .1, Low: closePrice - .1, Close: closePrice,
				PrevClose: closePrice - .05, Volume: 1000 + float64(i), Amount: 0,
				PctChange: .4, Adjusted: DailyBarAdjustedNone, Source: "unit_test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
			})
		}
	}
	if err := store.UpsertDailyBars(ctx, bars); err != nil {
		t.Fatalf("upsert daily bars: %v", err)
	}
	raw, err := store.marketDB.LoadOpportunityMarketScanMetrics(ctx)
	if err != nil {
		t.Fatalf("load scan metrics: %v", err)
	}
	tradeDate, universe, covered := opportunityMarketScanCoverage(raw)
	if tradeDate != start.AddDate(0, 0, 64).Format("2006-01-02") || universe != 2 || covered != 2 {
		t.Fatalf("coverage=%s %d/%d, want latest and 2/2", tradeDate, covered, universe)
	}
	scored := scoreOpportunityMarketScanMetrics(raw)
	if len(scored) != 2 || !isOpportunityMainBoardInstrument(scored[0].Instrument) || math.IsNaN(scored[0].PrefilterScore) {
		t.Fatalf("scored=%+v, want two finite main-board candidates", scored)
	}
	if scored[0].MedianAmount20 <= 0 {
		t.Fatalf("median amount proxy=%v, want positive value for amount-less Tencent bars", scored[0].MedianAmount20)
	}
}

func TestApplyOpportunityQFQMetricsEstimatesAmountWhenMissing(t *testing.T) {
	bars := make([]StockV2DailyBar, 65)
	for i := range bars {
		bars[i] = StockV2DailyBar{
			TradeDate: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.Local).Format("2006-01-02"),
			Close:     10 + float64(i)/100, Volume: 1000 + float64(i),
		}
	}
	var metrics OpportunityMarketScanMetrics
	applyOpportunityQFQMetrics(&metrics, bars)
	if !metrics.QFQAvailable || metrics.MedianAmount20 <= 0 {
		t.Fatalf("metrics=%+v, want QFQ data with positive amount proxy", metrics)
	}
}

func TestOpportunityMarketScanRepositoryDefaultsAndCandidates(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	config, err := store.GetOpportunityMarketScanConfig(ctx)
	if err != nil || config.Enabled {
		t.Fatalf("default config=%+v err=%v, want disabled", config, err)
	}
	run, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{TriggerType: OpportunityMarketScanTriggerManual})
	if err != nil {
		t.Fatalf("create scan run: %v", err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{{
		ID: "candidate-a", ScanRunID: run.ID, Symbol: "600000", Market: "SH", Name: "浦发银行",
		Stage: OpportunityMarketScanCandidateResearch, PrefilterScore: 70, FinalScore: 75,
		Metrics: OpportunityMarketScanMetrics{TradeDate: "2026-08-10", QFQAvailable: true},
	}}); err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	items, err := store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Limit: 10})
	if err != nil || len(items) != 1 || !items[0].Metrics.QFQAvailable {
		t.Fatalf("candidates=%+v err=%v", items, err)
	}
}

func TestOpportunityMarketScanDiscoveryRejectsSymbolOutsideBoundedSet(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "allowed", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "允许标的"},
		{ID: "outside", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "越界标的"},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatalf("upsert instrument: %v", err)
		}
	}
	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "市场扫描", UserThesis: "验证候选边界", CreatedBy: OpportunityMarketScanCreatedBy})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	discovery, _, err := store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{OpportunityID: opp.ID}, nil)
	if err != nil {
		t.Fatalf("create discovery: %v", err)
	}
	scanRun, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerManual, Status: OpportunityMarketScanStatusResearching,
		OpportunityID: opp.ID, DiscoveryRunID: discovery.ID,
	})
	if err != nil {
		t.Fatalf("create scan run: %v", err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{{
		ID: "allowed-scan", ScanRunID: scanRun.ID, Symbol: "600000", Market: "SH", Name: "允许标的",
		Stage: OpportunityMarketScanCandidateResearch,
	}}); err != nil {
		t.Fatalf("upsert allowed scan candidate: %v", err)
	}
	_, _, err = svc.opportunityDiscoveryReportFromResult(ctx, discovery, map[string]any{
		"schema_version": OpportunityDiscoveryReportSchemaVersion,
		"opportunity_id": opp.ID,
		"summary":        "越界测试",
		"candidates": []any{map[string]any{
			"symbol": "000001", "relevance_score": 80.0, "evidence_score": 70.0,
			"market_risk_score": 30.0, "confidence": .7,
		}},
	})
	if !errors.Is(err, ErrInvalidOpportunityResult) {
		t.Fatalf("error=%v, want ErrInvalidOpportunityResult", err)
	}
}

func TestOpportunityMarketScanResearchFailureSchedulesDurableRetry(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "市场扫描", UserThesis: "重试测试"})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	discovery, _, err := store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{
		OpportunityID: opp.ID, Status: OpportunityDiscoveryRunStatusFailed, ErrorMessage: "provider timeout",
	}, nil)
	if err != nil {
		t.Fatalf("create failed discovery: %v", err)
	}
	run, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerManual, Status: OpportunityMarketScanStatusResearching,
		OpportunityID: opp.ID, DiscoveryRunID: discovery.ID,
	})
	if err != nil {
		t.Fatalf("create scan run: %v", err)
	}
	svc.advanceOpportunityMarketResearch(ctx, run)
	got, err := store.GetOpportunityMarketScanRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get scan run: %v", err)
	}
	if got.Status != OpportunityMarketScanStatusResearching || got.RetryCount != 1 || got.NextRetryAt.IsZero() || got.ErrorMessage != discovery.ErrorMessage {
		t.Fatalf("retry state=%+v, want researching retry 1 with durable deadline", got)
	}
}

func TestOpportunityMarketScanStrategyBatchKeepsBoundedCandidateContext(t *testing.T) {
	input, err := normalizeStrategyGenerationInput(StrategyGenerationInput{
		Mode:          StrategyGenerationModeOpportunity,
		OpportunityID: "opp-1",
		CandidateID:   "candidate-a",
		CandidateIDs:  []string{"candidate-a", "candidate-b", "candidate-c", "candidate-d"},
		TargetInstruments: []StrategyGenerationTargetInstrument{
			{Symbol: "600000", Market: "SH"},
			{Symbol: "000001", Market: "SZ"},
			{Symbol: "002230", Market: "SZ"},
		},
	})
	if err != nil {
		t.Fatalf("normalize opportunity strategy batch: %v", err)
	}
	if len(input.CandidateIDs) != opportunityMarketScanStrategyLimit || input.CandidateID != "candidate-a" {
		t.Fatalf("candidate context=%+v legacy=%q, want bounded batch with stable first candidate", input.CandidateIDs, input.CandidateID)
	}
	want := []string{"candidate-a", "candidate-b", "candidate-c"}
	parsed := strategyGenerationCandidateIDsFromTrigger(strategyGenerationTriggerID(input))
	if len(parsed) != len(want) {
		t.Fatalf("parsed candidate ids=%+v, want %+v", parsed, want)
	}
	for i := range want {
		if parsed[i] != want[i] {
			t.Fatalf("parsed candidate ids=%+v, want %+v", parsed, want)
		}
	}
	if err := validateStrategyGenerationDraftTargets(strategyGenerationTriggerID(input), []StrategyGenerationDraft{
		{DraftType: StrategyGenerationDraftTypeNewStrategy, Symbol: "600000"},
		{DraftType: StrategyGenerationDraftTypeNewStrategy, Symbol: "600999"},
	}); !errors.Is(err, ErrInvalidStrategyGenerationResult) {
		t.Fatalf("out-of-scan strategy target error=%v, want ErrInvalidStrategyGenerationResult", err)
	}
}

func TestOpportunityMarketScanStrategyStepPromptKeepsAllBatchTargets(t *testing.T) {
	genCtx := StrategyGenerationContext{
		Input:                          StrategyGenerationInput{Mode: StrategyGenerationModeOpportunity},
		OpportunityEvidenceByCandidate: map[string][]OpportunityEvidence{},
	}
	for _, symbol := range []string{"600000", "000001", "002230"} {
		candidateID := "candidate-" + symbol
		genCtx.Input.CandidateIDs = append(genCtx.Input.CandidateIDs, candidateID)
		genCtx.Input.TargetInstruments = append(genCtx.Input.TargetInstruments, StrategyGenerationTargetInstrument{Symbol: symbol})
		genCtx.OpportunityCandidates = append(genCtx.OpportunityCandidates, OpportunityCandidate{
			ID: candidateID, Symbol: symbol, Name: "候选", Reason: strings.Repeat("候选理由", 100),
		})
		for evidenceIndex := 0; evidenceIndex < 10; evidenceIndex++ {
			genCtx.OpportunityEvidenceByCandidate[candidateID] = append(genCtx.OpportunityEvidenceByCandidate[candidateID], OpportunityEvidence{
				ID: candidateID + "-evidence", CandidateID: candidateID, SourceType: OpportunityEvidenceSourceExternal,
				Title: strings.Repeat("证据标题", 60), Summary: strings.Repeat("证据摘要", 100),
			})
		}
	}
	prompt := buildStrategyGenerationStepPrompt("task-batch", StrategyGenerationStepPack{
		RunID: "run-1", StepKey: StrategyGenerationStepFormatter, Role: "formatter",
		Objective: "生成有界策略草案", Context: genCtx,
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	for _, expected := range []string{"600000", "000001", "002230", "candidate-600000", "candidate-002230"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("batched strategy prompt missing %q", expected)
		}
	}
	if strings.Contains(prompt, promptTruncatedMarker) {
		t.Fatalf("bounded strategy prompt unexpectedly truncated at %d bytes", len(prompt))
	}
}
