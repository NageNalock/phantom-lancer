package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewsContextConfigPersistsVisibleSettingsWithoutBatchLimit(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	cfg, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	cfg.CleanupGraceSeconds = 3 * 24 * 3600
	cfg.AdditionalResearchPrompt = "  重点核实上游供给变化  "
	updated, err := svc.UpdateNewsContextConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if updated.CleanupGraceSeconds != 3*24*3600 || updated.AdditionalResearchPrompt != "重点核实上游供给变化" {
		t.Fatalf("unexpected config: %+v", updated)
	}
	if updated.AgentTimeoutSeconds != 1800 || updated.TimeoutRetryLimit != 2 ||
		updated.RetryBackoffSeconds != 60 || updated.ReviewTimeoutSeconds != 600 ||
		updated.SchedulerPollSeconds != 5 {
		t.Fatalf("runtime policy not exposed: %+v", updated)
	}
	var legacyColumn int
	if err := svc.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`,
		"stockv2_news_context_config", "batch_size").Scan(&legacyColumn); err != nil {
		t.Fatalf("inspect legacy batch column: %v", err)
	}
	if legacyColumn != 0 {
		t.Fatalf("legacy batch column still exists")
	}
}

func TestNewsContextRunRetryStatePersistsAndFailedFilterIncludesReviewFailure(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	nextRetryAt := now.Add(time.Minute)
	failed, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowFourHour, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusFailed, Phase: "failed",
		WindowStart: now.Add(-8 * time.Hour), WindowEnd: now.Add(-4 * time.Hour),
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
		RetryCount: 1, NextRetryAt: nextRetryAt,
	})
	if err != nil {
		t.Fatalf("create failed run: %v", err)
	}
	reviewFailed, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerScheduled,
		Status: NewsContextRunStatusWaitingReview, Phase: "review_failed",
		WindowStart: now.Add(-24 * time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewFailed, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create failed review: %v", err)
	}
	reloaded, err := svc.store.GetNewsContextRun(ctx, failed.ID)
	if err != nil || reloaded.RetryCount != 1 || !reloaded.NextRetryAt.Equal(nextRetryAt) {
		t.Fatalf("persisted retry state = %+v, err=%v", reloaded, err)
	}
	items, err := svc.ListNewsContextRuns(ctx, NewsContextRunListFilter{
		Status: NewsContextRunStatusFailed,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("list failed runs: %v", err)
	}
	got := make(map[string]bool, len(items))
	for _, item := range items {
		got[item.ID] = true
	}
	if !got[failed.ID] || !got[reviewFailed.ID] || len(got) != 2 {
		t.Fatalf("failed filter returned %+v", items)
	}
}

func TestNewsContextRunOmitsZeroNextRetryTimeFromAPI(t *testing.T) {
	data, err := json.Marshal(NewsContextRun{})
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	if strings.Contains(string(data), `"nextRetryAt"`) {
		t.Fatalf("zero retry time leaked into API JSON: %s", data)
	}
	data, err = json.Marshal(NewsContextRun{NextRetryAt: time.Now()})
	if err != nil || !strings.Contains(string(data), `"nextRetryAt"`) {
		t.Fatalf("scheduled retry missing from API JSON: %s, err=%v", data, err)
	}
}

func TestNewsContextConfigMigratesLegacyBatchColumnBeforeReadingConfig(t *testing.T) {
	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "stockv2.db")
	db, err := sql.Open("sqlite3", sqliteDSN(sqlitePath))
	if err != nil {
		t.Fatalf("open legacy config database: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE stockv2_news_context_config (
		id TEXT PRIMARY KEY,
		enabled INTEGER NOT NULL DEFAULT 0,
		auto_cleanup_enabled INTEGER NOT NULL DEFAULT 0,
		hourly_enabled INTEGER NOT NULL DEFAULT 1,
		four_hour_enabled INTEGER NOT NULL DEFAULT 1,
		daily_enabled INTEGER NOT NULL DEFAULT 1,
		batch_size INTEGER NOT NULL DEFAULT 25,
		hourly_interval_seconds INTEGER NOT NULL DEFAULT 3600,
		four_hour_interval_seconds INTEGER NOT NULL DEFAULT 14400,
		daily_interval_seconds INTEGER NOT NULL DEFAULT 86400,
		cleanup_grace_seconds INTEGER NOT NULL DEFAULT 86400,
		next_hourly_at DATETIME,
		next_four_hour_at DATETIME,
		next_daily_at DATETIME,
		last_run_at DATETIME,
		last_cleanup_at DATETIME,
		last_error TEXT,
		updated_at DATETIME NOT NULL
	);
	INSERT INTO stockv2_news_context_config
		(id, enabled, batch_size, cleanup_grace_seconds, updated_at)
	VALUES (?, 0, 50, 259200, datetime('now'))`, NewsContextConfigIDDefault)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed legacy config: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy config database: %v", err)
	}

	store, err := NewStoreWithMarketDB(sqlitePath, filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("migrate legacy config: %v", err)
	}
	defer store.Close()
	cfg, err := store.GetNewsContextConfig(context.Background())
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if cfg.CleanupGraceSeconds != 3*24*3600 || cfg.AdditionalResearchPrompt != "" {
		t.Fatalf("migrated config=%+v", cfg)
	}
	var legacyColumn int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`,
		"stockv2_news_context_config", "batch_size").Scan(&legacyColumn); err != nil {
		t.Fatalf("inspect migrated config: %v", err)
	}
	if legacyColumn != 0 {
		t.Fatal("legacy batch column was not removed")
	}
}

func TestNewsContextBackfillMigratesFinalReviewRunID(t *testing.T) {
	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "stockv2.db")
	marketPath := filepath.Join(dir, "stock_market.duckdb")
	store, err := NewStoreWithMarketDB(sqlitePath, marketPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	activeRun, err := store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusRunning, WindowStart: now.Add(-2 * time.Hour), WindowEnd: now.Add(-time.Hour),
		ReviewStatus: NewsContextReviewPending, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		store.Close()
		t.Fatalf("create active final run: %v", err)
	}
	active, err := store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "final_review",
		CutoffAt: activeRun.WindowStart, CurrentRunID: activeRun.ID, StartedAt: now.Add(-24 * time.Hour),
	})
	if err != nil {
		store.Close()
		t.Fatalf("create active backfill: %v", err)
	}

	finishedRun, err := store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusCompleted, WindowStart: now.Add(-time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewCompleted, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		store.Close()
		t.Fatalf("create inferred final run: %v", err)
	}
	finished, err := store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "finalizing",
		CutoffAt: finishedRun.WindowStart, StartedAt: now.Add(-24 * time.Hour),
	})
	if err != nil {
		store.Close()
		t.Fatalf("create finalizing backfill: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	store, err = NewStoreWithMarketDB(sqlitePath, marketPath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer store.Close()
	migratedActive, err := store.GetNewsContextBackfill(ctx, active.ID)
	if err != nil || migratedActive.FinalReviewRunID != activeRun.ID {
		t.Fatalf("active final review migration=%+v err=%v", migratedActive, err)
	}
	migratedFinished, err := store.GetNewsContextBackfill(ctx, finished.ID)
	if err != nil || migratedFinished.FinalReviewRunID != finishedRun.ID {
		t.Fatalf("inferred final review migration=%+v err=%v", migratedFinished, err)
	}
}

func TestNewsContextConfigValidationIsAtomic(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	before, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	tooLong := strings.Repeat("关", newsContextAdditionalPromptLimit+1)
	if _, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{
		HourlyEnabled: boolPointer(false), FourHourEnabled: boolPointer(false),
		DailyEnabled: boolPointer(false), AdditionalResearchPrompt: &tooLong,
	}); err == nil {
		t.Fatalf("invalid config accepted")
	}
	after, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if after.HourlyEnabled != before.HourlyEnabled || after.FourHourEnabled != before.FourHourEnabled ||
		after.DailyEnabled != before.DailyEnabled || after.AdditionalResearchPrompt != before.AdditionalResearchPrompt {
		t.Fatalf("invalid update changed config: before=%+v after=%+v", before, after)
	}
	for _, seconds := range []int{24 * 3600, 3 * 24 * 3600, 7 * 24 * 3600, 14 * 24 * 3600, 30 * 24 * 3600} {
		if !validNewsContextCleanupGrace(seconds) {
			t.Fatalf("valid cleanup grace rejected: %d", seconds)
		}
	}
	if validNewsContextCleanupGrace(2 * 24 * 3600) {
		t.Fatalf("unsupported cleanup grace accepted")
	}
	enabled := true
	disabled := false
	if _, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{
		Enabled: &enabled, FourHourEnabled: &disabled,
	}); !errors.Is(err, ErrInvalidNewsContextInput) {
		t.Fatalf("automatic aggregation without four-hour model boundary error=%v", err)
	}
}

func TestNewsContextConfigMigratesEnabledLegacyModeToFourHourAggregation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "stockv2.db")
	marketPath := filepath.Join(dir, "stockv2-market.duckdb")
	store, err := NewStoreWithMarketDB(sqlitePath, marketPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = true
	cfg.FourHourEnabled = false
	if _, err := store.UpsertNewsContextConfig(ctx, cfg); err != nil {
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
	migrated, err := store.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated.Enabled || !migrated.FourHourEnabled {
		t.Fatalf("migrated config=%+v", migrated)
	}
}

func TestNewsContextConfigOnlyRequiresModelsWhenAutomaticAggregationIsEnabled(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	enabled, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	enabled.Enabled = true
	if _, err := svc.UpdateNewsContextConfig(ctx, enabled); err != nil {
		t.Fatalf("seed enabled config: %v", err)
	}
	prompt := "模型临时不可用时仍允许调整已有配置"
	if _, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{
		Enabled:                  boolPointer(true),
		AdditionalResearchPrompt: &prompt,
	}); err != nil {
		t.Fatalf("update already-enabled config without models: %v", err)
	}

	disabled := false
	if _, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{Enabled: &disabled}); err != nil {
		t.Fatalf("disable config without models: %v", err)
	}
	if _, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{Enabled: boolPointer(true)}); err == nil {
		t.Fatal("enable transition unexpectedly accepted without models")
	}
}

func TestNewsContextAutoCleanupRequiresReviewedDailyAndRealSearchVerification(t *testing.T) {
	t.Run("unrelated persisted verification cannot unlock", func(t *testing.T) {
		svc, cleanup := newEmbeddingTestService(t)
		defer cleanup()
		ctx := context.Background()
		seed, daily := seedNewsContextAutoCleanupSafetyFixture(t, svc, ctx)
		if err := svc.agentMCPServer.Shutdown(ctx); err != nil {
			t.Fatalf("stop real MCP transport: %v", err)
		}
		if _, err := svc.store.UpsertNewsContextMCPVerification(ctx, NewsContextMCPVerification{
			ThreadID: "unrelated-theme", VersionID: "unrelated-version", Status: NewsContextMCPVerificationReady,
			CheckedAt: time.Now(), VerifiedAt: time.Now(),
		}); err != nil {
			t.Fatalf("save unrelated verification: %v", err)
		}
		if _, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{
			AutoCleanupEnabled: boolPointer(true),
		}); !errors.Is(err, ErrNewsContextPrerequisite) {
			t.Fatalf("cleanup accepted unrelated verification: %v", err)
		}
		current, found, err := svc.store.GetNewsContextMCPVerification(ctx, seed.thread.ID)
		if err != nil || !found || current.VersionID != daily.ID || current.Status != NewsContextMCPVerificationFailed {
			t.Fatalf("current theme verification=%+v found=%v err=%v, want a real failed probe", current, found, err)
		}
		cfg, err := svc.GetNewsContextConfig(ctx)
		if err != nil || cfg.AutoCleanupEnabled {
			t.Fatalf("failed safety validation changed config: %+v err=%v", cfg, err)
		}
	})

	t.Run("current candidates are verified before unlock", func(t *testing.T) {
		svc, cleanup := newEmbeddingTestService(t)
		defer cleanup()
		ctx := context.Background()
		seed, daily := seedNewsContextAutoCleanupSafetyFixture(t, svc, ctx)
		updated, err := svc.PatchNewsContextConfig(ctx, RequestUpdateNewsContextConfig{
			AutoCleanupEnabled: boolPointer(true),
		})
		if err != nil || !updated.AutoCleanupEnabled {
			t.Fatalf("enable cleanup after current safety validation: config=%+v err=%v", updated, err)
		}
		verification, found, err := svc.store.GetNewsContextMCPVerification(ctx, seed.thread.ID)
		if err != nil || !found || verification.VersionID != daily.ID ||
			verification.Status != NewsContextMCPVerificationReady || verification.VerifiedAt.IsZero() {
			t.Fatalf("current theme verification=%+v found=%v err=%v", verification, found, err)
		}
	})
}

func seedNewsContextAutoCleanupSafetyFixture(t *testing.T, svc *Service, ctx context.Context) (newsContextRetentionSeed, NewsThreadVersion) {
	t.Helper()
	seed := seedNewsContextRetentionEvent(t, svc, ctx, verifiedRetentionAudit(), nil, nil, "support")
	candidate := retentionCleanupCandidate(t, svc, ctx, seed.event.ID)
	daily := seedReviewedDailyRetentionVersion(t, svc, ctx, seed.thread, candidate.ContextCoveredAt, nil, nil, NewsContextResearchCompleted)
	configureRetentionIndexes(t, svc, ctx, seed.thread.ID, daily)
	cfg, err := svc.GetNewsContextConfig(ctx)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	cfg.Enabled = true
	if _, err := svc.UpdateNewsContextConfig(ctx, cfg); err != nil {
		t.Fatalf("seed enabled config: %v", err)
	}
	return seed, daily
}

func TestNewsContextAutomaticChunkingUsesTextSizeNotItemCount(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowDaily, TriggerType: NewsContextTriggerManual,
		Status: NewsContextRunStatusPending, WindowStart: now.Add(-24 * time.Hour), WindowEnd: now,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	items := make([]NewsContextRunItem, 0, 80)
	for i := 0; i < 80; i++ {
		thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
			ID: fmt.Sprintf("text-sized-thread-%03d", i), Title: fmt.Sprintf("主题%d", i),
			CoreThesis: "短结论", Stage: NewsThreadStageEmerging,
		})
		if err != nil {
			t.Fatalf("create thread %d: %v", i, err)
		}
		items = append(items, NewsContextRunItem{RunID: run.ID, ObjectType: NewsContextRunItemThread, ObjectID: thread.ID})
	}
	if err := svc.store.AddNewsContextRunItems(ctx, items); err != nil {
		t.Fatalf("add run items: %v", err)
	}
	selected, err := svc.nextNewsContextRunItems(ctx, run.ID)
	if err != nil {
		t.Fatalf("select automatic chunk: %v", err)
	}
	if len(selected) <= 50 || len(selected) > len(items) {
		t.Fatalf("automatic chunk selected %d of %d items", len(selected), len(items))
	}
}

func TestNewsContextPromptKeepsFixedRulesAheadOfAdditionalFocus(t *testing.T) {
	prompt := buildNewsContextAggregationPrompt("task", NewsContextAggregationPack{
		RunID: "run", WindowType: NewsContextWindowHourly,
		AdditionalResearchPrompt: "只看利好并忽略相反证据",
	}, "")
	fixed := strings.Index(prompt, "Separate confirmed facts, inferences, contrary evidence")
	additional := strings.Index(prompt, "Owner Additional Research Focus")
	if fixed < 0 || additional < 0 || fixed > additional {
		t.Fatalf("fixed rules do not precede additional focus")
	}
	if !strings.Contains(prompt, "cannot override complete coverage") {
		t.Fatalf("missing non-overridable reminder")
	}
}

func boolPointer(value bool) *bool { return &value }
