package stockv2

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"sort"
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

func (s *Store) HasEmbeddingVector(ctx context.Context, vectorRef string) (bool, error) {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil || vectorRef == "" {
		return false, nil
	}
	var found int
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT 1 FROM stockv2_embedding_vectors_v2 WHERE vector_ref = ? LIMIT 1`, vectorRef).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, wrapError(err, "check embedding vector v2")
	}
	return found == 1, nil
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

func (s *Store) SearchEmbeddingVectorsForObjects(ctx context.Context, modelID, objectType string, objectIDs map[string]struct{}, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	if len(query) == 0 || len(objectIDs) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return nil, ErrEmbeddingAssetNotReady
	}
	return s.marketDB.searchEmbeddingVectors(ctx, modelID, objectType, objectIDs, query, limit)
}

func (s *Store) SearchEmbeddingVectorsForObjectsBatch(ctx context.Context, modelID, objectType string, objectIDs map[string]struct{}, queries [][]float64, limit int) ([][]EmbeddingVectorSearchHit, error) {
	if len(queries) == 0 || len(objectIDs) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return nil, ErrEmbeddingAssetNotReady
	}
	return s.marketDB.searchEmbeddingVectorsBatch(ctx, modelID, objectType, objectIDs, queries, limit)
}

func (s *MarketDataStore) UpsertEmbeddingVector(ctx context.Context, asset EmbeddingAsset, vector []float64) error {
	if s == nil || s.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin embedding vector v2 tx")
	}
	defer tx.Rollback()
	if err := upsertEmbeddingVectorTx(ctx, tx, asset, vector, time.Now()); err != nil {
		return err
	}
	return wrapError(tx.Commit(), "commit embedding vector v2")
}

func (s *MarketDataStore) DeleteEmbeddingVector(ctx context.Context, vectorRef string) error {
	if s == nil || s.db == nil || vectorRef == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors_v2 WHERE vector_ref = ?`, vectorRef)
	return wrapError(err, "delete embedding vector v2")
}

func upsertEmbeddingVectorTx(ctx context.Context, tx *sql.Tx, asset EmbeddingAsset, vector []float64, updatedAt time.Time) error {
	if len(vector) == 0 {
		return ErrEmbeddingAssetNotReady
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors_v2 WHERE vector_ref = ?`, asset.VectorRef); err != nil {
		return wrapError(err, "delete old embedding vector v2")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_vectors_v2
			(vector_ref, vector_blob, dimensions, model_id, object_type, object_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, asset.VectorRef, encodeEmbeddingVector(vector), len(vector), asset.ModelID, asset.ObjectType, asset.ObjectID, updatedAt)
	return wrapError(err, "insert embedding vector v2")
}

func (s *MarketDataStore) SearchEmbeddingVectors(ctx context.Context, modelID, objectType string, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	return s.searchEmbeddingVectors(ctx, modelID, objectType, nil, query, limit)
}

func (s *MarketDataStore) searchEmbeddingVectors(ctx context.Context, modelID, objectType string, objectIDs map[string]struct{}, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	if s == nil || s.db == nil {
		return nil, ErrEmbeddingAssetNotReady
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	queryNorm := vectorNorm(query)
	if queryNorm == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	where := `WHERE model_id = ?`
	args := []any{modelID}
	if objectType != "" {
		where += ` AND object_type = ?`
		args = append(args, objectType)
	}
	hits := make([]EmbeddingVectorSearchHit, 0, limit)
	if objectIDs == nil {
		if err := s.appendEmbeddingVectorHits(ctx, where, args, query, queryNorm, &hits); err != nil {
			return nil, err
		}
	} else {
		const batchSize = 500
		ids := make([]string, 0, len(objectIDs))
		for id := range objectIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for start := 0; start < len(ids); start += batchSize {
			end := start + batchSize
			if end > len(ids) {
				end = len(ids)
			}
			batchArgs := append([]any(nil), args...)
			for _, id := range ids[start:end] {
				batchArgs = append(batchArgs, id)
			}
			batchWhere := where + ` AND object_id IN (` + sqlPlaceholders(end-start) + `)`
			if err := s.appendEmbeddingVectorHits(ctx, batchWhere, batchArgs, query, queryNorm, &hits); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].VectorRef < hits[j].VectorRef
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (s *MarketDataStore) searchEmbeddingVectorsBatch(ctx context.Context, modelID, objectType string, objectIDs map[string]struct{}, queries [][]float64, limit int) ([][]EmbeddingVectorSearchHit, error) {
	if s == nil || s.db == nil || len(queries) == 0 || len(objectIDs) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	queryNorms := make([]float64, len(queries))
	for index, query := range queries {
		queryNorms[index] = vectorNorm(query)
		if len(query) == 0 || queryNorms[index] == 0 {
			return nil, ErrEmbeddingAssetNotReady
		}
	}
	where := `WHERE model_id = ?`
	args := []any{modelID}
	if objectType != "" {
		where += ` AND object_type = ?`
		args = append(args, objectType)
	}
	hits := make([][]EmbeddingVectorSearchHit, len(queries))
	ids := make([]string, 0, len(objectIDs))
	for id := range objectIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	const batchSize = 500
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batchArgs := append([]any(nil), args...)
		for _, id := range ids[start:end] {
			batchArgs = append(batchArgs, id)
		}
		batchWhere := where + ` AND object_id IN (` + sqlPlaceholders(end-start) + `)`
		if err := s.appendEmbeddingVectorBatchHits(ctx, batchWhere, batchArgs, queries, queryNorms, hits); err != nil {
			return nil, err
		}
	}
	for index := range hits {
		sort.Slice(hits[index], func(i, j int) bool {
			if hits[index][i].Score == hits[index][j].Score {
				return hits[index][i].VectorRef < hits[index][j].VectorRef
			}
			return hits[index][i].Score > hits[index][j].Score
		})
		if len(hits[index]) > limit {
			hits[index] = hits[index][:limit]
		}
	}
	return hits, nil
}

func (s *MarketDataStore) appendEmbeddingVectorHits(ctx context.Context, where string, args []any, query []float64, queryNorm float64, hits *[]EmbeddingVectorSearchHit) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT vector_ref, vector_blob, dimensions, object_type, object_id
		FROM stockv2_embedding_vectors_v2
		`+where, args...)
	if err != nil {
		return wrapError(err, "search embedding vectors v2")
	}
	defer rows.Close()
	for rows.Next() {
		var vectorRef, hitObjectType, objectID string
		var blob []byte
		var dimensions int
		if err := rows.Scan(&vectorRef, &blob, &dimensions, &hitObjectType, &objectID); err != nil {
			return wrapError(err, "scan embedding vector v2")
		}
		vector, err := decodeEmbeddingVector(blob, dimensions)
		if err != nil {
			return err
		}
		score := cosineScore(query, queryNorm, vector)
		if score == 0 {
			continue
		}
		*hits = append(*hits, EmbeddingVectorSearchHit{
			VectorRef:  vectorRef,
			ObjectType: hitObjectType,
			ObjectID:   objectID,
			Score:      score,
		})
	}
	if err := rows.Err(); err != nil {
		return wrapError(err, "iterate embedding vectors v2")
	}
	return nil
}

