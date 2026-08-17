package stockv2

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
			hourly_interval_seconds INTEGER NOT NULL DEFAULT 3600,
			four_hour_interval_seconds INTEGER NOT NULL DEFAULT 14400,
			daily_interval_seconds INTEGER NOT NULL DEFAULT 86400,
			cleanup_grace_seconds INTEGER NOT NULL DEFAULT 86400,
			additional_research_prompt TEXT NOT NULL DEFAULT '',
			next_hourly_at DATETIME,
			next_four_hour_at DATETIME,
			next_daily_at DATETIME,
			last_run_at DATETIME,
			last_cleanup_at DATETIME,
			last_error TEXT,
			updated_at DATETIME NOT NULL
		);
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
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at DATETIME,
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
			source_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(run_id, object_type, object_id),
			FOREIGN KEY (run_id) REFERENCES stockv2_news_context_runs(id) ON DELETE CASCADE
		);
		DROP INDEX IF EXISTS idx_stockv2_news_context_run_items_run_status;
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_run_items_backfill_progress
			ON stockv2_news_context_run_items(run_id, status, object_type, object_id);

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
	`); err != nil {
		return wrapError(err, "ensure news context sqlite schema")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stockv2_news_context_config
		(id, enabled, auto_cleanup_enabled, hourly_enabled, four_hour_enabled,
		 daily_enabled, hourly_interval_seconds, four_hour_interval_seconds,
		 daily_interval_seconds, cleanup_grace_seconds, additional_research_prompt, updated_at)
		VALUES (?, 0, 0, 1, 1, 1, 3600, 14400, 86400, 86400, '', datetime('now'))`,
		NewsContextConfigIDDefault); err != nil {
		return wrapError(err, "seed news context config")
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
			effective_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			UNIQUE(thread_id, version_no),
			UNIQUE(thread_id, agent_run_id)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_versions_thread
			ON stockv2_news_thread_versions(thread_id, version_no);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_versions_run
			ON stockv2_news_thread_versions(run_id, material_change, created_at);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_versions_effective
			ON stockv2_news_thread_versions(thread_id, effective_at);

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
	return nil
}

