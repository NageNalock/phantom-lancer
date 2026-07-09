package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	defaultAssetMaintenanceAIBudget = 20
	assetAIBackoff                  = 6 * time.Hour
)

type assetMaintenanceBudget struct {
	AIRemaining int
}

type assetMaintenanceOptions struct {
	JobID         string
	TriggerSource string
	RequestedBy   string
	ForceAI       bool
	Budget        *assetMaintenanceBudget
}

func (s *Service) MaintainAssetForSymbol(ctx context.Context, symbol string, req AssetMaintainSymbolRequest) (AssetMaintainSymbolResult, error) {
	normalized, _ := normalizeQuoteSymbolInput(symbol)
	if normalized == "" {
		normalized = strings.TrimSpace(symbol)
	}
	if normalized == "" {
		return AssetMaintainSymbolResult{}, ErrInvalidQuoteSymbol
	}
	inst, err := s.store.GetInstrument(ctx, normalized)
	if err != nil {
		return AssetMaintainSymbolResult{}, err
	}
	budget := &assetMaintenanceBudget{AIRemaining: 1}
	if !req.ForceAI {
		budget.AIRemaining = defaultAssetMaintenanceAIBudget
	}
	return s.maintainAssetForInstrument(ctx, inst, assetMaintenanceOptions{
		TriggerSource: firstNonEmpty(req.TriggerSource, "manual"),
		RequestedBy:   firstNonEmpty(req.RequestedBy, "user"),
		ForceAI:       req.ForceAI,
		Budget:        budget,
	})
}

