package stockv2

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateSymbolResearchStrategy(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	got, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:            "浪潮信息观察",
		Kind:            StrategyKindSymbolStrategy,
		Scope:           StrategyScopeResearch,
		Source:          StrategySourceManual,
		Status:          StrategyStatusDraft,
		Symbol:          "000977",
		Market:          "SZ",
		Title:           "中期观察",
		Direction:       StrategyDirectionWatch,
		Thesis:          "算力主线延续，等待价格确认。",
		EntryConditions: []string{"breakout_review"},
		CreatedBy:       StrategySourceManual,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if got.Strategy.Kind != StrategyKindSymbolStrategy || got.Strategy.Scope != StrategyScopeResearch {
		t.Fatalf("strategy = %+v", got.Strategy)
	}
	if got.ActiveVersion == nil || got.ActiveVersion.VersionNo != 1 || got.ActiveVersion.Thesis == "" {
		t.Fatalf("active version = %+v", got.ActiveVersion)
	}
}

func TestCreateStrategyPersistsBiasAndPlaybook(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	playbook := map[string]any{
		"version": "v1",
		"rules": []any{
			map[string]any{"action": "build_position", "trigger": "回踩支撑位企稳", "target": "建仓到 5%"},
			map[string]any{"action": "exit_position", "trigger": "核心逻辑证伪", "target": "清仓"},
		},
	}
	created, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "剧本策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusDraft,
		Symbol:    "300750",
		Direction: StrategyBiasBullish,
		GenerationMeta: map[string]any{
			"playbook": playbook,
		},
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	got, err := svc.GetStrategy(ctx, created.Strategy.ID)
	if err != nil {
		t.Fatalf("get strategy: %v", err)
	}
	if got.ActiveVersion == nil || got.ActiveVersion.Direction != StrategyBiasBullish {
		t.Fatalf("active version = %+v", got.ActiveVersion)
	}
	if _, ok := got.ActiveVersion.GenerationMeta["playbook"].(map[string]any); !ok {
		t.Fatalf("generation meta = %+v", got.ActiveVersion.GenerationMeta)
	}
}

func TestCreatePortfolioBoundStrategyValidatesPortfolio(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	_, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:        "组合绑定单票策略",
		Kind:        StrategyKindSymbolStrategy,
		Scope:       StrategyScopePortfolioBound,
		Source:      StrategySourceManual,
		Status:      StrategyStatusDraft,
		Symbol:      "600000",
		PortfolioID: "missing",
		Direction:   StrategyDirectionWatch,
	})
	if !errors.Is(err, ErrPortfolioNotFound) {
		t.Fatalf("err = %v, want ErrPortfolioNotFound", err)
	}

	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-1")
	got, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:        "组合绑定单票策略",
		Kind:        StrategyKindSymbolStrategy,
		Scope:       StrategyScopePortfolioBound,
		Source:      StrategySourceManual,
		Status:      StrategyStatusDraft,
		Symbol:      "600000",
		PortfolioID: portfolio.ID,
		Direction:   StrategyDirectionWatch,
	})
	if err != nil {
		t.Fatalf("create bound strategy: %v", err)
	}
	if got.Strategy.PortfolioID != portfolio.ID {
		t.Fatalf("portfolio id = %q, want %q", got.Strategy.PortfolioID, portfolio.ID)
	}
}

func TestCreatePortfolioMonitorStrategy(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-monitor")
	got, err := svc.CreatePortfolioMonitorStrategy(ctx, portfolio.ID, RequestCreatePortfolioMonitorStrategy{})
	if err != nil {
		t.Fatalf("create portfolio monitor: %v", err)
	}
	if got.Strategy.Kind != StrategyKindPortfolioMonitor ||
		got.Strategy.Scope != StrategyScopePortfolioBound ||
		got.Strategy.Source != StrategySourceSystemTemplate ||
		got.Strategy.Status != StrategyStatusDraft ||
		got.Strategy.PortfolioID != portfolio.ID {
		t.Fatalf("strategy = %+v", got.Strategy)
	}
	if got.ActiveVersion == nil || got.ActiveVersion.VersionNo != 1 {
		t.Fatalf("active version = %+v", got.ActiveVersion)
	}
	if !containsString(got.ActiveVersion.EntryConditions, "daily_review") ||
		!containsString(got.ActiveVersion.EntryConditions, "stale_quote") ||
		!containsString(got.ActiveVersion.EntryConditions, "position_pct_limit") {
		t.Fatalf("entry conditions = %+v", got.ActiveVersion.EntryConditions)
	}
	if got.ActiveVersion.GenerationMeta["source"] != StrategySourceSystemTemplate {
		t.Fatalf("generation meta = %+v", got.ActiveVersion.GenerationMeta)
	}
}

