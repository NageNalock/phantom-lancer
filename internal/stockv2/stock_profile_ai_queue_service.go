package stockv2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: three workers are a bounded middle ground for the single-owner
	// server. Promote this to a provider budget setting only when models differ.
	stockProfileAIWorkerCount  = 3
	stockProfileAILeaseTTL     = 2 * time.Minute
	stockProfileAILeaseRenewal = 30 * time.Second
	stockProfileAIQueuePoll    = 2 * time.Second
	stockProfileAIMaxAttempts  = 5
)

func stockProfileSummaryInputVersion(pack StockProfileSummaryContext, force bool) string {
	announcementHashes := make([]string, 0, len(pack.NewAnnouncements))
	for _, item := range pack.NewAnnouncements {
		fingerprint := firstNonEmpty(
			strings.TrimSpace(item.ContentHash),
			strings.TrimSpace(item.AnnouncementID),
			strings.TrimSpace(item.ID),
			strings.TrimSpace(item.Title)+"|"+item.PublishedAt.UTC().Format(time.RFC3339Nano),
		)
		announcementHashes = append(announcementHashes, fingerprint)
	}
	sort.Strings(announcementHashes)
	previousRaw, _ := json.Marshal(pack.PreviousSummary)
	previousHash := sha256.Sum256(previousRaw)
	baseHash := strings.TrimSpace(pack.Profile.BaseProfileHash)
	if baseHash == "" {
		baseHash = stockProfileAIInputHash(pack.Profile)
	}
	input := struct {
		Schema                int      `json:"schema"`
		Symbol                string   `json:"symbol"`
		BaseProfileHash       string   `json:"baseProfileHash"`
		AnnouncementHashes    []string `json:"announcementHashes"`
		PreviousSummaryHash   string   `json:"previousSummaryHash"`
		DailyLatestDate       string   `json:"dailyLatestDate"`
		DailyMainNetInflow    float64  `json:"dailyMainNetInflow"`
		ForceMaintenanceToken string   `json:"forceMaintenanceToken,omitempty"`
	}{
		Schema:              1,
		Symbol:              pack.Profile.Symbol,
		BaseProfileHash:     baseHash,
		AnnouncementHashes:  announcementHashes,
		PreviousSummaryHash: hex.EncodeToString(previousHash[:]),
		DailyLatestDate:     pack.DailySummary.LatestDate,
		DailyMainNetInflow:  pack.DailySummary.MainNetInflow,
	}
	if force {
		input.ForceMaintenanceToken = generateID()
	}
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func stockProfileAIQueuePriority(decision string) int {
	switch decision {
	case AssetAIDecisionMissing:
		return 400
	case AssetAIDecisionManualForce:
		return 350
	case AssetAIDecisionAnnouncement:
		return 300
	case AssetAIDecisionBaseChanged:
		return 250
	case AssetAIDecisionRetry:
		return 100
	default:
		return 0
	}
}

func (s *Service) enqueueStockProfileAI(
	ctx context.Context,
	pack StockProfileSummaryContext,
	decision, requestedBy string,
	force bool,
) (StockProfileAIQueueItem, error) {
	return s.enqueueStockProfileAIWithState(
		ctx, pack, decision, requestedBy, force,
		StockProfileAIQueueStatusReady, time.Time{},
	)
}

func (s *Service) enqueueStockProfileAIWithState(
	ctx context.Context,
	pack StockProfileSummaryContext,
	decision, requestedBy string,
	force bool,
	queueStatus string,
	availableAt time.Time,
) (StockProfileAIQueueItem, error) {
	queueInput, err := newStockProfileAIQueueItem(pack, decision, requestedBy, force)
	if err != nil {
		return StockProfileAIQueueItem{}, err
	}
	queueInput.Status = queueStatus
	queueInput.AvailableAt = availableAt
	// Serialize queue-version changes with AI result application for the same
	// symbol. This makes the current-version check immediately before profile
	// persistence meaningful, while retaining parallelism across symbols.
	unlock := s.lockStockProfile(pack.Profile.Symbol)
	defer unlock()
	if err := s.store.UpdateStockProfileAIState(ctx, pack.Profile.Symbol, StockProfileAIStatusQueued, "", false); err != nil {
		return StockProfileAIQueueItem{}, err
	}
	item, err := s.store.EnqueueStockProfileAI(ctx, queueInput)
	if err != nil {
		_ = s.store.UpdateStockProfileAIState(ctx, pack.Profile.Symbol, pack.Profile.AIProfileStatus, pack.Profile.AIProfileError, false)
		return StockProfileAIQueueItem{}, err
	}
	if item.Status == StockProfileAIQueueStatusCompleted && item.CompletedInputVersion == item.DesiredInputVersion {
		_ = s.store.UpdateStockProfileAIState(ctx, pack.Profile.Symbol, StockProfileAIStatusReady, "", false)
		return item, nil
	}
	if item.Status == StockProfileAIQueueStatusFailed {
		_ = s.store.UpdateStockProfileAIState(ctx, pack.Profile.Symbol, StockProfileAIStatusFailed, item.LastError, false)
	}
	return item, nil
}

func newStockProfileAIQueueItem(
	pack StockProfileSummaryContext,
	decision, requestedBy string,
	force bool,
) (StockProfileAIQueueItem, error) {
	payload, err := json.Marshal(pack)
	if err != nil {
		return StockProfileAIQueueItem{}, err
	}
	return StockProfileAIQueueItem{
		Symbol:              pack.Profile.Symbol,
		Market:              pack.Profile.Market,
		Status:              StockProfileAIQueueStatusReady,
		Priority:            stockProfileAIQueuePriority(decision),
		TriggerReason:       decision,
		RequestedBy:         firstNonEmpty(requestedBy, "system"),
		DesiredInputVersion: stockProfileSummaryInputVersion(pack, force),
		PayloadJSON:         string(payload),
	}, nil
}

func stockProfileAIQueueState(item StockProfileAIQueueItem) (string, string) {
	switch item.Status {
	case StockProfileAIQueueStatusRunning:
		return StockProfileAIStatusRunning, ""
	case StockProfileAIQueueStatusCompleted:
		return StockProfileAIStatusReady, ""
	case StockProfileAIQueueStatusFailed:
		return StockProfileAIStatusFailed, item.LastError
	default:
		return StockProfileAIStatusQueued, ""
	}
}

func (s *Service) runStockProfileAIQueueWorker(ctx context.Context, workerID string) {
	ticker := time.NewTicker(stockProfileAIQueuePoll)
	defer ticker.Stop()
	for {
		if err := s.processNextStockProfileAIQueueItem(ctx, workerID); err != nil &&
			!errors.Is(err, ErrStockProfileAIQueueEmpty) && !errors.Is(err, context.Canceled) && s.log != nil {
			s.log.Warn("stock profile ai queue worker failed", "worker", workerID, "error", safelog.Text(err.Error(), 300))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) runStockProfileAIQueueBackground(ctx context.Context) {
	// Recovery and the one-time legacy migration finish before workers create new
	// AgentRuns. This removes the prepare/bind race without delaying HTTP or the
	// other background schedulers.
	s.recoverStockProfileAIQueueLeases(ctx, true)
	s.migrateLegacyStockProfileAIRuns(ctx)

	workerDone := make(chan struct{}, stockProfileAIWorkerCount)
	for worker := 0; worker < stockProfileAIWorkerCount; worker++ {
		workerID := fmt.Sprintf("stock-profile-ai-%d", worker+1)
		go func() {
			s.runStockProfileAIQueueWorker(ctx, workerID)
			workerDone <- struct{}{}
		}()
	}
	ticker := time.NewTicker(stockProfileAILeaseTTL)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for worker := 0; worker < stockProfileAIWorkerCount; worker++ {
				<-workerDone
			}
			return
		case <-ticker.C:
			s.recoverStockProfileAIQueueLeases(ctx, false)
		}
	}
}

func (s *Service) recoverStockProfileAIQueueLeases(ctx context.Context, all bool) {
	if s.store == nil {
		return
	}
	now := time.Now()
	var (
		runIDs []string
		err    error
	)
	if all {
		runIDs, err = s.store.RecoverRunningStockProfileAILeases(ctx, now)
	} else {
		runIDs, err = s.store.RecoverExpiredStockProfileAILeases(ctx, now)
	}
	if err != nil {
		if s.log != nil {
			s.log.Warn("recover stock profile ai queue leases failed", "error", safelog.Text(err.Error(), 240))
		}
		return
	}
	for _, runID := range runIDs {
		run, getErr := s.store.GetAgentRun(ctx, runID)
		if getErr != nil {
			continue
		}
		s.markStockProfileAIQueued(ctx, run.TriggerObjectID)
	}
}

func (s *Service) migrateLegacyStockProfileAIRuns(ctx context.Context) {
	if s.store == nil {
		return
	}
	runs, err := s.store.ListActiveStockProfileAgentRuns(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("list legacy stock profile ai runs failed", "error", safelog.Text(err.Error(), 240))
		}
		return
	}
	grouped := make(map[string][]AgentRun)
	order := make([]string, 0, len(runs))
	for _, run := range runs {
		if _, ok := grouped[run.TriggerObjectID]; !ok {
			order = append(order, run.TriggerObjectID)
		}
		grouped[run.TriggerObjectID] = append(grouped[run.TriggerObjectID], run)
	}
	for _, symbol := range order {
		if ctx.Err() != nil {
			return
		}
		symbolRuns := grouped[symbol]
		profile, profileErr := s.store.GetStockProfile(ctx, symbol)
		if profileErr != nil {
			if errors.Is(profileErr, ErrStockProfileNotFound) {
				for _, run := range symbolRuns {
					_, _ = s.store.FinalizeLegacyStockProfileAIRunMigration(
						ctx, run.ID, symbol, StockProfileAIQueueStatusFailed,
						"cannot migrate stock profile ai run: stock profile is unavailable",
					)
				}
			} else if s.log != nil {
				s.log.Warn("load stock profile for legacy ai migration failed", "symbol", symbol, "error", safelog.Text(profileErr.Error(), 240))
			}
			continue
		}
		newestRunCreatedAt := symbolRuns[len(symbolRuns)-1].CreatedAt
		if profile.AIProfileStatus == StockProfileAIStatusReady &&
			!profile.AIProfileUpdatedAt.IsZero() && !profile.AIProfileUpdatedAt.Before(newestRunCreatedAt) {
			for _, run := range symbolRuns {
				if _, finalizeErr := s.store.FinalizeLegacyStockProfileAIRunMigration(
					ctx, run.ID, symbol, StockProfileAIQueueStatusCompleted, "satisfied by a newer stock profile",
				); finalizeErr != nil && s.log != nil {
					s.log.Warn("finalize satisfied legacy stock profile ai run failed", "run_id", run.ID, "symbol", symbol, "error", safelog.Text(finalizeErr.Error(), 240))
				}
			}
			continue
		}
		recentBySymbol, recentErr := s.store.ListRecentAnnouncementsBySymbols(ctx, []string{profile.Symbol}, 100)
		recent := recentBySymbol[profile.Symbol]
		sourceStatuses := []AssetMaintenanceSourceStatus{{Source: "restart_recovery", Status: "success", CheckedAt: time.Now()}}
		if recentErr != nil {
			recent = nil
			sourceStatuses = append(sourceStatuses, AssetMaintenanceSourceStatus{
				Source: "restart_recovery_announcements", Status: "failed", Message: safelog.Text(recentErr.Error(), 240), CheckedAt: time.Now(),
			})
		}
		decision := AssetAIDecisionRetry
		if normalizeStockProfileAIStatus(profile.AIProfileStatus) == StockProfileAIStatusMissing ||
			strings.TrimSpace(profile.ProfileTextZh+profile.ProfileTextEn) == "" {
			decision = AssetAIDecisionMissing
		}
		item := AssetMaintenanceItem{
			Symbol:                profile.Symbol,
			Market:                profile.Market,
			InstrumentType:        profile.InstrumentType,
			Name:                  profile.Name,
			BaseProfileHashBefore: profile.BaseProfileHash,
			BaseProfileHashAfter:  profile.BaseProfileHash,
			StartedAt:             time.Now(),
		}
		pack := s.buildStockProfileSummaryContext(
			ctx, profile, profile, item,
			announcementsAfterAIProfile(recent, profile.AIProfileUpdatedAt),
			sourceStatuses,
		)
		queueInput, queueInputErr := newStockProfileAIQueueItem(pack, decision, "restart-recovery", false)
		if queueInputErr != nil {
			continue
		}
		if _, enqueueErr := s.store.EnqueueStockProfileAIIfAbsent(ctx, queueInput); enqueueErr != nil {
			if s.log != nil {
				s.log.Warn("enqueue legacy stock profile ai run failed", "symbol", symbol, "error", safelog.Text(enqueueErr.Error(), 240))
			}
			continue
		}
		for _, run := range symbolRuns {
			if _, finalizeErr := s.store.FinalizeLegacyStockProfileAIRunMigration(
				ctx, run.ID, symbol, "", "migrated to durable stock profile ai queue",
			); finalizeErr != nil && s.log != nil {
				s.log.Warn("finalize legacy stock profile ai run failed", "run_id", run.ID, "symbol", symbol, "error", safelog.Text(finalizeErr.Error(), 240))
			}
		}
		if latestQueue, getQueueErr := s.store.GetStockProfileAIQueueItem(ctx, symbol); getQueueErr == nil {
			status, message := stockProfileAIQueueState(latestQueue)
			_ = s.updateStockProfileAIState(ctx, symbol, status, message, false)
		}
	}
}

