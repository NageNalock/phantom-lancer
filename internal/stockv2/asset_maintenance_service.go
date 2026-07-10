package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	assetAIBackoff             = 6 * time.Hour
	baseProfileRefreshInterval = 7 * 24 * time.Hour
	// ponytail: the full Sina universe currently costs dozens of paginated
	// requests. Persist it for seven days; daily jobs still scan every cached
	// symbol, while manual symbols can introduce a new listing immediately.
	assetUniverseDiscoveryRefreshInterval = 7 * 24 * time.Hour
	// ponytail: a small result is the built-in outage fallback, not a complete
	// universe snapshot. Keep serving the existing cache but retry discovery
	// sooner so a transient Sina failure cannot hide new listings for a week.
	assetUniverseDiscoveryFallbackRetryInterval = 6 * time.Hour
)

type assetMaintenanceOptions struct {
	JobID                     string
	TriggerSource             string
	RequestedBy               string
	ForceAI                   bool
	AnnouncementsPrefetched   bool
	PrefetchedAnnouncements   []StockV2Announcement
	RecentAnnouncements       []StockV2Announcement
	AnnouncementPrefetchError error
	AnnouncementCheckedAt     time.Time
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
	return s.maintainAssetForInstrument(ctx, inst, assetMaintenanceOptions{
		TriggerSource: firstNonEmpty(req.TriggerSource, "manual"),
		RequestedBy:   firstNonEmpty(req.RequestedBy, "user"),
		ForceAI:       req.ForceAI,
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
	var aiErrorMessage string
	var dailyBarsErr error
	var sourceStatuses []AssetMaintenanceSourceStatus

	if err := s.store.UpsertInstrument(ctx, inst); err != nil {
		errs = append(errs, "instrument: "+err.Error())
	}

	fetchedDaily, bars, checkedDailyGaps, err := s.fetchDailyBarsForInstrument(ctx, inst)
	if err != nil {
		dailyBarsErr = err
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
			} else if checkErr := s.store.RecordDailyBarGapChecks(ctx, inst.Symbol, DailyBarAdjustedNone, checkedDailyGaps); checkErr != nil {
				item.DailyBarStatus = AssetDailyBarStatusFailed
				errs = append(errs, "daily gap check save: "+truncateDailyBarErr(checkErr.Error()))
			}
		}
	} else {
		item.DailyBarStatus = AssetDailyBarStatusSkipped
		if checkErr := s.store.RecordDailyBarGapChecks(ctx, inst.Symbol, DailyBarAdjustedNone, checkedDailyGaps); checkErr != nil {
			item.DailyBarStatus = AssetDailyBarStatusFailed
			errs = append(errs, "daily gap check save: "+truncateDailyBarErr(checkErr.Error()))
		}
	}

	// F10 基础画像刷新缓存：7 天内已刷新过则跳过 F10 抓取（节省 3 次 HTTP 请求/股票）
	profile, profileBefore, profileSourceStatuses, err := s.refreshAssetBaseProfileWithCache(ctx, inst, opts)
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

	announcements, annStatus, annErr := s.assetAnnouncementsForMaintenance(ctx, inst, opts)
	sourceStatuses = append(sourceStatuses, annStatus)
	if annErr != nil {
		item.AnnouncementStatus = AssetAnnouncementStatusFailed
		errs = append(errs, "announcements: "+annErr.Error())
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
	if aiStatus == StockProfileAIStatusQueued {
		item.AIQueueStatus = StockProfileAIQueueStatusReady
	} else if aiStatus == StockProfileAIStatusRunning {
		item.AIQueueStatus = StockProfileAIQueueStatusRunning
	} else if aiStatus == StockProfileAIStatusReady && assetAIDecisionCalled(aiDecision) {
		item.AIQueueStatus = StockProfileAIQueueStatusCompleted
	}
	if agentRun != nil {
		item.AgentRunID = agentRun.ID
		result.AgentRun = agentRun
	}
	if aiErr != nil && aiDecision != AssetAIDecisionSkippedConfig {
		aiErrorMessage = "ai: " + aiErr.Error()
		item.AIQueueStatus = StockProfileAIQueueStatusFailed
	}

	item.SourceStatuses = sourceStatuses
	item.DurationMs = time.Since(startedAt).Milliseconds()
	item.FinishedAt = time.Now()
	item.UpdatedAt = item.FinishedAt
	if len(errs) > 0 {
		item.ErrorMessage = safelog.Text(strings.Join(errs, "; "), 800)
		item.Status = AssetMaintenanceItemStatusFailed
	} else {
		item.Status = AssetMaintenanceItemStatusCompleted
	}
	if aiErrorMessage != "" {
		item.ErrorMessage = safelog.Text(strings.Trim(strings.Join([]string{item.ErrorMessage, aiErrorMessage}, "; "), "; "), 800)
	}
	item, err = s.store.UpsertAssetMaintenanceItem(ctx, item)
	if err != nil {
		return result, err
	}
	if assetAIDecisionCalled(item.AIDecision) {
		if syncErr := s.store.SyncAssetMaintenanceItemAIQueue(ctx, item.ID, item.Symbol); syncErr != nil && s.log != nil {
			s.log.Warn("sync asset maintenance ai queue state failed", "symbol", item.Symbol, "error", safelog.Text(syncErr.Error(), 240))
		}
	}
	result.Item = item
	if len(errs) > 0 {
		if dailyBarsErr != nil {
			return result, fmt.Errorf("%s: %w", item.ErrorMessage, dailyBarsErr)
		}
		return result, fmt.Errorf("%s", item.ErrorMessage)
	}
	return result, nil
}

func assetAIDecisionCalled(decision string) bool {
	switch decision {
	case AssetAIDecisionMissing, AssetAIDecisionBaseChanged, AssetAIDecisionAnnouncement,
		AssetAIDecisionRetry, AssetAIDecisionManualForce:
		return true
	default:
		return false
	}
}

func (s *Service) assetAnnouncementsForMaintenance(
	ctx context.Context,
	inst StockV2Instrument,
	opts assetMaintenanceOptions,
) ([]StockV2Announcement, AssetMaintenanceSourceStatus, error) {
	if !opts.AnnouncementsPrefetched {
		return s.fetchAndStoreAnnouncements(ctx, inst)
	}
	checkedAt := opts.AnnouncementCheckedAt
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	status := AssetMaintenanceSourceStatus{
		Source:    StockV2AnnouncementSourceCninfo,
		Status:    AssetAnnouncementStatusChecked,
		CheckedAt: checkedAt,
		Message:   "exchange-wide incremental sync",
	}
	if normalizeInstrumentType(inst.InstrumentType) != InstrumentTypeStock {
		status.Status = AssetAnnouncementStatusSkipped
		status.Message = "not a stock"
		return nil, status, nil
	}
	if opts.AnnouncementPrefetchError != nil {
		status.Status = AssetAnnouncementStatusFailed
		status.Message = safelog.Text(opts.AnnouncementPrefetchError.Error(), 300)
		return nil, status, opts.AnnouncementPrefetchError
	}
	return opts.PrefetchedAnnouncements, status, nil
}

func (s *Service) refreshAssetBaseProfile(ctx context.Context, inst StockV2Instrument) (StockProfile, StockProfile, []AssetMaintenanceSourceStatus, error) {
	var existingBeforeFetch StockProfile
	if loaded, err := s.store.GetStockProfile(ctx, inst.Symbol); err == nil {
		existingBeforeFetch = loaded
	} else if !errors.Is(err, ErrStockProfileNotFound) {
		return StockProfile{}, StockProfile{}, nil, err
	}

	baseProfile, profileStatuses := s.stockProfileBaseFromInstrumentWithSourceStatuses(ctx, inst, true)
	if sourceErr := stockProfileSourceFailure(profileStatuses); sourceErr != nil {
		return existingBeforeFetch, existingBeforeFetch, stockProfileSourceStatusesToAsset(profileStatuses), sourceErr
	}
	unlock := s.lockStockProfile(inst.Symbol)
	defer unlock()
	existing := existingBeforeFetch
	if latest, err := s.store.GetStockProfile(ctx, inst.Symbol); err == nil {
		existing = latest
	} else if !errors.Is(err, ErrStockProfileNotFound) {
		return StockProfile{}, existingBeforeFetch, stockProfileSourceStatusesToAsset(profileStatuses), err
	}
	profile := baseProfile
	if existing.Symbol != "" {
		profile = mergeStockProfileAIFields(baseProfile, existing)
	}
	hash := stockProfileAIInputHash(baseProfile)
	profile.BaseProfileHash = hash
	now := time.Now()
	if existing.BaseProfileHash == "" || existing.BaseProfileHash != hash || existing.BaseProfileUpdatedAt.IsZero() {
		profile.BaseProfileUpdatedAt = now
	} else {
		profile.BaseProfileUpdatedAt = existing.BaseProfileUpdatedAt
	}
	profile.BaseProfileCheckedAt = now
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

func stockProfileSourceFailure(statuses []StockProfileSourceStatus) error {
	var failures []string
	for _, status := range statuses {
		if status.Status == StockProfileSourceStatusFailed {
			failures = append(failures, firstNonEmpty(status.Source, "unknown")+": "+status.Message)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return errors.New(safelog.Text(strings.Join(failures, "; "), 600))
}

// refreshAssetBaseProfileWithCache 在批量维护管线中调用，带 F10 缓存逻辑。
// 如果基础画像在 7 天内已刷新过，则跳过 F10 抓取（节省 3 次 HTTP 请求/股票）。
// instrument 发生变化时直接刷新 F10；未变化时返回已持久化画像，避免用粗略的
// instrument 摘要覆盖缓存中更完整的 F10 内容。
func (s *Service) refreshAssetBaseProfileWithCache(ctx context.Context, inst StockV2Instrument, opts assetMaintenanceOptions) (StockProfile, StockProfile, []AssetMaintenanceSourceStatus, error) {
	// 手动强制时不走缓存
	if opts.ForceAI {
		return s.refreshAssetBaseProfile(ctx, inst)
	}

	var existing StockProfile
	if loaded, err := s.store.GetStockProfile(ctx, inst.Symbol); err == nil {
		existing = loaded
	} else if !errors.Is(err, ErrStockProfileNotFound) {
		return StockProfile{}, StockProfile{}, nil, err
	}

	// 缓存命中：7 天内已刷新过 F10，跳过 F10 抓取
	baseCheckedAt := existing.BaseProfileCheckedAt
	if baseCheckedAt.IsZero() {
		baseCheckedAt = existing.BaseProfileUpdatedAt
	}
	if !baseCheckedAt.IsZero() && time.Since(baseCheckedAt) < baseProfileRefreshInterval {
		baseProfile := buildStockProfileFromInstrument(inst)
		if stockProfileInstrumentFieldsChanged(existing, baseProfile) {
			// A cheap universe/instrument comparison runs every maintenance pass.
			// Only a detected change bypasses the F10 cache and spends remote calls.
			return s.refreshAssetBaseProfile(ctx, inst)
		}
		unlock := s.lockStockProfile(inst.Symbol)
		if latest, err := s.store.GetStockProfile(ctx, inst.Symbol); err == nil {
			existing = latest
		} else if !errors.Is(err, ErrStockProfileNotFound) {
			unlock()
			return StockProfile{}, existing, nil, err
		}
		latestCheckedAt := existing.BaseProfileCheckedAt
		if latestCheckedAt.IsZero() {
			latestCheckedAt = existing.BaseProfileUpdatedAt
		}
		cacheStillFresh := !latestCheckedAt.IsZero() && time.Since(latestCheckedAt) < baseProfileRefreshInterval
		instrumentStillUnchanged := !stockProfileInstrumentFieldsChanged(existing, baseProfile)
		if !cacheStillFresh || !instrumentStillUnchanged {
			unlock()
			return s.refreshAssetBaseProfile(ctx, inst)
		}
		// ponytail: a fresh, unchanged cached profile is already the minimum
		// correct state. Avoid a write and preserve its richer F10 base fields.
		profile := existing
		unlock()
		cacheStatus := []AssetMaintenanceSourceStatus{{
			Source:    "f10_cache",
			Status:    "skipped",
			Message:   "base profile refreshed within 7d, skipping F10 enrichment",
			CheckedAt: time.Now(),
		}}
		return profile, existing, cacheStatus, nil
	}

	// 缓存未命中：正常刷新
	return s.refreshAssetBaseProfile(ctx, inst)
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
	recent, err := s.store.ListRecentAnnouncementsBySymbols(ctx, []string{inst.Symbol}, inserted)
	if err != nil {
		return items[:0], status, nil
	}
	return recent[inst.Symbol], status, nil
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
	relevantAnnouncements := appendAnnouncementContext(
		newAnnouncements,
		announcementsAfterAIProfile(opts.RecentAnnouncements, profile.AIProfileUpdatedAt),
	)
	decision := assetAIDecision(profile, previous, item, relevantAnnouncements, opts.ForceAI)
	announcementContextFailed := assetMaintenanceSourceFailed(sourceStatuses, StockV2AnnouncementSourceCninfo)
	if decision == AssetAIDecisionSkippedUnneeded && !announcementContextFailed &&
		(profile.AIProfileStatus == StockProfileAIStatusQueued || profile.AIProfileStatus == StockProfileAIStatusRunning) {
		if needsRefresh, refreshErr := s.stockProfileAIQueueContextFailed(ctx, profile.Symbol); refreshErr != nil {
			return nil, AssetAIDecisionFailed, normalizeStockProfileAIStatus(profile.AIProfileStatus), refreshErr
		} else if needsRefresh {
			decision = AssetAIDecisionRetry
		}
	}
	if decision == AssetAIDecisionSkippedUnneeded {
		return nil, decision, normalizeStockProfileAIStatus(profile.AIProfileStatus), nil
	}
	pack := s.buildStockProfileSummaryContext(ctx, profile, previous, item, relevantAnnouncements, sourceStatuses)
	var queueItem StockProfileAIQueueItem
	var err error
	if announcementContextFailed {
		queueItem, err = s.enqueueStockProfileAIWithState(
			ctx, pack, decision, firstNonEmpty(opts.RequestedBy, "system"), opts.ForceAI,
			StockProfileAIQueueStatusRetryWait, time.Now().Add(time.Hour),
		)
	} else {
		queueItem, err = s.enqueueStockProfileAI(ctx, pack, decision, firstNonEmpty(opts.RequestedBy, "system"), opts.ForceAI)
	}
	if err != nil {
		return nil, AssetAIDecisionFailed, StockProfileAIStatusFailed, err
	}
	if queueItem.Status == StockProfileAIQueueStatusCompleted && queueItem.CompletedInputVersion == queueItem.DesiredInputVersion {
		return nil, decision, normalizeStockProfileAIStatus(profile.AIProfileStatus), nil
	}
	var currentRun *AgentRun
	queuedProfileStatus := StockProfileAIStatusQueued
	if queueItem.Status == StockProfileAIQueueStatusRunning && queueItem.CurrentAgentRunID != "" {
		if run, runErr := s.store.GetAgentRun(ctx, queueItem.CurrentAgentRunID); runErr == nil {
			currentRun = &run
			queuedProfileStatus = StockProfileAIStatusRunning
		}
	}
	hasPendingTask, err := s.store.HasPendingStockProfileUpdateTask(ctx, profile.Symbol)
	if err != nil {
		return nil, AssetAIDecisionFailed, StockProfileAIStatusFailed, err
	}
	if hasPendingTask {
		return currentRun, decision, queuedProfileStatus, nil
	}
	taskStatus := StockProfileUpdateStatusQueued
	taskAIStatus := StockProfileUpdateAIStatusQueued
	taskAgentRunID := ""
	if currentRun != nil {
		taskStatus = StockProfileUpdateStatusRunning
		taskAIStatus = StockProfileUpdateAIStatusRunning
		taskAgentRunID = currentRun.ID
	}
	task := StockProfileUpdateTask{
		ID:                  generateID(),
		Symbol:              profile.Symbol,
		Market:              profile.Market,
		TriggerSource:       normalizeStockProfileUpdateTrigger(opts.TriggerSource),
		TriggerReason:       "asset_maintenance_ai",
		Status:              taskStatus,
		BaseInputHashBefore: item.BaseProfileHashBefore,
		BaseInputHashAfter:  item.BaseProfileHashAfter,
		BaseInputChanged:    item.BaseProfileChanged,
		BaseProfileStatus:   StockProfileUpdateBaseStatusReady,
		AIDecision:          StockProfileAIDecisionCalled,
		AgentRunID:          taskAgentRunID,
		AIProfileStatus:     taskAIStatus,
		StartedAt:           time.Now(),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	createdTask, createErr := s.store.CreateStockProfileUpdateTask(ctx, task)
	if createErr != nil {
		return nil, AssetAIDecisionFailed, StockProfileAIStatusFailed, createErr
	}
	if syncErr := s.store.SyncStockProfileUpdateTaskAIQueue(ctx, createdTask.ID, profile.Symbol); syncErr != nil {
		return nil, AssetAIDecisionFailed, StockProfileAIStatusFailed, syncErr
	}
	return currentRun, decision, queuedProfileStatus, nil
}

func (s *Service) stockProfileAIQueueContextFailed(ctx context.Context, symbol string) (bool, error) {
	item, err := s.store.GetStockProfileAIQueueItem(ctx, symbol)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var pack StockProfileSummaryContext
	if err := json.Unmarshal([]byte(item.PayloadJSON), &pack); err != nil {
		return true, nil
	}
	return assetMaintenanceSourceFailed(pack.SourceStatuses, StockV2AnnouncementSourceCninfo), nil
}

func assetMaintenanceSourceFailed(statuses []AssetMaintenanceSourceStatus, source string) bool {
	for _, status := range statuses {
		if status.Source == source && status.Status == AssetAnnouncementStatusFailed {
			return true
		}
	}
	return false
}

func appendAnnouncementContext(primary, recent []StockV2Announcement) []StockV2Announcement {
	out := make([]StockV2Announcement, 0, len(primary)+len(recent))
	seen := make(map[string]struct{}, len(primary)+len(recent))
	for _, items := range [][]StockV2Announcement{primary, recent} {
		for _, item := range items {
			key := firstNonEmpty(item.ContentHash, item.AnnouncementID, item.ID)
			if key == "" {
				key = item.Title + "|" + item.PublishedAt.UTC().Format(time.RFC3339Nano)
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func announcementsAfterAIProfile(items []StockV2Announcement, aiUpdatedAt time.Time) []StockV2Announcement {
	if aiUpdatedAt.IsZero() {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if (item.PublishedAt.IsZero() && item.FetchedAt.IsZero()) ||
			item.PublishedAt.After(aiUpdatedAt) || item.FetchedAt.After(aiUpdatedAt) {
			out = append(out, item)
		}
	}
	return out
}

func assetAIDecision(profile StockProfile, previous StockProfile, item AssetMaintenanceItem, announcements []StockV2Announcement, force bool) string {
	if force {
		return AssetAIDecisionManualForce
	}
	status := normalizeStockProfileAIStatus(profile.AIProfileStatus)
	if status == StockProfileAIStatusMissing ||
		(status == StockProfileAIStatusReady && profile.AIProfileUpdatedAt.IsZero()) {
		return AssetAIDecisionMissing
	}
	if item.BaseProfileChanged {
		return AssetAIDecisionBaseChanged
	}
	if len(announcements) > 0 {
		return AssetAIDecisionAnnouncement
	}
	if status == StockProfileAIStatusFailed || status == StockProfileAIStatusNotConfigured {
		lastAttempt := profile.AIProfileAttemptedAt
		if lastAttempt.IsZero() {
			lastAttempt = profile.AIProfileUpdatedAt // legacy rows before attempted_at migration
		}
		if lastAttempt.IsZero() || time.Since(lastAttempt) >= assetAIBackoff {
			return AssetAIDecisionRetry
		}
		return AssetAIDecisionSkippedUnneeded
	}
	_ = previous
	return AssetAIDecisionSkippedUnneeded
}

func stockProfileInstrumentFieldsChanged(existing, current StockProfile) bool {
	if strings.TrimSpace(existing.Market) != strings.TrimSpace(current.Market) ||
		normalizeInstrumentType(existing.InstrumentType) != normalizeInstrumentType(current.InstrumentType) ||
		strings.TrimSpace(existing.Name) != strings.TrimSpace(current.Name) {
		return true
	}
	if value := strings.TrimSpace(current.Industry); value != "" && value != strings.TrimSpace(existing.Industry) {
		return true
	}
	return !profileTermsContainAll(existing.Sectors, current.Sectors) ||
		!profileTermsContainAll(existing.Concepts, current.Concepts)
}

func profileTermsContainAll(existing, current []string) bool {
	seen := make(map[string]struct{}, len(existing))
	for _, term := range cleanProfileTerms(existing) {
		seen[strings.ToLower(term)] = struct{}{}
	}
	for _, term := range cleanProfileTerms(current) {
		if _, ok := seen[strings.ToLower(term)]; !ok {
			return false
		}
	}
	return true
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
	if len(req.Symbols) > 0 {
		return limitStrings(compactStringList(req.Symbols, maxSymbols), maxSymbols), nil
	}
	normalize := func(symbols []string) []string {
		out := make([]string, 0, len(symbols))
		seen := make(map[string]struct{}, len(symbols))
		for _, symbol := range symbols {
			value, _ := normalizeQuoteSymbolInput(symbol)
			if value == "" {
				value = strings.TrimSpace(symbol)
			}
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
		return out
	}
	var priority []string
	if holdings, err := s.store.ListHoldingSymbols(ctx); err == nil {
		priority = append(priority, holdings...)
	}
	if strategySymbols, err := s.activeStrategySymbols(ctx); err == nil {
		items := make([]string, 0, len(strategySymbols))
		for symbol := range strategySymbols {
			items = append(items, symbol)
		}
		sort.Strings(items)
		priority = append(priority, items...)
	}
	stale, err := s.store.ListAssetMaintenancePrioritySymbols(
		ctx,
		time.Now().Add(-baseProfileRefreshInterval),
		time.Now().AddDate(0, 0, -7).Format("2006-01-02"),
	)
	if err != nil {
		return nil, err
	}
	priority = normalize(append(priority, stale...))

	stored, err := s.store.ListInstrumentSymbols(ctx)
	if err != nil {
		return nil, err
	}
	discovered, err := s.store.ListDiscoveredUniverseSymbols(ctx)
	if err != nil {
		return nil, err
	}
	discoveryMarker, err := s.store.GetAssetMaintenanceCursor(ctx, assetUniverseDiscoveryCursorScope)
	if err != nil {
		return nil, err
	}
	lastDiscovery, err := s.store.GetAssetMaintenanceCursorUpdatedAt(ctx, assetUniverseDiscoveryCursorScope)
	if err != nil {
		return nil, err
	}
	discoveryRefreshInterval := assetUniverseDiscoveryRefreshIntervalFor(discoveryMarker, len(discovered))
	if s.universeSource != nil && (len(discovered) == 0 || lastDiscovery.IsZero() || time.Since(lastDiscovery) >= discoveryRefreshInterval) {
		fresh := normalize(s.universeSource.GetDefaultSymbols())
		if len(fresh) >= 1000 {
			if err := s.store.CacheDiscoveredUniverseSymbols(ctx, "public_universe", fresh); err != nil {
				return nil, err
			}
			discovered = fresh
			if err := s.store.SetAssetMaintenanceCursor(ctx, assetUniverseDiscoveryCursorScope, fmt.Sprintf("full:%d", len(fresh))); err != nil {
				return nil, err
			}
		} else {
			// Never replace an existing full cache with the outage sample. On first
			// boot the sample remains useful, but its fallback marker forces a short
			// retry interval rather than treating it as fresh for seven days.
			if len(discovered) == 0 && len(fresh) > 0 {
				if err := s.store.CacheDiscoveredUniverseSymbols(ctx, "public_universe", fresh); err != nil {
					return nil, err
				}
				discovered = fresh
			}
			if err := s.store.SetAssetMaintenanceCursor(ctx, assetUniverseDiscoveryCursorScope, fmt.Sprintf("fallback:%d", len(fresh))); err != nil {
				return nil, err
			}
		}
	}
	universe := normalize(append(stored, discovered...))
	sort.Strings(universe)

	seen := make(map[string]struct{}, len(priority)+len(universe))
	out := make([]string, 0, len(priority)+len(universe))
	addUntil := func(symbols []string, limit int) string {
		lastScanned := ""
		for _, symbol := range symbols {
			if limit >= 0 && len(out) >= limit {
				break
			}
			lastScanned = symbol
			if _, ok := seen[symbol]; ok {
				continue
			}
			seen[symbol] = struct{}{}
			out = append(out, symbol)
		}
		return lastScanned
	}
	if maxSymbols <= 0 {
		addUntil(priority, -1)
		addUntil(universe, -1)
		return out, nil
	}

	reserveForCursor := maxSymbols / 4
	if reserveForCursor < 1 {
		reserveForCursor = 1
	}
	priorityBudget := maxSymbols - reserveForCursor
	priorityCursor, err := s.store.GetAssetMaintenanceCursor(ctx, assetMaintenancePriorityCursorScope)
	if err != nil {
		return nil, err
	}
	rotatedPriority := rotateSymbolsAfterExactCursor(priority, priorityCursor)
	if last := addUntil(rotatedPriority, priorityBudget); last != "" {
		if err := s.store.SetAssetMaintenanceCursor(ctx, assetMaintenancePriorityCursorScope, last); err != nil {
			return nil, err
		}
	}
	universeCursor, err := s.store.GetAssetMaintenanceCursor(ctx, assetMaintenanceUniverseCursorScope)
	if err != nil {
		return nil, err
	}
	if last := addUntil(rotateSymbolsAfterCursor(universe, universeCursor), maxSymbols); last != "" {
		if err := s.store.SetAssetMaintenanceCursor(ctx, assetMaintenanceUniverseCursorScope, last); err != nil {
			return nil, err
		}
	}
	if len(out) < maxSymbols {
		lastPriority, err := s.store.GetAssetMaintenanceCursor(ctx, assetMaintenancePriorityCursorScope)
		if err != nil {
			return nil, err
		}
		if last := addUntil(rotateSymbolsAfterExactCursor(priority, lastPriority), maxSymbols); last != "" {
			if err := s.store.SetAssetMaintenanceCursor(ctx, assetMaintenancePriorityCursorScope, last); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func assetUniverseDiscoveryRefreshIntervalFor(marker string, discoveredCount int) time.Duration {
	marker = strings.TrimSpace(marker)
	if strings.HasPrefix(marker, "fallback:") {
		return assetUniverseDiscoveryFallbackRetryInterval
	}
	// Plain numeric markers were written before discovery outcomes were typed.
	// Treat a small legacy cache as fallback so the migration self-heals without
	// waiting for the old seven-day freshness window to expire.
	if !strings.HasPrefix(marker, "full:") && discoveredCount < 1000 {
		return assetUniverseDiscoveryFallbackRetryInterval
	}
	return assetUniverseDiscoveryRefreshInterval
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
	case AssetAIDecisionSkippedUnneeded, AssetAIDecisionSkippedConfig:
		stats.AISkipped++
	}
	switch item.AIQueueStatus {
	case StockProfileAIQueueStatusReady, StockProfileAIQueueStatusRetryWait:
		stats.AIQueued++
	case StockProfileAIQueueStatusRunning:
		stats.AIRunning++
	case StockProfileAIQueueStatusCompleted:
		stats.AICompleted++
	case StockProfileAIQueueStatusFailed:
		stats.AIFailed++
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
	announcementSync := make(map[string]AnnouncementSyncState, 3)
	for _, market := range []string{"SH", "SZ", "BJ"} {
		state, exists, stateErr := s.store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, market)
		if stateErr != nil {
			return nil, stateErr
		}
		if exists {
			announcementSync[market] = state
		}
	}
	evaluatedAt := time.Now()
	out := make(map[string]StockV2AssetSummary, len(symbols))
	for _, symbol := range symbols {
		item := announcementStats[symbol]
		item.Symbol = symbol
		item.ProfileSummary = profiles[symbol]
		item.DailyBarQuality = qualities[symbol]
		item.LatestMaintenance = latestItems[symbol]
		item.Readiness = evaluateStockV2AssetReadiness(item, announcementSync[item.ProfileSummary.Market], evaluatedAt)
		out[symbol] = item
	}
	return out, nil
}

func evaluateStockV2AssetReadiness(item StockV2AssetSummary, announcementSync AnnouncementSyncState, now time.Time) StockV2AssetReadiness {
	readiness := StockV2AssetReadiness{EvaluatedAt: now, AnnouncementSyncAt: announcementSync.LastSuccessAt}
	readiness.DailyBarReady = item.DailyBarQuality.HasData && item.DailyBarQuality.Meets250 &&
		item.DailyBarQuality.IncompleteCount == 0 && !item.DailyBarQuality.Stale
	baseCheckedAt := item.ProfileSummary.BaseProfileCheckedAt
	if baseCheckedAt.IsZero() {
		baseCheckedAt = item.ProfileSummary.BaseProfileUpdatedAt
	}
	readiness.BaseProfileReady = item.ProfileSummary.Status == "ready" &&
		!baseCheckedAt.IsZero() &&
		now.Sub(baseCheckedAt) <= baseProfileRefreshInterval+24*time.Hour
	readiness.AnnouncementReady = normalizeInstrumentType(item.ProfileSummary.InstrumentType) != InstrumentTypeStock ||
		(!announcementSync.LastSuccessAt.IsZero() && now.Sub(announcementSync.LastSuccessAt) <= 36*time.Hour &&
			!announcementSync.CoveredThrough.Before(now.Add(-36*time.Hour)))
	readiness.AIProfileReady = item.ProfileSummary.AIProfileStatus == StockProfileAIStatusReady &&
		!item.ProfileSummary.AIProfileUpdatedAt.IsZero() &&
		!item.ProfileSummary.AIProfileUpdatedAt.Before(item.ProfileSummary.BaseProfileUpdatedAt) &&
		(item.LatestAnnouncementAt.IsZero() || !item.ProfileSummary.AIProfileUpdatedAt.Before(item.LatestAnnouncementAt)) &&
		(item.LatestAnnouncementFetchedAt.IsZero() || !item.ProfileSummary.AIProfileUpdatedAt.Before(item.LatestAnnouncementFetchedAt))
	readiness.DataReady = readiness.DailyBarReady && readiness.BaseProfileReady && readiness.AnnouncementReady
	readiness.Ready = readiness.DataReady && readiness.AIProfileReady
	if !readiness.DailyBarReady {
		readiness.Reasons = append(readiness.Reasons, "daily_bar_missing_or_stale")
	}
	if !readiness.BaseProfileReady {
		readiness.Reasons = append(readiness.Reasons, "base_profile_missing_or_stale")
	}
	if !readiness.AnnouncementReady {
		readiness.Reasons = append(readiness.Reasons, "announcement_coverage_missing_or_stale")
	}
	if !readiness.AIProfileReady {
		readiness.Reasons = append(readiness.Reasons, "ai_profile_missing_or_outdated")
	}
	return readiness
}
