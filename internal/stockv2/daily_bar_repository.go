package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UpsertDailyBars 批量落盘日 K（按 symbol+trade_date+adjusted+source 去重 upsert）。
// 明细数据写入本地 DuckDB market store；SQLite Store 只保留任务/设置等操作状态。
func (s *Store) UpsertDailyBars(ctx context.Context, bars []StockV2DailyBar) error {
	return s.marketDB.UpsertDailyBars(ctx, bars)
}

// GetDailyBars 查询日 K，支持 limit 与起止日期范围（可组合，均可空）。
// 结果按 trade_date 升序返回，便于前端直接绘制时间序列。
func (s *Store) GetDailyBars(ctx context.Context, symbol, adjusted, startDate, endDate string, limit int) ([]StockV2DailyBar, error) {
	return s.marketDB.GetDailyBars(ctx, symbol, adjusted, startDate, endDate, limit)
}

// GetDailyBarsStats 返回本地日 K 统计，供质量评估使用。
// rowCount=0 表示本地无数据。
func (s *Store) GetDailyBarsStats(ctx context.Context, symbol, adjusted string) (rowCount int, earliest, latest, source, lastError string, err error) {
	return s.marketDB.GetDailyBarsStats(ctx, symbol, adjusted)
}

type dailyBarsStats struct {
	Symbol    string
	RowCount  int
	Earliest  string
	Latest    string
	Source    string
	LastError string
}

func (s *Store) GetDailyBarsStatsBatch(ctx context.Context, symbols []string, adjusted string) (map[string]dailyBarsStats, error) {
	symbols = compactStringList(symbols, 100)
	if len(symbols) == 0 {
		return map[string]dailyBarsStats{}, nil
	}
	return s.marketDB.GetDailyBarsStatsBatch(ctx, symbols, adjusted)
}

// ListHoldingSymbols 返回所有持仓的去重 symbol（日 K 热集合用）。
func (s *Store) ListHoldingSymbols(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT symbol FROM stockv2_holdings WHERE symbol != '' ORDER BY symbol`)
	if err != nil {
		return nil, wrapError(err, "list holding symbols")
	}
	return scanStrings(rows, "scan holding symbol", "iterate holding symbols")
}

// CreateDailyBarJob 创建日 K 任务记录
func (s *Store) CreateDailyBarJob(ctx context.Context, job StockV2DailyBarJob) error {
	const q = `
		INSERT INTO stockv2_daily_bar_jobs (
			id, job_type, mode, symbol, status, total_count, processed_count,
			success_count, failed_count, start_at, end_at, error_message,
			range, adjusted, trigger_type, trigger_source, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, q,
		job.ID, job.JobType, job.Mode, job.Symbol, job.Status,
		job.TotalCount, job.ProcessedCount, job.SuccessCount, job.FailedCount,
		job.StartAt, job.EndAt, job.ErrorMessage,
		job.RangeCode, job.Adjusted, job.TriggerType, job.TriggerSource,
		job.CreatedAt,
	)
	return wrapError(err, "create daily bar job")
}

// UpdateDailyBarJob 增量更新日 K 任务（只更新非零 / 非空字段）。
func (s *Store) UpdateDailyBarJob(ctx context.Context, job StockV2DailyBarJob) error {
	var sets []string
	var args []any

	if job.Status != "" {
		sets = append(sets, "status = ?")
		args = append(args, job.Status)
	}
	if job.TotalCount > 0 {
		sets = append(sets, "total_count = ?")
		args = append(args, job.TotalCount)
	}
	// processed/success/failed 允许随任务推进，与 total 一起更新
	if job.TotalCount > 0 || job.ProcessedCount > 0 {
		sets = append(sets, "processed_count = ?")
		args = append(args, job.ProcessedCount)
	}
	if job.TotalCount > 0 || job.SuccessCount > 0 {
		sets = append(sets, "success_count = ?")
		args = append(args, job.SuccessCount)
	}
	if job.TotalCount > 0 || job.FailedCount > 0 {
		sets = append(sets, "failed_count = ?")
		args = append(args, job.FailedCount)
	}
	if job.FailedItems != nil {
		fj, _ := json.Marshal(job.FailedItems)
		sets = append(sets, "failed_items = ?")
		args = append(args, string(fj))
	}
	if !job.EndAt.IsZero() {
		sets = append(sets, "end_at = ?")
		args = append(args, job.EndAt)
	}
	if job.ErrorMessage != "" {
		sets = append(sets, "error_message = ?")
		args = append(args, job.ErrorMessage)
	}

	if len(sets) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE stockv2_daily_bar_jobs SET %s WHERE id = ?", strings.Join(sets, ", "))
	args = append(args, job.ID)
	_, err := s.db.ExecContext(ctx, query, args...)
	return wrapError(err, "update daily bar job")
}

