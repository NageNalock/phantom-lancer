package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const stockProfileAIQueuePayloadMaxBytes = 256 << 10

var (
	ErrStockProfileAIQueueEmpty      = errors.New("stock profile ai queue empty")
	ErrStockProfileAIQueueLeaseStale = errors.New("stock profile ai queue lease stale")
)

func (s *Store) EnqueueStockProfileAI(ctx context.Context, item StockProfileAIQueueItem) (StockProfileAIQueueItem, error) {
	return s.enqueueStockProfileAI(ctx, item, false)
}

// EnqueueStockProfileAIIfAbsent is reserved for upgrading pre-queue AgentRuns.
// A concurrently created queue row always wins, so recovery cannot replace a
// newer announcement or base-profile payload.
func (s *Store) EnqueueStockProfileAIIfAbsent(ctx context.Context, item StockProfileAIQueueItem) (StockProfileAIQueueItem, error) {
	return s.enqueueStockProfileAI(ctx, item, true)
}

func (s *Store) enqueueStockProfileAI(ctx context.Context, item StockProfileAIQueueItem, insertOnly bool) (StockProfileAIQueueItem, error) {
	item.Symbol = strings.TrimSpace(item.Symbol)
	item.DesiredInputVersion = strings.TrimSpace(item.DesiredInputVersion)
	if item.Symbol == "" || item.DesiredInputVersion == "" {
		return StockProfileAIQueueItem{}, errors.New("stock profile ai queue requires symbol and input version")
	}
	if len(item.PayloadJSON) == 0 || len(item.PayloadJSON) > stockProfileAIQueuePayloadMaxBytes {
		return StockProfileAIQueueItem{}, fmt.Errorf("stock profile ai queue payload must be 1-%d bytes", stockProfileAIQueuePayloadMaxBytes)
	}
	now := time.Now()
	if item.Status == "" {
		item.Status = StockProfileAIQueueStatusReady
	}
	if item.AvailableAt.IsZero() {
		item.AvailableAt = now
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if item.TriggerReason == "" {
		item.TriggerReason = AssetAIDecisionMissing
	}

	conflictClause := `
		ON CONFLICT(symbol) DO UPDATE SET
			market = excluded.market,
			priority = CASE
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN MAX(stockv2_stock_profile_ai_queue.priority, excluded.priority)
				ELSE excluded.priority
			END,
			trigger_reason = excluded.trigger_reason,
			requested_by = excluded.requested_by,
			desired_input_version = excluded.desired_input_version,
			payload_json = excluded.payload_json,
			status = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN 'running'
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND stockv2_stock_profile_ai_queue.status IN ('ready', 'retry_wait', 'completed')
				THEN stockv2_stock_profile_ai_queue.status
				ELSE excluded.status
			END,
			available_at = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'running'
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.available_at
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN excluded.available_at
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND stockv2_stock_profile_ai_queue.status IN ('ready', 'retry_wait', 'completed')
				THEN stockv2_stock_profile_ai_queue.available_at
				ELSE excluded.available_at
			END,
			attempt_count = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN stockv2_stock_profile_ai_queue.attempt_count
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND stockv2_stock_profile_ai_queue.status != 'failed'
				THEN stockv2_stock_profile_ai_queue.attempt_count
				ELSE 0
			END,
			current_agent_run_id = CASE WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN stockv2_stock_profile_ai_queue.current_agent_run_id ELSE NULL END,
			last_error = CASE WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN stockv2_stock_profile_ai_queue.last_error ELSE NULL END,
			updated_at = excluded.updated_at`
	if insertOnly {
		conflictClause = `ON CONFLICT(symbol) DO NOTHING`
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profile_ai_queue (
			symbol, market, status, priority, trigger_reason, requested_by,
			desired_input_version, claimed_input_version, payload_json,
			current_agent_run_id, attempt_count, available_at,
			lease_owner, lease_token, lease_expires_at,
			completed_input_version, completed_at, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL, 0, ?, NULL, NULL, NULL, NULL, NULL, NULL, ?, ?)
	`+conflictClause, item.Symbol, item.Market, item.Status, item.Priority, item.TriggerReason, item.RequestedBy,
		item.DesiredInputVersion, item.PayloadJSON, item.AvailableAt, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return StockProfileAIQueueItem{}, wrapError(err, "enqueue stock profile ai")
	}
	return s.GetStockProfileAIQueueItem(ctx, item.Symbol)
}

func (s *Store) GetStockProfileAIQueueItem(ctx context.Context, symbol string) (StockProfileAIQueueItem, error) {
	return scanStockProfileAIQueueItem(s.db.QueryRowContext(ctx, stockProfileAIQueueSelect+` WHERE symbol = ?`, strings.TrimSpace(symbol)))
}

func (s *Store) ClaimStockProfileAI(ctx context.Context, workerID string, now time.Time, leaseTTL time.Duration) (StockProfileAIQueueLease, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if leaseTTL <= 0 {
		leaseTTL = 2 * time.Minute
	}
	leaseToken := generateID()
	row := s.db.QueryRowContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = 'running',
			claimed_input_version = desired_input_version,
			lease_owner = ?, lease_token = ?, lease_expires_at = ?,
			current_agent_run_id = NULL,
			attempt_count = attempt_count + 1,
			updated_at = ?
		WHERE symbol = (
			SELECT symbol
			FROM stockv2_stock_profile_ai_queue
			WHERE status IN ('ready', 'retry_wait') AND available_at <= ?
			ORDER BY priority DESC, available_at, updated_at
			LIMIT 1
		)
		RETURNING `+stockProfileAIQueueColumns,
		workerID, leaseToken, now.Add(leaseTTL), now, now)
	item, err := scanStockProfileAIQueueItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StockProfileAIQueueLease{}, ErrStockProfileAIQueueEmpty
	}
	if err != nil {
		return StockProfileAIQueueLease{}, wrapError(err, "claim stock profile ai")
	}
	return StockProfileAIQueueLease{StockProfileAIQueueItem: item}, nil
}

