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

const stockProfileAIQueueResultMaxBytes = 64 << 10

var (
	ErrStockProfileAIQueueEmpty      = errors.New("stock profile ai queue empty")
	ErrStockProfileAIQueueLeaseStale = errors.New("stock profile ai queue lease stale")
)

func (s *Store) EnqueueStockProfileAI(ctx context.Context, item StockProfileAIQueueItem) (StockProfileAIQueueItem, error) {
	return s.enqueueStockProfileAI(ctx, item, false)
}

// ReviveStockProfileAI is the explicit same-version retry path used after the
// maintenance backoff or an owner action. Ordinary outbox reconciliation must
// never reset a terminal attempt counter.
func (s *Store) ReviveStockProfileAI(ctx context.Context, item StockProfileAIQueueItem) (StockProfileAIQueueItem, error) {
	return s.enqueueStockProfileAI(ctx, item, true)
}

func (s *Store) enqueueStockProfileAI(
	ctx context.Context,
	item StockProfileAIQueueItem,
	reviveFailed bool,
) (StockProfileAIQueueItem, error) {
	item.Symbol = strings.TrimSpace(item.Symbol)
	item.DesiredInputVersion = strings.TrimSpace(item.DesiredInputVersion)
	if item.Symbol == "" || item.DesiredInputVersion == "" {
		return StockProfileAIQueueItem{}, errors.New("stock profile ai queue requires symbol and input version")
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
	reviveFailedInt := 0
	if reviveFailed {
		reviveFailedInt = 1
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
			status = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN 'running'
				WHEN stockv2_stock_profile_ai_queue.status = 'failed'
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND ? = 0
				THEN 'failed'
				WHEN stockv2_stock_profile_ai_queue.status IN ('apply_pending','applying')
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.status
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND stockv2_stock_profile_ai_queue.status IN ('ready', 'retry_wait')
				THEN stockv2_stock_profile_ai_queue.status
				ELSE excluded.status
			END,
			available_at = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'failed'
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND ? = 0
				THEN stockv2_stock_profile_ai_queue.available_at
				WHEN stockv2_stock_profile_ai_queue.status = 'running'
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.available_at
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN excluded.available_at
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND stockv2_stock_profile_ai_queue.status IN ('ready', 'retry_wait')
				THEN stockv2_stock_profile_ai_queue.available_at
				ELSE excluded.available_at
			END,
			attempt_count = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN stockv2_stock_profile_ai_queue.attempt_count
				WHEN stockv2_stock_profile_ai_queue.status = 'failed'
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND ? = 0
				THEN stockv2_stock_profile_ai_queue.attempt_count
				WHEN stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND stockv2_stock_profile_ai_queue.status IN ('ready', 'retry_wait')
				THEN stockv2_stock_profile_ai_queue.attempt_count
				ELSE 0
			END,
			current_agent_run_id = CASE
				WHEN stockv2_stock_profile_ai_queue.status = 'running' THEN stockv2_stock_profile_ai_queue.current_agent_run_id
				WHEN stockv2_stock_profile_ai_queue.status IN ('apply_pending','applying')
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.current_agent_run_id ELSE NULL END,
			result_json = CASE WHEN stockv2_stock_profile_ai_queue.status IN ('apply_pending','applying')
				AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.result_json ELSE NULL END,
			result_hash = CASE WHEN stockv2_stock_profile_ai_queue.status IN ('apply_pending','applying')
				AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.result_hash ELSE NULL END,
			result_model = CASE WHEN stockv2_stock_profile_ai_queue.status IN ('apply_pending','applying')
				AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.result_model ELSE NULL END,
			result_confidence = CASE WHEN stockv2_stock_profile_ai_queue.status IN ('apply_pending','applying')
				AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
				THEN stockv2_stock_profile_ai_queue.result_confidence ELSE 0 END,
			last_error = CASE
				WHEN stockv2_stock_profile_ai_queue.status IN ('running','apply_pending','applying')
				THEN stockv2_stock_profile_ai_queue.last_error
				WHEN stockv2_stock_profile_ai_queue.status = 'failed'
					AND stockv2_stock_profile_ai_queue.desired_input_version = excluded.desired_input_version
					AND ? = 0
				THEN stockv2_stock_profile_ai_queue.last_error
				ELSE NULL
			END,
			updated_at = excluded.updated_at`
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profile_ai_queue (
			symbol, market, status, priority, trigger_reason, requested_by,
			desired_input_version, claimed_input_version,
			current_agent_run_id, attempt_count, available_at,
			lease_owner, lease_token, lease_expires_at,
			completed_input_version, completed_at, result_json, result_hash,
			result_model, result_confidence, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, NULL, 0, ?, NULL, NULL, NULL,
		          NULL, NULL, NULL, NULL, NULL, 0, NULL, ?, ?)
	`+conflictClause, item.Symbol, item.Market, item.Status, item.Priority, item.TriggerReason, item.RequestedBy,
		item.DesiredInputVersion, item.AvailableAt, item.CreatedAt, item.UpdatedAt,
		reviveFailedInt, reviveFailedInt, reviveFailedInt, reviveFailedInt)
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
		  AND ai_desired_input_version = ?
	`, runID, now, lease.Symbol, lease.ClaimedInputVersion); err != nil {
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
				WHEN 'apply_pending' THEN 'running'
				WHEN 'applying' THEN 'running'
				WHEN 'completed' THEN 'completed'
				WHEN 'failed' THEN 'partial'
				ELSE 'queued'
			END,
			agent_run_id = (SELECT current_agent_run_id FROM stockv2_stock_profile_ai_queue WHERE symbol = ?),
			ai_profile_status = CASE (SELECT status FROM stockv2_stock_profile_ai_queue WHERE symbol = ?)
				WHEN 'running' THEN 'running'
				WHEN 'apply_pending' THEN 'running'
				WHEN 'applying' THEN 'running'
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
	case StockProfileAIQueueStatusRunning, StockProfileAIQueueStatusApplyPending, StockProfileAIQueueStatusApplying:
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

func (s *Store) StageStockProfileAIResult(
	ctx context.Context,
	symbol, runID, resultJSON, resultHash, model string,
	confidence float64,
) error {
	if len(resultJSON) == 0 || len(resultJSON) > stockProfileAIQueueResultMaxBytes {
		return fmt.Errorf("stock profile AI result must be 1-%d bytes", stockProfileAIQueueResultMaxBytes)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin staging stock profile AI result")
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = 'apply_pending', result_json = ?, result_hash = ?,
			result_model = ?, result_confidence = ?, available_at = ?,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = NULL, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND current_agent_run_id = ?
		  AND desired_input_version = claimed_input_version
	`, resultJSON, strings.TrimSpace(resultHash), strings.TrimSpace(model), confidence,
		time.Now(), time.Now(), strings.TrimSpace(symbol), strings.TrimSpace(runID))
	if err != nil {
		return wrapError(err, "stage stock profile AI result")
	}
	if err := requireStockProfileAIQueueLease(result); err != nil {
		return err
	}
	if err := updateBoundStockProfileAIRecordsWithTx(
		ctx, tx, strings.TrimSpace(symbol), strings.TrimSpace(runID),
		StockProfileAIQueueStatusApplyPending, "",
	); err != nil {
		return err
	}
	// ponytail: staging is the durable completion point for a profile AgentRun.
	// Mark it completed in the same SQLite transaction so a process crash cannot
	// leave an applied/recoverable result attached to a permanently running run.
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_agent_runs
		SET status = 'completed', finished_at = COALESCE(finished_at, ?), updated_at = ?
		WHERE id = ? AND status IN ('ready', 'running', 'completed')
	`, time.Now(), time.Now(), strings.TrimSpace(runID)); err != nil {
		return wrapError(err, "complete staged stock profile AI run")
	}
	return wrapError(tx.Commit(), "commit staged stock profile AI result")
}

func (s *Store) ClaimStockProfileAIApply(
	ctx context.Context,
	workerID string,
	now time.Time,
	leaseTTL time.Duration,
) (StockProfileAIQueueLease, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if leaseTTL <= 0 {
		leaseTTL = 2 * time.Minute
	}
	leaseToken := generateID()
	item, err := scanStockProfileAIQueueItem(s.db.QueryRowContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = 'applying', lease_owner = ?, lease_token = ?, lease_expires_at = ?, updated_at = ?
		WHERE symbol = (
			SELECT symbol FROM stockv2_stock_profile_ai_queue
			WHERE status = 'apply_pending' AND available_at <= ?
			ORDER BY priority DESC, available_at, updated_at LIMIT 1
		)
		RETURNING `+stockProfileAIQueueColumns,
		workerID, leaseToken, now.Add(leaseTTL), now, now))
	if errors.Is(err, sql.ErrNoRows) {
		return StockProfileAIQueueLease{}, ErrStockProfileAIQueueEmpty
	}
	if err != nil {
		return StockProfileAIQueueLease{}, wrapError(err, "claim stock profile AI apply")
	}
	return StockProfileAIQueueLease{StockProfileAIQueueItem: item}, nil
}

