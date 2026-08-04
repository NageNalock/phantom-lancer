package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) ensureNewsContextBackfillSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_news_context_backfills (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			phase TEXT NOT NULL,
			owner_revision INTEGER NOT NULL DEFAULT 1,
			range_start_at DATETIME,
			cutoff_at DATETIME NOT NULL,
			total_news_count INTEGER NOT NULL DEFAULT 0,
			processed_news_count INTEGER NOT NULL DEFAULT 0,
			remaining_news_count INTEGER NOT NULL DEFAULT 0,
			missing_news_count INTEGER NOT NULL DEFAULT 0,
			completed_chunk_count INTEGER NOT NULL DEFAULT 0,
			current_window_start DATETIME,
			current_window_end DATETIME,
			current_run_id TEXT,
			final_review_run_id TEXT,
			error_message TEXT,
			requested_by TEXT,
			started_at DATETIME,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_backfills_status
			ON stockv2_news_context_backfills(status, updated_at);
		CREATE TABLE IF NOT EXISTS stockv2_news_context_backfill_runs (
			backfill_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (backfill_id, run_id),
			FOREIGN KEY (backfill_id) REFERENCES stockv2_news_context_backfills(id) ON DELETE CASCADE,
			FOREIGN KEY (run_id) REFERENCES stockv2_news_context_runs(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS stockv2_news_context_backfill_reviewed_versions (
			backfill_id TEXT NOT NULL,
			daily_run_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			version_id TEXT NOT NULL,
			final_review_run_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (backfill_id, daily_run_id, version_id),
			FOREIGN KEY (backfill_id) REFERENCES stockv2_news_context_backfills(id) ON DELETE CASCADE,
			FOREIGN KEY (daily_run_id) REFERENCES stockv2_news_context_runs(id) ON DELETE CASCADE,
			FOREIGN KEY (final_review_run_id) REFERENCES stockv2_news_context_runs(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_backfill_reviewed_thread
			ON stockv2_news_context_backfill_reviewed_versions(backfill_id, final_review_run_id, thread_id, created_at);
		CREATE TABLE IF NOT EXISTS stockv2_news_context_backfill_news (
			backfill_id TEXT NOT NULL,
			news_event_id TEXT NOT NULL,
			event_at DATETIME NOT NULL,
			event_unix_nano INTEGER NOT NULL,
			defer_retry_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (backfill_id, news_event_id),
			FOREIGN KEY (backfill_id) REFERENCES stockv2_news_context_backfills(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_context_backfill_news_unix_window
			ON stockv2_news_context_backfill_news(backfill_id, event_unix_nano);
	`)
	if err != nil {
		return wrapError(err, "ensure news context backfill schema")
	}
	if err := s.removeNewsContextBackfillRunUniqueConstraint(ctx); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "stockv2_news_context_backfills", "range_start_at", "DATETIME"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "stockv2_news_context_backfills", "completed_chunk_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "stockv2_news_context_backfills", "final_review_run_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "stockv2_news_context_backfills", "owner_revision", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := s.migrateNewsContextBackfillFinalReviewRunIDs(ctx); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "stockv2_news_context_backfill_news", "event_unix_nano", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "stockv2_news_context_backfill_news", "defer_retry_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT backfill_id,news_event_id,event_at
		FROM stockv2_news_context_backfill_news WHERE event_unix_nano=0`)
	if err != nil {
		return wrapError(err, "list news context backfill manifest time migration")
	}
	type manifestTime struct {
		backfillID, eventID string
		eventAt             time.Time
	}
	times := make([]manifestTime, 0)
	for rows.Next() {
		var value manifestTime
		if err := rows.Scan(&value.backfillID, &value.eventID, &value.eventAt); err != nil {
			rows.Close()
			return wrapError(err, "scan news context backfill manifest time migration")
		}
		times = append(times, value)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, value := range times {
		if _, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_backfill_news
			SET event_unix_nano=? WHERE backfill_id=? AND news_event_id=?`,
			value.eventAt.UnixNano(), value.backfillID, value.eventID); err != nil {
			return wrapError(err, "migrate news context backfill manifest time")
		}
	}
	if _, err := s.marketDB.db.ExecContext(ctx, `
		ALTER TABLE stockv2_news_thread_versions ADD COLUMN IF NOT EXISTS effective_at TIMESTAMP;
		UPDATE stockv2_news_thread_versions SET effective_at=created_at WHERE effective_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_stockv2_news_thread_versions_effective
			ON stockv2_news_thread_versions(thread_id, effective_at);
	`); err != nil {
		return wrapError(err, "ensure news context version effective time")
	}
	return nil
}

func (s *Store) migrateNewsContextBackfillFinalReviewRunIDs(ctx context.Context) error {
	// ponytail: this is an idempotent data migration, not a runtime compatibility
	// branch. It can be removed after every deployed database has crossed this
	// schema version; later migrations should use an explicit migration ledger.
	if _, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_backfills
		SET final_review_run_id=current_run_id
		WHERE TRIM(COALESCE(final_review_run_id,''))=''
		AND phase='final_review' AND TRIM(COALESCE(current_run_id,''))<>''`); err != nil {
		return wrapError(err, "migrate active news context backfill final review")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_backfills AS b
		SET final_review_run_id=(
			SELECT r.id FROM stockv2_news_context_runs r
			WHERE r.window_type=? AND r.trigger_type=? AND r.window_start=b.cutoff_at
			AND r.created_at>=COALESCE(b.started_at,b.updated_at)
			ORDER BY r.created_at DESC,r.id DESC LIMIT 1
		)
		WHERE TRIM(COALESCE(b.final_review_run_id,''))=''
		AND b.phase IN ('indexing','final_review','finalizing','completed')
		AND EXISTS (
			SELECT 1 FROM stockv2_news_context_runs r
			WHERE r.window_type=? AND r.trigger_type=? AND r.window_start=b.cutoff_at
			AND r.created_at>=COALESCE(b.started_at,b.updated_at)
		)`, NewsContextWindowDaily, NewsContextTriggerManual,
		NewsContextWindowDaily, NewsContextTriggerManual); err != nil {
		return wrapError(err, "infer news context backfill final review")
	}
	return nil
}

func (s *Store) removeNewsContextBackfillRunUniqueConstraint(ctx context.Context) error {
	var schema string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(sql,'') FROM sqlite_master
		WHERE type='table' AND name='stockv2_news_context_backfill_runs'`).Scan(&schema); err != nil {
		return wrapError(err, "inspect news context backfill run links")
	}
	if !strings.Contains(strings.ToUpper(schema), "RUN_ID TEXT NOT NULL UNIQUE") {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE stockv2_news_context_backfill_runs_new (
			backfill_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (backfill_id,run_id),
			FOREIGN KEY (backfill_id) REFERENCES stockv2_news_context_backfills(id) ON DELETE CASCADE,
			FOREIGN KEY (run_id) REFERENCES stockv2_news_context_runs(id) ON DELETE CASCADE
		)`); err != nil {
			return wrapError(err, "create migrated news context backfill run links")
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO stockv2_news_context_backfill_runs_new
			(backfill_id,run_id,created_at) SELECT backfill_id,run_id,created_at
			FROM stockv2_news_context_backfill_runs`); err != nil {
			return wrapError(err, "copy news context backfill run links")
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE stockv2_news_context_backfill_runs`); err != nil {
			return wrapError(err, "drop old news context backfill run links")
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE stockv2_news_context_backfill_runs_new
			RENAME TO stockv2_news_context_backfill_runs`); err != nil {
			return wrapError(err, "rename news context backfill run links")
		}
		return nil
	})
}

type newsContextBackfillSourceEvent struct {
	ID      string
	EventAt time.Time
}

func (s *Store) listNewsContextBackfillSourceEvents(ctx context.Context, cutoff time.Time) ([]newsContextBackfillSourceEvent, error) {
	rows, err := s.marketDB.db.QueryContext(ctx, `SELECT id,event_at FROM stockv2_news_events
		WHERE event_at<? AND (
			COALESCE(context_status,'pending') IN ('pending','claimed') OR
			(COALESCE(context_status,'pending')='deferred' AND TRIM(COALESCE(context_run_id,''))='')
		) ORDER BY event_at ASC,id ASC`, cutoff)
	if err != nil {
		return nil, wrapError(err, "list news context backfill source events")
	}
	return scanRows(rows, func(row rowScanner) (newsContextBackfillSourceEvent, error) {
		var item newsContextBackfillSourceEvent
		err := row.Scan(&item.ID, &item.EventAt)
		return item, err
	}, "scan news context backfill source event", "iterate news context backfill source events")
}

func (s *Store) CreateNewsContextBackfillWithManifest(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	events, err := s.listNewsContextBackfillSourceEvents(ctx, item.CutoffAt)
	if err != nil {
		return NewsContextBackfill{}, err
	}
	now := time.Now()
	if strings.TrimSpace(item.ID) == "" {
		item.ID = generateID()
	}
	if item.Status == "" {
		item.Status = NewsContextBackfillStatusRunning
	}
	if item.Phase == "" {
		item.Phase = "hourly"
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	if item.OwnerRevision <= 0 {
		item.OwnerRevision = 1
	}
	if item.RangeStartAt.IsZero() && len(events) > 0 {
		at := events[0].EventAt.In(time.Local)
		item.RangeStartAt = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	}
	if item.RangeStartAt.IsZero() {
		item.RangeStartAt = item.CutoffAt
	}
	item.TotalNewsCount = len(events)
	item.RemainingNewsCount = len(events)
	item.UpdatedAt = now
	err = s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfills
			(id,status,phase,owner_revision,range_start_at,cutoff_at,total_news_count,processed_news_count,
			 remaining_news_count,missing_news_count,completed_chunk_count,current_window_start,current_window_end,
			 current_run_id,final_review_run_id,error_message,requested_by,started_at,updated_at,completed_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Status, item.Phase, item.OwnerRevision,
			nullableTime(item.RangeStartAt), item.CutoffAt, item.TotalNewsCount, item.ProcessedNewsCount,
			item.RemainingNewsCount, item.MissingNewsCount, item.CompletedChunkCount, nullableTime(item.CurrentWindowStart),
			nullableTime(item.CurrentWindowEnd), nullableString(item.CurrentRunID),
			nullableString(item.FinalReviewRunID), nullableString(item.ErrorMessage), nullableString(item.RequestedBy), nullableTime(item.StartedAt),
			item.UpdatedAt, nullableTime(item.CompletedAt)); err != nil {
			return wrapError(err, "create news context backfill")
		}
		for _, event := range events {
			if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfill_news
				(backfill_id,news_event_id,event_at,event_unix_nano,created_at) VALUES (?,?,?,?,?)`,
				item.ID, event.ID, event.EventAt, event.EventAt.UnixNano(), now); err != nil {
				return wrapError(err, "freeze news context backfill manifest")
			}
		}
		return nil
	})
	if err != nil {
		return NewsContextBackfill{}, err
	}
	return s.GetNewsContextBackfill(ctx, item.ID)
}

func (s *Store) AppendNewsContextBackfillManifest(ctx context.Context, backfillID string, cutoff time.Time) (int, error) {
	events, err := s.listNewsContextBackfillSourceEvents(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	inserted := 0
	err = s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, event := range events {
			result, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfill_news
				(backfill_id,news_event_id,event_at,event_unix_nano,created_at) VALUES (?,?,?,?,?)
				ON CONFLICT(backfill_id,news_event_id) DO NOTHING`, strings.TrimSpace(backfillID),
				event.ID, event.EventAt, event.EventAt.UnixNano(), time.Now())
			if err != nil {
				return wrapError(err, "append news context backfill manifest")
			}
			if rows, _ := result.RowsAffected(); rows > 0 {
				inserted++
			}
		}
		return nil
	})
	return inserted, err
}

const newsContextBackfillSelectSQL = `
	SELECT id, status, phase, owner_revision, range_start_at, cutoff_at, total_news_count, processed_news_count,
	       remaining_news_count, missing_news_count, completed_chunk_count, current_window_start,
	       current_window_end, COALESCE(current_run_id,''), COALESCE(final_review_run_id,''), COALESCE(error_message,''),
	       COALESCE(requested_by,''), started_at, updated_at, completed_at
	FROM stockv2_news_context_backfills`

func scanNewsContextBackfill(row rowScanner) (NewsContextBackfill, error) {
	var item NewsContextBackfill
	var rangeStart, windowStart, windowEnd, startedAt, completedAt sql.NullTime
	err := row.Scan(&item.ID, &item.Status, &item.Phase, &item.OwnerRevision, &rangeStart, &item.CutoffAt,
		&item.TotalNewsCount, &item.ProcessedNewsCount, &item.RemainingNewsCount,
		&item.MissingNewsCount, &item.CompletedChunkCount, &windowStart, &windowEnd, &item.CurrentRunID,
		&item.FinalReviewRunID, &item.ErrorMessage, &item.RequestedBy, &startedAt, &item.UpdatedAt, &completedAt)
	assignNullTime(&item.RangeStartAt, rangeStart)
	assignNullTime(&item.CurrentWindowStart, windowStart)
	assignNullTime(&item.CurrentWindowEnd, windowEnd)
	assignNullTime(&item.StartedAt, startedAt)
	assignNullTime(&item.CompletedAt, completedAt)
	return item, err
}

func (s *Store) CreateNewsContextBackfill(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	now := time.Now()
	if strings.TrimSpace(item.ID) == "" {
		item.ID = generateID()
	}
	if item.Status == "" {
		item.Status = NewsContextBackfillStatusRunning
	}
	if item.Phase == "" {
		item.Phase = "queued"
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	if item.OwnerRevision <= 0 {
		item.OwnerRevision = 1
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfills
		(id,status,phase,owner_revision,range_start_at,cutoff_at,total_news_count,processed_news_count,
		 remaining_news_count,missing_news_count,completed_chunk_count,current_window_start,current_window_end,
		 current_run_id,final_review_run_id,error_message,requested_by,started_at,updated_at,completed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Status, item.Phase, item.OwnerRevision,
		nullableTime(item.RangeStartAt), item.CutoffAt, item.TotalNewsCount, item.ProcessedNewsCount,
		item.RemainingNewsCount, item.MissingNewsCount, item.CompletedChunkCount, nullableTime(item.CurrentWindowStart),
		nullableTime(item.CurrentWindowEnd), nullableString(item.CurrentRunID), nullableString(item.FinalReviewRunID),
		nullableString(item.ErrorMessage), nullableString(item.RequestedBy),
		nullableTime(item.StartedAt), item.UpdatedAt, nullableTime(item.CompletedAt))
	if err != nil {
		return NewsContextBackfill{}, wrapError(err, "create news context backfill")
	}
	return s.GetNewsContextBackfill(ctx, item.ID)
}

func (s *Store) GetNewsContextBackfill(ctx context.Context, id string) (NewsContextBackfill, error) {
	item, err := scanNewsContextBackfill(s.db.QueryRowContext(ctx,
		newsContextBackfillSelectSQL+` WHERE id=?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsContextBackfillNotFound
	}
	return item, wrapError(err, "get news context backfill")
}

func (s *Store) GetLatestNewsContextBackfill(ctx context.Context) (NewsContextBackfill, error) {
	item, err := scanNewsContextBackfill(s.db.QueryRowContext(ctx,
		newsContextBackfillSelectSQL+` ORDER BY started_at DESC, updated_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNewsContextBackfillNotFound
	}
	return item, wrapError(err, "get latest news context backfill")
}

func (s *Store) GetBlockingNewsContextBackfill(ctx context.Context) (NewsContextBackfill, bool, error) {
	item, err := scanNewsContextBackfill(s.db.QueryRowContext(ctx, newsContextBackfillSelectSQL+`
		WHERE status<>? OR missing_news_count>0
		ORDER BY started_at DESC, updated_at DESC LIMIT 1`, NewsContextBackfillStatusCompleted))
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	return item, err == nil, wrapError(err, "get blocking news context backfill")
}

func (s *Store) GetLatestCompletedNewsContextBackfillCutoff(ctx context.Context) (time.Time, bool, error) {
	var cutoff time.Time
	err := s.db.QueryRowContext(ctx, `SELECT cutoff_at
		FROM stockv2_news_context_backfills
		WHERE status=? AND missing_news_count=0
		ORDER BY cutoff_at DESC, completed_at DESC, id DESC LIMIT 1`,
		NewsContextBackfillStatusCompleted).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return cutoff, err == nil, wrapError(err, "get latest completed news context backfill cutoff")
}

func (s *Store) UpdateNewsContextBackfill(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	return s.updateNewsContextBackfill(ctx, item, true)
}

func (s *Store) UpdateNewsContextBackfillWorker(ctx context.Context, item NewsContextBackfill) (NewsContextBackfill, error) {
	return s.updateNewsContextBackfill(ctx, item, false)
}

func (s *Store) updateNewsContextBackfill(ctx context.Context, item NewsContextBackfill, ownerUpdate bool) (NewsContextBackfill, error) {
	item.UpdatedAt = time.Now()
	// ponytail: one owner revision is enough to invalidate every worker snapshot
	// taken before pause/resume/retry; workers do not need their own lock table.
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_backfills SET
		status=?, phase=?, owner_revision=owner_revision+?,
		range_start_at=?, cutoff_at=?, total_news_count=?, processed_news_count=?,
		remaining_news_count=?, missing_news_count=?, completed_chunk_count=?, current_window_start=?,
		current_window_end=?, current_run_id=?, final_review_run_id=?,
		error_message=?, requested_by=?, started_at=?, updated_at=?, completed_at=?
		WHERE id=? AND owner_revision=?`,
		item.Status, item.Phase, boolToInt(ownerUpdate),
		nullableTime(item.RangeStartAt), item.CutoffAt, item.TotalNewsCount, item.ProcessedNewsCount,
		item.RemainingNewsCount, item.MissingNewsCount, item.CompletedChunkCount, nullableTime(item.CurrentWindowStart),
		nullableTime(item.CurrentWindowEnd), nullableString(item.CurrentRunID), nullableString(item.FinalReviewRunID),
		nullableString(item.ErrorMessage), nullableString(item.RequestedBy), nullableTime(item.StartedAt),
		item.UpdatedAt, nullableTime(item.CompletedAt), item.ID, item.OwnerRevision)
	if err != nil {
		return NewsContextBackfill{}, wrapError(err, "update news context backfill")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		current, getErr := s.GetNewsContextBackfill(ctx, item.ID)
		if getErr != nil {
			return NewsContextBackfill{}, getErr
		}
		if ownerUpdate {
			return NewsContextBackfill{}, ErrNewsContextBackfillState
		}
		return current, nil
	}
	return s.GetNewsContextBackfill(ctx, item.ID)
}

func (s *Store) LinkNewsContextBackfillRun(ctx context.Context, backfillID, runID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfill_runs
		(backfill_id,run_id,created_at) VALUES (?,?,?) ON CONFLICT(backfill_id,run_id) DO NOTHING`,
		strings.TrimSpace(backfillID), strings.TrimSpace(runID), time.Now())
	return wrapError(err, "link news context backfill run")
}

func (s *Store) ReserveNewsContextBackfillRun(ctx context.Context, backfillID string, run NewsContextRun) (NewsContextBackfill, error) {
	backfillID = strings.TrimSpace(backfillID)
	if backfillID == "" || strings.TrimSpace(run.ID) == "" || run.WindowStart.IsZero() || !run.WindowEnd.After(run.WindowStart) {
		return NewsContextBackfill{}, ErrInvalidNewsContextInput
	}
	err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now()
		result, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_backfills SET
			phase=?, current_window_start=?, current_window_end=?, current_run_id=?, updated_at=?
			WHERE id=? AND status=? AND TRIM(COALESCE(current_run_id,''))=''`,
			newsContextBackfillWindowPhase(run.WindowType), run.WindowStart, run.WindowEnd,
			run.ID, now, backfillID, NewsContextBackfillStatusRunning)
		if err != nil {
			return wrapError(err, "reserve news context backfill run")
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return ErrNewsContextBackfillState
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfill_runs
			(backfill_id,run_id,created_at) VALUES (?,?,?)
			ON CONFLICT(backfill_id,run_id) DO NOTHING`, backfillID, run.ID, now); err != nil {
			return wrapError(err, "link reserved news context backfill run")
		}
		return nil
	})
	if err != nil {
		return NewsContextBackfill{}, err
	}
	return s.GetNewsContextBackfill(ctx, backfillID)
}

func (s *Store) ReserveNewsContextBackfillFinalReviewRun(ctx context.Context, item NewsContextBackfill, run NewsContextRun) (NewsContextBackfill, error) {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(run.ID) == "" ||
		run.WindowStart.IsZero() || !run.WindowEnd.After(run.WindowStart) {
		return NewsContextBackfill{}, ErrInvalidNewsContextInput
	}
	result, err := s.db.ExecContext(ctx, `UPDATE stockv2_news_context_backfills SET
		current_window_start=?,current_window_end=?,current_run_id=?,final_review_run_id=?,updated_at=?
		WHERE id=? AND status=? AND phase='final_review'
		AND TRIM(COALESCE(current_run_id,''))='' AND owner_revision=?`,
		run.WindowStart, run.WindowEnd, run.ID, run.ID, time.Now(), item.ID,
		NewsContextBackfillStatusRunning, item.OwnerRevision)
	if err != nil {
		return NewsContextBackfill{}, wrapError(err, "reserve news context backfill final review")
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return NewsContextBackfill{}, ErrNewsContextBackfillState
	}
	return s.GetNewsContextBackfill(ctx, item.ID)
}

func (s *Store) NewsContextBackfillForFinalReviewRun(ctx context.Context, runID string) (NewsContextBackfill, bool, error) {
	item, err := scanNewsContextBackfill(s.db.QueryRowContext(ctx, newsContextBackfillSelectSQL+`
		WHERE final_review_run_id=? ORDER BY started_at DESC,updated_at DESC LIMIT 1`, strings.TrimSpace(runID)))
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	return item, err == nil, wrapError(err, "get news context backfill final review owner")
}

func (s *Store) BeginNewsContextBackfillFragment(ctx context.Context, backfillID, runID string) (NewsContextRun, error) {
	backfillID = strings.TrimSpace(backfillID)
	runID = strings.TrimSpace(runID)
	if backfillID == "" || runID == "" {
		return NewsContextRun{}, ErrInvalidNewsContextInput
	}
	err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var status, currentRunID string
		if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(current_run_id,'')
			FROM stockv2_news_context_backfills WHERE id=?`, backfillID).Scan(&status, &currentRunID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNewsContextBackfillNotFound
			}
			return wrapError(err, "read news context backfill fragment owner")
		}
		if status != NewsContextBackfillStatusRunning || currentRunID != runID {
			return ErrNewsContextBackfillState
		}
		var phase string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(phase,'') FROM stockv2_news_context_runs
			WHERE id=? AND status=?`, runID, NewsContextRunStatusPending).Scan(&phase); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNewsContextBackfillState
			}
			return wrapError(err, "read pending news context backfill fragment")
		}
		if phase == "converging" {
			// DEPRECATED: rebuild an interrupted pre-materialization daily run once;
			// the model convergence path was removed in 2026-07.
			phase = "collecting"
		} else if phase != "collecting" && phase != newsContextRunPhaseCheckpoint && phase != newsContextRunPhaseMaterialize {
			phase = newsContextRunPhaseAggregating
		}
		result, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_runs SET
			status=?,phase=?,current_agent_run_id=NULL,error_message=NULL,finished_at=NULL,updated_at=?
			WHERE id=? AND status=?`, NewsContextRunStatusRunning, phase, time.Now(), runID,
			NewsContextRunStatusPending)
		if err != nil {
			return wrapError(err, "begin news context backfill fragment")
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return ErrNewsContextBackfillState
		}
		return nil
	})
	if err != nil {
		return NewsContextRun{}, err
	}
	return s.GetNewsContextRun(ctx, runID)
}

func (s *Store) NewsContextBackfillForRun(ctx context.Context, runID string) (NewsContextBackfill, bool, error) {
	item, err := scanNewsContextBackfill(s.db.QueryRowContext(ctx, newsContextBackfillSelectSQL+`
		WHERE id=(SELECT b.backfill_id FROM stockv2_news_context_backfill_runs b
			JOIN stockv2_news_context_backfills p ON p.id=b.backfill_id WHERE b.run_id=?
			ORDER BY CASE WHEN p.status IN (?,?) THEN 0 ELSE 1 END,
			p.started_at DESC,p.updated_at DESC LIMIT 1)`, strings.TrimSpace(runID),
		NewsContextBackfillStatusRunning, NewsContextBackfillStatusPaused))
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	return item, err == nil, wrapError(err, "get news context backfill for run")
}

func (s *Store) ListNewsContextBackfillsForRun(ctx context.Context, runID string) ([]NewsContextBackfill, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, ErrInvalidNewsContextInput
	}
	rows, err := s.db.QueryContext(ctx, newsContextBackfillSelectSQL+`
		WHERE id IN (SELECT backfill_id FROM stockv2_news_context_backfill_runs WHERE run_id=?)
		ORDER BY completed_at DESC, started_at DESC, updated_at DESC, id DESC`, runID)
	if err != nil {
		return nil, wrapError(err, "list news context backfills for run")
	}
	return scanRows(rows, scanNewsContextBackfill,
		"scan news context backfill for run", "iterate news context backfills for run")
}

func (s *Store) ListNewsContextBackfillRuns(ctx context.Context, backfillID, windowType string) ([]NewsContextRun, error) {
	rows, err := s.db.QueryContext(ctx, newsContextRunSelectSQL+`
		WHERE id IN (SELECT run_id FROM stockv2_news_context_backfill_runs WHERE backfill_id=?)
		AND (?='' OR window_type=?) ORDER BY window_start ASC,updated_at DESC`,
		strings.TrimSpace(backfillID), strings.TrimSpace(windowType), strings.TrimSpace(windowType))
	if err != nil {
		return nil, wrapError(err, "list news context backfill runs")
	}
	return scanRows(rows, scanNewsContextRun, "scan news context backfill run", "iterate news context backfill runs")
}

type newsContextBackfillWindowProgress struct {
	CompletedWindowCount     int
	ProcessedItemCount       int
	TotalItemCount           int
	PendingItemCount         int
	CompletedDurationSeconds int64
	AgentAttemptCount        int
	AgentFailedCount         int
	ModelDurationSeconds     int64
}

func (s *Store) NewsContextBackfillWindowProgress(ctx context.Context, backfillID string) (map[string]newsContextBackfillWindowProgress, error) {
	rows, err := s.db.QueryContext(ctx, `WITH runs AS (
		SELECT r.*,b.created_at AS backfill_linked_at FROM stockv2_news_context_runs r
		JOIN stockv2_news_context_backfill_runs b ON b.run_id=r.id
		WHERE b.backfill_id=?
	), window_progress AS (
		SELECT r.window_type,
		COALESCE(SUM(CASE WHEN r.status=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(r.processed_count),0),
		COALESCE(SUM(r.input_count),0),COALESCE(SUM(r.pending_count),0),
		COALESCE(SUM(CASE WHEN r.status=? AND r.started_at IS NOT NULL
			THEN MAX(0,CAST(strftime('%s',COALESCE(r.finished_at,r.updated_at)) AS INTEGER)-
				CAST(strftime('%s',r.started_at) AS INTEGER)) ELSE 0 END),0)
		FROM runs r GROUP BY r.window_type
	), agent_progress AS (
		SELECT r.window_type,COUNT(a.id) AS attempt_count,
		COALESCE(SUM(CASE WHEN a.status=? THEN 1 ELSE 0 END),0) AS failed_count,
		COALESCE(SUM(MAX(0,CAST(strftime('%s',COALESCE(a.finished_at,CURRENT_TIMESTAMP)) AS INTEGER)-
			CAST(strftime('%s',COALESCE(a.started_at,a.created_at)) AS INTEGER))),0) AS duration_seconds
		FROM runs r JOIN stockv2_agent_runs a
		ON a.trigger_object_type='news_context_run' AND a.trigger_object_id=r.id
		AND a.task_type=? AND a.created_at>=r.backfill_linked_at GROUP BY r.window_type
	)
	SELECT w.*,COALESCE(a.attempt_count,0),COALESCE(a.failed_count,0),COALESCE(a.duration_seconds,0)
	FROM window_progress w LEFT JOIN agent_progress a ON a.window_type=w.window_type`,
		strings.TrimSpace(backfillID),
		NewsContextRunStatusCompleted, NewsContextRunStatusCompleted,
		AgentRunStatusFailed, AgentTaskTypeNewsEventReview)
	if err != nil {
		return nil, wrapError(err, "get news context backfill window progress")
	}
	defer rows.Close()
	progress := make(map[string]newsContextBackfillWindowProgress, 3)
	for rows.Next() {
		var windowType string
		var item newsContextBackfillWindowProgress
		if err := rows.Scan(&windowType, &item.CompletedWindowCount, &item.ProcessedItemCount,
			&item.TotalItemCount, &item.PendingItemCount, &item.CompletedDurationSeconds,
			&item.AgentAttemptCount, &item.AgentFailedCount, &item.ModelDurationSeconds); err != nil {
			return nil, wrapError(err, "scan news context backfill window progress")
		}
		progress[windowType] = item
	}
	return progress, wrapError(rows.Err(), "iterate news context backfill window progress")
}

func (s *Store) ListNewsContextBackfillOutputVersionIDs(ctx context.Context, backfillID, windowType string, start, end time.Time) ([]string, error) {
	runRows, err := s.db.QueryContext(ctx, `SELECT r.id
		FROM stockv2_news_context_runs r
		JOIN stockv2_news_context_backfill_runs b ON b.run_id=r.id
		WHERE b.backfill_id=? AND r.window_type=? AND r.window_start>=? AND r.window_end<=?
		AND r.status=? ORDER BY r.window_start,r.id`, strings.TrimSpace(backfillID),
		strings.TrimSpace(windowType), start, end, NewsContextRunStatusCompleted)
	if err != nil {
		return nil, wrapError(err, "list news context backfill output runs")
	}
	runIDs := make([]string, 0)
	for runRows.Next() {
		var id string
		if err := runRows.Scan(&id); err != nil {
			runRows.Close()
			return nil, wrapError(err, "scan news context backfill output run")
		}
		runIDs = append(runIDs, id)
	}
	if err := runRows.Close(); err != nil || len(runIDs) == 0 {
		return nil, err
	}
	set := make(map[string]struct{})
	args := make([]any, 0, len(runIDs)+2)
	for _, id := range runIDs {
		args = append(args, id)
	}
	args = append(args, NewsContextRunItemCompleted)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT version_id
		FROM stockv2_news_context_run_items WHERE run_id IN (`+sqlPlaceholders(len(runIDs))+`)
		AND status=? AND TRIM(COALESCE(version_id,''))<>''`, args...)
	if err != nil {
		return nil, wrapError(err, "list inherited news context output versions")
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		set[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	marketArgs := make([]any, len(runIDs))
	for i, id := range runIDs {
		marketArgs[i] = id
	}
	versionRows, err := s.marketDB.db.QueryContext(ctx, `SELECT id
		FROM stockv2_news_thread_versions WHERE run_id IN (`+sqlPlaceholders(len(runIDs))+`)`, marketArgs...)
	if err != nil {
		return nil, wrapError(err, "list direct news context output versions")
	}
	for versionRows.Next() {
		var id string
		if err := versionRows.Scan(&id); err != nil {
			versionRows.Close()
			return nil, err
		}
		set[id] = struct{}{}
	}
	if err := versionRows.Close(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

type newsContextBackfillDailyOutput struct {
	DailyRunID string
	VersionID  string
}

type newsContextBackfillReviewedVersion struct {
	DailyRunID       string
	ThreadID         string
	VersionID        string
	FinalReviewRunID string
	CreatedAt        time.Time
}

func (s *Store) ListNewsContextBackfillFinalReviewedVersionIDs(ctx context.Context, threadID string) (map[string]struct{}, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, ErrInvalidNewsContextInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT v.version_id
		FROM stockv2_news_context_backfill_reviewed_versions v
		JOIN stockv2_news_context_backfills b
		  ON b.id=v.backfill_id AND b.final_review_run_id=v.final_review_run_id
		JOIN stockv2_news_context_backfill_runs br
		  ON br.backfill_id=v.backfill_id AND br.run_id=v.daily_run_id
		JOIN stockv2_news_context_runs daily ON daily.id=v.daily_run_id
		JOIN stockv2_news_context_runs final_review ON final_review.id=v.final_review_run_id
		WHERE v.thread_id=? AND daily.window_type=? AND daily.trigger_type=?
		AND daily.status=? AND final_review.status=? AND final_review.review_status=?`,
		threadID, NewsContextWindowDaily, NewsContextTriggerBackfill,
		NewsContextRunStatusCompleted, NewsContextRunStatusCompleted, NewsContextReviewCompleted)
	if err != nil {
		return nil, wrapError(err, "list final reviewed historical news context versions")
	}
	defer rows.Close()
	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapError(err, "scan final reviewed historical news context version")
		}
		ids[id] = struct{}{}
	}
	return ids, wrapError(rows.Err(), "iterate final reviewed historical news context versions")
}

func (s *Store) NewsContextBackfillReviewCoverage(ctx context.Context, backfillID, finalReviewRunID string) (int, int, error) {
	backfillID = strings.TrimSpace(backfillID)
	finalReviewRunID = strings.TrimSpace(finalReviewRunID)
	if backfillID == "" {
		return 0, 0, ErrInvalidNewsContextInput
	}
	var total, linked int
	err := s.db.QueryRowContext(ctx, `WITH outputs AS (
		SELECT DISTINCT r.id AS daily_run_id,i.version_id
		FROM stockv2_news_context_runs r
		JOIN stockv2_news_context_backfill_runs b ON b.run_id=r.id
		JOIN stockv2_news_context_run_items i ON i.run_id=r.id
		WHERE b.backfill_id=? AND r.window_type=? AND r.trigger_type=? AND r.status=?
		AND i.object_type=? AND i.status=? AND TRIM(COALESCE(i.version_id,''))<>''
	)
	SELECT COUNT(*),COALESCE(SUM(CASE WHEN EXISTS (
		SELECT 1 FROM stockv2_news_context_backfill_reviewed_versions v
		WHERE v.backfill_id=? AND v.daily_run_id=outputs.daily_run_id
		AND v.version_id=outputs.version_id AND v.final_review_run_id=?
	) THEN 1 ELSE 0 END),0) FROM outputs`, backfillID, NewsContextWindowDaily,
		NewsContextTriggerBackfill, NewsContextRunStatusCompleted, NewsContextRunItemThread,
		NewsContextRunItemCompleted, backfillID, finalReviewRunID).Scan(&total, &linked)
	if err != nil {
		return 0, 0, wrapError(err, "count news context backfill review coverage")
	}
	return total, linked, nil
}

func (s *Store) ListPendingNewsContextBackfillDailyOutputs(ctx context.Context, backfillID, finalReviewRunID string, limit int) ([]newsContextBackfillDailyOutput, error) {
	limit = normalizedPageLimit(limit, 100)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.id,i.version_id
		FROM stockv2_news_context_runs r
		JOIN stockv2_news_context_backfill_runs b ON b.run_id=r.id
		JOIN stockv2_news_context_run_items i ON i.run_id=r.id
		WHERE b.backfill_id=? AND r.window_type=? AND r.trigger_type=? AND r.status=?
		AND i.object_type=? AND i.status=? AND TRIM(COALESCE(i.version_id,''))<>''
		AND NOT EXISTS (
			SELECT 1 FROM stockv2_news_context_backfill_reviewed_versions v
			WHERE v.backfill_id=b.backfill_id AND v.daily_run_id=r.id
			AND v.version_id=i.version_id AND v.final_review_run_id=?
		)
		ORDER BY r.window_start ASC,r.id ASC,i.version_id ASC LIMIT ?`,
		strings.TrimSpace(backfillID), NewsContextWindowDaily, NewsContextTriggerBackfill,
		NewsContextRunStatusCompleted, NewsContextRunItemThread, NewsContextRunItemCompleted,
		strings.TrimSpace(finalReviewRunID), limit)
	if err != nil {
		return nil, wrapError(err, "list pending reviewed historical daily outputs")
	}
	return scanRows(rows, func(row rowScanner) (newsContextBackfillDailyOutput, error) {
		var item newsContextBackfillDailyOutput
		err := row.Scan(&item.DailyRunID, &item.VersionID)
		return item, err
	}, "scan pending reviewed historical daily output", "iterate pending reviewed historical daily outputs")
}

func (s *Store) UpsertNewsContextBackfillReviewedVersions(ctx context.Context, backfillID, finalReviewRunID string, items []newsContextBackfillReviewedVersion) error {
	backfillID = strings.TrimSpace(backfillID)
	finalReviewRunID = strings.TrimSpace(finalReviewRunID)
	if backfillID == "" || finalReviewRunID == "" {
		return ErrInvalidNewsContextInput
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now()
		for _, item := range items {
			if strings.TrimSpace(item.DailyRunID) == "" || strings.TrimSpace(item.ThreadID) == "" || strings.TrimSpace(item.VersionID) == "" {
				return ErrInvalidNewsContextInput
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_backfill_reviewed_versions
				(backfill_id,daily_run_id,thread_id,version_id,final_review_run_id,created_at)
				VALUES (?,?,?,?,?,?)
				ON CONFLICT(backfill_id,daily_run_id,version_id) DO UPDATE SET
				thread_id=excluded.thread_id,final_review_run_id=excluded.final_review_run_id,
				created_at=excluded.created_at`, backfillID, strings.TrimSpace(item.DailyRunID),
				strings.TrimSpace(item.ThreadID), strings.TrimSpace(item.VersionID), finalReviewRunID, now); err != nil {
				return wrapError(err, "save reviewed historical daily output")
			}
		}
		return nil
	})
}

func (s *Store) FindNewsContextBackfillReviewedVersionCoveringRun(
	ctx context.Context,
	backfillID, finalReviewRunID, threadID string,
	sourceStart, sourceEnd time.Time,
) (newsContextBackfillReviewedVersion, bool, error) {
	var item newsContextBackfillReviewedVersion
	if strings.TrimSpace(backfillID) == "" || strings.TrimSpace(finalReviewRunID) == "" ||
		strings.TrimSpace(threadID) == "" || sourceStart.IsZero() || !sourceEnd.After(sourceStart) {
		return item, false, ErrInvalidNewsContextInput
	}
	err := s.db.QueryRowContext(ctx, `SELECT v.daily_run_id,v.thread_id,v.version_id,v.final_review_run_id,v.created_at
		FROM stockv2_news_context_backfill_reviewed_versions v
		JOIN stockv2_news_context_runs r ON r.id=v.daily_run_id
		WHERE v.backfill_id=? AND v.final_review_run_id=? AND v.thread_id=?
		AND r.window_start<=? AND r.window_end>=?
		AND r.window_type=? AND r.trigger_type=? AND r.status=?
		ORDER BY r.window_end DESC,r.id DESC,v.version_id DESC LIMIT 1`, strings.TrimSpace(backfillID),
		strings.TrimSpace(finalReviewRunID), strings.TrimSpace(threadID), sourceStart, sourceEnd,
		NewsContextWindowDaily, NewsContextTriggerBackfill, NewsContextRunStatusCompleted).
		Scan(&item.DailyRunID, &item.ThreadID, &item.VersionID, &item.FinalReviewRunID, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	return item, err == nil, wrapError(err, "find reviewed historical daily output covering source run")
}

func (s *Store) ReplaceNewsContextMaterializedThreadItems(ctx context.Context, runID string, versions []NewsThreadVersion) error {
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_news_context_run_items
			WHERE run_id=? AND object_type=?`, strings.TrimSpace(runID), NewsContextRunItemThread); err != nil {
			return wrapError(err, "reset historical news context thread manifest")
		}
		now := time.Now()
		for _, version := range versions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_run_items
				(id,run_id,object_type,object_id,status,disposition,thread_id,version_id,source_at,created_at,updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`, generateID(), strings.TrimSpace(runID),
				NewsContextRunItemThread, version.ID, NewsContextRunItemCompleted, "materialized",
				version.ThreadID, version.ID, version.EffectiveAt, now, now); err != nil {
				return wrapError(err, "seed materialized news context thread checkpoint")
			}
		}
		return nil
	})
}

func (s *Store) NewsContextBackfillRunProgress(ctx context.Context, backfillID string) (int, int, error) {
	var processed, missing int
	err := s.db.QueryRowContext(ctx, `WITH completion_counts AS (
			SELECT i.object_id,COUNT(*) AS completion_count
			FROM stockv2_news_context_backfill_runs r
			JOIN stockv2_news_context_run_items i ON i.run_id=r.run_id
			WHERE r.backfill_id=? AND i.status=? AND i.object_type=?
			GROUP BY i.object_id
		)
		SELECT
			COALESCE(SUM(CASE WHEN COALESCE(c.completion_count,0)=1 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN COALESCE(c.completion_count,0)>1 THEN 1 ELSE 0 END),0)
		FROM stockv2_news_context_backfill_news m
		LEFT JOIN completion_counts c ON c.object_id=m.news_event_id
		WHERE m.backfill_id=?`, strings.TrimSpace(backfillID), NewsContextRunItemCompleted,
		NewsContextRunItemNewsEvent, strings.TrimSpace(backfillID)).Scan(&processed, &missing)
	if err != nil {
		return 0, 0, wrapError(err, "get news context backfill run progress")
	}
	return processed, missing, nil
}

// RequeueFirstNewsContextBackfillDeferrals turns the first deferred decision for
// a frozen news item back into unfinished work. A second deferred decision is
// left completed and protected, which gives ambiguous news one bounded retry
// without making the historical backfill loop forever.
func (s *Store) RequeueFirstNewsContextBackfillDeferrals(ctx context.Context, backfillID string) (int, error) {
	backfillID = strings.TrimSpace(backfillID)
	if backfillID == "" {
		return 0, ErrInvalidNewsContextInput
	}
	if err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT m.news_event_id
			FROM stockv2_news_context_backfill_news m
			JOIN stockv2_news_context_backfill_runs r ON r.backfill_id=m.backfill_id
			JOIN stockv2_news_context_run_items i ON i.run_id=r.run_id AND i.object_id=m.news_event_id
			WHERE m.backfill_id=? AND m.defer_retry_count=0
			AND i.object_type=? AND i.status=? AND i.disposition=?
			ORDER BY m.event_unix_nano,m.news_event_id`, backfillID, NewsContextRunItemNewsEvent,
			NewsContextRunItemCompleted, NewsEventContextDeferred)
		if err != nil {
			return wrapError(err, "list first deferred historical news")
		}
		ids := make([]string, 0)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return wrapError(err, "scan first deferred historical news")
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		now := time.Now()
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_backfill_news
				SET defer_retry_count=1 WHERE backfill_id=? AND news_event_id=? AND defer_retry_count=0`,
				backfillID, id); err != nil {
				return wrapError(err, "mark deferred historical news retry")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_run_items
				SET status=?,updated_at=? WHERE object_type=? AND object_id=? AND status=? AND disposition=?
				AND run_id IN (SELECT run_id FROM stockv2_news_context_backfill_runs WHERE backfill_id=?)`,
				NewsContextRunItemDeferred, now, NewsContextRunItemNewsEvent, id,
				NewsContextRunItemCompleted, NewsEventContextDeferred, backfillID); err != nil {
				return wrapError(err, "requeue first deferred historical news")
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	var pending int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM stockv2_news_context_backfill_news m
		WHERE m.backfill_id=? AND m.defer_retry_count=1 AND NOT EXISTS (
			SELECT 1 FROM stockv2_news_context_backfill_runs r
			JOIN stockv2_news_context_run_items i ON i.run_id=r.run_id
			WHERE r.backfill_id=m.backfill_id AND i.object_type=?
			AND i.object_id=m.news_event_id AND i.status=?
		)`, backfillID, NewsContextRunItemNewsEvent, NewsContextRunItemCompleted).Scan(&pending)
	return pending, wrapError(err, "count requeued first deferred historical news")
}

func (s *Store) CountNewsContextBackfillCoveredWithoutEvidence(ctx context.Context, backfillID string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.news_event_id
		FROM stockv2_news_context_backfill_news m
		JOIN stockv2_news_context_backfill_runs r ON r.backfill_id=m.backfill_id
		JOIN stockv2_news_context_run_items i ON i.run_id=r.run_id
			AND i.object_type=? AND i.object_id=m.news_event_id AND i.status=?
		WHERE m.backfill_id=?
		AND LOWER(TRIM(COALESCE(i.disposition,''))) NOT IN (?,?,?)`,
		NewsContextRunItemNewsEvent, NewsContextRunItemCompleted, strings.TrimSpace(backfillID),
		NewsEventContextNoise, "duplicate", NewsEventContextDeferred)
	if err != nil {
		return 0, wrapError(err, "list covered historical news")
	}
	coveredIDs, err := scanRows(rows, func(row rowScanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	}, "scan covered historical news", "iterate covered historical news")
	if err != nil {
		return 0, err
	}
	missing := 0
	for start := 0; start < len(coveredIDs); start += newsContextSeedPageSize {
		end := min(start+newsContextSeedPageSize, len(coveredIDs))
		args := make([]any, end-start)
		for index, id := range coveredIDs[start:end] {
			args[index] = id
		}
		var found int
		if err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT news_event_id)
			FROM stockv2_news_thread_evidence WHERE news_event_id IN (`+sqlPlaceholders(len(args))+`)`, args...).Scan(&found); err != nil {
			return 0, wrapError(err, "count compact evidence for covered historical news")
		}
		missing += len(args) - found
	}
	return missing, nil
}

func (s *Store) CountNewsContextBackfillManifest(ctx context.Context, backfillID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_backfill_news
		WHERE backfill_id=?`, strings.TrimSpace(backfillID)).Scan(&count)
	return count, wrapError(err, "count news context backfill manifest")
}

func (s *Store) NewsContextBackfillManifestRangeStart(ctx context.Context, backfillID string, cutoff time.Time) (time.Time, error) {
	var earliest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(event_unix_nano) FROM stockv2_news_context_backfill_news
		WHERE backfill_id=?`, strings.TrimSpace(backfillID)).Scan(&earliest)
	if err != nil {
		return time.Time{}, wrapError(err, "get news context backfill manifest range")
	}
	if !earliest.Valid {
		return cutoff, nil
	}
	at := time.Unix(0, earliest.Int64).In(time.Local)
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.Local), nil
}

func (s *Store) CountCompletedNewsContextBackfillChunks(ctx context.Context, backfillID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_agent_runs a
		JOIN stockv2_news_context_runs r ON r.id=a.trigger_object_id
		JOIN stockv2_news_context_backfill_runs b ON b.run_id=r.id
		WHERE b.backfill_id=? AND r.trigger_type=? AND a.task_type=? AND a.status=?
		AND a.trigger_object_type='news_context_run' AND a.created_at>=b.created_at`,
		strings.TrimSpace(backfillID), NewsContextTriggerBackfill,
		AgentTaskTypeNewsEventReview, AgentRunStatusCompleted).Scan(&count)
	return count, wrapError(err, "count completed news context backfill chunks")
}

func (s *Store) OldestPendingNewsContextBackfillEventAt(ctx context.Context, backfillID string) (time.Time, bool, error) {
	var value time.Time
	err := s.db.QueryRowContext(ctx, `SELECT m.event_at
		FROM stockv2_news_context_backfill_news m WHERE m.backfill_id=? AND NOT EXISTS (
			SELECT 1 FROM stockv2_news_context_run_items i
			JOIN stockv2_news_context_backfill_runs r ON r.run_id=i.run_id
			WHERE r.backfill_id=m.backfill_id AND i.object_type=?
			AND i.object_id=m.news_event_id AND i.status=?
		) ORDER BY m.event_unix_nano ASC,m.news_event_id ASC LIMIT 1`, strings.TrimSpace(backfillID), NewsContextRunItemNewsEvent,
		NewsContextRunItemCompleted).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, wrapError(err, "find oldest pending news context backfill event")
	}
	return value, true, nil
}

