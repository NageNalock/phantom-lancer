package stockv2

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAssetReadinessSymbolsDeduplicatesWithoutTruncating(t *testing.T) {
	got, err := NormalizeAssetReadinessSymbols([]string{"SZ000001", "000001", "600000.SH", " 600000 "})
	if err != nil {
		t.Fatalf("normalize symbols: %v", err)
	}
	if len(got) != 2 || got[0] != "000001" || got[1] != "600000" {
		t.Fatalf("symbols = %#v, want [000001 600000]", got)
	}
	if _, err := NormalizeAssetReadinessSymbols([]string{"invalid"}); err == nil {
		t.Fatal("invalid symbol was accepted")
	}
}

func TestDecideAssetReadinessStrictAndDegraded(t *testing.T) {
	items := []UnifiedAssetReadiness{
		{Symbol: "000001", AnalysisReady: true},
		{
			Symbol:  "600000",
			Reasons: []AssetReadinessReason{{Domain: "analysis", Code: "ai_input_version_outdated", Retryable: true}},
		},
	}
	strict, err := DecideAssetReadiness(items, AssetReadinessRequirementAnalysis, AssetReadinessModeStrict)
	if err != nil {
		t.Fatalf("strict decision: %v", err)
	}
	if strict.Status != AssetReadinessDecisionBlocked || strict.ReadyCount != 1 || len(strict.FailedSymbols) != 1 || strict.FailedSymbols[0] != "600000" {
		t.Fatalf("strict decision = %+v", strict)
	}
	degraded, err := DecideAssetReadiness(items, AssetReadinessRequirementAnalysis, AssetReadinessModeAllowDegraded)
	if err != nil {
		t.Fatalf("degraded decision: %v", err)
	}
	if degraded.Status != AssetReadinessDecisionDegraded || degraded.ReadyCount != 1 {
		t.Fatalf("degraded decision = %+v", degraded)
	}
}

