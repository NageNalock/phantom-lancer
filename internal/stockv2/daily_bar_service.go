package stockv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Daily Bars 服务层。
//
// 两类入口：
//  1. 统一数据资产维护：runUniverseUpdate 保存标的后检查该标的日 K，缺失 / 不足 / 陈旧才补。
//  2. 手动补拉：RunDailyBarsJob 支持单只、持仓热集合、全市场最近交易日窗口。
//
// 失败绝不伪造：抓取失败返回 error，不写空 bar、不伪装、不用最新价派生。

const (
	dailyBarsAgentTarget            = 250 // Agent 默认要求最近 250 个交易日
	universeMaintenanceMaxFreshness = 24 * time.Hour
)

func isValidDailyBarRange(r string) bool {
	switch r {
	case DailyBarRange6M, DailyBarRange1Y, DailyBarRange3Y, DailyBarRange5Y:
		return true
	}
	return false
}

func isValidDailyBarAdjusted(a string) bool {
	switch a {
	case DailyBarAdjustedNone, DailyBarAdjustedQFQ, DailyBarAdjustedHFQ:
		return true
	}
	return false
}

func normalizeDailyBarRange(r string) string {
	if !isValidDailyBarRange(r) {
		return DailyBarRange1Y
	}
	return r
}

func normalizeDailyBarAdjusted(a string) string {
	if !isValidDailyBarAdjusted(a) {
		return DailyBarAdjustedNone
	}
	return a
}

func dailyBarRangeDays(r string) int {
	switch r {
	case DailyBarRange6M:
		return 183
	case DailyBarRange1Y:
		return 365
	case DailyBarRange3Y:
		return 1096
	case DailyBarRange5Y:
		return 1826
	}
	return 365
}

// dailyBarRangeStartEnd 把区间码转成 [start, end] 日期串（Asia/Shanghai）。
func dailyBarRangeStartEnd(r string, now time.Time) (string, string) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	end := now.In(loc)
	start := end.AddDate(0, 0, -dailyBarRangeDays(r))
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

