package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOpportunityDiscoveryThemeChainAcceptsStructuredAndLegacyItems(t *testing.T) {
	for _, raw := range []string{
		`{"theme_chain":[{"layer":"上游稀缺供给","rank":1,"representatives":["600000"],"scarcity":"扩产周期长"}]}`,
		`{"theme_chain":["上游稀缺供给"]}`,
	} {
		var report OpportunityDiscoveryReport
		if err := json.Unmarshal([]byte(raw), &report); err != nil {
			t.Fatalf("decode theme chain %s: %v", raw, err)
		}
		if len(report.ThemeChain) != 1 || report.ThemeChain[0].Layer != "上游稀缺供给" {
			t.Fatalf("theme chain=%+v, want one normalized item", report.ThemeChain)
		}
	}

	var report OpportunityDiscoveryReport
	if err := json.Unmarshal([]byte(`{"theme_chain":[{"rank":1}]}`), &report); err == nil {
		t.Fatal("theme chain without layer should fail")
	}
}

func TestValidExternalOpportunityURLRejectsTruncatedOrNonHTTPURLs(t *testing.T) {
	for _, raw := range []string{
		"https://disc.static.szse.cn/finalpage/report.PDF",
		"http://example.com/report.pdf",
	} {
		if !validExternalOpportunityURL(raw) {
			t.Fatalf("validExternalOpportunityURL(%q) = false, want true", raw)
		}
	}
	for _, raw := range []string{
		"https://disc.static.szse.cn/finalpage/cad9...4cdc.PDF",
		"https://example.com/report…pdf",
		"ftp://example.com/report.pdf",
		"/relative/report.pdf",
		"",
	} {
		if validExternalOpportunityURL(raw) {
			t.Fatalf("validExternalOpportunityURL(%q) = true, want false", raw)
		}
	}
}

