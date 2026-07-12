package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: one worker is the safe default on the deployed two-vCPU/3.5 GiB
	// host. A second worker requires a later resource-aware claim gate.
	stockProfileAIWorkerCount          = 1
	stockProfileAILeaseTTL             = 2 * time.Minute
	stockProfileAILeaseRenewal         = 30 * time.Second
	stockProfileAIQueuePoll            = 2 * time.Second
	stockProfileAIMaxAttempts          = 5
	stockProfileAIReconcileBatchSize   = 500
	stockProfileAIReconcileCursorScope = "stock_profile_ai_reconcile"
)

type stockProfileAIOutboxEnqueueError struct {
	DesiredInputVersion string
	Err                 error
}

func (e *stockProfileAIOutboxEnqueueError) Error() string { return e.Err.Error() }
func (e *stockProfileAIOutboxEnqueueError) Unwrap() error { return e.Err }

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
	unlock := s.lockStockProfile(pack.Profile.Symbol)
	defer unlock()
	baseHash := strings.TrimSpace(pack.Profile.BaseProfileHash)
	if baseHash == "" {
		baseHash = stockProfileAIInputHash(pack.Profile)
	}
	var requiredCutoff time.Time
	for _, status := range pack.SourceStatuses {
		if status.Source == StockV2AnnouncementSourceCninfo &&
			strings.Contains(status.Message, "exchange-wide") && status.CheckedAt.After(requiredCutoff) {
			requiredCutoff = status.CheckedAt
		}
	}
	dataSummaryVersion, err := stockProfileDataSummaryVersionWithQuerier(
		ctx, s.store.assetDB(), pack.Profile.Symbol,
	)
	if err != nil {
		return StockProfileAIQueueItem{}, err
	}
	target, err := s.store.EnsureStockProfileAITarget(
		ctx, pack.Profile.Symbol, baseHash, dataSummaryVersion, decision, force, requiredCutoff,
	)
	if err != nil {
		return StockProfileAIQueueItem{}, err
	}
	queueInput := StockProfileAIQueueItem{
		Symbol:              pack.Profile.Symbol,
		Market:              pack.Profile.Market,
		Status:              queueStatus,
		Priority:            target.DesiredPriority,
		TriggerReason:       target.DesiredTriggerReason,
		RequestedBy:         firstNonEmpty(requestedBy, "system"),
		DesiredInputVersion: target.DesiredInputVersion,
		AvailableAt:         availableAt,
	}
	if target.DesiredInputVersion == target.AppliedInputVersion {
		_ = s.store.UpdateStockProfileAIState(ctx, pack.Profile.Symbol, StockProfileAIStatusReady, "", false)
		queueInput.Status = StockProfileAIQueueStatusCompleted
		queueInput.CompletedInputVersion = target.AppliedInputVersion
		return queueInput, nil
	}
	if err := s.store.UpdateStockProfileAIState(ctx, pack.Profile.Symbol, StockProfileAIStatusQueued, "", false); err != nil {
		return StockProfileAIQueueItem{}, err
	}
	enqueue := s.store.EnqueueStockProfileAI
	if decision == AssetAIDecisionRetry {
		enqueue = s.store.ReviveStockProfileAI
	}
	item, err := enqueue(ctx, queueInput)
	if err != nil {
		// DuckDB target is the outbox; the reconciler repairs a transient SQLite
		// enqueue failure without losing the requested version.
		return StockProfileAIQueueItem{}, &stockProfileAIOutboxEnqueueError{
			DesiredInputVersion: target.DesiredInputVersion,
			Err:                 err,
		}
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

func stockProfileAIQueueState(item StockProfileAIQueueItem) (string, string) {
	switch item.Status {
	case StockProfileAIQueueStatusRunning, StockProfileAIQueueStatusApplyPending, StockProfileAIQueueStatusApplying:
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
		if s.currentResourceGate().State == ResourceGateNormal {
			if err := s.processNextStockProfileAIQueueItem(ctx, workerID); err != nil &&
				!errors.Is(err, ErrStockProfileAIQueueEmpty) && !errors.Is(err, context.Canceled) && s.log != nil {
				s.log.Warn("stock profile ai queue worker failed", "worker", workerID, "error", safelog.Text(err.Error(), 300))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) runStockProfileAIQueueBackground(ctx context.Context) {
	s.recoverStockProfileAIQueueLeases(ctx, true)
	s.reconcileStockProfileAIQueue(ctx)

	workerDone := make(chan struct{}, stockProfileAIWorkerCount)
	for worker := 0; worker < stockProfileAIWorkerCount; worker++ {
		workerID := fmt.Sprintf("stock-profile-ai-%d", worker+1)
		go func() {
			s.runStockProfileAIQueueWorker(ctx, workerID)
			workerDone <- struct{}{}
		}()
	}
	leaseTicker := time.NewTicker(stockProfileAILeaseTTL)
	reconcileTicker := time.NewTicker(15 * time.Second)
	defer leaseTicker.Stop()
	defer reconcileTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			for worker := 0; worker < stockProfileAIWorkerCount; worker++ {
				<-workerDone
			}
			return
		case <-leaseTicker.C:
			s.recoverStockProfileAIQueueLeases(ctx, false)
		case <-reconcileTicker.C:
			s.reconcileStockProfileAIQueue(ctx)
		}
	}
}

func (s *Service) reconcileStockProfileAIQueue(ctx context.Context) {
	if s.store == nil {
		return
	}
	if err := s.reconcileStockProfileAIQueueBatch(ctx, stockProfileAIReconcileBatchSize); err != nil && s.log != nil {
		s.log.Warn("reconcile stock profile AI targets failed", "error", safelog.Text(err.Error(), 240))
	}
}

func (s *Service) reconcileStockProfileAIQueueBatch(ctx context.Context, limit int) error {
	cursor, err := s.store.GetAssetMaintenanceCursor(ctx, stockProfileAIReconcileCursorScope)
	if err != nil {
		return err
	}
	targets, err := s.store.ListPendingStockProfileAITargetsAfter(ctx, cursor, limit)
	if err != nil {
		return err
	}
	targets, err = s.store.RefreshPendingStockProfileAIDataSummaries(ctx, targets)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if target.DesiredInputVersion == target.AppliedInputVersion {
			continue
		}
		_, err = s.store.EnqueueStockProfileAI(ctx, StockProfileAIQueueItem{
			Symbol:              target.Symbol,
			Status:              StockProfileAIQueueStatusReady,
			Priority:            target.DesiredPriority,
			TriggerReason:       firstNonEmpty(target.DesiredTriggerReason, AssetAIDecisionMissing),
			RequestedBy:         "state-reconciler",
			DesiredInputVersion: target.DesiredInputVersion,
		})
		if err != nil {
			return fmt.Errorf("reconcile stock profile AI queue item %s: %w", target.Symbol, err)
		}
		if err := s.store.SyncAssetMaintenanceItemsAIQueueByVersion(
			ctx, target.Symbol, target.DesiredInputVersion,
		); err != nil {
			return err
		}
	}
	if len(targets) == 0 {
		return nil
	}
	// ponytail: advance only after the whole idempotent batch is present in
	// SQLite. A crash before this write merely repeats the batch; it cannot skip it.
	return s.store.SetAssetMaintenanceCursor(ctx, stockProfileAIReconcileCursorScope, targets[len(targets)-1].Symbol)
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

func (s *Service) processNextStockProfileAIQueueItem(ctx context.Context, workerID string) error {
	if s.store == nil {
		return ErrStockProfileAIQueueEmpty
	}
	if err := s.processNextStockProfileAIApply(ctx, workerID); err == nil {
		return nil
	} else if !errors.Is(err, ErrStockProfileAIQueueEmpty) {
		return err
	}
	lease, err := s.store.ClaimStockProfileAI(ctx, workerID, time.Now(), stockProfileAILeaseTTL)
	if err != nil {
		return err
	}
	pack, err := s.buildStockProfileAIContextForLease(ctx, lease)
	if err != nil {
		if errors.Is(err, ErrStockProfileAIQueueLeaseStale) {
			_ = s.store.SupersedeStockProfileAIRun(ctx, lease)
			return nil
		}
		_ = s.retryStockProfileAI(ctx, lease, time.Now().Add(time.Minute), err.Error(), false)
		return err
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
	if finished.Status == AgentRunStatusCompleted {
		if applyErr := s.processNextStockProfileAIApply(ctx, workerID); applyErr != nil &&
			!errors.Is(applyErr, ErrStockProfileAIQueueEmpty) {
			return applyErr
		}
		return nil
	}
	if finished.Status == AgentRunStatusSuperseded {
		return s.store.SupersedeStockProfileAIRun(ctx, lease)
	}
	terminal := lease.AttemptCount >= stockProfileAIMaxAttempts
	if err := s.retryStockProfileAI(ctx, lease, time.Now().Add(stockProfileAIRetryDelay(lease.AttemptCount)), finished.ErrorMessage, terminal); err != nil {
		return err
	}
	return nil
}

func (s *Service) buildStockProfileAIContextForLease(
	ctx context.Context,
	lease StockProfileAIQueueLease,
) (StockProfileSummaryContext, error) {
	if _, _, err := s.store.RefreshPendingStockProfileAIDataSummary(ctx, lease.Symbol); err != nil {
		return StockProfileSummaryContext{}, err
	}
	current, err := s.store.StockProfileAITargetCurrent(ctx, lease.Symbol, lease.ClaimedInputVersion)
	if err != nil {
		return StockProfileSummaryContext{}, err
	}
	if !current {
		return StockProfileSummaryContext{}, ErrStockProfileAIQueueLeaseStale
	}
	profile, err := s.store.GetStockProfile(ctx, lease.Symbol)
	if err != nil {
		return StockProfileSummaryContext{}, err
	}
	state, exists, err := s.store.GetStockProfileAIState(ctx, lease.Symbol)
	if err != nil || !exists {
		if err == nil {
			err = ErrStockProfileAIQueueLeaseStale
		}
		return StockProfileSummaryContext{}, err
	}
	previousSummary := StockProfilePreviousSummary{}
	if state.AppliedInputVersion != "" {
		version, found, versionErr := s.store.GetStockProfileAIVersion(
			ctx, lease.Symbol, state.AppliedInputVersion,
		)
		if versionErr != nil {
			return StockProfileSummaryContext{}, versionErr
		}
		if !found {
			return StockProfileSummaryContext{}, fmt.Errorf(
				"applied stock profile AI version %s is missing", state.AppliedInputVersion,
			)
		}
		previousSummary, err = stockProfilePreviousSummaryFromVersion(version)
		if err != nil {
			return StockProfileSummaryContext{}, err
		}
	}
	bySymbol, err := s.store.ListRecentAnnouncementsBySymbols(ctx, []string{lease.Symbol}, 100)
	if err != nil {
		return StockProfileSummaryContext{}, err
	}
	sourceStatuses := []AssetMaintenanceSourceStatus{{
		Source: "durable_context", Status: "success", CheckedAt: time.Now(),
	}}
	if normalizeInstrumentType(profile.InstrumentType) == InstrumentTypeStock && !state.RequiredMessageCutoffAt.IsZero() {
		syncState, synced, syncErr := s.store.GetAnnouncementSyncState(
			ctx, StockV2AnnouncementSourceCninfo, profile.Market,
		)
		if syncErr != nil {
			return StockProfileSummaryContext{}, syncErr
		}
		if !synced || syncState.CoveredThrough.Before(state.RequiredMessageCutoffAt) {
			sourceStatuses = append(sourceStatuses, AssetMaintenanceSourceStatus{
				Source: StockV2AnnouncementSourceCninfo, Status: AssetAnnouncementStatusFailed,
				Message: "announcement cursor has not reached the required AI cutoff", CheckedAt: time.Now(),
			})
		}
	}
	item := AssetMaintenanceItem{
		Symbol: profile.Symbol, Market: profile.Market, InstrumentType: profile.InstrumentType,
		Name: profile.Name, BaseProfileHashBefore: state.BaseProfileHash,
		BaseProfileHashAfter: state.BaseProfileHash, StartedAt: time.Now(),
	}
	pack := s.buildStockProfileSummaryContext(
		ctx, profile, profile, item, bySymbol[lease.Symbol], sourceStatuses,
	)
	pack.PreviousSummary = previousSummary
	return pack, nil
}

func stockProfilePreviousSummaryFromVersion(version StockProfileAIVersion) (StockProfilePreviousSummary, error) {
	var result map[string]any
	if err := json.Unmarshal([]byte(version.ResultJSON), &result); err != nil {
		return StockProfilePreviousSummary{}, wrapError(err, "decode previous stock profile AI version")
	}
	return StockProfilePreviousSummary{
		BusinessSummaryZh: firstProfileResultString(result, "summaryZh", "businessSummaryZh"),
		BusinessSummaryEn: firstProfileResultString(result, "summaryEn", "businessSummaryEn"),
		AIProfileModel:    version.ModelName,
		UpdatedAt:         version.CreatedAt,
	}, nil
}

func (s *Service) processNextStockProfileAIApply(ctx context.Context, workerID string) error {
	lease, err := s.store.ClaimStockProfileAIApply(ctx, workerID, time.Now(), stockProfileAILeaseTTL)
	if err != nil {
		return err
	}
	requeued, err := s.applyStockProfileAILease(ctx, lease)
	if errors.Is(err, ErrStockProfileAIQueueLeaseStale) {
		s.reconcileStockProfileAIQueue(ctx)
		return nil
	}
	if err != nil {
		return err
	}
	if requeued {
		s.markStockProfileAIQueued(ctx, lease.Symbol)
	}
	return nil
}

func (s *Service) applyStockProfileAILease(ctx context.Context, lease StockProfileAIQueueLease) (bool, error) {
	unlock := s.lockStockProfile(lease.Symbol)
	defer unlock()
	var result map[string]any
	if err := json.Unmarshal([]byte(lease.ResultJSON), &result); err != nil || len(result) == 0 {
		_ = s.store.RetryStockProfileAIApply(ctx, lease, time.Now().Add(6*time.Hour), "invalid staged AI result")
		return false, ErrInvalidStockProfileEnhancement
	}
	profile, err := s.store.GetStockProfile(ctx, lease.Symbol)
	if err != nil {
		_ = s.store.RetryStockProfileAIApply(ctx, lease, time.Now().Add(time.Minute), err.Error())
		return false, err
	}
	profile, err = s.stockProfileWithoutAppliedAI(ctx, profile)
	if err != nil {
		_ = s.store.RetryStockProfileAIApply(ctx, lease, time.Now().Add(time.Minute), err.Error())
		return false, err
	}
	baseTerms := stockProfileAIBaseTermsFromProfile(profile)
	profile, err = stockProfileWithEnhancement(profile, result, lease.ResultModel, lease.ResultConfidence, time.Now())
	if err != nil {
		_ = s.store.RetryStockProfileAIApply(ctx, lease, time.Now().Add(6*time.Hour), err.Error())
		return false, err
	}
	state, _, _ := s.store.GetStockProfileAIState(ctx, lease.Symbol)
	manifest, _ := json.Marshal(map[string]any{
		"schema":                  stockProfileAIInputSchemaVersion,
		"baseProfileHash":         state.BaseProfileHash,
		"announcementRevision":    state.AnnouncementRevision,
		"dataSummaryVersion":      state.DataSummaryVersion,
		"requiredMessageCutoffAt": state.RequiredMessageCutoffAt,
		"baseTerms":               baseTerms,
	})
	if _, err := s.store.ApplyStockProfileAIResult(ctx, lease, profile, string(manifest)); err != nil {
		if errors.Is(err, ErrStockProfileAIQueueLeaseStale) {
			return false, err
		}
		_ = s.store.RetryStockProfileAIApply(ctx, lease, time.Now().Add(time.Minute), err.Error())
		return false, err
	}
	s.markStockProfileEmbeddingStale(ctx, lease.Symbol)
	requeued, err := s.store.CompleteStockProfileAIApply(ctx, lease)
	if err != nil {
		return false, err
	}
	return requeued, nil
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