func (s *Store) BindStockProfileAIRun(ctx context.Context, lease StockProfileAIQueueLease, runID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin bind stock profile ai run")
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET current_agent_run_id = ?, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND lease_token = ?
	`, strings.TrimSpace(runID), time.Now(), lease.Symbol, lease.LeaseToken)
	if err != nil {
		return wrapError(err, "bind stock profile ai run")
	}
	if err := requireStockProfileAIQueueLease(result); err != nil {
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_update_tasks
		SET status = 'running', agent_run_id = ?, ai_profile_status = 'running', updated_at = ?
		WHERE symbol = ? AND status = 'queued' AND COALESCE(agent_run_id, '') = ''
	`, runID, now, lease.Symbol); err != nil {
		return wrapError(err, "bind stock profile update tasks to ai run")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET agent_run_id = ?, ai_profile_status = 'running', ai_queue_status = 'running', updated_at = ?
		WHERE symbol = ? AND ai_queue_status IN ('ready', 'retry_wait') AND COALESCE(agent_run_id, '') = ''
	`, runID, now, lease.Symbol); err != nil {
		return wrapError(err, "bind asset maintenance items to ai run")
	}
	return wrapError(tx.Commit(), "commit bound stock profile ai run")
}

func (s *Store) SyncStockProfileUpdateTaskAIQueue(ctx context.Context, taskID, symbol string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_update_tasks
		SET status = CASE (SELECT status FROM stockv2_stock_profile_ai_queue WHERE symbol = ?)
				WHEN 'running' THEN 'running'
				WHEN 'completed' THEN 'completed'
				WHEN 'failed' THEN 'partial'
				ELSE 'queued'
			END,
			agent_run_id = (SELECT current_agent_run_id FROM stockv2_stock_profile_ai_queue WHERE symbol = ?),
			ai_profile_status = CASE (SELECT status FROM stockv2_stock_profile_ai_queue WHERE symbol = ?)
				WHEN 'running' THEN 'running'
				WHEN 'completed' THEN 'ready'
				WHEN 'failed' THEN 'failed'
				ELSE 'queued'
			END,
			finished_at = CASE
				WHEN (SELECT status FROM stockv2_stock_profile_ai_queue WHERE symbol = ?) IN ('completed', 'failed') THEN ?
				ELSE NULL
			END,
			updated_at = ?
		WHERE id = ?
		  AND EXISTS (SELECT 1 FROM stockv2_stock_profile_ai_queue WHERE symbol = ?)
	`, symbol, symbol, symbol, symbol, now, now, taskID, symbol)
	return wrapError(err, "sync stock profile update task ai queue")
}

