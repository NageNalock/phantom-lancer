package stockv2

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	WatchRunStatusMatched    = "matched"
	WatchRunStatusNotMatched = "not_matched"
	WatchRunStatusSkipped    = "skipped"
	WatchRunStatusDegraded   = "degraded"

	WatchRulePriceAbove                = "price_above"
	WatchRulePriceBelow                = "price_below"
	WatchRulePriceBetween              = "price_between"
	WatchRulePctChangeAbove            = "pct_change_above"
	WatchRulePctChangeBelow            = "pct_change_below"
	WatchRuleQuoteStale                = "quote_stale"
	WatchRuleDailyCloseAbove           = "daily_close_above"
	WatchRuleDailyCloseBelow           = "daily_close_below"
	WatchRulePortfolioSymbolWeightOver = "portfolio_symbol_weight_above"
)

type WatchRunResult struct {
	WatchID     string            `json:"watchId"`
	Status      string            `json:"status"`
	Reason      string            `json:"reason,omitempty"`
	RuleResults []WatchRuleResult `json:"ruleResults"`
	Alert       *StockV2Alert     `json:"alert,omitempty"`
	CheckedAt   time.Time         `json:"checkedAt"`
}

type WatchRuleResult struct {
	RuleKey       string         `json:"ruleKey"`
	RuleType      string         `json:"ruleType"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason"`
	ObservedValue float64        `json:"observedValue,omitempty"`
	Threshold     any            `json:"threshold,omitempty"`
	Evidence      map[string]any `json:"evidence,omitempty"`
	DataTime      time.Time      `json:"dataTime,omitempty"`
}

type watchRule struct {
	Key           string
	Type          string
	Symbol        string
	PortfolioID   string
	Threshold     float64
	Low           float64
	High          float64
	MaxAgeSeconds int
}

func (s *Service) RunWatch(ctx context.Context, id string) (WatchRunResult, error) {
	watch, err := s.store.GetWatch(ctx, id)
	if err != nil {
		return WatchRunResult{}, err
	}
	checkedAt := time.Now()
	result := WatchRunResult{WatchID: watch.ID, CheckedAt: checkedAt}

	if skip := watchSkipReason(watch, checkedAt); skip != "" {
		result.Status = WatchRunStatusSkipped
		result.Reason = skip
		watch.LastCheckedAt = checkedAt
		watch.LastRunStatus = result.Status
		watch.LastRunReason = result.Reason
		_, err := s.store.UpdateWatch(ctx, watch)
		return result, err
	}

	rules := watchRulesFromConfig(watch)
	if len(rules) == 0 {
		result.Status = WatchRunStatusDegraded
		result.Reason = "no trigger rules configured"
		watch.LastCheckedAt = checkedAt
		watch.LastRunStatus = result.Status
		watch.LastRunReason = result.Reason
		_, err := s.store.UpdateWatch(ctx, watch)
		return result, err
	}

	for _, rule := range rules {
		result.RuleResults = append(result.RuleResults, s.evaluateWatchRule(ctx, watch, rule, checkedAt))
	}
	result.Status, result.Reason = aggregateWatchRules(watch.TriggerPolicy, result.RuleResults)

	watch.LastCheckedAt = checkedAt
	watch.LastRunStatus = result.Status
	watch.LastRunReason = result.Reason
	if _, err := s.store.UpdateWatch(ctx, watch); err != nil {
		return WatchRunResult{}, err
	}
	if result.Status == WatchRunStatusMatched {
		alert, err := s.CreateAlert(ctx, alertFromWatchRun(watch, result))
		if err != nil {
			return WatchRunResult{}, err
		}
		result.Alert = &alert
	}
	return result, nil
}