func TestOpportunityServiceSubmitResultValidatesCandidatesAndStrategyEntry(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()

	seedOpportunityInstrument(t, svc, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Industry:       "电力设备",
		Sector:         "新能源",
	})
	model := seedOpportunityChatModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity model: %v", err)
	}

	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{
		Title:      "AI model release",
		UserThesis: "new AI model may benefit battery and compute supply chains",
		CreatedBy:  "tester",
	})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	run, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{RequestedBy: "tester"})
	if err != nil {
		t.Fatalf("start opportunity discovery: %v", err)
	}
	if run.AgentRunID == "" || run.StepTotal != len(defaultOpportunityDiscoverySteps) || run.Status != OpportunityDiscoveryRunStatusPending {
		t.Fatalf("run=%+v, want pending run with agent record and default steps", run)
	}
	if _, err := svc.RecordOpportunityCandidate(ctx, OpportunityCandidate{
		RunID:           run.ID,
		Symbol:          "999999",
		RelationType:    OpportunityRelationDirect,
		RelevanceScore:  70,
		EvidenceScore:   70,
		MarketRiskScore: 30,
		Confidence:      0.7,
	}); !errors.Is(err, ErrOpportunitySymbolNotFound) {
		t.Fatalf("record invalid candidate error=%v, want ErrOpportunitySymbolNotFound", err)
	}

	result, err := svc.ProcessOpportunityDiscoverySubmittedResult(ctx, run.ID, AgentTaskSubmittedResult{
		OutputType:    OpportunityDiscoveryOutputType,
		ResultSummary: "found one valid candidate",
		Confidence:    0.8,
		Result: map[string]any{
			"schema_version":          OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id":          opp.ID,
			"summary":                 "The AI theme has one immediate local candidate.",
			"conclusion":              "Generate a watch strategy for the highest-confidence supplier.",
			"recommended_next_action": "generate_strategy",
			"candidates": []any{
				map[string]any{
					"symbol":             "300750",
					"relation_type":      OpportunityRelationDirect,
					"rank":               1,
					"relevance_score":    88.0,
					"evidence_score":     76.0,
					"market_risk_score":  42.0,
					"confidence":         0.81,
					"reason":             "battery and energy storage exposure",
					"risk_summary":       "theme may be indirect",
					"suggested_strategy": "watch pullbacks",
					"horizon_outlooks":   testModelHorizonOutlooks(200),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("process submit result: %v", err)
	}
	if result.ID == "" || result.Summary == "" {
		t.Fatalf("result=%+v, want persisted summary", result)
	}
	candidates, err := svc.ListOpportunityCandidates(ctx, OpportunityCandidateListFilter{RunID: run.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Symbol != "300750" || candidates[0].Name != "宁德时代" || len(candidates[0].HorizonOutlooks) != 3 {
		t.Fatalf("candidates=%+v, want validated master-data candidate", candidates)
	}
	finalRun, err := svc.GetOpportunityDiscoveryRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get final run: %v", err)
	}
	if finalRun.Status != OpportunityDiscoveryRunStatusCompleted || finalRun.CandidateCount != 1 {
		t.Fatalf("final run=%+v, want completed with candidate count", finalRun)
	}
	finalOpp, err := svc.GetOpportunity(ctx, opp.ID)
	if err != nil || finalOpp.Status != OpportunityStatusCompleted {
		t.Fatalf("final opportunity=%+v err=%v, want completed", finalOpp, err)
	}

	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStrategyGeneration, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind strategy generation model: %v", err)
	}
	strategyRun, err := svc.GenerateStrategyFromOpportunityCandidate(ctx, candidates[0].ID, RequestGenerateStrategyFromOpportunityCandidate{
		RequestedBy: "tester",
		UserGoal:    "turn this candidate into a quiet watch strategy",
	})
	if err != nil {
		t.Fatalf("generate strategy from candidate: %v", err)
	}
	if strategyRun.TaskType != AgentTaskTypeStrategyGeneration || strategyRun.Status != AgentRunStatusReady {
		t.Fatalf("strategy run=%+v, want ready strategy_generation run", strategyRun)
	}
	updatedCandidate, err := svc.GetOpportunityCandidate(ctx, candidates[0].ID)
	if err != nil || updatedCandidate.Status != OpportunityCandidateStatusStrategyRequested {
		t.Fatalf("updated candidate=%+v err=%v, want strategy_requested", updatedCandidate, err)
	}
}

func TestOpportunityServiceRejectsUnsafeAndMalformedSubmittedResults(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()

	seedOpportunityInstrument(t, svc, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
	})
	model := seedOpportunityChatModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity model: %v", err)
	}
	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "AI event", UserThesis: "theme"})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	run, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if _, err := svc.ProcessOpportunityDiscoverySubmittedResult(ctx, run.ID, AgentTaskSubmittedResult{
		OutputType: OpportunityDiscoveryOutputType,
		Result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": opp.ID,
			"summary":        "unsafe",
			"order":          map[string]any{"symbol": "300750", "side": "buy"},
			"candidates":     []any{},
		},
	}); !errors.Is(err, ErrOpportunityUnsafeResult) {
		t.Fatalf("unsafe result error=%v, want ErrOpportunityUnsafeResult", err)
	}

	run2, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	if _, err := svc.ProcessOpportunityDiscoverySubmittedResult(ctx, run2.ID, AgentTaskSubmittedResult{
		OutputType: OpportunityDiscoveryOutputType,
		Result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": "wrong-opportunity",
			"summary":        "wrong id",
			"candidates":     []any{},
		},
	}); !errors.Is(err, ErrInvalidOpportunityResult) {
		t.Fatalf("wrong opportunity result error=%v, want ErrInvalidOpportunityResult", err)
	}

	run3, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{})
	if err != nil {
		t.Fatalf("start third run: %v", err)
	}
	if _, err := svc.ProcessOpportunityDiscoverySubmittedResult(ctx, run3.ID, AgentTaskSubmittedResult{
		OutputType: OpportunityDiscoveryOutputType,
		Result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": opp.ID,
			"summary":        "semantic recall without trace",
			"candidates": []any{
				map[string]any{
					"symbol":            "300750",
					"relation_type":     OpportunityRelationDirect,
					"relevance_score":   90,
					"evidence_score":    80,
					"market_risk_score": 30,
					"confidence":        0.8,
					"recall_method":     "semantic_vector",
				},
			},
		},
	}); !errors.Is(err, ErrInvalidOpportunityResult) {
		t.Fatalf("semantic trace error=%v, want ErrInvalidOpportunityResult", err)
	}

	run4, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{})
	if err != nil {
		t.Fatalf("start fourth run: %v", err)
	}
	if _, err := svc.ProcessOpportunityDiscoverySubmittedResult(ctx, run4.ID, AgentTaskSubmittedResult{
		OutputType: OpportunityDiscoveryOutputType,
		Result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": opp.ID,
			"summary":        "unknown symbol",
			"candidates": []any{
				map[string]any{
					"symbol":            "999999",
					"relation_type":     OpportunityRelationDirect,
					"relevance_score":   90,
					"evidence_score":    80,
					"market_risk_score": 30,
					"confidence":        0.8,
					"horizon_outlooks":  testModelHorizonOutlooks(10),
				},
			},
		},
	}); !errors.Is(err, ErrOpportunitySymbolNotFound) {
		t.Fatalf("unknown symbol error=%v, want ErrOpportunitySymbolNotFound", err)
	}
	failedRun, err := svc.GetOpportunityDiscoveryRun(ctx, run4.ID)
	if err != nil {
		t.Fatalf("get failed run: %v", err)
	}
	if failedRun.Status != OpportunityDiscoveryRunStatusFailed || !strings.Contains(failedRun.ErrorMessage, "candidate symbol not found") {
		t.Fatalf("failed run=%+v, want failed status with candidate symbol context", failedRun)
	}
}

