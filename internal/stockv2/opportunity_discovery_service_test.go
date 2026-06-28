package stockv2

import (
	"context"
	"strings"
	"testing"
)

func TestBuildOpportunityDiscoveryPromptRequiresSearchMCPTraceAndSubmit(t *testing.T) {
	prompt := buildOpportunityDiscoveryPrompt("task-test", OpportunityDiscoveryContext{
		Opportunity: Opportunity{
			ID:         "opp-test",
			Title:      "字节跳动新 AI 模型很好",
			UserThesis: "AI 模型升级带动相关机会",
		},
		DiscoveryRun: OpportunityDiscoveryRun{ID: "disc-run-test", OpportunityID: "opp-test"},
	}, "http://127.0.0.1:1234/api/stockv2/agent/mcp")
	for _, want := range []string{
		"Codex CLI's own public search/browse capability",
		"stock_agent.start_discovery_step",
		"stock_agent.record_external_source",
		"stock_agent.record_candidate",
		"stock_agent.get_embedding_status",
		"stock_agent.submit_result",
		"opportunity-discovery-report/v1",
		"Do not implement or request web_search/web_fetch MCP tools",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRunOpportunityDiscoverySavesValidatedCandidate(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	seedStrategyGenerationInstrument(t, svc, ctx, "300750")
	model := seedStrategyGenerationModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity discovery model: %v", err)
	}
	opp, err := svc.store.CreateOpportunity(ctx, Opportunity{
		Title:      "AI 主题机会",
		UserThesis: "新模型带动产业链机会",
	})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "发现候选",
		confidence: 0.8,
		result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": opp.ID,
			"summary":        "发现 1 个候选",
			"candidates": []any{map[string]any{
				"symbol":                    "300750",
				"relation_type":             "supply_chain",
				"rank":                      1,
				"relevance_score":           90,
				"evidence_score":            75,
				"market_risk_score":         40,
				"confidence":                0.71,
				"reason":                    "与 AI 算力链相关",
				"risk_summary":              "波动较大",
				"suggested_strategy_intent": "后续生成观察型策略",
			}},
		},
	}

	discoveryRun, err := svc.RunOpportunityDiscovery(ctx, OpportunityDiscoveryInput{OpportunityID: opp.ID})
	if err != nil {
		t.Fatalf("run opportunity discovery: %v", err)
	}
	if discoveryRun.Status != OpportunityDiscoveryRunStatusCompleted {
		t.Fatalf("discovery run status = %s, want completed", discoveryRun.Status)
	}
	candidates, err := svc.store.ListOpportunityCandidates(ctx, discoveryRun.ID)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Symbol != "300750" || candidates[0].Rank != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].RelevanceScore != 90 || candidates[0].Confidence != 0.71 {
		t.Fatalf("candidate scores = %+v", candidates[0])
	}
	oppAfter, err := svc.store.GetOpportunity(ctx, opp.ID)
	if err != nil {
		t.Fatalf("get opportunity: %v", err)
	}
	if oppAfter.Status != OpportunityStatusCompleted {
		t.Fatalf("opportunity status = %s, want completed", oppAfter.Status)
	}
}

func TestRunOpportunityDiscoveryRejectsUnknownCandidateSymbol(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := seedStrategyGenerationModel(t, svc, ctx)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOpportunityDiscovery, RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity discovery model: %v", err)
	}
	opp, err := svc.store.CreateOpportunity(ctx, Opportunity{Title: "AI 主题机会"})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "发现候选",
		confidence: 0.8,
		result: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
			"opportunity_id": opp.ID,
			"summary":        "包含无效候选",
			"candidates": []any{map[string]any{
				"symbol":            "999999",
				"rank":              1,
				"relevance_score":   90,
				"evidence_score":    75,
				"market_risk_score": 40,
				"confidence":        0.71,
			}},
		},
	}

	discoveryRun, err := svc.RunOpportunityDiscovery(ctx, OpportunityDiscoveryInput{OpportunityID: opp.ID})
	if err != nil {
		t.Fatalf("run opportunity discovery should return failed run detail without bubbling fake executor error: %v", err)
	}
	if discoveryRun.Status != OpportunityDiscoveryRunStatusFailed {
		t.Fatalf("discovery run status = %s, want failed", discoveryRun.Status)
	}
	candidates, err := svc.store.ListOpportunityCandidates(ctx, discoveryRun.ID)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("invalid symbol should not be saved, got %+v", candidates)
	}
}

func TestRunAgentCLIDebugOpportunityDiscoveryMode(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	model := seedStrategyGenerationModel(t, svc, ctx)
	svc.agentExecutor = fakeDebugAgentExecutor{pool: svc.agentTaskPool}

	detail, err := svc.RunAgentCLIDebug(ctx, RequestRunAgentCLIDebug{
		ModelID:   model.ID,
		DebugMode: AgentTaskTypeOpportunityDiscovery,
	})
	if err != nil {
		t.Fatalf("run debug: %v", err)
	}
	if detail.Run.TaskType != AgentTaskTypeOpportunityDiscovery || detail.Run.Status != AgentRunStatusCompleted {
		t.Fatalf("debug run = %+v", detail.Run)
	}
	if detail.Ledger == nil || detail.Ledger.StructuredOutput["outputType"] != OpportunityDiscoveryOutputType {
		t.Fatalf("ledger = %+v", detail.Ledger)
	}
}