// FinalizeLegacyStockProfileAIRunMigration moves records that referenced an
// in-memory pre-queue AgentRun onto the durable queue state and supersedes the
// old run in one SQLite transaction. targetQueueStatus is only used when the
// old run is already satisfied or cannot be recovered; otherwise the queue row
// is read inside the transaction so a completed item cannot be reset to queued.
func (s *Store) FinalizeLegacyStockProfileAIRunMigration(
	ctx context.Context,
	runID, symbol, targetQueueStatus, reason string,
) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", wrapError(err, "begin legacy stock profile ai migration")
	}
	defer tx.Rollback()

	queueStatus := strings.TrimSpace(targetQueueStatus)
	currentRunID := ""
	queueError := ""
	if queueStatus == "" {
		err = tx.QueryRowContext(ctx, `
			SELECT status, COALESCE(current_agent_run_id, ''), COALESCE(last_error, '')
			FROM stockv2_stock_profile_ai_queue WHERE symbol = ?
		`, strings.TrimSpace(symbol)).Scan(&queueStatus, &currentRunID, &queueError)
		if err != nil {
			return "", wrapError(err, "read queue state for legacy stock profile ai migration")
		}
	}

	taskStatus := StockProfileUpdateStatusQueued
	aiStatus := StockProfileAIStatusQueued
	taskRunID := any(nil)
	finishedAt := any(nil)
	switch queueStatus {
	case StockProfileAIQueueStatusReady, StockProfileAIQueueStatusRetryWait:
	case StockProfileAIQueueStatusRunning:
		taskStatus = StockProfileUpdateStatusRunning
		aiStatus = StockProfileAIStatusRunning
		taskRunID = nullableString(currentRunID)
	case StockProfileAIQueueStatusCompleted:
		taskStatus = StockProfileUpdateStatusCompleted
		aiStatus = StockProfileAIStatusReady
		finishedAt = time.Now()
	case StockProfileAIQueueStatusFailed:
		taskStatus = StockProfileUpdateStatusPartial
		aiStatus = StockProfileAIStatusFailed
		finishedAt = time.Now()
		if queueError == "" {
			queueError = reason
		}
	default:
		return "", fmt.Errorf("invalid stock profile ai queue status %q", queueStatus)
	}

	now := time.Now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_update_tasks
		SET status = ?, agent_run_id = ?, ai_profile_status = ?, ai_profile_error = ?,
			finished_at = ?, updated_at = ?
		WHERE symbol = ? AND agent_run_id = ?
	`, taskStatus, taskRunID, aiStatus, nullableString(safelog.Text(queueError, 500)), finishedAt, now, symbol, runID); err != nil {
		return "", wrapError(err, "rebind legacy stock profile update task")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET agent_run_id = ?, ai_profile_status = ?, ai_queue_status = ?, updated_at = ?
		WHERE symbol = ? AND agent_run_id = ?
	`, taskRunID, aiStatus, queueStatus, now, symbol, runID); err != nil {
		return "", wrapError(err, "rebind legacy asset maintenance item")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_agent_runs
		SET status = 'superseded', error_message = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND status IN ('ready', 'running')
	`, safelog.Text(reason, 500), now, now, runID)
	if err != nil {
		return "", wrapError(err, "supersede migrated stock profile agent run")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", fmt.Errorf("legacy stock profile agent run %s is no longer active", runID)
	}
	if err := tx.Commit(); err != nil {
		return "", wrapError(err, "commit legacy stock profile ai migration")
	}
	return queueStatus, nil
}

func updateBoundStockProfileAIRecordsWithTx(
	ctx context.Context,
	tx *sql.Tx,
	symbol, runID, queueStatus, message string,
) error {
	runID = strings.TrimSpace(runID)
	taskStatus := StockProfileUpdateStatusQueued
	aiStatus := StockProfileAIStatusQueued
	boundRunID := any(nil)
	finishedAt := any(nil)
	aiError := ""
	switch queueStatus {
	case StockProfileAIQueueStatusReady:
	case StockProfileAIQueueStatusRetryWait:
		aiError = safelog.Text(message, 500)
	case StockProfileAIQueueStatusRunning:
		taskStatus = StockProfileUpdateStatusRunning
		aiStatus = StockProfileAIStatusRunning
		boundRunID = nullableString(runID)
	case StockProfileAIQueueStatusCompleted:
		taskStatus = StockProfileUpdateStatusCompleted
		aiStatus = StockProfileAIStatusReady
		boundRunID = nullableString(runID)
		finishedAt = time.Now()
	case StockProfileAIQueueStatusFailed:
		taskStatus = StockProfileUpdateStatusPartial
		aiStatus = StockProfileAIStatusFailed
		boundRunID = nullableString(runID)
		aiError = safelog.Text(message, 500)
		finishedAt = time.Now()
	default:
		return fmt.Errorf("invalid stock profile ai queue status %q", queueStatus)
	}
	taskFilter := "symbol = ? AND agent_run_id = ?"
	taskFilterArgs := []any{symbol, runID}
	assetFilter := "symbol = ? AND agent_run_id = ?"
	assetFilterArgs := []any{symbol, runID}
	if runID == "" {
		taskFilter = "symbol = ? AND status IN ('queued', 'running') AND COALESCE(agent_run_id, '') = ''"
		taskFilterArgs = []any{symbol}
		assetFilter = "symbol = ? AND ai_queue_status IN ('ready', 'running', 'retry_wait') AND COALESCE(agent_run_id, '') = ''"
		assetFilterArgs = []any{symbol}
	}
	now := time.Now()
	taskArgs := []any{taskStatus, boundRunID, aiStatus, nullableString(aiError), finishedAt, now}
	taskArgs = append(taskArgs, taskFilterArgs...)
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_update_tasks
		SET status = ?, agent_run_id = ?, ai_profile_status = ?, ai_profile_error = ?,
			finished_at = ?, updated_at = ?
		WHERE `+taskFilter, taskArgs...); err != nil {
		return wrapError(err, "sync bound stock profile ai task")
	}
	assetArgs := []any{boundRunID, aiStatus, queueStatus, now}
	assetArgs = append(assetArgs, assetFilterArgs...)
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET agent_run_id = ?, ai_profile_status = ?, ai_queue_status = ?, updated_at = ?
		WHERE `+assetFilter, assetArgs...); err != nil {
		return wrapError(err, "sync bound asset maintenance ai item")
	}
	return nil
}

func (s *Store) RenewStockProfileAILease(ctx context.Context, lease StockProfileAIQueueLease, expiresAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET lease_expires_at = ?, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND lease_token = ?
	`, expiresAt, time.Now(), lease.Symbol, lease.LeaseToken)
	if err != nil {
		return wrapError(err, "renew stock profile ai lease")
	}
	return requireStockProfileAIQueueLease(result)
}

