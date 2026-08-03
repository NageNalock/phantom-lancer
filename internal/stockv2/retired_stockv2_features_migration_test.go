package stockv2

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetiredStockV2FeaturesMigrationRemovesOnlyRetiredArtifacts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(filepath.Join(dir, "stockv2.db"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `
		ALTER TABLE stockv2_settings ADD COLUMN daily_bars_auto_enabled INTEGER DEFAULT 0;
		UPDATE stockv2_settings SET auto_update_enabled=0, daily_bars_auto_enabled=1;
		ALTER TABLE stockv2_news_link_candidates ADD COLUMN monitor_status TEXT NOT NULL DEFAULT 'pending';
		ALTER TABLE stockv2_news_link_candidates ADD COLUMN monitor_hit_id TEXT;
		ALTER TABLE stockv2_news_link_candidates ADD COLUMN monitored_at DATETIME;
		CREATE INDEX idx_stockv2_news_link_candidates_monitor_status
			ON stockv2_news_link_candidates(monitor_status);
		INSERT OR REPLACE INTO stockv2_monitor_task_configs
			(task_type, enabled, interval_seconds, updated_at)
		VALUES
			('news_strategy_monitor', 1, 60, datetime('now')),
			('portfolio_risk_monitor', 1, 60, datetime('now')),
			('daily_fundamental_monitor', 1, 60, datetime('now')),
			('data_quality_monitor', 1, 60, datetime('now')),
			('portfolio_sentinel', 1, 60, datetime('now'));
		INSERT INTO stockv2_monitor_runs
			(id, task_type, status, trigger_type, started_at, created_at)
		VALUES
			('retired-run', 'news_strategy_monitor', 'completed', 'manual', datetime('now'), datetime('now')),
			('retired-risk-run', 'portfolio_risk_monitor', 'completed', 'manual', datetime('now'), datetime('now')),
			('retired-fundamental-run', 'daily_fundamental_monitor', 'completed', 'manual', datetime('now'), datetime('now')),
			('retired-quality-run', 'data_quality_monitor', 'completed', 'manual', datetime('now'), datetime('now')),
			('kept-sentinel-run', 'portfolio_sentinel', 'completed', 'manual', datetime('now'), datetime('now')),
			('kept-run', 'data_strategy_monitor', 'completed', 'manual', datetime('now'), datetime('now'));
		INSERT INTO stockv2_monitor_hits
			(id, run_id, task_type, status, title, created_at)
		VALUES
			('retired-hit', 'retired-run', 'news_strategy_monitor', 'alerted', 'retired', datetime('now')),
			('kept-sentinel-hit', 'kept-sentinel-run', 'portfolio_sentinel', 'alerted', 'kept', datetime('now'));
		INSERT INTO stockv2_operation_reviews
			(id, hit_id, run_id, status, created_at, updated_at)
		VALUES ('retired-review', 'retired-hit', 'retired-run', 'pending', datetime('now'), datetime('now'));
		INSERT INTO stockv2_agent_runs
			(id, task_type, trigger_object_type, trigger_object_id, status, decision_ledger_id, created_at, updated_at)
		VALUES ('retired-agent-run', 'operation_review', 'operation_review', 'retired-review', 'completed', 'retired-ledger', datetime('now'), datetime('now'));
		INSERT INTO stockv2_agent_decision_ledgers
			(id, run_id, task_type, trigger_object_type, trigger_object_id, created_at, updated_at)
		VALUES ('retired-ledger', 'retired-agent-run', 'operation_review', 'operation_review', 'retired-review', datetime('now'), datetime('now'));
		INSERT INTO stockv2_alerts
			(id, monitor_hit_id, monitor_run_id, task_type, review_id, agent_run_id, decision_ledger_id,
			 status, level, title, triggered_at, created_at, updated_at)
		VALUES ('retired-alert', 'retired-hit', 'retired-run', 'news_strategy_monitor', 'retired-review',
			'retired-agent-run', 'retired-ledger', 'open', 'warning', 'retired', datetime('now'), datetime('now'), datetime('now'));
		INSERT INTO stockv2_agent_task_profiles
			(id, task_type, primary_model_id, fallback_model_id, max_budget, created_at, updated_at)
		VALUES
			('retired-risk-profile', 'portfolio_risk_review', '', '', 0, datetime('now'), datetime('now')),
			('retired-debate-profile', 'bull_bear_debate', '', '', 0, datetime('now'), datetime('now'));
		INSERT INTO stockv2_agent_runs
			(id, task_type, trigger_object_type, trigger_object_id, status, decision_ledger_id, created_at, updated_at)
		VALUES
			('retired-risk-agent-run', 'portfolio_risk_review', 'portfolio', 'p1', 'completed', 'retired-risk-ledger', datetime('now'), datetime('now')),
			('retired-debate-agent-run', 'bull_bear_debate', 'symbol', '000001', 'completed', 'retired-debate-ledger', datetime('now'), datetime('now'));
		INSERT INTO stockv2_agent_decision_ledgers
			(id, run_id, task_type, trigger_object_type, trigger_object_id, created_at, updated_at)
		VALUES
			('retired-risk-ledger', 'retired-risk-agent-run', 'portfolio_risk_review', 'portfolio', 'p1', datetime('now'), datetime('now')),
			('retired-debate-ledger', 'retired-debate-agent-run', 'bull_bear_debate', 'symbol', '000001', datetime('now'), datetime('now'));
		INSERT INTO stockv2_alerts
			(id, task_type, agent_run_id, decision_ledger_id, status, level, title, triggered_at, created_at, updated_at)
		VALUES ('retired-risk-alert', 'portfolio_risk_review', 'retired-risk-agent-run', 'retired-risk-ledger',
			'open', 'warning', 'retired agent alert', datetime('now'), datetime('now'), datetime('now'));
		INSERT INTO stockv2_strategies
			(id, name, kind, scope, source, status, created_at, updated_at)
		VALUES ('kept-strategy', 'kept', 'symbol_strategy', 'research', 'agent', 'active', datetime('now'), datetime('now'));
		INSERT INTO stockv2_strategy_versions
			(id, strategy_id, version_no, generation_meta_json, created_at)
		VALUES ('kept-version', 'kept-strategy', 1,
			'{"playbook":{"rules":[{"dataPrefilters":[],"newsPrefilters":[{"keyword":"legacy"}]}]}}',
			datetime('now'));
	`); err != nil {
		t.Fatalf("seed retired sqlite artifacts: %v", err)
	}

	if err := store.migrateRetiredStockV2Features(ctx); err != nil {
		t.Fatalf("migrate retired sqlite features: %v", err)
	}
	for _, taskType := range []string{
		"news_strategy_monitor",
		"portfolio_risk_monitor",
		"daily_fundamental_monitor",
		"data_quality_monitor",
	} {
		for _, table := range []string{"stockv2_monitor_task_configs", "stockv2_monitor_runs", "stockv2_monitor_hits"} {
			var count int
			query := "SELECT COUNT(*) FROM " + table + " WHERE task_type=?"
			if err := store.db.QueryRowContext(ctx, query, taskType).Scan(&count); err != nil {
				t.Fatalf("count %s rows in %s: %v", taskType, table, err)
			}
			if count != 0 {
				t.Fatalf("retired %s rows in %s = %d, want 0", taskType, table, count)
			}
		}
	}
	for _, check := range []struct {
		table string
		id    string
	}{
		{table: "stockv2_operation_reviews", id: "retired-review"},
		{table: "stockv2_alerts", id: "retired-alert"},
		{table: "stockv2_agent_runs", id: "retired-agent-run"},
		{table: "stockv2_agent_decision_ledgers", id: "retired-ledger"},
		{table: "stockv2_agent_task_profiles", id: "retired-risk-profile"},
		{table: "stockv2_agent_task_profiles", id: "retired-debate-profile"},
		{table: "stockv2_agent_runs", id: "retired-risk-agent-run"},
		{table: "stockv2_agent_runs", id: "retired-debate-agent-run"},
		{table: "stockv2_agent_decision_ledgers", id: "retired-risk-ledger"},
		{table: "stockv2_agent_decision_ledgers", id: "retired-debate-ledger"},
		{table: "stockv2_alerts", id: "retired-risk-alert"},
	} {
		var count int
		query := "SELECT COUNT(*) FROM " + check.table + " WHERE id=?"
		if err := store.db.QueryRowContext(ctx, query, check.id).Scan(&count); err != nil {
			t.Fatalf("count retired row %s in %s: %v", check.id, check.table, err)
		}
		if count != 0 {
			t.Fatalf("retired row %s in %s still exists", check.id, check.table)
		}
	}
	var duplicateSentinelConfig int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_monitor_task_configs WHERE task_type='portfolio_sentinel'`).Scan(&duplicateSentinelConfig); err != nil || duplicateSentinelConfig != 0 {
		t.Fatalf("duplicate sentinel config count=%d err=%v, want 0", duplicateSentinelConfig, err)
	}
	for _, id := range []string{"kept-run", "kept-sentinel-run"} {
		var keptRuns int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_monitor_runs WHERE id=?`, id).Scan(&keptRuns); err != nil || keptRuns != 1 {
			t.Fatalf("kept monitor run %s count=%d err=%v, want 1", id, keptRuns, err)
		}
	}
	var keptSentinelHit int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_monitor_hits WHERE id='kept-sentinel-hit'`).Scan(&keptSentinelHit); err != nil || keptSentinelHit != 1 {
		t.Fatalf("kept sentinel hit count=%d err=%v, want 1", keptSentinelHit, err)
	}
	var autoUpdateEnabled bool
	if err := store.db.QueryRowContext(ctx, `SELECT auto_update_enabled FROM stockv2_settings LIMIT 1`).Scan(&autoUpdateEnabled); err != nil || !autoUpdateEnabled {
		t.Fatalf("auto update enabled=%t err=%v, want true", autoUpdateEnabled, err)
	}
	if testColumnExists(t, store.db, "stockv2_settings", "daily_bars_auto_enabled") {
		t.Fatalf("legacy daily bars setting column still exists")
	}
	for _, column := range []string{"monitor_status", "monitor_hit_id", "monitored_at"} {
		if testColumnExists(t, store.db, "stockv2_news_link_candidates", column) {
			t.Fatalf("sqlite candidate column %s still exists", column)
		}
	}
	var generationMeta string
	if err := store.db.QueryRowContext(ctx, `SELECT generation_meta_json FROM stockv2_strategy_versions WHERE id='kept-version'`).Scan(&generationMeta); err != nil {
		t.Fatalf("read migrated strategy metadata: %v", err)
	}
	if strings.Contains(generationMeta, "newsPrefilters") || !strings.Contains(generationMeta, "dataPrefilters") {
		t.Fatalf("strategy metadata was not cleaned: %s", generationMeta)
	}
}

func TestMarketDataMigrationDropsRetiredCandidateColumns(t *testing.T) {
	ctx := context.Background()
	store, err := NewMarketDataStore(filepath.Join(t.TempDir(), "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new market store: %v", err)
	}
	defer store.Close()
	if _, err := store.db.ExecContext(ctx, `
		ALTER TABLE stockv2_news_link_candidates ADD COLUMN monitor_status VARCHAR DEFAULT 'pending';
		ALTER TABLE stockv2_news_link_candidates ADD COLUMN monitor_hit_id VARCHAR;
		ALTER TABLE stockv2_news_link_candidates ADD COLUMN monitored_at TIMESTAMP;
		CREATE INDEX idx_stockv2_market_news_link_candidates_monitor_status
			ON stockv2_news_link_candidates(monitor_status);
	`); err != nil {
		t.Fatalf("seed retired market columns: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM stockv2_market_schema_migrations WHERE id=?`, retiredNewsStrategyMonitorMarketMigration); err != nil {
		t.Fatalf("reset retired market migration marker: %v", err)
	}
	if err := store.migrateRetiredNewsStrategyMonitor(ctx); err != nil {
		t.Fatalf("migrate retired market columns: %v", err)
	}
	for _, column := range []string{"monitor_status", "monitor_hit_id", "monitored_at"} {
		var count int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name='stockv2_news_link_candidates' AND column_name=?
		`, column).Scan(&count); err != nil {
			t.Fatalf("check market column %s: %v", column, err)
		}
		if count != 0 {
			t.Fatalf("market candidate column %s still exists", column)
		}
	}
}
