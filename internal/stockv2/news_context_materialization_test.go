package stockv2

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaterializeNewsContextDailyVersionsIsLatestOnlyAndIdempotent(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)
	older := createConvergenceTestVersion(t, svc, "four-hour-old", "theme-a", "theme-a-old", 1, end.Add(-3*time.Hour))
	latest := createConvergenceTestVersion(t, svc, "four-hour-new", "theme-a", "theme-a-new", 2, end.Add(-time.Hour))
	other := createConvergenceTestVersion(t, svc, "four-hour-new", "theme-b", "theme-b-new", 1, end.Add(-time.Hour))
	run := createDailyMaterializationTestRun(t, svc, end)

	sources := sortedNewsContextVersions(map[string]NewsThreadVersion{
		older.ThreadID: latest,
		other.ThreadID: other,
	})
	versions, err := svc.store.MaterializeNewsContextDailyVersions(ctx, run.ID, end, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].ThreadID != "theme-a" || versions[0].CoreThesis != latest.CoreThesis ||
		versions[0].WindowType != NewsContextWindowDaily || versions[0].AgentRunID != "" || versions[0].MaterialChange {
		t.Fatalf("materialized versions=%+v", versions)
	}
	if err := svc.store.ReplaceNewsContextMaterializedThreadItems(ctx, run.ID, versions); err != nil {
		t.Fatal(err)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{
		RunID: run.ID, ObjectType: NewsContextRunItemThread, Limit: 10,
	})
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, item := range items {
		if item.Status != NewsContextRunItemCompleted || item.Disposition != "materialized" || item.VersionID == "" {
			t.Fatalf("materialized item=%+v", item)
		}
	}
	repeated, err := svc.store.MaterializeNewsContextDailyVersions(ctx, run.ID, end, sources)
	if err != nil || len(repeated) != len(versions) || repeated[0].ID != versions[0].ID || repeated[1].ID != versions[1].ID {
		t.Fatalf("repeat=%+v err=%v", repeated, err)
	}
	stored, err := svc.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestPrepareHourlyCheckpointDoesNotClaimOrCreateModelInput(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := time.Now().Truncate(time.Hour)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, Phase: "collecting",
		WindowStart: end.Add(-time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := svc.preparePendingNewsContextRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Phase != newsContextRunPhaseCheckpoint || prepared.InputCount != 0 || prepared.PendingCount != 0 {
		t.Fatalf("hourly checkpoint=%+v", prepared)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 1})
	if err != nil || len(items) != 0 {
		t.Fatalf("hourly items=%+v err=%v", items, err)
	}
}

func TestFourHourPreparationOwnsRawNewsAfterHourlyCheckpoint(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	end := newsContextBoundaryAtOrBefore(NewsContextWindowFourHour, time.Now())
	start := end.Add(-4 * time.Hour)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source: "test", Title: "四小时唯一模型输入", EventAt: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	hourly, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, Phase: "collecting",
		WindowStart: start, WindowEnd: start.Add(time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.preparePendingNewsContextRun(ctx, hourly); err != nil {
		t.Fatal(err)
	}
	var status, owner string
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),
		COALESCE(context_run_id,'') FROM stockv2_news_events WHERE id=?`, event.ID).Scan(&status, &owner); err != nil {
		t.Fatal(err)
	}
	if status != NewsEventContextPending || owner != "" {
		t.Fatalf("hourly checkpoint claimed news status=%s owner=%s", status, owner)
	}
	fourHour, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowFourHour, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, Phase: "collecting",
		WindowStart: start, WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := svc.preparePendingNewsContextRun(ctx, fourHour)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Phase != newsContextRunPhaseAggregating || prepared.InputCount != 1 || prepared.PendingCount != 1 {
		t.Fatalf("four-hour run=%+v", prepared)
	}
	items, err := svc.store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: fourHour.ID, Limit: 10})
	if err != nil || len(items) != 1 || items[0].ObjectID != event.ID || items[0].ObjectType != NewsContextRunItemNewsEvent {
		t.Fatalf("four-hour items=%+v err=%v", items, err)
	}
	if err := svc.store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),
		COALESCE(context_run_id,'') FROM stockv2_news_events WHERE id=?`, event.ID).Scan(&status, &owner); err != nil {
		t.Fatal(err)
	}
	if status != NewsEventContextClaimed || owner != fourHour.ID {
		t.Fatalf("four-hour claim status=%s owner=%s", status, owner)
	}
}