func (s *Service) maintainAssetForInstrument(ctx context.Context, inst StockV2Instrument, opts assetMaintenanceOptions) (AssetMaintainSymbolResult, error) {
	startedAt := time.Now()
	item := AssetMaintenanceItem{
		ID:             generateID(),
		JobID:          strings.TrimSpace(opts.JobID),
		Symbol:         inst.Symbol,
		Market:         inst.Market,
		InstrumentType: normalizeInstrumentType(inst.InstrumentType),
		Name:           inst.Name,
		Status:         AssetMaintenanceItemStatusRunning,
		StartedAt:      startedAt,
		CreatedAt:      startedAt,
		UpdatedAt:      startedAt,
	}
	item, _ = s.store.UpsertAssetMaintenanceItem(ctx, item)
	var result AssetMaintainSymbolResult
	var errs []string
	var sourceStatuses []AssetMaintenanceSourceStatus

	if err := s.store.UpsertInstrument(ctx, inst); err != nil {
		errs = append(errs, "instrument: "+err.Error())
	}

	fetchedDaily, bars, err := s.fetchDailyBarsForInstrumentWithQuality(ctx, inst, DailyBarsQuality{}, false)
	if err != nil {
		item.DailyBarStatus = AssetDailyBarStatusFailed
		errs = append(errs, "daily bars: "+truncateDailyBarErr(err.Error()))
	} else if fetchedDaily {
		item.DailyBarStatus = AssetDailyBarStatusFetched
		item.DailyBarFetched = len(bars)
		if len(bars) > 0 {
			item.DailyBarStart = bars[0].TradeDate
			item.DailyBarEnd = bars[len(bars)-1].TradeDate
			if saveErr := s.store.UpsertDailyBars(ctx, bars); saveErr != nil {
				item.DailyBarStatus = AssetDailyBarStatusFailed
				errs = append(errs, "daily bars save: "+truncateDailyBarErr(saveErr.Error()))
			}
		}
	} else {
		item.DailyBarStatus = AssetDailyBarStatusSkipped
	}

	profile, profileBefore, profileSourceStatuses, err := s.refreshAssetBaseProfile(ctx, inst)
	sourceStatuses = append(sourceStatuses, profileSourceStatuses...)
	if err != nil {
		item.BaseProfileStatus = AssetBaseProfileStatusFailed
		errs = append(errs, "base profile: "+err.Error())
	} else {
		result.Profile = profile
		item.BaseProfileHashBefore = profileBefore.BaseProfileHash
		item.BaseProfileHashAfter = profile.BaseProfileHash
		item.BaseProfileChanged = item.BaseProfileHashBefore == "" || item.BaseProfileHashBefore != item.BaseProfileHashAfter
		if item.BaseProfileChanged {
			item.BaseProfileStatus = AssetBaseProfileStatusUpdated
			s.markStockProfileEmbeddingStale(ctx, profile.Symbol)
		} else {
			item.BaseProfileStatus = AssetBaseProfileStatusUnchanged
		}
	}

	announcements, annStatus, annErr := s.fetchAndStoreAnnouncements(ctx, inst)
	sourceStatuses = append(sourceStatuses, annStatus)
	if annErr != nil {
		item.AnnouncementStatus = AssetAnnouncementStatusFailed
	} else {
		item.AnnouncementStatus = annStatus.Status
		item.AnnouncementsNew = len(announcements)
		for _, ann := range announcements {
			if ann.Major {
				item.MajorAnnouncementsNew++
			}
		}
		result.Announcements = announcements
	}

	agentRun, aiDecision, aiStatus, aiErr := s.maybeRunAssetProfileAI(ctx, profile, profileBefore, item, announcements, sourceStatuses, opts)
	item.AIDecision = aiDecision
	item.AIProfileStatus = aiStatus
	if agentRun != nil {
		item.AgentRunID = agentRun.ID
		result.AgentRun = agentRun
	}
	if aiErr != nil && aiDecision != AssetAIDecisionSkippedConfig {
		errs = append(errs, "ai: "+aiErr.Error())
	}

	item.SourceStatuses = sourceStatuses
	item.DurationMs = time.Since(startedAt).Milliseconds()
	item.FinishedAt = time.Now()
	item.UpdatedAt = item.FinishedAt
	if len(errs) > 0 {
		item.ErrorMessage = safelog.Text(strings.Join(errs, "; "), 800)
		if item.AgentRunID != "" && item.AIProfileStatus == StockProfileUpdateAIStatusRunning {
			item.Status = AssetMaintenanceItemStatusPartial
		} else {
			item.Status = AssetMaintenanceItemStatusFailed
		}
	} else if item.AgentRunID != "" && item.AIProfileStatus == StockProfileUpdateAIStatusRunning {
		item.Status = AssetMaintenanceItemStatusPartial
	} else {
		item.Status = AssetMaintenanceItemStatusCompleted
	}
	item, err = s.store.UpsertAssetMaintenanceItem(ctx, item)
	if err != nil {
		return result, err
	}
	result.Item = item
	if len(errs) > 0 {
		return result, fmt.Errorf("%s", item.ErrorMessage)
	}
	return result, nil
}

