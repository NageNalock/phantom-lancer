package stockv2

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type decisionGateBuildInput struct {
	ContextType          string
	ContextID            string
	Symbol               string
	Market               string
	InstrumentType       string
	TradeDate            string
	Bars                 []StockV2DailyBar
	QuoteAvailable       bool
	ThemeSignals         []string
	FlowAvailable        bool
	MainFlowRatio20      float64
	FactorCluster        string
	FactorBlocked        bool
	FactorReason         string
	MarketRegime         string
	BenchmarkAvailable   bool
	BenchmarkReturn20Pct float64
	ReferenceHealth      decisionReferenceHealth
	TradeCalendar        []decisionTradeDay
}

func (s *Service) buildDecisionGateSnapshot(ctx context.Context, input decisionGateBuildInput) DecisionGateSnapshot {
	now := time.Now()
	input.Symbol = strings.TrimSpace(input.Symbol)
	input.Market = strings.ToUpper(strings.TrimSpace(input.Market))
	if input.InstrumentType == "" {
		input.InstrumentType = InstrumentTypeStock
	}
	snapshot := DecisionGateSnapshot{
		ContextType: input.ContextType, ContextID: input.ContextID, Symbol: input.Symbol,
		Market: input.Market, InstrumentType: input.InstrumentType, TradeDate: input.TradeDate,
		Status: DecisionHealthHealthy, MarketRegime: firstNonEmpty(input.MarketRegime, "neutral"),
		AllowedActions: []string{StrategyGenerationRuleActionObserve, StrategyGenerationRuleActionBuildPosition,
			StrategyGenerationRuleActionAddPosition, StrategyGenerationRuleActionHold,
			StrategyGenerationRuleActionReduce, StrategyGenerationRuleActionExit},
		Metrics: map[string]any{}, CreatedAt: now,
	}
	if !decisionSupportedMarket(input.Market) || (input.InstrumentType != InstrumentTypeStock && input.InstrumentType != InstrumentTypeExchangeFund) {
		snapshot.DataHealth = append(snapshot.DataHealth, DecisionDataHealth{Key: "market_support", Label: "市场支持", Status: DecisionHealthBlocked, Required: true, Message: "当前仅支持沪深股票与场内基金", CheckedAt: now})
		snapshot.Gates = defaultBlockedDecisionGates("当前市场或品种不在确定性校验范围")
		return finalizeDecisionGateSnapshot(snapshot)
	}

	bars := append([]StockV2DailyBar(nil), input.Bars...)
	sort.Slice(bars, func(i, j int) bool { return bars[i].TradeDate < bars[j].TradeDate })
	features := calculateDecisionBarFeatures(bars)
	if snapshot.TradeDate == "" {
		snapshot.TradeDate = features.TradeDate
	}
	snapshot.Metrics["latestPrice"] = features.Close
	snapshot.Metrics["return5Pct"] = features.Return5Pct
	snapshot.Metrics["return20Pct"] = features.Return20Pct
	snapshot.Metrics["atr14"] = features.ATR14
	snapshot.Metrics["atr14Pct"] = features.ATR14Pct
	snapshot.Metrics["ma20"] = features.MA20
	snapshot.Metrics["factorCluster"] = input.FactorCluster
	barHealth := DecisionDataHealth{Key: "daily_bars", Label: "前复权日线", Required: true, AsOf: features.TradeDate, Source: features.Source, CheckedAt: now}
	if !features.Valid {
		barHealth.Status, barHealth.Message = DecisionHealthBlocked, "至少需要 21 根完整前复权日线计算 ATR 与趋势"
	} else if snapshot.TradeDate != "" && features.TradeDate < snapshot.TradeDate {
		barHealth.Status, barHealth.Message = DecisionHealthBlocked, "日线未覆盖决策交易日"
	} else {
		barHealth.Status = DecisionHealthHealthy
	}
	snapshot.DataHealth = append(snapshot.DataHealth, barHealth)
	quoteHealth := DecisionDataHealth{Key: "latest_quote", Label: "最新可交易价格", Required: true, CheckedAt: now}
	if input.QuoteAvailable {
		quoteHealth.Status, quoteHealth.Message = DecisionHealthHealthy, "已取得最新价格"
	} else {
		quoteHealth.Status, quoteHealth.Message = DecisionHealthDegraded, "缺少最新报价；仅允许用日收盘条件表达新增风险"
	}
	snapshot.DataHealth = append(snapshot.DataHealth, quoteHealth)
	benchmarkHealth := DecisionDataHealth{Key: "market_benchmark", Label: "沪深 300 基准", Required: true, CheckedAt: now}
	if input.BenchmarkAvailable {
		benchmarkHealth.Status, benchmarkHealth.Message = DecisionHealthHealthy, "基准日线已用于市场状态和超额收益校验"
		snapshot.Metrics["benchmarkReturn20Pct"] = input.BenchmarkReturn20Pct
	} else {
		benchmarkHealth.Status, benchmarkHealth.Message = DecisionHealthBlocked, "基准日线缺失，无法判断市场环境与超额收益"
	}
	snapshot.DataHealth = append(snapshot.DataHealth, benchmarkHealth)

	volatility := DecisionGateResult{Key: DecisionGateVolatility, Label: "波动与 ATR 校准", Status: DecisionGateStatusPass,
		Summary: "波动与入场距离处于可评估范围", Metrics: map[string]any{
			"return5Pct": features.Return5Pct, "return20Pct": features.Return20Pct,
			"atr14Pct": features.ATR14Pct, "close": features.Close, "ma20": features.MA20,
		}}
	if !features.Valid || features.ATR14 <= 0 {
		volatility.Status, volatility.Summary = DecisionGateStatusBlocked, "ATR 或日线不足，禁止建仓与加仓"
	} else {
		limit5 := math.Max(12, 4*features.ATR14Pct)
		limit20 := math.Max(25, 7*features.ATR14Pct)
		upperBand := features.MA20 + 3*features.ATR14
		volatility.Metrics["return5LimitPct"] = limit5
		volatility.Metrics["return20LimitPct"] = limit20
		volatility.Metrics["ma20Plus3ATR"] = upperBand
		if features.Return5Pct > limit5 || features.Return20Pct > limit20 || features.Close > upperBand {
			volatility.Status = DecisionGateStatusBlocked
			volatility.Summary = "短期涨幅或价格偏离超过 ATR 风险边界"
			volatility.Reasons = decisionVolatilityReasons(features, limit5, limit20, upperBand)
		} else if snapshot.MarketRegime == "risk_off" && (features.Close < features.MA20 || features.Return20Pct <= 0) {
			volatility.Status = DecisionGateStatusBlocked
			volatility.Summary = "风险关闭环境中，价格未同时站上 MA20 且保持 20 日相对强势"
		}
	}
	snapshot.Gates = append(snapshot.Gates, volatility)

	catalyst := DecisionGateResult{Key: DecisionGateCatalystPricing, Label: "催化剂定价", Status: DecisionGateStatusNotApplicable, Summary: "没有把主题催化作为本次必要前提"}
	if len(input.ThemeSignals) > 0 {
		catalyst.Status = DecisionGateStatusPass
		catalyst.Summary = "主题催化尚未达到确定性过度定价边界"
		catalyst.EvidenceRefs = compactStringList(input.ThemeSignals, 5)
		pricedThreshold := math.Max(8, 2*features.ATR14Pct)
		excessReturn := features.Return20Pct - input.BenchmarkReturn20Pct
		catalyst.Metrics = map[string]any{"return20Pct": features.Return20Pct, "excessReturn20Pct": excessReturn, "pricedThresholdPct": pricedThreshold, "mainFlowRatio20": input.MainFlowRatio20}
		if features.Valid && input.BenchmarkAvailable && excessReturn > pricedThreshold && (!input.FlowAvailable || input.MainFlowRatio20 <= 0) {
			catalyst.Status = DecisionGateStatusBlocked
			catalyst.Summary = "主题催化后的涨幅已超过 2 ATR / 8% 且缺少资金确认"
		}
	}
	snapshot.Gates = append(snapshot.Gates, catalyst)

	crowding := DecisionGateResult{Key: DecisionGateFactorCrowding, Label: "因子拥挤", Status: DecisionGateStatusPass, Summary: "未与更高排名候选形成重复建仓因子", Metrics: map[string]any{"cluster": input.FactorCluster}}
	if input.FactorBlocked {
		crowding.Status, crowding.Summary = DecisionGateStatusBlocked, firstNonEmpty(input.FactorReason, "同一高相关因子簇只保留一个建仓候选")
	}
	snapshot.Gates = append(snapshot.Gates, crowding)

	eventGate := DecisionGateResult{Key: DecisionGateEventCalendar, Label: "事件日历保护", Status: DecisionGateStatusPass, Summary: "保护窗口内没有已知重大事件"}
	if input.InstrumentType == InstrumentTypeExchangeFund {
		eventGate.Status, eventGate.Summary = DecisionGateStatusNotApplicable, "场内基金不应用个股财报与解禁日历"
		snapshot.DataHealth = append(snapshot.DataHealth, DecisionDataHealth{Key: "event_calendar", Label: "公司事件日历", Status: DecisionHealthNotApplicable, Required: false, Message: "场内基金不适用", CheckedAt: now})
	} else {
		reference := input.ReferenceHealth
		eventHealth := DecisionDataHealth{Key: "event_calendar", Label: "公司事件日历", Required: true, Source: reference.Source, CheckedAt: firstNonZeroTime(reference.CheckedAt, now)}
		calendarOK := decisionTradeCalendarCovers(input.TradeCalendar, snapshot.TradeDate)
		if !reference.EventOK || !calendarOK {
			eventHealth.Status, eventHealth.Message = DecisionHealthBlocked, firstNonEmpty(reference.Message, "关键事件数据源未完成校验")
			if reference.EventOK && !calendarOK {
				eventHealth.Message = "交易日历未覆盖决策窗口，无法校验事件前后交易日"
			}
			eventGate.Status, eventGate.Summary = DecisionGateStatusBlocked, "事件日历不可验证，禁止建仓与加仓"
		} else {
			eventHealth.Status, eventHealth.Message = DecisionHealthHealthy, "财报、解禁、分红、业绩预告及交易日历已检查"
			start, end := decisionEventQueryRange(snapshot.TradeDate)
			events, err := s.store.ListDecisionMarketEvents(ctx, input.Symbol, start, end, snapshot.TradeDate)
			if err != nil {
				eventHealth.Status, eventGate.Status = DecisionHealthBlocked, DecisionGateStatusBlocked
				eventHealth.Message, eventGate.Summary = "事件缓存读取失败", "事件日历不可验证，禁止建仓与加仓"
			} else if event := protectedDecisionEvent(snapshot.TradeDate, events, input.TradeCalendar); event != nil {
				eventGate.Status = DecisionGateStatusBlocked
				eventGate.Summary = fmt.Sprintf("%s %s 位于 -2 至 +1 个交易日保护窗口", event.EventDate, event.Title)
				eventGate.EvidenceRefs = []string{event.EventType + ":" + event.EventDate}
			}
		}
		snapshot.DataHealth = append(snapshot.DataHealth, eventHealth)
		financeHealth := DecisionDataHealth{Key: "financial_facts", Label: "财务事实", Required: false, Source: reference.Source, CheckedAt: firstNonZeroTime(reference.CheckedAt, now)}
		if reference.FinanceOK {
			financeHealth.Status, financeHealth.Message = DecisionHealthHealthy, "利润表、现金流与财务指标已检查"
			if fact, err := s.store.GetLatestDecisionFinancialFact(ctx, input.Symbol, snapshot.TradeDate); err == nil {
				financeHealth.AsOf = fact.ReportPeriod
				snapshot.Metrics["financialReportPeriod"] = fact.ReportPeriod
				snapshot.Metrics["operatingCashFlow"] = fact.OperatingCashFlow
				snapshot.Metrics["netProfit"] = fact.NetProfit
				snapshot.Metrics["roe"] = fact.ROE
			} else if decisionFactMissing(err) {
				financeHealth.Status, financeHealth.Message = DecisionHealthDegraded, "数据源可用，但截至决策日没有可用财务记录"
			}
		} else {
			financeHealth.Status, financeHealth.Message = DecisionHealthDegraded, firstNonEmpty(reference.Message, "财务事实源不完整；不得把基本面作为唯一建仓依据")
		}
		snapshot.DataHealth = append(snapshot.DataHealth, financeHealth)
	}
	snapshot.Gates = append(snapshot.Gates, eventGate)

	flowHealth := DecisionDataHealth{Key: "fund_flow", Label: "主动资金证据", Required: false, CheckedAt: now}
	if input.FlowAvailable {
		flowHealth.Status, flowHealth.Message = DecisionHealthHealthy, "资金流可用于交叉验证"
	} else {
		flowHealth.Status, flowHealth.Message = DecisionHealthDegraded, "资金流缺失，不作为中性分参与评分"
	}
	snapshot.DataHealth = append(snapshot.DataHealth, flowHealth)
	return finalizeDecisionGateSnapshot(snapshot)
}