func normalizeNewsContextConfigRecord(item NewsContextConfig) NewsContextConfig {
	if item.ID == "" {
		item.ID = NewsContextConfigIDDefault
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
	if !validNewsContextCleanupGrace(item.CleanupGraceSeconds) {
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
		       daily_enabled, hourly_interval_seconds,
		       four_hour_interval_seconds, daily_interval_seconds,
		       cleanup_grace_seconds, COALESCE(additional_research_prompt,''),
		       next_hourly_at, next_four_hour_at, next_daily_at,
		       last_run_at, last_cleanup_at, COALESCE(last_error,''), updated_at
		FROM stockv2_news_context_config WHERE id = ?
	`, NewsContextConfigIDDefault).Scan(
		&item.ID, &enabled, &autoCleanup, &hourly, &fourHour, &daily,
		&item.HourlyIntervalSeconds, &item.FourHourIntervalSeconds,
		&item.DailyIntervalSeconds, &item.CleanupGraceSeconds, &item.AdditionalResearchPrompt, &nextHourly,
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
			daily_enabled, hourly_interval_seconds,
			four_hour_interval_seconds, daily_interval_seconds, cleanup_grace_seconds,
			additional_research_prompt, next_hourly_at, next_four_hour_at, next_daily_at, last_run_at,
			last_cleanup_at, last_error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled=excluded.enabled, auto_cleanup_enabled=excluded.auto_cleanup_enabled,
			hourly_enabled=excluded.hourly_enabled, four_hour_enabled=excluded.four_hour_enabled,
			daily_enabled=excluded.daily_enabled,
			hourly_interval_seconds=excluded.hourly_interval_seconds,
			four_hour_interval_seconds=excluded.four_hour_interval_seconds,
			daily_interval_seconds=excluded.daily_interval_seconds,
			cleanup_grace_seconds=excluded.cleanup_grace_seconds,
			additional_research_prompt=excluded.additional_research_prompt,
			next_hourly_at=excluded.next_hourly_at, next_four_hour_at=excluded.next_four_hour_at,
			next_daily_at=excluded.next_daily_at, last_run_at=excluded.last_run_at,
			last_cleanup_at=excluded.last_cleanup_at, last_error=excluded.last_error,
			updated_at=excluded.updated_at
	`, item.ID, boolToInt(item.Enabled), boolToInt(item.AutoCleanupEnabled),
		boolToInt(item.HourlyEnabled), boolToInt(item.FourHourEnabled), boolToInt(item.DailyEnabled),
		item.HourlyIntervalSeconds, item.FourHourIntervalSeconds,
		item.DailyIntervalSeconds, item.CleanupGraceSeconds, item.AdditionalResearchPrompt, nullableTime(item.NextHourlyAt),
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
	       conflict_count, research_count, pending_count, retry_count, next_retry_at,
	       COALESCE(error_message,''),
	       COALESCE(requested_by,''), started_at, finished_at, created_at, updated_at
	FROM stockv2_news_context_runs`

func scanNewsContextRun(row rowScanner) (NewsContextRun, error) {
	var item NewsContextRun
	var nextRetryAt, startedAt, finishedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.WindowType, &item.TriggerType, &item.Status, &item.Phase,
		&item.WindowStart, &item.WindowEnd, &item.CurrentAgentRunID, &item.ReviewStatus,
		&item.ReviewRunID, &item.CleanupStatus, &item.CleanupRunID, &item.InputCount,
		&item.ProcessedCount, &item.CoveredCount, &item.NoiseCount, &item.DeferredCount,
		&item.CreatedThreadCount, &item.UpdatedThreadCount, &item.MaterialChangeCount,
		&item.ConflictCount, &item.ResearchCount, &item.PendingCount, &item.RetryCount,
		&nextRetryAt, &item.ErrorMessage,
		&item.RequestedBy, &startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	assignNullTime(&item.NextRetryAt, nextRetryAt)
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
			retry_count, next_retry_at,
			error_message, requested_by, started_at, finished_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		item.ConflictCount, item.ResearchCount, item.PendingCount, item.RetryCount,
		nullableTime(item.NextRetryAt), nullableString(item.ErrorMessage),
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

func (s *Store) IsNewsContextWindowComplete(ctx context.Context, windowType string, start, end time.Time) (bool, error) {
	if !validNewsContextWindowType(windowType) || start.IsZero() || !end.After(start) {
		return false, ErrInvalidNewsContextInput
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_runs
		WHERE window_type=? AND window_start=? AND window_end=? AND status IN (?,?)`,
		windowType, start, end, NewsContextRunStatusCompleted, NewsContextRunStatusWaitingReview).Scan(&count)
	return count > 0, wrapError(err, "check complete news context window")
}

func (s *Store) ResetPendingNewsContextRunManifest(ctx context.Context, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ErrInvalidNewsContextInput
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM stockv2_news_context_runs WHERE id=?`, runID).Scan(&status); err != nil {
			return wrapError(err, "load pending news context run")
		}
		if status != NewsContextRunStatusPending {
			return ErrInvalidNewsContextInput
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_news_context_run_items WHERE run_id=?`, runID); err != nil {
			return wrapError(err, "reset pending news context manifest")
		}
		_, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_runs SET
			input_count=0,processed_count=0,covered_count=0,noise_count=0,deferred_count=0,
			created_thread_count=0,updated_thread_count=0,material_change_count=0,
			conflict_count=0,research_count=0,pending_count=0,current_agent_run_id=NULL,
			error_message=NULL,updated_at=? WHERE id=?`, time.Now(), runID)
		return wrapError(err, "clear pending news context counters")
	})
}

func (s *Store) GetNewsContextRun(ctx context.Context, id string) (NewsContextRun, error) {
	item, err := scanNewsContextRun(s.db.QueryRowContext(ctx, newsContextRunSelectSQL+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsContextRunNotFound
	}
	return item, wrapError(err, "get news context run")
}

func (s *Store) BeginNewsContextReview(ctx context.Context, runID, reviewRunID string) (NewsContextRun, error) {
	runs, err := s.BeginNewsContextReviews(ctx, []string{runID}, reviewRunID, -1)
	if err != nil {
		return NewsContextRun{}, err
	}
	return runs[0], nil
}

func (s *Store) BeginNewsContextReviews(ctx context.Context, runIDs []string, reviewRunID string, retryCount int) ([]NewsContextRun, error) {
	reviewRunID = strings.TrimSpace(reviewRunID)
	uniqueRunIDs := uniqueNonEmptyStrings(runIDs)
	if len(uniqueRunIDs) == 0 || reviewRunID == "" {
		return nil, ErrInvalidNewsContextInput
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin news context review batch")
	}
	defer tx.Rollback()
	for _, runID := range uniqueRunIDs {
		query := `UPDATE stockv2_news_context_runs
			SET review_status=?, review_run_id=?, phase=?, error_message=NULL,
				next_retry_at=NULL, updated_at=?`
		args := []any{NewsContextReviewRunning, reviewRunID, "reviewing", now}
		if retryCount >= 0 {
			query += `, retry_count=?`
			args = append(args, retryCount)
		}
		query += ` WHERE id=? AND status=? AND review_status IN (?, ?)`
		args = append(args, runID, NewsContextRunStatusWaitingReview, NewsContextReviewPending, NewsContextReviewFailed)
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, wrapError(err, "begin news context review batch")
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil, ErrInvalidNewsContextInput
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapError(err, "commit news context review batch")
	}
	return s.ListNewsContextRunsByReviewRunID(ctx, reviewRunID)
}

func (s *Store) ListNewsContextRunsByReviewRunID(ctx context.Context, reviewRunID string) ([]NewsContextRun, error) {
	reviewRunID = strings.TrimSpace(reviewRunID)
	if reviewRunID == "" {
		return []NewsContextRun{}, nil
	}
	rows, err := s.db.QueryContext(ctx, newsContextRunSelectSQL+`
		WHERE review_run_id=? ORDER BY window_start ASC, created_at ASC`, reviewRunID)
	if err != nil {
		return nil, wrapError(err, "list news context runs by review run id")
	}
	return scanRows(rows, scanNewsContextRun, "scan linked news context run", "iterate linked news context runs")
}

func (s *Store) UpdateNewsContextRun(ctx context.Context, item NewsContextRun) (NewsContextRun, error) {
	item.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_news_context_runs SET
			trigger_type=?, status=?, phase=?, current_agent_run_id=?, review_status=?,
			review_run_id=?, cleanup_status=?, cleanup_run_id=?, input_count=?,
			processed_count=?, covered_count=?, noise_count=?, deferred_count=?,
			created_thread_count=?, updated_thread_count=?, material_change_count=?,
			conflict_count=?, research_count=?, pending_count=?, retry_count=?,
			next_retry_at=?, error_message=?,
			requested_by=?, started_at=?, finished_at=?, updated_at=?
		WHERE id=?
	`, item.TriggerType, item.Status, nullableString(item.Phase), nullableString(item.CurrentAgentRunID),
		item.ReviewStatus, nullableString(item.ReviewRunID), item.CleanupStatus,
		nullableString(item.CleanupRunID), item.InputCount, item.ProcessedCount,
		item.CoveredCount, item.NoiseCount, item.DeferredCount, item.CreatedThreadCount,
		item.UpdatedThreadCount, item.MaterialChangeCount, item.ConflictCount,
		item.ResearchCount, item.PendingCount, item.RetryCount, nullableTime(item.NextRetryAt),
		nullableString(item.ErrorMessage),
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
		WHERE status=? AND (? = '' OR window_type = ?)`, NewsContextRunStatusRunning,
		strings.TrimSpace(windowType), strings.TrimSpace(windowType)).Scan(&count)
	return count > 0, wrapError(err, "has running news context run")
}

func (s *Store) FailRunningNewsContextRuns(ctx context.Context, reason string) (int64, error) {
	now := time.Now()
	// A backfill reuses its persisted run manifest after restart. Reset only the
	// interrupted model slice; an explicitly paused parent remains paused.
	if _, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items SET
		status=?, agent_run_id=NULL, error_message=NULL, updated_at=?
		WHERE status=? AND run_id IN (
			SELECT current_run_id FROM stockv2_news_context_backfills
			WHERE current_run_id IS NOT NULL AND status IN (?,?)
		)`, NewsContextRunItemPending, now, NewsContextRunItemRunning,
		NewsContextBackfillStatusRunning, NewsContextBackfillStatusPaused); err != nil {
		return 0, wrapError(err, "recover interrupted news context backfill items")
	}
	recovered, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_runs SET
		status=?, phase=CASE WHEN phase IN (?,?,?) THEN phase ELSE 'queued' END,
		current_agent_run_id=NULL, error_message=NULL,
		finished_at=NULL, updated_at=? WHERE status=? AND id IN (
			SELECT current_run_id FROM stockv2_news_context_backfills
			WHERE current_run_id IS NOT NULL AND status IN (?,?)
		)`, NewsContextRunStatusPending, newsContextRunPhaseAggregating,
		newsContextRunPhaseCheckpoint, newsContextRunPhaseMaterialize, now, NewsContextRunStatusRunning,
		NewsContextBackfillStatusRunning, NewsContextBackfillStatusPaused)
	if err != nil {
		return 0, wrapError(err, "recover interrupted news context backfill run")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_runs
		SET status=?, error_message=?, finished_at=?, updated_at=?
		WHERE status=?`, NewsContextRunStatusFailed, nullableString(reason), now, now,
		NewsContextRunStatusRunning)
	if err != nil {
		return 0, wrapError(err, "fail running news context runs")
	}
	rows, _ := result.RowsAffected()
	recoveredRows, _ := recovered.RowsAffected()
	return rows + recoveredRows, nil
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
	if strings.TrimSpace(filter.Status) == NewsContextRunStatusFailed {
		parts = append(parts, "(status = ? OR review_status = ?)")
		args = append(args, NewsContextRunStatusFailed, NewsContextReviewFailed)
	} else {
		add("status", filter.Status)
	}
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
	       COALESCE(error_message,''), source_at, created_at, updated_at
	FROM stockv2_news_context_run_items`

func scanNewsContextRunItem(row rowScanner) (NewsContextRunItem, error) {
	var item NewsContextRunItem
	var sourceAt sql.NullTime
	err := row.Scan(&item.ID, &item.RunID, &item.ObjectType, &item.ObjectID, &item.Status,
		&item.Disposition, &item.ThreadID, &item.VersionID, &item.AgentRunID,
		&item.ErrorMessage, &sourceAt, &item.CreatedAt, &item.UpdatedAt)
	assignNullTime(&item.SourceAt, sourceAt)
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
					 version_id, agent_run_id, error_message, source_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(run_id, object_type, object_id) DO NOTHING
			`, item.ID, item.RunID, item.ObjectType, item.ObjectID, item.Status,
				nullableString(item.Disposition), nullableString(item.ThreadID),
				nullableString(item.VersionID), nullableString(item.AgentRunID),
				nullableString(item.ErrorMessage), nullableTime(item.SourceAt), item.CreatedAt, item.UpdatedAt)
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
		ORDER BY CASE WHEN object_type='news_event' THEN 0 ELSE 1 END,
		CASE WHEN object_type='news_event' THEN COALESCE(source_at,created_at) END ASC,
		CASE WHEN object_type='news_thread' THEN COALESCE(NULLIF(thread_id,''),object_id) END ASC,
		COALESCE(source_at,created_at) ASC,id ASC LIMIT ? OFFSET ?`, args...)
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

func (s *Store) HasCompletedDiscardedNewsContextRunItem(ctx context.Context, runID, newsEventID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_run_items
		WHERE run_id=? AND object_type=? AND object_id=? AND status=? AND disposition IN (?,?)`,
		strings.TrimSpace(runID), NewsContextRunItemNewsEvent, strings.TrimSpace(newsEventID),
		NewsContextRunItemCompleted, NewsEventContextNoise, "duplicate").Scan(&count)
	return count > 0, wrapError(err, "check completed discarded news context run item")
}