func (s *Service) processNextStockProfileAIQueueItem(ctx context.Context, workerID string) error {
	if s.store == nil {
		return ErrStockProfileAIQueueEmpty
	}
	lease, err := s.store.ClaimStockProfileAI(ctx, workerID, time.Now(), stockProfileAILeaseTTL)
	if err != nil {
		return err
	}
	var pack StockProfileSummaryContext
	if err := json.Unmarshal([]byte(lease.PayloadJSON), &pack); err != nil {
		_ = s.retryStockProfileAI(ctx, lease, time.Now(), "invalid persisted queue payload", true)
		return fmt.Errorf("decode stock profile ai queue payload for %s: %w", lease.Symbol, err)
	}
	if assetMaintenanceSourceFailed(pack.SourceStatuses, StockV2AnnouncementSourceCninfo) {
		if err := s.store.DeferStockProfileAI(ctx, lease, time.Now().Add(time.Hour), "announcement context is not fresh"); err != nil {
			return err
		}
		s.markStockProfileAIQueued(ctx, lease.Symbol)
		return nil
	}

	run, ledger, modelName, err := s.prepareStockProfileSummaryAgentRun(ctx, pack, lease.RequestedBy)
	if err != nil {
		decision := stockProfileAIDecisionForError(err)
		terminal := decision == StockProfileAIDecisionSkippedNotConfigured ||
			decision == StockProfileAIDecisionSkippedUnavailable
		_ = s.retryStockProfileAI(ctx, lease, time.Now().Add(stockProfileAIRetryDelay(lease.AttemptCount)), err.Error(), terminal)
		return err
	}
	if err := s.store.BindStockProfileAIRun(ctx, lease, run.ID); err != nil {
		superseded := run
		superseded.Status = AgentRunStatusSuperseded
		superseded.ErrorMessage = "queue lease lost before execution"
		superseded.FinishedAt = time.Now()
		_, _ = s.store.UpdateAgentRun(ctx, superseded)
		return err
	}
	lease.CurrentAgentRunID = run.ID
	_ = s.updateStockProfileAIState(ctx, lease.Symbol, StockProfileAIStatusRunning, "", false)

	heartbeatDone := make(chan struct{})
	go s.renewStockProfileAILease(ctx, lease, heartbeatDone)
	s.executeStockProfileAgentRun(ctx, run, ledger, pack, modelName)
	close(heartbeatDone)

	finished, getErr := s.store.GetAgentRun(ctx, run.ID)
	if getErr != nil {
		_ = s.retryStockProfileAI(ctx, lease, time.Now().Add(time.Minute), getErr.Error(), false)
		return getErr
	}
	if finished.Status == AgentRunStatusCompleted || finished.Status == AgentRunStatusSuperseded {
		requeued, completeErr := s.store.CompleteStockProfileAI(ctx, lease)
		if completeErr != nil {
			return completeErr
		}
		if requeued {
			s.markStockProfileAIQueued(ctx, lease.Symbol)
		}
		return nil
	}
	terminal := lease.AttemptCount >= stockProfileAIMaxAttempts
	if err := s.retryStockProfileAI(ctx, lease, time.Now().Add(stockProfileAIRetryDelay(lease.AttemptCount)), finished.ErrorMessage, terminal); err != nil {
		return err
	}
	return nil
}

