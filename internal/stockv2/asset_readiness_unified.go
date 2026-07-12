package stockv2

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	AssetReadinessRequirementMarket   = "market"
	AssetReadinessRequirementMessage  = "message"
	AssetReadinessRequirementAnalysis = "analysis"

	AssetReadinessModeStrict        = "strict"
	AssetReadinessModeAllowDegraded = "allow_degraded"

	AssetReadinessDecisionReady    = "ready"
	AssetReadinessDecisionDegraded = "degraded"
	AssetReadinessDecisionBlocked  = "blocked"

	assetReadinessBatchSize             = 200
	assetReadinessCalendarLookback      = 45 * 24 * time.Hour
	assetReadinessCalendarMaxLag        = 36 * time.Hour
	assetReadinessCalendarClockSkew     = 5 * time.Minute
	assetReadinessAnnouncementMaxLag    = 36 * time.Hour
	assetReadinessBaseProfileGrace      = 24 * time.Hour
	AssetReadinessLimitObservedCalendar = "trading_calendar_observed_only"
	AssetReadinessLimitMajorContent     = "major_announcement_content_status_unavailable"
)

var (
	ErrInvalidAssetReadinessRequirement = errors.New("invalid asset readiness requirement")
	ErrInvalidAssetReadinessMode        = errors.New("invalid asset readiness mode")
)

type AssetReadinessReason struct {
	Symbol    string    `json:"symbol,omitempty"`
	Domain    string    `json:"domain"`
	Code      string    `json:"code"`
	Retryable bool      `json:"retryable,omitempty"`
	RetryAt   time.Time `json:"retryAt,omitempty"`
}

type UnifiedAssetReadiness struct {
	Symbol              string                 `json:"symbol"`
	Market              string                 `json:"market,omitempty"`
	InstrumentType      string                 `json:"instrumentType,omitempty"`
	MarketReady         bool                   `json:"marketReady"`
	MessageReady        bool                   `json:"messageReady"`
	AnalysisReady       bool                   `json:"analysisReady"`
	DailyBarReady       bool                   `json:"dailyBarReady"`
	BaseProfileReady    bool                   `json:"baseProfileReady"`
	AnnouncementReady   bool                   `json:"announcementReady"`
	AIProfileReady      bool                   `json:"aiProfileReady"`
	ExpectedTradeDate   string                 `json:"expectedTradeDate,omitempty"`
	MarketAsOf          string                 `json:"marketAsOf,omitempty"`
	MessageAsOf         time.Time              `json:"messageAsOf,omitempty"`
	AnnouncementSyncAt  time.Time              `json:"announcementSyncAt,omitempty"`
	DesiredInputVersion string                 `json:"desiredInputVersion,omitempty"`
	AppliedInputVersion string                 `json:"appliedInputVersion,omitempty"`
	Reasons             []AssetReadinessReason `json:"reasons,omitempty"`
	Limitations         []string               `json:"limitations,omitempty"`
	EvaluatedAt         time.Time              `json:"evaluatedAt"`
}

type AssetReadinessDecision struct {
	Status        string                 `json:"status"`
	Requirement   string                 `json:"requirement"`
	Mode          string                 `json:"mode"`
	TargetCount   int                    `json:"targetCount"`
	ReadyCount    int                    `json:"readyCount"`
	FailedSymbols []string               `json:"failedSymbols,omitempty"`
	Reasons       []AssetReadinessReason `json:"reasons,omitempty"`
}

type AssetReadinessOverview struct {
	TargetCount                     int                      `json:"targetCount"`
	EvaluatedCount                  int                      `json:"evaluatedCount"`
	MarketReadyCount                int                      `json:"marketReadyCount"`
	MessageReadyCount               int                      `json:"messageReadyCount"`
	AnalysisReadyCount              int                      `json:"analysisReadyCount"`
	ExpectedTradeDate               string                   `json:"expectedTradeDate,omitempty"`
	LimitationCounts                map[string]int           `json:"limitationCounts,omitempty"`
	ReasonCounts                    map[string]int           `json:"reasonCounts,omitempty"`
	AnnouncementBodyParserAvailable bool                     `json:"announcementBodyParserAvailable"`
	AIQueue                         StockProfileAIQueueStats `json:"aiQueue"`
	ResourceGate                    ResourceGateStatus       `json:"resourceGate"`
	EvaluatedAt                     time.Time                `json:"evaluatedAt"`
}

