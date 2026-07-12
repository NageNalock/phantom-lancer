package stockv2

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ensureNewsContextSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_news_context_config (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			auto_cleanup_enabled INTEGER NOT NULL DEFAULT 0,
			hourly_enabled INTEGER NOT NULL DEFAULT 1,
			four_hour_enabled INTEGER NOT NULL DEFAULT 1,
			daily_enabled INTEGER NOT NULL DEFAULT 1,
			batch_size INTEGER NOT NULL DEFAULT 25,
			hourly_interval_seconds INTEGER NOT NULL DEFAULT 3600,
			four_hour_interval_seconds INTEGER NOT NULL DEFAULT 14400,
			daily_interval_seconds INTEGER NOT NULL DEFAULT 86400,
			cleanup_grace_seconds INTEGER NOT NULL DEFAULT 86400,
			next_hourly_at DATETIME,
			next_four_hour_at DATETIME,
			next_daily_at DATETIME,
			last_run_at DATETIME,
			last_cleanup_at DATETIME,
			last_error TEXT,
			updated_at DATETIME NOT NULL
		);
		INSERT OR IGNORE INTO stockv2_news_context_config
			(id, enabled, auto_cleanup_enabled, hourly_enabled, four_hour_enabled,
			 daily_enabled, batch_size, hourly_interval_seconds,
			 four_hour_interval_seconds, daily_interval_seconds,
			 cleanup_grace_seconds, updated_at)
		VALUES (?, 0, 0, 1, 1, 1, 25, 3600, 14400, 86400, 86400, datetime('now'));

		CREATE TABLE IF NOT EXISTS stockv2_news_context_runs (
			id TEXT PRIMARY KEY,
			window_type TEXT NOT NULL,
			trigger_type TEXT NOT NULL,
			status TEXT NOT NULL,
			phase TEXT,
			window_start DATETIME NOT NULL,
			window_end DATETIME NOT NULL,
			current_agent_run_id TEXT,
			review_status TEXT NOT NULL,
			review_run_id TEXT,
			cleanup_status TEXT NOT NULL,
			cleanup_run_id TEXT,
			input_count INTEGER NOT NULL DEFAULT 0,
			processed_count INTEGER NOT NULL DEFAULT 0,
			covered_count INTEGER NOT NULL DEFAULT 0,
			noise_count INTEGER NOT NULL DEFAULT 0,
			deferred_count INTEGER NOT NULL DEFAULT 0,
			created_thread_count INTEGER NOT NULL DEFAULT 0,
			updated_thread_count INTEGER NOT NULL DEFAULT 0,
			material_change_count INTEGER NOT NULL DEFAULT 0,
			conflict_count INTEGER NOT NULL DEFAULT 0,
			research_count INTEGER NOT NULL DEFAULT 0,
			pending_count INTEGER NOT NULL DEFAULT 0,
			error_message TEXT,
			requested_by TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(window_type, window_start, window_end)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_runs_status
			ON stockv2_news_context_runs(status, window_type, window_start);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_runs_review
			ON stockv2_news_context_runs(review_status, updated_at);

		CREATE TABLE IF NOT EXISTS stockv2_news_context_run_items (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			object_type TEXT NOT NULL,
			object_id TEXT NOT NULL,
			status TEXT NOT NULL,
			disposition TEXT,
			thread_id TEXT,
			version_id TEXT,
			agent_run_id TEXT,
			error_message TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(run_id, object_type, object_id),
			FOREIGN KEY (run_id) REFERENCES stockv2_news_context_runs(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_run_items_run_status
			ON stockv2_news_context_run_items(run_id, status, object_type);

		CREATE TABLE IF NOT EXISTS stockv2_news_context_cleanup_runs (
			id TEXT PRIMARY KEY,
			context_run_id TEXT,
			status TEXT NOT NULL,
			phase TEXT,
			cutoff DATETIME NOT NULL,
			scanned_count INTEGER NOT NULL DEFAULT 0,
			eligible_count INTEGER NOT NULL DEFAULT 0,
			compacted_count INTEGER NOT NULL DEFAULT 0,
			protected_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			released_bytes INTEGER NOT NULL DEFAULT 0,
			error_message TEXT,
			requested_by TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_cleanup_status
			ON stockv2_news_context_cleanup_runs(status, created_at);
	`, NewsContextConfigIDDefault); err != nil {
		return wrapError(err, "ensure news context sqlite schema")
	}

	if s.marketDB == nil || s.marketDB.db == nil {
		return errors.New("stockv2 market database is not configured")
	}
	if _, err := s.marketDB.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_news_threads (
			id VARCHAR PRIMARY KEY,
			title VARCHAR NOT NULL,
			core_thesis VARCHAR NOT NULL,
			stage VARCHAR NOT NULL,
			latest_change VARCHAR,
			confidence DOUBLE NOT NULL DEFAULT 0,
			status VARCHAR NOT NULL,
			industries_json VARCHAR NOT NULL DEFAULT '[]',
			symbols_json VARCHAR NOT NULL DEFAULT '[]',
			funds_json VARCHAR NOT NULL DEFAULT '[]',
			facts_json VARCHAR NOT NULL DEFAULT '[]',
			inferences_json VARCHAR NOT NULL DEFAULT '[]',
			counter_evidence_json VARCHAR NOT NULL DEFAULT '[]',
			open_questions_json VARCHAR NOT NULL DEFAULT '[]',
			leaders_json VARCHAR NOT NULL DEFAULT '[]',
			followers_json VARCHAR NOT NULL DEFAULT '[]',
			laggards_json VARCHAR NOT NULL DEFAULT '[]',
			next_candidates_json VARCHAR NOT NULL DEFAULT '[]',
			catalysts_json VARCHAR NOT NULL DEFAULT '[]',
			invalidations_json VARCHAR NOT NULL DEFAULT '[]',
			relations_json VARCHAR NOT NULL DEFAULT '[]',
			current_version INTEGER NOT NULL DEFAULT 0,
			current_version_id VARCHAR,
			review_status VARCHAR NOT NULL,
			index_status VARCHAR NOT NULL,
			index_error VARCHAR,
			first_seen_at TIMESTAMP NOT NULL,
			last_changed_at TIMESTAMP NOT NULL,
			last_reviewed_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_threads_stage
			ON stockv2_news_threads(stage, status, last_changed_at);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_threads_review
			ON stockv2_news_threads(review_status, index_status);

		CREATE TABLE IF NOT EXISTS stockv2_news_thread_versions (
			id VARCHAR PRIMARY KEY,
			thread_id VARCHAR NOT NULL,
			run_id VARCHAR NOT NULL,
			agent_run_id VARCHAR,
			window_type VARCHAR NOT NULL,
			version_no INTEGER NOT NULL,
			title VARCHAR NOT NULL,
			core_thesis VARCHAR NOT NULL,
			stage VARCHAR NOT NULL,
			latest_change VARCHAR,
			material_change INTEGER NOT NULL DEFAULT 0,
			confidence DOUBLE NOT NULL DEFAULT 0,
			industries_json VARCHAR NOT NULL DEFAULT '[]',
			symbols_json VARCHAR NOT NULL DEFAULT '[]',
			funds_json VARCHAR NOT NULL DEFAULT '[]',
			facts_json VARCHAR NOT NULL DEFAULT '[]',
			inferences_json VARCHAR NOT NULL DEFAULT '[]',
			counter_evidence_json VARCHAR NOT NULL DEFAULT '[]',
			open_questions_json VARCHAR NOT NULL DEFAULT '[]',
			leaders_json VARCHAR NOT NULL DEFAULT '[]',
			followers_json VARCHAR NOT NULL DEFAULT '[]',
			laggards_json VARCHAR NOT NULL DEFAULT '[]',
			next_candidates_json VARCHAR NOT NULL DEFAULT '[]',
			catalysts_json VARCHAR NOT NULL DEFAULT '[]',
			invalidations_json VARCHAR NOT NULL DEFAULT '[]',
			relations_json VARCHAR NOT NULL DEFAULT '[]',
			research_status VARCHAR,
			evidence_count INTEGER NOT NULL DEFAULT 0,
			review_status VARCHAR NOT NULL,
			index_status VARCHAR NOT NULL,
			index_error VARCHAR,
			created_at TIMESTAMP NOT NULL,
			UNIQUE(thread_id, version_no),
			UNIQUE(thread_id, agent_run_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_versions_thread
			ON stockv2_news_thread_versions(thread_id, version_no);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_versions_run
			ON stockv2_news_thread_versions(run_id, material_change, created_at);

		CREATE TABLE IF NOT EXISTS stockv2_news_thread_evidence (
			id VARCHAR PRIMARY KEY,
			thread_id VARCHAR NOT NULL,
			version_id VARCHAR NOT NULL,
			run_id VARCHAR NOT NULL,
			news_event_id VARCHAR,
			source VARCHAR,
			title VARCHAR NOT NULL,
			summary VARCHAR,
			url VARCHAR,
			content_hash VARCHAR,
			relation VARCHAR,
			event_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			UNIQUE(thread_id, version_id, news_event_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_evidence_thread
			ON stockv2_news_thread_evidence(thread_id, created_at);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_evidence_event
			ON stockv2_news_thread_evidence(news_event_id);
	`); err != nil {
		return wrapError(err, "ensure news context duckdb schema")
	}
	for _, stmt := range []string{
		`ALTER TABLE stockv2_news_events ADD COLUMN IF NOT EXISTS context_status VARCHAR DEFAULT 'pending'`,
		`ALTER TABLE stockv2_news_events ADD COLUMN IF NOT EXISTS context_run_id VARCHAR`,
		`ALTER TABLE stockv2_news_events ADD COLUMN IF NOT EXISTS context_covered_at TIMESTAMP`,
		`ALTER TABLE stockv2_news_events ADD COLUMN IF NOT EXISTS compacted_at TIMESTAMP`,
		`ALTER TABLE stockv2_news_events ADD COLUMN IF NOT EXISTS compacted_bytes BIGINT DEFAULT 0`,
		`ALTER TABLE stockv2_news_events ADD COLUMN IF NOT EXISTS protected_reason VARCHAR`,
	} {
		if _, err := s.marketDB.db.ExecContext(ctx, stmt); err != nil {
			return wrapError(err, "ensure news event context column")
		}
	}
	if _, err := s.marketDB.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_events_context_status
			ON stockv2_news_events(context_status, event_at);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_events_context_run
			ON stockv2_news_events(context_run_id, context_covered_at);
	`); err != nil {
		return wrapError(err, "ensure news event context indexes")
	}
	return nil
}

func normalizeNewsContextConfigRecord(item NewsContextConfig) NewsContextConfig {
	if item.ID == "" {
		item.ID = NewsContextConfigIDDefault
	}
	if item.BatchSize <= 0 {
		item.BatchSize = newsContextDefaultBatchSize
	}
	if item.BatchSize > newsContextMaxBatchSize {
		item.BatchSize = newsContextMaxBatchSize
	}
	if item.HourlyIntervalSeconds <= 0 {
		item.HourlyIntervalSeconds = 3600
	}
	if item.FourHourIntervalSeconds <= 0 {
		item.FourHourIntervalSeconds = 14400
	}
	if item.DailyIntervalSeconds <= 0 {
		item.DailyIntervalSeconds = 86400
	}
	if item.CleanupGraceSeconds <= 0 {
		item.CleanupGraceSeconds = 86400
	}
	return item
}

func (s *Store) GetNewsContextConfig(ctx context.Context) (NewsContextConfig, error) {
	var item NewsContextConfig
	var enabled, autoCleanup, hourly, fourHour, daily int
	var nextHourly, nextFourHour, nextDaily, lastRun, lastCleanup sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, enabled, auto_cleanup_enabled, hourly_enabled, four_hour_enabled,
		       daily_enabled, batch_size, hourly_interval_seconds,
		       four_hour_interval_seconds, daily_interval_seconds,
		       cleanup_grace_seconds, next_hourly_at, next_four_hour_at, next_daily_at,
		       last_run_at, last_cleanup_at, COALESCE(last_error,''), updated_at
		FROM stockv2_news_context_config WHERE id = ?
	`, NewsContextConfigIDDefault).Scan(
		&item.ID, &enabled, &autoCleanup, &hourly, &fourHour, &daily,
		&item.BatchSize, &item.HourlyIntervalSeconds, &item.FourHourIntervalSeconds,
		&item.DailyIntervalSeconds, &item.CleanupGraceSeconds, &nextHourly,
		&nextFourHour, &nextDaily, &lastRun, &lastCleanup, &item.LastError, &item.UpdatedAt,
	)
	if err != nil {
		return item, wrapError(err, "get news context config")
	}
	item.Enabled = enabled != 0
	item.AutoCleanupEnabled = autoCleanup != 0
	item.HourlyEnabled = hourly != 0
	item.FourHourEnabled = fourHour != 0
	item.DailyEnabled = daily != 0
	assignNullTime(&item.NextHourlyAt, nextHourly)
	assignNullTime(&item.NextFourHourAt, nextFourHour)
	assignNullTime(&item.NextDailyAt, nextDaily)
	assignNullTime(&item.LastRunAt, lastRun)
	assignNullTime(&item.LastCleanupAt, lastCleanup)
	return item, nil
}

func (s *Store) UpsertNewsContextConfig(ctx context.Context, item NewsContextConfig) (NewsContextConfig, error) {
	item = normalizeNewsContextConfigRecord(item)
	item.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_context_config (
			id, enabled, auto_cleanup_enabled, hourly_enabled, four_hour_enabled,
			daily_enabled, batch_size, hourly_interval_seconds,
			four_hour_interval_seconds, daily_interval_seconds, cleanup_grace_seconds,
			next_hourly_at, next_four_hour_at, next_daily_at, last_run_at,
			last_cleanup_at, last_error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled=excluded.enabled, auto_cleanup_enabled=excluded.auto_cleanup_enabled,
			hourly_enabled=excluded.hourly_enabled, four_hour_enabled=excluded.four_hour_enabled,
			daily_enabled=excluded.daily_enabled, batch_size=excluded.batch_size,
			hourly_interval_seconds=excluded.hourly_interval_seconds,
			four_hour_interval_seconds=excluded.four_hour_interval_seconds,
			daily_interval_seconds=excluded.daily_interval_seconds,
			cleanup_grace_seconds=excluded.cleanup_grace_seconds,
			next_hourly_at=excluded.next_hourly_at, next_four_hour_at=excluded.next_four_hour_at,
			next_daily_at=excluded.next_daily_at, last_run_at=excluded.last_run_at,
			last_cleanup_at=excluded.last_cleanup_at, last_error=excluded.last_error,
			updated_at=excluded.updated_at
	`, item.ID, boolToInt(item.Enabled), boolToInt(item.AutoCleanupEnabled),
		boolToInt(item.HourlyEnabled), boolToInt(item.FourHourEnabled), boolToInt(item.DailyEnabled),
		item.BatchSize, item.HourlyIntervalSeconds, item.FourHourIntervalSeconds,
		item.DailyIntervalSeconds, item.CleanupGraceSeconds, nullableTime(item.NextHourlyAt),
		nullableTime(item.NextFourHourAt), nullableTime(item.NextDailyAt), nullableTime(item.LastRunAt),
		nullableTime(item.LastCleanupAt), nullableString(item.LastError), item.UpdatedAt)
	if err != nil {
		return NewsContextConfig{}, wrapError(err, "upsert news context config")
	}
	return s.GetNewsContextConfig(ctx)
}

func assignNullTime(target *time.Time, value sql.NullTime) {
	if value.Valid {
		*target = value.Time
	}
}

const newsContextRunSelectSQL = `
	SELECT id, window_type, trigger_type, status, COALESCE(phase,''), window_start,
	       window_end, COALESCE(current_agent_run_id,''), review_status,
	       COALESCE(review_run_id,''), cleanup_status, COALESCE(cleanup_run_id,''),
	       input_count, processed_count, covered_count, noise_count, deferred_count,
	       created_thread_count, updated_thread_count, material_change_count,
	       conflict_count, research_count, pending_count, COALESCE(error_message,''),
	       COALESCE(requested_by,''), started_at, finished_at, created_at, updated_at
	FROM stockv2_news_context_runs`

func scanNewsContextRun(row rowScanner) (NewsContextRun, error) {
	var item NewsContextRun
	var startedAt, finishedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.WindowType, &item.TriggerType, &item.Status, &item.Phase,
		&item.WindowStart, &item.WindowEnd, &item.CurrentAgentRunID, &item.ReviewStatus,
		&item.ReviewRunID, &item.CleanupStatus, &item.CleanupRunID, &item.InputCount,
		&item.ProcessedCount, &item.CoveredCount, &item.NoiseCount, &item.DeferredCount,
		&item.CreatedThreadCount, &item.UpdatedThreadCount, &item.MaterialChangeCount,
		&item.ConflictCount, &item.ResearchCount, &item.PendingCount, &item.ErrorMessage,
		&item.RequestedBy, &startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	assignNullTime(&item.StartedAt, startedAt)
	assignNullTime(&item.FinishedAt, finishedAt)
	return item, err
}

func normalizeNewsContextRun(item NewsContextRun) NewsContextRun {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.TriggerType == "" {
		item.TriggerType = NewsContextTriggerScheduled
	}
	if item.Status == "" {
		item.Status = NewsContextRunStatusPending
	}
	if item.ReviewStatus == "" {
		item.ReviewStatus = NewsContextReviewPending
	}
	if item.CleanupStatus == "" {
		item.CleanupStatus = NewsContextCleanupPending
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return item
}

func (s *Store) CreateNewsContextRun(ctx context.Context, item NewsContextRun) (NewsContextRun, error) {
	item = normalizeNewsContextRun(item)
	if !validNewsContextWindowType(item.WindowType) || item.WindowStart.IsZero() || !item.WindowEnd.After(item.WindowStart) {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_context_runs (
			id, window_type, trigger_type, status, phase, window_start, window_end,
			current_agent_run_id, review_status, review_run_id, cleanup_status,
			cleanup_run_id, input_count, processed_count, covered_count, noise_count,
			deferred_count, created_thread_count, updated_thread_count,
			material_change_count, conflict_count, research_count, pending_count,
			error_message, requested_by, started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(window_type, window_start, window_end) DO NOTHING
	`, newsContextRunArgs(item)...)
	if err != nil {
		return NewsContextRun{}, wrapError(err, "create news context run")
	}
	return s.getNewsContextRunByWindow(ctx, item.WindowType, item.WindowStart, item.WindowEnd)
}

func newsContextRunArgs(item NewsContextRun) []any {
	return []any{
		item.ID, item.WindowType, item.TriggerType, item.Status, nullableString(item.Phase),
		item.WindowStart, item.WindowEnd, nullableString(item.CurrentAgentRunID), item.ReviewStatus,
		nullableString(item.ReviewRunID), item.CleanupStatus, nullableString(item.CleanupRunID),
		item.InputCount, item.ProcessedCount, item.CoveredCount, item.NoiseCount, item.DeferredCount,
		item.CreatedThreadCount, item.UpdatedThreadCount, item.MaterialChangeCount,
		item.ConflictCount, item.ResearchCount, item.PendingCount, nullableString(item.ErrorMessage),
		nullableString(item.RequestedBy), nullableTime(item.StartedAt), nullableTime(item.FinishedAt),
		item.CreatedAt, item.UpdatedAt,
	}
}

func (s *Store) getNewsContextRunByWindow(ctx context.Context, windowType string, start, end time.Time) (NewsContextRun, error) {
	item, err := scanNewsContextRun(s.db.QueryRowContext(ctx, newsContextRunSelectSQL+`
		WHERE window_type = ? AND window_start = ? AND window_end = ?`, windowType, start, end))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsContextRunNotFound
	}
	return item, wrapError(err, "get news context run by window")
}