func watchSkipReason(watch StockV2Watch, now time.Time) string {
	switch watch.Status {
	case WatchStatusPaused:
		return "watch is paused"
	case WatchStatusArchived:
		return "watch is archived"
	}
	switch watch.ScheduleKind {
	case WatchScheduleManual, "":
		return ""
	case WatchScheduleDaily:
		loc := chinaMarketTZ
		if sameDayInLoc(watch.LastCheckedAt, now, loc) {
			return "daily schedule already checked today"
		}
	case WatchScheduleMarketSession:
		// ponytail: no holiday calendar here; exchange calendar belongs with future scheduler.
		n := now.In(chinaMarketTZ)
		if n.Weekday() == time.Saturday || n.Weekday() == time.Sunday {
			return "market session is closed"
		}
		minute := n.Hour()*60 + n.Minute()
		if minute < 9*60+30 || minute > 15*60 {
			return "market session is closed"
		}
	}
	return ""
}

func watchRulesFromConfig(watch StockV2Watch) []watchRule {
	rawRules, ok := watch.TriggerConfig["rules"].([]any)
	if !ok || len(rawRules) == 0 {
		if _, hasType := watch.TriggerConfig["type"]; hasType {
			rawRules = []any{watch.TriggerConfig}
		} else if _, hasType := watch.TriggerConfig["ruleType"]; hasType {
			rawRules = []any{watch.TriggerConfig}
		}
	}

	rules := make([]watchRule, 0, len(rawRules))
	for i, raw := range rawRules {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ruleType := firstRuleString(m, "type", "ruleType")
		key := firstRuleString(m, "key", "ruleKey")
		if key == "" {
			key = ruleType
		}
		rule := watchRule{
			Key:           key,
			Type:          ruleType,
			Symbol:        firstNonEmpty(firstRuleString(m, "symbol"), watch.Symbol),
			PortfolioID:   firstNonEmpty(firstRuleString(m, "portfolioId"), watch.PortfolioID),
			Threshold:     firstRuleNumber(m, "threshold", "value"),
			Low:           firstRuleNumber(m, "low", "lower", "min"),
			High:          firstRuleNumber(m, "high", "upper", "max"),
			MaxAgeSeconds: int(firstRuleNumber(m, "maxAgeSeconds")),
		}
		if rule.Key == "" {
			rule.Key = fmt.Sprintf("rule_%d", i+1)
		}
		rules = append(rules, rule)
	}
	return rules
}

func (s *Service) evaluateWatchRule(ctx context.Context, watch StockV2Watch, rule watchRule, now time.Time) WatchRuleResult {
	switch rule.Type {
	case WatchRulePriceAbove, WatchRulePriceBelow, WatchRulePriceBetween,
		WatchRulePctChangeAbove, WatchRulePctChangeBelow, WatchRuleQuoteStale:
		return s.evaluateQuoteRule(ctx, rule, now)
	case WatchRuleDailyCloseAbove, WatchRuleDailyCloseBelow:
		return s.evaluateDailyCloseRule(ctx, rule)
	case WatchRulePortfolioSymbolWeightOver:
		return s.evaluatePortfolioWeightRule(ctx, watch, rule)
	default:
		return ruleResult(rule, WatchRunStatusDegraded, "unsupported rule type", 0, rule.Threshold, nil, time.Time{})
	}
}