func (s *Store) CountPendingNewsContextBackfillEventsInRange(ctx context.Context, backfillID string, start, end time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM stockv2_news_context_backfill_news m
		WHERE m.backfill_id=? AND m.event_unix_nano>=? AND m.event_unix_nano<? AND NOT EXISTS (
			SELECT 1 FROM stockv2_news_context_run_items i
			JOIN stockv2_news_context_backfill_runs r ON r.run_id=i.run_id
			WHERE r.backfill_id=m.backfill_id AND i.object_type=?
			AND i.object_id=m.news_event_id AND i.status=?
		)`, strings.TrimSpace(backfillID), start.UnixNano(), end.UnixNano(), NewsContextRunItemNewsEvent,
		NewsContextRunItemCompleted).Scan(&count)
	return count, wrapError(err, "count pending news context backfill events in range")
}

func (s *Store) CountNewsContextBackfillManifestInRange(ctx context.Context, backfillID string, start, end time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_backfill_news
		WHERE backfill_id=? AND event_unix_nano>=? AND event_unix_nano<?`,
		strings.TrimSpace(backfillID), start.UnixNano(), end.UnixNano()).Scan(&count)
	return count, wrapError(err, "count news context backfill manifest window")
}

type newsContextBackfillSourceStats struct {
	Total      int
	Pending    int
	Deferred   int
	Claimed    int
	TextBytes  int64
	EarliestAt time.Time
	LatestAt   time.Time
}

