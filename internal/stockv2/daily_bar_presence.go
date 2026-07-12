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
	return (bar.AmountPresent || bar.Amount != 0) &&
		!math.IsNaN(bar.Amount) && !math.IsInf(bar.Amount, 0)
}

func dailyBarTurnoverPresent(bar StockV2DailyBar) bool {
	return (bar.TurnoverRatePresent || bar.TurnoverRate != 0) &&
		bar.TurnoverRate >= 0 && !math.IsNaN(bar.TurnoverRate) && !math.IsInf(bar.TurnoverRate, 0)
}

func dailyBarNetInflowPresent(bar StockV2DailyBar) bool {
	return (bar.NetInflowPresent || bar.NetInflow != 0) &&
		!math.IsNaN(bar.NetInflow) && !math.IsInf(bar.NetInflow, 0)
}

func dailyBarMainNetInflowPresent(bar StockV2DailyBar) bool {
	return (bar.MainNetInflowPresent || bar.MainNetInflow != 0) &&
		!math.IsNaN(bar.MainNetInflow) && !math.IsInf(bar.MainNetInflow, 0)
}

func dailyBarOHLCVComplete(bar StockV2DailyBar) bool {
	prices := []float64{bar.Open, bar.High, bar.Low, bar.Close}
	for _, value := range prices {
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	if bar.Volume <= 0 || math.IsNaN(bar.Volume) || math.IsInf(bar.Volume, 0) {
		return false
	}
	return bar.High >= max(bar.Open, bar.Close, bar.Low) &&
		bar.Low <= min(bar.Open, bar.Close, bar.High)
}

func dailyBarFacetsComplete(bar StockV2DailyBar) bool {
	return dailyBarCoreFacetsComplete(bar) &&
		dailyBarFlowFacetsComplete(bar)
}

func dailyBarCoreFacetsComplete(bar StockV2DailyBar) bool {
	return dailyBarOHLCVComplete(bar) &&
		dailyBarAmountPresent(bar) &&
		dailyBarTurnoverPresent(bar)
}

func dailyBarFlowFacetsComplete(bar StockV2DailyBar) bool {
	// ponytail: public sources provide net flow by order size, not reliable gross
	// buy/sell amounts. Keep flow as a separate facet and never infer gross values.
	return dailyBarNetInflowPresent(bar) && dailyBarMainNetInflowPresent(bar)
}

func dailyBarAnalysisFacetsComplete(bar StockV2DailyBar, instrumentType string) bool {
	if !dailyBarCoreFacetsComplete(bar) {
		return false
	}
	return normalizeInstrumentType(instrumentType) != InstrumentTypeStock || dailyBarFlowFacetsComplete(bar)
}
