package stockv2

import (
	"context"
	"errors"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	agentDailyBarsInitialDays = 365
	agentDailyBarsOverlapDays = 90
	// ponytail: Tencent fqkline drops the newest row for some symbols at count=400;
	// 365 still covers a full calendar year and was verified to retain the close.
	agentDailyBarsFetchLimit     = 365
	agentDailyBarsRequestTimeout = 12 * time.Second
	agentDailyBarsRetryCooldown  = 10 * time.Minute
	agentDailyBarsRequestSpacing = 500 * time.Millisecond
	agentDailyBarsContextLimit   = 60
	agentDailyBarsRecentLimit    = 8
)

const (
	dailyBarsCoverageFresh              = "fresh"
	dailyBarsCoverageFreshPreviousClose = "fresh_previous_close"
	dailyBarsCoverageFreshLatest        = "fresh_latest_available"
	dailyBarsCoverageSourceLagging      = "source_lagging"
	dailyBarsCoverageRefreshFailed      = "refresh_failed"
	dailyBarsCoverageMissing            = "missing"
)

type agentDailyBarsFailure struct {
	RetryAfter    time.Time
	Message       string
	SourceLagging bool
}

type agentDailyBarsRefresh struct {
	Attempted     bool
	Error         string
	SourceLagging bool
}

func (s *Service) buildDailyBarsContext(ctx context.Context, symbol string) *DailyBarsContext {
	return s.buildDailyBarsContextAt(ctx, symbol, DailyBarAdjustedQFQ, time.Now())
}

func (s *Service) buildDailyBarsContextAt(ctx context.Context, symbol, adjusted string, now time.Time) *DailyBarsContext {
	symbol, _ = normalizeQuoteSymbolInput(symbol)
	adjusted = normalizeAgentDailyBarAdjusted(adjusted)
	now = now.In(chinaMarketTZ)
	completedEnd, postClose := agentDailyBarsCompletedEnd(now)
	quote := s.latestAgentDailyBarsQuote(ctx, symbol)
	currentSession := quote != nil && sameChinaMarketDate(quote.QuoteAt, now)

	refresh := s.ensureAgentDailyBars(ctx, symbol, adjusted, completedEnd, currentSession, postClose, now)
	bars, err := s.store.GetDailyBars(ctx, symbol, adjusted, "", completedEnd, agentDailyBarsContextLimit)
	if err != nil {
		if refresh.Error == "" {
			refresh.Error = safelog.Error(err, 240)
		}
		bars = nil
	}

	out := &DailyBarsContext{
		Symbol:                   symbol,
		Adjusted:                 adjusted,
		CheckedAt:                now,
		RefreshAttempted:         refresh.Attempted,
		CurrentSessionIncomplete: currentSession && !postClose,
		RefreshError:             refresh.Error,
	}
	if len(bars) == 0 {
		out.CoverageStatus = dailyBarsCoverageMissing
		return out
	}

	latest := bars[len(bars)-1]
	out.Count = len(bars)
	out.LatestTradeDate = latest.TradeDate
	out.LatestClose = latest.Close
	out.LatestFetchedAt = latest.FetchedAt
	out.Quality = latest.Quality
	out.Summary = dailyBarsContextSummary(bars)
	out.RecentBars = dailyBarEvidencePoints(bars, agentDailyBarsRecentLimit)
	features := calculateDecisionBarFeatures(bars)
	if features.Valid {
		structure := marketStructureEvidence(features)
		out.MarketStructure = &structure
	}
	out.CoverageStatus = agentDailyBarsCoverageStatus(latest.TradeDate, refresh.Error, refresh.SourceLagging, currentSession, postClose, now)
	return out
}

