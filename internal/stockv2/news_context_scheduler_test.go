package stockv2

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPrepareNewsContextScheduleUsesNaturalBoundaries(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 7, 13, 10, 37, 42, 0, location)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	if !prepareNewsContextSchedule(&cfg, now) {
		t.Fatal("schedule was not initialized")
	}
	wantHourly := time.Date(2026, 7, 13, 10, 0, 0, 0, location)
	wantFourHour := time.Date(2026, 7, 13, 16, 0, 0, 0, location)
	wantDaily := time.Date(2026, 7, 15, 0, 0, 0, 0, location)
	if !cfg.NextHourlyAt.Equal(wantHourly) || !cfg.NextFourHourAt.Equal(wantFourHour) || !cfg.NextDailyAt.Equal(wantDaily) {
		t.Fatalf("next windows hourly=%v four=%v daily=%v", cfg.NextHourlyAt, cfg.NextFourHourAt, cfg.NextDailyAt)
	}

	dailyOnly := defaultNewsContextConfig()
	dailyOnly.HourlyEnabled = false
	dailyOnly.FourHourEnabled = false
	if !prepareNewsContextSchedule(&dailyOnly, now) {
		t.Fatal("daily-only schedule was not initialized")
	}
	wantDirectDaily := time.Date(2026, 7, 13, 0, 0, 0, 0, location)
	if !dailyOnly.NextDailyAt.Equal(wantDirectDaily) {
		t.Fatalf("direct daily next=%v want=%v", dailyOnly.NextDailyAt, wantDirectDaily)
	}

	legacy := defaultNewsContextConfig()
	legacy.NextHourlyAt = time.Date(2026, 7, 13, 10, 45, 0, 0, location)
	legacy.NextFourHourAt = time.Date(2026, 7, 13, 14, 45, 0, 0, location)
	legacy.NextDailyAt = time.Date(2026, 7, 13, 6, 45, 0, 0, location)
	if !prepareNewsContextSchedule(&legacy, now) {
		t.Fatal("legacy rolling schedule was not aligned")
	}
	if !legacy.NextHourlyAt.Equal(wantHourly) ||
		!legacy.NextFourHourAt.Equal(time.Date(2026, 7, 13, 12, 0, 0, 0, location)) ||
		!legacy.NextDailyAt.Equal(wantDirectDaily) {
		t.Fatalf("legacy schedule rounded past pending windows: %+v", legacy)
	}
}

func TestFastForwardNewsContextScheduleForBackfillUsesPostCutoffNaturalWindows(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	cutoff := time.Date(2026, 7, 13, 10, 0, 0, 0, location)
	stale := time.Date(2026, 3, 1, 0, 0, 0, 0, location)
	cfg := defaultNewsContextConfig()
	cfg.NextHourlyAt = stale
	cfg.NextFourHourAt = stale
	cfg.NextDailyAt = stale

	if !fastForwardNewsContextScheduleForBackfill(&cfg, cutoff) {
		t.Fatal("stale schedule was not fast-forwarded")
	}
	wantHourly := time.Date(2026, 7, 13, 11, 0, 0, 0, location)
	wantFourHour := time.Date(2026, 7, 13, 16, 0, 0, 0, location)
	wantDaily := time.Date(2026, 7, 15, 0, 0, 0, 0, location)
	if !cfg.NextHourlyAt.Equal(wantHourly) || !cfg.NextFourHourAt.Equal(wantFourHour) || !cfg.NextDailyAt.Equal(wantDaily) {
		t.Fatalf("post-cutoff schedule hourly=%v four=%v daily=%v", cfg.NextHourlyAt, cfg.NextFourHourAt, cfg.NextDailyAt)
	}
	for _, tt := range []struct {
		windowType string
		endAt      time.Time
	}{
		{windowType: NewsContextWindowHourly, endAt: cfg.NextHourlyAt},
		{windowType: NewsContextWindowFourHour, endAt: cfg.NextFourHourAt},
		{windowType: NewsContextWindowDaily, endAt: cfg.NextDailyAt},
	} {
		startAt, ok := newsContextScheduledWindow(tt.windowType, tt.endAt)
		if !ok || startAt.Before(cutoff) {
			t.Fatalf("%s first real-time window starts before cutoff: %v..%v", tt.windowType, startAt, tt.endAt)
		}
	}
}

