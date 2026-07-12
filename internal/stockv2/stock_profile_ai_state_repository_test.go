package stockv2

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestBackfillStockProfileAIStatesPreservesAndVersionsLegacySummary(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	baseAt := time.Now().Add(-3 * time.Hour).Truncate(time.Millisecond)
	aiAt := baseAt.Add(time.Hour)
	profile, err := store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行",
		BusinessSummary: "银行业务", BusinessSummaryZh: "旧 AI 总结", ProfileText: "旧 AI 总结",
		ProfileTextZh: "旧 AI 总结", BaseProfileHash: "base-v1", BaseProfileUpdatedAt: baseAt,
		BaseProfileCheckedAt: baseAt, AIProfileStatus: StockProfileAIStatusReady,
		AIProfileModel: "legacy-model", AIProfileConfidence: 0.8, AIProfileUpdatedAt: aiAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stockv2_stock_profile_ai_states", "stockv2_stock_profile_ai_versions"} {
		if _, err := store.marketDB.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE symbol = ?`, profile.Symbol); err != nil {
			t.Fatal(err)
		}
	}

	count, err := store.BackfillStockProfileAIStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backfilled = %d, want 1", count)
	}
	state, exists, err := store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("state exists=%v err=%v", exists, err)
	}
	if state.DesiredInputVersion == "" || state.DesiredInputVersion != state.AppliedInputVersion {
		t.Fatalf("fresh legacy state = %+v", state)
	}
	var resultJSON string
	if err := store.marketDB.db.QueryRowContext(ctx, `
		SELECT result_json FROM stockv2_stock_profile_ai_versions
		WHERE symbol = ? AND input_version = ?
	`, profile.Symbol, state.AppliedInputVersion).Scan(&resultJSON); err != nil {
		t.Fatal(err)
	}
	if resultJSON == "" {
		t.Fatal("legacy summary version result is empty")
	}
	if second, err := store.BackfillStockProfileAIStates(ctx); err != nil || second != 0 {
		t.Fatalf("second backfill count=%d err=%v", second, err)
	}
}

func TestBackfillStockProfileAIStatesMarksAnnouncementNewerThanSummaryPending(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	baseAt := time.Now().Add(-4 * time.Hour).Truncate(time.Millisecond)
	aiAt := baseAt.Add(time.Hour)
	profile, err := store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行",
		BusinessSummary: "银行业务", BusinessSummaryZh: "旧 AI 总结", ProfileText: "旧 AI 总结",
		ProfileTextZh: "旧 AI 总结", BaseProfileHash: "base-v1", BaseProfileUpdatedAt: baseAt,
		AIProfileStatus: StockProfileAIStatusReady, AIProfileUpdatedAt: aiAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	announcementAt := aiAt.Add(time.Hour)
	if _, err := store.marketDB.db.ExecContext(ctx, `
		INSERT INTO stockv2_announcements (
			id, source, symbol, title, announcement_id, content_hash, dedupe_key,
			published_at, fetched_at, first_fetched_at, last_seen_at, body_status,
			created_at, updated_at
		) VALUES ('ann-1', 'cninfo', ?, '重大事项', 'ann-1', 'hash-1',
			'cninfo:id:ann-1', ?, ?, ?, ?, 'metadata_only', ?, ?)
	`, profile.Symbol, announcementAt, announcementAt, announcementAt, announcementAt,
		announcementAt, announcementAt); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stockv2_stock_profile_ai_states", "stockv2_stock_profile_ai_versions"} {
		if _, err := store.marketDB.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE symbol = ?`, profile.Symbol); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := store.BackfillStockProfileAIStates(ctx); err != nil {
		t.Fatal(err)
	}
	state, exists, err := store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("state exists=%v err=%v", exists, err)
	}
	if state.DesiredInputVersion == state.AppliedInputVersion || state.AppliedInputVersion == "" {
		t.Fatalf("stale legacy state = %+v", state)
	}
	if state.DesiredTriggerReason != AssetAIDecisionAnnouncement || state.AnnouncementRevision != 1 {
		t.Fatalf("announcement target = %+v", state)
	}
}

func TestReadyStockProfileAITargetDoesNotAdvanceForDailySummaryAlone(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	profile, err := store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行",
		BusinessSummary: "银行业务", BusinessSummaryZh: "银行业务",
		ProfileText: "银行业务", ProfileTextZh: "银行业务", BaseProfileHash: "base-v1",
		AIProfileStatus: StockProfileAIStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.marketDB.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_states
		SET applied_input_version = desired_input_version,
		    profile_schema_version = 2,
		    data_summary_version = ''
		WHERE symbol = ?
	`, profile.Symbol); err != nil {
		t.Fatal(err)
	}
	before, exists, err := store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("state exists=%v err=%v", exists, err)
	}
	if err := store.UpsertDailyBars(ctx, []StockV2DailyBar{
		stockProfileAITestDailyBar(profile.Symbol, "2026-07-10", 12, 300),
	}); err != nil {
		t.Fatal(err)
	}
	refreshed, changed, err := store.RefreshPendingStockProfileAIDataSummary(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if changed || refreshed.DesiredInputVersion != before.DesiredInputVersion {
		t.Fatalf("ready target advanced for daily data only: before=%+v after=%+v", before, refreshed)
	}
	if _, err := store.UpsertStockProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	after, _, err := store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if after.DesiredInputVersion != before.DesiredInputVersion || after.AppliedInputVersion != before.AppliedInputVersion {
		t.Fatalf("no-op base refresh advanced ready legacy state: before=%+v after=%+v", before, after)
	}
}