func (s *Store) NewsContextBackfillSourceStats(ctx context.Context, cutoff time.Time) (newsContextBackfillSourceStats, error) {
	var out newsContextBackfillSourceStats
	var earliest, latest sql.NullTime
	err := s.marketDB.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN COALESCE(context_status,'pending')='pending' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN COALESCE(context_status,'pending')='deferred' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN COALESCE(context_status,'pending')='claimed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(LENGTH(COALESCE(title,''))+LENGTH(COALESCE(summary,''))+LENGTH(COALESCE(content,''))),0),
		MIN(event_at), MAX(event_at)
		FROM stockv2_news_events WHERE event_at<? AND (
			COALESCE(context_status,'pending') IN ('pending','claimed') OR
			(COALESCE(context_status,'pending')='deferred' AND TRIM(COALESCE(context_run_id,''))='')
		)`, cutoff).Scan(&out.Total, &out.Pending,
		&out.Deferred, &out.Claimed, &out.TextBytes, &earliest, &latest)
	assignNullTime(&out.EarliestAt, earliest)
	assignNullTime(&out.LatestAt, latest)
	return out, wrapError(err, "get news context backfill source stats")
}

// ClaimNewsContextEvents gives a source news item one durable owner before a
// run manifest is created. The caller releases the claim if the SQLite write
// fails. The ordinary path never transfers another run's ownership.
func (s *Store) ClaimNewsContextEvents(ctx context.Context, runID string, start, end time.Time) ([]string, error) {
	return s.claimNewsContextEvents(ctx, runID, start, end, false, false)
}

// ClaimRealtimeNewsContextEvents also reclaims one explicitly deferred result.
// The durable event counter makes the retry bounded across restarts and across
// the overlapping hourly/four-hour/daily scheduled windows.
func (s *Store) ClaimRealtimeNewsContextEvents(ctx context.Context, runID string, start, end time.Time) ([]string, error) {
	return s.claimNewsContextEvents(ctx, runID, start, end, false, true)
}

func (s *Store) ClaimNewsContextFinalReviewEvents(ctx context.Context, runID string, start, end time.Time) ([]string, error) {
	return s.claimNewsContextEvents(ctx, runID, start, end, true, false)
}

func (s *Store) claimNewsContextEvents(ctx context.Context, runID string, start, end time.Time, reclaimInactive, retryFirstDeferral bool) ([]string, error) {
	if strings.TrimSpace(runID) == "" || start.IsZero() || !end.After(start) {
		return nil, ErrInvalidNewsContextInput
	}
	rows, err := s.marketDB.db.QueryContext(ctx, `SELECT id,COALESCE(context_status,'pending'),COALESCE(context_run_id,''),
		COALESCE(context_defer_retry_count,0)
		FROM stockv2_news_events
		WHERE event_at>=? AND event_at<? AND (COALESCE(context_status,'pending')=? OR
		(COALESCE(context_status,'pending')=? AND TRIM(COALESCE(context_run_id,''))='') OR
		(?=1 AND COALESCE(context_status,'pending')=? AND TRIM(COALESCE(context_run_id,''))<>''
			AND COALESCE(context_defer_retry_count,0)=0) OR
		(?=1 AND COALESCE(context_status,'pending')=?))
		ORDER BY event_at ASC, id ASC`, start, end, NewsEventContextPending, NewsEventContextDeferred,
		boolToInt(retryFirstDeferral), NewsEventContextDeferred,
		boolToInt(reclaimInactive), NewsEventContextClaimed)
	if err != nil {
		return nil, wrapError(err, "list news context events to claim")
	}
	ids := make([]string, 0)
	staleOwners := make(map[string]string)
	retryOwners := make(map[string]string)
	for rows.Next() {
		var id, status, owner string
		var deferRetryCount int
		if err := rows.Scan(&id, &status, &owner, &deferRetryCount); err != nil {
			rows.Close()
			return nil, wrapError(err, "scan news context event claim")
		}
		ids = append(ids, id)
		if status == NewsEventContextDeferred && owner != "" {
			if !retryFirstDeferral || deferRetryCount != 0 {
				rows.Close()
				return nil, ErrInvalidNewsContextInput
			}
			ownerRun, err := s.GetNewsContextRun(ctx, owner)
			if errors.Is(err, ErrNewsContextRunNotFound) {
				retryOwners[id] = owner
				continue
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			switch ownerRun.Status {
			case NewsContextRunStatusCompleted, NewsContextRunStatusFailed, NewsContextRunStatusWaitingReview:
				retryOwners[id] = owner
				continue
			default:
				rows.Close()
				return nil, ErrNewsContextAlreadyRunning
			}
		}
		if status != NewsEventContextClaimed {
			continue
		}
		if owner == "" || owner == runID {
			staleOwners[id] = owner
			continue
		}
		ownerRun, err := s.GetNewsContextRun(ctx, owner)
		if errors.Is(err, ErrNewsContextRunNotFound) {
			staleOwners[id] = owner
			continue
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		switch ownerRun.Status {
		case NewsContextRunStatusCompleted, NewsContextRunStatusFailed:
			staleOwners[id] = owner
		case NewsContextRunStatusRunning, NewsContextRunStatusPending, NewsContextRunStatusWaitingReview:
			rows.Close()
			return nil, ErrNewsContextAlreadyRunning
		default:
			rows.Close()
			return nil, ErrNewsContextAlreadyRunning
		}
	}
	if err := rows.Close(); err != nil {
		return nil, wrapError(err, "close news context event claims")
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin news context event claim")
	}
	defer tx.Rollback()
	if len(ids) == 0 {
		return ids, tx.Commit()
	}
	now := time.Now()
	for _, id := range ids {
		staleOwner := staleOwners[id]
		retryOwner := retryOwners[id]
		retryDeferral := retryOwner != ""
		result, err := tx.ExecContext(ctx, `UPDATE stockv2_news_events SET
			context_status=?, context_run_id=?, context_covered_at=NULL,
			protected_reason=NULL,
			context_defer_retry_count=CASE WHEN ?=1 THEN 1 ELSE COALESCE(context_defer_retry_count,0) END,
			updated_at=? WHERE id=?
			AND (COALESCE(context_status,'pending')=? OR
			(COALESCE(context_status,'pending')=? AND TRIM(COALESCE(context_run_id,''))='') OR
			(?=1 AND COALESCE(context_status,'pending')=? AND context_run_id=?
				AND COALESCE(context_defer_retry_count,0)=0) OR
			(?=1 AND COALESCE(context_status,'pending')=? AND COALESCE(context_run_id,'') IN (?,?)))`, NewsEventContextClaimed,
			runID, boolToInt(retryDeferral), now, id, NewsEventContextPending, NewsEventContextDeferred,
			boolToInt(retryDeferral), NewsEventContextDeferred, retryOwner,
			boolToInt(reclaimInactive), NewsEventContextClaimed, runID, staleOwner)
		if err != nil {
			return nil, wrapError(err, "claim news context event")
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, ErrNewsContextAlreadyRunning
		}
	}
	return ids, wrapError(tx.Commit(), "commit news context event claims")
}

// ClaimNewsContextBackfillEvents only claims IDs frozen in the parent manifest.
// This keeps retry/restart work on the same durable checklist; late news is
// added explicitly by the final rescan before it can enter a historical run.
func (s *Store) ClaimNewsContextBackfillEvents(ctx context.Context, backfillID, runID string, start, end time.Time) ([]string, error) {
	if strings.TrimSpace(backfillID) == "" || strings.TrimSpace(runID) == "" || start.IsZero() || !end.After(start) {
		return nil, ErrInvalidNewsContextInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.news_event_id
		FROM stockv2_news_context_backfill_news m
		WHERE m.backfill_id=? AND m.event_unix_nano>=? AND m.event_unix_nano<? AND NOT EXISTS (
			SELECT 1 FROM stockv2_news_context_run_items i
			JOIN stockv2_news_context_backfill_runs r ON r.run_id=i.run_id
			WHERE r.backfill_id=m.backfill_id AND i.object_type=?
			AND i.object_id=m.news_event_id AND i.status=?
		) ORDER BY m.event_unix_nano ASC,m.news_event_id ASC`, strings.TrimSpace(backfillID), start.UnixNano(), end.UnixNano(),
		NewsContextRunItemNewsEvent, NewsContextRunItemCompleted)
	if err != nil {
		return nil, wrapError(err, "list frozen news context backfill events")
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, wrapError(err, "scan frozen news context backfill event")
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return ids, nil
	}
	staleOwners := make(map[string]string)
	for _, id := range ids {
		var status, owner string
		if err := s.marketDB.db.QueryRowContext(ctx, `SELECT COALESCE(context_status,'pending'),
			COALESCE(context_run_id,'') FROM stockv2_news_events WHERE id=?`, id).Scan(&status, &owner); err != nil {
			return nil, wrapError(err, "read frozen news context event owner")
		}
		if status == NewsEventContextDeferred {
			if owner == "" || owner == runID {
				continue
			}
			var requeued int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)
				FROM stockv2_news_context_backfill_runs r
				JOIN stockv2_news_context_run_items i ON i.run_id=r.run_id
				JOIN stockv2_news_context_backfill_news m
					ON m.backfill_id=r.backfill_id AND m.news_event_id=i.object_id
				WHERE r.backfill_id=? AND i.run_id=? AND i.object_type=? AND i.object_id=?
				AND m.defer_retry_count=1 AND i.status=? AND i.disposition=?`, strings.TrimSpace(backfillID), owner,
				NewsContextRunItemNewsEvent, id, NewsContextRunItemDeferred,
				NewsEventContextDeferred).Scan(&requeued); err != nil {
				return nil, wrapError(err, "verify requeued deferred historical news owner")
			}
			if requeued != 1 {
				return nil, ErrNewsContextAlreadyRunning
			}
			staleOwners[id] = owner
			continue
		}
		if status != NewsEventContextClaimed || owner == "" || owner == runID {
			continue
		}
		ownerRun, err := s.GetNewsContextRun(ctx, owner)
		if err == nil && (ownerRun.Status == NewsContextRunStatusRunning || ownerRun.Status == NewsContextRunStatusPending) {
			return nil, ErrNewsContextAlreadyRunning
		}
		if err != nil && !errors.Is(err, ErrNewsContextRunNotFound) {
			return nil, err
		}
		staleOwners[id] = owner
	}
	tx, err := s.marketDB.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin frozen news context event claim")
	}
	defer tx.Rollback()
	now := time.Now()
	for _, id := range ids {
		staleOwner := staleOwners[id]
		result, err := tx.ExecContext(ctx, `UPDATE stockv2_news_events SET
			context_status=?,context_run_id=?,context_covered_at=NULL,
			protected_reason=NULL,updated_at=? WHERE id=? AND (
			COALESCE(context_status,'pending')=? OR
			(COALESCE(context_status,'pending')=? AND (TRIM(COALESCE(context_run_id,''))='' OR context_run_id=? OR context_run_id=?)) OR
			(COALESCE(context_status,'pending')=? AND (context_run_id=? OR context_run_id=?))
		)`, NewsEventContextClaimed, runID, now, id, NewsEventContextPending, NewsEventContextDeferred,
			runID, staleOwner, NewsEventContextClaimed, runID, staleOwner)
		if err != nil {
			return nil, wrapError(err, "claim frozen news context event")
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, ErrNewsContextAlreadyRunning
		}
	}
	return ids, wrapError(tx.Commit(), "commit frozen news context event claims")
}

func (s *Store) ReleaseNewsContextEventClaims(ctx context.Context, runID string) error {
	_, err := s.marketDB.db.ExecContext(ctx, `UPDATE stockv2_news_events SET
		context_status=?, context_run_id=NULL, protected_reason=NULL, updated_at=?
		WHERE context_status=? AND context_run_id=?`, NewsEventContextPending, time.Now(),
		NewsEventContextClaimed, strings.TrimSpace(runID))
	return wrapError(err, "release news context event claims")
}

func (s *Store) RequeueNewsContextRunEventItems(ctx context.Context, runID string, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now()
		for _, eventID := range eventIDs {
			event, err := s.GetNewsEvent(ctx, eventID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_run_items
				(id,run_id,object_type,object_id,status,source_at,created_at,updated_at)
				VALUES (?,?,?,?,?,?,?,?)
				ON CONFLICT(run_id,object_type,object_id) DO UPDATE SET
					status=excluded.status, disposition=NULL, thread_id=NULL, version_id=NULL,
					agent_run_id=NULL, error_message=NULL, source_at=excluded.source_at, updated_at=excluded.updated_at`,
				generateID(), runID, NewsContextRunItemNewsEvent, strings.TrimSpace(eventID),
				NewsContextRunItemPending, event.EventAt, now, now)
			if err != nil {
				return wrapError(err, "requeue news context run event item")
			}
		}
		return nil
	})
}

