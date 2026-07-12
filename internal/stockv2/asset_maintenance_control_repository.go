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

	"phantom-lancer/internal/safelog"
)

const (
	assetUniverseSnapshotStatusFull             = "full"
	assetUniverseSnapshotStatusCachedUnverified = "cached_unverified"
	assetUniverseSnapshotSourceLive             = "public_universe"
)

func assetMaintenanceScope(req UniverseUpdateRequest) string {
	if len(req.Symbols) > 0 {
		return AssetMaintenanceScopeExplicit
	}
	if req.MaxSymbols > 0 {
		return AssetMaintenanceScopeCappedRotation
	}
	return AssetMaintenanceScopeFullUniverse
}

func canonicalUniverseSymbols(symbols []string) []string {
	out := compactStringList(symbols, 0)
	sort.Strings(out)
	return out
}

func assetUniverseHash(symbols []string) string {
	sum := sha256.Sum256([]byte(strings.Join(canonicalUniverseSymbols(symbols), "\n")))
	return hex.EncodeToString(sum[:])
}

func assetMaintenanceItemID(jobID, symbol string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(jobID) + "\x00" + strings.TrimSpace(symbol)))
	return "asset-item-" + hex.EncodeToString(sum[:16])
}

// EnsureAssetUniverseSnapshot persists a complete, versioned symbol set. A
// partial discovery result must never call this method or replace the active
// pointer.
func (s *Store) EnsureAssetUniverseSnapshot(
	ctx context.Context,
	symbols []string,
	source string,
) (AssetUniverseSnapshot, error) {
	return s.ensureAssetUniverseSnapshot(ctx, symbols, source, assetUniverseSnapshotStatusFull, true)
}

func (s *Store) EnsureUnverifiedAssetUniverseSnapshot(
	ctx context.Context,
	symbols []string,
	source string,
) (AssetUniverseSnapshot, error) {
	return s.ensureAssetUniverseSnapshot(ctx, symbols, source, assetUniverseSnapshotStatusCachedUnverified, false)
}

