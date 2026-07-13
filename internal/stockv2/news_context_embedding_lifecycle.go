package stockv2

import (
	"context"
	"errors"
	"time"
)

func (s *Service) PruneTransientNewsContextEmbeddings(ctx context.Context, through time.Time) error {
	if through.IsZero() {
		return ErrInvalidNewsContextInput
	}
	// ponytail: daily completion is infrequent, so scan durable version rows
	// directly instead of maintaining a second cleanup queue.
	for offset := 0; ; offset += 500 {
		versions, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{Limit: 500, Offset: offset})
		if err != nil {
			return err
		}
		for _, version := range versions {
			if version.WindowType == NewsContextWindowDaily || version.MaterialChange || newsThreadVersionEffectiveTime(version).After(through) {
				continue
			}
			for {
				assets, err := s.store.ListEmbeddingAssets(ctx, EmbeddingAssetListFilter{
					ObjectType: EmbeddingObjectNewsThreadVersion,
					ObjectID:   version.ID,
					Limit:      200,
				})
				if err != nil {
					return err
				}
				if len(assets) == 0 {
					break
				}
				for _, asset := range assets {
					if err := s.store.DeleteEmbeddingVector(ctx, asset.VectorRef); err != nil {
						return err
					}
					if err := s.store.DeleteEmbeddingAsset(ctx, asset.ID); err != nil && !errors.Is(err, ErrEmbeddingAssetNotFound) {
						return err
					}
				}
			}
			if err := s.store.UpdateNewsThreadVersionIndexStatus(ctx, version.ID, NewsContextIndexPending, ""); err != nil {
				return err
			}
		}
		if len(versions) < 500 {
			return nil
		}
	}
}

func newsThreadVersionEffectiveTime(version NewsThreadVersion) time.Time {
	if !version.EffectiveAt.IsZero() {
		return version.EffectiveAt
	}
	return version.CreatedAt
}