func TestEvaluateUnifiedAssetReadinessRequiresMatchingAuthoritativeAIVersion(t *testing.T) {
	cutoff := time.Date(2026, 7, 10, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	summary := StockV2AssetSummary{
		Symbol: "000001",
		DailyBarQuality: DailyBarsQuality{
			HasData: true, CoverageKnown: true, FacetsComplete: true, LatestDate: "2026-07-10", ExpectedLatestDate: "2026-07-10",
		},
		ProfileSummary: StockProfileSummary{
			Symbol:                "000001",
			Market:                "SZ",
			InstrumentType:        InstrumentTypeStock,
			Status:                "ready",
			BaseProfileCheckedAt:  cutoff.Add(-time.Hour),
			AIProfileStatus:       StockProfileAIStatusReady,
			AIDesiredInputVersion: "version-2",
			AIAppliedInputVersion: "version-1",
		},
	}
	instrument := StockV2Instrument{Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock}
	syncState := map[string]AnnouncementSyncState{
		"SZ": readyAnnouncementSyncState(cutoff),
	}

	got := evaluateUnifiedAssetReadiness(summary, instrument, syncState, "2026-07-10", true, cutoff.Add(-time.Hour), cutoff)
	if got.AIProfileReady || got.AnalysisReady || !readinessHasReason(got.Reasons, "analysis", "ai_input_version_outdated") {
		t.Fatalf("outdated AI version readiness = %+v", got)
	}

	summary.ProfileSummary.AIAppliedInputVersion = "version-2"
	got = evaluateUnifiedAssetReadiness(summary, instrument, syncState, "2026-07-10", true, cutoff.Add(-time.Hour), cutoff)
	if !got.AIProfileReady || !got.AnalysisReady {
		t.Fatalf("matching AI version readiness = %+v", got)
	}

	summary.ProfileSummary.AIDesiredInputVersion = ""
	summary.ProfileSummary.AIAppliedInputVersion = ""
	got = evaluateUnifiedAssetReadiness(summary, instrument, syncState, "2026-07-10", true, cutoff.Add(-time.Hour), cutoff)
	if got.AIProfileReady || !readinessHasReason(got.Reasons, "analysis", "ai_desired_input_version_missing") {
		t.Fatalf("missing desired AI version readiness = %+v", got)
	}

	summary.ProfileSummary.BaseProfileCheckedAt = cutoff.Add(assetReadinessCalendarClockSkew + time.Second)
	got = evaluateUnifiedAssetReadiness(summary, instrument, syncState, "2026-07-10", true, cutoff.Add(-time.Hour), cutoff)
	if got.BaseProfileReady || got.MessageReady || !readinessHasReason(got.Reasons, "message", "base_profile_stale") {
		t.Fatalf("future base-profile check readiness = %+v", got)
	}
}

func TestEvaluateUnifiedAssetReadinessAcceptsVerifiedNoTradeCoverage(t *testing.T) {
	cutoff := time.Date(2026, 7, 10, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	got := evaluateUnifiedAssetReadiness(StockV2AssetSummary{
		Symbol: "600000",
		DailyBarQuality: DailyBarsQuality{
			HasData:              true,
			CoverageKnown:        true,
			FacetsComplete:       true,
			ExpectedDateCount:    250,
			CoveredDateCount:     250,
			VerifiedNoTradeCount: 5,
			LatestDate:           "2026-07-03",
			ExpectedLatestDate:   "2026-07-10",
			Stale:                true,
		},
		ProfileSummary: StockProfileSummary{
			Symbol:                "600000",
			Market:                "SH",
			InstrumentType:        InstrumentTypeStock,
			Status:                "ready",
			BaseProfileCheckedAt:  cutoff.Add(-time.Hour),
			AIProfileStatus:       StockProfileAIStatusReady,
			AIDesiredInputVersion: "version-1",
			AIAppliedInputVersion: "version-1",
		},
	}, StockV2Instrument{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
	}, map[string]AnnouncementSyncState{
		"SH": readyAnnouncementSyncState(cutoff),
	}, "2026-07-10", true, cutoff.Add(-time.Hour), cutoff)
	if !got.MarketReady || got.MarketAsOf != "2026-07-10" || !got.AnalysisReady {
		t.Fatalf("verified no-trade readiness = %+v", got)
	}
}

func TestEvaluateUnifiedAssetReadinessBlocksStaleOrObservedOnlyCalendar(t *testing.T) {
	cutoff := time.Date(2026, 7, 10, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	summary := StockV2AssetSummary{
		Symbol: "000001",
		DailyBarQuality: DailyBarsQuality{
			HasData: true, CoverageKnown: true, FacetsComplete: true,
			ExpectedLatestDate: "2026-07-10",
		},
		ProfileSummary: StockProfileSummary{
			Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock,
			Status: "ready", BaseProfileCheckedAt: cutoff.Add(-time.Hour),
			AIProfileStatus:       StockProfileAIStatusReady,
			AIDesiredInputVersion: "version-1", AIAppliedInputVersion: "version-1",
		},
	}
	instrument := StockV2Instrument{Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock}
	syncState := map[string]AnnouncementSyncState{
		"SZ": readyAnnouncementSyncState(cutoff),
	}

	stale := evaluateUnifiedAssetReadiness(
		summary, instrument, syncState, "2026-07-10", true,
		cutoff.Add(-assetReadinessCalendarMaxLag-time.Second), cutoff,
	)
	if stale.MarketReady || stale.AnalysisReady || !readinessHasReason(stale.Reasons, "market", "trading_calendar_stale") {
		t.Fatalf("stale authoritative calendar readiness = %+v", stale)
	}

	observedOnly := evaluateUnifiedAssetReadiness(
		summary, instrument, syncState, "2026-07-10", false, cutoff.Add(-time.Hour), cutoff,
	)
	if observedOnly.MarketReady || observedOnly.AnalysisReady ||
		!readinessHasReason(observedOnly.Reasons, "market", "trading_calendar_not_authoritative") {
		t.Fatalf("observed-only calendar readiness = %+v", observedOnly)
	}
}

func TestAssetReadinessCalendarObservationFreshRejectsFutureAndMissingValues(t *testing.T) {
	cutoff := time.Date(2026, 7, 10, 18, 0, 0, 0, time.UTC)
	for name, observedAt := range map[string]time.Time{
		"missing": {},
		"future":  cutoff.Add(assetReadinessCalendarClockSkew + time.Second),
		"stale":   cutoff.Add(-assetReadinessCalendarMaxLag - time.Second),
	} {
		if assetReadinessCalendarObservationFresh(observedAt, cutoff) {
			t.Fatalf("%s calendar observation unexpectedly fresh: %v", name, observedAt)
		}
	}
	if !assetReadinessCalendarObservationFresh(cutoff.Add(-time.Hour), cutoff) {
		t.Fatal("recent calendar observation reported stale")
	}
}

func TestEvaluateUnifiedAssetReadinessBlocksAnalysisWhenMajorContentUnavailable(t *testing.T) {
	cutoff := time.Date(2026, 7, 10, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	summary := StockV2AssetSummary{
		Symbol: "600000", MajorAnnouncementCount: 1,
		MajorAnnouncementContentUnavailableCount: 1,
		DailyBarQuality: DailyBarsQuality{
			HasData: true, CoverageKnown: true, FacetsComplete: true,
			ExpectedLatestDate: "2026-07-10",
		},
		ProfileSummary: StockProfileSummary{
			Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
			Status: "ready", BaseProfileCheckedAt: cutoff.Add(-time.Hour),
			AIProfileStatus:       StockProfileAIStatusReady,
			AIDesiredInputVersion: "version-1", AIAppliedInputVersion: "version-1",
		},
	}
	item := evaluateUnifiedAssetReadiness(
		summary,
		StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock},
		map[string]AnnouncementSyncState{
			"SH": readyAnnouncementSyncState(cutoff),
		},
		"2026-07-10", true, cutoff.Add(-time.Hour), cutoff,
	)
	if !item.MarketReady || !item.MessageReady || !item.AIProfileReady || item.AnalysisReady ||
		!readinessHasReason(item.Reasons, "analysis", "major_announcement_content_unavailable") {
		t.Fatalf("major content readiness = %+v", item)
	}
	decision, err := DecideAssetReadiness([]UnifiedAssetReadiness{item}, AssetReadinessRequirementAnalysis, AssetReadinessModeStrict)
	if err != nil || decision.Status != AssetReadinessDecisionBlocked {
		t.Fatalf("strict major content decision = %+v, err=%v", decision, err)
	}
}

func TestDecideAssetReadinessFiltersReasonsByRequirement(t *testing.T) {
	item := UnifiedAssetReadiness{
		Symbol: "000001",
		Reasons: []AssetReadinessReason{
			{Domain: "market", Code: "daily_bar_missing"},
			{Domain: "message", Code: "base_profile_missing"},
			{Domain: "analysis", Code: "ai_profile_missing_or_not_ready"},
		},
	}
	decision, err := DecideAssetReadiness([]UnifiedAssetReadiness{item}, AssetReadinessRequirementMessage, "")
	if err != nil {
		t.Fatalf("message decision: %v", err)
	}
	if decision.Status != AssetReadinessDecisionBlocked || len(decision.Reasons) != 1 || decision.Reasons[0].Domain != "message" {
		t.Fatalf("message decision = %+v", decision)
	}
	if _, err := DecideAssetReadiness([]UnifiedAssetReadiness{item}, "unknown", AssetReadinessModeStrict); err != ErrInvalidAssetReadinessRequirement {
		t.Fatalf("invalid requirement error = %v", err)
	}
}

func TestDecideAssetReadinessEmptyListRequiresExplicitDegradedMode(t *testing.T) {
	strict, err := DecideAssetReadiness(nil, AssetReadinessRequirementAnalysis, AssetReadinessModeStrict)
	if err != nil || strict.Status != AssetReadinessDecisionBlocked {
		t.Fatalf("strict empty decision = %+v, err=%v", strict, err)
	}
	degraded, err := DecideAssetReadiness(nil, AssetReadinessRequirementAnalysis, AssetReadinessModeAllowDegraded)
	if err != nil || degraded.Status != AssetReadinessDecisionDegraded {
		t.Fatalf("degraded empty decision = %+v, err=%v", degraded, err)
	}
}

func TestEvaluateAssetReadinessBatchUsesLocalCalendarAndKeepsEverySymbol(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(filepath.Join(dir, "stockv2.db"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, nil)
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "inst-000001", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "测试一"},
		{ID: "inst-600000", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "测试二"},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatalf("upsert instrument %s: %v", instrument.Symbol, err)
		}
	}
	if err := store.UpsertObservedTradingDates(ctx, []string{"2026-07-09", "2026-07-10"}, time.Date(2026, 7, 10, 17, 0, 0, 0, time.FixedZone("CST", 8*60*60))); err != nil {
		t.Fatalf("upsert calendar: %v", err)
	}
	cutoff := time.Date(2026, 7, 11, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	items, err := svc.EvaluateAssetReadinessBatch(ctx, []string{"SZ000001", "600000", "000001"}, cutoff)
	if err != nil {
		t.Fatalf("evaluate readiness: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	for symbol, item := range items {
		if item.Symbol != symbol || item.ExpectedTradeDate != "2026-07-10" {
			t.Fatalf("item %s = %+v", symbol, item)
		}
		if item.MarketReady || item.MessageReady || item.AnalysisReady {
			t.Fatalf("empty local asset reported ready: %+v", item)
		}
		if !readinessHasReason(item.Reasons, "market", "daily_bar_missing") {
			t.Fatalf("item %s reasons = %+v, want daily_bar_missing", symbol, item.Reasons)
		}
	}
}

func TestGetAssetReadinessOverviewUsesCurrentUniverseAndProtectedSymbols(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(filepath.Join(dir, "stockv2.db"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, nil)
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "inst-000001", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock},
		{ID: "inst-600000", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock},
		{ID: "inst-300001", Symbol: "300001", Market: "SZ", InstrumentType: InstrumentTypeStock},
		{ID: "inst-688001", Symbol: "688001", Market: "SH", InstrumentType: InstrumentTypeStock},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatalf("upsert instrument %s: %v", instrument.Symbol, err)
		}
	}
	if err := store.ReplaceDiscoveredUniverseSymbols(ctx, assetUniverseSnapshotSourceLive, []string{"000001"}); err != nil {
		t.Fatalf("replace discovered universe: %v", err)
	}
	portfolio := StockV2Portfolio{ID: "readiness-protected", Name: "测试组合", Cash: 10000, RiskLevel: "medium"}
	if err := store.CreatePortfolio(ctx, portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := store.CreateHolding(ctx, StockV2Holding{
		ID: "holding-600000", PortfolioID: portfolio.ID, Symbol: "600000", Market: "SH", Quantity: 100,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}
	if _, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name: "活跃策略", Kind: StrategyKindSymbolStrategy, Scope: StrategyScopeResearch,
		Source: StrategySourceManual, Status: StrategyStatusActive,
		Symbol: "300001", Market: "SZ", Direction: StrategyDirectionWatch,
	}); err != nil {
		t.Fatalf("create active strategy: %v", err)
	}

	symbols, err := svc.assetReadinessOverviewSymbols(ctx)
	if err != nil {
		t.Fatalf("overview symbols: %v", err)
	}
	if got := strings.Join(symbols, ","); got != "000001,300001,600000" {
		t.Fatalf("overview symbols = %q, want current generation plus protected symbols", got)
	}
	overview, err := svc.GetAssetReadinessOverview(ctx, time.Now())
	if err != nil {
		t.Fatalf("get readiness overview: %v", err)
	}
	if overview.TargetCount != 3 || overview.EvaluatedCount != 3 || overview.ReasonCounts["daily_bar_missing"] != 3 {
		t.Fatalf("overview = %+v", overview)
	}
}

func TestEvaluateUnifiedAssetReadinessRequiresLateAnnouncementRecheckCoverage(t *testing.T) {
	cutoff := time.Date(2026, 7, 12, 18, 0, 0, 0, chinaMarketTZ)
	summary := StockV2AssetSummary{
		Symbol: "000001",
		DailyBarQuality: DailyBarsQuality{
			HasData: true, CoverageKnown: true, FacetsComplete: true,
			ExpectedLatestDate: "2026-07-12",
		},
		ProfileSummary: StockProfileSummary{
			Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock,
			Status: "ready", BaseProfileCheckedAt: cutoff.Add(-time.Hour),
			AIProfileStatus:       StockProfileAIStatusReady,
			AIDesiredInputVersion: "version-1", AIAppliedInputVersion: "version-1",
		},
	}
	instrument := StockV2Instrument{Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock}
	state := readyAnnouncementSyncState(cutoff)
	state.LateRecheckCoveredThrough = announcementShanghaiDay(state.LateRecheckStartedAt).
		AddDate(0, 0, -2)
	item := evaluateUnifiedAssetReadiness(
		summary, instrument, map[string]AnnouncementSyncState{"SZ": state},
		"2026-07-12", true, cutoff.Add(-time.Hour), cutoff,
	)
	if item.AnnouncementReady || item.MessageReady ||
		!readinessHasReason(item.Reasons, "message", "announcement_late_recheck_incomplete") {
		t.Fatalf("incomplete late recheck readiness = %+v", item)
	}

	state = readyAnnouncementSyncState(cutoff)
	state.LastSuccessAt = cutoff.Add(announcementSyncClockSkew + time.Second)
	item = evaluateUnifiedAssetReadiness(
		summary, instrument, map[string]AnnouncementSyncState{"SZ": state},
		"2026-07-12", true, cutoff.Add(-time.Hour), cutoff,
	)
	if item.AnnouncementReady || !readinessHasReason(item.Reasons, "message", "announcement_cursor_behind") {
		t.Fatalf("future announcement cursor readiness = %+v", item)
	}
}

func readyAnnouncementSyncState(cutoff time.Time) AnnouncementSyncState {
	return AnnouncementSyncState{
		CoveredThrough:            cutoff.Add(-time.Hour),
		LastSuccessAt:             cutoff.Add(-time.Hour),
		LateRecheckStartedAt:      cutoff.AddDate(0, 0, -31),
		LateRecheckCoveredThrough: announcementShanghaiDay(cutoff).AddDate(0, 0, -1),
		LastLateRecheckAt:         cutoff.Add(-time.Hour),
	}
}

func readinessHasReason(reasons []AssetReadinessReason, domain, code string) bool {
	for _, reason := range reasons {
		if reason.Domain == domain && reason.Code == code {
			return true
		}
	}
	return false
}
