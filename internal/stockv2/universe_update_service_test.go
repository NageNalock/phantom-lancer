package stockv2

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSinaUniverseShortPageRequiresExplicitEmptyCompletion(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("page") == "1" {
			return stringResponse(http.StatusOK, `[{"code":"600000"}]`), nil
		}
		return stringResponse(http.StatusServiceUnavailable, "temporary truncation"), nil
	})}
	svc := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	source := NewUniverseDataSource(svc, client)
	if symbols, err := source.fetchSinaUniverseSymbols(context.Background()); err == nil {
		t.Fatalf("partial universe was accepted as complete: %v", symbols)
	}
}

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

func TestNewServiceMarksInterruptedScheduledTasksFailed(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	startedAt := time.Now().Add(-time.Hour)
	if err := store.UpsertQuoteRefreshTaskState(ctx, QuoteRefreshTaskState{
		TaskType:     MonitorTaskLatestQuoteRefresh,
		Status:       MonitorRunStatusRunning,
		TriggerType:  MonitorTriggerScheduled,
		StartedAt:    startedAt,
		ScopeSummary: "scanned 4 symbols",
		ScannedCount: 4,
		UpdatedAt:    startedAt,
	}); err != nil {
		t.Fatalf("upsert quote refresh state: %v", err)
	}
	if err := store.CreateDailyBarJob(ctx, StockV2DailyBarJob{
		ID:            "daily-interrupted",
		JobType:       DailyBarJobTypeIncremental,
		Mode:          DailyBarJobModeUniverseIncremental,
		Status:        "running",
		TriggerType:   MonitorTriggerScheduled,
		TriggerSource: "test",
		StartAt:       startedAt,
		CreatedAt:     startedAt,
	}); err != nil {
		t.Fatalf("create daily bar job: %v", err)
	}
	if _, err := store.CreateMonitorRun(ctx, MonitorRun{
		ID:          "monitor-interrupted",
		TaskType:    MonitorTaskPortfolioRiskMonitor,
		Status:      MonitorRunStatusRunning,
		TriggerType: MonitorTriggerScheduled,
		StartedAt:   startedAt,
		CreatedAt:   startedAt,
	}); err != nil {
		t.Fatalf("create monitor run: %v", err)
	}
	if err := store.UpsertNewsSourceState(ctx, NewsSourceState{
		Source:              NewsSourceJin10,
		Enabled:             true,
		Status:              NewsSourceStatusRunning,
		PollIntervalSeconds: 600,
		BatchLimit:          50,
		ProcessLimit:        50,
		LastRunAt:           startedAt,
	}); err != nil {
		t.Fatalf("upsert news source state: %v", err)
	}

	svc := NewService(store, nil, nil)
	defer svc.StopBackground()

	quoteState, err := store.GetQuoteRefreshTaskState(ctx, MonitorTaskLatestQuoteRefresh)
	if err != nil {
		t.Fatalf("get quote refresh state: %v", err)
	}
	if quoteState == nil || quoteState.Status != MonitorRunStatusFailed {
		t.Fatalf("quote refresh status = %#v, want failed", quoteState)
	}
	if quoteState.FinishedAt.IsZero() || !strings.Contains(quoteState.ErrorMessage, "service restart") {
		t.Fatalf("quote refresh state = %#v, want finished restart failure", quoteState)
	}

	dailyJob, err := store.GetDailyBarJob(ctx, "daily-interrupted")
	if err != nil {
		t.Fatalf("get daily bar job: %v", err)
	}
	if dailyJob.Status != "failed" || dailyJob.EndAt.IsZero() || !strings.Contains(dailyJob.ErrorMessage, "service restart") {
		t.Fatalf("daily bar job = %#v, want failed restart state", dailyJob)
	}

	monitorRun, err := store.GetMonitorRun(ctx, "monitor-interrupted")
	if err != nil {
		t.Fatalf("get monitor run: %v", err)
	}
	if monitorRun.Status != MonitorRunStatusFailed || monitorRun.FinishedAt.IsZero() || !strings.Contains(monitorRun.ErrorMessage, "service restart") {
		t.Fatalf("monitor run = %#v, want failed restart state", monitorRun)
	}

	newsState, ok, err := store.GetNewsSourceState(ctx, NewsSourceJin10)
	if err != nil {
		t.Fatalf("get news source state: %v", err)
	}
	if !ok {
		t.Fatalf("news source state missing")
	}
	if newsState.Status != NewsSourceStatusIdle || newsState.LastRunStatus != NewsSourceStatusFailed || !strings.Contains(newsState.LastRunError, "service restart") {
		t.Fatalf("news source state = %#v, want idle with failed last run", newsState)
	}
}

func TestScheduledUniverseUpdateDoesNotTreatGenericCompletedJobAsDailyCoverage(t *testing.T) {
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

	svc.checkAndExecuteScheduledUpdateAt(ctx, now)

	jobs, err := svc.store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("job count = %d, want the generic job plus a full-universe slot job", len(jobs))
	}
	foundDailySlot := false
	for _, job := range jobs {
		if job.Scope == AssetMaintenanceScopeFullUniverse && job.SlotStart.Equal(time.Date(2026, 6, 30, 23, 0, 0, 0, loc)) {
			foundDailySlot = true
		}
	}
	if !foundDailySlot {
		t.Fatalf("jobs = %+v, want a full-universe job bound to the current daily slot", jobs)
	}
	gotSettings, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if !gotSettings.LastScheduledUpdate.Equal(settings.LastScheduledUpdate) {
		t.Fatalf("last scheduled update = %v, want unchanged until the slot is covered", gotSettings.LastScheduledUpdate)
	}
}

