package stockv2

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateWatchAndListPagination(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:            "浪潮信息价格盯盘",
		Source:          WatchSourceManual,
		Symbol:          "000977",
		Market:          "SZ",
		TriggerPolicy:   WatchTriggerPolicyAny,
		TriggerConfig:   map[string]any{"priceAbove": 60.0},
		ScheduleKind:    WatchScheduleManual,
		CooldownSeconds: 600,
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	if watch.Status != WatchStatusActive || watch.TriggerConfig["priceAbove"] != 60.0 {
		t.Fatalf("watch = %+v", watch)
	}

	for _, symbol := range []string{"000001", "600000"} {
		if _, err := svc.CreateWatch(ctx, RequestCreateWatch{
			Name:   "分页盯盘 " + symbol,
			Source: WatchSourceManual,
			Symbol: symbol,
		}); err != nil {
			t.Fatalf("create extra watch: %v", err)
		}
	}

	page, err := svc.ListWatches(ctx, WatchListFilter{Status: WatchStatusActive, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list watches: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("paged watches length = %d, want 1", len(page))
	}
	count, err := svc.CountWatches(ctx, WatchListFilter{Status: WatchStatusActive})
	if err != nil {
		t.Fatalf("count watches: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestWatchStatusTransitions(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:   "状态盯盘",
		Source: WatchSourceManual,
		Symbol: "300750",
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}

	paused, err := svc.PauseWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("pause watch: %v", err)
	}
	if paused.Status != WatchStatusPaused {
		t.Fatalf("paused status = %q", paused.Status)
	}
	active, err := svc.ActivateWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("activate watch: %v", err)
	}
	if active.Status != WatchStatusActive {
		t.Fatalf("active status = %q", active.Status)
	}
	archived, err := svc.ArchiveWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("archive watch: %v", err)
	}
	if archived.Status != WatchStatusArchived || archived.ArchivedAt.IsZero() {
		t.Fatalf("archived watch = %+v", archived)
	}
	name := "不应更新"
	if _, err := svc.UpdateWatch(ctx, watch.ID, RequestUpdateWatch{Name: &name}); !errors.Is(err, ErrWatchArchived) {
		t.Fatalf("update archived err = %v, want ErrWatchArchived", err)
	}
	if _, err := svc.ActivateWatch(ctx, watch.ID); !errors.Is(err, ErrWatchArchived) {
		t.Fatalf("activate archived err = %v, want ErrWatchArchived", err)
	}
}

func TestCreateWatchFromStrategyValidatesRefs(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	if _, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:       "缺少策略引用",
		Source:     WatchSourceStrategy,
		Symbol:     "000977",
		StrategyID: "missing",
	}); !errors.Is(err, ErrStrategyNotFound) {
		t.Fatalf("err = %v, want ErrStrategyNotFound", err)
	}

	strategy, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "策略来源盯盘",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusActive,
		Symbol:    "000977",
		Direction: StrategyDirectionWatch,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:       "策略盯盘",
		Source:     WatchSourceStrategy,
		StrategyID: strategy.Strategy.ID,
		Symbol:     "000977",
	})
	if err != nil {
		t.Fatalf("create watch from strategy: %v", err)
	}
	if watch.StrategyVersionID != strategy.Strategy.ActiveVersionID {
		t.Fatalf("strategy version = %q, want %q", watch.StrategyVersionID, strategy.Strategy.ActiveVersionID)
	}
}

