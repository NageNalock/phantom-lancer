package stockv2

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestMigrateEmbeddingVectorsOnceMovesLegacyRowsAndUpdatesStatus(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Hour)

	_, err := store.marketDB.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_vectors
			(vector_ref, dim_index, value, model_id, object_type, object_id, updated_at)
		VALUES
			('vec-1', 0, 0.1, 'embedding-model-1', 'stock_profile', '688012', ?),
			('vec-1', 1, 0.2, 'embedding-model-1', 'stock_profile', '688012', ?),
			('vec-2', 0, 0.3, 'embedding-model-1', 'stock_profile', '300750', ?),
			('vec-2', 1, 0.4, 'embedding-model-1', 'stock_profile', '300750', ?)
	`, now, now, now, now)
	if err != nil {
		t.Fatalf("insert legacy vectors: %v", err)
	}

	svc := NewService(store, slog.Default(), http.DefaultClient)
	if err := svc.migrateEmbeddingVectorsOnce(ctx, 1); err != nil {
		t.Fatalf("migrate first batch: %v", err)
	}
	status, err := store.GetEmbeddingVectorMigrationStatus(ctx)
	if err != nil {
		t.Fatalf("get first status: %v", err)
	}
	if status.MigratedVectors != 1 || status.RemainingVectors != 1 || status.Status != embeddingVectorMigrationRunning {
		t.Fatalf("first status = %#v", status)
	}

	if err := svc.migrateEmbeddingVectorsOnce(ctx, 10); err != nil {
		t.Fatalf("migrate second batch: %v", err)
	}
	status, err = store.GetEmbeddingVectorMigrationStatus(ctx)
	if err != nil {
		t.Fatalf("get final status: %v", err)
	}
	if status.MigratedVectors != 2 || status.RemainingVectors != 0 || status.Status != embeddingVectorMigrationDone {
		t.Fatalf("final status = %#v", status)
	}
	var legacyRows int
	if err := store.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_vectors`).Scan(&legacyRows); err != nil {
		t.Fatalf("count legacy rows: %v", err)
	}
	if legacyRows != 0 {
		t.Fatalf("legacy rows after completed migration = %d", legacyRows)
	}

	hits, err := store.SearchEmbeddingVectors(ctx, "embedding-model-1", EmbeddingObjectStockProfile, []float64{0.1, 0.2}, 5)
	if err != nil {
		t.Fatalf("search migrated vectors: %v", err)
	}
	if len(hits) == 0 || hits[0].VectorRef != "vec-1" {
		t.Fatalf("hits = %#v", hits)
	}

	if err := store.UpsertEmbeddingVector(ctx, EmbeddingAsset{
		VectorRef:  "vec-3",
		ModelID:    "embedding-model-1",
		ObjectType: EmbeddingObjectStockProfile,
		ObjectID:   "002415",
	}, []float64{0.9, 0.1}); err != nil {
		t.Fatalf("upsert after completed migration: %v", err)
	}
	if err := store.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_vectors`).Scan(&legacyRows); err != nil {
		t.Fatalf("count legacy rows after v2-only upsert: %v", err)
	}
	if legacyRows != 0 {
		t.Fatalf("legacy rows after v2-only upsert = %d", legacyRows)
	}
}

func TestUpsertEmbeddingVectorCleansLegacyRowWhenMigrationCompletesDuringWrite(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.EnsureEmbeddingVectorMigrationStatus(ctx, 1, 0, 200); err != nil {
		t.Fatalf("ensure running migration: %v", err)
	}
	if err := store.marketDB.UpsertEmbeddingVector(ctx, EmbeddingAsset{
		VectorRef:  "vec-race",
		ModelID:    "embedding-model-1",
		ObjectType: EmbeddingObjectStockProfile,
		ObjectID:   "688012",
	}, []float64{0.1, 0.2}); err != nil {
		t.Fatalf("upsert while running: %v", err)
	}
	if err := store.MarkEmbeddingVectorMigrationProgress(ctx, 1, 1, 200, "vec-race"); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	if err := store.UpsertEmbeddingVector(ctx, EmbeddingAsset{
		VectorRef:  "vec-race",
		ModelID:    "embedding-model-1",
		ObjectType: EmbeddingObjectStockProfile,
		ObjectID:   "688012",
	}, []float64{0.2, 0.4}); err != nil {
		t.Fatalf("upsert after completed: %v", err)
	}
	var legacyRows int
	if err := store.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_vectors WHERE vector_ref = 'vec-race'`).Scan(&legacyRows); err != nil {
		t.Fatalf("count legacy rows: %v", err)
	}
	if legacyRows != 0 {
		t.Fatalf("legacy rows after completed upsert = %d", legacyRows)
	}
}
