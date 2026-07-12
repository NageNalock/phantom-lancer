package stockv2

import (
	"sort"
	"strings"
	"time"
)

type dailyBarMissingRange struct {
	Start string
	End   string
}

// dailyBarCoveragePlan separates a missing trading date from a stored row whose
// required fields are incomplete. Historical K sources repair DateGaps and
// CoreFacetGaps; the rate-limited fund-flow source repairs FlowFacetGaps only.
type dailyBarCoveragePlan struct {
	DateGaps      []dailyBarMissingRange
	CoreFacetGaps []dailyBarMissingRange
	FlowFacetGaps []dailyBarMissingRange
}

type dailyBarSourceRangeObservation struct {
	Source        string
	Range         dailyBarMissingRange
	Succeeded     bool
	ReturnedDates []string
}

// dailyBarNoTradeCoverage is evidence that two independent successful source
// checks returned no bar for a trading-date range. It is not a synthetic bar.
type dailyBarNoTradeCoverage struct {
	Range     dailyBarMissingRange
	Sources   []string
	CheckedAt time.Time
}

func planDailyBarCoverageWithCalendar(
	bars []StockV2DailyBar,
	tradingDates []string,
	verifiedNoTrade []dailyBarMissingRange,
	instrumentType string,
	startDate string,
	endDate string,
) dailyBarCoveragePlan {
	calendar := dailyBarDatesInWindow(tradingDates, startDate, endDate)
	if len(calendar) == 0 {
		return dailyBarCoveragePlan{}
	}

	barsByDate := make(map[string][]StockV2DailyBar, len(bars))
	for _, bar := range bars {
		if bar.TradeDate < startDate || bar.TradeDate > endDate {
			continue
		}
		barsByDate[bar.TradeDate] = append(barsByDate[bar.TradeDate], bar)
	}

	dateMissing := make(map[string]struct{})
	coreMissing := make(map[string]struct{})
	flowMissing := make(map[string]struct{})
	requireFlow := normalizeInstrumentType(instrumentType) == InstrumentTypeStock
	for _, tradeDate := range calendar {
		if dailyBarDateInRanges(tradeDate, verifiedNoTrade) {
			continue
		}
		rows := barsByDate[tradeDate]
		if len(rows) == 0 {
			dateMissing[tradeDate] = struct{}{}
			continue
		}
		coreComplete := false
		analysisComplete := false
		for _, bar := range rows {
			if dailyBarCoreFacetsComplete(bar) {
				coreComplete = true
			}
			if dailyBarAnalysisFacetsComplete(bar, instrumentType) {
				analysisComplete = true
			}
		}
		if !coreComplete {
			coreMissing[tradeDate] = struct{}{}
			continue
		}
		if requireFlow && !analysisComplete {
			flowMissing[tradeDate] = struct{}{}
		}
	}

	return dailyBarCoveragePlan{
		DateGaps:      dailyBarRangesForDateSet(calendar, dateMissing),
		CoreFacetGaps: dailyBarRangesForDateSet(calendar, coreMissing),
		FlowFacetGaps: dailyBarRangesForDateSet(calendar, flowMissing),
	}
}

func (p dailyBarCoveragePlan) historicalKFetchRanges(tradingDates []string) []dailyBarMissingRange {
	return mergeDailyBarRangesByTradingCalendar(
		append(append([]dailyBarMissingRange(nil), p.DateGaps...), p.CoreFacetGaps...),
		tradingDates,
	)
}

func mergeDailyBarRangesByTradingCalendar(ranges []dailyBarMissingRange, tradingDates []string) []dailyBarMissingRange {
	calendar := dailyBarDatesInWindow(tradingDates, "", "")
	selected := make(map[string]struct{})
	for _, tradeDate := range calendar {
		if dailyBarDateInRanges(tradeDate, ranges) {
			selected[tradeDate] = struct{}{}
		}
	}
	return dailyBarRangesForDateSet(calendar, selected)
}

