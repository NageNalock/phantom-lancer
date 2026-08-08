package stockv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) BuildStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	result, err := s.UpdateStockProfile(ctx, RequestUpdateStockProfile{
		Symbol:        symbol,
		TriggerSource: StockProfileUpdateTriggerManual,
		TriggerReason: "build_compat",
		RequestedBy:   "system",
	})
	if err != nil {
		return StockProfile{}, err
	}
	return result.Profile, nil
}

func (s *Service) UpdateStockProfile(ctx context.Context, req RequestUpdateStockProfile) (StockProfileUpdateResult, error) {
	normalizedSymbol, _ := normalizeQuoteSymbolInput(req.Symbol)
	if normalizedSymbol == "" {
		normalizedSymbol = strings.TrimSpace(req.Symbol)
	}
	trigger := normalizeStockProfileUpdateTrigger(req.TriggerSource)
	startedAt := time.Now()
	task := StockProfileUpdateTask{
		ID:              generateID(),
		Symbol:          normalizedSymbol,
		TriggerSource:   trigger,
		TriggerReason:   strings.TrimSpace(req.TriggerReason),
		Status:          StockProfileUpdateStatusCompleted,
		AIDecision:      StockProfileAIDecisionSkippedUnchanged,
		AIProfileStatus: StockProfileAIStatusMissing,
		StartedAt:       startedAt,
		CreatedAt:       startedAt,
		UpdatedAt:       startedAt,
	}

	existing, err := s.store.GetStockProfile(ctx, normalizedSymbol)
	if err != nil && !errors.Is(err, ErrStockProfileNotFound) {
		task.Status = StockProfileUpdateStatusFailed
		task.BaseProfileStatus = StockProfileUpdateBaseStatusFailed
		task.ErrorMessage = safelog.Text(err.Error(), 500)
		task.FinishedAt = time.Now()
		_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
		return StockProfileUpdateResult{}, err
	}
	if err == nil {
		task.AIProfileStatus = normalizeStockProfileAIStatus(existing.AIProfileStatus)
		if latestTasks, listErr := s.store.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: normalizedSymbol, Limit: 1}); listErr == nil && len(latestTasks) > 0 && latestTasks[0].BaseInputHashAfter != "" {
			task.BaseInputHashBefore = latestTasks[0].BaseInputHashAfter
		} else {
			task.BaseInputHashBefore = stockProfileAIInputHash(existing)
		}
	}
	instrument, err := s.store.GetInstrument(ctx, normalizedSymbol)
	if err != nil {
		task.Status = StockProfileUpdateStatusFailed
		task.BaseProfileStatus = StockProfileUpdateBaseStatusFailed
		task.ErrorMessage = safelog.Text(err.Error(), 500)
		task.FinishedAt = time.Now()
		_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
		return StockProfileUpdateResult{}, err
	}
	task.Market = instrument.Market
	baseProfile, sourceStatuses := s.stockProfileBaseFromInstrumentWithSourceStatuses(ctx, instrument, true)
	profile := s.mergeStockProfileExisting(ctx, baseProfile)
	task.SourceStatuses = sourceStatuses
	task.BaseInputHashAfter = stockProfileAIInputHash(baseProfile)
	task.BaseInputChanged = task.BaseInputHashBefore == "" || task.BaseInputHashBefore != task.BaseInputHashAfter

	profile, err = s.store.UpsertStockProfile(ctx, profile)
	if err != nil {
		task.Status = StockProfileUpdateStatusFailed
		task.BaseProfileStatus = StockProfileUpdateBaseStatusFailed
		task.ErrorMessage = safelog.Text(err.Error(), 500)
		task.FinishedAt = time.Now()
		_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
		return StockProfileUpdateResult{}, err
	}
	task.BaseProfileStatus = StockProfileUpdateBaseStatusReady
	task.AIProfileStatus = normalizeStockProfileAIStatus(profile.AIProfileStatus)
	s.markStockProfileEmbeddingStale(ctx, profile)

	var agentRun *AgentRun
	var agentLedger AgentDecisionLedger
	var agentRunModelName string
	var strictErr error
	if req.ForceAI || task.BaseInputChanged || stockProfileAIRequiresRefresh(task.AIProfileStatus) {
		run, ledger, modelName, runErr := s.prepareStockProfileSummaryAgentRun(ctx, profile, req.RequestedBy)
		if runErr != nil {
			task.AIDecision = stockProfileAIDecisionForError(runErr)
			task.AIProfileStatus = stockProfileAITaskStatusForDecision(task.AIDecision)
			task.AIProfileError = safelog.Text(runErr.Error(), 500)
			task.Status = StockProfileUpdateStatusPartial
			if task.AIDecision != StockProfileAIDecisionSkippedNotConfigured || req.StrictAI {
				task.ErrorMessage = safelog.Text(runErr.Error(), 500)
			}
			if req.StrictAI {
				task.Status = StockProfileUpdateStatusFailed
				strictErr = runErr
			}
		} else {
			task.Status = StockProfileUpdateStatusRunning
			task.AIDecision = StockProfileAIDecisionCalled
			task.AgentRunID = run.ID
			task.AIProfileStatus = StockProfileUpdateAIStatusRunning
			agentRun = &run
			agentLedger = ledger
			agentRunModelName = modelName
		}
	} else {
		task.AIDecision = StockProfileAIDecisionSkippedUnchanged
		task.AIProfileStatus = normalizeStockProfileAIStatus(profile.AIProfileStatus)
	}

	if task.Status != StockProfileUpdateStatusRunning {
		task.FinishedAt = time.Now()
	}
	createdTask, createErr := s.store.CreateStockProfileUpdateTask(ctx, task)
	if createErr != nil {
		if agentRun != nil {
			failedRun := *agentRun
			failedRun.Status = AgentRunStatusFailed
			failedRun.ErrorMessage = safelog.Text("create stock profile update task failed: "+createErr.Error(), 500)
			failedRun.FinishedAt = time.Now()
			_, _ = s.store.UpdateAgentRun(ctx, failedRun)
		}
		return StockProfileUpdateResult{}, createErr
	}
	if strictErr != nil {
		return StockProfileUpdateResult{Profile: profile, Task: createdTask}, strictErr
	}
	if agentRun != nil {
		go s.startStockProfileAgentRunAsync(context.Background(), *agentRun, agentLedger, profile, agentRunModelName)
	}
	return StockProfileUpdateResult{Profile: profile, Task: createdTask, AgentRun: agentRun}, nil
}

func (s *Service) ListStockProfileUpdateTasks(ctx context.Context, filter StockProfileUpdateTaskListFilter) ([]StockProfileUpdateTask, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 500)
	filter.Offset = normalizedPageOffset(filter.Offset)
	return s.store.ListStockProfileUpdateTasks(ctx, filter)
}

