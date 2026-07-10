package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	dailyBarSourceEastmoneyClosingQuote = "eastmoney_closing_quote"
	dailyBarSourceTencentClosingQuote   = "tencent_closing_quote"
	dailyBarCalendarAnchorSymbol        = "000001"
	dailyBarCalendarAnchorCount         = 500
)

type dailyBarQuoteBatchResult struct {
	Attempted int
	Fetched   int
	Upserted  int
	Rejected  int
	Failed    int
}

type dailyBarQuoteBatchState struct {
	tradeDate    string
	calendarFrom string
	tradingDates []string
	probeFailed  bool
	attempted    map[string]struct{}
	bars         map[string]StockV2DailyBar
}

type dailyBarQuoteBatchContextKey struct{}

// prefillClosingDailyBarsBatch reuses the existing 80-symbol quote endpoint.
// It never falls back to one request per symbol; historical gaps continue through
// the normal THS -> Tencent -> Baidu path after this batch step.
func (s *Service) prefillClosingDailyBarsBatch(ctx context.Context, instruments []StockV2Instrument, tradeDate string) (context.Context, dailyBarQuoteBatchResult, error) {
	state := dailyBarQuoteBatchState{
		tradeDate: strings.TrimSpace(tradeDate),
		attempted: make(map[string]struct{}, len(instruments)),
		bars:      make(map[string]StockV2DailyBar, len(instruments)),
	}
	if state.tradeDate == "" || s == nil || s.store == nil {
		return ctx, dailyBarQuoteBatchResult{}, nil
	}
	if _, err := time.Parse("2006-01-02", state.tradeDate); err != nil {
		return ctx, dailyBarQuoteBatchResult{}, fmt.Errorf("invalid closing quote trade date %q: %w", state.tradeDate, err)
	}
	previousState, hasPreviousState := ctx.Value(dailyBarQuoteBatchContextKey{}).(dailyBarQuoteBatchState)
	reusePreviousState := hasPreviousState && previousState.tradeDate != "" && previousState.tradeDate <= state.tradeDate
	if reusePreviousState {
		state.tradeDate = previousState.tradeDate
		state.calendarFrom = previousState.calendarFrom
		state.tradingDates = append([]string(nil), previousState.tradingDates...)
		state.probeFailed = previousState.probeFailed
	}

	specs := make([]quoteSymbol, 0, len(instruments))
	seen := make(map[string]struct{}, len(instruments))
	instrumentIndex := make(map[string]int, len(instruments))
	for i, instrument := range instruments {
		symbol, explicitMarket := normalizeQuoteSymbolInput(instrument.Symbol)
		if !isSixDigitSymbol(symbol) {
			continue
		}
		market := strings.ToUpper(strings.TrimSpace(instrument.Market))
		if market == "" {
			market = explicitMarket
		}
		if market == "" {
			market = inferAStockMarket(symbol)
		}
		if market == "" {
			continue
		}
		key := market + ":" + symbol
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		instrumentIndex[symbol] = i
		state.attempted[symbol] = struct{}{}
		specs = append(specs, quoteSymbol{
			Input:       instrument.Symbol,
			Symbol:      symbol,
			Market:      market,
			TencentCode: strings.ToLower(market) + symbol,
			EastmoneyID: eastmoneySecID(market, symbol),
		})
	}

	result := dailyBarQuoteBatchResult{Attempted: len(specs)}
	if len(specs) == 0 {
		return context.WithValue(ctx, dailyBarQuoteBatchContextKey{}, state), result, nil
	}

	probeEnd := 0
	var probeQuotes []StockV2QuoteLatest
	if !reusePreviousState {
		probeEnd = min(len(specs), 80)
		var probeFailures []UpdateFailure
		probeQuotes, probeFailures = s.fetchLatestQuotesForSpecsWithFailures(ctx, specs[:probeEnd])
		result.Fetched = len(probeQuotes)
		result.Failed = len(probeFailures)
		actualTradeDate := closingQuoteDateMode(probeQuotes, state.tradeDate)
		if actualTradeDate != "" {
			state.tradeDate = actualTradeDate
		} else {
			state.probeFailed = true
		}
		// One independent index K-line request per batch prevents a market-wide
		// stock-source outage from erasing that trading day from the observed calendar.
		// Failure is intentionally non-fatal; loadObservedTradingCalendar remains the fallback.
		_ = s.refreshReferenceTradingCalendar(ctx, state.tradeDate)
	}
	if state.probeFailed {
		// The two batch providers were already attempted. Preserve the attempted
		// state so the caller never degrades this current-day check into N requests.
		result.Failed = len(specs)
		if !reusePreviousState {
			if err := s.loadObservedTradingCalendar(ctx, &state); err != nil {
				return ctx, result, err
			}
		}
		return context.WithValue(ctx, dailyBarQuoteBatchContextKey{}, state), result, nil
	}
	symbols := make([]string, 0, len(specs))
	for _, spec := range specs {
		symbols = append(symbols, spec.Symbol)
	}
	covered, err := s.store.GetDailyBarSymbolCoverage(ctx, symbols, DailyBarAdjustedNone, state.tradeDate)
	if err != nil {
		return ctx, result, err
	}
	addQuotes := func(quotes []StockV2QuoteLatest) {
		for _, quote := range quotes {
			if index, ok := instrumentIndex[quote.Symbol]; ok && strings.TrimSpace(quote.Name) != "" {
				instruments[index].Name = strings.TrimSpace(quote.Name)
			}
			if covered[quote.Symbol] {
				continue
			}
			bar, ok := closingQuoteDailyBar(quote, state.tradeDate)
			if !ok {
				result.Rejected++
				continue
			}
			state.bars[bar.Symbol] = bar
		}
	}
	addQuotes(probeQuotes)

	remaining := make([]quoteSymbol, 0, len(specs)-probeEnd)
	for _, spec := range specs[probeEnd:] {
		if !covered[spec.Symbol] {
			remaining = append(remaining, spec)
		}
	}
	if len(remaining) > 0 {
		quotes, failures := s.fetchLatestQuotesForSpecsWithFailures(ctx, remaining)
		result.Fetched += len(quotes)
		result.Failed += len(failures)
		addQuotes(quotes)
	}
	required := 0
	for _, spec := range specs {
		if !covered[spec.Symbol] {
			required++
		}
	}
	if missing := required - len(state.bars); missing > result.Failed {
		result.Failed = missing
	}

	bars := make([]StockV2DailyBar, 0, len(state.bars))
	for _, bar := range state.bars {
		bars = append(bars, bar)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Symbol < bars[j].Symbol })
	if len(bars) > 0 {
		if err := s.store.UpsertDailyBars(ctx, bars); err != nil {
			return ctx, result, err
		}
	}
	if err := s.loadObservedTradingCalendar(ctx, &state); err != nil {
		return ctx, result, err
	}
	result.Upserted = len(bars)
	return context.WithValue(ctx, dailyBarQuoteBatchContextKey{}, state), result, nil
}

