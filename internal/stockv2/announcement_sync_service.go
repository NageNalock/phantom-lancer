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
		if err := validateAnnouncementSyncStateAt(previous, exists, now); err != nil {
			result.FinishedAt = time.Now()
			return result, fmt.Errorf("validate %s announcement cursor: %w", market, err)
		}
		windowEnd := now
		windowStart := windowEnd.Add(-initialLookback)
		if exists && !previous.CoveredThrough.IsZero() {
			windowStart = previous.CoveredThrough.Add(-overlap)
		}
		marketResult := AnnouncementMarketSyncResult{Market: market, WindowStart: windowStart, WindowEnd: windowEnd}
		latestPublishedAt := previous.LatestPublishedAt
		incremental, fetchErr := s.fetchAnnouncementWindow(ctx, market, pageSize, maxPages, windowStart, windowEnd)
		if fetchErr != nil {
			result.Markets = append(marketResults, marketResult)
			result.FinishedAt = time.Now()
			return result, fmt.Errorf("fetch %s incremental announcements: %w", market, fetchErr)
		}
		marketResult.PagesFetched = incremental.PagesFetched
		marketResult.FetchedCount = len(incremental.Items)
		allItems = append(allItems, incremental.Items...)
		for _, item := range incremental.Items {
			if item.PublishedAt.After(latestPublishedAt) {
				latestPublishedAt = item.PublishedAt
			}
		}

		lateStartedAt := previous.LateRecheckStartedAt
		lateCoveredThrough := previous.LateRecheckCoveredThrough
		lastLateRecheckAt := previous.LastLateRecheckAt
		lateDate, nextStartedAt := nextAnnouncementLateRecheckDate(previous, now)
		if !lateDate.IsZero() {
			lateWindow, lateErr := s.fetchAnnouncementWindow(ctx, market, pageSize, maxPages, lateDate, lateDate)
			if lateErr != nil {
				result.Markets = append(marketResults, marketResult)
				result.FinishedAt = time.Now()
				return result, fmt.Errorf("recheck %s announcements for %s: %w", market, lateDate.Format("2006-01-02"), lateErr)
			}
			marketResult.LateRecheckDate = lateDate
			marketResult.LateRecheckPagesFetched = lateWindow.PagesFetched
			marketResult.LateRecheckFetchedCount = len(lateWindow.Items)
			allItems = append(allItems, lateWindow.Items...)
			for _, item := range lateWindow.Items {
				if item.PublishedAt.After(latestPublishedAt) {
					latestPublishedAt = item.PublishedAt
				}
			}
			lateStartedAt = nextStartedAt
			lateCoveredThrough = lateDate
			lastLateRecheckAt = now
		}
		marketResult.LatestPublishedAt = latestPublishedAt
		marketResults = append(marketResults, marketResult)
		states = append(states, AnnouncementSyncState{
			Source:                    StockV2AnnouncementSourceCninfo,
			Market:                    market,
			CoveredThrough:            windowEnd,
			LatestPublishedAt:         latestPublishedAt,
			LastSuccessAt:             now,
			LastWindowStart:           windowStart,
			LastWindowEnd:             windowEnd,
			LastPageCount:             marketResult.PagesFetched,
			LastFetchedCount:          marketResult.FetchedCount + marketResult.LateRecheckFetchedCount,
			LateRecheckStartedAt:      lateStartedAt,
			LateRecheckCoveredThrough: lateCoveredThrough,
			LastLateRecheckAt:         lastLateRecheckAt,
			CreatedAt:                 previous.CreatedAt,
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

type announcementWindowFetch struct {
	Items        []StockV2Announcement
	PagesFetched int
}

func (s *Service) fetchAnnouncementWindow(
	ctx context.Context,
	market string,
	pageSize int,
	maxPages int,
	windowStart time.Time,
	windowEnd time.Time,
) (announcementWindowFetch, error) {
	result := announcementWindowFetch{Items: make([]StockV2Announcement, 0, pageSize)}
	for pageNumber := 1; pageNumber <= maxPages; pageNumber++ {
		page, err := s.announcementSource.FetchMarketAnnouncementsPage(
			ctx, market, pageNumber, pageSize, windowStart, windowEnd,
		)
		if err != nil {
			return result, fmt.Errorf("page %d: %w", pageNumber, err)
		}
		result.PagesFetched++
		result.Items = append(result.Items, page.Announcements...)
		if !page.HasMore {
			return result, nil
		}
		if page.RawCount == 0 {
			return result, fmt.Errorf("empty page %d before pagination completed", pageNumber)
		}
	}
	return result, fmt.Errorf("pagination exceeded %d pages", maxPages)
}

func nextAnnouncementLateRecheckDate(previous AnnouncementSyncState, now time.Time) (time.Time, time.Time) {
	// ponytail: one persisted date bucket per market and Shanghai calendar day caps network work.
	// The deliberate upper bound is 30 days of late-publication detection; widen
	// the lookback or add a bulk change feed only if CNINFO exceeds that contract.
	today := announcementShanghaiDay(now)
	if !previous.LastLateRecheckAt.IsZero() && announcementShanghaiDay(previous.LastLateRecheckAt).Equal(today) {
		return time.Time{}, previous.LateRecheckStartedAt
	}
	oldest := today.AddDate(0, 0, -announcementLateRecheckLookbackDays)
	latest := today.AddDate(0, 0, -1)
	startedAt := previous.LateRecheckStartedAt
	if startedAt.IsZero() || previous.LateRecheckCoveredThrough.IsZero() {
		return oldest, now
	}
	next := announcementShanghaiDay(previous.LateRecheckCoveredThrough).AddDate(0, 0, 1)
	if next.Before(oldest) {
		next = oldest
	}
	if next.After(latest) {
		return time.Time{}, startedAt
	}
	return next, startedAt
}

func announcementShanghaiDay(value time.Time) time.Time {
	local := value.In(chinaMarketTZ)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, chinaMarketTZ)
}

func validateAnnouncementSyncStateAt(state AnnouncementSyncState, exists bool, now time.Time) error {
	if !exists {
		return nil
	}
	latestAllowed := now.Add(announcementSyncClockSkew)
	for name, value := range map[string]time.Time{
		"covered_through":              state.CoveredThrough,
		"latest_published_at":          state.LatestPublishedAt,
		"last_success_at":              state.LastSuccessAt,
		"last_window_start":            state.LastWindowStart,
		"last_window_end":              state.LastWindowEnd,
		"late_recheck_started_at":      state.LateRecheckStartedAt,
		"late_recheck_covered_through": state.LateRecheckCoveredThrough,
		"last_late_recheck_at":         state.LastLateRecheckAt,
	} {
		if !value.IsZero() && value.After(latestAllowed) {
			return fmt.Errorf("%s is in the future", name)
		}
	}
	if !state.LateRecheckCoveredThrough.IsZero() && state.LateRecheckStartedAt.IsZero() {
		return fmt.Errorf("late recheck coverage has no start watermark")
	}
	if !state.LastLateRecheckAt.IsZero() && state.LateRecheckCoveredThrough.IsZero() {
		return fmt.Errorf("late recheck timestamp has no coverage watermark")
	}
	if !state.LastLateRecheckAt.IsZero() &&
		announcementShanghaiDay(state.LastLateRecheckAt).After(announcementShanghaiDay(now)) {
		return fmt.Errorf("last_late_recheck_at is in a future Shanghai calendar day")
	}
	if !state.LateRecheckCoveredThrough.IsZero() &&
		!announcementShanghaiDay(state.LateRecheckCoveredThrough).Before(announcementShanghaiDay(now)) {
		return fmt.Errorf("late_recheck_covered_through is not a historical date bucket")
	}
	return nil
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