func finalizeDecisionGateSnapshot(snapshot DecisionGateSnapshot) DecisionGateSnapshot {
	blocked, degraded := false, false
	for _, item := range snapshot.Gates {
		blocked = blocked || item.Status == DecisionGateStatusBlocked
		degraded = degraded || item.Status == DecisionGateStatusDegraded
	}
	for _, item := range snapshot.DataHealth {
		blocked = blocked || (item.Required && item.Status == DecisionHealthBlocked)
		degraded = degraded || item.Status == DecisionHealthDegraded
	}
	switch {
	case blocked:
		snapshot.Status = DecisionHealthBlocked
		snapshot.AllowedActions = []string{StrategyGenerationRuleActionObserve, StrategyGenerationRuleActionHold, StrategyGenerationRuleActionReduce, StrategyGenerationRuleActionExit}
	case degraded:
		snapshot.Status = DecisionHealthDegraded
	default:
		snapshot.Status = DecisionHealthHealthy
	}
	return snapshot
}

func defaultBlockedDecisionGates(reason string) []DecisionGateResult {
	return []DecisionGateResult{
		{Key: DecisionGateCatalystPricing, Label: "催化剂定价", Status: DecisionGateStatusBlocked, Summary: reason},
		{Key: DecisionGateFactorCrowding, Label: "因子拥挤", Status: DecisionGateStatusBlocked, Summary: reason},
		{Key: DecisionGateVolatility, Label: "波动与 ATR 校准", Status: DecisionGateStatusBlocked, Summary: reason},
		{Key: DecisionGateEventCalendar, Label: "事件日历保护", Status: DecisionGateStatusBlocked, Summary: reason},
	}
}