func (s *Service) refreshReferenceTradingCalendar(ctx context.Context, endDate string) error {
	if s == nil || s.store == nil || s.dailyBarsSource == nil {
		return nil
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return err
	}
	startDate := end.AddDate(-1, 0, -7).Format("2006-01-02")
	bars, err := s.dailyBarsSource.FetchDailyBars(
		ctx,
		dailyBarCalendarAnchorSymbol,
		"SH",
		startDate,
		endDate,
		DailyBarAdjustedNone,
		dailyBarCalendarAnchorCount,
	)
	if err != nil {
		return err
	}
	dates := make([]string, 0, len(bars))
	for _, bar := range bars {
		if bar.TradeDate >= startDate && bar.TradeDate <= endDate {
			dates = append(dates, bar.TradeDate)
		}
	}
	if len(dates) == 0 {
		return errors.New("Tencent index calendar returned no dates")
	}
	return s.store.UpsertObservedTradingDates(ctx, dates, time.Now())
}

func closingQuoteDateMode(quotes []StockV2QuoteLatest, maxDate string) string {
	counts := make(map[string]int)
	bestDate := ""
	bestCount := 0
	for _, quote := range quotes {
		if quote.QuoteAt.IsZero() {
			continue
		}
		tradeDate := quote.QuoteAt.In(chinaMarketTZ).Format("2006-01-02")
		if tradeDate == "" || tradeDate > maxDate {
			continue
		}
		counts[tradeDate]++
		if counts[tradeDate] > bestCount || (counts[tradeDate] == bestCount && tradeDate > bestDate) {
			bestDate = tradeDate
			bestCount = counts[tradeDate]
		}
	}
	return bestDate
}

func (s *Service) loadObservedTradingCalendar(ctx context.Context, state *dailyBarQuoteBatchState) error {
	calendarEnd, err := time.Parse("2006-01-02", state.tradeDate)
	if err != nil {
		return err
	}
	state.calendarFrom = calendarEnd.AddDate(-1, 0, -7).Format("2006-01-02")
	state.tradingDates, err = s.store.GetObservedTradingDates(ctx, state.calendarFrom, state.tradeDate)
	if err == nil && state.probeFailed && len(state.tradingDates) > 0 {
		latest := state.tradingDates[len(state.tradingDates)-1]
		latestDate, parseErr := time.Parse("2006-01-02", latest)
		if parseErr == nil && !latestDate.After(calendarEnd) && calendarEnd.Sub(latestDate) <= 14*24*time.Hour {
			state.tradeDate = latest
		}
	}
	return err
}

