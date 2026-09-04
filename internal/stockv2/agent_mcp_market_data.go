package stockv2

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) mcpGetDailyBars(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol, Adjusted, StartDate, EndDate string
		Limit                                int
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	p.Symbol = strings.TrimSpace(p.Symbol)
	if !isSixDigitSymbol(p.Symbol) || !validMCPDate(p.StartDate) || !validMCPDate(p.EndDate) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "symbol and YYYY-MM-DD date range are invalid"}
	}
	if p.Adjusted = strings.TrimSpace(p.Adjusted); p.Adjusted == "" {
		p.Adjusted = DailyBarAdjustedQFQ
	}
	if !isValidDailyBarAdjusted(p.Adjusted) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "adjusted must be none, qfq, or hfq"}
	}
	limit := mcpBoundedLimit(p.Limit, 60, 250)
	ctx := contextFromMCP()
	coverage := s.buildDailyBarsContextAt(ctx, p.Symbol, p.Adjusted, time.Now())
	bars, err := s.store.GetDailyBars(ctx, p.Symbol, p.Adjusted, strings.TrimSpace(p.StartDate), strings.TrimSpace(p.EndDate), limit)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(map[string]any{
		"symbol": p.Symbol, "adjusted": p.Adjusted, "count": len(bars),
		"priceSemantics": dailyBarPriceSemantics(p.Adjusted), "coverage": coverage,
		"bars": dailyBarEvidencePoints(bars, len(bars)),
	}), nil
}

func (s *Service) mcpGetMinuteBars(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol string `json:"symbol"`
		Days   int    `json:"days"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	p.Symbol = strings.TrimSpace(p.Symbol)
	if !isSixDigitSymbol(p.Symbol) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "valid symbol is required"}
	}
	p.Days = mcpBoundedLimit(p.Days, 1, 5)
	limit := mcpBoundedLimit(p.Limit, 240, 1200)
	// List the bounded retained window, then take its newest tail because the
	// repository deliberately returns chart-friendly ascending rows.
	items, err := s.ListMinuteBars(contextFromMCP(), p.Symbol, p.Days, p.Days*240)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return mcpJSONResult(map[string]any{"symbol": p.Symbol, "days": p.Days, "count": len(items), "bars": items}), nil
}

func (s *Service) mcpGetQuoteHistory(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol string `json:"symbol"`
		Hours  int    `json:"hours"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	p.Symbol = strings.TrimSpace(p.Symbol)
	if !isSixDigitSymbol(p.Symbol) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "valid symbol is required"}
	}
	p.Hours = mcpBoundedLimit(p.Hours, 8, 120)
	items, err := s.store.ListRecentQuoteSnapshots(contextFromMCP(), p.Symbol,
		time.Now().Add(-time.Duration(p.Hours)*time.Hour), mcpBoundedLimit(p.Limit, 240, 500))
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"quoteAt": item.QuoteAt, "collectedAt": item.CollectedAt, "lastPrice": item.LastPrice,
			"prevClose": item.PrevClose, "open": item.OpenPrice, "high": item.HighPrice, "low": item.LowPrice,
			"volume": item.Volume, "amount": item.Amount, "pctChange": item.PctChange,
			"amplitude": item.Amplitude, "turnoverRate": item.TurnoverRate, "volumeRatio": item.VolumeRatio,
			"mainNetInflow": item.MainNetInflow, "superNetInflow": item.SuperNetInflow,
			"largeNetInflow": item.LargeNetInflow, "mediumNetInflow": item.MediumNetInflow,
			"smallNetInflow": item.SmallNetInflow, "mainNetInflowPct": item.MainNetInflowPct,
			"source": item.Source, "status": item.Status,
		})
	}
	return mcpJSONResult(map[string]any{"symbol": p.Symbol, "hours": p.Hours, "count": len(out), "quotes": out}), nil
}

