package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"phantom-lancer/internal/safelog"
)

var ErrAssetMaintenanceRetryQueueEmpty = errors.New("asset maintenance retry queue empty")

// RecoverInterruptedAssetMaintenanceJobs preserves the frozen target set. Work
// that had not reached a terminal checked state is made immediately retryable;
// completed items are never reopened.
func (s *Store) RecoverInterruptedAssetMaintenanceJobs(ctx context.Context, reason string, now time.Time) ([]string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin recover interrupted asset maintenance jobs")
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM stockv2_update_jobs WHERE status = 'running' ORDER BY created_at`)
	if err != nil {
		return nil, wrapError(err, "list interrupted asset maintenance jobs")
	}
	jobIDs := make([]string, 0)
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			rows.Close()
			return nil, wrapError(err, "scan interrupted asset maintenance job")
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err := rows.Close(); err != nil {
		return nil, wrapError(err, "close interrupted asset maintenance jobs")
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate interrupted asset maintenance jobs")
	}
	message := safelog.Text(reason, 800)
	for _, jobID := range jobIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE stockv2_asset_maintenance_items
			SET status = 'retry_wait',
			    attempt_count = attempt_count + CASE WHEN status = 'running' THEN 1 ELSE 0 END,
			    next_retry_at = ?, checked_at = NULL, finished_at = ?,
			    error_message = ?, updated_at = ?
			WHERE job_id = ? AND status IN ('pending','running')
		`, now, now, message, now, jobID); err != nil {
			return nil, wrapError(err, "recover interrupted asset maintenance targets")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE stockv2_update_jobs SET error_message = ? WHERE id = ? AND status = 'running'
		`, message, jobID); err != nil {
			return nil, wrapError(err, "mark interrupted asset maintenance parent")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapError(err, "commit interrupted asset maintenance recovery")
	}
	return jobIDs, nil
}

func (s *Store) RecoverClaimedAssetMaintenanceRetries(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET status = 'retry_wait', next_retry_at = ?, finished_at = ?,
			error_message = 'retry worker interrupted before completion', updated_at = ?
		WHERE status = 'running'
	`, now, now, now)
	return wrapError(err, "recover claimed asset maintenance retries")
}

// PauseAssetMaintenanceJob atomically hands the frozen, unstarted tail to the
// durable retry queue before the in-process worker exits. Unchecked items keep
// checked_at NULL so coverage cannot mistake a resource pause for a check.
func (s *Store) PauseAssetMaintenanceJob(ctx context.Context, jobID, reason string, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin pause asset maintenance job")
	}
	defer tx.Rollback()
	message := safelog.Text(reason, 800)
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET status = 'retry_wait', next_retry_at = ?, checked_at = NULL,
			finished_at = ?, error_message = ?, updated_at = ?
		WHERE job_id = ? AND status = 'pending'
	`, now, now, message, now, jobID); err != nil {
		return wrapError(err, "queue paused asset maintenance targets")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_update_jobs
		SET status = 'paused', coverage_status = 'incomplete', freshness_status = 'retrying',
			checked_count = (
				SELECT COUNT(*) FROM stockv2_asset_maintenance_items
				WHERE job_id = ? AND status IN ('completed','retry_wait','failed') AND checked_at IS NOT NULL
			),
			processed_count = (
				SELECT COUNT(*) FROM stockv2_asset_maintenance_items
				WHERE job_id = ? AND status IN ('completed','retry_wait','failed') AND checked_at IS NOT NULL
			),
			retry_count = (
				SELECT COUNT(*) FROM stockv2_asset_maintenance_items WHERE job_id = ? AND status = 'retry_wait'
			),
			failed_count = (
				SELECT COUNT(*) FROM stockv2_asset_maintenance_items WHERE job_id = ? AND status = 'failed'
			),
			end_at = ?, error_message = ?
		WHERE id = ?
	`, jobID, jobID, jobID, jobID, now, message, jobID)
	if err != nil {
		return wrapError(err, "persist paused asset maintenance job")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return wrapError(err, "check paused asset maintenance job")
		}
		return ErrUpdateJobNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_maintenance_slots
		SET status = 'incomplete', covered_at = NULL, updated_at = ?
		WHERE job_id = ?
	`, now, jobID); err != nil {
		return wrapError(err, "persist paused asset maintenance slot")
	}
	return wrapError(tx.Commit(), "commit paused asset maintenance job")
}

func (s *Store) ClaimDueAssetMaintenanceRetry(ctx context.Context, now time.Time) (AssetMaintenanceItem, error) {
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssetMaintenanceItem{}, wrapError(err, "begin claim asset maintenance retry")
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `
		SELECT item.id
		FROM stockv2_asset_maintenance_items item
			WHERE item.status = 'retry_wait' AND item.next_retry_at <= ?
			  AND item.attempt_count < ?
		ORDER BY item.next_retry_at, item.created_at, item.symbol
		LIMIT 1
	`, now, assetMaintenanceRetryMaxAttempts).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return AssetMaintenanceItem{}, ErrAssetMaintenanceRetryQueueEmpty
	}
	if err != nil {
		return AssetMaintenanceItem{}, wrapError(err, "select asset maintenance retry")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET status = 'running', started_at = ?, finished_at = NULL, updated_at = ?
		WHERE id = ? AND status = 'retry_wait'
	`, now, now, id)
	if err != nil {
		return AssetMaintenanceItem{}, wrapError(err, "claim asset maintenance retry")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return AssetMaintenanceItem{}, err
		}
		return AssetMaintenanceItem{}, ErrAssetMaintenanceRetryQueueEmpty
	}
	item, err := scanAssetMaintenanceItem(tx.QueryRowContext(ctx, assetMaintenanceItemSelectSQL()+` WHERE id = ?`, id))
	if err != nil {
		return AssetMaintenanceItem{}, wrapError(err, "read claimed asset maintenance retry")
	}
	if err := tx.Commit(); err != nil {
		return AssetMaintenanceItem{}, wrapError(err, "commit claimed asset maintenance retry")
	}
	return item, nil
}

func (s *Store) RequeueClaimedAssetMaintenanceItem(
	ctx context.Context,
	item AssetMaintenanceItem,
	message string,
	now time.Time,
) error {
	item.AttemptCount++
	item.CheckedAt = now
	item.FinishedAt = now
	item.UpdatedAt = now
	item.ErrorMessage = safelog.Text(message, 800)
	if item.AttemptCount >= assetMaintenanceRetryMaxAttempts {
		item.Status = AssetMaintenanceItemStatusFailed
	} else {
		item.Status = AssetMaintenanceItemStatusRetryWait
		item.NextRetryAt = now.Add(assetMaintenanceRetryDelay(item.AttemptCount))
	}
	_, err := s.UpsertAssetMaintenanceItem(ctx, item)
	return err
}

func (s *Store) ListAssetMaintenanceJobFailures(ctx context.Context, jobID string) ([]UpdateFailure, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol, COALESCE(error_message, '')
		FROM stockv2_asset_maintenance_items
		WHERE job_id = ? AND status IN ('retry_wait','failed')
		ORDER BY symbol
	`, jobID)
	if err != nil {
		return nil, wrapError(err, "list asset maintenance job failures")
	}
	defer rows.Close()
	items := make([]UpdateFailure, 0)
	for rows.Next() {
		var item UpdateFailure
		if err := rows.Scan(&item.Symbol, &item.Reason); err != nil {
			return nil, wrapError(err, "scan asset maintenance job failure")
		}
		items = append(items, item)
	}
	return items, wrapError(rows.Err(), "iterate asset maintenance job failures")
}