func (s *Service) evaluateQuoteRule(ctx context.Context, rule watchRule, now time.Time) WatchRuleResult {
	if rule.Symbol == "" {
		return ruleResult(rule, WatchRunStatusDegraded, "symbol is required", 0, rule.Threshold, nil, time.Time{})
	}
	quotes, err := s.store.GetLatestQuotes(ctx, []string{rule.Symbol})
	if err != nil || len(quotes) == 0 {
		return ruleResult(rule, WatchRunStatusDegraded, "latest quote is missing", 0, rule.Threshold, nil, time.Time{})
	}
	quote := quotes[0]
	dataTime := quote.QuoteAt
	if dataTime.IsZero() {
		dataTime = quote.FetchedAt
	}
	if rule.Type == WatchRuleQuoteStale {
		maxAge := time.Duration(rule.MaxAgeSeconds) * time.Second
		if maxAge <= 0 {
			maxAge = 30 * time.Minute
		}
		age := now.Sub(quote.FetchedAt)
		matched := quote.Status != QuoteStatusFresh || quote.FetchedAt.IsZero() || age > maxAge
		return boolRuleResult(rule, matched, age.Seconds(), maxAge.Seconds(), "quote stale check", quoteEvidence(quote), dataTime)
	}
	if quote.Status != QuoteStatusFresh || quote.LastPrice <= 0 {
		return ruleResult(rule, WatchRunStatusDegraded, "latest quote is not fresh", quote.LastPrice, rule.Threshold, quoteEvidence(quote), dataTime)
	}

	switch rule.Type {
	case WatchRulePriceAbove:
		return boolRuleResult(rule, quote.LastPrice > rule.Threshold, quote.LastPrice, rule.Threshold, fmt.Sprintf("price %.2f > %.2f", quote.LastPrice, rule.Threshold), quoteEvidence(quote), dataTime)
	case WatchRulePriceBelow:
		return boolRuleResult(rule, quote.LastPrice < rule.Threshold, quote.LastPrice, rule.Threshold, fmt.Sprintf("price %.2f < %.2f", quote.LastPrice, rule.Threshold), quoteEvidence(quote), dataTime)
	case WatchRulePriceBetween:
		threshold := map[string]float64{"low": rule.Low, "high": rule.High}
		return boolRuleResult(rule, quote.LastPrice >= rule.Low && quote.LastPrice <= rule.High, quote.LastPrice, threshold, "price between range", quoteEvidence(quote), dataTime)
	case WatchRulePctChangeAbove:
		return boolRuleResult(rule, quote.PctChange > rule.Threshold, quote.PctChange, rule.Threshold, fmt.Sprintf("pct change %.2f > %.2f", quote.PctChange, rule.Threshold), quoteEvidence(quote), dataTime)
	case WatchRulePctChangeBelow:
		return boolRuleResult(rule, quote.PctChange < rule.Threshold, quote.PctChange, rule.Threshold, fmt.Sprintf("pct change %.2f < %.2f", quote.PctChange, rule.Threshold), quoteEvidence(quote), dataTime)
	}
	return ruleResult(rule, WatchRunStatusDegraded, "unsupported quote rule", 0, rule.Threshold, quoteEvidence(quote), dataTime)
}

func (s *Service) evaluateDailyCloseRule(ctx context.Context, rule watchRule) WatchRuleResult {
	if rule.Symbol == "" {
		return ruleResult(rule, WatchRunStatusDegraded, "symbol is required", 0, rule.Threshold, nil, time.Time{})
	}
	bars, err := s.store.GetDailyBars(ctx, rule.Symbol, DailyBarAdjustedNone, "", "", 1)
	if err != nil || len(bars) == 0 {
		return ruleResult(rule, WatchRunStatusDegraded, "daily bar is missing", 0, rule.Threshold, nil, time.Time{})
	}
	bar := bars[len(bars)-1]
	dataTime := dailyBarDataTime(bar)
	if bar.Quality == "failed" || bar.Quality == "empty" || isDailyBarsStale(bar.TradeDate, time.Now()) {
		return ruleResult(rule, WatchRunStatusDegraded, "daily bar quality is not usable", bar.Close, rule.Threshold, dailyBarEvidence(bar), dataTime)
	}
	if rule.Type == WatchRuleDailyCloseAbove {
		return boolRuleResult(rule, bar.Close > rule.Threshold, bar.Close, rule.Threshold, fmt.Sprintf("daily close %.2f > %.2f", bar.Close, rule.Threshold), dailyBarEvidence(bar), dataTime)
	}
	return boolRuleResult(rule, bar.Close < rule.Threshold, bar.Close, rule.Threshold, fmt.Sprintf("daily close %.2f < %.2f", bar.Close, rule.Threshold), dailyBarEvidence(bar), dataTime)
}

