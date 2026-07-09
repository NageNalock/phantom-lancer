package stockv2

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	MonitorRuleStatusMatched    = "matched"
	MonitorRuleStatusNotMatched = "not_matched"
	MonitorRuleStatusDegraded   = "degraded"

	MonitorRulePriceAbove                 = "price_above"
	MonitorRulePriceBelow                 = "price_below"
	MonitorRulePriceBetween               = "price_between"
	MonitorRulePctChangeAbove             = "pct_change_above"
	MonitorRulePctChangeBelow             = "pct_change_below"
	MonitorRuleQuoteStale                 = "quote_stale"
	MonitorRuleDailyCloseAbove            = "daily_close_above"
	MonitorRuleDailyCloseBelow            = "daily_close_below"
	MonitorRulePortfolioSymbolWeightOver  = "portfolio_symbol_weight_above"
	MonitorRulePortfolioSymbolWeightBelow = "portfolio_symbol_weight_below"
)

type MonitorRuleResult struct {
	RuleKey       string         `json:"ruleKey"`
	RuleType      string         `json:"ruleType"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason"`
	ObservedValue float64        `json:"observedValue,omitempty"`
	Threshold     any            `json:"threshold,omitempty"`
	Evidence      map[string]any `json:"evidence,omitempty"`
	DataTime      time.Time      `json:"dataTime,omitempty"`
}

type monitorRule struct {
	Key           string
	Type          string
	Symbol        string
	PortfolioID   string
	Threshold     float64
	Low           float64
	High          float64
	MaxAgeSeconds int
}

