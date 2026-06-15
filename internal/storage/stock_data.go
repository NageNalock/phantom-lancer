package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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
	return s.seedStockDataSources(ctx)
}

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
	item.Symbol = normalizeStockSymbol(item.Symbol)
	if item.Symbol == "" {
		return StockInstrument{}, errors.New("symbol is required")
	}
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		item.Name = item.Symbol
	}
	item.Status = defaultString(strings.TrimSpace(item.Status), "listed")
	item.Quality = defaultString(strings.TrimSpace(item.Quality), "fresh")
	item.Source = normalizeStockSource(item.Source)
	ts := now()
	existing, err := s.GetStockInstrument(ctx, item.Symbol)
	if err == nil {
		item.CreatedAt = existing.CreatedAt
		item.UpdatedAt = ts
	} else if err == ErrNotFound {
		item.CreatedAt = ts
		item.UpdatedAt = ts
	} else {
		return StockInstrument{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_instruments (symbol, market, name, status, industry, concept, listing_date, source, quality, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol) DO UPDATE SET market = excluded.market, name = excluded.name, status = excluded.status, industry = excluded.industry, concept = excluded.concept, listing_date = excluded.listing_date, source = excluded.source, quality = excluded.quality, updated_at = excluded.updated_at`,
		item.Symbol, item.Market, item.Name, item.Status, item.Industry, item.Concept, item.ListingDate, item.Source, item.Quality, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return StockInstrument{}, err
	}
	return item, nil
}

func (s *Store) GetStockInstrument(ctx context.Context, symbol string) (StockInstrument, error) {
	item, err := scanStockInstrument(s.db.QueryRowContext(ctx, `SELECT symbol, market, name, status, industry, concept, listing_date, source, quality, created_at, updated_at FROM stock_instruments WHERE symbol = ?`, normalizeStockSymbol(symbol)))
	if err == sql.ErrNoRows {
		return StockInstrument{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockInstruments(ctx context.Context, limit int) ([]StockInstrument, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT symbol, market, name, status, industry, concept, listing_date, source, quality, created_at, updated_at FROM stock_instruments ORDER BY updated_at DESC LIMIT ?`, limit)
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

func (s *Store) UpsertStockMarketDataPoint(ctx context.Context, point StockMarketDataPoint) (StockMarketDataPoint, bool, error) {
	point.Symbol = normalizeStockSymbol(point.Symbol)
	if point.Symbol == "" {
		return StockMarketDataPoint{}, false, errors.New("symbol is required")
	}
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
	item.Symbol = normalizeStockSymbol(item.Symbol)
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
	err := row.Scan(&item.Symbol, &item.Market, &item.Name, &item.Status, &item.Industry, &item.Concept, &item.ListingDate, &item.Source, &item.Quality, &item.CreatedAt, &item.UpdatedAt)
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

func newsDedupeKey(item StockNewsItem) string {
	base := item.SourceItemID
	if base == "" {
		base = strings.Join([]string{item.Symbol, item.Title, item.PublishedAt}, "|")
	}
	return fmt.Sprintf("%s:%s", item.Source, strings.ToLower(strings.TrimSpace(base)))
}