func (s *Store) GetNewsContextRun(ctx context.Context, id string) (NewsContextRun, error) {
	item, err := scanNewsContextRun(s.db.QueryRowContext(ctx, newsContextRunSelectSQL+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsContextRunNotFound
	}
	return item, wrapError(err, "get news context run")
}

func (s *Store) BeginNewsContextReview(ctx context.Context, runID, reviewRunID string) (NewsContextRun, error) {
	runID = strings.TrimSpace(runID)
	reviewRunID = strings.TrimSpace(reviewRunID)
	if runID == "" || reviewRunID == "" {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_runs
		SET review_status=?, review_run_id=?, phase=?, error_message=NULL, updated_at=?
		WHERE id=? AND status=? AND review_status IN (?, ?)`,
		NewsContextReviewRunning, reviewRunID, "reviewing", now, runID,
		NewsContextRunStatusWaitingReview, NewsContextReviewPending, NewsContextReviewFailed)
	if err != nil {
		return NewsContextRun{}, wrapError(err, "begin news context review")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		if _, err := s.GetNewsContextRun(ctx, runID); err != nil {
			return NewsContextRun{}, err
		}
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	return s.GetNewsContextRun(ctx, runID)
}

func (s *Store) FindNewsContextRunByReviewRunID(ctx context.Context, reviewRunID string) (NewsContextRun, bool, error) {
	reviewRunID = strings.TrimSpace(reviewRunID)
	if reviewRunID == "" {
		return NewsContextRun{}, false, nil
	}
	item, err := scanNewsContextRun(s.db.QueryRowContext(ctx, newsContextRunSelectSQL+` WHERE review_run_id=?`, reviewRunID))
	if errors.Is(err, sql.ErrNoRows) {
		return NewsContextRun{}, false, nil
	}
	return item, err == nil, wrapError(err, "find news context run by review run id")
}

func (s *Store) HasCompletedDailyNewsContextCheckpointAfter(ctx context.Context, after time.Time) (bool, error) {
	if after.IsZero() {
		return false, ErrInvalidNewsContextInput
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_runs
		WHERE window_type=? AND status=? AND review_status=? AND updated_at>=?`,
		NewsContextWindowDaily, NewsContextRunStatusCompleted, NewsContextReviewCompleted, after).Scan(&count)
	return count > 0, wrapError(err, "check completed daily news context checkpoint")
}

func (s *Store) UpdateNewsContextRun(ctx context.Context, item NewsContextRun) (NewsContextRun, error) {
	item.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_news_context_runs SET
			trigger_type=?, status=?, phase=?, current_agent_run_id=?, review_status=?,
			review_run_id=?, cleanup_status=?, cleanup_run_id=?, input_count=?,
			processed_count=?, covered_count=?, noise_count=?, deferred_count=?,
			created_thread_count=?, updated_thread_count=?, material_change_count=?,
			conflict_count=?, research_count=?, pending_count=?, error_message=?,
			requested_by=?, started_at=?, finished_at=?, updated_at=?
		WHERE id=?
	`, item.TriggerType, item.Status, nullableString(item.Phase), nullableString(item.CurrentAgentRunID),
		item.ReviewStatus, nullableString(item.ReviewRunID), item.CleanupStatus,
		nullableString(item.CleanupRunID), item.InputCount, item.ProcessedCount,
		item.CoveredCount, item.NoiseCount, item.DeferredCount, item.CreatedThreadCount,
		item.UpdatedThreadCount, item.MaterialChangeCount, item.ConflictCount,
		item.ResearchCount, item.PendingCount, nullableString(item.ErrorMessage),
		nullableString(item.RequestedBy), nullableTime(item.StartedAt), nullableTime(item.FinishedAt),
		item.UpdatedAt, item.ID)
	if err != nil {
		return NewsContextRun{}, wrapError(err, "update news context run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return NewsContextRun{}, ErrNewsContextRunNotFound
	}
	return s.GetNewsContextRun(ctx, item.ID)
}

func (s *Store) HasRunningNewsContextRun(ctx context.Context, windowTypes ...string) (bool, error) {
	windowType := ""
	if len(windowTypes) > 0 {
		windowType = windowTypes[0]
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_runs
		WHERE status IN (?, ?) AND (? = '' OR window_type = ?)`, NewsContextRunStatusRunning,
		NewsContextRunStatusWaitingReview, strings.TrimSpace(windowType), strings.TrimSpace(windowType)).Scan(&count)
	return count > 0, wrapError(err, "has running news context run")
}

