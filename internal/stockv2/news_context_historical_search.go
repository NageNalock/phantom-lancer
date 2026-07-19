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

	assets, err := s.store.ListReadyEmbeddingAssetsForSearch(
		ctx, EmbeddingObjectNewsThreadVersion, model.ID, len(queryVector))
	if err != nil {
		return nil, err
	}
	readyAssets := make(map[string]EmbeddingAsset, len(assets))
	readyAssetIDs := make([]string, 0, len(assets))
	for _, asset := range assets {
		readyAssets[asset.ObjectID] = asset
		readyAssetIDs = append(readyAssetIDs, asset.ObjectID)
	}
	if len(readyAssets) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}

	latest, err := s.store.ListLatestNewsThreadVersionsAtForSearch(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	latestVersions := make(map[string]NewsThreadVersion)
	for _, version := range latest {
		latestVersions[version.ThreadID] = version
	}

	embeddedVersions, err := s.store.ListNewsThreadVersionsForEmbeddingAssetsAt(ctx, cutoff, readyAssetIDs)
	if err != nil {
		return nil, err
	}
	retrievalVersions := make(map[string]NewsThreadVersion)
	for _, version := range embeddedVersions {
		asset, ready := readyAssets[version.ID]
		if !ready || asset.TextHash != hashEmbeddingText(NewsThreadVersionEmbeddingText(version)) {
			continue
		}
		if previous, found := retrievalVersions[version.ThreadID]; !found || newerNewsContextVersion(version, previous) {
			retrievalVersions[version.ThreadID] = version
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