func (s *Service) evaluatePortfolioWeightRule(ctx context.Context, watch StockV2Watch, rule watchRule) WatchRuleResult {
	portfolioID := firstNonEmpty(rule.PortfolioID, watch.PortfolioID)
	if portfolioID == "" || rule.Symbol == "" {
		return ruleResult(rule, WatchRunStatusDegraded, "portfolioId and symbol are required", 0, rule.Threshold, nil, time.Time{})
	}
	snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolioID, 1)
	if err != nil || len(snapshots) == 0 {
		return ruleResult(rule, WatchRunStatusDegraded, "portfolio snapshot is missing", 0, rule.Threshold, nil, time.Time{})
	}
	snapshot := snapshots[0]
	if snapshot.Status == PortfolioValuationStatusFailed || snapshot.TotalAssetValue <= 0 {
		return ruleResult(rule, WatchRunStatusDegraded, "portfolio snapshot is not usable", 0, rule.Threshold, portfolioEvidence(snapshot, nil), snapshot.ValuationAt)
	}
	holdings, err := s.store.ListHoldings(ctx, portfolioID)
	if err != nil {
		return ruleResult(rule, WatchRunStatusDegraded, "holdings are missing", 0, rule.Threshold, portfolioEvidence(snapshot, nil), snapshot.ValuationAt)
	}
	for _, holding := range holdings {
		if holding.Symbol != rule.Symbol {
			continue
		}
		weight := holding.PositionPct
		if weight <= 0 && holding.MarketValue > 0 {
			weight = holding.MarketValue / snapshot.TotalAssetValue * 100
		}
		return boolRuleResult(rule, weight > rule.Threshold, weight, rule.Threshold, fmt.Sprintf("position %.2f%% > %.2f%%", weight, rule.Threshold), portfolioEvidence(snapshot, &holding), snapshot.ValuationAt)
	}
	return boolRuleResult(rule, false, 0, rule.Threshold, "symbol is not held in portfolio", portfolioEvidence(snapshot, nil), snapshot.ValuationAt)
}

func aggregateWatchRules(policy string, results []WatchRuleResult) (string, string) {
	matched, degraded := 0, 0
	for _, result := range results {
		switch result.Status {
		case WatchRunStatusMatched:
			matched++
		case WatchRunStatusDegraded:
			degraded++
		}
	}
	if policy == WatchTriggerPolicyAll {
		if degraded > 0 {
			return WatchRunStatusDegraded, "one or more rules degraded"
		}
		if matched == len(results) {
			return WatchRunStatusMatched, "all rules matched"
		}
		return WatchRunStatusNotMatched, "not all rules matched"
	}
	if matched > 0 {
		return WatchRunStatusMatched, "one or more rules matched"
	}
	if degraded > 0 {
		return WatchRunStatusDegraded, "one or more rules degraded"
	}
	return WatchRunStatusNotMatched, "no rules matched"
}

func alertFromWatchRun(watch StockV2Watch, result WatchRunResult) RequestCreateAlert {
	rule := firstMatchedRule(result.RuleResults)
	dedupeKey := fmt.Sprintf("%s:%s:%s", watch.ID, rule.RuleKey, watchRunRuleSymbol(watch, rule))
	return RequestCreateAlert{
		WatchID:     watch.ID,
		Level:       AlertLevelWarning,
		Title:       alertTitleForRule(rule),
		Summary:     rule.Reason,
		DedupeKey:   dedupeKey,
		Evidence:    watchRunEvidence(result),
		TriggeredAt: result.CheckedAt,
	}
}

func firstMatchedRule(results []WatchRuleResult) WatchRuleResult {
	for _, result := range results {
		if result.Status == WatchRunStatusMatched {
			return result
		}
	}
	return WatchRuleResult{}
}

func ruleResult(rule watchRule, status, reason string, observed float64, threshold any, evidence map[string]any, dataTime time.Time) WatchRuleResult {
	if evidence == nil {
		evidence = map[string]any{}
	}
	return WatchRuleResult{
		RuleKey:       rule.Key,
		RuleType:      rule.Type,
		Status:        status,
		Reason:        reason,
		ObservedValue: observed,
		Threshold:     threshold,
		Evidence:      evidence,
		DataTime:      dataTime,
	}
}

