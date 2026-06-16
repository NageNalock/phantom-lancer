package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
)

type StockDataSource struct {
	Source              string `json:"source"`
	DisplayName         string `json:"displayName"`
	SourceType          string `json:"sourceType"`
	AuthMode            string `json:"authMode"`
	Enabled             bool   `json:"enabled"`
	Status              string `json:"status"`
	Quality             string `json:"quality"`
	LastCursor          string `json:"lastCursor,omitempty"`
	LastIngestedAt      string `json:"lastIngestedAt,omitempty"`
	NextAllowedAt       string `json:"nextAllowedAt,omitempty"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	FailureSummary      string `json:"failureSummary,omitempty"`
	RateLimitSeconds    int    `json:"rateLimitSeconds"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

type StockInstrument struct {
	Symbol      string `json:"symbol"`
	Market      string `json:"market,omitempty"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Industry    string `json:"industry,omitempty"`
	Concept     string `json:"concept,omitempty"`
	ListingDate string `json:"listingDate,omitempty"`
	Source      string `json:"source,omitempty"`
	Quality     string `json:"quality"`
	PY          string `json:"py,omitempty"`
	PYFull      string `json:"pyFull,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type StockMarketDataPoint struct {
	ID           string  `json:"id"`
	Symbol       string  `json:"symbol"`
	Market       string  `json:"market,omitempty"`
	Dataset      string  `json:"dataset"`
	DataDate     string  `json:"dataDate"`
	Open         float64 `json:"open"`
	High         float64 `json:"high"`
	Low          float64 `json:"low"`
	Close        float64 `json:"close"`
	Volume       float64 `json:"volume"`
	Amount       float64 `json:"amount"`
	PE           float64 `json:"pe"`
	PB           float64 `json:"pb"`
	TurnoverRate float64 `json:"turnoverRate"`
	NetInflow    float64 `json:"netInflow"`
	Quality      string  `json:"quality"`
	Source       string  `json:"source,omitempty"`
	RawJSON      string  `json:"rawJson,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type StockNewsItem struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceItemID string `json:"sourceItemId,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Market       string `json:"market,omitempty"`
	Title        string `json:"title"`
	Summary      string `json:"summary,omitempty"`
	Category     string `json:"category,omitempty"`
	Importance   string `json:"importance"`
	Keywords     string `json:"keywords,omitempty"`
	Quality      string `json:"quality"`
	PublishedAt  string `json:"publishedAt,omitempty"`
	DedupeKey    string `json:"dedupeKey"`
	RawPayload   string `json:"rawPayload,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type StockDataTask struct {
	ID             string `json:"id"`
	TaskType       string `json:"taskType"`
	Source         string `json:"source,omitempty"`
	Symbol         string `json:"symbol,omitempty"`
	Status         string `json:"status"`
	RequestedBy    string `json:"requestedBy"`
	InputJSON      string `json:"inputJson"`
	ResultJSON     string `json:"resultJson"`
	ProcessedCount int    `json:"processedCount"`
	FailedCount    int    `json:"failedCount"`
	FailureSummary string `json:"failureSummary,omitempty"`
	StartedAt      string `json:"startedAt,omitempty"`
	CompletedAt    string `json:"completedAt,omitempty"`
	NextRunAt      string `json:"nextRunAt,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type StockDataCoverage struct {
	Symbol        string `json:"symbol"`
	Dataset       string `json:"dataset"`
	FirstDate     string `json:"firstDate,omitempty"`
	LastDate      string `json:"lastDate,omitempty"`
	PointCount    int    `json:"pointCount"`
	LatestQuality string `json:"latestQuality"`
	LatestSource  string `json:"latestSource,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type StockDataHealthSummary struct {
	SourceCount        int    `json:"sourceCount"`
	AvailableSources   int    `json:"availableSources"`
	DegradedSources    int    `json:"degradedSources"`
	FailedSources      int    `json:"failedSources"`
	InstrumentCount    int    `json:"instrumentCount"`
	MarketPointCount   int    `json:"marketPointCount"`
	NewsItemCount      int    `json:"newsItemCount"`
	ImportantNewsCount int    `json:"importantNewsCount"`
	TaskCount          int    `json:"taskCount"`
	FailedTaskCount    int    `json:"failedTaskCount"`
	StaleQuoteCount    int    `json:"staleQuoteCount"`
	LastTaskAt         string `json:"lastTaskAt,omitempty"`
	LastNewsAt         string `json:"lastNewsAt,omitempty"`
}

type StockInstrumentSearchParams struct {
	Query           string
	Markets         []string
	Statuses        []string
	Industry        string
	Concepts        []string
	MinListingDate  string
	Quality         string
	IncludeDelisted bool
	Sort            string
	Page            int
	PageSize        int
}

type StockInstrumentSearchResult struct {
	Total    int                          `json:"total"`
	Page     int                          `json:"page"`
	PageSize int                          `json:"pageSize"`
	Items    []StockInstrument            `json:"items"`
	Snippets map[string]map[string]string `json:"snippets,omitempty"`
	FTS      bool                         `json:"fts"`
}

func (s *Store) migrateStockData(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS stock_data_sources (
  source TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT '',
  source_type TEXT NOT NULL DEFAULT 'market_data',
  auth_mode TEXT NOT NULL DEFAULT 'none',
  enabled INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'unknown',
  quality TEXT NOT NULL DEFAULT 'unknown',
  last_cursor TEXT NOT NULL DEFAULT '',
  last_ingested_at TEXT NOT NULL DEFAULT '',
  next_allowed_at TEXT NOT NULL DEFAULT '',
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  failure_summary TEXT NOT NULL DEFAULT '',
  rate_limit_seconds INTEGER NOT NULL DEFAULT 60,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_data_sources_status ON stock_data_sources(status, source_type);
CREATE TABLE IF NOT EXISTS stock_instruments (
  symbol TEXT PRIMARY KEY,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'listed',
  industry TEXT NOT NULL DEFAULT '',
  concept TEXT NOT NULL DEFAULT '',
  listing_date TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  quality TEXT NOT NULL DEFAULT 'fresh',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_instruments_status ON stock_instruments(status, market);
CREATE TABLE IF NOT EXISTS stock_market_data_points (
  id TEXT PRIMARY KEY,
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  dataset TEXT NOT NULL,
  data_date TEXT NOT NULL,
  open REAL NOT NULL DEFAULT 0,
  high REAL NOT NULL DEFAULT 0,
  low REAL NOT NULL DEFAULT 0,
  close REAL NOT NULL DEFAULT 0,
  volume REAL NOT NULL DEFAULT 0,
  amount REAL NOT NULL DEFAULT 0,
  pe REAL NOT NULL DEFAULT 0,
  pb REAL NOT NULL DEFAULT 0,
  turnover_rate REAL NOT NULL DEFAULT 0,
  net_inflow REAL NOT NULL DEFAULT 0,
  quality TEXT NOT NULL DEFAULT 'fresh',
  source TEXT NOT NULL DEFAULT '',
  raw_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(symbol, dataset, data_date, source)
);
CREATE INDEX IF NOT EXISTS idx_stock_market_points_symbol ON stock_market_data_points(symbol, dataset, data_date DESC);
CREATE INDEX IF NOT EXISTS idx_stock_market_points_dataset ON stock_market_data_points(dataset, data_date DESC);
CREATE TABLE IF NOT EXISTS stock_news_items (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  source_item_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  market TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  importance TEXT NOT NULL DEFAULT 'normal',
  keywords TEXT NOT NULL DEFAULT '',
  quality TEXT NOT NULL DEFAULT 'fresh',
  published_at TEXT NOT NULL DEFAULT '',
  dedupe_key TEXT NOT NULL,
  raw_payload TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(dedupe_key)
);
CREATE INDEX IF NOT EXISTS idx_stock_news_items_symbol ON stock_news_items(symbol, published_at DESC, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_news_items_source ON stock_news_items(source, published_at DESC, created_at DESC);
CREATE TABLE IF NOT EXISTS stock_data_tasks (
  id TEXT PRIMARY KEY,
  task_type TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'completed',
  requested_by TEXT NOT NULL DEFAULT 'system',
  input_json TEXT NOT NULL DEFAULT '{}',
  result_json TEXT NOT NULL DEFAULT '{}',
  processed_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  failure_summary TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  next_run_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_data_tasks_created ON stock_data_tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_data_tasks_status ON stock_data_tasks(status, task_type);
`)
	if err != nil {
		return err
	}
	// —— v2 增量迁移：stock_instruments 加拼音列 + 普通索引 ——
	// SQLite ALTER TABLE ... ADD COLUMN 如果列已存在会报错，忽略即可
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE stock_instruments ADD COLUMN py TEXT NOT NULL DEFAULT ''`); err != nil {
		_ = err
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE stock_instruments ADD COLUMN py_full TEXT NOT NULL DEFAULT ''`); err != nil {
		_ = err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stock_instruments_symbol_name ON stock_instruments(symbol, name)`); err != nil {
		return err
	}
	if FTS5Available() {
		if _, err := s.db.ExecContext(ctx, stockInstrumentFTSSQL); err != nil {
			return fmt.Errorf("stock migrate: create instrument fts5: %w", err)
		}
	}
	if err := s.repairLegacyStockInstrumentStatuses(ctx); err != nil {
		return err
	}
	return s.seedStockDataSources(ctx)
}

func (s *Store) repairLegacyStockInstrumentStatuses(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE stock_instruments SET status = 'listed', updated_at = ? WHERE status = 'tradable'`, now())
	if err != nil {
		return fmt.Errorf("stock migrate: repair legacy instrument status: %w", err)
	}
	return nil
}