func monitorRulesFromConfig(config map[string]any, symbol, portfolioID string) []monitorRule {
	rawRules, ok := config["rules"].([]any)
	if !ok || len(rawRules) == 0 {
		if _, hasType := config["type"]; hasType {
			rawRules = []any{config}
		} else if _, hasType := config["ruleType"]; hasType {
			rawRules = []any{config}
		}
	}

	rules := make([]monitorRule, 0, len(rawRules))
	for i, raw := range rawRules {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ruleType := normalizeMonitorRuleType(firstRuleString(m, "type", "ruleType"))
		key := firstRuleString(m, "key", "ruleKey")
		if key == "" {
			key = ruleType
		}
		rule := monitorRule{
			Key:           key,
			Type:          ruleType,
			Symbol:        firstNonEmpty(firstRuleString(m, "symbol"), symbol),
			PortfolioID:   firstNonEmpty(firstRuleString(m, "portfolioId"), portfolioID),
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

func (s *Service) evaluateMonitorRule(ctx context.Context, rule monitorRule, now time.Time) MonitorRuleResult {
	switch rule.Type {
	case MonitorRulePriceAbove, MonitorRulePriceBelow, MonitorRulePriceBetween,
		MonitorRulePctChangeAbove, MonitorRulePctChangeBelow, MonitorRuleQuoteStale:
		return s.evaluateQuoteRule(ctx, rule, now)
	case MonitorRuleDailyCloseAbove, MonitorRuleDailyCloseBelow:
		return s.evaluateDailyCloseRule(ctx, rule)
	case MonitorRulePortfolioSymbolWeightOver, MonitorRulePortfolioSymbolWeightBelow:
		return s.evaluatePortfolioWeightRule(ctx, rule)
	default:
		return ruleResult(rule, MonitorRuleStatusDegraded, "unsupported rule type", 0, rule.Threshold, nil, time.Time{})
	}
}

func (s *Service) evaluateQuoteRule(ctx context.Context, rule monitorRule, now time.Time) MonitorRuleResult {
	if rule.Symbol == "" {
		return ruleResult(rule, MonitorRuleStatusDegraded, "symbol is required", 0, rule.Threshold, nil, time.Time{})
	}
	quotes, err := s.store.GetLatestQuotes(ctx, []string{rule.Symbol})
	if err != nil || len(quotes) == 0 {
		return ruleResult(rule, MonitorRuleStatusDegraded, "latest quote is missing", 0, rule.Threshold, nil, time.Time{})
	}
	quote := quotes[0]
	dataTime := quote.QuoteAt
	if dataTime.IsZero() {
		dataTime = quote.FetchedAt
	}
	if rule.Type == MonitorRuleQuoteStale {
		maxAge := time.Duration(rule.MaxAgeSeconds) * time.Second
		if maxAge <= 0 {
			maxAge = 30 * time.Minute
		}
		age := now.Sub(quote.FetchedAt)
		matched := quote.Status != QuoteStatusFresh || quote.FetchedAt.IsZero() || age > maxAge
		return boolRuleResult(rule, matched, age.Seconds(), maxAge.Seconds(), "quote stale check", quoteEvidence(quote), dataTime)
	}
	if quote.Status != QuoteStatusFresh || quote.LastPrice <= 0 {
		return ruleResult(rule, MonitorRuleStatusDegraded, "latest quote is not fresh", quote.LastPrice, rule.Threshold, quoteEvidence(quote), dataTime)
	}

	switch rule.Type {
	case MonitorRulePriceAbove:
		return boolRuleResult(rule, quote.LastPrice > rule.Threshold, quote.LastPrice, rule.Threshold, fmt.Sprintf("price %.2f > %.2f", quote.LastPrice, rule.Threshold), quoteEvidence(quote), dataTime)
	case MonitorRulePriceBelow:
		return boolRuleResult(rule, quote.LastPrice < rule.Threshold, quote.LastPrice, rule.Threshold, fmt.Sprintf("price %.2f < %.2f", quote.LastPrice, rule.Threshold), quoteEvidence(quote), dataTime)
	case MonitorRulePriceBetween:
		threshold := map[string]float64{"low": rule.Low, "high": rule.High}
		return boolRuleResult(rule, quote.LastPrice >= rule.Low && quote.LastPrice <= rule.High, quote.LastPrice, threshold, "price between range", quoteEvidence(quote), dataTime)
	case MonitorRulePctChangeAbove:
		return boolRuleResult(rule, quote.PctChange > rule.Threshold, quote.PctChange, rule.Threshold, fmt.Sprintf("pct change %.2f > %.2f", quote.PctChange, rule.Threshold), quoteEvidence(quote), dataTime)
	case MonitorRulePctChangeBelow:
		return boolRuleResult(rule, quote.PctChange < rule.Threshold, quote.PctChange, rule.Threshold, fmt.Sprintf("pct change %.2f < %.2f", quote.PctChange, rule.Threshold), quoteEvidence(quote), dataTime)
	}
	return ruleResult(rule, MonitorRuleStatusDegraded, "unsupported quote rule", 0, rule.Threshold, quoteEvidence(quote), dataTime)
}

func (s *Service) evaluateDailyCloseRule(ctx context.Context, rule monitorRule) MonitorRuleResult {
	if rule.Symbol == "" {
		return ruleResult(rule, MonitorRuleStatusDegraded, "symbol is required", 0, rule.Threshold, nil, time.Time{})
	}
	bars, err := s.store.GetDailyBars(ctx, rule.Symbol, DailyBarAdjustedNone, "", "", 1)
	if err != nil || len(bars) == 0 {
		return ruleResult(rule, MonitorRuleStatusDegraded, "daily bar is missing", 0, rule.Threshold, nil, time.Time{})
	}
	bar := bars[len(bars)-1]
	dataTime := dailyBarDataTime(bar)
	if bar.Quality == "failed" || bar.Quality == "empty" || isDailyBarsStale(bar.TradeDate, time.Now()) {
		return ruleResult(rule, MonitorRuleStatusDegraded, "daily bar quality is not usable", bar.Close, rule.Threshold, dailyBarEvidence(bar), dataTime)
	}
	if rule.Type == MonitorRuleDailyCloseAbove {
		return boolRuleResult(rule, bar.Close > rule.Threshold, bar.Close, rule.Threshold, fmt.Sprintf("daily close %.2f > %.2f", bar.Close, rule.Threshold), dailyBarEvidence(bar), dataTime)
	}
	return boolRuleResult(rule, bar.Close < rule.Threshold, bar.Close, rule.Threshold, fmt.Sprintf("daily close %.2f < %.2f", bar.Close, rule.Threshold), dailyBarEvidence(bar), dataTime)
}

func (s *Service) evaluatePortfolioWeightRule(ctx context.Context, rule monitorRule) MonitorRuleResult {
	if rule.PortfolioID == "" || rule.Symbol == "" {
		return ruleResult(rule, MonitorRuleStatusDegraded, "portfolioId and symbol are required", 0, rule.Threshold, nil, time.Time{})
	}
	snapshots, err := s.store.GetPortfolioSnapshots(ctx, rule.PortfolioID, 1)
	if err != nil || len(snapshots) == 0 {
		return ruleResult(rule, MonitorRuleStatusDegraded, "portfolio snapshot is missing", 0, rule.Threshold, nil, time.Time{})
	}
	snapshot := snapshots[0]
	if snapshot.Status == PortfolioValuationStatusFailed || snapshot.TotalAssetValue <= 0 {
		return ruleResult(rule, MonitorRuleStatusDegraded, "portfolio snapshot is not usable", 0, rule.Threshold, portfolioEvidence(snapshot, nil), snapshot.ValuationAt)
	}
	holdings, err := s.store.ListHoldings(ctx, rule.PortfolioID)
	if err != nil {
		return ruleResult(rule, MonitorRuleStatusDegraded, "holdings are missing", 0, rule.Threshold, portfolioEvidence(snapshot, nil), snapshot.ValuationAt)
	}
	for _, holding := range holdings {
		if holding.Symbol != rule.Symbol {
			continue
		}
		weight := holding.PositionPct
		if weight <= 0 && holding.MarketValue > 0 {
			weight = holding.MarketValue / snapshot.TotalAssetValue * 100
		}
		if rule.Type == MonitorRulePortfolioSymbolWeightBelow {
			return boolRuleResult(rule, weight < rule.Threshold, weight, rule.Threshold, fmt.Sprintf("position %.2f%% < %.2f%%", weight, rule.Threshold), portfolioEvidence(snapshot, &holding), snapshot.ValuationAt)
		}
		return boolRuleResult(rule, weight > rule.Threshold, weight, rule.Threshold, fmt.Sprintf("position %.2f%% > %.2f%%", weight, rule.Threshold), portfolioEvidence(snapshot, &holding), snapshot.ValuationAt)
	}
	return boolRuleResult(rule, false, 0, rule.Threshold, "symbol is not held in portfolio", portfolioEvidence(snapshot, nil), snapshot.ValuationAt)
}

func ruleResult(rule monitorRule, status, reason string, observed float64, threshold any, evidence map[string]any, dataTime time.Time) MonitorRuleResult {
	if evidence == nil {
		evidence = map[string]any{}
	}
	return MonitorRuleResult{
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

func boolRuleResult(rule monitorRule, matched bool, observed float64, threshold any, reason string, evidence map[string]any, dataTime time.Time) MonitorRuleResult {
	status := MonitorRuleStatusNotMatched
	if matched {
		status = MonitorRuleStatusMatched
	}
	return ruleResult(rule, status, reason, observed, threshold, evidence, dataTime)
}

func normalizeMonitorRuleType(ruleType string) string {
	switch strings.TrimSpace(ruleType) {
	case "position_pct_above", "position_weight_above":
		return MonitorRulePortfolioSymbolWeightOver
	case "position_pct_below", "position_weight_below":
		return MonitorRulePortfolioSymbolWeightBelow
	default:
		return strings.TrimSpace(ruleType)
	}
}

func quoteEvidence(quote StockV2QuoteLatest) map[string]any {
	return map[string]any{
		"symbol":           quote.Symbol,
		"market":           quote.Market,
		"lastPrice":        quote.LastPrice,
		"pctChange":        quote.PctChange,
		"amplitude":        quote.Amplitude,
		"turnoverRate":     quote.TurnoverRate,
		"volumeRatio":      quote.VolumeRatio,
		"mainNetInflow":    quote.MainNetInflow,
		"mainNetInflowPct": quote.MainNetInflowPct,
		"status":           quote.Status,
		"source":           quote.Source,
		"quoteAt":          quote.QuoteAt,
		"fetchedAt":        quote.FetchedAt,
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

func alertTitleForRule(rule MonitorRuleResult) string {
	switch rule.RuleType {
	case MonitorRulePriceAbove:
		return fmt.Sprintf("价格突破 %.2f", thresholdFloat(rule.Threshold))
	case MonitorRulePriceBelow:
		return fmt.Sprintf("价格跌破 %.2f", thresholdFloat(rule.Threshold))
	case MonitorRulePctChangeAbove:
		return fmt.Sprintf("涨跌幅高于 %.2f%%", thresholdFloat(rule.Threshold))
	case MonitorRulePctChangeBelow:
		return fmt.Sprintf("涨跌幅低于 %.2f%%", thresholdFloat(rule.Threshold))
	case MonitorRuleDailyCloseAbove:
		return fmt.Sprintf("日收盘价突破 %.2f", thresholdFloat(rule.Threshold))
	case MonitorRuleDailyCloseBelow:
		return fmt.Sprintf("日收盘价跌破 %.2f", thresholdFloat(rule.Threshold))
	case MonitorRulePortfolioSymbolWeightOver:
		return fmt.Sprintf("仓位占比超过 %.2f%%", thresholdFloat(rule.Threshold))
	case MonitorRulePortfolioSymbolWeightBelow:
		return fmt.Sprintf("仓位占比低于 %.2f%%", thresholdFloat(rule.Threshold))
	case MonitorRuleQuoteStale:
		return "行情数据过期"
	default:
		return "监控规则命中"
	}
}

func thresholdFloat(value any) float64 {
	v, _ := value.(float64)
	return v
}