type decisionBarFeatures struct {
	Valid                bool
	TradeDate, Source    string
	Close, MA20, ATR14   float64
	ATR14Pct, Return5Pct float64
	Return20Pct          float64
}

func calculateDecisionBarFeatures(bars []StockV2DailyBar) decisionBarFeatures {
	if len(bars) < 21 {
		return decisionBarFeatures{}
	}
	last := len(bars) - 1
	close := bars[last].Close
	if close <= 0 {
		return decisionBarFeatures{}
	}
	features := decisionBarFeatures{Valid: true, TradeDate: bars[last].TradeDate, Source: bars[last].Source, Close: close,
		Return5Pct: pctReturn(close, bars[last-5].Close), Return20Pct: pctReturn(close, bars[last-20].Close)}
	for i := last - 19; i <= last; i++ {
		features.MA20 += bars[i].Close
	}
	features.MA20 /= 20
	start := last - 13
	for i := start; i <= last; i++ {
		previousClose := bars[i].PrevClose
		if previousClose <= 0 && i > 0 {
			previousClose = bars[i-1].Close
		}
		tr := math.Max(bars[i].High-bars[i].Low, math.Max(math.Abs(bars[i].High-previousClose), math.Abs(bars[i].Low-previousClose)))
		features.ATR14 += tr
	}
	features.ATR14 /= 14
	features.ATR14Pct = features.ATR14 / close * 100
	return features
}