type assetReadinessEnvironment struct {
	expectedTradeDate     string
	calendarAuthoritative bool
	calendarObservedAt    time.Time
	announcementSync      map[string]AnnouncementSyncState
	evaluatedAt           time.Time
}

// NormalizeAssetReadinessSymbols validates and de-duplicates symbols without
// truncating them. HTTP handlers own the public batch-size limit.
func NormalizeAssetReadinessSymbols(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		symbol, _ := normalizeQuoteSymbolInput(raw)
		if !isSixDigitSymbol(symbol) {
			compact := strings.NewReplacer(".", "", "-", "", "_", "", ":", "").Replace(strings.ToUpper(strings.TrimSpace(raw)))
			for _, market := range []string{"SH", "SZ", "BJ"} {
				if strings.HasSuffix(compact, market) {
					symbol = strings.TrimSuffix(compact, market)
					break
				}
			}
		}
		if !isSixDigitSymbol(symbol) {
			return nil, fmt.Errorf("invalid stock symbol %q", strings.TrimSpace(raw))
		}
		if _, exists := seen[symbol]; exists {
			continue
		}
		seen[symbol] = struct{}{}
		out = append(out, symbol)
	}
	return out, nil
}

// EvaluateAssetReadinessBatch evaluates local persisted assets only. It never
// fetches remote data. Existing stores cap several batch queries at 200, so the
// evaluator chunks locally instead of silently truncating the requested set.
func (s *Service) EvaluateAssetReadinessBatch(ctx context.Context, symbols []string, cutoffAt time.Time) (map[string]UnifiedAssetReadiness, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("stockv2 service is not configured")
	}
	normalized, err := NormalizeAssetReadinessSymbols(symbols)
	if err != nil {
		return nil, err
	}
	if cutoffAt.IsZero() {
		cutoffAt = time.Now()
	}
	out := make(map[string]UnifiedAssetReadiness, len(normalized))
	if len(normalized) == 0 {
		return out, nil
	}

	environment, err := s.loadAssetReadinessEnvironment(ctx, cutoffAt)
	if err != nil {
		return nil, err
	}

	for start := 0; start < len(normalized); start += assetReadinessBatchSize {
		end := start + assetReadinessBatchSize
		if end > len(normalized) {
			end = len(normalized)
		}
		chunk := normalized[start:end]
		summaries, err := s.assetReadinessSummaries(ctx, chunk)
		if err != nil {
			return nil, err
		}
		items, err := s.evaluateAssetReadinessSummaryChunk(ctx, chunk, summaries, environment)
		if err != nil {
			return nil, err
		}
		for symbol, item := range items {
			out[symbol] = item
		}
	}
	return out, nil
}

func (s *Service) loadAssetReadinessEnvironment(ctx context.Context, cutoffAt time.Time) (assetReadinessEnvironment, error) {
	expectedTradeDate, calendarAuthoritative, calendarObservedAt, err := s.expectedAssetReadinessTradeDate(ctx, cutoffAt)
	if err != nil {
		return assetReadinessEnvironment{}, err
	}
	announcementSync, err := s.assetReadinessAnnouncementSync(ctx)
	if err != nil {
		return assetReadinessEnvironment{}, err
	}
	return assetReadinessEnvironment{
		expectedTradeDate:     expectedTradeDate,
		calendarAuthoritative: calendarAuthoritative,
		calendarObservedAt:    calendarObservedAt,
		announcementSync:      announcementSync,
		evaluatedAt:           cutoffAt,
	}, nil
}