func isDailyBarsStale(latestDate string, now time.Time) bool {
	if latestDate == "" {
		return true
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	t, err := time.ParseInLocation("2006-01-02", latestDate, loc)
	if err != nil {
		return true
	}
	// 容忍最近 5 个自然日（覆盖周末 / 节假日缓冲），超出视为 stale
	return now.In(loc).Sub(t) > 5*24*time.Hour
}

func truncateDailyBarErr(msg string) string {
	if len(msg) > 200 {
		return msg[:200] + "..."
	}
	return msg
}

// GetDailyBars 查询日 K（直接读本地，不触发抓取）。
func (s *Service) GetDailyBars(ctx context.Context, symbol string, limit int, startDate, endDate, adjusted string) ([]StockV2DailyBar, error) {
	return s.store.GetDailyBars(ctx, symbol, normalizeDailyBarAdjusted(adjusted), startDate, endDate, limit)
}

// GetDailyBarsQuality 评估本地日 K 数据质量。
func (s *Service) GetDailyBarsQuality(ctx context.Context, symbol, adjusted string) (DailyBarsQuality, error) {
	adjusted = normalizeDailyBarAdjusted(adjusted)
	rowCount, earliest, latest, source, lastErr, err := s.store.GetDailyBarsStats(ctx, symbol, adjusted)
	if err != nil {
		return DailyBarsQuality{}, err
	}
	if lastErr == "" {
		jobErr, err := s.store.GetLatestDailyBarJobError(ctx, symbol, adjusted)
		if err != nil {
			s.log.Warn("get latest daily bar job error failed", "symbol", symbol, "error", err)
		} else {
			lastErr = jobErr
		}
	}

	q := DailyBarsQuality{
		Symbol:           symbol,
		Adjusted:         adjusted,
		HasData:          rowCount > 0,
		RowCount:         rowCount,
		EarliestDate:     earliest,
		LatestDate:       latest,
		LastErrorMessage: lastErr,
		Source:           source,
		CheckedAt:        time.Now(),
	}
	if rowCount > 0 {
		q.Meets250 = rowCount >= dailyBarsAgentTarget
		q.Stale = isDailyBarsStale(latest, time.Now())
	} else {
		q.Stale = true
	}
	return q, nil
}

func (s *Service) GetDailyBarsQualityBatch(ctx context.Context, symbols []string, adjusted string) (map[string]DailyBarsQuality, error) {
	out := make(map[string]DailyBarsQuality, len(symbols))
	seen := make(map[string]bool, len(symbols))
	for _, raw := range symbols {
		symbol := strings.TrimSpace(raw)
		if symbol == "" || seen[symbol] {
			continue
		}
		seen[symbol] = true
		if len(seen) > 100 {
			break
		}
		quality, err := s.GetDailyBarsQuality(ctx, symbol, adjusted)
		if err != nil {
			return nil, err
		}
		out[symbol] = quality
	}
	return out, nil
}

// ensureOneSymbol 抓取单只股票指定区间日 K 并落盘。失败返回 error，不写坏数据。
func (s *Service) ensureOneSymbol(ctx context.Context, symbol, market, startDate, endDate, adjusted string) (int, error) {
	// start/end 已限定范围；count 给上限 1800（经端点验证稳定），覆盖 5y。
	bars, err := s.dailyBarsSource.FetchDailyBars(ctx, symbol, market, startDate, endDate, adjusted, 1800)
	if err != nil {
		return 0, err
	}
	for i := range bars {
		bars[i].Symbol = symbol
	}
	if err := s.store.UpsertDailyBars(ctx, bars); err != nil {
		return 0, err
	}
	return len(bars), nil
}

// EnsureDailyBars 按需补拉：本地满足则跳过，否则起一个异步 ensure 任务。
func (s *Service) EnsureDailyBars(ctx context.Context, symbol, rangeCode, adjusted string) (DailyBarsEnsureResult, error) {
	rangeCode = normalizeDailyBarRange(rangeCode)
	adjusted = normalizeDailyBarAdjusted(adjusted)

	quality, err := s.GetDailyBarsQuality(ctx, symbol, adjusted)
	if err != nil {
		return DailyBarsEnsureResult{}, err
	}
	start, end := dailyBarRangeStartEnd(rangeCode, time.Now())

	result := DailyBarsEnsureResult{
		Symbol:       symbol,
		RangeCode:    rangeCode,
		Adjusted:     adjusted,
		EarliestDate: quality.EarliestDate,
		LatestDate:   quality.LatestDate,
		Quality:      quality,
	}

	// 本地已覆盖请求起点且数据不 stale → 直接跳过，不重复抓取
	if quality.HasData && !quality.Stale && quality.EarliestDate <= start {
		result.Skipped = true
		return result, nil
	}

	if running, err := s.store.FindRunningDailyBarJob(ctx, DailyBarJobModeSymbol, symbol, rangeCode, adjusted); err == nil {
		result.JobID = running.ID
		result.JobRunning = true
		return result, nil
	} else if !errors.Is(err, ErrDailyBarJobNotFound) {
		return result, err
	}

	// market 优先取主数据；缺失时数据源会按代码推断
	market := ""
	if inst, err := s.store.GetInstrument(ctx, symbol); err == nil {
		market = inst.Market
	}

	job := StockV2DailyBarJob{
		ID:            generateID(),
		JobType:       DailyBarJobTypeEnsure,
		Mode:          DailyBarJobModeSymbol,
		Symbol:        symbol,
		Status:        "running",
		TotalCount:    1,
		RangeCode:     rangeCode,
		Adjusted:      adjusted,
		TriggerType:   "manual",
		TriggerSource: "web",
		StartAt:       time.Now(),
		CreatedAt:     time.Now(),
	}
	if err := s.store.CreateDailyBarJob(ctx, job); err != nil {
		return result, wrapError(err, "create daily bar job")
	}

	result.JobID = job.ID
	result.JobRunning = true
	go s.runEnsureSymbolJob(context.Background(), job.ID, symbol, market, start, end, adjusted)
	return result, nil
}

// runEnsureSymbolJob 异步执行单只补拉，更新任务状态。
func (s *Service) runEnsureSymbolJob(ctx context.Context, jobID, symbol, market, start, end, adjusted string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("daily bar ensure job panicked", "job_id", jobID, "symbol", symbol, "panic", r)
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID:           jobID,
				Status:       "failed",
				EndAt:        time.Now(),
				ErrorMessage: fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	_, err := s.ensureOneSymbol(ctx, symbol, market, start, end, adjusted)
	now := time.Now()
	if err != nil {
		s.log.Warn("ensure daily bars failed", "symbol", symbol, "error", err)
		_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
			ID:             jobID,
			Status:         "failed",
			EndAt:          now,
			ProcessedCount: 1,
			FailedCount:    1,
			ErrorMessage:   truncateDailyBarErr(err.Error()),
			FailedItems:    []UpdateFailure{{Symbol: symbol, Reason: truncateDailyBarErr(err.Error())}},
		})
		return
	}

	_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
		ID:             jobID,
		Status:         "completed",
		EndAt:          now,
		ProcessedCount: 1,
		SuccessCount:   1,
	})
	_ = s.store.PruneDailyBarJobs(ctx, 100)
}

