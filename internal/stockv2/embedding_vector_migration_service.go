package stockv2

import (
	"context"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	embeddingVectorMigrationBatchSize    = 200
	embeddingVectorMigrationPollInterval = 30 * time.Second
)

func (s *Service) runEmbeddingVectorMigrationScheduler(ctx context.Context) {
	if s == nil || s.store == nil || s.store.marketDB == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.migrateEmbeddingVectorsOnce(ctx, embeddingVectorMigrationBatchSize); err != nil && s.log != nil {
				_ = s.store.MarkEmbeddingVectorMigrationFailed(ctx, safelog.Text(err.Error(), 500), embeddingVectorMigrationBatchSize)
				s.log.Warn("embedding vector compact migration failed", "error", safelog.Text(err.Error(), 300))
			}
			status, err := s.store.GetEmbeddingVectorMigrationStatus(ctx)
			if err == nil && status.Status == embeddingVectorMigrationDone {
				if err := s.store.marketDB.DeleteLegacyEmbeddingVectors(ctx); err != nil && s.log != nil {
					s.log.Warn("cleanup legacy embedding vectors failed", "error", safelog.Text(err.Error(), 300))
				}
				return
			}
			timer.Reset(embeddingVectorMigrationPollInterval)
		}
	}
}

func (s *Service) EmbeddingVectorMigrationStatus(ctx context.Context) (EmbeddingVectorMigrationStatus, error) {
	if s == nil || s.store == nil {
		return EmbeddingVectorMigrationStatus{}, ErrEmbeddingAssetNotReady
	}
	return s.store.GetEmbeddingVectorMigrationStatus(ctx)
}

func (s *Service) migrateEmbeddingVectorsOnce(ctx context.Context, batchSize int) error {
	if batchSize <= 0 {
		batchSize = embeddingVectorMigrationBatchSize
	}
	existing, err := s.store.GetEmbeddingVectorMigrationStatus(ctx)
	if err == nil && existing.Status == embeddingVectorMigrationDone {
		return nil
	}
	total, err := s.store.marketDB.CountLegacyEmbeddingVectorRefs(ctx)
	if err != nil {
		return err
	}
	migrated, err := s.store.marketDB.CountMigratedLegacyEmbeddingVectorRefs(ctx)
	if err != nil {
		return err
	}
	status, err := s.store.EnsureEmbeddingVectorMigrationStatus(ctx, total, migrated, batchSize)
	if err != nil {
		return err
	}
	if status.Status == embeddingVectorMigrationDone {
		return nil
	}
	refs, err := s.store.marketDB.ListUnmigratedEmbeddingVectorRefs(ctx, batchSize)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		migrated, err = s.store.marketDB.CountMigratedLegacyEmbeddingVectorRefs(ctx)
		if err != nil {
			return err
		}
		if err := s.store.MarkEmbeddingVectorMigrationProgress(ctx, migrated, total, batchSize, status.LastVectorRef); err != nil {
			return err
		}
		if migrated >= total {
			return s.store.marketDB.DeleteLegacyEmbeddingVectors(ctx)
		}
		return nil
	}
	lastRef := ""
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		asset, vector, err := s.store.marketDB.LoadLegacyEmbeddingVector(ctx, ref)
		if err != nil {
			return err
		}
		if err := s.store.marketDB.UpsertEmbeddingVectorV2(ctx, asset, vector); err != nil {
			return err
		}
		lastRef = ref
	}
	migrated, err = s.store.marketDB.CountMigratedLegacyEmbeddingVectorRefs(ctx)
	if err != nil {
		return err
	}
	if err := s.store.MarkEmbeddingVectorMigrationProgress(ctx, migrated, total, batchSize, lastRef); err != nil {
		return err
	}
	if migrated >= total {
		return s.store.marketDB.DeleteLegacyEmbeddingVectors(ctx)
	}
	return nil
}