func (s *Service) evaluateAssetReadinessSummaryChunk(
	ctx context.Context,
	symbols []string,
	summaries map[string]StockV2AssetSummary,
	environment assetReadinessEnvironment,
) (map[string]UnifiedAssetReadiness, error) {
	instruments, err := s.store.GetInstrumentsBySymbols(ctx, symbols)
	if err != nil {
		return nil, err
	}
	instrumentBySymbol := make(map[string]StockV2Instrument, len(instruments))
	for _, instrument := range instruments {
		instrumentBySymbol[instrument.Symbol] = instrument
	}
	out := make(map[string]UnifiedAssetReadiness, len(symbols))
	for _, symbol := range symbols {
		summary := summaries[symbol]
		summary.Symbol = symbol
		out[symbol] = evaluateUnifiedAssetReadiness(
			summary,
			instrumentBySymbol[symbol],
			environment.announcementSync,
			environment.expectedTradeDate,
			environment.calendarAuthoritative,
			environment.calendarObservedAt,
			environment.evaluatedAt,
		)
	}
	return out, nil
}

func (s *Service) assetReadinessSummaries(ctx context.Context, symbols []string) (map[string]StockV2AssetSummary, error) {
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

func (s *Service) GetAssetReadinessOverview(ctx context.Context, cutoffAt time.Time) (AssetReadinessOverview, error) {
	if s == nil || s.store == nil {
		return AssetReadinessOverview{}, errors.New("stockv2 service is not configured")
	}
	if cutoffAt.IsZero() {
		cutoffAt = time.Now()
	}
	symbols, err := s.assetReadinessOverviewSymbols(ctx)
	if err != nil {
		return AssetReadinessOverview{}, err
	}
	items, err := s.EvaluateAssetReadinessBatch(ctx, symbols, cutoffAt)
	if err != nil {
		return AssetReadinessOverview{}, err
	}
	queueStats, err := s.store.GetStockProfileAIQueueStats(ctx)
	if err != nil {
		return AssetReadinessOverview{}, err
	}
	overview := AssetReadinessOverview{
		TargetCount:                     len(symbols),
		EvaluatedCount:                  len(items),
		LimitationCounts:                map[string]int{},
		ReasonCounts:                    map[string]int{},
		AnnouncementBodyParserAvailable: announcementBodyParserAvailable(),
		AIQueue:                         queueStats,
		ResourceGate:                    s.currentResourceGate(),
		EvaluatedAt:                     cutoffAt,
	}
	for _, item := range items {
		if overview.ExpectedTradeDate == "" {
			overview.ExpectedTradeDate = item.ExpectedTradeDate
		}
		if item.MarketReady {
			overview.MarketReadyCount++
		}
		if item.MessageReady {
			overview.MessageReadyCount++
		}
		if item.AnalysisReady {
			overview.AnalysisReadyCount++
		}
		for _, limitation := range item.Limitations {
			overview.LimitationCounts[limitation]++
		}
		for _, reason := range item.Reasons {
			overview.ReasonCounts[reason.Code]++
		}
	}
	if len(overview.LimitationCounts) == 0 {
		overview.LimitationCounts = nil
	}
	if len(overview.ReasonCounts) == 0 {
		overview.ReasonCounts = nil
	}
	return overview, nil
}

func (s *Service) assetReadinessOverviewSymbols(ctx context.Context) ([]string, error) {
	discovered, err := s.store.ListDiscoveredUniverseSymbols(ctx)
	if err != nil {
		return nil, err
	}
	if len(discovered) == 0 {
		discovered, err = s.store.ListInstrumentSymbols(ctx)
		if err != nil {
			return nil, err
		}
	}
	holdings, err := s.store.ListHoldingSymbols(ctx)
	if err != nil {
		return nil, err
	}
	strategySymbols, err := s.activeStrategySymbols(ctx)
	if err != nil {
		return nil, err
	}
	symbols := append(append([]string(nil), discovered...), holdings...)
	for symbol := range strategySymbols {
		symbols = append(symbols, symbol)
	}
	symbols, err = NormalizeAssetReadinessSymbols(symbols)
	if err != nil {
		return nil, err
	}
	sort.Strings(symbols)
	return symbols, nil
}

func DecideAssetReadiness(items []UnifiedAssetReadiness, requirement, mode string) (AssetReadinessDecision, error) {
	requirement = strings.TrimSpace(requirement)
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = AssetReadinessModeStrict
	}
	if requirement != AssetReadinessRequirementMarket && requirement != AssetReadinessRequirementMessage && requirement != AssetReadinessRequirementAnalysis {
		return AssetReadinessDecision{}, ErrInvalidAssetReadinessRequirement
	}
	if mode != AssetReadinessModeStrict && mode != AssetReadinessModeAllowDegraded {
		return AssetReadinessDecision{}, ErrInvalidAssetReadinessMode
	}
	decision := AssetReadinessDecision{
		Status:      AssetReadinessDecisionReady,
		Requirement: requirement,
		Mode:        mode,
		TargetCount: len(items),
	}
	if len(items) == 0 {
		decision.Status = AssetReadinessDecisionBlocked
		if mode == AssetReadinessModeAllowDegraded {
			decision.Status = AssetReadinessDecisionDegraded
		}
		decision.Reasons = []AssetReadinessReason{{Domain: requirement, Code: "asset_list_empty"}}
		return decision, nil
	}
	for _, item := range items {
		if assetReadinessRequirementMet(item, requirement) {
			decision.ReadyCount++
			continue
		}
		decision.FailedSymbols = append(decision.FailedSymbols, item.Symbol)
		for _, reason := range item.Reasons {
			if assetReadinessReasonMatchesRequirement(reason, requirement) {
				decision.Reasons = append(decision.Reasons, reason)
			}
		}
	}
	if len(decision.FailedSymbols) == 0 {
		return decision, nil
	}
	sort.Strings(decision.FailedSymbols)
	sort.SliceStable(decision.Reasons, func(i, j int) bool {
		left, right := decision.Reasons[i], decision.Reasons[j]
		if left.Symbol != right.Symbol {
			return left.Symbol < right.Symbol
		}
		if left.Domain != right.Domain {
			return left.Domain < right.Domain
		}
		return left.Code < right.Code
	})
	if mode == AssetReadinessModeAllowDegraded {
		decision.Status = AssetReadinessDecisionDegraded
	} else {
		decision.Status = AssetReadinessDecisionBlocked
	}
	return decision, nil
}