func (s *Store) FailRunningNewsContextRuns(ctx context.Context, reason string) (int64, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_runs
		SET status=?, error_message=?, finished_at=?, updated_at=?
		WHERE status IN (?, ?)`, NewsContextRunStatusFailed, nullableString(reason), now, now,
		NewsContextRunStatusRunning, NewsContextRunStatusWaitingReview)
	if err != nil {
		return 0, wrapError(err, "fail running news context runs")
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

func newsContextRunWhere(filter NewsContextRunListFilter) (string, []any) {
	parts := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, column+" = ?")
			args = append(args, strings.TrimSpace(value))
		}
	}
	add("window_type", filter.WindowType)
	add("trigger_type", filter.TriggerType)
	add("status", filter.Status)
	add("review_status", filter.ReviewStatus)
	if !filter.Since.IsZero() {
		parts = append(parts, "window_start >= ?")
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		parts = append(parts, "window_start < ?")
		args = append(args, filter.Until)
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsContextRuns(ctx context.Context, filter NewsContextRunListFilter) ([]NewsContextRun, error) {
	where, args := newsContextRunWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, newsContextRunSelectSQL+` WHERE `+where+`
		ORDER BY window_start DESC, created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news context runs")
	}
	return scanRows(rows, scanNewsContextRun, "scan news context run", "iterate news context runs")
}

func (s *Store) CountNewsContextRuns(ctx context.Context, filter NewsContextRunListFilter) (int, error) {
	where, args := newsContextRunWhere(filter)
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_runs WHERE `+where, args...).Scan(&count)
	return count, wrapError(err, "count news context runs")
}

const newsContextRunItemSelectSQL = `
	SELECT id, run_id, object_type, object_id, status, COALESCE(disposition,''),
	       COALESCE(thread_id,''), COALESCE(version_id,''), COALESCE(agent_run_id,''),
	       COALESCE(error_message,''), created_at, updated_at
	FROM stockv2_news_context_run_items`

func scanNewsContextRunItem(row rowScanner) (NewsContextRunItem, error) {
	var item NewsContextRunItem
	err := row.Scan(&item.ID, &item.RunID, &item.ObjectType, &item.ObjectID, &item.Status,
		&item.Disposition, &item.ThreadID, &item.VersionID, &item.AgentRunID,
		&item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *Store) AddNewsContextRunItems(ctx context.Context, items []NewsContextRunItem) error {
	if len(items) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now()
		for _, item := range items {
			if item.ID == "" {
				item.ID = generateID()
			}
			if item.Status == "" {
				item.Status = NewsContextRunItemPending
			}
			if item.CreatedAt.IsZero() {
				item.CreatedAt = now
			}
			item.UpdatedAt = now
			_, err := tx.ExecContext(ctx, `
				INSERT INTO stockv2_news_context_run_items
					(id, run_id, object_type, object_id, status, disposition, thread_id,
					 version_id, agent_run_id, error_message, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(run_id, object_type, object_id) DO NOTHING
			`, item.ID, item.RunID, item.ObjectType, item.ObjectID, item.Status,
				nullableString(item.Disposition), nullableString(item.ThreadID),
				nullableString(item.VersionID), nullableString(item.AgentRunID),
				nullableString(item.ErrorMessage), item.CreatedAt, item.UpdatedAt)
			if err != nil {
				return wrapError(err, "add news context run item")
			}
		}
		return nil
	})
}

func newsContextRunItemWhere(filter NewsContextRunItemListFilter) (string, []any) {
	parts := []string{"1=1"}
	args := make([]any, 0)
	for _, pair := range []struct{ column, value string }{
		{"run_id", filter.RunID}, {"object_type", filter.ObjectType},
		{"status", filter.Status}, {"agent_run_id", filter.AgentRunID},
	} {
		if strings.TrimSpace(pair.value) != "" {
			parts = append(parts, pair.column+" = ?")
			args = append(args, strings.TrimSpace(pair.value))
		}
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsContextRunItems(ctx context.Context, filter NewsContextRunItemListFilter) ([]NewsContextRunItem, error) {
	where, args := newsContextRunItemWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 1000), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, newsContextRunItemSelectSQL+` WHERE `+where+`
		ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news context run items")
	}
	return scanRows(rows, scanNewsContextRunItem, "scan news context run item", "iterate news context run items")
}

func (s *Store) CountNewsContextRunItems(ctx context.Context, filter NewsContextRunItemListFilter) (int, error) {
	where, args := newsContextRunItemWhere(filter)
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_run_items WHERE `+where, args...).Scan(&count)
	return count, wrapError(err, "count news context run items")
}

func (s *Store) CountNewsContextRunThreadDisposition(ctx context.Context, runID, disposition string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_run_items
		WHERE run_id=? AND object_type=? AND status=? AND disposition=?`,
		strings.TrimSpace(runID), NewsContextRunItemThread, NewsContextRunItemCompleted,
		strings.TrimSpace(disposition)).Scan(&count)
	return count, wrapError(err, "count news context thread disposition")
}

func (s *Store) MarkNewsContextRunItemsRunning(ctx context.Context, runID, agentRunID string, objectIDs []string) (int64, error) {
	if len(objectIDs) == 0 {
		return 0, nil
	}
	args := make([]any, 0, 4+len(objectIDs))
	now := time.Now()
	args = append(args, NewsContextRunItemRunning, nullableString(agentRunID), now, runID)
	for _, id := range objectIDs {
		args = append(args, id)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
		SET status=?, agent_run_id=?, error_message=NULL, updated_at=?
		WHERE run_id=? AND object_id IN (`+sqlPlaceholders(len(objectIDs))+`) AND status IN ('pending','failed','deferred')`, args...)
	if err != nil {
		return 0, wrapError(err, "mark news context run items running")
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

func (s *Store) CompleteNewsContextRunItem(ctx context.Context, runID, objectID, disposition, threadID, versionID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
		SET status=?, disposition=?, thread_id=?, version_id=?, error_message=NULL, updated_at=?
		WHERE run_id=? AND object_id=?`, NewsContextRunItemCompleted, nullableString(disposition),
		nullableString(threadID), nullableString(versionID), time.Now(), runID, objectID)
	if err != nil {
		return wrapError(err, "complete news context run item")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrInvalidNewsContextInput
	}
	return nil
}