func (s *Service) refreshAssetBaseProfile(ctx context.Context, inst StockV2Instrument) (StockProfile, StockProfile, []AssetMaintenanceSourceStatus, error) {
	var existing StockProfile
	if loaded, err := s.store.GetStockProfile(ctx, inst.Symbol); err == nil {
		existing = loaded
	} else if !errors.Is(err, ErrStockProfileNotFound) {
		return StockProfile{}, StockProfile{}, nil, err
	}
	baseProfile, profileStatuses := s.stockProfileBaseFromInstrumentWithSourceStatuses(ctx, inst, true)
	profile := s.mergeStockProfileExisting(ctx, baseProfile)
	hash := stockProfileAIInputHash(baseProfile)
	profile.BaseProfileHash = hash
	profile.BaseProfileUpdatedAt = time.Now()
	updated, err := s.store.UpsertStockProfile(ctx, profile)
	if err != nil {
		return StockProfile{}, existing, stockProfileSourceStatusesToAsset(profileStatuses), err
	}
	task := StockProfileUpdateTask{
		ID:                  generateID(),
		Symbol:              inst.Symbol,
		Market:              inst.Market,
		TriggerSource:       StockProfileUpdateTriggerAuto,
		TriggerReason:       "asset_maintenance",
		Status:              StockProfileUpdateStatusCompleted,
		BaseInputHashBefore: existing.BaseProfileHash,
		BaseInputHashAfter:  hash,
		BaseInputChanged:    existing.BaseProfileHash == "" || existing.BaseProfileHash != hash,
		BaseProfileStatus:   StockProfileUpdateBaseStatusReady,
		AIDecision:          StockProfileAIDecisionSkippedUnchanged,
		AIProfileStatus:     normalizeStockProfileAIStatus(updated.AIProfileStatus),
		SourceStatuses:      profileStatuses,
		StartedAt:           time.Now(),
		FinishedAt:          time.Now(),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
	return updated, existing, stockProfileSourceStatusesToAsset(profileStatuses), nil
}

func stockProfileSourceStatusesToAsset(items []StockProfileSourceStatus) []AssetMaintenanceSourceStatus {
	out := make([]AssetMaintenanceSourceStatus, 0, len(items))
	for _, item := range items {
		out = append(out, AssetMaintenanceSourceStatus{
			Source:    item.Source,
			Status:    item.Status,
			Message:   item.Message,
			CheckedAt: item.FetchedAt,
		})
	}
	return out
}

func (s *Service) fetchAndStoreAnnouncements(ctx context.Context, inst StockV2Instrument) ([]StockV2Announcement, AssetMaintenanceSourceStatus, error) {
	if s.announcementSource == nil || normalizeInstrumentType(inst.InstrumentType) != InstrumentTypeStock {
		return nil, AssetMaintenanceSourceStatus{Source: StockV2AnnouncementSourceCninfo, Status: AssetAnnouncementStatusSkipped, CheckedAt: time.Now(), Message: "not a stock"}, nil
	}
	backoffKey := "announcement:" + StockV2AnnouncementSourceCninfo + ":" + strings.ToUpper(inst.Market)
	if until, ok := s.assetSourceBackoffUntil(backoffKey, time.Now()); ok {
		return nil, AssetMaintenanceSourceStatus{Source: StockV2AnnouncementSourceCninfo, Status: AssetAnnouncementStatusSkipped, CheckedAt: time.Now(), Message: "backoff until " + until.Format(time.RFC3339)}, nil
	}
	items, status, err := s.announcementSource.FetchAnnouncements(ctx, inst, 20)
	if err != nil || len(items) == 0 {
		if err != nil {
			s.recordAssetSourceBackoff(backoffKey, 15*time.Minute)
		}
		if status.Status == "ok" {
			status.Status = AssetAnnouncementStatusChecked
		}
		return nil, status, err
	}
	before, _ := s.store.CountAnnouncements(ctx, AnnouncementListFilter{Symbol: inst.Symbol})
	inserted, err := s.store.UpsertAnnouncements(ctx, items)
	if err != nil {
		status.Status = AssetAnnouncementStatusFailed
		status.Message = err.Error()
		s.recordAssetSourceBackoff(backoffKey, 15*time.Minute)
		return nil, status, err
	}
	s.clearAssetSourceBackoff(backoffKey)
	after, _ := s.store.CountAnnouncements(ctx, AnnouncementListFilter{Symbol: inst.Symbol})
	if inserted == 0 && after > before {
		inserted = after - before
	}
	status.Status = AssetAnnouncementStatusChecked
	if inserted <= 0 {
		return nil, status, nil
	}
	latest, err := s.store.ListAnnouncements(ctx, AnnouncementListFilter{Symbol: inst.Symbol, Limit: inserted})
	if err != nil {
		return items[:0], status, nil
	}
	return latest, status, nil
}

func (s *Service) assetSourceBackoffUntil(key string, now time.Time) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	s.assetBackoffMu.Lock()
	defer s.assetBackoffMu.Unlock()
	until := s.assetBackoff[key]
	if until.IsZero() {
		return time.Time{}, false
	}
	if !now.Before(until) {
		delete(s.assetBackoff, key)
		return time.Time{}, false
	}
	return until, true
}

func (s *Service) recordAssetSourceBackoff(key string, d time.Duration) {
	if s == nil || d <= 0 {
		return
	}
	s.assetBackoffMu.Lock()
	defer s.assetBackoffMu.Unlock()
	if s.assetBackoff == nil {
		s.assetBackoff = map[string]time.Time{}
	}
	s.assetBackoff[key] = time.Now().Add(d)
}