func verifyDailyBarNoTradeCoverage(
	tradingDates []string,
	observations []dailyBarSourceRangeObservation,
	requestedRanges []dailyBarMissingRange,
	now time.Time,
) []dailyBarNoTradeCoverage {
	calendar := dailyBarDatesInWindow(tradingDates, "", "")
	cutoff := now.In(chinaMarketTZ).AddDate(0, 0, -3).Format("2006-01-02")
	type verifiedDate struct {
		date    string
		sources []string
	}
	verified := make([]verifiedDate, 0, len(calendar))
	for _, tradeDate := range calendar {
		if tradeDate > cutoff || !dailyBarDateInRanges(tradeDate, requestedRanges) {
			continue
		}
		sourceSet := make(map[string]struct{})
		for _, observation := range observations {
			source := strings.TrimSpace(observation.Source)
			if !observation.Succeeded || source == "" ||
				tradeDate < observation.Range.Start || tradeDate > observation.Range.End ||
				dailyBarDateInList(tradeDate, observation.ReturnedDates) {
				continue
			}
			sourceSet[source] = struct{}{}
		}
		if len(sourceSet) < 2 {
			continue
		}
		sources := make([]string, 0, len(sourceSet))
		for source := range sourceSet {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		verified = append(verified, verifiedDate{date: tradeDate, sources: sources})
	}

	out := make([]dailyBarNoTradeCoverage, 0, len(verified))
	for _, item := range verified {
		if len(out) > 0 &&
			dailyBarStringSlicesEqual(out[len(out)-1].Sources, item.sources) &&
			dailyBarDatesAdjacentInCalendar(out[len(out)-1].Range.End, item.date, calendar) {
			out[len(out)-1].Range.End = item.date
			continue
		}
		out = append(out, dailyBarNoTradeCoverage{
			Range:     dailyBarMissingRange{Start: item.date, End: item.date},
			Sources:   item.sources,
			CheckedAt: now,
		})
	}
	return out
}

func dailyBarRequestedTradingDates(tradingDates []string, ranges []dailyBarMissingRange) []string {
	dates := dailyBarDatesInWindow(tradingDates, "", "")
	out := make([]string, 0, len(dates))
	for _, tradeDate := range dates {
		if dailyBarDateInRanges(tradeDate, ranges) {
			out = append(out, tradeDate)
		}
	}
	return out
}

func dailyBarDatesInWindow(dates []string, startDate, endDate string) []string {
	seen := make(map[string]struct{}, len(dates))
	out := make([]string, 0, len(dates))
	for _, tradeDate := range dates {
		tradeDate = strings.TrimSpace(tradeDate)
		if _, err := time.Parse("2006-01-02", tradeDate); err != nil ||
			(startDate != "" && tradeDate < startDate) ||
			(endDate != "" && tradeDate > endDate) {
			continue
		}
		if _, ok := seen[tradeDate]; ok {
			continue
		}
		seen[tradeDate] = struct{}{}
		out = append(out, tradeDate)
	}
	sort.Strings(out)
	return out
}

func dailyBarRangesForDateSet(calendar []string, selected map[string]struct{}) []dailyBarMissingRange {
	var out []dailyBarMissingRange
	start := ""
	end := ""
	for _, tradeDate := range calendar {
		if _, ok := selected[tradeDate]; !ok {
			if start != "" {
				out = append(out, dailyBarMissingRange{Start: start, End: end})
				start, end = "", ""
			}
			continue
		}
		if start == "" {
			start = tradeDate
		}
		end = tradeDate
	}
	if start != "" {
		out = append(out, dailyBarMissingRange{Start: start, End: end})
	}
	return out
}

func dailyBarDateInRanges(tradeDate string, ranges []dailyBarMissingRange) bool {
	for _, item := range ranges {
		if item.Start <= tradeDate && tradeDate <= item.End {
			return true
		}
	}
	return false
}

func dailyBarDateInList(tradeDate string, dates []string) bool {
	for _, item := range dates {
		if strings.TrimSpace(item) == tradeDate {
			return true
		}
	}
	return false
}

func dailyBarStringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizeDailyBarEvidenceSources(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func dailyBarDatesAdjacentInCalendar(left, right string, calendar []string) bool {
	for index := 1; index < len(calendar); index++ {
		if calendar[index-1] == left && calendar[index] == right {
			return true
		}
	}
	return false
}

func clampDailyBarRangeToInstrument(inst StockV2Instrument, startDate, endDate string) (string, string) {
	if listDate := normalizedInstrumentBoundaryDate(inst.ListDate); listDate != "" && (startDate == "" || listDate > startDate) {
		startDate = listDate
	}
	if delistDate := normalizedInstrumentBoundaryDate(inst.DelistDate); delistDate != "" && (endDate == "" || delistDate < endDate) {
		endDate = delistDate
	}
	if startDate == "" || endDate == "" || startDate > endDate {
		return "", ""
	}
	return startDate, endDate
}

func normalizedInstrumentBoundaryDate(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return ""
}

func planDailyBarMissingRanges(dates []string, targetStart, targetEnd time.Time) []dailyBarMissingRange {
	start := normalizeDateOnly(targetStart)
	end := normalizeDateOnly(targetEnd)
	if end.Before(start) {
		return nil
	}
	if len(dates) == 0 {
		return []dailyBarMissingRange{{Start: dateString(start), End: dateString(end)}}
	}
	parsed := make([]time.Time, 0, len(dates))
	for _, raw := range dates {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			continue
		}
		t = normalizeDateOnly(t)
		if t.Before(start) || t.After(end) {
			continue
		}
		if len(parsed) == 0 || !parsed[len(parsed)-1].Equal(t) {
			parsed = append(parsed, t)
		}
	}
	if len(parsed) == 0 {
		return []dailyBarMissingRange{{Start: dateString(start), End: dateString(end)}}
	}
	var ranges []dailyBarMissingRange
	if parsed[0].After(start) {
		ranges = append(ranges, dailyBarMissingRange{Start: dateString(start), End: dateString(parsed[0].AddDate(0, 0, -1))})
	}
	// ponytail: without an exchange trading calendar, natural-day middle gaps cannot be
	// distinguished from statutory market closures. Only repair leading/tail gaps here;
	// add a persisted exchange calendar before restoring middle-gap maintenance.
	latest := parsed[len(parsed)-1]
	if latest.Before(end) {
		ranges = append(ranges, dailyBarMissingRange{Start: dateString(latest.AddDate(0, 0, 1)), End: dateString(end)})
	}
	return compactDailyBarRanges(ranges)
}

func planDailyBarMissingRangesWithCalendar(dates, tradingDates []string, targetStart, targetEnd time.Time) []dailyBarMissingRange {
	start := dateString(targetStart)
	targetEndDate := dateString(targetEnd)
	if targetEndDate < start || len(tradingDates) == 0 {
		return planDailyBarMissingRanges(dates, targetStart, targetEnd)
	}
	calendar := make([]string, 0, len(tradingDates))
	seenCalendar := make(map[string]struct{}, len(tradingDates))
	for _, tradeDate := range tradingDates {
		if tradeDate < start || tradeDate > targetEndDate {
			continue
		}
		if _, ok := seenCalendar[tradeDate]; ok {
			continue
		}
		seenCalendar[tradeDate] = struct{}{}
		calendar = append(calendar, tradeDate)
	}
	if len(calendar) == 0 {
		return planDailyBarMissingRanges(dates, targetStart, targetEnd)
	}
	sort.Strings(calendar)
	effectiveStart := targetStart
	effectiveEnd := targetEnd
	calendarStart, startErr := time.Parse("2006-01-02", calendar[0])
	calendarEnd, err := time.Parse("2006-01-02", calendar[len(calendar)-1])
	if len(calendar) >= 5 && startErr == nil {
		normalizedTargetStart := normalizeDateOnly(targetStart)
		calendarStart = normalizeDateOnly(calendarStart)
		if !calendarStart.Before(normalizedTargetStart) && calendarStart.Sub(normalizedTargetStart) <= 14*24*time.Hour {
			effectiveStart = calendarStart
		}
	}
	if len(calendar) >= 5 && err == nil {
		normalizedTargetEnd := normalizeDateOnly(targetEnd)
		calendarEnd = normalizeDateOnly(calendarEnd)
		if !calendarEnd.After(normalizedTargetEnd) && normalizedTargetEnd.Sub(calendarEnd) <= 14*24*time.Hour {
			effectiveEnd = calendarEnd
		}
	}
	start = dateString(effectiveStart)
	end := dateString(effectiveEnd)
	// Keep the bootstrap envelope even when a fresh symbol only has today's
	// batch quote. The observed calendar caps holiday tails and classifies only
	// the gaps between the symbol's existing boundary dates.
	ranges := planDailyBarMissingRanges(dates, effectiveStart, effectiveEnd)
	existing := make(map[string]struct{}, len(dates))
	orderedExisting := make([]string, 0, len(dates))
	for _, tradeDate := range dates {
		if tradeDate >= start && tradeDate <= end {
			if _, ok := existing[tradeDate]; ok {
				continue
			}
			existing[tradeDate] = struct{}{}
			orderedExisting = append(orderedExisting, tradeDate)
		}
	}
	if len(orderedExisting) < 2 {
		return ranges
	}
	sort.Strings(orderedExisting)
	earliest := orderedExisting[0]
	latest := orderedExisting[len(orderedExisting)-1]
	expected := make([]string, 0, len(calendar))
	for _, tradeDate := range calendar {
		if tradeDate <= earliest || tradeDate >= latest {
			continue
		}
		expected = append(expected, tradeDate)
	}
	if len(expected) == 0 {
		return ranges
	}
	sort.Strings(expected)

	missingStart := ""
	lastMissing := ""
	for _, tradeDate := range expected {
		if _, ok := existing[tradeDate]; ok {
			if missingStart != "" {
				ranges = append(ranges, dailyBarMissingRange{Start: missingStart, End: lastMissing})
				missingStart = ""
				lastMissing = ""
			}
			continue
		}
		if missingStart == "" {
			missingStart = tradeDate
		}
		lastMissing = tradeDate
	}
	if missingStart != "" {
		ranges = append(ranges, dailyBarMissingRange{Start: missingStart, End: lastMissing})
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start == ranges[j].Start {
			return ranges[i].End < ranges[j].End
		}
		return ranges[i].Start < ranges[j].Start
	})
	return compactDailyBarRanges(ranges)
}