func TestStartDueNewsContextRunFastForwardsStaleScheduleBeforeBackfillStep(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	location := time.Local
	cutoff := time.Date(2026, 7, 13, 10, 0, 0, 0, location)
	now := cutoff.Add(30 * time.Minute)
	stale := cutoff.AddDate(0, -3, 0)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.NextHourlyAt = stale
	cfg.NextFourHourAt = stale
	cfg.NextDailyAt = stale
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save stale config: %v", err)
	}
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create blocking backfill: %v", err)
	}

	if err := svc.startDueNewsContextRun(ctx, now); err != nil {
		t.Fatalf("start due run: %v", err)
	}
	runs, err := svc.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list real-time runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("stale schedule created pre-cutoff runs: %+v", runs)
	}
	stored, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get fast-forwarded config: %v", err)
	}
	if !stored.NextHourlyAt.Equal(cutoff.Add(time.Hour)) ||
		!stored.NextFourHourAt.Equal(time.Date(2026, 7, 13, 16, 0, 0, 0, location)) ||
		!stored.NextDailyAt.Equal(time.Date(2026, 7, 15, 0, 0, 0, 0, location)) {
		t.Fatalf("fast-forwarded schedule=%+v", stored)
	}

	if err := svc.runNewsContextBackfillStep(ctx); err != nil {
		t.Fatalf("historical step did not get its turn: %v", err)
	}
	advanced, err := svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil {
		t.Fatalf("get advanced backfill: %v", err)
	}
	if advanced.Phase != "four_hour" {
		t.Fatalf("historical step did not advance after real-time fast-forward: %+v", advanced)
	}

	if err := svc.startDueNewsContextRun(ctx, cutoff.Add(90*time.Minute)); err != nil {
		t.Fatalf("start first post-cutoff run: %v", err)
	}
	runs, err = svc.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{Limit: 20})
	if err != nil || len(runs) != 1 {
		t.Fatalf("post-cutoff runs=%+v err=%v", runs, err)
	}
	if !runs[0].WindowStart.Equal(cutoff) || !runs[0].WindowEnd.Equal(cutoff.Add(time.Hour)) {
		t.Fatalf("first hourly real-time window=%v..%v want=%v..%v", runs[0].WindowStart, runs[0].WindowEnd, cutoff, cutoff.Add(time.Hour))
	}
	waitForNewsContextRunTerminal(t, svc, runs[0].ID)
}