// GetDailyBarJob 获取单个日 K 任务
func (s *Store) GetDailyBarJob(ctx context.Context, id string) (StockV2DailyBarJob, error) {
	row := s.db.QueryRowContext(ctx, dailyBarJobSelect+" WHERE id = ?", id)
	job, err := scanDailyBarJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2DailyBarJob{}, ErrDailyBarJobNotFound
		}
		return StockV2DailyBarJob{}, wrapError(err, "get daily bar job")
	}
	return job, nil
}

// GetLatestDailyBarJob 获取最近一条日 K 任务
func (s *Store) GetLatestDailyBarJob(ctx context.Context) (StockV2DailyBarJob, error) {
	row := s.db.QueryRowContext(ctx, dailyBarJobSelect+" ORDER BY created_at DESC LIMIT 1")
	job, err := scanDailyBarJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2DailyBarJob{}, ErrDailyBarJobNotFound
		}
		return StockV2DailyBarJob{}, wrapError(err, "get latest daily bar job")
	}
	return job, nil
}

// ListDailyBarJobs 列出日 K 任务（按创建时间倒序）
func (s *Store) ListDailyBarJobs(ctx context.Context, limit int, offset int) ([]StockV2DailyBarJob, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, dailyBarJobSelect+" ORDER BY created_at DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, wrapError(err, "list daily bar jobs")
	}
	return scanRows(rows, scanDailyBarJob, "scan daily bar job", "iterate daily bar jobs")
}

// CountDailyBarJobs 返回日 K 任务总数，供前端分页。
func (s *Store) CountDailyBarJobs(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_daily_bar_jobs`).Scan(&count)
	if err != nil {
		return 0, wrapError(err, "count daily bar jobs")
	}
	return count, nil
}

// ListRunningDailyBarJobs 列出当前正在执行的日 K 任务，不受分页影响。
func (s *Store) ListRunningDailyBarJobs(ctx context.Context) ([]StockV2DailyBarJob, error) {
	rows, err := s.db.QueryContext(ctx, dailyBarJobSelect+" WHERE status = 'running' ORDER BY created_at DESC")
	if err != nil {
		return nil, wrapError(err, "list running daily bar jobs")
	}
	return scanRows(rows, scanDailyBarJob, "scan running daily bar job", "iterate running daily bar jobs")
}

// FindRunningDailyBarJob 获取同一任务作用域内的运行中日 K 任务。
func (s *Store) FindRunningDailyBarJob(ctx context.Context, mode, symbol, rangeCode, adjusted string) (StockV2DailyBarJob, error) {
	row := s.db.QueryRowContext(ctx, dailyBarJobSelect+`
		WHERE status = 'running'
		  AND COALESCE(mode,'') = ?
		  AND COALESCE(symbol,'') = ?
		  AND COALESCE(range,'') = ?
		  AND COALESCE(adjusted,'') = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, mode, symbol, rangeCode, adjusted)
	job, err := scanDailyBarJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2DailyBarJob{}, ErrDailyBarJobNotFound
		}
		return StockV2DailyBarJob{}, wrapError(err, "find running daily bar job")
	}
	return job, nil
}