func (s *Service) CountStockProfileUpdateTasks(ctx context.Context, filter StockProfileUpdateTaskListFilter) (int, error) {
	return s.store.CountStockProfileUpdateTasks(ctx, filter)
}

func (s *Service) RebuildStockProfiles(ctx context.Context) (RebuildStockProfilesResult, error) {
	total, err := s.store.CountInstruments(ctx)
	if err != nil {
		return RebuildStockProfilesResult{}, err
	}
	result := RebuildStockProfilesResult{Total: total, UpdatedAt: time.Now()}
	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		instruments, err := s.store.GetInstruments(ctx, pageSize, offset)
		if err != nil {
			return result, err
		}
		if len(instruments) == 0 {
			break
		}
		for _, instrument := range instruments {
			profile := buildStockProfileFromInstrument(instrument)
			existing, getErr := s.store.GetStockProfile(ctx, instrument.Symbol)
			switch {
			case getErr == nil:
				profile = mergeStockProfileAIFields(profile, existing)
				// ponytail: hourly rebuilds usually see unchanged master data; skip the
				// DuckDB write instead of adding a separate profile-diff subsystem.
				if stockProfileContentEqual(existing, profile) {
					result.Success++
					continue
				}
			case errors.Is(getErr, ErrStockProfileNotFound):
			default:
				result.Failed++
				result.FailedItems = append(result.FailedItems, UpdateFailure{
					Symbol: instrument.Symbol,
					Reason: stockProfileSnippet(getErr.Error(), 240),
				})
				continue
			}
			updated, err := s.store.UpsertStockProfile(ctx, profile)
			if err != nil {
				result.Failed++
				result.FailedItems = append(result.FailedItems, UpdateFailure{
					Symbol: instrument.Symbol,
					Reason: stockProfileSnippet(err.Error(), 240),
				})
				continue
			}
			s.markStockProfileEmbeddingStale(ctx, updated)
			result.Success++
		}
	}
	return result, nil
}

func stockProfileContentEqual(left, right StockProfile) bool {
	left.UpdatedAt = time.Time{}
	right.UpdatedAt = time.Time{}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

type stockProfileDeepUpdateOptions struct {
	SymbolBudget      int
	AIRoundsPerSymbol int
	RateLimit         time.Duration
	Now               time.Time
	RequestedBy       string
}

var errStockProfileMaintenanceDeferred = errors.New("stock profile maintenance deferred during news context backfill")

func (s *Service) RunAutomaticDeepStockProfileUpdate(ctx context.Context, trigger string) (StockProfileDeepUpdateResult, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return StockProfileDeepUpdateResult{}, err
	}
	return s.runAutomaticDeepStockProfileUpdate(ctx, trigger, stockProfileDeepUpdateOptions{
		SymbolBudget:      settings.BaseProfileDeepUpdateBatchSize,
		AIRoundsPerSymbol: settings.BaseProfileDeepUpdateAIBudget,
		RateLimit:         time.Duration(settings.BaseProfileDeepUpdateRateLimitMs) * time.Millisecond,
		RequestedBy:       "system",
	})
}

func (s *Service) runAutomaticDeepStockProfileUpdate(ctx context.Context, trigger string, opts stockProfileDeepUpdateOptions) (StockProfileDeepUpdateResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	symbolBudget := normalizeStockProfileDeepUpdateBatchSize(opts.SymbolBudget)
	aiRoundsPerSymbol := normalizeStockProfileDeepUpdateAIBudget(opts.AIRoundsPerSymbol)
	rateLimit := opts.RateLimit
	if rateLimit < 0 {
		rateLimit = 0
	}
	requestedBy := strings.TrimSpace(opts.RequestedBy)
	if requestedBy == "" {
		requestedBy = "system"
	}
	result := StockProfileDeepUpdateResult{
		SymbolBudget:      symbolBudget,
		AIRoundsPerSymbol: aiRoundsPerSymbol,
		AIBudget:          aiRoundsPerSymbol,
		RateLimitMs:       int(rateLimit / time.Millisecond),
		UpdatedAt:         now,
	}
	if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
		return result, errStockProfileMaintenanceDeferred
	}
	headLimit := symbolBudget * 8
	if headLimit < symbolBudget {
		headLimit = symbolBudget
	}
	candidates, err := s.store.ListStockProfileDeepUpdateCandidates(ctx, headLimit)
	if err != nil {
		return result, err
	}
	seed := now.Format("2006-01-02")
	// ponytail: 现阶段用“旧任务优先 + 每日稳定 hash”当滚动队列；需要强 SLA 时再加持久化队列表。
	sort.SliceStable(candidates, func(i, j int) bool {
		leftRepair := normalizeStockProfileAIStatus(candidates[i].AIProfileStatus) == StockProfileAIStatusFailed
		rightRepair := normalizeStockProfileAIStatus(candidates[j].AIProfileStatus) == StockProfileAIStatusFailed
		if leftRepair != rightRepair {
			return leftRepair
		}
		leftBucket := stockProfileDeepUpdateFreshnessBucket(candidates[i].LastTaskAt)
		rightBucket := stockProfileDeepUpdateFreshnessBucket(candidates[j].LastTaskAt)
		if leftBucket != rightBucket {
			return leftBucket < rightBucket
		}
		return stockProfileScatterRank(candidates[i].Instrument.Symbol, seed) < stockProfileScatterRank(candidates[j].Instrument.Symbol, seed)
	})
	if len(candidates) > symbolBudget {
		candidates = candidates[:symbolBudget]
	}
	result.CandidateCount = len(candidates)

	for i, candidate := range candidates {
		if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
			return result, errStockProfileMaintenanceDeferred
		}
		if i > 0 {
			if err := sleepStockProfileDeepUpdate(ctx, stockProfileDeepUpdateDelay(candidate.Instrument.Symbol, rateLimit, seed)); err != nil {
				return result, err
			}
			if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
				return result, errStockProfileMaintenanceDeferred
			}
		}
		update, err := s.UpdateStockProfile(ctx, RequestUpdateStockProfile{
			Symbol:        candidate.Instrument.Symbol,
			TriggerSource: StockProfileUpdateTriggerAuto,
			TriggerReason: "auto_deep_queue:" + strings.TrimSpace(trigger),
			RequestedBy:   requestedBy,
		})
		result.ProcessedCount++
		if err != nil {
			result.FailedCount++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: candidate.Instrument.Symbol, Reason: stockProfileSnippet(err.Error(), 240)})
			continue
		}
		result.SuccessCount++
		if update.Task.BaseInputChanged {
			result.InputChanged++
		} else {
			result.InputUnchanged++
		}
		if update.Task.AIDecision == StockProfileAIDecisionCalled {
			result.AICalledCount++
		} else {
			result.AISkippedCount++
		}
	}
	result.UpdatedAt = time.Now()
	return result, nil
}