// RunDailyBarsJob 触发日 K 任务（symbol / hot / universe_incremental）。
func (s *Service) RunDailyBarsJob(ctx context.Context, req DailyBarsJobRequest) (StockV2DailyBarJob, error) {
	mode := req.Mode
	if mode == "" {
		mode = DailyBarJobModeSymbol
	}
	rangeCode := normalizeDailyBarRange(req.RangeCode)
	adjusted := normalizeDailyBarAdjusted(req.Adjusted)
	triggerType := req.TriggerType
	if triggerType == "" {
		triggerType = "manual"
	}
	triggerSource := req.TriggerSource
	if triggerSource == "" {
		triggerSource = "web"
	}
	req.Mode = mode
	req.RangeCode = rangeCode
	req.Adjusted = adjusted
	req.TriggerType = triggerType
	req.TriggerSource = triggerSource
	if mode == DailyBarJobModeSymbol && req.Symbol == "" {
		return StockV2DailyBarJob{}, errors.New("symbol is required for symbol mode")
	}

	if mode == DailyBarJobModeSymbol {
		if running, err := s.store.FindRunningDailyBarJob(ctx, DailyBarJobModeSymbol, req.Symbol, rangeCode, adjusted); err == nil {
			return running, nil
		} else if !errors.Is(err, ErrDailyBarJobNotFound) {
			return StockV2DailyBarJob{}, err
		}
	} else {
		// 批量模式（hot / universe）全局去重，避免多个大任务同时打数据源。
		if running, _ := s.store.HasRunningDailyBarJob(ctx); running {
			return StockV2DailyBarJob{}, ErrDailyBarJobAlreadyRunning
		}
	}

	jobType := DailyBarJobTypeIncremental
	if mode == DailyBarJobModeSymbol {
		jobType = DailyBarJobTypeEnsure
	}

	job := StockV2DailyBarJob{
		ID:            generateID(),
		JobType:       jobType,
		Mode:          mode,
		Symbol:        req.Symbol,
		Status:        "running",
		RangeCode:     rangeCode,
		Adjusted:      adjusted,
		TriggerType:   triggerType,
		TriggerSource: triggerSource,
		StartAt:       time.Now(),
		CreatedAt:     time.Now(),
	}
	if err := s.store.CreateDailyBarJob(ctx, job); err != nil {
		return StockV2DailyBarJob{}, wrapError(err, "create daily bar job")
	}

	go s.runDailyBarsBatchJob(context.Background(), job.ID, req, mode, rangeCode, adjusted)
	return job, nil
}