func decisionVolatilityReasons(features decisionBarFeatures, limit5, limit20, upperBand float64) []string {
	var out []string
	if features.Return5Pct > limit5 {
		out = append(out, fmt.Sprintf("5日涨幅 %.1f%% > %.1f%%", features.Return5Pct, limit5))
	}
	if features.Return20Pct > limit20 {
		out = append(out, fmt.Sprintf("20日涨幅 %.1f%% > %.1f%%", features.Return20Pct, limit20))
	}
	if features.Close > upperBand {
		out = append(out, fmt.Sprintf("收盘 %.3f > MA20+3ATR %.3f", features.Close, upperBand))
	}
	return out
}

func decisionEventQueryRange(tradeDate string) (string, string) {
	date, err := time.Parse("2006-01-02", tradeDate)
	if err != nil {
		date = time.Now()
	}
	return date.AddDate(0, 0, -5).Format("2006-01-02"), date.AddDate(0, 0, 10).Format("2006-01-02")
}

func protectedDecisionEvent(tradeDate string, events []decisionMarketEvent, calendar []decisionTradeDay) *decisionMarketEvent {
	for i := range events {
		distance, ok := decisionTradeSessionDistance(tradeDate, events[i].EventDate, calendar)
		if ok && distance >= -1 && distance <= 2 {
			return &events[i]
		}
	}
	return nil
}

func decisionTradeCalendarCovers(calendar []decisionTradeDay, tradeDate string) bool {
	if len(calendar) == 0 || tradeDate == "" {
		return false
	}
	start, end := decisionEventQueryRange(tradeDate)
	return calendar[0].Date <= start && calendar[len(calendar)-1].Date >= end
}

func decisionTradeSessionDistance(from, to string, calendar []decisionTradeDay) (int, bool) {
	if from == "" || to == "" || len(calendar) == 0 || from < calendar[0].Date || from > calendar[len(calendar)-1].Date || to < calendar[0].Date || to > calendar[len(calendar)-1].Date {
		return 0, false
	}
	if from == to {
		return 0, true
	}
	distance := 0
	if from < to {
		for _, day := range calendar {
			if day.Open && day.Date > from && day.Date <= to {
				distance++
			}
		}
		return distance, true
	}
	for _, day := range calendar {
		if day.Open && day.Date > to && day.Date <= from {
			distance--
		}
	}
	return distance, true
}

