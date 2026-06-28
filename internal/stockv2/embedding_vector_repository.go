package stockv2

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

type EmbeddingVectorSearchHit struct {
	VectorRef  string
	ObjectType string
	ObjectID   string
	Score      float64
}

func (s *Store) UpsertEmbeddingVector(ctx context.Context, asset EmbeddingAsset, vector []float64) error {
	if len(vector) == 0 {
		return ErrEmbeddingAssetNotReady
	}
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	return s.marketDB.UpsertEmbeddingVector(ctx, asset, vector)
}

func (s *Store) DeleteEmbeddingVector(ctx context.Context, vectorRef string) error {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil || vectorRef == "" {
		return nil
	}
	return s.marketDB.DeleteEmbeddingVector(ctx, vectorRef)
}

func (s *Store) SearchEmbeddingVectors(ctx context.Context, modelID, objectType string, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	if len(query) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return nil, ErrEmbeddingAssetNotReady
	}
	return s.marketDB.SearchEmbeddingVectors(ctx, modelID, objectType, query, limit)
}

func (s *MarketDataStore) UpsertEmbeddingVector(ctx context.Context, asset EmbeddingAsset, vector []float64) error {
	if s == nil || s.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin embedding vector tx")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors WHERE vector_ref = ?`, asset.VectorRef); err != nil {
		return wrapError(err, "delete old embedding vector")
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO stockv2_embedding_vectors
			(vector_ref, dim_index, value, model_id, object_type, object_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return wrapError(err, "prepare embedding vector insert")
	}
	defer stmt.Close()
	now := time.Now()
	for i, value := range vector {
		if _, err := stmt.ExecContext(ctx, asset.VectorRef, i, value, asset.ModelID, asset.ObjectType, asset.ObjectID, now); err != nil {
			return wrapError(err, "insert embedding vector value")
		}
	}
	return wrapError(tx.Commit(), "commit embedding vector")
}

func (s *MarketDataStore) DeleteEmbeddingVector(ctx context.Context, vectorRef string) error {
	if s == nil || s.db == nil || vectorRef == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors WHERE vector_ref = ?`, vectorRef)
	return wrapError(err, "delete embedding vector")
}

func (s *MarketDataStore) SearchEmbeddingVectors(ctx context.Context, modelID, objectType string, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	if s == nil || s.db == nil {
		return nil, ErrEmbeddingAssetNotReady
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	queryNorm := 0.0
	for _, value := range query {
		queryNorm += value * value
	}
	queryNorm = math.Sqrt(queryNorm)
	if queryNorm == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin embedding vector search")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS stockv2_query_embedding (dim_index INTEGER, value DOUBLE)`); err != nil {
		return nil, wrapError(err, "create query embedding temp table")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_query_embedding`); err != nil {
		return nil, wrapError(err, "clear query embedding temp table")
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO stockv2_query_embedding (dim_index, value) VALUES (?, ?)`)
	if err != nil {
		return nil, wrapError(err, "prepare query embedding insert")
	}
	for i, value := range query {
		if _, err := stmt.ExecContext(ctx, i, value); err != nil {
			_ = stmt.Close()
			return nil, wrapError(err, "insert query embedding value")
		}
	}
	_ = stmt.Close()

	rows, err := queryEmbeddingVectorSearch(ctx, tx, modelID, objectType, queryNorm, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]EmbeddingVectorSearchHit, 0)
	for rows.Next() {
		var hit EmbeddingVectorSearchHit
		if err := rows.Scan(&hit.VectorRef, &hit.ObjectType, &hit.ObjectID, &hit.Score); err != nil {
			return nil, wrapError(err, "scan embedding vector hit")
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate embedding vector hits")
	}
	return out, wrapError(tx.Commit(), "commit embedding vector search")
}

func queryEmbeddingVectorSearch(ctx context.Context, tx *sql.Tx, modelID, objectType string, queryNorm float64, limit int) (*sql.Rows, error) {
	where := `WHERE v.model_id = ?`
	args := []any{queryNorm, modelID, limit}
	if objectType != "" {
		where += ` AND v.object_type = ?`
		args = []any{queryNorm, modelID, objectType, limit}
	}
	query := fmt.Sprintf(`
		SELECT v.vector_ref, MIN(v.object_type) AS object_type, MIN(v.object_id) AS object_id,
		       SUM(v.value * q.value) / (? * SQRT(SUM(v.value * v.value))) AS score
		FROM stockv2_embedding_vectors v
		JOIN stockv2_query_embedding q ON q.dim_index = v.dim_index
		%s
		GROUP BY v.vector_ref
		HAVING SQRT(SUM(v.value * v.value)) > 0
		ORDER BY score DESC
		LIMIT ?
	`, where)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "search embedding vectors")
	}
	return rows, nil
}
