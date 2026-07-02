package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) GetEmbeddingVectorMigrationStatus(ctx context.Context) (EmbeddingVectorMigrationStatus, error) {
	if s == nil || s.db == nil {
		return EmbeddingVectorMigrationStatus{}, ErrEmbeddingAssetNotReady
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, schema_version, status, total_vectors, migrated_vectors, remaining_vectors,
		       batch_size, COALESCE(last_vector_ref,''), COALESCE(last_error,''),
		       started_at, completed_at, updated_at
		FROM stockv2_embedding_vector_migrations
		WHERE id = ?
	`, embeddingVectorMigrationID)
	status, err := scanEmbeddingVectorMigrationStatus(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EmbeddingVectorMigrationStatus{
				ID:            embeddingVectorMigrationID,
				SchemaVersion: embeddingVectorSchemaVersion,
				Status:        embeddingVectorMigrationPending,
			}, nil
		}
		return EmbeddingVectorMigrationStatus{}, wrapError(err, "get embedding vector migration status")
	}
	return status, nil
}

func (s *Store) EnsureEmbeddingVectorMigrationStatus(ctx context.Context, totalVectors, migratedVectors, batchSize int) (EmbeddingVectorMigrationStatus, error) {
	if s == nil || s.db == nil {
		return EmbeddingVectorMigrationStatus{}, ErrEmbeddingAssetNotReady
	}
	now := time.Now()
	remaining := totalVectors - migratedVectors
	if remaining < 0 {
		remaining = 0
	}
	status := embeddingVectorMigrationRunning
	var completedAt any
	if remaining == 0 {
		status = embeddingVectorMigrationDone
		completedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_embedding_vector_migrations (
			id, schema_version, status, total_vectors, migrated_vectors, remaining_vectors,
			batch_size, started_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			schema_version = excluded.schema_version,
			status = CASE
				WHEN stockv2_embedding_vector_migrations.status = ? THEN stockv2_embedding_vector_migrations.status
				ELSE excluded.status
			END,
			total_vectors = excluded.total_vectors,
			migrated_vectors = excluded.migrated_vectors,
			remaining_vectors = excluded.remaining_vectors,
			batch_size = excluded.batch_size,
			started_at = COALESCE(stockv2_embedding_vector_migrations.started_at, excluded.started_at),
			completed_at = CASE WHEN excluded.remaining_vectors = 0 THEN excluded.completed_at ELSE NULL END,
			last_error = NULL,
			updated_at = excluded.updated_at
	`, embeddingVectorMigrationID, embeddingVectorSchemaVersion, status, totalVectors, migratedVectors,
		remaining, batchSize, now, completedAt, now, embeddingVectorMigrationDone)
	if err != nil {
		return EmbeddingVectorMigrationStatus{}, wrapError(err, "ensure embedding vector migration status")
	}
	return s.GetEmbeddingVectorMigrationStatus(ctx)
}

func (s *Store) MarkEmbeddingVectorMigrationProgress(ctx context.Context, migratedVectors, totalVectors, batchSize int, lastVectorRef string) error {
	if s == nil || s.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	now := time.Now()
	remaining := totalVectors - migratedVectors
	if remaining < 0 {
		remaining = 0
	}
	status := embeddingVectorMigrationRunning
	var completedAt any
	if remaining == 0 {
		status = embeddingVectorMigrationDone
		completedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_vector_migrations
		SET status = ?, migrated_vectors = ?, remaining_vectors = ?, batch_size = ?,
		    last_vector_ref = ?, last_error = NULL, completed_at = ?, updated_at = ?
		WHERE id = ?
	`, status, migratedVectors, remaining, batchSize, nullableString(lastVectorRef), completedAt, now, embeddingVectorMigrationID)
	return wrapError(err, "mark embedding vector migration progress")
}

func (s *Store) MarkEmbeddingVectorMigrationFailed(ctx context.Context, errText string, batchSize int) error {
	if s == nil || s.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_embedding_vector_migrations
		SET status = ?, batch_size = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`, embeddingVectorMigrationFailed, batchSize, nullableString(errText), time.Now(), embeddingVectorMigrationID)
	return wrapError(err, "mark embedding vector migration failed")
}

