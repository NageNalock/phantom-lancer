package stockv2

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SyncAnnouncementMarkets fetches exchange-wide CNINFO pages, then commits all
// announcements and cursors in one transaction. Any page failure leaves every
// requested market cursor unchanged, so the overlap window is safe to retry.
func (s *Service) SyncAnnouncementMarkets(ctx context.Context, req AnnouncementMarketsSyncRequest) (AnnouncementMarketsSyncResult, error) {
	startedAt := time.Now()
	result := AnnouncementMarketsSyncResult{StartedAt: startedAt, NewBySymbol: map[string][]StockV2Announcement{}}
	if s == nil || s.store == nil || s.announcementSource == nil {
		return result, fmt.Errorf("announcement sync is not configured")
	}
	markets, err := normalizedAnnouncementSyncMarkets(req.Markets)
	if err != nil {
		return result, err
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = announcementSyncDefaultPageSize
	}
	if pageSize > announcementSyncMaxPageSize {
		pageSize = announcementSyncMaxPageSize
	}
	maxPages := req.MaxPages
	if maxPages <= 0 {
		maxPages = announcementSyncDefaultMaxPages
	}
	overlap := req.Overlap
	if overlap == 0 {
		overlap = announcementSyncDefaultOverlap
	}
	if overlap < 0 {
		return result, fmt.Errorf("announcement overlap must not be negative")
	}
	initialLookback := req.InitialLookback
	if initialLookback == 0 {
		initialLookback = announcementSyncDefaultInitialLookback
	}
	if initialLookback < 0 {
		return result, fmt.Errorf("announcement initial lookback must not be negative")
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	allItems := make([]StockV2Announcement, 0, len(markets)*pageSize)
	states := make([]AnnouncementSyncState, 0, len(markets))
	marketResults := make([]AnnouncementMarketSyncResult, 0, len(markets))
	for _, market := range markets {
		previous, exists, err := s.store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, market)
		if err != nil {
			result.FinishedAt = time.Now()
			return result, err
		}
		windowEnd := now
		if previous.CoveredThrough.After(windowEnd) {
			windowEnd = previous.CoveredThrough
		}
		windowStart := windowEnd.Add(-initialLookback)
		if exists && !previous.CoveredThrough.IsZero() {
			windowStart = previous.CoveredThrough.Add(-overlap)
		}
		marketResult := AnnouncementMarketSyncResult{Market: market, WindowStart: windowStart, WindowEnd: windowEnd}
		latestPublishedAt := previous.LatestPublishedAt
		completed := false
		for pageNumber := 1; pageNumber <= maxPages; pageNumber++ {
			page, fetchErr := s.announcementSource.FetchMarketAnnouncementsPage(ctx, market, pageNumber, pageSize, windowStart, windowEnd)
			if fetchErr != nil {
				result.Markets = append(marketResults, marketResult)
				result.FinishedAt = time.Now()
				return result, fmt.Errorf("fetch %s announcement page %d: %w", market, pageNumber, fetchErr)
			}
			marketResult.PagesFetched++
			marketResult.FetchedCount += len(page.Announcements)
			allItems = append(allItems, page.Announcements...)
			for _, item := range page.Announcements {
				if item.PublishedAt.After(latestPublishedAt) {
					latestPublishedAt = item.PublishedAt
				}
			}
			if !page.HasMore {
				completed = true
				break
			}
			if page.RawCount == 0 {
				result.Markets = append(marketResults, marketResult)
				result.FinishedAt = time.Now()
				return result, fmt.Errorf("fetch %s announcements: empty page %d before pagination completed", market, pageNumber)
			}
		}
		if !completed {
			result.Markets = append(marketResults, marketResult)
			result.FinishedAt = time.Now()
			return result, fmt.Errorf("fetch %s announcements exceeded %d pages", market, maxPages)
		}
		marketResult.LatestPublishedAt = latestPublishedAt
		marketResults = append(marketResults, marketResult)
		states = append(states, AnnouncementSyncState{
			Source:            StockV2AnnouncementSourceCninfo,
			Market:            market,
			CoveredThrough:    windowEnd,
			LatestPublishedAt: latestPublishedAt,
			LastSuccessAt:     now,
			LastWindowStart:   windowStart,
			LastWindowEnd:     windowEnd,
			LastPageCount:     marketResult.PagesFetched,
			LastFetchedCount:  marketResult.FetchedCount,
			CreatedAt:         previous.CreatedAt,
		})
	}

	newItems, err := s.store.CommitAnnouncementSyncBatch(ctx, allItems, states)
	if err != nil {
		result.Markets = marketResults
		result.FinishedAt = time.Now()
		return result, err
	}
	insertedByMarket := make(map[string]int, len(markets))
	for _, item := range newItems {
		insertedByMarket[strings.ToUpper(strings.TrimSpace(item.Market))]++
		result.NewBySymbol[item.Symbol] = append(result.NewBySymbol[item.Symbol], item)
	}
	for i := range marketResults {
		marketResults[i].InsertedCount = insertedByMarket[marketResults[i].Market]
	}
	result.Markets = marketResults
	result.FinishedAt = time.Now()
	return result, nil
}

func normalizedAnnouncementSyncMarkets(markets []string) ([]string, error) {
	if len(markets) == 0 {
		markets = []string{"SH", "SZ", "BJ"}
	}
	out := make([]string, 0, len(markets))
	seen := make(map[string]struct{}, len(markets))
	for _, market := range markets {
		normalized, err := normalizeCninfoMarket(market)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}
