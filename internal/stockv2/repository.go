package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store 数据存储包装器
type Store struct {
	db       *sql.DB
	dbPath   string
	marketDB *MarketDataStore
}

// wrapError 包装错误，err 为 nil 时返回 nil
func wrapError(err error, msg string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}

func normalizeInstrumentType(value string) string {
	switch strings.TrimSpace(value) {
	case InstrumentTypeExchangeFund:
		return InstrumentTypeExchangeFund
	default:
		return InstrumentTypeStock
	}
}

// runTx 在单个数据库事务里执行 apply:开启事务后运行 apply,出错自动回滚,
// 全部成功才提交。沿用 SavePortfolioValuation 的事务模式,供 RecordTransaction
// 这类「写流水 + 调现金 + 调持仓」的多步原子写复用。
func (s *Store) runTx(ctx context.Context, apply func(ctx context.Context, tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin transaction")
	}
	defer func() { _ = tx.Rollback() }() // commit 成功后为 no-op
	if err := apply(ctx, tx); err != nil {
		return err
	}
	return wrapError(tx.Commit(), "commit transaction")
}

// DefaultMarketDBPath 返回 Stock V2 市场数据资产库路径。
// SQLite 只承载组合、持仓、任务和设置等操作状态；日 K 等历史行情明细
// 进入 DuckDB，避免把高容量分析数据混入事务库。dataDir 为空时回退到
// SQLite 文件所在目录，保证单测和旧构造路径也能得到稳定文件路径。
func DefaultMarketDBPath(dataDir, sqliteDBPath string) string {
	if strings.TrimSpace(dataDir) != "" {
		return filepath.Join(dataDir, "stockv2", "stock_market.duckdb")
	}
	if sqliteDBPath != "" && sqliteDBPath != ":memory:" {
		return filepath.Join(filepath.Dir(sqliteDBPath), "stockv2", "stock_market.duckdb")
	}
	if sqliteDBPath == ":memory:" {
		return ":memory:"
	}
	return ""
}

// NewStore 创建新的存储实例。stockv2 使用独立的 SQLite 连接（带
// _parse_time=true 以支持 time.Time 字段扫描），并自动初始化 V2 操作状态表。
// 日 K 历史行情明细由同一个 Store 持有的 DuckDB marketDB 承载。
func NewStore(dbPath string) (*Store, error) {
	return NewStoreWithMarketDB(dbPath, DefaultMarketDBPath("", dbPath))
}

// NewStoreWithMarketDB 创建 Stock V2 存储，并显式指定 DuckDB 市场数据文件。
func NewStoreWithMarketDB(dbPath, marketDBPath string) (*Store, error) {
	dsn := fmt.Sprintf("%s?_parse_time=true&_loc=Local&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open stockv2 db: %w", err)
	}
	db.SetMaxOpenConns(1)
	marketDB, err := NewMarketDataStore(marketDBPath)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open stockv2 market db: %w", err)
	}

	s := &Store{db: db, dbPath: dbPath, marketDB: marketDB}
	// 旧版本曾把日 K 明细写入 SQLite；在任何 schema rebuild 发生前先迁入
	// DuckDB，避免开发期旧表重建逻辑误删历史行情资产。
	if err := s.migrateLegacyDailyBars(context.Background()); err != nil {
		_ = marketDB.Close()
		_ = db.Close()
		return nil, fmt.Errorf("migrate legacy daily bars: %w", err)
	}
	if err := s.init(context.Background()); err != nil {
		_ = marketDB.Close()
		_ = db.Close()
		return nil, fmt.Errorf("init stockv2 schema: %w", err)
	}
	return s, nil
}

