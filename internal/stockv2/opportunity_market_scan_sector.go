package stockv2

import (
	"math"
	"sort"
	"strings"
	"time"
)

type opportunityMarketSectorGroup struct {
	key   string
	name  string
	items []opportunityMarketScanRawMetric
}

type opportunityMarketAdmission struct {
	price   bool
	sector  bool
	message bool
}

func buildOpportunityMarketSectorSnapshot(
	scored []opportunityMarketScanRawMetric,
	previous OpportunityMarketSectorSnapshot,
	tradeDate string,
	at time.Time,
) (OpportunityMarketSectorSnapshot, map[string][]OpportunityMarketSectorSignal) {
	snapshot := OpportunityMarketSectorSnapshot{
		CapturedAt: at, TradeDate: tradeDate, Status: DecisionHealthHealthy, EligibleCount: len(scored),
	}
	groups := make(map[string]*opportunityMarketSectorGroup)
	for _, item := range scored {
		memberships := opportunityMarketSectorMemberships(item.Instrument)
		if len(memberships) > 0 {
			snapshot.ClassifiedCount++
		}
		for key, name := range memberships {
			group := groups[key]
			if group == nil {
				group = &opportunityMarketSectorGroup{key: key, name: name}
				groups[key] = group
			}
			group.items = append(group.items, item)
		}
	}
	if snapshot.EligibleCount > 0 {
		snapshot.CoverageRatio = float64(snapshot.ClassifiedCount) / float64(snapshot.EligibleCount)
	}
	if snapshot.CoverageRatio < opportunityMarketScanMinimumSectorCoverage {
		snapshot.Status = DecisionHealthBlocked
		snapshot.Message = "行业分类覆盖不足，板块轮动结论不可用"
	} else {
		snapshot.Message = "行业与板块已完成横截面聚合和连续状态判断"
	}

	previousByKey := make(map[string]OpportunityMarketSectorTrend, len(previous.Trends))
	for _, trend := range previous.Trends {
		previousByKey[trend.Key] = trend
	}
	rankBySymbol := make(map[string]int, len(scored))
	for i, item := range scored {
		rankBySymbol[item.Instrument.Symbol] = i + 1
	}
	for _, group := range groups {
		if len(group.items) < 5 {
			continue
		}
		trend := opportunityMarketSectorTrendFromGroup(*group, rankBySymbol)
		prior := previousByKey[trend.Key]
		trend.PreviousState = prior.State
		trend.State = classifyOpportunityMarketSector(trend, prior)
		if trend.State == "" {
			continue
		}
		trend.FirstSeenTradeDate, trend.Streak = opportunityMarketSectorContinuity(prior, previous.TradeDate, tradeDate)
		trend.RepresentativeSymbols, trend.RepresentativeNames = opportunityMarketSectorRepresentatives(group.items)
		snapshot.Trends = append(snapshot.Trends, trend)
	}
	sort.SliceStable(snapshot.Trends, func(i, j int) bool {
		left, right := opportunityMarketSectorStatePriority(snapshot.Trends[i].State), opportunityMarketSectorStatePriority(snapshot.Trends[j].State)
		if left != right {
			return left > right
		}
		if snapshot.Trends[i].Score != snapshot.Trends[j].Score {
			return snapshot.Trends[i].Score > snapshot.Trends[j].Score
		}
		return snapshot.Trends[i].Key < snapshot.Trends[j].Key
	})
	if len(snapshot.Trends) > 12 {
		snapshot.Trends = snapshot.Trends[:12]
	}
	snapshot.TrackedSectorCount = len(snapshot.Trends)

	signals := make(map[string][]OpportunityMarketSectorSignal)
	for _, trend := range snapshot.Trends {
		group := groups[trend.Key]
		if group == nil {
			continue
		}
		for _, item := range group.items {
			symbol := item.Instrument.Symbol
			if len(signals[symbol]) >= 2 {
				continue
			}
			signals[symbol] = append(signals[symbol], OpportunityMarketSectorSignal{
				Key: trend.Key, Name: trend.Name, State: trend.State, Score: trend.Score,
				FirstSeenTradeDate: trend.FirstSeenTradeDate, Streak: trend.Streak,
			})
		}
	}
	return snapshot, signals
}