func (s *Store) ResetFailedNewsContextRunItems(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
		SET status=?, agent_run_id=NULL, error_message=NULL, updated_at=?
		WHERE run_id=? AND status IN (?, ?, ?)`, NewsContextRunItemPending, time.Now(), runID,
		NewsContextRunItemFailed, NewsContextRunItemDeferred, NewsContextRunItemRunning)
	if err != nil {
		return wrapError(err, "reset failed news context run items")
	}
	return nil
}

const newsContextCleanupSelectSQL = `
	SELECT id, COALESCE(context_run_id,''), status, COALESCE(phase,''), cutoff,
	       scanned_count, eligible_count, compacted_count, protected_count,
	       failed_count, released_bytes, COALESCE(error_message,''),
	       COALESCE(requested_by,''), started_at, finished_at, created_at, updated_at
	FROM stockv2_news_context_cleanup_runs`

func scanNewsContextCleanupRun(row rowScanner) (NewsContextCleanupRun, error) {
	var item NewsContextCleanupRun
	var startedAt, finishedAt sql.NullTime
	err := row.Scan(&item.ID, &item.ContextRunID, &item.Status, &item.Phase, &item.Cutoff,
		&item.ScannedCount, &item.EligibleCount, &item.CompactedCount, &item.ProtectedCount,
		&item.FailedCount, &item.ReleasedBytes, &item.ErrorMessage, &item.RequestedBy,
		&startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt)
	assignNullTime(&item.StartedAt, startedAt)
	assignNullTime(&item.FinishedAt, finishedAt)
	return item, err
}

func normalizeNewsContextCleanupRun(item NewsContextCleanupRun) NewsContextCleanupRun {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.Status == "" {
		item.Status = NewsContextCleanupPending
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return item
}

func (s *Store) CreateNewsContextCleanupRun(ctx context.Context, item NewsContextCleanupRun) (NewsContextCleanupRun, error) {
	item = normalizeNewsContextCleanupRun(item)
	if item.Cutoff.IsZero() {
		return item, ErrInvalidNewsContextInput
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stockv2_news_context_cleanup_runs
		(id, context_run_id, status, phase, cutoff, scanned_count, eligible_count,
		 compacted_count, protected_count, failed_count, released_bytes, error_message,
		 requested_by, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, nullableString(item.ContextRunID), item.Status, nullableString(item.Phase), item.Cutoff,
		item.ScannedCount, item.EligibleCount, item.CompactedCount, item.ProtectedCount,
		item.FailedCount, item.ReleasedBytes, nullableString(item.ErrorMessage), nullableString(item.RequestedBy),
		nullableTime(item.StartedAt), nullableTime(item.FinishedAt), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return item, wrapError(err, "create news context cleanup run")
	}
	return s.GetNewsContextCleanupRun(ctx, item.ID)
}

func (s *Store) GetNewsContextCleanupRun(ctx context.Context, id string) (NewsContextCleanupRun, error) {
	item, err := scanNewsContextCleanupRun(s.db.QueryRowContext(ctx, newsContextCleanupSelectSQL+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsContextCleanupNotFound
	}
	return item, wrapError(err, "get news context cleanup run")
}

func (s *Store) UpdateNewsContextCleanupRun(ctx context.Context, item NewsContextCleanupRun) (NewsContextCleanupRun, error) {
	item.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_cleanup_runs SET
		context_run_id=?, status=?, phase=?, cutoff=?, scanned_count=?, eligible_count=?,
		compacted_count=?, protected_count=?, failed_count=?, released_bytes=?,
		error_message=?, requested_by=?, started_at=?, finished_at=?, updated_at=? WHERE id=?`,
		nullableString(item.ContextRunID), item.Status, nullableString(item.Phase), item.Cutoff,
		item.ScannedCount, item.EligibleCount, item.CompactedCount, item.ProtectedCount,
		item.FailedCount, item.ReleasedBytes, nullableString(item.ErrorMessage), nullableString(item.RequestedBy),
		nullableTime(item.StartedAt), nullableTime(item.FinishedAt), item.UpdatedAt, item.ID)
	if err != nil {
		return item, wrapError(err, "update news context cleanup run")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return item, ErrNewsContextCleanupNotFound
	}
	return s.GetNewsContextCleanupRun(ctx, item.ID)
}

func newsContextCleanupWhere(filter NewsContextCleanupRunListFilter) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(filter.Status) != "" {
		parts = append(parts, "status=?")
		args = append(args, strings.TrimSpace(filter.Status))
	}
	if !filter.Since.IsZero() {
		parts = append(parts, "created_at>=?")
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		parts = append(parts, "created_at<?")
		args = append(args, filter.Until)
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsContextCleanupRuns(ctx context.Context, filter NewsContextCleanupRunListFilter) ([]NewsContextCleanupRun, error) {
	where, args := newsContextCleanupWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, newsContextCleanupSelectSQL+` WHERE `+where+`
		ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news context cleanup runs")
	}
	return scanRows(rows, scanNewsContextCleanupRun, "scan news context cleanup run", "iterate news context cleanup runs")
}

func (s *Store) CountNewsContextCleanupRuns(ctx context.Context, filter NewsContextCleanupRunListFilter) (int, error) {
	where, args := newsContextCleanupWhere(filter)
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_cleanup_runs WHERE `+where, args...).Scan(&count)
	return count, wrapError(err, "count news context cleanup runs")
}

const newsThreadSelectSQL = `
	SELECT id, title, core_thesis, stage, COALESCE(latest_change,''), confidence, status,
	       industries_json, symbols_json, funds_json, facts_json, inferences_json,
	       counter_evidence_json, open_questions_json, leaders_json, followers_json,
	       laggards_json, next_candidates_json, catalysts_json, invalidations_json,
	       relations_json, current_version, COALESCE(current_version_id,''),
	       review_status, index_status, COALESCE(index_error,''), first_seen_at,
	       last_changed_at, last_reviewed_at, created_at, updated_at
	FROM stockv2_news_threads`

func scanNewsThread(row rowScanner) (NewsThread, error) {
	var item NewsThread
	var industries, symbols, funds, facts, inferences, counterEvidence, openQuestions string
	var leaders, followers, laggards, nextCandidates, catalysts, invalidations, relations string
	var lastReviewed sql.NullTime
	err := row.Scan(&item.ID, &item.Title, &item.CoreThesis, &item.Stage, &item.LatestChange,
		&item.Confidence, &item.Status, &industries, &symbols, &funds, &facts, &inferences,
		&counterEvidence, &openQuestions, &leaders, &followers, &laggards, &nextCandidates,
		&catalysts, &invalidations, &relations, &item.CurrentVersion, &item.CurrentVersionID,
		&item.ReviewStatus, &item.IndexStatus, &item.IndexError, &item.FirstSeenAt,
		&item.LastChangedAt, &lastReviewed, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	item.Industries = unmarshalStrings(industries)
	item.Symbols = unmarshalStrings(symbols)
	item.Funds = unmarshalStrings(funds)
	item.Facts = unmarshalStrings(facts)
	item.Inferences = unmarshalStrings(inferences)
	item.CounterEvidence = unmarshalStrings(counterEvidence)
	item.OpenQuestions = unmarshalStrings(openQuestions)
	item.Leaders = unmarshalStrings(leaders)
	item.Followers = unmarshalStrings(followers)
	item.Laggards = unmarshalStrings(laggards)
	item.NextCandidates = unmarshalStrings(nextCandidates)
	item.Catalysts = unmarshalStrings(catalysts)
	item.Invalidations = unmarshalStrings(invalidations)
	_ = json.Unmarshal([]byte(relations), &item.Relations)
	assignNullTime(&item.LastReviewedAt, lastReviewed)
	return item, nil
}

func normalizeNewsThread(item NewsThread) NewsThread {
	now := time.Now()
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.Status == "" {
		item.Status = NewsThreadStatusActive
	}
	if item.ReviewStatus == "" {
		item.ReviewStatus = NewsContextReviewPending
	}
	if item.IndexStatus == "" {
		item.IndexStatus = NewsContextIndexPending
	}
	if item.FirstSeenAt.IsZero() {
		item.FirstSeenAt = now
	}
	if item.LastChangedAt.IsZero() {
		item.LastChangedAt = now
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return item
}

func newsThreadArgs(item NewsThread) []any {
	relations, _ := json.Marshal(item.Relations)
	return []any{
		item.ID, item.Title, item.CoreThesis, item.Stage, nullableString(item.LatestChange),
		item.Confidence, item.Status, marshalStrings(item.Industries), marshalStrings(item.Symbols),
		marshalStrings(item.Funds), marshalStrings(item.Facts), marshalStrings(item.Inferences),
		marshalStrings(item.CounterEvidence), marshalStrings(item.OpenQuestions),
		marshalStrings(item.Leaders), marshalStrings(item.Followers), marshalStrings(item.Laggards),
		marshalStrings(item.NextCandidates), marshalStrings(item.Catalysts), marshalStrings(item.Invalidations),
		string(relations), item.CurrentVersion, nullableString(item.CurrentVersionID), item.ReviewStatus,
		item.IndexStatus, nullableString(item.IndexError), item.FirstSeenAt, item.LastChangedAt,
		nullableTime(item.LastReviewedAt), item.CreatedAt, item.UpdatedAt,
	}
}

func upsertNewsThreadTx(ctx context.Context, tx *sql.Tx, item NewsThread) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_news_threads (
			id,title,core_thesis,stage,latest_change,confidence,status,industries_json,
			symbols_json,funds_json,facts_json,inferences_json,counter_evidence_json,
			open_questions_json,leaders_json,followers_json,laggards_json,
			next_candidates_json,catalysts_json,invalidations_json,relations_json,
			current_version,current_version_id,review_status,index_status,index_error,
			first_seen_at,last_changed_at,last_reviewed_at,created_at,updated_at
		) VALUES (`+sqlPlaceholders(31)+`)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, core_thesis=excluded.core_thesis, stage=excluded.stage,
			latest_change=excluded.latest_change, confidence=excluded.confidence,
			status=excluded.status, industries_json=excluded.industries_json,
			symbols_json=excluded.symbols_json, funds_json=excluded.funds_json,
			facts_json=excluded.facts_json, inferences_json=excluded.inferences_json,
			counter_evidence_json=excluded.counter_evidence_json,
			open_questions_json=excluded.open_questions_json, leaders_json=excluded.leaders_json,
			followers_json=excluded.followers_json, laggards_json=excluded.laggards_json,
			next_candidates_json=excluded.next_candidates_json, catalysts_json=excluded.catalysts_json,
			invalidations_json=excluded.invalidations_json, relations_json=excluded.relations_json,
			current_version=excluded.current_version, current_version_id=excluded.current_version_id,
			review_status=excluded.review_status, index_status=excluded.index_status,
			index_error=excluded.index_error, first_seen_at=excluded.first_seen_at,
			last_changed_at=excluded.last_changed_at, last_reviewed_at=excluded.last_reviewed_at,
			updated_at=excluded.updated_at
	`, newsThreadArgs(item)...)
	return err
}

func (s *Store) CreateNewsThread(ctx context.Context, item NewsThread) (NewsThread, error) {
	item = normalizeNewsThread(item)
	if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.CoreThesis) == "" {
		return item, ErrInvalidNewsContextInput
	}
	_, err := s.marketDB.db.ExecContext(ctx, `INSERT INTO stockv2_news_threads (
		id,title,core_thesis,stage,latest_change,confidence,status,industries_json,
		symbols_json,funds_json,facts_json,inferences_json,counter_evidence_json,
		open_questions_json,leaders_json,followers_json,laggards_json,next_candidates_json,
		catalysts_json,invalidations_json,relations_json,current_version,current_version_id,
		review_status,index_status,index_error,first_seen_at,last_changed_at,last_reviewed_at,
		created_at,updated_at) VALUES (`+sqlPlaceholders(31)+`) ON CONFLICT(id) DO NOTHING`, newsThreadArgs(item)...)
	if err != nil {
		return item, wrapError(err, "create news thread")
	}
	return s.GetNewsThread(ctx, item.ID)
}

func (s *Store) UpdateNewsThread(ctx context.Context, item NewsThread) (NewsThread, error) {
	item = normalizeNewsThread(item)
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return item, wrapError(err, "begin update news thread")
	}
	defer tx.Rollback()
	if err := upsertNewsThreadTx(ctx, tx, item); err != nil {
		return item, wrapError(err, "update news thread")
	}
	if err := tx.Commit(); err != nil {
		return item, wrapError(err, "commit update news thread")
	}
	return s.GetNewsThread(ctx, item.ID)
}

func (s *Store) GetNewsThread(ctx context.Context, id string) (NewsThread, error) {
	item, err := scanNewsThread(s.marketDB.db.QueryRowContext(ctx, newsThreadSelectSQL+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsThreadNotFound
	}
	return item, wrapError(err, "get news thread")
}

func newsThreadWhere(filter NewsThreadListFilter) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	for _, pair := range []struct{ column, value string }{
		{"id", filter.ID}, {"status", filter.Status}, {"stage", filter.Stage},
		{"review_status", filter.ReviewStatus}, {"index_status", filter.IndexStatus},
	} {
		if strings.TrimSpace(pair.value) != "" {
			parts = append(parts, pair.column+"=?")
			args = append(args, strings.TrimSpace(pair.value))
		}
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		parts = append(parts, "(LOWER(title) LIKE ? OR LOWER(core_thesis) LIKE ? OR LOWER(latest_change) LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}
	if q := strings.TrimSpace(filter.Affected); q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		parts = append(parts, "(LOWER(industries_json) LIKE ? OR LOWER(symbols_json) LIKE ? OR LOWER(funds_json) LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}
	if !filter.Since.IsZero() {
		parts = append(parts, "last_changed_at>=?")
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		parts = append(parts, "last_changed_at<?")
		args = append(args, filter.Until)
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsThreads(ctx context.Context, filter NewsThreadListFilter) ([]NewsThread, error) {
	where, args := newsThreadWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadSelectSQL+` WHERE `+where+`
		ORDER BY last_changed_at DESC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news threads")
	}
	return scanRows(rows, scanNewsThread, "scan news thread", "iterate news threads")
}