func (s *Service) expectedAssetReadinessTradeDate(ctx context.Context, cutoffAt time.Time) (string, bool, time.Time, error) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	end := effectiveDailyBarEnd(cutoffAt, loc)
	start := end.Add(-assetReadinessCalendarLookback)
	dates, err := s.store.GetObservedTradingDates(ctx, dateString(start), dateString(end))
	if err != nil {
		return "", false, time.Time{}, err
	}
	if len(dates) == 0 {
		return "", false, time.Time{}, nil
	}
	latest := dates[len(dates)-1]
	authoritative, observedAt, err := s.store.TradingCalendarDateProvenance(ctx, latest)
	return latest, authoritative, observedAt, err
}

func (s *Service) assetReadinessAnnouncementSync(ctx context.Context) (map[string]AnnouncementSyncState, error) {
	out := make(map[string]AnnouncementSyncState, 3)
	for _, market := range []string{"SH", "SZ", "BJ"} {
		state, exists, err := s.store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, market)
		if err != nil {
			return nil, err
		}
		if exists {
			out[market] = state
		}
	}
	return out, nil
}

func evaluateUnifiedAssetReadiness(
	summary StockV2AssetSummary,
	instrument StockV2Instrument,
	announcementSync map[string]AnnouncementSyncState,
	expectedTradeDate string,
	calendarAuthoritative bool,
	calendarObservedAt time.Time,
	cutoffAt time.Time,
) UnifiedAssetReadiness {
	market := strings.TrimSpace(instrument.Market)
	if market == "" {
		market = strings.TrimSpace(summary.ProfileSummary.Market)
	}
	instrumentType := normalizeInstrumentType(instrument.InstrumentType)
	if strings.TrimSpace(instrument.InstrumentType) == "" {
		instrumentType = normalizeInstrumentType(summary.ProfileSummary.InstrumentType)
	}
	item := UnifiedAssetReadiness{
		Symbol:            summary.Symbol,
		Market:            market,
		InstrumentType:    instrumentType,
		ExpectedTradeDate: expectedTradeDate,
		EvaluatedAt:       cutoffAt,
	}
	if expectedTradeDate != "" && !calendarAuthoritative {
		item.Limitations = append(item.Limitations, AssetReadinessLimitObservedCalendar)
	}

	quality := summary.DailyBarQuality
	item.DailyBarReady = true
	if !quality.HasData {
		item.DailyBarReady = false
		item.addReason("market", "daily_bar_missing", true, time.Time{})
	}
	if quality.HasData && !quality.CoverageKnown {
		item.DailyBarReady = false
		item.addReason("market", "daily_bar_coverage_unverified", true, time.Time{})
	}
	if quality.DateGapCount > 0 {
		item.DailyBarReady = false
		item.addReason("market", "daily_bar_date_gaps", true, time.Time{})
	}
	if quality.HasData && (quality.CoreGapCount > 0 || quality.FlowGapCount > 0 ||
		(quality.DateGapCount == 0 && (quality.IncompleteCount > 0 || !quality.FacetsComplete))) {
		item.DailyBarReady = false
		item.addReason("market", "daily_bar_fields_incomplete", true, time.Time{})
	}
	if expectedTradeDate == "" {
		item.DailyBarReady = false
		item.addReason("market", "trading_calendar_unavailable", true, time.Time{})
	} else {
		if !calendarAuthoritative {
			item.DailyBarReady = false
			item.addReason("market", "trading_calendar_not_authoritative", true, time.Time{})
		}
		if !assetReadinessCalendarObservationFresh(calendarObservedAt, cutoffAt) {
			item.DailyBarReady = false
			item.addReason("market", "trading_calendar_stale", true, time.Time{})
		}
	}
	if expectedTradeDate != "" && quality.ExpectedLatestDate != expectedTradeDate {
		item.DailyBarReady = false
		item.addReason("market", "daily_bar_coverage_outdated", true, time.Time{})
	}
	item.MarketReady = item.DailyBarReady
	if item.MarketReady {
		item.MarketAsOf = quality.ExpectedLatestDate
	}

	baseCheckedAt := summary.ProfileSummary.BaseProfileCheckedAt
	if baseCheckedAt.IsZero() {
		baseCheckedAt = summary.ProfileSummary.BaseProfileUpdatedAt
	}
	item.BaseProfileReady = summary.ProfileSummary.Status == "ready" &&
		!baseCheckedAt.IsZero() &&
		!baseCheckedAt.After(cutoffAt.Add(assetReadinessCalendarClockSkew)) &&
		cutoffAt.Sub(baseCheckedAt) <= baseProfileRefreshInterval+assetReadinessBaseProfileGrace
	if !item.BaseProfileReady {
		code := "base_profile_missing"
		if !baseCheckedAt.IsZero() {
			code = "base_profile_stale"
		}
		item.addReason("message", code, true, time.Time{})
	}

	item.AnnouncementReady = instrumentType != InstrumentTypeStock
	if instrumentType == InstrumentTypeStock {
		state, exists := announcementSync[market]
		coverageCutoff := cutoffAt.Add(-assetReadinessAnnouncementMaxLag)
		if exists {
			item.AnnouncementSyncAt = state.LastSuccessAt
		}
		cursorReady := exists && !state.LastSuccessAt.IsZero() && !state.CoveredThrough.IsZero() &&
			!state.LastSuccessAt.Before(coverageCutoff) && !state.CoveredThrough.Before(coverageCutoff) &&
			!state.LastSuccessAt.After(cutoffAt.Add(announcementSyncClockSkew)) &&
			!state.CoveredThrough.After(cutoffAt.Add(announcementSyncClockSkew))
		lateRecheckReady := exists && announcementLateRecheckReady(state, cutoffAt)
		item.AnnouncementReady = cursorReady && lateRecheckReady
		if !cursorReady {
			item.addReason("message", "announcement_cursor_behind", true, time.Time{})
		}
		if !lateRecheckReady {
			item.addReason("message", "announcement_late_recheck_incomplete", true, time.Time{})
		}
		if item.AnnouncementReady {
			item.MessageAsOf = state.CoveredThrough
		}
	}
	majorContentReady := summary.MajorAnnouncementContentUnavailableCount == 0
	if !majorContentReady {
		// ponytail: only a verified text_ready body can clear this limitation;
		// processing, retry, parser-unavailable, and terminal scan-only PDFs remain stale.
		item.Limitations = append(item.Limitations, AssetReadinessLimitMajorContent)
		item.addReason("analysis", "major_announcement_content_unavailable", true, time.Time{})
	}
	item.MessageReady = item.BaseProfileReady && item.AnnouncementReady

	item.DesiredInputVersion = strings.TrimSpace(summary.ProfileSummary.AIDesiredInputVersion)
	item.AppliedInputVersion = strings.TrimSpace(summary.ProfileSummary.AIAppliedInputVersion)
	item.AIProfileReady = summary.ProfileSummary.AIProfileStatus == StockProfileAIStatusReady &&
		item.DesiredInputVersion != "" && item.DesiredInputVersion == item.AppliedInputVersion
	switch {
	case summary.ProfileSummary.AIProfileStatus != StockProfileAIStatusReady:
		item.addReason("analysis", "ai_profile_missing_or_not_ready", true, time.Time{})
	case item.DesiredInputVersion == "":
		item.addReason("analysis", "ai_desired_input_version_missing", true, time.Time{})
	case item.DesiredInputVersion != item.AppliedInputVersion:
		item.addReason("analysis", "ai_input_version_outdated", true, time.Time{})
	}
	item.AnalysisReady = item.MarketReady && item.MessageReady && item.AIProfileReady && majorContentReady
	return item
}

