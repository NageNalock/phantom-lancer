package stockv2

import (
	"math"
	"strconv"
	"strings"
)

func parseDailyBarFloatWithPresence(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	switch strings.ToLower(raw) {
	case "", "-", "--", "—", "null", "nil", "n/a":
		return 0, false
	}
	raw = strings.TrimPrefix(raw, "+")
	raw = strings.TrimSuffix(raw, "%")
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func dailyBarAmountPresent(bar StockV2DailyBar) bool {
	return bar.AmountPresent || bar.Amount != 0
}

func dailyBarTurnoverPresent(bar StockV2DailyBar) bool {
	return bar.TurnoverRatePresent || bar.TurnoverRate != 0
}

func dailyBarNetInflowPresent(bar StockV2DailyBar) bool {
	return bar.NetInflowPresent || bar.NetInflow != 0
}

func dailyBarMainNetInflowPresent(bar StockV2DailyBar) bool {
	return bar.MainNetInflowPresent || bar.MainNetInflow != 0
}

func dailyBarFacetsComplete(bar StockV2DailyBar) bool {
	return dailyBarCoreFacetsComplete(bar) &&
		dailyBarNetInflowPresent(bar) &&
		dailyBarMainNetInflowPresent(bar)
}

func dailyBarCoreFacetsComplete(bar StockV2DailyBar) bool {
	return dailyBarAmountPresent(bar) && dailyBarTurnoverPresent(bar)
}