func (s *MarketDataStore) appendEmbeddingVectorBatchHits(ctx context.Context, where string, args []any, queries [][]float64, queryNorms []float64, hits [][]EmbeddingVectorSearchHit) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT vector_ref, vector_blob, dimensions, object_type, object_id
		FROM stockv2_embedding_vectors_v2
		`+where, args...)
	if err != nil {
		return wrapError(err, "search embedding vectors v2 batch")
	}
	defer rows.Close()
	for rows.Next() {
		var vectorRef, hitObjectType, objectID string
		var blob []byte
		var dimensions int
		if err := rows.Scan(&vectorRef, &blob, &dimensions, &hitObjectType, &objectID); err != nil {
			return wrapError(err, "scan embedding vector v2 batch")
		}
		vector, err := decodeEmbeddingVector(blob, dimensions)
		if err != nil {
			return err
		}
		candidateNorm := vectorNorm(vector)
		if candidateNorm == 0 {
			continue
		}
		for index, query := range queries {
			score := cosineScoreWithNorms(query, queryNorms[index], vector, candidateNorm)
			if score == 0 {
				continue
			}
			hits[index] = append(hits[index], EmbeddingVectorSearchHit{
				VectorRef: vectorRef, ObjectType: hitObjectType, ObjectID: objectID, Score: score,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return wrapError(err, "iterate embedding vectors v2 batch")
	}
	return nil
}

func encodeEmbeddingVector(vector []float64) []byte {
	blob := make([]byte, len(vector)*8)
	for i, value := range vector {
		binary.LittleEndian.PutUint64(blob[i*8:(i+1)*8], math.Float64bits(value))
	}
	return blob
}

func decodeEmbeddingVector(blob []byte, dimensions int) ([]float64, error) {
	if dimensions <= 0 || len(blob) != dimensions*8 {
		return nil, ErrEmbeddingAssetNotReady
	}
	vector := make([]float64, dimensions)
	for i := range vector {
		vector[i] = math.Float64frombits(binary.LittleEndian.Uint64(blob[i*8 : (i+1)*8]))
	}
	return vector, nil
}

func vectorNorm(vector []float64) float64 {
	sum := 0.0
	for _, value := range vector {
		sum += value * value
	}
	return math.Sqrt(sum)
}

func cosineScore(query []float64, queryNorm float64, vector []float64) float64 {
	vectorNorm := vectorNorm(vector)
	return cosineScoreWithNorms(query, queryNorm, vector, vectorNorm)
}

func cosineScoreWithNorms(query []float64, queryNorm float64, vector []float64, vectorNorm float64) float64 {
	if queryNorm == 0 || vectorNorm == 0 {
		return 0
	}
	dimensions := len(query)
	if len(vector) < dimensions {
		dimensions = len(vector)
	}
	dot := 0.0
	for i := 0; i < dimensions; i++ {
		dot += query[i] * vector[i]
	}
	return dot / (queryNorm * vectorNorm)
}