func (s *Service) ensureAgentDailyBars(
	ctx context.Context,
	symbol, adjusted, completedEnd string,
	currentSession, postClose bool,
	now time.Time,
) agentDailyBarsRefresh {
	if symbol == "" || s == nil || s.store == nil {
		return agentDailyBarsRefresh{Error: "daily bar symbol or store unavailable"}
	}

	s.agentDailyBarsMu.Lock()
	defer s.agentDailyBarsMu.Unlock()

	latestBars, err := s.store.GetDailyBars(ctx, symbol, adjusted, "", completedEnd, 1)
	if err != nil {
		return agentDailyBarsRefresh{Error: safelog.Error(err, 240)}
	}
	if agentDailyBarsVerified(latestBars, currentSession, postClose, now) {
		return agentDailyBarsRefresh{}
	}

	phase := agentDailyBarsPhase(currentSession, postClose)
	failureKey := symbol + "\x00" + adjusted + "\x00" + phase
	if failure, ok := s.agentDailyBarsFailures[failureKey]; ok && now.Before(failure.RetryAfter) {
		return agentDailyBarsRefresh{Error: failure.Message, SourceLagging: failure.SourceLagging}
	}
	if s.dailyBarsSource == nil || s.dailyBarsSource.httpClient == nil {
		message := "daily bar public source unavailable"
		s.agentDailyBarsFailures[failureKey] = agentDailyBarsFailure{
			RetryAfter: now.Add(agentDailyBarsRetryCooldown),
			Message:    message,
		}
		return agentDailyBarsRefresh{Error: message}
	}

	requestCtx, cancel := context.WithTimeout(ctx, agentDailyBarsRequestTimeout)
	defer cancel()
	if err := s.waitForAgentDailyBarsRequest(requestCtx); err != nil {
		message := safelog.Error(err, 240)
		s.agentDailyBarsFailures[failureKey] = agentDailyBarsFailure{
			RetryAfter: now.Add(agentDailyBarsRetryCooldown),
			Message:    message,
		}
		return agentDailyBarsRefresh{Attempted: true, Error: message}
	}

	rowCount, _, _, _, _, err := s.store.GetDailyBarsStats(requestCtx, symbol, adjusted)
	if err != nil {
		message := safelog.Error(err, 240)
		s.agentDailyBarsFailures[failureKey] = agentDailyBarsFailure{
			RetryAfter: now.Add(agentDailyBarsRetryCooldown),
			Message:    message,
		}
		return agentDailyBarsRefresh{Attempted: true, Error: message}
	}
	startDays := agentDailyBarsOverlapDays
	if rowCount < dailyBarsAgentTarget {
		startDays = agentDailyBarsInitialDays
	}
	start := now.AddDate(0, 0, -startDays).Format("2006-01-02")
	market := s.agentDailyBarsMarket(requestCtx, symbol)
	s.agentDailyBarsLastRequest = time.Now()
	bars, err := s.dailyBarsSource.FetchDailyBars(requestCtx, symbol, market, start, completedEnd, adjusted, agentDailyBarsFetchLimit)
	if err == nil {
		bars = completedDailyBarsOnly(bars, completedEnd, now)
		if len(bars) == 0 {
			err = errors.New("daily bar source returned no completed bars")
		} else {
			for i := range bars {
				bars[i].Symbol = symbol
			}
		}
	}
	if err == nil {
		err = s.store.UpsertDailyBars(requestCtx, bars)
	}
	if err != nil {
		message := safelog.Error(err, 240)
		s.agentDailyBarsFailures[failureKey] = agentDailyBarsFailure{
			RetryAfter: now.Add(agentDailyBarsRetryCooldown),
			Message:    message,
		}
		if s.log != nil {
			s.log.Warn("stockv2 agent daily bars refresh failed",
				"symbol", symbol,
				"adjusted", adjusted,
				"phase", phase,
				"error", message,
			)
		}
		return agentDailyBarsRefresh{Attempted: true, Error: message}
	}

	if currentSession && postClose {
		latest, readErr := s.store.GetDailyBars(requestCtx, symbol, adjusted, "", completedEnd, 1)
		if readErr != nil {
			message := safelog.Error(readErr, 240)
			s.agentDailyBarsFailures[failureKey] = agentDailyBarsFailure{
				RetryAfter: now.Add(agentDailyBarsRetryCooldown),
				Message:    message,
			}
			return agentDailyBarsRefresh{Attempted: true, Error: message}
		}
		today := now.In(chinaMarketTZ).Format("2006-01-02")
		if len(latest) == 0 || latest[len(latest)-1].TradeDate < today {
			message := "daily bar source has not published the latest completed session"
			s.agentDailyBarsFailures[failureKey] = agentDailyBarsFailure{
				RetryAfter:    now.Add(agentDailyBarsRetryCooldown),
				Message:       message,
				SourceLagging: true,
			}
			return agentDailyBarsRefresh{Attempted: true, Error: message, SourceLagging: true}
		}
	}

	delete(s.agentDailyBarsFailures, failureKey)
	return agentDailyBarsRefresh{Attempted: true}
}

func (s *Service) waitForAgentDailyBarsRequest(ctx context.Context) error {
	wait := agentDailyBarsRequestSpacing - time.Since(s.agentDailyBarsLastRequest)
	if s.agentDailyBarsLastRequest.IsZero() || wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) latestAgentDailyBarsQuote(ctx context.Context, symbol string) *StockV2QuoteLatest {
	quotes, err := s.store.GetLatestQuotes(ctx, []string{symbol})
	if err != nil || len(quotes) == 0 {
		return nil
	}
	return &quotes[0]
}