func stockProfileAIRequiresRefresh(status string) bool {
	switch normalizeStockProfileAIStatus(status) {
	case StockProfileAIStatusFailed, StockProfileAIStatusMissing:
		return true
	default:
		return false
	}
}

func (s *Service) runBaseProfileMaintenanceScheduler(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	s.maybeRunBaseProfileMaintenance(ctx, "scheduler")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeRunBaseProfileMaintenance(ctx, "scheduler")
		}
	}
}

func (s *Service) maybeRunBaseProfileMaintenance(ctx context.Context, trigger string) {
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.BaseProfileAutoMaintainEnabled {
		return
	}
	interval := time.Duration(settings.BaseProfileMaintainIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	now := time.Now()
	if !settings.BaseProfileNextMaintainAt.IsZero() && settings.BaseProfileNextMaintainAt.After(now) {
		return
	}
	if settings.BaseProfileNextMaintainAt.IsZero() && !settings.BaseProfileLastMaintainAt.IsZero() && now.Sub(settings.BaseProfileLastMaintainAt) < interval {
		settings.BaseProfileNextMaintainAt = settings.BaseProfileLastMaintainAt.Add(interval)
		_ = s.store.CreateOrUpdateSettings(ctx, settings)
		s.settings = settings
		return
	}
	if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
		return
	}
	if !s.tryStartBackgroundHeavyWork() {
		return
	}
	defer s.finishBackgroundHeavyWork()

	s.baseProfileMu.Lock()
	defer s.baseProfileMu.Unlock()
	settings, err = s.GetSettings(ctx)
	if err != nil || !settings.BaseProfileAutoMaintainEnabled {
		return
	}
	now = time.Now()
	if !settings.BaseProfileNextMaintainAt.IsZero() && settings.BaseProfileNextMaintainAt.After(now) {
		return
	}

	result, runErr := s.RebuildStockProfiles(ctx)
	deepResult := StockProfileDeepUpdateResult{}
	var deepErr error
	if runErr == nil {
		deepResult, deepErr = s.runAutomaticDeepStockProfileUpdate(ctx, trigger, stockProfileDeepUpdateOptions{
			SymbolBudget:      settings.BaseProfileDeepUpdateBatchSize,
			AIRoundsPerSymbol: settings.BaseProfileDeepUpdateAIBudget,
			RateLimit:         time.Duration(settings.BaseProfileDeepUpdateRateLimitMs) * time.Millisecond,
			RequestedBy:       "system",
		})
	}
	if errors.Is(deepErr, errStockProfileMaintenanceDeferred) {
		return
	}
	settings.BaseProfileLastMaintainAt = now
	settings.BaseProfileNextMaintainAt = now.Add(interval)
	if runErr != nil {
		settings.BaseProfileLastMaintainResult = fmt.Sprintf("failed trigger=%s error=%s", trigger, safelog.Text(runErr.Error(), 180))
	} else if deepErr != nil {
		settings.BaseProfileLastMaintainResult = fmt.Sprintf("partial trigger=%s total=%d success=%d failed=%d deepError=%s", trigger, result.Total, result.Success, result.Failed, stockProfileSnippet(deepErr.Error(), 180))
	} else {
		settings.BaseProfileLastMaintainResult = fmt.Sprintf("completed trigger=%s total=%d success=%d failed=%d deepCandidates=%d deepProcessed=%d deepAI=%d aiRoundsPerSymbol=%d deepFailed=%d", trigger, result.Total, result.Success, result.Failed, deepResult.CandidateCount, deepResult.ProcessedCount, deepResult.AICalledCount, deepResult.AIRoundsPerSymbol, deepResult.FailedCount)
	}
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil && s.log != nil {
		s.log.Warn("save base profile maintenance state failed", "trigger", trigger, "error", safelog.Text(err.Error(), 240))
		return
	}
	if s.log != nil && (runErr != nil || deepErr != nil || result.Failed > 0 || deepResult.FailedCount > 0) {
		errText := ""
		if runErr != nil {
			errText = runErr.Error()
		} else if deepErr != nil {
			errText = deepErr.Error()
		}
		s.log.Warn("stock profile maintenance finished with errors", "trigger", trigger, "total_count", result.Total, "success_count", result.Success, "failed_count", result.Failed, "failure_sample", stockV2FailureSample(result.FailedItems, 5), "deep_candidate_count", deepResult.CandidateCount, "deep_processed_count", deepResult.ProcessedCount, "deep_success_count", deepResult.SuccessCount, "deep_failed_count", deepResult.FailedCount, "deep_failure_sample", stockV2FailureSample(deepResult.FailedItems, 5), "error", safelog.Text(errText, 300))
	}
	s.settings = settings
}

func (s *Service) GetStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	normalizedSymbol, _ := normalizeQuoteSymbolInput(symbol)
	if normalizedSymbol == "" {
		normalizedSymbol = strings.TrimSpace(symbol)
	}
	return s.store.GetStockProfile(ctx, normalizedSymbol)
}

func (s *Service) ListStockProfiles(ctx context.Context, filter StockProfileListFilter) ([]StockProfile, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 500)
	filter.Offset = normalizedPageOffset(filter.Offset)
	if filter.InstrumentType != "" {
		filter.InstrumentType = normalizeInstrumentType(filter.InstrumentType)
	}
	return s.store.ListStockProfiles(ctx, filter)
}

func (s *Service) CountStockProfiles(ctx context.Context, filter StockProfileListFilter) (int, error) {
	if filter.InstrumentType != "" {
		filter.InstrumentType = normalizeInstrumentType(filter.InstrumentType)
	}
	return s.store.CountStockProfiles(ctx, filter)
}

func (s *Service) ListStockProfileSummaries(ctx context.Context, symbols []string) (map[string]StockProfileSummary, error) {
	symbols = compactStringList(symbols, 100)
	out, err := s.store.ListStockProfileSummaries(ctx, symbols)
	if err != nil {
		return nil, err
	}
	for _, symbol := range symbols {
		if _, ok := out[symbol]; !ok {
			out[symbol] = StockProfileSummary{Symbol: symbol, Status: "missing", AIProfileStatus: StockProfileAIStatusMissing}
		}
	}
	return out, nil
}

func (s *Service) RunAgentStockProfileSummary(ctx context.Context, symbol string, requestedBy string) (AgentRun, error) {
	result, err := s.UpdateStockProfile(ctx, RequestUpdateStockProfile{
		Symbol:        symbol,
		TriggerSource: StockProfileUpdateTriggerManual,
		TriggerReason: "legacy_run_agent",
		RequestedBy:   requestedBy,
		ForceAI:       true,
		StrictAI:      true,
	})
	if err != nil {
		return AgentRun{}, err
	}
	if result.AgentRun == nil {
		return AgentRun{}, ErrAgentExecutorUnavailable
	}
	return *result.AgentRun, nil
}

