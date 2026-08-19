package stockv2

import (
	"context"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: three recalls per event and eight per batch cover the observed
	// small event batches without recreating the unbounded MCP transcript. Raise
	// these measured ceilings only if recall audits show missed existing themes.
	newsContextCandidateLimitPerEvent = 3
	newsContextCandidateLimitPerBatch = 8

	newsContextCandidateLookupStatusReady       = "ready"
	newsContextCandidateLookupStatusEmpty       = "no_existing_threads"
	newsContextCandidateLookupStatusUnavailable = "unavailable"
)

func (e *agentAPIExecutor) prefetchNewsContextCandidates(
	ctx context.Context,
	pack NewsContextAggregationPack,
) NewsContextAggregationPack {
	if e == nil || e.service == nil || len(pack.InputNewsEvents) == 0 {
		return pack
	}
	versionCount, err := e.service.store.CountNewsThreadVersions(ctx, NewsThreadVersionListFilter{
		Until: pack.WindowEnd.Add(time.Nanosecond),
	})
	if err != nil {
		return newsContextCandidateLookupUnavailable(pack)
	}
	if versionCount == 0 {
		pack.CandidateLookupStatus = newsContextCandidateLookupStatusEmpty
		return pack
	}

	candidates := make(map[string]NewsContextPromptThread)
	asOf := pack.WindowEnd.Format(time.RFC3339Nano)
	requests := make([]SemanticSearchRequest, 0, len(pack.InputNewsEvents))
	eventIDs := make([]string, 0, len(pack.InputNewsEvents))
	for _, event := range pack.InputNewsEvents {
		query := newsContextCandidateQuery(event)
		if query == "" {
			continue
		}
		requests = append(requests, SemanticSearchRequest{
			Query: query,
			Limit: newsContextCandidateLimitPerEvent,
			AsOf:  asOf,
		})
		eventIDs = append(eventIDs, event.ID)
	}
	if len(requests) > 0 {
		results, searchErr := e.service.semanticSearchNewsThreadsAtBatch(ctx, requests)
		if searchErr != nil {
			return newsContextCandidateLookupUnavailable(pack)
		}
		for index, hits := range results {
			mergeNewsContextCandidates(candidates, eventIDs[index], hits)
		}
	}
	for _, thread := range pack.InputThreads {
		delete(candidates, firstNonEmpty(thread.ID, thread.ThemeID))
	}

	pack.CandidateThreads = sortedNewsContextCandidates(candidates, newsContextCandidateLimitPerBatch)
	pack.CandidateLookupStatus = newsContextCandidateLookupStatusReady
	return pack
}

func newsContextCandidateLookupUnavailable(pack NewsContextAggregationPack) NewsContextAggregationPack {
	pack.CandidateThreads = nil
	pack.CandidateLookupStatus = newsContextCandidateLookupStatusUnavailable
	return pack
}

func newsContextCandidateQuery(event NewsEvent) string {
	query := strings.TrimSpace(strings.Join([]string{event.Title, event.Summary}, "\n"))
	return safelog.Text(query, 2000)
}

func mergeNewsContextCandidates(
	candidates map[string]NewsContextPromptThread,
	newsEventID string,
	hits []SemanticNewsThreadResult,
) {
	for _, hit := range hits {
		id := firstNonEmpty(hit.Thread.ID, hit.Thread.ThemeID)
		if id == "" {
			continue
		}
		candidate, exists := candidates[id]
		if !exists {
			candidate = compactNewsThreadForPrompt(hit.Thread)
			candidate.RetrievalScore = hit.Score
		} else if hit.Score > candidate.RetrievalScore {
			candidate.RetrievalScore = hit.Score
		}
		candidate.MatchedNewsEventIDs = uniqueNonEmptyStrings(append(candidate.MatchedNewsEventIDs, newsEventID))
		candidates[id] = candidate
	}
}

func sortedNewsContextCandidates(
	candidates map[string]NewsContextPromptThread,
	limit int,
) []NewsContextPromptThread {
	items := make([]NewsContextPromptThread, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, candidate)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RetrievalScore != items[j].RetrievalScore {
			return items[i].RetrievalScore > items[j].RetrievalScore
		}
		return firstNonEmpty(items[i].ID, items[i].ThemeID) <
			firstNonEmpty(items[j].ID, items[j].ThemeID)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}