func (s *Store) CompleteStockProfileAI(ctx context.Context, lease StockProfileAIQueueLease) (bool, error) {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, wrapError(err, "begin complete stock profile ai")
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = CASE WHEN desired_input_version = claimed_input_version THEN 'completed' ELSE 'ready' END,
			completed_input_version = CASE WHEN desired_input_version = claimed_input_version THEN claimed_input_version ELSE completed_input_version END,
			completed_at = CASE WHEN desired_input_version = claimed_input_version THEN ? ELSE completed_at END,
			available_at = available_at,
			attempt_count = CASE WHEN desired_input_version = claimed_input_version THEN attempt_count ELSE 0 END,
			current_agent_run_id = NULL,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = NULL, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND lease_token = ?
		RETURNING status
	`, now, now, lease.Symbol, lease.LeaseToken)
	var status string
	if err := row.Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return false, ErrStockProfileAIQueueLeaseStale
	} else if err != nil {
		return false, wrapError(err, "complete stock profile ai")
	}
	if err := updateBoundStockProfileAIRecordsWithTx(ctx, tx, lease.Symbol, lease.CurrentAgentRunID, status, ""); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, wrapError(err, "commit completed stock profile ai")
	}
	return status == StockProfileAIQueueStatusReady, nil
}

func (s *Store) RetryStockProfileAI(ctx context.Context, lease StockProfileAIQueueLease, availableAt time.Time, message string, terminal bool) error {
	status := StockProfileAIQueueStatusRetryWait
	if terminal {
		status = StockProfileAIQueueStatusFailed
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin retry stock profile ai")
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = CASE WHEN desired_input_version = claimed_input_version THEN ? ELSE 'ready' END,
			available_at = CASE WHEN desired_input_version = claimed_input_version THEN ? ELSE available_at END,
			attempt_count = CASE WHEN desired_input_version = claimed_input_version THEN attempt_count ELSE 0 END,
			current_agent_run_id = NULL,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = ?, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND lease_token = ?
		RETURNING status
	`, status, availableAt, safelog.Text(message, 500), now, lease.Symbol, lease.LeaseToken)
	var actualStatus string
	if err := row.Scan(&actualStatus); errors.Is(err, sql.ErrNoRows) {
		return ErrStockProfileAIQueueLeaseStale
	} else if err != nil {
		return wrapError(err, "retry stock profile ai")
	}
	if err := updateBoundStockProfileAIRecordsWithTx(ctx, tx, lease.Symbol, lease.CurrentAgentRunID, actualStatus, message); err != nil {
		return err
	}
	return wrapError(tx.Commit(), "commit retried stock profile ai")
}

