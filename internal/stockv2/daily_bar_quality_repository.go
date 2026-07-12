package stockv2

import (
	"context"
	"time"
)

func (s *Store) RefreshDailyBarCoverageQuality(
	ctx context.Context,
	inst StockV2Instrument,
	adjusted string,
	startDate string,
	endDate string,
) (DailyBarCoverageQuality, error) {
	facts, err := s.marketDB.GetDailyBarStoredCoverageFacts(ctx, inst.Symbol, adjusted, startDate, endDate)
	if err != nil {
		return DailyBarCoverageQuality{}, err
	}
	tradingDates, err := s.GetObservedTradingDates(ctx, startDate, endDate)
	if err != nil {
		return DailyBarCoverageQuality{}, err
	}
	verifiedNoTrade, err := s.ListDailyBarGapChecks(ctx, inst.Symbol, adjusted, startDate, endDate)
	if err != nil {
		return DailyBarCoverageQuality{}, err
	}
	quality := evaluateDailyBarCoverageQuality(
		inst.Symbol,
		adjusted,
		inst.InstrumentType,
		startDate,
		endDate,
		tradingDates,
		facts,
		verifiedNoTrade,
		time.Now(),
	)
	if err := s.marketDB.UpsertDailyBarCoverageQuality(ctx, quality); err != nil {
		return DailyBarCoverageQuality{}, err
	}
	return quality, nil
}

func (s *Store) GetDailyBarCoverageQuality(ctx context.Context, symbol, adjusted string) (DailyBarCoverageQuality, error) {
	return s.marketDB.GetDailyBarCoverageQuality(ctx, symbol, adjusted)
}

func (s *Store) GetDailyBarCoverageQualityBatch(ctx context.Context, symbols []string, adjusted string) (map[string]DailyBarCoverageQuality, error) {
	return s.marketDB.GetDailyBarCoverageQualityBatch(ctx, symbols, adjusted)
}