func decisionTradingSessionCutoff(calendar []decisionTradeDay, tradeDate string, sessions int) time.Time {
	if sessions <= 0 {
		return time.Time{}
	}
	openDates := make([]string, 0, sessions)
	for _, day := range calendar {
		if day.Open && day.Date <= tradeDate {
			openDates = append(openDates, day.Date)
		}
	}
	if len(openDates) < sessions {
		return time.Time{}
	}
	cutoff, _ := time.ParseInLocation("2006-01-02", openDates[len(openDates)-sessions], time.Local)
	return cutoff
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func decisionActionAllowed(snapshot DecisionGateSnapshot, action string) bool {
	action = strings.TrimSpace(action)
	for _, allowed := range snapshot.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}

func (s *Service) fillStrategyGenerationDecisionGates(ctx context.Context, out *StrategyGenerationContext) error {
	if out == nil {
		return nil
	}
	triggerID := strategyGenerationTriggerID(out.Input)
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil {
		return err
	}
	decisionDate := time.Now().Format("2006-01-02")
	tradeCalendar, _ := s.refreshDecisionTradeCalendar(ctx, config, decisionDate)
	catalystCutoff := decisionTradingSessionCutoff(tradeCalendar, decisionDate, 20)
	items := make([]OpportunityMarketScanCandidate, 0)
	type targetMeta struct {
		symbol, market, instrumentType string
		quoteAvailable                 bool
		themeSignals                   []string
		industry                       string
		concepts                       []string
	}
	metas := make([]targetMeta, 0)
	if out.Mode == StrategyGenerationModePortfolio {
		for _, holding := range out.Holdings {
			instrumentType := InstrumentTypeStock
			if holding.Profile != nil {
				instrumentType = holding.Profile.InstrumentType
			} else if instrument, err := s.store.GetInstrument(ctx, holding.Symbol); err == nil {
				instrumentType = instrument.InstrumentType
			}
			industry, concepts := "", []string(nil)
			if instrument, err := s.store.GetInstrument(ctx, holding.Symbol); err == nil {
				industry, concepts = instrument.Industry, instrument.Concepts
			}
			metas = append(metas, targetMeta{holding.Symbol, holding.Market, instrumentType, holding.Quote != nil && holding.Quote.LastPrice > 0, nil, industry, concepts})
		}
	} else {
		for _, target := range out.Targets {
			if target.Instrument == nil {
				continue
			}
			metas = append(metas, targetMeta{target.Instrument.Symbol, target.Instrument.Market, target.Instrument.InstrumentType,
				target.LatestQuote != nil && target.LatestQuote.LastPrice > 0, strategyOpportunityThemeSignals(*out, target.Instrument.Symbol, catalystCutoff),
				target.Instrument.Industry, target.Instrument.Concepts})
		}
	}
	for _, meta := range metas {
		items = append(items, OpportunityMarketScanCandidate{Symbol: meta.symbol, Market: meta.market, Industry: meta.industry, Concepts: meta.concepts,
			Metrics: OpportunityMarketScanMetrics{InstrumentType: meta.instrumentType}})
	}
	reference := s.refreshDecisionReferenceData(ctx, config, items)
	benchmarkBars, benchmarkErr := s.refreshDecisionBenchmark(ctx, config, decisionDate)
	benchmarkReturn, benchmarkOK := decisionBenchmarkReturn20(benchmarkBars)
	if benchmarkErr != nil {
		benchmarkOK = false
	}
	marketRegime := decisionBenchmarkMarketRegime(benchmarkReturn, benchmarkOK)
	out.DecisionGates = make(map[string]DecisionGateSnapshot, len(metas))
	barsBySymbol := make(map[string][]StockV2DailyBar, len(metas))
	for _, meta := range metas {
		barsBySymbol[meta.symbol], _ = s.store.GetDailyBars(ctx, meta.symbol, DailyBarAdjustedQFQ, "", "", 120)
	}
	clusters, crowded := opportunityMarketFactorClusters(items, barsBySymbol)
	for _, meta := range metas {
		bars := barsBySymbol[meta.symbol]
		snapshot := s.buildDecisionGateSnapshot(ctx, decisionGateBuildInput{
			ContextType: "strategy_generation", ContextID: triggerID, Symbol: meta.symbol, Market: meta.market,
			InstrumentType: meta.instrumentType, Bars: bars, QuoteAvailable: meta.quoteAvailable,
			ThemeSignals: meta.themeSignals, MarketRegime: marketRegime, ReferenceHealth: reference[meta.symbol],
			BenchmarkAvailable: benchmarkOK, BenchmarkReturn20Pct: benchmarkReturn,
			FactorCluster: clusters[meta.symbol], FactorBlocked: crowded[meta.symbol] != "", FactorReason: crowded[meta.symbol],
			TradeCalendar: tradeCalendar,
		})
		snapshot, err = s.store.SaveDecisionGateSnapshot(ctx, snapshot)
		if err != nil {
			return err
		}
		out.DecisionGates[meta.symbol] = snapshot
	}
	out.FreshnessSummary["decisionGateCount"] = len(out.DecisionGates)
	return nil
}

func strategyOpportunityThemeSignals(out StrategyGenerationContext, symbol string, catalystCutoff time.Time) []string {
	var signals []string
	for _, candidate := range out.OpportunityCandidates {
		if candidate.Symbol == symbol {
			for _, evidence := range out.OpportunityEvidenceByCandidate[candidate.ID] {
				if !catalystCutoff.IsZero() && !evidence.PublishedAt.IsZero() && !evidence.PublishedAt.Before(catalystCutoff) {
					signals = append(signals, firstNonEmpty(evidence.Title, evidence.Summary))
				}
			}
		}
	}
	return compactStringList(signals, 5)
}

func (s *Service) applyDecisionGatesToStrategyReport(ctx context.Context, run AgentRun, report *StrategyGenerationReport) {
	if report == nil {
		return
	}
	items, err := s.store.ListDecisionGateSnapshots(ctx, "strategy_generation", run.TriggerObjectID)
	if err != nil || len(items) == 0 {
		return
	}
	decisionGates := make(map[string]DecisionGateSnapshot, len(items))
	for _, item := range items {
		decisionGates[item.Symbol] = item
	}
	for i := range report.Drafts {
		draft := &report.Drafts[i]
		snapshot, ok := decisionGates[strings.TrimSpace(draft.Symbol)]
		if !ok {
			continue
		}
		draft.GateSnapshotID = snapshot.ID
		ensureDecisionGateAuditFields(draft, snapshot)
		blockedReason := ""
		for _, rule := range draft.Playbook.Rules {
			if !decisionActionAllowed(snapshot, rule.Action) {
				blockedReason = decisionGateBlockedReason(snapshot.Gates)
				break
			}
		}
		basisText := append(append([]string(nil), draft.DecisionBasis...), draft.Thesis)
		basisText = append(basisText, draft.EvidenceSummary...)
		if strategyDraftAddsRisk(*draft) && decisionBasisNeedsFinancialFacts(basisText) && decisionDataHealthStatus(snapshot.DataHealth, "financial_facts") != DecisionHealthHealthy {
			blockedReason = "策略以催化剂或基本面为核心依据，但决策时点财务事实不完整"
		}
		if blockedReason != "" {
			draft.DraftType = StrategyGenerationDraftTypeNoChange
			draft.Playbook.Rules = nil
			draft.RiskSummary = compactStringList(append(draft.RiskSummary, "确定性门阻断："+blockedReason), 20)
			report.RunSummary.KeyConflicts = compactStringList(append(report.RunSummary.KeyConflicts,
				fmt.Sprintf("%s 未创建策略草案：%s", draft.Symbol, blockedReason)), 20)
			continue
		}
		calibrateStrategyRuleThresholds(draft, snapshot)
	}
}

func strategyDraftAddsRisk(draft StrategyGenerationDraft) bool {
	for _, rule := range draft.Playbook.Rules {
		if rule.Action == StrategyGenerationRuleActionBuildPosition || rule.Action == StrategyGenerationRuleActionAddPosition {
			return true
		}
	}
	return false
}

func decisionBasisNeedsFinancialFacts(items []string) bool {
	keywords := []string{
		"fundamental", "catalyst", "earnings", "revenue", "profit", "order", "valuation",
		"基本面", "催化", "财务", "财报", "业绩", "营收", "利润", "订单", "估值", "现金流", "供需", "景气",
	}
	for _, item := range items {
		value := strings.ToLower(strings.TrimSpace(item))
		for _, keyword := range keywords {
			if strings.Contains(value, keyword) {
				return true
			}
		}
	}
	return false
}

func decisionDataHealthStatus(items []DecisionDataHealth, key string) string {
	for _, item := range items {
		if item.Key == key {
			return item.Status
		}
	}
	return ""
}

func ensureDecisionGateAuditFields(draft *StrategyGenerationDraft, snapshot DecisionGateSnapshot) {
	if draft == nil {
		return
	}
	basis := append([]string(nil), draft.DecisionBasis...)
	basis = append(basis, "price", "factor", "event_calendar")
	for _, gate := range snapshot.Gates {
		if gate.Key == DecisionGateCatalystPricing && gate.Status != DecisionGateStatusNotApplicable {
			basis = append(basis, "catalyst")
			if decisionDataHealthStatus(snapshot.DataHealth, "fund_flow") == DecisionHealthHealthy {
				basis = append(basis, "flow")
			}
			break
		}
	}
	basisText := append(append([]string(nil), draft.Thesis), draft.EvidenceSummary...)
	if decisionBasisNeedsFinancialFacts(basisText) {
		basis = append(basis, "fundamental")
	}
	draft.DecisionBasis = compactStringList(basis, 12)
	// The gate snapshot is an exact, persisted evidence identifier supplied to
	// the Agent and is always part of the server's final authorization decision.
	draft.EvidenceRefIDs = compactStringList(append(draft.EvidenceRefIDs, snapshot.ID), 50)
}

func calibrateStrategyRuleThresholds(draft *StrategyGenerationDraft, snapshot DecisionGateSnapshot) {
	price, _ := numberFromAny(snapshot.Metrics["latestPrice"])
	atr, _ := numberFromAny(snapshot.Metrics["atr14"])
	if price <= 0 || atr <= 0 {
		return
	}
	minimumDistance := .5 * atr
	for ruleIndex := range draft.Playbook.Rules {
		rule := &draft.Playbook.Rules[ruleIndex]
		if rule.Action != StrategyGenerationRuleActionBuildPosition && rule.Action != StrategyGenerationRuleActionAddPosition {
			continue
		}
		for filterIndex := range rule.DataPrefilters {
			filter := rule.DataPrefilters[filterIndex]
			typeName := normalizeWatchRuleType(stringFromAny(filter["type"]))
			threshold, ok := numberFromAny(filter["threshold"])
			if !ok || math.Abs(threshold-price) >= minimumDistance {
				continue
			}
			switch typeName {
			case WatchRulePriceAbove, WatchRuleDailyCloseAbove:
				filter["threshold"] = price + minimumDistance
			case WatchRulePriceBelow, WatchRuleDailyCloseBelow:
				filter["threshold"] = price - minimumDistance
			}
		}
	}
}

func (s *Service) fillPortfolioSentinelDecisionGates(ctx context.Context, out *PortfolioSentinelContext) error {
	if out == nil {
		return nil
	}
	type meta struct {
		symbol, market, instrumentType string
		quoteAvailable                 bool
		industry                       string
		concepts                       []string
	}
	bySymbol := map[string]meta{}
	for _, portfolio := range out.Portfolios {
		for _, holding := range portfolio.Holdings {
			instrumentType := InstrumentTypeStock
			if holding.Profile != nil {
				instrumentType = holding.Profile.InstrumentType
			} else if instrument, err := s.store.GetInstrument(ctx, holding.Holding.Symbol); err == nil {
				instrumentType = instrument.InstrumentType
			}
			industry, concepts := "", []string(nil)
			if instrument, err := s.store.GetInstrument(ctx, holding.Holding.Symbol); err == nil {
				industry, concepts = instrument.Industry, instrument.Concepts
			}
			bySymbol[holding.Holding.Symbol] = meta{holding.Holding.Symbol, holding.Holding.Market, instrumentType,
				holding.Quote != nil && holding.Quote.LastPrice > 0, industry, concepts}
		}
	}
	for _, candidate := range out.Candidates {
		if _, exists := bySymbol[candidate.Symbol]; exists {
			continue
		}
		instrument, err := s.store.GetInstrument(ctx, candidate.Symbol)
		if err != nil {
			continue
		}
		quoteAvailable := false
		if quotes, err := s.store.GetLatestQuotes(ctx, []string{candidate.Symbol}); err == nil && len(quotes) > 0 {
			quoteAvailable = quotes[0].LastPrice > 0
		}
		bySymbol[candidate.Symbol] = meta{candidate.Symbol, firstNonEmpty(candidate.Market, instrument.Market), instrument.InstrumentType, quoteAvailable, instrument.Industry, instrument.Concepts}
	}
	items := make([]OpportunityMarketScanCandidate, 0, len(bySymbol))
	for _, item := range bySymbol {
		items = append(items, OpportunityMarketScanCandidate{Symbol: item.symbol, Market: item.market, Industry: item.industry, Concepts: item.concepts,
			Metrics: OpportunityMarketScanMetrics{InstrumentType: item.instrumentType}})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Symbol < items[j].Symbol })
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil {
		return err
	}
	reference := s.refreshDecisionReferenceData(ctx, config, items)
	decisionDate := time.Now().Format("2006-01-02")
	tradeCalendar, _ := s.refreshDecisionTradeCalendar(ctx, config, decisionDate)
	benchmarkBars, benchmarkErr := s.refreshDecisionBenchmark(ctx, config, decisionDate)
	benchmarkReturn, benchmarkOK := decisionBenchmarkReturn20(benchmarkBars)
	if benchmarkErr != nil {
		benchmarkOK = false
	}
	marketRegime := decisionBenchmarkMarketRegime(benchmarkReturn, benchmarkOK)
	out.DecisionGates = make(map[string]DecisionGateSnapshot, len(bySymbol))
	barsBySymbol := make(map[string][]StockV2DailyBar, len(bySymbol))
	for _, item := range bySymbol {
		barsBySymbol[item.symbol], _ = s.store.GetDailyBars(ctx, item.symbol, DailyBarAdjustedQFQ, "", "", 120)
	}
	clusters, crowded := opportunityMarketFactorClusters(items, barsBySymbol)
	for _, item := range bySymbol {
		bars := barsBySymbol[item.symbol]
		snapshot := s.buildDecisionGateSnapshot(ctx, decisionGateBuildInput{
			ContextType: "portfolio_sentinel", ContextID: out.RunID, Symbol: item.symbol, Market: item.market,
			InstrumentType: item.instrumentType, Bars: bars, QuoteAvailable: item.quoteAvailable,
			MarketRegime: marketRegime, ReferenceHealth: reference[item.symbol],
			BenchmarkAvailable: benchmarkOK, BenchmarkReturn20Pct: benchmarkReturn,
			FactorCluster: clusters[item.symbol], FactorBlocked: crowded[item.symbol] != "", FactorReason: crowded[item.symbol],
			TradeCalendar: tradeCalendar,
		})
		snapshot, err = s.store.SaveDecisionGateSnapshot(ctx, snapshot)
		if err != nil {
			return err
		}
		out.DecisionGates[item.symbol] = snapshot
	}
	return nil
}

