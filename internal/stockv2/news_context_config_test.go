package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	oldEventAt := time.Now().Add(-48 * time.Hour)
	oldCoveredAt := oldEventAt.Add(time.Hour)
	if _, err := svc.store.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events
		SET event_at=?, context_covered_at=? WHERE id=?`, oldEventAt, oldCoveredAt, seed.event.ID); err != nil {
		t.Fatalf("age cleanup safety fixture: %v", err)
	}
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