func TestActiveStrategyUpdateCreatesNewVersion(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	created, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "平安银行策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusDraft,
		Symbol:    "000001",
		Direction: StrategyDirectionWatch,
		Thesis:    "初始判断",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := svc.ActivateStrategy(ctx, created.Strategy.ID); err != nil {
		t.Fatalf("activate strategy: %v", err)
	}

	newThesis := "更新后的正式判断"
	updated, err := svc.UpdateStrategy(ctx, created.Strategy.ID, RequestUpdateStrategy{Thesis: &newThesis})
	if err != nil {
		t.Fatalf("update strategy: %v", err)
	}
	if updated.ActiveVersion == nil || updated.ActiveVersion.VersionNo != 2 || updated.ActiveVersion.Thesis != newThesis {
		t.Fatalf("active version after update = %+v", updated.ActiveVersion)
	}
	versions, err := svc.ListStrategyVersions(ctx, created.Strategy.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].VersionNo != 1 || versions[1].VersionNo != 2 {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestArchivedStrategyCannotUpdateOrActivate(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	created, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "归档保护策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusDraft,
		Symbol:    "300001",
		Direction: StrategyDirectionWatch,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := svc.ArchiveStrategy(ctx, created.Strategy.ID); err != nil {
		t.Fatalf("archive strategy: %v", err)
	}
	name := "不应更新"
	if _, err := svc.UpdateStrategy(ctx, created.Strategy.ID, RequestUpdateStrategy{Name: &name}); !errors.Is(err, ErrStrategyArchived) {
		t.Fatalf("update archived err = %v, want ErrStrategyArchived", err)
	}
	if _, err := svc.ActivateStrategy(ctx, created.Strategy.ID); !errors.Is(err, ErrStrategyArchived) {
		t.Fatalf("activate archived err = %v, want ErrStrategyArchived", err)
	}
}

func TestListGetAndCountStrategies(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	created, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "列表测试策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusDraft,
		Symbol:    "002415",
		Direction: StrategyDirectionHold,
		Thesis:    "测试 active version 摘要",
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	got, err := svc.GetStrategy(ctx, created.Strategy.ID)
	if err != nil {
		t.Fatalf("get strategy: %v", err)
	}
	if got.ActiveVersion == nil || got.ActiveVersion.Direction != StrategyDirectionHold {
		t.Fatalf("get active version = %+v", got.ActiveVersion)
	}
	list, err := svc.ListStrategies(ctx, StrategyListFilter{Symbol: "002415", Limit: 10})
	if err != nil {
		t.Fatalf("list strategies: %v", err)
	}
	if len(list) != 1 || list[0].ActiveVersion == nil {
		t.Fatalf("list = %+v", list)
	}
	for _, item := range []RequestCreateStrategy{
		{
			Name:      "分页测试策略",
			Kind:      StrategyKindSymbolStrategy,
			Scope:     StrategyScopeResearch,
			Source:    StrategySourceManual,
			Status:    StrategyStatusDraft,
			Symbol:    "000001",
			Direction: StrategyDirectionWatch,
			Thesis:    "分页用例",
		},
		{
			Name:      "关键词目标策略",
			Kind:      StrategyKindSymbolStrategy,
			Scope:     StrategyScopeResearch,
			Source:    StrategySourceManual,
			Status:    StrategyStatusDraft,
			Symbol:    "300750",
			Direction: StrategyDirectionBuySignal,
			Thesis:    "关键词用例",
		},
	} {
		if _, err := svc.CreateStrategy(ctx, item); err != nil {
			t.Fatalf("create extra strategy: %v", err)
		}
	}
	page, err := svc.ListStrategies(ctx, StrategyListFilter{Kind: StrategyKindSymbolStrategy, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list paged strategies: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("paged list length = %d, want 1", len(page))
	}
	keywordList, err := svc.ListStrategies(ctx, StrategyListFilter{Keyword: "目标", Limit: 10})
	if err != nil {
		t.Fatalf("list keyword strategies: %v", err)
	}
	if len(keywordList) != 1 || keywordList[0].Strategy.Symbol != "300750" {
		t.Fatalf("keyword list = %+v", keywordList)
	}
	keywordCount, err := svc.CountStrategies(ctx, StrategyListFilter{Keyword: "目标"})
	if err != nil {
		t.Fatalf("count keyword strategies: %v", err)
	}
	if keywordCount != 1 {
		t.Fatalf("keyword count = %d, want 1", keywordCount)
	}
	count, err := svc.CountStrategies(ctx, StrategyListFilter{Kind: StrategyKindSymbolStrategy, Status: StrategyStatusDraft})
	if err != nil {
		t.Fatalf("count strategies: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestArchiveInactiveStrategies(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	oldDraft := createStrategyForArchiveTest(t, svc, "过期草稿", StrategyStatusDraft)
	oldPaused := createStrategyForArchiveTest(t, svc, "过期暂停", StrategyStatusPaused)
	oldActive := createStrategyForArchiveTest(t, svc, "生效策略", StrategyStatusActive)
	recentDraft := createStrategyForArchiveTest(t, svc, "近期草稿", StrategyStatusDraft)
	boundaryDraft := createStrategyForArchiveTest(t, svc, "边界草稿", StrategyStatusDraft)

	for id, updatedAt := range map[string]time.Time{
		oldDraft.Strategy.ID:      now.Add(-strategyAutoArchiveAfter - time.Second),
		oldPaused.Strategy.ID:     now.Add(-strategyAutoArchiveAfter - time.Hour),
		oldActive.Strategy.ID:     now.Add(-strategyAutoArchiveAfter - time.Hour),
		recentDraft.Strategy.ID:   now.Add(-strategyAutoArchiveAfter + time.Second),
		boundaryDraft.Strategy.ID: now.Add(-strategyAutoArchiveAfter),
	} {
		if _, err := svc.store.db.ExecContext(ctx, `UPDATE stockv2_strategies SET updated_at = ? WHERE id = ?`, updatedAt, id); err != nil {
			t.Fatalf("set strategy updated_at: %v", err)
		}
	}

	count, err := svc.archiveInactiveStrategies(ctx, now)
	if err != nil {
		t.Fatalf("archive inactive strategies: %v", err)
	}
	if count != 2 {
		t.Fatalf("archived count = %d, want 2", count)
	}

	for _, tc := range []struct {
		id         string
		wantStatus string
	}{
		{oldDraft.Strategy.ID, StrategyStatusArchived},
		{oldPaused.Strategy.ID, StrategyStatusArchived},
		{oldActive.Strategy.ID, StrategyStatusActive},
		{recentDraft.Strategy.ID, StrategyStatusDraft},
		{boundaryDraft.Strategy.ID, StrategyStatusDraft},
	} {
		got, err := svc.GetStrategy(ctx, tc.id)
		if err != nil {
			t.Fatalf("get strategy %s: %v", tc.id, err)
		}
		if got.Strategy.Status != tc.wantStatus {
			t.Errorf("strategy %s status = %q, want %q", tc.id, got.Strategy.Status, tc.wantStatus)
		}
		if tc.wantStatus == StrategyStatusArchived && !got.Strategy.ArchivedAt.Equal(now) {
			t.Errorf("strategy %s archivedAt = %v, want %v", tc.id, got.Strategy.ArchivedAt, now)
		}
	}

	visible, err := svc.ListStrategies(ctx, StrategyListFilter{ExcludeArchived: true, Limit: 20})
	if err != nil {
		t.Fatalf("list current strategies: %v", err)
	}
	if len(visible) != 3 {
		t.Fatalf("current strategy count = %d, want 3", len(visible))
	}
	archived, err := svc.ListStrategies(ctx, StrategyListFilter{Status: StrategyStatusArchived, ExcludeArchived: true, Limit: 20})
	if err != nil {
		t.Fatalf("list archived strategies: %v", err)
	}
	if len(archived) != 2 {
		t.Fatalf("archived strategy count = %d, want 2", len(archived))
	}
}

func createStrategyForArchiveTest(t *testing.T, svc *Service, name, status string) StrategyWithVersion {
	t.Helper()
	created, err := svc.CreateStrategy(context.Background(), RequestCreateStrategy{
		Name:      name,
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    status,
		Symbol:    "600000",
		Direction: StrategyDirectionWatch,
	})
	if err != nil {
		t.Fatalf("create strategy %q: %v", name, err)
	}
	return created
}

func newStrategyTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, nil)
	return svc, func() {
		svc.StopBackground()
		_ = store.Close()
	}
}

func createStrategyTestPortfolio(t *testing.T, store *Store, id string) StockV2Portfolio {
	t.Helper()
	portfolio := StockV2Portfolio{
		ID:        id,
		Name:      "测试组合",
		Cash:      10000,
		RiskLevel: "medium",
	}
	if err := store.CreatePortfolio(context.Background(), portfolio); err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	return portfolio
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