func (s *Service) agentDailyBarsMarket(ctx context.Context, symbol string) string {
	if instrument, err := s.store.GetInstrument(ctx, symbol); err == nil {
		return instrument.Market
	}
	market, _ := inferInstrumentMarketAndType(symbol)
	return market
}

func agentDailyBarsCompletedEnd(now time.Time) (string, bool) {
	now = now.In(chinaMarketTZ)
	postClose := now.Hour() > 15 || now.Hour() == 15 && now.Minute() >= 10
	end := now
	if !postClose {
		end = now.AddDate(0, 0, -1)
	}
	return end.Format("2006-01-02"), postClose
}

func agentDailyBarsVerified(bars []StockV2DailyBar, currentSession, postClose bool, now time.Time) bool {
	if len(bars) == 0 {
		return false
	}
	latest := bars[len(bars)-1]
	if !sameChinaMarketDate(latest.FetchedAt, now) {
		return false
	}
	if currentSession && postClose && latest.TradeDate < now.In(chinaMarketTZ).Format("2006-01-02") {
		return false
	}
	return true
}

func agentDailyBarsCoverageStatus(latestDate, refreshError string, sourceLagging, currentSession, postClose bool, now time.Time) string {
	if sourceLagging {
		return dailyBarsCoverageSourceLagging
	}
	if strings.TrimSpace(refreshError) != "" {
		return dailyBarsCoverageRefreshFailed
	}
	if currentSession && !postClose {
		return dailyBarsCoverageFreshPreviousClose
	}
	if currentSession && latestDate < now.In(chinaMarketTZ).Format("2006-01-02") {
		return dailyBarsCoverageSourceLagging
	}
	if !currentSession {
		return dailyBarsCoverageFreshLatest
	}
	return dailyBarsCoverageFresh
}

func agentDailyBarsPhase(currentSession, postClose bool) string {
	if currentSession && postClose {
		return "post_close"
	}
	if currentSession {
		return "intraday"
	}
	return "latest_available"
}

func sameChinaMarketDate(left, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return false
	}
	return left.In(chinaMarketTZ).Format("2006-01-02") == right.In(chinaMarketTZ).Format("2006-01-02")
}

func normalizeAgentDailyBarAdjusted(adjusted string) string {
	adjusted = strings.TrimSpace(adjusted)
	if adjusted == "" {
		return DailyBarAdjustedQFQ
	}
	if !isValidDailyBarAdjusted(adjusted) {
		return DailyBarAdjustedQFQ
	}
	return adjusted
}

func completedDailyBarsOnly(bars []StockV2DailyBar, completedEnd string, fetchedAt time.Time) []StockV2DailyBar {
	out := bars[:0]
	for _, bar := range bars {
		if bar.TradeDate == "" || bar.TradeDate > completedEnd {
			continue
		}
		bar.FetchedAt = fetchedAt
		out = append(out, bar)
	}
	return out
}

func dailyBarsContextSummary(bars []StockV2DailyBar) map[string]float64 {
	if len(bars) == 0 {
		return nil
	}
	latest := bars[len(bars)-1]
	high := bars[0].High
	low := bars[0].Low
	for _, bar := range bars {
		if bar.High > high {
			high = bar.High
		}
		if bar.Low < low {
			low = bar.Low
		}
	}
	out := map[string]float64{
		"latestClose": latest.Close,
		"rangeHigh":   high,
		"rangeLow":    low,
	}
	features := calculateDecisionBarFeatures(bars)
	if features.Valid {
		out["return3Pct"] = features.Return3Pct
		out["return5Pct"] = features.Return5Pct
		out["return20Pct"] = features.Return20Pct
		out["volumeRatio3ToPrior"] = features.VolumeRatio3ToPrior
		out["latestCloseLocationPct"] = features.LatestCloseLocationPct
		out["lowCloseDays3"] = float64(features.LowCloseDays3)
	}
	return out
}

func dailyBarEvidencePoints(bars []StockV2DailyBar, limit int) []DailyBarEvidencePoint {
	if limit <= 0 || len(bars) == 0 {
		return nil
	}
	start := max(0, len(bars)-limit)
	out := make([]DailyBarEvidencePoint, 0, len(bars)-start)
	for _, bar := range bars[start:] {
		out = append(out, DailyBarEvidencePoint{
			TradeDate: bar.TradeDate, Open: bar.Open, High: bar.High, Low: bar.Low,
			Close: bar.Close, PrevClose: bar.PrevClose, Volume: bar.Volume,
			Amount: bar.Amount, PctChange: bar.PctChange,
		})
	}
	return out
}
