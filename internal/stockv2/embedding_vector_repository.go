package stockv2

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	embeddingVectorMigrationID      = "embedding_vectors_v2"
	embeddingVectorSchemaVersion    = 2
	embeddingVectorMigrationPending = "pending"
	embeddingVectorMigrationRunning = "running"
	embeddingVectorMigrationDone    = "completed"
	embeddingVectorMigrationFailed  = "failed"
)

type EmbeddingVectorSearchHit struct {
	VectorRef  string
	ObjectType string
	ObjectID   string
	Score      float64
}

type EmbeddingVectorMigrationStatus struct {
	ID               string    `json:"id"`
	SchemaVersion    int       `json:"schemaVersion"`
	Status           string    `json:"status"`
	TotalVectors     int       `json:"totalVectors"`
	MigratedVectors  int       `json:"migratedVectors"`
	RemainingVectors int       `json:"remainingVectors"`
	BatchSize        int       `json:"batchSize"`
	LastVectorRef    string    `json:"lastVectorRef,omitempty"`
	LastError        string    `json:"lastError,omitempty"`
	StartedAt        time.Time `json:"startedAt,omitempty"`
	CompletedAt      time.Time `json:"completedAt,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt,omitempty"`
}

func (s *Store) UpsertEmbeddingVector(ctx context.Context, asset EmbeddingAsset, vector []float64) error {
	if len(vector) == 0 {
		return ErrEmbeddingAssetNotReady
	}
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	ready, err := s.embeddingVectorV2Ready(ctx)
	if err == nil && ready {
		return s.marketDB.UpsertEmbeddingVectorV2(ctx, asset, vector)
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
	ready, err := s.embeddingVectorV2Ready(ctx)
	if err == nil && ready {
		return s.marketDB.SearchEmbeddingVectorsV2(ctx, modelID, objectType, query, limit)
	}
	return s.marketDB.SearchEmbeddingVectors(ctx, modelID, objectType, query, limit)
}

func (s *Store) embeddingVectorV2Ready(ctx context.Context) (bool, error) {
	status, err := s.GetEmbeddingVectorMigrationStatus(ctx)
	if err != nil {
		return false, err
	}
	return status.Status == embeddingVectorMigrationDone, nil
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
	if err := upsertEmbeddingVectorV2Tx(ctx, tx, asset, vector, now); err != nil {
		return err
	}
	return wrapError(tx.Commit(), "commit embedding vector")
}

func (s *MarketDataStore) DeleteEmbeddingVector(ctx context.Context, vectorRef string) error {
	if s == nil || s.db == nil || vectorRef == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin delete embedding vector tx")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors WHERE vector_ref = ?`, vectorRef); err != nil {
		return wrapError(err, "delete legacy embedding vector")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors_v2 WHERE vector_ref = ?`, vectorRef); err != nil {
		return wrapError(err, "delete embedding vector v2")
	}
	return wrapError(tx.Commit(), "commit delete embedding vector")
}

func (s *MarketDataStore) UpsertEmbeddingVectorV2(ctx context.Context, asset EmbeddingAsset, vector []float64) error {
	if s == nil || s.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin embedding vector v2 tx")
	}
	defer tx.Rollback()
	if err := upsertEmbeddingVectorV2Tx(ctx, tx, asset, vector, time.Now()); err != nil {
		return err
	}
	return wrapError(tx.Commit(), "commit embedding vector v2")
}

func upsertEmbeddingVectorV2Tx(ctx context.Context, tx *sql.Tx, asset EmbeddingAsset, vector []float64, updatedAt time.Time) error {
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

func (s *MarketDataStore) SearchEmbeddingVectorsV2(ctx context.Context, modelID, objectType string, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT vector_ref, vector_blob, dimensions, object_type, object_id
		FROM stockv2_embedding_vectors_v2
		`+where, args...)
	if err != nil {
		return nil, wrapError(err, "search embedding vectors v2")
	}
	defer rows.Close()
	hits := make([]EmbeddingVectorSearchHit, 0, limit)
	for rows.Next() {
		var vectorRef, hitObjectType, objectID string
		var blob []byte
		var dimensions int
		if err := rows.Scan(&vectorRef, &blob, &dimensions, &hitObjectType, &objectID); err != nil {
			return nil, wrapError(err, "scan embedding vector v2")
		}
		vector, err := decodeEmbeddingVector(blob, dimensions)
		if err != nil {
			return nil, err
		}
		score := cosineScore(query, queryNorm, vector)
		if score == 0 {
			continue
		}
		hits = append(hits, EmbeddingVectorSearchHit{
			VectorRef:  vectorRef,
			ObjectType: hitObjectType,
			ObjectID:   objectID,
			Score:      score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate embedding vectors v2")
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