func decisionBenchmarkReturn20(items []decisionIndexBar) (float64, bool) {
	if len(items) < 21 {
		return 0, false
	}
	bars := append([]decisionIndexBar(nil), items...)
	sort.Slice(bars, func(i, j int) bool { return bars[i].TradeDate < bars[j].TradeDate })
	last := len(bars) - 1
	if bars[last].Close <= 0 || bars[last-20].Close <= 0 {
		return 0, false
	}
	return pctReturn(bars[last].Close, bars[last-20].Close), true
}

func decisionBenchmarkMarketRegime(return20 float64, available bool) string {
	if !available {
		return "neutral"
	}
	if return20 < 0 {
		return "risk_off"
	}
	if return20 > 0 {
		return "risk_on"
	}
	return "neutral"
}

func (s *Service) refreshOpportunityDecisionOutcomes(ctx context.Context, candidate *OpportunityMarketScanCandidate) {
	if candidate == nil || candidate.Metrics.GateSnapshotID == "" || candidate.Metrics.TradeDate == "" {
		return
	}
	bars, err := s.store.GetDailyBars(ctx, candidate.Symbol, DailyBarAdjustedQFQ, "", "", 500)
	if err != nil || len(bars) == 0 {
		return
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].TradeDate < bars[j].TradeDate })
	baseIndex := -1
	for i := range bars {
		if bars[i].TradeDate == candidate.Metrics.TradeDate {
			baseIndex = i
			break
		}
	}
	if baseIndex < 0 || bars[baseIndex].Close <= 0 {
		return
	}
	indexBars, _ := s.store.GetDecisionIndexBars(ctx, "000300.SH", "9999-12-31", 500)
	indexClose := make(map[string]float64, len(indexBars))
	for _, bar := range indexBars {
		indexClose[bar.TradeDate] = bar.Close
	}
	indexBase := indexClose[candidate.Metrics.TradeDate]
	existing, _ := s.store.ListDecisionGateOutcomes(ctx, candidate.Metrics.GateSnapshotID)
	existingByHorizon := make(map[int]DecisionGateOutcome, len(existing))
	for _, item := range existing {
		existingByHorizon[item.Horizon] = item
	}
	for _, horizon := range []int{1, 3, 5, 10, 20} {
		target := baseIndex + horizon
		if target >= len(bars) || bars[target].Close <= 0 {
			continue
		}
		item := DecisionGateOutcome{
			SnapshotID: candidate.Metrics.GateSnapshotID, Horizon: horizon,
			DueTradeDate: bars[target].TradeDate, ObservedDate: bars[target].TradeDate,
			ReturnPct: pctReturn(bars[target].Close, bars[baseIndex].Close), Status: "observed_return_only",
		}
		if indexBase > 0 && indexClose[bars[target].TradeDate] > 0 {
			item.ExcessReturnPct = item.ReturnPct - pctReturn(indexClose[bars[target].TradeDate], indexBase)
			item.Status = "observed"
		}
		if prior, ok := existingByHorizon[horizon]; ok && prior.Status == item.Status && prior.ObservedDate == item.ObservedDate && math.Abs(prior.ReturnPct-item.ReturnPct) < 1e-9 && math.Abs(prior.ExcessReturnPct-item.ExcessReturnPct) < 1e-9 {
			continue
		}
		item.UpdatedAt = time.Now()
		if err := s.store.SaveDecisionGateOutcome(ctx, item); err == nil {
			existingByHorizon[horizon] = item
		}
	}
	candidate.Metrics.DecisionOutcomes = candidate.Metrics.DecisionOutcomes[:0]
	for _, horizon := range []int{1, 3, 5, 10, 20} {
		if item, ok := existingByHorizon[horizon]; ok {
			candidate.Metrics.DecisionOutcomes = append(candidate.Metrics.DecisionOutcomes, item)
		}
	}
}