func (s *Service) prepareStockProfileSummaryAgentRun(ctx context.Context, profile StockProfile, requestedBy string) (AgentRun, AgentDecisionLedger, string, error) {
	return s.prepareStockProfileSummaryAgentRunAttempt(ctx, profile, requestedBy, false)
}

func (s *Service) prepareStockProfileSummaryAgentRunAttempt(
	ctx context.Context,
	profile StockProfile,
	requestedBy string,
	fallbackOnly bool,
) (AgentRun, AgentDecisionLedger, string, error) {
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeStockProfileSummary)
	if err != nil {
		return AgentRun{}, AgentDecisionLedger{}, "", err
	}
	modelProfile := taskProfile
	if fallbackOnly {
		modelProfile.PrimaryModelID = ""
	}
	model, err := s.resolveModel(ctx, modelProfile)
	if err != nil {
		if !fallbackOnly {
			profile.AIProfileStatus = StockProfileAIStatusNotConfigured
			profile.AIProfileError = err.Error()
			_, _ = s.store.UpsertStockProfile(ctx, profile)
		}
		return AgentRun{}, AgentDecisionLedger{}, "", err
	}
	if s.agentExecutor == nil {
		return AgentRun{}, AgentDecisionLedger{}, "", ErrAgentExecutorUnavailable
	}
	inputArtifact, _ := json.Marshal(map[string]any{
		"task":    AgentTaskTypeStockProfileSummary,
		"profile": profile,
	})
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeStockProfileSummary,
		ExecutionMode:        taskProfile.ExecutionMode,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		ReasoningEffort:      taskProfile.ReasoningEffort,
		TriggerObjectType:    "stock_profile",
		TriggerObjectID:      profile.Symbol,
		RequestedBy:          requestedBy,
		InputSummary:         fmt.Sprintf("stock_profile_summary symbol=%s market=%s name=%s", profile.Symbol, profile.Market, profile.Name),
		InputArtifactSummary: string(inputArtifact),
	})
	if err != nil {
		return AgentRun{}, AgentDecisionLedger{}, "", err
	}
	return run, ledger, model.ModelName, nil
}

func (s *Service) startStockProfileAgentRunAsync(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, profile StockProfile, modelName string) {
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("stock profile agent run panicked", "run_id", run.ID, "ledger_id", ledger.ID, "symbol", profile.Symbol, "market", profile.Market, "model", modelName, "panic", r)
			}
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()
	if _, _, err := s.executeStockProfileAgentRun(ctx, run, ledger, profile, modelName); err != nil && s.log != nil {
		s.log.Warn("stock profile agent run finished with error", "run_id", run.ID, "ledger_id", ledger.ID, "symbol", profile.Symbol, "model", modelName, "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) executeStockProfileAgentRun(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	profile StockProfile,
	modelName string,
) (AgentRun, AgentDecisionLedger, error) {
	finalRun, finalLedger, output, execErr := s.executeStockProfileAgentAttempt(ctx, run, ledger, profile, modelName)
	if finalRun.Status == AgentRunStatusCompleted ||
		!stockProfileFallbackEligible(ctx, finalRun, output, execErr) {
		return finalRun, finalLedger, stockProfileAttemptError(finalRun, execErr)
	}
	if output != nil {
		if err := ensureExecutorProcessGroupStopped(output.ProcessGroupID); err != nil {
			return finalRun, finalLedger, fmt.Errorf("clean failed stock profile process: %w", err)
		}
	}
	fallbackRun, fallbackLedger, fallbackModelName, err := s.prepareStockProfileSummaryAgentRunAttempt(ctx, profile, "system", true)
	if err != nil || fallbackRun.ModelID == finalRun.ModelID {
		return finalRun, finalLedger, stockProfileAttemptError(finalRun, firstNonNil(execErr, err))
	}
	if err := s.linkStockProfileFallbackAttempt(ctx, finalRun, finalLedger, fallbackRun, fallbackLedger); err != nil {
		s.finalizeAgentRun(ctx, fallbackRun.ID, nil, err)
		return finalRun, finalLedger, stockProfileAttemptError(finalRun, err)
	}
	if s.log != nil {
		s.log.Warn(
			"falling back stock profile agent after recoverable model failure",
			"symbol", profile.Symbol,
			"primary_agent_run_id", finalRun.ID,
			"primary_model_id", finalRun.ModelID,
			"fallback_agent_run_id", fallbackRun.ID,
			"fallback_model_id", fallbackRun.ModelID,
			"error", safelog.Text(firstNonEmpty(finalRun.ErrorMessage, stockProfileErrorString(execErr)), 240),
		)
	}
	fallbackFinalRun, fallbackFinalLedger, _, fallbackErr := s.executeStockProfileAgentAttempt(
		ctx, fallbackRun, fallbackLedger, profile, fallbackModelName,
	)
	if fallbackFinalRun.Status == AgentRunStatusCompleted {
		s.markStockProfileUpdateTaskAIResult(ctx, finalRun.ID, StockProfileUpdateStatusCompleted, StockProfileAIStatusReady, "")
	} else {
		s.markStockProfileUpdateTaskAIResult(
			ctx,
			finalRun.ID,
			StockProfileUpdateStatusPartial,
			StockProfileAIStatusFailed,
			firstNonEmpty(fallbackFinalRun.ErrorMessage, stockProfileErrorString(fallbackErr)),
		)
	}
	return fallbackFinalRun, fallbackFinalLedger, stockProfileAttemptError(fallbackFinalRun, fallbackErr)
}

func (s *Service) executeStockProfileAgentAttempt(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	profile StockProfile,
	modelName string,
) (AgentRun, AgentDecisionLedger, *AgentExecutorOutput, error) {
	if s.agentExecutor == nil {
		err := ErrAgentExecutorUnavailable
		s.finalizeAgentRun(ctx, run.ID, nil, err)
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
		return finalRun, finalLedger, nil, err
	}
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update stock profile agent run to running failed", "run_id", run.ID, "ledger_id", ledger.ID, "symbol", profile.Symbol, "market", profile.Market, "model", modelName, "error", safelog.Text(err.Error(), 240))
	}
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecuteStockProfileSummary(ctx, taskID, profile, modelName, run.ReasoningEffort)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execOutput, execErr
}