func (s *Store) ListNewsContextRunOutputVersionIDs(ctx context.Context, runID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version_id
		FROM stockv2_news_context_run_items
		WHERE run_id=? AND status=? AND TRIM(COALESCE(version_id,''))<>''
		GROUP BY version_id
		ORDER BY MIN(COALESCE(source_at,created_at)) ASC,version_id ASC`,
		strings.TrimSpace(runID), NewsContextRunItemCompleted)
	if err != nil {
		return nil, wrapError(err, "list news context run output versions")
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapError(err, "scan news context run output version")
		}
		ids = append(ids, id)
	}
	return ids, wrapError(rows.Err(), "iterate news context run output versions")
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

func (s *Store) ResetNewsContextRunItemsForAgent(ctx context.Context, runID, agentRunID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
		SET status=?, agent_run_id=NULL, error_message=NULL, updated_at=?
		WHERE run_id=? AND agent_run_id=? AND status=?`, NewsContextRunItemPending, time.Now(),
		strings.TrimSpace(runID), strings.TrimSpace(agentRunID), NewsContextRunItemRunning)
	if err != nil {
		return wrapError(err, "reset news context retry items")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrInvalidNewsContextInput
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
	stored, err := s.GetNewsThread(ctx, item.ID)
	if err != nil {
		return item, err
	}
	if newsThreadEmbeddingIndexable(stored) {
		if err := s.EnsureEmbeddingWork(ctx, EmbeddingObjectNewsThread, stored.ID); err != nil {
			return item, err
		}
	}
	return stored, nil
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
	stored, err := s.GetNewsThread(ctx, item.ID)
	if err != nil {
		return item, err
	}
	if newsThreadEmbeddingIndexable(stored) {
		if err := s.QueueEmbeddingWork(ctx, EmbeddingObjectNewsThread, stored.ID); err != nil {
			return item, err
		}
	}
	return stored, nil
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
	       review_status, index_status, COALESCE(index_error,''),
	       COALESCE(effective_at, created_at), created_at
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
		&item.ReviewStatus, &item.IndexStatus, &item.IndexError, &item.EffectiveAt, &item.CreatedAt)
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
	if item.EffectiveAt.IsZero() {
		item.EffectiveAt = item.CreatedAt
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
		item.EvidenceCount, item.ReviewStatus, item.IndexStatus, nullableString(item.IndexError),
		item.EffectiveAt, item.CreatedAt,
	}
}

