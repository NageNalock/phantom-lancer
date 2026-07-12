package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	db.SetMaxIdleConns(1)

	// ponytail: this service shares a 3.5 GiB host with Codex workers. A 768 MiB
	// cap leaves headroom for the Go heap and foreground requests; DuckDB may spill
	// larger analytical work to disk instead of forcing the host into swap.
	if _, err := db.ExecContext(context.Background(), "SET memory_limit = '768MB'"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set duckdb memory_limit: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), "SET threads = 1"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set duckdb threads: %w", err)
	}

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
				turnover_rate DOUBLE,
				net_inflow DOUBLE,
				main_net_inflow DOUBLE,
				amount_present BOOLEAN NOT NULL DEFAULT FALSE,
				turnover_rate_present BOOLEAN NOT NULL DEFAULT FALSE,
				net_inflow_present BOOLEAN NOT NULL DEFAULT FALSE,
				main_net_inflow_present BOOLEAN NOT NULL DEFAULT FALSE,
				data_payload_json VARCHAR,
			adjusted VARCHAR NOT NULL DEFAULT 'none',
			source VARCHAR,
			fetched_at TIMESTAMP,
			quality VARCHAR,
			error_message VARCHAR,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(symbol, trade_date, adjusted, source)
		);
		DROP INDEX IF EXISTS idx_stockv2_daily_bars_symbol_date;
		DROP INDEX IF EXISTS idx_stockv2_daily_bars_symbol_adjusted;
		CREATE INDEX IF NOT EXISTS idx_stockv2_daily_bars_symbol_adjusted_date
			ON stockv2_daily_bars(symbol, adjusted, trade_date);
			CREATE INDEX IF NOT EXISTS idx_stockv2_daily_bars_symbol_adjusted_fetched
				ON stockv2_daily_bars(symbol, adjusted, fetched_at);
			CREATE TABLE IF NOT EXISTS stockv2_trading_calendar (
				trade_date DATE PRIMARY KEY,
				observed_at TIMESTAMP NOT NULL,
				source VARCHAR NOT NULL DEFAULT 'legacy_observed',
				authoritative BOOLEAN NOT NULL DEFAULT FALSE
			);
			CREATE TABLE IF NOT EXISTS stockv2_daily_bar_quality (
			symbol VARCHAR NOT NULL,
			adjusted VARCHAR NOT NULL,
			row_count BIGINT NOT NULL,
			incomplete_count BIGINT NOT NULL DEFAULT 0,
			earliest_date VARCHAR,
			latest_date VARCHAR,
			source VARCHAR,
			last_error VARCHAR,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(symbol, adjusted)
		);
		CREATE TABLE IF NOT EXISTS stockv2_daily_bar_coverage_quality (
			symbol VARCHAR NOT NULL,
			adjusted VARCHAR NOT NULL,
			instrument_type VARCHAR NOT NULL,
			window_start_date VARCHAR NOT NULL,
			window_end_date VARCHAR NOT NULL,
			expected_date_count BIGINT NOT NULL DEFAULT 0,
			covered_date_count BIGINT NOT NULL DEFAULT 0,
			date_gap_count BIGINT NOT NULL DEFAULT 0,
			core_gap_count BIGINT NOT NULL DEFAULT 0,
			flow_gap_count BIGINT NOT NULL DEFAULT 0,
			verified_no_trade_count BIGINT NOT NULL DEFAULT 0,
			expected_latest_date VARCHAR,
			checked_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(symbol, adjusted)
		);

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
			last_update_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_symbol ON stockv2_instruments(symbol);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_market ON stockv2_instruments(market);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_type ON stockv2_instruments(instrument_type);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_created_at ON stockv2_instruments(created_at);

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
			amplitude DOUBLE NOT NULL DEFAULT 0,
			turnover_rate DOUBLE NOT NULL DEFAULT 0,
			volume_ratio DOUBLE NOT NULL DEFAULT 0,
			main_net_inflow DOUBLE NOT NULL DEFAULT 0,
			super_net_inflow DOUBLE NOT NULL DEFAULT 0,
			large_net_inflow DOUBLE NOT NULL DEFAULT 0,
			medium_net_inflow DOUBLE NOT NULL DEFAULT 0,
			small_net_inflow DOUBLE NOT NULL DEFAULT 0,
			main_net_inflow_pct DOUBLE NOT NULL DEFAULT 0,
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

		CREATE TABLE IF NOT EXISTS stockv2_quote_snapshots (
			id VARCHAR PRIMARY KEY,
			symbol VARCHAR NOT NULL,
			market VARCHAR,
			name VARCHAR,
			last_price DOUBLE NOT NULL DEFAULT 0,
			prev_close DOUBLE NOT NULL DEFAULT 0,
			open_price DOUBLE NOT NULL DEFAULT 0,
			high_price DOUBLE NOT NULL DEFAULT 0,
			low_price DOUBLE NOT NULL DEFAULT 0,
			volume DOUBLE NOT NULL DEFAULT 0,
			amount DOUBLE NOT NULL DEFAULT 0,
			pct_change DOUBLE NOT NULL DEFAULT 0,
			amplitude DOUBLE NOT NULL DEFAULT 0,
			turnover_rate DOUBLE NOT NULL DEFAULT 0,
			volume_ratio DOUBLE NOT NULL DEFAULT 0,
			main_net_inflow DOUBLE NOT NULL DEFAULT 0,
			super_net_inflow DOUBLE NOT NULL DEFAULT 0,
			large_net_inflow DOUBLE NOT NULL DEFAULT 0,
			medium_net_inflow DOUBLE NOT NULL DEFAULT 0,
			small_net_inflow DOUBLE NOT NULL DEFAULT 0,
			main_net_inflow_pct DOUBLE NOT NULL DEFAULT 0,
			quote_at TIMESTAMP NOT NULL,
			collected_at TIMESTAMP NOT NULL,
			source VARCHAR NOT NULL,
			status VARCHAR NOT NULL,
			error_message VARCHAR,
			created_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_quote_snapshots_symbol_collected
			ON stockv2_quote_snapshots(symbol, collected_at);

		CREATE TABLE IF NOT EXISTS stockv2_minute_bars (
			symbol VARCHAR NOT NULL,
			market VARCHAR,
			minute_at TIMESTAMP NOT NULL,
			open DOUBLE NOT NULL DEFAULT 0,
			high DOUBLE NOT NULL DEFAULT 0,
			low DOUBLE NOT NULL DEFAULT 0,
			close DOUBLE NOT NULL DEFAULT 0,
			prev_close DOUBLE NOT NULL DEFAULT 0,
			volume DOUBLE NOT NULL DEFAULT 0,
			amount DOUBLE NOT NULL DEFAULT 0,
			pct_change DOUBLE NOT NULL DEFAULT 0,
			main_net_inflow DOUBLE NOT NULL DEFAULT 0,
			snapshot_count INTEGER NOT NULL DEFAULT 0,
			source VARCHAR,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(symbol, minute_at)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_minute_bars_symbol_minute
			ON stockv2_minute_bars(symbol, minute_at);

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
			ai_profile_attempted_at TIMESTAMP,
			fund_type VARCHAR,
			tracking_index VARCHAR,
			theme VARCHAR,
			constituent_hint VARCHAR,
			base_profile_hash VARCHAR,
			base_profile_updated_at TIMESTAMP,
			base_profile_checked_at TIMESTAMP,
			profile_version INTEGER NOT NULL DEFAULT 1,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_stock_profiles_market ON stockv2_stock_profiles(market);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_stock_profiles_type ON stockv2_stock_profiles(instrument_type);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_stock_profiles_updated_at ON stockv2_stock_profiles(updated_at);

		CREATE TABLE IF NOT EXISTS stockv2_stock_profile_ai_states (
			symbol VARCHAR PRIMARY KEY,
			profile_schema_version INTEGER NOT NULL,
			base_profile_hash VARCHAR NOT NULL,
			announcement_revision BIGINT NOT NULL DEFAULT 0,
			manual_generation BIGINT NOT NULL DEFAULT 0,
			required_message_cutoff_at TIMESTAMP,
			desired_input_version VARCHAR,
			desired_trigger_reason VARCHAR,
			desired_priority INTEGER NOT NULL DEFAULT 0,
			desired_at TIMESTAMP,
			applied_input_version VARCHAR,
			applied_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_profile_ai_states_pending
			ON stockv2_stock_profile_ai_states(desired_input_version, applied_input_version, desired_priority);

		CREATE TABLE IF NOT EXISTS stockv2_stock_profile_ai_versions (
			symbol VARCHAR NOT NULL,
			input_version VARCHAR NOT NULL,
			base_profile_hash VARCHAR NOT NULL,
			announcement_revision BIGINT NOT NULL DEFAULT 0,
			announcement_cutoff_at TIMESTAMP,
			previous_input_version VARCHAR,
			input_manifest_json VARCHAR NOT NULL DEFAULT '{}',
			result_json VARCHAR NOT NULL,
			result_hash VARCHAR NOT NULL,
			model_name VARCHAR,
			confidence DOUBLE NOT NULL DEFAULT 0,
			agent_run_id VARCHAR,
			created_at TIMESTAMP NOT NULL,
			PRIMARY KEY(symbol, input_version),
			UNIQUE(agent_run_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_profile_ai_versions_symbol_created
			ON stockv2_stock_profile_ai_versions(symbol, created_at DESC);

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

		CREATE TABLE IF NOT EXISTS stockv2_announcements (
			id VARCHAR PRIMARY KEY,
			source VARCHAR NOT NULL,
			symbol VARCHAR NOT NULL,
			market VARCHAR,
			org_id VARCHAR,
			title VARCHAR NOT NULL,
			category VARCHAR,
			announcement_id VARCHAR,
			pdf_url VARCHAR,
			content_hash VARCHAR NOT NULL,
			dedupe_key VARCHAR UNIQUE,
			symbol_revision BIGINT NOT NULL DEFAULT 0,
			major BOOLEAN NOT NULL DEFAULT FALSE,
			major_reason VARCHAR,
			published_at TIMESTAMP,
			fetched_at TIMESTAMP NOT NULL,
			first_fetched_at TIMESTAMP,
			last_seen_at TIMESTAMP,
			body_status VARCHAR NOT NULL DEFAULT 'metadata_only',
			body_text_excerpt VARCHAR,
			body_hash VARCHAR,
			body_checked_at TIMESTAMP,
			body_error VARCHAR,
			body_attempt_count INTEGER NOT NULL DEFAULT 0,
			body_next_attempt_at TIMESTAMP,
			body_content_bytes BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			UNIQUE(source, symbol, content_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_announcements_symbol_published
			ON stockv2_announcements(symbol, published_at);
		CREATE INDEX IF NOT EXISTS idx_stockv2_announcements_major
			ON stockv2_announcements(major, published_at);

		CREATE TABLE IF NOT EXISTS stockv2_announcement_body_budgets (
			budget_date VARCHAR PRIMARY KEY,
			request_count INTEGER NOT NULL DEFAULT 0,
			byte_budget_used BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);

		CREATE TABLE IF NOT EXISTS stockv2_announcement_sync_states (
			source VARCHAR NOT NULL,
			market VARCHAR NOT NULL,
			covered_through TIMESTAMP NOT NULL,
			latest_published_at TIMESTAMP,
			last_success_at TIMESTAMP NOT NULL,
			last_window_start TIMESTAMP,
			last_window_end TIMESTAMP,
			last_page_count INTEGER NOT NULL DEFAULT 0,
			last_fetched_count INTEGER NOT NULL DEFAULT 0,
			last_inserted_count INTEGER NOT NULL DEFAULT 0,
			late_recheck_started_at TIMESTAMP,
			late_recheck_covered_through TIMESTAMP,
			last_late_recheck_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (source, market)
		);

		CREATE TABLE IF NOT EXISTS stockv2_embedding_vectors_v2 (
			vector_ref VARCHAR PRIMARY KEY,
			vector_blob BLOB NOT NULL,
			vector_values DOUBLE[],
			dimensions INTEGER NOT NULL,
			model_id VARCHAR NOT NULL,
			object_type VARCHAR NOT NULL,
			object_id VARCHAR NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_vectors_v2_model_object
			ON stockv2_embedding_vectors_v2(model_id, object_type, object_id);

		CREATE TABLE IF NOT EXISTS stockv2_embedding_assets (
			id VARCHAR PRIMARY KEY,
			object_type VARCHAR NOT NULL,
			object_id VARCHAR NOT NULL,
			text_hash VARCHAR NOT NULL,
			text_summary VARCHAR,
			model_id VARCHAR NOT NULL,
			provider_id VARCHAR,
			embedding_protocol VARCHAR,
			embedding_dimensions INTEGER NOT NULL DEFAULT 0,
			vector_ref VARCHAR,
			status VARCHAR NOT NULL,
			error_message VARCHAR,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			UNIQUE(object_type, object_id, model_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_object
			ON stockv2_embedding_assets(object_type, object_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_model
			ON stockv2_embedding_assets(model_id);
		CREATE INDEX IF NOT EXISTS idx_stockv2_embedding_assets_status
			ON stockv2_embedding_assets(status);
	`)
	if err != nil {
		return fmt.Errorf("init duckdb daily bars schema: %w", err)
	}
	if err := s.cleanupInstrumentSchema(ctx); err != nil {
		return err
	}
	for _, stmt := range []string{
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS turnover_rate DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS main_net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS amount_present BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS turnover_rate_present BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS net_inflow_present BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS main_net_inflow_present BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE stockv2_daily_bars ADD COLUMN IF NOT EXISTS data_payload_json VARCHAR`,
		`ALTER TABLE stockv2_daily_bar_quality ADD COLUMN IF NOT EXISTS incomplete_count BIGINT DEFAULT -1`,
		`ALTER TABLE stockv2_trading_calendar ADD COLUMN IF NOT EXISTS source VARCHAR DEFAULT 'legacy_observed'`,
		`ALTER TABLE stockv2_trading_calendar ADD COLUMN IF NOT EXISTS authoritative BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE stockv2_stock_profiles ADD COLUMN IF NOT EXISTS base_profile_hash VARCHAR`,
		`ALTER TABLE stockv2_stock_profiles ADD COLUMN IF NOT EXISTS base_profile_updated_at TIMESTAMP`,
		`ALTER TABLE stockv2_stock_profiles ADD COLUMN IF NOT EXISTS base_profile_checked_at TIMESTAMP`,
		`ALTER TABLE stockv2_stock_profiles ADD COLUMN IF NOT EXISTS ai_profile_attempted_at TIMESTAMP`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS amplitude DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS turnover_rate DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS volume_ratio DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS main_net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS super_net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS large_net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS medium_net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS small_net_inflow DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_quotes_latest ADD COLUMN IF NOT EXISTS main_net_inflow_pct DOUBLE DEFAULT 0`,
		`ALTER TABLE stockv2_embedding_vectors_v2 ADD COLUMN IF NOT EXISTS vector_values DOUBLE[]`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS dedupe_key VARCHAR`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS symbol_revision BIGINT DEFAULT 0`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS first_fetched_at TIMESTAMP`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_status VARCHAR DEFAULT 'metadata_only'`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_text_excerpt VARCHAR`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_hash VARCHAR`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_checked_at TIMESTAMP`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_error VARCHAR`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_attempt_count INTEGER DEFAULT 0`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_next_attempt_at TIMESTAMP`,
		`ALTER TABLE stockv2_announcements ADD COLUMN IF NOT EXISTS body_content_bytes BIGINT DEFAULT 0`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate duckdb asset columns: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_announcements
		SET dedupe_key = CASE
			WHEN COALESCE(announcement_id, '') <> '' THEN source || ':id:' || announcement_id
			ELSE source || ':hash:' || symbol || ':' || content_hash
		END
		WHERE COALESCE(dedupe_key, '') = '';
		DELETE FROM stockv2_announcements
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY dedupe_key ORDER BY updated_at DESC, created_at DESC, id DESC
				) AS rn
				FROM stockv2_announcements
			) ranked WHERE rn > 1
		);
		UPDATE stockv2_announcements
		SET first_fetched_at = COALESCE(first_fetched_at, fetched_at, created_at),
		    last_seen_at = COALESCE(last_seen_at, updated_at, fetched_at, created_at),
		    body_status = COALESCE(NULLIF(body_status, ''), 'metadata_only'),
		    body_attempt_count = COALESCE(body_attempt_count, 0),
		    body_content_bytes = COALESCE(body_content_bytes, 0);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_stockv2_announcements_dedupe
			ON stockv2_announcements(dedupe_key);
		CREATE INDEX IF NOT EXISTS idx_stockv2_announcements_body_pending
			ON stockv2_announcements(major, body_status, body_next_attempt_at);
	`); err != nil {
		return fmt.Errorf("migrate duckdb announcement identity: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profiles
		SET ai_profile_attempted_at = ai_profile_updated_at,
			ai_profile_updated_at = NULL
		WHERE ai_profile_attempted_at IS NULL
		  AND ai_profile_updated_at IS NOT NULL
		  AND ai_profile_status IN ('failed', 'not_configured')
	`); err != nil {
		return fmt.Errorf("migrate stock profile ai attempt timestamps: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profiles
		SET base_profile_checked_at = base_profile_updated_at
		WHERE base_profile_checked_at IS NULL AND base_profile_updated_at IS NOT NULL
	`); err != nil {
		return fmt.Errorf("migrate stock profile base check timestamps: %w", err)
	}
	if err := s.backfillDailyBarQuality(ctx); err != nil {
		return err
	}
	if err := s.backfillTradingCalendar(ctx); err != nil {
		return err
	}
	return nil
}

func (s *MarketDataStore) cleanupInstrumentSchema(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_name = 'stockv2_instruments' AND column_name = 'status'
	`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		DROP TABLE IF EXISTS stockv2_instruments_clean;
		CREATE TABLE stockv2_instruments_clean (
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
			last_update_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY(id)
		);
		INSERT INTO stockv2_instruments_clean (
			id, symbol, market, instrument_type, name, industry, sector, concepts,
			list_date, delist_date, last_update_at, created_at, updated_at
		)
		SELECT
			id, symbol, market, COALESCE(instrument_type, 'stock'), name, industry, sector, concepts,
			list_date, delist_date, last_update_at, created_at, updated_at
		FROM stockv2_instruments;
		DROP TABLE stockv2_instruments;
		ALTER TABLE stockv2_instruments_clean RENAME TO stockv2_instruments;
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_symbol ON stockv2_instruments(symbol);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_market ON stockv2_instruments(market);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_type ON stockv2_instruments(instrument_type);
		CREATE INDEX IF NOT EXISTS idx_stockv2_market_instruments_created_at ON stockv2_instruments(created_at);
	`)
	return err
}

func (s *MarketDataStore) backfillDailyBarQuality(ctx context.Context) error {
	var qualityCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_daily_bar_quality`).Scan(&qualityCount); err != nil {
		return wrapError(err, "count duckdb daily bar quality")
	}
	if qualityCount > 0 {
		var invalidCount int
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM stockv2_daily_bar_quality
			WHERE row_count <= 0 OR COALESCE(incomplete_count, -1) < 0
			   OR COALESCE(earliest_date, '') = '' OR COALESCE(latest_date, '') = ''
		`).Scan(&invalidCount); err != nil {
			return wrapError(err, "check duckdb daily bar quality")
		}
		if invalidCount == 0 {
			return nil
		}
	}
	var barCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_daily_bars`).Scan(&barCount); err != nil {
		return wrapError(err, "count duckdb daily bars for quality backfill")
	}
	if barCount == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO stockv2_daily_bar_quality (
				symbol, adjusted, row_count, incomplete_count, earliest_date, latest_date, source, last_error, updated_at
		)
		WITH selected AS (
			SELECT *
			FROM (
				SELECT *,
					ROW_NUMBER() OVER (
						PARTITION BY symbol, adjusted, trade_date
						ORDER BY
							CASE WHEN
								COALESCE(open, 0) > 0 AND isfinite(COALESCE(open, 0)) AND
								COALESCE(high, 0) > 0 AND isfinite(COALESCE(high, 0)) AND
								COALESCE(low, 0) > 0 AND isfinite(COALESCE(low, 0)) AND
								COALESCE(close, 0) > 0 AND isfinite(COALESCE(close, 0)) AND
								COALESCE(volume, 0) > 0 AND isfinite(COALESCE(volume, 0)) AND
								high >= greatest(open, close, low) AND low <= least(open, close, high) AND
								(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND isfinite(COALESCE(amount, 0)) AND
								(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
								COALESCE(turnover_rate, 0) >= 0 AND isfinite(COALESCE(turnover_rate, 0))
							THEN 0 ELSE 1 END,
							CASE WHEN
								(COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND isfinite(COALESCE(net_inflow, 0)) AND
								(COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0) AND isfinite(COALESCE(main_net_inflow, 0))
							THEN 0 ELSE 1 END,
							fetched_at DESC
					) AS rn
				FROM stockv2_daily_bars
			)
			WHERE rn = 1
		),
		agg AS (
			SELECT
					symbol,
					adjusted,
					COUNT(*) AS row_count,
					SUM(CASE WHEN
						(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND
						(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
						(
							COALESCE((SELECT instrument_type FROM stockv2_instruments i WHERE i.symbol = selected.symbol LIMIT 1), 'stock') = 'exchange_fund' OR
							((COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND
							 (COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0))
						)
					THEN 0 ELSE 1 END) AS incomplete_count,
					COALESCE(strftime(MIN(trade_date), '%Y-%m-%d'), '') AS earliest_date,
				COALESCE(strftime(MAX(trade_date), '%Y-%m-%d'), '') AS latest_date
			FROM selected
			GROUP BY symbol, adjusted
		),
		latest_source AS (
			SELECT symbol, adjusted, source,
				ROW_NUMBER() OVER (PARTITION BY symbol, adjusted ORDER BY trade_date DESC, fetched_at DESC) AS rn
			FROM selected
		),
		latest_error AS (
			SELECT symbol, adjusted, error_message,
				ROW_NUMBER() OVER (PARTITION BY symbol, adjusted ORDER BY fetched_at DESC) AS rn
			FROM stockv2_daily_bars
			WHERE COALESCE(error_message, '') != ''
		)
		SELECT
			a.symbol,
				a.adjusted,
				a.row_count,
				a.incomplete_count,
				a.earliest_date,
			a.latest_date,
			COALESCE(ls.source, ''),
			COALESCE(le.error_message, ''),
			?
		FROM agg a
		LEFT JOIN latest_source ls ON ls.symbol = a.symbol AND ls.adjusted = a.adjusted AND ls.rn = 1
		LEFT JOIN latest_error le ON le.symbol = a.symbol AND le.adjusted = a.adjusted AND le.rn = 1
	`, time.Now())
	if err != nil {
		return wrapError(err, "backfill duckdb daily bar quality")
	}
	return nil
}

func (s *MarketDataStore) backfillTradingCalendar(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_trading_calendar`).Scan(&count); err != nil {
		return wrapError(err, "count duckdb trading calendar")
	}
	if count > 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO stockv2_trading_calendar (trade_date, observed_at, source, authoritative)
		SELECT DISTINCT trade_date, ?, 'daily_bar_backfill', FALSE
		FROM stockv2_daily_bars
		WHERE COALESCE(close, 0) > 0
	`, time.Now())
	return wrapError(err, "backfill duckdb trading calendar")
}

// UpsertDailyBars writes daily bars into DuckDB without allowing a partial
// refresh to erase facets already supplied by the same source row.
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

	if _, err := tx.ExecContext(ctx, `
		CREATE TEMP TABLE IF NOT EXISTS stockv2_daily_bars_stage AS
		SELECT * FROM stockv2_daily_bars WHERE 1 = 0
	`); err != nil {
		return wrapError(err, "create duckdb daily bar stage")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_daily_bars_stage`); err != nil {
		return wrapError(err, "clear duckdb daily bar stage")
	}

	const q = `
			INSERT INTO stockv2_daily_bars_stage (
				id, symbol, market, trade_date, open, high, low, close, prev_close,
				volume, amount, pct_change, turnover_rate, net_inflow, main_net_inflow,
				amount_present, turnover_rate_present, net_inflow_present, main_net_inflow_present,
				data_payload_json, adjusted, source, fetched_at,
				quality, error_message, created_at, updated_at
			) VALUES (?, ?, ?, CAST(? AS DATE), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return wrapError(err, "prepare duckdb upsert daily bar")
	}
	defer stmt.Close()

	now := time.Now()
	affected := map[string]StockV2DailyBar{}
	for i := range bars {
		b := bars[i]
		applyDailyBarPresenceCompatibility(&b)
		if b.Quality == DailyBarQualityOK && !dailyBarFacetsComplete(b) {
			b.Quality = DailyBarQualityPartial
		}
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
			b.Volume, b.Amount, b.PctChange, b.TurnoverRate, b.NetInflow, b.MainNetInflow,
			b.AmountPresent, b.TurnoverRatePresent, b.NetInflowPresent, b.MainNetInflowPresent,
			b.DataPayload, b.Adjusted, b.Source,
			nullableTime(b.FetchedAt), b.Quality, b.ErrorMessage,
			b.CreatedAt, b.UpdatedAt,
		); err != nil {
			return wrapError(err, fmt.Sprintf("stage duckdb daily bar %s %s", b.Symbol, b.TradeDate))
		}
		affected[b.Symbol+"\x00"+b.Adjusted] = b
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_daily_bars
		SELECT * FROM stockv2_daily_bars_stage
		ON CONFLICT (symbol, trade_date, adjusted, source) DO UPDATE SET
			market = CASE WHEN COALESCE(excluded.market, '') <> '' THEN excluded.market ELSE stockv2_daily_bars.market END,
			open = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                      excluded.high > 0 AND isfinite(excluded.high) AND
			                      excluded.low > 0 AND isfinite(excluded.low) AND
			                      excluded.close > 0 AND isfinite(excluded.close) AND
			                      excluded.volume > 0 AND isfinite(excluded.volume) AND
			                      excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                      excluded.low <= least(excluded.open, excluded.close, excluded.high)
			            THEN excluded.open ELSE stockv2_daily_bars.open END,
			high = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                       excluded.high > 0 AND isfinite(excluded.high) AND
			                       excluded.low > 0 AND isfinite(excluded.low) AND
			                       excluded.close > 0 AND isfinite(excluded.close) AND
			                       excluded.volume > 0 AND isfinite(excluded.volume) AND
			                       excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                       excluded.low <= least(excluded.open, excluded.close, excluded.high)
			            THEN excluded.high ELSE stockv2_daily_bars.high END,
			low = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                      excluded.high > 0 AND isfinite(excluded.high) AND
			                      excluded.low > 0 AND isfinite(excluded.low) AND
			                      excluded.close > 0 AND isfinite(excluded.close) AND
			                      excluded.volume > 0 AND isfinite(excluded.volume) AND
			                      excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                      excluded.low <= least(excluded.open, excluded.close, excluded.high)
			           THEN excluded.low ELSE stockv2_daily_bars.low END,
			close = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                        excluded.high > 0 AND isfinite(excluded.high) AND
			                        excluded.low > 0 AND isfinite(excluded.low) AND
			                        excluded.close > 0 AND isfinite(excluded.close) AND
			                        excluded.volume > 0 AND isfinite(excluded.volume) AND
			                        excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                        excluded.low <= least(excluded.open, excluded.close, excluded.high)
			             THEN excluded.close ELSE stockv2_daily_bars.close END,
			prev_close = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                             excluded.high > 0 AND isfinite(excluded.high) AND
			                             excluded.low > 0 AND isfinite(excluded.low) AND
			                             excluded.close > 0 AND isfinite(excluded.close) AND
			                             excluded.volume > 0 AND isfinite(excluded.volume) AND
			                             excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                             excluded.low <= least(excluded.open, excluded.close, excluded.high)
			                  THEN excluded.prev_close ELSE stockv2_daily_bars.prev_close END,
			volume = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                         excluded.high > 0 AND isfinite(excluded.high) AND
			                         excluded.low > 0 AND isfinite(excluded.low) AND
			                         excluded.close > 0 AND isfinite(excluded.close) AND
			                         excluded.volume > 0 AND isfinite(excluded.volume) AND
			                         excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                         excluded.low <= least(excluded.open, excluded.close, excluded.high)
			              THEN excluded.volume ELSE stockv2_daily_bars.volume END,
			pct_change = CASE WHEN excluded.open > 0 AND isfinite(excluded.open) AND
			                             excluded.high > 0 AND isfinite(excluded.high) AND
			                             excluded.low > 0 AND isfinite(excluded.low) AND
			                             excluded.close > 0 AND isfinite(excluded.close) AND
			                             excluded.volume > 0 AND isfinite(excluded.volume) AND
			                             excluded.high >= greatest(excluded.open, excluded.close, excluded.low) AND
			                             excluded.low <= least(excluded.open, excluded.close, excluded.high)
			                  THEN excluded.pct_change ELSE stockv2_daily_bars.pct_change END,
			amount = CASE WHEN excluded.amount_present AND isfinite(excluded.amount) THEN excluded.amount ELSE stockv2_daily_bars.amount END,
			turnover_rate = CASE WHEN excluded.turnover_rate_present AND excluded.turnover_rate >= 0 AND isfinite(excluded.turnover_rate) THEN excluded.turnover_rate ELSE stockv2_daily_bars.turnover_rate END,
			net_inflow = CASE WHEN excluded.net_inflow_present AND isfinite(excluded.net_inflow) THEN excluded.net_inflow ELSE stockv2_daily_bars.net_inflow END,
			main_net_inflow = CASE WHEN excluded.main_net_inflow_present AND isfinite(excluded.main_net_inflow) THEN excluded.main_net_inflow ELSE stockv2_daily_bars.main_net_inflow END,
			amount_present = stockv2_daily_bars.amount_present OR (excluded.amount_present AND isfinite(excluded.amount)),
			turnover_rate_present = stockv2_daily_bars.turnover_rate_present OR (excluded.turnover_rate_present AND excluded.turnover_rate >= 0 AND isfinite(excluded.turnover_rate)),
			net_inflow_present = stockv2_daily_bars.net_inflow_present OR (excluded.net_inflow_present AND isfinite(excluded.net_inflow)),
			main_net_inflow_present = stockv2_daily_bars.main_net_inflow_present OR (excluded.main_net_inflow_present AND isfinite(excluded.main_net_inflow)),
			data_payload_json = CASE WHEN COALESCE(excluded.data_payload_json, '') <> ''
			                         THEN excluded.data_payload_json ELSE stockv2_daily_bars.data_payload_json END,
			fetched_at = GREATEST(stockv2_daily_bars.fetched_at, excluded.fetched_at),
			quality = CASE
				WHEN stockv2_daily_bars.quality = 'ok' OR excluded.quality = 'ok' THEN 'ok'
				ELSE excluded.quality
			END,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`); err != nil {
		return wrapError(err, "merge duckdb daily bar stage")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_trading_calendar (trade_date, observed_at, source, authoritative)
		SELECT DISTINCT trade_date, ?, 'daily_bar_observation', FALSE
		FROM stockv2_daily_bars_stage
		WHERE COALESCE(close, 0) > 0
		ON CONFLICT(trade_date) DO UPDATE SET
			observed_at = CASE
				WHEN stockv2_trading_calendar.authoritative
				THEN stockv2_trading_calendar.observed_at
				ELSE GREATEST(stockv2_trading_calendar.observed_at, excluded.observed_at)
			END,
			source = CASE WHEN stockv2_trading_calendar.authoritative
			              THEN stockv2_trading_calendar.source ELSE excluded.source END,
			authoritative = stockv2_trading_calendar.authoritative OR excluded.authoritative
	`, now); err != nil {
		return wrapError(err, "update duckdb trading calendar")
	}

	// 批量刷新所有受影响 symbol 的 quality（一次查询代替 N 次）
	if err := batchRefreshDailyBarQualityWithTx(ctx, tx, affected, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return wrapError(err, "commit duckdb daily bars")
	}
	return nil
}

func refreshDailyBarQualityWithTx(ctx context.Context, tx *sql.Tx, symbol, adjusted string, updatedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO stockv2_daily_bar_quality (
				symbol, adjusted, row_count, incomplete_count, earliest_date, latest_date, source, last_error, updated_at
		)
		WITH selected AS (
			SELECT *
			FROM (
				SELECT *,
					ROW_NUMBER() OVER (
						PARTITION BY symbol, adjusted, trade_date
						ORDER BY
							CASE WHEN
								COALESCE(open, 0) > 0 AND isfinite(COALESCE(open, 0)) AND
								COALESCE(high, 0) > 0 AND isfinite(COALESCE(high, 0)) AND
								COALESCE(low, 0) > 0 AND isfinite(COALESCE(low, 0)) AND
								COALESCE(close, 0) > 0 AND isfinite(COALESCE(close, 0)) AND
								COALESCE(volume, 0) > 0 AND isfinite(COALESCE(volume, 0)) AND
								high >= greatest(open, close, low) AND low <= least(open, close, high) AND
								(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND isfinite(COALESCE(amount, 0)) AND
								(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
								COALESCE(turnover_rate, 0) >= 0 AND isfinite(COALESCE(turnover_rate, 0))
							THEN 0 ELSE 1 END,
							CASE WHEN
								(COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND isfinite(COALESCE(net_inflow, 0)) AND
								(COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0) AND isfinite(COALESCE(main_net_inflow, 0))
							THEN 0 ELSE 1 END,
							fetched_at DESC
					) AS rn
				FROM stockv2_daily_bars
				WHERE symbol = ? AND adjusted = ?
			)
			WHERE rn = 1
		)
		SELECT
				?,
				?,
				COUNT(*),
				SUM(CASE WHEN
					(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND
					(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
					(
						COALESCE((SELECT instrument_type FROM stockv2_instruments i WHERE i.symbol = selected.symbol LIMIT 1), 'stock') = 'exchange_fund' OR
						((COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND
						 (COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0))
					)
				THEN 0 ELSE 1 END),
				COALESCE(strftime(MIN(trade_date), '%Y-%m-%d'), ''),
			COALESCE(strftime(MAX(trade_date), '%Y-%m-%d'), ''),
			COALESCE((SELECT source FROM selected
			          ORDER BY trade_date DESC, fetched_at DESC LIMIT 1), ''),
			COALESCE((SELECT error_message FROM stockv2_daily_bars
			          WHERE symbol = ? AND adjusted = ? AND COALESCE(error_message, '') != ''
			          ORDER BY fetched_at DESC LIMIT 1), ''),
			?
		FROM selected
	`, symbol, adjusted, symbol, adjusted, symbol, adjusted, updatedAt)
	if err != nil {
		return wrapError(err, fmt.Sprintf("refresh duckdb daily bar quality %s %s", symbol, adjusted))
	}
	return nil
}

// batchRefreshDailyBarQualityWithTx 批量刷新多个 symbol+adjusted 的日 K 质量统计。
// 用一次窗口函数查询代替原来每个 symbol 单独查询（N 次 → 1 次）。
func batchRefreshDailyBarQualityWithTx(ctx context.Context, tx *sql.Tx, affected map[string]StockV2DailyBar, updatedAt time.Time) error {
	if len(affected) == 0 {
		return nil
	}

	// 收集所有受影响的 (symbol, adjusted) 对
	type pair struct{ symbol, adjusted string }
	pairs := make([]pair, 0, len(affected))
	for _, b := range affected {
		pairs = append(pairs, pair{b.Symbol, b.Adjusted})
	}

	// 构建 IN 子句
	placeholders := make([]string, len(pairs))
	args := make([]any, 0, len(pairs)*2+1)
	for i, p := range pairs {
		placeholders[i] = "(?, ?)"
		args = append(args, p.symbol, p.adjusted)
	}
	args = append(args, updatedAt)

	// 使用窗口函数一次查询所有 symbol 的质量统计
	query := `
		INSERT OR REPLACE INTO stockv2_daily_bar_quality (
				symbol, adjusted, row_count, incomplete_count, earliest_date, latest_date, source, last_error, updated_at
		)
		WITH selected AS (
			SELECT *
			FROM (
				SELECT *,
					ROW_NUMBER() OVER (
						PARTITION BY symbol, adjusted, trade_date
						ORDER BY
							CASE WHEN
								COALESCE(open, 0) > 0 AND isfinite(COALESCE(open, 0)) AND
								COALESCE(high, 0) > 0 AND isfinite(COALESCE(high, 0)) AND
								COALESCE(low, 0) > 0 AND isfinite(COALESCE(low, 0)) AND
								COALESCE(close, 0) > 0 AND isfinite(COALESCE(close, 0)) AND
								COALESCE(volume, 0) > 0 AND isfinite(COALESCE(volume, 0)) AND
								high >= greatest(open, close, low) AND low <= least(open, close, high) AND
								(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND isfinite(COALESCE(amount, 0)) AND
								(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
								COALESCE(turnover_rate, 0) >= 0 AND isfinite(COALESCE(turnover_rate, 0))
							THEN 0 ELSE 1 END,
							CASE WHEN
								(COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND isfinite(COALESCE(net_inflow, 0)) AND
								(COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0) AND isfinite(COALESCE(main_net_inflow, 0))
							THEN 0 ELSE 1 END,
							fetched_at DESC
					) AS rn
				FROM stockv2_daily_bars
				WHERE (symbol, adjusted) IN (` + strings.Join(placeholders, ",") + `)
			)
			WHERE rn = 1
		),
		agg AS (
			SELECT
					symbol,
					adjusted,
					COUNT(*) AS row_count,
					SUM(CASE WHEN
						(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND
						(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
						(
							COALESCE((SELECT instrument_type FROM stockv2_instruments i WHERE i.symbol = selected.symbol LIMIT 1), 'stock') = 'exchange_fund' OR
							((COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND
							 (COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0))
						)
					THEN 0 ELSE 1 END) AS incomplete_count,
					COALESCE(strftime(MIN(trade_date), '%Y-%m-%d'), '') AS earliest_date,
				COALESCE(strftime(MAX(trade_date), '%Y-%m-%d'), '') AS latest_date
			FROM selected
			GROUP BY symbol, adjusted
		),
		latest_source AS (
			SELECT symbol, adjusted, source,
				ROW_NUMBER() OVER (PARTITION BY symbol, adjusted ORDER BY trade_date DESC, fetched_at DESC) AS rn
			FROM selected
		),
		latest_error AS (
			SELECT symbol, adjusted, error_message,
				ROW_NUMBER() OVER (PARTITION BY symbol, adjusted ORDER BY fetched_at DESC) AS rn
			FROM stockv2_daily_bars
			WHERE (symbol, adjusted) IN (` + strings.Join(placeholders, ",") + `)
			  AND COALESCE(error_message, '') != ''
		)
		SELECT
			a.symbol,
				a.adjusted,
				a.row_count,
				a.incomplete_count,
				a.earliest_date,
			a.latest_date,
			COALESCE(ls.source, '') AS source,
			COALESCE(le.error_message, '') AS last_error,
			?
		FROM agg a
		LEFT JOIN latest_source ls ON ls.symbol = a.symbol AND ls.adjusted = a.adjusted AND ls.rn = 1
		LEFT JOIN latest_error le ON le.symbol = a.symbol AND le.adjusted = a.adjusted AND le.rn = 1
	`

	// 展开参数：IN 子句出现 2 次，每次都需要完整的 pairs 参数
	fullArgs := make([]any, 0, len(pairs)*6+1)
	for i := 0; i < 2; i++ {
		for _, p := range pairs {
			fullArgs = append(fullArgs, p.symbol, p.adjusted)
		}
	}
	fullArgs = append(fullArgs, updatedAt)

	if _, err := tx.ExecContext(ctx, query, fullArgs...); err != nil {
		return wrapError(err, "batch refresh duckdb daily bar quality")
	}
	return nil
}

func (s *MarketDataStore) GetDailyBars(ctx context.Context, symbol, adjusted, startDate, endDate string, limit int) ([]StockV2DailyBar, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	baseQuery := `
		WITH selected AS (
			SELECT id, symbol, market, trade_date, open, high, low, close, prev_close,
			       volume, amount, pct_change, turnover_rate, net_inflow, main_net_inflow,
			       amount_present, turnover_rate_present, net_inflow_present, main_net_inflow_present,
			       data_payload_json, adjusted, source, fetched_at, quality, error_message,
			       created_at, updated_at
			FROM (
				SELECT *,
					ROW_NUMBER() OVER (
						PARTITION BY symbol, adjusted, trade_date
						ORDER BY
								CASE WHEN
									COALESCE(open, 0) > 0 AND isfinite(COALESCE(open, 0)) AND
									COALESCE(high, 0) > 0 AND isfinite(COALESCE(high, 0)) AND
									COALESCE(low, 0) > 0 AND isfinite(COALESCE(low, 0)) AND
									COALESCE(close, 0) > 0 AND isfinite(COALESCE(close, 0)) AND
									COALESCE(volume, 0) > 0 AND isfinite(COALESCE(volume, 0)) AND
									high >= greatest(open, close, low) AND low <= least(open, close, high) AND
									(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND isfinite(COALESCE(amount, 0)) AND
									(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
									COALESCE(turnover_rate, 0) >= 0 AND isfinite(COALESCE(turnover_rate, 0))
								THEN 0 ELSE 1 END,
								CASE WHEN
									(COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND isfinite(COALESCE(net_inflow, 0)) AND
									(COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0) AND isfinite(COALESCE(main_net_inflow, 0))
								THEN 0 ELSE 1 END,
								fetched_at DESC
					) AS rn
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
	baseQuery += `
			)
			WHERE rn = 1
		)
		SELECT id, symbol, COALESCE(market,''), strftime(trade_date, '%Y-%m-%d') AS trade_date,
		       COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0),
		       COALESCE(prev_close,0), COALESCE(volume,0), COALESCE(amount,0), COALESCE(pct_change,0),
		       COALESCE(turnover_rate,0), COALESCE(net_inflow,0), COALESCE(main_net_inflow,0),
		       COALESCE(amount_present,FALSE), COALESCE(turnover_rate_present,FALSE),
		       COALESCE(net_inflow_present,FALSE), COALESCE(main_net_inflow_present,FALSE),
		       COALESCE(data_payload_json,''),
		       adjusted, COALESCE(source,''), fetched_at, COALESCE(quality,''), COALESCE(error_message,''),
		       created_at, updated_at
		FROM selected
	`
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

func (s *MarketDataStore) GetDailyBarDates(ctx context.Context, symbol, adjusted, startDate, endDate string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	query := `
		SELECT DISTINCT strftime(trade_date, '%Y-%m-%d') AS trade_date
		FROM stockv2_daily_bars
		WHERE symbol = ? AND adjusted = ?
	`
	args := []any{symbol, adjusted}
	if startDate != "" {
		query += " AND trade_date >= CAST(? AS DATE)"
		args = append(args, startDate)
	}
	if endDate != "" {
		query += " AND trade_date <= CAST(? AS DATE)"
		args = append(args, endDate)
	}
	query += " ORDER BY trade_date ASC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "get duckdb daily bar dates")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, wrapError(err, "scan daily bar date")
		}
		out = append(out, date)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate daily bar dates")
	}
	return out, nil
}

// GetCompleteDailyBarDates returns only dates whose selected row contains all
// data facets required by downstream analysis. A partial row remains queryable,
// but deliberately stays outside gap coverage so the next maintenance pass can
// repair it.
func (s *MarketDataStore) GetCompleteDailyBarDates(ctx context.Context, symbol, adjusted, startDate, endDate string, requireFlow bool) ([]string, error) {
	facts, err := s.GetDailyBarStoredCoverageFacts(ctx, symbol, adjusted, startDate, endDate)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(facts))
	for _, fact := range facts {
		if fact.CoreComplete && (!requireFlow || fact.FlowComplete) {
			out = append(out, fact.TradeDate)
		}
	}
	return out, nil
}

func (s *MarketDataStore) GetObservedTradingDates(ctx context.Context, startDate, endDate string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT strftime(trade_date, '%Y-%m-%d')
		FROM stockv2_trading_calendar
		WHERE trade_date >= CAST(? AS DATE) AND trade_date <= CAST(? AS DATE)
		ORDER BY trade_date
	`, startDate, endDate)
	if err != nil {
		return nil, wrapError(err, "get duckdb observed trading dates")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tradeDate string
		if err := rows.Scan(&tradeDate); err != nil {
			return nil, wrapError(err, "scan duckdb observed trading date")
		}
		out = append(out, tradeDate)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate duckdb observed trading dates")
	}
	return out, nil
}

func (s *MarketDataStore) UpsertObservedTradingDates(ctx context.Context, dates []string, observedAt time.Time) error {
	return s.upsertTradingDates(ctx, dates, observedAt, "observed_daily_bar", false)
}

func (s *MarketDataStore) UpsertReferenceTradingDates(ctx context.Context, dates []string, observedAt time.Time) error {
	return s.upsertTradingDates(ctx, dates, observedAt, "tencent_reference_index", true)
}

func (s *MarketDataStore) upsertTradingDates(ctx context.Context, dates []string, observedAt time.Time, source string, authoritative bool) error {
	if len(dates) == 0 {
		return nil
	}
	if s == nil || s.db == nil {
		return fmt.Errorf("market data store is not initialized")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin upsert duckdb trading calendar")
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO stockv2_trading_calendar (trade_date, observed_at, source, authoritative)
		VALUES (CAST(? AS DATE), ?, ?, ?)
		ON CONFLICT(trade_date) DO UPDATE SET
			observed_at = CASE
				WHEN stockv2_trading_calendar.authoritative AND NOT excluded.authoritative
				THEN stockv2_trading_calendar.observed_at
				ELSE GREATEST(stockv2_trading_calendar.observed_at, excluded.observed_at)
			END,
			source = CASE WHEN excluded.authoritative THEN excluded.source ELSE stockv2_trading_calendar.source END,
			authoritative = stockv2_trading_calendar.authoritative OR excluded.authoritative
	`)
	if err != nil {
		return wrapError(err, "prepare upsert duckdb trading calendar")
	}
	defer stmt.Close()
	seen := make(map[string]struct{}, len(dates))
	for _, tradeDate := range dates {
		tradeDate = strings.TrimSpace(tradeDate)
		if _, err := time.Parse("2006-01-02", tradeDate); err != nil {
			continue
		}
		if _, ok := seen[tradeDate]; ok {
			continue
		}
		seen[tradeDate] = struct{}{}
		if _, err := stmt.ExecContext(ctx, tradeDate, observedAt, source, authoritative); err != nil {
			return wrapError(err, "upsert duckdb trading calendar date")
		}
	}
	return wrapError(tx.Commit(), "commit duckdb trading calendar")
}

func (s *MarketDataStore) TradingCalendarDateAuthoritative(ctx context.Context, tradeDate string) (bool, error) {
	authoritative, _, err := s.TradingCalendarDateProvenance(ctx, tradeDate)
	return authoritative, err
}

func (s *MarketDataStore) TradingCalendarDateProvenance(ctx context.Context, tradeDate string) (bool, time.Time, error) {
	if s == nil || s.db == nil || strings.TrimSpace(tradeDate) == "" {
		return false, time.Time{}, nil
	}
	var authoritative bool
	var observedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(authoritative, FALSE), observed_at
		FROM stockv2_trading_calendar WHERE trade_date = CAST(? AS DATE)
	`, tradeDate).Scan(&authoritative, &observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	return authoritative, observedAt, wrapError(err, "read trading calendar provenance")
}

func (s *MarketDataStore) GetDailyBarSymbolCoverage(ctx context.Context, symbols []string, adjusted, tradeDate string) (map[string]bool, error) {
	out := make(map[string]bool, len(symbols))
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	symbols = compactStringList(symbols, 0)
	if len(symbols) == 0 {
		return out, nil
	}
	query := `
		SELECT DISTINCT symbol
		FROM stockv2_daily_bars
		WHERE adjusted = ? AND trade_date = CAST(? AS DATE)
		  AND symbol IN (` + sqlPlaceholders(len(symbols)) + `)
		  AND (COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0)
		  AND (COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0)
	`
	args := make([]any, 0, len(symbols)+2)
	args = append(args, adjusted, tradeDate)
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "get duckdb daily bar symbol coverage")
	}
	defer rows.Close()
	for rows.Next() {
		var symbol string
		if err := rows.Scan(&symbol); err != nil {
			return nil, wrapError(err, "scan duckdb daily bar symbol coverage")
		}
		out[symbol] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate duckdb daily bar symbol coverage")
	}
	return out, nil
}

func (s *MarketDataStore) GetDailyBarsStats(ctx context.Context, symbol, adjusted string) (rowCount int, earliest, latest, source, lastError string, err error) {
	stats, err := s.GetDailyBarsStatsDetailed(ctx, symbol, adjusted)
	return stats.RowCount, stats.Earliest, stats.Latest, stats.Source, stats.LastError, err
}

func (s *MarketDataStore) GetDailyBarsStatsDetailed(ctx context.Context, symbol, adjusted string) (dailyBarsStats, error) {
	if s == nil || s.db == nil {
		return dailyBarsStats{}, fmt.Errorf("market data store is not initialized")
	}
	stats := dailyBarsStats{Symbol: symbol}
	err := s.db.QueryRowContext(ctx, `
		SELECT row_count, COALESCE(incomplete_count, row_count),
		       COALESCE(earliest_date,''), COALESCE(latest_date,''),
		       COALESCE(source,''), COALESCE(last_error,'')
		FROM stockv2_daily_bar_quality
		WHERE symbol = ? AND adjusted = ?
	`, symbol, adjusted).Scan(
		&stats.RowCount,
		&stats.IncompleteCount,
		&stats.Earliest,
		&stats.Latest,
		&stats.Source,
		&stats.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return stats, nil
	}
	if err != nil {
		return dailyBarsStats{}, wrapError(err, "get duckdb daily bars stats")
	}
	return stats, nil
}

func (s *MarketDataStore) GetDailyBarsStatsBatch(ctx context.Context, symbols []string, adjusted string) (map[string]dailyBarsStats, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	symbols = compactStringList(symbols, 200)
	if len(symbols) == 0 {
		return map[string]dailyBarsStats{}, nil
	}
	args := make([]any, 0, len(symbols)+3)
	args = append(args, adjusted)
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol, row_count, COALESCE(incomplete_count, row_count),
		       COALESCE(earliest_date,''), COALESCE(latest_date,''),
		       COALESCE(source,''), COALESCE(last_error,'')
		FROM stockv2_daily_bar_quality
		WHERE adjusted = ? AND symbol IN (`+sqlPlaceholders(len(symbols))+`)
	`, args...)
	if err != nil {
		return nil, wrapError(err, "get duckdb daily bars stats batch")
	}
	defer rows.Close()

	out := make(map[string]dailyBarsStats, len(symbols))
	for rows.Next() {
		var item dailyBarsStats
		if err := rows.Scan(
			&item.Symbol,
			&item.RowCount,
			&item.IncompleteCount,
			&item.Earliest,
			&item.Latest,
			&item.Source,
			&item.LastError,
		); err != nil {
			return nil, wrapError(err, "scan daily bars stats batch")
		}
		out[item.Symbol] = item
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate daily bars stats batch")
	}
	return out, nil
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

func scanDailyBarsRows(rows *sql.Rows) ([]StockV2DailyBar, error) {
	var out []StockV2DailyBar
	columns, _ := rows.Columns()
	withFieldPresence := len(columns) >= 27
	withDataFacets := len(columns) >= 23
	for rows.Next() {
		var b StockV2DailyBar
		var fetchedAt sql.NullTime
		if withFieldPresence {
			if err := rows.Scan(
				&b.ID, &b.Symbol, &b.Market, &b.TradeDate,
				&b.Open, &b.High, &b.Low, &b.Close,
				&b.PrevClose, &b.Volume, &b.Amount, &b.PctChange,
				&b.TurnoverRate, &b.NetInflow, &b.MainNetInflow,
				&b.AmountPresent, &b.TurnoverRatePresent, &b.NetInflowPresent, &b.MainNetInflowPresent,
				&b.DataPayload, &b.Adjusted, &b.Source, &fetchedAt, &b.Quality, &b.ErrorMessage,
				&b.CreatedAt, &b.UpdatedAt,
			); err != nil {
				return nil, wrapError(err, "scan daily bar")
			}
		} else if withDataFacets {
			if err := rows.Scan(
				&b.ID, &b.Symbol, &b.Market, &b.TradeDate,
				&b.Open, &b.High, &b.Low, &b.Close,
				&b.PrevClose, &b.Volume, &b.Amount, &b.PctChange,
				&b.TurnoverRate, &b.NetInflow, &b.MainNetInflow, &b.DataPayload,
				&b.Adjusted, &b.Source, &fetchedAt, &b.Quality, &b.ErrorMessage,
				&b.CreatedAt, &b.UpdatedAt,
			); err != nil {
				return nil, wrapError(err, "scan daily bar")
			}
		} else {
			if err := rows.Scan(
				&b.ID, &b.Symbol, &b.Market, &b.TradeDate,
				&b.Open, &b.High, &b.Low, &b.Close,
				&b.PrevClose, &b.Volume, &b.Amount, &b.PctChange,
				&b.Adjusted, &b.Source, &fetchedAt, &b.Quality, &b.ErrorMessage,
				&b.CreatedAt, &b.UpdatedAt,
			); err != nil {
				return nil, wrapError(err, "scan daily bar")
			}
		}
		if fetchedAt.Valid {
			b.FetchedAt = fetchedAt.Time
		}
		applyDailyBarPresenceCompatibility(&b)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate daily bars")
	}
	return out, nil
}

func applyDailyBarPresenceCompatibility(bar *StockV2DailyBar) {
	if bar == nil {
		return
	}
	if !bar.AmountPresent && bar.Amount != 0 {
		bar.AmountPresent = true
	}
	if !bar.TurnoverRatePresent && bar.TurnoverRate != 0 {
		bar.TurnoverRatePresent = true
	}
	if !bar.NetInflowPresent && bar.NetInflow != 0 {
		bar.NetInflowPresent = true
	}
	if !bar.MainNetInflowPresent && bar.MainNetInflow != 0 {
		bar.MainNetInflowPresent = true
	}
}