func TestLegacyHourlyCoverageMovesBackToPendingForFourHourAggregation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "stockv2.db")
	marketPath := filepath.Join(dir, "stockv2-market.duckdb")
	store, err := NewStoreWithMarketDB(sqlitePath, marketPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	end := time.Now().Truncate(time.Hour)
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "test", Title: "旧小时覆盖", EventAt: end.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowHourly, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusCompleted, Phase: "completed",
		WindowStart: end.Add(-time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddNewsContextRunItems(ctx, []NewsContextRunItem{{
		RunID: run.ID, ObjectType: NewsContextRunItemNewsEvent, ObjectID: event.ID,
		Status: NewsContextRunItemCompleted, Disposition: NewsEventContextNoise,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events SET
		context_status=?,context_run_id=?,context_covered_at=? WHERE id=?`,
		NewsEventContextNoise, run.ID, time.Now(), event.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStoreWithMarketDB(sqlitePath, marketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var status, owner string
	var coveredAt any
	if err := store.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),
		COALESCE(context_run_id,''),context_covered_at FROM stockv2_news_events WHERE id=?`, event.ID).
		Scan(&status, &owner, &coveredAt); err != nil {
		t.Fatal(err)
	}
	if status != NewsEventContextPending || owner != "" || coveredAt != nil {
		t.Fatalf("migrated status=%s owner=%s coveredAt=%v", status, owner, coveredAt)
	}
	items, err := store.ListNewsContextRunItems(ctx, NewsContextRunItemListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(items) != 0 {
		t.Fatalf("legacy hourly items=%+v err=%v", items, err)
	}
}

func TestManualDailyInputIncludesOnlyThemesChangedInWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	end := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)
	createConvergenceTestVersion(t, svc, "old", "unchanged-theme", "unchanged-old", 1, end.Add(-48*time.Hour))
	createConvergenceTestVersion(t, svc, "old", "changed-theme", "changed-old", 1, end.Add(-48*time.Hour))
	latest := createConvergenceTestVersion(t, svc, "four-hour", "changed-theme", "changed-new", 2, end.Add(-time.Hour))
	run := NewsContextRun{
		ID: "manual-daily", WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		WindowStart: end.Add(-24 * time.Hour), WindowEnd: end,
	}
	versions, err := svc.newsContextDailyInputVersions(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].ID != latest.ID {
		t.Fatalf("daily sources=%+v", versions)
	}
}

func TestHistoricalNewsContextPromptDisablesPublicBrowseButKeepsSemanticLookup(t *testing.T) {
	prompt := buildNewsContextAggregationPrompt("task", NewsContextAggregationPack{
		RunID: "run", WindowType: NewsContextWindowFourHour, WindowEnd: time.Now(),
		HistoricalReconstruction: true,
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	for _, want := range []string{
		"This is historical reconstruction", "Do not perform public search/browse",
		"semantic theme search/detail tools", "empty `search_audit` array",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "Actively use Codex CLI public search/browse") {
		t.Fatal("historical prompt enabled routine public browsing")
	}
}

func TestListNewsThreadVersionsPaginationIsStableWhenSortValuesTie(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	at := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	run := createDailyMaterializationTestRun(t, svc, at.Add(time.Hour))
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

func createDailyMaterializationTestRun(t *testing.T, svc *Service, end time.Time) NewsContextRun {
	t.Helper()
	run, err := svc.store.CreateNewsContextRun(context.Background(), NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, Phase: newsContextRunPhaseMaterialize,
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
		WindowType: NewsContextWindowFourHour, VersionNo: versionNo, Title: "主题 " + threadID,
		CoreThesis: "结论 " + id, Stage: NewsThreadStageSpreading, Confidence: 0.7,
		ReviewStatus: NewsContextReviewNotRequired, IndexStatus: NewsContextIndexPending,
		EffectiveAt: effectiveAt, CreatedAt: effectiveAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := svc.store.marketDB.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	thread := normalizeNewsThread(NewsThread{
		ID: threadID, Title: version.Title, CoreThesis: version.CoreThesis,
		Stage: version.Stage, Confidence: version.Confidence, Status: NewsThreadStatusActive,
		CurrentVersion: version.VersionNo, CurrentVersionID: version.ID,
		ReviewStatus: version.ReviewStatus, IndexStatus: version.IndexStatus,
		FirstSeenAt: effectiveAt, LastChangedAt: effectiveAt, CreatedAt: effectiveAt,
	})
	if err := upsertNewsThreadTx(context.Background(), tx, thread); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
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