func opportunityMarketSectorMemberships(instrument StockV2Instrument) map[string]string {
	out := make(map[string]string, 2)
	if name := strings.TrimSpace(instrument.Industry); name != "" {
		out["industry:"+strings.ToLower(name)] = name
	}
	if name := strings.TrimSpace(instrument.Sector); name != "" && !strings.EqualFold(name, strings.TrimSpace(instrument.Industry)) {
		out["sector:"+strings.ToLower(name)] = name
	}
	return out
}

func opportunityMarketSectorTrendFromGroup(group opportunityMarketSectorGroup, rankBySymbol map[string]int) OpportunityMarketSectorTrend {
	trend := OpportunityMarketSectorTrend{Key: group.key, Name: group.name, MemberCount: len(group.items)}
	return3 := make([]float64, 0, len(group.items))
	return5 := make([]float64, 0, len(group.items))
	return20 := make([]float64, 0, len(group.items))
	aboveNow, above3, positive5, expanded := 0, 0, 0, 0
	for _, item := range group.items {
		r3, r5, r20 := pctReturn(item.Close, item.Close3), pctReturn(item.Close, item.Close5), pctReturn(item.Close, item.Close20)
		return3, return5, return20 = append(return3, r3), append(return5, r5), append(return20, r20)
		if item.Close > item.MA20 {
			aboveNow++
		}
		if item.Close3 > item.MA20Prev3 {
			above3++
		}
		if r5 > 0 {
			positive5++
		}
		if safeRatio(item.Volume5, item.Volume20) >= 1.10 {
			expanded++
		}
		if rankBySymbol[item.Instrument.Symbol] <= opportunityMarketScanLocalLimit {
			trend.Top200Count++
		}
	}
	count := float64(len(group.items))
	trend.AboveMA20Ratio = float64(aboveNow) / count
	trend.AboveMA20Delta3 = trend.AboveMA20Ratio - float64(above3)/count
	trend.Positive5DayRatio = float64(positive5) / count
	trend.VolumeExpansionRatio = float64(expanded) / count
	trend.MedianReturn3Pct = opportunityMedian(return3)
	trend.MedianReturn5Pct = opportunityMedian(return5)
	trend.MedianReturn20Pct = opportunityMedian(return20)
	topShare := math.Min(1, float64(trend.Top200Count)/math.Max(3, count*.15))
	acceleration := math.Max(0, math.Min(1, (trend.AboveMA20Delta3+.10)/.30))
	trend.Score = clampScore(30*trend.AboveMA20Ratio + 20*trend.Positive5DayRatio +
		18*trend.VolumeExpansionRatio + 17*acceleration + 15*topShare)
	return trend
}

func classifyOpportunityMarketSector(current, previous OpportunityMarketSectorTrend) string {
	wasTracked := previous.State != "" && previous.State != OpportunityMarketSectorStateInvalidated
	if wasTracked && current.AboveMA20Ratio < .35 && current.AboveMA20Delta3 <= -.08 {
		return OpportunityMarketSectorStateInvalidated
	}
	if wasTracked && (current.AboveMA20Ratio < .48 || current.AboveMA20Delta3 <= -.08 || current.MedianReturn5Pct < -1) {
		return OpportunityMarketSectorStateFading
	}
	if current.MedianReturn5Pct >= 12 || current.MedianReturn20Pct >= 25 ||
		(current.MedianReturn5Pct >= 8 && current.AboveMA20Ratio >= .82) {
		return OpportunityMarketSectorStateOverheated
	}
	core := current.AboveMA20Ratio >= .55 && current.Positive5DayRatio >= .52 &&
		current.VolumeExpansionRatio >= .20 && current.Top200Count >= 2
	if core && (previous.State == OpportunityMarketSectorStateEmerging || previous.State == OpportunityMarketSectorStateConfirmed) {
		return OpportunityMarketSectorStateConfirmed
	}
	if core && (current.AboveMA20Delta3 >= .08 || current.MedianReturn3Pct >= 1.2) {
		return OpportunityMarketSectorStateEmerging
	}
	if core && current.AboveMA20Ratio >= .70 && current.Top200Count >= 4 && current.MedianReturn5Pct >= 3 {
		return OpportunityMarketSectorStateConfirmed
	}
	if wasTracked {
		return OpportunityMarketSectorStateFading
	}
	return ""
}