func TestStartDueNewsContextRunDirectCadenceClaimsPostCutoffPartialWindowOnce(t *testing.T) {
	cutoff := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	for _, tt := range []struct {
		name            string
		windowType      string
		fourHourEnabled bool
		now             time.Time
		wantStart       time.Time
		wantEnd         time.Time
	}{
		{
			name: "four-hour direct", windowType: NewsContextWindowFourHour, fourHourEnabled: true,
			now:       cutoff.Add(2*time.Hour + 30*time.Minute),
			wantStart: time.Date(2026, 7, 13, 8, 0, 0, 0, time.Local),
			wantEnd:   time.Date(2026, 7, 13, 12, 0, 0, 0, time.Local),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			oldEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
				Source: "test", Title: "历史消息", EventAt: cutoff.Add(-30 * time.Minute),
			})
			if err != nil {
				t.Fatalf("create historical news: %v", err)
			}
			partialEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
				Source: "test", Title: "截止点后的消息", EventAt: cutoff.Add(30 * time.Minute),
			})
			if err != nil {
				t.Fatalf("create partial-window news: %v", err)
			}
			cfg := defaultNewsContextConfig()
			cfg.Enabled = true
			cfg.HourlyEnabled = false
			cfg.FourHourEnabled = tt.fourHourEnabled
			cfg.DailyEnabled = false
			stale := cutoff.AddDate(0, -3, 0)
			cfg.NextFourHourAt = stale
			cfg.NextDailyAt = stale
			if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
				t.Fatalf("save direct cadence config: %v", err)
			}
			if _, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
				Status: NewsContextBackfillStatusRunning, Phase: "hourly", CutoffAt: cutoff,
			}); err != nil {
				t.Fatalf("create blocking backfill: %v", err)
			}

			if err := svc.startDueNewsContextRun(ctx, tt.now); err != nil {
				t.Fatalf("start direct cadence run: %v", err)
			}
			runs, err := svc.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{WindowType: tt.windowType, Limit: 10})
			if err != nil || len(runs) != 1 {
				t.Fatalf("direct cadence runs=%+v err=%v", runs, err)
			}
			if !runs[0].WindowStart.Equal(tt.wantStart) || !runs[0].WindowEnd.Equal(tt.wantEnd) {
				t.Fatalf("direct cadence window=%v..%v want=%v..%v", runs[0].WindowStart, runs[0].WindowEnd, tt.wantStart, tt.wantEnd)
			}
			waitForNewsContextRunTerminal(t, svc, runs[0].ID)
			items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
				RunID: runs[0].ID, ObjectType: NewsContextRunItemNewsEvent, Limit: 10,
			})
			if err != nil || len(items) != 1 || items[0].ObjectID != partialEvent.ID {
				t.Fatalf("partial-window news items=%+v err=%v", items, err)
			}
			again, err := svc.store.ClaimNewsContextEvents(ctx, "duplicate-owner", cutoff, tt.wantEnd)
			if err != nil || len(again) != 0 {
				t.Fatalf("partial-window news was claimable twice: ids=%v err=%v", again, err)
			}
			var oldStatus, oldRunID string
			if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),COALESCE(context_run_id,'')
				FROM stockv2_news_events WHERE id=?`, oldEvent.ID).Scan(&oldStatus, &oldRunID); err != nil {
				t.Fatalf("read historical ownership: %v", err)
			}
			if oldStatus != NewsEventContextPending || oldRunID != "" {
				t.Fatalf("historical news crossed cutoff status=%q run=%q", oldStatus, oldRunID)
			}
		})
	}
}

func TestCompleteNewsContextRunAdvancesScheduledBoundaryButNotManualPlan(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	location := time.Local
	next := time.Date(2026, 7, 13, 9, 0, 0, 0, location)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.FourHourEnabled = false
	cfg.DailyEnabled = false
	cfg.NextHourlyAt = next
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	scheduled := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowHourly,
		previousNewsContextBoundary(NewsContextWindowHourly, next), next, NewsContextTriggerScheduled, NewsContextRunStatusRunning)
	if err := svc.completeNewsContextRun(ctx, &scheduled, cfg); err != nil {
		t.Fatalf("complete scheduled run: %v", err)
	}
	stored, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get config after scheduled run: %v", err)
	}
	wantNext := nextNewsContextBoundary(NewsContextWindowHourly, next)
	if !stored.NextHourlyAt.Equal(wantNext) {
		t.Fatalf("scheduled next=%v want=%v", stored.NextHourlyAt, wantNext)
	}

	manualEnd := wantNext
	manual := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowHourly,
		previousNewsContextBoundary(NewsContextWindowHourly, manualEnd), manualEnd, NewsContextTriggerManual, NewsContextRunStatusRunning)
	if err := svc.completeNewsContextRun(ctx, &manual, stored); err != nil {
		t.Fatalf("complete manual run: %v", err)
	}
	stored, err = svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get config after manual run: %v", err)
	}
	if !stored.NextHourlyAt.Equal(wantNext) {
		t.Fatalf("manual run changed automatic plan to %v", stored.NextHourlyAt)
	}
}

func TestNewsContextParentWindowRequiresEveryEnabledChild(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	location := time.Local
	start := time.Date(2026, 7, 13, 8, 0, 0, 0, location)
	end := time.Date(2026, 7, 13, 12, 0, 0, 0, location)
	cfg := defaultNewsContextConfig()
	cfg.NextHourlyAt = end
	for cursor := start; cursor.Before(end.Add(-time.Hour)); cursor = nextNewsContextBoundary(NewsContextWindowHourly, cursor) {
		childEnd := nextNewsContextBoundary(NewsContextWindowHourly, cursor)
		createCompletedNewsContextTestWindow(t, svc, NewsContextWindowHourly, cursor, childEnd,
			NewsContextTriggerScheduled, NewsContextRunStatusCompleted)
	}
	ready, impossible, err := svc.newsContextParentWindowReadiness(ctx, cfg, NewsContextWindowFourHour, start, end)
	if err != nil || ready || impossible {
		t.Fatalf("incomplete parent readiness ready=%v impossible=%v err=%v", ready, impossible, err)
	}
	lastStart := end.Add(-time.Hour)
	createCompletedNewsContextTestWindow(t, svc, NewsContextWindowHourly, lastStart, end,
		NewsContextTriggerScheduled, NewsContextRunStatusWaitingReview)
	ready, impossible, err = svc.newsContextParentWindowReadiness(ctx, cfg, NewsContextWindowFourHour, start, end)
	if err != nil || !ready || impossible {
		t.Fatalf("complete parent readiness ready=%v impossible=%v err=%v", ready, impossible, err)
	}

	direct := cfg
	direct.HourlyEnabled = false
	ready, impossible, err = svc.newsContextParentWindowReadiness(ctx, direct, NewsContextWindowFourHour, start, end)
	if err != nil || !ready || impossible {
		t.Fatalf("direct parent readiness ready=%v impossible=%v err=%v", ready, impossible, err)
	}
	directDaily := cfg
	directDaily.FourHourEnabled = false
	dailyStart := time.Date(2026, 7, 13, 0, 0, 0, 0, location)
	dailyEnd := time.Date(2026, 7, 14, 0, 0, 0, 0, location)
	ready, impossible, err = svc.newsContextParentWindowReadiness(ctx, directDaily, NewsContextWindowDaily, dailyStart, dailyEnd)
	if err != nil || !ready || impossible {
		t.Fatalf("direct daily readiness ready=%v impossible=%v err=%v", ready, impossible, err)
	}
}

func TestStartDueNewsContextRunSkipsIncompleteParentsAndStartsHourlyWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 10, 30, 0, 0, time.Local)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.NextHourlyAt = time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	cfg.NextFourHourAt = time.Date(2026, 7, 13, 8, 0, 0, 0, time.Local)
	cfg.NextDailyAt = time.Date(2026, 7, 13, 0, 0, 0, 0, time.Local)
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := svc.startDueNewsContextRun(ctx, now); err != nil {
		t.Fatalf("start due run: %v", err)
	}
	runs, err := svc.store.ListNewsContextRuns(ctx, NewsContextRunListFilter{WindowType: NewsContextWindowHourly, Limit: 10})
	if err != nil || len(runs) != 1 {
		t.Fatalf("hourly runs=%+v err=%v", runs, err)
	}
	wantStart := time.Date(2026, 7, 13, 9, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	if !runs[0].WindowStart.Equal(wantStart) || !runs[0].WindowEnd.Equal(wantEnd) {
		t.Fatalf("hourly window=%v..%v", runs[0].WindowStart, runs[0].WindowEnd)
	}
	waitForNewsContextRunTerminal(t, svc, runs[0].ID)
}

func TestStartDueNewsContextRunRecoversPersistedPendingManifest(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := newsContextBoundaryAtOrBefore(NewsContextWindowHourly, time.Now())
	start := previousNewsContextBoundary(NewsContextWindowHourly, end)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.FourHourEnabled = false
	cfg.DailyEnabled = false
	cfg.NextHourlyAt = end
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	run := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowHourly, start, end,
		NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: "partial-crash-item",
		Status: NewsContextRunItemPending,
	}}); err != nil {
		t.Fatalf("seed partial manifest: %v", err)
	}
	if err := svc.startDueNewsContextRun(ctx, end.Add(time.Minute)); err != nil {
		t.Fatalf("resume due pending run: %v", err)
	}
	waitForNewsContextRunTerminal(t, svc, run.ID)
	stored, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || stored.Status != NewsContextRunStatusCompleted {
		t.Fatalf("recovered run=%+v err=%v", stored, err)
	}
	count, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID})
	if err != nil || count != 0 {
		t.Fatalf("recovered manifest count=%d err=%v", count, err)
	}
}

func TestSeedFourHourIgnoresLegacyHourlyThemeOutputs(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Date(2026, 7, 13, 8, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 13, 12, 0, 0, 0, time.Local)
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "旧小时主题", CoreThesis: "不再作为四小时输入", Stage: NewsThreadStageEmerging,
	})
	if err != nil {
		t.Fatalf("create legacy hourly theme: %v", err)
	}
	child := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowHourly, start, start.Add(time.Hour),
		NewsContextTriggerScheduled, NewsContextRunStatusCompleted)
	if _, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: child.ID, AgentRunID: "legacy-hour-agent",
		WindowType: NewsContextWindowHourly, VersionNo: 1, Title: thread.Title,
		CoreThesis: thread.CoreThesis, Stage: thread.Stage, EffectiveAt: child.WindowEnd,
	}); err != nil {
		t.Fatalf("create legacy hourly output: %v", err)
	}
	parent := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour, start, end,
		NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &parent); err != nil {
		t.Fatalf("seed four-hour run: %v", err)
	}
	count, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: parent.ID,
	})
	if err != nil || count != 0 {
		t.Fatalf("four-hour run consumed legacy hourly output count=%d err=%v", count, err)
	}
}

func TestSeedFourHourWithoutHourlyClaimsRawNewsOnly(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.HourlyEnabled = false
	cfg.DailyEnabled = false
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save direct four-hour config: %v", err)
	}
	end := newsContextBoundaryAtOrBefore(NewsContextWindowFourHour, time.Now())
	start := previousNewsContextBoundary(NewsContextWindowFourHour, end)
	oldThread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "既有主题", CoreThesis: "仅通过语义检索按需读取", Stage: NewsThreadStageEmerging,
		LastChangedAt: start.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create old theme: %v", err)
	}
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "窗口内消息", EventAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatalf("create window news: %v", err)
	}
	run := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour, start, end,
		NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &run); err != nil {
		t.Fatalf("seed direct four-hour run: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list direct four-hour items: %v", err)
	}
	foundEvent := false
	for _, item := range items {
		if item.ObjectID == oldThread.ID || item.ObjectType == NewsContextRunItemThread {
			t.Fatalf("four-hour manifest eagerly included existing theme: %+v", items)
		}
		if item.ObjectID == event.ID && item.ObjectType == NewsContextRunItemNewsEvent {
			foundEvent = true
		}
	}
	if !foundEvent || len(items) != 1 {
		t.Fatalf("direct four-hour manifest did not claim only raw news: items=%+v", items)
	}
}

func TestSeedDailyMaterializesLatestCompleteFourHourVersionPerTheme(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Date(2026, 7, 13, 0, 0, 0, 0, time.Local)
	end := start.Add(24 * time.Hour)
	if _, err := svc.store.UpsertNewsContextConfig(ctx, defaultNewsContextConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		ID: "daily-stable-theme", Title: "跨日主题", CoreThesis: "保留最新四小时变化",
		Stage: NewsThreadStageSpreading, FirstSeenAt: start,
	})
	if err != nil {
		t.Fatalf("create stable theme: %v", err)
	}
	latestVersionID := ""
	for index, childStart := 0, start; childStart.Before(end); index, childStart = index+1, childStart.Add(4*time.Hour) {
		childEnd := childStart.Add(4 * time.Hour)
		child := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour, childStart, childEnd,
			NewsContextTriggerScheduled, NewsContextRunStatusCompleted)
		version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
			ThreadID: thread.ID, RunID: child.ID, AgentRunID: "four-hour-agent-" + child.ID,
			WindowType: NewsContextWindowFourHour, VersionNo: index + 1,
			Title: "跨日主题", CoreThesis: "保留每个四小时变化", Stage: NewsThreadStageSpreading,
			LatestChange: fmt.Sprintf("四小时变化 %d", index), ReviewStatus: NewsContextReviewNotRequired, EffectiveAt: childEnd,
		})
		if err != nil {
			t.Fatalf("create four-hour output: %v", err)
		}
		latestVersionID = version.ID
		if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
			RunID: child.ID, ObjectType: NewsContextRunItemThread, ObjectID: "input-" + child.ID,
			Status: NewsContextRunItemCompleted, ThreadID: version.ThreadID, VersionID: version.ID,
			SourceAt: childEnd,
		}}); err != nil {
			t.Fatalf("record four-hour output: %v", err)
		}
	}
	parent := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowDaily, start, end,
		NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &parent); err != nil {
		t.Fatalf("seed daily parent: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: parent.ID, ObjectType: NewsContextRunItemThread, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list daily parent inputs: %v", err)
	}
	if len(items) != 1 || items[0].ThreadID != thread.ID || items[0].VersionID == latestVersionID {
		t.Fatalf("daily materialization items=%+v latest source=%s", items, latestVersionID)
	}
	materialized, err := svc.store.GetNewsThreadVersion(ctx, items[0].VersionID)
	if err != nil || materialized.RunID != parent.ID || materialized.AgentRunID != "" ||
		materialized.WindowType != NewsContextWindowDaily || materialized.LatestChange != "四小时变化 5" {
		t.Fatalf("daily materialized version=%+v err=%v", materialized, err)
	}
}

func TestSeedRealtimeNewsContextRunDoesNotClaimBackfillHistory(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	oldEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "历史消息", EventAt: cutoff.Add(-30 * time.Minute)})
	if err != nil {
		t.Fatalf("create old event: %v", err)
	}
	newEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "实时消息", EventAt: cutoff.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("create new event: %v", err)
	}
	if _, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusFailed, Phase: "failed", CutoffAt: cutoff,
	}); err != nil {
		t.Fatalf("create blocking backfill: %v", err)
	}
	cfg := defaultNewsContextConfig()
	cfg.HourlyEnabled = false
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save direct four-hour config: %v", err)
	}
	crossing := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
		cutoff.Add(-2*time.Hour), cutoff.Add(2*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &crossing); err != nil {
		t.Fatalf("seed crossing run: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: crossing.ID, Limit: 10})
	if err != nil || len(items) != 1 || items[0].ObjectID != newEvent.ID {
		t.Fatalf("crossing items=%+v err=%v", items, err)
	}
	var oldStatus, oldRunID string
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),COALESCE(context_run_id,'')
		FROM stockv2_news_events WHERE id=?`, oldEvent.ID).Scan(&oldStatus, &oldRunID); err != nil {
		t.Fatalf("read old event ownership: %v", err)
	}
	if oldStatus != NewsEventContextPending || oldRunID != "" {
		t.Fatalf("historical event was claimed status=%q run=%q", oldStatus, oldRunID)
	}

	if _, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "历史主题", CoreThesis: "仅用于确认空检查点不读取主题", Stage: NewsThreadStageEmerging,
	}); err != nil {
		t.Fatalf("create theme: %v", err)
	}
	beforeCutoff := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
		cutoff.Add(-6*time.Hour), cutoff.Add(-2*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &beforeCutoff); err != nil {
		t.Fatalf("seed pre-cutoff checkpoint: %v", err)
	}
	count, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: beforeCutoff.ID})
	if err != nil || count != 0 || beforeCutoff.InputCount != 0 {
		t.Fatalf("pre-cutoff checkpoint count=%d input=%d err=%v", count, beforeCutoff.InputCount, err)
	}
}