func (s *Store) ValidateNewsContextFinalReviewEventManifest(ctx context.Context, runID string, eventIDs []string) error {
	runID = strings.TrimSpace(runID)
	eventIDs = uniqueNonEmptyStrings(eventIDs)
	if runID == "" {
		return ErrInvalidNewsContextInput
	}
	if len(eventIDs) == 0 {
		return nil
	}
	args := make([]any, 0, len(eventIDs)+2)
	args = append(args, NewsEventContextClaimed, runID)
	for _, id := range eventIDs {
		args = append(args, id)
	}
	var claimed int
	if err := s.marketDB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_events
		WHERE context_status=? AND context_run_id=? AND id IN (`+sqlPlaceholders(len(eventIDs))+`)`, args...).Scan(&claimed); err != nil {
		return wrapError(err, "verify final review news claims")
	}
	itemArgs := make([]any, 0, len(eventIDs)+2)
	itemArgs = append(itemArgs, runID, NewsContextRunItemNewsEvent)
	for _, id := range eventIDs {
		itemArgs = append(itemArgs, id)
	}
	var manifested int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT object_id)
		FROM stockv2_news_context_run_items WHERE run_id=? AND object_type=?
		AND object_id IN (`+sqlPlaceholders(len(eventIDs))+`)`, itemArgs...).Scan(&manifested); err != nil {
		return wrapError(err, "verify final review news manifest")
	}
	if claimed != len(eventIDs) || manifested != len(eventIDs) {
		return fmt.Errorf("%w: final review news coverage claimed=%d manifested=%d expected=%d",
			ErrInvalidNewsContextInput, claimed, manifested, len(eventIDs))
	}
	return nil
}