func scanEmbeddingVectorMigrationStatus(row rowScanner) (EmbeddingVectorMigrationStatus, error) {
	var status EmbeddingVectorMigrationStatus
	var startedAt, completedAt sql.NullTime
	if err := row.Scan(
		&status.ID, &status.SchemaVersion, &status.Status, &status.TotalVectors,
		&status.MigratedVectors, &status.RemainingVectors, &status.BatchSize,
		&status.LastVectorRef, &status.LastError, &startedAt, &completedAt, &status.UpdatedAt,
	); err != nil {
		return EmbeddingVectorMigrationStatus{}, err
	}
	if startedAt.Valid {
		status.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		status.CompletedAt = completedAt.Time
	}
	return status, nil
}

func (s *MarketDataStore) CountLegacyEmbeddingVectorRefs(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrEmbeddingAssetNotReady
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (SELECT vector_ref FROM stockv2_embedding_vectors GROUP BY vector_ref)
	`).Scan(&count); err != nil {
		return 0, wrapError(err, "count legacy embedding vector refs")
	}
	return count, nil
}

func (s *MarketDataStore) CountMigratedLegacyEmbeddingVectorRefs(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrEmbeddingAssetNotReady
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT legacy.vector_ref
			FROM (SELECT vector_ref FROM stockv2_embedding_vectors GROUP BY vector_ref) legacy
			JOIN stockv2_embedding_vectors_v2 compact ON compact.vector_ref = legacy.vector_ref
		)
	`).Scan(&count); err != nil {
		return 0, wrapError(err, "count migrated legacy embedding vector refs")
	}
	return count, nil
}

func (s *MarketDataStore) ListUnmigratedEmbeddingVectorRefs(ctx context.Context, limit int) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, ErrEmbeddingAssetNotReady
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT legacy.vector_ref
		FROM (
			SELECT vector_ref, MIN(updated_at) AS updated_at
			FROM stockv2_embedding_vectors
			GROUP BY vector_ref
		) legacy
		LEFT JOIN stockv2_embedding_vectors_v2 compact ON compact.vector_ref = legacy.vector_ref
		WHERE compact.vector_ref IS NULL
		ORDER BY legacy.updated_at ASC, legacy.vector_ref ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, wrapError(err, "list unmigrated embedding vector refs")
	}
	defer rows.Close()
	refs := make([]string, 0, limit)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, wrapError(err, "scan unmigrated embedding vector ref")
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate unmigrated embedding vector refs")
	}
	return refs, nil
}

func (s *MarketDataStore) LoadLegacyEmbeddingVector(ctx context.Context, vectorRef string) (EmbeddingAsset, []float64, error) {
	if s == nil || s.db == nil || vectorRef == "" {
		return EmbeddingAsset{}, nil, ErrEmbeddingAssetNotReady
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT dim_index, value, model_id, object_type, object_id, updated_at
		FROM stockv2_embedding_vectors
		WHERE vector_ref = ?
		ORDER BY dim_index ASC
	`, vectorRef)
	if err != nil {
		return EmbeddingAsset{}, nil, wrapError(err, "load legacy embedding vector")
	}
	defer rows.Close()
	asset := EmbeddingAsset{VectorRef: vectorRef}
	vector := make([]float64, 0)
	for rows.Next() {
		var dim int
		var value float64
		var updatedAt time.Time
		if err := rows.Scan(&dim, &value, &asset.ModelID, &asset.ObjectType, &asset.ObjectID, &updatedAt); err != nil {
			return EmbeddingAsset{}, nil, wrapError(err, "scan legacy embedding vector")
		}
		for len(vector) <= dim {
			vector = append(vector, 0)
		}
		vector[dim] = value
	}
	if err := rows.Err(); err != nil {
		return EmbeddingAsset{}, nil, wrapError(err, "iterate legacy embedding vector")
	}
	if len(vector) == 0 {
		return EmbeddingAsset{}, nil, ErrEmbeddingAssetNotReady
	}
	return asset, vector, nil
}

func (s *MarketDataStore) DeleteLegacyEmbeddingVectors(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors`)
	return wrapError(err, "delete legacy embedding vectors")
}