func (s *Store) CountNewsThreads(ctx context.Context, filter NewsThreadListFilter) (int, error) {
	where, args := newsThreadWhere(filter)
	var count int
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_threads WHERE `+where, args...).Scan(&count)
	return count, wrapError(err, "count news threads")
}

const newsThreadVersionSelectSQL = `
	SELECT id, thread_id, run_id, COALESCE(agent_run_id,''), window_type, version_no,
	       title, core_thesis, stage, COALESCE(latest_change,''), material_change,
	       confidence, industries_json, symbols_json, funds_json, facts_json,
	       inferences_json, counter_evidence_json, open_questions_json, leaders_json,
	       followers_json, laggards_json, next_candidates_json, catalysts_json,
	       invalidations_json, relations_json, COALESCE(research_status,''), evidence_count,
	       review_status, index_status, COALESCE(index_error,''), created_at
	FROM stockv2_news_thread_versions`

func scanNewsThreadVersion(row rowScanner) (NewsThreadVersion, error) {
	var item NewsThreadVersion
	var material int
	var industries, symbols, funds, facts, inferences, counterEvidence, openQuestions string
	var leaders, followers, laggards, nextCandidates, catalysts, invalidations, relations string
	err := row.Scan(&item.ID, &item.ThreadID, &item.RunID, &item.AgentRunID, &item.WindowType,
		&item.VersionNo, &item.Title, &item.CoreThesis, &item.Stage, &item.LatestChange,
		&material, &item.Confidence, &industries, &symbols, &funds, &facts, &inferences,
		&counterEvidence, &openQuestions, &leaders, &followers, &laggards, &nextCandidates,
		&catalysts, &invalidations, &relations, &item.ResearchStatus, &item.EvidenceCount,
		&item.ReviewStatus, &item.IndexStatus, &item.IndexError, &item.CreatedAt)
	if err != nil {
		return item, err
	}
	item.MaterialChange = material != 0
	item.Industries = unmarshalStrings(industries)
	item.Symbols = unmarshalStrings(symbols)
	item.Funds = unmarshalStrings(funds)
	item.Facts = unmarshalStrings(facts)
	item.Inferences = unmarshalStrings(inferences)
	item.CounterEvidence = unmarshalStrings(counterEvidence)
	item.OpenQuestions = unmarshalStrings(openQuestions)
	item.Leaders = unmarshalStrings(leaders)
	item.Followers = unmarshalStrings(followers)
	item.Laggards = unmarshalStrings(laggards)
	item.NextCandidates = unmarshalStrings(nextCandidates)
	item.Catalysts = unmarshalStrings(catalysts)
	item.Invalidations = unmarshalStrings(invalidations)
	_ = json.Unmarshal([]byte(relations), &item.Relations)
	return item, nil
}

func normalizeNewsThreadVersion(item NewsThreadVersion) NewsThreadVersion {
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.ReviewStatus == "" {
		item.ReviewStatus = NewsContextReviewPending
	}
	if item.IndexStatus == "" {
		item.IndexStatus = NewsContextIndexPending
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	return item
}

func newsThreadVersionArgs(item NewsThreadVersion) []any {
	relations, _ := json.Marshal(item.Relations)
	return []any{
		item.ID, item.ThreadID, item.RunID, nullableString(item.AgentRunID), item.WindowType,
		item.VersionNo, item.Title, item.CoreThesis, item.Stage, nullableString(item.LatestChange),
		boolToInt(item.MaterialChange), item.Confidence, marshalStrings(item.Industries),
		marshalStrings(item.Symbols), marshalStrings(item.Funds), marshalStrings(item.Facts),
		marshalStrings(item.Inferences), marshalStrings(item.CounterEvidence),
		marshalStrings(item.OpenQuestions), marshalStrings(item.Leaders), marshalStrings(item.Followers),
		marshalStrings(item.Laggards), marshalStrings(item.NextCandidates), marshalStrings(item.Catalysts),
		marshalStrings(item.Invalidations), string(relations), nullableString(item.ResearchStatus),
		item.EvidenceCount, item.ReviewStatus, item.IndexStatus, nullableString(item.IndexError), item.CreatedAt,
	}
}

func insertNewsThreadVersionTx(ctx context.Context, tx *sql.Tx, item NewsThreadVersion) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_thread_versions (
		id,thread_id,run_id,agent_run_id,window_type,version_no,title,core_thesis,stage,
		latest_change,material_change,confidence,industries_json,symbols_json,funds_json,
		facts_json,inferences_json,counter_evidence_json,open_questions_json,leaders_json,
		followers_json,laggards_json,next_candidates_json,catalysts_json,invalidations_json,
		relations_json,research_status,evidence_count,review_status,index_status,index_error,created_at
	) VALUES (`+sqlPlaceholders(32)+`) ON CONFLICT DO NOTHING`, newsThreadVersionArgs(item)...)
	return err
}

func (s *Store) CreateNewsThreadVersion(ctx context.Context, item NewsThreadVersion) (NewsThreadVersion, error) {
	item = normalizeNewsThreadVersion(item)
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return item, wrapError(err, "begin create news thread version")
	}
	defer tx.Rollback()
	if err := insertNewsThreadVersionTx(ctx, tx, item); err != nil {
		return item, wrapError(err, "create news thread version")
	}
	if err := tx.Commit(); err != nil {
		return item, wrapError(err, "commit news thread version")
	}
	if item.AgentRunID != "" {
		row := s.marketDB.db.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE thread_id=? AND agent_run_id=?`, item.ThreadID, item.AgentRunID)
		return scanNewsThreadVersion(row)
	}
	row := s.marketDB.db.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE thread_id=? AND version_no=?`, item.ThreadID, item.VersionNo)
	return scanNewsThreadVersion(row)
}

func (s *Store) GetNewsThreadVersion(ctx context.Context, id string) (NewsThreadVersion, error) {
	item, err := scanNewsThreadVersion(s.marketDB.db.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsThreadNotFound
	}
	return item, wrapError(err, "get news thread version")
}

func newsThreadVersionWhere(filter NewsThreadVersionListFilter) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	for _, pair := range []struct{ column, value string }{
		{"id", filter.ID}, {"thread_id", filter.ThreadID}, {"run_id", filter.RunID},
		{"agent_run_id", filter.AgentRunID}, {"window_type", filter.WindowType},
		{"review_status", filter.ReviewStatus}, {"index_status", filter.IndexStatus},
	} {
		if strings.TrimSpace(pair.value) != "" {
			parts = append(parts, pair.column+"=?")
			args = append(args, strings.TrimSpace(pair.value))
		}
	}
	if filter.MaterialChange != nil {
		parts = append(parts, "material_change=?")
		args = append(args, boolToInt(*filter.MaterialChange))
	}
	if !filter.Since.IsZero() {
		parts = append(parts, "created_at>=?")
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		parts = append(parts, "created_at<?")
		args = append(args, filter.Until)
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsThreadVersions(ctx context.Context, filter NewsThreadVersionListFilter) ([]NewsThreadVersion, error) {
	where, args := newsThreadVersionWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadVersionSelectSQL+` WHERE `+where+`
		ORDER BY created_at DESC, version_no DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news thread versions")
	}
	return scanRows(rows, scanNewsThreadVersion, "scan news thread version", "iterate news thread versions")
}

func (s *Store) CountNewsThreadVersions(ctx context.Context, filter NewsThreadVersionListFilter) (int, error) {
	where, args := newsThreadVersionWhere(filter)
	var count int
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_thread_versions WHERE `+where, args...).Scan(&count)
	return count, wrapError(err, "count news thread versions")
}

// FindCompletedDailyNewsThreadVersionAfter returns the latest durable daily
// checkpoint created after the source news was covered. Callers can use the
// returned version for the historical-vector safety gate.
func (s *Store) FindCompletedDailyNewsThreadVersionAfter(ctx context.Context, threadID string, after time.Time) (NewsThreadVersion, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || after.IsZero() {
		return NewsThreadVersion{}, false, ErrInvalidNewsContextInput
	}
	item, err := scanNewsThreadVersion(s.marketDB.db.QueryRowContext(ctx, newsThreadVersionSelectSQL+`
		WHERE thread_id=? AND window_type=? AND review_status=? AND created_at>=?
		ORDER BY created_at DESC, version_no DESC LIMIT 1`,
		threadID, NewsContextWindowDaily, NewsContextReviewCompleted, after))
	if errors.Is(err, sql.ErrNoRows) {
		return NewsThreadVersion{}, false, nil
	}
	return item, err == nil, wrapError(err, "find completed daily news thread version")
}

const newsThreadEvidenceSelectSQL = `
	SELECT id, thread_id, version_id, run_id, COALESCE(news_event_id,''),
	       COALESCE(source,''), title, COALESCE(summary,''), COALESCE(url,''),
	       COALESCE(content_hash,''), COALESCE(relation,''), event_at, created_at
	FROM stockv2_news_thread_evidence`

