-- 股票系统 V2 数据库初始化脚本
-- 创建 V2 专用数据表，完全独立于 V1 系统

-- 创建股票主数据表
CREATE TABLE IF NOT EXISTS stockv2_instruments (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL UNIQUE,
    market TEXT NOT NULL,
    name TEXT,
    industry TEXT,
    sector TEXT,
    concepts TEXT, -- JSON数组存储概念信息
    list_date TEXT,
    delist_date TEXT,
    status TEXT DEFAULT 'active', -- active, delisted, suspended
    last_update_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,

    -- 创建索引
    INDEX idx_stockv2_instruments_symbol (symbol),
    INDEX idx_stockv2_instruments_market (market),
    INDEX idx_stockv2_instruments_industry (industry),
    INDEX idx_stockv2_instruments_status (status)
);

-- 创建投资组合表
CREATE TABLE IF NOT EXISTS stockv2_portfolios (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    cash REAL NOT NULL DEFAULT 0.0,
    risk_level TEXT DEFAULT 'medium', -- low, medium, high
    max_single_position_pct REAL DEFAULT 20.0,
    max_drawdown_pct REAL DEFAULT 30.0,
    allow_buy INTEGER DEFAULT 1,
    allow_add INTEGER DEFAULT 1,
    allow_reduce INTEGER DEFAULT 1,
    allow_sell INTEGER DEFAULT 1,
    notes TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,

    -- 创建索引
    INDEX idx_stockv2_portfolios_name (name),
    INDEX idx_stockv2_portfolios_risk_level (risk_level)
);

-- 创建持仓记录表
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
    last_price_at TEXT,
    tradable_status TEXT DEFAULT 'unknown',
    market_value REAL DEFAULT 0.0,
    pnl REAL DEFAULT 0.0,
    position_pct REAL DEFAULT 0.0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,

    -- 外键约束
    FOREIGN KEY (portfolio_id) REFERENCES stockv2_portfolios(id) ON DELETE CASCADE,

    -- 创建索引
    INDEX idx_stockv2_holdings_portfolio_id (portfolio_id),
    INDEX idx_stockv2_holdings_symbol (symbol),
    INDEX idx_stockv2_holdings_market (market)
);

-- 创建更新任务记录表
CREATE TABLE IF NOT EXISTS stockv2_update_jobs (
    id TEXT PRIMARY KEY,
    trigger_type TEXT NOT NULL, -- manual, scheduled
    trigger_source TEXT,
    status TEXT NOT NULL, -- running, completed, failed, cancelled
    total_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    start_at TEXT,
    end_at TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,

    -- 创建索引
    INDEX idx_stockv2_update_jobs_status (status),
    INDEX idx_stockv2_update_jobs_trigger_type (trigger_type),
    INDEX idx_stockv2_update_jobs_created_at (created_at)
);