func (s *Service) clearAssetSourceBackoff(key string) {
	if s == nil {
		return
	}
	s.assetBackoffMu.Lock()
	defer s.assetBackoffMu.Unlock()
	delete(s.assetBackoff, key)
}

func (s *Service) maybeRunAssetProfileAI(
	ctx context.Context,
	profile StockProfile,
	previous StockProfile,
	item AssetMaintenanceItem,
	newAnnouncements []StockV2Announcement,
	sourceStatuses []AssetMaintenanceSourceStatus,
	opts assetMaintenanceOptions,
) (*AgentRun, string, string, error) {
	if strings.TrimSpace(profile.Symbol) == "" {
		return nil, AssetAIDecisionSkippedUnneeded, StockProfileAIStatusMissing, nil
	}
	decision := assetAIDecision(profile, previous, item, newAnnouncements, opts.ForceAI)
	if decision == AssetAIDecisionSkippedUnneeded {
		return nil, decision, normalizeStockProfileAIStatus(profile.AIProfileStatus), nil
	}
	budget := opts.Budget
	if budget != nil {
		if budget.AIRemaining <= 0 {
			return nil, AssetAIDecisionSkippedBudget, normalizeStockProfileAIStatus(profile.AIProfileStatus), nil
		}
		budget.AIRemaining--
	}
	pack := s.buildStockProfileSummaryContext(ctx, profile, previous, item, newAnnouncements, sourceStatuses)
	run, ledger, modelName, err := s.prepareStockProfileSummaryAgentRun(ctx, pack, firstNonEmpty(opts.RequestedBy, "system"))
	if err != nil {
		if stockProfileAIDecisionForError(err) == StockProfileAIDecisionSkippedNotConfigured {
			return nil, AssetAIDecisionSkippedConfig, StockProfileAIStatusNotConfigured, err
		}
		return nil, AssetAIDecisionFailed, StockProfileAIStatusFailed, err
	}
	task := StockProfileUpdateTask{
		ID:                  generateID(),
		Symbol:              profile.Symbol,
		Market:              profile.Market,
		TriggerSource:       normalizeStockProfileUpdateTrigger(opts.TriggerSource),
		TriggerReason:       "asset_maintenance_ai",
		Status:              StockProfileUpdateStatusRunning,
		BaseInputHashBefore: item.BaseProfileHashBefore,
		BaseInputHashAfter:  item.BaseProfileHashAfter,
		BaseInputChanged:    item.BaseProfileChanged,
		BaseProfileStatus:   StockProfileUpdateBaseStatusReady,
		AIDecision:          StockProfileAIDecisionCalled,
		AgentRunID:          run.ID,
		AIProfileStatus:     StockProfileUpdateAIStatusRunning,
		StartedAt:           time.Now(),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	_, _ = s.store.CreateStockProfileUpdateTask(ctx, task)
	go s.startStockProfileAgentRunAsync(context.Background(), run, ledger, pack, modelName)
	return &run, decision, StockProfileUpdateAIStatusRunning, nil
}

func assetAIDecision(profile StockProfile, previous StockProfile, item AssetMaintenanceItem, announcements []StockV2Announcement, force bool) string {
	if force {
		return AssetAIDecisionManualForce
	}
	if normalizeStockProfileAIStatus(profile.AIProfileStatus) == StockProfileAIStatusMissing || strings.TrimSpace(profile.ProfileTextZh+profile.ProfileTextEn) == "" {
		return AssetAIDecisionMissing
	}
	if item.BaseProfileChanged {
		return AssetAIDecisionBaseChanged
	}
	if len(announcements) > 0 {
		return AssetAIDecisionAnnouncement
	}
	if normalizeStockProfileAIStatus(profile.AIProfileStatus) == StockProfileAIStatusFailed && (profile.AIProfileUpdatedAt.IsZero() || time.Since(profile.AIProfileUpdatedAt) >= assetAIBackoff) {
		return AssetAIDecisionRetry
	}
	_ = previous
	return AssetAIDecisionSkippedUnneeded
}

func (s *Service) buildStockProfileSummaryContext(ctx context.Context, profile, previous StockProfile, item AssetMaintenanceItem, announcements []StockV2Announcement, sourceStatuses []AssetMaintenanceSourceStatus) StockProfileSummaryContext {
	pack := StockProfileSummaryContext{
		Profile: profile,
		PreviousSummary: StockProfilePreviousSummary{
			BusinessSummaryZh: previous.BusinessSummaryZh,
			BusinessSummaryEn: previous.BusinessSummaryEn,
			ProfileTextZh:     previous.ProfileTextZh,
			ProfileTextEn:     previous.ProfileTextEn,
			AIProfileModel:    previous.AIProfileModel,
			UpdatedAt:         previous.AIProfileUpdatedAt,
		},
		BaseDiff: StockProfileBaseDiff{
			HashBefore: item.BaseProfileHashBefore,
			HashAfter:  item.BaseProfileHashAfter,
			Changed:    item.BaseProfileChanged,
			Fields:     baseProfileChangedFields(previous, profile),
		},
		NewAnnouncements:     announcements,
		MajorAnnouncements:   filterMajorAnnouncements(announcements),
		DailySummary:         s.stockProfileDailySummary(ctx, profile.Symbol),
		SourceStatuses:       sourceStatuses,
		MaintenanceStartedAt: item.StartedAt,
	}
	return pack
}

func baseProfileChangedFields(before, after StockProfile) []string {
	var fields []string
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	if before.Industry != after.Industry {
		fields = append(fields, "industry")
	}
	if strings.Join(cleanProfileTerms(before.Concepts), "\x00") != strings.Join(cleanProfileTerms(after.Concepts), "\x00") {
		fields = append(fields, "concepts")
	}
	if strings.Join(cleanProfileTerms(before.BusinessLinesZh), "\x00") != strings.Join(cleanProfileTerms(after.BusinessLinesZh), "\x00") {
		fields = append(fields, "businessLinesZh")
	}
	if before.BusinessSummary != after.BusinessSummary || before.BusinessSummaryZh != after.BusinessSummaryZh {
		fields = append(fields, "businessSummary")
	}
	return fields
}

func filterMajorAnnouncements(items []StockV2Announcement) []StockV2Announcement {
	out := make([]StockV2Announcement, 0, len(items))
	for _, item := range items {
		if item.Major {
			out = append(out, item)
		}
	}
	return out
}

func (s *Service) stockProfileDailySummary(ctx context.Context, symbol string) StockProfileDailySummary {
	bars, err := s.store.GetDailyBars(ctx, symbol, DailyBarAdjustedNone, "", "", 5)
	if err != nil || len(bars) == 0 {
		return StockProfileDailySummary{}
	}
	last := bars[len(bars)-1]
	summary := StockProfileDailySummary{
		LatestDate:    last.TradeDate,
		RowCount:      len(bars),
		MainNetInflow: last.MainNetInflow,
	}
	if len(bars) >= 2 && bars[0].Close > 0 {
		summary.PctChange5D = (last.Close - bars[0].Close) / bars[0].Close * 100
	}
	return summary
}

func (s *Service) selectAssetMaintenanceSymbols(ctx context.Context, req UniverseUpdateRequest) ([]string, error) {
	maxSymbols := req.MaxSymbols
	if maxSymbols <= 0 {
		maxSymbols = 5000
	}
	if len(req.Symbols) > 0 {
		return limitStrings(compactStringList(req.Symbols, maxSymbols), maxSymbols), nil
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(symbols []string) {
		for _, symbol := range symbols {
			normalized, _ := normalizeQuoteSymbolInput(symbol)
			if normalized == "" {
				normalized = strings.TrimSpace(symbol)
			}
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
			if len(out) >= maxSymbols {
				return
			}
		}
	}
	if holdings, err := s.store.ListHoldingSymbols(ctx); err == nil {
		add(holdings)
	}
	if len(out) < maxSymbols {
		if strategySymbols, err := s.activeStrategySymbols(ctx); err == nil {
			items := make([]string, 0, len(strategySymbols))
			for symbol := range strategySymbols {
				items = append(items, symbol)
			}
			add(items)
		}
	}
	if len(out) < maxSymbols {
		if watches, err := s.store.ListWatches(ctx, WatchListFilter{Status: WatchStatusActive, Limit: 5000}); err == nil {
			items := make([]string, 0, len(watches))
			for _, watch := range watches {
				items = append(items, watch.Symbol)
			}
			add(items)
		}
	}
	if len(out) < maxSymbols {
		if stored, err := s.store.ListInstrumentSymbols(ctx); err == nil {
			add(stored)
		}
	}
	if len(out) < maxSymbols && s.universeSource != nil {
		add(s.universeSource.GetDefaultSymbols())
	}
	return limitStrings(out, maxSymbols), nil
}

func limitStrings(items []string, limit int) []string {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func mergeAssetMaintenanceStats(stats AssetMaintenanceStats, item AssetMaintenanceItem) AssetMaintenanceStats {
	switch item.DailyBarStatus {
	case AssetDailyBarStatusFetched:
		stats.DailyBarFetched++
	case AssetDailyBarStatusSkipped:
		stats.DailyBarSkipped++
	}
	switch item.BaseProfileStatus {
	case AssetBaseProfileStatusUpdated:
		stats.BaseProfileUpdated++
	case AssetBaseProfileStatusUnchanged:
		stats.BaseProfileUnchanged++
	}
	stats.AnnouncementsNew += item.AnnouncementsNew
	stats.MajorAnnouncementsNew += item.MajorAnnouncementsNew
	switch item.AIDecision {
	case AssetAIDecisionMissing, AssetAIDecisionBaseChanged, AssetAIDecisionAnnouncement, AssetAIDecisionRetry, AssetAIDecisionManualForce:
		stats.AICalled++
	case AssetAIDecisionSkippedUnneeded, AssetAIDecisionSkippedBudget, AssetAIDecisionSkippedConfig:
		stats.AISkipped++
	}
	return stats
}

func (s *Service) ListAssetMaintenanceItems(ctx context.Context, filter AssetMaintenanceItemListFilter) ([]AssetMaintenanceItem, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 200)
	filter.Offset = normalizedPageOffset(filter.Offset)
	return s.store.ListAssetMaintenanceItems(ctx, filter)
}

func (s *Service) CountAssetMaintenanceItems(ctx context.Context, filter AssetMaintenanceItemListFilter) (int, error) {
	return s.store.CountAssetMaintenanceItems(ctx, filter)
}

func (s *Service) ListAnnouncements(ctx context.Context, filter AnnouncementListFilter) ([]StockV2Announcement, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 100)
	filter.Offset = normalizedPageOffset(filter.Offset)
	return s.store.ListAnnouncements(ctx, filter)
}

func (s *Service) CountAnnouncements(ctx context.Context, filter AnnouncementListFilter) (int, error) {
	return s.store.CountAnnouncements(ctx, filter)
}

func (s *Service) ListAssetSummaries(ctx context.Context, symbols []string) (map[string]StockV2AssetSummary, error) {
	symbols = compactStringList(symbols, 200)
	if len(symbols) == 0 {
		return map[string]StockV2AssetSummary{}, nil
	}
	profiles, err := s.ListStockProfileSummaries(ctx, symbols)
	if err != nil {
		return nil, err
	}
	qualities, err := s.GetDailyBarsQualityBatch(ctx, symbols, DailyBarAdjustedNone)
	if err != nil {
		return nil, err
	}
	announcementStats, err := s.store.LatestAnnouncementStats(ctx, symbols)
	if err != nil {
		return nil, err
	}
	latestItems, err := s.store.LatestAssetMaintenanceItems(ctx, symbols)
	if err != nil {
		return nil, err
	}
	out := make(map[string]StockV2AssetSummary, len(symbols))
	for _, symbol := range symbols {
		item := announcementStats[symbol]
		item.Symbol = symbol
		item.ProfileSummary = profiles[symbol]
		item.DailyBarQuality = qualities[symbol]
		item.LatestMaintenance = latestItems[symbol]
		out[symbol] = item
	}
	return out, nil
}