func scanNewsThreadEvidence(row rowScanner) (NewsThreadEvidence, error) {
	var item NewsThreadEvidence
	var eventAt sql.NullTime
	err := row.Scan(&item.ID, &item.ThreadID, &item.VersionID, &item.RunID, &item.NewsEventID,
		&item.Source, &item.Title, &item.Summary, &item.URL, &item.ContentHash,
		&item.Relation, &eventAt, &item.CreatedAt)
	assignNullTime(&item.EventAt, eventAt)
	return item, err
}

func insertNewsThreadEvidenceTx(ctx context.Context, tx *sql.Tx, item NewsThreadEvidence) error {
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_thread_evidence
		(id,thread_id,version_id,run_id,news_event_id,source,title,summary,url,
		 content_hash,relation,event_at,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, item.ID, item.ThreadID,
		item.VersionID, item.RunID, nullableString(item.NewsEventID), nullableString(item.Source),
		item.Title, nullableString(item.Summary), nullableString(item.URL), nullableString(item.ContentHash),
		nullableString(item.Relation), nullableTime(item.EventAt), item.CreatedAt)
	return err
}

func (s *Store) CreateNewsThreadEvidence(ctx context.Context, item NewsThreadEvidence) (NewsThreadEvidence, error) {
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	if err := insertNewsThreadEvidenceTx(ctx, tx, item); err != nil {
		return item, wrapError(err, "create news thread evidence")
	}
	if err := tx.Commit(); err != nil {
		return item, err
	}
	if item.NewsEventID != "" {
		return scanNewsThreadEvidence(s.marketDB.db.QueryRowContext(ctx, newsThreadEvidenceSelectSQL+`
			WHERE thread_id=? AND version_id=? AND news_event_id=?`, item.ThreadID, item.VersionID, item.NewsEventID))
	}
	return item, nil
}

func newsThreadEvidenceWhere(filter NewsThreadEvidenceListFilter) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	for _, pair := range []struct{ column, value string }{
		{"thread_id", filter.ThreadID}, {"version_id", filter.VersionID},
		{"run_id", filter.RunID}, {"news_event_id", filter.NewsEventID},
	} {
		if strings.TrimSpace(pair.value) != "" {
			parts = append(parts, pair.column+"=?")
			args = append(args, strings.TrimSpace(pair.value))
		}
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsThreadEvidence(ctx context.Context, filter NewsThreadEvidenceListFilter) ([]NewsThreadEvidence, error) {
	where, args := newsThreadEvidenceWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 1000), normalizedPageOffset(filter.Offset))
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadEvidenceSelectSQL+` WHERE `+where+`
		ORDER BY created_at DESC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news thread evidence")
	}
	return scanRows(rows, scanNewsThreadEvidence, "scan news thread evidence", "iterate news thread evidence")
}

func (s *Store) CountNewsThreadEvidence(ctx context.Context, filter NewsThreadEvidenceListFilter) (int, error) {
	where, args := newsThreadEvidenceWhere(filter)
	var count int
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_thread_evidence WHERE `+where, args...).Scan(&count)
	return count, wrapError(err, "count news thread evidence")
}

func (s *Store) ListNewsContextChangedThreads(ctx context.Context, runID string, limit, offset int) ([]NewsThreadChange, error) {
	rows, err := s.marketDB.db.QueryContext(ctx, `SELECT run_id, thread_id, id, title, stage,
		COALESCE(latest_change,''), material_change, created_at
		FROM stockv2_news_thread_versions WHERE run_id=?
		ORDER BY created_at ASC, thread_id ASC LIMIT ? OFFSET ?`, runID,
		normalizedPageLimit(limit, 1000), normalizedPageOffset(offset))
	if err != nil {
		return nil, wrapError(err, "list news context changed threads")
	}
	defer rows.Close()
	out := make([]NewsThreadChange, 0)
	for rows.Next() {
		var item NewsThreadChange
		var material int
		if err := rows.Scan(&item.RunID, &item.ThreadID, &item.VersionID, &item.Title,
			&item.Stage, &item.LatestChange, &material, &item.CreatedAt); err != nil {
			return nil, wrapError(err, "scan news context changed thread")
		}
		item.MaterialChange = material != 0
		out = append(out, item)
	}
	return out, wrapError(rows.Err(), "iterate news context changed threads")
}

func newsContextStableID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:16])
}