func stockProfileFallbackEligible(ctx context.Context, run AgentRun, output *AgentExecutorOutput, execErr error) bool {
	if ctx.Err() != nil || run.Status != AgentRunStatusFailed {
		return false
	}
	if errors.Is(execErr, ErrAgentExecutorUnavailable) ||
		errors.Is(execErr, ErrAgentTaskRequiresCLI) ||
		errors.Is(execErr, ErrAgentExecutionModeModelMismatch) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(run.ErrorMessage + " " + stockProfileErrorString(execErr)))
	if strings.Contains(message, "save stock profile enhancement failed:") &&
		!strings.Contains(message, ErrInvalidStockProfileEnhancement.Error()) {
		// ponytail: persistence may already have crossed a storage boundary; a
		// second paid model call cannot safely repair it.
		return false
	}
	if output != nil {
		return true
	}
	return agentProviderUsageLimitFailure(execErr, output) ||
		strings.Contains(message, "no valid result submitted") ||
		strings.Contains(message, "without submitting") ||
		strings.Contains(message, "no result submitted") ||
		strings.Contains(message, ErrInvalidStockProfileEnhancement.Error()) ||
		strings.Contains(message, "execution timed out") ||
		strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "api request failed") ||
		strings.Contains(message, "provider") ||
		strings.Contains(message, "codex")
}

func (s *Service) linkStockProfileFallbackAttempt(
	ctx context.Context,
	primaryRun AgentRun,
	primaryLedger AgentDecisionLedger,
	fallbackRun AgentRun,
	fallbackLedger AgentDecisionLedger,
) error {
	primaryLedger.OutputArtifactSummary = safelog.Text(
		strings.TrimSpace(primaryLedger.OutputArtifactSummary)+"\nfallback_agent_run_id: "+fallbackRun.ID,
		16384,
	)
	if primaryLedger.RedactionSummary == nil {
		primaryLedger.RedactionSummary = map[string]any{}
	}
	if fallbackLedger.RedactionSummary == nil {
		fallbackLedger.RedactionSummary = map[string]any{}
	}
	primaryLedger.RedactionSummary["fallbackAgentRunId"] = fallbackRun.ID
	fallbackLedger.RedactionSummary["fallbackFromAgentRunId"] = primaryRun.ID
	if _, err := s.store.UpdateAgentDecisionLedger(ctx, primaryLedger); err != nil {
		return err
	}
	_, err := s.store.UpdateAgentDecisionLedger(ctx, fallbackLedger)
	return err
}

func stockProfileAttemptError(run AgentRun, execErr error) error {
	if run.Status == AgentRunStatusCompleted {
		return nil
	}
	if strings.TrimSpace(run.ErrorMessage) != "" {
		return errors.New(run.ErrorMessage)
	}
	return execErr
}

func stockProfileErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonNil(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func (s *Service) applyStockProfileEnhancementResult(ctx context.Context, symbol string, result map[string]any, modelName string, confidence float64) (StockProfile, error) {
	profile, err := s.store.GetStockProfile(ctx, strings.TrimSpace(symbol))
	if err != nil {
		return StockProfile{}, err
	}
	if len(result) == 0 {
		profile.AIProfileStatus = StockProfileAIStatusFailed
		profile.AIProfileError = ErrInvalidStockProfileEnhancement.Error()
		_, _ = s.store.UpsertStockProfile(ctx, profile)
		return StockProfile{}, ErrInvalidStockProfileEnhancement
	}
	profile.BusinessSummaryZh = firstProfileResultString(result, "summaryZh", "businessSummaryZh")
	if profile.BusinessSummaryZh == "" {
		profile.BusinessSummaryZh = profile.BusinessSummary
	}
	profile.BusinessSummaryEn = firstProfileResultString(result, "summaryEn", "businessSummaryEn")
	profile.AliasesZh = appendProfileTerms(profile.AliasesZh, profileResultStrings(result, "aliasesZh", "aliasZh")...)
	profile.AliasesEn = appendProfileTerms(profile.AliasesEn, profileResultStrings(result, "aliasesEn", "aliasEn")...)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, profileResultStrings(result, "keywordsZh", "keywordZh")...)
	profile.KeywordsEn = appendProfileTerms(profile.KeywordsEn, profileResultStrings(result, "keywordsEn", "keywordEn")...)
	profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, profileResultStrings(result, "businessLinesZh", "businessLineZh")...)
	profile.BusinessLinesEn = appendProfileTerms(profile.BusinessLinesEn, profileResultStrings(result, "businessLinesEn", "businessLineEn")...)
	profile.RiskTagsZh = appendProfileTerms(profile.RiskTagsZh, profileResultStrings(result, "riskTagsZh", "riskTagZh")...)
	profile.RiskTagsEn = appendProfileTerms(profile.RiskTagsEn, profileResultStrings(result, "riskTagsEn", "riskTagEn")...)
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesZh...)
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesEn...)
	profile.ProfileTextZh = buildProfileTextZh(profile)
	profile.ProfileTextEn = buildProfileTextEn(profile)
	profile.ProfileText = buildProfileText(profile)
	profile.AIProfileStatus = StockProfileAIStatusReady
	profile.AIProfileModel = modelName
	profile.AIProfileConfidence = confidence
	profile.AIProfileError = ""
	profile.AIProfileUpdatedAt = time.Now()
	updated, err := s.store.UpsertStockProfile(ctx, profile)
	if err != nil {
		return StockProfile{}, err
	}
	s.markStockProfileEmbeddingStale(ctx, updated)
	return updated, nil
}

func (s *Service) markStockProfileAIEnhancementFailed(ctx context.Context, run AgentRun, message string) {
	if run.TaskType != AgentTaskTypeStockProfileSummary || run.TriggerObjectType != "stock_profile" || strings.TrimSpace(run.TriggerObjectID) == "" {
		return
	}
	profile, err := s.store.GetStockProfile(ctx, strings.TrimSpace(run.TriggerObjectID))
	if err != nil {
		if s.log != nil {
			s.log.Warn("mark stock profile ai failed: get profile failed", "run_id", run.ID, "task_type", run.TaskType, "symbol", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
		}
		return
	}
	profile.AIProfileStatus = StockProfileAIStatusFailed
	profile.AIProfileError = safelog.Text(message, 500)
	profile.AIProfileUpdatedAt = time.Now()
	if _, err := s.store.UpsertStockProfile(ctx, profile); err != nil && s.log != nil {
		s.log.Warn("mark stock profile ai failed: save profile failed", "run_id", run.ID, "task_type", run.TaskType, "symbol", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
	}
	s.markStockProfileUpdateTaskAIResult(ctx, run.ID, StockProfileUpdateStatusPartial, StockProfileAIStatusFailed, message)
}

func (s *Service) markStockProfileUpdateTaskAIResult(ctx context.Context, agentRunID, taskStatus, aiStatus, message string) {
	if s.store == nil || strings.TrimSpace(agentRunID) == "" {
		return
	}
	if err := s.store.UpdateStockProfileUpdateTaskAIResultByAgentRunID(ctx, agentRunID, taskStatus, aiStatus, message); err != nil && s.log != nil {
		s.log.Warn("update stock profile task ai result failed", "run_id", agentRunID, "status", aiStatus, "error", err)
	}
}

func firstProfileResultString(result map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringFromAny(result[key])); value != "" {
			return value
		}
	}
	return ""
}

