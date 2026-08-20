package stockv2

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpportunityDiscoveryScopeSchemaMigratesExistingRunTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stockv2.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE stockv2_opportunity_discovery_runs (
		id TEXT PRIMARY KEY, opportunity_id TEXT NOT NULL, agent_run_id TEXT, status TEXT NOT NULL,
		current_step_id TEXT, step_total INTEGER NOT NULL DEFAULT 0, step_completed INTEGER NOT NULL DEFAULT 0,
		candidate_count INTEGER NOT NULL DEFAULT 0, evidence_count INTEGER NOT NULL DEFAULT 0,
		external_source_count INTEGER NOT NULL DEFAULT 0, started_at DATETIME, finished_at DATETIME,
		error_message TEXT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create legacy discovery run table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStoreWithMarketDB(dbPath, filepath.Join(dir, "market.duckdb"))
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer store.Close()
	rows, err := store.db.Query(`PRAGMA table_info(stockv2_opportunity_discovery_runs)`)
	if err != nil {
		t.Fatalf("inspect migrated run table: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan migrated run table: %v", err)
		}
		found = found || name == "exclude_chi_next_and_star_market"
	}
	if !found {
		t.Fatal("migrated discovery run table is missing scope snapshot column")
	}
	config, err := store.GetOpportunityDiscoveryConfig(context.Background())
	if err != nil || config.ExcludeChiNextAndStarMarket {
		t.Fatalf("migrated config=%+v err=%v, want disabled default", config, err)
	}
}

func TestOpportunityRepositoryPersistsDiscoveryObjectsAndEmbeddingAssets(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config, err := store.GetOpportunityDiscoveryConfig(ctx)
	if err != nil || config.ExcludeChiNextAndStarMarket {
		t.Fatalf("default opportunity discovery config=%+v err=%v, want exclusion disabled", config, err)
	}
	config.ExcludeChiNextAndStarMarket = true
	config, err = store.SaveOpportunityDiscoveryConfig(ctx, config)
	if err != nil || !config.ExcludeChiNextAndStarMarket {
		t.Fatalf("saved opportunity discovery config=%+v err=%v, want exclusion enabled", config, err)
	}

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
		OpportunityID:               opp.ID,
		AgentRunID:                  "agent-run-1",
		Status:                      OpportunityDiscoveryRunStatusPending,
		ExcludeChiNextAndStarMarket: true,
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
	persistedRun, err := store.GetOpportunityDiscoveryRun(ctx, run.ID)
	if err != nil || !persistedRun.ExcludeChiNextAndStarMarket {
		t.Fatalf("persisted run=%+v err=%v, want exclusion snapshot", persistedRun, err)
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