func (s *Store) ApplyNewsContextBatch(ctx context.Context, runID, agentRunID, windowType string, report NewsContextReport) (NewsContextBatchApplyResult, error) {
	result := NewsContextBatchApplyResult{UrgentReview: report.UrgentReview}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(agentRunID) == "" ||
		report.RunID != runID || report.WindowType != windowType || !validNewsContextWindowType(windowType) {
		return result, ErrInvalidNewsContextResult
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return result, wrapError(err, "begin apply news context batch")
	}
	defer tx.Rollback()
	now := time.Now()
	threadForNews := make(map[string]string)
	versionForThread := make(map[string]string)
	researchStatus, researchProtectionReason := newsContextReportResearchGate(report)

	for index, change := range report.ThreadChanges {
		threadID := strings.TrimSpace(change.ThreadID)
		existingTarget := threadID != ""
		if threadID == "" {
			threadID = newsContextStableID("thread_", runID, fmt.Sprint(index), change.Title, change.CoreThesis)
			result.CreatedThreadCount++
		} else {
			result.UpdatedThreadCount++
		}
		var existing NewsThread
		existing, getErr := scanNewsThread(tx.QueryRowContext(ctx, newsThreadSelectSQL+` WHERE id=?`, threadID))
		if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
			return result, wrapError(getErr, "load news thread during batch")
		}
		if existingTarget && errors.Is(getErr, sql.ErrNoRows) {
			return result, fmt.Errorf("%w: target news thread does not exist", ErrInvalidNewsContextResult)
		}
		versionID := newsContextStableID("threadver_", runID, threadID, change.Title,
			change.CoreThesis, change.Stage, change.LatestChange, fmt.Sprint(change.MaterialChange))
		version, versionErr := scanNewsThreadVersion(tx.QueryRowContext(ctx, newsThreadVersionSelectSQL+`
			WHERE id=?`, versionID))
		if versionErr != nil && !errors.Is(versionErr, sql.ErrNoRows) {
			return result, wrapError(versionErr, "load idempotent news thread version")
		}
		if errors.Is(versionErr, sql.ErrNoRows) {
			versionNo := existing.CurrentVersion + 1
			if versionNo <= 0 {
				versionNo = 1
			}
			version = normalizeNewsThreadVersion(NewsThreadVersion{
				ID: versionID, ThreadID: threadID,
				RunID: runID, AgentRunID: agentRunID, WindowType: windowType, VersionNo: versionNo,
				Title: change.Title, CoreThesis: change.CoreThesis, Stage: change.Stage,
				LatestChange: change.LatestChange, MaterialChange: change.MaterialChange,
				Confidence: change.Confidence, Industries: change.Industries, Symbols: change.Symbols,
				Funds: change.Funds, Facts: change.Facts, Inferences: change.Inferences,
				CounterEvidence: change.CounterEvidence, OpenQuestions: change.OpenQuestions,
				Leaders: change.Leaders, Followers: change.Followers, Laggards: change.Laggards,
				NextCandidates: change.NextCandidates, Catalysts: change.Catalysts,
				Invalidations: change.Invalidations, Relations: change.Relations,
				ResearchStatus: newsContextWorseResearchStatus(researchStatus, change.ResearchStatus), EvidenceCount: len(change.EvidenceNewsIDs),
			})
			if windowType == NewsContextWindowHourly && !change.MaterialChange {
				version.ReviewStatus = NewsContextReviewNotRequired
			}
			if err := insertNewsThreadVersionTx(ctx, tx, version); err != nil {
				return result, wrapError(err, "insert news thread version in batch")
			}
		}
		thread := normalizeNewsThread(NewsThread{
			ID: threadID, Title: change.Title, CoreThesis: change.CoreThesis, Stage: change.Stage,
			LatestChange: change.LatestChange, Confidence: change.Confidence, Status: NewsThreadStatusActive,
			Industries: change.Industries, Symbols: change.Symbols, Funds: change.Funds,
			Facts: change.Facts, Inferences: change.Inferences, CounterEvidence: change.CounterEvidence,
			OpenQuestions: change.OpenQuestions, Leaders: change.Leaders, Followers: change.Followers,
			Laggards: change.Laggards, NextCandidates: change.NextCandidates, Catalysts: change.Catalysts,
			Invalidations: change.Invalidations, Relations: change.Relations,
			CurrentVersion: version.VersionNo, CurrentVersionID: version.ID,
			ReviewStatus: version.ReviewStatus, IndexStatus: NewsContextIndexPending,
			FirstSeenAt: existing.FirstSeenAt, LastChangedAt: now, CreatedAt: existing.CreatedAt,
		})
		if err := upsertNewsThreadTx(ctx, tx, thread); err != nil {
			return result, wrapError(err, "upsert news thread in batch")
		}
		if strings.TrimSpace(change.Action) == "merge" {
			for _, sourceThreadID := range change.SourceThreadIDs {
				sourceThreadID = strings.TrimSpace(sourceThreadID)
				if sourceThreadID == "" || sourceThreadID == threadID {
					continue
				}
				update, err := tx.ExecContext(ctx, `UPDATE stockv2_news_threads
					SET status=?, last_changed_at=?, updated_at=? WHERE id=?`,
					NewsThreadStatusMerged, now, now, sourceThreadID)
				if err != nil {
					return result, wrapError(err, "mark merged source news thread")
				}
				if rows, _ := update.RowsAffected(); rows == 0 {
					return result, fmt.Errorf("%w: merged source news thread does not exist", ErrInvalidNewsContextResult)
				}
			}
		}
		versionForThread[threadID] = version.ID
		result.ChangedThreadIDs = append(result.ChangedThreadIDs, threadID)
		result.ChangedVersionIDs = append(result.ChangedVersionIDs, version.ID)
		if change.MaterialChange {
			result.MaterialChangeCount++
		}
		if strings.Contains(strings.ToLower(change.LatestChange), "conflict") || len(change.CounterEvidence) > 0 {
			result.ConflictCount++
		}
		for _, eventID := range change.EvidenceNewsIDs {
			event, eventErr := scanNewsEvent(tx.QueryRowContext(ctx, newsEventSelectSQL()+` WHERE id=?`, eventID))
			if eventErr != nil {
				return result, wrapError(eventErr, "load news evidence event")
			}
			evidence := NewsThreadEvidence{
				ID: newsContextStableID("evidence_", version.ID, event.ID), ThreadID: threadID,
				VersionID: version.ID, RunID: runID, NewsEventID: event.ID, Source: event.Source,
				Title: event.Title, Summary: event.Summary, URL: sanitizeOpportunityURL(event.URL),
				ContentHash: event.DedupeKey, Relation: "support", EventAt: event.EventAt, CreatedAt: now,
			}
			if err := insertNewsThreadEvidenceTx(ctx, tx, evidence); err != nil {
				return result, wrapError(err, "insert news thread evidence in batch")
			}
			threadForNews[event.ID] = threadID
		}
	}
	if windowType == NewsContextWindowDaily {
		for _, threadID := range report.UnchangedThreadIDs {
			threadID = strings.TrimSpace(threadID)
			existing, getErr := scanNewsThread(tx.QueryRowContext(ctx, newsThreadSelectSQL+` WHERE id=?`, threadID))
			if getErr != nil {
				return result, wrapError(getErr, "load unchanged daily news thread")
			}
			checkpointResearchStatus := researchStatus
			if strings.TrimSpace(existing.CurrentVersionID) != "" {
				currentVersion, currentErr := scanNewsThreadVersion(tx.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE id=?`, existing.CurrentVersionID))
				if currentErr != nil {
					return result, wrapError(currentErr, "load current news thread version for daily checkpoint")
				}
				checkpointResearchStatus = newsContextWorseResearchStatus(checkpointResearchStatus, currentVersion.ResearchStatus)
			}
			versionID := newsContextStableID("threadver_", runID, threadID, "daily-unchanged")
			version, versionErr := scanNewsThreadVersion(tx.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE id=?`, versionID))
			if versionErr != nil && !errors.Is(versionErr, sql.ErrNoRows) {
				return result, wrapError(versionErr, "load unchanged daily news thread version")
			}
			if errors.Is(versionErr, sql.ErrNoRows) {
				version = normalizeNewsThreadVersion(NewsThreadVersion{
					ID: versionID, ThreadID: threadID, RunID: runID, AgentRunID: agentRunID,
					WindowType: windowType, VersionNo: existing.CurrentVersion + 1,
					Title: existing.Title, CoreThesis: existing.CoreThesis, Stage: existing.Stage,
					LatestChange: "每日复核：主题阶段保持不变", MaterialChange: false,
					Confidence: existing.Confidence, Industries: existing.Industries,
					Symbols: existing.Symbols, Funds: existing.Funds, Facts: existing.Facts,
					Inferences: existing.Inferences, CounterEvidence: existing.CounterEvidence,
					OpenQuestions: existing.OpenQuestions, Leaders: existing.Leaders,
					Followers: existing.Followers, Laggards: existing.Laggards,
					NextCandidates: existing.NextCandidates, Catalysts: existing.Catalysts,
					Invalidations: existing.Invalidations, Relations: existing.Relations,
					ResearchStatus: checkpointResearchStatus,
				})
				if err := insertNewsThreadVersionTx(ctx, tx, version); err != nil {
					return result, wrapError(err, "insert unchanged daily news thread version")
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_threads
				SET current_version=?, current_version_id=?, review_status=?, updated_at=? WHERE id=?`,
				version.VersionNo, version.ID, version.ReviewStatus, now, threadID); err != nil {
				return result, wrapError(err, "update unchanged daily news thread checkpoint")
			}
			versionForThread[threadID] = version.ID
		}
	}

	decisionByID := make(map[string]NewsContextNewsDecision, len(report.NewsDecisions))
	for _, decision := range report.NewsDecisions {
		decisionByID[decision.NewsEventID] = decision
	}
	for _, eventID := range report.ProcessedNewsIDs {
		decision := decisionByID[eventID]
		if researchProtectionReason != "" {
			decision.Disposition = NewsEventContextDeferred
			decision.Reason = researchProtectionReason
			decisionByID[eventID] = decision
		}
		status := NewsEventContextCovered
		switch strings.TrimSpace(decision.Disposition) {
		case NewsEventContextNoise, "duplicate":
			status = NewsEventContextNoise
			result.NoiseCount++
		case NewsEventContextDeferred:
			status = NewsEventContextDeferred
			result.DeferredCount++
		default:
			result.CoveredCount++
		}
		coveredAt := any(now)
		protectedReason := any(nil)
		if status == NewsEventContextDeferred {
			coveredAt = nil
			protectedReason = nullableString(decision.Reason)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_events SET
			context_status=?, context_run_id=?, context_covered_at=?, protected_reason=?, updated_at=? WHERE id=?`,
			status, runID, coveredAt, protectedReason, now, eventID); err != nil {
			return result, wrapError(err, "mark news event context in batch")
		}
		if decision.ThreadID != "" {
			threadForNews[eventID] = decision.ThreadID
		}
		result.ProcessedCount++
	}
	if err := tx.Commit(); err != nil {
		return result, wrapError(err, "commit news context batch")
	}

	markChanged := func(threadID, versionID string) error {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil
		}
		_, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
			SET status=?, disposition='changed', thread_id=?, version_id=?, error_message=NULL, updated_at=?
			WHERE run_id=? AND object_type=? AND object_id=? AND agent_run_id=? AND status=?`,
			NewsContextRunItemCompleted, threadID, nullableString(versionID), time.Now(),
			runID, NewsContextRunItemThread, threadID, agentRunID, NewsContextRunItemRunning)
		return wrapError(err, "complete changed news context thread item")
	}
	for _, change := range report.ThreadChanges {
		if err := markChanged(change.ThreadID, versionForThread[strings.TrimSpace(change.ThreadID)]); err != nil {
			return result, err
		}
		for _, sourceThreadID := range change.SourceThreadIDs {
			if sourceThreadID != change.ThreadID {
				if err := markChanged(sourceThreadID, ""); err != nil {
					return result, err
				}
			}
		}
	}
	for _, threadID := range report.UnchangedThreadIDs {
		threadID = strings.TrimSpace(threadID)
		update, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
			SET status=?, disposition='unchanged', thread_id=?, version_id=?, error_message=NULL, updated_at=?
			WHERE run_id=? AND object_type=? AND object_id=? AND agent_run_id=? AND status=?`,
			NewsContextRunItemCompleted, threadID, nullableString(versionForThread[threadID]), time.Now(),
			runID, NewsContextRunItemThread, threadID, agentRunID, NewsContextRunItemRunning)
		if err != nil {
			return result, wrapError(err, "complete unchanged news context thread item")
		}
		if rows, _ := update.RowsAffected(); rows != 1 {
			return result, ErrInvalidNewsContextResult
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
		SET status=?, disposition=CASE WHEN disposition IS NULL OR disposition='' THEN 'reviewed' ELSE disposition END,
		    error_message=NULL, updated_at=?
		WHERE run_id=? AND object_type=? AND agent_run_id=? AND status=?`,
		NewsContextRunItemCompleted, time.Now(), runID, NewsContextRunItemThread,
		agentRunID, NewsContextRunItemRunning); err != nil {
		return result, wrapError(err, "complete news context thread run items")
	}
	for _, eventID := range report.ProcessedNewsIDs {
		decision := decisionByID[eventID]
		threadID := threadForNews[eventID]
		if err := s.CompleteNewsContextRunItem(ctx, runID, eventID, decision.Disposition,
			threadID, versionForThread[threadID]); err != nil {
			return result, err
		}
	}
	return result, s.refreshNewsContextRunCounts(ctx, runID)
}

func (s *Store) refreshNewsContextRunCounts(ctx context.Context, runID string) error {
	run, err := s.GetNewsContextRun(ctx, runID)
	if err != nil {
		return err
	}
	unchangedThreadCount := 0
	err = s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN status=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND status=? AND disposition NOT IN (?, ?) THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND disposition=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND disposition=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND status=? AND disposition='unchanged' THEN 1 ELSE 0 END),0)
		FROM stockv2_news_context_run_items WHERE run_id=?`,
		NewsContextRunItemCompleted,
		NewsContextRunItemNewsEvent, NewsContextRunItemCompleted, NewsEventContextNoise, NewsEventContextDeferred,
		NewsContextRunItemNewsEvent, NewsEventContextNoise,
		NewsContextRunItemNewsEvent, NewsEventContextDeferred,
		NewsContextRunItemThread, NewsContextRunItemCompleted,
		runID).Scan(&run.InputCount, &run.ProcessedCount,
		&run.CoveredCount, &run.NoiseCount, &run.DeferredCount, &unchangedThreadCount)
	if err != nil {
		return wrapError(err, "refresh news context run item counts")
	}
	if err := s.marketDB.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN version_no=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN version_no>1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN material_change<>0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN counter_evidence_json<>'[]' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN COALESCE(research_status,'')<>'' THEN 1 ELSE 0 END),0)
		FROM stockv2_news_thread_versions WHERE run_id=?`, runID).Scan(
		&run.CreatedThreadCount, &run.UpdatedThreadCount, &run.MaterialChangeCount,
		&run.ConflictCount, &run.ResearchCount); err != nil {
		return wrapError(err, "refresh news context version counts")
	}
	run.UpdatedThreadCount -= unchangedThreadCount
	if run.UpdatedThreadCount < 0 {
		run.UpdatedThreadCount = 0
	}
	run.PendingCount = run.InputCount - run.ProcessedCount
	if run.PendingCount < 0 {
		run.PendingCount = 0
	}
	_, err = s.UpdateNewsContextRun(ctx, run)
	return err
}

func (s *Store) ListNewsEventsPendingContext(ctx context.Context, before time.Time, limit, offset int) ([]NewsEvent, error) {
	args := []any{NewsEventContextPending, NewsEventContextDeferred}
	where := ` WHERE COALESCE(context_status, 'pending') IN (?, ?)`
	if !before.IsZero() {
		where += ` AND event_at < ?`
		args = append(args, before)
	}
	args = append(args, normalizedPageLimit(limit, 1000), normalizedPageOffset(offset))
	rows, err := s.marketDB.db.QueryContext(ctx, newsEventSelectSQL()+where+`
		ORDER BY event_at ASC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news events pending context")
	}
	return scanRows(rows, scanNewsEvent, "scan pending context news event", "iterate pending context news events")
}

func (s *Store) MarkNewsEventContext(ctx context.Context, eventID, status, runID, protectedReason string, coveredAt time.Time) error {
	if coveredAt.IsZero() && status != NewsEventContextDeferred && status != NewsEventContextPending {
		coveredAt = time.Now()
	}
	result, err := s.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events SET
		context_status=?, context_run_id=?, context_covered_at=?, protected_reason=?, updated_at=? WHERE id=?`,
		status, nullableString(runID), nullableTime(coveredAt), nullableString(protectedReason), time.Now(), eventID)
	if err != nil {
		return wrapError(err, "mark news event context")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrNewsEventNotFound
	}
	return nil
}

func (s *Store) CountNewsEventsByContextStatus(ctx context.Context, status string) (int, error) {
	var count int
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_events
		WHERE COALESCE(context_status, 'pending')=?`, status).Scan(&count)
	return count, wrapError(err, "count news events by context status")
}

