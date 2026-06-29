package stockv2

import (
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
		ID:            generateID(),
		Symbol:        normalizedSymbol,
		TriggerSource: trigger,
		TriggerReason: strings.TrimSpace(req.TriggerReason),
		Status:        StockProfileUpdateStatusCompleted,
		AIDecision:    StockProfileAIDecisionSkippedUnchanged,
		StartedAt:     startedAt,
		CreatedAt:     startedAt,
		UpdatedAt:     startedAt,
	}

	existing, err := s.store.GetStockProfile(ctx, normalizedSymbol)
	if err != nil && !errors.Is(err, ErrStockProfileNotFound) {
		task.Status = StockProfileUpdateStatusFailed
		task.ErrorMessage = err.Error()
		task.FinishedAt = time.Now()
		_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
		return StockProfileUpdateResult{}, err
	}
	if err == nil {
		if latestTasks, listErr := s.store.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: normalizedSymbol, Limit: 1}); listErr == nil && len(latestTasks) > 0 && latestTasks[0].BaseInputHashAfter != "" {
			task.BaseInputHashBefore = latestTasks[0].BaseInputHashAfter
		} else {
			task.BaseInputHashBefore = stockProfileAIInputHash(existing)
		}
	}
	instrument, err := s.store.GetInstrument(ctx, normalizedSymbol)
	if err != nil {
		task.Status = StockProfileUpdateStatusFailed
		task.ErrorMessage = err.Error()
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
		task.ErrorMessage = err.Error()
		task.FinishedAt = time.Now()
		_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
		return StockProfileUpdateResult{}, err
	}

	var agentRun *AgentRun
	var strictErr error
	if req.ForceAI || task.BaseInputChanged {
		run, runErr := s.startStockProfileSummaryAgentRun(ctx, profile, req.RequestedBy)
		if runErr != nil {
			task.AIDecision = stockProfileAIDecisionForError(runErr)
			if task.AIDecision == StockProfileAIDecisionFailed || req.StrictAI {
				task.Status = StockProfileUpdateStatusPartial
				task.ErrorMessage = runErr.Error()
			}
			if req.StrictAI {
				task.Status = StockProfileUpdateStatusFailed
				strictErr = runErr
			}
		} else {
			task.AIDecision = StockProfileAIDecisionCalled
			task.AgentRunID = run.ID
			agentRun = &run
		}
	} else {
		task.AIDecision = StockProfileAIDecisionSkippedUnchanged
	}

	task.FinishedAt = time.Now()
	createdTask, createErr := s.store.CreateStockProfileUpdateTask(ctx, task)
	if createErr != nil {
		return StockProfileUpdateResult{}, createErr
	}
	if strictErr != nil {
		return StockProfileUpdateResult{Profile: profile, Task: createdTask}, strictErr
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
			if _, err := s.store.UpsertStockProfile(ctx, s.stockProfileFromInstrument(ctx, instrument, false)); err != nil {
				result.Failed++
				result.FailedItems = append(result.FailedItems, UpdateFailure{
					Symbol: instrument.Symbol,
					Reason: err.Error(),
				})
				continue
			}
			result.Success++
		}
	}
	return result, nil
}

type stockProfileDeepUpdateOptions struct {
	SymbolBudget int
	AIBudget     int
	RateLimit    time.Duration
	Now          time.Time
	RequestedBy  string
}

func (s *Service) RunAutomaticDeepStockProfileUpdate(ctx context.Context, trigger string) (StockProfileDeepUpdateResult, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return StockProfileDeepUpdateResult{}, err
	}
	return s.runAutomaticDeepStockProfileUpdate(ctx, trigger, stockProfileDeepUpdateOptions{
		SymbolBudget: settings.BaseProfileDeepUpdateBatchSize,
		AIBudget:     settings.BaseProfileDeepUpdateAIBudget,
		RateLimit:    time.Duration(settings.BaseProfileDeepUpdateRateLimitMs) * time.Millisecond,
		RequestedBy:  "system",
	})
}