func profileResultStrings(result map[string]any, keys ...string) []string {
	for _, key := range keys {
		if items := stringsFromAny(result[key]); len(items) > 0 {
			return items
		}
	}
	return nil
}

func stringsFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return cleanProfileTerms(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return cleanProfileTerms(out)
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return cleanProfileTerms(strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || r == '\n'
		}))
	default:
		return nil
	}
}

func (s *Service) upsertInstrumentWithProfile(ctx context.Context, instrument StockV2Instrument) error {
	if err := s.store.UpsertInstrument(ctx, instrument); err != nil {
		return err
	}
	profile, err := s.store.UpsertStockProfile(ctx, s.stockProfileFromInstrument(ctx, instrument, false))
	if err != nil {
		return err
	}
	s.markStockProfileEmbeddingStale(ctx, profile)
	return nil
}

func (s *Service) markStockProfileEmbeddingStale(ctx context.Context, profile StockProfile) {
	symbol := strings.TrimSpace(profile.Symbol)
	if symbol == "" {
		return
	}
	// ponytail: profile refreshes often rewrite unchanged rows; compare the exact embedding
	// document hash so only semantic changes consume another embedding request.
	textHash := hashEmbeddingText(stockProfileEmbeddingText(profile))
	if err := s.store.MarkEmbeddingAssetsStaleForObjectTextHash(ctx, EmbeddingObjectStockProfile, symbol, textHash); err != nil && s.log != nil {
		s.log.Warn("mark stock profile embedding stale failed", "symbol", safelog.Text(symbol, 80), "error", safelog.Text(err.Error(), 240))
	}
}

func (s *Service) stockProfileFromInstrument(ctx context.Context, instrument StockV2Instrument, enrichPublicSources bool) StockProfile {
	profile, _ := s.stockProfileFromInstrumentWithSourceStatuses(ctx, instrument, enrichPublicSources)
	return profile
}

func (s *Service) stockProfileFromInstrumentWithSourceStatuses(ctx context.Context, instrument StockV2Instrument, enrichPublicSources bool) (StockProfile, []StockProfileSourceStatus) {
	base, sourceStatuses := s.stockProfileBaseFromInstrumentWithSourceStatuses(ctx, instrument, enrichPublicSources)
	return s.mergeStockProfileExisting(ctx, base), sourceStatuses
}

func (s *Service) stockProfileBaseFromInstrumentWithSourceStatuses(ctx context.Context, instrument StockV2Instrument, enrichPublicSources bool) (StockProfile, []StockProfileSourceStatus) {
	base := buildStockProfileFromInstrument(instrument)
	var sourceStatuses []StockProfileSourceStatus
	if enrichPublicSources {
		base, sourceStatuses = s.enrichStockProfileFromPublicSources(ctx, base, instrument)
	}
	return base, sourceStatuses
}

func (s *Service) mergeStockProfileExisting(ctx context.Context, base StockProfile) StockProfile {
	existing, err := s.store.GetStockProfile(ctx, base.Symbol)
	if err != nil {
		return base
	}
	// ponytail: profile_update_tasks 保存基础输入 hash;这里仅合并既有 AI 增强与兼容字段。
	return mergeStockProfileAIFields(base, existing)
}

func buildStockProfileFromInstrument(instrument StockV2Instrument) StockProfile {
	instrument.InstrumentType = normalizeInstrumentType(instrument.InstrumentType)
	aliases := cleanProfileTerms([]string{
		instrument.Symbol,
		instrument.Market + instrument.Symbol,
		instrument.Symbol + "." + instrument.Market,
		instrument.Name,
	})
	sectors := cleanProfileTerms([]string{instrument.Sector})
	concepts := cleanProfileTerms(instrument.Concepts)
	tags := cleanProfileTerms([]string{instrument.Industry, instrument.Sector})

	profile := StockProfile{
		Symbol:          strings.TrimSpace(instrument.Symbol),
		Market:          strings.TrimSpace(instrument.Market),
		InstrumentType:  instrument.InstrumentType,
		Name:            strings.TrimSpace(instrument.Name),
		Aliases:         aliases,
		AliasesZh:       cleanProfileTerms([]string{strings.TrimSpace(instrument.Name)}),
		AliasesEn:       cleanProfileTerms([]string{instrument.Symbol, instrument.Market + instrument.Symbol, instrument.Symbol + "." + instrument.Market}),
		Industry:        strings.TrimSpace(instrument.Industry),
		Sectors:         sectors,
		Concepts:        concepts,
		Tags:            tags,
		KeywordsZh:      cleanProfileTerms(append([]string{instrument.Industry, instrument.Sector}, instrument.Concepts...)),
		KeywordsEn:      cleanProfileTerms([]string{instrument.Symbol, instrument.Market}),
		ProfileVersion:  2,
		AIProfileStatus: StockProfileAIStatusMissing,
	}
	if profile.Name == "" {
		profile.Name = profile.Symbol
	}
	if profile.InstrumentType == InstrumentTypeExchangeFund || looksLikeExchangeFund(profile.Name) {
		enrichExchangeFundProfile(&profile)
	}
	profile.BusinessSummary = buildProfileSummary(profile)
	profile.BusinessSummaryZh = profile.BusinessSummary
	profile.ProfileTextZh = buildProfileTextZh(profile)
	profile.ProfileTextEn = buildProfileTextEn(profile)
	profile.ProfileText = buildProfileText(profile)
	return profile
}

func mergeStockProfileAIFields(base, existing StockProfile) StockProfile {
	base.BusinessSummaryEn = existing.BusinessSummaryEn
	base.BusinessLinesZh = appendProfileTerms(base.BusinessLinesZh, existing.BusinessLinesZh...)
	base.BusinessLinesEn = appendProfileTerms(base.BusinessLinesEn, existing.BusinessLinesEn...)
	base.RiskTagsZh = appendProfileTerms(base.RiskTagsZh, existing.RiskTagsZh...)
	base.RiskTagsEn = appendProfileTerms(base.RiskTagsEn, existing.RiskTagsEn...)
	base.AIProfileStatus = existing.AIProfileStatus
	base.AIProfileModel = existing.AIProfileModel
	base.AIProfileConfidence = existing.AIProfileConfidence
	base.AIProfileError = existing.AIProfileError
	base.AIProfileUpdatedAt = existing.AIProfileUpdatedAt
	base.AliasesZh = appendProfileTerms(base.AliasesZh, existing.AliasesZh...)
	base.AliasesEn = appendProfileTerms(base.AliasesEn, existing.AliasesEn...)
	base.KeywordsZh = appendProfileTerms(base.KeywordsZh, existing.KeywordsZh...)
	base.KeywordsEn = appendProfileTerms(base.KeywordsEn, existing.KeywordsEn...)
	if existing.BusinessSummaryZh != "" && (existing.AIProfileStatus == StockProfileAIStatusReady || stockProfileSummaryLooksBasic(base.BusinessSummaryZh)) {
		base.BusinessSummaryZh = existing.BusinessSummaryZh
	}
	if base.BusinessSummaryZh != "" {
		base.BusinessSummary = base.BusinessSummaryZh
	}
	if base.FundType == "" {
		base.FundType = existing.FundType
	}
	if base.TrackingIndex == "" {
		base.TrackingIndex = existing.TrackingIndex
	}
	if base.Theme == "" {
		base.Theme = existing.Theme
	}
	if base.ConstituentHint == "" {
		base.ConstituentHint = existing.ConstituentHint
	}
	base.Aliases = appendProfileTerms(base.Aliases, base.AliasesZh...)
	base.Aliases = appendProfileTerms(base.Aliases, base.AliasesEn...)
	base.ProfileTextZh = buildProfileTextZh(base)
	base.ProfileTextEn = buildProfileTextEn(base)
	base.ProfileText = buildProfileText(base)
	return base
}