const stockInstrumentFTSSQL = `
CREATE VIRTUAL TABLE IF NOT EXISTS stock_instruments_fts USING fts5(
  symbol, name, py, py_full, industry, concept,
  market UNINDEXED, status UNINDEXED, quality UNINDEXED,
  tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER IF NOT EXISTS stock_instruments_ai AFTER INSERT ON stock_instruments BEGIN
  INSERT INTO stock_instruments_fts(rowid, symbol, name, py, py_full, industry, concept, market, status, quality)
  VALUES (new.rowid, new.symbol, new.name, new.py, new.py_full, new.industry, new.concept, new.market, new.status, new.quality);
END;
CREATE TRIGGER IF NOT EXISTS stock_instruments_au AFTER UPDATE ON stock_instruments BEGIN
  DELETE FROM stock_instruments_fts WHERE rowid = old.rowid;
  INSERT INTO stock_instruments_fts(rowid, symbol, name, py, py_full, industry, concept, market, status, quality)
  VALUES (new.rowid, new.symbol, new.name, new.py, new.py_full, new.industry, new.concept, new.market, new.status, new.quality);
END;
CREATE TRIGGER IF NOT EXISTS stock_instruments_ad AFTER DELETE ON stock_instruments BEGIN
  DELETE FROM stock_instruments_fts WHERE rowid = old.rowid;
END;
INSERT INTO stock_instruments_fts(rowid, symbol, name, py, py_full, industry, concept, market, status, quality)
SELECT rowid, symbol, name, py, py_full, industry, concept, market, status, quality
FROM stock_instruments
WHERE rowid NOT IN (SELECT rowid FROM stock_instruments_fts);
`

