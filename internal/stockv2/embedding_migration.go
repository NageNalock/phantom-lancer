package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultEmbeddingMigrationMaxStalledBatches = 3

type OfflineEmbeddingMigrationRequest struct {
	TargetModelID         string
	BatchSize             int
	MaintainRateLimitMs   int
	MaxStalledBatches     int
	EnableAutoMaintenance bool
}

type EmbeddingMigrationProgress struct {
	Stage             string
	Batch             int
	SourceCount       int
	ReadyAssets       int
	FailedAssets      int
	RemainingEstimate int
	BatchTotal        int
	BatchSucceeded    int
	BatchFailed       int
	UpdatedAt         time.Time
}

type EmbeddingMigrationVerification struct {
	TargetModelID       string
	ExpectedDimensions  int
	SourceCount         int
	ReadyAssets         int
	MissingAssets       int
	FailedAssets        int
	StaleAssets         int
	ChangedAssets       int
	DimensionMismatches int
	MissingVectors      int
	TargetVectorRows    int64
	OtherModelAssets    int64
	OtherModelVectors   int64
}

func (v EmbeddingMigrationVerification) Complete() bool {
	return v.SourceCount > 0 &&
		v.ReadyAssets == v.SourceCount &&
		v.MissingAssets == 0 &&
		v.FailedAssets == 0 &&
		v.StaleAssets == 0 &&
		v.ChangedAssets == 0 &&
		v.DimensionMismatches == 0 &&
		v.MissingVectors == 0 &&
		v.TargetVectorRows == int64(v.ReadyAssets)
}

type OfflineEmbeddingMigrationResult struct {
	SourceModelID       string
	TargetModelID       string
	TargetModelName     string
	EmbeddingDimensions int
	BatchCount          int
	SourceCount         int
	DeletedAssets       int64
	DeletedVectors      int64
	Verification        EmbeddingMigrationVerification
	StartedAt           time.Time
	CompletedAt         time.Time
}

