package stockv2

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewServiceMarksInterruptedUniverseUpdateJobFailed(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	job := StockV2UpdateJob{
		ID:            "job-interrupted",
		TriggerType:   "manual",
		TriggerSource: "test",
		Status:        "running",
		StartAt:       time.Now().Add(-time.Hour),
	}
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatalf("create update job: %v", err)
	}

	svc := NewService(store, nil, nil)
	defer svc.StopBackground()

	got, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.ErrorMessage, "service restart") {
		t.Fatalf("error message = %q, want restart reason", got.ErrorMessage)
	}
}

func TestUniverseMaintenanceFreshSkipRequiresReadyDailyBars(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-fresh",
		Symbol:         "000001",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "平安银行",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert fresh instrument: %v", err)
	}
	if err := svc.store.UpsertDailyBars(ctx, readyDailyBarsForTest("000001", "SZ", time.Now())); err != nil {
		t.Fatalf("upsert ready bars: %v", err)
	}

	quality, err := svc.GetDailyBarsQuality(ctx, "000001", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get daily bar quality: %v", err)
	}
	skip, err := svc.shouldSkipFreshUniverseSymbol(ctx, "000001", time.Now(), svc.universeMaintenanceFreshnessWindow(), quality, true)
	if err != nil {
		t.Fatalf("fresh skip check: %v", err)
	}
	if !skip {
		t.Fatalf("fresh ready instrument was not skipped")
	}

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-missing-bars",
		Symbol:         "000002",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "万科A",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert missing bars instrument: %v", err)
	}
	quality, err = svc.GetDailyBarsQuality(ctx, "000002", DailyBarAdjustedNone)
	if err != nil {
		t.Fatalf("get missing daily bar quality: %v", err)
	}
	skip, err = svc.shouldSkipFreshUniverseSymbol(ctx, "000002", time.Now(), svc.universeMaintenanceFreshnessWindow(), quality, true)
	if err != nil {
		t.Fatalf("missing bars skip check: %v", err)
	}
	if skip {
		t.Fatalf("instrument with missing daily bars was skipped")
	}
}

func TestScheduledUniverseUpdateSkipsWhenRecentJobCompleted(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 6, 30, 23, 5, 0, 0, loc)
	settings := svc.settings
	settings.AutoUpdateEnabled = true
	settings.UpdateIntervalSec = 3600
	settings.LastScheduledUpdate = now.AddDate(0, 0, -1)
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	svc.settings = settings

	if err := svc.store.CreateUpdateJob(ctx, StockV2UpdateJob{
		ID:             "job-recent-completed",
		TriggerType:    "manual",
		TriggerSource:  "test",
		Status:         "completed",
		TotalCount:     1,
		ProcessedCount: 1,
		SuccessCount:   1,
		StartAt:        now.Add(-10 * time.Minute),
		EndAt:          now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("create completed job: %v", err)
	}

	before := now.Add(-time.Second)
	svc.checkAndExecuteScheduledUpdateAt(ctx, now)

	jobs, err := svc.store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("job count = %d, want 1", len(jobs))
	}
	gotSettings, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if gotSettings.LastScheduledUpdate.Before(before.Add(-time.Second)) {
		t.Fatalf("last scheduled update = %v, want refreshed after %v", gotSettings.LastScheduledUpdate, before)
	}
}

func TestScheduledUniverseUpdateOnlyRunsInNightWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	settings := svc.settings
	settings.AutoUpdateEnabled = true
	settings.LastScheduledUpdate = time.Time{}
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	svc.settings = settings

	svc.checkAndExecuteScheduledUpdateAt(ctx, time.Date(2026, 6, 30, 14, 0, 0, 0, loc))
	jobs, err := svc.store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("job count = %d, want 0 outside night window", len(jobs))
	}
}

func readyDailyBarsForTest(symbol, market string, now time.Time) []StockV2DailyBar {
	bars := make([]StockV2DailyBar, 0, dailyBarsAgentTarget+10)
	start := now.AddDate(0, 0, -(dailyBarsAgentTarget + 9))
	for i := 0; i < dailyBarsAgentTarget+10; i++ {
		tradeDate := start.AddDate(0, 0, i).Format("2006-01-02")
		bars = append(bars, StockV2DailyBar{
			Symbol:    symbol,
			Market:    market,
			TradeDate: tradeDate,
			Open:      10,
			High:      11,
			Low:       9,
			Close:     10,
			Adjusted:  DailyBarAdjustedNone,
			Source:    "unit_test",
			FetchedAt: now,
			Quality:   DailyBarQualityOK,
		})
	}
	return bars
}