func (s *Store) seedStockDataSources(ctx context.Context) error {
	defaults := []StockDataSource{
		{
			Source:           "manual_seed",
			DisplayName:      "Manual Seed",
			SourceType:       "market_data",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "available",
			Quality:          "fresh",
			RateLimitSeconds: 1,
		},
		{
			Source:           "a_stock_data",
			DisplayName:      "a-stock-data Skill",
			SourceType:       "skill",
			AuthMode:         "user_config",
			Enabled:          true,
			Status:           "unknown",
			Quality:          "unknown",
			FailureSummary:   "默认股票数据 Skill 能力登记，真实可用性由后续探测任务刷新",
			RateLimitSeconds: 60,
		},
		{
			Source:           "eastmoney_public_quote",
			DisplayName:      "Eastmoney Public Quote",
			SourceType:       "market_data",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "available",
			Quality:          "fresh",
			RateLimitSeconds: 30,
		},
		{
			Source:           "sina_public_quote",
			DisplayName:      "Sina Public Quote",
			SourceType:       "market_data",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "available",
			Quality:          "fresh",
			RateLimitSeconds: 30,
		},
		{
			Source:           "jin10_search",
			DisplayName:      "Jin10 Search Adapter",
			SourceType:       "search",
			AuthMode:         "user_config",
			Enabled:          true,
			Status:           "unknown",
			Quality:          "unknown",
			FailureSummary:   "临时搜索适配器登记，不保存 endpoint、cookie 或 token",
			RateLimitSeconds: 60,
		},
		{
			Source:           "eastmoney_universe",
			DisplayName:      "Eastmoney A-share Universe",
			SourceType:       "market_data",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "available",
			Quality:          "unknown",
			FailureSummary:   "A 股全量主数据主源，后台维护任务按频率自动刷新",
			RateLimitSeconds: 20 * 60 * 60,
		},
		{
			Source:           "sina_universe",
			DisplayName:      "Sina A-share Universe Fallback",
			SourceType:       "market_data",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "unknown",
			Quality:          "unknown",
			FailureSummary:   "A 股主数据兜底源，仅在主源失败时使用，不执行退市软标",
			RateLimitSeconds: 20 * 60 * 60,
		},
		{
			Source:           "financial_report_feed",
			DisplayName:      "Financial Report Feed",
			SourceType:       "report",
			AuthMode:         "user_config",
			Enabled:          true,
			Status:           "unknown",
			Quality:          "unknown",
			FailureSummary:   "财报/公告采集源登记，不保存私有 endpoint、cookie 或 token",
			RateLimitSeconds: 6 * 60 * 60,
		},
		{
			Source:           "research_report_feed",
			DisplayName:      "Research Report Feed",
			SourceType:       "report",
			AuthMode:         "user_config",
			Enabled:          true,
			Status:           "unknown",
			Quality:          "unknown",
			FailureSummary:   "研报采集源登记，不保存私有 endpoint、cookie 或 token",
			RateLimitSeconds: 6 * 60 * 60,
		},
		{
			Source:           "local_data_scheduler",
			DisplayName:      "Local Data Scheduler",
			SourceType:       "scheduler",
			AuthMode:         "none",
			Enabled:          true,
			Status:           "available",
			Quality:          "fresh",
			RateLimitSeconds: 30 * 60,
		},
	}
	for _, src := range defaults {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM stock_data_sources WHERE source = ?`, normalizeStockSource(src.Source)).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if _, err := s.UpsertStockDataSource(ctx, src); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertStockDataSource(ctx context.Context, src StockDataSource) (StockDataSource, error) {
	src.Source = normalizeStockSource(src.Source)
	if src.Source == "" {
		return StockDataSource{}, errors.New("source is required")
	}
	src.DisplayName = defaultString(strings.TrimSpace(src.DisplayName), src.Source)
	src.SourceType = defaultString(strings.TrimSpace(src.SourceType), "market_data")
	src.AuthMode = defaultString(strings.TrimSpace(src.AuthMode), "none")
	if src.AuthMode == "disabled" {
		src.Enabled = false
	}
	src.Status = defaultString(strings.TrimSpace(src.Status), "unknown")
	src.Quality = defaultString(strings.TrimSpace(src.Quality), "unknown")
	if src.RateLimitSeconds <= 0 {
		src.RateLimitSeconds = 60
	}
	ts := now()
	existing, err := s.GetStockDataSource(ctx, src.Source)
	if err == nil {
		src.CreatedAt = existing.CreatedAt
		src.UpdatedAt = ts
		if src.LastCursor == "" {
			src.LastCursor = existing.LastCursor
		}
		if src.LastIngestedAt == "" {
			src.LastIngestedAt = existing.LastIngestedAt
		}
		if src.NextAllowedAt == "" {
			src.NextAllowedAt = existing.NextAllowedAt
		}
		if src.FailureSummary == "" {
			src.FailureSummary = existing.FailureSummary
		}
		if src.ConsecutiveFailures == 0 {
			src.ConsecutiveFailures = existing.ConsecutiveFailures
		}
	} else if err == ErrNotFound {
		src.CreatedAt = ts
		src.UpdatedAt = ts
	} else {
		return StockDataSource{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_data_sources (source, display_name, source_type, auth_mode, enabled, status, quality, last_cursor, last_ingested_at, next_allowed_at, consecutive_failures, failure_summary, rate_limit_seconds, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source) DO UPDATE SET display_name = excluded.display_name, source_type = excluded.source_type, auth_mode = excluded.auth_mode, enabled = excluded.enabled, status = excluded.status, quality = excluded.quality, last_cursor = excluded.last_cursor, last_ingested_at = excluded.last_ingested_at, next_allowed_at = excluded.next_allowed_at, consecutive_failures = excluded.consecutive_failures, failure_summary = excluded.failure_summary, rate_limit_seconds = excluded.rate_limit_seconds, updated_at = excluded.updated_at`,
		src.Source, src.DisplayName, src.SourceType, src.AuthMode, boolInt(src.Enabled), src.Status, src.Quality, src.LastCursor, src.LastIngestedAt, src.NextAllowedAt, src.ConsecutiveFailures, src.FailureSummary, src.RateLimitSeconds, src.CreatedAt, src.UpdatedAt)
	if err != nil {
		return StockDataSource{}, err
	}
	return src, nil
}