func stockProfileSummaryLooksBasic(summary string) bool {
	summary = strings.TrimSpace(summary)
	return summary == "" ||
		strings.Contains(summary, "股票标的") ||
		strings.Contains(summary, "场内基金") ||
		strings.Contains(summary, "行业:") ||
		strings.Contains(summary, "基金类型:")
}

func cleanProfileTerms(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		term := strings.TrimSpace(item)
		term = strings.Trim(term, "，,;；、 ")
		if term == "" || term == "." {
			continue
		}
		key := strings.ToLower(term)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, term)
	}
	return out
}

func appendProfileTerms(base []string, items ...string) []string {
	return cleanProfileTerms(append(base, items...))
}

func looksLikeExchangeFund(name string) bool {
	upperName := strings.ToUpper(name)
	return strings.Contains(upperName, "ETF") ||
		strings.Contains(upperName, "LOF") ||
		strings.Contains(name, "基金")
}

func enrichExchangeFundProfile(profile *StockProfile) {
	name := profile.Name
	upperName := strings.ToUpper(name)
	profile.FundType = "场内基金"
	profile.Tags = appendProfileTerms(profile.Tags, "场内基金")
	if strings.Contains(upperName, "ETF") {
		profile.FundType = "ETF"
		themeName := strings.TrimSpace(strings.ReplaceAll(name, "ETF", ""))
		profile.Tags = appendProfileTerms(profile.Tags, "ETF", themeName)
		profile.AliasesEn = appendProfileTerms(profile.AliasesEn, "ETF")
		profile.KeywordsEn = appendProfileTerms(profile.KeywordsEn, "ETF", themeName)
	}
	if strings.Contains(upperName, "LOF") {
		profile.FundType = "LOF"
		profile.Tags = appendProfileTerms(profile.Tags, "LOF")
		profile.AliasesEn = appendProfileTerms(profile.AliasesEn, "LOF")
		profile.KeywordsEn = appendProfileTerms(profile.KeywordsEn, "LOF")
	}

	// ponytail: ETF 画像先用名称关键词做高召回种子;等有正式基金画像源后替换这里的规则表。
	for _, rule := range []struct {
		keyword string
		index   string
		theme   string
	}{
		{"沪深300", "沪深300", "宽基指数"},
		{"中证500", "中证500", "宽基指数"},
		{"中证1000", "中证1000", "宽基指数"},
		{"上证50", "上证50", "宽基指数"},
		{"科创50", "科创50", "科创板"},
		{"创业板", "创业板", "创业板"},
		{"红利", "", "红利"},
		{"证券", "", "证券"},
		{"银行", "", "银行"},
		{"医药", "", "医药"},
		{"新能源", "", "新能源"},
		{"人工智能", "", "人工智能"},
		{"芯片", "", "芯片"},
		{"半导体", "", "半导体"},
		{"军工", "", "军工"},
	} {
		if !strings.Contains(name, rule.keyword) {
			continue
		}
		profile.Tags = appendProfileTerms(profile.Tags, rule.keyword, rule.theme)
		if profile.TrackingIndex == "" && rule.index != "" {
			profile.TrackingIndex = rule.index
		}
		if profile.Theme == "" && rule.theme != "" {
			profile.Theme = rule.theme
		}
	}
	if profile.TrackingIndex != "" {
		profile.ConstituentHint = "跟踪 " + profile.TrackingIndex + " 相关成分股"
	} else if profile.Theme != "" {
		profile.ConstituentHint = "关注 " + profile.Theme + " 主题相关成分股"
	}
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.FundType, profile.TrackingIndex, profile.Theme)
	profile.AliasesZh = appendProfileTerms(profile.AliasesZh, profile.FundType, profile.TrackingIndex, profile.Theme)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, profile.FundType, profile.TrackingIndex, profile.Theme, profile.ConstituentHint)
}

func buildProfileSummary(profile StockProfile) string {
	parts := []string{profile.Name + "(" + profile.Symbol + ")"}
	if profile.Market != "" {
		parts = append(parts, profile.Market+"市场")
	}
	if profile.InstrumentType == InstrumentTypeExchangeFund {
		parts = append(parts, "场内基金")
	} else {
		parts = append(parts, "股票标的")
	}
	if profile.Industry != "" {
		parts = append(parts, "行业:"+profile.Industry)
	}
	if len(profile.Sectors) > 0 {
		parts = append(parts, "板块:"+strings.Join(profile.Sectors, "、"))
	}
	if len(profile.Concepts) > 0 {
		parts = append(parts, "概念:"+strings.Join(profile.Concepts, "、"))
	}
	if profile.FundType != "" {
		parts = append(parts, "基金类型:"+profile.FundType)
	}
	if profile.TrackingIndex != "" {
		parts = append(parts, "跟踪指数:"+profile.TrackingIndex)
	}
	if profile.Theme != "" {
		parts = append(parts, "主题:"+profile.Theme)
	}
	return strings.Join(parts, "。")
}

func buildProfileText(profile StockProfile) string {
	terms := []string{profile.ProfileTextZh, profile.ProfileTextEn}
	terms = append(terms, profile.Aliases...)
	terms = append(terms, profile.AliasesZh...)
	terms = append(terms, profile.AliasesEn...)
	terms = append(terms, profile.KeywordsZh...)
	terms = append(terms, profile.KeywordsEn...)
	terms = append(terms, profile.BusinessLinesZh...)
	terms = append(terms, profile.BusinessLinesEn...)
	terms = append(terms, profile.RiskTagsZh...)
	terms = append(terms, profile.RiskTagsEn...)
	return strings.Join(cleanProfileTerms(terms), " ")
}