func (s *Service) RunOfflineEmbeddingMigration(
	ctx context.Context,
	req OfflineEmbeddingMigrationRequest,
	progress func(EmbeddingMigrationProgress),
) (OfflineEmbeddingMigrationResult, error) {
	result := OfflineEmbeddingMigrationResult{StartedAt: time.Now()}
	if s == nil || s.store == nil {
		return result, errors.New("stockv2 service is not configured")
	}
	req.TargetModelID = strings.TrimSpace(req.TargetModelID)
	if req.TargetModelID == "" {
		return result, ErrEmbeddingModelNotConfigured
	}
	if req.BatchSize <= 0 || req.BatchSize > maxEmbeddingMaintainBatchSize {
		return result, fmt.Errorf("%w: batch size must be between 1 and %d", ErrInvalidEmbeddingConfig, maxEmbeddingMaintainBatchSize)
	}
	if req.MaintainRateLimitMs < 0 {
		return result, fmt.Errorf("%w: rate limit cannot be negative", ErrInvalidEmbeddingConfig)
	}
	if req.MaxStalledBatches <= 0 {
		req.MaxStalledBatches = defaultEmbeddingMigrationMaxStalledBatches
	}

	target, err := s.store.GetAgentModelProfile(ctx, req.TargetModelID)
	if err != nil {
		return result, err
	}
	if target.ModelType != AgentModelTypeEmbedding {
		return result, ErrEmbeddingModelInvalid
	}
	if !target.Enabled {
		return result, ErrEmbeddingModelUnavailable
	}
	probe, err := s.TestAgentModel(ctx, RequestTestAgentModel{
		ProviderID:          target.ProviderID,
		ModelName:           target.ModelName,
		ModelType:           AgentModelTypeEmbedding,
		EmbeddingProtocol:   target.EmbeddingProtocol,
		EmbeddingDimensions: target.EmbeddingDimensions,
		InputModalities:     target.InputModalities,
		EncodingFormat:      target.EncodingFormat,
		Input:               "StockV2 offline embedding migration connectivity test",
	})
	if err != nil {
		return result, fmt.Errorf("probe target embedding model: %w", err)
	}
	if !probe.OK {
		return result, fmt.Errorf("probe target embedding model: %s", strings.TrimSpace(probe.Message))
	}
	if target.EmbeddingDimensions > 0 && probe.EmbeddingDimensions != target.EmbeddingDimensions {
		return result, ErrEmbeddingDimensionsMismatch
	}

	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return result, err
	}
	result.SourceModelID = strings.TrimSpace(cfg.EmbeddingModelID)
	result.TargetModelID = target.ID
	result.TargetModelName = target.ModelName
	result.EmbeddingDimensions = probe.EmbeddingDimensions

	enabled := true
	autoMaintain := false
	targetID := target.ID
	batchSize := req.BatchSize
	rateLimit := req.MaintainRateLimitMs
	if _, err := s.UpdateEmbeddingConfig(ctx, RequestUpdateEmbeddingConfig{
		EmbeddingModelID:    &targetID,
		Enabled:             &enabled,
		AutoMaintainEnabled: &autoMaintain,
		MaintainBatchSize:   &batchSize,
		MaintainRateLimitMs: &rateLimit,
	}); err != nil {
		return result, fmt.Errorf("switch embedding model: %w", err)
	}

	sourceCount, err := s.countIndexableEmbeddingSources(ctx)
	if err != nil {
		return result, err
	}
	result.SourceCount = sourceCount
	if sourceCount == 0 {
		return result, errors.New("embedding migration has no indexable sources")
	}

	stalled := 0
	for batch := 1; ; batch++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		batchResult, err := s.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{
			ObjectTypes: normalizeEmbeddingObjectTypes(nil),
			Limit:       req.BatchSize,
		})
		if err != nil {
			return result, fmt.Errorf("rebuild embedding batch %d: %w", batch, err)
		}
		result.BatchCount = batch
		ready, countErr := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
			ModelID: target.ID,
			Status:  EmbeddingAssetStatusReady,
		})
		if countErr != nil {
			return result, countErr
		}
		failed, countErr := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
			ModelID: target.ID,
			Status:  EmbeddingAssetStatusFailed,
		})
		if countErr != nil {
			return result, countErr
		}
		if progress != nil {
			progress(EmbeddingMigrationProgress{
				Stage:             "rebuild",
				Batch:             batch,
				SourceCount:       sourceCount,
				ReadyAssets:       ready,
				FailedAssets:      failed,
				RemainingEstimate: max(sourceCount-ready, 0),
				BatchTotal:        batchResult.Total,
				BatchSucceeded:    batchResult.Succeeded,
				BatchFailed:       batchResult.Failed,
				UpdatedAt:         time.Now(),
			})
		}
		if batchResult.Total == 0 {
			break
		}
		if batchResult.Succeeded == 0 {
			stalled++
			if stalled >= req.MaxStalledBatches {
				return result, fmt.Errorf("embedding migration made no progress for %d consecutive batches", stalled)
			}
			delay := time.Duration(1<<(stalled-1)) * time.Second
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(delay):
			}
		} else {
			stalled = 0
		}
	}

	verification, err := s.VerifyEmbeddingMigration(ctx, target.ID)
	if err != nil {
		return result, err
	}
	result.Verification = verification
	if !verification.Complete() {
		return result, fmt.Errorf("embedding migration verification failed: %+v", verification)
	}
	if progress != nil {
		progress(EmbeddingMigrationProgress{
			Stage:       "verified",
			Batch:       result.BatchCount,
			SourceCount: sourceCount,
			ReadyAssets: verification.ReadyAssets,
			UpdatedAt:   time.Now(),
		})
	}

	deletedVectors, err := s.store.DeleteEmbeddingVectorsExceptModel(ctx, target.ID)
	if err != nil {
		return result, fmt.Errorf("delete old embedding vectors: %w", err)
	}
	if err := s.store.CheckpointEmbeddingVectors(ctx); err != nil {
		return result, fmt.Errorf("checkpoint embedding vectors: %w", err)
	}
	deletedAssets, err := s.store.DeleteEmbeddingAssetsExceptModel(ctx, target.ID)
	if err != nil {
		return result, fmt.Errorf("delete old embedding assets: %w", err)
	}
	result.DeletedVectors = deletedVectors
	result.DeletedAssets = deletedAssets

	verification, err = s.VerifyEmbeddingMigration(ctx, target.ID)
	if err != nil {
		return result, err
	}
	result.Verification = verification
	if !verification.Complete() || verification.OtherModelAssets != 0 || verification.OtherModelVectors != 0 {
		return result, fmt.Errorf("embedding cleanup verification failed: %+v", verification)
	}
	if err := s.store.CompactEmbeddingMetadata(ctx); err != nil {
		return result, fmt.Errorf("compact embedding metadata: %w", err)
	}

	cfg, err = s.store.GetEmbeddingConfig(ctx)
	if err != nil {
		return result, err
	}
	cfg.EmbeddingModelID = target.ID
	cfg.Enabled = true
	cfg.AutoMaintainEnabled = req.EnableAutoMaintenance
	cfg.LastProbeAt = time.Now()
	cfg.LastProbeStatus = EmbeddingStatusReady
	cfg.LastError = ""
	if cfg.AutoMaintainEnabled {
		cfg.NextMaintainAt = time.Now().Add(time.Duration(cfg.MaintainIntervalSeconds) * time.Second)
	} else {
		cfg.NextMaintainAt = time.Time{}
	}
	if _, err := s.store.UpsertEmbeddingConfig(ctx, cfg); err != nil {
		return result, err
	}
	result.CompletedAt = time.Now()
	if progress != nil {
		progress(EmbeddingMigrationProgress{
			Stage:       "cleaned",
			Batch:       result.BatchCount,
			SourceCount: sourceCount,
			ReadyAssets: verification.ReadyAssets,
			UpdatedAt:   result.CompletedAt,
		})
	}
	return result, nil
}

func (s *Service) countIndexableEmbeddingSources(ctx context.Context) (int, error) {
	count := 0
	err := s.forEachEmbeddingSource(ctx, normalizeEmbeddingObjectTypes(nil), func(source embeddingAssetSource) error {
		if strings.TrimSpace(source.Text) != "" {
			count++
		}
		return nil
	})
	return count, err
}