func TestSeedRealtimeNewsContextRunHonorsCompletedBackfillWatermark(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	oldEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "待下次补处理的旧消息", EventAt: cutoff.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create old event: %v", err)
	}
	atCutoff, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "截止点消息", EventAt: cutoff,
	})
	if err != nil {
		t.Fatalf("create cutoff event: %v", err)
	}
	newEvent, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "截止点后消息", EventAt: cutoff.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create new event: %v", err)
	}
	if _, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusCompleted, Phase: "completed", CutoffAt: cutoff,
		CompletedAt: cutoff.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create completed backfill: %v", err)
	}
	cfg := defaultNewsContextConfig()
	cfg.HourlyEnabled = false
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save direct four-hour config: %v", err)
	}
	run := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
		cutoff.Add(-time.Hour), cutoff.Add(time.Hour), NewsContextTriggerManual, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &run); err != nil {
		t.Fatalf("seed crossing run: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list crossing inputs: %v", err)
	}
	got := make(map[string]bool, len(items))
	for _, item := range items {
		got[item.ObjectID] = true
	}
	if got[oldEvent.ID] || !got[atCutoff.ID] || !got[newEvent.ID] || len(got) != 2 {
		t.Fatalf("completed cutoff ownership mismatch: items=%+v", items)
	}
	var oldStatus, oldRunID string
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),COALESCE(context_run_id,'')
		FROM stockv2_news_events WHERE id=?`, oldEvent.ID).Scan(&oldStatus, &oldRunID); err != nil {
		t.Fatalf("read old event ownership: %v", err)
	}
	if oldStatus != NewsEventContextPending || oldRunID != "" {
		t.Fatalf("pre-watermark event was claimed status=%q run=%q", oldStatus, oldRunID)
	}
}

func TestScheduledFourHourRunSweepsRealtimeOwnedPendingAndRetriesDeferralOnce(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	cutoff := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	if _, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusCompleted, Phase: "completed", CutoffAt: cutoff,
		CompletedAt: cutoff.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create completed backfill: %v", err)
	}
	preCutoff, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "仍归历史补处理所有", EventAt: cutoff.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create pre-cutoff event: %v", err)
	}
	latePending, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "前一窗口迟到消息", EventAt: cutoff.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create late pending event: %v", err)
	}
	firstOwner := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
		cutoff, cutoff.Add(time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusCompleted)
	deferred, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "首次暂缓消息", EventAt: cutoff.Add(20 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create deferred event: %v", err)
	}
	if err := svc.store.MarkNewsEventContext(ctx, deferred.ID, NewsEventContextDeferred,
		firstOwner.ID, "缺少一次关键核验", time.Time{}); err != nil {
		t.Fatalf("mark first deferral: %v", err)
	}

	run := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
		cutoff.Add(48*time.Hour), cutoff.Add(52*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &run); err != nil {
		t.Fatalf("seed catch-up run: %v", err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list catch-up inputs: %v", err)
	}
	got := make(map[string]bool, len(items))
	for _, item := range items {
		got[item.ObjectID] = true
	}
	if got[preCutoff.ID] || !got[latePending.ID] || !got[deferred.ID] || len(got) != 2 {
		t.Fatalf("catch-up ownership mismatch: %+v", items)
	}
	var retryCount int
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_defer_retry_count,0)
		FROM stockv2_news_events WHERE id=?`, deferred.ID).Scan(&retryCount); err != nil || retryCount != 1 {
		t.Fatalf("deferred retry count=%d err=%v", retryCount, err)
	}

	if err := svc.store.MarkNewsEventContext(ctx, latePending.ID, NewsEventContextNoise,
		run.ID, "", time.Now()); err != nil {
		t.Fatalf("complete late pending event: %v", err)
	}
	if err := svc.store.MarkNewsEventContext(ctx, deferred.ID, NewsEventContextDeferred,
		run.ID, "第二次仍缺少核验", time.Time{}); err != nil {
		t.Fatalf("mark second deferral: %v", err)
	}
	next := createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
		cutoff.Add(52*time.Hour), cutoff.Add(56*time.Hour), NewsContextTriggerScheduled, NewsContextRunStatusPending)
	if err := svc.seedNewsContextRunItems(ctx, &next); err != nil {
		t.Fatalf("seed post-retry run: %v", err)
	}
	count, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: next.ID, ObjectType: NewsContextRunItemNewsEvent,
	})
	if err != nil || count != 0 {
		t.Fatalf("second deferral was retried again count=%d err=%v", count, err)
	}
}