func (s *Store) CompleteStockProfileAIApply(ctx context.Context, lease StockProfileAIQueueLease) (bool, error) {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, wrapError(err, "begin complete stock profile AI apply")
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = CASE WHEN desired_input_version = claimed_input_version THEN 'completed' ELSE 'ready' END,
			completed_input_version = CASE WHEN desired_input_version = claimed_input_version THEN claimed_input_version ELSE completed_input_version END,
			completed_at = CASE WHEN desired_input_version = claimed_input_version THEN ? ELSE completed_at END,
			attempt_count = CASE WHEN desired_input_version = claimed_input_version THEN attempt_count ELSE 0 END,
			current_agent_run_id = NULL,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			result_json = NULL, result_hash = NULL, result_model = NULL, result_confidence = 0,
			last_error = NULL, updated_at = ?
		WHERE symbol = ? AND status = 'applying' AND lease_token = ?
		RETURNING status
	`, now, now, lease.Symbol, lease.LeaseToken).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrStockProfileAIQueueLeaseStale
	}
	if err != nil {
		return false, wrapError(err, "complete stock profile AI apply")
	}
	if err := updateBoundStockProfileAIRecordsWithTx(ctx, tx, lease.Symbol, lease.CurrentAgentRunID, status, ""); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, wrapError(err, "commit completed stock profile AI apply")
	}
	return status == StockProfileAIQueueStatusReady, nil
}

func (s *Store) RetryStockProfileAIApply(
	ctx context.Context,
	lease StockProfileAIQueueLease,
	availableAt time.Time,
	message string,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = CASE WHEN desired_input_version = claimed_input_version THEN 'apply_pending' ELSE 'ready' END,
			available_at = ?, lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = ?, updated_at = ?
		WHERE symbol = ? AND status = 'applying' AND lease_token = ?
	`, availableAt, safelog.Text(message, 500), time.Now(), lease.Symbol, lease.LeaseToken)
	if err != nil {
		return wrapError(err, "retry stock profile AI apply")
	}
	return requireStockProfileAIQueueLease(result)
}