func (s *Store) ensureAssetUniverseSnapshot(
	ctx context.Context,
	symbols []string,
	source string,
	status string,
	activate bool,
) (AssetUniverseSnapshot, error) {
	symbols = canonicalUniverseSymbols(symbols)
	if len(symbols) == 0 {
		return AssetUniverseSnapshot{}, errors.New("asset universe snapshot is empty")
	}
	if strings.TrimSpace(source) == "" {
		source = assetUniverseSnapshotSourceLive
	}
	hash := assetUniverseHash(symbols)
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssetUniverseSnapshot{}, wrapError(err, "begin asset universe snapshot")
	}
	defer tx.Rollback()

	snapshot := AssetUniverseSnapshot{}
	err = tx.QueryRowContext(ctx, `
		SELECT id, universe_hash, status, source, target_count, created_at
		FROM stockv2_universe_snapshots
		WHERE universe_hash = ? AND status = ?
		ORDER BY created_at DESC LIMIT 1
	`, hash, status).Scan(
		&snapshot.ID, &snapshot.UniverseHash, &snapshot.Status,
		&snapshot.Source, &snapshot.TargetCount, &snapshot.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		snapshot = AssetUniverseSnapshot{
			ID:           generateID(),
			UniverseHash: hash,
			Status:       status,
			Source:       source,
			TargetCount:  len(symbols),
			CreatedAt:    now,
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stockv2_universe_snapshots
				(id, universe_hash, status, source, target_count, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, snapshot.ID, snapshot.UniverseHash, snapshot.Status, snapshot.Source, snapshot.TargetCount, snapshot.CreatedAt); err != nil {
			return AssetUniverseSnapshot{}, wrapError(err, "insert asset universe snapshot")
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO stockv2_universe_snapshot_members (snapshot_id, symbol, position)
			VALUES (?, ?, ?)
		`)
		if err != nil {
			return AssetUniverseSnapshot{}, wrapError(err, "prepare asset universe members")
		}
		defer stmt.Close()
		for position, symbol := range symbols {
			if _, err := stmt.ExecContext(ctx, snapshot.ID, symbol, position); err != nil {
				return AssetUniverseSnapshot{}, wrapError(err, "insert asset universe member")
			}
		}
	} else if err != nil {
		return AssetUniverseSnapshot{}, wrapError(err, "find asset universe snapshot")
	}
	if snapshot.TargetCount != len(symbols) {
		return AssetUniverseSnapshot{}, fmt.Errorf("asset universe hash count mismatch: stored=%d current=%d", snapshot.TargetCount, len(symbols))
	}
	if activate {
		if _, err := tx.ExecContext(ctx, `
				INSERT INTO stockv2_universe_state (id, active_snapshot_id, updated_at)
				VALUES ('active', ?, ?)
				ON CONFLICT(id) DO UPDATE SET active_snapshot_id = excluded.active_snapshot_id, updated_at = excluded.updated_at
			`, snapshot.ID, now); err != nil {
			return AssetUniverseSnapshot{}, wrapError(err, "activate asset universe snapshot")
		}
	}
	if err := tx.Commit(); err != nil {
		return AssetUniverseSnapshot{}, wrapError(err, "commit asset universe snapshot")
	}
	return snapshot, nil
}

// PrepareAssetMaintenanceJob freezes all targets before workers start. Cursor
// movement and network work happen only after this transaction succeeds, so a
// crash cannot silently skip an unpersisted tail of the universe.
func (s *Store) PrepareAssetMaintenanceJob(
	ctx context.Context,
	job StockV2UpdateJob,
	snapshot AssetUniverseSnapshot,
	symbols []string,
) error {
	symbols = canonicalUniverseSymbols(symbols)
	if len(symbols) == 0 {
		return errors.New("asset maintenance job has no targets")
	}
	if job.Scope == AssetMaintenanceScopeFullUniverse {
		var persistedHash, persistedStatus string
		var persistedCount int
		err := s.db.QueryRowContext(ctx, `
			SELECT universe_hash, target_count, status
			FROM stockv2_universe_snapshots
			WHERE id = ?
		`, snapshot.ID).Scan(&persistedHash, &persistedCount, &persistedStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("full-universe maintenance snapshot was not persisted")
		}
		if err != nil {
			return wrapError(err, "load full-universe maintenance snapshot")
		}
		if persistedCount != len(symbols) || persistedHash != assetUniverseHash(symbols) {
			return errors.New("full-universe maintenance targets do not match the persisted snapshot")
		}
		expectedStatus := assetUniverseSnapshotStatusCachedUnverified
		if job.UniverseVerified {
			expectedStatus = assetUniverseSnapshotStatusFull
		}
		if persistedStatus != expectedStatus {
			return errors.New("full-universe maintenance verification does not match its snapshot status")
		}
		snapshot.UniverseHash = persistedHash
		snapshot.TargetCount = persistedCount
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin prepared asset maintenance job")
	}
	defer tx.Rollback()
	now := time.Now()
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_update_jobs
			SET scope = ?, slot_start = ?, universe_snapshot_id = ?, universe_hash = ?, universe_verified = ?,
				expected_latest_trade_date = ?, message_cutoff_at = ?, error_message = ?,
			coverage_status = 'pending', freshness_status = 'pending', total_count = ?
		WHERE id = ?
		`, job.Scope, nullableTime(job.SlotStart), nullableString(snapshot.ID), nullableString(snapshot.UniverseHash), job.UniverseVerified,
		nullableString(job.ExpectedLatestDate), nullableTime(job.MessageCutoffAt), nullableString(job.ErrorMessage), len(symbols), job.ID)
	if err != nil {
		return wrapError(err, "prepare asset maintenance job metadata")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return ErrUpdateJobNotFound
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO stockv2_asset_maintenance_items (
			id, job_id, symbol, status, priority_reason, attempt_count,
			expected_latest_trade_date, started_at, created_at, updated_at
		) VALUES (?, ?, ?, 'pending', ?, 0, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`)
	if err != nil {
		return wrapError(err, "prepare asset maintenance targets")
	}
	defer stmt.Close()
	priorityReason := "full_universe"
	if job.Scope == AssetMaintenanceScopeExplicit {
		priorityReason = "explicit"
	} else if job.Scope == AssetMaintenanceScopeCappedRotation {
		priorityReason = "cursor"
	}
	for _, symbol := range symbols {
		if _, err := stmt.ExecContext(ctx, assetMaintenanceItemID(job.ID, symbol), job.ID, symbol,
			priorityReason, nullableString(job.ExpectedLatestDate), now, now, now); err != nil {
			return wrapError(err, "insert asset maintenance target")
		}
	}
	if job.Scope == AssetMaintenanceScopeFullUniverse && !job.SlotStart.IsZero() {
		slotEnd := job.SlotStart.Add(7 * time.Hour)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stockv2_maintenance_slots (
				slot_start, slot_end, expected_latest_trade_date, universe_snapshot_id,
				universe_hash, target_count, job_id, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)
			ON CONFLICT(slot_start) DO UPDATE SET
				slot_end = excluded.slot_end,
				expected_latest_trade_date = excluded.expected_latest_trade_date,
				universe_snapshot_id = excluded.universe_snapshot_id,
				universe_hash = excluded.universe_hash,
				target_count = excluded.target_count,
				job_id = excluded.job_id,
				status = 'pending', covered_at = NULL, updated_at = excluded.updated_at
		`, job.SlotStart, slotEnd, nullableString(job.ExpectedLatestDate), snapshot.ID,
			snapshot.UniverseHash, len(symbols), job.ID, now, now); err != nil {
			return wrapError(err, "bind asset maintenance slot")
		}
	}
	return wrapError(tx.Commit(), "commit prepared asset maintenance job")
}

func (s *Store) MarkAssetMaintenanceItemsRetryWait(
	ctx context.Context,
	jobID string,
	symbols []string,
	reason string,
) error {
	symbols = compactStringList(symbols, 500)
	if len(symbols) == 0 {
		return nil
	}
	args := make([]any, 0, len(symbols)+6)
	now := time.Now()
	args = append(args, now.Add(15*time.Minute), now, safelog.Text(reason, 800), now, now, jobID)
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_asset_maintenance_items
		SET status = 'retry_wait', attempt_count = attempt_count + 1,
			next_retry_at = ?, checked_at = ?, error_message = ?, finished_at = ?, updated_at = ?
		WHERE job_id = ? AND symbol IN (`+sqlPlaceholders(len(symbols))+`)
	`, args...)
	return wrapError(err, "mark asset maintenance targets retry wait")
}

type assetMaintenanceJobCounts struct {
	Target  int
	Checked int
	Fresh   int
	Retry   int
	Failed  int
}

func (s *Store) GetAssetMaintenanceAssetsProgressByJobIDs(
	ctx context.Context,
	jobIDs []string,
) (map[string]AssetMaintenanceAssetsProgress, error) {
	jobIDs = compactStringList(jobIDs, 100)
	out := make(map[string]AssetMaintenanceAssetsProgress, len(jobIDs))
	if len(jobIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		args = append(args, jobID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT job_id,
		       SUM(CASE WHEN status = 'completed'
		                     AND daily_bar_status IN ('fetched','skipped')
		                     AND daily_flow_status IN ('ready','not_required') THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'completed'
		                     AND base_profile_status IN ('updated','unchanged')
		                     AND announcement_status IN ('checked','skipped') THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'completed'
		                     AND daily_bar_status IN ('fetched','skipped')
		                     AND daily_flow_status IN ('ready','not_required')
		                     AND base_profile_status IN ('updated','unchanged')
		                     AND announcement_status IN ('checked','skipped') THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'retry_wait' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END)
		FROM stockv2_asset_maintenance_items
		WHERE job_id IN (`+sqlPlaceholders(len(jobIDs))+`)
		GROUP BY job_id
	`, args...)
	if err != nil {
		return nil, wrapError(err, "aggregate asset maintenance freshness")
	}
	defer rows.Close()
	for rows.Next() {
		var jobID string
		var progress AssetMaintenanceAssetsProgress
		if err := rows.Scan(&jobID, &progress.MarketFresh, &progress.MessageFresh,
			&progress.Fresh, &progress.Retrying, &progress.Failed); err != nil {
			return nil, wrapError(err, "scan asset maintenance freshness")
		}
		out[jobID] = progress
	}
	return out, wrapError(rows.Err(), "iterate asset maintenance freshness")
}