// Close 关闭底层 DB 连接
func (s *Store) Close() error {
	var err error
	if s.marketDB != nil {
		err = s.marketDB.Close()
	}
	if s.db != nil {
		if closeErr := s.db.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

func (s *Store) MarketDBPath() string {
	if s.marketDB == nil {
		return ""
	}
	return s.marketDB.Path()
}

func (s *Store) assetDB() *sql.DB {
	if s != nil && s.marketDB != nil && s.marketDB.db != nil {
		return s.marketDB.db
	}
	// ponytail: fallback only keeps empty-path tests alive; normal Stock V2 stores use DuckDB.
	return s.db
}

// initSchemaSQL 初始化 V2 表结构。只创建表、索引和默认配置行，不依赖
// 触发器/视图（Go 层显式维护 updated_at）。
const initSchemaSQL = `
CREATE TABLE IF NOT EXISTS stockv2_instruments (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL UNIQUE,
    market TEXT NOT NULL,
    instrument_type TEXT NOT NULL DEFAULT 'stock',
    name TEXT,
    industry TEXT,
    sector TEXT,
    concepts TEXT,
    list_date TEXT,
    delist_date TEXT,
    status TEXT DEFAULT 'active',
    last_update_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_stock_profiles (
    symbol TEXT PRIMARY KEY,
    market TEXT NOT NULL,
    instrument_type TEXT NOT NULL DEFAULT 'stock',
    name TEXT NOT NULL,
    aliases_json TEXT NOT NULL DEFAULT '[]',
    industry TEXT,
    sectors_json TEXT NOT NULL DEFAULT '[]',
    concepts_json TEXT NOT NULL DEFAULT '[]',
    tags_json TEXT NOT NULL DEFAULT '[]',
    business_summary TEXT,
    profile_text TEXT NOT NULL,
    aliases_zh_json TEXT NOT NULL DEFAULT '[]',
    aliases_en_json TEXT NOT NULL DEFAULT '[]',
    keywords_zh_json TEXT NOT NULL DEFAULT '[]',
    keywords_en_json TEXT NOT NULL DEFAULT '[]',
    business_summary_zh TEXT,
    business_summary_en TEXT,
    business_lines_zh_json TEXT NOT NULL DEFAULT '[]',
    business_lines_en_json TEXT NOT NULL DEFAULT '[]',
    risk_tags_zh_json TEXT NOT NULL DEFAULT '[]',
    risk_tags_en_json TEXT NOT NULL DEFAULT '[]',
    profile_text_zh TEXT,
    profile_text_en TEXT,
    ai_profile_status TEXT NOT NULL DEFAULT 'missing',
    ai_profile_model TEXT,
    ai_profile_confidence REAL NOT NULL DEFAULT 0,
    ai_profile_error TEXT,
    ai_profile_updated_at DATETIME,
    fund_type TEXT,
    tracking_index TEXT,
    theme TEXT,
    constituent_hint TEXT,
    profile_version INTEGER NOT NULL DEFAULT 1,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_stock_profile_update_tasks (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL,
    market TEXT,
    trigger_source TEXT NOT NULL,
    trigger_reason TEXT,
    status TEXT NOT NULL,
    base_input_hash_before TEXT,
    base_input_hash_after TEXT,
    base_input_changed INTEGER NOT NULL DEFAULT 0,
    base_profile_status TEXT,
    ai_decision TEXT NOT NULL,
    agent_run_id TEXT,
    ai_profile_status TEXT,
    ai_profile_error TEXT,
    source_statuses_json TEXT NOT NULL DEFAULT '[]',
    error_message TEXT,
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_news_events (
    id TEXT PRIMARY KEY,
    raw_news_id TEXT,
    source TEXT NOT NULL,
    external_id TEXT,
    title TEXT NOT NULL,
    summary TEXT,
    content TEXT,
    url TEXT,
    quality_status TEXT,
    dedupe_key TEXT,
    link_status TEXT NOT NULL DEFAULT 'pending',
    event_at DATETIME NOT NULL,
    link_processed_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_news_link_candidates (
    id TEXT PRIMARY KEY,
    news_event_id TEXT NOT NULL,
    raw_news_id TEXT,
    symbol TEXT NOT NULL,
    market TEXT,
    instrument_name TEXT,
    match_method TEXT NOT NULL,
    score REAL NOT NULL DEFAULT 0,
    reason TEXT,
    matched_terms_json TEXT NOT NULL DEFAULT '[]',
    monitor_status TEXT NOT NULL DEFAULT 'pending',
    monitor_hit_id TEXT,
    monitored_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (news_event_id) REFERENCES stockv2_news_events(id) ON DELETE CASCADE,
    UNIQUE(news_event_id, symbol)
);
CREATE TABLE IF NOT EXISTS stockv2_portfolios (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    cash REAL NOT NULL DEFAULT 0.0,
    risk_level TEXT DEFAULT 'medium',
    max_single_position_pct REAL DEFAULT 20.0,
    max_drawdown_pct REAL DEFAULT 30.0,
    allow_buy INTEGER DEFAULT 1,
    allow_add INTEGER DEFAULT 1,
    allow_reduce INTEGER DEFAULT 1,
    allow_sell INTEGER DEFAULT 1,
    notes TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_holdings (
    id TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL,
    symbol TEXT NOT NULL,
    market TEXT,
    name TEXT,
    quantity REAL NOT NULL,
    available_quantity REAL NOT NULL,
    cost_price REAL NOT NULL,
    last_price REAL DEFAULT 0.0,
    last_price_at DATETIME,
    tradable_status TEXT DEFAULT 'unknown',
    market_value REAL DEFAULT 0.0,
    pnl REAL DEFAULT 0.0,
    position_pct REAL DEFAULT 0.0,
    acquired_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (portfolio_id) REFERENCES stockv2_portfolios(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS stockv2_transactions (
    id TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL,
    symbol TEXT NOT NULL,
    market TEXT,
    name TEXT,
    side TEXT NOT NULL,
    quantity REAL NOT NULL,
    price REAL NOT NULL,
    amount REAL NOT NULL,
    executed_at DATETIME NOT NULL,
    note TEXT,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (portfolio_id) REFERENCES stockv2_portfolios(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_stockv2_transactions_portfolio ON stockv2_transactions(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_transactions_portfolio_executed ON stockv2_transactions(portfolio_id, executed_at);
CREATE TABLE IF NOT EXISTS stockv2_quotes_latest (
    symbol TEXT PRIMARY KEY,
    market TEXT NOT NULL,
    name TEXT,
    last_price REAL NOT NULL DEFAULT 0.0,
    prev_close REAL NOT NULL DEFAULT 0.0,
    open_price REAL NOT NULL DEFAULT 0.0,
    high_price REAL NOT NULL DEFAULT 0.0,
    low_price REAL NOT NULL DEFAULT 0.0,
    volume REAL NOT NULL DEFAULT 0.0,
    amount REAL NOT NULL DEFAULT 0.0,
    pct_change REAL NOT NULL DEFAULT 0.0,
    amplitude REAL NOT NULL DEFAULT 0.0,
    turnover_rate REAL NOT NULL DEFAULT 0.0,
    volume_ratio REAL NOT NULL DEFAULT 0.0,
    main_net_inflow REAL NOT NULL DEFAULT 0.0,
    super_net_inflow REAL NOT NULL DEFAULT 0.0,
    large_net_inflow REAL NOT NULL DEFAULT 0.0,
    medium_net_inflow REAL NOT NULL DEFAULT 0.0,
    small_net_inflow REAL NOT NULL DEFAULT 0.0,
    main_net_inflow_pct REAL NOT NULL DEFAULT 0.0,
    quote_at DATETIME NOT NULL,
    fetched_at DATETIME NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_quote_snapshots (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL,
    market TEXT,
    name TEXT,
    last_price REAL NOT NULL DEFAULT 0.0,
    prev_close REAL NOT NULL DEFAULT 0.0,
    open_price REAL NOT NULL DEFAULT 0.0,
    high_price REAL NOT NULL DEFAULT 0.0,
    low_price REAL NOT NULL DEFAULT 0.0,
    volume REAL NOT NULL DEFAULT 0.0,
    amount REAL NOT NULL DEFAULT 0.0,
    pct_change REAL NOT NULL DEFAULT 0.0,
    amplitude REAL NOT NULL DEFAULT 0.0,
    turnover_rate REAL NOT NULL DEFAULT 0.0,
    volume_ratio REAL NOT NULL DEFAULT 0.0,
    main_net_inflow REAL NOT NULL DEFAULT 0.0,
    super_net_inflow REAL NOT NULL DEFAULT 0.0,
    large_net_inflow REAL NOT NULL DEFAULT 0.0,
    medium_net_inflow REAL NOT NULL DEFAULT 0.0,
    small_net_inflow REAL NOT NULL DEFAULT 0.0,
    main_net_inflow_pct REAL NOT NULL DEFAULT 0.0,
    quote_at DATETIME NOT NULL,
    collected_at DATETIME NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    created_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_minute_bars (
    symbol TEXT NOT NULL,
    market TEXT,
    minute_at DATETIME NOT NULL,
    open REAL NOT NULL DEFAULT 0.0,
    high REAL NOT NULL DEFAULT 0.0,
    low REAL NOT NULL DEFAULT 0.0,
    close REAL NOT NULL DEFAULT 0.0,
    prev_close REAL NOT NULL DEFAULT 0.0,
    volume REAL NOT NULL DEFAULT 0.0,
    amount REAL NOT NULL DEFAULT 0.0,
    pct_change REAL NOT NULL DEFAULT 0.0,
    main_net_inflow REAL NOT NULL DEFAULT 0.0,
    snapshot_count INTEGER NOT NULL DEFAULT 0,
    source TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY(symbol, minute_at)
);
CREATE TABLE IF NOT EXISTS stockv2_quote_refresh_statuses (
    symbol TEXT PRIMARY KEY,
    market TEXT,
    source TEXT,
    status TEXT NOT NULL,
    last_attempt_at DATETIME NOT NULL,
    last_success_at DATETIME,
    last_failure_at DATETIME,
    error_message TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_quote_refresh_statuses_status ON stockv2_quote_refresh_statuses(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_quote_refresh_statuses_updated_at ON stockv2_quote_refresh_statuses(updated_at);
CREATE TABLE IF NOT EXISTS stockv2_quote_refresh_task_state (
    task_type TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    trigger_type TEXT,
    started_at DATETIME,
    finished_at DATETIME,
    scope_summary TEXT,
    scanned_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_portfolio_snapshots (
    id TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL,
    valuation_at DATETIME NOT NULL,
    cash REAL NOT NULL DEFAULT 0.0,
    holding_market_value REAL NOT NULL DEFAULT 0.0,
    total_asset_value REAL NOT NULL DEFAULT 0.0,
    cash_pct REAL NOT NULL DEFAULT 0.0,
    position_count INTEGER NOT NULL DEFAULT 0,
    stale_quote_count INTEGER NOT NULL DEFAULT 0,
    estimated_quote_count INTEGER NOT NULL DEFAULT 0,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (portfolio_id) REFERENCES stockv2_portfolios(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS stockv2_strategies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    scope TEXT NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    symbol TEXT,
    market TEXT,
    portfolio_id TEXT,
    active_version_id TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    archived_at DATETIME,
    FOREIGN KEY (portfolio_id) REFERENCES stockv2_portfolios(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS stockv2_strategy_versions (
    id TEXT PRIMARY KEY,
    strategy_id TEXT NOT NULL,
    version_no INTEGER NOT NULL,
    title TEXT,
    direction TEXT,
    thesis TEXT,
    entry_conditions_json TEXT,
    exit_conditions_json TEXT,
    risk_notes TEXT,
    evidence_refs_json TEXT,
    generation_meta_json TEXT,
    created_by TEXT,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (strategy_id) REFERENCES stockv2_strategies(id) ON DELETE CASCADE,
    UNIQUE(strategy_id, version_no)
);
CREATE TABLE IF NOT EXISTS stockv2_watches (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    symbol TEXT,
    market TEXT,
    portfolio_id TEXT,
    strategy_id TEXT,
    strategy_version_id TEXT,
    trigger_policy TEXT NOT NULL,
    trigger_config_json TEXT,
    schedule_kind TEXT NOT NULL,
    cooldown_seconds INTEGER NOT NULL DEFAULT 0,
    last_checked_at DATETIME,
    last_triggered_at DATETIME,
    last_run_status TEXT,
    last_run_reason TEXT,
    archived_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (portfolio_id) REFERENCES stockv2_portfolios(id) ON DELETE SET NULL,
    FOREIGN KEY (strategy_id) REFERENCES stockv2_strategies(id) ON DELETE SET NULL,
    FOREIGN KEY (strategy_version_id) REFERENCES stockv2_strategy_versions(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS stockv2_alerts (
    id TEXT PRIMARY KEY,
    watch_id TEXT,
    monitor_hit_id TEXT,
    monitor_run_id TEXT,
    task_type TEXT,
    strategy_id TEXT,
    portfolio_id TEXT,
    symbol TEXT,
    market TEXT,
    review_id TEXT,
    review_status TEXT,
    agent_run_id TEXT,
    decision_ledger_id TEXT,
    trigger_source TEXT,
    status TEXT NOT NULL,
    level TEXT NOT NULL,
    title TEXT NOT NULL,
    summary TEXT,
    dedupe_key TEXT,
    evidence_json TEXT,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    first_seen_at DATETIME,
    last_seen_at DATETIME,
    triggered_at DATETIME NOT NULL,
    acknowledged_at DATETIME,
    resolved_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (watch_id) REFERENCES stockv2_watches(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS stockv2_update_jobs (
    id TEXT PRIMARY KEY,
    trigger_type TEXT NOT NULL,
    trigger_source TEXT,
    status TEXT NOT NULL,
    total_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    failed_items TEXT,
    start_at DATETIME,
    end_at DATETIME,
    error_message TEXT,
    created_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_update_progress (
    update_job_id TEXT PRIMARY KEY,
    processed_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    current_batch INTEGER DEFAULT 0,
    current_batch_progress INTEGER DEFAULT 0,
    current_symbol TEXT,
    error_count INTEGER DEFAULT 0,
    last_error TEXT,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (update_job_id) REFERENCES stockv2_update_jobs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS stockv2_settings (
    id TEXT PRIMARY KEY,
    auto_update_enabled INTEGER DEFAULT 0,
    update_interval_sec INTEGER DEFAULT 3600,
    proxy_enabled INTEGER DEFAULT 0,
    proxy_type TEXT,
    proxy_host TEXT,
    proxy_port INTEGER,
    last_scheduled_update DATETIME,
    financial_juice_enabled INTEGER DEFAULT 0,
    financial_juice_endpoint TEXT,
    financial_juice_cookie TEXT,
    base_profile_auto_maintain_enabled INTEGER DEFAULT 0,
    base_profile_maintain_interval_seconds INTEGER DEFAULT 86400,
    base_profile_deep_update_batch_size INTEGER DEFAULT 12,
    base_profile_deep_update_ai_budget INTEGER DEFAULT 2,
    base_profile_deep_update_rate_limit_ms INTEGER DEFAULT 1500,
    base_profile_last_maintain_at DATETIME,
    base_profile_next_maintain_at DATETIME,
    base_profile_last_maintain_result TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_instruments_symbol ON stockv2_instruments(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_instruments_market ON stockv2_instruments(market);
CREATE INDEX IF NOT EXISTS idx_stockv2_instruments_industry ON stockv2_instruments(industry);
CREATE INDEX IF NOT EXISTS idx_stockv2_instruments_status ON stockv2_instruments(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_stock_profiles_market ON stockv2_stock_profiles(market);
CREATE INDEX IF NOT EXISTS idx_stockv2_stock_profiles_updated_at ON stockv2_stock_profiles(updated_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_profile_update_tasks_symbol ON stockv2_stock_profile_update_tasks(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_profile_update_tasks_created_at ON stockv2_stock_profile_update_tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_profile_update_tasks_status ON stockv2_stock_profile_update_tasks(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_news_link_candidates_event ON stockv2_news_link_candidates(news_event_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_news_link_candidates_symbol ON stockv2_news_link_candidates(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_news_link_candidates_score ON stockv2_news_link_candidates(score);
CREATE INDEX IF NOT EXISTS idx_stockv2_portfolios_name ON stockv2_portfolios(name);
CREATE INDEX IF NOT EXISTS idx_stockv2_holdings_portfolio_id ON stockv2_holdings(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_holdings_symbol ON stockv2_holdings(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_quotes_latest_market ON stockv2_quotes_latest(market);
CREATE INDEX IF NOT EXISTS idx_stockv2_quotes_latest_status ON stockv2_quotes_latest(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_quotes_latest_fetched_at ON stockv2_quotes_latest(fetched_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_quote_snapshots_symbol_collected ON stockv2_quote_snapshots(symbol, collected_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_minute_bars_symbol_minute ON stockv2_minute_bars(symbol, minute_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_portfolio_snapshots_portfolio_id ON stockv2_portfolio_snapshots(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_portfolio_snapshots_valuation_at ON stockv2_portfolio_snapshots(valuation_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_strategies_kind ON stockv2_strategies(kind);
CREATE INDEX IF NOT EXISTS idx_stockv2_strategies_status ON stockv2_strategies(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_strategies_scope ON stockv2_strategies(scope);
CREATE INDEX IF NOT EXISTS idx_stockv2_strategies_symbol ON stockv2_strategies(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_strategies_portfolio_id ON stockv2_strategies(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_strategy_versions_strategy_id ON stockv2_strategy_versions(strategy_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_watches_status ON stockv2_watches(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_watches_portfolio_id ON stockv2_watches(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_watches_strategy_id ON stockv2_watches(strategy_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_watches_symbol ON stockv2_watches(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_watches_updated_at ON stockv2_watches(updated_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_watch_id ON stockv2_alerts(watch_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_status ON stockv2_alerts(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_triggered_at ON stockv2_alerts(triggered_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_dedupe_key ON stockv2_alerts(watch_id, dedupe_key);
CREATE INDEX IF NOT EXISTS idx_stockv2_update_jobs_status ON stockv2_update_jobs(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_update_jobs_created_at ON stockv2_update_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_stockv2_update_jobs_status_created ON stockv2_update_jobs(status, created_at DESC);
INSERT OR IGNORE INTO stockv2_settings (id, auto_update_enabled, update_interval_sec, created_at, updated_at)
VALUES ('1', 0, 3600, datetime('now'), datetime('now'));

-- 日 K 任务记录（语义独立于主数据 update job）
CREATE TABLE IF NOT EXISTS stockv2_daily_bar_jobs (
    id TEXT PRIMARY KEY,
    job_type TEXT NOT NULL,
    mode TEXT,
    symbol TEXT,
    status TEXT NOT NULL,
    total_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    failed_items TEXT,
    range TEXT,
    adjusted TEXT,
    trigger_type TEXT,
    trigger_source TEXT,
    start_at DATETIME,
    end_at DATETIME,
    error_message TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_daily_bar_jobs_created_at
    ON stockv2_daily_bar_jobs(created_at);

-- 监控与任务:系统固化的后台监控行为(非用户创建对象)。
-- task_configs 存开关/周期/范围/敏感度/冷却/Agent 开关;runs 记录每次执行;
-- hits 记录规则命中候选(candidate→可选 doublecheck→alerted)。
CREATE TABLE IF NOT EXISTS stockv2_monitor_task_configs (
    task_type TEXT PRIMARY KEY,
    enabled INTEGER DEFAULT 0,
    interval_seconds INTEGER DEFAULT 3600,
    scope TEXT,
    sensitivity TEXT DEFAULT 'normal',
    cooldown_seconds INTEGER DEFAULT 3600,
    agent_doublecheck_enabled INTEGER DEFAULT 0,
    agent_budget INTEGER DEFAULT 0,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS stockv2_monitor_runs (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL,
    trigger_type TEXT NOT NULL,
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    scope_summary TEXT,
    scanned_count INTEGER DEFAULT 0,
    hit_count INTEGER DEFAULT 0,
    alert_count INTEGER DEFAULT 0,
    review_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    error_message TEXT,
    metadata_json TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_runs_task_type ON stockv2_monitor_runs(task_type);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_runs_status ON stockv2_monitor_runs(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_runs_created_at ON stockv2_monitor_runs(created_at);
CREATE TABLE IF NOT EXISTS stockv2_monitor_hits (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL,
    strategy_id TEXT,
    portfolio_id TEXT,
    symbol TEXT,
    market TEXT,
    title TEXT NOT NULL,
    summary TEXT,
    evidence_json TEXT,
    agent_decision_id TEXT,
    alert_id TEXT,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (run_id) REFERENCES stockv2_monitor_runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_hits_run_id ON stockv2_monitor_hits(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_hits_task_type ON stockv2_monitor_hits(task_type);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_hits_status ON stockv2_monitor_hits(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_hits_strategy_id ON stockv2_monitor_hits(strategy_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_hits_portfolio_id ON stockv2_monitor_hits(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_monitor_hits_symbol ON stockv2_monitor_hits(symbol);
CREATE TABLE IF NOT EXISTS stockv2_operation_reviews (
    id TEXT PRIMARY KEY,
    hit_id TEXT NOT NULL,
    run_id TEXT,
    status TEXT NOT NULL,
    output_type TEXT,
    strategy_id TEXT,
    portfolio_id TEXT,
    symbol TEXT,
    market TEXT,
    input_context_json TEXT,
    result_json TEXT,
    result_summary TEXT,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    completed_at DATETIME,
    closed_at DATETIME,
    FOREIGN KEY (hit_id) REFERENCES stockv2_monitor_hits(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_hit_id ON stockv2_operation_reviews(hit_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_status ON stockv2_operation_reviews(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_run_id ON stockv2_operation_reviews(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_strategy_id ON stockv2_operation_reviews(strategy_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_portfolio_id ON stockv2_operation_reviews(portfolio_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_symbol ON stockv2_operation_reviews(symbol);
CREATE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_created_at ON stockv2_operation_reviews(created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stockv2_operation_reviews_active_hit
    ON stockv2_operation_reviews(hit_id)
    WHERE status <> 'closed';

-- ===== 消息面数据资产:RawNews -> NewsEvent -> NewsLinkCandidate =====
CREATE TABLE IF NOT EXISTS stockv2_raw_news (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    source_id TEXT,
    language TEXT,
    title TEXT NOT NULL,
    content TEXT,
    snippet TEXT,
    published_at DATETIME,
    fetched_at DATETIME NOT NULL,
    url TEXT,
    raw_payload_json TEXT,
    content_hash TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    quality TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_raw_news_source ON stockv2_raw_news(source);
CREATE INDEX IF NOT EXISTS idx_stockv2_raw_news_status ON stockv2_raw_news(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_raw_news_fetched_at ON stockv2_raw_news(fetched_at);

CREATE TABLE IF NOT EXISTS stockv2_news_source_states (
    source TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    cursor TEXT,
    poll_interval_seconds INTEGER NOT NULL DEFAULT 600,
    jitter_seconds INTEGER NOT NULL DEFAULT 60,
    batch_limit INTEGER NOT NULL DEFAULT 50,
    process_limit INTEGER NOT NULL DEFAULT 50,
    backoff_base_seconds INTEGER NOT NULL DEFAULT 30,
    backoff_max_seconds INTEGER NOT NULL DEFAULT 900,
    next_run_at DATETIME,
    last_run_at DATETIME,
    last_run_status TEXT,
    last_run_error TEXT,
    last_fetch_at DATETIME,
    last_success_at DATETIME,
    last_error_at DATETIME,
    last_error TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    backoff_until DATETIME,
    raw_news_count INTEGER NOT NULL DEFAULT 0,
    news_event_count INTEGER NOT NULL DEFAULT 0,
    link_candidate_count INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_news_source_states_status ON stockv2_news_source_states(status);

-- ===== Opportunity Discovery:主题机会发现对象、运行留痕和 embedding 元数据 =====
CREATE TABLE IF NOT EXISTS stockv2_opportunities (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    user_thesis TEXT NOT NULL,
    market_scope TEXT NOT NULL,
    instrument_scope TEXT NOT NULL,
    status TEXT NOT NULL,
    created_by TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunities_status ON stockv2_opportunities(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunities_updated_at ON stockv2_opportunities(updated_at);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_discovery_runs (
    id TEXT PRIMARY KEY,
    opportunity_id TEXT NOT NULL,
    agent_run_id TEXT,
    status TEXT NOT NULL,
    current_step_id TEXT,
    step_total INTEGER NOT NULL DEFAULT 0,
    step_completed INTEGER NOT NULL DEFAULT 0,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    evidence_count INTEGER NOT NULL DEFAULT 0,
    external_source_count INTEGER NOT NULL DEFAULT 0,
    started_at DATETIME,
    finished_at DATETIME,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (opportunity_id) REFERENCES stockv2_opportunities(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_runs_opportunity ON stockv2_opportunity_discovery_runs(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_runs_agent ON stockv2_opportunity_discovery_runs(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_runs_status ON stockv2_opportunity_discovery_runs(status);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_discovery_steps (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    step_key TEXT NOT NULL,
    step_title TEXT NOT NULL,
    status TEXT NOT NULL,
    order_index INTEGER NOT NULL,
    input_summary TEXT,
    output_summary TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at DATETIME,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (run_id) REFERENCES stockv2_opportunity_discovery_runs(id) ON DELETE CASCADE,
    UNIQUE(run_id, step_key)
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_steps_run ON stockv2_opportunity_discovery_steps(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_steps_status ON stockv2_opportunity_discovery_steps(status);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_candidates (
    id TEXT PRIMARY KEY,
    opportunity_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    symbol TEXT NOT NULL,
    market TEXT,
    instrument_type TEXT NOT NULL,
    name TEXT,
    relation_type TEXT NOT NULL,
    relevance_score REAL NOT NULL DEFAULT 0,
    evidence_score REAL NOT NULL DEFAULT 0,
    market_risk_score REAL NOT NULL DEFAULT 0,
    confidence REAL NOT NULL DEFAULT 0,
    rank INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    reason TEXT,
    risk_summary TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (opportunity_id) REFERENCES stockv2_opportunities(id) ON DELETE CASCADE,
    FOREIGN KEY (run_id) REFERENCES stockv2_opportunity_discovery_runs(id) ON DELETE CASCADE,
    UNIQUE(run_id, symbol)
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_opportunity ON stockv2_opportunity_candidates(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_run ON stockv2_opportunity_candidates(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_status ON stockv2_opportunity_candidates(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_symbol ON stockv2_opportunity_candidates(symbol);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_evidence (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    candidate_id TEXT,
    source_type TEXT NOT NULL,
    source_ref TEXT,
    title TEXT NOT NULL,
    summary TEXT,
    url TEXT,
    publisher TEXT,
    published_at DATETIME,
    confidence REAL NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    FOREIGN KEY (run_id) REFERENCES stockv2_opportunity_discovery_runs(id) ON DELETE CASCADE,
    FOREIGN KEY (candidate_id) REFERENCES stockv2_opportunity_candidates(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_evidence_run ON stockv2_opportunity_evidence(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_evidence_candidate ON stockv2_opportunity_evidence(candidate_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_evidence_source_type ON stockv2_opportunity_evidence(source_type);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_results (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE,
    summary TEXT,
    conclusion TEXT,
    recommended_next_action TEXT,
    raw_result_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    FOREIGN KEY (run_id) REFERENCES stockv2_opportunity_discovery_runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS stockv2_embedding_config (
    id TEXT PRIMARY KEY,
    embedding_model_id TEXT,
    enabled INTEGER NOT NULL DEFAULT 0,
    auto_maintain_enabled INTEGER NOT NULL DEFAULT 0,
    maintain_interval_seconds INTEGER NOT NULL DEFAULT 600,
    maintain_batch_size INTEGER NOT NULL DEFAULT 50,
    maintain_rate_limit_ms INTEGER NOT NULL DEFAULT 500,
    last_probe_at DATETIME,
    last_probe_status TEXT,
    last_error TEXT,
    last_maintain_at DATETIME,
    next_maintain_at DATETIME,
    last_maintain_result TEXT,
    updated_at DATETIME NOT NULL
);
INSERT OR IGNORE INTO stockv2_embedding_config
    (id, embedding_model_id, enabled, last_probe_status, updated_at)
VALUES
    ('stockv2-embedding-config', '', 0, 'embedding_model_not_configured', datetime('now'));
CREATE TABLE IF NOT EXISTS stockv2_embedding_assets (
    id TEXT PRIMARY KEY,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    text_summary TEXT,
    model_id TEXT NOT NULL,
    provider_id TEXT,
    embedding_protocol TEXT,
    embedding_dimensions INTEGER NOT NULL DEFAULT 0,
    vector_ref TEXT,
    status TEXT NOT NULL,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(object_type, object_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_object ON stockv2_embedding_assets(object_type, object_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_model ON stockv2_embedding_assets(model_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_status ON stockv2_embedding_assets(status);
-- 内置监控任务默认配置(全部默认关闭,用户显式开启后才会周期执行)。
INSERT OR IGNORE INTO stockv2_monitor_task_configs (task_type, enabled, interval_seconds, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at) VALUES ('latest_quote_refresh', 1, 30, 'normal', 0, 0, 0, datetime('now'));
INSERT OR IGNORE INTO stockv2_monitor_task_configs (task_type, enabled, interval_seconds, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at) VALUES ('data_strategy_monitor', 0, 600, 'normal', 1800, 0, 0, datetime('now'));
INSERT OR IGNORE INTO stockv2_monitor_task_configs (task_type, enabled, interval_seconds, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at) VALUES ('portfolio_risk_monitor', 0, 600, 'normal', 1800, 0, 0, datetime('now'));
INSERT OR IGNORE INTO stockv2_monitor_task_configs (task_type, enabled, interval_seconds, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at) VALUES ('news_strategy_monitor', 0, 600, 'normal', 3600, 0, 0, datetime('now'));
INSERT OR IGNORE INTO stockv2_monitor_task_configs (task_type, enabled, interval_seconds, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at) VALUES ('daily_fundamental_monitor', 0, 86400, 'normal', 3600, 0, 0, datetime('now'));
INSERT OR IGNORE INTO stockv2_monitor_task_configs (task_type, enabled, interval_seconds, sensitivity, cooldown_seconds, agent_doublecheck_enabled, agent_budget, updated_at) VALUES ('data_quality_monitor', 0, 3600, 'normal', 3600, 0, 0, datetime('now'));
DELETE FROM stockv2_monitor_task_configs WHERE task_type IN ('universe_update', 'daily_bars_sync');

-- ===== Agent 治理层:provider/model/task 绑定 + 运行 + 决策账本 =====
-- 本轮不存任何 secret;敏感字段在 service 层经 internal/safelog 脱敏后再写入。
CREATE TABLE IF NOT EXISTS stockv2_agent_provider_profiles (
    id TEXT PRIMARY KEY,
    provider_type TEXT NOT NULL,
    name TEXT NOT NULL,
    display_name TEXT,
    config_state TEXT NOT NULL DEFAULT 'not_configured',
    auth_state TEXT NOT NULL DEFAULT 'unknown',
    availability TEXT NOT NULL DEFAULT 'unknown',
    last_probe_at DATETIME,
    last_probe_result TEXT,
    metadata_json TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_provider_profiles_type ON stockv2_agent_provider_profiles(provider_type);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_provider_profiles_availability ON stockv2_agent_provider_profiles(availability);
CREATE TABLE IF NOT EXISTS stockv2_agent_model_profiles (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    model_name TEXT NOT NULL,
    display_name TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'available',
    cost_level TEXT NOT NULL DEFAULT 'medium',
    context_limit INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (provider_id) REFERENCES stockv2_agent_provider_profiles(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_model_profiles_provider_id ON stockv2_agent_model_profiles(provider_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_model_profiles_status ON stockv2_agent_model_profiles(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_model_profiles_enabled ON stockv2_agent_model_profiles(enabled);
CREATE TABLE IF NOT EXISTS stockv2_agent_task_profiles (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL UNIQUE,
    primary_model_id TEXT,
    fallback_model_id TEXT,
    max_budget INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_task_profiles_task_type ON stockv2_agent_task_profiles(task_type);
CREATE TABLE IF NOT EXISTS stockv2_agent_runs (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    provider_id TEXT,
    model_id TEXT,
    trigger_object_type TEXT NOT NULL,
    trigger_object_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    cost_estimate_json TEXT,
    error_message TEXT,
    output TEXT,
    decision_ledger_id TEXT,
    started_at DATETIME,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_runs_task_type ON stockv2_agent_runs(task_type);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_runs_status ON stockv2_agent_runs(status);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_runs_trigger ON stockv2_agent_runs(trigger_object_type, trigger_object_id);
CREATE TABLE IF NOT EXISTS stockv2_agent_decision_ledgers (
    id TEXT PRIMARY KEY,
    run_id TEXT,
    provider_id TEXT,
    model_id TEXT,
    task_type TEXT NOT NULL,
    trigger_object_type TEXT NOT NULL,
    trigger_object_id TEXT NOT NULL,
    input_summary TEXT,
    prompt TEXT,
    input_artifact_summary TEXT,
    output_artifact_summary TEXT,
    structured_output_json TEXT,
    redaction_summary_json TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_decision_ledgers_run_id ON stockv2_agent_decision_ledgers(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_agent_decision_ledgers_task_type ON stockv2_agent_decision_ledgers(task_type);

-- ===== Opportunity discovery =====
CREATE TABLE IF NOT EXISTS stockv2_opportunities (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    user_thesis TEXT,
    market_scope TEXT NOT NULL DEFAULT 'a_share',
    instrument_scope TEXT NOT NULL DEFAULT 'both',
    status TEXT NOT NULL DEFAULT 'draft',
    created_by TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunities_status ON stockv2_opportunities(status);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_discovery_runs (
    id TEXT PRIMARY KEY,
    opportunity_id TEXT NOT NULL,
    agent_run_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    current_step_id TEXT,
    step_total INTEGER NOT NULL DEFAULT 8,
    step_completed INTEGER NOT NULL DEFAULT 0,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    evidence_count INTEGER NOT NULL DEFAULT 0,
    external_source_count INTEGER NOT NULL DEFAULT 0,
    started_at DATETIME,
    finished_at DATETIME,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_discovery_runs_opp ON stockv2_opportunity_discovery_runs(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_discovery_runs_agent_run ON stockv2_opportunity_discovery_runs(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_discovery_runs_status ON stockv2_opportunity_discovery_runs(status);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_discovery_steps (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    step_key TEXT NOT NULL,
    step_title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    order_index INTEGER NOT NULL DEFAULT 0,
    input_summary TEXT,
    output_summary TEXT,
    metadata_json TEXT,
    started_at DATETIME,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(run_id, step_key)
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_discovery_steps_run ON stockv2_opportunity_discovery_steps(run_id);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_evidence (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    candidate_id TEXT,
    source_type TEXT NOT NULL,
    source_ref TEXT,
    title TEXT NOT NULL,
    summary TEXT,
    url TEXT,
    publisher TEXT,
    published_at DATETIME,
    confidence REAL NOT NULL DEFAULT 0,
    metadata_json TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_evidence_run ON stockv2_opportunity_evidence(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_evidence_candidate ON stockv2_opportunity_evidence(candidate_id);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_candidates (
    id TEXT PRIMARY KEY,
    opportunity_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    symbol TEXT NOT NULL,
    market TEXT NOT NULL,
    instrument_type TEXT NOT NULL DEFAULT 'stock',
    name TEXT NOT NULL,
    relation_type TEXT NOT NULL DEFAULT 'weak',
    relevance_score REAL NOT NULL DEFAULT 0,
    evidence_score REAL NOT NULL DEFAULT 0,
    market_risk_score REAL NOT NULL DEFAULT 0,
    confidence REAL NOT NULL DEFAULT 0,
    rank INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'candidate',
    reason TEXT,
    risk_summary TEXT,
    metadata_json TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(run_id, symbol)
);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_opp ON stockv2_opportunity_candidates(opportunity_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_run ON stockv2_opportunity_candidates(run_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_opportunity_candidates_symbol ON stockv2_opportunity_candidates(symbol);
CREATE TABLE IF NOT EXISTS stockv2_opportunity_results (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE,
    summary TEXT,
    conclusion TEXT,
    recommended_next_action TEXT,
    raw_result_json TEXT,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS stockv2_embedding_config (
    id TEXT PRIMARY KEY,
    embedding_model_id TEXT,
    enabled INTEGER NOT NULL DEFAULT 0,
    auto_maintain_enabled INTEGER NOT NULL DEFAULT 0,
    maintain_interval_seconds INTEGER NOT NULL DEFAULT 600,
    maintain_batch_size INTEGER NOT NULL DEFAULT 50,
    maintain_rate_limit_ms INTEGER NOT NULL DEFAULT 500,
    last_probe_at DATETIME,
    last_probe_status TEXT,
    last_error TEXT,
    last_maintain_at DATETIME,
    next_maintain_at DATETIME,
    last_maintain_result TEXT,
    updated_at DATETIME NOT NULL
);
INSERT OR IGNORE INTO stockv2_embedding_config
    (id, embedding_model_id, enabled, updated_at)
VALUES ('default', '', 0, datetime('now'));
-- Codex CLI 默认 Provider: 使用当前主机 codex 登录态,不需要第三方 key/base_url。
INSERT OR IGNORE INTO stockv2_agent_provider_profiles
    (id, provider_type, name, display_name, config_state, auth_state, availability, metadata_json, created_at, updated_at)
VALUES
    ('agent-provider-codex-cli-default', 'codex_cli', 'default', 'Codex CLI 默认 Provider', 'configured', 'unknown', 'unknown', '{"managed":"system","source":"codex_cli_default"}', datetime('now'), datetime('now'));
-- Agent task profiles 幂等种入。可配置/执行任务由 service 层校验;
-- 其余 task 先作为未来能力展示,后端 service 会拒绝绑定和执行。
INSERT OR IGNORE INTO stockv2_agent_task_profiles (id, task_type, primary_model_id, fallback_model_id, max_budget, created_at, updated_at) VALUES
    ('agent-task-operation-review', 'operation_review', '', '', 0, datetime('now'), datetime('now')),
    ('agent-task-strategy-generation', 'strategy_generation', '', '', 0, datetime('now'), datetime('now')),
    ('agent-task-opportunity-discovery', 'opportunity_discovery', '', '', 0, datetime('now'), datetime('now')),
    ('agent-task-news-event-review', 'news_event_review', '', '', 0, datetime('now'), datetime('now')),
    ('agent-task-portfolio-risk-review', 'portfolio_risk_review', '', '', 0, datetime('now'), datetime('now')),
    ('agent-task-stock-profile-summary', 'stock_profile_summary', '', '', 0, datetime('now'), datetime('now')),
    ('agent-task-bull-bear-debate', 'bull_bear_debate', '', '', 0, datetime('now'), datetime('now'));
`

// init 初始化 V2 表结构。如果检测到旧 schema（例如时间列是 TEXT 类型），
// 会先 DROP 所有 V2 表再重建（开发阶段数据无价值，保证 schema 最新）。
func (s *Store) init(ctx context.Context) error {
	// 检查是否需要重建（开发阶段，避免 schema 变更后残留旧表）
	needsRebuild, err := s.needsRebuild(ctx)
	if err != nil {
		return fmt.Errorf("check schema: %w", err)
	}
	if needsRebuild {
		if err := s.dropAllV2Tables(ctx); err != nil {
			return fmt.Errorf("drop old v2 tables: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS stockv2_agent_authorizations"); err != nil {
		return fmt.Errorf("drop old agent authorization table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, initSchemaSQL); err != nil {
		return fmt.Errorf("exec init schema: %w", err)
	}
	if err := s.ensureEmbeddingSchema(ctx); err != nil {
		return fmt.Errorf("ensure embedding schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_monitor_task_configs
		SET enabled = 1, interval_seconds = 30, updated_at = datetime('now')
		WHERE task_type = 'latest_quote_refresh' AND enabled = 0 AND interval_seconds = 300
	`); err != nil {
		return fmt.Errorf("migrate latest quote monitor default: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_instruments", "instrument_type", "TEXT NOT NULL DEFAULT 'stock'"); err != nil {
		return fmt.Errorf("add instrument_type column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stockv2_instruments_type ON stockv2_instruments(instrument_type)`); err != nil {
		return fmt.Errorf("create instrument type index: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_stock_profiles", "instrument_type", "TEXT NOT NULL DEFAULT 'stock'"); err != nil {
		return fmt.Errorf("add stock profile instrument_type column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stockv2_stock_profiles_type ON stockv2_stock_profiles(instrument_type)`); err != nil {
		return fmt.Errorf("create stock profile type index: %w", err)
	}
	quoteColumns := []struct {
		name    string
		colType string
	}{
		{"amplitude", "REAL NOT NULL DEFAULT 0"},
		{"turnover_rate", "REAL NOT NULL DEFAULT 0"},
		{"volume_ratio", "REAL NOT NULL DEFAULT 0"},
		{"main_net_inflow", "REAL NOT NULL DEFAULT 0"},
		{"super_net_inflow", "REAL NOT NULL DEFAULT 0"},
		{"large_net_inflow", "REAL NOT NULL DEFAULT 0"},
		{"medium_net_inflow", "REAL NOT NULL DEFAULT 0"},
		{"small_net_inflow", "REAL NOT NULL DEFAULT 0"},
		{"main_net_inflow_pct", "REAL NOT NULL DEFAULT 0"},
	}
	for _, column := range quoteColumns {
		if err := s.ensureColumn(ctx, "stockv2_quotes_latest", column.name, column.colType); err != nil {
			return fmt.Errorf("add latest quote %s column: %w", column.name, err)
		}
	}
	profileColumns := []struct {
		name    string
		colType string
	}{
		{"aliases_zh_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"aliases_en_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"keywords_zh_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"keywords_en_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"business_summary_zh", "TEXT"},
		{"business_summary_en", "TEXT"},
		{"business_lines_zh_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"business_lines_en_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"risk_tags_zh_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"risk_tags_en_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"profile_text_zh", "TEXT"},
		{"profile_text_en", "TEXT"},
		{"ai_profile_status", "TEXT NOT NULL DEFAULT 'missing'"},
		{"ai_profile_model", "TEXT"},
		{"ai_profile_confidence", "REAL NOT NULL DEFAULT 0"},
		{"ai_profile_error", "TEXT"},
		{"ai_profile_updated_at", "DATETIME"},
	}
	for _, column := range profileColumns {
		if err := s.ensureColumn(ctx, "stockv2_stock_profiles", column.name, column.colType); err != nil {
			return fmt.Errorf("add stock profile %s column: %w", column.name, err)
		}
	}
	profileTaskColumns := []struct {
		name    string
		colType string
	}{
		{"base_profile_status", "TEXT"},
		{"ai_profile_status", "TEXT"},
		{"ai_profile_error", "TEXT"},
	}
	for _, column := range profileTaskColumns {
		if err := s.ensureColumn(ctx, "stockv2_stock_profile_update_tasks", column.name, column.colType); err != nil {
			return fmt.Errorf("add stock profile update task %s column: %w", column.name, err)
		}
	}

	// 增量迁移：给 stockv2_update_jobs 加 failed_items 列
	if err := s.ensureColumn(ctx, "stockv2_update_jobs", "failed_items", "TEXT"); err != nil {
		return fmt.Errorf("add failed_items column: %w", err)
	}

	// 增量迁移：legacy 独立日 K 调度开关与最近日 K 维护时间
	if err := s.ensureColumn(ctx, "stockv2_settings", "daily_bars_auto_enabled", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("add daily_bars_auto_enabled column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "daily_bars_last_run", "DATETIME"); err != nil {
		return fmt.Errorf("add daily_bars_last_run column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "financial_juice_enabled", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("add financial_juice_enabled column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "financial_juice_endpoint", "TEXT"); err != nil {
		return fmt.Errorf("add financial_juice_endpoint column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "financial_juice_cookie", "TEXT"); err != nil {
		return fmt.Errorf("add financial_juice_cookie column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_auto_maintain_enabled", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("add base_profile_auto_maintain_enabled column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_maintain_interval_seconds", "INTEGER DEFAULT 86400"); err != nil {
		return fmt.Errorf("add base_profile_maintain_interval_seconds column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_deep_update_batch_size", "INTEGER DEFAULT 12"); err != nil {
		return fmt.Errorf("add base_profile_deep_update_batch_size column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_deep_update_ai_budget", "INTEGER DEFAULT 2"); err != nil {
		return fmt.Errorf("add base_profile_deep_update_ai_budget column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_deep_update_rate_limit_ms", "INTEGER DEFAULT 1500"); err != nil {
		return fmt.Errorf("add base_profile_deep_update_rate_limit_ms column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_last_maintain_at", "DATETIME"); err != nil {
		return fmt.Errorf("add base_profile_last_maintain_at column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_next_maintain_at", "DATETIME"); err != nil {
		return fmt.Errorf("add base_profile_next_maintain_at column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_settings", "base_profile_last_maintain_result", "TEXT"); err != nil {
		return fmt.Errorf("add base_profile_last_maintain_result column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_raw_news", "url", "TEXT"); err != nil {
		return fmt.Errorf("add raw news url column: %w", err)
	}
	newsEventColumns := []struct {
		name    string
		colType string
	}{
		{"raw_news_id", "TEXT"},
		{"external_id", "TEXT"},
		{"summary", "TEXT"},
		{"content", "TEXT"},
		{"url", "TEXT"},
		{"quality_status", "TEXT"},
		{"dedupe_key", "TEXT"},
		{"link_status", "TEXT NOT NULL DEFAULT 'pending'"},
		{"event_at", "DATETIME"},
		{"link_processed_at", "DATETIME"},
	}
	for _, column := range newsEventColumns {
		if err := s.ensureColumn(ctx, "stockv2_news_events", column.name, column.colType); err != nil {
			return fmt.Errorf("add news event %s column: %w", column.name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_news_events
		SET event_at = COALESCE(NULLIF(event_at, ''), created_at, updated_at, datetime('now'))
		WHERE event_at IS NULL OR event_at = ''
	`); err != nil {
		return fmt.Errorf("backfill news event event_at: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_news_link_candidates", "raw_news_id", "TEXT"); err != nil {
		return fmt.Errorf("add news link candidate raw_news_id column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_news_link_candidates", "monitor_status", "TEXT NOT NULL DEFAULT 'pending'"); err != nil {
		return fmt.Errorf("add news link candidate monitor_status column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_news_link_candidates", "monitor_hit_id", "TEXT"); err != nil {
		return fmt.Errorf("add news link candidate monitor_hit_id column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_news_link_candidates", "monitored_at", "DATETIME"); err != nil {
		return fmt.Errorf("add news link candidate monitored_at column: %w", err)
	}
	newsSourceColumns := []struct {
		name    string
		colType string
	}{
		{"poll_interval_seconds", "INTEGER NOT NULL DEFAULT 600"},
		{"jitter_seconds", "INTEGER NOT NULL DEFAULT 60"},
		{"batch_limit", "INTEGER NOT NULL DEFAULT 50"},
		{"process_limit", "INTEGER NOT NULL DEFAULT 50"},
		{"backoff_base_seconds", "INTEGER NOT NULL DEFAULT 30"},
		{"backoff_max_seconds", "INTEGER NOT NULL DEFAULT 900"},
		{"next_run_at", "DATETIME"},
		{"last_run_at", "DATETIME"},
		{"last_run_status", "TEXT"},
		{"last_run_error", "TEXT"},
	}
	for _, column := range newsSourceColumns {
		if err := s.ensureColumn(ctx, "stockv2_news_source_states", column.name, column.colType); err != nil {
			return fmt.Errorf("add news source state %s column: %w", column.name, err)
		}
	}
	if err := s.ensureColumn(ctx, "stockv2_daily_bar_jobs", "symbol", "TEXT"); err != nil {
		return fmt.Errorf("add daily bar job symbol column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_watches", "last_run_status", "TEXT"); err != nil {
		return fmt.Errorf("add watch last_run_status column: %w", err)
	}
	if err := s.ensureColumn(ctx, "stockv2_watches", "last_run_reason", "TEXT"); err != nil {
		return fmt.Errorf("add watch last_run_reason column: %w", err)
	}

	// 增量迁移：持仓表加建仓时间
	if err := s.ensureColumn(ctx, "stockv2_holdings", "acquired_at", "DATETIME"); err != nil {
		return fmt.Errorf("add acquired_at column: %w", err)
	}
	alertColumns := []struct {
		name    string
		colType string
	}{
		{"monitor_hit_id", "TEXT"},
		{"monitor_run_id", "TEXT"},
		{"task_type", "TEXT"},
		{"strategy_id", "TEXT"},
		{"portfolio_id", "TEXT"},
		{"symbol", "TEXT"},
		{"market", "TEXT"},
		{"review_id", "TEXT"},
		{"review_status", "TEXT"},
		{"agent_run_id", "TEXT"},
		{"decision_ledger_id", "TEXT"},
		{"trigger_source", "TEXT"},
		{"occurrence_count", "INTEGER NOT NULL DEFAULT 1"},
		{"first_seen_at", "DATETIME"},
		{"last_seen_at", "DATETIME"},
	}
	for _, column := range alertColumns {
		if err := s.ensureColumn(ctx, "stockv2_alerts", column.name, column.colType); err != nil {
			return fmt.Errorf("add alert %s column: %w", column.name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_monitor_hit_id ON stockv2_alerts(monitor_hit_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_monitor_run_id ON stockv2_alerts(monitor_run_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_task_type ON stockv2_alerts(task_type);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_strategy_id ON stockv2_alerts(strategy_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_portfolio_id ON stockv2_alerts(portfolio_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_symbol ON stockv2_alerts(symbol);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_monitor_dedupe_key ON stockv2_alerts(dedupe_key);
		CREATE INDEX IF NOT EXISTS idx_stockv2_alerts_last_seen_at ON stockv2_alerts(last_seen_at);
	`); err != nil {
		return fmt.Errorf("create alert monitor indexes: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_events_raw_news_id
		    ON stockv2_news_events(raw_news_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_events_link_status
		    ON stockv2_news_events(link_status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_events_event_at
		    ON stockv2_news_events(event_at);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_link_candidates_raw_news
		    ON stockv2_news_link_candidates(raw_news_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_link_candidates_monitor_status
		    ON stockv2_news_link_candidates(monitor_status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_daily_bar_jobs_running_scope
		    ON stockv2_daily_bar_jobs(status, mode, symbol, range, adjusted)
	`); err != nil {
		return fmt.Errorf("create stockv2 incremental indexes: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_quote_refresh_statuses (
		    symbol TEXT PRIMARY KEY,
		    market TEXT,
		    source TEXT,
		    status TEXT NOT NULL,
		    last_attempt_at DATETIME NOT NULL,
		    last_success_at DATETIME,
		    last_failure_at DATETIME,
		    error_message TEXT,
		    consecutive_failures INTEGER NOT NULL DEFAULT 0,
		    updated_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_quote_refresh_statuses_status ON stockv2_quote_refresh_statuses(status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_quote_refresh_statuses_updated_at ON stockv2_quote_refresh_statuses(updated_at);
		CREATE TABLE IF NOT EXISTS stockv2_quote_refresh_task_state (
		    task_type TEXT PRIMARY KEY,
		    status TEXT NOT NULL,
		    trigger_type TEXT,
		    started_at DATETIME,
		    finished_at DATETIME,
		    scope_summary TEXT,
		    scanned_count INTEGER NOT NULL DEFAULT 0,
		    success_count INTEGER NOT NULL DEFAULT 0,
		    failed_count INTEGER NOT NULL DEFAULT 0,
		    error_message TEXT,
		    updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("ensure quote refresh status tables: %w", err)
	}

	return nil
}

// ensureColumn 确保指定表有指定列，没有就 ALTER TABLE ADD
func (s *Store) ensureColumn(ctx context.Context, table, column, colType string) error {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?
	`, table, column).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, colType))
	return err
}

// needsRebuild 检测 V2 表是否是旧 schema。
func (s *Store) needsRebuild(ctx context.Context) (bool, error) {
	// 检查表是否存在
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='stockv2_portfolios'
	`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil // 表不存在，不需要重建，直接新建
	}

	// 检查 created_at 列的类型
	var colType string
	err = s.db.QueryRowContext(ctx, `
		SELECT type FROM pragma_table_info('stockv2_portfolios')
		WHERE name='created_at'
	`).Scan(&colType)
	if err != nil {
		return false, err
	}
	// 旧 schema 用 TEXT，新 schema 用 DATETIME
	if colType == "TEXT" {
		return true, nil
	}
	return false, nil
}

// dropAllV2Tables 删除所有 stockv2_ 前缀的表
func (s *Store) dropAllV2Tables(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type='table' AND name LIKE 'stockv2_%'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, t := range tables {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+t); err != nil {
			return fmt.Errorf("drop %s: %w", t, err)
		}
	}
	return nil
}

const portfolioSelectSQL = `
	SELECT id, name, COALESCE(description,''), cash, risk_level, max_single_position_pct,
	       max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, COALESCE(notes,''),
	       created_at, updated_at
	FROM stockv2_portfolios
`

// CreatePortfolio 创建投资组合
func (s *Store) CreatePortfolio(ctx context.Context, portfolio StockV2Portfolio) error {
	query := `
		INSERT INTO stockv2_portfolios (
			id, name, description, cash, risk_level, max_single_position_pct,
			max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	now := time.Now()
	portfolio.CreatedAt = now
	portfolio.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, query,
		portfolio.ID,
		portfolio.Name,
		portfolio.Description,
		portfolio.Cash,
		portfolio.RiskLevel,
		portfolio.MaxSinglePositionPct,
		portfolio.MaxDrawdownPct,
		portfolio.AllowBuy,
		portfolio.AllowAdd,
		portfolio.AllowReduce,
		portfolio.AllowSell,
		portfolio.Notes,
		portfolio.CreatedAt,
		portfolio.UpdatedAt,
	)

	return wrapError(err, "create portfolio")
}

// GetPortfolio 获取投资组合
func (s *Store) GetPortfolio(ctx context.Context, id string) (StockV2Portfolio, error) {
	portfolio, err := scanPortfolio(s.db.QueryRowContext(ctx, portfolioSelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Portfolio{}, ErrPortfolioNotFound
		}
		return StockV2Portfolio{}, wrapError(err, "get portfolio")
	}

	return portfolio, nil
}

// UpdatePortfolio 更新投资组合
func (s *Store) UpdatePortfolio(ctx context.Context, portfolio StockV2Portfolio) error {
	query := `
		UPDATE stockv2_portfolios
		SET name = ?, description = ?, cash = ?, risk_level = ?, max_single_position_pct = ?,
		    max_drawdown_pct = ?, allow_buy = ?, allow_add = ?, allow_reduce = ?, allow_sell = ?,
		    notes = ?, updated_at = ?
		WHERE id = ?
	`

	portfolio.UpdatedAt = time.Now()

	result, err := s.db.ExecContext(ctx, query,
		portfolio.Name,
		portfolio.Description,
		portfolio.Cash,
		portfolio.RiskLevel,
		portfolio.MaxSinglePositionPct,
		portfolio.MaxDrawdownPct,
		portfolio.AllowBuy,
		portfolio.AllowAdd,
		portfolio.AllowReduce,
		portfolio.AllowSell,
		portfolio.Notes,
		portfolio.UpdatedAt,
		portfolio.ID,
	)

	if err != nil {
		return wrapError(err, "update portfolio")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check affected rows")
	}

	if rows == 0 {
		return ErrPortfolioNotFound
	}

	return nil
}

// DeletePortfolio 删除投资组合
func (s *Store) DeletePortfolio(ctx context.Context, id string) error {
	query := "DELETE FROM stockv2_portfolios WHERE id = ?"

	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return wrapError(err, "delete portfolio")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check affected rows")
	}

	if rows == 0 {
		return ErrPortfolioNotFound
	}

	return nil
}

// ListPortfolios 列出所有投资组合
func (s *Store) ListPortfolios(ctx context.Context) ([]StockV2Portfolio, error) {
	rows, err := s.db.QueryContext(ctx, portfolioSelectSQL+" ORDER BY created_at DESC")
	if err != nil {
		return nil, wrapError(err, "list portfolios")
	}
	return scanRows(rows, scanPortfolio, "scan portfolio", "iterate portfolios")
}

func scanPortfolio(row rowScanner) (StockV2Portfolio, error) {
	var p StockV2Portfolio
	err := row.Scan(
		&p.ID, &p.Name, &p.Description, &p.Cash, &p.RiskLevel, &p.MaxSinglePositionPct,
		&p.MaxDrawdownPct, &p.AllowBuy, &p.AllowAdd, &p.AllowReduce, &p.AllowSell,
		&p.Notes, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, err
}

const holdingSelectSQL = `
	SELECT id, portfolio_id, symbol, COALESCE(market,''), COALESCE(name,''), quantity, available_quantity,
	       cost_price, last_price, last_price_at, COALESCE(tradable_status,'unknown'), market_value,
	       pnl, position_pct, acquired_at, created_at, updated_at
	FROM stockv2_holdings
`

// CreateHolding 创建持仓
func (s *Store) CreateHolding(ctx context.Context, holding StockV2Holding) error {
	query := `
		INSERT INTO stockv2_holdings (
			id, portfolio_id, symbol, market, name, quantity, available_quantity,
			cost_price, last_price, last_price_at, tradable_status, market_value,
			pnl, position_pct, acquired_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	now := time.Now()
	if holding.CreatedAt.IsZero() {
		holding.CreatedAt = now
	}
	if holding.UpdatedAt.IsZero() {
		holding.UpdatedAt = now
	}
	if holding.AcquiredAt.IsZero() {
		holding.AcquiredAt = now
	}

	_, err := s.db.ExecContext(ctx, query,
		holding.ID,
		holding.PortfolioID,
		holding.Symbol,
		holding.Market,
		holding.Name,
		holding.Quantity,
		holding.AvailableQuantity,
		holding.CostPrice,
		holding.LastPrice,
		holding.LastPriceAt,
		holding.TradableStatus,
		holding.MarketValue,
		holding.PnL,
		holding.PositionPct,
		holding.AcquiredAt,
		holding.CreatedAt,
		holding.UpdatedAt,
	)

	return wrapError(err, "create holding")
}

// GetHolding 获取持仓
func (s *Store) GetHolding(ctx context.Context, id string) (StockV2Holding, error) {
	holding, err := scanHolding(s.db.QueryRowContext(ctx, holdingSelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Holding{}, ErrHoldingNotFound
		}
		return StockV2Holding{}, wrapError(err, "get holding")
	}

	return holding, nil
}

// UpdateHolding 更新持仓
func (s *Store) UpdateHolding(ctx context.Context, holding StockV2Holding) error {
	query := `
		UPDATE stockv2_holdings
		SET symbol = ?, market = ?, name = ?, quantity = ?, available_quantity = ?,
		    cost_price = ?, last_price = ?, last_price_at = ?, tradable_status = ?,
		    market_value = ?, pnl = ?, position_pct = ?, acquired_at = ?, updated_at = ?
		WHERE id = ?
	`

	holding.UpdatedAt = time.Now()

	result, err := s.db.ExecContext(ctx, query,
		holding.Symbol,
		holding.Market,
		holding.Name,
		holding.Quantity,
		holding.AvailableQuantity,
		holding.CostPrice,
		holding.LastPrice,
		holding.LastPriceAt,
		holding.TradableStatus,
		holding.MarketValue,
		holding.PnL,
		holding.PositionPct,
		holding.AcquiredAt,
		holding.UpdatedAt,
		holding.ID,
	)

	if err != nil {
		return wrapError(err, "update holding")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check affected rows")
	}

	if rows == 0 {
		return ErrHoldingNotFound
	}

	return nil
}

// DeleteHolding 删除持仓
func (s *Store) DeleteHolding(ctx context.Context, id string) error {
	query := "DELETE FROM stockv2_holdings WHERE id = ?"

	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return wrapError(err, "delete holding")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check affected rows")
	}

	if rows == 0 {
		return ErrHoldingNotFound
	}

	return nil
}

// ListHoldings 列出投资组合的所有持仓
func (s *Store) ListHoldings(ctx context.Context, portfolioID string) ([]StockV2Holding, error) {
	rows, err := s.db.QueryContext(ctx, holdingSelectSQL+" WHERE portfolio_id = ? ORDER BY created_at DESC", portfolioID)
	if err != nil {
		return nil, wrapError(err, "list holdings")
	}
	return scanRows(rows, scanHolding, "scan holding", "iterate holdings")
}

func scanHolding(row rowScanner) (StockV2Holding, error) {
	var h StockV2Holding
	var lastPriceAt, acquiredAt sql.NullTime
	err := row.Scan(
		&h.ID, &h.PortfolioID, &h.Symbol, &h.Market, &h.Name, &h.Quantity,
		&h.AvailableQuantity, &h.CostPrice, &h.LastPrice, &lastPriceAt,
		&h.TradableStatus, &h.MarketValue, &h.PnL, &h.PositionPct, &acquiredAt,
		&h.CreatedAt, &h.UpdatedAt,
	)
	if lastPriceAt.Valid {
		h.LastPriceAt = lastPriceAt.Time
	}
	if acquiredAt.Valid {
		h.AcquiredAt = acquiredAt.Time
	} else {
		h.AcquiredAt = h.CreatedAt
	}
	return h, err
}

const instrumentSelectSQL = `
	SELECT id, symbol, market, COALESCE(instrument_type,'stock'), COALESCE(name,''), COALESCE(industry,''), COALESCE(sector,''),
	       concepts, COALESCE(list_date,''), COALESCE(delist_date,''), COALESCE(status,'active'),
	       last_update_at, created_at, updated_at
	FROM stockv2_instruments
`

// CreateInstrument 创建标的主数据
// UpsertInstrument 插入或更新标的主数据（按 symbol 去重）
func (s *Store) UpsertInstrument(ctx context.Context, instrument StockV2Instrument) error {
	query := `
		INSERT INTO stockv2_instruments (
			id, symbol, market, instrument_type, name, industry, sector, concepts,
			list_date, delist_date, status, last_update_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			market = excluded.market,
			instrument_type = excluded.instrument_type,
			name = excluded.name,
			industry = excluded.industry,
			sector = excluded.sector,
			concepts = excluded.concepts,
			list_date = excluded.list_date,
			delist_date = excluded.delist_date,
			status = excluded.status,
			last_update_at = excluded.last_update_at,
			updated_at = excluded.updated_at
	`

	now := time.Now()
	instrument.UpdatedAt = now
	instrument.InstrumentType = normalizeInstrumentType(instrument.InstrumentType)

	// 如果是新插入，设置 created_at；ON CONFLICT 时保留原值
	// 这里我们用同一份参数，created_at 在冲突时被 excluded 值覆盖的问题不存在
	// 因为 DO UPDATE SET 里没列 created_at，会保留原值
	instrument.CreatedAt = now

	conceptsJSON, _ := json.Marshal(instrument.Concepts)

	_, err := s.assetDB().ExecContext(ctx, query,
		instrument.ID,
		instrument.Symbol,
		instrument.Market,
		instrument.InstrumentType,
		instrument.Name,
		instrument.Industry,
		instrument.Sector,
		string(conceptsJSON),
		instrument.ListDate,
		instrument.DelistDate,
		instrument.Status,
		now,
		instrument.CreatedAt,
		instrument.UpdatedAt,
	)

	return wrapError(err, "upsert instrument")
}

// GetInstrument 获取标的主数据
func (s *Store) GetInstrument(ctx context.Context, symbol string) (StockV2Instrument, error) {
	instrument, err := scanInstrument(s.assetDB().QueryRowContext(ctx, instrumentSelectSQL+" WHERE symbol = ?", symbol))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Instrument{}, ErrInstrumentNotFound
		}
		return StockV2Instrument{}, wrapError(err, "get instrument")
	}

	return instrument, nil
}

// GetInstruments 获取标的主数据列表（分页）
func (s *Store) CountInstruments(ctx context.Context) (int, error) {
	var count int
	err := s.assetDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_instruments`).Scan(&count)
	return count, wrapError(err, "count instruments")
}

func (s *Store) CountInstrumentsFiltered(ctx context.Context, market, instrumentType, profileStatus string) (int, error) {
	where, args := instrumentFilterSQL(market, instrumentType, profileStatus)
	var count int
	query := `SELECT COUNT(*) FROM stockv2_instruments i` + instrumentProfileJoinSQL(profileStatus) + ` WHERE ` + where
	err := s.assetDB().QueryRowContext(ctx, query, args...).Scan(&count)
	return count, wrapError(err, "count filtered instruments")
}

func (s *Store) GetInstruments(ctx context.Context, limit int, offset int) ([]StockV2Instrument, error) {
	return s.GetInstrumentsFiltered(ctx, "", "", "", limit, offset)
}

func (s *Store) GetInstrumentsFiltered(ctx context.Context, market, instrumentType, profileStatus string, limit int, offset int) ([]StockV2Instrument, error) {
	where, args := instrumentFilterSQL(market, instrumentType, profileStatus)
	args = append(args, limit, offset)
	query := instrumentSelectFilteredSQL(profileStatus) + " WHERE " + where + " ORDER BY i.created_at DESC LIMIT ? OFFSET ?"
	rows, err := s.assetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "get instruments")
	}
	return scanRows(rows, scanInstrument, "scan instrument", "iterate instruments")
}

// SearchInstruments 按代码或名称搜索股票（模糊匹配）
func (s *Store) SearchInstruments(ctx context.Context, keyword string, limit int) ([]StockV2Instrument, error) {
	return s.SearchInstrumentsFiltered(ctx, keyword, "", "", "", limit)
}

func (s *Store) SearchInstrumentsFiltered(ctx context.Context, keyword, market, instrumentType, profileStatus string, limit int) ([]StockV2Instrument, error) {
	if keyword == "" {
		return []StockV2Instrument{}, nil
	}
	pattern := "%" + strings.ToLower(keyword) + "%"
	where, filterArgs := instrumentFilterSQL(market, instrumentType, profileStatus)
	args := append([]any{pattern, pattern}, filterArgs...)
	args = append(args, keyword, limit)
	query := instrumentSelectFilteredSQL(profileStatus) + `
		WHERE (LOWER(i.symbol) LIKE ? OR LOWER(i.name) LIKE ?) AND ` + where + `
		ORDER BY
		  CASE WHEN LOWER(i.symbol) = LOWER(?) THEN 0 ELSE 1 END,
		  i.symbol ASC
		LIMIT ?
	`

	rows, err := s.assetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "search instruments")
	}
	return scanRows(rows, scanInstrument, "scan instrument", "iterate instruments")
}

func instrumentSelectFilteredSQL(profileStatus string) string {
	return `
	SELECT i.id, i.symbol, i.market, COALESCE(i.instrument_type,'stock'), COALESCE(i.name,''), COALESCE(i.industry,''), COALESCE(i.sector,''),
	       i.concepts, COALESCE(i.list_date,''), COALESCE(i.delist_date,''), COALESCE(i.status,'active'),
	       i.last_update_at, i.created_at, i.updated_at
	FROM stockv2_instruments i
` + instrumentProfileJoinSQL(profileStatus)
}

func instrumentProfileJoinSQL(profileStatus string) string {
	if instrumentProfileStatusSQL(profileStatus) == "" {
		return ""
	}
	return " LEFT JOIN stockv2_stock_profiles p ON p.symbol = i.symbol"
}

func instrumentFilterSQL(market, instrumentType, profileStatus string) (string, []any) {
	var parts []string
	var args []any
	switch strings.ToUpper(strings.TrimSpace(market)) {
	case "SH", "SZ", "BJ":
		parts = append(parts, "i.market = ?")
		args = append(args, strings.ToUpper(strings.TrimSpace(market)))
	}
	switch strings.TrimSpace(instrumentType) {
	case InstrumentTypeExchangeFund:
		parts = append(parts, "COALESCE(i.instrument_type,'stock') = ?")
		args = append(args, InstrumentTypeExchangeFund)
	case InstrumentTypeStock:
		parts = append(parts, "COALESCE(i.instrument_type,'stock') = ?")
		args = append(args, InstrumentTypeStock)
	}
	if profileSQL := instrumentProfileStatusSQL(profileStatus); profileSQL != "" {
		parts = append(parts, profileSQL)
	}
	if len(parts) == 0 {
		return "1=1", args
	}
	return strings.Join(parts, " AND "), args
}

func instrumentProfileStatusSQL(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "basic_ready", "ready":
		return "p.symbol IS NOT NULL AND COALESCE(p.profile_text,'') <> ''"
	case "basic_partial", "partial":
		return "p.symbol IS NOT NULL AND COALESCE(p.profile_text,'') = ''"
	case "basic_missing", "missing":
		return "p.symbol IS NULL"
	case "ai_ready":
		return "p.symbol IS NOT NULL AND COALESCE(p.ai_profile_status,'missing') = 'ready'"
	case "ai_failed":
		return "p.symbol IS NOT NULL AND COALESCE(p.ai_profile_status,'missing') = 'failed'"
	case "ai_not_configured":
		return "p.symbol IS NOT NULL AND COALESCE(p.ai_profile_status,'missing') = 'not_configured'"
	case "ai_missing":
		return "p.symbol IS NULL OR COALESCE(p.ai_profile_status,'missing') = 'missing'"
	default:
		return ""
	}
}

// GetInstrumentsByMarket 根据市场获取股票列表
func (s *Store) GetInstrumentsByMarket(ctx context.Context, market string) ([]StockV2Instrument, error) {
	rows, err := s.assetDB().QueryContext(ctx, instrumentSelectSQL+" WHERE market = ? AND status = 'active' ORDER BY symbol ASC", market)
	if err != nil {
		return nil, wrapError(err, "get instruments by market")
	}
	return scanRows(rows, scanInstrument, "scan instrument", "iterate instruments")
}

func scanInstrument(row rowScanner) (StockV2Instrument, error) {
	var inst StockV2Instrument
	var conceptsJSON []byte
	var lastUpdate sql.NullTime
	err := row.Scan(
		&inst.ID, &inst.Symbol, &inst.Market, &inst.InstrumentType, &inst.Name,
		&inst.Industry, &inst.Sector, &conceptsJSON, &inst.ListDate, &inst.DelistDate,
		&inst.Status, &lastUpdate, &inst.CreatedAt, &inst.UpdatedAt,
	)
	if lastUpdate.Valid {
		inst.LastUpdate = lastUpdate.Time
	}
	inst.InstrumentType = normalizeInstrumentType(inst.InstrumentType)
	if len(conceptsJSON) > 0 {
		_ = json.Unmarshal(conceptsJSON, &inst.Concepts)
	}
	return inst, err
}

// UpdateInstrument 更新标的主数据
func (s *Store) UpdateInstrument(ctx context.Context, instrument StockV2Instrument) error {
	query := `
		UPDATE stockv2_instruments
		SET instrument_type = ?, name = ?, industry = ?, sector = ?, concepts = ?,
		    list_date = ?, delist_date = ?, status = ?, last_update_at = ?, updated_at = ?
		WHERE id = ?
	`

	now := time.Now()
	instrument.UpdatedAt = now
	instrument.LastUpdate = now
	instrument.InstrumentType = normalizeInstrumentType(instrument.InstrumentType)

	conceptsJSON, _ := json.Marshal(instrument.Concepts)

	result, err := s.assetDB().ExecContext(ctx, query,
		instrument.InstrumentType,
		instrument.Name,
		instrument.Industry,
		instrument.Sector,
		string(conceptsJSON),
		instrument.ListDate,
		instrument.DelistDate,
		instrument.Status,
		now,
		instrument.UpdatedAt,
		instrument.ID,
	)

	if err != nil {
		return wrapError(err, "update instrument")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check affected rows")
	}

	if rows == 0 {
		return ErrInstrumentNotFound
	}

	return nil
}

func (s *Store) CreateUpdateJob(ctx context.Context, job StockV2UpdateJob) error {
	query := `
		INSERT INTO stockv2_update_jobs (
			id, trigger_type, trigger_source, status, total_count,
			processed_count, success_count, failed_count, start_at, end_at,
			error_message, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	now := time.Now()
	job.CreatedAt = now

	_, err := s.db.ExecContext(ctx, query,
		job.ID,
		job.TriggerType,
		job.TriggerSource,
		job.Status,
		job.TotalCount,
		job.ProcessedCount,
		job.SuccessCount,
		job.FailedCount,
		job.StartAt,
		job.EndAt,
		job.ErrorMessage,
		job.CreatedAt,
	)

	return wrapError(err, "create update job")
}

const updateJobSelectSQL = `
	SELECT id, trigger_type, COALESCE(trigger_source,''), status, total_count,
	       processed_count, success_count, failed_count, COALESCE(failed_items,''),
	       start_at, end_at, COALESCE(error_message,''), created_at
	FROM stockv2_update_jobs
`

func (s *Store) GetUpdateJob(ctx context.Context, id string) (StockV2UpdateJob, error) {
	job, err := scanUpdateJob(s.db.QueryRowContext(ctx, updateJobSelectSQL+" WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2UpdateJob{}, ErrUpdateJobNotFound
		}
		return StockV2UpdateJob{}, wrapError(err, "get update job")
	}
	return job, nil
}

// GetLatestUpdateJob 获取最新的更新任务
func (s *Store) GetLatestUpdateJob(ctx context.Context) (StockV2UpdateJob, error) {
	job, err := scanUpdateJob(s.db.QueryRowContext(ctx, updateJobSelectSQL+" ORDER BY created_at DESC LIMIT 1"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2UpdateJob{}, ErrUpdateJobNotFound
		}
		return StockV2UpdateJob{}, wrapError(err, "get latest update job")
	}
	return job, nil
}

// UpdateUpdateJob 增量更新更新任务（只更新非零值字段）
func (s *Store) UpdateUpdateJob(ctx context.Context, job StockV2UpdateJob) error {
	var sets []string
	var args []any

	if job.Status != "" {
		sets = append(sets, "status = ?")
		args = append(args, job.Status)
	}
	if job.TotalCount > 0 {
		sets = append(sets, "total_count = ?")
		args = append(args, job.TotalCount)
	}
	if job.ProcessedCount > 0 || job.TotalCount > 0 {
		sets = append(sets, "processed_count = ?")
		args = append(args, job.ProcessedCount)
	}
	if job.SuccessCount > 0 || job.TotalCount > 0 {
		sets = append(sets, "success_count = ?")
		args = append(args, job.SuccessCount)
	}
	if job.FailedCount > 0 || job.TotalCount > 0 {
		sets = append(sets, "failed_count = ?")
		args = append(args, job.FailedCount)
	}
	if len(job.FailedItems) > 0 {
		// 序列化失败详情
		failedJSON, _ := json.Marshal(job.FailedItems)
		sets = append(sets, "failed_items = ?")
		args = append(args, string(failedJSON))
	}
	if !job.EndAt.IsZero() {
		sets = append(sets, "end_at = ?")
		args = append(args, job.EndAt)
	}
	if job.ErrorMessage != "" {
		sets = append(sets, "error_message = ?")
		args = append(args, job.ErrorMessage)
	}

	if len(sets) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE stockv2_update_jobs SET %s WHERE id = ?", strings.Join(sets, ", "))
	args = append(args, job.ID)

	_, err := s.db.ExecContext(ctx, query, args...)
	return wrapError(err, "update update job")
}

func (s *Store) FailRunningUpdateJobs(ctx context.Context, reason string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_update_jobs
		SET status = 'failed', end_at = ?, error_message = ?
		WHERE status = 'running'
	`, time.Now(), strings.TrimSpace(reason))
	if err != nil {
		return 0, wrapError(err, "fail running update jobs")
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

// ListUpdateJobs 获取更新任务列表
func (s *Store) ListUpdateJobs(ctx context.Context, limit int) ([]StockV2UpdateJob, error) {
	rows, err := s.db.QueryContext(ctx, updateJobSelectSQL+" ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, wrapError(err, "list update jobs")
	}
	return scanRows(rows, scanUpdateJob, "scan update job", "iterate update jobs")
}

// PruneUpdateJobs 清理更新任务记录，只保留最新的 keep 条
func (s *Store) PruneUpdateJobs(ctx context.Context, keep int) error {
	query := `
		DELETE FROM stockv2_update_jobs
		WHERE id IN (
			SELECT id FROM stockv2_update_jobs
			ORDER BY created_at DESC
			LIMIT -1 OFFSET ?
		)
	`
	_, err := s.db.ExecContext(ctx, query, keep)
	return wrapError(err, "prune update jobs")
}

// GetUpdateProgress 获取更新进度
func (s *Store) GetUpdateProgress(ctx context.Context, updateJobID string) (StockV2UpdateProgress, error) {
	query := `
		SELECT update_job_id, current_batch, current_batch_progress,
		       current_symbol, error_count, last_error, updated_at
		FROM stockv2_update_progress
		WHERE update_job_id = ?
	`

	row := s.db.QueryRowContext(ctx, query, updateJobID)

	var progress StockV2UpdateProgress
	err := row.Scan(
		&progress.UpdateJobID,
		&progress.CurrentBatch,
		&progress.CurrentBatchProgress,
		&progress.CurrentSymbol,
		&progress.ErrorCount,
		&progress.LastError,
		&progress.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 如果没有进度记录，返回默认值
			return StockV2UpdateProgress{
				UpdateJobID: updateJobID,
				UpdatedAt:   time.Now(),
			}, nil
		}
		return StockV2UpdateProgress{}, wrapError(err, "get update progress")
	}

	return progress, nil
}

// UpdateUpdateProgress 更新进度
func (s *Store) UpdateUpdateProgress(ctx context.Context, progress StockV2UpdateProgress) error {
	query := `
		INSERT OR REPLACE INTO stockv2_update_progress (
			update_job_id, current_batch, current_batch_progress,
			current_symbol, error_count, last_error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	progress.UpdatedAt = time.Now()

	_, err := s.db.ExecContext(ctx, query,
		progress.UpdateJobID,
		progress.CurrentBatch,
		progress.CurrentBatchProgress,
		progress.CurrentSymbol,
		progress.ErrorCount,
		progress.LastError,
		progress.UpdatedAt,
	)

	return wrapError(err, "update update progress")
}

// CreateOrUpdateSettings 创建或更新配置
func (s *Store) CreateOrUpdateSettings(ctx context.Context, settings StockV2Settings) error {
	query := `
		INSERT OR REPLACE INTO stockv2_settings (
			id, auto_update_enabled, update_interval_sec, proxy_enabled,
			proxy_type, proxy_host, proxy_port, last_scheduled_update,
			daily_bars_auto_enabled, daily_bars_last_run,
			financial_juice_enabled, financial_juice_endpoint, financial_juice_cookie, base_profile_auto_maintain_enabled,
			base_profile_maintain_interval_seconds, base_profile_deep_update_batch_size,
			base_profile_deep_update_ai_budget, base_profile_deep_update_rate_limit_ms, base_profile_last_maintain_at,
			base_profile_next_maintain_at, base_profile_last_maintain_result,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	now := time.Now()
	if settings.CreatedAt.IsZero() {
		settings.CreatedAt = now
	}
	settings.UpdatedAt = now

	var dailyBarsLastRun any
	if !settings.DailyBarsLastRun.IsZero() {
		dailyBarsLastRun = settings.DailyBarsLastRun
	}
	if settings.BaseProfileMaintainIntervalSeconds <= 0 {
		settings.BaseProfileMaintainIntervalSeconds = 86400
	}
	settings.BaseProfileDeepUpdateBatchSize = normalizeStockProfileDeepUpdateBatchSize(settings.BaseProfileDeepUpdateBatchSize)
	settings.BaseProfileDeepUpdateAIBudget = normalizeStockProfileDeepUpdateAIBudget(settings.BaseProfileDeepUpdateAIBudget)
	settings.BaseProfileDeepUpdateRateLimitMs = normalizeStockProfileDeepUpdateRateLimitMs(settings.BaseProfileDeepUpdateRateLimitMs)

	_, err := s.db.ExecContext(ctx, query,
		settings.ID,
		settings.AutoUpdateEnabled,
		settings.UpdateIntervalSec,
		settings.ProxyEnabled,
		settings.ProxyType,
		settings.ProxyHost,
		settings.ProxyPort,
		settings.LastScheduledUpdate,
		settings.DailyBarsAutoEnabled,
		dailyBarsLastRun,
		settings.FinancialJuiceEnabled,
		nullableString(settings.FinancialJuiceEndpoint),
		nullableString(settings.FinancialJuiceCookie),
		settings.BaseProfileAutoMaintainEnabled,
		settings.BaseProfileMaintainIntervalSeconds,
		settings.BaseProfileDeepUpdateBatchSize,
		settings.BaseProfileDeepUpdateAIBudget,
		settings.BaseProfileDeepUpdateRateLimitMs,
		nullableTime(settings.BaseProfileLastMaintainAt),
		nullableTime(settings.BaseProfileNextMaintainAt),
		nullableString(settings.BaseProfileLastMaintainResult),
		settings.CreatedAt,
		settings.UpdatedAt,
	)

	return wrapError(err, "create or update settings")
}

// nullTimeDefault 返回 NullTime 的值，如果无效则返回 fallback
func nullTimeDefault(nt sql.NullTime, fallback time.Time) time.Time {
	if nt.Valid {
		return nt.Time
	}
	return fallback
}

// scanUpdateJob 从 sql.Row / sql.Rows 扫描一个 UpdateJob
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanUpdateJob(row rowScanner) (StockV2UpdateJob, error) {
	var job StockV2UpdateJob
	var startAt, endAt sql.NullTime
	var failedItemsJSON string

	err := row.Scan(
		&job.ID,
		&job.TriggerType,
		&job.TriggerSource,
		&job.Status,
		&job.TotalCount,
		&job.ProcessedCount,
		&job.SuccessCount,
		&job.FailedCount,
		&failedItemsJSON,
		&startAt,
		&endAt,
		&job.ErrorMessage,
		&job.CreatedAt,
	)
	if err != nil {
		return job, err
	}

	job.StartAt = nullTimeDefault(startAt, job.CreatedAt)
	job.EndAt = nullTimeDefault(endAt, job.CreatedAt)

	// 解析失败详情 JSON
	if failedItemsJSON != "" && failedItemsJSON != "[]" {
		_ = json.Unmarshal([]byte(failedItemsJSON), &job.FailedItems)
	}

	return job, nil
}

// GetSettings 获取配置
func (s *Store) GetSettings(ctx context.Context) (StockV2Settings, error) {
	query := `
		SELECT id, auto_update_enabled, update_interval_sec, proxy_enabled,
		       COALESCE(proxy_type,''), COALESCE(proxy_host,''), COALESCE(proxy_port, 0), last_scheduled_update,
		       COALESCE(daily_bars_auto_enabled, 0), daily_bars_last_run,
		       COALESCE(financial_juice_enabled, 0), COALESCE(financial_juice_endpoint, ''), COALESCE(financial_juice_cookie, ''),
		       COALESCE(base_profile_auto_maintain_enabled, 0),
		       COALESCE(base_profile_maintain_interval_seconds, 86400),
		       COALESCE(base_profile_deep_update_batch_size, 12),
		       COALESCE(base_profile_deep_update_ai_budget, 2),
		       COALESCE(base_profile_deep_update_rate_limit_ms, 1500),
		       base_profile_last_maintain_at, base_profile_next_maintain_at,
		       COALESCE(base_profile_last_maintain_result, ''),
		       created_at, updated_at
		FROM stockv2_settings
		LIMIT 1
	`

	row := s.db.QueryRowContext(ctx, query)

	var settings StockV2Settings
	var lastScheduledUpdate sql.NullTime
	var dailyBarsLastRun sql.NullTime
	var baseProfileLastMaintainAt sql.NullTime
	var baseProfileNextMaintainAt sql.NullTime
	err := row.Scan(
		&settings.ID,
		&settings.AutoUpdateEnabled,
		&settings.UpdateIntervalSec,
		&settings.ProxyEnabled,
		&settings.ProxyType,
		&settings.ProxyHost,
		&settings.ProxyPort,
		&lastScheduledUpdate,
		&settings.DailyBarsAutoEnabled,
		&dailyBarsLastRun,
		&settings.FinancialJuiceEnabled,
		&settings.FinancialJuiceEndpoint,
		&settings.FinancialJuiceCookie,
		&settings.BaseProfileAutoMaintainEnabled,
		&settings.BaseProfileMaintainIntervalSeconds,
		&settings.BaseProfileDeepUpdateBatchSize,
		&settings.BaseProfileDeepUpdateAIBudget,
		&settings.BaseProfileDeepUpdateRateLimitMs,
		&baseProfileLastMaintainAt,
		&baseProfileNextMaintainAt,
		&settings.BaseProfileLastMaintainResult,
		&settings.CreatedAt,
		&settings.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 如果没有配置记录，返回默认配置
			return StockV2Settings{
				ID:                                 "1",
				AutoUpdateEnabled:                  false,
				UpdateIntervalSec:                  3600,
				ProxyEnabled:                       false,
				ProxyType:                          "http",
				ProxyHost:                          "",
				ProxyPort:                          8080,
				BaseProfileMaintainIntervalSeconds: 86400,
				BaseProfileDeepUpdateBatchSize:     defaultStockProfileDeepUpdateBatchSize,
				BaseProfileDeepUpdateAIBudget:      defaultStockProfileDeepUpdateAIBudget,
				BaseProfileDeepUpdateRateLimitMs:   defaultStockProfileDeepUpdateRateLimitMs,
				CreatedAt:                          time.Now(),
				UpdatedAt:                          time.Now(),
			}, nil
		}
		return StockV2Settings{}, wrapError(err, "get settings")
	}

	settings.LastScheduledUpdate = nullTimeDefault(lastScheduledUpdate, settings.CreatedAt)
	if dailyBarsLastRun.Valid {
		settings.DailyBarsLastRun = dailyBarsLastRun.Time
	}
	if baseProfileLastMaintainAt.Valid {
		settings.BaseProfileLastMaintainAt = baseProfileLastMaintainAt.Time
	}
	if baseProfileNextMaintainAt.Valid {
		settings.BaseProfileNextMaintainAt = baseProfileNextMaintainAt.Time
	}
	if settings.BaseProfileMaintainIntervalSeconds <= 0 {
		settings.BaseProfileMaintainIntervalSeconds = 86400
	}
	settings.BaseProfileDeepUpdateBatchSize = normalizeStockProfileDeepUpdateBatchSize(settings.BaseProfileDeepUpdateBatchSize)
	settings.BaseProfileDeepUpdateAIBudget = normalizeStockProfileDeepUpdateAIBudget(settings.BaseProfileDeepUpdateAIBudget)
	settings.BaseProfileDeepUpdateRateLimitMs = normalizeStockProfileDeepUpdateRateLimitMs(settings.BaseProfileDeepUpdateRateLimitMs)
	settings.FinancialJuiceCookieSet = strings.TrimSpace(settings.FinancialJuiceCookie) != "" || financialJuiceEndpointHasCredential(settings.FinancialJuiceEndpoint)

	return settings, nil
}
