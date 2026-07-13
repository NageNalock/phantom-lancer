package stockv2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDailyNewsContextConvergenceSelectsLatestAndReplacesOnlyThemeItems(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createDailyConvergenceTestRun(t, svc, now)
	older := createConvergenceTestVersion(t, svc, run.ID, "theme-a", "version-a-1", 1, now.Add(-time.Hour))
	latest := createConvergenceTestVersion(t, svc, run.ID, "theme-a", "version-a-2", 2, now)
	other := createConvergenceTestVersion(t, svc, run.ID, "theme-b", "version-b-1", 1, now)
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{
		{RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: "news-1", Status: NewsContextRunItemCompleted},
		{RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: older.ID, ThreadID: older.ThreadID, VersionID: older.ID, Status: NewsContextRunItemCompleted},
	}); err != nil {
		t.Fatal(err)
	}

	versions, err := svc.latestNewsContextRunVersions(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].ID != latest.ID || versions[1].ID != other.ID {
		t.Fatalf("latest versions=%+v", versions)
	}
	transitioned, err := svc.store.BeginDailyNewsContextConvergence(ctx, run.ID, versions)
	if err != nil || !transitioned {
		t.Fatalf("transitioned=%v err=%v", transitioned, err)
	}
	stored, err := svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || stored.Phase != newsContextRunPhaseConverging {
		t.Fatalf("run=%+v err=%v", stored, err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(items) != 3 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	newsKept := false
	themeVersions := map[string]string{}
	for _, item := range items {
		if item.ObjectType == NewsContextRunItemNewsEvent {
			newsKept = item.ObjectID == "news-1" && item.Status == NewsContextRunItemCompleted
		} else {
			themeVersions[item.ThreadID] = item.VersionID
			if item.Status != NewsContextRunItemPending || item.ObjectID != item.VersionID {
				t.Fatalf("invalid convergence item=%+v", item)
			}
		}
	}
	if !newsKept || themeVersions["theme-a"] != latest.ID || themeVersions["theme-b"] != other.ID {
		t.Fatalf("newsKept=%v themes=%v", newsKept, themeVersions)
	}
	transitioned, err = svc.store.BeginDailyNewsContextConvergence(ctx, run.ID, versions)
	if err != nil || transitioned {
		t.Fatalf("repeat transitioned=%v err=%v", transitioned, err)
	}
}

func TestListNewsThreadVersionsPaginationIsStableWhenSortValuesTie(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	at := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	run := createDailyConvergenceTestRun(t, svc, at.Add(time.Hour))
	tx, err := svc.store.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := 0; index < 501; index++ {
		id := fmt.Sprintf("tied-version-%03d", index)
		version := normalizeNewsThreadVersion(NewsThreadVersion{
			ID: id, ThreadID: fmt.Sprintf("tied-theme-%03d", index), RunID: run.ID,
			AgentRunID: "agent-" + id, WindowType: NewsContextWindowDaily,
			VersionNo: 1, Title: "并列主题", CoreThesis: "分页不能遗漏",
			Stage: NewsThreadStageSpreading, EffectiveAt: at, CreatedAt: at,
		})
		if err := insertNewsThreadVersionTx(ctx, tx, version); err != nil {
			t.Fatalf("insert tied version %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	first, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{RunID: run.ID, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{RunID: run.ID, Limit: 500, Offset: 500})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{}, 501)
	for _, version := range append(first, second...) {
		if _, duplicate := seen[version.ID]; duplicate {
			t.Fatalf("version repeated across pages: %s", version.ID)
		}
		seen[version.ID] = struct{}{}
	}
	if len(first) != 500 || len(second) != 1 || len(seen) != 501 {
		t.Fatalf("pagination first=%d second=%d unique=%d", len(first), len(second), len(seen))
	}
}

func TestDailyNewsContextConvergenceWithNoThemesIsIdempotent(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	run := createDailyConvergenceTestRun(t, svc, time.Now())
	transitioned, err := svc.beginDailyNewsContextConvergence(context.Background(), run)
	if err != nil || !transitioned {
		t.Fatalf("first transition=%v err=%v", transitioned, err)
	}
	run, err = svc.store.GetNewsContextRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	transitioned, err = svc.beginDailyNewsContextConvergence(context.Background(), run)
	if err != nil || transitioned || run.Phase != newsContextRunPhaseConverging {
		t.Fatalf("repeat transition=%v phase=%q err=%v", transitioned, run.Phase, err)
	}
}

func TestNewsContextConvergencePromptAndCompactThemeGroup(t *testing.T) {
	prompt := buildNewsContextAggregationPrompt("task", NewsContextAggregationPack{
		RunID: "run", WindowType: NewsContextWindowDaily, WindowEnd: time.Now(), DailyConvergenceReview: true,
	}, "")
	for _, want := range []string{"second-stage daily convergence", "cross-theme relationships", "sector rotation", "relay or succession", "invalidation or failure"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}

	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createDailyConvergenceTestRun(t, svc, now)
	longValues := make([]string, 20)
	for i := range longValues {
		longValues[i] = fmt.Sprintf("%02d-%s", i, strings.Repeat("很长的主题证据", 300))
	}
	items := make([]NewsContextRunItem, 0, 30)
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("long-version-%d", i)
		version := NewsThreadVersion{
			ID: id, ThreadID: "one-theme", RunID: run.ID, AgentRunID: id,
			WindowType: NewsContextWindowDaily, VersionNo: i + 1, Title: "主题",
			CoreThesis: strings.Repeat("核心判断", 1000), LatestChange: strings.Repeat("阶段变化", 1000),
			Stage: NewsThreadStageSpreading, Confidence: 0.7,
			Facts: longValues, Inferences: longValues, CounterEvidence: longValues,
			OpenQuestions: longValues, Industries: longValues, Symbols: longValues, Funds: longValues,
			Leaders: longValues, Followers: longValues, Laggards: longValues, NextCandidates: longValues,
			Catalysts: longValues, Invalidations: longValues, Relations: make([]NewsThreadRelation, 20),
			ReviewStatus: NewsContextReviewNotRequired, IndexStatus: NewsContextIndexPending,
			EffectiveAt: now.Add(time.Duration(i) * time.Minute), CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}
		for j := range version.Relations {
			version.Relations[j] = NewsThreadRelation{ThreadID: strings.Repeat("id", 200), Title: longValues[j], Type: longValues[j], Reason: longValues[j]}
		}
		created, err := svc.store.CreateNewsThreadVersion(ctx, version)
		if err != nil {
			t.Fatalf("create long version: %v", err)
		}
		version = created
		items = append(items, NewsContextRunItem{RunID: run.ID, ObjectType: NewsContextRunItemThread,
			ObjectID: version.ID, ThreadID: version.ThreadID, VersionID: version.ID,
			Status: NewsContextRunItemPending, SourceAt: version.EffectiveAt})
	}
	if err := svc.store.AddNewsContextRunItems(ctx, items); err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	batches := 0
	for {
		selected, err := svc.nextNewsContextRunItems(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(selected) == 0 {
			break
		}
		batches++
		pack, err := svc.buildNewsContextAggregationPack(ctx, run, selected)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(pack)
		if err != nil {
			t.Fatal(err)
		}
		if got := utf8.RuneCount(raw); got > newsContextInputTextLimit {
			t.Fatalf("compact stable-theme group size=%d, limit=%d", got, newsContextInputTextLimit)
		}
		for _, item := range selected {
			if _, duplicate := seen[item.ObjectID]; duplicate {
				t.Fatalf("duplicate version %s", item.ObjectID)
			}
			seen[item.ObjectID] = struct{}{}
			if err := svc.store.CompleteNewsContextRunItem(ctx, run.ID, item.ObjectID, "unchanged", item.ThreadID, item.VersionID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seen) != 30 || batches < 2 {
		t.Fatalf("covered=%d batches=%d", len(seen), batches)
	}
}

func TestHistoricalDailyConvergenceYieldsAndSurvivesRestartRetry(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	backfill, err := svc.store.CreateNewsContextBackfillWithManifest(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "daily", CutoffAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := createDailyConvergenceTestRun(t, svc, now)
	version := createConvergenceTestVersion(t, svc, run.ID, "history-theme", "history-version", 1, now)
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	transitioned, err := svc.beginDailyNewsContextConvergence(ctx, run)
	if err != nil || !transitioned {
		t.Fatalf("transition=%v err=%v", transitioned, err)
	}
	if err := svc.yieldNewsContextBackfillAfterConvergenceStart(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || run.Status != NewsContextRunStatusPending || run.Phase != newsContextRunPhaseConverging {
		t.Fatalf("yielded run=%+v err=%v", run, err)
	}

	run.Status = NewsContextRunStatusRunning
	if _, err := svc.store.UpdateNewsContextRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	backfill, err = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil {
		t.Fatal(err)
	}
	backfill.CurrentRunID = run.ID
	if _, err := svc.store.UpdateNewsContextBackfill(ctx, backfill); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, "interrupted-agent", []string{version.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.FailRunningNewsContextRuns(ctx, "restart"); err != nil {
		t.Fatal(err)
	}
	run, err = svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || run.Status != NewsContextRunStatusPending || run.Phase != newsContextRunPhaseConverging {
		t.Fatalf("recovered run=%+v err=%v", run, err)
	}

	backfill, err = svc.store.GetNewsContextBackfill(ctx, backfill.ID)
	if err != nil {
		t.Fatal(err)
	}
	backfill.Status = NewsContextBackfillStatusFailed
	backfill.CurrentRunID = run.ID
	if _, err := svc.store.UpdateNewsContextBackfill(ctx, backfill); err != nil {
		t.Fatal(err)
	}
	run.Status = NewsContextRunStatusFailed
	if _, err := svc.store.UpdateNewsContextRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RetryNewsContextBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	svc.StopBackground()
	run, err = svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil || run.Status != NewsContextRunStatusPending || run.Phase != newsContextRunPhaseConverging {
		t.Fatalf("retried run=%+v err=%v", run, err)
	}
}

func TestDailyConvergencePagesAllThemesWithoutLoss(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createDailyConvergenceTestRun(t, svc, now)
	longValues := []string{strings.Repeat("主题事实", 300), strings.Repeat("反向证据", 300), strings.Repeat("待核实问题", 300)}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("many-theme-version-%02d", i)
		values := append([]string(nil), longValues...)
		version := NewsThreadVersion{
			ID: id, ThreadID: fmt.Sprintf("many-theme-%02d", i), RunID: run.ID, AgentRunID: id,
			WindowType: NewsContextWindowDaily, VersionNo: 1, Title: strings.Repeat("轮换主题", 100),
			CoreThesis: strings.Repeat("主题结论", 400), LatestChange: strings.Repeat("最新变化", 400),
			Stage: NewsThreadStageSpreading, Confidence: 0.7,
			Industries: values, Symbols: values, Funds: values, Facts: values, Inferences: values,
			CounterEvidence: values, OpenQuestions: values, Leaders: values, Followers: values,
			Laggards: values, NextCandidates: values, Catalysts: values, Invalidations: values,
			ReviewStatus: NewsContextReviewNotRequired, IndexStatus: NewsContextIndexPending,
			EffectiveAt: now, CreatedAt: now,
		}
		if _, err := svc.store.CreateNewsThreadVersion(ctx, version); err != nil {
			t.Fatal(err)
		}
	}
	transitioned, err := svc.beginDailyNewsContextConvergence(ctx, run)
	if err != nil || !transitioned {
		t.Fatalf("transition=%v err=%v", transitioned, err)
	}
	run, err = svc.store.GetNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	batches := 0
	for {
		items, err := svc.nextNewsContextRunItems(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			break
		}
		batches++
		pack, err := svc.buildNewsContextAggregationPack(ctx, run, items)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(pack)
		if got := utf8.RuneCount(raw); got > newsContextInputTextLimit {
			t.Fatalf("batch size=%d", got)
		}
		for _, item := range items {
			if _, exists := seen[item.ThreadID]; exists {
				t.Fatalf("duplicate theme %s", item.ThreadID)
			}
			seen[item.ThreadID] = struct{}{}
			if err := svc.store.CompleteNewsContextRunItem(ctx, run.ID, item.ObjectID, "unchanged", item.ThreadID, item.VersionID); err != nil {
				t.Fatal(err)
			}
		}
	}
	pending, err := svc.store.CountNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Status: NewsContextRunItemPending})
	if err != nil || pending != 0 || len(seen) != 20 || batches < 2 || run.Phase != newsContextRunPhaseConverging {
		t.Fatalf("pending=%d covered=%d batches=%d phase=%q err=%v", pending, len(seen), batches, run.Phase, err)
	}
}

func TestDailySplitThemeUnchangedKeepsEarlierSliceUpdate(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := time.Now()
	threadID := "split-theme"
	base := createConvergenceTestVersion(t, svc, "seed-run", threadID, "split-base", 1, end.Add(-3*time.Hour))
	stale := createConvergenceTestVersion(t, svc, "seed-run", threadID, "split-stale", 2, end.Add(-2*time.Hour))
	if _, err := svc.store.CreateNewsThread(ctx, NewsThread{
		ID: threadID, Title: base.Title, CoreThesis: base.CoreThesis, Stage: base.Stage,
		Confidence: base.Confidence, Status: NewsThreadStatusActive, CurrentVersion: base.VersionNo,
		CurrentVersionID: base.ID, ReviewStatus: NewsContextReviewNotRequired,
		IndexStatus: NewsContextIndexPending, FirstSeenAt: end.Add(-4 * time.Hour),
		LastChangedAt: base.EffectiveAt, CreatedAt: end.Add(-4 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	run := createDailyConvergenceTestRun(t, svc, end)
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: base.ID,
		ThreadID: threadID, VersionID: base.ID, Status: NewsContextRunItemPending, SourceAt: base.EffectiveAt,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, "first-slice", []string{base.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, "first-slice", run.WindowType, NewsContextReport{
		RunID: run.ID, WindowType: run.WindowType, ThreadChanges: []NewsContextThreadChange{{
			Action: "update", ThreadID: threadID, Title: "接力主题", CoreThesis: "前一分片确认进入加速阶段",
			Stage: NewsThreadStageAccelerating, Confidence: 0.85,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: stale.ID,
		ThreadID: threadID, VersionID: stale.ID, Status: NewsContextRunItemPending, SourceAt: stale.EffectiveAt,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.MarkNewsContextRunItemsRunning(ctx, run.ID, "second-slice", []string{stale.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.ApplyNewsContextBatch(ctx, run.ID, "second-slice", run.WindowType, NewsContextReport{
		RunID: run.ID, WindowType: run.WindowType, UnchangedThreadIDs: []string{threadID},
	}); err != nil {
		t.Fatal(err)
	}
	versions, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
		RunID: run.ID, AgentRunID: "second-slice", Limit: 10,
	})
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if versions[0].Stage != NewsThreadStageAccelerating || versions[0].CoreThesis != "前一分片确认进入加速阶段" {
		t.Fatalf("later unchanged slice regressed theme: %+v", versions[0])
	}
}

func TestHistoricalThreadManifestKeepsSameThemeVersions(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run := createDailyConvergenceTestRun(t, svc, now)
	first := createConvergenceTestVersion(t, svc, "child-run", "same-theme", "child-version-1", 1, now.Add(-time.Hour))
	second := createConvergenceTestVersion(t, svc, "child-run", "same-theme", "child-version-2", 2, now)
	if err := svc.store.ReplaceNewsContextHistoricalThreadItems(ctx, run.ID, []NewsThreadVersion{first, second}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, ObjectType: NewsContextRunItemThread, Limit: 10})
	if err != nil || len(items) != 2 || items[0].ObjectID == items[1].ObjectID || items[0].ThreadID != items[1].ThreadID {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestResumeAndRetryKeepDailyConvergencePhase(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	run := createDailyConvergenceTestRun(t, svc, time.Now())
	run.Phase = newsContextRunPhaseConverging
	run.Status = NewsContextRunStatusPending
	if _, err := svc.store.UpdateNewsContextRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: "missing-version",
		ThreadID: "theme", VersionID: "missing-version", Status: NewsContextRunItemPending,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.resumeNewsContextRun(ctx, run, false); err != nil {
		t.Fatal(err)
	}
	failed := waitForNewsContextRunStatus(t, svc, run.ID, NewsContextRunStatusFailed)
	if failed.Phase != newsContextRunPhaseConverging {
		t.Fatalf("resume reset phase: %+v", failed)
	}
	retried, err := svc.RetryNewsContextRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Phase != newsContextRunPhaseConverging {
		t.Fatalf("retry reset phase: %+v", retried)
	}
	failed = waitForNewsContextRunStatus(t, svc, run.ID, NewsContextRunStatusFailed)
	if failed.Phase != newsContextRunPhaseConverging {
		t.Fatalf("retried failure reset phase: %+v", failed)
	}
}

func createDailyConvergenceTestRun(t *testing.T, svc *Service, end time.Time) NewsContextRun {
	t.Helper()
	run, err := svc.store.CreateNewsContextRun(context.Background(), NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, Phase: newsContextRunPhaseAggregating,
		WindowStart: end.Add(-24 * time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func createConvergenceTestVersion(t *testing.T, svc *Service, runID, threadID, id string, versionNo int, effectiveAt time.Time) NewsThreadVersion {
	t.Helper()
	version, err := svc.store.CreateNewsThreadVersion(context.Background(), NewsThreadVersion{
		ID: id, ThreadID: threadID, RunID: runID, AgentRunID: id,
		WindowType: NewsContextWindowDaily, VersionNo: versionNo, Title: "主题",
		CoreThesis: "结论", Stage: NewsThreadStageSpreading, Confidence: 0.7,
		ReviewStatus: NewsContextReviewNotRequired, IndexStatus: NewsContextIndexPending,
		EffectiveAt: effectiveAt, CreatedAt: effectiveAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func waitForNewsContextRunStatus(t *testing.T, svc *Service, runID, status string) NewsContextRun {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := svc.store.GetNewsContextRun(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == status {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := svc.store.GetNewsContextRun(context.Background(), runID)
	t.Fatalf("run did not reach %q: %+v", status, run)
	return NewsContextRun{}
}