func (s *Store) finalizeAssetMaintenanceJob(
	ctx context.Context,
	jobID string,
	stats AssetMaintenanceStats,
	failedItems []UpdateFailure,
	writeBytesEnd, peakRSS uint64,
) (assetMaintenanceJobCounts, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return assetMaintenanceJobCounts{}, wrapError(err, "begin finalize asset maintenance job")
	}
	defer tx.Rollback()
	var universeVerified bool
	var scope, parentStatus, parentError string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(scope, ''), COALESCE(universe_verified, 0), status, COALESCE(error_message, '')
		FROM stockv2_update_jobs WHERE id = ?
	`, jobID).Scan(&scope, &universeVerified, &parentStatus, &parentError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return assetMaintenanceJobCounts{}, ErrUpdateJobNotFound
		}
		return assetMaintenanceJobCounts{}, wrapError(err, "read asset maintenance universe verification")
	}
	coverageVerifiable := scope != AssetMaintenanceScopeFullUniverse || universeVerified
	var counts assetMaintenanceJobCounts
	if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*),
			       COALESCE(SUM(CASE WHEN status IN ('completed','retry_wait','failed')
			                              AND checked_at IS NOT NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'completed'
		                         AND daily_bar_status IN ('fetched','skipped')
		                         AND daily_flow_status IN ('ready','not_required')
		                         AND base_profile_status IN ('updated','unchanged')
		                         AND announcement_status IN ('checked','skipped') THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'retry_wait' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM stockv2_asset_maintenance_items WHERE job_id = ?
	`, jobID).Scan(&counts.Target, &counts.Checked, &counts.Fresh, &counts.Retry, &counts.Failed); err != nil {
		return counts, wrapError(err, "count finalized asset maintenance items")
	}
	coverage := AssetMaintenanceCoverageIncomplete
	runStatus := "failed"
	if coverageVerifiable && counts.Target > 0 && counts.Checked == counts.Target {
		coverage = AssetMaintenanceCoverageCovered
		runStatus = "completed"
	} else if parentStatus == "paused" {
		runStatus = "paused"
	}
	freshness := AssetMaintenanceFreshnessStale
	if !coverageVerifiable {
		freshness = AssetMaintenanceFreshnessStale
	} else if counts.Failed > 0 {
		freshness = AssetMaintenanceFreshnessFailed
	} else if counts.Retry > 0 {
		freshness = AssetMaintenanceFreshnessRetrying
	} else if strings.Contains(parentError, "trading_calendar_unavailable:") {
		freshness = AssetMaintenanceFreshnessRetrying
	} else if counts.Target > 0 && counts.Fresh == counts.Target {
		freshness = AssetMaintenanceFreshnessReady
	}
	statsJSON, _ := jsonMarshal(stats)
	failedJSON, _ := jsonMarshal(failedItems)
	now := time.Now()
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_update_jobs
		SET status = ?, coverage_status = ?, freshness_status = ?,
			checked_count = ?, processed_count = ?, success_count = ?, fresh_count = ?,
			stale_count = ?, retry_count = ?, failed_count = ?,
			asset_stats_json = ?, failed_items = ?, end_at = ?,
			write_bytes_end = ?, peak_rss_bytes = ?
		WHERE id = ?
	`, runStatus, coverage, freshness, counts.Checked, counts.Checked, counts.Fresh,
		counts.Fresh, counts.Target-counts.Fresh, counts.Retry, counts.Failed,
		statsJSON, failedJSON, now, writeBytesEnd, peakRSS, jobID)
	if err != nil {
		return counts, wrapError(err, "finalize asset maintenance job")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return counts, wrapError(err, "check finalized asset maintenance job")
		}
		return counts, ErrUpdateJobNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_maintenance_slots
		SET status = ?, covered_at = CASE WHEN ? = 'covered' THEN ? ELSE NULL END, updated_at = ?
		WHERE job_id = ?
	`, coverage, coverage, now, now, jobID); err != nil {
		return counts, wrapError(err, "finalize asset maintenance slot")
	}
	if err := tx.Commit(); err != nil {
		return counts, wrapError(err, "commit finalized asset maintenance job")
	}
	return counts, nil
}

