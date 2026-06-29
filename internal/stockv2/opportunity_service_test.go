package stockv2

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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
		Status:         "active",
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
	if len(candidates) != 1 || candidates[0].Symbol != "300750" || candidates[0].Name != "宁德时代" {
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
		Status:         "active",
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

func seedOpportunityInstrument(t *testing.T, svc *Service, inst StockV2Instrument) {
	t.Helper()
	if inst.Status == "" {
		inst.Status = "active"
	}
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