func (s *Store) HasRunningNewsContextCleanupRun(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_cleanup_runs WHERE status=?`, NewsContextCleanupRunning).Scan(&count)
	return count > 0, wrapError(err, "has running news context cleanup run")
}

func (s *Store) FailRunningNewsContextCleanupRuns(ctx context.Context, reason string) (int64, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_cleanup_runs
		SET status=?, phase='failed', error_message=?, finished_at=?, updated_at=?
		WHERE status=?`, NewsContextCleanupFailed, nullableString(reason), now, now, NewsContextCleanupRunning)
	if err != nil {
		return 0, wrapError(err, "fail running news context cleanup runs")
	}
	rows, err := result.RowsAffected()
	return rows, wrapError(err, "check failed news context cleanup runs")
}

func scanNewsContextCleanupCandidate(row rowScanner) (NewsContextCleanupCandidate, error) {
	var item NewsContextCleanupCandidate
	var linkProcessedAt, coveredAt sql.NullTime
	err := row.Scan(&item.Event.ID, &item.Event.RawNewsID, &item.Event.Source, &item.Event.ExternalID,
		&item.Event.Title, &item.Event.Summary, &item.Event.Content, &item.Event.URL,
		&item.Event.QualityStatus, &item.Event.DedupeKey, &item.Event.LinkStatus,
		&item.Event.EventAt, &linkProcessedAt, &item.Event.CreatedAt, &item.Event.UpdatedAt,
		&item.ContextStatus, &item.ContextRunID, &coveredAt, &item.ProtectedReason)
	assignNullTime(&item.Event.LinkProcessedAt, linkProcessedAt)
	assignNullTime(&item.ContextCoveredAt, coveredAt)
	return item, err
}

func (s *Store) ListNewsEventsForContextCleanup(ctx context.Context, before time.Time, afterID string, limit int) ([]NewsContextCleanupCandidate, error) {
	rows, err := s.marketDB.db.QueryContext(ctx, `
		SELECT id, COALESCE(raw_news_id,''), source, COALESCE(external_id,''), title,
		       COALESCE(summary,''), COALESCE(content,''), COALESCE(url,''),
		       COALESCE(quality_status,''), COALESCE(dedupe_key,''), link_status,
		       event_at, link_processed_at, created_at, updated_at,
		       COALESCE(context_status,'pending'), COALESCE(context_run_id,''),
		       context_covered_at, COALESCE(protected_reason,'')
		FROM stockv2_news_events
		WHERE COALESCE(context_status,'pending') IN (?, ?)
		  AND context_covered_at IS NOT NULL AND context_covered_at < ?
		  AND compacted_at IS NULL AND id > ?
		ORDER BY id ASC LIMIT ?
	`, NewsEventContextCovered, NewsEventContextNoise, before, afterID, normalizedPageLimit(limit, 1000))
	if err != nil {
		return nil, wrapError(err, "list news events for context cleanup")
	}
	return scanRows(rows, scanNewsContextCleanupCandidate, "scan context cleanup candidate", "iterate context cleanup candidates")
}

func (s *Store) ProtectNewsEventForCleanup(ctx context.Context, eventID, reason string) error {
	result, err := s.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events
		SET protected_reason=?, updated_at=? WHERE id=? AND compacted_at IS NULL`,
		nullableString(reason), time.Now(), eventID)
	if err != nil {
		return wrapError(err, "protect news event for cleanup")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrNewsEventNotFound
	}
	return nil
}

func (s *Store) NewsEventCleanupProtected(ctx context.Context, eventID string) (bool, string, error) {
	var status, linkStatus string
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'), COALESCE(link_status,'pending')
		FROM stockv2_news_events WHERE id=?`, eventID).Scan(&status, &linkStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return true, "消息不存在", nil
	}
	if err != nil {
		return false, "", wrapError(err, "get news event cleanup protection")
	}
	// protected_reason is only the last observed gate failure for UI diagnostics.
	// Live references are re-evaluated on every retry so resolved conditions do
	// not become permanent locks.
	if status != NewsEventContextCovered && status != NewsEventContextNoise {
		return true, "消息尚未完成归纳覆盖", nil
	}
	if linkStatus == NewsEventLinkStatusPending || linkStatus == NewsEventLinkStatusFailed {
		return true, "消息的股票关联尚未完成", nil
	}
	var activeCandidates int
	err = s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_link_candidates
		WHERE news_event_id=? AND COALESCE(monitor_status,'pending') IN (?, ?)`, eventID,
		NewsLinkMonitorStatusPending, NewsLinkMonitorStatusFailed).Scan(&activeCandidates)
	if err != nil {
		return false, "", wrapError(err, "check news event active candidates")
	}
	if activeCandidates > 0 {
		return true, "消息仍有待处理或失败的股票关联", nil
	}
	return false, "", nil
}

func (s *Store) DeleteNewsLinkCandidatesByEvent(ctx context.Context, eventID string) error {
	_, err := s.marketDB.db.ExecContext(ctx, `DELETE FROM stockv2_news_link_candidates WHERE news_event_id=?`, eventID)
	return wrapError(err, "delete news link candidates by event")
}

func (s *Store) CompactNewsEvent(ctx context.Context, eventID string) (int64, error) {
	protected, reason, err := s.NewsEventCleanupProtected(ctx, eventID)
	if err != nil {
		return 0, err
	}
	if protected {
		return 0, fmt.Errorf("%w: %s", ErrNewsContextReviewIncomplete, reason)
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, wrapError(err, "begin compact news event")
	}
	defer tx.Rollback()
	var event NewsEvent
	event, err = scanNewsEvent(tx.QueryRowContext(ctx, newsEventSelectSQL()+` WHERE id=?`, eventID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNewsEventNotFound
		}
		return 0, err
	}
	var alreadyCompacted sql.NullTime
	var priorBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT compacted_at, COALESCE(compacted_bytes,0)
		FROM stockv2_news_events WHERE id=?`, eventID).Scan(&alreadyCompacted, &priorBytes); err != nil {
		return 0, err
	}
	if alreadyCompacted.Valid {
		return 0, nil
	}
	released := int64(len([]byte(event.Summary)) + len([]byte(event.Content)) + len([]byte(event.URL)))
	if event.RawNewsID != "" {
		var content, snippet, rawPayload, rawURL string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(content,''), COALESCE(snippet,''),
			COALESCE(raw_payload_json,''), COALESCE(url,'') FROM stockv2_raw_news WHERE id=?`, event.RawNewsID).
			Scan(&content, &snippet, &rawPayload, &rawURL); err == nil {
			released += int64(len([]byte(content)) + len([]byte(snippet)) + len([]byte(rawPayload)) + len([]byte(rawURL)))
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_raw_news WHERE id=?`, event.RawNewsID); err != nil {
			return 0, err
		}
	}
	now := time.Now()
	result, err := tx.ExecContext(ctx, `UPDATE stockv2_news_events SET summary=NULL, content=NULL,
		url=NULL, context_status=?, compacted_at=?, compacted_bytes=?, protected_reason=NULL,
		updated_at=? WHERE id=? AND compacted_at IS NULL`, NewsEventContextCompacted, now, released, now, eventID)
	if err != nil {
		return 0, wrapError(err, "compact news event")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, wrapError(err, "commit compact news event")
	}
	return released, nil
}

func (s *Store) NewsContextStorageStats(ctx context.Context) (current, processed, compacted, protected int, releasedBytes int64, err error) {
	rows, queryErr := s.marketDB.db.QueryContext(ctx, `SELECT COALESCE(context_status,'pending'),
		COUNT(*), COALESCE(SUM(compacted_bytes),0),
		SUM(CASE WHEN COALESCE(protected_reason,'') <> '' THEN 1 ELSE 0 END)
		FROM stockv2_news_events GROUP BY COALESCE(context_status,'pending')`)
	if queryErr != nil {
		err = wrapError(queryErr, "get news context storage stats")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count, protectedCount int
		var bytes int64
		if scanErr := rows.Scan(&status, &count, &bytes, &protectedCount); scanErr != nil {
			err = scanErr
			return
		}
		if status != NewsEventContextCompacted {
			current += count
		}
		protected += protectedCount
		releasedBytes += bytes
		switch status {
		case NewsEventContextCovered, NewsEventContextNoise, NewsEventContextDeferred, NewsEventContextCompacted:
			processed += count
		}
		if status == NewsEventContextCompacted {
			compacted += count
		}
	}
	err = wrapError(rows.Err(), "iterate news context storage stats")
	return
}

func (s *Store) UpdateNewsThreadIndexStatus(ctx context.Context, threadID, status, errorMessage string) error {
	result, err := s.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_threads SET index_status=?,
		index_error=?, updated_at=? WHERE id=?`, status, nullableString(errorMessage), time.Now(), threadID)
	if err != nil {
		return wrapError(err, "update news thread index status")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrNewsThreadNotFound
	}
	return nil
}

func (s *Store) UpdateNewsThreadVersionIndexStatus(ctx context.Context, versionID, status, errorMessage string) error {
	result, err := s.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_thread_versions
		SET index_status=?, index_error=? WHERE id=?`, status, nullableString(errorMessage), versionID)
	if err != nil {
		return wrapError(err, "update news thread version index status")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrNewsThreadNotFound
	}
	return nil
}

func (s *Store) UpdateNewsThreadReviewStatusForRun(ctx context.Context, runID, status string, reviewedAt time.Time) error {
	if reviewedAt.IsZero() {
		reviewedAt = time.Now()
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin update news thread review status")
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT thread_id, id FROM stockv2_news_thread_versions WHERE run_id=?`, runID)
	if err != nil {
		return wrapError(err, "list reviewed news thread versions")
	}
	type currentVersion struct{ threadID, versionID string }
	versions := make([]currentVersion, 0)
	for rows.Next() {
		var item currentVersion
		if err := rows.Scan(&item.threadID, &item.versionID); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_thread_versions SET review_status=? WHERE run_id=?`, status, runID); err != nil {
		return wrapError(err, "update news thread version review status")
	}
	for _, item := range versions {
		if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_threads SET review_status=?,
			last_reviewed_at=?, updated_at=? WHERE id=? AND current_version_id=?`,
			status, reviewedAt, reviewedAt, item.threadID, item.versionID); err != nil {
			return wrapError(err, "update current news thread review status")
		}
	}
	return wrapError(tx.Commit(), "commit news thread review status")
}
