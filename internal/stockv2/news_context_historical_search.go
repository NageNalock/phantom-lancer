package stockv2

import (
	"context"
	"strings"
)

func (s *Service) semanticSearchNewsThreadsAt(ctx context.Context, req SemanticSearchRequest) ([]SemanticNewsThreadResult, error) {
	query := strings.TrimSpace(req.Query)
	cutoff := parseNewsContextTime(req.AsOf)
	if query == "" || cutoff.IsZero() {
		return nil, ErrInvalidEmbeddingRequest
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	model, _, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return nil, err
	}
	queryVector, err := s.generateEmbedding(ctx, model, query)
	if err != nil {
		return nil, err
	}
	if err := validateEmbeddingDimensions(model, len(queryVector)); err != nil {
		return nil, err
	}

	readyAssets := make(map[string]EmbeddingAsset)
	for offset := 0; ; offset += 200 {
		page, err := s.store.ListEmbeddingAssets(ctx, EmbeddingAssetListFilter{
			ObjectType: EmbeddingObjectNewsThreadVersion,
			ModelID:    model.ID,
			Status:     EmbeddingAssetStatusReady,
			Limit:      200,
			Offset:     offset,
		})
		if err != nil {
			return nil, err
		}
		for _, asset := range page {
			if asset.EmbeddingDimensions == len(queryVector) && strings.TrimSpace(asset.VectorRef) != "" {
				readyAssets[asset.ObjectID] = asset
			}
		}
		if len(page) < 200 {
			break
		}
	}
	if len(readyAssets) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}

	latestVersions := make(map[string]NewsThreadVersion)
	retrievalVersions := make(map[string]NewsThreadVersion)
	for offset := 0; ; offset += 500 {
		page, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{Limit: 500, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, version := range page {
			effectiveAt := newsThreadVersionEffectiveTime(version)
			if effectiveAt.IsZero() || effectiveAt.After(cutoff) {
				continue
			}
			if previous, found := latestVersions[version.ThreadID]; !found || newerNewsContextVersion(version, previous) {
				latestVersions[version.ThreadID] = version
			}
			asset, ready := readyAssets[version.ID]
			if !ready || asset.TextHash != hashEmbeddingText(NewsThreadVersionEmbeddingText(version)) {
				continue
			}
			if previous, found := retrievalVersions[version.ThreadID]; !found || newerNewsContextVersion(version, previous) {
				retrievalVersions[version.ThreadID] = version
			}
		}
		if len(page) < 500 {
			break
		}
	}
	if len(retrievalVersions) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	allowedIDs := make(map[string]struct{}, len(retrievalVersions))
	threadByRetrievalID := make(map[string]string, len(retrievalVersions))
	for threadID, version := range retrievalVersions {
		allowedIDs[version.ID] = struct{}{}
		threadByRetrievalID[version.ID] = threadID
	}
	hits, err := s.store.SearchEmbeddingVectorsForObjects(ctx, model.ID, EmbeddingObjectNewsThreadVersion, allowedIDs, queryVector, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticNewsThreadResult, 0, len(hits))
	for _, hit := range hits {
		threadID, ok := threadByRetrievalID[hit.ObjectID]
		version, found := latestVersions[threadID]
		asset, ready := readyAssets[hit.ObjectID]
		if !ok || !found || !ready || asset.VectorRef != hit.VectorRef || (req.MinScore != 0 && hit.Score < req.MinScore) {
			continue
		}
		versionCopy := version
		out = append(out, SemanticNewsThreadResult{
			Score: hit.Score, Thread: historicalNewsThreadSnapshot(version), Version: &versionCopy,
			RetrievalVersionID: hit.ObjectID, Asset: asset,
		})
	}
	if len(out) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	return out, nil
}

func newerNewsContextVersion(candidate, current NewsThreadVersion) bool {
	candidateAt := newsThreadVersionEffectiveTime(candidate)
	currentAt := newsThreadVersionEffectiveTime(current)
	if !candidateAt.Equal(currentAt) {
		return candidateAt.After(currentAt)
	}
	if candidate.VersionNo != current.VersionNo {
		return candidate.VersionNo > current.VersionNo
	}
	return candidate.ID > current.ID
}

func historicalNewsThreadSnapshot(version NewsThreadVersion) NewsThread {
	effectiveAt := newsThreadVersionEffectiveTime(version)
	return NewsThread{
		ID: version.ThreadID, ThemeID: version.ThreadID, Title: version.Title,
		Summary: version.CoreThesis, CoreThesis: version.CoreThesis, Stage: version.Stage,
		LatestChange: version.LatestChange, Confidence: version.Confidence,
		Status:     NewsThreadStatusActive,
		Industries: version.Industries, Symbols: version.Symbols, Funds: version.Funds,
		Facts: version.Facts, Inferences: version.Inferences, CounterEvidence: version.CounterEvidence,
		OpenQuestions: version.OpenQuestions, Leaders: version.Leaders, Followers: version.Followers,
		Laggards: version.Laggards, NextCandidates: version.NextCandidates, Catalysts: version.Catalysts,
		Invalidations: version.Invalidations, Relations: version.Relations,
		CurrentVersion: version.VersionNo, CurrentVersionID: version.ID,
		ReviewStatus: version.ReviewStatus, IndexStatus: version.IndexStatus,
		IndexError: version.IndexError, FirstSeenAt: effectiveAt, LastChangedAt: effectiveAt,
		CreatedAt: version.CreatedAt, UpdatedAt: version.CreatedAt,
	}
}