func TestScheduledUniverseUpdateDecisionStartsWithoutConfirmingSlot(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 6, 30, 23, 5, 0, 0, loc)

	decision, _ := decideScheduledUniverseUpdate(now.AddDate(0, 0, -1), StockV2UpdateJob{}, false, now)
	if decision != scheduledUniverseUpdateStart {
		t.Fatalf("decision = %s, want start", decision)
	}
}

func TestScheduledUniverseUpdateDecisionWaitsWhileJobRunning(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 6, 30, 23, 5, 0, 0, loc)
	latest := StockV2UpdateJob{Status: "running", CreatedAt: now.Add(-time.Minute)}

	decision, _ := decideScheduledUniverseUpdate(now.AddDate(0, 0, -1), latest, true, now)
	if decision != scheduledUniverseUpdateWait {
		t.Fatalf("decision = %s, want wait", decision)
	}
}

func TestScheduledUniverseUpdateDecisionRetriesFailedCurrentSlot(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	slotStart := time.Date(2026, 6, 30, 23, 0, 0, 0, loc)
	now := slotStart.Add(48 * time.Minute)
	latest := StockV2UpdateJob{
		Status:           "failed",
		Scope:            AssetMaintenanceScopeFullUniverse,
		UniverseVerified: true,
		CoverageStatus:   AssetMaintenanceCoverageIncomplete,
		SlotStart:        slotStart,
		CreatedAt:        slotStart.Add(30 * time.Second),
		EndAt:            now.Add(-time.Minute),
	}

	decision, _ := decideScheduledUniverseUpdate(slotStart.Add(30*time.Second), latest, true, now)
	if decision != scheduledUniverseUpdateStart {
		t.Fatalf("decision = %s, want start", decision)
	}
}

func TestUnverifiedUniverseWaitsForPersistedDiscoveryRetryWindow(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	slotStart := time.Date(2026, 7, 12, 23, 0, 0, 0, loc)
	lastAttempt := slotStart.Add(10 * time.Minute)
	job := StockV2UpdateJob{
		Scope: AssetMaintenanceScopeFullUniverse, SlotStart: slotStart,
		UniverseVerified: false, Status: "failed", CoverageStatus: AssetMaintenanceCoverageIncomplete,
	}
	if !shouldWaitForUniverseDiscoveryRetry(job, slotStart, lastAttempt, lastAttempt.Add(5*time.Hour)) {
		t.Fatal("unverified cached generation did not wait for its persisted retry window")
	}
	if shouldWaitForUniverseDiscoveryRetry(job, slotStart, lastAttempt, lastAttempt.Add(6*time.Hour)) {
		t.Fatal("universe discovery remained blocked after the retry window")
	}
}

func TestScheduledUniverseUpdateDecisionBacksOffReferenceCalendarFailure(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	slotStart := time.Date(2026, 6, 30, 23, 0, 0, 0, loc)
	failedAt := slotStart.Add(5 * time.Minute)
	latest := StockV2UpdateJob{
		Status:           "failed",
		Scope:            AssetMaintenanceScopeFullUniverse,
		UniverseVerified: true,
		CoverageStatus:   AssetMaintenanceCoverageIncomplete,
		SlotStart:        slotStart,
		EndAt:            failedAt,
		ErrorMessage:     "trading_calendar_unavailable: provider timeout",
	}
	decision, _ := decideScheduledUniverseUpdate(
		slotStart.Add(-24*time.Hour), latest, true, failedAt.Add(dailyBarReferenceCalendarProviderBackoff-time.Second),
	)
	if decision != scheduledUniverseUpdateWait {
		t.Fatalf("decision before calendar backoff expiry = %s, want wait", decision)
	}
	decision, _ = decideScheduledUniverseUpdate(
		slotStart.Add(-24*time.Hour), latest, true, failedAt.Add(dailyBarReferenceCalendarProviderBackoff),
	)
	if decision != scheduledUniverseUpdateStart {
		t.Fatalf("decision at calendar backoff expiry = %s, want start", decision)
	}
}

func TestScheduledUniverseUpdateDoesNotDuplicateCalendarFailureDuringBackoff(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	slotStart := time.Date(2026, 6, 30, 23, 0, 0, 0, loc)
	failedAt := slotStart.Add(3 * time.Minute)
	job := testAssetMaintenanceJob("job-calendar-backoff", AssetMaintenanceScopeFullUniverse, slotStart)
	job.Status = "failed"
	job.CoverageStatus = AssetMaintenanceCoverageIncomplete
	job.FreshnessStatus = AssetMaintenanceFreshnessRetrying
	job.EndAt = failedAt
	job.ErrorMessage = "trading_calendar_unavailable: provider timeout"
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	newerUnrelated := testAssetMaintenanceJob("job-newer-explicit", AssetMaintenanceScopeExplicit, time.Time{})
	newerUnrelated.Status = "completed"
	if err := store.CreateUpdateJob(ctx, newerUnrelated); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	svc.resourceGateReader = func() ResourceGateStatus {
		return ResourceGateStatus{State: ResourceGateNormal}
	}
	svc.checkAndExecuteScheduledUpdateAt(ctx, failedAt.Add(time.Minute))
	jobs, err := store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("calendar backoff created duplicate jobs: %+v", jobs)
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