func (s *Service) mcpGetFundFlowHistory(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol, Market, StartDate, EndDate string
		Limit                              int
		Refresh                            bool
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	p.Symbol = strings.TrimSpace(p.Symbol)
	p.Market = strings.ToUpper(strings.TrimSpace(p.Market))
	if !isSixDigitSymbol(p.Symbol) || !validMCPDate(p.StartDate) || !validMCPDate(p.EndDate) {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "symbol and YYYY-MM-DD date range are invalid"}
	}
	if instrument, err := s.store.GetInstrument(contextFromMCP(), p.Symbol); err == nil {
		p.Market = firstNonEmpty(p.Market, instrument.Market)
		if instrument.InstrumentType == InstrumentTypeExchangeFund {
			return mcpJSONResult(map[string]any{"symbol": p.Symbol, "status": DecisionHealthNotApplicable, "message": "exchange funds do not use the A-share per-stock moneyflow dataset", "points": []any{}}), nil
		}
	}
	if p.Market != "SH" && p.Market != "SZ" {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "market must resolve to SH or SZ"}
	}
	limit := mcpBoundedLimit(p.Limit, 60, 120)
	endDate := strings.TrimSpace(p.EndDate)
	if endDate == "" {
		endDate = time.Now().In(chinaMarketTZ).Format("2006-01-02")
	}
	ctx := contextFromMCP()
	points, err := s.store.ListDecisionFundFlowPoints(ctx, p.Symbol, p.StartDate, endDate, limit)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	requiredAsOf := ""
	if raw := s.decisionLatestRawBar(ctx, p.Symbol, endDate); raw != nil {
		requiredAsOf = raw.TradeDate
	}
	refreshNeeded := p.Refresh || len(points) == 0 || (requiredAsOf != "" && points[len(points)-1].TradeDate < requiredAsOf)
	refreshError := ""
	if refreshNeeded {
		config, configErr := s.store.GetOpportunityMarketScanConfig(ctx)
		if configErr != nil {
			refreshError = safelog.Error(configErr, 240)
		} else if fetched, fetchErr := s.fetchOpportunityMarketFundFlow(ctx, config, p.Symbol, p.Market, endDate, mcpFundFlowFetchLimit(limit)); fetchErr != nil {
			refreshError = safelog.Error(fetchErr, 240)
		} else {
			fetchedAt := time.Now()
			if saveErr := s.store.UpsertDecisionFundFlowPoints(ctx, p.Symbol, p.Market, fetched.Source, fetched.Points, fetchedAt); saveErr != nil {
				return nil, mcpErrorFromError(saveErr)
			}
			var metrics OpportunityMarketScanMetrics
			applyOpportunityFundFlow(&metrics, fetched.Points, fetched.Source)
			if saveErr := s.store.UpsertDecisionFundFlowEvidence(ctx,
				decisionFundFlowEvidenceFromMetrics(p.Symbol, p.Market, metrics)); saveErr != nil {
				return nil, mcpErrorFromError(saveErr)
			}
			points, err = s.store.ListDecisionFundFlowPoints(ctx, p.Symbol, p.StartDate, endDate, limit)
			if err != nil {
				return nil, mcpErrorFromError(err)
			}
		}
	}
	aggregate, aggregateErr := s.store.GetDecisionFundFlowEvidence(ctx, p.Symbol)
	if aggregateErr != nil && !errors.Is(aggregateErr, sql.ErrNoRows) {
		return nil, mcpErrorFromError(aggregateErr)
	}
	status := DecisionHealthHealthy
	if len(points) == 0 || refreshError != "" {
		status = DecisionHealthDegraded
	}
	return mcpJSONResult(map[string]any{
		"symbol": p.Symbol, "market": p.Market, "status": status, "requiredAsOf": requiredAsOf,
		"refreshAttempted": refreshNeeded, "refreshError": refreshError, "aggregate": decisionFundFlowEvidenceMap(aggregate),
		"count": len(points), "points": points,
	}), nil
}

func (s *Service) mcpGetDecisionEvidence(args json.RawMessage) (any, *mcpError) {
	var p struct {
		Symbol, AsOf, ContextType, ContextID string
		FinancialLimit, EventLimit           int
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	p.Symbol = strings.TrimSpace(p.Symbol)
	if !isSixDigitSymbol(p.Symbol) || !validMCPDate(p.AsOf) || (strings.TrimSpace(p.ContextID) != "" && strings.TrimSpace(p.ContextType) == "") {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "symbol, asOf, or decision context is invalid"}
	}
	if p.AsOf = strings.TrimSpace(p.AsOf); p.AsOf == "" {
		p.AsOf = time.Now().In(chinaMarketTZ).Format("2006-01-02")
	}
	ctx := contextFromMCP()
	var snapshot DecisionGateSnapshot
	var snapshotErr error
	if strings.TrimSpace(p.ContextID) != "" {
		snapshot, snapshotErr = s.store.GetLatestDecisionGateSnapshot(ctx, p.ContextType, p.ContextID, p.Symbol)
	} else {
		snapshot, snapshotErr = s.store.GetLatestDecisionGateSnapshotForSymbol(ctx, p.Symbol)
	}
	facts, err := s.store.ListDecisionFinancialFacts(ctx, p.Symbol, p.AsOf, mcpBoundedLimit(p.FinancialLimit, 12, 30))
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	asOf, _ := time.ParseInLocation("2006-01-02", p.AsOf, chinaMarketTZ)
	events, err := s.store.ListDecisionMarketEvents(ctx, p.Symbol,
		asOf.AddDate(0, -6, 0).Format("2006-01-02"), asOf.AddDate(0, 1, 0).Format("2006-01-02"), p.AsOf)
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	eventLimit := mcpBoundedLimit(p.EventLimit, 20, 50)
	if len(events) > eventLimit {
		events = events[len(events)-eventLimit:]
	}
	health, healthErr := s.store.GetDecisionReferenceHealth(ctx, p.Symbol)
	flow, flowErr := s.store.GetDecisionFundFlowEvidence(ctx, p.Symbol)
	return mcpJSONResult(map[string]any{
		"symbol": p.Symbol, "asOf": p.AsOf,
		"decisionGate":    optionalDecisionGate(snapshot, snapshotErr),
		"referenceHealth": optionalDecisionReferenceHealth(health, healthErr),
		"financialFacts":  decisionFinancialFactMaps(facts), "marketEvents": decisionMarketEventMaps(events),
		"fundFlowAggregate": optionalDecisionFundFlow(flow, flowErr),
	}), nil
}

