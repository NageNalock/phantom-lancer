package stockv2

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// MarketDataStore owns analytical stock market assets in a local DuckDB file.
// It is embedded like SQLite: no external database server, endpoint, account,
// or network connection is required. Operational state stays in SQLite; high
// volume historical bars live here.
type MarketDataStore struct {
	db   *sql.DB
	path string
}

func NewMarketDataStore(path string) (*MarketDataStore, error) {
	if path == "" {
		return &MarketDataStore{}, nil
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create market db dir: %w", err)
		}
	}
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &MarketDataStore{db: db, path: path}
	if err := s.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *MarketDataStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *MarketDataStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *MarketDataStore) init(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_daily_bars (
			id VARCHAR,
			symbol VARCHAR NOT NULL,
			market VARCHAR,
			trade_date DATE NOT NULL,
			open DOUBLE,
			high DOUBLE,
			low DOUBLE,
			close DOUBLE,
			prev_close DOUBLE,
			volume DOUBLE,
			amount DOUBLE,
			pct_change DOUBLE,
			adjusted VARCHAR NOT NULL DEFAULT 'none',
			source VARCHAR,
			fetched_at TIMESTAMP,
			quality VARCHAR,
			error_message VARCHAR,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(symbol, trade_date, adjusted, source)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_daily_bars_symbol_date
			ON stockv2_daily_bars(symbol, trade_date);
		CREATE INDEX IF NOT EXISTS idx_stockv2_daily_bars_symbol_adjusted
			ON stockv2_daily_bars(symbol, adjusted);

		CREATE TABLE IF NOT EXISTS stockv2_instruments (
			id VARCHAR,
			symbol VARCHAR NOT NULL UNIQUE,
			market VARCHAR NOT NULL,
			instrument_type VARCHAR NOT NULL DEFAULT 'stock',
			name VARCHAR,
			industry VARCHAR,
			sector VARCHAR,
			concepts VARCHAR,
			list_date VARCHAR,
			delist_date VARCHAR,
			status VARCHAR DEFAULT 'active',
			last_update_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_symbol ON stockv2_instruments(symbol);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_market ON stockv2_instruments(market);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_status ON stockv2_instruments(status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_type ON stockv2_instruments(instrument_type);

		CREATE TABLE IF NOT EXISTS stockv2_quotes_latest (
			symbol VARCHAR PRIMARY KEY,
			market VARCHAR NOT NULL,
			name VARCHAR,
			last_price DOUBLE NOT NULL DEFAULT 0,
			prev_close DOUBLE NOT NULL DEFAULT 0,
			open_price DOUBLE NOT NULL DEFAULT 0,
			high_price DOUBLE NOT NULL DEFAULT 0,
			low_price DOUBLE NOT NULL DEFAULT 0,
			volume DOUBLE NOT NULL DEFAULT 0,
			amount DOUBLE NOT NULL DEFAULT 0,
			pct_change DOUBLE NOT NULL DEFAULT 0,
			quote_at TIMESTAMP NOT NULL,
			fetched_at TIMESTAMP NOT NULL,
			source VARCHAR NOT NULL,
			status VARCHAR NOT NULL,
			error_message VARCHAR,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_quotes_latest_status ON stockv2_quotes_latest(status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_quotes_latest_fetched_at ON stockv2_quotes_latest(fetched_at);

		CREATE TABLE IF NOT EXISTS stockv2_stock_profiles (
			symbol VARCHAR PRIMARY KEY,
			market VARCHAR NOT NULL,
			instrument_type VARCHAR NOT NULL DEFAULT 'stock',
			name VARCHAR NOT NULL,
			aliases_json VARCHAR NOT NULL DEFAULT '[]',
			industry VARCHAR,
			sectors_json VARCHAR NOT NULL DEFAULT '[]',
			concepts_json VARCHAR NOT NULL DEFAULT '[]',
			tags_json VARCHAR NOT NULL DEFAULT '[]',
			business_summary VARCHAR,
			profile_text VARCHAR NOT NULL,
			aliases_zh_json VARCHAR NOT NULL DEFAULT '[]',
			aliases_en_json VARCHAR NOT NULL DEFAULT '[]',
			keywords_zh_json VARCHAR NOT NULL DEFAULT '[]',
			keywords_en_json VARCHAR NOT NULL DEFAULT '[]',
			business_summary_zh VARCHAR,
			business_summary_en VARCHAR,
			business_lines_zh_json VARCHAR NOT NULL DEFAULT '[]',
			business_lines_en_json VARCHAR NOT NULL DEFAULT '[]',
			risk_tags_zh_json VARCHAR NOT NULL DEFAULT '[]',
			risk_tags_en_json VARCHAR NOT NULL DEFAULT '[]',
			profile_text_zh VARCHAR,
			profile_text_en VARCHAR,
			ai_profile_status VARCHAR NOT NULL DEFAULT 'missing',
			ai_profile_model VARCHAR,
			ai_profile_confidence DOUBLE NOT NULL DEFAULT 0,
			ai_profile_error VARCHAR,
			ai_profile_updated_at TIMESTAMP,
			fund_type VARCHAR,
			tracking_index VARCHAR,
			theme VARCHAR,
			constituent_hint VARCHAR,
			profile_version INTEGER NOT NULL DEFAULT 1,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_stock_profiles_market ON stockv2_stock_profiles(market);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_stock_profiles_type ON stockv2_stock_profiles(instrument_type);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_stock_profiles_updated_at ON stockv2_stock_profiles(updated_at);

		CREATE TABLE IF NOT EXISTS stockv2_raw_news (
			id VARCHAR PRIMARY KEY,
			source VARCHAR NOT NULL,
			source_id VARCHAR,
			language VARCHAR,
			title VARCHAR NOT NULL,
			content VARCHAR,
			snippet VARCHAR,
			published_at TIMESTAMP,
			fetched_at TIMESTAMP NOT NULL,
			url VARCHAR,
			raw_payload_json VARCHAR,
			content_hash VARCHAR NOT NULL,
			dedupe_key VARCHAR NOT NULL UNIQUE,
			quality VARCHAR NOT NULL,
			status VARCHAR NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_raw_news_source ON stockv2_raw_news(source);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_raw_news_status ON stockv2_raw_news(status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_raw_news_fetched_at ON stockv2_raw_news(fetched_at);

		CREATE TABLE IF NOT EXISTS stockv2_news_events (
			id VARCHAR PRIMARY KEY,
			raw_news_id VARCHAR,
			source VARCHAR NOT NULL,
			external_id VARCHAR,
			title VARCHAR NOT NULL,
			summary VARCHAR,
			content VARCHAR,
			url VARCHAR,
			quality_status VARCHAR,
			dedupe_key VARCHAR,
			link_status VARCHAR NOT NULL DEFAULT 'pending',
			event_at TIMESTAMP NOT NULL,
			link_processed_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_events_raw_news ON stockv2_news_events(raw_news_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_events_link_status ON stockv2_news_events(link_status);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_events_event_at ON stockv2_news_events(event_at);

		CREATE TABLE IF NOT EXISTS stockv2_news_link_candidates (
			id VARCHAR PRIMARY KEY,
			news_event_id VARCHAR NOT NULL,
			raw_news_id VARCHAR,
			symbol VARCHAR NOT NULL,
			market VARCHAR,
			instrument_name VARCHAR,
			match_method VARCHAR NOT NULL,
			score DOUBLE NOT NULL DEFAULT 0,
			reason VARCHAR,
			matched_terms_json VARCHAR NOT NULL DEFAULT '[]',
			monitor_status VARCHAR NOT NULL DEFAULT 'pending',
			monitor_hit_id VARCHAR,
			monitored_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			UNIQUE(news_event_id, symbol)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_link_candidates_event ON stockv2_news_link_candidates(news_event_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_link_candidates_raw_news ON stockv2_news_link_candidates(raw_news_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_link_candidates_symbol ON stockv2_news_link_candidates(symbol);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_news_link_candidates_monitor_status ON stockv2_news_link_candidates(monitor_status);
	`)
	if err != nil {
		return fmt.Errorf("init duckdb daily bars schema: %w", err)
	}
	return nil
}

// UpsertDailyBars writes daily bars into DuckDB. DuckDB does not support the
// exact SQLite UPSERT syntax used by the ops store, so we use INSERT OR REPLACE
// against the declared primary key.
func (s *MarketDataStore) UpsertDailyBars(ctx context.Context, bars []StockV2DailyBar) error {
	if len(bars) == 0 {
		return nil
	}
	if s == nil || s.db == nil {
		return fmt.Errorf("market data store is not initialized")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin duckdb tx")
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		INSERT OR REPLACE INTO stockv2_daily_bars (
			id, symbol, market, trade_date, open, high, low, close, prev_close,
			volume, amount, pct_change, adjusted, source, fetched_at, quality,
			error_message, created_at, updated_at
		) VALUES (?, ?, ?, CAST(? AS DATE), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return wrapError(err, "prepare duckdb upsert daily bar")
	}
	defer stmt.Close()

	now := time.Now()
	for i := range bars {
		b := bars[i]
		if b.ID == "" {
			b.ID = generateID()
		}
		if b.CreatedAt.IsZero() {
			b.CreatedAt = now
		}
		b.UpdatedAt = now
		if b.Adjusted == "" {
			b.Adjusted = DailyBarAdjustedNone
		}

		if _, err := stmt.ExecContext(ctx,
			b.ID, b.Symbol, b.Market, b.TradeDate,
			b.Open, b.High, b.Low, b.Close, b.PrevClose,
			b.Volume, b.Amount, b.PctChange, b.Adjusted, b.Source,
			nullableTime(b.FetchedAt), b.Quality, b.ErrorMessage,
			b.CreatedAt, b.UpdatedAt,
		); err != nil {
			return wrapError(err, fmt.Sprintf("upsert duckdb daily bar %s %s", b.Symbol, b.TradeDate))
		}
	}

	if err := tx.Commit(); err != nil {
		return wrapError(err, "commit duckdb daily bars")
	}
	return nil
}

func (s *MarketDataStore) GetDailyBars(ctx context.Context, symbol, adjusted, startDate, endDate string, limit int) ([]StockV2DailyBar, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	baseQuery := `
		SELECT id, symbol, COALESCE(market,''), strftime(trade_date, '%Y-%m-%d') AS trade_date,
		       COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0),
		       COALESCE(prev_close,0), COALESCE(volume,0), COALESCE(amount,0), COALESCE(pct_change,0),
		       adjusted, COALESCE(source,''), fetched_at, COALESCE(quality,''), COALESCE(error_message,''),
		       created_at, updated_at
		FROM stockv2_daily_bars
		WHERE symbol = ? AND adjusted = ?
	`
	args := []any{symbol, adjusted}
	if startDate != "" {
		baseQuery += " AND trade_date >= CAST(? AS DATE)"
		args = append(args, startDate)
	}
	if endDate != "" {
		baseQuery += " AND trade_date <= CAST(? AS DATE)"
		args = append(args, endDate)
	}
	query := baseQuery + " ORDER BY trade_date ASC"
	if limit > 0 {
		query = "SELECT * FROM (" + baseQuery + " ORDER BY trade_date DESC LIMIT ?) ORDER BY trade_date ASC"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "get duckdb daily bars")
	}
	defer rows.Close()
	return scanDailyBarsRows(rows)
}

func (s *MarketDataStore) GetDailyBarsStats(ctx context.Context, symbol, adjusted string) (rowCount int, earliest, latest, source, lastError string, err error) {
	if s == nil || s.db == nil {
		return 0, "", "", "", "", fmt.Errorf("market data store is not initialized")
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(strftime(MIN(trade_date), '%Y-%m-%d'),''),
		       COALESCE(strftime(MAX(trade_date), '%Y-%m-%d'),''),
		       COALESCE((SELECT source FROM stockv2_daily_bars
		                 WHERE symbol = ? AND adjusted = ?
		                 ORDER BY fetched_at DESC LIMIT 1),'')
		FROM stockv2_daily_bars
		WHERE symbol = ? AND adjusted = ?
	`, symbol, adjusted, symbol, adjusted).Scan(&rowCount, &earliest, &latest, &source)
	if err != nil {
		return 0, "", "", "", "", wrapError(err, "get duckdb daily bars stats")
	}

	var le sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT error_message FROM stockv2_daily_bars
		WHERE symbol = ? AND adjusted = ? AND COALESCE(error_message, '') != ''
		ORDER BY fetched_at DESC LIMIT 1
	`, symbol, adjusted).Scan(&le)
	if le.Valid {
		lastError = le.String
	}
	return rowCount, earliest, latest, source, lastError, nil
}

func (s *MarketDataStore) CountDailyBars(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_daily_bars`).Scan(&count); err != nil {
		return 0, wrapError(err, "count duckdb daily bars")
	}
	return count, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func scanDailyBarsRows(rows *sql.Rows) ([]StockV2DailyBar, error) {
	var out []StockV2DailyBar
	for rows.Next() {
		var b StockV2DailyBar
		var fetchedAt sql.NullTime
		if err := rows.Scan(
			&b.ID, &b.Symbol, &b.Market, &b.TradeDate,
			&b.Open, &b.High, &b.Low, &b.Close,
			&b.PrevClose, &b.Volume, &b.Amount, &b.PctChange,
			&b.Adjusted, &b.Source, &fetchedAt, &b.Quality, &b.ErrorMessage,
			&b.CreatedAt, &b.UpdatedAt,
		); err != nil {
			return nil, wrapError(err, "scan daily bar")
		}
		if fetchedAt.Valid {
			b.FetchedAt = fetchedAt.Time
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate daily bars")
	}
	return out, nil
}