func (s *Store) SupersedeStockProfileAIRun(ctx context.Context, lease StockProfileAIQueueLease) error {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin supersede stock profile AI run")
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_stock_profile_ai_queue
		SET status = 'ready', available_at = ?, attempt_count = 0,
			current_agent_run_id = NULL,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = NULL, updated_at = ?
		WHERE symbol = ? AND status = 'running' AND lease_token = ?
	`, now, now, lease.Symbol, lease.LeaseToken)
	if err != nil {
		return wrapError(err, "supersede stock profile AI run")
	}
	if err := requireStockProfileAIQueueLease(result); err != nil {
		return err
	}
	if err := updateBoundStockProfileAIRecordsWithTx(ctx, tx, lease.Symbol, lease.CurrentAgentRunID, StockProfileAIQueueStatusReady, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return wrapError(err, "commit superseded stock profile AI run")
	}
	return nil
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

	where := "status IN ('running','applying') AND lease_expires_at < ?"
	args := []any{now}
	if all {
		where = "status IN ('running','applying')"
		args = nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT symbol, COALESCE(current_agent_run_id, ''), status
		FROM stockv2_stock_profile_ai_queue
		WHERE `+where, args...)
	if err != nil {
		return nil, wrapError(err, "list expired stock profile ai leases")
	}
	type recoveredLease struct {
		symbol string
		runID  string
		status string
	}
	var recovered []recoveredLease
	var runIDs []string
	for rows.Next() {
		var item recoveredLease
		if err := rows.Scan(&item.symbol, &item.runID, &item.status); err != nil {
			rows.Close()
			return nil, err
		}
		recovered = append(recovered, item)
		if item.status == StockProfileAIQueueStatusRunning && strings.TrimSpace(item.runID) != "" {
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
		SET status = CASE WHEN status = 'applying' THEN 'apply_pending' ELSE 'ready' END,
			available_at = ?,
			current_agent_run_id = CASE WHEN status = 'applying' THEN current_agent_run_id ELSE NULL END,
			lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			last_error = 'worker lease expired', updated_at = ?
		WHERE `+where, updateArgs...); err != nil {
		return nil, wrapError(err, "recover expired stock profile ai leases")
	}
	for _, item := range recovered {
		if item.status == StockProfileAIQueueStatusApplying {
			continue
		}
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
	current = desired != "" && desired == claimed && currentRun == strings.TrimSpace(runID)
	if !current {
		return true, false, nil
	}
	targetCurrent, err := s.StockProfileAITargetCurrent(ctx, symbol, claimed)
	if err != nil {
		return true, false, err
	}
	return true, targetCurrent, nil
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
		case StockProfileAIQueueStatusApplyPending, StockProfileAIQueueStatusApplying:
			stats.Applying += count
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
	COALESCE(current_agent_run_id, ''), attempt_count, available_at,
	COALESCE(lease_owner, ''), COALESCE(lease_token, ''), lease_expires_at,
	completed_at, COALESCE(result_json, ''), COALESCE(result_hash, ''),
	COALESCE(result_model, ''), COALESCE(result_confidence, 0),
	COALESCE(last_error, ''), created_at, updated_at`

const stockProfileAIQueueSelect = `SELECT ` + stockProfileAIQueueColumns + ` FROM stockv2_stock_profile_ai_queue`

func scanStockProfileAIQueueItem(row rowScanner) (StockProfileAIQueueItem, error) {
	var item StockProfileAIQueueItem
	var leaseExpiresAt, completedAt sql.NullTime
	err := row.Scan(
		&item.Symbol, &item.Market, &item.Status, &item.Priority, &item.TriggerReason, &item.RequestedBy,
		&item.DesiredInputVersion, &item.ClaimedInputVersion, &item.CompletedInputVersion,
		&item.CurrentAgentRunID, &item.AttemptCount, &item.AvailableAt,
		&item.LeaseOwner, &item.LeaseToken, &leaseExpiresAt,
		&completedAt, &item.ResultJSON, &item.ResultHash, &item.ResultModel,
		&item.ResultConfidence, &item.LastError, &item.CreatedAt, &item.UpdatedAt,
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
