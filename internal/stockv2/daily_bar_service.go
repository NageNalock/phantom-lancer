package stockv2

import (
	"context"
	"errors"
	"fmt"
	"time"

	"phantom-lancer/internal/safelog"
)

// Daily Bars 服务层。
//
// 两类入口：
//  1. 统一数据资产维护：runUniverseUpdate 保存标的后检查该标的日 K，缺失 / 不足 / 陈旧才补。
//  2. 手动补拉：RunDailyBarsJob 支持单只、持仓热集合、全市场最近交易日窗口。
//
// 失败绝不伪造：抓取失败返回 error，不写空 bar、不伪装、不用最新价派生。

const (
	dailyBarsAgentTarget               = 250 // Agent 默认要求最近 250 个交易日
	assetMaintenanceDailyBarWindowDays = 420 // natural days; stably covers >=250 trading sessions
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
	end := effectiveDailyBarEnd(now.In(loc), loc)
	start := end.AddDate(0, 0, -dailyBarRangeDays(r))
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

func assetMaintenanceDailyBarStartEnd(now time.Time) (string, string) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	end := effectiveDailyBarEnd(now.In(loc), loc)
	start := end.AddDate(0, 0, -assetMaintenanceDailyBarWindowDays)
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

func effectiveDailyBarEnd(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	end := now.In(loc)
	if end.Hour() < 16 || (end.Hour() == 16 && end.Minute() < 30) {
		end = end.AddDate(0, 0, -1)
	}
	for end.Weekday() == time.Saturday || end.Weekday() == time.Sunday {
		end = end.AddDate(0, 0, -1)
	}
	return end
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
	stats, err := s.store.GetDailyBarsStatsDetailed(ctx, symbol, adjusted)
	if err != nil {
		return DailyBarsQuality{}, err
	}
	lastErr := stats.LastError
	if lastErr == "" {
		jobErr, err := s.store.GetLatestDailyBarJobError(ctx, symbol, adjusted)
		if err != nil {
			if s.log != nil {
				s.log.Warn("get latest daily bar job error failed", "symbol", symbol, "adjusted", adjusted, "error", safelog.Text(err.Error(), 240))
			}
		} else {
			lastErr = jobErr
		}
	}

	stats.LastError = lastErr
	return dailyBarsQualityFromStats(symbol, adjusted, stats, time.Now()), nil
}

func (s *Service) GetDailyBarsQualityBatch(ctx context.Context, symbols []string, adjusted string) (map[string]DailyBarsQuality, error) {
	adjusted = normalizeDailyBarAdjusted(adjusted)
	symbols = compactStringList(symbols, 200)
	out := make(map[string]DailyBarsQuality, len(symbols))
	if len(symbols) == 0 {
		return out, nil
	}
	stats, err := s.store.GetDailyBarsStatsBatch(ctx, symbols, adjusted)
	if err != nil {
		return nil, err
	}
	jobErrors, err := s.store.GetLatestDailyBarJobErrors(ctx, symbols, adjusted)
	if err != nil {
		if s.log != nil {
			s.log.Warn("get latest daily bar job errors failed", "symbol_count", len(symbols), "adjusted", adjusted, "error", safelog.Text(err.Error(), 240))
		}
		jobErrors = map[string]string{}
	}
	checkedAt := time.Now()
	for _, symbol := range symbols {
		stat := stats[symbol]
		stat.Symbol = symbol
		if stat.LastError == "" {
			stat.LastError = jobErrors[symbol]
		}
		out[symbol] = dailyBarsQualityFromStats(symbol, adjusted, stat, checkedAt)
	}
	return out, nil
}

func dailyBarsQualityFromStats(symbol, adjusted string, stats dailyBarsStats, checkedAt time.Time) DailyBarsQuality {
	q := DailyBarsQuality{
		Symbol:           symbol,
		Adjusted:         adjusted,
		HasData:          stats.RowCount > 0,
		RowCount:         stats.RowCount,
		IncompleteCount:  stats.IncompleteCount,
		FacetsComplete:   stats.RowCount > 0 && stats.IncompleteCount == 0,
		EarliestDate:     stats.Earliest,
		LatestDate:       stats.Latest,
		LastErrorMessage: stats.LastError,
		Source:           stats.Source,
		CheckedAt:        checkedAt,
	}
	if stats.RowCount > 0 {
		q.Meets250 = stats.RowCount >= dailyBarsAgentTarget
		q.Stale = isDailyBarsStale(stats.Latest, checkedAt)
	} else {
		q.Stale = true
	}
	return q
}

// ensureOneSymbol 抓取单只股票指定区间日 K 并落盘。失败返回 error，不写坏数据。
func (s *Service) ensureOneSymbol(ctx context.Context, inst StockV2Instrument, startDate, endDate, adjusted string) (int, error) {
	symbol := inst.Symbol
	market := inst.Market
	startDate, endDate = clampDailyBarRangeToInstrument(inst, startDate, endDate)
	if startDate == "" {
		return 0, nil
	}
	if adjusted == DailyBarAdjustedNone {
		endDate = dailyBarBatchTargetDate(ctx, endDate)
		startTime, err := time.Parse("2006-01-02", startDate)
		if err != nil {
			return 0, err
		}
		endTime, err := time.Parse("2006-01-02", endDate)
		if err != nil {
			return 0, err
		}
		// Best effort: a temporary flow-provider failure must not block genuine
		// OHLC/amount/turnover gaps from being repaired below.
		_, _ = s.repairStoredDailyBarFlowFacets(ctx, inst, startDate, endDate)
		// Historical OHLC is fetched only when amount/turnover are missing. Stock
		// rows that only lack flow facets are retried above with one Eastmoney call.
		dates, err := s.store.GetCompleteDailyBarDates(ctx, symbol, adjusted, startDate, endDate, false)
		if err != nil {
			return 0, err
		}
		tradingDates, err := s.observedTradingDates(ctx, startDate, endDate)
		if err != nil {
			return 0, err
		}
		ranges := planDailyBarMissingRangesWithCalendar(dates, tradingDates, startTime, endTime)
		ranges = excludeBatchClosingQuoteRetry(ctx, symbol, endDate, ranges)
		checkedRanges, err := s.store.ListDailyBarGapChecks(ctx, symbol, adjusted, startDate, endDate)
		if err != nil {
			return 0, err
		}
		ranges = subtractCheckedDailyBarRanges(ranges, checkedRanges)
		if len(ranges) == 0 {
			return 0, nil
		}
		bars, absencesConfirmed, err := s.fetchDailyBarsForMissingRanges(ctx, inst, ranges)
		if err != nil {
			return 0, err
		}
		var checkedAbsences []dailyBarMissingRange
		if absencesConfirmed {
			checkedAbsences = stableDailyBarGapChecks(dailyBarRangesWithoutReturnedBars(ranges, bars), time.Now())
		}
		_ = s.enrichDailyBarsWithDataFacets(ctx, inst, bars)
		if err := s.store.UpsertDailyBars(ctx, bars); err != nil {
			return 0, err
		}
		if err := s.store.RecordDailyBarGapChecks(ctx, symbol, adjusted, checkedAbsences); err != nil {
			return 0, err
		}
		return len(bars), nil
	}

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
	if quality.HasData && quality.FacetsComplete && !quality.Stale && quality.EarliestDate <= start {
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

	// instrument 优先取主数据；缺失时数据源会按代码推断 market。
	inst := StockV2Instrument{Symbol: symbol, InstrumentType: InstrumentTypeStock}
	if existing, err := s.store.GetInstrument(ctx, symbol); err == nil {
		inst = existing
		inst.Symbol = symbol
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
	go s.runEnsureSymbolJob(context.Background(), job.ID, inst, start, end, adjusted)
	return result, nil
}

// runEnsureSymbolJob 异步执行单只补拉，更新任务状态。
func (s *Service) runEnsureSymbolJob(ctx context.Context, jobID string, inst StockV2Instrument, start, end, adjusted string) {
	symbol := inst.Symbol
	market := inst.Market
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("daily bar ensure job panicked", "job_id", jobID, "symbol", symbol, "market", market, "start_date", start, "end_date", end, "adjusted", adjusted, "panic", r)
			}
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID:           jobID,
				Status:       "failed",
				EndAt:        time.Now(),
				ErrorMessage: fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	_, err := s.ensureOneSymbol(ctx, inst, start, end, adjusted)
	now := time.Now()
	if err != nil {
		if s.log != nil {
			s.log.Warn("ensure daily bars failed", "job_id", jobID, "symbol", symbol, "market", market, "start_date", start, "end_date", end, "adjusted", adjusted, "error", safelog.Text(err.Error(), 300))
		}
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
		s.dailyBarJobMu.Lock()
		defer s.dailyBarJobMu.Unlock()
	} else {
		s.bulkMaintenanceMu.Lock()
		defer s.bulkMaintenanceMu.Unlock()
		if s.bulkMaintenanceRun {
			return StockV2DailyBarJob{}, ErrDailyBarJobAlreadyRunning
		}
	}

	if mode == DailyBarJobModeSymbol {
		if running, err := s.store.FindRunningDailyBarJob(ctx, DailyBarJobModeSymbol, req.Symbol, rangeCode, adjusted); err == nil {
			return running, nil
		} else if !errors.Is(err, ErrDailyBarJobNotFound) {
			return StockV2DailyBarJob{}, err
		}
	} else {
		// 批量模式（hot / universe）全局去重，避免多个大任务同时打数据源。
		running, err := s.store.HasRunningDailyBarJob(ctx)
		if err != nil {
			return StockV2DailyBarJob{}, err
		}
		if running {
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
	if mode != DailyBarJobModeSymbol {
		s.bulkMaintenanceRun = true
	}

	go s.runDailyBarsBatchJob(context.Background(), job.ID, req, mode, rangeCode, adjusted)
	return job, nil
}

// runDailyBarsBatchJob 异步执行批量日 K 任务（symbol/hot/universe_incremental）。
func (s *Service) runDailyBarsBatchJob(ctx context.Context, jobID string, req DailyBarsJobRequest, mode, rangeCode, adjusted string) {
	if mode != DailyBarJobModeSymbol {
		defer func() {
			s.bulkMaintenanceMu.Lock()
			s.bulkMaintenanceRun = false
			s.bulkMaintenanceMu.Unlock()
		}()
	}
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("daily bar batch job panicked", "job_id", jobID, "mode", mode, "range_code", rangeCode, "adjusted", adjusted, "trigger_type", req.TriggerType, "trigger_source", req.TriggerSource, "symbol", req.Symbol, "panic", r)
			}
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
		endT := effectiveDailyBarEnd(time.Now().In(loc), loc)
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
		if s.log != nil {
			s.log.Warn("daily bar batch collect symbols failed", "job_id", jobID, "mode", mode, "range_code", rangeCode, "adjusted", adjusted, "trigger_type", req.TriggerType, "trigger_source", req.TriggerSource, "symbol", req.Symbol, "error", safelog.Text(collectErr.Error(), 240))
		}
		_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
			ID: jobID, Status: "failed", EndAt: time.Now(),
			ErrorMessage: truncateDailyBarErr(collectErr.Error()),
		})
		return
	}

	// GetInstrumentsBySymbols caps one query at 1000 symbols. Chunk this local
	// lookup so the full-market job retains persisted fund/stock types and
	// listing bounds without adding one database request per symbol.
	instrumentBySymbol := make(map[string]StockV2Instrument, len(symbols))
	for offset := 0; offset < len(symbols); offset += 1000 {
		endOffset := min(offset+1000, len(symbols))
		instruments, err := s.store.GetInstrumentsBySymbols(ctx, symbols[offset:endOffset])
		if err != nil {
			if s.log != nil {
				s.log.Warn("daily bar batch load instruments failed", "job_id", jobID, "mode", mode, "symbol_count", len(symbols), "error", safelog.Text(err.Error(), 240))
			}
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID: jobID, Status: "failed", EndAt: time.Now(),
				ErrorMessage: truncateDailyBarErr(err.Error()),
			})
			return
		}
		for _, instrument := range instruments {
			instrumentBySymbol[instrument.Symbol] = instrument
		}
	}

	total := len(symbols)
	_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{ID: jobID, TotalCount: total})
	workCtx := ctx
	if mode == DailyBarJobModeUniverseIncremental && adjusted == DailyBarAdjustedNone {
		instruments := make([]StockV2Instrument, 0, len(symbols))
		for _, symbol := range symbols {
			instruments = append(instruments, dailyBarBatchInstrument(symbol, instrumentBySymbol))
		}
		var prefillErr error
		workCtx, _, prefillErr = s.prefillClosingDailyBarsBatch(ctx, instruments, end)
		if prefillErr != nil {
			_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
				ID: jobID, Status: "failed", EndAt: time.Now(),
				ErrorMessage: truncateDailyBarErr(prefillErr.Error()),
			})
			return
		}
	}

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
		inst := dailyBarBatchInstrument(sym, instrumentBySymbol)
		_, err := s.ensureOneSymbol(workCtx, inst, start, end, adjusted)
		if err != nil {
			failedItems = append(failedItems, UpdateFailure{Symbol: sym, Reason: safelog.Text(truncateDailyBarErr(err.Error()), 240)})
			if errors.Is(err, errBaiduDailyBarsAccessDenied) {
				_ = s.store.UpdateDailyBarJob(ctx, StockV2DailyBarJob{
					ID:             jobID,
					Status:         "failed",
					EndAt:          time.Now(),
					ProcessedCount: processed,
					SuccessCount:   success,
					FailedCount:    len(failedItems),
					ErrorMessage:   truncateDailyBarErr(err.Error()),
					FailedItems:    failedItems,
				})
				return
			}
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
	if len(failedItems) > 0 && s.log != nil {
		s.log.Warn("daily bar batch job completed with item failures", "job_id", jobID, "mode", mode, "range_code", rangeCode, "adjusted", adjusted, "trigger_type", req.TriggerType, "trigger_source", req.TriggerSource, "symbol", req.Symbol, "start_date", start, "end_date", end, "total_count", total, "processed_count", processed, "success_count", success, "failed_count", len(failedItems), "failure_sample", stockV2FailureSample(failedItems, 5))
	}
	_ = s.store.PruneDailyBarJobs(ctx, 100)
}

func dailyBarBatchInstrument(symbol string, instrumentBySymbol map[string]StockV2Instrument) StockV2Instrument {
	market, instrumentType := inferInstrumentMarketAndType(symbol)
	instrument, ok := instrumentBySymbol[symbol]
	if !ok {
		return StockV2Instrument{Symbol: symbol, Market: market, InstrumentType: instrumentType}
	}
	instrument.Symbol = symbol
	if instrument.Market == "" {
		instrument.Market = market
	}
	if instrument.InstrumentType == "" {
		instrument.InstrumentType = instrumentType
	}
	return instrument
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

func sameDayInLoc(a, b time.Time, loc *time.Location) bool {
	if a.IsZero() {
		return false
	}
	a = a.In(loc)
	b = b.In(loc)
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