func (s *Service) VerifyEmbeddingMigration(ctx context.Context, targetModelID string) (EmbeddingMigrationVerification, error) {
	target, err := s.store.GetAgentModelProfile(ctx, strings.TrimSpace(targetModelID))
	if err != nil {
		return EmbeddingMigrationVerification{}, err
	}
	verification := EmbeddingMigrationVerification{
		TargetModelID:      target.ID,
		ExpectedDimensions: target.EmbeddingDimensions,
	}
	err = s.forEachEmbeddingSource(ctx, normalizeEmbeddingObjectTypes(nil), func(source embeddingAssetSource) error {
		text := strings.TrimSpace(source.Text)
		if text == "" {
			return nil
		}
		verification.SourceCount++
		asset, err := s.store.GetEmbeddingAssetByObject(ctx, source.ObjectType, source.ObjectID, target.ID)
		if err != nil {
			if errors.Is(err, ErrEmbeddingAssetNotFound) {
				verification.MissingAssets++
				return nil
			}
			return err
		}
		switch asset.Status {
		case EmbeddingAssetStatusReady:
			verification.ReadyAssets++
		case EmbeddingAssetStatusFailed:
			verification.FailedAssets++
		case EmbeddingAssetStatusStale:
			verification.StaleAssets++
		default:
			verification.FailedAssets++
		}
		if asset.TextHash != hashEmbeddingText(text) {
			verification.ChangedAssets++
		}
		if target.EmbeddingDimensions > 0 && asset.EmbeddingDimensions != target.EmbeddingDimensions {
			verification.DimensionMismatches++
		}
		if asset.VectorRef == "" {
			verification.MissingVectors++
			return nil
		}
		hasVector, err := s.store.HasEmbeddingVector(ctx, asset.VectorRef)
		if err != nil {
			return err
		}
		if !hasVector {
			verification.MissingVectors++
		}
		return nil
	})
	if err != nil {
		return verification, err
	}
	verification.TargetVectorRows, err = s.store.CountEmbeddingVectorsForModel(ctx, target.ID)
	if err != nil {
		return verification, err
	}
	verification.OtherModelAssets, err = s.store.CountEmbeddingAssetsExceptModel(ctx, target.ID)
	if err != nil {
		return verification, err
	}
	verification.OtherModelVectors, err = s.store.CountEmbeddingVectorsExceptModel(ctx, target.ID)
	return verification, err
}

func (s *Store) CountEmbeddingAssetsExceptModel(ctx context.Context, modelID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_assets WHERE model_id <> ?`, strings.TrimSpace(modelID)).Scan(&count)
	return count, wrapError(err, "count old embedding assets")
}

func (s *Store) DeleteEmbeddingAssetsExceptModel(ctx context.Context, modelID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_embedding_assets WHERE model_id <> ?`, strings.TrimSpace(modelID))
	if err != nil {
		return 0, wrapError(err, "delete old embedding assets")
	}
	count, err := result.RowsAffected()
	return count, wrapError(err, "count deleted old embedding assets")
}

func (s *Store) CountEmbeddingVectorsForModel(ctx context.Context, modelID string) (int64, error) {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return 0, ErrEmbeddingAssetNotReady
	}
	var count int64
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_vectors_v2 WHERE model_id = ?`, strings.TrimSpace(modelID)).Scan(&count)
	return count, wrapError(err, "count target embedding vectors")
}

func (s *Store) CountEmbeddingVectorsExceptModel(ctx context.Context, modelID string) (int64, error) {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return 0, ErrEmbeddingAssetNotReady
	}
	var count int64
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_embedding_vectors_v2 WHERE model_id <> ?`, strings.TrimSpace(modelID)).Scan(&count)
	return count, wrapError(err, "count old embedding vectors")
}

func (s *Store) DeleteEmbeddingVectorsExceptModel(ctx context.Context, modelID string) (int64, error) {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return 0, ErrEmbeddingAssetNotReady
	}
	result, err := s.marketDB.db.ExecContext(ctx, `DELETE FROM stockv2_embedding_vectors_v2 WHERE model_id <> ?`, strings.TrimSpace(modelID))
	if err != nil {
		return 0, wrapError(err, "delete old embedding vectors")
	}
	count, err := result.RowsAffected()
	return count, wrapError(err, "count deleted old embedding vectors")
}

func (s *Store) CheckpointEmbeddingVectors(ctx context.Context) error {
	if s == nil || s.marketDB == nil || s.marketDB.db == nil {
		return ErrEmbeddingAssetNotReady
	}
	_, err := s.marketDB.db.ExecContext(ctx, `CHECKPOINT`)
	return wrapError(err, "checkpoint embedding vector storage")
}

func (s *Store) CompactEmbeddingMetadata(ctx context.Context) error {
	// ponytail: migration is an offline owner operation, so SQLite VACUUM is the
	// smallest safe way to return deleted model metadata pages to the filesystem.
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return wrapError(err, "checkpoint embedding metadata")
	}
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return wrapError(err, "vacuum embedding metadata")
}