// HasRunningDailyBarJob 是否有进行中的日 K 任务（用于去重）
func (s *Store) HasRunningDailyBarJob(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stockv2_daily_bar_jobs WHERE status = 'running'`).Scan(&count)
	if err != nil {
		return false, wrapError(err, "check running daily bar job")
	}
	return count > 0, nil
}

func (s *Store) FailRunningDailyBarJobs(ctx context.Context, reason string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_daily_bar_jobs
		SET status = 'failed', end_at = ?, error_message = ?
		WHERE status = 'running'
	`, time.Now(), strings.TrimSpace(reason))
	if err != nil {
		return 0, wrapError(err, "fail running daily bar jobs")
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

// GetLatestDailyBarJobError 返回某只股票最近一次日 K 任务失败摘要。
func (s *Store) GetLatestDailyBarJobError(ctx context.Context, symbol, adjusted string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(symbol,''), COALESCE(error_message,''), COALESCE(failed_items,'')
		FROM stockv2_daily_bar_jobs
		WHERE COALESCE(adjusted,'') = ?
		  AND (status = 'failed' OR failed_count > 0)
		ORDER BY created_at DESC
		LIMIT 50
	`, adjusted)
	if err != nil {
		return "", wrapError(err, "get latest daily bar job error")
	}
	defer rows.Close()

	for rows.Next() {
		var jobSymbol, errorMessage, failedItemsJSON string
		if err := rows.Scan(&jobSymbol, &errorMessage, &failedItemsJSON); err != nil {
			return "", wrapError(err, "scan daily bar job error")
		}
		if jobSymbol == symbol && errorMessage != "" {
			return errorMessage, nil
		}
		if failedItemsJSON != "" && failedItemsJSON != "[]" {
			var failedItems []UpdateFailure
			if err := json.Unmarshal([]byte(failedItemsJSON), &failedItems); err == nil {
				for _, item := range failedItems {
					if item.Symbol == symbol && item.Reason != "" {
						return item.Reason, nil
					}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", wrapError(err, "iterate daily bar job errors")
	}
	return "", nil
}

func (s *Store) GetLatestDailyBarJobErrors(ctx context.Context, symbols []string, adjusted string) (map[string]string, error) {
	symbols = compactStringList(symbols, 100)
	if len(symbols) == 0 {
		return map[string]string{}, nil
	}
	wanted := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(symbol,''), COALESCE(error_message,''), COALESCE(failed_items,'')
		FROM stockv2_daily_bar_jobs
		WHERE COALESCE(adjusted,'') = ?
		  AND (status = 'failed' OR failed_count > 0)
		ORDER BY created_at DESC
		LIMIT 100
	`, adjusted)
	if err != nil {
		return nil, wrapError(err, "get latest daily bar job errors")
	}
	defer rows.Close()

	out := make(map[string]string, len(symbols))
	for rows.Next() {
		var jobSymbol, errorMessage, failedItemsJSON string
		if err := rows.Scan(&jobSymbol, &errorMessage, &failedItemsJSON); err != nil {
			return nil, wrapError(err, "scan daily bar job error")
		}
		if wanted[jobSymbol] && errorMessage != "" {
			if _, ok := out[jobSymbol]; !ok {
				out[jobSymbol] = errorMessage
			}
		}
		if failedItemsJSON == "" || failedItemsJSON == "[]" {
			continue
		}
		var failedItems []UpdateFailure
		if err := json.Unmarshal([]byte(failedItemsJSON), &failedItems); err != nil {
			continue
		}
		for _, item := range failedItems {
			if wanted[item.Symbol] && item.Reason != "" {
				if _, ok := out[item.Symbol]; !ok {
					out[item.Symbol] = item.Reason
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate daily bar job errors")
	}
	return out, nil
}

// PruneDailyBarJobs 清理日 K 任务记录，保留最近 keep 条
func (s *Store) PruneDailyBarJobs(ctx context.Context, keep int) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM stockv2_daily_bar_jobs
		WHERE id IN (
			SELECT id FROM stockv2_daily_bar_jobs
			ORDER BY created_at DESC
			LIMIT -1 OFFSET ?
		)
	`, keep)
	return wrapError(err, "prune daily bar jobs")
}

const dailyBarJobSelect = `
	SELECT id, job_type, COALESCE(mode,''), COALESCE(symbol,''), status, total_count, processed_count,
	       success_count, failed_count, COALESCE(failed_items,''), start_at, end_at,
	       COALESCE(error_message,''), COALESCE(range,''), COALESCE(adjusted,''),
	       COALESCE(trigger_type,''), COALESCE(trigger_source,''), created_at
	FROM stockv2_daily_bar_jobs
`

func scanDailyBarJob(row rowScanner) (StockV2DailyBarJob, error) {
	var job StockV2DailyBarJob
	var startAt, endAt sql.NullTime
	var failedItemsJSON string

	err := row.Scan(
		&job.ID, &job.JobType, &job.Mode, &job.Symbol, &job.Status,
		&job.TotalCount, &job.ProcessedCount, &job.SuccessCount, &job.FailedCount,
		&failedItemsJSON, &startAt, &endAt, &job.ErrorMessage,
		&job.RangeCode, &job.Adjusted, &job.TriggerType, &job.TriggerSource,
		&job.CreatedAt,
	)
	if err != nil {
		return job, err
	}

	job.StartAt = nullTimeDefault(startAt, job.CreatedAt)
	if endAt.Valid {
		job.EndAt = endAt.Time
	}
	if failedItemsJSON != "" && failedItemsJSON != "[]" {
		_ = json.Unmarshal([]byte(failedItemsJSON), &job.FailedItems)
	}
	return job, nil
}