func TestOpportunityDiscoveryScopeExcludesChiNextAndStarStocksAcrossRunAndStrategy(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()

	for _, inst := range []StockV2Instrument{
		{ID: "main", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "主板股票"},
		{ID: "chinext", Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "创业板股票"},
		{ID: "star", Symbol: "688981", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "科创板股票"},
		{ID: "etf", Symbol: "159915", Market: "SZ", InstrumentType: InstrumentTypeExchangeFund, Name: "创业板ETF"},
	} {
		seedOpportunityInstrument(t, svc, inst)
	}
	model := seedOpportunityChatModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity model: %v", err)
	}
	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{
		Title: "A 股主题", UserThesis: "验证机会范围", MarketScope: OpportunityMarketScopeAShare, InstrumentScope: OpportunityInstrumentScopeBoth,
	})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}

	legacyRun, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{})
	if err != nil {
		t.Fatalf("start legacy-scope run: %v", err)
	}
	legacyCandidate, err := svc.RecordOpportunityCandidate(ctx, validOpportunityCandidateForTest(legacyRun.ID, "300750"))
	if err != nil {
		t.Fatalf("record candidate while exclusion is disabled: %v", err)
	}
	exclude := true
	if _, err := svc.UpdateOpportunityDiscoveryConfig(ctx, RequestUpdateOpportunityDiscoveryConfig{ExcludeChiNextAndStarMarket: &exclude}); err != nil {
		t.Fatalf("enable opportunity scope exclusion: %v", err)
	}
	run, err := svc.StartOpportunityDiscoveryRun(ctx, opp.ID, RequestStartOpportunityDiscoveryRun{})
	if err != nil {
		t.Fatalf("start excluded-scope run: %v", err)
	}
	if !run.ExcludeChiNextAndStarMarket {
		t.Fatalf("run=%+v, want exclusion snapshot", run)
	}
	if _, err := svc.RecordOpportunityCandidate(ctx, validOpportunityCandidateForTest(run.ID, "688981")); !errors.Is(err, ErrOpportunityCandidateOutOfScope) {
		t.Fatalf("record STAR candidate error=%v, want out of scope", err)
	}
	if _, err := svc.RecordOpportunityCandidate(ctx, validOpportunityCandidateForTest(run.ID, "600000")); err != nil {
		t.Fatalf("record main-board candidate: %v", err)
	}
	if _, err := svc.RecordOpportunityCandidate(ctx, validOpportunityCandidateForTest(run.ID, "159915")); err != nil {
		t.Fatalf("record ETF candidate: %v", err)
	}
	if _, err := svc.GenerateStrategyFromOpportunityCandidate(ctx, legacyCandidate.ID, RequestGenerateStrategyFromOpportunityCandidate{}); !errors.Is(err, ErrOpportunityCandidateOutOfScope) {
		t.Fatalf("generate strategy from excluded historical candidate error=%v, want out of scope", err)
	}

	_, _, err = svc.opportunityDiscoveryReportFromResult(ctx, run, map[string]any{
		"schema_version": OpportunityDiscoveryReportSchemaVersion,
		"opportunity_id": opp.ID,
		"summary":        "不应接受双创候选",
		"candidates": []any{map[string]any{
			"symbol": "300750", "relation_type": OpportunityRelationDirect,
			"relevance_score": 80, "evidence_score": 70, "market_risk_score": 40,
			"confidence": 0.7, "horizon_outlooks": testModelHorizonOutlooks(100),
		}},
	})
	if !errors.Is(err, ErrInvalidOpportunityResult) || !strings.Contains(err.Error(), "outside the configured discovery scope") {
		t.Fatalf("final report error=%v, want out-of-scope invalid result", err)
	}
}

func TestIsChiNextOrStarMarketStock(t *testing.T) {
	tests := []struct {
		inst StockV2Instrument
		want bool
	}{
		{StockV2Instrument{Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock}, true},
		{StockV2Instrument{Symbol: "301230", Market: "SZ", InstrumentType: InstrumentTypeStock}, true},
		{StockV2Instrument{Symbol: "688981", Market: "SH", InstrumentType: InstrumentTypeStock}, true},
		{StockV2Instrument{Symbol: "689009", Market: "SH", InstrumentType: InstrumentTypeStock}, true},
		{StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock}, false},
		{StockV2Instrument{Symbol: "159915", Market: "SZ", InstrumentType: InstrumentTypeExchangeFund}, false},
		{StockV2Instrument{Symbol: "300750", Market: "HK", InstrumentType: InstrumentTypeStock}, false},
	}
	for _, tt := range tests {
		if got := isChiNextOrStarMarketStock(tt.inst); got != tt.want {
			t.Fatalf("isChiNextOrStarMarketStock(%+v)=%v, want %v", tt.inst, got, tt.want)
		}
	}
}

func validOpportunityCandidateForTest(runID, symbol string) OpportunityCandidate {
	return OpportunityCandidate{
		RunID: runID, Symbol: symbol, RelationType: OpportunityRelationDirect,
		RelevanceScore: 80, EvidenceScore: 70, MarketRiskScore: 40, Confidence: 0.7,
	}
}

func seedOpportunityInstrument(t *testing.T, svc *Service, inst StockV2Instrument) {
	t.Helper()
	if err := svc.store.UpsertInstrument(context.Background(), inst); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
}

func seedOpportunityChatModel(t *testing.T, svc *Service, ctx context.Context) AgentModelProfile {
	t.Helper()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeCodexCLI,
		Name:         "codex-opportunity",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-opportunity",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	return model
}