func (s *Store) DeferStockProfileAI(ctx context.Context, lease StockProfileAIQueueLease, availableAt time.Time, message string) error {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin defer stock profile ai")
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = CASE WHEN desired_input_version = claimed_input_version THEN 'retry_wait' ELSE 'ready' END,
			available_at = CASE WHEN desired_input_version = claimed_input_version THEN ? ELSE available_at END,
			attempt_count = CASE WHEN desired_input_version = claimed_input_version THEN MAX(attempt_count - 1, 0) ELSE 0 END,
			current_agent_run_id = NULL,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = ?, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND lease_token = ?
		RETURNING status
	`, availableAt, safelog.Text(message, 500), now, lease.Symbol, lease.LeaseToken)
	var actualStatus string
	if err := row.Scan(&actualStatus); errors.Is(err, sql.ErrNoRows) {
		return ErrStockProfileAIQueueLeaseStale
	} else if err != nil {
		return wrapError(err, "defer stock profile ai")
	}
	if err := updateBoundStockProfileAIRecordsWithTx(ctx, tx, lease.Symbol, lease.CurrentAgentRunID, actualStatus, message); err != nil {
		return err
	}
	return wrapError(tx.Commit(), "commit deferred stock profile ai")
}

func (s *Store) RecoverExpiredStockProfileAILeases(ctx context.Context, now time.Time) ([]string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	return s.recoverStockProfileAILeases(ctx, now, false)
}

func (s *Store) RecoverRunningStockProfileAILeases(ctx context.Context, now time.Time) ([]string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	return s.recoverStockProfileAILeases(ctx, now, true)
}

func (s *Store) recoverStockProfileAILeases(ctx context.Context, now time.Time, all bool) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapError(err, "begin recover expired stock profile ai leases")
	}
	defer tx.Rollback()

	where := "status = 'running' AND lease_expires_at < ?"
	args := []any{now}
	if all {
		where = "status = 'running'"
		args = nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT symbol, COALESCE(current_agent_run_id, '')
		FROM stockv2_stock_profile_ai_queue
		WHERE `+where, args...)
	if err != nil {
		return nil, wrapError(err, "list expired stock profile ai leases")
	}
	type recoveredLease struct {
		symbol string
		runID  string
	}
	var recovered []recoveredLease
	var runIDs []string
	for rows.Next() {
		var item recoveredLease
		if err := rows.Scan(&item.symbol, &item.runID); err != nil {
			rows.Close()
			return nil, err
		}
		recovered = append(recovered, item)
		if strings.TrimSpace(item.runID) != "" {
			runIDs = append(runIDs, item.runID)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	updateArgs := []any{now, now}
	if !all {
		updateArgs = append(updateArgs, now)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = 'ready', available_at = ?, current_agent_run_id = NULL,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = 'worker lease expired', updated_at = ?
		WHERE `+where, updateArgs...); err != nil {
		return nil, wrapError(err, "recover expired stock profile ai leases")
	}
	for _, item := range recovered {
		if strings.TrimSpace(item.runID) == "" {
			continue
		}
		if err := updateBoundStockProfileAIRecordsWithTx(
			ctx, tx, item.symbol, item.runID, StockProfileAIQueueStatusReady, "worker lease expired",
		); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE stockv2_agent_runs
			SET status = 'superseded', error_message = 'worker lease expired before completion',
				finished_at = ?, updated_at = ?
			WHERE id = ? AND status IN ('ready', 'running')
		`, now, now, item.runID); err != nil {
			return nil, wrapError(err, "supersede expired stock profile ai run")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapError(err, "commit recovered stock profile ai leases")
	}
	return runIDs, nil
}

func (s *Store) StockProfileAIQueueRunCurrent(ctx context.Context, symbol, runID string) (exists, current bool, err error) {
	var desired, claimed, currentRun string
	err = s.db.QueryRowContext(ctx, `
		SELECT desired_input_version, COALESCE(claimed_input_version, ''), COALESCE(current_agent_run_id, '')
		FROM stockv2_stock_profile_ai_queue WHERE symbol = ?
	`, strings.TrimSpace(symbol)).Scan(&desired, &claimed, &currentRun)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, desired != "" && desired == claimed && currentRun == strings.TrimSpace(runID), nil
}

func (s *Store) GetStockProfileAIQueueStats(ctx context.Context) (StockProfileAIQueueStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM stockv2_stock_profile_ai_queue GROUP BY status`)
	if err != nil {
		return StockProfileAIQueueStats{}, wrapError(err, "count stock profile ai queue")
	}
	defer rows.Close()
	var stats StockProfileAIQueueStats
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return StockProfileAIQueueStats{}, err
		}
		switch status {
		case StockProfileAIQueueStatusReady:
			stats.Ready = count
		case StockProfileAIQueueStatusRunning:
			stats.Running = count
		case StockProfileAIQueueStatusRetryWait:
			stats.Retrying = count
		case StockProfileAIQueueStatusCompleted:
			stats.Completed = count
		case StockProfileAIQueueStatusFailed:
			stats.Failed = count
		}
	}
	return stats, rows.Err()
}

func requireStockProfileAIQueueLease(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStockProfileAIQueueLeaseStale
	}
	return nil
}

const stockProfileAIQueueColumns = `
	symbol, market, status, priority, trigger_reason, COALESCE(requested_by, ''),
	desired_input_version, COALESCE(claimed_input_version, ''), COALESCE(completed_input_version, ''),
	payload_json, COALESCE(current_agent_run_id, ''), attempt_count, available_at,
	COALESCE(lease_owner, ''), COALESCE(lease_token, ''), lease_expires_at,
	completed_at, COALESCE(last_error, ''), created_at, updated_at`

const stockProfileAIQueueSelect = `SELECT ` + stockProfileAIQueueColumns + ` FROM stockv2_stock_profile_ai_queue`

func scanStockProfileAIQueueItem(row rowScanner) (StockProfileAIQueueItem, error) {
	var item StockProfileAIQueueItem
	var leaseExpiresAt, completedAt sql.NullTime
	err := row.Scan(
		&item.Symbol, &item.Market, &item.Status, &item.Priority, &item.TriggerReason, &item.RequestedBy,
		&item.DesiredInputVersion, &item.ClaimedInputVersion, &item.CompletedInputVersion,
		&item.PayloadJSON, &item.CurrentAgentRunID, &item.AttemptCount, &item.AvailableAt,
		&item.LeaseOwner, &item.LeaseToken, &leaseExpiresAt,
		&completedAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return StockProfileAIQueueItem{}, err
	}
	if leaseExpiresAt.Valid {
		item.LeaseExpiresAt = leaseExpiresAt.Time
	}
	if completedAt.Valid {
		item.CompletedAt = completedAt.Time
	}
	return item, nil
}