func TestStartDueNewsContextRunAutomaticallyRetriesFourHourAndDaily(t *testing.T) {
	for _, windowType := range []string{NewsContextWindowFourHour, NewsContextWindowDaily} {
		t.Run(windowType, func(t *testing.T) {
			svc, cleanup := newStrategyTestService(t)
			defer cleanup()
			ctx := context.Background()
			end := newsContextBoundaryAtOrBefore(windowType, time.Now())
			start := previousNewsContextBoundary(windowType, end)
			cfg := defaultNewsContextConfig()
			cfg.Enabled = true
			cfg.HourlyEnabled = false
			cfg.FourHourEnabled = true
			cfg.DailyEnabled = windowType == NewsContextWindowDaily
			setNewsContextNextAt(&cfg, windowType, end)
			if windowType == NewsContextWindowDaily {
				cfg.NextFourHourAt = nextNewsContextBoundary(
					NewsContextWindowFourHour,
					newsContextBoundaryAtOrBefore(NewsContextWindowFourHour, time.Now()),
				)
				for childStart := start; childStart.Before(end); childStart = nextNewsContextBoundary(NewsContextWindowFourHour, childStart) {
					createCompletedNewsContextTestWindow(t, svc, NewsContextWindowFourHour,
						childStart, nextNewsContextBoundary(NewsContextWindowFourHour, childStart),
						NewsContextTriggerScheduled, NewsContextRunStatusCompleted)
				}
			}
			if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
				t.Fatalf("save config: %v", err)
			}
			run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
				WindowType: windowType, TriggerType: NewsContextTriggerScheduled,
				Status: NewsContextRunStatusFailed, Phase: "failed",
				WindowStart: start, WindowEnd: end,
				ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
				ErrorMessage: "API model stopped without submitting a result",
				NextRetryAt:  time.Now().Add(-time.Second),
			})
			if err != nil {
				t.Fatalf("create failed run: %v", err)
			}
			if err := svc.startDueNewsContextRun(ctx, time.Now()); err != nil {
				t.Fatalf("start automatic retry: %v", err)
			}
			waitForNewsContextRunTerminal(t, svc, run.ID)
			reloaded, err := svc.store.GetNewsContextRun(ctx, run.ID)
			if err != nil || reloaded.Status != NewsContextRunStatusCompleted || reloaded.RetryCount != 1 ||
				!reloaded.NextRetryAt.IsZero() {
				t.Fatalf("automatic retry result = %+v, err=%v", reloaded, err)
			}
		})
	}
}