func closingQuoteDailyBar(quote StockV2QuoteLatest, tradeDate string) (StockV2DailyBar, bool) {
	if quote.Symbol == "" || quote.QuoteAt.IsZero() || quote.QuoteAt.In(chinaMarketTZ).Format("2006-01-02") != tradeDate {
		return StockV2DailyBar{}, false
	}
	if quote.LastPrice <= 0 || quote.OpenPrice <= 0 || quote.HighPrice <= 0 || quote.LowPrice <= 0 {
		return StockV2DailyBar{}, false
	}
	// A stale suspended quote can still expose the last price. Requiring actual
	// traded volume/amount prevents manufacturing a daily bar from that snapshot.
	if quote.Volume <= 0 && (!quote.amountPresent || quote.Amount <= 0) {
		return StockV2DailyBar{}, false
	}

	amount := quote.Amount
	source := dailyBarSourceEastmoneyClosingQuote
	if quote.Source == QuoteSourceTencent {
		// Tencent field 37 is reported in ten-thousand CNY; daily bars store CNY.
		amount *= 10_000
		source = dailyBarSourceTencentClosingQuote
	}
	netInflow := 0.0
	if quote.netInflowPresent {
		netInflow = quote.MainNetInflow + quote.MediumNetInflow + quote.SmallNetInflow
	}
	payload, _ := json.Marshal(map[string]any{
		"largeNetInflow":      quote.LargeNetInflow,
		"mediumNetInflow":     quote.MediumNetInflow,
		"smallNetInflow":      quote.SmallNetInflow,
		"superNetInflow":      quote.SuperNetInflow,
		"mainNetInflowPct":    quote.MainNetInflowPct,
		"orderFlowPresent":    quote.netInflowPresent,
		"turnoverRatePresent": quote.turnoverRatePresent,
	})
	quality := DailyBarQualityOK
	if !quote.amountPresent || !quote.turnoverRatePresent || !quote.mainNetInflowPresent || !quote.netInflowPresent {
		quality = DailyBarQualityPartial
	}
	fetchedAt := quote.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	return StockV2DailyBar{
		ID:                   generateID(),
		Symbol:               quote.Symbol,
		Market:               quote.Market,
		TradeDate:            tradeDate,
		Open:                 quote.OpenPrice,
		High:                 quote.HighPrice,
		Low:                  quote.LowPrice,
		Close:                quote.LastPrice,
		PrevClose:            quote.PrevClose,
		Volume:               quote.Volume,
		Amount:               amount,
		PctChange:            quote.PctChange,
		TurnoverRate:         quote.TurnoverRate,
		NetInflow:            netInflow,
		MainNetInflow:        quote.MainNetInflow,
		AmountPresent:        quote.amountPresent,
		TurnoverRatePresent:  quote.turnoverRatePresent,
		NetInflowPresent:     quote.netInflowPresent,
		MainNetInflowPresent: quote.mainNetInflowPresent,
		DataPayload:          string(payload),
		Adjusted:             DailyBarAdjustedNone,
		Source:               source,
		FetchedAt:            fetchedAt,
		Quality:              quality,
	}, true
}

func dailyBarQuoteBatchStatus(ctx context.Context, symbol, tradeDate string) (StockV2DailyBar, bool, bool) {
	state, ok := ctx.Value(dailyBarQuoteBatchContextKey{}).(dailyBarQuoteBatchState)
	if !ok || state.tradeDate != tradeDate {
		return StockV2DailyBar{}, false, false
	}
	_, attempted := state.attempted[symbol]
	bar, hasBar := state.bars[symbol]
	return bar, hasBar, attempted
}

func dailyBarBatchTargetDate(ctx context.Context, fallback string) string {
	state, ok := ctx.Value(dailyBarQuoteBatchContextKey{}).(dailyBarQuoteBatchState)
	if ok && state.tradeDate != "" && state.tradeDate <= fallback {
		return state.tradeDate
	}
	return fallback
}

func excludeBatchClosingQuoteRetry(ctx context.Context, symbol, tradeDate string, ranges []dailyBarMissingRange) []dailyBarMissingRange {
	state, stateOK := ctx.Value(dailyBarQuoteBatchContextKey{}).(dailyBarQuoteBatchState)
	_, _, attempted := dailyBarQuoteBatchStatus(ctx, symbol, tradeDate)
	if !attempted || len(ranges) == 0 {
		return ranges
	}
	out := ranges[:0]
	for _, missing := range ranges {
		// ponytail: a current-day-only miss was already checked through the batch
		// providers; retry it next run instead of exploding into per-symbol calls.
		if missing.Start == tradeDate && missing.End == tradeDate {
			continue
		}
		if stateOK && state.probeFailed && missing.End == tradeDate {
			parsedTradeDate, err := time.Parse("2006-01-02", tradeDate)
			if err != nil {
				out = append(out, missing)
				continue
			}
			missing.End = parsedTradeDate.AddDate(0, 0, -1).Format("2006-01-02")
			if missing.Start > missing.End {
				continue
			}
		}
		out = append(out, missing)
	}
	return out
}

func (s *Service) observedTradingDates(ctx context.Context, startDate, endDate string) ([]string, error) {
	if state, ok := ctx.Value(dailyBarQuoteBatchContextKey{}).(dailyBarQuoteBatchState); ok && state.calendarFrom <= startDate && state.tradeDate >= endDate {
		out := make([]string, 0, len(state.tradingDates))
		for _, tradeDate := range state.tradingDates {
			if tradeDate >= startDate && tradeDate <= endDate {
				out = append(out, tradeDate)
			}
		}
		return out, nil
	}
	return s.store.GetObservedTradingDates(ctx, startDate, endDate)
}
