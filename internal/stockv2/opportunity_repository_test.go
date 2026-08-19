package stockv2

import (
	"context"
	"testing"
)

func TestOpportunityRepositoryPersistsDiscoveryObjectsAndEmbeddingAssets(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	opp, err := store.CreateOpportunity(ctx, Opportunity{
		Title:           "AI model release",
		UserThesis:      "new AI model may benefit local compute suppliers",
		MarketScope:     OpportunityMarketScopeAShare,
		InstrumentScope: OpportunityInstrumentScopeBoth,
		Status:          OpportunityStatusDraft,
		CreatedBy:       "tester",
	})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	if opp.ID == "" || opp.CreatedAt.IsZero() {
		t.Fatalf("opportunity = %+v, want id and timestamps", opp)
	}

	run, steps, err := store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{
		OpportunityID: opp.ID,
		AgentRunID:    "agent-run-1",
		Status:        OpportunityDiscoveryRunStatusPending,
	}, []OpportunityDiscoveryStep{{
		StepKey:    "scope",
		StepTitle:  "Scope theme",
		Status:     OpportunityDiscoveryStepStatusPending,
		OrderIndex: 1,
	}})
	if err != nil {
		t.Fatalf("create discovery run: %v", err)
	}
	if run.ID == "" || run.StepTotal != 1 || len(steps) != 1 {
		t.Fatalf("run=%+v steps=%+v, want one seeded step", run, steps)
	}

	candidate, err := store.UpsertOpportunityCandidate(ctx, OpportunityCandidate{
		OpportunityID:   opp.ID,
		RunID:           run.ID,
		Symbol:          "300750",
		Market:          "SZ",
		InstrumentType:  InstrumentTypeStock,
		Name:            "宁德时代",
		RelationType:    OpportunityRelationDirect,
		RelevanceScore:  91,
		EvidenceScore:   82,
		MarketRiskScore: 35,
		Confidence:      0.72,
		Rank:            1,
		Status:          OpportunityCandidateStatusCandidate,
		Reason:          "battery supply chain",
	})
	if err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	evidence, err := store.CreateOpportunityEvidence(ctx, OpportunityEvidence{
		RunID:       run.ID,
		CandidateID: candidate.ID,
		SourceType:  OpportunityEvidenceSourceExternal,
		Title:       "external article",
		URL:         "https://example.com/news?id=secret",
		Confidence:  0.8,
	})
	if err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	result, err := store.UpsertOpportunityResult(ctx, OpportunityResult{
		RunID:                 run.ID,
		Summary:               "one candidate",
		Conclusion:            "watch the chain",
		RecommendedNextAction: "generate strategy",
		RawResult: map[string]any{
			"schema_version": OpportunityDiscoveryReportSchemaVersion,
		},
	})
	if err != nil {
		t.Fatalf("upsert result: %v", err)
	}

	candidates, err := store.ListOpportunityCandidates(ctx, OpportunityCandidateListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(candidates) != 1 || candidates[0].ID != candidate.ID {
		t.Fatalf("candidates=%+v err=%v, want saved candidate", candidates, err)
	}
	evidenceItems, err := store.ListOpportunityEvidence(ctx, OpportunityEvidenceListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(evidenceItems) != 1 || evidenceItems[0].ID != evidence.ID {
		t.Fatalf("evidence=%+v err=%v, want saved evidence", evidenceItems, err)
	}
	gotResult, err := store.GetOpportunityResultByRunID(ctx, run.ID)
	if err != nil || gotResult.ID != result.ID {
		t.Fatalf("result=%+v err=%v, want saved result", gotResult, err)
	}
	total, err := store.CountOpportunities(ctx, OpportunityListFilter{Keyword: "AI"})
	if err != nil || total != 1 {
		t.Fatalf("count opportunities = %d err=%v, want 1", total, err)
	}

	cfg, err := store.UpsertEmbeddingConfig(ctx, EmbeddingConfig{
		EmbeddingModelID: "embedding-model-1",
		Enabled:          true,
		LastProbeStatus:  EmbeddingStatusReady,
	})
	if err != nil || !cfg.Enabled || cfg.EmbeddingModelID != "embedding-model-1" {
		t.Fatalf("embedding config=%+v err=%v", cfg, err)
	}
	asset, err := store.UpsertEmbeddingAsset(ctx, EmbeddingAsset{
		ObjectType:          EmbeddingObjectStockProfile,
		ObjectID:            "300750",
		TextHash:            "hash-1",
		ModelID:             "embedding-model-1",
		EmbeddingDimensions: 3,
		VectorRef:           "vector-300750",
		Status:              EmbeddingAssetStatusReady,
	})
	if err != nil {
		t.Fatalf("upsert embedding asset: %v", err)
	}
	if err := store.UpsertEmbeddingVector(ctx, asset, []float64{0.1, 0.2, 0.3}); err != nil {
		t.Fatalf("upsert embedding vector: %v", err)
	}
	hits, err := store.SearchEmbeddingVectors(ctx, "embedding-model-1", EmbeddingObjectStockProfile, []float64{0.1, 0.2, 0.3}, 5)
	if err != nil {
		t.Fatalf("search embedding vectors: %v", err)
	}
	if len(hits) != 1 || hits[0].VectorRef != "vector-300750" {
		t.Fatalf("hits=%+v, want vector-300750", hits)
	}
	secondAsset, err := store.UpsertEmbeddingAsset(ctx, EmbeddingAsset{
		ObjectType:          EmbeddingObjectStockProfile,
		ObjectID:            "600519",
		TextHash:            "hash-2",
		ModelID:             "embedding-model-1",
		EmbeddingDimensions: 3,
		VectorRef:           "vector-600519",
		Status:              EmbeddingAssetStatusReady,
	})
	if err != nil {
		t.Fatalf("upsert second embedding asset: %v", err)
	}
	if err := store.UpsertEmbeddingVector(ctx, secondAsset, []float64{0.3, 0.2, 0.1}); err != nil {
		t.Fatalf("upsert second embedding vector: %v", err)
	}
	batchHits, err := store.SearchEmbeddingVectorsForObjectsBatch(ctx, "embedding-model-1", EmbeddingObjectStockProfile,
		map[string]struct{}{"300750": {}, "600519": {}}, [][]float64{{0.1, 0.2, 0.3}, {0.3, 0.2, 0.1}}, 1)
	if err != nil {
		t.Fatalf("batch search embedding vectors: %v", err)
	}
	if len(batchHits) != 2 || len(batchHits[0]) != 1 || len(batchHits[1]) != 1 ||
		batchHits[0][0].VectorRef != "vector-300750" || batchHits[1][0].VectorRef != "vector-600519" {
		t.Fatalf("batch hits=%+v, want each matching vector", batchHits)
	}
}