func TestNewsContextAutomaticRetryStopsAfterLimit(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := newsContextBoundaryAtOrBefore(NewsContextWindowFourHour, time.Now())
	start := previousNewsContextBoundary(NewsContextWindowFourHour, end)
	cfg := defaultNewsContextConfig()
	cfg.Enabled = true
	cfg.HourlyEnabled = false
	cfg.DailyEnabled = false
	cfg.NextFourHourAt = end
	if _, err := svc.store.UpsertNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowFourHour, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusFailed, Phase: "failed",
		WindowStart: start, WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
		ErrorMessage: "save news context result failed: invalid news context result: duplicate processed news id",
		RetryCount:   newsContextTimeoutRetryLimit,
	})
	if err != nil {
		t.Fatalf("create exhausted run: %v", err)
	}
	if err := svc.startDueNewsContextRun(ctx, time.Now()); err != nil {
		t.Fatalf("inspect exhausted run: %v", err)
	}
	reloaded, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err == nil {
		err = svc.decorateNewsContextRun(ctx, &reloaded)
	}
	if err != nil || reloaded.Status != NewsContextRunStatusFailed || !reloaded.AutoRetryExhausted ||
		!reloaded.NextRetryAt.IsZero() {
		t.Fatalf("exhausted run = %+v, err=%v", reloaded, err)
	}
}

func createCompletedNewsContextTestWindow(t *testing.T, svc *Service, windowType string, start, end time.Time, trigger, status string) NewsContextRun {
	t.Helper()
	run, err := svc.store.CreateNewsContextRun(context.Background(), NewsContextRun{
		WindowType: windowType, TriggerType: trigger, Status: status, WindowStart: start, WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create %s window %v..%v: %v", windowType, start, end, err)
	}
	return run
}

func waitForNewsContextRunTerminal(t *testing.T, svc *Service, runID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := svc.store.GetNewsContextRun(context.Background(), runID)
		if err == nil && run.Status != NewsContextRunStatusRunning && run.Status != NewsContextRunStatusPending {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("news context run %s did not finish", runID)
}
