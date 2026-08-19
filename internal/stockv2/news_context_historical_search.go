package stockv2

import (
	"context"
	"strings"
	"time"
)

func (s *Service) semanticSearchNewsThreadsAt(ctx context.Context, req SemanticSearchRequest) ([]SemanticNewsThreadResult, error) {
	items, err := s.semanticSearchNewsThreadsAtBatch(ctx, []SemanticSearchRequest{req})
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

type historicalNewsThreadSearchCorpus struct {
	readyAssets         map[string]EmbeddingAsset
	latestVersions      map[string]NewsThreadVersion
	allowedIDs          map[string]struct{}
	threadByRetrievalID map[string]string
}

func (s *Service) semanticSearchNewsThreadsAtBatch(ctx context.Context, requests []SemanticSearchRequest) ([][]SemanticNewsThreadResult, error) {
	if len(requests) == 0 {
		return nil, ErrInvalidEmbeddingRequest
	}
	cutoff := parseNewsContextTime(requests[0].AsOf)
	if cutoff.IsZero() {
		return nil, ErrInvalidEmbeddingRequest
	}
	queries := make([]string, len(requests))
	limits := make([]int, len(requests))
	for index, req := range requests {
		queries[index] = strings.TrimSpace(req.Query)
		requestCutoff := parseNewsContextTime(req.AsOf)
		if queries[index] == "" || requestCutoff.IsZero() || !requestCutoff.Equal(cutoff) {
			return nil, ErrInvalidEmbeddingRequest
		}
		limits[index] = req.Limit
		if limits[index] <= 0 {
			limits[index] = 10
		}
		if limits[index] > 50 {
			limits[index] = 50
		}
	}
	model, _, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return nil, err
	}
	queryVectors := make([][]float64, len(queries))
	for index, query := range queries {
		queryVectors[index], err = s.generateEmbedding(ctx, model, query)
		if err != nil {
			return nil, err
		}
		if err := validateEmbeddingDimensions(model, len(queryVectors[index])); err != nil {
			return nil, err
		}
		if index > 0 && len(queryVectors[index]) != len(queryVectors[0]) {
			return nil, ErrEmbeddingDimensionsMismatch
		}
	}
	corpus, err := s.prepareHistoricalNewsThreadSearchCorpus(ctx, model, cutoff, len(queryVectors[0]))
	if err != nil {
		return nil, err
	}
	// ponytail: all per-event query vectors share one immutable historical
	// corpus and one vector-table scan. This preserves individual recall while
	// avoiding the previous N+1 full metadata/vector reads per model batch.
	hits, err := s.store.SearchEmbeddingVectorsForObjectsBatch(
		ctx, model.ID, EmbeddingObjectNewsThreadVersion, corpus.allowedIDs, queryVectors, 50)
	if err != nil {
		return nil, err
	}
	out := make([][]SemanticNewsThreadResult, len(requests))
	for index := range requests {
		for _, hit := range hits[index] {
			if len(out[index]) >= limits[index] {
				break
			}
			threadID, ok := corpus.threadByRetrievalID[hit.ObjectID]
			version, found := corpus.latestVersions[threadID]
			asset, ready := corpus.readyAssets[hit.ObjectID]
			if !ok || !found || !ready || asset.VectorRef != hit.VectorRef ||
				(requests[index].MinScore != 0 && hit.Score < requests[index].MinScore) {
				continue
			}
			versionCopy := version
			out[index] = append(out[index], SemanticNewsThreadResult{
				Score: hit.Score, Thread: historicalNewsThreadSnapshot(version), Version: &versionCopy,
				RetrievalVersionID: hit.ObjectID, Asset: asset,
			})
		}
		if len(out[index]) == 0 {
			return nil, ErrEmbeddingAssetNotReady
		}
	}
	return out, nil
}

func (s *Service) prepareHistoricalNewsThreadSearchCorpus(ctx context.Context, model AgentModelProfile, cutoff time.Time, dimensions int) (historicalNewsThreadSearchCorpus, error) {
	corpus := historicalNewsThreadSearchCorpus{}
	assets, err := s.store.ListReadyEmbeddingAssetsForSearch(
		ctx, EmbeddingObjectNewsThreadVersion, model.ID, dimensions)
	if err != nil {
		return corpus, err
	}
	readyAssets := make(map[string]EmbeddingAsset, len(assets))
	readyAssetIDs := make([]string, 0, len(assets))
	for _, asset := range assets {
		readyAssets[asset.ObjectID] = asset
		readyAssetIDs = append(readyAssetIDs, asset.ObjectID)
	}
	if len(readyAssets) == 0 {
		return corpus, ErrEmbeddingAssetNotReady
	}

	latest, err := s.store.ListLatestNewsThreadVersionsAtForSearch(ctx, cutoff)
	if err != nil {
		return corpus, err
	}
	latestVersions := make(map[string]NewsThreadVersion)
	for _, version := range latest {
		latestVersions[version.ThreadID] = version
	}

	embeddedVersions, err := s.store.ListNewsThreadVersionsForEmbeddingAssetsAt(ctx, cutoff, readyAssetIDs)
	if err != nil {
		return corpus, err
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
		return corpus, ErrEmbeddingAssetNotReady
	}
	allowedIDs := make(map[string]struct{}, len(retrievalVersions))
	threadByRetrievalID := make(map[string]string, len(retrievalVersions))
	for threadID, version := range retrievalVersions {
		allowedIDs[version.ID] = struct{}{}
		threadByRetrievalID[version.ID] = threadID
	}
	corpus.readyAssets = readyAssets
	corpus.latestVersions = latestVersions
	corpus.allowedIDs = allowedIDs
	corpus.threadByRetrievalID = threadByRetrievalID
	return corpus, nil
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
