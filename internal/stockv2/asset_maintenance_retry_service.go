package stockv2

import (
	"context"
	"errors"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	assetMaintenanceRetryInterval    = time.Minute
	assetMaintenanceRetryBatchSize   = 2
	assetMaintenanceRetryMaxAttempts = 5
)

func (s *Service) runAssetMaintenanceRetryScheduler(ctx context.Context) {
	if s != nil && s.store != nil {
		if err := s.store.RecoverClaimedAssetMaintenanceRetries(ctx, time.Now()); err != nil && s.log != nil {
			s.log.Warn("recover asset maintenance retries failed", "error", safelog.Text(err.Error(), 240))
		}
	}
	ticker := time.NewTicker(assetMaintenanceRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.processAssetMaintenanceRetries(ctx, now); err != nil &&
				!errors.Is(err, ErrAssetMaintenanceRetryQueueEmpty) && s.log != nil {
				s.log.Warn("asset maintenance retry batch stopped", "error", safelog.Text(err.Error(), 240))
			}
		}
	}
}

func (s *Service) processAssetMaintenanceRetries(ctx context.Context, now time.Time) error {
	if s == nil || s.store == nil {
		return ErrAssetMaintenanceRetryQueueEmpty
	}
	retryBatchSize := maintenanceConcurrencyForResourceGate(s.currentResourceGate())
	if retryBatchSize == 0 {
		return ErrAssetMaintenanceRetryQueueEmpty
	}
	retryBatchSize = min(retryBatchSize, assetMaintenanceRetryBatchSize)
	s.bulkMaintenanceMu.Lock()
	if s.bulkMaintenanceRun {
		s.bulkMaintenanceMu.Unlock()
		return ErrAssetMaintenanceRetryQueueEmpty
	}
	s.bulkMaintenanceRun = true
	s.bulkMaintenanceMu.Unlock()
	defer func() {
		s.bulkMaintenanceMu.Lock()
		s.bulkMaintenanceRun = false
		s.bulkMaintenanceMu.Unlock()
	}()

	processed := 0
	type marketAnnouncementRetry struct {
		result AnnouncementMarketsSyncResult
		err    error
	}
	announcementRetries := make(map[string]marketAnnouncementRetry)
	for processed < retryBatchSize {
		if maintenanceConcurrencyForResourceGate(s.currentResourceGate()) == 0 {
			if processed == 0 {
				return ErrAssetMaintenanceRetryQueueEmpty
			}
			return nil
		}
		item, err := s.store.ClaimDueAssetMaintenanceRetry(ctx, now)
		if errors.Is(err, ErrAssetMaintenanceRetryQueueEmpty) {
			if processed == 0 {
				return err
			}
			return nil
		}
		if err != nil {
			return err
		}
		processed++
		instrument, err := s.store.GetInstrument(ctx, item.Symbol)
		if err != nil {
			if saveErr := s.store.RequeueClaimedAssetMaintenanceItem(ctx, item, err.Error(), now); saveErr != nil {
				return saveErr
			}
			continue
		}
		job, jobErr := s.store.GetUpdateJob(ctx, item.JobID)
		if jobErr != nil {
			if saveErr := s.store.RequeueClaimedAssetMaintenanceItem(ctx, item, jobErr.Error(), now); saveErr != nil {
				return saveErr
			}
			continue
		}

		announcementBatch := AnnouncementMarketsSyncResult{NewBySymbol: map[string][]StockV2Announcement{}}
		var announcementErr error
		var recent map[string][]StockV2Announcement
		var recentErr error
		if normalizeInstrumentType(instrument.InstrumentType) == InstrumentTypeStock {
			market := strings.ToUpper(strings.TrimSpace(instrument.Market))
			state, exists, stateErr := s.store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, market)
			if stateErr != nil {
				announcementErr = stateErr
			} else {
				covered := announcementSyncCoversMaintenanceJob(state, exists, job, now)
				if covered {
					announcementBatch.FinishedAt = state.LastSuccessAt
				} else if cached, ok := announcementRetries[market]; ok {
					announcementBatch, announcementErr = cached.result, cached.err
				} else {
					announcementBatch, announcementErr = s.SyncAnnouncementMarkets(ctx, AnnouncementMarketsSyncRequest{
						Markets: []string{market}, Now: now,
					})
					announcementRetries[market] = marketAnnouncementRetry{result: announcementBatch, err: announcementErr}
				}
			}
			recent, recentErr = s.store.ListRecentAnnouncementsBySymbols(ctx, []string{item.Symbol}, 100)
		}
		announcementContextErr := errors.Join(announcementErr, recentErr)
		_, _ = s.maintainAssetForInstrument(ctx, instrument, assetMaintenanceOptions{
			JobID: item.JobID, ItemID: item.ID, AttemptCount: item.AttemptCount,
			ExpectedLatestDate: item.ExpectedLatestDate,
			TriggerSource:      "retry", RequestedBy: "system",
			AnnouncementsPrefetched:   true,
			PrefetchedAnnouncements:   announcementBatch.NewBySymbol[item.Symbol],
			RecentAnnouncements:       recent[item.Symbol],
			AnnouncementPrefetchError: announcementContextErr,
			AnnouncementCheckedAt:     announcementBatch.FinishedAt,
		})
		failures, failuresErr := s.store.ListAssetMaintenanceJobFailures(ctx, item.JobID)
		if failuresErr != nil {
			return failuresErr
		}
		if _, finalizeErr := s.store.finalizeAssetMaintenanceJob(
			ctx, item.JobID, job.AssetStats, failures, job.WriteBytesEnd, job.PeakRSSBytes,
		); finalizeErr != nil {
			return finalizeErr
		}
	}
	return nil
}

func announcementSyncCoversMaintenanceJob(state AnnouncementSyncState, exists bool, job StockV2UpdateJob, now time.Time) bool {
	if !exists || state.LastSuccessAt.IsZero() {
		return false
	}
	if validateAnnouncementSyncStateAt(state, true, now) != nil {
		return false
	}
	if !job.MessageCutoffAt.IsZero() {
		return !state.CoveredThrough.Before(job.MessageCutoffAt)
	}
	return !state.LastSuccessAt.Before(now.Add(-assetReadinessAnnouncementMaxLag))
}

func assetMaintenanceRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 15 * time.Minute * time.Duration(1<<min(attempt-1, 4))
	return min(delay, 6*time.Hour)
}