func insertNewsThreadVersionTx(ctx context.Context, tx *sql.Tx, item NewsThreadVersion) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_thread_versions (
		id,thread_id,run_id,agent_run_id,window_type,version_no,title,core_thesis,stage,
		latest_change,material_change,confidence,industries_json,symbols_json,funds_json,
		facts_json,inferences_json,counter_evidence_json,open_questions_json,leaders_json,
		followers_json,laggards_json,next_candidates_json,catalysts_json,invalidations_json,
		relations_json,research_status,evidence_count,review_status,index_status,index_error,effective_at,created_at
	) VALUES (`+sqlPlaceholders(33)+`) ON CONFLICT DO NOTHING`, newsThreadVersionArgs(item)...)
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
	var stored NewsThreadVersion
	if item.AgentRunID != "" {
		row := s.marketDB.db.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE thread_id=? AND agent_run_id=?`, item.ThreadID, item.AgentRunID)
		stored, err = scanNewsThreadVersion(row)
	} else {
		row := s.marketDB.db.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE thread_id=? AND version_no=?`, item.ThreadID, item.VersionNo)
		stored, err = scanNewsThreadVersion(row)
	}
	if err != nil {
		return item, err
	}
	if newsThreadVersionEmbeddingIndexable(stored) {
		if err := s.EnsureEmbeddingWork(ctx, EmbeddingObjectNewsThreadVersion, stored.ID); err != nil {
			return item, err
		}
	}
	return stored, nil
}

// MaterializeNewsContextDailyVersions copies one latest changed-theme snapshot
// per stable theme into an explicit daily checkpoint. The source version is
// part of the stable id: a restart is idempotent, while a late four-hour change
// creates a new immutable daily version when the same window is rebuilt.
func (s *Store) MaterializeNewsContextDailyVersions(
	ctx context.Context,
	runID string,
	effectiveAt time.Time,
	sources []NewsThreadVersion,
) ([]NewsThreadVersion, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || effectiveAt.IsZero() {
		return nil, ErrInvalidNewsContextInput
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin materialize daily news context versions")
	}
	defer tx.Rollback()
	now := time.Now()
	materialized := make([]NewsThreadVersion, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		threadID := strings.TrimSpace(source.ThreadID)
		if threadID == "" {
			return nil, ErrInvalidNewsContextInput
		}
		if _, exists := seen[threadID]; exists {
			return nil, fmt.Errorf("%w: duplicate daily checkpoint theme", ErrInvalidNewsContextInput)
		}
		seen[threadID] = struct{}{}
		versionID := newsContextStableID("threadver_daily_", runID, threadID, source.ID)
		version, versionErr := scanNewsThreadVersion(tx.QueryRowContext(ctx,
			newsThreadVersionSelectSQL+` WHERE id=?`, versionID))
		if versionErr != nil && !errors.Is(versionErr, sql.ErrNoRows) {
			return nil, wrapError(versionErr, "load materialized daily news context version")
		}
		if errors.Is(versionErr, sql.ErrNoRows) {
			var versionNo int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0)+1
				FROM stockv2_news_thread_versions WHERE thread_id=?`, threadID).Scan(&versionNo); err != nil {
				return nil, wrapError(err, "allocate materialized daily version number")
			}
			version = source
			version.ID = versionID
			version.RunID = runID
			version.AgentRunID = ""
			version.WindowType = NewsContextWindowDaily
			version.VersionNo = versionNo
			version.MaterialChange = false
			version.ReviewStatus = NewsContextReviewPending
			version.IndexStatus = NewsContextIndexPending
			version.IndexError = ""
			version.EffectiveAt = effectiveAt
			version.CreatedAt = now
			version = normalizeNewsThreadVersion(version)
			if err := insertNewsThreadVersionTx(ctx, tx, version); err != nil {
				return nil, wrapError(err, "insert materialized daily news context version")
			}
		}
		current, currentErr := scanNewsThreadVersion(tx.QueryRowContext(ctx,
			newsThreadVersionSelectSQL+` WHERE id=(SELECT current_version_id FROM stockv2_news_threads WHERE id=?)`, threadID))
		if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
			return nil, wrapError(currentErr, "load current version before daily materialization")
		}
		if errors.Is(currentErr, sql.ErrNoRows) || !current.EffectiveAt.After(version.EffectiveAt) {
			result, err := tx.ExecContext(ctx, `UPDATE stockv2_news_threads SET
				current_version=?,current_version_id=?,review_status=?,index_status=?,index_error=NULL,updated_at=?
				WHERE id=?`, version.VersionNo, version.ID, version.ReviewStatus,
				version.IndexStatus, now, threadID)
			if err != nil {
				return nil, wrapError(err, "promote materialized daily news context version")
			}
			if rows, _ := result.RowsAffected(); rows == 0 {
				return nil, ErrNewsThreadNotFound
			}
		}
		materialized = append(materialized, version)
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapError(err, "commit materialized daily news context versions")
	}
	work := make([]embeddingWorkItem, 0, len(materialized)*2)
	for _, version := range materialized {
		work = append(work,
			embeddingWorkItem{ObjectType: EmbeddingObjectNewsThread, ObjectID: version.ThreadID},
			embeddingWorkItem{ObjectType: EmbeddingObjectNewsThreadVersion, ObjectID: version.ID},
		)
	}
	if err := s.QueueEmbeddingWorkItems(ctx, work); err != nil {
		return nil, err
	}
	sort.Slice(materialized, func(i, j int) bool { return materialized[i].ThreadID < materialized[j].ThreadID })
	return materialized, nil
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
		parts = append(parts, "COALESCE(effective_at,created_at)>=?")
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		parts = append(parts, "COALESCE(effective_at,created_at)<?")
		args = append(args, filter.Until)
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListNewsThreadVersions(ctx context.Context, filter NewsThreadVersionListFilter) ([]NewsThreadVersion, error) {
	where, args := newsThreadVersionWhere(filter)
	args = append(args, normalizedPageLimit(filter.Limit, 500), normalizedPageOffset(filter.Offset))
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadVersionSelectSQL+` WHERE `+where+`
		ORDER BY COALESCE(effective_at,created_at) DESC, version_no DESC,
		thread_id ASC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news thread versions")
	}
	return scanRows(rows, scanNewsThreadVersion, "scan news thread version", "iterate news thread versions")
}

func (s *Store) ListNewsContextRunVersionsNeedingIndex(ctx context.Context, runID string, limit int) ([]NewsThreadVersion, error) {
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadVersionSelectSQL+`
		WHERE run_id=? AND index_status<>?
		ORDER BY COALESCE(effective_at,created_at) ASC,version_no ASC,thread_id ASC,id ASC
		LIMIT ?`, strings.TrimSpace(runID), NewsContextIndexReady, normalizedPageLimit(limit, 100))
	if err != nil {
		return nil, wrapError(err, "list news context run versions needing index")
	}
	return scanRows(rows, scanNewsThreadVersion,
		"scan news context run version needing index", "iterate news context run versions needing index")
}

func (s *Store) ListLatestNewsThreadVersionsAt(ctx context.Context, at time.Time, limit, offset int) ([]NewsThreadVersion, error) {
	if at.IsZero() {
		return nil, ErrInvalidNewsContextInput
	}
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadVersionSelectSQL+`
		WHERE COALESCE(effective_at,created_at)<=?
		QUALIFY ROW_NUMBER() OVER (
			PARTITION BY thread_id ORDER BY COALESCE(effective_at,created_at) DESC,version_no DESC,id DESC
		)=1
		ORDER BY thread_id ASC LIMIT ? OFFSET ?`, at, normalizedPageLimit(limit, 500), normalizedPageOffset(offset))
	if err != nil {
		return nil, wrapError(err, "list latest news thread versions at time")
	}
	return scanRows(rows, scanNewsThreadVersion, "scan latest historical news thread version", "iterate latest historical news thread versions")
}

func (s *Store) ListLatestNewsThreadVersionsAtForSearch(ctx context.Context, at time.Time) ([]NewsThreadVersion, error) {
	if at.IsZero() {
		return nil, ErrInvalidNewsContextInput
	}
	rows, err := s.marketDB.db.QueryContext(ctx, newsThreadVersionSelectSQL+`
		WHERE COALESCE(effective_at,created_at)<=?
		QUALIFY ROW_NUMBER() OVER (
			PARTITION BY thread_id ORDER BY COALESCE(effective_at,created_at) DESC,version_no DESC,id DESC
		)=1
		ORDER BY thread_id`, at)
	if err != nil {
		return nil, wrapError(err, "list latest news thread versions for historical search")
	}
	return scanRows(rows, scanNewsThreadVersion, "scan latest search news thread version", "iterate latest search news thread versions")
}

func (s *Store) ListNewsThreadVersionsForEmbeddingAssetsAt(ctx context.Context, at time.Time, objectIDs []string) ([]NewsThreadVersion, error) {
	if at.IsZero() {
		return nil, ErrInvalidNewsContextInput
	}
	const batchSize = 500
	out := make([]NewsThreadVersion, 0, len(objectIDs))
	for start := 0; start < len(objectIDs); start += batchSize {
		end := start + batchSize
		if end > len(objectIDs) {
			end = len(objectIDs)
		}
		args := make([]any, 0, 1+end-start)
		args = append(args, at)
		for _, id := range objectIDs[start:end] {
			args = append(args, id)
		}
		rows, err := s.marketDB.db.QueryContext(ctx, newsThreadVersionSelectSQL+`
			WHERE COALESCE(effective_at,created_at)<=?
			  AND id IN (`+sqlPlaceholders(end-start)+`)`, args...)
		if err != nil {
			return nil, wrapError(err, "list news thread versions for embedding assets")
		}
		page, err := scanRows(rows, scanNewsThreadVersion, "scan embedded news thread version", "iterate embedded news thread versions")
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
	}
	return out, nil
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
	SELECT e.id, e.thread_id, e.version_id, e.run_id, COALESCE(e.news_event_id,''),
	       COALESCE(e.source,''), e.title, COALESCE(e.summary,''), COALESCE(e.url,''),
	       COALESCE(e.content_hash,''), COALESCE(e.relation,''), e.event_at,
	       CASE
	         WHEN TRIM(COALESCE(e.news_event_id,''))='' OR n.id IS NULL THEN NULL
	         WHEN n.compacted_at IS NOT NULL OR COALESCE(n.context_status,'pending')='compacted' THEN TRUE
	         ELSE FALSE
	       END,
	       e.created_at
	FROM stockv2_news_thread_evidence e
	LEFT JOIN stockv2_news_events n ON n.id=e.news_event_id`

func scanNewsThreadEvidence(row rowScanner) (NewsThreadEvidence, error) {
	var item NewsThreadEvidence
	var eventAt sql.NullTime
	var originalNewsDeleted sql.NullBool
	err := row.Scan(&item.ID, &item.ThreadID, &item.VersionID, &item.RunID, &item.NewsEventID,
		&item.Source, &item.Title, &item.Summary, &item.URL, &item.ContentHash,
		&item.Relation, &eventAt, &originalNewsDeleted, &item.CreatedAt)
	assignNullTime(&item.EventAt, eventAt)
	if originalNewsDeleted.Valid {
		item.OriginalNewsDeleted = &originalNewsDeleted.Bool
	}
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
		{"e.thread_id", filter.ThreadID}, {"e.version_id", filter.VersionID},
		{"e.run_id", filter.RunID}, {"e.news_event_id", filter.NewsEventID},
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
		ORDER BY e.created_at DESC, e.id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list news thread evidence")
	}
	return scanRows(rows, scanNewsThreadEvidence, "scan news thread evidence", "iterate news thread evidence")
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

func (s *Store) ListNewsContextChangedThreadsForRuns(ctx context.Context, runIDs []string, limit, offset int) ([]NewsThreadChange, error) {
	runIDs = uniqueNonEmptyStrings(runIDs)
	if len(runIDs) == 0 {
		return []NewsThreadChange{}, nil
	}
	args := make([]any, 0, len(runIDs)+2)
	for _, runID := range runIDs {
		args = append(args, runID)
	}
	args = append(args, normalizedPageLimit(limit, 1000), normalizedPageOffset(offset))
	rows, err := s.marketDB.db.QueryContext(ctx, `WITH ranked AS (
		SELECT run_id, thread_id, id, title, stage, COALESCE(latest_change,'') AS latest_change,
			MAX(CASE WHEN material_change THEN 1 ELSE 0 END) OVER (PARTITION BY thread_id) AS material_change,
			COUNT(*) OVER (PARTITION BY thread_id) AS change_count, created_at,
			ROW_NUMBER() OVER (PARTITION BY thread_id ORDER BY created_at DESC, id DESC) AS row_no
		FROM stockv2_news_thread_versions WHERE run_id IN (`+sqlPlaceholders(len(runIDs))+`)
	)
	SELECT run_id, thread_id, id, title, stage, latest_change, material_change, change_count, created_at
	FROM ranked WHERE row_no=1 ORDER BY created_at ASC, thread_id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list merged news context changed threads")
	}
	defer rows.Close()
	out := make([]NewsThreadChange, 0)
	for rows.Next() {
		var item NewsThreadChange
		var material int
		if err := rows.Scan(&item.RunID, &item.ThreadID, &item.VersionID, &item.Title,
			&item.Stage, &item.LatestChange, &material, &item.ChangeCount, &item.CreatedAt); err != nil {
			return nil, wrapError(err, "scan merged news context changed thread")
		}
		item.MaterialChange = material != 0
		out = append(out, item)
	}
	return out, wrapError(rows.Err(), "iterate merged news context changed threads")
}

func (s *Store) NewsContextChangedThreadCountsForRuns(ctx context.Context, runIDs []string) (int, int, error) {
	runIDs = uniqueNonEmptyStrings(runIDs)
	if len(runIDs) == 0 {
		return 0, 0, nil
	}
	args := make([]any, 0, len(runIDs))
	for _, runID := range runIDs {
		args = append(args, runID)
	}
	var changedCount, materialChangeCount int
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(material_change),0) FROM (
		SELECT thread_id, MAX(CASE WHEN material_change THEN 1 ELSE 0 END) AS material_change
		FROM stockv2_news_thread_versions WHERE run_id IN (`+sqlPlaceholders(len(runIDs))+`)
		GROUP BY thread_id
	)`, args...).Scan(&changedCount, &materialChangeCount)
	return changedCount, materialChangeCount, wrapError(err, "count merged news context changed threads")
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
	report.NewsDecisions = append([]NewsContextNewsDecision(nil), report.NewsDecisions...)
	for index := range report.NewsDecisions {
		disposition, ok := normalizeNewsContextDisposition(report.NewsDecisions[index].Disposition)
		if !ok {
			return result, ErrInvalidNewsContextResult
		}
		report.NewsDecisions[index].Disposition = disposition
	}
	logicalRun, err := s.GetNewsContextRun(ctx, runID)
	if err != nil {
		return result, err
	}
	batchRows, err := s.db.QueryContext(ctx, newsContextRunItemSelectSQL+`
		WHERE run_id=? AND agent_run_id=?
		ORDER BY COALESCE(source_at,created_at) ASC,object_id ASC`,
		runID, agentRunID)
	if err != nil {
		return result, wrapError(err, "list news context batch items")
	}
	batchItems, err := scanRows(batchRows, scanNewsContextRunItem,
		"scan news context batch item", "iterate news context batch items")
	if err != nil {
		return result, err
	}
	if err := validateNewsContextBatchNewsCoverage(batchItems, report); err != nil {
		return result, err
	}
	effectiveAt := logicalRun.WindowEnd
	if effectiveAt.IsZero() {
		effectiveAt = time.Now()
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return result, wrapError(err, "begin apply news context batch")
	}
	defer tx.Rollback()
	now := time.Now()
	threadForNews := make(map[string]string)
	versionForThread := make(map[string]string)
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
			var versionNo int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0)+1
				FROM stockv2_news_thread_versions WHERE thread_id=?`, threadID).Scan(&versionNo); err != nil {
				return result, wrapError(err, "allocate news thread version number")
			}
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
				ResearchStatus: newsContextWorseResearchStatus(NewsContextResearchNotRequired, change.ResearchStatus), EvidenceCount: len(change.EvidenceNewsIDs),
				EffectiveAt: effectiveAt,
			})
			if windowType == NewsContextWindowHourly && !change.MaterialChange {
				version.ReviewStatus = NewsContextReviewNotRequired
			}
			if err := insertNewsThreadVersionTx(ctx, tx, version); err != nil {
				return result, wrapError(err, "insert news thread version in batch")
			}
		}
		promoteCurrent := errors.Is(getErr, sql.ErrNoRows) || strings.TrimSpace(existing.CurrentVersionID) == ""
		if !promoteCurrent {
			currentVersion, currentErr := scanNewsThreadVersion(tx.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE id=?`, existing.CurrentVersionID))
			if currentErr != nil {
				return result, wrapError(currentErr, "load current news thread version time")
			}
			promoteCurrent = !currentVersion.EffectiveAt.After(version.EffectiveAt)
		}
		firstSeenAt := existing.FirstSeenAt
		if firstSeenAt.IsZero() {
			firstSeenAt = effectiveAt
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
			FirstSeenAt: firstSeenAt, LastChangedAt: effectiveAt, CreatedAt: existing.CreatedAt,
		})
		if promoteCurrent {
			if err := upsertNewsThreadTx(ctx, tx, thread); err != nil {
				return result, wrapError(err, "upsert news thread in batch")
			}
		}
		if promoteCurrent && strings.TrimSpace(change.Action) == "merge" {
			for _, sourceThreadID := range change.SourceThreadIDs {
				sourceThreadID = strings.TrimSpace(sourceThreadID)
				if sourceThreadID == "" || sourceThreadID == threadID {
					continue
				}
				sourceThread, sourceErr := scanNewsThread(tx.QueryRowContext(ctx, newsThreadSelectSQL+` WHERE id=?`, sourceThreadID))
				if errors.Is(sourceErr, sql.ErrNoRows) {
					return result, fmt.Errorf("%w: merged source news thread does not exist", ErrInvalidNewsContextResult)
				}
				if sourceErr != nil {
					return result, wrapError(sourceErr, "load merged source news thread")
				}
				if strings.TrimSpace(sourceThread.CurrentVersionID) != "" {
					sourceVersion, sourceVersionErr := scanNewsThreadVersion(tx.QueryRowContext(ctx, newsThreadVersionSelectSQL+` WHERE id=?`, sourceThread.CurrentVersionID))
					if sourceVersionErr != nil {
						return result, wrapError(sourceVersionErr, "load merged source current version")
					}
					if sourceVersion.EffectiveAt.After(version.EffectiveAt) {
						continue
					}
				}
				update, err := tx.ExecContext(ctx, `UPDATE stockv2_news_threads
					SET status=?, last_changed_at=?, updated_at=? WHERE id=?`,
					NewsThreadStatusMerged, effectiveAt, now, sourceThreadID)
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
	decisionByID := make(map[string]NewsContextNewsDecision, len(report.NewsDecisions))
	for _, decision := range report.NewsDecisions {
		decisionByID[decision.NewsEventID] = decision
	}
	for _, eventID := range report.ProcessedNewsIDs {
		decision := decisionByID[eventID]
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
		result.ProcessedCount++
	}
	if err := tx.Commit(); err != nil {
		return result, wrapError(err, "commit news context batch")
	}
	work := make([]embeddingWorkItem, 0, len(result.ChangedThreadIDs)+len(result.ChangedVersionIDs))
	for _, id := range result.ChangedThreadIDs {
		work = append(work, embeddingWorkItem{ObjectType: EmbeddingObjectNewsThread, ObjectID: id})
	}
	for _, id := range result.ChangedVersionIDs {
		work = append(work, embeddingWorkItem{ObjectType: EmbeddingObjectNewsThreadVersion, ObjectID: id})
	}
	if err := s.QueueEmbeddingWorkItems(ctx, work); err != nil {
		return result, err
	}

	markChanged := func(threadID, versionID string) error {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil
		}
		_, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
			SET status=?, disposition='changed', thread_id=?, version_id=COALESCE(?,version_id), error_message=NULL, updated_at=?
			WHERE run_id=? AND object_type=? AND COALESCE(NULLIF(thread_id,''),object_id)=? AND agent_run_id=? AND status=?`,
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
			SET status=?, disposition='unchanged', thread_id=?, version_id=COALESCE(?,version_id), error_message=NULL, updated_at=?
			WHERE run_id=? AND object_type=? AND COALESCE(NULLIF(thread_id,''),object_id)=? AND agent_run_id=? AND status=?`,
			NewsContextRunItemCompleted, threadID, nullableString(versionForThread[threadID]), time.Now(),
			runID, NewsContextRunItemThread, threadID, agentRunID, NewsContextRunItemRunning)
		if err != nil {
			return result, wrapError(err, "complete unchanged news context thread item")
		}
		if rows, _ := update.RowsAffected(); rows < 1 {
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
		COALESCE(SUM(CASE WHEN object_type=? AND status=? AND disposition NOT IN (?, ?, ?) THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND disposition IN (?, ?) THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND disposition=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN object_type=? AND status=? AND disposition='unchanged' THEN 1 ELSE 0 END),0)
		FROM stockv2_news_context_run_items WHERE run_id=?`,
		NewsContextRunItemCompleted,
		NewsContextRunItemNewsEvent, NewsContextRunItemCompleted, NewsEventContextNoise, "duplicate", NewsEventContextDeferred,
		NewsContextRunItemNewsEvent, NewsEventContextNoise, "duplicate",
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

type newsContextCleanupQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func newsEventCleanupProtected(ctx context.Context, queryer newsContextCleanupQueryer, eventID string) (bool, string, error) {
	var status, linkStatus string
	err := queryer.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'), COALESCE(link_status,'pending')
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
	return false, "", nil
}

func (s *Store) NewsEventCleanupProtected(ctx context.Context, eventID string) (bool, string, error) {
	return newsEventCleanupProtected(ctx, s.marketDB.db, eventID)
}

func (s *Store) CompactNewsEvent(ctx context.Context, eventID string) (int64, error) {
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, wrapError(err, "begin compact news event")
	}
	defer tx.Rollback()
	protected, reason, err := newsEventCleanupProtected(ctx, tx, eventID)
	if err != nil {
		return 0, err
	}
	if protected {
		return 0, fmt.Errorf("%w: %s", ErrNewsContextReviewIncomplete, reason)
	}
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_news_link_candidates WHERE news_event_id=?`, eventID); err != nil {
		return 0, wrapError(err, "delete news link candidates for compact event")
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
