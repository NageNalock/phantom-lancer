package stockv2

import (
	"context"
	"fmt"
	"time"
)

type dailyBarStoredCoverageFact struct {
	TradeDate    string
	CoreComplete bool
	FlowComplete bool
}

// DailyBarCoverageQuality is the persisted, window-scoped quality contract.
// Date, core market data, and order-flow are separate so a flow-provider outage
// never causes a complete OHLCV row to be fetched again.
type DailyBarCoverageQuality struct {
	Symbol               string
	Adjusted             string
	InstrumentType       string
	WindowStartDate      string
	WindowEndDate        string
	ExpectedDateCount    int
	CoveredDateCount     int
	DateGapCount         int
	CoreGapCount         int
	FlowGapCount         int
	VerifiedNoTradeCount int
	ExpectedLatestDate   string
	CheckedAt            time.Time
}

func (s *MarketDataStore) GetDailyBarStoredCoverageFacts(
	ctx context.Context,
	symbol string,
	adjusted string,
	startDate string,
	endDate string,
) ([]dailyBarStoredCoverageFact, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	if startDate == "" {
		startDate = "0001-01-01"
	}
	if endDate == "" {
		endDate = "9999-12-31"
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH scored AS (
			SELECT
				trade_date,
				fetched_at,
				(
					COALESCE(open, 0) > 0 AND isfinite(COALESCE(open, 0)) AND
					COALESCE(high, 0) > 0 AND isfinite(COALESCE(high, 0)) AND
					COALESCE(low, 0) > 0 AND isfinite(COALESCE(low, 0)) AND
					COALESCE(close, 0) > 0 AND isfinite(COALESCE(close, 0)) AND
					COALESCE(volume, 0) > 0 AND isfinite(COALESCE(volume, 0)) AND
					high >= greatest(open, close, low) AND
					low <= least(open, close, high) AND
					(COALESCE(amount_present, FALSE) OR COALESCE(amount, 0) != 0) AND
					isfinite(COALESCE(amount, 0)) AND
					(COALESCE(turnover_rate_present, FALSE) OR COALESCE(turnover_rate, 0) != 0) AND
					COALESCE(turnover_rate, 0) >= 0 AND isfinite(COALESCE(turnover_rate, 0))
				) AS core_complete,
				(
					(COALESCE(net_inflow_present, FALSE) OR COALESCE(net_inflow, 0) != 0) AND
					isfinite(COALESCE(net_inflow, 0)) AND
					(COALESCE(main_net_inflow_present, FALSE) OR COALESCE(main_net_inflow, 0) != 0) AND
					isfinite(COALESCE(main_net_inflow, 0))
				) AS flow_complete
			FROM stockv2_daily_bars
			WHERE symbol = ? AND adjusted = ?
			  AND trade_date >= CAST(? AS DATE) AND trade_date <= CAST(? AS DATE)
		), ranked AS (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY trade_date
				ORDER BY core_complete DESC, flow_complete DESC, fetched_at DESC
			) AS rn
			FROM scored
		)
		SELECT strftime(trade_date, '%Y-%m-%d'), core_complete, flow_complete
		FROM ranked WHERE rn = 1
		ORDER BY trade_date
	`, symbol, adjusted, startDate, endDate)
	if err != nil {
		return nil, wrapError(err, "query daily bar stored coverage facts")
	}
	defer rows.Close()
	out := make([]dailyBarStoredCoverageFact, 0)
	for rows.Next() {
		var fact dailyBarStoredCoverageFact
		if err := rows.Scan(&fact.TradeDate, &fact.CoreComplete, &fact.FlowComplete); err != nil {
			return nil, wrapError(err, "scan daily bar stored coverage fact")
		}
		out = append(out, fact)
	}
	return out, wrapError(rows.Err(), "iterate daily bar stored coverage facts")
}

func evaluateDailyBarCoverageQuality(
	symbol string,
	adjusted string,
	instrumentType string,
	startDate string,
	endDate string,
	tradingDates []string,
	facts []dailyBarStoredCoverageFact,
	verifiedNoTrade []dailyBarMissingRange,
	checkedAt time.Time,
) DailyBarCoverageQuality {
	calendar := dailyBarDatesInWindow(tradingDates, startDate, endDate)
	quality := DailyBarCoverageQuality{
		Symbol:          symbol,
		Adjusted:        adjusted,
		InstrumentType:  normalizeInstrumentType(instrumentType),
		WindowStartDate: startDate,
		WindowEndDate:   endDate,
		CheckedAt:       checkedAt,
	}
	quality.ExpectedDateCount = len(calendar)
	if len(calendar) > 0 {
		quality.ExpectedLatestDate = calendar[len(calendar)-1]
	}
	factByDate := make(map[string]dailyBarStoredCoverageFact, len(facts))
	for _, fact := range facts {
		factByDate[fact.TradeDate] = fact
	}
	requireFlow := quality.InstrumentType == InstrumentTypeStock
	for _, tradeDate := range calendar {
		if fact, ok := factByDate[tradeDate]; ok {
			quality.CoveredDateCount++
			if !fact.CoreComplete {
				quality.CoreGapCount++
				continue
			}
			if requireFlow && !fact.FlowComplete {
				quality.FlowGapCount++
			}
			continue
		}
		if dailyBarDateInRanges(tradeDate, verifiedNoTrade) {
			quality.CoveredDateCount++
			quality.VerifiedNoTradeCount++
			continue
		}
		quality.DateGapCount++
	}
	return quality
}

func (s *MarketDataStore) UpsertDailyBarCoverageQuality(ctx context.Context, quality DailyBarCoverageQuality) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("market data store is not initialized")
	}
	if quality.CheckedAt.IsZero() {
		quality.CheckedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO stockv2_daily_bar_coverage_quality (
			symbol, adjusted, instrument_type, window_start_date, window_end_date,
			expected_date_count, covered_date_count, date_gap_count, core_gap_count,
			flow_gap_count, verified_no_trade_count, expected_latest_date,
			checked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		quality.Symbol, quality.Adjusted, quality.InstrumentType,
		quality.WindowStartDate, quality.WindowEndDate,
		quality.ExpectedDateCount, quality.CoveredDateCount,
		quality.DateGapCount, quality.CoreGapCount, quality.FlowGapCount,
		quality.VerifiedNoTradeCount, quality.ExpectedLatestDate,
		quality.CheckedAt, time.Now(),
	)
	return wrapError(err, "upsert daily bar coverage quality")
}

func (s *MarketDataStore) GetDailyBarCoverageQuality(ctx context.Context, symbol, adjusted string) (DailyBarCoverageQuality, error) {
	items, err := s.GetDailyBarCoverageQualityBatch(ctx, []string{symbol}, adjusted)
	if err != nil {
		return DailyBarCoverageQuality{}, err
	}
	return items[symbol], nil
}

func (s *MarketDataStore) GetDailyBarCoverageQualityBatch(ctx context.Context, symbols []string, adjusted string) (map[string]DailyBarCoverageQuality, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	symbols = compactStringList(symbols, 200)
	out := make(map[string]DailyBarCoverageQuality, len(symbols))
	if len(symbols) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(symbols)+1)
	args = append(args, adjusted)
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol, adjusted, instrument_type, window_start_date, window_end_date,
		       expected_date_count, covered_date_count, date_gap_count, core_gap_count,
		       flow_gap_count, verified_no_trade_count, COALESCE(expected_latest_date, ''),
		       checked_at
		FROM stockv2_daily_bar_coverage_quality
		WHERE adjusted = ? AND symbol IN (`+sqlPlaceholders(len(symbols))+`)
	`, args...)
	if err != nil {
		return nil, wrapError(err, "query daily bar coverage quality batch")
	}
	defer rows.Close()
	for rows.Next() {
		var item DailyBarCoverageQuality
		if err := rows.Scan(
			&item.Symbol, &item.Adjusted, &item.InstrumentType,
			&item.WindowStartDate, &item.WindowEndDate,
			&item.ExpectedDateCount, &item.CoveredDateCount,
			&item.DateGapCount, &item.CoreGapCount, &item.FlowGapCount,
			&item.VerifiedNoTradeCount, &item.ExpectedLatestDate, &item.CheckedAt,
		); err != nil {
			return nil, wrapError(err, "scan daily bar coverage quality")
		}
		out[item.Symbol] = item
	}
	return out, wrapError(rows.Err(), "iterate daily bar coverage quality")
}