func opportunityMarketSectorContinuity(previous OpportunityMarketSectorTrend, previousTradeDate, tradeDate string) (string, int) {
	if previous.State == "" || previous.State == OpportunityMarketSectorStateInvalidated {
		return tradeDate, 1
	}
	firstSeen := previous.FirstSeenTradeDate
	if firstSeen == "" {
		firstSeen = previousTradeDate
	}
	if firstSeen == "" {
		firstSeen = tradeDate
	}
	streak := max(previous.Streak, 1)
	if previousTradeDate != tradeDate {
		streak++
	}
	return firstSeen, streak
}

func opportunityMarketSectorRepresentatives(items []opportunityMarketScanRawMetric) ([]string, []string) {
	if len(items) == 0 {
		return nil, nil
	}
	ordered := append([]opportunityMarketScanRawMetric(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i].PrefilterScore - math.Max(0, pctReturn(ordered[i].Close, ordered[i].Close5)-8)*4
		right := ordered[j].PrefilterScore - math.Max(0, pctReturn(ordered[j].Close, ordered[j].Close5)-8)*4
		if left != right {
			return left > right
		}
		return ordered[i].Instrument.Symbol < ordered[j].Instrument.Symbol
	})
	symbols := make([]string, 0, opportunityMarketScanSectorRepresentativeLimit)
	names := make([]string, 0, opportunityMarketScanSectorRepresentativeLimit)
	for _, item := range ordered[:min(len(ordered), opportunityMarketScanSectorRepresentativeLimit)] {
		symbols = append(symbols, item.Instrument.Symbol)
		names = append(names, item.Instrument.Name)
	}
	return symbols, names
}

func opportunityMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

func opportunityMarketSectorStatePriority(state string) int {
	switch state {
	case OpportunityMarketSectorStateEmerging:
		return 5
	case OpportunityMarketSectorStateConfirmed:
		return 4
	case OpportunityMarketSectorStateOverheated:
		return 3
	case OpportunityMarketSectorStateFading:
		return 2
	case OpportunityMarketSectorStateInvalidated:
		return 1
	default:
		return 0
	}
}

func opportunityMarketSectorAdmissionSymbols(snapshot OpportunityMarketSectorSnapshot) map[string]struct{} {
	out := make(map[string]struct{}, opportunityMarketScanSectorLocalReserve)
	sectors := 0
	for _, trend := range snapshot.Trends {
		if trend.State != OpportunityMarketSectorStateEmerging && trend.State != OpportunityMarketSectorStateConfirmed {
			continue
		}
		sectors++
		for _, symbol := range trend.RepresentativeSymbols {
			if len(out) >= opportunityMarketScanSectorLocalReserve {
				return out
			}
			out[symbol] = struct{}{}
		}
		if sectors >= opportunityMarketScanSectorLimit {
			break
		}
	}
	return out
}

func opportunityMarketSourceLane(admission opportunityMarketAdmission) string {
	count := 0
	if admission.price {
		count++
	}
	if admission.sector {
		count++
	}
	if admission.message {
		count++
	}
	if count > 1 {
		return OpportunityMarketScanSourceMixed
	}
	if admission.sector {
		return OpportunityMarketScanSourceSector
	}
	if admission.message {
		return OpportunityMarketScanSourceMessage
	}
	return OpportunityMarketScanSourcePrice
}

func opportunityMarketAdmissionReasons(admission opportunityMarketAdmission) []string {
	var out []string
	if admission.price {
		out = append(out, OpportunityMarketScanSourcePrice)
	}
	if admission.sector {
		out = append(out, OpportunityMarketScanSourceSector)
	}
	if admission.message {
		out = append(out, OpportunityMarketScanSourceMessage)
	}
	return out
}