func boolRuleResult(rule watchRule, matched bool, observed float64, threshold any, reason string, evidence map[string]any, dataTime time.Time) WatchRuleResult {
	status := WatchRunStatusNotMatched
	if matched {
		status = WatchRunStatusMatched
	}
	return ruleResult(rule, status, reason, observed, threshold, evidence, dataTime)
}

func firstRuleString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func firstRuleNumber(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch value := m[key].(type) {
		case float64:
			return value
		case int:
			return float64(value)
		case jsonNumber:
			n, _ := value.Float64()
			return n
		}
	}
	return 0
}

type jsonNumber interface {
	Float64() (float64, error)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func quoteEvidence(quote StockV2QuoteLatest) map[string]any {
	return map[string]any{
		"symbol":    quote.Symbol,
		"market":    quote.Market,
		"lastPrice": quote.LastPrice,
		"pctChange": quote.PctChange,
		"status":    quote.Status,
		"source":    quote.Source,
		"quoteAt":   quote.QuoteAt,
		"fetchedAt": quote.FetchedAt,
	}
}

func dailyBarEvidence(bar StockV2DailyBar) map[string]any {
	return map[string]any{
		"symbol":    bar.Symbol,
		"tradeDate": bar.TradeDate,
		"close":     bar.Close,
		"quality":   bar.Quality,
		"source":    bar.Source,
		"fetchedAt": bar.FetchedAt,
	}
}

func portfolioEvidence(snapshot PortfolioSnapshot, holding *StockV2Holding) map[string]any {
	evidence := map[string]any{
		"portfolioId":     snapshot.PortfolioID,
		"snapshotId":      snapshot.ID,
		"valuationAt":     snapshot.ValuationAt,
		"totalAssetValue": snapshot.TotalAssetValue,
		"status":          snapshot.Status,
	}
	if holding != nil {
		evidence["symbol"] = holding.Symbol
		evidence["positionPct"] = holding.PositionPct
		evidence["marketValue"] = holding.MarketValue
	}
	return evidence
}

func dailyBarDataTime(bar StockV2DailyBar) time.Time {
	if bar.FetchedAt.IsZero() {
		return time.Now()
	}
	return bar.FetchedAt
}

func alertTitleForRule(rule WatchRuleResult) string {
	switch rule.RuleType {
	case WatchRulePriceAbove:
		return fmt.Sprintf("价格突破 %.2f", thresholdFloat(rule.Threshold))
	case WatchRulePriceBelow:
		return fmt.Sprintf("价格跌破 %.2f", thresholdFloat(rule.Threshold))
	case WatchRulePctChangeAbove:
		return fmt.Sprintf("涨跌幅高于 %.2f%%", thresholdFloat(rule.Threshold))
	case WatchRulePctChangeBelow:
		return fmt.Sprintf("涨跌幅低于 %.2f%%", thresholdFloat(rule.Threshold))
	case WatchRuleDailyCloseAbove:
		return fmt.Sprintf("日收盘价突破 %.2f", thresholdFloat(rule.Threshold))
	case WatchRuleDailyCloseBelow:
		return fmt.Sprintf("日收盘价跌破 %.2f", thresholdFloat(rule.Threshold))
	case WatchRulePortfolioSymbolWeightOver:
		return fmt.Sprintf("仓位占比超过 %.2f%%", thresholdFloat(rule.Threshold))
	case WatchRuleQuoteStale:
		return "行情数据过期"
	default:
		return "监控规则命中"
	}
}

func thresholdFloat(value any) float64 {
	v, _ := value.(float64)
	return v
}

func watchRunRuleSymbol(watch StockV2Watch, rule WatchRuleResult) string {
	if symbol, ok := rule.Evidence["symbol"].(string); ok && symbol != "" {
		return symbol
	}
	return watch.Symbol
}

func watchRunEvidence(result WatchRunResult) map[string]any {
	return map[string]any{
		"watchId":     result.WatchID,
		"status":      result.Status,
		"reason":      result.Reason,
		"checkedAt":   result.CheckedAt,
		"ruleResults": result.RuleResults,
	}
}