func compactDailyBarRanges(ranges []dailyBarMissingRange) []dailyBarMissingRange {
	out := ranges[:0]
	for _, r := range ranges {
		if r.Start == "" || r.End == "" || r.Start > r.End {
			continue
		}
		out = append(out, r)
	}
	return out
}

func subtractCheckedDailyBarRanges(ranges, checked []dailyBarMissingRange) []dailyBarMissingRange {
	remaining := append([]dailyBarMissingRange(nil), ranges...)
	for _, covered := range checked {
		if covered.Start == "" || covered.End == "" || covered.Start > covered.End {
			continue
		}
		next := make([]dailyBarMissingRange, 0, len(remaining)+1)
		for _, item := range remaining {
			if covered.End < item.Start || covered.Start > item.End {
				next = append(next, item)
				continue
			}
			if covered.Start > item.Start {
				if end := shiftDailyBarDate(covered.Start, -1); end >= item.Start {
					next = append(next, dailyBarMissingRange{Start: item.Start, End: end})
				}
			}
			if covered.End < item.End {
				if start := shiftDailyBarDate(covered.End, 1); start <= item.End {
					next = append(next, dailyBarMissingRange{Start: start, End: item.End})
				}
			}
		}
		remaining = next
	}
	return compactDailyBarRanges(remaining)
}