func (s *Service) retryStockProfileAI(
	ctx context.Context,
	lease StockProfileAIQueueLease,
	availableAt time.Time,
	message string,
	terminal bool,
) error {
	if err := s.store.RetryStockProfileAI(ctx, lease, availableAt, message, terminal); err != nil {
		return err
	}
	item, err := s.store.GetStockProfileAIQueueItem(ctx, lease.Symbol)
	if err != nil {
		return err
	}
	aiStatus := StockProfileAIStatusQueued
	if item.Status == StockProfileAIQueueStatusFailed {
		aiStatus = StockProfileAIStatusFailed
	}
	profileStatus := aiStatus
	if profile, profileErr := s.store.GetStockProfile(ctx, lease.Symbol); profileErr == nil &&
		aiStatus == StockProfileAIStatusFailed && profile.AIProfileStatus == StockProfileAIStatusNotConfigured {
		profileStatus = StockProfileAIStatusNotConfigured
	}
	profileMessage := ""
	markAttempt := false
	if aiStatus == StockProfileAIStatusFailed {
		profileMessage = safelog.Text(message, 500)
		markAttempt = true
	}
	return s.updateStockProfileAIState(ctx, lease.Symbol, profileStatus, profileMessage, markAttempt)
}

func (s *Service) renewStockProfileAILease(ctx context.Context, lease StockProfileAIQueueLease, done <-chan struct{}) {
	ticker := time.NewTicker(stockProfileAILeaseRenewal)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := s.store.RenewStockProfileAILease(ctx, lease, time.Now().Add(stockProfileAILeaseTTL)); err != nil {
				if s.log != nil && !errors.Is(err, ErrStockProfileAIQueueLeaseStale) {
					s.log.Warn("renew stock profile ai queue lease failed", "worker", lease.LeaseOwner, "symbol", lease.Symbol, "error", safelog.Text(err.Error(), 240))
				}
				return
			}
		}
	}
}

func stockProfileAIRetryDelay(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return time.Minute
	case attempt == 2:
		return 5 * time.Minute
	case attempt == 3:
		return 30 * time.Minute
	default:
		return 6 * time.Hour
	}
}

func (s *Service) markStockProfileAIQueued(ctx context.Context, symbol string) {
	_ = s.updateStockProfileAIState(ctx, symbol, StockProfileAIStatusQueued, "", false)
}

func (s *Service) GetStockProfileAIQueueStats(ctx context.Context) (StockProfileAIQueueStats, error) {
	return s.store.GetStockProfileAIQueueStats(ctx)
}
