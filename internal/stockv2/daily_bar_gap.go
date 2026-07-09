package stockv2

import "time"

type dailyBarMissingRange struct {
	Start string
	End   string
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
	for i := 1; i < len(parsed); i++ {
		prev := parsed[i-1]
		cur := parsed[i]
		// ponytail: without a trading calendar, treat natural-day gaps above a long weekend
		// as obvious missing spans; swap to exchange calendars if false positives matter.
		if cur.Sub(prev) > 4*24*time.Hour {
			ranges = append(ranges, dailyBarMissingRange{Start: dateString(prev.AddDate(0, 0, 1)), End: dateString(cur.AddDate(0, 0, -1))})
		}
	}
	latest := parsed[len(parsed)-1]
	if latest.Before(end) {
		ranges = append(ranges, dailyBarMissingRange{Start: dateString(latest.AddDate(0, 0, 1)), End: dateString(end)})
	}
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

// filterBarsByRanges 只保留 tradeDate 落在任一缺口区间内的 bars。
// 用于百度全量抓取后按缺口过滤，避免不必要的全量 upsert。
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