func dailyBarRangesWithoutReturnedBars(ranges []dailyBarMissingRange, bars []StockV2DailyBar) []dailyBarMissingRange {
	covered := make([]dailyBarMissingRange, 0, len(bars))
	for _, bar := range bars {
		if bar.TradeDate != "" {
			covered = append(covered, dailyBarMissingRange{Start: bar.TradeDate, End: bar.TradeDate})
		}
	}
	return subtractCheckedDailyBarRanges(ranges, covered)
}

func stableDailyBarGapChecks(ranges []dailyBarMissingRange, now time.Time) []dailyBarMissingRange {
	// ponytail: providers can publish or correct very recent bars late. Negative
	// coverage starts only through T-3 and expires in the repository after 30
	// days; newer misses remain retryable on every maintenance pass.
	cutoff := now.In(chinaMarketTZ).AddDate(0, 0, -3).Format("2006-01-02")
	out := make([]dailyBarMissingRange, 0, len(ranges))
	for _, item := range ranges {
		if item.Start > cutoff {
			continue
		}
		if item.End > cutoff {
			item.End = cutoff
		}
		out = append(out, item)
	}
	return compactDailyBarRanges(out)
}

func shiftDailyBarDate(raw string, days int) string {
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return raw
	}
	return parsed.AddDate(0, 0, days).Format("2006-01-02")
}

// filterBarsByRanges 只保留 tradeDate 落在任一缺口区间内的 bars，避免不必要的全量 upsert。
func filterBarsByRanges(bars []StockV2DailyBar, ranges []dailyBarMissingRange) []StockV2DailyBar {
	if len(bars) == 0 || len(ranges) == 0 {
		return nil
	}
	out := make([]StockV2DailyBar, 0, len(bars))
	for _, bar := range bars {
		date := bar.TradeDate
		if date == "" {
			continue
		}
		for _, r := range ranges {
			if date >= r.Start && date <= r.End {
				out = append(out, bar)
				break
			}
		}
	}
	return out
}

func normalizeDateOnly(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func dateString(t time.Time) string {
	return normalizeDateOnly(t).Format("2006-01-02")
}