func (s *Store) GetStockDataSource(ctx context.Context, source string) (StockDataSource, error) {
	item, err := scanStockDataSource(s.db.QueryRowContext(ctx, `SELECT source, display_name, source_type, auth_mode, enabled, status, quality, last_cursor, last_ingested_at, next_allowed_at, consecutive_failures, failure_summary, rate_limit_seconds, created_at, updated_at FROM stock_data_sources WHERE source = ?`, normalizeStockSource(source)))
	if err == sql.ErrNoRows {
		return StockDataSource{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockDataSources(ctx context.Context) ([]StockDataSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, display_name, source_type, auth_mode, enabled, status, quality, last_cursor, last_ingested_at, next_allowed_at, consecutive_failures, failure_summary, rate_limit_seconds, created_at, updated_at FROM stock_data_sources ORDER BY source_type, source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockDataSource
	for rows.Next() {
		item, err := scanStockDataSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateStockDataSourceHealth(ctx context.Context, src StockDataSource) (StockDataSource, error) {
	existing, err := s.GetStockDataSource(ctx, src.Source)
	if err != nil {
		return StockDataSource{}, err
	}
	ts := now()
	if src.DisplayName == "" {
		src.DisplayName = existing.DisplayName
	}
	if src.SourceType == "" {
		src.SourceType = existing.SourceType
	}
	if src.AuthMode == "" {
		src.AuthMode = existing.AuthMode
	}
	if src.LastCursor == "" {
		src.LastCursor = existing.LastCursor
	}
	if src.LastIngestedAt == "" {
		src.LastIngestedAt = existing.LastIngestedAt
	}
	if src.NextAllowedAt == "" {
		src.NextAllowedAt = existing.NextAllowedAt
	}
	src.Enabled = existing.Enabled
	src.RateLimitSeconds = existing.RateLimitSeconds
	src.CreatedAt = existing.CreatedAt
	src.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `UPDATE stock_data_sources SET status = ?, quality = ?, last_cursor = ?, last_ingested_at = ?, next_allowed_at = ?, consecutive_failures = ?, failure_summary = ?, updated_at = ? WHERE source = ?`,
		defaultString(src.Status, existing.Status), defaultString(src.Quality, existing.Quality), src.LastCursor, src.LastIngestedAt, src.NextAllowedAt, src.ConsecutiveFailures, src.FailureSummary, ts, existing.Source)
	if err != nil {
		return StockDataSource{}, err
	}
	return s.GetStockDataSource(ctx, existing.Source)
}

func (s *Store) UpsertStockInstrument(ctx context.Context, item StockInstrument) (StockInstrument, error) {
	var inferredMarket string
	item.Symbol, inferredMarket = normalizeStockSymbolAndMarket(item.Symbol)
	if item.Symbol == "" {
		return StockInstrument{}, errors.New("symbol is required")
	}
	item.Market = normalizeStockMarket(defaultString(item.Market, inferredMarket))
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		item.Name = item.Symbol
	}
	item.Status = normalizeStockInstrumentWriteStatus(item.Status)
	item.Quality = defaultString(strings.TrimSpace(item.Quality), "fresh")
	item.Source = normalizeStockSource(item.Source)
	ts := now()
	existing, err := s.GetStockInstrument(ctx, item.Symbol)
	if err == nil {
		// —— manual_override 保护：用户手工覆盖的 name/industry/concept 永不被自动刷新覆盖 ——
		if existing.Source == "manual_override" && item.Source != "manual_override" {
			item.Name = existing.Name
			if existing.Industry != "" {
				item.Industry = existing.Industry
			}
			if existing.Concept != "" {
				item.Concept = existing.Concept
			}
		}
		item.CreatedAt = existing.CreatedAt
		item.UpdatedAt = ts
	} else if err == ErrNotFound {
		item.CreatedAt = ts
		item.UpdatedAt = ts
	} else {
		return StockInstrument{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_instruments (symbol, market, name, status, industry, concept, listing_date, source, quality, py, py_full, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol) DO UPDATE SET market = excluded.market, name = excluded.name, status = excluded.status, industry = excluded.industry, concept = excluded.concept, listing_date = excluded.listing_date, source = excluded.source, quality = excluded.quality, py = excluded.py, py_full = excluded.py_full, updated_at = excluded.updated_at`,
		item.Symbol, item.Market, item.Name, item.Status, item.Industry, item.Concept, item.ListingDate, item.Source, item.Quality, item.PY, item.PYFull, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return StockInstrument{}, err
	}
	return item, nil
}

func (s *Store) GetStockInstrument(ctx context.Context, symbol string) (StockInstrument, error) {
	item, err := scanStockInstrument(s.db.QueryRowContext(ctx, `SELECT symbol, market, name, status, industry, concept, listing_date, source, quality, py, py_full, created_at, updated_at FROM stock_instruments WHERE symbol = ?`, normalizeStockSymbol(symbol)))
	if err == sql.ErrNoRows {
		return StockInstrument{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockInstruments(ctx context.Context, limit int) ([]StockInstrument, error) {
	return s.ListStockInstrumentsFiltered(ctx, limit, true)
}

func (s *Store) ListStockInstrumentsFiltered(ctx context.Context, limit int, includeDelisted bool) ([]StockInstrument, error) {
	if limit <= 0 || limit > 10000 {
		limit = 200
	}
	query := `SELECT symbol, market, name, status, industry, concept, listing_date, source, quality, py, py_full, created_at, updated_at FROM stock_instruments`
	args := []any{}
	if !includeDelisted {
		query += ` WHERE status != 'delisted'`
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockInstrument
	for rows.Next() {
		item, err := scanStockInstrument(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SearchStockInstruments(ctx context.Context, params StockInstrumentSearchParams) (StockInstrumentSearchResult, error) {
	params = normalizeStockInstrumentSearchParams(params)
	where, args, orderArgs, orderBy, useFTS := stockInstrumentSearchSQL(params)
	countQuery := `SELECT COUNT(1) FROM stock_instruments` + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return StockInstrumentSearchResult{}, err
	}
	query := `SELECT symbol, market, name, status, industry, concept, listing_date, source, quality, py, py_full, created_at, updated_at FROM stock_instruments` + where + orderBy + ` LIMIT ? OFFSET ?`
	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, orderArgs...)
	queryArgs = append(queryArgs, params.PageSize, (params.Page-1)*params.PageSize)
	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return StockInstrumentSearchResult{}, err
	}
	defer rows.Close()
	items := []StockInstrument{}
	for rows.Next() {
		item, err := scanStockInstrument(rows)
		if err != nil {
			return StockInstrumentSearchResult{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return StockInstrumentSearchResult{}, err
	}
	return StockInstrumentSearchResult{
		Total:    total,
		Page:     params.Page,
		PageSize: params.PageSize,
		Items:    items,
		Snippets: stockInstrumentSnippets(items, params.Query),
		FTS:      useFTS,
	}, nil
}

// AllInstrumentSymbols 返回当前所有 instrument 的 symbol 集合，用于远端集合 diff。
func (s *Store) AllInstrumentSymbols(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol FROM stock_instruments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err != nil {
			return nil, err
		}
		set[sym] = true
	}
	return set, rows.Err()
}

// BatchUpsertInstruments 批量写入，返回实际 upsert 成功的行数和累计错误笔记。
// 对于 5500 只量级不使用 1000-row batch，保持行级 upsert 以复用 manual_override 保护逻辑。
func (s *Store) BatchUpsertInstruments(ctx context.Context, items []StockInstrument) (int, []string) {
	saved := 0
	notes := make([]string, 0, 4)
	for i := range items {
		if _, err := s.UpsertStockInstrument(ctx, items[i]); err != nil {
			notes = append(notes, items[i].Symbol+": "+err.Error())
			continue
		}
		saved++
	}
	return saved, notes
}

// MarkInstrumentsDelisted 把给定 symbols 的 status 改成 'delisted'（软删除，不删行）。
// 只有当前 status 还不是 delisted 的行才会被写，返回受影响行数。
func (s *Store) MarkInstrumentsDelisted(ctx context.Context, symbols []string) (int, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	ts := now()
	count := 0
	for _, sym := range symbols {
		sym = normalizeStockSymbol(sym)
		if sym == "" {
			continue
		}
		res, err := s.db.ExecContext(ctx,
			`UPDATE stock_instruments SET status = 'delisted', quality = 'stale', updated_at = ? WHERE symbol = ? AND status != 'delisted'`,
			ts, sym)
		if err != nil {
			return count, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			count += int(n)
		}
	}
	return count, nil
}

// LastTaskCompletedAt 返回指定 taskType 的最近一次 completed/degraded 任务完成时间；无则零值。
func (s *Store) LastTaskCompletedAt(ctx context.Context, taskType string) time.Time {
	var completedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT completed_at FROM stock_data_tasks WHERE task_type = ? AND status IN ('completed','degraded') ORDER BY completed_at DESC LIMIT 1`,
		taskType).Scan(&completedAt)
	if err != nil || completedAt == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, completedAt)
	return t
}

// DistinctStockIndustries 返回所有去重后的行业名（排除空串）。
func (s *Store) DistinctStockIndustries(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT industry FROM stock_instruments WHERE industry != '' ORDER BY industry`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func (s *Store) UpsertStockMarketDataPoint(ctx context.Context, point StockMarketDataPoint) (StockMarketDataPoint, bool, error) {
	var inferredMarket string
	point.Symbol, inferredMarket = normalizeStockSymbolAndMarket(point.Symbol)
	if point.Symbol == "" {
		return StockMarketDataPoint{}, false, errors.New("symbol is required")
	}
	point.Market = normalizeStockMarket(defaultString(point.Market, inferredMarket))
	point.Dataset = defaultString(strings.TrimSpace(point.Dataset), "daily_kline")
	point.DataDate = strings.TrimSpace(point.DataDate)
	if point.DataDate == "" {
		return StockMarketDataPoint{}, false, errors.New("data date is required")
	}
	point.Quality = defaultString(strings.TrimSpace(point.Quality), "fresh")
	point.Source = normalizeStockSource(point.Source)
	existing, err := s.getStockMarketDataPointByKey(ctx, point.Symbol, point.Dataset, point.DataDate, point.Source)
	created := false
	ts := now()
	if err == nil {
		point.ID = existing.ID
		point.CreatedAt = existing.CreatedAt
		point.UpdatedAt = ts
	} else if err == ErrNotFound {
		id, err := ids.New("stdp")
		if err != nil {
			return StockMarketDataPoint{}, false, err
		}
		point.ID = id
		point.CreatedAt = ts
		point.UpdatedAt = ts
		created = true
	} else {
		return StockMarketDataPoint{}, false, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_market_data_points (id, symbol, market, dataset, data_date, open, high, low, close, volume, amount, pe, pb, turnover_rate, net_inflow, quality, source, raw_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol, dataset, data_date, source) DO UPDATE SET market = excluded.market, open = excluded.open, high = excluded.high, low = excluded.low, close = excluded.close, volume = excluded.volume, amount = excluded.amount, pe = excluded.pe, pb = excluded.pb, turnover_rate = excluded.turnover_rate, net_inflow = excluded.net_inflow, quality = excluded.quality, raw_json = excluded.raw_json, updated_at = excluded.updated_at`,
		point.ID, point.Symbol, point.Market, point.Dataset, point.DataDate, point.Open, point.High, point.Low, point.Close, point.Volume, point.Amount, point.PE, point.PB, point.TurnoverRate, point.NetInflow, point.Quality, point.Source, point.RawJSON, point.CreatedAt, point.UpdatedAt)
	if err != nil {
		return StockMarketDataPoint{}, false, err
	}
	return point, created, nil
}

func (s *Store) getStockMarketDataPointByKey(ctx context.Context, symbol, dataset, dataDate, source string) (StockMarketDataPoint, error) {
	item, err := scanStockMarketDataPoint(s.db.QueryRowContext(ctx, `SELECT id, symbol, market, dataset, data_date, open, high, low, close, volume, amount, pe, pb, turnover_rate, net_inflow, quality, source, raw_json, created_at, updated_at FROM stock_market_data_points WHERE symbol = ? AND dataset = ? AND data_date = ? AND source = ?`, normalizeStockSymbol(symbol), dataset, dataDate, normalizeStockSource(source)))
	if err == sql.ErrNoRows {
		return StockMarketDataPoint{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockMarketDataPoints(ctx context.Context, symbol, dataset string, limit int) ([]StockMarketDataPoint, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT id, symbol, market, dataset, data_date, open, high, low, close, volume, amount, pe, pb, turnover_rate, net_inflow, quality, source, raw_json, created_at, updated_at FROM stock_market_data_points`
	args := []any{}
	conds := []string{}
	if symbol = normalizeStockSymbol(symbol); symbol != "" {
		conds = append(conds, "symbol = ?")
		args = append(args, symbol)
	}
	if dataset = strings.TrimSpace(dataset); dataset != "" {
		conds = append(conds, "dataset = ?")
		args = append(args, dataset)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += ` ORDER BY data_date DESC, updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockMarketDataPoint
	for rows.Next() {
		item, err := scanStockMarketDataPoint(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) StockDataCoverage(ctx context.Context, symbol string) ([]StockDataCoverage, error) {
	query := `SELECT symbol, dataset, MIN(data_date), MAX(data_date), COUNT(1), COALESCE(MAX(updated_at), '') FROM stock_market_data_points`
	args := []any{}
	if symbol = normalizeStockSymbol(symbol); symbol != "" {
		query += ` WHERE symbol = ?`
		args = append(args, symbol)
	}
	query += ` GROUP BY symbol, dataset ORDER BY symbol, dataset`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockDataCoverage
	for rows.Next() {
		var item StockDataCoverage
		if err := rows.Scan(&item.Symbol, &item.Dataset, &item.FirstDate, &item.LastDate, &item.PointCount, &item.UpdatedAt); err != nil {
			return nil, err
		}
		latest, err := scanStockMarketDataPoint(s.db.QueryRowContext(ctx, `SELECT id, symbol, market, dataset, data_date, open, high, low, close, volume, amount, pe, pb, turnover_rate, net_inflow, quality, source, raw_json, created_at, updated_at FROM stock_market_data_points WHERE symbol = ? AND dataset = ? ORDER BY data_date DESC, updated_at DESC LIMIT 1`, item.Symbol, item.Dataset))
		if err == nil {
			item.LatestQuality = latest.Quality
			item.LatestSource = latest.Source
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertStockNewsItem(ctx context.Context, item StockNewsItem) (StockNewsItem, bool, error) {
	item.Source = normalizeStockSource(item.Source)
	if item.Source == "" {
		return StockNewsItem{}, false, errors.New("source is required")
	}
	var inferredMarket string
	item.Symbol, inferredMarket = normalizeStockSymbolAndMarket(item.Symbol)
	item.Market = normalizeStockMarket(defaultString(item.Market, inferredMarket))
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		return StockNewsItem{}, false, errors.New("title is required")
	}
	item.Importance = defaultString(strings.TrimSpace(item.Importance), "normal")
	item.Quality = defaultString(strings.TrimSpace(item.Quality), "fresh")
	item.DedupeKey = strings.TrimSpace(item.DedupeKey)
	if item.DedupeKey == "" {
		item.DedupeKey = newsDedupeKey(item)
	}
	existing, err := s.getStockNewsItemByDedupe(ctx, item.DedupeKey)
	created := false
	ts := now()
	if err == nil {
		item.ID = existing.ID
		item.CreatedAt = existing.CreatedAt
		item.UpdatedAt = ts
	} else if err == ErrNotFound {
		id, err := ids.New("stnw")
		if err != nil {
			return StockNewsItem{}, false, err
		}
		item.ID = id
		item.CreatedAt = ts
		item.UpdatedAt = ts
		created = true
	} else {
		return StockNewsItem{}, false, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_news_items (id, source, source_item_id, symbol, market, title, summary, category, importance, keywords, quality, published_at, dedupe_key, raw_payload, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(dedupe_key) DO UPDATE SET source_item_id = excluded.source_item_id, symbol = excluded.symbol, market = excluded.market, title = excluded.title, summary = excluded.summary, category = excluded.category, importance = excluded.importance, keywords = excluded.keywords, quality = excluded.quality, published_at = excluded.published_at, raw_payload = excluded.raw_payload, updated_at = excluded.updated_at`,
		item.ID, item.Source, item.SourceItemID, item.Symbol, item.Market, item.Title, item.Summary, item.Category, item.Importance, item.Keywords, item.Quality, item.PublishedAt, item.DedupeKey, item.RawPayload, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return StockNewsItem{}, false, err
	}
	return item, created, nil
}

func (s *Store) getStockNewsItemByDedupe(ctx context.Context, dedupeKey string) (StockNewsItem, error) {
	item, err := scanStockNewsItem(s.db.QueryRowContext(ctx, `SELECT id, source, source_item_id, symbol, market, title, summary, category, importance, keywords, quality, published_at, dedupe_key, raw_payload, created_at, updated_at FROM stock_news_items WHERE dedupe_key = ?`, dedupeKey))
	if err == sql.ErrNoRows {
		return StockNewsItem{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockNewsItems(ctx context.Context, source, symbol string, limit int) ([]StockNewsItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, source, source_item_id, symbol, market, title, summary, category, importance, keywords, quality, published_at, dedupe_key, raw_payload, created_at, updated_at FROM stock_news_items`
	args := []any{}
	conds := []string{}
	if source = normalizeStockSource(source); source != "" {
		conds = append(conds, "source = ?")
		args = append(args, source)
	}
	if symbol = normalizeStockSymbol(symbol); symbol != "" {
		conds = append(conds, "symbol = ?")
		args = append(args, symbol)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += ` ORDER BY COALESCE(NULLIF(published_at, ''), created_at) DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockNewsItem
	for rows.Next() {
		item, err := scanStockNewsItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockDataTask(ctx context.Context, task StockDataTask) (StockDataTask, error) {
	id, err := ids.New("stdt")
	if err != nil {
		return StockDataTask{}, err
	}
	ts := now()
	task.ID = id
	task.TaskType = strings.TrimSpace(task.TaskType)
	if task.TaskType == "" {
		return StockDataTask{}, errors.New("task type is required")
	}
	task.Source = normalizeStockSource(task.Source)
	task.Symbol = normalizeStockSymbol(task.Symbol)
	task.Status = defaultString(strings.TrimSpace(task.Status), "completed")
	task.RequestedBy = defaultString(strings.TrimSpace(task.RequestedBy), "system")
	task.InputJSON = defaultString(task.InputJSON, "{}")
	task.ResultJSON = defaultString(task.ResultJSON, "{}")
	if task.StartedAt == "" {
		task.StartedAt = ts
	}
	if task.CompletedAt == "" && (task.Status == "completed" || task.Status == "failed" || task.Status == "degraded" || task.Status == "blocked") {
		task.CompletedAt = ts
	}
	task.CreatedAt = ts
	task.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_data_tasks (id, task_type, source, symbol, status, requested_by, input_json, result_json, processed_count, failed_count, failure_summary, started_at, completed_at, next_run_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.TaskType, task.Source, task.Symbol, task.Status, task.RequestedBy, task.InputJSON, task.ResultJSON, task.ProcessedCount, task.FailedCount, task.FailureSummary, task.StartedAt, task.CompletedAt, task.NextRunAt, task.CreatedAt, task.UpdatedAt)
	return task, err
}

func (s *Store) ListStockDataTasks(ctx context.Context, limit int) ([]StockDataTask, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_type, source, symbol, status, requested_by, input_json, result_json, processed_count, failed_count, failure_summary, started_at, completed_at, next_run_at, created_at, updated_at FROM stock_data_tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockDataTask
	for rows.Next() {
		item, err := scanStockDataTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) StockDataHealthSummary(ctx context.Context) (StockDataHealthSummary, error) {
	var summary StockDataHealthSummary
	row := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(1) FROM stock_data_sources),
  (SELECT COUNT(1) FROM stock_data_sources WHERE status = 'available'),
  (SELECT COUNT(1) FROM stock_data_sources WHERE status IN ('degraded', 'rate_limited', 'auth_required')),
  (SELECT COUNT(1) FROM stock_data_sources WHERE status IN ('failed', 'disabled')),
  (SELECT COUNT(1) FROM stock_instruments),
  (SELECT COUNT(1) FROM stock_market_data_points),
  (SELECT COUNT(1) FROM stock_news_items),
  (SELECT COUNT(1) FROM stock_news_items WHERE importance IN ('high', 'urgent')),
  (SELECT COUNT(1) FROM stock_data_tasks),
  (SELECT COUNT(1) FROM stock_data_tasks WHERE status = 'failed'),
  (SELECT COUNT(1) FROM stock_quotes WHERE data_freshness = 'stale'),
  COALESCE((SELECT created_at FROM stock_data_tasks ORDER BY created_at DESC LIMIT 1), ''),
  COALESCE((SELECT created_at FROM stock_news_items ORDER BY created_at DESC LIMIT 1), '')
`)
	if err := row.Scan(&summary.SourceCount, &summary.AvailableSources, &summary.DegradedSources, &summary.FailedSources, &summary.InstrumentCount, &summary.MarketPointCount, &summary.NewsItemCount, &summary.ImportantNewsCount, &summary.TaskCount, &summary.FailedTaskCount, &summary.StaleQuoteCount, &summary.LastTaskAt, &summary.LastNewsAt); err != nil {
		return StockDataHealthSummary{}, err
	}
	return summary, nil
}

func scanStockDataSource(row interface{ Scan(...any) error }) (StockDataSource, error) {
	var item StockDataSource
	var enabled int
	err := row.Scan(&item.Source, &item.DisplayName, &item.SourceType, &item.AuthMode, &enabled, &item.Status, &item.Quality, &item.LastCursor, &item.LastIngestedAt, &item.NextAllowedAt, &item.ConsecutiveFailures, &item.FailureSummary, &item.RateLimitSeconds, &item.CreatedAt, &item.UpdatedAt)
	item.Enabled = enabled == 1
	return item, err
}

func scanStockInstrument(row interface{ Scan(...any) error }) (StockInstrument, error) {
	var item StockInstrument
	err := row.Scan(&item.Symbol, &item.Market, &item.Name, &item.Status, &item.Industry, &item.Concept, &item.ListingDate, &item.Source, &item.Quality, &item.PY, &item.PYFull, &item.CreatedAt, &item.UpdatedAt)
	item.Status = normalizeStockInstrumentStatus(item.Status)
	return item, err
}

func scanStockMarketDataPoint(row interface{ Scan(...any) error }) (StockMarketDataPoint, error) {
	var item StockMarketDataPoint
	err := row.Scan(&item.ID, &item.Symbol, &item.Market, &item.Dataset, &item.DataDate, &item.Open, &item.High, &item.Low, &item.Close, &item.Volume, &item.Amount, &item.PE, &item.PB, &item.TurnoverRate, &item.NetInflow, &item.Quality, &item.Source, &item.RawJSON, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanStockNewsItem(row interface{ Scan(...any) error }) (StockNewsItem, error) {
	var item StockNewsItem
	err := row.Scan(&item.ID, &item.Source, &item.SourceItemID, &item.Symbol, &item.Market, &item.Title, &item.Summary, &item.Category, &item.Importance, &item.Keywords, &item.Quality, &item.PublishedAt, &item.DedupeKey, &item.RawPayload, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanStockDataTask(row interface{ Scan(...any) error }) (StockDataTask, error) {
	var item StockDataTask
	err := row.Scan(&item.ID, &item.TaskType, &item.Source, &item.Symbol, &item.Status, &item.RequestedBy, &item.InputJSON, &item.ResultJSON, &item.ProcessedCount, &item.FailedCount, &item.FailureSummary, &item.StartedAt, &item.CompletedAt, &item.NextRunAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func normalizeStockSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	source = strings.ReplaceAll(source, " ", "_")
	source = strings.ReplaceAll(source, "-", "_")
	return source
}

func normalizeStockInstrumentSearchParams(params StockInstrumentSearchParams) StockInstrumentSearchParams {
	params.Query = strings.TrimSpace(params.Query)
	params.Industry = strings.TrimSpace(params.Industry)
	params.MinListingDate = strings.TrimSpace(params.MinListingDate)
	params.Quality = normalizeStockInstrumentQuality(params.Quality)
	params.Sort = strings.TrimSpace(params.Sort)
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	if params.PageSize < 10 {
		params.PageSize = 10
	}
	if params.PageSize > 200 {
		params.PageSize = 200
	}
	params.Markets = normalizeCSVList(params.Markets, normalizeStockMarketFilter)
	params.Statuses, params.IncludeDelisted = normalizeStockInstrumentStatuses(params.Statuses, params.IncludeDelisted)
	params.Concepts = normalizeCSVList(params.Concepts, strings.TrimSpace)
	return params
}

func stockInstrumentSearchSQL(params StockInstrumentSearchParams) (string, []any, []any, string, bool) {
	clauses := []string{}
	args := []any{}
	useFTS := false
	if !params.IncludeDelisted {
		clauses = append(clauses, "status != 'delisted'")
	}
	if len(params.Markets) > 0 {
		clauses = append(clauses, "market IN ("+placeholders(len(params.Markets))+")")
		for _, v := range params.Markets {
			args = append(args, v)
		}
	}
	if len(params.Statuses) > 0 {
		statuses := stockInstrumentStatusFilterValues(params.Statuses)
		clauses = append(clauses, "status IN ("+placeholders(len(statuses))+")")
		for _, v := range statuses {
			args = append(args, v)
		}
	}
	if params.Quality != "" {
		clauses = append(clauses, "quality = ?")
		args = append(args, params.Quality)
	}
	if params.Industry != "" {
		clauses = append(clauses, "industry LIKE ?")
		args = append(args, "%"+params.Industry+"%")
	}
	for _, concept := range params.Concepts {
		clauses = append(clauses, "concept LIKE ?")
		args = append(args, "%"+concept+"%")
	}
	if params.MinListingDate != "" {
		clauses = append(clauses, "listing_date >= ?")
		args = append(args, params.MinListingDate)
	}
	tokens := strings.Fields(params.Query)
	if len(tokens) > 0 {
		for _, token := range tokens {
			like := "%" + token + "%"
			clauses = append(clauses, "(symbol LIKE ? OR name LIKE ? OR py LIKE ? OR py_full LIKE ? OR industry LIKE ? OR concept LIKE ?)")
			args = append(args, like, like, like, like, like, like)
		}
		if ftsQuery, ok := stockInstrumentFTSQuery(tokens); ok {
			clauses = append(clauses, "rowid IN (SELECT rowid FROM stock_instruments_fts WHERE stock_instruments_fts MATCH ?)")
			args = append(args, ftsQuery)
			useFTS = true
		}
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	orderArgs := []any{}
	orderBy := " ORDER BY updated_at DESC, symbol ASC"
	switch params.Sort {
	case "symbol_asc":
		orderBy = " ORDER BY symbol ASC"
	case "market_then_symbol":
		orderBy = " ORDER BY market ASC, symbol ASC"
	case "updated_desc":
		orderBy = " ORDER BY updated_at DESC, symbol ASC"
	case "relevance", "":
		if params.Query != "" {
			prefix := strings.Fields(params.Query)[0]
			prefixLike := prefix + "%"
			containsLike := "%" + prefix + "%"
			orderBy = ` ORDER BY CASE
  WHEN symbol LIKE ? THEN 0
  WHEN py LIKE ? THEN 1
  WHEN py_full LIKE ? THEN 2
  WHEN symbol LIKE ? THEN 3
  ELSE 4
END, symbol ASC`
			orderArgs = append(orderArgs, prefixLike, strings.ToUpper(prefixLike), strings.ToLower(prefixLike), containsLike)
		}
	default:
		orderBy = " ORDER BY updated_at DESC, symbol ASC"
	}
	return where, args, orderArgs, orderBy, useFTS
}

func normalizeStockInstrumentStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", "all":
		return ""
	case "tradable":
		return "listed"
	default:
		return status
	}
}

func normalizeStockInstrumentWriteStatus(status string) string {
	status = normalizeStockInstrumentStatus(status)
	if status == "" {
		return "listed"
	}
	return status
}

func normalizeStockInstrumentStatuses(statuses []string, includeDelisted bool) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range statuses {
		for _, part := range strings.Split(raw, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "all" {
				includeDelisted = true
				continue
			}
			status := normalizeStockInstrumentStatus(part)
			if status == "" || seen[status] {
				continue
			}
			seen[status] = true
			out = append(out, status)
		}
	}
	return out, includeDelisted
}

func normalizeStockMarketFilter(market string) string {
	market = strings.ToUpper(strings.TrimSpace(market))
	if market == "ALL" {
		return ""
	}
	return market
}

func normalizeStockInstrumentQuality(quality string) string {
	quality = strings.ToLower(strings.TrimSpace(quality))
	if quality == "all" {
		return ""
	}
	return quality
}

func stockInstrumentStatusFilterValues(statuses []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(statuses)+1)
	for _, status := range statuses {
		status = normalizeStockInstrumentStatus(status)
		if status == "" {
			continue
		}
		if !seen[status] {
			seen[status] = true
			out = append(out, status)
		}
		// 兼容曾经把 instrument lifecycle status 写成 tradable 的历史数据；
		// 数据迁移会修正存量行，这里保证旧进程/旧库查询仍不空。
		if status == "listed" && !seen["tradable"] {
			seen["tradable"] = true
			out = append(out, "tradable")
		}
	}
	return out
}

func stockInstrumentFTSQuery(tokens []string) (string, bool) {
	parts := []string{}
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" || !asciiFTSToken(token) {
			continue
		}
		parts = append(parts, strings.ToLower(token)+"*")
	}
	if len(parts) == 0 || !FTS5Available() {
		return "", false
	}
	return strings.Join(parts, " AND "), true
}

func asciiFTSToken(token string) bool {
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func stockInstrumentSnippets(items []StockInstrument, query string) map[string]map[string]string {
	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return nil
	}
	out := map[string]map[string]string{}
	for _, item := range items {
		fields := map[string]string{}
		if highlighted := highlightFirstToken(item.Name, tokens); highlighted != item.Name {
			fields["name"] = highlighted
		}
		if highlighted := highlightFirstToken(item.Industry, tokens); highlighted != item.Industry {
			fields["industry"] = highlighted
		}
		if highlighted := highlightFirstToken(item.Concept, tokens); highlighted != item.Concept {
			fields["concept"] = highlighted
		}
		if len(fields) > 0 {
			out[item.Symbol] = fields
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func highlightFirstToken(value string, tokens []string) string {
	if value == "" {
		return value
	}
	lower := strings.ToLower(value)
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		idx := strings.Index(lower, strings.ToLower(token))
		if idx < 0 {
			continue
		}
		end := idx + len(token)
		return value[:idx] + "[" + value[idx:end] + "]" + value[end:]
	}
	return value
}

func normalizeCSVList(values []string, normalize func(string) string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			value := normalize(strings.TrimSpace(part))
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func newsDedupeKey(item StockNewsItem) string {
	base := item.SourceItemID
	if base == "" {
		base = strings.Join([]string{item.Symbol, item.Title, item.PublishedAt}, "|")
	}
	return fmt.Sprintf("%s:%s", item.Source, strings.ToLower(strings.TrimSpace(base)))
}