func assetReadinessCalendarObservationFresh(observedAt, cutoffAt time.Time) bool {
	if observedAt.IsZero() || cutoffAt.IsZero() {
		return false
	}
	if observedAt.After(cutoffAt.Add(assetReadinessCalendarClockSkew)) {
		return false
	}
	return cutoffAt.Sub(observedAt) <= assetReadinessCalendarMaxLag
}

func announcementLateRecheckReady(state AnnouncementSyncState, cutoffAt time.Time) bool {
	if cutoffAt.IsZero() || state.LateRecheckStartedAt.IsZero() ||
		state.LateRecheckCoveredThrough.IsZero() || state.LastLateRecheckAt.IsZero() {
		return false
	}
	latestAllowed := cutoffAt.Add(announcementSyncClockSkew)
	if state.LateRecheckStartedAt.After(latestAllowed) ||
		state.LateRecheckCoveredThrough.After(latestAllowed) ||
		state.LastLateRecheckAt.After(latestAllowed) ||
		state.LastLateRecheckAt.Before(cutoffAt.Add(-assetReadinessAnnouncementMaxLag)) ||
		!announcementShanghaiDay(state.LateRecheckCoveredThrough).Before(announcementShanghaiDay(cutoffAt)) {
		return false
	}
	bootstrapTarget := announcementShanghaiDay(state.LateRecheckStartedAt).AddDate(0, 0, -1)
	rollingTarget := announcementShanghaiDay(cutoffAt).AddDate(0, 0, -announcementLateRecheckLookbackDays)
	if rollingTarget.After(bootstrapTarget) {
		bootstrapTarget = rollingTarget
	}
	return !announcementShanghaiDay(state.LateRecheckCoveredThrough).Before(bootstrapTarget)
}

func (item *UnifiedAssetReadiness) addReason(domain, code string, retryable bool, retryAt time.Time) {
	for _, reason := range item.Reasons {
		if reason.Domain == domain && reason.Code == code {
			return
		}
	}
	item.Reasons = append(item.Reasons, AssetReadinessReason{Symbol: item.Symbol, Domain: domain, Code: code, Retryable: retryable, RetryAt: retryAt})
}

func assetReadinessRequirementMet(item UnifiedAssetReadiness, requirement string) bool {
	switch requirement {
	case AssetReadinessRequirementMarket:
		return item.MarketReady
	case AssetReadinessRequirementMessage:
		return item.MessageReady
	case AssetReadinessRequirementAnalysis:
		return item.AnalysisReady
	default:
		return false
	}
}

func assetReadinessReasonMatchesRequirement(reason AssetReadinessReason, requirement string) bool {
	switch requirement {
	case AssetReadinessRequirementMarket:
		return reason.Domain == "market"
	case AssetReadinessRequirementMessage:
		return reason.Domain == "message"
	case AssetReadinessRequirementAnalysis:
		return reason.Domain == "market" || reason.Domain == "message" || reason.Domain == "analysis"
	default:
		return false
	}
}