func (s *Service) runAutomaticDeepStockProfileUpdate(ctx context.Context, trigger string, opts stockProfileDeepUpdateOptions) (StockProfileDeepUpdateResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	symbolBudget := normalizeStockProfileDeepUpdateBatchSize(opts.SymbolBudget)
	aiBudget := normalizeStockProfileDeepUpdateAIBudget(opts.AIBudget)
	rateLimit := opts.RateLimit
	if rateLimit < 0 {
		rateLimit = 0
	}
	requestedBy := strings.TrimSpace(opts.RequestedBy)
	if requestedBy == "" {
		requestedBy = "system"
	}
	result := StockProfileDeepUpdateResult{
		SymbolBudget: symbolBudget,
		AIBudget:     aiBudget,
		RateLimitMs:  int(rateLimit / time.Millisecond),
		UpdatedAt:    now,
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
		if result.AICalledCount >= aiBudget {
			result.StoppedByBudget = true
			break
		}
		if i > 0 {
			if err := sleepStockProfileDeepUpdate(ctx, stockProfileDeepUpdateDelay(candidate.Instrument.Symbol, rateLimit, seed)); err != nil {
				return result, err
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
		if result.AICalledCount >= aiBudget {
			result.StoppedByBudget = true
		}
	}
	result.UpdatedAt = time.Now()
	return result, nil
}

func (s *Service) runBaseProfileMaintenanceScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
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
			SymbolBudget: settings.BaseProfileDeepUpdateBatchSize,
			AIBudget:     settings.BaseProfileDeepUpdateAIBudget,
			RateLimit:    time.Duration(settings.BaseProfileDeepUpdateRateLimitMs) * time.Millisecond,
			RequestedBy:  "system",
		})
	}
	settings.BaseProfileLastMaintainAt = now
	settings.BaseProfileNextMaintainAt = now.Add(interval)
	if runErr != nil {
		settings.BaseProfileLastMaintainResult = fmt.Sprintf("failed trigger=%s error=%s", trigger, runErr.Error())
	} else if deepErr != nil {
		settings.BaseProfileLastMaintainResult = fmt.Sprintf("partial trigger=%s total=%d success=%d failed=%d deepError=%s", trigger, result.Total, result.Success, result.Failed, stockProfileSnippet(deepErr.Error(), 180))
	} else {
		settings.BaseProfileLastMaintainResult = fmt.Sprintf("completed trigger=%s total=%d success=%d failed=%d deepCandidates=%d deepProcessed=%d deepAI=%d deepFailed=%d stoppedByBudget=%t", trigger, result.Total, result.Success, result.Failed, deepResult.CandidateCount, deepResult.ProcessedCount, deepResult.AICalledCount, deepResult.FailedCount, deepResult.StoppedByBudget)
	}
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil && s.log != nil {
		s.log.Warn("save base profile maintenance state failed", "error", err)
		return
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
	out := make(map[string]StockProfileSummary, len(symbols))
	for _, raw := range symbols {
		symbol := strings.TrimSpace(raw)
		if symbol == "" {
			continue
		}
		profile, err := s.store.GetStockProfile(ctx, symbol)
		if err != nil {
			if errors.Is(err, ErrStockProfileNotFound) {
				out[symbol] = StockProfileSummary{Symbol: symbol, Status: "missing", AIProfileStatus: StockProfileAIStatusMissing}
				continue
			}
			return nil, err
		}
		status := "ready"
		if strings.TrimSpace(profile.ProfileText) == "" {
			status = "partial"
		}
		out[symbol] = StockProfileSummary{
			Symbol:              profile.Symbol,
			Status:              status,
			BusinessSummary:     firstNonEmpty(profile.BusinessSummaryZh, profile.BusinessSummary, profile.BusinessSummaryEn),
			AIProfileStatus:     profile.AIProfileStatus,
			AIProfileModel:      profile.AIProfileModel,
			AIProfileConfidence: profile.AIProfileConfidence,
			AIProfileUpdatedAt:  profile.AIProfileUpdatedAt,
			UpdatedAt:           profile.UpdatedAt,
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

func (s *Service) startStockProfileSummaryAgentRun(ctx context.Context, profile StockProfile, requestedBy string) (AgentRun, error) {
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeStockProfileSummary)
	if err != nil {
		return AgentRun{}, err
	}
	model, err := s.resolveModel(ctx, taskProfile)
	if err != nil {
		profile.AIProfileStatus = StockProfileAIStatusNotConfigured
		profile.AIProfileError = err.Error()
		_, _ = s.store.UpsertStockProfile(ctx, profile)
		return AgentRun{}, err
	}
	if s.agentExecutor == nil {
		return AgentRun{}, ErrAgentExecutorUnavailable
	}
	inputArtifact, _ := json.Marshal(map[string]any{
		"task":    AgentTaskTypeStockProfileSummary,
		"profile": profile,
	})
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeStockProfileSummary,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "stock_profile",
		TriggerObjectID:      profile.Symbol,
		RequestedBy:          requestedBy,
		InputSummary:         fmt.Sprintf("stock_profile_summary symbol=%s market=%s name=%s", profile.Symbol, profile.Market, profile.Name),
		InputArtifactSummary: string(inputArtifact),
	})
	if err != nil {
		return AgentRun{}, err
	}
	go s.startStockProfileAgentRunAsync(context.Background(), run, ledger, profile, model.ModelName)
	return run, nil
}

func (s *Service) startStockProfileAgentRunAsync(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, profile StockProfile, modelName string) {
	defer func() {
		if r := recover(); r != nil {
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update stock profile agent run to running failed", "run_id", run.ID, "error", err)
	}
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecuteStockProfileSummary(ctx, taskID, profile, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
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
	return s.store.UpsertStockProfile(ctx, profile)
}

func (s *Service) markStockProfileAIEnhancementFailed(ctx context.Context, run AgentRun, message string) {
	if run.TaskType != AgentTaskTypeStockProfileSummary || run.TriggerObjectType != "stock_profile" || strings.TrimSpace(run.TriggerObjectID) == "" {
		return
	}
	profile, err := s.store.GetStockProfile(ctx, strings.TrimSpace(run.TriggerObjectID))
	if err != nil {
		if s.log != nil {
			s.log.Warn("mark stock profile ai failed: get profile failed", "run_id", run.ID, "symbol", run.TriggerObjectID, "error", err)
		}
		return
	}
	profile.AIProfileStatus = StockProfileAIStatusFailed
	profile.AIProfileError = safelog.Text(message, 500)
	profile.AIProfileUpdatedAt = time.Now()
	if _, err := s.store.UpsertStockProfile(ctx, profile); err != nil && s.log != nil {
		s.log.Warn("mark stock profile ai failed: save profile failed", "run_id", run.ID, "symbol", run.TriggerObjectID, "error", err)
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
	_, err := s.store.UpsertStockProfile(ctx, s.stockProfileFromInstrument(ctx, instrument, false))
	return err
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
