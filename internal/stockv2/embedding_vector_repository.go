package stockv2

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"sort"
	"strconv"
	"strings"
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
			(vector_ref, vector_blob, vector_values, dimensions, model_id, object_type, object_id, updated_at)
		VALUES (?, ?, ?::DOUBLE[], ?, ?, ?, ?, ?)
	`, asset.VectorRef, encodeEmbeddingVector(vector), formatDuckDBVector(vector), len(vector), asset.ModelID, asset.ObjectType, asset.ObjectID, updatedAt)
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

	// 优先使用 DuckDB 原生 array_cosine_distance 在 SQL 层计算相似度 + 排序 + 截断。
	// 只扫描有 vector_values 的行（新写入的数据都有），旧数据（只有 vector_blob）在回退路径处理。
	hits, err := s.searchEmbeddingVectorsNative(ctx, modelID, objectType, query, limit)
	if err != nil {
		// 原生路径失败（如旧版 DuckDB 不支持 array_cosine_distance），回退到 Go 层计算
		hits = nil
	}

	// 如果原生路径返回的结果不够，回退到 blob 路径补充旧数据
	if len(hits) < limit {
		blobHits, err := s.searchEmbeddingVectorsBlob(ctx, modelID, objectType, query, limit)
		if err == nil {
			hits = mergeEmbeddingHits(hits, blobHits, limit)
		}
	}

	return hits, nil
}

// searchEmbeddingVectorsNative 使用 DuckDB SQL 层完成相似度计算和 TopK。
// 通过 list_transform + list_sum + sqrt 手动计算 cosine similarity（array_cosine_distance
// 要求 DOUBLE[ANY] 类型参数，而列存储的是 DOUBLE[]，直接调用会类型不匹配）。
// 相比 Go 层全量加载+计算，数据量越大性能优势越明显。
func (s *MarketDataStore) searchEmbeddingVectorsNative(ctx context.Context, modelID, objectType string, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	where := `WHERE e.model_id = ? AND e.vector_values IS NOT NULL`
	args := []any{formatDuckDBVector(query), modelID}
	if objectType != "" {
		where += ` AND e.object_type = ?`
		args = append(args, objectType)
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH query_vec AS (SELECT ?::DOUBLE[] AS qv)
		SELECT e.vector_ref, e.object_type, e.object_id,
			list_sum(list_transform(e.vector_values, (x, i) -> x * query_vec.qv[i]))
			/ (sqrt(list_sum(list_transform(e.vector_values, x -> x*x)))
			   * sqrt(list_sum(list_transform(query_vec.qv, x -> x*x))))
			AS score
		FROM stockv2_embedding_vectors_v2 e, query_vec
		`+where+`
		ORDER BY score DESC
		LIMIT ?
	`, append(args, limit)...)
	if err != nil {
		return nil, wrapError(err, "search embedding vectors native")
	}
	defer rows.Close()
	hits := make([]EmbeddingVectorSearchHit, 0, limit)
	for rows.Next() {
		var vectorRef, hitObjectType, objectID string
		var score float64
		if err := rows.Scan(&vectorRef, &hitObjectType, &objectID, &score); err != nil {
			return nil, wrapError(err, "scan embedding vector native hit")
		}
		if score <= 0 {
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
		return nil, wrapError(err, "iterate embedding vectors native")
	}
	return hits, nil
}

// searchEmbeddingVectorsBlob 是旧路径：加载所有 vector_blob，在 Go 层解码、计算 cosine、排序。
// 用于兼容没有 vector_values 的旧数据。
func (s *MarketDataStore) searchEmbeddingVectorsBlob(ctx context.Context, modelID, objectType string, query []float64, limit int) ([]EmbeddingVectorSearchHit, error) {
	queryNorm := vectorNorm(query)
	if queryNorm == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	where := `WHERE model_id = ? AND vector_values IS NULL`
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
		return nil, wrapError(err, "search embedding vectors blob")
	}
	defer rows.Close()
	hits := make([]EmbeddingVectorSearchHit, 0, limit)
	for rows.Next() {
		var vectorRef, hitObjectType, objectID string
		var blob []byte
		var dimensions int
		if err := rows.Scan(&vectorRef, &blob, &dimensions, &hitObjectType, &objectID); err != nil {
			return nil, wrapError(err, "scan embedding vector blob")
		}
		vector, err := decodeEmbeddingVector(blob, dimensions)
		if err != nil {
			continue
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
		return nil, wrapError(err, "iterate embedding vectors blob")
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

// mergeEmbeddingHits 合并原生路径和 blob 路径的结果，去重后按分数排序取 TopK。
func mergeEmbeddingHits(native, blob []EmbeddingVectorSearchHit, limit int) []EmbeddingVectorSearchHit {
	seen := make(map[string]bool, len(native))
	for _, h := range native {
		seen[h.VectorRef] = true
	}
	merged := make([]EmbeddingVectorSearchHit, 0, len(native)+len(blob))
	merged = append(merged, native...)
	for _, h := range blob {
		if !seen[h.VectorRef] {
			merged = append(merged, h)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Score == merged[j].Score {
			return merged[i].VectorRef < merged[j].VectorRef
		}
		return merged[i].Score > merged[j].Score
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// formatDuckDBVector 将 Go []float64 格式化为 DuckDB 数组字面量字符串，
// 用于通过 CAST(? AS DOUBLE[]) 传入 SQL。
func formatDuckDBVector(vector []float64) string {
	if len(vector) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.Grow(len(vector) * 16)
	sb.WriteByte('[')
	for i, v := range vector {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	}
	sb.WriteByte(']')
	return sb.String()
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