// runDailyBarsBatchJob 异步执行批量日 K 任务（symbol/hot/universe_incremental）。
func (s *Service) runDailyBarsBatchJob(ctx context.Context, jobID string, req DailyBarsJobRequest, mode, rangeCode, adjusted string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("daily bar batch job panicked", "job_id", jobID, "panic", r)
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID:           jobID,
				Status:       "failed",
				EndAt:        time.Now(),
				ErrorMessage: fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	// universe_incremental 是收盘后增量，只补最近交易日窗口，忽略请求的大区间
	var start, end string
	if mode == DailyBarJobModeUniverseIncremental {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			loc = time.Local
		}
		endT := time.Now().In(loc)
		startT := endT.AddDate(0, 0, -10) // 最近 ~10 个自然日，覆盖最近几个交易日
		start, end = startT.Format("2006-01-02"), endT.Format("2006-01-02")
	} else {
		start, end = dailyBarRangeStartEnd(rangeCode, time.Now())
	}

	// 收集待处理 symbol
	var symbols []string
	var collectErr error
	switch mode {
	case DailyBarJobModeSymbol:
		symbols = []string{req.Symbol}
	case DailyBarJobModeHot:
		symbols, collectErr = s.store.ListHoldingSymbols(ctx)
	case DailyBarJobModeUniverseIncremental:
		symbols, collectErr = s.store.ListInstrumentSymbols(ctx)
	default:
		collectErr = fmt.Errorf("unknown mode %q", mode)
	}
	if collectErr != nil {
		_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
			ID: jobID, Status: "failed", EndAt: time.Now(),
			ErrorMessage: truncateDailyBarErr(collectErr.Error()),
		})
		return
	}

	total := len(symbols)
	_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{ID: jobID, TotalCount: total})

	processed := 0
	success := 0
	var failedItems []UpdateFailure

	for i, sym := range symbols {
		select {
		case <-ctx.Done():
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID: jobID, Status: "cancelled", EndAt: time.Now(),
				ProcessedCount: processed, SuccessCount: success,
				FailedCount: len(failedItems), FailedItems: failedItems,
			})
			return
		default:
		}

		processed++
		_, err := s.ensureOneSymbol(ctx, sym, "", start, end, adjusted)
		if err != nil {
			failedItems = append(failedItems, UpdateFailure{Symbol: sym, Reason: truncateDailyBarErr(err.Error())})
		} else {
			success++
		}

		// 每 25 只或最后一只，更新进度
		if i%25 == 0 || i == total-1 {
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID:             jobID,
				ProcessedCount: processed,
				SuccessCount:   success,
				FailedCount:    len(failedItems),
				FailedItems:    failedItems,
			})
		}

		// 单只间抖动，避免被数据源风控；最后一只不抖
		if i < total-1 {
			if err := sleepJitter(ctx, 80*time.Millisecond, 60*time.Millisecond); err != nil {
				return
			}
		}
	}

	status := "completed"
	if success == 0 && total > 0 {
		status = "failed"
	}
	endAt := time.Now()
	_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
		ID:             jobID,
		Status:         status,
		EndAt:          endAt,
		ProcessedCount: processed,
		SuccessCount:   success,
		FailedCount:    len(failedItems),
		FailedItems:    failedItems,
	})
	if req.TriggerType == "scheduled" && mode == DailyBarJobModeUniverseIncremental && status == "completed" {
		s.recordDailyBarsLastRun(ctx, endAt)
	}
	_ = s.store.PruneDailyBarJobs(ctx, 100)
}

// GetDailyBarJob / ListDailyBarJobs / GetLatestDailyBarJob 透传 store
func (s *Service) GetDailyBarJob(ctx context.Context, id string) (StockV2DailyBarJob, error) {
	return s.store.GetDailyBarJob(ctx, id)
}
func (s *Service) ListDailyBarJobs(ctx context.Context, limit int, offset int) ([]StockV2DailyBarJob, error) {
	return s.store.ListDailyBarJobs(ctx, limit, offset)
}
func (s *Service) CountDailyBarJobs(ctx context.Context) (int, error) {
	return s.store.CountDailyBarJobs(ctx)
}
func (s *Service) ListRunningDailyBarJobs(ctx context.Context) ([]StockV2DailyBarJob, error) {
	return s.store.ListRunningDailyBarJobs(ctx)
}
func (s *Service) GetLatestDailyBarJob(ctx context.Context) (StockV2DailyBarJob, error) {
	return s.store.GetLatestDailyBarJob(ctx)
}

func (s *Service) recordDailyBarsLastRun(ctx context.Context, when time.Time) {
	settings := s.settings
	settings.DailyBarsLastRun = when
	if err := s.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		s.log.Warn("update daily bars last run failed", "error", err)
		return
	}
	s.settings = settings
}

func sameDayInLoc(a, b time.Time, loc *time.Location) bool {
	if a.IsZero() {
		return false
	}
	a = a.In(loc)
	b = b.In(loc)
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