func buildProfileTextZh(profile StockProfile) string {
	terms := []string{
		profile.Symbol,
		profile.Market,
		profile.Market + profile.Symbol,
		profile.Symbol + "." + profile.Market,
		profile.Name,
		profile.Industry,
		profile.BusinessSummary,
		profile.FundType,
		profile.TrackingIndex,
		profile.Theme,
		profile.ConstituentHint,
	}
	terms = append(terms, profile.Aliases...)
	terms = append(terms, profile.Sectors...)
	terms = append(terms, profile.Concepts...)
	terms = append(terms, profile.Tags...)
	terms = append(terms, profile.AliasesZh...)
	terms = append(terms, profile.KeywordsZh...)
	terms = append(terms, profile.BusinessLinesZh...)
	terms = append(terms, profile.RiskTagsZh...)
	return strings.Join(cleanProfileTerms(terms), " ")
}

func buildProfileTextEn(profile StockProfile) string {
	terms := []string{
		profile.Symbol,
		profile.Market,
		profile.Market + profile.Symbol,
		profile.Symbol + "." + profile.Market,
		profile.BusinessSummaryEn,
		profile.FundType,
	}
	terms = append(terms, profile.AliasesEn...)
	terms = append(terms, profile.KeywordsEn...)
	terms = append(terms, profile.BusinessLinesEn...)
	terms = append(terms, profile.RiskTagsEn...)
	return strings.Join(cleanProfileTerms(terms), " ")
}

func normalizeStockProfileUpdateTrigger(value string) string {
	switch strings.TrimSpace(value) {
	case StockProfileUpdateTriggerAuto:
		return StockProfileUpdateTriggerAuto
	default:
		return StockProfileUpdateTriggerManual
	}
}

func stockProfileAIDecisionForError(err error) string {
	switch {
	case errors.Is(err, ErrAgentTaskProfileNotFound), errors.Is(err, ErrAgentModelNotAvailable):
		return StockProfileAIDecisionSkippedNotConfigured
	case errors.Is(err, ErrAgentExecutorUnavailable):
		return StockProfileAIDecisionSkippedUnavailable
	default:
		return StockProfileAIDecisionFailed
	}
}

func normalizeStockProfileAIStatus(status string) string {
	switch strings.TrimSpace(status) {
	case StockProfileAIStatusReady:
		return StockProfileAIStatusReady
	case StockProfileAIStatusFailed:
		return StockProfileAIStatusFailed
	case StockProfileAIStatusNotConfigured:
		return StockProfileAIStatusNotConfigured
	case StockProfileUpdateAIStatusRunning:
		return StockProfileUpdateAIStatusRunning
	default:
		return StockProfileAIStatusMissing
	}
}

func stockProfileAITaskStatusForDecision(decision string) string {
	switch decision {
	case StockProfileAIDecisionSkippedNotConfigured:
		return StockProfileAIStatusNotConfigured
	case StockProfileAIDecisionSkippedUnavailable, StockProfileAIDecisionFailed:
		return StockProfileAIStatusFailed
	default:
		return StockProfileAIStatusMissing
	}
}

func stockProfileAIInputHash(profile StockProfile) string {
	input := struct {
		Symbol            string   `json:"symbol"`
		Market            string   `json:"market"`
		InstrumentType    string   `json:"instrumentType"`
		Name              string   `json:"name"`
		Aliases           []string `json:"aliases"`
		AliasesZh         []string `json:"aliasesZh"`
		AliasesEn         []string `json:"aliasesEn"`
		Industry          string   `json:"industry"`
		Sectors           []string `json:"sectors"`
		Concepts          []string `json:"concepts"`
		Tags              []string `json:"tags"`
		KeywordsZh        []string `json:"keywordsZh"`
		KeywordsEn        []string `json:"keywordsEn"`
		BusinessSummary   string   `json:"businessSummary"`
		BusinessSummaryZh string   `json:"businessSummaryZh"`
		BusinessLinesZh   []string `json:"businessLinesZh"`
		BusinessLinesEn   []string `json:"businessLinesEn"`
		RiskTagsZh        []string `json:"riskTagsZh"`
		RiskTagsEn        []string `json:"riskTagsEn"`
		FundType          string   `json:"fundType"`
		TrackingIndex     string   `json:"trackingIndex"`
		Theme             string   `json:"theme"`
		ConstituentHint   string   `json:"constituentHint"`
	}{
		Symbol:            profile.Symbol,
		Market:            profile.Market,
		InstrumentType:    profile.InstrumentType,
		Name:              profile.Name,
		Aliases:           cleanProfileTerms(profile.Aliases),
		AliasesZh:         cleanProfileTerms(profile.AliasesZh),
		AliasesEn:         cleanProfileTerms(profile.AliasesEn),
		Industry:          strings.TrimSpace(profile.Industry),
		Sectors:           cleanProfileTerms(profile.Sectors),
		Concepts:          cleanProfileTerms(profile.Concepts),
		Tags:              cleanProfileTerms(profile.Tags),
		KeywordsZh:        cleanProfileTerms(profile.KeywordsZh),
		KeywordsEn:        cleanProfileTerms(profile.KeywordsEn),
		BusinessSummary:   strings.TrimSpace(profile.BusinessSummary),
		BusinessSummaryZh: strings.TrimSpace(profile.BusinessSummaryZh),
		BusinessLinesZh:   cleanProfileTerms(profile.BusinessLinesZh),
		BusinessLinesEn:   cleanProfileTerms(profile.BusinessLinesEn),
		RiskTagsZh:        cleanProfileTerms(profile.RiskTagsZh),
		RiskTagsEn:        cleanProfileTerms(profile.RiskTagsEn),
		FundType:          strings.TrimSpace(profile.FundType),
		TrackingIndex:     strings.TrimSpace(profile.TrackingIndex),
		Theme:             strings.TrimSpace(profile.Theme),
		ConstituentHint:   strings.TrimSpace(profile.ConstituentHint),
	}
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func stockProfileScatterRank(symbol, seed string) uint64 {
	sum := sha256.Sum256([]byte(strings.TrimSpace(seed) + ":" + strings.TrimSpace(symbol)))
	var rank uint64
	for i := 0; i < 8; i++ {
		rank = rank<<8 | uint64(sum[i])
	}
	return rank
}

func stockProfileDeepUpdateFreshnessBucket(lastTaskAt time.Time) int64 {
	if lastTaskAt.IsZero() {
		return 0
	}
	return lastTaskAt.UTC().Unix()/86400 + 1
}

func stockProfileDeepUpdateDelay(symbol string, base time.Duration, seed string) time.Duration {
	if base <= 0 {
		return 0
	}
	jitter := time.Duration(stockProfileScatterRank(symbol, seed) % uint64(base))
	return base + jitter
}

func sleepStockProfileDeepUpdate(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