func (s *Store) GetAssetMaintenanceSlot(ctx context.Context, slotStart time.Time) (AssetMaintenanceSlot, bool, error) {
	var item AssetMaintenanceSlot
	var coveredAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT slot_start, slot_end, COALESCE(expected_latest_trade_date,''),
		       universe_snapshot_id, universe_hash, target_count, COALESCE(job_id,''),
		       status, covered_at, created_at, updated_at
		FROM stockv2_maintenance_slots WHERE slot_start = ?
	`, slotStart).Scan(
		&item.SlotStart, &item.SlotEnd, &item.ExpectedLatestTradeDate,
		&item.UniverseSnapshotID, &item.UniverseHash, &item.TargetCount, &item.JobID,
		&item.Status, &coveredAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AssetMaintenanceSlot{}, false, nil
	}
	if err != nil {
		return AssetMaintenanceSlot{}, false, wrapError(err, "get asset maintenance slot")
	}
	if coveredAt.Valid {
		item.CoveredAt = coveredAt.Time
	}
	return item, true, nil
}

func (s *Store) PruneAssetMaintenanceHistory(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM stockv2_asset_maintenance_items
			WHERE created_at < ?
			  AND NOT EXISTS (
				SELECT 1 FROM stockv2_update_jobs
				WHERE id = stockv2_asset_maintenance_items.job_id AND status = 'running'
			  )
		`, now.AddDate(0, 0, -30)); err != nil {
			return wrapError(err, "prune asset maintenance items")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM stockv2_update_jobs WHERE created_at < ? AND status <> 'running'
		`, now.AddDate(0, 0, -180)); err != nil {
			return wrapError(err, "prune asset maintenance jobs")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM stockv2_maintenance_slots WHERE slot_start < ?
		`, now.AddDate(0, 0, -180)); err != nil {
			return wrapError(err, "prune asset maintenance slots")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM stockv2_universe_snapshots
			WHERE id NOT IN (SELECT active_snapshot_id FROM stockv2_universe_state)
			  AND id NOT IN (
				SELECT universe_snapshot_id FROM stockv2_maintenance_slots
				WHERE universe_snapshot_id IS NOT NULL AND universe_snapshot_id <> ''
			  )
			  AND id NOT IN (
				SELECT universe_snapshot_id FROM stockv2_update_jobs
				WHERE universe_snapshot_id IS NOT NULL AND universe_snapshot_id <> ''
			  )
			  AND id NOT IN (
				SELECT id FROM stockv2_universe_snapshots ORDER BY created_at DESC LIMIT 4
			  )
		`); err != nil {
			return wrapError(err, "prune asset universe snapshots")
		}
		return nil
	})
}

func jsonMarshal(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}