-- 创建 V2 配置表
CREATE TABLE IF NOT EXISTS stockv2_settings (
    id TEXT PRIMARY KEY,
    auto_update_enabled INTEGER DEFAULT 0,
    update_interval_sec INTEGER DEFAULT 3600,
    proxy_enabled INTEGER DEFAULT 0,
    proxy_type TEXT,
    proxy_host TEXT,
    proxy_port INTEGER,
    last_scheduled_update TEXT,
    financial_juice_enabled INTEGER DEFAULT 0,
    financial_juice_cookie TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 初始化 V2 设置表数据
INSERT OR IGNORE INTO stockv2_settings (id, auto_update_enabled, update_interval_sec, created_at, updated_at)
VALUES ('1', 0, 3600, datetime('now'), datetime('now'));

-- 创建更新进度临时表（用于存储实时更新状态）
CREATE TABLE IF NOT EXISTS stockv2_update_progress (
    update_job_id TEXT PRIMARY KEY,
    current_batch INTEGER DEFAULT 0,
    current_batch_progress INTEGER DEFAULT 0,
    current_symbol TEXT,
    error_count INTEGER DEFAULT 0,
    last_error TEXT,
    updated_at TEXT NOT NULL,

    FOREIGN KEY (update_job_id) REFERENCES stockv2_update_jobs(id) ON DELETE CASCADE
);

-- 创建触发器：自动更新 updated_at 字段
CREATE TRIGGER IF NOT EXISTS trigger_stockv2_instruments_updated_at
    AFTER UPDATE ON stockv2_instruments
    FOR EACH ROW
BEGIN
    UPDATE stockv2_instruments
    SET updated_at = datetime('now')
    WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trigger_stockv2_portfolios_updated_at
    AFTER UPDATE ON stockv2_portfolios
    FOR EACH ROW
BEGIN
    UPDATE stockv2_portfolios
    SET updated_at = datetime('now')
    WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trigger_stockv2_holdings_updated_at
    AFTER UPDATE ON stockv2_holdings
    FOR EACH ROW
BEGIN
    UPDATE stockv2_holdings
    SET updated_at = datetime('now')
    WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trigger_stockv2_settings_updated_at
    AFTER UPDATE ON stockv2_settings
    FOR EACH ROW
BEGIN
    UPDATE stockv2_settings
    SET updated_at = datetime('now')
    WHERE id = NEW.id;
END;

-- 创建视图：活跃股票概览
CREATE VIEW IF NOT EXISTS v_stockv2_active_instruments AS
SELECT
    id,
    symbol,
    market,
    name,
    industry,
    sector,
    json_extract(concepts, '$') as concepts,
    list_date,
    last_update_at
FROM stockv2_instruments
WHERE status = 'active' AND delist_date IS NULL;

-- 创建视图：投资组合概览
CREATE VIEW IF NOT EXISTS v_stockv2_portfolio_summary AS
SELECT
    p.id,
    p.name,
    p.description,
    p.cash,
    p.risk_level,
    COUNT(h.id) as holding_count,
    COALESCE(SUM(h.market_value), 0) as total_value,
    (p.cash + COALESCE(SUM(h.market_value), 0)) as total_asset_value,
    (p.cash / (p.cash + COALESCE(SUM(h.market_value), 1))) * 100 as cash_pct
FROM stockv2_portfolios p
LEFT JOIN stockv2_holdings h ON p.id = h.portfolio_id
GROUP BY p.id, p.name, p.description, p.cash, p.risk_level;

-- 插入一些示例数据（可选）
INSERT OR IGNORE INTO stockv2_instruments (
    id, symbol, market, name, industry, sector, status, created_at, updated_at
) VALUES
    ('1', '000001', 'sz', '平安银行', '银行', '金融', 'active', datetime('now'), datetime('now')),
    ('2', '000002', 'sz', '万科A', '房地产', '地产', 'active', datetime('now'), datetime('now')),
    ('3', '600000', 'sh', '浦发银行', '银行', '金融', 'active', datetime('now'), datetime('now')),
    ('4', '600036', 'sh', '招商银行', '银行', '金融', 'active', datetime('now'), datetime('now'));

INSERT OR IGNORE INTO stockv2_portfolios (
    id, name, description, risk_level, cash, created_at, updated_at
) VALUES
    ('1', '我的股票组合', '示例投资组合', 'medium', 100000.0, datetime('now'), datetime('now'));

-- 创建更新历史触发器
CREATE TRIGGER IF NOT EXISTS trigger_stockv2_update_jobs_history
    AFTER INSERT ON stockv2_update_jobs
    FOR EACH ROW
BEGIN
    INSERT INTO stockv2_update_progress (
        update_job_id, current_batch, current_batch_progress, updated_at
    ) VALUES (NEW.id, 0, 0, datetime('now'));
END;

-- StockV2 embedding config and vector asset metadata.
CREATE TABLE IF NOT EXISTS stockv2_embedding_config (
    id TEXT PRIMARY KEY,
    embedding_model_id TEXT,
    enabled INTEGER NOT NULL DEFAULT 0,
    last_probe_at DATETIME,
    last_probe_status TEXT,
    last_error TEXT,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS stockv2_embedding_assets (
    id TEXT PRIMARY KEY,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    text_summary TEXT,
    model_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    embedding_protocol TEXT NOT NULL,
    embedding_dimensions INTEGER NOT NULL,
    vector_ref TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(object_type, object_id, model_id, embedding_dimensions)
);

CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_object
    ON stockv2_embedding_assets(object_type, object_id);
CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_model_status
    ON stockv2_embedding_assets(model_id, embedding_dimensions, status);
CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_status
    ON stockv2_embedding_assets(status);

INSERT OR IGNORE INTO stockv2_embedding_config (id, enabled, updated_at)
VALUES ('default', 0, datetime('now'));

-- 显示创建完成信息
SELECT '股票系统 V2 数据库初始化完成' as status;