func (s *Service) applyDecisionGateToPortfolioPlan(ctx context.Context, runID string, plan *PortfolioSentinelActionPlan) bool {
	if plan == nil || (plan.Action != PortfolioSentinelPlanBuild && plan.Action != PortfolioSentinelPlanAdd) {
		return false
	}
	snapshot, err := s.store.GetLatestDecisionGateSnapshot(ctx, "portfolio_sentinel", runID, plan.Symbol)
	if err != nil {
		plan.Action = PortfolioSentinelPlanHold
		plan.TriggerMode, plan.TriggerPolicy = "", ""
		plan.Conditions, plan.Sizing = nil, nil
		plan.Reason = firstNonEmpty(plan.Reason, "维持持有") + "；确定性校验快照缺失，未发布新增风险动作"
		return true
	}
	action := StrategyGenerationRuleActionBuildPosition
	if plan.Action == PortfolioSentinelPlanAdd {
		action = StrategyGenerationRuleActionAddPosition
	}
	if !decisionActionAllowed(snapshot, action) {
		reason := decisionGateBlockedReason(snapshot.Gates)
		plan.Action = PortfolioSentinelPlanHold
		plan.TriggerMode, plan.TriggerPolicy = "", ""
		plan.Conditions, plan.Sizing = nil, nil
		plan.Reason = firstNonEmpty(plan.Reason, "维持持有") + "；确定性门阻断新增风险：" + reason
		return true
	}
	price, _ := numberFromAny(snapshot.Metrics["latestPrice"])
	atr, _ := numberFromAny(snapshot.Metrics["atr14"])
	if price <= 0 || atr <= 0 {
		return false
	}
	minimumDistance := .5 * atr
	for i := range plan.Conditions {
		condition := &plan.Conditions[i]
		if condition.Threshold == nil || math.Abs(*condition.Threshold-price) >= minimumDistance {
			continue
		}
		switch normalizeWatchRuleType(condition.Type) {
		case WatchRulePriceAbove, WatchRuleDailyCloseAbove:
			value := price + minimumDistance
			condition.Threshold = &value
		case WatchRulePriceBelow, WatchRuleDailyCloseBelow:
			value := price - minimumDistance
			condition.Threshold = &value
		}
	}
	return false
}