func TestCreateAlertAndDedupe(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:            "提醒盯盘",
		Source:          WatchSourceManual,
		Symbol:          "600000",
		CooldownSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	triggeredAt := time.Now()
	alert, err := svc.CreateAlert(ctx, RequestCreateAlert{
		WatchID:     watch.ID,
		Level:       AlertLevelWarning,
		Title:       "价格突破",
		Summary:     "价格突破观察线",
		DedupeKey:   "price_above:600000:10",
		Evidence:    map[string]any{"lastPrice": 10.2},
		TriggeredAt: triggeredAt,
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	if alert.Status != AlertStatusOpen || alert.Level != AlertLevelWarning || alert.Evidence["lastPrice"] != 10.2 {
		t.Fatalf("alert = %+v", alert)
	}

	duplicate, err := svc.CreateAlert(ctx, RequestCreateAlert{
		WatchID:     watch.ID,
		Level:       AlertLevelCritical,
		Title:       "重复价格突破",
		DedupeKey:   "price_above:600000:10",
		TriggeredAt: triggeredAt.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create duplicate alert: %v", err)
	}
	if duplicate.ID != alert.ID {
		t.Fatalf("duplicate id = %q, want existing %q", duplicate.ID, alert.ID)
	}

	list, err := svc.ListAlerts(ctx, AlertListFilter{Status: AlertStatusOpen, WatchID: watch.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("open alert length = %d, want 1", len(list))
	}
	count, err := svc.CountAlerts(ctx, AlertListFilter{WatchID: watch.ID})
	if err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if count != 1 {
		t.Fatalf("alert count = %d, want 1", count)
	}

	updatedWatch, err := svc.GetWatch(ctx, watch.ID)
	if err != nil {
		t.Fatalf("get watch: %v", err)
	}
	if updatedWatch.LastTriggeredAt.IsZero() {
		t.Fatalf("last triggered was not updated")
	}
}

func TestAlertStatusTransitions(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	watch, err := svc.CreateWatch(ctx, RequestCreateWatch{
		Name:   "提醒状态盯盘",
		Source: WatchSourceManual,
		Symbol: "000001",
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	alert, err := svc.CreateAlert(ctx, RequestCreateAlert{
		WatchID: watch.ID,
		Level:   AlertLevelInfo,
		Title:   "数据过期",
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	ack, err := svc.AcknowledgeAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("ack alert: %v", err)
	}
	if ack.Status != AlertStatusAcknowledged || ack.AcknowledgedAt.IsZero() {
		t.Fatalf("ack alert = %+v", ack)
	}
	resolved, err := svc.ResolveAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("resolve alert: %v", err)
	}
	if resolved.Status != AlertStatusResolved || resolved.ResolvedAt.IsZero() {
		t.Fatalf("resolved alert = %+v", resolved)
	}

	ignoredAlert, err := svc.CreateAlert(ctx, RequestCreateAlert{
		WatchID:   watch.ID,
		Level:     AlertLevelWarning,
		Title:     "仓位过高",
		DedupeKey: "position_pct:000001",
	})
	if err != nil {
		t.Fatalf("create ignored alert: %v", err)
	}
	ignored, err := svc.IgnoreAlert(ctx, ignoredAlert.ID)
	if err != nil {
		t.Fatalf("ignore alert: %v", err)
	}
	if ignored.Status != AlertStatusIgnored || ignored.AcknowledgedAt.IsZero() {
		t.Fatalf("ignored alert = %+v", ignored)
	}
}

func TestCreateStrategyWatchFromPriceTriggersAndDuplicate(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	strategy, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "价格触发策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusDraft,
		Symbol:    "000977",
		Market:    "SZ",
		Direction: StrategyDirectionWatch,
		GenerationMeta: map[string]any{
			"priceTriggers": map[string]any{
				"triggerPriceAbove": 60.0,
				"stopLoss":          50.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	watch, err := svc.CreateStrategyWatch(ctx, strategy.Strategy.ID)
	if err != nil {
		t.Fatalf("create strategy watch: %v", err)
	}
	if watch.Source != WatchSourceStrategy ||
		watch.StrategyID != strategy.Strategy.ID ||
		watch.StrategyVersionID != strategy.Strategy.ActiveVersionID ||
		watch.Symbol != "000977" {
		t.Fatalf("watch = %+v", watch)
	}
	if watch.TriggerConfig["template"] != "strategy_price_triggers_v1" {
		t.Fatalf("trigger config = %+v", watch.TriggerConfig)
	}
	if !watchTestHasRule(watch.TriggerConfig, "trigger_price_above", WatchRulePriceAbove) ||
		!watchTestHasRule(watch.TriggerConfig, "stop_loss", WatchRulePriceBelow) {
		t.Fatalf("rules = %+v", watch.TriggerConfig["rules"])
	}

	again, err := svc.CreateStrategyWatch(ctx, strategy.Strategy.ID)
	if err != nil {
		t.Fatalf("create duplicate strategy watch: %v", err)
	}
	if again.ID != watch.ID {
		t.Fatalf("duplicate watch id = %q, want %q", again.ID, watch.ID)
	}
}

func TestCreateStrategyWatchMissingActiveVersion(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	created, err := svc.CreateStrategy(ctx, RequestCreateStrategy{
		Name:      "缺版本策略",
		Kind:      StrategyKindSymbolStrategy,
		Scope:     StrategyScopeResearch,
		Source:    StrategySourceManual,
		Status:    StrategyStatusDraft,
		Symbol:    "000001",
		Direction: StrategyDirectionWatch,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	strategy := created.Strategy
	strategy.ActiveVersionID = ""
	if _, err := svc.store.UpdateStrategyWithVersion(ctx, strategy, nil); err != nil {
		t.Fatalf("clear active version: %v", err)
	}
	if _, err := svc.CreateStrategyWatch(ctx, strategy.ID); !errors.Is(err, ErrStrategyVersionNotFound) {
		t.Fatalf("err = %v, want ErrStrategyVersionNotFound", err)
	}
}

func TestCreatePortfolioMonitorWatchAndDuplicate(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-watch")
	if err := svc.store.CreateHolding(ctx, StockV2Holding{
		ID:                "holding-watch",
		PortfolioID:       portfolio.ID,
		Symbol:            "000001",
		Market:            "SZ",
		Name:              "平安银行",
		Quantity:          100,
		AvailableQuantity: 100,
		CostPrice:         10,
	}); err != nil {
		t.Fatalf("create holding: %v", err)
	}

	watch, err := svc.CreatePortfolioMonitorWatch(ctx, portfolio.ID)
	if err != nil {
		t.Fatalf("create portfolio monitor watch: %v", err)
	}
	if watch.Source != WatchSourcePortfolioMonitor || watch.PortfolioID != portfolio.ID || watch.StrategyID != "" {
		t.Fatalf("watch = %+v", watch)
	}
	if watch.TriggerConfig["template"] != "portfolio_monitor_watch_v1" ||
		!watchTestHasRule(watch.TriggerConfig, "quote_stale_000001", WatchRuleQuoteStale) ||
		!watchTestHasRule(watch.TriggerConfig, "weight_above_000001", WatchRulePortfolioSymbolWeightOver) {
		t.Fatalf("trigger config = %+v", watch.TriggerConfig)
	}

	again, err := svc.CreatePortfolioMonitorWatch(ctx, portfolio.ID)
	if err != nil {
		t.Fatalf("create duplicate portfolio watch: %v", err)
	}
	if again.ID != watch.ID {
		t.Fatalf("duplicate watch id = %q, want %q", again.ID, watch.ID)
	}
}

func TestCreatePortfolioMonitorStrategyWatchWithoutSymbol(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newWatchTestService(t)
	defer cleanup()

	portfolio := createStrategyTestPortfolio(t, svc.store, "portfolio-strategy-watch")
	strategy, err := svc.CreatePortfolioMonitorStrategy(ctx, portfolio.ID, RequestCreatePortfolioMonitorStrategy{})
	if err != nil {
		t.Fatalf("create portfolio monitor strategy: %v", err)
	}
	watch, err := svc.CreateStrategyWatch(ctx, strategy.Strategy.ID)
	if err != nil {
		t.Fatalf("create strategy watch: %v", err)
	}
	if watch.Source != WatchSourceStrategy || watch.PortfolioID != portfolio.ID || watch.Symbol != "" {
		t.Fatalf("watch = %+v", watch)
	}
	if watch.TriggerConfig["template"] != "strategy_portfolio_monitor_watch_v1" {
		t.Fatalf("trigger config = %+v", watch.TriggerConfig)
	}
}

func newWatchTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, nil)
	return svc, func() {
		_ = store.Close()
	}
}

func watchTestHasRule(config map[string]any, key string, ruleType string) bool {
	rules, _ := config["rules"].([]any)
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		if rule["key"] == key && rule["type"] == ruleType {
			return true
		}
	}
	return false
}