func (s *Service) mcpGetMarketScanCandidates(args json.RawMessage) (any, *mcpError) {
	var p struct {
		RunID, Symbol, Stage string
		Limit                int
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	ctx := contextFromMCP()
	p.RunID = strings.TrimSpace(p.RunID)
	var run *OpportunityMarketScanRun
	if p.RunID == "" {
		runs, err := s.store.ListOpportunityMarketScanRuns(ctx, OpportunityMarketScanRunListFilter{Limit: 1})
		if err != nil {
			return nil, mcpErrorFromError(err)
		}
		if len(runs) == 0 {
			return mcpJSONResult(map[string]any{"count": 0, "items": []any{}}), nil
		}
		run = &runs[0]
		p.RunID = run.ID
	} else if item, err := s.store.GetOpportunityMarketScanRun(ctx, p.RunID); err != nil {
		return nil, mcpErrorFromError(err)
	} else {
		run = &item
	}
	items, err := s.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{
		ScanRunID: p.RunID, Symbol: strings.TrimSpace(p.Symbol), Stage: strings.TrimSpace(p.Stage),
		Limit: mcpBoundedLimit(p.Limit, 30, 100),
	})
	if err != nil {
		return nil, mcpErrorFromError(err)
	}
	return mcpJSONResult(map[string]any{"run": run, "count": len(items), "items": items}), nil
}

func dailyBarPriceSemantics(adjusted string) string {
	if adjusted == DailyBarAdjustedNone {
		return "unadjusted completed-session prices; eligible for daily-close reference after freshness checks"
	}
	return "adjusted trend series; never use these prices as executable thresholds"
}

func validMCPDate(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func mcpBoundedLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	return min(value, maximum)
}

func mcpFundFlowFetchLimit(responseLimit int) int {
	// ponytail: callers may request a short response, but 20/60-day aggregate
	// fields must never be recomputed from that truncated response window.
	return max(responseLimit, 60)
}

func decisionFundFlowEvidenceMap(item decisionFundFlowEvidence) map[string]any {
	if item.Symbol == "" {
		return nil
	}
	return map[string]any{
		"symbol": item.Symbol, "market": item.Market, "asOf": item.AsOf,
		"mainNetInflow5": item.MainNetInflow5, "mainNetInflow20": item.MainNetInflow20,
		"mainNetInflow60": item.MainNetInflow60, "mainFlowRatio20": item.MainFlowRatio20,
		"positiveFlowDays20": item.PositiveFlowDays20, "source": item.Source, "fetchedAt": item.FetchedAt,
	}
}

func optionalDecisionGate(item DecisionGateSnapshot, err error) any {
	if err != nil {
		return nil
	}
	return item
}

func optionalDecisionReferenceHealth(item decisionReferenceHealth, err error) any {
	if err != nil {
		return nil
	}
	return map[string]any{"symbol": item.Symbol, "status": item.Status, "source": item.Source,
		"message": item.Message, "eventAvailable": item.EventOK, "financialAvailable": item.FinanceOK, "checkedAt": item.CheckedAt}
}

func optionalDecisionFundFlow(item decisionFundFlowEvidence, err error) any {
	if err != nil {
		return nil
	}
	return decisionFundFlowEvidenceMap(item)
}

func decisionFinancialFactMaps(items []decisionFinancialFact) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"reportPeriod": item.ReportPeriod, "dataset": item.Dataset, "announcedAt": item.AnnouncedAt,
			"revenue": item.Revenue, "netProfit": item.NetProfit, "operatingCashFlow": item.OperatingCashFlow,
			"roe": item.ROE, "grossMargin": item.GrossMargin, "source": item.Source, "fetchedAt": item.FetchedAt,
		})
	}
	return out
}

func decisionMarketEventMaps(items []decisionMarketEvent) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"eventType": item.EventType, "eventDate": item.EventDate,
			"announcedAt": item.AnnouncedAt, "title": item.Title, "source": item.Source, "fetchedAt": item.FetchedAt})
	}
	return out
}
